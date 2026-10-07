#!/usr/bin/env bash
#
# access-control-test.sh -- attack the running service's access control.
#
# The other gates ask "does it work?". This one asks "can it be made to do
# something it should refuse?", which is a different question and needs a
# different kind of test: every check here is an ATTACK that must FAIL.
#
# A test that asserts a protection works is only worth something if it would
# notice the protection being removed. Several checks here were written by
# first demonstrating the attack succeeding against the real binary, then
# fixing the code, then confirming the same check flipped. Where that is true
# the comment says so.
set -euo pipefail

ARCH="${1:-arm64}"
case "$ARCH" in
    arm64) PLATFORM=linux/arm64 ;;
    armhf) PLATFORM=linux/arm/v7 ;;
    *) echo "usage: $0 [armhf|arm64]" >&2; exit 2 ;;
esac

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

BIN="image/out/bin/$ARCH"
if [ ! -x "$BIN/P4wnP1_service" ]; then
    echo "==> building binaries for $ARCH"
    mkdir -p "$BIN"
    for b in P4wnP1_service P4wnP1_cli p4wnp1-hashpw; do
        if [ "$ARCH" = arm64 ]; then
            CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o "$BIN/$b" "./cmd/$b"
        else
            CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=6 go build -o "$BIN/$b" "./cmd/$b"
        fi
    done
fi

echo "==> attacking access control in a $PLATFORM container"
docker run --rm -i --platform "$PLATFORM" --privileged -v "$REPO_ROOT:/repo:ro" \
    -e ARCH="$ARCH" debian:bookworm-slim bash -s <<'IN_CONTAINER'
set -uo pipefail
PASS=0; FAIL=0
ok()   { printf '  \033[1;32mPASS\033[0m  %s\n' "$1"; PASS=$((PASS+1)); }
bad()  { printf '  \033[1;31mFAIL\033[0m  %s\n        \033[1;31m%s\033[0m\n' "$1" "$2"; FAIL=$((FAIL+1)); }
note() { printf '  \033[1;33mNOTE\033[0m  %s\n        %s\n' "$1" "$2"; }
check(){ [ "$2" = "$3" ] && ok "$1" || bad "$1" "expected $2, got $3"; }
section(){ printf '\n  \033[1m%s\033[0m\n' "$1"; }

apt-get update -qq >/dev/null 2>&1
apt-get install -y -qq curl python3 >/dev/null 2>&1

mkdir -p /usr/local/P4wnP1 /etc/p4wnp1
install -m 0755 /repo/image/out/bin/"$ARCH"/{P4wnP1_service,P4wnP1_cli,p4wnp1-hashpw} /usr/local/bin/
cp -R /repo/dist/{keymaps,scripts,HIDScripts,www,db} /usr/local/P4wnP1/ 2>/dev/null || true

PW="access-control-test-password"
printf '%s' "$PW" | /usr/local/bin/p4wnp1-hashpw --username admin >/dev/null 2>&1
/usr/local/bin/P4wnP1_service > /tmp/svc.log 2>&1 &
SVC=$!
for i in $(seq 1 45); do
    curl -sf -o /dev/null http://127.0.0.1:8000/app/ 2>/dev/null && break
    sleep 1
done

B=http://127.0.0.1:8000
JSON='-H Content-Type:application/json'
code(){ curl -s -o /dev/null -w '%{http_code}' "$@"; }
body(){ curl -s "$@"; }

TOK=$(body -X POST $JSON -d "{\"username\":\"admin\",\"password\":\"$PW\"}" $B/api/auth/login \
      | python3 -c 'import sys,json;print(json.load(sys.stdin).get("token",""))' 2>/dev/null)
AUTH="-H Authorization:Bearer${TOK:+ }Bearer"
AUTHH="Authorization: Bearer $TOK"
[ -n "$TOK" ] && ok "login issues a token" || bad "login issues a token" "no token returned"

