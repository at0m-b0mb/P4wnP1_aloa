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
	partial := &PartialDeployError{Failures: []string{
		"bluetooth startup: Failed",
	}}
	if startupNeedsFallback(partial) {
		t.Fatal("a partial deploy still runs servicestart.sh, which overwrites " +
			"the USB, network and WiFi configuration that DID deploy")
	}
}

// Wrapped, because the error crosses a gRPC boundary and may be annotated on
// the way. errors.As is used for exactly this reason; a type assertion would
// have silently reverted to the old behaviour.
func TestPartialDeployIsRecognisedThroughWrapping(t *testing.T) {
	wrapped := fmt.Errorf("deploying startup template: %w",
		&PartialDeployError{Failures: []string{"wifi startup: no wlan0"}})
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
	e := &PartialDeployError{Failures: []string{"bluetooth startup: Failed", "wifi startup: no wlan0"}}
	msg := e.Error()
	for _, want := range []string{"bluetooth", "wifi", "no wlan0", "partially deployed"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error message %q does not mention %q", msg, want)
		}
	}
}
