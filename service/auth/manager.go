package auth

import (
	"context"
	"sync"
	"time"
)

// Manager is the single integration point used by both the HTTP handler and
// the gRPC interceptors. Construct one at service start and pass it around.
type Manager struct {
	Store    *Store
	Sessions *Sessions

	// ttl is the session lifetime applied to fresh logins and to slide-
	// forward refreshes.
	ttl time.Duration

	// stopPruner signals the GC goroutine to exit. Closed once by Close.
	stopPruner chan struct{}
	closeOnce  sync.Once
}

// NewManager wires up a Manager from a Store and Sessions. Starts a
// background goroutine that prunes expired sessions every minute.
func NewManager(store *Store, sessions *Sessions, ttl time.Duration) *Manager {
	if ttl <= 0 {
		ttl = DefaultSessionTTL
	}
	m := &Manager{
		Store:      store,
		Sessions:   sessions,
		ttl:        ttl,
		stopPruner: make(chan struct{}),
	}
	go m.runPruner()
	return m
}

// runPruner is the background GC. Exits when Close is called.
func (m *Manager) runPruner() {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			m.Sessions.PruneExpired()
		case <-m.stopPruner:
			return
		}
	}
}

// Close signals the pruner to exit. Idempotent.
func (m *Manager) Close() {
	m.closeOnce.Do(func() { close(m.stopPruner) })
}

// Login verifies credentials and returns a new session token on success.
// Sleeps for FailedLoginDelay on failure -- caller doesn't need to add
// extra delay.
func (m *Manager) Login(_ context.Context, username, password string) (*Session, error) {
	if !m.Store.Verify(username, password) {
		time.Sleep(FailedLoginDelay)
		return nil, ErrInvalidCredentials
	}
	return m.Sessions.Mint(username, m.ttl)
}

// ChangePassword verifies the old password, sets the new one, and revokes
// all existing sessions (forcing every client to log in again with the new
// password).
func (m *Manager) ChangePassword(_ context.Context, username, oldPassword, newPassword string) error {
	if !m.Store.Verify(username, oldPassword) {
		time.Sleep(FailedLoginDelay)
		return ErrInvalidCredentials
	}
	if err := m.Store.SetPassword(username, newPassword); err != nil {
		return err
	}
	m.Sessions.RevokeAll()
	return nil
}

// ValidateToken is the hot path used by every gRPC and HTTP request that
// needs auth. Returns the session on success (with the expiry slid forward
// by ttl). Returns ErrInvalidToken otherwise.
func (m *Manager) ValidateToken(token string) (*Session, error) {
	return m.Sessions.Lookup(token, m.ttl)
}
