package oled

import (
	"fmt"
	"os"
	"strings"
)

// The boot splash.
//
// A device that sits on someone's desk pretending to be a USB stick should
// look like it was made on purpose when you do glance at it. On a 128x64
// 1bpp panel that means doing less, carefully: a wordmark with room around it,
// one hairline rule, and a single line of small type. No gradients to dither,
// no icon that would read as mush at 7 pixels.
//
// The house display face is a serif, which cannot survive at this size -- its
// brackets and modulated strokes land on nothing. So the wordmark is the body
// face at 2x with the slab underline doing the job the serif would: giving the
// name weight and a baseline to sit on.

// Brand is the operator's own mark on the boot screen.
//
// Configurable rather than compiled in, because a device that gets sold or
// handed over should carry the name of whoever is standing behind it, and
// that is not a thing to need a rebuild for. Set it with --brand, or put a
// line in /etc/p4wnp1/brand.txt and every image built from this tree picks
// it up.
//
// Kept to one short line in the lower band: the wordmark is the identity of
// the device, and a second mark competing with it at the same weight makes
// both of them read as clutter on a 128-pixel screen.
type Brand struct {
	// Name is the line under the rule, e.g. "at0m-b0mb".
	Name string
	// Tagline is an optional second line, smaller in effect because it sits
	// below the fold of attention rather than in a smaller face -- there is
	// no smaller face at this size.
	Tagline string
}

// BrandFile is where the daemon looks for a brand if none was passed.
const BrandFile = "/etc/p4wnp1/brand.txt"

// Letterspace puts a thin space between characters, which is what makes a
// short string read as a mark rather than as a word. Used for the subtitle
// and for a brand name short enough to carry it.
func Letterspace(s string) string {
	r := []rune(s)
	out := make([]rune, 0, len(r)*2)
	for i, c := range r {
		if i > 0 {
			out = append(out, ' ')
		}
		out = append(out, c)
	}
	return string(out)
}

// DrawSplash renders the boot screen. progress is 0..1; pass a negative number
// to omit the progress rule entirely.
func DrawSplash(fb *Framebuffer, version string, progress float64) {
	DrawSplashBranded(fb, version, progress, Brand{})
}

// DrawSplashBranded is DrawSplash with the operator's mark.
func DrawSplashBranded(fb *Framebuffer, version string, progress float64, b Brand) {
	fb.Clear()

	// Wordmark, optically centred: text sits slightly above true centre so the
	// composition does not look bottom-heavy once the small type is under it.
	fb.TextScaledCentered(12, 2, "P4wnP1")

	// Slab under the wordmark, inset to the text width rather than the screen
	// width, so it reads as part of the mark.
	w := TextWidthScaled("P4wnP1", 2)
	x := (Width - w) / 2
	fb.FillRect(x, 28, w, 2, true)

	// Letterspaced subtitle. Spacing it out is what makes four initials read as
	// a mark rather than an abbreviation someone forgot to expand.
	fb.TextCentered(35, "A . L . O . A .")

	// The bar and the version share the lower band, so only one is shown. The
	// first render of this put the bar at y=50 and the version at y=55, which
	// left them touching with no air between -- visible immediately once it
	// was drawn, invisible while it was only arithmetic.
	// The lower band carries, in order of what the moment needs: the progress
	// bar while booting, then the operator's mark, then the version. Only one
	// of them, because stacking two on a 64-pixel panel leaves neither any
	// air and the result looks like a crash report.
	switch {
	case progress >= 0:
		drawProgress(fb, 20, 50, Width-40, 6, progress)
	case b.Name != "":
		drawBrand(fb, b)
	case version != "":
		fb.TextCentered(50, Truncate(version, Cols))
	}
}

