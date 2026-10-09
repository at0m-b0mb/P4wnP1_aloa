package oled

import (
	"bufio"
	"crypto/rand"
	"fmt"
	"os"
	"strings"
)

// First-boot credentials, shown on the panel instead of left on the card.
//
// A headless appliance has to let you in and has no way to tell you a secret,
// so first boot generates a password and writes it to the FAT boot partition
// -- the one place readable from the laptop you flashed with, and therefore
// the one place readable by anyone who later holds the card. That exposure
// lasts for the life of the device, because nothing ever removes the file.
//
// A panel changes the arithmetic. The device CAN tell you a secret: it can
// show you. So it shows you, you confirm you have written it down, and then
// the files go. The window narrows from "forever, to anyone holding the card"
// to "once, to whoever is standing over the device at first boot".
//
// Two rules this must never break:
//
//   - NOTHING is deleted until the operator acknowledges on the panel. No
//     timer, no delete-on-display. A credential erased before it was read
//     bricks the device, which is the same failure as never writing it, just
//     arrived at from the other side.
//   - The erase is HONEST about what it is. Overwriting a file on an SD card
//     does not reliably destroy it: the flash translation layer may have
//     already remapped those blocks, and the old contents can survive in
//     pages the filesystem can no longer address. This reduces the exposure.
//     It does not guarantee anything, and the panel says so.

// FirstRunFile is where first boot leaves the handoff for the panel.
// Under /var/lib rather than /root because the daemon's unit sets
// ProtectHome, and because this is state, not a home directory.
const FirstRunFile = "/var/lib/p4wnp1/firstboot-creds"

// FirstRunCreds is what first boot wants the operator to see once.
type FirstRunCreds struct {
	SSHUser string
	SSHPass string
	WebUser string
	WebPass string
	// WiFiPSK is the access point's per-device key.
	WiFiPSK string
	// WiFiSSID is carried for the QR code ONLY, never drawn as text.
	//
	// The handoff used to omit it entirely, on the grounds that the SSID is
	// broadcast anyway and is often emoji that a 5x7 font renders as a row
	// of question marks. Both true, and neither applies to a QR: it encodes
	// BYTES, not glyphs, and a join URI needs the network's name whether or
	// not a human can read it off the panel. Without it the WiFi card can
	// only offer the key, which still has to be typed with a thumb.
	WiFiSSID string
	// Erase lists every file holding a copy, including this one. The panel
	// shreds exactly this list and nothing it inferred for itself.
	Erase []string
}

