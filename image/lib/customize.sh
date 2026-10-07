#!/bin/bash
#
# customize.sh -- runs INSIDE the chroot of the target Raspberry Pi OS rootfs.
#
# Everything here executes under QEMU when cross-building, so keep it to
# package installs and file manipulation: no benchmarks, no long loops.
#
# Reads: P4_ARCH, and the staged payload at /tmp/p4wnp1-payload.
set -euo pipefail

ARCH="${P4_ARCH:?}"
PAYLOAD=/tmp/p4wnp1-payload
P4ROOT=/usr/local/P4wnP1

export DEBIAN_FRONTEND=noninteractive

log()  { printf '\033[1;32m[chroot]\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m[chroot:warn]\033[0m %s\n' "$*"; }
die()  { printf '\033[1;31m[chroot:FATAL]\033[0m %s\n' "$*" >&2; exit 1; }

# ---------------------------------------------------------------------------
# 1. packages
#
# Package names are Debian 13 (trixie) / Raspberry Pi OS 2025+ correct.
# Historical P4wnP1 docs ask for packages that NO LONGER EXIST and that is a
# hard install failure under `set -e`:
#     policykit-1  -> polkitd            (renamed in bookworm)
#     python-pip   -> python3-pip        (python2 removed entirely)
#     python-dev   -> python3-dev
# REQUIRED packages abort the build if missing. OPTIONAL ones only warn, so a
# single upstream rename cannot brick the whole image build.
# ---------------------------------------------------------------------------
REQUIRED_PKGS=(
    hostapd dnsmasq
    iw rfkill wireless-tools
    bluez bluez-tools
    iptables nftables
    python3 python3-pip
    openssl haveged
    usbutils kmod
    ca-certificates
)
OPTIONAL_PKGS=(
    polkitd                 # was policykit-1
    bridge-utils            # legacy brctl; iproute2 is preferred but some paths still shell out
    genisoimage             # UMS CD-ROM image creation
    tcpdump
    screen
    autossh
    iodine
    avahi-daemon
    dosfstools
    net-tools               # ifconfig/route, still referenced by some legacy scripts
)

log "apt-get update"
apt-get update -qq || die "apt-get update failed (no network in the chroot?)"

log "installing ${#REQUIRED_PKGS[@]} required packages"
apt-get install -y -qq --no-install-recommends "${REQUIRED_PKGS[@]}" \
    || die "a REQUIRED package failed to install"

log "installing optional packages (failures are tolerated individually)"
for p in "${OPTIONAL_PKGS[@]}"; do
    if apt-get install -y -qq --no-install-recommends "$p" >/dev/null 2>&1; then
        log "  + $p"
    else
        warn "  ! $p unavailable on this release -- skipped"
    fi
done

# ---------------------------------------------------------------------------
# 2. P4wnP1 payload
# ---------------------------------------------------------------------------
log "installing binaries -> /usr/local/bin"
for b in P4wnP1_service P4wnP1_cli p4wnp1-hashpw; do
    install -m 0755 "$PAYLOAD/bin/$b" "/usr/local/bin/$b"
done

log "installing data -> $P4ROOT"
mkdir -p "$P4ROOT"
# ${P4ROOT:?} rather than $P4ROOT: this is an `rm -rf` running as root inside a
# chroot, and an empty variable would make it `rm -rf /keymaps`. P4ROOT is a
# constant today, but the cost of being wrong here is the whole image.
for d in keymaps scripts HIDScripts www db helper ums legacy; do
    if [ -d "$PAYLOAD/dist/$d" ]; then
        rm -rf "${P4ROOT:?P4ROOT must be set}/$d"
        cp -R "$PAYLOAD/dist/$d" "$P4ROOT/$d"
    fi
done
chmod -R a+rX "$P4ROOT"
find "$P4ROOT/scripts" -name '*.sh' -exec chmod 0755 {} + 2>/dev/null || true

mkdir -p /etc/p4wnp1 /var/lib/p4wnp1
chmod 0700 /etc/p4wnp1

# Optional build overlay (files dropped verbatim into the rootfs).
if [ -d "$PAYLOAD/overlay" ]; then
    log "applying build overlay"
    cp -a "$PAYLOAD/overlay/." /
fi

# ---------------------------------------------------------------------------
# 3. systemd units
#
# `systemctl enable` inside a chroot has no running manager to talk to.
# SYSTEMD_OFFLINE=1 makes systemctl operate purely on the filesystem. If even
# that is unavailable we fall back to writing the wants/ symlink by hand,
# which is all `enable` does for a plain WantedBy= unit.
# ---------------------------------------------------------------------------
log "installing systemd units"
install -m 0644 "$PAYLOAD/dist/P4wnP1.service"           /etc/systemd/system/P4wnP1.service
install -m 0644 "$PAYLOAD/dist/p4wnp1-firstboot.service" /etc/systemd/system/p4wnp1-firstboot.service

enable_unit() {
    local unit="$1" target="${2:-multi-user.target}"
    if SYSTEMD_OFFLINE=1 systemctl enable "$unit" >/dev/null 2>&1; then
        log "  enabled $unit"
    else
        mkdir -p "/etc/systemd/system/${target}.wants"
        ln -sf "/etc/systemd/system/${unit}" "/etc/systemd/system/${target}.wants/${unit}"
        log "  enabled $unit (manual symlink)"
    fi
}
enable_unit P4wnP1.service
enable_unit p4wnp1-firstboot.service

for u in ssh.service haveged.service; do
    SYSTEMD_OFFLINE=1 systemctl enable "$u" >/dev/null 2>&1 && log "  enabled $u" || warn "  could not enable $u"
