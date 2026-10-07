#!/usr/bin/env bash
#
# check-js.sh -- parse every console JavaScript file.
#
# This exists because a missing closing paren in app.js shipped once and was
# only caught by loading the page in a browser and reading the console. The
# whole file fails to parse, so the console renders nothing at all -- a total
# outage from a one-character mistake, invisible to every other check in the
# repo. `node --check` catches it in a second.
#
# There is no node on the development machine here, so it runs in a container.
# Set P4_NODE=1 to use a local node if you have one.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

FILES=(dist/www/app/js/*.js)
[ -e "${FILES[0]}" ] || { echo "no JS files found under dist/www/app/js"; exit 1; }

if [ "${P4_NODE:-0}" = "1" ] && command -v node >/dev/null; then
    fail=0
    for f in "${FILES[@]}"; do
        if node --check "$f" 2>/dev/null; then printf '  ok    %s\n' "$f"
        else printf '  FAIL  %s\n' "$f"; node --check "$f" 2>&1 | sed 's/^/        /'; fail=1; fi
    done
    exit "$fail"
fi

command -v docker >/dev/null || {
    echo "need docker (or P4_NODE=1 with node installed) to syntax-check the console JS" >&2
    exit 1
}

docker run --rm -v "$REPO_ROOT:/repo:ro" -w /repo node:22-alpine sh -c '
fail=0
for f in dist/www/app/js/*.js; do
    if node --check "$f" 2>/dev/null; then
        printf "  ok    %s\n" "$f"
    else
        printf "  FAIL  %s\n" "$f"
        node --check "$f" 2>&1 | sed "s/^/        /"
        fail=1
    fi
done
exit $fail
'
