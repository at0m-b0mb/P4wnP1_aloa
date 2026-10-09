// p4wnp1-oled drives the Waveshare 1.3inch OLED HAT as a second way to
// control the device, alongside the web console.
//
// It authenticates with the machine-local credential the service writes to
// /run/p4wnp1/local.token, so it needs no password and no configuration: it
// is a local script like any other, holding an ordinary session.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/mame82/P4wnP1_aloa/oled"
)

var version = "dev" // set with -ldflags "-X main.version=..."

// envOr lets a flag default come from the environment, so the systemd unit
// can set it without anyone editing an ExecStart line.
func envOr(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && strings.TrimSpace(v) != "" {
		return v
	}
	return def
}

func main() {
	var (
		busName  = flag.String("bus", "spi", "panel bus: spi or i2c")
		ctrlName = flag.String("controller", "sh1106", "panel controller: sh1106 or ssd1306")
		spiPort  = flag.String("spi-port", "", "SPI port (default: first available)")
		i2cBus   = flag.String("i2c-bus", "", "I2C bus (default: first available)")
		i2cAddr  = flag.Uint("i2c-addr", 0x3C, "I2C address")
		dcPin    = flag.String("dc-pin", "GPIO24", "data/command GPIO (SPI only)")
		rstPin   = flag.String("rst-pin", "GPIO25", "reset GPIO, empty to skip")
		baseURL  = flag.String("url", oled.DefaultBaseURL, "P4wnP1 service base URL")
		token    = flag.String("token", oled.DefaultTokenPath, "machine-local credential")
		contrast = flag.Int("contrast", 0xCF, "panel contrast, 0-255")
		poll     = flag.Duration("poll", 5*time.Second, "how often to re-read the active screen")
		headless = flag.Bool("headless", false, "run the UI with no panel (for testing the stack)")
		waitSvc  = flag.Duration("wait-service", 90*time.Second, "how long to wait for the service at startup")
		brand    = flag.String("brand", "", "operator name on the boot splash (default: read "+oled.BrandFile+")")
		tagline  = flag.String("tagline", "", "second line under the operator name")
		firstRun = flag.String("firstrun-creds", oled.FirstRunFile, "first-boot credentials handoff")
		bootFlag = flag.String("firstboot-flag", oled.FirstBootFlag, "file first boot creates when it is done")
		mirror   = flag.String("mirror-socket", oled.MirrorSocket, "serve the panel here; empty to disable")
		// Which way up the image sits on the glass. The default matches the
		// keys when the board hangs from a USB port; "180" is for a board
		// mounted the other way round. Also readable from the environment so
		// it can be set in the unit file without editing the command line.
		rotate = flag.String("rotate", envOr("P4WNP1_OLED_ROTATE", "0"), "image rotation: 0 or 180")
	)
	flag.Parse()

	log.SetFlags(0)
	log.SetPrefix("p4wnp1-oled: ")

	cfg := oled.DefaultPanelConfig()
	switch strings.ToLower(*busName) {
	case "i2c":
		cfg.Bus = oled.BusI2C
	case "spi":
		cfg.Bus = oled.BusSPI
	default:
		log.Fatalf("unknown bus %q (want spi or i2c)", *busName)
	}
	switch strings.ToLower(*ctrlName) {
	case "ssd1306":
		cfg.Controller = oled.SSD1306
	case "sh1106":
		cfg.Controller = oled.SH1106
	default:
		log.Fatalf("unknown controller %q (want sh1106 or ssd1306)", *ctrlName)
	}
	cfg.SPIPort, cfg.I2CBus, cfg.I2CAddr = *spiPort, *i2cBus, uint16(*i2cAddr)
	cfg.DCPin, cfg.RSTPin = *dcPin, *rstPin
	cfg.Rotate = oled.ParseRotation(*rotate)
	log.Printf("panel rotation: %d degrees", int(cfg.Rotate))

	var disp oled.Display
	if *headless {
		disp = &oled.NullDisplay{}
		log.Printf("headless: no panel will be driven")
	} else {
		d, err := oled.OpenPanel(cfg)
		if err != nil {
			// No HAT fitted is the common case on a bare board, and it is not
			// a crash: the device works perfectly without a screen. Say what
			// went wrong once and exit cleanly so systemd does not restart in
			// a loop forever.
			log.Printf("no panel (%v)", err)
			log.Printf("if a HAT is fitted, check dtparam=spi=on in config.txt and that nothing else holds the bus")
			return
		}
		disp = d
	}
	defer disp.Close()
	_ = disp.SetContrast(byte(*contrast))

	// The operator's mark, from the flag or from a file so an image can be
	// branded without a rebuild.
	brandInfo := oled.Brand{Name: *brand, Tagline: *tagline}
	if brandInfo.Name == "" {
		brandInfo = oled.LoadBrand(oled.BrandFile)
	}

	fb := oled.NewFramebuffer()
	oled.DrawSplashBranded(fb, oled.VersionLine(version), 0, brandInfo)
	_ = disp.Show(fb)

	client := oled.NewAPIClient(*baseURL, *token)

	// FIRST BOOT: hold the panel on a warning until setup finishes.
	//
	// Setup takes minutes on a Pi Zero W -- resize, three SSH host keys on
	// one 1GHz core, the web admin, the key off the card -- and for all of
	// it the device looks idle. Pulling the plug there is the one genuinely
	// destructive thing on offer: a half-written auth.json, host keys that
	// were generated but never installed, an interrupted resize. So say so,
	// for exactly as long as it is true.
	if oled.FirstBootRunning(*bootFlag) {
		log.Printf("first boot still running; holding the panel on the warning")
		waitForFirstBoot(disp, fb, *bootFlag)
	}

	// Wait for the service. The daemon usually starts alongside it, so a
	// first call that fails means "not up yet", not "broken" -- and a splash
	// that sits there saying nothing while the device boots is the single
	// most common way a screen gets reported as dead.
	if !waitForService(disp, fb, client, *waitSvc, brandInfo) {
		oled.DrawFatal(fb, " NO SERVICE ",
			"The P4wnP1 service did not answer. Check: systemctl status P4wnP1")
		_ = disp.Show(fb)
		time.Sleep(10 * time.Second)
		// EXIT NON-ZERO. A clean exit means "no panel fitted" and the unit
		// sets SuccessExitStatus=0 precisely so that case does not restart
		// forever. Returning here borrowed that meaning for a completely
		// different situation -- the service being slow to come up -- so
		// Restart=on-failure never fired and the panel stayed dark until
		// someone noticed and restarted it by hand.
		os.Exit(1)
	}

	app := oled.NewApp(client, oled.NewRoot())
	app.Refresh()

	// First boot leaves the generated credentials here for the panel to show
	// once. Pushed before the menu because this is the only time they can be
	// read, and a screen the operator has to go looking for is a screen they
	// will find after they have already rebooted.
	if c := oled.LoadFirstRunCreds(*firstRun); c != nil {
		app.Push(oled.NewFirstRunView(c))
	}

	in, err := openInput(*headless)
	if err != nil {
		oled.DrawFatal(fb, " NO BUTTONS ", err.Error())
		_ = disp.Show(fb)
		time.Sleep(10 * time.Second)
		// Same reasoning: a panel whose controls could not be claimed is a
		// failure to be retried, not a board without a HAT.
		os.Exit(1)
	}
	defer in.Close()
	// The button test screen reads the pins directly; nothing else uses this.
	app.Input = in

	// Mirror the panel for the web console. A failure here must not stop
	// the device working: the panel in your hand is the primary interface
	// and the remote copy is a convenience.
	remote := make(chan oled.Button, 8)
	if *mirror != "" {
		mir := &oled.Mirror{
			Frame: panel.Frame,
			Text:  panel.Text,
			Press: func(b oled.Button) bool {
				select {
				case remote <- b:
					return true
				default:
					return false
				}
			},
		}
		if err := mir.Listen(*mirror); err != nil {
			log.Printf("panel mirror unavailable (%v)", err)
		} else {
			defer mir.Close()
			log.Printf("mirroring the panel on %s", *mirror)
		}
	}

	run(app, disp, fb, in, remote, *poll)
}

