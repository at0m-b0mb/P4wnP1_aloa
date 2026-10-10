//go:build linux
// +build linux

package service

import (
	"os"
	"path/filepath"
	"testing"
)

// An ABSENT image folder means "no disk images", which is an ordinary state,
// not an internal server error.
//
// Found by browsing every page of the console against the real service
// binary: ListUmsImageFlashdrive and ListUmsImageCdrom returned the raw
// ioutil.ReadDir error, the bridge classified it as HTTP 500, and the new
// Storage page answered a perfectly normal empty library with
//
//	500 open /usr/local/P4wnP1/ums/flashdrive: no such file or directory
//
// The smoke test already pins the same principle one layer up -- missing
// hardware must report 503, not 500.
func TestListImagesTreatsAnAbsentFolderAsEmpty(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "definitely-not-there")

	got, err := listImages(missing, ".img", ".bin")
	if err != nil {
		t.Fatalf("an absent folder returned an error, which the bridge turns into "+
			"HTTP 500 on a page that should simply say there are no images: %v", err)
	}
	if got == nil {
		t.Error("returned a nil slice; the RPC assigns it straight to MsgArray")
	}
	if len(got) != 0 {
		t.Errorf("an absent folder listed %d images", len(got))
	}
}

// A folder that exists and is empty must behave identically -- the operator
// cannot tell the two apart and neither state is an error.
func TestListImagesHandlesAnEmptyFolder(t *testing.T) {
	dir := t.TempDir()
	got, err := listImages(dir, ".img")
	if err != nil {
		t.Fatalf("an empty folder errored: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("an empty folder listed %d images", len(got))
	}
}

// It must still LIST. A fix that returns empty for everything would make the
// Storage page permanently claim there is nothing to mount.
func TestListImagesStillFindsImages(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"payload.img", "stick.bin", "notes.txt", "disc.iso"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	got, err := listImages(dir, ".img", ".bin")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("listed %v, want exactly payload.img and stick.bin -- the extension "+
			"filter is what keeps a .txt or an .iso out of the flashdrive list", got)
	}
}

// And a REAL failure must still be reported. Swallowing every error would
// turn an unreadable folder into a silent empty library, which is the
// opposite mistake and hides a broken device.
func TestListImagesStillReportsARealFailure(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "locked")
	if err := os.Mkdir(sub, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(sub, 0o700) })
	if os.Geteuid() == 0 {
		t.Skip("running as root, which can read a 0000 directory")
	}
	if _, err := listImages(sub, ".img"); err == nil {
		t.Error("an unreadable folder was reported as empty; a broken install would " +
			"look exactly like a device with no images")
	}
}
