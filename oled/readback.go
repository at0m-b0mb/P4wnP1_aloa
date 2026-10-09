package oled

import "strings"

// Reading the panel back as text.
//
// This began as a test helper: asserting on the words a screen shows beats
// asserting on a bitmap, because a failure then says what the panel says. It
// lives in the package proper now because the daemon serves it -- a remote
// mirror of a 128x64 panel is far more useful as twenty-one columns of text
// than as a PNG, for anything reading it rather than looking at it.

// fbText reads the screen back as text by matching 5x7 cells against the font.
// Asserting on words beats asserting on bitmaps: a failure says what the panel
// says.
//
// It SCANS rather than assuming a grid. The first version stepped x in CharW
// from zero, which silently missed every string the UI positions deliberately
// -- the title bar is inset one pixel, the confirm buttons sit at x=76 -- and
// reported them as unreadable when they rendered perfectly. A reader that only
// sees text which happens to be grid-aligned is a reader that lies.
func ReadBack(fb *Framebuffer) string {
	var out strings.Builder
	for row := 0; row < Rows; row++ {
		// Every string the UI draws sits on an exact row boundary, so there
		// is nothing to search for vertically. An earlier version tried row
		// +/-1 and picked whichever matched most glyphs, which ranked the
		// boundary line of an inverted bar -- fifteen cells that each look
		// like an underscore -- above the six real characters next to it.
		line, _ := scanRow(fb, row*LineH)
		out.WriteString(strings.TrimRight(line, " "))
		out.WriteByte('\n')
	}
	return out.String()
}

// scanRow walks x across one pixel row, emitting a character whenever the cell
// at x matches a glyph. Scanning rather than stepping in CharW from zero,
// because the UI positions some text deliberately off the grid -- the title
// is inset one pixel and the confirm buttons sit at x=76.
func scanRow(fb *Framebuffer, y int) (string, int) {
	var b strings.Builder
	score := 0
	for x := 0; x+GlyphW <= Width; {
		r, ok := cellRune(fb, x, y)
		if !ok {
			b.WriteByte(' ')
			x++
			continue
		}
		b.WriteRune(r)
		if r != ' ' {
			score++
		}
		x += CharW
	}
	return b.String(), score
}

// cellRune matches one 5x7 cell, trying the cell and its complement so text on
// an inverted bar reads the same as text on black.
func cellRune(fb *Framebuffer, x, y int) (rune, bool) {
	var cell, inv [5]byte
	for c := 0; c < GlyphW; c++ {
		var bb, ib byte
		for r := 0; r < GlyphH; r++ {
			if fb.Get(x+c, y+r) {
				bb |= 1 << uint(r)
			} else {
				ib |= 1 << uint(r)
			}
		}
		cell[c], inv[c] = bb, ib
	}
	// Skip anything the drawing code marked as a graphic.
	//
	// There is NO local way to tell a QR module from inverted punctuation.
	// A cell showing an inverted '.' is thirty-three lit pixels with two
	// dark ones in the bottom middle -- and a block of QR "paper" with one
	// dark module in the same place is the identical cell. I tried
	// rejecting complement matches on sparse glyphs, and it worked until a
	// selected job row showed "win_recon.js" on an inverted bar, where the
	// '.' is real and had to read.
	//
	// So the drawing code says which rectangles are pictures, and this skips
	// them. Explicit beats clever: a QR code is not text and no heuristic
	// should have to guess that.
	if fb.inGraphic(x, y) {
		return 0, false
	}
	for i, g := range font5x7 {
		if g == cell || g == inv {
			return rune(0x20 + i), true
		}
	}
	return 0, false
}
