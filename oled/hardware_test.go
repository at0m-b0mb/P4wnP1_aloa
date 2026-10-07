package oled

import (
	"testing"
	"time"
)

// The parts of the hardware path that can be checked without hardware: the
// command bytes, the addressing arithmetic, and the debounce timing. None of
// these can be exercised in the simulator, and all three are places where a
// mistake shows up as "the screen is dead" or "the stick is unusable" rather
// than as an error anyone can read.

func TestSH1106AddressesTheVisibleColumnsNotRAMColumnZero(t *testing.T) {
	// SH1106 RAM is 132 wide with the 128-pixel panel centred, so the first
	// visible column is 2. Getting this wrong shifts every frame two pixels
	// and wraps it.
	for page := 0; page < Pages; page++ {
		got := pageCommands(SH1106, page)
		want := []byte{byte(0xB0 | page), 0x02, 0x10}
		if len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
			t.Errorf("SH1106 page %d: got % X, want % X", page, got, want)
		}
	}
}

func TestSSD1306AddressesFromColumnZero(t *testing.T) {
	for page := 0; page < Pages; page++ {
		got := pageCommands(SSD1306, page)
		want := []byte{byte(0xB0 | page), 0x00, 0x10}
		if len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
			t.Errorf("SSD1306 page %d: got % X, want % X", page, got, want)
		}
	}
}

// The charge pump is the difference between an initialised panel and a lit
// one, and the two controllers use different commands. Sending the SSD1306
// pair to an SH1106 leaves the display configured and dark, which is
// indistinguishable from a wiring fault and is the commonest way this board
// gets reported broken.
func TestInitSequencesEnableTheRightChargePump(t *testing.T) {
	sh := SH1106.initSequence()
	if !containsPair(sh, cmdSetDCDC, 0x8B) {
		t.Errorf("SH1106 init is missing the DC-DC enable (0xAD 0x8B): % X", sh)
	}
	if containsByte(sh, cmdChargePump) {
		t.Errorf("SH1106 init sends the SSD1306 charge pump command 0x8D: % X", sh)
	}
	if containsByte(sh, cmdSetMemoryMode) {
		t.Errorf("SH1106 init sends a memory addressing mode it does not have: % X", sh)
	}

	ss := SSD1306.initSequence()
	if !containsPair(ss, cmdChargePump, 0x14) {
		t.Errorf("SSD1306 init is missing the charge pump enable (0x8D 0x14): % X", ss)
	}
	if containsByte(ss, cmdSetDCDC) {
		t.Errorf("SSD1306 init sends the SH1106 DC-DC command 0xAD: % X", ss)
	}
}

func TestInitSequencesConfigureA128x64Panel(t *testing.T) {
	for _, c := range []Controller{SH1106, SSD1306} {
		seq := c.initSequence()
		if seq[0] != cmdDisplayOff {
			t.Errorf("%s: init does not start with display off: % X", c, seq[:4])
		}
		if seq[len(seq)-1] != cmdDisplayOn {
			t.Errorf("%s: init does not end with display on: % X", c, seq[len(seq)-4:])
		}
		// 1/64 duty and the 0x12 COM pin layout are what make it 128x64
		// rather than 128x32; the wrong pair shows every other row.
		if !containsPair(seq, cmdSetMultiplex, 0x3F) {
			t.Errorf("%s: multiplex is not 1/64: % X", c, seq)
		}
		if !containsPair(seq, cmdSetComPins, 0x12) {
			t.Errorf("%s: COM pins are not set for 64 rows: % X", c, seq)
		}
	}
}

func containsByte(seq []byte, b byte) bool {
	for _, x := range seq {
		if x == b {
			return true
		}
	}
	return false
}

func containsPair(seq []byte, a, b byte) bool {
	for i := 0; i+1 < len(seq); i++ {
		if seq[i] == a && seq[i+1] == b {
			return true
		}
	}
	return false
}

