# Known issues

Last refreshed: 2026-10-07, for v0.3.1. Two audit passes (96 agents, then 57)
in which every high-severity finding was independently re-checked by a second
reviewer before being accepted, plus a pass driving the console by hand against
the real service, which is where the worst bug in this list was found.

Severity: **Critical** — boot-blocking or exploitable. **High** — blocks a
common workflow. **Medium** — confusing or stale. **Low** — cosmetic.

---

## Fixed in v0.2.0

### Critical — the service panicked on every cold boot

`CheckLibComposite` ended in an unconditional `log.Panic(err)` that ran even
when `err` was nil, i.e. on the success path. On a stock Raspberry Pi OS cold
boot, libcomposite is a loadable module that is not yet loaded and the
preceding `rmmod` probe could never report "builtin", so execution reached that
line every time. The panic was inside `NewService()`, before `Start()`, with no
`recover()` in the path: no gRPC, no web console, no access point.

Deceptively, the `modprobe` succeeded *before* the panic, so a manual
`systemctl restart` found the module loaded and the device looked healthy —
only cold boots failed. This is the most likely cause of upstream
[#363](https://github.com/RoganDawes/P4wnP1_aloa/issues/363).

### Critical — a password change needed no token

`/api/auth/changepw` required only the old password, and checked no
Content-Type, making it a CORS-simple request deliverable from any site the
operator was visiting. `ChangePassword` calls `RevokeAll()`, so a correct guess
changed the admin password and logged the operator out. Now requires a valid
bearer token, and both login and changepw require `application/json`.

### Critical — an exposed RPC whose body was `panic("implement me")`

`ListenWiFiStateChanges`. grpc-go does not recover handler panics, so any
authenticated caller could end the process with one request. Returns
`Unimplemented` now, and a panic-recovery interceptor was added as a backstop.

### Critical — the access point used a PSK published in this repo

`dist/db` still carries the upstream default `MaMe82-P4wnP1`, and the deployed
template beats the compile-time default, so that is what a flashed device
actually broadcast. A guard in the access-point start path now substitutes a
per-device random PSK, generated once and persisted at
`/etc/p4wnp1/generated-ap.psk`.

### High — nearly the whole CLI failed `Unauthenticated`

Of 21 `grpc.Dial` sites, exactly one attached the bearer token. Every other
command — including the trigger-action helpers boot scripts call — failed once
auth was enforced. All 21 now route through one authenticated dialer.

### High — reboot and shutdown did nothing

`Service.Stop()` tears subsystems down before the reboot syscall, and two
teardowns nil-dereferenced on ordinary hardware (no Bluetooth adapter; stock
kernel without the dwc2 netlink family). Each teardown is now individually
recovered, so the syscalls are reached.

### High — the WiFi constructor panicked on a boot race

The unit starts before `sysinit.target` and is not ordered against udev, while
brcmfmac loads firmware asynchronously — so `wlan0` often does not exist yet.
`NewWifiService` panicked, killing the whole device for that boot. It now waits
briefly, then degrades with WiFi disabled.

### High — `install.sh` never configured USB gadget mode

It never wrote `dtoverlay=dwc2` or `modules-load=dwc2`, so on a manual install
the entire USB feature set silently did nothing. It also used `/boot`, which
Debian 12 moved to `/boot/firmware`. Both fixed, with the boot partition
detected rather than assumed.

### High — one retired package aborted the whole install

`policykit-1` (renamed `polkitd` in Debian 12) was in a single `apt-get install`
under `set -e`. Packages are now split into required and optional.

### High — the web UI was dead

Auth was added to every RPC without updating the GopherJS client, which has no
auth code. Replaced by a new console; see the README.

### Medium — `FSReadFile` allocated an attacker-supplied length

`make([]byte, req.Len)` on an unvalidated int64. Now bounds-checked.

### Medium — six vet defects, including a guaranteed panic

The `triggerTypeGroupReceive` branch type-asserted to the *wrong* oneof
variant, so every group-receive trigger firing panicked the daemon. Plus two
dropped-argument format strings and three unreachable returns.

### Medium — arm64 was impossible

Build tags gated the core service to 32-bit ARM. They were incidental, not a
real dependency. Pi Zero 2 W / 3 / 4 / 5 now build.

---

## Fixed in v0.3.1 -- access control

An audit of access control specifically: seven parallel reviewers over the
routing, token, origin, path, dispatch, privilege and test-coverage surfaces,
every finding adversarially re-checked by three reviewers with different
lenses, and the survivors confirmed by ATTACKING A RUNNING SERVICE rather than
by reading code. `make access-control` is the gate that came out of it -- 60
checks, every one an attack that must fail, and most were written by first
demonstrating the attack succeeding.

### Critical -- arbitrary root file write through the file-IO allowlist

`safeJoinUnderBase` used `filepath.Clean`, which is purely lexical and does
not resolve symlinks, while one of the three allowed folders is `/tmp`.
Demonstrated end to end against the real binary: an unprivileged local user
created

```
/tmp/sub/escalate -> /etc/cron.d/pwned
```

and `FSWriteFile(folder=TMP, filename="sub/escalate")` wrote a **root-owned
cron job** through it. Writing a cron drop-in as root is root code execution.
The same symlink served reads, leaking `/etc/p4wnp1/auth.json` (the bcrypt
password hashes) and `/run/p4wnp1/local.token`.

The kernel does not save you here, and the way it fails is the interesting
part. `fs.protected_symlinks` refuses a foreign-owned symlink only when it
sits **directly** in a sticky world-writable directory. The first attempt at
this attack -- a symlink straight in `/tmp` -- was correctly blocked by it.
Moving the symlink one level down, into an ordinary directory the attacker
created, defeats the protection entirely, because `/tmp/sub` is not sticky.

Fixed in two layers that cover each other's blind spot: `safeJoinUnderBase`
resolves the deepest existing ancestor with `EvalSymlinks` and re-checks
containment, and `common.WriteFile`/`ReadFile` open with `O_NOFOLLOW`. The
containment check catches a symlinked *directory component*, which
`O_NOFOLLOW` cannot see; `O_NOFOLLOW` catches a *dangling* symlink leaf, which
`EvalSymlinks` cannot resolve.

Preconditions: a local non-root shell on the device plus an API session. The
`p4wnp1` SSH account is exactly such a shell, and it is deliberately non-root,
so this crossed a boundary the design intends to hold.

### High -- an out-of-band password reset left the OLD password working

`p4wnp1-hashpw` runs as a separate process and rewrites
`/etc/p4wnp1/auth.json` directly; that is how first boot seeds the account and
how an operator resets a forgotten password. The running service read that
file once at startup and never again, so after a reset the **new password was
rejected, the old one kept working, and every pre-existing session survived**.
A rotation that leaves the old credential live is worse than no rotation,
because the operator believes it is done.

`Store.Verify` now reloads when the file's modtime or size changes. A corrupt
or half-written file is refused rather than emptying the user table, which
would lock the operator out of their own device.

### High -- `P4wnP1_cli auth changepw` could never work

It posted with `httpClient.Post`, which attaches no headers, to an endpoint
that requires a bearer token. HTTP 401, every time it was run. The smoke test
asserts that a changepw *without* a token is refused -- a correct assertion
that passed the entire time the CLI sent exactly that shape.

### Medium -- the credential endpoints had no origin or host check

The Host and Origin checks lived inside `authenticate()`, which covers
`/api/v1/*` only. `/api/auth/*` is a different handler and had neither, so a
foreign origin posting to `/api/auth/login` got HTTP 200 and a fresh token.
No CORS header is emitted so an ordinary cross-origin page cannot read that
response -- but a DNS-rebound one can. Now a single `GuardBrowserOrigin`
middleware states the rule once for login, logout, whoami and changepw.

### Medium -- DNS rebinding defeated the same-origin check

`sameOrigin` compares `Origin` against `Host`, and an attacker who controls a
domain controls both. Confirmed against a running service:

```
Host: evil.example + Origin: http://evil.example  -> 200, the RPC executed
Origin: http://evil.example alone                 -> 403, correctly refused
```

Rebinding requires a *name*, because an IP literal resolves to itself. This
device is reached at `172.16.0.1` over USB, `172.24.0.1` over its own access
point, or `localhost` on the device itself, so the fix accepts IP literals,
loopback and mDNS `.local` names and refuses other DNS names. An operator who
genuinely reaches it by a name of their own lists it in
`P4WNP1_ALLOWED_HOSTS`, and the 403 says so.

### Medium -- changing the password locked the device out of itself

`ChangePassword` calls `RevokeAll`, which destroys the machine-local
credential along with every human session, and nothing re-issued it until
`KeepLocalTokenFresh` next ticked -- up to twelve hours later. For that whole
window any trigger action firing on the device would fail `Unauthenticated`,
which is precisely the outage the local credential exists to prevent. It is
now re-issued immediately.

### Medium -- `FailedLoginDelay` throttled nothing

The one-second delay ran in each request's own goroutine, so twenty parallel
password guesses cost about one second in total rather than twenty. The delay
exists specifically to deny brute-force throughput and was not denying any.
Rejections now serialise on a mutex, capping the rate at one guess per delay
however many connections an attacker opens. Successful logins do not take the
lock, so the throttle cannot be turned into a denial of service against the
operator -- asserted by its own test.

### Medium -- missing hardware was reported as an internal server error

Every USB, HID and Bluetooth RPC answered HTTP 500 on a board without that
hardware. A 500 asserts that the *service* is broken; a Pi with no UDC bound
or no Bluetooth controller is a normal, supported state. Fixed with one
classifier on the JSON bridge rather than a wrap at each of the thirteen call
sites, so RPCs added later are covered too. Only the three
hardware-unavailable sentinels are reclassified; everything else passes
through untouched, so a genuine bug still surfaces as one.

### High -- an apostrophe in the WiFi passphrase bricked the device at first boot

`install.sh` wrote the SSID and PSK into `/etc/p4wnp1/initial.conf` wrapped in
single quotes with no escaping. The firstboot helper **sources that file as
root**, under `set -euo pipefail`, and it is the script that creates the admin
account. So an apostrophe -- an ordinary thing for a passphrase to contain --
produced `P4WNP1_INITIAL_SSID='Bob's AP'`, which does not parse, which aborted
firstboot, which meant **no admin account was created and the device rejected
every console request**.

A crafted value was worse: everything after the closing quote ran as root on
first boot. Install commands get copied out of documentation and support
threads, so that is not purely self-inflicted.

Fixed at both ends: `install.sh` emits values with `printf %q`, and firstboot
syntax-checks the file with `bash -n` before sourcing it, so a hand-edited
typo is a warning and a fall back to defaults rather than a brick.

### High -- the healthcheck leaked the admin password and a bearer token

`p4wnp1-healthcheck.sh --login-test`, documented to be run with `sudo`:

- passed the admin password to `curl` as a command-line argument, leaving it in
  `/proc/<pid>/cmdline`, which is world-readable -- and this device ships an
  unprivileged `p4wnp1` SSH account;
- wrote the login response, which contains a **bearer token**, to
  `/tmp/p4wnp1-login-$$`: a predictable path in a world-writable directory,
  created 0644, and a symlink target a local user can pre-create so that root
  writes through it (`curl -o` opens `O_CREAT|O_TRUNC` and follows symlinks);
- only removed that file on the success path.

Now stdin, `mktemp`, and a trap on `EXIT INT TERM`.

### Medium -- a race could leave the device unable to authenticate to itself

`ProvisionLocalToken` held its lock only around the state swap, leaving a
window as wide as one file write. Two overlapping calls each wrote their own
token and then each revoked what it believed was the previous one, so the
loser's revocation could land on the token actually left in the file.
Measured: with calls started within 100us of each other the file held a
**revoked** token in 97-99% of 2000 trials, and the window closed at about
250us. On a device the overlap is the twelve-hourly refresh colliding with a
password change. Provisioning is now atomic, and `writeTokenFile` uses
`os.CreateTemp` rather than a fixed `.new` suffix two writers would collide on.

### Low -- `localTokenState` was a package-level var

Two Managers shared one slot: the second to provision took ownership of the
first's entry, leaving the first's session unrevocable and its file orphaned,
while each Manager called `Revoke` against its own session map for the other's
token -- a no-op that looked like success. Now per-Manager.

### Checked and found sound

Stated because "we tested it" is worth nothing without saying what was tested.
With evidence from a running service: unauthenticated callers are refused on
every API route; foreign, `null` and lookalike origins are refused even with a
valid token; `OPTIONS` cannot be turned into a permissive preflight, and no
CORS header is emitted anywhere; lexical path traversal is refused on all
three folders for both read and write, including encoded and doubled forms;
logout revokes one session and only one; a password change revokes all; an
oversized request body is refused; no token or password appears in the service
log.

### Medium -- an unauthenticated websocket upgrade parked goroutines forever

The router dispatched on `Sec-Websocket-Protocol` **before any auth or origin
check**, into a wrapper built with `WithWebsockets(true)`. `WithOriginFunc`
governs the HTTP check, not the websocket one, and the library's default
websocket origin check compares `Origin` with `Host` -- two headers the caller
writes. gorilla's `Upgrade` then clears the socket deadline, so the server's
`ReadHeaderTimeout` and `IdleTimeout` stop applying, and `handleWebSocket`
blocks in `ReadMessage` with no read limit.

An anonymous peer with nothing but TCP reach could park goroutines and file
descriptors indefinitely, or stream an unbounded first frame into the heap of
a root process driving HID injection, DHCP and hostapd. Confirmed against the
running service: `HTTP/1.1 101 Switching Protocols`, no credential.

Deleted rather than fixed, because nothing speaks it: the transport existed
for the GopherJS client, which is documented as non-functional, and the
console uses fetch and SSE.

### Medium -- a revoked session kept receiving the event stream

Authentication on `/api/v1/events` happened once, at open, and then the
connection blocked for its whole life. A logout, a per-session revoke and a
password change all left it running, still delivering every device event --
HID activity, DHCP leases, trigger fires -- to a credential that had been
withdrawn. The keepalive tick now re-checks the session (without sliding its
expiry) and closes the stream.

### Medium -- `/api/auth/login` decoded an unbounded request body

The one endpoint an anonymous caller can reach that parses a body, on a 512MB
device running as root. All three auth handlers now share a 16 KiB limit.

### Medium -- the login throttle bounded the rate but not the cost

Serialising the post-failure delay stops an attacker getting more than one
guess per delay. It does nothing about each attempt's cost: bcrypt at cost 12
is roughly 250ms of CPU on a Pi Zero W and runs *before* the delay, so fifty
parallel attempts still bought fifty concurrent bcrypts on a single-core board
that may be mid-keystroke-injection. Concurrent verifications are now capped
at two.

### Medium -- WiFi pre-shared keys were written to the systemd journal

`log.Printf("Settings: %+v", ...)` on the WiFi deploy path. protoc-gen-go
gives every message a `String()` that text-marshals the whole nested
structure, so the access point's PSK **and the PSK of every saved client
network** went to the journal, readable by any local account. The client keys
are the worse half: those are the operator's own home, office and client-site
networks.

### Medium -- `DBBackup` wrote anywhere, world-readable

A caller-supplied filename was concatenated onto the backup directory with no
containment, and `Store.Backup` opened it `O_CREAT|O_TRUNC` with no
`O_NOFOLLOW` at mode `0664` -- a world-readable dump of a datastore that holds
every stored WiFi PSK.

### Medium -- `safePathInAllowlist` had no symlink containment

The other entry point to the path allowlist, missed when `safeJoinUnderBase`
was hardened. It guards `HIDRunScript`, `HIDRunScriptJob` and `FSGetFileInfo`,
with `/tmp` among the bases they allow -- so a local user could plant a
symlink and have `HIDRunScript` read an arbitrary root-readable file and
**type it into the attached host**.

### Added: the console can answer "is anyone else signed in?"

Not a bug fix but a gap this audit exposed. The README, this file and five
code comments all asserted that the machine-local credential "appears in the
session list". There was no session list -- no RPC, no endpoint, no view. A
documented auditability property the device did not have.

`GET /api/auth/sessions` now lists who is signed in and
`POST /api/auth/sessions/revoke` ends one, with a "Who is signed in" dialog in
the console. The list carries **no tokens**: it is rendered in a browser, so a
list of live tokens would turn an audit view into a credential dump. An id is
a truncated SHA-256 of the token -- enough to name a session, not enough to be
one. Revoking the device's own credential re-issues it immediately rather than
being refused.

### Known and accepted, recorded so it is not rediscovered

Method names are matched **case-insensitively** by the JSON bridge, so
`GetLEDSettings` is also reachable as `getledsettings`. That is not a hole
today because nothing filters on the method name, but any future per-method
allowlist, read-only mode or audit log must lowercase before comparing.
`make access-control` prints this as a NOTE so it stays visible.

---

## Fixed in v0.3.0

### Critical — the device could not authenticate to itself

P4wnP1 drives itself through `P4wnP1_cli`. `servicestart.sh` — the fallback that
brings up the USB gadget, the DHCP servers and the WiFi access point when a
startup master template fails — is a shell script full of CLI calls, and so is
every user-written trigger action. When the API began requiring a bearer token
in v0.2.0, every one of them started failing:

```
servicestart.sh error: Error setting LED blink count 2:
  rpc error: code = Unauthenticated desc = missing authorization metadata
```

So a device whose startup template failed came up with **no network at all**,
and the fallback meant to rescue it was itself the broken part. There is no
password on disk for a script to log in with, and adding one would have been
worse.

Fixed by having the service log in as itself: it mints an **ordinary** session,
the same kind `auth login` returns, and writes that token to
`/run/p4wnp1/local.token` (mode `0600`, tmpfs). No bypass in the token
validator, no second credential type — a local script is an authenticated
client like any other, revocable and expiring like any other.
`KeepLocalTokenFresh` re-provisions at half the session TTL, because a trigger
can fire days after the last CLI call and because a password change revokes
every session including this one. `Store.SetPassword` now refuses the reserved
username so the session list cannot be made to lie about who called.

This escaped every gate because the console authenticates normally and looked
perfect throughout, and because nothing in the suite had ever read the
service's own log. Three smoke checks now do, all negative-tested by
reintroducing the bug.

### Medium — `StoreDeployedWifiSettings` marshalled a nil message

On any board without WiFi it answered `proto: Marshal called with nil`, because
the current-settings helper returns nil there and the result went straight into
`StoreWifiSettings`. It now reports `codes.Unavailable` with the reason. Found
by `make feature-test`, which was written for exactly this class of bug.

### Medium — USB unavailability was reported as an internal server error

Every USB RPC answered HTTP 500 on a board with no UDC bound, claiming a server
fault for an ordinary "this hardware is not here" condition — while the
equivalent WiFi condition correctly answered 503. `ParseGadgetState` now wraps
the `ErrUsbNotUsable` sentinel and the RPC layer maps it to `codes.Unavailable`.
Genuine bad requests still answer 400.

### Medium — the console could not name two of its own shipped reflexes

Two of the four trigger actions the device deploys at boot use the
`deploySettingsTemplate` action, which was missing from the console's action
list, so the Reflexes table described them as **"unrecognised"**. An operator
could see that something was armed at boot but not what it would do. The
builder can now create them too.

### Low — error toasts outlived the page that raised them

Error toasts live 12–20 seconds so there is time to read the hint and press the
action button. That is right while you stay put and wrong the moment you
navigate: "This device has no usable WiFi" from Radio sat on top of the
Keystrokes editor, covering the Run button. Toasts now clear on navigation, and
identical ones collapse instead of stacking.

### Low — the sign-in screen carried a typography colophon

A paragraph about typeface and palette choices, on the sign-in screen of a
red-team device. Removed.

### Low — the deployed-reflex table was headed with a Go type name

It read `DEPLOYED -- DEPLOYEDTRIGGERACTIONS`. That suffix is the set's internal
identifier.

---

## Fixed since v0.2.0

Found by a second audit pass, and by building verification that would have
caught them the first time. Each is now covered by a check in `make lint`.

### High — badger refused to open after an unclean shutdown

Opened with `Truncate` at its default of `false`. This device's normal power-off
is being pulled out of a USB port, so an unclean shutdown mid-write is the
expected case -- and without `Truncate` badger returns "Value log truncate
required to run DB" and refuses to open, so the template database never loads
and the service cannot start. `SyncWrites` was already on, so everything
acknowledged is on disk; truncating discards only what was in flight.

### High — the whole Keystrokes view was non-functional

Every RPC in the HIDScript path used a wrong request shape: `dir` sent as a
boolean, `content` instead of `data`, an absolute path where a relative one was
required, and a read with no `len`. You could not run a script, and loading a
stored one silently returned an empty string. Found and fixed, and
`tools/check-rpc-shapes.py` now checks every console payload against the proto.

### High — "Use at boot" erased the boot configuration

`SetStartupMasterTemplate` takes a `StringMessage`, whose field is `msg`. The
console sent `templateName`, which the JSON bridge discarded silently, so the
button set the boot default to an empty string instead of setting it.

### Medium — device events destroyed in-progress edits

A USB attach or detach re-rendered the whole Cable view, discarding half-entered
values. That event arrives exactly when someone is most likely to be mid-edit.

### Medium — the focus indicator failed WCAG 1.4.11

An alpha-blended gold measuring 1.27:1 to 1.94:1 against the surfaces it
appeared on, against a 3:1 requirement. Now solid, 5.19:1 to 11.34:1, and
enforced by the contrast suite.

### Medium — a self-recursive helper removed every table

`dataTable()` called itself, so Radio, Reflexes and Loadouts rendered their
first card and stopped. Valid syntax, so `node --check` passed it. This is why
`make check-render` exists.

### Medium — tables scrolled the whole page sideways on a phone

At 375px the tables measured 489px, so the page scrolled horizontally and every
other layout broke with it.

### Low — the job list never refreshed

A HIDScript job finishing produces no event the console can see, so the list
showed it running until you navigated away and back.

---

## Open

### High — no TLS

The console is served over plain HTTP, so bearer tokens ride in cleartext.
Acceptable over USB or the device's own WPA2 AP; not acceptable anywhere else.
A self-signed cert generated at first boot, with fingerprint verification on
first connect, is the intended fix.

### Medium — the shipped template database still contains the old defaults

The runtime guard above stops the bad PSK being broadcast, but `dist/db` itself
still carries the upstream SSID and PSK. The database should be regenerated.

### Medium — the emoji SSID crashes NetworkManager clients

Kali's build script (not this code) sets an emoji SSID; the URL-encoded
filename overflows the 255-char limit and crash-loops NetworkManager.
Upstream [#365](https://github.com/RoganDawes/P4wnP1_aloa/issues/365).
This fork defaults to ASCII.

### Medium — HIDScript runs unsandboxed

`otto` exposes a bridge into a VM that runs as root and is reachable from the
authenticated API. The sandbox boundary is not meaningful today; treat
HIDScript authorship as equivalent to root access.

### Medium — grpc-go is pinned to v1.38.0

The HTTP/2 stack underneath it (`golang.org/x/net`) has been bumped and carries
the Rapid Reset fix, but the grpc-go pin itself is old. A bump touches the whole
RPC layer.

### Medium — upstream [#354](https://github.com/RoganDawes/P4wnP1_aloa/issues/354): HID disabled at runtime

No reproducer documented. Needs a runtime trace on hardware.

### Low — KARMA and multi-SSID need Nexmon

Only the Pi Zero W's BCM43430A1 has a comparable firmware patch. Everything
else degrades to a normal access point.

---

## Not verified at all

**No physical Raspberry Pi was used.** Nothing above about runtime behaviour on
real hardware — boot, USB enumeration, keystroke injection, hostapd, Bluetooth
pairing, the trigger engine — has been observed. The fixes are derived from
reading the code and the kernel's documented behaviour. Boot one and check.
