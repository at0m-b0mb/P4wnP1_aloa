package auth

import (
	"context"
	"strings"

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

// StreamInterceptor returns a grpc.StreamServerInterceptor that rejects
// every stream whose metadata doesn't carry a valid bearer token.
func StreamInterceptor(m *Manager) grpc.StreamServerInterceptor {
	return func(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		sess, err := authFromMetadata(ss.Context(), m)
		if err != nil {
			return err
		}
		wrapped := &wrappedServerStream{
			ServerStream: ss,
			ctx:          ContextWithSession(ss.Context(), sess),
		}
		return handler(srv, wrapped)
	}
}

// authFromMetadata extracts the bearer token from incoming gRPC metadata
// and validates it via the Manager. Returns a gRPC-statused error on
// failure so the client sees the right code.
func authFromMetadata(ctx context.Context, m *Manager) (*Session, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "missing metadata")
	}
	vals := md.Get(MetadataAuthKey)
	if len(vals) == 0 {
		return nil, status.Error(codes.Unauthenticated, "missing authorization metadata")
	}
	raw := vals[0]
	lower := strings.ToLower(raw)
	if !strings.HasPrefix(lower, BearerPrefix) {
		return nil, status.Error(codes.Unauthenticated, "authorization must be 'Bearer <token>'")
	}
	tok := strings.TrimSpace(raw[len(BearerPrefix):])
	sess, err := m.ValidateToken(tok)
	if err != nil {
		return nil, status.Error(codes.Unauthenticated, "invalid or expired token")
	}
	return sess, nil
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
