// Package auth implements authentication for the P4wnP1 gRPC API and HTTP
// surface. It is deliberately single-user-oriented (one admin account on a
// physical device) and uses opaque random tokens with an in-memory session
// map rather than stateless JWTs.
//
// Wire diagram:
//
//	HTTP request                              gRPC request
//	     |                                          |
//	     v                                          v
//	+----------+   /api/auth/login -> Handler   +-----------------+
//	| HTTP     | -- /api/auth/whoami            | Unary/Stream    |
//	| router   | -- static SPA (no auth)        | Interceptor     |
//	+----------+                                +-----------------+
//	     |                                          |
//	     v                                          v
//	  Other paths -> gRPC-web wrapper -> +-----------------+
//	                                    | grpc.Server     |
//	                                    | (interceptors)  |
//	                                    +-----------------+
//	                                          |
//	                                          v
//	                                       Manager
//	                                       /  |   \
//	                                  Store  Sessions
//	                                  (file)  (memory)
//
// Manager is the single entrypoint -- construct one at service start and
// share it between the HTTP handler and the gRPC interceptors.
package auth

import (
	"errors"
	"time"
)

const (
	// DefaultSessionTTL is how long a freshly issued token stays valid
	// without use. Sliding-window: each successful auth refreshes the
	// expiry.
	DefaultSessionTTL = 24 * time.Hour

	// FailedLoginDelay is how long we sleep on a failed login before
	// replying. Plain rate-limit-by-delay -- a single physical attacker
	// can hammer all they like, but no useful brute-force throughput.
	FailedLoginDelay = 1 * time.Second

	// MetadataAuthKey is the gRPC metadata key the interceptor reads
	// the token from. Per gRPC convention (and so the JS client doesn't
	// have to special-case anything), the canonical "Authorization"
	// header maps to this lowercase key.
	MetadataAuthKey = "authorization"

	// HTTPAuthHeader is the HTTP equivalent.
	HTTPAuthHeader = "Authorization"

	// BearerPrefix is the prefix on the metadata value. We accept both
	// "Bearer " and "bearer " (case-insensitive on the prefix).
	BearerPrefix = "bearer "
)

// Errors exposed from the package. Callers compare via errors.Is.
var (
	ErrUnauthenticated   = errors.New("auth: unauthenticated")
	ErrInvalidToken      = errors.New("auth: invalid or expired token")
	ErrInvalidCredentials = errors.New("auth: invalid username or password")
	ErrUserNotFound      = errors.New("auth: user not found")
	ErrUserExists        = errors.New("auth: user already exists")
	ErrWeakPassword      = errors.New("auth: password too weak (need 12+ chars)")
)

// Session is the in-memory record of a successful login.
type Session struct {
	Token    string
	Username string
	IssuedAt time.Time
	Expires  time.Time
}

// ctxKey is unexported so callers must use the typed accessor below.
type ctxKey struct{}

// sessionContextKey is the singleton context key used by interceptors to
// stash the authenticated session for downstream handlers.
var sessionContextKey = ctxKey{}
