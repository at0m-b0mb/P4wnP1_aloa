# changelog

## v0.8.0 -- 2026-10-14 -- security release

The first release since v0.7.1. Everything tagged between them (v0.7.2 to
v0.7.6) was never published, so if you are coming from v0.7.1 this also brings
the OLED panel work, the rebuilt web console and the USB mass-storage fixes.

Ten defects, from two audits and from driving a real device. Each is listed with
what an attacker could actually do, because "hardening" is not a description.

### Credentials were readable by anything on the device

The generated SSH password is written to `/boot/firmware/p4wnp1-credentials.txt`
-- deliberate, because a headless appliance has no other way to show it to you.
But vfat carries no Unix ownership, so the mount options set the mode for the
whole partition, and Raspberry Pi OS mounts it `defaults`, which is 0755.
Measured on a running device: `-rwxr-xr-x 1 root root`. Any unprivileged
process could read the password of an account holding `NOPASSWD:ALL` sudo --
no card, no physical access.

The boot partition now mounts `fmask=0177`, so every file on it is 0600. The
image build fails if that is missing, because the failure is silent: the image
boots, the file is correct, and only a mode nobody looks at is wrong.

Whoever holds the card can still read it. Nothing on the device can prevent
that, and the file still says so.

### A WiFi client could choose what the device typed into the attached host

`DHCP_LEASE_HOST` is the client's own DHCP option 12, and it was interpolated
raw into the HIDScript preamble: `var DHCP_LEASE_HOST='...'`. A hostname of
`x';type('...');var y='` closed the string and appended statements. An
unauthenticated party in radio range chose the keystrokes and the operator's own
trigger delivered them.

All nine interpolated values now go through a proper JavaScript string literal,
including `U+2028` and `U+2029`, which are line terminators in JavaScript and
end a statement from inside the quotes.

A stock device was not exposed, because no shipped template arms a DHCP-lease
trigger. But running a payload when a client takes a lease is what the feature
is for.

### Bluetooth paired on a PIN printed in this project's source

`1337`, in three places, with Discoverable, Bondable and the NAP network service
all on at every start -- and no equivalent of the guard WiFi already had. The
PIN was also written to the log on every pairing attempt, and `RequestPasskey`
returned the same constant.

A per-device PIN is now generated and substituted in the deploy path, which is
the one chokepoint the startup defaults, a restored template and the operator's
RPC all pass through.

This does not make legacy pairing strong: with Secure Simple Pairing off, the
link key derives from the PIN and a captured pairing exchange can be attacked
offline. It removes the part that needed no capture at all.

### Revoking a session did not stop a live event feed

A gRPC stream authenticated once, at open, and never again, so a revoked token
-- or a changed admin password -- kept receiving DHCP leases, HID job results
and trigger fires. Expiry did not stop it either. The control failed silently in
the operator's favour: the revoke returned 204 and the session list then omitted
it, so the console said the intruder was gone while the feed ran.

The JSON API's event stream had been fixed for exactly this. The gRPC sibling
now re-checks on the same 20-second interval.

### The rest

- **The WiFi passphrase was a command-line argument** to `wpa_passphrase`, so it
  sat in world-readable `/proc/<pid>/cmdline`. It goes on stdin now.
- **A failed hostapd config write was ignored** and the access point started on
  whatever was already at the fixed `/tmp` path -- the previous deployment's
  SSID or PSK, or an open network where WPA2 had just been asked for, while the
  console and panel reported the new settings.
- **One unauthenticated peer could open unlimited gRPC streams.** grpc-go leaves
  `MaxConcurrentStreams` effectively unlimited; it is 64 now, with keepalive
  bounds on connection age and idle time.
- **A login flood could lock the operator out.** `Login` ignored the request
  context, so a client that hung up still paid a full bcrypt and the failure
  delay, and nothing bounded the queue they were filling. The queue is capped
  and returns 503 rather than growing; abandoned logins stop costing.
