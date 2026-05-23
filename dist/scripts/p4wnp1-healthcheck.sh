#!/bin/bash
#
# p4wnp1-healthcheck.sh -- post-install smoke test for P4wnP1 A.L.O.A.
#
# Run on the Pi via SSH after install.sh + first boot. Verifies that every
# critical piece is in place and prints a one-line PASS/FAIL per check, plus
# a final summary.
#
# Usage:
#     sudo ./p4wnp1-healthcheck.sh
#     sudo ./p4wnp1-healthcheck.sh --login-test  # also try a real /api/auth/login round-trip
#
# Exit codes:
#     0  -- all checks passed
#     1  -- one or more checks failed (see output)
#     2  -- usage / preflight error
#

set -u   # no -e: we want every check to run even if earlier ones fail
LOGIN_TEST=0
for arg in "$@"; do
    case "$arg" in
        --login-test) LOGIN_TEST=1 ;;
        -h|--help)
            sed -n 's/^# \?//;3,17p' "$0"
            exit 0
            ;;
        *) echo "unknown option: $arg" >&2; exit 2 ;;
    esac
done

if [[ $EUID -ne 0 ]]; then
    echo "error: must run as root" >&2
    exit 2
fi

# Color helpers (no colors if NO_COLOR set or stdout isn't a tty).
if [[ -t 1 && -z "${NO_COLOR:-}" ]]; then
    G=$'\033[32m'; R=$'\033[31m'; Y=$'\033[33m'; B=$'\033[1m'; N=$'\033[0m'
else
    G='' R='' Y='' B='' N=''
fi

PASS=0
FAIL=0
WARN=0
say_pass() { echo "  ${G}✓${N} $*"; PASS=$((PASS+1)); }
say_fail() { echo "  ${R}✗${N} $*"; FAIL=$((FAIL+1)); }
say_warn() { echo "  ${Y}!${N} $*"; WARN=$((WARN+1)); }
section()  { echo; echo "${B}== $* ==${N}"; }

# ---------------------------------------------------------------------------
section "1. Binaries"
# ---------------------------------------------------------------------------
for bin in /usr/local/bin/P4wnP1_service /usr/local/bin/P4wnP1_cli /usr/local/bin/p4wnp1-hashpw; do
    if [[ -x "$bin" ]]; then say_pass "$bin (executable)"; else say_fail "$bin (missing or not +x)"; fi
done

# ---------------------------------------------------------------------------
section "2. Data files"
# ---------------------------------------------------------------------------
for d in /usr/local/P4wnP1/scripts /usr/local/P4wnP1/keymaps /usr/local/P4wnP1/www /usr/local/P4wnP1/db; do
    if [[ -d "$d" ]]; then say_pass "$d/"; else say_fail "$d/ (missing)"; fi
done
if [[ -f /usr/local/P4wnP1/www/webapp.js ]]; then say_pass "webapp.js"; else say_fail "webapp.js (missing)"; fi
if [[ -x /usr/local/P4wnP1/scripts/firstboot-secure-defaults.sh ]]; then
    say_pass "firstboot-secure-defaults.sh (+x)"
else
    say_fail "firstboot-secure-defaults.sh (missing or not +x)"
fi

# ---------------------------------------------------------------------------
section "3. systemd units"
# ---------------------------------------------------------------------------
for unit in P4wnP1.service p4wnp1-firstboot.service; do
    if [[ -f "/etc/systemd/system/$unit" ]]; then
        say_pass "$unit (installed)"
    else
        say_fail "$unit (not in /etc/systemd/system/)"
        continue
    fi
    if systemctl is-enabled --quiet "$unit"; then
        say_pass "$unit (enabled)"
    else
        say_warn "$unit (not enabled)"
    fi
done
if systemctl is-active --quiet P4wnP1.service; then
    say_pass "P4wnP1.service (running)"
else
    say_fail "P4wnP1.service (not running): journalctl -u P4wnP1.service -n 30"
