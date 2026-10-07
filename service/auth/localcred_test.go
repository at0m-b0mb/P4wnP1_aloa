package auth

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func testManager(t *testing.T) *Manager {
	t.Helper()
	store, err := NewStore(filepath.Join(t.TempDir(), "users.json"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	m := NewManager(store, NewSessions(), time.Hour)
	t.Cleanup(m.Close)
	return m
}

// The whole point: a script that reads the file can authenticate.
func TestProvisionLocalTokenIsUsableCredential(t *testing.T) {
	m := testManager(t)
	path := filepath.Join(t.TempDir(), "run", "local.token")

	if err := m.ProvisionLocalToken(path); err != nil {
		t.Fatalf("ProvisionLocalToken: %v", err)
	}

	tok, err := ReadLocalToken(path)
	if err != nil {
		t.Fatalf("ReadLocalToken: %v", err)
	}
	if tok == "" {
		t.Fatal("token file is empty")
	}

	sess, err := m.ValidateToken(tok)
	if err != nil {
		t.Fatalf("the provisioned token does not authenticate: %v", err)
	}
	if sess.Username != LocalUsername {
		t.Errorf("session username = %q, want %q", sess.Username, LocalUsername)
	}
}

// A token readable by anyone but root would hand the device to any local
// user, so the mode is part of the contract.
func TestLocalTokenFileIsRootOnly(t *testing.T) {
	m := testManager(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "run", "local.token")
	if err := m.ProvisionLocalToken(path); err != nil {
		t.Fatalf("ProvisionLocalToken: %v", err)
	}

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := fi.Mode().Perm(); perm != 0600 {
		t.Errorf("token file mode = %04o, want 0600", perm)
	}

	di, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if perm := di.Mode().Perm(); perm != 0700 {
		t.Errorf("token dir mode = %04o, want 0700", perm)
	}
}

// Re-provisioning must not leave the old session behind, or a device up for
// a month accumulates a session per refresh.
func TestReprovisionRevokesPreviousToken(t *testing.T) {
	m := testManager(t)
	path := filepath.Join(t.TempDir(), "local.token")

	if err := m.ProvisionLocalToken(path); err != nil {
		t.Fatalf("first provision: %v", err)
	}
	first, _ := ReadLocalToken(path)

	if err := m.ProvisionLocalToken(path); err != nil {
		t.Fatalf("second provision: %v", err)
	}
	second, _ := ReadLocalToken(path)

	if first == second {
		t.Fatal("re-provisioning reused the same token")
	}
	if _, err := m.ValidateToken(first); err == nil {
		t.Error("the replaced token still authenticates; it should be revoked")
	}
	if _, err := m.ValidateToken(second); err != nil {
		t.Errorf("the current token does not authenticate: %v", err)
	}
}

// This is the behaviour that makes KeepLocalTokenFresh necessary rather than
// optional. The local credential is an ORDINARY session, so changing the
// admin password revokes it along with every other. If this test ever starts
// failing because the token survives, the credential has stopped being
// ordinary and the comment in localcred.go is no longer true.
func TestLocalTokenIsRevokedWithAllSessions(t *testing.T) {
	m := testManager(t)
	path := filepath.Join(t.TempDir(), "local.token")
	if err := m.ProvisionLocalToken(path); err != nil {
		t.Fatalf("ProvisionLocalToken: %v", err)
	}
	tok, _ := ReadLocalToken(path)

	if err := m.Store.SetPassword("admin", "initial-password-ok"); err != nil {
		t.Fatalf("SetPassword: %v", err)
	}
	if err := m.ChangePassword(nil, "admin", "initial-password-ok", "another-password-ok"); err != nil { //nolint:staticcheck // nil ctx is unused by ChangePassword
		t.Fatalf("ChangePassword: %v", err)
	}

	if _, err := m.ValidateToken(tok); err == nil {
		t.Fatal("local token survived RevokeAll: it is no longer an ordinary session")
	}

	// ...and a refresh repairs it, which is what the timer does on the device.
	if err := m.ProvisionLocalToken(path); err != nil {
		t.Fatalf("re-provision after revoke: %v", err)
	}
	fresh, _ := ReadLocalToken(path)
	if _, err := m.ValidateToken(fresh); err != nil {
		t.Errorf("re-provisioned token does not authenticate: %v", err)
	}
}

// The CLI calls this on every invocation, including on a laptop where the
// file will never exist. That must not be an error.
func TestReadLocalTokenMissingFileIsNotAnError(t *testing.T) {
	tok, err := ReadLocalToken(filepath.Join(t.TempDir(), "absent"))
	if err != nil {
		t.Errorf("missing file returned an error: %v", err)
	}
	if tok != "" {
		t.Errorf("missing file returned token %q, want empty", tok)
	}
}

func TestRemoveLocalToken(t *testing.T) {
	m := testManager(t)
	path := filepath.Join(t.TempDir(), "local.token")
	if err := m.ProvisionLocalToken(path); err != nil {
		t.Fatalf("ProvisionLocalToken: %v", err)
	}
	tok, _ := ReadLocalToken(path)

	if err := m.RemoveLocalToken(); err != nil {
		t.Fatalf("RemoveLocalToken: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("token file still present after RemoveLocalToken")
	}
	if _, err := m.ValidateToken(tok); err == nil {
		t.Error("token still authenticates after RemoveLocalToken")
	}
	// Idempotent.
	if err := m.RemoveLocalToken(); err != nil {
		t.Errorf("second RemoveLocalToken: %v", err)
	}
}

// If a human could register this name, the session list would not let you
// tell a script apart from a user who picked a confusing name.
func TestLocalUsernameIsReserved(t *testing.T) {
	m := testManager(t)
	if err := m.Store.SetPassword(LocalUsername, "a-long-enough-password"); err == nil {
		t.Fatalf("SetPassword accepted the reserved name %q", LocalUsername)
	}
	// Ordinary names still work.
	if err := m.Store.SetPassword("admin", "a-long-enough-password"); err != nil {
		t.Errorf("SetPassword rejected an ordinary name: %v", err)
	}
}
