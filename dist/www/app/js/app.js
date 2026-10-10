/* app.js -- the P4wnP1 operator console.
 *
 * No framework and no build step, on purpose. The smallest supported target is
 * a Pi Zero W: one 1GHz ARM11 core and 512MB of RAM, serving this over USB
 * ethernet or its own access point. Shipping a bundler toolchain and a runtime
 * framework to that device buys nothing and costs boot time, memory and the
 * ability to fix the UI in place with a text editor over SSH.
 */
'use strict';

/* Shared helpers -- h(), $, clear(), toast(), the dialogs, b64encode/b64decode
 * and the formatters -- live in ui.js, which loads first. */

/* --- turning backend errors into something you can act on ------------------
 *
 * The service's error strings are written for whoever was debugging the
 * subsystem at the time: "HIDScript not available (mouse and keyboard
 * disabled)", "couldn't find working UDC driver", "Hostapd failed to bring up
 * Access Point". Each is accurate and none tells an operator what to DO. These
 * are the failures you actually hit, with the fix attached.
 *
 * Matching is on a substring of the backend message, deliberately loose: the
 * exact wording has changed upstream before, and a translation that silently
 * stops matching is worse than none. Anything unmatched falls through to the
 * raw message rather than being swallowed. */
const ERROR_GUIDE = [
  {
    /* THE MOST COMMON ERROR IN NORMAL USE, and the one that used to arrive as
       a bare fact. api.js throws both of these with status 0: the request was
       aborted after 20 seconds, or fetch itself failed. On this device that is
       usually not a fault at all -- it is what a SUCCESSFUL deploy looks like
       when the thing being reconfigured is the link the request arrived on.
       Every page that can kill the link says so in its own banner
       (view-wifi.js, view-templates.js); the toast that appears at the moment
       it happens said nothing, so the operator either pressed Apply again --
       re-running a reconfiguration whose result they cannot see -- or
       rebooted, when the move is to reconnect and reload. */
    match: /did not answer within|could not reach the device/i,
    message: 'The device stopped answering.',
    hint: 'On this device that usually means the change WAS applied and took the link with it. '
      + 'Reload the console, reconnecting first if the address or the SSID changed -- USB answers '
      + 'on 172.16.0.1 and the access point on 172.24.0.1 unless they were moved. Send it again '
      + 'only once you know it did not land: read the setting back from its own page, or check '
      + 'the Journal.',
  },
  {
    match: /HIDScript (not available|engine disabled)|mouse and keyboard disable/i,
    message: 'No keyboard or mouse is being presented to the host.',
    hint: 'A script has nothing to type into. Enable the keyboard or mouse function under Cable and deploy.',
    action: { label: 'Open Cable', view: 'cable' },
  },
  {
    match: /find working UDC|no UDC|udc driver/i,
    message: 'The Pi has no USB device controller bound.',
    hint: 'USB gadget mode is not active, so no USB function can work. Check that config.txt contains "dtoverlay=dwc2" and cmdline.txt contains "modules-load=dwc2", then reboot.',
  },
  {
    match: /gadget .* doesn't exist|USB subsystem not available/i,
    message: 'The USB gadget is not composed.',
    hint: 'Deploy a USB composition under Cable first. If that fails too, the device is not in USB gadget mode at all.',
    action: { label: 'Open Cable', view: 'cable' },
  },
  {
    match: /reverted to old ones/i,
    message: 'That USB composition was refused, and the previous one was put back.',
    hint: 'Nothing was lost. A common cause is asking for more functions than the available USB endpoints allow -- try removing one.',
  },
  {
    match: /Hostapd failed|Error starting hostapd/i,
    message: 'The access point did not start.',
    /* NOT the regulatory domain. The generated hostapd.conf contains no
       country_code line at all -- grep the service, nothing writes one -- and
       wifiSetReg substitutes "US" when the field is empty and logs that it
       did (service/wifi.go). So an unset domain cannot be why hostapd failed,
       and sending operators to go and set one wasted their time. The domain
       still matters indirectly: hostapd will not bring an AP up on a channel
       the domain in force forbids. */
    hint: 'Usually wlan0 is rfkill-blocked, already in use by wpa_supplicant, or the '
      + 'configured channel is not permitted by the regulatory domain currently in force.',
    action: { label: 'Open WiFi', view: 'wifi' },
  },
  {
    match: /WiFi subsystem is unavailable/i,
    message: 'This device has no usable WiFi.',
    hint: 'Either the board has no WiFi, wlan0 did not appear at boot, or hostapd and wpa_supplicant are not installed. Everything else on the device still works.',
  },
  {
    match: /no BSS configurations provided/i,
    message: 'Station mode needs a network to join.',
    hint: 'You set WiFi to station mode but listed no SSID to connect to.',
  },
  {
    match: /outside the allowed directories|must be relative/i,
    message: 'That file path is not allowed.',
    hint: 'The device only reads and writes inside its own script folders and /tmp. This is a guard against reading arbitrary files over the API.',
  },
  {
    match: /key exists already/i,
    message: 'Something is already stored under that name.',
    hint: 'Pick a different name, or delete the existing one first.',
  },
  {
    match: /exceeds the .* byte limit|negative read/i,
    message: 'That read was refused as unreasonable.',
    hint: 'The file is probably too large to open in the console. Fetch it over SSH instead.',
  },
  {
    match: /Couldn't load any language map/i,
    message: 'The keyboard layout files are missing.',
    hint: 'The device cannot type without them. Reinstall, or check /usr/local/P4wnP1/keymaps exists.',
  },
  {
    match: /bluez|bluetooth mgmt-api|Newer Bluez/i,
    message: 'The Bluetooth stack is not usable.',
    hint: 'Either no controller was found, or bluetoothd is not running. Bluetooth features are unavailable; the rest of the device is unaffected.',
  },
];

/* Returns {message, hint, action} for anything thrown by the API client. */
function explainError(e, what) {
  const raw = (e && e.message) ? String(e.message) : String(e || 'unknown error');

  if (e instanceof Api.ApiError) {
    if (e.status === 403) {
      return { message: 'The device refused that request as cross-origin.',
               hint: 'Open the console directly from the device address rather than through another page or a proxy.' };
    }
    if (e.status === 415) {
      return { message: 'The device rejected the request format.', hint: raw };
    }
    if (e.status === 501) {
      return { message: 'That is not implemented on this device yet.', hint: raw };
    }
    if (e.status === 503) {
      return { message: 'That subsystem is unavailable right now.', hint: raw };
    }
  }

  for (const g of ERROR_GUIDE) {
    if (g.match.test(raw)) return { message: g.message, hint: g.hint, action: g.action };
  }

  /* Unmatched: show it, prefixed with what we were doing, and keep it short
     enough to read in a toast. A 4KB JSON blob in a toast helps nobody. */
  const trimmed = raw.length > 240 ? raw.slice(0, 240) + '...' : raw;
  return { message: (what ? 'Could not ' + what + '.' : 'That did not work.'), hint: trimmed };
}

/* Toast with guidance, and an action button when there is somewhere to go.
 *
 * Identical messages collapse onto one another. A view that loads WiFi state,
 * the AP config and the station list makes three calls that all fail for the
 * same reason on a device with no WiFi, and three stacked copies of the same
 * paragraph reads as three separate faults. */
function reportError(e, what) {
  const { message, hint, action } = explainError(e, what);
  const box = $('#toasts');
  if (!box) return;

  const key = message + '\u0000' + (hint || '');
  const existing = box.querySelector('[data-toast-key="' + CSS.escape(key) + '"]');
  if (existing) {
    // Restart its clock rather than queueing a duplicate behind it.
    clearTimeout(Number(existing.dataset.toastTimer));
    existing.dataset.toastTimer = String(
      setTimeout(() => existing.remove(), action ? 20000 : 12000));
    return;
  }

  const t = h('div.toast.toast-err', { 'data-toast-key': key },
    h('p.toast-title', message),
    hint ? h('p.toast-hint', hint) : null,
    action ? h('button.btn.btn-sm', {
      type: 'button', style: 'margin-top:8px',
      onclick: () => { t.remove(); go(action.view); },
    }, action.label) : null,
    h('button.toast-close', { type: 'button', 'aria-label': 'Dismiss', onclick: () => t.remove() }, '×'));
  box.append(t);
  t.dataset.toastTimer = String(setTimeout(() => t.remove(), action ? 20000 : 12000));
}

/* Any RPC can fail because the operator just reconfigured the very interface
   they are talking over. Report it, never swallow it, and bounce to sign-in
   only when the token itself is the problem. */
async function guard(fn, what) {
  try { return await fn(); }
  catch (e) {
    if (e instanceof Api.ApiError && e.isAuthFailure) { Api.setToken(null); renderSignIn(); return undefined; }
    reportError(e, what);
    return undefined;
  }
}

/* --------------------------------------------------------------- the theme */

const Theme = (() => {
  const KEY = 'p4wnp1.theme';
  const read = () => { try { return localStorage.getItem(KEY) || 'auto'; } catch (_) { return 'auto'; } };
  const media = window.matchMedia('(prefers-color-scheme: dark)');

  function apply() {
    const choice = read();
    const resolved = choice === 'auto' ? (media.matches ? 'dark' : 'light') : choice;
    document.documentElement.setAttribute('data-theme', resolved);
  }
  /* The *choice* is stored, not the resolved value, so "Auto" keeps following
     the OS after a restart instead of freezing to whatever it was. */
  function set(choice) { try { localStorage.setItem(KEY, choice); } catch (_) {} apply(); renderThemeSwitch(); }

  media.addEventListener('change', apply);
  return { apply, set, read };
})();

function renderThemeSwitch() {
  const host = $('#theme-switch');
  if (!host) return;
  clear(host);
  const cur = Theme.read();
  for (const [val, label] of [['light', 'Light'], ['dark', 'Dark'], ['auto', 'Auto']]) {
    host.append(h('button', {
      type: 'button', 'aria-pressed': String(cur === val),
      onclick: () => Theme.set(val),
    }, label));
  }
}

/* ------------------------------------------------------------ shared state */

const State = {
  usb: null,
  wifi: null,
  eth: null,
  bt: null,
  triggers: null,
  hidJobs: [],
  usbAttached: null,     // null = unknown; the device cannot know until an event arrives
  log: [],               // newest last, capped
  stopStream: null,
  view: 'overview',
  formDirty: false,   // true while a form has unsaved edits; blocks auto-reload
};

const LOG_CAP = 600;
const LEVEL_NAME = ['debug', 'info', 'warn', 'error'];

/* ------------------------------------------------------- the cable (signature) */

/* The USB functions a composite gadget can expose, in the order they appear to
   a host. `on` and `detail` are read from the live GadgetSettings. */
const USB_FUNCTIONS = [
  { key: 'use_HID_KEYBOARD', name: 'Keyboard', detail: s => s.dev_path_hid_keyboard,
    what: 'The host accepts anything the device types as real keystrokes. This is what HIDScript drives.' },
  { key: 'use_HID_MOUSE', name: 'Mouse', detail: s => s.dev_path_hid_mouse,
    what: 'Pointer control, including absolute positioning on Windows.' },
  { key: 'use_HID_RAW', name: 'Raw HID', detail: s => s.dev_path_hid_raw,
    what: 'A plain two-way data channel over HID. Useful for talking to software you control on the host; it types nothing by itself.' },
  { key: 'use_RNDIS', name: 'RNDIS', detail: s => s.rndis_settings && s.rndis_settings.host_addr,
    what: 'A USB network adapter that Windows picks up without a driver. Pair it with CDC ECM so one composition covers Windows, macOS and Linux.' },
  { key: 'use_CDC_ECM', name: 'CDC ECM', detail: s => s.cdc_ecm_settings && s.cdc_ecm_settings.host_addr,
    what: 'The same idea for macOS and Linux, which do not take to RNDIS. Harmless to enable alongside it.' },
  { key: 'use_SERIAL', name: 'Serial', detail: () => 'ttyGS0',
    what: 'A USB serial port. Mostly useful as a console into this device, not as something to do to the host.' },
  { key: 'use_UMS', name: 'Storage', detail: s => s.ums_settings && (s.ums_settings.file || (s.ums_settings.cdrom ? 'cdrom' : '')),
    what: 'The device appears as a USB stick or a CD-ROM, backed by an image file you choose.' },
];

/* THE USB ENDPOINT BUDGET.
 *
 * The dwc2 controller on a Pi Zero W has seven usable endpoints and each
 * function costs some. Compose more than seven and the service refuses --
 * correctly -- with "Gadget Settings consume 8 out of 7 available USB
 * Endpoints", which is true, arrives only after you have ticked the boxes
 * and confirmed a disconnect warning, and does not say what to turn off.
 *
 * Reported from hardware by an operator trying to add Storage to a device
 * that already had both network functions and a keyboard.
 *
 * Mirrored from service/SubSysUSB.go; kept in step by the endpoint-budget
 * check in tools/check-rpc-shapes.py. */
const USB_ENDPOINT_MAX = 7;
const USB_ENDPOINT_COST = {
  use_HID_KEYBOARD: 1, use_HID_MOUSE: 1, use_HID_RAW: 1,
  use_RNDIS: 2, use_CDC_ECM: 2, use_SERIAL: 2, use_UMS: 2,
};
function endpointsUsed(settings) {
  return USB_FUNCTIONS.reduce(
    (n, f) => n + (settings && settings[f.key] ? (USB_ENDPOINT_COST[f.key] || 0) : 0), 0);
}

/* --- which interface is this console reached over? ------------------------
 *
 * Cable used to answer this with /^172\.16\.0\./.test(location.hostname).
 * usbeth's address is editable on the Network page -- that is what that page
 * is for -- and the shipped usbeth is a /30, so the test was both wider than
 * the network it described and wrong the moment anyone moved it. WiFi,
 * Network and Templates all answer the question properly, by comparing the
 * browser's own host against the addresses the device reports.
 *
 * consoleLink() returns:
 *   'exact'   the browser is talking to that interface's own address
 *   'network' the browser's address is inside that interface's network
 *   'other'   an exact match on a DIFFERENT managed interface, so this one
 *             is not the link carrying the page
 *   'unknown' a hostname, a proxy, a route this console cannot see, or no
 *             interface list at all
 * 'unknown' is never treated as safe: the caller takes the stronger warning,
 * because a wrong reassurance here is how a device is lost. */
function ip4ToNumber(s) {
  const m = /^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})$/.exec(String(s || '').trim());
  if (!m) return null;
  let n = 0;
  for (let i = 1; i <= 4; i++) {
    const o = Number(m[i]);
    if (o > 255) return null;
    n = (n * 256) + o;
  }
  return n;
}

function consoleLink(ifaces, name) {
  const host = String(location.hostname || '');
  const list = (ifaces && Array.isArray(ifaces.list)) ? ifaces.list.filter(i => i && typeof i === 'object') : null;
  if (!host || !list || !list.length) return 'unknown';
  const self = list.find(i => i.name === name);
  if (self) {
    if (self.ipAddress4 && String(self.ipAddress4) === host) return 'exact';
    const h = ip4ToNumber(host);
    const a = ip4ToNumber(self.ipAddress4);
    const m = ip4ToNumber(self.netmask4);
    if (h !== null && a !== null && m !== null && m !== 0 && (((h ^ a) & m) === 0)) return 'network';
  }
  for (const i of list) {
    if (i.name !== name && i.ipAddress4 && String(i.ipAddress4) === host) return 'other';
  }
  return 'unknown';
}

/* What a re-compose costs, given which link carries this page and what the
   new composition still presents. An enabled gadget with neither RNDIS nor
   CDC ECM has no USB ethernet function, so the link does not come back. */
