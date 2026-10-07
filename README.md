<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="docs/img/banner-dark.svg">
    <img src="docs/img/banner-light.svg" alt="P4wnP1 A.L.O.A. — a little offensive appliance" width="100%">
  </picture>
</p>

<p align="center">
  <strong>A Raspberry Pi that becomes whatever USB device the job needs.</strong><br>
  Keyboard, mouse, network adapter, mass storage, serial — alone or all at once,<br>
  reconfigurable from a browser, scriptable, and able to act on its own once the cable is in.
</p>

---

> **Authorized testing only.** This is a red-team tool. Point it only at systems you own or
> have written permission to test. See [DISCLAIMER.md](DISCLAIMER.md).

> **Hardware-verification status.** Everything here is built and tested in CI-equivalent
> automation — the images are assembled from official Raspberry Pi OS releases, mounted and
> checked before publishing. **None of it has been booted on a physical Pi.** Read
> [What is not verified](#what-is-not-verified) before you rely on it for anything.

---

## Quick start

Flash an image, plug it into a target, and it comes up as a USB device you control from a browser.

```bash
# 1. Download a release image and verify it
sha256sum -c P4wnP1-ALOA-<version>-armhf.img.xz.sha256

# 2. Flash it (or use Raspberry Pi Imager / balenaEtcher)
xz -d P4wnP1-ALOA-<version>-armhf.img.xz
sudo dd if=P4wnP1-ALOA-<version>-armhf.img of=/dev/sdX bs=4M conv=fsync status=progress
```

Boot the Pi with the USB cable in the **data** port (the inner one on a Zero), then:

```bash
ssh p4wnp1@172.16.0.1          # over USB ethernet. You are forced to change the password.
sudo cat /root/INITIAL_CREDENTIALS.txt   # the web console password and WiFi PSK for THIS device
```

Then open **<http://172.16.0.1:8000>**.

Nothing is shared between devices: the SSH password must be changed on first login, the host
keys are generated per device, and the console password and WiFi PSK are random per device.
That is why the first login has to come over USB or serial — the WiFi key is not knowable
until you have read it off the device.

---

## The console

<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="docs/img/console-cable-dark.jpg">
    <img src="docs/img/console-cable-light.jpg" alt="The Cable view: a live strip of the USB functions the target host enumerates" width="100%">
  </picture>
</p>

The first thing the console shows you is **what the cable presents** — a live picture of the USB
functions the machine on the other end actually enumerates, each segment carrying its device path
or host address, with the attach state driven by real gadget events.

Six views, named for what the device does:

| View | What it is |
|---|---|
| **Cable** | USB composition and the identity the device claims — vendor, product, serial |
| **Radio** | WiFi state, the Bluetooth controller, and the interface table |
| **Keystrokes** | HIDScript editor, runner, and job control |
| **Reflexes** | The trigger/action rules the device acts on by itself |
| **Loadouts** | Whole-device configurations, backup, reboot, shutdown |
| **Journal** | Everything the device reports, live |

<p align="center">
  <img src="docs/img/console-reflexes-dark.jpg" alt="The Reflexes view, listing trigger and action pairs" width="49%">
  <img src="docs/img/console-journal-dark.jpg" alt="The Journal view, streaming device events live" width="49%">
</p>

It is plain HTML, CSS and JavaScript — no framework, no build step, 188KB including webfonts.
The smallest supported target is a Pi Zero W with one 1GHz core and 512MB of RAM, and it is
frequently driven from a phone joined to the device's own access point. Light and dark themes,
and every colour pairing is checked against WCAG AA by `make contrast`.

---

## What it does

**USB.** Presents HID keyboard, HID mouse, raw HID, RNDIS and CDC ECM ethernet, mass storage
(disk or CD-ROM) and serial — individually or as one composite device, reconfigurable at runtime
without a reboot.

**Keystroke injection.** HIDScript is real JavaScript running on the device: typing with
human-like cadence, 15 keyboard layouts, mouse control including absolute positioning, LED-state
feedback for branching, and up to 8 parallel jobs. Existing **DuckyScript** payloads convert
straight across:

```bash
P4wnP1_cli ducky convert payload.txt -o payload.js
```

**Networking.** WiFi access point or station with failover, Bluetooth NAP, per-interface DHCP
server or client, and persistent templates for all of it.

**Autonomy.** Trigger/action rules let the device act without an operator: when the USB host
attaches, when an SSID appears, when a DHCP lease is granted, when a GPIO pin changes, when a
script signals a group value — run a bash script, a HIDScript, or deploy a whole configuration.

**Two control surfaces.** The browser console above, and `P4wnP1_cli` over gRPC, locally or
remotely. Both authenticate.

---

## Hardware

| Board | Image | Status |
|---|---|---|
| **Pi Zero W** | `armhf` | The classic target. The only board with a Nexmon-patched firmware, so the only one where KARMA and multi-SSID can work. |
| **Pi Zero** | `armhf` | No WiFi or Bluetooth. USB surface only. |
| **Pi Zero 2 W** | `arm64` | USB, normal AP and Bluetooth all build. No Nexmon for its BCM43436, so no KARMA. |
| **Pi 3 / 4 / 5** | `arm64` | On a Pi 4/5 the USB-C port is the one that does peripheral mode. |

> Upstream and earlier versions of this README said the Pi Zero 2 W was unsupported and blamed
> the WiFi chipset. That was not the real reason. The core service was gated to 32-bit ARM by
> build tags (`+build linux,arm`) that turned out to be incidental rather than a genuine
> dependency — removing them builds arm64 with no code changes. The chipset only limits
> KARMA, not the device.

You also need a microSD card (8GB minimum), a USB cable or OTG adapter, and ideally an external
5V supply so the Pi can stay powered while detached from the target.

---

## Building

Everything cross-compiles from macOS or Linux. No Pi required to build.

```bash
make build-armv6     # binaries for Pi Zero / Zero W
make build-arm64     # binaries for Pi Zero 2 W / 3 / 4 / 5
make image           # flashable .img.xz for both, via Docker
make test            # unit tests
make contrast        # WCAG check on the console palette
```

Image building is documented in detail in **[image/README.md](image/README.md)** — it downloads
an official Raspberry Pi OS Lite release, verifies its checksum, grows the root partition in
place, chroots in (under QEMU for a foreign architecture), installs the payload, writes the boot
configuration, shrinks and compresses. Every built image is then **mounted and verified** before
it is published: partition geometry, the payload, enabled units, the dwc2 overlay, that
`root=PARTUUID` still matches the disk identifier, and that no SSH host keys were baked in.

Installing onto a Pi you already have:

```bash
sudo ./install.sh --ssid MyAP --wifi-country GB
```

---

## Security posture

This is a tool for attacking systems, which makes its own security worth stating plainly.

- **The API authenticates.** Every gRPC method and every JSON endpoint requires a bearer token.
  Tokens are opaque, random, expire on a sliding window and can be revoked.
- **Per-device secrets.** Nothing meaningful is shared between two flashed devices.
- **Path handling is allowlisted.** The RPCs that take a filesystem path reject traversal and
  anything outside the permitted directories.
- **The console is same-origin only.** It emits no CORS headers and rejects foreign origins
  outright, because this device is often reached from a browser that is simultaneously visiting
  untrusted pages.
- **Payload text cannot become code.** The DuckyScript converter escapes its output so a crafted
  payload cannot close the generated JavaScript literal and run as root.

**Still open:** there is no TLS. The console is served over plain HTTP, so a bearer token rides
in cleartext over whatever link you reach it on. On the device's own WPA2 access point or a USB
cable that is survivable; over anything else it is not. Treat this as a device you reach
directly, not one you expose.

---

## What is not verified

Being specific about this matters more than the feature list.

**No physical Raspberry Pi was used at any point.** What that does and does not mean:

*Verified by automation:* the Go service and CLI cross-compile for both architectures; unit
tests pass for the auth, JSON bridge and DuckyScript packages; `go vet` is clean for both
targets; images build from official releases and pass a mount-and-inspect check covering
partition geometry, payload, enabled units and boot configuration; the console was exercised
against a mock implementing the real API shapes, in both themes and at phone width, with no
console errors.

*Not verified at all:* that an image boots. That USB gadget mode initialises on real silicon and
a host enumerates the functions. That keystroke injection types correctly into a real machine.
That hostapd brings up the access point. That Bluetooth pairs. That the trigger engine fires on
real events. That any of this survives having the cable pulled out mid-write.

If you are evaluating this for real work, **boot it on a Pi first and check those yourself.**
[KNOWN_ISSUES.md](KNOWN_ISSUES.md) tracks defects found by reading the code, several of which are
reachable only on hardware.

---

## Licence and selling this

P4wnP1 A.L.O.A. is **GPL-3.0** (see [LICENSE](LICENSE)), inherited from
[MaMe82's original](https://github.com/mame82/P4wnP1_aloa).

GPL-3.0 **permits selling** devices with this software on them. What it requires is that every
buyer gets the complete corresponding source for the version on their device, under GPL-3.0,
including your modifications, and that you do not add restrictions on their right to use, modify
and redistribute it. You cannot keep changes to this codebase proprietary while shipping them.

In practice that means the viable commercial shape is hardware, assembly, support, documentation,
training and engagement services — not licence fees for the code. If you intend to build a
business on this, get the licence position reviewed by someone qualified; the paragraph above is
a description of what the licence says, not legal advice.

---

## Documentation

- **[INSTALL.md](INSTALL.md)** — installing onto an existing Raspberry Pi OS
- **[image/README.md](image/README.md)** — building flashable images
- **[docs/TUTORIAL.md](docs/TUTORIAL.md)** — HIDScript, the CLI, and trigger actions
- **[KNOWN_ISSUES.md](KNOWN_ISSUES.md)** — the defect backlog, prioritised
- **[CHANGELOG.md](CHANGELOG.md)** — what changed and why

## Credits

Created by **[MaMe82](https://github.com/mame82)** (Marcus Mengs), whose design — the composite
gadget, HIDScript, and the trigger/action engine — is what this is. Maintained upstream at
[RoganDawes/P4wnP1_aloa](https://github.com/RoganDawes/P4wnP1_aloa). Kali Linux ship a prebuilt
image for the Pi Zero W.

This fork modernises the toolchain, adds authentication, a JSON API, arm64 support, a new
console, DuckyScript conversion and a reproducible image pipeline.
