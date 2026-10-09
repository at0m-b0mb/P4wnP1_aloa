/* view-system.js -- the device itself.
 *
 * The original GopherJS client's "Generic settings" page carried four things
 * that nothing in this console covered: the status LED, the configuration
 * database (backup AND restore -- this console only had backup, buried on the
 * Loadouts page), reboot/shutdown, and nothing at all for "is the API even
 * answering". Master templates are deliberately NOT here: Views.loadouts owns
 * them and does more than the original did.
 *
 * Everything on this page is read from, or sent to, an RPC that already
 * exists. No new backend capability:
 *
 *   GetLEDSettings / SetLEDSettings     LEDSettings.blink_count
 *   ListStoredDBBackups / DBBackup / DBRestore
 *   Reboot / Shutdown
 *   EchoRequest
 *   GetStartupMasterTemplate            (read-only, and only because a
 *                                        database restore replaces it)
 *
 * Loads after app.js, which defines Views, and uses only the helpers in
 * ui.js and app.js. A classic script, no module, no build step -- same
 * reasoning as app.js: the smallest target is a Pi Zero W.
 */
'use strict';

/* --- the LED ---------------------------------------------------------------
 *
 * The Pi's green ACT LED is the only output this device has on a build with no
 * OLED panel, and service/SubSysLED.go drives it as a repeating blink pattern
 * with exactly one knob: how many blinks per cycle.
 *
 * The timings below are that file's constants (pLED_DELAY_ON,
 * pLED_DELAY_OFF, pLED_DELAY_PAUSE). The 0 = off reading is the device's own
 * two other clients agreeing: cli_client's --blink help text says "0: Off,
 * 1..254: blink n times, >254: On", and the OLED System menu labels 0 as
 * "LED: off".
 *
 * THE 255 CASE DOES NOT DO WHAT THAT HELP TEXT SAYS, and the page must not
 * repeat it. Read led_loop with a count of 255:
 *
 *   - before the loop it writes pLED_ON.
 *   - the inner loop writes pLED_ON, sleeps, and then the branch commented
 *     "Don't turn off led if blink_count >= 255 (solid)" -- `if 255 >
 *     blink_count` -- is FALSE, so pLED_OFF is never written at all.
 *
 * With a count of 0 the inner loop never runs, so the only write is the
 * pLED_ON before it. Both cases therefore leave /sys/class/leds/led0/
 * brightness holding the same value forever, and nothing on the device can
 * tell them apart. 0 and 255 are the same setting wearing two names.
 *
 * Which value that is: pLED_ON is the string "0". Writing 0 to a leds-gpio
 * brightness file is dark -- the driver applies the ACTIVE_LOW inversion the
 * Pi's ACT LED needs, so polarity does not rescue it. So the constant is
 * misnamed and ">= 255" most likely holds the LED OFF, which is why the two
 * clients that only ever set 0, 1 and 2 are right and the one help string
 * that mentions 255 is wrong. The page states the part that is certain (the
 * two are indistinguishable) and hedges the part that is not (which state).
 *
 * What a PARTICULAR count means is not knowable from here and this page does
 * not pretend otherwise. Nothing in the service assigns a meaning to "two
 * blinks"; it is whatever the operator's reflexes and scripts set it to, so
 * the page says that instead of inventing a convention. */
const LED_ON_MS = 200;
const LED_OFF_MS = 200;
const LED_PAUSE_MS = 500;
const LED_SOLID_AT = 255;

/* 0 and >= 255 deliberately read the same, because on the device they ARE
   the same -- see the note above. Calling one "off" and the other "lit solid"
   was the page asserting a difference the service does not produce. */
function ledPattern(n) {
  if (n === null) return 'unknown';
  if (n === 0 || n >= LED_SOLID_AT) return 'held, not blinking';
  return n + (n === 1 ? ' blink' : ' blinks') + ', pause, repeat';
}

