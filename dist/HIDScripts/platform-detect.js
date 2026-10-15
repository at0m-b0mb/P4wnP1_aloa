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
 * VERIFIED:    not yet run on hardware. waitLED's return shape, however, IS now
 *              verified against hid/controller.go:527-543 -- see "THE RETURN
 *              SHAPE" below for what that corrected.
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
 * THE RETURN SHAPE, AND THE BUG THAT FINDING IT OUT FIXED
 *
 * waitLED returns an object, built at hid/controller.go:527-543:
 *
 *   ERROR bool, ERRORTEXT string, TIMEOUT bool,
 *   NUM bool, CAPS bool, SCROLL bool, COMPOSE bool, KANA bool
 *
 * This script was first written against a GUESS at those names, because the
 * only shipped caller -- wifi_covert_channel.js:69 -- throws the result away,
 * so nothing in the repository demonstrated them. The guess happened to get
 * TIMEOUT right. It missed ERROR entirely, and that was a real bug: ERROR is
 * set when there is no keyboard at all (controller.go:516,
 * `if ctl.Keyboard == nil { err = ErrNoKeyboard }`), and in that case TIMEOUT
 * is FALSE. So the old helper read "not a timeout" as "a report arrived" and
 * would have announced WINDOWS_OR_LINUX with nothing plugged in -- confidently
 * wrong, which is the worst way for a detector to fail.
 *
 * Hence ledReportArrived() now reads the three real fields directly and treats
 * ERROR as its own outcome rather than folding it into either other case.
 */

var ATTACH_SETTLE_MS = 1200;  // let enumeration finish before judging silence
var LISTEN_MS = 1500;         // how long silence must last to count as silence

// Returns "report", "timeout", "error" or "unknown-shape".
//
// TIMEOUT IS CHECKED BEFORE ERROR, and the order is the whole of this function.
//
// hid/controller.go builds the result as:
//     ERROR:   err != nil
//     TIMEOUT: err == ErrTimeout
// so a plain timeout sets BOTH flags. An earlier version of this helper tested
// ERROR first -- with a confident comment explaining why -- and therefore read
// every timeout as "there is no keyboard".
//
// That is not a theoretical ordering nit. macOS does not send keyboard LED
// reports, so waitLED ALWAYS times out there, so this script reported
// NO_KEYBOARD on exactly the platform it exists to recognise as a Mac. Caught
// by running it against a real Mac; nothing in the unit tests could have.
//
// ES5 only.
function ledReportArrived(r) {
	if (r === null || typeof r !== "object") return "unknown-shape";
	if (r.TIMEOUT === true) return "timeout";
	if (r.ERROR === true) return "error";   // a REAL error: no keyboard, etc.
	if (r.ERROR === false) return "report";
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

var guess;
var why;

// "error" means the keyboard function is not there -- no USB host, or a gadget
// deployed without hid.keyboard. That is a BROKEN SETUP, not a quiet host, and
// it must never be reported as a platform guess. Checked before everything else
// for the same reason ERROR is checked first inside the helper.
if (onAttach === "error" || echoed === "error") {
	guess = "NO_KEYBOARD";
	why = "waitLED reported ERROR, which means there is no keyboard to wait on. " +
	      "Either nothing is plugged into a host, or the deployed USB gadget has " +
	      "no hid.keyboard function. Fix the link before reading anything into this";
} else if (onAttach === "unknown-shape" || echoed === "unknown-shape") {
	guess = "UNKNOWN";
	why = "waitLED returned a shape this script does not recognise, so no " +
	      "detection was possible. Compare hid/controller.go:527-543 against " +
	      "ledReportArrived() -- the struct has probably changed";
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

return "platform-detect: " + guess + " -- " + why + ". Treat as a hint, not a fact.";
