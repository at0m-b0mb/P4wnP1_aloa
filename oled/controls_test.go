package oled

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every key must visibly answer on every screen.
//
// This is the test that was missing when the board shipped. The device was
// reported from hardware as "the joystick is working but I think the keys
// doesn't work", and the keys were in fact read, debounced and delivered
// perfectly -- they were simply unbound on the screen being used, so they
// changed nothing on the panel. Nothing in the suite noticed, because every
// screen's own test only asserted what its own bindings did.
//
// The property is not "KEY1 does something useful everywhere". It is weaker
// and absolute: after any of the three keys, the panel must not be byte for
// byte what it was before. An operator holding this board has no other
// channel. A control that leaves the screen unchanged is a control that is
// indistinguishable from a disconnected one.
func TestEveryKeyAnswersOnEveryScreen(t *testing.T) {
	keys := []Button{BtnAction, BtnRefresh, BtnHome}
	for _, w := range script() {
		for _, k := range keys {
			app := NewApp(NewFakeClient(), NewRoot())
			for _, b := range w.keys {
				app.Handle(b)
			}
			before := renderText(app)
			app.Handle(k)
			after := renderText(app)
			if before == after {
				t.Errorf("%s: %s changed nothing on screen\n%s", w.label, k, before)
			}
		}
	}
}

// The three keys must do what the About screen says they do, not merely
// something. Checked here so the fallback cannot quietly become a toast that
// replaces real behaviour.
func TestKeyFallbackMeanings(t *testing.T) {
	// KEY3 from a nested screen returns to the root.
	app := NewApp(NewFakeClient(), NewRoot())
	for _, b := range path(toSystem, sysButtons, []Button{BtnEnter}) {
		app.Handle(b)
	}
	if app.Depth() < 2 {
		t.Fatalf("setup: expected to be nested, depth %d", app.Depth())
	}
	app.Handle(BtnHome)
	if app.Depth() != 1 {
		t.Errorf("KEY3 left the stack at depth %d", app.Depth())
	}

	// KEY3 at the root says so rather than appearing dead.
	app.Handle(BtnHome)
	if s := renderText(app); !strings.Contains(s, "already home") {
		t.Errorf("KEY3 at the root said nothing:\n%s", s)
	}

	// KEY1 on a screen with no action says so.
	app2 := NewApp(NewFakeClient(), NewRoot())
	app2.Handle(BtnAction)
	if s := renderText(app2); !strings.Contains(s, "KEY1") {
		t.Errorf("KEY1 on the root menu said nothing:\n%s", s)
	}

	// KEY2 re-reads the device. The root menu holds no device data, so use a
	// screen that does and count the calls.
	c := NewFakeClient()
	app3 := NewApp(c, NewRoot())
	for _, b := range toLoadouts {
		app3.Handle(b)
	}
	n := c.CountCalls("List(Loadouts)")
	app3.Handle(BtnRefresh)
	if got := c.CountCalls("List(Loadouts)"); got != n+1 {
		t.Errorf("KEY2 made %d list calls, want exactly one more than %d", got, n)
	}
}

// Deploying must leave the operator looking at the device's own state.
//
// Deploying the stored WiFi config and being told "deployed" was reported from
// hardware as "it said successful but I don't know what happened", which is
// the correct reaction: a word on the hint line for three seconds is not
// evidence that a radio came up.
func TestDeployLandsOnStatus(t *testing.T) {
	c := NewFakeClient()
	app := NewApp(c, NewRoot())
	for _, b := range path(down(rowOf(NewRoot(), "Radio (WiFi/BT)")), []Button{BtnEnter, BtnEnter}) {
		app.Handle(b)
	}
	// "Deploy now", then confirm with Yes.
	app.Handle(BtnEnter)
	app.Handle(BtnEnter)
	app.Handle(BtnRight)
	app.Handle(BtnConfirm)

	if !c.Called("Deploy(") {
		t.Fatalf("nothing was deployed: %v", c.Calls)
	}
	if _, ok := app.Top().(*StatusView); !ok {
		t.Fatalf("after deploying, the top screen is %T, want the status readback", app.Top())
	}
	if s := renderText(app); !strings.Contains(s, "WiFi") {
		t.Errorf("the status readback does not mention the radio:\n%s", s)
	}
}

