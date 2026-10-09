package oled

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mame82/P4wnP1_aloa/common"
)

// The client names 20-odd RPCs as strings. A typo in one of them cannot fail
// at compile time and cannot fail in the simulator, because the simulator
// uses the fake. It would fail for the first time on hardware, as "error 404"
// on a 128x64 screen. These tests close that gap.

// protoRPCs reads the service definition and returns every declared RPC name.
func protoRPCs(t *testing.T) map[string]bool {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "proto", "grpc.proto"))
	if err != nil {
		t.Fatalf("read grpc.proto: %v", err)
	}
	re := regexp.MustCompile(`(?m)^\s*rpc\s+([A-Za-z0-9_]+)\s*\(`)
	out := map[string]bool{}
	for _, m := range re.FindAllStringSubmatch(string(raw), -1) {
		out[m[1]] = true
	}
	if len(out) < 50 {
		t.Fatalf("only found %d rpcs in grpc.proto; the parser is wrong", len(out))
	}
	return out
}

// clientRPCs is every method name this package sends, gathered from the one
// place they are written down plus the handful used directly.
func clientRPCs() []string {
	var names []string
	for _, m := range kindMeta {
		for _, n := range []string{m.ListRPC, m.DeployRPC, m.DeleteRPC} {
			if n != "" {
				names = append(names, n)
			}
		}
	}
	return append(names,
		"SetStartupMasterTemplate", "GetStartupMasterTemplate",
		"HIDRunScript", "HIDRunScriptJob", "HIDGetRunningScriptJobs",
		"HIDCancelAllScriptJobs", "GetDeployedGadgetSetting", "DeployGadgetSetting",
		"SetLEDSettings", "Reboot", "Shutdown",
		"GetAllDeployedEthernetInterfaceSettings", "GetWiFiState",
		"GetDeployedTriggerActionSet",
	)
}

func TestEveryRPCTheClientSendsExistsInTheProto(t *testing.T) {
	declared := protoRPCs(t)
	for _, name := range clientRPCs() {
		if !declared[name] {
			t.Errorf("the OLED client calls %q, which is not an RPC in proto/grpc.proto", name)
		}
	}
}

// recorder is a stand-in service that records what it was asked for and
// replies with canned bodies, so the client's request shapes and its parsing
// are both exercised.
type recorder struct {
	mu       sync.Mutex
	calls    []string
	bodies   map[string]string
	reply    map[string]string
	status   map[string]int
	authSeen []string
}

func newRecorder() *recorder {
	return &recorder{
		bodies: map[string]string{},
		reply:  map[string]string{},
		status: map[string]int{},
	}
}

func (r *recorder) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		name := strings.TrimPrefix(req.URL.Path, "/api/v1/rpc/")
		body, _ := io.ReadAll(req.Body)

		r.mu.Lock()
		r.calls = append(r.calls, name)
		r.bodies[name] = string(body)
		r.authSeen = append(r.authSeen, req.Header.Get("Authorization"))
		code, reply := r.status[name], r.reply[name]
		r.mu.Unlock()

		if code == 0 {
			code = http.StatusOK
		}
		if reply == "" {
			reply = "{}"
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_, _ = w.Write([]byte(reply))
	})
}

func (r *recorder) called(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, c := range r.calls {
		if c == name {
			return true
		}
	}
	return false
}

// count reports how many times an RPC was called. "Was it called" cannot
// catch a call that fires once per item where it should fire once.
func (r *recorder) count(name string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, c := range r.calls {
		if c == name {
			n++
		}
	}
	return n
}

func (r *recorder) body(name string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.bodies[name]
}

