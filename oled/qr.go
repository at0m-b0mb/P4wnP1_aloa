package oled

import (
	"errors"
	"fmt"
	"strings"
)

// A QR encoder, byte mode, versions 1 to 4, error correction level M.
//
// WHY THIS IS HERE AT ALL
//
// First boot generates a 20-character password and shows it once on a 1.3
// inch panel, and somebody then has to type it into a browser. Every step of
// that is a chance to get a character wrong, and a wrong character produces a
// login failure that says nothing about WHICH character. A QR code removes
// the transcription entirely for anyone holding a phone, and the password
// stays printed beside it for anyone who is not.
//
// WHY IT IS WRITTEN OUT RATHER THAN IMPORTED
//
// The alternative was a dependency in a binary that ships to a Pi Zero W, for
// one screen. This file is self-contained, has no imports beyond errors and
// fmt, and encodes only what the panel needs: binary data, short strings,
// fixed error correction. Versions 1-4 cover up to 62 bytes at level M, which
// is comfortably more than any secret this device generates.
//
// Level M corrects about 15% of the symbol. On a self-lit OLED read from
// 20cm that is generous, and it buys tolerance for the panel's own
// limitation: at 2 pixels per module a version 2 symbol is 50x50 of the 64
// pixels available, so there is no room to go bigger if a lower level turned
// out not to scan.
//
// Correctness here is not something to take on faith -- a QR that decodes to
// the wrong bytes is worse than no QR, because it fails silently and the
// operator blames their phone. qr_test.go decodes what this produces.

// ECLevel is the error correction level.
//
// Only two are implemented, and the reason is capacity. A symbol has to fit a
// 64-pixel panel at two pixels per module, which caps it at version 3 -- and
// at that size the difference between M and L is 44 bytes versus 53. A WiFi
// join URI for even a short SSID is 47 bytes, so without L it does not fit at
// all. M is used wherever it fits, because on a display the realistic damage
// is glare rather than print noise and more correction is still better.
type ECLevel int

const (
	ECLow    ECLevel = iota // ~7% recovery; the format-info bits are 01
	ECMedium                // ~15%;                                   00
)

func (e ECLevel) formatBits() int {
	if e == ECLow {
		return 0b01
	}
	return 0b00
}

// qrVersion describes one version at one error correction level.
type qrVersion struct {
	version    int
	size       int // modules per side
	totalBytes int // data + ecc codewords
	dataBytes  int // data codewords across all blocks
	blocks     int // ecc blocks
	eccPerBlk  int // ecc codewords per block
	alignAt    int // single alignment pattern centre, 0 for version 1
	level      ECLevel
}

// Values from ISO/IEC 18004 tables 9 and 13-16.
//
// Ordered so that pickVersion walks smallest-symbol-first and, within a
// size, strongest-correction-first: a payload that fits at M never silently
// gets L.
var qrVersions = []qrVersion{
	{1, 21, 26, 16, 1, 10, 0, ECMedium},
	{1, 21, 26, 19, 1, 7, 0, ECLow},
	{2, 25, 44, 28, 1, 16, 18, ECMedium},
	{2, 25, 44, 34, 1, 10, 18, ECLow},
	{3, 29, 70, 44, 1, 26, 22, ECMedium},
	{3, 29, 70, 55, 1, 15, 22, ECLow},
	{4, 33, 100, 64, 2, 18, 26, ECMedium},
	{4, 33, 100, 80, 1, 20, 26, ECLow},
}

// QRCode is a square bitmap of modules. true is dark.
type QRCode struct {
	Size    int
	Version int
	Level   ECLevel
	mod     []bool
	fixed   []bool // function module: not data, and never masked
}

// At reports whether the module at (x, y) is dark.
func (q *QRCode) At(x, y int) bool {
	if x < 0 || y < 0 || x >= q.Size || y >= q.Size {
		return false
	}
	return q.mod[y*q.Size+x]
}

func (q *QRCode) set(x, y int, dark, isFixed bool) {
	q.mod[y*q.Size+x] = dark
	if isFixed {
		q.fixed[y*q.Size+x] = true
	}
}

func (q *QRCode) isFixed(x, y int) bool { return q.fixed[y*q.Size+x] }

// NewQR encodes data in the smallest symbol that holds it, preferring the
// stronger error correction level where both fit.
func NewQR(data []byte) (*QRCode, error) { return NewQRMax(data, 0) }