// panel is the frame the mirror serves. The guard that keeps secrets off it
// lives in oled.PanelSource, where it is tested.
var panel = &oled.PanelSource{}

func publish(app *oled.App, fb *oled.Framebuffer) { panel.Publish(app, fb) }

// waitForFirstBoot holds the DO NOT POWER OFF screen until the flag appears.
//
// There is no upper bound on purpose. A resize on a slow card can take a
// while, and a timeout here would replace a true warning with a false
// all-clear -- which is worse than making someone wait. If setup really is
// wedged the operator still has every other signal: the LED, the journal,
// and the fact that the panel is plainly still saying "SETTING UP".
func waitForFirstBoot(disp oled.Display, fb *oled.Framebuffer, flag string) {
	// The bar is elapsed time against a typical first boot, not real
	// progress -- there is nothing to measure. It is there so the screen is
	// visibly alive; a frozen panel is what makes people pull the plug.
	const typical = 4 * time.Minute
	start := time.Now()
	for oled.FirstBootRunning(flag) {
		el := time.Since(start)
		p := float64(el) / float64(typical)
		if p > 0.97 {
			p = 0.97 // never show full while it is still going
		}
		oled.DrawSetupWarning(fb, fmt.Sprintf("%ds elapsed", int(el.Seconds())), p)
		_ = disp.Show(fb)
		time.Sleep(time.Second)
	}
	oled.DrawSetupDone(fb, fmt.Sprintf("took %ds", int(time.Since(start).Seconds())))
	_ = disp.Show(fb)
	time.Sleep(3 * time.Second)
}

