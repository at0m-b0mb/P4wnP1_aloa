/*
 * keymap-check.js -- type the whole printable set, so you can SEE whether the
 *                    layout you chose matches the target's.
 *
 * TARGET:      any host that accepts a USB keyboard. Platform-agnostic.
 * NEEDS:       a text field already focused on the target. Run one of the
 *              <platform>-01-hello.js scripts first to open an editor, or click
 *              into any text box yourself.
 * TOUCHES:     nothing. It types into whatever has focus. Read it and discard.
 * LIMITATION:  the device cannot read the screen, so it cannot tell you whether
 *              the output was right -- only YOU can, by looking. That is the
 *              entire point of this script existing.
 * VERIFIED:    not yet tested on hardware.
 *
 * WHY THIS IS THE MOST USEFUL SCRIPT IN THE BOX
 *
 * A keyboard does not send characters. It sends key POSITIONS, and the host
 * decides what each position means using whatever layout the host is set to.
 * So if the target is a German machine and you typed with layout('us'), then
 * type("ps -y") arrives as "ps -z", a path separator becomes something else
 * entirely, and a payload that is perfectly correct fails in a way that looks
 * like the payload is broken.
 *
 * Punctuation is where this bites, because punctuation moves between layouts far
 * more than letters do. The lines below are ordered worst-first: if line 1 comes
 * out wrong, stop and change the layout, because every shell payload in this
 * directory depends on those characters.
 *
 * Set LAYOUT below to each candidate in turn and run it again until the screen
 * matches the comment. The accepted values are:
 *
 *   US DE CH RU BE BR CA CM ES FI FR GB IT SV   (case-insensitive)
 *
 * Those are the "Name" fields INSIDE dist/keymaps/*.json, not the filenames,
 * and the two differ: the German map is the file DE_ASCII.json but its Name is
 * "DE", so layout('de') works and layout('de_ascii') fails. hid/keyboard.go:128
 * keys the table on strings.ToUpper(kbdmap.Name), which is the whole reason.
 */

var LAYOUT = 'us';

layout(LAYOUT);
typingSpeed(20, 10);   // slow enough that a host cannot drop keys, fast enough to watch

// Line 1: the characters shell payloads actually need. Check this one first.
type("1: \\ / | - _ . , : ; ' \" ` $ & * ( ) { } [ ] < > ? ! @ # % ^ ~ + =\n");

// Line 2: digits and the shifted row. A wrong layout usually survives digits
// and fails the shifted row, so disagreement between these two lines tells you
// it is a layout problem and not a dropped-keystroke problem.
type("2: 0123456789\n");
type("3: !\"#$%&'()\n");

// Line 4: letters. These are almost always right; the classic exceptions are
// y/z on German layouts and q/a/w/z on French ones, which is exactly what this
// line is for.
type("4: abcdefghijklmnopqrstuvwxyz\n");
type("5: ABCDEFGHIJKLMNOPQRSTUVWXYZ\n");

// Line 6: a real command, assembled from the characters above, so you can judge
// the thing you care about rather than judging a character table. It is a
// comment on every platform's shell, so pressing ENTER on it does nothing.
type("6: # cd \"/tmp\" && ls -la | grep '^d' > /dev/null; echo $?\n");

type("\n");
type("Compare line 1 against the script's comment. If it differs, change\n");
type("LAYOUT at the top and run this again before running anything else.\n");

return "keymap-check: typed 6 lines with layout '" + LAYOUT + "'. Read the target's screen and compare.";
