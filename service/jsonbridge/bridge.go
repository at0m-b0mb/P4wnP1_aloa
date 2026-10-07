// Package jsonbridge exposes a gRPC service implementation over plain
// JSON/HTTP by reflecting over its method set.
//
// # WHY THIS EXISTS
//
// P4wnP1's web client was written in GopherJS against gRPC-web with the
// websocket transport. That transport replaces the HTTP upgrade request's
// headers with headers carried inside the first websocket frame
// (see improbable-eng/grpc-web's wrapper.go: `req.Header = headers`), so a
// browser cannot attach an Authorization header or a cookie to a gRPC call
// without the client itself putting it in that frame. The GopherJS client
// never did, and rebuilding it requires a pinned Go 1.12 toolchain.
//
// Rather than resurrect that toolchain, this package re-publishes the exact
// same service over a transport any frontend can speak: one POST per RPC,
// JSON in, JSON out, a normal `Authorization: Bearer` header. Because it
// discovers methods reflectively there is no per-RPC code to write or keep in
// sync -- adding an RPC to the .proto and implementing it on the server is
// enough for it to appear here.
//
// Message encoding is protojson, not encoding/json. That matters: the service
// definition uses protobuf `oneof` fields (TriggerAction.Trigger,
// TriggerAction.Action, DeployedGadgetSettings' value union), which compile to
// Go interface fields. encoding/json cannot unmarshal into an interface, so it
// would silently lose every trigger and action -- the heart of the automation
// engine. protojson understands oneofs and round-trips them correctly.
package jsonbridge

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"

	"github.com/golang/protobuf/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
)

var (
	ctxType   = reflect.TypeOf((*context.Context)(nil)).Elem()
	errType   = reflect.TypeOf((*error)(nil)).Elem()
	protoType = reflect.TypeOf((*proto.Message)(nil)).Elem()
)

// marshalOpts keeps zero values in the output and emits the PROTO field names.
//
// EmitUnpopulated: a frontend rendering a form needs to know a boolean is
// false, not have the field vanish; this makes the JSON shape stable and lets
// the UI bind directly to it.
//
// UseProtoNames is load-bearing and was missing. Without it protojson emits the
// lowerCamelCase JSON name, so `use_HID_KEYBOARD` went out as `useHIDKEYBOARD`
// and `rndis_settings` as `rndisSettings`. The console reads the proto names --
// the ones written in grpc.proto and shown by every protobuf tool -- so ALL
// ELEVEN keys it needs from GadgetSettings were absent from the response. Every
// USB checkbox rendered unchecked whatever was deployed, the cable strip showed
// every function off, and toggling one sent both spellings at once, which
// protojson rejects as a duplicate field.
//
// Unmarshalling is unaffected: protojson accepts either spelling on input, so
// this only changes what we emit, and it changes it to match the .proto.
var marshalOpts = protojson.MarshalOptions{
	EmitUnpopulated: true,
	UseProtoNames:   true,
}

// unmarshalOpts tolerates fields the server does not know. Without this a
// frontend echoing back a response body it had augmented (or one built against
// a newer proto) would get a hard 400 instead of having the extra keys ignored.
var unmarshalOpts = protojson.UnmarshalOptions{DiscardUnknown: true}

// Method is a single discovered unary RPC.
type Method struct {
	Name    string // canonical name, e.g. "GetDeviceState"
	fn      reflect.Value
	reqType reflect.Type // concrete struct type behind the *Req argument
}

// Bridge dispatches JSON payloads to a gRPC service implementation.
type Bridge struct {
	target reflect.Value

	mu      sync.RWMutex
	methods map[string]*Method // key: lowercased name
	names   []string           // canonical names, sorted
}

// New reflects over target (a gRPC service implementation, e.g. *server) and
// returns a Bridge covering every unary RPC it implements.
//
// A method is treated as a unary RPC when it looks exactly like generated gRPC
// server code:
//
//	func (T) Name(context.Context, *Req) (*Resp, error)
//
// where Req and Resp are both protobuf messages. Anything else on the type --
// helpers, streaming RPCs, lifecycle methods -- is ignored, so this is safe to
// point at a type with a lot of non-RPC surface.
func New(target interface{}) (*Bridge, error) {
	if target == nil {
		return nil, errors.New("jsonbridge: target is nil")
	}
	tv := reflect.ValueOf(target)
	if !tv.IsValid() || (tv.Kind() == reflect.Ptr && tv.IsNil()) {
		return nil, errors.New("jsonbridge: target is a nil pointer")
	}

	b := &Bridge{target: tv, methods: make(map[string]*Method)}
	tt := tv.Type()
	for i := 0; i < tt.NumMethod(); i++ {
		m := tt.Method(i)
		if !m.IsExported() {
			continue
		}
		ft := m.Func.Type()
		// Receiver + ctx + req in; resp + error out.
		if ft.NumIn() != 3 || ft.NumOut() != 2 {
			continue
		}
		if ft.In(1) != ctxType {
			continue
		}
		if ft.Out(1) != errType {
			continue
		}
		req, resp := ft.In(2), ft.Out(0)
		if req.Kind() != reflect.Ptr || resp.Kind() != reflect.Ptr {
			continue
		}
		if !req.Implements(protoType) || !resp.Implements(protoType) {
			continue
		}
		b.methods[strings.ToLower(m.Name)] = &Method{
			Name:    m.Name,
			fn:      m.Func,
			reqType: req.Elem(),
		}
		b.names = append(b.names, m.Name)
	}
	if len(b.methods) == 0 {
		return nil, errors.New("jsonbridge: target exposes no unary RPC methods")
	}
	sort.Strings(b.names)
	return b, nil
}

