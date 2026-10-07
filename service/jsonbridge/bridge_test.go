package jsonbridge

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	pb "github.com/mame82/P4wnP1_aloa/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// fakeSvc mimics the shape of generated gRPC server code, using the real
// protobuf message types from the P4wnP1 service definition.
type fakeSvc struct {
	lastTA *pb.TriggerAction
}

func (f *fakeSvc) EchoTriggerAction(_ context.Context, in *pb.TriggerAction) (*pb.TriggerAction, error) {
	f.lastTA = in
	return in, nil
}

// GetFlag returns an EventValue, whose single field is the third oneof in the
// service definition -- so this also covers the scalar-in-oneof case.
func (f *fakeSvc) GetFlag(_ context.Context, _ *pb.Empty) (*pb.EventValue, error) {
	return &pb.EventValue{Val: &pb.EventValue_Tbool{Tbool: true}}, nil
}

func (f *fakeSvc) BoomStatus(_ context.Context, _ *pb.Empty) (*pb.Empty, error) {
	return nil, status.Error(codes.PermissionDenied, "nope")
}

func (f *fakeSvc) BoomPlain(_ context.Context, _ *pb.Empty) (*pb.Empty, error) {
	return nil, errors.New("plain failure")
}

// --- methods that must NOT be discovered as RPCs ---

func (f *fakeSvc) NotAnRPC() string                         { return "x" }
func (f *fakeSvc) WrongArity(_ context.Context) error       { return nil }
func (f *fakeSvc) NotProto(_ context.Context, _ *int) error { return nil }
func (f *fakeSvc) Streamish(_ *pb.EventRequest, _ pb.P4WNP1_EventListenServer) error {
	return nil
}

func newTestBridge(t *testing.T) (*Bridge, *fakeSvc) {
	t.Helper()
	f := &fakeSvc{}
	b, err := New(f)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return b, f
}

func TestDiscoversOnlyUnaryProtoRPCs(t *testing.T) {
	b, _ := newTestBridge(t)
	got := b.Methods()

	want := []string{"BoomPlain", "BoomStatus", "EchoTriggerAction", "GetFlag"}
	if len(got) != len(want) {
		t.Fatalf("discovered %v, want exactly %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Methods()[%d] = %q, want %q (should be sorted)", i, got[i], want[i])
		}
	}
	// The negative cases are the point of this test: a streaming RPC or a
	// helper method must not become an HTTP endpoint.
	for _, bad := range []string{"NotAnRPC", "WrongArity", "NotProto", "Streamish"} {
		if b.Has(bad) {
			t.Errorf("%s was discovered as an RPC but must not be", bad)
		}
	}
}

func TestHasIsCaseInsensitive(t *testing.T) {
	b, _ := newTestBridge(t)
	for _, n := range []string{"GetFlag", "getflag", "GETFLAG", "gEtFlAg"} {
		if !b.Has(n) {
			t.Errorf("Has(%q) = false, want true", n)
		}
	}
	if b.Has("NoSuchMethod") {
		t.Error(`Has("NoSuchMethod") = true, want false`)
	}
}

// TestOneofRoundTrip is the reason this package uses protojson instead of
// encoding/json. TriggerAction has two oneof fields, which compile to Go
// interface fields; encoding/json cannot unmarshal into those and would drop
// both the trigger and the action, silently turning every automation rule into
// an empty one.
func TestOneofRoundTrip(t *testing.T) {
	b, f := newTestBridge(t)

	in := `{
		"id": 11,
		"isActive": true,
		"groupReceive": {"groupName": "svc-up", "value": 7},
		"bashScript": {"scriptName": "startup.sh"}
	}`
	out, err := b.Call(context.Background(), "EchoTriggerAction", []byte(in))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}

	// Server side: the oneofs must have materialised as the right concrete types.
	if f.lastTA == nil {
		t.Fatal("handler never received a request")
	}
	gr, ok := f.lastTA.Trigger.(*pb.TriggerAction_GroupReceive)
	if !ok {
		t.Fatalf("Trigger = %T, want *pb.TriggerAction_GroupReceive", f.lastTA.Trigger)
	}
	if gr.GroupReceive.GroupName != "svc-up" || gr.GroupReceive.Value != 7 {
		t.Errorf("trigger = %+v, want groupName=svc-up value=7", gr.GroupReceive)
	}
	bs, ok := f.lastTA.Action.(*pb.TriggerAction_BashScript)
	if !ok {
		t.Fatalf("Action = %T, want *pb.TriggerAction_BashScript", f.lastTA.Action)
	}
	if bs.BashScript.ScriptName != "startup.sh" {
		t.Errorf("scriptName = %q, want startup.sh", bs.BashScript.ScriptName)
	}

	// Wire side: the response must carry both oneofs back.
	var decoded map[string]interface{}
	if err := json.Unmarshal(out, &decoded); err != nil {
		t.Fatalf("response is not valid JSON: %v\n%s", err, out)
	}
	if _, ok := decoded["groupReceive"]; !ok {
		t.Errorf("response lost the groupReceive oneof: %s", out)
	}
	if _, ok := decoded["bashScript"]; !ok {
		t.Errorf("response lost the bashScript oneof: %s", out)
	}
}

func TestEmptyBodyMeansEmptyMessage(t *testing.T) {
	b, _ := newTestBridge(t)
	// The many Empty-taking RPCs are called with no body at all by a frontend
	// doing `fetch(url, {method:"POST"})`; that must not be a 400.
	for _, body := range []string{"", "   ", "null", "{}"} {
		out, err := b.Call(context.Background(), "GetFlag", []byte(body))
		if err != nil {
			t.Fatalf("Call with body %q: %v", body, err)
		}
		if !strings.Contains(string(out), `"tbool":true`) {
			t.Errorf("body %q -> %s, want tbool:true", body, out)
		}
	}
}

