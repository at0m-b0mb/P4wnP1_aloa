package oled

import (
	"fmt"
	"testing"
)

// glyphPixels renders one glyph as a 7-row bitmap for comparison.
func glyphPixels(ch rune) [7][5]bool {
	var out [7][5]bool
	g := font5x7[ch-0x20]
	for c := 0; c < 5; c++ {
		for r := 0; r < 7; r++ {
			out[r][c] = (g[c]>>uint(r))&1 == 1
		}
	}
	return out
}

func glyphDistance(a, b rune) int {
	x, y, n := glyphPixels(a), glyphPixels(b), 0
	for r := 0; r < 7; r++ {
		for c := 0; c < 5; c++ {
			if x[r][c] != y[r][c] {
				n++
			}
		}
	}
	return n
}

// The panel shows randomly generated passwords, and the operator has to copy
// them off a 1.3 inch screen by eye. A character pair that differs by one or
// two pixels is not a pair -- it is one character with two meanings, and the
// cost lands on someone typing a 20-character string that then does not work,
// with no way to tell which character they got wrong.
//
// 'l' used to differ from '1' by a SINGLE pixel. This is the gate that stopped
// that being true, and the threshold is deliberately a floor rather than a
// description of today: it has to fail if someone redraws a glyph and takes
// the separation back out.
func TestConfusableGlyphsStayDistinguishable(t *testing.T) {
	const minPixels = 3

	for _, p := range []struct{ a, b rune }{
		{'1', 'l'}, {'1', 'I'}, {'l', 'I'},
		{'0', 'O'}, {'0', 'o'},
		{'5', 'S'}, {'2', 'Z'}, {'8', 'B'}, {'6', 'G'},
		{'9', 'g'}, {'U', 'V'},
	} {
		t.Run(fmt.Sprintf("%c vs %c", p.a, p.b), func(t *testing.T) {
			if d := glyphDistance(p.a, p.b); d < minPixels {
				t.Errorf("'%c' and '%c' differ by only %d pixel(s); want at least %d.\n%s",
					p.a, p.b, d, minPixels, sideBySide(p.a, p.b))
			}
		})
	}
}

// sideBySide prints two glyphs next to each other, because a failure here is
// about what something LOOKS like and a number alone cannot show that.
func sideBySide(a, b rune) string {
	x, y := glyphPixels(a), glyphPixels(b)
	s := fmt.Sprintf("        '%c'     '%c'\n", a, b)
	for r := 0; r < 7; r++ {
		s += "        "
		for c := 0; c < 5; c++ {
			s += map[bool]string{true: "#", false: "."}[x[r][c]]
		}
		s += "   "
		for c := 0; c < 5; c++ {
			s += map[bool]string{true: "#", false: "."}[y[r][c]]
		}
		s += "\n"
	}
	return s
}

// Zero must keep its slash. It is the one glyph where the distinguishing mark
// is interior rather than a change of outline, so it is the easiest to lose
// by accident while "tidying up" the font.
func TestZeroIsSlashed(t *testing.T) {
	zero, oh := glyphPixels('0'), glyphPixels('O')
	interior := 0
	for r := 1; r < 6; r++ {
		for c := 1; c < 4; c++ {
			if zero[r][c] && !oh[r][c] {
				interior++
			}
		}
	}
	if interior < 2 {
		t.Errorf("'0' has %d interior pixels that 'O' lacks; it has lost its slash.\n%s",
			interior, sideBySide('0', 'O'))
	}
}
