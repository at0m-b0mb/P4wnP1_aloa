/* console-render-test.js -- render every console view in a DOM and assert it works.
 *
 * WHY THIS EXISTS
 *
 * `node --check` only parses. It happily accepted a helper that called itself:
 *
 *     function dataTable(...children) {
 *       return h('div.table-wrap', {...}, dataTable(...children));   // oops
 *     }
 *
 * Valid JavaScript, infinite recursion at runtime. Every table in the console
 * vanished and the only symptom was a page that rendered half of itself. The
 * same gap would hide a typo'd function name, a property read off undefined, or
 * any other runtime error inside a view.
 *
 * So: load the real console files into jsdom, stub the network with the real
 * response shapes, render each view, and fail on a throw or an empty result.
 *
 * Run via `make check-render` (jsdom is installed in a container).
 */
'use strict';

const fs = require('fs');
const path = require('path');
const { JSDOM } = require('jsdom');

const APP = path.join(__dirname, '..', 'dist/www/app');

/* Response shapes copied from proto/grpc.proto, same discipline as the mock. */
const RPC = {
  GetDeployedGadgetSetting: {
    enabled: true, vid: '0x1d6b', pid: '0x0137', manufacturer: 'M', product: 'P', serial: 'S',
    use_HID_KEYBOARD: true, use_HID_MOUSE: true, use_HID_RAW: false,
    use_RNDIS: true, use_CDC_ECM: false, use_SERIAL: false, use_UMS: false,
    rndis_settings: { host_addr: 'aa', dev_addr: 'bb' },
    cdc_ecm_settings: { host_addr: '', dev_addr: '' },
    ums_settings: { cdrom: false, file: '' },
    dev_path_hid_keyboard: '/dev/hidg0', dev_path_hid_mouse: '/dev/hidg1', dev_path_hid_raw: '',
  },
  GetWiFiState: { mode: 1, channel: 6, ssid: 'AP', currentSettings: { regulatory: 'US', name: 'n' } },
  GetBluetoothControllerInformation: { name: 'bt', short_name: 'bt', bluetooth_version: 6, is_available: true },
  GetAllDeployedEthernetInterfaceSettings: { list: [
    { name: 'usb0', mode: 2, ipAddress4: '172.16.0.1', netmask4: '255.255.255.252', settingsInUse: true },
  ] },
  ListStoredHIDScripts: { msgArray: ['a.js', 'b.js'] },
  ListStoredBashScripts: { msgArray: ['s.sh'] },
  HIDGetRunningScriptJobs: { ids: [7] },
  GetDeployedTriggerActionSet: { Name: 'default', TriggerActions: [
    { id: 1, isActive: true, oneShot: false, immutable: true, serviceStarted: {}, bashScript: { scriptName: 's.sh' } },
    { id: 2, isActive: false, oneShot: true, immutable: false, groupReceive: { groupName: 'g', value: 1 }, log: {} },
  ] },
  ListStoredMasterTemplate: { msgArray: ['initial', 'other'] },
  GetStartupMasterTemplate: { msg: 'initial' },
};

