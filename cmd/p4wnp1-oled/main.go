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

	// Wait for the service. The daemon usually starts alongside it, so a
	// first call that fails means "not up yet", not "broken" -- and a splash
	// that sits there saying nothing while the device boots is the single
	// most common way a screen gets reported as dead.
	if !waitForService(disp, fb, client, *waitSvc, brandInfo) {
		oled.DrawFatal(fb, " NO SERVICE ",
			"The P4wnP1 service did not answer. Check: systemctl status P4wnP1")
		_ = disp.Show(fb)
		time.Sleep(10 * time.Second)
		return
	}

	app := oled.NewApp(client, oled.NewRoot())
	app.Refresh()

	in, err := openInput(*headless)
	if err != nil {
		oled.DrawFatal(fb, " NO BUTTONS ", err.Error())
		_ = disp.Show(fb)
		time.Sleep(10 * time.Second)
		return
	}
	defer in.Close()

	run(app, disp, fb, in, *poll)
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

func run(app *oled.App, disp oled.Display, fb *oled.Framebuffer, in oled.Input, poll time.Duration) {
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
		case <-tick.C:
			app.Refresh()
		case <-paint.C:
			app.Render(fb)
			_ = disp.Show(fb)
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
