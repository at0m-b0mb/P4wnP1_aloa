package oled

import (
	"fmt"
	"time"
)

// Screen layout. Eight 8-pixel rows: one for the title, six for content, one
// for the hint line.
//
// The hint line is not decoration. This device has eight unlabelled controls
// and no manual within reach, so every screen states what the context key
// does. Without it an operator has to remember, and an operator who has to
// remember uses the web console instead.
const (
	titleRow  = 0
	bodyTop   = 1
	bodyRows  = 6
	hintRow   = 7
	bodyPxTop = bodyTop * LineH
)

// Action is what a key press does to the view stack.
type Action int

const (
	ActNone Action = iota
	ActPop         // back one screen
	ActHome        // back to the root
	ActQuit        // tear down the UI
)

// View is one screen.
type View interface {
	Title() string
	Render(fb *Framebuffer, app *App)
	Handle(b Button, app *App) Action
	// Hint is the label for the context key, or "" if the screen has none.
	Hint() string
}

// Resettable is implemented by a view whose selection should return to the
// top when the operator asks to go home. ResetCursor reports whether it
// actually moved, so the caller can tell a real change from a no-op.
type Resettable interface{ ResetCursor() bool }

// Refresher is implemented by views whose content comes from the device, so
// the refresh key and the periodic poll reach them without the App knowing
// what any particular screen holds.
type Refresher interface{ Refresh(app *App) }

// App is the UI. It owns the view stack, the toast and the client.
type App struct {
	Client Client
	stack  []View

	// Input is the source the presses arrived from, kept only so the button
	// test screen can read the pins directly. Nil elsewhere.
	Input Input

	toast     string
	toastTill time.Time
	nowFn     func() time.Time

	// changes counts anything the operator can SEE: a toast, a push, a pop, a
	// jump home. Handle uses it to tell "the screen dealt with that press"
	// from "nothing happened at all", which is the difference between a key
	// that is unbound and a key that is not wired up.
	changes int

	// seen counts debounced presses per control, for the button test.
	seen map[Button]int

	// Quit is closed when the user asks to exit (simulator only; on the
	// device the daemon runs until stopped).
	quit bool
}

func NewApp(c Client, root View) *App {
	return &App{Client: c, stack: []View{root}}
}

func (a *App) now() time.Time {
	if a.nowFn != nil {
		return a.nowFn()
	}
	return time.Now()
}

// SetClock injects time for tests.
func (a *App) SetClock(fn func() time.Time) { a.nowFn = fn }

func (a *App) Top() View {
	if len(a.stack) == 0 {
		return nil
	}
	return a.stack[len(a.stack)-1]
}

func (a *App) Push(v View) {
	a.stack = append(a.stack, v)
	a.changes++
	if r, ok := v.(Refresher); ok {
		r.Refresh(a)
	}
}

func (a *App) Pop() {
	if len(a.stack) > 1 {
		a.stack = a.stack[:len(a.stack)-1]
		a.changes++
	}
}

// Home returns to a KNOWN STATE, not merely to the root screen.
//
// It used to pop the stack and leave the root menu's selection wherever it
// happened to be, so KEY3 from deep in the tree landed you on the root with
// the cursor halfway down it. On a device with eight unlabelled controls the
// single reliable escape should put you somewhere you can predict without
// looking -- and it is the difference between "press KEY3 then down twice to
// reach Cable" being a usable instruction and being a guess.
//
// It also makes the panel scriptable. Driving it over the mirror, I pressed
// KEY3 and then two downs expecting Cable, and landed on Net configs,
// because Home had preserved a selection I could not see.
func (a *App) Home() {
	if len(a.stack) > 1 {
		a.stack = a.stack[:1]
		a.changes++
	}
	if r, ok := a.stack[0].(Resettable); ok && r.ResetCursor() {
		a.changes++
	}
}

func (a *App) Depth() int     { return len(a.stack) }
func (a *App) Quitting() bool { return a.quit }

