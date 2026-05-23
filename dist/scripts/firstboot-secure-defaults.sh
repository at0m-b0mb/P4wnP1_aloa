#!/bin/bash
#
# firstboot-secure-defaults.sh
#
# Runs once, on the very first boot of a freshly flashed P4wnP1 image, and
# replaces the well-known shared defaults with per-device random secrets:
#
#   - root SSH password (`toor` -> random 20 chars)
#   - SSH host keys (shared in prebuilt images -> freshly generated per host)
#
# The generated credentials are written to:
#
#   /root/INITIAL_CREDENTIALS.txt   (mode 0600, root-only)
#   systemd journal                 (warn level, kept for audit)
#
# A flag file at /var/lib/p4wnp1/firstboot.done marks completion; the script
# refuses to run again once that file exists. Delete the flag to force a re-run.
#
# If /etc/p4wnp1/initial.conf exists (written by install.sh), the WiFi SSID and
# PSK chosen at install time are surfaced in the credentials file so the
# operator has a single document with everything. The values themselves are
# NOT pushed into the P4wnP1 badger DB by this script -- doing so reliably
# requires a known-good CLI invocation that varies by image revision. The
# credentials file points the operator at the web UI for the one-click update.
#
# Things this script intentionally does NOT touch:
#   - the WiFi access point PSK in the running service's DB
#   - the Bluetooth pairing PIN
#   - the web client (it has no auth at all -- that is a separate roadmap item)

set -euo pipefail

FLAG_DIR=/var/lib/p4wnp1
FLAG_FILE="${FLAG_DIR}/firstboot.done"
CREDS_FILE=/root/INITIAL_CREDENTIALS.txt
INITIAL_CONF=/etc/p4wnp1/initial.conf
LOG_TAG=p4wnp1-firstboot

# Defaults if /etc/p4wnp1/initial.conf doesn't exist (e.g. the user installed
# manually without the installer). These match service/defaults.go.
P4WNP1_INITIAL_SSID=HackProKP
P4WNP1_INITIAL_PSK="(unset -- still on service default 'MaMe82-P4wnP1')"

# shellcheck source=/dev/null
[[ -r "${INITIAL_CONF}" ]] && . "${INITIAL_CONF}"

log() {
    logger -t "${LOG_TAG}" -p user.warn -- "$*"
    echo "[firstboot] $*" >&2
}

if [[ $EUID -ne 0 ]]; then
    log "must run as root"
    exit 1
fi

mkdir -p "${FLAG_DIR}"

if [[ -e "${FLAG_FILE}" ]]; then
    log "flag file ${FLAG_FILE} exists; firstboot already completed, exiting"
    exit 0
fi

# --- random password generation ---------------------------------------------
# Prefer openssl, fall back to /dev/urandom + tr. The Pi has haveged enabled
# by default in P4wnP1 images so urandom is well-seeded by the time we run.
gen_password() {
    local length="${1:-20}"
    if command -v openssl >/dev/null 2>&1; then
        openssl rand -base64 32 | tr -d '/+=\n' | head -c "${length}"
    else
        LC_ALL=C tr -dc 'A-Za-z0-9' </dev/urandom | head -c "${length}"
    fi
}

NEW_ROOT_PW=$(gen_password 20)

# --- rotate root password ---------------------------------------------------
log "rotating root password"
echo "root:${NEW_ROOT_PW}" | chpasswd
# Force password change is intentionally NOT used -- the operator may need to
# script unattended logins immediately. The credentials file tells them what
# was set; rotation policy is up to them.

# --- regenerate SSH host keys -----------------------------------------------
# Prebuilt images ship with identical host keys. Without regeneration, every
# P4wnP1 in the world presents the same key, defeating the point of TOFU.
log "regenerating SSH host keys"
rm -f /etc/ssh/ssh_host_*_key /etc/ssh/ssh_host_*_key.pub
if command -v ssh-keygen >/dev/null 2>&1; then
    ssh-keygen -A
    if systemctl is-enabled --quiet ssh 2>/dev/null; then
        systemctl restart ssh || log "ssh restart failed; new keys take effect on next start"
    fi
else
    log "ssh-keygen not found; host keys NOT regenerated (install openssh-server)"
fi

# --- capture SSH fingerprints for the operator ------------------------------
SSH_FPS=""
for keyfile in /etc/ssh/ssh_host_*_key.pub; do
    [[ -f "${keyfile}" ]] || continue
    fp=$(ssh-keygen -lf "${keyfile}" 2>/dev/null || true)
    SSH_FPS+="    ${fp}"$'\n'
done

# --- write credentials file -------------------------------------------------
umask 077
cat > "${CREDS_FILE}" <<EOF
==============================================================================
 P4wnP1 A.L.O.A. -- initial credentials (generated on first boot)
 Generated: $(date -u +'%Y-%m-%dT%H:%M:%SZ')
 Host:      $(hostname)
==============================================================================

  SSH root password:    ${NEW_ROOT_PW}

  SSH host fingerprints (verify these on first connection):
${SSH_FPS}
------------------------------------------------------------------------------
 WiFi credentials chosen at install time
------------------------------------------------------------------------------

  WiFi AP SSID:         ${P4WNP1_INITIAL_SSID}
  WiFi AP PSK:          ${P4WNP1_INITIAL_PSK}

  These values were captured by install.sh. The running P4wnP1 service still
  has to be told to broadcast them. After your first web-client login at
  http://172.24.0.1:8000 (or 172.16.0.1 via USB), apply them:
      web client -> WiFi -> Settings -> set SSID + PSK -> save as template
                                                       -> set as default

------------------------------------------------------------------------------
 ITEMS STILL ON SHARED DEFAULTS -- change these manually:
------------------------------------------------------------------------------

  Bluetooth PIN:        1337
                          -> web client -> Bluetooth -> Settings -> new PIN
                          -> or: systemctl disable --now bluetooth if unused

  Web client:           NO AUTHENTICATION on the gRPC API.
                          -> restrict reachability via iptables/nftables, or
                          -> only enable the AP/BT when actively using P4wnP1.

  This file is mode 0600 and owned by root. After you have copied the
  credentials somewhere safe, shred it:

       shred -u ${CREDS_FILE}

==============================================================================
EOF
chmod 0600 "${CREDS_FILE}"

# Also log a short summary to journal so an operator who SSHs in immediately
# sees something useful. The full creds file stays on disk (mode 0600).
log "first-boot security setup complete; see ${CREDS_FILE} for new credentials"
log "WiFi PSK and Bluetooth PIN are STILL at shared defaults -- change via web UI"

touch "${FLAG_FILE}"
chmod 0600 "${FLAG_FILE}"

exit 0
