/*
 * mac-02-recon.js -- open Terminal and run the host's own inventory commands,
 *                    leaving the output on screen to read and photograph.
 *
 * TARGET:      macOS 12 and later
 * NEEDS:       an unlocked session, and the Keyboard Setup Assistant already
 *              dealt with (see mac-01-hello.js). No administrator rights: every
 *              command below runs as a standard user and none uses sudo.
 * TOUCHES:     nothing. Read-only built-ins, output left in the Terminal window.
 *              Nothing written, installed, or sent anywhere.
 * LIMITATION:  the output stays on the TARGET's screen. This device is a
 *              keyboard with no return channel, so it cannot collect what it
 *              produced. You read the screen; your engagement note is a photo.
 * VERIFIED:    FAILED on first hardware run, 2026-10-14. Terminal never
 *              launched. The cause was not macOS -- RAYCAST owned CMD+SPACE on
 *              that machine, not Spotlight. Raycast matched "TextEdit" for
 *              mac-01-hello and launched it, but its top hit for "Terminal" was
 *              something else, and this script pressed ENTER on it blind.
 *              Nothing was executed. See the launcher note below.
 *
 * WHY NOT system_profiler ON ITS OWN
 *
 * `system_profiler` with no arguments takes tens of seconds and floods the
 * window with thousands of lines, which is useless in front of a client and
 * slow enough that an operator assumes the payload hung. The single data type
 * below returns the half-dozen facts anyone actually wants in about a second.
 *
 * ONE KEYMAP NOTE
 *
 * The `|` and `"` characters below move between layouts more than anything else
 * in these commands. If the output looks like a shell syntax error rather than
 * an inventory, that is a layout mismatch and not a broken command -- run
 * keymap-check.js and fix layout() above.
 */

// WHICH LAUNCHER ANSWERS CMD+SPACE IS A PROPERTY OF THE HOST, NOT OF macOS.
//
// Raycast, Alfred and LaunchBar all intercept it, and all rank results
// differently from Spotlight and from each other. On the machine this script
// first ran against, Raycast held the shortcut: it found TextEdit for
// mac-01-hello.js and launched it, and then sent this script somewhere else
// entirely for "Terminal".
//
// So the query below is a variable, and the delay after it is long on purpose.
// ios-02-spotlight.js refuses to press ENTER at all for this reason -- the
// reasoning was written there before a Mac proved it, and had not been applied
// to its siblings.
//
// If this opens the wrong thing on your target, set QUERY to something that
// ranks unambiguously in THAT host's launcher. There is no value that is
// correct everywhere.
var QUERY = "Terminal";

layout('us');
typingSpeed(20, 25);

delay(600);

press("GUI SPACE");
delay(1200);           // let the launcher appear before typing into it
type(QUERY);
delay(2000);           // LONG: the result list reorders as it searches, and
                       // whatever is highlighted when ENTER lands is what opens
press("ENTER");
delay(3000);           // a cold terminal is slow, and may restore old windows

type("echo '=== P4wnP1 read-only host inventory ==='\n");
delay(150);
type("sw_vers\n");                                   // macOS product and build
delay(400);
type("hostname\n");
delay(300);
type("id -un; id -Gn\n");                            // user, and its groups
delay(400);
type("sysctl -n hw.model\n");                        // hardware model identifier
delay(400);
type("ifconfig | grep 'inet ' | grep -v 127.0.0.1\n");  // addressing
delay(600);
type("system_profiler SPHardwareDataType | head -20\n");
delay(2500);           // the one slow command here. Do not shorten.
type("echo '=== end ==='\n");

return "mac-02-recon: commands typed into Terminal. Read the target's screen -- this " +
"device cannot see the output.";
