/* palette.js -- one keystroke to anywhere in the console.
 *
 * WHY THIS FILE EXISTS
 *
 * The console was eight pages. It is a dozen and more now, and they carry a few
 * hundred controls between them. Finding one means already knowing which page
 * it lives on, and nothing on screen tells you: the DHCP range is on Network,
 * the CD-ROM flag is on Storage, the pairing PIN is on Bluetooth, the status
 * LED is on System, and the only way to learn that is to open every one. On a
 * phone it is worse -- the rail is a horizontally scrolling strip and the last
 * several entries start off the right edge. Pages registered in Views but not
 * yet listed in NAV have no rail button at all; see pageOrder().
 *
 * So: a text box that searches the console. It matches page names AND the words
 * an operator actually has in their head -- "dhcp", "ssid", "ums", "led",
 * "reboot", "nap", "lease" -- and takes you to the page that holds that
 * control. The keyword lists were not invented: every word in them was grepped
 * out of the page that the command opens, because a search box that lands you
 * on the wrong page is worse than no search box.
 *
 * WHAT IT DELIBERATELY DOES NOT DO
 *
 * It does not run anything that changes the device. Jumping to a page is safe
 * and reversible; "reboot" from a box where Enter fires the highlighted row is
 * not, so "reboot" is a SEARCH WORD that takes you to System, not a command.
 * The only actions registered are the three chrome controls that live in the
 * rail and are otherwise unreachable by keyboard from the middle of a page --
 * the theme, the session list, the password -- plus Sign out, which confirms
 * first for exactly the mis-fire reason above.
 *
 * WHAT IT MAY NOT DO TO THE REST OF THE CONSOLE
 *
 * It is loaded on every page, so it holds one document-level keydown listener
 * for the life of the tab. That listener is the whole risk in this file: eat a
 * key the HIDScript editor needed and a payload loses a line. See onGlobalKey.
 *
 * NO NEW CSS. The overlay is .modal-backdrop and .modal -- the same dialog
 * chrome as every other dialog in the console: a centring grid that scrolls
 * itself over a 460px-max card, identical at every width, which is what every
 * other dialog here gets (there is no phone-specific .modal rule in app.css,
 * so do not describe this as a bottom sheet) -- and the rows are
 * .nav-item, which already has a hover state and a highlighted-row look. The
 * handful of declarations that no existing class provides (a bounded scrolling
 * list; the highlight on the active row) are set inline from token names, the
 * way kvRow() in app.js already sets its own, so there is nothing new for
 * tools/check_contrast.py to gate and nothing to wire beyond this one script.
 *
 * Everything is inside an IIFE. A top-level `const` in a classic script lands
 * in the shared global lexical environment, so a name this file and app.js both
 * declared would be a SyntaxError that stops the whole console from loading.
 * The single export is window.CommandPalette.
 */
'use strict';