// NewQRMax is NewQR restricted to symbols no larger than maxModules across
// (0 for no limit). The panel is 64 pixels high and a module has to be two
// pixels to scan, so a caller that has to fit a symbol on screen cannot just
// take whatever version the payload needs -- it has to know in advance
// whether the payload fits at all, and do something else if it does not.
func NewQRMax(data []byte, maxModules int) (*QRCode, error) {
	if len(data) == 0 {
		return nil, errors.New("qr: nothing to encode")
	}
	v, err := pickVersionMax(len(data), maxModules)
	if err != nil {
		return nil, err
	}

	codewords := qrCodewords(v, data)

	q := &QRCode{Size: v.size, Version: v.version, Level: v.level,
		mod: make([]bool, v.size*v.size), fixed: make([]bool, v.size*v.size)}
	q.drawFunctionPatterns(v)
	q.placeData(codewords)

	// Try every mask and keep the one the standard's penalty rules like
	// least. Masking is not cosmetic: an unmasked symbol can contain large
	// blank runs or accidental finder-like sequences that stop a decoder
	// locking on at all.
	best, bestScore := 0, -1
	saved := make([]bool, len(q.mod))
	copy(saved, q.mod)
	for m := 0; m < 8; m++ {
		copy(q.mod, saved)
		q.applyMask(m)
		q.writeFormat(m)
		if s := q.penalty(); bestScore < 0 || s < bestScore {
			best, bestScore = m, s
		}
	}
	copy(q.mod, saved)
	q.applyMask(best)
	q.writeFormat(best)
	return q, nil
}

func pickVersion(n int) (qrVersion, error) { return pickVersionMax(n, 0) }

func pickVersionMax(n, maxModules int) (qrVersion, error) {
	best := 0
	for _, v := range qrVersions {
		if maxModules > 0 && v.size > maxModules {
			continue
		}
		if v.dataBytes-2 > best {
			best = v.dataBytes - 2
		}
		// 4 bits of mode + 8 bits of length + 8 bits per byte, rounded up.
		if (4+8+8*n+7)/8 <= v.dataBytes {
			return v, nil
		}
	}
	if maxModules > 0 {
		return qrVersion{}, fmt.Errorf(
			"qr: %d bytes does not fit a symbol of %d modules or less (max %d bytes)",
			n, maxModules, best)
	}
	return qrVersion{}, fmt.Errorf("qr: %d bytes is more than version 4 holds (%d)", n, best)
}

// qrCodewords builds the interleaved data+ecc stream.
func qrCodewords(v qrVersion, data []byte) []byte {
	bits := &bitBuf{}
	bits.add(0b0100, 4)    // byte mode
	bits.add(len(data), 8) // length; 8 bits is correct for versions 1-9
	for _, b := range data {
		bits.add(int(b), 8)
	}
	// Terminator, up to four zero bits, then pad to a byte boundary.
	for i := 0; i < 4 && len(bits.b) < v.dataBytes*8; i++ {
		bits.add(0, 1)
	}
	for len(bits.b)%8 != 0 {
		bits.add(0, 1)
	}
	// Then the two alternating pad codewords until the capacity is filled.
	pad := []int{0xEC, 0x11}
	for i := 0; len(bits.b) < v.dataBytes*8; i++ {
		bits.add(pad[i%2], 8)
	}
	raw := bits.bytes()

	// Split into blocks. For versions 1-4 level M the blocks are equal, so
	// there is no short/long group to handle.
	perBlock := v.dataBytes / v.blocks
	dataBlocks := make([][]byte, v.blocks)
	eccBlocks := make([][]byte, v.blocks)
	for i := 0; i < v.blocks; i++ {
		dataBlocks[i] = raw[i*perBlock : (i+1)*perBlock]
		eccBlocks[i] = reedSolomon(dataBlocks[i], v.eccPerBlk)
	}

	// Interleave: one codeword from each block in turn, data then ecc.
	out := make([]byte, 0, v.totalBytes)
	for i := 0; i < perBlock; i++ {
		for b := 0; b < v.blocks; b++ {
			out = append(out, dataBlocks[b][i])
		}
	}
	for i := 0; i < v.eccPerBlk; i++ {
		for b := 0; b < v.blocks; b++ {
			out = append(out, eccBlocks[b][i])
		}
	}
	return out
}

type bitBuf struct{ b []bool }