- **Config files holding PSKs lived in the real `/tmp`.** The main unit now has
  `PrivateTmp=yes`, which the OLED and first-boot units already had.

### USB mass storage, found by using it

Four defects that no container could have caught, because a container has no
configfs:

- Mass storage could not be enabled **without an image at all** -- the empty
  backing path was a zero-length write, which configfs answers with EFAULT, and
  the whole deploy reverted. The shipped default is an empty path, so "a
  removable drive with no media" never worked.
- An empty LUN read back as `"."`, so the console reported a drive with no media
  as holding an image named dot.
- `EBUSY` on eject now says the attached **host** is holding the medium and must
  release it, instead of an errno that reads like a fault on the Pi.
- Copying payloads from a Mac left AppleDouble sidecars (`._name.js`) in the
  payload list, because the filter only looked at the extension.

Attaching, reading, hot-swapping and ejecting are all verified on hardware now.

### Payloads

Fifteen HIDScripts for Windows, macOS, Linux, Android and iOS, plus a keymap
verifier and an LED-based OS guess. **None has been run against a real host
yet**; every file says so in its own header.

Android and iOS are deliberately short. The shipped USB HID descriptor declares
only the Keyboard/Keypad usage page, so the Android HOME and BACK buttons, the
iOS system keys and every media key are not sendable by this device -- which is
what most published Android BadUSB material depends on.

### Tooling

- `tools/hidscript-check` -- the first gate that looks at the payloads. It
  compiles each with the real otto and validates every `press()` token and
  `layout()` name. It self-tests on every run, which is how it caught itself
  reading its own documentation as code.
- `tools/deploy-service.sh` -- install a rebuilt service over SSH without
  reflashing, so a fix can be verified on hardware in the sitting it was
  written.


## unreleased -- console usability and stability

A second audit pass over v0.2.0, and the verification that would have caught
each of these the first time.

### The Keystrokes view did not work at all

Every RPC in the HIDScript path used a wrong request shape -- `dir` sent as a
boolean, `content` instead of `data`, an absolute path where a relative one was
required, a read with no `len`, and a response field that does not exist. You
could not run a script, and loading a stored one silently gave you an empty
string. That is the core creative task on this device.

### "Use at boot" erased the boot configuration

`SetStartupMasterTemplate` takes a `StringMessage` whose field is `msg`; the
console sent `templateName`. The JSON bridge discards unknown fields, so the
request succeeded and set the boot default to an empty string.

### Other fixes

- A self-recursive `dataTable()` helper silently removed every table from
  Radio, Reflexes and Loadouts. Valid syntax; `node --check` passed it.
- Tables scrolled the whole page sideways at 375px.
- A USB attach or detach re-rendered the Cable view and discarded in-progress
  edits -- an event that arrives exactly when someone is mid-edit.
- The focus indicator measured 1.27:1 against a 3:1 WCAG requirement.
- The HID warning was skipped when Keystrokes was deep-linked.
- The job list never refreshed, so a finished job showed as running.
- badger opened without `Truncate`, so an unclean shutdown -- the normal way
  this device is powered off -- left it refusing to open.
- `rm -rf "$P4ROOT/$d"` in the image builder, unguarded, as root in a chroot.

### Learnability

- **Overview**, now the default view: a plain-language primer on what the
  device is, a live status strip, and the four concepts explained in the
  product's own vocabulary. Assembled entirely from existing RPCs.
- **Reflexes can be built from the UI.** It was read-only -- the device's
  distinguishing capability was the one thing the console would not let you
  touch. Create, arm, disarm and delete, with the form driven by the same table
  that builds the wire payload.
- **A HIDScript reference** on the page you write scripts on, listing all 24
  functions the VM exposes. They are injected at runtime and appear in no file
  an operator can open.
- **Saving scripts to the device.** There was no way to do it; anything written
  in the console was lost on navigation.
- **Each USB function explains itself**, including the one genuinely
  non-obvious thing: RNDIS is what Windows accepts, CDC ECM is what macOS and
  Linux accept, and the answer is usually both.
