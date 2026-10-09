#!/bin/bash

# fallback script, called in case the configured 'Startup Master Template' fails
# the script uses the CLI client to do basic configuration and make P4wnP1 reachable
# Additionally it serves as example on how to use the CLI client to configure P4wnP1 A.L.O.A.

# Enable USB functions RNDIS, CDC ECM
P4wnP1_cli usb set --vid 0x1d6c --pid 0x1347 --manufacturer "MaMe82" --sn "deadbeef1337" --product "P4wnP1 by MaMe82" --rndis --cdc-ecm

# Configure USB ethernet interface "usbeth" to run a DHCP server
#   - use IPv4 172.16.0.1 for interface with netmask 255.255.255.252
#   - disable DHCP option 3 (router) by passing an empty value
#   - disable DHCP option 6 (DNS) by passing an empty value
#   - add a DHCP range from 172.16.0.2 to 172.16.0.2 (single IP) with a lease time of 1 minute
P4wnP1_cli net set server -i usbeth -a 172.16.0.1 -m 255.255.255.248 -o "3:" -o "6:" -r "172.16.0.2|172.16.0.2|5m"

# Enable WiFi AP (reg US, channel 6, don't use nexmon firmware)
#
# THE SSID HAS A SIZE BUDGET. Keep it at 15 bytes or fewer.
#
# It was "💥🖥💥 Ⓟ➃ⓌⓃ🅟❶" -- 32 bytes of emoji, which is legal 802.11 but
# does not fit a scan-to-join QR. Such a code is the WIFI: structure plus the
# 20-character key plus the WHOLE SSID, and a panel-sized symbol holds 53
# bytes. Over budget, the OLED first-run card silently drops the join code and
# shows the key to be typed with a thumb instead. Keep this in step with
# dist/db/init.db (see tools/initdb) and P4WNP1_INITIAL_SSID in
# dist/scripts/firstboot-secure-defaults.sh.
#
# The key here is the published upstream default ON PURPOSE: it is a value the
# PSK guard recognises, so the service refuses to broadcast it and substitutes
# a per-device random key instead, persisted at /etc/p4wnp1/generated-ap.psk.
# See service/wifi_psk_guard.go. Giving a key at all is what selects WPA2-PSK;
# omitting it would stand up an OPEN access point.
P4wnP1_cli wifi set ap -r US -c 6 -s "💥P4wnP1" -k "MaMe82-P4wnP1" --nonexmon

# Configure USB ethernet interface "wlan0" to run a DHCP server
#   - use IPv4 172.24.0.1 for interface with netmask 255.255.255.0
#   - disable DHCP option 3 (router) by passing an empty value
#   - disable DHCP option 6 (DNS) by passing an empty value
#   - add a DHCP range from 172.24.0.10 to 172.24.0.20 with a lease time of 5 minutes
P4wnP1_cli net set server -i wlan0 -a 172.24.0.1 -m 255.255.255.0 -o "3:" -o "6:" -r "172.24.0.10|172.24.0.20|5m"

P4wnP1_cli led -b 2