func (s *bitBuf) add(val, n int) {
	for i := n - 1; i >= 0; i-- {
		s.b = append(s.b, (val>>uint(i))&1 == 1)
	}
}

func (s *bitBuf) bytes() []byte {
	out := make([]byte, (len(s.b)+7)/8)
	for i, bit := range s.b {
		if bit {
			out[i/8] |= 1 << uint(7-i%8)
		}
	}
	return out
}

// --- GF(256), the field QR's error correction lives in ---------------------
//
// Primitive polynomial 0x11D, as the standard specifies. The tables are built
// once at package init rather than written out, because a hand-typed 512-entry
// table is a hand-typed 512-entry table.

var gfExp [512]byte
var gfLog [256]byte

func init() {
	x := 1
	for i := 0; i < 255; i++ {
		gfExp[i] = byte(x)
		gfLog[x] = byte(i)
		x <<= 1
		if x&0x100 != 0 {
			x ^= 0x11D
		}
	}
	for i := 255; i < 512; i++ {
		gfExp[i] = gfExp[i-255]
	}
}

func gfMul(a, b byte) byte {
	if a == 0 || b == 0 {
		return 0
	}
	return gfExp[int(gfLog[a])+int(gfLog[b])]
}

// reedSolomon returns n error correction codewords for data.
func reedSolomon(data []byte, n int) []byte {
	// Generator polynomial: product of (x - a^i) for i in 0..n-1.
	gen := make([]byte, n+1)
	gen[0] = 1
	for i := 0; i < n; i++ {
		for j := i; j >= 0; j-- {
			gen[j+1] ^= gfMul(gen[j], gfExp[i])
		}
	}

	rem := make([]byte, n)
	for _, d := range data {
		factor := d ^ rem[0]
		copy(rem, rem[1:])
		rem[n-1] = 0
		for i := 0; i < n; i++ {
			rem[i] ^= gfMul(gen[i+1], factor)
		}
	}
	return rem
}

// --- the matrix ------------------------------------------------------------

func (q *QRCode) drawFunctionPatterns(v qrVersion) {
	n := v.size
	// Three finder patterns with their separators.
	for _, p := range [][2]int{{0, 0}, {n - 7, 0}, {0, n - 7}} {
		q.drawFinder(p[0], p[1])
	}
	// Timing patterns.
	for i := 8; i < n-8; i++ {
		on := i%2 == 0
		q.set(i, 6, on, true)
		q.set(6, i, on, true)
	}
	// Alignment pattern. For versions 2-4 there is exactly one that does not
	// collide with a finder, at (alignAt, alignAt).
	if v.alignAt > 0 {
		q.drawAlignment(v.alignAt, v.alignAt)
	}
	// The dark module, always at (8, 4*version+9).
	q.set(8, 4*v.version+9, true, true)
	// Reserve the format information areas so data placement skips them.
	for i := 0; i < 9; i++ {
		if i != 6 {
			q.set(i, 8, false, true)
			q.set(8, i, false, true)
		}
	}
	for i := 0; i < 8; i++ {
		q.set(n-1-i, 8, false, true)
		q.set(8, n-1-i, false, true)
	}
}

func (q *QRCode) drawFinder(ox, oy int) {
	for dy := -1; dy <= 7; dy++ {
		for dx := -1; dx <= 7; dx++ {
			x, y := ox+dx, oy+dy
			if x < 0 || y < 0 || x >= q.Size || y >= q.Size {
				continue
			}
			// Concentric: 7x7 ring, white gap, 3x3 centre; -1 and 7 are the
			// separator and are always light.
			inRing := dx >= 0 && dx <= 6 && dy >= 0 && dy <= 6
			edge := dx == 0 || dx == 6 || dy == 0 || dy == 6
			centre := dx >= 2 && dx <= 4 && dy >= 2 && dy <= 4
			q.set(x, y, inRing && (edge || centre), true)
		}
	}
}

func (q *QRCode) drawAlignment(cx, cy int) {
	for dy := -2; dy <= 2; dy++ {
		for dx := -2; dx <= 2; dx++ {
			ring := dx == -2 || dx == 2 || dy == -2 || dy == 2
			centre := dx == 0 && dy == 0
			q.set(cx+dx, cy+dy, ring || centre, true)
		}
	}
}

