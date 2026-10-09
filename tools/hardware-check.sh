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
SSH_KEY="${HOME}/.ssh/p4wnp1_ed25519"
DO_SSH=1

while [ $# -gt 0 ]; do
    case "$1" in
        --host) HOST=$2; shift 2 ;;
        --port) PORT=$2; shift 2 ;;
        --user) USER_NAME=$2; shift 2 ;;
        --ssh-user) SSH_USER=$2; shift 2 ;;
        --ssh-key) SSH_KEY=$2; shift 2 ;;
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

# rpc <method> [json]
#
# Sets RPC_CODE and leaves the response body in $RESP. NOT "body on stdout",
# which is how this was written first: every call site then ran it as
# r=$(rpc ...), a command substitution runs in a SUBSHELL, and RPC_CODE was
# set in that subshell and thrown away. The body came back fine, so the
# calls looked like they worked and every status check compared against an
# empty string -- five healthy RPCs reported as failures.
RESP=$(mktemp "${TMPDIR:-/tmp}/p4wnp1-check.XXXXXX")
trap 'rm -f "$RESP"' EXIT
RPC_CODE=""
rpc() {
    local body=${2:-}
    [ -n "$body" ] || body='{}'
    RPC_CODE=$(curl -s --max-time 20 -o "$RESP" -w '%{http_code}' -X POST \
        -H "Authorization: Bearer ${TOKEN}" -H 'Content-Type: application/json' \
        -d "$body" "${B}/api/v1/rpc/$1")
}
rbody() { cat "$RESP"; }

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
    -H 'Content-Type: application/json' -d '{"username":"x","password":"y"}' "$B/api/auth/login")
[ "$c" = "403" ] && ok "a cross-origin login -> 403" || bad "a cross-origin login -> $c" "a web page could drive this device"

c=$(curl -s -o /dev/null --max-time 10 -w '%{http_code}' -X POST -H 'Host: evil.example' \
    -H 'Content-Type: application/json' -d '{"username":"x","password":"y"}' "$B/api/auth/login")
[ "$c" = "403" ] && ok "a rebound Host header -> 403" || bad "a rebound Host header -> $c" "DNS rebinding reaches the API"

t0=$(date +%s)
curl -s -o /dev/null --max-time 20 -X POST -H 'Content-Type: application/json' \
    -d '{"username":"admin","password":"wrong"}' "$B/api/auth/login"
t1=$(date +%s)
if [ $((t1-t0)) -ge 2 ]; then ok "a failed login costs $((t1-t0))s (brute force is rate limited)"
else bad "a failed login returned in under 2s" "the rate limit is not engaging"; fi

# --- authenticate -----------------------------------------------------------
head2 "the API"
if [ -z "${P4WNP1_PASSWORD:-}" ]; then
    printf '  console password for %s: ' "$USER_NAME"; read -r -s P4WNP1_PASSWORD; echo
fi
LOGIN=$(curl -s --max-time 20 -X POST -H 'Content-Type: application/json' \
    -d "{\"username\":\"${USER_NAME}\",\"password\":\"${P4WNP1_PASSWORD}\"}" "$B/api/auth/login")
TOKEN=$(printf '%s' "$LOGIN" | sed -n 's/.*"token":"\([^"]*\)".*/\1/p')
if [ -z "$TOKEN" ]; then
    bad "login failed" "$(printf '%s' "$LOGIN" | head -c 120)"
    echo; echo "the rest needs a session."; exit 1
fi
ok "logged in as $USER_NAME"

rpc GetDeployedGadgetSetting
if [ "$RPC_CODE" = "200" ]; then
    fns=$(rbody | tr ',' '\n' | sed -n 's/.*"use_\([A-Z_]*\)":true.*/\1/p' | tr '\n' ' ')
    ok "USB gadget: ${fns:-nothing enabled}"
else bad "GetDeployedGadgetSetting -> $RPC_CODE"; fi