// --- the button test --------------------------------------------------------

// fakeInput reports pin levels that the test controls.
type fakeInput struct {
	ChanInput
	held map[Button]bool
}

func (f *fakeInput) Levels() map[Button]bool { return f.held }
func (f *fakeInput) PinName(b Button) string { return DefaultPins[b] }

// The button test is the only answer this device can give to "is this key
// wired up?", so it has to show the live pin, the press count and the pin
// name -- each one rules out a different cause.
func TestButtonTestShowsLevelsCountsAndPins(t *testing.T) {
	in := &fakeInput{held: map[Button]bool{BtnKey1: true}}
	app := NewApp(NewFakeClient(), NewRoot())
	app.Input = in
	for _, b := range path(toSystem, sysButtons, []Button{BtnEnter}) {
		app.Handle(b)
	}

	s := renderText(app)
	for _, want := range []string{"Up", "KEY1", "KEY2", "KEY3", "Push"} {
		if !strings.Contains(s, want) {
			t.Errorf("the button test does not list %q:\n%s", want, s)
		}
	}
	// KEY1 is held, nothing else is.
	if !strings.Contains(s, "KEY1 #") {
		t.Errorf("a held control is not marked:\n%s", s)
	}
	if !strings.Contains(s, "KEY2 .") {
		t.Errorf("a released control is not marked:\n%s", s)
	}

	// Pressing a key names its pin, so the wiring can be checked against the
	// board rather than against a rebuild.
	app.Handle(BtnKey2)
	if s := renderText(app); !strings.Contains(s, DefaultPins[BtnKey2]) {
		t.Errorf("pressing KEY2 did not name %s:\n%s", DefaultPins[BtnKey2], s)
	}

	// The count comes from presses the UI actually received, which is what
	// separates "the pin is dead" from "the debouncer ate it".
	before := app.Seen(BtnKey2)
	app.Handle(BtnKey2)
	if app.Seen(BtnKey2) != before+1 {
		t.Errorf("press count did not advance")
	}

	// Left must get you out, even though left is itself under test.
	app.Handle(BtnBack)
	if _, ok := app.Top().(*ButtonTest); ok {
		t.Errorf("left did not leave the button test")
	}
}

// With no raw access -- the simulator, or a build with no GPIO -- the screen
// must say so rather than showing every control as released, which would read
// as eight dead pins.
func TestButtonTestIsHonestWithoutPinAccess(t *testing.T) {
	app := NewApp(NewFakeClient(), NewRoot())
	for _, b := range path(toSystem, sysButtons, []Button{BtnEnter}) {
		app.Handle(b)
	}
	s := renderText(app)
	if !strings.Contains(s, "no raw pin") {
		t.Errorf("the button test claims to know the pin state it cannot read:\n%s", s)
	}
	if strings.Contains(s, "KEY1 .") {
		t.Errorf("an unknown pin is shown as released:\n%s", s)
	}
}

