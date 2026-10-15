#!/usr/bin/env bash
#
# build-update-bundle.sh -- produce the tarball p4wnp1-update installs.
#
#   ./tools/build-update-bundle.sh --arch armhf
#   ./tools/build-update-bundle.sh --arch all
#
# Output: image/out/P4wnP1-ALOA-<version>-update-<arch>.tar.xz + .sha256
#
# WHY A BUNDLE AND NOT THE IMAGE
#
# The release ships 650MB images, and flashing one costs the operator every
# template, payload and mass-storage image they had, plus their per-device
# credentials -- a fresh first boot regenerates those, so the card they wrote
# down becomes wrong. For an existing install that is a terrible trade for a
# bug fix, and an appliance nobody updates stays on the version it shipped with.
#
# This is the same content minus the operating system: about 25MB, and
# p4wnp1-update applies it in place while leaving live state alone.
#
# WHAT GOES IN, AND WHAT DELIBERATELY DOES NOT
#
#   bin/      the four binaries, plus p4wnp1-update itself so it can update
#   units/    the systemd units
#   www/      the web console
#   keymaps/  layout tables
#   scripts/  shipped bash, including the updater's source
#   HIDScripts/ shipped payloads
#   db/init.db  the factory template database
#   VERSION, ARCH  so the updater can refuse a bundle for the wrong board
#
# NOT included, because they are the operator's and an update must not carry
# them: store/ (the live datastore), ums/ (their images), /etc/p4wnp1 (their
# credentials), and their own .db backups.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

ARCHES=""
VERSION="${P4_VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}"

while [ $# -gt 0 ]; do
    case "$1" in
        --arch) ARCHES="$2"; shift 2 ;;
        --version) VERSION="$2"; shift 2 ;;
        -h|--help) sed -n '2,8p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
        *) echo "unknown argument: $1" >&2; exit 2 ;;
    esac
done
[ -n "$ARCHES" ] || { echo "--arch is required (armhf | arm64 | all)" >&2; exit 2; }
[ "$ARCHES" = "all" ] && ARCHES="armhf arm64"

say() { printf '\033[1;32m[bundle]\033[0m %s\n' "$*"; }
die() { printf '\033[1;31m[bundle:FAILED]\033[0m %s\n' "$*" >&2; exit 1; }

# A bundle built from a dirty tree is not reproducible and should never reach a
# release. The image build stamps "-dirty" into the name for the same reason;
# here it is a hard stop, because an update bundle is applied to someone else's
# running device.
case "$VERSION" in
    *-dirty) die "the working tree is dirty ($VERSION). Commit first -- an update bundle
goes onto someone else's device and must be reproducible from a tag." ;;
esac

mkdir -p image/out

for ARCH in $ARCHES; do
    case "$ARCH" in
        armhf) GOOS=linux GOARCH=arm   GOARM=6 ; MAKETGT=build-armv6 ; BIN=build ;;
        arm64) GOOS=linux GOARCH=arm64 GOARM="" ; MAKETGT=build-arm64 ; BIN=build/arm64 ;;
        *) die "unknown arch $ARCH" ;;
    esac

    say "building binaries for $ARCH"
    make "$MAKETGT" >/dev/null || die "make $MAKETGT failed"

    STAGE=$(mktemp -d); trap 'rm -rf "$STAGE"' EXIT
    mkdir -p "$STAGE"/{bin,units,db}

    for b in P4wnP1_service P4wnP1_cli p4wnp1-hashpw p4wnp1-oled; do
        [ -f "$BIN/$b" ] || die "$BIN/$b was not produced"
        cp "$BIN/$b" "$STAGE/bin/"
    done
    # The updater ships inside the bundle so it can improve itself.
    cp dist/scripts/p4wnp1-update "$STAGE/bin/p4wnp1-update"
    chmod 0755 "$STAGE"/bin/*

    for u in P4wnP1.service p4wnp1-firstboot.service p4wnp1-oled.service; do
        [ -f "dist/$u" ] && cp "dist/$u" "$STAGE/units/"
    done

    cp -R dist/www      "$STAGE/"
    cp -R dist/keymaps  "$STAGE/"
    cp -R dist/scripts  "$STAGE/"
    cp -R dist/HIDScripts "$STAGE/"
    [ -f dist/db/init.db ] && cp dist/db/init.db "$STAGE/db/init.db"

    # macOS tar writes AppleDouble sidecars (._name) unless told not to. They
    # end in .js, so ListFilesOfFolder listed fifteen of them beside fifteen
    # real payloads on a device. Found by doing exactly that.
    find "$STAGE" -name '._*' -delete 2>/dev/null || true
    find "$STAGE" -name '.DS_Store' -delete 2>/dev/null || true

    echo "$VERSION" > "$STAGE/VERSION"
    echo "$ARCH"    > "$STAGE/ARCH"
    # And inside the web root, because that is the only part of the install the
    # console can actually fetch. Without this the device knows its version and
    # cannot tell anyone: the first real update left www/VERSION ABSENT while
    # $P4ROOT/VERSION was correct, and the updater REPLACES www/ wholesale, so
    # an install-time copy would have been deleted rather than preserved.
    echo "$VERSION" > "$STAGE/www/VERSION"

    OUT="image/out/P4wnP1-ALOA-${VERSION}-update-${ARCH}.tar.xz"
    say "packing $OUT"
    # COPYFILE_DISABLE stops macOS tar re-adding the sidecars just removed.
    COPYFILE_DISABLE=1 tar cJf "$OUT" -C "$STAGE" .
    ( cd image/out && shasum -a 256 "$(basename "$OUT")" > "$(basename "$OUT").sha256" )

    say "  $(wc -c < "$OUT" | tr -d ' ') bytes"
    say "  $(awk '{print substr($1,1,16)}' "$OUT.sha256")"
    rm -rf "$STAGE"; trap - EXIT
done

say "done"
