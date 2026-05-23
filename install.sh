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
#   * Compile from source. You need build/P4wnP1_service, build/P4wnP1_cli,
#     build/webapp.js, build/webapp.js.map present in the repo. Build them
#     with build_support/build.sh on a Linux host first.
#   * Flash the Nexmon-patched WiFi firmware. KARMA + multi-SSID need a
#     separately-built firmware blob from the Nexmon project.

set -euo pipefail

# ---------------------------------------------------------------------------
# Defaults (override via CLI flags)
# ---------------------------------------------------------------------------
DEFAULT_SSID="HackProKP"
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
    "${REPO_ROOT}/build/webapp.js"
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
    apt-get install -y --no-install-recommends \
        git screen hostapd autossh bluez bluez-tools bridge-utils \
        policykit-1 genisoimage iodine haveged dnsmasq dhcpcd5 \
        avahi-daemon dosfstools wpasupplicant tcpdump \
        python3-pip python3-dev python3-configobj python3-requests \
        i2c-tools openssh-server
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
cat > /etc/p4wnp1/initial.conf <<EOF
# Read by /usr/local/P4wnP1/scripts/firstboot-secure-defaults.sh on first boot.
# Edit before first boot to customise; delete /var/lib/p4wnp1/firstboot.done
# to force the helper to re-run.
P4WNP1_INITIAL_SSID='${SSID}'
P4WNP1_INITIAL_PSK='${PSK}'
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
