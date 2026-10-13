# Code audit of v0.7.6 — results

Run 2026-10-13. Eight agents, one per attack surface, each finding then handed to
**two skeptics instructed to refute it**. 80 agents, 0 errors, ~2.4M tokens.

**36 findings judged: 13 confirmed by both skeptics, 10 split (one refuted, one
could not), 13 refuted.**

## How much to trust this

**I did not personally verify any of it.** Bash was blocked partway through that
session, so I could not run a single grep or test against these claims. What
follows is agent output that survived adversarial review — which is a real
filter, and not the same thing as confirmed.

Two reasons to take it seriously anyway. The refutations are specific and
technical, not hand-waving: one killed a clickjacking finding by pointing out
that third-party localStorage partitioning means a framed console reads an empty
token store and renders the *sign-in form*, so there is nothing privileged to
clickjack. Another killed a `/tmp` config-injection finding on
`fs.protected_regular=1`, which Debian ships by default and which root is not
exempt from. And one refutation caught an auditor **fabricating a quote** from
`service/auth/handler.go` to manufacture a "the code claims the opposite" angle.
That is the behaviour you want from a verification pass.

The full per-agent detail — every scenario, every line of evidence, both
verdicts — is in:

```
~/.claude/projects/-Users-b0mba-at0mica-Documents-at0m-b0mb-Project/
  3b064d38-e52f-4822-9083-d06023148ce6/subagents/workflows/wf_1b69f670-380/journal.jsonl
```

That path is under `~/.claude/projects/`, **not** the session temp directory, so
it survives. One `{"type":"result",...}` line per agent. Read it before
re-running the audit — it cost 2.4M tokens.

## Confirmed, deduplicated

The 13 confirmed findings are **8 distinct defects**; several surfaces found the
same thing independently, which is corroboration rather than noise. Ordered by my
judgement of what to fix first.

### C1 — Generated SSH/sudo password written in cleartext to the FAT boot partition
`dist/scripts/firstboot-secure-defaults.sh:363` and `:367` — **found twice
(secrets, image)**

The boot partition is vfat, so it is world-readable on the running device, and
the account whose password is written there has `NOPASSWD:ALL` sudo
(`image/lib/customize.sh:234`). Any local account — or anyone who puts the SD
card in a reader — reads a root-equivalent credential out of a file with no
meaningful permissions, because vfat cannot carry Unix modes.

This is the one I would fix first. It defeats the per-image credential generation
that replaced the old shared `p4wnp1:p4wnp1` default: the credential is now
unique but it is left lying in the open.

### C2 — Bluetooth ships the hardcoded, source-published PIN "1337" with permissive defaults
`service/defaults.go:37`, `service/bluetooth.go:70` — **found twice (netsvc, image)**

`NewDefaultAgent("1337")`, with `ServiceNetworkServerNap: true`,
`Discoverable: true` and `Bondable: true` deployed at every service start
(`defaults.go:24,29,30`). There is **no equivalent of the WiFi PSK guard** —
`wifi_psk_guard.go` substitutes a per-device key for any publicly-known PSK, and
Bluetooth got nothing comparable.

**Nuance worth keeping, from a refutation:** a separate finding claimed the agent
*auto-accepts just-works pairing* (`RequestAuthorization` returns nil
unconditionally) and that was **refuted** — `defaults.go:32` sets
`SecureSimplePairing: false`, so BlueZ takes the legacy `RequestPinCode` path and
the just-works branch is unreachable as shipped. The console also explains the
consequence at the point of change. So the live defect is **the published PIN plus
the permissive defaults**, not the auto-accept. Fix the PIN; don't rewrite the
agent on the strength of the refuted claim.

### C3 — hostapd config write error is discarded, so root hostapd runs whatever is already at the predictable /tmp path
`service/wifi.go:324` — **confirmed once, split once**

`hostapdCreateConfigFile2(newWifiSettings, wSvc.PathHostapdConf)` throws its
error away. If that write fails, `StartHostapd` (`wifi.go:84`) launches hostapd
against the existing file content.

**This one is sharpened by a refutation elsewhere**, which is why it is ranked
this high. A broader "root daemons read configs from world-writable /tmp" finding
was refuted on `fs.protected_regular=1` grounds — root's `O_CREAT` open fails
EACCES on an attacker-owned file in a sticky directory, so the attacker cannot
pre-plant content *and* have root write to it. But that same refutation concluded:
*"That dropped error is the only path by which pre-created content reaches a root
daemon at all, and it is a different defect from the one reported."* Two
independent analyses land on this line. Check the error.

### C4 — Login flood: a FIFO queue locks the operator out, and Login discards the request context
`service/auth/manager.go:94` and `:95` — **found twice (auth ×2)**