function cableConsequence(link, draft) {
  const keepsEthernet = draft.enabled !== false && !!(draft.use_RNDIS || draft.use_CDC_ECM);
  const host = 'Any host currently attached sees every function disconnect and re-enumerate, '
    + 'exactly as if the cable had been pulled out and pushed back in.';
  const back = keepsEthernet
    ? 'It normally comes back within a few seconds -- reload the page. If it does not, reach the '
      + 'device over WiFi or the serial console.'
    : 'This composition presents neither RNDIS nor CDC ECM, so the USB ethernet link does not come '
      + 'back at all: the way in is WiFi, Bluetooth, or a serial console on ttyGS0.';
  if (link === 'exact' || link === 'network') {
    return (link === 'exact'
      ? 'You are reading this over the USB ethernet link -- ' + location.hostname
        + ' is usbeth’s own address -- and that link is one of the functions being rebuilt. '
      : 'Your address (' + location.hostname + ') is inside usbeth’s network, so this page is '
        + 'almost certainly riding the USB ethernet link, which is one of the functions being rebuilt. ')
      + 'This console WILL disconnect. ' + back + ' ' + host;
  }
  if (link === 'other') {
    return host + ' This console is not reading over usbeth, so it should survive'
      + (keepsEthernet ? '.' : ', but the USB ethernet link will not: this composition presents '
        + 'neither RNDIS nor CDC ECM.');
  }
  /* 'unknown' covers two cases -- an address that matched no reported
     interface, AND an interface list that could not be read at all -- so the
     sentence has to be true of both. Saying "does not match any interface the
     device reported" when nothing was reported is a claim about the device
     that is simply false, which is the defect this function exists to fix. */
  return 'Which interface carries this page is unknown: ' + (location.hostname || 'this address')
    + ' could not be matched against any interface this console was able to confirm. Treat it as '
    + 'if it were the USB link -- if it is, this console WILL disconnect. ' + back + ' ' + host;
}

function renderCable() {
  const s = State.usb;
  const strip = h('div.cable-strip');
  if (!s) {
    strip.append(h('div.empty', 'USB composition not loaded.'));
  } else {
    for (const fn of USB_FUNCTIONS) {
      const on = !!s[fn.key];
      let detail = '';
      try { detail = fn.detail(s) || ''; } catch (_) { detail = ''; }
      strip.append(h('div.fn' + (on ? '.fn-on' : ''),
        h('span.fn-name', fn.name),
        h('span.fn-detail', on ? (detail || 'active') : 'off')));
    }
  }

  const attached = State.usbAttached;
  const dot = attached === true ? 'dot-ok' : attached === false ? 'dot-idle' : 'dot-warn';
  const stateText = attached === true ? 'host attached'
    : attached === false ? 'no host' : 'attach state unknown';

  return h('div.cable',
    h('div.cable-head',
      h('div',
        h('span.card-title', { style: 'margin:0' }, 'What the cable presents'),
        h('div', { class: 'field-hint', style: 'margin-top:4px' },
          s && s.enabled === false
            ? 'The composite gadget is disabled, so the host sees nothing.'
            : 'Every lit segment is a device the target machine enumerates.')),
      h('span#cable-state.cable-state', h('span', { class: 'dot ' + dot, style: 'margin-right:6px' }), stateText)),
    strip,
    s ? h('div', { style: 'margin-top:16px', class: 'figures' },
      figure(s.vid || '--', 'vendor id'),
      figure(s.pid || '--', 'product id'),
      figure(String(USB_FUNCTIONS.filter(f => s[f.key]).length), 'functions'),
    ) : null,
    s ? h('p', { class: 'field-hint', style: 'margin-top:12px' },
      'Identity: ', h('span.mono', [s.manufacturer, s.product].filter(Boolean).join(' / ') || 'unset'),
      s.serial ? ' serial ' : '', s.serial ? h('span.mono', s.serial) : '') : null,
  );
}

/* "Save what is running, under a name."
 *
 * Nine Store* RPCs are implemented in the service. Until this helper existed
 * exactly one of them -- StoreDeployedUSBSettings -- had a caller anywhere:
 * not in the CLI (cmd_template.go offers deploy and list), not on the panel
 * (oled/client.go's kindMeta has List/Deploy/Delete columns and no Store, so
 * storing is not merely unwired there but inexpressible). The template
 * library was read-only in practice: an operator could deploy and delete
 * whatever init.db happened to ship and could not keep anything they built.
 *
 * Two places promised otherwise. The console's own Overview primer says
 * "Store a Loadout when you have something worth keeping", and first boot
 * prints a WiFi procedure ending "save as template -> set as default" for
 * the one task it reports as still outstanding. Both named a screen that did
 * not exist.
 *
 * The StoreDeployed* family takes a name and nothing else -- the service
 * snapshots the running configuration itself (rpc_server.go:681 for WiFi,
 * :359 for Bluetooth) -- so there is no settings payload to assemble here
 * and no way for this to store something other than what is actually live.
 */
function storeDeployedButton({ rpc, title, placeholder, hint, onDone }) {
  return h('button.btn.btn-quiet', {
    onclick: async () => {
      const name = await promptValue({
        title,
        label: 'Template name',
        placeholder,
        hint,
        validate: v => v ? null : 'Give the template a name.',
      });
      if (!name) return;
      /* guard() returns undefined on failure; an Empty reply is {} , which is
         truthy, but a bare `if (r)` would still be wrong if the reply were
         ever a falsy scalar. Compare against undefined explicitly. */
      const r = await guard(() => Api.rpc(rpc, { msg: name }), 'store the template');
      if (r !== undefined) { toast('Stored as "' + name + '".'); if (onDone) onDone(); }
    },
  }, 'Store as template');
}

function figure(n, label) {
  return h('div.figure', h('span.figure-n', n), h('span.figure-l', label));
}

/* ---------------------------------------------------------------- the views */

const Views = {};

/* --- Overview: what this device is, and what it is doing right now -------
 *
 * This view exists because the console used to open on Cable, which shows seven
 * checkboxes labelled RNDIS, CDC ECM and Raw HID and explains none of them. A
 * competent security person who has not used a P4wnP1 before has no way in
 * from there. Everything here is assembled from RPCs that already existed --
 * no new backend capability. */

const CONCEPTS = [
  {
    term: 'The cable',
    view: 'cable',
    plain: 'What the device pretends to be when you plug it into a computer.',
    detail: 'A Pi can present itself over one USB cable as several devices at once: a keyboard, a mouse, a network adapter, a USB stick, a serial port. The target machine sees ordinary hardware and sets it up without asking. Choosing which of those to present is the first thing you do.',
  },
  {
    term: 'Keystrokes',
    view: 'keystrokes',
    plain: 'Typing into the target, from a script.',
    detail: 'Because the target believes a real keyboard is attached, anything the device types is accepted as if you had typed it. HIDScript is JavaScript that runs on the device and drives that keyboard, with control over speed, layout and timing.',
  },
  {
    term: 'Reflexes',
    view: 'reflexes',
    plain: 'Rules that make the device act on its own.',
    detail: 'A reflex is "when X happens, do Y" -- when a host attaches, when an access point starts, when a DHCP lease is granted. This is what lets the device work with nobody at the keyboard, which is the point of leaving one plugged in.',
  },
  {
    term: 'Loadouts',
    view: 'loadouts',
    plain: 'A whole configuration, saved and recalled as one.',
    detail: 'USB composition, WiFi, Bluetooth, networking and reflexes together under one name. Build a setup once, store it, and deploy the same thing again later or at boot.',
  },
];

function statusTile(value, label, state) {
  return h('div.tile' + (state ? '.tile-' + state : ''),
    h('span.tile-value', value),
    h('span.tile-label', label));
}

Views.overview = async function () {
  const main = clear($('#view'));
  main.append(pageHead('Overview', 'What this device is presenting, and what it is doing right now.'));

  /* The primer is dismissible and the choice is remembered, so it helps a
     newcomer without nagging someone on their fiftieth engagement. */
  let dismissed = false;
  try { dismissed = localStorage.getItem('p4wnp1.primerDismissed') === '1'; } catch (_) {}
  if (!dismissed) main.append(renderPrimer());

  const statusHost = h('div.card',
    h('h2.card-title', 'Right now'),
    h('div#overview-tiles.tiles', h('div.empty', 'Reading device state...')));
  main.append(statusHost);

  main.append(h('div.card',
    h('h2.card-title', 'The four things this device does'),
    h('div.concepts', ...CONCEPTS.map(c =>
      h('details.concept',
        h('summary',
          h('span.concept-term', c.term),
          h('span.concept-plain', c.plain)),
        h('div.concept-detail',
          h('p', c.detail),
          h('button.btn.btn-sm', {
            type: 'button', onclick: () => go(c.view),
          }, 'Open ' + c.term)))))));

  /* Every call is independent; one failure must not blank the whole view. */
  const [usb, wifi, eth, triggers, jobs] = await Promise.all([
    Api.rpc('GetDeployedGadgetSetting').catch(() => null),
    Api.rpc('GetWiFiState').catch(() => null),
    Api.rpc('GetAllDeployedEthernetInterfaceSettings').catch(() => null),
    Api.rpc('GetDeployedTriggerActionSet').catch(() => null),
    Api.rpc('HIDGetRunningScriptJobs').catch(() => null),
  ]);
  State.usb = usb || State.usb;

  const tiles = $('#overview-tiles');
  if (!tiles) return;
  clear(tiles);

  const fnCount = usb ? USB_FUNCTIONS.filter(f => usb[f.key]).length : null;
  const attached = State.usbAttached;
  const armed = triggers ? ((triggers.TriggerActions || []).filter(t => t.isActive).length) : null;
  const running = jobs ? (jobs.ids || []).length : null;
  const ifaces = eth ? ((eth.list || []).filter(i => i.settingsInUse).length) : null;

  tiles.append(
    statusTile(
      attached === true ? 'Attached' : attached === false ? 'No host' : 'Unknown',
      'USB host',
      attached === true ? 'ok' : attached === false ? 'idle' : 'warn'),
    statusTile(fnCount === null ? '--' : String(fnCount), 'USB functions presented'),
    statusTile(wifi && wifi.ssid ? wifi.ssid : '--', 'WiFi'),
    statusTile(ifaces === null ? '--' : String(ifaces), 'Interfaces up'),
    statusTile(armed === null ? '--' : String(armed), 'Reflexes armed'),
    statusTile(running === null ? '--' : String(running), 'Scripts running',
      running ? 'ok' : null));

  if (usb) main.insertBefore(renderCable(), statusHost.nextSibling);

  /* Say the obvious thing out loud when the device cannot do anything useful,
     rather than leaving the operator to infer it from a row of zeroes. */
  if (usb && usb.enabled === false) {
    main.insertBefore(h('div.banner.banner-danger',
      h('p.banner-title', 'The USB gadget is switched off'),
      h('p', 'The target machine sees nothing at all when the cable goes in. Turn it on under Cable and deploy.'),
      h('button.btn.btn-sm', { style: 'margin-top:10px', onclick: () => go('cable') }, 'Open Cable')),
      statusHost);
  }
};

function renderPrimer() {
  const card = h('div.card.primer',
    h('div.card-head',
      h('h2.card-title', { style: 'margin:0' }, 'New to this device?'),
      h('button.btn.btn-sm.btn-quiet', {
        type: 'button',
        onclick: () => {
          try { localStorage.setItem('p4wnp1.primerDismissed', '1'); } catch (_) {}
          card.remove();
          toast('Hidden. It is still in the Overview if you clear site data.');
        },
      }, 'Dismiss')),
    h('p.primer-lede',
      'P4wnP1 is a Raspberry Pi that pretends to be USB devices. Plug it into a computer and ' +
      'that computer sets it up as a keyboard, a network adapter or a USB stick, without asking anyone. ' +
      'What you do with that is up to you.'),
    h('ol.primer-steps',
      h('li',
        h('strong', 'Decide what the cable presents.'),
        ' Under Cable, tick the functions you want and deploy. A keyboard lets you type into the host; ' +
        'a network adapter lets you route its traffic.'),
      h('li',
        h('strong', 'Write what should happen.'),
        ' Under Keystrokes, a script types into the host. The reference on that page lists every ' +
        'function you can call.'),
      h('li',
        h('strong', 'Make it automatic.'),
        ' Under Reflexes, bind that script to an event -- "when a USB host attaches, run this" -- so ' +
        'the device works with nobody present.')),
    h('p.field-hint',
      'Everything you deploy is live immediately and not saved. Store a Loadout when you have ' +
      'something worth keeping.'));
  return card;
}

/* --- Cable: USB gadget composition ------------------------------------- */

Views.cable = async function () {
  const [usb, ifaces] = await Promise.all([
    guard(() => Api.rpc('GetDeployedGadgetSetting'), 'read USB settings'),
    /* Only to work out whether this page rides the USB link. Unguarded: a
       device that cannot answer it is not a reason for a toast, and
       consoleLink() reports 'unknown' rather than guessing. */
    Api.rpc('GetAllDeployedEthernetInterfaceSettings').catch(() => null),
  ]);
  State.usb = usb;
  const s = State.usb;
  const link = consoleLink(ifaces, 'usbeth');
  const main = clear($('#view'));

  main.append(pageHead('Cable',
    'The USB side of the device: what it claims to be, and which functions it exposes to a host when the cable goes in.'));
  main.append(renderCable());

  if (!s) return;

  const draft = JSON.parse(JSON.stringify(s));
  State.formDirty = false;
  /* A half-finished edit must not follow the operator off this page.
     State.formDirty is ONE global: left true, every other view answers the
     next state-change event with a "The device changed these settings.
     [Reload]" toast and a stale page, on a page with no form to protect. The
     other three editors clear it on teardown (view-wifi.js, view-network.js,
     view-bluetooth.js); Cable was the one that did not. */
  onViewTeardown(() => { State.formDirty = false; });
  const markDirty = () => { State.formDirty = true; };
  const form = h('div.card',
    h('h2.card-title', 'Composition'),
    h('div.grid-2',
      h('div',
        h('p.field-hint', { style: 'margin:-4px 0 14px' },
          'Each of these is a separate device the host sets up. You can present several at once.'),
        ...USB_FUNCTIONS.map(fn => h('div.fn-choice',
          h('label.check',
            h('input', {
              type: 'checkbox', checked: !!draft[fn.key],
              onchange: e => { draft[fn.key] = e.target.checked; markDirty(); },
            }),
            h('span.fn-choice-name', fn.name)),
          h('p.fn-choice-what', fn.what))),
        h('label.check',
          h('input', {
            type: 'checkbox', checked: draft.enabled !== false,
            onchange: e => { draft.enabled = e.target.checked; markDirty(); },
          }),
          h('span', 'Gadget enabled'))),
      h('div',
        textField('Vendor ID', draft.vid, v => { draft.vid = v; markDirty(); }, '0x1d6b'),
        textField('Product ID', draft.pid, v => { draft.pid = v; markDirty(); }, '0x0137'),
        textField('Manufacturer', draft.manufacturer, v => { draft.manufacturer = v; markDirty(); }),
        textField('Product', draft.product, v => { draft.product = v; markDirty(); }),
        textField('Serial', draft.serial, v => { draft.serial = v; markDirty(); }))),
    h('div.btn-row', { style: 'margin-top:8px' },
      h('button.btn.btn-primary', {
        onclick: async (e) => {
          /* Re-composing the gadget tears down and rebuilds the USB
             functions. If the console is reached over USB ethernet -- the
             normal case -- this request is what kills the connection it
             arrived on. Say so before doing it. */
          /* Refuse here, before the disconnect warning. Letting someone
             confirm "this console WILL disconnect" for a composition that
             cannot deploy is a cruel way to find out. */
          const used = endpointsUsed(draft);
          if (used > USB_ENDPOINT_MAX) {
            await showDetail({
              title: 'Too many USB functions',
              body: 'This composition needs ' + used + ' USB endpoints and the board has '
                  + USB_ENDPOINT_MAX + '.\n\n'
                  + 'Network and storage functions cost 2 endpoints each; keyboard, mouse and '
                  + 'raw HID cost 1.\n\n'
                  + 'To add Storage or Serial, turn off RNDIS or CDC ECM first.',
            });
            return;
          }
          /* The SECOND composition the service refuses, for the same reason
             the endpoint budget is checked above: an enabled gadget with
             nothing ticked is rejected by SubSysUSB.go:318 with "if the
             composite gadget isn't disabled, as least one function has to be
             enabled" -- typo included -- and the operator would be handed
             that sentence only after confirming a disconnect warning. */
          if (draft.enabled !== false && !USB_FUNCTIONS.some(f => draft[f.key])) {
            await showDetail({
              title: 'An enabled gadget has to present something',
              body: 'No function is ticked and the gadget is enabled. The service refuses that '
                  + 'composition.\n\n'
                  + 'Tick at least one function, or untick "Gadget enabled" -- that is the '
                  + 'supported way to present nothing at all to the host.',
            });
            return;
          }
          const yes = await confirmAction({
            title: 'Re-compose the USB gadget?',
            body: 'The device tears down every USB function and rebuilds it with these settings.',
            consequence: cableConsequence(link, draft),
            confirmLabel: 'Deploy',
          });
          if (!yes) return;
          e.target.disabled = true;
          const res = await guard(() => Api.rpc('DeployGadgetSetting', draft), 'deploy USB settings');
          e.target.disabled = false;
          if (res) { State.formDirty = false; toast('USB composition deployed.'); Views.cable(); }
        },
      }, 'Deploy composition'),
      h('button.btn', { onclick: () => { State.formDirty = false; Views.cable(); } }, 'Discard changes'),
      storeDeployedButton({
        rpc: 'StoreDeployedUSBSettings',
        title: 'Store this USB composition',
        placeholder: 'hid-and-storage',
        hint: 'Reusable later from Loadouts, and as part of a whole-device template.',
      })));
  main.append(form);
};