// placeData walks the symbol in the standard zigzag: two-module-wide columns
// from the right, alternating upward and downward, skipping function modules
// and the vertical timing line.
func (q *QRCode) placeData(cw []byte) {
	n := q.Size
	bit := 0
	up := true
	for right := n - 1; right >= 1; right -= 2 {
		if right == 6 {
			right = 5 // the vertical timing pattern is not a data column
		}
		for i := 0; i < n; i++ {
			y := i
			if up {
				y = n - 1 - i
			}
			for dx := 0; dx < 2; dx++ {
				x := right - dx
				if q.isFixed(x, y) {
					continue
				}
				dark := false
				if bit < len(cw)*8 {
					dark = (cw[bit/8]>>uint(7-bit%8))&1 == 1
				}
				q.set(x, y, dark, false)
				bit++
			}
		}
		up = !up
	}
}

func maskAt(m, x, y int) bool {
	switch m {
	case 0:
		return (x+y)%2 == 0
	case 1:
		return y%2 == 0
	case 2:
		return x%3 == 0
	case 3:
		return (x+y)%3 == 0
	case 4:
		return (y/2+x/3)%2 == 0
	case 5:
		return (x*y)%2+(x*y)%3 == 0
	case 6:
		return ((x*y)%2+(x*y)%3)%2 == 0
	default:
		return ((x+y)%2+(x*y)%3)%2 == 0
	}
}

func (q *QRCode) applyMask(m int) {
	for y := 0; y < q.Size; y++ {
		for x := 0; x < q.Size; x++ {
			if !q.isFixed(x, y) && maskAt(m, x, y) {
				q.mod[y*q.Size+x] = !q.mod[y*q.Size+x]
			}
		}
	}
}

// writeFormat places the 15-bit format information twice, as the standard
// requires: once around the top-left finder and once split between the other
// two. A decoder reads whichever copy is intact.
func (q *QRCode) writeFormat(mask int) {
	data := q.Level.formatBits()<<3 | mask

	// BCH(15,5) with generator 0x537, then XOR the standard's mask pattern.
	rem := data
	for i := 0; i < 10; i++ {
		rem <<= 1
		if rem&(1<<10) != 0 {
			rem ^= 0x537
		}
	}
	bits := ((data<<10 | rem) ^ 0x5412) & 0x7FFF

	n := q.Size
	get := func(i int) bool { return (bits>>uint(i))&1 == 1 }
	for i := 0; i <= 5; i++ {
		q.set(8, i, get(i), true)
	}
	q.set(8, 7, get(6), true)
	q.set(8, 8, get(7), true)
	q.set(7, 8, get(8), true)
	for i := 9; i <= 14; i++ {
		q.set(14-i, 8, get(i), true)
	}
	for i := 0; i <= 7; i++ {
		q.set(n-1-i, 8, get(i), true)
	}
	for i := 8; i <= 14; i++ {
		q.set(8, n-15+i, get(i), true)
	}
	q.set(8, n-8, true, true) // the dark module, restated after masking
}

