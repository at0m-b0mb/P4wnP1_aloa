package service

// rest_api.go -- a JSON/HTTP face for the whole gRPC service.
//
// Mounted under /api/v1/. Three endpoints cover the entire surface:
//
//	GET  /api/v1/rpc              -> {"methods":[...]}  (discovery)
//	POST /api/v1/rpc/{Method}     -> JSON in, JSON out  (all 80+ unary RPCs)
//	GET  /api/v1/events           -> Server-Sent Events (the EventListen stream)
//
// Every endpoint requires `Authorization: Bearer <token>`, obtained from the
// existing POST /api/auth/login. Authorisation is deliberately re-checked here
// rather than delegated: this handler does NOT go through the gRPC server, so
// the gRPC interceptors never run for these requests.
//
// SECURITY NOTE -- why there are no CORS headers here.
//
// The gRPC-web wrapper this service also mounts is built with
// `originFunc: func(string) bool { return true }` and `AllowCredentials: true`
// (see improbable-eng/grpc-web's options.go defaults), i.e. it answers
// cross-origin preflights from anywhere. That is survivable there only because
// its auth is a bearer header an attacker's page cannot read. This handler
// deliberately emits no Access-Control-Allow-Origin at all, so browsers refuse
// cross-origin use of it outright, and additionally rejects any request that
// arrives with a foreign Origin. A red-team appliance is frequently reached
// from a browser that is simultaneously visiting untrusted pages; treating
// same-origin as the only acceptable case is the conservative choice.

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/mame82/P4wnP1_aloa/common_web"
	pb "github.com/mame82/P4wnP1_aloa/proto"
	"github.com/mame82/P4wnP1_aloa/service/auth"
	"github.com/mame82/P4wnP1_aloa/service/jsonbridge"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
)

// APIPrefix is the URL prefix the JSON API lives under.
const APIPrefix = "/api/v1/"

// maxAPIBodyBytes caps a request body. The largest legitimate payload is a
// master template or a HIDScript source blob; 8 MiB is generous for both and
// stops an unauthenticated-at-the-TCP-level peer from ballooning memory before
// the token check even runs.
const maxAPIBodyBytes = 8 << 20

// RecoverHandler wraps an http.Handler so a panic in anything below it becomes
// a 500 instead of ending the process.
//
// This is not covered by the gRPC recovery interceptors. The JSON bridge
// invokes service methods DIRECTLY by reflection (jsonbridge.Call ->
// reflect.Value.Call), so grpc.NewServer's interceptor chain never runs for a
// request that arrives over /api/v1/rpc/. A panic in any of the 82 RPCs
// reached that way would take the whole appliance down -- and this is the path
// the web console uses for everything.
//
// Wrapping the top-level router covers the JSON API, the auth endpoints, the
// gRPC-web bridge and the static file server in one place.
func RecoverHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rw := &recordingWriter{ResponseWriter: w}
		defer func() {
			rec := recover()
			if rec == nil {
				return
			}
			log.Printf("PANIC recovered serving %s %s: %v\n%s",
				r.Method, r.URL.Path, rec, debug.Stack())

			// Once a status line is out -- which it always is for SSE, where we
			// write 200 and then stream -- there is no way to turn the response
			// into an error. Dropping the connection is the only honest signal
			// left, and the client's reconnect logic handles it.
			if rw.wroteHeader {
				return
			}
			rw.Header().Set("Content-Type", "application/json")
			rw.WriteHeader(http.StatusInternalServerError)
			_, _ = rw.Write([]byte(
				`{"error":"internal error; the service survived and the details are in the journal"}`))
		}()
		next.ServeHTTP(rw, r)
	})
}

// recordingWriter tracks whether the status line has gone out, and forwards the
// optional interfaces the handlers below it rely on. Without Flush the SSE
// stream would buffer; without Unwrap http.NewResponseController cannot reach
// the real writer; without Hijack the gRPC-web websocket upgrade fails.
type recordingWriter struct {
	http.ResponseWriter
	wroteHeader bool
}

func (w *recordingWriter) WriteHeader(code int) {
	if !w.wroteHeader {
		w.wroteHeader = true
		w.ResponseWriter.WriteHeader(code)
	}
}

func (w *recordingWriter) Write(b []byte) (int, error) {
	w.wroteHeader = true
	return w.ResponseWriter.Write(b)
}

func (w *recordingWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *recordingWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := w.ResponseWriter.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, fmt.Errorf("the underlying ResponseWriter does not support hijacking")
}

