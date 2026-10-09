/* ui.js -- DOM helpers, dialogs and formatting shared by every view.
 *
 * The dialogs here replace window.prompt / alert / confirm. Those were not just
 * ugly: prompt() is blocked outright in some mobile browsers and in cross-origin
 * iframes, none of them can validate input, none can explain consequences
 * properly, and a mis-click loses whatever the operator had typed. This console
 * is frequently driven from a phone, so that mattered.
 */
'use strict';

/* Tiny hyperscript. `h('div.card', {onclick: f}, 'text', child)`.
 * Text always goes in via textContent, never innerHTML, so device-supplied
 * strings (SSIDs, hostnames, log lines, script output) cannot inject markup. */
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
const clear = el => { while (el && el.firstChild) el.removeChild(el.firstChild); return el; };

function toast(message, isError) {
  const box = $('#toasts');
  if (!box) return;
  const t = h('div.toast' + (isError ? '.toast-err' : ''),
    message instanceof Node ? message : String(message));
  box.append(t);
  setTimeout(() => t.remove(), isError ? 9000 : 4000);
}

/* Remove every toast currently on screen.
 *
 * Error toasts live 12-20 seconds so there is time to read the hint and press
 * the action button. That is right while you stay put, and wrong the moment
 * you navigate: "This device has no usable WiFi" from the Radio page would
 * still be sitting over the Keystrokes editor twenty seconds later, covering
 * the Run button. Advice about a page you have left is just an obstruction. */
function clearToasts() {
  const box = $('#toasts');
  if (box) clear(box);
}

/* --- base64 that survives non-ASCII ---------------------------------------
 * btoa() throws on any code point above 0xFF, so a script containing an
 * accented character or a smart quote -- which a payload pasted from a web page
 * very often does -- would fail to save with an unhelpful DOM exception. */
function b64encode(str) {
  const bytes = new TextEncoder().encode(str);
  let bin = '';
  for (const b of bytes) bin += String.fromCharCode(b);
  return btoa(bin);
}
function b64decode(b64) {
  const bin = atob(b64 || '');
  const bytes = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i);
  return new TextDecoder().decode(bytes);
}

/* --- modal ----------------------------------------------------------------
 * One implementation behind confirm/prompt/show. Focus is trapped while open,
 * Escape cancels, Enter confirms from a single-line input, and focus returns to
 * whatever opened it. */
const Modal = (() => {
  let openCount = 0;

  function open({ title, body, actions, onEscape, initialFocus }) {
    const previouslyFocused = document.activeElement;
    const titleId = 'modal-title-' + (++openCount);

    const panel = h('div.modal', {
      role: 'dialog', 'aria-modal': 'true', 'aria-labelledby': titleId,
    },
      h('h2.modal-title', { id: titleId }, title),
      h('div.modal-body', body),
      h('div.modal-actions', actions));

    const backdrop = h('div.modal-backdrop', panel);

    function close() {
      backdrop.remove();
      document.removeEventListener('keydown', onKey, true);
      if (previouslyFocused && previouslyFocused.focus) previouslyFocused.focus();
    }

    function focusables() {
      return [...panel.querySelectorAll(
        'button,input,select,textarea,a[href],[tabindex]:not([tabindex="-1"])')]
        .filter(el => !el.disabled && el.offsetParent !== null);
    }

    function onKey(e) {
      if (e.key === 'Escape') { e.preventDefault(); close(); if (onEscape) onEscape(); return; }
      if (e.key !== 'Tab') return;
      // Trap focus: without this, Tab walks out of the dialog into the page
      // behind it, which is both confusing and an accessibility failure.
      const f = focusables();
      if (!f.length) return;
      const first = f[0], last = f[f.length - 1];
      if (e.shiftKey && document.activeElement === first) { e.preventDefault(); last.focus(); }
      else if (!e.shiftKey && document.activeElement === last) { e.preventDefault(); first.focus(); }
    }

    backdrop.addEventListener('mousedown', e => {
      if (e.target === backdrop) { close(); if (onEscape) onEscape(); }
    });
    document.addEventListener('keydown', onKey, true);
    document.body.append(backdrop);

    const target = initialFocus || focusables()[0];
    if (target) target.focus();
    return close;
  }

  return { open };
})();

