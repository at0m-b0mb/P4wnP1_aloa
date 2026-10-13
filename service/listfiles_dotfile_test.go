package service

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// TestListFilesOfFolderHidesDotfiles pins the AppleDouble case.
//
// Found by deploying payloads to a real device from this Mac: macOS tar writes
// a "._name" sidecar next to every file it copies, carrying the extended
// attributes. Those sidecars end in ".js", so the extension filter let all
// fifteen of them through and ListStoredHIDScripts returned 37 entries where
// 22 were real. On the device's 21-column panel that list is unusable.
//
// This is the ORDINARY path, not an edge case -- operators copy payloads from
// laptops and plenty of those are Macs.
func TestListFilesOfFolderHidesDotfiles(t *testing.T) {
	dir := t.TempDir()
	write := func(name string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// The real payloads.
	write("win-01-hello.js")
	write("mac-01-hello.js")
	write("keymap-check.js")
	// The AppleDouble sidecars macOS leaves beside them.
	write("._win-01-hello.js")
	write("._mac-01-hello.js")
	write("._keymap-check.js")
	// Other junk that reaches these directories.
	write(".DS_Store")
	write(".gitkeep")
	// A non-payload, to prove the extension filter still does its job.
	write("notes.txt")

	got, err := ListFilesOfFolder(dir, "js")
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(got)

	want := []string{"keymap-check.js", "mac-01-hello.js", "win-01-hello.js"}
	if len(got) != len(want) {
		t.Fatalf("got %d entries %v, want %d %v", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("entry %d = %q, want %q (full list: %v)", i, got[i], want[i], got)
		}
	}
}

// TestListFilesOfFolderStillListsRealFiles is the other direction: a filter
// that hides everything would also pass the test above. This repository has
// shipped four checks that could not fail; this is the guard against a fifth.
func TestListFilesOfFolderStillListsRealFiles(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"a.js", "b.js", "c.bin", "d.iso"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	js, err := ListFilesOfFolder(dir, "js")
	if err != nil {
		t.Fatal(err)
	}
	if len(js) != 2 {
		t.Errorf("expected 2 .js files, got %d: %v", len(js), js)
	}

	// No extension filter means everything visible, dotfiles still excluded.
	all, err := ListFilesOfFolder(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 4 {
		t.Errorf("expected all 4 files with no extension filter, got %d: %v", len(all), all)
	}
}
