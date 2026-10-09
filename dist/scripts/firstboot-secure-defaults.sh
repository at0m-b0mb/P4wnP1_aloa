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
#   - the console's own password (it IS bootstrapped below, via p4wnp1-hashpw;
#     this comment used to say the web client had no auth at all, which has
#     been untrue since the auth layer landed)

set -euo pipefail

# Whatever happens below -- success, failure, or a signal -- the busy marker
# and the LED must not be left as they are. A device that finished an hour ago
# and is still flashing "do not power off" trains people to ignore the signal,
# which is worse than not having one.
cleanup_announce() {
    local rc=$?
    if [ "${ANNOUNCED:-0}" = "1" ]; then
        if [ "$rc" -eq 0 ]; then announce_done; else led_setup_done; fi
    fi
    return $rc
}
trap cleanup_announce EXIT

FLAG_DIR=/var/lib/p4wnp1
FLAG_FILE="${FLAG_DIR}/firstboot.done"
CREDS_FILE=/root/INITIAL_CREDENTIALS.txt
INITIAL_CONF=/etc/p4wnp1/initial.conf
LOG_TAG=p4wnp1-firstboot

# Defaults if /etc/p4wnp1/initial.conf doesn't exist (e.g. the user installed
# manually without the installer). These match service/defaults.go.
P4WNP1_INITIAL_SSID=HackProKP
P4WNP1_INITIAL_PSK="(unset -- still on service default 'MaMe82-P4wnP1')"

# Syntax-check before sourcing.
#
# This script runs under `set -euo pipefail`, so sourcing a file with an
# unbalanced quote aborts it -- and this script is what creates the admin
# account, so aborting leaves a device that rejects every console request. The
# header of initial.conf invites hand-editing, so a stray quote is a question
# of when, not whether. `bash -n` parses without executing, which turns a
# bricked first boot into a warning and a fall back to the defaults above.
if [[ -r "${INITIAL_CONF}" ]]; then
    if bash -n "${INITIAL_CONF}" 2>/dev/null; then
        # shellcheck source=/dev/null
        . "${INITIAL_CONF}"
    else
        echo "[firstboot] WARNING: ${INITIAL_CONF} is not valid shell and was ignored." >&2
        echo "[firstboot] WARNING: falling back to the built-in defaults." >&2
    fi
fi

log() {
    logger -t "${LOG_TAG}" -p user.warn -- "$*"
    echo "[firstboot] $*" >&2
}

if [[ $EUID -ne 0 ]]; then
    log "must run as root"
    exit 1
fi

mkdir -p "${FLAG_DIR}"

# --- tell the operator not to pull the plug ---------------------------------
#
# First boot takes minutes on a Pi Zero W: resize, three SSH host keys on one
# 1GHz core, the web admin, the key off the card. For all of it the device
# looks idle -- no network yet, nothing obvious happening -- and the natural
# thing to do with an appliance that seems hung is unplug it. That is the one
# genuinely destructive act available here: a half-written auth.json, host
# keys generated but never installed, an interrupted resize.
#
# A device WITH a panel says so on the panel. This is for every other device,
# and for anyone who looks at the card afterwards and wonders.
#
# Two signals, because neither is enough alone:
#
#   the ACT LED   fast heartbeat while setup runs, normal activity after.
#                 Visible across a room, needs nothing but eyes.
#   a file on     /boot/firmware/DO-NOT-POWER-OFF.txt exists only while setup
#   the card      is in progress; SETUP-COMPLETE.txt replaces it at the end.
#                 Readable from any laptop, and if someone DID pull the plug
#                 early, the file left behind tells them exactly that.
BOOT_DIR_EARLY=/boot/firmware
[ -d "$BOOT_DIR_EARLY" ] || BOOT_DIR_EARLY=/boot
BUSY_FILE="${BOOT_DIR_EARLY}/DO-NOT-POWER-OFF.txt"
DONE_FILE="${BOOT_DIR_EARLY}/SETUP-COMPLETE.txt"