// Running a payload must never hold a request open on it.
//
// The service treats a cancelled request as job.Cancel() and interrupts the
// Otto VM, so a client-side HTTP deadline does not time out the reply -- it
// aborts the payload, part-way through typing into someone's machine. The
// panel had a six second timeout and asked for an unbounded run, which meant
// every foreground payload longer than six seconds was killed mid-keystroke
// and the panel reported "service unreachable", blaming the network for its
// own deadline. Four of the seven payloads this device ships are while(true)
// loops, so for those a foreground run could never once have succeeded.
func TestRunningAPayloadNeverWaitsOnIt(t *testing.T) {
	c := NewFakeClient()
	app := NewApp(c, NewRoot())
	for _, b := range path(toPayloads, []Button{BtnEnter, BtnRight, BtnConfirm}) {
		app.Handle(b)
	}

	v, ok := app.Top().(*RunningView)
	if !ok {
		t.Fatalf("after starting a payload the top screen is %T, want the watcher", app.Top())
	}
	if c.Called("RunHID(") && !c.Called("StartHID(") {
		t.Error("the panel used a call that waits on the job")
	}
	if !c.Called("StartHID(") {
		t.Fatalf("no payload was started: %v", c.Calls)
	}
	// While it is running, the result must NOT be asked for: fetching it
	// waits on the job, which is the same hazard by another name.
	if c.Called("CollectResult(") {
		t.Errorf("the result was collected while the job was still running: %v", c.Calls)
	}

	s := renderText(app)
	for _, want := range []string{"Running", "running", "KEY1 stop it"} {
		if !strings.Contains(s, want) {
			t.Errorf("the watcher does not show %q:\n%s", want, s)
		}
	}

	// Now let the job finish. The watcher polls, sees it gone, and only
	// then collects.
	c.Jobs = nil
	v.Refresh(app)
	if !c.Called("CollectResult(") {
		t.Errorf("the result was never collected once the job finished: %v", c.Calls)
	}
	s = renderText(app)
	if !strings.Contains(s, "finished") {
		t.Errorf("the watcher does not report the job finished:\n%s", s)
	}

	// And KEY1 must stop a running payload -- the one control you urgently
	// want when a script is mid-type into someone else's machine.
	c2 := NewFakeClient()
	app2 := NewApp(c2, NewRoot())
	for _, b := range path(toPayloads, []Button{BtnEnter, BtnRight, BtnConfirm}) {
		app2.Handle(b)
	}
	app2.Handle(BtnAction)
	if !c2.Called("CancelAllJobs") {
		t.Errorf("KEY1 did not stop the running payload: %v", c2.Calls)
	}
}

// The five-second poll must not throw away what the operator typed.
//
// USBView is a Refresher, so the daemon's tick called Refresh on it, which
// re-read the device and set dirty=false. Tick three boxes, pause to think,
// and five seconds later they are all clear again -- and the hint line has
// reverted from "KEY1 deploy changes" to "press toggles", removing the one
// clue that anything was pending.
func TestThePollDoesNotDiscardUnsavedUSBChanges(t *testing.T) {
	c := NewFakeClient()
	app := NewApp(c, NewRoot())
	for _, b := range toCable {
		app.Handle(b)
	}
	v, ok := app.Top().(*USBView)
	if !ok {
		t.Fatalf("top screen is %T, want the USB view", app.Top())
	}

	before := renderText(app)
	app.Handle(BtnConfirm) // toggle the selected function
	after := renderText(app)
	if before == after {
		t.Fatal("setup: the toggle did not change the screen")
	}
	if !v.dirty {
		t.Fatal("setup: the view does not consider itself dirty")
	}

	// What the daemon's ticker does, three times over.
	for i := 0; i < 3; i++ {
		app.Refresh()
	}
	if !v.dirty {
		t.Error("a background poll cleared the pending-changes flag")
	}
	if got := renderText(app); got != after {
		t.Errorf("a background poll changed the screen under the operator:\nwas:\n%s\nnow:\n%s", after, got)
	}
	if !strings.Contains(renderText(app), "KEY1") {
		t.Errorf("the screen stopped offering to deploy the pending changes:\n%s", renderText(app))
	}

	// Deploying must still send exactly what is on screen.
	app.Handle(BtnAction)
	app.Handle(BtnRight)
	app.Handle(BtnConfirm)
	if !c.Called("SetUSB(") {
		t.Errorf("the pending changes were never deployed: %v", c.Calls)
	}
}

