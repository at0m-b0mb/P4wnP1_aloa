// hidscript-check -- prove every shipped HIDScript can actually run.
//
//	go run ./tools/hidscript-check            # checks dist/HIDScripts
//	go run ./tools/hidscript-check -dir X     # checks X
//	go run ./tools/hidscript-check -self-test # proves the gate can fail
//
// # WHY THIS EXISTS
//
// Sixteen payload files were added to dist/HIDScripts in one go without a
// single one being parsed, because the tooling to parse them did not exist.
// `make verify` covered the service, the console, the CLI and the palette, and
// had nothing at all to say about the scripts this device's entire purpose is
// to type.
//
// # WHAT IT CHECKS, AND WHY EACH ONE IS A REAL FAILURE MODE
//
//  1. PARSE. Compiled with the real otto, the same ES5 interpreter the service
//     uses (go.mod). This is the rigorous check: it catches every piece of ES6
//     -- let, const, arrow functions, template literals, for...of, class,
//     destructuring, spread -- without anyone having to think of them in
//     advance. A grep for banned keywords only ever catches the ones you
//     remembered.
//
//  2. press() KEY NAMES. The subtlest failure in this class of tool: a script
//     with a bad key name parses, loads, runs, and silently does nothing. Every
//     string literal handed to press() is split on whitespace and each token
//     checked against the union of every dist/keymaps/*.json Mapping.
//
//  3. layout() NAMES. A layout that does not exist is the other silent one --
//     and a wrong layout turns correct payloads into garbage on the target.
//
//  4. UNKNOWN CALLS. A call to a function that is not in the API, not a JS
//     builtin, and not defined in the file itself. This is how a payload
//     written from upstream documentation, or from a plausible guess, fails.
//
//  5. SUBDIRECTORIES. service/common.go ListFilesOfFolder does
//     `if !fitem.IsDir()` and never recurses, so a script in a subdirectory is
//     invisible to ListStoredHIDScripts and to the web console. It is not
//     broken, which is worse: it is absent, and nothing says so.
//
// # THE API SURFACE IS READ FROM THE SOURCE, NOT LISTED HERE
//
// apiNames is derived by scanning hid/*.go for vm.Set("name"), so this gate
// cannot drift away from the real interpreter. A hardcoded list would go stale
// the first time someone adds a function, and would then reject valid scripts.
//
// # -self-test
//
// This repository has shipped four gates that could not fail. Negative-testing
// is not optional here, so the proof is built in: -self-test feeds the checker
// one deliberately broken script per rule and fails unless every single one is
// caught. Run it whenever you touch this file.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"encoding/json"

	"github.com/robertkrimen/otto"
)

type problem struct {
	file string
	rule string
	msg  string
}

// jsBuiltins are the globals otto provides. Calls to these are never flagged.
var jsBuiltins = map[string]bool{
	"Array": true, "Boolean": true, "Date": true, "Error": true, "EvalError": true,
	"Function": true, "JSON": true, "Math": true, "Number": true, "Object": true,
	"RangeError": true, "ReferenceError": true, "RegExp": true, "String": true,
	"SyntaxError": true, "TypeError": true, "URIError": true,
	"decodeURI": true, "decodeURIComponent": true, "encodeURI": true,
	"encodeURIComponent": true, "escape": true, "eval": true, "isFinite": true,
	"isNaN": true, "parseFloat": true, "parseInt": true, "unescape": true,
	// Control-flow keywords that the call regex would otherwise see as calls.
	"if": true, "for": true, "while": true, "switch": true, "catch": true,
	"return": true, "typeof": true, "function": true, "delete": true, "in": true,
	"new": true, "do": true, "else": true, "throw": true, "var": true,
}

