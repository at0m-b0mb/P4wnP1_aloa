/* api.js -- the only thing in the console that talks to the device.
 *
 * The service exposes every unary RPC at POST /api/v1/rpc/<MethodName> and the
 * event stream at GET /api/v1/events. Auth is a bearer token from
 * POST /api/auth/login.
 *
 * Note on the event stream: it is Server-Sent Events, but it is NOT consumed
 * with EventSource. EventSource cannot set request headers, so it cannot carry
 * the bearer token, and putting the token in the query string would leak it
 * into access logs and browser history. We read the response body with fetch +
 * a stream reader instead and parse the SSE framing ourselves.
 */
'use strict';

const Api = (() => {
  /* Two missed 20s server heartbeats before we call the link dead. */
  const STREAM_SILENCE_TIMEOUT = 45000;
  /* An RPC that never settles hangs its view with no error path, so every call
     gets a deadline. Generous, because deploying a template genuinely takes
     seconds on a Pi Zero. */
  const RPC_TIMEOUT = 20000;

  const TOKEN_KEY = 'p4wnp1.token';
  const USER_KEY = 'p4wnp1.user';

  let token = null;
  try { token = localStorage.getItem(TOKEN_KEY); } catch (_) { /* private mode */ }

  function setToken(t, user) {
    token = t;
    try {
      if (t) { localStorage.setItem(TOKEN_KEY, t); localStorage.setItem(USER_KEY, user || ''); }
      else { localStorage.removeItem(TOKEN_KEY); localStorage.removeItem(USER_KEY); }
    } catch (_) { /* storage unavailable; session stays in memory only */ }
  }

  function currentUser() {
    try { return localStorage.getItem(USER_KEY) || null; } catch (_) { return null; }
  }

  function hasToken() { return !!token; }

  /* Thrown for any non-2xx. Carries the status so callers can distinguish
     "your token died" (401) from "that failed" (everything else). */
  class ApiError extends Error {
    constructor(status, message, method) {
      super(message);
      this.name = 'ApiError';
      this.status = status;
      this.method = method;
    }
    get isAuthFailure() { return this.status === 401; }
  }

  async function readError(res) {
    const text = await res.text().catch(() => '');
    if (!text) return res.statusText || ('HTTP ' + res.status);
    try {
      const j = JSON.parse(text);
      return j.error || j.message || text;
    } catch (_) {
      return text.slice(0, 400);
    }
  }

  const authHeaders = () => (token ? { 'Authorization': 'Bearer ' + token } : {});

  /* --- auth ------------------------------------------------------------ */

  async function login(username, password) {
    const res = await fetch('/api/auth/login', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ username, password }),
    });
    if (!res.ok) throw new ApiError(res.status, await readError(res), 'login');
    const body = await res.json();
    if (!body.token) throw new ApiError(500, 'login succeeded but returned no token', 'login');
    setToken(body.token, username);
    return body;
  }

  async function logout() {
    if (token) {
      // Best-effort: a failed server-side revoke must not trap the operator in
      // a session they are trying to leave.
      await fetch('/api/auth/logout', { method: 'POST', headers: authHeaders() }).catch(() => {});
    }
    setToken(null, null);
  }

  async function whoami() {
    const res = await fetch('/api/auth/whoami', { headers: authHeaders() });
    if (!res.ok) throw new ApiError(res.status, await readError(res), 'whoami');
    return res.json();
  }

  async function changePassword(username, oldPassword, newPassword) {
    const res = await fetch('/api/auth/changepw', {
      method: 'POST',
      headers: Object.assign({ 'Content-Type': 'application/json' }, authHeaders()),
      body: JSON.stringify({ username, old_password: oldPassword, new_password: newPassword }),
    });
    if (!res.ok) throw new ApiError(res.status, await readError(res), 'changepw');
  }

  /* --- RPC -------------------------------------------------------------- */

  /* Every gRPC method is reachable by name. `rpc('GetWiFiState')` with no
     argument sends no body, which the server reads as an empty message -- that
     is what the many Empty-taking RPCs expect. */
  async function rpc(method, payload) {
    /* AbortSignal.timeout rather than a bare fetch: without it a request to a
       device that has just reconfigured the interface we are talking over never
       settles, and the caller waits inside guard() forever with no error and no
       spinner. */
    let signal;
    try { signal = AbortSignal.timeout(RPC_TIMEOUT); } catch (_) { signal = undefined; }

    let res;
    try {
      res = await fetch('/api/v1/rpc/' + encodeURIComponent(method), {
        method: 'POST',
        headers: Object.assign(
          payload === undefined ? {} : { 'Content-Type': 'application/json' },
          authHeaders()),
        body: payload === undefined ? undefined : JSON.stringify(payload),
        signal,
      });
    } catch (e) {
      if (e && (e.name === 'TimeoutError' || e.name === 'AbortError')) {
        throw new ApiError(0, 'the device did not answer within ' +
          (RPC_TIMEOUT / 1000) + ' seconds', method);
      }
      throw new ApiError(0, 'could not reach the device: ' + (e && e.message), method);
    }
    if (!res.ok) throw new ApiError(res.status, await readError(res), method);
    const text = await res.text();
    return text ? JSON.parse(text) : {};
  }

  async function methods() {
    const res = await fetch('/api/v1/rpc', { headers: authHeaders() });
    if (!res.ok) throw new ApiError(res.status, await readError(res), 'methods');
    return (await res.json()).methods || [];
  }

  /* --- event stream ----------------------------------------------------- */

  /* Opens the SSE stream and calls onEvent for each event. Reconnects with
     backoff, because this device drops its own network out from under you
     routinely -- deploying a WiFi or USB template is expected to break the
     very connection the console is using. Returns a stop() function. */
  function streamEvents({ onEvent, onStatus }) {
    let stopped = false;
    let controller = null;
    let backoff = 500;
    const MAX_BACKOFF = 15000;

    const status = (s, detail) => { if (onStatus) onStatus(s, detail); };

    async function connectOnce() {
      controller = new AbortController();
      const res = await fetch('/api/v1/events', {
        headers: authHeaders(),
        signal: controller.signal,
      });
      if (res.status === 401) throw new ApiError(401, 'token rejected by event stream', 'events');
      if (!res.ok || !res.body) throw new ApiError(res.status, await readError(res), 'events');

      status('open');
      backoff = 500;

      const reader = res.body.getReader();
      const decoder = new TextDecoder();
      let buf = '';

      /* Watchdog. The server sends a keepalive comment every 20s precisely so
         a client can tell a dead link from a quiet one -- and we were throwing
         those away and keying nothing off them. reader.read() has no timeout,
         so on a blackholed socket its promise never settles: connectOnce
         neither returns nor throws, the reconnect loop never runs, and the
         status pill reads "live" forever while the console is deaf. That is
         this device's NORMAL failure mode, because deploying a USB or WiFi
         template reconfigures the very interface the console arrived over.

         Deliberately not Promise.race with an abort: the losing read() would
         reject with an unhandled AbortError on every tick. One timer, reset on
         each chunk, calling abort directly. It captures `ctl` by value because
         `controller` is reassigned on every reconnect -- a stale timer closing
         over the outer variable would abort the NEXT connection and spin. */
      const ctl = controller;
      let watchdog = null;
      const armWatchdog = () => {
        if (watchdog) clearTimeout(watchdog);
        watchdog = setTimeout(() => {
          // Two missed heartbeats. Abort so read() rejects and the loop retries.
          try { ctl.abort(); } catch (_) {}
        }, STREAM_SILENCE_TIMEOUT);
      };
      armWatchdog();

      try {
      for (;;) {
        const { value, done } = await reader.read();
        if (done) break;
        armWatchdog();
        buf += decoder.decode(value, { stream: true });

        // SSE frames are separated by a blank line.
        let sep;
        while ((sep = buf.indexOf('\n\n')) !== -1) {
          const frame = buf.slice(0, sep);
          buf = buf.slice(sep + 2);
          for (const line of frame.split('\n')) {
            // A keepalive comment carries no data, but its ARRIVAL is the
            // signal: it proves the link is alive. armWatchdog() above already
            // ran for this chunk, so there is nothing more to do but skip it.
            if (line.startsWith(':')) continue;
            if (!line.startsWith('data:')) continue;
            const raw = line.slice(5).trim();
            if (!raw) continue;
            try { onEvent(JSON.parse(raw)); }
            catch (e) { console.warn('unparseable event frame', raw, e); }
          }
        }
      }
      } finally {
        if (watchdog) clearTimeout(watchdog);
      }
    }

    (async function loop() {
      while (!stopped) {
        try {
          await connectOnce();
          if (!stopped) status('closed');
        } catch (e) {
          if (stopped) return;
          if (e instanceof ApiError && e.isAuthFailure) { status('unauthorized'); return; }
          status('retrying', e.message);
        }
        if (stopped) return;
        await new Promise(r => setTimeout(r, backoff));
        backoff = Math.min(backoff * 2, MAX_BACKOFF);
      }
    })();

    return function stop() {
      stopped = true;
      status('stopped');
      if (controller) { try { controller.abort(); } catch (_) {} }
    };
  }

  return {
    ApiError,
    login, logout, whoami, changePassword,
    rpc, methods, streamEvents,
    hasToken, setToken, currentUser,
  };
})();
