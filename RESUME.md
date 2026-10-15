# Where we stopped — resume here

Written 2026-10-14, after v0.8.0 shipped. Delete this file when the work below
is done.

## v0.8.0 is published

https://github.com/at0m-b0mb/P4wnP1_aloa/releases/tag/v0.8.0 — four images, four
checksums, no orphans. **All ten audit findings are fixed** (C1–C8 from the code
audit, S1 from the split list) plus four USB storage defects found on hardware.

`docs/audit-v0.7.6-code.md` and `docs/audit-v0.7.6-live.md` remain the record of
what each one actually allowed. Nothing in them is outstanding.

## The three things that are NOT verified

Stated first because they are the honest gaps in a release that otherwise looks
finished.

1. **The updater's AUTOMATIC rollback has never fired.** Update, update again,
   and `--rollback` by hand are all verified on hardware. The path taken when the
   post-update health check FAILS is not — triggering it means deliberately
   installing a binary that will not start.
2. **The arm64 images have never been booted.** Both arm64 variants ship
   untested on a real Pi Zero 2 W.
3. **Twelve of the fifteen payloads have never run against a host.** Verified:
   `mac-01-hello`, `mac-03-lock`, `platform-detect` — all against a Mac.
   `mac-02-recon` is a recorded FAILURE. Each file's own `VERIFIED:` line is the
   authority; `docs/payload-test-log.md` is the running record.

**C6 (`PrivateTmp`) is now verified on hardware.** The in-place update installed
the new unit file; the device reports `PrivateTmp: yes` and the access point
stayed up (`AP_UP`, channel 6) through it. That also explains a confusing
symptom worth remembering: after the update the service's `/tmp` is PRIVATE, so
files written there through the API before the update are no longer visible to
it. That looks exactly like a failed upload and is actually proof of success.

## The device

Running a hand-deployed binary **newer than any flashed image**, so a reflash
reverts it — which is now the right move anyway, to test item 1 above.
`172.16.0.1:8000`, `en9` = `172.16.0.2` over CDC ECM.

Credentials from this boot, decoded from the panel QR codes with
`cv2.QRCodeDetector` rather than read by eye — the pixel font makes `0`/`O` and
`y`/`g` genuinely ambiguous and my visual reading was wrong:

| | |
|---|---|
| Web | `admin` / `yVM5OQ9hoU3nRG3CP3sd` |
| SSH | `p4wnp1` / `o7xXWjD6ysgEEhtzAcKB` |
| WiFi | `💥P4wnP1` / `Yxp7TOxjK0qNGkcn57fK` |

**The Pi's sshd password auth is wedged** — the password packet is sent and never
answered, from SSH sessions that were killed. The API and ping are completely
healthy. Unplug and replug clears it. `tools/deploy-service.sh` works once it is.

## What to do next

- Flash v0.8.0, boot it, confirm the AP comes up and a client gets a lease.
  That closes gap 1 and is the last thing standing between this release and
  "verified".
- Run the remaining payloads against hosts you own. `keymap-check.js` needs a
  text field focused — **not a terminal**, every line ends in Enter.
- `tools/hidscript-check` gates the payloads; `tools/deploy-service.sh` installs
  a rebuilt service without reflashing.

## Five things about my own work, so they are not lost

Each was a mistake that looked like correctness.

1. **A test that cannot fail is worse than no test**, and I did it again while
   *verifying* — my macOS lock check read a key that is absent whether locked or
   not, so it reported "unlocked" either way and nearly had me call a working
   payload broken. Negative-test every check, including the ones you write to
   check someone else.
2. **A gate with false positives gets switched off**, which is a slower way of
   having no gate. `hidscript-check` first rejected two correct upstream scripts
   by validating layouts against filenames; `DE_ASCII.json` declares `Name: "DE"`.
3. **A scanner that matches its own documentation — four times now.** The latest:
   the payload gate failed `keymap-check.js` over a comment saying
   `layout('de_ascii')` does *not* work. Scanners here read code lines only, and
   the `press`/`layout` checks need a comments-only strip because the string
   literals are the thing being examined.
4. **Running it is not optional.** Every payload silently returned `null` because
   `AsyncOtto.go:196` wraps the source in a function and discards a trailing
   expression; and `platform-detect` called a Mac "no keyboard" because
   `controller.go` sets `ERROR` on a timeout too. Neither was reachable from a
   test. Both died on first contact with real hardware.
5. **`startupNeedsFallback` has been wrong three times.** Read all three test
   files before touching it again.

And the operational one: **anything you will need next session does not belong
in the scratchpad.** It is wiped between sessions — that ate `flash-sd.sh`
mid-flash, `findings-trim.json`, and the SSH helpers mid-deploy, which is why
`tools/deploy-service.sh` now lives in the repo.

## Gates

```bash
colima start      # tools/check-js.sh needs docker; it is not automatic
make verify
```

Adds `hidscript-check` to the previous list: 22 payloads compiled under the real
otto, wrapped the way the service wraps them, with a 26-case self-test.

Against the device:

```bash
P4WNP1_PASSWORD='yVM5OQ9hoU3nRG3CP3sd' ./tools/hardware-check.sh
```