/* Confirm a consequential action.
 *
 * `consequence` is deliberately a separate field from `body`: the most common
 * failure of a confirm dialog is telling the user WHAT will happen but not what
 * it will cost them, so the caller is pushed to say it. */
function confirmAction({ title, body, consequence, confirmLabel = 'Continue', danger = false }) {
  return new Promise(resolve => {
    let settled = false;
    const done = v => { if (!settled) { settled = true; resolve(v); } };
    const close = Modal.open({
      title,
      body: [
        body ? h('p', body) : null,
        consequence ? h('div.modal-consequence',
          h('span.modal-consequence-label', 'What this does'),
          h('p', consequence)) : null,
      ],
      actions: [
        h('button.btn', { type: 'button', onclick: () => { close(); done(false); } }, 'Cancel'),
        h('button', {
          type: 'button',
          class: 'btn ' + (danger ? 'btn-danger' : 'btn-primary'),
          onclick: () => { close(); done(true); },
        }, confirmLabel),
      ],
      onEscape: () => done(false),
    });
  });
}

/* Ask for one value, with validation that runs before the dialog closes. */
function promptValue({ title, label, value = '', placeholder = '', hint = '', confirmLabel = 'Save', validate }) {
  return new Promise(resolve => {
    let settled = false;
    const done = v => { if (!settled) { settled = true; resolve(v); } };

    const input = h('input', { type: 'text', value, placeholder, autocomplete: 'off' });
    const error = h('div.field-error', { role: 'alert' });

    function submit() {
      const v = input.value.trim();
      if (validate) {
        const problem = validate(v);
        if (problem) {
          clear(error).append(problem);
          input.focus();
          input.select();
          return;
        }
      }
      close();
      done(v);
    }

    input.addEventListener('keydown', e => {
      if (e.key === 'Enter') { e.preventDefault(); submit(); }
    });

    const close = Modal.open({
      title,
      body: [
        h('label.field',
          h('span.field-label', label),
          input,
          hint ? h('span.field-hint', hint) : null),
        error,
      ],
      actions: [
        h('button.btn', { type: 'button', onclick: () => { close(); done(null); } }, 'Cancel'),
        h('button.btn.btn-primary', { type: 'button', onclick: submit }, confirmLabel),
      ],
      onEscape: () => done(null),
      initialFocus: input,
    });
    input.select();
  });
}

/* Ask for several values at once, validated together before the dialog closes.
 * Used for the password change, where validating one field at a time would mean
 * three separate dialogs and no way to compare the two new-password entries. */
