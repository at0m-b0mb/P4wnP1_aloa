# Installing P4wnP1 A.L.O.A.

Three install paths for a Raspberry Pi Zero W. Pick the one that matches what you have and what you trust.

|  | Path | Best for | Effort |
|---|---|---|---|
| 🟢 | **[Path A — Raspberry Pi OS Lite + `install.sh`](#path-a--raspberry-pi-os-lite--installsh-recommended)** | New installs in 2026; modern kernel; layered Kali tools optional. | ~15 min |
| 🟡 | [Path B — Official Kali prebuilt image](#path-b--official-kali-prebuilt-image) | Want Kali tools out-of-the-box, accept older P4wnP1 baseline. | ~10 min |
| ⚪ | [Path C — Manual install on existing OS](#path-c--manual-install-on-existing-os) | You already have a Pi running something else. | ~25 min |

> 📌 **What changed.** Kali still ships a P4wnP1 image (`kali-linux-2026.1-raspberry-pi-zero-w-p4wnp1-aloa-armel.img.xz`), but the P4wnP1 service binary inside it has not been updated since the upstream `v0.1.1-beta` release in 2020 and the default WiFi SSID in that image (`💥🖥💥 Ⓟ➃ⓌⓃ🅟❶`) is known to crash NetworkManager on Linux clients (upstream [#365](https://github.com/RoganDawes/P4wnP1_aloa/issues/365)). **For a fresh build today, Path A — Raspberry Pi OS Lite (Bookworm armhf) + this repo's `install.sh` — is the cleanest option**, with current kernel/firmware, ASCII-only SSID by default, and the [security hardening fixes](KNOWN_ISSUES.md) applied at install time.

---

## Hardware checklist

- [ ] **Raspberry Pi Zero W (BCM43430A1)**. The Pi Zero **2** W is *not* supported — different WiFi chip, no Nexmon firmware patches.
- [ ] **microSD card**, 8 GB minimum, class 10 or better, 16 GB+ recommended.
- [ ] **microSD card reader** on your host machine.
- [ ] **USB-A male to micro-USB cable** (data-capable, not charge-only) for the target connection.
- [ ] *(Recommended)* A second micro-USB cable + 5 V supply so the Pi stays powered when detached from a target — required for the connect/disconnect HID workflows.
- [ ] A laptop / phone with WiFi for the first-boot connection.

---

## Path A — Raspberry Pi OS Lite + `install.sh` (recommended)

This is the simplest, most modern setup. You flash a clean Raspberry Pi OS Lite image, SSH in, and run a one-line installer that handles everything (apt deps, binaries, systemd units, branding, first-boot credential rotation).

### A.1 Flash Raspberry Pi OS Lite (32-bit / armhf)

1. Download **Raspberry Pi OS Lite (32-bit)** from <https://www.raspberrypi.com/software/operating-systems/>.

   The current Bookworm Lite armhf image (verified May 2026) is `2025-05-13-raspios-bookworm-armhf-lite.img.xz` — direct link via <https://downloads.raspberrypi.com/raspios_lite_armhf/images/>.

   > Pick **32-bit Lite**, not 64-bit (the Pi Zero W's ARMv6 CPU doesn't support 64-bit Linux). "Lite" means headless / no desktop. Bookworm is preferred over Trixie for the Pi Zero W today — its kernel 6.1 LTS is supported until December 2026 and is well-tested on the 512 MB Zero W.

2. Flash it with [Raspberry Pi Imager](https://www.raspberrypi.com/software/). In the **gear / advanced settings** before flashing:
   - **Enable SSH** with a strong password (don't reuse a password you care about — you'll rotate it shortly anyway).
   - **Set username**: `kp` (or whatever you want — not `pi`).
   - **Set hostname**: `hackprokp` (or whatever).
   - **WiFi credentials**: your home network — the Pi needs internet on first boot to apt-install dependencies.
   - **Locale & timezone**: yours.

3. Boot the Pi. Wait ~30 seconds. Find it on your network:
   ```sh
   ping hackprokp.local           # if your network supports mDNS (most do)
   # or use your router's DHCP lease list to find its IP
   ```

### A.2 Get the build artefacts onto the Pi

You need the compiled binaries + the repo tree. Two options:

**Option 1 — clone + pre-built binaries (fastest).** If you have a release tarball with `P4wnP1_service`, `P4wnP1_cli`, `webapp.js`, `webapp.js.map`:

```sh
ssh kp@hackprokp.local
git clone https://github.com/at0m-b0mb/P4wnP1_aloa ~/P4wnP1_aloa
# copy your pre-built binaries into ~/P4wnP1_aloa/build/
```

**Option 2 — build from source on a Linux/macOS host first, scp to the Pi.** See [Building from source](#building-from-source) below.

### A.3 Run the installer

```sh
ssh kp@hackprokp.local
cd ~/P4wnP1_aloa
sudo ./install.sh
```

The installer will:
- Install all apt dependencies (`hostapd`, `dnsmasq`, `bluez`, `haveged`, ...).
- Copy binaries + data into `/usr/local/`.
- Install both systemd units (`P4wnP1.service` + `p4wnp1-firstboot.service`).
- Disable conflicting network services.
- Write your chosen SSID + PSK to `/etc/p4wnp1/initial.conf`.
- Reboot.

To customise the branding at install time:

```sh
sudo ./install.sh --ssid 'HackProKP-pentest' --psk 'mystrongPSK_2026!'
sudo ./install.sh --skip-reboot              # if you want to inspect before rebooting
```

Defaults: SSID `HackProKP`, PSK randomly generated and saved in the credentials file.

### A.4 First boot

On the next boot, `p4wnp1-firstboot.service` does three things:

1. Rotates the **SSH root password** to a random 20-char secret and regenerates SSH host keys.
2. **Bootstraps the web UI admin account** — generates a random 20-char password, runs it through `p4wnp1-hashpw` to produce a bcrypt hash, and writes it to `/etc/p4wnp1/auth.json` (mode 0600). Without this file the P4wnP1 service rejects every gRPC/HTTP request with HTTP 401, so this step is mandatory.
3. Writes both plaintext passwords to `/root/INITIAL_CREDENTIALS.txt` (mode 0600) alongside the SSH host fingerprints and the WiFi SSID/PSK chosen at install time.

Connect to your new AP and read the credentials:

```sh
# Connect to WiFi SSID "HackProKP" (or whatever you chose) with the PSK
ssh root@172.24.0.1     # use the random root password just generated
cat /root/INITIAL_CREDENTIALS.txt
```

`INITIAL_CREDENTIALS.txt` contains, in order:

- SSH root password + SSH host fingerprints
- Web client admin username (`admin`) + password
- WiFi SSID + PSK
- Bluetooth PIN (still on shared default — change in the web UI)

Once you've stored everything in a password manager, shred the file:

```sh
shred -u /root/INITIAL_CREDENTIALS.txt
```

Continue to [First boot](#first-boot) for what to do next.

---

## Path B — Official Kali prebuilt image

1. Download the current image from <https://www.kali.org/get-kali/#kali-arm>. The file is named like:
   ```
   kali-linux-2026.1-raspberry-pi-zero-w-p4wnp1-aloa-armel.img.xz
   ```
2. Verify the SHA256 against the value on the Kali downloads page:
   ```sh
   sha256sum kali-linux-2026.1-raspberry-pi-zero-w-p4wnp1-aloa-armel.img.xz
   ```
3. Decompress and flash in one pipe (saves disk space):
   ```sh
   xzcat kali-linux-*-raspberry-pi-zero-w-p4wnp1-aloa-armel.img.xz | sudo dd of=/dev/diskN bs=4M status=progress conv=fsync
   ```
4. Find your microSD device first:
   ```sh
   diskutil list                       # macOS  — e.g. /dev/disk4
   # OR
   lsblk                                # Linux — e.g. /dev/sdb
   ```
   Replace `/dev/diskN` in step 3 with the correct device. **Get this wrong and you will overwrite the wrong disk.** Double-check.

   **Cross-platform GUI alternative:** [Raspberry Pi Imager](https://www.raspberrypi.com/software/) → "Use custom" → select the `.img.xz` file directly.

5. Eject the card, insert it into the Pi Zero W, and power it on.
6. Wait ~30 seconds for first boot. Jump to [First boot](#first-boot).

> ⚠️ **Hardware restriction**: this image works **only** on the original Raspberry Pi Zero W (BCM43430A1 WiFi). It does **not** boot on the Pi Zero **2** W. The Nexmon firmware patches are specific to the original chip; the v2 board uses a different WiFi chip not yet supported.

> ⚠️ **Default credentials on the official image** (you will rotate these — see [Security checklist](README.md#security-checklist)):
>   - SSH: `root` / `toor`, or `kali` / `kali`
>   - WiFi SSID: `💥🖥💥 Ⓟ➃ⓌⓃ🅟❶` (unicode/emoji) — note: this exact SSID is known to trigger NetworkManager/Netplan crashes on some Linux clients ([upstream #365](https://github.com/RoganDawes/P4wnP1_aloa/issues/365)). If you connect from a Linux laptop with NetworkManager, save the network with a renamed connection or change the SSID via the web client on first login.
>   - WiFi PSK: `MaMe82-P4wnP1`
>
> ⚠️ The upstream code release tag is `v0.1.1-beta` (2020) — Kali rebuilds the image with newer kernel/userland but the P4wnP1 service binary itself has not been updated since.

---

## Path B.alt — Build your own Kali RPi Zero W image (advanced, rarely needed)

This is the path the original `build_support/rpi0w-nexmon-p4wnp1-aloa.sh` was written for. It runs `debootstrap` inside a Linux host to produce a flashable image with Kali + Nexmon firmware + P4wnP1 pre-installed.

**Status:** the script targets `armel` Kali and assumes Python 2 / Go 1.10 are available in the repo. Current Kali sources have moved on. Until the script is updated (PRs welcome — track issue *TODO: file issue*), expect to manually fix:

- Replace `architecture="armel"` with `armhf` and adjust the `--arch` flag passed to `debootstrap`.
- Replace `python-dev`, `python-pip`, `python-configobj`, `python-requests` with their `python3-*` equivalents.
- Bump the bundled Go to 1.21+ and migrate the build steps off `go get` to `go install` with module-aware paths.
- Resolve `kali-arm` mirror differences for `kali-rolling` vs the snapshot used in 2020.
- The Nexmon firmware build for `BCM43430A1` may need a refreshed patchset.

Once those edits are made, the high-level flow is:

```sh
# On a Debian / Kali / Ubuntu host (NOT macOS — needs debootstrap, loop devices, qemu-user-static)
sudo apt install -y debootstrap qemu-user-static kpartx parted xz-utils \
                    kali-archive-keyring binfmt-support

cd build_support
sudo ./rpi0w-nexmon-p4wnp1-aloa.sh 0.1.1   # version tag, becomes part of the filename
```

Output: an `.img` in `build_support/rpi0w-nexmon-p4wnp1-aloa-<ver>/`. Flash it the same way as Path A.

If you get the script working against current Kali, please open a PR with the diff.

---

## Path C — Manual install on existing OS

For when you already have a Pi running Raspberry Pi OS / Kali / Debian and don't want to re-flash. This is what `install.sh` does, but by hand.

If you already have a working Pi Zero W running **Raspberry Pi OS Lite (32-bit, Bookworm/Bullseye armhf)** or a hand-built Kali, you can install P4wnP1 onto it.

### 1. Prepare the base OS

Flash Raspberry Pi OS Lite (32-bit) using Raspberry Pi Imager. In the Imager's advanced settings (gear icon):

- Enable **SSH** (set a strong password, **don't use `raspberry` or `toor`**).
- Set the **WiFi country + SSID** so the Pi can reach the internet on first boot for package installs.
- Set the **hostname** (e.g. `p4wnp1`).

Boot the Pi, SSH in, and update:

```sh
sudo apt update && sudo apt full-upgrade -y
sudo apt install -y git screen hostapd autossh bluez bluez-tools bridge-utils \
                    policykit-1 genisoimage iodine haveged dnsmasq dhcpcd5 \
                    avahi-daemon dosfstools wpasupplicant tcpdump \
                    python3-pip python3-dev
```

### 2. Get the prebuilt P4wnP1 binaries

From a Linux/macOS workstation, download the latest release tarball (or build from source — see [Building from source](#building-from-source)). You need:

- `P4wnP1_service` (ARM6 binary)
- `P4wnP1_cli` (ARM6 binary)
- `webapp.js` and `webapp.js.map`
- the `dist/` tree from this repository

Copy them onto the Pi:

```sh
scp P4wnP1_service P4wnP1_cli webapp.js webapp.js.map root@<pi-ip>:/tmp/
scp -r dist root@<pi-ip>:/tmp/
```

### 3. Install on the Pi

SSH into the Pi and run:

```sh
sudo install -m 755 /tmp/P4wnP1_service /usr/local/bin/
sudo install -m 755 /tmp/P4wnP1_cli /usr/local/bin/
sudo mkdir -p /usr/local/P4wnP1
sudo cp -R /tmp/dist/{keymaps,scripts,HIDScripts,www,db,helper,ums,legacy} /usr/local/P4wnP1/
sudo cp /tmp/webapp.js /tmp/webapp.js.map /usr/local/P4wnP1/www/
sudo cp /tmp/dist/P4wnP1.service /etc/systemd/system/

# Python dep for one of the legacy backdoor scripts (optional)
sudo pip3 install pydispatcher --break-system-packages

# Disable the stock network manager so P4wnP1 can own the interfaces
sudo systemctl disable networking.service || true

# Enable + start P4wnP1
sudo systemctl daemon-reload
sudo systemctl enable haveged avahi-daemon P4wnP1
sudo systemctl start P4wnP1
```

Verify:

```sh
systemctl status P4wnP1
journalctl -u P4wnP1 -n 50 --no-pager
```

If the service is active, jump to [First boot](#first-boot).

> ⚠️ The Nexmon-patched WiFi firmware that ships with the prebuilt Kali image is **not** installed by this path. Without it, KARMA and the WiFi covert-channel features are unavailable; standard AP / station mode still work.

---

## Building from source

Build outputs are the three binaries / JS files referenced in Path C.

### Option 1 — Docker (recommended for macOS / non-Linux hosts)

```sh
cd build_support
docker build -t p4wnp1-builder .
docker run --rm -v "$PWD/../build:/out" p4wnp1-builder \
       bash -c 'cp /root/P4wnP1_aloa/build/{P4wnP1_service,P4wnP1_cli,webapp.js,webapp.js.map} /out/'
```

The `Dockerfile` pins Go 1.12.16 (GopherJS dependency) — this is intentional and not something to "modernize" without also replacing GopherJS, which is a much larger change.

### Option 2 — Native (Linux only)

Requires Go 1.12 (1.13 also works for the service binary but **not** for the GopherJS web client build) and GopherJS:

```sh
go install github.com/gopherjs/gopherjs@latest      # if your Go is recent, you may need 1.12.x via gvm
cd build_support
./build.sh
```

Outputs land in `../build/`.

---

## First boot

After flashing and powering the Pi on, give it ~30 seconds. You should see:

- A **WiFi access point** named `P4wnP1` (PSK `MaMe82-P4wnP1`).
- A **USB ethernet device** when the Pi is plugged into a host via the inner micro-USB port.
- A **Bluetooth device** named `P4wnP1` advertising NAP.

### Connecting

| Channel | Connect to | P4wnP1 IP | Notes |
|---|---|---|---|
| WiFi AP | SSID `P4wnP1`, PSK `MaMe82-P4wnP1` | `172.24.0.1` | Fastest path. |
| USB ethernet | Plug Pi into target host via **inner** micro-USB port | `172.16.0.1` | Both RNDIS (Windows) and CDC ECM (macOS / Linux) advertised. |
| Bluetooth NAP | Pair to `P4wnP1`, PIN `1337` | `172.26.0.1` | Slow without high-speed mode. |

### Access

- **Web client:** `http://<P4wnP1-IP>:8000` in any modern browser. Log in with `admin` + the password from `INITIAL_CREDENTIALS.txt`. All gRPC and HTTP calls require a valid bearer token — there is no anonymous access.
- **CLI over SSH:** `ssh root@<P4wnP1-IP>` — password from `INITIAL_CREDENTIALS.txt`. The CLI also needs a token to talk to the gRPC API; see *CLI authentication* below.

### CLI authentication

Once auth is enabled (Path A), `P4wnP1_cli` calls need an `Authorization: Bearer <token>` header. The CLI doesn't yet have a built-in `login` subcommand — that's slated for the next milestone. As a workaround, get a token via the HTTP endpoint and pass it via the gRPC metadata flag (if your CLI version supports it), or run the CLI on the Pi itself which can still hit the local listener with a manually-obtained token:

```sh
# Get a token (run from the Pi or from any client that can reach :8000)
TOKEN=$(curl -s -X POST http://172.24.0.1:8000/api/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"<your-password>"}' | jq -r .token)
echo "$TOKEN"

# Check it works
curl -s http://172.24.0.1:8000/api/auth/whoami -H "Authorization: Bearer $TOKEN"
```

### Change default credentials immediately

On a fresh install, the `p4wnp1-firstboot.service` oneshot unit runs automatically and replaces the dangerous shared defaults for **SSH only**:

- Root password rotated to a random 20-char secret.
- SSH host keys regenerated per device.
- Credentials written to `/root/INITIAL_CREDENTIALS.txt` (mode 0600) and visible via:
  ```sh
  journalctl -t p4wnp1-firstboot
  ```

> **You must connect via USB ethernet or the WiFi AP and `cat /root/INITIAL_CREDENTIALS.txt` on the very first session.** SSH login with the old `toor` password no longer works after first boot.

The WiFi PSK and Bluetooth PIN still need manual rotation — the firstboot helper does not touch the P4wnP1 service's badger DB. Do this immediately:

```sh
# On the Pi, via SSH:

# 1. Change the WiFi PSK — easiest from the web client:
#    WiFi tab → Settings → set new password → Save as template → Deploy

# 2. Change the Bluetooth PIN, or disable BT entirely if you don't need it:
P4wnP1_cli template deploy bluetooth <your-locked-down-template>
# or
systemctl disable --now bluetooth

# 3. Once you've stored the SSH password somewhere safe, shred the credentials file:
shred -u /root/INITIAL_CREDENTIALS.txt
```

To force the firstboot helper to re-run (e.g. after rebuilding the image):

```sh
sudo rm /var/lib/p4wnp1/firstboot.done
sudo systemctl restart p4wnp1-firstboot.service
```

See the [Security checklist in README.md](README.md#security-checklist) for the full hardening list.

---

## Troubleshooting

**No WiFi AP appears after boot.**
- Wait the full 30 seconds — first boot does additional setup.
- Check the green LED. Solid + slow blink = booting. Off = SD card / power problem.
- Re-flash. Most issues at this stage are bad `dd` writes or undersized cards.

**WiFi AP is visible but I can't connect.**
- Confirm PSK is exactly `MaMe82-P4wnP1` (case-sensitive, hyphen, capital MM).
- Some Android versions hide 2.4 GHz-only APs when "WiFi 6 / 6E only" is forced. Disable that.

**Web client loads, but the gRPC connection fails (`Failed to fetch` in console).**
- Service may not be running: `ssh root@172.24.0.1 'systemctl status P4wnP1'`.
- Browser may be caching an old `webapp.js` — hard-reload (Cmd/Ctrl+Shift+R).
- If you installed via Path C and forgot `webapp.js.map`, the page works but you'll see source-map errors — these are cosmetic.

**`P4wnP1_cli` returns "connection refused".**
- The service isn't up. Check `journalctl -u P4wnP1 -n 100 --no-pager`.
- If running remotely, pass `--host <IP> --port 50051`.

**Pi reboots when I plug it into the target USB port.**
- Power supply problem. The Pi pulls more current than some USB 2.0 hub ports can deliver under WiFi load. Use the external supply on the outer USB port, and connect to the target via the inner port.

**Keystrokes are typed in the wrong language.**
- Set the HIDScript layout: `P4wnP1_cli hid run -c 'layout("de"); type("ümlaut")'`. Supported layouts: `us`, `gb`, `de`, `fr`, `es`, `it`, `ru`, `br`, `be`, `ch`, `fi`, `sv`. Full list: `ls /usr/local/P4wnP1/keymaps/`.

**SSH fails with host key warnings on a fresh image.**
- Expected. Prebuilt images share host keys. After your first login, run `dpkg-reconfigure openssh-server` on the Pi to regenerate.

---

## Uninstall

On the Pi:

```sh
sudo systemctl stop P4wnP1
sudo systemctl disable P4wnP1
sudo rm /usr/local/bin/P4wnP1_service /usr/local/bin/P4wnP1_cli
sudo rm /etc/systemd/system/P4wnP1.service
sudo rm -rf /usr/local/P4wnP1
sudo systemctl daemon-reload
```

This leaves Kali / the base OS untouched.

---

## Next steps

- Read [docs/TUTORIAL.md](docs/TUTORIAL.md) for the HIDScript walkthrough, CLI reference, and TriggerActions overview.
- Read the [Security checklist](README.md#security-checklist) and apply it before deploying.
- If you'd like to contribute fixes (especially to Path B), open an issue first describing what you're tackling so we can coordinate.
