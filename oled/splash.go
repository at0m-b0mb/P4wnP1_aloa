package oled

import "fmt"

// The boot splash.
//
// A device that sits on someone's desk pretending to be a USB stick should
// look like it was made on purpose when you do glance at it. On a 128x64
// 1bpp panel that means doing less, carefully: a wordmark with room around it,
// one hairline rule, and a single line of small type. No gradients to dither,
// no icon that would read as mush at 7 pixels.
//
// The house display face is a serif, which cannot survive at this size -- its
// brackets and modulated strokes land on nothing. So the wordmark is the body
// face at 2x with the slab underline doing the job the serif would: giving the
// name weight and a baseline to sit on.

// DrawSplash renders the boot screen. progress is 0..1; pass a negative number
// to omit the progress rule entirely.
func DrawSplash(fb *Framebuffer, version string, progress float64) {
	fb.Clear()

	// Wordmark, optically centred: text sits slightly above true centre so the
	// composition does not look bottom-heavy once the small type is under it.
	fb.TextScaledCentered(12, 2, "P4wnP1")

	// Slab under the wordmark, inset to the text width rather than the screen
	// width, so it reads as part of the mark.
	w := TextWidthScaled("P4wnP1", 2)
	x := (Width - w) / 2
	fb.FillRect(x, 28, w, 2, true)

	// Letterspaced subtitle. Spacing it out is what makes four initials read as
	// a mark rather than an abbreviation someone forgot to expand.
	fb.TextCentered(35, "A . L . O . A .")

	// The bar and the version share the lower band, so only one is shown. The
	// first render of this put the bar at y=50 and the version at y=55, which
	// left them touching with no air between -- visible immediately once it
	// was drawn, invisible while it was only arithmetic.
	switch {
	case progress >= 0:
		drawProgress(fb, 20, 50, Width-40, 6, progress)
	case version != "":
		fb.TextCentered(50, Truncate(version, Cols))
	}
}

// drawProgress is a hairline track with a filled bar. One pixel of padding
// inside the border, so the fill never touches it and muddies the edge.
func drawProgress(fb *Framebuffer, x, y, w, h int, p float64) {
	if p < 0 {
		p = 0
	}
	if p > 1 {
		p = 1
	}
	fb.Rect(x, y, w, h, true)
	inner := w - 4
	filled := int(float64(inner)*p + 0.5)
	if filled > 0 {
		fb.FillRect(x+2, y+2, filled, h-4, true)
	}
}

// DrawBootStep is the splash with a status line instead of a progress bar, for
// the stages where there is something specific to say -- "waiting for service"
// reads better than a bar that has stalled.
func DrawBootStep(fb *Framebuffer, step string) {
	fb.Clear()
	fb.TextScaledCentered(10, 2, "P4wnP1")
	w := TextWidthScaled("P4wnP1", 2)
	x := (Width - w) / 2
	fb.FillRect(x, 27, w, 2, true)
	fb.TextCentered(34, "A . L . O . A .")
	fb.HLine(8, 46, Width-16, true)
	fb.TextCentered(52, Truncate(step, Cols))
}

// DrawFatal is what the operator sees when the daemon cannot continue. It says
// what failed and what to do, because a device with a blank screen and a
// cryptic journal entry is a device that gets thrown in a drawer.
func DrawFatal(fb *Framebuffer, headline, detail string) {
	fb.Clear()
	// Draw the heading on black and THEN invert the band. Filling the band
	// first and drawing on top of it meant white text on white, and the invert
	// turned the whole lot black again -- a header that rendered as nothing at
	// all. It took looking at the output to notice; the code read fine.
	fb.Text(2, 1, Truncate(headline, Cols-1))
	fb.Invert(0, 0, Width, LineH+2)

	y := LineH + 6
	for _, line := range wrap(detail, Cols) {
		if y+GlyphH > Height {
			break
		}
		fb.Text(0, y, line)
		y += LineH
	}
}

// wrap breaks s into lines of at most n characters, splitting on spaces where
// it can and mid-word only when a single word is longer than the line.
func wrap(s string, n int) []string {
	if n <= 0 {
		return nil
	}
	var out []string
	cur := ""
	flush := func() {
		if cur != "" {
			out = append(out, cur)
			cur = ""
		}
	}
	for _, word := range splitSpaces(s) {
		for len([]rune(word)) > n {
			flush()
			r := []rune(word)
			out = append(out, string(r[:n]))
			word = string(r[n:])
		}
		switch {
		case cur == "":
			cur = word
		case len([]rune(cur))+1+len([]rune(word)) <= n:
			cur += " " + word
		default:
			flush()
			cur = word
		}
	}
	flush()
	return out
}

func splitSpaces(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\n' {
			if cur != "" {
				out = append(out, cur)
				cur = ""
			}
			continue
		}
		cur += string(r)
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}

// VersionLine formats the build stamp for the splash.
func VersionLine(version string) string {
	if version == "" {
		return ""
	}
	return fmt.Sprintf("v%s", version)
}
