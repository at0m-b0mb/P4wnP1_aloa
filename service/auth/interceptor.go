package auth

import (
	"context"
	"log"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// UnaryInterceptor returns a grpc.UnaryServerInterceptor that rejects every
// call whose metadata doesn't carry a valid bearer token. The Manager is
// captured in the closure.
//
// Pass to grpc.NewServer(grpc.UnaryInterceptor(...)).
func UnaryInterceptor(m *Manager) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		sess, err := authFromMetadata(ctx, m)
		if err != nil {
			return nil, err
		}
		return handler(ContextWithSession(ctx, sess), req)
	}
}

// streamRecheckInterval is how often a live stream re-reads its session.
//
// It matches the JSON API's event stream (service/rest_api.go), deliberately:
// two long-lived transports onto the same handler should not disagree about how
// quickly a revocation takes effect.
//
// A var rather than a const so the tests can shorten it. A revocation test that
// has to wait 20 seconds is a test nobody runs.
var streamRecheckInterval = 20 * time.Second

// StreamInterceptor returns a grpc.StreamServerInterceptor that rejects
// every stream whose metadata doesn't carry a valid bearer token, and then
// keeps checking for as long as the stream lives.
//
// WHY THE RECHECK EXISTS
//
// Authenticating once at stream open is enough for a unary RPC, which is over
// in milliseconds. It is not enough for EventListen, which blocks for the life
// of the TCP connection: nothing re-read the session map, so revoking a token
// -- or changing the admin password, which calls RevokeAll -- left the revoked
// holder's feed running. Indefinitely, since the 24h TTL is only enforced
// inside Sessions.Lookup, which a live stream never called.
//
// The feed carries DHCP leases with MAC, IP and hostname, HID job results and
// trigger fires, so the holder keeps watching the device. Worse, the control
// fails SILENTLY in the operator's favour: the revoke returns 204 and the
// console's session list then omits it, so the operator is told the intruder is
// gone while the stream is still delivering.
//
// The JSON API's event stream had exactly this hole, found earlier and fixed on
// a ticker, under the comment "Revocation that does not reach the one
// long-lived connection is not revocation." That reasoning covered this sibling
// too; it just was not applied here. Two transports, one handler, one rule.
func StreamInterceptor(m *Manager) grpc.StreamServerInterceptor {
	return func(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		sess, tok, err := authFromMetadataWithToken(ss.Context(), m)
		if err != nil {
			return err
		}

		// Cancelling this context is what tears the stream down: EventListen
		// already selects on its own context's Done channel.
		ctx, cancel := context.WithCancel(ContextWithSession(ss.Context(), sess))
		defer cancel()

		go func() {
			t := time.NewTicker(streamRecheckInterval)
			defer t.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-t.C:
					// Lookup is the only thing that enforces expiry as well as
					// revocation, so this covers both.
					if _, err := m.Sessions.Lookup(tok, 0); err != nil {
						log.Printf("gRPC: stream closed, its session is no longer valid")
						cancel()
						return
					}
				}
			}
		}()

		return handler(srv, &wrappedServerStream{ServerStream: ss, ctx: ctx})
	}
}

// authFromMetadata extracts the bearer token from incoming gRPC metadata
// and validates it via the Manager. Returns a gRPC-statused error on
// failure so the client sees the right code.
func authFromMetadata(ctx context.Context, m *Manager) (*Session, error) {
	sess, _, err := authFromMetadataWithToken(ctx, m)
	return sess, err
}

// authFromMetadataWithToken is authFromMetadata, and also hands back the raw
// token.
//
// StreamInterceptor needs it to re-check the session periodically, and having
// it return the token is better than having the interceptor re-parse the header
// itself: two parsers of the same thing is how they come to disagree about
// which one is authoritative.
func authFromMetadataWithToken(ctx context.Context, m *Manager) (*Session, string, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return nil, "", status.Error(codes.Unauthenticated, "missing metadata")
	}
	vals := md.Get(MetadataAuthKey)
	if len(vals) == 0 {
		return nil, "", status.Error(codes.Unauthenticated, "missing authorization metadata")
	}
	raw := vals[0]
	lower := strings.ToLower(raw)
	if !strings.HasPrefix(lower, BearerPrefix) {
		return nil, "", status.Error(codes.Unauthenticated, "authorization must be 'Bearer <token>'")
	}
	tok := strings.TrimSpace(raw[len(BearerPrefix):])
	sess, err := m.ValidateToken(tok)
	if err != nil {
		return nil, "", status.Error(codes.Unauthenticated, "invalid or expired token")
	}
	return sess, tok, nil
}

// wrappedServerStream is a tiny grpc.ServerStream wrapper that swaps in a
// derived context carrying the authenticated session. Without this, the
// inner handler's calls to ss.Context() would return the original context
// (no session) and downstream code couldn't see who's calling.
type wrappedServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (w *wrappedServerStream) Context() context.Context { return w.ctx }