// A framebuffer page must be exactly the bytes the panel expects, in the
// layout it expects: one byte per column, bit 0 at the top of the page.
func TestFramebufferMatchesTheControllerLayout(t *testing.T) {
	fb := NewFramebuffer()
	if len(fb.Bytes()) != Pages*Width {
		t.Fatalf("buffer is %d bytes, panel wants %d", len(fb.Bytes()), Pages*Width)
	}
	for p := 0; p < Pages; p++ {
		if len(fb.Page(p)) != Width {
			t.Errorf("page %d is %d bytes, want %d", p, len(fb.Page(p)), Width)
		}
	}
	if fb.Page(-1) != nil || fb.Page(Pages) != nil {
		t.Error("an out-of-range page must be nil, not a panic or a wrapped slice")
	}

	// Top-left pixel is bit 0 of page 0, column 0.
	fb.Set(0, 0, true)
	if fb.Page(0)[0] != 0x01 {
		t.Errorf("pixel (0,0) set byte to %#x, want 0x01", fb.Page(0)[0])
	}
	// Bottom-right is bit 7 of the last page, last column.
	fb.Clear()
	fb.Set(Width-1, Height-1, true)
	if got := fb.Page(Pages - 1)[Width-1]; got != 0x80 {
		t.Errorf("pixel (127,63) set byte to %#x, want 0x80", got)
	}
}

// Drawing outside the panel must be dropped, not wrapped. A label one pixel
// too long should be clipped, not reappear on the left-hand side.
func TestDrawingOutsideThePanelIsDroppedNotWrapped(t *testing.T) {
	fb := NewFramebuffer()
	for _, p := range [][2]int{
		{-1, 0}, {0, -1}, {Width, 0}, {0, Height},
		{-100, -100}, {1000, 1000}, {Width, Height},
	} {
		fb.Set(p[0], p[1], true)
	}
	for _, b := range fb.Bytes() {
		if b != 0 {
			t.Fatal("an out-of-range Set lit a pixel inside the panel")
		}
	}
	if fb.Get(-1, 0) || fb.Get(0, -1) || fb.Get(Width, 0) || fb.Get(0, Height) {
		t.Error("Get outside the panel returned true")
	}

	// Text that runs off the right edge must stop, not wrap to the next row.
	fb.Clear()
	fb.Text(Width-8, 0, "ABCDEFGH")
	for y := LineH; y < Height; y++ {
		for x := 0; x < Width; x++ {
			if fb.Get(x, y) {
				t.Fatalf("text overflowed onto row %d at x=%d", y/LineH, x)
			}
		}
	}
}

func TestTruncateAndPad(t *testing.T) {
	cases := []struct {
		in   string
		n    int
		want string
	}{
		{"", 5, ""},
		{"abc", 5, "abc"},
		{"abcde", 5, "abcde"},
		{"abcdef", 5, "abcd~"},
		{"abcdef", 1, "~"},
		{"abcdef", 0, ""},
		{"abcdef", -1, ""},
		{"ünïcødé", 3, "ün~"},
	}
	for _, c := range cases {
		if got := Truncate(c.in, c.n); got != c.want {
			t.Errorf("Truncate(%q,%d) = %q, want %q", c.in, c.n, got, c.want)
		}
		// Whatever comes out must fit.
		if n := len([]rune(Truncate(c.in, c.n))); c.n > 0 && n > c.n {
			t.Errorf("Truncate(%q,%d) returned %d runes", c.in, c.n, n)
		}
	}
	if got := Pad("ab", 5); got != "ab   " {
		t.Errorf("Pad = %q", got)
	}
	if got := Pad("abcdef", 3); got != "abcdef" {
		t.Errorf("Pad must not shorten: %q", got)
	}
}

func TestWrapNeverExceedsTheWidth(t *testing.T) {
	inputs := []string{
		"", "short",
		"the WiFi subsystem is unavailable on this device",
		"supercalifragilisticexpialidociousandthensomemore",
		"a b c d e f g h i j k l m n o p q r s t u v w x y z",
		"trailing   spaces   everywhere   ",
	}
	for _, in := range inputs {
		for _, n := range []int{1, 5, Cols, 100} {
			for _, line := range wrap(in, n) {
				if len([]rune(line)) > n {
					t.Errorf("wrap(%q,%d) produced a %d-rune line: %q",
						in, n, len([]rune(line)), line)
				}
			}
		}
	}
	if got := wrap("anything", 0); got != nil {
		t.Errorf("wrap with width 0 returned %v, want nil", got)
	}
}