// penalty scores a masked symbol by the four rules in the standard. Lower is
// better. The absolute numbers do not matter; only the ordering does.
func (q *QRCode) penalty() int {
	n, score := q.Size, 0

	// Rule 1: runs of five or more same-coloured modules in a line.
	for _, byRow := range []bool{true, false} {
		for a := 0; a < n; a++ {
			run, prev := 0, false
			for b := 0; b < n; b++ {
				var v bool
				if byRow {
					v = q.At(b, a)
				} else {
					v = q.At(a, b)
				}
				if b > 0 && v == prev {
					run++
				} else {
					run = 1
				}
				prev = v
				if run == 5 {
					score += 3
				} else if run > 5 {
					score++
				}
			}
		}
	}
	// Rule 2: 2x2 blocks of one colour.
	for y := 0; y < n-1; y++ {
		for x := 0; x < n-1; x++ {
			v := q.At(x, y)
			if v == q.At(x+1, y) && v == q.At(x, y+1) && v == q.At(x+1, y+1) {
				score += 3
			}
		}
	}
	// Rule 3: the finder-like 1:1:3:1:1 sequence with four light modules on
	// one side -- the pattern most likely to make a decoder lock on in the
	// wrong place.
	pat := []bool{true, false, true, true, true, false, true, false, false, false, false}
	rev := make([]bool, len(pat))
	for i := range pat {
		rev[i] = pat[len(pat)-1-i]
	}
	match := func(get func(int) bool, start int) bool {
		for _, p := range [][]bool{pat, rev} {
			ok := true
			for i := range p {
				if get(start+i) != p[i] {
					ok = false
					break
				}
			}
			if ok {
				return true
			}
		}
		return false
	}
	for a := 0; a < n; a++ {
		for b := 0; b+11 <= n; b++ {
			row := func(i int) bool { return q.At(i, a) }
			col := func(i int) bool { return q.At(a, i) }
			if match(row, b) {
				score += 40
			}
			if match(col, b) {
				score += 40
			}
		}
	}
	// Rule 4: deviation from an even split of dark and light.
	dark := 0
	for _, v := range q.mod {
		if v {
			dark++
		}
	}
	pct := dark * 100 / (n * n)
	prev := pct / 5 * 5       // nearest multiple of 5 at or below
	next := (pct + 4) / 5 * 5 // nearest multiple of 5 at or above
	a, b := abs(prev-50)/5, abs(next-50)/5
	if a < b {
		score += a * 10
	} else {
		score += b * 10
	}
	return score
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// DrawQR paints a QR symbol at (x, y), scale pixels per module, with a quiet
// zone of that many modules around it. Returns the total size in pixels.
//
// IT IS DRAWN INVERTED, and that is the whole point.
//
// A QR code is defined as DARK modules on a LIGHT field. An OLED is the other
// way round: it lights the pixels you set and everything else is black. Draw
// a symbol the obvious way -- lit modules on the dark panel -- and you have
// produced a photographic negative of a QR code. I did exactly that first and
// it did not decode at all, not as rendered and not with a dark border added;
// the detector found nothing, because there was nothing of the right polarity
// to find. Some phones invert as a fallback, which would have made this a bug
// that worked on the tester's handset and failed on the operator's.
//
// So: fill the area lit, which is the paper, and CLEAR the dark modules.
//
// The quiet zone is lit for the same reason -- it is part of the paper, not
// part of the bezel. The standard asks for four modules; at 2 pixels per
// module a version 2 symbol plus four modules each side is 66 pixels on a
// 64-pixel-high panel, so it does not fit. Two is what there is room for, and
// two is tested to decode rather than assumed to.
func (fb *Framebuffer) DrawQR(x, y int, q *QRCode, scale, quiet int) int {
	if scale < 1 {
		scale = 1
	}
	if quiet < 0 {
		quiet = 0
	}
	dim := (q.Size + 2*quiet) * scale
	fb.FillRect(x, y, dim, dim, true) // the paper
	ox, oy := x+quiet*scale, y+quiet*scale
	for my := 0; my < q.Size; my++ {
		for mx := 0; mx < q.Size; mx++ {
			if !q.At(mx, my) {
				continue
			}
			fb.FillRect(ox+mx*scale, oy+my*scale, scale, scale, false)
		}
	}
	return dim
}

// QRPixels reports how wide a symbol for this payload would be, so a caller
// can lay out around it before committing to draw.
func QRPixels(q *QRCode, scale, quiet int) int { return (q.Size + 2*quiet) * scale }

// WiFiURI builds the string a phone understands as "join this network".
//
//	WIFI:T:WPA;S:<ssid>;P:<key>;;
//
// iOS Camera and Android both offer a Join prompt for this, which turns the
// WiFi card from something you squint at and retype into something you point
// a camera at. That is the whole value: the AP key is the one credential on
// this device that is normally typed on a phone, with a thumb, standing up.
//
// The escaping is not optional. Backslash, semicolon, comma, colon and
// double quote are all structural in this format, so an SSID containing one
// -- and "P4wnP1; guest" is a perfectly legal SSID -- would otherwise end the
// field early and produce a QR that joins the wrong network, or no network,
// without looking any different.
func WiFiURI(ssid, key string) string {
	esc := func(s string) string {
		var b strings.Builder
		for _, r := range s {
			switch r {
			case '\\', ';', ',', ':', '"':
				b.WriteByte('\\')
			}
			b.WriteRune(r)
		}
		return b.String()
	}
	if key == "" {
		// An open network. T:nopass is the format's way of saying so; naming
		// WPA with no key would make a phone prompt for one that does not
		// exist.
		return "WIFI:T:nopass;S:" + esc(ssid) + ";;"
	}
	return "WIFI:T:WPA;S:" + esc(ssid) + ";P:" + esc(key) + ";;"
}
