# Where we stopped — resume here

Written 2026-10-13. Replaces the 2026-10-09 version, which described v0.7.5 and
is now stale. Delete this file when the work below is done.

## Read these three first

| | |
|---|---|
| `docs/audit-v0.7.6-code.md` | The 8-surface code audit's results. 36 findings judged by two skeptics each: **13 confirmed (8 distinct defects), 10 split, 13 refuted.** Deduplicated and ranked, with the refuted ones listed so nobody re-reports them. **Start at C1 and S1.** |
| `docs/audit-v0.7.6-live.md` | Four defects (L1–L4) I measured against the running device, plus six things I attacked and could **not** break. Where each fix goes, and the one trap in fixing L1. |
| `dist/HIDScripts/README.md` | The new payload library, the three hard ceilings, and the two constraints that silently waste your time when adding a script. |

**The two highest-priority items across both audits:**

- **C1** — the generated SSH/sudo password is written in cleartext to the **vfat
  boot partition**, which carries no Unix modes and is world-readable on the
  running device, and that account has `NOPASSWD:ALL`. This defeats the
  per-image credential generation that replaced the old shared default: the
  credential is unique now, and left in the open.
- **S1** — a DHCP client's own hostname may be interpolated **unescaped into
  generated HIDScript source**. If it holds, anyone who associates to the AP and
  takes a lease controls what the device types into the attached host. The two
  skeptics disagreed, and the shipped `init.db` arms no DHCP-lease trigger, so
  the hinge is whether an operator armed one. Settle that before anything else.

## The device is fine, and it is on v0.7.6

Flashed, booted and reachable at `172.16.0.1:8000` over the USB link (`en9` =
`172.16.0.2`, CDC ECM). Confirmed as v0.7.6 rather than assumed: `/app/` serves
`?v=29`, matching tag v0.7.6 and HEAD. v0.7.5 served `?v=26`.

**Credentials are unknown.** This was a fresh first boot, so they regenerated.
The five passwords from earlier in the session all return 401 — verified, not
guessed. They are on the OLED panel, on the card after "Write these down".
Everything in the next section needs them.

## 1. Nothing I wrote on 2026-10-13 has been run. Not once.

Bash was blocked by a safety classifier partway through that session, so from
that point I could only read and write files. **Fourteen new payload files went
in without a single parse check**, let alone a gate or a hardware test.

So before anything else:

```bash
colima start && make verify
```

`make verify` was **green (EXIT=0) at HEAD `72d3709`**, before the payload files
existed, so any failure is mine. Colima does not start by itself —
`tools/check-js.sh` is the gate that errors with a docker socket message when it
is down, and that is what a cold Mac looks like, not a real failure.

Then read the new scripts with an eye for the three things most likely wrong:

- **ES6 that otto will reject.** No `let`, `const`, `=>`, backtick strings,
  `for...of`, destructuring, spread, `Object.assign`, `String.includes` /
  `startsWith` / `endsWith`, `Array.includes`, `Promise`, `Map`, `Set`.
- **A `press()` key name not in `dist/keymaps/common.json`.** 175 valid names.
  This is the subtlest failure mode: the script parses and runs and does nothing.
- **`waitLED`'s return shape**, which is genuinely unverified — see item 3.

## 2. Write `tools/check-hidscripts.sh` — this is the highest-value task here

There is no gate for HIDScripts at all, and fourteen untested files just landed.
The repo's own rule is that every fix gets a gate, and this is the gap that rule
exists to catch.

It should fail on: ES6 syntax; any call that is not in the real API (`type`,
`press`, `delay`, `layout`, `typingSpeed`, `waitLED`, `waitLEDRepeat`, `move`,
`moveTo`, `moveStepped`, `click`, `doubleClick`, `button`); any `press()` key
name absent from `common.json`; and any `.js` file in a **subdirectory** of
`dist/HIDScripts/`, because `ListFilesOfFolder` never recurses and such a file is
invisible to the console.

**Negative-test it by reintroducing each bug**, per the lesson in item 5. A gate
that cannot fail is worse than no gate, and this repo has shipped four of those.

## 3. The one knowingly-unverified thing in the new code

