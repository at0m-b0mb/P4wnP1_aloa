/* view-bluetooth.js -- the Bluetooth controller page.
 *
 * The Radio page carries a small read-only Bluetooth card. This is the page
 * where the controller can actually be CHANGED: its name, the flags that
 * decide who may find it and pair with it, the PAN services it offers, the
 * pairing PIN, and the stored templates.
 *
 * FOUR THINGS SHAPED THIS FILE, all read out of the service rather than
 * guessed. Each one is the difference between a page that looks right and a
 * page that is right.
 *
 * 1. DEPLOY SENDS THE WHOLE CONTROLLER, NOT A DIFF.
 *    BluetoothControllerInformationFromRpc (service/bluetooth/controller.go)
 *    reads exactly four things off the request: Name, CurrentSettings and the
 *    three ServiceNetworkServer* booleans. Everything else in the message is
 *    ignored. UpdateSettingsFromChangedControllerInformation then DIFFS that
 *    against the live controller and writes every field that differs.
 *
 *    So a payload carrying one flag turns the other fifteen OFF, and a payload
 *    with no `name` renames the device to the empty string. Every deploy from
 *    this page therefore sends a complete copy of the live settings with the
 *    operator's edits applied on top. This is not belt-and-braces; sending
 *    less is actively destructive.
 *
 * 2. THE DEPLOY PATH ONLY WRITES NINE OF THE SIXTEEN FLAGS.
 *    powered, connectable, fast_connectable, discoverable, bondable,
 *    link_level_security, secure_simple_pairing, high_speed and low_energy.
 *    br_edr, advertising, secure_connections, debug_keys, privacy,
 *    controller_configuration and static_address are read back and never
 *    written -- and neither is short_name, despite being a settable alias on
 *    the controller. Those are shown as status here, not offered as controls.
 *    A switch that silently does nothing is worse than no switch.
 *
 * 3. `supported_settings` IS A CAPABILITY MASK AND IT IS LOAD-BEARING.
 *    A controller advertises which of the sixteen it implements. Offering a
 *    toggle for one it does not means a deploy that errors out partway,
 *    after some other flags have already been written. Toggles here are
 *    gated on the mask.
 *
 * 4. BLUETOOTH IS OFTEN SIMPLY NOT THERE.
 *    Every Bluetooth call in the service begins with IsServiceAvailable() and
 *    returns ErrBtSvcNotAvailable if not, which the RPC layer maps to
 *    Unavailable -> HTTP 503. On a Pi Zero W the controller frequently is not
 *    ready when the service starts. That is a normal state for this hardware,
 *    not a fault, so the reads here are not routed through guard(): a device
 *    without Bluetooth gets one explanation in the page instead of a stack of
 *    error toasts on every visit.
 */
'use strict';

