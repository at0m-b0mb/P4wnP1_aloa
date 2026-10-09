package oled

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeHandoff(t *testing.T, dir, body string) string {
	t.Helper()
	p := filepath.Join(dir, "firstboot-creds")
	if err := os.WriteFile(p, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadFirstRunCreds(t *testing.T) {
	dir := t.TempDir()
	boot := filepath.Join(dir, "p4wnp1-credentials.txt")
	if err := os.WriteFile(boot, []byte("password: hunter2\n"), 0644); err != nil {
		t.Fatal(err)
	}
	p := writeHandoff(t, dir, "# comment\nweb_user=admin\nweb_pass=WEBPASSWORD123\n"+
		"ssh_user=p4wnp1\nssh_pass=SSHPASSWORD456\nerase="+boot+"\n")

	c := LoadFirstRunCreds(p)
	if c == nil {
		t.Fatal("handoff did not load")
	}
	if c.WebUser != "admin" || c.WebPass != "WEBPASSWORD123" {
		t.Errorf("web creds = %q/%q", c.WebUser, c.WebPass)
	}
	if c.SSHUser != "p4wnp1" || c.SSHPass != "SSHPASSWORD456" {
		t.Errorf("ssh creds = %q/%q", c.SSHUser, c.SSHPass)
	}
	// The handoff must always erase itself, listed or not. Leaving behind the
	// file that exists purely to carry the secret would defeat the feature.
	if !contains(c.Erase, p) {
		t.Errorf("the handoff does not erase itself: %v", c.Erase)
	}
	if !contains(c.Erase, boot) {
		t.Errorf("the card copy is not in the erase list: %v", c.Erase)
	}

	if LoadFirstRunCreds(filepath.Join(dir, "nope")) != nil {
		t.Error("a missing handoff produced credentials")
	}
	// A key-only device has no password to show.
	q := writeHandoff(t, t.TempDir(), "ssh_user=p4wnp1\n")
	if LoadFirstRunCreds(q) != nil {
		t.Error("a handoff with no password produced a screen with nothing on it")
	}
}

func TestShredRemovesAndOverwrites(t *testing.T) {
	dir := t.TempDir()
	secret := "SUPERSECRETVALUE-0123456789"
	a := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(a, []byte(secret), 0600); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, "never-existed")

	// A file that was already gone is not a failure: the panel must not
	// report "could not erase" for something nobody has to erase.
	if left := Shred([]string{a, missing}); len(left) != 0 {
		t.Fatalf("Shred reported failures: %v", left)
	}
	if _, err := os.Stat(a); !os.IsNotExist(err) {
		t.Errorf("the file is still there: %v", err)
	}
}

func TestShredNamesWhatItCouldNotRemove(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root; an unwritable directory would not stop it")
	}
	dir := t.TempDir()
	sub := filepath.Join(dir, "locked")
	if err := os.Mkdir(sub, 0755); err != nil {
		t.Fatal(err)
	}
	stuck := filepath.Join(sub, "x.txt")
	if err := os.WriteFile(stuck, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(sub, 0555); err != nil { // no unlink in this directory
		t.Fatal(err)
	}
	defer os.Chmod(sub, 0755)

	left := Shred([]string{stuck})
	if len(left) != 1 || left[0] != stuck {
		t.Fatalf("Shred claimed success on a file it could not remove: %v", left)
	}
}

// Nothing may be destroyed before the operator says so. A credential erased
// before it was read is the same bricked device as one never written.
func TestNothingIsErasedWithoutConfirmation(t *testing.T) {
	dir := t.TempDir()
	boot := filepath.Join(dir, "card.txt")
	if err := os.WriteFile(boot, []byte("password: hunter2"), 0644); err != nil {
		t.Fatal(err)
	}
	p := writeHandoff(t, dir, "web_user=admin\nweb_pass=WEBPASS\nssh_user=p4wnp1\nssh_pass=SSHPASS\nerase="+boot+"\n")
	c := LoadFirstRunCreds(p)

	app := NewApp(NewFakeClient(), NewRoot())
	app.Push(NewFirstRunView(c))

	// Walk every control except the one that erases, several times over,
	// including backing out of the confirm dialog with No.
	for i := 0; i < 3; i++ {
		for _, b := range []Button{BtnUp, BtnDown, BtnLeft, BtnRight, BtnConfirm, BtnRefresh, BtnHome} {
			app.Handle(b)
		}
	}
	app.Push(NewFirstRunView(c))
	app.Handle(BtnAction)  // opens the confirmation
	app.Handle(BtnConfirm) // "No" is selected by default
	for _, f := range []string{p, boot} {
		if _, err := os.Stat(f); err != nil {
			t.Fatalf("%s was erased without a confirmed yes: %v", filepath.Base(f), err)
		}
	}

	// And now actually confirm.
	app.Handle(BtnAction)
	app.Handle(BtnRight) // move to Yes
	app.Handle(BtnConfirm)
	for _, f := range []string{p, boot} {
		if _, err := os.Stat(f); !os.IsNotExist(err) {
			t.Errorf("%s survived a confirmed erase: %v", filepath.Base(f), err)
		}
	}
}

// The screen must actually show the secret, and show it unmistakably -- a
// password you cannot read off the panel is the whole problem, not the fix.
func TestFirstRunShowsEveryCredential(t *testing.T) {
	dir := t.TempDir()
	p := writeHandoff(t, dir, "web_user=admin\nweb_pass=WEBPASS12345678\nssh_user=p4wnp1\nssh_pass=SSHPASS87654321\n")
	c := LoadFirstRunCreds(p)

	app := NewApp(NewFakeClient(), NewRoot())
	app.Push(NewFirstRunView(c))

	seen := map[string]bool{}
	for i := 0; i < 6; i++ {
		// Flattened: the secret cards wrap their password beside the QR code.
		s := renderFlat(app)
		for _, want := range []string{"SSHPASS87654321", "WEBPASS12345678", "p4wnp1", "admin"} {
			if strings.Contains(s, want) {
				seen[want] = true
			}
		}
		app.Handle(BtnDown)
	}
	for _, want := range []string{"SSHPASS87654321", "WEBPASS12345678", "p4wnp1", "admin"} {
		if !seen[want] {
			t.Errorf("paging through the screens never showed %q", want)
		}
	}
}

// After erasing, the screen must not go on displaying what it just destroyed,
// and must say plainly what an SD-card erase does and does not achieve.
func TestFirstRunIsHonestAfterErasing(t *testing.T) {
	dir := t.TempDir()
	p := writeHandoff(t, dir, "web_user=admin\nweb_pass=WEBPASS12345678\n")
	c := LoadFirstRunCreds(p)

	app := NewApp(NewFakeClient(), NewRoot())
	v := NewFirstRunView(c)
	app.Push(v)
	app.Handle(BtnAction)
	app.Handle(BtnRight)
	app.Handle(BtnConfirm)

	s := renderText(app)
	// Flattened for the ABSENCE check specifically: a password still on
	// screen but wrapped across two lines would slip past a plain substring
	// search, and "we erased it but it is still displayed" is the one thing
	// this test exists to catch.
	if strings.Contains(renderFlat(app), "WEBPASS12345678") {
		t.Errorf("the password is still on screen after the erase:\n%s", s)
	}
	if !strings.Contains(s, "Erased") {
		t.Errorf("the screen does not say it erased anything:\n%s", s)
	}
	if !strings.Contains(s, "fragments") && !strings.Contains(s, "SD flash") {
		t.Errorf("the screen claims a clean erase on flash storage:\n%s", s)
	}
}

// The panel must show every credential the erase destroys.
//
// KEY1 shreds /root/INITIAL_CREDENTIALS.txt, and the per-device WiFi access
// point key lives in there. Before this, the handoff carried only the web
// and SSH passwords -- so an operator who followed the instruction on the
// screen destroyed a key they had never been shown, and could not get back.
// Shown-set must equal erased-set.
func TestEveryCredentialTheEraseDestroysIsShownFirst(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "INITIAL_CREDENTIALS.txt")
	if err := os.WriteFile(root, []byte("admin pw and the wifi psk live here"), 0600); err != nil {
		t.Fatal(err)
	}
	p := writeHandoff(t, dir, "web_user=admin\nweb_pass=WEBPASSWORD1234\n"+
		"wifi_psk=WIFIKEY123456789abc\nerase="+root+"\n")

	c := LoadFirstRunCreds(p)
	if c == nil {
		t.Fatal("handoff did not load")
	}
	if c.WiFiPSK != "WIFIKEY123456789abc" {
		t.Fatalf("the AP key did not load: %q", c.WiFiPSK)
	}

	app := NewApp(NewFakeClient(), NewRoot())
	app.Push(NewFirstRunView(c))
	seen := map[string]bool{}
	for i := 0; i < 6; i++ {
		// Flattened: beside the QR code the text column is eleven characters
		// wide, so these passwords wrap onto a second line. Still shown in
		// full, which is what "KEY1 erases it, so show it first" requires.
		s := renderFlat(app)
		for _, want := range []string{"WEBPASSWORD1234", "WIFIKEY123456789abc", "WiFi"} {
			if strings.Contains(s, want) {
				seen[want] = true
			}
		}
		app.Handle(BtnDown)
	}
	for _, want := range []string{"WEBPASSWORD1234", "WIFIKEY123456789abc", "WiFi"} {
		if !seen[want] {
			t.Errorf("paging through the cards never showed %q -- yet KEY1 erases it", want)
		}
	}
}

