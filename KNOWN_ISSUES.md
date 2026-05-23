# Known issues & verified bugs

Compiled from a review of the local code, upstream GitHub issues at [RoganDawes/P4wnP1_aloa](https://github.com/RoganDawes/P4wnP1_aloa), the official Kali downloads, and dependency vulnerability databases. Last refreshed: 2026-05-23.

Severity legend:

- 🔴 **Critical** — exploitable security issue or boot-blocking bug.
- 🟠 **High** — blocks common workflows or risks user data.
- 🟡 **Medium** — confusing UX, intermittent breakage, or stale tooling.
- 🟢 **Low** — cosmetic, future-proofing.

---

## 🔴 Code-level security bugs (found by local review)

### ✅ 1. Unauthenticated arbitrary file read via `HIDRunScript` / `HIDRunScriptJob` — **fixed**

- **Where:** [service/rpc_server.go:795](service/rpc_server.go#L795), [service/rpc_server.go:824](service/rpc_server.go#L824)
- **Was:** Both RPCs accepted a `ScriptPath` field from the gRPC request and passed it unmodified to `ioutil.ReadFile` as root, reachable over the no-auth gRPC API.
- **Fix applied:** Both call sites now route `ScriptPath` through `safePathInAllowlist` (in [service/common.go](service/common.go)) which rejects anything outside `/usr/local/P4wnP1/HIDScripts` or `/tmp`. Untested on hardware; verify by trying `P4wnP1_cli hid run -r /etc/shadow` and confirming you get "path is outside the allowed directories".
- **Residual risk:** The gRPC API itself is still unauthenticated, so the auth slice is still required to close the broader attack surface.

### ✅ 2. Self-acknowledged path traversal in `FSReadFile` / `FSWriteFile` — **fixed**

- **Where:** Both RPCs in [service/rpc_server.go](service/rpc_server.go), plus a new shared helper `resolveAccessibleFolder` to keep the two call sites in sync.
- **Was:** `req.Filename` was concatenated onto a base path without traversal checks; the maintainer's own `//ToDo:` comment acknowledged it.
- **Fix applied:** Both RPCs now go through `safeJoinUnderBase(base, filename)` which rejects absolute filenames, `..` traversal, and any path that resolves outside the chosen base.
- **Residual risk:** Same as #1 — the API is still unauthenticated.

### 🟠 3. Hard-coded WiFi PSK in source defaults — **partial fix**

- **Where:** [service/defaults.go:193](service/defaults.go#L193)
- **Was:** `PSK: "MaMe82-P4wnP1"` — every device shipped with the same WiFi password.
- **Fix applied:** Source default is now `HackProKP-changeme` (still bad, but at least different from the well-known one). The new [`install.sh`](install.sh) captures a per-device PSK at install time (random by default, or `--psk` to set explicitly) and writes it to `/etc/p4wnp1/initial.conf` + the credentials file.
- **Residual:** The install-time PSK is not pushed into the running service's badger DB automatically. The operator has to apply it once via the web client on first login. Documented in INSTALL.md.

### ✅ 4. Arbitrary `os.Stat` over unauthenticated API — **fixed**

- **Where:** `FSGetFileInfo` in [service/rpc_server.go](service/rpc_server.go).
- **Was:** Direct `os.Stat(req.Path)` with no allowlist — full filesystem enumeration.
- **Fix applied:** Path now restricted to `common.PATH_ROOT` (`/usr/local/P4wnP1/...`) or `/tmp` via `safePathInAllowlist`. Same residual risk as #1.

### 🟠 5. No TLS on web client; cookies in cleartext (once auth lands)

- **Where:** [service/rpc_server.go:1037](service/rpc_server.go#L1037)
- **What:** The web client is served over plain HTTP on port 8000. Once auth tokens exist, they ride in cleartext over a WPA2-PSK-protected (or fully open Bluetooth) network.
- **Fix:** Generate a self-signed cert on first boot and serve gRPC-web over HTTPS. Trade-off: cert pinning + manual fingerprint verification on first connect, since CA-signed certs aren't realistic on a device with no DNS name.

---

## 🟠 Upstream bugs verified via GitHub

### 🟠 [#365] Emoji SSID crashes NetworkManager on Linux clients

- **Link:** <https://github.com/RoganDawes/P4wnP1_aloa/issues/365> (filed 2026-03-29)
- **What:** The Kali prebuilt image ships with WiFi SSID `💥🖥💥 Ⓟ➃ⓌⓃ🅟❶` (configured in Kali's build script, not the P4wnP1 code itself — the code defaults to plain `P4wnP1`). When a Linux client using NetworkManager + Netplan saves the connection, the URL-encoded filename overflows the 255-char filesystem limit, triggering an assertion in the keyfile writer and causing a crash loop.
- **Workaround:** Manually remove the malformed file from `/etc/netplan/` and `/run/NetworkManager/system-connections/`, then restart NetworkManager. Or: connect from a non-NetworkManager client (Android, iOS, macOS, Windows) for first login and rename the SSID via the web UI.
- **Fix:** Lobby Kali to change the default SSID in [their build script](https://gitlab.com/kalilinux/build-scripts/kali-arm/-/blob/main/raspberry-pi-zero-w-p4wnp1-aloa.sh) to ASCII-only.

### 🟠 [#363] WiFi AP fails to start after fresh boot

- **Link:** <https://github.com/RoganDawes/P4wnP1_aloa/issues/363> (filed 2025-12-18)
- **What:** AP does not come up on some Pis after first boot; the reporter asks how to restart it without re-flashing.
- **Workaround:** `P4wnP1_cli wifi deploy <ap-template>` after SSHing in via USB ethernet. Or: `systemctl restart P4wnP1`.

### 🟠 [#354] P4wnP1 disables HID at runtime

- **Link:** <https://github.com/RoganDawes/P4wnP1_aloa/issues/354> (filed 2024-11-22)
- **What:** USB HID becomes unusable after some sequence of USB function changes. No reproducer documented.
- **Fix:** Needs runtime trace on hardware — possibly related to libcomposite reload at [service/SubSysUSB.go:326](service/SubSysUSB.go#L326).

### 🟡 [#355] Default SSH credentials reported as incorrect

- **Link:** <https://github.com/RoganDawes/P4wnP1_aloa/issues/355> (filed 2024-11-24)
- **What:** Bare report of `root` / `toor` not working. No further detail. Likely cause: user tried `root`/`toor` against a Kali build where the default user is `kali`/`kali` (Kali changed the default in 2020.1+).
- **Fix:** Documentation — both credential pairs are now listed in [INSTALL.md](INSTALL.md).

### 🟡 [#362] Pi Zero 2 W support

- **Link:** <https://github.com/RoganDawes/P4wnP1_aloa/issues/362> (filed 2025-12-14)
- **What:** Image does not boot on the Pi Zero 2 W. Confirmed by Kali docs as expected behavior.
- **Fix:** Genuinely hard — the Zero 2 W uses a different WiFi chip (BCM43436) that doesn't have a Nexmon firmware patch comparable to the BCM43430A1. Removing the KARMA + multi-SSID features would let it work, but that loses the project's distinguishing capabilities.

---

## 🟡 Stale dependencies (no live CVE in this version, but old)

### 🟡 `golang.org/x/net v0.0.0-20211112202133-69e39bad7dc2`

- **CVE-2023-44487** (HTTP/2 Rapid Reset DoS) — fixed in `v0.17.0`; **this pin predates the fix.**
- **Impact:** A network attacker that can reach the gRPC port can DoS the service by abusing HTTP/2 stream resets. In practice, the service is single-purpose on a Pi Zero and an attacker on the AP has higher-impact options (see code-level findings above), so this is medium risk in context.
- **Fix:** Bump in [go.mod](go.mod). Pairs with the Go-build modernization slice.

### 🟡 `google.golang.org/grpc v1.38.0`

- Same Rapid Reset family of issues; mitigated in grpc-go v1.56.3 / v1.58.3 / v1.59.0.
- **Fix:** Bump.

### 🟢 `github.com/dgraph-io/badger v1.5.5-0.20181020...`

- 2018 version of badger; no known CVEs against v1.x, but multiple bug fixes since (concurrent-iterator safety, value-log corruption on crash).
- **Impact:** Low — used as a small embedded KV for templates. Power loss during write could lose recent template changes.
- **Fix:** Migration to badger v3/v4 is non-trivial (API changes); not worth the churn for this use case.

### 🟢 `github.com/robertkrimen/otto`

- The JavaScript interpreter that runs HIDScript. Pre-ES6 only, and the HIDScript sandbox boundary depends on otto's strictness. No CVEs filed.
- **Impact:** HIDScript already runs as root and is fed by the (no-auth) gRPC API, so the sandbox is moot today. Once auth exists this becomes a real consideration.

### 🟡 `gopherjs v1.18.0-beta2` + the whole GopherJS toolchain

- GopherJS development has slowed and it requires Go 1.12 specifically. This is the single biggest blocker to modernizing the Go toolchain.
- **Fix:** Replace with a Vue 3 / TypeScript SPA over gRPC-web. ~40+ hours, tracked under the Go-build modernization slice.

---

## 🟡 Tooling / build issues

### 🟡 The in-repo `build_support/rpi0w-nexmon-p4wnp1-aloa.sh` is stale

- It targets the older Kali armel pipeline and assumes Python 2. Kali now maintains their own copy at [gitlab.com/kalilinux/build-scripts/kali-arm](https://gitlab.com/kalilinux/build-scripts/kali-arm/-/blob/main/raspberry-pi-zero-w-p4wnp1-aloa.sh) with Python 3.
- **Fix:** Either (a) delete the in-repo script and link to the Kali one, or (b) sync the in-repo script with Kali's current version. Recommendation: (a) — there's no value in maintaining a divergent copy.

### 🟡 `go.mod` pins `go 1.13`; GopherJS pins Go 1.12

- This means the codebase doesn't compile on any modern Go install without `gvm` or a Docker pin.
- **Fix:** Two-step — first bump just `go.mod` to a modern Go for the service/CLI binaries (they don't need GopherJS), then plan the GopherJS migration separately.

### 🟢 Hand-rolled trailing-newline `\n` in shell scripts via `echo`

- Pre-existing `dist/scripts/trigger-aware.sh` uses `echo "\t..."` which isn't portable (shellcheck SC2028).
- **Fix:** One-line `printf` replacements. Low value; left alone in this pass.

---

## How to use this file

This file is a **prioritized backlog**, not a TODO list. If you're about to make a fix, file an issue and link this section so the conversation has context. The Critical-tagged items in particular are not theoretical — anyone on the AP today can extract `/etc/shadow` from a default-install P4wnP1.