// drawBrand renders the operator's mark under a hairline, so it reads as an
// attribution rather than as part of the product name.
func drawBrand(fb *Framebuffer, b Brand) {
	name := b.Name
	// Letterspace it only if it still fits afterwards; a name that has to be
	// truncated to be spaced is worse than one simply set plain.
	if spaced := Letterspace(name); len([]rune(spaced)) <= Cols {
		name = spaced
	}

	// Fixed positions, and NO second rule.
	//
	// Two earlier attempts put a hairline above the name. The first computed
	// it from a moving baseline and drew straight through "A . L . O . A .".
	// The second cleared the subtitle but sat four pixels under it, so it
	// read as an underline of the subtitle rather than a separator above the
	// mark -- and the wordmark already has a slab. A third horizontal line on
	// a 64-pixel panel is clutter whichever row it lands on, so the space
	// does the separating instead.
	//
	// The subtitle ends at y=41 and the panel at 64: the whole lower band is
	// 22 pixels, and every row in it is placed explicitly.
	if b.Tagline == "" {
		fb.TextCentered(51, Truncate(name, Cols))
		return
	}
	fb.TextCentered(48, Truncate(name, Cols))
	fb.TextCentered(56, Truncate(b.Tagline, Cols))
}

// drawProgress is a hairline track with a filled bar. One pixel of padding
// inside the border, so the fill never touches it and muddies the edge.
func drawProgress(fb *Framebuffer, x, y, w, h int, p float64) {
	if p < 0 {
		p = 0
	}
	if p > 1 {
		p = 1
	}
	fb.Rect(x, y, w, h, true)
	inner := w - 4
	filled := int(float64(inner)*p + 0.5)
	if filled > 0 {
		fb.FillRect(x+2, y+2, filled, h-4, true)
	}
}

// DrawBootStep is the splash with a status line instead of a progress bar, for
// the stages where there is something specific to say -- "waiting for service"
// reads better than a bar that has stalled.
func DrawBootStep(fb *Framebuffer, step string) {
	fb.Clear()
	fb.TextScaledCentered(10, 2, "P4wnP1")
	w := TextWidthScaled("P4wnP1", 2)
	x := (Width - w) / 2
	fb.FillRect(x, 27, w, 2, true)
	fb.TextCentered(34, "A . L . O . A .")
	fb.HLine(8, 46, Width-16, true)
	fb.TextCentered(52, Truncate(step, Cols))
}

// DrawFatal is what the operator sees when the daemon cannot continue. It says
// what failed and what to do, because a device with a blank screen and a
// cryptic journal entry is a device that gets thrown in a drawer.
func DrawFatal(fb *Framebuffer, headline, detail string) {
	fb.Clear()
	// Draw the heading on black and THEN invert the band. Filling the band
	// first and drawing on top of it meant white text on white, and the invert
	// turned the whole lot black again -- a header that rendered as nothing at
	// all. It took looking at the output to notice; the code read fine.
	fb.Text(2, 1, Truncate(headline, Cols-1))
	fb.Invert(0, 0, Width, LineH+2)

	y := LineH + 6
	for _, line := range wrap(detail, Cols) {
		if y+GlyphH > Height {
			break
		}
		fb.Text(0, y, line)
		y += LineH
	}
}

// wrap breaks s into lines of at most n characters, splitting on spaces where
// it can and mid-word only when a single word is longer than the line.
func wrap(s string, n int) []string {
	if n <= 0 {
		return nil
	}
	var out []string
	cur := ""
	flush := func() {
		if cur != "" {
			out = append(out, cur)
			cur = ""
		}
	}
	for _, word := range splitSpaces(s) {
		for len([]rune(word)) > n {
			flush()
			r := []rune(word)
			out = append(out, string(r[:n]))
			word = string(r[n:])
		}
		switch {
		case cur == "":
			cur = word
		case len([]rune(cur))+1+len([]rune(word)) <= n:
			cur += " " + word
		default:
			flush()
			cur = word
		}
	}
	flush()
	return out
}

