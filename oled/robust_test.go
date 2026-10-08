package oled

import (
	"math/rand"
	"strings"
	"testing"
)

// Nobody can test this against the panel yet, so the tests have to be the
// thing that says it works. These are the invariants that must hold from ANY
// state the operator can reach, rather than along the happy path a scripted
// walk takes.

var allButtons = []Button{
	BtnUp, BtnDown, BtnLeft, BtnRight, BtnPress,
	BtnKey1, BtnKey2, BtnKey3,
}

// litPixels counts set pixels in the whole frame.
func litPixels(fb *Framebuffer) int {
	return litIn(fb, 0, Height)
}

// bodyPixels counts only the six content rows.
//
// Counting the whole frame is useless as a blankness check and I proved it:
// the title bar is inverted on every screen, so litPixels is never zero even
// when the body draws nothing at all. Deleting a screen's entire error branch
// produced zero test failures. The body is the part that can actually be
// empty, so it is the part to measure.
func bodyPixels(fb *Framebuffer) int {
	return litIn(fb, bodyPxTop, hintRow*LineH-1)
}

func litIn(fb *Framebuffer, y0, y1 int) int {
	n := 0
	for y := y0; y < y1 && y < Height; y++ {
		for x := 0; x < Width; x++ {
			if fb.Get(x, y) {
				n++
			}
		}
	}
	return n
}

// TestRandomWalkNeverBreaks hammers the UI with random presses. A device you
// operate with your thumb, often without looking, will receive sequences no
// scripted test would think to try.
func TestRandomWalkNeverBreaks(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	app := NewApp(NewFakeClient(), NewRoot())
	fb := NewFramebuffer()

	for i := 0; i < 20000; i++ {
		b := allButtons[rng.Intn(len(allButtons))]
		app.Handle(b) // a panic here fails the test, which is the point
		app.Render(fb)

		if n := bodyPixels(fb); n == 0 {
			t.Fatalf("step %d (%s): the content area is blank\n%s", i, b, fbText(fb))
		}
		if d := app.Depth(); d < 1 {
			t.Fatalf("step %d (%s): the view stack emptied", i, b)
		}
		// The deepest legitimate path is root -> list -> item actions ->
		// confirmation, which is four. Six leaves headroom while still
		// catching a screen that pushes without popping; twelve, which this
		// was, catches nothing.
		if d := app.Depth(); d > 6 {
			t.Fatalf("step %d (%s): the view stack grew to %d, something pushes without popping", i, b, d)
		}
	}
}

// Whatever you have got yourself into, KEY3 must get you out. On a device
// with no labels and no manual to hand, one reliable escape is the difference
// between usable and not.
func TestHomeAlwaysEscapes(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	for trial := 0; trial < 200; trial++ {
		app := NewApp(NewFakeClient(), NewRoot())
		for i := 0; i < 40; i++ {
			app.Handle(allButtons[rng.Intn(len(allButtons))])
		}
		app.Handle(BtnHome)
		if d := app.Depth(); d != 1 {
			t.Fatalf("trial %d: KEY3 left the stack at depth %d", trial, d)
		}
	}
}

// Likewise back: enough presses of left must always reach the root. A screen
// that swallows back is a screen you can get stranded on.
func TestBackAlwaysReachesTheRoot(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	for trial := 0; trial < 200; trial++ {
		app := NewApp(NewFakeClient(), NewRoot())
		for i := 0; i < 40; i++ {
			app.Handle(allButtons[rng.Intn(len(allButtons))])
		}
		// Twice the maximum depth seen in the walk above, so a screen that
		// needs two presses (the confirm dialog does) still gets there.
		for i := 0; i < 40; i++ {
			app.Handle(BtnBack)
		}
		if d := app.Depth(); d != 1 {
			t.Fatalf("trial %d: 40 back presses left the stack at depth %d", trial, d)
		}
	}
}

