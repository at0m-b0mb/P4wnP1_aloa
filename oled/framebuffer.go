package oled

import (
	"image"
	"image/color"
	"strings"
)

// Display geometry for the Waveshare 1.3inch OLED HAT.
const (
	Width  = 128
	Height = 64
	Pages  = Height / 8
	// Cols and Rows are how much 5x7 text fits: 21 x 8.
	Cols = Width / CharW
	Rows = Height / LineH
)

// Framebuffer is a 1bpp buffer in the controller's native layout: Pages rows
// of Width bytes, each byte holding 8 vertically-stacked pixels with bit 0 at
// the top. Storing it this way makes Blit a straight copy per page instead of
// a transpose on a 1GHz single-core board.
//
// It is pure Go with no build tags and no hardware, so every screen in this
// package can be rendered and inspected on a laptop. That matters more than
// usual here: this code drives a display I have never had in front of me.
type Framebuffer struct {
	buf [Pages * Width]byte
}

func NewFramebuffer() *Framebuffer { return &Framebuffer{} }

// Bytes exposes the raw pages for a driver to push to the panel.
func (f *Framebuffer) Bytes() []byte { return f.buf[:] }

// Load replaces the contents from a stored copy. Returns false if the length
// is wrong rather than panicking: the caller is usually holding bytes that
// came from somewhere else.
func (f *Framebuffer) Load(b []byte) bool {
	if len(b) != len(f.buf) {
		return false
	}
	copy(f.buf[:], b)
	return true
}

// Page returns the Width bytes of one 8-pixel-tall page.
func (f *Framebuffer) Page(p int) []byte {
	if p < 0 || p >= Pages {
		return nil
	}
	return f.buf[p*Width : (p+1)*Width]
}

func (f *Framebuffer) Clear() {
	for i := range f.buf {
		f.buf[i] = 0
	}
}

// Set turns one pixel on or off. Out-of-range coordinates are dropped rather
// than wrapping: a text run that overflows the right edge should be clipped,
// not reappear on the left.
func (f *Framebuffer) Set(x, y int, on bool) {
	if x < 0 || x >= Width || y < 0 || y >= Height {
		return
	}
	idx := (y/8)*Width + x
	bit := byte(1) << uint(y%8)
	if on {
		f.buf[idx] |= bit
	} else {
		f.buf[idx] &^= bit
	}
}

func (f *Framebuffer) Get(x, y int) bool {
	if x < 0 || x >= Width || y < 0 || y >= Height {
		return false
	}
	return f.buf[(y/8)*Width+x]&(1<<uint(y%8)) != 0
}

// HLine and VLine are separate from Rect because almost every rule on a screen
// this size is axis-aligned, and a general line routine would be slower and
// more code for no gain.
func (f *Framebuffer) HLine(x, y, w int, on bool) {
	for i := 0; i < w; i++ {
		f.Set(x+i, y, on)
	}
}

func (f *Framebuffer) VLine(x, y, h int, on bool) {
	for i := 0; i < h; i++ {
		f.Set(x, y+i, on)
	}
}

func (f *Framebuffer) Rect(x, y, w, h int, on bool) {
	if w <= 0 || h <= 0 {
		return
	}
	f.HLine(x, y, w, on)
	f.HLine(x, y+h-1, w, on)
	f.VLine(x, y, h, on)
	f.VLine(x+w-1, y, h, on)
}

func (f *Framebuffer) FillRect(x, y, w, h int, on bool) {
	for j := 0; j < h; j++ {
		f.HLine(x, y+j, w, on)
	}
}

// Invert flips every pixel in a rectangle. Used to show the selected row of a
// menu, which reads far better on a 1bpp panel than any marker glyph.
func (f *Framebuffer) Invert(x, y, w, h int) {
	for j := 0; j < h; j++ {
		for i := 0; i < w; i++ {
			f.Set(x+i, y+j, !f.Get(x+i, y+j))
		}
	}
}

// Text draws s with its top-left at (x,y) and returns the x just past it.
// Clipped at the right edge.
func (f *Framebuffer) Text(x, y int, s string) int {
	for _, r := range s {
		g := glyph(r)
		for col := 0; col < GlyphW; col++ {
			bits := g[col]
			for row := 0; row < GlyphH; row++ {
				if bits&(1<<uint(row)) != 0 {
					f.Set(x+col, y+row, true)
				}
			}
		}
		x += CharW
		if x >= Width {
			break
		}
	}
	return x
}

