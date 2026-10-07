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
    hint: 'Most often the WiFi regulatory domain is unset -- hostapd refuses to run without a country code -- or wlan0 is rfkill-blocked. Check the regulatory domain under Radio.',
    action: { label: 'Open Radio', view: 'radio' },
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

/* Toast with guidance, and an action button when there is somewhere to go. */
function reportError(e, what) {
  const { message, hint, action } = explainError(e, what);
  const box = $('#toasts');
  if (!box) return;
  const t = h('div.toast.toast-err',
    h('p.toast-title', message),
    hint ? h('p.toast-hint', hint) : null,
    action ? h('button.btn.btn-sm', {
      type: 'button', style: 'margin-top:8px',
      onclick: () => { t.remove(); go(action.view); },
    }, action.label) : null,
    h('button.toast-close', { type: 'button', 'aria-label': 'Dismiss', onclick: () => t.remove() }, '×'));
  box.append(t);
  setTimeout(() => t.remove(), action ? 20000 : 12000);
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
  State.usb = await guard(() => Api.rpc('GetDeployedGadgetSetting'), 'read USB settings');
  const s = State.usb;
  const main = clear($('#view'));

  main.append(pageHead('Cable',
    'The USB side of the device: what it claims to be, and which functions it exposes to a host when the cable goes in.'));
  main.append(renderCable());

  if (!s) return;

  const draft = JSON.parse(JSON.stringify(s));
  State.formDirty = false;
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
          const overUsb = /^172\.16\.0\./.test(location.hostname);
          const yes = await confirmAction({
            title: 'Re-compose the USB gadget?',
            body: 'The device tears down every USB function and rebuilds it with these settings.',
            consequence: overUsb
              ? 'You are reading this over the USB ethernet link, and that link is one of the functions being rebuilt. This console WILL disconnect. It normally comes back within a few seconds -- reload the page. If it does not, reach the device over WiFi or the serial console.'
              : 'Any host currently attached sees every function disconnect and re-enumerate, exactly as if the cable had been pulled out and pushed back in.',
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
      h('button.btn.btn-quiet', {
        onclick: async () => {
          const name = await promptValue({
            title: 'Store this USB composition',
            label: 'Template name',
            placeholder: 'hid-and-storage',
            hint: 'Reusable later from Loadouts, and as part of a whole-device template.',
            validate: v => v ? null : 'Give the template a name.',
          });
          if (!name) return;
          const r = await guard(() => Api.rpc('StoreDeployedUSBSettings', { msg: name }), 'store the template');
          if (r !== undefined) toast('Stored as "' + name + '".');
        },
      }, 'Store as template')));
  main.append(form);
};

/* --- Radio: WiFi + Bluetooth ------------------------------------------- */

const WIFI_MODE = ['unknown', 'access point', 'station', 'disabled'];

