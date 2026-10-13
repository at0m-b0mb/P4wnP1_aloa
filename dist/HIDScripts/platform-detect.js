/*
 * platform-detect.js -- guess the host OS from keyboard LED behaviour, and
 *                       return the guess so a trigger can branch on it.
 *
 * TARGET:      any host that accepts a USB keyboard.
 * NEEDS:       nothing. It does not need a focused text field and types no
 *              visible character, so it is safe to run first.
 * TOUCHES:     it toggles NUMLOCK and toggles it back, so the host's lock state
 *              is left as it was found. Nothing else.
 * LIMITATION:  a heuristic, and a coarse one. See both comment blocks below.
 * VERIFIED:    not yet tested on hardware, and ONE SPECIFIC THING IS UNVERIFIED
 *              BY DESIGN -- see "THE RETURN SHAPE" below. Confirm that first.
 *
 * HOW IT WORKS, AND WHY IT IS ONLY A HINT
 *
 * A USB keyboard has exactly one feedback channel: the host sends LED reports
 * back saying which of NUM, CAPS and SCROLL are lit. waitLED(mask, timeoutMs)
 * blocks until one changes or the timeout expires. That is the whole of what
 * this device can learn about the host. There is no way to read the screen and
 * no way to run a command and see its output.
 *
 * The families differ in how they treat a newly attached keyboard:
 *
 *   - Windows pushes the current lock state to a new keyboard almost at once.
 *     wifi_covert_channel.js relies on exactly this, and its comment says so:
 *     it waits for "an arriving LED report, which is sent by windows after
 *     keyboard driver initialization". That is the single best-attested
 *     observation available here, because a shipped upstream payload depends
 *     on it.
 *   - Linux with a desktop environment usually behaves the same way, though
 *     which LEDs it asserts varies by distribution.
 *   - macOS has no SCROLL lock concept and generally does not drive NUMLOCK on
 *     an external keyboard the way the other two do, so the common observation
 *     is silence.
 *
 * Silence is therefore the macOS signal -- and that is precisely why this is a
 * hint and not a fact, because silence is ALSO what you get from a locked
 * machine, a host still enumerating, a KVM in the path, some virtual machines,
 * and every Android and iOS device. The honest reading of that result is "not
 * obviously Windows or Linux", which is how it is worded below.
 *
 * Use it to choose a default. Do not use it to decide something you cannot undo.
 *
 * THE RETURN SHAPE, AND WHY THIS CODE IS WRITTEN SO DEFENSIVELY
 *
 * waitLED's two-argument call is confirmed from wifi_covert_channel.js:69.
 * What its return VALUE looks like is not: the only shipped script that calls
 * it throws the result away, so nothing in this repository demonstrates the
 * field names. Rather than guess a shape and ship a payload that silently
 * mis-detects every host, ledReportArrived() below accepts every plausible
 * shape and falls back to a stated assumption when it recognises none.
 *
 * FIRST JOB ON HARDWARE: run this, read the returned string, and if it says the
 * shape was unrecognised, read hid/*.go for waitLED's real return value and
 * replace this helper with a direct field read. Then delete this block.
 */

var ATTACH_SETTLE_MS = 1200;  // let enumeration finish before judging silence
var LISTEN_MS = 1500;         // how long silence must last to count as silence

// Returns one of "report", "timeout" or "unknown-shape". ES5 only -- no
// String.includes, no Object.keys shortcuts, nothing otto will reject.
function ledReportArrived(r) {
	if (r === undefined || r === null) {
		// Some bindings return undefined on timeout and an object otherwise.
		return "timeout";
	}
	if (typeof r === "boolean") {
		// Others return true when a report arrived.
		return r ? "report" : "timeout";
	}
	if (typeof r === "object") {
		// Upstream style is uppercase field names; be ready for either.
		if (r.TIMEOUT === true || r.timeout === true) return "timeout";
		if (r.TIMEOUT === false || r.timeout === false) return "report";
		// An object with LED state but no timeout flag: a report, presumably.
		if (r.NUM !== undefined || r.num !== undefined ||
		    r.CAPS !== undefined || r.caps !== undefined) return "report";
	}
	return "unknown-shape";
}

delay(ATTACH_SETTLE_MS);

// Step 1: did the host volunteer an LED report without being asked?
var onAttach = ledReportArrived(waitLED(ANY_OR_NONE, LISTEN_MS));

// Step 2: ask directly. Toggle NUMLOCK and see whether the host echoes it back.
// A host that tracks NUMLOCK answers; one that does not, stays quiet.
press("NUM");
var echoed = ledReportArrived(waitLED(ANY_OR_NONE, LISTEN_MS));

// Put the lock state back exactly as found. An operator should never discover
// afterwards that a detection script left their NUMLOCK inverted.
press("NUM");
delay(120);

var shapeKnown = (onAttach !== "unknown-shape" && echoed !== "unknown-shape");
var guess;
var why;

if (!shapeKnown) {
	guess = "UNKNOWN";
	why = "waitLED returned a shape this script does not recognise, so no " +
	      "detection was possible. Read hid/*.go and fix ledReportArrived()";
} else if (echoed === "report") {
	guess = "WINDOWS_OR_LINUX";
	why = onAttach === "report"
		? "the host volunteered an LED report on attach and echoed a NUMLOCK change"
		: "the host echoed a NUMLOCK change but volunteered nothing on attach";
} else if (onAttach === "report") {
	guess = "UNCLEAR";
	why = "a report arrived on attach but NUMLOCK was not echoed";
} else {
	guess = "NOT_OBVIOUSLY_WINDOWS_OR_LINUX";
	why = "no LED report at all. macOS, Android, iOS, a locked machine and a " +
	      "host still enumerating all look identical here";
}

"platform-detect: " + guess + " -- " + why + ". Treat as a hint, not a fact.";