# An unauthenticated user is the baseline threat. Every API surface must
# refuse them -- not just the one endpoint somebody remembered to test.
section "1. nothing works without a token"
check "POST /api/v1/rpc/{m} refuses"        "401" "$(code -X POST $JSON -d '{}' $B/api/v1/rpc/GetLEDSettings)"
check "GET  /api/v1/rpc (method list) refuses" "401" "$(code $B/api/v1/rpc)"
check "GET  /api/v1/events (SSE) refuses"   "401" "$(code $B/api/v1/events)"
check "GET  /api/auth/whoami refuses"       "401" "$(code $B/api/auth/whoami)"
check "garbage token refuses"               "401" "$(code -X POST $JSON -H 'Authorization: Bearer not-a-real-token' -d '{}' $B/api/v1/rpc/GetLEDSettings)"
check "non-bearer scheme refuses"           "401" "$(code -X POST $JSON -H 'Authorization: Basic YWRtaW46eA==' -d '{}' $B/api/v1/rpc/GetLEDSettings)"
check "wrong password refuses"              "401" "$(code -X POST $JSON -d '{"username":"admin","password":"wrong"}' $B/api/auth/login)"

# The console is reached from a browser that is often also visiting untrusted
# pages, so a foreign origin must be refused even WITH a valid token.
section "2. cross-origin"
check "foreign Origin refused despite a valid token" "403" \
  "$(code -X POST $JSON -H "$AUTHH" -H 'Origin: https://evil.example' -d '{}' $B/api/v1/rpc/GetLEDSettings)"
check "Origin: null refused"                "403" \
  "$(code -X POST $JSON -H "$AUTHH" -H 'Origin: null' -d '{}' $B/api/v1/rpc/GetLEDSettings)"
check "lookalike host refused"              "403" \
  "$(code -X POST $JSON -H "$AUTHH" -H 'Origin: http://127.0.0.1.evil.example' -d '{}' $B/api/v1/rpc/GetLEDSettings)"
check "no Origin (curl, the CLI) still works" "200" \
  "$(code -X POST $JSON -H "$AUTHH" -d '{}' $B/api/v1/rpc/GetLEDSettings)"

# DNS rebinding: the attacker controls the domain, so they control BOTH Origin
# and Host, and the two match. Before the Host guard this executed the RPC --
# confirmed against a running service, 200 with Host+Origin both evil.example
# while forging Origin alone was correctly refused 403.
check "a rebindable Host is refused even when Origin matches it" "403" \
  "$(code -X POST $JSON -H "$AUTHH" -H 'Host: evil.example' -H 'Origin: http://evil.example' -d '{}' $B/api/v1/rpc/GetLEDSettings)"
check "an IP-literal Host still works (an IP cannot be rebound)" "200" \
  "$(code -X POST $JSON -H "$AUTHH" -H 'Host: 127.0.0.1:8000' -d '{}' $B/api/v1/rpc/GetLEDSettings)"

# The credential endpoints are the ones most worth attacking, and they were the
# ones with no Host or Origin check at all: a foreign origin got 200 and a
# fresh token. Guarding the RPC surface while leaving login open is not a fix.
check "login refuses a foreign Origin"      "403" \
  "$(code -X POST $JSON -H 'Origin: https://evil.example' -d "{\"username\":\"admin\",\"password\":\"$PW\"}" $B/api/auth/login)"
check "login refuses a rebindable Host"     "403" \
  "$(code -X POST $JSON -H 'Host: evil.example' -H 'Origin: http://evil.example' -d "{\"username\":\"admin\",\"password\":\"$PW\"}" $B/api/auth/login)"
check "login still works for the CLI (no Origin)" "200" \
  "$(code -X POST $JSON -d "{\"username\":\"admin\",\"password\":\"$PW\"}" $B/api/auth/login)"
check "whoami refuses a foreign Origin"     "403" \
  "$(code -H "$AUTHH" -H 'Origin: https://evil.example' $B/api/auth/whoami)"

