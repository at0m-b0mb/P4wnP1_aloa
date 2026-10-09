package oled

import (
	"strings"
	"testing"
)

// The escaping is the part that can go wrong silently. Backslash, semicolon,
// comma, colon and double quote are all structural in a WIFI: URI, so an
// unescaped one in an SSID or key ends the field early -- producing a QR
// that joins the wrong network, or no network, and looks no different from a
// correct one.
func TestWiFiURIEscaping(t *testing.T) {
	for _, c := range []struct{ ssid, key, want string }{
		{"P4wnP1", "secret", `WIFI:T:WPA;S:P4wnP1;P:secret;;`},
		{"guest;net", "pw", `WIFI:T:WPA;S:guest\;net;P:pw;;`},
		{"a,b", "c:d", `WIFI:T:WPA;S:a\,b;P:c\:d;;`},
		{`q"x`, `back\slash`, `WIFI:T:WPA;S:q\"x;P:back\\slash;;`},
		// An open network must say nopass, not WPA with an empty key --
		// that would make a phone prompt for a password that does not exist.
		{"OpenAP", "", `WIFI:T:nopass;S:OpenAP;;`},
	} {
		if got := WiFiURI(c.ssid, c.key); got != c.want {
			t.Errorf("WiFiURI(%q, %q)\n got: %s\nwant: %s", c.ssid, c.key, got, c.want)
		}
	}
}

// Emoji survive, because a QR encodes bytes. This is what lets the shipped
// SSID keep its branding and still be scannable.
func TestWiFiURIKeepsEmoji(t *testing.T) {
	uri := WiFiURI("\U0001F4A5P4wnP1", "bGu6hrPUPlburycwcsMs")
	if !strings.Contains(uri, "\U0001F4A5P4wnP1") {
		t.Errorf("the emoji SSID did not survive: %q", uri)
	}
	if _, err := NewQRMax([]byte(uri), qrMaxModules); err != nil {
		t.Errorf("the shipped default SSID does not fit a panel-sized symbol: %v\n"+
			"URI is %d bytes; the budget is 53", err, len(uri))
	}
}

// The card offers a join URI when it fits and falls back when it does not --
// and SAYS which, because a QR that quietly means something other than what
// the operator expects is worse than no QR at all.
func TestWiFiCardFallsBackHonestly(t *testing.T) {
	const key = "bGu6hrPUPlburycwcsMs"

	wifiCard := func(ssid string) credCard {
		v := NewFirstRunView(&FirstRunCreds{WiFiPSK: key, WiFiSSID: ssid})
		for _, c := range v.cards() {
			if c.pass == key {
				return c
			}
		}
		t.Fatalf("no WiFi card for ssid %q", ssid)
		return credCard{}
	}

	t.Run("short SSID gets a join URI", func(t *testing.T) {
		c := wifiCard("\U0001F4A5P4wnP1")
		if !strings.HasPrefix(c.qr, "WIFI:") {
			t.Errorf("qr payload is not a join URI: %q", c.qr)
		}
		if !strings.Contains(c.note, "join") {
			t.Errorf("note %q does not tell the operator they can scan to join", c.note)
		}
	})

	t.Run("over-budget SSID falls back to the key", func(t *testing.T) {
		// The old branded SSID: 32 bytes, which pushes the URI past what a
		// panel-sized symbol holds.
		c := wifiCard("\U0001F4A5\U0001F5A5\U0001F4A5 Ⓟ➃ⓌⓃ\U0001F15F❶")
		if c.qr != "" {
			t.Errorf("a 32-byte SSID produced a join URI that cannot be drawn: %q", c.qr)
		}
		if !strings.Contains(c.note, "key only") {
			t.Errorf("note %q does not say the QR is the key alone", c.note)
		}
		// The key must still be shown; losing the join is acceptable,
		// losing the credential is not.
		if c.pass != key {
			t.Errorf("the key is no longer on the card")
		}
	})

	t.Run("no SSID at all still shows the key", func(t *testing.T) {
		v := NewFirstRunView(&FirstRunCreds{WiFiPSK: key})
		for _, c := range v.cards() {
			if c.pass == key {
				if c.qr != "" {
					t.Errorf("a join URI was built with no SSID: %q", c.qr)
				}
				return
			}
		}
		t.Fatal("the WiFi card disappeared when the SSID was unknown")
	})
}

// A version 3 symbol is 62 pixels at one quiet module and 66 at two, so the
// quiet zone has to shrink for the bigger code or nothing is drawn at all --
// which is what happened first: the join URI was encoded correctly and the
// card silently rendered text-only.
func TestWiFiJoinCardActuallyDrawsItsCode(t *testing.T) {
	v := NewFirstRunView(&FirstRunCreds{
		WiFiPSK: "bGu6hrPUPlburycwcsMs", WiFiSSID: "\U0001F4A5P4wnP1"})
	app := NewApp(NewFakeClient(), v)
	for i, card := range v.cards() {
		if card.pass == "" {
			continue
		}
		v.page = i
		fb := NewFramebuffer()
		app.Render(fb)

		lit := 0
		for y := 0; y < Height; y++ {
			for x := 0; x < 62; x++ {
				if fb.Get(x, y) {
					lit++
				}
			}
		}
		if lit < 500 {
			t.Fatalf("only %d lit pixels in the code column: the join QR was "+
				"encoded but never drawn", lit)
		}
		// And the key is still readable beside it.
		flat := strings.Join(strings.Fields(ReadBack(fb)), "")
		if !strings.Contains(flat, card.pass) {
			t.Errorf("the key is not readable on the card:\n%s", ReadBack(fb))
		}
	}
}
