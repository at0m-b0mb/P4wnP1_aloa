# HIDScripts — the payload library

Scripts here are typed by the device into whatever host it is plugged into. They
run in **otto**, an ES5 JavaScript interpreter (`go.mod:9`), inside the service.

## What has actually been run

| Script | Status |
|---|---|
| `mac-01-hello.js` | **Verified** on a real Mac — Spotlight, TextEdit, four lines typed clean |
| `mac-03-lock.js` | **Verified** on a real Mac — the screen locked |
| `platform-detect.js` | **Verified** on a real Mac — correctly answers `NOT_OBVIOUSLY_WINDOWS_OR_LINUX` |
| everything else | **Never run against a host.** Parses, and nothing more |

Each script's own `VERIFIED:` line is the authority; this table is a summary.
"Parses" means it compiles under the real otto with every `press()` token and
`layout()` name checked — it says nothing about whether the target behaves as
the script assumes. That line is load-bearing: this project's rule is
that you never claim a capability you have not demonstrated, and an honest
"untested" is worth more than a confident guess.

## Run these two first

| | |
|---|---|
| `platform-detect.js` | Guesses the host OS from keyboard LED behaviour. Types nothing visible, so it is safe to run blind. It returns a **hint**, not a fact — read its header on why. |
| `keymap-check.js` | Types the full printable set so you can see whether your `layout()` matches the target. The most useful script in the directory. |

The keymap one matters more than it sounds. A keyboard does not send characters,
it sends key *positions*, and the host decides what each position means. Type
`ps -y` with the wrong layout against a German machine and it arrives as `ps -z`.
Punctuation moves far more than letters do, which is why every shell payload here
is a layout mismatch away from failing in a way that looks like a code bug.

## The platform sets

