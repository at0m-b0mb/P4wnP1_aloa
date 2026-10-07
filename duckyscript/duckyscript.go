// Package duckyscript converts DuckyScript payloads into HIDScript, the
// JavaScript dialect P4wnP1's HID engine executes.
//
// # WHY
//
// DuckyScript is the lingua franca of USB HID injection: the large majority of
// published payloads for this class of device are written in it. P4wnP1's own
// HIDScript is strictly more capable -- it is real JavaScript, with mouse
// control, LED-state feedback and conditionals -- but nothing is written in it.
// Converting rather than reimplementing means the existing corpus runs here
// without anyone retyping it, and the result is ordinary HIDScript that an
// operator can read, diff and edit afterwards.
//
// # SCOPE
//
// DuckyScript 1.0 is covered in full. Later dialects (Hak5's DuckyScript 2.0
// and 3.0) added variables, conditionals, functions and exfiltration verbs that
// have no 1:1 HIDScript form; those are reported as warnings rather than
// silently dropped, because a payload that converts cleanly but is missing a
// third of its logic is worse than one that refuses.
//
// The conversion is deliberately total and non-executing: it reads text and
// writes text. It never touches a keyboard, a device file or the network.
package duckyscript

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Options controls conversion.
type Options struct {
	// Layout is the keyboard layout the generated script selects, matching a
	// file in dist/keymaps (US, DE_ASCII, gb, fr, ...). DuckyScript has no
	// layout concept -- it assumes the target's layout matches whatever the
	// payload author used -- so this has to come from outside. Empty means no
	// layout() call is emitted and the device default applies.
	Layout string

	// TypingSpeedMillis and TypingJitterMillis, if non-zero, emit a
	// typingSpeed() call. Constant-rate typing is both unrealistic and, on a
	// slow target, unreliable: keystrokes outrun the focused application.
	TypingSpeedMillis  int
	TypingJitterMillis int
}

// Warning is something the converter could not represent faithfully.
type Warning struct {
	Line int    // 1-based line in the source
	Text string // the offending source line, trimmed
	Why  string
}

func (w Warning) String() string {
	return fmt.Sprintf("line %d: %s  (%q)", w.Line, w.Why, w.Text)
}

// keyAliases maps DuckyScript key names onto the names in dist/keymaps.
// Lower-cased on lookup. Anything not here and not a single printable
// character is reported as a warning rather than guessed at.
var keyAliases = map[string]string{
	// modifiers
	"ctrl": "CTRL", "control": "CTRL",
	"alt":   "ALT",
	"shift": "SHIFT",
	"gui":   "GUI", "windows": "GUI", "win": "GUI", "command": "GUI", "cmd": "GUI",
	// named keys
	"enter": "ENTER", "return": "ENTER",
	"esc": "ESCAPE", "escape": "ESCAPE",
	"space": "SPACE", "spacebar": "SPACE",
	"tab":       "TAB",
	"backspace": "BACKSPACE", "bksp": "BACKSPACE",
	"delete": "DELETE", "del": "DELETE",
	"insert": "INSERT", "ins": "INSERT",
	"home": "HOME", "end": "END",
	"pageup": "PAGEUP", "pagedown": "PAGEDOWN",
	"up": "UP", "uparrow": "UP",
	"down": "DOWN", "downarrow": "DOWN",
	"left": "LEFT", "leftarrow": "LEFT",
	"right": "RIGHT", "rightarrow": "RIGHT",
	"capslock":    "CAPSLOCK",
	"numlock":     "NUMLOCK",
	"scrolllock":  "SCROLLLOCK",
	"printscreen": "PRINTSCR", "prtscn": "PRINTSCR", "printscrn": "PRINTSCR",
	"break": "BREAK", "pause": "PAUSE",
}

// unsupportedKeys are real DuckyScript key names with no equivalent in the
// shipped keymaps. Naming them explicitly produces a precise warning instead of
// a vague "unknown command".
var unsupportedKeys = map[string]string{
	"menu": "the application/context-menu key is not in P4wnP1's keymaps",
	"app":  "the application/context-menu key is not in P4wnP1's keymaps",
}

