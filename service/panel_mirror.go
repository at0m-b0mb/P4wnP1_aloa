package service

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/mame82/P4wnP1_aloa/oled"
)

// Mirroring the OLED panel into the web console.
//
// The panel daemon owns the framebuffer and the buttons and runs as its own
// process, so the service does not render anything here -- it is a proxy. It
// connects to the daemon's unix socket, asks for the current frame, and turns
// 1024 bytes of 1bpp page-layout data into a PNG.
//
// Both routes sit behind the same authentication and the same browser-origin
// guard as every RPC. A session that can reach these can already reboot the
// device through the API, so the reachable surface does not widen -- but the
// panel stops being the control that requires you to be in the room, and
// that is a real change, stated here and in the daemon.
//
// A screen the daemon declares secret -- the first-boot credentials -- is
// refused with 409 rather than served. That exception is the whole reason
// showing a password on the panel is safer than leaving it on the card.

// panelSocket is where the daemon listens. A variable so tests can point it
// at a temporary path.
var panelSocket = oled.MirrorSocket

// panelDialTimeout is short on purpose: the daemon is on the same box, and a
// console view that polls twice a second must fail fast when there is no
// panel rather than queue up.
const panelDialTimeout = 2 * time.Second

// panelAsk sends one command and returns the payload.
func panelAsk(cmd string) (payload []byte, err error) {
	c, err := net.DialTimeout("unix", panelSocket, panelDialTimeout)
	if err != nil {
		return nil, fmt.Errorf("no panel daemon")
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(panelDialTimeout))

	if _, err := fmt.Fprintf(c, "%s\n", cmd); err != nil {
		return nil, fmt.Errorf("no panel daemon")
	}
	r := bufio.NewReader(c)
	status, err := r.ReadString('\n')
	if err != nil {
		return nil, fmt.Errorf("no answer from the panel")
	}
	status = strings.TrimSpace(status)
	switch {
	case status == "OK":
		return nil, nil
	case strings.HasPrefix(status, "OK "):
		n, convErr := strconv.Atoi(strings.TrimPrefix(status, "OK "))
		if convErr != nil || n < 0 || n > 1<<20 {
			return nil, fmt.Errorf("the panel answered with a bad length")
		}
		buf := make([]byte, n)
		if _, err := io.ReadFull(r, buf); err != nil {
			return nil, fmt.Errorf("the panel answer was short")
		}
		return buf, nil
	case strings.HasPrefix(status, "NO "):
		return nil, errPanelWithheld{strings.TrimPrefix(status, "NO ")}
	default:
		return nil, fmt.Errorf("%s", strings.TrimPrefix(status, "ERR "))
	}
}

// errPanelWithheld is the daemon refusing to mirror what is on screen. A
// distinct type because it is a 409, not a 500: nothing is broken.
type errPanelWithheld struct{ why string }

func (e errPanelWithheld) Error() string { return e.why }

// panelPNG renders the daemon's 1bpp framebuffer.
//
// The buffer is in the CONTROLLER's layout -- Pages rows of Width bytes, each
// byte eight vertically stacked pixels with bit 0 at the top -- not in raster
// order. Reading it as rows of pixels produces a sheared mess that still
// looks plausibly like a screen, which is exactly the kind of wrong that
// survives review.
func panelPNG(buf []byte) ([]byte, error) {
	const w, h = oled.Width, oled.Height
	if len(buf) != w*h/8 {
		return nil, fmt.Errorf("expected %d bytes, got %d", w*h/8, len(buf))
	}
	// 1:1. The obvious thing is to scale up here so the browser gets
	// something bigger than a postage stamp, but a 4x image is sixteen
	// times the pixels to encode, and this encodes on a single-core Pi Zero
	// W every time a browser asks. The console scales it instead, with
	// image-rendering:pixelated, which is both free and sharper than
	// anything a resampler would give you for a 1bpp source.
	const s = 1
	img := image.NewRGBA(image.Rect(0, 0, w*s, h*s))
	// The real panel is a blue-white monochrome OLED on black.
	on := color.RGBA{0x7A, 0xD3, 0xFF, 0xFF}
	off := color.RGBA{0x05, 0x08, 0x0C, 0xFF}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := off
			if buf[(y/8)*w+x]&(1<<uint(y%8)) != 0 {
				c = on
			}
			for dy := 0; dy < s; dy++ {
				for dx := 0; dx < s; dx++ {
					img.Set(x*s+dx, y*s+dy, c)
				}
			}
		}
	}
	// BestSpeed, not BestCompression: this runs on a single-core Pi Zero W
	// and the console asks for a new one twice a second.
	var out bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := enc.Encode(&out, img); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func (a *apiHandler) handlePanelImage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		apiError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	/* Authenticate BEFORE touching the panel.
	 *
	 * This was missing. These three routes shipped with no auth and no
	 * origin guard at all, while every other handler on the same mux called
	 * authenticate() -- and the comment where they are registered claimed
	 * they had "Same auth and same origin guard as the RPCs", which made the
	 * hole read as a decision someone had already checked.
	 *
	 * What it exposed: panel.png and panel.txt handed the device's screen to
	 * anyone who could reach port 8000, and panel/press let them PRESS THE
	 * BUTTONS -- unauthenticated remote control of the physical console, on
	 * a box whose whole purpose is to be plugged into someone else's
	 * machine, over an AP that is deliberately broadcasting. The one saving
	 * grace was the secret guard in PanelSource.Publish, which withholds the
	 * first-boot credentials screen, so the password itself did not leak.
	 * That guard was never meant to be the only thing standing there.
	 */
	if _, ok := a.authenticate(w, r); !ok {
		return
	}
	buf, err := panelAsk("FRAME")
	if err != nil {
		a.panelErr(w, err)
		return
	}
	pngBytes, err := panelPNG(buf)
	if err != nil {
		apiError(w, http.StatusBadGateway, err.Error())
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(pngBytes)
}

func (a *apiHandler) handlePanelText(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		apiError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	if _, ok := a.authenticate(w, r); !ok {
		return
	}
	buf, err := panelAsk("TEXT")
	if err != nil {
		a.panelErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(buf)
}

func (a *apiHandler) handlePanelPress(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		apiError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	if _, ok := a.authenticate(w, r); !ok {
		return
	}
	var req struct {
		Button string `json:"button"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<12)).Decode(&req); err != nil {
		apiError(w, http.StatusBadRequest, "malformed request body")
		return
	}
	// Validated HERE as well as in the daemon, so a nonsense value never
	// reaches the socket and the error names the real problem.
	if _, ok := oled.ParseButton(req.Button); !ok {
		apiError(w, http.StatusBadRequest, "no such button: "+req.Button)
		return
	}
	if _, err := panelAsk("PRESS " + req.Button); err != nil {
		a.panelErr(w, err)
		return
	}
	apiJSON(w, http.StatusOK, map[string]string{"pressed": req.Button})
}

func (a *apiHandler) panelErr(w http.ResponseWriter, err error) {
	if wh, ok := err.(errPanelWithheld); ok {
		// Not a fault. The panel is deliberately not showing this remotely.
		apiError(w, http.StatusConflict, wh.why)
		return
	}
	// No daemon is the ordinary case on a board with no HAT, and on every
	// plain image. 503 so the console can say "no panel" rather than
	// "something is broken".
	apiError(w, http.StatusServiceUnavailable, err.Error())
}
