package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// bcryptCost is the work factor. 12 is a reasonable 2026 baseline; takes
// ~250ms on a Pi Zero W per hash. Cost 10 would be faster but is below
// current OWASP guidance.
const bcryptCost = 12

// MinPasswordLength is enforced on every set/change. 12 chars catches the
// most egregious "admin/admin" patterns without being a guarantee of
// strength (rate-limiting + bcrypt cost do the heavy lifting).
const MinPasswordLength = 12

// userRecord is the on-disk representation of a single account.
type userRecord struct {
	Username     string `json:"username"`
	PasswordHash string `json:"password_hash"`
}

// storeFile is the JSON file format.
type storeFile struct {
	Version int          `json:"version"`
	Users   []userRecord `json:"users"`
}

// Store is the bcrypt-hashed password store, persisted to a single JSON
// file. Concurrent access is serialised through mu -- the file is small
// (one or two users typically) so a coarse lock is fine.
type Store struct {
	path string

	mu    sync.RWMutex
	users map[string]string // username -> bcrypt hash

	// loadedMod and loadedSize are the modtime and size of the file as of the
	// last load, used to notice an out-of-band rewrite. See reloadIfChanged.
	loadedMod  time.Time
	loadedSize int64
}

// NewStore opens the password file at path. If the file doesn't exist, an
// empty store is returned. The caller is expected to seed it via SetPassword
// during first-boot bootstrap.
func NewStore(path string) (*Store, error) {
	s := &Store{path: path, users: map[string]string{}}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

// load reads the JSON file into the in-memory map. Missing file is OK.
func (s *Store) load() error {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read auth file %s: %w", s.path, err)
	}
	if len(data) == 0 {
		return nil
	}
	var file storeFile
	if err := json.Unmarshal(data, &file); err != nil {
		return fmt.Errorf("parse auth file %s: %w", s.path, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.users = map[string]string{}
	for _, u := range file.Users {
		s.users[u.Username] = u.PasswordHash
	}
	if fi, err := os.Stat(s.path); err == nil {
		s.loadedMod, s.loadedSize = fi.ModTime(), fi.Size()
	}
	return nil
}

// reloadIfChanged re-reads the password file when another process has
// rewritten it.
//
// p4wnp1-hashpw runs as a SEPARATE PROCESS and writes this file directly --
// that is how first boot seeds the account and how an operator resets a
// forgotten password. The running service had read the file once at startup
// and never looked again, so after such a reset the new password was rejected
// and THE OLD ONE KEPT WORKING until the service happened to restart. A
// password reset that leaves the old password live is worse than no reset,
// because the operator believes they have rotated it.
//
// Checked on the Verify path rather than with a watcher: this file changes
// perhaps twice in a device's life, and a stat() per login attempt is
// immaterial next to the bcrypt comparison that follows it.
func (s *Store) reloadIfChanged() {
	if s.path == "" {
		return // in-memory store, used by tests
	}
	fi, err := os.Stat(s.path)
	if err != nil {
		return // missing or unreadable: keep what we have
	}
	s.mu.RLock()
	unchanged := fi.ModTime().Equal(s.loadedMod) && fi.Size() == s.loadedSize
	s.mu.RUnlock()
	if unchanged {
		return
	}
	if err := s.load(); err != nil {
		// A corrupt or half-written file must not wipe the users we already
		// have -- that would lock the operator out of their own device.
		log.Printf("WARNING: auth: %s changed on disk but could not be re-read: %v", s.path, err)
	}
}

// persist writes the current map back to disk atomically (write+rename).
// Caller must hold s.mu (or know we're single-threaded, e.g. during init).
func (s *Store) persist() error {
	file := storeFile{Version: 1}
	for u, h := range s.users {
		file.Users = append(file.Users, userRecord{Username: u, PasswordHash: h})
	}
	data, err := json.MarshalIndent(&file, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal auth file: %w", err)
	}

	// Atomic replace: write to temp file then rename. Ensures we never see
	// a half-written auth.json on disk if power is yanked mid-write.
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("ensure auth dir: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".auth-*.json.tmp")
	if err != nil {
		return fmt.Errorf("create tmp auth file: %w", err)
	}
	tmpPath := tmp.Name()
	// On any error path below, attempt to clean up the tmp file.
	defer func() { _ = os.Remove(tmpPath) }()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write tmp auth file: %w", err)
	}
	if err := tmp.Chmod(0600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod tmp auth file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync tmp auth file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close tmp auth file: %w", err)
	}
	if err := os.Rename(tmpPath, s.path); err != nil {
		return fmt.Errorf("rename tmp auth file: %w", err)
	}
	return nil
}

// SetPassword creates or updates a user. Replaces any existing hash for the
// username. Used by both the first-boot bootstrap and ChangePassword.
//
// LocalUsername is refused: it names the sessions the service issues to the
// device's own scripts (see localcred.go), and a human account sharing that
// name would make the console's session list lie about who called.
func (s *Store) SetPassword(username, password string) error {
	if username == "" {
		return errors.New("auth: username is empty")
	}
	if username == LocalUsername {
		return fmt.Errorf("auth: %q is reserved for the device's own scripts", LocalUsername)
	}
	if len(password) < MinPasswordLength {
		return ErrWeakPassword
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	s.mu.Lock()
	s.users[username] = string(hash)
	err = s.persist()
	s.mu.Unlock()
	return err
}

// Verify returns true iff password matches the stored hash for username.
// Uses constant-time bcrypt compare. Returns false (NOT an error) if the
// user doesn't exist, so the caller can't distinguish "no such user" from
// "wrong password" -- standard auth hygiene.
func (s *Store) Verify(username, password string) bool {
	// Pick up a rewrite by p4wnp1-hashpw in another process before deciding.
	s.reloadIfChanged()

	s.mu.RLock()
	hash, ok := s.users[username]
	s.mu.RUnlock()
	if !ok {
		// Burn equivalent cycles to avoid timing oracle for username
		// enumeration. Hash an arbitrary string and discard.
		_, _ = bcrypt.GenerateFromPassword([]byte("dummy-to-equalise-timing"), bcryptCost)
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// HasAnyUsers returns true if the store has at least one account. Used by
// service startup to decide whether to refuse to start (no admin = no
// access) or to proceed.
func (s *Store) HasAnyUsers() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.users) > 0
}
