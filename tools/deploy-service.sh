#!/usr/bin/env bash
#
# deploy-service.sh -- build the service for the Pi and install it over SSH,
#                      without reflashing the card.
#
#   P4WNP1_SSH_PASSWORD='...' ./tools/deploy-service.sh
#   P4WNP1_SSH_PASSWORD='...' ./tools/deploy-service.sh --arch arm64 --host 172.16.0.1
#
# WHY THIS EXISTS
#
# Reflashing to test a one-line fix costs a 40-minute image build, physically
# moving the card out of the Pi, and -- the part that actually hurts -- a fresh
# first boot, which regenerates every credential and locks you out of the device
# you were midway through testing. Installing the binary takes about a minute
# and keeps the credentials, so a fix can be verified on real hardware in the
# same sitting it was written.
#
# WHY IT LIVES IN THE REPO
#
# It was an expect script in a scratch directory, and that directory is wiped
# between sessions. It vanished mid-deploy, which is the same way tools/flash-sd.sh
# came to be here. A tool you need every time you touch the service belongs with
# the service.
#
# WHY expect
#
# The device authenticates with a password and macOS ships no sshpass. expect is
# in /usr/bin on every Mac. The password comes from the environment and is never
# written to disk or passed on a command line, where /proc would expose it --
# the same mistake this project just fixed in service/wifi.go.
#
# IT KEEPS A BACKUP, ON PURPOSE
#
# A bad binary means a device that does not come back, reachable only by pulling
# the card. The previous binary is kept beside the new one so recovery is one
# ssh command rather than a reflash.
set -euo pipefail

ARCH=armv6
HOST=172.16.0.1
USER=p4wnp1
RESTART=1

while [ $# -gt 0 ]; do
    case "$1" in
        --arch)    ARCH="$2"; shift 2 ;;
        --host)    HOST="$2"; shift 2 ;;
        --user)    USER="$2"; shift 2 ;;
        --no-restart) RESTART=0; shift ;;
        -h|--help)
            sed -n '2,8p' "$0" | sed 's/^# \{0,1\}//'
            exit 0 ;;
        *) echo "unknown argument: $1" >&2; exit 2 ;;
    esac
done

die() { printf '\nFAILED: %s\n' "$1" >&2; exit 1; }

[ -n "${P4WNP1_SSH_PASSWORD:-}" ] || die "set P4WNP1_SSH_PASSWORD (the panel's SSH card)"
command -v expect >/dev/null || die "expect not found; it ships in /usr/bin on macOS"

cd "$(dirname "$0")/.."

# ONE AT A TIME.
#
# Two copies of this script ran concurrently once, and both wedged: a Pi Zero W
# is a single core running hostapd, dnsmasq and the USB gadget, and two password
# logins at the same time were enough to stall both for minutes with no output.
# It looked exactly like a dead device, and the temptation at that point is to
# go and power-cycle the thing you were halfway through deploying to.
#
# mkdir is atomic on every filesystem that matters, which is why it is the lock
# rather than a test -f.
# THE LOCK RECORDS ITS OWNER, because an EXIT trap does not run on SIGKILL.
# The first version did not, and the first time this script was killed it left
# a lock nothing could clear. Every run after that refused in milliseconds --
# which, piped through `tail`, looked exactly like the hang it was put there to
# prevent. A lock you cannot tell from an abandoned one is worse than no lock.
LOCK=/tmp/.p4wnp1-deploy.lock
if ! mkdir "$LOCK" 2>/dev/null; then
    OWNER=$(cat "$LOCK/pid" 2>/dev/null || echo "")
    if [ -n "$OWNER" ] && kill -0 "$OWNER" 2>/dev/null; then
        die "another deploy is already running (pid $OWNER).
Wait for it, or stop it with:  kill $OWNER"
    fi
    echo "==> clearing a stale lock from pid ${OWNER:-unknown}, which is no longer running"
    rm -rf "$LOCK"
    mkdir "$LOCK" 2>/dev/null || die "could not take the lock at $LOCK"
fi
echo $$ > "$LOCK/pid"
trap 'rm -rf "$LOCK" 2>/dev/null || true' EXIT

case "$ARCH" in
    armv6) TARGET=build-service-armv6; BIN=build/P4wnP1_service ;;
    arm64) TARGET=build-service-arm64; BIN=build/arm64/P4wnP1_service ;;
    *) die "--arch must be armv6 (Pi Zero W) or arm64 (Pi Zero 2 W)" ;;
