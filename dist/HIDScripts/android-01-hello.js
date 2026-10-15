/*
 * android-01-hello.js -- type visible text into whatever field has focus, to
 *                        prove an Android phone accepted a USB keyboard.
 *
 * TARGET:      Android 9 and later, over USB OTG.
 * NEEDS:       the phone UNLOCKED, and a TEXT FIELD ALREADY FOCUSED with the
 *              cursor in it. Open any notes app, messaging draft, or the browser
 *              address bar and tap into it first. This script cannot create that
 *              condition -- read the next block for why.
 * TOUCHES:     it types characters into the focused field. Nothing is saved,
 *              sent, or installed -- but if you focused a messaging app's
 *              compose box, the text is sitting in a draft, so choose the field
 *              you focus deliberately.
 * LIMITATION:  substantial, and specific. Read the ceiling block below before
 *              planning anything around this platform.
 * VERIFIED:    not yet tested on hardware. Android keyboard handling varies by
 *              OEM and version more than any other platform here, so treat
 *              every claim in this file as needing confirmation on the actual
 *              handset you care about.
 *
 * THE ANDROID CEILING, AND WHY IT IS WHERE IT IS
 *
 * Most published Android BadUSB material depends on keys this device physically
 * cannot send. That is not an opinion -- it is readable in the shipped USB HID
 * report descriptor at service/SubSysUSB.go:62, which declares usage page 0x07
 * (Keyboard/Keypad) and usages 0x00 to 0x65, and NOTHING ELSE. There is no
 * Consumer Control page (0x0C) in the descriptor. So:
 *
 *   NOT POSSIBLE: the Android HOME button, BACK as a system key, APP_SWITCH,
 *   volume up/down, power, play/pause, camera, search. Every one of those is a
 *   consumer-control or system usage this descriptor does not expose. A payload
 *   that claims to press HOME on Android is not working around this -- it is
 *   wrong.
 *
 *   NOT POSSIBLE EITHER: command execution. There is no shell on a stock
 *   Android reachable from a keyboard. No run dialog, no terminal, no ADB
 *   (which needs USB debugging enabled, a host-side authorisation the user must
 *   accept on screen, and is a completely different USB function from HID).
 *
 *   ACTUALLY POSSIBLE: text entry into a focused field, which is what this
 *   script does; focus traversal with TAB and the arrow keys, which
 *   android-02-focus-walk.js demonstrates; ENTER to activate the focused
 *   control; and ESC, which many Android versions treat as Back -- that last
 *   one is the closest thing to a system key available, and it is inconsistent
 *   enough that it is demonstrated rather than relied on.
 *
 * So Android, for this device, is a text-entry and focus-navigation target. That
 * is a real capability and worth demonstrating honestly. It is not a shell, and
 * a kit that implies otherwise will embarrass whoever repeats the claim.
 */

layout('us');          // Android honours the layout IT is set to, not this one.
                       // A phone configured for a non-US layout will mangle
                       // punctuation exactly as a desktop would. See keymap-check.js.
typingSpeed(30, 40);   // Deliberately slower than the desktop scripts: soft
                       // keyboards and IME layers on Android drop fast input
                       // more readily than a desktop driver does.

// No waitLED here. Android is not reliably going to send a keyboard LED report,
// so waiting on one would stall for the whole timeout on a perfectly healthy
// phone. A plain settle delay is the honest tool. See platform-detect.js.
delay(1000);

type("This text was typed by a USB device, not by a person.\n");
type("An Android phone will accept a USB keyboard over OTG and trust it.\n");
type("Nothing was installed. Nothing was sent. Clear this field.\n");

return "android-01-hello: typed into the focused field. If nothing appeared, no text " +
"field had focus -- this device cannot create one. See the header.";
