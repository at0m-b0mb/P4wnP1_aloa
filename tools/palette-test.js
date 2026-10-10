/* Functional test for the command palette, driven in jsdom. */
'use strict';
const fs = require('fs');
const path = require('path');
const { JSDOM } = require('jsdom');
const APP = path.join(__dirname, '..', 'dist/www/app');

let pass = 0, fail = 0;
const ok = m => { console.log('  PASS  ' + m); pass++; };
const bad = (m, why) => { console.log('  FAIL  ' + m + '\n        ' + why); fail++; };
const is = (m, a, b) => (String(a) === String(b) ? ok(m) : bad(m, `got ${JSON.stringify(a)} want ${JSON.stringify(b)}`));

function makeDom() {
  const html = fs.readFileSync(path.join(APP, 'index.html'), 'utf8');
  const dom = new JSDOM(html, { url: 'http://127.0.0.1:8000/app/', pretendToBeVisual: true, runScripts: 'dangerously' });
  const { window } = dom;
  if (typeof window.matchMedia !== 'function') {
    window.matchMedia = q => ({ matches: false, media: q, addEventListener(){}, removeEventListener(){}, addListener(){}, removeListener(){}, onchange: null, dispatchEvent: () => false });
  }
  window.fetch = async (url) => {
    const u = String(url);
    const ok2 = body => ({ ok: true, status: 200, json: async () => body, text: async () => JSON.stringify(body), headers: { get: () => 'application/json' }, body: null });
    if (u.includes('/api/auth/whoami')) return ok2({ username: 'admin' });
    if (u.includes('/api/auth/logout')) return ok2({});
    if (u.includes('/api/v1/events')) return { ok: true, status: 200, body: null, text: async () => '' };
    return ok2({});
  };
  window.localStorage.setItem('p4wnp1.token', 't'.repeat(43));
  window.localStorage.setItem('p4wnp1.user', 'admin');
  const srcs = [...window.document.querySelectorAll('script[src]')].map(t => t.getAttribute('src').split('?')[0]);
  for (const tag of [...window.document.querySelectorAll('script[src]')]) tag.remove();
  const errs = [];
  window.addEventListener('error', e => errs.push(e.error || e.message));
  dom.virtualConsole.on('jsdomError', e => errs.push(e));
  for (const f of srcs) {
    const el = window.document.createElement('script');
    el.textContent = fs.readFileSync(path.join(APP, f), 'utf8');
    window.document.head.appendChild(el);
  }
  window.__errs = errs;
  return window;
}

const key = (w, target, k, opts = {}) => {
  const e = new w.KeyboardEvent('keydown', Object.assign({ key: k, bubbles: true, cancelable: true }, opts));
  target.dispatchEvent(e);
  return e;
};
const tick = () => new Promise(r => setTimeout(r, 0));

