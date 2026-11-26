package hid

import "fmt"

type EventType int32

const (
	EventType_JOB_STARTED EventType = 0
	EventType_JOB_STOPPED EventType = 1
	EventType_CONTROLLER_ABORTED EventType = 2
	EventType_JOB_CANCELLED EventType = 3
	EventType_JOB_SUCCEEDED EventType = 4
	EventType_JOB_SUCCEEDED_NO_RESULT EventType = 5
	EventType_JOB_FAILED EventType = 6
	EventType_JOB_WAIT_LED_FINISHED EventType = 7
	EventType_JOB_WAIT_LED_REPEATED_FINISHED EventType = 8
	EventType_JOB_NO_FREE_VM EventType = 9

)

/*
var EventType_name = map[int32]string{
	0: "JOB_STARTED",
	1: "JOB_STOPPED",
	2: "CONTROLLER_ABORTED",
	3: "JOB_CANCELLED",

}
var EventType_value = map[string]int32{
	"JOB_STARTED": 0,
	"JOB_STOPPED": 1,
	"CONTROLLER_ABORTED": 2,
	"JOB_CANCELLED": 3,
}
*/

var eventTypeName = map[EventType]string{
	EventType_JOB_STARTED:                    "JOB_STARTED",
	EventType_JOB_STOPPED:                    "JOB_STOPPED",
	EventType_CONTROLLER_ABORTED:             "CONTROLLER_ABORTED",
	EventType_JOB_CANCELLED:                  "JOB_CANCELLED",
	EventType_JOB_SUCCEEDED:                  "JOB_SUCCEEDED",
	EventType_JOB_SUCCEEDED_NO_RESULT:        "JOB_SUCCEEDED_NO_RESULT",
	EventType_JOB_FAILED:                     "JOB_FAILED",
	EventType_JOB_WAIT_LED_FINISHED:          "JOB_WAIT_LED_FINISHED",
	EventType_JOB_WAIT_LED_REPEATED_FINISHED: "JOB_WAIT_LED_REPEATED_FINISHED",
	EventType_JOB_NO_FREE_VM:                 "JOB_NO_FREE_VM",
}

func (t EventType) String() string {
	if n, ok := eventTypeName[t]; ok {
		return n
	}
	return fmt.Sprintf("EventType(%d)", int32(t))
}

type Event struct {
	Type    EventType
	Job     *AsyncOttoJob
	Vm      *AsyncOttoVM
	Message string
	//ScriptSource string
}

// String keeps Message OUT of the log, and it is the whole reason this method
// exists.
//
// Audit finding S3, second pass. The first pass stopped jsType from logging the
// first fifteen characters of every type() call, and missed that the complete
// HIDScript SOURCE was already going to the journal on every single run:
// controller.go packed it into Message for JOB_STARTED, and two handlers
// printed the whole struct with %+v. The unit sends stdout to the journal, so a
// payload that types a password archived it -- always, not only on error, and
// in full rather than truncated. The narrow fix had made the leak smaller in
// one place while a larger one sat beside it.
//
// fmt uses this method for %v, %+v and %s on an Event, so the fix reaches every
// caller that exists and every one added later -- which matters more than
// patching the two call sites, because the next `%+v` on an event is the kind of
// line nobody reads twice. Message is reported only as a byte count: an
// operator still learns a message was attached and how big it was, and the
// event type already names the condition, which is what the fixed-string
// messages were saying anyway.
//
// If you need the text of a message, print event.Message deliberately, at a
// site where you have decided it cannot carry what a payload typed.
func (e Event) String() string {
	job := "-"
	if e.Job != nil {
		job = fmt.Sprintf("%d", e.Job.Id)
	}
	vm := "-"
	if e.Vm != nil {
		vm = fmt.Sprintf("%d", e.Vm.Id)
	}
	return fmt.Sprintf("hid.Event{Type:%s Job:%s Vm:%s Message:%dB}", e.Type, job, vm, len(e.Message))
}

type EventHandler interface {
	HandleEvent(event Event)
}
