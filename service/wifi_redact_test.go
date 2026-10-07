//go:build linux
// +build linux

package service

import (
	"strings"
	"testing"

	pb "github.com/mame82/P4wnP1_aloa/proto"
)

// The WiFi deploy path used to log the whole settings message with %+v.
// protoc-gen-go's String() text-marshals the entire nested structure, so that
// put the access point's PSK and the PSK of every saved client network into
// the systemd journal, which any local account can read. The client keys are
// the worse half: those are the operator's own home and office networks.
func TestWifiSettingsLogLineContainsNoPSK(t *testing.T) {
	settings := &pb.WiFiSettings{
		Name:        "startup",
		Regulatory:  "GB",
		Channel:     6,
		WorkingMode: pb.WiFiWorkingMode_AP,
		Ap_BSS:      &pb.WiFiBSSCfg{SSID: "P4wnP1", PSK: "AP-SECRET-PSK-0001"},
		Client_BSSList: []*pb.WiFiBSSCfg{
			{SSID: "OperatorHomeWiFi", PSK: "CLIENT-SECRET-PSK-0002"},
			{SSID: "ClientSiteWiFi", PSK: "CLIENT-SECRET-PSK-0003"},
		},
	}

	line := describeWifiSettings(settings)

	for _, secret := range []string{
		"AP-SECRET-PSK-0001",
		"CLIENT-SECRET-PSK-0002",
		"CLIENT-SECRET-PSK-0003",
	} {
		if strings.Contains(line, secret) {
			t.Errorf("the log line leaks a PSK (%s): %s", secret, line)
		}
	}

	// It must still be worth logging: the operator needs to be able to tell
	// which configuration was deployed from this line alone.
	for _, want := range []string{"startup", "GB", "P4wnP1", "OperatorHomeWiFi", "ClientSiteWiFi"} {
		if !strings.Contains(line, want) {
			t.Errorf("the log line dropped something useful (%s): %s", want, line)
		}
	}
	// And it must distinguish "a key is set" from "no key", without saying
	// how long it is.
	if !strings.Contains(line, "PSK set") {
		t.Errorf("the log line does not say whether a PSK is configured: %s", line)
	}
}

func TestWifiSettingsLogLineHandlesEmptyAndNil(t *testing.T) {
	if got := describeWifiSettings(nil); got != "<nil>" {
		t.Errorf("nil settings rendered as %q", got)
	}
	line := describeWifiSettings(&pb.WiFiSettings{Name: "bare"})
	if !strings.Contains(line, "bare") || !strings.Contains(line, "none") {
		t.Errorf("empty settings rendered oddly: %s", line)
	}
}
