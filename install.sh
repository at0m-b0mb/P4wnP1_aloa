#!/bin/bash
#
# install.sh -- Easy installer for P4wnP1 A.L.O.A. on Raspberry Pi OS Lite.
#
# Tested baseline: Raspberry Pi OS Lite (Bookworm, 32-bit/armhf) on a
# Raspberry Pi Zero W (BCM43430A1).
#
# Usage (on the Pi, with internet reachable via WiFi or USB OTG ethernet):
#
#     sudo ./install.sh                       # use defaults (HackProKP / random PSK)
#     sudo ./install.sh --ssid MyAP --psk 'verysecret123!'
#     sudo ./install.sh --skip-reboot         # don't auto-reboot at the end
#
# The script is idempotent: re-running it re-applies missing pieces but
# doesn't double-install systemd units or overwrite a credentials file
# that already exists.
#
# What it does:
#   1. Verifies it's running on Pi OS / Debian-derived, with apt available
#   2. Apt-installs all P4wnP1 dependencies
#   3. Copies binaries + data from this repo into /usr/local/...
#   4. Installs the two systemd units (P4wnP1.service + p4wnp1-firstboot.service)
#   5. Disables conflicting network services (NetworkManager/networking)
#   6. Sets branding (default SSID + initial random PSK) in /etc/p4wnp1/initial.conf
#   7. Enables services so the next boot brings P4wnP1 up
#
# What it does NOT do:
#   * Compile from source. You need build/P4wnP1_service, build/P4wnP1_cli
#     and build/p4wnp1-hashpw present in the repo. Build them with
#     `make build-armv6` (Pi Zero/Zero W) or `make build-arm64`
#     (Pi Zero 2 W / 3 / 4 / 5) -- both cross-compile fine from macOS.
#   * Flash the Nexmon-patched WiFi firmware. KARMA + multi-SSID need a
#     separately-built firmware blob from the Nexmon project.

set -euo pipefail

# ---------------------------------------------------------------------------
# Defaults (override via CLI flags)
# ---------------------------------------------------------------------------
DEFAULT_SSID="HackProKP"
WIFI_COUNTRY="${WIFI_COUNTRY:-US}"   # regulatory domain; hostapd needs one
DEFAULT_PSK=""                       # empty -> generate random
SKIP_REBOOT=0
SKIP_APT=0
REPO_ROOT="$(cd "$(dirname "$0")" && pwd)"

# ---------------------------------------------------------------------------
# Arg parsing
# ---------------------------------------------------------------------------
SSID="${DEFAULT_SSID}"
PSK="${DEFAULT_PSK}"

usage() {
    cat <<EOF
Usage: sudo $0 [options]

Options:
    --ssid NAME           WiFi SSID broadcast by the P4wnP1 AP. Default: HackProKP.
                          Avoid emoji/unicode -- crashes NetworkManager on Linux.
    --psk PASSPHRASE      WiFi PSK (8-63 ASCII chars). If omitted, a 16-char
                          random PSK is generated and written to /root/INITIAL_CREDENTIALS.txt
    --skip-reboot         Don't reboot at the end.
    --skip-apt            Don't install apt packages (assume they're present).
    -h, --help            Show this help.
EOF
}

while [[ $# -gt 0 ]]; do
    case "$1" in
        --ssid)        SSID="$2"; shift 2 ;;
        --psk)         PSK="$2"; shift 2 ;;
        --skip-reboot) SKIP_REBOOT=1; shift ;;
        --skip-apt)    SKIP_APT=1; shift ;;
        --wifi-country) WIFI_COUNTRY="$2"; shift 2 ;;
        -h|--help)     usage; exit 0 ;;
        *)             echo "unknown option: $1" >&2; usage >&2; exit 2 ;;
    esac
done

# ---------------------------------------------------------------------------
# Sanity checks
# ---------------------------------------------------------------------------
if [[ $EUID -ne 0 ]]; then
    echo "error: must run as root (sudo ./install.sh)" >&2
    exit 1
