package oled

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// ask sends one command and returns the status line and payload.
func ask(t *testing.T, sock, cmd string) (string, []byte) {
	t.Helper()
	c, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()
	fmt.Fprintf(c, "%s\n", cmd)
	r := bufio.NewReader(c)
	status, err := r.ReadString('\n')
	if err != nil {
		t.Fatalf("read status: %v", err)
	}
	status = strings.TrimSpace(status)
	if !strings.HasPrefix(status, "OK ") {
		return status, nil
	}
	n, _ := strconv.Atoi(strings.TrimPrefix(status, "OK "))
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		t.Fatalf("read payload: %v", err)
	}
	return status, buf
}

// sockPath returns a SHORT path for a unix socket.
//
// Not t.TempDir(): a sockaddr_un holds 104 bytes on macOS and 108 on Linux,
// and the macOS temp directory alone is longer than that. The failure is
// `bind: invalid argument`, which says nothing about length.
func sockPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "p4oled")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return filepath.Join(dir, "p.sock")
}

// startMirror serves one app over a socket, through the REAL PanelSource.
//
// Not a reimplementation of it. The first version of this helper had its own
// copy of the "is a secret on screen" check, so the test that mattered most
// -- the one asserting a password never leaves the device -- was proving the
// test file was correct and saying nothing about the daemon.
func startMirror(t *testing.T, app *App, fb *Framebuffer) (*PanelSource, string, chan Button) {
	t.Helper()
	sock := sockPath(t)
	presses := make(chan Button, 8)
	src := &PanelSource{}
	m := &Mirror{
		Frame: src.Frame,
		Text:  src.Text,
		Press: func(b Button) bool {
			select {
			case presses <- b:
				return true
			default:
				return false
			}
		},
	}
	if err := m.Listen(sock); err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { m.Close() })
	return src, sock, presses
}

// paint is what the daemon's loop does on every repaint.
func paint(src *PanelSource, app *App, fb *Framebuffer) {
	app.Render(fb)
	src.Publish(app, fb)
}

func TestMirrorServesFrameAndText(t *testing.T) {
	app := NewApp(NewFakeClient(), NewRoot())
	fb := NewFramebuffer()
	src, sock, _ := startMirror(t, app, fb)
	paint(src, app, fb)

	status, buf := ask(t, sock, "FRAME")
	if !strings.HasPrefix(status, "OK ") {
		t.Fatalf("FRAME -> %q", status)
	}
	if len(buf) != Width*Height/8 {
		t.Errorf("frame is %d bytes, want %d", len(buf), Width*Height/8)
	}
	if allZero(buf) {
		t.Error("the frame is entirely blank")
	}

	status, text := ask(t, sock, "TEXT")
	if !strings.HasPrefix(status, "OK ") {
		t.Fatalf("TEXT -> %q", status)
	}
	for _, want := range []string{"P4wnP1", "Status", "Payloads"} {
		if !strings.Contains(string(text), want) {
			t.Errorf("the mirrored text does not mention %q:\n%s", want, text)
		}
	}
}

func allZero(b []byte) bool {
	for _, x := range b {
		if x != 0 {
			return false
		}
	}
	return true
}

// A remote press must be the SAME press as one on the board. If the two ever
// take different paths, remote control becomes a second UI with its own bugs.
func TestMirrorPressReachesTheUI(t *testing.T) {
	app := NewApp(NewFakeClient(), NewRoot())
	fb := NewFramebuffer()
	src, sock, presses := startMirror(t, app, fb)
	paint(src, app, fb)

	for _, name := range []string{"down", "key1", "KEY3", "press", "right"} {
		status, _ := ask(t, sock, "PRESS "+name)
		if status != "OK" {
			t.Errorf("PRESS %s -> %q", name, status)
			continue
		}
		select {
		case b := <-presses:
			if !strings.EqualFold(b.String(), name) {
				t.Errorf("PRESS %s delivered %s", name, b)
			}
		default:
			t.Errorf("PRESS %s delivered nothing", name)
		}
	}

	for _, bad := range []string{"", "banana", "f1"} {
		status, _ := ask(t, sock, "PRESS "+bad)
		if !strings.HasPrefix(status, "ERR") {
			t.Errorf("PRESS %q was accepted: %q", bad, status)
		}
	}
	if status, _ := ask(t, sock, "WAT"); !strings.HasPrefix(status, "ERR") {
		t.Errorf("an unknown command was accepted: %q", status)
	}
}

