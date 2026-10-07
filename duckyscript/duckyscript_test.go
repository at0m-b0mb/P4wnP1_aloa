package duckyscript

import (
	"strings"
	"testing"
)

func convert(t *testing.T, src string) (string, []Warning) {
	t.Helper()
	return Convert(src, Options{})
}

func mustContain(t *testing.T, got, want string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Errorf("output is missing %q\n--- got ---\n%s", want, got)
	}
}

func TestRemBecomesComment(t *testing.T) {
	out, w := convert(t, "REM this is a note")
	mustContain(t, out, "// this is a note")
	if len(w) != 0 {
		t.Errorf("unexpected warnings: %v", w)
	}
}

func TestDelay(t *testing.T) {
	out, w := convert(t, "DELAY 500")
	mustContain(t, out, "delay(500);")
	if len(w) != 0 {
		t.Errorf("unexpected warnings: %v", w)
	}
}

func TestDelayRejectsNonNumeric(t *testing.T) {
	_, w := convert(t, "DELAY soon")
	if len(w) != 1 {
		t.Fatalf("expected 1 warning, got %v", w)
	}
}

func TestString(t *testing.T) {
	out, _ := convert(t, `STRING powershell -w hidden`)
	mustContain(t, out, `type("powershell -w hidden");`)
}

// Leading whitespace inside STRING is payload, not formatting. Trimming it
// corrupts anything indentation-sensitive (Python, YAML, heredocs).
func TestStringPreservesLeadingSpace(t *testing.T) {
	out, _ := convert(t, "STRING     indented")
	mustContain(t, out, `type("    indented");`)
}

func TestStringLnAppendsNewline(t *testing.T) {
	out, _ := convert(t, "STRINGLN whoami")
	mustContain(t, out, `type("whoami\n");`)
}