fi

if ! command -v apt-get >/dev/null 2>&1; then
    echo "error: apt-get not found. This installer targets Debian-derived" >&2
    echo "       distros (Raspberry Pi OS, Kali, Debian, Ubuntu). For other" >&2
    echo "       systems, see INSTALL.md path C." >&2
    exit 1
fi

# Validate PSK if provided
if [[ -n "${PSK}" ]]; then
    psk_len=${#PSK}
    if (( psk_len < 8 || psk_len > 63 )); then
        echo "error: --psk must be 8-63 ASCII characters (got ${psk_len})" >&2
        exit 1
    fi
fi

# Hardware probe (best-effort; non-fatal)
if [[ -r /proc/device-tree/model ]]; then
    model=$(tr -d '\0' </proc/device-tree/model)
    echo "info: detected hardware: ${model}"
    case "${model}" in
        *"Zero W"*)            ;;
        *"Zero 2 W"*)          echo "warning: Pi Zero 2 W is NOT officially supported. The Nexmon WiFi firmware patches are specific to the BCM43430A1 chip on the original Zero W. USB gadget functions will work; WiFi KARMA + multi-SSID will not." ;;
        *)                     echo "warning: This installer is designed for Pi Zero W. Other models may work but are untested." ;;
    esac
fi

# ---------------------------------------------------------------------------
# Required source artefacts (must exist in the repo)
# ---------------------------------------------------------------------------
required_files=(
    "${REPO_ROOT}/build/P4wnP1_service"
    "${REPO_ROOT}/build/P4wnP1_cli"
    "${REPO_ROOT}/build/p4wnp1-hashpw"
    "${REPO_ROOT}/dist/P4wnP1.service"
    "${REPO_ROOT}/dist/p4wnp1-firstboot.service"
    "${REPO_ROOT}/dist/scripts/firstboot-secure-defaults.sh"
)

missing=()
for f in "${required_files[@]}"; do
    [[ -e "${f}" ]] || missing+=("${f}")
