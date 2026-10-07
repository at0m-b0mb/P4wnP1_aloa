//go:build linux
// +build linux

package service

import "testing"

func TestIsCompromisedPSK(t *testing.T) {
	bad := []string{
		"MaMe82-P4wnP1",      // upstream default, shipped in dist/db
		"mame82-p4wnp1",      // case must not be an escape hatch
		"  MaMe82-P4wnP1  ",  // nor surrounding whitespace
		"HackProKP-changeme", // this fork's placeholder
		"p4wnp1", "toor", "changeme",
	}
	for _, p := range bad {
		if !isCompromisedPSK(p) {
			t.Errorf("isCompromisedPSK(%q) = false; this value is public", p)
		}
	}
	good := []string{"", "a genuinely chosen passphrase", "MaMe82-P4wnP2", "correct horse battery staple"}
	for _, p := range good {
		if isCompromisedPSK(p) {
			t.Errorf("isCompromisedPSK(%q) = true; it should be allowed", p)
		}
	}
}

func TestGeneratePSKIsValidAndUnique(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		p, err := generatePSK()
		if err != nil {
			t.Fatalf("generatePSK: %v", err)
		}
		// WPA2 requires 8..63 printable ASCII.
		if len(p) < 8 || len(p) > 63 {
			t.Fatalf("PSK length %d is outside the WPA2 range: %q", len(p), p)
		}
		for _, r := range p {
			if r < 0x20 || r > 0x7e {
				t.Fatalf("PSK contains a non-printable character: %q", p)
			}
		}
		// Characters that get mangled when retyped or passed through a shell.
		for _, bad := range []rune{'/', '+', '='} {
			for _, r := range p {
				if r == bad {
					t.Fatalf("PSK contains %q, which is excluded on purpose: %q", bad, p)
				}
			}
		}
		if seen[p] {
			t.Fatalf("generatePSK produced a duplicate within 200 draws: %q", p)
		}
		seen[p] = true
		// A generated PSK must never itself be on the blocklist.
		if isCompromisedPSK(p) {
			t.Fatalf("generated PSK is on the blocklist: %q", p)
		}
	}
}

func TestGuardSubstitutesOnlyCompromisedPSKs(t *testing.T) {
	t.Run("leaves a chosen PSK alone", func(t *testing.T) {
		v := "a genuinely chosen passphrase"
		view := &WiFiSettingsPSKView{Get: func() string { return v }, Set: func(s string) { v = s }}
		if guardAccessPointPSK(view) {
			t.Error("guard substituted a PSK it should not have touched")
		}
		if v != "a genuinely chosen passphrase" {
			t.Errorf("PSK was modified to %q", v)
		}
	})

	t.Run("leaves an empty PSK alone", func(t *testing.T) {
		v := ""
		view := &WiFiSettingsPSKView{Get: func() string { return v }, Set: func(s string) { v = s }}
		if guardAccessPointPSK(view) {
			t.Error("guard acted on an empty PSK (an open network is a separate decision)")
		}
	})

	t.Run("nil view is safe", func(t *testing.T) {
		if guardAccessPointPSK(nil) {
			t.Error("guard acted on a nil view")
		}
	})
}