- **Errors are translated** into what went wrong and what to do, with a button
  to the view that fixes it.
- Real dialogs replace all 10 `window.prompt`/`alert`/`confirm` calls. Those
  could not validate, could not explain consequences, and `prompt()` is blocked
  outright in some mobile browsers -- and this console is often driven from a
  phone.
- Skip link, nav landmark, labelled log region, and tables that scroll in place
  instead of taking the page with them.

### Verification added

Each of these exists because a bug got past the previous gate.

- `make check-rpc` -- every console RPC payload against the `.proto`. Would
  have caught all five wrong-field bugs.
- `make check-render` -- all seven views rendered in jsdom. Catches runtime
  failures that `node --check` cannot see.
- `make check-js` -- parses the console JavaScript.
- `make mock` -- a shape-strict mock device, so the console can be developed
  with no Pi. The previous mock accepted whatever it was sent, which is what
  let four wrong field names ship.
- `make smoke` grew to 24 checks; the contrast suite to 21 pairings, now
  including the focus indicator.
- shellcheck widened to the image pipeline and tools.


## v0.2.0 -- 2026-10-06

The project did not work. This release makes it boot, adds a usable interface,
and ships flashable images.

### The reason it was broken

`CheckLibComposite` ended in an unconditional `log.Panic(err)` that fired even
on its own success path. On a stock Raspberry Pi OS cold boot it was reached
every time, inside `NewService()`, with no `recover()` anywhere in the path. No
gRPC, no web console, no access point. Because the `modprobe` succeeded just
before the panic, a manual restart made the device look healthy -- only cold
boots failed, which is why it presented as intermittent.

Four more showstoppers sat behind it: an exposed RPC whose body was
`panic("implement me")` (any authenticated request ended the process), a WiFi
constructor that panicked on an ordinary udev race, 20 of 21 CLI call sites
dialling without the bearer token so nearly every command failed
`Unauthenticated`, and an access point broadcasting a PSK published in this
repository.

### Added

- **A working web console** (`dist/www/app/`). Plain HTML/CSS/JS, no framework,
  no build step, 188KB with webfonts. Six views named for what the device does.
  The signature view shows, live, which USB functions a host enumerates.
- **A JSON API** (`/api/v1/`). All 82 unary RPCs exposed by reflection, plus
  the event stream as SSE. Encoding is protojson, because the service
  definition uses oneofs that `encoding/json` cannot round-trip.
- **A flashable image pipeline** (`image/`). Builds from an official Raspberry
  Pi OS Lite release for armhf and arm64, on macOS or Linux, everything
  privileged inside a container. Every image is mounted and verified before
  publishing.
- **arm64 support.** Pi Zero 2 W / 3 / 4 / 5. The build tags that blocked it
  were incidental, not a real dependency.
- **DuckyScript conversion** (`duckyscript/`, `P4wnP1_cli ducky convert`).
- **A WCAG contrast test** for the console palette (`make contrast`).

### Fixed

- The cold-boot panic, and `CheckBnep`'s `log.Fatal`.
- `ListenWiFiStateChanges` panicking; added gRPC panic-recovery interceptors.
- `/api/auth/changepw` requiring no bearer token -- a CSRF-able full takeover.
- `FSReadFile` allocating an attacker-supplied length.
- Reboot and shutdown never reaching their syscalls.
- The WiFi constructor panicking on a cold-boot race with udev.
- 20 of 21 CLI dial sites missing the bearer token.
- The access point using a PSK published in this repo.
- `install.sh` never configuring `dwc2`, so USB silently did nothing; and
  `policykit-1` aborting the whole install on Debian 13.
- A wrong-oneof type assertion that panicked on every group-receive trigger.
- Six vet defects and an unbounded-restart hazard in the systemd unit.
- Mobile layout overflow in the console.

### Changed

