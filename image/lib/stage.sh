#!/bin/bash
#
# stage.sh -- image surgery. Runs INSIDE the builder container (see
# image/Dockerfile) with --privileged and /dev bind-mounted, because it needs
# loop devices and mount(2).
#
# It never recreates the partition table: partition 2 is grown in place so the
# PARTUUID in cmdline.txt and /etc/fstab stays valid. Recreating partitions
# would change the disk identifier and leave an unbootable image.
#
# Inputs (environment):
#   P4_ARCH      armhf | arm64
#   P4_BASE_IMG  path to the decompressed base .img
#   P4_OUT       path to write the finished .img
#   P4_REPO      path to the P4wnP1 repo checkout
#   P4_GROW_MB   megabytes to add to the root partition (default 1536)
#   P4_SHRINK    1 = shrink the result to minimum + slack before handing back
set -euo pipefail

ARCH="${P4_ARCH:?P4_ARCH required}"
BASE_IMG="${P4_BASE_IMG:?P4_BASE_IMG required}"
OUT="${P4_OUT:?P4_OUT required}"
REPO="${P4_REPO:?P4_REPO required}"
GROW_MB="${P4_GROW_MB:-1536}"
SHRINK="${P4_SHRINK:-1}"
SLACK_MB=48          # free space left in the shrunk image

MNT=/mnt/p4wnp1
LOOP=""

log()  { printf '\033[1;36m[stage]\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m[stage:warn]\033[0m %s\n' "$*"; }
die()  { printf '\033[1;31m[stage:FATAL]\033[0m %s\n' "$*" >&2; exit 1; }

cleanup() {
    local rc=$?
    set +e
    # Order matters: deepest mount first.
    umount -lf "$MNT/boot/firmware" 2>/dev/null
    for d in dev/pts dev sys proc; do umount -lf "$MNT/$d" 2>/dev/null; done
    umount -lf "$MNT" 2>/dev/null
    [ -n "$LOOP" ] && losetup -d "$LOOP" 2>/dev/null
    [ $rc -ne 0 ] && warn "stage.sh exiting with code $rc"
    return $rc
}
trap cleanup EXIT

# --------------------------------------------------------------------------
# 0. sanity
# --------------------------------------------------------------------------
[ -f "$BASE_IMG" ] || die "base image not found: $BASE_IMG"
command -v losetup >/dev/null || die "losetup missing"
ls /dev/loop-control >/dev/null 2>&1 || die "no /dev/loop-control -- run the container with --privileged -v /dev:/dev"

case "$ARCH" in
    armhf) BINFMT=qemu-arm ;;
    arm64) BINFMT=qemu-aarch64 ;;
    *) die "unsupported P4_ARCH '$ARCH' (want armhf or arm64)" ;;
esac

# If the rootfs architecture differs from the host, we need a registered
# binfmt handler with the F (fix-binary) flag so the interpreter survives the
# chroot. Without F we would have to copy a static qemu into the rootfs.
HOST_ARCH="$(dpkg --print-architecture)"
if [ "$ARCH" != "$HOST_ARCH" ]; then
    if [ ! -e "/proc/sys/fs/binfmt_misc/$BINFMT" ]; then
        mount -t binfmt_misc binfmt_misc /proc/sys/fs/binfmt_misc 2>/dev/null || true
    fi
    [ -e "/proc/sys/fs/binfmt_misc/$BINFMT" ] || \
        die "binfmt handler $BINFMT not registered on the host kernel. Run:
    docker run --privileged --rm tonistiigi/binfmt --install arm64,arm"
    if ! grep -q 'flags:.*F' "/proc/sys/fs/binfmt_misc/$BINFMT"; then
        warn "$BINFMT is registered WITHOUT the F flag; chroot may fail with 'exec format error'."
    fi
    log "foreign-arch build: $HOST_ARCH host -> $ARCH rootfs via $BINFMT"
fi

# --------------------------------------------------------------------------
# 1. copy + grow the image
# --------------------------------------------------------------------------
log "copying base image -> $OUT"
rm -f "$OUT"
cp --sparse=always "$BASE_IMG" "$OUT"

log "growing image file by ${GROW_MB}MB"
truncate -s "+${GROW_MB}M" "$OUT"

LOOP="$(losetup --find --show --partscan "$OUT")"
log "attached $LOOP"
[ -b "${LOOP}p2" ] || die "no ${LOOP}p2 -- partition scan failed"

