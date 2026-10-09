#!/bin/bash
#
# verify.sh -- prove a built image is actually an image before we ship it.
#
# Runs INSIDE the builder container, against the finished .img. This exists
# because a build once produced a 2.6GB file of pure zeros that compressed to
# 410KB and still reported success: stage.sh had done everything right, but the
# host read the file through the VM's shared filesystem before the container's
# writes were visible there, and compressed what it saw. Nothing in the
# pipeline noticed, because nothing had ever looked at the output.
#
# So: look at the output. Mount it and check the things that must be true.
set -euo pipefail

IMG="${1:?usage: verify.sh <image>}"
MNT=/mnt/p4verify
LOOP=""

ok()   { printf '\033[1;32m[verify]\033[0m %s\n' "$*"; }
die()  { printf '\033[1;31m[verify:FAIL]\033[0m %s\n' "$*" >&2; exit 1; }

cleanup() {
    set +e
    umount -lf "$MNT/boot/firmware" 2>/dev/null
    umount -lf "$MNT" 2>/dev/null
    [ -n "$LOOP" ] && losetup -d "$LOOP" 2>/dev/null
}
trap cleanup EXIT

[ -f "$IMG" ] || die "image not found: $IMG"

SIZE=$(stat -c %s "$IMG")
[ "$SIZE" -gt $((512 * 1024 * 1024)) ] || die "image is only $SIZE bytes; a Pi OS image is >512MB"

# A file of zeros has no MBR signature. Catch the pathological case before
# bothering with loop devices.
if ! dd if="$IMG" bs=512 count=1 status=none | od -An -tx1 | tr -d ' \n' | grep -q '55aa$'; then
    die "no MBR boot signature at offset 510 -- this file is not a disk image (all zeros?)"
fi
ok "MBR signature present"

LOOP="$(losetup --find --show --partscan "$IMG")"

# WAIT for the partition nodes. --partscan asks the kernel to read the
# partition table, but ${LOOP}p1 and ${LOOP}p2 are created by UDEV, and that
# happens asynchronously. Testing for them on the next line is a race.
#
# It lost that race on a CI runner building four images at once: a perfectly
# good armhf/oled image was rejected with "partition 1 (boot) missing" while
# the other three variants passed. The image was fine. The check was early.
#
# A gate that fails good artifacts is not merely annoying, it is corrosive:
# it teaches everyone to re-run until green, and that is exactly the habit
# that waves a real failure through. So this waits up to ten seconds and
# only then calls it missing -- which is still a hard failure, because an
# image with no partition table really must not ship.
if command -v udevadm >/dev/null 2>&1; then
    udevadm settle --timeout=10 >/dev/null 2>&1 || true
fi
for _ in $(seq 1 50); do
    [ -b "${LOOP}p1" ] && [ -b "${LOOP}p2" ] && break
    sleep 0.2
done
[ -b "${LOOP}p1" ] || die "partition 1 (boot) missing after waiting 10s"
[ -b "${LOOP}p2" ] || die "partition 2 (root) missing after waiting 10s"
ok "two partitions present"

# The partition must not claim space past the end of the file -- that is what a
# botched shrink produces, and it is not always fatal at flash time, so it has
# to be checked rather than assumed.
P2_END=$(partx -g -o END -n 2 "$LOOP" | tr -d ' ')
IMG_SECTORS=$(( SIZE / 512 ))
[ "$P2_END" -lt "$IMG_SECTORS" ] || \
    die "partition 2 ends at sector $P2_END but the image is only $IMG_SECTORS sectors"
ok "partition table fits inside the image"

mkdir -p "$MNT"
mount "${LOOP}p2" "$MNT" || die "root filesystem will not mount"
mount "${LOOP}p1" "$MNT/boot/firmware" || die "boot partition will not mount"
ok "both filesystems mount"

fail=0
# `-e` follows symlinks, and systemd's enable symlinks are ABSOLUTE
# (/etc/systemd/system/P4wnP1.service). Followed from inside this container
# that resolves against the CONTAINER's root, where the unit does not exist,
# so a correctly-enabled service reads as missing. Accept a symlink that
# exists as a link even when its target does not resolve here.
check() {
    if [ -e "$MNT$1" ] || [ -L "$MNT$1" ]; then ok "present: $1"; else
        printf '\033[1;31m[verify:FAIL]\033[0m missing: %s\n' "$1" >&2; fail=1; fi
}