/* --- Radio: WiFi + Bluetooth ------------------------------------------- */


/* Views.radio is gone.
   Every card it held now has a page of its own that can actually CHANGE the
   thing it describes: WiFi (view-wifi.js), Bluetooth and its pairing agent
   (view-bluetooth.js), and the interface addresses (view-network.js). Keeping
   a read-only copy beside them meant two screens disagreeing about the same
   device -- and the copy was wrong twice over, which is how this was noticed:
   WIFI_MODE was indexed with an enum NAME so it printed nothing it defined,
   and it told operators hostapd refuses to start without a regulatory domain,
   which is not true (see the note in the error guide above).
   #radio still resolves, to WiFi -- see go(). */


/* --- Keystrokes: HIDScript -------------------------------------------- */

/* The complete HIDScript API, read off the vm.Set() calls in hid/controller.go.
 *
 * This exists because there was previously NO way to discover it. The functions
 * are injected into an otto VM at runtime, so they appear in no file the
 * operator can open, and nothing in the product named them. You had to read the
 * Go source to find out that typingSpeed() or waitLED() existed at all. */
const HID_API = [
  { group: 'Keyboard', fns: [
    { sig: `type("text")`, doc: 'Type a string through the emulated keyboard. \\n sends Return.' },
    { sig: `press("CTRL ALT DELETE")`, doc: 'Press a key combination once. Space-separated key names.' },
    { sig: `layout("US")`, doc: 'Select the keyboard layout the target host is using. Wrong layout means wrong characters.' },
    { sig: `typingSpeed(ms, jitter)`, doc: 'Delay between keystrokes, plus a random variation. Constant-rate typing outruns some applications.' },
  ]},
  { group: 'Mouse', fns: [
    { sig: `move(dx, dy)`, doc: 'Move the pointer by a relative offset.' },
    { sig: `moveStepped(dx, dy)`, doc: 'Same, but in smaller interpolated steps.' },
    { sig: `moveTo(x, y)`, doc: 'Move to an absolute position. Needs the absolute-mouse gadget and works best on Windows.' },
    { sig: `button(BT1)`, doc: 'Hold a button down. BT1, BT2, BT3, or BTNONE to release.' },
    { sig: `click(BT1)`, doc: 'Press and release.' },
    { sig: `doubleClick(BT1)`, doc: 'Two clicks in quick succession.' },
  ]},
  { group: 'Timing and feedback', fns: [
    { sig: `delay(ms)`, doc: 'Wait. The most common fix when a payload races the target.' },
    { sig: `waitLED(mask)`, doc: 'Block until a keyboard LED changes. The host can signal back this way: NUM, CAPS, SCROLL, COMPOSE, KANA, ANY, ANY_OR_NONE.' },
    { sig: `waitLEDRepeat(mask, count, maxPause, timeout)`, doc: 'Wait for the host to toggle an LED repeatedly, as a crude input channel.' },
  ]},
];

/* Set by a save, consumed by the next render of Keystrokes. */
let pendingSource = null;

const SAMPLE_SCRIPT = `// HIDScript runs ON the device and types into the attached host.
// Nothing happens until you press Run.

layout('US');            // must match the TARGET's keyboard layout
typingSpeed(80, 20);     // 80ms between keys, +/- 20ms of jitter

delay(500);              // give the host a moment to notice the keyboard
type('hello from P4wnP1\\n');
`;

/* Decode a stored script. FSReadFile needs an explicit length -- with len
 * unset it returns readCount 0 and no data -- so the size comes from
 * FSGetFileInfo first. */
async function readStoredScript(name) {
  const info = await guard(
    () => Api.rpc('FSGetFileInfo', { path: '/usr/local/P4wnP1/HIDScripts/' + name }),
    'read script info');
  if (!info) return null;
  const size = Number(info.size) || 0;
  /* null, not ''. The caller's guard is `if (src !== null)`, so returning an
     empty string loaded the empty file INTO the editor and blanked whatever
     the operator had typed, under a toast that talked about the file rather
     than about their work. */
  if (size === 0) { toast('"' + name + '" is empty, so the editor was left as it is.', true); return null; }
  if (size > 512 * 1024) { toast('"' + name + '" is too large to open here (' + fmtBytes(size) + ').', true); return null; }
  const res = await guard(
    () => Api.rpc('FSReadFile', { folder: 2 /* HID_SCRIPTS */, filename: name, start: 0, len: size }),
    'read script');
  if (!res) return null;
  try { return b64decode(res.data); }
  catch (e) { toast('Could not decode "' + name + '": ' + e.message, true); return null; }
}

/* Write the editor contents to a temp file and start a job.
 *
 * Three calls, and every one of them was wrong before:
 *   - FSCreateTempDirOrFile takes {dir, prefix, onlyFolder}; `dir` is a STRING
 *     path, and passing a boolean made protojson reject the whole request.
 *   - FSWriteFile's payload field is `data`, not `content`, and `filename` must
 *     be RELATIVE to the chosen folder -- an absolute path is refused.
 *   - the response field is `resultPath`.
 * Verified against a running service before being written this way. */
async function runScriptSource(src, timeoutSeconds) {
  const tmp = await guard(
    () => Api.rpc('FSCreateTempDirOrFile', { dir: '', prefix: 'console', onlyFolder: false }),
    'create a temporary file');
  if (!tmp || !tmp.resultPath) return null;

  const absPath = tmp.resultPath;
  const base = absPath.slice(absPath.lastIndexOf('/') + 1);

  const wrote = await guard(() => Api.rpc('FSWriteFile', {
    folder: 0 /* TMP */, filename: base, data: b64encode(src), append: false,
  }), 'write the script');
  if (wrote === undefined) return null;

  return guard(() => Api.rpc('HIDRunScriptJob', {
    scriptPath: absPath, timeoutSeconds: Number(timeoutSeconds) || 0,
  }), 'start the script');
}

Views.keystrokes = async function () {
  const main = clear($('#view'));
  main.append(pageHead('Keystrokes',
    'HIDScript runs on the device and drives the emulated keyboard and mouse against whatever host the cable is plugged into.'));

  /* Running a script with no HID function enabled fails with a message from
     deep in the HID layer. Say it here instead, where it is fixable.
     State.usb is only populated by Overview and Cable, so deep-linking or
     reloading straight onto this view used to skip the warning entirely --
     exactly the case where a newcomer most needs it. Fetch it if absent. */
  if (!State.usb) {
    State.usb = await Api.rpc('GetDeployedGadgetSetting').catch(() => null);
  }
  if (State.usb && !State.usb.use_HID_KEYBOARD && !State.usb.use_HID_MOUSE) {
    main.append(h('div.banner.banner-danger',
      h('p.banner-title', 'No keyboard or mouse is being presented'),
      h('p', 'A script has nothing to type into. Enable the keyboard or mouse function under Cable and deploy, then come back.'),
      h('button.btn.btn-sm', { style: 'margin-top:10px', onclick: () => go('cable') }, 'Open Cable')));
  }

  const stored = await guard(() => Api.rpc('ListStoredHIDScripts'), 'list stored scripts');
  const names = (stored && stored.msgArray) || [];

  const editor = h('textarea', {
    rows: '16', spellcheck: 'false', 'aria-label': 'HIDScript source',
    wrap: 'off',
  });
  /* wrap: 'off' keeps a payload's line structure visible, which is right on a
     wide screen and unusable on a phone: inside a card at 375px the content
     box is about 42 monospace columns, and HIDScript lines routinely run far
     longer than that -- an encoded one-liner inside type('...') is hundreds
     of characters. The operator could see only a 42-column window of it and
     had to drag the textarea sideways with a thumb while the page scrolled
     too. So: soft wrap where the screen is narrow, and a toggle either way.
     The attribute is not reliably re-applied once the element is in the
     document, so the rendering is pinned from CSS as well. */
  const narrow = (() => {
    try { return window.matchMedia('(max-width: 560px)').matches; } catch (_) { return false; }
  })();
  const setWrap = (on) => {
    editor.wrap = on ? 'soft' : 'off';
    editor.style.whiteSpace = on ? 'pre-wrap' : 'pre';
  };
  setWrap(narrow);
  /* A save re-renders this page; pendingSource carries the operator's work
     across it so the demo script does not silently replace a payload. */
  editor.value = pendingSource !== null ? pendingSource : SAMPLE_SCRIPT;
  pendingSource = null;
  /* What the editor holds that the device also holds: the demo script on a
     fresh render, or the source that was just saved. Anything else in the
     textarea exists nowhere but this tab -- nothing in the console persists a
     draft -- so anything that would overwrite it asks first. */
  let baseline = editor.value;

  const timeoutInput = h('input', { type: 'number', value: '30', min: '0', max: '3600' });
  const runBtn = h('button.btn.btn-primary', { type: 'button' }, 'Run on the host');

  const picker = h('select', {
    'aria-label': 'Load a stored script',
    onchange: async e => {
      const n = e.target.value;
      /* Reset the select first, so bailing out below does not leave it
         showing a script that was never loaded. */
      e.target.value = '';
      if (!n) return;
      /* On a phone this select is a full-screen list one tap from the editor,
         and loading used to assign straight over the textarea. A stray tap
         discarded a payload that existed nowhere else, with a toast reading
         'Loaded "x".' */
      if (editor.value.trim() && editor.value !== baseline) {
        const yes = await confirmAction({
          title: 'Discard what is in the editor?',
          body: 'The source in the editor has not been saved to the device.',
          consequence: 'Loading "' + n + '" writes over it. The console keeps no draft and no '
            + 'undo, so unsaved HIDScript exists only in this textarea.',
          confirmLabel: 'Discard and load',
          danger: true,
        });
        if (!yes) return;
      }
      const src = await readStoredScript(n);
      if (src !== null) { editor.value = src; baseline = src; toast('Loaded "' + n + '".'); }
    },
  }, h('option', { value: '' }, names.length ? 'Load a stored script...' : 'No stored scripts'),
     ...names.map(n => h('option', { value: n }, n)));

  runBtn.addEventListener('click', async () => {
    const src = editor.value;
    if (!src.trim()) { toast('There is nothing to run.', true); return; }
    const t = Number(timeoutInput.value);
    if (!Number.isFinite(t) || t < 0) { toast('Timeout must be zero or a positive number of seconds.', true); return; }

    runBtn.disabled = true;
    clear(runBtn).append(h('span.spinner'), ' Starting');
    const job = await runScriptSource(src, t);
    runBtn.disabled = false;
    clear(runBtn).append('Run on the host');
    if (job) { toast('Started job ' + job.id + '.'); refreshJobs(); }
  });

  main.append(h('div.card',
    h('div.card-head',
      h('h2.card-title', { style: 'margin:0' }, 'Script'),
      picker),
    h('label.field', { style: 'margin-top:16px' },
      h('span.field-label', 'HIDScript source'),
      editor,
      h('span.field-hint', 'Plain JavaScript, plus the functions in the reference below.')),
    /* Outside the label above, because a label inside a label is not valid
       HTML and a click would land on the wrong control. */
    h('div.btn-row', { style: 'margin:-8px 0 6px' },
      h('label.check', { style: 'margin-bottom:0' },
        h('input', {
          type: 'checkbox', checked: narrow,
          onchange: e => setWrap(e.target.checked),
        }),
        h('span', 'Wrap long lines'))),
    h('div.row',
      h('label.field', { style: 'max-width:170px' },
        h('span.field-label', 'Timeout (seconds)'),
        timeoutInput,
        h('span.field-hint', '0 runs with no time limit')),
      h('div.btn-row', { style: 'padding-top:26px' },
        runBtn,
        h('button.btn', {
          type: 'button',
          onclick: async () => {
            /* The list could not be read at all -- distinct from "the list is
               empty", because only one of those two means the name is free. */
            const namesKnown = !!(stored && Array.isArray(stored.msgArray));
            const name = await promptValue({
              title: 'Save to the device',
              /* Prefilling the likeliest collision is how the destructive
                 case used to be the default one: FSWriteFile opens with
                 O_TRUNC and there is no file-delete or file-restore RPC, so
                 the old payload.js was simply gone. */
              value: names.includes('payload.js') ? '' : 'payload.js',
              label: 'File name',
              hint: 'Stored in /usr/local/P4wnP1/HIDScripts and listed above. A name already in '
                + 'that list is replaced, and only after a confirmation -- saving over a stored '
                + 'script keeps no copy.',
              validate: v => {
                if (!v) return 'Give the file a name.';
                if (!/^[A-Za-z0-9._-]+$/.test(v)) return 'Letters, digits, dot, dash and underscore only.';
                if (!v.endsWith('.js')) return 'Use a .js extension.';
                if (v.startsWith('.')) return 'The name cannot start with a dot.';
                /* A collision is NOT refused here, it is confirmed below.
                   Refusing it looks safer and is worse: the picker above
                   loads a stored script INTO this editor, so editing one and
                   saving it back is the normal use of this page, and there is
                   no FS delete RPC -- a refusal makes HIDScripts append-only
                   from the console, so the operator either reaches for SSH or
                   leaves payload2.js, payload3.js behind forever with no way
                   to clean them up. Every other store path in this console
                   confirms rather than refuses for exactly that reason
                   (view-templates.js nameIsFree, view-system.js's backup,
                   which says "There is no way to recover it from here" and
                   still offers the button). What was actually broken was the
                   silence, and a dialog fixes that. */
                return null;
              },
            });
            if (!name) return;
            if (!namesKnown) {
              const ok = await confirmAction({
                title: 'Save as "' + name + '"?',
                body: 'The list of stored scripts could not be read, so whether that name is '
                  + 'already in use is unknown.',
                consequence: 'If it is, the device writes over the existing script without asking '
                  + 'and without keeping a copy. There is no file-restore in this console.',
                confirmLabel: 'Save it anyway',
                danger: true,
              });
              if (!ok) return;
            } else if (names.includes(name)) {
              const ok = await confirmAction({
                title: 'Replace the stored script "' + name + '"?',
                body: 'A script is already stored under that name. This is also how a script '
                  + 'loaded from the picker above is saved back after an edit.',
                consequence: 'FSWriteFile opens the file with O_TRUNC, so what is on the device '
                  + 'is replaced and no copy is kept. This console has no file-restore and no '
                  + 'file-delete, so the only way back to the old contents is a copy you hold '
                  + 'outside this tab.',
                confirmLabel: 'Replace it',
                danger: true,
              });
              if (!ok) return;
            }
            const r = await guard(() => Api.rpc('FSWriteFile', {
              folder: 2 /* HID_SCRIPTS */, filename: name,
              data: b64encode(editor.value), append: false,
            }), 'save the script');
            if (r !== undefined) {
              toast('Saved as "' + name + '".');
              /* Carry the source across the re-render.
                 Views.keystrokes() rebuilds the textarea and seeds it with
                 SAMPLE_SCRIPT, so saving replaced the operator's payload
                 with the demo script, in place, under a success toast. The
                 usual next move is "Run on the host" -- which would then have
                 typed `hello from P4wnP1` into whatever the cable is plugged
                 into. Keystrokes delivered to a host cannot be withdrawn;
                 this page's own Cancel-all dialog says so. */
              pendingSource = editor.value;
              Views.keystrokes();
            }
          },
        }, 'Save to device'),
        h('button.btn.btn-danger', {
          type: 'button',
          onclick: async () => {
            if (!State.hidJobs.length) { toast('Nothing is running.'); return; }
            const yes = await confirmAction({
              title: 'Cancel every running script?',
              body: 'There ' + (State.hidJobs.length === 1 ? 'is 1 job' : 'are ' + State.hidJobs.length + ' jobs') + ' running.',
              consequence: 'Each stops wherever it has got to. Anything already typed into the host stays typed -- cancelling does not undo keystrokes.',
              confirmLabel: 'Cancel all',
              danger: true,
            });
            if (!yes) return;
            const r = await guard(() => Api.rpc('HIDCancelAllScriptJobs'), 'cancel jobs');
            if (r !== undefined) { toast('Cancelled.'); refreshJobs(); }
          },
        }, 'Cancel all')))));

  const jobsCard = h('div.card',
    h('div.card-head',
      h('h2.card-title', { style: 'margin:0' }, 'Jobs'),
      h('button.btn.btn-sm.btn-quiet', { type: 'button', onclick: () => refreshJobs() }, 'Refresh')),
    h('div#jobs', h('div.empty', 'None running.')));
  main.append(jobsCard);
  refreshJobs();

  /* A script finishes on the device with no event the console can see, so
     without this the job list shows a job as running until you navigate away
     and back -- and the Cancel button next to it does nothing because the job
     is already gone. Poll while anything is running, and stop when it is not,
     so an idle console is not making a request every three seconds forever. */
  const poll = setInterval(() => {
    if (State.view !== 'keystrokes') return;
    if (!State.hidJobs.length) return;
    refreshJobs();
  }, 3000);
  onViewTeardown(() => clearInterval(poll));

  main.append(renderHidReference());
};

