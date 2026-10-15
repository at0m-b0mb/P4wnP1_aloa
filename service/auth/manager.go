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
	//
	// A BUFFERED CHANNEL, not a sync.Mutex, because acquiring it has to be
	// cancellable. A mutex cannot be selected on, so a request whose client
	// has already hung up would wait for it anyway and then hold it for the
	// full delay on behalf of a closed socket -- which is precisely the
	// backlog an attacker was building in finding C4. Capacity 1 is a mutex
	// that select can wait on.
	failedLogin chan struct{}

	// loginSlots bounds how many logins may be verifying AT ONCE. See Login.
	loginSlots chan struct{}

	// loginQueue bounds how many may be WAITING. See maxQueuedLogins.
	loginQueue chan struct{}
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

// maxQueuedLogins caps how many logins may be WAITING to be verified.
//
// maxConcurrentLogins bounds how many bcrypt hashes run at once; it does not
// bound how many requests pile up behind them, and that queue was unbounded.
// Go wakes blocked channel senders in FIFO order, so the operator's own correct
// password went to the back of a line an attacker was filling -- minutes of
// lockout at a handful of requests per second, growing without limit.
//
// Past this many waiting, a login is REFUSED outright with 503 and a
// Retry-After rather than enqueued. Refusing is what makes the lockout
// impossible: the line can never grow long enough to be worth standing in. A
// legitimate operator never sees it -- it takes sixteen simultaneous logins to
// reach, and there is one of them.
const maxQueuedLogins = 16

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
		loginQueue: make(chan struct{}, maxQueuedLogins),
		// Capacity 1: a mutex that select can wait on. A nil channel would
		// block forever in the select, so this must not be left unset --
		// which is why it is here and not relying on the zero value.
		failedLogin: make(chan struct{}, 1),
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
func (m *Manager) Login(ctx context.Context, username, password string) (*Session, error) {
	// TAKE THE CONTEXT. It used to be `_ context.Context`, and the consequence
	// was that hanging up changed nothing: an attacker could fire requests and
	// close each socket immediately, and every one still ran a full bcrypt and
	// still slept out the failure delay. The queue filled with work nobody was
	// waiting for, on a board with one core.
	//
	// Every blocking step below is now cancellable, so a client that leaves
	// stops costing anything within one step.
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Hold a slot across the password check itself, so the number of bcrypt
	// hashes running at once is bounded no matter how many connections an
	// attacker opens. Released before the failure delay below, which has its
	// own lock -- otherwise a slow attacker would also block the operator.
	//
	// Acquiring is a select, not a bare send: Go wakes blocked channel senders
	// in FIFO order, so without this the operator's own correct login queues
	// behind every attacker request already waiting, and waits
	// (queued / slots) x bcrypt. That is minutes at a few requests per second,
	// and it is unbounded -- which is the lockout half of finding C4.
	// handleLogin caps how many may queue at all; this makes the ones that do
	// give up when their client does.
	if m.loginSlots != nil {
		select {
		case m.loginSlots <- struct{}{}:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
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
		//
		// Both the wait for the lock and the sleep itself are cancellable, for
		// the reason in the context note at the top: a client that hung up
		// should stop occupying the one slot that serialises every rejection.
		// Holding it out for a full second on behalf of a socket that closed
		// is exactly the backlog an attacker was building.
		select {
		case m.failedLogin <- struct{}{}:
			defer func() { <-m.failedLogin }()
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		t := time.NewTimer(FailedLoginDelay)
		defer t.Stop()
		select {
		case <-t.C:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
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

// AdmitLogin reserves a place in the login queue.
//
// Non-blocking ON PURPOSE: the whole point is to refuse rather than enqueue
// once the queue is full, so that the queue can never grow long enough to lock
// the operator out. Returns false when the caller should answer 503.
//
// release is nil when admission was refused, so the caller can defer it
// unconditionally only after checking ok.
func (m *Manager) AdmitLogin() (release func(), ok bool) {
	if m.loginQueue == nil {
		return func() {}, true // a Manager built without a queue does not gate
	}
	select {
	case m.loginQueue <- struct{}{}:
		return func() { <-m.loginQueue }, true
	default:
		return nil, false
	}
}