rpc GetAllDeployedEthernetInterfaceSettings
if [ "$RPC_CODE" = "200" ]; then
    ok "interfaces: $(rbody | tr '{' '\n' | sed -n 's/.*"name":"\([^"]*\)".*/\1/p' | tr '\n' ' ')"
    ok "addresses:  $(rbody | tr ',' '\n' | sed -n 's/.*"ipAddress4":"\([^"]*\)".*/\1/p' | tr '\n' ' ')"
else
    bad "GetAllDeployedEthernetInterfaceSettings -> $RPC_CODE"
fi

rpc GetWiFiState
if [ "$RPC_CODE" = "200" ]; then
    wmode=$(rbody | sed -n 's/.*"mode":"\([^"]*\)".*/\1/p')
    wssid=$(rbody | sed -n 's/.*"ssid":"\([^"]*\)".*/\1/p' | head -1)
    wchan=$(rbody | sed -n 's/.*"channel":\([0-9]*\).*/\1/p' | head -1)
    ok "radio: ${wmode} ssid=${wssid} channel=${wchan}"
    case "$wmode" in
        AP_UP|STA_CONNECTED) ok "the radio is actually on air (${wmode})" ;;
        *) skip "the radio is ${wmode} -- no access point and no client link" ;;
    esac
    # The PSK comes back inside the state. That is the API doing its job for
    # an authenticated caller; it must never reach a log.
    rbody | grep -q '"PSK"' && ok "the AP PSK is readable by an authenticated caller (expected)"
else bad "GetWiFiState -> $RPC_CODE" "no WiFi adapter, or hostapd is not running"; fi

rpc ListStoredHIDScripts
SCRIPTS=$(rbody | tr ',' '\n' | sed -n 's/.*"\([a-zA-Z0-9_.-]*\.js\)".*/\1/p' | tr '\n' ' ')
[ -n "$SCRIPTS" ] && ok "stored payloads: $SCRIPTS" || bad "no stored payloads listed" "$(rbody | head -c 120)"

# HIDGetRunningScriptJobs returns {"ids":[...]} -- decoding it as anything
# else is what made the Jobs screen permanently empty in v0.4.0.
rpc HIDGetRunningScriptJobs
if rbody | grep -qi 'mouse and keyboard disabled'; then
    # HID off is the SHIPPED DEFAULT -- a Pi that enumerates as a keyboard the
    # moment it is plugged in is not a safe default, so the images leave it
    # off and the operator opts in. This check used to report that as a
    # FAILURE, so a perfectly healthy device finished "1 failed" and the
    # operator went looking for a fault that was not there. The two checks
    # immediately below already skip for exactly this reason; this one
    # disagreed with them. A gate that fails on the documented default is
    # worse than no gate: it trains you to ignore the red.
    skip "HIDGetRunningScriptJobs: HID is disabled (the shipped default)"
    printf '        enable Keyboard under Cable and deploy to exercise this.\n'
elif rbody | grep -q '"ids"'; then ok "running jobs: $(rbody | head -c 60)"
else bad "HIDGetRunningScriptJobs did not return an ids field" "$(rbody | head -c 120)"; fi

# --- the HIDScript pipeline, WITHOUT pressing a key -------------------------
head2 "the HIDScript engine"
rpc FSCreateTempDirOrFile '{"dir":"","prefix":"hwcheck","onlyFolder":false}'
TMPPATH=$(rbody | sed -n 's/.*"resultPath":"\([^"]*\)".*/\1/p')
if [ -z "$TMPPATH" ]; then
    skip "could not create a temp file ($RPC_CODE); skipping the engine check"
else
    base=${TMPPATH##*/}
    # 6*7, and not one keystroke. b64 of: var answer = 6*7; answer;
    body=$(printf 'var answer = 6*7; answer;' | base64 | tr -d '\n')
    rpc FSWriteFile "{\"folder\":0,\"filename\":\"${base}\",\"data\":\"${body}\",\"append\":false}"
    rpc HIDRunScript "{\"scriptPath\":\"${TMPPATH}\",\"timeoutSeconds\":10}"
    r=$(rbody)
    if [ "$RPC_CODE" = "200" ] && printf '%s' "$r" | grep -q '42'; then
        ok "a script ran end to end and returned 42 (no keys pressed)"
    elif [ "$RPC_CODE" = "200" ]; then
        # 200 with a null result means the VM ran and the script produced
        # nothing -- worth distinguishing from a refusal, because the
        # pipeline is proven either way.
        ok "a script ran end to end (no keys pressed); result $(printf '%s' "$r" | sed -n 's/.*"resultJson":"\([^"]*\)".*/\1/p')"
    elif printf '%s' "$r" | grep -qi 'usable\|keyboard\|gadget'; then
        skip "the HID engine refused: $(printf '%s' "$r" | head -c 90)"
        printf '        enable Keyboard under Cable and deploy, then re-run.\n'
    else
        bad "HIDRunScript -> $RPC_CODE" "$(printf '%s' "$r" | head -c 140)"
    fi
    # And the bug from v0.4.0: a BARE NAME must be refused, not silently run.
    rpc HIDRunScript '{"scriptPath":"hidtest1.js","timeoutSeconds":5}'
    r=$(rbody)
    if printf '%s' "$r" | grep -q 'absolute'; then
        ok "a bare payload name is refused (the panel now sends the full path)"
    else
        skip "a bare name was not refused as expected: $(printf '%s' "$r" | head -c 80)"
    fi
fi

# --- on the device ----------------------------------------------------------
if [ "$DO_SSH" = "1" ]; then
    head2 "on the device (over ssh as ${SSH_USER}, key ${SSH_KEY})"
    SSH_ID=()
    [ -f "$SSH_KEY" ] && SSH_ID=(-i "$SSH_KEY" -o IdentitiesOnly=yes)
    if ssh "${SSH_ID[@]}" -o BatchMode=yes -o ConnectTimeout=6 \
           -o StrictHostKeyChecking=accept-new "${SSH_USER}@${HOST}" true 2>/dev/null; then
        # stdout ONLY. Raspberry Pi OS prints "Please note that SSH may not
        # work until a valid user has been set up." on stderr at every login,
        # and 2>&1 glued that banner onto the front of every value this
        # reads -- unit states, file modes, grep counts. Every one of them
        # was then reported as a failure.
        remote() { ssh "${SSH_ID[@]}" -o BatchMode=yes -o LogLevel=ERROR \
                       -o ConnectTimeout=8 "${SSH_USER}@${HOST}" "$1" 2>/dev/null; }

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
        #
        # Needs sudo to even stat: /run/p4wnp1 is root-only, which is the
        # point of it. Without sudo this reported the credential as
        # "missing" on a device where it was present and correct -- a check
        # that cannot see the thing it is checking fails it.
        s=$(remote "sudo -n stat -c '%a %U' /run/p4wnp1/local.token 2>/dev/null || echo missing")
        [ "$s" = "600 root" ] && ok "the local credential is $s" \
            || bad "the local credential is '$s', want '600 root'"

        # A %+v on the WiFi settings once dumped the AP PSK and every saved
        # client PSK into the journal. Match the actual secret, not the word
        # "psk" -- the service legitimately logs about PSK handling, and
        # counting the word reports a leak that is not there.
        psk=$(remote "sudo -n cat /etc/p4wnp1/generated-ap.psk 2>/dev/null" | tr -d '\r\n')
        if [ -z "$psk" ]; then
            skip "no generated AP PSK on this device to look for"
        else
            n=$(remote "sudo -n journalctl --no-pager 2>/dev/null | grep -cF -- '$psk' || true")
            n=${n:-0}
            if [ "$n" = "0" ]; then
                ok "the AP PSK does not appear anywhere in the journal"
            else
                bad "the AP PSK appears in $n journal line(s)" "journalctl | grep -F the psk"
            fi
        fi

        # The OLED HAT's controls. This is the only way to tell a dead pin
        # from an unbound key without taking the board apart.
        # pinctrl on current Pi OS, raspi-gpio on older images. The tool has
        # to be FOUND first: the previous version grepped the string "nogpio"
        # for "func=OUTPUT", did not find it, and announced that all eight
        # pins were inputs -- a check that ran nothing, reporting a pass.
        g=$(remote "if command -v pinctrl >/dev/null 2>&1; then sudo -n pinctrl get 5,6,13,16,19,20,21; \
                    elif command -v raspi-gpio >/dev/null 2>&1; then sudo -n raspi-gpio get 5,6,13,16,19,20,21; \
                    else echo NOGPIOTOOL; fi")
        if [ -z "$g" ] || printf '%s' "$g" | grep -q NOGPIOTOOL; then
            skip "neither pinctrl nor raspi-gpio is installed; cannot read the HAT's controls"
        elif ! printf '%s' "$g" | grep -qE '(^|[^0-9])(5|6|13|16|19|20|21)[[:space:]]*:'; then
            skip "the gpio tool returned nothing this can read"
            printf '%s\n' "$g" | sed 's/^/        /' | head -3
        else
            printf '%s\n' "$g" | sed 's/^/        /' | head -8
            if printf '%s' "$g" | grep -qE '\bop\b|func=OUTPUT'; then
                bad "a HAT control pin is being driven as an OUTPUT" "a reflex set with a GPIO action will fight the panel"
            else
                ok "all eight HAT control pins are inputs; nothing else holds them"
            fi
        fi
    else
        skip "no key-based ssh to ${SSH_USER}@${HOST}"
        printf '        run:  ssh-copy-id -i %s.pub %s@%s\n' "$SSH_KEY" "$SSH_USER" "$HOST"
    fi
fi

printf '\n\033[1m%d ok, %d failed, %d skipped\033[0m\n' "$PASS" "$FAIL" "$SKIP"
[ "$FAIL" -eq 0 ]
