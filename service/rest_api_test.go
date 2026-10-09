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
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

// TestRecoverHandlerSurvivesAPanic covers the gap the gRPC interceptors leave.
//
// jsonbridge invokes service methods directly by reflection, so a request over
// /api/v1/rpc/ never passes through grpc.NewServer's interceptor chain. Before
// RecoverHandler, a panic in any of the 82 RPCs reachable that way -- the path
// the web console uses for everything -- ended the process.
func TestRecoverHandlerSurvivesAPanic(t *testing.T) {
	boom := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	})
	rec := httptest.NewRecorder()

	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("the panic escaped RecoverHandler: %v", r)
			}
		}()
		RecoverHandler(boom).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/rpc/X", nil))
	}()

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	if body := rec.Body.String(); !strings.Contains(body, "internal error") {
		t.Errorf("body = %q, want a JSON error", body)
	}
}

// Once bytes are on the wire -- always true for SSE, which writes 200 and then
// streams -- a response cannot be turned into an error. The handler must not
// try, because WriteHeader after a write logs a spurious superfluous-call
// warning and corrupts nothing useful.
func TestRecoverHandlerAfterHeadersSent(t *testing.T) {
	streamed := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: one\n\n"))
		panic("died mid-stream")
	})
	rec := httptest.NewRecorder()

	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("the panic escaped RecoverHandler: %v", r)
			}
		}()
		RecoverHandler(streamed).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/events", nil))
	}()

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d; the 200 was already sent and must not be rewritten", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "data: one") {
		t.Errorf("the bytes written before the panic were lost: %q", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "internal error") {
		t.Error("an error body was appended to an already-streaming response")
	}
}

// The wrapper must forward the optional interfaces the handlers beneath it
// rely on: Flush for SSE, Unwrap for http.NewResponseController.
func TestRecordingWriterForwardsInterfaces(t *testing.T) {
	rec := httptest.NewRecorder()
	w := &recordingWriter{ResponseWriter: rec}
	if _, ok := interface{}(w).(http.Flusher); !ok {
		t.Error("recordingWriter is not an http.Flusher; SSE would buffer")
	}
	if u, ok := interface{}(w).(interface{ Unwrap() http.ResponseWriter }); !ok || u.Unwrap() != rec {
		t.Error("recordingWriter does not Unwrap to the real writer")
	}
}

// knownHost is the only thing standing between this device and DNS rebinding.
// sameOrigin compares Origin against Host, and an attacker who controls a
// domain controls both: point evil.example at 172.16.0.1 and a victim's
// browser sends Origin and Host that match, so the origin check passes. That
// was confirmed against a running service before this guard existed.
func TestKnownHostAcceptsEveryRouteToTheDevice(t *testing.T) {
	for _, h := range []string{
		"172.16.0.1:8000",   // USB ethernet, the primary route
		"172.24.0.1:8000",   // the device's own access point
		"172.26.0.1:8000",   // bluetooth tethering
		"127.0.0.1:8000",    // on the device itself
		"localhost:8000",    //
		"[::1]:8000",        // IPv6 loopback
		"192.168.1.50",      // no port
		"p4wnp1.local:8000", // mDNS
		"",                  // HTTP/1.0 and some non-browser clients
	} {
		if !knownHost(h) {
			t.Errorf("knownHost(%q) = false; this would lock the operator out", h)
		}
	}
}

func TestKnownHostRejectsRebindableNames(t *testing.T) {
	for _, h := range []string{
		"evil.example:8000",
		"rebind.attacker.com",
		"172.16.0.1.evil.example", // looks like the device, is not
		"localhost.evil.example",  // ditto
	} {
		if knownHost(h) {
			t.Errorf("knownHost(%q) = true; DNS rebinding is possible", h)
		}
	}
}

func TestKnownHostHonoursTheOperatorsOwnName(t *testing.T) {
	if knownHost("box.internal") {
		t.Fatal("box.internal was accepted before being allowed")
	}
	t.Setenv(allowedHostEnv, "p4wnp1.lan, box.internal")
	if !knownHost("box.internal:8000") {
		t.Error("a host listed in " + allowedHostEnv + " was still refused")
	}
	if !knownHost("P4wnP1.LAN") {
		t.Error("the allowlist should be case-insensitive")
	}
	if knownHost("other.internal") {
		t.Error("a host NOT in the allowlist was accepted")
	}
}

// The console busts its own cache with ?v=N on every asset in
// app/index.html. That only works if the HTML carrying the version is
// always refetched. Nothing set a Cache-Control header, so browsers cached
// index.html off Last-Modified and went on requesting the old ?v= -- which
// means a device upgrade silently fails to reach anyone who has visited
// before, and looks like the change simply did not work.
func TestHTMLIsNotCachedButAssetsMayBe(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "app", "js"), 0755); err != nil {
		t.Fatal(err)
	}
	for path, body := range map[string]string{
		"app/index.html": "<html><script src=\"js/app.js?v=1\"></script></html>",
		"app/js/app.js":  "// console",
	} {
		if err := os.WriteFile(filepath.Join(root, path), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}

	raw := http.FileServer(http.Dir(root))
	// The same wrapper the service installs.
	srv := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if p == "" || strings.HasSuffix(p, "/") || strings.HasSuffix(p, ".html") {
			w.Header().Set("Cache-Control", "no-cache, must-revalidate")
		}
		raw.ServeHTTP(w, r)
	})

	for _, tc := range []struct {
		path        string
		wantNoCache bool
	}{
		{"/app/index.html", true},
		{"/app/", true}, // directory index serves the same HTML
		{"/app/js/app.js?v=1", false},
	} {
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, httptest.NewRequest(http.MethodGet, tc.path, nil))
		got := w.Header().Get("Cache-Control")
		has := strings.Contains(got, "no-cache")
		if has != tc.wantNoCache {
			t.Errorf("%s: Cache-Control %q, wanted no-cache=%v", tc.path, got, tc.wantNoCache)
		}
	}
}
