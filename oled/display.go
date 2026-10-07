package oled

// Display is the panel. Kept to three methods so the UI never learns anything
// about SPI, I2C or GPIO, and so the simulator and the tests are first-class
// implementations rather than shims bolted on afterwards.
//
// That separation is load-bearing on this project: the display is driven by
// code that has to be correct before anyone can see a pixel of it.
type Display interface {
	// Show pushes a whole frame. Implementations may skip unchanged pages.
	Show(fb *Framebuffer) error
	// SetContrast takes 0..255. Dimming matters on a device whose whole
	// purpose can be sitting unnoticed on someone's desk.
	SetContrast(level byte) error
	Close() error
}

// Controller selects the command dialect. The Waveshare 1.3inch OLED HAT is
// SH1106; the cheap 0.96inch modules are SSD1306. They share a command set
// almost exactly, and differ in the two ways that break a display silently if
// you guess:
//
//   - SH1106 has 132 columns of RAM with the 128-pixel panel centred, so
//     column 0 of the panel is column 2 of RAM. Drive it as an SSD1306 and the
//     image is shifted two pixels and wraps.
//   - SH1106 has no horizontal addressing mode, so a frame must be written one
//     page at a time with the column cursor reset each time, rather than
//     streamed as 1024 bytes.
type Controller int

const (
	SH1106 Controller = iota
	SSD1306
)

func (c Controller) String() string {
	if c == SSD1306 {
		return "SSD1306"
	}
	return "SH1106"
}

// colOffset is where the visible panel starts inside controller RAM.
func (c Controller) colOffset() int {
	if c == SH1106 {
		return 2
	}
	return 0
}

// Command bytes shared by both controllers.
const (
	cmdDisplayOff        = 0xAE
	cmdDisplayOn         = 0xAF
	cmdSetContrast       = 0x81
	cmdSetDisplayClock   = 0xD5
	cmdSetMultiplex      = 0xA8
	cmdSetDisplayOffset  = 0xD3
	cmdSetStartLine      = 0x40
	cmdChargePump        = 0x8D // SSD1306 only
	cmdSetDCDC           = 0xAD // SH1106 only
	cmdSegRemap          = 0xA1
	cmdComScanDec        = 0xC8
	cmdSetComPins        = 0xDA
	cmdSetPrecharge      = 0xD9
	cmdSetVComDetect     = 0xDB
	cmdDisplayAllOnResme = 0xA4
	cmdNormalDisplay     = 0xA6
	cmdSetPageAddr       = 0xB0
	cmdSetMemoryMode     = 0x20 // SSD1306 only
)

// initSequence returns the power-on commands for a 128x64 panel.
//
// The two controllers diverge on exactly one line, the charge-pump enable:
// SSD1306 uses 0x8D 0x14, SH1106 uses 0xAD 0x8B. Sending the wrong one leaves
// the panel initialised but unlit, which looks identical to a wiring fault and
// is the single most common way this board is reported "dead".
func (c Controller) initSequence() []byte {
	seq := []byte{
		cmdDisplayOff,
		cmdSetDisplayClock, 0x80,
		cmdSetMultiplex, 0x3F, // 1/64 duty
		cmdSetDisplayOffset, 0x00,
		cmdSetStartLine | 0x00,
	}
	if c == SSD1306 {
		seq = append(seq, cmdChargePump, 0x14)
		seq = append(seq, cmdSetMemoryMode, 0x00) // horizontal addressing
	} else {
		seq = append(seq, cmdSetDCDC, 0x8B)
	}
	seq = append(seq,
		cmdSegRemap,   // column 127 -> SEG0, i.e. rotate 180 with ComScanDec
		cmdComScanDec, // scan COM63 -> COM0
		cmdSetComPins, 0x12,
		cmdSetContrast, 0xCF,
		cmdSetPrecharge, 0xF1,
		cmdSetVComDetect, 0x40,
		cmdDisplayAllOnResme,
		cmdNormalDisplay,
		cmdDisplayOn,
	)
	return seq
}

// pageCommands positions the controller's cursor at the start of one page.
//
// Extracted from the SPI driver so the part most likely to be silently wrong
// can be tested on any machine. The SH1106 offset is the classic way to get
// this board wrong: its RAM is 132 columns wide with the 128-pixel panel
// centred, so the first visible column is RAM column 2. Drive it as an
// SSD1306 and every frame is shifted two pixels and wraps at the edge --
// which looks like a corrupt framebuffer rather than an addressing mistake.
func pageCommands(c Controller, page int) []byte {
	off := c.colOffset()
	return []byte{
		byte(cmdSetPageAddr | page),
		byte(0x00 | (off & 0x0F)), // lower nibble of the column
		byte(0x10 | (off >> 4)),   // upper nibble
	}
}

// NullDisplay accepts frames and discards them. Used by the daemon when it is
// asked to run headless (for a smoke test on a board with no HAT fitted) so
// the whole stack above the panel still runs and can be exercised.
type NullDisplay struct{ Frames int }

func (n *NullDisplay) Show(*Framebuffer) error { n.Frames++; return nil }
func (n *NullDisplay) SetContrast(byte) error  { return nil }
func (n *NullDisplay) Close() error            { return nil }

// RecordingDisplay keeps the last frame, so a test can assert on what would
// have reached the panel.
type RecordingDisplay struct {
	Last     *Framebuffer
	Frames   int
	Contrast byte
	Closed   bool
}

func (r *RecordingDisplay) Show(fb *Framebuffer) error {
	cp := *fb
	r.Last = &cp
	r.Frames++
	return nil
}
func (r *RecordingDisplay) SetContrast(l byte) error { r.Contrast = l; return nil }
func (r *RecordingDisplay) Close() error             { r.Closed = true; return nil }
