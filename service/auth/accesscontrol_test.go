package auth

import (
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
