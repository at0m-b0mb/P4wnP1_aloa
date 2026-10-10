/* view-wifi.js -- the WiFi editor: the access point this device spawns, or the
 * network it joins.
 *
 * WHY THIS FILE EXISTS
 *
 * This console could not change WiFi at all. DeployWiFiSettings had no caller
 * anywhere in dist/www, and the Radio page -- since retired, because every
 * card it held now has a page that can change what it shows -- rendered
 * GetWiFiState as a read-only table. Seven more RPCs had no caller either:
 *
 *     DeployWiFiSettings        StoreWifiSettings
 *     GetStoredWifiSettings     ListStoredWifiSettings
 *     DeleteStoredWifiSettings  DeployStoredWifiSettings
 *
 * The product knew. dist/scripts/firstboot-secure-defaults.sh writes the
 * initial credentials file on every device, and where it should tell the
 * operator how to change the access point name and key it says, in as many
 * words, "There is no field for this in the web console" and sends them to the
 * CLI instead. The original client had a whole WiFi page
 * (web_client/hvueComponentWiFi.go, 390 lines, deleted in 8191890); this is
 * that page rebuilt in this console's vocabulary, with the things this project
 * has since learned about its own radio written next to the fields they
 * constrain.
 *
 * WHAT IS DANGEROUS HERE
 *
 * Three things, all of them ways to end up with no radio and no idea why:
 *
 *   1. Applying new settings while reading this page over the device's own
 *      access point disconnects the operator mid-request, and they then have
 *      to rejoin with the NEW name and the NEW key.
 *   2. service/wifi_psk_guard.go REFUSES to broadcast a PSK published in this
 *      project's source and substitutes a per-device random one. Deploy one of
 *      those keys over the access point you are using and you are locked out
 *      of a network whose key you do not know.
 *   3. Station mode stops the access point BEFORE it tries to join anything
 *      (DeploySettings in service/wifi.go: StopWpaSupplicant, StopHostapd,
 *      then runStaMode). If the target network is not in the scan, the deploy
 *      fails with the old access point already gone.
 *
 * So this page says all three, in the confirmation, before anything is sent.
 *
 * Everything is wrapped in an IIFE and only Views.wifi is exported. A top-level
 * `const` in a classic script lands in the shared global lexical environment,
 * so a name this file and app.js (or a sibling view) both declared would be a
 * SyntaxError that stops the whole console from loading.
 */
'use strict';

