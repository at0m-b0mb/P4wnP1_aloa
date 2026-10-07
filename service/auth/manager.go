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

	// local tracks the machine-local credential issued to the device's own
	// scripts. See localcred.go for why it is kept out of Sessions, and why
	// it belongs here rather than at package scope.
	local localTokenState

	// failedLogin serialises the delay on a rejected login. See Login.
	failedLogin sync.Mutex

	// loginSlots bounds how many logins may be verifying AT ONCE. See Login.
	loginSlots chan struct{}
}

// maxConcurrentLogins caps simultaneous password verifications.
//
// Serialising the post-failure delay stops an attacker getting more than one
// GUESS per delay, but it does nothing about the cost of each attempt:
// bcrypt at cost 12 takes roughly 250ms of CPU on a Pi Zero W, and it runs
// BEFORE the delay. Fifty parallel attempts therefore still bought fifty
// concurrent bcrypts on a single-core 1GHz board whose job at that moment may
// be typing keystrokes into a host. The rate limit was real; the resource
// limit was missing.
//
// Two slots, not one: the operator and whatever they have open elsewhere
// should not queue behind each other, and two concurrent bcrypts is a
// manageable load where fifty is not.
const maxConcurrentLogins = 2

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
		loginSlots: make(chan struct{}, maxConcurrentLogins),
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
	// Hold a slot across the password check itself, so the number of bcrypt
	// hashes running at once is bounded no matter how many connections an
	// attacker opens. Released before the failure delay below, which has its
	// own lock -- otherwise a slow attacker would also block the operator.
	if m.loginSlots != nil {
		m.loginSlots <- struct{}{}
	}
	ok := m.Store.Verify(username, password)
	if m.loginSlots != nil {
		<-m.loginSlots
	}

	if !ok {
		// Hold a lock across the delay. Sleeping alone throttles nothing:
		// each request sleeps in its own goroutine, so twenty parallel
		// guesses cost about one second in total rather than twenty, and the
		// "no useful brute-force throughput" the delay was meant to provide
		// was not being provided at all. Serialising rejections caps the rate
		// at one guess per FailedLoginDelay no matter how many connections
		// an attacker opens.
		//
		// Only REJECTED logins take the lock, so a correct password is never
		// made to queue behind an attacker.
		m.failedLogin.Lock()
		time.Sleep(FailedLoginDelay)
		m.failedLogin.Unlock()
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
	// RevokeAll has just destroyed the device's own script credential along
	// with every human session. Re-issue it now rather than leaving the
	// device unable to drive itself until the refresh timer next fires.
	m.ReprovisionLocalTokenIfConfigured()
	return nil
}

// ValidateToken is the hot path used by every gRPC and HTTP request that
// needs auth. Returns the session on success (with the expiry slid forward
// by ttl). Returns ErrInvalidToken otherwise.
func (m *Manager) ValidateToken(token string) (*Session, error) {
	return m.Sessions.Lookup(token, m.ttl)
}
