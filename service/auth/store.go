package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

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
	return nil
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
func (s *Store) SetPassword(username, password string) error {
	if username == "" {
		return errors.New("auth: username is empty")
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