// LoadFirstRunCreds reads the handoff, or returns nil if there is none --
// which is the normal case on every boot after the first.
func LoadFirstRunCreds(path string) *FirstRunCreds {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	c := &FirstRunCreds{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch strings.TrimSpace(k) {
		case "ssh_user":
			c.SSHUser = v
		case "ssh_pass":
			c.SSHPass = v
		case "web_user":
			c.WebUser = v
		case "web_pass":
			c.WebPass = v
		case "wifi_psk":
			c.WiFiPSK = v
		case "wifi_ssid":
			c.WiFiSSID = v
		case "erase":
			if v != "" {
				c.Erase = append(c.Erase, v)
			}
		}
	}
	if sc.Err() != nil {
		return nil
	}
	if c.SSHPass == "" && c.WebPass == "" && c.WiFiPSK == "" {
		// Nothing worth showing. A key-only device reaches this.
		return nil
	}
	// The handoff always erases itself, whether or not it said so.
	if !contains(c.Erase, path) {
		c.Erase = append(c.Erase, path)
	}
	return c
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// Shred overwrites each file with random bytes, flushes, and unlinks it.
//
// Returns the paths it could NOT remove, so the panel can name them rather
// than claim a clean sweep. A file that was already gone is not a failure.
func Shred(paths []string) []string {
	var failed []string
	for _, p := range paths {
		if err := shredOne(p); err != nil {
			failed = append(failed, p)
		}
	}
	return failed
}

func shredOne(path string) error {
	fi, err := os.Stat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if fi.Mode().IsRegular() && fi.Size() > 0 {
		// Best effort: a failure to overwrite must not stop the unlink. An
		// unlinked file is strictly better than an overwritten one that is
		// still sitting there with its name on it.
		if f, err := os.OpenFile(path, os.O_WRONLY, 0); err == nil {
			buf := make([]byte, 4096)
			for left := fi.Size(); left > 0; {
				n := int64(len(buf))
				if left < n {
					n = left
				}
				if _, err := rand.Read(buf[:n]); err != nil {
					break
				}
				if _, err := f.Write(buf[:n]); err != nil {
					break
				}
				left -= n
			}
			_ = f.Sync()
			_ = f.Close()
		}
	}
	return os.Remove(path)
}

// --- the screen -------------------------------------------------------------

// FirstRunView shows the credentials one card at a time and offers the erase.
type FirstRunView struct {
	c    *FirstRunCreds
	page int
	done bool     // the erase has run
	left []string // what the erase could not remove

	// liveSSID is what the radio is actually broadcasting, read once from
	// the service; ssidDone records that we asked, so a device with no WiFi
	// is not re-queried on every redraw.
	liveSSID string
	ssidDone bool
}

func NewFirstRunView(c *FirstRunCreds) *FirstRunView { return &FirstRunView{c: c} }

// resolveSSID asks the service what the AP is actually broadcasting, once.
//
// The handoff's wifi_ssid comes from install.sh via /etc/p4wnp1/initial.conf,
// and that is NOT necessarily what is on air: the AP broadcasts whatever the
// deployed template says, and on a stock image those differ. Printing the
// wrong one is irritating. Encoding the wrong one in a join QR produces a
// code that cannot work and gives no clue why, so the live value wins and the
// handoff is only a fallback for when the service cannot be reached.
func (v *FirstRunView) resolveSSID(app *App) {
	if v.ssidDone || app == nil || app.Client == nil {
		return
	}
	v.ssidDone = true
	if live, err := app.Client.LiveSSID(); err == nil && live != "" {
		v.liveSSID = live
	}
}

// ssid is the name to put in a join code: live if we have it, otherwise
// whatever first boot wrote down.
func (v *FirstRunView) ssid() string {
	if v.liveSSID != "" {
		return v.liveSSID
	}
	return v.c.WiFiSSID
}

func (v *FirstRunView) Title() string { return "First boot" }

// Secret keeps this screen off the remote mirror.
//
// The entire point of showing a password on the panel is that it reaches
// whoever is STANDING OVER THE DEVICE and then stops existing. Streaming it
// to a browser hands it to anyone holding a console session and quietly
// undoes that. Once erased there is nothing left to withhold, so the screen
// mirrors again -- which also means a remote operator can see that the erase
// happened.
func (v *FirstRunView) Secret() bool { return !v.done }

func (v *FirstRunView) Hint() string {
	if v.done {
		return "left to finish"
	}
	return fmt.Sprintf("%d/%d  KEY1 erase", v.page+1, len(v.cards()))
}

type credCard struct {
	head, user, pass, note string
	// qr overrides what the QR encodes, when that should differ from the
	// text shown. The WiFi card uses it to carry a join URI while still
	// printing the bare key for anyone without a camera.
	qr string
}

// qrMaxModules is the largest symbol that fits the panel.
//
// 64 pixels high, two pixels per module to scan reliably, and at least one
// module of quiet zone on each side: (31 + 2) * 2 = 66 is already too tall,
// so 31 modules is the ceiling and version 3 (29 modules) is the largest
// usable version. Stated once here because two places depend on it and they
// must not drift apart.
const qrMaxModules = 31

func (v *FirstRunView) cards() []credCard {
	var out []credCard
	out = append(out, credCard{
		head: "Write these down.",
		note: "They are shown once. Press KEY1 when you have them.",
	})
	if v.c.SSHPass != "" {
		out = append(out, credCard{
			head: "SSH  172.16.0.1",
			user: "user " + v.c.SSHUser,
			pass: v.c.SSHPass,
		})
	}
	if v.c.WebPass != "" {
		out = append(out, credCard{
			head: "Web  :8000",
			user: "user " + v.c.WebUser,
			pass: v.c.WebPass,
		})
	}
	if v.c.WiFiPSK != "" {
		card := credCard{head: "WiFi join", user: "key:", pass: v.c.WiFiPSK}
		// Encode a JOIN URI rather than the bare key when the SSID is known
		// and the result still fits. A phone then offers "join this
		// network" instead of handing over a string to retype -- and the AP
		// key is the one credential here that normally gets typed on a
		// phone, with a thumb, standing up.
		//
		// It does not always fit: a join URI is the key plus ~18 bytes of
		// structure plus the entire SSID, and a long or emoji-heavy SSID
		// blows past what 31 modules hold. Then the card falls back to the
		// key alone and SAYS so, because a QR that quietly means something
		// other than what the operator expects is worse than no QR.
		if ssid := v.ssid(); ssid != "" {
			uri := WiFiURI(ssid, v.c.WiFiPSK)
			if _, err := NewQRMax([]byte(uri), qrMaxModules); err == nil {
				card.qr, card.note = uri, "scan to join"
			} else {
				card.note = "key only: SSID long"
			}
		}
		out = append(out, card)
	}
	return out
}

func (v *FirstRunView) Render(fb *Framebuffer, app *App) {
	if v.done {
		y := bodyPxTop
		for _, line := range v.doneLines() {
			if y+GlyphH > hintRow*LineH-2 {
				break
			}
			fb.Text(0, y, Truncate(line, Cols))
			y += LineH
		}
		return
	}

	v.resolveSSID(app)
	cards := v.cards()
	if v.page >= len(cards) {
		v.page = len(cards) - 1
	}
	c := cards[v.page]

	// A card carrying a secret is drawn as a QR code with the text beside
	// it, using the whole panel. See renderQRCard.
	if c.pass != "" {
		v.renderQRCard(fb, c, len(cards))
		return
	}

	y := bodyPxTop
	fb.Text(0, y, Truncate(c.head, Cols))
	y += LineH
	if c.user != "" {
		fb.Text(0, y, Truncate(c.user, Cols))
		y += LineH
	}
	if c.pass != "" {
		// The password gets its own inverted line. On a 21-column panel a
		// 20-character secret has to be unmistakable: if you cannot tell
		// where it starts and stops you will mistype it, and a device you
		// cannot log into is the whole problem this screen exists to fix.
		//
		// Text on the row, highlight from one pixel above -- the same shape
		// the list rows use. The first version drew the text one pixel LOWER
		// instead, which rendered a line the framebuffer reader could not
		// find at all: on the panel it would have been a bright empty bar
		// where the password should be.
		fb.Text(0, y, Truncate(c.pass, Cols))
		fb.Invert(0, y-1, Width, LineH)
		y += LineH
	}
	if c.note != "" {
		for _, line := range wrap(c.note, Cols) {
			if y+GlyphH > hintRow*LineH-2 {
				break
			}
			fb.Text(0, y, line)
			y += LineH
		}
	}
}

// Chromeless reports that the secret cards draw the whole panel themselves.
// Only those: the opening card and the "erased" summary are ordinary screens
// and keep the title bar and the hint line.
func (v *FirstRunView) Chromeless() bool {
	if v.done {
		return false
	}
	cards := v.cards()
	if v.page < 0 || v.page >= len(cards) {
		return false
	}
	return cards[v.page].pass != ""
}

// renderQRCard draws a secret as a QR code with its text beside it.
//
// The arithmetic is forced and worth stating, because it is why this screen
// looks the way it does. A 20-character password needs a version 2 symbol,
// 25 modules square. Two pixels per module is the smallest that decodes
// reliably off a 1.3 inch panel, and the quiet zone cannot be dropped -- at
// zero quiet modules nothing decodes at all, measured, not assumed. That is
// (25 + 2*2) * 2 = 58 pixels, on a panel 64 pixels high. The title bar and
// hint line together are 16 of those, so they go: hence Chromeless.
//
// What is left is 128-58-2 = 68 pixels of width, eleven characters per line.
// The password is split across two of them rather than truncated, because a
// password you can only see half of is no better than one you cannot see.
func (v *FirstRunView) renderQRCard(fb *Framebuffer, c credCard, total int) {
	const (
		scale = 2
		gap   = 3
	)
	payload := c.pass
	if c.qr != "" {
		payload = c.qr
	}
	textX, dim := 0, 0
	if q, err := NewQRMax([]byte(payload), qrMaxModules); err == nil {
		// Quiet zone of 2 modules where there is room, 1 where there is not.
		//
		// Not a detail. A version 2 symbol (a bare password) is 58 pixels at
		// quiet 2 and fits easily. A version 3 symbol (a WiFi join URI) is
		// 66 at quiet 2, which is TALLER THAN THE PANEL -- so the fits-check
		// below suppressed the code entirely and the card silently became
		// text-only. The join URI was encoded correctly and never drawn.
		// At quiet 1 it is 62 and fits, and quiet 1 is measured to decode.
		// Zero is not an option: with no quiet zone nothing decodes at all.
		for _, quiet := range []int{2, 1} {
			d := QRPixels(q, scale, quiet)
			if d <= Height && d+gap+CharW*9 <= Width {
				fb.DrawQR(0, (Height-d)/2, q, scale, quiet)
				textX, dim = d+gap, d
				break
			}
		}
	}
	_ = dim
	// A QR that could not be built is not a reason to show nothing: the
	// password still has to reach the operator, so the text column simply
	// takes the whole width instead.
	cols := (Width - textX) / CharW

	// Text sits on exact LineH boundaries. Not cosmetic: ReadBack scans rows
	// at multiples of LineH, and it is what serves /panel.txt to the web
	// console and what the tests read. Drawing this column at y=2 looked
	// perfect on the glass and rendered a COMPLETELY EMPTY card to both --
	// the password was on the panel and nowhere else.
	y := 0
	line := func(sv string, invert bool) {
		if y+GlyphH > Height {
			return
		}
		fb.Text(textX, y, Truncate(sv, cols))
		if invert {
			fb.Invert(textX, y-1, Width-textX, LineH)
		}
		y += LineH
	}

	line(c.head, false)
	if c.user != "" {
		line(c.user, false)
	}
	// The secret itself, wrapped rather than cut, and inverted so it is
	// obvious where it starts and stops.
	for _, part := range chunkString(c.pass, cols) {
		line(part, true)
	}
	// The page counter and the erase key, which the hint line would normally
	// carry. Pushed to the bottom of the column so they do not crowd the
	// password.
	y = Height - 2*LineH
	line(fmt.Sprintf("%d/%d", v.page+1, total), false)
	line("KEY1 erase", false)
}

// chunkString splits s into runs of at most n characters.
func chunkString(s string, n int) []string {
	if n <= 0 {
		return []string{s}
	}
	var out []string
	for len(s) > n {
		out = append(out, s[:n])
		s = s[n:]
	}
	if s != "" {
		out = append(out, s)
	}
	return out
}

func (v *FirstRunView) doneLines() []string {
	if len(v.left) == 0 {
		return []string{"Erased.", "", "The copies on disk and",
			"on the card are gone.", "SD flash may still hold", "fragments."}
	}
	out := []string{"Erased what it could.", "Still on disk:"}
	for _, p := range v.left {
		out = append(out, shortPath(p))
	}
	out = append(out, "Remove it over ssh.")
	return out
}

// shortPath keeps the end of a path, which is the part that identifies it.
func shortPath(p string) string {
	if len(p) <= Cols {
		return p
	}
	return "~" + p[len(p)-(Cols-1):]
}

func (v *FirstRunView) Handle(b Button, app *App) Action {
	if v.done {
		if b == BtnBack || b == BtnConfirm || b == BtnHome {
			return ActHome
		}
		return ActNone
	}
	switch b {
	case BtnUp, BtnLeft:
		if v.page > 0 {
			v.page--
			return ActNone
		}
		return ActPop
	case BtnDown, BtnRight, BtnConfirm:
		if v.page < len(v.cards())-1 {
			// No toast: the hint line already carries the page counter, and
			// a toast would cover the one thing that must stay on screen --
			// "KEY1 erase".
			v.page++
		} else {
			app.Toast("KEY1 erases these")
		}
	case BtnHome:
		return ActHome
	case BtnAction:
		app.Push(NewConfirm("Erase", "Erased for good. Written them down?",
			"Nothing can show them again. If you have not, choose No.",
			func(a *App) {
				v.left = Shred(v.c.Erase)
				v.done = true
				if len(v.left) == 0 {
					a.Toast("erased")
				} else {
					a.Toast("%d left on disk", len(v.left))
				}
			}))
	}
	return ActNone
}