function makeDom() {
  const html = fs.readFileSync(path.join(APP, 'index.html'), 'utf8');
  /* runScripts: 'dangerously' so injected <script> elements execute in the
     window's own scope. window.eval() does NOT give the code a `window`
     binding, which the console's files rely on. */
  const dom = new JSDOM(html, {
    url: 'http://127.0.0.1:8000/app/',
    pretendToBeVisual: true,
    runScripts: 'dangerously',
  });
  const { window } = dom;

  /* jsdom does not implement matchMedia. Every browser since IE10 does, so this
     is a gap in the harness rather than something the console should guard
     against -- polyfill it here, do not weaken the product for it. */
  if (typeof window.matchMedia !== 'function') {
    window.matchMedia = q => ({
      matches: false, media: q,
      addEventListener() {}, removeEventListener() {},
      addListener() {}, removeListener() {},
      onchange: null, dispatchEvent: () => false,
    });
  }

  window.fetch = async (url, opts = {}) => {
    const u = String(url);
    const ok = body => ({
      ok: true, status: 200,
      json: async () => body,
      text: async () => JSON.stringify(body),
      headers: { get: () => 'application/json' },
      body: null,
    });
    if (u.includes('/api/auth/whoami')) return ok({ username: 'admin' });
    if (u.includes('/api/auth/login')) return ok({ token: 't'.repeat(43) });
    if (u.includes('/api/v1/events')) return { ok: true, status: 200, body: null, text: async () => '' };
    const m = u.match(/\/api\/v1\/rpc\/(\w+)/);
    if (m) return ok(RPC[m[1]] !== undefined ? RPC[m[1]] : {});
    return ok({});
  };
  window.localStorage.setItem('p4wnp1.token', 't'.repeat(43));
  window.localStorage.setItem('p4wnp1.user', 'admin');

  /* The page's own <script src> tags cannot resolve (no server), so strip them
     and inject the real file contents in the same order a browser would. */
  for (const tag of [...window.document.querySelectorAll('script[src]')]) tag.remove();

  /* Capture anything a script throws while executing. jsdom reports these on
     the virtual console rather than rethrowing, so without this a file that
     dies halfway through just leaves its consts uninitialised and the real
     cause is invisible. */
  const scriptErrors = [];
  window.addEventListener('error', e => scriptErrors.push(e.error || e.message));
  dom.virtualConsole.on('jsdomError', e => scriptErrors.push(e));

  for (const f of ['js/ui.js', 'js/api.js', 'js/app.js']) {
    const el = window.document.createElement('script');
    el.textContent = fs.readFileSync(path.join(APP, f), 'utf8');
    window.document.head.appendChild(el);
  }
  window.__scriptErrors = scriptErrors;
  return window;
}

(async () => {
  let pass = 0, fail = 0;
  const ok = m => { console.log('  \x1b[1;32mPASS\x1b[0m  ' + m); pass++; };
  const bad = (m, why) => { console.log('  \x1b[1;31mFAIL\x1b[0m  ' + m + '\n        ' + why); fail++; };

  let window;
  try {
    window = makeDom();
    const errs = window.__scriptErrors || [];
    if (errs.length) {
      bad('console scripts load without throwing',
        errs.map(e => String(e && e.stack ? e.stack.split('\n').slice(0, 3).join('\n        ') : e)).join('\n        '));
    } else {
      ok('console scripts load without throwing');
    }
  } catch (e) {
    bad('console scripts load into a DOM', e.stack.split('\n').slice(0, 3).join('\n        '));
    process.exit(1);
  }

  /* Top-level `const`/`let` in a classic script live in the global LEXICAL
     environment, not on `window` -- so window.Views is undefined even though
     Views exists. Indirect eval runs in global scope and can see them. */
  const grab = expr => window.eval(expr);

  // The console renders into #root, which renderConsole() builds.
  try {
    window.renderConsole();
    ok('renderConsole() builds the shell');
  } catch (e) {
    bad('renderConsole() builds the shell', e.stack.split('\n').slice(0, 3).join('\n        '));
  }

  let viewNames = [];
  try {
    viewNames = grab('Object.keys(Views)');
  } catch (e) {
    bad('Views object is reachable', String(e.message));
  }
  if (!viewNames.length) bad('Views object is reachable', 'no views found');
  else ok(`found ${viewNames.length} views: ${viewNames.join(', ')}`);

  for (const name of viewNames) {
    try {
      await grab(`Views[${JSON.stringify(name)}]()`);
      const view = window.document.querySelector('#view');
      const nodes = view ? view.querySelectorAll('*').length : 0;
      if (nodes < 8) bad(`view "${name}" renders`, `only ${nodes} nodes -- looks empty`);
      else ok(`view "${name}" renders (${nodes} nodes)`);
    } catch (e) {
      bad(`view "${name}" renders`, String(e.message) + '\n        ' +
        String(e.stack).split('\n').slice(1, 3).join('\n        '));
    }
  }

  // The helpers that a view failure would otherwise mask.
  try {
    const t = grab("dataTable(h('tbody'))");
    if (t && t.querySelector && t.querySelector('table.data')) ok('dataTable() wraps a real table');
    else bad('dataTable() wraps a real table', 'no table.data inside the wrapper');
  } catch (e) {
    bad('dataTable() wraps a real table', String(e.message));
  }

  console.log(`\n  ---- ${pass} passed, ${fail} failed ----`);
  process.exit(fail ? 1 : 0);
})();
