//go:build linux
// +build linux

package service

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// captureLog redirects the standard logger for the duration of a test.
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	w, flags := log.Writer(), log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	t.Cleanup(func() { log.SetOutput(w); log.SetFlags(flags) })
	return &buf
}

// AN ABSENT IMAGE FOLDER IS NOT AN ERROR, SO IT MUST NOT BE LOGGED LIKE ONE.
//
// listImages logs the miss so a genuinely broken install stays discoverable
// in the journal rather than looking like an empty library. But
// ListUmsImageFlashdrive and ListUmsImageCdrom are called every time the
// console's Storage page is opened, and the dist/ums folder is routinely
// absent -- a hand-installed device, a restored backup, or an operator who
// tidied up all leave it gone. Unconditional, that is two SD-card writes per
// page visit for a state that is explicitly normal, and the one line that
// was meant to flag a broken install becomes indistinguishable from
// navigation noise.
func TestAnAbsentImageFolderIsLoggedOncePerProcessNotPerCall(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "definitely-not-there")
	umsMissingLogged.Delete(missing)

	buf := captureLog(t)
	for i := 0; i < 25; i++ {
		got, err := listImages(missing, ".img", ".bin")
		if err != nil {
			t.Fatalf("call %d returned an error for an absent folder: %v", i, err)
		}
		if len(got) != 0 {
			t.Fatalf("call %d listed %d images from an absent folder", i, len(got))
		}
	}

	if n := strings.Count(buf.String(), missing); n != 1 {
		t.Errorf("25 listings of an absent folder wrote %d journal lines, want 1: "+
			"every visit to the Storage page writes to the SD card for a "+
			"condition that is not an error\n%s", n, buf.String())
	}
}

// The two folders are latched SEPARATELY, or the flashdrive miss would
// silence the cdrom one and a half-broken install would report as a whole
// one.
func TestEachImageFolderGetsItsOwnLine(t *testing.T) {
	base := t.TempDir()
	flash := filepath.Join(base, "flashdrive")
	cdrom := filepath.Join(base, "cdrom")
	umsMissingLogged.Delete(flash)
	umsMissingLogged.Delete(cdrom)

	buf := captureLog(t)
	for i := 0; i < 3; i++ {
		if _, err := listImages(flash, ".img", ".bin"); err != nil {
			t.Fatal(err)
		}
		if _, err := listImages(cdrom, ".iso"); err != nil {
			t.Fatal(err)
		}
	}
	if n := strings.Count(buf.String(), flash); n != 1 {
		t.Errorf("the flashdrive folder was named %d times, want 1\n%s", n, buf.String())
	}
	if n := strings.Count(buf.String(), cdrom); n != 1 {
		t.Errorf("the cdrom folder was named %d times, want 1\n%s", n, buf.String())
	}
}

// AND IT IS A LATCH, NOT A MUTE. A folder that is read successfully and then
// removed is a new fact about the device -- an unmounted image store, an
// operator who deleted it while the console was open -- and the journal has
// to be able to say so. A sync.Once would have swallowed it for the life of
// the process.
func TestTheLatchReopensAfterTheFolderHasBeenRead(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ums")
	umsMissingLogged.Delete(dir)

	buf := captureLog(t)
	if _, err := listImages(dir, ".img"); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(buf.String(), dir); n != 1 {
		t.Fatalf("the first miss was logged %d times, want 1\n%s", n, buf.String())
	}

	// The folder appears and is listed, which clears the latch.
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := listImages(dir, ".img"); err != nil {
		t.Fatal(err)
	}

	// And goes again.
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := listImages(dir, ".img"); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(buf.String(), dir); n != 2 {
		t.Errorf("the folder was named %d times, want 2 (gone, read, gone again): a "+
			"folder that disappears after having been read is a state change the "+
			"journal has to carry\n%s", n, buf.String())
	}
}