// Methods returns the canonical names of every dispatchable RPC, sorted.
// Served at /api/v1/rpc so a frontend (or an operator with curl) can
// discover the surface without reading the .proto.
func (b *Bridge) Methods() []string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make([]string, len(b.names))
	copy(out, b.names)
	return out
}

// Has reports whether name is a dispatchable RPC (case-insensitive).
func (b *Bridge) Has(name string) bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	_, ok := b.methods[strings.ToLower(name)]
	return ok
}

// Call dispatches one RPC. body is the raw JSON request (empty is treated as
// an empty message, which is what the many Empty-taking RPCs want). The
// returned bytes are the JSON-encoded response.
//
// Errors come back as *CallError carrying a gRPC code, so the HTTP layer can
// map them to a status without re-deriving intent.
func (b *Bridge) Call(ctx context.Context, name string, body []byte) ([]byte, error) {
	b.mu.RLock()
	m, ok := b.methods[strings.ToLower(name)]
	b.mu.RUnlock()
	if !ok {
		return nil, &CallError{Code: codes.NotFound, Msg: fmt.Sprintf("no such method %q", name)}
	}

	reqPtr := reflect.New(m.reqType)
	payload := strings.TrimSpace(string(body))
	if payload == "" || payload == "null" {
		payload = "{}"
	}
	pm, ok := reqPtr.Interface().(proto.Message)
	if !ok {
		return nil, &CallError{Code: codes.Internal, Msg: "request type is not a protobuf message"}
	}
	if err := unmarshalOpts.Unmarshal([]byte(payload), proto.MessageV2(pm)); err != nil {
		return nil, &CallError{Code: codes.InvalidArgument, Msg: fmt.Sprintf("malformed request body: %v", err)}
	}

	out := m.fn.Call([]reflect.Value{b.target, reflect.ValueOf(ctx), reqPtr})

	// out[1] is the error. Preserve any gRPC status the handler set so the
	// HTTP layer reports PermissionDenied as 403 rather than a blanket 500.
	if errv := out[1]; !errv.IsNil() {
		err := errv.Interface().(error)
		if st, ok := status.FromError(err); ok {
			return nil, &CallError{Code: st.Code(), Msg: st.Message()}
		}
		return nil, &CallError{Code: codes.Unknown, Msg: err.Error()}
	}

	respv := out[0]
	if respv.IsNil() {
		return []byte("{}"), nil
	}
	rpm, ok := respv.Interface().(proto.Message)
	if !ok {
		return nil, &CallError{Code: codes.Internal, Msg: "response type is not a protobuf message"}
	}
	encoded, err := marshalOpts.Marshal(proto.MessageV2(rpm))
	if err != nil {
		return nil, &CallError{Code: codes.Internal, Msg: fmt.Sprintf("failed to encode response: %v", err)}
	}
	return encoded, nil
}

// CallError carries a gRPC code alongside the message so HTTPStatus can map it.
type CallError struct {
	Code codes.Code
	Msg  string
}

func (e *CallError) Error() string { return e.Msg }

// HTTPStatus maps a gRPC code to the closest HTTP status.
func HTTPStatus(c codes.Code) int {
	switch c {
	case codes.OK:
		return 200
	case codes.InvalidArgument, codes.FailedPrecondition, codes.OutOfRange:
		return 400
	case codes.Unauthenticated:
		return 401
	case codes.PermissionDenied:
		return 403
	case codes.NotFound:
		return 404
	case codes.AlreadyExists, codes.Aborted:
		return 409
	case codes.ResourceExhausted:
		return 429
	case codes.Unimplemented:
		return 501
	case codes.Unavailable:
		return 503
	case codes.DeadlineExceeded:
		return 504
	case codes.Canceled:
		// Client went away. 499 is nginx's convention and is more honest than
		// pretending the request succeeded or that the server broke.
		return 499
	default:
		return 500
	}
}

// MarshalMessage encodes a single protobuf message with the same options the
// bridge uses for RPC responses, so a streamed event is shaped identically to
// a unary response and a frontend can share one decoder for both.
func MarshalMessage(m proto.Message) ([]byte, error) {
	if m == nil {
		return []byte("{}"), nil
	}
	return marshalOpts.Marshal(proto.MessageV2(m))
}