# The payload.
check /usr/local/bin/P4wnP1_service
check /usr/local/bin/P4wnP1_cli
check /usr/local/bin/p4wnp1-hashpw
check /usr/local/P4wnP1/keymaps
check /usr/local/P4wnP1/HIDScripts
check /usr/local/P4wnP1/www/app/index.html
check /usr/local/P4wnP1/www/app/js/app.js
check /etc/p4wnp1/build-info

# The service units, and that they are actually enabled rather than just copied.
check /etc/systemd/system/P4wnP1.service
check /etc/systemd/system/p4wnp1-firstboot.service
check /etc/systemd/system/multi-user.target.wants/P4wnP1.service
check /etc/systemd/system/multi-user.target.wants/p4wnp1-firstboot.service

# The two variants must be genuinely different artifacts, not one image with
# a different filename. So each is checked for what it should have AND for
# what it should not: the plain image is verified to contain no OLED binary,
# no unit and no SPI line, which is what makes "this build has no OLED
# support" a fact about the file rather than a claim in its description.
absent() {
    if [ -e "$MNT$1" ] || [ -L "$MNT$1" ]; then
        printf '\033[1;31m[verify:FAIL]\033[0m should NOT be in this variant: %s\n' "$1" >&2; fail=1
    else
        ok "absent, as this variant requires: $1"
    fi
}

if [ "${P4_OLED:-0}" = "1" ]; then
    check /usr/local/bin/p4wnp1-oled
    check /etc/systemd/system/p4wnp1-oled.service
    check /etc/systemd/system/multi-user.target.wants/p4wnp1-oled.service
    if [ -n "${P4_BRAND:-}" ]; then
        check /etc/p4wnp1/brand.txt
    fi
    # SPI, without which a correctly wired OLED HAT stays dark. Checked here
    # rather than trusted, because the symptom is a blank panel and the first
    # thing anyone blames is their soldering.
    if grep -q '^dtparam=spi=on' "$MNT/boot/firmware/config.txt"; then
        ok "config.txt enables SPI for the OLED HAT"
    else
        printf '\033[1;31m[verify:FAIL]\033[0m config.txt does not enable SPI; an OLED HAT will not light\n' >&2; fail=1
    fi
else
    absent /usr/local/bin/p4wnp1-oled
    absent /etc/systemd/system/p4wnp1-oled.service
    absent /etc/systemd/system/multi-user.target.wants/p4wnp1-oled.service
    absent /etc/p4wnp1/brand.txt
    if grep -q '^dtparam=spi=on' "$MNT/boot/firmware/config.txt"; then
        printf '\033[1;31m[verify:FAIL]\033[0m the plain variant enables SPI; that belongs to the oled build\n' >&2; fail=1
    else
        ok "SPI left off, as the plain variant requires"
    fi
fi

# Boot configuration -- without these the entire USB feature set is dead.
# dr_mode=peripheral SPECIFICALLY, not just "a dwc2 overlay".
#
# Stock Raspberry Pi OS already ships `dtoverlay=dwc2,dr_mode=host` in
# config.txt, so `grep '^dtoverlay=dwc2'` matched the BASE IMAGE and this
# check passed on an image where P4wnP1's own line had never been added --
# and host mode is the exact opposite of what gadget mode needs. A gate that
# is satisfied by the thing it is supposed to be checking for the absence of
# is not a gate.
if grep -qE '^dtoverlay=dwc2,dr_mode=peripheral' "$MNT/boot/firmware/config.txt"; then
    ok "config.txt enables dwc2 in peripheral mode"
else
    printf '\033[1;31m[verify:FAIL]\033[0m config.txt has no dtoverlay=dwc2,dr_mode=peripheral; USB gadget mode will not work\n' >&2; fail=1
fi
if grep -q 'modules-load=dwc2' "$MNT/boot/firmware/cmdline.txt"; then
    ok "cmdline.txt loads dwc2"
else
    printf '\033[1;31m[verify:FAIL]\033[0m cmdline.txt does not load dwc2\n' >&2; fail=1