(async () => {
  const w = makeDom();
  const doc = w.document;
  if (w.__errs.length) bad('scripts load clean', w.__errs.map(String).join('\n'));
  else ok('scripts load clean');

  is('CommandPalette is exported', typeof w.CommandPalette, 'object');

  // ---- console shell ----
  w.renderConsole();
  await tick();
  const btn = [...doc.querySelectorAll('.rail-foot button')].find(b => /Find a control/.test(b.textContent));
  btn ? ok('affordance button is in the rail footer') : bad('affordance button is in the rail footer', 'not found');
  is('affordance sits after the theme switch, not before it',
     doc.querySelector('.rail-foot').children[1] === btn, 'true');
  is('and the theme switch is still div:first-child (app.css phone rule)',
     doc.querySelector('.rail-foot > div:first-child') !== null, 'true');
  is('its accessible name is not polluted by the key hint',
     btn.querySelector('.mono').getAttribute('aria-hidden'), 'true');

  // ---- "/" opens ----
  key(w, doc.body, '/');
  let panel = doc.querySelector('[role="dialog"][aria-modal="true"]');
  panel ? ok('"/" opens the palette') : bad('"/" opens the palette', 'no dialog');
  const input = doc.getElementById('p4-cmd-input');
  is('focus moved into the input', doc.activeElement === input, 'true');
  is('input is a combobox', input.getAttribute('role'), 'combobox');
  const list = doc.getElementById('p4-cmd-list');
  is('listbox is wired to the input', input.getAttribute('aria-controls'), list.id);
  const rows = () => [...list.querySelectorAll('[role="option"]')];
  is('every page plus the actions are listed', rows().length >= 13, 'true');
  is('first row is active', rows()[0].getAttribute('aria-selected'), 'true');
  is('activedescendant points at it', input.getAttribute('aria-activedescendant'), rows()[0].id);
  is('active row is visibly marked', /surface-sunk/.test(rows()[0].getAttribute('style') || ''), 'true');
  is('rows are not tab stops', rows()[0].getAttribute('tabindex'), '-1');
  is('current page is tagged', rows()[0].textContent.includes('current'), 'true');

  // ---- filtering ----
  input.value = 'dhcp';
  input.dispatchEvent(new w.Event('input', { bubbles: true }));
  is('"dhcp" finds Network', rows()[0].textContent.startsWith('Network'), 'true');
  input.value = 'netwrk';
  input.dispatchEvent(new w.Event('input', { bubbles: true }));
  is('"netwrk" (typo) still finds Network via subsequence', rows()[0].textContent.startsWith('Network'), 'true');
  input.value = 'led';
  input.dispatchEvent(new w.Event('input', { bubbles: true }));
  is('"led" finds System', rows()[0].textContent.startsWith('System'), 'true');
  input.value = 'led';
  input.dispatchEvent(new w.Event('input', { bubbles: true }));
  is('a keyword hit beats a hit inside an English word ("called")', rows()[0].textContent.startsWith('System'), 'true');
  for (const [q, want] of [['ums', 'Cable'], ['pin', 'Bluetooth'], ['lease', 'Network'],
                           ['oled', 'Panel'], ['iso', 'Storage'], ['hidscript', 'Keystrokes'],
                           ['trigger', 'Reflexes'], ['master template', 'Loadouts'],
                           ['log', 'Journal'], ['ssid', 'WiFi'],
                           /* Radio was removed and its cards split across WiFi,
                              Bluetooth and Network. Anyone who still calls it
                              that must still find the page. */
                           ['radio', 'WiFi'],
                           ['reboo', 'System'], ['usb', 'Cable'], ['eject', 'Storage'],
                           /* 'password' must rank the ACCOUNT action first, even
                              though the WiFi page also carries the word for the
                              access-point key. Someone typing it almost always
                              means their own login. */
                           ['password', 'Change the console password'], ['dark', 'Switch to the dark theme'],
                           ['nap', 'Bluetooth'], ['dns', 'Network'], ['journal', 'Journal']]) {
    input.value = q;
    input.dispatchEvent(new w.Event('input', { bubbles: true }));
    is(`"${q}" finds ${want}`, rows().length ? rows()[0].textContent.startsWith(want) : 'no rows', 'true');
  }
  input.value = 'reboot';
  input.dispatchEvent(new w.Event('input', { bubbles: true }));
  is('"reboot" finds System and does not reboot anything', rows()[0].textContent.startsWith('System'), 'true');
  input.value = 's';
  input.dispatchEvent(new w.Event('input', { bubbles: true }));
  is('a bare "s" does not land on Sign out', rows()[0].textContent.startsWith('Sign out'), 'false');
  input.value = 'zzqq';
  input.dispatchEvent(new w.Event('input', { bubbles: true }));
  is('no match shows an empty state', list.querySelectorAll('.empty').length, 1);
  is('no match clears activedescendant', input.hasAttribute('aria-activedescendant'), 'false');
  let e = key(w, input, 'Enter');
  is('Enter with no match runs nothing', doc.querySelector('[role="dialog"]') !== null, 'true');

  // ---- arrows ----
  input.value = '';
  input.dispatchEvent(new w.Event('input', { bubbles: true }));
  key(w, input, 'ArrowDown');
  is('ArrowDown moves to the second row', rows()[1].getAttribute('aria-selected'), 'true');
  is('and unmarks the first', rows()[0].getAttribute('aria-selected'), 'false');
  key(w, input, 'ArrowUp');
  key(w, input, 'ArrowUp');
  is('ArrowUp from the top wraps to the last row', rows()[rows().length - 1].getAttribute('aria-selected'), 'true');

  // ---- Escape closes and restores focus ----
  key(w, doc.getElementById('p4-cmd-input'), 'Escape');
  const navBtn = doc.querySelector('.nav-item');
  navBtn.focus();
  key(w, doc.body, '/');
  is('palette opened from a focused nav button', doc.activeElement === doc.getElementById('p4-cmd-input'), 'true');
  key(w, doc.getElementById('p4-cmd-input'), 'Escape');
  is('Escape closes', doc.querySelector('[role="dialog"][aria-modal="true"]'), 'null');
  is('focus is restored to the opener', doc.activeElement === navBtn, 'true');

  // ---- Enter runs a page jump ----
  key(w, doc.body, '/');
  const i2 = doc.getElementById('p4-cmd-input');
  i2.value = 'journal';
  i2.dispatchEvent(new w.Event('input', { bubbles: true }));
  key(w, i2, 'Enter');
  await tick();
  is('Enter navigated to the Journal', w.location.hash, '#journal');
  is('and closed the palette', doc.querySelector('[role="dialog"][aria-modal="true"]'), 'null');
  is('the page really rendered', /Journal/.test(doc.querySelector('.page-title').textContent), 'true');

  // ---- hover only takes over once the pointer actually moves ----
  key(w, doc.body, '/');
  const iH = doc.getElementById('p4-cmd-input');
  const rowsH = () => [...doc.querySelectorAll('#p4-cmd-list [role="option"]')];
  rowsH()[2].dispatchEvent(new w.MouseEvent('mouseenter', { bubbles: false }));
  rowsH()[2].dispatchEvent(new w.MouseEvent('mouseover', { bubbles: true }));
  is('a row appearing under a still pointer does not steal the highlight', rowsH()[0].getAttribute('aria-selected'), 'true');
  rowsH()[2].dispatchEvent(new w.MouseEvent('mousemove', { bubbles: true }));
  is('moving the pointer does move the highlight', rowsH()[2].getAttribute('aria-selected'), 'true');
  is('and the old row is released', rowsH()[0].getAttribute('aria-selected'), 'false');
  key(w, iH, 'Escape');

  // ---- clicking a row runs it ----
  key(w, doc.body, '/');
  const i3 = doc.getElementById('p4-cmd-input');
  i3.value = 'cable';
  i3.dispatchEvent(new w.Event('input', { bubbles: true }));
  doc.querySelector('#p4-cmd-list [role="option"]').click();
  await tick();
  is('a click navigates too', w.location.hash, '#cable');

  // ---- the theme row ----
  key(w, doc.body, '/');
  const i4 = doc.getElementById('p4-cmd-input');
  i4.value = 'theme';
  i4.dispatchEvent(new w.Event('input', { bubbles: true }));
  const themeRow = doc.querySelector('#p4-cmd-list [role="option"]');
  is('the theme row says which way it goes', /Switch to the dark theme/.test(themeRow.textContent), 'true');
  themeRow.click();
  await tick();
  is('it switched the theme', doc.documentElement.getAttribute('data-theme'), 'dark');
  key(w, doc.body, '/');
  const i5 = doc.getElementById('p4-cmd-input');
  i5.value = 'theme';
  i5.dispatchEvent(new w.Event('input', { bubbles: true }));
  is('and the label flipped', /Switch to the light theme/.test(doc.querySelector('#p4-cmd-list [role="option"]').textContent), 'true');
  key(w, doc.getElementById('p4-cmd-input'), 'Escape');

  // ---- it must not steal keys from an editor ----
  w.go('keystrokes');
  await tick(); await tick();
  const ta = doc.querySelector('#view textarea');
  ta ? ok('found the HIDScript textarea') : bad('found the HIDScript textarea', 'none on the Keystrokes page');
  ta.focus();
  e = key(w, ta, '/');
  is('"/" in the editor is left alone', e.defaultPrevented, 'false');
  is('and does not open the palette', doc.querySelector('[role="dialog"][aria-modal="true"]'), 'null');
  e = key(w, ta, 'k', { ctrlKey: true });
  is('Ctrl-K in the editor is left alone (macOS kill-line)', e.defaultPrevented, 'false');
  e = key(w, ta, 'k', { metaKey: true });
  is('Cmd-K in the editor DOES open it', e.defaultPrevented, 'true');
  is('the palette is open', doc.querySelector('[role="dialog"][aria-modal="true"]') !== null, 'true');
  key(w, doc.getElementById('p4-cmd-input'), 'Escape');
  is('focus went back to the editor', doc.activeElement === ta, 'true');

  // ---- Enter on the Close button closes; it must not run the highlighted row ----
  /* focusables() is [input, Close], so one Tab from the search box lands on
     Close. The panel's keydown handler used to take Enter whatever the target
     was and preventDefault() it, which cancels a button's own activation -- so
     Tab-then-Enter did not close the dialog, it ran the highlighted row. On
     this page, with an empty query, that row is "you are on this page", whose
     run() is go('keystrokes'): the view is torn down and re-read from the
     device, taking the unsaved payload with it. jsdom does not implement a
     button's Enter activation, so what is pinned here is the half this file
     controls -- the event is left unprevented and no command fires. */
  ta.value = 'type("unsaved payload")';
  key(w, doc.body, '/');
  const closeBtn = [...doc.querySelectorAll('[role="dialog"] .modal-actions button')]
    .find(b => b.textContent === 'Close');
  closeBtn ? ok('the palette has a Close button') : bad('the palette has a Close button', 'none in .modal-actions');
  closeBtn.focus();
  e = key(w, closeBtn, 'Enter');
  await tick();
  is('Enter on Close is left to the button', e.defaultPrevented, 'false');
  /* run() closes the palette before it acts, so a palette still open is proof
     that no command fired. */
  is('and no command fired', doc.querySelector('[role="dialog"][aria-modal="true"]') !== null, 'true');
  is('the unsaved payload is still in the editor', doc.querySelector('#view textarea').value, 'type("unsaved payload")');
  closeBtn.click();
  is('clicking Close still closes', doc.querySelector('[role="dialog"][aria-modal="true"]'), 'null');

  // ---- a text input is protected too ----
  const ti = doc.querySelector('#view input[type=text]') || doc.querySelector('input[type=text]');
  if (ti) {
    ti.focus();
    e = key(w, ti, '/');
    is('"/" in a text field is left alone', e.defaultPrevented, 'false');
  }

  // ---- the Panel page binds arrows on document; the palette must not fight it ----
  w.go('panel');
  await tick(); await tick();
  e = key(w, doc.body, 'ArrowDown');
  is('Panel still gets ArrowDown when the palette is closed', e.defaultPrevented, 'true');
  key(w, doc.body, '/');
  const i6 = doc.getElementById('p4-cmd-input');
  e = key(w, i6, 'ArrowDown');
  is('ArrowDown inside the palette moves the palette', e.defaultPrevented, 'true');
  is('and the palette is still open (no OLED press ran it off)', doc.querySelector('[role="dialog"][aria-modal="true"]') !== null, 'true');
  key(w, i6, 'Escape');

  // ---- it must not open over another dialog ----
  w.go('overview');
  await tick();
  const closeModal = w.eval("Modal.open({title:'x', body:'y', actions:[]})");
  e = key(w, doc.body, 'k', { ctrlKey: true });
  is('Ctrl-K does not open over an existing dialog', doc.querySelectorAll('[aria-modal="true"]').length, 1);
  closeModal();

  // ---- sign out confirms rather than firing ----
  key(w, doc.body, '/');
  const i7 = doc.getElementById('p4-cmd-input');
  i7.value = 'sign out';
  i7.dispatchEvent(new w.Event('input', { bubbles: true }));
  doc.querySelector('#p4-cmd-list [role="option"]').click();
  await tick();
  is('sign out asks first', /Sign out of the console/.test(doc.body.textContent), 'true');
  is('and has not signed anyone out yet', doc.querySelectorAll('.signin').length, 0);
  const cancel = [...doc.querySelectorAll('.modal-actions button')].find(b => b.textContent === 'Cancel');
  cancel.click();
  await tick();
  is('cancelling leaves you signed in', doc.querySelectorAll('.signin').length, 0);

  // ---- the shell is rebuilt: the button comes back ----
  w.renderSignIn();
  await tick();
  w.renderConsole();
  await tick();
  const again = [...doc.querySelectorAll('.rail-foot button')].filter(b => /Find a control/.test(b.textContent));
  is('exactly one affordance after a re-render', again.length, 1);
  again[0].click();
  is('the button opens the palette', doc.querySelector('[role="dialog"][aria-modal="true"]') !== null, 'true');
  key(w, doc.body, 'Escape');

  // ---- back button dismisses ----
  key(w, doc.body, '/');
  w.location.hash = '#storage';
  await tick();
  is('a hash change closes the palette', doc.querySelector('[role="dialog"][aria-modal="true"]'), 'null');

  // ---- the sign-in screen: no shell, so nothing to search ----
  w.localStorage.removeItem('p4wnp1.token');
  w.eval('Api.setToken(null)');
  w.renderSignIn();
  await tick();
  is('no affordance on the sign-in screen', doc.querySelectorAll('.rail-foot').length, 0);
  const e2 = key(w, doc.body, 'k', { ctrlKey: true });
  is('Ctrl-K is left alone on the sign-in screen', e2.defaultPrevented, 'false');
  is('and opens nothing', doc.querySelectorAll('[aria-modal="true"]').length, 0);
  const pw = doc.querySelector('.signin input[type=password]');
  pw.focus();
  const e3 = key(w, pw, '/');
  is('"/" typed into the password box is left alone', e3.defaultPrevented, 'false');

  if (w.__errs.length) bad('no uncaught errors over the whole run', w.__errs.map(String).join('\n'));
  else ok('no uncaught errors over the whole run');

  console.log(`\n  ---- ${pass} passed, ${fail} failed ----`);
  process.exit(fail ? 1 : 0);
})().catch(e => { console.log('HARNESS THREW\n' + e.stack); process.exit(1); });
