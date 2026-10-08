package oled

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Mirroring the panel off the device.
//
// The daemon owns the framebuffer and the buttons; the web console talks to
// the service, which is a different process. This is the bridge: a unix
// socket the daemon listens on and the service proxies. A socket rather than
// a file because both directions matter -- the console reads frames AND sends
// presses -- and a shared file gives you a race on every write.
//
// WHAT THIS CHANGES ABOUT THE DEVICE, STATED PLAINLY.
//
// The panel is a PHYSICAL control surface. Mirroring it means anyone holding
// a console session can drive it: walk the menus, deploy a loadout, reboot
// the box. That is the point of the feature and it is not free. The session
// is already enough to do all of those things through the API directly, so
// the surface does not widen -- but the panel stops being the thing that
// needs you in the room, and that is worth knowing before you enable it.
//
// One screen is never mirrored, and that exception is load-bearing. The
// first-boot credentials screen exists so a secret can be shown to whoever is
// STANDING OVER THE DEVICE and then destroyed. Streaming it to a browser
// would hand that secret to anyone with a session and silently undo the whole
// property. So a view that declares itself secret is served as a refusal, not
// as pixels.

// MirrorSocket is where the daemon listens. Under /run because it is runtime
// state on tmpfs, beside the credential the daemon already uses.
const MirrorSocket = "/run/p4wnp1/panel.sock"

// Secret is implemented by a view whose contents must never leave the device.
type Secret interface{ Secret() bool }

// ShowingSecret reports whether what is on screen right now must not be
// mirrored. Only the top view matters: it is the only one being rendered.
func (a *App) ShowingSecret() bool {
	s, ok := a.Top().(Secret)
	return ok && s.Secret()
}

// Mirror serves the panel over a unix socket.
type Mirror struct {
	// Frame returns a copy of the current framebuffer bytes, or nil if the
	// screen must not be mirrored right now.
	Frame func() []byte
	// Text returns the same screen read back as text, or "" if withheld.
	Text func() string
	// Press injects a button. Returns false if it was dropped.
	Press func(Button) bool

	ln   net.Listener
	once sync.Once
}

// Listen starts the socket. The caller closes it.
//
// Mode 0600: the socket is as privileged as the panel, and the only client
// is the service, which runs as root on the same box.
func (m *Mirror) Listen(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	// A socket left behind by a killed daemon would make Listen fail with
	// "address already in use" forever.
	_ = os.Remove(path)
	ln, err := net.Listen("unix", path)
	if err != nil {
		return err
	}
	if err := os.Chmod(path, 0600); err != nil {
		ln.Close()
		return err
	}
	m.ln = ln
	go m.serve()
	return nil
}

func (m *Mirror) Close() error {
	var err error
	m.once.Do(func() {
		if m.ln != nil {
			err = m.ln.Close()
		}
	})
	return err
}

func (m *Mirror) serve() {
	for {
		c, err := m.ln.Accept()
		if err != nil {
			return // listener closed
		}
		go m.handle(c)
	}
}

// The protocol is one line in, one answer out, then the connection closes.
//
//	FRAME  -> "OK <n>\n" followed by n raw framebuffer bytes, or "NO <why>"
//	TEXT   -> "OK <n>\n" followed by n bytes of text,           or "NO <why>"
//	PRESS <button> -> "OK" / "ERR <why>"
//
// Deliberately trivial. This carries a 1KB bitmap between two processes on
// the same box; anything with a schema and a handshake would be more code
// than the thing it transports.
func (m *Mirror) handle(c net.Conn) {
	defer c.Close()
	r := bufio.NewReader(c)
	line, err := r.ReadString('\n')
	if err != nil {
		return
	}
	fields := strings.Fields(strings.TrimSpace(line))
	if len(fields) == 0 {
		fmt.Fprintf(c, "ERR empty\n")
		return
	}

	switch strings.ToUpper(fields[0]) {
	case "FRAME":
		b := m.Frame()
		if b == nil {
			fmt.Fprintf(c, "NO this screen is not mirrored\n")
			return
		}
		fmt.Fprintf(c, "OK %d\n", len(b))
		_, _ = c.Write(b)
	case "TEXT":
		t := m.Text()
		if t == "" {
			fmt.Fprintf(c, "NO this screen is not mirrored\n")
			return
		}
		fmt.Fprintf(c, "OK %d\n", len(t))
		_, _ = c.Write([]byte(t))
	case "PRESS":
		if len(fields) < 2 {
			fmt.Fprintf(c, "ERR which button\n")
			return
		}
		b, ok := ParseButton(fields[1])
		if !ok {
			fmt.Fprintf(c, "ERR no such button %q\n", fields[1])
			return
		}
		if !m.Press(b) {
			fmt.Fprintf(c, "ERR dropped (ui busy)\n")
			return
		}
		fmt.Fprintf(c, "OK\n")
	default:
		fmt.Fprintf(c, "ERR unknown command\n")
	}
}

// ParseButton maps a name to a control. The names are the ones Button.String
// produces, so the two cannot drift.
func ParseButton(s string) (Button, bool) {
	for _, b := range AllButtons {
		if strings.EqualFold(b.String(), s) {
			return b, true
		}
	}
	return BtnNone, false
}

// PanelSource is the last frame the UI drew, ready to be served.
//
// It lives here, not in the daemon's main package, for one reason: the guard
// that keeps the credentials screen off the mirror is in Publish, and while
// that guard sat in main.go nothing could test it. The mirror tests were
// passing against a copy of the logic written in the test file -- they
// proved the test's wiring was right and said nothing at all about the
// daemon's. A guard whose failure mode is "a password reaches a browser"
// has to be covered by the thing that actually runs.
type PanelSource struct {
	mu    sync.Mutex
	frame []byte
}

// Publish records the frame just drawn. Called on every repaint, so it does
// one 1024-byte copy and nothing else -- reading the screen back to text
// costs about twice a full render and is done on demand instead.
//
// A screen that declares itself secret publishes NOTHING, and clears
// whatever was there before it: a stale frame of the menu would be harmless,
// but a stale frame from one page earlier in the credentials flow would not.
func (p *PanelSource) Publish(app *App, fb *Framebuffer) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if app.ShowingSecret() {
		p.frame = nil
		return
	}
	if p.frame == nil {
		p.frame = make([]byte, len(fb.Bytes()))
	}
	copy(p.frame, fb.Bytes())
}

// Frame returns a copy of the last published frame, or nil if there is none
// to give.
func (p *PanelSource) Frame() []byte {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.frame == nil {
		return nil
	}
	out := make([]byte, len(p.frame))
	copy(out, p.frame)
	return out
}

// Text reads the last published frame back as text.
//
// The copy happens under the lock and the scan outside it. Holding the UI's
// lock for the length of a ReadBack would make the panel in your hand
// stutter whenever someone remote was watching.
func (p *PanelSource) Text() string {
	buf := p.Frame()
	if buf == nil {
		return ""
	}
	fb := NewFramebuffer()
	if !fb.Load(buf) {
		return ""
	}
	return ReadBack(fb)
}