# The LED is ACT on most boards and led0 on others. Try both, care about
# neither failing -- a device with no LED must still boot.
led_path() {
    for p in /sys/class/leds/ACT /sys/class/leds/led0 /sys/class/leds/mmc0::; do
        [ -d "$p" ] && { printf '%s' "$p"; return 0; }
    done
    return 1
}
led_setup_running() {
    local l; l=$(led_path) || return 0
    echo timer > "$l/trigger" 2>/dev/null || return 0
    echo 120   > "$l/delay_on"  2>/dev/null || true
    echo 120   > "$l/delay_off" 2>/dev/null || true
}
led_setup_done() {
    local l; l=$(led_path) || return 0
    # Back to whatever the board normally does with it.
    echo mmc0 > "$l/trigger" 2>/dev/null || echo none > "$l/trigger" 2>/dev/null || true
}

announce_busy() {
    led_setup_running
    rm -f "$DONE_FILE" 2>/dev/null || true
    cat > "$BUSY_FILE" 2>/dev/null <<BUSY || true
P4wnP1 A.L.O.A. -- FIRST BOOT IN PROGRESS. DO NOT POWER THE DEVICE OFF.

While this file exists, the device is still setting itself up:

  * growing the root filesystem to fill the card
  * generating this device's own SSH host keys
  * creating the web console administrator
  * adopting any authorized_keys you left on this partition

On a Raspberry Pi Zero W that takes a few minutes, and for most of it the
device looks idle. It is not. The green LED is blinking steadily while setup
runs; it returns to normal card-activity flicker when it is finished.

Pulling the power during this window can leave a half-written credentials
file, SSH host keys that were generated but never installed, or an
interrupted filesystem resize.

When setup finishes this file is replaced by SETUP-COMPLETE.txt. If you are
reading THIS file on a card you have taken out of a device, setup did not
finish -- reflash it.
BUSY
    sync 2>/dev/null || true
}

announce_done() {
    led_setup_done
    rm -f "$BUSY_FILE" 2>/dev/null || true
    cat > "$DONE_FILE" 2>/dev/null <<DONE || true
P4wnP1 A.L.O.A. -- first boot completed. Safe to power off.

Finished: $(date -u +'%Y-%m-%dT%H:%M:%SZ' 2>/dev/null || echo unknown)
Host:     $(hostname 2>/dev/null || echo unknown)

This device now has its own SSH host keys and its own credentials. Nothing
here is shared with any other device built from the same image.

  ssh ${OPERATOR_USER:-p4wnp1}@172.16.0.1     (over the USB cable)
  web http://172.16.0.1:8000

How to reach it depends on what you left on this partition before first boot:

  authorized_keys present  -> the account is reachable by that key and its
                              password is LOCKED. Nothing secret is on this
                              card. This is the recommended way.
  nothing present          -> a password was generated and written next to
                              this file as p4wnp1-credentials.txt. Read it,
                              then delete it.

The web console password is NOT on this card. On a device with the OLED HAT
the panel shows it once and then erases every copy. Otherwise it is in
/root/INITIAL_CREDENTIALS.txt, readable once you are on the device.
DONE
    sync 2>/dev/null || true
}

if [[ -e "${FLAG_FILE}" ]]; then
    log "flag file ${FLAG_FILE} exists; firstboot already completed, exiting"
    exit 0
fi

# Past here we are genuinely setting up, so start saying so. ANNOUNCED gates
# the EXIT trap: an early return above must not clear a marker it never set,
# or a reboot during someone else's setup would wrongly report completion.
ANNOUNCED=1
announce_busy
log "setup starting -- LED on heartbeat, ${BUSY_FILE} written"

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

# --- issue a per-device operator password -----------------------------------
#
# The image ships this account LOCKED (image/lib/customize.sh). It used to ship
# p4wnp1:p4wnp1 on every device, with NOPASSWD:ALL sudo and headless SSH
# enabled, which meant anyone who could reach port 22 -- including the host the
# appliance was plugged into -- could take root on it. `chage -d 0` did not
# save it: forcing a password change at first login just means the attacker
# picks the new password.
#
# A password generated here is per-device, because it is generated ON the
# device. The problem that remains is purely one of delivery: a headless box
# cannot show it to you. Two answers, in order of preference:
#
#   1. You set your own at flash time. Raspberry Pi Imager writes
#      /boot/firmware/userconf.txt and Raspberry Pi OS's own service creates
#      that account before we run. If ANY unlocked, non-system, sudo-capable
#      account already exists, you have a way in and this leaves p4wnp1 locked.
#   2. Otherwise we generate one and write it to the BOOT partition, which is
#      FAT and readable from the machine you flashed the card with. Put the
#      card back in your laptop and read it.
#
# Writing a credential to a FAT partition is not nothing -- anyone holding the
# card can read it. That is a deliberate trade against the alternative, which
# was the same password on every device in the world, and it is stated in the
# file itself so nobody discovers it later.
BOOT_DIR=/boot/firmware
[ -d "$BOOT_DIR" ] || BOOT_DIR=/boot
OPERATOR_USER="${P4WNP1_OPERATOR_USER:-p4wnp1}"
OPERATOR_PW_SET=""

