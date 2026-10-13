/*
 * linux-02-recon.js -- open a terminal and run the host's own inventory
 *                      commands, leaving the output on screen.
 *
 * TARGET:      a Linux DESKTOP session. Assumes CTRL+ALT+T opens a terminal --
 *              read linux-01-hello.js on why that assumption is the weak point.
 * NEEDS:       an unlocked graphical session. No root: every command below runs
 *              as a normal user and none uses sudo.
 * TOUCHES:     nothing. Read-only commands, output left in the terminal.
 * LIMITATION:  the output stays on the TARGET's screen -- this device is a
 *              keyboard with no return channel. You read the screen.
 *              Also: `lsb_release` is absent on a surprising number of modern
 *              distributions, which is why /etc/os-release is read directly.
 * VERIFIED:    not yet tested on hardware.
 *
 * WHY THESE COMMANDS AND NOT THE OBVIOUS ONES
 *
 * `lsb_release -a` is the command everyone reaches for and it is missing on
 * stock Fedora, on minimal Debian, and on most containers -- it lives in a
 * separate package. /etc/os-release is part of systemd and is therefore present
 * on effectively every desktop Linux in use, so it is the reliable answer to
 * "what is this machine".
 *
 * `ip` rather than `ifconfig` for the same reason, in the other direction:
 * net-tools is no longer installed by default on most distributions, so
 * `ifconfig` is the one that is missing now.
 *
 * KEYMAP WARNING, SHARPER HERE THAN ELSEWHERE
 *
 * A terminal can run with a different keyboard layout from the graphical
 * session that launched it -- the X or Wayland layout and the console layout are
 * configured separately and genuinely do disagree on some systems. So a payload
 * can type correctly into a GUI text field on this host and produce garbage in
 * its terminal. If these commands come out mangled, that is the first thing to
 * suspect, and keymap-check.js run INSIDE the terminal is how you confirm it.
 */

layout('us');
typingSpeed(20, 25);

waitLED(ANY_OR_NONE, 3000);
delay(300);

press("CTRL ALT t");
delay(1800);

type("echo '=== P4wnP1 read-only host inventory ==='\n");
delay(150);
type("cat /etc/os-release | head -4\n");        // distribution and version
delay(400);
type("uname -srm\n");                           // kernel, release, machine
delay(300);
type("hostname\n");
delay(300);
type("id\n");                                   // user, groups, and so privilege
delay(300);
type("ip -brief address | grep -v ' lo '\n");   // addressing, loopback removed
delay(500);
type("echo '=== end ==='\n");

"linux-02-recon: commands typed into a terminal. Read the target's screen -- " +
"this device cannot see the output.";