// withClient wires an APIClient to a recorder and a token file.
func withClient(t *testing.T) (*APIClient, *recorder) {
	t.Helper()
	rec := newRecorder()
	srv := httptest.NewServer(rec.handler())
	t.Cleanup(srv.Close)

	tok := filepath.Join(t.TempDir(), "local.token")
	if err := os.WriteFile(tok, []byte("test-token-value\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return NewAPIClient(srv.URL, tok), rec
}

func TestClientAuthenticatesWithTheLocalCredential(t *testing.T) {
	c, rec := withClient(t)
	if _, err := c.List(KindHIDScript); err != nil {
		t.Fatalf("List: %v", err)
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.authSeen) == 0 {
		t.Fatal("no request reached the service")
	}
	// The trailing newline in the file must not reach the header.
	if got := rec.authSeen[0]; got != "Bearer test-token-value" {
		t.Errorf("Authorization = %q, want %q", got, "Bearer test-token-value")
	}
}

func TestClientWithoutACredentialSaysSo(t *testing.T) {
	rec := newRecorder()
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()
	c := NewAPIClient(srv.URL, filepath.Join(t.TempDir(), "absent"))

	_, err := c.List(KindHIDScript)
	if err == nil {
		t.Fatal("expected an error with no credential file")
	}
	if !strings.Contains(err.Error(), "credential") {
		t.Errorf("error %q does not mention the credential", err)
	}
	if len(rec.calls) != 0 {
		t.Error("a request was sent without a credential")
	}
}

// A 401 means our cached credential is stale -- the service re-issues it when
// sessions are revoked -- so the client must drop it and pick up the new one
// rather than failing forever.
func TestClientReloadsTheCredentialAfterA401(t *testing.T) {
	rec := newRecorder()
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()

	tok := filepath.Join(t.TempDir(), "local.token")
	if err := os.WriteFile(tok, []byte("stale"), 0600); err != nil {
		t.Fatal(err)
	}
	c := NewAPIClient(srv.URL, tok)

	rec.status["ListStoredHIDScripts"] = http.StatusUnauthorized
	if _, err := c.List(KindHIDScript); err == nil {
		t.Fatal("expected the 401 to surface")
	}

	// The service re-issues; the file now holds a working credential.
	if err := os.WriteFile(tok, []byte("fresh"), 0600); err != nil {
		t.Fatal(err)
	}
	rec.status["ListStoredHIDScripts"] = http.StatusOK
	rec.reply["ListStoredHIDScripts"] = `{"msgArray":["a.js"]}`
	if _, err := c.List(KindHIDScript); err != nil {
		t.Fatalf("second call: %v", err)
	}
	rec.mu.Lock()
	last := rec.authSeen[len(rec.authSeen)-1]
	rec.mu.Unlock()
	if last != "Bearer fresh" {
		t.Errorf("after a 401 the client sent %q; it must re-read the file", last)
	}
}

func TestClientSendsTheRightRequestShapes(t *testing.T) {
	c, rec := withClient(t)

	if err := c.DeployStored(KindMasterTemplate, "default"); err != nil {
		t.Fatal(err)
	}
	if got := rec.body("DeployStoredMasterTemplate"); !strings.Contains(got, `"msg":"default"`) {
		t.Errorf("DeployStoredMasterTemplate body = %s, want a msg field", got)
	}

	if err := c.SetLEDBlink(2); err != nil {
		t.Fatal(err)
	}
	if got := rec.body("SetLEDSettings"); !strings.Contains(got, `"blink_count":2`) {
		t.Errorf("SetLEDSettings body = %s, want blink_count", got)
	}

	rec.reply["HIDRunScriptJob"] = `{"id":7}`
	id, err := c.StartHIDScript("x.js")
	if err != nil {
		t.Fatal(err)
	}
	if id != 7 {
		t.Errorf("StartHIDScript returned job %d, want 7", id)
	}
	// Starting a payload must use the RPC that RETURNS, never the one that
	// waits. HIDRunScript and HIDGetScriptJobResult both block on the job
	// and both take the request context, so a client timeout on either one
	// cancels the running script.
	if rec.called("HIDRunScript") {
		t.Error("the panel called HIDRunScript, which waits on the job and can abort it")
	}
	// ABSOLUTE, under the HIDScripts directory.
	//
	// This assertion used to read `"scriptPath":"x.js"` -- it tested that the
	// client sent the name it had been given, which it faithfully did, and
	// which the service rejects out of hand with "path must be absolute".
	// The test encoded the bug, so it passed on every machine until a payload
	// was run on a real device and the OLED printed the refusal.
	if got := rec.body("HIDRunScriptJob"); !strings.Contains(got, `"scriptPath":"`+HIDScriptDir+`/x.js"`) {
		t.Errorf("HIDRunScriptJob body = %s, want an absolute path under %s", got, HIDScriptDir)
	}

	// Only a bare name is a payload name. Anything with a separator in it --
	// including an absolute path -- never reaches the wire.
	for _, bad := range []string{"../../etc/shadow", "sub/x.js", "/tmp/x.js", "..", ""} {
		if _, err := c.StartHIDScript(bad); err == nil {
			t.Errorf("StartHIDScript(%q) was accepted", bad)
		}
	}
}

// The run RPC checks the path it is given against common.PATH_HID_SCRIPTS. If
// that constant ever moves, this client would go on building paths to the old
// place and every payload would fail with a refusal that names a directory
// nobody changed.
func TestHIDScriptDirMatchesService(t *testing.T) {
	if HIDScriptDir != common.PATH_HID_SCRIPTS {
		t.Fatalf("oled builds payload paths under %s, the service allows %s",
			HIDScriptDir, common.PATH_HID_SCRIPTS)
	}
}

// SetUSBFunctions must read the deployed gadget first and modify it, not build
// a fresh message. The gadget carries the vendor and product IDs, the serial
// and the manufacturer string, and deploying a message built only from the
// toggles would quietly reset every one of them.
func TestSetUSBFunctionsPreservesTheRestOfTheGadget(t *testing.T) {
	c, rec := withClient(t)
	rec.reply["GetDeployedGadgetSetting"] = `{
		"vid":"0x1d6c","pid":"0x1347","manufacturer":"MaMe82",
		"product":"P4wnP1 by MaMe82","serial":"deadbeef1337",
		"use_HID_KEYBOARD":false,"use_RNDIS":true}`

	if err := c.SetUSBFunctions(map[string]bool{"use_HID_KEYBOARD": true}); err != nil {
		t.Fatal(err)
	}
	sent := rec.body("DeployGadgetSetting")
	for _, keep := range []string{`"vid":"0x1d6c"`, `"pid":"0x1347"`, `"serial":"deadbeef1337"`, `"use_RNDIS":true`} {
		if !strings.Contains(sent, keep) {
			t.Errorf("DeployGadgetSetting dropped %s\nsent: %s", keep, sent)
		}
	}
	if !strings.Contains(sent, `"use_HID_KEYBOARD":true`) {
		t.Errorf("the toggle was not applied\nsent: %s", sent)
	}
}

func TestClientParsesStatus(t *testing.T) {
	c, rec := withClient(t)
	rec.reply["GetDeployedGadgetSetting"] = `{"use_HID_KEYBOARD":true,"use_RNDIS":true}`
	rec.reply["GetAllDeployedEthernetInterfaceSettings"] =
		`{"list":[{"name":"usbeth","ipAddress4":"172.16.0.1","mode":"DHCP_SERVER"}]}`
	rec.reply["GetWiFiState"] = `{"mode":"AP","ssid":"P4wnP1"}`
	rec.reply["GetDeployedTriggerActionSet"] =
		`{"TriggerActions":[{"isActive":true},{"isActive":false}]}`
	// The shape the SERVICE sends: HIDScriptJobList is a bare list of ids.
	// This stub used to say {"jobs":[{"id":3,"scriptPath":"a.js"}]}, which no
	// version of the service has ever produced -- the test and the client
	// agreed with each other and both disagreed with the device, so the Jobs
	// screen was empty on hardware while the suite was green.
	rec.reply["HIDGetRunningScriptJobs"] = `{"ids":[3,4]}`

	st, err := c.Status()
	if err != nil {
		t.Fatal(err)
	}
	if len(st.USBFunctions) != 2 {
		t.Errorf("USB functions = %v, want 2", st.USBFunctions)
	}
	if len(st.Interfaces) != 1 || st.Interfaces[0].IP != "172.16.0.1" {
		t.Errorf("interfaces = %+v", st.Interfaces)
	}
	if st.WiFi != "AP P4wnP1" {
		t.Errorf("wifi = %q", st.WiFi)
	}
	if st.Reflexes != 2 || st.ReflexesArmed != 1 {
		t.Errorf("reflexes = %d/%d, want 1/2", st.ReflexesArmed, st.Reflexes)
	}
	if st.RunningJobs != 2 {
		t.Errorf("jobs = %d, want 2", st.RunningJobs)
	}
	// Counting jobs must not cost a call per job: the dashboard polls this
	// every five seconds on a single-core board.
	if n := rec.count("HIDGetRunningJobState"); n != 0 {
		t.Errorf("the status poll described %d jobs; it only needs the count", n)
	}
}

// Status must degrade rather than fail: a board with no WiFi still has a USB
// gadget worth showing, and a status screen that refuses to render because
// one subsystem is absent is useless on exactly the devices that need it.
func TestStatusSurvivesSubsystemsBeingAbsent(t *testing.T) {
	c, rec := withClient(t)
	rec.status["GetWiFiState"] = http.StatusServiceUnavailable
	rec.reply["GetWiFiState"] = `{"error":"the WiFi subsystem is unavailable on this device"}`
	rec.status["GetDeployedGadgetSetting"] = http.StatusServiceUnavailable
	rec.reply["GetDeployedGadgetSetting"] = `{"error":"gadget mame82_gadget doesn't exist"}`
	rec.reply["GetAllDeployedEthernetInterfaceSettings"] =
		`{"list":[{"name":"usbeth","ipAddress4":"172.16.0.1"}]}`

	st, err := c.Status()
	if err != nil {
		t.Fatalf("Status returned an error instead of degrading: %v", err)
	}
	if st.WiFi != "unavailable" {
		t.Errorf("wifi = %q, want unavailable", st.WiFi)
	}
	if len(st.Interfaces) != 1 {
		t.Errorf("the interfaces that DID answer were lost: %+v", st.Interfaces)
	}
	if st.Err == "" {
		t.Error("nothing recorded that part of the status was unreadable")
	}
}

// The service's own message is worth more than a status code on a screen this
// small, so it has to survive the round trip.
func TestClientSurfacesTheServiceMessage(t *testing.T) {
	c, rec := withClient(t)
	rec.status["DeployStoredWifiSettings"] = http.StatusServiceUnavailable
	rec.reply["DeployStoredWifiSettings"] = `{"error":"the WiFi subsystem is unavailable on this device"}`

	err := c.DeployStored(KindWifiSettings, "ap")
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "WiFi subsystem") {
		t.Errorf("error %q lost the service's message", err)
	}
}

func TestClientHandlesAnUnreachableService(t *testing.T) {
	tok := filepath.Join(t.TempDir(), "local.token")
	if err := os.WriteFile(tok, []byte("tok"), 0600); err != nil {
		t.Fatal(err)
	}
	// A port nothing is listening on.
	c := NewAPIClient("http://127.0.0.1:1", tok)
	if _, err := c.List(KindHIDScript); err == nil {
		t.Fatal("expected an error")
	} else if !strings.Contains(err.Error(), "unreachable") {
		t.Errorf("error %q should say the service is unreachable", err)
	}
}

// Garbage on the wire must not take the daemon down.
func TestClientSurvivesMalformedJSON(t *testing.T) {
	c, rec := withClient(t)
	rec.reply["ListStoredHIDScripts"] = `{"msgArray": [this is not json`
	if _, err := c.List(KindHIDScript); err == nil {
		t.Error("expected a parse error, got none")
	}
}

func TestKindMetadataIsComplete(t *testing.T) {
	// Every Kind the UI can construct must have metadata, or List panics on
	// a nil map entry at the worst possible moment.
	for k := KindMasterTemplate; k <= KindDBBackup; k++ {
		m, ok := kindMeta[k]
		if !ok {
			t.Errorf("Kind %d has no metadata", k)
			continue
		}
		if m.Label == "" {
			t.Errorf("Kind %d has no label", k)
		}
		if m.ListRPC == "" {
			t.Errorf("Kind %s has no list RPC", m.Label)
		}
		if n := len([]rune(m.Label)); n > Cols-1 {
			t.Errorf("Kind label %q is %d chars, too wide for the title bar", m.Label, n)
		}
		if m.Deployable && m.DeployRPC == "" {
			t.Errorf("Kind %s claims to be deployable with no deploy RPC", m.Label)
		}
		if m.Deletable && m.DeleteRPC == "" {
			t.Errorf("Kind %s claims to be deletable with no delete RPC", m.Label)
		}
	}
}

func TestBothClientsSatisfyTheInterface(t *testing.T) {
	// A compile-time check, so the fake and the real client cannot drift: if
	// the UI gains a need, both have to answer it.
	var _ Client = NewFakeClient()
	var _ Client = NewAPIClient("http://x", "/dev/null")
}

// A deadline and a dead socket are different faults and need different
// fixes. This client reported both as "service unreachable", which sent me
// looking at the network for a timeout the client itself owned.
func TestTimeoutIsNotReportedAsUnreachable(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		_, _ = w.Write([]byte("{}"))
	}))
	defer slow.Close()

	dir := t.TempDir()
	tok := filepath.Join(dir, "tok")
	if err := os.WriteFile(tok, []byte("sometoken"), 0600); err != nil {
		t.Fatal(err)
	}
	c := NewAPIClient(slow.URL, tok)
	c.HTTP.Timeout = 50 * time.Millisecond

	_, err := c.List(KindHIDScript)
	if err == nil {
		t.Fatal("a call that outran its deadline returned no error")
	}
	if strings.Contains(err.Error(), "unreachable") {
		t.Errorf("a timeout was reported as %q -- it blames the network for our own deadline", err)
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("a timeout was reported as %q, want it to say so", err)
	}
}

