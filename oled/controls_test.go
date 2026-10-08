package oled

import (
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
