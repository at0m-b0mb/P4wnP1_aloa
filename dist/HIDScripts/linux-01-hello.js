/*
 * linux-01-hello.js -- prove the device is a keyboard, by opening a terminal
 *                      and typing into it.
 *
 * TARGET:      a Linux DESKTOP session. Which one matters -- see below.
 * NEEDS:       an unlocked, logged-in graphical session. ASSUMES CTRL+ALT+T
 *              opens a terminal, which is true on GNOME, Ubuntu, Cinnamon and
 *              most Debian derivatives, and NOT true on a stock KDE Plasma,
 *              XFCE, or i3 install.
 * TOUCHES:     nothing. It opens a terminal and types `echo` lines. No file is
 *              written, nothing is installed, no network connection is made.
 * LIMITATION:  there is no universal launcher on Linux, and this device cannot
 *              see the screen to find out which one worked. Read the next block
 *              before running this on a target you do not control.
 * VERIFIED:    not yet tested on hardware.
 *
 * THE FRAGMENTATION IS THE STORY
 *
 * Windows has GUI+R. macOS has CMD+SPACE. Linux has whatever the desktop
 * environment decided, and they disagree:
 *
 *   CTRL+ALT+T   GNOME, Ubuntu, Cinnamon, most Debian derivatives. Not KDE.
 *   ALT+F2       GNOME and KDE run dialog. Not XFCE by default.
 *   GUI          GNOME activities overview, type to search.
 *   none at all  i3, sway, bare WMs, and any desktop whose user rebound it.
 *
 * This script tries CTRL+ALT+T and then ALT+F2 as a fallback, and that is as
 * far as blind fallback can honestly go. It CANNOT verify either worked, so if
 * both fail, the `echo` lines below are typed into whatever had focus -- a
 * browser, a chat window, a document. On an engagement that is the difference
 * between a clean demonstration and typing into a stranger's email.
 *
 * So: know the target's desktop before you run this. If you do not, run
 * linux-03-lock.js first -- GUI+L is far more portable, and if the screen locks
 * you at least know keystrokes arrive.
 */

layout('us');          // Match the TARGET's layout. See keymap-check.js.
typingSpeed(20, 30);

// Most Linux desktops do send an LED report once the HID driver binds, so this
// wait usually returns quickly and saves a blind delay. The timeout means a
// desktop that stays silent costs 3 seconds rather than hanging the job.
waitLED(ANY_OR_NONE, 3000);
delay(300);

// First choice: a terminal directly.
press("CTRL ALT t");
delay(1800);           // terminal emulators cold-start slower than a dialog

// Fallback: the run dialog, then ask it for a terminal. If CTRL+ALT+T already
// worked, this types "xterm" into that terminal and runs it -- harmless, and
// visible, which is better than silent ambiguity. If it did not work, this is
// the path that saves the run.
press("ALT F2");
delay(900);
type("xterm\n");
delay(1800);

type("echo 'This line was typed by a USB device, not by a person.'\n");
delay(200);
type("echo 'It was plugged into this machine and the kernel bound it as a keyboard.'\n");
delay(200);
type("echo 'Nothing was installed and nothing was written. Close this window.'\n");

"linux-01-hello: tried CTRL+ALT+T then ALT+F2. Read the target's screen -- if " +
"the text went somewhere unexpected, see this script's header on launchers.";
