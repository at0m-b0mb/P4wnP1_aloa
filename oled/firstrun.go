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
	// WiFiPSK is the access point's per-device key. No SSID: the device
	// broadcasts that, so it is discoverable by scanning and is not a
	// secret -- and it is often emoji, which this font cannot draw.
	WiFiPSK string
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
}

func NewFirstRunView(c *FirstRunCreds) *FirstRunView { return &FirstRunView{c: c} }

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

type credCard struct{ head, user, pass, note string }

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
		out = append(out, credCard{
			head: "WiFi access point",
			user: "key:",
			pass: v.c.WiFiPSK,
			note: "SSID is broadcast; scan for it.",
		})
	}
	return out
}

func (v *FirstRunView) Render(fb *Framebuffer, _ *App) {
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

	cards := v.cards()
	if v.page >= len(cards) {
		v.page = len(cards) - 1
	}
	c := cards[v.page]
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