// Zero and "could not ask" are different answers. A dashboard that renders
// the second as the first is confidently wrong about whether a payload is
// typing into someone's machine right now.
func TestStatusDoesNotInventZeroes(t *testing.T) {
	// Rendered directly, not pushed: StatusView is a Refresher, so pushing
	// it through App would immediately overwrite this fixture with whatever
	// the fake device says -- and the test would silently check the wrong
	// data while still passing for the wrong reason.
	st := Status{USBHost: "composed", WiFi: "idle"} // both OK flags false
	s := &StatusView{st: st, loaded: true}
	s.cur.setLen(len(s.lines()))
	fb := NewFramebuffer()
	s.Render(fb, nil)
	out := ReadBack(fb)
	for _, bad := range []string{"Jobs 0 running", "Reflex 0/0"} {
		if strings.Contains(out, bad) {
			t.Errorf("the screen states %q for a number it never read:\n%s", bad, out)
		}
	}
	if !strings.Contains(out, "unreadable") {
		t.Errorf("the screen does not say the numbers are unreadable:\n%s", out)
	}
}

// waitForService exists to hold the splash until the API answers. It could
// not work, because Status() returned a nil error no matter what happened.
func TestStatusFailsWhenNothingAnswers(t *testing.T) {
	dir := t.TempDir()
	tok := filepath.Join(dir, "t")
	if err := os.WriteFile(tok, []byte("tok"), 0600); err != nil {
		t.Fatal(err)
	}
	// A server that refuses everything, i.e. a service that is not up.
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"nope"}`, http.StatusServiceUnavailable)
	}))
	defer dead.Close()

	c := NewAPIClient(dead.URL, tok)
	if _, err := c.Status(); err == nil {
		t.Fatal("Status() reported success against a service that answered nothing")
	}

	// And when something DOES answer, it must not fail.
	alive := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	}))
	defer alive.Close()
	if _, err := NewAPIClient(alive.URL, tok).Status(); err != nil {
		t.Errorf("Status() failed against a service that answered: %v", err)
	}
}

// The poll must not scroll the screen out from under the operator. The rows
// at the bottom of a long status -- the WiFi state and the job count -- were
// unreadable, because every five seconds the window jumped back up.
func TestStatusKeepsItsScrollPositionAcrossAPoll(t *testing.T) {
	c := NewFakeClient()
	app := NewApp(c, NewRoot())
	for _, b := range toStatus {
		app.Handle(b)
	}
	for i := 0; i < 3; i++ {
		app.Handle(BtnDown)
	}
	v := app.Top().(*StatusView)
	scrolled := v.cur.first
	if scrolled == 0 {
		t.Fatal("setup: the screen did not scroll")
	}
	before := renderText(app)
	app.Refresh()
	if v.cur.first != scrolled {
		t.Errorf("the poll moved the window from row %d to %d", scrolled, v.cur.first)
	}
	if got := renderText(app); got != before {
		t.Errorf("the poll changed what was on screen:\nwas:\n%s\nnow:\n%s", before, got)
	}
}

// HID is opt-in by design, so the screen has to say so where you meet it.
//
// The shipped loadout composes ethernet only: the device does not offer a
// keyboard to a host until someone asks. That is a deliberate choice, and
// the price of it is that "HIDScript not available (mouse and keyboard
// disabled)" -- which is what the service says -- reads as a broken device
// to anyone holding the panel. So the panel refuses first, and names the
// remedy.
func TestPayloadsExplainTheOptInKeyboard(t *testing.T) {
	c := NewFakeClient()
	// Ethernet only, exactly what the shipped loadout deploys.
	c.Toggles = []Toggle{
		{Key: "use_HID_KEYBOARD", Label: "Keyboard", On: false},
		{Key: "use_HID_MOUSE", Label: "Mouse", On: false},
		{Key: "use_RNDIS", Label: "RNDIS net", On: true},
		{Key: "use_CDC_ECM", Label: "CDC ECM net", On: true},
	}
	app := NewApp(c, NewRoot())
	for _, b := range toPayloads {
		app.Handle(b)
	}

	if s := renderText(app); !strings.Contains(s, "no keyboard") {
		t.Errorf("the payload list does not warn that nothing is listening:\n%s", s)
	}

	// Choosing one must explain, not attempt.
	app.Handle(BtnEnter)
	s := renderText(app)
	// WITHOUT SCROLLING. The screen has six body rows; an instruction on row
	// seven is an instruction nobody reads. Checking the whole wrapped text
	// would pass for a remedy buried below the fold.
	visible := strings.Join(strings.Split(strings.TrimRight(s, "\n"), "\n")[:7], "\n")
	for _, want := range []string{"No keyboard", "Cable", "Keyboard", "KEY1"} {
		if !strings.Contains(visible, want) {
			t.Errorf("the refusal does not mention %q on the FIRST screenful:\n%s", want, visible)
		}
	}
	if c.Called("StartHID(") {
		t.Errorf("a payload was started with nothing to type into: %v", c.Calls)
	}

	// With a keyboard deployed it must behave exactly as before.
	c2 := NewFakeClient() // the fake's default has the keyboard on
	app2 := NewApp(c2, NewRoot())
	for _, b := range toPayloads {
		app2.Handle(b)
	}
	if s := renderText(app2); strings.Contains(s, "no keyboard") {
		t.Errorf("warned about a missing keyboard when one is deployed:\n%s", s)
	}
	app2.Handle(BtnEnter)
	app2.Handle(BtnRight)
	app2.Handle(BtnConfirm)
	if !c2.Called("StartHID(") {
		t.Errorf("a payload did not start with a keyboard deployed: %v", c2.Calls)
	}
}

// KEY3 must land you somewhere you can predict without looking.
//
// Home used to pop the stack and leave the root menu's selection wherever it
// was, so "press KEY3 then down twice to reach Cable" was a guess rather
// than an instruction. It also made the panel unscriptable: driving it over
// the mirror, KEY3 followed by two downs reached Net configs instead of
// Cable, because Home had preserved a selection I could not see.
func TestHomeLandsOnAKnownState(t *testing.T) {
	app := NewApp(NewFakeClient(), NewRoot())

	// Wander: down the root menu, into a screen, around inside it.
	for i := 0; i < 5; i++ {
		app.Handle(BtnDown)
	}
	app.Handle(BtnEnter)
	for i := 0; i < 3; i++ {
		app.Handle(BtnDown)
	}
	app.Handle(BtnHome)

	if app.Depth() != 1 {
		t.Fatalf("KEY3 left the stack at depth %d", app.Depth())
	}
	m, ok := app.Top().(*Menu)
	if !ok {
		t.Fatalf("the root is %T", app.Top())
	}
	if m.cur.sel != 0 || m.cur.first != 0 {
		t.Errorf("KEY3 left the selection at row %d (window from %d), want the top", m.cur.sel, m.cur.first)
	}

	// The property that matters, stated the way an operator would: from
	// anywhere, KEY3 then N downs reaches the Nth entry, every time.
	wantRow := rowOf(NewRoot(), "Cable (USB)")
	for _, wander := range [][]Button{
		{BtnDown, BtnDown, BtnDown, BtnDown, BtnDown, BtnDown, BtnDown},
		{BtnUp, BtnUp},
		{BtnDown, BtnEnter, BtnDown, BtnBack, BtnDown},
	} {
		for _, b := range wander {
			app.Handle(b)
		}
		app.Handle(BtnHome)
		for i := 0; i < wantRow; i++ {
			app.Handle(BtnDown)
		}
		app.Handle(BtnEnter)
		if _, ok := app.Top().(*USBView); !ok {
			t.Errorf("after KEY3 + %d downs + enter, landed on %T, want the Cable screen", wantRow, app.Top())
		}
		app.Handle(BtnHome)
	}
}

// And at a root that is already tidy, KEY3 must still answer -- otherwise it
// is the one press most likely to look like a dead key.
func TestHomeStillSpeaksAtATidyRoot(t *testing.T) {
	app := NewApp(NewFakeClient(), NewRoot())
	app.Handle(BtnHome) // already at the top, nothing to do
	if s := renderText(app); !strings.Contains(s, "already home") {
		t.Errorf("KEY3 at a tidy root said nothing:\n%s", s)
	}
	// But with the selection moved, it reports doing something.
	app.Handle(BtnDown)
	app.Handle(BtnDown)
	app.Handle(BtnHome)
	if s := renderText(app); !strings.Contains(s, "home") {
		t.Errorf("KEY3 with the cursor moved said nothing:\n%s", s)
	}
}
