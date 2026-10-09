package oled

import (
	"strings"
	"testing"
)

func qrTestCreds() *FirstRunCreds {
	return &FirstRunCreds{
		SSHUser: "p4wnp1", SSHPass: "eSjpYjuIKylArsuPNrI6",
		WebUser: "admin", WebPass: "YXpXK1UdUyM5069A3XAM",
		WiFiPSK: "bGu6hrPUPlburycwcsMs",
	}
}

// Each secret card must still show the password as text. The QR is for the
// operator with a phone; the text is for the one without, and dropping it in
// favour of a prettier screen would lock that person out.
//
// The QR codes on these exact cards were decoded for real with OpenCV and
// came back byte for byte -- see qr_test.go for why that check cannot run
// here. What this asserts is everything around it that Go can see.
func TestFirstRunSecretCardsShowBothQRAndText(t *testing.T) {
	c := qrTestCreds()
	v := NewFirstRunView(c)
	app := NewApp(NewFakeClient(), v)

	cards := v.cards()
	secrets := 0
	for i, card := range cards {
		if card.pass == "" {
			continue
		}
		secrets++
		v.page = i
		fb := NewFramebuffer()
		app.Render(fb)
		text := ReadBack(fb)
		flat := strings.ReplaceAll(text, "\n", "")
		flat = strings.ReplaceAll(flat, " ", "")

		// The whole secret, allowing for the wrap across two lines.
		if !strings.Contains(flat, card.pass) {
			t.Errorf("card %d (%q): the password is not readable on the panel.\n%s",
				i, card.head, text)
		}
		// And it has to say which key destroys it, since the hint line that
		// normally carries that is gone on these cards.
		if !strings.Contains(text, "KEY1") {
			t.Errorf("card %d: no KEY1 hint, and the chrome that usually "+
				"carries it is suppressed here.\n%s", i, text)
		}
		// Something must actually be drawn in the QR column, or this is just
		// the old text card with the chrome removed.
		lit := 0
		for y := 0; y < Height; y++ {
			for x := 0; x < 50; x++ {
				if fb.Get(x, y) {
					lit++
				}
			}
		}
		if lit < 500 {
			t.Errorf("card %d: only %d lit pixels in the QR column; nothing was drawn", i, lit)
		}
	}
	if secrets != 3 {
		t.Fatalf("expected 3 secret cards (ssh, web, wifi), found %d", secrets)
	}
}

// The chrome is suppressed on the secret cards and kept everywhere else. If
// it were ever drawn on a QR card the hint line would land across the bottom
// rows of the symbol and it would stop scanning -- silently, because the
// screen would still look fine.
func TestOnlySecretCardsAreChromeless(t *testing.T) {
	v := NewFirstRunView(qrTestCreds())
	cards := v.cards()
	for i, card := range cards {
		v.page = i
		want := card.pass != ""
		if got := v.Chromeless(); got != want {
			t.Errorf("card %d (%q): Chromeless()=%v, want %v", i, card.head, got, want)
		}
	}
	// The "erased" summary is an ordinary screen again.
	v.done = true
	if v.Chromeless() {
		t.Error("the erased summary should keep its title and hint")
	}
}

// A QR card must not be taller than the panel, and the arithmetic that makes
// that true is tight enough to be worth pinning: version 2 at 2 pixels per
// module with a 2-module quiet zone is exactly 58 of the 64 pixels. Lose six
// pixels anywhere and this stops fitting.
func TestQRCardFitsThePanel(t *testing.T) {
	q, err := NewQR([]byte("YXpXK1UdUyM5069A3XAM"))
	if err != nil {
		t.Fatal(err)
	}
	dim := QRPixels(q, 2, 2)
	if dim > Height {
		t.Fatalf("a %dpx symbol will not fit a %dpx panel", dim, Height)
	}
	if left := Width - dim - 3; left/CharW < 9 {
		t.Errorf("only %d characters of text fit beside the code; "+
			"the password needs two lines of at least 10", left/CharW)
	}
}

func TestChunkString(t *testing.T) {
	for _, c := range []struct {
		in   string
		n    int
		want []string
	}{
		{"YXpXK1UdUyM5069A3XAM", 11, []string{"YXpXK1UdUyM", "5069A3XAM"}},
		{"short", 11, []string{"short"}},
		{"", 11, nil},
		{"abc", 1, []string{"a", "b", "c"}},
		// n <= 0 must not spin forever; it returns the string whole.
		{"abc", 0, []string{"abc"}},
	} {
		got := chunkString(c.in, c.n)
		if len(got) != len(c.want) {
			t.Errorf("chunkString(%q,%d) = %q, want %q", c.in, c.n, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("chunkString(%q,%d) = %q, want %q", c.in, c.n, got, c.want)
				break
			}
		}
	}
}