// laterDialect are DuckyScript 2.0/3.0 verbs. They parse as commands but have
// no 1:1 HIDScript form.
var laterDialect = map[string]string{
	"ALTCHAR":                 "DuckyScript 2.0 ALTCHAR (alt-code entry) has no direct HIDScript form",
	"ALTSTRING":               "DuckyScript 2.0 ALTSTRING has no direct HIDScript form",
	"ALTCODE":                 "DuckyScript 2.0 ALTCODE has no direct HIDScript form",
	"VAR":                     "DuckyScript 3.0 variables are not converted; HIDScript is JavaScript, so write them directly",
	"IF":                      "DuckyScript 3.0 conditionals are not converted; HIDScript is JavaScript, so write them directly",
	"END_IF":                  "DuckyScript 3.0 conditionals are not converted",
	"WHILE":                   "DuckyScript 3.0 loops are not converted; HIDScript is JavaScript, so write them directly",
	"END_WHILE":               "DuckyScript 3.0 loops are not converted",
	"FUNCTION":                "DuckyScript 3.0 functions are not converted; HIDScript is JavaScript, so write them directly",
	"END_FUNCTION":            "DuckyScript 3.0 functions are not converted",
	"RANDOM_LOWERCASE_LETTER": "DuckyScript 3.0 RANDOM_* verbs are not converted",
	"EXFIL":                   "DuckyScript 3.0 EXFIL has no HIDScript equivalent",
	"ATTACKMODE":              "ATTACKMODE is a Bash Bunny directive; set the USB composition in the P4wnP1 console instead",
	"LED":                     "LED is a Bash Bunny directive and has no effect here",
	"GET":                     "GET is a Bash Bunny directive and has no effect here",
}

// Convert translates DuckyScript source into HIDScript.
//
// It returns the generated script and any warnings. Warnings never stop the
// conversion -- the caller decides whether a payload that lost something is
// still worth running -- but an operator should always be shown them.
func Convert(src string, opts Options) (string, []Warning) {
	var out strings.Builder
	var warns []Warning

	out.WriteString("// Converted from DuckyScript by P4wnP1.\n")
	out.WriteString("// Review before running: the conversion is mechanical and cannot know\n")
	out.WriteString("// what the target machine's state or keyboard layout actually is.\n\n")

	if opts.Layout != "" {
		fmt.Fprintf(&out, "layout(%s);\n", jsString(opts.Layout))
	}
	if opts.TypingSpeedMillis > 0 || opts.TypingJitterMillis > 0 {
		fmt.Fprintf(&out, "typingSpeed(%d, %d);\n", opts.TypingSpeedMillis, opts.TypingJitterMillis)
	}
	if opts.Layout != "" || opts.TypingSpeedMillis > 0 || opts.TypingJitterMillis > 0 {
		out.WriteString("\n")
	}

	defaultDelay := 0
	// lastEmitted is what REPEAT repeats. DuckyScript's REPEAT re-runs the
	// previous *command*, not the previous line, so comments and blanks must
	// not clobber it.
	lastEmitted := ""

	lines := strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n")
	for i, raw := range lines {
		lineNo := i + 1
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}

		cmd, rest := splitCommand(line)
		upper := strings.ToUpper(cmd)

		switch upper {
		case "REM", "REM_BLOCK", "END_REM":
			fmt.Fprintf(&out, "// %s\n", rest)
			continue

		case "DELAY":
			n, err := strconv.Atoi(strings.TrimSpace(rest))
			if err != nil {
				warns = append(warns, Warning{lineNo, line, "DELAY needs a whole number of milliseconds"})
				continue
			}
			emit(&out, &lastEmitted, fmt.Sprintf("delay(%d);", n))
			continue

		case "DEFAULTDELAY", "DEFAULT_DELAY":
			n, err := strconv.Atoi(strings.TrimSpace(rest))
			if err != nil {
				warns = append(warns, Warning{lineNo, line, "DEFAULTDELAY needs a whole number of milliseconds"})
				continue
			}
			defaultDelay = n
			fmt.Fprintf(&out, "// default delay between commands: %dms\n", n)
			continue

		case "STRING":
			// Intentionally NOT TrimSpace'd: leading spaces in a STRING are
			// part of the payload, and trimming them silently corrupts
			// indentation-sensitive targets.
			emit(&out, &lastEmitted, fmt.Sprintf("type(%s);", jsString(rest)))
			emitDefaultDelay(&out, defaultDelay)
			continue

		case "STRINGLN":
			emit(&out, &lastEmitted, fmt.Sprintf("type(%s);", jsString(rest+"\n")))
			emitDefaultDelay(&out, defaultDelay)
			continue

		case "REPEAT":
			n, err := strconv.Atoi(strings.TrimSpace(rest))
			if err != nil || n < 1 {
				warns = append(warns, Warning{lineNo, line, "REPEAT needs a positive whole number"})
				continue
			}
			if lastEmitted == "" {
				warns = append(warns, Warning{lineNo, line, "REPEAT with no preceding command to repeat"})
				continue
			}
			for j := 0; j < n; j++ {
				fmt.Fprintf(&out, "%s\n", lastEmitted)
				emitDefaultDelay(&out, defaultDelay)
			}
			continue
		}

		if why, ok := laterDialect[upper]; ok {
			warns = append(warns, Warning{lineNo, line, why})
			fmt.Fprintf(&out, "// UNCONVERTED: %s\n", line)
			continue
		}

		// Anything else is a key or a key combo: "GUI r", "CTRL-ALT DEL", "F5".
		combo, bad := toCombo(line)
		if bad != "" {
			warns = append(warns, Warning{lineNo, line, bad})
			fmt.Fprintf(&out, "// UNCONVERTED: %s\n", line)
			continue
		}
		emit(&out, &lastEmitted, fmt.Sprintf("press(%s);", jsString(combo)))
		emitDefaultDelay(&out, defaultDelay)
	}

	return out.String(), warns
}