func splitSpaces(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\n' {
			if cur != "" {
				out = append(out, cur)
				cur = ""
			}
			continue
		}
		cur += string(r)
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}

// VersionLine formats the build stamp for the splash.
func VersionLine(version string) string {
	if version == "" {
		return ""
	}
	return fmt.Sprintf("v%s", version)
}

// LoadBrand reads the operator's mark from a file: the first non-empty,
// non-comment line is the name, the second is an optional tagline.
//
// A file rather than only a flag, so an image can be branded by dropping one
// line into /etc/p4wnp1/brand.txt -- no rebuild, no editing a unit. A missing
// file is the normal case and is not an error.
func LoadBrand(path string) Brand {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Brand{}
	}
	var lines []string
	for _, l := range strings.Split(string(raw), "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		lines = append(lines, l)
	}
	var b Brand
	if len(lines) > 0 {
		b.Name = lines[0]
	}
	if len(lines) > 1 {
		b.Tagline = lines[1]
	}
	return b
}

// FirstBootFlag is the file first boot creates when it has finished. Its
// ABSENCE means setup is still running.
const FirstBootFlag = "/var/lib/p4wnp1/firstboot.done"

// DrawSetupWarning is the screen shown while first boot is still working.
//
// First boot on a Pi Zero W takes minutes: it resizes the root filesystem,
// generates three SSH host keys on a single 1GHz core, bootstraps the web
// admin and adopts any key left on the card. For all of that the device looks
// idle -- no network yet, nothing on the panel but a splash -- and the
// obvious thing for an operator to do with an appliance that appears to have
// hung is pull the plug.
//
// Pulling the plug in the middle of that is the one genuinely destructive
// thing available: a half-written auth.json, host keys that exist but were
// never installed, a resize interrupted partway. So the panel says so, in
// the largest type the screen has, for as long as it is true.
func DrawSetupWarning(fb *Framebuffer, detail string, progress float64) {
	fb.Clear()

	// Every 1x line sits on an exact LineH boundary. Not cosmetic: the
	// framebuffer reader that the tests use scans those rows, so text drawn
	// one pixel off is text no test can see. The first version put the
	// heading at y=1 and the detail at y=46, and both read back as noise --
	// the screen looked right and was unverifiable.
	fb.Text(2, 0, Truncate("SETTING UP", Cols-1))
	fb.Invert(0, 0, Width, LineH+1)

	// The one line that matters, at double height so it cannot be mistaken
	// for ordinary status text.
	fb.TextScaledCentered(14, 2, "DO NOT")
	fb.TextScaledCentered(30, 2, "POWER OFF")

	if detail != "" {
		fb.TextCentered(48, Truncate(detail, Cols))
	}
	drawProgress(fb, 14, 57, Width-28, 6, progress)
}

// DrawSetupDone is the handover: setup finished, the device is safe to unplug
// from here on. Shown briefly so an operator who looked away still learns
// that the dangerous window has closed.
func DrawSetupDone(fb *Framebuffer, detail string) {
	fb.Clear()
	fb.Text(2, 0, Truncate("SETUP COMPLETE", Cols-1))
	fb.Invert(0, 0, Width, LineH+1)
	fb.TextScaledCentered(16, 2, "READY")
	fb.HLine(12, 37, Width-24, true)
	if detail != "" {
		fb.TextCentered(40, Truncate(detail, Cols))
	}
	fb.TextCentered(56, "Safe to power off")
}

// FirstBootRunning reports whether first boot is still working, by the
// absence of its completion flag.
//
// Deliberately a FILE check rather than asking systemd. The daemon would
// otherwise need dbus access to query a unit, and the flag is the same thing
// the firstboot script itself gates on -- so the panel and the script cannot
// disagree about whether setup has finished.
func FirstBootRunning(flagPath string) bool {
	if flagPath == "" {
		return false
	}
	_, err := os.Stat(flagPath)
	return os.IsNotExist(err)
}
