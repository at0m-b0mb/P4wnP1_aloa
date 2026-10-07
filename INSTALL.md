# Installing P4wnP1 A.L.O.A.

Three routes, easiest first:

1. **[Flash a release image](#1-flash-a-release-image)** — nothing to build.
2. **[Install onto an existing Raspberry Pi OS](#2-install-onto-an-existing-raspberry-pi-os)** — keep your current system.
3. **[Build an image yourself](#3-build-an-image-yourself)** — from source, reproducibly.

> **None of this has been tested on physical hardware.** See *What is not verified* in the
> [README](README.md#what-is-not-verified).

---

## 1. Flash a release image

Download from [Releases](https://github.com/at0m-b0mb/P4wnP1_aloa/releases/latest):

| Image | Boards |
|---|---|
| `…-armhf.img.xz` | Pi Zero, Pi Zero W, Pi 1 |
| `…-arm64.img.xz` | Pi Zero 2 W, Pi 3, Pi 4, Pi 5 |

```bash
sha256sum -c P4wnP1-ALOA-<version>-armhf.img.xz.sha256    # verify FIRST
xz -d P4wnP1-ALOA-<version>-armhf.img.xz
sudo dd if=P4wnP1-ALOA-<version>-armhf.img of=/dev/sdX bs=4M conv=fsync status=progress
```

Raspberry Pi Imager and balenaEtcher both work too, and read `.img.xz` directly.

Then go to [First boot](#first-boot).

---

## 2. Install onto an existing Raspberry Pi OS

Start from **Raspberry Pi OS Lite**. The installer targets current releases (Debian 12
"bookworm" and 13 "trixie") and detects the boot partition rather than assuming it.

```bash
git clone https://github.com/at0m-b0mb/P4wnP1_aloa.git
cd P4wnP1_aloa

# Build the three binaries for your board (works from macOS or Linux too):
make build-armv6        # Pi Zero / Zero W
#   or
make build-arm64        # Pi Zero 2 W / 3 / 4 / 5

sudo ./install.sh --ssid MyAP --wifi-country GB
```

### What install.sh does

1. Installs dependencies. Required packages are fatal; optional ones are installed
   individually and only warn — so one retired package name cannot abort the whole install.
   (`policykit-1` became `polkitd` in Debian 12, and it used to take the install down with it.)
2. Copies the binaries to `/usr/local/bin` and the data tree to `/usr/local/P4wnP1`.
3. Installs and enables the systemd units.
4. **Writes the boot configuration** — `dtoverlay=dwc2` in `config.txt` and `modules-load=dwc2`
   on the kernel command line. Without these the Pi's USB controller stays in host mode, the
   gadget subsystem has no UDC to bind to, and **every USB function silently does nothing**.
   Both files are backed up first, and `cmdline.txt` is kept as a single line (a stray newline
   there makes the Pi ignore everything after it, `root=` included).
5. Sets the WiFi regulatory domain and unblocks rfkill. On current Pi OS `wlan0` stays
   soft-blocked until a country is set and hostapd refuses to start without one — this is
   almost always why "the access point never appears".
6. Marks `wlan0` / `usb0` unmanaged in NetworkManager, without disabling NM entirely, so
   `eth0` keeps working as a recovery path.

### Options

```
--ssid NAME           AP name. Avoid emoji -- it crash-loops NetworkManager on Linux clients.
--psk PASSPHRASE      WiFi PSK (8-63 chars). Omitted: a random one is generated.
--wifi-country CC     Regulatory domain. Default US. Required, or hostapd will not start.
--skip-reboot         Do not reboot at the end.
--skip-apt            Assume dependencies are already present.
```

It is idempotent: re-running re-applies missing pieces without duplicating units or
overwriting an existing credentials file.

---

## 3. Build an image yourself

```bash
./image/build.sh --arch all
```

Runs on macOS or Linux; everything privileged happens inside a container, and every image is
mounted and verified before it is published. Fully documented in
**[image/README.md](image/README.md)**.

---

## First boot

Plug the Pi into a host with the cable in the **data** port — on a Zero that is the **inner**
micro-USB; the outer one is power only. The device comes up as a USB ethernet adapter at
`172.16.0.1`.

```bash
ssh p4wnp1@172.16.0.1
```

You will be **forced to change the password** immediately — the image ships a documented
default precisely so it cannot survive as a real credential.

Then read this device's generated secrets:

```bash
sudo cat /root/INITIAL_CREDENTIALS.txt
```

That file holds the **web console password** and the **WiFi PSK**, both generated on this
device at first boot. Nothing is shared between two flashed devices: SSH host keys are
regenerated per device too.

This is why the first login has to come over USB or serial — the WiFi key is not knowable
until you have read it off the device. The alternative would be a PSK identical on every unit
and public the moment the image is.

Finally, open the console:

```
http://172.16.0.1:8000
```

### If USB ethernet does not appear

Use the serial console: connect a 3.3V USB-TTL adapter to GPIO14/15 (pins 8 and 10) and ground,
at 115200 baud. `enable_uart=1` is set in the image. Or put the card in another machine and
check that `config.txt` contains `dtoverlay=dwc2` and `cmdline.txt` contains
`modules-load=dwc2`.

---

## CLI authentication

The API requires a bearer token, so the CLI logs in once and caches it:

```bash
P4wnP1_cli auth login --username admin
P4wnP1_cli auth whoami
P4wnP1_cli usb get settings
```

The token lives at `~/.p4wnp1/token` (mode 0600) and is attached to every subsequent call.
Tokens last 24 hours on a sliding window.

```bash
P4wnP1_cli auth changepw        # change the console password
P4wnP1_cli auth logout          # revoke the token
```

For a remote device, add `--host`/`--port`.

---

## Health check

```bash
sudo /usr/local/P4wnP1/scripts/p4wnp1-healthcheck.sh
```

Verifies binaries, data files, systemd units, firstboot completion, the auth file's shape, the
port listeners, and the HTTP health endpoint. Add `--login-test` for a real login round trip.

---

## Updating an existing install

```bash
git pull
make build-armv6                 # or build-arm64
sudo ./install.sh --skip-apt
sudo systemctl restart P4wnP1
```

`install.sh` will not overwrite `/root/INITIAL_CREDENTIALS.txt` or re-randomise your secrets.

---

## Uninstalling

```bash
sudo make remove
```

Stops and disables the units and removes the binaries. It leaves `/usr/local/P4wnP1` and
`/etc/p4wnp1` in place so your templates and credentials survive; delete them by hand if you
want them gone. The `dwc2` lines added to `config.txt` and `cmdline.txt` are also left alone —
remove them manually if you want the USB port back in host mode.

---

## Troubleshooting

**The access point never appears.** Almost always the regulatory domain. Check
`raspi-config nonint get_wifi_country` and `rfkill list`. The service degrades to running
without WiFi rather than failing to start, so the rest of the device will still work.

**No USB functions.** Check `config.txt` for `dtoverlay=dwc2` and `cmdline.txt` for
`modules-load=dwc2`, then `lsmod | grep libcomposite` and `ls /sys/class/udc` — an empty
`/sys/class/udc` means no USB device controller is bound and nothing gadget-related can work.

**The service is not running.** `systemctl status P4wnP1` and `journalctl -u P4wnP1 -b`.
The unit restarts on failure, rate-limited to five attempts a minute; after that it stays in
`failed` state deliberately, so a genuinely broken device reports as broken rather than
flapping.

**Every command returns Unauthenticated.** Your token expired, or firstboot has not run.
`P4wnP1_cli auth login` again, and check `/etc/p4wnp1/auth.json` exists.

More, including what is still broken, in [KNOWN_ISSUES.md](KNOWN_ISSUES.md).
