#!/usr/bin/env bash
#
# check-cachebust.sh -- if a console file changed, index.html's ?v= must have
# been bumped.
#
# WHY
#
# index.html says it, and nothing enforced it:
#
#   ?v= is bumped whenever these files change: the console is updated in
#   place on a device an operator reaches over USB or its own access point,
#   and a stale cached bundle talking to a new API is a confusing failure.
#
# It is confusing because nothing looks broken. The browser serves the JS it
# already had, the page renders, and a feature you just added is simply
# absent -- so you go looking for the bug in code that is correct and not
# being run. That happened here: a GPIO trigger was added, the file on the
# server had it, and the page did not, because the browser still held v23.
#
# HOW
#
# .cachebust records the version and a hash of every console file at the time
# that version was set. If the files hash differently now, the version must
# have moved too. Re-record with:  make cachebust
set -uo pipefail
cd "$(dirname "$0")/.."

APP=dist/www/app
STAMP="$APP/.cachebust"

current_hash() {
    find "$APP" -type f ! -name 'index.html' ! -name '.cachebust' \
        | LC_ALL=C sort | xargs shasum -a 256 2>/dev/null | shasum -a 256 | cut -d' ' -f1
}

version_in_html() {
    grep -oE '\?v=[0-9]+' "$APP/index.html" | sort -u | sed 's/?v=//'
}

vs=$(version_in_html)
n=$(printf '%s\n' "$vs" | grep -c .)
if [ "$n" -ne 1 ]; then
    printf '  \033[31mFAIL\033[0m  index.html uses %d different ?v= values: %s\n' \
        "$n" "$(printf '%s' "$vs" | tr '\n' ' ')"
    echo "        One stale tag is enough to serve a mismatched file. Make them equal."
    exit 1
fi
V="$vs"
H=$(current_hash)
[ -n "$H" ] || { echo "  FAIL  could not hash the console files; this check cannot work"; exit 1; }

if [ "${1:-}" = "--record" ]; then
    printf '%s %s\n' "$V" "$H" > "$STAMP"
    echo "  recorded v$V = ${H:0:16}"
    exit 0
fi

if [ ! -f "$STAMP" ]; then
    printf '  \033[31mFAIL\033[0m  %s is missing. Run: make cachebust\n' "$STAMP"
    exit 1
fi
read -r RV RH < "$STAMP"
if [ "$H" = "$RH" ]; then
    printf '  \033[32mok\033[0m    console files unchanged since v%s was set\n' "$RV"
    exit 0
fi
if [ "$V" != "$RV" ]; then
    printf '  \033[32mok\033[0m    console files changed and ?v= moved %s -> %s\n' "$RV" "$V"
    printf '        re-record with: make cachebust\n'
    exit 0
fi
printf '  \033[31mFAIL\033[0m  console files changed but index.html is still ?v=%s\n' "$V"
echo "        A browser that already has v$V will keep serving the OLD files:"
echo "        the page renders, your change is simply absent, and the bug"
echo "        appears to be in code that is correct and not being run."
echo "        Bump every ?v= in $APP/index.html, then: make cachebust"
exit 1