# OPTIONS is routed to the gRPC-web wrapper before any auth check, so confirm
# it cannot be turned into a permissive preflight. If a CORS header ever shows
# up here, the origin checks above stop being worth anything.
PREFLIGHT=$(curl -s -i -X OPTIONS -H 'Origin: https://evil.example' \
            -H 'Access-Control-Request-Method: POST' $B/api/v1/rpc/GetLEDSettings 2>/dev/null)
if echo "$PREFLIGHT" | grep -qi 'access-control-allow-origin'; then
    bad "preflight emits no CORS grant" "$(echo "$PREFLIGHT" | grep -i access-control- | tr -d '\r' | tr '\n' ' ')"
else
    ok "preflight emits no CORS grant"
fi

# Every file RPC, every folder. The allowlist is a containment boundary, and
# a boundary tested at one door is not tested.
section "3. path traversal, every folder"
for folder in 0 1 2; do
  for name in "../etc/shadow" "../../etc/p4wnp1/auth.json" "/etc/shadow" "../../../root/INITIAL_CREDENTIALS.txt"; do
    R=$(body -X POST $JSON -H "$AUTHH" \
        -d "{\"filename\":\"$name\",\"folder\":$folder,\"start\":0,\"len\":64}" $B/api/v1/rpc/FSReadFile)
    case "$R" in
      *'"error"'*) : ;;
      *) bad "folder=$folder read refuses '$name'" "got a success response: ${R:0:120}" ;;
    esac
  done
done
ok "all folders refuse lexical traversal on read"

for folder in 0 1 2; do
  R=$(body -X POST $JSON -H "$AUTHH" \
      -d "{\"filename\":\"../../../etc/escaped_$folder\",\"folder\":$folder,\"data\":\"eA==\"}" $B/api/v1/rpc/FSWriteFile)
  case "$R" in
    *'"error"'*) : ;;
    *) bad "folder=$folder write refuses traversal" "got a success response: ${R:0:120}" ;;
  esac
done
ok "all folders refuse lexical traversal on write"

# THIS SECTION FOUND A REAL ESCAPE. Before the fix, a non-root local user
# created /tmp/sub/escalate -> /etc/cron.d/pwned and FSWriteFile wrote a
# root-owned cron job through it -- root code execution from an unprivileged
# shell. filepath.Clean is lexical and does not resolve symlinks, and the
# kernel's fs.protected_symlinks only covers symlinks sitting DIRECTLY in a
# sticky directory, not one level down in a directory the attacker made.
section "4. symlink escape from the world-writable /tmp allowlist"
useradd -m lowpriv >/dev/null 2>&1 || true
rm -rf /tmp/sub /tmp/rootlink /etc/cron.d/pwned
su lowpriv -s /bin/sh -c 'mkdir -p /tmp/sub && ln -sf /etc/cron.d/pwned /tmp/sub/escalate && ln -sf /run/p4wnp1/local.token /tmp/sub/tok' 2>/dev/null
ln -sf /etc/p4wnp1/auth.json /tmp/rootlink

CRON_PAYLOAD=$(printf '* * * * * root id\n' | base64 | tr -d '\n')
R=$(body -X POST $JSON -H "$AUTHH" \
    -d "{\"filename\":\"sub/escalate\",\"folder\":0,\"data\":\"$CRON_PAYLOAD\"}" $B/api/v1/rpc/FSWriteFile)
if [ -e /etc/cron.d/pwned ]; then
    bad "write cannot escape through a symlink in a subdirectory" \
        "ROOT FILE WRITTEN OUTSIDE THE ALLOWLIST at /etc/cron.d/pwned"
else
    case "$R" in
      *'"error"'*) ok "write cannot escape through a symlink in a subdirectory" ;;
      *) bad "write cannot escape through a symlink in a subdirectory" "write reported success: ${R:0:120}" ;;
    esac
fi

