/* app.js -- the P4wnP1 operator console.
 *
 * No framework and no build step, on purpose. The smallest supported target is
 * a Pi Zero W: one 1GHz ARM11 core and 512MB of RAM, serving this over USB
 * ethernet or its own access point. Shipping a bundler toolchain and a runtime
 * framework to that device buys nothing and costs boot time, memory and the
 * ability to fix the UI in place with a text editor over SSH.
 */
'use strict';

/* ---------------------------------------------------------------- utilities */

/* Tiny hyperscript. `h('div.card', {onclick: f}, 'text', child)`. Text is
   always set via textContent, never innerHTML, so device-supplied strings
   (SSIDs, hostnames, log lines, script output) cannot inject markup. */
function h(spec, props, ...children) {
  const [tagAndId, ...classes] = String(spec).split('.');
  const [tag, id] = tagAndId.split('#');
  const el = document.createElement(tag || 'div');
  if (id) el.id = id;
  if (classes.length) el.className = classes.join(' ');
  if (props && props.constructor === Object) {
    for (const [k, v] of Object.entries(props)) {
      if (v === null || v === undefined || v === false) continue;
      if (k.startsWith('on') && typeof v === 'function') el.addEventListener(k.slice(2), v);
      else if (k === 'class') el.className += (el.className ? ' ' : '') + v;
      else if (k === 'value') el.value = v;
      else if (k === 'checked') el.checked = !!v;
      else if (k === 'disabled') el.disabled = !!v;
      else el.setAttribute(k, v);
    }
  } else if (props !== undefined && props !== null) {
    children.unshift(props);
  }
  for (const c of children.flat(4)) {
    if (c === null || c === undefined || c === false) continue;
    el.append(c instanceof Node ? c : document.createTextNode(String(c)));
  }
  return el;
}

const $ = sel => document.querySelector(sel);
const clear = el => { while (el.firstChild) el.removeChild(el.firstChild); return el; };

function toast(message, isError) {
  const box = $('#toasts');
  const t = h('div.toast' + (isError ? '.toast-err' : ''), message);
  box.append(t);
  setTimeout(() => t.remove(), isError ? 8000 : 4000);
}

/* Any RPC can fail because the operator just reconfigured the very interface
   they are talking over. Report it, never swallow it, and bounce to sign-in
   only when the token itself is the problem. */
async function guard(fn, what) {
  try { return await fn(); }
  catch (e) {
    if (e instanceof Api.ApiError && e.isAuthFailure) { Api.setToken(null); renderSignIn(); return undefined; }
    toast((what ? what + ': ' : '') + e.message, true);
    return undefined;
  }
}

const fmtTime = ms => new Date(Number(ms) || Date.now())
  .toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false });

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
  view: 'cable',
};

const LOG_CAP = 600;
const LEVEL_NAME = ['debug', 'info', 'warn', 'error'];

/* ------------------------------------------------------- the cable (signature) */

/* The USB functions a composite gadget can expose, in the order they appear to
   a host. `on` and `detail` are read from the live GadgetSettings. */
