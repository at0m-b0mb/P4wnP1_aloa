//go:build linux
// +build linux

package service

import (
	"strings"
	"testing"

	pb "github.com/mame82/P4wnP1_aloa/proto"
)

// The access point's cipher and authentication choices, pinned.
//
// These are the lines that decide what an attacker in range can do, and
// they are assembled by string concatenation where a one-word change is
// invisible in review. So they are asserted, not trusted.
//
// WPA3 is deliberately absent and cannot be added here: it mandates
// Protected Management Frames, PMF needs the BIP-CMAC-128 cipher, and the
// BCM43430 in a Pi Zero W advertises only WEP40, WEP104, TKIP and
// CCMP-128. hostapd 2.10 on the device does have SAE compiled in -- the
// radio is the limit, not the software. See the README.
func TestHostapdConfigOffersNothingWeak(t *testing.T) {
	for _, tc := range []struct {
		name     string
		settings *pb.WiFiSettings
		want     []string
		reject   []string
	}{
		{
			name: "WPA2 PSK",
			settings: &pb.WiFiSettings{
				WorkingMode: pb.WiFiWorkingMode_AP,
				AuthMode:    pb.WiFiAuthMode_WPA2_PSK,
				Channel:     6,
				Ap_BSS:      &pb.WiFiBSSCfg{SSID: "test-ap", PSK: "correcthorsebattery"},
			},
			want: []string{"wpa=2", "wpa_key_mgmt=WPA-PSK", "rsn_pairwise=CCMP", "auth_algs=1"},
			// wpa=1 is WPA1. wpa_pairwise would re-admit TKIP. auth_algs=3
			// advertises the WEP-era Shared Key handshake.
			reject: []string{"TKIP", "wpa_pairwise", "wpa=1", "auth_algs=3", "wep"},
		},
		{
			name: "open network",
			settings: &pb.WiFiSettings{
				WorkingMode: pb.WiFiWorkingMode_AP,
				AuthMode:    pb.WiFiAuthMode_OPEN,
				Channel:     6,
				Ap_BSS:      &pb.WiFiBSSCfg{SSID: "open-ap"},
			},
			// An open AP has nothing to authenticate, so it must offer
			// Open System only -- never Shared Key, which is meaningless
			// without WEP and leaks keystream where it does work.
			want:   []string{"auth_algs=1"},
			reject: []string{"auth_algs=3", "TKIP", "wep"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := wifiCreateHostapdConfString(tc.settings)
			if err != nil {
				t.Fatalf("building the config failed: %v", err)
			}
			for _, w := range tc.want {
				if !strings.Contains(cfg, w) {
					t.Errorf("config is missing %q:\n%s", w, cfg)
				}
			}
			low := strings.ToLower(cfg)
			for _, r := range tc.reject {
				if strings.Contains(low, strings.ToLower(r)) {
					t.Errorf("config offers %q, which it must not:\n%s", r, cfg)
				}
			}
		})
	}
}
