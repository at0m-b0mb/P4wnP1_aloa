//go:build linux
// +build linux

package service

import (
	"errors"
	"fmt"
	"strings"
	"testing"
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
