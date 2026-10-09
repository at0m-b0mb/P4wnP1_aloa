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
// renderFlat is renderText with the layout taken out: all whitespace removed
// so a secret that WRAPS across lines still matches as one string.
//
// Needed since the first-run secret cards gained a QR code. The text column
// beside a 58-pixel symbol is eleven characters wide, so a 20-character
// password is shown on two lines instead of one. It is still entirely on the
// panel -- which is the property these tests care about -- but it is no
// longer a contiguous substring of the rendered screen.
func renderFlat(app *App) string {
	return strings.Join(strings.Fields(renderText(app)), "")
}

func renderText(app *App) string {
	fb := NewFramebuffer()
	app.Render(fb)
	return fbText(fb)
}

// fbText is ReadBack, kept under its old name so the walk tests read the same.
func fbText(fb *Framebuffer) string { return ReadBack(fb) }

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

// rowOf returns how many down-presses reach the named row of a menu, read
// from the menu itself.
//
// These paths used to be literal row numbers with a comment claiming that
// reordering a menu would break them by name. It would not, and it did not:
// adding "Buttons test" to System moved Reboot from row 4 to row 5, and the
// row-4 path walked onto "LED: off" instead. One test reported "reboot did
// NOT happen after confirming", which was true and useless. The dangerous
// case is the other one -- a shift that lands on Shut down and passes, while
// the test still says it is checking Reboot.
func rowOf(m *Menu, label string) int {
	for i, it := range m.items {
		if it.Label == label {
			return i
		}
	}
	panic("no menu row labelled " + label)
}

var (
	toStatus   = path(down(rowOf(NewRoot(), "Status")), []Button{BtnEnter})
	toLoadouts = path(down(rowOf(NewRoot(), "Loadouts")), []Button{BtnEnter})
	toCable    = path(down(rowOf(NewRoot(), "Cable (USB)")), []Button{BtnEnter})
	toPayloads = path(down(rowOf(NewRoot(), "Payloads")), []Button{BtnEnter})
	toJobs     = path(down(rowOf(NewRoot(), "Jobs")), []Button{BtnEnter})
	toSystem   = path(down(rowOf(NewRoot(), "System")), []Button{BtnEnter})

	sysReboot   = down(rowOf(NewSystemMenu(), "Reboot"))
	sysShutdown = down(rowOf(NewSystemMenu(), "Shut down"))
	sysButtons  = down(rowOf(NewSystemMenu(), "Buttons test"))
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
		// The hint now carries the endpoint budget too, because a composition
		// that cannot deploy is worth knowing about before you confirm it.
		{label: "08-cable-dirty", keys: path(toCable, []Button{BtnDown, BtnConfirm}),
			want: []string{"KEY1 deploy", "/7"}},
		{label: "09-payloads", keys: toPayloads,
			want: []string{"Payloads", "hello.js", "run in bg"}},
		{label: "10-payload-confirm", keys: path(toPayloads, []Button{BtnEnter}),
			want: []string{"Run", "Yes", "No", "attached host"}},
		{label: "11-payload-running", keys: path(toPayloads, []Button{BtnEnter, BtnRight, BtnConfirm}),
			want: []string{"Running", "job", "running", "KEY1 stop it"}},
		{label: "12-jobs", keys: toJobs,
			want: []string{"Jobs", "win_recon.js", "stop all"}},
		{label: "13-system", keys: toSystem,
			want: []string{"System", "Backups", "LED", "Reboot", "Shut down"}},
		{label: "14-reboot-confirm", keys: path(toSystem, sysReboot, []Button{BtnEnter}),
			want: []string{"Reboot", "Yes", "No"}},
		{label: "15-buttons-test", keys: path(toSystem, sysButtons, []Button{BtnEnter}),
			want: []string{"Buttons", "Up", "KEY1", "KEY2", "KEY3", "left exits"}},
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