const USB_FUNCTIONS = [
  { key: 'use_HID_KEYBOARD', name: 'Keyboard', detail: s => s.dev_path_hid_keyboard },
  { key: 'use_HID_MOUSE',    name: 'Mouse',    detail: s => s.dev_path_hid_mouse },
  { key: 'use_HID_RAW',      name: 'Raw HID',  detail: s => s.dev_path_hid_raw },
  { key: 'use_RNDIS',        name: 'RNDIS',    detail: s => s.rndis_settings && s.rndis_settings.host_addr },
  { key: 'use_CDC_ECM',      name: 'CDC ECM',  detail: s => s.cdc_ecm_settings && s.cdc_ecm_settings.host_addr },
  { key: 'use_SERIAL',       name: 'Serial',   detail: () => 'ttyGS0' },
  { key: 'use_UMS',          name: 'Storage',  detail: s => s.ums_settings && (s.ums_settings.file || (s.ums_settings.cdrom ? 'cdrom' : '')) },
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
      h('span.cable-state', h('span', { class: 'dot ' + dot, style: 'margin-right:6px' }), stateText)),
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
  const form = h('div.card',
    h('h2.card-title', 'Composition'),
    h('div.grid-2',
      h('div',
        ...USB_FUNCTIONS.map(fn => h('label.check',
          h('input', {
            type: 'checkbox', checked: !!draft[fn.key],
            onchange: e => { draft[fn.key] = e.target.checked; },
          }),
          h('span', fn.name))),
        h('label.check',
          h('input', {
            type: 'checkbox', checked: draft.enabled !== false,
            onchange: e => { draft.enabled = e.target.checked; },
          }),
          h('span', 'Gadget enabled'))),
      h('div',
        textField('Vendor ID', draft.vid, v => draft.vid = v, '0x1d6b'),
        textField('Product ID', draft.pid, v => draft.pid = v, '0x0137'),
        textField('Manufacturer', draft.manufacturer, v => draft.manufacturer = v),
        textField('Product', draft.product, v => draft.product = v),
        textField('Serial', draft.serial, v => draft.serial = v))),
    h('div.btn-row', { style: 'margin-top:8px' },
      h('button.btn.btn-primary', {
        onclick: async (e) => {
          /* Re-composing the gadget tears down and rebuilds the USB
             functions. If the console is reached over USB ethernet -- the
             normal case -- this request is what kills the connection it
             arrived on. Say so before doing it. */
          const overUsb = /^172\.16\.0\./.test(location.hostname);
          const msg = overUsb
            ? 'Re-composing USB will drop the USB ethernet link this console is using. You will need to reconnect. Continue?'
            : 'Re-compose the USB gadget now? Any host currently attached will see every function disconnect and re-enumerate.';
          if (!confirm(msg)) return;
          e.target.disabled = true;
          const res = await guard(() => Api.rpc('DeployGadgetSetting', draft), 'deploy USB settings');
          e.target.disabled = false;
          if (res) { toast('USB composition deployed.'); Views.cable(); }
        },
      }, 'Deploy composition'),
      h('button.btn', { onclick: () => Views.cable() }, 'Discard changes'),
      h('button.btn.btn-quiet', {
        onclick: async () => {
          const r = await guard(() => Api.rpc('StoreDeployedUSBSettings', { msg: prompt('Store current USB settings as:') || '' }), 'store');
          if (r !== undefined) toast('Stored.');
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
      h('table.data', { style: 'margin-top:24px' },
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
        : h('table.data',
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
    list.length ? h('table.data',
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

const SAMPLE_SCRIPT = `// HIDScript runs on the device and types into the attached host.
// Nothing here executes until you press Run.
layout('us');
typingSpeed(80, 20);
type('hello from P4wnP1\\n');
`;

Views.keystrokes = async function () {
  const main = clear($('#view'));
  main.append(pageHead('Keystrokes',
    'Run HIDScript on the device. Scripts drive the emulated keyboard and mouse against whatever host the cable is in.'));

  if (State.usb && !State.usb.use_HID_KEYBOARD && !State.usb.use_HID_MOUSE) {
    main.append(h('div.banner',
      h('p.banner-title', 'No HID function is active'),
      h('p', 'Neither the keyboard nor the mouse gadget is enabled, so a script has nothing to type into. Enable one under Cable and deploy.')));
  }

  const stored = await guard(() => Api.rpc('ListStoredHIDScripts'), 'list scripts');
  const names = (stored && stored.msgArray) || [];

  const editor = h('textarea', { rows: '14', spellcheck: 'false' });
  editor.value = SAMPLE_SCRIPT;

  const timeoutInput = h('input', { type: 'number', value: '30', min: '0' });

  main.append(h('div.card',
    h('h2.card-title', 'Script'),
    names.length ? h('label.field',
      h('span.field-label', 'Stored scripts'),
      h('select', {
        onchange: async e => {
          const n = e.target.value;
          if (!n) return;
          const r = await guard(() => Api.rpc('FSReadFile', {
            filename: n, folder: 2, /* HIDScripts folder */
          }), 'read script');
          if (r && r.content) {
            try { editor.value = atob(r.content); }
            catch (_) { toast('Script content was not valid base64.', true); }
          }
        },
      }, h('option', { value: '' }, 'Load a stored script...'),
        ...names.map(n => h('option', { value: n }, n)))) : null,
    h('label.field',
      h('span.field-label', 'HIDScript source'),
      editor),
    h('div.row',
      h('label.field', { style: 'max-width:160px' },
        h('span.field-label', 'Timeout (seconds)'),
        timeoutInput,
        h('span.field-hint', '0 means no timeout')),
      h('div.btn-row', { style: 'padding-top:26px' },
        h('button.btn.btn-primary', {
          onclick: async (e) => {
            const src = editor.value;
            if (!src.trim()) { toast('Nothing to run.', true); return; }
            e.target.disabled = true;
            /* The service runs scripts from a path, so the source is written to
               a temp file first. FSCreateTempDirOrFile + FSWriteFile is the
               same route the old client used. */
            const tmp = await guard(() => Api.rpc('FSCreateTempDirOrFile', { dir: false, path: '', prefix: 'console' }), 'create temp file');
            if (!tmp) { e.target.disabled = false; return; }
            const wrote = await guard(() => Api.rpc('FSWriteFile', {
              filename: tmp.resultPath || tmp.path || '', folder: 0,
              content: btoa(unescape(encodeURIComponent(src))), append: false,
            }), 'write script');
            if (wrote === undefined) { e.target.disabled = false; return; }
            const job = await guard(() => Api.rpc('HIDRunScriptJob', {
              scriptPath: tmp.resultPath || tmp.path || '',
              timeoutSeconds: Number(timeoutInput.value) || 0,
            }), 'start script');
            e.target.disabled = false;
            if (job) { toast('Started job ' + job.id + '.'); refreshJobs(); }
          },
        }, 'Run'),
        h('button.btn.btn-danger', {
          onclick: async () => {
            if (!confirm('Cancel every running HIDScript job?')) return;
            const r = await guard(() => Api.rpc('HIDCancelAllScriptJobs'), 'cancel jobs');
            if (r !== undefined) { toast('Cancelled.'); refreshJobs(); }
          },
        }, 'Cancel all')))));

  const jobsCard = h('div.card', h('h2.card-title', 'Running jobs'), h('div#jobs', h('div.empty', 'None.')));
  main.append(jobsCard);
  refreshJobs();
};

async function refreshJobs() {
  const host = $('#jobs');
  if (!host) return;
  const r = await guard(() => Api.rpc('HIDGetRunningScriptJobs'), 'list jobs');
  State.hidJobs = (r && r.ids) || [];
  clear(host);
  if (!State.hidJobs.length) { host.append(h('div.empty', 'None.')); return; }
  host.append(h('table.data',
    h('thead', h('tr', h('th', 'Job'), h('th', 'Actions'))),
    h('tbody', ...State.hidJobs.map(id => h('tr',
      h('td.mono', String(id)),
      h('td',
        h('button.btn.btn-sm', {
          onclick: async () => {
            const r = await guard(() => Api.rpc('HIDGetScriptJobResult', { id }), 'job result');
            if (r) alert('Job ' + id + (r.isFinished ? ' finished' : ' running') + '\n\n' + (r.resultJson || '(no result)'));
          },
        }, 'Result'),
        ' ',
        h('button.btn.btn-sm.btn-danger', {
          onclick: async () => {
            const r = await guard(() => Api.rpc('HIDCancelScriptJob', { id }), 'cancel');
            if (r !== undefined) { toast('Cancelled job ' + id + '.'); refreshJobs(); }
          },
        }, 'Cancel'))))))); 
}

/* --- Reflexes: trigger/action automation ------------------------------- */

const TRIGGER_LABEL = {
  serviceStarted: 'service started', usbGadgetConnected: 'USB host attached',
  usbGadgetDisconnected: 'USB host detached', wifiAPStarted: 'WiFi AP started',
  wifiConnectedAsSta: 'joined WiFi', sshLogin: 'SSH login',
  dhcpLeaseGranted: 'DHCP lease granted', gpioIn: 'GPIO input',
  groupReceive: 'group value', groupReceiveMulti: 'group values',
};
const ACTION_LABEL = {
  bashScript: 'run bash script', hidScript: 'run HIDScript',
  deploySettingsTemplate: 'deploy template', log: 'write log',
  gpioOut: 'GPIO output', groupSend: 'send group value',
};

Views.reflexes = async function () {
  const main = clear($('#view'));
  main.append(pageHead('Reflexes',
    'Rules the device acts on by itself: when a trigger fires, it runs an action. This is what makes it autonomous once the cable is in.'));

  const set = await guard(() => Api.rpc('GetDeployedTriggerActionSet'), 'read trigger actions');
  State.triggers = set;
  const items = (set && set.TriggerActions) || [];

  const describe = (obj, labels) => {
    for (const k of Object.keys(labels)) if (obj && obj[k] !== undefined && obj[k] !== null) return labels[k];
    return 'unrecognised';
  };

  main.append(h('div.card',
    h('h2.card-title', 'Deployed set' + (set && set.Name ? ' -- ' + set.Name : '')),
    items.length ? h('table.data',
      h('thead', h('tr',
        h('th', 'Id'), h('th', 'When'), h('th', 'Then'),
        h('th', 'Active'), h('th', 'One shot'), h('th', 'Locked'))),
      h('tbody', ...items.map(t => h('tr',
        h('td.mono', String(t.id ?? '--')),
        h('td', describe(t, TRIGGER_LABEL)),
        h('td', describe(t, ACTION_LABEL)),
        h('td', h('span', { class: 'dot ' + (t.isActive ? 'dot-ok' : 'dot-idle') })),
        h('td', t.oneShot ? 'yes' : 'no'),
        h('td', t.immutable ? 'yes' : 'no')))))
      : h('div.empty', 'No trigger actions are deployed.')));

  main.append(h('div.card',
    h('h2.card-title', 'Group value'),
    h('p.field-hint', { style: 'margin-top:-8px;margin-bottom:16px' },
      'Group values are how bash scripts, HIDScripts and triggers signal each other on the device.'),
    (() => {
      const g = h('input', { type: 'text', value: '' });
      const v = h('input', { type: 'number', value: '1' });
      return h('div.row',
        h('label.field', h('span.field-label', 'Group'), g),
        h('label.field', { style: 'max-width:140px' }, h('span.field-label', 'Value'), v),
        h('div', { style: 'padding-top:26px' },
          h('button.btn', {
            onclick: async () => {
              if (!g.value.trim()) { toast('Name a group first.', true); return; }
              const r = await guard(() => Api.rpc('FireActionGroupSend',
                { groupName: g.value.trim(), value: Number(v.value) || 0 }), 'send group value');
              if (r !== undefined) toast('Sent.');
            },
          }, 'Send')));
    })()));
};

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
  const startupName = startup && (startup.templateName || startup.msg) || null;

  main.append(h('div.card',
    h('h2.card-title', 'Stored loadouts'),
    names.length ? h('table.data',
      h('thead', h('tr', h('th', 'Name'), h('th', 'Boot default'), h('th', 'Actions'))),
      h('tbody', ...names.map(n => h('tr',
        h('td.mono', n),
        h('td', startupName === n ? 'yes' : ''),
        h('td',
          h('button.btn.btn-sm.btn-primary', {
            onclick: async () => {
              if (!confirm('Deploy "' + n + '" now?\n\nThis reconfigures USB, WiFi and networking together and will very likely drop the connection this console is using.')) return;
              const r = await guard(() => Api.rpc('DeployStoredMasterTemplate', { msg: n }), 'deploy loadout');
              if (r !== undefined) toast('Deployed "' + n + '".');
            },
          }, 'Deploy'),
          ' ',
          h('button.btn.btn-sm', {
            onclick: async () => {
              const r = await guard(() => Api.rpc('SetStartupMasterTemplate', { templateName: n }), 'set boot default');
              if (r !== undefined) { toast('"' + n + '" will load at boot.'); Views.loadouts(); }
            },
          }, 'Use at boot'))))))
      : h('div.empty', 'No loadouts stored yet.')));

  main.append(h('div.card',
    h('h2.card-title', 'Device'),
    h('div.btn-row',
      h('button.btn', {
        onclick: async () => {
          const name = prompt('Back up the template database as:');
          if (!name) return;
          const r = await guard(() => Api.rpc('DBBackup', { msg: name }), 'backup');
          if (r !== undefined) toast('Backed up.');
        },
      }, 'Back up database'),
      h('button.btn.btn-danger', {
        onclick: async () => {
          if (!confirm('Reboot the device now?')) return;
          await guard(() => Api.rpc('Reboot'), 'reboot');
          toast('Reboot requested.');
        },
      }, 'Reboot'),
      h('button.btn.btn-danger', {
        onclick: async () => {
          if (!confirm('Shut the device down?\n\nYou will have to remove and re-insert power to bring it back.')) return;
          await guard(() => Api.rpc('Shutdown'), 'shutdown');
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
        h('span#stream-pill.pill', h('span.dot.dot-idle'), 'connecting'),
        ' ',
        h('button.btn.btn-sm.btn-quiet', { onclick: () => { State.log = []; paintLog(); } }, 'Clear'))),
    h('div#log.log')));
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
    if (map[sub] && State.view === map[sub]) Views[State.view]();
  }
}

function push(entry) {
  State.log.push(entry);
  if (State.log.length > LOG_CAP) State.log.splice(0, State.log.length - LOG_CAP);
  if (State.view === 'journal') paintLog();
  const c = $('#journal-count');
  if (c) c.textContent = String(State.log.length);
}

function refreshCableIfVisible() { if (State.view === 'cable') Views.cable(); }

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
  ['cable', 'Cable'],
  ['radio', 'Radio'],
  ['keystrokes', 'Keystrokes'],
  ['reflexes', 'Reflexes'],
  ['loadouts', 'Loadouts'],
  ['journal', 'Journal'],
];

function go(view) {
  if (!Views[view]) view = 'cable';
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
    h('div.app',
      h('nav.rail',
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
            onclick: async () => {
              const u = Api.currentUser();
              const oldp = prompt('Current password for ' + u + ':');
              if (!oldp) return;
              const newp = prompt('New password (12 characters or more):');
              if (!newp) return;
              if (newp.length < 12) { toast('Too short. Use 12 characters or more.', true); return; }
              const r = await guard(() => Api.changePassword(u, oldp, newp), 'change password');
              if (r !== undefined) { toast('Password changed. Sign in again.'); await Api.logout(); renderSignIn(); }
            },
          }, 'Change password'),
          h('button.btn.btn-sm.btn-quiet', {
            onclick: async () => { if (State.stopStream) State.stopStream(); await Api.logout(); renderSignIn(); },
          }, 'Sign out'))),
      h('main.main', h('div#view'))));
  renderThemeSwitch();
  startStream();
  go((location.hash || '#cable').slice(1));
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
  const v = (location.hash || '#cable').slice(1);
  if (v !== State.view && Views[v]) go(v);
});