function ledTiming(n) {
  if (n === null) return 'The device did not report a blink count, so what the LED is doing now is unknown.';
  if (n === 0) {
    return 'No pattern. The service writes the LED once and then leaves it alone. '
      + 'Both other clients on this device call 0 "off" and the value it writes is '
      + 'brightness 0, which is dark, so off is the reading this page goes with. A '
      + 'count of ' + LED_SOLID_AT + ' or more writes exactly the same value, so the '
      + 'two settings look identical on the hardware.';
  }
  if (n >= LED_SOLID_AT) {
    return 'Not a pattern. At ' + LED_SOLID_AT + ' or more the service skips its '
      + 'turn-off step entirely, so it writes the same value it writes for 0 and never '
      + 'writes the other one -- which means this is indistinguishable from 0 on the '
      + 'device. The service calls it "solid" and one help string calls it "On", but '
      + 'the value being written is brightness 0, so expect it to look like off rather '
      + 'than lit. Use a blink count if you need to see something.';
  }
  const cycle = n * (LED_ON_MS + LED_OFF_MS) + LED_PAUSE_MS;
  return LED_ON_MS + ' ms on, ' + LED_OFF_MS + ' ms off, ' + n + ' times, then a '
    + LED_PAUSE_MS + ' ms gap -- a ' + (cycle / 1000).toFixed(1) + ' s cycle. '
    + 'Counting the blinks in the gaps is the point, so keep counts low enough to count.';
}

/* A simulation of the pattern, not a reading of the LED.
 *
 * Worth being exact about why this is honest: there is no RPC that reports
 * LED state. SubSysLED's LedState1 has an `Available` field and the service
 * never sets it, so even the device does not know whether the LED exists --
 * on a Pi with the ACT LED repurposed by a dtoverlay, every write goes
 * nowhere and nothing notices. So this dot is drawn from the timing
 * constants above and the card says so in as many words. It is here because
 * the operator is usually not holding the device, and "2 blinks" on a screen
 * is a number, while a blinking dot is the thing they will be looking for. */
/* Stops the previous preview. Views.system() re-renders ITSELF after every LED
   change and after a backup or restore, and a direct Views.system() call does
   NOT run runViewTeardown() -- only go() does. So each re-render used to start
   another independent 200 ms chain ticking away at a dot that had just been
   removed from the DOM, and clicking the five presets in a row left five of
   them running until the operator navigated elsewhere. onViewTeardown alone
   does not cover this; the previous chain has to be stopped on the way in. */
let ledPreviewStop = null;

function ledPreviewDot(n) {
  if (ledPreviewStop) ledPreviewStop();

  const dot = h('span', { class: 'dot dot-idle', style: 'width:12px;height:12px;vertical-align:middle' });
  /* No dot-ok for >= 255. The service writes the same value for 255 and for 0
     and never writes the other one, so a green dot here against a grey one for
     0 would be drawing a difference that does not exist on the device. Both
     get the static dot; the table row says "held, not blinking" for both. */
  if (n === null || n === 0 || n >= LED_SOLID_AT) return dot;

  const steps = [];
  for (let i = 0; i < n; i++) { steps.push([true, LED_ON_MS]); steps.push([false, LED_OFF_MS]); }
  steps.push([false, LED_PAUSE_MS]);

  let at = 0;
  let timer = null;
  let live = true;
  const stop = () => { live = false; if (timer) clearTimeout(timer); timer = null; };
  /* `live` and not just clearTimeout: a tick already dequeued and waiting to
     run would re-arm itself and resurrect the chain we just cleared. */
  ledPreviewStop = stop;
  /* REQUIRED: a view that starts a timer and does not register teardown
     leaves it running for the life of the page, and the dot it is writing to
     has been removed from the DOM -- so it burns a wakeup every 200ms,
     forever, on a single-core 1GHz board. */
  onViewTeardown(stop);

  const tick = () => {
    if (!live) return;
    const step = steps[at % steps.length];
    dot.className = 'dot ' + (step[0] ? 'dot-ok' : 'dot-idle');
    at++;
    timer = setTimeout(tick, step[1]);
  };
  tick();
  return dot;
}

