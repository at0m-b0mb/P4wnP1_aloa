#!/bin/bash
set -euo pipefail

# has to be run from 'build_support' subfolder
cd .. || { echo "could not cd to repo root" >&2; exit 1; }
echo "compiling P4wnP1_service, P4wnP1_cli, and p4wnp1-hashpw ..."
env GOOS=linux GOARCH=arm GOARM=6 go build -o build/P4wnP1_service cmd/P4wnP1_service/P4wnP1_service.go
env GOOS=linux GOARCH=arm GOARM=6 go build -o build/P4wnP1_cli cmd/P4wnP1_cli/P4wnP1_cli.go
env GOOS=linux GOARCH=arm GOARM=6 go build -o build/p4wnp1-hashpw ./cmd/p4wnp1-hashpw
env GOOS=linux GOARCH=arm GOARM=6 go build -o build/p4wnp1-oled ./cmd/p4wnp1-oled

# No web-app build step. The console is hand-written JavaScript served
# straight from dist/www/app/ -- there is nothing to compile. The original
# GopherJS client that used to be compiled here has been deleted: it could
# not work (it predates the auth layer and never sent a bearer token) and
# nothing built it.

echo "...Results stored in ./build directory"
echo
echo "On P4wnP1 ALOA the compiled files have to be placed at the following"
echo "locations:"
echo
echo "    /usr/local/bin/P4wnP1_cli"
echo "    /usr/local/bin/P4wnP1_service"
echo "    /usr/local/bin/p4wnp1-hashpw"
echo "    /usr/local/bin/p4wnp1-oled"

