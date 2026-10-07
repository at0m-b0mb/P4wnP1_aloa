#!/usr/bin/env bash
#
# build.sh -- produce a flashable P4wnP1 A.L.O.A. Raspberry Pi OS image.
#
# Runs on macOS or Linux. All the privileged work (loop devices, mounting,
# foreign-architecture chroot) happens inside a container, so the host only
# needs Docker and a Go toolchain.
#
#   ./image/build.sh --arch armhf      # Pi Zero / Zero W / Pi 1  (ARMv6)
#   ./image/build.sh --arch arm64      # Pi Zero 2 W / 3 / 4 / 5  (ARMv8)
#   ./image/build.sh --arch all
#
# Output: image/out/P4wnP1-ALOA-<version>-<arch>.img.xz + .sha256
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

# --- configuration ---------------------------------------------------------
PIOS_DATE="${PIOS_DATE:-2026-09-15}"
PIOS_MIRROR="${PIOS_MIRROR:-https://downloads.raspberrypi.com}"
VERSION="${P4_VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}"

ARCHES=""
GROW_MB=1536
SHRINK=1
COMPRESS=1
SSH_USER=p4wnp1
SSH_PASS=p4wnp1
WIFI_COUNTRY=US
BUILDER_TAG=p4wnp1-imgbuilder:1

# These live inside the repo on purpose: the builder container bind-mounts the
# repo at /repo, so repo-relative paths are the only ones guaranteed to resolve
# identically on both sides of the mount. Both dirs are gitignored.
WORK="$REPO_ROOT/image/out"
CACHE="$REPO_ROOT/image/cache"

c()    { printf '\033[1;35m==>\033[0m \033[1m%s\033[0m\n' "$*"; }
info() { printf '    %s\n' "$*"; }
die()  { printf '\033[1;31mERROR:\033[0m %s\n' "$*" >&2; exit 1; }

usage() {
    sed -n '3,14p' "${BASH_SOURCE[0]}" | sed 's/^# \?//'
    cat <<EOF

Options:
  --arch ARCH         armhf | arm64 | all          (required)
  --grow-mb N         extra megabytes for the rootfs (default $GROW_MB)
  --no-shrink         skip shrinking the finished image
  --no-compress       leave a raw .img instead of .img.xz
  --ssh-user NAME     operator account name        (default $SSH_USER)
  --ssh-pass PASS     operator initial password    (default $SSH_PASS; a
                      password change is forced at first login either way)
  --wifi-country CC   regulatory domain            (default $WIFI_COUNTRY)
  --pios-date DATE    Raspberry Pi OS release      (default $PIOS_DATE)
EOF
}

while [ $# -gt 0 ]; do
    case "$1" in
        --arch)          ARCHES="$2"; shift 2 ;;
        --grow-mb)       GROW_MB="$2"; shift 2 ;;
        --no-shrink)     SHRINK=0; shift ;;
        --no-compress)   COMPRESS=0; shift ;;
        --ssh-user)      SSH_USER="$2"; shift 2 ;;
        --ssh-pass)      SSH_PASS="$2"; shift 2 ;;
        --wifi-country)  WIFI_COUNTRY="$2"; shift 2 ;;
        --pios-date)     PIOS_DATE="$2"; shift 2 ;;
        -h|--help)       usage; exit 0 ;;
        *) die "unknown option: $1 (try --help)" ;;
    esac
done
[ -n "$ARCHES" ] || { usage; die "--arch is required"; }
[ "$ARCHES" = "all" ] && ARCHES="armhf arm64"

# --- preflight -------------------------------------------------------------
c "Preflight"
command -v docker >/dev/null || die "docker not found"
docker info >/dev/null 2>&1 || die "cannot reach the Docker daemon. On macOS with Colima: colima start"
command -v go >/dev/null || die "go toolchain not found (needed to cross-compile the binaries)"
info "docker: $(docker info --format '{{.ServerVersion}} {{.OSType}}/{{.Architecture}}' 2>/dev/null)"
info "go:     $(go version | awk '{print $3}')"
info "version stamp: $VERSION"

