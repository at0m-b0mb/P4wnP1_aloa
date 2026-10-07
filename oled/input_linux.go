//go:build linux
// +build linux

package oled

import (
	"fmt"
	"sync"
	"time"

	"periph.io/x/periph/conn/gpio"
	"periph.io/x/periph/conn/gpio/gpioreg"
	"periph.io/x/periph/host"
)

// GPIO pin names for the Waveshare 1.3inch OLED HAT, from its wiki. All eight
// controls are active-low with the internal pull-up engaged.
//
// These are BCM numbers, which is what periph's names use. They are a
// variable rather than a constant block so an operator with a differently
// wired panel can override them in the daemon's config without a rebuild.
var DefaultPins = map[Button]string{
	BtnUp:    "GPIO6",
	BtnDown:  "GPIO19",
	BtnLeft:  "GPIO5",
	BtnRight: "GPIO26",
	BtnPress: "GPIO13",
	BtnKey1:  "GPIO21",
	BtnKey2:  "GPIO20",
	BtnKey3:  "GPIO16",
}

// GPIOInput polls the joystick and keys.
//
// Polling at 50Hz rather than using edge interrupts, deliberately. periph's
// edge detection needs one watcher goroutine per pin and the HAT has eight;
// on a single-core Pi Zero W that is eight goroutines waking on every contact
// bounce. A 20ms poll of eight pins is a handful of reads and gives the
// debouncer a regular clock to work against, which is also what makes
// hold-to-repeat predictable.
type GPIOInput struct {
	pins map[Button]gpio.PinIO
	deb  *Debouncer
	ch   chan Button
	stop chan struct{}
	once sync.Once
}

// OpenGPIOInput claims the control pins. Returns an error if any is missing or
// cannot be configured, naming the pin -- "no such GPIO" with no name is the
// kind of message that costs an hour.
func OpenGPIOInput(pinNames map[Button]string) (*GPIOInput, error) {
	if _, err := host.Init(); err != nil {
		return nil, fmt.Errorf("periph host init: %w", err)
	}
	if pinNames == nil {
		pinNames = DefaultPins
	}

	g := &GPIOInput{
		pins: map[Button]gpio.PinIO{},
		deb:  NewDebouncer(),
		ch:   make(chan Button, 16),
		stop: make(chan struct{}),
	}
	for btn, name := range pinNames {
		p := gpioreg.ByName(name)
		if p == nil {
			return nil, fmt.Errorf("no such GPIO %q for %s", name, btn)
		}
		// Pull-up: the HAT's switches short to ground, so idle reads high.
		if err := p.In(gpio.PullUp, gpio.NoEdge); err != nil {
			return nil, fmt.Errorf("configure %s (%s) as input: %w", name, btn, err)
		}
		g.pins[btn] = p
	}

	go g.poll()
	return g, nil
}

func (g *GPIOInput) poll() {
	t := time.NewTicker(20 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-g.stop:
			return
		case <-t.C:
			for btn, pin := range g.pins {
				// Active low.
				if g.deb.Update(btn, pin.Read() == gpio.Low) {
					select {
					case g.ch <- btn:
					default:
						// Full queue means the UI is behind. Dropping is
						// right: replaying a backlog of presses after a slow
						// redraw would walk the menu on its own.
					}
				}
			}
		}
	}
}

func (g *GPIOInput) Events() <-chan Button { return g.ch }

func (g *GPIOInput) Close() error {
	g.once.Do(func() { close(g.stop) })
	return nil
}
