#!/usr/bin/env bash
#
# usb-functions-check.sh -- prove USB Serial and Mass Storage actually appear
# on the host, against a real device.
#
# WHY THIS IS SEPARATE FROM hardware-check.sh
#
# hardware-check.sh is read-only: it asks the device questions. This one
# RECONFIGURES THE USB GADGET several times, which briefly drops the very link
# it is talking over. That is not something to run by accident, so it lives in
# its own script and is not part of `make verify`.
#
# WHY IT CANNOT BE A CONTAINER TEST
#
# tools/feature-test.sh runs the real service in a container and classifies
# every UMS call as "expected-without-hardware", because a container has no
# /sys/kernel/config/usb_gadget. That is a pass that proves nothing about
# whether a host ever sees a disk. And Serial is not an RPC at all -- it is a
# USB function toggle whose only observable effect is on the machine at the
# other end of the cable.
#
# So every assertion here is made FROM THE HOST: after deploying, does a
# /dev/cu.* appear, does a disk appear. The device reporting success is not
# the same as the cable presenting a device.
#
# Usage:  P4WNP1_PASSWORD=... ./tools/usb-functions-check.sh [--host 172.16.0.1]
set -uo pipefail

HOST=172.16.0.1
PORT=8000
while [ $# -gt 0 ]; do
    case "$1" in
        --host) HOST=$2; shift 2 ;;
        --port) PORT=$2; shift 2 ;;
        *) echo "usage: $0 [--host H] [--port P]" >&2; exit 2 ;;
    esac
done
B="http://${HOST}:${PORT}"

RED=$'\033[31m'; GRN=$'\033[32m'; YEL=$'\033[33m'; BLD=$'\033[1m'; OFF=$'\033[0m'
PASS=0; FAIL=0; NOTE=0
ok()   { printf "  ${GRN}ok${OFF}    %s\n" "$1"; PASS=$((PASS+1)); }
bad()  { printf "  ${RED}FAIL${OFF}  %s\n" "$1"; [ -n "${2:-}" ] && printf "        %s\n" "$2"; FAIL=$((FAIL+1)); }
note() { printf "  ${YEL}--${OFF}    %s\n" "$1"; NOTE=$((NOTE+1)); }
head2(){ printf "\n${BLD}%s${OFF}\n" "$1"; }

if [ -z "${P4WNP1_PASSWORD:-}" ]; then
    printf 'console password for admin: '; read -r -s P4WNP1_PASSWORD; echo
fi

LOGIN_JSON=$(python3 -I -c 'import json,sys;print(json.dumps({"username":"admin","password":sys.argv[1]}))' "$P4WNP1_PASSWORD")
TOK=$(curl -s --max-time 20 -X POST -H 'Content-Type: application/json' \
      -d "$LOGIN_JSON" "$B/api/auth/login" | sed -n 's/.*"token":"\([^"]*\)".*/\1/p')
[ -n "$TOK" ] || { echo "login failed"; exit 1; }
echo "logged in to $B"

rpc() {
    local body="${2:-}"; [ -n "$body" ] || body='{}'
    curl -s --max-time 30 -X POST -H "Authorization: Bearer $TOK" \
         -H 'Content-Type: application/json' -d "$body" "$B/api/v1/rpc/$1"
}

# Wait for the link to return rather than sleeping a guessed number of
# seconds. A fixed sleep is how this kind of test becomes flaky and then gets
# ignored.
relink() {
    local _
    for _ in $(seq 1 40); do
        ping -c1 -W1000 "$HOST" >/dev/null 2>&1 && { sleep 2; return 0; }
        sleep 1
    done
    return 1
}

serial_ports() { ls /dev/cu.usbmodem* 2>/dev/null | tr '\n' ' '; }
ext_disks()    { diskutil list external physical 2>/dev/null | sed -n 's|^/dev/\(disk[0-9]*\).*|\1|p' | tr '\n' ' '; }

# Build a composition by taking the LIVE settings and flipping only the
# booleans we care about.
#
# Writing the payload from scratch is wrong twice over. It omits
# rndis_settings and cdc_ecm_settings, which the service dereferences while
# validating -- that was a nil pointer panic until it was fixed, recovered
# into a useless "internal error". And it is not what any real client does:
# the console reads the deployed object, changes what the operator touched and
# sends the whole thing back. A test that builds requests differently from
# every real caller tests a path nobody uses.
#
# Reading first also keeps the MAC addresses, vid/pid and device strings as
# deployed, so the host does not see a different device and re-DHCP mid-test.
compose() {  # keyboard mouse raw serial ums [umsfile]
    rpc GetDeployedGadgetSetting | python3 -I -c '
import json,sys
gs = json.load(sys.stdin)
# argv[1:7], not [1:6]. The slice stopped one short, so the filename --
# the sixth argument -- was never read and every UMS deploy went out
# with an empty backing image. The device said so once the error
# stopped being discarded; before that it was just "internal error".
kb, mo, raw, ser, ums, f = (sys.argv[1:7] + [""])[:6]
b = lambda v: v == "true"
gs["enabled"] = True
gs["use_RNDIS"] = True          # never drop the link this test runs over
gs["use_CDC_ECM"] = True
gs["use_HID_KEYBOARD"] = b(kb)
gs["use_HID_MOUSE"]    = b(mo)
gs["use_HID_RAW"]      = b(raw)
gs["use_SERIAL"]       = b(ser)
gs["use_UMS"]          = b(ums)
if b(ums):
    gs["ums_settings"] = {"cdrom": False, "file": f}
json.dump(gs, sys.stdout)
' "$1" "$2" "$3" "$4" "$5" "${6:-}"
}