`Login(_ context.Context, ...)` never reads the context, so a client that hangs
up still pays the full bcrypt-12 plus the 1s delay. Requests queue on
`m.loginSlots` (cap 2) in FIFO order, so the operator's correct login waits behind
every attacker request already enqueued, and the queue is unbounded. No
`WriteTimeout`, no listener limit (`rpc_server.go:1749-1754` sets only
`ReadHeaderTimeout` and `IdleTimeout`, and the latter does not apply while a
handler is running).

**My own live measurement bounds this**, and the two should be read together —
see `docs/audit-v0.7.6-live.md` L3. Six parallel wrong logins took 19s wall,
~3.2s each, confirming the serialisation; `/api/auth/health` stayed at 28–41ms
throughout, so the single core is **not** saturated and the rest of the device
stays responsive. It is a login-plane DoS, not a device-wide one. The audit's
contribution over my measurement is the unbounded queue and the discarded
context, both of which I did not test.

The existing test proves the wrong thing: `TestASuccessfulLoginDoesNotWaitOnThe`
`FailureLock` uses three attackers and only shows the operator does not queue
behind the *delay* lock. It never touches the slot queue. Another instance of
this repo's recurring "a test that cannot fail" problem.

### C5 — WiFi PSK passed to /usr/bin/wpa_passphrase as a command-line argument
`service/wifi.go:738` — **found twice confirmed, once split**

Visible in `/proc/<pid>/cmdline` to every local account for the lifetime of the
process. Applies to both the AP PSK and the operator's upstream client
passphrase. Fix is to feed the passphrase on stdin, which `wpa_passphrase`
supports.

### C6 — Configs holding every WiFi PSK written to fixed /tmp paths with no O_NOFOLLOW, no O_EXCL and no PrivateTmp
`service/wifi.go:558` — **found twice**

`/tmp/hostapd_wlan0.conf` and `/tmp/wpa_supplicant_wlan0.conf`, plain
`os.WriteFile`, mode applied only on create. The main unit has no `PrivateTmp`
(`dist/P4wnP1.service`) although `p4wnp1-oled.service` and
`p4wnp1-firstboot.service` both do.

**Treat the severity as open.** A refutation argued convincingly that
`fs.protected_regular=1`, `protected_fifos` and `protected_symlinks` block the
pre-plant attack, leaving only a transient DoS via `mkdir` on the config path.
Even so: these four writes bypass the project's own hardened
`common.WriteFile` (which has `O_NOFOLLOW` and optional `O_EXCL`) and the
`CreateTemp`+`Chmod`+`Rename` pattern already used in `service/auth/store.go:157`.
Adding `PrivateTmp=yes` to the main unit is cheap and closes the whole class.

### C7 — gRPC MaxConcurrentStreams unset on a 0.0.0.0:50051 listener
`service/rpc_server.go:1591`

grpc-go v1.38.0 with no `MaxConcurrentStreams`, no `KeepaliveParams`, no
`MaxConnectionAge`. One unauthenticated connection may open unlimited streams.

### C8 — gRPC streams authenticate once; neither revocation nor expiry reaches a live EventListen
`service/auth/interceptor.go:32` — **both skeptics corrected high → medium**

`StreamInterceptor` validates at stream open and never again. `EventListen`
(`rpc_server.go:1058`) then blocks in a `for`/`select` whose only exits are
transport teardown and event-manager shutdown. Revocation is a map delete with no
broadcast, and `Session` carries no cancel function, so nothing can reach the
live stream. It also outlives the 24h TTL, since expiry is only enforced inside
`Sessions.Lookup`, which the stream never calls.

The SSE transport **was** fixed for exactly this, on a 20s ticker, under the
comment *"Revocation that does not reach the one long-lived connection is not
revocation"* (`rest_api.go:438-443`). The gRPC sibling the comment's own reasoning
covers was left alone.

Both skeptics downgraded it to medium on sound grounds: it requires an already-
valid token, what is retained is read-only (unary RPCs re-validate per call), and
the feed carries DHCP leases, SSH usernames and HID job results but no PSKs or
tokens. The reason it is still real: **the control fails silently** — revoke
returns 204 and the console then omits the session, so the operator is told the
intruder is gone while the feed keeps running.

## Split — one skeptic refuted, one could not

These need a human decision. Listed worst-first by potential impact.

### S1 — A DHCP client's own hostname is interpolated unescaped into generated HIDScript source
**Investigate this one first.** If it holds, an unauthenticated party who merely
associates to the AP and takes a DHCP lease controls **what the device types into
the attached host** — which is the whole product, turned against its operator. It
is the only finding in the audit with that reach, and the two skeptics disagreed.

Note what the shipped database says, from a *different* finding's refutation: the
shipped `init.db` contains **no `dhcpLeaseGranted` trigger** (fields decoded:
`tas_startup` is serviceStarted, `tas_wifi_covert_channel` is usbGadgetConnected,
`tas_bincounter` is GPIO/group). So this may require the operator to have armed a
DHCP-lease trigger. That is the hinge — settle it before anything else.

