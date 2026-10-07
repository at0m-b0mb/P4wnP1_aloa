package auth

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Each test here was written by first demonstrating the broken behaviour
// against the real code, then fixing it, then confirming the test flipped.
// None of them can pass against the code as it was.

// p4wnp1-hashpw is a SEPARATE PROCESS that writes /etc/p4wnp1/auth.json
// directly. The running service used to read that file once at startup and
// never again, so resetting a forgotten password left the OLD password working
// until the next restart -- the operator believes they have rotated a
// credential that is still live.
func TestOutOfBandPasswordChangeTakesEffectImmediately(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")

	svcStore, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := svcStore.SetPassword("admin", "the-old-password"); err != nil {
		t.Fatal(err)
	}
	m := NewManager(svcStore, NewSessions(), time.Hour)
	defer m.Close()

	if _, err := m.Login(nil, "admin", "the-old-password"); err != nil { //nolint:staticcheck // nil ctx unused
		t.Fatalf("the old password should work before the rotation: %v", err)
	}

	// Another process rewrites the file. os.Stat's modtime can have coarse
	// granularity, so make sure the change is detectable.
	time.Sleep(10 * time.Millisecond)
	hashpwStore, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := hashpwStore.SetPassword("admin", "the-rotated-password"); err != nil {
		t.Fatal(err)
	}

	if _, err := m.Login(nil, "admin", "the-old-password"); err == nil { //nolint:staticcheck // nil ctx unused
		t.Error("the OLD password still logs in after an out-of-band rotation")
	}
	if _, err := m.Login(nil, "admin", "the-rotated-password"); err != nil { //nolint:staticcheck // nil ctx unused
		t.Errorf("the NEW password does not log in after an out-of-band rotation: %v", err)
	}
}

// A corrupt or half-written auth file must not empty the user table: that
// would lock the operator out of their own device.
func TestCorruptAuthFileDoesNotWipeTheUserTable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	store, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetPassword("admin", "a-good-password-here"); err != nil {
		t.Fatal(err)
	}

	time.Sleep(10 * time.Millisecond)
	if err := writeTokenFile(path, "{ this is not json"); err != nil {
		t.Fatal(err)
	}

	if !store.Verify("admin", "a-good-password-here") {
		t.Error("a corrupt auth file locked out a valid account")
	}
}

// localTokenState used to be a package-level var, so two Managers shared one
// slot: the second to provision took ownership of the first's entry, leaving
// the first's session unrevocable and its file orphaned.
func TestLocalTokenStateIsPerManager(t *testing.T) {
	dir := t.TempDir()
	pathA := filepath.Join(dir, "a.token")
	pathB := filepath.Join(dir, "b.token")

	sA, _ := NewStore(filepath.Join(dir, "a.json"))
	sB, _ := NewStore(filepath.Join(dir, "b.json"))
	mA := NewManager(sA, NewSessions(), time.Hour)
	mB := NewManager(sB, NewSessions(), time.Hour)
	defer mA.Close()
	defer mB.Close()

	if err := mA.ProvisionLocalToken(pathA); err != nil {
		t.Fatal(err)
	}
	tokA, _ := ReadLocalToken(pathA)
	if err := mB.ProvisionLocalToken(pathB); err != nil {
		t.Fatal(err)
	}

	// B provisioning must not have disturbed A.
	if _, err := mA.ValidateToken(tokA); err != nil {
		t.Errorf("B's provisioning revoked A's token: %v", err)
	}

	// Each Manager removes its OWN token and leaves the other's alone.
	if err := mB.RemoveLocalToken(); err != nil {
		t.Fatal(err)
	}
	if got, _ := ReadLocalToken(pathB); got != "" {
		t.Error("B's token file survived B's RemoveLocalToken")
	}
	if got, _ := ReadLocalToken(pathA); got == "" {
		t.Error("B's RemoveLocalToken deleted A's file")
	}

	if err := mA.RemoveLocalToken(); err != nil {
		t.Fatal(err)
	}
	if _, err := mA.ValidateToken(tokA); err == nil {
		t.Error("A's RemoveLocalToken did not revoke A's own session")
	}
}