head2 "baseline"
BASE=$(rpc GetDeployedGadgetSetting)
if printf '%s' "$BASE" | grep -q '"use_RNDIS"'; then
    ok "deployed: $(printf '%s' "$BASE" | tr ',' '\n' | sed -n 's/.*"use_\([A-Z_]*\)":true.*/\1/p' | tr '\n' ' ')"
else
    bad "GetDeployedGadgetSetting" "$(printf '%s' "$BASE" | head -c 160)"; exit 1
fi
printf '  host sees: serial=[%s] disks=[%s]\n' "$(serial_ports)" "$(ext_disks)"

head2 "the endpoint budget (a Pi Zero W has 7)"
# RNDIS 2 + CDC_ECM 2 + SERIAL 2 + UMS 2 = 8.
r=$(rpc DeployGadgetSetting "$(compose false false false true true test.bin)")
msg=$(printf '%s' "$r" | sed 's/.*"error":"\([^"]*\)".*/\1/')
if printf '%s' "$r" | grep -qi 'endpoint'; then
    ok "8 endpoints refused, and the reason names endpoints"
elif printf '%s' "$r" | grep -qi 'internal error'; then
    # An earlier version of this script counted this as a pass. It is not:
    # an internal error would "refuse" every composition including the legal
    # ones, so any error is NOT evidence of a working check. That is exactly
    # how a nil-pointer panic in the validator hid behind a green test.
    bad "8 endpoints gave an INTERNAL ERROR, not a refusal" "$msg"
elif printf '%s' "$r" | grep -qi 'error'; then
    bad "8 endpoints refused for the wrong reason" "$msg"
else
    bad "8 endpoints was ACCEPTED" "the budget is not enforced on the device"
fi
relink && ok "device still reachable after the refusal" || bad "unreachable after the refusal"

head2 "SERIAL"
r=$(rpc DeployGadgetSetting "$(compose false false false true false)")
if printf '%s' "$r" | grep -qi '"error"'; then
    bad "deploying SERIAL" "$(printf '%s' "$r" | head -c 200)"
elif relink; then
    ok "deployed, and the USB link came back"
    sleep 3
    sp=$(serial_ports)
    [ -n "$sp" ] && ok "the HOST now sees a serial port: $sp" \
                 || bad "no /dev/cu.usbmodem* appeared on the host" \
                        "the device reported success but the cable presents no serial device"
    printf '%s' "$(rpc GetDeployedGadgetSetting)" | grep -q '"use_SERIAL":true' \
        && ok "the device reports use_SERIAL:true" || bad "device does not report use_SERIAL:true"
else
    bad "USB link did not come back after enabling SERIAL"
fi

head2 "MASS STORAGE"
IMG=$(rpc ListUmsImageFlashdrive | sed -n 's/.*"msgArray":\["\([^"]*\)".*/\1/p')
if [ -z "$IMG" ]; then
    note "no flashdrive image on the device; UMS has nothing to back it"
    note "skipping -- without a backing file it cannot present a disk"
else
    ok "backing image: $IMG"
    r=$(rpc DeployGadgetSetting "$(compose false false false false true "$IMG")")
    if printf '%s' "$r" | grep -qi '"error"'; then
        bad "deploying UMS" "$(printf '%s' "$r" | head -c 200)"
    elif relink; then
        ok "deployed, and the USB link came back"
        sleep 5
        dk=$(ext_disks)
        [ -n "$dk" ] && ok "the HOST now sees a removable disk: $dk" \
                     || bad "no external disk appeared on the host" \
                            "the device reported success but the cable presents no storage"
    else
        bad "USB link did not come back after enabling UMS"
    fi
fi

head2 "restore"
r=$(rpc DeployGadgetSetting "$(compose false false false false false)")
if printf '%s' "$r" | grep -qi '"error"'; then
    bad "restoring the baseline" "$(printf '%s' "$r" | head -c 160)"
else
    relink && ok "restored and reachable" || bad "unreachable after restore"
    d=$(rpc GetDeployedGadgetSetting)
    printf '%s' "$d" | grep -q '"use_SERIAL":false' && printf '%s' "$d" | grep -q '"use_UMS":false' \
        && ok "serial and storage are off again" \
        || note "final: $(printf '%s' "$d" | tr ',' '\n' | sed -n 's/.*"use_\([A-Z_]*\)":true.*/\1/p' | tr '\n' ' ')"
    printf '  host sees: serial=[%s] disks=[%s]\n' "$(serial_ports)" "$(ext_disks)"
fi

printf "\n${BLD}%d ok, %d failed, %d notes${OFF}\n" "$PASS" "$FAIL" "$NOTE"
[ "$FAIL" -eq 0 ]