`dist/HIDScripts/platform-detect.js` reads `waitLED`'s **return value**. The
two-argument call `waitLED(mask, timeoutMs)` is confirmed from
`wifi_covert_channel.js:69`, but that script throws the result away, so nothing
in the repository demonstrates the field names, and I could not grep the Go
source once Bash was gone.

So `ledReportArrived()` probes several plausible shapes (`TIMEOUT`/`timeout`,
boolean, `undefined`) and returns `"unknown-shape"` when it recognises none,
which the script then reports honestly instead of guessing.

**Fix it properly:** read `waitLED`'s implementation in `hid/*.go`, replace the
helper with a direct field read, and delete the comment block that says to do
this. Its header marks it as the first thing to confirm on hardware.

## 4. Still open from before

- **The v0.7.6 release is a DRAFT with 5 of 8 assets.** `armhf`, `arm64` and
  `oled-armhf` images were never uploaded. An earlier attempt was interrupted —
  **ask before retrying**, it is a long upload.
- **USB storage eject and hot-swap have never been tested on hardware.** The fix
  in `mountUMSInto` (`service/SubSysUSB.go`) is unproven on a real device.
  Attaching was proven working; eject and hot-swap were proven broken, then
  fixed, then never retested.
- **arm64 images have never been booted on hardware.**
- **Two audit findings left deliberately unfixed:** the fallback rule's "USB
  fails while WiFi succeeds" case, which needs reachability as a first-class
  idea rather than a count; and `resolveAP`'s synchronous RPC on the panel draw
  path.
- **The 8-surface code audit finished** — results written up in
  `docs/audit-v0.7.6-code.md`. **Nothing from it has been fixed yet.** Full
  per-agent detail (every scenario, both verdicts) is in
  `~/.claude/projects/-Users-b0mba-at0mica-Documents-at0m-b0mb-Project/`
  `3b064d38-e52f-4822-9083-d06023148ce6/subagents/workflows/wf_1b69f670-380/`
  `journal.jsonl` — one `{"type":"result",...}` line per agent. That path is
  under `~/.claude/projects/`, not the session temp directory, so it survives.
  **Read it before re-running the audit; it cost 2.4M tokens.**

## 5. Three things about my own work, so they are not lost

Carried forward because each was a mistake that looked like correctness, and each
has now bitten more than once.

1. **A test that cannot fail is worse than no test.** It happened four times in
   one session: `TestResolveAPAsksTheServiceOnlyOnce` pinned the bug as a
   requirement; the long-PSK tests pass with the fix deleted; the first
   skipped-subsystem test passed with the bug reinstated; the CLI gate passed
   both bugs it was written for. **Negative-test every gate by reintroducing the
   exact bug.** This applies directly to item 2.

2. **`startupNeedsFallback` has been wrong three times.** Any error → fallback
   (destructive). Then "not a `PartialDeployError`" → fallback, inert because a
   skipped Bluetooth counted as success. Now "`Succeeded` is zero" — and the
   audit argues that is *still* not reachability, which is what
   `service/startup_fallback_links_test.go` addresses. Read all three test files
   before touching it again.

3. **A grep that matches its own documentation.** The image checker reported
   `Views.radio` and `WIFI_MODE` as surviving three times; both were the comments
   explaining their removal. It reads code lines only now. `strings(1)` is also
   absent from the builder image — use `grep -a`.

And one from 2026-10-13 worth adding: **anything you will need next session does
not belong in the scratchpad.** It is wiped between sessions. That ate
`flash-sd.sh` mid-flash and `findings-trim.json` the day before, and this file
and `docs/audit-v0.7.6-live.md` exist in the repo for that reason.

## Gates

```bash
colima start      # tools/check-js.sh needs docker; it is not automatic
make verify
```

Covers: api-auth, cli-commands, cachebust, js, render (13 views), palette (82
checks), shell-quoting, rpc-shapes (incl. reflex field names), contrast, vet on
both arches, unit tests, smoke (29), feature (85 RPCs), access-control (63).

Does **not** cover HIDScripts — see item 2.

To see the console with no device:

```bash
PORT=8111 ./tools/live-console.sh arm64   # admin / live-console-password
```

With the device, once you have the panel password:

```bash
P4WNP1_PASSWORD=... ./tools/hardware-check.sh
```
