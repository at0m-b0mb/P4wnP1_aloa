//go:build linux
// +build linux

package oled

import (
	"fmt"
	"time"

	"periph.io/x/periph/conn"
	"periph.io/x/periph/conn/gpio"
	"periph.io/x/periph/conn/gpio/gpioreg"
	"periph.io/x/periph/conn/i2c"
	"periph.io/x/periph/conn/i2c/i2creg"
	"periph.io/x/periph/conn/physic"
	"periph.io/x/periph/conn/spi"
	"periph.io/x/periph/conn/spi/spireg"
	"periph.io/x/periph/host"
)

// Bus selects how the panel is wired. The Waveshare 1.3inch OLED HAT ships in
// 4-wire SPI by default; its wiki documents an I2C option reached by moving
// solder blobs on the back (BS1=1, BS0=0, address 0x3C). Both are supported
// because asking an operator to resolder a board to use our software would be
// absurd, and because the 0.96inch I2C modules are everywhere.
type Bus int

const (
	BusSPI Bus = iota
	BusI2C
)

// PanelConfig is the full hardware description. Defaults match the Waveshare
// 1.3inch OLED HAT as documented on its wiki.
type PanelConfig struct {
	Bus        Bus
	Controller Controller

	SPIPort string // e.g. "/dev/spidev0.0"; empty picks the first
	SPIHz   physic.Frequency

	I2CBus  string // empty picks the first
	I2CAddr uint16

	DCPin  string // BCM 24 on the HAT
	RSTPin string // BCM 25 on the HAT

	// Rotate turns the image on the glass. See Rotation: the default matches
	// the keys when the board hangs from a USB port, which is how a Zero W
	// is normally used.
	Rotate Rotation
}

// DefaultPanelConfig is the Waveshare 1.3inch OLED HAT as it arrives.
func DefaultPanelConfig() PanelConfig {
	return PanelConfig{
		Bus:        BusSPI,
		Controller: SH1106,
		SPIHz:      8 * physic.MegaHertz,
		I2CAddr:    0x3C,
		DCPin:      "GPIO24",
		RSTPin:     "GPIO25",
	}
}

// panel drives an SH1106/SSD1306 over SPI or I2C.
type panel struct {
	cfg  PanelConfig
	conn conn.Conn
	spi  spi.PortCloser
	i2c  i2c.BusCloser
	dc   gpio.PinIO
	rst  gpio.PinIO

	// prev is the last frame pushed, so Show can skip pages that did not
	// change. On a Pi Zero W writing all eight pages every frame is most of
	// the cost of the UI, and most frames change one row.
	prev  Framebuffer
	dirty bool // force a full write on the next Show
}

// OpenPanel initialises the display. Returns an error rather than panicking on
// a board with no HAT fitted: the daemon treats "no display" as a reason to
// exit quietly, not a crash.
func OpenPanel(cfg PanelConfig) (Display, error) {
	if _, err := host.Init(); err != nil {
		return nil, fmt.Errorf("periph host init: %w", err)
	}

	p := &panel{cfg: cfg, dirty: true}

	switch cfg.Bus {
	case BusI2C:
		b, err := i2creg.Open(cfg.I2CBus)
		if err != nil {
			return nil, fmt.Errorf("open I2C bus: %w", err)
		}
		p.i2c = b
		p.conn = &i2c.Dev{Bus: b, Addr: cfg.I2CAddr}
	default:
		port, err := spireg.Open(cfg.SPIPort)
		if err != nil {
			return nil, fmt.Errorf("open SPI port (is dtparam=spi=on set?): %w", err)
		}
		p.spi = port
		hz := cfg.SPIHz
		if hz == 0 {
			hz = 8 * physic.MegaHertz
		}
		c, err := port.Connect(hz, spi.Mode0, 8)
		if err != nil {
			port.Close()
			return nil, fmt.Errorf("configure SPI: %w", err)
		}
		p.conn = c

		// D/C is SPI-only: on I2C the control byte carries that bit instead.
		if p.dc = gpioreg.ByName(cfg.DCPin); p.dc == nil {
			port.Close()
			return nil, fmt.Errorf("no such GPIO %q for D/C", cfg.DCPin)
		}
		if err := p.dc.Out(gpio.Low); err != nil {
			port.Close()
			return nil, fmt.Errorf("drive D/C: %w", err)
		}
	}

	// Reset is optional: on I2C wiring it is often left unconnected, and the
	// panel powers up in a usable state without it.
	if cfg.RSTPin != "" {
		if p.rst = gpioreg.ByName(cfg.RSTPin); p.rst != nil {
			_ = p.rst.Out(gpio.High)
			time.Sleep(time.Millisecond)
			_ = p.rst.Out(gpio.Low)
			time.Sleep(10 * time.Millisecond)
			_ = p.rst.Out(gpio.High)
			time.Sleep(10 * time.Millisecond)
		}
	}

	if err := p.commands(cfg.Controller.initSequenceRotated(cfg.Rotate)); err != nil {
		p.Close()
		return nil, fmt.Errorf("initialise panel: %w", err)
	}
	return p, nil
}

// commands sends command bytes. On SPI that means D/C low; on I2C it means a
// 0x00 control byte ahead of the payload.
func (p *panel) commands(b []byte) error {
	if p.cfg.Bus == BusI2C {
		return p.conn.Tx(append([]byte{0x00}, b...), nil)
	}
	if err := p.dc.Out(gpio.Low); err != nil {
		return err
	}
	return p.conn.Tx(b, nil)
}

func (p *panel) data(b []byte) error {
	if p.cfg.Bus == BusI2C {
		return p.conn.Tx(append([]byte{0x40}, b...), nil)
	}
	if err := p.dc.Out(gpio.High); err != nil {
		return err
	}
	return p.conn.Tx(b, nil)
}

func (p *panel) Show(fb *Framebuffer) error {
	for page := 0; page < Pages; page++ {
		cur := fb.Page(page)
		if !p.dirty && pageEqual(cur, p.prev.Page(page)) {
			continue
		}
		// Position the cursor for this page. SH1106 has no addressing mode
		// that would let the controller advance pages for us, so this runs
		// per page for both controllers rather than branching.
		if err := p.commands(pageCommands(p.cfg.Controller, page)); err != nil {
			return err
		}
		if err := p.data(cur); err != nil {
			return err
		}
	}
	p.prev = *fb
	p.dirty = false
	return nil
}

func pageEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (p *panel) SetContrast(level byte) error {
	return p.commands([]byte{cmdSetContrast, level})
}

func (p *panel) Close() error {
	// Blank the panel on the way out. A frozen last frame on a device left
	// plugged into a target is a tell; a dark screen is not.
	_ = p.commands([]byte{cmdDisplayOff})
	if p.spi != nil {
		return p.spi.Close()
	}
	if p.i2c != nil {
		return p.i2c.Close()
	}
	return nil
}