- The systemd unit gained `Restart=on-failure` with bounded rate limiting.
- The GopherJS client is retired; `/` redirects to the new console.
- `build_support/rpi0w-nexmon-p4wnp1-aloa.sh` removed -- it could no longer
  produce a working image, and a silently-broken image builder is worse than
  none.

### Not verified

No physical Raspberry Pi was used. Builds, unit tests, vet, image assembly and
the console (against a mock) are all verified by automation. Nothing about
real-hardware behaviour is.


## unreleased

- **Removed `.github/workflows/ci.yml`.** GitHub's anti-abuse layer kept
  flagging the workflow's git-clone step on this repo (the runner saw
  "Your account is suspended" 403s even though the account itself was
  fine), most likely because of the security-themed pre-built ARM
  binaries committed under `build/`. For a solo personal fork, local
  verification covers the same ground:
    - `make test`         -- run the `service/auth` unit tests
    - `make lint`         -- shellcheck + golangci-lint
    - `make build-armv6`  -- cross-compile the three binaries
  `.golangci.yml` stays (used by `make lint`). If you want CI back later,
  the workflow file is one `git revert e91f5e18^..HEAD` away in history.

## unreleased (earlier in this branch)

- **CLI auth subcommands (Milestone 3).** `P4wnP1_cli` now has built-in
  `auth login` / `auth logout` / `auth whoami` / `auth changepw` commands.
  Tokens cache at `~/.p4wnp1/token` (mode 0600, written atomically via
  tmpfile + rename) and a `grpc.PerRPCCredentials` implementation attaches
  the cached token to every existing CLI command's gRPC call. After auth
  hardening, this is what makes the CLI usable again. New files:
  `cli_client/auth_client.go` (HTTP client + token store) and
  `cli_client/cmd_auth.go` (cobra subcommands). `cli_client/rpc_client.go`
  picks up `tokenAuthCreds` + a `metadataContextWithToken` helper. INSTALL.md
  "CLI authentication" section rewritten with the real flow.
- **Pre-built `linux/arm/6` binaries refreshed in `build/`.** The committed
  `P4wnP1_service`, `P4wnP1_cli`, and the new `p4wnp1-hashpw` binaries are
  now built from the current source -- they include auth, the path-traversal
  fixes, and the M3 CLI subcommands. Previous binaries were upstream's 2020
  build which lacked all of that. `install.sh` is now usable on a clean
  `git clone` without any local toolchain.
- **`make build-armv6` Makefile target** for rebuilding the three binaries
  on any host with Go 1.24+ (works on macOS via cross-compile -- no Docker
  needed). Includes `-trimpath -ldflags='-s -w'` for reproducible stripped
  binaries.
- **Tests: `service/auth/` now has 25 unit tests** covering bcrypt
  round-trip, atomic persist, file mode 0600, JSON format, token
  randomness, expiry, sliding window, revocation, prune, login flow,
  ChangePassword session revocation, and timing-oracle defense on
  failed login. Tests run on every push via the new `go test` CI job.
- **Deps: `go mod tidy` upgraded transitive dependencies.** Most
  notably, `golang.org/x/net` went from `v0.0.0-20211112…` to `v0.49.0`,
  which **closes CVE-2023-44487 (HTTP/2 Rapid Reset DoS)** -- a known
  issue tracked in KNOWN_ISSUES.md. Also bumped `golang.org/x/crypto`
  to v0.47.0 (latest), `golang.org/x/sys` to v0.40.0. `go.mod` directive
  bumped from `go 1.16` to `go 1.24` (required by the new dep versions).
- **CI bumped to Go 1.24** to match the new module directive. Added a
  dedicated `go test (auth package)` job so the unit tests run on every
  push.
- **New: `dist/scripts/p4wnp1-healthcheck.sh`** -- post-install smoke
  test the operator runs on the Pi after first boot. Verifies binaries,
  data files, systemd units, firstboot completion, auth.json shape, port
  listeners, the HTTP `/api/auth/health` endpoint, and (optionally with
  `--login-test`) a real login round-trip using the credentials in
  `INITIAL_CREDENTIALS.txt`. Color-coded PASS/WARN/FAIL output.
