#!/usr/bin/env bash
# Every HTTP handler on the JSON API must authenticate.
#
# WHY THIS EXISTS
#
# The panel mirror shipped three routes -- /api/v1/panel.png, /panel.txt and
# /panel/press -- that called no authentication at all, while every other
# handler on the same mux called a.authenticate(). Anyone who could reach
# port 8000 could read the device's screen and PRESS ITS BUTTONS.
#
# Three things that were supposed to stop that did not:
#
#   1. The comment where the routes are registered said "Same auth and same
#      origin guard as the RPCs". It was simply untrue, and it read like a
#      decision someone had already checked.
#   2. tools/access-control-test.sh enumerates endpoints BY HAND. It was
#      complete for the endpoints that existed when it was written, and
#      nobody extended it when three more appeared.
#   3. service/panel_mirror_test.go built an apiHandler with no auth manager
#      and asserted the handlers return 200 to a request with no token. The
#      test did not miss the hole; it encoded it as correct behaviour.
#
# A hand-maintained list cannot catch the endpoint nobody remembered to add
# to it. This gate does not take a list: it finds every handler in the
# source and insists each one authenticates, so a NEW route that forgets is
# caught the moment it is written -- on any machine, with no container, no
# device and no network.
set -euo pipefail
cd "$(dirname "$0")/.."

RED=$'\033[31m'; GREEN=$'\033[32m'; OFF=$'\033[0m'
pass=0; fail=0
ok()  { printf "  ${GREEN}ok${OFF}    %s\n" "$1"; pass=$((pass+1)); }
bad() { printf "  ${RED}FAIL${OFF}  %s\n" "$1"; [ -n "${2:-}" ] && printf "        %s\n" "$2"; fail=$((fail+1)); }

echo "==> every *apiHandler HTTP handler authenticates"

found=0
while IFS= read -r line; do
    file=${line%%:*}; rest=${line#*:}; name=${rest%%:*}; auth=${rest##*:}
    found=$((found+1))
    if [ "$auth" = "yes" ]; then
        ok "$name authenticates  ($file)"
    else
        bad "$name does NOT authenticate  ($file)" \
            "add: if _, ok := a.authenticate(w, r); !ok { return }"
    fi
done < <(
    for f in service/*.go; do
        [[ "$f" == *_test.go ]] && continue
        awk -v F="$f" '
            /^func \(a \*apiHandler\) handle[A-Za-z]*\(w http\.ResponseWriter, r \*http\.Request\)/ {
                # ".*) " is greedy and ran to the LAST ") " on the line --
                # the one closing the parameter list -- leaving just "{".
                # Anchor on the handler name itself instead.
                name = $0
                match(name, /handle[A-Za-z]*\(/)
                name = substr(name, RSTART, RLENGTH - 1)
                body = ""; inside = 1; next
            }
            inside { body = body $0 "\n" }
            inside && /^}$/ {
                print F ":" name ":" (body ~ /a\.authenticate\(/ ? "yes" : "no")
                inside = 0
            }
        ' "$f"
    done
)

# A gate that finds nothing passes vacuously. If the handler signature is
# ever reworded this awk stops matching, every check silently disappears,
# and the suite still prints green -- which is the failure mode that let the
# original hole through. So: assert the gate actually looked at something.
if [ "$found" -lt 4 ]; then
    bad "only $found handlers found -- this gate has stopped matching" \
        "the handler signature probably changed; fix the pattern in $0"
fi

echo
echo "==> the registered routes are the ones we checked"
# Catches the other shape of the bug: a route wired to a plain function or a
# closure rather than an *apiHandler method, which the scan above cannot see.
while IFS= read -r route; do
    h=$(sed -n "s|.*mux.HandleFunc(\"${route}\", *\([A-Za-z.]*\)).*|\1|p" service/rest_api.go | head -1)
    case "$h" in
        a.handle*) ok "$route -> $h (an apiHandler method, scanned above)" ;;
        "")        bad "$route has no handler I could resolve" ;;
        *)         bad "$route -> $h is NOT an apiHandler method" \
                       "this gate cannot see its auth; route it through apiHandler" ;;
    esac
done < <(grep -oE 'mux\.HandleFunc\("[^"]+"' service/rest_api.go | sed 's/.*("//;s/"//')

echo
if [ "$fail" -eq 0 ]; then
    printf "  ---- %d passed, 0 failed ----\n" "$pass"
else
    printf "  ---- %d passed, ${RED}%d failed${OFF} ----\n" "$pass" "$fail"
    exit 1
fi