Views.radio = async function () {
  const main = clear($('#view'));
  main.append(pageHead('Radio',
    'The wireless side: the access point this device spawns or the network it joins, and the Bluetooth controller.'));

  const [wifi, bt] = await Promise.all([
    guard(() => Api.rpc('GetWiFiState'), 'read WiFi state'),
    guard(() => Api.rpc('GetBluetoothControllerInformation'), 'read Bluetooth controller'),
  ]);
  State.wifi = wifi; State.bt = bt;

  if (wifi) {
    const cs = wifi.currentSettings || {};
    main.append(h('div.card',
      h('h2.card-title', 'WiFi'),
      h('div.figures',
        figure(wifi.ssid || '--', 'ssid'),
        figure(String(wifi.channel || '--'), 'channel'),
        figure(WIFI_MODE[wifi.mode] || String(wifi.mode), 'mode')),
      dataTable( { style: 'margin-top:24px' },
        h('tbody',
          kvRow('Regulatory domain', cs.regulatory || 'unset',
            cs.regulatory ? null : 'hostapd refuses to start without one; the AP will not appear.'),
          kvRow('SSID hidden', cs.hide_ssid ? 'yes' : 'no'),
          kvRow('Nexmon features', cs.nexmon ? 'enabled' : 'off',
            cs.nexmon ? 'KARMA and multi-SSID need a Nexmon-patched firmware blob installed.' : null),
          kvRow('Disabled', cs.disabled ? 'yes' : 'no'),
          kvRow('Template name', cs.name || '--')))));
  }

  if (bt) {
    main.append(h('div.card',
      h('h2.card-title', 'Bluetooth'),
      bt.is_available === false
        ? h('div.banner.banner-danger',
          h('p.banner-title', 'Controller not available'),
          h('p', 'No usable Bluetooth controller was found. On a Pi this usually means the kernel module or the bluetooth service did not come up.'))
        : dataTable(
          h('tbody',
            kvRow('Name', bt.name || '--'),
            kvRow('Short name', bt.short_name || '--'),
            kvRow('Version', String(bt.bluetooth_version ?? '--')),
            kvRow('NAP server', bt.service_network_server_nap ? 'on' : 'off',
              bt.service_network_server_nap ? 'The device offers network access over Bluetooth PAN.' : null),
            kvRow('PANU', bt.service_network_server_panu ? 'on' : 'off'),
            kvRow('GN', bt.service_network_server_gn ? 'on' : 'off')))));
  }

  const eth = await guard(() => Api.rpc('GetAllDeployedEthernetInterfaceSettings'), 'read interfaces');
  State.eth = eth;
  const list = (eth && eth.list) || [];
  const ETH_MODE = ['manual', 'dhcp client', 'dhcp server', 'unmanaged'];
  main.append(h('div.card',
    h('h2.card-title', 'Interfaces'),
    list.length ? dataTable(
      h('thead', h('tr',
        h('th', 'Interface'), h('th', 'Mode'), h('th', 'Address'), h('th', 'In use'))),
      h('tbody', ...list.map(i => h('tr',
        h('td.mono', i.name || '--'),
        h('td', ETH_MODE[i.mode] || String(i.mode)),
        h('td.mono', i.ipAddress4 ? i.ipAddress4 + '/' + (i.netmask4 || '') : '--'),
        h('td', i.settingsInUse ? 'yes' : 'no')))))
      : h('div.empty', 'No managed interfaces reported.')));
};

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
  if (size === 0) { toast('"' + name + '" is empty.', true); return ''; }
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
  editor.value = SAMPLE_SCRIPT;

  const timeoutInput = h('input', { type: 'number', value: '30', min: '0', max: '3600' });
  const runBtn = h('button.btn.btn-primary', { type: 'button' }, 'Run on the host');

  const picker = h('select', {
    'aria-label': 'Load a stored script',
    onchange: async e => {
      const n = e.target.value;
      if (!n) return;
      const src = await readStoredScript(n);
      if (src !== null) { editor.value = src; toast('Loaded "' + n + '".'); }
      e.target.value = '';
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
            const name = await promptValue({
              title: 'Save to the device',
              label: 'File name',
              value: 'payload.js',
              hint: 'Stored in /usr/local/P4wnP1/HIDScripts and listed above.',
              validate: v => {
                if (!v) return 'Give the file a name.';
                if (!/^[A-Za-z0-9._-]+$/.test(v)) return 'Letters, digits, dot, dash and underscore only.';
                if (!v.endsWith('.js')) return 'Use a .js extension.';
                if (v.startsWith('.')) return 'The name cannot start with a dot.';
                return null;
              },
            });
            if (!name) return;
            const r = await guard(() => Api.rpc('FSWriteFile', {
              folder: 2 /* HID_SCRIPTS */, filename: name,
              data: b64encode(editor.value), append: false,
            }), 'save the script');
            if (r !== undefined) { toast('Saved as "' + name + '".'); Views.keystrokes(); }
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
 * GPIO and the multi-value group trigger are deliberately not offered: they
 * need hardware context the console cannot show, and putting them behind a
 * select with no explanation would be worse than leaving them to the CLI. */
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
  { key: 'log', label: 'Write a line to the Journal',
    hint: 'Useful for proving a trigger fires before you attach anything real to it.', fields: [] },
];

const byKey = (list, k) => list.find(x => x.key === k);

function describeOneOf(obj, list) {
  for (const item of list) if (obj && obj[item.key] !== undefined && obj[item.key] !== null) return item.label;
  return 'unrecognised';
}

Views.reflexes = async function () {
  const main = clear($('#view'));
  main.append(pageHead('Reflexes',
    'Rules the device acts on by itself: when something happens, run something. This is what lets it work with nobody at the keyboard.'));

  const [set, hidScripts, bashScripts] = await Promise.all([
    guard(() => Api.rpc('GetDeployedTriggerActionSet'), 'read the reflexes'),
    Api.rpc('ListStoredHIDScripts').catch(() => null),
    Api.rpc('ListStoredBashScripts').catch(() => null),
  ]);
  State.triggers = set;
  const items = (set && set.TriggerActions) || [];
  const scriptOptions = {
    hid: (hidScripts && hidScripts.msgArray) || [],
    bash: (bashScripts && bashScripts.msgArray) || [],
  };

  main.append(renderReflexBuilder(scriptOptions));

  main.append(h('div.card',
    h('div.card-head',
      h('h2.card-title', { style: 'margin:0' },
        'Deployed' + (set && set.Name ? ' -- ' + set.Name : '')),
      h('span.field-hint', items.length
        ? items.filter(t => t.isActive).length + ' of ' + items.length + ' armed'
        : '')),
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
function renderReflexBuilder(scriptOptions) {
  const triggerSel = h('select', { 'aria-label': 'Trigger' },
    ...TRIGGERS.map(t => h('option', { value: t.key }, t.label)));
  const actionSel = h('select', { 'aria-label': 'Action' },
    ...ACTIONS.map(a => h('option', { value: a.key }, a.label)));
  const oneShot = h('input', { type: 'checkbox' });

  const triggerFields = h('div.builder-fields');
  const actionFields = h('div.builder-fields');
  const triggerHint = h('p.field-hint');
  const actionHint = h('p.field-hint');

  /* Build inputs for whichever member is selected, and keep the DOM nodes so
     collect() can read them back without a second lookup table. */
  function renderFields(spec, host, hintEl, store) {
    clear(host); clear(hintEl);
    if (spec.hint) hintEl.append(spec.hint);
    store.inputs = {};
    for (const f of spec.fields) {
      let input;
      if (f.options) {
        const opts = scriptOptions[f.options] || [];
        input = h('select', {},
          h('option', { value: '' }, opts.length ? 'Choose a script...' : 'No scripts stored'),
          ...opts.map(n => h('option', { value: n }, n)));
      } else {
        input = h('input', { type: f.type || 'text', value: f.value || '', placeholder: f.placeholder || '' });
      }
      store.inputs[f.name] = { el: input, spec: f };
      host.append(h('label.field', h('span.field-label', f.label), input));
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

  const [list, startup] = await Promise.all([
    guard(() => Api.rpc('ListStoredMasterTemplate'), 'list loadouts'),
    guard(() => Api.rpc('GetStartupMasterTemplate'), 'read startup loadout'),
  ]);
  const names = (list && list.msgArray) || [];
  const startupName = (startup && startup.msg) || null;

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
              const yes = await confirmAction({
                title: 'Deploy "' + n + '"?',
                body: 'A loadout reconfigures USB, WiFi, Bluetooth and networking together, all at once.',
                consequence: 'Whichever link you are reading this over -- USB, WiFi or Bluetooth -- is almost certainly reconfigured by this. Expect the console to disconnect. Reload after a few seconds; if the network details changed you may need to reconnect to a different SSID or address.',
                confirmLabel: 'Deploy loadout',
              });
              if (!yes) return;
              const r = await guard(() => Api.rpc('DeployStoredMasterTemplate', { msg: n }), 'deploy loadout');
              if (r !== undefined) toast('Deployed "' + n + '".');
            },
          }, 'Deploy'),
          ' ',
          h('button.btn.btn-sm', {
            onclick: async () => {
              /* StringMessage's field is `msg`. Sending `templateName` was
                 accepted and silently discarded by the bridge, so this set the
                 boot default to an EMPTY string -- clicking "Use at boot"
                 erased the boot configuration instead of setting it. */
              const r = await guard(() => Api.rpc('SetStartupMasterTemplate', { msg: n }), 'set the boot default');
              if (r !== undefined) { toast('"' + n + '" will load at boot.'); Views.loadouts(); }
            },
          }, 'Use at boot'))))))
      : h('div.empty', 'No loadouts stored yet.')));

  main.append(h('div.card',
    h('h2.card-title', 'Device'),
    h('div.btn-row',
      h('button.btn', {
        onclick: async () => {
          const name = await promptValue({
            title: 'Back up the template database',
            label: 'Backup name',
            placeholder: 'before-engagement',
            hint: 'Captures every stored template: USB, WiFi, Bluetooth, network and reflexes.',
            validate: v => {
              if (!v) return 'Give the backup a name.';
              if (!/^[A-Za-z0-9._-]+$/.test(v)) return 'Letters, digits, dot, dash and underscore only.';
              return null;
            },
          });
          if (!name) return;
          const r = await guard(() => Api.rpc('DBBackup', { msg: name }), 'back up the database');
          if (r !== undefined) toast('Backed up as "' + name + '".');
        },
      }, 'Back up database'),
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

function paintLog() {
  const box = $('#log');
  if (!box) return;
  clear(box);
  if (!State.log.length) { box.append(h('div.empty', 'No events yet.')); return; }
  for (const e of State.log) {
    box.append(h('div', { class: 'log-line' + (e.lv === 2 ? ' lv-warn' : e.lv === 3 ? ' lv-err' : '') },
      h('span.log-t', fmtTime(e.t)),
      h('span.log-src', e.src),
      h('span.log-msg', e.msg)));
  }
  box.scrollTop = box.scrollHeight;
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
    const map = { 0: 'cable', 1: 'radio', 3: 'radio', 5: 'reflexes' };
    if (map[sub]) reloadViewOnStateChange(map[sub]);
  }
}

function push(entry) {
  State.log.push(entry);
  if (State.log.length > LOG_CAP) State.log.splice(0, State.log.length - LOG_CAP);
  if (State.view === 'journal') paintLog();
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
    dataTable( ...children));
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

const NAV = [
  ['overview', 'Overview'],
  ['cable', 'Cable'],
  ['radio', 'Radio'],
  ['keystrokes', 'Keystrokes'],
  ['reflexes', 'Reflexes'],
  ['loadouts', 'Loadouts'],
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

function go(view) {
  if (!Views[view]) view = 'overview';
  runViewTeardown();
  State.view = view;
  try { location.hash = '#' + view; } catch (_) {}
  for (const b of document.querySelectorAll('.nav-item')) {
    b.setAttribute('aria-current', b.dataset.view === view ? 'page' : 'false');
  }
  Views[view]();
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
            onclick: () => changePasswordFlow(),
          }, 'Change password'),
          h('button.btn.btn-sm.btn-quiet', {
            onclick: async () => { if (State.stopStream) State.stopStream(); await Api.logout(); renderSignIn(); },
          }, 'Sign out'))),
      h('main.main', { id: 'main' }, h('div#view', { tabindex: '-1' }))));
  renderThemeSwitch();
  startStream();
  go((location.hash || '#overview').slice(1));
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
        h('span.mono', '/root/INITIAL_CREDENTIALS.txt'), '. Read it over SSH.'),
      h('div.colophon',
        'Fraunces for identity and figures, Inter for interface text, the system monospace for measured values. ',
        'Warm paper and two golds: a deep brass that stays legible as small text, and a brighter tone reserved for marks that carry no words. ',
        'Dark mode is true black.'))));
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
