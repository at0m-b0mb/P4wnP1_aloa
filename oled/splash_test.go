package oled

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The brand is read from a file on the device, so the parsing is what stands
// between an operator editing one line and a splash that renders wrong.
func TestLoadBrand(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
		return p
	}

	cases := []struct {
		name, body, wantName, wantTag string
	}{
		{"plain", "at0m-b0mb\n", "at0m-b0mb", ""},
		{"two lines", "at0m-b0mb\ndefence by love\n", "at0m-b0mb", "defence by love"},
		{"comments and blanks", "# the name\n\n  at0m-b0mb  \n\n# tag\nsecond\n", "at0m-b0mb", "second"},
		{"empty file", "", "", ""},
		{"only comments", "# nothing here\n", "", ""},
		{"extra lines ignored", "a\nb\nc\nd\n", "a", "b"},
	}
	for _, c := range cases {
		got := LoadBrand(write(c.name, c.body))
		if got.Name != c.wantName || got.Tagline != c.wantTag {
			t.Errorf("%s: got {%q,%q}, want {%q,%q}", c.name, got.Name, got.Tagline, c.wantName, c.wantTag)
		}
	}

	// A missing file is the normal case on an unbranded device, not an error.
	if b := LoadBrand(filepath.Join(dir, "absent")); b.Name != "" || b.Tagline != "" {
		t.Errorf("a missing brand file produced %+v", b)
	}
}

func TestLetterspace(t *testing.T) {
	if got := Letterspace("abc"); got != "a b c" {
		t.Errorf("Letterspace = %q", got)
	}
	if got := Letterspace(""); got != "" {
		t.Errorf("Letterspace(empty) = %q", got)
	}
}

// Nothing the brand draws may collide with the subtitle above it or run off
// the panel below. Both happened: a hairline drew straight through
// "A . L . O . A .", and the fix for that read as an underline of it.
func TestBrandedSplashStaysInItsBand(t *testing.T) {
	cases := []Brand{
		{Name: "at0m-b0mb"},
		{Name: "at0m-b0mb", Tagline: "defence by love"},
		{Name: "a"},
		{Name: strings.Repeat("x", 60), Tagline: strings.Repeat("y", 60)},
	}
	for _, b := range cases {
		fb := NewFramebuffer()
		DrawSplashBranded(fb, "", -1, b)

		// The subtitle occupies y=35..41. Nothing the brand adds may touch
		// rows 42..44, the breathing room between the two.
		for y := 42; y <= 44; y++ {
			for x := 0; x < Width; x++ {
				if fb.Get(x, y) {
					t.Errorf("brand %+v: drew into the gap at y=%d, x=%d\n%s", b, y, x, fb)
					break
				}
			}
		}

		// The subtitle must still be legible, i.e. unchanged.
		plain := NewFramebuffer()
		DrawSplashBranded(plain, "", -1, Brand{})
		for y := 35; y <= 41; y++ {
			for x := 0; x < Width; x++ {
				if fb.Get(x, y) != plain.Get(x, y) {
					t.Errorf("brand %+v: altered the subtitle at (%d,%d)\n%s", b, x, y, fb)
					break
				}
			}
		}

		// And it must actually have drawn something.
		lit := 0
		for y := 45; y < Height; y++ {
			for x := 0; x < Width; x++ {
				if fb.Get(x, y) {
					lit++
				}
			}
		}
		if lit == 0 {
			t.Errorf("brand %+v: nothing was drawn in the lower band", b)
		}
	}
}