// The security-critical test. A payload's STRING text is data; if it can close
// the generated JavaScript string literal it becomes code, and that code runs
// as root on the device. Every one of these must come back inert.
func TestStringCannotEscapeTheJSLiteral(t *testing.T) {
	cases := []struct {
		name, payload string
	}{
		{"double quote", `say "hi"`},
		{"quote then statement", `x"); require('fs'); ("`},
		{"backslash", `C:\Users\admin`},
		{"trailing backslash", `ends with \`},
		{"newline escape text", `a\nb`},
		{"line separator U+2028", "before\u2028after"},
		{"paragraph separator U+2029", "before\u2029after"},
		{"null byte", "a\x00b"},
		{"backtick", "`id`"},
		{"dollar brace", "${process}"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, _ := convert(t, "STRING "+tc.payload)
			// Find the generated type("...") line.
			var line string
			for _, l := range strings.Split(out, "\n") {
				if strings.HasPrefix(l, "type(") {
					line = l
					break
				}
			}
			if line == "" {
				t.Fatalf("no type() call generated for %q\n%s", tc.payload, out)
			}
			body := strings.TrimSuffix(strings.TrimPrefix(line, "type("), ");")

			if !strings.HasPrefix(body, `"`) || !strings.HasSuffix(body, `"`) {
				t.Fatalf("literal is not quote-delimited: %s", line)
			}
			inner := body[1 : len(body)-1]

			// Walk the literal: every quote and every backslash must be escaped.
			for i := 0; i < len(inner); i++ {
				if inner[i] == '\\' {
					if i+1 >= len(inner) {
						t.Fatalf("literal ends on a dangling backslash: %s", line)
					}
					i++ // skip the escaped character
					continue
				}
				if inner[i] == '"' {
					t.Fatalf("unescaped quote at %d would close the literal: %s", i, line)
				}
				if inner[i] == '\n' || inner[i] == '\r' {
					t.Fatalf("raw newline would terminate the literal: %q", line)
				}
			}
			// U+2028/U+2029 are line terminators to a JavaScript parser even
			// though they are not \n, so they must not survive raw.
			if strings.ContainsAny(inner, "\u2028\u2029") {
				t.Fatalf("raw JS line terminator survived in: %q", line)
			}
		})
	}
}

func TestSimpleKeyCombos(t *testing.T) {
	cases := map[string]string{
		"GUI r":           `press("GUI r");`,
		"CTRL-ALT DEL":    `press("CTRL ALT DELETE");`,
		"CTRL ALT DELETE": `press("CTRL ALT DELETE");`,
		"ENTER":           `press("ENTER");`,
		"F5":              `press("F5");`,
		"F12":             `press("F12");`,
		"WINDOWS r":       `press("GUI r");`,
		"ESCAPE":          `press("ESCAPE");`,
		"ESC":             `press("ESCAPE");`,
		"UPARROW":         `press("UP");`,
		"PRINTSCREEN":     `press("PRINTSCR");`,
		"CTRL+SHIFT+ESC":  `press("CTRL SHIFT ESCAPE");`,
	}
	for src, want := range cases {
		t.Run(src, func(t *testing.T) {
			out, w := convert(t, src)
			mustContain(t, out, want)
			if len(w) != 0 {
				t.Errorf("unexpected warnings for %q: %v", src, w)
			}
		})
	}
}

func TestUnknownKeyWarnsAndDoesNotGuess(t *testing.T) {
	out, w := convert(t, "FROBNICATE")
	if len(w) != 1 {
		t.Fatalf("expected 1 warning, got %v", w)
	}
	mustContain(t, out, "// UNCONVERTED: FROBNICATE")
	if strings.Contains(out, "press(") {
		t.Error("an unrecognised key must not become a press() call")
	}
}

// MENU/APP is a genuine DuckyScript key with no entry in P4wnP1's keymaps.
// Converting it to something else would type the wrong thing on a real target.
func TestUnsupportedKeyIsNamedPrecisely(t *testing.T) {
	_, w := convert(t, "MENU")
	if len(w) != 1 {
		t.Fatalf("expected 1 warning, got %v", w)
	}
	if !strings.Contains(w[0].Why, "context-menu") {
		t.Errorf("warning should explain the specific problem, got: %s", w[0].Why)
	}
}

func TestRepeatRepeatsThePreviousCommand(t *testing.T) {
	out, w := convert(t, "STRING a\nREPEAT 3")
	if len(w) != 0 {
		t.Fatalf("unexpected warnings: %v", w)
	}
	if n := strings.Count(out, `type("a");`); n != 4 {
		t.Errorf("want the command once plus 3 repeats = 4, got %d\n%s", n, out)
	}
}

// A comment between a command and REPEAT must not become the thing repeated.
func TestRepeatSkipsComments(t *testing.T) {
	out, _ := convert(t, "STRING a\nREM just a note\nREPEAT 2")
	if n := strings.Count(out, `type("a");`); n != 3 {
		t.Errorf("want 3 type() calls, got %d\n%s", n, out)
	}
}

func TestRepeatWithNothingToRepeatWarns(t *testing.T) {
	_, w := convert(t, "REPEAT 3")
	if len(w) != 1 {
		t.Fatalf("expected 1 warning, got %v", w)
	}
}

func TestDefaultDelayIsInsertedBetweenCommands(t *testing.T) {
	out, _ := convert(t, "DEFAULTDELAY 50\nSTRING a\nENTER")
	if n := strings.Count(out, "delay(50);"); n != 2 {
		t.Errorf("want a 50ms delay after each of the 2 commands, got %d\n%s", n, out)
	}
}

func TestLaterDialectIsReportedNotDropped(t *testing.T) {
	for _, verb := range []string{"ALTCHAR 65", "VAR $x = 1", "WHILE true", "EXFIL $x", "ATTACKMODE HID"} {
		t.Run(verb, func(t *testing.T) {
			out, w := convert(t, verb)
			if len(w) != 1 {
				t.Fatalf("expected a warning for %q, got %v", verb, w)
			}
			// It must also be visible in the output, so an operator reading the
			// generated script sees the gap.
			mustContain(t, out, "// UNCONVERTED:")
		})
	}
}

func TestOptionsEmitLayoutAndSpeed(t *testing.T) {
	out, _ := Convert("ENTER", Options{Layout: "DE_ASCII", TypingSpeedMillis: 80, TypingJitterMillis: 20})
	mustContain(t, out, `layout("DE_ASCII");`)
	mustContain(t, out, "typingSpeed(80, 20);")
}

func TestNoOptionsEmitsNoLayoutCall(t *testing.T) {
	out, _ := Convert("ENTER", Options{})
	if strings.Contains(out, "layout(") {
		t.Errorf("layout() should not be emitted when no layout is chosen:\n%s", out)
	}
}

func TestBlankLinesAndCRLF(t *testing.T) {
	out, w := convert(t, "STRING a\r\n\r\nENTER\r\n")
	if len(w) != 0 {
		t.Fatalf("unexpected warnings: %v", w)
	}
	mustContain(t, out, `type("a");`)
	mustContain(t, out, `press("ENTER");`)
	if strings.Contains(out, "\r") {
		t.Error("carriage returns leaked into the generated script")
	}
}

// A realistic end-to-end payload: the shape most published DuckyScript takes.
func TestRealisticPayload(t *testing.T) {
	src := `REM Open a terminal and report the hostname
DELAY 1000
GUI r
DELAY 500
STRING cmd
ENTER
DELAY 750
STRINGLN hostname
`
	out, w := Convert(src, Options{Layout: "US", TypingSpeedMillis: 60, TypingJitterMillis: 15})
	if len(w) != 0 {
		t.Fatalf("a plain DuckyScript 1.0 payload should convert cleanly, got: %v", w)
	}
	for _, want := range []string{
		`layout("US");`,
		"typingSpeed(60, 15);",
		"// Open a terminal and report the hostname",
		"delay(1000);",
		`press("GUI r");`,
		`type("cmd");`,
		`press("ENTER");`,
		`type("hostname\n");`,
	} {
		mustContain(t, out, want)
	}
}

func TestSupportedKeysIsSortedAndNonEmpty(t *testing.T) {
	k := SupportedKeys()
	if len(k) < 30 {
		t.Fatalf("expected a substantial key list, got %d", len(k))
	}
	for i := 1; i < len(k); i++ {
		if k[i-1] > k[i] {
			t.Fatalf("SupportedKeys is not sorted at %d: %q > %q", i, k[i-1], k[i])
		}
	}
}

func TestWarningStringMentionsLine(t *testing.T) {
	_, w := convert(t, "STRING ok\nFROBNICATE")
	if len(w) != 1 {
		t.Fatalf("expected 1 warning, got %v", w)
	}
	if w[0].Line != 2 {
		t.Errorf("warning line = %d, want 2", w[0].Line)
	}
	if !strings.Contains(w[0].String(), "line 2") {
		t.Errorf("Warning.String() should name the line: %s", w[0])
	}
}
