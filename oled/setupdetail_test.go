package oled

import (
	"strings"
	"testing"
	"time"
)

func TestFormatElapsed(t *testing.T) {
	for _, c := range []struct {
		d    time.Duration
		want string
	}{
		{0, "0:00"},
		{9 * time.Second, "0:09"},
		{59 * time.Second, "0:59"},
		{60 * time.Second, "1:00"},
		{187 * time.Second, "3:07"}, // the case that used to read "187s"
		{3661 * time.Second, "61:01"},
		{-5 * time.Second, "0:00"}, // a clock that went backwards is not a crash
	} {
		if got := FormatElapsed(c.d); got != c.want {
			t.Errorf("FormatElapsed(%v) = %q, want %q", c.d, got, c.want)
		}
	}
}

// The screen has to distinguish "this is taking a while" from "this is
// stuck", because the operator's next move after the second one is to pull
// the power -- which is the single thing DO NOT POWER OFF is there to stop.
func TestSetupDetailSaysWhenItHasOverrun(t *testing.T) {
	const typical = 4 * time.Minute

	within := SetupDetail(90*time.Second, typical)
	if !strings.Contains(within, "1:30") {
		t.Errorf("within the expected window, detail = %q; it should show the elapsed time", within)
	}
	if !strings.Contains(within, "4 min") {
		t.Errorf("within the expected window, detail = %q; it should say what is expected, "+
			"so the operator does not have to guess whether this is normal", within)
	}

	over := SetupDetail(5*time.Minute, typical)
	if strings.Contains(over, "of ~") {
		t.Errorf("past the expected window, detail = %q; it must stop implying it is on schedule", over)
	}
	if !strings.Contains(over, "still working") {
		t.Errorf("past the expected window, detail = %q; it must say it is still working, "+
			"because the progress bar is clamped and cannot say it", over)
	}
	if !strings.Contains(over, "5:00") {
		t.Errorf("past the expected window, detail = %q; it should still show the elapsed time", over)
	}
}

// Every variant has to fit the panel, or the one line that explains an
// alarming screen gets truncated mid-word.
func TestSetupDetailFitsThePanel(t *testing.T) {
	for _, d := range []time.Duration{
		0, 30 * time.Second, 90 * time.Second, 4 * time.Minute,
		10 * time.Minute, time.Hour,
	} {
		for _, typ := range []time.Duration{0, time.Minute, 4 * time.Minute} {
			s := SetupDetail(d, typ)
			if len(s) > Cols {
				t.Errorf("SetupDetail(%v, %v) = %q is %d chars; the panel fits %d",
					d, typ, s, len(s), Cols)
			}
		}
	}
}

// A zero "typical" must not produce "of ~0 min", which reads as a broken
// device rather than an unknown duration.
func TestSetupDetailWithNoExpectation(t *testing.T) {
	s := SetupDetail(30*time.Second, 0)
	if strings.Contains(s, "~0") {
		t.Errorf("detail = %q; ~0 min is not a duration", s)
	}
}

// The warning itself must still be readable, and must still be the loudest
// thing on the screen.
func TestSetupWarningStillSaysDoNotPowerOff(t *testing.T) {
	fb := NewFramebuffer()
	DrawSetupWarning(fb, SetupDetail(90*time.Second, 4*time.Minute), 0.4)
	got := ReadBack(fb)
	for _, want := range []string{"SETTING UP", "1:30"} {
		if !strings.Contains(got, want) {
			t.Errorf("the setup screen does not show %q:\n%s", want, got)
		}
	}
	// DO NOT / POWER OFF are drawn at 2x, which the 1x row scanner cannot
	// read, so assert they are drawn by their pixels instead: the middle of
	// the panel must be substantially lit.
	lit := 0
	for y := 14; y < 44; y++ {
		for x := 0; x < Width; x++ {
			if fb.Get(x, y) {
				lit++
			}
		}
	}
	if lit < 200 {
		t.Errorf("only %d lit pixels where DO NOT POWER OFF should be", lit)
	}
}
