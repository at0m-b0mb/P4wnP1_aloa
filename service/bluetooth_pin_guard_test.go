//go:build linux
// +build linux

package service

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Audit finding C2. The Bluetooth pairing PIN was the literal "1337" in three
// places -- the compile-time default, the agent built at service start, and the
// shipped template database -- while the shipped controller settings turn
// Discoverable, Bondable and the NAP network service ON at every start.
//
// WiFi had exactly this problem and got wifi_psk_guard.go. Bluetooth never got
// the sibling, which is the finding. These tests hold the new guard to the same
// behaviour as the WiFi one.

func TestTheShippedPinIsRecognisedAsPublic(t *testing.T) {
	// The value actually shipped, in every spelling it appears in.
	for _, pin := range []string{"1337", " 1337 ", "1337\n"} {
		if !isCompromisedBTPin(pin) {
			t.Errorf("isCompromisedBTPin(%q) = false; this is THE shipped PIN and is in the "+
				"published source of every copy of this project", pin)
		}
	}
}

func TestTrivialPinsAreRecognised(t *testing.T) {
	for _, pin := range []string{"0000", "1234", "123456", "1111", "9999", "p4wnp1", "P4wnP1"} {
		if !isCompromisedBTPin(pin) {
			t.Errorf("isCompromisedBTPin(%q) = false; every pairing dialog tries these first", pin)
		}
	}
}

// TestAnEmptyPinIsNotSubstituted pins a deliberate decision. Empty means "no
// PIN configured", and generating one for it would turn "pairing is off" into
// "pairing is on with a credential the operator never asked for".
func TestAnEmptyPinIsNotSubstituted(t *testing.T) {
	for _, pin := range []string{"", "   ", "\n"} {
		if isCompromisedBTPin(pin) {
			t.Errorf("isCompromisedBTPin(%q) = true; empty must mean 'no PIN', not 'bad PIN'", pin)
		}
		got, substituted := guardBluetoothPIN(pin)
		if substituted {
			t.Errorf("guardBluetoothPIN(%q) substituted; an empty PIN must be left alone", pin)
		}
		if got != pin {
			t.Errorf("guardBluetoothPIN(%q) = %q; must pass through unchanged", pin, got)
		}
	}
}

// TestAnOperatorsOwnPinIsLeftAlone is the other direction, and it matters: a
// guard that replaced every PIN would pass the tests above and quietly override
// a choice the operator made deliberately.
func TestAnOperatorsOwnPinIsLeftAlone(t *testing.T) {
	for _, pin := range []string{"84720193", "55512345", "hunter2", "4818"} {
		got, substituted := guardBluetoothPIN(pin)
		if substituted {
			t.Errorf("guardBluetoothPIN(%q) substituted an operator's own PIN", pin)
		}
		if got != pin {
			t.Errorf("guardBluetoothPIN(%q) = %q; must be unchanged", pin, got)
		}
	}
}

// TestTheGuardIsActuallyWiredIn exists because deleting the call compiles
// cleanly.
//
// Go does not complain about an unused package-level function, so removing
// `guardBluetoothPIN(src.Pin)` from DeployBluetoothAgentSettings leaves a tree
// that builds, a guard that is fully tested, and a device that pairs on the
// published PIN. Every other test in this file would still pass. That is the
// shape of defect this repository keeps shipping, so it gets its own check.
//
// Source-level, like tools/check-api-auth.sh, which verifies the same way that
// every HTTP handler authenticates. DeployBluetoothAgentSettings needs a live
// BlueZ over dbus to call for real, so reading the code is what is available --
// and it is the thing that actually went wrong, not a proxy for it.
func TestTheGuardIsActuallyWiredIn(t *testing.T) {
	const (
		file = "bluetooth.go"
		fn   = "func (bt *BtService) DeployBluetoothAgentSettings"
	)
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("reading %s: %v", file, err)
	}
	src := string(b)

	start := strings.Index(src, fn)
	if start < 0 {
		t.Fatalf("could not find %q in %s -- if it was renamed, this check must be "+
			"repointed, not deleted", fn, file)
	}
	// The body ends at the first line that is exactly "}" at column 0.
	end := strings.Index(src[start:], "\n}\n")
	if end < 0 {
		t.Fatalf("could not find the end of %s", fn)
	}
	body := src[start : start+end]

	if !strings.Contains(body, "guardBluetoothPIN(") {
		t.Errorf("DeployBluetoothAgentSettings does not call guardBluetoothPIN.\n"+
			"It is the single chokepoint every deploy route passes through -- the startup "+
			"defaults, a restored template, and the operator's RPC -- so without the call "+
			"there the published PIN reaches the radio.\nBody was:\n%s", body)
	}
	// And the guarded value must be what gets set, not the original.
	if strings.Contains(body, "SetPIN(src.Pin)") {
		t.Error("DeployBluetoothAgentSettings calls SetPIN(src.Pin), which is the UNGUARDED " +
			"value -- the guard's return value is what must be deployed")
	}
}