/* The function reference. Collapsed by default so it does not push the editor
   off screen, but present on the same page -- the point is that you never have
   to leave to find out what you can call. */
function renderHidReference() {
  const body = h('div', { style: 'margin-top:4px' },
    ...HID_API.map(g => h('div', { style: 'margin-bottom:20px' },
      h('h3.ref-group', g.group),
      h('dl.ref-list',
        ...g.fns.flatMap(f => [
          h('dt', h('code', f.sig)),
          h('dd', f.doc),
        ])))),
    h('p.field-hint',
      'Key names for press(): CTRL ALT SHIFT GUI ENTER ESCAPE TAB SPACE BACKSPACE DELETE ' +
      'INSERT HOME END PAGEUP PAGEDOWN UP DOWN LEFT RIGHT CAPSLOCK NUMLOCK SCROLLLOCK ' +
      'PRINTSCR PAUSE F1-F24, and any single character.'),
    h('p.field-hint',
      'DuckyScript payloads convert with: P4wnP1_cli ducky convert payload.txt -o payload.js'));

  const details = h('details.ref', h('summary', 'HIDScript reference'), body);
  return h('div.card', details);
}

async function refreshJobs() {
  const host = $('#jobs');
  if (!host) return;
  const r = await guard(() => Api.rpc('HIDGetRunningScriptJobs'), 'list jobs');
  State.hidJobs = (r && r.ids) || [];
  clear(host);
  if (!State.hidJobs.length) { host.append(h('div.empty', 'None running.')); return; }
  host.append(dataTable(
    h('thead', h('tr', h('th', 'Job'), h('th', 'Actions'))),
    h('tbody', ...State.hidJobs.map(id => h('tr',
      h('td.mono', String(id)),
      h('td', h('div.btn-row',
        h('button.btn.btn-sm', {
          type: 'button',
          onclick: async () => {
            /* NEVER ask for the result of a job that is still running.
             *
             * HIDGetScriptJobResult WAITS on the job and takes the request
             * context, and the service turns a cancelled request into
             * job.Cancel(), which interrupts the Otto VM. api.js aborts
             * every RPC after 20 seconds. So clicking "Result" on a payload
             * with more than 20 seconds left to run does not read it --
             * it KILLS it, part-way through typing into the target host,
             * and then reports "the device did not answer within 20
             * seconds", blaming the network for our own deadline.
             *
             * Same bug the OLED panel had; the panel's gate is in
             * oled/screens.go (RunningView polls JobRunning and only
             * collects once the id is gone). This is that gate.
             *
             * Re-listing is cheap and returns at once. */
            const live = await guard(() => Api.rpc('HIDGetRunningScriptJobs'), 'list jobs');
            if (live && (live.ids || []).includes(id)) {
              await showDetail({
                title: 'Job ' + id + ' — still running',
                body: 'This job has not finished, so its result cannot be read yet.\n\n'
                    + 'Asking for it would hold a request open on the running script, and '
                    + 'when that request times out the service cancels the job — stopping '
                    + 'the payload part-way through typing into the host.\n\n'
                    + 'Wait for it to leave this list, or press Cancel to stop it deliberately.',
              });
              return;
            }
            const r = await guard(() => Api.rpc('HIDGetScriptJobResult', { id }), 'read the job result');
            if (!r) return;
            let pretty = r.resultJson || '';
            try { pretty = JSON.stringify(JSON.parse(pretty), null, 2); } catch (_) { /* leave as-is */ }
            await showDetail({
              title: 'Job ' + id + (r.isFinished ? ' — finished' : ' — still running'),
              body: pretty || '(the script produced no result)',
              mono: true,
            });
          },
        }, 'Result'),
        h('button.btn.btn-sm.btn-danger', {
          type: 'button',
          onclick: async () => {
            /* 28px tall, a thumb's width from "Result" -- which is now safe
               by design -- and this one stops a payload part-way through
               typing into someone else's machine. "Cancel all" on this page
               has always confirmed and said what it costs; the per-job
               button, which is the one a mis-tap reaches, did not. */
            const yes = await confirmAction({
              title: 'Cancel job ' + id + '?',
              body: 'The script stops wherever it has got to.',
              consequence: 'Anything already typed into the host stays typed -- cancelling does '
                + 'not undo keystrokes. A half-finished command is left in whatever had focus on '
                + 'the target machine.',
              confirmLabel: 'Cancel job ' + id,
              danger: true,
            });
            if (!yes) return;
            const r = await guard(() => Api.rpc('HIDCancelScriptJob', { id }), 'cancel the job');
            if (r !== undefined) { toast('Cancelled job ' + id + '.'); refreshJobs(); }
          },
        }, 'Cancel')))))))); 
}

/* --- Reflexes: trigger/action automation ------------------------------- */

/* Triggers and actions are protobuf oneofs: the wire format is the member's own
 * field name carrying its message, e.g. {"serviceStarted":{}} or
 * {"bashScript":{"scriptName":"startup.sh"}}. The `fields` here drive the form
 * AND the payload, so the two cannot drift apart.
 *
 * GPIO IS offered now. It was left out on the grounds that it "needs hardware
 * context the console cannot show" -- but GetAvailableGpios returns exactly
 * that context and was simply never called, so the reason had stopped being
 * true. The pin list comes from the device, so a board with no usable pins
 * offers none rather than inviting a guess.
 *
 * NOTE THE FIELD NAMES. protojson emits what the .proto declares, and
 * TriggerGPIOIn mixes conventions: gpio_name and debounce_millis are
 * snake_case while pullUpDown and gpioInEdge are camelCase. Getting one wrong
 * is silent -- DiscardUnknown drops it and the reflex arms with a zero value,
 * which for pullUpDown means UP and for gpioInEdge means RISING.
 *
 * The multi-value group trigger is still not offered: it has no equivalent
 * source of context. */
const TRIGGERS = [
  { key: 'serviceStarted', label: 'The device finishes booting',
    hint: 'Fires once, every boot, as soon as the service is up.', fields: [] },
  { key: 'usbGadgetConnected', label: 'A USB host attaches',
    hint: 'Fires when the cable goes into a machine that powers up the gadget.', fields: [] },
  { key: 'usbGadgetDisconnected', label: 'The USB host goes away',
    hint: 'Fires when the cable is pulled or the host powers down.', fields: [] },
  { key: 'wifiAPStarted', label: 'The access point comes up', fields: [] },
  { key: 'wifiConnectedAsSta', label: 'The device joins a WiFi network', fields: [] },
  { key: 'dhcpLeaseGranted', label: 'A client takes a DHCP lease',
    hint: 'Fires when something connects and is given an address -- a good proxy for "a machine just joined".', fields: [] },
  { key: 'gpioIn', label: 'A GPIO pin changes',
    hint: 'A wire into one of the Pi\u2019s pins. Use it for a button, a reed switch, or '
        + 'another board signalling this one. The pin list comes from the device.',
    fields: [
      { name: 'gpio_name', label: 'Pin', required: true, options: 'gpio',
        chooseLabel: 'Choose a pin\u2026',
        emptyLabel: 'No GPIO pins available on this board' },
      { name: 'pullUpDown', label: 'Idle state', choices: ['UP', 'DOWN', 'OFF'], value: 'UP',
        hint: 'What holds the pin when nothing drives it. UP for a button wired to ground.' },
      { name: 'gpioInEdge', label: 'Fires on', choices: ['RISING', 'FALLING', 'BOTH'], value: 'FALLING',
        hint: 'FALLING is the press of a button wired to ground; RISING is the release.' },
      { name: 'debounce_millis', label: 'Debounce (ms)', type: 'number', value: '100', numeric: true,
        hint: 'A mechanical contact bounces for a few milliseconds and would otherwise '
            + 'fire the reflex several times per press. 0 disables it.' },
    ] },
  { key: 'sshLogin', label: 'Someone logs in over SSH',
    fields: [{ name: 'loginUser', label: 'Username', placeholder: 'any user if left blank' }] },
  { key: 'groupReceive', label: 'A group value arrives',
    hint: 'Group values are how scripts on the device signal each other.',
    fields: [
      { name: 'groupName', label: 'Group', placeholder: 'svc', required: true },
      { name: 'value', label: 'Value', type: 'number', value: '1', numeric: true },
    ] },
];

const ACTIONS = [
  { key: 'hidScript', label: 'Run a HIDScript',
    hint: 'Types into the attached host.',
    fields: [{ name: 'scriptName', label: 'Script', required: true, options: 'hid' }] },
  { key: 'bashScript', label: 'Run a bash script',
    hint: 'Runs on the device itself, as root.',
    fields: [{ name: 'scriptName', label: 'Script', required: true, options: 'bash' }] },
  { key: 'groupSend', label: 'Send a group value',
    hint: 'Signal another script or reflex.',
    fields: [
      { name: 'groupName', label: 'Group', placeholder: 'ack', required: true },
      { name: 'value', label: 'Value', type: 'number', value: '1', numeric: true },
    ] },
  { key: 'gpioOut', label: 'Set a GPIO pin',
    hint: 'Drive one of the Pi\u2019s pins high or low -- an LED, a relay, or a signal to '
        + 'another board. Check what is wired to it before you drive it.',
    fields: [
      { name: 'gpio_name', label: 'Pin', required: true, options: 'gpio',
        chooseLabel: 'Choose a pin\u2026',
        emptyLabel: 'No GPIO pins available on this board' },
      { name: 'value', label: 'Drive it', choices: ['HIGH', 'LOW', 'TOGGLE'], value: 'HIGH' },
    ] },
  { key: 'log', label: 'Write a line to the Journal',
    hint: 'Useful for proving a trigger fires before you attach anything real to it.', fields: [] },
  // Two of the reflexes the device ships with use this, so leaving it out of
  // the list made the Deployed table describe them as "unrecognised" -- an
  // operator could see that something was armed but not what it would do.
  { key: 'deploySettingsTemplate', label: 'Deploy a stored template',
    hint: 'Applies a saved USB, network, WiFi, Bluetooth or reflex template. Choose the kind '
        + 'first: the name is looked up in that kind’s store and nowhere else.',
    fields: [
      { name: 'type', label: 'Kind', value: 'NETWORK',
        // FULL_SETTINGS deploys a whole loadout (SubSysTriggerAction.go:344
        // -> DeployStoredMasterTemplate). It is the enum's ZERO value, so
        // omitting it from this list did not merely hide it -- it made the
        // default-selected 'NETWORK' the only way to express anything, and a
        // reflex could not deploy a loadout at all.
        choices: ['FULL_SETTINGS', 'USB', 'NETWORK', 'WIFI', 'BLUETOOTH', 'TRIGGER_ACTIONS'],
        hint: 'Which store the name below is resolved in.' },
      /* A PICKER, keyed off Kind -- not free text.
         This was the last reflex field that took a name as a string: a typo,
         or a name that exists under a DIFFERENT kind, armed a rule pointing
         at nothing. Nothing said so, because the name is not resolved at save
         time and at fire time SubSysTriggerAction logs the failure and carries
         on. ui.js records the same failure as already fixed for the script
         fields: "a typo armed a rule that pointed at nothing, and nothing
         said so". A list of what exists in the chosen store cannot be
         mistyped and cannot be the wrong store. */
      { name: 'templateName', label: 'Template', required: true,
        optionsBy: 'type',
        optionsFor: {
          FULL_SETTINGS: 'tplMaster', USB: 'tplUsb', NETWORK: 'tplNet',
          WIFI: 'tplWifi', BLUETOOTH: 'tplBt', TRIGGER_ACTIONS: 'tplTas',
        },
        chooseLabel: 'Choose a template…',
        emptyLabel: 'Nothing of that kind is stored',
        hint: 'Stored templates of the chosen kind, read from the device.' },
    ] },
];