// A device reachable only by key, with no WiFi configured, still has a web
// password to hand over -- and one with ONLY a WiFi key must still show it.
func TestHandoffLoadsWithAnySingleCredential(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{"web only", "web_user=admin\nweb_pass=ONLYWEB123456\n", "ONLYWEB123456"},
		{"wifi only", "wifi_psk=ONLYWIFI1234567\n", "ONLYWIFI1234567"},
		{"ssh only", "ssh_user=p4wnp1\nssh_pass=ONLYSSH12345678\n", "ONLYSSH12345678"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := writeHandoff(t, t.TempDir(), tc.body)
			c := LoadFirstRunCreds(p)
			if c == nil {
				t.Fatalf("a handoff carrying %s did not load", tc.name)
			}
			app := NewApp(NewFakeClient(), NewRoot())
			app.Push(NewFirstRunView(c))
			found := false
			for i := 0; i < 5 && !found; i++ {
				// renderFlat, not renderText: beside the QR code the
				// column is eleven characters wide, so these passwords
				// wrap. Still shown in full, just not on one line.
				if strings.Contains(renderFlat(app), tc.want) {
					found = true
				}
				app.Handle(BtnDown)
			}
			if !found {
				t.Errorf("never showed %q", tc.want)
			}
		})
	}
}