var (
	// press("GUI r") / press('GUI r')
	rePress = regexp.MustCompile(`press\s*\(\s*["']([^"']*)["']\s*\)`)
	// layout('us')
	reLayout = regexp.MustCompile(`layout\s*\(\s*["']([^"']*)["']\s*\)`)
	// bareword( -- a call. Rejects member calls (foo.bar()) via the lookbehind
	// substitute: we require the char before the name not to be '.'.
	reCall = regexp.MustCompile(`(^|[^.\w$])([A-Za-z_$][\w$]*)\s*\(`)
	// function foo( -- a locally defined function.
	reFuncDecl = regexp.MustCompile(`function\s+([A-Za-z_$][\w$]*)\s*\(`)
	// var foo = function  /  foo = function
	reFuncExpr = regexp.MustCompile(`([A-Za-z_$][\w$]*)\s*=\s*function\s*\(`)
	// vm.Set("name") in the Go source -- the real API surface.
	reVMSet = regexp.MustCompile(`vm\.Set\("([A-Za-z_0-9]+)"`)
	// Strip comments and strings before the call scan, so prose does not
	// register as code. This repo's scripts are mostly comment by volume, and
	// an earlier gate in this tree reported its own documentation as a defect
	// three times running.
	reBlockComment = regexp.MustCompile(`(?s)/\*.*?\*/`)
	reLineComment  = regexp.MustCompile(`//[^\n]*`)
	reDblString    = regexp.MustCompile(`"(\\.|[^"\\])*"`)
	reSglString    = regexp.MustCompile(`'(\\.|[^'\\])*'`)
)

// apiNames scans hid/*.go for vm.Set("x") so the gate tracks the interpreter.
func apiNames(repoRoot string) (map[string]bool, error) {
	paths, err := filepath.Glob(filepath.Join(repoRoot, "hid", "*.go"))
	if err != nil {
		return nil, err
	}
	names := map[string]bool{}
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		for _, m := range reVMSet.FindAllStringSubmatch(string(b), -1) {
			names[m[1]] = true
		}
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("found no vm.Set() names under %s/hid -- "+
			"this gate cannot validate anything, so it is failing rather than passing vacuously",
			repoRoot)
	}
	return names, nil
}

// keyNames is the union of every keymap's Mapping. The union, not just
// common.json, because a language keymap may legitimately add keys.
func keyNames(repoRoot string) (map[string]bool, error) {
	paths, err := filepath.Glob(filepath.Join(repoRoot, "dist", "keymaps", "*.json"))
	if err != nil {
		return nil, err
	}
	keys := map[string]bool{}
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		var km struct {
			Mapping map[string]interface{} `json:"Mapping"`
		}
		if err := json.Unmarshal(b, &km); err != nil {
			return nil, fmt.Errorf("%s: %v", filepath.Base(p), err)
		}
		for k := range km.Mapping {
			keys[k] = true
		}
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("found no key names under %s/dist/keymaps -- refusing to pass vacuously", repoRoot)
	}
	return keys, nil
}

// layoutNames returns the names layout() will actually accept.
//
// THESE ARE THE "Name" FIELDS INSIDE THE JSON, NOT THE FILENAMES. The first
// version of this function used filenames and was wrong, which it proved by
// failing two correct upstream scripts: DE_ASCII.json declares Name "DE", so
// layout('de') resolves and layout('de_ascii') does not. The filename is not
// the identifier.
//
// hid/keyboard.go:128 keys the map with strings.ToUpper(kbdmap.Name), and
// hid/keyboard.go:196 looks up strings.ToUpper(name), so matching is
// case-insensitive on the Name field and nothing else.
func layoutNames(repoRoot string) (map[string]bool, error) {
	paths, err := filepath.Glob(filepath.Join(repoRoot, "dist", "keymaps", "*.json"))
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		var km struct {
			Name string `json:"Name"`
		}
		if err := json.Unmarshal(b, &km); err != nil {
			return nil, fmt.Errorf("%s: %v", filepath.Base(p), err)
		}
		if n := strings.ToUpper(strings.TrimSpace(km.Name)); n != "" && n != "COMMON" {
			// COMMON is merged into every other map (keyboard.go:120) and is
			// never selectable by name.
			out[n] = true
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("found no selectable layout names under %s/dist/keymaps -- refusing to pass vacuously", repoRoot)
	}
	return out, nil
}

func blankKeepingLines(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' {
			return '\n' // keep line numbering usable
		}
		return ' '
	}, s)
}

// stripComments blanks comments and KEEPS string literals.
//
// This is what the press() and layout() scans run over, and the distinction is
// the whole point: those checks examine string LITERALS, so they cannot use
// stripNonCode -- it would blank the very thing being inspected -- but they
// must not read COMMENTS either.
//
// That is not hypothetical. The first version scanned raw source and promptly
// failed keymap-check.js, whose header comment says
//
//	so layout('de') works and layout('de_ascii') does not
//
// The gate read its own documentation as code and reported the example of the
// wrong usage as an instance of the wrong usage. This repository has now done
// that four times -- an image checker reported Views.radio and WIFI_MODE three
// times running, each a comment explaining their removal. Any scanner here
// reads code lines only.
func stripComments(src string) string {
	src = reBlockComment.ReplaceAllStringFunc(src, blankKeepingLines)
	src = reLineComment.ReplaceAllStringFunc(src, blankKeepingLines)
	return src
}

