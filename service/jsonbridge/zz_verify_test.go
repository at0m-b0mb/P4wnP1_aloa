package jsonbridge

import (
	"encoding/json"
	"testing"

	pb "github.com/mame82/P4wnP1_aloa/proto"
	"github.com/golang/protobuf/proto"
)

func TestZZMarshalShape(t *testing.T) {
	gs := &pb.GadgetSettings{
		Enabled:          true,
		Vid:              "0x1d6b",
		Use_HID_KEYBOARD: true,
	}
	out, err := marshalOpts.Marshal(proto.MessageV2(gs))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	t.Logf("MARSHALLED: %s", out)

	// What the console does: deep copy, then write a snake_case key.
	var m map[string]interface{}
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatal(err)
	}
	keys := []string{}
	for k := range m {
		keys = append(keys, k)
	}
	t.Logf("KEYS: %v", keys)
	if _, ok := m["use_HID_KEYBOARD"]; ok {
		t.Logf("server emitted snake_case use_HID_KEYBOARD -- no mismatch")
	} else {
		t.Logf("server did NOT emit use_HID_KEYBOARD (console reads undefined)")
	}

	// Simulate checkbox onchange: draft['use_HID_KEYBOARD'] = false
	m["use_HID_KEYBOARD"] = false
	body, _ := json.Marshal(m)
	t.Logf("CONSOLE BODY: %s", body)

	var back pb.GadgetSettings
	err = unmarshalOpts.Unmarshal(body, proto.MessageV2(&back))
	t.Logf("UNMARSHAL ERR: %v", err)
}