fi

# ---------------------------------------------------------------------------
section "4. First-boot credential rotation"
# ---------------------------------------------------------------------------
if [[ -f /var/lib/p4wnp1/firstboot.done ]]; then
    say_pass "firstboot.done flag present (firstboot helper ran)"
else
    say_fail "firstboot.done flag missing -- firstboot helper hasn't run yet?"
fi
if [[ -f /root/INITIAL_CREDENTIALS.txt ]]; then
    mode=$(stat -c '%a' /root/INITIAL_CREDENTIALS.txt 2>/dev/null || stat -f '%Lp' /root/INITIAL_CREDENTIALS.txt)
    if [[ "$mode" = "600" ]]; then
        say_pass "/root/INITIAL_CREDENTIALS.txt (mode 0600)"
    else
        say_fail "/root/INITIAL_CREDENTIALS.txt mode is $mode, expected 600"
    fi
else
    say_warn "/root/INITIAL_CREDENTIALS.txt not present (already shredded? or firstboot didn't run)"
fi

# ---------------------------------------------------------------------------
section "5. Auth store"
# ---------------------------------------------------------------------------
AUTH_FILE=/etc/p4wnp1/auth.json
if [[ -f "$AUTH_FILE" ]]; then
    mode=$(stat -c '%a' "$AUTH_FILE" 2>/dev/null || stat -f '%Lp' "$AUTH_FILE")
    if [[ "$mode" = "600" ]]; then
        say_pass "$AUTH_FILE (mode 0600)"
    else
        say_fail "$AUTH_FILE mode is $mode, expected 600"
    fi
    if grep -q '"username"' "$AUTH_FILE" 2>/dev/null; then
        say_pass "$AUTH_FILE contains at least one user"
    else
        say_fail "$AUTH_FILE has no users -- web client will reject all logins"
    fi
    # shellcheck disable=SC2016  # the $2 below is a regex literal, not a shell var
    if grep -q '"password_hash"' "$AUTH_FILE" 2>/dev/null && grep -q '\$2[aby]\$' "$AUTH_FILE" 2>/dev/null; then
        say_pass 'password_hash looks like bcrypt ($2a$/$2b$/$2y$)'
    else
        say_fail "password_hash missing or not bcrypt-formatted"
    fi
else
    say_fail "$AUTH_FILE missing -- web client login will fail"
fi

# ---------------------------------------------------------------------------
section "6. Network listeners"
# ---------------------------------------------------------------------------
check_port() {
    local proto="$1" port="$2" label="$3"
    if command -v ss >/dev/null 2>&1; then
        if ss -lnt "sport = :$port" 2>/dev/null | grep -q ":$port "; then
            say_pass "$label ($proto/$port listening)"
            return
        fi
    fi
    # Fallback: try a connect
    if (echo > "/dev/tcp/127.0.0.1/$port") 2>/dev/null; then
        say_pass "$label ($proto/$port responsive)"
    else
        say_fail "$label ($proto/$port not listening)"
    fi
}
check_port tcp 50051 "gRPC API"
check_port tcp 8000  "gRPC-web + HTTP"
check_port tcp 22    "SSH"

