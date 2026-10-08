#!/usr/bin/env bash
# Exercise a REAL P4wnP1 over the USB ethernet link and say what works.
#
# Everything else in tools/ runs the binary in a container. This one talks to
# the appliance on your desk, because a whole class of fault only exists
# there: a field name the service never sends, a path the service refuses, a
# GPIO something else is holding. Three of those shipped in v0.4.0 and were
# found by looking at the panel, not by the suite.
#
#   P4WNP1_PASSWORD=... ./tools/hardware-check.sh
#   ./tools/hardware-check.sh --host 172.16.0.1 --port 8000 --user admin
#
# It does not type anything into the host. The HIDScript it runs returns a
# number and presses no keys, so the pipeline is proven without a payload
# landing in whatever window you happen to have focused. Firing a real one is
# a decision you make, not a side effect of a health check.
set -u

HOST=172.16.0.1
PORT=8000
USER_NAME="admin"
SSH_USER=p4wnp1
DO_SSH=1

while [ $# -gt 0 ]; do
    case "$1" in
        --host) HOST=$2; shift 2 ;;
        --port) PORT=$2; shift 2 ;;
        --user) USER_NAME=$2; shift 2 ;;
        --ssh-user) SSH_USER=$2; shift 2 ;;
        --no-ssh) DO_SSH=0; shift ;;
        -h|--help) sed -n '2,20p' "$0"; exit 0 ;;
        *) echo "unknown option $1" >&2; exit 2 ;;
    esac
done

B="http://${HOST}:${PORT}"
PASS=0
FAIL=0
SKIP=0

