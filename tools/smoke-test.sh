#!/usr/bin/env bash
#
# smoke-test.sh -- run the real service binary against the real dist tree in a
# container, and check the things that actually have to work.
#
# This exists because "it compiles" and "the unit tests pass" both stayed true
# while the service panicked on every cold boot. The panic was on a success
# path, in a constructor, behind a modprobe that made a manual restart look
# healthy -- nothing short of starting the binary would have caught it.
#
# A container has no configfs, no UDC and no radio, so this cannot tell you the
# USB gadget works. What it CAN tell you is that the service survives the
# absence of all of that, comes up, serves, authenticates, and shuts down
# cleanly -- which is the entire class of bug that made this project unusable.
#
# Usage: ./tools/smoke-test.sh [armhf|arm64]      (default: arm64)
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
if [ ! -x "$BIN/P4wnP1_service" ]; then
    echo "==> building binaries for $ARCH"
    mkdir -p "$BIN"
    for b in P4wnP1_service P4wnP1_cli p4wnp1-hashpw; do
        CGO_ENABLED=0 GOOS=linux GOARCH="$([ "$ARCH" = arm64 ] && echo arm64 || echo arm)" \
            ${ARCH:+$([ "$ARCH" = armhf ] && echo GOARM=6)} \
            go build -o "$BIN/$b" "./cmd/$b"
    done
fi

echo "==> running the smoke test in a $PLATFORM container"
docker run --rm -i --platform "$PLATFORM" --privileged -v "$REPO_ROOT:/repo:ro" \
    -e ARCH="$ARCH" debian:bookworm-slim bash -s <<'IN_CONTAINER'
set -uo pipefail
PASS=0; FAIL=0
ok()   { printf '  \033[1;32mPASS\033[0m  %s\n' "$1"; PASS=$((PASS+1)); }
bad()  { printf '  \033[1;31mFAIL\033[0m  %s  (%s)\n' "$1" "$2"; FAIL=$((FAIL+1)); }
check(){ # check <description> <expected> <actual>
    [ "$2" = "$3" ] && ok "$1" || bad "$1" "expected $2, got $3"; }

apt-get update -qq >/dev/null 2>&1
apt-get install -y -qq curl >/dev/null 2>&1

mkdir -p /usr/local/P4wnP1 /etc/p4wnp1
install -m 0755 /repo/image/out/bin/"$ARCH"/{P4wnP1_service,P4wnP1_cli,p4wnp1-hashpw} /usr/local/bin/
cp -R /repo/dist/{keymaps,scripts,HIDScripts,www,db} /usr/local/P4wnP1/ 2>/dev/null || true

PW="smoke-test-password-long-enough"
printf '%s' "$PW" | /usr/local/bin/p4wnp1-hashpw --username admin >/dev/null 2>&1
check "firstboot bootstrap writes auth.json 0600" "600" "$(stat -c %a /etc/p4wnp1/auth.json 2>/dev/null)"

/usr/local/bin/P4wnP1_service > /tmp/svc.log 2>&1 &
SVC=$!
for i in $(seq 1 40); do
    curl -sf -o /dev/null http://127.0.0.1:8000/ 2>/dev/null && break
    sleep 1
done

# The single most important assertion in this file: the process is still here.
kill -0 "$SVC" 2>/dev/null && ok "service survived startup without hardware" \
                           || bad "service survived startup without hardware" "it exited -- see log below"

grep -q "service initialized" /tmp/svc.log && ok "reached 'service initialized'" \
                                           || bad "reached 'service initialized'" "not in log"
grep -q "gRPC server listening"     /tmp/svc.log && ok "gRPC listener up"  || bad "gRPC listener up" "absent"
grep -q "gRPC-web server listening" /tmp/svc.log && ok "HTTP listener up"  || bad "HTTP listener up" "absent"
grep -q "panic" /tmp/svc.log && bad "no panic in the log" "found one" || ok "no panic in the log"

code(){ curl -s -o /dev/null -w '%{http_code}' "$@"; }
JSON='-H Content-Type:application/json'

check "GET / redirects to the console"        "302" "$(code http://127.0.0.1:8000/)"
check "the console is served"                 "200" "$(code http://127.0.0.1:8000/app/)"
# Every script the page loads, not just one: a missing file is a blank console,
# and index.html referencing a file that was never installed is exactly the kind
# of packaging mistake this test exists to catch.
for js in ui api app; do
  check "console JS is served ($js.js)"       "200" "$(code http://127.0.0.1:8000/app/js/$js.js)"
done
check "console CSS is served"                 "200" "$(code http://127.0.0.1:8000/app/css/app.css)"
check "console favicon is served"             "200" "$(code http://127.0.0.1:8000/app/favicon.svg)"
check "unauthenticated API is refused"        "401" "$(code http://127.0.0.1:8000/api/v1/rpc)"
check "login without Content-Type is refused" "415" "$(code -X POST -d '{}' http://127.0.0.1:8000/api/auth/login)"
check "wrong password is refused"             "401" "$(code -X POST $JSON -d '{"username":"admin","password":"nope"}' http://127.0.0.1:8000/api/auth/login)"
check "changepw without a token is refused"   "401" "$(code -X POST $JSON -d '{"username":"admin","old_password":"'"$PW"'","new_password":"another-long-password"}' http://127.0.0.1:8000/api/auth/changepw)"