function promptForm({ title, intro, fields, confirmLabel = 'Save', validate }) {
  return new Promise(resolve => {
    let settled = false;
    const done = v => { if (!settled) { settled = true; resolve(v); } };

    const inputs = {};
    const error = h('div.field-error', { role: 'alert' });

    const body = [
      intro ? h('p', intro) : null,
      ...fields.map(f => {
        /* A field with `options` is a picker, not a text box.
         *
         * This matters more than it looks. The reflex builder used to take a
         * template name as free text: a typo armed a rule that pointed at
         * nothing, and nothing said so -- not at save time, because the name
         * is not resolved then, and not at fire time, because a trigger
         * action logs its failure to the journal and carries on. The
         * operator is left believing the device will react to something it
         * will ignore. A list of what actually exists cannot be mistyped.
         *
         * `emptyLabel` gives the picker a "leave this alone" entry, which a
         * loadout needs -- it may deliberately set only some subsystems. */
        /* `multiple` exists because some proto fields are `repeated` and a
           single picker would quietly drop the rest. A loadout's
           template_names_network is the case in hand: one stored network
           template per interface, so a loadout usually names several. */
        const input = f.options
          ? h('select', f.multiple
            ? { autocomplete: 'off', multiple: true, size: String(Math.min(Math.max(f.options.length, 2), 6)) }
            : { autocomplete: 'off' },
            ...(f.emptyLabel && !f.multiple ? [h('option', { value: '' }, f.emptyLabel)] : []),
            ...f.options.map(o => h('option', { value: o }, o)))
          : h('input', {
            type: f.type || 'text',
            value: f.value || '',
            placeholder: f.placeholder || '',
            autocomplete: f.autocomplete || 'off',
          });
        /* Setting .value on a <select> only takes once the options exist, so
           it happens here rather than in the h() props above. A value that is
           not among the options leaves the select on its first entry, which
           is the honest outcome: the stored name is gone. */
        if (f.options && !f.multiple && f.value) input.value = f.value;
        if (f.options && f.multiple && Array.isArray(f.value)) {
          for (const o of input.options) o.selected = f.value.includes(o.value);
        }
        inputs[f.key] = input;
        input.addEventListener('keydown', e => {
          if (e.key === 'Enter') { e.preventDefault(); submit(); }
        });
        return h('label.field',
          h('span.field-label', f.label),
          input,
          f.hint ? h('span.field-hint', f.hint) : null);
      }),
      error,
    ];

    function submit() {
      const values = {};
      for (const k of Object.keys(inputs)) {
        const el = inputs[k];
        /* A <select multiple>'s .value is just its FIRST selected option --
           reading it like a text box silently discards every other choice,
           which is the exact failure `multiple` was added to prevent. */
        values[k] = el.multiple ? [...el.selectedOptions].map(o => o.value) : el.value;
      }
      const problem = validate ? validate(values) : null;
      if (problem) {
        clear(error).append(problem);
        const first = fields.find(f => f.key === problem.field) || fields[0];
        if (inputs[first.key]) inputs[first.key].focus();
        return;
      }
      close();
      done(values);
    }

    const close = Modal.open({
      title,
      body,
      actions: [
        h('button.btn', { type: 'button', onclick: () => { close(); done(null); } }, 'Cancel'),
        h('button.btn.btn-primary', { type: 'button', onclick: submit }, confirmLabel),
      ],
      onEscape: () => done(null),
      initialFocus: inputs[fields[0].key],
    });
  });
}

/* Show something the user needs to read -- a job result, a decoded payload. */
function showDetail({ title, body, mono = false }) {
  return new Promise(resolve => {
    const close = Modal.open({
      title,
      body: mono ? h('pre.detail', String(body)) : h('p', String(body)),
      actions: [h('button.btn.btn-primary', { type: 'button', onclick: () => { close(); resolve(); } }, 'Close')],
      onEscape: resolve,
    });
  });
}

const fmtTime = ms => new Date(Number(ms) || Date.now())
  .toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false });

/* A point in time relative to now: "in 23h", "4m ago", "just now".
 *
 * fmtTime shows a clock time only, which is wrong for anything more than a few
 * hours out: a session expiring in 24 hours renders with the same HH:MM:SS it
 * was issued at, so "expires 08:04:24" reads as six seconds away rather than
 * tomorrow morning. That was shipped in the sessions view and spotted by
 * reading the rendered table. */
const fmtRelative = ms => {
  const diff = Number(ms) - Date.now();
  const abs = Math.abs(diff);
  const mins = Math.round(abs / 60000);
  if (mins < 1) return 'just now';
  let span;
  if (mins < 60) span = mins + 'm';
  else if (mins < 60 * 48) span = Math.round(mins / 60) + 'h';
  else span = Math.round(mins / (60 * 24)) + ' days';
  return diff >= 0 ? 'in ' + span : span + ' ago';
};

const fmtBytes = n => {
  n = Number(n) || 0;
  if (n < 1024) return n + ' B';
  if (n < 1024 * 1024) return (n / 1024).toFixed(1) + ' KB';
  return (n / 1024 / 1024).toFixed(1) + ' MB';
};
