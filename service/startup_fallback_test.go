//go:build linux
// +build linux

package service

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/mame82/P4wnP1_aloa/service/bluetooth"
)

// THE BUG THIS FILE EXISTS FOR
//
// The boot path fell back to the hardcoded servicestart.sh on ANY error from
// the startup master template. DeployMasterTemplate is best-effort per
// subsystem and returns a combined error when any one of them fails, so a
// single failing subsystem triggered a script that re-deploys USB, networking
// and the WiFi access point from values hardcoded years ago -- undoing every
// subsystem that had just come up correctly.
//
// Observed on a Pi Zero W: the service starts before sysinit.target, BlueZ
// answers DeployStoredBluetoothSettings with a bare "Failed" that early, and
// the device therefore took the fallback on every boot. The access point it
// had correctly brought up from its own database as the shipped 10-byte SSID
// was immediately replaced by the script's 32-byte emoji SSID -- which is the
// one thing that cannot fit a scan-to-join QR, so the OLED card silently
// dropped the join code. The deployed SSID and the stored SSID disagreed and
// nothing said why.
func TestPartialDeployDoesNotTriggerTheDestructiveFallback(t *testing.T) {
	// Exactly what a Zero W produced: everything up, Bluetooth failed.
	partial := &PartialDeployError{
		Failures:  []string{"bluetooth startup: Failed"},
		Succeeded: 6, Attempted: 7,
	}
	if startupNeedsFallback(partial) {
		t.Fatal("a partial deploy still runs servicestart.sh, which overwrites " +
			"the USB, network and WiFi configuration that DID deploy")
	}
}

// THE REGRESSION THE FIRST FIX INTRODUCED
//
// startupNeedsFallback originally decided by error TYPE: any
// *PartialDeployError meant "something is up, keep it". But that error is
// built whenever len(failures) > 0, so an all-subsystems-failed deploy was
// the same value as a one-subsystem-failed deploy, and BOTH suppressed the
// fallback. A device that deployed nothing got no fallback either -- no USB
// gadget, no DHCP, no access point, nothing retrying. Recovery meant pulling
// the SD card. Before the fix, v0.7.1 recovered from this state by running
// servicestart.sh, which is exactly what its own header says it is for.
//
// Reachable on this appliance: datastore.Restore writes keys one at a time in
// sorted order and runs only when the DB directory does not yet exist, so
// pulling the power mid-first-boot -- the normal way this device is switched
// off -- permanently keeps master_startup and tas_startup while losing
// usbs_startup and ws_startup, which sort after them.
func TestNothingDeployedStillRunsTheFallback(t *testing.T) {
	// Every hardware subsystem failed; only the reflex set, which touches no
	// hardware, would have succeeded -- and here not even that.
	allFailed := &PartialDeployError{
		Failures: []string{
			"USB usbs_startup: ErrGet",
			"reflexes tas_startup: ErrGet",
			"network usbeth_startup: no such interface",
			"network wlan0_startup_dhcp_server: no such interface",
			"bluetooth bt_startup: Failed",
			"wifi ws_startup: ErrGet",
		},
		Succeeded: 0, Attempted: 6,
	}
	if !startupNeedsFallback(allFailed) {
		t.Fatal("NOTHING deployed and the fallback was suppressed: no USB gadget, " +
			"no DHCP, no access point, and nothing retries. The device is " +
			"unreachable and recovery means pulling the SD card.")
	}
	if !allFailed.NothingDeployed() {
		t.Error("NothingDeployed() is false with Succeeded == 0")
	}
}

// The boundary. One subsystem up is still "something to protect", so the
// fallback must stay suppressed -- otherwise this fix would undo the one
// before it and the emoji SSID would come back.
func TestExactlyOneSubsystemUpSuppressesTheFallback(t *testing.T) {
	oneUp := &PartialDeployError{
		Failures:  []string{"bluetooth bt_startup: Failed", "wifi ws_startup: ErrGet"},
		Succeeded: 1, Attempted: 3,
	}
	if startupNeedsFallback(oneUp) {
		t.Error("one subsystem up still runs the fallback, which would overwrite it")
	}
}

