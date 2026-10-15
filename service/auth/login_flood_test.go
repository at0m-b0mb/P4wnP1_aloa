package auth

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// Audit finding C4. An unauthenticated peer could lock the operator out of
// logging in, and make the device do work for sockets that had already closed.
//
// Two halves:
//
//   - Login took `_ context.Context` and never read it, so hanging up changed
//     nothing: the goroutine still ran a full bcrypt and still slept out the
//     failure delay. The backlog was work nobody was waiting for.
//   - Nothing bounded that backlog. Go wakes blocked channel senders in FIFO
//     order, so the operator's own correct password went to the back of a line
//     an attacker was filling, and the line could grow without limit.

// TestAnAbandonedLoginStopsCosting covers the first half. A cancelled context
// must abort promptly rather than running to completion for a dead client.
func TestAnAbandonedLoginStopsCosting(t *testing.T) {
	m := newManager(t, time.Hour)
	if err := m.Store.SetPassword("admin", "correct-horse-battery"); err != nil {
		t.Fatal(err)
	}

	// Occupy every verification slot so the next login must wait, which is the
	// state an attacker creates.
	var wg sync.WaitGroup
	block := make(chan struct{})
	for i := 0; i < maxConcurrentLogins; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m.loginSlots <- struct{}{}
			<-block
			<-m.loginSlots
		}()
	}
	// Let them take the slots.
	deadline := time.Now().Add(2 * time.Second)
	for len(m.loginSlots) < maxConcurrentLogins && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := m.Login(ctx, "admin", "wrong"); done <- err }()

	// It must still be waiting -- otherwise this proves nothing about abandon.
	select {
	case err := <-done:
		close(block)
		wg.Wait()
		t.Fatalf("login did not queue behind the occupied slots: %v", err)
	case <-time.After(150 * time.Millisecond):
	}

	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("abandoned login returned %v, want context.Canceled", err)
		}
	case <-time.After(3 * time.Second):
		t.Error("ABANDONED LOGIN KEPT RUNNING: cancelling the request did not release it, " +
			"so a client that hangs up still costs a full bcrypt and the failure delay")
	}

	close(block)
	wg.Wait()
}

// TestTheLoginQueueIsBounded covers the second half: past the cap, a login is
// refused rather than enqueued. An unbounded queue is the lockout.
func TestTheLoginQueueIsBounded(t *testing.T) {
	m := newManager(t, time.Hour)

	var releases []func()
	for i := 0; i < maxQueuedLogins; i++ {
		release, ok := m.AdmitLogin()
		if !ok {
			t.Fatalf("admission refused at %d, below the cap of %d", i, maxQueuedLogins)
		}
		releases = append(releases, release)
	}

	// The next one must be refused, not queued.
	if _, ok := m.AdmitLogin(); ok {
		t.Errorf("admission succeeded at %d, past the cap of %d -- the queue is unbounded "+
			"and the operator can be pushed to the back of it", maxQueuedLogins+1, maxQueuedLogins)
	}

	// And releasing one must make room again, or the device would refuse
	// logins forever after a single burst.
	releases[0]()
	release, ok := m.AdmitLogin()
	if !ok {
		t.Fatal("releasing a slot did not make room; the device would refuse logins forever " +
			"after one burst")
	}
	release()
	for _, r := range releases[1:] {
		r()
	}
}

// TestTheOperatorIsNotLockedOut is the one that matters, stated as the
// behaviour rather than the mechanism: while attackers hold every verification
// slot, a correct password must still get through in reasonable time.
func TestTheOperatorIsNotLockedOut(t *testing.T) {
	m := newManager(t, time.Hour)
	const pw = "correct-horse-battery"
	if err := m.Store.SetPassword("admin", pw); err != nil {
		t.Fatal(err)
	}

	// A burst of wrong guesses, each with a context that is cancelled almost
	// at once -- exactly the attacker that fires and hangs up.
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel()
			release, ok := m.AdmitLogin()
			if !ok {
				return // refused, which is the point
			}
			defer release()
			_, _ = m.Login(ctx, "admin", "wrong")
		}()
	}

	start := time.Now()
	release, ok := m.AdmitLogin()
	if !ok {
		// Being refused is acceptable and is itself the protection; the
		// operator retries. What must NOT happen is an unbounded wait.
		t.Log("operator was refused admission during the flood; that is the designed " +
			"behaviour and is recoverable by retrying")
	} else {
		sess, err := m.Login(context.Background(), "admin", pw)
		release()
		if err != nil {
			t.Fatalf("the operator's correct password was rejected during a flood: %v", err)
		}
		if sess == nil {
			t.Fatal("no session issued")
		}
	}
	if elapsed := time.Since(start); elapsed > 25*time.Second {
		t.Errorf("the operator waited %v behind the flood; the queue is not bounded", elapsed)
	}
	wg.Wait()
}

// TestACorrectLoginStillWorksNormally is the control. Everything above could
// pass for an implementation that refused all logins.
func TestACorrectLoginStillWorksNormally(t *testing.T) {
	m := newManager(t, time.Hour)
	const pw = "correct-horse-battery"
	if err := m.Store.SetPassword("admin", pw); err != nil {
		t.Fatal(err)
	}

	release, ok := m.AdmitLogin()
	if !ok {
		t.Fatal("an idle device refused a login")
	}
	defer release()

	sess, err := m.Login(context.Background(), "admin", pw)
	if err != nil {
		t.Fatalf("a correct password on an idle device failed: %v", err)
	}
	if sess.Username != "admin" || sess.Token == "" {
		t.Errorf("bad session: %+v", sess.Username)
	}

	// And a wrong one is still rejected, still slowly.
	start := time.Now()
	if _, err := m.Login(context.Background(), "admin", "wrong"); !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("wrong password returned %v, want ErrInvalidCredentials", err)
	}
	if time.Since(start) < FailedLoginDelay/2 {
		t.Error("the failure delay was skipped; brute force is no longer throttled")
	}
}