// TextRow draws into one of the Rows text lines.
func (f *Framebuffer) TextRow(col, row int, s string) {
	f.Text(col*CharW, row*LineH, s)
}

// TextCentered centres s horizontally on the given pixel row.
func (f *Framebuffer) TextCentered(y int, s string) {
	x := (Width - TextWidth(s)) / 2
	if x < 0 {
		x = 0
	}
	// Snap to the character grid.
	//
	// ReadBack scans cells at multiples of CharW, and it is what serves
	// /panel.txt to the web console as well as what the tests read. Centred
	// text landing on an odd pixel put every glyph half-way across two
	// cells, and the scanner simply dropped the ones it could not resolve:
	// "1:30 of ~4 min" came back as ":30 of ~4 min", silently missing its
	// first character. The panel looked perfect; the mirror was wrong.
	//
	// The cost is up to five pixels of centring, which nobody can see, and
	// it applies to every centred string on the device rather than only the
	// one that exposed it.
	x -= x % CharW
	f.Text(x, y, s)
}

// Truncate shortens s to fit n characters, marking the cut with a '~' so a
// clipped value is never mistaken for a complete one. On a 21-column display
// that distinction decides whether an operator trusts what they are reading.
func Truncate(s string, n int) string {
	r := []rune(s)
	if n <= 0 {
		return ""
	}
	if len(r) <= n {
		return s
	}
	if n == 1 {
		return "~"
	}
	return string(r[:n-1]) + "~"
}

// Pad right-pads s to n characters so inverted selection bars have a uniform
// width without the caller measuring anything.
func Pad(s string, n int) string {
	if d := n - len([]rune(s)); d > 0 {
		return s + strings.Repeat(" ", d)
	}
	return s
}

// Image renders the buffer as a Go image, for tests and for the simulator.
// Lit pixels are white on black, which is what the blue-on-black panel
// actually looks like.
func (f *Framebuffer) Image(scale int) *image.RGBA {
	if scale < 1 {
		scale = 1
	}
	img := image.NewRGBA(image.Rect(0, 0, Width*scale, Height*scale))
	off := color.RGBA{8, 10, 14, 255}
	on := color.RGBA{120, 200, 255, 255}
	for y := 0; y < Height; y++ {
		for x := 0; x < Width; x++ {
			c := off
			if f.Get(x, y) {
				c = on
			}
			for dy := 0; dy < scale; dy++ {
				for dx := 0; dx < scale; dx++ {
					img.Set(x*scale+dx, y*scale+dy, c)
				}
			}
		}
	}
	return img
}

// String renders the buffer as ASCII art, which makes a failing test say what
// is actually on the screen instead of printing a byte slice.
func (f *Framebuffer) String() string {
	var b strings.Builder
	b.Grow((Width + 1) * Height)
	for y := 0; y < Height; y++ {
		for x := 0; x < Width; x++ {
			if f.Get(x, y) {
				b.WriteByte('#')
			} else {
				b.WriteByte('.')
			}
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// TextScaled draws s with each pixel blown up s-fold. At 2x the 5x7 font
// becomes 10x14, which on a 128x64 panel is the largest size that still fits a
// useful word across the screen.
//
// Scaling rather than carrying a second font: a 1bpp panel at this size has no
// room for the serifs and modulated strokes the house display face is made of,
// so a bigger version of a clean grotesque reads as deliberate where a
// squashed serif reads as broken.
func (f *Framebuffer) TextScaled(x, y, scale int, s string) int {
	if scale < 1 {
		scale = 1
	}
	if scale == 1 {
		return f.Text(x, y, s)
	}
	for _, r := range s {
		g := glyph(r)
		for col := 0; col < GlyphW; col++ {
			bits := g[col]
			for row := 0; row < GlyphH; row++ {
				if bits&(1<<uint(row)) == 0 {
					continue
				}
				f.FillRect(x+col*scale, y+row*scale, scale, scale, true)
			}
		}
		x += CharW * scale
		if x >= Width {
			break
		}
	}
	return x
}

// TextScaledCentered centres scaled text horizontally.
func (f *Framebuffer) TextScaledCentered(y, scale int, s string) {
	w := TextWidth(s) * scale
	x := (Width - w) / 2
	if x < 0 {
		x = 0
	}
	f.TextScaled(x, y, scale, s)
}

// TextWidthScaled is TextWidth for scaled text.
func TextWidthScaled(s string, scale int) int {
	if scale < 1 {
		scale = 1
	}
	return TextWidth(s) * scale
}
