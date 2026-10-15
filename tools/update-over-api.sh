#!/usr/bin/env bash
#
# update-over-api.sh -- install an update bundle using only the device's own
#                       JSON API. No SSH, no scp.
#
#   P4WNP1_PASSWORD='...' ./tools/update-over-api.sh image/out/<bundle>.tar.xz
#
# WHY THIS EXISTS
#
# Two reasons, and the second is the interesting one.
#
# 1. scp to this device is unreliable in practice. ssh reaches authentication
#    instantly, and scp then hangs after the password is accepted -- it needs
#    the SFTP subsystem, which ssh-with-a-command does not. Rather than keep
#    fighting that, everything here goes over the API, which is healthy.
#
# 2. It is the same path the web console would take. The console runs in a
#    browser on the operator's own laptop, which HAS internet even when the
#    device does not -- so the browser fetches the bundle and carries it across
#    the USB link. This script is that design, proven from a shell first.
#
# WHY IT UPLOADS IN CHUNKS
#
# service/rest_api.go caps a request body at 8 MiB, deliberately, so that an
# unauthenticated peer cannot balloon memory before the token check runs. A
# bundle is bigger than that. Raising the cap for convenience would weaken a
# security control, so this uses FSWriteFile's `append` flag instead and stays
# inside it.
#
# WHY IT RUNS THE UPDATER THROUGH systemd-run
#
# The only way to execute something as root over the API is a trigger action,
# and a trigger action runs as a CHILD OF THE SERVICE. The updater's first move
# is to stop that service -- and systemd kills the whole control group, so the
# updater would kill itself halfway through, leaving the device with new
# binaries and a stopped service.
#
# systemd-run puts it in a transient unit of its own, outside that cgroup, so
# stopping P4wnP1.service no longer reaches it. --no-block so the trigger
# action returns immediately rather than waiting on an update that is about to
# stop the thing it would return to.
set -euo pipefail

HOST="${P4WNP1_HOST:-172.16.0.1}"
PORT="${P4WNP1_PORT:-8000}"
USER="${P4WNP1_USER:-admin}"
BUNDLE="${1:-}"
API="http://$HOST:$PORT"
CHUNK=$((3 * 1024 * 1024))   # 3 MiB raw -> ~4 MiB base64, well under the 8 MiB cap

say()  { printf '\033[1;32m[api-update]\033[0m %s\n' "$*"; }
die()  { printf '\033[1;31m[api-update:FAILED]\033[0m %s\n' "$*" >&2; exit 1; }

[ -n "$BUNDLE" ]            || die "usage: $0 <bundle.tar.xz>"
[ -r "$BUNDLE" ]            || die "cannot read $BUNDLE"
[ -n "${P4WNP1_PASSWORD:-}" ] || die "set P4WNP1_PASSWORD (the panel's Web card)"

NAME=$(basename "$BUNDLE")
SUM=$(shasum -a 256 "$BUNDLE" | awk '{print $1}')
SIZE=$(wc -c < "$BUNDLE" | tr -d ' ')
say "$NAME  $SIZE bytes  ${SUM:0:16}"

TOK=$(curl -s -m 30 -X POST "$API/api/auth/login" -H 'Content-Type: application/json' \
      -d "{\"username\":\"$USER\",\"password\":\"$P4WNP1_PASSWORD\"}" \
      | python3 -c 'import sys,json;print(json.load(sys.stdin).get("token",""))')
[ -n "$TOK" ] || die "login failed"
say "logged in"

rpc() { # rpc <Method> <json>
    curl -s -m 120 -X POST "$API/api/v1/rpc/$1" \
         -H "Authorization: Bearer $TOK" -H 'Content-Type: application/json' -d "$2"
}

# rpc_file <Method> <file-containing-the-json-body>
#
# A chunk of base64 is megabytes long, and passing it as an ARGUMENT dies with
# "Argument list too long" -- ARG_MAX is about 1 MiB on macOS and the shell
# counts the whole command line. The body goes through a file instead, which
# has no such limit.
rpc_file() {
    curl -s -m 300 -X POST "$API/api/v1/rpc/$1" \
         -H "Authorization: Bearer $TOK" -H 'Content-Type: application/json' \
         --data-binary "@$2"
}

# --- upload in chunks -------------------------------------------------------

