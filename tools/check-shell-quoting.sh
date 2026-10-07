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

# --- shipped scripts must not hand secrets to the kernel or to /tmp ---------
#
# Everything on a process's command line is in /proc/<pid>/cmdline, which is
# world-readable. This device ships an unprivileged SSH account, so "only the
# operator can see it" is not true here. The healthcheck was passing the admin
# password to curl as an argument.
#
# And a predictable path in a world-writable directory is three bugs at once:
# the file lands 0644 by default (the login response holds a BEARER TOKEN), a
# local user can pre-create it as a symlink and have root write through it, and
# cleanup on the success path only means failures leave it behind.
echo
echo "==> shipped scripts keep secrets off command lines and out of /tmp"

SCRIPTS=$(find "$REPO_ROOT/dist/scripts" "$REPO_ROOT/install.sh" -name '*.sh' -o -name 'install.sh' 2>/dev/null | sort -u)

# Deliberately NOT a quote-aware regex. The first version of this check tried
# to match the quoted payload after -d and stopped at the first escaped quote,
# so it missed the exact line it was written for -- the healthcheck's
# -d "{\"username\":...\"password\":..." -- and reported a clean pass. Match
# the flag and the secret word on the same line and let "@-" (read from stdin)
# be the way to say "this one is fine".
# Join backslash continuations first, so a curl spread over five lines is
# examined as the one command it is, then require all three of: it is a curl,
# it carries a data flag, and the payload names a secret. Matching line by line
# produced a false positive on `tr -d ' '` -- which is also a "-d" -- and the
# resulting red herring is exactly the kind of noise that gets a check deleted.
secret_cmdline_hits() {
    awk '
        { line = line $0 }
        /\\$/ { sub(/\\$/, " ", line); next }
        {
            if (line ~ /curl/ &&
                line ~ /(^|[[:space:]])(-d|--data|--data-binary|--data-raw)[[:space:]]/ &&
                tolower(line) ~ /password|psk|token|secret/ &&
                line !~ /@-/)
                print NR ": " line
            line = ""
        }
    ' "$1"
}

secret_on_cmdline=0
for f in $SCRIPTS; do
    if hits=$(secret_cmdline_hits "$f") && [ -n "$hits" ]; then
        printf '  \033[1;31mFAIL\033[0m  %-28s passes a secret as a command-line argument\n' "$(basename "$f")"
        printf '%s\n' "$hits" | sed 's/^/          /'
        secret_on_cmdline=1
    fi
done
if [ "$secret_on_cmdline" -eq 0 ]; then
    printf '  \033[1;32mPASS\033[0m  %-28s no secret is passed as an argument\n' "all scripts"
    PASS=$((PASS+1))
else
    FAIL=$((FAIL+1))
fi

predictable_tmp=0
for f in $SCRIPTS; do
    # /tmp/...$$ or /tmp/...$RANDOM: guessable, and $$ especially so.
    if grep -nE '/tmp/[A-Za-z0-9_.-]*\$(\$|RANDOM)' "$f" | grep -v '^[0-9]*:#' >/dev/null 2>&1; then
        printf '  \033[1;31mFAIL\033[0m  %-28s uses a predictable path in /tmp\n' "$(basename "$f")"
        grep -nE '/tmp/[A-Za-z0-9_.-]*\$(\$|RANDOM)' "$f" | grep -v '^[0-9]*:#' | sed 's/^/          /'
        predictable_tmp=1
    fi
done
if [ "$predictable_tmp" -eq 0 ]; then
    printf '  \033[1;32mPASS\033[0m  %-28s no predictable temp paths (use mktemp)\n' "all scripts"
    PASS=$((PASS+1))
else
    FAIL=$((FAIL+1))
fi

printf '\n  ---- %d passed, %d failed ----\n\n' "$PASS" "$FAIL"
[ "$FAIL" -eq 0 ]