esac

echo "==> building ($ARCH)"
make "$TARGET" >/dev/null
[ -f "$BIN" ] || die "$BIN was not produced"
LOCAL_SUM=$(shasum -a 256 "$BIN" | cut -c1-16)
printf '    %s  %s bytes\n' "$LOCAL_SUM" "$(wc -c < "$BIN" | tr -d ' ')"

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

# The password reaches expect through the environment, never argv.
cat > "$WORK/ssh.exp" <<'EXP'
#!/usr/bin/expect -f
set timeout 600
spawn -noecho ssh -o ConnectTimeout=20 -o StrictHostKeyChecking=accept-new \
    -o LogLevel=ERROR [lindex $argv 0]@[lindex $argv 1] [lindex $argv 2]
expect { -re "(?i)password:" { send "$env(P4WNP1_SSH_PASSWORD)\r"; exp_continue } eof }
catch wait result
exit [lindex $result 3]
EXP
cat > "$WORK/scp.exp" <<'EXP'
#!/usr/bin/expect -f
set timeout 600
spawn -noecho scp -o ConnectTimeout=20 -o StrictHostKeyChecking=accept-new \
    -o LogLevel=ERROR [lindex $argv 0] [lindex $argv 1]@[lindex $argv 2]:[lindex $argv 3]
expect { -re "(?i)password:" { send "$env(P4WNP1_SSH_PASSWORD)\r"; exp_continue } eof }
catch wait result
exit [lindex $result 3]
EXP
chmod +x "$WORK"/*.exp

run() { "$WORK/ssh.exp" "$USER" "$HOST" "$1"; }

# TWO LOGINS, NOT FIVE.
#
# This used to be a login per step -- check, verify, install, restart -- and
# each one is a full password authentication against a board that is already
# busy. They were slow enough to look like a hang. Everything that happens on
# the device now happens in ONE remote shell, with the checksum gate as the
# first link of an && chain, so a corrupted copy still cannot reach
# /usr/local/bin.
echo "==> copying (16MB over the USB link, a few seconds)"
"$WORK/scp.exp" "$BIN" "$USER" "$HOST" /tmp/P4wnP1_service_new >/dev/null \
    || die "copy failed"

RESTART_CMD="true"
[ "$RESTART" = "1" ] && RESTART_CMD="sudo systemctl restart P4wnP1.service && echo '    restarted'"

echo "==> verifying the copy, installing, restarting (one session)"
run "set -e
     echo -n '    arch: '; uname -m
     REMOTE=\$(sha256sum /tmp/P4wnP1_service_new | cut -c1-16)
     if [ \"\$REMOTE\" != '$LOCAL_SUM' ]; then
         echo \"    CHECKSUM MISMATCH: local $LOCAL_SUM, remote \$REMOTE -- NOT installing\"
         rm -f /tmp/P4wnP1_service_new
         exit 3
     fi
     echo \"    \$REMOTE matches\"
     sudo cp -a /usr/local/bin/P4wnP1_service /usr/local/bin/P4wnP1_service.bak 2>/dev/null || true
     sudo install -m 755 -o root -g root /tmp/P4wnP1_service_new /usr/local/bin/P4wnP1_service
     rm -f /tmp/P4wnP1_service_new
     echo '    installed (previous kept as P4wnP1_service.bak)'
     $RESTART_CMD" || die "install failed on the device (see the output above)"

if [ "$RESTART" = "1" ]; then
    echo "==> waiting for the API"
    for i in $(seq 1 40); do
        if curl -s -m 3 "http://$HOST:8000/api/auth/health" >/dev/null 2>&1; then
            echo "    answering after ${i}s"
            break
        fi
        sleep 2
        [ "$i" = 40 ] && die "the API did not come back. Roll back with:
  ssh $USER@$HOST 'sudo install -m755 /usr/local/bin/P4wnP1_service.bak /usr/local/bin/P4wnP1_service && sudo systemctl restart P4wnP1.service'"
    done
fi

cat <<EOF

Deployed. The device now runs a binary built from your working tree, which is
NEWER than whatever image is on the card -- a reflash will revert it.

Verify:  P4WNP1_PASSWORD='<web password>' ./tools/hardware-check.sh
EOF