// The error message is the only thing an operator sees in the journal when a
// subsystem silently does not come up, so it must carry the counts that drove
// the decision -- not just the failures.
func TestPartialDeployErrorStatesTheCounts(t *testing.T) {
	e := &PartialDeployError{
		Failures:  []string{"bluetooth bt_startup: Failed"},
		Succeeded: 6, Attempted: 7,
	}
	msg := e.Error()
	for _, want := range []string{"6 of 7", "bluetooth", "partially deployed"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error message %q does not mention %q", msg, want)
		}
	}
}

// Wrapped, because the error crosses a gRPC boundary and may be annotated on
// the way. errors.As is used for exactly this reason; a type assertion would
// have silently reverted to the old behaviour.
func TestPartialDeployIsRecognisedThroughWrapping(t *testing.T) {
	wrapped := fmt.Errorf("deploying startup template: %w",
		&PartialDeployError{
			Failures:  []string{"wifi startup: no wlan0"},
			Succeeded: 6, Attempted: 7,
		})
	if startupNeedsFallback(wrapped) {
		t.Error("a wrapped PartialDeployError was not recognised; the fallback " +
			"would run and overwrite what deployed")
	}
}

// The fallback MUST still run for a device that deployed nothing. That is the
// whole reason the script exists, and a fix that disabled it would leave an
// unreachable appliance -- a worse failure than the one being fixed.
func TestTotalFailureStillRunsTheFallback(t *testing.T) {
	for _, e := range []error{
		errors.New("datastore: key not found"),
		errors.New("no such master template \"startup\""),
		fmt.Errorf("opening store: %w", errors.New("permission denied")),
	} {
		if !startupNeedsFallback(e) {
			t.Errorf("a device that deployed NOTHING (%v) would get no fallback "+
				"and never become reachable", e)
		}
	}
}

func TestSuccessNeedsNoFallback(t *testing.T) {
	if startupNeedsFallback(nil) {
		t.Error("a clean deploy must not run the fallback script")
	}
}

// PartialDeployError must name the subsystems, because that message is the
// only thing an operator sees when a subsystem silently does not come up.
func TestPartialDeployErrorNamesWhatFailed(t *testing.T) {
	e := &PartialDeployError{
		Failures:  []string{"bluetooth startup: Failed", "wifi startup: no wlan0"},
		Succeeded: 5, Attempted: 7,
	}
	msg := e.Error()
	for _, want := range []string{"bluetooth", "wifi", "no wlan0", "partially deployed"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error message %q does not mention %q", msg, want)
		}
	}
}

// THE BUG THAT MADE THE FIRST TWO FIXES INERT
//
// The Bluetooth step returns "no controller on this board" as a SKIP, and
// `try` counted every nil return as a success. On a Pi Zero W the controller
// is never up when the service starts -- it runs before sysinit.target -- so
// Succeeded was at least 1 on every boot of the shipped image and
// NothingDeployed() could never become true. The fallback restored in v0.7.3
// could not fire on the only hardware it ships for.
//
// It printed the contradiction in the journal, two lines apart, and nobody
// read it:
//
//	master template: no Bluetooth on this board, skipping
//	master template: bluetooth startup ok
func TestASkippedSubsystemIsNotASuccess(t *testing.T) {
	// Exactly the shipped-device disaster case: the datastore lost the USB,
	// WiFi and reflex templates, every hardware subsystem failed, and
	// Bluetooth was skipped because the board has no controller up yet.
	e := &PartialDeployError{
		Failures: []string{
			"USB usbs_startup: ErrGet",
			"reflexes tas_startup: ErrGet",
			"wifi ws_startup: ErrGet",
			"network usbeth_startup: no such interface",
		},
		Succeeded: 0, Skipped: 1, Attempted: 5,
	}
	if !e.NothingDeployed() {
		t.Fatal("a skipped subsystem is being counted as deployed")
	}
	if !startupNeedsFallback(e) {
		t.Fatal("NOTHING deployed and the fallback is still suppressed: no USB " +
			"gadget, no DHCP, no access point, nothing retrying. This is the " +
			"state the fallback exists for, and it is the state a Pi Zero W " +
			"reaches, because its Bluetooth is always skipped.")
	}
}