func TestEmitUnpopulatedKeepsZeroValues(t *testing.T) {
	b, _ := newTestBridge(t)
	out, err := b.Call(context.Background(), "EchoTriggerAction", []byte(`{"id":0,"isActive":false}`))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	// A UI binding a checkbox to isActive needs the key present even when false.
	var decoded map[string]interface{}
	if err := json.Unmarshal(out, &decoded); err != nil {
		t.Fatalf("bad JSON: %v", err)
	}
	for _, k := range []string{"id", "isActive"} {
		if _, ok := decoded[k]; !ok {
			t.Errorf("zero-valued field %q was omitted: %s", k, out)
		}
	}
}

func TestUnknownMethodIsNotFound(t *testing.T) {
	b, _ := newTestBridge(t)
	_, err := b.Call(context.Background(), "NoSuchThing", nil)
	if err == nil {
		t.Fatal("expected an error for an unknown method")
	}
	var ce *CallError
	if !errors.As(err, &ce) {
		t.Fatalf("error is %T, want *CallError", err)
	}
	if ce.Code != codes.NotFound {
		t.Errorf("code = %v, want NotFound", ce.Code)
	}
}

func TestMalformedBodyIsInvalidArgument(t *testing.T) {
	b, _ := newTestBridge(t)
	_, err := b.Call(context.Background(), "GetFlag", []byte(`{this is not json`))
	var ce *CallError
	if !errors.As(err, &ce) {
		t.Fatalf("error is %T, want *CallError", err)
	}
	if ce.Code != codes.InvalidArgument {
		t.Errorf("code = %v, want InvalidArgument", ce.Code)
	}
}

func TestUnknownFieldsAreDiscarded(t *testing.T) {
	b, _ := newTestBridge(t)
	// A frontend round-tripping a response it decorated locally must not 400.
	if _, err := b.Call(context.Background(), "GetFlag", []byte(`{"notAField":123}`)); err != nil {
		t.Errorf("unknown field should be discarded, got: %v", err)
	}
}

func TestHandlerStatusCodeIsPreserved(t *testing.T) {
	b, _ := newTestBridge(t)
	_, err := b.Call(context.Background(), "BoomStatus", nil)
	var ce *CallError
	if !errors.As(err, &ce) {
		t.Fatalf("error is %T, want *CallError", err)
	}
	if ce.Code != codes.PermissionDenied {
		t.Errorf("code = %v, want PermissionDenied (handler status must survive)", ce.Code)
	}
	if ce.Msg != "nope" {
		t.Errorf("msg = %q, want %q", ce.Msg, "nope")
	}
}

func TestPlainErrorBecomesUnknown(t *testing.T) {
	b, _ := newTestBridge(t)
	_, err := b.Call(context.Background(), "BoomPlain", nil)
	var ce *CallError
	if !errors.As(err, &ce) {
		t.Fatalf("error is %T, want *CallError", err)
	}
	// status.FromError maps a non-status error to Unknown; assert we surface
	// that rather than mislabelling it as a client error.
	if ce.Code != codes.Unknown {
		t.Errorf("code = %v, want Unknown", ce.Code)
	}
}

func TestNewRejectsBadTargets(t *testing.T) {
	if _, err := New(nil); err == nil {
		t.Error("New(nil) should fail")
	}
	var nilSvc *fakeSvc
	if _, err := New(nilSvc); err == nil {
		t.Error("New(typed nil pointer) should fail")
	}
	type noRPCs struct{}
	if _, err := New(&noRPCs{}); err == nil {
		t.Error("New(type with no RPCs) should fail")
	}
}

func TestHTTPStatusMapping(t *testing.T) {
	cases := map[codes.Code]int{
		codes.OK:                 200,
		codes.InvalidArgument:    400,
		codes.Unauthenticated:    401,
		codes.PermissionDenied:   403,
		codes.NotFound:           404,
		codes.AlreadyExists:      409,
		codes.ResourceExhausted:  429,
		codes.Internal:           500,
		codes.Unimplemented:      501,
		codes.Unavailable:        503,
		codes.DeadlineExceeded:   504,
		codes.Canceled:           499,
		codes.FailedPrecondition: 400,
	}
	for code, want := range cases {
		if got := HTTPStatus(code); got != want {
			t.Errorf("HTTPStatus(%v) = %d, want %d", code, got, want)
		}
	}
}

func TestMarshalMessageMatchesCallEncoding(t *testing.T) {
	b, _ := newTestBridge(t)
	ev := &pb.EventValue{Val: &pb.EventValue_Tbool{Tbool: true}}
	direct, err := MarshalMessage(ev)
	if err != nil {
		t.Fatalf("MarshalMessage: %v", err)
	}
	viaCall, err := b.Call(context.Background(), "GetFlag", nil)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	// Streamed events and unary responses must share one encoding so the
	// frontend can use a single decoder for both.
	if string(direct) != string(viaCall) {
		t.Errorf("MarshalMessage=%s but Call=%s; encodings must match", direct, viaCall)
	}
}

func TestMarshalMessageNil(t *testing.T) {
	out, err := MarshalMessage(nil)
	if err != nil {
		t.Fatalf("MarshalMessage(nil): %v", err)
	}
	if string(out) != "{}" {
		t.Errorf("MarshalMessage(nil) = %s, want {}", out)
	}
}
