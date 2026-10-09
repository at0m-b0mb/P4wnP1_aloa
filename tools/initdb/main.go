//go:build linux
// +build linux

// initdb rewrites a field in the shipped template database, dist/db/init.db.
//
// # WHY THIS EXISTS
//
// init.db is a serialised datastore backup -- a binary blob -- and it is the
// real source of truth for what a freshly flashed device does. The AP's SSID
// lives there, NOT in install.sh. That caught me out: changing DEFAULT_SSID
// in install.sh altered what first boot PRINTS and what the panel hands over,
// while the radio went on broadcasting whatever the blob said. The card named
// one network and the device offered another.
//
// It matters now because the SSID has a size budget. A scan-to-join QR has to
// fit a 62-pixel symbol, which holds 53 bytes; the WIFI: structure and the
// generated key take 38 of them, leaving 15 BYTES for the SSID. The shipped
// default was 32 bytes of emoji, so no join code could ever be built and the
// WiFi card silently fell back to showing the key alone.
//
// Editing a binary blob by hand is how you get a device that will not boot, so
// this does it through the same datastore code the service uses.
//
// linux-only because service/datastore is (badger + the service's own build
// tags), so run it in a container:
//
//	docker run --rm --platform linux/arm64 -v "$PWD:/src" -w /src \
//	  golang:1.26-bookworm go run ./tools/initdb \
//	  -in dist/db/init.db -out dist/db/init.db -ssid 'NAME'
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/mame82/P4wnP1_aloa/service/datastore"

	pb "github.com/mame82/P4wnP1_aloa/proto"
)

// wsStartup is the key holding the WiFi settings deployed at boot. It matches
// cSTORE_PREFIX_WIFI_SETTINGS + "startup" in service/rpc_server.go.
const wsStartup = "ws_startup"

func main() {
	in := flag.String("in", "dist/db/init.db", "template database to read")
	out := flag.String("out", "", "where to write the result (default: same as -in)")
	ssid := flag.String("ssid", "", "new access point SSID (required)")
	list := flag.Bool("list", false, "print every key in the database and exit")
	maxBytes := flag.Int("max-ssid-bytes", 15, "refuse an SSID longer than this; 0 disables the check")
	flag.Parse()

	if *list {
		parent, err := os.MkdirTemp("", "initdb")
		if err != nil {
			log.Fatal(err)
		}
		defer os.RemoveAll(parent)
		// datastore.Open restores the backup ONLY if the directory does not
		// already exist, so hand it a path inside the temp dir rather than
		// the temp dir itself -- MkdirTemp creates what it returns, and an
		// existing directory silently yields an empty store.
		work := filepath.Join(parent, "db")
		store, err := datastore.Open(work, *in)
		if err != nil {
			log.Fatalf("opening %s: %v", *in, err)
		}
		defer store.Close()
		keys, err := store.Keys()
		if err != nil {
			log.Fatal(err)
		}
		for _, k := range keys {
			fmt.Println(k)
		}
		return
	}
	if *ssid == "" {
		log.Fatal("-ssid is required")
	}
	if *out == "" {
		*out = *in
	}

	// The budget is the whole reason this tool exists, so it is enforced
	// here rather than left for someone to rediscover on a panel.
	if *maxBytes > 0 && len(*ssid) > *maxBytes {
		log.Fatalf("the SSID is %d bytes and the limit is %d.\n"+
			"A scan-to-join QR must fit a 62-pixel symbol (53 bytes); the WIFI:\n"+
			"structure and the 20-character generated key take 38, leaving %d for\n"+
			"the SSID. Emoji cost 3-4 bytes EACH. Pass -max-ssid-bytes 0 to\n"+
			"override, and accept that the WiFi card will show the key only.",
			len(*ssid), *maxBytes, *maxBytes)
	}

	parent, err := os.MkdirTemp("", "initdb")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(parent)
	// See the note in the -list branch: the path must NOT exist yet, or the
	// backup is never restored and every key reads as missing.
	work := filepath.Join(parent, "db")

	store, err := datastore.Open(work, *in)
	if err != nil {
		log.Fatalf("opening %s: %v", *in, err)
	}
	defer store.Close()

	ws := &pb.WiFiSettings{}
	if err := store.Get(wsStartup, ws); err != nil {
		log.Fatalf("reading %s: %v", wsStartup, err)
	}
	if ws.Ap_BSS == nil {
		log.Fatalf("%s has no AP BSS to rename", wsStartup)
	}

	old := ws.Ap_BSS.SSID
	ws.Ap_BSS.SSID = *ssid
	if err := store.Put(wsStartup, ws, true); err != nil {
		log.Fatalf("writing %s: %v", wsStartup, err)
	}

	tmp := filepath.Join(parent, "out.db")
	if err := store.Backup(tmp); err != nil {
		log.Fatalf("backing up: %v", err)
	}
	b, err := os.ReadFile(tmp)
	if err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(*out, b, 0o644); err != nil {
		log.Fatal(err)
	}

	fmt.Printf("ssid %q (%d bytes) -> %q (%d bytes)\n", old, len(old), *ssid, len(*ssid))
	fmt.Printf("wrote %s (%d bytes)\n", *out, len(b))
}
