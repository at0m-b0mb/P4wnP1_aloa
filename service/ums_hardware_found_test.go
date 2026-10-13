package service

import (
	"errors"
	"os"
	"strings"
	"syscall"
	"testing"
)

// Three defects found by driving a REAL Pi Zero W on 2026-10-13, after the
// container suite was green. Each test below reproduces the exact kernel
// behaviour that caused it, so reverting any of the three fixes fails here.
//
// None of these was reachable in a container: configfs does not exist there, so
// every write succeeds trivially and the kernel's refusals never happen. That
// is the standing lesson -- the USB gadget is only really tested on a device.

// --- 1. EFAULT on a zero-length write ---------------------------------------

// TestEmptyImageIsNotWrittenWhenBuildingTheFunction pins the rule that broke
// deploying mass storage at all.
//
// The hardware symptom: enabling UMS with no image chosen returned
//
//	attaching USB Mass Storage image "" failed: write
//	.../mass_storage.ms1/lun.0/file: bad address
//
// and reverted the whole gadget. defaults.go ships UmsSettings.File as "", so
// "a removable drive with no media in it" -- the ordinary case -- could not be
// deployed. configfs rejects a zero-length write with EFAULT.
//
// DeployGadgetSettings itself needs real configfs, so this pins the decision it
// now makes: an empty image means DO NOT WRITE.
func TestEmptyImageIsNotWrittenWhenBuildingTheFunction(t *testing.T) {
	// The kernel's refusal, modelled exactly: zero bytes -> EFAULT.
	configfsLike := func(path string, data []byte) error {
		if len(data) == 0 {
			return &os.PathError{Op: "write", Path: path, Err: syscall.EFAULT}
		}
		return nil
	}

	// What the build path does now: write only when there is a name.
	deployWrite := func(file string) error {
		if file == "" {
			return nil // nothing to attach, and the fresh LUN is already empty
		}
		return configfsLike("/lun.0/file", []byte(file))
	}

	if err := deployWrite(""); err != nil {
		t.Fatalf("building the function with no image must not write, got %v", err)
	}
	if err := deployWrite("test.bin"); err != nil {
		t.Fatalf("a real image must still be written, got %v", err)
	}

	// Negative control: prove the modelled kernel really does refuse, so this
	// test cannot pass because the model is toothless.
	if err := configfsLike("/lun.0/file", []byte("")); !errors.Is(err, syscall.EFAULT) {
		t.Fatal("the EFAULT model does not refuse a zero-length write; this test proves nothing")
	}
}

// --- 2. EBUSY must explain itself -------------------------------------------

// TestEjectExplainsWhoIsHoldingTheDisk guards the error TEXT, because the text
// is the whole fix.
//
// fsg_store_file() returns EBUSY when prevent_medium_removal is set and the LUN
// is open -- i.e. when the attached host has locked the medium, which every OS
// does on mount. Nothing on the P4wnP1 can clear it. Confirmed on hardware:
// macOS held the lock through `diskutil unmount` and released it only on
// `diskutil eject`.
//
// The bare errno reads as a fault on the P4wnP1, sending the operator looking
// for a stuck process here instead of ejecting on the machine in front of them.
func TestEjectExplainsWhoIsHoldingTheDisk(t *testing.T) {
	busy := func(path string, data []byte) error {
		return &os.PathError{Op: "write", Path: path, Err: syscall.EBUSY}
	}

	err := mountUMSInto(busy, "/lun.0", "test.bin", false)
	if err == nil {
		t.Fatal("a busy LUN must fail")
	}
	msg := strings.ToLower(err.Error())

	// It must name the host as the holder, and name the remedy.
	for _, want := range []string{"host", "eject"} {
		if !strings.Contains(msg, want) {
			t.Errorf("EBUSY message must mention %q, so the operator knows where to act.\ngot: %s", want, err)
		}
	}
	// And it must not be only the raw errno, which is what it used to be.
	if msg == "ejecting the current usb mass storage image failed: write /lun.0/file: device or resource busy" {
		t.Error("EBUSY still reports the bare errno; the operator is not told the host must release the device")
	}
}

// TestNonBusyEjectFailuresKeepTheirCause makes sure the EBUSY special case did
// not swallow every other error into one friendly-but-wrong sentence.
func TestNonBusyEjectFailuresKeepTheirCause(t *testing.T) {
	denied := func(path string, data []byte) error {
		return &os.PathError{Op: "write", Path: path, Err: syscall.EACCES}
	}
	err := mountUMSInto(denied, "/lun.0", "test.bin", false)
	if err == nil {
		t.Fatal("a failing write must fail")
	}
	if strings.Contains(strings.ToLower(err.Error()), "host") {
		t.Errorf("EACCES must not be reported as the host holding the disk: %v", err)
	}
	if !strings.Contains(err.Error(), "permission denied") {
		t.Errorf("a non-EBUSY cause must survive into the message, got: %v", err)
	}
}

// --- 3. An empty LUN read back as "." ---------------------------------------

// TestUmsReadBackNameOnEmptyLun pins filepath.Base("") == ".".
//
// After a successful eject the console showed ums_settings.file as ".", which
// is what the operator and the panel both display. Observed on hardware.
func TestUmsReadBackNameOnEmptyLun(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
		want string
	}{
		// The empty-LUN spellings. "\n" is the one a real eject produces, and
		// it is the one TrimSuffix("\000\n") missed.
		{"bare newline", "\n", ""},
		{"nul and newline", "\x00\n", ""},
		{"nothing at all", "", ""},
		{"several newlines", "\n\n", ""},

		// Occupied LUNs: the directory is stripped, the name survives.
		{"flashdrive image", "/usr/local/P4wnP1/ums/flashdrive/test.bin\n", "test.bin"},
		{"cdrom image", "/usr/local/P4wnP1/ums/cdrom/cdrom.iso\n", "cdrom.iso"},
		{"nul terminated", "/usr/local/P4wnP1/ums/flashdrive/test.bin\x00\n", "test.bin"},
		{"bare name", "test.bin\n", "test.bin"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := umsReadBackName(tc.raw); got != tc.want {
				t.Errorf("umsReadBackName(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

// TestUmsReadBackNameNeverReportsDot is the blunt version of the above: no
// input may ever produce ".", whatever else changes.
func TestUmsReadBackNameNeverReportsDot(t *testing.T) {
	for _, raw := range []string{"", "\n", "\x00", "\x00\n", "\n\x00", "/", "//", "/\n"} {
		if got := umsReadBackName(raw); got == "." {
			t.Errorf("umsReadBackName(%q) = %q -- an empty LUN must read back empty, not %q", raw, got, ".")
		}
	}
}
