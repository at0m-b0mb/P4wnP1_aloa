# changelog

## unreleased

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