const byKey = (list, k) => list.find(x => x.key === k);

/* The label of whichever oneof member is set, WITH the member's own values.
 *
 * The label alone made every rule of the same shape read identically -- "A USB
 * host attaches -> Run a HIDScript" whatever script, pin, group or template it
 * named. Two such rules were indistinguishable in the only table that lists
 * them, so disarming or deleting the right one was guesswork from the Id
 * column, and the Delete dialog repeated the same ambiguous sentence back.
 * The specs already enumerate the fields, so the values cost nothing to show.
 * EmitUnpopulated means a zero value arrives as an empty string, which is
 * dropped here rather than printed as a blank parameter. */
function describeOneOf(obj, list) {
  for (const item of list) {
    const v = obj && obj[item.key];
    if (v === undefined || v === null) continue;
    const vals = (item.fields || [])
      .map(f => (v[f.name] === undefined || v[f.name] === null) ? '' : String(v[f.name]))
      .filter(s => s !== '');
    return vals.length ? item.label + ' (' + vals.join(', ') + ')' : item.label;
  }
  return 'unrecognised';
}

Views.reflexes = async function () {
  const main = clear($('#view'));
  main.append(pageHead('Reflexes',
    'Rules the device acts on by itself: when something happens, run something. This is what lets it work with nobody at the keyboard.'));

  const [set, hidScripts, bashScripts, gpios,
         tasStored, masterT, usbT, wifiT, btT, netT] = await Promise.all([
    guard(() => Api.rpc('GetDeployedTriggerActionSet'), 'read the reflexes'),
    Api.rpc('ListStoredHIDScripts').catch(() => null),
    Api.rpc('ListStoredBashScripts').catch(() => null),
    /* The pin list is what makes GPIO offerable at all. It used to be left
       out of this builder on the grounds that GPIO "needs hardware context
       the console cannot show" -- but GetAvailableGpios IS that context, and
       it was simply never called. A board with no usable pins answers with
       an empty list and the fields say so, which is the honest version of
       the same caution. */
    Api.rpc('GetAvailableGpios').catch(() => null),
    /* The six template stores, because "Deploy a stored template" resolves a
       name in exactly one of them and the builder has to offer the right
       list. The reflex-set list is also what the store-under-a-name step
       below checks against. Unguarded: an empty store, a device with no
       Bluetooth controller and a device with no WiFi all answer some of these
       with an error, and a toast per empty list on every visit is noise
       rather than information. */
    Api.rpc('ListStoredTriggerActionSets').catch(() => null),
    Api.rpc('ListStoredMasterTemplate').catch(() => null),
    Api.rpc('ListStoredUSBSettings').catch(() => null),
    Api.rpc('ListStoredWifiSettings').catch(() => null),
    Api.rpc('ListStoredBluetoothSettings').catch(() => null),
    Api.rpc('ListStoredEthernetInterfaceSettings').catch(() => null),
  ]);
  State.triggers = set;
  const items = (set && set.TriggerActions) || [];
  /* null = that list could NOT BE READ, which is a different fact from an
     empty store: only one of the two means "there is nothing to pick", and
     only one means a name's availability is unknown. The pickers and the
     store-under-a-name step below both say which they are looking at rather
     than showing the plausible default. */
  const stringList = r => (r === null || r === undefined) ? null
    : (Array.isArray(r.msgArray) ? r.msgArray.filter(n => typeof n === 'string' && n.length) : []);
  const storedTas = stringList(tasStored);
  const optionLists = {
    hid: stringList(hidScripts),
    bash: stringList(bashScripts),
    gpio: stringList(gpios),
    tplMaster: stringList(masterT),
    tplUsb: stringList(usbT),
    tplWifi: stringList(wifiT),
    tplBt: stringList(btT),
    tplNet: stringList(netT),
    tplTas: stringList(tasStored),
  };

  main.append(renderReflexBuilder(optionLists));

  /* STORING WHAT IS RUNNING.
   *
   * Reflexes built on this page are runtime only, and until this button
   * existed nothing on the page said so and nothing pointed at a store: the
   * only control that could keep them was on Templates, under "Store the
   * running reflexes", a page this one never named. So an operator could
   * build six rules, pull the cable -- which view-system.js calls "the normal
   * way this device is switched off" -- and lose all six with no screen
   * having mentioned it. The Overview primer's advice to "Store a Loadout
   * when you have something worth keeping" did not help either: a loadout can
   * only name an already-stored reflex SET, which this page could not create.
   *
   * There is no StoreDeployedTriggerActionSet, so unlike USB, WiFi and
   * Bluetooth the set is read and sent back whole. The actions go back exactly
   * as the device produced them -- ids included -- so no field name inside a
   * TriggerAction is ever typed here; only the two top-level keys are, and
   * tools/check-rpc-shapes.py covers those.
   *
   * Immutable rules are dropped. DeployTriggerActionSetAdd refuses to add one
   * ("we don't allow adding immutable settings via RPC call"), so storing them
   * would put rules in the set that can never be deployed from it and the
   * count would overstate what the set does. */
  const mutable = items.filter(t => t && !t.immutable);
  const lockedItems = items.filter(t => t && t.immutable);
  async function storeRunningReflexes() {
    if (set === undefined) {
      toast('The running reflexes could not be read, so there is nothing to store under a name.', true);
      return;
    }
    if (!mutable.length) {
      await showDetail({
        title: 'Nothing to store',
        body: lockedItems.length
          ? 'Every reflex running on this device is one of the service’s own locked rules. '
            + 'Those cannot be added back from a stored set, so a set made of them would deploy '
            + 'nothing. Build a rule above first.'
          : 'No reflexes are running. Build one above first.',
      });
      return;
    }
    const armed = mutable.filter(t => t.isActive).length;
    const name = await promptValue({
      title: 'Store the running reflexes',
      label: 'Name',
      placeholder: 'engagement-a',
      hint: mutable.length + ' reflex' + (mutable.length === 1 ? '' : 'es') + ' will be stored'
        + (armed === mutable.length ? '' : ' (' + armed + ' armed, ' + (mutable.length - armed)
          + ' switched off, kept as they are)')
        + (lockedItems.length ? '; the ' + lockedItems.length + ' locked one'
          + (lockedItems.length === 1 ? '' : 's') + ' the service owns cannot be' : '')
        + '. Letters, digits, dot, dash and underscore.',
      validate: v => {
        if (!v) return 'Give it a name.';
        if (!/^[A-Za-z0-9._-]+$/.test(v)) return 'Letters, digits, dot, dash and underscore only.';
        return null;
      },
    });
    if (!name) return;
    /* A store writes over the key and reports success, so the name is checked
       first -- the same step Templates takes before every store. */
    if (storedTas === null) {
      const ok = await confirmAction({
        title: 'Store as "' + name + '"?',
        body: 'The list of stored reflex sets could not be read, so whether that name is already '
          + 'in use is unknown.',
        consequence: 'If it is, the device replaces the existing set without asking and without '
          + 'keeping a copy. There is no undo.',
        confirmLabel: 'Store it anyway',
        danger: true,
      });
      if (!ok) return;
    } else if (storedTas.includes(name)) {
      const ok = await confirmAction({
        title: 'Replace "' + name + '"?',
        body: 'A reflex set is already stored under that name.',
        consequence: 'It is overwritten and no copy is kept. Any loadout that names it deploys '
          + 'these rules instead from now on.',
        confirmLabel: 'Replace it',
        danger: true,
      });
      if (!ok) return;
    }
    const r = await guard(
      () => Api.rpc('StoreTriggerActionSet', { TriggerActions: mutable, Name: name }),
      'store the reflexes');
    if (r !== undefined) {
      toast('Stored ' + mutable.length + ' reflex' + (mutable.length === 1 ? '' : 'es')
        + ' as "' + name + '".');
      Views.reflexes();
    }
  }

  main.append(h('div.card',
    h('div.card-head',
      // The set's Name is an internal identifier -- it is literally the
      // string "DeployedTriggerActions" -- so printing it just shouted a
      // Go type name at the operator.
      h('h2.card-title', { style: 'margin:0' }, 'Deployed'),
      h('div.btn-row',
        h('span.field-hint', items.length
          ? items.filter(t => t.isActive).length + ' of ' + items.length + ' armed'
          : ''),
        h('button.btn.btn-sm.btn-quiet', {
          type: 'button', onclick: () => storeRunningReflexes(),
        }, 'Store these reflexes'))),
    h('p.field-hint', { style: 'margin:-8px 0 14px' },
      'These rules are running, not stored. They live in memory, so a reboot -- or pulling the '
      + 'cable, which is how this device is normally switched off -- loses them: the device comes '
      + 'back with the service’s own locked rules and whatever reflex set the boot loadout '
      + 'names, if it names one. Store them under a name to keep them, and because a loadout can '
      + 'only name an already-stored set, that is also what lets a loadout bring them back. '
      + 'Stored sets are listed on Templates.'),
    items.length ? dataTable(
      h('thead', h('tr',
        h('th', 'Id'), h('th', 'When'), h('th', 'Then'),
        h('th', 'Once'), h('th', 'State'), h('th', ''))),
      h('tbody', ...items.map(t => h('tr',
        h('td.mono', String(t.id ?? '--')),
        h('td', describeOneOf(t, TRIGGERS)),
        h('td', describeOneOf(t, ACTIONS)),
        h('td', t.oneShot ? 'yes' : 'no'),
        h('td',
          h('span.pill',
            h('span', { class: 'dot ' + (t.isActive ? 'dot-ok' : 'dot-idle') }),
            t.isActive ? 'Armed' : 'Off')),
        h('td', h('div.btn-row',
          t.immutable
            ? h('span.field-hint', 'locked')
            : [
                h('button.btn.btn-sm', {
                  type: 'button',
                  onclick: async () => {
                    const next = Object.assign({}, t, { isActive: !t.isActive });
                    const r = await guard(
                      () => Api.rpc('DeployTriggerActionSetUpdate', { TriggerActions: [next] }),
                      t.isActive ? 'disarm the reflex' : 'arm the reflex');
                    if (r !== undefined) { toast(t.isActive ? 'Disarmed.' : 'Armed.'); Views.reflexes(); }
                  },
                }, t.isActive ? 'Disarm' : 'Arm'),
                h('button.btn.btn-sm.btn-danger', {
                  type: 'button',
                  onclick: async () => {
                    const yes = await confirmAction({
                      title: 'Delete reflex ' + t.id + '?',
                      body: describeOneOf(t, TRIGGERS) + ' -> ' + describeOneOf(t, ACTIONS),
                      consequence: 'The rule is removed from the running device immediately. If it is part of a stored loadout it will come back when that loadout is deployed again.',
                      confirmLabel: 'Delete',
                      danger: true,
                    });
                    if (!yes) return;
                    const r = await guard(
                      () => Api.rpc('DeployTriggerActionSetRemove', { TriggerActions: [t] }),
                      'delete the reflex');
                    if (r !== undefined) { toast('Deleted.'); Views.reflexes(); }
                  },
                }, 'Delete'),
              ]))))))
      : h('div.empty', 'No reflexes are deployed. Add one above and the device starts acting on its own.')));

  main.append(h('div.card',
    h('h2.card-title', 'Send a group value by hand'),
    h('p.field-hint', { style: 'margin:-8px 0 16px' },
      'Fires a group value right now, without waiting for a trigger. The quickest way to test a reflex you just built.'),
    (() => {
      const g = h('input', { type: 'text', placeholder: 'svc' });
      const v = h('input', { type: 'number', value: '1' });
      return h('div.row',
        h('label.field', h('span.field-label', 'Group'), g),
        h('label.field', { style: 'max-width:140px' }, h('span.field-label', 'Value'), v),
        h('div', { style: 'padding-top:26px' },
          h('button.btn', {
            type: 'button',
            onclick: async () => {
              if (!g.value.trim()) { toast('Name a group first.', true); return; }
              const r = await guard(() => Api.rpc('FireActionGroupSend',
                { groupName: g.value.trim(), value: Number(v.value) || 0 }), 'send the group value');
              if (r !== undefined) toast('Sent.');
            },
          }, 'Send')));
    })()));
};

/* The builder. Inline rather than a modal: it has two dependent selects and a
   variable set of fields, which is more than a dialog should carry. */
function renderReflexBuilder(optionLists) {
  const triggerSel = h('select', { 'aria-label': 'Trigger' },
    ...TRIGGERS.map(t => h('option', { value: t.key }, t.label)));
  const actionSel = h('select', { 'aria-label': 'Action' },
    ...ACTIONS.map(a => h('option', { value: a.key }, a.label)));
  const oneShot = h('input', { type: 'checkbox' });

  const triggerFields = h('div.builder-fields');
  const actionFields = h('div.builder-fields');
  const triggerHint = h('p.field-hint');
  const actionHint = h('p.field-hint');

  /* Fill a picker in place, keeping the current selection when the new list
     still holds it.
     `opts` of null means the device did not answer, which is not the same as
     an empty store: saying "nothing is stored" to someone whose list failed
     to read is a claim about their device that may well be false. */
  function fillOptions(select, opts, f) {
    const had = select.value;
    const list = Array.isArray(opts) ? opts : [];
    clear(select);
    select.append(h('option', { value: '' },
      list.length ? (f.chooseLabel || 'Choose a script...')
        : Array.isArray(opts) ? (f.emptyLabel || 'No scripts stored')
          : 'Unknown -- the device did not answer'));
    for (const n of list) select.append(h('option', { value: n }, n));
    if (had && list.indexOf(had) !== -1) select.value = had;
  }

  /* Build inputs for whichever member is selected, and keep the DOM nodes so
     collect() can read them back without a second lookup table. */
  function renderFields(spec, host, hintEl, store) {
    clear(host); clear(hintEl);
    if (spec.hint) hintEl.append(spec.hint);
    store.inputs = {};
    for (const f of spec.fields) {
      let input;
      if (f.options || f.optionsBy) {
        /* `options` names a fixed list; `optionsBy` names another field in
           this same form whose value chooses the list, and is filled in the
           second pass below. */
        input = h('select', {});
        /* Filled unconditionally, so a dependent picker whose controlling
           field is missing from the spec still carries a placeholder rather
           than rendering as an empty box with nothing in it at all. The
           second pass overwrites this for the dependent case. */
        fillOptions(input, f.options ? optionLists[f.options] : undefined, f);
      } else if (f.choices) {
        // A fixed set of values the API will accept. Typing these by hand
        // into a text box only produces rejections the operator cannot
        // predict -- the enum is not written down anywhere in the console.
        input = h('select', {}, ...f.choices.map(c => h('option', { value: c }, c)));
        if (f.value) input.value = f.value;
      } else {
        input = h('input', { type: f.type || 'text', value: f.value || '', placeholder: f.placeholder || '' });
      }
      store.inputs[f.name] = { el: input, spec: f };
      /* Per-field hint. The spec objects already carried `hint` on the GPIO
         fields and the renderer dropped it on the floor -- the text existed
         in the source and never reached a screen. These fields are the ones
         that most need it: "pull up or down" and "rising or falling" are not
         guessable, and guessing wrong arms a reflex that fires on the wrong
         edge or never fires at all. */
      host.append(h('label.field',
        h('span.field-label', f.label),
        input,
        f.hint ? h('span.field-hint', f.hint) : null));
    }
    /* Second pass: fields whose list depends on another field here. Done
       after the loop so the controlling field may be declared anywhere in the
       spec, and re-run on every change so the list shown is always the store
       the chosen kind resolves against -- a WiFi template name is not a
       loadout name, and the service looks a name up in one store only. */
    for (const f of spec.fields) {
      if (!f.optionsBy) continue;
      const dep = store.inputs[f.name];
      const ctl = store.inputs[f.optionsBy];
      if (!dep || !ctl) continue;
      const refill = () => fillOptions(dep.el, optionLists[(f.optionsFor || {})[ctl.el.value]], f);
      refill();
      ctl.el.addEventListener('change', refill);
    }
  }

  const tStore = { inputs: {} }, aStore = { inputs: {} };
  const syncTrigger = () => renderFields(byKey(TRIGGERS, triggerSel.value), triggerFields, triggerHint, tStore);
  const syncAction = () => renderFields(byKey(ACTIONS, actionSel.value), actionFields, actionHint, aStore);
  triggerSel.addEventListener('change', syncTrigger);
  actionSel.addEventListener('change', syncAction);
  syncTrigger(); syncAction();

  function collect(store) {
    const out = {};
    for (const [name, { el, spec }] of Object.entries(store.inputs)) {
      const raw = el.value.trim();
      if (spec.required && !raw) return { error: spec.label + ' is required.' };
      if (!raw) continue;
      out[name] = spec.numeric ? Number(raw) : raw;
      if (spec.numeric && !Number.isFinite(out[name])) return { error: spec.label + ' must be a number.' };
    }
    return { value: out };
  }

  const createBtn = h('button.btn.btn-primary', { type: 'button' }, 'Add reflex');
  createBtn.addEventListener('click', async () => {
    const t = collect(tStore), a = collect(aStore);
    if (t.error) { toast(t.error, true); return; }
    if (a.error) { toast(a.error, true); return; }

    const ta = { isActive: true, oneShot: oneShot.checked, immutable: false };
    ta[triggerSel.value] = t.value;
    ta[actionSel.value] = a.value;

    createBtn.disabled = true;
    const r = await guard(
      () => Api.rpc('DeployTriggerActionSetAdd', { TriggerActions: [ta] }),
      'add the reflex');
    createBtn.disabled = false;
    if (r !== undefined) { toast('Reflex added and armed.'); Views.reflexes(); }
  });

  return h('div.card',
    h('details.builder', { open: 'open' },
      h('summary', 'Add a reflex'),
      h('div', { style: 'margin-top:16px' },
        h('div.builder-grid',
          h('div',
            h('span.builder-step', 'When'),
            h('label.field', h('span.field-label', 'Trigger'), triggerSel),
            triggerHint,
            triggerFields),
          h('div',
            h('span.builder-step', 'Then'),
            h('label.field', h('span.field-label', 'Action'), actionSel),
            actionHint,
            actionFields)),
        h('label.check', { style: 'margin-top:8px' },
          oneShot,
          h('span', 'Only once -- disarm itself after it fires')),
        h('div.btn-row', { style: 'margin-top:12px' }, createBtn))));
}

