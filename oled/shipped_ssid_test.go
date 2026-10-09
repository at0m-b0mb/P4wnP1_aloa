package oled

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// THE DRIFT THIS FILE EXISTS FOR
//
// The access point's name is written down in three places, and they disagreed:
//
//	dist/db/init.db                        the stored startup template
//	dist/scripts/servicestart.sh           the hardcoded fallback
//	dist/scripts/firstboot-secure-defaults.sh  what the panel and the creds
//	                                           file tell the operator
//
// The database was changed to a 10-byte name so a scan-to-join QR would fit.
// The fallback script still held the old 32-byte emoji name, and on a Pi Zero W
// the fallback ran on every boot (a Bluetooth failure was being treated as a
// whole-template failure), so the radio broadcast 32 bytes while the card
// named 10. The join QR could not be built, and nothing anywhere said why.
//
// Two invariants, both cheap, both checked from the files that actually ship:
//  1. the names agree
//  2. a join URI for that name plus a 20-character generated key fits a
//     panel-sized symbol
//
// Invariant 2 is the one with teeth. It is not a style rule -- it is the
// difference between a phone offering "Join Network" and an operator typing
// twenty random characters with a thumb.
func shippedSSIDs(t *testing.T) map[string]string {
	t.Helper()
	type src struct {
		path string
		re   *regexp.Regexp
	}
	sources := []src{
		{filepath.Join("..", "dist", "scripts", "firstboot-secure-defaults.sh"),
			regexp.MustCompile(`(?m)^P4WNP1_INITIAL_SSID=(.+)$`)},
		{filepath.Join("..", "dist", "scripts", "servicestart.sh"),
			regexp.MustCompile(`wifi set ap .*-s "([^"]+)"`)},
	}
	out := map[string]string{}
	for _, s := range sources {
		b, err := os.ReadFile(s.path)
		if err != nil {
			t.Fatalf("reading %s: %v", s.path, err)
		}
		m := s.re.FindSubmatch(b)
		if m == nil {
			t.Fatalf("no SSID found in %s -- the pattern has stopped matching, so "+
				"this check would pass anything", s.path)
		}
		out[s.path] = string(m[1])
	}
	if len(out) < 2 {
		t.Fatalf("only %d SSID sources were read; this check cannot fail", len(out))
	}
	return out
}

func TestShippedSSIDsAgree(t *testing.T) {
	found := shippedSSIDs(t)
	var first, firstPath string
	for path, ssid := range found {
		if first == "" {
			first, firstPath = ssid, path
			continue
		}
		if ssid != first {
			t.Errorf("the shipped access point name disagrees:\n  %s: %q (%d bytes)\n  %s: %q (%d bytes)",
				firstPath, first, len(first), path, ssid, len(ssid))
		}
	}
}

func TestShippedSSIDFitsAJoinQR(t *testing.T) {
	// A generated PSK is 20 characters; see service/wifi_psk_guard.go.
	const generatedKeyLen = 20
	key := make([]byte, generatedKeyLen)
	for i := range key {
		key[i] = 'A'
	}

	for path, ssid := range shippedSSIDs(t) {
		uri := WiFiURI(ssid, string(key))
		if _, err := NewQRMax([]byte(uri), qrMaxModules); err != nil {
			t.Errorf("%s ships SSID %q (%d bytes), which cannot carry a join code:\n"+
				"  %v\n"+
				"  the URI is %d bytes and a %d-module symbol holds 53.\n"+
				"  Emoji cost 3-4 bytes EACH. Shorten the name, or accept that the\n"+
				"  OLED card will print the key for the operator to type.",
				path, ssid, len(ssid), err, len(uri), qrMaxModules)
		}
	}
}

// And the card built from the shipped name really does offer a join code,
// end to end -- not merely "the encoder did not error".
func TestShippedSSIDProducesAJoinCard(t *testing.T) {
	for path, ssid := range shippedSSIDs(t) {
		v := NewFirstRunView(&FirstRunCreds{WebPass: "web"})
		client := NewFakeClient()
		client.SSID, client.PSK = ssid, "cGNGK5JiD86SI0QHF8Yh"
		v.resolveAP(NewApp(client, v))

		var wifi *credCard
		for i := range v.cards() {
			if c := v.cards()[i]; c.head == "WiFi join" {
				wifi = &c
			}
		}
		if wifi == nil {
			t.Fatalf("%s: no WiFi card was built for SSID %q", path, ssid)
		}
		if wifi.qr == "" {
			t.Errorf("%s: SSID %q produced a key-only card (%q), not a join code",
				path, ssid, wifi.note)
		}
	}
}
