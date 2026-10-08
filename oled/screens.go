package oled

import (
	"fmt"
	"strings"
)

// The screen tree.
//
// Most of the 82-method API is the same shape -- list what is stored under a
// category, then deploy or delete one -- so StoredList covers USB, WiFi,
// network, Bluetooth, reflex sets, loadouts and backups with one screen rather
// than seven. What is left is the handful of things that genuinely need their
// own shape: the dashboard, the USB function toggles, running jobs, and the
// system actions that reboot the box.

// NewRoot builds the top-level menu.
func NewRoot() *Menu {
	m := NewMenu("P4wnP1", []MenuItem{
		{"Status", func(*App) View { return NewStatusView() }},
		{"Loadouts", func(*App) View { return NewStoredList(KindMasterTemplate) }},
		{"Cable (USB)", func(*App) View { return NewUSBView() }},
		{"Payloads", func(*App) View { return NewPayloadList() }},
		{"Radio (WiFi/BT)", func(*App) View { return NewRadioMenu() }},
		{"Network", func(*App) View { return NewStoredList(KindEthernetSettings) }},
		{"Reflexes", func(*App) View { return NewStoredList(KindTriggerActionSet) }},
		{"Jobs", func(*App) View { return NewJobsView() }},
		{"System", func(*App) View { return NewSystemMenu() }},
	})
	m.hint = "KEY3 home"
	return m
}

// --- status -----------------------------------------------------------------

// StatusView is the dashboard: what the cable presents, what addresses exist,
// what the radio is doing, how many reflexes are armed.
type StatusView struct {
	st     Status
	err    string
	loaded bool
	cur    cursor
}

func NewStatusView() *StatusView { return &StatusView{} }

func (s *StatusView) Title() string { return "Status" }
func (s *StatusView) Hint() string  { return "KEY2 refresh" }

func (s *StatusView) Refresh(app *App) {
	st, err := app.Client.Status()
	s.loaded = true
	if err != nil {
		s.err = err.Error()
		return
	}
	s.err = ""
	s.st = st
	s.cur.setLen(len(s.lines()))
}

// lines flattens the status into display rows, so scrolling is trivial and the
// layout is one list rather than hand-placed fields.
func (s *StatusView) lines() []string {
	var l []string
	l = append(l, "USB  "+s.st.USBHost)
	if len(s.st.USBFunctions) > 0 {
		l = append(l, "  "+strings.Join(s.st.USBFunctions, ","))
	}
	for _, i := range s.st.Interfaces {
		ip := i.IP
		if ip == "" {
			ip = "-"
		}
		l = append(l, fmt.Sprintf("%s %s", Pad(Truncate(i.Name, 6), 6), ip))
	}
	l = append(l, "WiFi "+s.st.WiFi)
	l = append(l, fmt.Sprintf("Reflex %d/%d armed", s.st.ReflexesArmed, s.st.Reflexes))
	l = append(l, fmt.Sprintf("Jobs %d running", s.st.RunningJobs))
	if s.st.Err != "" {
		l = append(l, "! "+s.st.Err)
	}
	return l
}

func (s *StatusView) Render(fb *Framebuffer, _ *App) {
	if !s.loaded {
		fb.Text(0, bodyPxTop+LineH, "  reading...")
		return
	}
	if s.err != "" {
		y := bodyPxTop
		for _, line := range wrap(s.err, Cols) {
			fb.Text(0, y, line)
			y += LineH
		}
		return
	}
	lines := s.lines()
	for r := 0; r < bodyRows; r++ {
		i := s.cur.first + r
		if i >= len(lines) {
			break
		}
		fb.Text(0, bodyPxTop+r*LineH, Truncate(lines[i], Cols))
	}
	s.cur.drawScrollbar(fb)
}

func (s *StatusView) Handle(b Button, app *App) Action {
	switch b {
	case BtnUp:
		if s.cur.first > 0 {
			s.cur.first--
		}
	case BtnDown:
		if s.cur.first < len(s.lines())-bodyRows {
			s.cur.first++
		}
	case BtnBack:
		return ActPop
	case BtnHome:
		return ActHome
	}
	return ActNone
}

