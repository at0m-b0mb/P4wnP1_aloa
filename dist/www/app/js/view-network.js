/* view-network.js -- the IPv4 side of every network interface.
 *
 * WHY THIS FILE EXISTS
 *
 * The original client had a whole Network settings page
 * (web_client/hvueComponentsNetwork.go, 760 lines, deleted in 8191890) and
 * this console had nothing for it. Eight RPCs had no caller anywhere:
 *
 *     GetAllDeployedEthernetInterfaceSettings   GetDeployedEthernetInterfaceSettings
 *     DeployEthernetInterfaceSettings           StoreEthernetInterfaceSettings
 *     ListStoredEthernetInterfaceSettings       GetStoredEthernetInterfaceSettings
 *     DeployStoredEthernetInterfaceSettings     DeleteStoredEthernetInterfaceSettings
 *
 * The Radio page showed a read-only table of interfaces and that was the whole
 * of it: the address the device hands itself, the address range it hands out,
 * the static reservations and the DHCP options it serves -- everything that
 * decides whether a target machine gets on the link at all -- could not be
 * seen, let alone changed, from any client. The CLI has no cmd_network.go
 * either, and the OLED panel has no networking screen. This was the largest
 * hole in the product.
 *
 * THE DANGER, WHICH IS ALSO THE POINT OF THE PAGE
 *
 * The operator is almost certainly reading this over usbeth (172.16.0.1) or
 * wlan0 (172.24.0.1). Changing the mode, address or netmask of that interface
 * -- or simply taking it down -- drops the connection the console arrived on,
 * and getting it wrong leaves the device unreachable. So the page works out
 * which interface the browser is talking to and says so, in the table, next to
 * the form, and in the confirmation, with the way back in spelled out.
 *
 * Everything is wrapped in an IIFE and only Views.network is exported. A
 * top-level `const` in a classic script lands in the shared global lexical
 * environment, so a name this file and app.js (or a sibling view file) both
 * declared would be a SyntaxError that stops the whole console from loading.
 */
'use strict';

