//go:build linux
// +build linux

package service

// wifi_psk_guard.go -- refuse to broadcast an access point on a PSK that is
// public knowledge.
//
// WHY THIS EXISTS
//
// The template database shipped in dist/db still carries the upstream default
// PSK "MaMe82-P4wnP1". It is in the published source of every copy of this
// project, so it is not a secret in any sense -- yet it is what a freshly
// flashed device would actually broadcast, because the deployed template wins
// over the compile-time default in service/defaults.go.
//
// Changing the constant in defaults.go did not fix this, and neither does
// changing the shipped database: an operator restoring an older backup, or
// upgrading a device that has been running for a year, gets the bad PSK back.
// The check therefore lives in the code path that actually starts the access
// point, where it catches every route to the same outcome.
//
// On a match, a per-device PSK is generated once and reused from then on, so
// the key does not change on every boot and an operator can write it down.

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// generatedPSKPath holds the per-device replacement PSK. /etc/p4wnp1 is mode
// 0700 and the file itself is 0600.
const generatedPSKPath = "/etc/p4wnp1/generated-ap.psk"

// compromisedPSKs are PSKs published in this project's source or documentation.
// Compared case-insensitively.
var compromisedPSKs = []string{
	"MaMe82-P4wnP1",      // upstream default, in dist/db and years of docs
	"HackProKP-changeme", // this fork's placeholder in service/defaults.go
	"p4wnp1",
	"toor",
	"changeme",
}

// isCompromisedPSK reports whether psk is a known-public value.
func isCompromisedPSK(psk string) bool {
	p := strings.TrimSpace(psk)
	for _, bad := range compromisedPSKs {
		if strings.EqualFold(p, bad) {
			return true
		}
	}
	return false
}

// generatePSK returns a random 20-character PSK.
//
// WPA2 PSKs are 8..63 printable ASCII. The base64 alphabet is trimmed of
// characters that get mangled when a key is retyped from a screen or passed
// through a shell -- and '/' and '+' in particular are a common source of
// "the password is wrong" support traffic.
func generatePSK() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("could not read random bytes: %w", err)
	}
	s := base64.StdEncoding.EncodeToString(buf)
	s = strings.NewReplacer("/", "", "+", "", "=", "").Replace(s)
	if len(s) < 20 {
		return "", fmt.Errorf("generated PSK too short (%d chars)", len(s))
	}
	return s[:20], nil
}

// devicePSK returns this device's generated PSK, creating and persisting one
// on first call.
func devicePSK() (string, error) {
	if b, err := os.ReadFile(generatedPSKPath); err == nil {
		if psk := strings.TrimSpace(string(b)); len(psk) >= 8 {
			return psk, nil
		}
		log.Printf("WiFi: %s exists but holds no usable PSK; regenerating", generatedPSKPath)
	}

	psk, err := generatePSK()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(generatedPSKPath), 0700); err != nil {
		return "", fmt.Errorf("could not create %s: %w", filepath.Dir(generatedPSKPath), err)
	}
	// Write via a temp file and rename so a power loss mid-write cannot leave a
	// truncated key behind -- this device is normally switched off by being
	// pulled out of a USB port.
	tmp := generatedPSKPath + ".tmp"
	if err := os.WriteFile(tmp, []byte(psk+"\n"), 0600); err != nil {
		return "", fmt.Errorf("could not write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, generatedPSKPath); err != nil {
		return "", fmt.Errorf("could not install %s: %w", generatedPSKPath, err)
	}
	log.Printf("WiFi: generated a per-device access point PSK and stored it at %s", generatedPSKPath)
	return psk, nil
}

// guardAccessPointPSK replaces a publicly-known PSK with this device's
// generated one, in place.
//
// Returns true when a substitution happened. A failure to generate is reported
// but does NOT block the deployment: an operator who has deliberately set a
// weak key on a lab network should still get their access point, and the
// warning is in the journal either way.
func guardAccessPointPSK(settings *WiFiSettingsPSKView) bool {
	if settings == nil || settings.PSK() == "" {
		return false
	}
	if !isCompromisedPSK(settings.PSK()) {
		return false
	}

	// Deliberately NOT printing the value, even though this particular one is
	// public: a log line that sometimes contains a PSK trains everyone reading
	// it to expect PSKs in logs.
	log.Printf("WiFi: REFUSING to broadcast on the configured PSK -- it is a value " +
		"published in this project's source and is not a secret.")

	psk, err := devicePSK()
	if err != nil {
		log.Printf("WiFi: could not generate a replacement PSK (%v); leaving the configured "+
			"value in place. ANYONE CAN JOIN THIS ACCESS POINT.", err)
		return false
	}
	settings.SetPSK(psk)
	log.Printf("WiFi: substituted this device's generated PSK. Read it with: "+
		"sudo cat %s", generatedPSKPath)
	return true
}

// WiFiSettingsPSKView is the narrow slice of WiFiSettings the guard needs,
// so the guard can be unit-tested without constructing protobuf messages.
type WiFiSettingsPSKView struct {
	Get func() string
	Set func(string)
}

func (v *WiFiSettingsPSKView) PSK() string {
	if v == nil || v.Get == nil {
		return ""
	}
	return v.Get()
}

func (v *WiFiSettingsPSKView) SetPSK(s string) {
	if v != nil && v.Set != nil {
		v.Set(s)
	}
}