# Docker on macOS can only bind-mount paths the VM shares -- $HOME by default.
case "$REPO_ROOT" in
    "$HOME"/*) ;;
    *) info "NOTE: repo is outside \$HOME; if the bind mount fails, move the checkout under \$HOME" ;;
esac

c "Registering QEMU binfmt handlers"
# The F (fix-binary) flag is what lets a foreign-arch interpreter survive the
# chroot into the Pi rootfs. tonistiigi/binfmt installs handlers with it.
docker run --privileged --rm tonistiigi/binfmt:latest --install arm64,arm >/dev/null 2>&1 \
    || info "binfmt install reported a problem; continuing (handlers may already be present)"

c "Building the builder container ($BUILDER_TAG)"
docker build -q -t "$BUILDER_TAG" -f image/Dockerfile image/ >/dev/null \
    || die "failed to build the builder image"
info "ok"

mkdir -p "$WORK" "$CACHE"

# --- per-architecture build ------------------------------------------------
for ARCH in $ARCHES; do
    case "$ARCH" in
        armhf) GOARCH=arm;   GOARM=6; BOARDS="Pi Zero, Zero W, Pi 1, Pi 2 (32-bit)" ;;
        arm64) GOARCH=arm64; GOARM="";  BOARDS="Pi Zero 2 W, Pi 3, Pi 4, Pi 5" ;;
        *) die "unsupported --arch '$ARCH'" ;;
    esac

    c "[$ARCH] Cross-compiling P4wnP1 binaries  ($BOARDS)"
    BINDIR="$REPO_ROOT/image/out/bin/$ARCH"
    mkdir -p "$BINDIR"
    for pkg in P4wnP1_service P4wnP1_cli p4wnp1-hashpw; do
        info "building $pkg"
        env CGO_ENABLED=0 GOOS=linux GOARCH="$GOARCH" ${GOARM:+GOARM=$GOARM} \
            go build -trimpath -ldflags "-s -w -X main.version=$VERSION" \
            -o "$BINDIR/$pkg" "./cmd/$pkg" \
            || die "failed to build $pkg for $ARCH"
    done
    ls -la "$BINDIR" | tail -n +2 | awk '{printf "    %-20s %s\n", $9, $5}'

    # --- base image ---
    IMG_NAME="${PIOS_DATE}-raspios-trixie-${ARCH}-lite.img"
    XZ="$CACHE/$IMG_NAME.xz"
    RAW="$CACHE/$IMG_NAME"
    if [ ! -f "$RAW" ]; then
        if [ ! -f "$XZ" ]; then
            c "[$ARCH] Downloading Raspberry Pi OS Lite ($PIOS_DATE)"
            URL="$PIOS_MIRROR/raspios_lite_${ARCH}/images/raspios_lite_${ARCH}-${PIOS_DATE}/${IMG_NAME}.xz"
            info "$URL"
            curl -fL --progress-bar -o "$XZ.part" "$URL" || die "download failed: $URL"
            curl -fsL -o "$XZ.sha256" "$URL.sha256" || die "checksum download failed"
            mv "$XZ.part" "$XZ"
            c "[$ARCH] Verifying checksum"
            ( cd "$CACHE" && sha256sum -c "$(basename "$XZ.sha256")" ) \
                || ( cd "$CACHE" && shasum -a 256 -c "$(basename "$XZ.sha256")" ) \
                || die "CHECKSUM MISMATCH on $XZ -- refusing to build from it"
            info "checksum ok"
        fi
        c "[$ARCH] Decompressing base image"
        xz -dk -T0 "$XZ" || die "decompress failed"
    fi
    info "base image: $RAW ($(du -h "$RAW" | cut -f1))"

    OUT_IMG="$WORK/P4wnP1-ALOA-${VERSION}-${ARCH}.img"

    c "[$ARCH] Building image (privileged container)"
    docker run --rm --privileged \
        -v /dev:/dev \
        -v "$REPO_ROOT:/repo" \
        -e P4_ARCH="$ARCH" \
        -e P4_BASE_IMG="/repo/image/cache/$IMG_NAME" \
        -e P4_OUT="/repo/image/out/$(basename "$OUT_IMG")" \
        -e P4_REPO=/repo \
        -e P4_GROW_MB="$GROW_MB" \
        -e P4_SHRINK="$SHRINK" \
        -e P4_SSH_USER="$SSH_USER" \
        -e P4_SSH_PASS="$SSH_PASS" \
        -e P4_WIFI_COUNTRY="$WIFI_COUNTRY" \
        "$BUILDER_TAG" /repo/image/lib/stage.sh \
        || die "image build failed for $ARCH"

    [ -f "$OUT_IMG" ] || die "stage.sh reported success but $OUT_IMG is missing"

    # Verify BEFORE compressing.
    #
    # A build once produced 2.6GB of pure zeros, compressed it to 410KB, wrote
    # a checksum for it and reported success. stage.sh had done everything
    # correctly inside the container; the host read the file through the VM's
    # shared filesystem before those writes were visible and compressed what it
    # saw. Nothing caught it because nothing had ever looked at the output.
    #
    # `sync` on the host first, then mount the image in a container and check
    # it really contains what it should.
    sync 2>/dev/null || true
    c "[$ARCH] Verifying the built image"
    docker run --rm --privileged \
        -v /dev:/dev \
        -v "$REPO_ROOT:/repo" \
        -e P4_ARCH="$ARCH" \
        "$BUILDER_TAG" /repo/image/lib/verify.sh "/repo/image/out/$(basename "$OUT_IMG")" \
        || die "the built image failed verification; not publishing it"

    if [ "$COMPRESS" = "1" ]; then
        c "[$ARCH] Compressing"
        rm -f "$OUT_IMG.xz"
        xz -T0 -6 "$OUT_IMG" || die "xz failed"
        OUT_FINAL="$OUT_IMG.xz"
    else
        OUT_FINAL="$OUT_IMG"
    fi

    ( cd "$(dirname "$OUT_FINAL")" && \
      { sha256sum "$(basename "$OUT_FINAL")" 2>/dev/null || shasum -a 256 "$(basename "$OUT_FINAL")"; } \
      > "$(basename "$OUT_FINAL").sha256" )

    c "[$ARCH] DONE"
    info "$OUT_FINAL ($(du -h "$OUT_FINAL" | cut -f1))"
    info "$(cat "$OUT_FINAL.sha256")"
done

c "All builds complete"
ls -la "$WORK"/*.img* 2>/dev/null | awk '{printf "    %-60s %s\n", $9, $5}'
