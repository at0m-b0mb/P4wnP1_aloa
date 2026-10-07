package auth

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
)

// HTTPPrefix is the URL prefix all auth HTTP endpoints live under. The main
// HTTP router routes anything beginning with this to the auth handler
// before falling through to gRPC-web / static files.
const HTTPPrefix = "/api/auth/"

// maxAuthBodyBytes caps every request body these handlers parse.
//
// /api/auth/login is the ONE endpoint an anonymous caller can reach that
// parses a body, and it did so with an unbounded json.NewDecoder(r.Body). A Pi
// Zero W has 512MB of RAM and this service runs as root driving HID injection,
// DHCP and hostapd, so an anonymous POST of a few hundred megabytes was a
// memory-exhaustion denial of service against the whole appliance with no
// credential of any kind.
//
// A credentials object is a few hundred bytes. 16 KiB is generous enough that
// no legitimate client can hit it and small enough that hitting it costs an
// attacker more than it costs the device.
const maxAuthBodyBytes = 16 << 10

// HTTPHandler returns an http.Handler that serves the auth-related HTTP
// endpoints. None of these endpoints require an existing valid token --
// they ARE the way to obtain one.
//
//	POST /api/auth/login    {"username":"...", "password":"..."}
//	      -> 200 {"token":"...", "expires_at":<unix>}
//	      -> 401 {"error":"invalid credentials"}
//	POST /api/auth/logout   (Authorization: Bearer ...)
//	      -> 204
//	GET  /api/auth/whoami   (Authorization: Bearer ...)
//	      -> 200 {"username":"...", "expires_at":<unix>}
//	      -> 401 {"error":"unauthenticated"}
//	POST /api/auth/changepw {"username":"...","old_password":"...","new_password":"..."}
//	      -> 204
//	      -> 401 {"error":"invalid credentials"}
//	GET  /api/auth/sessions (Authorization: Bearer ...)
//	      -> 200 {"sessions":[{"id","username","issued_at","expires_at",
//	                           "is_local_script","is_current"}, ...]}
//	POST /api/auth/sessions/revoke {"id":"..."}  (Authorization: Bearer ...)
//	      -> 204
//	      -> 404 {"error":"no such session"}
//	GET  /api/auth/health   -> 200 {"status":"ok","authenticated":<bool>}
func HTTPHandler(m *Manager) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && !requireJSONContentType(w, r) {
			return
		}
		handleLogin(m, w, r)
	})
	mux.HandleFunc("/logout", func(w http.ResponseWriter, r *http.Request) {
		handleLogout(m, w, r)
	})
	mux.HandleFunc("/whoami", func(w http.ResponseWriter, r *http.Request) {
		handleWhoAmI(m, w, r)
	})
	mux.HandleFunc("/changepw", func(w http.ResponseWriter, r *http.Request) {
		handleChangePassword(m, w, r)
	})
	// Register the more specific route first: ServeMux would otherwise match
	// "/sessions/revoke" against the "/sessions" pattern's subtree.
	mux.HandleFunc("/sessions/revoke", func(w http.ResponseWriter, r *http.Request) {
		handleRevokeSession(m, w, r)
	})
	mux.HandleFunc("/sessions", func(w http.ResponseWriter, r *http.Request) {
		handleListSessions(m, w, r)
	})
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		handleHealth(m, w, r)
	})
	// Strip HTTPPrefix so the inner mux's relative routes work.
	return http.StripPrefix(strings.TrimSuffix(HTTPPrefix, "/"), mux)
}

// decodeLimited parses a JSON request body, refusing to read more than
// maxAuthBodyBytes. http.MaxBytesReader, rather than io.LimitReader, because
// it also stops the client sending more once the limit is hit instead of
// silently truncating and leaving the connection to drain.
func decodeLimited(w http.ResponseWriter, r *http.Request, dst interface{}) error {
	return json.NewDecoder(http.MaxBytesReader(w, r.Body, maxAuthBodyBytes)).Decode(dst)
}

// --- JSON helpers ----------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, body interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// extractBearer reads the Authorization header and returns the bearer token
// or "" if absent / malformed.
func extractBearer(r *http.Request) string {
	h := r.Header.Get(HTTPAuthHeader)
	if h == "" {
		return ""
	}
	lower := strings.ToLower(h)
	if !strings.HasPrefix(lower, BearerPrefix) {
		return ""
	}
	return strings.TrimSpace(h[len(BearerPrefix):])
}

// --- Handlers --------------------------------------------------------------

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type loginResponse struct {
	Token     string `json:"token"`
	ExpiresAt int64  `json:"expires_at"` // unix seconds
	Username  string `json:"username"`
}

func handleLogin(m *Manager, w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	var req loginRequest
	if err := decodeLimited(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	sess, err := m.Login(r.Context(), req.Username, req.Password)
	if err != nil {
		if errors.Is(err, ErrInvalidCredentials) {
			writeError(w, http.StatusUnauthorized, "invalid credentials")
			return
		}
		writeError(w, http.StatusInternalServerError, "login failed")
		return
	}
	writeJSON(w, http.StatusOK, loginResponse{
		Token:     sess.Token,
		ExpiresAt: sess.Expires.Unix(),
		Username:  sess.Username,
	})
}

func handleLogout(m *Manager, w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	tok := extractBearer(r)
	if tok != "" {
		m.Sessions.Revoke(tok)
	}
	w.WriteHeader(http.StatusNoContent)
}

func handleWhoAmI(m *Manager, w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET required")
		return
	}
	tok := extractBearer(r)
	if tok == "" {
		writeError(w, http.StatusUnauthorized, "no token")
		return
	}
	sess, err := m.ValidateToken(tok)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"username":   sess.Username,
		"expires_at": sess.Expires.Unix(),
	})
}