// Toast shows a transient message on the hint line. Three seconds: long enough
// to read eleven words, short enough that it is gone before it becomes stale.
func (a *App) Toast(format string, args ...interface{}) {
	a.toast = fmt.Sprintf(format, args...)
	a.toastTill = a.now().Add(3 * time.Second)
	a.changes++
}

func (a *App) activeToast() string {
	if a.toast != "" && a.now().Before(a.toastTill) {
		return a.toast
	}
	return ""
}

// Handle routes a press.
//
// The screen gets first refusal. If it does nothing the operator can see, the
// three keys fall back to their documented meanings here, so that KEY1, KEY2
// and KEY3 always answer on every screen in the tree.
//
// That fallback is not a nicety. The first hardware report on this board was
// "the joystick is working but I think the keys doesn't work" -- made from the
// Radio menu, which is a plain Menu, and Menu bound neither KEY1 (nothing to
// act on) nor KEY2 (nothing to refresh). Both keys were read correctly,
// debounced correctly and delivered correctly, and then dropped on the floor.
// A control that silently does nothing is indistinguishable from a control
// that is not connected, so now there is no such control.
func (a *App) Handle(b Button) {
	v := a.Top()
	if v == nil {
		return
	}
	if a.seen == nil {
		a.seen = map[Button]int{}
	}
	a.seen[b]++

	before := a.changes
	switch v.Handle(b, a) {
	case ActPop:
		a.Pop()
	case ActHome:
		a.Home()
	case ActQuit:
		a.quit = true
		return
	}
	if a.changes != before {
		return // the screen answered for itself
	}

	switch b {
	case BtnRefresh:
		// A no-op on a screen that holds no device data, but the toast is
		// the point: it proves the key reached the UI.
		a.Refresh()
		a.Toast("refreshed")
	case BtnHome:
		// Home from here too: at the root with the selection partway down,
		// KEY3 still has work to do -- it puts the cursor back on the first
		// row. Only when there is genuinely nothing left to change does it
		// say so, because pressing KEY3 at a tidy root is the single most
		// likely way to conclude the key is dead.
		before := a.changes
		a.Home()
		if a.changes != before {
			a.Toast("home")
		} else {
			a.Toast("already home")
		}
	case BtnAction:
		a.Toast("KEY1: nothing here")
	}
}

// Seen reports how many debounced presses of b have reached the UI. Used by
// the button test to separate a dead pin from an unbound key.
func (a *App) Seen(b Button) int { return a.seen[b] }

// Refresh re-reads the current screen's data.
func (a *App) Refresh() {
	if r, ok := a.Top().(Refresher); ok {
		r.Refresh(a)
	}
}

// Render draws the whole screen: chrome plus the active view's body.
func (a *App) Render(fb *Framebuffer) {
	fb.Clear()
	v := a.Top()
	if v == nil {
		return
	}

	// Title bar, inverted. A back arrow when there is somewhere to go back to,
	// so the stack depth is visible rather than something to keep in your head.
	title := v.Title()
	if a.Depth() > 1 {
		title = "<" + title
	}
	fb.Text(1, titleRow*LineH, Truncate(title, Cols-1))
	fb.Invert(0, titleRow*LineH, Width, LineH)

	v.Render(fb, a)

	// Hint line: a toast if one is live, otherwise the screen's own hint.
	hint := a.activeToast()
	if hint == "" {
		hint = v.Hint()
	}
	if hint != "" {
		fb.HLine(0, hintRow*LineH-1, Width, true)
		fb.Text(1, hintRow*LineH, Truncate(hint, Cols-1))
	}
}

// --- scrolling list ---------------------------------------------------------

// cursor tracks selection and the visible window over a list. Pulled out
// because every screen in this UI is a list of something, and getting the
// window arithmetic subtly wrong on each one separately is how a menu ends up
// skipping an entry at the bottom.
type cursor struct {
	sel   int
	first int
	n     int
}