// --- generic stored-item list ------------------------------------------------

// StoredList lists what is stored under one Kind and acts on a selection.
type StoredList struct {
	kind   Kind
	names  []string
	err    string
	loaded bool
	cur    cursor
}

func NewStoredList(k Kind) *StoredList { return &StoredList{kind: k} }

func (l *StoredList) Title() string { return l.kind.Label() }

func (l *StoredList) Hint() string {
	if l.kind.Deletable() {
		return "KEY1 delete"
	}
	return "KEY2 refresh"
}

func (l *StoredList) Refresh(app *App) {
	names, err := app.Client.List(l.kind)
	l.loaded = true
	if err != nil {
		l.err = err.Error()
		l.names = nil
		l.cur.setLen(0)
		return
	}
	l.err = ""
	l.names = names
	l.cur.setLen(len(names))
}

func (l *StoredList) Render(fb *Framebuffer, _ *App) {
	switch {
	case !l.loaded:
		fb.Text(0, bodyPxTop+LineH, "  reading...")
	case l.err != "":
		y := bodyPxTop
		for _, line := range wrap(l.err, Cols) {
			fb.Text(0, y, line)
			y += LineH
		}
	case len(l.names) == 0:
		fb.Text(0, bodyPxTop+LineH, "  nothing stored")
	default:
		l.cur.drawRows(fb, func(i int) string { return l.names[i] })
	}
}

func (l *StoredList) Handle(b Button, app *App) Action {
	switch b {
	case BtnUp:
		l.cur.move(-1)
	case BtnDown:
		l.cur.move(1)
	case BtnBack:
		return ActPop
	case BtnHome:
		return ActHome
	case BtnEnter, BtnConfirm:
		if len(l.names) == 0 {
			return ActNone
		}
		app.Push(l.actionsFor(l.names[l.cur.sel]))
	case BtnAction:
		if len(l.names) == 0 || !l.kind.Deletable() {
			return ActNone
		}
		name := l.names[l.cur.sel]
		app.Push(NewConfirm("Delete", "Delete "+Truncate(name, 14)+"?",
			"This removes the stored copy. Anything already deployed keeps running.",
			func(a *App) {
				if err := a.Client.DeleteStored(l.kind, name); err != nil {
					a.Toast("%s", Truncate(err.Error(), Cols-1))
					return
				}
				a.Toast("deleted")
				l.Refresh(a)
			}))
	}
	return ActNone
}

// actionsFor is what you can do with one stored item.
func (l *StoredList) actionsFor(name string) View {
	items := []MenuItem{}
	if l.kind.Deployable() {
		items = append(items, MenuItem{"Deploy now", func(a *App) View {
			return NewConfirm("Deploy", "Deploy "+Truncate(name, 13)+"?",
				deployWarning(l.kind), func(a *App) {
					if err := a.Client.DeployStored(l.kind, name); err != nil {
						a.Push(NewTextView("Failed", err.Error()))
						return
					}
					// Land on Status, not on a toast.
					//
					// Deploying the stored WiFi config called "startup" and
					// being told "deployed" is not an answer to "did the
					// access point come up?". The first thing the operator
					// did on hardware was deploy it and then ask what had
					// happened -- fairly, because the only evidence was a
					// word that disappeared after three seconds. Status
					// reads the device back: interfaces, addresses, the
					// radio's actual mode and SSID.
					a.Toast("deployed %s", Truncate(name, 10))
					a.Push(NewStatusView())
				})
		}})
	}
	if l.kind == KindMasterTemplate {
		items = append(items, MenuItem{"Use at boot", func(a *App) View {
			return NewConfirm("Boot loadout", "Load "+Truncate(name, 12)+" at boot?",
				"Replaces whatever runs at startup now.", func(a *App) {
					if err := a.Client.SetStartupTemplate(name); err != nil {
						a.Toast("%s", Truncate(err.Error(), Cols-1))
						return
					}
					// Say plainly that nothing changes yet, because nothing
					// visibly does and the natural reading of a success
					// message is that it already took effect.
					a.Push(NewTextView("Boot loadout",
						"Saved. "+name+" will be loaded the next time the "+
							"device boots. Nothing has changed right now -- "+
							"use Deploy now to apply it immediately."))
				})
		}})
	}
	if l.kind.Deletable() {
		items = append(items, MenuItem{"Delete", func(a *App) View {
			return NewConfirm("Delete", "Delete "+Truncate(name, 14)+"?",
				"Removes the stored copy only.", func(a *App) {
					if err := a.Client.DeleteStored(l.kind, name); err != nil {
						a.Toast("%s", Truncate(err.Error(), Cols-1))
						return
					}
					a.Toast("deleted")
					l.Refresh(a)
					a.Pop()
				})
		}})
	}
	m := NewMenu(Truncate(name, Cols-1), items)
	return m
}

