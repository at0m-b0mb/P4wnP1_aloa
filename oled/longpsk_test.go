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
//
// # WHAT THIS TEST USED TO ACCEPT, AND WHY THAT WAS WORTHLESS
//
// It took a clipped key as a pass as long as the clip carried a '~'. The
// clamp at the end of renderQRCard supplies that mark unconditionally, so the
// fix the test exists for -- dropping the QR code so the key can use the full
// width -- was unreachable to it: deleting those three lines left this file
// green, logging "41-char key: clipped but MARKED, acceptable" for 41, 50 and
// 51 characters, while the panel handed the operator 40 characters of a key
// that is 41 long.
//
// So it asserts COMPLETENESS. The arithmetic says it can: with the code
// dropped the column is the full 21 glyphs over 4 rows, which is 84
// characters against WPA2's maximum of 63. No key the card can build needs
// clipping, so a marked clip is a failure here, not an acceptable outcome.
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
		if !strings.ContainsAny(flat, "/") {
			t.Errorf("%d-char key: the page counter is gone:\n%s", n, txt)
		}

		// THE WHOLE KEY, OR THE CARD HAS FAILED AT ITS ONE JOB.
		//
		// KEY1 shreds /root/INITIAL_CREDENTIALS.txt, which is the other
		// copy, so a key the panel cannot show in full is a key the
		// operator cannot recover. A '~' does not redeem that: it says
		// "something is missing" to someone who still has no way to find
		// out what.
		if !strings.Contains(flat, key) {
			marked := ""
			if strings.Contains(txt, "~") {
				marked = " (the cut is marked, which is not good enough: " +
					"21 columns over 4 rows hold 84 characters and WPA2 " +
					"stops at 63, so no key needs clipping -- the QR-drop " +
					"guard in renderQRCard is what makes it fit)"
			}
			t.Errorf("%d-char key: the key is not on the panel in full%s:\n%s", n, marked, txt)
		}
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

// AND THE CARD HAS TO SAY WHICH OUTCOME IT IS IN.
//
// Dropping the code is the right trade, but silently it is indistinguishable
// from a broken panel: the same screen, for the same device, offers a
// scannable code for the 20-character generated key and nothing at all once
// the operator sets a 41-character passphrase. The note renderQRCard draws is
// the only thing that tells them the code was suppressed rather than lost.
func TestTheDroppedJoinCodeSaysSo(t *testing.T) {
	panel := func(key string) string {
		v := NewFirstRunView(&FirstRunCreds{WebPass: "web"})
		client := NewFakeClient()
		client.SSID, client.PSK = "\U0001F4A5P4wnP1", key
		app := NewApp(client, v)
		v.resolveAP(app)
		for i, c := range v.cards() {
			if c.head == "WiFi join" {
				v.page = i
				fb := NewFramebuffer()
				app.Render(fb)
				return ReadBack(fb)
			}
		}
		t.Fatalf("no WiFi card for a %d-character key", len(key))
		return ""
	}

	// 41 characters: one past the narrow column's capacity, so the code
	// goes and the key takes the full width.
	long := panel(strings.Repeat("A", 41))
	if !strings.Contains(long, "no code") {
		t.Errorf("the code was suppressed and the card does not say so, so the "+
			"panel reads as broken:\n%s", long)
	}

	// 20 characters: the code is there, and the card must not claim
	// otherwise.
	short := panel("cGNGK5JiD86SI0QHF8Yh")
	if strings.Contains(short, "no code") {
		t.Errorf("the card says there is no code while drawing one:\n%s", short)
	}
	if !strings.Contains(short, "scan") {
		t.Errorf("a card offering a join code does not say it can be scanned:\n%s", short)
	}
}