(function () {

  /* --- the mode enum ------------------------------------------------------
   *
   * Order IS the proto enum order (EthernetInterfaceSettings.Mode in
   * proto/grpc.proto): MANUAL=0, DHCP_CLIENT=1, DHCP_SERVER=2, UNMANAGED=3.
   *
   * The JSON bridge marshals without UseEnumNumbers, so a real device sends
   * the NAME -- "DHCP_SERVER". The jsdom render harness and tools/mock-service
   * both send the NUMBER. modeKey() takes either rather than trusting one of
   * them, because reading a mode wrong here means offering to change an
   * interface while describing a different configuration than it has.
   *
   * `what` is what the mode DOES, read off service/SubSysNetworkManager.go's
   * DeploySettings rather than guessed from the name. */
  const MODES = [
    {
      key: 'MANUAL', label: 'Static address',
      what: 'The device sets this address on the interface, brings it up, and does nothing '
          + 'else. Nothing hands out addresses, so whatever is on the other end needs an '
          + 'address of its own already.',
    },
    {
      key: 'DHCP_CLIENT', label: 'DHCP client',
      what: 'The device asks the other end for an address. The address and netmask below '
          + 'are ignored entirely. This is the mode for plugging the Pi into somebody '
          + 'else’s network, or joining an access point you do not run.',
    },
    {
      key: 'DHCP_SERVER', label: 'DHCP server',
      what: 'The device takes the address below and hands out addresses to whatever '
          + 'attaches. This is how a target machine gets on the link with nobody touching '
          + 'it, and it is what the shipped usbeth, wlan0 and bteth configurations do.',
    },
    {
      key: 'UNMANAGED', label: 'Leave alone',
      what: 'The service stops managing the interface: any DHCP server or client it was '
          + 'running on it is stopped, and then nothing happens. The address already on '
          + 'the interface is left exactly as it is -- it is not removed, and the link is '
          + 'neither brought up nor taken down.',
    },
  ];
  const MODE_BY_KEY = {};
  for (const m of MODES) MODE_BY_KEY[m.key] = m;

  function modeKey(v) {
    if (typeof v === 'number') return MODES[v] ? MODES[v].key : null;
    const s = String(v === null || v === undefined ? '' : v).toUpperCase();
    if (MODE_BY_KEY[s]) return s;
    if (/^[0-3]$/.test(s)) return MODES[Number(s)].key;
    return null;
  }
  function modeLabel(v) {
    const k = modeKey(v);
    return k ? MODE_BY_KEY[k].label : 'unknown';
  }
  /* Only these two modes read the address out of the settings; see the switch
     in DeploySettings. For DHCP_CLIENT and UNMANAGED the fields are dead. */
  const usesAddress = k => k === 'MANUAL' || k === 'DHCP_SERVER';

  /* --- DHCP options ------------------------------------------------------
   *
   * The ones worth naming, from RFC 2132. The number is what goes on the
   * wire; the name is here so an operator does not have to remember that 3 is
   * the gateway and 6 is DNS. */
  const OPTION_NAMES = {
    1: 'subnet mask', 3: 'router (default gateway)', 6: 'DNS server',
    12: 'hostname', 15: 'domain name', 26: 'interface MTU',
    28: 'broadcast address', 42: 'NTP server', 44: 'NetBIOS name server',
    66: 'TFTP server name', 67: 'boot file name', 119: 'domain search list',
    121: 'classless static route', 252: 'WPAD proxy auto-config URL',
  };
  function optionName(n) {
    const num = Number(String(n).trim());
    if (!Number.isInteger(num)) return '';
    return OPTION_NAMES[num] || 'option ' + num;
  }

  /* --- addresses ---------------------------------------------------------
   *
   * These are validated HERE because the service does not validate them.
   * IpNetFromIPv4AndNetmask (SubSysNetworkManager.go) checks the netmask and
   * then checks the netmask AGAIN instead of the address, so a malformed
   * address falls straight through to netlink and the interface ends up with
   * no usable address at all, reported as a successful deploy. */
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
  const isIp4 = s => ip4(s) !== null;

  /* A netmask must be dotted decimal and a run of ones followed by a run of
     zeros. The service parses it with net.ParseIP, so "24" or "/24" is
     rejected outright with "Couldn't parse netmask" -- which is why the hint
     next to the field says dotted decimal rather than leaving it to be found
     out. */
  function isNetmask4(s) {
    const n = ip4(s);
    if (n === null || n === 0) return false;
    const inverted = (~n) >>> 0;
    return ((inverted + 1) & inverted) === 0;
  }
  const sameNetwork = (a, b, mask) => (((a ^ b) & mask) === 0);

  /* --- which interface is this console reached over? ---------------------
   *
   * An exact match on the interface's own address is certain: the browser is
   * talking to that address, so that interface is the link. A match on the
   * network is strong but not certain -- two managed interfaces could in
   * principle overlap -- so it is reported as "looks like". Anything else
   * (a hostname, a proxy, a link this console cannot see) is unknown, and the
   * page says unknown rather than guessing, because a wrong reassurance here
   * is how a device becomes unreachable. */
  function connectedInterface(list) {
    const host = String(location.hostname || '');
    for (const i of list) {
      if (i.ipAddress4 && String(i.ipAddress4) === host) return { name: i.name, certain: true };
    }
    const hostNum = ip4(host);
    if (hostNum === null) return null;
    for (const i of list) {
      const addr = ip4(i.ipAddress4);
      const mask = ip4(i.netmask4);
      if (addr === null || mask === null || mask === 0) continue;
      if (sameNetwork(hostNum, addr, mask)) return { name: i.name, certain: false };
    }
    return null;
  }

  /* How to get back in, per interface. Concrete, because "you may lose the
     connection" without a route back is not a warning, it is an apology. */
  function recoveryText(name) {
    const others = {
      usbeth: 'If you lose it: reconnect to whatever address you just set, or reach the '
            + 'device over WiFi instead (the shipped access point answers on 172.24.0.1).',
      wlan0: 'If you lose it: rejoin the access point at whatever address you just set, or '
           + 'reach the device over the USB cable instead (172.16.0.1 by default).',
      bteth: 'If you lose it: re-establish the Bluetooth network connection, or reach the '
           + 'device over USB (172.16.0.1) or WiFi (172.24.0.1).',
    };
    return 'The request itself may never answer -- the link dies while it is still in flight -- '
      + 'so this console will report a timeout even when the change was made. '
      + (others[name] || 'If you lose it, reach the device over one of its other '
        + 'interfaces -- USB is 172.16.0.1 and WiFi is 172.24.0.1 by default.')
      + ' Failing that: the Serial USB function gives a console over ttyGS0, and the last '
      + 'resort is pulling the SD card. Note that what you apply here is not written to '
      + 'disk -- power-cycling the Pi restores the service’s own defaults and then '
      + 'whatever the boot loadout names, so a reboot usually brings the old address back '
      + 'unless you stored this configuration as a template AND the boot loadout uses it.';
  }

  /* --- small builders, in the console's existing vocabulary -------------- */

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

  /* What to print in an "Address" column, honestly.
   *
   * ipAddress4 is what the service was TOLD to set, not what is on the
   * interface. In DHCP client mode the address comes from the other end and
   * this API never reports it; in unmanaged mode the service is not setting an
   * address at all. In both cases the stored value is a plausible address that
   * is very probably not the one in use, so say unknown instead of showing it. */
  function addressText(iface) {
    const key = modeKey(iface && iface.mode);
    if (key === 'DHCP_CLIENT') return 'unknown -- handed out by the other end';
    if (key === 'UNMANAGED') return 'unknown -- not managed by the service';
    if (!iface || !iface.ipAddress4) return 'none';
    return iface.ipAddress4 + ' / ' + (iface.netmask4 || '?');
  }
  const addressIsLiteral = iface => {
    const key = modeKey(iface && iface.mode);
    return (key === 'MANUAL' || key === 'DHCP_SERVER') && !!(iface && iface.ipAddress4);
  };

  /* Is this interface ACTUALLY handing out addresses right now?
   *
   * DHCP_SERVER mode on its own does not mean dnsmasq is running. DeploySettings
   * (SubSysNetworkManager.go) only reaches StartDHCPServer when the interface
   * exists AND `enabled` is set -- the `else` branch takes the link down and
   * never starts the server -- and it returns before recording the settings at
   * all when net.InterfaceByName fails, which is what settingsInUse=false
   * reports. A range-less server starts and offers nothing.
   *
   * Saying "hands out 172.24.0.2-172.24.0.20" for a wlan0 on a device with no
   * WiFi hardware is the kind of wrong this page exists to stop: the whole
   * point of the Deployed-now table is to be right about what a target machine
   * attaching to the link will get. */
  function servingState(iface) {
    const key = modeKey(iface && iface.mode);
    if (key !== 'DHCP_SERVER') return { serving: false, why: 'no' };
    const dhcp = iface.dhcpServerSettings;
    const ranges = (dhcp && Array.isArray(dhcp.ranges)) ? dhcp.ranges : [];
    if (!ranges.length) return { serving: false, why: 'nothing (no range set)' };
    if (!iface.settingsInUse) return { serving: false, why: 'nothing -- these settings are not applied' };
    if (!iface.enabled) return { serving: false, why: 'nothing -- the link is held down' };
    return {
      serving: true,
      why: ranges.map(r => trimmed(r.rangeLower) + '–' + trimmed(r.rangeUpper)).join(', '),
    };
  }

  /* Which interface the editor was on, remembered across a re-render.
   *
   * Applying, deploying, storing and deleting all finish by calling
   * Views.network() again, which rebuilds everything from a fresh read. Without
   * this the editor comes back on list[0] -- and list is sorted by name, so on
   * a full device that is bteth. Apply to usbeth and the page returns showing
   * "Configure bteth": the toast says to check the table, and the obvious next
   * button, "Re-read from device", would read the wrong interface. */
  let lastSelected = null;

  /* ---------------------------------------------------------------- the view */

  Views.network = async function () {
    const main = clear($('#view'));
    main.append(pageHead('Network',
      'The IPv4 side of each interface: the address this device holds on it, and whether it '
      + 'hands out addresses to whatever is on the other end.'));

    const [all, stored] = await Promise.all([
      guard(() => Api.rpc('GetAllDeployedEthernetInterfaceSettings'), 'read the deployed interfaces'),
      /* Not guarded: an empty template store is the normal state of a fresh
         device, and a toast about it on every visit is noise, not information. */
      Api.rpc('ListStoredEthernetInterfaceSettings').catch(() => null),
    ]);

    const list = (all && Array.isArray(all.list) ? all.list : [])
      .filter(i => i && typeof i === 'object')
      .slice()
      .sort((a, b) => String(a.name || '').localeCompare(String(b.name || '')));
    const templates = (stored && Array.isArray(stored.msgArray) ? stored.msgArray : [])
      .filter(n => typeof n === 'string' && n.length);
    const link = connectedInterface(list);

    /* --- the link warning, before anything else on the page -------------- */
    if (link) {
      main.append(h('div.banner.banner-danger',
        h('p.banner-title',
          link.certain
            ? 'You are connected over ' + link.name
            : 'You look to be connected over ' + link.name),
        h('p',
          link.certain
            ? 'This console is talking to ' + link.name + '’s own address ('
              + location.hostname + '). Changing that interface’s mode, address or '
              + 'netmask, or taking it down, will drop this page.'
            : 'This console’s address (' + location.hostname + ') is inside '
              + link.name + '’s network, so that is almost certainly the interface '
              + 'carrying this page. Changing it will drop the connection.')));
    } else if (list.length) {
      main.append(h('div.banner',
        h('p.banner-title', 'Which interface carries this page is unknown'),
        h('p', 'This console was reached as "' + location.hostname + '", which does not match '
          + 'any managed interface address or network -- a hostname, a proxy or a route this '
          + 'console cannot see. Treat every interface below as if it might be the one you are '
          + 'using.')));
    }

    /* --- what is deployed now -------------------------------------------- */

    /* `list` is the single source of truth for everything on this page, and
       both the summary table and the editor's own "deployed right now" block
       read from it. So the summary is painted into a host rather than built
       once: when "Re-read from device" refreshes one interface, both have to
       move together, or the page would show two different answers to the same
       question in two places. */
    const liveHost = h('div');
    main.append(liveHost);

    function paintLiveCard() {
      const serving = list.filter(i => servingState(i).serving).length;
      const applied = list.filter(i => i.settingsInUse).length;
      const liveCard = h('div.card', h('h2.card-title', 'Deployed now'));
      if (list.length) {
        liveCard.append(
          h('div.figures',
            figure(String(list.length), 'interfaces'),
            figure(String(serving), 'handing out addresses'),
            figure(String(applied), 'actually applied')),
          dataTable({ style: 'margin-top:24px' },
            h('thead', h('tr',
              h('th', 'Interface'), h('th', 'Mode'), h('th', 'Address'),
              h('th', 'Link'), h('th', 'Applied'), h('th', 'Hands out'), h('th', ''))),
            h('tbody', ...list.map(i => {
              const handsOut = servingState(i).why;
              return h('tr',
                h('td',
                  h('span.mono', i.name || '--'),
                  link && link.name === i.name
                    ? h('div', { style: 'margin-top:4px' },
                      h('span.pill', h('span.dot.dot-warn'), 'your link'))
                    : null),
                h('td', modeLabel(i.mode)),
                h('td', addressIsLiteral(i)
                  ? h('span.mono', addressText(i))
                  : addressText(i)),
                h('td', i.enabled ? 'up' : 'down'),
                h('td', i.settingsInUse ? 'yes' : 'no'),
                h('td', handsOut),
                h('td', h('button.btn.btn-sm', {
                  type: 'button',
                  onclick: () => selectInterface(i.name),
                }, 'Configure')));
            }))),
          h('p.field-hint', { style: 'margin-top:14px' },
            '"Applied" is the service’s own settingsInUse flag: no means it holds these '
            + 'settings but never managed to put them on the wire, which almost always means '
            + 'the interface does not exist on this device yet. usbeth appears only once a USB '
            + 'composition with RNDIS or CDC ECM is deployed; wlan0 needs WiFi hardware; bteth '
            + 'needs the Bluetooth NAP service. Deploying to an interface that is not there is '
            + 'reported as success and changes nothing.'));
      } else {
        liveCard.append(h('div.empty',
          'No managed interfaces were reported. The service manages usbeth, wlan0 and bteth and '
          + 'nothing else, so either it did not answer (check the Journal) or this build has no '
          + 'network subsystem. Nothing on this page can configure an interface the service does '
          + 'not manage.'));
      }
      clear(liveHost).append(liveCard);
    }

    /* --- the editor ------------------------------------------------------ */

    const editorHost = h('div');
    main.append(editorHost);

    /* A form with unsaved edits must not be rebuilt from under the operator.
       State.formDirty is what reloadViewOnStateChange() in app.js checks; the
       teardown clears it so a half-finished edit here cannot block the Cable
       or Radio pages from refreshing later. */
    State.formDirty = false;
    onViewTeardown(() => { State.formDirty = false; });
    const markDirty = () => { State.formDirty = true; };

    let selected = (lastSelected && list.some(i => i.name === lastSelected))
      ? lastSelected
      : (list.length ? list[0].name : null);
    lastSelected = selected;
    let draft = null;
    let synthesizedDhcp = false;

    /* Build the editable draft out of what the device reported. Defensive
       about types throughout: this is device-supplied JSON, and a view that
       throws renders nothing at all. */
    function loadDraft(name, source) {
      const live = source || list.find(i => i.name === name) || {};
      draft = {
        name: name,
        mode: modeKey(live.mode),          // may be null -- see the picker
        ipAddress4: typeof live.ipAddress4 === 'string' ? live.ipAddress4 : '',
        netmask4: typeof live.netmask4 === 'string' ? live.netmask4 : '',
        enabled: live.enabled !== false,
        dhcp: null,
      };

      const d = live.dhcpServerSettings;
      if (d && typeof d === 'object' && !Array.isArray(d)) {
        const rawOptions = (d.options && typeof d.options === 'object' && !Array.isArray(d.options))
          ? d.options : {};
        draft.dhcp = {
          listenPort: String(Number(d.listenPort) || 0),
          listenInterface: typeof d.listenInterface === 'string' ? d.listenInterface : name,
          leaseFile: typeof d.leaseFile === 'string' ? d.leaseFile : '',
          notAuthoritative: !!d.notAuthoritative,
          doNotBindInterface: !!d.doNotBindInterface,
          callbackScript: typeof d.callbackScript === 'string' ? d.callbackScript : '',
          ranges: (Array.isArray(d.ranges) ? d.ranges : []).map(r => ({
            rangeLower: trimmed(r && r.rangeLower),
            rangeUpper: trimmed(r && r.rangeUpper),
            leaseTime: trimmed(r && r.leaseTime),
          })),
          staticHosts: (Array.isArray(d.staticHosts) ? d.staticHosts : []).map(s => ({
            mac: trimmed(s && s.mac),
            ip: trimmed(s && s.ip),
          })),
          options: Object.keys(rawOptions)
            .map(k => ({ opt: String(k), val: trimmed(rawOptions[k]) }))
            .sort((a, b) => Number(a.opt) - Number(b.opt)),
        };
        synthesizedDhcp = false;
      } else {
        /* No DHCP configuration was reported for this interface, so there is
           nothing to show and something has to be in the form before DHCP
           server mode can be applied at all (DeploySettings refuses the mode
           outright with "doesn't provide DhcpServerSettings"). Start from what
           the project itself ships for a new interface -- the lease file name
           comes from common_web.NameLeaseFileDHCPSrv -- with options 3 and 6
           present but empty. A banner above the form says this happened, so
           these values are visible rather than smuggled in. */
        draft.dhcp = {
          listenPort: '0',
          listenInterface: name,
          leaseFile: '/tmp/dnsmasq_' + name + '.leases',
          notAuthoritative: false,
          doNotBindInterface: false,
          callbackScript: '',
          ranges: [],
          staticHosts: [],
          options: [{ opt: '3', val: '' }, { opt: '6', val: '' }],
        };
        synthesizedDhcp = true;
      }
    }

    /* Switching interfaces replaces `draft` wholesale, so unapplied edits are
       gone. Ask first, because the two ways in -- the picker and the Configure
       button in the table -- are both one tap, and on a phone the picker is a
       full-screen list that is easy to open by accident. Repainting on a
       refusal is what puts the <select> back: paintEditor() rebuilds it with
       picker.value = selected, and `draft` is untouched, so every character
       typed is still there. */
    /* The editor is below the table, which on a phone is off-screen. */
    function scrollToEditor() {
      if (editorHost.scrollIntoView) {
        try { editorHost.scrollIntoView({ block: 'start', behavior: 'smooth' }); } catch (_) {}
      }
    }

    async function selectInterface(name) {
      if (!name) return;
      /* Already here: "Configure" on the row being edited is a request to look
         at the editor, not to reload it. Keep the draft as it is. */
      if (name === selected) { scrollToEditor(); return; }
      if (State.formDirty) {
        const yes = await confirmAction({
          title: 'Discard the unapplied changes to ' + selected + '?',
          body: 'The form for ' + selected + ' has edits that have not been sent to the device.',
          consequence: 'Those edits are thrown away and the form is reloaded with what '
            + name + ' currently has. Nothing on the device changes either way -- '
            + selected + ' keeps the configuration it already had.',
          confirmLabel: 'Discard and switch to ' + name,
        });
        if (!yes) { paintEditor(); return; }
      }
      selected = name;
      lastSelected = name;
      loadDraft(name);
      State.formDirty = false;
      paintEditor();
      scrollToEditor();
    }

    /* --- validation -------------------------------------------------------
     *
     * Everything here refuses BEFORE the disconnect warning, for the same
     * reason the Cable page checks the USB endpoint budget first: letting
     * someone confirm "this will drop your connection" for a configuration
     * that cannot work is a cruel way to find out. */
    function problems() {
      const out = [];
      const d = draft.dhcp;

      if (!draft.mode) {
        out.push('Pick a mode. The device reported a mode this console does not recognise, '
          + 'so there is nothing safe to default to.');
        return out;
      }

      if (usesAddress(draft.mode)) {
        if (!isIp4(draft.ipAddress4)) {
          out.push('"' + trimmed(draft.ipAddress4) + '" is not an IPv4 address. It has to be '
            + 'four dotted decimal numbers, like 172.16.0.1. The service does not check this '
            + 'itself -- it would accept the value, report success, and leave the interface '
            + 'with no usable address.');
        }
        if (!isNetmask4(draft.netmask4)) {
          out.push('"' + trimmed(draft.netmask4) + '" is not a netmask. It has to be dotted '
            + 'decimal, like 255.255.255.0; the service rejects a /24 style prefix outright.');
        }
      }

      if (draft.mode !== 'DHCP_SERVER') return out;

      if (!trimmed(d.listenInterface)) {
        out.push('The DHCP server needs an interface to bind to. Leaving it empty writes '
          + '"interface=" into the dnsmasq config and dnsmasq will not start.');
      }
      const port = Number(trimmed(d.listenPort));
      if (!Number.isInteger(port) || port < 0 || port > 65535) {
        out.push('The listen port has to be a whole number from 0 to 65535. 0 means DHCP only.');
      }

      if (!d.ranges.length) {
        out.push('A DHCP server with no address range hands out nothing. dnsmasq starts, logs, '
          + 'and never offers an address, so whatever attaches sits there with no address at '
          + 'all. Add at least one range, or use Static address mode instead.');
      }

      const net = ip4(draft.ipAddress4);
      const mask = ip4(draft.netmask4);
      d.ranges.forEach((r, n) => {
        const label = 'Range ' + (n + 1) + ': ';
        const lo = ip4(r.rangeLower);
        const hi = ip4(r.rangeUpper);
        if (lo === null) out.push(label + '"' + r.rangeLower + '" is not an IPv4 address.');
        if (hi === null) out.push(label + '"' + r.rangeUpper + '" is not an IPv4 address.');
        if (lo !== null && hi !== null && lo > hi) {
          out.push(label + 'the lower address is above the upper one.');
        }
        if (r.leaseTime && !/^(\d+[smhd]?|infinite)$/i.test(r.leaseTime)) {
          out.push(label + '"' + r.leaseTime + '" is not a lease time. dnsmasq takes a number '
            + 'of seconds, or a number with s, m, h or d, or the word infinite.');
        }
        if (net !== null && mask !== null) {
          for (const [val, which] of [[lo, 'lower'], [hi, 'upper']]) {
            if (val !== null && !sameNetwork(val, net, mask)) {
              out.push(label + 'the ' + which + ' address is outside the network you have given '
                + draft.name + ' (' + trimmed(draft.ipAddress4) + ' / '
                + trimmed(draft.netmask4) + '). dnsmasq needs the range to be on the same '
                + 'network as the interface’s own address and refuses to start otherwise.');
            }
          }
        }
      });

      d.staticHosts.forEach((s, n) => {
        const label = 'Reservation ' + (n + 1) + ': ';
        if (!/^[0-9a-f]{2}(:[0-9a-f]{2}){5}$/i.test(s.mac)) {
          out.push(label + '"' + s.mac + '" is not a MAC address. Six hex pairs separated by '
            + 'colons, like 00:11:22:33:44:55.');
        }
        if (!isIp4(s.ip)) out.push(label + '"' + s.ip + '" is not an IPv4 address.');
      });

      const seen = {};
      d.options.forEach((o, n) => {
        const num = Number(trimmed(o.opt));
        if (!Number.isInteger(num) || num < 1 || num > 254) {
          out.push('Option ' + (n + 1) + ': "' + o.opt + '" is not a DHCP option number. '
            + 'They run from 1 to 254; RFC 2132 lists them.');
          return;
        }
        if (seen[num]) {
          out.push('Option ' + num + ' is listed twice. The service stores options in a map, '
            + 'so one of the two is dropped and which one survives is undefined. Remove one.');
        }
        seen[num] = true;
      });

      return out;
    }

    /* --- the payload ------------------------------------------------------
     *
     * Every key below is spelled exactly as proto/grpc.proto spells it. The
     * bridge unmarshals with DiscardUnknown, so a misspelling is accepted,
     * silently dropped, and the service then acts on a zero value: a typo in
     * `netmask4` would deploy an interface with no netmask and report success.
     * Note the shapes that do not look like the rest: `ipAddress4`,
     * `dhcpServerSettings`, `staticHosts`, `notAuthoritative`.
     *
     * The original client had exactly this bug here -- its DHCP settings
     * carried `nonAuthoritative`, which is not a field of DHCPServerSettings,
     * so the authoritative toggle it offered could never have had any effect. */
    function buildSettings() {
      const d = draft.dhcp;
      const optionMap = {};
      for (const o of d.options) optionMap[String(Number(trimmed(o.opt)))] = o.val;

      return {
        name: draft.name,
        mode: draft.mode,
        ipAddress4: trimmed(draft.ipAddress4),
        netmask4: trimmed(draft.netmask4),
        enabled: !!draft.enabled,
        /* Always sent, whatever the mode. The service only reads it in the
           DHCP_SERVER branch, but it replaces CurrentSettings wholesale, so
           omitting it would throw the ranges and reservations away the moment
           someone parked an interface in DHCP client mode. */
        dhcpServerSettings: {
          listenPort: Number(trimmed(d.listenPort)) || 0,
          listenInterface: trimmed(d.listenInterface),
          leaseFile: trimmed(d.leaseFile),
          notAuthoritative: !!d.notAuthoritative,
          doNotBindInterface: !!d.doNotBindInterface,
          callbackScript: trimmed(d.callbackScript),
          ranges: d.ranges.map(r => ({
            rangeLower: trimmed(r.rangeLower),
            rangeUpper: trimmed(r.rangeUpper),
            leaseTime: trimmed(r.leaseTime),
          })),
          options: optionMap,
          staticHosts: d.staticHosts.map(s => ({ mac: trimmed(s.mac), ip: trimmed(s.ip) })),
        },
      };
    }

    /* What this apply will cost, in words, for the confirmation dialog. */
    function consequenceText() {
      const isLink = link && link.name === draft.name;
      const parts = [];
      if (draft.mode === 'MANUAL' || draft.mode === 'DHCP_SERVER') {
        parts.push('Every address currently on ' + draft.name + ' is flushed and replaced with '
          + trimmed(draft.ipAddress4) + ' / ' + trimmed(draft.netmask4) + '.');
      }
      if (draft.mode === 'DHCP_CLIENT') {
        parts.push('Every address currently on ' + draft.name + ' is flushed and the device asks '
          + 'the other end for a new one, which it may not get.');
      }
      if (draft.mode === 'UNMANAGED') {
        parts.push('Any DHCP server or client running on ' + draft.name + ' is stopped. The '
          + 'address already on the interface is left alone.');
      }
      if (!draft.enabled && draft.mode !== 'UNMANAGED') {
        parts.push('The interface is then taken DOWN, so nothing can reach it at all.');
      }
      if (isLink) {
        parts.push('This is the interface carrying this page, so this console WILL lose its '
          + 'connection. ' + recoveryText(draft.name));
      } else {
        parts.push('Anything already connected over ' + draft.name + ' loses its address and '
          + 'has to ask for a new one.');
      }
      return parts.join(' ');
    }

    /* --- painting --------------------------------------------------------- */

    function paintEditor() {
      clear(editorHost);
      if (!list.length) return;
      if (!draft) loadDraft(selected);

      const live = list.find(i => i.name === selected) || {};
      const isLink = link && link.name === selected;
      const modeSpec = draft.mode ? MODE_BY_KEY[draft.mode] : null;

      const picker = h('select', {
        'aria-label': 'Interface to configure',
        onchange: e => selectInterface(e.target.value),
      }, ...list.map(i => h('option', { value: i.name }, i.name)));
      picker.value = selected;                  // only takes once the options exist

      const modeSel = h('select', {
        'aria-label': 'Mode',
        onchange: e => { draft.mode = e.target.value || null; markDirty(); paintEditor(); },
      },
        ...(draft.mode ? [] : [h('option', { value: '' }, '-- pick a mode --')]),
        ...MODES.map(m => h('option', { value: m.key }, m.label)));
      if (draft.mode) modeSel.value = draft.mode;

      const card = h('div.card',
        h('div.card-head',
          h('h2.card-title', { style: 'margin:0' }, 'Configure ' + selected),
          picker),

        isLink
          ? h('div.banner.banner-danger', { style: 'margin-bottom:20px' },
            h('p.banner-title', 'This is the connection you are using'),
            h('p', 'Applying anything below drops this page. ' + recoveryText(selected)))
          : null,

        /* What is deployed, before offering to change it. */
        h('p.field-hint', { style: 'margin:0 0 6px' }, 'Deployed on ' + selected + ' right now:'),
        dataTable(h('tbody',
          kvRow('Mode', modeLabel(live.mode)),
          kvRow('Address', addressText(live),
            usesAddress(modeKey(live.mode)) ? null
              : 'This mode does not set an address, and the service does not report the one '
                + 'the interface actually has. ' + (live.ipAddress4
                  ? 'The value stored against it is ' + live.ipAddress4 + ', which is what '
                    + 'would be used if you switched to static or DHCP server mode.'
                  : 'Nothing is stored against it either.')),
          kvRow('Link', live.enabled ? 'up' : 'down'),
          kvRow('Applied', live.settingsInUse ? 'yes' : 'no',
            live.settingsInUse ? null
              : 'The service holds these settings but never put them on the wire -- most '
                + 'likely the interface does not exist on this device yet.'),
          kvRow('DHCP ranges', (live.dhcpServerSettings
            && Array.isArray(live.dhcpServerSettings.ranges)
            && live.dhcpServerSettings.ranges.length)
            ? live.dhcpServerSettings.ranges
              .map(r => trimmed(r.rangeLower) + '–' + trimmed(r.rangeUpper)
                + (r.leaseTime ? ' for ' + r.leaseTime : ''))
              .join(', ')
            : 'none'))),

        h('div', { style: 'margin-top:24px' },
          field('Mode', modeSel, modeSpec ? null
            : 'The device reported a mode this console does not know. Nothing will be applied '
              + 'until you choose one.'),
          modeSpec ? h('p.field-hint', { style: 'margin:-10px 0 18px' }, modeSpec.what) : null,

          /* No repaint: nothing rendered depends on `enabled`, and rebuilding
             the card would take focus off the checkbox the operator just used. */
          checkRow('Interface up', draft.enabled, v => { draft.enabled = v; markDirty(); },
            draft.mode === 'UNMANAGED'
              ? 'Ignored in this mode: the service does not touch the link state of an '
                + 'unmanaged interface.'
              : 'Untick this and the device sets the address and then takes the link down, so '
                + 'nothing can reach it. In DHCP server mode the server is not started either.'),

          usesAddress(draft.mode)
            ? h('div.grid-2',
              field('IPv4 address',
                cellInput(draft.ipAddress4, '172.16.0.1',
                  v => { draft.ipAddress4 = v; markDirty(); }, 'IPv4 address'),
                'The address this device holds on ' + selected + '. Dotted decimal.'),
              field('Netmask',
                cellInput(draft.netmask4, '255.255.255.0',
                  v => { draft.netmask4 = v; markDirty(); }, 'Netmask'),
                'Dotted decimal, not a /24 style prefix. The shipped usbeth uses '
                  + '255.255.255.252, which leaves room for exactly one host.'))
            : h('p.field-hint', { style: 'margin:0 0 8px' },
              'This mode ignores the address and netmask, so they are not shown. They are kept '
              + 'as they are and come back if you switch to a mode that uses them.'),
        ),

        h('div.btn-row', { style: 'margin-top:8px' },
          h('button.btn.btn-primary', {
            type: 'button',
            onclick: async (e) => {
              const bad = problems();
              if (bad.length) {
                await showDetail({
                  title: 'This would not work',
                  body: bad.map((p, i) => (i + 1) + '. ' + p).join('\n\n'),
                });
                return;
              }
              const yes = await confirmAction({
                title: 'Apply this to ' + draft.name + '?',
                body: 'The device reconfigures ' + draft.name + ' as ' +
                  MODE_BY_KEY[draft.mode].label.toLowerCase() + '.',
                consequence: consequenceText(),
                confirmLabel: 'Apply to ' + draft.name,
                danger: !!isLink,
              });
              if (!yes) return;

              const s = buildSettings();
              e.target.disabled = true;
              /* Spelled out key by key rather than passed through as `s` so
                 tools/check-rpc-shapes.py can read the field names out of the
                 source and fail the build on a typo, instead of letting the
                 device act on a silently dropped field. It is the same object. */
              const res = await guard(() => Api.rpc('DeployEthernetInterfaceSettings', {
                name: s.name,
                mode: s.mode,
                ipAddress4: s.ipAddress4,
                netmask4: s.netmask4,
                enabled: s.enabled,
                dhcpServerSettings: s.dhcpServerSettings,
              }), 'apply the settings for ' + draft.name);
              e.target.disabled = false;
              if (res === undefined) return;
              State.formDirty = false;
              toast('Applied to ' + draft.name + '. Check the table: if nothing changed, the '
                + 'interface is not present on this device.');
              Views.network();
            },
          }, 'Apply to ' + selected),

          h('button.btn', {
            type: 'button',
            onclick: () => {
              State.formDirty = false;
              loadDraft(selected);
              paintEditor();
            },
          }, 'Discard changes'),

          /* Re-read this one interface rather than the whole page. The point
             of it is the moment straight after an apply, when the device may
             have taken only part of what it was sent -- or none of it, if the
             interface is not actually present.
             The reply replaces the entry in `list`, so the summary table and
             the "deployed right now" block are repainted from it too. Showing
             a refreshed form beside a stale table would put two different
             answers to the same question on one screen. */
          h('button.btn.btn-quiet', {
            type: 'button',
            onclick: async () => {
              const fresh = await guard(
                () => Api.rpc('GetDeployedEthernetInterfaceSettings', { msg: selected }),
                'read ' + selected + ' back from the device');
              if (fresh === undefined) return;
              const at = list.findIndex(i => i.name === selected);
              /* The reply is EthernetInterfaceSettings and may carry a `name`
                 of its own; keep the one we asked about, since that is the row
                 this is replacing. */
              if (at !== -1) list[at] = Object.assign({}, fresh, { name: selected });
              State.formDirty = false;
              loadDraft(selected);
              paintLiveCard();
              paintEditor();
              toast('Re-read ' + selected + ' from the device.');
            },
          }, 'Re-read from device'),

          /* StoreEthernetInterfaceSettings is the one Store* RPC that takes a
             settings payload rather than snapshotting what is live, so this
             stores the FORM -- including edits that have not been applied. The
             dialog says so, because "store" everywhere else in this console
             means "save what is running". */
          h('button.btn.btn-quiet', {
            type: 'button',
            onclick: async () => {
              const bad = problems();
              if (bad.length) {
                await showDetail({
                  title: 'Not stored',
                  body: 'A template that cannot be deployed is worse than none -- it fails at '
                    + 'deploy time, which on this device is mid-reconfiguration over a link '
                    + 'the template is itself changing.\n\n'
                    + bad.map((p, i) => (i + 1) + '. ' + p).join('\n\n'),
                });
                return;
              }
              const name = await promptValue({
                title: 'Store these ' + selected + ' settings',
                label: 'Template name',
                placeholder: 'ap-dhcp',
                hint: 'Stored as "' + selected + '_<name>": the service puts the interface name '
                    + 'in front of yours, so a template always says which interface it is for. '
                    + 'This saves what is in the form above, including changes you have not '
                    + 'applied. An existing name is overwritten.',
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
              const res = await guard(
                () => Api.rpc('StoreEthernetInterfaceSettings', {
                  TemplateName: name,
                  settings: settings,
                }),
                'store the template');
              if (res !== undefined) {
                toast('Stored as "' + selected + '_' + name + '".');
                Views.network();
              }
            },
          }, 'Store as template')));

      editorHost.append(card);
      if (draft.mode === 'DHCP_SERVER') editorHost.append(paintDhcpCard());
    }

    /* --- the DHCP server card -------------------------------------------- */

    function paintDhcpCard() {
      const d = draft.dhcp;
      const repaint = () => { markDirty(); paintEditor(); };

      /* The "bound to the wrong interface" warning lives in its own slot and
         is refilled in place as the field is typed into. Repainting the card
         on each keystroke -- which is what the first version did -- rebuilds
         the input, so the operator could type exactly one character before
         losing focus. Doing it on `change` instead is no better: that fires on
         blur, so it would snatch focus from whatever field they tabbed to. */
      const bindWarning = h('div');
      function paintBindWarning() {
        clear(bindWarning);
        const bound = trimmed(d.listenInterface);
        if (!bound || bound === draft.name) return;
        bindWarning.append(h('div.banner.banner-danger', { style: 'margin-bottom:20px' },
          h('p.banner-title', 'The DHCP server is bound to ' + bound + ', not ' + draft.name),
          h('p', 'dnsmasq is told to serve ' + bound + ', so nothing attaching to '
            + draft.name + ' will get an address. The service does not correct this for you. '
            + 'The field is under "How the server runs" below.')));
      }
      paintBindWarning();

      const rangeRows = d.ranges.map((r, n) => h('tr',
        h('td', cellInput(r.rangeLower, '172.16.0.2', v => { r.rangeLower = v; markDirty(); },
          'Range ' + (n + 1) + ' lowest address')),
        h('td', cellInput(r.rangeUpper, '172.16.0.2', v => { r.rangeUpper = v; markDirty(); },
          'Range ' + (n + 1) + ' highest address')),
        h('td', cellInput(r.leaseTime, '5m', v => { r.leaseTime = v; markDirty(); },
          'Range ' + (n + 1) + ' lease time')),
        h('td', h('button.btn.btn-sm.btn-danger', {
          type: 'button',
          onclick: () => { d.ranges.splice(n, 1); repaint(); },
        }, 'Remove'))));

      const hostRows = d.staticHosts.map((s, n) => h('tr',
        h('td', cellInput(s.mac, '00:11:22:33:44:55', v => { s.mac = v; markDirty(); },
          'Reservation ' + (n + 1) + ' MAC address')),
        h('td', cellInput(s.ip, '172.16.0.2', v => { s.ip = v; markDirty(); },
          'Reservation ' + (n + 1) + ' address')),
        h('td', h('button.btn.btn-sm.btn-danger', {
          type: 'button',
          onclick: () => { d.staticHosts.splice(n, 1); repaint(); },
        }, 'Remove'))));

      const optionRows = d.options.map((o, n) => {
        /* The name cell is updated in place rather than by repainting the
           card: a repaint on every keystroke -- or on blur, which is when
           `change` fires -- would steal focus from the value field the
           operator is tabbing into. */
        const nameCell = h('td.field-hint', optionName(o.opt));
        return h('tr',
          h('td', cellInput(o.opt, '3', v => {
            o.opt = v; markDirty(); clear(nameCell).append(optionName(v));
          }, 'Option ' + (n + 1) + ' number')),
          nameCell,
          h('td', cellInput(o.val, 'empty = do not send this option',
            v => { o.val = v; markDirty(); }, 'Option ' + (n + 1) + ' value')),
          h('td', h('button.btn.btn-sm.btn-danger', {
            type: 'button',
            onclick: () => { d.options.splice(n, 1); repaint(); },
          }, 'Remove')));
      });

      return h('div.card',
        h('h2.card-title', 'What ' + draft.name + ' hands out'),

        synthesizedDhcp
          ? h('div.banner', { style: 'margin-bottom:20px' },
            h('p.banner-title', 'The device reported no DHCP configuration for ' + draft.name),
            h('p', 'DHCP server mode cannot be applied without one -- the service refuses it '
              + 'outright. The form below starts from what this project ships for a new '
              + 'interface: no address range yet, and options 3 and 6 present but empty. '
              + 'Add at least one range before applying.'))
          : null,

        bindWarning,

        /* ranges */
        h('h3.field-label', 'Address ranges'),
        h('p.field-hint', { style: 'margin:0 0 12px' },
          'The addresses handed out, lowest to highest. A single address in both columns '
          + 'hands out exactly that one, which is what the shipped usbeth does. Lease time is '
          + 'a number of seconds or a number with s, m, h or d, or the word infinite; leave it '
          + 'empty and dnsmasq uses its own default of one hour. Its minimum is two minutes.'),
        d.ranges.length
          ? dataTable(
            h('thead', h('tr',
              h('th', 'Lowest'), h('th', 'Highest'), h('th', 'Lease time'), h('th', ''))),
            h('tbody', ...rangeRows))
          : h('div.empty', 'No ranges. In DHCP server mode that means nothing gets an address: '
            + 'dnsmasq starts and never offers one. Add a range below.'),
        h('div.btn-row', { style: 'margin:12px 0 28px' },
          h('button.btn.btn-sm', {
            type: 'button',
            onclick: () => {
              d.ranges.push({ rangeLower: '', rangeUpper: '', leaseTime: '5m' });
              repaint();
            },
          }, 'Add a range')),

        /* static hosts */
        h('h3.field-label', 'Fixed addresses'),
        h('p.field-hint', { style: 'margin:0 0 12px' },
          'A machine with this MAC address always gets this address. Useful when a payload on '
          + 'the target has to know where to reach this device, or the other way round.'),
        d.staticHosts.length
          ? dataTable(
            h('thead', h('tr', h('th', 'MAC address'), h('th', 'Address'), h('th', ''))),
            h('tbody', ...hostRows))
          : h('div.empty', 'None. Addresses come out of the ranges above, in whatever order '
            + 'dnsmasq picks.'),
        h('div.btn-row', { style: 'margin:12px 0 28px' },
          h('button.btn.btn-sm', {
            type: 'button',
            onclick: () => { d.staticHosts.push({ mac: '', ip: '' }); repaint(); },
          }, 'Add a fixed address')),

        /* options */
        h('h3.field-label', 'Options served with each lease'),
        h('p.field-hint', { style: 'margin:0 0 12px' },
          'An option with a VALUE is sent to the client. An option with an EMPTY value is '
          + 'suppressed -- dnsmasq is told to send it not at all. That is deliberate in the '
          + 'shipped usbeth and wlan0 configurations: 3 (router) and 6 (DNS) are listed empty '
          + 'so the target does not install this device as its default gateway or name server, '
          + 'which would put its traffic through the Pi. bteth is the exception and sets both '
          + 'to the device’s own address, because Android will not use a Bluetooth network '
          + 'that offers no gateway. Options 1, 12 and 28 are sent by dnsmasq regardless unless '
          + 'you suppress them here.'),
        d.options.length
          ? dataTable(
            h('thead', h('tr',
              h('th', 'Number'), h('th', 'What it is'), h('th', 'Value'), h('th', ''))),
            h('tbody', ...optionRows))
          : h('div.empty', 'None listed, so dnsmasq sends whatever it would send by default -- '
            + 'including itself as router and DNS server.'),
        h('div.btn-row', { style: 'margin:12px 0 28px' },
          h('button.btn.btn-sm', {
            type: 'button',
            onclick: () => { d.options.push({ opt: '', val: '' }); repaint(); },
          }, 'Add an option')),

        /* the rest */
        h('h3.field-label', 'How the server runs'),
        h('div.grid-2', { style: 'margin-top:12px' },
          h('div',
            checkRow('Authoritative', !d.notAuthoritative,
              v => { d.notAuthoritative = !v; markDirty(); },
              'An authoritative server tells a client its old address is wrong and replaces it '
              + 'immediately instead of waiting for the lease to expire. Turn it off only if '
              + 'another DHCP server is meant to own this link.'),
            checkRow('Bind only to ' + draft.name, !d.doNotBindInterface,
              v => { d.doNotBindInterface = !v; markDirty(); },
              'On, the config gets "bind-interfaces" and dnsmasq binds this interface alone. '
              + 'Off, it binds 0.0.0.0 instead, so it does not need restarting when the '
              + 'interface goes up and down -- but it then holds the DHCP port (and the DNS '
              + 'port, if the port below is not 0) on every address this device has, so '
              + 'nothing else can bind them. The config still names one interface to serve, '
              + 'so requests arriving anywhere else are received and discarded rather than '
              + 'answered.'),
            field('Listen port',
              cellInput(d.listenPort, '0', v => { d.listenPort = v; markDirty(); }, 'Listen port'),
              '0 means DHCP only. Any other port also starts a DNS server on it; 53 makes this '
              + 'device the name server for anything that accepts option 6 above.')),
          h('div',
            field('Interface to serve',
              cellInput(d.listenInterface, draft.name,
                v => { d.listenInterface = v; markDirty(); paintBindWarning(); },
                'Interface to serve'),
              'Should be ' + draft.name + '. The service writes this straight into the dnsmasq '
              + 'config without checking it against the interface being configured.'),
            field('Lease file',
              cellInput(d.leaseFile, '/tmp/dnsmasq_' + draft.name + '.leases',
                v => { d.leaseFile = v; markDirty(); }, 'Lease file'),
              'Where dnsmasq records what it has handed out. Empty falls back to '
              + '/tmp/dnsmasq_' + trimmed(d.listenInterface || draft.name) + '.leases.'),
            field('Script to run on each lease',
              cellInput(d.callbackScript, '',
                v => { d.callbackScript = v; markDirty(); }, 'Script to run on each lease'),
              'Run as root on this device every time a lease is handed out or dropped, as '
              + '"script add|del MAC IP HOSTNAME". Leave it empty unless you mean it.'))));
    }

    paintLiveCard();
    if (list.length) { loadDraft(selected); paintEditor(); }

    /* --- stored templates ------------------------------------------------ */

    const tplCard = h('div.card', h('h2.card-title', 'Stored interface templates'));
    if (templates.length) {
      tplCard.append(
        h('p.field-hint', { style: 'margin:0 0 14px' },
          'A template carries one interface’s whole configuration, the DHCP server '
          + 'included. Deploying one applies it to the interface recorded INSIDE it, which is '
          + 'the name in front of the underscore. Templates are also what a Loadout names '
          + 'under "Network".'),
        dataTable(
          h('thead', h('tr', h('th', 'Template'), h('th', ''))),
          h('tbody', ...templates.map(name => h('tr',
            h('td.mono', name),
            h('td', h('div.btn-row',
              /* Reading the template before deploying it: the operator would
                 otherwise be choosing from a list of names with no way to see
                 what any of them does to a link they may be sitting on. */
              h('button.btn.btn-sm.btn-primary', {
                type: 'button',
                onclick: async () => {
                  const t = await guard(
                    () => Api.rpc('GetStoredEthernetInterfaceSettings', { msg: name }),
                    'read the template "' + name + '"');
                  if (t === undefined) return;
                  const iface = String((t && t.name) || '');
                  const key = modeKey(t && t.mode);
                  const addr = (t && t.ipAddress4)
                    ? t.ipAddress4 + ' / ' + (t.netmask4 || '?') : 'none';
                  const ranges = (t && t.dhcpServerSettings
                    && Array.isArray(t.dhcpServerSettings.ranges))
                    ? t.dhcpServerSettings.ranges : [];
                  const isLink = link && iface && link.name === iface;
                  const lines = [
                    'Interface: ' + (iface || 'not recorded'),
                    'Mode: ' + modeLabel(t && t.mode),
                    'Address: ' + addr,
                    'Link: ' + (t && t.enabled ? 'up' : 'down'),
                  ];
                  if (key === 'DHCP_SERVER') {
                    lines.push('Hands out: ' + (ranges.length
                      ? ranges.map(r => trimmed(r.rangeLower) + '-' + trimmed(r.rangeUpper)).join(', ')
                      : 'nothing -- no range in this template'));
                  }
                  const yes = await confirmAction({
                    title: 'Deploy "' + name + '"?',
                    body: lines.join(' · '),
                    /* The managed-interface check matters as much here as it
                       does on "Load into form": without it this dialog would
                       promise that something connected over the interface
                       loses its address, when in fact the service refuses the
                       whole request and nothing happens at all. */
                    consequence: !iface
                      ? 'This template records no interface name, so the service will look up '
                        + 'an interface called "" and refuse it. Nothing changes.'
                      : !list.some(i => i.name === iface)
                        ? 'The service does not manage an interface called "' + iface
                          + '" on this device, so it will refuse this outright. Nothing '
                          + 'changes, on this interface or any other.'
                        : 'This is applied to ' + iface + ', not to whatever is selected above. '
                          + (isLink
                            ? 'That is the interface carrying this page, so this console WILL '
                              + 'lose its connection. ' + recoveryText(iface)
                            : 'Anything connected over ' + iface
                              + ' loses its address and has to ask for a new one.'),
                    confirmLabel: 'Deploy',
                    danger: !!isLink,
                  });
                  if (!yes) return;
                  const r = await guard(
                    () => Api.rpc('DeployStoredEthernetInterfaceSettings', { msg: name }),
                    'deploy the template "' + name + '"');
                  if (r !== undefined) { toast('Deployed "' + name + '".'); Views.network(); }
                },
              }, 'Deploy'),
              ' ',
              /* Load, not deploy: the original client offered both and the
                 difference matters here more than anywhere else in the
                 console -- this is how you read a template, and edit it,
                 without putting it on the wire first. */
              h('button.btn.btn-sm', {
                type: 'button',
                onclick: async () => {
                  const t = await guard(
                    () => Api.rpc('GetStoredEthernetInterfaceSettings', { msg: name }),
                    'read the template "' + name + '"');
                  if (t === undefined) return;
                  const iface = String((t && t.name) || '');
                  if (!list.some(i => i.name === iface)) {
                    await showDetail({
                      title: 'Cannot load this one',
                      body: 'The template is for an interface called "' + iface + '", which the '
                        + 'service does not manage on this device. It would be refused at '
                        + 'deploy time too.',
                    });
                    return;
                  }
                  selected = iface;
                  lastSelected = iface;
                  loadDraft(iface, t);
                  State.formDirty = true;
                  paintEditor();
                  toast('Loaded "' + name + '" into the form for ' + iface
                    + '. Nothing is applied until you press Apply.');
                },
              }, 'Load into form'),
              ' ',
              h('button.btn.btn-sm.btn-danger', {
                type: 'button',
                onclick: async () => {
                  const yes = await confirmAction({
                    title: 'Delete template "' + name + '"?',
                    body: 'The stored configuration is removed from the device.',
                    consequence: 'Nothing running changes. Any Loadout that names this template '
                      + 'under "Network" will fail to apply its networking when it is next '
                      + 'deployed, and say so only in the journal.',
                    confirmLabel: 'Delete',
                    danger: true,
                  });
                  if (!yes) return;
                  const r = await guard(
                    () => Api.rpc('DeleteStoredEthernetInterfaceSettings', { msg: name }),
                    'delete the template "' + name + '"');
                  if (r !== undefined) { toast('Deleted "' + name + '".'); Views.network(); }
                },
              }, 'Delete'))))))));
    } else {
      tplCard.append(h('div.empty',
        'No interface templates stored. Configure an interface above and press "Store as '
        + 'template" to keep it: a stored template can be deployed again in one step, and is '
        + 'what a Loadout names under "Network" to set up the whole device at once.'));
    }
    main.append(tplCard);
  };

})();
