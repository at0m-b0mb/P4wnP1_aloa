/*
 * win-03-lock.js -- lock the workstation. One key combination, nothing else.
 *
 * TARGET:      Windows 10, Windows 11 (GUI+L has locked Windows since XP)
 * NEEDS:       an unlocked, logged-in session.
 * TOUCHES:     the session's lock state. Nothing on disk, nothing installed,
 *              no files read or written. The user's work stays exactly as it
 *              was behind the lock screen.
 * LIMITATION:  it cannot be undone from here -- this device has no way to type
 *              the user's password back in, and should not. Someone has to log
 *              in again.
 * VERIFIED:    not yet tested on hardware.
 *
 * WHY THIS TINY SCRIPT IS WORTH SHIPPING
 *
 * It is the cleanest demonstration in the box. It proves the device reached the
 * machine and was obeyed, with an effect the owner sees instantly and feels in
 * their stomach -- and it touches nothing at all. For an awareness demo in a
 * room full of people, "I plugged this in and your screen locked" lands harder
 * than any amount of typed text, and it leaves you nothing to clean up.
 *
 * It is also the right first test against a NEW target. If the lock screen
 * appears, keystrokes are being delivered and the host is obeying modifiers,
 * which is most of what can go wrong. If nothing happens, stop and fix the
 * link before trying a payload whose failure is harder to see.
 *
 * GUI+L needs no layout: it is a key POSITION plus a modifier, and L sits in the
 * same place on every layout this device ships a keymap for. layout() is still
 * called so the script does not inherit whatever the last job happened to set.
 */

layout('us');

// Wait for the host's keyboard driver to be up. On a freshly attached gadget the
// first keystrokes can be sent before Windows has finished binding the HID
// driver, and they are simply lost -- which looks exactly like a broken payload.
// wifi_covert_channel.js uses the same wait for the same reason: an arriving LED
// report means Windows has initialised the keyboard.
waitLED(ANY_OR_NONE, 5000);
delay(200);

press("GUI l");

"win-03-lock: sent GUI+L. The target should be at its lock screen.";