- **Build-tag fix in `hid/AsyncOtto.go`:** added `// +build linux` so the
  file no longer trips `go vet` on macOS / Darwin (it references
  `haltirq` which is defined in `hid/controller.go`, a Linux-only file).
  Pre-existing upstream bug, surfaced by running vet from a Mac.

## unreleased (earlier in this branch)

- **Auth (M1+M2): gRPC API is no longer wide-open.** New `service/auth/`
  package introduces a real authentication layer:
    - bcrypt (cost 12) password storage in `/etc/p4wnp1/auth.json` (mode 0600);
    - opaque 32-byte random tokens with a 24-hour sliding-window TTL;
    - unary + stream gRPC interceptors that reject every RPC without a
      valid `Authorization: bearer <token>` metadata;
    - HTTP endpoints under `/api/auth/` for login / logout / whoami /
      changepw / health (these are the only public surface).
  Closes [KNOWN_ISSUES.md](KNOWN_ISSUES.md) item #6.
- **First-boot bootstrap of admin account.** The first-boot helper now
  generates a random 20-char admin password, bcrypt-hashes it via the new
  `p4wnp1-hashpw` binary, writes it to `/etc/p4wnp1/auth.json`, and surfaces
  the plaintext in `/root/INITIAL_CREDENTIALS.txt`. After bootstrap, the
  helper restarts `P4wnP1.service` so the running service picks up the new
  auth file (the service starts early in boot, before firstboot runs).
- **New binary `p4wnp1-hashpw`** at [cmd/p4wnp1-hashpw](cmd/p4wnp1-hashpw/main.go)
  for offline bcrypt + auth file management. Used by the firstboot helper
  and available for emergency password reset over SSH.
- **`golang.org/x/crypto`** added to go.mod (bcrypt).
- **Deprecation pass:** `io/ioutil` was deprecated in Go 1.16. Replaced
  `ioutil.ReadFile`, `ioutil.WriteFile`, `ioutil.TempDir`, `ioutil.TempFile`,
  and `ioutil.ReadAll` with their `os` / `io` equivalents across 13 files
  (service/, cli_client/, cmd/testhid, hid/, service/datastore). Skipped
  `ioutil.ReadDir` in [service/SubSysUSB.go](service/SubSysUSB.go) and
  [service/common.go](service/common.go) — the `os.ReadDir` replacement
  returns `[]DirEntry` instead of `[]FileInfo`, an API change risky to
  refactor without compile testing on hardware.
- **`context.WithTimeout` cancel-leak fixes** in three call sites flagged by
  `go vet -lostcancel`:
    - [service/rpc_server.go](service/rpc_server.go) `HIDRunScript` — captures
      `cancel` and `defer`s it.
    - [service/rpc_server.go](service/rpc_server.go) `HIDRunScriptJob` — runs
      `cancel()` from a watchdog goroutine after the context's `Done()` fires
      (background job, so plain `defer` is wrong).
    - [cmd/testhid/testhid.go](cmd/testhid/testhid.go) — captures `cancel`.
- **`exec.Command("which", ...)` -> `exec.LookPath`** in
  [service/common.go](service/common.go) `binaryAvailable` — portable, no
  subprocess spawn, same $PATH semantics.
- **`go.mod`**: `go 1.13` -> `go 1.16` (needed for `os.ReadFile`); removed the
  deprecated `github.com/golang/lint` indirect dependency (replaced by
  `golang.org/x/lint` years ago, only present as `// indirect`).
- **CI workflow**: bumped `setup-go@v5` to Go 1.21 for the service-side build.
  The legacy GopherJS web-client build (Go 1.12) is now documented as a
  separate stage in `build_support/Dockerfile`.