# Is there already a human account that can log in and reach root?
someone_can_log_in() {
    local u
    while IFS=: read -r u _ uid _ _ _ shell; do
        [ "$uid" -ge 1000 ] 2>/dev/null || continue
        [ "$u" = "$OPERATOR_USER" ] && continue
        case "$shell" in */nologin|*/false) continue ;; esac
        # A hash field of "!" or "*" (or "!..." ) means locked.
        case "$(passwd -S "$u" 2>/dev/null | awk '{print $2}')" in
            P) return 0 ;;
        esac
    done < /etc/passwd
    return 1
}

# --- adopt an SSH public key left on the boot partition ----------------------
#
# The whole difficulty with a headless appliance is DELIVERY: it has to let
# you in, and it has no way to tell you a secret. Every answer below this line
# is a workaround for that. A public key is not a workaround -- it is the
# problem not existing. A public key is not a secret, so leaving one on a FAT
# partition that anyone holding the card can read costs nothing, where leaving
# a generated password there costs everything.
#
# So: drop your authorized_keys on the boot partition after flashing, and this
# device is reachable on first boot with no password anywhere, on the card or
# in its own filesystem.
#
#     cp ~/.ssh/id_ed25519.pub /Volumes/bootfs/authorized_keys
#
# Keys are APPENDED, never replaced, and the card file is left alone. Both
# matter: a device that silently discarded a key you added on it later, every
# time it rebooted, would be worse than useless -- and the card file is how
# you recover if you ever reset the root filesystem.
OPERATOR_KEY_INSTALLED=0
adopt_boot_keys() {
    local src dest home_dir added=0 line
    for src in "${BOOT_DIR}/authorized_keys" "${BOOT_DIR}/p4wnp1_authorized_keys"; do
        [ -s "$src" ] || continue
        if ! grep -qE '^[[:space:]]*(ssh-(rsa|ed25519|dss)|ecdsa-sha2-|sk-ssh-|sk-ecdsa-)' "$src"; then
            log "WARNING: ${src} holds no OpenSSH public key; ignoring it"
            continue
        fi
        home_dir=$(getent passwd "$OPERATOR_USER" | cut -d: -f6)
        [ -n "$home_dir" ] || home_dir="/home/${OPERATOR_USER}"
        dest="${home_dir}/.ssh/authorized_keys"
        install -d -m 0700 -o "$OPERATOR_USER" -g "$OPERATOR_USER" "${home_dir}/.ssh"
        [ -f "$dest" ] || install -m 0600 -o "$OPERATOR_USER" -g "$OPERATOR_USER" /dev/null "$dest"
        while IFS= read -r line; do
            case "$line" in ''|'#'*) continue ;; esac
            grep -qxF -- "$line" "$dest" 2>/dev/null && continue
            printf '%s\n' "$line" >> "$dest"
            added=$((added+1))
        done < "$src"
        chown "$OPERATOR_USER:$OPERATOR_USER" "$dest"
        chmod 0600 "$dest"
        OPERATOR_KEY_INSTALLED=1
        log "adopted ${added} new public key(s) from ${src} into ${dest}"
    done
    [ "$OPERATOR_KEY_INSTALLED" = "1" ]
}

# Leaving the account LOCKED is the honest outcome when the password cannot be
# delivered. An account whose secret exists only inside the device -- in
# /root/INITIAL_CREDENTIALS.txt, which you must already be root to read -- is
# not a way in, it is a deadlock. A locked account is still rescuable: put the
# card in a reader and let Raspberry Pi Imager write userconf.txt.
operator_delivery_failed() {
    log "ERROR: cannot deliver a password for '${OPERATOR_USER}': $1"
    log "ERROR: leaving the account LOCKED -- a secret nobody holds is worse than none"
    log "ERROR: to get in, put the card in a reader and use Raspberry Pi Imager's"
    log "ERROR: 'Set username and password', which writes ${BOOT_DIR}/userconf.txt"
}

