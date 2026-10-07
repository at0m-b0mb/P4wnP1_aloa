# Known issues

Last refreshed: 2026-10-06, after a 96-agent audit of the codebase in which
every high-severity finding was independently re-checked by a second reviewer
before being accepted.

Severity: **Critical** — boot-blocking or exploitable. **High** — blocks a
common workflow. **Medium** — confusing or stale. **Low** — cosmetic.

---

## Fixed in this pass

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

## Open

### High — no TLS

The console is served over plain HTTP, so bearer tokens ride in cleartext.
Acceptable over USB or the device's own WPA2 AP; not acceptable anywhere else.
A self-signed cert generated at first boot, with fingerprint verification on
first connect, is the intended fix.

### High — badger is not crash-safe as configured

`dist/db` uses badger v1.5.5 (2018) with default open options. This device is
normally powered off by being pulled out of a USB port, so an unclean shutdown
mid-write is the *normal* case, not an edge case. A truncated value log can
lose recent template changes or fail to open. Needs `Truncate: true` and a
sync-on-write policy at minimum.

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