(function () {

  /* Loaded twice -- two <script> tags, or a stale cache-busted copy alongside a
     fresh one -- would mean two keydown listeners and two buttons. */
  if (window.CommandPalette) return;

  /* ui.js is loaded before this file and everything here is built with h().
     If it is missing, the console is already broken; do not add to the wreck. */
  if (typeof h !== 'function') return;

  const ID = 'p4-cmd';
  const DIALOG_LABEL = 'Find a page or control';

  /* --- what is actually on each page ---------------------------------------
   *
   * `hint` is the page's own one-line description, shortened -- taken from the
   * pageHead() call in the file that renders it, so the palette cannot promise
   * something the page does not do.
   *
   * `words` is what someone searching would type. Each term either appears in
   * the view that this command opens (grep over the view files) or is a plain
   * synonym for something that does -- "preset" for a loadout, "volume" for the
   * emulated disk. What a word may NEVER be is the name of a control that does
   * not exist: "scan" was in this list for the Radio page and had to come out,
   * because nothing in the console scans for networks (view-wifi.js says so in
   * as many words), so the only thing that keyword taught you was that a scan
   * button must be hiding somewhere. A keyword that lands you on the wrong
   * page costs more than a missing one, and a keyword for a capability the
   * device does not have costs more again.
   *
   * `label` is only needed for a page that is registered in Views but that
   * NAV has not caught up with -- see pageOrder(). A page NAV knows about
   * takes its name from NAV. */
  const PAGES = {
    overview: {
      hint: 'What the device is presenting, and what it is doing right now.',
      words: 'home start status summary dashboard attached first run primer',
    },
    cable: {
      hint: 'The USB side: what the device claims to be, and which functions a host sees.',
      words: 'usb gadget gadget settings vid pid serial manufacturer product hid keyboard '
           + 'mouse raw rndis cdc ecm ums mass storage endpoints composite',
    },
    storage: {
      hint: 'The disk an attached host sees, and how to swap it without pulling the cable.',
      words: 'ums image iso cdrom disk drive eject insert file volume flash removable',
    },
    /* Registered by view-wifi.js. The old 'radio' page it replaced is gone --
       every card Radio held is now on a page that can change the thing it
       shows -- so its keywords moved here, including the ones only it had:
       'password' and 'radio' itself, so an operator who still thinks of this
       page by its old name finds it. */
    wifi: {
      label: 'WiFi',
      hint: 'The access point this device spawns, or the network it joins. The only place '
          + 'either can be changed from a browser.',
      words: 'wifi wlan ssid psk password passphrase key channel regulatory country '
           + 'nexmon access point ap station sta client join connect hostapd wpa radio '
           + 'qr code template apply change edit set',
    },
    bluetooth: {
      hint: 'What the controller is called, who may find and pair with it, and whether it hands out network access.',
      /* 'version' belongs here and nowhere else: the controller's HCI version
         and core-spec name are the only version numbers the console shows
         anywhere (view-bluetooth.js renders both). It used to be filed under
         System, which shows no version at all. */
      words: 'bt bluetooth pair pairing pin agent bondable discoverable nap pan network '
           + 'access point controller name alias version hci core spec',
    },
    network: {
      hint: 'The IPv4 address each interface holds, and whether it hands out addresses.',
      words: 'ip ipv4 address netmask subnet dhcp server client lease range reservation dns '
           + 'interface usb0 wlan0 bteth eth0 static route gateway',
    },
    keystrokes: {
      hint: 'HIDScript that runs on the device and drives the emulated keyboard and mouse.',
      words: 'hidscript script type typing payload job run editor keyboard mouse layout '
           + 'keymap stored scripts',
    },
    reflexes: {
      hint: 'Rules the device acts on by itself: when something happens, run something.',
      words: 'trigger action rule automation arm boot service started usb attached ssh '
           + 'login dhcp lease group send receive one shot',
    },
    loadouts: {
      hint: 'A whole-device configuration -- USB, WiFi, Bluetooth, network and reflexes together.',
      words: 'master template profile preset loadout boot startup default deploy snapshot',
    },
    /* Registered by view-templates.js. See the note on wifi above. */
    templates: {
      label: 'Templates',
      hint: 'Every configuration saved on this device, and what each saved name actually '
          + 'contains before you deploy it.',
      words: 'template templates stored saved store inspect contents deploy delete usb '
           + 'wifi bluetooth network trigger master loadout name list',
    },
    system: {
      hint: 'The status LED, the database that holds every stored template, and the buttons that take the device off the air.',
      /* 'uptime' and 'version' were here and are gone. Nothing in the console,
         the service or the proto reports an uptime, and the only version shown
         anywhere is the Bluetooth controller's. Both sent you to a page that
         does not have what you asked for -- see the note on 'scan' above.
         'health' stays: the "Is the service answering?" card is exactly that,
         under a different name. */
      words: 'led light blink reboot restart shutdown power off database db backup restore '
           + 'service health answering round trip',
    },
    panel: {
      hint: 'The screen on the device, mirrored, and its eight controls.',
      words: 'oled display screen hat buttons mirror keys press',
    },
    journal: {
      hint: 'Everything the device reports, live.',
      words: 'log logs journal events stream errors warnings debug messages output',
    },
  };

  /* --- inline declarations -------------------------------------------------
   *
   * Only what no existing class provides. Colours are token names, never
   * literals, so the house palette still owns every colour in both themes.
   *
   * ACTIVE mirrors .nav-item[aria-current="page"] in app.css. It is NOT that
   * selector: aria-current="page" means "this is the page you are on", and
   * putting it on every highlighted row of a search result would have a screen
   * reader announce a dozen current pages. The row says aria-selected instead,
   * which is what role="option" wants, and the look is applied here. */
  const LIST_STYLE = 'max-height:min(52vh,460px);overflow-y:auto;overscroll-behavior:contain;'
    + 'display:flex;flex-direction:column;gap:2px;margin:0 0 var(--s3)';
  const ITEM_STYLE = 'width:100%;text-align:left;white-space:normal';
  /* .nav-item .count already styles the tag; it just must not wrap. Without
     this the longer tags broke over three lines of two or three characters and
     made their row twice the height of every other one. */
  const TAG_STYLE = 'white-space:nowrap;flex:none';
  const ACTIVE = {
    background: 'var(--surface-sunk)',
    color: 'var(--text)',
    'font-weight': '600',
    'box-shadow': 'inset 2px 0 0 var(--brass)',
  };

  /* The open palette, or null. */
  let cur = null;
  /* The visible button. Built once and re-parented, because renderConsole()
     rebuilds the whole rail and would otherwise leave stale copies behind. */
  let affordance = null;

  /* ------------------------------------------------------------- the commands
   *
   * Built at open time, not at load time: the theme row's label depends on
   * which theme is showing, the "current" tag depends on which page you are
   * on, and NAV is read live so a page added to app.js later appears here
   * without this file being touched. */

  /* Every page you can actually reach, as [view, label].
   *
   * NAV first, in NAV order, because that is the order of the rail and the
   * order a row keeps between searches. Then any view that is REGISTERED in
   * Views but missing from NAV.
   *
   * That second half is not defensive padding. NAV is a const in app.js and
   * the views are assigned from six different files, so the two drift: at the
   * time of writing view-wifi.js registers Views.wifi and view-templates.js
   * registers Views.templates, and NAV lists neither -- so the rail has no
   * button for either page and, reading NAV alone, this palette had none
   * either. Two whole pages reachable only by typing their hash by hand, in
   * the one place whose entire job is "one keystroke to anywhere in the
   * console". Reading Views as well costs four lines and means a page is
   * findable the moment it exists.
   *
   * go() accepts them: it checks Views[view], not NAV. The rail simply has no
   * item to light up, so nothing in it is marked current while such a page is
   * open -- honest, and better than the page being unreachable. */
  function pageOrder() {
    const out = [];
    const seen = Object.create(null);
    const nav = (typeof NAV !== 'undefined' && Array.isArray(NAV)) ? NAV : [];
    for (const entry of nav) {
      if (!Array.isArray(entry) || !entry[0]) continue;
      const view = String(entry[0]);
      if (seen[view]) continue;
      seen[view] = true;
      out.push([view, String(entry[1] || entry[0])]);
    }
    const views = (typeof Views !== 'undefined' && Views) ? Views : null;
    if (views) {
      for (const view of Object.keys(views)) {
        if (seen[view] || typeof views[view] !== 'function') continue;
        seen[view] = true;
        /* PAGES carries the proper spelling ('WiFi', not 'Wifi'). A view with
           no PAGES entry falls back to its own key capitalised, which is at
           least the hash you would type. */
        const info = PAGES[view] || {};
        out.push([view, info.label || (view.charAt(0).toUpperCase() + view.slice(1))]);
      }
    }
    return out;
  }

  function pageCommands() {
    const here = (typeof State !== 'undefined' && State) ? State.view : null;
    return pageOrder().map(([view, label]) => {
      if (!view || !label) return null;
      const info = PAGES[view] || {};
      const current = view === here;
      return {
        id: 'page:' + view,
        title: label,
        group: current ? 'current' : 'page',
        hint: current
          ? 'You are on this page. Opening it again re-reads it from the device.'
          : (info.hint || 'Open the ' + label + ' page.'),
        words: view + ' ' + (info.words || ''),
        run: () => {
          /* go() is app.js's router: it tears down the old view's timers,
             clears stale toasts and repaints. Setting the hash alone would
             work through the hashchange listener, but not when the target is
             the page already showing -- and that case is the useful one
             ("re-read this"). */
          if (typeof go === 'function') go(view);
          else try { location.hash = '#' + view; } catch (_) { /* no router */ }
        },
      };
    }).filter(Boolean);
  }

  function actionCommands() {
    const out = [];

    /* The theme. The rail's switch is three buttons at the bottom of the rail;
       from the middle of a long page it is a scroll away on a phone. */
    if (typeof Theme !== 'undefined' && Theme && typeof Theme.set === 'function') {
      /* The RESOLVED theme, which is what is on screen -- Theme.read() may say
         'auto', and "Switch to the dark theme" must not appear while the dark
         theme is already showing. app.js writes the resolved value here. */
      const dark = document.documentElement.getAttribute('data-theme') === 'dark';
      out.push({
        id: 'theme:flip',
        title: dark ? 'Switch to the light theme' : 'Switch to the dark theme',
        group: 'appearance',
        hint: 'Remembered in this browser, not on the device. Another operator on another '
            + 'phone keeps their own.',
        words: 'theme appearance dark light night contrast colour color paper black',
        run: () => Theme.set(dark ? 'light' : 'dark'),
      });
      let choice = 'auto';
      try { choice = Theme.read(); } catch (_) { choice = 'auto'; }
      if (choice !== 'auto') {
        out.push({
          id: 'theme:auto',
          title: 'Follow the system theme',
          group: 'appearance',
          hint: 'Light or dark according to this phone or laptop, and it keeps following it.',
          words: 'theme auto automatic system os appearance dark light',
          run: () => Theme.set('auto'),
        });
      }
    }

    if (typeof sessionsFlow === 'function') {
      out.push({
        id: 'account:sessions',
        title: 'Who is signed in',
        group: 'account',
        hint: 'Every session on this device, including the credential the device uses to run '
            + 'its own startup and trigger scripts. Opens a list; changes nothing by itself.',
        words: 'sessions who signed in logged in token revoke end kick someone else',
        run: () => sessionsFlow(),
      });
    }

    if (typeof changePasswordFlow === 'function') {
      out.push({
        id: 'account:password',
        title: 'Change the console password',
        group: 'account',
        hint: 'Opens the form. Saving it ends every signed-in session, including this one.',
        words: 'password passphrase credentials change rotate login admin',
        run: () => changePasswordFlow(),
      });
    }

    /* Sign out is last on purpose: ties in the ranking break towards the
       earlier command, so a half-typed "s" lands on Storage, not here. */
    out.push({
      id: 'account:signout',
      title: 'Sign out',
      group: 'account',
      hint: 'Ends this browser session only.',
      words: 'sign out signout log out logout leave lock exit session',
      run: async () => {
        /* The rail's Sign out button fires on a deliberate click at a known
           place on screen. This one fires on Enter in a search box, which is
           where a mis-fire happens, so it asks. */
        const ok = (typeof confirmAction === 'function') ? await confirmAction({
          title: 'Sign out of the console',
          body: 'You will need the console password to get back in. It is in '
              + '/root/INITIAL_CREDENTIALS.txt on the device unless it has been changed.',
          consequence: 'Only this browser session ends. The device carries on exactly as it is: '
              + 'the USB gadget stays attached, running scripts keep running, and armed reflexes '
              + 'keep firing. Nothing on the device is changed or stopped.',
          confirmLabel: 'Sign out',
        }) : true;
        if (!ok) return;
        try {
          if (typeof State !== 'undefined' && State && State.stopStream) State.stopStream();
        } catch (_) { /* the event stream is already gone */ }
        try {
          if (typeof Api !== 'undefined' && Api && Api.logout) await Api.logout();
        } catch (_) { /* the token is dropped locally either way */ }
        if (typeof renderSignIn === 'function') renderSignIn();
      },
    });

    return out;
  }

  function buildCommands() {
    const all = pageCommands().concat(actionCommands());
    /* Precomputed lowercase haystacks: the filter runs on every keystroke.
       The keywords and the prose are kept APART on purpose -- see rank(). */
    for (const c of all) {
      c._title = String(c.title).toLowerCase();
      c._words = (c.title + ' ' + c.group + ' ' + (c.words || '')).toLowerCase();
      c._tokens = c._words.split(/[^a-z0-9]+/).filter(Boolean);
      c._prose = String(c.hint || '').toLowerCase();
    }
    return all;
  }

  /* --------------------------------------------------------------- matching
   *
   * Substring first, subsequence as a fallback, no library. There is no
   * bundler here and the device is routinely operated with no internet route
   * at all, so a dependency is not an option -- and for a list this size (the
   * pages, plus four or five chrome actions) a ranked substring match is not a
   * compromise, it is the right algorithm.
   *
   * Ranking, best first:
   *   0  the title, exactly
   *   1  the title starts with what you typed
   *   2  the title contains it
   *   3  every word you typed is somewhere in the title
   *   4  every word you typed IS one of the page's keywords
   *   5  every word you typed STARTS one of them ("reboo" -> "reboot")
   *   6  every word you typed is somewhere inside the keywords
   *   7  every word you typed is somewhere in the page's description
   *   8  the letters you typed appear in order in the title ("netwrk" -> Network)
   * Ties break towards the earlier command, which is NAV order: the page list
   * never reshuffles, so the position of a row you use often stays put.
   *
   * THE TIERS BELOW 4 EXIST BECAUSE SUBSTRINGS LIE, and both cases were real:
   *
   *   "led"  put you on Bluetooth, whose description says the controller is
   *          "called" something -- which contains "led" -- while the status LED
   *          is on System, later in NAV.
   *   "log"  put you on Reflexes, which has "ssh login" among its keywords,
   *          rather than Journal, which is the log.
   *
   * Hence: a word that IS a keyword beats a word that merely starts one, which
   * beats a word buried inside one, which beats a word in the prose. */

  function isSubsequence(needle, hay) {
    let i = 0;
    for (let j = 0; j < hay.length && i < needle.length; j++) {
      if (hay[j] === needle[i]) i++;
    }
    return i === needle.length;
  }

  function rank(cmd, q, terms, squashed) {
    if (!q) return 0;
    const title = cmd._title;
    if (title === q) return 0;
    if (title.indexOf(q) === 0) return 1;
    if (title.indexOf(q) > 0) return 2;
    if (terms.every(t => title.indexOf(t) >= 0)) return 3;
    if (terms.every(t => cmd._tokens.indexOf(t) >= 0)) return 4;
    if (terms.every(t => cmd._tokens.some(k => k.indexOf(t) === 0))) return 5;
    if (terms.every(t => cmd._words.indexOf(t) >= 0)) return 6;
    if (terms.every(t => cmd._words.indexOf(t) >= 0 || cmd._prose.indexOf(t) >= 0)) return 7;
    /* Subsequence against the title only. A two-letter subsequence of a
       hundred words of description matches nearly everything, and a list of
       everything is indistinguishable from a broken filter. Better to show
       nothing and say so. */
    if (squashed && isSubsequence(squashed, title)) return 8;
    return -1;
  }

  function filterCommands(cmds, raw) {
    const q = String(raw || '').toLowerCase().trim().replace(/\s+/g, ' ');
    const terms = q ? q.split(' ') : [];
    const squashed = q.replace(/ /g, '');
    const hits = [];
    cmds.forEach((c, i) => {
      const r = rank(c, q, terms, squashed);
      if (r >= 0) hits.push({ c: c, r: r, i: i });
    });
    hits.sort((a, b) => (a.r - b.r) || (a.i - b.i));
    return hits.map(x => x.c);
  }

  /* ------------------------------------------------------------- the overlay */

  function styleActive(el, on) {
    for (const prop of Object.keys(ACTIVE)) {
      /* setProperty rather than .style.x = : a var() value is set verbatim and
         removing it restores the class's own look. */
      try {
        if (on) el.style.setProperty(prop, ACTIVE[prop]);
        else el.style.removeProperty(prop);
      } catch (_) { /* no CSSOM for this property; cosmetic only */ }
    }
  }

  function setActive(n, scroll) {
    if (!cur) return;
    const items = cur.items;
    if (!items.length) {
      cur.active = -1;
      cur.input.removeAttribute('aria-activedescendant');
      return;
    }
    /* Wraps. Down from the last row goes to the first, which is what every
       list of this shape does and saves a long hold on a full-length list. */
    const i = ((n % items.length) + items.length) % items.length;
    cur.active = i;
    items.forEach((el, k) => {
      const on = k === i;
      el.setAttribute('aria-selected', on ? 'true' : 'false');
      styleActive(el, on);
    });
    cur.input.setAttribute('aria-activedescendant', items[i].id);
    if (scroll) {
      /* jsdom has no layout and older WebKit takes no options object. Neither
         is a reason to break the palette. */
      try { items[i].scrollIntoView({ block: 'nearest' }); } catch (_) {}
    }
  }

  /* How many rows there are, as a sentence.
   *
   * The visible list already answers this for anyone who can see it. The
   * reason it is written down is that nothing otherwise told a screen reader
   * the result of a keystroke: the rows are announced one at a time through
   * aria-activedescendant as you move, and a query that matches NOTHING moves
   * nothing, so typing a word that misses produced silence -- identical to a
   * search box that has stopped responding. A persistent role="status" is the
   * one shape of live region that reliably announces a CHANGE; a .empty div
   * that only appears when the list empties is not, which is why the count
   * lives here and not on that div.
   *
   * Empty while the box is empty, so opening the palette does not announce a
   * number nobody asked for, and set only when the text actually differs so
   * that re-filtering to the same count stays quiet. */
  function paintCount(n, queried) {
    if (!cur || !cur.count) return;
    let msg = '';
    if (queried) msg = n ? (n === 1 ? '1 match' : n + ' matches') : 'No matches';
    if (cur.count.textContent !== msg) cur.count.textContent = msg;
  }

  function paint() {
    if (!cur) return;
    const queried = !!String(cur.input.value || '').trim();
    const shown = filterCommands(cur.cmds, cur.input.value);
    cur.shown = shown;
    cur.items = [];
    clear(cur.list);
    paintCount(shown.length, queried);

    if (!shown.length) {
      /* "No matches" is the count line's job now; this says what to do about
         it, so the two do not read as the same sentence twice. */
      cur.list.append(h('div.empty', cur.cmds.length
        ? 'Try a page name, or a word like dhcp, ssid, ums, led or reboot.'
        : 'No commands are available. The console shell has not finished loading.'));
      setActive(-1, false);
      return;
    }

    shown.forEach((cmd, idx) => {
      const el = h('button.nav-item', {
        type: 'button',
        id: ID + '-opt-' + idx,
        role: 'option',
        'aria-selected': 'false',
        /* Options are not tab stops: focus stays in the input so typing keeps
           filtering, and aria-activedescendant tells a screen reader which row
           Enter would run. Tab leaves the list, it does not walk it. */
        tabindex: '-1',
        style: ITEM_STYLE,
        onclick: () => run(cmd),
      },
        h('span', { style: 'display:block;min-width:0' },
          h('span', cmd.title),
          cmd.hint ? h('span.field-hint', { style: 'display:block' }, cmd.hint) : null),
        h('span.count', { style: TAG_STYLE }, cmd.group));
      cur.items.push(el);
      cur.list.append(el);
    });

    setActive(0, false);
  }

  function run(cmd) {
    if (!cmd) return;
    /* Close BEFORE acting. A command may open a dialog, and ui.js's Modal
       traps focus on the assumption that one dialog is open at a time --
       stacking one on top of the palette leaves the trap pointing at the
       panel underneath (the same trap sessionsFlow() documents in app.js). */
    close();
    try {
      const r = cmd.run();
      if (r && typeof r.catch === 'function') {
        r.catch(e => {
          if (typeof toast === 'function') {
            toast('"' + cmd.title + '" failed: ' + ((e && e.message) || e), true);
          }
        });
      }
    } catch (e) {
      if (typeof toast === 'function') {
        toast('"' + cmd.title + '" failed: ' + ((e && e.message) || e), true);
      }
    }
  }

  function isOpen() { return !!cur; }

  function close() {
    if (!cur) return;
    const open = cur;
    cur = null;                       // first, so a late event sees it closed
    try { open.backdrop.remove(); } catch (_) {}
    try { window.removeEventListener('hashchange', open.onNavigate); } catch (_) {}
    /* Focus goes back where it was. Not if that element has since been removed
       from the document -- running a page jump repaints #view, so the thing
       that had focus may no longer exist, and focusing a detached node silently
       drops focus on <body> instead of leaving it somewhere useful. */
    const back = open.restoreTo;
    try {
      if (back && back.focus && document.contains(back)) back.focus();
    } catch (_) {}
  }

  function open() {
    if (cur) return;

    const restoreTo = document.activeElement;

    const input = h('input', {
      type: 'text',
      id: ID + '-input',
      autocomplete: 'off',
      spellcheck: 'false',
      placeholder: 'dhcp, ssid, led, reboot...',
      /* A combobox over a listbox. aria-expanded is fixed true because the
         list is always present while the palette is open -- it shows an empty
         state rather than collapsing, so "expanded" is never a lie. */
      role: 'combobox',
      'aria-expanded': 'true',
      'aria-controls': ID + '-list',
      'aria-autocomplete': 'list',
      'aria-describedby': ID + '-help',
      oninput: paint,
    });

    const list = h('div', {
      id: ID + '-list',
      role: 'listbox',
      'aria-label': 'Matching pages and commands',
      style: LIST_STYLE,
    });

    /* Hover follows the pointer so that a click never runs something other
     * than the row that is highlighted.
     *
     * mousemove, NOT mouseenter, and this is not a detail: mouseenter fires
     * when a row APPEARS under a pointer that has not moved. Open the palette
     * with the shortcut while the mouse happens to be resting where the third
     * row lands and the third row came up highlighted -- so Enter ran the
     * third row instead of the best match, decided by where somebody had left
     * the mouse. Seen on the real console, not reasoned about. Requiring
     * movement means the pointer only takes over when it is actually used. */
    list.addEventListener('mousemove', e => {
      if (!cur) return;
      const row = (e.target && e.target.closest) ? e.target.closest('[role="option"]') : null;
      if (!row) return;
      const i = cur.items.indexOf(row);
      if (i >= 0 && i !== cur.active) setActive(i, false);
    });

    /* Built once and kept, not rebuilt per paint: see paintCount(). */
    const count = h('div.field-hint', {
      id: ID + '-count',
      role: 'status',
      'aria-live': 'polite',
      /* min-height reserves the line while it is empty, so the first keystroke
         does not push the help text and the Close button down by a line. This
         is what app.css's own .field-error does for the same reason. */
      style: 'margin-top:0;min-height:1em',
    });

    const panel = h('div.modal', {
      role: 'dialog',
      'aria-modal': 'true',
      'aria-label': DIALOG_LABEL,
      onkeydown: onPanelKey,
    },
      h('label.field', { for: ID + '-input' },
        h('span.field-label', DIALOG_LABEL),
        input),
      list,
      count,
      h('div.field-hint', { id: ID + '-help' },
        'Type a page name, or what the control is called. Up and Down move, Enter opens, Esc closes.'),
      h('div.modal-actions',
        h('button.btn.btn-sm', { type: 'button', onclick: () => close() }, 'Close')));

    const backdrop = h('div.modal-backdrop', panel);

    /* Clicking off closes, like every other dialog here. mousedown rather than
       click so a drag that starts inside the panel and ends on the backdrop
       does not count as clicking off. */
    backdrop.addEventListener('mousedown', e => { if (e.target === backdrop) close(); });

    /* Back is the reflex people use to dismiss an overlay, and on a phone it is
       THE dismiss gesture. Without this the panel floats over whichever page
       the router just painted, with its rows still live. */
    const onNavigate = () => close();
    window.addEventListener('hashchange', onNavigate);

    cur = {
      backdrop: backdrop, panel: panel, input: input, list: list, count: count,
      cmds: buildCommands(), shown: [], items: [], active: -1,
      restoreTo: restoreTo, onNavigate: onNavigate,
    };

    document.body.append(backdrop);
    paint();
    try { input.focus(); } catch (_) {}
  }

  /* ------------------------------------------------------------------- keys */

  function focusables() {
    if (!cur) return [];
    return [...cur.panel.querySelectorAll('input,button')]
      .filter(el => !el.disabled && el.getAttribute('tabindex') !== '-1');
  }

  function onPanelKey(e) {
    if (!cur) return;

    /* CONTAINMENT. Nothing typed inside an open palette may reach a
       document-level keydown listener on the page behind it.
       *
       * This is not hypothetical and it is not cosmetic. The Panel page binds
       * keydown on DOCUMENT in the bubble phase (app.js) and maps the arrows,
       * Enter, Space and 1/2/3 straight onto Api.panelPress() -- a POST that
       * presses a physical button on the board. Its only guard is
       * `ev.target.tagName` being INPUT/TEXTAREA/SELECT, which covers the
       * search box but NOT the palette's Close button. So: open the palette on
       * the Panel page, press Tab once (which this file traps onto Close), and
       * every ArrowUp/ArrowDown pressed to move the highlight ALSO pressed a
       * button on the device, as did Space and 1/2/3. Reproduced in jsdom
       * against the real app.js, three leaked presses.
       *
       * The panel is an ancestor of the input, of every row and of Close, so
       * one stopPropagation here contains the lot. It must NOT be done from
       * the document capture listener instead: that fires before the event
       * reaches the target, so stopping there would stop typing in the box.
       *
       * Unconditional, and before the guards below: a key this file does not
       * handle, and a key pressed mid-IME-composition, must be contained just
       * the same. stopPropagation does not touch the default action, so the
       * character still lands in the input. */
    e.stopPropagation();

    if (e.defaultPrevented || e.isComposing) return;

    if (e.key === 'ArrowDown') { e.preventDefault(); setActive(cur.active + 1, true); return; }
    if (e.key === 'ArrowUp') { e.preventDefault(); setActive(cur.active - 1, true); return; }

    if (e.key === 'Enter') {
      /* Always prevented, even with no match: Enter in a text box inside a
         page that has a form must not submit anything. */
      e.preventDefault();
      if (cur.active >= 0 && cur.shown[cur.active]) run(cur.shown[cur.active]);
      return;
    }

    /* Home/End/PageUp/PageDown are deliberately NOT bound. They move the caret
       in the box you are typing in, and taking them to scroll a list this short
       would be a worse trade. */

    if (e.key === 'Tab') {
      const f = focusables();
      if (f.length < 2) { e.preventDefault(); return; }
      const first = f[0], last = f[f.length - 1];
      if (e.shiftKey && document.activeElement === first) { e.preventDefault(); last.focus(); }
      else if (!e.shiftKey && document.activeElement === last) { e.preventDefault(); first.focus(); }
    }
  }

  /* Is this element one you type into? The "/" shortcut must never reach the
     HIDScript editor, a password box, or a name field half-filled in. */
  function isTypingTarget(el) {
    if (!el) return false;
    try { if (el.isContentEditable) return true; } catch (_) {}
    const tag = String(el.tagName || '').toUpperCase();
    if (tag === 'TEXTAREA' || tag === 'SELECT') return true;
    if (String(el.getAttribute && el.getAttribute('role') || '') === 'textbox') return true;
    if (tag === 'INPUT') {
      const type = String(el.type || 'text').toLowerCase();
      /* Everything except the inputs that are really buttons or sliders. */
      return ['button', 'checkbox', 'color', 'file', 'image', 'radio', 'range', 'reset', 'submit']
        .indexOf(type) < 0;
    }
    return false;
  }

  /* The palette only makes sense once the console shell is up: on the sign-in
     screen there is no page to jump to and no session to act with. And it never
     opens over another dialog -- ui.js's Modal has the focus trap, and a second
     trapped panel on top of it would fight for Escape. */
  function canOpen() {
    if (!document.querySelector('.app')) return false;
    if (document.querySelector('.modal-backdrop')) return false;
    return true;
  }

  /* One document-level listener for the life of the tab. It is in the capture
     phase so that opening the palette can stop the key reaching a view's own
     handler -- the Panel page binds ArrowUp/ArrowDown/Enter/1/2/3 on document
     and sends them to the OLED buttons on the board. It preventDefaults ONLY
     the keys it acts on, and only when it acts. */
  function onGlobalKey(e) {
    if (!e || e.defaultPrevented || e.isComposing) return;

    if (isOpen()) {
      /* Escape is handled here rather than on the panel so it works even if
         focus has somehow landed outside it. */
      if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); close(); return; }

      /* The other half of the containment in onPanelKey: a key whose target is
         OUTSIDE the panel never bubbles through the panel, so it would reach
         the page's own document listeners -- the Panel page's OLED keys among
         them -- with a modal dialog open on top. That happens whenever focus
         is not in the palette: input.focus() failed, or something moved focus
         away. Stop it here (capture, so it never reaches the target at all)
         and put focus back where the dialog says it is. Tab is prevented as
         well, or the browser's own default would walk focus further away from
         the dialog immediately after we pulled it back. */
      if (cur.panel && !cur.panel.contains(e.target)) {
        e.stopPropagation();
        if (e.key === 'Tab') e.preventDefault();
        try { cur.input.focus(); } catch (_) { /* nothing more to try */ }
      }
      return;
    }

    if (e.key === 'k' || e.key === 'K') {
      if (e.altKey) return;
      if (!e.ctrlKey && !e.metaKey) return;
      /* macOS text widgets bind Ctrl-K to "delete to the end of the line", and
         the Keystrokes editor is a textarea holding a payload that is only
         saved on request. Taking Ctrl-K there would cost an operator a line of
         their script the first time they reached for the shortcut out of habit.
         Cmd-K still opens the palette from inside the editor, and so does the
         button in the rail; on a keyboard with no Cmd, Ctrl-K works everywhere
         except that one textarea. */
      if (e.ctrlKey && !e.metaKey
          && String((e.target && e.target.tagName) || '').toUpperCase() === 'TEXTAREA') return;
      if (!canOpen()) return;
      e.preventDefault();
      e.stopPropagation();
      open();
      return;
    }

    if (e.key === '/') {
      /* Shift is allowed through: on a German or French layout "/" IS a
         shifted key, and requiring an unshifted one would mean the shortcut
         simply does not exist for those operators. Ctrl, Cmd and Alt are not:
         those are browser and OS shortcuts. */
      if (e.ctrlKey || e.metaKey || e.altKey) return;
      if (isTypingTarget(e.target)) return;
      if (!canOpen()) return;
      e.preventDefault();
      e.stopPropagation();
      open();
    }
  }

  /* --------------------------------------------------------- the visible way in
   *
   * A shortcut nobody is told about does not exist. This file may not touch
   * app.js or index.html, so the button is built here and put into the rail's
   * footer next to the theme switch and the account controls -- which is where
   * the other things that are not a page already live, and which is pinned to
   * the bottom of the rail on a desktop and wraps into the strip on a phone.
   *
   * renderConsole() rebuilds the whole rail on every sign-in, and renderSignIn()
   * empties #root entirely, so this cannot be done once at load. */

  function buildAffordance() {
    return h('button.btn.btn-sm.btn-quiet', {
      type: 'button',
      /* Not a native accesskey: those collide with the browser's own and
         cannot be discovered either. The title says what the keys are. */
      title: 'Search the console. Ctrl-K, Cmd-K, or / on its own.',
      'aria-keyshortcuts': 'Control+K Meta+K',
      onclick: () => { if (canOpen()) open(); },
      /* The "/" is a hint to the eye, not part of the button's name: without
         aria-hidden a screen reader announces "Find a control slash". */
    }, 'Find a control ', h('span.mono', { 'aria-hidden': 'true' }, '/'));
  }

  function ensureAffordance() {
    try {
      /* Fast path: this runs on every DOM change anywhere in #root. */
      if (affordance && document.contains(affordance)) return;
      const host = document.querySelector('.rail-foot');
      if (!host) return;                      // sign-in screen: nothing to search
      if (!affordance) affordance = buildAffordance();
      /* SECOND, not first. app.css's phone layout has a
         `.rail-foot > div:first-child { margin-bottom: 0 !important }` rule
         that flattens the theme switch into the one-line footer strip --
         putting a <button> in front of it makes `div:first-child` match
         nothing and silently re-inflates the footer on every phone. Going in
         after the theme switch leaves that rule pointing where it was. */
      const anchor = host.firstChild ? host.firstChild.nextSibling : null;
      host.insertBefore(affordance, anchor);
    } catch (_) { /* never let the chrome break the console */ }
  }

  function install() {
    document.addEventListener('keydown', onGlobalKey, true);
    ensureAffordance();
    /* The shell appears and reappears: after sign-in, after a password change,
       after a session expires and the operator signs back in. Watching #root is
       the only way to notice from here without editing app.js. The handler is
       idempotent and its fast path is one document.contains(). */
    try {
      const root = document.getElementById('root') || document.documentElement;
      new MutationObserver(ensureAffordance).observe(root, { childList: true, subtree: true });
    } catch (_) { /* no MutationObserver: the button still appears on first paint */ }
  }

  window.CommandPalette = {
    open: () => { if (canOpen()) open(); },
    close: close,
    isOpen: isOpen,
  };

  /* app.js boots on DOMContentLoaded and this file is loaded after it, so in a
     browser the event has not fired yet. The jsdom render harness injects these
     scripts into an already-parsed document, where it may have fired long ago --
     check the state rather than trusting the event. */
  try {
    if (document.readyState === 'loading') {
      window.addEventListener('DOMContentLoaded', () => { try { install(); } catch (_) {} });
    } else {
      install();
    }
  } catch (_) { /* a broken environment must not take the console down with it */ }

})();
