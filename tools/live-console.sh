#!/usr/bin/env bash
#
# live-console.sh -- serve the REAL console from the REAL service binary on
# your laptop, with no Raspberry Pi attached.
#
# `make mock` serves the console against a Python stand-in. That is fast, but a
# mock can disagree with the service, and twice has: once it emitted camelCase
# field names while the real protojson emitted proto names (every USB setting
# silently read as empty), once it accepted an RPC payload shape the service
# rejects. This runs the actual compiled service, so what you click is what the
# device does.
#
# Hardware-backed features -- USB gadget, WiFi, Bluetooth, HID -- will report
# that they are unavailable. That is correct here and is exactly what
# tools/feature-test.py asserts. Everything else is live.
set -euo pipefail

ARCH="${1:-arm64}"
PORT="${PORT:-8000}"
case "$ARCH" in
    arm64) PLATFORM=linux/arm64 ;;
    armhf) PLATFORM=linux/arm/v7 ;;
    *) echo "usage: $0 [armhf|arm64]   (PORT=8000)" >&2; exit 2 ;;
esac

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

BIN="image/out/bin/$ARCH"
if [ ! -x "$BIN/P4wnP1_service" ]; then
    echo "==> building binaries for $ARCH"
    mkdir -p "$BIN"
    for b in P4wnP1_service P4wnP1_cli p4wnp1-hashpw; do
        if [ "$ARCH" = arm64 ]; then
            CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o "$BIN/$b" "./cmd/$b"
        else
            CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=6 go build -o "$BIN/$b" "./cmd/$b"
        fi
    done
fi

PASSWORD="${P4WNP1_DEV_PASSWORD:-live-console-password}"

cat <<BANNER

  P4wnP1 A.L.O.A. console, live from the real service
  ---------------------------------------------------
    http://127.0.0.1:$PORT/app/
    username  admin
    password  $PASSWORD

  Ctrl-C to stop.

BANNER

# -t only when there really is a terminal, so this also works under nohup / CI.
# The array starts non-empty: expanding an empty one under `set -u` is an error
# in bash 3.2, which is what macOS ships.
DOCKER_TTY=(-i)
if [ -t 0 ] && [ -t 1 ]; then DOCKER_TTY+=(-t); fi

exec docker run --rm "${DOCKER_TTY[@]}" --init --platform "$PLATFORM" --privileged \
    -p "127.0.0.1:$PORT:8000" \
    -v "$REPO_ROOT:/repo:ro" \
    -e ARCH="$ARCH" -e PASSWORD="$PASSWORD" \
    debian:bookworm-slim bash -s <<'IN_CONTAINER'
set -euo pipefail
mkdir -p /usr/local/P4wnP1 /etc/p4wnp1
install -m 0755 /repo/image/out/bin/"$ARCH"/P4wnP1_service /usr/local/bin/
install -m 0755 /repo/image/out/bin/"$ARCH"/P4wnP1_cli /usr/local/bin/
install -m 0755 /repo/image/out/bin/"$ARCH"/p4wnp1-hashpw /usr/local/bin/
cp -R /repo/dist/keymaps /repo/dist/scripts /repo/dist/HIDScripts \
      /repo/dist/www /repo/dist/db /usr/local/P4wnP1/ 2>/dev/null || true
printf '%s' "$PASSWORD" | /usr/local/bin/p4wnp1-hashpw --username admin >/dev/null
exec /usr/local/bin/P4wnP1_service
IN_CONTAINER