/* --- the database ---------------------------------------------------------
 *
 * Read service/datastore/store.go before changing a word of the text in this
 * card. Restore(path, true) -- which is what DBRestore calls -- does this:
 * clear EVERY key in the live database, load the backup into a temporary
 * database, then copy every key across. It is a replace, not a merge, and it
 * happens on the open database.
 *
 * That last part is the bit worth checking, and it is NOT the same as
 * datastore.Open's behaviour: Open restores its initial backup only when the
 * working directory does not already exist, so on an installed device it
 * never fires. DBRestore has no such condition. The restored templates are
 * therefore in the library the moment the call returns -- no restart needed
 * to see them.
 *
 * What a restart (or a deploy) IS needed for is the configuration actually
 * running. DBRestore touches the stored library only; it does not re-deploy
 * USB, WiFi, Bluetooth or networking. The card says both halves.
 *
 * What is NOT in this database, and so survives a restore untouched:
 *   - HIDScript and bash scripts. ListStoredHIDScripts and
 *     ListStoredBashScripts read the filesystem (rpc_server.go:628, and
 *     PATH_HID_SCRIPTS / PATH_BASH_SCRIPTS in common/globals.go).
 *   - the console password and your session. service/auth/store.go keeps
 *     users in a separate JSON file, so a restore cannot lock you out and
 *     cannot roll a password change back. Saying so is the difference
 *     between an operator using this feature and not touching it.
 *   - the backups themselves: they are .db files in the backup directory,
 *     not keys in the database.
 */
const DB_BACKUP_DIR = '/usr/local/P4wnP1/db';
const DB_FACTORY_FILE = 'init.db';

/* Mirror of the server's filename handling (rpc_server.go DBBackup/DBRestore):
   it appends .db unless the extension already is .db, case-insensitively. The
   list comes back with extensions, so showing the operator the name the file
   will actually have avoids "I saved it as before-job and the list shows
   before-job.db". */
function dbFileName(name) {
  return /\.db$/i.test(name) ? name : name + '.db';
}

/* --- the page ------------------------------------------------------------ */