// stripNonCode blanks comments AND string literals, so the call scan sees only
// code. Prose mentioning foo() must not register as a call to foo().
func stripNonCode(src string) string {
	src = stripComments(src)
	src = reDblString.ReplaceAllStringFunc(src, blankKeepingLines)
	src = reSglString.ReplaceAllStringFunc(src, blankKeepingLines)
	return src
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func checkScript(path, src string, api, keys, layouts map[string]bool) []problem {
	var out []problem
	name := filepath.Base(path)
	add := func(rule, msg string) { out = append(out, problem{name, rule, msg}) }

	// 1. PARSE with the real interpreter, WRAPPED THE WAY THE SERVICE WRAPS IT.
	//
	// hid/AsyncOtto.go:196 runs `(function() { <source> })();`, not the bare
	// source. Compiling the bare source is a different language in one
	// important respect: a top-level `return` is an "Illegal return statement"
	// standalone and perfectly legal inside the wrapper. Since `return` is how
	// a script reports its result (see rule 3b), checking the unwrapped form
	// would reject every correct script in the directory.
	//
	// Test what actually runs.
	if _, err := otto.New().Compile(path, "(function() {\n"+src+"\n})();"); err != nil {
		add("parse", strings.TrimSpace(err.Error()))
		// A file that will not parse makes every other check noise.
		return out
	}

	// 2 and 3 run over COMMENT-STRIPPED source with string literals intact.
	// The literals are what is being checked, so stripNonCode is wrong here --
	// but raw source is wrong too, because a comment that documents a bad key
	// name or a bad layout would be reported as one. See stripComments.
	noComments := stripComments(src)

	// 2. press() key names.
	for _, m := range rePress.FindAllStringSubmatch(noComments, -1) {
		combo := m[1]
		if strings.TrimSpace(combo) == "" {
			add("press", "press(\"\") with an empty combo")
			continue
		}
		for _, tok := range strings.Fields(combo) {
			if !keys[tok] {
				add("press", fmt.Sprintf("press(%q): %q is not in any keymap", combo, tok))
			}
		}
	}

	// 3. layout() names.
	for _, m := range reLayout.FindAllStringSubmatch(noComments, -1) {
		if !layouts[strings.ToUpper(m[1])] {
			var have []string
			for k := range layouts {
				have = append(have, k)
			}
			sort.Strings(have)
			add("layout", fmt.Sprintf("layout(%q) is not a loaded keymap Name; have: %s",
				m[1], strings.Join(have, " ")))
		}
	}

	// 3b. A dangling result.
	//
	// hid/AsyncOtto.go:196 wraps the script in (function(){ ... })() before
	// running it, so a bare trailing expression sits inside a function body and
	// its value is THROWN AWAY. The job succeeds, the console prints null, and
	// nothing anywhere says why.
	//
	// Every script in dist/HIDScripts shipped with this bug, because the house
	// style said "end with a string expression" and that was simply wrong.
	// Verified on a real device: `var a=2+3; "x:"+a;` -> null, and
	// `return "x:"+a;` -> "x:5".
	//
	// Only a trailing STRING literal is flagged. A script that ends in a call,
	// a closing brace or an infinite loop has not promised a result -- several
	// upstream scripts legitimately return nothing.
	{
		lines := strings.Split(noComments, "\n")
		for i := len(lines) - 1; i >= 0; i-- {
			t := strings.TrimSpace(lines[i])
			if t == "" {
				continue
			}
			// Walk back over continuation lines of a concatenated string.
			start := i
			for start > 0 {
				prev := strings.TrimSpace(lines[start-1])
				if strings.HasSuffix(prev, "+") {
					start--
					continue
				}
				break
			}
			first := strings.TrimSpace(lines[start])
			if strings.HasPrefix(first, "\"") || strings.HasPrefix(first, "'") {
				add("noresult", fmt.Sprintf(
					"line %d: the script ends with a bare string expression, which otto "+
						"DISCARDS -- the job succeeds and the console shows null. Use "+
						"`return %s`", start+1, truncate(first, 40)))
			}
			break
		}
	}

	// 4. Unknown calls, over comment- and string-stripped source.
	code := stripNonCode(src)
	local := map[string]bool{}
	for _, m := range reFuncDecl.FindAllStringSubmatch(code, -1) {
		local[m[1]] = true
	}
	for _, m := range reFuncExpr.FindAllStringSubmatch(code, -1) {
		local[m[1]] = true
	}
	seen := map[string]bool{}
	for _, m := range reCall.FindAllStringSubmatch(code, -1) {
		fn := m[2]
		if api[fn] || local[fn] || jsBuiltins[fn] || seen[fn] {
			continue
		}
		seen[fn] = true
		add("call", fmt.Sprintf("%s() is not in the HIDScript API, not a JS builtin, and not defined in this file", fn))
	}
	return out
}

func run(dir, repoRoot string) (int, []problem, error) {
	api, err := apiNames(repoRoot)
	if err != nil {
		return 0, nil, err
	}
	keys, err := keyNames(repoRoot)
	if err != nil {
		return 0, nil, err
	}
	layouts, err := layoutNames(repoRoot)
	if err != nil {
		return 0, nil, err
	}

	var probs []problem
	checked := 0

	// 5. Subdirectories -- walk, so a nested script is found and reported.
	err = filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(strings.ToLower(p), ".js") {
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		if strings.Contains(rel, string(filepath.Separator)) {
			probs = append(probs, problem{rel, "subdir",
				"in a subdirectory; ListFilesOfFolder never recurses, so this script is " +
					"INVISIBLE to the console and to ListStoredHIDScripts. Flatten it with a prefix."})
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		checked++
		probs = append(probs, checkScript(p, string(b), api, keys, layouts)...)
		return nil
	})
	return checked, probs, err
}

// selfTest proves each rule can fail. See the package comment.
func selfTest(repoRoot string) int {
	api, err := apiNames(repoRoot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "self-test setup failed: %v\n", err)
		return 1
	}
	keys, _ := keyNames(repoRoot)
	layouts, _ := layoutNames(repoRoot)

	cases := []struct {
		name string
		rule string
		src  string
	}{
		{"es6 let", "parse", "let x = 1; type('hi');"},
		{"es6 const", "parse", "const x = 1; type('hi');"},
		{"es6 arrow", "parse", "var f = () => 1; f();"},
		{"es6 template literal", "parse", "var s = `hi`; type(s);"},
		{"es6 for-of", "parse", "for (var x of [1,2]) { delay(x); }"},
		{"es6 class", "parse", "class A {}; type('hi');"},
		{"bad key name", "press", `press("GUI VOLUME_UP");`},
		{"android home key", "press", `press("HOMEPAGE");`},
		{"empty combo", "press", `press("");`},
		{"bad layout", "layout", `layout('klingon');`},
		{"unknown function", "call", `sleep(100);`},
		{"console.log is not a thing", "call", `consoleLog('x');`},
		{"plausible but absent API", "call", `typeString("hi");`},
	}

	bad := 0
	for _, c := range cases {
		probs := checkScript("selftest.js", c.src, api, keys, layouts)
		hit := false
		for _, p := range probs {
			if p.rule == c.rule {
				hit = true
				break
			}
		}
		if hit {
			fmt.Printf("  \033[32mok\033[0m    caught: %s (%s)\n", c.name, c.rule)
		} else {
			fmt.Printf("  \033[31mMISS\033[0m  NOT caught: %s -- expected rule %q, got %v\n", c.name, c.rule, probs)
			bad++
		}
	}

	// POSITIVE CASES. These exist because the first version of this gate FAILED
	// two correct upstream scripts: it validated layout() against filenames,
	// but DE_ASCII.json declares Name "DE", so layout('de') is right and
	// layout('de_ascii') is wrong. A false positive gets a gate switched off,
	// which is just a slower way of having no gate -- so every accepted form
	// is pinned here, in both cases, alongside the rejections above.
	for _, good := range []string{
		`layout('de');`, `layout('DE');`, `layout('us');`, `layout('US');`,
		`layout('gb');`, `layout('DE_ASCII');`, // the last is the FILENAME: must be rejected
	} {
		probs := checkScript("pos.js", good, api, keys, layouts)
		wantReject := strings.Contains(strings.ToUpper(good), "DE_ASCII")
		gotReject := len(probs) > 0
		if wantReject != gotReject {
			verb := "accepted"
			if wantReject {
				verb = "rejected"
			}
			fmt.Printf("  \033[31mMISS\033[0m  %s should be %s; got %v\n", good, verb, probs)
			bad++
		} else {
			fmt.Printf("  \033[32mok\033[0m    %-22s correctly %s\n", good,
				map[bool]string{true: "rejected (filename, not Name)", false: "accepted"}[wantReject])
		}
	}

	// DOCUMENTATION IS NOT CODE. The gate's first version failed
	// keymap-check.js because its header comment names layout('de_ascii') as
	// the spelling that does NOT work. Four separate checkers in this
	// repository have now reported their own prose as a defect.
	for _, c := range []struct {
		name string
		src  string
		bad  bool
	}{
		{"bad layout in a line comment", "// layout('de_ascii') does not work\nlayout('us');", false},
		{"bad layout in a block comment", "/*\n * layout('klingon') is not real\n */\nlayout('us');", false},
		{"bad key in a comment", "// press(\"VOLUME_UP\") cannot be sent\npress(\"GUI r\");", false},
		{"unknown call in a comment", "// sleep(100) does not exist; use delay\ndelay(100);", false},
		{"bad layout in REAL code still caught", "layout('klingon');", true},
		{"dangling string result is caught", "delay(1);\n\"done\";", true},
		{"dangling concatenated result is caught", "delay(1);\n\"a\" +\n\"b\";", true},
		{"an explicit return is fine", "delay(1);\nreturn \"done\";", false},
		{"ending in a call is fine", "delay(1);\npress(\"ENTER\");", false},
		{"ending in a brace is fine", "while (true) {\n delay(1);\n}", false},
		{"bad key in REAL code still caught", "press(\"VOLUME_UP\");", true},
	} {
		probs := checkScript("doc.js", c.src, api, keys, layouts)
		if (len(probs) > 0) != c.bad {
			state := "flagged"
			if !c.bad {
				state = "NOT flagged"
			}
			fmt.Printf("  \033[31mMISS\033[0m  %s: expected %s, got %v\n", c.name, state, probs)
			bad++
		} else {
			fmt.Printf("  \033[32mok\033[0m    %s\n", c.name)
		}
	}

	// A clean script must pass, or the gate is useless in the other direction:
	// a check that fails everything is as bad as one that fails nothing.
	clean := `layout('us');
typingSpeed(20, 30);
function helper() { return 1; }
press("GUI r");
delay(helper());
type("notepad\n");
waitLED(ANY_OR_NONE, 5000);
move(1, 1);
click(BT1);
return "done";`
	if probs := checkScript("clean.js", clean, api, keys, layouts); len(probs) != 0 {
		fmt.Printf("  \033[31mMISS\033[0m  a VALID script was rejected: %v\n", probs)
		bad++
	} else {
		fmt.Printf("  \033[32mok\033[0m    a valid script passes\n")
	}

	fmt.Println()
	if bad != 0 {
		fmt.Printf("  ---- self-test FAILED: %d of %d rules do not work ----\n", bad, len(cases)+1)
		return 1
	}
	fmt.Printf("  ---- self-test passed: all %d rules demonstrably fail on bad input ----\n", len(cases)+1)
	return 0
}

func main() {
	dir := flag.String("dir", "dist/HIDScripts", "directory of HIDScripts to check")
	root := flag.String("root", ".", "repository root (for hid/*.go and dist/keymaps)")
	self := flag.Bool("self-test", false, "prove every rule can fail, then exit")
	flag.Parse()

	if *self {
		os.Exit(selfTest(*root))
	}

	checked, probs, err := run(*dir, *root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  error: %v\n", err)
		os.Exit(2)
	}

	sort.Slice(probs, func(i, j int) bool {
		if probs[i].file != probs[j].file {
			return probs[i].file < probs[j].file
		}
		return probs[i].rule < probs[j].rule
	})
	for _, p := range probs {
		fmt.Printf("  \033[31mFAIL\033[0m  %s [%s] %s\n", p.file, p.rule, p.msg)
	}

	fmt.Println()
	if len(probs) == 0 {
		fmt.Printf("  ---- %d HIDScripts: all parse under otto, every key name and layout is real ----\n", checked)
		return
	}
	fmt.Printf("  ---- %d HIDScripts checked, %d problems ----\n", checked, len(probs))
	os.Exit(1)
}