log "growing partition 2 to fill the image"
# parted prints a cosmetic warning about the backup GPT/MBR not matching after
# truncate; -s accepts it. 'resizepart' keeps the MBR disk id intact.
parted -s "$LOOP" resizepart 2 100% || die "parted resizepart failed"
partprobe "$LOOP" 2>/dev/null || true
sleep 1

log "fsck + resize2fs"
e2fsck -pf "${LOOP}p2" || e2fsck -fy "${LOOP}p2" || true
resize2fs "${LOOP}p2" || die "resize2fs failed"

# --------------------------------------------------------------------------
# 2. mount
# --------------------------------------------------------------------------
mkdir -p "$MNT"
mount "${LOOP}p2" "$MNT" || die "mount rootfs failed"
mkdir -p "$MNT/boot/firmware"
mount "${LOOP}p1" "$MNT/boot/firmware" || die "mount boot failed"

log "rootfs: $(grep -oP 'PRETTY_NAME="\K[^"]+' "$MNT/etc/os-release" 2>/dev/null || echo unknown)"
log "free space on rootfs: $(df -h "$MNT" | awk 'NR==2{print $4}')"

for d in proc sys dev dev/pts; do
    mkdir -p "$MNT/$d"
    mount --bind "/$d" "$MNT/$d" || die "bind mount /$d failed"
done

# Give the chroot working DNS for apt. Pi OS ships /etc/resolv.conf as a
# symlink into /run on some builds, so replace rather than append.
cp -f /etc/resolv.conf "$MNT/etc/resolv.conf" 2>/dev/null || true

# --------------------------------------------------------------------------
# 3. stage the payload the chroot script will install
# --------------------------------------------------------------------------
PAYLOAD="$MNT/tmp/p4wnp1-payload"
log "staging payload -> /tmp/p4wnp1-payload"
rm -rf "$PAYLOAD"
mkdir -p "$PAYLOAD"
rsync -a --exclude '.git' \
      "$REPO/dist"    "$PAYLOAD/" 2>/dev/null || die "rsync dist failed"
mkdir -p "$PAYLOAD/bin"
for b in P4wnP1_service P4wnP1_cli p4wnp1-hashpw p4wnp1-oled; do
    src="$REPO/image/out/bin/$ARCH/$b"
    [ -f "$src" ] || die "missing built binary $src (run image/build.sh, which cross-compiles first)"
    install -m 0755 "$src" "$PAYLOAD/bin/$b"
done
file "$PAYLOAD/bin/P4wnP1_service" | sed 's/^/[stage] binary: /'
install -m 0755 "$REPO/image/lib/customize.sh" "$PAYLOAD/customize.sh"
[ -d "$REPO/image/overlay/common" ] && rsync -a "$REPO/image/overlay/common/" "$PAYLOAD/overlay/" 2>/dev/null || true

# --------------------------------------------------------------------------
# 4. run the in-chroot customisation
# --------------------------------------------------------------------------
log "entering chroot to customise ($ARCH)"
chroot "$MNT" /bin/bash -c "P4_ARCH='$ARCH' /tmp/p4wnp1-payload/customize.sh" \
    || die "in-chroot customisation failed"

# --------------------------------------------------------------------------
# 5. boot-partition configuration (done from outside the chroot: these files
#    live on the FAT partition and have no dependency on the guest userland)
# --------------------------------------------------------------------------
BOOT="$MNT/boot/firmware"
CFG="$BOOT/config.txt"
CMD="$BOOT/cmdline.txt"

log "configuring $CFG for USB gadget mode (dwc2)"
if ! grep -q '^# --- P4wnP1' "$CFG" 2>/dev/null; then
    cat >> "$CFG" <<'EOF'

# --- P4wnP1 A.L.O.A. -------------------------------------------------------
# dwc2 in peripheral/OTG mode is what lets the Pi present itself to a host as
# a USB device (HID keyboard/mouse, ethernet, mass storage). Without this
# overlay the USB gadget subsystem has no UDC to bind to and NOTHING in
# P4wnP1's USB feature set works.
[all]
dtoverlay=dwc2,dr_mode=peripheral
# Headless appliance: no HDMI, faster boot, less power draw.
disable_splash=1
# Keep the UART console available -- it is the recovery path if USB and WiFi
# are both misconfigured.
enable_uart=1
# --- end P4wnP1 ------------------------------------------------------------
EOF
else
    log "config.txt already carries a P4wnP1 block; leaving it alone"
fi

log "configuring $CMD"
if ! grep -q 'modules-load=dwc2' "$CMD"; then
    # cmdline.txt MUST stay a single line. Append our token, preserving the
    # existing contents (including the PARTUUID root= and the 'resize' token
    # that triggers Pi OS's first-boot filesystem expansion).
    tr -d '\n' < "$CMD" > "$CMD.new"
    printf ' modules-load=dwc2\n' >> "$CMD.new"
    mv "$CMD.new" "$CMD"
fi
log "cmdline.txt -> $(cat "$CMD")"

# Headless SSH on first boot.
touch "$BOOT/ssh"

# --------------------------------------------------------------------------
# 6. teardown
# --------------------------------------------------------------------------
log "cleaning payload staging dir"
rm -rf "$PAYLOAD"

log "zeroing free space (improves compression)"
# dd to a file then delete: writes zeros over unallocated blocks so xz can
# squash them. || true because it is expected to fail with ENOSPC.
dd if=/dev/zero of="$MNT/.zerofill" bs=4M status=none 2>/dev/null || true
rm -f "$MNT/.zerofill"
sync

umount -lf "$MNT/boot/firmware"
for d in dev/pts dev sys proc; do umount -lf "$MNT/$d"; done
umount "$MNT" || umount -lf "$MNT"

if [ "$SHRINK" = "1" ]; then
    log "shrinking image"
    e2fsck -pf "${LOOP}p2" || e2fsck -fy "${LOOP}p2" || true
    resize2fs -M "${LOOP}p2" || die "resize2fs -M failed"

    BLK_SIZE=$(tune2fs -l "${LOOP}p2" | awk -F: '/Block size/{gsub(/ /,"",$2);print $2}')
    BLK_CNT=$(tune2fs -l "${LOOP}p2" | awk -F: '/Block count/{gsub(/ /,"",$2);print $2}')
    # Grow back by SLACK_MB so the first boot has somewhere to write before
    # Pi OS's auto-expand runs.
    SLACK_BLKS=$(( SLACK_MB * 1024 * 1024 / BLK_SIZE ))
    resize2fs "${LOOP}p2" $(( BLK_CNT + SLACK_BLKS )) || warn "slack regrow failed"
    BLK_CNT=$(tune2fs -l "${LOOP}p2" | awk -F: '/Block count/{gsub(/ /,"",$2);print $2}')

    FS_BYTES=$(( BLK_CNT * BLK_SIZE ))
    P2_START=$(partx -g -o START -n 2 "$LOOP" | tr -d ' ')
    P2_SECTORS=$(( FS_BYTES / 512 ))
    P2_END=$(( P2_START + P2_SECTORS - 1 ))

    # Shrink partition 2 to match the shrunken filesystem.
    #
    # `parted resizepart` is NOT usable here: it asks "Shrinking a partition can
    # cause data loss, are you sure?" on stdin and -s does not suppress that
    # prompt, so it silently fails and leaves the table claiming the partition
    # extends past the end of the (truncated) file. sfdisk -N takes the change
    # non-interactively. The leading comma keeps the existing start sector, so
    # the MBR disk identifier -- and therefore the PARTUUID that cmdline.txt and
    # /etc/fstab reference -- is preserved.
    log "resizing partition 2 to $P2_SECTORS sectors (ends at $P2_END)"
    if echo ",${P2_SECTORS}" | sfdisk --no-reread --no-tell-kernel -N 2 "$LOOP" >/dev/null 2>&1; then
        log "  partition table updated"
    else
        die "sfdisk failed to shrink partition 2; refusing to truncate the image (that would leave a corrupt partition table)"
    fi

    # Verify the table really says what we think before we cut the file short.
    ACTUAL_END=$(partx -g -o END -n 2 "$LOOP" 2>/dev/null | tr -d ' ')
    if [ -n "$ACTUAL_END" ] && [ "$ACTUAL_END" != "$P2_END" ]; then
        die "partition 2 ends at sector $ACTUAL_END but we expected $P2_END; refusing to truncate"
    fi
    log "  verified: partition 2 ends at sector $ACTUAL_END"

    losetup -d "$LOOP"; LOOP=""
    IMG_BYTES=$(( (P2_END + 1) * 512 ))
    truncate -s "$IMG_BYTES" "$OUT"
    log "shrunk to $(numfmt --to=iec "$IMG_BYTES" 2>/dev/null || echo "$IMG_BYTES bytes")"
fi

log "DONE -> $OUT ($(du -h "$OUT" | cut -f1))"
