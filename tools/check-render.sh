#!/usr/bin/env bash
# Render every console view in jsdom. See tools/console-render-test.js for why.
set -euo pipefail
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
command -v docker >/dev/null || { echo "need docker to run the console render test" >&2; exit 1; }
docker run --rm -v "$REPO_ROOT:/repo" -w /tmp node:22-alpine sh -c '
  npm install --silent --no-audit --no-fund jsdom >/dev/null 2>&1
  cp -r /repo/dist /tmp/dist
  cp /repo/tools/console-render-test.js /tmp/
  mkdir -p /tmp/tools && cp /repo/tools/console-render-test.js /tmp/tools/
  node /tmp/tools/console-render-test.js
'
