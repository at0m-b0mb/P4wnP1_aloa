# P4wnP1 A.L.O.A.

**A Little Offensive Appliance** — turns a Raspberry Pi Zero W into a flexible, low-cost platform for pentesting, red-teaming, and physical-engagement work.

P4wnP1 A.L.O.A. (originally by [MaMe82](https://github.com/mame82)) emulates configurable USB devices (keyboard, mouse, ethernet, mass-storage, serial, CD-ROM), runs scripted HID injection, and exposes WiFi / Bluetooth / Ethernet stacks over a single gRPC API with both a CLI and a browser-based control panel.

> ⚠️ **Project status.** Upstream code is dormant (last source release: `v0.1.1-beta`, 2020), but Kali Linux still ships a current prebuilt image — `kali-linux-2026.1-raspberry-pi-zero-w-p4wnp1-aloa-armel.img.xz`. The maintained code fork is [RoganDawes/P4wnP1_aloa](https://github.com/RoganDawes/P4wnP1_aloa) (issues from 2024–2026 are actively reported there, though merges are infrequent). See [INSTALL.md](INSTALL.md) and [KNOWN_ISSUES.md](KNOWN_ISSUES.md).

> ⚠️ **Authorized use only.** Read [DISCLAIMER.md](DISCLAIMER.md). Don't point this at devices or networks you don't own or have written permission to test.

---

## What it does

- **USB device emulation** — RNDIS / CDC ECM ethernet, mass-storage (flash drive or CD-ROM), HID keyboard + mouse, serial. Hot-reconfigurable without reboot.
- **HIDScript** — JavaScript-based keystroke/mouse-injection language with up to 8 parallel jobs, LED-feedback branching, layout switching, and absolute mouse positioning on Windows targets.
- **WiFi** — Access Point mode, station mode, automatic failover, optional KARMA (Nexmon firmware) and multi-SSID beaconing.
- **Bluetooth** — full Bluez stack control; NAP (network access point) with PIN or SSP pairing; high-speed (802.11) data rates.
- **Networking** — per-interface DHCP server/client, manual IP, persistent templates.
- **TriggerActions** — event-driven automation. React to USB plug events, SSID changes, GPIO state, Bluetooth peers, etc., with reusable templates instead of static bash payloads.
- **Two control surfaces**:
  - **CLI** (`P4wnP1_cli`) — local or remote over gRPC. Compiles for any major OS.
  - **Web client** — single-page app served on port `8000`, full feature parity plus HIDScript editor, job manager, and template UI.

A deeper tour of HIDScript, the CLI, and TriggerActions lives in [docs/TUTORIAL.md](docs/TUTORIAL.md).

---

## Hardware

Designed and tested on **Raspberry Pi Zero W (BCM43430A1)** only. The **Pi Zero 2 W is not supported** — the prebuilt Kali image will not boot on it, and the Nexmon firmware patches are chip-specific (the v2 uses a different WiFi chip). Tracking issue: [upstream #307](https://github.com/RoganDawes/P4wnP1_aloa/issues/307).

You also need:

- A microSD card (8 GB minimum, 16 GB+ recommended).
- A USB-A cable / OTG adapter to connect the Pi to a target host.
- (Recommended) An external 5 V supply so the Pi can stay powered when detached from the target — required for the connect/disconnect HID tricks documented in the tutorial.

---

## Quick start (recommended path)

```sh
# 1. Flash Raspberry Pi OS Lite (32-bit/armhf, Bookworm) with Raspberry Pi Imager.
#    In the advanced settings, set SSH on + WiFi creds + a non-default username.
# 2. SSH to the Pi.
git clone https://github.com/at0m-b0mb/P4wnP1_aloa ~/P4wnP1_aloa
cd ~/P4wnP1_aloa
# (drop pre-built binaries into ./build/ — see INSTALL.md A.2)
sudo ./install.sh                          # or: --ssid Foo --psk 'bar'
# 3. After the auto-reboot, connect to the WiFi AP "HackProKP" and:
ssh root@172.24.0.1                        # initial password in /root/INITIAL_CREDENTIALS.txt
```

Three full install paths (Pi OS Lite, official Kali image, manual layered install) are detailed in [INSTALL.md](INSTALL.md). After flashing:

1. **First boot** takes ~30 seconds. The Pi appears as a USB ethernet device *and* spins up a WiFi access point.
2. **Connect** using one of:

   | Channel | SSID / Device | Default creds | P4wnP1 IP |
   |---|---|---|---|
   | WiFi (Path A — Pi OS Lite + installer) | `HackProKP` (or your `--ssid`) | random per install (in `/root/INITIAL_CREDENTIALS.txt`) | `172.24.0.1` |
   | WiFi (Path B — Kali image) | `💥🖥💥 Ⓟ➃ⓌⓃ🅟❶` | PSK `MaMe82-P4wnP1` | `172.24.0.1` |
   | USB ethernet (RNDIS / CDC ECM) | — | — | `172.16.0.1` |
   | Bluetooth NAP | device `P4wnP1` | PIN `1337` | `172.26.0.1` |
   | SSH | any of the above | random per install (Path A) or `root`/`toor` / `kali`/`kali` (Path B) | — |

   The emoji SSID in the Kali image is known to crash NetworkManager/Netplan on some Linux clients — see [KNOWN_ISSUES.md](KNOWN_ISSUES.md#-upstream-365-emoji-ssid-crashes-networkmanager-on-linux-clients).

5. **Open the web client** at `http://172.24.0.1:8000` (or any of the IPs above on port 8000).
6. **Or SSH in** and run `P4wnP1_cli` for the command-line tool.

> 🔐 **On first boot, the new `p4wnp1-firstboot.service` rotates the SSH root password and SSH host keys automatically.** Find the generated SSH password in `/root/INITIAL_CREDENTIALS.txt` (mode 0600) or via `journalctl -t p4wnp1-firstboot`. The WiFi PSK and Bluetooth PIN still need to be rotated manually — see the [Security checklist](#security-checklist).

A first concrete task — typing "Hello world" into a USB-attached host — is walked through in [docs/TUTORIAL.md](docs/TUTORIAL.md#run-a-keystroke-injection).

---

## Security checklist

As of the 2026 hardening pass, the gRPC API **does** have authentication. Every gRPC and HTTP request requires a bearer token; without one, the server returns gRPC `Unauthenticated` / HTTP 401. The web client speaks HTTP (no TLS yet) so credentials travel in cleartext on the local network — restrict your AP reachability accordingly.

Apply this checklist before deploying:

- [x] **Automated on first boot** by `p4wnp1-firstboot.service`:
    - Root SSH password rotated to a random 20-char secret.
    - SSH host keys regenerated per device.
    - Web client admin account bootstrapped with a random 20-char password, bcrypt-hashed into `/etc/p4wnp1/auth.json` (mode 0600).
    - All credentials written to `/root/INITIAL_CREDENTIALS.txt` (mode 0600) and surfaced in `journalctl -t p4wnp1-firstboot`.
- [ ] Change the WiFi AP PSK (web client → *WiFi* → *Settings* → save as new template).
- [ ] Change the Bluetooth PIN, or disable Bluetooth entirely if unused.
- [ ] Change the admin password from its first-boot random via web UI → Settings → Change Password (or `POST /api/auth/changepw`).
- [ ] If exposing the web client outside a trusted network, restrict access with `iptables` / `nftables` and consider an HTTPS reverse proxy in front (TLS is a roadmap item).
- [ ] Disable any USB function (mass storage, ECM, RNDIS) you don't actually need.
- [ ] After you've copied initial credentials to a password manager, shred the file: `shred -u /root/INITIAL_CREDENTIALS.txt`.

### Authentication architecture (short version)

- **Password storage:** bcrypt (cost 12) in `/etc/p4wnp1/auth.json`, mode 0600.
- **Tokens:** opaque 32-byte random, base64url-encoded. In-memory session map only — service restart logs everyone out.
- **TTL:** 24 hours, sliding window (each successful auth refreshes expiry).
- **gRPC:** `Authorization: bearer <token>` in metadata. Unary + stream interceptors reject anything without one.
- **HTTP:** `Authorization: Bearer <token>` header. Routes under `/api/auth/` (login, logout, whoami, changepw, health) are public; everything else needs a token.
- **CLI:** see [INSTALL.md → CLI authentication](INSTALL.md#cli-authentication) for the workaround until a `P4wnP1_cli login` subcommand lands.

Full architecture diagram in [service/auth/auth.go](service/auth/auth.go).

---

## Building from source

The build chain is dated (Go 1.13, GopherJS, Go pre-modules patterns). Working build paths are documented in [INSTALL.md → Building from source](INSTALL.md#building-from-source). A short summary:

```sh
# In the project root, on a Linux host with Go 1.12 and gopherjs installed
cd build_support && ./build.sh
```

Outputs `P4wnP1_service`, `P4wnP1_cli`, and `webapp.js` into `build/`. The Makefile's `installkali` target copies them into place on a running Kali Pi.

A reproducible Docker build is also provided (`build_support/Dockerfile`) and is the recommended path on macOS or non-Linux hosts.

---

## Project layout

```
cmd/                 Entrypoints (P4wnP1_service, P4wnP1_cli)
service/             Core daemon — USB, HID, WiFi, BT, network, triggers
cli_client/          Cobra-based CLI commands
web_client/          GopherJS web app (Quasar / hvue / mvuex)
common/, common_web/ Shared helpers
proto/               gRPC protocol definitions
hid/                 USB HID gadget logic
netlink/, mnetlink/, mgenetlink/   Netlink bindings
dist/                Files installed to /usr/local/P4wnP1/ (www, keymaps, scripts, db)
build_support/       Image build script + Dockerfile
docs/                Tutorial and extended docs
```

---

## Links

- **Install guide:** [INSTALL.md](INSTALL.md)
- **Tutorial & HIDScript reference:** [docs/TUTORIAL.md](docs/TUTORIAL.md)
- **Known issues & bug list:** [KNOWN_ISSUES.md](KNOWN_ISSUES.md)
- **Changelog:** [CHANGELOG.md](CHANGELOG.md)
- **Disclaimer:** [DISCLAIMER.md](DISCLAIMER.md)
- **License:** GPL-3.0 — see [LICENSE](LICENSE)
- **This fork:** <https://github.com/at0m-b0mb/P4wnP1_aloa>
- **Upstream maintained fork:** <https://github.com/RoganDawes/P4wnP1_aloa>
- **Original project:** <https://github.com/mame82/P4wnP1_aloa>