if id -u "$OPERATOR_USER" >/dev/null 2>&1; then
    adopt_boot_keys || true
    if [ "$OPERATOR_KEY_INSTALLED" = "1" ]; then
        log "'${OPERATOR_USER}' is reachable by public key; leaving its password LOCKED"
        log "no credential has been written to ${BOOT_DIR}"
    elif someone_can_log_in; then
        log "another account can already log in; leaving '${OPERATOR_USER}' locked"
    elif [ "$(passwd -S "$OPERATOR_USER" 2>/dev/null | awk '{print $2}')" = "P" ]; then
        log "'${OPERATOR_USER}' already has a password; leaving it alone"
    else
        # DELIVER FIRST, THEN SET.
        #
        # This used to run chpasswd and then try to write the file, warning if
        # it could not. That warning described an unrecoverable device: the
        # account now has a password, the only readable copy was never
        # written, and the other copy lives in /root/INITIAL_CREDENTIALS.txt,
        # which you need to be root to read, which needs the password. A
        # locked account you can still rescue by putting the card back in a
        # reader. An account with a secret nobody holds, you cannot.
        #
        # So the password is generated, written, and READ BACK, and only then
        # applied. If delivery fails for any reason the account stays locked
        # and the journal says so in terms that name the remedy.
        OPERATOR_PW_CAND=$(gen_password 20)
        OPERATOR_CREDS_FILE="${BOOT_DIR}/p4wnp1-credentials.txt"

        # The write is INSIDE the if-condition on purpose. A command in a
        # condition is exempt from `set -e`, so a FAT partition that is full
        # or has been remounted read-only takes the failure branch below
        # instead of killing first boot outright -- which would also skip the
        # web admin bootstrap and the SSH host-key regeneration that come
        # after it, turning one unreadable file into an unusable device.
        if [ -d "$BOOT_DIR" ] && [ -w "$BOOT_DIR" ] &&
           cat > "${OPERATOR_CREDS_FILE}" <<CREDS
P4wnP1 A.L.O.A. -- first-boot credentials for THIS device

  ssh ${OPERATOR_USER}@172.16.0.1          (over the USB ethernet link)
  password: ${OPERATOR_PW_CAND}

This password was generated on this device at first boot. It is not shared
with any other device.

It is written here because a headless appliance has no other way to show it to
you: this is the FAT boot partition, readable from the machine you flashed the
card with. ANYONE HOLDING THE CARD CAN READ IT. Log in, change the password,
and delete this file:

  passwd
  sudo rm /boot/firmware/p4wnp1-credentials.txt

The web console password and the WiFi PSK are NOT here. They are in
/root/INITIAL_CREDENTIALS.txt, readable once you are on the device.

To avoid this file entirely, set your own account at flash time -- Raspberry Pi
Imager's "Set username and password" writes /boot/firmware/userconf.txt, and
first boot then leaves this account locked.
CREDS
        then
            sync 2>/dev/null || true
            # Read it back before trusting it. A full or read-only FAT
            # partition can accept the redirect and keep nothing, and the
            # only symptom would be an operator who cannot log in.
            if [ -s "${OPERATOR_CREDS_FILE}" ] &&
               grep -qF -- "${OPERATOR_PW_CAND}" "${OPERATOR_CREDS_FILE}" 2>/dev/null; then
                echo "${OPERATOR_USER}:${OPERATOR_PW_CAND}" | chpasswd
                OPERATOR_PW_SET="${OPERATOR_PW_CAND}"
                log "issued a per-device password for '${OPERATOR_USER}'"
                log "wrote ${OPERATOR_CREDS_FILE} -- read it from the card, then delete it"
            else
                rm -f "${OPERATOR_CREDS_FILE}" 2>/dev/null || true
                operator_delivery_failed "the file could not be read back (partition full?)"
            fi
        else
            operator_delivery_failed "${BOOT_DIR} is not writable"
        fi
        unset OPERATOR_PW_CAND
    fi
fi

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

