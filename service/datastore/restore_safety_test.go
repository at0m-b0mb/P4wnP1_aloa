//go:build linux
// +build linux

package datastore

import (
	"os"
	"path/filepath"
	"testing"

	pb "github.com/mame82/P4wnP1_aloa/proto"
)

// Audit finding S2.
//
// Restore(replace=true) used to Clear() the live database FIRST -- before
// anything had opened the backup -- then discard the error from tmpDB.Load()
// and end in `return nil`. A truncated or corrupt backup therefore erased every
// stored template, loadout and reflex and reported success.
//
// That is not an exotic input here. This device is powered off by being pulled
// out of a USB port, which is exactly how a half-written backup comes to exist
// -- the same reason badger is opened with Truncate.
//
// These tests exist because the failure is silent: the operator sees
// "Restored", and finds out later.

func newTestStore(t *testing.T) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	work := filepath.Join(dir, "store")
	// Create the directory FIRST. Open() only restores its init backup when the
	// work path does not already exist, and this test has no init backup to
	// give it -- Open(path, "") fails in os.Open(""), which is a trap worth
	// knowing about and not what is under test here.
	if err := os.MkdirAll(work, 0o700); err != nil {
		t.Fatal(err)
	}
	s, err := Open(work, "")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(s.Close)
	return s, dir
}

// seed puts a couple of recognisable keys in and returns their names.
func seed(t *testing.T, s *Store) []string {
	t.Helper()
	keys := []string{"tas_keepme", "ws_keepme"}
	for _, k := range keys {
		if err := s.Put(k, &pb.StringMessage{Msg: "precious-" + k}, true); err != nil {
			t.Fatalf("Put %s: %v", k, err)
		}
	}
	return keys
}

func liveKeys(t *testing.T, s *Store) []string {
	t.Helper()
	ks, err := s.Keys()
	if err != nil {
		t.Fatalf("Keys: %v", err)
	}
	return ks
}

// TestACorruptBackupDoesNotEraseTheDatabase is the whole finding in one test.
func TestACorruptBackupDoesNotEraseTheDatabase(t *testing.T) {
	s, dir := newTestStore(t)
	want := seed(t, s)

	bad := filepath.Join(dir, "corrupt.db")
	if err := os.WriteFile(bad, []byte("this is not a badger backup, not even close"), 0o600); err != nil {
		t.Fatal(err)
	}

	err := s.Restore(bad, true)
	if err == nil {
		t.Error("restoring a corrupt backup reported SUCCESS; it must fail loudly")
	}

	got := liveKeys(t, s)
	if len(got) < len(want) {
		t.Fatalf("THE LIVE DATABASE WAS ERASED by a corrupt restore: %d keys left, wanted %d (%v)",
			len(got), len(want), want)
	}
	// And the values must still be the originals, not empty shells.
	var m pb.StringMessage
	if err := s.Get("tas_keepme", &m); err != nil {
		t.Fatalf("a seeded key is gone after a failed restore: %v", err)
	}
	if m.Msg != "precious-tas_keepme" {
		t.Errorf("value was damaged by a failed restore: %q", m.Msg)
	}
}

// TestATruncatedBackupDoesNotEraseTheDatabase is the realistic version of the
// above: a backup that STARTED being written and then lost power.
func TestATruncatedBackupDoesNotEraseTheDatabase(t *testing.T) {
	s, dir := newTestStore(t)
	seed(t, s)

	good := filepath.Join(dir, "good.db")
	if err := s.Backup(good); err != nil {
		t.Fatalf("Backup: %v", err)
	}
	whole, err := os.ReadFile(good)
	if err != nil {
		t.Fatal(err)
	}
	if len(whole) < 16 {
		t.Fatalf("backup is implausibly small (%d bytes)", len(whole))
	}

	// Cut it off part-way, as an unplug would.
	cut := filepath.Join(dir, "cut.db")
	if err := os.WriteFile(cut, whole[:len(whole)/2], 0o600); err != nil {
		t.Fatal(err)
	}

	_ = s.Restore(cut, true) // may or may not error depending on where the cut lands

	// Whatever it decided, the live data must not have been thrown away before
	// the backup was known good.
	if got := liveKeys(t, s); len(got) == 0 {
		t.Fatal("A TRUNCATED BACKUP EMPTIED THE DATABASE. The clear must happen " +
			"after the backup is known readable, not before.")
	}
}

// TestAnEmptyBackupIsRefused covers the case badger will happily accept: a file
// that parses into nothing. Restoring it is indistinguishable from a wipe.
func TestAnEmptyBackupIsRefused(t *testing.T) {
	s, dir := newTestStore(t)
	seed(t, s)

	empty := filepath.Join(dir, "empty.db")
	if err := os.WriteFile(empty, []byte{}, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := s.Restore(empty, true); err == nil {
		t.Error("an empty backup was accepted; restoring it only erases the database")
	}
	if got := liveKeys(t, s); len(got) == 0 {
		t.Fatal("an empty backup erased the database")
	}
}

// TestAGoodBackupStillRestores is the control. Everything above would pass for
// an implementation that refused every restore.
func TestAGoodBackupStillRestores(t *testing.T) {
	s, dir := newTestStore(t)
	seed(t, s)

	good := filepath.Join(dir, "good.db")
	if err := s.Backup(good); err != nil {
		t.Fatalf("Backup: %v", err)
	}

	// Change the live data, then restore over it.
	if err := s.Put("tas_keepme", &pb.StringMessage{Msg: "changed"}, true); err != nil {
		t.Fatal(err)
	}
	if err := s.Restore(good, true); err != nil {
		t.Fatalf("a GOOD backup failed to restore: %v", err)
	}

	var m pb.StringMessage
	if err := s.Get("tas_keepme", &m); err != nil {
		t.Fatalf("key missing after a good restore: %v", err)
	}
	if m.Msg != "precious-tas_keepme" {
		t.Errorf("restore did not bring the original value back: got %q", m.Msg)
	}
}