done

# hostapd and dnsmasq must NOT autostart: P4wnP1 spawns and configures both
# itself with generated config files. A distro-managed instance holding wlan0
# is a classic "the AP never comes up" cause.
for u in hostapd.service dnsmasq.service wpa_supplicant.service; do
    SYSTEMD_OFFLINE=1 systemctl disable "$u" >/dev/null 2>&1 || true
    SYSTEMD_OFFLINE=1 systemctl mask    "$u" >/dev/null 2>&1 && log "  masked $u" || warn "  could not mask $u"
done

# ---------------------------------------------------------------------------
# 4. NetworkManager coexistence
#
# Raspberry Pi OS has used NetworkManager by default since bookworm. P4wnP1
# drives wlan0 / usb0 directly (hostapd, wpa_supplicant, its own addressing),
# so NM must not touch them -- but we deliberately do NOT disable NM wholesale:
# leaving it in charge of eth0 keeps a working "plug in a cable and SSH" path,
# which is the operator's recovery route when USB and WiFi are both wedged.
# ---------------------------------------------------------------------------
log "marking wlan0/usb0 unmanaged in NetworkManager"
mkdir -p /etc/NetworkManager/conf.d
cat > /etc/NetworkManager/conf.d/99-p4wnp1-unmanaged.conf <<'EOF'
# P4wnP1 A.L.O.A. owns these interfaces. NetworkManager must keep its hands
# off them or it will fight hostapd/wpa_supplicant for wlan0 and strip the
# addresses P4wnP1 assigns to the USB gadget interface.
[keyfile]
unmanaged-devices=interface-name:wlan0;interface-name:usb0;interface-name:bnep0;interface-name:p4wnp1-br0
EOF

# ---------------------------------------------------------------------------
# 5. operator SSH account
#
# Raspberry Pi OS no longer ships a default user. Without one, and with the
# root account locked, a flashed image is unreachable. We create a dedicated
# account and FORCE a password change on first login (`chage -d 0`) so the
# documented default cannot survive as a real credential.
# ---------------------------------------------------------------------------
SSH_USER="${P4_SSH_USER:-p4wnp1}"
SSH_PASS="${P4_SSH_PASS:-p4wnp1}"
log "creating operator account '$SSH_USER' (password change forced at first login)"
if ! id -u "$SSH_USER" >/dev/null 2>&1; then
    useradd -m -s /bin/bash -G sudo,dialout,plugdev,video "$SSH_USER"
fi
echo "${SSH_USER}:${SSH_PASS}" | chpasswd
chage -d 0 "$SSH_USER"          # expire immediately -> must set a new password
# Give the operator passwordless sudo: this is a single-purpose appliance and
# every useful P4wnP1 action needs root anyway.
echo "${SSH_USER} ALL=(ALL) NOPASSWD:ALL" > "/etc/sudoers.d/010_${SSH_USER}-nopasswd"
chmod 0440 "/etc/sudoers.d/010_${SSH_USER}-nopasswd"

# ---------------------------------------------------------------------------
# 6. regulatory domain
#
# On current Pi OS wlan0 is rfkill-soft-blocked until a country code is set,
# and hostapd refuses to start without one. This is the single most common
# cause of "the access point never appears".
# ---------------------------------------------------------------------------
log "setting WiFi regulatory domain"
WIFI_COUNTRY="${P4_WIFI_COUNTRY:-US}"
raspi-config nonint do_wifi_country "$WIFI_COUNTRY" >/dev/null 2>&1 \
    && log "  country=$WIFI_COUNTRY via raspi-config" \
    || { printf 'REGDOMAIN=%s\n' "$WIFI_COUNTRY" > /etc/default/crda 2>/dev/null || true
         warn "  raspi-config unavailable; wrote /etc/default/crda fallback"; }
# Belt and braces: unblock wifi at every boot regardless of how the country
# code was stored.
cat > /etc/systemd/system/p4wnp1-rfkill-unblock.service <<'EOF'
[Unit]
Description=P4wnP1 -- unblock WiFi rfkill before the service starts
DefaultDependencies=no
After=systemd-modules-load.service
Before=P4wnP1.service
[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=/usr/sbin/rfkill unblock wifi
ExecStart=/usr/sbin/rfkill unblock bluetooth
[Install]
WantedBy=multi-user.target
EOF
enable_unit p4wnp1-rfkill-unblock.service

# ---------------------------------------------------------------------------
# 7. build stamp
# ---------------------------------------------------------------------------
cat > /etc/p4wnp1/build-info <<EOF
P4WNP1_IMAGE_ARCH=$ARCH
P4WNP1_IMAGE_BASE=$(grep -oP 'PRETTY_NAME="\K[^"]+' /etc/os-release 2>/dev/null || echo unknown)
P4WNP1_IMAGE_KERNELS=$(ls /lib/modules 2>/dev/null | tr '\n' ' ')
EOF
log "build info:"; sed 's/^/    /' /etc/p4wnp1/build-info

# ---------------------------------------------------------------------------
# 8. cleanup
# ---------------------------------------------------------------------------
log "cleaning apt caches"
apt-get clean
rm -rf /var/lib/apt/lists/* /tmp/* /var/tmp/* 2>/dev/null || true
# Remove the SSH host keys baked into the build: firstboot regenerates them so
# every device gets a unique identity. Shipping shared host keys would let
# anyone who downloaded the image MITM every other device.
rm -f /etc/ssh/ssh_host_*
truncate -s 0 /var/log/*.log 2>/dev/null || true

log "customisation complete"