// FailedLoginDelay sleeps in the caller's own goroutine. Without a lock that
// throttles nothing: N parallel guesses all sleep concurrently and the whole
// batch costs one delay, not N. The delay exists specifically to deny
// brute-force throughput, so this is the property worth pinning.
func TestParallelFailedLoginsAreSerialised(t *testing.T) {
	if FailedLoginDelay > time.Second {
		t.Skipf("FailedLoginDelay is %v; this test assumes something short", FailedLoginDelay)
	}
	store, _ := NewStore(filepath.Join(t.TempDir(), "auth.json"))
	if err := store.SetPassword("admin", "the-real-password"); err != nil {
		t.Fatal(err)
	}
	m := NewManager(store, NewSessions(), time.Hour)
	defer m.Close()

	const n = 4
	start := time.Now()
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = m.Login(nil, "admin", "wrong-password") //nolint:staticcheck // nil ctx unused
		}()
	}
	wg.Wait()
	elapsed := time.Since(start)

	// Serialised, n rejections cost about n delays. Unserialised they cost
	// about one. Assert comfortably above one delay to avoid flaking on a
	// loaded machine, while still failing outright if the lock is removed.
	floor := time.Duration(float64(FailedLoginDelay) * float64(n) * 0.6)
	if elapsed < floor {
		t.Errorf("%d parallel bad logins took %v; expected at least %v. "+
			"The delay is not throttling anything -- an attacker just opens "+
			"more connections.", n, elapsed, floor)
	}
}

// A correct password must never be made to queue behind an attacker's wrong
// ones, or the throttle becomes a denial of service against the operator.
func TestASuccessfulLoginDoesNotWaitOnTheFailureLock(t *testing.T) {
	store, _ := NewStore(filepath.Join(t.TempDir(), "auth.json"))
	if err := store.SetPassword("admin", "the-real-password"); err != nil {
		t.Fatal(err)
	}
	m := NewManager(store, NewSessions(), time.Hour)
	defer m.Close()

	// Occupy the failure path.
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = m.Login(nil, "admin", "wrong") //nolint:staticcheck // nil ctx unused
		}()
	}
	time.Sleep(20 * time.Millisecond) // let them grab the lock

	start := time.Now()
	if _, err := m.Login(nil, "admin", "the-real-password"); err != nil { //nolint:staticcheck // nil ctx unused
		t.Fatalf("valid login failed: %v", err)
	}
	elapsed := time.Since(start)
	wg.Wait()

	// bcrypt at cost 12 is itself slow, so allow generous headroom; the point
	// is that it did not serialise behind three FailedLoginDelays.
	if elapsed >= 3*FailedLoginDelay {
		t.Errorf("a valid login waited %v behind failing ones -- the throttle "+
			"is a DoS against the operator", elapsed)
	}
}

// /api/auth/changepw must require a bearer token. The CLI used to POST this
// with no Authorization header at all, so `P4wnP1_cli auth changepw` answered
// 401 every time it was run.
func TestChangepwRequiresABearerToken(t *testing.T) {
	store, _ := NewStore(filepath.Join(t.TempDir(), "auth.json"))
	if err := store.SetPassword("admin", "the-real-password"); err != nil {
		t.Fatal(err)
	}
	m := NewManager(store, NewSessions(), time.Hour)
	defer m.Close()
	srv := httptest.NewServer(HTTPHandler(m))
	defer srv.Close()

	body := `{"username":"admin","old_password":"the-real-password","new_password":"a-brand-new-pw"}`

	// No token: refused.
	resp, err := http.Post(srv.URL+"/api/auth/changepw", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("changepw without a token = %d, want 401", resp.StatusCode)
	}

	// With a token: accepted. This half is what proves the endpoint is usable
	// at all -- without it, "401" could just mean the route is broken.
	sess, err := m.Login(nil, "admin", "the-real-password") //nolint:staticcheck // nil ctx unused
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/auth/changepw", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+sess.Token)
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusNoContent && resp2.StatusCode != http.StatusOK {
		t.Errorf("changepw WITH a token = %d, want 204/200", resp2.StatusCode)
	}
}