Views.system = async function () {
  const main = clear($('#view'));
  main.append(pageHead('System',
    'The device itself: its status LED, the database that holds every stored template, '
    + 'and the two buttons that take it off the air.'));

  /* All four reads are tolerant. A device answering none of them should still
     show a page that explains what the controls do -- that is when an
     operator most needs to read it. guard() reports and returns undefined;
     the plain .catch(() => null) calls are ones where a toast would be noise
     (the boot default is incidental here, and Loadouts already reports it). */
  const [led, backupList, startup] = await Promise.all([
    guard(() => Api.rpc('GetLEDSettings'), 'read the LED setting'),
    guard(() => Api.rpc('ListStoredDBBackups'), 'list database backups'),
    Api.rpc('GetStartupMasterTemplate').catch(() => null),
  ]);

  /* The bridge sets EmitUnpopulated, so a real device always sends
     blink_count -- including 0. `led.blink_count || null` would turn a
     perfectly good "off" into "unknown", which is a lie in the quiet
     direction. Check the type instead. */
  const blink = (led && typeof led.blink_count === 'number') ? led.blink_count : null;
  const backups = (backupList && backupList.msgArray) || [];
  const bootDefault = (startup && startup.msg) || null;

  /* ---------------------------------------------------------------- LED --- */

  const setBlink = async (n, label) => {
    const r = await guard(() => Api.rpc('SetLEDSettings', { blink_count: n }), 'set the LED');
    if (r !== undefined) { toast('LED set to ' + label + '.'); Views.system(); }
  };

  /* The last preset was labelled "Solid" and promised to hold the LED on. It
     is kept because 255 is a distinct stored value other tooling reads and
     sets, but it is labelled for what the service actually does with it. */
  const presets = [
    [0, 'Off', 'Stop the pattern.'],
    [1, '1 blink', null],
    [2, '2 blinks', null],
    [3, '3 blinks', null],
    [LED_SOLID_AT, 'Hold (' + LED_SOLID_AT + ')',
      'Stops blinking. Writes the same value Off does, so expect no visible change.'],
  ];

  main.append(h('div.card',
    h('h2.card-title', 'Status LED'),
    h('p.field-hint', { style: 'margin:-4px 0 16px; max-width:70ch' },
      'The green ACT LED beside the SD card slot, used as a signal. The service '
      + 'blinks it in a repeating pattern and the blink count is the only thing '
      + 'you set. It is the whole user interface on a build with no screen.'),

    h('div.figures', { style: 'margin-bottom:16px' },
      figure(blink === null ? '--' : String(blink), 'blink count'),
      h('div.figure',
        h('span.figure-n', ledPreviewDot(blink)),
        h('span.figure-l', 'pattern'))),

    dataTable(h('tbody',
      kvRow('Doing now', ledPattern(blink), ledTiming(blink)),
      kvRow('What it means',
        'whatever you decided',
        'Nothing in the service attaches a meaning to a count. It is a channel, '
        + 'not a vocabulary: a reflex sets it to 2 when a payload finishes '
        + 'because you wrote that reflex. Write the counts down somewhere, '
        + 'because the device will not remember them for you.'),
      kvRow('Range',
        '0 to ' + LED_SOLID_AT,
        '1 to ' + (LED_SOLID_AT - 1) + ' blink that many times per cycle, and those are '
        + 'the only settings that make the LED do anything you can watch. 0 is off. '
        + LED_SOLID_AT + ' and above stop blinking and write the same value 0 does, so '
        + 'they are indistinguishable from off on the device -- whatever the service\'s '
        + 'own "solid" comment intended.'),
      kvRow('Survives a reboot',
        'no',
        'The blink count is runtime state, not a stored setting -- there is no '
        + 'LED template and nothing writes it to the database. After a restart '
        + 'it is back to 0 unless something sets it again, which in practice '
        + 'means a reflex or a startup script.'))),

    h('div.btn-row', { style: 'margin-top:16px' },
      /* The preset that is already set carries BOTH aria-pressed and the
         primary style. aria-pressed alone would have been a screen-reader-only
         fact: the only aria-pressed rule in app.css is scoped to
         .theme-switch, so a sighted operator would see five identical
         buttons while a screen reader announced one of them as pressed. */
      ...presets.map(p => h('button', {
        class: 'btn btn-sm' + (blink === p[0] ? ' btn-primary' : ''),
        'aria-pressed': String(blink === p[0]),
        title: blink === p[0] ? 'Already set' : (p[2] || ''),
        onclick: () => setBlink(p[0], p[1].toLowerCase()),
      }, p[1])),
      h('button.btn.btn-sm.btn-quiet', {
        onclick: async () => {
          const v = await promptValue({
            title: 'Set the blink count',
            label: 'Blinks per cycle',
            value: blink === null ? '' : String(blink),
            placeholder: '2',
            hint: '0 turns it off. 1 to ' + (LED_SOLID_AT - 1) + ' blink that many times, '
              + 'then pause and repeat. ' + LED_SOLID_AT + ' stops blinking and writes '
              + 'the same value 0 does, so it looks like off.',
            confirmLabel: 'Set',
            validate: s => {
              if (!/^\d+$/.test(s)) return 'A whole number from 0 to ' + LED_SOLID_AT + '.';
              const n = Number(s);
              if (n > LED_SOLID_AT) {
                return 'Anything above ' + LED_SOLID_AT + ' behaves exactly like '
                  + LED_SOLID_AT + ' -- blinking stops. Use ' + LED_SOLID_AT + '.';
              }
              return null;
            },
          });
          if (v === null) return;
          const n = Number(v);
          await setBlink(n, ledPattern(n));
        },
      }, 'Set a count...')),

    h('p.field-hint', { style: 'margin:12px 0 0; max-width:70ch' },
      'The dot above is drawn from the service\'s own timings, not read back from '
      + 'the hardware. No RPC reports LED state, and on a board where the ACT LED '
      + 'has been reassigned by a dtoverlay the writes go nowhere and nothing '
      + 'reports it. If the pattern you set does not appear on the device, that '
      + 'is the likely reason. Separately: the service names the brightness value '
      + 'it writes for 0 and for ' + LED_SOLID_AT + ' "LED on" while the value '
      + 'itself is 0, which a stock Pi reads as dark. Treat a count of '
      + LED_SOLID_AT + ' as '
      + '"stop blinking", not as a light you can see from across a room.')));

  /* ----------------------------------------------------------- database --- */

  const backupNow = async () => {
    const name = await promptValue({
      title: 'Back up the configuration database',
      label: 'Backup name',
      placeholder: 'before-engagement',
      hint: 'Saved as a .db file in ' + DB_BACKUP_DIR + ' on the device. '
        + 'Letters, digits, dot, dash and underscore.',
      confirmLabel: 'Back up',
      validate: v => {
        if (!v) return 'Give the backup a name.';
        if (!/^[A-Za-z0-9._-]+$/.test(v)) return 'Letters, digits, dot, dash and underscore only.';
        /* init.db is the factory template set the image shipped with, and it
           is the file datastore.Open restores on a first boot
           (service/service.go:180). DBBackup opens the target with O_TRUNC,
           so letting someone name a backup "init" would silently overwrite
           the one database on the device you cannot get back. */
        if (dbFileName(v).toLowerCase() === DB_FACTORY_FILE) {
          return DB_FACTORY_FILE + ' is the factory template set this image shipped with. '
            + 'Writing over it would destroy the only copy. Pick another name.';
        }
        return null;
      },
    });
    if (!name) return;
    const file = dbFileName(name);

    if (backups.some(b => b.toLowerCase() === file.toLowerCase())) {
      const yes = await confirmAction({
        title: 'Replace "' + file + '"?',
        body: 'A backup of that name already exists on the device.',
        consequence: 'The existing file is overwritten and its contents are gone. '
          + 'There is no way to recover it from here.',
        confirmLabel: 'Replace it',
        danger: true,
      });
      if (!yes) return;
    }

    const r = await guard(() => Api.rpc('DBBackup', { msg: name }), 'back up the database');
    if (r !== undefined) { toast('Backed up as "' + file + '".'); Views.system(); }
  };

  const restore = async (file) => {
    const factory = file.toLowerCase() === DB_FACTORY_FILE;
    const yes = await confirmAction({
      title: 'Restore "' + file + '"?',
      body: factory
        ? 'This is the factory template set the image shipped with. Restoring it puts '
          + 'the template library back to what the device had on first boot.'
        : 'Every stored template on the device is replaced by the ones in this backup.',
      consequence: 'This is a replace, not a merge. Stored USB, WiFi, Bluetooth and '
        + 'network templates, stored reflex sets, stored loadouts and the boot-default '
        + 'loadout'
        + (bootDefault ? ' (currently "' + bootDefault + '")' : '')
        + ' are all cleared and rewritten from the backup. Anything you have stored '
        + 'since the backup was taken and have not stored elsewhere is lost. '
        + 'Your scripts, your password and this session are NOT in this database and '
        + 'are not touched. Nothing currently deployed changes -- the USB gadget, WiFi '
        + 'and networking keep running as they are until you deploy a loadout or reboot.',
      confirmLabel: factory ? 'Restore factory templates' : 'Restore and replace',
      danger: true,
    });
    if (!yes) return;
    const r = await guard(() => Api.rpc('DBRestore', { msg: file }), 'restore the database');
    if (r === undefined) return;
    toast('Restored from "' + file + '". The template library is replaced; nothing '
      + 'deployed has changed yet.');
    Views.system();
  };

  main.append(h('div.card',
    h('h2.card-title', 'Configuration database'),
    h('p.field-hint', { style: 'margin:-4px 0 16px; max-width:72ch' },
      'One database holds every stored template on this device: USB compositions, '
      + 'WiFi and Bluetooth settings, network interfaces, reflex sets, loadouts, and '
      + 'which loadout runs at boot. A backup is a copy of all of it under a name. '
      + 'A restore replaces all of it.'),

    dataTable(h('tbody',
      kvRow('Backups on device',
        String(backups.length),
        'Kept in ' + DB_BACKUP_DIR + '. Taken and restored from here; there is no RPC '
        + 'to delete one, so pruning them means a shell on the device.'),
      kvRow('Boot default',
        bootDefault || 'unknown',
        bootDefault
          ? 'The loadout applied when the service starts. It lives in this database, so '
            + 'a restore can change it under you.'
          : 'Could not be read, so whether a loadout runs at boot is unknown. Loadouts '
            + 'shows and sets this.'),
      kvRow('Not in this database',
        'scripts, password, sessions',
        'HIDScript and bash scripts are ordinary files on the device, and the console '
        + 'password and your session live in a separate file. A restore leaves all of '
        + 'them alone -- it cannot lock you out and it cannot undo a password change.'))),

    h('div.btn-row', { style: 'margin-top:16px' },
      h('button.btn.btn-primary', { onclick: backupNow }, 'Back up now')),

    h('h2.card-title', { style: 'margin-top:24px' }, 'Restore from a backup'),
    backups.length
      ? dataTable(
        h('thead', h('tr', h('th', 'Backup'), h('th', 'What it is'), h('th', 'Action'))),
        h('tbody', ...backups.map(file => h('tr',
          h('td.mono', file),
          h('td', file.toLowerCase() === DB_FACTORY_FILE
            ? 'factory templates, as shipped'
            : 'taken from this device'),
          h('td', h('button.btn.btn-sm.btn-danger', {
            onclick: () => restore(file),
          }, 'Restore'))))))
      : h('div.empty',
        'No backups on the device. Press "Back up now" before you change anything you '
        + 'would mind rebuilding -- it is the only undo this device has.'),
    backups.length
      ? h('p.field-hint', { style: 'margin:12px 0 0; max-width:72ch' },
        'A restore takes effect on the stored library immediately; it does not need a '
        + 'restart. It also does not re-deploy anything, so what is running stays '
        + 'running until you deploy a loadout or reboot.')
      : null));

  /* --------------------------------------------------- service round trip --- */

  const echoOut = h('div', { style: 'margin-top:14px' });

  main.append(h('div.card',
    h('h2.card-title', 'Is the service answering?'),
    h('p.field-hint', { style: 'margin:-4px 0 4px; max-width:72ch' },
      'Sends a short string to the device and checks that the same string comes back. '
      + 'It touches no subsystem and changes nothing, so it separates "the API is down" '
      + 'from "that one feature is broken" -- which otherwise look identical from here.'),
    h('div.btn-row', { style: 'margin-top:14px' },
      h('button.btn', {
        onclick: async (e) => {
          const btn = e.target;
          btn.disabled = true;
          clear(echoOut).append(h('p.field-hint', 'Waiting for the device...'));
          const probe = 'console-' + Date.now();
          const started = Date.now();
          const r = await guard(() => Api.rpc('EchoRequest', { msg: probe }), 'reach the service');
          const ms = Date.now() - started;
          btn.disabled = false;
          clear(echoOut);
          if (r === undefined) {
            /* guard() has already explained the failure in a toast. Leave a
               mark here too, because the toast will be gone in twelve
               seconds and this is the one card someone comes back to. */
            echoOut.append(banner('No reply. Whatever else on this page fails, it is '
              + 'not one subsystem -- the device is not answering the API at all.', 'danger'));
            return;
          }
          if (r.msg !== probe) {
            echoOut.append(banner('The device answered in ' + ms + ' ms but sent back '
              + 'something different from what was sent. Something between this console '
              + 'and the service is rewriting the request or the reply -- a proxy, most '
              + 'likely. Treat everything else on this console as unreliable until that '
              + 'is explained.', 'danger'));
            return;
          }
          echoOut.append(h('div.figures',
            figure(ms + ' ms', 'round trip'),
            figure('exact', 'reply match')));
          echoOut.append(h('p.field-hint', { style: 'margin:10px 0 0' },
            'The service is up and the request and reply both came through unaltered. '
            + 'This says nothing about USB, WiFi or Bluetooth -- each of those can be '
            + 'broken while this passes.'));
        },
      }, 'Send a test request')),
    echoOut));

  /* -------------------------------------------------------------- power --- */

  main.append(h('div.card',
    h('h2.card-title', 'Power'),
    h('p.field-hint', { style: 'margin:-4px 0 16px; max-width:72ch' },
      'Both of these end this session. Read the difference before you press one: a '
      + 'reboot comes back on its own and a shutdown does not.'),
    h('div.btn-row',
      h('button.btn.btn-danger', {
        onclick: async () => {
          const yes = await confirmAction({
            title: 'Reboot the device?',
            body: 'The service stops, the Pi restarts, and the boot-default loadout is applied again'
              + (bootDefault ? ' -- currently "' + bootDefault + '"' : '') + '.',
            consequence: 'Every connection drops, including this one. The device is '
              + 'unreachable for roughly 30 seconds. Anything you have deployed but not '
              + 'stored as a template is lost, and the LED blink count goes back to 0.',
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
            /* The card intro says both buttons end the session, but a modal
               covers the card -- a confirm has to stand on its own. */
            consequence: 'Every connection drops, including this one, and it does not '
              + 'come back: there is no remote way to switch the device on again. You '
              + 'have to physically unplug it and plug it back in. Do not do this to a '
              + 'device you cannot reach.',
            confirmLabel: 'Shut down',
            danger: true,
          });
          if (!yes) return;
          await guard(() => Api.rpc('Shutdown'), 'shut the device down');
          toast('Shutdown requested.');
        },
      }, 'Shut down')),
    h('p.field-hint', { style: 'margin:16px 0 0; max-width:72ch' },
      'Pulling the cable instead of shutting down is the normal way this device is '
      + 'switched off and it is survivable -- the datastore opens with truncation '
      + 'allowed precisely so an unclean stop does not leave a device that will not '
      + 'boot. What it can cost you is the last thing you stored.')));
};
