package service

import (
	"fmt"
	"strings"
	"testing"

	"github.com/robertkrimen/otto"
)

// Audit finding S1. executeActionStartHidScript builds a preamble of
// `var NAME='value';` lines and glues it onto the front of the operator's
// HIDScript. Those values used to be interpolated raw, and two of them come
// from an unauthenticated party:
//
//	DHCP_LEASE_HOST  the client's own DHCP option 12
//	SSH_LOGIN_USER   the username from an SSH login ATTEMPT
//
// Breaking out of the quotes therefore meant arbitrary HIDScript -- keystrokes
// of the attacker's choosing, typed by the device into the attached host.
//
// These tests run the generated preamble through the REAL otto, the same
// interpreter the service uses, and assert two things: nothing injected
// executed, and the value still arrives intact. Asserting only the first would
// pass for an escaper that threw the data away.

// injectionPayloads are the shapes that break a naive '%s'. Each tries to end
// the string literal and start a statement.
var injectionPayloads = []struct {
	name string
	in   string
}{
	{"single quote and a call", `x';type('pwned');var y='`},
	{"escaped quote", `x\';type('pwned');var y='`},
	{"double quote", `x";type("pwned");var y="`},
	{"newline inside the literal", "x'\ntype('pwned');var y='"},
	{"carriage return", "x'\rtype('pwned');var y='"},
	{"trailing backslash", `x\`},
	{"line comment swallowing the rest", `x';type('pwned');//`},
	{"block comment", `x';type('pwned');/*`},
	// U+2028 and U+2029 are LINE TERMINATORS in JavaScript. A hand-rolled
	// escaper that handles quotes, backslashes and \n almost always forgets
	// these two, and they end a statement from inside the quotes.
	{"U+2028 line separator", "x type('pwned'); var y='"},
	{"U+2029 paragraph separator", "x type('pwned'); var y='"},
	{"NUL byte", "x\x00type('pwned')"},
	{"script close, for good measure", `x</script>';type('pwned');var y='`},
	// And the ordinary case: a legitimate hostname that merely contains an
	// apostrophe must survive unharmed, not be mangled or dropped.
	{"legitimate apostrophe", "pat's-laptop"},
	{"legitimate unicode", "lópez-macbook"},
}

// runPreamble builds the preamble the way the code does, runs it in otto with a
// recording type(), and reports what the variable ended up as and what was
// typed. buildLine is injected so the same harness can exercise the OLD,
// vulnerable formatting as a negative control.
func runPreamble(t *testing.T, buildLine func(string) string, value string) (got string, typed []string, runErr error) {
	t.Helper()

	src := buildLine(value)
	vm := otto.New()
	if err := vm.Set("type", func(call otto.FunctionCall) otto.Value {
		typed = append(typed, call.Argument(0).String())
		return otto.UndefinedValue()
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := vm.Run(src); err != nil {
		// A syntax error is a FAILED injection, not a pass: record it and let
		// the caller decide. The fix must produce parseable source.
		return "", typed, err
	}
	v, err := vm.Get("DHCP_LEASE_HOST")
	if err != nil {
		t.Fatal(err)
	}
	got, _ = v.ToString()
	return got, typed, nil
}

func safeLine(v string) string {
	return fmt.Sprintf("var DHCP_LEASE_HOST=%s;\n", jsStringLiteral(v))
}

func vulnerableLine(v string) string {
	// Exactly what the code did before the fix.
	return fmt.Sprintf("var DHCP_LEASE_HOST='%s';\n", v)
}

func TestHidScriptPreambleResistsInjection(t *testing.T) {
	for _, tc := range injectionPayloads {
		t.Run(tc.name, func(t *testing.T) {
			got, typed, err := runPreamble(t, safeLine, tc.in)
			if err != nil {
				t.Fatalf("the escaped preamble must still parse, got: %v\nsource: %s", err, safeLine(tc.in))
			}
			if len(typed) != 0 {
				t.Errorf("INJECTION EXECUTED: type() was called with %q", typed)
			}
			// The data must survive, byte for byte. An escaper that returns ""
			// would pass the check above and be useless.
			if got != tc.in {
				t.Errorf("value was not preserved:\n got  %q\n want %q", got, tc.in)
			}
		})
	}
}

// TestTheOldFormattingWasReallyVulnerable is the negative control. Without it,
// the test above would pass just as happily against code that was never broken,
// and this repository has shipped four gates with exactly that property.
func TestTheOldFormattingWasReallyVulnerable(t *testing.T) {
	const payload = `x';type('pwned');var y='`

	_, typed, err := runPreamble(t, vulnerableLine, payload)
	if err != nil {
		t.Fatalf("the vulnerable form should have parsed and run the injection, got %v", err)
	}
	if len(typed) == 0 {
		t.Fatal("the old formatting did NOT execute the injection -- this test proves nothing, " +
			"and TestHidScriptPreambleResistsInjection is therefore not evidence of a fix")
	}
	if typed[0] != "pwned" {
		t.Errorf("expected the injected type('pwned'), got %q", typed)
	}
}

// TestJsStringLiteralShape pins the output format itself, so a future "tidy-up"
// that swaps json.Marshal for something hand-rolled has to meet the same bar.
func TestJsStringLiteralShape(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"plain", `"plain"`},
		{`has"quote`, `"has\"quote"`},
		{`has\backslash`, `"has\\backslash"`},
		{"has\nnewline", `"has\nnewline"`},
		{"has\ttab", `"has\ttab"`},
	} {
		if got := jsStringLiteral(tc.in); got != tc.want {
			t.Errorf("jsStringLiteral(%q) = %s, want %s", tc.in, got, tc.want)
		}
	}

	// Always quoted, always parseable, whatever goes in.
	for _, tc := range injectionPayloads {
		got := jsStringLiteral(tc.in)
		if !strings.HasPrefix(got, `"`) || !strings.HasSuffix(got, `"`) {
			t.Errorf("jsStringLiteral(%q) = %s -- must be a quoted literal", tc.in, got)
		}
	}
}

// TestEverySshLoginUserPayloadIsAlsoSafe covers the second attacker-controlled
// field explicitly. It shares the escaper, but SSH_LOGIN_USER is reachable
// without even associating -- a login ATTEMPT carries the username -- so it is
// worth its own failing test rather than an assumption of symmetry.
func TestEverySshLoginUserPayloadIsAlsoSafe(t *testing.T) {
	for _, tc := range injectionPayloads {
		src := fmt.Sprintf("var SSH_LOGIN_USER=%s;\n", jsStringLiteral(tc.in))
		vm := otto.New()
		var typed []string
		if err := vm.Set("type", func(call otto.FunctionCall) otto.Value {
			typed = append(typed, call.Argument(0).String())
			return otto.UndefinedValue()
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := vm.Run(src); err != nil {
			t.Errorf("%s: preamble must parse, got %v", tc.name, err)
			continue
		}
		if len(typed) != 0 {
			t.Errorf("%s: INJECTION EXECUTED via SSH_LOGIN_USER: %q", tc.name, typed)
		}
		v, _ := vm.Get("SSH_LOGIN_USER")
		if got, _ := v.ToString(); got != tc.in {
			t.Errorf("%s: value not preserved: got %q want %q", tc.name, got, tc.in)
		}
	}
}
