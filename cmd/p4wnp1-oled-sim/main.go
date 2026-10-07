// p4wnp1-oled-sim runs the OLED interface in a browser, with no Raspberry Pi,
// no OLED HAT and no SD card.
//
// The UI, the fonts, the menus and the state machine are the same code the
// device runs; only the panel and the joystick are swapped for a PNG and some
// buttons. That is the point of keeping Display and Input as interfaces: the
// simulator is not a mock-up of the interface, it IS the interface.
//
// By default it drives a fake device with plausible contents. Point --url at a
// real service with --token and it drives that instead.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"image/png"
	"log"
	"net/http"
	"sync"

	"github.com/mame82/P4wnP1_aloa/oled"
)

func main() {
	var (
		addr  = flag.String("listen", "127.0.0.1:8088", "address to serve on")
		url   = flag.String("url", "", "drive a REAL device at this base URL instead of the fake one")
		token = flag.String("token", oled.DefaultTokenPath, "credential file, when --url is set")
		scale = flag.Int("scale", 5, "pixel scale")
	)
	flag.Parse()
	log.SetFlags(0)

	var client oled.Client = oled.NewFakeClient()
	what := "a fake device"
	if *url != "" {
		client = oled.NewAPIClient(*url, *token)
		what = *url
	}

	s := &sim{
		app:   oled.NewApp(client, oled.NewRoot()),
		fb:    oled.NewFramebuffer(),
		scale: *scale,
	}
	s.app.Refresh()

	http.HandleFunc("/", s.page)
	http.HandleFunc("/screen.png", s.screen)
	http.HandleFunc("/press", s.press)

	fmt.Printf("\n  P4wnP1 OLED simulator -- driving %s\n", what)
	fmt.Printf("  http://%s\n\n", *addr)
	fmt.Printf("  Arrow keys move, Enter or Space selects, Backspace goes back.\n")
	fmt.Printf("  1 2 3 are the three keys down the left edge of the HAT.\n\n")
	log.Fatal(http.ListenAndServe(*addr, nil))
}

type sim struct {
	mu    sync.Mutex
	app   *oled.App
	fb    *oled.Framebuffer
	scale int
}

func (s *sim) screen(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.app.Render(s.fb)
	img := s.fb.Image(s.scale)
	s.mu.Unlock()

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(buf.Bytes())
}

var buttons = map[string]oled.Button{
	"up": oled.BtnUp, "down": oled.BtnDown, "left": oled.BtnLeft,
	"right": oled.BtnRight, "press": oled.BtnPress,
	"key1": oled.BtnKey1, "key2": oled.BtnKey2, "key3": oled.BtnKey3,
}

func (s *sim) press(w http.ResponseWriter, r *http.Request) {
	b, ok := buttons[r.URL.Query().Get("b")]
	if !ok {
		http.Error(w, "unknown button", 400)
		return
	}
	s.mu.Lock()
	s.app.Handle(b)
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}

func (s *sim) page(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(pageHTML))
}

// The page deliberately mirrors the physical HAT: the panel on top, the
// five-way stick bottom-left where your right thumb finds it, the three keys
// stacked down the left edge as they are on the board. Muscle memory built
// here should transfer to the hardware.
const pageHTML = `<!doctype html>
<html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>P4wnP1 OLED</title>
<style>
  :root{
    --paper:#F3F1EC; --ink:#17150F; --muted:#6B6558;
    --brass:#7A5C1E; --line:#D9D4C7; --panel:#0A0B0E;
  }
  :root:not([data-theme="light"]){ @media (prefers-color-scheme: dark){
    --paper:#000; --ink:#F3F1EC; --muted:#9A948A; --line:#23211C;
  }}
  *{box-sizing:border-box}
  body{margin:0;background:var(--paper);color:var(--ink);
       font:15px/1.5 Inter,-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif;
       display:flex;min-height:100vh;align-items:center;justify-content:center;padding:24px}
  .wrap{max-width:560px;width:100%}
  h1{font:600 20px/1.2 Inter,sans-serif;margin:0 0 2px}
  .sub{color:var(--muted);font-size:13px;margin:0 0 20px}
  .screen{background:var(--panel);border:1px solid var(--line);border-radius:10px;
          padding:14px;display:flex;justify-content:center}
  img{display:block;image-rendering:pixelated;max-width:100%;height:auto}
  .pad{display:grid;grid-template-columns:repeat(3,56px);grid-template-rows:repeat(3,44px);
       gap:8px;justify-content:center;margin:20px 0 0}
  .keys{display:flex;gap:8px;justify-content:center;margin-top:12px}
  button{font:inherit;font-size:13px;border:1px solid var(--line);background:transparent;
         color:var(--ink);border-radius:8px;cursor:pointer;padding:0 10px;min-height:40px}
  button:hover{border-color:var(--brass);color:var(--brass)}
  button:active{background:var(--brass);color:var(--paper);border-color:var(--brass)}
  button:focus-visible{outline:2px solid var(--brass);outline-offset:2px}
  .c{grid-column:2}
  .hint{color:var(--muted);font-size:12.5px;margin-top:18px;text-align:center}
  kbd{font:12px ui-monospace,monospace;border:1px solid var(--line);border-bottom-width:2px;
      border-radius:4px;padding:1px 5px;color:var(--ink)}
</style></head><body><div class="wrap">
  <h1>P4wnP1 OLED</h1>
  <p class="sub">Waveshare 1.3&quot; HAT &mdash; the same UI the device runs.</p>
  <div class="screen"><img id="s" src="/screen.png" alt="OLED screen"></div>

  <div class="pad">
    <div></div><button class="c" data-b="up">&uarr;</button><div></div>
    <button data-b="left">&larr;</button>
    <button data-b="press">PRESS</button>
    <button data-b="right">&rarr;</button>
    <div></div><button class="c" data-b="down">&darr;</button><div></div>
  </div>
  <div class="keys">
    <button data-b="key1">KEY1</button>
    <button data-b="key2">KEY2</button>
    <button data-b="key3">KEY3</button>
  </div>

  <p class="hint">
    <kbd>&larr;&uarr;&rarr;&darr;</kbd> move &middot; <kbd>Enter</kbd> select &middot;
    <kbd>Backspace</kbd> back &middot; <kbd>1</kbd> <kbd>2</kbd> <kbd>3</kbd> keys
  </p>
</div>
<script>
const img = document.getElementById('s');
// Cache-bust every refresh: the browser must never reuse a frame, or the
// screen appears frozen while the UI is in fact responding.
function refresh(){ img.src = '/screen.png?t=' + Date.now(); }
async function press(b){ await fetch('/press?b=' + b, {method:'POST'}); refresh(); }
document.querySelectorAll('button[data-b]').forEach(el =>
  el.addEventListener('click', () => press(el.dataset.b)));
const keymap = {
  ArrowUp:'up', ArrowDown:'down', ArrowLeft:'left', ArrowRight:'right',
  Enter:'press', ' ':'press', Backspace:'left',
  '1':'key1', '2':'key2', '3':'key3',
};
addEventListener('keydown', e => {
  const b = keymap[e.key];
  if (!b) return;
  e.preventDefault();
  press(b);
});
// Poll slowly so a toast expiring or a background refresh shows up without
// the user touching anything.
setInterval(refresh, 1000);
</script></body></html>`