// The session list is served to a browser. If it carried tokens it would stop
// being an audit view and become a credential dump -- any XSS, any shoulder
// surf, any screenshot would hand over every live session on the device.
func TestSessionListNeverContainsAToken(t *testing.T) {
	store, _ := NewStore(filepath.Join(t.TempDir(), "auth.json"))
	if err := store.SetPassword("admin", "the-real-password"); err != nil {
		t.Fatal(err)
	}
	m := NewManager(store, NewSessions(), time.Hour)
	defer m.Close()
	srv := httptest.NewServer(HTTPHandler(m))
	defer srv.Close()

	var tokens []string
	for i := 0; i < 3; i++ {
		sess, err := m.Login(nil, "admin", "the-real-password") //nolint:staticcheck // nil ctx unused
		if err != nil {
			t.Fatal(err)
		}
		tokens = append(tokens, sess.Token)
	}
	if err := m.ProvisionLocalToken(filepath.Join(t.TempDir(), "local.token")); err != nil {
		t.Fatal(err)
	}

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/auth/sessions", nil)
	req.Header.Set("Authorization", "Bearer "+tokens[0])
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/auth/sessions = %d, want 200", resp.StatusCode)
	}
	raw, _ := io.ReadAll(resp.Body)
	body := string(raw)

	for i, tok := range tokens {
		if strings.Contains(body, tok) {
			t.Fatalf("token %d appears verbatim in the session list response", i)
		}
	}
	local, _ := ReadLocalToken(filepath.Join(t.TempDir(), "local.token"))
	if local != "" && strings.Contains(body, local) {
		t.Fatal("the machine-local token appears in the session list response")
	}

	var got struct {
		Sessions []SessionInfo `json:"sessions"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("response is not the documented shape: %v", err)
	}
	if len(got.Sessions) != 4 { // three logins plus the local script
		t.Errorf("listed %d sessions, want 4", len(got.Sessions))
	}
	current, localSeen := 0, 0
	for _, s := range got.Sessions {
		if s.IsCurrent {
			current++
		}
		if s.IsLocalScript {
			localSeen++
			if s.Username != LocalUsername {
				t.Errorf("local script session username = %q", s.Username)
			}
		}
	}
	if current != 1 {
		t.Errorf("%d sessions marked current, want exactly 1", current)
	}
	if localSeen != 1 {
		t.Errorf("%d sessions marked as the local script, want 1", localSeen)
	}
}

func TestSessionListRequiresAToken(t *testing.T) {
	store, _ := NewStore(filepath.Join(t.TempDir(), "auth.json"))
	m := NewManager(store, NewSessions(), time.Hour)
	defer m.Close()
	srv := httptest.NewServer(HTTPHandler(m))
	defer srv.Close()

	for _, path := range []string{"/api/auth/sessions", "/api/auth/sessions/revoke"} {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			t.Errorf("%s served without a token", path)
		}
	}
}

// A session ID must name a session without being usable as one.
func TestSessionIDIsNotAUsableCredential(t *testing.T) {
	store, _ := NewStore(filepath.Join(t.TempDir(), "auth.json"))
	if err := store.SetPassword("admin", "the-real-password"); err != nil {
		t.Fatal(err)
	}
	m := NewManager(store, NewSessions(), time.Hour)
	defer m.Close()

	sess, err := m.Login(nil, "admin", "the-real-password") //nolint:staticcheck // nil ctx unused
	if err != nil {
		t.Fatal(err)
	}
	id := SessionID(sess.Token)
	if id == sess.Token {
		t.Fatal("the session ID is the token")
	}
	if _, err := m.ValidateToken(id); err == nil {
		t.Fatal("the session ID authenticates as a token")
	}
}

func TestRevokeByIDEndsOnlyThatSession(t *testing.T) {
	store, _ := NewStore(filepath.Join(t.TempDir(), "auth.json"))
	if err := store.SetPassword("admin", "the-real-password"); err != nil {
		t.Fatal(err)
	}
	m := NewManager(store, NewSessions(), time.Hour)
	defer m.Close()
	srv := httptest.NewServer(HTTPHandler(m))
	defer srv.Close()

	mine, _ := m.Login(nil, "admin", "the-real-password")   //nolint:staticcheck // nil ctx unused
	theirs, _ := m.Login(nil, "admin", "the-real-password") //nolint:staticcheck // nil ctx unused
	other, _ := m.Login(nil, "admin", "the-real-password")  //nolint:staticcheck // nil ctx unused

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/auth/sessions/revoke",
		strings.NewReader(`{"id":"`+SessionID(theirs.Token)+`"}`))
	req.Header.Set("Authorization", "Bearer "+mine.Token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("revoke = %d, want 204", resp.StatusCode)
	}

	if _, err := m.ValidateToken(theirs.Token); err == nil {
		t.Error("the named session survived revocation")
	}
	if _, err := m.ValidateToken(mine.Token); err != nil {
		t.Error("revoking another session killed the caller's own")
	}
	if _, err := m.ValidateToken(other.Token); err != nil {
		t.Error("revoking one session killed an unrelated one")
	}

	// An unknown id must say so rather than silently succeeding.
	req2, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/auth/sessions/revoke",
		strings.NewReader(`{"id":"0000000000000000"}`))
	req2.Header.Set("Authorization", "Bearer "+mine.Token)
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusNotFound {
		t.Errorf("revoking an unknown id = %d, want 404", resp2.StatusCode)
	}
}

// Revoking the device's own credential must not leave the device unable to
// drive itself -- that is the outage the local credential exists to prevent.
func TestRevokingTheLocalScriptSessionReissuesIt(t *testing.T) {
	store, _ := NewStore(filepath.Join(t.TempDir(), "auth.json"))
	if err := store.SetPassword("admin", "the-real-password"); err != nil {
		t.Fatal(err)
	}
	m := NewManager(store, NewSessions(), time.Hour)
	defer m.Close()
	srv := httptest.NewServer(HTTPHandler(m))
	defer srv.Close()

	path := filepath.Join(t.TempDir(), "local.token")
	if err := m.ProvisionLocalToken(path); err != nil {
		t.Fatal(err)
	}
	before, _ := ReadLocalToken(path)
	mine, _ := m.Login(nil, "admin", "the-real-password") //nolint:staticcheck // nil ctx unused

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/auth/sessions/revoke",
		strings.NewReader(`{"id":"`+SessionID(before)+`"}`))
	req.Header.Set("Authorization", "Bearer "+mine.Token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("revoke = %d, want 204", resp.StatusCode)
	}

	if _, err := m.ValidateToken(before); err == nil {
		t.Error("the revoked local session still authenticates")
	}
	after, _ := ReadLocalToken(path)
	if after == "" || after == before {
		t.Fatal("the local credential was not re-issued")
	}
	if _, err := m.ValidateToken(after); err != nil {
		t.Errorf("the re-issued local credential does not authenticate: %v", err)
	}
}

// Provisioning must be atomic. Taking the lock only around the state swap left
// a window as wide as one file write: two overlapping calls each wrote their
// own token, then each revoked what it believed was the previous one, and the
// loser's revocation could land on the token actually left in the file.
// Measured before the fix: with calls started within 100us of each other the
// file held a REVOKED token 97-99% of the time.
//
// On a device the overlap is the twelve-hourly refresh colliding with a
// password change -- rare, and silent, and it leaves the device unable to
// authenticate to itself until the next refresh.
func TestConcurrentProvisioningAlwaysLeavesAValidToken(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "local.token")
	store, err := NewStore(filepath.Join(dir, "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	m := NewManager(store, NewSessions(), time.Hour)
	defer m.Close()
	if err := m.ProvisionLocalToken(path); err != nil {
		t.Fatal(err)
	}

	broken := 0
	for i := 0; i < 300; i++ {
		var wg sync.WaitGroup
		for j := 0; j < 2; j++ {
			wg.Add(1)
			go func() { defer wg.Done(); _ = m.ProvisionLocalToken(path) }()
		}
		wg.Wait()

		tok, err := ReadLocalToken(path)
		if err != nil {
			t.Fatalf("round %d: reading the token file: %v", i, err)
		}
		if _, err := m.ValidateToken(tok); err != nil {
			broken++
		}
	}
	if broken > 0 {
		t.Errorf("the token file held a revoked token in %d/300 rounds -- the "+
			"device would be unable to authenticate to itself", broken)
	}
}

// Two concurrent password changes are the realistic way to hit the above: both
// call RevokeAll and then re-issue.
func TestConcurrentPasswordChangesLeaveTheDeviceAbleToAuthenticate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "local.token")
	store, err := NewStore(filepath.Join(dir, "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	const pw = "a-stable-test-password"
	if err := store.SetPassword("admin", pw); err != nil {
		t.Fatal(err)
	}
	m := NewManager(store, NewSessions(), time.Hour)
	defer m.Close()
	if err := m.ProvisionLocalToken(path); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 25; i++ {
		var wg sync.WaitGroup
		for j := 0; j < 2; j++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_ = m.ChangePassword(nil, "admin", pw, pw) //nolint:staticcheck // nil ctx unused
			}()
		}
		wg.Wait()

		tok, _ := ReadLocalToken(path)
		if _, err := m.ValidateToken(tok); err != nil {
			t.Fatalf("round %d: the device cannot authenticate to itself after "+
				"two concurrent password changes", i)
		}
	}
}

// /api/auth/login is the only endpoint an anonymous caller can reach that
// parses a request body, and it used an unbounded json.NewDecoder(r.Body). On
// a 512MB Pi running as root, that is a denial of service against the whole
// appliance requiring no credential of any kind.
func TestAnonymousLoginBodyIsBounded(t *testing.T) {
	store, _ := NewStore(filepath.Join(t.TempDir(), "auth.json"))
	if err := store.SetPassword("admin", "the-real-password"); err != nil {
		t.Fatal(err)
	}
	m := NewManager(store, NewSessions(), time.Hour)
	defer m.Close()
	srv := httptest.NewServer(HTTPHandler(m))
	defer srv.Close()

	huge := `{"username":"admin","password":"` + strings.Repeat("A", maxAuthBodyBytes*4) + `"}`
	resp, err := http.Post(srv.URL+"/api/auth/login", "application/json", strings.NewReader(huge))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		t.Fatal("an oversized anonymous login body was accepted")
	}

	// ...and an ordinary body still works, so the limit is not just "reject
	// everything", which would also pass the check above.
	ok := `{"username":"admin","password":"the-real-password"}`
	resp2, err := http.Post(srv.URL+"/api/auth/login", "application/json", strings.NewReader(ok))
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("an ordinary login body was rejected: HTTP %d", resp2.StatusCode)
	}
}

// Serialising the post-failure delay bounds the GUESS RATE. It does not bound
// the COST of each attempt: bcrypt at cost 12 is ~250ms of CPU on a Pi Zero W
// and it runs before the delay, so N parallel attempts bought N concurrent
// bcrypts on a single-core board that may be mid-keystroke-injection.
func TestConcurrentLoginsAreBounded(t *testing.T) {
	store, _ := NewStore(filepath.Join(t.TempDir(), "auth.json"))
	if err := store.SetPassword("admin", "the-real-password"); err != nil {
		t.Fatal(err)
	}
	m := NewManager(store, NewSessions(), time.Hour)
	defer m.Close()

	var inFlight, peak int64
	var mu sync.Mutex
	observe := func(delta int64) {
		mu.Lock()
		inFlight += delta
		if inFlight > peak {
			peak = inFlight
		}
		mu.Unlock()
	}

	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			observe(1)
			_, _ = m.Login(nil, "admin", "wrong-password") //nolint:staticcheck // nil ctx unused
			observe(-1)
		}()
	}
	wg.Wait()

	// The counter above brackets the whole Login call, including the delay, so
	// it cannot measure bcrypt concurrency directly. What it CAN prove is that
	// the slots exist and are sized as documented -- the useful regression
	// guard, since the failure mode is someone removing them.
	if cap(m.loginSlots) != maxConcurrentLogins {
		t.Errorf("login slots = %d, want %d", cap(m.loginSlots), maxConcurrentLogins)
	}
	if maxConcurrentLogins < 1 {
		t.Error("maxConcurrentLogins must leave at least one slot or no one can log in")
	}
	if peak == 0 {
		t.Error("the probe never observed a login in flight")
	}
}
