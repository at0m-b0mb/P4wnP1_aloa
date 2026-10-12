package oled

import (
	"testing"
	"time"
)

// resolveAP's call is SYNCHRONOUS AND ON THE BUTTON LOOP.
//
// It runs from Render, Render runs on the same select loop in
// cmd/p4wnp1-oled that reads input events, and every LiveAP call is a real
// HTTP round trip that makes the service exec `iw dev wlan0 info`. So each
// attempt blocks KEY1 -- the erase key, the only control this screen offers
// -- for as long as the service takes to answer, up to the client's 6-second
// timeout.
//
// The retry that replaced the single latched attempt was rate limited to one
// call per second, which is the right rate for a background poller and the
// wrong one for a call on the draw path: on the device the window exists for
// -- one whose access point never comes up -- it is ~120 blocking calls over
// two minutes, and the panel stalls in bursts for the whole of it.
//
// TestResolveAPGivesUpEventually already pins the window, but its ceiling is
// 200 calls, which a flat one-second interval passes comfortably. This pins
// the rate.
func TestResolveAPBacksOffRatherThanBlockingOncePerSecond(t *testing.T) {
	clock := time.Unix(1700000000, 0)
	old := nowFunc
	nowFunc = func() time.Time { return clock }
	t.Cleanup(func() { nowFunc = old })

	v := NewFirstRunView(&FirstRunCreds{WebPass: "web"})
	client := NewFakeClient() // the access point never comes up
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

	// Three minutes of the real paint ticker: 150ms a frame, which is how
	// often Render -- and so resolveAP -- actually runs.
	start := clock
	var at []time.Duration
	seen := 0
	for i := 0; i < 1200; i++ {
		v.resolveAP(app)
		if n := calls(); n > seen {
			at = append(at, clock.Sub(start))
			seen = n
		}
		clock = clock.Add(150 * time.Millisecond)
	}

	if len(at) == 0 {
		t.Fatal("it never asked at all")
	}
	// 1s, 2s, 4s, 8s, 16s then 30s apart is 8 attempts inside the window.
	// A flat one-second interval is ~120, which is the state this test
	// exists to keep the panel out of.
	if len(at) > 10 {
		t.Errorf("%d blocking calls on the button loop over the retry window, want "+
			"a handful: the interval is not backing off, so KEY1 is unresponsive "+
			"in bursts for two minutes on a device with no access point\n  at: %v",
			len(at), at)
	}

	// AND THE EARLY ATTEMPTS MUST STAY PROMPT. Backing off is only
	// acceptable because the answer normally arrives within seconds, when
	// the startup master template deploys. A fix that simply slowed the
	// first retry down would lose the WiFi card on a device that was merely
	// a little slow to bring the radio up -- the bug the retry exists for.
	if at[0] != 0 {
		t.Errorf("the first attempt was at %v, not on the first frame", at[0])
	}
	if len(at) < 2 {
		t.Fatal("it asked exactly once; one early empty answer loses the WiFi card for good")
	}
	if at[1] > 2*time.Second {
		t.Errorf("the second attempt was %v in, too late to catch the startup "+
			"template deploy", at[1])
	}

	// The window still closes.
	if last := at[len(at)-1]; last > apRetryWindow {
		t.Errorf("an attempt at %v is past the %v window", last, apRetryWindow)
	}
}

// The ceiling has to bite, or "doubling" runs away: 1s doubled eleven times
// is half an hour, and an access point that came up at t=90s would never be
// noticed inside the window.
func TestResolveAPBackoffIsCapped(t *testing.T) {
	if apRetryMax <= apRetryInterval {
		t.Fatalf("apRetryMax %v is not above apRetryInterval %v", apRetryMax, apRetryInterval)
	}
	if apRetryMax > apRetryWindow/4 {
		t.Errorf("apRetryMax %v is more than a quarter of the %v window, so a late "+
			"access point would not be seen before the window closes",
			apRetryMax, apRetryWindow)
	}
}
