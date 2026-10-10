/* view-templates.js -- the template library: everything this device has saved.
 *
 * WHY THIS FILE EXISTS
 *
 * The device stores six kinds of configuration under a name -- USB, WiFi,
 * Bluetooth, network, reflex sets and loadouts -- and this console could manage
 * two of them. Fourteen RPCs that are implemented in the service had no caller
 * in ANY client (not here, not cli_client, not the OLED panel):
 *
 *   USB        StoreUSBSettings  GetStoredUSBSettings
 *              DeployStoredUSBSettings  DeleteStoredUSBSettings
 *   WiFi       StoreWifiSettings  GetStoredWifiSettings
 *              DeployStoredWifiSettings  DeleteStoredWifiSettings
 *   Reflexes   StoreTriggerActionSet  DeployStoredTriggerActionSetAdd
 *              DeployStoredTriggerActionSetReplace  DeleteStoredTriggerActionSet
 *              DeployTriggerActionSetReplace
 *   Loadouts   DeployMasterTemplate  DeleteStoredMasterTemplate
 *
 * The practical effect: an operator could store a USB composition from the
 * Cable page and a WiFi configuration from the WiFi page, and then could
 * never look at one, deploy one, copy one or delete one again. The reflex
 * editor could arm rules on the running device and had no way to save the set.
 * Nothing in the product could remove a stored template of any kind, so the
 * store only ever grew. The Loadouts page could NAME these templates in a
 * loadout while offering no way to see what any of the names contained.
 *
 * This page does not duplicate what already works: Bluetooth templates are
 * managed on the Bluetooth page and network templates on the Network page,
 * each next to the settings they hold, and composing/storing a loadout and
 * setting the boot default stay on the Loadouts page. This page is the four
 * kinds that had nowhere else, plus inspecting, deploying and deleting
 * loadouts that exist.
 *
 * FOUR HAZARDS THIS PAGE IS BUILT AROUND
 *
 * 1. Storing overwrites in silence. Every Store* RPC calls
 *    datastore.Store.Put(key, value, allowOverwrite=true), so saving under a
 *    name that exists replaces it with no error, no prompt and no copy kept.
 *    So: every save checks the name against the list first, and says what
 *    replacing it costs.
 *
 * 2. A name tells the operator nothing. Deploying blind is how a device ends
 *    up unreachable, so every Deploy here READS the stored object first with
 *    the matching GetStored* and names the real consequence -- which USB
 *    functions, which SSID, which mode. The one exception is reflex sets:
 *    the service exposes no GetStoredTriggerActionSet, so a stored set
 *    genuinely cannot be read without deploying it. The page says that
 *    instead of showing a plausible summary.
 *
 * 3. Add and Replace are not variations of each other.
 *    DeployStoredTriggerActionSetAdd appends to what is running;
 *    DeployStoredTriggerActionSetReplace calls ClearTriggerActions first and
 *    throws every running reflex away except the locked ones. The two method
 *    names differ by one word, so the UI never relies on the name alone.
 *
 * 4. There is no referential integrity. DeleteStoredUSBSettings and friends
 *    delete by name with no check, and a loadout is only five names. Delete a
 *    template a loadout references and the loadout still names it; at boot
 *    that subsystem fails to deploy and says so only in the journal. So every
 *    Delete here reads the loadouts first and names the ones that would break.
 *
 * Everything is wrapped in an IIFE and only Views.templates is exported. A
 * top-level `const` in a classic script lands in the shared global lexical
 * environment, so a name this file and app.js (or a sibling view) both
 * declared would be a SyntaxError that stops the whole console from loading.
 */
'use strict';