// handleListSessions answers "who is logged in right now".
//
// A device that can be reached over USB, over its own access point and over
// Bluetooth, with one shared account, had no way at all to answer that. An
// operator who suspected a session was not theirs could only change the
// password and revoke everything, including the device's own script
// credential.
//
// The response carries no tokens. This is served to a browser, and a list of
// live tokens would turn an audit view into a credential dump.
func handleListSessions(m *Manager, w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET required")
		return
	}
	tok := extractBearer(r)
	if tok == "" {
		writeError(w, http.StatusUnauthorized, "no token")
		return
	}
	if _, err := m.ValidateToken(tok); err != nil {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"sessions": m.Sessions.List(tok),
	})
}

type revokeSessionRequest struct {
	ID string `json:"id"`
}

// handleRevokeSession ends one named session.
//
// Without this, the only way to remove a session you did not recognise was to
// change the password, which revokes every session including the one the
// device uses to drive itself.
func handleRevokeSession(m *Manager, w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	tok := extractBearer(r)
	if tok == "" {
		writeError(w, http.StatusUnauthorized, "no token")
		return
	}
	if _, err := m.ValidateToken(tok); err != nil {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	var req revokeSessionRequest
	if err := decodeLimited(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}
	if req.ID == "" {
		writeError(w, http.StatusBadRequest, "id is required")
		return
	}
	// Revoking the device's own script credential would stop its startup and
	// trigger actions working, with no sign of why. Re-issue it instead of
	// refusing, so the operator still gets the "this session is gone" they
	// asked for.
	isLocal := false
	for _, s := range m.Sessions.List(tok) {
		if s.ID == req.ID && s.IsLocalScript {
			isLocal = true
		}
	}
	if !m.Sessions.RevokeByID(req.ID) {
		writeError(w, http.StatusNotFound, "no such session")
		return
	}
	if isLocal {
		m.ReprovisionLocalTokenIfConfigured()
	}
	w.WriteHeader(http.StatusNoContent)
}

type changePasswordRequest struct {
	Username    string `json:"username"`
	OldPassword string `json:"old_password"`
	NewPassword string `json:"new_password"`
}

// requireJSONContentType rejects a request that does not declare a JSON body.
//
// This is a CSRF control, not a parsing nicety. A POST with a form or plain
// content type is a CORS "simple request": a browser sends it cross-origin
// with no preflight, so any page the operator happens to be visiting can
// deliver it. Demanding application/json forces a preflight, and because these
// routes emit no Access-Control-Allow-Origin the preflight fails and the
// request never arrives.
//
// Applied to login and changepw only, deliberately -- NOT mux-wide. The CLI
// builds its logout POST without a Content-Type (cli_client/auth_client.go),
// and breaking logout to harden it would be a poor trade.
func requireJSONContentType(w http.ResponseWriter, r *http.Request) bool {
	ct := r.Header.Get("Content-Type")
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = ct[:i]
	}
	if !strings.EqualFold(strings.TrimSpace(ct), "application/json") {
		writeError(w, http.StatusUnsupportedMediaType, "Content-Type: application/json required")
		return false
	}
	return true
}

func handleChangePassword(m *Manager, w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	// A password change used to require only the OLD password and no bearer
	// token at all. Combined with the lack of a Content-Type check that made it
	// a CORS-simple request any website the operator visited could deliver, and
	// ChangePassword calls RevokeAll() on success -- so a correct guess changed
	// the admin password and silently logged the operator out of their own
	// device. Proving possession of a live session is the minimum bar for the
	// one endpoint that rewrites credentials.
	if _, err := m.ValidateToken(extractBearer(r)); err != nil {
		writeError(w, http.StatusUnauthorized, "a valid bearer token is required to change a password")
		return
	}
	if !requireJSONContentType(w, r) {
		return
	}
	var req changePasswordRequest
	if err := decodeLimited(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if err := m.ChangePassword(r.Context(), req.Username, req.OldPassword, req.NewPassword); err != nil {
		switch {
		case errors.Is(err, ErrInvalidCredentials):
			writeError(w, http.StatusUnauthorized, "invalid credentials")
		case errors.Is(err, ErrWeakPassword):
			writeError(w, http.StatusBadRequest, "password too weak (need 12+ chars)")
		default:
			writeError(w, http.StatusInternalServerError, "change failed")
		}
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleHealth returns service-up status + whether the current request
// already carries a valid token. Useful for the SPA to decide on initial
// render whether to show the login screen or the dashboard.
func handleHealth(m *Manager, w http.ResponseWriter, r *http.Request) {
	authenticated := false
	if tok := extractBearer(r); tok != "" {
		if _, err := m.ValidateToken(tok); err == nil {
			authenticated = true
		}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":        "ok",
		"authenticated": authenticated,
		"server_time":   time.Now().Unix(),
	})
}
