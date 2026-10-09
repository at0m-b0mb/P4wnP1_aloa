# Building a flashable P4wnP1 image

`image/build.sh` turns an official Raspberry Pi OS Lite release into a
ready-to-flash P4wnP1 A.L.O.A. image. It runs on **macOS or Linux**; every
privileged step (loop devices, mounting, the foreign-architecture chroot)
happens inside a container, so the host only needs Docker and a Go toolchain.

```bash
./image/build.sh --arch armhf     # Pi Zero, Pi Zero W, Pi 1       (ARMv6)
./image/build.sh --arch arm64     # Pi Zero 2 W, Pi 3, Pi 4, Pi 5  (ARMv8)
./image/build.sh --arch all
```

Output lands in `image/out/`:

```
P4wnP1-ALOA-<version>-<arch>.img.xz
P4wnP1-ALOA-<version>-<arch>.img.xz.sha256
```

## Which image do I want?

| Board | Architecture | Notes |
|---|---|---|
| Pi Zero W | `armhf` | The classic P4wnP1 target. Only board with a Nexmon-patched firmware available, so the only one where KARMA / multi-SSID can work. |
| Pi Zero | `armhf` | No WiFi or Bluetooth. USB attack surface only. |
| Pi Zero 2 W | `arm64` | USB gadget, normal AP, Bluetooth all work. No Nexmon for its BCM43436, so no KARMA. |
| Pi 4 / Pi 5 | `arm64` | USB-C port must be the one in peripheral mode. |

`arm64` support exists because the service used to be gated to 32-bit ARM by
build tags that turned out to be incidental rather than a real dependency.

## What the build does

1. Downloads the official Raspberry Pi OS Lite image and **verifies its
   SHA-256** against the published checksum. A mismatch aborts the build
   rather than producing an image from an unverified base.
2. Grows the root partition (default +1536MB) **in place**. The partition table
   is never recreated: doing so changes the MBR disk identifier, which changes
   the PARTUUID that `cmdline.txt` and `/etc/fstab` reference, and the result
   does not boot.
3. Chroots into the image and installs dependencies, the P4wnP1 binaries and
   the data tree. When the image architecture differs from the host, this runs
   under QEMU via a binfmt handler registered with the `F` (fix-binary) flag,
   which is what lets the interpreter survive the chroot.
4. Writes the boot configuration: `dtoverlay=dwc2` and `modules-load=dwc2`.
   **This is what makes the USB side work at all** -- without it there is no
   UDC for the gadget subsystem to bind to and no USB function does anything.
5. Creates the operator account, sets the WiFi regulatory domain, and enables
   the services.
6. Shrinks the filesystem and the partition to fit, then compresses.

## Options

```
--arch ARCH          armhf | arm64 | all                      (required)
--grow-mb N          extra megabytes for the rootfs           (default 1536)
--no-shrink          skip shrinking the finished image
--no-compress        leave a raw .img instead of .img.xz
--ssh-user NAME      operator account name                    (default p4wnp1)
--ssh-pass PASS      operator password baked into the image.
                     NOT RECOMMENDED: identical on every device built
                     from it. Omit for a per-device password at first boot.
--wifi-country CC    regulatory domain                        (default US)
--pios-date DATE     Raspberry Pi OS release to base on       (default 2026-09-15)
```

## Credentials in a built image

Nothing shared between devices survives first boot:

- **SSH**: the account is created **locked**. First boot generates a password
  for that one device and writes it to `p4wnp1-credentials.txt` on the boot
  partition (and `/root/INITIAL_CREDENTIALS.txt`); the OLED panel shows it as
  a QR code if a HAT is fitted. If you left an `authorized_keys` on the boot
  partition, or set a user in Raspberry Pi Imager, first boot adopts that and
  generates nothing.

  `--ssh-pass` bakes one password into the image instead. It is the shared-
  password escape hatch, not a per-unit mechanism: every device flashed from
  that image has the same one, and the build prints two warnings when you use
  it. `chage -d 0` was deliberately abandoned -- forcing a change at first
  login only means whoever logs in first chooses the new password.
- **SSH host keys**: deleted from the image. If they shipped baked in, anyone
  who downloaded the image could impersonate every device built from it.
  `p4wnp1-firstboot.service` regenerates them per device.
- **Web console password**: generated per device at first boot and written to
  `/root/INITIAL_CREDENTIALS.txt` (mode 0600). Read it over SSH.
- **WiFi PSK**: generated per device at first boot.

The first login therefore has to come over **USB ethernet** (`172.16.0.1`) or
the **serial console**, because the WiFi PSK is not knowable until you have
read it off the device. That is deliberate: the alternative is a PSK that is
identical on every unit and public the moment the image is.

## Requirements

- **Docker**. On macOS, Colima works: `colima start --cpu 6 --memory 10 --disk 80`.
- **Go** (1.24+) on the host, to cross-compile the binaries.
- About 12GB of free disk for both architectures (base images decompress to
  ~2.7GB each and are cached in `image/cache/`).
- The checkout should live under `$HOME`: Docker on macOS can only bind-mount
  paths the VM shares, and `$HOME` is the default share.

## Flashing

Use Raspberry Pi Imager, balenaEtcher, or:

```bash
xz -d P4wnP1-ALOA-<version>-<arch>.img.xz
sudo dd if=P4wnP1-ALOA-<version>-<arch>.img of=/dev/sdX bs=4M conv=fsync status=progress
```

Verify the checksum first:

```bash
sha256sum -c P4wnP1-ALOA-<version>-<arch>.img.xz.sha256
```

## Troubleshooting

**`no /dev/loop-control`** -- the container was not given `--privileged -v /dev:/dev`.
`build.sh` passes both; if you are invoking `stage.sh` by hand, you need them.

**`binfmt handler qemu-arm not registered`** -- run
`docker run --privileged --rm tonistiigi/binfmt --install arm64,arm`.
`build.sh` does this automatically.

**`exec format error` inside the chroot** -- the binfmt handler is registered
without the `F` flag, so the interpreter does not survive the chroot.
Re-register with `tonistiigi/binfmt`.

**Build succeeds but the image does not boot** -- check that partition 2 was
not shrunk past the filesystem. `stage.sh` verifies the partition end against
the filesystem size before truncating and refuses to continue on a mismatch,
so this should be impossible; if it happens, file it with the build log.

## What is NOT tested

These images have been **built and inspected, not booted on hardware**. The
build pipeline is verified end to end -- partitioning, chroot, package
installation, boot configuration, shrink -- but no claim is made that any
particular USB, WiFi or Bluetooth behaviour works on a real Pi until someone
boots one. See the "Hardware verification" section of the top-level README.