# ---------------------------------------------------------------------------
section "7. HTTP auth endpoint smoke test"
# ---------------------------------------------------------------------------
if command -v curl >/dev/null 2>&1; then
    health_response=$(curl -s -o /tmp/p4wnp1-health-$$ -w '%{http_code}' \
        http://127.0.0.1:8000/api/auth/health 2>/dev/null)
    if [[ "$health_response" = "200" ]]; then
        say_pass "GET /api/auth/health -> 200"
        if grep -q '"status":"ok"' /tmp/p4wnp1-health-$$; then
            say_pass "/api/auth/health body looks healthy"
        else
            say_warn "/api/auth/health body unexpected: $(cat /tmp/p4wnp1-health-$$)"
        fi
    else
        say_fail "GET /api/auth/health -> HTTP $health_response"
    fi
    rm -f /tmp/p4wnp1-health-$$

    # Negative test: a gRPC-web call without an Authorization header must
    # be rejected (proves the interceptor is wired).
    grpc_unauth=$(curl -s -o /dev/null -w '%{http_code}' -X POST \
        -H 'Content-Type: application/grpc-web+proto' \
        http://127.0.0.1:8000/P4wnP1.P4WNP1/GetAvailableGpios 2>/dev/null)
    if [[ "$grpc_unauth" = "200" ]]; then
        # gRPC-web returns 200 with a trailer carrying the gRPC status, so
        # we can't reliably tell "unauth" from "ok" via HTTP code alone.
        # Skip this check.
        say_warn "unauth gRPC-web returned 200 (status is in the trailer; manual verify recommended)"
    elif [[ "$grpc_unauth" = "401" || "$grpc_unauth" = "403" || "$grpc_unauth" = "000" ]]; then
        say_pass "unauth gRPC-web rejected (HTTP $grpc_unauth)"
    else
        say_warn "unauth gRPC-web returned HTTP $grpc_unauth (unexpected)"
    fi
else
    say_warn "curl not installed -- skipping HTTP smoke tests"
fi

# ---------------------------------------------------------------------------
section "8. WiFi access point"
# ---------------------------------------------------------------------------
if command -v iw >/dev/null 2>&1; then
    iface=$(iw dev 2>/dev/null | awk '/Interface/ {print $2; exit}')
    if [[ -n "$iface" ]]; then
        ap_type=$(iw dev "$iface" info 2>/dev/null | awk '/type/ {print $2}')
        if [[ "$ap_type" = "AP" ]]; then
            ssid=$(iw dev "$iface" info 2>/dev/null | awk '/ssid/ {print $2}')
            say_pass "WiFi $iface is in AP mode (SSID: ${ssid:-unknown})"
        else
            say_warn "WiFi $iface is in '$ap_type' mode, not AP"
        fi
    else
        say_warn "no WiFi interface found via 'iw dev' (no onboard wlan?)"
    fi
else
    say_warn "iw not installed -- skipping WiFi check"
fi

# ---------------------------------------------------------------------------
section "9. Optional: real login round-trip"
# ---------------------------------------------------------------------------
if (( LOGIN_TEST == 1 )); then
    if [[ ! -f /root/INITIAL_CREDENTIALS.txt ]]; then
        say_warn "no /root/INITIAL_CREDENTIALS.txt -- can't extract admin creds; skipping"
    else
        admin_pw=$(awk -F': +' '/Web client admin password/ {print $2}' /root/INITIAL_CREDENTIALS.txt | tr -d ' ')
        if [[ -z "$admin_pw" ]]; then
            say_warn "couldn't parse admin password from INITIAL_CREDENTIALS.txt"
        else
            login_response=$(curl -s -o /tmp/p4wnp1-login-$$ -w '%{http_code}' \
                -H 'Content-Type: application/json' \
                -X POST \
                -d "{\"username\":\"admin\",\"password\":\"${admin_pw}\"}" \
                http://127.0.0.1:8000/api/auth/login 2>/dev/null)
            if [[ "$login_response" = "200" ]] && grep -q '"token"' /tmp/p4wnp1-login-$$; then
                say_pass "login round-trip succeeded; got a token"
            else
                say_fail "login round-trip failed (HTTP $login_response): $(cat /tmp/p4wnp1-login-$$)"
            fi
            rm -f /tmp/p4wnp1-login-$$
        fi
    fi
fi

# ---------------------------------------------------------------------------
echo
echo "${B}Summary:${N} ${G}${PASS} pass${N} / ${Y}${WARN} warn${N} / ${R}${FAIL} fail${N}"
if (( FAIL > 0 )); then
    echo "Run 'journalctl -u P4wnP1.service -n 100 --no-pager' for service logs."
    exit 1
fi
exit 0