// And the message must not claim a skipped subsystem came up.
func TestPartialDeployErrorDistinguishesSkippedFromUp(t *testing.T) {
	e := &PartialDeployError{
		Failures:  []string{"wifi ws_startup: ErrGet"},
		Succeeded: 4, Skipped: 1, Attempted: 6,
	}
	msg := e.Error()
	if !strings.Contains(msg, "4 of 6") {
		t.Errorf("message does not state the real count: %q", msg)
	}
	if !strings.Contains(msg, "1 skipped") {
		t.Errorf("message hides the skipped subsystem, so the journal reads as "+
			"though more came up than did: %q", msg)
	}
}

// A skip must still not trigger the fallback on its own -- a board with no
// Bluetooth and everything else up is a healthy device.
func TestSkippedBluetoothAloneDoesNotRunTheFallback(t *testing.T) {
	e := &PartialDeployError{
		Failures: nil, Succeeded: 5, Skipped: 1, Attempted: 6,
	}
	if startupNeedsFallback(e) {
		t.Error("a healthy board with no Bluetooth would run the destructive " +
			"fallback and have its working configuration overwritten")
	}
}

// THE MAPPING THAT WAS ACTUALLY WRONG.
//
// TestASkippedSubsystemIsNotASuccess pins what PartialDeployError does with a
// skip count. It does NOT pin where the skip comes from -- I reverted the
// Bluetooth branch to `return nil` and every test stayed green, which is the
// same "gate that cannot catch its own bug" this suite exists to avoid. The
// defect lived in the mapping, so the mapping is what gets pinned here.
func TestBluetoothUnavailableIsSkippedNotSucceeded(t *testing.T) {
	got := classifyDeploy(bluetoothDeployErr(bluetooth.ErrBtSvcNotAvailable))
	if got == outcomeOK {
		t.Fatal("a board with no Bluetooth controller counts as a SUCCESSFUL " +
			"deploy. Succeeded is then never zero on a Pi Zero W, " +
			"NothingDeployed() can never be true, and the fallback that keeps " +
			"an unconfigured device reachable never runs.")
	}
	if got != outcomeSkipped {
		t.Errorf("classified as %v, want outcomeSkipped -- counting it as a "+
			"FAILURE is the opposite mistake: the destructive fallback would "+
			"then run on every boot of a board that is working fine", got)
	}
}

// A real Bluetooth error is still a failure, not a skip. Swallowing those
// would hide a genuinely broken controller behind "not on this board".
func TestARealBluetoothErrorIsStillAFailure(t *testing.T) {
	if got := classifyDeploy(bluetoothDeployErr(errors.New("Failed"))); got != outcomeFailed {
		t.Errorf("a bare BlueZ \"Failed\" classified as %v, want outcomeFailed", got)
	}
	if got := classifyDeploy(bluetoothDeployErr(nil)); got != outcomeOK {
		t.Errorf("a successful Bluetooth deploy classified as %v, want outcomeOK", got)
	}
}

// The three-way classification itself.
func TestClassifyDeploy(t *testing.T) {
	for _, c := range []struct {
		name string
		err  error
		want deployOutcome
	}{
		{"nil is success", nil, outcomeOK},
		{"a plain error is a failure", errors.New("boom"), outcomeFailed},
		{"the skip sentinel is a skip", errSubsystemSkipped, outcomeSkipped},
		{"a WRAPPED skip is still a skip",
			fmt.Errorf("%w: no controller", errSubsystemSkipped), outcomeSkipped},
		{"a wrapped plain error is still a failure",
			fmt.Errorf("deploying: %w", errors.New("boom")), outcomeFailed},
	} {
		if got := classifyDeploy(c.err); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}