func (w *recordingWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// NewAPIHandler builds the JSON API handler for srv.
func NewAPIHandler(srv *server, authMgr *auth.Manager) (http.Handler, error) {
	bridge, err := jsonbridge.New(srv)
	if err != nil {
		return nil, fmt.Errorf("building JSON bridge: %w", err)
	}
	// One place where "the hardware is not here" stops looking like "the
	// service is broken", for every RPC including ones added later.
	bridge.SetErrorClassifier(hardwareUnavailable)
	log.Printf("JSON API: exposing %d unary RPCs under %s", len(bridge.Methods()), APIPrefix)

	a := &apiHandler{srv: srv, authMgr: authMgr, bridge: bridge}

	mux := http.NewServeMux()
	mux.HandleFunc("/rpc", a.handleMethodList)
	mux.HandleFunc("/rpc/", a.handleRPC)
	mux.HandleFunc("/events", a.handleEvents)
	return http.StripPrefix(strings.TrimSuffix(APIPrefix, "/"), mux), nil
}

type apiHandler struct {
	srv     *server
	authMgr *auth.Manager
	bridge  *jsonbridge.Bridge
}

// --- helpers ---------------------------------------------------------------

func apiJSON(w http.ResponseWriter, status int, body interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func apiError(w http.ResponseWriter, status int, msg string) {
	apiJSON(w, status, map[string]string{"error": msg})
}

// allowedHostEnv lets an operator who reaches the device by some name of their
// own add it, e.g. P4WNP1_ALLOWED_HOSTS="p4wnp1.lan,box.internal".
const allowedHostEnv = "P4WNP1_ALLOWED_HOSTS"

// knownHost reports whether the Host header names this device in a way that
// cannot be forged by an attacker's DNS.
//
// This exists because sameOrigin compares Origin against Host, and an attacker
// who controls a domain controls BOTH. Point evil.example at 172.16.0.1 and a
// victim's browser sends Origin: http://evil.example with
// Host: evil.example -- they match, so the origin check passes and the
// attacker's page is same-origin with the device. That was confirmed against a
// running service: an RPC executed with Host and Origin both set to
// evil.example, while the same request with only Origin forged was correctly
// refused 403. This is classic DNS rebinding, and the origin check alone
// offers nothing against it.
//
// The device is reached at an IP literal: 172.16.0.1 over the USB ethernet
// link, 172.24.0.1 over its own access point, or localhost on the device
// itself. Rebinding REQUIRES a name, because an IP literal resolves to itself.
// So accepting IP literals, loopback names and mDNS .local names -- and
// refusing other DNS names -- removes the attack without constraining any
// legitimate route to the console.
func knownHost(host string) bool {
	if host == "" {
		return true // HTTP/1.0 and some non-browser clients send no Host
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	host = strings.Trim(host, "[]") // IPv6 literal

	if net.ParseIP(host) != nil {
		return true // an IP literal cannot be rebound: it resolves to itself
	}
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	if strings.HasSuffix(host, ".local") {
		return true // mDNS; not resolvable from the public DNS
	}
	for _, extra := range strings.Split(os.Getenv(allowedHostEnv), ",") {
		if extra = strings.ToLower(strings.TrimSpace(extra)); extra != "" && extra == host {
			return true
		}
	}
	return false
}

// GuardBrowserOrigin rejects requests a browser should never be able to make
// to this device, BEFORE the wrapped handler sees them.
//
// It exists because the Host and Origin checks used to live only inside
// authenticate(), which covers /api/v1/* and nothing else. /api/auth/login is
// served by a different handler and had neither check: a foreign origin got
// HTTP 200 and a freshly minted token. No CORS header is emitted, so a plain
// cross-origin page cannot READ that response -- but a DNS-rebound page is
// same-origin and can, which turns login into an unthrottled oracle reachable
// from any victim's browser on the device's network. Guarding the RPC surface
// while leaving the credential endpoint open is not a fix.
func GuardBrowserOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !knownHost(r.Host) {
			apiError(w, http.StatusForbidden,
				"this device is reached by IP address (e.g. 172.16.0.1), not by the name '"+
					r.Host+"'. If that name really is yours, list it in "+allowedHostEnv+".")
			return
		}
		if !sameOrigin(r) {
			apiError(w, http.StatusForbidden, "cross-origin requests are not permitted")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// sameOrigin reports whether the request is safe to serve.
//
// A missing Origin header is accepted: that is what non-browser clients (curl,
// the CLI, scripts) send, and they are not subject to the ambient-credential
// problem Origin exists to signal. A present-but-foreign Origin is rejected.
func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	// Compare hostnames only. The port legitimately differs because the API is
	// reached on the web port while Host may carry a different one, and an
	// attacker cannot control the hostname a browser reports.
	oh, rh := u.Hostname(), r.Host
	if h, _, err := net.SplitHostPort(rh); err == nil {
		rh = h
	}
	return strings.EqualFold(oh, rh)
}

// authenticate enforces the bearer token and returns a context carrying the
// session, plus gRPC metadata so any downstream code that reads metadata (as
// the gRPC handlers may) still finds the token.
func (a *apiHandler) authenticate(w http.ResponseWriter, r *http.Request) (context.Context, bool) {
	if !knownHost(r.Host) {
		apiError(w, http.StatusForbidden,
			"this device is reached by IP address (e.g. 172.16.0.1), not by the name '"+
				r.Host+"'. If that name really is yours, list it in "+allowedHostEnv+".")
		return nil, false
	}
	if !sameOrigin(r) {
		apiError(w, http.StatusForbidden, "cross-origin requests are not permitted")
		return nil, false
	}
	raw := r.Header.Get(auth.HTTPAuthHeader)
	if raw == "" {
		apiError(w, http.StatusUnauthorized, "missing Authorization header")
		return nil, false
	}
	if !strings.HasPrefix(strings.ToLower(raw), auth.BearerPrefix) {
		apiError(w, http.StatusUnauthorized, "Authorization must be 'Bearer <token>'")
		return nil, false
	}
	tok := strings.TrimSpace(raw[len(auth.BearerPrefix):])
	sess, err := a.authMgr.ValidateToken(tok)
	if err != nil {
		apiError(w, http.StatusUnauthorized, "invalid or expired token")
		return nil, false
	}
	ctx := auth.ContextWithSession(r.Context(), sess)
	ctx = metadata.NewIncomingContext(ctx, metadata.Pairs(auth.MetadataAuthKey, raw))
	return ctx, true
}

// --- GET /api/v1/rpc -------------------------------------------------------

func (a *apiHandler) handleMethodList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		apiError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	if _, ok := a.authenticate(w, r); !ok {
		return
	}
	apiJSON(w, http.StatusOK, map[string]interface{}{"methods": a.bridge.Methods()})
}

// --- POST /api/v1/rpc/{Method} ---------------------------------------------

func (a *apiHandler) handleRPC(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		apiError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	ctx, ok := a.authenticate(w, r)
	if !ok {
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/rpc/")
	if name == "" || strings.Contains(name, "/") {
		apiError(w, http.StatusBadRequest, "expected /api/v1/rpc/<MethodName>")
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxAPIBodyBytes))
	if err != nil {
		apiError(w, http.StatusRequestEntityTooLarge,
			fmt.Sprintf("request body exceeds %d bytes", maxAPIBodyBytes))
		return
	}

	out, err := a.bridge.Call(ctx, name, body)
	if err != nil {
		var ce *jsonbridge.CallError
		code := codes.Unknown
		msg := err.Error()
		if errors.As(err, &ce) {
			code, msg = ce.Code, ce.Msg
		}
		apiError(w, jsonbridge.HTTPStatus(code), msg)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(out)
}

// --- GET /api/v1/events ----------------------------------------------------

// handleEvents streams the service's event bus to the client as Server-Sent
// Events.
//
// Note for frontend code: the browser's EventSource API cannot set request
// headers, so it cannot carry the bearer token. Consume this with fetch() and
// a ReadableStream reader instead. Requiring the header (rather than accepting
// a token in the query string) keeps the token out of access logs, browser
// history and Referer headers.
func (a *apiHandler) handleEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		apiError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	ctx, ok := a.authenticate(w, r)
	if !ok {
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		apiError(w, http.StatusInternalServerError, "streaming unsupported by this server")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	stream := &sseEventStream{ctx: ctx, w: w, flusher: flusher}

	// Heartbeat: an idle SSE connection behind a proxy or a sleeping phone gets
	// silently dropped. A comment line every 20s keeps it alive and lets the
	// client notice a dead link quickly.
	done := make(chan struct{})
	defer close(done)
	go func() {
		t := time.NewTicker(20 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-t.C:
				stream.comment("keepalive")
			}
		}
	}()

	// EventListen blocks until the context is cancelled (client disconnect).
	if err := a.srv.EventListen(&pb.EventRequest{ListenType: common_web.EVT_ANY}, stream); err != nil {
		log.Printf("JSON API: event stream ended: %v", err)
	}
}

// sseEventStream adapts pb.P4WNP1_EventListenServer onto an SSE response.
//
// EventListen is the service's only streaming RPC, so a single concrete
// adapter covers the whole streaming surface -- no reflection needed.
type sseEventStream struct {
	ctx     context.Context
	w       http.ResponseWriter
	flusher http.Flusher

	mu sync.Mutex // serialises writes: Send and the keepalive goroutine race
}

func (s *sseEventStream) Send(e *pb.Event) error {
	payload, err := jsonbridge.MarshalMessage(e)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := fmt.Fprintf(s.w, "data: %s\n\n", payload); err != nil {
		return err
	}
	s.flusher.Flush()
	return nil
}

func (s *sseEventStream) comment(text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fmt.Fprintf(s.w, ": %s\n\n", text)
	s.flusher.Flush()
}

// --- grpc.ServerStream surface ---------------------------------------------
// EventListen only ever calls Send and Context; the rest exist to satisfy the
// interface and are no-ops or errors rather than panics, so an unexpected call
// degrades instead of taking the service down.

func (s *sseEventStream) Context() context.Context     { return s.ctx }
func (s *sseEventStream) SetHeader(metadata.MD) error  { return nil }
func (s *sseEventStream) SendHeader(metadata.MD) error { return nil }
func (s *sseEventStream) SetTrailer(metadata.MD)       {}
func (s *sseEventStream) SendMsg(m interface{}) error {
	e, ok := m.(*pb.Event)
	if !ok {
		return fmt.Errorf("sseEventStream: expected *pb.Event, got %T", m)
	}
	return s.Send(e)
}
func (s *sseEventStream) RecvMsg(interface{}) error {
	return fmt.Errorf("sseEventStream: receiving is not supported on a server-streaming RPC")
}