(function () {

  /* Every field of BluetoothControllerSettings, in proto order
     (proto/grpc.proto). The list is complete on purpose: a deploy payload is
     built from it, and a key missing here would be sent as false. */
  const SETTING_KEYS = [
    'powered', 'connectable', 'fast_connectable', 'discoverable', 'bondable',
    'link_level_security', 'secure_simple_pairing', 'br_edr', 'high_speed',
    'low_energy', 'advertising', 'secure_connections', 'debug_keys', 'privacy',
    'controller_configuration', 'static_address',
  ];

  /* The flags the deploy path writes, so these are the ones offered as
     controls. `what` says what the setting does and what goes wrong. */
  const EDITABLE = [
    { key: 'powered', label: 'Powered',
      what: 'The radio. Off means the controller does nothing at all: not discoverable, '
          + 'not connectable, no PAN, and already-connected devices drop.' },
    { key: 'connectable', label: 'Connectable',
      what: 'Accept incoming connections. With this off, a device that already knows this '
          + 'one cannot reach it, and Discoverable has no effect either.' },
    { key: 'discoverable', label: 'Discoverable',
      what: 'Answer scans, so nearby phones list this device by name. This is the loudest '
          + 'thing Bluetooth does and the easiest way to be noticed. Turn it on while you '
          + 'pair and off again afterwards; bonded devices reconnect without it.' },
    { key: 'bondable', label: 'Bondable',
      what: 'Allow pairing. A bonded device keeps its keys and reconnects later without '
          + 'being asked again -- including after this device reboots.' },
    { key: 'secure_simple_pairing', label: 'Secure Simple Pairing',
      what: 'Pairing with per-session keys instead of a fixed PIN. This device has no '
          + 'screen and no buttons, so SSP can only run in "just works" mode: nothing is '
          + 'displayed, nothing is confirmed, and anything in range that asks to pair '
          + 'succeeds. Turn SSP off and the PIN below is demanded instead -- older and '
          + 'weaker cryptographically, but something has to be known. High Speed needs '
          + 'SSP on, so the two trade against each other.' },
    { key: 'high_speed', label: 'High Speed',
      what: 'Carry data over the 802.11 radio rather than the Bluetooth one. Without it a '
          + 'NAP client pairs, gets a DHCP lease, and then runs too slowly to be usable. '
          + 'Requires Secure Simple Pairing.' },
    { key: 'low_energy', label: 'Low Energy',
      what: 'Bluetooth LE. Nothing on this device uses it -- the PAN services below are '
          + 'all classic Bluetooth -- so it is spare capability, and a radio that is '
          + 'advertising is a radio that can be seen.' },
    { key: 'link_level_security', label: 'Link level security',
      what: 'Demand legacy authentication on every link. Old, and some hosts will refuse '
          + 'to connect at all while it is on.' },
  ];

  /* Read back, never written by the deploy path. Shown so the operator can see
     the controller's real state, with no control attached. */
  const READ_ONLY = [
    { key: 'fast_connectable', label: 'Fast connectable',
      what: 'Shortened page scan interval. Deliberately not offered as a control: the '
          + 'service writes the Connectable value into this setting instead of this one '
          + '(SetFastConnectable in service/bluetooth/controller.go), so a switch here '
          + 'would not do what its label says.' },
    { key: 'br_edr', label: 'BR/EDR',
      what: 'Classic Bluetooth, as opposed to LE-only operation.' },
    { key: 'advertising', label: 'Advertising',
      what: 'LE advertising is running.' },
    { key: 'secure_connections', label: 'Secure Connections',
      what: 'The stronger link encryption added in Bluetooth 4.1.' },
    { key: 'debug_keys', label: 'Debug keys',
      what: 'The controller is using fixed, publicly known debug keys. If this is on, '
          + 'treat every Bluetooth link as cleartext to anyone listening.' },
    { key: 'privacy', label: 'Privacy',
      what: 'LE privacy: resolvable random addresses instead of a fixed one.' },
    { key: 'controller_configuration', label: 'Controller configuration',
      what: 'The controller is in configuration mode rather than operating normally.' },
    { key: 'static_address', label: 'Static address',
      what: 'A static random LE address is set.' },
  ];

  /* The three PAN server services, and the proto field each one rides on. */
  const SERVICES = [
    { key: 'service_network_server_nap', draft: 'nap', label: 'NAP',
      title: 'Network Access Point',
      what: 'Hand out network access over Bluetooth, the way a phone tethers. This is the '
          + 'one that gets used: a paired phone joins and reaches whatever this device can '
          + 'route to.' },
    { key: 'service_network_server_panu', draft: 'panu', label: 'PANU',
      title: 'Personal Area Network User',
      what: 'Act as a client-side PAN endpoint rather than a provider.' },
    { key: 'service_network_server_gn', draft: 'gn', label: 'GN',
      title: 'Group ad-hoc Network',
      what: 'Peer-to-peer PAN with no uplink, for a group of devices talking only to each '
          + 'other.' },
  ];

  /* HCI version byte -> core specification, from the Bluetooth SIG's assigned
     numbers. The raw byte is always shown next to it, so if this table is ever
     behind the spec the real value is still on screen. */
  const HCI_VERSION = {
    0: '1.0b', 1: '1.1', 2: '1.2', 3: '2.0 + EDR', 4: '2.1 + EDR', 5: '3.0 + HS',
    6: '4.0', 7: '4.1', 8: '4.2', 9: '5.0', 10: '5.1', 11: '5.2', 12: '5.3', 13: '5.4',
  };

  /* The bridge the service puts PAN clients onto, and its default address.
     Both are constants in service/defaults.go; they are named here so the
     page can point at something an operator can actually go and look at. */
  const BRIDGE_NAME = 'bteth';
  const BRIDGE_NET = '172.26.';

  /* The PIN every image ships with. /root/INITIAL_CREDENTIALS.txt prints it on
     first boot and tells the operator to change it in the web client. */
  const SHIPPED_PIN = '1337';

  /* ----------------------------------------------------------- small helpers */

  /* protojson encodes a `bytes` field as base64. b64decode() in ui.js runs the
     result through TextDecoder, which is right for scripts and wrong for a MAC
     address -- any byte above 0x7F comes back as U+FFFD. These are raw bytes,
     so read them as bytes. */
  function hexBytes(b64, sep) {
    if (typeof b64 !== 'string' || b64 === '') return null;
    let bin;
    try { bin = atob(b64); } catch (_) { return null; }
    if (!bin.length) return null;
    const out = [];
    for (let i = 0; i < bin.length; i++) {
      out.push(bin.charCodeAt(i).toString(16).padStart(2, '0'));
    }
    return out.join(sep);
  }

  /* A complete settings object: all sixteen keys, explicitly. See note 1. */
  function fullSettings(src) {
    const out = {};
    for (const k of SETTING_KEYS) out[k] = !!(src && src[k]);
    return out;
  }

  const onOff = v => (v ? 'on' : 'off');

  /* True when this console is being reached over the Bluetooth bridge, which
     makes the console itself one of the things a disconnect can drop. */
  function onBluetoothBridge() {
    return new RegExp('^' + BRIDGE_NET.replace(/\./g, '\\.')).test(location.hostname);
  }

  /* A checkbox row in the Cable page's style. `disabled` renders the same row
     with the checkbox greyed out -- still showing the flag's real state, but
     not operable -- which is how an unsupported flag appears. */
  function toggleRow({ label, checked, what, disabled, onchange }) {
    return h('div.fn-choice',
      h('label.check',
        h('input', { type: 'checkbox', checked: !!checked, disabled: !!disabled, onchange }),
        h('span.fn-choice-name', label)),
      what ? h('p.fn-choice-what', what) : null);
  }

  /* Read a Bluetooth RPC without raising a toast for "there is no controller".
     Auth failures still go through the normal path -- an expired token must
     bounce to sign-in, not be mistaken for missing hardware. */
  async function read(method) {
    try { return { ok: true, value: await Api.rpc(method) }; }
    catch (e) {
      if (e instanceof Api.ApiError && e.isAuthFailure) {
        Api.setToken(null);
        renderSignIn();
        return { ok: false, gone: true };
      }
      return { ok: false, error: e };
    }
  }

  /* ------------------------------------------------------------------- view */

  Views.bluetooth = async function () {
    const main = clear($('#view'));
    main.append(pageHead('Bluetooth',
      'The Bluetooth controller: what it is called, who is allowed to find it and pair '
      + 'with it, and whether it hands out network access.'));

    const [ciRes, agentRes, listRes] = await Promise.all([
      read('GetBluetoothControllerInformation'),
      read('GetBluetoothAgentSettings'),
      read('ListStoredBluetoothSettings'),
    ]);
    if (ciRes.gone || agentRes.gone || listRes.gone) return;

    const ci = ciRes.ok ? (ciRes.value || {}) : null;
    const agent = agentRes.ok ? (agentRes.value || {}) : null;
    const names = (listRes.ok && listRes.value && listRes.value.msgArray) || [];
    State.bt = ci;

    /* The controller is usable only if the read succeeded AND the service says
       so. is_available is emitted even when false (the bridge sets
       EmitUnpopulated), so false here is a statement, not a missing key. */
    const haveCi = !!ci && ci.is_available !== false;

    /* State.formDirty is GLOBAL (app.js), and reloadViewOnStateChange() and
       refreshCableIfVisible() both consult it. Leaving it true on the way out
       of this page therefore degrades every other page: Radio and Cable stop
       auto-refreshing on a state-change event and start asking first instead.
       So it is cleared here -- before the no-controller branch, which used to
       return without clearing it -- and again on teardown. */
    State.formDirty = false;
    onViewTeardown(() => { State.formDirty = false; });

    /* --- no controller ---------------------------------------------------- */

    if (!haveCi) {
      const detail = ciRes.ok
        ? 'The service answered, and reported that it has no usable controller.'
        : explainError(ciRes.error, 'read the Bluetooth controller').hint;
      main.append(h('div.card',
        h('h2.card-title', 'No Bluetooth controller'),
        h('div.banner.banner-danger',
          /* Scoped to the controller, not to the page. The PIN card and the
             template list below are read and rendered separately and can
             still be live, so a blanket "nothing here can be changed" would
             be contradicted further down the same page. */
          h('p.banner-title', 'The controller cannot be read, so none of its settings '
            + 'can be changed'),
          h('p', detail || 'The controller could not be read.')),
        h('p.field-hint',
          'Three things cause this, in rough order of likelihood. The board has no '
          + 'Bluetooth at all -- a Pi Zero without the W does not. Or bluetoothd is not '
          + 'running, so there is no stack to talk to. Or the controller was simply not '
          + 'ready when the service started: on a Pi Zero W this is common, and it is why '
          + 'a boot-time Bluetooth template can come back as a bare "Failed" while the '
          + 'same template deploys fine by hand a minute later.'),
        h('p.field-hint',
          'Over SSH: "systemctl status bluetooth" and "hciconfig -a" say which of the '
          + 'three it is. If the controller appears later, reload this page -- the service '
          + 'picks it up without a restart. Everything else on the device is unaffected.'),
        h('div.btn-row',
          h('button.btn', { type: 'button', onclick: () => Views.bluetooth() }, 'Check again'))));
    }

    /* --- what the controller is ------------------------------------------ */

    if (haveCi) {
      const cur = ci.current_settings || null;
      const sup = ci.supported_settings || null;
      const mac = hexBytes(ci.address, ':');
      const cod = hexBytes(ci.class_of_device, ' ');
      const ver = Number(ci.bluetooth_version);
      const verName = HCI_VERSION[ver];
      const svcOn = SERVICES.filter(s => ci[s.key]).length;

      main.append(h('div.card',
        h('h2.card-title', 'Controller'),
        h('div.figures',
          figure(ci.name || '--', 'name'),
          figure(cur ? onOff(cur.powered) : '?', 'radio'),
          figure(String(svcOn), 'pan services')),
        dataTable({ style: 'margin-top:24px' },
          h('tbody',
            kvRow('Address', mac || 'unknown',
              mac ? 'Fixed in the chip and broadcast on every connection. A device that '
                  + 'has seen this address once can recognise it again.'
                  : 'The controller did not report an address.'),
            kvRow('Core spec', Number.isFinite(ver) ? (verName ? verName + '  (HCI version ' + ver + ')' : 'HCI version ' + ver) : 'unknown',
              (Number.isFinite(ver) && !verName)
                ? 'The controller reports a version byte this page has no name for.'
                : null),
            kvRow('Vendor', Number.isFinite(Number(ci.manufacturer)) ? String(ci.manufacturer) : 'unknown',
              'Bluetooth SIG company identifier for the chip. A Pi Zero W reports 15, '
              + 'which is Broadcom.'),
            kvRow('Class of device', cod || 'unknown',
              'Three raw bytes saying what kind of device this claims to be. Nothing in '
              + 'this product decodes or sets them.'),
            kvRow('Short name', ci.short_name || 'unset',
              'The abbreviated name used where the full one will not fit. Read-only here: '
              + 'the deploy path ignores this field, so a value typed in would be '
              + 'discarded without complaint.')))));

      /* --- the flags that can be changed --------------------------------- */

      /* The capability mask decides what is offered. Without it, nothing is:
         a deploy that asks a controller for a setting it does not implement
         fails partway through, having already written the flags ahead of it. */
      const draft = {
        name: ci.name || '',
        settings: fullSettings(cur),
        nap: !!ci[SERVICES[0].key],
        panu: !!ci[SERVICES[1].key],
        gn: !!ci[SERVICES[2].key],
      };
      const markDirty = () => { State.formDirty = true; };

      const settingsCard = h('div.card', h('h2.card-title', 'Settings'));

      /* Two different missing reads, both of which have to disable the form.
         `supported_settings` for the reason below. `current_settings` because
         a deploy sends the WHOLE sixteen (note 1), and with nothing to copy
         from, fullSettings() yields sixteen falses -- so flipping one switch
         would send powered:false and drop every Bluetooth connection, while
         deployConsequence(), having no `cur` to compare against, would have
         announced that nothing disconnects. An honest read-only table is the
         only safe thing to show. */
      if (!sup || !cur) {
        settingsCard.append(h('div.banner.banner-danger',
          h('p.banner-title', sup
            ? 'Read-only: the controller did not report its current settings'
            : 'Read-only: the capability mask is missing'),
          h('p', sup
            ? 'A deploy from this page replaces all sixteen flags at once, and with no '
              + 'reading of the live ones there is nothing to carry over -- so changing '
              + 'one switch would turn the other fifteen off, the radio among them. '
              + 'Reload to try the read again.'
            : 'The controller did not report which settings it supports, so this page '
              + 'will not offer to change any of them. Asking a controller for a setting '
              + 'it does not implement fails halfway through a deploy, with the earlier '
              + 'flags already written. Reload to try the read again.')));
        settingsCard.append(dataTable(h('tbody',
          ...EDITABLE.map(f => kvRow(f.label, cur ? onOff(cur[f.key]) : 'unknown', f.what)))));
      } else {
        settingsCard.append(h('p.field-hint', { style: 'margin:-4px 0 18px' },
          'Nothing here takes effect until you press Deploy, and Deploy sends all of it '
          + 'at once. A setting this controller does not implement is shown greyed out, '
          + 'with its real state still visible, rather than being offered and then '
          + 'refused.'));

        settingsCard.append(h('div.grid-2',
          h('div',
            h('h3.card-title', 'Identity and pairing'),
            textField('Name', draft.name, v => { draft.name = v; markDirty(); }, 'P4wnP1'),
            h('p.field-hint', { style: 'margin:-8px 0 18px' },
              'What nearby devices see in their Bluetooth list. Leave it blank and the '
              + 'controller is renamed to nothing, which is not the same as hiding it -- '
              + 'use Discoverable for that.'),
            ...EDITABLE.map(f => {
              const supported = !!sup[f.key];
              return toggleRow({
                label: f.label,
                checked: draft.settings[f.key],
                disabled: !supported,
                what: supported ? f.what
                  : f.what + ' This controller does not report support for it, so it is '
                           + 'not offered.',
                onchange: e => { draft.settings[f.key] = e.target.checked; markDirty(); },
              });
            })),
          h('div',
            h('h3.card-title', 'Network services (PAN)'),
            h('p.field-hint', { style: 'margin:-8px 0 18px' },
              'Bluetooth network bridging. Turning any of these on creates the '
              + BRIDGE_NAME + ' bridge and puts clients on it; turning the last one off '
              + 'tears the bridge down. A client only gets a working connection if DHCP '
              + 'is running on ' + BRIDGE_NAME + ' and hands out a router and a DNS '
              + 'server -- an Android phone pairs, takes the lease and then goes quiet '
              + 'without them. Check the Deployed-now table on Network for ' + BRIDGE_NAME
              + '; its default address is ' + BRIDGE_NET + '0.1.'),
            ...SERVICES.map(s => toggleRow({
              label: s.label + ' -- ' + s.title,
              checked: draft[s.draft],
              what: s.what,
              onchange: e => { draft[s.draft] = e.target.checked; markDirty(); },
            })))));

        settingsCard.append(h('div.btn-row', { style: 'margin-top:8px' },
          h('button.btn.btn-primary', {
            type: 'button',
            onclick: async (e) => {
              const changes = describeChanges(ci, cur, draft);
              if (!changes.length) {
                toast('Nothing has been changed, so there is nothing to deploy.');
                return;
              }
              const yes = await confirmAction({
                title: 'Deploy these Bluetooth settings?',
                body: changes.join(' '),
                consequence: deployConsequence(ci, cur, draft),
                confirmLabel: 'Deploy',
                danger: !!(cur && cur.powered && !draft.settings.powered),
              });
              if (!yes) return;
              e.target.disabled = true;
              /* The full settings object and the name go every time. A partial
                 payload here turns off whatever it omits -- see note 1. */
              const res = await guard(
                () => Api.rpc('DeployBluetoothControllerInformation', {
                  name: draft.name,
                  current_settings: draft.settings,
                  service_network_server_nap: draft.nap,
                  service_network_server_panu: draft.panu,
                  service_network_server_gn: draft.gn,
                }),
                'deploy the Bluetooth settings');
              e.target.disabled = false;
              if (res !== undefined) {
                State.formDirty = false;
                toast('Bluetooth settings deployed.');
                Views.bluetooth();
              }
            },
          }, 'Deploy'),
          h('button.btn', {
            type: 'button',
            onclick: () => { State.formDirty = false; Views.bluetooth(); },
          }, 'Discard changes'),
          /* Store the EDIT, not the running configuration.
             StoreDeployedBluetoothSettings (in Templates below) snapshots what
             is live. This one keeps what is on screen, which is how you
             prepare a profile you do not want to switch to right now -- a
             quiet one, say -- without dropping the link you are using. */
          h('button.btn.btn-quiet', {
            type: 'button',
            onclick: async () => {
              /* The template must carry a PIN. The service reads settings.As
                 without a nil check when deploying, so a template stored
                 without an agent section applies the controller half and then
                 errors on the PIN -- leaving the controller changed. If the
                 PIN could not be read, this is not offered. */
              if (!agent || typeof agent.pin !== 'string' || agent.pin === '') {
                await showDetail({
                  title: 'Cannot store these settings',
                  body: 'A Bluetooth template holds the controller settings AND the '
                      + 'pairing PIN together, and the PIN could not be read from this '
                      + 'device.\n\nStoring a template without one produces something '
                      + 'that deploys the controller half and then fails on the PIN, '
                      + 'leaving the controller changed and the PIN untouched. So this '
                      + 'is refused rather than offered.\n\nDeploy these settings first, '
                      + 'then use "Store as template" under Templates, which snapshots '
                      + 'both halves on the device itself.',
                });
                return;
              }
              const name = await promptValue({
                title: 'Store these settings as a template',
                label: 'Template name',
                placeholder: 'bt-quiet',
                hint: 'Stores what is on screen, including the pairing PIN as it is now. '
                    + 'Nothing is deployed. An existing template of the same name is '
                    + 'replaced.',
                validate: v => (v ? null : 'Give the template a name.'),
              });
              if (!name) return;
              const wrapped = {
                ci: {
                  name: draft.name,
                  current_settings: draft.settings,
                  service_network_server_nap: draft.nap,
                  service_network_server_panu: draft.panu,
                  service_network_server_gn: draft.gn,
                },
                as: { pin: agent.pin },
              };
              const res = await guard(
                () => Api.rpc('StoreBluetoothSettings', { TemplateName: name, settings: wrapped }),
                'store the Bluetooth template');
              if (res !== undefined) {
                toast('Stored as "' + name + '". Nothing was deployed.');
                Views.bluetooth();
              }
            },
          }, 'Store without deploying')));
      }

      main.append(settingsCard);

      /* --- what the controller reports but will not change ---------------- */

      main.append(h('div.card',
        h('h2.card-title', 'Reported, not settable'),
        h('p.field-hint', { style: 'margin:-4px 0 14px' },
          'The controller reports these and the deploy path never writes them, so they '
          + 'are listed rather than offered. "Supported" is what the chip implements; '
          + '"now" is what is actually set.'),
        dataTable(
          h('thead', h('tr', h('th', 'Setting'), h('th', 'Supported'), h('th', 'Now'))),
          h('tbody', ...READ_ONLY.map(f => h('tr',
            h('td', f.label, h('div.field-hint', f.what)),
            h('td.mono', sup ? (sup[f.key] ? 'yes' : 'no') : '?'),
            h('td.mono', cur ? onOff(cur[f.key]) : '?')))))));

      /* --- joining someone else's PAN ------------------------------------- */

      main.append(h('div.card',
        h('h2.card-title', 'Join another device\'s network'),
        h('p.field-hint', { style: 'margin:-4px 0 14px' },
          'The services above make this device a PAN provider. This does the opposite: it '
          + 'connects out, to a PAN offered by a device that is already paired. The '
          + 'service looks the remote device up among the ones this controller already '
          + 'knows -- there is no scan RPC anywhere in this product, so a device that has '
          + 'never been paired cannot be reached from here.'),
        h('div.btn-row',
          h('button.btn', {
            type: 'button',
            onclick: async () => {
              /* Disconnecting drops that device's network link, and if the
                 console is being read over the Bluetooth bridge, one of the
                 devices this can drop is the one asking. Same rule as the
                 Deploy consequence: say it before, not after. */
              const r = await promptForm({
                title: 'Connect or disconnect a PAN',
                intro: 'Acts on one remote device, immediately. Nothing is stored and '
                     + 'nothing survives a reboot. Disconnecting takes that device\'s '
                     + 'network connection away at once.'
                     + (onBluetoothBridge()
                        ? ' You are reading this console over a ' + BRIDGE_NET + 'x '
                          + 'address, which is the Bluetooth bridge -- so if the address '
                          + 'below is the device you are on, this console is what '
                          + 'disconnects. Reach the device over USB or WiFi to undo it.'
                        : ''),
                confirmLabel: 'Apply',
                fields: [
                  { key: 'mac', label: 'Remote device address',
                    placeholder: 'AA:BB:CC:DD:EE:FF',
                    hint: 'Must already be paired with this device.' },
                  { key: 'action', label: 'Action', options: ['connect', 'disconnect'] },
                  { key: 'type', label: 'Service', options: ['NAP', 'PANU', 'GN'],
                    hint: 'Which of the remote device\'s services to join. Ignored when '
                        + 'disconnecting -- the service drops the whole link.' },
                ],
                validate: v => {
                  if (!/^([0-9A-Fa-f]{2}:){5}[0-9A-Fa-f]{2}$/.test(v.mac || '')) {
                    return 'Six hex pairs separated by colons, e.g. AA:BB:CC:DD:EE:FF.';
                  }
                  return null;
                },
              });
              if (!r) return;
              const connect = r.action === 'connect';
              /* register_or_unregister means register/unregister for a server
                 and connect/disconnect for a client; server_or_connect false
                 selects the client side. bridge_name is deliberately omitted:
                 the service ignores it and uses its own bridge. */
              const res = await guard(
                () => Api.rpc('SetBluetoothNetworkService', {
                  register_or_unregister: connect,
                  server_or_connect: false,
                  type: r.type,
                  mac_or_name: r.mac,
                }),
                connect ? 'connect to that device' : 'disconnect that device');
              if (res !== undefined) {
                toast(connect ? 'Connect requested for ' + r.mac + '.'
                              : 'Disconnect requested for ' + r.mac + '.');
              }
            },
          }, 'Connect or disconnect'))));
    }

    /* --- the pairing PIN -------------------------------------------------- */

    const pinCard = h('div.card', h('h2.card-title', 'Pairing PIN'));
    /* Which SSP branch this device is actually in, read back rather than
       assumed. GetDefaultBluetoothSettings (service/defaults.go) ships
       SecureSimplePairing: false -- "we start with PIN based auth" -- and
       HighSpeed: false with it, because High Speed needs SSP on. The explainer
       below had that backwards and told the operator the PIN was never asked
       for on a device where it is the only thing asked for. `cur` is scoped to
       the controller card above, so the flag is re-read here; a controller that
       could not be read gives "unknown", not a guess. */
    const sspSettings = (ci && ci.current_settings && typeof ci.current_settings === 'object')
      ? ci.current_settings : null;
    const sspKnown = haveCi && !!sspSettings;
    const sspOn = sspKnown ? !!sspSettings.secure_simple_pairing : false;
    /* An absent `pin` key is NOT an empty PIN. The bridge marshals with
       EmitUnpopulated, so a working agent always sends the field even when it
       is "" -- which makes a missing key a failed read, and saying "no PIN is
       set" about one would be inventing a fact. */
    if (!agent || typeof agent.pin !== 'string') {
      /* Why the read failed is not the same question in both cases, and saying
         "this device has no controller" on a page that is at that moment
         showing a populated Controller card above would be plainly false. The
         PIN lives in the pairing agent, not in the controller, so the two reads
         can fail independently. */
      pinCard.append(
        h('p.field-hint',
          haveCi
            ? 'The PIN could not be read, even though the controller above was read '
              + 'fine. The PIN belongs to the pairing agent rather than to the '
              + 'controller, so the two are read separately and the agent is what '
              + 'failed -- most often because it is not registered on the bus. Nothing '
              + 'about the PIN can be read or changed here until that read works; the '
              + 'controller settings above are unaffected.'
            : 'The PIN cannot be read: that needs a working controller, and this device '
              + 'did not report one.'),
        h('p.field-hint',
          'Every image ships ' + SHIPPED_PIN + ' as the PIN, so unless someone has '
          + 'changed it on this device, ' + SHIPPED_PIN + ' is what it is -- but that is '
          + 'an inference from the image, not a reading from this device.'));
    } else {
      let pin = agent.pin;
      const isDefault = pin === SHIPPED_PIN;
      pinCard.append(h('p.field-hint', { style: 'margin:-4px 0 14px' },
        'Demanded of a remote device when it pairs -- but only while Secure Simple '
        + 'Pairing is off. With SSP on there is no PIN prompt at all, because this device '
        + 'has no way to show or confirm one, and pairing is "just works". '
        + (sspKnown
          ? (sspOn
            ? 'SSP is currently ON for this controller, so nothing is asking for this '
              + 'PIN.'
            : 'SSP is currently OFF for this controller -- which is how the image ships '
              + '-- so this PIN is what a remote device has to produce.')
          : 'The controller could not be read, so which of the two applies here is '
            + 'unknown.')));
      if (isDefault) {
        pinCard.append(h('div.banner.banner-danger', { style: 'margin-bottom:14px' },
          h('p.banner-title', 'This is the published default'),
          h('p', 'The PIN is still ' + SHIPPED_PIN + ', the same value every P4wnP1 built '
               + 'from this image ships with. It is printed in the project\'s own '
               + 'documentation and in /root/INITIAL_CREDENTIALS.txt on this device. '
               + 'Anyone who has read either can pair. Change it here, or switch the '
               + 'radio off above, or disable the stack entirely with '
               + '"systemctl disable --now bluetooth".')));
      } else if (pin === '') {
        pinCard.append(h('div.banner.banner-danger', { style: 'margin-bottom:14px' },
          h('p.banner-title', 'No PIN is set'),
          h('p', 'The agent has an empty PIN. What a remote device is asked for in that '
               + 'state has not been verified here, so do not assume it is a refusal.')));
      }
      pinCard.append(textField('PIN', pin, v => { pin = v; }, '4 to 16 digits'));
      pinCard.append(h('div.btn-row', { style: 'margin-top:12px' },
        h('button.btn.btn-primary', {
          type: 'button',
          onclick: async (e) => {
            const v = String(pin || '').trim();
            /* Checked here so the message names the rule. The agent takes a
               bare string, so a bad value is accepted and then fails at
               pairing time, which is the worst place to find out. */
            if (!/^[0-9]{4,16}$/.test(v)) {
              toast('A pairing PIN must be 4 to 16 digits.', true);
              return;
            }
            if (v === SHIPPED_PIN) {
              toast('That is the shipped default. Pick something else.', true);
              return;
            }
            e.target.disabled = true;
            const res = await guard(
              () => Api.rpc('DeployBluetoothAgentSettings', { pin: v }),
              'set the pairing PIN');
            e.target.disabled = false;
            if (res !== undefined) { toast('Pairing PIN changed.'); Views.bluetooth(); }
          },
        }, 'Change PIN'),
        h('button.btn.btn-quiet', {
          type: 'button',
          onclick: () => showDetail({
            title: 'What the PIN does and does not protect',
            body: 'A PIN is only asked for when Secure Simple Pairing is OFF -- and SSP is '
                + 'OFF on a device as shipped. service/defaults.go starts the controller '
                + 'with PIN-based authentication, and with High Speed off as a consequence, '
                + 'because High Speed is the thing that needs SSP on. So on a shipped '
                + 'device the PIN IS demanded of anything that pairs, and it is '
                + SHIPPED_PIN + ' until someone changes it.\n\n'
                + 'This device right now: '
                + (sspKnown
                  ? (sspOn
                    ? 'SSP is ON, so this device is in the "just works" branch below and '
                      + 'the PIN is not being asked for.'
                    : 'SSP is OFF, so the PIN below is what a remote device has to produce. '
                      + 'It is the control that is doing the work here.')
                  : 'the controller could not be read, so which branch this device is in is '
                    + 'unknown. Do not assume either one.')
                + '\n\n'
                + 'The other branch: turn SSP on and this device pairs in "just works" mode '
                + '-- no PIN, no prompt, no confirmation, so anything in range that is '
                + 'allowed to pair can. That is not a bug in this console; it is the shape '
                + 'of the problem. SSP cannot request a predefined PIN, and a headless '
                + 'device cannot display a passkey or confirm one. The project chose a '
                + 'static PIN as the less bad of two bad options and wrote the reasoning '
                + 'down in service/bluetooth.go.\n\n'
                + 'Either way, keep Discoverable and Bondable off except while you are '
                + 'actually pairing. That control works in both branches. The PIN only '
                + 'works in the SSP-off one.',
            mono: false,
          }),
        }, 'Why a PIN may not be asked for')));
    }
    main.append(pinCard);

    /* --- templates -------------------------------------------------------- */

    const tplCard = h('div.card', h('h2.card-title', 'Templates'));
    tplCard.append(h('p.field-hint', { style: 'margin:-4px 0 14px' },
      'A Bluetooth template holds the controller settings and the pairing PIN under one '
      + 'name. Loadouts deploy one as part of a whole-device configuration, and the boot '
      + 'loadout can name one too.'));
    if (names.length) {
      tplCard.append(dataTable(
        h('thead', h('tr', h('th', 'Name'), h('th', 'Actions'))),
        h('tbody', ...names.map(n => h('tr',
          h('td.mono', n),
          h('td',
            h('button.btn.btn-sm.btn-primary', {
              type: 'button',
              onclick: async () => {
                const yes = await confirmAction({
                  title: 'Deploy "' + n + '"?',
                  body: 'This replaces the controller name, every changeable flag, the '
                      + 'three PAN services and the pairing PIN with whatever the '
                      + 'template holds. Press Inspect first if you are not sure what '
                      + 'that is.',
                  consequence: 'Any device currently connected over Bluetooth drops if the '
                      + 'template powers the radio down or turns off the service it is '
                      + 'using. If you are reading this over Bluetooth PAN, that includes '
                      + 'this console.',
                  confirmLabel: 'Deploy template',
                });
                if (!yes) return;
                const r = await guard(
                  () => Api.rpc('DeployStoredBluetoothSettings', { msg: n }),
                  'deploy the Bluetooth template');
                if (r !== undefined) { toast('Deployed "' + n + '".'); Views.bluetooth(); }
              },
            }, 'Deploy'),
            ' ',
            h('button.btn.btn-sm.btn-quiet', {
              type: 'button',
              onclick: async () => {
                const t = await guard(
                  () => Api.rpc('GetStoredBluetoothSettings', { msg: n }),
                  'read the template "' + n + '"');
                if (t === undefined) return;
                await showDetail({
                  title: 'Template "' + n + '"',
                  body: describeTemplate(t),
                  mono: true,
                });
              },
            }, 'Inspect'),
            ' ',
            h('button.btn.btn-sm.btn-danger', {
              type: 'button',
              onclick: async () => {
                const yes = await confirmAction({
                  title: 'Delete "' + n + '"?',
                  body: 'The template is removed from the device database.',
                  consequence: 'This cannot be undone from here. A loadout that names this '
                      + 'template keeps the name and will fail at its Bluetooth step when '
                      + 'it is next deployed -- check Loadouts if you are not sure. '
                      + 'Nothing currently running changes.',
                  confirmLabel: 'Delete',
                  danger: true,
                });
                if (!yes) return;
                const r = await guard(
                  () => Api.rpc('DeleteStoredBluetoothSettings', { msg: n }),
                  'delete the template');
                if (r !== undefined) { toast('Deleted "' + n + '".'); Views.bluetooth(); }
              },
            }, 'Delete')))))));
    } else {
      tplCard.append(h('div.empty',
        'No Bluetooth templates stored. Get the controller the way you want it, press '
        + 'Deploy, then "Store as template" below -- that snapshots what is actually '
        + 'running, PIN included, and makes it available to Loadouts and to the boot '
        + 'configuration.'));
    }
    tplCard.append(h('div.btn-row', { style: 'margin-top:20px' },
      storeDeployedButton({
        rpc: 'StoreDeployedBluetoothSettings',
        title: 'Store the running Bluetooth configuration',
        placeholder: 'bt-nap',
        hint: 'Snapshots what is live on the device right now -- the controller settings '
            + 'and the pairing PIN together. Not what is typed into the form above. An '
            + 'existing template of the same name is replaced without further warning.',
        onDone: () => Views.bluetooth(),
      })));
    main.append(tplCard);
  };

  /* ---------------------------------------------- describing what will change */

  /* Plain sentences for the confirm dialog. Named rather than diffed as a blob
     because "deploy these settings?" with no list is a dialog people click
     through, and the whole point of confirming is that they read it. */
  function describeChanges(ci, cur, draft) {
    const out = [];
    const was = ci.name || '';
    if (draft.name !== was) {
      out.push('Name: ' + (was ? '"' + was + '"' : 'unset')
        + ' becomes ' + (draft.name ? '"' + draft.name + '"' : 'unset') + '.');
    }
    for (const f of EDITABLE) {
      const before = !!(cur && cur[f.key]);
      const after = !!draft.settings[f.key];
      if (before !== after) out.push(f.label + ': ' + onOff(before) + ' becomes ' + onOff(after) + '.');
    }
    for (const s of SERVICES) {
      const before = !!ci[s.key];
      const after = !!draft[s.draft];
      if (before !== after) out.push(s.label + ': ' + onOff(before) + ' becomes ' + onOff(after) + '.');
    }
    return out;
  }

  /* The cost, not the mechanism. confirmAction keeps this separate from the
     body precisely so it gets said. */
  function deployConsequence(ci, cur, draft) {
    const parts = [];
    const overBt = onBluetoothBridge();
    const poweringOff = !!(cur && cur.powered) && !draft.settings.powered;
    const servicesBefore = SERVICES.filter(s => ci[s.key]).length;
    const servicesAfter = SERVICES.filter(s => draft[s.draft]).length;
    const losingService = SERVICES.some(s => ci[s.key] && !draft[s.draft]);

    if (poweringOff) {
      parts.push('Powering the radio off drops every Bluetooth connection immediately and '
        + 'stops the controller answering anything.');
    }
    if (losingService && !poweringOff) {
      parts.push('A PAN service is being turned off, so any device using it loses its '
        + 'network connection through this one.');
    }
    if (servicesBefore > 0 && servicesAfter === 0) {
      parts.push('With the last service off, the ' + BRIDGE_NAME + ' bridge is torn down.');
    }
    if (overBt && (poweringOff || losingService)) {
      parts.push('You are reading this over a ' + BRIDGE_NET + 'x address, which is the '
        + 'Bluetooth bridge -- so this console is one of the things that disconnects. '
        + 'Reach the device over USB or WiFi to undo it.');
    }
    if (!draft.settings.discoverable && cur && cur.discoverable) {
      parts.push('Dropping Discoverable does not disconnect anything already bonded; it '
        + 'only stops new devices finding this one in a scan.');
    }
    if (draft.settings.discoverable && cur && !cur.discoverable) {
      parts.push('Discoverable means anything in range can see this device by name, and '
        + 'it stays that way until you turn it off.');
    }
    if (!parts.length) {
      parts.push('No connection is dropped by this change. The controller is reconfigured '
        + 'in place.');
    }
    return parts.join(' ');
  }

  /* What a stored template actually holds, for Inspect. Deploy is the most
     destructive button on this page, and a list of names with no way to see
     inside them makes it a blind one. */
  function describeTemplate(t) {
    const ci = t && t.ci;
    const as = t && t.as;
    const lines = [];
    if (!ci) {
      lines.push('No controller section stored.');
      lines.push('');
      lines.push('Deploying this would send an empty controller: the name would be');
      lines.push('cleared and every flag turned off. Delete it rather than deploy it.');
    } else {
      const cur = ci.current_settings || {};
      lines.push('Name:            ' + (ci.name || '(unset -- deploying clears the name)'));
      lines.push('');
      for (const f of EDITABLE) lines.push(pad(f.label) + onOff(cur[f.key]));
      lines.push('');
      for (const s of SERVICES) lines.push(pad(s.label) + onOff(ci[s.key]));
    }
    lines.push('');
    if (as && typeof as.pin === 'string' && as.pin !== '') {
      lines.push('Pairing PIN:     stored'
        + (as.pin === SHIPPED_PIN ? ' -- and it is ' + SHIPPED_PIN + ', the published default' : ''));
    } else {
      lines.push('Pairing PIN:     NOT STORED');
      lines.push('');
      lines.push('Deploying a template with no PIN applies the controller settings and');
      lines.push('then fails on the PIN step, leaving the controller changed and the PIN');
      lines.push('untouched. Store a replacement from a running device instead.');
    }
    lines.push('');
    lines.push('Deploying replaces all of the above. Flags this controller does not');
    lines.push('support are refused by the controller, not by this page.');
    return lines.join('\n');
  }

  const pad = s => (s + ':').padEnd(24, ' ');

})();
