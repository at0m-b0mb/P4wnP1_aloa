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
    const res = await fetch('/api/v1/rpc/' + encodeURIComponent(method), {
      method: 'POST',
      headers: Object.assign(
        payload === undefined ? {} : { 'Content-Type': 'application/json' },
        authHeaders()),
      body: payload === undefined ? undefined : JSON.stringify(payload),
    });
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

      for (;;) {
        const { value, done } = await reader.read();
        if (done) break;
        buf += decoder.decode(value, { stream: true });

        // SSE frames are separated by a blank line.
        let sep;
        while ((sep = buf.indexOf('\n\n')) !== -1) {
          const frame = buf.slice(0, sep);
          buf = buf.slice(sep + 2);
          for (const line of frame.split('\n')) {
            if (line.startsWith(':')) continue;            // keepalive comment
            if (!line.startsWith('data:')) continue;
            const raw = line.slice(5).trim();
            if (!raw) continue;
            try { onEvent(JSON.parse(raw)); }
            catch (e) { console.warn('unparseable event frame', raw, e); }
          }
        }
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