// The Cable screen must be able to bring a dead gadget back.
//
// GetDeployedGadgetSetting reports configfs as it is right now, so on a
// device whose gadget has been torn down it returns enabled:false. The
// read-modify-write sent that straight back, so ticking every function and
// deploying produced another disabled gadget -- the one screen whose job is
// to restore USB could not, and said nothing about why.
func TestSetUSBFunctionsForcesTheGadgetEnabled(t *testing.T) {
	c, rec := withClient(t)
	// A torn-down gadget: everything off, enabled false.
	rec.reply["GetDeployedGadgetSetting"] = `{"enabled":false,"vid":"0x1d6b","pid":"0x1347",
		"use_HID_KEYBOARD":false,"use_RNDIS":false}`
	if err := c.SetUSBFunctions(map[string]bool{"use_HID_KEYBOARD": true, "use_RNDIS": true}); err != nil {
		t.Fatal(err)
	}
	body := rec.body("DeployGadgetSetting")
	if !strings.Contains(body, `"enabled":true`) {
		t.Errorf("deployed a gadget that is still disabled: %s", body)
	}
	// And it must still preserve the rest of the message.
	for _, keep := range []string{`"vid":"0x1d6b"`, `"pid":"0x1347"`, `"use_HID_KEYBOARD":true`} {
		if !strings.Contains(body, keep) {
			t.Errorf("deploy lost %s: %s", keep, body)
		}
	}
}
