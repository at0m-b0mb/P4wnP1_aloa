#!/usr/bin/env bash
#
# flash-sd.sh -- write a P4wnP1 image to the SD card, on macOS.
#
#   sudo ./tools/flash-sd.sh image/out/P4wnP1-ALOA-vX.Y.Z-oled-armhf.img.xz
#
# WHY THIS LIVES IN THE REPO
#
# It used to live in a scratch directory, and that directory is wiped between
# sessions. Twice now the first sign of that was `command not found` in the
# middle of a flash. A tool you need on every release belongs with the thing
# it releases.
#
# WHY IT FINDS THE CARD BY IDENTITY
#
# Disk numbers move between replugs. "It was disk4 last time" is how someone
# writes an image over their own system drive. This matches on the reader's
# name, its exact byte count, and that it is removable and not virtual, and it
# REFUSES rather than guessing if it does not find exactly one.
set -euo pipefail

IMG="${1:-}"
WANT_NAME="${P4_SD_NAME:-Built In SDXC Reader}"
WANT_BYTES="${P4_SD_BYTES:-64088965120}"

die() { printf '\nABORTED: %s\n' "$1" >&2; exit 1; }

[ "$(id -u)" -eq 0 ] || die "run this with sudo"
[ -n "$IMG" ]        || die "usage: sudo $0 <image.img.xz>"
[ -f "$IMG" ]        || die "image not found: $IMG"

echo "==> finding the card by what it IS, not by its number"
matches=()
for node in $(diskutil list | sed -n 's|^/dev/\(disk[0-9]*\) .*|\1|p'); do
    info=$(diskutil info "/dev/$node" 2>/dev/null) || continue
    grep -q "$WANT_NAME" <<<"$info"                   || continue
    grep -q "$WANT_BYTES" <<<"$info"                  || continue
    grep -qE 'Removable Media: +Removable' <<<"$info" || continue
    grep -qE 'Virtual: +No' <<<"$info"                || continue
    matches+=("$node")
done
[ "${#matches[@]}" -eq 0 ] && die "no '$WANT_NAME' of $WANT_BYTES bytes attached -- is the card in?"
[ "${#matches[@]}" -gt 1 ] && die "found ${#matches[@]} candidates (${matches[*]}); refusing to guess"

DISK="/dev/${matches[0]}"
[ "$DISK" = "/dev/disk0" ] && die "that is the system disk"
echo "    found $DISK"
diskutil info "$DISK" | grep -E 'Media Name|Disk Size|Removable Media' | sed 's/^/    /'

echo
echo "==> verifying the image checksum (never write an image you have not checked)"
( cd "$(dirname "$IMG")" && shasum -a 256 -c "$(basename "$IMG").sha256" ) \
    || die "checksum mismatch -- do not write this image"

echo
echo "==> unmounting $DISK"
# RETRY, and force as a last resort.
#
# Spotlight indexes a new partition the moment it mounts, and mdsync then
# DISSENTS from the unmount. Under `set -e` that aborts the script -- safe,
# since nothing has been written, but it reads like a failed flash. It bites
# hardest immediately AFTER a successful flash, because the card has just
# acquired a fresh FAT partition to discover.
unmounted=0
for attempt in 1 2 3 4 5; do
    if diskutil unmountDisk "$DISK"; then unmounted=1; break; fi
    echo "    unmount attempt $attempt refused (Spotlight indexes a new card); waiting"
    sleep 4
done
if [ "$unmounted" -ne 1 ]; then
    echo "    falling back to a forced unmount"
    diskutil unmountDisk force "$DISK" || die "could not unmount $DISK; nothing has been written"
fi

echo
echo "==> writing. Several minutes, and it prints nothing until it finishes."
echo "    (press Ctrl-T at any time to see progress)"
xz -dc "$IMG" | dd of="${DISK/disk/rdisk}" bs=4m
sync

echo
echo "==> done; ejecting"
# The data is already synced, so this is tidiness rather than safety -- but
# Spotlight can dissent here too, and a clean eject is worth a few retries.
for _ in 1 2 3 4 5; do
    diskutil eject "$DISK" >/dev/null 2>&1 && { echo "    ejected cleanly"; break; }
    sleep 3
done

cat <<'EOF'

Put the card back in the device and plug it into USB. First boot takes a few
minutes; the panel says so while it works.

The panel shows FOUR cards: "Write these down", then SSH, Web and WiFi. The
last three each carry a QR code, and the WiFi one is a scan-to-join code --
your phone should offer "Join Network" rather than making you type the key.
KEY1 erases the credentials once you have them.

If you see only x/3, the WiFi card is missing and this is not the image you
meant to write.
EOF
