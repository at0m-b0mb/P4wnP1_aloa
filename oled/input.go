package oled

import "time"

// Button is one physical control on the Waveshare 1.3inch OLED HAT: a 5-way
// joystick and three keys down the left edge.
type Button int

const (
	BtnNone Button = iota
	BtnUp
	BtnDown
	BtnLeft
	BtnRight
	BtnPress // joystick centre
	BtnKey1
	BtnKey2
	BtnKey3
)

func (b Button) String() string {
	switch b {
	case BtnUp:
		return "up"
	case BtnDown:
		return "down"
	case BtnLeft:
		return "left"
	case BtnRight:
		return "right"
	case BtnPress:
		return "press"
	case BtnKey1:
		return "key1"
	case BtnKey2:
		return "key2"
	case BtnKey3:
		return "key3"
	}
	return "none"
}

// The control scheme, in one place so the on-screen hints and the handlers
// cannot drift apart:
//
//	up / down     move the selection
//	right, press  enter / confirm
//	left          back
//	KEY1          context action (varies per screen, always labelled)
//	KEY2          refresh
//	KEY3          jump to the TOP of the root menu -- a known state, so
//	              "KEY3 then two downs" is an instruction, not a guess
//
// Left-is-back rather than a dedicated key, because on a 5-way stick the
// horizontal axis is the one your thumb finds without looking, and this device
// is often operated in a pocket or under a desk.
const (
	BtnBack    = BtnLeft
	BtnEnter   = BtnRight
	BtnConfirm = BtnPress
	BtnAction  = BtnKey1
	BtnRefresh = BtnKey2
	BtnHome    = BtnKey3
)

// Input is a source of button presses. An interface so the UI can be driven by
// GPIO on the device, by a keyboard in the simulator, or by a slice in a test,
// with no code path that only exists on hardware.
type Input interface {
	// Events yields presses. Closed when the source is finished.
	Events() <-chan Button
	Close() error
}

// ChanInput adapts a plain channel, for the simulator and tests.
type ChanInput struct{ Ch chan Button }

func NewChanInput() *ChanInput { return &ChanInput{Ch: make(chan Button, 16)} }

func (c *ChanInput) Events() <-chan Button { return c.Ch }
func (c *ChanInput) Close() error          { return nil }

// Send delivers a press, dropping it if nothing is reading. Dropping rather
// than blocking: an input source must never wedge because the UI is busy
// redrawing.
func (c *ChanInput) Send(b Button) {
	select {
	case c.Ch <- b:
	default:
	}
}

// Debouncer turns raw level samples into clean presses, with hold-to-repeat.
//
// Mechanical contacts bounce for a few milliseconds, so a naive edge read
// produces a burst of presses from one push and the menu jumps five rows.
// Hold-to-repeat matters just as much in the other direction: scrolling a list
// of forty stored templates one press at a time, on a stick, is miserable.
type Debouncer struct {
	// Settle is how long a level must hold before it counts.
	Settle time.Duration
	// RepeatDelay is the hold time before auto-repeat starts, and
	// RepeatEvery the interval after that. Zero disables repeat.
	RepeatDelay time.Duration
	RepeatEvery time.Duration

	state    map[Button]*btnState
	nowFn    func() time.Time
	repeatOK map[Button]bool
	initOnce bool
}

type btnState struct {
	pressed    bool
	since      time.Time
	lastRepeat time.Time
}

// NewDebouncer returns a debouncer with settings suited to a thumb stick.
func NewDebouncer() *Debouncer {
	return &Debouncer{
		Settle:      12 * time.Millisecond,
		RepeatDelay: 450 * time.Millisecond,
		RepeatEvery: 110 * time.Millisecond,
	}
}

// SetClock injects time, so the repeat behaviour can be tested without
// sleeping. A test that sleeps for real is a test nobody runs.
func (d *Debouncer) SetClock(fn func() time.Time) { d.nowFn = fn }

func (d *Debouncer) now() time.Time {
	if d.nowFn != nil {
		return d.nowFn()
	}
	return time.Now()
}

func (d *Debouncer) init() {
	if d.initOnce {
		return
	}
	d.state = map[Button]*btnState{}
	d.repeatOK = map[Button]bool{
		BtnUp: true, BtnDown: true, BtnLeft: true, BtnRight: true,
	}
	d.initOnce = true
}

// Update feeds the current physical level of one button (true = held) and
// returns whether a press should be emitted right now.
func (d *Debouncer) Update(b Button, held bool) bool {
	d.init()
	now := d.now()
	st, ok := d.state[b]
	if !ok {
		st = &btnState{}
		d.state[b] = st
	}

	if !held {
		st.pressed = false
		st.since = time.Time{}
		return false
	}

	// Rising edge: start the settle timer, emit nothing yet.
	if !st.pressed {
		st.pressed = true
		st.since = now
		st.lastRepeat = time.Time{}
		return false
	}

	held4 := now.Sub(st.since)
	if held4 < d.Settle {
		return false
	}

	// First emission once settled.
	if st.lastRepeat.IsZero() {
		st.lastRepeat = now
		return true
	}

	// Auto-repeat, only for the directions where it helps.
	if d.RepeatEvery <= 0 || !d.repeatOK[b] {
		return false
	}
	if held4 < d.RepeatDelay {
		return false
	}
	if now.Sub(st.lastRepeat) >= d.RepeatEvery {
		st.lastRepeat = now
		return true
	}
	return false
}

// DefaultPins is the wiring of the Waveshare 1.3inch OLED HAT, from its wiki.
// All eight controls are active-low with the internal pull-up engaged.
//
// BCM numbers, which is what periph's names use. A variable rather than a
// constant block so an operator with a differently wired panel can override
// them without a rebuild.
//
// It lives in the portable file, not next to the GPIO driver, because it is
// the BOARD's wiring rather than anything about Linux -- and because the
// button test and its tests both need to name a pin on a machine that has no
// GPIO at all. While it was behind the linux build tag, nothing off-device
// could so much as print it.
var DefaultPins = map[Button]string{
	BtnUp:    "GPIO6",
	BtnDown:  "GPIO19",
	BtnLeft:  "GPIO5",
	BtnRight: "GPIO26",
	BtnPress: "GPIO13",
	BtnKey1:  "GPIO21",
	BtnKey2:  "GPIO20",
	BtnKey3:  "GPIO16",
}

// AllButtons is every control, in the order the button test lists them:
// joystick first, then the three keys down the edge.
var AllButtons = []Button{
	BtnUp, BtnDown, BtnLeft, BtnRight, BtnPress,
	BtnKey1, BtnKey2, BtnKey3,
}

// LevelReader is implemented by an Input that can report the RAW, undebounced
// state of each control.
//
// This exists for one screen: the button test. Everything else in the UI sees
// debounced presses, which is right -- but it means a report of "the keys
// don't work" cannot be answered. The pin could be unreadable, the wiring
// could differ from this board revision, the debouncer could be eating the
// press, or the key could simply be unbound on that screen. Showing the live
// pin next to the press count separates all four, on the device, with no
// console and no login.
type LevelReader interface {
	Levels() map[Button]bool
	// PinName is what the pin is called on the board, so an operator can
	// check it against the silkscreen rather than against a rebuild.
	PinName(b Button) string
}