# The same symlink grants an arbitrary READ, and the two things most worth
# reading are the password hashes and the service's own local credential.
for pair in "sub/tok:the machine-local credential" "rootlink:the password hash database"; do
    fn="${pair%%:*}"; what="${pair#*:}"
    R=$(body -X POST $JSON -H "$AUTHH" \
        -d "{\"filename\":\"$fn\",\"folder\":0,\"start\":0,\"len\":512}" $B/api/v1/rpc/FSReadFile)
    case "$R" in
      *'"error"'*) ok "read cannot reach $what through a symlink" ;;
      *) bad "read cannot reach $what through a symlink" "LEAKED: ${R:0:150}" ;;
    esac
done

# ...while ordinary use must keep working. A path guard that also blocks
# legitimate writes would be "fixed" by reverting it.
W=$(body -X POST $JSON -H "$AUTHH" -d '{"filename":"ok.txt","folder":0,"data":"aGVsbG8K"}' $B/api/v1/rpc/FSWriteFile)
R=$(body -X POST $JSON -H "$AUTHH" -d '{"filename":"ok.txt","folder":0,"start":0,"len":16}' $B/api/v1/rpc/FSReadFile)
case "$W$R" in
  *'"error"'*) bad "ordinary file IO still works" "$W $R" ;;
  *) ok "ordinary file IO still works" ;;
esac
# A real subdirectory, created out of band: FSWriteFile opens with O_CREATE,
# which does not create parent directories, so the directory must exist first.
# The containment fix must not have broken writes into legitimate subdirs.
mkdir -p /tmp/realsub
W=$(body -X POST $JSON -H "$AUTHH" -d '{"filename":"realsub/ok.txt","folder":0,"data":"aGVsbG8K"}' $B/api/v1/rpc/FSWriteFile)
case "$W" in
  *'"error"'*) bad "ordinary writes into subdirectories still work" "$W" ;;
  *) ok "ordinary writes into subdirectories still work" ;;
esac

section "5. token lifecycle"
T2=$(body -X POST $JSON -d "{\"username\":\"admin\",\"password\":\"$PW\"}" $B/api/auth/login \
     | python3 -c 'import sys,json;print(json.load(sys.stdin).get("token",""))' 2>/dev/null)
check "a fresh token works"                 "200" "$(code -H "Authorization: Bearer $T2" $B/api/auth/whoami)"
curl -s -o /dev/null -X POST -H "Authorization: Bearer $T2" $B/api/auth/logout
check "logout revokes that token"           "401" "$(code -H "Authorization: Bearer $T2" $B/api/auth/whoami)"
check "logout did NOT revoke other sessions" "200" "$(code -H "$AUTHH" $B/api/auth/whoami)"

# A password change must invalidate every session, or a stolen token outlives
# the response to it being stolen.
T3=$(body -X POST $JSON -d "{\"username\":\"admin\",\"password\":\"$PW\"}" $B/api/auth/login \
     | python3 -c 'import sys,json;print(json.load(sys.stdin).get("token",""))' 2>/dev/null)
NEWPW="rotated-access-control-password"
curl -s -o /dev/null -X POST $JSON -H "Authorization: Bearer $T3" \
  -d "{\"username\":\"admin\",\"old_password\":\"$PW\",\"new_password\":\"$NEWPW\"}" $B/api/auth/changepw
check "changing the password revokes every session" "401" "$(code -H "Authorization: Bearer $T3" $B/api/auth/whoami)"
check "the old password no longer logs in"  "401" \
  "$(code -X POST $JSON -d "{\"username\":\"admin\",\"password\":\"$PW\"}" $B/api/auth/login)"
check "the new password does log in"        "200" \
  "$(code -X POST $JSON -d "{\"username\":\"admin\",\"password\":\"$NEWPW\"}" $B/api/auth/login)"

TOK=$(body -X POST $JSON -d "{\"username\":\"admin\",\"password\":\"$NEWPW\"}" $B/api/auth/login \
      | python3 -c 'import sys,json;print(json.load(sys.stdin).get("token",""))' 2>/dev/null)
