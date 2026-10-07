package auth

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// LocalTokenPath is where the service writes the credential its own startup
// and trigger scripts use. It lives under /run, which is a tmpfs on
// Raspberry Pi OS: the file is rewritten on every service start and never
// survives a power cycle.
const LocalTokenPath = "/run/p4wnp1/local.token"

// LocalUsername is the account name recorded on the session issued to the
// device's own scripts. Store.SetPassword refuses to create a user by this
// name, so no human account can share it and the console's session list
// shows plainly when a caller was a local script.
const LocalUsername = "local-script"

// Why this exists:
//
// The device drives itself through P4wnP1_cli. The fallback startup script
// that brings up the USB gadget, the DHCP servers and the WiFi AP is a shell
// script full of CLI calls, and so is every user-written trigger action. Once
// the API required a bearer token, all of them began failing with
// Unauthenticated -- so a device whose startup template failed had no network
// at all and no way to tell you why.
//
// Those scripts cannot log in: there is no password on disk for them to use,
// and putting one there would be worse than what follows.
//
// What follows is deliberately unexciting: the service logs in as itself. It
// mints an ORDINARY session -- the same kind `P4wnP1_cli auth login` gets --
// and writes that token to a file only root can read. There is no second
// credential type, no bypass in the token validator, and no special case in
// the interceptors. A local script is an authenticated client like any
// other: its session appears in the session list, it expires on the normal
// schedule, and revoking all sessions revokes it too.
//
// Root on the device can already read the password hash database, the stored
// WiFi keys and every template, so a root-only file hands root nothing it
// could not already take.
//
// Because the session is ordinary, it expires like one. Manager.ValidateToken
// slides the expiry forward only when the token is *used*, and a trigger can
// fire days after the last CLI call, so KeepLocalTokenFresh re-provisions on
// a timer at half the session TTL. That also repairs the file after a
// password change wipes every session.

// localTokenState tracks the token currently on disk so it can be revoked
// when replaced.
type localTokenState struct {
	mu    sync.Mutex
	token string
	path  string
}

var localState localTokenState

// ProvisionLocalToken mints a session for the device's own scripts and
// writes the token to path with mode 0600 (parent directory 0700),
// replacing and revoking any token it previously wrote.
func (m *Manager) ProvisionLocalToken(path string) error {
	sess, err := m.Sessions.Mint(LocalUsername, m.ttl)
	if err != nil {
		return fmt.Errorf("mint local session: %w", err)
	}
	if err := writeTokenFile(path, sess.Token); err != nil {
		// Don't leave an unreachable session in the map.
		m.Sessions.Revoke(sess.Token)
		return err
	}

	localState.mu.Lock()
	previous := localState.token
	localState.token = sess.Token
	localState.path = path
	localState.mu.Unlock()

	if previous != "" {
		m.Sessions.Revoke(previous)
	}
	return nil
}

// writeTokenFile writes tok to path atomically, so a script reading the file
// never sees a half-written token. The temporary file is created 0600 from
// the start rather than chmod-ed afterwards.
func writeTokenFile(path, tok string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("create local token dir: %w", err)
	}
	tmp := path + ".new"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("create local token file: %w", err)
	}
	if _, err := f.WriteString(tok); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("write local token: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("close local token: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("install local token: %w", err)
	}
	return nil
}

// KeepLocalTokenFresh re-provisions the local token every half session TTL
// until stop is closed. Blocks; run it in a goroutine.
//
// Half the TTL, not the whole of it, so the token on disk is always valid
// for at least ttl/2 more and a script never races the refresh.
func (m *Manager) KeepLocalTokenFresh(path string, stop <-chan struct{}) {
	interval := m.ttl / 2
	if interval <= 0 {
		interval = DefaultSessionTTL / 2
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			if err := m.ProvisionLocalToken(path); err != nil {
				// Not fatal: the existing token stays valid until its
				// own expiry, and the next tick tries again.
				fmt.Fprintf(os.Stderr, "could not refresh local token: %v\n", err)
			}
		case <-stop:
			return
		}
	}
}

// RemoveLocalToken revokes the session and deletes the file. Safe to call
// when none was provisioned.
func (m *Manager) RemoveLocalToken() error {
	localState.mu.Lock()
	tok := localState.token
	path := localState.path
	localState.token = ""
	localState.path = ""
	localState.mu.Unlock()

	if tok != "" {
		m.Sessions.Revoke(tok)
	}
	if path == "" {
		return nil
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// ReadLocalToken reads a token written by ProvisionLocalToken. Used by
// P4wnP1_cli when no interactive login is cached. Returns "" with no error
// when the file does not exist, so a caller off-device just falls through to
// its normal "please log in" path.
func ReadLocalToken(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) || os.IsPermission(err) {
			return "", nil
		}
		return "", err
	}
	return string(b), nil
}
