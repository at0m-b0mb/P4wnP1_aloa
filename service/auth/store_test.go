package auth

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// newStoreTempfile gives each subtest its own auth.json under t.TempDir, so
// tests don't see each other.
func newStoreTempfile(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "auth.json")
	s, err := NewStore(path)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return s
}

func TestStore_MissingFileIsOK(t *testing.T) {
	s := newStoreTempfile(t)
	if s.HasAnyUsers() {
		t.Fatal("fresh store should have zero users")
	}
}

func TestStore_SetAndVerify(t *testing.T) {
	s := newStoreTempfile(t)
	const user, pw = "admin", "very-secret-password!"
	if err := s.SetPassword(user, pw); err != nil {
		t.Fatalf("SetPassword: %v", err)
	}
	if !s.HasAnyUsers() {
		t.Fatal("store should report at least one user after SetPassword")
	}
	if !s.Verify(user, pw) {
		t.Fatal("Verify rejected the correct password")
	}
	if s.Verify(user, "wrong-password!") {
		t.Fatal("Verify accepted a wrong password")
	}
	if s.Verify("nobody", pw) {
		t.Fatal("Verify accepted an unknown user")
	}
}

func TestStore_RejectShortPassword(t *testing.T) {
	s := newStoreTempfile(t)
	err := s.SetPassword("admin", "short")
	if err == nil {
		t.Fatal("SetPassword should reject password shorter than MinPasswordLength")
	}
	// A specific sentinel so callers can switch on it.
	if !strings.Contains(err.Error(), "weak") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestStore_RejectEmptyUsername(t *testing.T) {
	s := newStoreTempfile(t)
	err := s.SetPassword("", "longenoughpassword-12chars-or-more")
	if err == nil {
		t.Fatal("SetPassword should reject empty username")
	}
}

func TestStore_PersistsAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")

	// First instance: write a user.
	s1, err := NewStore(path)
	if err != nil {
		t.Fatalf("NewStore #1: %v", err)
	}
	const user, pw = "admin", "this-is-a-strong-password"
	if err := s1.SetPassword(user, pw); err != nil {
		t.Fatalf("SetPassword: %v", err)
	}

	// Second instance: read back, verify.
	s2, err := NewStore(path)
	if err != nil {
		t.Fatalf("NewStore #2: %v", err)
	}
	if !s2.HasAnyUsers() {
		t.Fatal("reopened store should have the user we wrote")
	}
	if !s2.Verify(user, pw) {
		t.Fatal("reopened store rejected the previously-set password")
	}
}

func TestStore_FileModeIs0600(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	s, err := NewStore(path)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := s.SetPassword("admin", "longenoughpassword-12chars"); err != nil {
		t.Fatalf("SetPassword: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	// On macOS / Linux Stat gives us mode bits in low 9. We persist via
	// CreateTemp + Chmod(0600) + Rename. Make sure that lands.
	mode := info.Mode().Perm()
	if mode != 0600 {
		t.Fatalf("auth file should be mode 0600, got %o", mode)
	}
}

func TestStore_OnDiskFormatIsValidJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	s, err := NewStore(path)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := s.SetPassword("admin", "longenoughpassword-12chars"); err != nil {
		t.Fatalf("SetPassword: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var sf storeFile
	if err := json.Unmarshal(data, &sf); err != nil {
		t.Fatalf("auth file is not valid JSON: %v\n%s", err, data)
	}
	if sf.Version != 1 {
		t.Fatalf("file version should be 1, got %d", sf.Version)
	}
	if len(sf.Users) != 1 {
		t.Fatalf("expected 1 user, got %d", len(sf.Users))
	}
	if sf.Users[0].Username != "admin" {
		t.Fatalf("expected username 'admin', got %q", sf.Users[0].Username)
	}
	// The hash should look like a bcrypt hash, not the plaintext.
	if !strings.HasPrefix(sf.Users[0].PasswordHash, "$2") {
		t.Fatalf("stored password doesn't look like a bcrypt hash: %q", sf.Users[0].PasswordHash)
	}
	if strings.Contains(sf.Users[0].PasswordHash, "longenough") {
		t.Fatalf("PLAINTEXT PASSWORD LEAKED INTO STORE FILE: %s", sf.Users[0].PasswordHash)
	}
}

func TestStore_ReplacePassword(t *testing.T) {
	s := newStoreTempfile(t)
	const user = "admin"
	if err := s.SetPassword(user, "first-password-12-chars"); err != nil {
		t.Fatalf("SetPassword #1: %v", err)
	}
	if err := s.SetPassword(user, "second-password-12chars"); err != nil {
		t.Fatalf("SetPassword #2: %v", err)
	}
	if s.Verify(user, "first-password-12-chars") {
		t.Fatal("old password should no longer verify after replacement")
	}
	if !s.Verify(user, "second-password-12chars") {
		t.Fatal("new password should verify after replacement")
	}
}

// A store created before the auth file exists must pick it up when it
// appears, with no restart.
//
// This is load-bearing for first boot. The service starts early, before the
// first-boot helper has bootstrapped an admin, so NewStore runs against a
// path with no file behind it. load() treats that as "no users" rather than
// an error, so the store keeps its path -- and reloadIfChanged then sees the
// file the moment it is written.
//
// The first-boot helper did not trust this and ran `systemctl restart
// P4wnP1.service` instead. On a USB-gadget appliance that restart tears down
// the gadget and the host cannot re-enumerate without a physical replug, so
// the device silently vanished from its operator a few minutes into every
// first boot. The restart was never needed; this test is why it could go.
func TestStoreAdoptsAnAuthFileThatAppearsLater(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")

	// No file yet -- exactly the state at service start on a fresh image.
	s, err := NewStore(path)
	if err != nil {
		t.Fatalf("NewStore on a missing file failed: %v", err)
	}
	if s.HasAnyUsers() {
		t.Fatal("a store with no file reported users")
	}
	if s.Verify("admin", "whatever") {
		t.Fatal("a store with no file verified a password")
	}

	// First boot bootstraps the admin, by writing the file.
	other, err := NewStore(filepath.Join(dir, "scratch.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := other.SetPassword("admin", "correct-horse-battery"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "scratch.json"))
	if err != nil {
		t.Fatal(err)
	}
	// Written after a beat so the mtime differs from the zero value.
	time.Sleep(10 * time.Millisecond)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}

	// The SAME store object, no restart, must now accept the new password.
	if !s.Verify("admin", "correct-horse-battery") {
		t.Error("the store did not pick up an auth file written after it started")
	}
	if s.Verify("admin", "wrong") {
		t.Error("the store accepted a wrong password")
	}
	if !s.HasAnyUsers() {
		t.Error("HasAnyUsers still reports none after the file appeared")
	}
}