func emit(out *strings.Builder, last *string, stmt string) {
	fmt.Fprintf(out, "%s\n", stmt)
	*last = stmt
}

func emitDefaultDelay(out *strings.Builder, d int) {
	if d > 0 {
		fmt.Fprintf(out, "delay(%d);\n", d)
	}
}

// splitCommand splits a line into its first token and the remainder, keeping
// the remainder's interior spacing intact.
func splitCommand(line string) (cmd, rest string) {
	i := strings.IndexAny(line, " \t")
	if i < 0 {
		return line, ""
	}
	// Exactly one separator is consumed and the remainder is returned
	// UNTRIMMED. DuckyScript's convention is "STRING<space><payload>", and the
	// payload's leading whitespace is data: trimming it silently corrupts
	// anything indentation-sensitive on the target. Commands that want a bare
	// number (DELAY, REPEAT) trim it themselves.
	return line[:i], line[i+1:]
}

// toCombo turns a DuckyScript key line into a HIDScript combo string.
// DuckyScript writes modifiers joined by '-' or by spaces, and both forms turn
// up in real payloads: "CTRL-ALT DEL", "CTRL ALT DELETE", "GUI r".
func toCombo(line string) (combo string, problem string) {
	fields := strings.FieldsFunc(line, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '-' || r == '+'
	})
	if len(fields) == 0 {
		return "", "no keys on this line"
	}

	mapped := make([]string, 0, len(fields))
	for _, f := range fields {
		if f == "" {
			continue
		}
		low := strings.ToLower(f)
		if why, bad := unsupportedKeys[low]; bad {
			return "", why
		}
		if name, ok := keyAliases[low]; ok {
			mapped = append(mapped, name)
			continue
		}
		// F1..F24
		if len(f) >= 2 && (f[0] == 'F' || f[0] == 'f') {
			if n, err := strconv.Atoi(f[1:]); err == nil && n >= 1 && n <= 24 {
				mapped = append(mapped, "F"+strconv.Itoa(n))
				continue
			}
		}
		// A single printable character is a literal key; the keymaps carry
		// them with case significance ("a" and "A" are different entries).
		if len([]rune(f)) == 1 {
			mapped = append(mapped, f)
			continue
		}
		return "", fmt.Sprintf("unrecognised key or command %q", f)
	}
	if len(mapped) == 0 {
		return "", "no keys on this line"
	}
	return strings.Join(mapped, " "), ""
}

// jsString renders s as a JavaScript string literal.
//
// This is the part that has to be right: payload text is attacker-controlled
// relative to the converter, and a naive quote would let a crafted STRING
// close the literal and append arbitrary JavaScript to a script that then runs
// as root on the device. Everything outside printable ASCII is escaped
// numerically, and the characters that can terminate or continue a literal --
// quote, backslash, newline, and the two Unicode line terminators JavaScript
// treats as line breaks -- are escaped explicitly.
func jsString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		case '\u2028':
			// U+2028 LINE SEPARATOR and U+2029 PARAGRAPH SEPARATOR are
			// line terminators to a JavaScript parser, so a raw one ends the
			// string literal exactly like a newline would. Emit the escape
			// TEXT, not the character.
			b.WriteString("\\u2028")
		case '\u2029':
			b.WriteString("\\u2029")
		default:
			if r < 0x20 || r == 0x7f {
				fmt.Fprintf(&b, `\u%04x`, r)
			} else if r < 0x80 {
				b.WriteRune(r)
			} else {
				// Keep non-ASCII readable in the generated script; the HID
				// layer maps it through the active keymap and will report a
				// character it cannot type.
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

// SupportedKeys lists every DuckyScript key name the converter understands,
// sorted. Used by the CLI's --list-keys and by the console's help text.
func SupportedKeys() []string {
	out := make([]string, 0, len(keyAliases)+24)
	for k := range keyAliases {
		out = append(out, strings.ToUpper(k))
	}
	for i := 1; i <= 24; i++ {
		out = append(out, "F"+strconv.Itoa(i))
	}
	sort.Strings(out)
	return out
}
