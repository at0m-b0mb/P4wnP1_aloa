# Live security findings against v0.7.6

Measured against the running device on 2026-10-13, over the USB ethernet link.
Every line says how it was measured. Nothing here is inferred from reading code
alone — that is what the separate code audit was for, and its results are **not**
in this file (see RESUME.md).

Device under test: v0.7.6, `172.16.0.1:8000`, reached from `en9` (`172.16.0.2`)
over CDC ECM. Running image confirmed by `/app/` serving `?v=29`, which matches
tag v0.7.6 and HEAD; v0.7.5 served `?v=26`.

## Tested and sound

Each of these was an attempt to break something that did not work. Worth keeping,
because a future change that breaks one of them should be noticed.

- **The unauthenticated API is closed.** `/api/v1/rpc`, `/api/v1/panel.txt` and
  `/api/v1/events` all return `401 {"error":"missing Authorization header"}`.
  `/api/auth/whoami` and `/api/auth/sessions` return `401 {"error":"no token"}`.

- **No RPC-method-existence oracle.** `GET /api/v1/rpc/<name>` returns the same
  `405 use POST` whether the method exists or not. Tested with four names, two
  real and two invented. The 405-before-401 ordering is a cosmetic nit, not a
  disclosure.

- **Failed logins really are serialised.** Six parallel wrong logins took 19s
  wall — about 3.2s each, not six at once — so the lock in
  `service/auth/manager.go` genuinely spans the KDF and the delay, not just the
  delay. Brute-force throughput is ~0.31 guesses/sec.

- **A login flood does not take the device down.** While those six ran,
  `/api/auth/health` stayed at 28–41ms against a 20–28ms baseline. The single
  ARM11 core never saturated. *I expected this to be a pre-auth CPU DoS and it
  is not.*

- **Request bodies are bounded and the bound works.** A 10MB POST to
  `/api/auth/login` was cut off after ~231KB of TCP buffering and answered
  `400 invalid JSON body` in 31ms; the device was back to 26ms immediately.
  (`decodeLimited`, `service/auth/handler.go`.)

- **Nothing reads an unauthenticated body.** The same 10MB POST to
  `/api/v1/rpc/DeployGadgetSetting` and to `/api/v1/panel/press` returned 401
  with `uploaded=0` in 19ms — not one byte consumed before the auth check. No
  pre-auth amplification.

## L1 — the console page ships no CSP, no X-Frame-Options, no nosniff

**Medium.** `curl -D - http://172.16.0.1:8000/app/` returns only
`Accept-Ranges`, `Cache-Control`, `Content-Length`, `Content-Type`,
`Last-Modified` and `Date`. The JSON API sets `X-Content-Type-Options: nosniff`
in `apiJSON` (`service/rest_api.go`), but the static file server sets nothing.

An attacker page can iframe the console. How much that is worth depends on where
the session token lives, which the code audit was asked to determine and which I
never got the answer to. Independently of that, no CSP means any XSS found in the
console has no second line of defence.

**Where the fix goes:** `service/rpc_server.go:1701`, the `fileServer`
`HandlerFunc` that currently only sets `Cache-Control`. Better still, wrap
`http_handler` so the API responses get `X-Frame-Options` too.

**The one trap in fixing it:** `dist/www/app/index.html:22` contains an inline
`<script>` that sets the theme before first paint, to avoid a white flash for
dark-mode users. A strict `script-src 'self'` **will break it**. Either move it
to its own file (and then bump every `?v=` — `tools/check-cachebust.sh` will
fail until you do, which is it working) or carry a hash in the policy. Moving it
is cleaner; it stays blocking if it is a plain `<script src>` in `<head>`.

Suggested policy, given there is no CDN and no third-party anything:
`default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:;
object-src 'none'; base-uri 'none'; frame-ancestors 'none'`.
`frame-ancestors 'none'` also covers X-Frame-Options.

## L2 — the device clock is ~28 days behind

**Low, availability only. I rated this higher before tracing it and was wrong.**

`/api/auth/health` reported `server_time: 1789434672` = 2026-09-15 01:11 UTC,
while the real date was 2026-10-13. `Last-Modified` on the same response carried
the true 2026-10-12 file mtime, so it is the clock and not the file. The Pi has
no RTC and there is no internet over the USB link, so fake-hwclock restores a
build-time clock and nothing corrects it.

What depends on it is less than it looks. `service/auth/token.go` issues expiry
with `now.Add(ttl)` (line 57) and checks it with `!now.Before(sess.Expires)`
(lines 79 and 114), both off the **same** `now`. A consistently wrong clock is
therefore harmless: a session still lasts its full 24h of wall time. There is no
TLS and no nonce window, so nothing else is time-dependent.

What remains is a clock *jump*. If NTP ever lands, every live session's expiry is
suddenly 28 days in the past, so the operator and the on-disk local token
(`localcred.go`, `KeepLocalTokenFresh`, 12h refresh) are logged out at once. A
backward jump would extend sessions, but this device's clock starts behind, so it
jumps forward. Annoyance, not a hole.

Kept on the list because it is real and will confuse someone, not because it is
dangerous.

## L3 — a login flood locks the operator out of logging in

**Low.** Follows directly from the serialisation measured above: the lock that
makes brute force useless is global, so while an attacker guesses at one per
3.2s, the real operator's login queues behind them. Already-authenticated
sessions are unaffected; only *new* logins stall.

Worth a thought rather than an urgent fix. Per-source-address queuing would help
and would also weaken the global guarantee, so it is a trade, not a free win.

## L4 — ListenAndServe's real error is thrown away

**Low, but it blinds the one failure you most need to diagnose.**
`service/rpc_server.go:1757`:

```go
go func() {
    if err_http := http_srv.ListenAndServe(); err_http != nil {
        log.Fatal(err)
    }
}()
```

It tests `err_http` and prints `err`. The only `err` in scope comes from
`net.Listen` at line 1604, which is nil on every path that reaches here. So if
the web server ever fails to start — a port already bound, for instance — the
service dies printing `<nil>` and the actual reason is gone.

Fix: `log.Fatal(err_http)`.

## Environment facts worth keeping

- Open ports: 22 and 8000. 80 closed.
- SSH: `OpenSSH_10.0p2 Raspbian-7+deb13u4`. Host key fingerprints on this boot:
  - RSA `SHA256:oJV5rElKcK6cHkFLpb6fuKZIf7qV5LtlwhmgxHhYhrA`
  - ECDSA `SHA256:95qxSuOlvcbx5HA1d+pSPbrqhy/1HnJpSVaSAeRCtGI`
  - ED25519 `SHA256:mbjZuC6sk8E4l7aPlmtj/G01spix/8K0kbG+EHAWym8`

  **Keep these.** If a future reflash produces the *same* fingerprints, host keys
  are baked into the image and every device in the world shares them. Current
  evidence says they are not: `image/lib/customize.sh:302` does
  `rm -f /etc/ssh/ssh_host_*` and `image/lib/verify.sh:184` checks for it. A
  second flash is what would actually prove it.

- The shipped template database `dist/db/init.db` holds 14 keys: two master
  templates (`master_startup`, `master_wifi covert channel`), three
  trigger-action sets (`tas_bincounter`, `tas_startup`,
  `tas_wifi_covert_channel`), and the startup settings. Listed with
  `go run ./tools/initdb -in dist/db/init.db -list` in a linux container.

- `make verify` was **green (EXIT=0)** at HEAD `72d3709`, with Colima running.
  Colima is not started automatically; `colima start` was needed first, and
  `tools/check-js.sh` is the gate that fails with a docker socket error when it
  is down.