# --- bootstrap web UI admin account -----------------------------------------
# The P4wnP1 service refuses to authenticate anyone if /etc/p4wnp1/auth.json
# is missing. Generate a random admin password and hand it to p4wnp1-hashpw,
# which bcrypts it and writes the auth file. Surface the plaintext to the
# operator (once) via INITIAL_CREDENTIALS.txt.
ADMIN_USER="admin"
NEW_ADMIN_PW=$(gen_password 20)
ADMIN_BOOTSTRAP_OK=0
if command -v p4wnp1-hashpw >/dev/null 2>&1; then
    log "bootstrapping web UI admin account"
    mkdir -p /etc/p4wnp1
    # Pipe via stdin so the password never lands on disk in plaintext.
    if printf '%s' "${NEW_ADMIN_PW}" | \
        p4wnp1-hashpw \
            --username "${ADMIN_USER}" \
            --output /etc/p4wnp1/auth.json >/dev/null 2>&1; then
        chmod 0600 /etc/p4wnp1/auth.json
        chown root:root /etc/p4wnp1/auth.json
        ADMIN_BOOTSTRAP_OK=1
    else
        log "p4wnp1-hashpw failed -- web UI admin NOT bootstrapped"
    fi
else
    log "p4wnp1-hashpw not found in PATH -- web UI admin NOT bootstrapped"
fi

# --- write credentials file -------------------------------------------------
umask 077
cat > "${CREDS_FILE}" <<EOF
==============================================================================
 P4wnP1 A.L.O.A. -- initial credentials (generated on first boot)
 Generated: $(date -u +'%Y-%m-%dT%H:%M:%SZ')
 Host:      $(hostname)
==============================================================================

  SSH root password:    ${NEW_ROOT_PW}
                          (root cannot log in over SSH: Raspberry Pi OS ships
                           PermitRootLogin prohibit-password. Use the operator
                           account below, or a console.)

  SSH operator account: ${OPERATOR_USER}
  Operator password:    ${OPERATOR_PW_SET:-(unchanged -- either you set one at flash time, or it is still locked)}

  SSH host fingerprints (verify these on first connection):