- **Repo URLs** in README, INSTALL, CHANGELOG, install.sh, and the Dockerfile
  now point at the active fork: <https://github.com/at0m-b0mb/P4wnP1_aloa>.
- **Pi OS Lite version pin:** INSTALL.md now names the specific Bookworm Lite
  armhf image (`2025-05-13-raspios-bookworm-armhf-lite.img.xz`) and explains
  why Bookworm is preferred over Trixie for the Pi Zero W (kernel 6.1 LTS
  supported until Dec 2026, well-tested on 512 MB devices).

## unreleased (earlier in this branch)

- **Easy install on Raspberry Pi OS Lite.** New top-level [`install.sh`](install.sh)
  automates the full install on a stock Raspberry Pi OS Lite (Bookworm armhf)
  image: apt deps, binary placement, systemd units, conflicting-service
  disable, branding capture (`--ssid` / `--psk`). Defaults: SSID `HackProKP`,
  randomly generated 16-char PSK persisted to the first-boot credentials file.
  Pi OS Lite is now the recommended install path (Path A in INSTALL.md);
  the older Kali prebuilt image path is documented as Path B.
- **Security: path-traversal fixes in 4 gRPC RPCs.** Patched arbitrary file
  read/write/stat via `HIDRunScript`, `HIDRunScriptJob`, `FSReadFile`,
  `FSWriteFile`, and `FSGetFileInfo` in [service/rpc_server.go](service/rpc_server.go).
  Two new helpers in [service/common.go](service/common.go) (`safeJoinUnderBase`
  and `safePathInAllowlist`) enforce that user-supplied filenames stay under
  the explicitly allowed base directories (`/usr/local/P4wnP1/...`, `/tmp`).
  These bugs were exploitable against the no-auth gRPC API: anyone on the AP
  could read `/etc/shadow` or write executable bash scripts as root. Tracked
  in [KNOWN_ISSUES.md](KNOWN_ISSUES.md) findings #1, #2, and #4.
- **Branding: default WiFi SSID + PSK.** Changed in [service/defaults.go](service/defaults.go)
  from `P4wnP1` / `MaMe82-P4wnP1` to `HackProKP` / `HackProKP-changeme`. The
  source default is still meant to be overridden per device (by `install.sh`
  or via the web client); the new value is ASCII-only so it avoids the
  NetworkManager emoji-SSID crash (upstream #365).
- **Security: first-boot defaults** are no longer the shared `toor` / shared SSH
  host keys that prebuilt images ship with. A new `p4wnp1-firstboot.service`
  oneshot unit runs once at first boot and:
    - rotates the root password to a random 20-character secret;
    - regenerates SSH host keys (prebuilt images otherwise share one key);
    - writes the new credentials + SSH fingerprints to
      `/root/INITIAL_CREDENTIALS.txt` (mode 0600) and to the systemd journal.
  The WiFi AP PSK and Bluetooth PIN are *not* changed automatically (they live
  in the badger DB and need a known-good CLI invocation); the credentials file
  loudly flags that they remain on shared defaults and points the operator at
  the web client to rotate them. Delete `/var/lib/p4wnp1/firstboot.done` to
  force a re-run.
- **Docs:** `README.md` rewritten for clarity (1599 -> ~120 lines); new
  `INSTALL.md` covering three concrete install paths including the current
  state of the Kali RPi Zero W image lineup; original tutorial preserved at
  `docs/TUTORIAL.md`.
- **Quality:** GitHub Actions CI added (shellcheck, go vet, gofmt informational,
  armv6 cross-compile attempt); `.golangci.yml` with a conservative starting
  linter set; `make help`, `make lint`, `make test` targets.

## v0.1.1-beta

- fix #81: 100 percent CPU load in WiFI STA mode
- fix: Italian layout `it`
- fix: UK layout `gb`
- fix: French layout `fr`
- fix #38: file permission
- added Finnish `fi` and Swedish `sv` layout (same)
- added German(Switzerland) layout `ch`
- added French(Belgian) layout `be`