AUTHH="Authorization: Bearer $TOK"

# NOTE: this section deliberately runs AFTER the password rotation in section
# 5. RevokeAll() destroys every session including the device's own script
# credential, and nothing used to re-issue it until KeepLocalTokenFresh next
# ticked -- up to twelve hours during which any trigger firing on the device
# would fail Unauthenticated. Keep this section after section 5.
section "6. the machine-local credential (AFTER a password rotation)"
check "token file is root-only"             "600" "$(stat -c %a /run/p4wnp1/local.token 2>/dev/null)"
check "its directory is root-only"          "700" "$(stat -c %a /run/p4wnp1 2>/dev/null)"
if [ -r /run/p4wnp1/local.token ] && su lowpriv -s /bin/sh -c 'cat /run/p4wnp1/local.token' >/dev/null 2>&1; then
    bad "an unprivileged local user cannot read the token" "lowpriv read it"
else
    ok "an unprivileged local user cannot read the token"
fi
# It must be an ORDINARY session: revocable like any other, not a bypass.
LOCAL_TOK=$(cat /run/p4wnp1/local.token 2>/dev/null)
if [ -n "$LOCAL_TOK" ]; then
    check "the local credential authenticates like any session" "200" \
      "$(code -H "Authorization: Bearer $LOCAL_TOK" $B/api/auth/whoami)"
    WHO=$(body -H "Authorization: Bearer $LOCAL_TOK" $B/api/auth/whoami)
    case "$WHO" in
      *local-script*) ok "it identifies itself as a script, not as a human user" ;;
      *) bad "it identifies itself as a script, not as a human user" "whoami said: ${WHO:0:120}" ;;
    esac
else
    bad "the local credential file has contents" "it was empty or missing"
fi

# An audit view that leaks the thing it is auditing is worse than none: this
# list is rendered in a browser, so a token in it would be exposed to any XSS,
# screenshot or shoulder-surf.
section "7. the session list"
check "listing sessions needs a token"      "401" "$(code $B/api/auth/sessions)"
check "revoking a session needs a token"    "401" "$(code -X POST $JSON -d '{"id":"x"}' $B/api/auth/sessions/revoke)"

SESSIONS=$(body -H "$AUTHH" $B/api/auth/sessions)
if printf '%s' "$SESSIONS" | grep -qF "$TOK"; then
    bad "the session list contains no tokens" "the caller's own token appears verbatim in the response"
elif [ -n "$LOCAL_TOK" ] && printf '%s' "$SESSIONS" | grep -qF "$LOCAL_TOK"; then
    bad "the session list contains no tokens" "the machine-local token appears in the response"
else
    ok "the session list contains no tokens"
fi
case "$SESSIONS" in
  *local-script*) ok "the device's own credential is shown as a script, not a user" ;;
  *) bad "the device's own credential is shown as a script, not a user" "no local-script entry: ${SESSIONS:0:140}" ;;
esac

# Revoke a DIFFERENT session and confirm the caller's own survives: a revoke
# that signs you out while removing someone else is not a usable control.
VICTIM=$(body -X POST $JSON -d "{\"username\":\"admin\",\"password\":\"$NEWPW\"}" $B/api/auth/login \
         | python3 -c 'import sys,json;print(json.load(sys.stdin).get("token",""))' 2>/dev/null)
VID=$(body -H "Authorization: Bearer $VICTIM" $B/api/auth/sessions \
      | python3 -c 'import sys,json;print(next((s["id"] for s in json.load(sys.stdin)["sessions"] if s["is_current"]),""))' 2>/dev/null)
if [ -n "$VID" ]; then
    check "revoking a named session returns 204" "204" \
      "$(code -X POST $JSON -H "$AUTHH" -d "{\"id\":\"$VID\"}" $B/api/auth/sessions/revoke)"
    check "that session is now dead"            "401" "$(code -H "Authorization: Bearer $VICTIM" $B/api/auth/whoami)"
    check "the caller's own session survived"   "200" "$(code -H "$AUTHH" $B/api/auth/whoami)"
