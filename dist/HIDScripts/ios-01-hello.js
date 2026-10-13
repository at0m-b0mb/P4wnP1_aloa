/*
 * ios-01-hello.js -- type visible text into a focused field on an iPhone or
 *                    iPad, and document exactly how little else is possible.
 *
 * TARGET:      iOS 13 and later, iPadOS 13 and later.
 * NEEDS:       a great deal. In order: a working physical connection (see the
 *              block below -- this is the step that usually fails), the device
 *              UNLOCKED, and a TEXT FIELD ALREADY FOCUSED with the cursor in it.
 *              Open Notes, tap into a note, and leave the keyboard up.
 * TOUCHES:     it types characters into the focused field. In Notes that is a
 *              real edit to a real note, saved automatically by the app -- so
 *              use a scratch note, not something that matters.
 * LIMITATION:  iOS is the hardest target in this box and the easiest to write
 *              something dishonest about. Read the ceiling block.
 * VERIFIED:    not yet tested on hardware. Nothing in this file should be
 *              repeated to a client until it has been seen working on the
 *              specific device and iOS version in question.
 *
 * THE CONNECTION IS THE HARD PART, NOT THE PAYLOAD
 *
 * Before any keystroke matters, the phone has to accept the device at all:
 *
 *   - iPhone 15 and later, and recent iPads, have USB-C and can take a USB
 *     keyboard directly.
 *   - Older iPhones need the Lightning to USB Camera Adapter. The plain
 *     Lightning-to-USB cable will not do it.
 *   - The phone must supply bus power to the gadget, and it may simply refuse,
 *     reporting that the accessory needs too much power. A powered hub between
 *     them is the usual answer and it is not always enough.
 *   - "Allow accessory to connect" prompts and USB Restricted Mode can block
 *     the whole thing. A phone locked for over an hour will not talk to a new
 *     USB accessory at all until it is unlocked.
 *
 * If nothing happens when you run this, the overwhelmingly likely reason is one
 * of the five above and not the script. Check the connection before debugging
 * anything else.
 *
 * THE iOS CEILING, STATED PLAINLY
 *
 * What is real: text entry into a field that already has focus, which is what
 * this script does. On iPadOS, CMD+SPACE can reach Spotlight. Arrow keys and
 * TAB navigate in some apps. On iPadOS, Full Keyboard Access expands this a
 * long way -- but it is OFF by default and turning it on needs someone to walk
 * through Settings by hand, so a payload cannot assume it.
 *
 * What is NOT real, at all: there is no shell, no command execution, no
 * filesystem access, no way to install or run anything, and no system keys --
 * the home and lock buttons are consumer-control usages, and this device's HID
 * report descriptor (service/SubSysUSB.go:62) declares only usage page 0x07,
 * usages 0x00 to 0x65. A locked iPhone gives you essentially nothing.
 *
 * This file and ios-02-spotlight.js are the entire honest iOS set, and that is
 * the right size for it. Two scripts that work beat six that imply a capability
 * this platform does not grant. If you need a sentence for a client: "on iOS
 * this device can type into a field you have already opened, and that is all."
 */

layout('us');          // iOS honours the layout the DEVICE is set to. A phone
                       // configured for another language will mangle punctuation
                       // just as a desktop would. See keymap-check.js.
typingSpeed(40, 50);   // The slowest in the set, on purpose. iOS text input
                       // goes through an IME layer that drops fast input more
                       // readily than any desktop driver.

// No waitLED. iOS will not reliably send a keyboard LED report, so waiting on
// one stalls for the full timeout on a healthy device. See platform-detect.js.
delay(1500);           // iOS is also the slowest to bind a new keyboard.

type("This text was typed by a USB device, not by a person.\n");
type("An iPhone will accept a USB keyboard and trust what it types.\n");
type("Nothing was installed - on iOS, nothing could be. Delete this note.\n");

"ios-01-hello: typed into the focused field. If nothing appeared, check the " +
"physical connection first -- see this script's header, it is almost always that.";
