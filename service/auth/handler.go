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
//	GET  /api/auth/health   -> 200 {"status":"ok","authenticated":<bool>}
func HTTPHandler(m *Manager) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
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
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		handleHealth(m, w, r)
	})
	// Strip HTTPPrefix so the inner mux's relative routes work.
	return http.StripPrefix(strings.TrimSuffix(HTTPPrefix, "/"), mux)
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
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
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

type changePasswordRequest struct {
	Username    string `json:"username"`
	OldPassword string `json:"old_password"`
	NewPassword string `json:"new_password"`
}

func handleChangePassword(m *Manager, w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	var req changePasswordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
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
		"status":         "ok",
		"authenticated":  authenticated,
		"server_time":    time.Now().Unix(),
	})
}