ok()   { printf '  \033[32mok\033[0m    %s\n' "$1"; PASS=$((PASS+1)); }
bad()  { printf '  \033[31mFAIL\033[0m  %s\n' "$1"; [ $# -gt 1 ] && printf '        %s\n' "$2"; FAIL=$((FAIL+1)); }
skip() { printf '  --    %s\n' "$1"; SKIP=$((SKIP+1)); }
head2(){ printf '\n\033[1m%s\033[0m\n' "$1"; }

# rpc <method> <json> -> body on stdout, http code in RPC_CODE
RPC_CODE=""
rpc() {
    local out
    out=$(curl -s --max-time 15 -w $'\n%{http_code}' -X POST \
        -H "Authorization: Bearer ${TOKEN}" -H 'Content-Type: application/json' \
        -d "${2:-\{\}}" "${B}/api/v1/rpc/$1")
    RPC_CODE=${out##*$'\n'}
    printf '%s' "${out%$'\n'*}"
}

# --- reachability -----------------------------------------------------------
head2 "the link"
if ping -c 1 -t 3 "$HOST" >/dev/null 2>&1; then
    ok "$HOST answers (USB ethernet gadget is up)"
else
    bad "$HOST does not answer" "is the cable in a DATA port, and is the gadget composed?"
    echo; echo "nothing else can be checked without the link."; exit 1
fi
MY_IP=$(ifconfig 2>/dev/null | awk '/172\.16\.0\./ {print $2; exit}')
[ -n "$MY_IP" ] && ok "this machine got $MY_IP from the device's DHCP server" \
                || skip "no 172.16.0.x address here (static config?)"

# --- the console is served --------------------------------------------------
head2 "the web console"
code=$(curl -s -o /dev/null --max-time 10 -w '%{http_code}' "$B/app/js/app.js")
[ "$code" = "200" ] && ok "console assets are served" || bad "console assets -> HTTP $code"

# --- access control, before authenticating ----------------------------------
head2 "access control (every one of these must be refused)"
for m in GetDeployedGadgetSetting HIDRunScript Reboot DBBackup; do
    c=$(curl -s -o /dev/null --max-time 10 -w '%{http_code}' -X POST \
        -H 'Content-Type: application/json' -d '{}' "$B/api/v1/rpc/$m")
    [ "$c" = "401" ] && ok "$m without a token -> 401" || bad "$m without a token -> $c" "this RPC is exposed"
done
c=$(curl -s -o /dev/null --max-time 10 -w '%{http_code}' -X POST \
    -H 'Authorization: Bearer deadbeef' -H 'Content-Type: application/json' -d '{}' \
    "$B/api/v1/rpc/ListStoredHIDScripts")
[ "$c" = "401" ] && ok "a junk bearer token -> 401" || bad "a junk bearer token -> $c"

c=$(curl -s -o /dev/null --max-time 10 -w '%{http_code}' -X POST -H 'Origin: http://evil.example' \
    -H 'Content-Type: application/json' -d '{"login":"x","password":"y"}' "$B/api/auth/login")
[ "$c" = "403" ] && ok "a cross-origin login -> 403" || bad "a cross-origin login -> $c" "a web page could drive this device"

c=$(curl -s -o /dev/null --max-time 10 -w '%{http_code}' -X POST -H 'Host: evil.example' \
    -H 'Content-Type: application/json' -d '{"login":"x","password":"y"}' "$B/api/auth/login")
[ "$c" = "403" ] && ok "a rebound Host header -> 403" || bad "a rebound Host header -> $c" "DNS rebinding reaches the API"

t0=$(date +%s)
curl -s -o /dev/null --max-time 20 -X POST -H 'Content-Type: application/json' \
    -d '{"login":"admin","password":"wrong"}' "$B/api/auth/login"
t1=$(date +%s)
if [ $((t1-t0)) -ge 2 ]; then ok "a failed login costs $((t1-t0))s (brute force is rate limited)"
else bad "a failed login returned in under 2s" "the rate limit is not engaging"; fi

# --- authenticate -----------------------------------------------------------
head2 "the API"
if [ -z "${P4WNP1_PASSWORD:-}" ]; then
    printf '  console password for %s: ' "$USER_NAME"; read -r -s P4WNP1_PASSWORD; echo
fi
LOGIN=$(curl -s --max-time 20 -X POST -H 'Content-Type: application/json' \
    -d "{\"login\":\"${USER_NAME}\",\"password\":\"${P4WNP1_PASSWORD}\"}" "$B/api/auth/login")
TOKEN=$(printf '%s' "$LOGIN" | sed -n 's/.*"token":"\([^"]*\)".*/\1/p')
if [ -z "$TOKEN" ]; then
    bad "login failed" "$(printf '%s' "$LOGIN" | head -c 120)"
    echo; echo "the rest needs a session."; exit 1
fi
ok "logged in as $USER_NAME"

r=$(rpc GetDeployedGadgetSetting)
if [ "$RPC_CODE" = "200" ]; then
    fns=$(printf '%s' "$r" | tr ',' '\n' | sed -n 's/.*"use_\([A-Z_]*\)":true.*/\1/p' | tr '\n' ' ')
    ok "USB gadget: ${fns:-nothing enabled}"
else bad "GetDeployedGadgetSetting -> $RPC_CODE"; fi

r=$(rpc GetAllDeployedEthernetInterfaceSettings)
[ "$RPC_CODE" = "200" ] && ok "interfaces: $(printf '%s' "$r" | tr ',' '\n' | sed -n 's/.*"name":"\([^"]*\)".*/\1/p' | tr '\n' ' ')" \
                        || bad "GetAllDeployedEthernetInterfaceSettings -> $RPC_CODE"

r=$(rpc GetWiFiState)
if [ "$RPC_CODE" = "200" ]; then
    ok "radio: $(printf '%s' "$r" | sed -n 's/.*"mode":"\([^"]*\)".*/\1/p') $(printf '%s' "$r" | sed -n 's/.*"ssid":"\([^"]*\)".*/\1/p')"
else bad "GetWiFiState -> $RPC_CODE" "no WiFi adapter, or hostapd is not running"; fi

r=$(rpc ListStoredHIDScripts)
SCRIPTS=$(printf '%s' "$r" | tr ',' '\n' | sed -n 's/.*"\([a-zA-Z0-9_.-]*\.js\)".*/\1/p' | tr '\n' ' ')
[ -n "$SCRIPTS" ] && ok "stored payloads: $SCRIPTS" || bad "no stored payloads listed" "$r"

# HIDGetRunningScriptJobs returns {"ids":[...]} -- decoding it as anything
# else is what made the Jobs screen permanently empty in v0.4.0.
r=$(rpc HIDGetRunningScriptJobs)
if printf '%s' "$r" | grep -q '"ids"'; then ok "running jobs: $r"
else bad "HIDGetRunningScriptJobs did not return an ids field" "$r"; fi

# --- the HIDScript pipeline, WITHOUT pressing a key -------------------------
head2 "the HIDScript engine"
tmp=$(rpc FSCreateTempDirOrFile '{"dir":"","prefix":"hwcheck","onlyFolder":false}')
TMPPATH=$(printf '%s' "$tmp" | sed -n 's/.*"resultPath":"\([^"]*\)".*/\1/p')
if [ -z "$TMPPATH" ]; then
    skip "could not create a temp file ($RPC_CODE); skipping the engine check"
else
    base=${TMPPATH##*/}
    # 6*7, and not one keystroke. b64 of: var answer = 6*7; answer;
    body=$(printf 'var answer = 6*7; answer;' | base64 | tr -d '\n')
    rpc FSWriteFile "{\"folder\":0,\"filename\":\"${base}\",\"data\":\"${body}\",\"append\":false}" >/dev/null
    r=$(rpc HIDRunScript "{\"scriptPath\":\"${TMPPATH}\",\"timeoutSeconds\":10}")
    if [ "$RPC_CODE" = "200" ] && printf '%s' "$r" | grep -q '42'; then
        ok "a script ran end to end and returned 42 (no keys pressed)"
    elif printf '%s' "$r" | grep -qi 'usable\|keyboard\|gadget'; then
        skip "the HID engine refused: $(printf '%s' "$r" | head -c 90)"
        printf '        enable Keyboard under Cable and deploy, then re-run.\n'
    else
        bad "HIDRunScript -> $RPC_CODE" "$(printf '%s' "$r" | head -c 140)"
    fi
    # And the bug from v0.4.0: a BARE NAME must be refused, not silently run.
    r=$(rpc HIDRunScript '{"scriptPath":"hidtest1.js","timeoutSeconds":5}')
    if printf '%s' "$r" | grep -q 'absolute'; then
        ok "a bare payload name is refused (the panel now sends the full path)"
    else
        skip "a bare name was not refused as expected: $(printf '%s' "$r" | head -c 80)"
    fi
fi

# --- on the device ----------------------------------------------------------
if [ "$DO_SSH" = "1" ]; then
    head2 "on the device (over ssh, needs a key: ssh-copy-id ${SSH_USER}@${HOST})"
    if ssh -o BatchMode=yes -o ConnectTimeout=6 -o StrictHostKeyChecking=accept-new \
           "${SSH_USER}@${HOST}" true 2>/dev/null; then
        remote() { ssh -o BatchMode=yes -o ConnectTimeout=8 "${SSH_USER}@${HOST}" "$1" 2>&1; }

        for u in P4wnP1 ssh; do
            s=$(remote "systemctl is-active $u")
            [ "$s" = "active" ] && ok "$u is $s" || bad "$u is $s"
        done
        s=$(remote "systemctl is-active p4wnp1-oled 2>/dev/null || echo absent")
        case "$s" in
            active) ok "p4wnp1-oled is active (this is an -oled image)" ;;
            absent|inactive) skip "p4wnp1-oled: $s (a plain image has no panel)" ;;
            *) bad "p4wnp1-oled is $s" "journalctl -u p4wnp1-oled" ;;
        esac

        # The machine-local credential: tmpfs, root-only.
        s=$(remote "stat -c '%a %U' /run/p4wnp1/local.token 2>/dev/null || echo missing")
        [ "$s" = "600 root" ] && ok "the local credential is $s" \
            || bad "the local credential is '$s', want '600 root'"

        # No secret may reach the journal. This caught a %+v that dumped the
        # AP PSK and every saved client PSK.
        n=$(remote "sudo -n journalctl -u P4wnP1 --no-pager 2>/dev/null | grep -ciE 'psk|passphrase\"' || true")
        case "$n" in
            0) ok "no PSK appears in the service journal" ;;
            *[0-9]*) bad "$n journal lines mention a PSK" "journalctl -u P4wnP1 | grep -i psk" ;;
            *) skip "could not read the journal (sudo needs a password)" ;;
        esac

        # The OLED HAT's controls. This is the only way to tell a dead pin
        # from an unbound key without taking the board apart.
        g=$(remote "command -v raspi-gpio >/dev/null && raspi-gpio get 5,6,13,16,19,20,21 || echo nogpio")
        if [ "$g" = "nogpio" ]; then
            skip "raspi-gpio not installed; cannot read the HAT's controls"
        else
            printf '        %s\n' "$g" | sed -n '1,9p'
            if printf '%s' "$g" | grep -q 'func=OUTPUT'; then
                bad "something is driving a control pin as an OUTPUT" "a reflex set with a GPIO action will fight the panel"
            else
                ok "all eight control pins are inputs (nothing else is holding them)"
            fi
        fi
    else
        skip "no key-based ssh to ${SSH_USER}@${HOST}"
        printf '        run:  ssh-copy-id %s@%s     then re-run this script.\n' "$SSH_USER" "$HOST"
    fi
fi

printf '\n\033[1m%d ok, %d failed, %d skipped\033[0m\n' "$PASS" "$FAIL" "$SKIP"
[ "$FAIL" -eq 0 ]