TOK=$(curl -s -X POST -H 'Content-Type: application/json' \
      -d '{"username":"admin","password":"'"$PW"'"}' \
      http://127.0.0.1:8000/api/auth/login | sed -n 's/.*"token":"\([^"]*\)".*/\1/p')
[ -n "$TOK" ] && ok "login returns a token (${#TOK} chars)" || bad "login returns a token" "empty"

AUTH="-H Authorization:Bearer${TOK:+ }Bearer"
N=$(curl -s -H "Authorization: Bearer $TOK" http://127.0.0.1:8000/api/v1/rpc | tr ',' '\n' | wc -l)
[ "$N" -ge 80 ] && ok "JSON bridge exposes $N RPCs" || bad "JSON bridge exposes 80+ RPCs" "got $N"

check "cross-origin is refused even with a token" "403" \
  "$(code -H 'Origin: https://evil.example' -H "Authorization: Bearer $TOK" http://127.0.0.1:8000/api/v1/rpc)"
check "an unimplemented RPC returns 501, not a corpse" "501" \
  "$(code -X POST -H "Authorization: Bearer $TOK" http://127.0.0.1:8000/api/v1/rpc/ListenWiFiStateChanges)"
check "a hostile read length is rejected" "400" \
  "$(code -X POST $JSON -H "Authorization: Bearer $TOK" -d '{"filename":"x","folder":0,"start":0,"len":9999999999}' http://127.0.0.1:8000/api/v1/rpc/FSReadFile)"

# The API must speak the field names the console reads. Without UseProtoNames
# protojson emits use_HID_KEYBOARD as useHIDKEYBOARD, and every USB key the
# console needs disappears from the response -- the Cable view renders every
# function as off whatever is deployed. Nothing here exercised a USB round-trip
# before, which is exactly why that shipped.
USB_JSON=$(curl -s -X POST -H "Authorization: Bearer $TOK" http://127.0.0.1:8000/api/v1/rpc/GetDeployedGadgetSetting)
case "$USB_JSON" in
    *use_HID_KEYBOARD*) ok "USB settings use proto field names" ;;
    *useHIDKEYBOARD*)   bad "USB settings use proto field names" "got camelCase; the console cannot read this" ;;
    *)
        # No UDC in a container, so an error here is expected and fine -- what
        # must never happen is a SUCCESSFUL response in the wrong spelling.
        case "$USB_JSON" in
            *'"error"'*) ok "USB settings RPC answered (no UDC in a container, as expected)" ;;
            *) bad "USB settings use proto field names" "unrecognised response: ${USB_JSON:0:120}" ;;
        esac
        ;;
esac

# A 500 asserts that the SERVICE is broken. On a board with no USB device
# controller, no WiFi and no Bluetooth -- which is a normal, supported state,
# and exactly what a container is -- the service is fine and the hardware is
# simply absent. Reporting that as an internal fault misleads anyone reading
# logs and anything that retries on 5xx. Every one of these answered 500 until
# the bridge got a central error classifier.
INTERNAL_ERRORS=0
for m in GetDeployedGadgetSetting GetWiFiState HIDGetRunningScriptJobs \
         GetBluetoothControllerInformation HIDCancelAllScriptJobs; do
    C=$(code -X POST $JSON -H "Authorization: Bearer $TOK" -d '{}' "http://127.0.0.1:8000/api/v1/rpc/$m")
    if [ "$C" = "500" ]; then
        bad "missing hardware is 503, not 500 ($m)" "got 500: the service claims it is itself broken"
        INTERNAL_ERRORS=$((INTERNAL_ERRORS+1))
    fi
done
[ "$INTERNAL_ERRORS" -eq 0 ] && ok "missing hardware reports 503, not an internal server error"

# The device drives ITSELF through P4wnP1_cli: servicestart.sh and every user
# trigger action are shell scripts full of CLI calls. When the API began
# requiring a bearer token, all of them started failing Unauthenticated -- so a
# device whose startup template failed came up with no USB gadget, no DHCP and
# no WiFi AP, and the fallback meant to rescue it was the thing that was
# broken. The console looked perfect throughout, which is why this went
# unnoticed; nothing here had ever read the service's own log.
if grep -q 'Unauthenticated' /tmp/svc.log; then
    bad "the device's own scripts can authenticate" \
        "the service log contains Unauthenticated: $(grep -m1 'Unauthenticated' /tmp/svc.log | tail -c 90)"
else
    ok "the device's own scripts can authenticate"
fi

# ...and prove it directly rather than only by absence of an error. This runs
# as root with no ~/.p4wnp1/token, exactly as a boot script does, so it passes
# only if the local credential under /run is found and accepted.
check "the local script credential exists, root-only" "600" \
  "$(stat -c %a /run/p4wnp1/local.token 2>/dev/null)"

if [ -z "${HOME:-}" ] || [ ! -f "$HOME/.p4wnp1/token" ]; then
    if CLI_OUT=$(/usr/local/bin/P4wnP1_cli led -b 3 2>&1); then
        ok "P4wnP1_cli works with no interactive login (as boot scripts run it)"
    else
        bad "P4wnP1_cli works with no interactive login (as boot scripts run it)" \
            "${CLI_OUT##*$'\n'}"
    fi
else
    ok "skipped CLI-without-login check (a cached token exists)"
fi

# After every malformed request above, it must still be serving.
kill -0 "$SVC" 2>/dev/null && ok "service still alive after hostile input" \
                           || bad "service still alive after hostile input" "it died"

kill -TERM "$SVC" 2>/dev/null
for i in $(seq 1 15); do kill -0 "$SVC" 2>/dev/null || break; sleep 1; done
kill -0 "$SVC" 2>/dev/null && bad "clean shutdown on SIGTERM" "still running after 15s" \
                           || ok "clean shutdown on SIGTERM"

echo
echo "  ---- $PASS passed, $FAIL failed ----"
if [ "$FAIL" -ne 0 ]; then
    echo; echo "  service log:"; sed 's/^/    /' /tmp/svc.log | tail -40
    exit 1
fi
IN_CONTAINER
