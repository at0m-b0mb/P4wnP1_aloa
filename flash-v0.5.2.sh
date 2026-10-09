#!/bin/bash
# Write P4wnP1 A.L.O.A. v0.5.2 (oled / armhf) to the SD card.
#
# v0.5.2 is the security fix: /api/v1/panel.png, /panel.txt and /panel/press
# shipped with no authentication, so anyone who could reach the device's web
# port could read its screen and press its buttons. v0.5.1 on the card right
# now has that hole.
#
# Run with sudo.
#
# Unlike the v0.5.1 script this does NOT hardcode a disk number. Disk numbers
# move between replugs, and "it was disk4 last time" is how someone writes an
# image over their own system drive. This finds the card by identity and
# refuses if it cannot find exactly one.
set -euo pipefail

IMG="/private/tmp/claude-501/-Users-b0mba-at0mica-Documents-at0m-b0mb-Project/3b064d38-e52f-4822-9083-d06023148ce6/scratchpad/flash/P4wnP1-ALOA-v0.5.2-oled-armhf.img.xz"
WANT_NAME="Built In SDXC Reader"
WANT_BYTES="64088965120"

die() { printf '\nABORTED: %s\n' "$1" >&2; exit 1; }

[ "$(id -u)" -eq 0 ] || die "run this with sudo"
[ -f "$IMG" ] || die "image not found at $IMG"

echo "==> finding the card by what it IS, not by its number"
matches=()
for node in $(diskutil list | sed -n 's|^/dev/\(disk[0-9]*\) .*|\1|p'); do
    info=$(diskutil info "/dev/$node" 2>/dev/null) || continue
    grep -q "$WANT_NAME" <<<"$info"              || continue
    grep -q "$WANT_BYTES" <<<"$info"             || continue
    grep -qE 'Removable Media: +Removable' <<<"$info" || continue
    grep -qE 'Virtual: +No' <<<"$info"           || continue
    # Never the disk the running system is on.
    grep -qE 'Device Location: +Internal' <<<"$info" && \
        ! grep -q "$WANT_NAME" <<<"$info" && continue
    matches+=("$node")
done

[ "${#matches[@]}" -eq 0 ] && die "no '$WANT_NAME' of $WANT_BYTES bytes is attached -- is the card in?"
[ "${#matches[@]}" -gt 1 ] && die "found ${#matches[@]} candidate disks (${matches[*]}); refusing to guess"

DISK="/dev/${matches[0]}"
echo "    found $DISK"
diskutil info "$DISK" | grep -E 'Device Node|Media Name|Disk Size|Removable Media' | sed 's/^/    /'

# The system disk must never be the target, whatever the matching said.
[ "$DISK" = "/dev/disk0" ] && die "that is the system disk"

echo
echo "==> verifying the image checksum (never write an image you have not checked)"
( cd "$(dirname "$IMG")" && shasum -a 256 -c "$(basename "$IMG").sha256" ) \
    || die "checksum mismatch -- do not write this image"

echo
echo "==> unmounting $DISK"
diskutil unmountDisk "$DISK"

echo
echo "==> writing. Several minutes, and it prints nothing until it finishes."
echo "    (press Ctrl-T at any time to see progress)"
xz -dc "$IMG" | dd of="${DISK/disk/rdisk}" bs=4m
sync

echo
echo "==> done; ejecting"
diskutil eject "$DISK" || true

cat <<'EOF'

Put the card back in the device and plug it into USB.
First boot takes a few minutes; the OLED says so while it works.

The panel shows FOUR cards. Write down card 3 (Web, user admin) -- that
is the one needed to run the test suite. KEY1 erases them when you are
done.
EOF
