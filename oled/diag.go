package oled

import "fmt"

// ButtonTest answers one question that nothing else on this device can:
// is a control actually reaching the software?
//
// The first hardware report on this board was "the joystick is working but I
// think the keys doesn't work". That single sentence has four possible causes
// and no way to tell them apart from the menus:
//
//  1. the GPIO is not readable at all (wrong pin, pin held by something else)
//  2. the pin reads, but the debouncer never emits          (settle too long)
//  3. the press is emitted, but the screen does not bind it (unbound key)
//  4. the switch is dead                                    (hardware)
//
// This screen shows, live and side by side, the RAW pin level and the number
// of debounced presses the UI has received. Level moves but count does not is
// (2). Count moves but the menus did nothing is (3). Neither moves, while the
// pin name shown here matches the board, is (1) or (4). No console, no login,
// no second machine.
type ButtonTest struct {
	last string
}

func NewButtonTest() *ButtonTest { return &ButtonTest{} }

func (t *ButtonTest) Title() string { return "Buttons" }

func (t *ButtonTest) Hint() string {
	if t.last != "" {
		return t.last
	}
	return "left exits, KEY3 home"
}

// shortLabel is the four-column name used in the grid.
func shortLabel(b Button) string {
	switch b {
	case BtnUp:
		return "Up"
	case BtnDown:
		return "Down"
	case BtnLeft:
		return "Left"
	case BtnRight:
		return "Rght"
	case BtnPress:
		return "Push"
	case BtnKey1:
		return "KEY1"
	case BtnKey2:
		return "KEY2"
	case BtnKey3:
		return "KEY3"
	}
	return "?"
}

func (t *ButtonTest) Render(fb *Framebuffer, app *App) {
	lr, _ := app.Input.(LevelReader)
	var levels map[Button]bool
	if lr != nil {
		levels = lr.Levels()
	}

	// Two columns of four: joystick on the left, keys on the right. Eight
	// controls do not fit in six body rows one per line.
	//
	//	Up   . 0  KEY1 # 2
	//
	// '#' is held right now, '.' is released, the number is how many
	// debounced presses this control has delivered since boot.
	for r := 0; r < 4; r++ {
		y := bodyPxTop + r*LineH
		left, right := AllButtons[r], AllButtons[r+4]
		fb.Text(0, y, cell(left, levels, lr, app))
		fb.Text(11*GlyphW, y, cell(right, levels, lr, app))
	}

	if lr == nil {
		fb.Text(0, bodyPxTop+5*LineH, Truncate("no raw pin access", Cols))
	}
}

func cell(b Button, levels map[Button]bool, lr LevelReader, app *App) string {
	mark := "."
	if lr == nil {
		mark = "?"
	} else if levels[b] {
		mark = "#"
	}
	n := app.Seen(b)
	if n > 99 {
		n = 99
	}
	return fmt.Sprintf("%-4s %s %-2d", shortLabel(b), mark, n)
}

func (t *ButtonTest) Handle(b Button, app *App) Action {
	switch b {
	case BtnBack:
		return ActPop
	case BtnHome:
		// KEY3 keeps its meaning even here. Trapping the operator on the
		// diagnostic screen to diagnose the escape key would be a poor
		// trade, and KEY3 needs no live grid to prove itself: the jump to
		// the root IS the proof, and its press count is waiting when you
		// come back in.
		return ActHome
	}
	// Every other control reports itself and its pin, rather than falling
	// through to App's "KEY1: nothing here" -- on this screen a press IS the
	// result, so it has to be the thing that gets printed.
	pin := ""
	if lr, ok := app.Input.(LevelReader); ok {
		pin = " " + lr.PinName(b)
	}
	t.last = shortLabel(b) + pin + " ok"
	app.Toast("%s", Truncate(t.last, Cols-1))
	return ActNone
}
