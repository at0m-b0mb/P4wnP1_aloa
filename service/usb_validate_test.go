//go:build linux
// +build linux

package service

import (
	"strings"
	"testing"

	pb "github.com/mame82/P4wnP1_aloa/proto"
)

// ValidateGadgetSetting used to dereference the per-function settings
// messages without checking them, so enabling RNDIS, CDC ECM or UMS without
// supplying the matching sub-message was a nil pointer panic.
//
// It was caught on real hardware, by a test script that built its request
// from scratch instead of reading the deployed settings first. Every
// composition it tried came back "internal error; the service survived and
// the details are in the journal" -- which is true, and useless. The panic
// happened during validation, so it pre-empted the endpoint-budget check
// below it: a request that should have been cleanly refused for consuming 8
// of 7 endpoints crashed instead, and the script scored that crash as a
// correct refusal. A bug that makes a test pass is the expensive kind.
//
// The web console never hit it because it reads the deployed object and
// sends the whole thing back. Everything that builds a request from scratch
// -- a script, the API, a hand-written stored template -- does hit it.
func validSettings() *pb.GadgetSettings {
	return &pb.GadgetSettings{
		Enabled: true, Vid: "0x1d6b", Pid: "0x0104",
		Manufacturer: "MaMe82", Product: "P4wnP1", Serial: "deadbeef1337",
		Use_RNDIS:      true,
		RndisSettings:  &pb.GadgetSettingsEthernet{DevAddr: "42:63:65:12:34:56", HostAddr: "42:63:65:56:34:12"},
		Use_CDC_ECM:    true,
		CdcEcmSettings: &pb.GadgetSettingsEthernet{DevAddr: "42:63:66:12:34:56", HostAddr: "42:63:66:56:34:12"},
	}
}

func TestValidateGadgetSettingRejectsMissingSubMessages(t *testing.T) {
	for _, c := range []struct {
		name   string
		mangle func(*pb.GadgetSettings)
		want   string
	}{
		{"RNDIS on, no rndis_settings",
			func(g *pb.GadgetSettings) { g.RndisSettings = nil },
			"rndis_settings"},
		{"CDC ECM on, no cdc_ecm_settings",
			func(g *pb.GadgetSettings) { g.CdcEcmSettings = nil },
			"cdc_ecm_settings"},
		{"UMS on, no ums_settings",
			func(g *pb.GadgetSettings) { g.Use_UMS = true; g.UmsSettings = nil },
			"ums_settings"},
	} {
		t.Run(c.name, func(t *testing.T) {
			gs := validSettings()
			c.mangle(gs)
			// The point of the test is that this RETURNS rather than panics.
			// Without the guard the call never gets as far as the error check.
			err := ValidateGadgetSetting(gs)
			if err == nil {
				t.Fatalf("missing sub-message was accepted; want an error naming %q", c.want)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error does not name the missing field.\n got: %v\nwant it to mention: %s",
					err, c.want)
			}
		})
	}
}

// The budget check sits AFTER the MAC validation, so a crash above it hid
// the refusal entirely. This pins the ordering down: a request that is both
// over budget and otherwise valid must be refused FOR THE BUDGET, with a
// message that says so, because that is what the operator has to act on.
func TestValidateGadgetSettingRefusesOverBudgetWithAUsefulReason(t *testing.T) {
	gs := validSettings() // RNDIS 2 + CDC ECM 2 = 4
	gs.Use_SERIAL = true  // +2 = 6
	gs.Use_UMS = true     // +2 = 8, over the 7 available
	gs.UmsSettings = &pb.GadgetSettingsUMS{File: "test.bin"}

	err := ValidateGadgetSetting(gs)
	if err == nil {
		t.Fatal("8 endpoints was accepted; the Pi Zero W has 7")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "endpoint") {
		t.Errorf("the refusal does not mention endpoints, so the operator cannot tell "+
			"what to turn off.\n got: %v", err)
	}
}

// And the legal combinations must still pass, or the guards above would have
// been "fixed" by refusing everything.
func TestValidateGadgetSettingAcceptsCompositionsThatFit(t *testing.T) {
	for _, c := range []struct {
		name  string
		build func() *pb.GadgetSettings
	}{
		{"both networks (4 endpoints)", validSettings},
		{"networks + serial (6)", func() *pb.GadgetSettings {
			g := validSettings()
			g.Use_SERIAL = true
			return g
		}},
		{"networks + mass storage (6)", func() *pb.GadgetSettings {
			g := validSettings()
			g.Use_UMS = true
			g.UmsSettings = &pb.GadgetSettingsUMS{File: "test.bin"}
			return g
		}},
		{"networks + keyboard + mouse (6)", func() *pb.GadgetSettings {
			g := validSettings()
			g.Use_HID_KEYBOARD = true
			g.Use_HID_MOUSE = true
			return g
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			if err := ValidateGadgetSetting(c.build()); err != nil {
				t.Errorf("a composition that fits in 7 endpoints was refused: %v", err)
			}
		})
	}
}