// Every screen must render something readable even when the device refuses
// every call. These are the paths that only run on the day something is
// broken, which is the worst day to discover they render a blank panel.
func TestEveryScreenSurvivesTheDeviceFailing(t *testing.T) {
	ops := []string{
		"Status", "List", "Deploy", "Delete", "RunHID", "RunningJobs",
		"CancelAllJobs", "USBFunctions", "SetUSB", "SetStartup", "SetLED",
		"Reboot", "Shutdown",
	}
	for _, op := range ops {
		for _, w := range script() {
			c := NewFakeClient()
			c.Fail[op] = "the " + op + " subsystem is unavailable on this device"
			app := NewApp(c, NewRoot())

			for _, k := range w.keys {
				app.Handle(k)
			}

			check := func(stage string) {
				fb := NewFramebuffer()
				app.Render(fb)
				if bodyPixels(fb) == 0 {
					t.Errorf("fail=%s %s (%s): the content area is blank -- the\n"+
						"operator sees a title and nothing else when this fails\n%s",
						op, w.label, stage, fbText(fb))
				}
				if app.Depth() < 1 {
					t.Errorf("fail=%s %s (%s): empty view stack", op, w.label, stage)
				}
			}

			// Check on arrival, and after EACH further press.
			//
			// An earlier version pressed all eight buttons and then looked
			// once. Back and home are among those eight, so it had navigated
			// off the broken screen before checking, and deleting a screen's
			// whole error branch produced no failures at all.
			check("on arrival")
			for _, b := range allButtons {
				app.Handle(b)
				check("after " + b.String())
			}
		}
	}
}

// A brand-new device has nothing stored. Every list must say so rather than
// drawing an empty box that looks like a failed load.
func TestEmptyDeviceSaysSoOnEveryList(t *testing.T) {
	c := NewFakeClient()
	for k := range c.Stored {
		c.Stored[k] = nil
	}
	c.Jobs = nil

	for _, w := range script() {
		app := NewApp(c, NewRoot())
		for _, k := range w.keys {
			app.Handle(k)
		}
		fb := NewFramebuffer()
		app.Render(fb)
		if bodyPixels(fb) == 0 {
			t.Errorf("%s: the content area is blank on an empty device\n%s", w.label, fbText(fb))
		}
	}

	// The specific message, on a list that is reachable in one step.
	app := NewApp(c, NewRoot())
	app.Handle(BtnDown)
	app.Handle(BtnEnter)
	got := renderText(app)
	if !strings.Contains(got, "nothing stored") {
		t.Errorf("an empty list does not say so:\n%s", got)
	}
}

// Names come from the device, not from us. A long one must be truncated with
// a visible marker rather than wrapping, overflowing or panicking.
func TestHostileNamesDoNotBreakTheScreen(t *testing.T) {
	c := NewFakeClient()
	c.Stored[KindMasterTemplate] = []string{
		strings.Repeat("A", 500),
		"name with spaces and (parens) [brackets] {braces}",
		"tab\there",
		"newline\nhere",
		"\x00\x01\x02control",
		"émoji→✓ and ünïcødé",
		"",
		strings.Repeat("x", Cols),
		strings.Repeat("y", Cols+1),
	}

	app := NewApp(c, NewRoot())
	app.Handle(BtnDown)
	app.Handle(BtnEnter)

	for i := 0; i < len(c.Stored[KindMasterTemplate])+2; i++ {
		fb := NewFramebuffer()
		app.Render(fb)
		if bodyPixels(fb) == 0 {
			t.Fatalf("row %d: the content area is blank", i)
		}
		// Nothing may be drawn outside the panel; Set clips, so this is
		// really a check that the renderer does not hang or mis-measure.
		for _, line := range strings.Split(fbText(fb), "\n") {
			if len([]rune(line)) > Cols {
				t.Errorf("row %d: a line is %d chars wide, panel is %d:\n%q",
					i, len([]rune(line)), Cols, line)
			}
		}
		app.Handle(BtnDown)
	}

	// And entering one must work.
	app.Handle(BtnEnter)
	fb := NewFramebuffer()
	app.Render(fb)
	if bodyPixels(fb) == 0 {
		t.Error("entering a hostile name blanked the content area")
	}
}

