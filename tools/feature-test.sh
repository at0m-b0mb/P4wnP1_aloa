#!/usr/bin/env bash
#
# feature-test.sh -- start the real service in a container and exercise every RPC.
#
# make smoke proves the service comes up. This proves the FEATURES work. See
# tools/feature-test.py for what each verdict means.
set -euo pipefail

ARCH="${1:-arm64}"
case "$ARCH" in
    arm64) PLATFORM=linux/arm64 ;;
    armhf) PLATFORM=linux/arm/v7 ;;
    *) echo "usage: $0 [armhf|arm64]" >&2; exit 2 ;;
esac

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

BIN="image/out/bin/$ARCH"
# ALWAYS rebuild.
#
# This used to be `if [ ! -x "$BIN/P4wnP1_service" ]`, which builds only when
# the binary is MISSING -- never when it is merely out of date. image/out is
# gitignored and nothing cleans it, so the binary sat there for days while
# this suite reported on it as though it were current. It was caught when
# three freshly added endpoint checks came back 404: the routes existed in the
# source and in the running device, and simply were not in the two-day-old
# build being tested.
#
# That is the worst way for a test to fail -- it does not. It passes, loudly,
# about code nobody is shipping. `go build` is incremental and costs a second
# or two on a warm cache, which is nothing next to a suite that lies.
echo "==> building binaries for $ARCH (always, so the suite tests THIS source)"
mkdir -p "$BIN"
if true; then
    for b in P4wnP1_service P4wnP1_cli p4wnp1-hashpw; do
        if [ "$ARCH" = arm64 ]; then
            CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o "$BIN/$b" "./cmd/$b"
        else
            CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=6 go build -o "$BIN/$b" "./cmd/$b"
        fi
    done
fi

echo "==> exercising every RPC in a $PLATFORM container"
docker run --rm -i --platform "$PLATFORM" --privileged -v "$REPO_ROOT:/repo:ro" \
    -e ARCH="$ARCH" debian:bookworm-slim bash -s <<'IN_CONTAINER'
set -uo pipefail
apt-get update -qq >/dev/null 2>&1
apt-get install -y -qq python3 >/dev/null 2>&1

mkdir -p /usr/local/P4wnP1 /etc/p4wnp1
install -m 0755 /repo/image/out/bin/"$ARCH"/{P4wnP1_service,P4wnP1_cli,p4wnp1-hashpw} /usr/local/bin/
cp -R /repo/dist/{keymaps,scripts,HIDScripts,www,db} /usr/local/P4wnP1/ 2>/dev/null || true

printf '%s' "feature-test-password-long" | /usr/local/bin/p4wnp1-hashpw --username admin >/dev/null 2>&1

/usr/local/bin/P4wnP1_service > /tmp/svc.log 2>&1 &
SVC=$!
for i in $(seq 1 45); do
    python3 -c "
import urllib.request,sys
try: urllib.request.urlopen('http://127.0.0.1:8000/',timeout=1); sys.exit(0)
except Exception: sys.exit(1)" 2>/dev/null && break
    sleep 1
done

python3 /repo/tools/feature-test.py
rc=$?

# A crash during the run is itself a failure, however the checks scored.
if ! kill -0 "$SVC" 2>/dev/null; then
    echo
    echo "  THE SERVICE DIED DURING THE RUN -- last log lines:"
    tail -25 /tmp/svc.log | sed 's/^/    /'
    rc=1
fi
if grep -q 'panic' /tmp/svc.log; then
    echo
    echo "  PANIC IN THE SERVICE LOG:"
    grep -A12 'panic' /tmp/svc.log | head -20 | sed 's/^/    /'
    rc=1
fi
kill -TERM "$SVC" 2>/dev/null
exit $rc
IN_CONTAINER
