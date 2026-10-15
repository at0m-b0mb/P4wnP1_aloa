#!/usr/bin/env bash
#
# run-on-device.sh -- run a command as root on the device and read its output,
#                     using only the JSON API.
#
#   P4WNP1_PASSWORD='...' ./tools/run-on-device.sh 'cat /usr/local/P4wnP1/VERSION'
#   P4WNP1_PASSWORD='...' ./tools/run-on-device.sh --detach 'p4wnp1-update --rollback'
#
# WHY THIS EXISTS
#
# scp to this device hangs after the password is accepted -- ssh reaches
# authentication instantly, so it is the SFTP subsystem specifically -- which
# left no way to look at the device while testing the updater. Everything here
# goes over the API, which is healthy.
#
# HOW IT GETS OUTPUT BACK
#
# A trigger action runs bash as root but returns nothing to the caller. The
# only readable channels the API offers are the three allowlisted folders, so
# the command's output is written into HIDScripts and read back with FSReadFile.
# It is a strange route and it is the one that exists.
#
# WHEN TO USE --detach
#
# A trigger action is a CHILD OF THE SERVICE. Anything that stops that service
# -- the updater, a rollback, systemctl restart -- kills the whole control
# group, taking the command with it halfway through. --detach puts it in a
# transient systemd unit of its own so it survives, and gives up reading the
# output, because there will be no service to read it from.
#
# THIS IS A DEBUG TOOL, NOT A FEATURE. It writes a bash script into the
# device's script folder and fires it. That is an authenticated operator doing
# what an authenticated operator may already do -- it is not an escalation --
# but it is not something to leave lying around in a production flow.
set -euo pipefail

HOST="${P4WNP1_HOST:-172.16.0.1}"
PORT="${P4WNP1_PORT:-8000}"
USER="${P4WNP1_USER:-admin}"
API="http://$HOST:$PORT"
DETACH=0

[ "${1:-}" = "--detach" ] && { DETACH=1; shift; }
CMD="${1:-}"
[ -n "$CMD" ] || { echo "usage: $0 [--detach] '<command>'" >&2; exit 2; }
[ -n "${P4WNP1_PASSWORD:-}" ] || { echo "set P4WNP1_PASSWORD" >&2; exit 2; }

TOK=$(curl -s -m 30 -X POST "$API/api/auth/login" -H 'Content-Type: application/json' \
      -d "{\"username\":\"$USER\",\"password\":\"$P4WNP1_PASSWORD\"}" \
      | python3 -c 'import sys,json;print(json.load(sys.stdin).get("token",""))')
[ -n "$TOK" ] || { echo "login failed" >&2; exit 1; }

api() { curl -s -m 60 -X POST "$API/api/v1/rpc/$1" -H "Authorization: Bearer $TOK" \
             -H 'Content-Type: application/json' -d "$2"; }

TMPD=$(mktemp -d); trap 'rm -rf "$TMPD"' EXIT
TAG="rod$$"
OUTJS="zz-$TAG.js"

if [ "$DETACH" = 1 ]; then
    cat > "$TMPD/run.sh" <<SH
#!/bin/bash
systemd-run --unit=$TAG --no-block /bin/bash -c '$CMD'
SH
else
    cat > "$TMPD/run.sh" <<SH
#!/bin/bash
OUT=/usr/local/P4wnP1/HIDScripts/$OUTJS
{ $CMD ; } > /tmp/$TAG.out 2>&1
{ echo "/*"; cat /tmp/$TAG.out; echo "*/"; echo 'return "ok";'; } > "\$OUT"
rm -f /tmp/$TAG.out
SH
fi

api FSWriteFile "{\"folder\":\"BASH_SCRIPTS\",\"filename\":\"$TAG.sh\",\"data\":\"$(base64 < "$TMPD/run.sh" | tr -d '\n')\"}" >/dev/null
api DeployTriggerActionSetAdd "{\"Name\":\"$TAG\",\"TriggerActions\":[{\"isActive\":true,\"oneShot\":true,\"groupReceive\":{\"groupName\":\"$TAG\",\"value\":1},\"bashScript\":{\"scriptName\":\"$TAG.sh\"}}]}" >/dev/null
api FireActionGroupSend "{\"groupName\":\"$TAG\",\"value\":1}" >/dev/null || true

if [ "$DETACH" = 1 ]; then
    echo "[run-on-device] fired detached as unit $TAG (no output: the service is expected to go away)"
    exit 0
fi

sleep 3
for _ in $(seq 1 20); do
    R=$(api FSReadFile "{\"folder\":\"HID_SCRIPTS\",\"filename\":\"$OUTJS\",\"len\":4000}")
    echo "$R" | grep -q '"data"' && {
        echo "$R" | python3 -c 'import sys,json,base64;print(base64.b64decode(json.load(sys.stdin)["data"]).decode(errors="replace"))'
        exit 0
    }
    sleep 1
done
echo "[run-on-device] no output after 20s -- the command may still be running, or it stopped the service" >&2
exit 1
