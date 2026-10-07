# build_support

## What used to be here

`rpi0w-nexmon-p4wnp1-aloa.sh` has been removed. It built a Kali ARM image and
could no longer produce a working one:

- it targeted the old Kali `armel` pipeline;
- its package list asked for `python-dev`, `python-pip`, `python-configobj`,
  `python-requests` and `policykit-1` -- Python 2 is gone from Debian entirely
  and `policykit-1` was renamed to `polkitd` in Debian 12;
- it wrote `/boot/config.txt`, but Debian 12 moved the boot partition to
  `/boot/firmware`, so on any current release those writes landed in a file
  nothing reads;
- it shelled out to `ifconfig`, which is no longer installed by default.

Kali maintain their own current copy, which is the one to look at if you want a
Kali-based build:
<https://gitlab.com/kalilinux/build-scripts/kali-arm/-/blob/main/raspberry-pi-zero-w-p4wnp1-aloa.sh>

## What to use instead

**[`../image/`](../image/)** builds a flashable image from an official
Raspberry Pi OS Lite release, for both 32-bit (Pi Zero / Zero W) and 64-bit
(Pi Zero 2 W / 3 / 4 / 5) targets:

```bash
./image/build.sh --arch all
```

It runs on macOS or Linux; everything privileged happens inside a container.

## `build.sh` and `Dockerfile`

`build.sh` cross-compiles the three binaries. `make build-armv6` and
`make build-arm64` do the same thing and are the documented route.

The `Dockerfile` is the legacy GopherJS web-client builder. The GopherJS client
it built is no longer used -- it has no auth support, so every RPC it makes is
rejected -- and has been replaced by the console in `dist/www/app/`, which is
plain HTML/CSS/JS and needs no build step at all. The Dockerfile is kept only
for reference.
