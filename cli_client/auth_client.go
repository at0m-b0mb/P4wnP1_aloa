package cli_client

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// authHTTPPort is the port the service exposes its HTTP auth endpoints on.
// It's the same port as the gRPC-web wrapper -- the service's HTTP router
// dispatches /api/auth/* to the auth handler before anything else.
const authHTTPPort = "8000"

// tokenFilePath returns the location of the per-user token cache. ~/.p4wnp1/
// is created on save with mode 0700; the token file itself is mode 0600.
func tokenFilePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("can't locate home dir: %w", err)
	}
	return filepath.Join(home, ".p4wnp1", "token"), nil
}

// tokenFileContents is what we serialise to disk. It's plain JSON so an
// operator can `cat` it and see what's there without needing the binary.
type tokenFileContents struct {
	Host      string `json:"host"`
	Username  string `json:"username"`
	Token     string `json:"token"`
	ExpiresAt int64  `json:"expires_at"` // unix seconds; 0 = unknown
}

// SaveToken writes a token to ~/.p4wnp1/token with mode 0600. Replaces any
// existing token.
func SaveToken(host, username, token string, expiresAt int64) error {
	path, err := tokenFilePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("create token dir: %w", err)
	}

	data, err := json.MarshalIndent(&tokenFileContents{
		Host: host, Username: username, Token: token, ExpiresAt: expiresAt,
	}, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal token: %w", err)
	}

	// Atomic write: tmpfile + rename, so a crash mid-write can't corrupt.
	tmp, err := os.CreateTemp(filepath.Dir(path), ".token-*.tmp")
	if err != nil {
		return fmt.Errorf("create tmp token file: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write tmp token file: %w", err)
	}
	if err := tmp.Chmod(0600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod tmp token file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

// LoadToken reads ~/.p4wnp1/token. Returns ("", "", "", 0, nil) when no
// token has been saved yet (caller should treat as "logged out").
func LoadToken() (host, username, token string, expiresAt int64, err error) {
	path, err := tokenFilePath()
	if err != nil {
		return "", "", "", 0, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", "", "", 0, nil
		}
		return "", "", "", 0, err
	}
	var t tokenFileContents
	if err := json.Unmarshal(data, &t); err != nil {
		return "", "", "", 0, fmt.Errorf("parse token file %s: %w", path, err)
	}
	return t.Host, t.Username, t.Token, t.ExpiresAt, nil
}

// ClearToken deletes ~/.p4wnp1/token. Returns nil if the file didn't exist.
func ClearToken() error {
	path, err := tokenFilePath()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// --- HTTP client for the /api/auth/* endpoints --------------------------------

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type loginResponse struct {
	Token     string `json:"token"`
	ExpiresAt int64  `json:"expires_at"`
	Username  string `json:"username"`
}

type errorResponse struct {
	Error string `json:"error"`
}

// httpClient gives every auth call a fixed 10s timeout so a misconfigured
// host doesn't hang the CLI.
var httpClient = &http.Client{Timeout: 10 * time.Second}

// apiLogin POSTs credentials to /api/auth/login and returns the new token
// + expiry. host = the gRPC server hostname (we re-derive the HTTP port).
func apiLogin(host, username, password string) (*loginResponse, error) {
	body, _ := json.Marshal(loginRequest{Username: username, Password: password})
	url := fmt.Sprintf("http://%s:%s/api/auth/login", host, authHTTPPort)
	resp, err := httpClient.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("connect to %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return nil, errors.New("invalid username or password")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("login failed: HTTP %d", resp.StatusCode)
	}
	var lr loginResponse
	if err := json.NewDecoder(resp.Body).Decode(&lr); err != nil {
		return nil, fmt.Errorf("parse login response: %w", err)
	}
	return &lr, nil
}

// apiLogout POSTs the token to /api/auth/logout (server-side revoke). The
// CLI ALSO deletes the local token cache; this call just makes the server
// drop the session immediately rather than waiting for it to expire.
func apiLogout(host, token string) error {
	url := fmt.Sprintf("http://%s:%s/api/auth/logout", host, authHTTPPort)
	req, err := http.NewRequest(http.MethodPost, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	// Logout is idempotent; treat any 2xx OR 401 as success (401 means the
	// token was already invalid, which is fine -- we still wipe locally).
	if resp.StatusCode >= 500 {
		return fmt.Errorf("logout failed: HTTP %d", resp.StatusCode)
	}
	return nil
}

type whoamiResponse struct {
	Username  string `json:"username"`
	ExpiresAt int64  `json:"expires_at"`
}

func apiWhoami(host, token string) (*whoamiResponse, error) {
	url := fmt.Sprintf("http://%s:%s/api/auth/whoami", host, authHTTPPort)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, errors.New("token is invalid or expired")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("whoami failed: HTTP %d", resp.StatusCode)
	}
	var w whoamiResponse
	if err := json.NewDecoder(resp.Body).Decode(&w); err != nil {
		return nil, err
	}
	return &w, nil
}

type changePasswordRequest struct {
	Username    string `json:"username"`
	OldPassword string `json:"old_password"`
	NewPassword string `json:"new_password"`
}

func apiChangePassword(host, username, oldPw, newPw string) error {
	body, _ := json.Marshal(changePasswordRequest{
		Username: username, OldPassword: oldPw, NewPassword: newPw,
	})
	url := fmt.Sprintf("http://%s:%s/api/auth/changepw", host, authHTTPPort)
	resp, err := httpClient.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNoContent {
		return nil
	}
	// Try to read the server's error message; fall back to status code.
	raw, _ := io.ReadAll(resp.Body)
	var er errorResponse
	if json.Unmarshal(raw, &er) == nil && er.Error != "" {
		return fmt.Errorf("changepw failed: %s (HTTP %d)", er.Error, resp.StatusCode)
	}
	return fmt.Errorf("changepw failed: HTTP %d", resp.StatusCode)
}
