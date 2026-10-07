package service

// These tests reflect over the REAL service implementation, so they only build
// on linux (the hid and USB subsystems the server type depends on are
// linux-only). Run them with:
//
//	GOOS=linux go vet ./service/        # compile check from any host
//	docker run ... go test ./service/   # actual execution
//
// Verified in a linux/arm64 container during development.

import (
	"strings"
	"testing"

	"github.com/mame82/P4wnP1_aloa/service/jsonbridge"
)

// TestBridgeCoversTheRealService is the load-bearing test for the JSON API:
// it asserts the reflective discovery actually finds the service's RPCs rather
// than silently finding none (which would leave the API responding 404 to
// everything while still starting up cleanly).
func TestBridgeCoversTheRealService(t *testing.T) {
	b, err := jsonbridge.New(&server{})
	if err != nil {
		t.Fatalf("jsonbridge.New on the real server: %v", err)
	}
	methods := b.Methods()

	// The service definition has 80+ unary RPCs. If a refactor drops the count
	// through the floor, the API has quietly lost most of its surface.
	// grpc.proto declares 83 RPCs, exactly one of which (EventListen) is
	// server-streaming, so 82 is full coverage. The floor sits just below that
	// so adding an RPC does not fail the build, while a collapse in discovery
	// -- which would 404 the whole API while the service still started cleanly
	// -- still trips it.
	if len(methods) < 80 {
		t.Errorf("discovered only %d RPCs; expected 82. Discovery is probably broken.\n%v",
			len(methods), methods)
	}
	t.Logf("discovered %d unary RPCs", len(methods))

	// Spot-check representative RPCs across every subsystem, so a change that
	// breaks one area's message shapes is caught here.
	for _, want := range []string{
		"DeployGadgetSetting",             // USB gadget
		"GetDeployedGadgetSetting",        // USB gadget
		"DeployWiFiSettings",              // WiFi
		"GetWiFiState",                    // WiFi
		"DeployBluetoothSettings",         // Bluetooth
		"GetBluetoothAgentSettings",       // Bluetooth
		"DeployEthernetInterfaceSettings", // Ethernet / USB networking
		"HIDRunScript",                    // HID injection
		"HIDGetRunningScriptJobs",         // HID job control
		"DeployTriggerActionSetReplace",   // automation engine
		"WaitTriggerGroupReceive",         // automation engine
		"FSReadFile",                      // filesystem
		"FSWriteFile",                     // filesystem
		"DBBackup",                        // template store
		"DeployMasterTemplate",            // whole-device config
		"Reboot",                          // system control
		"Shutdown",                        // system control
	} {
		if !b.Has(want) {
			t.Errorf("RPC %q is missing from the JSON API", want)
		}
	}

	// EventListen is a *streaming* RPC. It must NOT appear as a unary endpoint
	// -- it is served separately at /api/v1/events as SSE. If it leaked into
	// the unary set, calling it would block a request goroutine forever.
	if b.Has("EventListen") {
		t.Error("EventListen must not be exposed as a unary RPC (it is server-streaming)")
	}
}

func TestSameOriginPolicy(t *testing.T) {
	cases := []struct {
		name, origin, host string
		want               bool
	}{
		{"no origin (curl/CLI)", "", "172.16.0.1:8000", true},
		{"same host", "http://172.16.0.1:8000", "172.16.0.1:8000", true},
		{"same host, different port", "http://172.16.0.1:1234", "172.16.0.1:8000", true},
		{"same host, https", "https://172.16.0.1", "172.16.0.1:8000", true},
		{"hostname match", "http://p4wnp1.local", "p4wnp1.local", true},
		{"case-insensitive host", "http://P4wnP1.local", "p4wnp1.local", true},
		{"foreign origin", "https://evil.example", "172.16.0.1:8000", false},
		{"foreign origin, lookalike", "https://172.16.0.1.evil.example", "172.16.0.1:8000", false},
		{"null origin (sandboxed iframe)", "null", "172.16.0.1:8000", false},
		{"garbage origin", "::::", "172.16.0.1:8000", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newTestRequest(tc.origin, tc.host)
			if got := sameOrigin(r); got != tc.want {
				t.Errorf("sameOrigin(origin=%q host=%q) = %v, want %v",
					tc.origin, tc.host, got, tc.want)
			}
		})
	}
}

func TestAPIPrefixShape(t *testing.T) {
	// The router does a HasPrefix check against this, and the handler strips it.
	// Both break if it stops being a slash-terminated absolute path.
	if !strings.HasPrefix(APIPrefix, "/") || !strings.HasSuffix(APIPrefix, "/") {
		t.Errorf("APIPrefix = %q; must start and end with /", APIPrefix)
	}
}
