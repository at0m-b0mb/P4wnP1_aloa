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

// The warning has one job: be impossible to miss, and be true only while it
// is true. An operator who reads "DO NOT POWER OFF" on a device that
// finished ten minutes ago learns to ignore the message, which is worse than
// not having one.
func TestSetupWarningSaysTheOneThingThatMatters(t *testing.T) {
	fb := NewFramebuffer()
	DrawSetupWarning(fb, "42s elapsed", 0.3)
	s := ReadBack(fb)

	for _, want := range []string{"SETTING UP", "42s elapsed"} {
		if !strings.Contains(s, want) {
			t.Errorf("the warning screen does not say %q:\n%s", want, s)
		}
	}
}

// hasScaledText reports whether every pixel of s, drawn centred at y with
// the given scale, is lit in fb.
//
// ReadBack cannot see scaled text -- it matches 5x7 cells against the font
// and a 2x glyph is not one. So the double-height warning, which is the
// whole point of the screen, is checked by rendering the same call into a
// blank buffer and requiring the real screen to contain those exact pixels.
// Asserting on "some pixels are lit around there" would pass for a smear.
func hasScaledText(fb *Framebuffer, y, scale int, s string) bool {
	want := NewFramebuffer()
	want.TextScaledCentered(y, scale, s)
	for x := 0; x < Width; x++ {
		for yy := 0; yy < Height; yy++ {
			if want.Get(x, yy) && !fb.Get(x, yy) {
				return false
			}
		}
	}
	return true
}

// The warning has to be BIG. A device that says "do not power off" in the
// same small type as everything else is a device whose warning gets skimmed.
func TestSetupWarningIsDrawnLarge(t *testing.T) {
	fb := NewFramebuffer()
	DrawSetupWarning(fb, "42s elapsed", 0.3)
	if !hasScaledText(fb, 14, 2, "DO NOT") {
		t.Error("\"DO NOT\" is not rendered at double height where it should be")
	}
	if !hasScaledText(fb, 30, 2, "POWER OFF") {
		t.Error("\"POWER OFF\" is not rendered at double height where it should be")
	}
	// And it must not be drawn at ordinary size by accident.
	small := NewFramebuffer()
	small.TextCentered(14, "DO NOT")
	same := true
	for x := 0; x < Width && same; x++ {
		for y := 0; y < Height; y++ {
			if small.Get(x, y) != fb.Get(x, y) {
				same = false
				break
			}
		}
	}
	if same {
		t.Error("the warning is drawn at ordinary size")
	}
}

// And when it is over, it must say so -- including to someone who looked
// away and came back.
func TestSetupDoneSaysItIsSafe(t *testing.T) {
	fb := NewFramebuffer()
	DrawSetupDone(fb, "took 180s")
	s := ReadBack(fb)
	for _, want := range []string{"SETUP COMPLETE", "Safe to power off", "took 180s"} {
		if !strings.Contains(s, want) {
			t.Errorf("the done screen does not say %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "DO NOT") {
		t.Errorf("the done screen still warns against powering off:\n%s", s)
	}
	if !hasScaledText(fb, 16, 2, "READY") {
		t.Error("the done screen does not say READY at double height")
	}
}

// The panel and the firstboot script must agree about whether setup has
// finished, so both gate on the same file.
func TestFirstBootRunningTracksTheFlag(t *testing.T) {
	dir := t.TempDir()
	flag := filepath.Join(dir, "firstboot.done")

	if !FirstBootRunning(flag) {
		t.Error("with no flag file, setup should be considered RUNNING")
	}
	if err := os.WriteFile(flag, []byte("done\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if FirstBootRunning(flag) {
		t.Error("with the flag file present, setup should be considered FINISHED")
	}
	// An empty path must not make every boot look like a first boot.
	if FirstBootRunning("") {
		t.Error("an empty flag path reported setup as running")
	}
}
