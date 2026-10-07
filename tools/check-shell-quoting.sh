#!/usr/bin/env bash
#
# check-shell-quoting.sh -- the values install.sh writes into
# /etc/p4wnp1/initial.conf must survive being sourced.
#
# That file is SOURCED AS ROOT by the firstboot helper, which runs under
# `set -euo pipefail` and is what creates the admin account. So a value that
# breaks the file does not produce a cosmetic glitch:
#
#   * An apostrophe -- ordinary in a passphrase -- made the file unparseable,
#     firstboot aborted, no admin account was created, and the device rejected
#     every console request. An apostrophe bricked it.
#   * A crafted value ran arbitrary commands as root on first boot.
#
# This exercises install.sh's OWN quoting function against both, by extracting
# it from the script rather than reimplementing it: a copy here could drift
# from the real one and pass while the real one was broken.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
INSTALL="$REPO_ROOT/install.sh"

[ -r "$INSTALL" ] || { echo "install.sh not found at $INSTALL" >&2; exit 2; }

# Pull the real definition out of install.sh.
SHQUOTE_DEF=$(grep -m1 '^shquote()' "$INSTALL" || true)
if [ -z "$SHQUOTE_DEF" ]; then
    echo "FAIL -- install.sh no longer defines shquote()." >&2
    echo "        If the quoting moved, point this check at the new code;" >&2
    echo "        do not delete the check." >&2
    exit 1
fi
eval "$SHQUOTE_DEF"

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
cd "$WORK"

PASS=0; FAIL=0
try() {
    local label="$1" ssid="$2" psk="$3"
    rm -f executed
    {
        echo "P4WNP1_INITIAL_SSID=$(shquote "$ssid")"
        echo "P4WNP1_INITIAL_PSK=$(shquote "$psk")"
    } > conf

    unset P4WNP1_INITIAL_SSID P4WNP1_INITIAL_PSK

    if ! bash -n conf 2>/dev/null; then
        printf '  \033[1;31mFAIL\033[0m  %-28s produced unparseable shell -- firstboot would abort\n' "$label"
        FAIL=$((FAIL+1)); return
    fi
    # shellcheck source=/dev/null
    . ./conf
    if [ -f executed ]; then
        printf '  \033[1;31mFAIL\033[0m  %-28s EXECUTED A COMMAND -- this is root RCE at first boot\n' "$label"
        FAIL=$((FAIL+1)); return
    fi
    if [ "${P4WNP1_INITIAL_SSID-}" != "$ssid" ] || [ "${P4WNP1_INITIAL_PSK-}" != "$psk" ]; then
        printf '  \033[1;31mFAIL\033[0m  %-28s value changed in the round trip\n' "$label"
        FAIL=$((FAIL+1)); return
    fi
    printf '  \033[1;32mPASS\033[0m  %-28s round-tripped exactly\n' "$label"
    PASS=$((PASS+1))
}

echo "==> install.sh writes values that survive being sourced"
try "plain"                 "HackProKP"                "a-plain-passphrase"
try "apostrophes"           "Bob's AP"                 "pa'ss'word"
try "injection attempt"     "x'; touch executed; #"    "pw-for-injection"
try "double quotes"         'say "hello"'              'pw"with"quotes'
try "backslashes"           'back\slash'               'a\b\c'
try "command substitution"  '$(touch executed)'        '`touch executed`'
try "semicolons and pipes"  'a; b | c && d'            'e; f'
try "whitespace"            'spaces   and	tabs'       'trailing   '
try "non-ASCII"             'üñïçødé AP 💥'            'ünïcødé-psk'
try "leading dash"          '-not-a-flag'              '--also-not'

printf '\n  ---- %d passed, %d failed ----\n\n' "$PASS" "$FAIL"
[ "$FAIL" -eq 0 ]
