package oled

import (
	"bytes"
	"testing"
)

// HOW THIS ENCODER WAS ACTUALLY VERIFIED
//
// By decoding its output with a real decoder, not by these tests. Everything
// it produced -- a 20-character password, a 61-byte version 4 payload, a
// WIFI: URI, and the panel rendering at 2 pixels per module -- was written to
// PNG and read back with OpenCV's QRCodeDetector, and had to come out byte
// for byte. That is the only check that proves the bytes on the glass mean
// what they are supposed to mean.
//
// It cannot live in CI: OpenCV is not a dependency of this repo and will not
// be added for one screen. So it was run by hand, and these tests exist to
// stop a later edit silently breaking what it confirmed. They are regression
// guards standing on an external result, not a substitute for one. If the
// placement or masking is ever reworked, decode the output again for real --
// a QR that scans to the wrong bytes fails silently, and the operator blames
// their phone.
//
// Two things the external pass caught that no structural test would have:
// the symbol must be drawn INVERTED on an OLED (lit paper, unlit modules),
// and the quiet zone is load-bearing -- with zero quiet modules nothing
// decodes at all.

// readBack walks the finished symbol in reverse: undo the mask, read the
// zigzag, and reassemble the data codewords. It deliberately does NOT reuse
// placeData, so a self-consistent placement bug cannot hide here.
func readBack(t *testing.T, q *QRCode, mask int, v qrVersion) []byte {
	t.Helper()
	n := q.Size
	var bits []bool
	up := true
	for right := n - 1; right >= 1; right -= 2 {
		if right == 6 {
			right = 5
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
				b := q.At(x, y)
				if maskAt(mask, x, y) {
					b = !b
				}
				bits = append(bits, b)
			}
		}
		up = !up
	}
	out := make([]byte, len(bits)/8)
	for i := 0; i < len(out)*8; i++ {
		if bits[i] {
			out[i/8] |= 1 << uint(7-i%8)
		}
	}
	// Versions with more than one ECC block interleave their codewords, so
	// the stream read off the matrix is not the data stream. Version 4 level
	// M has two blocks; without undoing that, the length byte alone comes
	// back as 32 for a 44-byte payload. The encoder was right -- a real
	// decoder read that same symbol correctly -- and this read-back was
	// simply not finishing the job.
	if v.blocks > 1 {
		perBlock := v.dataBytes / v.blocks
		data := make([]byte, v.dataBytes)
		for i := 0; i < perBlock; i++ {
			for b := 0; b < v.blocks; b++ {
				data[b*perBlock+i] = out[i*v.blocks+b]
			}
		}
		return data
	}
	return out
}

// findMask recovers the mask from the format information, which is also a
// check that the format bits were written where a decoder looks for them.
func findMask(t *testing.T, q *QRCode) int {
	t.Helper()
	var bits int
	for i := 0; i <= 5; i++ {
		if q.At(8, i) {
			bits |= 1 << uint(i)
		}
	}
	if q.At(8, 7) {
		bits |= 1 << 6
	}
	if q.At(8, 8) {
		bits |= 1 << 7
	}
	if q.At(7, 8) {
		bits |= 1 << 8
	}
	for i := 9; i <= 14; i++ {
		if q.At(14-i, 8) {
			bits |= 1 << uint(i)
		}
	}
	unmasked := bits ^ 0x5412
	level := (unmasked >> 13) & 0b11
	if level != 0b00 {
		t.Errorf("format info says error correction level %02b, want 00 (M)", level)
	}
	return (unmasked >> 10) & 0b111
}

func TestQRRoundTripsItsPayload(t *testing.T) {
	for _, payload := range []string{
		"abc",
		"YXpXK1UdUyM5069A3XAM", // a real 20-char generated password
		"lI1l0OoIl1Ii0O",       // the confusable characters the QR exists to avoid
		"WIFI:T:WPA;S:P4wnP1;P:bGu6hrPUPlburycwcsMs;;",
	} {
		t.Run(payload[:min(12, len(payload))], func(t *testing.T) {
			q, err := NewQR([]byte(payload))
			if err != nil {
				t.Fatalf("encode: %v", err)
			}
			v, err := pickVersion(len(payload))
			if err != nil {
				t.Fatal(err)
			}
			got := readBack(t, q, findMask(t, q), v)

			// Mode nibble, then an 8-bit length, then the bytes.
			if got[0]>>4 != 0b0100 {
				t.Fatalf("mode nibble is %04b, want 0100 (byte mode)", got[0]>>4)
			}
			n := int(got[0]&0x0F)<<4 | int(got[1]>>4)
			if n != len(payload) {
				t.Fatalf("length field says %d, payload is %d", n, len(payload))
			}
			recovered := make([]byte, n)
			for i := 0; i < n; i++ {
				recovered[i] = got[1+i]<<4 | got[2+i]>>4
			}
			if !bytes.Equal(recovered, []byte(payload)) {
				t.Errorf("round trip lost the payload\n got: %q\nwant: %q", recovered, payload)
			}
		})
	}
}