// A long list must scroll to its last entry and no further, and the selection
// must stay inside the window. Off-by-one here shows up as an item you can
// see but never select.
func TestLongListScrollsToBothEnds(t *testing.T) {
	c := NewFakeClient()
	var many []string
	for i := 0; i < 97; i++ {
		many = append(many, strings.Repeat("i", 1+i%4))
	}
	c.Stored[KindMasterTemplate] = many

	app := NewApp(c, NewRoot())
	app.Handle(BtnDown)
	app.Handle(BtnEnter)

	list, ok := app.Top().(*StoredList)
	if !ok {
		t.Fatalf("expected a StoredList, got %T", app.Top())
	}
	if list.cur.n != 97 {
		t.Fatalf("list length = %d, want 97", list.cur.n)
	}

	for i := 0; i < 200; i++ {
		app.Handle(BtnDown)
		if list.cur.sel < 0 || list.cur.sel >= list.cur.n {
			t.Fatalf("selection %d out of range after %d downs", list.cur.sel, i)
		}
		if list.cur.first < 0 || (list.cur.n > bodyRows && list.cur.first > list.cur.n-bodyRows) {
			t.Fatalf("window start %d out of range after %d downs", list.cur.first, i)
		}
		if list.cur.sel < list.cur.first || list.cur.sel >= list.cur.first+bodyRows {
			t.Fatalf("selection %d is outside the visible window [%d,%d)",
				list.cur.sel, list.cur.first, list.cur.first+bodyRows)
		}
	}
	for i := 0; i < 200; i++ {
		app.Handle(BtnUp)
		if list.cur.sel < list.cur.first || list.cur.sel >= list.cur.first+bodyRows {
			t.Fatalf("selection %d outside the window after %d ups", list.cur.sel, i)
		}
	}
}

// The USB screen batches toggles and deploys once. Deploying per toggle would
// re-enumerate the device six times to turn on six functions, which is both
// slow and conspicuous on the host.
func TestUSBTogglesDeployOnceNotPerToggle(t *testing.T) {
	c := NewFakeClient()
	app := NewApp(c, NewRoot())
	for _, k := range toCable {
		app.Handle(k)
	}
	for i := 0; i < 4; i++ {
		app.Handle(BtnConfirm) // toggle
		app.Handle(BtnDown)
	}
	deploys := 0
	for _, call := range c.Calls {
		if strings.HasPrefix(call, "SetUSB") {
			deploys++
		}
	}
	if deploys != 0 {
		t.Fatalf("%d deploys happened while only toggling", deploys)
	}

	app.Handle(BtnAction)  // deploy
	app.Handle(BtnRight)   // select Yes
	app.Handle(BtnConfirm) // confirm

	deploys = 0
	for _, call := range c.Calls {
		if strings.HasPrefix(call, "SetUSB") {
			deploys++
		}
	}
	if deploys != 1 {
		t.Fatalf("expected exactly 1 deploy after confirming, got %d (%v)", deploys, c.Calls)
	}
}

// Anything that reboots, shuts down, deletes or deploys must be behind a
// confirmation, and cancelling must do nothing at all.
func TestDestructiveActionsNeedConfirmationAndCancelDoesNothing(t *testing.T) {
	cases := []struct {
		name string
		keys []Button
		did  func(*FakeClient) bool
	}{
		{"reboot", path(toSystem, sysReboot, []Button{BtnEnter}),
			func(c *FakeClient) bool { return c.Rebooted }},
		{"shutdown", path(toSystem, sysShutdown, []Button{BtnEnter}),
			func(c *FakeClient) bool { return c.ShutDown }},
		{"deploy", path(toLoadouts, []Button{BtnEnter, BtnEnter}),
			func(c *FakeClient) bool { return c.Called("Deploy(") }},
		{"delete", path(toLoadouts, []Button{BtnAction}),
			func(c *FakeClient) bool { return c.Called("Delete(") }},
	}

	for _, tc := range cases {
		// Reaching the dialog must not act.
		c := NewFakeClient()
		app := NewApp(c, NewRoot())
		for _, k := range tc.keys {
			app.Handle(k)
		}
		if tc.did(c) {
			t.Errorf("%s: happened on the way to the confirmation", tc.name)
		}

		// Cancelling must not act. "No" is selected by default, so a bare
		// press is the cancel path.
		app.Handle(BtnConfirm)
		if tc.did(c) {
			t.Errorf("%s: happened after cancelling", tc.name)
		}

		// Confirming must act.
		c2 := NewFakeClient()
		app2 := NewApp(c2, NewRoot())
		for _, k := range tc.keys {
			app2.Handle(k)
		}
		app2.Handle(BtnRight)
		app2.Handle(BtnConfirm)
		if !tc.did(c2) {
			t.Errorf("%s: did NOT happen after confirming (%v)", tc.name, c2.Calls)
		}
	}
}