// deployWarning says what the operator is about to change. Deploying a network
// or WiFi config can drop the very link the console is reached over, and on a
// screen you are holding that is worth stating before it happens.
func deployWarning(k Kind) string {
	switch k {
	case KindWifiSettings:
		return "The access point or client link may drop."
	case KindEthernetSettings:
		return "The network link you are using may drop."
	case KindUSBSettings, KindMasterTemplate:
		return "The USB connection to the host will re-enumerate."
	case KindDBBackup:
		return "This REPLACES the current datastore."
	}
	return ""
}

// --- USB composition ---------------------------------------------------------

// USBView toggles the functions the cable presents, then deploys them as one
// change. Batched rather than per-toggle because every deploy re-enumerates
// the USB device, and re-enumerating six times to turn on six functions is
// both slow and conspicuous.
type USBView struct {
	toggles []Toggle
	err     string
	loaded  bool
	dirty   bool
	cur     cursor
}

func NewUSBView() *USBView { return &USBView{} }

func (u *USBView) Title() string { return "Cable" }

func (u *USBView) Hint() string {
	if u.dirty {
		return "KEY1 deploy changes"
	}
	return "press toggles"
}

func (u *USBView) Refresh(app *App) {
	t, err := app.Client.USBFunctions()
	u.loaded = true
	if err != nil {
		u.err = err.Error()
		return
	}
	u.err = ""
	u.toggles = t
	u.dirty = false
	u.cur.setLen(len(t))
}

func (u *USBView) Render(fb *Framebuffer, _ *App) {
	switch {
	case !u.loaded:
		fb.Text(0, bodyPxTop+LineH, "  reading...")
	case u.err != "":
		y := bodyPxTop
		for _, line := range wrap(u.err, Cols) {
			fb.Text(0, y, line)
			y += LineH
		}
	default:
		u.cur.drawRows(fb, func(i int) string {
			box := "[ ] "
			if u.toggles[i].On {
				box = "[x] "
			}
			return box + u.toggles[i].Label
		})
	}
}

func (u *USBView) Handle(b Button, app *App) Action {
	switch b {
	case BtnUp:
		u.cur.move(-1)
	case BtnDown:
		u.cur.move(1)
	case BtnEnter, BtnConfirm:
		if len(u.toggles) == 0 {
			return ActNone
		}
		u.toggles[u.cur.sel].On = !u.toggles[u.cur.sel].On
		u.dirty = true
	case BtnAction:
		if !u.dirty {
			app.Toast("no changes")
			return ActNone
		}
		app.Push(NewConfirm("Deploy", "Apply USB changes?",
			"The host will see the device disconnect and come back.",
			func(a *App) {
				on := map[string]bool{}
				for _, t := range u.toggles {
					on[t.Key] = t.On
				}
				if err := a.Client.SetUSBFunctions(on); err != nil {
					a.Push(NewTextView("Failed", err.Error()))
					return
				}
				a.Toast("deployed")
				u.Refresh(a)
			}))
	case BtnBack:
		return ActPop
	case BtnHome:
		return ActHome
	}
	return ActNone
}