func (c *cursor) setLen(n int) {
	c.n = n
	if c.sel >= n {
		c.sel = n - 1
	}
	if c.sel < 0 {
		c.sel = 0
	}
	c.clamp()
}

func (c *cursor) clamp() {
	if c.n <= bodyRows {
		c.first = 0
		return
	}
	if c.sel < c.first {
		c.first = c.sel
	}
	if c.sel >= c.first+bodyRows {
		c.first = c.sel - bodyRows + 1
	}
	if c.first > c.n-bodyRows {
		c.first = c.n - bodyRows
	}
	if c.first < 0 {
		c.first = 0
	}
}

// move steps the selection, wrapping at both ends. Wrapping because reaching
// the last item of a 40-entry list should not require 39 presses back.
func (c *cursor) move(d int) {
	if c.n == 0 {
		return
	}
	c.sel = (c.sel + d + c.n) % c.n
	c.clamp()
}

// scrollbarW is the lane reserved on the right when a list does not fit: two
// pixels of knob and one of air.
const scrollbarW = 3

// drawRows renders the visible window. label is called for each index.
func (c *cursor) drawRows(fb *Framebuffer, label func(i int) string) {
	// When a scrollbar is showing, the selection bar stops short of it and
	// labels get one column less. Inverting the full width instead put the
	// highlight straight over the rail, so on the selected row the scrollbar
	// vanished -- the one row where you most want to know where you are.
	w, cols := Width, Cols
	if c.n > bodyRows {
		w, cols = Width-scrollbarW, Cols-1
	}
	for r := 0; r < bodyRows; r++ {
		i := c.first + r
		if i >= c.n {
			break
		}
		y := bodyPxTop + r*LineH
		fb.Text(0, y, Pad(Truncate(label(i), cols), cols))
		if i == c.sel {
			fb.Invert(0, y-1, w, LineH)
		}
	}
	c.drawScrollbar(fb)
}

// drawScrollbar is a 2px rail on the right, drawn only when the list does not
// fit. A permanent scrollbar on a 21-column screen costs a character of every
// row for nothing.
func (c *cursor) drawScrollbar(fb *Framebuffer) {
	if c.n <= bodyRows {
		return
	}
	top := bodyPxTop
	h := bodyRows * LineH
	fb.VLine(Width-1, top, h, true)
	knob := h * bodyRows / c.n
	if knob < 3 {
		knob = 3
	}
	pos := top + (h-knob)*c.first/(c.n-bodyRows)
	fb.FillRect(Width-2, pos, 2, knob, true)
}

// --- generic views ----------------------------------------------------------

// MenuItem is one row of a static menu.
type MenuItem struct {
	Label string
	// Do runs on enter. Return a view to push, or nil to stay.
	Do func(app *App) View
}

// Menu is a fixed list of labelled actions.
type Menu struct {
	title string
	items []MenuItem
	cur   cursor
	hint  string
}

func NewMenu(title string, items []MenuItem) *Menu {
	m := &Menu{title: title, items: items}
	m.cur.setLen(len(items))
	return m
}

// ResetCursor puts the selection back on the first row.
func (m *Menu) ResetCursor() bool {
	if m.cur.sel == 0 && m.cur.first == 0 {
		return false
	}
	m.cur.sel, m.cur.first = 0, 0
	return true
}

func (m *Menu) Title() string { return m.title }
func (m *Menu) Hint() string  { return m.hint }

func (m *Menu) Render(fb *Framebuffer, _ *App) {
	if len(m.items) == 0 {
		fb.Text(0, bodyPxTop+LineH, "  (nothing here)")
		return
	}
	m.cur.drawRows(fb, func(i int) string { return m.items[i].Label })
}

func (m *Menu) Handle(b Button, app *App) Action {
	switch b {
	case BtnUp:
		m.cur.move(-1)
	case BtnDown:
		m.cur.move(1)
	case BtnBack:
		return ActPop
	case BtnHome:
		return ActHome
	case BtnEnter, BtnConfirm:
		if m.cur.n == 0 {
			return ActNone
		}
		if do := m.items[m.cur.sel].Do; do != nil {
			if v := do(app); v != nil {
				app.Push(v)
			}
		}
	}
	return ActNone
}

