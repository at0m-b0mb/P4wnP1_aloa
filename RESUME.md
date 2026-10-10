# Where we stopped — resume here

Written at the end of the session on 2026-10-09. Delete this file when the
work below is done.

## The device is fine

**v0.7.5 is tagged, flashed and verified on real hardware.** Leave it alone
unless you want the pending fixes.

- 19 RPCs exercised over USB, zero errors
- the startup fallback correctly did NOT run
- GPIO returns all 29 real pins (`GPIO0`–`GPIO27`, `GPIO46`)
- the OLED panel mirror serves
- four credential cards, and the WiFi join QR works on a phone

Credentials from this boot (they change on every reflash):

| | |
|---|---|
| Web | `admin` / `8iDuGecNKaw8tjUd5Wid` at `172.16.0.1:8000` (USB) or `172.24.0.1:8000` (its WiFi) |
| SSH | `p4wnp1` / `PHE32NuhvGN3MnN73LX1` |
| WiFi | `💥P4wnP1` / `BAmJ3BDMfsZiuCvowPo6` |

## 1. Apply the two CSS fixes — do this first, it is 5 minutes

Both were found by testing the **running device** at six viewport widths, and
both are verified live. They are written up in full, with the measurements,
in the session scratchpad copy of `pending-css-fixes.md`; the short version:

In `dist/www/app/css/app.css`:

```css
/* line ~102 — minmax(300px,1fr) keeps a 300px FLOOR, so below ~390px of
   viewport the track still claims 300px and pushes the page sideways.
   Measured at 320px: Cable, Bluetooth and Network each gave the page 21px
   of horizontal scroll. Verified fix: 21px -> 0 on all three. */
.grid-2 { grid-template-columns: repeat(auto-fit, minmax(min(300px, 100%), 1fr)); }

/* line ~115 — flex + space-between with no gap and no wrap, and .cable-state
   has no nowrap, so on a phone the status text squeezes to 96px and wraps to
   three lines against "WHAT THE CABLE PRESENTS". Photographed on the owner's
   own phone. Verified fix: three lines -> one. */
.cable-head  { gap: var(--s3); }
.cable-state { white-space: nowrap; flex: none; }
@media (max-width: 560px) {
  .cable-head { flex-wrap: wrap; gap: var(--s2); }
  .cable-head > div { flex: 1 1 100%; }
}
```

Then: `./tools/check-cachebust.sh` will fail — that is it working. Bump every
`?v=` in `dist/www/app/index.html`, run `make cachebust`, re-run the gates.

## 2. Finish the audit fixes

48 findings were confirmed by an adversarial audit (9 others were refuted).
**6 are fixed.** The remaining ~39 are listed in the session scratchpad as
`findings-trim.json`, grouped by file under the keys `app-js`, `css`, `go`,
`palette`, `templates`, `wifi-bt`.

The workflow that was fixing them is at:

```
.claude/.../workflows/scripts/console-audit-fixes-wf_9cc82ea0-d45.js
```

Re-run it with `Workflow({scriptPath: <that>})`. It reads the findings file
itself. Agents own disjoint files so they cannot collide — keep that property
if you change it.

Highest value remaining:

- **`app-js` (19)** — reflexes cannot be saved from the Reflexes page; a
  stored script is silently overwritten by the name the dialog pre-fills; a
  deploy timeout gives a fact and no next step; 8 strings still send the
  operator to the retired Radio page.
- **`css` (5)** — tap targets are 30px (buttons) and 16px (checkboxes)
  against a 44px guideline; account chrome eats ~44% of a phone viewport
  before the page starts.

## 3. Three things about my own work, so they are not lost

These are the ones worth remembering, because each was a mistake that looked
like correctness:

1. **A test that cannot fail is worse than no test.** It happened four times
   this session — `TestResolveAPAsksTheServiceOnlyOnce` pinned the bug as a
   requirement; the long-PSK tests pass with the fix deleted; my first
   skipped-subsystem test passed with the bug reinstated; my CLI gate passed
   both bugs it was written for. Negative-test every gate by reintroducing
   the exact bug.

2. **`startupNeedsFallback` has now been wrong three times.** Any error →
   fallback (destructive). Then "not a PartialDeployError" → fallback, which
   was inert because a skipped Bluetooth counted as a success. Now "Succeeded
   is zero" — and the audit argues that is *still* not reachability, which is
   what `service/startup_fallback_links_test.go` addresses. Read all three
   test files before touching it again.

3. **A grep that matches its own documentation.** The image checker reported
   `Views.radio` and `WIFI_MODE` as surviving three times; they were the
   comments explaining the removal. It reads code lines only now.

## Gates (all green at this commit)

```
make verify
```

covers: api-auth, cli-commands, cachebust, js, render (13 views), palette (82
checks), shell-quoting, rpc-shapes (incl. reflex field names), contrast, vet
on both arches, unit tests, smoke (29), feature (85 RPCs), access-control (63).

To see the console with no device:

```bash
PORT=8111 ./tools/live-console.sh arm64   # admin / live-console-password
```