// waitForService polls until the API answers, animating the splash so the
// screen is visibly alive rather than apparently frozen.
func waitForService(disp oled.Display, fb *oled.Framebuffer, c oled.Client, limit time.Duration, b oled.Brand) bool {
	deadline := time.Now().Add(limit)
	for i := 0; time.Now().Before(deadline); i++ {
		if _, err := c.Status(); err == nil {
			return true
		}
		elapsed := limit - time.Until(deadline)
		oled.DrawSplashBranded(fb, oled.VersionLine(version), float64(elapsed)/float64(limit), b)
		_ = disp.Show(fb)
		time.Sleep(time.Second)
	}
	return false
}

func run(app *oled.App, disp oled.Display, fb *oled.Framebuffer, in oled.Input, remote <-chan oled.Button, poll time.Duration) {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)

	tick := time.NewTicker(poll)
	defer tick.Stop()
	// A separate, faster tick for the display, so a toast appears and expires
	// on time without polling the device every 150ms.
	paint := time.NewTicker(150 * time.Millisecond)
	defer paint.Stop()

	app.Render(fb)
	_ = disp.Show(fb)
	publish(app, fb)

	for {
		select {
		case <-sig:
			log.Printf("stopping")
			return
		case b, ok := <-in.Events():
			if !ok {
				return
			}
			app.Handle(b)
			if app.Quitting() {
				return
			}
			app.Render(fb)
			_ = disp.Show(fb)
			publish(app, fb)
		case b := <-remote:
			// A press from the web console. Identical to a press on the
			// board from here down, so there is no second code path that
			// only remote control exercises.
			app.Handle(b)
			if app.Quitting() {
				return
			}
			app.Render(fb)
			_ = disp.Show(fb)
			publish(app, fb)
		case <-tick.C:
			app.Refresh()
		case <-paint.C:
			app.Render(fb)
			_ = disp.Show(fb)
			publish(app, fb)
		}
	}
}

func openInput(headless bool) (oled.Input, error) {
	if headless {
		// Nothing to read from, but the loop still runs and the stack is
		// exercised, which is what --headless is for.
		return oled.NewChanInput(), nil
	}
	in, err := oled.OpenGPIOInput(nil)
	if err != nil {
		return nil, fmt.Errorf("%v", err)
	}
	return in, nil
}
