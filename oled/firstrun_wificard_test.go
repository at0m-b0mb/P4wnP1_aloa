package oled

import (
	"strings"
	"testing"
	"time"
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

// resolveAP must RETRY, and must stop. The first version latched a
// `ssidDone` flag before the call and never cleared it, so exactly one
// attempt was ever made -- and the service makes that one attempt likely to
// come back empty. Service.Start() opens the RPC listener before it deploys
// the startup master template, and until that deploy runs GetWiFiState
// answers from its initial state, whose AP BSS is an empty struct. A panel
// that asks in that window cached "no key" for the life of the process and
// showed three cards, which is the exact bug reading the key live was meant
// to end.
func TestResolveAPRetriesUntilTheServiceHasAnAnswer(t *testing.T) {
	clock := time.Unix(1700000000, 0)
	old := nowFunc
	nowFunc = func() time.Time { return clock }
	t.Cleanup(func() { nowFunc = old })

	v := NewFirstRunView(&FirstRunCreds{WebPass: "web"})
	client := NewFakeClient()
	client.SSID, client.PSK = "", "" // the service is up but the AP is not yet
	app := NewApp(client, v)

	calls := func() int {
		n := 0
		for _, c := range client.Calls {
			if c == "LiveAP" {
				n++
			}
		}
		return n
	}

	// Many frames inside one second must produce ONE call: Render runs every
	// frame, and an unthrottled retry would be an RPC per frame.
	for i := 0; i < 50; i++ {
		v.resolveAP(app)
	}
	if n := calls(); n != 1 {
		t.Fatalf("%d calls within one second; the retry is not rate limited", n)
	}
	if len(v.cards()) != 2 {
		t.Fatalf("a WiFi card was built with no key")
	}

	// A second later it tries again -- this is the whole point.
	clock = clock.Add(2 * time.Second)
	v.resolveAP(app)
	if n := calls(); n != 2 {
		t.Fatalf("after the retry interval it made %d calls, want 2: it never retries, "+
			"so one early empty answer loses the WiFi card for good", n)
	}

	// Now the AP comes up, as it does once the startup template deploys.
	client.SSID, client.PSK = "\U0001F4A5P4wnP1", "cGNGK5JiD86SI0QHF8Yh"
	clock = clock.Add(2 * time.Second)
	v.resolveAP(app)

	if v.psk() != client.PSK {
		t.Fatalf("psk() = %q after the AP came up, want the live key", v.psk())
	}
	cards := v.cards()
	if len(cards) != 3 {
		t.Fatalf("%d cards, want 3 (intro, Web, WiFi) once the key arrived", len(cards))
	}

	// And it stops asking, because it has what it came for.
	before := calls()
	clock = clock.Add(time.Hour)
	for i := 0; i < 20; i++ {
		v.resolveAP(app)
	}
	if n := calls(); n != before {
		t.Errorf("made %d more calls after it already had the key", n-before)
	}
}

// It must also give up. A device with no access point at all would otherwise
// issue one RPC a second for as long as the operator stood in front of it.
func TestResolveAPGivesUpEventually(t *testing.T) {
	clock := time.Unix(1700000000, 0)
	old := nowFunc
	nowFunc = func() time.Time { return clock }
	t.Cleanup(func() { nowFunc = old })

	v := NewFirstRunView(&FirstRunCreds{WebPass: "web"})
	client := NewFakeClient() // no PSK, ever
	app := NewApp(client, v)

	for i := 0; i < 600; i++ {
		v.resolveAP(app)
		clock = clock.Add(time.Second)
	}
	n := 0
	for _, c := range client.Calls {
		if c == "LiveAP" {
			n++
		}
	}
	if n == 0 {
		t.Fatal("it never asked at all")
	}
	if n > 200 {
		t.Errorf("%d calls over 600 seconds: the retry window does not close", n)
	}
	// And still no empty WiFi card.
	for _, c := range v.cards() {
		if c.head == "WiFi join" {
			t.Error("built a WiFi card with no key")
		}
	}
}