// THE one that matters.
//
// Showing a password on the panel is only safer than leaving it on the card
// because it reaches whoever is standing over the device and then stops
// existing. If the mirror streams that screen to a browser, the feature is
// not merely useless, it is worse than what it replaced.
func TestMirrorNeverServesTheCredentialsScreen(t *testing.T) {
	dir := t.TempDir()
	h := filepath.Join(dir, "creds")
	if err := os.WriteFile(h, []byte("web_user=admin\nweb_pass=SECRETPASSWORD99\nssh_user=p4wnp1\nssh_pass=SSHSECRET1234567\n"), 0600); err != nil {
		t.Fatal(err)
	}
	c := LoadFirstRunCreds(h)
	if c == nil {
		t.Fatal("handoff did not load")
	}

	app := NewApp(NewFakeClient(), NewRoot())
	fb := NewFramebuffer()
	src, sock, _ := startMirror(t, app, fb)

	// The menu first, so there is a published frame to go stale.
	paint(src, app, fb)
	if status, _ := ask(t, sock, "FRAME"); !strings.HasPrefix(status, "OK ") {
		t.Fatalf("the menu was not served: %q", status)
	}

	app.Push(NewFirstRunView(c))

	// Every page of it, including the ones holding the passwords.
	for page := 0; page < 4; page++ {
		paint(src, app, fb)
		if status, _ := ask(t, sock, "FRAME"); !strings.HasPrefix(status, "NO ") {
			t.Fatalf("page %d: FRAME was served while a secret was on screen: %q", page, status)
		}
		status, text := ask(t, sock, "TEXT")
		if !strings.HasPrefix(status, "NO ") {
			t.Fatalf("page %d: TEXT was served while a secret was on screen: %q", page, status)
		}
		if strings.Contains(string(text), "SECRET") {
			t.Fatalf("page %d: a password left the device", page)
		}
		app.Handle(BtnDown)
	}

	// Once erased there is nothing left to withhold, and a remote operator
	// should be able to see that it happened.
	app.Handle(BtnAction)
	app.Handle(BtnRight)
	app.Handle(BtnConfirm)
	paint(src, app, fb)
	status, text := ask(t, sock, "TEXT")
	if !strings.HasPrefix(status, "OK ") {
		t.Fatalf("after erasing, the panel is still withheld: %q", status)
	}
	if strings.Contains(string(text), "SECRETPASSWORD99") || strings.Contains(string(text), "SSHSECRET") {
		t.Fatalf("a password is still on the mirrored screen:\n%s", text)
	}
	if !strings.Contains(string(text), "Erased") {
		t.Errorf("the mirror does not show that the erase happened:\n%s", text)
	}
}

// A socket left behind by a killed daemon must not stop the next one.
func TestMirrorReclaimsAStaleSocket(t *testing.T) {
	sock := sockPath(t)
	if err := os.WriteFile(sock, []byte("stale"), 0600); err != nil {
		t.Fatal(err)
	}
	m := &Mirror{Frame: func() []byte { return nil }, Text: func() string { return "" },
		Press: func(Button) bool { return true }}
	if err := m.Listen(sock); err != nil {
		t.Fatalf("a stale socket file blocked the daemon: %v", err)
	}
	defer m.Close()
	fi, err := os.Stat(sock)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0600 {
		t.Errorf("socket mode is %o, want 600", fi.Mode().Perm())
	}
}
