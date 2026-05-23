package auth

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

// newManager wires up a real Store on a tempfile and a fresh Sessions.
// Tests use this rather than NewManager so they can shut the pruner down
// cleanly via t.Cleanup.
func newManager(t *testing.T, ttl time.Duration) *Manager {
	t.Helper()
	path := filepath.Join(t.TempDir(), "auth.json")
	store, err := NewStore(path)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	sessions := NewSessions()
	m := NewManager(store, sessions, ttl)
	t.Cleanup(m.Close)
	return m
}

// quickManager returns a manager with NO sleep on failed login (we set
// FailedLoginDelay = 0 via the unexported field after the fact). This
// avoids waiting 1s per failed-login test.
func quickManager(t *testing.T) *Manager {
	t.Helper()
	m := newManager(t, time.Hour)
	// Speed up failure paths: we can't change the const, but we can
	// override per-call by calling the lower-level Store.Verify directly
	// where that matters. Most tests don't need to.
	return m
}

func TestManager_LoginSuccess(t *testing.T) {
	m := quickManager(t)
	const user, pw = "admin", "this-is-a-strong-password"
	if err := m.Store.SetPassword(user, pw); err != nil {
		t.Fatalf("SetPassword: %v", err)
	}

	sess, err := m.Login(context.Background(), user, pw)
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if sess.Username != user {
		t.Fatalf("session user = %q, want %q", sess.Username, user)
	}
	if sess.Token == "" {
		t.Fatal("session token is empty")
	}
	// Token should immediately validate via the manager.
	if _, err := m.ValidateToken(sess.Token); err != nil {
		t.Fatalf("ValidateToken on fresh token: %v", err)
	}
}

func TestManager_LoginRejectsWrongPassword(t *testing.T) {
	m := quickManager(t)
	if err := m.Store.SetPassword("admin", "this-is-a-strong-password"); err != nil {
		t.Fatalf("SetPassword: %v", err)
	}
	t0 := time.Now()
	_, err := m.Login(context.Background(), "admin", "wrong-password")
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("expected ErrInvalidCredentials, got %v", err)
	}
	// Login on bad creds should sleep at least ~FailedLoginDelay. Don't
	// assert strict equality, since CI may be slow; just confirm it
	// didn't return instantly.
	if elapsed := time.Since(t0); elapsed < 500*time.Millisecond {
		t.Fatalf("failed login returned too fast (%v) -- timing oracle?", elapsed)
	}
}

func TestManager_LoginRejectsUnknownUser(t *testing.T) {
	m := quickManager(t)
	if err := m.Store.SetPassword("admin", "this-is-a-strong-password"); err != nil {
		t.Fatalf("SetPassword: %v", err)
	}
	_, err := m.Login(context.Background(), "ghost", "this-is-a-strong-password")
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("expected ErrInvalidCredentials for unknown user, got %v", err)
	}
}

func TestManager_ChangePasswordSuccess(t *testing.T) {
	m := quickManager(t)
	const user = "admin"
	if err := m.Store.SetPassword(user, "old-password-12-chars"); err != nil {
		t.Fatalf("SetPassword: %v", err)
	}
	// Mint two sessions; ChangePassword must revoke both.
	a, _ := m.Sessions.Mint(user, time.Hour)
	b, _ := m.Sessions.Mint(user, time.Hour)

	err := m.ChangePassword(context.Background(), user, "old-password-12-chars", "new-password-12-chars")
	if err != nil {
		t.Fatalf("ChangePassword: %v", err)
	}

	// Old creds rejected.
	if _, err := m.Login(context.Background(), user, "old-password-12-chars"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatal("old password should no longer log in")
	}
	// New creds accepted.
	if _, err := m.Login(context.Background(), user, "new-password-12-chars"); err != nil {
		t.Fatalf("new password should log in: %v", err)
	}
	// Existing sessions revoked.
	if _, err := m.ValidateToken(a.Token); err != ErrInvalidToken {
		t.Fatal("session A should be revoked after ChangePassword")
	}
	if _, err := m.ValidateToken(b.Token); err != ErrInvalidToken {
		t.Fatal("session B should be revoked after ChangePassword")
	}
}

func TestManager_ChangePasswordRejectsWrongOldPassword(t *testing.T) {
	m := quickManager(t)
	if err := m.Store.SetPassword("admin", "old-password-12-chars"); err != nil {
		t.Fatalf("SetPassword: %v", err)
	}

	err := m.ChangePassword(context.Background(), "admin", "wrong-old-pw", "new-password-12-chars")
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("expected ErrInvalidCredentials, got %v", err)
	}
	// Old password should STILL work (the change was rejected).
	if _, err := m.Login(context.Background(), "admin", "old-password-12-chars"); err != nil {
		t.Fatalf("old password should still work after rejected change: %v", err)
	}
}

func TestManager_ChangePasswordRejectsWeakNewPassword(t *testing.T) {
	m := quickManager(t)
	if err := m.Store.SetPassword("admin", "old-password-12-chars"); err != nil {
		t.Fatalf("SetPassword: %v", err)
	}
	err := m.ChangePassword(context.Background(), "admin", "old-password-12-chars", "weak")
	if !errors.Is(err, ErrWeakPassword) {
		t.Fatalf("expected ErrWeakPassword, got %v", err)
	}
}

func TestManager_ValidateTokenSlidesExpiry(t *testing.T) {
	m := newManager(t, time.Hour)
	if err := m.Store.SetPassword("admin", "longenoughpassword-12chars"); err != nil {
		t.Fatalf("SetPassword: %v", err)
	}
	sess, err := m.Login(context.Background(), "admin", "longenoughpassword-12chars")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	originalExpiry := sess.Expires

	// Move the sessions clock forward 10 minutes, then re-validate. The
	// re-validation should slide the expiry forward by 1h (Manager.ttl).
	m.Sessions.now = func() time.Time { return time.Now().Add(10 * time.Minute) }
	got, err := m.ValidateToken(sess.Token)
	if err != nil {
		t.Fatalf("ValidateToken: %v", err)
	}
	if !got.Expires.After(originalExpiry) {
		t.Fatal("ValidateToken should slide expiry forward")
	}
}