// --- payloads ----------------------------------------------------------------

// PayloadList runs stored HIDScripts against the attached host.
type PayloadList struct {
	names  []string
	err    string
	loaded bool
	cur    cursor
}

func NewPayloadList() *PayloadList { return &PayloadList{} }

func (p *PayloadList) Title() string { return "Payloads" }
func (p *PayloadList) Hint() string  { return "KEY1 = run in bg" }

func (p *PayloadList) Refresh(app *App) {
	names, err := app.Client.List(KindHIDScript)
	p.loaded = true
	if err != nil {
		p.err = err.Error()
		return
	}
	p.err = ""
	p.names = names
	p.cur.setLen(len(names))
}

func (p *PayloadList) Render(fb *Framebuffer, _ *App) {
	switch {
	case !p.loaded:
		fb.Text(0, bodyPxTop+LineH, "  reading...")
	case p.err != "":
		y := bodyPxTop
		for _, line := range wrap(p.err, Cols) {
			fb.Text(0, y, line)
			y += LineH
		}
	case len(p.names) == 0:
		fb.Text(0, bodyPxTop+LineH, "  no payloads stored")
	default:
		p.cur.drawRows(fb, func(i int) string { return p.names[i] })
	}
}

func (p *PayloadList) run(app *App, name string, background bool) {
	what := "Run"
	detail := "Types into the attached host. Make sure the keyboard function is deployed."
	if background {
		what = "Run (bg)"
		detail = "Starts it and returns here. Watch it under Jobs."
	}
	app.Push(NewConfirm(what, "Run "+Truncate(name, 14)+"?", detail, func(a *App) {
		out, err := a.Client.RunHIDScript(name, background)
		if err != nil {
			a.Push(NewTextView("Failed", err.Error()))
			return
		}
		if background {
			a.Toast("%s", Truncate(out, Cols-1))
			return
		}
		a.Push(NewTextView("Result", out))
	}))
}

func (p *PayloadList) Handle(b Button, app *App) Action {
	switch b {
	case BtnUp:
		p.cur.move(-1)
	case BtnDown:
		p.cur.move(1)
	case BtnEnter, BtnConfirm:
		if len(p.names) > 0 {
			p.run(app, p.names[p.cur.sel], false)
		}
	case BtnAction:
		if len(p.names) > 0 {
			p.run(app, p.names[p.cur.sel], true)
		}
	case BtnBack:
		return ActPop
	case BtnHome:
		return ActHome
	}
	return ActNone
}

// --- jobs --------------------------------------------------------------------

// JobsView lists running HIDScript jobs and can stop them all. A payload that
// is mid-type into someone's machine is the thing you most urgently want a
// physical control for.
type JobsView struct {
	jobs   []Job
	err    string
	loaded bool
	cur    cursor
}

func NewJobsView() *JobsView { return &JobsView{} }

func (j *JobsView) Title() string { return "Jobs" }
func (j *JobsView) Hint() string  { return "KEY1 stop all" }

func (j *JobsView) Refresh(app *App) {
	jobs, err := app.Client.RunningJobs()
	j.loaded = true
	if err != nil {
		j.err = err.Error()
		return
	}
	j.err = ""
	j.jobs = jobs
	j.cur.setLen(len(jobs))

	// Label only what fits on the panel. Each one is another round trip to
	// the service, and a Pi Zero W polling eight of them every five seconds
	// to fill rows nobody can see is a poor trade.
	for i := range j.jobs {
		if i >= bodyRows {
			break
		}
		j.jobs[i].Name = app.Client.DescribeJob(j.jobs[i].ID)
	}
}

