//go:build linux
// +build linux

package service

import (
	"testing"

	"github.com/mame82/P4wnP1_aloa/common"
)

// The two paths into USB Mass Storage disagreed about whose job it was to
// turn a name into a path.
//
// DeployGadgetSettings prefixed the image directory. MountUMSFile wrote the
// caller's string straight into configfs. ListUmsImageFlashdrive and
// ListUmsImageCdrom both return BARE NAMES, so the obvious sequence -- list
// the images, mount one -- could only fail, and failed in the least helpful
// way available: the kernel resolves the backing path itself, so the error
// came back as ENOENT against the configfs attribute, naming
// /sys/kernel/config/.../lun.0/file rather than the image that was missing.
//
// Caught on real hardware. Mass storage deployed, the device reported
// success, and no disk appeared on the host.
func TestUMSBackingPath(t *testing.T) {
	for _, c := range []struct {
		name  string
		cdrom bool
		want  string
	}{
		// The case that was broken: a bare name from the listing RPCs.
		{"test.bin", false, common.PATH_IMAGE_FLASHDRIVE + "/test.bin"},
		{"test.iso", true, common.PATH_IMAGE_CDROM + "/test.iso"},

		// Empty is meaningful and must pass through untouched: writing ""
		// to lun.0/file detaches the LUN. Prefixing it would produce the
		// directory path, which the kernel cannot open as a backing file --
		// and that is exactly what the old read-back reported as ".",
		// because filepath.Base("") is ".".
		{"", false, ""},
		{"", true, ""},

		// An absolute path is honoured, for an image kept elsewhere.
		{"/srv/images/payload.img", false, "/srv/images/payload.img"},
		{"/srv/images/payload.iso", true, "/srv/images/payload.iso"},
	} {
		if got := umsBackingPath(c.name, c.cdrom); got != c.want {
			t.Errorf("umsBackingPath(%q, cdrom=%v) = %q, want %q",
				c.name, c.cdrom, got, c.want)
		}
	}
}

// Whatever else changes, a bare name must not reach the kernel unresolved --
// that is the specific failure, and it is silent.
func TestUMSBareNameIsAlwaysResolved(t *testing.T) {
	for _, n := range []string{"a.bin", "image.iso", "deeply.named.file.img"} {
		for _, cdrom := range []bool{false, true} {
			got := umsBackingPath(n, cdrom)
			if got == n {
				t.Errorf("umsBackingPath(%q, cdrom=%v) returned the bare name; "+
					"the kernel cannot resolve that and reports ENOENT against "+
					"the configfs attribute instead of the image", n, cdrom)
			}
			if got[0] != '/' {
				t.Errorf("umsBackingPath(%q, cdrom=%v) = %q, which is not absolute",
					n, cdrom, got)
			}
		}
	}
}

// The two image directories must stay distinct, or a cdrom image would be
// looked for among the flashdrives.
func TestUMSDirectoriesDiffer(t *testing.T) {
	if common.PATH_IMAGE_CDROM == common.PATH_IMAGE_FLASHDRIVE {
		t.Fatal("the cdrom and flashdrive image directories are the same path")
	}
	if umsBackingPath("x.img", false) == umsBackingPath("x.img", true) {
		t.Error("cdrom and flashdrive resolve the same name to the same path")
	}
}
