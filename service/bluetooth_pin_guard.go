//go:build linux
// +build linux

package service

// bluetooth_pin_guard.go -- refuse to pair on a PIN that is public knowledge.
//
// WHY THIS EXISTS
//
// The Bluetooth pairing PIN was the literal "1337", in three places: the
// compile-time default (service/defaults.go), the agent constructed at service
// start (NewDefaultAgent in service/bluetooth.go) and the shipped template
// database. It is printed in this project's source, so it is not a secret, and
// it ships alongside defaults that make the radio reachable:
// Discoverable, Bondable and the NAP network service are all ON at every
// service start (service/defaults.go).
//
// WiFi had exactly this problem and got wifi_psk_guard.go. Bluetooth never got
// the equivalent, which is the whole finding -- two sibling radios, one guarded
// and one not. This is that sibling.
//
// The guard lives in DeployBluetoothAgentSettings, which is the single
// chokepoint every route passes through: the startup defaults, a stored
// template restored from a year-old backup, and the operator's own RPC. Putting
// it in defaults.go instead would not work, for the same reason it did not work
// for WiFi -- a deployed template beats a compile-time constant.
//
// WHAT THIS DOES NOT DO, STATED PLAINLY
//
// It does not make Bluetooth legacy pairing strong. With Secure Simple Pairing
// off -- which is the shipped default, see defaults.go CurrentSettings -- the
// link key is derived from the PIN, and anyone who captures the pairing
// exchange can brute-force a short PIN offline. Eight digits is about 27 bits;
// that is a real obstacle to someone typing guesses at a phone and almost none
// to someone who sniffed the exchange and has a laptop.
//
// What it removes is the far worse problem: that the PIN was the same published
// constant on every device ever flashed, so no capture was needed at all. An
// operator who wants pairing to be genuinely strong should turn SSP on and
// accept its "just works" consequence, which the console explains at the point
// of the toggle.

import (
	"crypto/rand"
	"fmt"
	"log"
	"math/big"
	"os"
	"path/filepath"
	"strings"
)

// generatedBTPinPath holds the per-device replacement PIN. /etc/p4wnp1 is mode
// 0700 and the file itself is 0600, matching the WiFi guard.
const generatedBTPinPath = "/etc/p4wnp1/generated-bt.pin"

// btPinDigits is the length of a generated PIN.
//
// Digits only, because a Bluetooth pairing prompt on a phone or a car head unit
// is very often a numeric keypad, and a PIN the operator cannot type is a PIN
// that gets replaced with 0000. Eight rather than four or six because legacy
// pairing has no rate limit worth the name and the extra two digits cost the
// operator nothing.
const btPinDigits = 8

// compromisedBTPins are PINs published in this project's source or
// documentation, plus the handful every pairing dialog tries first.
var compromisedBTPins = []string{
	"1337", // this project's default, in defaults.go, bluetooth.go and dist/db
	"0000",
	"1234",
	"12345",
	"123456",
	"1111",
	"9999",
	"p4wnp1",
}

// isCompromisedBTPin reports whether pin is a known-public or trivial value.
//
// An EMPTY pin is not treated as compromised: it means "no PIN configured", and
// the deploy path handles that separately. Substituting a generated PIN for an
// empty one would turn "Bluetooth pairing is off" into "Bluetooth pairing is on
// with a PIN the operator never asked for".
func isCompromisedBTPin(pin string) bool {
	p := strings.TrimSpace(pin)
	if p == "" {
		return false
	}
	for _, bad := range compromisedBTPins {
		if strings.EqualFold(p, bad) {
			return true
		}
	}
	return false
}

// generateBTPin returns a random numeric PIN of btPinDigits digits.
//
// crypto/rand via math/big, not math/rand: this is a credential. Leading zeros
// are kept, so the PIN the operator reads is exactly the PIN they type.
func generateBTPin() (string, error) {
	max := big.NewInt(1)
	for i := 0; i < btPinDigits; i++ {
		max.Mul(max, big.NewInt(10))
	}
	n, err := rand.Int(rand.Reader, max)
	if err != nil {
		return "", fmt.Errorf("could not read random bytes: %w", err)
	}
	return fmt.Sprintf("%0*d", btPinDigits, n), nil
}

// deviceBTPin returns this device's generated PIN, creating and persisting one
// on first call so it survives reboots and the operator can write it down.
func deviceBTPin() (string, error) {
	if b, err := os.ReadFile(generatedBTPinPath); err == nil {
		if pin := strings.TrimSpace(string(b)); pin != "" && !isCompromisedBTPin(pin) {
			return pin, nil
		}
		log.Printf("Bluetooth: %s exists but holds no usable PIN; regenerating", generatedBTPinPath)
	}

	pin, err := generateBTPin()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(generatedBTPinPath), 0700); err != nil {
		return "", fmt.Errorf("could not create %s: %w", filepath.Dir(generatedBTPinPath), err)
	}
	// Temp file and rename, so a power loss mid-write cannot leave a truncated
	// PIN behind. This device is normally switched off by being pulled out of a
	// USB port, so that is the expected way for it to lose power, not the
	// exceptional one.
	tmp := generatedBTPinPath + ".tmp"
	if err := os.WriteFile(tmp, []byte(pin+"\n"), 0600); err != nil {
		return "", fmt.Errorf("could not write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, generatedBTPinPath); err != nil {
		return "", fmt.Errorf("could not install %s: %w", generatedBTPinPath, err)
	}
	log.Printf("Bluetooth: generated a per-device pairing PIN and stored it at %s", generatedBTPinPath)
	return pin, nil
}

// guardBluetoothPIN returns the PIN that should actually be deployed, and
// whether it had to be substituted.
//
// A failure to generate is reported but does NOT block the deployment, matching
// guardAccessPointPSK: an operator running a lab experiment should still get
// their radio, and the warning is in the journal either way.
func guardBluetoothPIN(pin string) (string, bool) {
	if !isCompromisedBTPin(pin) {
		return pin, false
	}

	// Deliberately NOT printing the configured value, even though this one is
	// public. A log line that sometimes contains a credential trains everyone
	// reading it to expect credentials in logs, and the next value through here
	// is the generated one.
	log.Printf("Bluetooth: REFUSING to pair on the configured PIN -- it is a value " +
		"published in this project's source and is not a secret.")

	generated, err := deviceBTPin()
	if err != nil {
		log.Printf("Bluetooth: could not generate a replacement PIN (%v); leaving the "+
			"configured value in place. ANYONE CAN PAIR WITH THIS DEVICE.", err)
		return pin, false
	}
	log.Printf("Bluetooth: substituted this device's generated PIN. Read it with: "+
		"sudo cat %s", generatedBTPinPath)
	return generated, true
}