/* --- Loadouts: master templates ---------------------------------------- */

Views.loadouts = async function () {
  const main = clear($('#view'));
  main.append(pageHead('Loadouts',
    'A loadout is a whole-device configuration: USB, WiFi, Bluetooth, network and reflexes together. Deploying one changes everything at once.'));

  /* The five subsystem lists are what a loadout is composed FROM, so they
     are fetched here rather than inside the editor: a picker that cannot be
     populated should not be offered at all. Unguarded -- a device with no
     Bluetooth controller answers these with an error, and a toast per empty
     list on every visit to this page is noise, not information. */
  const [list, startup, usbT, wifiT, btT, netT, tasT] = await Promise.all([
    guard(() => Api.rpc('ListStoredMasterTemplate'), 'list loadouts'),
    guard(() => Api.rpc('GetStartupMasterTemplate'), 'read startup loadout'),
    Api.rpc('ListStoredUSBSettings').catch(() => null),
    Api.rpc('ListStoredWifiSettings').catch(() => null),
    Api.rpc('ListStoredBluetoothSettings').catch(() => null),
    Api.rpc('ListStoredEthernetInterfaceSettings').catch(() => null),
    Api.rpc('ListStoredTriggerActionSets').catch(() => null),
  ]);
  const names = (list && list.msgArray) || [];
  const startupName = (startup && startup.msg) || null;
  const arr = r => (r && r.msgArray) || [];
  const parts = {
    usb: arr(usbT), wifi: arr(wifiT), bt: arr(btT),
    net: arr(netT), tas: arr(tasT),
  };

  main.append(h('div.card',
    h('h2.card-title', 'Stored loadouts'),
    names.length ? dataTable(
      h('thead', h('tr', h('th', 'Name'), h('th', 'Boot default'), h('th', 'Actions'))),
      h('tbody', ...names.map(n => h('tr',
        h('td.mono', n),
        h('td', startupName === n ? 'yes' : ''),
        h('td',
          h('button.btn.btn-sm.btn-primary', {
            onclick: async () => {
              /* READ THE LOADOUT BEFORE ASKING.
                 A loadout that names a reflex set runs
                 DeployStoredTriggerActionSetReplace, which clears every
                 non-immutable reflex on the running device before adding the
                 stored ones -- and reflexes built on the Reflexes page are
                 runtime-only, so they are simply gone. The old confirmation
                 enumerated four subsystems and read as a complete list, so
                 nothing warned anyone. Templates treats the identical call as
                 the most destructive thing on its page: counts what will be
                 lost, names the locked survivors, marks itself danger. Two
                 pages, one RPC path, so they say the same thing now. */
              const t = await guard(() => Api.rpc('GetStoredMasterTemplate', { msg: n }),
                'read loadout "' + n + '"');
              if (t === undefined || t === null) return;
              const named = [
                ['USB', t.template_name_usb], ['WiFi', t.template_name_wifi],
                ['Bluetooth', t.template_name_bluetooth],
                ['Reflexes', t.template_name_trigger_actions],
                ['Network', (t.template_names_network || []).join(', ')],
              ].filter(([, v]) => v).map(([k, v]) => k + ': ' + v);
              const tas = typeof t.template_name_trigger_actions === 'string'
                ? t.template_name_trigger_actions : '';
              let reflexes;
              if (tas) {
                const live = await Api.rpc('GetDeployedTriggerActionSet').catch(() => null);
                const all = (live && Array.isArray(live.TriggerActions)) ? live.TriggerActions : null;
                const losing = all ? all.filter(x => x && !x.immutable).length : null;
                const keeping = all ? all.filter(x => x && x.immutable).length : null;
                reflexes = ' It names the reflex set "' + tas + '", so every reflex running on the '
                  + 'device is thrown away first and then that set is applied. '
                  + (losing === null
                    ? 'How many are running now could not be read, so how many you are about to '
                      + 'lose is unknown. '
                    : losing + ' reflex' + (losing === 1 ? '' : 'es') + ' will be discarded'
                      + (keeping ? ' (the ' + keeping + ' locked one' + (keeping === 1 ? '' : 's')
                        + ' the service owns survive)' : '') + '. ')
                  + 'Rules built on the Reflexes page are not written to disk, so they are not '
                  + 'recoverable unless you stored them first.';
              } else {
                reflexes = ' It names no reflex set, so the reflexes running now are left alone.';
              }
              const yes = await confirmAction({
                title: 'Deploy "' + n + '"?',
                body: 'A loadout reconfigures USB, WiFi, Bluetooth, networking and reflexes '
                  + 'together, all at once.'
                  + (named.length
                    ? ' "' + n + '" holds — ' + named.join('; ') + '.'
                    : ' "' + n + '" names no template at all, so deploying it would change nothing.'),
                consequence: 'Whichever link you are reading this over -- USB, WiFi or Bluetooth -- is '
                  + 'almost certainly reconfigured by this. Expect the console to disconnect. Reload '
                  + 'after a few seconds; if the network details changed you may need to reconnect to '
                  + 'a different SSID or address.' + reflexes,
                confirmLabel: 'Deploy loadout',
                danger: !!tas,
              });
              if (!yes) return;
              const r = await guard(() => Api.rpc('DeployStoredMasterTemplate', { msg: n }), 'deploy loadout');
              if (r !== undefined) toast('Deployed "' + n + '".');
            },
          }, 'Deploy'),
          ' ',
          h('button.btn.btn-sm', {
            onclick: async () => {
              /* CONFIRM FIRST, and say what is being armed.
                 This is a 28px button a thumb's width from Deploy and
                 Inspect, and it was the only unconfirmed action on the page
                 -- yet it is the most permanent one. Deploy changes the
                 device until the next boot; this changes what the device
                 comes BACK as, and no screen in the console can unset it.
                 Arming a loadout whose WiFi template is in station mode, or
                 whose USB composition has no RNDIS and no CDC ECM, means the
                 device returns from its next power cycle with no route to
                 this console at all. */
              const t = await guard(() => Api.rpc('GetStoredMasterTemplate', { msg: n }),
                'read loadout "' + n + '"');
              if (t === undefined || t === null) return;
              const parts = [
                ['USB', t.template_name_usb], ['WiFi', t.template_name_wifi],
                ['Bluetooth', t.template_name_bluetooth],
                ['Reflexes', t.template_name_trigger_actions],
                ['Network', (t.template_names_network || []).join(', ')],
              ].filter(([, v]) => v).map(([k, v]) => k + ': ' + v);
              const okGo = await confirmAction({
                title: 'Load "' + n + '" at every boot?',
                body: 'This is what the device will apply on every power-up from now on'
                  + (parts.length ? ', and it holds \u2014 ' + parts.join('; ') + '.' : '.'),
                consequence: 'Nothing in this console can unset it; only arming a different '
                  + 'loadout replaces it. If its USB composition drops RNDIS and CDC ECM, or '
                  + 'its WiFi template joins a network instead of spawning the access point, '
                  + 'the device comes back unreachable and the SD card has to come out.',
                confirmLabel: 'Load it at boot',
                danger: true,
              });
              if (!okGo) return;
              /* StringMessage's field is `msg`. Sending `templateName` was
                 accepted and silently discarded by the bridge, so this set the
                 boot default to an EMPTY string -- clicking "Use at boot"
                 erased the boot configuration instead of setting it. */
              const r = await guard(() => Api.rpc('SetStartupMasterTemplate', { msg: n }), 'set the boot default');
              if (r !== undefined) { toast('"' + n + '" will load at boot.'); Views.loadouts(); }
            },
          }, 'Use at boot'),
          ' ',
          /* GetStoredMasterTemplate had no caller in any client, which made
             Deploy the most destructive action in the product AND a blind
             one: the operator chose from a list of names with no way to see
             what any of them would do. A loadout is five names, so showing
             it costs one RPC and a table. */
          h('button.btn.btn-sm.btn-quiet', {
            onclick: async () => {
              const t = await guard(() => Api.rpc('GetStoredMasterTemplate', { msg: n }),
                'read loadout "' + n + '"');
              if (t === undefined) return;
              const net = t.template_names_network || [];
              const row = (k, v) => k + ': ' + (v && v.length ? v : '(unset -- left as-is)');
              await showDetail({
                title: 'Loadout "' + n + '"',
                body: [
                  row('USB        ', t.template_name_usb),
                  row('WiFi       ', t.template_name_wifi),
                  row('Bluetooth  ', t.template_name_bluetooth),
                  row('Reflexes   ', t.template_name_trigger_actions),
                  row('Network    ', net.join(', ')),
                  '',
                  'Deploying applies only the subsystems named above.',
                  'Anything left unset keeps its current configuration.',
                ].join('\n'),
                mono: true,
              });
            },
          }, 'Inspect'))))))
      : h('div.empty', 'No loadouts stored yet.')));

  /* THE EDITOR.
   *
   * StoreMasterTemplate had no caller in any client, so the set of
   * deployable loadouts was frozen at whatever dist/db/init.db shipped. The
   * Overview primer told the operator to "Store a Loadout when you have
   * something worth keeping" and no client could.
   *
   * A MasterTemplate is five names and nothing else (proto/grpc.proto:144),
   * so this needs no settings plumbing -- only the lists already fetched
   * above. Pickers rather than text boxes because a loadout that names a
   * template which does not exist fails at DEPLOY time, which is the worst
   * moment: mid-reconfiguration, over a link the loadout is itself changing. */
  const anyParts = Object.values(parts).some(a => a.length);
  main.append(h('div.card',
    h('h2.card-title', 'Compose a loadout'),
    h('p.field-hint', { style: 'margin-bottom:14px' },
      'A loadout names one stored template per subsystem. Leave a subsystem unset and '
      + 'deploying the loadout will not touch it.'),
    anyParts
      ? h('div.btn-row', h('button.btn.btn-primary', {
        onclick: async () => {
          const none = '-- leave unset --';
          const r = await promptForm({
            title: 'Compose a loadout',
            intro: 'Only the subsystems you name are applied when this loadout is deployed.',
            confirmLabel: 'Store loadout',
            fields: [
              { key: 'name', label: 'Loadout name', placeholder: 'engagement-a',
                hint: 'Storing under an existing name replaces it.' },
              { key: 'usb', label: 'USB', options: parts.usb, emptyLabel: none },
              { key: 'wifi', label: 'WiFi', options: parts.wifi, emptyLabel: none },
              { key: 'bt', label: 'Bluetooth', options: parts.bt, emptyLabel: none },
              { key: 'tas', label: 'Reflexes', options: parts.tas, emptyLabel: none },
              { key: 'net', label: 'Network', options: parts.net, multiple: true,
                hint: 'One template per interface, so several may apply. Ctrl-click or '
                    + 'cmd-click to select more than one; select none to leave networking alone.' },
            ],
            validate: v => {
              if (!v.name) return 'Give the loadout a name.';
              /* A loadout that names nothing is accepted by the service and
                 deploys nothing -- it would sit in the list looking like a
                 configuration. Refuse it here instead. */
              if (!v.usb && !v.wifi && !v.bt && !v.tas && !v.net.length) {
                return 'Name at least one template, or the loadout would do nothing.';
              }
              return null;
            },
          });
          if (!r) return;
          /* Field names are the proto names exactly (the bridge uses
             UseProtoNames), and an unknown key is DISCARDED rather than
             rejected -- a misspelling here would store an empty loadout
             that deploys nothing. tools/check-rpc-shapes.py checks these
             against proto/grpc.proto so a typo fails the build, not the
             device. */
          const tpl = {};
          if (r.usb) tpl.template_name_usb = r.usb;
          if (r.wifi) tpl.template_name_wifi = r.wifi;
          if (r.bt) tpl.template_name_bluetooth = r.bt;
          if (r.tas) tpl.template_name_trigger_actions = r.tas;
          if (r.net.length) tpl.template_names_network = r.net;
          const res = await guard(
            () => Api.rpc('StoreMasterTemplate', { TemplateName: r.name, template: tpl }),
            'store the loadout');
          if (res !== undefined) { toast('Stored loadout "' + r.name + '".'); Views.loadouts(); }
        },
      }, 'New loadout'))
      /* Radio was retired and its cards split across WiFi, Bluetooth and
         Network, so this empty state -- the whole screen, for an operator
         with nothing to compose from -- was naming a page that is not in the
         nav and not in the command palette. */
      : h('div.empty',
        'Nothing to compose from yet. Store a USB, WiFi, Bluetooth, network or reflex '
        + 'template first: Cable, Bluetooth and Network each have a "Store as template" '
        /* WiFi deliberately does NOT say "Store as template": it has two store
           paths and view-wifi.js relabels both, because one snapshots what is
           running and the other stores the form including edits that were
           never applied. Naming a button that is not on the page is the same
           class of error as the "Radio screen" this empty state used to send
           people to. */
        + 'button; WiFi has "Store what is running" and "Store the form as a template"; '
        + 'and Reflexes has "Store these reflexes".')));

  main.append(h('div.card',
    h('h2.card-title', 'Device'),
    /* THE BACKUP BUTTON IS GONE FROM HERE, deliberately.
     *
     * It called DBBackup with only a character check on the name. The System
     * page wraps the same RPC in two guards it documents as necessary: it
     * refuses the name "init", because init.db is the factory template set
     * this image shipped with and the file datastore.Open restores on a first
     * boot -- DBBackup opens the target with O_TRUNC, so "init" truncated the
     * only copy on the device, with no RPC to restore or list it back -- and
     * it confirms before replacing an existing backup, which this one
     * overwrote without a word. The hint here also understated what a backup
     * holds, omitting loadouts and the boot default.
     *
     * Duplicating the guards is how they drift apart again, and System
     * already has the list of existing backups (ListStoredDBBackups, which
     * this page does not fetch) and the matching restore beside it. So: one
     * entry point, with the guards, and a pointer to it. */
    h('p.field-hint', { style: 'margin:-8px 0 14px' },
      'Backing up and restoring the template database is on System, beside the list of backups '
      + 'already on the device. A backup holds every stored template -- USB, WiFi, Bluetooth and '
      + 'network settings, reflex sets and loadouts -- and the boot default.'),
    h('div.btn-row',
      h('button.btn', { onclick: () => go('system') }, 'Open System'),
      h('button.btn.btn-danger', {
        onclick: async () => {
          const yes = await confirmAction({
            title: 'Reboot the device?',
            body: 'The service stops, the Pi restarts, and the boot-default loadout is applied again.',
            consequence: 'Every connection drops, including this one. The device is unreachable for roughly 30 seconds. Anything you have deployed but not stored as a template is lost.',
            confirmLabel: 'Reboot',
            danger: true,
          });
          if (!yes) return;
          await guard(() => Api.rpc('Reboot'), 'reboot the device');
          toast('Reboot requested. This console will go quiet for about 30 seconds.');
        },
      }, 'Reboot'),
      h('button.btn.btn-danger', {
        onclick: async () => {
          const yes = await confirmAction({
            title: 'Shut the device down?',
            body: 'The Pi powers off cleanly.',
            consequence: 'There is no remote way to switch it back on. You have to physically unplug it and plug it back in. Do not do this to a device you cannot reach.',
            confirmLabel: 'Shut down',
            danger: true,
          });
          if (!yes) return;
          await guard(() => Api.rpc('Shutdown'), 'shut the device down');
          toast('Shutdown requested.');
        },
      }, 'Shut down'))));
};