(function () {

  /* --- enums ---------------------------------------------------------------
   *
   * The JSON bridge marshals with EmitUnpopulated and WITHOUT UseEnumNumbers
   * (service/jsonbridge/bridge.go), so a real device sends the enum NAME --
   * "AP", "WPA2_PSK" -- and sends it even when it is the zero value. The
   * jsdom render harness and tools/mock-service send numbers, or nothing at
   * all. enumName() takes any of those rather than trusting one, because
   * reading a mode wrong here means describing a configuration the operator
   * is about to deploy as something other than what it is.
   *
   * Order IS the proto enum order in proto/grpc.proto. */
  const WIFI_WORKING = ['UNKNOWN', 'AP', 'STA', 'STA_FAILOVER_AP'];
  const WIFI_AUTH = ['WPA2_PSK', 'OPEN'];
  const WIFI_STATE = ['STA_NOT_CONNECTED', 'AP_UP', 'STA_CONNECTED'];

  const WORKING_LABEL = {
    UNKNOWN: 'not recorded',
    AP: 'access point -- spawns its own network',
    STA: 'station -- joins a network someone else runs',
    STA_FAILOVER_AP: 'station, falling back to its own access point',
  };
  const STATE_LABEL = {
    STA_NOT_CONNECTED: 'station, not connected',
    AP_UP: 'access point up',
    STA_CONNECTED: 'station, connected',
  };

  function enumName(value, names) {
    if (value === null || value === undefined || value === '') return null;
    if (typeof value === 'number') return names[value] || null;
    const s = String(value).toUpperCase();
    if (names.indexOf(s) !== -1) return s;
    if (/^\d+$/.test(s)) return names[Number(s)] || null;
    return null;
  }

  /* --- small helpers ------------------------------------------------------ */

  const str = v => (v === null || v === undefined ? '' : String(v));
  const names = r => (r && Array.isArray(r.msgArray)
    ? r.msgArray.filter(n => typeof n === 'string' && n.length)
    : (r ? [] : null));          // null means "could not be read"
  const count = l => (l === null ? '?' : String(l.length));
  const isEmptyReply = o => !o || typeof o !== 'object' || !Object.keys(o).length;

  const pad = (s, n) => {
    s = str(s);
    return s.length >= n ? s : s + ' '.repeat(n - s.length);
  };
  const row = (k, v) => pad(k, 20) + str(v);

  /* --- what the WiFi subsystem will actually accept ------------------------
   *
   * Four limits a stored configuration can violate, all of which this page
   * exists to name BEFORE a deploy rather than leave the operator to discover
   * after. Without them the "Before you deploy this" list is quiet about the
   * ways a stored WiFi configuration fails to come up at all, and about the
   * one way it comes up on a key the operator does not have.
   *
   * 1. PSKs this device REFUSES to broadcast. Read off compromisedPSKs in
   *    service/wifi_psk_guard.go. The comparison there is strings.EqualFold
   *    on a trimmed value -- case-insensitive, surrounding whitespace ignored
   *    -- and this matches it exactly, because warning about a key the guard
   *    will not catch, or failing to warn about one it will, is worse than
   *    not warning at all.
   *
   *    The guard does not refuse the deploy: it rewrites the key in place to
   *    this device's own generated one and brings the access point up on
   *    THAT. So the deploy reports success, the SSID is the one on screen,
   *    and the key is not. The replacement IS readable: DeploySettings sets
   *    State.CurrentSettings to the message the guard mutated, so the reply
   *    to the deploy carries it, and deployWifi below puts it on screen. It
   *    is in that reply and nowhere else a browser can reach -- a deploy that
   *    takes the link with it before the reply arrives loses it, and then the
   *    WiFi page's "Deployed now" card is where to read it from.
   *
   *    This is not hypothetical here. dist/db/init.db ships the first of
   *    these inside a stored WiFi template, so on a freshly flashed device
   *    the only saved configuration in the list below is exactly the one that
   *    needs the warning.
   *
   * 2. An SSID is capped at 32 BYTES, and wifiCreateHostapdConfString
   *    (service/wifi.go) refuses a longer one outright with the byte count --
   *    so this fails the deploy rather than degrading.
   *
   * 3. A WPA2 passphrase is 8..63 bytes. wifiCreateHostapdConfString writes
   *    the value as hostapd's `wpa_passphrase` and nothing else, and hostapd
   *    refuses that directive outside 8..63 and exits -- it does NOT fall
   *    back to reading a 64-character value as a raw hex key. That form is
   *    `wpa_psk`, a separate directive which appears nowhere in this
   *    repository. Nothing on the AP path checks either bound, so hostapd is
   *    what says no, in the journal, after the radio has already been taken
   *    down.
   *
   * 4. The channel is not checked anywhere either. hw_mode=g is written
   *    unconditionally and `channel=%d` with no validation, so hostapd is
   *    again the only thing that says no -- with the radio already stopped.
   *    The WiFi page refuses anything outside 1..14 in its own form, and so
   *    does the CLI (createWifiAPSettings, cli_client/cmd_wifi.go) -- but
   *    StoreWifiSettings itself checks nothing, so a template written over
   *    the JSON/gRPC API directly, restored from an older database, or stored
   *    before either check existed can carry any uint32. The CLI's station
   *    constructor sets no channel at all, which is the 0/ACS case below. */
  const PUBLISHED_PSKS = [
    'MaMe82-P4wnP1',      // upstream default, in dist/db and years of docs
    'HackProKP-changeme', // this fork's placeholder in service/defaults.go
    'p4wnp1',
    'toor',
    'changeme',
  ];
  const GENERATED_PSK_PATH = '/etc/p4wnp1/generated-ap.psk';
  const SSID_MAX_BYTES = 32;
  const PSK_MIN_BYTES = 8;
  const PSK_MAX_BYTES = 63;
  /* 2.4GHz only, because hw_mode=g is written unconditionally. Same bounds as
     the WiFi page's own form and the CLI's. */
  const CHANNEL_MIN = 1;
  const CHANNEL_MAX = 14;

  /* BYTES, because every limit above is a byte limit -- Go's len() on a
     string, which is what the service checks with. A JavaScript string's
     .length is UTF-16 code units, which is neither characters nor bytes: one
     emoji is four bytes and counts as two there, so an SSID hostapd refuses
     would read as eight under the cap. */
  function byteLength(s) {
    const v = str(s);
    if (!v) return 0;
    try { return new TextEncoder().encode(v).length; } catch (_) { /* fall through */ }
    let n = 0;
    for (const ch of v) {
      const c = ch.codePointAt(0);
      n += c < 0x80 ? 1 : c < 0x800 ? 2 : c < 0x10000 ? 3 : 4;
    }
    return n;
  }

  /* A channel for display. uint32's zero value is not a channel: hostapd
     reads channel=0 as "choose one yourself", so printing "channel 0" next to
     a warning that says no channel is recorded is the page contradicting
     itself in one dialog. */
  function channelShown(w) {
    const v = str(w && w.channel).trim();
    return (!v || v === '0') ? '' : v;
  }

  function isPublishedPSK(psk) {
    const p = str(psk).trim().toLowerCase();
    if (!p) return false;
    return PUBLISHED_PSKS.some(bad => bad.toLowerCase() === p);
  }

  /* What the channel in a stored configuration does to an access point.
   *
   * Nothing in the service looks at it: wifiCreateHostapdConfString writes
   * `hw_mode=g` unconditionally and `channel=%d` straight through
   * (service/wifi.go), and runAPMode then starts hostapd against that file.
   * So hostapd is the only thing that refuses a channel, and by then
   * StopHostapd and StopWpaSupplicant have already run -- there is no access
   * point afterwards and nothing puts the old one back.
   *
   * Returns null when there is nothing to say. Otherwise `text` is the
   * sentence for the "Before you deploy this" list, `why` is the clause
   * deployWifi splices into its consequence, and `wontStart` is true ONLY
   * where the outcome is knowable. A channel of 0 is the case where it is
   * not: hostapd reads 0 as "choose one yourself", which needs ACS in the
   * build and in the driver, and nothing a browser can reach says whether
   * either is there. Guessing in that case would be inventing a failure.
   *
   * `regulatory` empty is not "no domain": wifiSetReg substitutes US and logs
   * that it did, so US is the domain in force and its channel list is the one
   * hostapd is held to. */
  function channelProblem(w) {
    const raw = str(w.channel).trim();
    const ch = Number(raw);
    const domain = str(w.regulatory).trim().toUpperCase();
    const inForce = domain || 'US';
    const named = domain ? 'the domain in force, ' + domain + ','
      : 'US, which the service substitutes for the empty domain recorded here,';

    if (raw !== '' && !Number.isInteger(ch)) {
      return {
        text: 'The channel recorded is "' + raw + '", which is not a whole channel number. '
          + 'What hostapd is handed, and whether it starts on it, cannot be worked out from '
          + 'here.',
        why: 'the channel recorded is not a whole number',
        wontStart: false,
      };
    }
    if (raw === '' || ch === 0) {
      return {
        text: 'No channel is recorded, so hostapd is handed channel=0, which it reads as a '
          + 'request to pick one itself. That needs ACS support in both the hostapd build '
          + 'and the driver, and nothing in this console can tell you whether this device '
          + 'has it: the access point either comes up on a channel nobody chose or hostapd '
          + 'exits with the radio already stopped. Set a channel on the WiFi page and store '
          + 'the template again.',
        why: 'no channel is recorded, so hostapd is asked to choose one and may refuse',
        wontStart: false,
      };
    }
    if (ch === CHANNEL_MAX) {
      return {
        text: 'Channel 14 exists only in Japan and only for 802.11b, and this device always '
          + 'configures hostapd hw_mode=g, so hostapd will not bring an access point up on '
          + 'it. The radio is stopped before hostapd is started, so there is no access point '
          + 'afterwards and nothing puts the old one back.',
        why: 'hostapd will refuse channel 14 (802.11b-only, and this device always configures '
          + 'hw_mode=g) and the radio is stopped before hostapd is started',
        wontStart: true,
      };
    }
    if (ch < CHANNEL_MIN || ch > CHANNEL_MAX) {
      return {
        text: 'Channel ' + ch + ' is not a 2.4GHz channel, and this device always configures '
          + 'hostapd hw_mode=g, so hostapd will refuse it. The radio is stopped before '
          + 'hostapd is started, so there is no access point afterwards and nothing puts the '
          + 'old one back.',
        why: 'channel ' + ch + ' is not a 2.4GHz channel and this device always configures '
          + 'hw_mode=g, so hostapd will refuse it and the radio is stopped before hostapd '
          + 'is started',
        wontStart: true,
      };
    }
    if (ch >= 12 && inForce === 'US') {
      return {
        text: 'Channel ' + ch + ' is not permitted under ' + named + ' so hostapd will not '
          + 'bring an access point up on it. The radio is stopped before hostapd is started, '
          + 'so there is no access point afterwards and nothing puts the old one back. Set a '
          + 'domain that permits it, or pick a channel of 11 or lower.',
        why: 'channel ' + ch + ' is not permitted under ' + named + ' so hostapd will refuse '
          + 'to come up on it and the radio is stopped before hostapd is started',
        wontStart: true,
      };
    }
    if (ch >= 12) {
      return {
        text: 'Channel ' + ch + ' is permitted in some regulatory domains and not others. '
          + 'Whether ' + domain + ' is one of them is decided by the kernel\'s regulatory '
          + 'database on the device, not by this console. Where it is not, hostapd will not '
          + 'start, and the radio has already been stopped by then.',
        why: 'channel ' + ch + ' is not permitted in every regulatory domain and this console '
          + 'cannot tell whether ' + domain + ' allows it; where it does not, hostapd will '
          + 'not start',
        wontStart: false,
      };
    }
    return null;
  }

  /* Which interface is this console reached over?
   *
   * Only an EXACT match on an interface's own address is used. The browser is
   * talking to that address, so that interface is the link, and nothing has to
   * be inferred. Anything else -- a hostname, a proxy, a route this console
   * cannot see -- is reported as unknown, because a wrong reassurance here is
   * how a device becomes unreachable. The service manages usbeth, wlan0 and
   * bteth and nothing else, which is why those three names are meaningful. */
  function linkInterface(ifaces) {
    const host = str(location.hostname);
    if (!host) return null;
    for (const i of (ifaces || [])) {
      if (i && i.ipAddress4 && str(i.ipAddress4) === host) return str(i.name);
    }
    return null;
  }

  /* --- the four kinds, as literal RPC call sites --------------------------
   *
   * Every method name is written out in full at its call site rather than
   * passed around as a string, so tools/check-rpc-shapes.py can check the
   * payload keys of all of them against proto/grpc.proto. The bridge
   * unmarshals with DiscardUnknown, so a misspelled key is DISCARDED rather
   * than rejected: the deploy reports success and configures nothing, or
   * configures something with a zeroed field. That checker is the only thing
   * standing between a typo here and a device that lies about it. */
  const USB = {
    label: 'USB composition',
    field: 'template_name_usb',
    read: n => Api.rpc('GetStoredUSBSettings', { msg: n }),
    deploy: n => Api.rpc('DeployStoredUSBSettings', { msg: n }),
    remove: n => Api.rpc('DeleteStoredUSBSettings', { msg: n }),
    /* `settings` is the object the device just handed back, unmodified. A
       hand-built GadgetSettings would be twenty chances to misspell a key. */
    copy: (n, settings) => Api.rpc('StoreUSBSettings', { TemplateName: n, settings: settings }),
  };
  const WIFI = {
    label: 'WiFi configuration',
    field: 'template_name_wifi',
    read: n => Api.rpc('GetStoredWifiSettings', { msg: n }),
    deploy: n => Api.rpc('DeployStoredWifiSettings', { msg: n }),
    remove: n => Api.rpc('DeleteStoredWifiSettings', { msg: n }),
    copy: (n, settings) => Api.rpc('StoreWifiSettings', { TemplateName: n, settings: settings }),
  };
  const TAS = {
    label: 'reflex set',
    field: 'template_name_trigger_actions',
    add: n => Api.rpc('DeployStoredTriggerActionSetAdd', { msg: n }),
    replace: n => Api.rpc('DeployStoredTriggerActionSetReplace', { msg: n }),
    remove: n => Api.rpc('DeleteStoredTriggerActionSet', { msg: n }),
    /* TriggerActionSet.Name IS the storage key -- StoreTriggerActionSet does
       Put(prefix + set.Name), so the name travels inside the message rather
       than beside it as every other Store* RPC does. Both keys are
       capitalised in the proto (`TriggerActions`, `Name`); the field names
       here are not uniformly snake_case and guessing wrong would store an
       empty set under an empty name. */
    store: (n, actions) => Api.rpc('StoreTriggerActionSet', { TriggerActions: actions, Name: n }),
    clear: () => Api.rpc('DeployTriggerActionSetReplace', { TriggerActions: [] }),
  };
  const MASTER = {
    label: 'loadout',
    read: n => Api.rpc('GetStoredMasterTemplate', { msg: n }),
    deploy: n => Api.rpc('DeployStoredMasterTemplate', { msg: n }),
    remove: n => Api.rpc('DeleteStoredMasterTemplate', { msg: n }),
  };

  /* ---------------------------------------------------------------- the view */

  Views.templates = async function () {
    const main = clear($('#view'));
    main.append(pageHead('Templates',
      'Every configuration saved on this device, and what each saved name actually '
      + 'contains. Read one before you deploy it: a name on its own says nothing about '
      + 'what it will do to the link you are reading this over.'));

    /* One guarded call, so an expired token bounces to sign-in -- guard()
       handles the 401. Everything else is unguarded on purpose: an empty
       template store and a device with no WiFi or no Bluetooth are both
       normal, and a toast per empty list on every visit is noise rather than
       information. A read that failed stays distinguishable from one that
       succeeded and was empty, because `null` and `[]` mean different things
       everywhere below. */
    const [
      usbL, wifiL, tasL, masterL, btL, netL,
      startup, liveUsb, liveWifi, liveTas, ifaces,
    ] = await Promise.all([
      Api.rpc('ListStoredUSBSettings').catch(() => null),
      Api.rpc('ListStoredWifiSettings').catch(() => null),
      Api.rpc('ListStoredTriggerActionSets').catch(() => null),
      guard(() => Api.rpc('ListStoredMasterTemplate'), 'list the stored loadouts'),
      Api.rpc('ListStoredBluetoothSettings').catch(() => null),
      Api.rpc('ListStoredEthernetInterfaceSettings').catch(() => null),
      Api.rpc('GetStartupMasterTemplate').catch(() => null),
      Api.rpc('GetDeployedGadgetSetting').catch(() => null),
      Api.rpc('GetWiFiState').catch(() => null),
      Api.rpc('GetDeployedTriggerActionSet').catch(() => null),
      Api.rpc('GetAllDeployedEthernetInterfaceSettings').catch(() => null),
    ]);

    const usbNames = names(usbL);
    const wifiNames = names(wifiL);
    const tasNames = names(tasL);
    const masterNames = names(masterL);
    const btNames = names(btL);
    const netNames = names(netL);
    const startupName = (startup && typeof startup.msg === 'string' && startup.msg) || null;
    const ifaceList = (ifaces && Array.isArray(ifaces.list) ? ifaces.list : [])
      .filter(i => i && typeof i === 'object');
    const linkName = linkInterface(ifaceList);

    /* --- read every loadout, once -----------------------------------------
     *
     * This costs one RPC per stored loadout on page load, and it buys the
     * only referential-integrity check that exists anywhere in the product:
     * four Delete buttons and one Deploy button all need to know which
     * loadouts name which template. Reading them lazily per click would make
     * each delete slower AND let two buttons on the same page disagree.
     *
     * A read that failed is recorded as a failure, not as "not referenced".
     * The difference matters: "no loadout names this" is a reason to delete
     * and "this could not be checked" is not. */
    const loadouts = [];
    let loadoutsUnread = 0;
    if (masterNames && masterNames.length) {
      const got = await Promise.all(
        masterNames.map(n => MASTER.read(n).then(t => ({ name: n, t: t }), () => ({ name: n, t: null }))));
      for (const g of got) {
        if (g.t && !isEmptyReply(g.t)) loadouts.push({ name: g.name, t: g.t });
        else loadoutsUnread++;
      }
    }

    /* Which loadouts name `value` in `field`. `template_names_network` is
       repeated, so it is checked as a list; the other four are single. */
    function referencedBy(value, field) {
      if (!value) return [];
      return loadouts.filter(l => {
        if (field === 'template_names_network') {
          const a = l.t.template_names_network;
          return Array.isArray(a) && a.indexOf(value) !== -1;
        }
        return str(l.t[field]) === value;
      }).map(l => l.name);
    }

    /* The sentence a Delete confirmation needs about loadouts. Said in full
       because the failure it describes is silent: the loadout keeps the dead
       name, and the subsystem it names fails only when the loadout is next
       deployed -- which is usually at boot, with nobody watching. */
    function referenceWarning(value, field) {
      const refs = referencedBy(value, field);
      if (refs.length) {
        const many = refs.length > 1;
        return 'The loadout' + (many ? 's ' : ' ')
          + refs.map(n => '"' + n + '"').join(', ') + (many ? ' name' : ' names')
          + ' this one, and deleting it does not change ' + (many ? 'them' : 'that loadout')
          + ': ' + (many ? 'they keep' : 'it keeps') + ' the name. The next time '
          + (many ? 'one of them is' : 'it is') + ' deployed -- which for the boot default '
          + 'is every boot -- that subsystem fails to come up and says so only in the '
          + 'journal. Nothing else is rolled back, because a loadout deploys best-effort, '
          + 'subsystem by subsystem.';
      }
      if (loadoutsUnread) {
        return 'Whether a loadout names this one could NOT be checked: '
          + loadoutsUnread + ' loadout' + (loadoutsUnread > 1 ? 's' : '')
          + ' could not be read. If one does name it, that loadout will fail to deploy '
          + 'this subsystem from now on and will say so only in the journal.';
      }
      if (!masterNames) {
        return 'Whether a loadout names this one could not be checked -- the list of '
          + 'loadouts could not be read.';
      }
      if (!loadouts.length) return 'No loadouts are stored, so nothing references it.';
      return 'No loadout names this one, so nothing else breaks.';
    }

    /* --- storing under a name that is taken ------------------------------- */

    async function nameIsFree(kindLabel, name, existing) {
      if (existing === null) {
        return confirmAction({
          title: 'Store as "' + name + '"?',
          body: 'The list of stored ' + kindLabel + 's could not be read, so it is not '
            + 'known whether that name is already in use.',
          consequence: 'If it is, the device replaces the existing one without asking and '
            + 'without keeping a copy. There is no undo.',
          confirmLabel: 'Store it anyway',
          danger: true,
        });
      }
      if (existing.indexOf(name) === -1) return true;
      return confirmAction({
        title: 'Replace "' + name + '"?',
        body: 'A ' + kindLabel + ' is already stored under that name.',
        consequence: 'It is overwritten. The device does this without asking and without '
          + 'keeping a copy -- the store writes over the key and reports success -- so '
          + 'whatever is under "' + name + '" now is gone. Any loadout or reflex that '
          + 'names it gets the new contents from the next deploy onwards.',
        confirmLabel: 'Replace it',
        danger: true,
      });
    }

    const nameRules = {
      label: 'Name',
      hint: 'Letters, digits, dot, dash and underscore.',
      validate: v => {
        if (!v) return 'Give it a name.';
        if (!/^[A-Za-z0-9._-]+$/.test(v)) return 'Letters, digits, dot, dash and underscore only.';
        return null;
      },
    };

    /* --- the link this page is riding on ---------------------------------- */

    if (linkName) {
      main.append(h('div.banner.banner-danger',
        h('p.banner-title', 'You are connected over ' + linkName),
        h('p', 'This console is talking to ' + linkName + '’s own address ('
          + location.hostname + '). '
          + (linkName === 'usbeth'
            ? 'That interface only exists while the USB gadget presents RNDIS or CDC ECM, '
              + 'so deploying a USB composition without one of those -- or a loadout that '
              + 'names it -- ends this session.'
            : linkName === 'wlan0'
              ? 'Deploying a WiFi configuration reconfigures the radio carrying this page. '
                + 'A different SSID or key means rejoining; station mode or a disabled radio '
                + 'means the access point stops existing.'
              : linkName === 'bteth'
                ? 'Deploying a Bluetooth or loadout template reconfigures the link carrying '
                  + 'this page.'
                : 'Deploying something that reconfigures it ends this session.')
          + ' A deploy that kills the link usually reports a timeout even though it worked.')));
    } else {
      main.append(h('div.banner',
        h('p.banner-title', 'Which interface carries this page is unknown'),
        h('p', 'This console was reached as "' + (location.hostname || 'an unknown address')
          + '", which does not match the address of any interface the service manages. '
          + 'Treat every deploy below as if it might drop the connection you are using: '
          + 'the device is reachable over USB (172.16.0.1 by default), its own access point '
          + '(172.24.0.1) and Bluetooth, and the Serial USB function gives a console over '
          + 'ttyGS0 if all three are gone.')));
    }

    /* --- what is stored, in total ----------------------------------------- */

    main.append(h('div.card',
      h('h2.card-title', 'What is stored'),
      h('div.figures',
        figure(count(usbNames), 'USB compositions'),
        figure(count(wifiNames), 'WiFi configurations'),
        figure(count(tasNames), 'reflex sets'),
        figure(count(masterNames), 'loadouts')),
      h('p.field-hint', { style: 'margin-top:16px' },
        'A "?" means that list could not be read, not that it is empty. Two more kinds are '
        + 'stored the same way but are managed next to the settings they hold: '
        + count(btNames) + ' Bluetooth configuration(s) on the Bluetooth page and '
        + count(netNames) + ' network template(s) on the Network page. Composing a new '
        + 'loadout and choosing the one that loads at boot stay on the Loadouts page.'),
      startupName
        ? h('p.field-hint', { style: 'margin-top:8px' },
          'The loadout ', h('span.mono', startupName), ' is deployed at every boot.')
        : h('p.field-hint', { style: 'margin-top:8px' },
          'No loadout is set to deploy at boot' + (startup ? '' : ', or that could not be read')
          + '. Without one the service brings up its own built-in defaults instead of '
          + 'anything stored here.')));

    /* ------------------------------------------------------------------ USB */

    const usbCard = h('div.card', h('h2.card-title', 'USB compositions'));
    usbCard.append(h('p.field-hint', { style: 'margin:0 0 14px' },
      'A USB composition is the whole gadget: which functions the host enumerates '
      + '(keyboard, mouse, network, storage, serial), the vendor and product ids it reports, '
      + 'and the storage image it backs. Deploying one tears the gadget down and builds it '
      + 'again, so the host re-enumerates the device from scratch.'));

    usbCard.append(liveSummary(liveUsb, 'USB gadget', g => {
      const on = USB_FUNCTIONS.filter(f => g[f.key]).map(f => f.name);
      return [
        kvRow('Functions', on.length ? on.join(', ') : 'none',
          on.length ? null : 'The host enumerates the device and finds nothing on it.'),
        kvRow('Gadget', g.enabled === false ? 'disabled' : g.enabled === true ? 'enabled' : 'not reported',
          g.enabled === false ? 'The composite gadget is off, so the host sees nothing at all.' : null),
        kvRow('Identity', (str(g.vid) || '--') + ' / ' + (str(g.pid) || '--')),
        kvRow('Endpoints', endpointsUsed(g) + ' of ' + USB_ENDPOINT_MAX + ' used'),
      ];
    }));

    if (usbNames === null) {
      usbCard.append(h('div.empty',
        'The list of stored USB compositions could not be read. The device may have just '
        + 'reconfigured the interface this page arrived over -- check the Journal.'));
    } else if (!usbNames.length) {
      usbCard.append(h('div.empty',
        'No USB compositions stored. Set the gadget up on the Cable page and press "Store as '
        + 'template" there to keep it; a stored composition can be deployed again in one '
        + 'step and is what a loadout names under "USB".'));
    } else {
      usbCard.append(dataTable(
        h('thead', h('tr', h('th', 'Name'), h('th', 'Named by'), h('th', 'Actions'))),
        h('tbody', ...usbNames.map(n => h('tr',
          h('td.mono', n),
          h('td', namedByCell(n, USB.field)),
          h('td', h('div.btn-row',
            h('button.btn.btn-sm', {
              type: 'button',
              onclick: () => inspectUsb(n),
            }, 'Inspect'),
            h('button.btn.btn-sm.btn-primary', {
              type: 'button',
              onclick: () => deployUsb(n),
            }, 'Deploy'),
            h('button.btn.btn-sm.btn-quiet', {
              type: 'button',
              onclick: () => copyStored(USB, n, usbNames),
            }, 'Copy'),
            h('button.btn.btn-sm.btn-danger', {
              type: 'button',
              onclick: () => deleteStored(USB, n, usbNames),
            }, 'Delete'))))))));
    }
    main.append(usbCard);

    /* ----------------------------------------------------------------- WiFi */

    const wifiCard = h('div.card', h('h2.card-title', 'WiFi configurations'));
    wifiCard.append(h('p.field-hint', { style: 'margin:0 0 14px' },
      'A WiFi configuration is the whole radio: whether the device spawns an access point '
      + 'or joins one, the SSID and key, the channel and the regulatory domain. Deploying '
      + 'one restarts hostapd or wpa_supplicant, so the network the device offers stops '
      + 'existing while it changes.'));

    wifiCard.append(liveSummary(liveWifi, 'WiFi radio', w => {
      const cs = (w.currentSettings && typeof w.currentSettings === 'object') ? w.currentSettings : {};
      const state = enumName(w.mode, WIFI_STATE);
      return [
        kvRow('State', state ? STATE_LABEL[state] : 'not reported'),
        kvRow('SSID', str(w.ssid) || 'none'),
        kvRow('Channel', str(w.channel) || 'not reported'),
        kvRow('Regulatory domain', str(cs.regulatory) || 'unset',
          /* NOT fatal. Nothing writes country_code into hostapd.conf, and
             wifiSetReg substitutes "US" for an empty value and logs it. The
             domain still constrains which channels an AP may use. */
          str(cs.regulatory) ? null : 'Unset: the service substitutes US and logs that it did. '
            + 'The domain in force still decides which channels an AP may use.'),
        kvRow('From template', str(cs.name) || 'not recorded',
          'The name recorded inside the running configuration. It is not proof that a '
          + 'template of that name still exists, or that it still contains this.'),
      ];
    }));

    if (wifiNames === null) {
      wifiCard.append(h('div.empty',
        'The list of stored WiFi configurations could not be read. On a device with no WiFi '
        + 'hardware the store still works, so this is a read failure rather than a missing '
        + 'radio -- check the Journal.'));
    } else if (!wifiNames.length) {
      wifiCard.append(h('div.empty',
        'No WiFi configurations stored. Set the radio up on the WiFi page and keep it with '
        + '"Store what is running" (what the service holds now) or "Store the form as a '
        + 'template" (what is in the editor, applied or not). The device\'s own first-boot '
        + 'notes end with "save as template -> set as default", which needs one of these '
        + 'to exist.'));
    } else {
      wifiCard.append(dataTable(
        h('thead', h('tr', h('th', 'Name'), h('th', 'Named by'), h('th', 'Actions'))),
        h('tbody', ...wifiNames.map(n => h('tr',
          h('td.mono', n),
          h('td', namedByCell(n, WIFI.field)),
          h('td', h('div.btn-row',
            h('button.btn.btn-sm', {
              type: 'button',
              onclick: () => inspectWifi(n),
            }, 'Inspect'),
            h('button.btn.btn-sm.btn-primary', {
              type: 'button',
              onclick: () => deployWifi(n),
            }, 'Deploy'),
            h('button.btn.btn-sm.btn-quiet', {
              type: 'button',
              onclick: () => copyStored(WIFI, n, wifiNames),
            }, 'Copy'),
            h('button.btn.btn-sm.btn-danger', {
              type: 'button',
              onclick: () => deleteStored(WIFI, n, wifiNames),
            }, 'Delete'))))))));
    }
    main.append(wifiCard);

    /* ------------------------------------------------------------- reflexes */

    const liveActions = (liveTas && Array.isArray(liveTas.TriggerActions))
      ? liveTas.TriggerActions.filter(t => t && typeof t === 'object') : null;
    const mutable = liveActions ? liveActions.filter(t => !t.immutable) : null;
    const locked = liveActions ? liveActions.filter(t => t.immutable) : null;

    const tasCard = h('div.card', h('h2.card-title', 'Reflex sets'));
    tasCard.append(h('p.field-hint', { style: 'margin:0 0 14px' },
      'A reflex set is a group of "when this happens, do that" rules saved under one name. '
      + 'Reflexes are the only thing on this device that acts with nobody at the keyboard, '
      + 'and a set is how you keep a working group of them.'));

    /* Two different unknowns, kept apart. `liveTas === null` is a read that
       failed; a reply with no TriggerActions list at all is a read that
       succeeded and said nothing, which is not the same thing and is not the
       same as "no reflexes are running" either. */
    const tasUnknownWhy = liveTas === null
      ? 'What is running right now could not be read'
      : 'The device answered but reported no list of reflexes at all';

    tasCard.append(liveActions === null
      ? h('p.field-hint', { style: 'margin:0 0 14px' },
        tasUnknownWhy + ', so neither "Add to running" nor "Replace all running" below can '
        + 'be described in terms of what you would be changing.')
      : h('div', { style: 'margin-bottom:16px' },
        h('div.figures',
          figure(String(liveActions.filter(t => t.isActive).length), 'armed now'),
          figure(String(mutable.length), 'replaceable'),
          figure(String(locked.length), 'locked')),
        h('p.field-hint', { style: 'margin-top:14px' },
          'Locked reflexes are the service\'s own; they survive a replace and cannot be '
          + 'stored or deleted from here.')));

    /* ADD versus REPLACE.
     *
     * The two RPCs differ by one word and do opposite things to everything
     * already running, so the difference is spelled out here, labelled on the
     * buttons, and said again in each confirmation. Relying on the operator
     * to remember which of "Add" and "Replace" is which is how you lose a set
     * of reflexes that took an hour to build. */
    tasCard.append(h('p.field-hint', { style: 'margin:0 0 14px' },
      'Add keeps every reflex that is running and appends the stored set to it. Replace '
      + 'throws away every running reflex except the locked ones FIRST, then applies the '
      + 'stored set. Neither is written to disk, so a reboot gives you the boot loadout\'s '
      + 'reflexes back either way.'));

    if (tasNames === null) {
      tasCard.append(h('div.empty', 'The list of stored reflex sets could not be read.'));
    } else if (!tasNames.length) {
      tasCard.append(h('div.empty',
        'No reflex sets stored. Build the rules you want on the Reflexes page, then use '
        + '"Store the running reflexes" below to keep them under a name.'));
    } else {
      tasCard.append(dataTable(
        h('thead', h('tr', h('th', 'Name'), h('th', 'Named by'), h('th', 'Actions'))),
        h('tbody', ...tasNames.map(n => h('tr',
          h('td.mono', n),
          h('td', namedByCell(n, TAS.field)),
          h('td', h('div.btn-row',
            h('button.btn.btn-sm.btn-primary', {
              type: 'button',
              onclick: () => addTas(n),
            }, 'Add to running'),
            h('button.btn.btn-sm.btn-danger', {
              type: 'button',
              onclick: () => replaceTas(n),
            }, 'Replace all running'),
            h('button.btn.btn-sm.btn-danger', {
              type: 'button',
              onclick: () => deleteStored(TAS, n, tasNames),
            }, 'Delete'))))))));
    }

    /* The service has no GetStoredTriggerActionSet. This is the one kind of
       template on the device that cannot be read without being deployed, and
       saying so is better than offering an Inspect button that shows a
       plausible guess. */
    tasCard.append(h('p.field-hint', { style: 'margin-top:14px' },
      'There is no Inspect here, and that is a gap in the device rather than in this page: '
      + 'the service exposes no way to read a stored reflex set back. What is inside a name '
      + 'can only be found out by adding it to what is running and looking at the Reflexes '
      + 'page afterwards. Use Add rather than Replace for a set you do not recognise -- Add '
      + 'can be undone by deleting the rules it brought in, Replace cannot.'));

    tasCard.append(h('div.btn-row', { style: 'margin-top:18px' },
      h('button.btn', {
        type: 'button',
        onclick: () => storeRunningTas(),
      }, 'Store the running reflexes'),
      h('button.btn.btn-danger', {
        type: 'button',
        onclick: () => clearRunningTas(),
      }, 'Clear every running reflex')));
    main.append(tasCard);

    /* ------------------------------------------------------------- loadouts */

    const masterCard = h('div.card', h('h2.card-title', 'Loadouts'));
    masterCard.append(h('p.field-hint', { style: 'margin:0 0 14px' },
      'A loadout is five names and nothing else: one stored template per subsystem. '
      + 'Deploying one applies each named subsystem in turn -- USB first, then reflexes, '
      + 'network, Bluetooth, WiFi -- and carries on past a failure, so a missing template '
      + 'means that one subsystem is left alone while the rest still change. Composing a '
      + 'new loadout and setting the boot default are on the Loadouts page.'));

    if (masterNames === null) {
      masterCard.append(h('div.empty', 'The list of stored loadouts could not be read.'));
    } else if (!masterNames.length) {
      masterCard.append(h('div.empty',
        'No loadouts stored. Store a USB, WiFi, Bluetooth, network or reflex template '
        + 'first, then compose one on the Loadouts page.'));
    } else {
      masterCard.append(dataTable(
        h('thead', h('tr',
          h('th', 'Name'), h('th', 'At boot'), h('th', 'Names'), h('th', 'Actions'))),
        h('tbody', ...masterNames.map(n => {
          const entry = loadouts.find(l => l.name === n);
          return h('tr',
            h('td.mono', n),
            h('td', startupName === n ? 'yes' : ''),
            h('td', entry ? missingCell(entry.t) : 'could not be read'),
            h('td', h('div.btn-row',
              h('button.btn.btn-sm', {
                type: 'button',
                onclick: () => inspectMaster(n),
              }, 'Inspect'),
              h('button.btn.btn-sm.btn-primary', {
                type: 'button',
                onclick: () => deployMaster(n),
              }, 'Deploy'),
              h('button.btn.btn-sm.btn-danger', {
                type: 'button',
                onclick: () => deleteMaster(n),
              }, 'Delete'))));
        }))));
    }

    masterCard.append(h('p.field-hint', { style: 'margin-top:14px' },
      'The "Names" column compares each loadout\'s five names against what is actually '
      + 'stored. The device never does this: nothing stops a template being deleted while '
      + 'a loadout still names it, and the loadout finds out at deploy time.'));

    masterCard.append(h('div.btn-row', { style: 'margin-top:18px' },
      h('button.btn', {
        type: 'button',
        onclick: () => deployAdHoc(),
      }, 'Deploy a combination without storing it')));
    main.append(masterCard);

    /* ------------------------------------------------------- small builders */

    /* "Deployed now", for one subsystem. Takes the live reply and a function
       that turns it into kvRows, so a failed or empty read is handled once
       here rather than in three places. */
    function liveSummary(live, what, rows) {
      if (live === null) {
        return h('p.field-hint', { style: 'margin:0 0 14px' },
          'What the ' + what + ' is doing right now could not be read, so there is nothing '
          + 'to compare a stored configuration against.');
      }
      if (isEmptyReply(live)) {
        return h('p.field-hint', { style: 'margin:0 0 14px' },
          'The device reported nothing about the ' + what + '.');
      }
      let built;
      try { built = rows(live); } catch (e) { built = null; }
      if (!built) {
        return h('p.field-hint', { style: 'margin:0 0 14px' },
          'The ' + what + ' reply could not be read.');
      }
      return h('div', { style: 'margin-bottom:18px' },
        h('h3.field-label', 'Deployed now'),
        dataTable({ style: 'margin-top:8px' }, h('tbody', ...built)));
    }

    function namedByCell(value, field) {
      const refs = referencedBy(value, field);
      if (refs.length) return h('span.mono', refs.join(', '));
      if (loadoutsUnread || !masterNames) return 'unknown';
      return h('span.field-hint', 'nothing');
    }

    /* Which of a loadout's five names do not exist. Reported per subsystem
       rather than as a count, because "WiFi missing" and "Bluetooth missing"
       are very different problems. A list that could not be read is reported
       as unknown rather than as present. */
    function missingReferences(t) {
      const out = [];
      /* The NAME goes in the message, not just the subsystem. "WiFi missing"
         sends the operator to the WiFi list to work out which of five names
         it meant; "WiFi gone_wifi" is the thing they have to go and fix. */
      const one = (field, label, list) => {
        const v = str(t[field]);
        if (!v) return;
        if (list === null) out.push(label + ' ' + v + ' (unchecked)');
        else if (list.indexOf(v) === -1) out.push(label + ' ' + v);
      };
      one('template_name_usb', 'USB', usbNames);
      one('template_name_wifi', 'WiFi', wifiNames);
      one('template_name_bluetooth', 'Bluetooth', btNames);
      one('template_name_trigger_actions', 'reflexes', tasNames);
      const net = Array.isArray(t.template_names_network) ? t.template_names_network : [];
      for (const nn of net) {
        if (!nn) continue;
        if (netNames === null) out.push('network ' + nn + ' (unchecked)');
        else if (netNames.indexOf(nn) === -1) out.push('network ' + nn);
      }
      return out;
    }

    function missingCell(t) {
      const named = [
        str(t.template_name_usb) && 'USB',
        str(t.template_name_wifi) && 'WiFi',
        str(t.template_name_bluetooth) && 'Bluetooth',
        str(t.template_name_trigger_actions) && 'reflexes',
        (Array.isArray(t.template_names_network) ? t.template_names_network.length : 0) && 'network',
      ].filter(Boolean);
      if (!named.length) return h('span.field-hint', 'nothing -- this loadout does nothing');
      const missing = missingReferences(t);
      if (!missing.length) return named.join(', ');
      return h('div',
        h('div', named.join(', ')),
        h('div.field-hint', 'missing: ' + missing.join(', ')));
    }

    /* ------------------------------------------------------------ USB flows */

    async function readUsb(n) {
      const g = await guard(() => USB.read(n), 'read the USB composition "' + n + '"');
      if (g === undefined) return null;
      if (isEmptyReply(g)) {
        await showDetail({
          title: 'Nothing stored under "' + n + '"',
          body: 'The device answered with an empty configuration. The name is in the list, '
            + 'so something is stored under it, but it contains no gadget settings this '
            + 'console can read. Deploying it would compose a gadget with no functions and '
            + 'no identity.',
        });
        return null;
      }
      return g;
    }

    /* What is wrong with a stored composition, said before it is deployed
       rather than discovered afterwards. All three of these are refusals or
       dead ends the service will not explain in these terms. */
    function usbProblems(g, skipNetwork) {
      const out = [];
      const used = endpointsUsed(g);
      if (used > USB_ENDPOINT_MAX) {
        out.push('It asks for ' + used + ' of the ' + USB_ENDPOINT_MAX + ' USB endpoints '
          + 'this controller has, so the service will refuse it outright and put the '
          + 'current composition back. Remove a function from it first.');
      }
      if (g.enabled === false) {
        out.push('The gadget is marked disabled, so deploying this presents NOTHING to the '
          + 'host -- no keyboard, no network, no storage.');
      }
      if (!USB_FUNCTIONS.some(f => g[f.key])) {
        out.push('It enables no USB functions at all. The host will enumerate the device '
          + 'and find nothing on it.');
      }
      if (!g.use_RNDIS && !g.use_CDC_ECM && !skipNetwork) {
        out.push('It has no USB network function (neither RNDIS nor CDC ECM), so the usbeth '
          + 'interface disappears when it is deployed. If this console is reached over the '
          + 'USB cable, it does not come back.');
      }
      if (g.use_UMS && !(g.ums_settings && str(g.ums_settings.file))) {
        out.push('Storage is enabled with no image file, so the host sees a drive with '
          + 'nothing behind it.');
      }
      return out;
    }

    function usbSummary(g) {
      const lines = [
        row('Gadget', g.enabled === false ? 'disabled -- the host sees nothing'
          : g.enabled === true ? 'enabled' : 'not recorded'),
        row('Vendor / product', (str(g.vid) || '--') + ' / ' + (str(g.pid) || '--')),
        row('Reported as', [str(g.manufacturer), str(g.product)].filter(Boolean).join(' / ') || 'unset'),
        row('Serial', str(g.serial) || 'unset'),
        row('Endpoints', endpointsUsed(g) + ' of ' + USB_ENDPOINT_MAX),
        '',
        'Functions the host would enumerate:',
      ];
      for (const f of USB_FUNCTIONS) {
        let detail = '';
        try { detail = f.detail(g) || ''; } catch (e) { detail = ''; }
        lines.push('  ' + pad(f.name, 12)
          + (g[f.key] ? (detail ? pad('on', 6) + detail : 'on') : 'off'));
      }
      if (g.use_UMS) {
        const ums = (g.ums_settings && typeof g.ums_settings === 'object') ? g.ums_settings : {};
        lines.push('',
          row('Storage image', str(ums.file) || 'none -- the host sees an empty drive'),
          row('Storage kind', ums.cdrom ? 'CD-ROM, read only' : 'flash drive, writable'));
      }
      return lines;
    }

    async function inspectUsb(n) {
      const g = await readUsb(n);
      if (!g) return;
      const problems = usbProblems(g);
      const body = usbSummary(g).concat(
        problems.length ? ['', 'Before you deploy this:'].concat(problems.map(p => '  - ' + p)) : [],
        ['', 'Deploying it does not change anything else: USB only.']);
      await showDetail({ title: 'USB composition "' + n + '"', body: body.join('\n'), mono: true });
    }

    async function deployUsb(n) {
      const g = await readUsb(n);
      if (!g) return;
      const on = USB_FUNCTIONS.filter(f => g[f.key]).map(f => f.name);
      const killsLink = linkName === 'usbeth' && !g.use_RNDIS && !g.use_CDC_ECM;
      /* The missing network function is the FIRST thing the consequence says
         when it is about to kill this session, so it is not repeated in the
         "Also" list underneath. */
      const problems = usbProblems(g, killsLink);
      const yes = await confirmAction({
        title: 'Deploy "' + n + '"?',
        body: 'The host would enumerate: ' + (on.length ? on.join(', ') : 'nothing')
          + '. Identity ' + (str(g.vid) || '--') + '/' + (str(g.pid) || '--') + '.',
        consequence: (killsLink
          ? 'THIS ENDS THIS SESSION. You are reading this over usbeth, and this composition '
            + 'has no USB network function, so that interface disappears and does not come '
            + 'back. Reach the device over its access point (172.24.0.1 by default) or over '
            + 'Bluetooth afterwards, or power-cycle it to get the boot loadout back. '
          : linkName === 'usbeth'
            ? 'You are reading this over usbeth. The gadget is torn down and rebuilt, so the '
              + 'host re-enumerates it: this page will stall for a few seconds and the '
              + 'network adapter may come back with a different name on the host. '
            : 'Whatever is plugged into the cable loses every USB device this gadget was '
              + 'presenting and gets this set instead. A host in the middle of reading the '
              + 'storage image or running a script over HID will see the device vanish. ')
          + (problems.length ? 'Also: ' + problems.join(' ') : ''),
        confirmLabel: 'Deploy',
        danger: killsLink || problems.length > 0,
      });
      if (!yes) return;
      const r = await guard(() => USB.deploy(n), 'deploy the USB composition "' + n + '"');
      if (r !== undefined) { toast('Deployed "' + n + '".'); Views.templates(); }
    }

    /* ----------------------------------------------------------- WiFi flows */

    async function readWifi(n) {
      const w = await guard(() => WIFI.read(n), 'read the WiFi configuration "' + n + '"');
      if (w === undefined) return null;
      if (isEmptyReply(w)) {
        await showDetail({
          title: 'Nothing stored under "' + n + '"',
          body: 'The device answered with an empty configuration. Deploying it would ask '
            + 'for an unknown working mode with no SSID, which the WiFi subsystem refuses.',
        });
        return null;
      }
      return w;
    }

    /* `skipPublishedPsk` is the same device as usbProblems' `skipNetwork`:
       deployWifi leads its consequence with the key substitution, because on
       wlan0 that IS the consequence, so it must not also arrive as an
       afterthought in the "Also:" list. Inspect passes nothing and gets it. */
    function wifiProblems(w, skipPublishedPsk) {
      const out = [];
      const mode = enumName(w.working_mode, WIFI_WORKING);
      const auth = enumName(w.auth_mode, WIFI_AUTH);
      const ap = (w.ap_BSS && typeof w.ap_BSS === 'object') ? w.ap_BSS : {};
      const joinList = Array.isArray(w.client_BSS_list) ? w.client_BSS_list : [];
      const spawnsAp = mode === 'AP' || mode === 'STA_FAILOVER_AP';
      const pskPublished = auth === 'WPA2_PSK' && isPublishedPSK(ap.PSK);
      /* An SSID the config builder rejects means hostapd is started against
         the PREVIOUS /tmp/hostapd_<iface>.conf, so the channel in THIS
         template never reaches hostapd -- naming it as the reason the access
         point will not come up would contradict the SSID entry three
         sentences later. Hence: no channel verdict when the SSID is the
         thing that fails.
         Computed here, before the regulatory entry, which used to end "The
         access point still comes up" unconditionally -- true of the domain
         substitution on its own, and flatly contradicted further down when
         the channel is one the substituted domain forbids. */
      const ssidBad = spawnsAp && (!str(ap.SSID) || byteLength(ap.SSID) > SSID_MAX_BYTES);
      const chp = (spawnsAp && !ssidBad) ? channelProblem(w) : null;
      /* EVERY way this configuration ends without an access point of its own,
         not just the channel -- because the regulatory entry below promises
         one comes up, and an entry further down that says it does not makes
         this list argue with itself. The channel alone was gated here, so a
         template with an empty domain and a key outside 8..63 read "The
         access point still comes up." two sentences above "there is no access
         point afterwards".
         The key window is checked directly rather than reused from below: a
         PUBLISHED key is replaced with a valid generated one before hostapd
         sees anything, so only a key hostapd actually reads can be out of
         bounds. Same reasoning, and the same condition, as deployWifi's
         keyWontStart. */
      const keyWontStart = spawnsAp && auth === 'WPA2_PSK' && !pskPublished
        && (byteLength(ap.PSK) < PSK_MIN_BYTES || byteLength(ap.PSK) > PSK_MAX_BYTES);
      const apComesUp = !(ssidBad || keyWontStart || (chp && chp.wontStart));

      if (w.disabled) {
        out.push('The radio is marked disabled, so deploying this stops both hostapd and '
          + 'wpa_supplicant: no access point and no joining anything.');
      }
      /* Not "nothing happens". DeploySettings stops wpa_supplicant and
         hostapd before it reaches the working-mode switch, and the default
         branch of that switch returns "Unknown working mode" -- so the radio
         is taken down and nothing is started on it, and the deploy reports
         the error afterwards. UNKNOWN is the proto's zero value, so anything
         that stored a template without setting the field lands here. */
      if (!mode || mode === 'UNKNOWN') {
        out.push('No usable working mode is recorded, so the subsystem cannot tell whether '
          + 'to spawn an access point or join one'
          /* The working-mode switch is INSIDE `if !newWifiSettings.Disabled`
             (service/wifi.go), so a disabled radio never reaches the default
             branch and never produces "Unknown working mode": err is still
             the nil from StopHostapd, the deploy reports success and sets
             CurrentSettings. Claiming a refusal here contradicted the deploy
             dialog, which gets this right -- noApWhy checks w.disabled first
             and says nothing about the mode. */
          + (w.disabled
            ? '. The radio is marked disabled, so the subsystem stops at that and never '
              + 'reaches the working mode: this deploy reports SUCCESS with nothing running. '
              + 'The missing mode only fails a deploy once the radio is switched back on.'
            : ' and refuses the deploy with "Unknown working mode". It refuses it AFTER '
              + 'stopping hostapd and wpa_supplicant: the radio ends up carrying nothing, '
              + 'whatever it was carrying before, and nothing puts the old access point '
              + 'back.'));
      }
      /* NOT a failure, and the previous wording here said it was.
         wifiCreateHostapdConfString writes no country_code line at all, and
         wifiSetReg substitutes "US" for an empty domain and logs it -- and
         DeploySettings ignores even a real error from it ("we don't abort on
         error here"). So the access point comes up; it comes up on someone
         else's rules. Calling that a hostapd failure put a red warning on a
         deploy that works, which is the fastest way to teach an operator to
         click past every other warning on this page. */
      if (spawnsAp && !str(w.regulatory)) {
        out.push('No regulatory domain is recorded, so the device silently uses "US". '
          + (apComesUp ? 'The access point still comes up. ' : '')
          + 'If this device is not in the US that is the wrong domain: channels 12 and 13 '
          + 'are unavailable and the permitted transmit power may not be the local one. Set '
          + 'it on the WiFi page and store the template again.');
      }
      /* An SSID the config builder rejects does NOT mean "refused, nothing
         happened", and saying so would be the reassuring version of a worse
         outcome. wifiCreateHostapdConfString rejects an empty or over-32-byte
         SSID, but runAPMode calls hostapdCreateConfigFile2 and DISCARDS its
         error (service/wifi.go:324), then starts hostapd against
         /tmp/hostapd_<iface>.conf anyway. The new file was never written, so
         hostapd reads whatever is already there: the PREVIOUS access point,
         if one was deployed since boot, and the deploy reports success. The
         file is in /tmp, so after a reboot there is nothing to read and the
         deploy fails instead. Either way the SSID named here is not what
         ends up on the air. Byte count because the service checks Go's
         len(), which is bytes. */
      const ssidBytes = byteLength(ap.SSID);
      if (ssidBad) {
        out.push((!str(ap.SSID)
          ? 'The access point has no SSID, and the service will not build a configuration '
            + 'without one.'
          : 'The SSID is ' + ssidBytes + ' bytes and 802.11 allows ' + SSID_MAX_BYTES
            + ', so the service will not build a configuration from it. It is only '
            + str(ap.SSID).length + ' characters -- an emoji or an accented letter costs two '
            + 'to four bytes each, which is how a short-looking SSID goes over.')
          + ' What happens next is NOT a clean refusal: the service throws that error away, '
          + 'leaves the old hostapd configuration in /tmp untouched and starts hostapd on it '
          + 'regardless. So the access point that comes up is the PREVIOUS one -- its SSID, '
          + 'its key -- and the deploy reports success. If the device has been rebooted '
          + 'since the last access point was deployed there is no old configuration to fall '
          + 'back to and the deploy fails with no access point at all. Neither outcome is '
          + 'the configuration named here.');
      }
      /* The channel, which nothing in the service validates. Under spawnsAp
         only: in plain station mode the channel comes from whatever network
         the device joins and the stored value is never used. */
      if (chp) out.push(chp.text);
      if ((mode === 'STA' || mode === 'STA_FAILOVER_AP') && !joinList.length) {
        out.push('Station mode is set with no network to join; the service refuses this '
          + 'with "no BSS configurations provided".');
      }
      if (auth === 'OPEN' && spawnsAp) {
        out.push('Authentication is OPEN: the access point takes no key, so anything in '
          + 'radio range can join it and reach this console and the device\'s own network.');
      }
      /* ORDER MATTERS HERE, AND IT IS THE SERVICE'S ORDER.
       *
       * guardAccessPointPSK runs at the TOP of DeploySettings, before the
       * radio is stopped and long before hostapd is handed a passphrase. So a
       * published key is replaced by a generated 20-byte one and it is the
       * REPLACEMENT whose length hostapd sees. Two of the five published
       * values -- "toor" and "p4wnp1" -- are themselves under the eight-byte
       * floor, so checking the length first would warn that the access point
       * cannot start when the substitution is the very thing that makes it
       * start. Hence: substitution first, and the length window only for a
       * key that actually reaches hostapd. */
      if (spawnsAp && pskPublished) {
        if (!skipPublishedPsk) {
          out.push('THE STORED KEY WILL NOT BE THE ONE BROADCAST. It is a value published in '
            + 'this project\'s own source, so it is not a secret, and the service refuses to '
            + 'put an access point on it (service/wifi_psk_guard.go). It does not refuse the '
            + 'deploy: before anything else happens it swaps in a key generated for this '
            + 'device, brings the access point up on that and reports success. The reply to '
            + 'that deploy carries the replacement, and this page shows it the moment it '
            + 'arrives -- but only then: if the deploy takes the link with it first, read the '
            + 'key off the WiFi page\'s "Deployed now" card over any other link, or on the '
            + 'device at ' + GENERATED_PSK_PATH + '. Anything holding the stored key cannot '
            + 'join. The stored template is not rewritten, so this happens again on every '
            + 'deploy, with the same generated key.');
        }
      } else if (auth === 'WPA2_PSK' && spawnsAp) {
        /* One check for the whole 8..63 byte window. The old one fired only
           on an empty key while stating the eight-byte rule, so a template
           carrying a three-character key passed as having no problem and then
           died in hostapd with the radio already stopped. */
        const n = byteLength(ap.PSK);
        const tail = ' hostapd rejects the configuration at startup, and because the radio '
          + 'is stopped before it is started there is no access point afterwards and nothing '
          + 'puts the old one back.';
        if (!n) {
          out.push('WPA2 is set with an empty key.' + tail);
        } else if (n < PSK_MIN_BYTES) {
          out.push('The key is ' + n + ' byte' + (n === 1 ? '' : 's') + ' and a WPA2 '
            + 'passphrase must be at least ' + PSK_MIN_BYTES + '.' + tail);
        } else if (n > PSK_MAX_BYTES) {
          /* No "at 64 it is read as a raw hex key": that is hostapd's wpa_psk
             directive, and wifiCreateHostapdConfString only ever writes
             wpa_passphrase. There is nothing for a 64-byte value to be read
             as, which is what the WiFi page says too. */
          out.push('The key is ' + n + ' bytes and a WPA2 passphrase stops at '
            + PSK_MAX_BYTES + '. The device writes it as hostapd\'s wpa_passphrase, which '
            + 'accepts nothing outside ' + PSK_MIN_BYTES + '..' + PSK_MAX_BYTES + ' -- the '
            + 'raw-hex form a 64-character value looks like is the separate wpa_psk '
            + 'directive, which this device never writes.' + tail);
        }
      }
      /* The nexmon flag used to be listed here as "the deploy fails without a
         patched firmware blob". It does not. DeploySettings carries a
         "ToDo: Dis/Enable nexmon if needed" comment where the firmware swap
         would go and never reads the field again -- its only other appearance
         in the service is the %v in describeWifiSettings's log line, and the
         CLI's DisableNexmon default is inside a commented-out block. So the
         flag changes what this console and your templates SAY about the
         device and nothing about the radio: it is not a reason not to deploy,
         and it must not set `danger`. Inspect reports it in the summary
         instead, which is where a stored-but-inert field belongs. */
      return out;
    }

    function wifiSummary(w) {
      const mode = enumName(w.working_mode, WIFI_WORKING);
      const auth = enumName(w.auth_mode, WIFI_AUTH);
      const ap = (w.ap_BSS && typeof w.ap_BSS === 'object') ? w.ap_BSS : {};
      const joinList = (Array.isArray(w.client_BSS_list) ? w.client_BSS_list : [])
        .map(b => str(b && b.SSID)).filter(Boolean);
      const psk = str(ap.PSK);
      return [
        row('Mode', mode ? WORKING_LABEL[mode] : 'not recorded'),
        row('Radio', w.disabled ? 'disabled' : 'enabled'),
        row('Channel', channelShown(w) || 'not recorded'),
        row('Regulatory domain', str(w.regulatory) || 'unset'),
        row('Authentication', auth === 'OPEN' ? 'open -- no key'
          : auth === 'WPA2_PSK' ? 'WPA2 pre-shared key' : 'not recorded'),
        '',
        row('Access point SSID', str(ap.SSID) || 'none'),
        row('  broadcast', w.hide_ssid ? 'hidden' : 'visible'),
        /* Bytes, because the 8..63 window hostapd enforces is a byte window
           and "characters" would have been a different number for any key
           that is not plain ASCII. */
        row('  key', psk
          ? byteLength(psk) + ' bytes, not shown here'
            + (isPublishedPSK(psk) ? ' -- AND NOT WHAT WILL BE BROADCAST, see below' : '')
          : 'none stored'),
        row('Networks to join', joinList.length ? joinList.join(', ') : 'none'),
        /* Reported, not warned about: this build never loads or unloads the
           patched firmware, whatever the flag says. */
        row('Nexmon features', w.nexmon
          ? 'on -- recorded only; this build never loads the patched firmware'
          : 'off'),
        row('Name inside it', str(w.name) || 'unset'),
        '',
        'The key is not printed. It is not needed to decide whether to deploy this, and',
        'this dialog is the kind of thing that ends up in a screenshot. Read it off the',
        'device over SSH if you need to hand it out.',
      ];
    }

    async function inspectWifi(n) {
      const w = await readWifi(n);
      if (!w) return;
      const problems = wifiProblems(w);
      const body = wifiSummary(w).concat(
        problems.length ? ['', 'Before you deploy this:'].concat(problems.map(p => '  - ' + p)) : [],
        ['', 'Deploying it changes the radio only: USB, Bluetooth and the IP settings on',
          'wlan0 are left exactly as they are. The address the access point hands out is a',
          'network template, not part of this.']);
      await showDetail({ title: 'WiFi configuration "' + n + '"', body: body.join('\n'), mono: true });
    }

    async function deployWifi(n) {
      const w = await readWifi(n);
      if (!w) return;
      const mode = enumName(w.working_mode, WIFI_WORKING);
      const ap = (w.ap_BSS && typeof w.ap_BSS === 'object') ? w.ap_BSS : {};
      const spawnsAp = mode === 'AP' || mode === 'STA_FAILOVER_AP';
      const pskBytes = byteLength(ap.PSK);
      const wpa2 = enumName(w.auth_mode, WIFI_AUTH) === 'WPA2_PSK';
      /* The access point will come up on a key the operator does not have.
         Led with rather than listed, because when this console is on wlan0 it
         is not a caveat about the configuration -- it is the end of the
         session, with no SSID change to hint at why. */
      const pskSwapped = spawnsAp && wpa2 && isPublishedPSK(ap.PSK);
      /* A working mode the subsystem does not accept leaves the radio
         carrying NOTHING, and this is the one path that used to miss it.
         DeploySettings stops wpa_supplicant and hostapd before it reaches
         `switch newWifiSettings.WorkingMode`, whose default branch returns
         "Unknown working mode" (service/wifi.go) -- so an unrecorded or
         unknown mode is not "nothing happens", it is the access point gone
         and the deploy reporting an error. UNKNOWN is also the proto's zero
         value, so a template stored by anything that did not set the field
         lands here. */
      const modeUnusable = !mode || mode === 'UNKNOWN';
      /* hostapd will refuse the configuration at startup and the radio is
         already stopped by then, so there is no access point afterwards --
         the same end state as `disabled` or plain station mode, which is why
         it feeds the same flag rather than getting wording of its own. Only
         for a key hostapd actually sees: a published one is replaced with a
         valid generated key before any of this. */
      const keyWontStart = spawnsAp && wpa2 && !pskSwapped
        && (pskBytes < PSK_MIN_BYTES || pskBytes > PSK_MAX_BYTES);
      /* The channel is the other way hostapd exits on a configuration this
         console was previously quiet about. Only the cases where the outcome
         is knowable feed the flag -- channelProblem() says which -- and not
         at all when the SSID is what fails, because then hostapd is started
         against the previous configuration file and never sees this channel.
         Same condition as wifiProblems, so the two cannot disagree. */
      const ssidBad = spawnsAp
        && (!str(ap.SSID) || byteLength(ap.SSID) > SSID_MAX_BYTES);
      const chp = (spawnsAp && !ssidBad) ? channelProblem(w) : null;
      const apWontStart = keyWontStart || !!(chp && chp.wontStart);
      const problems = wifiProblems(w, pskSwapped);
      const onWifi = linkName === 'wlan0';
      const stopsAp = w.disabled || mode === 'STA' || modeUnusable || apWontStart;
      /* WHY there will be no access point, in the service's own order: the
         mode is decided before the AP path runs at all, and a key hostapd
         refuses is decided before it ever reads the channel. Empty when the
         radio is simply switched off or the configuration is plain station
         mode, where "no access point" is the point rather than a failure. */
      const noApWhy = w.disabled ? ''
        : modeUnusable
          ? 'the working mode recorded is not one the subsystem accepts, so the deploy fails '
            + 'with "Unknown working mode" AFTER hostapd and wpa_supplicant have already '
            + 'been stopped'
          : keyWontStart
            ? 'hostapd will refuse the key it carries and the radio is stopped before '
              + 'hostapd is started'
            : (chp && chp.wontStart) ? chp.why : '';
      const noApTail = noApWhy
        ? ' -- ' + noApWhy + '. Nothing puts the old access point back' : '';
      const liveSsid = liveWifi ? str(liveWifi.ssid) : '';
      const ssidChanges = !!(liveSsid && str(ap.SSID) && liveSsid !== str(ap.SSID));
      const yes = await confirmAction({
        title: 'Deploy "' + n + '"?',
        body: (mode ? WORKING_LABEL[mode] : 'working mode not recorded')
          + (str(ap.SSID) ? ', SSID "' + str(ap.SSID) + '"' : '')
          + (channelShown(w) ? ', channel ' + channelShown(w) : '')
          + (str(w.regulatory) ? ', domain ' + str(w.regulatory) : ''),
        consequence: (pskSwapped
          ? (onWifi && !stopsAp ? 'THIS ENDS THIS SESSION, AND NOT FOR THE REASON IT LOOKS '
            + 'LIKE. ' : '')
            + 'The key stored in this configuration is published in this project\'s own '
            + 'source, so the service will not broadcast it: at deploy time it substitutes a '
            + 'key generated for this device and reports success '
            + '(service/wifi_psk_guard.go). The SSID will be the one named above and the key '
            + 'will not, so every client holding the stored key -- including whatever you '
            + 'are reading this on, if it is on this radio -- is locked out until it is given '
            + 'the new one. The reply to this deploy carries the replacement, and this page '
            + 'puts it on screen as soon as it arrives -- so if you are NOT on this radio you '
            + 'will be shown the key. If you are, the reply may never reach this browser: '
            + 'read it afterwards from the WiFi page over USB or Bluetooth, or on the device '
            + 'at ' + GENERATED_PSK_PATH + '. '
          : '')
          + (onWifi
          ? (stopsAp
            ? 'THIS ENDS THIS SESSION. You are reading this over wlan0 and this '
              + 'configuration leaves no access point running'
              + noApTail
              + '. There will be no network to be on. '
              + 'Reach the device over the USB cable (172.16.0.1 by default) afterwards. '
            : ssidChanges
              ? 'You are reading this over wlan0 and the SSID changes from "' + liveSsid
                + '" to "' + str(ap.SSID) + '", so this session ends and you have to join the '
                + 'new network. If the key is also different you need that too: this page '
                + 'does not print a stored key, and the WiFi page shows the one in force only '
                + 'over a link that survives. '
              : 'You are reading this over wlan0. The radio restarts, so expect this page to '
                + 'stall and your client to re-associate; if the key in this configuration '
                + 'differs from the one you joined with, you will not get back on. ')
          : 'Anything joined to this device over WiFi is disconnected. '
            + (stopsAp ? 'The access point stops existing entirely'
              + noApTail + '. '
              : pskSwapped ? 'It has to re-associate, and cannot, because the key it has is '
                + 'not the key the access point comes back on. '
                : 'It has to re-associate, with whatever key this configuration carries. '))
          + (problems.length ? 'Also: ' + problems.join(' ') : ''),
        confirmLabel: 'Deploy',
        danger: (onWifi && stopsAp) || pskSwapped || problems.length > 0,
      });
      if (!yes) return;
      const r = await guard(() => WIFI.deploy(n), 'deploy the WiFi configuration "' + n + '"');
      if (r === undefined) return;

      /* THE REPLACEMENT KEY IS IN THIS REPLY AND NOWHERE ELSE A BROWSER CAN
         REACH IT.
         DeployStoredWifiSettings hands the stored message to
         DeployWiFiSettings, guardAccessPointPSK mutates Ap_BSS.PSK in place
         at the top of DeploySettings, and on success DeploySettings sets
         State.CurrentSettings to that same mutated message -- so the key the
         access point is actually broadcasting comes back here. Dropping it
         while telling the operator to go and read it over SSH is how a deploy
         from this page leaves an access point nobody can join. The WiFi page
         has done this since it was written; this is the same five lines.
         The stored template is NOT rewritten, so a comparison against the
         stored key is what detects the substitution. */
      const sent = str(ap.PSK);
      const back = (r && r.currentSettings && r.currentSettings.ap_BSS
        && typeof r.currentSettings.ap_BSS.PSK === 'string')
        ? r.currentSettings.ap_BSS.PSK : '';
      if (sent && back && back !== sent) {
        await showDetail({
          title: 'The device replaced the access point key',
          body: 'The key stored in "' + n + '" is published in this project’s source, so '
            + 'the device generated its own and brought the access point up on that instead. '
            + 'Write it down now -- this is the only place a browser is shown it:\n\n'
            + back
            + '\n\nIt is kept at ' + GENERATED_PSK_PATH + ' (mode 0600) and reused from then '
            + 'on, so it will not change on the next boot. The template still holds the old '
            + 'key, so every deploy of "' + n + '" lands on this same generated one.',
          mono: true,
        });
      }

      toast('Deployed "' + n + '".');
      Views.templates();
    }

    /* -------------------------------------------------------- reflex  flows */

    async function addTas(n) {
      const running = liveActions ? liveActions.length : null;
      const yes = await confirmAction({
        title: 'Add "' + n + '" to what is running?',
        body: 'The reflexes in this set are added to the device. Nothing running is removed.',
        consequence: (running === null
          ? 'How many reflexes are running now could not be read, so how many you will end '
            + 'up with is unknown. '
          : 'You have ' + running + ' now and will have more. ')
          + 'What is inside "' + n + '" cannot be read before deploying it -- the service '
          + 'has no way to read a stored set back -- so the rules it brings in may duplicate '
          + 'ones you already have, and a reflex that runs a script starts acting the moment '
          + 'its trigger fires. Check the Reflexes page straight afterwards. Nothing is '
          + 'written to disk: a reboot reverts to the boot loadout\'s reflexes.',
        confirmLabel: 'Add them',
      });
      if (!yes) return;
      const r = await guard(() => TAS.add(n), 'add the reflex set "' + n + '"');
      if (r !== undefined) { toast('Added "' + n + '" to the running reflexes.'); Views.templates(); }
    }

    async function replaceTas(n) {
      const losing = mutable ? mutable.length : null;
      const keeping = locked ? locked.length : null;
      const yes = await confirmAction({
        title: 'Replace everything running with "' + n + '"?',
        body: 'This is not the same as Add. Every reflex running on the device is thrown '
          + 'away first, and then the stored set is applied.',
        consequence: (losing === null
          ? 'How many reflexes are running now could not be read, so how many you are about '
            + 'to lose is unknown. '
          : losing + ' reflex' + (losing === 1 ? '' : 'es') + ' will be discarded'
            + (keeping ? ' (the ' + keeping + ' locked one'
              + (keeping === 1 ? '' : 's') + ' the service owns survive)' : '') + '. ')
          + 'They are gone from the running device immediately and are not recoverable '
          + 'unless you stored them first. What is inside "' + n + '" cannot be read before '
          + 'deploying it, so you may be trading a working set for an unknown one.',
        confirmLabel: 'Discard and replace',
        danger: true,
      });
      if (!yes) return;
      const r = await guard(() => TAS.replace(n), 'replace the running reflexes with "' + n + '"');
      if (r !== undefined) { toast('Running reflexes replaced with "' + n + '".'); Views.templates(); }
    }

    /* Storing the running set.
     *
     * There is no StoreDeployedTriggerActionSet, so unlike USB, WiFi and
     * Bluetooth the set has to be read and sent back whole. The actions go
     * back exactly as the device produced them -- ids included -- so no field
     * name in a TriggerAction is ever typed by this console; only the two
     * top-level keys are, and the shape checker covers those.
     *
     * Immutable reflexes are dropped. DeployTriggerActionSetAdd refuses to add
     * one ("we don't allow adding immutable settings via RPC call"), so
     * storing them would put rules in the set that can never be deployed from
     * it, and the count would overstate what the set does. */
    async function storeRunningTas() {
      if (liveActions === null) {
        toast(tasUnknownWhy + ', so there is nothing to store under a name.', true);
        return;
      }
      if (!mutable.length) {
        await showDetail({
          title: 'Nothing to store',
          body: liveActions.length
            ? 'Every reflex running on this device is one of the service\'s own locked '
              + 'rules. Those cannot be added back from a stored set, so a set made of them '
              + 'would deploy nothing. Build a rule on the Reflexes page first.'
            : 'No reflexes are running. Build one on the Reflexes page first.',
        });
        return;
      }
      const armed = mutable.filter(t => t.isActive).length;
      const name = await promptValue({
        title: 'Store the running reflexes',
        label: nameRules.label,
        placeholder: 'engagement-a',
        hint: mutable.length + ' reflex' + (mutable.length === 1 ? '' : 'es') + ' will be stored'
          + (armed === mutable.length ? '' : ' (' + armed + ' armed, '
            + (mutable.length - armed) + ' switched off, kept as they are)')
          + '. ' + nameRules.hint,
        validate: nameRules.validate,
      });
      if (!name) return;
      if (!await nameIsFree('reflex set', name, tasNames)) return;
      const r = await guard(() => TAS.store(name, mutable), 'store the reflex set');
      if (r !== undefined) { toast('Stored ' + mutable.length + ' reflexes as "' + name + '".'); Views.templates(); }
    }

    async function clearRunningTas() {
      if (liveActions !== null && !mutable.length) {
        /* Two different reasons there is nothing to clear, and saying the
           wrong one is a claim about the device that is simply untrue. A
           device answering with an empty set -- which EmitUnpopulated makes
           the normal reply when no reflexes are armed -- used to be told
           every reflex it was running was locked. */
        toast(liveActions.length
          ? 'There is nothing to clear: every running reflex is a locked one the service owns.'
          : 'There is nothing to clear: no reflexes are running.', false);
        return;
      }
      const losing = mutable ? mutable.length : null;
      const yes = await confirmAction({
        title: 'Clear every running reflex?',
        body: 'An empty set is deployed over the running one, which is how the device is '
          + 'returned to reacting to nothing.',
        consequence: (losing === null
          ? 'How many reflexes are running could not be read, so how many you are about to '
            + 'remove is unknown. '
          : 'All ' + losing + ' replaceable reflex' + (losing === 1 ? '' : 'es')
            + ' stop immediately'
            + (locked && locked.length ? '; the ' + locked.length + ' locked one'
              + (locked.length === 1 ? '' : 's') + ' the service owns stay' : '') + '. ')
          + 'The device will no longer react to a host attaching, an access point coming up '
          + 'or anything else on its own. Nothing stored is deleted, and a reboot brings the '
          + 'boot loadout\'s reflexes back.',
        confirmLabel: 'Clear them',
        danger: true,
      });
      if (!yes) return;
      const r = await guard(() => TAS.clear(), 'clear the running reflexes');
      if (r !== undefined) { toast('Running reflexes cleared.'); Views.templates(); }
    }

    /* ------------------------------------------------------- loadout  flows */

    async function readMaster(n) {
      const t = await guard(() => MASTER.read(n), 'read the loadout "' + n + '"');
      if (t === undefined) return null;
      if (isEmptyReply(t)) {
        await showDetail({
          title: 'Nothing stored under "' + n + '"',
          body: 'The device answered with an empty loadout. Deploying it would name no '
            + 'subsystem and change nothing.',
        });
        return null;
      }
      return t;
    }

    function masterSummary(t) {
      const net = Array.isArray(t.template_names_network) ? t.template_names_network : [];
      const mark = (field, list) => {
        const v = str(t[field]);
        if (!v) return 'unset -- left exactly as it is';
        if (list === null) return v + '   (cannot check whether it still exists)';
        return v + (list.indexOf(v) === -1 ? '   MISSING -- nothing is stored under that name' : '   (stored)');
      };
      const lines = [
        row('USB', mark('template_name_usb', usbNames)),
        row('Reflexes', mark('template_name_trigger_actions', tasNames)),
        row('Bluetooth', mark('template_name_bluetooth', btNames)),
        row('WiFi', mark('template_name_wifi', wifiNames)),
      ];
      if (!net.length) {
        lines.push(row('Network', 'unset -- left exactly as it is'));
      } else {
        lines.push(row('Network', ''));
        for (const nn of net) {
          lines.push('  ' + str(nn)
            + (netNames === null ? '   (cannot check whether it still exists)'
              : netNames.indexOf(str(nn)) === -1 ? '   MISSING -- nothing is stored under that name'
                : '   (stored)'));
        }
      }
      return lines;
    }

    async function inspectMaster(n) {
      const t = await readMaster(n);
      if (!t) return;
      const missing = missingReferences(t);
      const body = masterSummary(t).concat([
        '',
        'Deploying applies only the subsystems named above, in that order, and carries on',
        'past a failure -- so one missing template does not stop the others. Anything left',
        'unset keeps its current configuration.',
        '',
        startupName === n
          ? 'This is the boot default: it is deployed on every boot.'
          : 'This is not the boot default.',
      ], missing.length
        ? ['', 'It names ' + missing.length
          + (missing.length === 1 ? ' template that cannot' : ' templates that cannot')
          + ' be found: ' + missing.join(', ') + '. '
          + (missing.length === 1 ? 'That subsystem' : 'Those subsystems')
          + ' will be left alone and the failure will appear only in the journal.']
        : []);
      await showDetail({ title: 'Loadout "' + n + '"', body: body.join('\n'), mono: true });
    }

    async function deployMaster(n) {
      const t = await readMaster(n);
      if (!t) return;
      const net = Array.isArray(t.template_names_network) ? t.template_names_network : [];
      const named = [
        str(t.template_name_usb) && 'USB ' + str(t.template_name_usb),
        str(t.template_name_trigger_actions) && 'reflexes ' + str(t.template_name_trigger_actions),
        net.length && 'network ' + net.join(' + '),
        str(t.template_name_bluetooth) && 'Bluetooth ' + str(t.template_name_bluetooth),
        str(t.template_name_wifi) && 'WiFi ' + str(t.template_name_wifi),
      ].filter(Boolean);
      const missing = missingReferences(t);
      if (!named.length) {
        await showDetail({
          title: 'This loadout does nothing',
          body: '"' + n + '" names no template for any subsystem, so deploying it would '
            + 'change nothing at all. Compose it on the Loadouts page or delete it.',
        });
        return;
      }
      const yes = await confirmAction({
        title: 'Deploy "' + n + '"?',
        body: 'Applies, in this order: ' + named.join(', ') + '.',
        consequence: 'This reconfigures ' + (named.length > 1 ? named.length
          + ' subsystems at once' : 'one subsystem') + '. '
          + (str(t.template_name_trigger_actions)
            ? 'The reflexes step REPLACES every reflex running on the device rather than '
              + 'adding to them. '
            : '')
          + (linkName
            ? 'You are reading this over ' + linkName + ', which this loadout '
              + ((linkName === 'usbeth' && str(t.template_name_usb))
                || (linkName === 'wlan0' && str(t.template_name_wifi))
                || (linkName === 'bteth' && str(t.template_name_bluetooth))
                ? 'reconfigures, so expect this session to end. '
                : 'does not name directly -- but the network templates it applies can still '
                  + 'change that interface\'s address. ')
            : 'Whichever link you are reading this over may well be one of them, so expect '
              + 'the console to disconnect. ')
          + 'Reload after a few seconds; if the address or SSID changed you will have to '
          + 'reconnect to the new one. '
          + (missing.length
            ? 'It also names ' + missing.length
              + (missing.length === 1 ? ' template that cannot' : ' templates that cannot')
              + ' be found (' + missing.join(', ') + '); '
              + (missing.length === 1 ? 'that subsystem is' : 'those subsystems are')
              + ' left alone and the rest still change, so the device ends up '
              + 'half-configured.'
            : ''),
        confirmLabel: 'Deploy loadout',
        danger: true,
      });
      if (!yes) return;
      const r = await guard(() => MASTER.deploy(n), 'deploy the loadout "' + n + '"');
      if (r !== undefined) { toast('Deployed "' + n + '".'); Views.templates(); }
    }

    async function deleteMaster(n) {
      const isBoot = startupName === n;
      const yes = await confirmAction({
        title: 'Delete loadout "' + n + '"?',
        body: 'The loadout is removed from the device. The templates it named are not '
          + 'touched, and nothing running changes.',
        consequence: isBoot
          ? 'THIS IS THE BOOT DEFAULT. The device keeps "' + n + '" as the name to deploy at '
            + 'boot, and nothing clears that for you: at the next boot the service will look '
            + 'for a loadout that no longer exists, fall back to its own built-in defaults, '
            + 'and deploy none of your stored configuration. Set a different boot default on '
            + 'the Loadouts page first.'
          : 'Nothing else references a loadout, so nothing else breaks. It cannot be '
            + 'recovered unless the template database was backed up (System page).',
        confirmLabel: 'Delete',
        danger: true,
      });
      if (!yes) return;
      const r = await guard(() => MASTER.remove(n), 'delete the loadout "' + n + '"');
      if (r !== undefined) { toast('Deleted "' + n + '".'); Views.templates(); }
    }

    /* Deploy a combination without storing it.
     *
     * DeployMasterTemplate had no caller anywhere, so the only way to apply
     * several subsystems together was to first create a stored loadout for it.
     * This is the same operation without leaving a name behind -- useful
     * exactly once, which is when you want it.
     *
     * All five keys are sent every time, with "" and [] for the ones left
     * unset, because that is precisely how the service reads them
     * (`if len(mt.TemplateNameUsb) > 0`) and because a literal with every key
     * present is a literal tools/check-rpc-shapes.py can check in full. */
    async function deployAdHoc() {
      const have = {
        usb: usbNames || [], wifi: wifiNames || [], bt: btNames || [],
        tas: tasNames || [], net: netNames || [],
      };
      if (!Object.keys(have).some(k => have[k].length)) {
        await showDetail({
          title: 'Nothing to combine',
          body: 'No templates are stored, or none of the lists could be read. Store a USB, '
            + 'WiFi, Bluetooth, network or reflex template first.',
        });
        return;
      }
      const none = '-- leave alone --';
      const r = await promptForm({
        title: 'Deploy a combination',
        intro: 'This applies the templates you pick, right now, and stores nothing. Only the '
          + 'subsystems you name are touched; anything left alone keeps its current '
          + 'configuration. The reflexes pick REPLACES every reflex running on the device.',
        confirmLabel: 'Review',
        fields: [
          { key: 'usb', label: 'USB', options: have.usb, emptyLabel: none },
          { key: 'wifi', label: 'WiFi', options: have.wifi, emptyLabel: none },
          { key: 'bt', label: 'Bluetooth', options: have.bt, emptyLabel: none },
          { key: 'tas', label: 'Reflexes (replaces what is running)', options: have.tas, emptyLabel: none },
          { key: 'net', label: 'Network', options: have.net, multiple: true,
            hint: 'One template per interface, so several may apply. Ctrl-click or cmd-click '
                + 'to pick more than one; pick none to leave networking alone.' },
        ],
        validate: v => {
          if (!v.usb && !v.wifi && !v.bt && !v.tas && !v.net.length) {
            return 'Pick at least one template, or this would do nothing.';
          }
          return null;
        },
      });
      if (!r) return;

      const chosen = [
        r.usb && 'USB ' + r.usb,
        r.tas && 'reflexes ' + r.tas,
        r.net.length && 'network ' + r.net.join(' + '),
        r.bt && 'Bluetooth ' + r.bt,
        r.wifi && 'WiFi ' + r.wifi,
      ].filter(Boolean);
      const yes = await confirmAction({
        title: 'Deploy this combination?',
        body: 'Applies, in this order: ' + chosen.join(', ') + '. Nothing is stored.',
        consequence: 'Each subsystem is applied in turn and a failure does not stop the '
          + 'rest, so this can leave the device half-configured. '
          + (r.tas ? 'Every reflex running on the device is discarded first, except the '
            + 'locked ones the service owns. ' : '')
          + (linkName
            ? 'You are reading this over ' + linkName + '; a USB, WiFi or network template '
              + 'that touches it ends this session. '
            : 'Whichever link you are reading this over may be one of these, so expect the '
              + 'console to disconnect. ')
          + 'Because nothing is stored, a reboot undoes all of it and the boot loadout is '
          + 'applied instead.',
        confirmLabel: 'Deploy now',
        danger: true,
      });
      if (!yes) return;
      const res = await guard(() => Api.rpc('DeployMasterTemplate', {
        template_name_usb: r.usb || '',
        template_name_wifi: r.wifi || '',
        template_name_bluetooth: r.bt || '',
        template_name_trigger_actions: r.tas || '',
        template_names_network: r.net,
      }), 'deploy the combination');
      if (res !== undefined) { toast('Deployed.'); Views.templates(); }
    }

    /* ----------------------------------------------------- copy  and delete */

    /* Copy, for USB and WiFi.
     *
     * The point is to keep a known-good configuration before overwriting it:
     * the Cable and WiFi pages' store buttons write whatever name you type
     * straight over an existing one, and before this there was no way to take
     * a copy first.
     *
     * The settings object goes back exactly as the device produced it, so
     * none of its field names are typed here. The one exception is
     * WiFiSettings.name, which is the template name recorded INSIDE the
     * message -- leave it alone and the copy reports the original's name on
     * the WiFi page after it is deployed, which is a quiet lie about which
     * template is running. */
    async function copyStored(kind, n, existing) {
      const src = kind === USB ? await readUsb(n) : await readWifi(n);
      if (!src) return;
      const name = await promptValue({
        title: 'Copy "' + n + '"',
        label: nameRules.label,
        value: n + '-copy',
        hint: 'Stores a second copy of "' + n + '" under a new name. Nothing is deployed and '
          + 'the original is untouched. ' + nameRules.hint,
        confirmLabel: 'Store copy',
        validate: v => {
          const problem = nameRules.validate(v);
          if (problem) return problem;
          if (v === n) return 'That is the name it already has. Pick a different one.';
          return null;
        },
      });
      if (!name) return;
      if (!await nameIsFree(kind.label, name, existing)) return;
      if (kind === WIFI) src.name = name;
      const r = await guard(() => kind.copy(name, src), 'store the copy');
      if (r !== undefined) { toast('Copied "' + n + '" to "' + name + '".'); Views.templates(); }
    }

    async function deleteStored(kind, n, existing) {
      const yes = await confirmAction({
        title: 'Delete ' + kind.label + ' "' + n + '"?',
        body: 'The stored ' + kind.label + ' is removed from the device. Nothing running '
          + 'changes -- what is deployed now stays deployed.',
        consequence: referenceWarning(n, kind.field)
          + ' It cannot be recovered unless the template database was backed up (System page).',
        confirmLabel: 'Delete',
        danger: true,
      });
      if (!yes) return;
      const r = await guard(() => kind.remove(n), 'delete the ' + kind.label + ' "' + n + '"');
      if (r !== undefined) { toast('Deleted "' + n + '".'); Views.templates(); }
    }
  };

})();