### S2 — datastore.Restore clears the live database, then discards the error from the load meant to refill it
`service/datastore/store.go:144` and ~`:175`, returning `nil` at `:223`.

**Two independent paths reached this conclusion.** The split finding above, and
the refutation of a *different* DBRestore finding, which rejected the security
framing but ended: *"Clear() before a validated load plus the swallowed Load error
means ANY corrupt backup — including one truncated by the power-loss-on-unplug
case that `store.go:66 badgerOpts.Truncate = true` exists for — silently empties
the live datastore and answers 'Restored'."*

Not a privilege crossing. A real data-loss bug on a device that is powered by the
USB port it is plugged into, so abrupt power loss is the normal case.

### S3 — HIDScript `type()` writes the typed text to the systemd journal
`hid/controller.go` — the first 15 characters always, the whole string on a write
error. Whatever a payload types — including anything an operator typed into a
target — lands in the journal. Related to the already-fixed `%+v`-dumps-PSKs
class.

### S4 — gRPC and HTTP are hardcoded to bind 0.0.0.0
`service/service.go:274`. On the USB link and the device's own AP this is fine. In
**station mode**, when the appliance has joined someone else's network, it exposes
a cleartext management API to every host on that network. A posture question more
than a bug, but it deserves a deliberate answer.

### S5 — SSE keepalive goroutine can write to the ResponseWriter after ServeHTTP returned
Racing `recordingWriter` and net/http's pooled `bufio.Writer`. A genuine data
race; `go test -race` on the SSE path would settle it.

### S6 — knownHost whitelists the entire `.local` namespace
`service/rest_api.go:220`. `strings.HasSuffix(host, ".local")` accepts every mDNS
name, not just the device's own, and `TestKnownHostRejectsRebindableNames` has no
`.local` case — so the test overstates the control.

The refuting skeptic's argument is strong: an mDNS responder must already be on
the same L2 link, where it can reach everything directly; there is no cookie, and
the token is in origin-scoped localStorage, so a rebound page cannot borrow
credentials. Still worth narrowing to `p4wnp1.local` and adding the missing test
row — cheap, and it fixes a test that currently claims more than it proves.

### S7–S10 — duplicates of confirmed items
Framability ×2 (my L1), hostapd stale-config (C3), upstream PSK on the command
line (C5).

## Refuted — do not re-report these without new evidence

13 findings died. The ones worth knowing, because they will occur to the next
person too:

- **Console framability as a clickjacking vector.** Headers genuinely absent, but
  third-party storage partitioning means a framed console reads an empty
  localStorage, so `hasToken()` is false and it renders the sign-in form. No
  ambient credential exists anywhere — the tree has no `Set-Cookie` at all. Still
  worth adding `frame-ancestors 'none'` as defence in depth (see L1), but the
  exploit chain does not close.
- **ChangePassword cross-account rewrite.** False: `manager.go:125` verifies the
  *target* account's own current password. The finding also fabricated a comment
  quote. Residual: it should hold `loginSlots`/`failedLogin` for parity with
  `Login`, and bind `sess.Username` rather than `req.Username`.
- **DBRestore / MountUMSFile / FSCreateTempDirOrFile / trigger ScriptName path
  traversal.** All four refuted on the same ground: every route is token-gated,
  and **a token on this device is root-equivalent by design** — `FSWriteFile` into
  `PATH_BASH_SCRIPTS` at mode 0700 plus a trigger action equals arbitrary root
  execution. A traversal grants nothing the caller does not already have. Each
  leaves a real hygiene residual worth bundling, none is a privilege crossing.
- **Unbounded trigger fork storm.** Refuted: the shipped `init.db` arms no
  DHCP-lease trigger, the AP is WPA2 with a per-device PSK, and the HID path is
  bounded at `MAX_VM = 8`. Residual: no rate limit or `TasksMax=` for bash
  actions.
- **Token in cleartext over the AP.** Refuted because the premise that PSK and
  console password are separate credentials is false — `oled/firstrun.go:368-419`
  builds one card deck, so whoever scanned the WiFi QR was shown the console
  password on the previous card. Also cited two block-commented code regions as
  live evidence.

## Two meta-observations worth keeping

**Dead code was cited as live evidence three separate times** — `defaults.go:177`
opens a block comment closing at `:200`; `bluetooth.go:313` sits inside a comment
block running to `:356`; `rpc_server.go:1499` and `:1528` comment out two
unauthenticated gRPC starters. Every auditor that quoted one drew a wrong
conclusion, and every skeptic caught it. If this codebase is audited again, the
first instruction should be *check whether the line you are quoting is inside a
comment.*

**The strongest findings came from asymmetry, not from absence** — "the SSE
transport re-checks the session and the gRPC one does not" (C8), "the WiFi PSK has
a substitution guard and Bluetooth has none" (C2), "the other FS RPCs were
tightened and this one was missed". Pointing a future audit at *inconsistency
between siblings* would be more productive than another sweep for missing
controls.