/* --- Journal: live events ---------------------------------------------- */

/* The OLED panel, mirrored.
 *
 * The device has a screen and eight buttons on it. This is that screen, and
 * those buttons, from wherever you are.
 *
 * Three things worth knowing about how it is built:
 *
 *   - the image is fetched with the bearer token and shown from an object
 *     URL, because a browser will not put an Authorization header on an
 *     <img src>. Every frame revokes the previous URL; without that, a view
 *     left open for an hour leaks seven thousand blobs.
 *   - it polls at 2fps, not 30. Encoding one frame costs the Pi Zero W about
 *     10ms, so this is roughly 2% of its single core WHILE YOU WATCH and
 *     nothing at all when you are not. That is also why the image comes over
 *     at 128x64 and is magnified here rather than on the device: a 4x image
 *     measured 59ms a frame on the same board.
 *   - a press goes to the same handler as a press on the board. There is no
 *     second input path that only remote control exercises.
 */
Views.panel = async function () {
  const main = clear($('#view'));
  main.append(pageHead('Panel',
    'The screen on the device, mirrored, and its eight controls. ' +
    'A press here is the same press as a press on the board.'));

  const img = h('img#panel-img.panel-img', {
    alt: 'The device OLED screen', width: 128, height: 64,
  });
  const pill = h('span#panel-pill.pill', { role: 'status', 'aria-live': 'polite' },
    h('span.dot.dot-idle'), 'connecting');
  const note = h('p#panel-note.field-hint', { style: 'margin-top:10px' }, '');
  const text = h('pre#panel-text.panel-text', {
    'aria-label': 'The panel contents as text',
  }, '');

  const key = (label, button, cls, title) => h('button.btn.panel-key' + (cls || ''), {
    type: 'button', title: title || label, 'data-button': button,
    onclick: () => press(button),
  }, label);

  main.append(h('div.grid.two',
    h('div.card',
      h('div', { style: 'display:flex;justify-content:space-between;align-items:baseline;margin-bottom:12px' },
        h('h2.card-title', { style: 'margin:0' }, 'Screen'), pill),
      h('div.panel-bezel', img),
      note),
    h('div.card',
      h('h2.card-title', 'Controls'),
      h('div.panel-pad',
        h('div'), key('\u25B2', 'up', '', 'Up'), h('div'),
        key('\u25C0', 'left', '', 'Left - back'),
        key('\u25CF', 'press', '', 'Press - confirm'),
        key('\u25B6', 'right', '', 'Right - enter'),
        h('div'), key('\u25BC', 'down', '', 'Down'), h('div')),
      h('div.panel-keys',
        key('KEY1', 'key1', '.btn-quiet', 'The action named on the bottom line'),
        key('KEY2', 'key2', '.btn-quiet', 'Refresh'),
        key('KEY3', 'key3', '.btn-quiet', 'Back to the root menu')),
      h('p.field-hint', { style: 'margin-top:14px' },
        'Arrow keys, Enter and 1/2/3 work too while this view is open.'),
      h('h2.card-title', { style: 'margin-top:22px' }, 'As text'),
      text)));

  let lastURL = null;
  let stopped = false;
  /* How long to wait before the next poll.
   *
   * 500ms is right when the panel is live -- it is what makes the mirror
   * feel like a screen rather than a slideshow. It is the wrong number for a
   * device that has no panel at all. A plain (non-oled) image never starts
   * the daemon, so every single poll returns 503, forever, and the console
   * was asking twice a second for the whole time the tab stayed open. On a
   * single-core Pi Zero W that is not free, and it buys literally nothing:
   * the answer cannot change until a daemon starts.
   *
   * So: full speed while it works, and back off when it does not. 409 is
   * deliberately NOT backed off -- that one means "the panel is showing the
   * first-boot credentials", which is a screen the operator dismisses by
   * hand at any moment, and the mirror should come back the instant they
   * do. It is also the cheapest possible answer for the service to give,
   * since it withholds the frame instead of encoding one. */
  const FAST = 500, SLOW = 5000;
  let delay = FAST;

  function status(kind, label) {
    const el = $('#panel-pill');
    if (!el) return;
    clear(el);
    el.append(h('span.dot.dot-' + kind), label);
  }

  async function press(button) {
    try {
      await Api.panelPress(button);
      await refresh();
    } catch (e) {
      toast(e.message || 'the press was not accepted', 'bad');
    }
  }

  async function refresh() {
    if (stopped) return;
    try {
      const blob = await Api.panelImage();
      const url = URL.createObjectURL(blob);
      const el = $('#panel-img');
      if (el) el.src = url;
      /* Revoke AFTER swapping, never before: revoking a URL the <img> is
         still showing blanks the picture on some browsers. */
      if (lastURL) URL.revokeObjectURL(lastURL);
      lastURL = url;
      status('ok', 'live');
      delay = FAST;
      const n = $('#panel-note'); if (n) n.textContent = '';
      const t = $('#panel-text');
      if (t) t.textContent = await Api.panelText().catch(() => '');
    } catch (e) {
      const n = $('#panel-note');
      if (e && e.status === 409) {
        delay = FAST;  // transient by nature: see the note on FAST/SLOW
        status('idle', 'not mirrored');
        if (n) n.textContent = 'The device is showing its first-boot credentials. '
          + 'That screen is deliberately never mirrored: it exists so the password '
          + 'reaches whoever is standing over the device, and then stops existing.';
      } else if (e && e.status === 503) {
        delay = SLOW;
        status('bad', 'no panel');
        if (n) n.textContent = 'No OLED daemon is running. A plain image has no panel, '
          + 'and a board with no HAT fitted does not start one.';
      } else {
        delay = SLOW;
        status('bad', 'error');
        if (n) n.textContent = (e && e.message) || 'the panel could not be read';
      }
    }
  }

  const KEYMAP = {
    ArrowUp: 'up', ArrowDown: 'down', ArrowLeft: 'left', ArrowRight: 'right',
    Enter: 'press', ' ': 'press', 1: 'key1', 2: 'key2', 3: 'key3',
  };
  function onKey(ev) {
    if (ev.target && /^(INPUT|TEXTAREA|SELECT)$/.test(ev.target.tagName)) return;
    const b = KEYMAP[ev.key];
    if (!b) return;
    ev.preventDefault();
    press(b);
  }
  document.addEventListener('keydown', onKey);

  /* setTimeout rather than setInterval, because the interval is no longer a
     constant. setInterval would also queue polls on top of a slow one. */
  let timer = null;
  function schedule() {
    if (stopped) return;
    timer = setTimeout(async () => { await refresh(); schedule(); }, delay);
  }
  onViewTeardown(() => {
    stopped = true;
    if (timer) clearTimeout(timer);
    document.removeEventListener('keydown', onKey);
    if (lastURL) URL.revokeObjectURL(lastURL);
  });

  await refresh();
  schedule();
};

Views.journal = function () {
  const main = clear($('#view'));
  main.append(pageHead('Journal',
    'Everything the device reports, live. Log lines, HID activity, and every trigger that fires.'));
  main.append(h('div.card',
    h('div', { style: 'display:flex;justify-content:space-between;align-items:baseline;margin-bottom:12px' },
      h('h2.card-title', { style: 'margin:0' }, 'Event stream'),
      h('div',
        h('span#stream-pill.pill', { role: 'status', 'aria-live': 'polite' },
        h('span.dot.dot-idle'), 'connecting'),
        ' ',
        h('button.btn.btn-sm.btn-quiet', { onclick: () => { State.log = []; paintLog(); } }, 'Clear'))),
    h('div#log.log', {
      role: 'log', tabindex: '0',
      'aria-label': 'Device event log, newest at the bottom',
    })));
  paintLog();
  paintStreamPill();
};

function logLine(e) {
  return h('div', { class: 'log-line' + (e.lv === 2 ? ' lv-warn' : e.lv === 3 ? ' lv-err' : '') },
    h('span.log-t', fmtTime(e.t)),
    h('span.log-src', e.src),
    h('span.log-msg', e.msg));
}

/* Is the operator reading the newest line, or have they scrolled back?
 *
 * This is the whole question. The Journal used to rebuild all 600 rows and
 * then set scrollTop = scrollHeight on EVERY arriving event -- which is to
 * say, exactly when the device is producing the output someone is trying to
 * read. Scrolling back to an earlier line was impossible and the text
 * selection was destroyed before it could be copied. So: only follow the tail
 * if that is where they already were. */
function logAtBottom(box) {
  return (box.scrollHeight - box.scrollTop - box.clientHeight) < 24;
}

function paintLog() {
  const box = $('#log');
  if (!box) return;
  const follow = logAtBottom(box);
  clear(box);
  if (!State.log.length) { box.append(h('div.empty', 'No events yet.')); return; }
  for (const e of State.log) box.append(logLine(e));
  if (follow) box.scrollTop = box.scrollHeight;
}

/* One arriving event appends one row. Rebuilding the list would throw away
   the selection even when the view is pinned to the bottom. */
function appendLogLine(entry, dropped) {
  const box = $('#log');
  if (!box) return;
  const follow = logAtBottom(box);
  const empty = box.querySelector('.empty');
  if (empty) empty.remove();
  for (let i = 0; i < dropped && box.firstChild; i++) box.removeChild(box.firstChild);
  box.append(logLine(entry));
  if (follow) box.scrollTop = box.scrollHeight;
}

function paintStreamPill() {
  const pill = $('#stream-pill');
  if (!pill) return;
  const s = State.streamStatus || 'connecting';
  const dot = s === 'open' ? 'dot-ok' : s === 'retrying' || s === 'closed' ? 'dot-warn' : 'dot-idle';
  clear(pill).append(h('span', { class: 'dot ' + dot }), s === 'open' ? 'live' : s);
}

/* ------------------------------------------------------- event stream wiring */

function handleEvent(ev) {
  const vals = (ev.values || []).map(v =>
    v.tstring !== undefined ? v.tstring :
    v.tbool !== undefined ? v.tbool :
    v.tint64 !== undefined ? Number(v.tint64) : null);
  const type = Number(ev.type);

  if (type === 1) {                       // EVT_LOG
    push({ src: String(vals[0] ?? '?'), lv: Number(vals[1] ?? 1), msg: String(vals[2] ?? ''), t: vals[3] });
  } else if (type === 3) {                // EVT_HID
    push({ src: 'HID', lv: 1, msg: vals.filter(v => v !== null).join(' '), t: Date.now() });
  } else if (type === 4) {                // EVT_TRIGGER
    const tt = Number(vals[0]);
    if (tt === 1) { State.usbAttached = true; refreshCableIfVisible(); }
    if (tt === 2) { State.usbAttached = false; refreshCableIfVisible(); }
    push({ src: 'trigger', lv: 1, msg: 'trigger type ' + tt + ' fired', t: Date.now() });
  } else if (type === 5) {                // EVT_NOTIFY_STATE_CHANGE
    // The device is telling us a subsystem's settings changed under us --
    // reload the view that shows it rather than displaying stale state.
    const sub = Number(vals[0]);
    /* Subsystem -> the view that shows it. The numbers are
       STATE_CHANGE_EVT_TYPE_* in common_web/EventConst.go. Both the "this
       changed" events and the "the stored list changed" events are mapped,
       because a loadout, the CLI or the OLED panel can reconfigure the device
       under a page that is open -- and a console showing an address or a
       pairing state that stopped being true half a minute ago is worse than
       one that admits it needs a reload. */
    const map = {
      0: 'cable',      // USB
      1: 'wifi',       // WiFi
      2: 'network',    // NETWORK
      3: 'bluetooth',  // BLUETOOTH
      5: 'reflexes',   // TRIGGER_ACTIONS
      6: 'system',     // LED
      8: 'cable',      // stored USB settings list
      9: 'network',    // stored ethernet interface settings list
      10: 'wifi',      // stored WiFi settings list
      11: 'bluetooth', // stored Bluetooth settings list
      12: 'reflexes',  // stored trigger action sets list
      14: 'templates', // stored global settings list
    };
    if (map[sub]) reloadViewOnStateChange(map[sub]);
  }
}

function push(entry) {
  State.log.push(entry);
  let dropped = 0;
  if (State.log.length > LOG_CAP) {
    dropped = State.log.length - LOG_CAP;
    State.log.splice(0, dropped);
  }
  if (State.view === 'journal') appendLogLine(entry, dropped);
  const c = $('#journal-count');
  if (c) c.textContent = String(State.log.length);
}

/* Re-rendering a view from under the operator throws away whatever they were
   typing. A USB attach or detach arrives exactly when someone is most likely to
   be mid-edit -- they are plugging the thing in -- so the old behaviour of
   calling Views.cable() on every such event silently wiped half-entered VIDs,
   product strings and checkbox changes.
   The attach indicator is updated in place instead, and a full reload is only
   offered, never forced, when the form has unsaved changes. */
