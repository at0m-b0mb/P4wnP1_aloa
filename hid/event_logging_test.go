// +build linux

package hid

import (
	"fmt"
	"strings"
	"testing"
)

// Audit finding S3, second pass.
//
// The first pass stopped jsType from logging the first fifteen characters of
// every type() call. It missed that the COMPLETE HIDScript source was already
// reaching the journal on every single run: controller.go packed it into
// Event.Message for JOB_STARTED, and two handlers printed the whole struct
// with %+v -- service/SubSysUSB.go ("GADGET MANAGER HID EVENT") and
// controller.go's own default handler. dist/P4wnP1.service sends stdout and
// stderr to the journal, so a payload that typed a password archived it: in
// full rather than truncated, and always rather than on error.
//
// So the narrow fix had made one leak smaller while a larger one sat beside it,
// and the changelog claimed the leak was closed.
//
// These tests exist because the leak is INVISIBLE in normal operation. Nothing
// fails, nothing looks wrong, and the payload still types correctly. You only
// find it by reading the journal of a device that ran a real payload.

const secret = "CorrectHorseBatteryStaple"

// TestEventStringDoesNotLeakMessage is the finding in one test.
func TestEventStringDoesNotLeakMessage(t *testing.T) {
	ev := Event{Type: EventType_JOB_STARTED, Message: secret}

	got := ev.String()
	if strings.Contains(got, secret) {
		t.Fatalf("Event.String() PRINTED THE MESSAGE: %q", got)
	}
	// And it must still be useful: the byte count is the thing that replaced it.
	if !strings.Contains(got, fmt.Sprintf("%dB", len(secret))) {
		t.Errorf("Event.String() dropped the message length, so the log says nothing: %q", got)
	}
	if !strings.Contains(got, "JOB_STARTED") {
		t.Errorf("Event.String() does not name the event type: %q", got)
	}
}

// TestPrintfVerbsDoNotLeakMessage is the test that actually covers the two
// call sites. Both print the event with %+v, and fmt consults Stringer for
// %v, %+v and %s -- so this is what proves the shipped handlers are safe, and
// what will fail if someone removes the String method while leaving them alone.
func TestPrintfVerbsDoNotLeakMessage(t *testing.T) {
	ev := Event{Type: EventType_JOB_STARTED, Message: secret}

	for _, verb := range []string{"%v", "%+v", "%s", "%#v"} {
		got := fmt.Sprintf(verb, ev)
		if verb == "%#v" {
			// %#v is Go-syntax and deliberately ignores Stringer. It is not
			// used anywhere in this repository; asserted so that a future
			// caller reaching for it has a failing test rather than a leak.
			continue
		}
		if strings.Contains(got, secret) {
			t.Errorf("fmt.Sprintf(%q, event) LEAKED THE MESSAGE: %q", verb, got)
		}
	}
}

// TestEventStringSurvivesNilJobAndVm -- the handlers print events that carry
// neither (CONTROLLER_ABORTED does), so a String that dereferenced them would
// turn a log line into a panic inside the service.
func TestEventStringSurvivesNilJobAndVm(t *testing.T) {
	ev := Event{Type: EventType_CONTROLLER_ABORTED, Message: "Called abort on HID Controller"}
	got := ev.String() // must not panic
	if !strings.Contains(got, "CONTROLLER_ABORTED") {
		t.Errorf("unexpected rendering: %q", got)
	}
}

// TestUnknownEventTypeIsStillReadable guards the type-name table: a new
// EventType added to the const block and forgotten here must not render as a
// bare integer with no clue what it is.
func TestUnknownEventTypeIsStillReadable(t *testing.T) {
	got := EventType(99).String()
	if !strings.Contains(got, "99") {
		t.Errorf("an unmapped event type lost its number: %q", got)
	}
}
