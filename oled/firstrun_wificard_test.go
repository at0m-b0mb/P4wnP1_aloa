package oled

import (
	"strings"
	"testing"
)

// THE BUG THIS FILE EXISTS FOR
//
// Reported from real hardware as "I don't see the WiFi creds": the first-run
// screen showed three cards -- intro, SSH, Web -- where it should have shown
// four. The WiFi card was absent because the handoff carried no wifi_psk line.
//
// First boot writes that line only when /etc/p4wnp1/generated-ap.psk already
// exists. The SERVICE writes that file, the first time it refuses to broadcast
// a published PSK, and nothing orders the two events. Lose the race and the
// key never reaches the panel -- and KEY1 then shreds
// /root/INITIAL_CREDENTIALS.txt, which holds the only other copy. The operator
// destroys a credential they were never shown.
//
// So the card must not depend on the handoff having won a race.
func TestWiFiCardAppearsWhenOnlyTheServiceKnowsTheKey(t *testing.T) {
	// A handoff exactly as first boot writes it when it loses the race: web
	// and SSH, no wifi_psk, no wifi_ssid.
	creds := &FirstRunCreds{
		WebUser: "admin", WebPass: "YXpXK1UdUyM5069A3XAM",
		SSHUser: "operator", SSHPass: "eSjpYjuIKylArsuPNrI6",
	}
	v := NewFirstRunView(creds)

	// Before asking the service there is no WiFi card. This is the state the
	// device shipped in, and asserting it keeps the test honest: without it a
	// change that always built a WiFi card would pass below for the wrong
	// reason.
	if n := len(v.cards()); n != 3 {
		t.Fatalf("with no key from either source the panel should show 3 cards, got %d", n)
	}

	client := NewFakeClient()
	client.SSID = "\U0001F4A5P4wnP1"
	client.PSK = "cGNGK5JiD86SI0QHF8Yh"
	app := NewApp(client, v)
	v.resolveAP(app)

	cards := v.cards()
	if len(cards) != 4 {
		t.Fatalf("the WiFi card is still missing: %d cards, want 4", len(cards))
	}
	wifi := cards[3]
	if wifi.pass != client.PSK {
		t.Errorf("WiFi card key = %q, want the live key %q", wifi.pass, client.PSK)
	}
	// And because the live SSID is short enough, it must be a real join code
	// rather than the key alone -- that was the other half of the report.
	if !strings.HasPrefix(wifi.qr, "WIFI:") {
		t.Errorf("the card did not offer a join code: qr = %q", wifi.qr)
	}
	if !strings.Contains(wifi.qr, client.PSK) {
		t.Errorf("the join code does not carry the live key: %q", wifi.qr)
	}
	if !strings.Contains(wifi.note, "join") {
		t.Errorf("note %q does not tell the operator they can scan to join", wifi.note)
	}
}

// The live value wins over a stale handoff. install.sh's copy can name a key
// the radio is not using -- the guard replaces a published PSK at deploy time,
// after first boot has already written its file -- and showing the wrong key
// is worse than showing none, because the operator has no way to tell.
func TestLiveKeyBeatsTheHandoffCopy(t *testing.T) {
	v := NewFirstRunView(&FirstRunCreds{
		WebPass: "web", WiFiPSK: "stale-handoff-key", WiFiSSID: "stale",
	})
	client := NewFakeClient()
	client.SSID = "live-ssid"
	client.PSK = "live-key-from-service"
	v.resolveAP(NewApp(client, v))

	if got := v.psk(); got != "live-key-from-service" {
		t.Errorf("psk() = %q, want the live key", got)
	}
	if got := v.ssid(); got != "live-ssid" {
		t.Errorf("ssid() = %q, want the live name", got)
	}
}

// ...but a service that cannot be reached must not erase a key first boot DID
// manage to write down. Degrading to no card at all would reintroduce the bug
// by another route.
func TestHandoffKeySurvivesAnUnreachableService(t *testing.T) {
	v := NewFirstRunView(&FirstRunCreds{
		WebPass: "web", WiFiPSK: "handoff-key", WiFiSSID: "handoff-ssid",
	})
	client := NewFakeClient()
	client.Fail = map[string]string{"LiveAP": "no service"}
	v.resolveAP(NewApp(client, v))

	if got := v.psk(); got != "handoff-key" {
		t.Errorf("psk() = %q, want the handoff copy when the service is down", got)
	}
	if got := v.ssid(); got != "handoff-ssid" {
		t.Errorf("ssid() = %q, want the handoff copy when the service is down", got)
	}
	found := false
	for _, c := range v.cards() {
		if c.pass == "handoff-key" {
			found = true
		}
	}
	if !found {
		t.Error("the WiFi card vanished when the service was unreachable")
	}
}

// A device with no access point at all must not grow an empty WiFi card
// showing a blank key.
func TestNoWiFiCardWhenThereIsNoAccessPoint(t *testing.T) {
	v := NewFirstRunView(&FirstRunCreds{WebUser: "admin", WebPass: "web"})
	client := NewFakeClient()
	client.SSID = "some-ap"
	client.PSK = "" // an open AP, or station mode, or no radio
	v.resolveAP(NewApp(client, v))

	for _, c := range v.cards() {
		if c.head == "WiFi join" {
			t.Fatalf("built a WiFi card with no key: %+v", c)
		}
	}
}

// resolveAP asks ONCE. It runs from Render, which runs on every frame, and the
// panel redraws continuously -- an unguarded call would hammer the service
// with one RPC per frame for as long as the card is on screen.
func TestResolveAPAsksTheServiceOnlyOnce(t *testing.T) {
	v := NewFirstRunView(&FirstRunCreds{WebPass: "web"})
	client := NewFakeClient()
	client.SSID, client.PSK = "ap", "key"
	app := NewApp(client, v)

	for i := 0; i < 25; i++ {
		v.resolveAP(app)
	}
	n := 0
	for _, c := range client.Calls {
		if c == "LiveAP" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("LiveAP called %d times, want 1 -- Render calls this every frame", n)
	}
}