function refreshCableIfVisible() {
  if (State.view !== 'cable' && State.view !== 'overview') return;
  const dot = $('#cable-state');
  if (!dot) { if (!State.formDirty) Views[State.view](); return; }
  const a = State.usbAttached;
  clear(dot).append(
    h('span', { class: 'dot ' + (a === true ? 'dot-ok' : a === false ? 'dot-idle' : 'dot-warn'),
                style: 'margin-right:6px' }),
    a === true ? 'host attached' : a === false ? 'no host' : 'attach state unknown');
}

/* A subsystem changed under us. Reload only if it is safe to do so. */
function reloadViewOnStateChange(view) {
  if (State.view !== view) return;
  if (!State.formDirty) { Views[view](); return; }
  toast(h('span', 'The device changed these settings. ',
    h('button.btn.btn-sm.btn-quiet', {
      type: 'button', style: 'margin-left:6px',
      onclick: () => { State.formDirty = false; Views[view](); },
    }, 'Reload')));
}

function startStream() {
  if (State.stopStream) State.stopStream();
  State.stopStream = Api.streamEvents({
    onEvent: handleEvent,
    onStatus: (s, detail) => {
      State.streamStatus = s;
      paintStreamPill();
      if (s === 'unauthorized') { Api.setToken(null); renderSignIn(); }
      if (s === 'retrying' && detail) console.warn('event stream retrying:', detail);
    },
  });
}

/* ------------------------------------------------------------- small pieces */

function pageHead(title, note) {
  return h('div.page-head',
    h('div',
      h('h1', { class: 'page-title display' }, title),
      note ? h('p.page-note', note) : null),
    h('div', { id: 'head-extra' }));
}

/* Tables carry more columns than a phone is wide. Wrapping them in their own
   horizontal scroller keeps the PAGE from scrolling sideways -- which breaks
   every other layout on the screen -- while the table itself stays readable
   and scrollable in place. */
function dataTable(...children) {
  return h('div.table-wrap', { tabindex: '0', role: 'region', 'aria-label': 'Table, scrollable horizontally' },
    h('table.data', ...children));
}

function kvRow(k, v, hint) {
  return h('tr',
    h('th', { scope: 'row', style: 'text-transform:none;letter-spacing:0;font-size:13px;color:var(--text-muted)' }, k),
    h('td', h('span.mono', String(v)), hint ? h('div.field-hint', hint) : null));
}

function textField(label, value, onInput, placeholder) {
  return h('label.field',
    h('span.field-label', label),
    h('input', {
      type: 'text', value: value || '', placeholder: placeholder || '',
      oninput: e => onInput(e.target.value),
    }));
}

/* ------------------------------------------------------------------ chrome */

/* Grouped by what the operator is doing, not alphabetically: the USB side,
   then the wireless and network side, then payloads, then configuration, then
   what the device is telling you. */
const NAV = [
  ['overview', 'Overview'],
  ['cable', 'Cable'],
  ['storage', 'Storage'],
  ['wifi', 'WiFi'],
  ['bluetooth', 'Bluetooth'],
  ['network', 'Network'],
  ['keystrokes', 'Keystrokes'],
  ['reflexes', 'Reflexes'],
  ['loadouts', 'Loadouts'],
  ['templates', 'Templates'],
  ['system', 'System'],
  ['panel', 'Panel'],
  ['journal', 'Journal'],
];

/* Anything a view starts -- a timer, an interval -- registers here and is torn
   down when the view changes. Without this a poll started on Keystrokes keeps
   firing on every other view for the life of the page, holding the old DOM
   alive and making requests nobody asked for. */
let viewCleanups = [];
function onViewTeardown(fn) { viewCleanups.push(fn); }
function runViewTeardown() {
  for (const fn of viewCleanups) { try { fn(); } catch (_) { /* never block navigation */ } }
  viewCleanups = [];
}

/* Keep the current page visible in the mobile nav strip.
   Under 820px the rail becomes one horizontally scrolling row. With twelve
   entries the later ones start off-screen, so navigating to System or Journal
   -- by hash, by a link, or by the strip scrolling back on re-render -- left
   the highlighted item outside the viewport and the operator with no sign of
   where they were. */
function revealCurrentNavItem() {
  const el = document.querySelector('.nav-item[aria-current="page"]');
  if (!el || !el.scrollIntoView) return;
  try {
    el.scrollIntoView({ inline: 'nearest', block: 'nearest' });
  } catch (e) {
    /* jsdom has no layout and older WebKit takes no options object. Neither
       is a reason to fail a render. */
  }
}

/* Pages that moved. A bookmark or a link in someone's notes should not land
   on Overview with no explanation. */
const VIEW_ALIASES = { radio: 'wifi' };

function go(view) {
  if (VIEW_ALIASES[view]) view = VIEW_ALIASES[view];
  if (!Views[view]) view = 'overview';
  runViewTeardown();
  clearToasts();
  State.view = view;
  try { location.hash = '#' + view; } catch (_) {}
  for (const b of document.querySelectorAll('.nav-item')) {
    b.setAttribute('aria-current', b.dataset.view === view ? 'page' : 'false');
  }
  revealCurrentNavItem();
  /* SAY THE PAGE CHANGED.
     go() replaced the whole of #view and moved nothing: focus stayed on the
     nav button that was pressed, the title stayed "P4wnP1 A.L.O.A." forever,
     and #view has no live region of its own. A VoiceOver operator tapped a
     nav item, the entire main content was replaced, and nothing said so --
     with thirteen pages in a horizontally scrolling strip, that is the main
     way around this console. The title is set before the render because the
     render is async; the focus target already carries tabindex="-1" and
     #view:focus { outline: none } (app.css), so both halves were in place. */
  const navEntry = NAV.find(e => e[0] === view);
  try { document.title = (navEntry ? navEntry[1] : view) + ' - P4wnP1 A.L.O.A.'; } catch (_) {}
  Views[view]();
  const main = $('#view');
  if (main && main.focus) { try { main.focus(); } catch (_) {} }
}

function renderConsole() {
  const root = clear($('#root'));
  root.append(
    h('a.skip-link', { href: '#main',
      onclick: e => { e.preventDefault(); const m = $('#view'); if (m) m.focus(); } },
      'Skip to content'),
    h('div.app',
      h('nav.rail', { 'aria-label': 'Sections' },
        h('div.brand',
          h('span', { class: 'brand-mark display display-lg' }, 'P4wnP1'),
          h('span.brand-sub', 'A.L.O.A.')),
        h('div.nav', ...NAV.map(([v, label]) => h('button.nav-item', {
          type: 'button', 'data-view': v, onclick: () => go(v),
        }, h('span', label), v === 'journal' ? h('span', { class: 'count', id: 'journal-count' }, '0') : null))),
        h('div.rail-foot',
          h('div', { style: 'margin-bottom:12px' }, h('div#theme-switch.theme-switch')),
          h('div.field-hint', { style: 'margin-bottom:8px' },
            'Signed in as ', h('span.mono', Api.currentUser() || 'operator')),
          h('button.btn.btn-sm.btn-quiet', {
            onclick: () => sessionsFlow(),
          }, 'Who is signed in'),
          h('button.btn.btn-sm.btn-quiet', {
            onclick: () => changePasswordFlow(),
          }, 'Change password'),
          h('button.btn.btn-sm.btn-quiet', {
            onclick: async () => {
              /* ASK FIRST. This is 30px tall with a transparent border and
                 sits 12px from "Change password"; under 820px the rail-foot
                 is part of the header of every page, so it is in the top
                 chrome of the whole console on a phone. A mis-tap used to
                 sign the operator out instantly -- renderSignIn() clears
                 #root and nothing here persists a draft, so an unsaved
                 HIDScript or a half-filled network form went with it. The
                 command palette's Sign out entry already asks for exactly
                 this reason; the rail button it duplicates did not. */
              const yes = await confirmAction({
                title: 'Sign out of the console?',
                body: 'Only this browser session ends. The device carries on exactly as it is: '
                  + 'the USB gadget stays attached, running scripts keep running, and armed '
                  + 'reflexes keep firing.',
                consequence: 'Anything you have typed and not saved is lost -- the console keeps '
                  + 'no drafts -- and getting back in needs the console password, which is in '
                  + '/root/INITIAL_CREDENTIALS.txt on the device unless it has been changed.',
                confirmLabel: 'Sign out',
              });
              if (!yes) return;
              if (State.stopStream) State.stopStream();
              await Api.logout();
              renderSignIn();
            },
          }, 'Sign out'))),
      h('main.main', { id: 'main' }, h('div#view', { tabindex: '-1' }))));
  renderThemeSwitch();
  startStream();
  go((location.hash || '#overview').slice(1));
}

/* Who is signed in right now.
 *
 * This device is reachable over USB, over its own access point and over
 * Bluetooth, and every operator shares one account. Until this existed there
 * was no way to answer "is anyone else on my device?" -- an operator who
 * suspected a session was not theirs could only change the password, which
 * ends every session including the one the device uses to drive itself.
 *
 * The list carries no tokens. An id names a session well enough to end it and
 * cannot be used as one. */
async function sessionsFlow() {
  const sessions = await guard(() => Api.listSessions(), 'list the signed-in sessions');
  if (!sessions) return;

  const row = s => h('tr',
    h('td',
      s.is_local_script
        ? h('span.pill', h('span.dot.dot-idle'), 'this device')
        : h('span', s.username || 'operator'),
      s.is_current ? h('span.field-hint', { style: 'margin-left:8px' }, '(you)') : null),
    h('td', { style: 'white-space:nowrap' }, fmtRelative(Number(s.issued_at) * 1000)),
    h('td', { style: 'white-space:nowrap' }, fmtRelative(Number(s.expires_at) * 1000)),
    h('td', s.is_current
      ? h('span.field-hint', 'sign out instead')
      : h('button.btn.btn-sm.btn-danger', {
          type: 'button',
          onclick: async () => {
            // Close this dialog BEFORE confirming. Modal.open's focus trap
            // assumes one dialog at a time; stacking a confirmation on top of
            // the list leaves the trap pointing at the dialog underneath.
            close();
            const ok = await confirmAction({
              title: 'End a session',
              body: s.is_local_script
                ? 'This is the credential the device uses to run its own startup and trigger scripts.'
                : 'Whoever is using this session is signed out at once.',
              consequence: s.is_local_script
                ? 'It is re-issued immediately, so the device keeps working. Anything holding the old one stops.'
                : 'They can sign in again with the console password.',
              confirmLabel: 'End it',
              danger: true,
            });
            if (ok) {
              if (await guard(() => Api.revokeSession(s.id), 'end that session') !== undefined) {
                toast('Session ended.');
              }
            }
            sessionsFlow();
          },
        }, 'End')));

  const close = Modal.open({
    title: 'Signed-in sessions',
    body: h('div',
      h('p.field-hint', { style: 'margin-bottom:12px' },
        sessions.length === 1
          ? 'One session is active.'
          : sessions.length + ' sessions are active. "This device" is the credential the device uses to run its own startup and trigger scripts.'),
      dataTable(
        h('thead', h('tr', h('th', 'Who'), h('th', 'Signed in'), h('th', 'Expires'), h('th', ''))),
        h('tbody', ...sessions.map(row)))),
    actions: [h('button.btn.btn-primary', { type: 'button', onclick: () => close() }, 'Close')],
  });
}

/* Changing the console password.
 *
 * The server revokes every session on success, so the operator is signed out by
 * design -- say that up front rather than letting it look like a failure. The
 * 12-character minimum is the server's rule (service/auth), enforced here too so
 * the user is not told about it only after a round trip. */
async function changePasswordFlow() {
  const user = Api.currentUser() || 'admin';
  const values = await promptForm({
    title: 'Change the console password',
    intro: 'Every signed-in session is ended when the password changes, including this one. You will be asked to sign in again.',
    confirmLabel: 'Change password',
    fields: [
      { key: 'old', label: 'Current password', type: 'password', autocomplete: 'current-password' },
      { key: 'next', label: 'New password', type: 'password', autocomplete: 'new-password',
        hint: 'At least 12 characters.' },
      { key: 'again', label: 'New password again', type: 'password', autocomplete: 'new-password' },
    ],
    validate: v => {
      if (!v.old) return 'Enter your current password.';
      if (!v.next) return 'Enter a new password.';
      if (v.next.length < 12) return 'The new password must be at least 12 characters.';
      if (v.next !== v.again) return 'The two new passwords do not match.';
      if (v.next === v.old) return 'The new password is the same as the current one.';
      return null;
    },
  });
  if (!values) return;

  try {
    await Api.changePassword(user, values.old, values.next);
  } catch (e) {
    toast(e.status === 401
      ? 'That current password was rejected.'
      : 'Could not change the password: ' + e.message, true);
    return;
  }
  toast('Password changed. Sign in again with the new one.');
  if (State.stopStream) { State.stopStream(); State.stopStream = null; }
  Api.setToken(null);
  renderSignIn('Your password changed, so every session was ended. Sign in again.');
}

/* ------------------------------------------------------------- sign-in view */

function renderSignIn(message) {
  if (State.stopStream) { State.stopStream(); State.stopStream = null; }
  /* go() keeps document.title on the current page, so without this the title
     still names whatever page was open before the session ended. */
  try { document.title = 'Sign in - P4wnP1 A.L.O.A.'; } catch (_) {}
  const root = clear($('#root'));
  const user = h('input', { type: 'text', autocomplete: 'username', value: 'admin' });
  const pass = h('input', { type: 'password', autocomplete: 'current-password' });
  const err = h('div');

  async function submit(e) {
    if (e) e.preventDefault();
    clear(err);
    if (!user.value || !pass.value) { err.append(banner('Enter a username and password.', 'danger')); return; }
    const btn = $('#signin-btn');
    btn.disabled = true;
    clear(btn).append(h('span.spinner'), ' Signing in');
    try {
      await Api.login(user.value, pass.value);
      renderConsole();
    } catch (ex) {
      btn.disabled = false;
      clear(btn).append('Sign in');
      err.append(banner(ex.status === 401
        ? 'Those credentials were rejected.'
        : 'Sign-in failed: ' + ex.message, 'danger'));
      pass.value = '';
      pass.focus();
    }
  }

  root.append(h('div.signin-wrap',
    h('form.signin', { onsubmit: submit },
      h('div.brand',
        h('span', { class: 'brand-mark display display-lg' }, 'P4wnP1'),
        h('span.brand-sub', 'A.L.O.A.')),
      message ? banner(message, 'warn') : null,
      err,
      h('label.field', h('span.field-label', 'Username'), user),
      h('label.field', h('span.field-label', 'Password'), pass),
      h('button.btn.btn-primary', { id: 'signin-btn', type: 'submit', style: 'width:100%' }, 'Sign in'),
      h('p.field-hint', { style: 'margin-top:16px' },
        'The initial password is generated on the device at first boot and written to ',
        h('span.mono', '/root/INITIAL_CREDENTIALS.txt'), '. Read it over SSH.'))));
  pass.focus();
}

function banner(text, kind) {
  return h('div', { class: 'banner banner-' + (kind || 'warn') }, h('p', text));
}

/* --------------------------------------------------------------- bootstrap */

window.addEventListener('DOMContentLoaded', async () => {
  Theme.apply();
  if (!Api.hasToken()) { renderSignIn(); return; }
  /* A cached token may have expired while the tab was closed. Check it before
     painting a console that would then fail every request. */
  try {
    await Api.whoami();
    renderConsole();
  } catch (e) {
    Api.setToken(null);
    renderSignIn(e instanceof Api.ApiError && e.isAuthFailure
      ? 'Your session expired. Sign in again.' : null);
  }
});

window.addEventListener('hashchange', () => {
  const v = (location.hash || '#overview').slice(1);
  if (v !== State.view && Views[v]) go(v);
});
