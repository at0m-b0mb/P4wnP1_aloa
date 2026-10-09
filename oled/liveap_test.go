package oled

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// serveFixture stands up a server that answers every RPC with the given body.
func serveFixture(t *testing.T, body []byte) *APIClient {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	tok := filepath.Join(t.TempDir(), "local.token")
	if err := os.WriteFile(tok, []byte("t\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return NewAPIClient(srv.URL, tok)
}

// LiveAP decodes a response CAPTURED FROM A REAL DEVICE, not one written from
// the proto definition.
//
// The field names protojson emits are not the Go field names and are not
// uniformly snake_case: "ssid" at the top level but "currentSettings", then
// "ap_BSS" holding "SSID" and "PSK" in capitals. Guessing wrong is silent --
// encoding/json leaves the field at its zero value, LiveAP returns an empty
// key, and the WiFi card disappears exactly as it did before this was fixed.
// A hand-written fixture would encode the same guess twice and prove nothing,
// so oled/testdata/getwifistate_ap.json is a real reply with the key replaced.
func TestLiveAPDecodesARealDeviceReply(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "getwifistate_ap.json"))
	if err != nil {
		t.Fatal(err)
	}
	c := serveFixture(t, body)

	ssid, psk, err := c.LiveAP()
	if err != nil {
		t.Fatalf("LiveAP: %v", err)
	}
	if ssid == "" {
		t.Error("LiveAP returned no SSID from a reply that contains one; the tag is wrong")
	}
	if psk == "" {
		t.Fatal("LiveAP returned NO KEY from a reply that contains one.\n" +
			"That is the silent failure this test exists for: a wrong json tag " +
			"yields an empty string, and the first-run WiFi card then vanishes.")
	}
	if want := "EXAMPLEKEY1234567890"; psk != want {
		t.Errorf("psk = %q, want %q", psk, want)
	}
	if want := "\U0001F4A5P4wnP1"; ssid != want {
		t.Errorf("ssid = %q, want %q", ssid, want)
	}
}

// The fixture must keep carrying a key, or the test above passes vacuously the
// day someone redacts it to "".
func TestLiveAPFixtureStillHasAKeyToFind(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "getwifistate_ap.json"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(body)
	for _, needle := range []string{`"ap_BSS"`, `"PSK"`, `"ssid"`, `"currentSettings"`} {
		if !strings.Contains(s, needle) {
			t.Errorf("the captured reply no longer contains %s; it is no longer a "+
				"sample of what the device sends and cannot gate the tags", needle)
		}
	}
}

// The top-level "ssid" is what hostapd reports and is empty before the radio
// answers. The deployed settings still name the network, and a join code needs
// it, so that is the fallback.
func TestLiveAPFallsBackToTheDeployedSSID(t *testing.T) {
	c := serveFixture(t, []byte(`{"mode":"AP_UP","ssid":"",
	  "currentSettings":{"ap_BSS":{"SSID":"configured-ap","PSK":"keykeykey"}}}`))
	ssid, psk, err := c.LiveAP()
	if err != nil {
		t.Fatal(err)
	}
	if ssid != "configured-ap" {
		t.Errorf("ssid = %q, want the deployed name when the radio reports none", ssid)
	}
	if psk != "keykeykey" {
		t.Errorf("psk = %q", psk)
	}
}

// A station-mode or disabled device has no AP key. That must come back empty
// rather than as some other BSS's key -- client_BSS_list also has SSID/PSK
// fields, and a decoder that reached into the wrong one would hand the panel a
// key for a network the device is joining, not offering.
func TestLiveAPIgnoresClientBSSKeys(t *testing.T) {
	c := serveFixture(t, []byte(`{"mode":"STA_CONNECTED","ssid":"someones-wifi",
	  "currentSettings":{"working_mode":"STA","ap_BSS":{"SSID":"","PSK":""},
	  "client_BSS_list":[{"SSID":"someones-wifi","PSK":"NOT-THE-AP-KEY"}]}}`))
	_, psk, err := c.LiveAP()
	if err != nil {
		t.Fatal(err)
	}
	if psk != "" {
		t.Errorf("psk = %q -- that is a client network's key, not this AP's", psk)
	}
}
