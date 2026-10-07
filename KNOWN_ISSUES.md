# Known issues

Last refreshed: 2026-10-07, for v0.3.0. Two audit passes (96 agents, then 57)
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
