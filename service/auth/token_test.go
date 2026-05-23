package auth

import (
	"context"
	"strings"
	"testing"
	"time"
)

// fixedClock returns a Sessions with a controllable clock. Tests use it to
// avoid time.Sleep -- expiry behaviour is verified by stepping the clock
// forward, not by waiting in real time.
func fixedClock(t *testing.T) (*Sessions, *time.Time) {
	t.Helper()
	now := time.Date(2026, time.May, 23, 12, 0, 0, 0, time.UTC)
	s := NewSessions()
	s.now = func() time.Time { return now }
	return s, &now
}

func TestSessions_MintAndLookup(t *testing.T) {
	s, _ := fixedClock(t)
	sess, err := s.Mint("admin", time.Hour)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	if sess.Token == "" {
		t.Fatal("Mint returned empty token")
	}
	if sess.Username != "admin" {
		t.Fatalf("session username = %q, want admin", sess.Username)
	}

	got, err := s.Lookup(sess.Token, 0)
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if got.Token != sess.Token {
		t.Fatal("Lookup returned a different session")
	}
}

func TestSessions_UnknownTokenRejected(t *testing.T) {
	s, _ := fixedClock(t)
	if _, err := s.Lookup("never-issued-this", 0); err != ErrInvalidToken {
		t.Fatalf("expected ErrInvalidToken, got %v", err)
	}
}

func TestSessions_TokenIsRandom(t *testing.T) {
	// 100 mints, all distinct, all base64url-encoded of ~32-43 chars.
	s := NewSessions()
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		sess, err := s.Mint("admin", time.Hour)
		if err != nil {
			t.Fatalf("Mint: %v", err)
		}
		if seen[sess.Token] {
			t.Fatalf("duplicate token after %d mints: %s", i, sess.Token)
		}
		seen[sess.Token] = true
		if len(sess.Token) < 32 || len(sess.Token) > 64 {
			t.Fatalf("token length looks wrong: %d (%q)", len(sess.Token), sess.Token)
		}
		// base64url alphabet is [A-Za-z0-9_-]
		for _, c := range sess.Token {
			ok := (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') ||
				(c >= '0' && c <= '9') || c == '_' || c == '-'
			if !ok {
				t.Fatalf("token contains non-base64url char %q: %s", c, sess.Token)
			}
		}
	}
}

func TestSessions_ExpiredTokenRejected(t *testing.T) {
	s, now := fixedClock(t)
	sess, _ := s.Mint("admin", time.Minute)

	// Step time past expiry.
	*now = now.Add(2 * time.Minute)
	if _, err := s.Lookup(sess.Token, 0); err != ErrInvalidToken {
		t.Fatalf("expected ErrInvalidToken on expired session, got %v", err)
	}
	// Subsequent lookup should still reject (session was evicted).
	if _, err := s.Lookup(sess.Token, 0); err != ErrInvalidToken {
		t.Fatal("subsequent lookup of expired token should still be rejected")
	}
}

func TestSessions_SlidingExpiry(t *testing.T) {
	s, now := fixedClock(t)
	sess, _ := s.Mint("admin", time.Hour)
	originalExpiry := sess.Expires

	// Step ahead 30 min and Lookup with ttl=1h -- should slide expiry to
	// now+1h, i.e. 1.5h after issue.
	*now = now.Add(30 * time.Minute)
	got, err := s.Lookup(sess.Token, time.Hour)
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if !got.Expires.After(originalExpiry) {
		t.Fatalf("expected sliding expiry to push Expires forward, was %v now %v",
			originalExpiry, got.Expires)
	}
	// Specifically, new expiry should be now + 1h.
	want := now.Add(time.Hour)
	if !got.Expires.Equal(want) {
		t.Fatalf("expected expiry %v, got %v", want, got.Expires)
	}
}

func TestSessions_ZeroTTLDoesNotSlide(t *testing.T) {
	s, now := fixedClock(t)
	sess, _ := s.Mint("admin", time.Hour)
	originalExpiry := sess.Expires

	*now = now.Add(30 * time.Minute)
	got, err := s.Lookup(sess.Token, 0)
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if !got.Expires.Equal(originalExpiry) {
		t.Fatalf("Lookup with ttl=0 should NOT slide expiry, was %v now %v",
			originalExpiry, got.Expires)
	}
}

func TestSessions_Revoke(t *testing.T) {
	s, _ := fixedClock(t)
	sess, _ := s.Mint("admin", time.Hour)
	s.Revoke(sess.Token)
	if _, err := s.Lookup(sess.Token, 0); err != ErrInvalidToken {
		t.Fatal("revoked token should be rejected")
	}
	// Revoking again is a no-op (idempotent).
	s.Revoke(sess.Token)
}

func TestSessions_RevokeAll(t *testing.T) {
	s, _ := fixedClock(t)
	a, _ := s.Mint("admin", time.Hour)
	b, _ := s.Mint("admin", time.Hour)
	c, _ := s.Mint("admin", time.Hour)
	s.RevokeAll()
	for _, sess := range []*Session{a, b, c} {
		if _, err := s.Lookup(sess.Token, 0); err != ErrInvalidToken {
			t.Fatalf("token %s should be rejected after RevokeAll", sess.Token)
		}
	}
}

func TestSessions_PruneExpired(t *testing.T) {
	s, now := fixedClock(t)
	shortSess, _ := s.Mint("admin", time.Minute)
	longSess, _ := s.Mint("admin", time.Hour)

	*now = now.Add(5 * time.Minute) // shortSess expired, longSess still valid

	evicted := s.PruneExpired()
	if evicted != 1 {
		t.Fatalf("PruneExpired evicted %d, want 1", evicted)
	}
	// shortSess gone, longSess still there.
	if _, err := s.Lookup(shortSess.Token, 0); err != ErrInvalidToken {
		t.Fatal("expired session should be gone after Prune")
	}
	if _, err := s.Lookup(longSess.Token, 0); err != nil {
		t.Fatalf("non-expired session should survive Prune: %v", err)
	}
}

func TestContextSession_Roundtrip(t *testing.T) {
	sess := &Session{Token: "abc", Username: "admin"}
	ctx := ContextWithSession(context.Background(), sess)
	got, ok := SessionFromContext(ctx)
	if !ok {
		t.Fatal("SessionFromContext returned ok=false on a context that has one")
	}
	if got != sess {
		t.Fatal("SessionFromContext returned a different session pointer")
	}

	// And the negative case.
	if _, ok := SessionFromContext(context.Background()); ok {
		t.Fatal("empty context should not have a session")
	}
}

func TestGenerateToken_AlphabetIsBase64URL(t *testing.T) {
	tok, err := generateToken()
	if err != nil {
		t.Fatalf("generateToken: %v", err)
	}
	if strings.ContainsAny(tok, "+/=") {
		t.Fatalf("token should be raw base64url (no +/=), got %q", tok)
	}
}
