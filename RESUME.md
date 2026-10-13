# Where we stopped — resume here

Written 2026-10-13, after the payloads, both audits and the USB-storage work.
Delete this file when the work below is done.

## Read these three first

| | |
|---|---|
| `docs/audit-v0.7.6-code.md` | The 8-surface code audit. 36 findings judged by two skeptics each: **13 confirmed (8 distinct defects), 10 split, 13 refuted.** Deduplicated and ranked, with the refuted ones listed so nobody re-reports them. **Nothing from it is fixed yet — start at C1 and S1.** |
| `docs/audit-v0.7.6-live.md` | Four defects (L1–L4) measured against the running device, plus six things I attacked and could **not** break. |
| `dist/HIDScripts/README.md` | The payload library, the three hard ceilings, and the two constraints that silently waste your time when adding a script. |

**The two highest-priority items, both still open:**

- **C1** — the generated SSH/sudo password is written in cleartext to the **vfat
  boot partition**, which carries no Unix modes and is world-readable on the
  running device, and that account has `NOPASSWD:ALL`. This defeats the
  per-image credential generation that replaced the old shared default.
- **S1** — a DHCP client's own hostname may be interpolated **unescaped into
  generated HIDScript source**. If it holds, anyone who associates to the AP and
  takes a lease controls what the device types into the attached host. The two
  skeptics disagreed, and the shipped `init.db` arms no DHCP-lease trigger, so
  the hinge is whether an operator armed one. Settle that first.

## The device

**Running v0.7.6 plus a hand-deployed service binary**, not a reflashed image.
`172.16.0.1:8000`, `en9` = `172.16.0.2` over CDC ECM.

Credentials from this boot, read off the panel QR codes with
`cv2.QRCodeDetector` rather than by eye — worth repeating, the pixel font makes
`0`/`O` and `y`/`g` genuinely ambiguous and my visual reading was wrong:

| | |
|---|---|
| Web | `admin` / `yVM5OQ9hoU3nRG3CP3sd` at `172.16.0.1:8000` |
| SSH | `p4wnp1` / `o7xXWjD6ysgEEhtzAcKB` |
| WiFi | `💥P4wnP1` / `Yxp7TOxjK0qNGkcn57fK` |

**The binary on the device is newer than the flashed image.** It was built from
`0c30856` and installed over SSH; the old one is at
`/usr/local/bin/P4wnP1_service.bak-v0.7.6`. A reflash would revert the four
storage fixes, so **rebuild and redeploy, or roll a new image, before flashing
anything.**

Deploying a new binary, which takes about a minute:

```bash
make build-service-armv6
scp build/P4wnP1_service p4wnp1@172.16.0.1:/tmp/svc
ssh p4wnp1@172.16.0.1 'sudo install -m755 -o root -g root /tmp/svc /usr/local/bin/P4wnP1_service && sudo systemctl restart P4wnP1.service'
```

## What is verified on hardware, so you need not redo it

- **USB mass storage, fully**: attach `test.bin` → macOS mounts `/Volumes/README`
  (FAT12, read `readme.txt`) → **hot-swap** to `cdrom.iso` with no unplug →
  `/Volumes/Test_CD`, CD-ROM flag applied → **eject** → swap back. The
  eject/hot-swap fix that had never run on a device works.
- **`hardware-check.sh`: 22 ok, 0 failed.** Access control all refused, radio on
  air, three interfaces, HIDScript engine ran a script end to end.
- **All 22 payloads parse** under the real otto, with every `press()` token and
  `layout()` name validated (`tools/hidscript-check`, in `make verify`).
- **`waitLED`'s return shape** — `hid/controller.go:527-543` returns
  `{ERROR, ERRORTEXT, TIMEOUT, NUM, CAPS, SCROLL, COMPOSE, KANA}`.
  `platform-detect.js` reads them directly now.
- `make verify` green at `0c30856`.

## Still open

- **Nothing from either audit is fixed.** That is the main body of work left.
- **The v0.7.6 release is a DRAFT with 5 of 8 assets.** `armhf`, `arm64` and
  `oled-armhf` images were never uploaded. An earlier attempt was interrupted —
  **ask before retrying**, it is a long upload. Note any new image should be
  built from `0c30856` or later so it carries the storage fixes.
- **arm64 images have never been booted on hardware.**
- **The payloads have been parsed, not run.** No `win-*`, `mac-*`, `linux-*`,
  `android-*` or `ios-*` script has typed into a real host. Every `VERIFIED:`
  line says so. They are on the device, so this is now just a matter of
  plugging it into something and running one.
- **`build/P4wnP1_service` is tracked in git** — a 16MB binary re-committed on
  every build. Worth `.gitignore` + `git rm --cached`, but it changes what a
  clone gets, so it is a decision rather than a cleanup.
- **Two audit findings left deliberately unfixed:** the fallback rule's "USB
  fails while WiFi succeeds" case, which needs reachability as a first-class
  idea rather than a count; and `resolveAP`'s synchronous RPC on the panel draw
  path.

## Four things about my own work, so they are not lost

1. **A test that cannot fail is worse than no test**, and it keeps happening.
   `TestResolveAPAsksTheServiceOnlyOnce` pinned a bug as a requirement; the
   long-PSK tests pass with the fix deleted; the first skipped-subsystem test
   passed with the bug reinstated; the CLI gate passed both bugs it was written
   for. **Negative-test every gate by reintroducing the exact bug.** Every fix
   in `0c30856` was checked that way, and `tools/hidscript-check -self-test`
   does it automatically on every `make verify`.

2. **A gate with false positives gets switched off, which is a slower way of
   having no gate.** `hidscript-check`'s first version rejected two correct
   upstream scripts by validating layouts against *filenames* — `DE_ASCII.json`
   declares `Name: "DE"`, so `layout('de')` is right. Pin the accepted forms,
   not just the rejected ones.

3. **A scanner that matches its own documentation — four times now.** The image
   checker reported `Views.radio` and `WIFI_MODE` three times running, each a
   comment explaining their removal; then `hidscript-check` failed
   `keymap-check.js` over a comment saying `layout('de_ascii')` does *not* work.
   Any scanner here reads code lines only. Note the subtlety: the `press`/
   `layout` checks cannot strip string literals, because literals are the thing
   being checked — they need a comments-only strip.

4. **`startupNeedsFallback` has been wrong three times.** Any error → fallback
   (destructive). Then "not a `PartialDeployError`" → fallback, inert because a
   skipped Bluetooth counted as success. Now "`Succeeded` is zero" — and the
   audit argues that is *still* not reachability. Read all three test files
   before touching it again.

And the operational one: **anything you will need next session does not belong
in the scratchpad.** It is wiped between sessions — that ate `flash-sd.sh`
mid-flash and `findings-trim.json` the day before. This file and the two audit
documents live in the repo for that reason.

## Gates

```bash
colima start      # tools/check-js.sh needs docker; it is not automatic
make verify
```

Covers: api-auth, cli-commands, cachebust, js, render (13 views), palette (87
checks), **hidscript (22 scripts + a 26-case self-test)**, shell-quoting,
rpc-shapes, contrast, vet on both arches, unit tests, smoke, feature (85 RPCs),
access-control (63).

Console with no device:

```bash
PORT=8111 ./tools/live-console.sh arm64   # admin / live-console-password
```

Against the device:

```bash
P4WNP1_PASSWORD='yVM5OQ9hoU3nRG3CP3sd' ./tools/hardware-check.sh
```
