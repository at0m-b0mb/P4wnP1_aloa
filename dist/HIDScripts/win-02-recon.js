/*
 * win-02-recon.js -- open a console and run the host's own inventory commands,
 *                    leaving the output on screen to read and photograph.
 *
 * TARGET:      Windows 10, Windows 11
 * NEEDS:       an unlocked, logged-in session. No administrator rights: every
 *              command below works as a standard user.
 * TOUCHES:     nothing. It opens cmd.exe, runs read-only built-ins, and leaves
 *              the window open. Nothing is written to disk, nothing installed,
 *              no network connection is made.
 * LIMITATION:  the output stays on the TARGET's screen. This device is a
 *              keyboard: it has no way to read back what appeared, so there is
 *              no "results" to collect here. You read the screen. That is the
 *              honest shape of recon over a HID-only channel, and it is why
 *              engagement notes for this payload are a photograph.
 * VERIFIED:    not yet tested on hardware.
 *
 * WHY cmd AND NOT POWERSHELL
 *
 * PowerShell is the better shell and the worse choice here. It is slower to
 * start, its first-run profile work is visible, and on a managed estate it is
 * far more likely to be logged, constrained, or watched than cmd.exe running
 * `ver`. For reading five facts off a machine in front of its owner, cmd is
 * quieter, faster and completely sufficient.
 *
 * It is also easier on the keymap. PowerShell payloads are dense with $ { } [ ]
 * and ', every one of which moves between layouts -- see keymap-check.js. The
 * commands below are almost entirely letters and spaces, so they survive a
 * layout mismatch that would mangle a PowerShell one-liner.
 */

layout('us');          // Match the TARGET's layout. Run keymap-check.js if unsure.
typingSpeed(20, 25);

press("GUI r");
delay(800);            // Run dialog: cold-machine budget. See win-01-hello.js.
type("cmd\n");
delay(1200);           // cmd's window needs to exist before it can take keys.

// Each command on its own line, so a dropped keystroke spoils one line rather
// than the whole run, and so the operator can see which step produced what.
type("echo === P4wnP1 read-only host inventory ===\n");
delay(150);
type("ver\n");                                  // Windows version
delay(300);
type("hostname\n");                             // machine name
delay(300);
type("whoami /user\n");                         // user and SID
delay(400);
type("whoami /groups | findstr /i \"Administrators Domain\"\n");  // is this user privileged
delay(600);
type("ipconfig | findstr /i \"IPv4 Gateway\"\n");                 // addressing
delay(600);
type("systeminfo | findstr /i \"OS Name OS Version Domain Boot Time\"\n");
delay(1500);           // systeminfo is genuinely slow; do not shorten this.
type("echo === end === \n");

"win-02-recon: commands typed into cmd.exe. Read the target's screen -- this " +
"device cannot see the output.";