done
if (( ${#missing[@]} > 0 )); then
    echo "error: the following required files are missing:" >&2
    printf '  %s\n' "${missing[@]}" >&2
    echo >&2
    echo "Run build_support/build.sh on a Linux host to produce the binaries" >&2
    echo "and webapp.js, then re-run this installer." >&2
    exit 1
fi

# ---------------------------------------------------------------------------
# 1. APT dependencies
# ---------------------------------------------------------------------------
if (( SKIP_APT == 0 )); then
    echo "==> installing apt dependencies"
    export DEBIAN_FRONTEND=noninteractive
    apt-get update -qq
    # Packages are split in two on purpose. This script runs under `set -e`,
    # so a SINGLE unavailable package in one apt-get invocation aborts the
    # whole install -- and package names do get retired: `policykit-1` was
    # dropped in Debian 12 (it is `polkitd` now), and current Raspberry Pi OS
    # is Debian 13. Required packages are still fatal; optional ones are tried
    # one at a time and only warn.
    REQUIRED_PKGS=(
        hostapd dnsmasq
        iw rfkill wireless-tools
        bluez bluez-tools
        openssl haveged
        usbutils kmod
        python3 python3-pip
        openssh-server ca-certificates
    )
    OPTIONAL_PKGS=(
        polkitd policykit-1          # renamed in Debian 12; try both
        bridge-utils genisoimage
        git screen autossh iodine tcpdump
        avahi-daemon dosfstools i2c-tools
        wpasupplicant dhcpcd5
        python3-dev python3-configobj python3-requests
        net-tools nftables iptables
    )

    echo "==> installing required packages"
    apt-get install -y --no-install-recommends "${REQUIRED_PKGS[@]}"

    echo "==> installing optional packages (individually; failures only warn)"
    for p in "${OPTIONAL_PKGS[@]}"; do
        if apt-get install -y --no-install-recommends "${p}" >/dev/null 2>&1; then
            echo "    + ${p}"
        else
            echo "    ! ${p} unavailable on this release -- skipped"
        fi
    done
    apt-get install -y --no-install-recommends pydispatcher 2>/dev/null || \
        pip3 install --break-system-packages pydispatcher || \
        echo "warning: pydispatcher unavailable; legacy HID backdoor scripts may not run"
else
    echo "==> skipping apt install (--skip-apt)"
fi

# ---------------------------------------------------------------------------
# 2. Generate / select credentials
# ---------------------------------------------------------------------------
gen_password() {
    local length="${1:-16}"
    if command -v openssl >/dev/null 2>&1; then
        openssl rand -base64 32 | tr -d '/+=\n' | head -c "${length}"
    else
        LC_ALL=C tr -dc 'A-Za-z0-9' </dev/urandom | head -c "${length}"
    fi
}

if [[ -z "${PSK}" ]]; then
    PSK=$(gen_password 16)
    PSK_SOURCE="auto-generated"
else
    PSK_SOURCE="user-supplied"
fi

# ---------------------------------------------------------------------------
# 3. Install binaries + data files
# ---------------------------------------------------------------------------
echo "==> installing binaries to /usr/local/bin"
install -m 0755 "${REPO_ROOT}/build/P4wnP1_service" /usr/local/bin/
install -m 0755 "${REPO_ROOT}/build/P4wnP1_cli"     /usr/local/bin/
install -m 0755 "${REPO_ROOT}/build/p4wnp1-hashpw"  /usr/local/bin/

echo "==> installing data files to /usr/local/P4wnP1"
mkdir -p /usr/local/P4wnP1
for d in keymaps scripts HIDScripts www db helper ums legacy; do
    if [[ -d "${REPO_ROOT}/dist/${d}" ]]; then
        cp -R "${REPO_ROOT}/dist/${d}" /usr/local/P4wnP1/
    fi
done
install -m 0644 "${REPO_ROOT}/build/webapp.js"     /usr/local/P4wnP1/www/
[[ -f "${REPO_ROOT}/build/webapp.js.map" ]] && \
    install -m 0644 "${REPO_ROOT}/build/webapp.js.map" /usr/local/P4wnP1/www/
chmod 0755 /usr/local/P4wnP1/scripts/firstboot-secure-defaults.sh
chmod 0755 /usr/local/P4wnP1/scripts/p4wnp1-healthcheck.sh 2>/dev/null || true

# ---------------------------------------------------------------------------
# 4. Write the branding/initial config that the firstboot helper reads
# ---------------------------------------------------------------------------
echo "==> writing initial config to /etc/p4wnp1/initial.conf"
mkdir -p /etc/p4wnp1
umask 077
# Emit a value as a shell literal that survives being sourced.
#
# These used to be written as '${SSID}' -- wrapped in single quotes with no
# escaping -- and the firstboot helper SOURCES this file as root. An SSID or
# passphrase containing an apostrophe, which is an entirely ordinary thing for
# a passphrase to contain, closed the quote early:
#
#     P4WNP1_INITIAL_SSID='Bob's AP'
#
# firstboot runs under `set -euo pipefail`, so sourcing that aborts it. The
# helper is what creates the admin account, so the device came up with NO
# account, rejecting every console request. An apostrophe bricked it.
#
# A deliberately crafted value was worse: everything after the closing quote
# ran as root on first boot.
#
# The replacement below is the standard way to emit a single-quoted shell
# literal: end the quote, emit an escaped apostrophe, reopen the quote.
# printf %q is a bash builtin whose whole job is this: emit a string as a
# shell literal that reads back as the identical string. Hand-rolling the
# single-quote escaping is possible but easy to get subtly wrong -- the first
# attempt at it here was wrong, and an apostrophe still broke the file.
#
# The output is not always single-quoted (printf picks the form it needs, and
# uses $'...' for control characters), but it is always valid shell and always
# round-trips, which is the property that matters.
shquote() { printf '%q' "$1"; }

cat > /etc/p4wnp1/initial.conf <<EOF
# Read by /usr/local/P4wnP1/scripts/firstboot-secure-defaults.sh on first boot.
# Edit before first boot to customise; delete /var/lib/p4wnp1/firstboot.done
# to force the helper to re-run.
#
# Values are shell-quoted. If you edit them by hand, keep them quoted -- this
# file is sourced by the firstboot helper.
P4WNP1_INITIAL_SSID=$(shquote "${SSID}")
P4WNP1_INITIAL_PSK=$(shquote "${PSK}")
EOF
chmod 0600 /etc/p4wnp1/initial.conf
umask 022

# ---------------------------------------------------------------------------
# 5. Install systemd units
# ---------------------------------------------------------------------------
echo "==> installing systemd units"
install -m 0644 "${REPO_ROOT}/dist/P4wnP1.service"           /etc/systemd/system/
install -m 0644 "${REPO_ROOT}/dist/p4wnp1-firstboot.service" /etc/systemd/system/

# ---------------------------------------------------------------------------
# 5b. Boot configuration: USB gadget mode and the WiFi regulatory domain
#
# This section is what makes the USB side of P4wnP1 work AT ALL, and earlier
# versions of this installer omitted it entirely. Without `dtoverlay=dwc2` the
# Pi's USB controller stays in host mode, there is no UDC for the gadget
# subsystem to bind to, and every USB function -- HID keyboard and mouse,
# ethernet, mass storage, serial -- silently does nothing. The service starts,
# the web UI loads, and the whole point of the device is dead.
#
# The boot partition moved in Debian 12: /boot/firmware on bookworm and later,
# /boot before that. Detect rather than assume, because writing to the wrong
# one fails silently -- the file is created, nothing ever reads it.
# ---------------------------------------------------------------------------
echo "==> configuring boot for USB gadget mode"

if [[ -f /boot/firmware/config.txt ]]; then
    BOOT_DIR=/boot/firmware                 # bookworm (Debian 12) and later
elif [[ -f /boot/config.txt ]]; then
    BOOT_DIR=/boot                          # bullseye and earlier
else
    BOOT_DIR=""
fi

if [[ -z "${BOOT_DIR}" ]]; then
    echo "    WARNING: no config.txt found in /boot/firmware or /boot."
    echo "             This does not look like Raspberry Pi OS. USB gadget mode"
    echo "             has NOT been configured and no USB function will work."
    echo "             Add 'dtoverlay=dwc2' to your config.txt and"
    echo "             'modules-load=dwc2' to your kernel command line by hand."
else
    echo "    boot partition: ${BOOT_DIR}"
    CFG="${BOOT_DIR}/config.txt"
    CMD="${BOOT_DIR}/cmdline.txt"

    if grep -q '^# --- P4wnP1' "${CFG}" 2>/dev/null; then
        echo "    config.txt already carries a P4wnP1 block; leaving it alone"
    else
        cp -a "${CFG}" "${CFG}.p4wnp1-backup.$(date +%s)" 2>/dev/null || true
        cat >> "${CFG}" <<'BOOTCFG'

# --- P4wnP1 A.L.O.A. -------------------------------------------------------
# dwc2 in peripheral mode is what lets the Pi present itself to a host as a USB
# device. Without it the USB gadget subsystem has no UDC to bind to.
[all]
dtoverlay=dwc2,dr_mode=peripheral
enable_uart=1
# --- end P4wnP1 ------------------------------------------------------------
BOOTCFG
        echo "    appended dwc2 overlay to config.txt (backup kept alongside)"
    fi

    if [[ -f "${CMD}" ]]; then
        if grep -q 'modules-load=dwc2' "${CMD}"; then
            echo "    cmdline.txt already loads dwc2"
        else
            cp -a "${CMD}" "${CMD}.p4wnp1-backup.$(date +%s)" 2>/dev/null || true
            # cmdline.txt MUST remain a single line; appending a newline here
            # makes the Pi ignore everything after it, including root=.
            tr -d '\n' < "${CMD}" > "${CMD}.p4wnp1-new"
            printf ' modules-load=dwc2\n' >> "${CMD}.p4wnp1-new"
            mv "${CMD}.p4wnp1-new" "${CMD}"
            echo "    added modules-load=dwc2 to the kernel command line"
        fi
    else
        echo "    WARNING: ${CMD} not found; dwc2 will not be loaded at boot"
    fi
fi

# The WiFi regulatory domain is not optional on current Pi OS: wlan0 stays
# rfkill-soft-blocked until a country is set, and hostapd refuses to start
# without one. "The access point never appears" is almost always this.
echo "==> setting WiFi regulatory domain (${WIFI_COUNTRY})"
if command -v raspi-config >/dev/null 2>&1; then
    raspi-config nonint do_wifi_country "${WIFI_COUNTRY}" >/dev/null 2>&1 \
        && echo "    country set to ${WIFI_COUNTRY}" \
        || echo "    WARNING: raspi-config could not set the country code"
else
    echo "    WARNING: raspi-config not present; set the WiFi country yourself"
    echo "             or hostapd may refuse to start."
fi
rfkill unblock wifi      2>/dev/null || true
rfkill unblock bluetooth 2>/dev/null || true

# ---------------------------------------------------------------------------
# 6. Disable conflicting network services
# ---------------------------------------------------------------------------
echo "==> disabling conflicting network services"
# Pi OS Lite default is dhcpcd; we keep dhcpcd5 as a binary but disable the
# stock service since P4wnP1 wraps it. NetworkManager on Bookworm Lite is
# usually absent but check anyway.
for svc in networking NetworkManager; do
    if systemctl list-unit-files "${svc}.service" >/dev/null 2>&1; then
        systemctl disable "${svc}.service" 2>/dev/null || true
    fi
done

# ---------------------------------------------------------------------------
# 7. Enable services
# ---------------------------------------------------------------------------
echo "==> enabling P4wnP1 services"
systemctl daemon-reload
systemctl enable haveged.service       2>/dev/null || true
systemctl enable avahi-daemon.service  2>/dev/null || true
systemctl enable ssh.service           2>/dev/null || true
systemctl enable P4wnP1.service
systemctl enable p4wnp1-firstboot.service

# ---------------------------------------------------------------------------
# Done
# ---------------------------------------------------------------------------
echo
echo "================================================================="
echo " P4wnP1 A.L.O.A. installed."
echo "================================================================="
echo
echo "  WiFi SSID:   ${SSID}"
echo "  WiFi PSK:    ${PSK}   (${PSK_SOURCE})"
echo "  P4wnP1 IPs:  172.24.0.1 (WiFi) / 172.16.0.1 (USB) / 172.26.0.1 (BT)"
echo "  Web client:  http://172.24.0.1:8000 after reboot"
echo
echo "  Initial SSH credentials will be generated on first boot by"
echo "  p4wnp1-firstboot.service and written to:"
echo
echo "      /root/INITIAL_CREDENTIALS.txt   (mode 0600)"
echo
echo "  Connect via USB ethernet (172.16.0.1) or WiFi AP for first login,"
echo "  then run: cat /root/INITIAL_CREDENTIALS.txt"
echo
echo "================================================================="

if (( SKIP_REBOOT == 0 )); then
    echo "  Rebooting in 10 seconds. Ctrl-C to abort."
    echo "================================================================="
    sleep 10
    systemctl reboot
else
    echo "  --skip-reboot was set; reboot manually with: sudo reboot"
    echo "================================================================="
fi