(function () {

  /* --- the enums ----------------------------------------------------------
   *
   * Order IS the proto enum order (proto/grpc.proto). Get this wrong and the
   * page describes a different configuration than the device has.
   *
   *   WiFiWorkingMode: UNKNOWN=0, AP=1, STA=2, STA_FAILOVER_AP=3
   *   WiFiAuthMode:    WPA2_PSK=0, OPEN=1          <- WPA2 is the ZERO value
   *   WiFiStateMode:   STA_NOT_CONNECTED=0, AP_UP=1, STA_CONNECTED=2
   *
   * The bridge marshals with UseProtoNames and without UseEnumNumbers, so a
   * real device sends the NAME ("AP", "WPA2_PSK"). The jsdom render harness
   * and tools/mock-service.py both send the NUMBER. Every reader below takes
   * either rather than trusting one of them.
   *
   * `what` is what the mode DOES, read off DeploySettings / runStaMode /
   * runAPMode in service/wifi.go rather than guessed from the name. */
  const WORKING_MODES = [
    {
      key: 'UNKNOWN', label: 'Unknown', offerable: false, ap: false, sta: false,
      what: 'Not a usable working mode. DeploySettings falls through to its default branch and '
          + 'returns "Unknown working mode" without starting anything -- after it has already '
          + 'stopped hostapd and wpa_supplicant.',
    },
    {
      key: 'AP', label: 'Access point', offerable: true, ap: true, sta: false,
      what: 'The device runs hostapd and spawns its own 2.4GHz network. This is how the console '
          + 'is normally reached without a cable, and it is what the shipped configuration does.',
    },
    {
      key: 'STA', label: 'Join a network (station)', offerable: true, ap: false, sta: true,
      what: 'The device runs wpa_supplicant and joins one of the networks listed below. It '
          + 'scans first and only attempts a network it can currently see; if none of them is '
          + 'in the scan, the deploy fails and the radio is left carrying nothing.',
    },
    {
      key: 'STA_FAILOVER_AP', label: 'Join a network, fall back to access point',
      offerable: true, ap: true, sta: true,
      what: 'Tries to join one of the networks below and, if that fails, brings up the access '
          + 'point instead. Both halves have to be filled in, because either one may be the '
          + 'one that ends up running.',
    },
  ];
  const WORKING_BY_KEY = {};
  for (const m of WORKING_MODES) WORKING_BY_KEY[m.key] = m;

  const AUTH_MODES = [
    {
      key: 'WPA2_PSK', label: 'WPA2 with a pre-shared key',
      what: 'hostapd is configured wpa=2, WPA-PSK, CCMP only -- no WPA1 and no TKIP, and Open '
          + 'System authentication only, never the WEP-era Shared Key handshake. The key below '
          + 'is what a client has to present.',
    },
    {
      key: 'OPEN', label: 'Open -- no key at all',
      what: 'There is no authentication and no encryption. Anyone in range joins, and every '
          + 'byte in either direction is readable by anyone listening -- including whatever a '
          + 'payload on a target machine sends back over it.',
    },
  ];
  const AUTH_BY_KEY = {};
  for (const m of AUTH_MODES) AUTH_BY_KEY[m.key] = m;

  const STATE_MODES = ['not connected', 'access point up', 'joined a network'];

  function enumKey(table, v) {
    if (typeof v === 'number') return table[v] ? table[v].key : null;
    const s = String(v === null || v === undefined ? '' : v).toUpperCase();
    for (const e of table) if (e.key === s) return e.key;
    if (/^\d+$/.test(s) && table[Number(s)]) return table[Number(s)].key;
    return null;
  }
  const workingKey = v => enumKey(WORKING_MODES, v);
  const authKey = v => enumKey(AUTH_MODES, v);

  function stateModeText(v) {
    if (typeof v === 'number') return STATE_MODES[v] || 'unknown';
    const s = String(v === null || v === undefined ? '' : v).toUpperCase();
    if (s === 'STA_NOT_CONNECTED') return STATE_MODES[0];
    if (s === 'AP_UP') return STATE_MODES[1];
    if (s === 'STA_CONNECTED') return STATE_MODES[2];
    if (/^\d+$/.test(s) && STATE_MODES[Number(s)]) return STATE_MODES[Number(s)];
    return 'unknown';
  }

  const usesAP = key => !!(WORKING_BY_KEY[key] && WORKING_BY_KEY[key].ap);
  const usesSTA = key => !!(WORKING_BY_KEY[key] && WORKING_BY_KEY[key].sta);
  const modeLabel = key => (WORKING_BY_KEY[key] ? WORKING_BY_KEY[key].label : 'unknown');

  /* --- PSKs this device will not broadcast --------------------------------
   *
   * Read off compromisedPSKs in service/wifi_psk_guard.go. The comparison
   * there is strings.EqualFold on a trimmed value, so it is case-insensitive
   * and ignores surrounding whitespace; this matches that exactly, because
   * warning about a key the guard will not catch -- or failing to warn about
   * one it will -- is worse than not warning at all.
   *
   * dist/scripts/servicestart.sh still deploys the first of these at boot, so
   * this is not hypothetical: it is what a freshly flashed device tries to
   * broadcast, and the guard is the only reason it does not. */
  const PUBLISHED_PSKS = [
    'MaMe82-P4wnP1',      // upstream default, in dist/db and years of docs
    'HackProKP-changeme', // this fork's placeholder in service/defaults.go
    'p4wnp1',
    'toor',
    'changeme',
  ];
  const GENERATED_PSK_PATH = '/etc/p4wnp1/generated-ap.psk';
  function isPublishedPSK(psk) {
    const p = String(psk === null || psk === undefined ? '' : psk).trim().toLowerCase();
    if (!p) return false;
    return PUBLISHED_PSKS.some(bad => bad.toLowerCase() === p);
  }

  /* --- sizes --------------------------------------------------------------
   *
   * An SSID's limit is in BYTES, and the two limits that matter are different
   * numbers for different reasons.
   *
   * 32 bytes is 802.11, and the service enforces it:
   * wifiCreateHostapdConfString refuses anything longer and names the byte
   * count, because the alternative is hostapd exiting during the deploy and
   * the operator seeing "the WiFi does not work" with nothing pointing at the
   * name they chose.
   *
   * 15 bytes is this device's OLED panel. The first-boot credentials card
   * builds a scan-to-join QR, and a panel-sized symbol -- version 3 at error
   * correction level L, the largest that fits 64 pixels at two pixels per
   * module -- holds 53 bytes (oled/qr.go). A join URI is
   *
   *     WIFI:T:WPA;S:<ssid>;P:<key>;;
   *
   * which is 18 bytes of structure plus the whole SSID plus the whole key, so
   * with the 20-character generated key there are 15 bytes left for the name.
   * Over budget the card drops the join code and prints the key to be typed
   * with a thumb instead. service/shipped_db_ssid_test.go pins the shipped
   * SSID to this same 15 and firstboot-secure-defaults.sh tells the operator
   * the same number.
   *
   * Emoji cost 3-4 bytes EACH, and the SSID this image ships starts with one,
   * so counting characters instead of bytes would be wrong by four on the very
   * first device anyone opens this page on. */
  const SSID_MAX_BYTES = 32;
  const QR_SSID_BUDGET = 15;
  const QR_CAPACITY = 53;
  const QR_URI_OVERHEAD = 18;   // "WIFI:T:WPA;S:" + ";P:" + ";;"

  /* UTF-8 length without TextEncoder: jsdom does not put TextEncoder on the
     window, and a view that throws during render renders nothing at all. */
  function utf8len(s) {
    let n = 0;
    for (const ch of String(s === null || s === undefined ? '' : s)) {
      const c = ch.codePointAt(0);
      n += c < 0x80 ? 1 : c < 0x800 ? 2 : c < 0x10000 ? 3 : 4;
    }
    return n;
  }
  const charCount = s => [...String(s === null || s === undefined ? '' : s)].length;

  /* WiFiURI (oled/qr.go) backslash-escapes these five characters, and every
     backslash is another byte out of the 53. */
  function qrFieldBytes(s) {
    let n = 0;
    for (const ch of String(s === null || s === undefined ? '' : s)) {
      const c = ch.codePointAt(0);
      n += c < 0x80 ? 1 : c < 0x800 ? 2 : c < 0x10000 ? 3 : 4;
      if (ch === '\\' || ch === ';' || ch === ',' || ch === ':' || ch === '"') n += 1;
    }
    return n;
  }
  const joinUriBytes = (ssid, psk) => QR_URI_OVERHEAD + qrFieldBytes(ssid) + qrFieldBytes(psk);

  /* WPA2 pre-shared keys are 8..63 characters.
     wifiCreateHostapdConfString writes the value as hostapd's
     `wpa_passphrase`, and hostapd refuses that directive outside 8..63 and
     exits -- it does NOT fall back to reading a 64-character value as a raw
     hex key. That form is `wpa_psk`, a separate directive this device never
     writes, so there is nothing for a 64-character value to be read as. The
     service checks the lower bound in the CLI path only
     (cli_client/cmd_wifi.go), so both bounds are checked here for every path. */
  const PSK_MIN = 8;
  const PSK_MAX = 63;

  /* The channels this device can use at all. wifiCreateHostapdConfString sets
     hw_mode=g unconditionally, so 2.4GHz only, and the CLI refuses anything
     outside 1..14. */
  const CHANNEL_MIN = 1;
  const CHANNEL_MAX = 14;

  /* --- addresses, only to work out whether this page rides on the AP ------ */

  function ip4(s) {
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

  /* The address this image ships wlan0 with. Used only as a fallback when the
     interface list cannot be read: the DEPLOYED address is the real answer,
     and an operator who changed it on the Network page would be misled by a
     constant. */
  const SHIPPED_WLAN_ADDR = '172.24.0.1';

  /* Is this console being read over the device's own access point?
   *
   * An exact match on wlan0's own address is certain: the browser is talking to
   * that address. A match on wlan0's network is strong but not certain. Anything
   * else -- a hostname, a proxy, a link this console cannot see -- is unknown,
   * and the page then says nothing rather than reassuring anyone, because a
   * wrong reassurance here is how an operator loses the device. */
  function apLink(interfaces) {
    const host = String(location.hostname || '');
    const list = (interfaces && Array.isArray(interfaces.list)) ? interfaces.list : [];
    const wlan = list.find(i => i && i.name === 'wlan0');
    if (wlan) {
      if (wlan.ipAddress4 && String(wlan.ipAddress4) === host) {
        return { certain: true, addr: String(wlan.ipAddress4) };
      }
      const hostNum = ip4(host);
      const addr = ip4(wlan.ipAddress4);
      const mask = ip4(wlan.netmask4);
      if (hostNum !== null && addr !== null && mask !== null && mask !== 0
          && (((hostNum ^ addr) & mask) === 0)) {
        return { certain: false, addr: String(wlan.ipAddress4) };
      }
      return null;
    }
    /* No interface list to check against, so fall back to the shipped address.
       Reported as uncertain, which it is. */
    if (host === SHIPPED_WLAN_ADDR) return { certain: false, addr: host };
    return null;
  }

  /* --- small builders, in the console's existing vocabulary --------------- */

  function field(label, input, hint) {
    return h('label.field',
      h('span.field-label', label),
      input,
      hint ? h('span.field-hint', hint) : null);
  }

  function checkRow(label, checked, onChange, hint) {
    return h('div', { style: 'margin-bottom:14px' },
      h('label.check',
        h('input', { type: 'checkbox', checked: !!checked, onchange: e => onChange(e.target.checked) }),
        h('span', label)),
      hint ? h('p.field-hint', { style: 'margin:0 0 0 26px' }, hint) : null);
  }

  function cellInput(value, placeholder, onInput, ariaLabel) {
    return h('input', {
      type: 'text', value: value === undefined || value === null ? '' : String(value),
      placeholder: placeholder || '', 'aria-label': ariaLabel, autocomplete: 'off',
      oninput: e => onInput(e.target.value),
    });
  }

  const trimmed = v => String(v === undefined || v === null ? '' : v).trim();

  /* Whether keys are on screen, and a template waiting to be edited, both
     survive the re-render that follows every action on this page. showKeys is
     a deliberate choice the operator made; pendingDraft is consumed once, so
     a loaded template is not overwritten by the device's own settings the
     moment the page rebuilds itself. */
  let showKeys = false;
  let pendingDraft = null;

  /* ---------------------------------------------------------------- the view */

  Views.wifi = async function () {
    const main = clear($('#view'));
    main.append(pageHead('WiFi',
      'The access point this device spawns, or the network it joins. This is the only place '
      + 'either can be changed from a browser.'));

    /* Consumed on entry: an action below re-renders the page, and a template
       loaded for editing must not survive into the render after that one. */
    const loaded = pendingDraft;
    pendingDraft = null;

    const [state, stored, interfaces] = await Promise.all([
      guard(() => Api.rpc('GetWiFiState'), 'read the WiFi state'),
      /* Not guarded: an empty template store is the normal state of a fresh
         device, and a toast about it on every visit is noise, not information. */
      Api.rpc('ListStoredWifiSettings').catch(() => null),
      /* Read ONLY to work out whether this page is riding on wlan0. There is no
         interfaces card here -- that is view-network.js's job. */
      Api.rpc('GetAllDeployedEthernetInterfaceSettings').catch(() => null),
    ]);
    State.wifi = state;

    const templates = (stored && Array.isArray(stored.msgArray) ? stored.msgArray : [])
      .filter(n => typeof n === 'string' && n.length)
      .slice()
      .sort((a, b) => a.localeCompare(b));
    const link = apLink(interfaces);

    /* --- the link warning, before anything else on the page -------------- */
    if (link) {
      main.append(h('div.banner.banner-danger',
        h('p.banner-title', link.certain
          ? 'You are reading this over the device’s own access point'
          : 'You look to be reading this over the device’s own access point'),
        h('p', (link.certain
          ? 'This console is talking to wlan0’s own address (' + location.hostname + '). '
          : 'This console’s address (' + location.hostname + ') is inside wlan0’s network, so '
            + 'that is almost certainly the link carrying this page. ')
          + 'Applying anything below restarts the radio and drops this page. The request itself '
          + 'will probably never answer -- the link dies while it is still in flight -- so '
          + 'expect a timeout even when the change was made.'),
        h('p', 'The way back in: rejoin with the NEW name and the NEW key, and return to '
          + link.addr + '. Failing that, the USB cable answers on 172.16.0.1 when a RNDIS or '
          + 'CDC ECM composition is deployed, the Serial USB function gives a console over '
          + 'ttyGS0, and an OLED panel can show the access point’s current name and key with '
          + 'no network at all.')));
    }

    /* --- the subsystem may not be there ---------------------------------- */
    if (state === undefined) {
      main.append(h('div.banner.banner-danger',
        h('p.banner-title', 'The WiFi state could not be read'),
        h('p', 'The WiFi RPCs answer "the WiFi subsystem is unavailable on this device" when '
          + 'the service could not bring it up: no wlan0 on this board, or hostapd or '
          + 'wpa_supplicant missing. On a cold boot it can also mean brcmfmac had not finished '
          + 'loading its firmware when the service started -- the service waits 15 seconds for '
          + 'wlan0 and then carries on without it. The Journal has the reason.'),
        h('p', 'No editor is shown, deliberately. Building a form out of defaults while the '
          + 'device’s real configuration is unknown is how an operator applies a blank access '
          + 'point over a working one.')));
      main.append(h('div.btn-row', { style: 'margin-bottom:28px' },
        h('button.btn.btn-primary', { type: 'button', onclick: () => Views.wifi() },
          'Try again')));
      main.append(renderTemplates(templates, link, true));
      return;
    }

    const live = (state && typeof state === 'object') ? state : {};
    const cs = (live.currentSettings && typeof live.currentSettings === 'object')
      ? live.currentSettings : {};

    main.append(renderDeployedCard(live, cs));

    /* --- the editor ------------------------------------------------------- */

    /* A form with unsaved edits must not be rebuilt from under the operator.
       State.formDirty is what reloadViewOnStateChange() in app.js checks; the
       teardown clears it so a half-finished edit here cannot block another
       page from refreshing later. */
    State.formDirty = false;
    onViewTeardown(() => { State.formDirty = false; });
    const markDirty = () => { State.formDirty = true; };

    const editorHost = h('div');
    main.append(editorHost);

    /* Every key input and every "Show keys" button painted this round, so the
       toggle can flip them all in place. Flipping input.type directly rather
       than repainting is what keeps focus and the caret where they were. */
    let keyInputs = [];
    let keyToggles = [];
    function toggleKeys() {
      showKeys = !showKeys;
      for (const el of keyInputs) el.type = showKeys ? 'text' : 'password';
      for (const b of keyToggles) b.textContent = showKeys ? 'Hide keys' : 'Show keys';
    }
    function keysToggleButton() {
      const b = h('button.btn.btn-sm.btn-quiet', { type: 'button', onclick: toggleKeys },
        showKeys ? 'Hide keys' : 'Show keys');
      keyToggles.push(b);
      return b;
    }

    let draft = null;

    /* Build the editable draft from what the device (or a stored template)
       reported. Defensive about types throughout: this is device-supplied
       JSON, and a view that throws renders nothing at all. */
    function loadDraft(source) {
      const s = (source && typeof source === 'object') ? source : {};
      const ap = (s.ap_BSS && typeof s.ap_BSS === 'object') ? s.ap_BSS : {};
      const clients = Array.isArray(s.client_BSS_list) ? s.client_BSS_list : [];
      draft = {
        name: typeof s.name === 'string' ? s.name : '',
        disabled: s.disabled === true,
        regulatory: typeof s.regulatory === 'string' ? s.regulatory : '',
        workingMode: workingKey(s.working_mode),
        authMode: authKey(s.auth_mode),
        channel: Number(s.channel) ? String(Number(s.channel)) : '',
        hideSsid: s.hide_ssid === true,
        nexmon: s.nexmon === true,
        apSsid: typeof ap.SSID === 'string' ? ap.SSID : '',
        apPsk: typeof ap.PSK === 'string' ? ap.PSK : '',
        clients: clients
          .filter(c => c && typeof c === 'object')
          .map(c => ({
            SSID: typeof c.SSID === 'string' ? c.SSID : '',
            PSK: typeof c.PSK === 'string' ? c.PSK : '',
          })),
      };
    }

    /* --- validation -------------------------------------------------------
     *
     * Everything here refuses BEFORE the disconnect warning. Letting someone
     * confirm "this will drop your connection" for a configuration that cannot
     * work is a cruel way to find out. */
    function problems() {
      const out = [];
      const m = draft.workingMode;

      if (!m || !WORKING_BY_KEY[m].offerable) {
        out.push('Pick a working mode. The device reported a mode this console does not '
          + 'recognise, or none at all, and there is nothing safe to default to.');
        return out;
      }

      /* Empty is NOT refused here, because the service accepts it: wifiSetReg
         (service/wifi.go:1070) substitutes "US" and logs that it did, and the
         field hint above says so. Refusing it anyway put the operator in a
         loop: the refusal itself told them to leave the field empty, and
         leaving it empty produced the same refusal, with Apply dead either
         way. What empty costs is said in the confirmation instead, next to the
         rest of the consequences.
         A MALFORMED value is a different thing and is still refused: "iw reg
         set" rejects it, the service logs the failure and carries on, and the
         radio keeps whatever domain was in force -- not the one asked for, and
         not shown anywhere. */
      const reg = trimmed(draft.regulatory);
      if (reg && !/^[A-Za-z]{2}$/.test(reg)) {
        out.push('"' + reg + '" is not a regulatory domain. It is two letters, ISO/IEC 3166-1 '
          + 'alpha-2 -- GB, US, DE. "iw reg set" refuses anything else; the service logs that '
          + 'failure and carries on, so the radio would keep whatever domain was already in '
          + 'force and nothing on this page would tell you which one that is. Give two letters, '
          + 'or clear the field to have the service substitute US on purpose.');
      }

      if (usesAP(m)) {
        const ssidBytes = utf8len(draft.apSsid);
        if (!ssidBytes) {
          out.push('The access point needs a name. The service refuses an empty SSID outright: '
            + 'hostapd will not start without one.');
        } else if (ssidBytes > SSID_MAX_BYTES) {
          out.push('The access point name is ' + ssidBytes + ' bytes. 802.11 allows '
            + SSID_MAX_BYTES + ' and the service refuses anything longer. Emoji cost 3-4 bytes '
            + 'each, so this is often shorter than it looks.');
        }

        const ch = Number(trimmed(draft.channel));
        if (!Number.isInteger(ch) || ch < CHANNEL_MIN || ch > CHANNEL_MAX) {
          out.push('"' + trimmed(draft.channel) + '" is not a usable channel. This device '
            + 'configures hostapd for the 2.4GHz band only, so the channel is a whole number '
            + 'from ' + CHANNEL_MIN + ' to ' + CHANNEL_MAX + '.');
        }

        if (!draft.authMode) {
          out.push('Pick an authentication mode for the access point. The service defaults an '
            + 'unset value to WPA2, which with no key is a configuration hostapd will not '
            + 'start.');
        } else if (draft.authMode === 'WPA2_PSK') {
          const key = String(draft.apPsk || '');
          if (key.length < PSK_MIN || key.length > PSK_MAX) {
            out.push('A WPA2 key is ' + PSK_MIN + ' to ' + PSK_MAX + ' characters; this one is '
              + key.length + '. The device writes it as hostapd wpa_passphrase, which accepts '
              + 'nothing outside that range: hostapd exits on the length and the access point '
              + 'never comes up. (A 64-digit hex value is a raw key, but that is the wpa_psk '
              + 'directive, which this device never writes.) For an access point with no key at '
              + 'all, choose Open above -- and read what that says.');
          }
        }
      }

      if (usesSTA(m)) {
        const named = draft.clients.filter(c => utf8len(c.SSID) > 0);
        if (!named.length) {
          out.push('Station mode needs at least one network to join. runStaMode refuses an '
            + 'empty list with "WiFi mode set to station (STA) but no BSS configurations '
            + 'provided", and by the time it does that the access point has already been '
            + 'stopped.');
        }
        draft.clients.forEach((c, n) => {
          const label = 'Network ' + (n + 1) + ': ';
          const bytes = utf8len(c.SSID);
          if (!bytes) {
            out.push(label + 'no name. Remove the row or fill it in -- an empty SSID can never '
              + 'match anything in a scan.');
            return;
          }
          if (bytes > SSID_MAX_BYTES) {
            out.push(label + 'the name is ' + bytes + ' bytes; 802.11 allows '
              + SSID_MAX_BYTES + '.');
          }
          const key = String(c.PSK || '');
          if (key.length && (key.length < PSK_MIN || key.length > PSK_MAX)) {
            out.push(label + 'the key is ' + key.length + ' characters. It has to be '
              + PSK_MIN + ' to ' + PSK_MAX + ', or empty for a network with no key.');
          }
        });
        /* A Set, not an object used as a map. `seen[ssid]` walks the prototype
           chain, so a network listed ONCE and named "toString", "constructor",
           "valueOf" or "hasOwnProperty" read back truthy on the first look and
           was refused as a duplicate -- a deploy blocked for a reason that was
           not true. And `seen['__proto__'] = true` stores nothing at all (it
           sets the prototype), so a genuine pair of those was reported once per
           row instead of once. A Set has neither problem: an SSID is an
           arbitrary byte string and nothing about it should reach a prototype. */
        const seen = new Set();
        for (const c of named) {
          if (seen.has(c.SSID)) {
            out.push('"' + c.SSID + '" is listed twice. Both entries go into '
              + 'wpa_supplicant.conf and which one wins is not something this console can '
              + 'predict. Remove one.');
          }
          seen.add(c.SSID);
        }
      }

      return out;
    }

    /* --- the payload ------------------------------------------------------
     *
     * EVERY KEY BELOW IS SPELLED EXACTLY AS proto/grpc.proto SPELLS IT. The
     * bridge unmarshals with DiscardUnknown, so a misspelling is accepted,
     * silently dropped, and the service acts on the zero value instead. For
     * this message the zero values are: working mode UNKNOWN (which fails the
     * deploy after stopping the radio), auth mode WPA2_PSK, channel 0, and an
     * empty SSID -- so a typo in `ap_BSS` deploys an access point with no name
     * and no key and reports success.
     *
     * The shapes that do not look like the rest, because they are the ones a
     * reflex gets wrong: `working_mode`, `auth_mode`, `hide_ssid`, `ap_BSS`
     * (capitalised BSS), `client_BSS_list`, and inside a WiFiBSSCfg the fields
     * are `SSID` and `PSK` in full capitals.
     *
     * Enums go out as NAMES. protojson accepts either the name or the number
     * on input, and the name is what the service emits, so both directions
     * read the same. */
    function buildSettings() {
      return {
        name: trimmed(draft.name),
        disabled: !!draft.disabled,
        regulatory: trimmed(draft.regulatory).toUpperCase(),
        working_mode: draft.workingMode,
        auth_mode: draft.authMode || 'WPA2_PSK',
        channel: Number(trimmed(draft.channel)) || 0,
        /* ap_BSS and client_BSS_list are ALWAYS both sent, whatever the mode.
           DeploySettings replaces CurrentSettings wholesale, so omitting the
           half this mode does not use would throw it away -- and then the
           failover mode, which needs both, would have nothing to fail over to,
           and storing the form as a template would capture a configuration with a hole
           in it. */
        ap_BSS: {
          SSID: draft.apSsid,
          /* An Open access point ignores the key, and hostapd never writes it
             to its config. Sending it anyway would park a live secret in the
             device database for no reason, so it is dropped here -- and the
             field hint says so, because a key that disappears without a word
             is its own kind of surprise. */
          PSK: draft.authMode === 'OPEN' ? '' : String(draft.apPsk || ''),
        },
        client_BSS_list: draft.clients
          .filter(c => utf8len(c.SSID) > 0)
          .map(c => ({ SSID: c.SSID, PSK: String(c.PSK || '') })),
        hide_ssid: !!draft.hideSsid,
        nexmon: !!draft.nexmon,
      };
    }

    /* What this apply costs, in words, for the confirmation dialog. */
    function consequenceText(settings) {
      const m = draft.workingMode;
      const joining = settings.client_BSS_list.map(c => '"' + c.SSID + '"').join(' or ');
      const ch = Number(trimmed(draft.channel));
      const parts = [];

      /* Empty regulatory is allowed (the service substitutes US), so this is
         where the operator finds out that a default decided which channels the
         radio may legally use -- and, if they picked one US forbids, that the
         access point will not come up at all. */
      if (!trimmed(draft.regulatory) && !draft.disabled) {
        parts.push('No regulatory domain is set, so the service substitutes US and writes a '
          + 'line in the journal saying so: a default decides which channels this radio may '
          + 'legally use, not you.'
          + (usesAP(m) && Number.isInteger(ch) && ch >= 12
            ? ' US does not permit channel ' + ch + ', so hostapd will refuse to bring the '
              + 'access point up on it. Set a domain that allows it, or pick a lower channel.'
            : ''));
      }

      if (draft.disabled) {
        parts.push('The radio is switched off: neither hostapd nor wpa_supplicant is started, '
          + 'so the device spawns nothing and joins nothing. Everything currently on the access '
          + 'point is dropped and stays dropped.');
      } else {
        parts.push('hostapd and wpa_supplicant are both stopped first, so whatever is on the '
          + 'radio now is disconnected before anything new starts.');
        if (m === 'AP') {
          parts.push('An access point called "' + draft.apSsid + '" is then brought up on '
            + 'channel ' + trimmed(draft.channel)
            + (draft.authMode === 'OPEN'
              ? ' with NO key: anyone in range can join it and read everything on it.'
              : ' and clients have to present the key above.'));
        } else if (m === 'STA') {
          parts.push('The device then scans and tries to join ' + joining + '. If none of those '
            + 'is in the scan, or the key is wrong, the deploy FAILS -- and the access point is '
            + 'already gone by then, so the radio ends up carrying nothing at all.');
        } else if (m === 'STA_FAILOVER_AP') {
          parts.push('The device then scans and tries to join ' + joining + ', and if that does '
            + 'not work it brings up the access point "' + draft.apSsid + '" on channel '
            + trimmed(draft.channel) + ' instead.');
        }
      }

      if (usesAP(m) && !draft.disabled && draft.authMode !== 'OPEN'
          && isPublishedPSK(draft.apPsk)) {
        parts.push('THE KEY YOU TYPED WILL NOT BE USED: it is published in this project’s '
          + 'source, so the device substitutes a per-device random one (see '
          + 'service/wifi_psk_guard.go). If this console still has a connection afterwards it '
          + 'will show you the replacement; if it does not, the only way to read it is '
          + 'sudo cat ' + GENERATED_PSK_PATH + ' on the device itself.');
      }

      if (link) {
        parts.push('This page is riding on that access point, so this console WILL lose its '
          + 'connection'
          + (usesAP(m) && !draft.disabled
            ? ' and you will have to rejoin "' + draft.apSsid + '"'
              + (draft.authMode === 'OPEN' ? ', which will have no key, ' : ' with the new key ')
              + 'and come back to ' + link.addr + '.'
            : ' and the access point will not come back: reach the device over USB '
              + '(172.16.0.1) or the serial console instead.'));
      }

      parts.push('None of this is written to disk. Power-cycle the Pi and it returns to the '
        + 'stored startup configuration, so keep anything that has to survive a reboot with '
        + '"Store the form as a template" and name that template in a Loadout.');

      return parts.join(' ');
    }

    /* --- painting ---------------------------------------------------------
     *
     * paintEditor() rebuilds the whole card, so it is called only for changes
     * that alter WHICH fields exist -- the working mode, the authentication
     * mode, adding or removing a network. Anything typed updates its own slot
     * in place instead: repainting on each keystroke rebuilds the input, so
     * the operator gets exactly one character in before losing focus, and
     * doing it on `change` is no better because that fires on blur and snatches
     * focus from whatever field they tabbed to. */
    function paintEditor() {
      keyInputs = [];
      keyToggles = [];
      clear(editorHost);

      const m = draft.workingMode;
      const modeSpec = m ? WORKING_BY_KEY[m] : null;
      const offerable = !!(modeSpec && modeSpec.offerable);

      const modeSel = h('select', {
        'aria-label': 'Working mode',
        onchange: e => { draft.workingMode = e.target.value || null; markDirty(); paintEditor(); },
      },
        ...(offerable ? [] : [h('option', { value: '' }, '-- pick a mode --')]),
        ...WORKING_MODES.filter(x => x.offerable).map(x => h('option', { value: x.key }, x.label)));
      if (offerable) modeSel.value = m;

      const card = h('div.card',
        h('h2.card-title', 'Change the WiFi'),

        h('p.field-hint', { style: 'margin:0 0 18px' },
          'Nothing here reaches the device until you press Apply. This console cannot scan for '
          + 'networks -- there is no RPC for it -- so a name has to be typed exactly; the '
          + 'device does its own scan when it deploys and refuses a network it cannot see.'),

        field('Working mode', modeSel, offerable ? null
          : 'The device reported ' + (m ? '"' + m + '"' : 'nothing usable')
            + '. Nothing will be applied until you choose one.'),
        modeSpec ? h('p.field-hint', { style: 'margin:-10px 0 18px' }, modeSpec.what) : null,

        checkRow('Switch the radio off', draft.disabled,
          v => { draft.disabled = v; markDirty(); },
          'Stops hostapd and wpa_supplicant and starts neither. The wlan0 interface itself is '
          + 'left alone -- the network subsystem still manages its address -- but nothing can '
          + 'reach this device over the air. Everything else on this page is still stored, and '
          + 'ignored, while this is ticked.'),

        field('Regulatory domain',
          cellInput(draft.regulatory, 'GB', v => { draft.regulatory = v; markDirty(); },
            'Regulatory domain'),
          'Two letters, ISO/IEC 3166-1 alpha-2. It is what the kernel is told with "iw reg '
          + 'set", and it decides which channels this radio may legally use: 12 and 13 are not '
          + 'permitted in US, and 14 is Japan-only and 802.11b-only, which this device never '
          + 'configures. hostapd will not bring up an access point on a channel the domain in '
          + 'force forbids. An empty value is not fatal -- the service substitutes US and logs '
          + 'it -- and a code the kernel does not know fails at "iw reg set", which the service '
          + 'logs and then ignores, leaving whatever domain was already in force.'),

        field('Configuration name',
          cellInput(draft.name, 'default', v => { draft.name = v; markDirty(); },
            'Configuration name'),
          'A label. It is carried inside the settings, shown on this page, and written to the '
          + 'journal when they are deployed. It is NOT where a template is stored -- "Store as '
          + 'template" asks for that name separately.'));

      if (usesAP(m)) card.append(apSection());
      if (usesSTA(m)) card.append(staSection());

      card.append(
        h('h3.field-label', { style: 'margin-top:28px' }, 'Nexmon'),
        checkRow('Nexmon firmware features', draft.nexmon,
          v => { draft.nexmon = v; markDirty(); },
          'Stored and reported, and nothing else: DeploySettings carries a '
          + '"ToDo: Dis/Enable nexmon if needed" comment where the firmware swap would go '
          + '(service/wifi.go), so this build never loads or unloads the patched blob. The '
          + 'original client disabled this toggle for the same reason. Setting it changes what '
          + 'this page and your templates say about the device, and nothing about the radio.'),

        h('div.btn-row', { style: 'margin-top:24px' },
          h('button.btn.btn-primary', { type: 'button', onclick: e => apply(e.target) },
            'Apply to the device'),
          h('button.btn', {
            type: 'button',
            onclick: () => { State.formDirty = false; loadDraft(cs); paintEditor(); },
          }, 'Discard changes'),
          /* Re-read matters most in the moment straight after an apply, when
             the device may have taken only part of what it was sent -- or
             substituted the key. */
          h('button.btn.btn-quiet', {
            type: 'button',
            onclick: async () => {
              const fresh = await guard(() => Api.rpc('GetWiFiState'),
                'read the WiFi state back from the device');
              if (fresh === undefined) return;
              State.formDirty = false;
              State.wifi = fresh;
              toast('Re-read from the device.');
              Views.wifi();
            },
          }, 'Re-read from device'),
          /* StoreWifiSettings takes a settings payload rather than snapshotting
             what is live, so this stores THE FORM, unapplied edits included.
             The dialog says so, because "store" everywhere else in this console
             means "save what is running". */
          /* Labelled for what it stores, not "Store as template". The deployed
             card above carries a store button too, and that one snapshots what
             is RUNNING -- so two buttons reading "Store as template" on one
             page, both reporting the same success, is how an operator names a
             template after the configuration they just typed and gets the old
             one inside it. */
          h('button.btn.btn-quiet', { type: 'button', onclick: () => storeForm() },
            'Store the form as a template')));

      editorHost.append(card);
    }

    /* --- the access point half -------------------------------------------- */

    function apSection() {
      const authSel = h('select', {
        'aria-label': 'Authentication mode',
        onchange: e => { draft.authMode = e.target.value || null; markDirty(); paintEditor(); },
      },
        ...(draft.authMode ? [] : [h('option', { value: '' }, '-- pick one --')]),
        ...AUTH_MODES.map(a => h('option', { value: a.key }, a.label)));
      if (draft.authMode) authSel.value = draft.authMode;
      const authSpec = draft.authMode ? AUTH_BY_KEY[draft.authMode] : null;

      /* The SSID size report, refilled in place as the name is typed. */
      const sizeSlot = h('div');
      function paintSize() {
        const bytes = utf8len(draft.apSsid);
        const chars = charCount(draft.apSsid);
        const out = [h('p.field-hint', { style: 'margin:-10px 0 0' },
          bytes + ' bytes'
          + (bytes === chars ? '' : ' (' + chars + ' characters; some of them cost more than '
            + 'one byte each)')
          + ' of the ' + SSID_MAX_BYTES + ' bytes 802.11 allows.')];

        if (bytes > SSID_MAX_BYTES) {
          out.push(h('div.banner.banner-danger', { style: 'margin:10px 0 0' },
            h('p.banner-title', 'Too long for 802.11'),
            h('p', 'The service refuses an SSID over ' + SSID_MAX_BYTES + ' bytes, so this will '
              + 'not deploy at all.')));
        } else if (bytes > QR_SSID_BUDGET) {
          const uri = joinUriBytes(draft.apSsid,
            draft.authMode === 'OPEN' ? '' : String(draft.apPsk || ''));
          out.push(h('div.banner', { style: 'margin:10px 0 0' },
            h('p.banner-title', 'Over the OLED join-code budget: ' + bytes + ' bytes, '
              + QR_SSID_BUDGET + ' fit'),
            h('p', 'The panel’s first-boot credentials card builds a scan-to-join QR out of '
              + 'this name and the access point key. A panel-sized symbol holds ' + QR_CAPACITY
              + ' bytes; the WIFI: structure takes ' + QR_URI_OVERHEAD + ' and the '
              + '20-character generated key takes the rest, which leaves ' + QR_SSID_BUDGET
              + ' bytes for the name. Over budget the card drops the join code and prints the '
              + 'key on screen to be typed with a thumb instead.'),
            h('p', 'With the key currently in the form the join code would be ' + uri
              + ' bytes of ' + QR_CAPACITY + '. Nothing else breaks -- the access point works '
              + 'normally, you just lose the scan-to-join shortcut.')));
        }
        clear(sizeSlot).append(...out);
      }

      /* The PSK guard warning, same in-place treatment. */
      const pskSlot = h('div');
      function paintPskWarning() {
        clear(pskSlot);
        if (draft.authMode === 'OPEN') {
          if (String(draft.apPsk || '').length) {
            pskSlot.append(h('p.field-hint', { style: 'margin:-10px 0 0' },
              'Authentication is Open, so this key is not sent to the device at all and the '
              + 'network will have none.'));
          }
          return;
        }
        if (isPublishedPSK(draft.apPsk)) {
          pskSlot.append(h('div.banner.banner-danger', { style: 'margin:10px 0 0' },
            h('p.banner-title', 'The device will not broadcast on this key'),
            h('p', 'This key is published in this project’s source, so it is not a secret. '
              + 'service/wifi_psk_guard.go replaces it with a per-device random key at deploy '
              + 'time: the deploy still reports success, and the key you typed is simply not '
              + 'the one clients will need.'),
            h('p', 'If this console keeps its connection it will show you the replacement '
              + 'afterwards. If it does not -- which is exactly what happens when you are on '
              + 'this access point -- the only way to read it is sudo cat '
              + GENERATED_PSK_PATH + ' on the device. Pick your own key instead.')));
          return;
        }
        const n = String(draft.apPsk || '').length;
        if (n && (n < PSK_MIN || n > PSK_MAX)) {
          pskSlot.append(h('p.field-hint', { style: 'margin:-10px 0 0' },
            n + ' characters. A WPA2 key has to be ' + PSK_MIN + ' to ' + PSK_MAX + '.'));
        }
      }

      const chanSlot = h('div');
      function paintChannelNote() {
        clear(chanSlot);
        const ch = Number(trimmed(draft.channel));
        if (!Number.isInteger(ch) || ch < CHANNEL_MIN || ch > CHANNEL_MAX) return;
        if (ch === CHANNEL_MAX) {
          chanSlot.append(h('p.field-hint', { style: 'margin:-10px 0 0' },
            'Channel 14 exists only in Japan and only for 802.11b. This device always '
            + 'configures hostapd hw_mode=g, so an access point on it will not come up.'));
        } else if (ch >= 12) {
          chanSlot.append(h('p.field-hint', { style: 'margin:-10px 0 0' },
            'Channel ' + ch + ' is permitted in some regulatory domains and not others -- not '
            + 'in US. Where the domain in force forbids it, hostapd will not start.'));
        }
      }

      const pskInput = h('input', {
        type: showKeys ? 'text' : 'password',
        value: String(draft.apPsk || ''),
        placeholder: PSK_MIN + ' to ' + PSK_MAX + ' characters',
        autocomplete: 'new-password', 'aria-label': 'Access point key',
        oninput: e => {
          draft.apPsk = e.target.value;
          markDirty();
          paintPskWarning();
          paintSize();     // the join-code arithmetic includes the key
        },
      });
      keyInputs.push(pskInput);

      const section = h('div',
        h('h3.field-label', { style: 'margin-top:28px' },
          'The access point this device spawns'),

        field('Network name (SSID)',
          cellInput(draft.apSsid, 'p4wnp1', v => { draft.apSsid = v; markDirty(); paintSize(); },
            'Access point name'),
          null),
        sizeSlot,

        h('div', { style: 'margin-top:18px' },
          field('Authentication', authSel, null),
          authSpec ? h('p.field-hint', { style: 'margin:-10px 0 18px' }, authSpec.what) : null),

        draft.authMode === 'OPEN'
          ? h('div.banner.banner-danger', { style: 'margin:0 0 18px' },
            h('p.banner-title', 'This will be an open network'),
            h('p', 'No key and no encryption. Anyone in range joins it, and anyone listening '
              + 'reads everything that crosses it -- this console’s own traffic included, and '
              + 'whatever a payload on a target machine sends back over it.'))
          : null,

        field('Pre-shared key', pskInput,
          draft.authMode === 'OPEN'
            ? 'Not used while authentication is Open, and not sent. It stays in the form so you '
              + 'do not have to retype it if you switch back.'
            : 'Travels in cleartext between this browser and the device, is written into '
              + '/tmp/hostapd_wlan0.conf as wpa_passphrase, and is handed back in full by '
              + 'GetWiFiState to any signed-in session. Treat it as a secret of the device, not '
              + 'of a person.'),
        pskSlot,
        h('div.btn-row', { style: 'margin:10px 0 0' }, keysToggleButton()),

        h('div', { style: 'margin-top:18px' },
          field('Channel',
            cellInput(draft.channel, '6', v => {
              draft.channel = v; markDirty(); paintChannelNote();
            }, 'Channel'),
            'A 2.4GHz channel, ' + CHANNEL_MIN + ' to ' + CHANNEL_MAX + '. Ignored when the '
            + 'device joins a network instead of spawning one -- the channel then comes from '
            + 'whatever it joins.'),
          chanSlot),

        h('div', { style: 'margin-top:18px' },
          checkRow('Do not beacon the name', draft.hideSsid,
            v => { draft.hideSsid = v; markDirty(); },
            'hostapd gets ignore_broadcast_ssid=2, so the access point stops announcing its '
            + 'name in beacons. This does not hide the network: a client configured for it '
            + 'broadcasts the name itself in every probe request, so anyone watching the air '
            + 'learns it from your own devices. It does reliably break auto-join on some phones '
            + 'and laptops, and it makes the OLED join code the only convenient way on.')));

      paintSize();
      paintPskWarning();
      paintChannelNote();
      return section;
    }

    /* --- the station half -------------------------------------------------- */

    function staSection() {
      const repaint = () => { markDirty(); paintEditor(); };

      const rows = draft.clients.map((c, n) => {
        const sizeCell = h('td.field-hint');
        function paintCell() {
          const bytes = utf8len(c.SSID);
          clear(sizeCell).append(!bytes ? 'empty'
            : bytes > SSID_MAX_BYTES ? bytes + ' bytes -- too long'
              : bytes + ' bytes');
        }
        const keyInput = h('input', {
          type: showKeys ? 'text' : 'password',
          value: String(c.PSK || ''),
          placeholder: 'empty = no key',
          autocomplete: 'new-password',
          'aria-label': 'Key for network ' + (n + 1),
          oninput: e => { c.PSK = e.target.value; markDirty(); },
        });
        keyInputs.push(keyInput);
        paintCell();
        return h('tr',
          h('td', cellInput(c.SSID, 'network name', v => {
            c.SSID = v; markDirty(); paintCell();
          }, 'Name of network ' + (n + 1))),
          sizeCell,
          h('td', keyInput),
          h('td', h('button.btn.btn-sm.btn-danger', {
            type: 'button',
            onclick: () => { draft.clients.splice(n, 1); repaint(); },
          }, 'Remove')));
      });

      return h('div',
        h('h3.field-label', { style: 'margin-top:28px' }, 'Networks this device will join'),
        h('p.field-hint', { style: 'margin:0 0 12px' },
          'The device scans, keeps the entries whose name it can actually see, and hands those '
          + 'to wpa_supplicant. An entry with no key is treated as an open network -- and if '
          + 'the scan says that network is NOT open, the entry is dropped with a line in the '
          + 'journal and nothing on screen. If nothing matches at all, the whole deploy fails.'),
        draft.clients.length
          ? dataTable(
            h('thead', h('tr',
              h('th', 'Network name'), h('th', 'Size'), h('th', 'Key'), h('th', ''))),
            h('tbody', ...rows))
          : h('div.empty', 'No networks listed. In this mode that means the deploy is refused '
            + 'before it starts -- and the access point is stopped first either way. Add one.'),
        h('div.btn-row', { style: 'margin:12px 0 0' },
          h('button.btn.btn-sm', {
            type: 'button',
            onclick: () => { draft.clients.push({ SSID: '', PSK: '' }); repaint(); },
          }, 'Add a network'),
          keysToggleButton()),
        h('p.field-hint', { style: 'margin:12px 0 0' },
          'These are usually the operator’s own home and office networks. wpa_supplicant.conf '
          + 'is written 0600 on the device and the deploy log line carries only whether a key is '
          + 'set -- but GetWiFiState returns every one of them in full to any signed-in '
          + 'session, so a stored template is as sensitive as the keys inside it.'));
    }

    /* --- apply ------------------------------------------------------------- */

    async function apply(btn) {
      const bad = problems();
      if (bad.length) {
        await showDetail({
          title: 'Not applied',
          body: 'Nothing was sent to the device.\n\n'
            + bad.map((p, i) => (i + 1) + '. ' + p).join('\n\n'),
          mono: true,
        });
        return;
      }

      const settings = buildSettings();
      const yes = await confirmAction({
        title: draft.disabled ? 'Switch the radio off?' : 'Apply these WiFi settings?',
        body: modeLabel(draft.workingMode)
          /* Never a blank after "regulatory domain": empty is a real choice
             here, and it means something specific. */
          + ' · regulatory domain '
          + (trimmed(draft.regulatory).toUpperCase() || 'unset, so US is substituted')
          + (usesAP(draft.workingMode)
            ? ' · access point "' + draft.apSsid + '" on channel ' + trimmed(draft.channel)
              + (draft.authMode === 'OPEN' ? ', open' : ', WPA2')
            : '')
          + (usesSTA(draft.workingMode)
            ? ' · ' + settings.client_BSS_list.length + ' network(s) to join'
            : ''),
        consequence: consequenceText(settings),
        confirmLabel: draft.disabled ? 'Switch off' : 'Apply',
        danger: !!link || draft.disabled || draft.authMode === 'OPEN'
          || (usesAP(draft.workingMode) && isPublishedPSK(draft.apPsk)),
      });
      if (!yes) return;

      btn.disabled = true;
      const res = await guard(() => Api.rpc('DeployWiFiSettings', settings),
        'apply the WiFi settings');
      btn.disabled = false;
      if (res === undefined) return;
      State.formDirty = false;

      /* DeploySettings returns the state with CurrentSettings set to the
         message it actually used -- which the PSK guard may have MUTATED in
         place. So the replacement key is in this reply and nowhere else a
         browser can reach it. Showing it here is the difference between an
         operator who can rejoin their access point and one who has to go and
         find the device. */
      const sent = settings.ap_BSS.PSK;
      const back = (res && res.currentSettings && res.currentSettings.ap_BSS
        && typeof res.currentSettings.ap_BSS.PSK === 'string')
        ? res.currentSettings.ap_BSS.PSK : '';
      if (sent && back && back !== sent) {
        await showDetail({
          title: 'The device replaced the access point key',
          body: 'The key you set is published in this project’s source, so the device generated '
            + 'its own and is broadcasting on that instead. Write it down now -- this is the '
            + 'only place a browser is shown it:\n\n'
            + back
            + '\n\nIt is kept at ' + GENERATED_PSK_PATH + ' (mode 0600) and reused from then '
            + 'on, so it will not change on the next boot.',
          mono: true,
        });
      }

      toast('Applied. The table above is re-read from the device -- check it says what you '
        + 'expected, because a deploy that only half worked still reports success.');
      Views.wifi();
    }

    /* --- store the form as a template -------------------------------------- */

    async function storeForm() {
      const bad = problems();
      if (bad.length) {
        await showDetail({
          title: 'Not stored',
          body: 'A template that cannot be deployed is worse than none: it fails at deploy '
            + 'time, which for WiFi is after the access point has already been stopped.\n\n'
            + bad.map((p, i) => (i + 1) + '. ' + p).join('\n\n'),
          mono: true,
        });
        return;
      }
      const name = await promptValue({
        title: 'Store these WiFi settings',
        label: 'Template name',
        placeholder: 'ap-engagement',
        hint: 'Saves what is in the form above, INCLUDING changes you have not applied. An '
            + 'existing name is overwritten without a further warning. A stored template can be '
            + 'deployed again in one step and is what a Loadout names under WiFi -- which is '
            + 'the only way to make a configuration survive a reboot. The keys go into the '
            + 'device database as typed.',
        validate: v => {
          if (!v) return 'Give the template a name.';
          if (!/^[A-Za-z0-9._-]+$/.test(v)) {
            return 'Letters, digits, dot, dash and underscore only.';
          }
          return null;
        },
      });
      if (!name) return;
      const settings = buildSettings();
      const res = await guard(() => Api.rpc('StoreWifiSettings', {
        TemplateName: name,
        settings,
      }), 'store the template');
      if (res !== undefined) {
        /* Hand the form back to itself. Views.wifi() has to run to pick up the
           new name in the templates list, and it rebuilds the editor from the
           DEVICE -- so without this the configuration that was just stored
           disappeared off the screen the instant it was saved, and the operator
           had to Load the template they had only this moment made in order to
           carry on editing or to apply it. */
        pendingDraft = settings;
        toast('Stored as "' + name + '". The form still holds it; nothing has been applied to '
          + 'the device.');
        Views.wifi();
      }
    }

    /* A template chosen with "Load into form" wins over the live settings;
       otherwise the form starts from what the device reported. */
    loadDraft(loaded || cs);
    if (loaded) State.formDirty = true;
    paintEditor();

    main.append(renderTemplates(templates, link, false));
  };

  /* ------------------------------------------------------- the deployed card */

  function renderDeployedCard(live, cs) {
    const ap = (cs.ap_BSS && typeof cs.ap_BSS === 'object') ? cs.ap_BSS : {};
    const clients = Array.isArray(cs.client_BSS_list) ? cs.client_BSS_list : [];
    const mode = workingKey(cs.working_mode);
    const auth = authKey(cs.auth_mode);
    const hasAp = usesAP(mode);
    const hasSta = usesSTA(mode);
    const apSsidBytes = utf8len(ap.SSID);

    /* The key is behind a button rather than on screen. GetWiFiState hands it
       over in full, so this is not protection -- it is simply not putting a key
       in front of whoever is standing behind the operator until they ask. */
    const keyValue = h('span.mono', ap.PSK ? 'set' : 'not set');
    const keyRow = h('tr',
      h('th', { scope: 'row',
        style: 'text-transform:none;letter-spacing:0;font-size:13px;color:var(--text-muted)' },
        'Access point key'),
      h('td',
        keyValue,
        (typeof ap.PSK === 'string' && ap.PSK)
          ? h('div.btn-row', { style: 'margin-top:8px' },
            h('button.btn.btn-sm.btn-quiet', {
              type: 'button',
              onclick: e => { clear(keyValue).append(ap.PSK); e.target.remove(); },
            }, 'Show it'))
          : null,
        h('div.field-hint',
          isPublishedPSK(ap.PSK)
            ? 'This is a key published in this project’s source. If the access point is up it '
              + 'is NOT running on this key -- the guard substituted a per-device one, readable '
              + 'with sudo cat ' + GENERATED_PSK_PATH + '. If it is not up, this is what a '
              + 'deploy would try and the guard would catch again.'
            : auth === 'OPEN'
              ? 'Authentication is Open, so this value is ignored and the network has no key.'
              : 'What hostapd was last given. The API returns it in full to any signed-in '
                + 'session.')));

    return h('div.card',
      h('h2.card-title', 'Deployed now'),
      h('div.figures',
        figure(live.ssid || '--', 'ssid on the air'),
        figure(String(live.channel || '--'), 'channel'),
        figure(stateModeText(live.mode), 'radio state')),
      h('p.field-hint', { style: 'margin:18px 0 0' },
        'The three figures come from "iw dev wlan0 info" and are what the radio is actually '
        + 'doing. Everything in the table is what the service was last TOLD. They disagree when '
        + 'a deploy failed half way, and the radio is the one to believe. Note that "not '
        + 'connected" is also what the state reads when the radio is switched off -- the '
        + 'protocol has no separate value for that, so check the Radio off row.'),
      dataTable({ style: 'margin-top:18px' },
        h('tbody',
          kvRow('Working mode', modeLabel(mode),
            mode ? null
              : 'The service reported a working mode this console does not recognise. On a '
                + 'device that has deployed nothing yet this is UNKNOWN, which no deploy '
                + 'accepts.'),
          kvRow('Radio off', cs.disabled === true ? 'yes' : 'no',
            cs.disabled === true
              ? 'Neither hostapd nor wpa_supplicant is running, so nothing can reach this '
                + 'device over the air.'
              : null),
          kvRow('Regulatory domain', cs.regulatory || 'unset',
            cs.regulatory ? null
              : 'Nothing was set, so the service used US and said so in the journal. The domain '
                + 'decides which channels are legal, and hostapd will not start on one that is '
                + 'not.'),
          hasAp ? kvRow('Access point name', ap.SSID || 'unset',
            !ap.SSID ? 'hostapd will not start without one.'
              : apSsidBytes + ' bytes'
                + (apSsidBytes > QR_SSID_BUDGET
                  ? ' -- over the ' + QR_SSID_BUDGET + '-byte budget for the OLED panel’s '
                    + 'scan-to-join code, so that card prints the key to be typed instead.'
                  : '.')) : null,
          hasAp ? kvRow('Authentication', auth ? AUTH_BY_KEY[auth].label : 'unknown',
            auth === 'OPEN'
              ? 'There is no key and no encryption on this access point.' : null) : null,
          hasAp ? keyRow : null,
          hasAp ? kvRow('Name beaconed', cs.hide_ssid === true ? 'no' : 'yes',
            cs.hide_ssid === true
              ? 'Beacons carry no name. Clients that know the network still broadcast it in '
                + 'their probe requests, so this hides nothing from anyone listening.'
              : null) : null,
          hasAp ? kvRow('Channel (configured)', String(cs.channel || 'unset')) : null,
          hasSta ? kvRow('Networks to join',
            clients.length
              ? clients.map(c => (c && c.SSID) ? c.SSID : '(unnamed)').join(', ')
              : 'none',
            clients.length ? null
              : 'Station mode with an empty list is refused at deploy time.') : null,
          kvRow('Nexmon flag', cs.nexmon === true ? 'set' : 'not set',
            'Recorded only. This build never loads or unloads the patched firmware, whatever '
            + 'this says.'),
          kvRow('Configuration name', cs.name || '--',
            'A label carried inside the settings. Not the name of a stored template.'))),
      h('div.btn-row', { style: 'margin-top:20px' }, storeRunningButton()));
  }

  /* storeDeployedButton() hard-codes the label "Store as template", which is
     the right words on every other page in this console because they have only
     one store path. This page has two -- that one, and the editor's, which
     stores the FORM including edits that were never applied -- and they were
     both reading "Store as template" side by side, with the same
     'Stored as "x"' toast afterwards. An operator typing a new access point
     and then naming a template would have captured the OLD running one and had
     no way to tell. Relabelled here rather than in app.js, which the other
     views share. */
  function storeRunningButton() {
    const b = storeDeployedButton({
      rpc: 'StoreDeployedWifiSettings',
      title: 'Store the running WiFi configuration',
      placeholder: 'ap-engagement',
      hint: 'Captures what the service holds right now -- mode, name, key, channel, '
          + 'regulatory domain and the networks to join -- rather than what is in the form '
          + 'below. The service refuses this outright when it has no deployed settings at '
          + 'all.',
      onDone: () => Views.wifi(),
    });
    b.textContent = 'Store what is running';
    return b;
  }

  /* --------------------------------------------------------- stored templates */

  function renderTemplates(templates, link, subsystemDown) {
    const card = h('div.card', h('h2.card-title', 'Stored WiFi templates'));

    if (!templates.length) {
      card.append(h('div.empty',
        'No WiFi templates stored. Fill in the form above and press "Store the form as a '
        + 'template" to keep '
        + 'a configuration: a template can be deployed again in one step, and naming one in a '
        + 'Loadout under WiFi is the only way to make it come back after a reboot. Nothing '
        + 'applied on this page survives a power cycle on its own.'));
      return card;
    }

    card.append(h('p.field-hint', { style: 'margin:0 0 14px' },
      'A template carries a whole WiFi configuration: working mode, name, key, channel, '
      + 'regulatory domain and the networks to join. Deploying one applies all of it -- there '
      + 'is no partial deploy -- so read it before you press Deploy, and especially so if this '
      + 'page is riding on the access point.'
      + (subsystemDown
        ? ' The WiFi subsystem did not answer, so a deploy will be refused outright; reading '
          + 'and deleting still work.'
        : '')));

    card.append(dataTable(
      h('thead', h('tr', h('th', 'Template'), h('th', ''))),
      h('tbody', ...templates.map(name => h('tr',
        h('td.mono', name),
        h('td', h('div.btn-row',
          h('button.btn.btn-sm.btn-primary', {
            type: 'button', onclick: () => deployTemplate(name, link),
          }, 'Deploy'),
          ' ',
          /* Load, not deploy. The original client offered both, and the
             difference matters here more than anywhere else in the console:
             this is how a template is read and edited without being put on the
             air first -- which for WiFi is the difference between checking a
             configuration and losing the link while you check it. */
          h('button.btn.btn-sm', {
            type: 'button', onclick: () => loadTemplate(name),
          }, 'Load into form'),
          ' ',
          h('button.btn.btn-sm.btn-danger', {
            type: 'button', onclick: () => deleteTemplate(name),
          }, 'Delete'))))))));

    return card;
  }

  /* Read a template before deploying it. Without this the operator is choosing
     from a list of names with no way to see what any of them does to a radio
     they may be sitting on. */
  async function deployTemplate(name, link) {
    const t = await guard(() => Api.rpc('GetStoredWifiSettings', { msg: name }),
      'read the template "' + name + '"');
    if (t === undefined) return;

    const s = (t && typeof t === 'object') ? t : {};
    const ap = (s.ap_BSS && typeof s.ap_BSS === 'object') ? s.ap_BSS : {};
    const clients = Array.isArray(s.client_BSS_list) ? s.client_BSS_list : [];
    const mode = workingKey(s.working_mode);
    const auth = authKey(s.auth_mode);
    const usable = !!(mode && WORKING_BY_KEY[mode].offerable);

    const lines = [
      'Mode: ' + modeLabel(mode),
      'Radio off: ' + (s.disabled === true ? 'yes' : 'no'),
      'Regulatory domain: ' + (s.regulatory || 'unset'),
    ];
    if (usesAP(mode)) {
      lines.push('Access point: "' + (ap.SSID || 'unset') + '" on channel '
        + (s.channel || 'unset'));
      lines.push('Authentication: ' + (auth ? AUTH_BY_KEY[auth].label : 'unknown')
        + (auth === 'OPEN' ? '' : (ap.PSK ? ', key set' : ', NO KEY SET')));
    }
    if (usesSTA(mode)) {
      lines.push('Joins: ' + (clients.length
        ? clients.map(c => (c && c.SSID) ? c.SSID : '(unnamed)').join(', ')
        : 'nothing -- the network list in this template is empty'));
    }

    const parts = [];
    if (!usable) {
      parts.push('This template records a working mode the service does not accept, so the '
        + 'deploy fails with "Unknown working mode" -- but hostapd and wpa_supplicant are '
        + 'stopped BEFORE that check, so the radio is left carrying nothing.');
    } else if (s.disabled === true) {
      parts.push('The radio is switched off: everything on the access point is dropped and '
        + 'nothing is started in its place.');
    } else {
      parts.push('hostapd and wpa_supplicant are stopped first, so whatever is on the radio now '
        + 'is disconnected before anything new starts.');
      if (usesSTA(mode) && !clients.length) {
        parts.push('This template has no networks to join, so station mode is refused and the '
          + 'radio ends up with nothing at all.');
      }
      if (usesAP(mode) && auth === 'OPEN') {
        parts.push('The access point it brings up has NO key: anyone in range can join it and '
          + 'read everything on it.');
      }
      if (usesAP(mode) && auth !== 'OPEN' && isPublishedPSK(ap.PSK)) {
        parts.push('Its access point key is published in this project’s source, so the device '
          + 'will substitute a per-device random one instead -- readable only with sudo cat '
          + GENERATED_PSK_PATH + ' on the device.');
      }
    }
    if (link) {
      parts.push('This page is riding on the access point, so this console WILL lose its '
        + 'connection'
        + (usable && usesAP(mode) && s.disabled !== true
          ? ' and you will have to rejoin "' + (ap.SSID || '') + '" with this template’s key '
            + 'and come back to ' + link.addr + '.'
          : ' and the access point will not come back: reach the device over USB (172.16.0.1) '
            + 'or the serial console instead.'));
    }
    parts.push('Deploying a template does not make it the one that comes back after a reboot. '
      + 'That is a Loadout which names it under WiFi, set as the boot default.');

    const yes = await confirmAction({
      title: 'Deploy "' + name + '"?',
      body: lines.join(' · '),
      consequence: parts.join(' '),
      confirmLabel: 'Deploy',
      danger: true,
    });
    if (!yes) return;
    const r = await guard(() => Api.rpc('DeployStoredWifiSettings', { msg: name }),
      'deploy the template "' + name + '"');
    if (r !== undefined) { toast('Deployed "' + name + '".'); Views.wifi(); }
  }

  async function loadTemplate(name) {
    const t = await guard(() => Api.rpc('GetStoredWifiSettings', { msg: name }),
      'read the template "' + name + '"');
    if (t === undefined) return;
    pendingDraft = t;
    toast('Loaded "' + name + '" into the form. Nothing is applied until you press Apply.');
    Views.wifi();
  }

  async function deleteTemplate(name) {
    const yes = await confirmAction({
      title: 'Delete template "' + name + '"?',
      body: 'The stored configuration is removed from the device database.',
      consequence: 'Nothing on the radio changes -- what is deployed now stays deployed. Any '
        + 'Loadout that names this template under WiFi will fail to set up WiFi the next time '
        + 'it is deployed, and will say so only in the journal. If that Loadout is the boot '
        + 'default, the device comes up next time with whatever servicestart.sh gives it '
        + 'instead.',
      confirmLabel: 'Delete',
      danger: true,
    });
    if (!yes) return;
    const r = await guard(() => Api.rpc('DeleteStoredWifiSettings', { msg: name }),
      'delete the template "' + name + '"');
    if (r !== undefined) { toast('Deleted "' + name + '".'); Views.wifi(); }
  }

})();