func (j *JobsView) Render(fb *Framebuffer, _ *App) {
	switch {
	case !j.loaded:
		fb.Text(0, bodyPxTop+LineH, "  reading...")
	case j.err != "":
		y := bodyPxTop
		for _, line := range wrap(j.err, Cols) {
			fb.Text(0, y, line)
			y += LineH
		}
	case len(j.jobs) == 0:
		fb.Text(0, bodyPxTop+LineH, "  nothing running")
	default:
		j.cur.drawRows(fb, func(i int) string {
			if j.jobs[i].Name == "" {
				return fmt.Sprintf("job %d", j.jobs[i].ID)
			}
			return fmt.Sprintf("%d %s", j.jobs[i].ID, j.jobs[i].Name)
		})
	}
}

func (j *JobsView) Handle(b Button, app *App) Action {
	switch b {
	case BtnUp:
		j.cur.move(-1)
	case BtnDown:
		j.cur.move(1)
	case BtnAction:
		if len(j.jobs) == 0 {
			app.Toast("nothing running")
			return ActNone
		}
		app.Push(NewConfirm("Stop all", "Stop every running job?",
			"Anything mid-type stops where it is.", func(a *App) {
				if err := a.Client.CancelAllJobs(); err != nil {
					a.Toast("%s", Truncate(err.Error(), Cols-1))
					return
				}
				a.Toast("stopped")
				j.Refresh(a)
			}))
	case BtnBack:
		return ActPop
	case BtnHome:
		return ActHome
	}
	return ActNone
}

// --- radio -------------------------------------------------------------------

func NewRadioMenu() *Menu {
	m := NewMenu("Radio", []MenuItem{
		{"WiFi configs", func(*App) View { return NewStoredList(KindWifiSettings) }},
		{"Bluetooth configs", func(*App) View { return NewStoredList(KindBluetoothSettings) }},
	})
	m.hint = "press to open"
	return m
}

// --- system ------------------------------------------------------------------

func NewSystemMenu() *Menu {
	m := NewMenu("System", []MenuItem{
		{"Backups", func(*App) View { return NewStoredList(KindDBBackup) }},
		{"LED: 1 blink", func(a *App) View { return ledItem(a, 1) }},
		{"LED: 2 blinks", func(a *App) View { return ledItem(a, 2) }},
		{"LED: off", func(a *App) View { return ledItem(a, 0) }},
		{"Reboot", func(*App) View {
			return NewConfirm("Reboot", "Reboot the device?",
				"The USB connection and the access point both drop.", func(a *App) {
					if err := a.Client.Reboot(); err != nil {
						a.Toast("%s", Truncate(err.Error(), Cols-1))
						return
					}
					a.Toast("rebooting")
				})
		}},
		{"Shut down", func(*App) View {
			return NewConfirm("Shut down", "Shut down the device?",
				"Wait for the LED to stop before pulling the cable.", func(a *App) {
					if err := a.Client.Shutdown(); err != nil {
						a.Toast("%s", Truncate(err.Error(), Cols-1))
						return
					}
					a.Toast("shutting down")
				})
		}},
		{"Buttons test", func(*App) View { return NewButtonTest() }},
		{"About", func(*App) View {
			return NewTextView("About", "P4wnP1 A.L.O.A. on-device console. "+
				"Up/down move, right or press enters, left goes back. "+
				"KEY1 is the action named on the bottom line, KEY2 refreshes, "+
				"KEY3 returns to the root. Every key always answers: if it has "+
				"nothing to do on a screen it says so. "+
				"The HAT uses GPIO 5,6,13,16,19,20,21 for its controls and "+
				"24,25 for the panel -- a reflex set that drives any of those "+
				"as an output will fight this screen. "+
				"Buttons test under System shows the live pin state.")
		}},
	})
	m.hint = "KEY3 home"
	return m
}

func ledItem(a *App, n int) View {
	if err := a.Client.SetLEDBlink(n); err != nil {
		a.Toast("%s", Truncate(err.Error(), Cols-1))
		return nil
	}
	a.Toast("LED set")
	return nil
}
