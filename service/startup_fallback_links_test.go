//go:build linux
// +build linux

package service

import (
	"os"
	"regexp"
	"strings"
	"testing"

	pb "github.com/mame82/P4wnP1_aloa/proto"
)

// WHY THIS FILE IS SEPARATE FROM startup_fallback_test.go
//
// That file pins the SUCCESS COUNT rule: fall back when Succeeded is zero.
// This one pins the rule that replaced it -- fall back when the device has no
// way in -- because a count of successes is not reachability. Three versions
// of startupNeedsFallback have now been wrong in a way the suite of the day
// could not see, so each criterion keeps its own cases rather than being
// edited into the old ones.
//
// The two rules disagree on exactly one state, and it is a state a shipped
// device reaches: the startup USB template has use_UMS with a backing image
// that is no longer on disk (a hand-installed device, a restored backup or an
// operator who tidied up all leave the ums folder absent), the WiFi template
// cannot find wlan0, and the network and reflex templates deploy fine.
// Succeeded is then 3 and the old rule vetoed the fallback -- on a device
// presenting nothing to the cable and broadcasting nothing.

func TestNoLinkUpRunsTheFallbackEvenWithSuccesses(t *testing.T) {
	e := &PartialDeployError{
		Failures: []string{
			"USB usbs_startup: USB Mass Storage image \"/usr/local/P4wnP1/ums/image.bin\" cannot be used",
			"wifi ws_startup: no wlan0",
		},
		Succeeded: 3, Attempted: 5,
		LinksAttempted: 2, LinksUp: 0,
	}
	if !startupNeedsFallback(e) {
		t.Fatal("no USB gadget and no access point, and the fallback is suppressed")
	}
	if e.NothingDeployed() {
		t.Error("NothingDeployed() is true with Succeeded == 3")
	}
	if !strings.Contains(e.Error(), "NO LINK") {
		t.Errorf("the journal line does not say no link came up: %q", e.Error())
	}
}

func TestAFailedUsbWithTheApUpKeepsTheFallbackOff(t *testing.T) {
	e := &PartialDeployError{
		Failures:  []string{"USB usbs_startup: image cannot be used"},
		Succeeded: 4, Attempted: 5,
		LinksAttempted: 2, LinksUp: 1,
	}
	if startupNeedsFallback(e) {
		t.Fatal("the script would rewrite the access point's SSID and PSK, dropping the one link that works")
	}
}

func TestATemplateThatNamesNothingRunsTheFallback(t *testing.T) {
	e := &PartialDeployError{Succeeded: 0, Attempted: 0}
	if !startupNeedsFallback(e) {
		t.Fatal("a template naming nothing deploys nothing and must take the fallback")
	}
	if !strings.Contains(e.Error(), "named no subsystems") {
		t.Errorf("message does not say the template named nothing: %q", e.Error())
	}
}

func TestNoLinkInfoFallsBackToTheCount(t *testing.T) {
	e := &PartialDeployError{
		Failures:  []string{"reflexes tas_startup: ErrGet"},
		Succeeded: 2, Attempted: 3,
	}
	if startupNeedsFallback(e) {
		t.Fatal("an error carrying no link counts must not invent an answer")
	}
	if e.NoLinkDeployed() {
		t.Error("NoLinkDeployed() is true with no links attempted")
	}
}

func TestMasterTemplateNamesNothing(t *testing.T) {
	if !masterTemplateNamesNothing(nil) {
		t.Error("nil template")
	}
	if !masterTemplateNamesNothing(&pb.MasterTemplate{}) {
		t.Error("all names empty")
	}
	if !masterTemplateNamesNothing(&pb.MasterTemplate{TemplateNamesNetwork: []string{"", ""}}) {
		t.Error("a list of empty network names is still nothing")
	}
	if masterTemplateNamesNothing(&pb.MasterTemplate{TemplateNameUsb: "usbs_startup"}) {
		t.Error("a USB name is something")
	}
	if masterTemplateNamesNothing(&pb.MasterTemplate{TemplateNamesNetwork: []string{"", "wlan0_dhcp"}}) {
		t.Error("a non-empty network name is something")
	}
}

// AND THE WIRING, WHICH IS WHERE THIS CLASS OF BUG ACTUALLY LIVES.
//
// Everything above tests PartialDeployError's arithmetic. None of it can see
// whether DeployMasterTemplate still MARKS the USB gadget and the WiFi radio
// as the link-bearing steps -- and that is the mapping that decides whether
// the criterion means anything, exactly as the Bluetooth skip/success mapping
// was for the rule before it. Flip the flag on the USB step to false and
// every test above stays green while a device with no gadget and no access
// point boots with the fallback suppressed.
//
// DeployMasterTemplate cannot be called without a device -- each step reaches
// for configfs, netlink and hostapd -- so the flags are read from the source,
// which is the same thing tools/check-rpc-shapes.py does for payload names.
func TestOnlyUsbAndWifiAreMarkedAsLinks(t *testing.T) {
	src, err := os.ReadFile("rpc_server.go")
	if err != nil {
		t.Fatalf("cannot read rpc_server.go: %v", err)
	}
	want := map[string]string{
		"USB":       "true",
		"wifi":      "true",
		"reflexes":  "false",
		"network":   "false",
		"bluetooth": "false",
	}
	found := map[string]string{}
	re := regexp.MustCompile(`try\("(USB|wifi|reflexes|network|bluetooth) ?"?\+?[^,]*, (true|false),`)
	for _, m := range re.FindAllStringSubmatch(string(src), -1) {
		found[m[1]] = m[2]
	}
	for what, flag := range want {
		got, ok := found[what]
		if !ok {
			t.Errorf("no try() call found for %q -- if the step was renamed, this "+
				"gate has to be renamed with it, not left passing on nothing", what)
			continue
		}
		if got != flag {
			t.Errorf("the %s step is marked link=%s, want %s: the fallback criterion "+
				"asks whether a LINK came up, and this is where it learns which "+
				"steps are links", what, got, flag)
		}
	}
}
