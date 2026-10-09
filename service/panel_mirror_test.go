package service

import (
	"bytes"
	"context"
	"encoding/json"
	"image/png"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mame82/P4wnP1_aloa/oled"
	"github.com/mame82/P4wnP1_aloa/service/auth"
)

// fakePanel stands in for the daemon's socket.
func fakePanel(t *testing.T, frame []byte, text string, withheld bool) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "p4svc")
	if err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(dir, "p.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close(); os.RemoveAll(dir) })

	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				buf := make([]byte, 128)
				n, _ := c.Read(buf)
				cmd := strings.TrimSpace(string(buf[:n]))
				switch {
				case withheld:
					c.Write([]byte("NO this screen is not mirrored\n"))
				case cmd == "FRAME":
					c.Write([]byte("OK " + itoa(len(frame)) + "\n"))
					c.Write(frame)
				case cmd == "TEXT":
					c.Write([]byte("OK " + itoa(len(text)) + "\n"))
					c.Write([]byte(text))
				case strings.HasPrefix(cmd, "PRESS "):
					c.Write([]byte("OK\n"))
				default:
					c.Write([]byte("ERR unknown command\n"))
				}
			}(c)
		}
	}()
	return sock
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

func withPanelSocket(t *testing.T, sock string) {
	t.Helper()
	old := panelSocket
	panelSocket = sock
	t.Cleanup(func() { panelSocket = old })
}

// The framebuffer is in the CONTROLLER's layout -- pages of vertically
// stacked bits -- not raster order. Reading it as rows of pixels produces a
// sheared image that still looks like a screen, which is the kind of wrong
// that survives a glance. So this draws a shape at known coordinates and
// checks those exact pixels come back.
func TestPanelPNGMatchesTheFramebufferLayout(t *testing.T) {
	fb := oled.NewFramebuffer()
	// Three marks at coordinates whose page and bit differ.
	marks := [][2]int{{0, 0}, {5, 9}, {127, 63}}
	for _, m := range marks {
		fb.Set(m[0], m[1], true)
	}

	raw, err := panelPNG(fb.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("the service produced something that is not a PNG: %v", err)
	}
	b := img.Bounds()
	const s = 1
	if b.Dx() != oled.Width*s || b.Dy() != oled.Height*s {
		t.Fatalf("image is %dx%d, want %dx%d", b.Dx(), b.Dy(), oled.Width*s, oled.Height*s)
	}

	lit := func(x, y int) bool {
		r, g, bl, _ := img.At(x*s, y*s).RGBA()
		return r+g+bl > 0x8000
	}
	for _, m := range marks {
		if !lit(m[0], m[1]) {
			t.Errorf("pixel (%d,%d) is set in the framebuffer but dark in the PNG", m[0], m[1])
		}
	}
	// And the pixels either side of a mark must NOT be lit, which is what
	// catches a shear: a wrong layout lights something, just not this.
	for _, m := range [][2]int{{1, 0}, {5, 10}, {126, 63}} {
		if lit(m[0], m[1]) {
			t.Errorf("pixel (%d,%d) is clear in the framebuffer but lit in the PNG", m[0], m[1])
		}
	}

	if _, err := panelPNG([]byte{1, 2, 3}); err == nil {
		t.Error("a short framebuffer was accepted")
	}
}

