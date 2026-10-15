/*
 * mac-01-hello.js -- prove the device is a keyboard, via Spotlight and TextEdit.
 *
 * TARGET:      macOS 12 and later (CMD+SPACE Spotlight has been stable far longer)
 * NEEDS:       an unlocked, logged-in session, AND the keyboard already
 *              identified -- read the next block, this is the one that bites.
 * TOUCHES:     nothing on disk. It opens TextEdit and types into an unsaved
 *              document. Close without saving and no trace remains.
 * LIMITATION:  keystrokes go wherever focus is, and this device cannot see the
 *              screen to check. Spotlight is also the most focus-sensitive
 *              launcher of the three desktop platforms -- see the delays.
 * VERIFIED:    YES -- run against a real Mac on 2026-10-14 through the device.
 *              Spotlight opened, TextEdit launched, all four lines typed with
 *              no dropped or garbled characters on the 'us' keymap. The
 *              Keyboard Setup Assistant did NOT appear, but that Mac had seen
 *              this gadget many times already -- the warning below is about
 *              FIRST attach and remains unverified.
 *
 * THE KEYBOARD SETUP ASSISTANT -- READ THIS BEFORE BLAMING THE PAYLOAD
 *
 * The first time macOS sees an unfamiliar USB keyboard, it can open the Keyboard
 * Setup Assistant: a window that asks you to identify the keyboard by pressing
 * the key next to the left shift. It takes focus, it is modal, and it swallows
 * everything this device types. A payload that worked perfectly in your own
 * testing will do nothing at all on a fresh target, and the reason is sitting
 * in a window the operator has not looked at.
 *
 * There is no way to defeat it from here -- the assistant wants specific
 * physical keys and reads them as a hardware probe. Plan around it:
 *
 *   - Expect it on first attach to any Mac that has not seen this gadget before.
 *   - It is per-keyboard-identity, so changing the gadget's USB product or
 *     vendor ID re-triggers it on a Mac that had already been taught.
 *   - If a demo must work first time, attach once beforehand and dismiss it.
 *
 * Treat "nothing happened on a Mac" as this, until you have ruled it out.
 *
 * WHICH LAUNCHER ANSWERS CMD+SPACE IS A PROPERTY OF THE HOST
 *
 * This script passed, and partly by luck. The machine it ran against had
 * RAYCAST on CMD+SPACE, not Spotlight -- Raycast happened to rank TextEdit
 * first, so ENTER opened the right thing. Its sibling mac-02-recon.js asked the
 * same launcher for "Terminal", got something else, and failed.
 *
 * Alfred, LaunchBar and Raycast all intercept this shortcut and all rank
 * differently. Note that macOS is the only platform here with this problem:
 * Windows GUI+R and Linux ALT+F2 are RUN dialogs, which execute exactly the
 * text you give them, while CMD+SPACE opens a ranked SEARCH whose top hit
 * depends on the host's own content and history.
 *
 * WHY THE DELAYS ARE LONGER THAN THEY LOOK LIKE THEY NEED TO BE
 *
 * Spotlight animates in, and it drops characters typed during that animation --
 * so "TextEdit" becomes "xtEdit" and Spotlight launches something else, or
 * nothing. Worse, Spotlight keeps SEARCHING after you stop typing, and the
 * highlighted result can change in the moment between your last character and
 * your ENTER. Both delays below exist for that: one to let the panel appear,
 * one to let the result list settle before committing to it. Shortening the
 * second is how you end up launching a random document on someone's Mac.
 */

layout('us');          // Match the TARGET's layout. See keymap-check.js.
typingSpeed(20, 30);

// macOS is the platform least likely to send an LED report, so do not wait on
// one here -- a plain delay is the honest tool. See platform-detect.js.
delay(600);

press("GUI SPACE");    // GUI is CMD on macOS. Spotlight.
delay(900);            // let the panel animate in before typing. See header.
type("TextEdit");
delay(1200);           // let the result list settle BEFORE ENTER. See header.
press("ENTER");
delay(2500);           // TextEdit cold-starts slowly and may open a file dialog.

// TextEdit may open with its "New Document" chooser rather than a blank page,
// depending on the user's settings. CMD+N gives a blank document either way, and
// is harmless if one is already open -- it just makes a second one.
press("GUI n");
delay(900);

type("This line was typed by a USB device, not by a person.\n");
type("It was plugged into this Mac's USB port and macOS believed it was a keyboard.\n");
type("\n");
type("Nothing was installed and nothing was saved. Close without saving.\n");

return "mac-01-hello: typed into TextEdit. If nothing appeared, check for the Keyboard " +
"Setup Assistant window -- see this script's header.";
