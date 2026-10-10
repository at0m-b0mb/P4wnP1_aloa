#!/usr/bin/env bash
#
# check-palette.sh -- drive the command palette in jsdom.
#
# WHY THIS IS SEPARATE FROM check-render.sh
#
# The render gate enumerates Object.keys(Views) and calls each one. The
# palette is not a view: it registers no Views entry, it installs global key
# handlers and builds its own DOM. So the render gate proves only that
# palette.js LOADS without throwing -- not that Ctrl-K opens anything, that
# Escape closes it, that typing filters, that Enter navigates, or that it
# keeps its hands off the HIDScript editor. A console feature whose only
# coverage is "the file parsed" is a feature nobody is testing.
set -uo pipefail
cd "$(dirname "$0")/.."

if [ "${P4_NODE:-0}" = "1" ] && command -v node >/dev/null; then
    exec node tools/palette-test.js
fi
command -v docker >/dev/null || {
    echo "need docker (or P4_NODE=1 with node installed) to run the palette test" >&2
    exit 1
}
docker run --rm -v "$PWD:/repo:ro" -w /tmp node:22-alpine sh -c '
  cp -R /repo/tools /repo/dist /tmp/ 2>/dev/null
  npm install --silent --no-audit --no-fund jsdom >/dev/null 2>&1
  node /tmp/tools/palette-test.js'
