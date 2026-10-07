package oled

import (
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// walk is a scripted trip through the UI: a label, the keys to press first,
// and what the resulting screen must contain.
type walk struct {
	label string
	keys  []Button
	// want are substrings that must appear in the rendered text. Asserting on
	// rendered OUTPUT rather than on internal state is the point: a screen can
	// hold the right data and still draw nothing, which is exactly the class
	// of bug that cost two rounds on the splash.
	want []string
	// notWant catches things that must not be on screen, such as a secret.
	notWant []string
}

// renderText returns what the screen says, as text, by reading the
// framebuffer back through the font. Cheaper and far more legible in a
// failure message than comparing pixels.
func renderText(app *App) string {
	fb := NewFramebuffer()
	app.Render(fb)
	return fbText(fb)
}

// fbText reads the screen back as text by matching 5x7 cells against the font.
// Asserting on words beats asserting on bitmaps: a failure says what the panel
// says.
//
// It SCANS rather than assuming a grid. The first version stepped x in CharW
// from zero, which silently missed every string the UI positions deliberately
// -- the title bar is inset one pixel, the confirm buttons sit at x=76 -- and
// reported them as unreadable when they rendered perfectly. A reader that only
// sees text which happens to be grid-aligned is a reader that lies.
func fbText(fb *Framebuffer) string {
	var out strings.Builder
	for row := 0; row < Rows; row++ {
		// Every string the UI draws sits on an exact row boundary, so there
		// is nothing to search for vertically. An earlier version tried row
		// +/-1 and picked whichever matched most glyphs, which ranked the
		// boundary line of an inverted bar -- fifteen cells that each look
		// like an underscore -- above the six real characters next to it.
		line, _ := scanRow(fb, row*LineH)
		out.WriteString(strings.TrimRight(line, " "))
		out.WriteByte('\n')
	}
	return out.String()
}

// scanRow walks x across one pixel row, emitting a character whenever the cell
// at x matches a glyph. Scanning rather than stepping in CharW from zero,
// because the UI positions some text deliberately off the grid -- the title
// is inset one pixel and the confirm buttons sit at x=76.
func scanRow(fb *Framebuffer, y int) (string, int) {
	var b strings.Builder
	score := 0
	for x := 0; x+GlyphW <= Width; {
		r, ok := cellRune(fb, x, y)
		if !ok {
			b.WriteByte(' ')
			x++
			continue
		}
		b.WriteRune(r)
		if r != ' ' {
			score++
		}
		x += CharW
	}
	return b.String(), score
}

// cellRune matches one 5x7 cell, trying the cell and its complement so text on
// an inverted bar reads the same as text on black.
func cellRune(fb *Framebuffer, x, y int) (rune, bool) {
	var cell, inv [5]byte
	for c := 0; c < GlyphW; c++ {
		var bb, ib byte
		for r := 0; r < GlyphH; r++ {
			if fb.Get(x+c, y+r) {
				bb |= 1 << uint(r)
			} else {
				ib |= 1 << uint(r)
			}
		}
		cell[c], inv[c] = bb, ib
	}
	for i, g := range font5x7 {
		if g == cell || g == inv {
			return rune(0x20 + i), true
		}
	}
	return 0, false
}

// down returns n presses of the down key, so a path reads as "the third item".
func down(n int) []Button {
	out := make([]Button, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, BtnDown)
	}
	return out
}