${SSH_FPS}
------------------------------------------------------------------------------
 Web client (http://172.24.0.1:8000) admin login
------------------------------------------------------------------------------

  Username:             ${ADMIN_USER}
  Password:             ${NEW_ADMIN_PW}
  Bootstrap status:     $([ "${ADMIN_BOOTSTRAP_OK}" = "1" ] && echo "OK" || echo "FAILED -- log in via SSH and run p4wnp1-hashpw manually")

  This password is bcrypt-hashed in /etc/p4wnp1/auth.json. Change it from
  the web client (Settings -> Change Password) on first login.

  Until you log in, EVERY gRPC and HTTP request to the P4wnP1 service is
  rejected with HTTP 401 / gRPC Unauthenticated. There is no anonymous
  access.

------------------------------------------------------------------------------
 WiFi credentials chosen at install time
------------------------------------------------------------------------------

  WiFi AP SSID:         ${P4WNP1_INITIAL_SSID}
  WiFi AP PSK:          ${P4WNP1_INITIAL_PSK}

  These values were captured by install.sh. The running P4wnP1 service still
  has to be told to broadcast them. Setting the SSID and PSK is a CLI job --
  there is no field for it in the web console:
      P4wnP1_cli wifi set ap --ssid '<SSID>' --psk '<PSK>'
  Then, to keep that configuration and have it load on every boot:
      web console -> Radio -> WiFi -> "Store as template"
      web console -> Loadouts -> "Compose a loadout" -> name the WiFi template
                             -> "Use at boot"

------------------------------------------------------------------------------
 ITEMS STILL ON SHARED DEFAULTS -- change these manually:
------------------------------------------------------------------------------

  Bluetooth PIN:        1337
                          -> web console -> Radio -> "Bluetooth pairing"
                          -> or: systemctl disable --now bluetooth if unused

  Transport:            NO TLS. The API authenticates every request with a
                        bearer token, but that token travels in cleartext.
                          -> reach the device over the USB cable or its own
                             WPA2 access point, not over a shared network.

  This file is mode 0600 and owned by root. After you have copied the
  credentials somewhere safe, shred it:

       shred -u ${CREDS_FILE}

==============================================================================
EOF
chmod 0600 "${CREDS_FILE}"

# --- hand the credentials to the panel, if this device has one --------------
#
# A board with an OLED HAT can do what a headless one cannot: tell you the
# secret itself. The panel shows these once, you confirm you have written them
# down, and then it erases every copy -- this handoff, the file on the FAT
# boot partition, and ${CREDS_FILE}.
#
# That is a real improvement on the status quo, which is a plaintext password
# sitting on a FAT partition for the life of the device, readable by anyone
# who ever holds the card. It is not magic: it narrows the window from
# "forever, to anyone with the card" to "once, to whoever is standing over the
# device at first boot". And an erase on an SD card is not a guarantee --
# wear levelling can leave the old contents in blocks the filesystem can no
# longer reach. The panel says so rather than claiming the data is gone.
#
# Written only when the panel binary is actually installed. On a plain image
# there is no screen to read it from, so an extra copy of the credentials
# would be pure exposure for no benefit.
if [ -x /usr/local/bin/p4wnp1-oled ]; then
    PANEL_HANDOFF="${FLAG_DIR}/firstboot-creds"
    umask 077
    {
        echo "# read once by the OLED panel, then erased. 0600, root."
        echo "web_user=${ADMIN_USER}"
        echo "web_pass=${NEW_ADMIN_PW}"
        # The WiFi access point key.
        #
        # Without this the panel erased more than it displayed: KEY1 shreds
        # ${CREDS_FILE}, and the per-device AP key lives in there. An
        # operator who followed the instructions on screen destroyed a
        # credential they had never been shown.
        #
        # The SSID is NOT sent. It is not a secret -- the device broadcasts
        # it, so anyone can read it by scanning -- and it is frequently
        # emoji, which a 5x7 font renders as a row of question marks. The
        # key is the part that has to reach a human.
        if [ -r /etc/p4wnp1/generated-ap.psk ]; then
            echo "wifi_psk=$(cat /etc/p4wnp1/generated-ap.psk)"
        fi
        if [ -n "${OPERATOR_PW_SET}" ]; then
            echo "ssh_user=${OPERATOR_USER}"
            echo "ssh_pass=${OPERATOR_PW_SET}"
        fi
        # Every copy the panel should destroy on acknowledgement. It erases
        # exactly this list and nothing it worked out for itself.
        [ -f "${BOOT_DIR}/p4wnp1-credentials.txt" ] &&
            echo "erase=${BOOT_DIR}/p4wnp1-credentials.txt"
        echo "erase=${CREDS_FILE}"
    } > "${PANEL_HANDOFF}"
    chmod 0600 "${PANEL_HANDOFF}"
    log "left the first-boot credentials for the panel at ${PANEL_HANDOFF}"
fi

# Also log a short summary to journal so an operator who SSHs in immediately
# sees something useful. The full creds file stays on disk (mode 0600).
log "first-boot security setup complete; see ${CREDS_FILE} for new credentials"
log "WiFi PSK and Bluetooth PIN are STILL at shared defaults -- change via web UI"

touch "${FLAG_FILE}"
chmod 0600 "${FLAG_FILE}"

# DO NOT RESTART P4wnP1 HERE.
#
# This used to run `systemctl restart P4wnP1.service`, to make the service
# re-read the auth.json we just wrote. It was never necessary, and it was
# actively destructive.
#
# Never necessary: the service starts before this script runs, so NewStore
# opens a path with no file behind it -- and load() treats a missing file as
# "no users yet" rather than an error, so the store KEEPS ITS PATH. Its
# reloadIfChanged() then stats the file on every Verify and picks up our
# auth.json the moment we write it. Pinned by
# TestStoreAdoptsAnAuthFileThatAppearsLater in service/auth.
#
# Actively destructive: this is a USB-gadget appliance, and restarting the
# service tears the gadget down. Every service start unbinds the UDC and
# rebuilds an empty, disabled gadget before it reads any template, and a USB
# host cannot re-enumerate a device that has stopped presenting itself. So
# the device dropped off its operator's machine a few minutes into every
# first boot -- cable still plugged in, panel still lit, simply gone -- and
# only a physical replug brought it back. It cost an evening to find,
# because every symptom pointed at USB and the cause was a line about
# passwords.
#
# If this service ever does need a nudge, send it SIGHUP and make the
# service handle it. Do not restart the thing that owns the only link the
# operator has.
log "auth.json written; the service picks it up on its own (no restart)"

exit 0
