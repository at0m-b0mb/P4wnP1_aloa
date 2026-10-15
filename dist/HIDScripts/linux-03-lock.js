/*
 * linux-03-lock.js -- lock the screen, and double as the portable "does this
 *                     link even work" test for Linux targets.
 *
 * TARGET:      a Linux desktop session. GUI+L is the lock shortcut on GNOME,
 *              KDE Plasma, Cinnamon, MATE and Ubuntu -- the widest agreement any
 *              shortcut enjoys across Linux desktops.
 * NEEDS:       an unlocked, logged-in graphical session with a screen locker
 *              installed. A bare window manager with no locker will do nothing.
 * TOUCHES:     the session's lock state. Nothing on disk, nothing installed.
 * LIMITATION:  not undoable from here -- this device cannot type the user's
 *              password back, and should not. On XFCE the default is
 *              CTRL+ALT+DELETE rather than GUI+L, and on i3 or sway it is
 *              whatever the user bound, so "nothing happened" does not prove
 *              keystrokes were not delivered on those desktops.
 * VERIFIED:    not yet tested on hardware.
 *
 * WHY RUN THIS FIRST ON A LINUX TARGET
 *
 * linux-01-hello.js and linux-02-recon.js both have to guess at a launcher, and
 * when the guess is wrong they type into whatever had focus -- which on someone
 * else's machine is the one outcome you cannot take back. This script guesses at
 * nothing. It sends one combination that the major desktops agree on, and the
 * result is unambiguous and visible from across the room.
 *
 * Lock screen appears: keystrokes arrive, modifiers work, go ahead.
 * Nothing happens: either the desktop is one of the exceptions above, or the
 * link is not working -- and either way, find out before typing commands blind.
 *
 * GUI+L needs no layout, because L sits in the same position on every keymap
 * this device ships. layout() is still called so the script does not inherit
 * whatever the previous job happened to set.
 */

layout('us');

waitLED(ANY_OR_NONE, 3000);
delay(300);

press("GUI l");

return "linux-03-lock: sent GUI+L. If the target did not lock, check its desktop " +
"environment against this script's LIMITATION line before blaming the link.";