// Entering a screen and backing out must return the stack to where it was.
// The depth cap in the random walk cannot catch a slow leak: the legitimate
// maximum is only four deep, so a stack that never pops still looks fine for
// a long time. This cycles the same screen and checks the stack comes back.
func TestEnteringAndLeavingDoesNotLeakStack(t *testing.T) {
	app := NewApp(NewFakeClient(), NewRoot())
	for i := 0; i < 100; i++ {
		app.Handle(BtnEnter) // into Status
		if d := app.Depth(); d != 2 {
			t.Fatalf("cycle %d: depth after entering = %d, want 2", i, d)
		}
		app.Handle(BtnBack)
		if d := app.Depth(); d != 1 {
			t.Fatalf("cycle %d: depth after leaving = %d, want 1 -- the stack is leaking", i, d)
		}
	}

	// And a deeper path, through a confirmation.
	//
	// A FRESH app per cycle, because the root menu keeps its cursor across
	// KEY3 -- you come home and find your place, which is the right
	// behaviour and means a key path is only absolute from a fresh start.
	// Reusing one app here walked somewhere else on the second lap and the
	// test caught it, correctly, as the wrong screen.
	for i := 0; i < 100; i++ {
		a := NewApp(NewFakeClient(), NewRoot())
		for _, k := range path(toLoadouts, []Button{BtnEnter, BtnEnter}) {
			a.Handle(k)
		}
		if d := a.Depth(); d != 4 {
			t.Fatalf("cycle %d: depth at the confirmation = %d, want 4", i, d)
		}
		a.Handle(BtnHome)
		if d := a.Depth(); d != 1 {
			t.Fatalf("cycle %d: KEY3 left depth at %d", i, d)
		}
	}
}

// Coming home keeps your place in the root menu. Stated as a test because it
// is a deliberate choice rather than an accident, and because a later change
// that resets it would otherwise look harmless.
func TestHomeKeepsYourPlaceInTheRootMenu(t *testing.T) {
	app := NewApp(NewFakeClient(), NewRoot())
	app.Handle(BtnDown)
	app.Handle(BtnDown) // Cable
	app.Handle(BtnEnter)
	app.Handle(BtnHome)
	if got := renderText(app); !strings.Contains(got, "Cable") {
		t.Errorf("after KEY3 the root menu lost its place:\n%s", got)
	}
	// And entering again goes back to the same screen.
	app.Handle(BtnEnter)
	if got := renderText(app); !strings.Contains(got, "Keyboard") {
		t.Errorf("re-entering did not return to Cable:\n%s", got)
	}
}

// The default on every confirmation is No, so a stray press cannot reboot the
// device or wipe a template.
func TestConfirmationsDefaultToNo(t *testing.T) {
	c := NewConfirm("T", "Do it?", "", nil)
	if c.yes {
		t.Error("a confirmation opened with Yes selected")
	}
}

// Toggling the panel display on and off is not a thing the UI does, but the
// recording display proves the daemon's paint loop pushes frames that differ.
func TestRenderIsDeterministic(t *testing.T) {
	a := NewApp(NewFakeClient(), NewRoot())
	b := NewApp(NewFakeClient(), NewRoot())
	for _, k := range path(toCable, []Button{BtnDown, BtnConfirm}) {
		a.Handle(k)
		b.Handle(k)
	}
	fa, fb2 := NewFramebuffer(), NewFramebuffer()
	a.Render(fa)
	b.Render(fb2)
	if fa.String() != fb2.String() {
		t.Error("the same key sequence produced two different screens")
	}
}
