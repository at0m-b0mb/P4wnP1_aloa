package oled

import (
	"strings"
	"testing"
)

// psk() can now return whatever key the access point is actually using, and
// WPA2 allows 63 characters. Before the live-key change it was always the
// 20-character generated key, so nothing exercised a long one.
//
// The failure was not a clipped line. renderQRCard draws the page counter and
// "KEY1 erase" at fixed rows, Framebuffer.Text ORs pixels rather than
// replacing them, and the secret's chunks were not bounded -- so at 41
// characters the fifth chunk landed on the counter and at 51 the sixth
// swallowed the erase hint, producing overlapped glyphs with no mark to say
// anything had been cut.
func TestLongAPKeyNeverOverprintsTheFooter(t *testing.T) {
	for _, n := range []int{20, 32, 40, 41, 50, 51, 63} {
		key := strings.Repeat("A", n)
		v := NewFirstRunView(&FirstRunCreds{WebPass: "web"})
		client := NewFakeClient()
		client.SSID, client.PSK = "\U0001F4A5P4wnP1", key
		app := NewApp(client, v)
		v.resolveAP(app)

		var idx = -1
		for i, c := range v.cards() {
			if c.head == "WiFi join" {
				idx = i
			}
		}
		if idx < 0 {
			t.Fatalf("%d-char key: no WiFi card", n)
		}
		v.page = idx
		fb := NewFramebuffer()
		app.Render(fb)
		txt := ReadBack(fb)

		// The footer must survive intact. It is the operator's only
		// instruction for getting rid of the credentials.
		if !strings.Contains(txt, "KEY1 erase") {
			t.Errorf("%d-char key: the 'KEY1 erase' line is gone:\n%s", n, txt)
		}
		flat := strings.Join(strings.Fields(txt), "")
		if !strings.Contains(flat, "4/4") && !strings.Contains(flat, "2/2") && !strings.Contains(flat, "3/3") {
			// the page counter, whatever the total happens to be
			if !strings.ContainsAny(flat, "/") {
				t.Errorf("%d-char key: the page counter is gone:\n%s", n, txt)
			}
		}

		// The key must be readable IN FULL, or explicitly marked as cut.
		// A silently clipped credential is the dangerous outcome: the
		// operator types it and gets rejected with no clue why.
		if strings.Contains(flat, key) {
			continue
		}
		if strings.Contains(txt, "~") {
			t.Logf("%d-char key: clipped but MARKED, acceptable", n)
			continue
		}
		t.Errorf("%d-char key: the key is neither complete nor marked as cut:\n%s", n, txt)
	}
}

// The trade is deliberate: past the width of the narrow column the QR is
// dropped so the key can use the whole panel. Short keys must keep their
// code, or this "fix" would have quietly removed the join feature.
func TestShortKeysKeepTheirJoinCode(t *testing.T) {
	v := NewFirstRunView(&FirstRunCreds{WebPass: "web"})
	client := NewFakeClient()
	client.SSID, client.PSK = "\U0001F4A5P4wnP1", "cGNGK5JiD86SI0QHF8Yh" // 20, the generated length
	app := NewApp(client, v)
	v.resolveAP(app)

	for i, c := range v.cards() {
		if c.head != "WiFi join" {
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
			t.Fatalf("the join code was dropped for a 20-character key: only %d lit pixels", lit)
		}
		return
	}
	t.Fatal("no WiFi card")
}