func path(parts ...[]Button) []Button {
	var out []Button
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// Root menu order, named so a reordering of the menu breaks these by name
// rather than by silently walking somewhere else.
var (
	toStatus   = path(down(0), []Button{BtnEnter})
	toLoadouts = path(down(1), []Button{BtnEnter})
	toCable    = path(down(2), []Button{BtnEnter})
	toPayloads = path(down(3), []Button{BtnEnter})
	toJobs     = path(down(7), []Button{BtnEnter})
	toSystem   = path(down(8), []Button{BtnEnter})
)

// Each walk starts from a FRESH app and spells out the whole path from the
// root. The first version chained them, so inserting one screenshot shifted
// every later path and the test started asserting about the wrong screens --
// which it reported as missing text rather than as a broken script.
func script() []walk {
	return []walk{
		{label: "01-root",
			want: []string{"P4wnP1", "Status", "Loadouts", "Cable", "Payloads"}},
		{label: "02-status", keys: toStatus,
			want: []string{"Status", "USB", "usbeth", "172.16.0.1", "WiFi"}},
		{label: "03-status-scrolled", keys: path(toStatus, down(3)),
			want: []string{"Reflex", "armed", "Jobs"}},
		{label: "04-loadouts", keys: toLoadouts,
			want: []string{"Loadouts", "default", "keyboard_attack"}},
		{label: "05-loadout-actions", keys: path(toLoadouts, []Button{BtnEnter}),
			want: []string{"Deploy now", "Use at boot", "Delete"}},
		{label: "06-deploy-confirm", keys: path(toLoadouts, []Button{BtnEnter, BtnEnter}),
			want: []string{"Deploy", "No", "Yes", "re-enumerate"}},
		{label: "07-cable", keys: toCable,
			want: []string{"Cable", "Keyboard", "RNDIS", "[x]", "[ ]"}},
		{label: "08-cable-dirty", keys: path(toCable, []Button{BtnDown, BtnConfirm}),
			want: []string{"deploy changes"}},
		{label: "09-payloads", keys: toPayloads,
			want: []string{"Payloads", "hello.js", "run in bg"}},
		{label: "10-payload-confirm", keys: path(toPayloads, []Button{BtnEnter}),
			want: []string{"Run", "Yes", "No", "attached host"}},
		{label: "11-payload-result", keys: path(toPayloads, []Button{BtnEnter, BtnRight, BtnConfirm}),
			want: []string{"Result", "typed"}},
		{label: "12-jobs", keys: toJobs,
			want: []string{"Jobs", "win_recon.js", "stop all"}},
		{label: "13-system", keys: toSystem,
			want: []string{"System", "Backups", "LED", "Reboot", "Shut down"}},
		{label: "14-reboot-confirm", keys: path(toSystem, down(4), []Button{BtnEnter}),
			want: []string{"Reboot", "Yes", "No"}},
	}
}

// TestEveryScreenRendersWhatItShould walks the UI and checks the text that
// actually reaches the panel.
func TestEveryScreenRendersWhatItShould(t *testing.T) {
	for _, w := range script() {
		app := NewApp(NewFakeClient(), NewRoot())
		for _, k := range w.keys {
			app.Handle(k)
		}
		got := renderText(app)
		for _, want := range w.want {
			if !strings.Contains(got, want) {
				t.Errorf("%s: screen does not show %q\n%s", w.label, want, got)
			}
		}
		for _, no := range w.notWant {
			if strings.Contains(got, no) {
				t.Errorf("%s: screen shows %q and must not\n%s", w.label, no, got)
			}
		}
	}
}

// TestContactSheet writes every screen to one PNG. Not an assertion: a way to
// look at a display I have never had in front of me. Set OLED_SHEET to a path
// to produce it.
func TestContactSheet(t *testing.T) {
	out := os.Getenv("OLED_SHEET")
	if out == "" {
		t.Skip("set OLED_SHEET=<path.png> to write the contact sheet")
	}

	type shot struct {
		label string
		fb    *Framebuffer
	}
	var shots []shot

	// The boot screens first, since they are what you see before the UI runs.
	for _, s := range []struct {
		label string
		draw  func(*Framebuffer)
	}{
		{"00-splash", func(f *Framebuffer) { DrawSplash(f, VersionLine("0.3.1"), 0.65) }},
		{"00-boot", func(f *Framebuffer) { DrawBootStep(f, "waiting for service") }},
	} {
		fb := NewFramebuffer()
		s.draw(fb)
		shots = append(shots, shot{s.label, fb})
	}

	for _, w := range script() {
		app := NewApp(NewFakeClient(), NewRoot())
		for _, k := range w.keys {
			app.Handle(k)
		}
		fb := NewFramebuffer()
		app.Render(fb)
		shots = append(shots, shot{w.label, fb})
	}

	// An error screen too: the failure paths are part of the design, not an
	// afterthought, and they are the ones nobody looks at until they fire.
	bad := NewFakeClient()
	bad.Fail["List"] = "the WiFi subsystem is unavailable on this device"
	errApp := NewApp(bad, NewRoot())
	errApp.Handle(BtnDown)
	errApp.Handle(BtnEnter)
	fb := NewFramebuffer()
	errApp.Render(fb)
	shots = append(shots, shot{"13-error", fb})

	const sc, gap, cols = 3, 12, 3
	rows := (len(shots) + cols - 1) / cols
	cellW, cellH := Width*sc+gap, Height*sc+gap
	sheet := image.NewRGBA(image.Rect(0, 0, cellW*cols+gap, cellH*rows+gap))
	draw.Draw(sheet, sheet.Bounds(), &image.Uniform{color.RGBA{24, 24, 28, 255}}, image.Point{}, draw.Src)
	for i, s := range shots {
		x0 := gap + (i%cols)*cellW
		y0 := gap + (i/cols)*cellH
		draw.Draw(sheet, image.Rect(x0, y0, x0+Width*sc, y0+Height*sc), s.fb.Image(sc), image.Point{}, draw.Src)
	}
	if err := os.MkdirAll(filepath.Dir(out), 0755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, sheet); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %d screens to %s", len(shots), out)
}