// The debouncer decides whether the stick feels broken or fine, and it cannot
// be checked on hardware anyone has. Driven by an injected clock so it runs
// in microseconds: a test that sleeps for real is a test nobody runs.
func TestDebouncerRejectsContactBounce(t *testing.T) {
	d := NewDebouncer()
	now := time.Unix(0, 0)
	d.SetClock(func() time.Time { return now })

	// Bounce: held, released, held again, all inside the settle window.
	if d.Update(BtnUp, true) {
		t.Error("emitted on the rising edge, before settling")
	}
	now = now.Add(3 * time.Millisecond)
	if d.Update(BtnUp, false) {
		t.Error("emitted on release")
	}
	now = now.Add(2 * time.Millisecond)
	if d.Update(BtnUp, true) {
		t.Error("emitted immediately after a bounce")
	}
	// Still inside Settle from the new edge.
	now = now.Add(d.Settle - time.Millisecond)
	if d.Update(BtnUp, true) {
		t.Error("emitted before the settle time elapsed")
	}
	// Past it: exactly one press.
	now = now.Add(2 * time.Millisecond)
	if !d.Update(BtnUp, true) {
		t.Fatal("no press after the contact settled")
	}
	if d.Update(BtnUp, true) {
		t.Error("a single hold produced a second press immediately")
	}
}

func TestDebouncerRepeatsOnHoldButNotTooSoon(t *testing.T) {
	d := NewDebouncer()
	now := time.Unix(0, 0)
	d.SetClock(func() time.Time { return now })

	d.Update(BtnDown, true)
	now = now.Add(d.Settle + time.Millisecond)
	if !d.Update(BtnDown, true) {
		t.Fatal("no initial press")
	}

	// Nothing until RepeatDelay. The step is checked BEFORE taking it, so
	// the loop stops short of the boundary instead of crossing it and then
	// complaining about a repeat that was due.
	start := time.Unix(0, 0)
	const step = 20 * time.Millisecond
	for now.Add(step).Sub(start) < d.RepeatDelay {
		now = now.Add(step)
		if d.Update(BtnDown, true) {
			t.Fatalf("repeated after only %v, before the %v delay",
				now.Sub(start), d.RepeatDelay)
		}
	}

	// Then roughly one per RepeatEvery.
	repeats := 0
	for i := 0; i < 50; i++ {
		now = now.Add(d.RepeatEvery)
		if d.Update(BtnDown, true) {
			repeats++
		}
	}
	if repeats < 40 {
		t.Errorf("only %d repeats in 50 intervals; hold-to-scroll will feel stuck", repeats)
	}

	// Releasing stops it.
	d.Update(BtnDown, false)
	now = now.Add(time.Second)
	if d.Update(BtnDown, false) {
		t.Error("emitted while released")
	}
}

// The three side keys must NOT auto-repeat. KEY1 is the action key -- holding
// it a moment too long should not deploy a USB composition twice or fire a
// payload again.
func TestActionKeysDoNotAutoRepeat(t *testing.T) {
	d := NewDebouncer()
	now := time.Unix(0, 0)
	d.SetClock(func() time.Time { return now })

	for _, b := range []Button{BtnKey1, BtnKey2, BtnKey3, BtnPress} {
		d = NewDebouncer()
		d.SetClock(func() time.Time { return now })
		d.Update(b, true)
		now = now.Add(d.Settle + time.Millisecond)
		if !d.Update(b, true) {
			t.Fatalf("%s: no initial press", b)
		}
		extra := 0
		for i := 0; i < 100; i++ {
			now = now.Add(50 * time.Millisecond)
			if d.Update(b, true) {
				extra++
			}
		}
		if extra != 0 {
			t.Errorf("%s repeated %d times while held; it must fire once", b, extra)
		}
	}
}

func TestDebouncerKeepsButtonsIndependent(t *testing.T) {
	d := NewDebouncer()
	now := time.Unix(0, 0)
	d.SetClock(func() time.Time { return now })

	d.Update(BtnUp, true)
	d.Update(BtnDown, true)
	now = now.Add(d.Settle + time.Millisecond)
	if !d.Update(BtnUp, true) {
		t.Error("up did not fire")
	}
	if !d.Update(BtnDown, true) {
		t.Error("down did not fire; the buttons share state")
	}
}

// NullDisplay and RecordingDisplay are what the daemon and the tests run
// against, so they have to satisfy the interface the panel does.
func TestDisplayImplementations(t *testing.T) {
	var _ Display = &NullDisplay{}
	var _ Display = &RecordingDisplay{}

	rec := &RecordingDisplay{}
	fb := NewFramebuffer()
	fb.Set(5, 5, true)
	if err := rec.Show(fb); err != nil {
		t.Fatal(err)
	}
	// Show must COPY: the daemon reuses one framebuffer for every frame, so
	// keeping a pointer would make every recorded frame the latest one.
	fb.Clear()
	if !rec.Last.Get(5, 5) {
		t.Error("RecordingDisplay kept a reference instead of a copy")
	}
}