else
    bad "could identify a session to revoke" "no is_current entry in the list"
fi
check "revoking an unknown id says so"      "404" \
  "$(code -X POST $JSON -H "$AUTHH" -d '{"id":"0000000000000000"}' $B/api/auth/sessions/revoke)"

# A session id must name a session without being usable as one.
if [ -n "$VID" ]; then
    check "a session id is not a usable token" "401" "$(code -H "Authorization: Bearer $VID" $B/api/auth/whoami)"
fi

section "8. dispatch surface"
check "an unknown method is 404, not a crash" "404" \
  "$(code -X POST $JSON -H "$AUTHH" -d '{}' $B/api/v1/rpc/NoSuchMethodAtAll)"
check "a method name with a slash is rejected" "400" \
  "$(code -X POST $JSON -H "$AUTHH" -d '{}' $B/api/v1/rpc/Foo/Bar)"
check "GET on an RPC is rejected"           "405" "$(code -H "$AUTHH" $B/api/v1/rpc/GetLEDSettings)"
check "an oversized body is rejected"       "413" \
  "$(python3 -c 'print("{\"a\":\"" + "x"*9000000 + "\"}")' | curl -s -o /dev/null -w '%{http_code}' -X POST $JSON -H "$AUTHH" --data-binary @- $B/api/v1/rpc/GetLEDSettings)"

# Method lookup is keyed on the LOWERCASED name, so the same RPC is reachable
# under any casing. That is not a hole today -- nothing filters on the name --
# but anything added later that matches names case-sensitively (an audit log,
# a read-only mode, a per-method allowlist) would be trivially bypassable.
# Asserted so the property is visible rather than discovered.
LOWER=$(code -X POST $JSON -H "$AUTHH" -d '{}' $B/api/v1/rpc/getledsettings)
UPPER=$(code -X POST $JSON -H "$AUTHH" -d '{}' $B/api/v1/rpc/GETLEDSETTINGS)
if [ "$LOWER" = "200" ] && [ "$UPPER" = "200" ]; then
    note "method names are matched case-insensitively" \
         "GetLEDSettings is also reachable as getledsettings and GETLEDSETTINGS. Any future per-method filter must lowercase before comparing."
else
    ok "method names are case-sensitive (this changed -- update the note in this test)"
fi