fi
# cmdline.txt must be exactly one line or the Pi ignores everything after the
# first newline -- including root=.
LINES=$(wc -l < "$MNT/boot/firmware/cmdline.txt")
if [ "$LINES" -le 1 ]; then ok "cmdline.txt is a single line"; else
    printf '\033[1;31m[verify:FAIL]\033[0m cmdline.txt spans %s lines; everything after the first is ignored\n' "$LINES" >&2; fail=1
fi
# The root= PARTUUID must still match the partition table, or it will not boot.
CMD_UUID=$(grep -oE 'root=PARTUUID=[0-9a-fA-F-]+' "$MNT/boot/firmware/cmdline.txt" | cut -d= -f3 || true)
DISK_ID=$(fdisk -l "$IMG" 2>/dev/null | grep -oE 'Disk identifier: 0x[0-9a-f]+' | cut -d' ' -f3 | sed 's/0x//')
if [ -n "$CMD_UUID" ] && [ -n "$DISK_ID" ]; then
    if [ "${CMD_UUID%%-*}" = "$DISK_ID" ]; then ok "root=PARTUUID matches the disk identifier"; else
        printf '\033[1;31m[verify:FAIL]\033[0m root=PARTUUID=%s does not match disk id %s; this image will not boot\n' "$CMD_UUID" "$DISK_ID" >&2; fail=1; fi
fi

# SSH host keys must NOT be baked in: shipping them would let anyone who
# downloaded the image impersonate every device built from it.
if ls "$MNT"/etc/ssh/ssh_host_* >/dev/null 2>&1; then
    printf '\033[1;31m[verify:FAIL]\033[0m SSH host keys are baked into the image; they must be generated per device\n' >&2; fail=1
else
    ok "no SSH host keys baked in (regenerated at first boot)"
fi

# No account in the image may be loginable.
#
# The image used to ship p4wnp1:p4wnp1 -- the same password on every device
# flashed from it -- with NOPASSWD:ALL sudo and headless SSH enabled, so anyone
# who could reach port 22 could take root. The USB cable alone is such a path:
# the gadget hands the attached host an address on the same subnet. The comment
# defending it argued that `chage -d 0` made it safe because a password change
# is forced at first login, but that only stops the default PERSISTING, not
# being USED once -- and the attacker is then the one who chooses the new one.
#
# A password hash field of "!" or "*" (or anything starting "!") is locked.
# Anything else is a usable credential shared by every copy of this image, and
# publishing that is worse than publishing nothing.
LOGINABLE=""
if [ -r "$MNT/etc/shadow" ]; then
    while IFS=: read -r user hash _; do
        case "$hash" in
            ''|'!'*|'*') continue ;;       # no password, or locked
        esac
        LOGINABLE="${LOGINABLE} ${user}"
    done < "$MNT/etc/shadow"
fi
if [ -n "$LOGINABLE" ]; then
    printf '\033[1;31m[verify:FAIL]\033[0m these accounts ship with a usable password, identical on every flashed device:%s\n' "$LOGINABLE" >&2
    printf '\033[1;31m[verify:FAIL]\033[0m first boot must issue per-device credentials; see image/lib/customize.sh\n' >&2
    fail=1
else
    ok "no account ships with a password (first boot issues per-device credentials)"
fi

# The binaries must be the right architecture.
ARCH_LINE=$(file -b "$MNT/usr/local/bin/P4wnP1_service" 2>/dev/null || echo unknown)
ok "service binary: ${ARCH_LINE:0:60}"
case "${P4_ARCH:-}" in
    armhf) echo "$ARCH_LINE" | grep -q 'ELF 32-bit.*ARM' || { printf '\033[1;31m[verify:FAIL]\033[0m expected a 32-bit ARM binary\n' >&2; fail=1; } ;;
    arm64) echo "$ARCH_LINE" | grep -q 'ELF 64-bit.*aarch64' || { printf '\033[1;31m[verify:FAIL]\033[0m expected an aarch64 binary\n' >&2; fail=1; } ;;
esac

USED=$(df -h "$MNT" | awk 'NR==2{print $3}')
ok "root filesystem holds $USED of data"

[ "$fail" -eq 0 ] || die "image verification failed -- refusing to publish this artifact"
ok "IMAGE VERIFIED"