func TestQRVersionSelection(t *testing.T) {
	for _, c := range []struct {
		n       int
		version int
	}{
		{1, 1}, {14, 1}, // version 1 level M holds 14 bytes
		{15, 2}, {26, 2},
		{27, 3}, {42, 3},
		{43, 4}, {62, 4},
	} {
		q, err := NewQR(bytes.Repeat([]byte("x"), c.n))
		if err != nil {
			t.Fatalf("%d bytes: %v", c.n, err)
		}
		if q.Version != c.version {
			t.Errorf("%d bytes chose version %d, want %d", c.n, q.Version, c.version)
		}
	}
	if _, err := NewQR(bytes.Repeat([]byte("x"), 63)); err == nil {
		t.Error("63 bytes was accepted; version 4 level M does not hold it")
	}
	if _, err := NewQR(nil); err == nil {
		t.Error("an empty payload was accepted")
	}
}

// The three finder patterns, the timing lines and the dark module are what a
// decoder locks onto before it reads anything. If any of them is misplaced
// the symbol is not found at all.
func TestQRFunctionPatterns(t *testing.T) {
	q, err := NewQR([]byte("YXpXK1UdUyM5069A3XAM"))
	if err != nil {
		t.Fatal(err)
	}
	n := q.Size
	for _, o := range [][2]int{{0, 0}, {n - 7, 0}, {0, n - 7}} {
		ox, oy := o[0], o[1]
		if !q.At(ox, oy) || !q.At(ox+6, oy+6) || !q.At(ox+3, oy+3) {
			t.Errorf("finder at (%d,%d) is not solid where it must be", ox, oy)
		}
		if q.At(ox+1, oy+1) || q.At(ox+5, oy+5) {
			t.Errorf("finder at (%d,%d) has no light ring", ox, oy)
		}
	}
	for i := 8; i < n-8; i++ {
		if q.At(i, 6) != (i%2 == 0) {
			t.Fatalf("horizontal timing pattern wrong at %d", i)
		}
		if q.At(6, i) != (i%2 == 0) {
			t.Fatalf("vertical timing pattern wrong at %d", i)
		}
	}
	if !q.At(8, 4*q.Version+9) {
		t.Error("the dark module is not set")
	}
}

// Drawing. The inversion and the quiet zone are both things the external
// decode proved necessary, so both are pinned here.
func TestDrawQRIsInvertedAndQuiet(t *testing.T) {
	q, err := NewQR([]byte("YXpXK1UdUyM5069A3XAM"))
	if err != nil {
		t.Fatal(err)
	}
	const scale, quiet = 2, 2
	fb := NewFramebuffer()
	dim := fb.DrawQR(0, 0, q, scale, quiet)

	if want := (q.Size + 2*quiet) * scale; dim != want {
		t.Errorf("DrawQR returned %d, want %d", dim, want)
	}
	if dim > Height {
		t.Errorf("a %d pixel symbol does not fit a %d pixel panel", dim, Height)
	}
	// The quiet zone must be LIT: it is the paper, not the bezel.
	for i := 0; i < quiet*scale; i++ {
		if !fb.Get(i, 0) || !fb.Get(0, i) {
			t.Fatalf("quiet zone pixel (%d,0) is dark; the symbol is not inverted", i)
		}
	}
	// A dark module must be UNLIT, and a light one LIT -- the opposite of
	// the obvious way round, which does not decode.
	ox := quiet * scale
	if q.At(0, 0) == fb.Get(ox, ox) {
		t.Error("module polarity is not inverted; an OLED drawn the obvious " +
			"way round is a photographic negative of a QR code")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
