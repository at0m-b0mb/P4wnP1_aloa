//go:build linux
// +build linux

package service

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// BOTH OF THESE WERE FOUND ON HARDWARE, NOT BY READING THE CODE.
//
// Driving a real Pi Zero W with an image attached:
//
//	swap to another image -> "device or resource busy"   (EBUSY)
//	eject                 -> "bad address"               (EFAULT)
//
// EBUSY is the mass_storage gadget's contract: lun.0/file cannot be rewritten
// while a backing file is attached. The kernel models a disk as a medium --
// eject it, then insert the next one -- and writing a new path over a live one
// is not a transition it offers.
//
// EFAULT is why EJECT ITSELF never worked, and it is the subtler of the two.
// Clearing the attribute means writing an empty value, and os.WriteFile with
// an empty slice issues a ZERO-LENGTH write, which configfs rejects. Every
// other attribute write in SubSysUSB.go passes at least one byte ("0", "1"),
// which is why nothing else ever tripped over it.
//
// The order is the whole fix, and a test against a temp directory could only
// see the final file contents -- so the writer is injected and the calls are
// recorded.

type wr struct{ path, data string }

func recorder(fail map[string]error) (func(string, []byte) error, *[]wr) {
	var got []wr
	return func(p string, d []byte) error {
		got = append(got, wr{p, string(d)})
		if fail != nil {
			if e, ok := fail[p+"="+string(d)]; ok {
				return e
			}
		}
		return nil
	}, &got
}

func TestMountEjectsBeforeInserting(t *testing.T) {
	write, got := recorder(nil)
	if err := mountUMSInto(write, "/lun", "/img/payload.img", false); err != nil {
		t.Fatal(err)
	}
	if len(*got) != 3 {
		t.Fatalf("made %d writes, want 3 (eject, cdrom, insert): %+v", len(*got), *got)
	}
	if (*got)[0].path != "/lun/file" || (*got)[0].data == "" {
		t.Errorf("first write must EJECT, and must not be zero-length: %+v", (*got)[0])
	}
	if (*got)[2].path != "/lun/file" || (*got)[2].data != "/img/payload.img" {
		t.Errorf("last write must insert the image: %+v", (*got)[2])
	}
	// The ordering is the fix. Writing the new path first is what the kernel
	// refuses with EBUSY.
	if (*got)[0].data == (*got)[2].data {
		t.Error("it wrote the same value twice; nothing was ejected")
	}
}

// Nothing may ever issue a zero-length write to configfs: that is EFAULT, and
// it is what made eject fail.
func TestNoZeroLengthWriteEver(t *testing.T) {
	for _, c := range []struct {
		name  string
		file  string
		cdrom bool
	}{
		{"insert a flashdrive image", "/img/a.img", false},
		{"insert a cdrom image", "/img/a.iso", true},
		{"eject", "", false},
		{"eject in cdrom mode", "", true},
	} {
		write, got := recorder(nil)
		if err := mountUMSInto(write, "/lun", c.file, c.cdrom); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		for i, w := range *got {
			if len(w.data) == 0 {
				t.Errorf("%s: write %d to %s is ZERO LENGTH -- configfs answers EFAULT, "+
					"'bad address', and the operation silently cannot work", c.name, i, w.path)
			}
		}
	}
}

// Eject must stop after detaching. Carrying on to write a cdrom flag and an
// empty path is how the EFAULT came back by another route.
func TestEjectDetachesAndStops(t *testing.T) {
	write, got := recorder(nil)
	if err := mountUMSInto(write, "/lun", "", false); err != nil {
		t.Fatal(err)
	}
	if len(*got) != 1 {
		t.Fatalf("eject made %d writes, want exactly 1: %+v", len(*got), *got)
	}
	if (*got)[0].path != "/lun/file" {
		t.Errorf("eject wrote to %s", (*got)[0].path)
	}
}

// cdrom is a property of the LUN, not of the path. MountUMSFile used the flag
// only to choose a directory, so a swap from a flashdrive to an .iso left the
// LUN in disk mode and the host saw a writable volume where a CD-ROM was
// asked for. It must be written, and while the LUN is detached.
func TestCdromFlagIsWrittenWhileDetached(t *testing.T) {
	for _, c := range []struct {
		cdrom bool
		want  string
	}{{true, "1"}, {false, "0"}} {
		write, got := recorder(nil)
		if err := mountUMSInto(write, "/lun", "/img/x", c.cdrom); err != nil {
			t.Fatal(err)
		}
		var idx = -1
		for i, w := range *got {
			if w.path == "/lun/cdrom" {
				idx = i
				if w.data != c.want {
					t.Errorf("cdrom=%v wrote %q, want %q", c.cdrom, w.data, c.want)
				}
			}
		}
		if idx == -1 {
			t.Fatalf("cdrom=%v: the cdrom attribute was never written, so the LUN keeps "+
				"whatever mode it had and the host sees the wrong kind of device", c.cdrom)
		}
		if idx != 1 {
			t.Errorf("cdrom written at step %d; it must come after the eject and before "+
				"the insert, because the kernel only accepts it while detached", idx)
		}
	}
}

// A failure to eject must be reported as a failure to eject. Reporting it as
// "setting backing file" sent the operator looking at the image.
func TestEjectFailureIsNamedAsSuch(t *testing.T) {
	write, _ := recorder(map[string]error{"/lun/file=\n": errors.New("device or resource busy")})
	err := mountUMSInto(write, "/lun", "/img/x", false)
	if err == nil {
		t.Fatal("a failed eject was reported as success")
	}
	if !strings.Contains(err.Error(), "eject") {
		t.Errorf("error %q does not say the EJECT failed", err)
	}
}

func TestInsertFailureNamesTheImage(t *testing.T) {
	write, _ := recorder(map[string]error{"/lun/file=/img/x": fmt.Errorf("no such file")})
	err := mountUMSInto(write, "/lun", "/img/x", false)
	if err == nil || !strings.Contains(err.Error(), "/img/x") {
		t.Errorf("error %v does not name the image the operator chose", err)
	}
}