func TestGeneratedPinShape(t *testing.T) {
	numeric := regexp.MustCompile(`^[0-9]+$`)
	seen := map[string]bool{}

	for i := 0; i < 200; i++ {
		pin, err := generateBTPin()
		if err != nil {
			t.Fatalf("generateBTPin: %v", err)
		}
		if len(pin) != btPinDigits {
			t.Fatalf("generateBTPin() = %q, want exactly %d digits -- leading zeros must be "+
				"KEPT, so the PIN read is the PIN typed", pin, btPinDigits)
		}
		if !numeric.MatchString(pin) {
			t.Fatalf("generateBTPin() = %q, want digits only -- a pairing prompt is very often "+
				"a numeric keypad", pin)
		}
		if _, err := strconv.Atoi(pin); err != nil {
			t.Fatalf("generateBTPin() = %q is not a number: %v", pin, err)
		}
		// A generated PIN must never be one the guard would itself reject.
		if isCompromisedBTPin(pin) {
			t.Fatalf("generateBTPin() produced %q, which is on the compromised list", pin)
		}
		seen[pin] = true
	}

	// Not a statistical test, just a smoke alarm: a constant or a
	// near-constant generator would collapse this to a handful of values. 200
	// draws from 10^8 should essentially never collide.
	if len(seen) < 190 {
		t.Errorf("200 generated PINs produced only %d distinct values; the generator is "+
			"not random enough to be a credential", len(seen))
	}
}

// TestGeneratedPinsAreNotSequential catches the specific way a "random" numeric
// generator usually goes wrong -- seeding from the clock, so consecutive calls
// are adjacent.
func TestGeneratedPinsAreNotSequential(t *testing.T) {
	var prev int
	adjacent := 0
	for i := 0; i < 50; i++ {
		pin, err := generateBTPin()
		if err != nil {
			t.Fatal(err)
		}
		n, _ := strconv.Atoi(pin)
		if i > 0 {
			d := n - prev
			if d < 0 {
				d = -d
			}
			if d < 1000 {
				adjacent++
			}
		}
		prev = n
	}
	if adjacent > 2 {
		t.Errorf("%d of 49 consecutive PINs were within 1000 of each other; this looks like a "+
			"clock-seeded generator, not crypto/rand", adjacent)
	}
}

// TestNoHardcodedPinSurvivesInTheTree is the regression guard for the finding
// as a whole. Three separate places held the literal, and fixing two of three
// would have looked exactly like fixing it.
//
// It reads CODE lines only. An earlier checker in this repository reported its
// own documentation as a defect three times running, and this file's comments
// necessarily mention the value.
func TestNoHardcodedPinSurvivesInTheTree(t *testing.T) {
	// Sites that may legitimately contain it, and why:
	//   bluetooth_pin_guard.go      -- the compromised list; that IS the fix
	//   bluetooth_pin_guard_test.go -- this file
	//   defaults.go                 -- the deliberate trigger value, documented
	// Anything else is a hardcoded PIN that bypasses the guard.
	//
	// Kept as a list rather than a scan so that adding a fourth site is a
	// decision someone has to write down here, not something that slips in.
	allowed := map[string]bool{
		"bluetooth_pin_guard.go":      true,
		"bluetooth_pin_guard_test.go": true,
		"defaults.go":                 true,
	}

	root := ".." // the test runs in service/; walk the whole repo
	var offenders []string
	scanned := 0

	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // unreadable paths are not this test's business
		}
		if info.IsDir() {
			switch info.Name() {
			case ".git", "vendor", "node_modules", "build", "out", "cache":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") {
			return nil
		}
		if allowed[filepath.Base(p)] {
			return nil
		}
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			return nil
		}
		scanned++
		inBlock := false
		for i, line := range strings.Split(string(b), "\n") {
			code := line
			// Strip comments, so this cannot report its own documentation --
			// the mistake this repository has now made four times.
			if inBlock {
				if idx := strings.Index(code, "*/"); idx >= 0 {
					code, inBlock = code[idx+2:], false
				} else {
					continue
				}
			}
			if idx := strings.Index(code, "/*"); idx >= 0 {
				if end := strings.Index(code[idx:], "*/"); end >= 0 {
					code = code[:idx] + code[idx+end+2:]
				} else {
					code, inBlock = code[:idx], true
				}
			}
			if idx := strings.Index(code, "//"); idx >= 0 {
				code = code[:idx]
			}
			if strings.Contains(code, `"1337"`) {
				offenders = append(offenders,
					fmt.Sprintf("%s:%d: %s", p, i+1, strings.TrimSpace(line)))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the tree: %v", err)
	}

	// A scan that read nothing would report no offenders and look like a pass.
	if scanned < 20 {
		t.Fatalf("only %d .go files were scanned; the walk is broken and this test proves "+
			"nothing", scanned)
	}
	for _, o := range offenders {
		t.Errorf("hardcoded Bluetooth PIN outside the guard: %s", o)
	}
}
