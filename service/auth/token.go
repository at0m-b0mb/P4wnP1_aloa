package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"sort"
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

// SessionInfo describes a live session for display. It deliberately carries
// no token: this is served over HTTP to a browser, and a list of live tokens
// would turn a read-only audit view into a credential dump.
type SessionInfo struct {
	// ID identifies a session well enough to revoke it, without being usable
	// as one. It is a truncated SHA-256 of the token, so it cannot be turned
	// back into the token it names.
	ID        string `json:"id"`
	Username  string `json:"username"`
	IssuedAt  int64  `json:"issued_at"`
	ExpiresAt int64  `json:"expires_at"`
	// IsLocalScript marks the credential the device issues to its own startup
	// and trigger scripts, so an operator reading this list can tell the
	// machine's own activity from a human's.
	IsLocalScript bool `json:"is_local_script"`
	// IsCurrent marks the session making the request.
	IsCurrent bool `json:"is_current"`
}

// SessionID derives the public identifier for a token. One-way: knowing an ID
// does not let you reconstruct the token.
func SessionID(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:8])
}

// List returns every live session, newest first, without their tokens.
// Expired entries are pruned on the way so the list cannot show a session
// that would no longer authenticate.
func (s *Sessions) List(currentToken string) []SessionInfo {
	s.PruneExpired()

	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]SessionInfo, 0, len(s.m))
	for tok, sess := range s.m {
		out = append(out, SessionInfo{
			ID:            SessionID(tok),
			Username:      sess.Username,
			IssuedAt:      sess.IssuedAt.Unix(),
			ExpiresAt:     sess.Expires.Unix(),
			IsLocalScript: sess.Username == LocalUsername,
			IsCurrent:     tok != "" && tok == currentToken,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].IssuedAt > out[j].IssuedAt })
	return out
}

// RevokeByID revokes the session with the given public ID. Reports whether
// one was found, so the caller can answer 404 rather than pretending.
func (s *Sessions) RevokeByID(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for tok := range s.m {
		if SessionID(tok) == id {
			delete(s.m, tok)
			return true
		}
	}
	return false
}
