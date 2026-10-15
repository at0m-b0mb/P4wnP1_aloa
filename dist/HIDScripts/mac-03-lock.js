/*
 * mac-03-lock.js -- lock the screen. One key combination, nothing else.
 *
 * TARGET:      macOS 10.13 and later (CTRL+CMD+Q is the built-in Lock Screen
 *              shortcut from High Sierra onward)
 * NEEDS:       an unlocked, logged-in session, and the Keyboard Setup Assistant
 *              already dealt with (see mac-01-hello.js).
 * TOUCHES:     the session's lock state. Nothing on disk, nothing installed.
 *              The user's work stays exactly as it was behind the lock screen.
 * LIMITATION:  not undoable from here -- this device cannot type the user's
 *              password back, and should not. Someone has to log in again.
 *              On macOS older than 10.13 this combination does nothing; the old
 *              equivalent was CTRL+SHIFT+EJECT, and there is no EJECT key in
 *              this device's keymap, so there is no fallback to offer.
 * VERIFIED:    not yet tested on hardware.
 *
 * WHY THIS IS THE RIGHT FIRST TEST ON A MAC
 *
 * It is the fastest way to find out whether anything is being delivered at all.
 * macOS is the platform where a payload most often does nothing for a reason
 * that has nothing to do with the payload -- the Keyboard Setup Assistant
 * holding focus, or the gadget not yet bound. Both of those also stop this
 * script, and this script fails VISIBLY in one second rather than leaving you
 * to wonder whether Spotlight launched the wrong thing.
 *
 * So: run this before mac-01-hello.js on a new target. Screen locks, the link
 * works. Nothing happens, fix the link -- do not go hunting in the payload.
 *
 * It is also the cleanest demonstration in the box for an audience, and it
 * leaves nothing behind to clean up.
 */

layout('us');

// No LED wait here: macOS is the platform least likely to send an LED report,
// so waiting for one would stall for the full timeout on a perfectly healthy
// host. A plain delay is the honest tool. See platform-detect.js.
delay(800);

press("CTRL GUI q");   // GUI is CMD. Lock Screen.

return "mac-03-lock: sent CTRL+CMD+Q. The target should be at its lock screen. If not, " +
"see this script's LIMITATION line and mac-01-hello.js on the Setup Assistant.";