// panelHandler returns an apiHandler with a real auth manager, and a request
// factory that carries a valid token.
//
// The previous version of this file did neither. It built `&apiHandler{}`
// with no auth manager and asserted the handlers answered 200 to a request
// with no token -- so the test did not merely miss the fact that these three
// routes had no authentication, it pinned that behaviour down as correct.
// Every run went green while /api/v1/panel.png served the device's screen,
// and /api/v1/panel/press let anyone who could reach port 8000 press its
// buttons, to the whole network.
func panelHandler(t *testing.T) (*apiHandler, func(method, path, body string) *http.Request) {
	t.Helper()
	store, err := auth.NewStore(filepath.Join(t.TempDir(), "auth.json"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	const user, pass = "admin", "correct-horse-battery-staple"
	if err := store.SetPassword(user, pass); err != nil {
		t.Fatalf("SetPassword: %v", err)
	}
	mgr := auth.NewManager(store, auth.NewSessions(), time.Hour)
	t.Cleanup(mgr.Close)
	sess, err := mgr.Login(context.Background(), user, pass)
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	a := &apiHandler{authMgr: mgr}
	authed := func(method, path, body string) *http.Request {
		var r *http.Request
		if body == "" {
			r = httptest.NewRequest(method, path, nil)
		} else {
			r = httptest.NewRequest(method, path, strings.NewReader(body))
		}
		// An IP literal cannot be DNS-rebound, so knownHost accepts it.
		r.Host = "172.16.0.1:8000"
		r.Header.Set(auth.HTTPAuthHeader, "Bearer "+sess.Token)
		return r
	}
	return a, authed
}

// TestPanelRoutesRefuseUnauthenticatedCallers is the test whose absence let
// the hole ship. It is deliberately separate from the behaviour tests: those
// can be rewritten freely, this one states the security property.
func TestPanelRoutesRefuseUnauthenticatedCallers(t *testing.T) {
	fb := oled.NewFramebuffer()
	fb.Text(0, 0, "HELLO")
	a, _ := panelHandler(t)

	for _, c := range []struct {
		name   string
		fn     func(http.ResponseWriter, *http.Request)
		method string
		path   string
		body   string
	}{
		{"panel.png", a.handlePanelImage, http.MethodGet, "/panel.png", ""},
		{"panel.txt", a.handlePanelText, http.MethodGet, "/panel.txt", ""},
		{"panel/press", a.handlePanelPress, http.MethodPost, "/panel/press", `{"button":"key1"}`},
	} {
		t.Run(c.name+" without a token", func(t *testing.T) {
			// A LIVE panel behind it: the refusal must come from the auth
			// check, not from there being nothing to serve. Without this the
			// test would pass against a device with no HAT and prove nothing.
			withPanelSocket(t, fakePanel(t, fb.Bytes(), "HELLO\n", false))
			var r *http.Request
			if c.body == "" {
				r = httptest.NewRequest(c.method, c.path, nil)
			} else {
				r = httptest.NewRequest(c.method, c.path, strings.NewReader(c.body))
			}
			r.Host = "172.16.0.1:8000"
			w := httptest.NewRecorder()
			c.fn(w, r)
			if w.Code != http.StatusUnauthorized {
				t.Fatalf("%s with no token -> %d, want 401. Body: %s",
					c.name, w.Code, w.Body.String())
			}
			if strings.Contains(w.Body.String(), "HELLO") {
				t.Fatalf("%s leaked panel content to an unauthenticated caller", c.name)
			}
		})

		t.Run(c.name+" with a junk token", func(t *testing.T) {
			withPanelSocket(t, fakePanel(t, fb.Bytes(), "HELLO\n", false))
			var r *http.Request
			if c.body == "" {
				r = httptest.NewRequest(c.method, c.path, nil)
			} else {
				r = httptest.NewRequest(c.method, c.path, strings.NewReader(c.body))
			}
			r.Host = "172.16.0.1:8000"
			r.Header.Set(auth.HTTPAuthHeader, "Bearer not-a-real-token")
			w := httptest.NewRecorder()
			c.fn(w, r)
			if w.Code != http.StatusUnauthorized {
				t.Fatalf("%s with a junk token -> %d, want 401", c.name, w.Code)
			}
		})
	}
}

func TestPanelRoutes(t *testing.T) {
	fb := oled.NewFramebuffer()
	fb.Text(0, 0, "HELLO")
	a, authed := panelHandler(t)

	t.Run("image", func(t *testing.T) {
		withPanelSocket(t, fakePanel(t, fb.Bytes(), "HELLO\n", false))
		w := httptest.NewRecorder()
		a.handlePanelImage(w, authed(http.MethodGet, "/panel.png", ""))
		if w.Code != 200 {
			t.Fatalf("code %d: %s", w.Code, w.Body.String())
		}
		if ct := w.Header().Get("Content-Type"); ct != "image/png" {
			t.Errorf("content type %q", ct)
		}
		if _, err := png.Decode(bytes.NewReader(w.Body.Bytes())); err != nil {
			t.Errorf("body is not a PNG: %v", err)
		}
	})

	t.Run("text", func(t *testing.T) {
		withPanelSocket(t, fakePanel(t, fb.Bytes(), "HELLO\n", false))
		w := httptest.NewRecorder()
		a.handlePanelText(w, authed(http.MethodGet, "/panel.txt", ""))
		if w.Code != 200 || !strings.Contains(w.Body.String(), "HELLO") {
			t.Errorf("code %d body %q", w.Code, w.Body.String())
		}
	})

	t.Run("press", func(t *testing.T) {
		withPanelSocket(t, fakePanel(t, nil, "", false))
		for _, b := range []string{"up", "key1", "press"} {
			w := httptest.NewRecorder()
			r := authed(http.MethodPost, "/panel/press", `{"button":"`+b+`"}`)
			a.handlePanelPress(w, r)
			if w.Code != 200 {
				t.Errorf("press %s -> %d %s", b, w.Code, w.Body.String())
			}
		}
		// A nonsense button must be refused BY THE SERVICE, so the error
		// names the real problem instead of whatever the socket says.
		w := httptest.NewRecorder()
		a.handlePanelPress(w, authed(http.MethodPost, "/panel/press", `{"button":"banana"}`))
		if w.Code != http.StatusBadRequest {
			t.Errorf("a nonsense button -> %d, want 400", w.Code)
		}
	})

	// A withheld screen is 409 CONFLICT, not 500. Nothing is broken; the
	// device is declining, and the console has to be able to tell those
	// apart to say something true to the operator.
	t.Run("withheld", func(t *testing.T) {
		withPanelSocket(t, fakePanel(t, fb.Bytes(), "secret", true))
		for _, h := range []struct {
			name string
			fn   func(http.ResponseWriter, *http.Request)
			req  *http.Request
		}{
			{"image", a.handlePanelImage, authed(http.MethodGet, "/panel.png", "")},
			{"text", a.handlePanelText, authed(http.MethodGet, "/panel.txt", "")},
		} {
			w := httptest.NewRecorder()
			h.fn(w, h.req)
			if w.Code != http.StatusConflict {
				t.Errorf("%s while withheld -> %d, want 409", h.name, w.Code)
			}
			if strings.Contains(w.Body.String(), "secret") {
				t.Errorf("%s leaked the withheld content", h.name)
			}
		}
	})

	// No daemon at all is the ordinary case on a plain image.
	t.Run("no daemon", func(t *testing.T) {
		withPanelSocket(t, "/tmp/definitely-not-a-socket-9f3a")
		w := httptest.NewRecorder()
		a.handlePanelImage(w, authed(http.MethodGet, "/panel.png", ""))
		if w.Code != http.StatusServiceUnavailable {
			t.Errorf("no daemon -> %d, want 503", w.Code)
		}
	})
}