Each set has a *hello* (prove the device is a keyboard), and where the platform
allows it, a *recon* (run the host's own read-only inventory commands) and a
*lock* (prove reach, touch nothing).

| Platform | Scripts | What you get |
|---|---|---|
| Windows | `win-01-hello`, `win-02-recon`, `win-03-lock` | Everything. `GUI+R` is a real launcher, `cmd` is a real shell. |
| macOS | `mac-01-hello`, `mac-02-recon`, `mac-03-lock` | Everything, once the Keyboard Setup Assistant is out of the way — see below. |
| Linux | `linux-01-hello`, `linux-02-recon`, `linux-03-lock` | Everything, *if* you know the desktop environment. There is no universal launcher. |
| Android | `android-01-hello`, `android-02-focus-walk` | Text entry and focus navigation. No shell. No system keys. |
| iOS / iPadOS | `ios-01-hello`, `ios-02-spotlight` | Text entry into a field you already opened. Spotlight on iPadOS only. |

**Start with the `lock` script on a new desktop target.** It guesses at nothing
and fails visibly in one second, where a *hello* that misses its launcher types
into whatever had focus — on someone else's machine, the one outcome you cannot
take back.

## Three ceilings that are not negotiable

These are read out of the source, not assumed, and no script can work around them.

**There are no media keys. None.** The shipped USB HID report descriptor at
`service/SubSysUSB.go:62` declares usage page `0x07` (Keyboard/Keypad), usages
`0x00`–`0x65`, and nothing else. There is no Consumer Control page (`0x0C`). So
volume, play/pause, brightness, the **Android HOME and BACK buttons**, and the
iOS home and lock keys are physically unsendable by this device. Most published
Android BadUSB material depends on exactly these. A script that claims to press
HOME on Android is not clever — it is wrong.

**Android and iOS give you no command execution.** No shell, no run dialog, no
terminal. ADB is a different USB function entirely and needs a host-side
authorisation the user taps on screen. On these platforms this device is a
text-entry and focus-navigation tool, which is a real capability worth
demonstrating honestly, and is not a shell.

**macOS first-attach can eat your payload.** The first time macOS sees an
unfamiliar USB keyboard it may open the Keyboard Setup Assistant, which is modal,
takes focus, and swallows everything typed. It cannot be defeated from here — it
probes specific physical keys. It also re-triggers if the gadget's USB vendor or
product ID changes. Treat "nothing happened on a Mac" as this until ruled out.

## Scripts cannot see the screen

Every `recon` script leaves its output on the **target's** screen and stops there.
This device is a keyboard: there is no return channel, so it cannot read back
what it produced. Your engagement note for those payloads is a photograph.

`platform-detect.js` is the one exception, and it only gets LED reports — three
bits of feedback, which is why its answer is a hint.

## What this library deliberately does not contain

Nothing here harvests credentials, installs persistence, tampers with or evades
security controls, sends anything to a third party, escalates privilege, or
modifies the user's files. Every script states what it `TOUCHES:` and the honest
answer is almost always "nothing — it types and you read the screen".

That is a design decision, not an oversight. These are payloads an operator can
run in front of a client and explain line by line. If a reader could not tell one
from malware, it would be the wrong payload for this box.

`helper.js` and `wifi_covert_channel.js` are upstream MaMe82 code and are a
different thing — they contain a base64-encoded in-memory PowerShell stager. They
are left as they came. The scripts documented above neither extend nor imitate
them.

## Adding a script

Two constraints will silently waste your time if you miss them:

1. **Flat files only.** `ListFilesOfFolder` in `service/common.go` does
   `if !fitem.IsDir()` and never recurses, so a script in a subdirectory is
   invisible to `ListStoredHIDScripts` and to the web console. Use a platform
   prefix in the filename instead.

2. **ES5 only, and a small API.** otto rejects `let`, `const`, `=>`, backtick
   strings, `for...of`, `class`, destructuring, spread, `Object.assign`,
   `String.includes`/`startsWith`/`endsWith`, `Array.includes`, `Promise`, `Map`
   and `Set`. The entire API is `type`, `press`, `delay`, `layout`,
   `typingSpeed`, `waitLED`, `waitLEDRepeat`, `move`, `moveTo`, `moveStepped`,
   `click`, `doubleClick`, `button`, plus the constants `NUM CAPS SCROLL ANY
   ANY_OR_NONE BT1 BT2 BT3 BTNONE`. There is no `console.log`, no `require`, no
   file IO, no `setTimeout`. To report something to the operator, **`return` a
   value** — do not just leave a trailing expression.

   That distinction is not cosmetic and it cost me every script in this
   directory. `hid/AsyncOtto.go:196` wraps your source in
   `(function() { ... })()` before running it, so a bare trailing expression is
   inside a function body and its value is discarded: the job succeeds, the
   console shows `null`, and nothing says why. Verified on hardware —
   `var a = 2+3; "x:" + a;` returns `null`, and `return "x:" + a;` returns
   `"x:5"`. `tools/hidscript-check` now fails a script that ends without a
   `return`.

Valid `press()` key names come from the `Mapping` tables in `dist/keymaps/*.json`
(316 across all of them, 175 in `common.json`). Anything not in there is
invalid, and a wrong key name is the subtlest way to ship a dead payload — the
script parses, loads, runs, and does nothing.

Valid `layout()` values are **the `Name` fields inside those files, not the
filenames**:

```
US DE CH RU BE BR CA CM ES FI FR GB IT SV      (case-insensitive)
```

The German map is the file `DE_ASCII.json` but declares `Name: "DE"`, so
`layout('de')` works and `layout('de_ascii')` does not. `hid/keyboard.go:128`
keys the table on `strings.ToUpper(kbdmap.Name)`. I got this wrong first time
and the gate below caught it.

## The gate

```bash
go run ./tools/hidscript-check              # check every script
go run ./tools/hidscript-check -self-test   # prove the gate can fail
```

`make verify` runs both. It compiles each script with the real otto, so it
catches **every** ES6 construct rather than the ones someone remembered to grep
for, and it validates every `press()` token and `layout()` name against the
keymaps, flags calls to functions that do not exist, and fails any script in a
subdirectory.

The `-self-test` run feeds it one deliberately broken script per rule and fails
unless all of them are caught — plus positive cases, because the first version
of this gate **rejected two correct upstream scripts** by validating layouts
against filenames. A gate with false positives gets switched off, which is a
slower way of having no gate. This repository has shipped four checks that could
not fail; the self-test is why this one is not the fifth.

Use the header block the other scripts use — `TARGET`, `NEEDS`, `TOUCHES`,
`LIMITATION`, `VERIFIED` — and keep `VERIFIED` honest.
