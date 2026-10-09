//go:build linux
// +build linux

package service

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/mame82/P4wnP1_aloa/service/datastore"

	pb "github.com/mame82/P4wnP1_aloa/proto"
)

// THE GAP THIS CLOSES
//
// oled/shipped_ssid_test.go checks that the two SHELL SCRIPTS agree on the
// access point's name and that it fits a scan-to-join QR. Its own header
// names three sources of truth and lists dist/db/init.db first -- and then
// does not read it, because the datastore is linux-only and that test is not.
//
// init.db is the one that matters. It holds ws_startup, the stored WiFi
// template the service actually deploys at boot; the scripts only describe
// the AP, this creates it. Checking the two descriptions and not the thing
// being described is how the original drift survived: the database said one
// name, servicestart.sh said another, and the radio followed whichever ran
// last.
//
// Reading it through the datastore rather than grepping the blob is
// deliberate. A badger backup keeps superseded versions, so the old 32-byte
// SSID is still present as bytes in a file whose live value is correct --
// a grep would report drift that does not exist, or miss drift that does.
const wsStartupKey = "ws_startup"

func shippedTemplateSSID(t *testing.T) string {
	t.Helper()
	src := filepath.Join("..", "dist", "db", "init.db")
	if _, err := os.Stat(src); err != nil {
		t.Fatalf("the shipped template database is missing: %v", err)
	}
	// datastore.Open restores the backup ONLY when the working directory
	// does not already exist, and MkdirTemp creates what it returns -- so
	// hand it a path INSIDE the temp dir. Getting this wrong yields an empty
	// store and a test that passes by finding nothing.
	parent := t.TempDir()
	store, err := datastore.Open(filepath.Join(parent, "db"), src)
	if err != nil {
		t.Fatalf("opening %s: %v", src, err)
	}
	defer store.Close()

	ws := &pb.WiFiSettings{}
	if err := store.Get(wsStartupKey, ws); err != nil {
		t.Fatalf("reading %s from the shipped database: %v", wsStartupKey, err)
	}
	if ws.Ap_BSS == nil {
		t.Fatalf("%s has no AP BSS", wsStartupKey)
	}
	if ws.Ap_BSS.SSID == "" {
		t.Fatalf("%s has an empty SSID; this check would pass anything", wsStartupKey)
	}
	return ws.Ap_BSS.SSID
}

// The budget. A join URI is the WIFI: structure plus the 20-character
// generated key plus the WHOLE SSID, and a panel-sized symbol holds 53 bytes,
// which leaves 15 for the name. Over that and the OLED card silently drops
// the join code and prints the key to be typed with a thumb.
func TestShippedDatabaseSSIDFitsTheJoinQRBudget(t *testing.T) {
	ssid := shippedTemplateSSID(t)
	const budget = 15
	if n := len(ssid); n > budget {
		t.Errorf("dist/db/init.db ships SSID %q at %d bytes; the budget is %d.\n"+
			"Emoji cost 3-4 bytes EACH. This is the name the RADIO uses, so no\n"+
			"join code can be built for this device however short the scripts say\n"+
			"it is. Regenerate with tools/initdb.", ssid, n, budget)
	}
}

// And it must agree with what the scripts tell the operator, or the card
// names one network while the radio offers another.
func TestShippedDatabaseSSIDMatchesTheScripts(t *testing.T) {
	ssid := shippedTemplateSSID(t)
	for _, c := range []struct {
		path string
		re   *regexp.Regexp
	}{
		{filepath.Join("..", "dist", "scripts", "firstboot-secure-defaults.sh"),
			regexp.MustCompile(`(?m)^P4WNP1_INITIAL_SSID=(.+)$`)},
		{filepath.Join("..", "dist", "scripts", "servicestart.sh"),
			regexp.MustCompile(`wifi set ap .*-s "([^"]+)"`)},
	} {
		b, err := os.ReadFile(c.path)
		if err != nil {
			t.Fatalf("reading %s: %v", c.path, err)
		}
		m := c.re.FindSubmatch(b)
		if m == nil {
			t.Fatalf("no SSID found in %s; the pattern has stopped matching, so "+
				"this check cannot fail", c.path)
		}
		if got := string(m[1]); got != ssid {
			t.Errorf("%s says %q but the shipped database broadcasts %q", c.path, got, ssid)
		}
	}
}
