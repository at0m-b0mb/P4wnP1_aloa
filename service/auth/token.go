package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"sync"
	"time"
)

// Sessions is an in-memory token -> session map. Service restart wipes it
// (acceptable: the user just logs in again). All callers that hold a
// Manager talk through here.
type Sessions struct {
	mu  sync.RWMutex
	now func() time.Time // injectable for tests; defaults to time.Now
	m   map[string]*Session
}

// NewSessions creates an empty session store with a background GC goroutine
// that prunes expired sessions every minute. Call Close to stop it.
func NewSessions() *Sessions {
	s := &Sessions{
		now: time.Now,
		m:   map[string]*Session{},
	}
	return s
}

// generateToken returns 32 cryptographically random bytes encoded
// base64url-without-padding. ~43 ASCII chars. Long enough that brute force
// is impossible even with unbounded attempts.
func generateToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// Mint creates a new session for the given username and returns the token.
// The session is added to the map and the token-to-session lookup works
// immediately.
func (s *Sessions) Mint(username string, ttl time.Duration) (*Session, error) {
	tok, err := generateToken()
	if err != nil {
		return nil, err
	}
	now := s.now()
	sess := &Session{
		Token:    tok,
		Username: username,
		IssuedAt: now,
		Expires:  now.Add(ttl),
	}
	s.mu.Lock()
	s.m[tok] = sess
	s.mu.Unlock()
	return sess, nil
}

// Lookup returns the session for a token if it exists and is not expired.
// On a successful lookup the session expiry is slid forward by ttl (rolling
// session); pass ttl=0 to skip the slide (useful in WhoAmI calls that
// shouldn't extend the session).
//
// Returns (nil, ErrInvalidToken) for unknown or expired tokens.
func (s *Sessions) Lookup(token string, ttl time.Duration) (*Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.m[token]
	if !ok {
		return nil, ErrInvalidToken
	}
	now := s.now()
	if !now.Before(sess.Expires) {
		// Expired -- evict and report.
		delete(s.m, token)
		return nil, ErrInvalidToken
	}
	if ttl > 0 {
		sess.Expires = now.Add(ttl)
	}
	return sess, nil
}

// Revoke deletes a token. Used by an explicit logout. Idempotent.
func (s *Sessions) Revoke(token string) {
	s.mu.Lock()
	delete(s.m, token)
	s.mu.Unlock()
}

// RevokeAll wipes every session. Used by ChangePassword to force re-login
// across all clients after a password change.
func (s *Sessions) RevokeAll() {
	s.mu.Lock()
	s.m = map[string]*Session{}
	s.mu.Unlock()
}

// PruneExpired walks the map and deletes anything past its expiry. Returns
// the number of sessions evicted. Cheap because the map is tiny in
// practice.
func (s *Sessions) PruneExpired() int {
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	evicted := 0
	for tok, sess := range s.m {
		if !now.Before(sess.Expires) {
			delete(s.m, tok)
			evicted++
		}
	}
	return evicted
}

// ContextWithSession returns a derived context carrying the session. Used
// by interceptors to expose the authenticated user to downstream handlers.
func ContextWithSession(ctx context.Context, sess *Session) context.Context {
	return context.WithValue(ctx, sessionContextKey, sess)
}

// SessionFromContext extracts a session from a context, if present.
// Returns (nil, false) if the context didn't carry one (e.g. unauthenticated
// path).
func SessionFromContext(ctx context.Context) (*Session, bool) {
	sess, ok := ctx.Value(sessionContextKey).(*Session)
	return sess, ok
}
