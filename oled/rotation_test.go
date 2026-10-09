package oled

import "testing"

// The HAT can hang either way up. With a Zero W in a laptop's USB port the
// board sits with its plug to the LEFT and KEY1..KEY3 to the RIGHT, and in
// that position the panel's own orientation reads upside down. Every line
// mirrored is far harder to read than small text has any right to be, and it
// was being blamed on the font.
//
// These assert the command bytes rather than pixels because that is where the
// rotation now happens -- in the controller, not the framebuffer -- so no
// amount of rendering a frame would show it.
func TestInitSequenceRotation(t *testing.T) {
	has := func(seq []byte, b byte) bool {
		for _, x := range seq {
			if x == b {
				return true
			}
		}
		return false
	}

	for _, c := range []Controller{SH1106, SSD1306} {
		t.Run("default is the keys-match orientation", func(t *testing.T) {
			seq := c.initSequenceRotated(RotateNone)
			if has(seq, cmdSegRemap) || has(seq, cmdComScanDec) {
				t.Errorf("RotateNone still sends the remap commands; "+
					"the default is supposed to be the OTHER way up now (%v)", c)
			}
		})

		t.Run("180 sends both commands", func(t *testing.T) {
			seq := c.initSequenceRotated(Rotate180)
			// Both, or it is a mirror image rather than a rotation: SegRemap
			// alone flips left/right, ComScanDec alone flips top/bottom.
			// Either one on its own produces text that is backwards, which
			// looks like a corrupt font rather than a wrong setting.
			if !has(seq, cmdSegRemap) {
				t.Error("Rotate180 does not send SegRemap -- vertical mirror, not a rotation")
			}
			if !has(seq, cmdComScanDec) {
				t.Error("Rotate180 does not send ComScanDec -- horizontal mirror, not a rotation")
			}
		})

		t.Run("rotation changes nothing else", func(t *testing.T) {
			// Strip the two rotation bytes from the 180 sequence and it must
			// equal the default one. This is what stops a future edit from
			// quietly changing the charge pump or the multiplex ratio on only
			// one of the two paths -- which would show up as a dead panel in
			// exactly one orientation.
			var stripped []byte
			for _, b := range c.initSequenceRotated(Rotate180) {
				if b == cmdSegRemap || b == cmdComScanDec {
					continue
				}
				stripped = append(stripped, b)
			}
			base := c.initSequenceRotated(RotateNone)
			if len(stripped) != len(base) {
				t.Fatalf("%v: sequences differ by more than the rotation bytes: %d vs %d",
					c, len(stripped), len(base))
			}
			for i := range base {
				if base[i] != stripped[i] {
					t.Fatalf("%v: byte %d differs outside the rotation: %#x vs %#x",
						c, i, base[i], stripped[i])
				}
			}
		})
	}

	t.Run("the SH1106 column offset is unchanged by rotation", func(t *testing.T) {
		// Its RAM is 132 wide with the 128-pixel glass centred on
		// SEG2..SEG129, so reversing the segment mapping puts pixel 0 at RAM
		// column 131-129 = 2 -- the same offset, from the other end. If that
		// reasoning is ever wrong the symptom is a two-pixel shift with the
		// right-hand edge wrapping, so it is worth stating out loud.
		if got := SH1106.colOffset(); got != 2 {
			t.Errorf("SH1106 colOffset = %d, want 2", got)
		}
		if got := SSD1306.colOffset(); got != 0 {
			t.Errorf("SSD1306 colOffset = %d, want 0", got)
		}
	})
}

func TestParseRotation(t *testing.T) {
	for in, want := range map[string]Rotation{
		"180": Rotate180, " 180 ": Rotate180,
		"0": RotateNone, "": RotateNone,
		// A typo must not stop the panel coming up at all; it falls back to
		// the default rather than refusing to start.
		"upside-down": RotateNone, "90": RotateNone, "360": RotateNone,
	} {
		if got := ParseRotation(in); got != want {
			t.Errorf("ParseRotation(%q) = %v, want %v", in, got, want)
		}
	}
}
