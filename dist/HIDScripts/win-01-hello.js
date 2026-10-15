/*
 * win-01-hello.js -- prove the device is a keyboard, by opening Run and typing.
 *
 * TARGET:      Windows 10, Windows 11
 * NEEDS:       an unlocked, logged-in desktop session. GUI+R does nothing on
 *              the lock screen, which is a precondition, not a bug.
 * TOUCHES:     nothing. It opens the Run dialog, types one line into Notepad,
 *              and leaves the window on screen for you to read. Nothing is
 *              saved, installed, or changed.
 * LIMITATION:  the keystrokes land wherever focus is. If something steals focus
 *              between GUI+R and the first type(), the text goes there instead.
 *              There is no way for this device to read the screen and check --
 *              it is a keyboard, it has no feedback channel.
 * VERIFIED:    not yet tested on hardware.
 *
 * WHY THE DELAYS ARE WHAT THEY ARE
 *
 * The Run dialog is not ready the instant GUI+R is sent, and the gap is much
 * longer on a machine that has just booted or just woken than on a warm one --
 * the shell has to page in. 800ms is chosen to survive a cold machine; on a
 * warm one it merely looks slow. If a payload of this shape ever "types half a
 * command into the desktop", this delay is the first thing to raise.
 */

layout('us');          // Set this to match the TARGET's layout, not yours.
typingSpeed(20, 30);   // Deliberately not 0: see win-02-keymap.js on why.

press("GUI r");
delay(800);            // Run dialog, cold-machine budget. See header.
type("notepad\n");
delay(1500);           // Notepad's first window is slow to take focus.

type("This line was typed by a USB device, not by a person.\n");
type("It was plugged in at ");
type("this computer's keyboard port and the computer believed it.\n");
type("\n");
type("Nothing was installed and nothing was changed. Close this window.\n");

return "win-01-hello: typed into Notepad. Check the target's screen.";