TMPD=$(mktemp -d); trap 'rm -rf "$TMPD"' EXIT
split -b "$CHUNK" "$BUNDLE" "$TMPD/part."
PARTS=$(ls "$TMPD"/part.* | sort)
N=$(echo "$PARTS" | wc -l | tr -d ' ')
say "uploading in $N chunk(s) of up to $((CHUNK/1024/1024)) MiB"

i=0
for part in $PARTS; do
    i=$((i+1))
    APPEND=true; [ "$i" = 1 ] && APPEND=false      # first chunk truncates
    # Build the body in a file. python does the base64 and the JSON quoting in
    # one pass, so neither the chunk nor the filename can break the encoding.
    python3 - "$part" "$NAME" "$APPEND" > "$TMPD/body.json" <<'ENC'
import base64, json, sys
part, name, append = sys.argv[1], sys.argv[2], sys.argv[3] == "true"
with open(part, "rb") as f:
    json.dump({"folder": "TMP", "filename": name,
               "append": append,
               "data": base64.b64encode(f.read()).decode()}, sys.stdout)
ENC
    R=$(rpc_file FSWriteFile "$TMPD/body.json")
    [ "$R" = "{}" ] || die "chunk $i/$N rejected: $(echo "$R" | head -c 200)"
    printf '\r    %d/%d' "$i" "$N"
done
echo

# --- prove it arrived intact ------------------------------------------------
#
# The device computes the checksum itself. Trusting the upload because each
# chunk returned {} would only prove the requests were accepted, not that the
# bytes on disk are the bytes we sent.
say "verifying on the device"
cat > "$TMPD/verify.sh" <<VERIFY
#!/bin/bash
sha256sum /tmp/$NAME > /tmp/p4-bundle.sum 2>&1
VERIFY
rpc FSWriteFile "{\"folder\":\"BASH_SCRIPTS\",\"filename\":\"p4-verify-bundle.sh\",\"data\":\"$(base64 < "$TMPD/verify.sh" | tr -d '\n')\"}" >/dev/null

# --- the updater, in a transient unit --------------------------------------

cat > "$TMPD/doupdate.sh" <<UPD
#!/bin/bash
# Unpack far enough to get the updater that ships inside the bundle: the device
# may not have one yet, which is the bootstrap case for the very first update.
set -e
rm -rf /tmp/p4upd && mkdir -p /tmp/p4upd
tar xJf /tmp/$NAME -C /tmp/p4upd
install -m 0755 /tmp/p4upd/bin/p4wnp1-update /usr/local/bin/p4wnp1-update
# OUTSIDE this service's cgroup -- see the header. Stopping P4wnP1.service
# would otherwise kill the updater that is doing the stopping.
systemd-run --unit=p4wnp1-selfupdate --no-block \
    /usr/local/bin/p4wnp1-update --file /tmp/$NAME
UPD
rpc FSWriteFile "{\"folder\":\"BASH_SCRIPTS\",\"filename\":\"p4-doupdate.sh\",\"data\":\"$(base64 < "$TMPD/doupdate.sh" | tr -d '\n')\"}" >/dev/null
say "staged the update script"

GROUP="p4update$$"
TA="{\"Name\":\"api-update\",\"TriggerActions\":[{\"isActive\":true,\"oneShot\":true,\"groupReceive\":{\"groupName\":\"$GROUP\",\"value\":1},\"bashScript\":{\"scriptName\":\"p4-doupdate.sh\"}}]}"
R=$(rpc DeployTriggerActionSetAdd "$TA")
echo "$R" | grep -q '"error"' && die "could not register the trigger: $(echo "$R" | head -c 200)"
say "trigger armed on group $GROUP"

say "firing -- the service will stop and come back"
rpc FireActionGroupSend "{\"groupName\":\"$GROUP\",\"value\":1}" >/dev/null || true

# --- wait for it to return --------------------------------------------------

say "waiting for the API (up to 180s)"
sleep 10
BACK=0
for i in $(seq 1 180); do
    if curl -fsS -m 2 "$API/api/auth/health" >/dev/null 2>&1; then BACK=$i; break; fi
    sleep 1
done
[ "$BACK" != 0 ] || die "the API did not come back. The updater health-checks and rolls back on
its own, so the device is probably on its previous version -- but check the panel."
say "API answering again after ~$((BACK+10))s"