# Routing is where auth gets skipped by accident: the API handler is chosen by
# a path prefix, and anything that reaches a handler by a path the prefix check
# did not expect arrives with no token checked. ServeMux normalises and
# redirects rather than serving these, but that is a property worth asserting
# rather than assuming, because it changes when routes change.
# The router used to dispatch on Sec-Websocket-Protocol BEFORE any auth check,
# into a wrapper built with WithWebsockets(true). WithOriginFunc governs the
# HTTP check, not the websocket one, and the library's default websocket origin
# check compares two headers the caller writes -- so `Host: anything` plus a
# matching Origin always passed. gorilla then cleared the socket deadline and
# blocked in ReadMessage with no read limit: an anonymous peer could park
# goroutines and fds forever, or stream an unbounded frame into a root process.
# Nothing shipped ever spoke this transport.
section "9. the unauthenticated websocket upgrade is gone"
WS=$(python3 - <<'PYEOF'
import socket, os, base64
CRLF = chr(13) + chr(10)          # written this way so no layer of shell,
BLANK = CRLF + CRLF               # heredoc or generator can mangle it
key = base64.b64encode(os.urandom(16)).decode()
req = CRLF.join([
    "GET /P4wnP1_grpc.P4WNP1/EchoRequest HTTP/1.1",
    "Host: anything",
    "Origin: http://anything",
    "Upgrade: websocket",
    "Connection: Upgrade",
    "Sec-WebSocket-Version: 13",
    "Sec-WebSocket-Protocol: grpc-websockets",
    "Sec-WebSocket-Key: " + key,
]) + BLANK
try:
    s = socket.create_connection(("127.0.0.1", 8000), timeout=5)
    s.sendall(req.encode())
    data = s.recv(64).decode(errors="replace").strip()
    print(data.splitlines()[0] if data else "EMPTY-RESPONSE")
    s.close()
except Exception as e:
    print("PROBE-ERROR", e)
PYEOF
)
case "$WS" in
  *101*)                   bad "an anonymous websocket upgrade is refused" "server answered: $WS (pre-auth goroutine/heap DoS)" ;;
  HTTP/*)                  ok  "an anonymous websocket upgrade is refused ($WS)" ;;
  # Anything else means the probe did not actually speak to the server. A
  # broken probe must fail loudly: the first version of this check had a
  # mangled heredoc, never ran, and reported a clean pass.
  *)                       bad "an anonymous websocket upgrade is refused" "the probe did not run: $WS" ;;
esac

section "10. path confusion must not skip authentication"
for p in \
  "/api/v1/rpc/../../auth/health" \
  "/api/v1/../auth/health" \
  "/api/v1//rpc/GetLEDSettings" \
  "/api/v1/rpc/%2e%2e/%2e%2e/auth/health" \
  "/api/v1/rpc;x/GetLEDSettings" \
  "/API/V1/rpc/GetLEDSettings" \
  "/api/v1/rpc/GetLEDSettings/" \
  "/api/auth/../v1/rpc/GetLEDSettings" ; do
    C=$(curl -s -L -o /dev/null -w '%{http_code}' --path-as-is -X POST $JSON -d '{}' "$B$p" 2>/dev/null)
    case "$C" in
      200|204) bad "path confusion cannot skip auth ($p)" "reached a handler unauthenticated: HTTP $C" ;;
    esac
done
ok "path confusion cannot skip auth"

# The static tree is deliberately unauthenticated so the console can load
# before login. It must therefore contain nothing but the console.
for p in "/../etc/shadow" "/..%2f..%2fetc/shadow" "/db" "/keymaps" "/../../root/INITIAL_CREDENTIALS.txt"; do
    C=$(code --path-as-is "$B$p")
    [ "$C" = "200" ] && bad "the static server serves only the console ($p)" "HTTP 200"
done
ok "the static server serves only the console"

# One endpoint answers without a token, on purpose, so pin what it discloses.
H=$(body $B/api/auth/health)
case "$H" in
  *password*|*token*|*hash*|*ssid*|*psk*)
    bad "the unauthenticated health endpoint discloses nothing sensitive" "${H:0:140}" ;;
  *) ok "the unauthenticated health endpoint discloses nothing sensitive" ;;
esac

section "11. secrets must not leak into logs"
if grep -qiE "Bearer [A-Za-z0-9_-]{20,}|password_hash|\"password\"" /tmp/svc.log; then
    bad "no token or password in the service log" "$(grep -oiE 'Bearer [A-Za-z0-9_-]{20,}|password_hash|"password"' /tmp/svc.log | head -1)"
else
    ok "no token or password in the service log"
fi
if grep -q 'Unauthenticated' /tmp/svc.log; then
    bad "the device's own scripts authenticate" "$(grep -m1 Unauthenticated /tmp/svc.log | tail -c 90)"
else
    ok "the device's own scripts authenticate"
fi

kill -0 "$SVC" 2>/dev/null && ok "service survived every attack above" \
                           || bad "service survived every attack above" "it died"
if grep -q 'panic' /tmp/svc.log; then
    bad "no panic in the log" "$(grep -m1 -A3 panic /tmp/svc.log | tr '\n' ' ')"
else
    ok "no panic in the log"
fi

kill -TERM "$SVC" 2>/dev/null
printf '\n  ---- %d passed, %d failed ----\n\n' "$PASS" "$FAIL"
[ "$FAIL" -eq 0 ]
IN_CONTAINER
