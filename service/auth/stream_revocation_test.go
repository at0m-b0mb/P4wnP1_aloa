package auth

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// Audit finding C8, confirmed by both skeptics.
//
// A gRPC stream authenticated once, at open, and never again. EventListen then
// blocks for the life of the TCP connection, so revoking the token -- or
// changing the admin password, which calls RevokeAll -- did not stop the feed.
// Nor did expiry, since the TTL is only enforced inside Sessions.Lookup, which
// a live stream never called.
//
// What makes it worse than a leak is that it fails SILENTLY in the operator's
// favour: the revoke returns 204, the console's session list then omits the
// session, and the operator is told the intruder is gone while the stream is
// still delivering DHCP leases, HID job results and trigger fires.
//
// The JSON API's event stream was fixed for exactly this, on a ticker. These
// tests hold the gRPC sibling to the same rule.

// fakeStream is the least ServerStream that satisfies the interceptor.
type fakeStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (f *fakeStream) Context() context.Context { return f.ctx }

// withShortRecheck makes the ticker fast enough to test and restores it after.
func withShortRecheck(t *testing.T, d time.Duration) {
	t.Helper()
	old := streamRecheckInterval
	streamRecheckInterval = d
	t.Cleanup(func() { streamRecheckInterval = old })
}

func bearerCtx(tok string) context.Context {
	return metadata.NewIncomingContext(context.Background(),
		metadata.Pairs(MetadataAuthKey, "Bearer "+tok))
}

// runStream starts the interceptor around a handler that blocks until its
// context is cancelled, and reports how it ended.
func runStream(t *testing.T, m *Manager, tok string) (done chan error, entered chan struct{}) {
	t.Helper()
	done = make(chan error, 1)
	entered = make(chan struct{})

	interceptor := StreamInterceptor(m)
	go func() {
		done <- interceptor(nil, &fakeStream{ctx: bearerCtx(tok)}, &grpc.StreamServerInfo{},
			func(srv interface{}, ss grpc.ServerStream) error {
				close(entered)
				// Exactly what EventListen does: block on the stream context.
				<-ss.Context().Done()
				return ss.Context().Err()
			})
	}()
	return done, entered
}

func TestRevokingASessionClosesItsLiveStream(t *testing.T) {
	withShortRecheck(t, 20*time.Millisecond)
	m := newManager(t, time.Hour)

	sess, err := m.Sessions.Mint("admin", time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	done, entered := runStream(t, m, sess.Token)
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("the handler never started; the stream did not authenticate")
	}

	// It must still be running before we revoke, or the test proves nothing
	// about revocation -- it would pass for a stream that never ran at all.
	select {
	case err := <-done:
		t.Fatalf("the stream ended before the session was revoked: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	m.Sessions.Revoke(sess.Token)

	select {
	case <-done:
		// Correct: the recheck noticed and cancelled.
	case <-time.After(3 * time.Second):
		t.Fatal("REVOKED SESSION STILL STREAMING: the stream outlived its session")
	}
}

// TestChangingThePasswordClosesLiveStreams covers the operator's actual
// incident response. ChangePassword calls RevokeAll, and if that does not reach
// an open stream then the one action an operator takes when they think they are
// compromised is the one that does not work.
func TestChangingThePasswordClosesLiveStreams(t *testing.T) {
	withShortRecheck(t, 20*time.Millisecond)
	m := newManager(t, time.Hour)

	sess, err := m.Sessions.Mint("admin", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	done, entered := runStream(t, m, sess.Token)
	<-entered

	m.Sessions.RevokeAll()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("RevokeAll did not reach a live stream")
	}
}

// TestAnExpiredSessionClosesItsLiveStream covers the half that is easy to miss.
// Expiry is enforced only inside Lookup, so before the recheck existed a stream
// outlived not just revocation but its own TTL -- unbounded, for as long as the
// TCP connection held.
//
// THE MANAGER TTL IS WHAT MATTERS HERE, NOT THE MINTED ONE. Opening the stream
// calls ValidateToken, which calls Lookup WITH the manager's ttl, and Lookup
// slides the expiry forward on every successful call -- sessions are rolling.
// So a short-lived token handed to a stream is immediately refreshed to the
// manager's TTL, and an earlier version of this test minted a 50ms session
// against a 1h manager and then waited in vain.
//
// The recheck deliberately passes ttl=0, so it does NOT slide. That is the
// property under test: a stream cannot keep its own session alive by existing.
func TestAnExpiredSessionClosesItsLiveStream(t *testing.T) {
	withShortRecheck(t, 20*time.Millisecond)
	m := newManager(t, 250*time.Millisecond)

	sess, err := m.Sessions.Mint("admin", 250*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	done, entered := runStream(t, m, sess.Token)
	<-entered

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("an EXPIRED session's stream kept running -- the recheck is sliding the " +
			"expiry forward instead of just reading it")
	}
}

// TestAValidStreamIsNotClosed is the other direction, and it is not optional:
// an interceptor that cancelled everything would pass all three tests above and
// break the console's event feed entirely.
func TestAValidStreamIsNotClosed(t *testing.T) {
	withShortRecheck(t, 10*time.Millisecond)
	m := newManager(t, time.Hour)

	sess, err := m.Sessions.Mint("admin", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	done, entered := runStream(t, m, sess.Token)
	<-entered

	// Many recheck intervals must pass without the stream being disturbed.
	select {
	case err := <-done:
		t.Fatalf("a VALID session's stream was closed after %d rechecks: %v", 50, err)
	case <-time.After(500 * time.Millisecond):
		// Correct.
	}
}

// TestAStreamWithNoTokenNeverStarts keeps the original guarantee intact.
func TestAStreamWithNoTokenNeverStarts(t *testing.T) {
	m := newManager(t, time.Hour)
	interceptor := StreamInterceptor(m)

	started := false
	err := interceptor(nil, &fakeStream{ctx: context.Background()}, &grpc.StreamServerInfo{},
		func(srv interface{}, ss grpc.ServerStream) error { started = true; return nil })

	if err == nil {
		t.Error("a stream with no metadata must be refused")
	}
	if started {
		t.Error("the handler ran for an unauthenticated stream")
	}
}