// Confirm is a yes/no gate in front of anything destructive or outward-facing.
type Confirm struct {
	title    string
	question string
	detail   string
	onYes    func(app *App)
	yes      bool
}

func NewConfirm(title, question, detail string, onYes func(app *App)) *Confirm {
	return &Confirm{title: title, question: question, detail: detail, onYes: onYes}
}

func (c *Confirm) Title() string { return c.title }
func (c *Confirm) Hint() string  { return "L/R then press" }

func (c *Confirm) Render(fb *Framebuffer, _ *App) {
	y := bodyPxTop
	for _, line := range wrap(c.question, Cols) {
		if y+GlyphH > hintRow*LineH-2 {
			break
		}
		fb.Text(0, y, line)
		y += LineH
	}
	if c.detail != "" {
		for _, line := range wrap(c.detail, Cols) {
			if y+GlyphH > hintRow*LineH-10 {
				break
			}
			fb.Text(0, y, line)
			y += LineH
		}
	}

	// Buttons on the last body row.
	by := bodyPxTop + (bodyRows-1)*LineH
	noX, yesX := 8, 72
	fb.Text(noX+4, by, "No")
	fb.Text(yesX+4, by, "Yes")
	if c.yes {
		fb.Invert(yesX, by-1, TextWidth("Yes")+8, LineH)
	} else {
		fb.Invert(noX, by-1, TextWidth("No")+8, LineH)
	}
}

func (c *Confirm) Handle(b Button, app *App) Action {
	switch b {
	case BtnLeft:
		// Left is back everywhere else, but here it moves the selection: a
		// confirm dialog where the natural "no" gesture silently cancels is
		// fine, and that is exactly what popping does.
		if c.yes {
			c.yes = false
			return ActNone
		}
		return ActPop
	case BtnRight:
		c.yes = true
	case BtnUp, BtnDown:
		c.yes = !c.yes
	case BtnConfirm:
		// Pop BEFORE running the action, not after.
		//
		// Returning ActPop and letting App.Handle do it afterwards popped
		// whatever the action had just pushed, so a payload that ran and
		// produced output showed the confirmation again instead of the
		// result. The dialog has to be off the stack before the action gets
		// a chance to put something on it.
		app.Pop()
		if c.yes && c.onYes != nil {
			c.onYes(app)
		}
		return ActNone
	case BtnHome:
		return ActHome
	}
	return ActNone
}

// TextView shows scrollable wrapped text: command output, credentials, an
// error too long for a toast.
type TextView struct {
	title string
	lines []string
	cur   cursor
	hint  string
}

func NewTextView(title, body string) *TextView {
	t := &TextView{title: title, lines: wrap(body, Cols)}
	if len(t.lines) == 0 {
		t.lines = []string{"(empty)"}
	}
	t.cur.setLen(len(t.lines))
	return t
}

func (t *TextView) Title() string { return t.title }
func (t *TextView) Hint() string  { return t.hint }

func (t *TextView) Render(fb *Framebuffer, _ *App) {
	// Text scrolls as a block, so no row is highlighted.
	for r := 0; r < bodyRows; r++ {
		i := t.cur.first + r
		if i >= len(t.lines) {
			break
		}
		fb.Text(0, bodyPxTop+r*LineH, t.lines[i])
	}
	t.cur.drawScrollbar(fb)
}

func (t *TextView) Handle(b Button, _ *App) Action {
	switch b {
	case BtnUp:
		if t.cur.first > 0 {
			t.cur.first--
		}
	case BtnDown:
		if t.cur.first < len(t.lines)-bodyRows {
			t.cur.first++
		}
	case BtnBack, BtnConfirm:
		return ActPop
	case BtnHome:
		return ActHome
	}
	return ActNone
}
