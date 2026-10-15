# Payload test log

Twelve of the fifteen payloads have never run against a host, because the hosts
are Windows PCs, Linux boxes, Android phones and iPhones and the keystrokes
arrive over physical USB — a VM will not stand in for one.

This is where results get recorded as they happen. Fill in a row, and the
script's own `VERIFIED:` line gets updated to match.

## How to run one with nothing but the device

No second laptop. The OLED panel does it all.

1. **Turn the keyboard on.** Panel → **Cable (USB)** → joystick to **Keyboard**
   → press in to tick → **KEY1** to deploy. HID is off by default on purpose: a
   device that offers a keyboard to every host it meets, before anyone asked, is
   a worse device.
   - Endpoint budget is 7. Keyboard costs 1. If the panel says `OVER n/7`, turn
     off **RNDIS net** — macOS and Linux do not use it.
2. **Plug the device into the target host.** Give it a few seconds to enumerate.
3. **Run it.** Panel → **Payloads** → joystick to the script → **KEY1**.
   - If the hint line says `no keyboard: see Cable`, step 1 did not take.
4. **Watch the target's screen.** That is the result. The device is a keyboard;
   it cannot see what happened.

## What to write down

A result is only useful if it says what was observed, not whether it "worked".
For each run:

- **Host** — OS and version (`Windows 11 23H2`, `Android 14 on a Pixel 7`)
- **Did the launcher open?** The step most likely to fail, and the one the
  script cannot check
- **Did the text arrive, and was it correct?** Garbled punctuation means the
  keymap is wrong, not the script
- **Anything unexpected** — a dialog, focus stolen, a wrong app opening
- **Where stray text went**, if the launcher missed

## The known trap, found on a Mac

`mac-01-hello` worked and `mac-02-recon` did not, on the same machine, minutes
apart. The cause was not macOS: **Raycast owned CMD+SPACE, not Spotlight.**
Raycast matched "TextEdit" and launched it; its top hit for "Terminal" was
something else, and the script pressed ENTER on it blind.

So every `hello` and `recon` script carries a dependency none of their headers
mention: **which launcher answers the shortcut.** Alfred, LaunchBar and Raycast
all intercept it, and all rank results differently.

`ios-02-spotlight.js` deliberately does **not** press ENTER, for exactly this
reason, written before the Mac proved it. The lesson was there and had not been
applied to its siblings.

When testing a `hello` or `recon` script, note what the launcher actually
selected. That is the interesting part of the result.

## Log

| Script | Host | Result | Observed by | Date |
|---|---|---|---|---|
| `platform-detect.js` | macOS 26, Apple silicon | **Pass** — returned `NOT_OBVIOUSLY_WINDOWS_OR_LINUX`, correct for a Mac (macOS does not drive keyboard LEDs) | Claude, directly | 2026-10-14 |
| `mac-01-hello.js` | macOS 26, Apple silicon | **Pass** — launcher opened, TextEdit launched, all four lines typed with no dropped or garbled characters on `us` | Claude + operator (screenshot) | 2026-10-14 |
| `mac-03-lock.js` | macOS 26, Apple silicon | **Pass** — screen locked. Confirmed by eye; the `CGSSessionScreenIsLocked` probe is absent while unlocked and reports "unlocked" either way | operator, visually | 2026-10-14 |
| `mac-02-recon.js` | macOS 26, Apple silicon | **FAIL** — Terminal never launched. Raycast owns CMD+SPACE; its top hit for "Terminal" was not Terminal.app and the script pressed ENTER blind. Nothing executed | Claude, directly | 2026-10-14 |
| `keymap-check.js` | | | | |
| `win-01-hello.js` | | | | |
| `win-02-recon.js` | | | | |
| `win-03-lock.js` | | | | |
| `linux-01-hello.js` | | | | |
| `linux-02-recon.js` | | | | |
| `linux-03-lock.js` | | | | |
| `android-01-hello.js` | | | | |
| `android-02-focus-walk.js` | | | | |
| `ios-01-hello.js` | | | | |
| `ios-02-spotlight.js` | | | | |

## Two rules for this file

**"Observed by" is not decoration.** A result someone reported and a result that
was measured are different kinds of evidence, and the header will say which. A
`VERIFIED:` line claiming more than was seen is the same defect as a test that
cannot fail — this project has shipped five of those, including one written to
verify another payload.

**A failure is a result.** `mac-02-recon` failing is the most useful row in the
table so far: it found a host dependency that affects six other scripts and was
invisible from the code.
