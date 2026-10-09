/* view-storage.js -- USB Mass Storage: the disk an attached host sees.
 *
 * WHY THIS VIEW EXISTS
 *
 * Cable composes the USB gadget and can turn the Storage function on. It does
 * not say WHAT disk the host then finds, and there was no way to choose one:
 * ListUmsImageFlashdrive, ListUmsImageCdrom and MountUMSFile had no caller
 * anywhere in this console. So an operator could enable mass storage and the
 * host would enumerate a drive with whatever happened to be in it -- usually
 * nothing -- with no screen in the product that could say so, let alone change
 * it.
 *
 * The original GopherJS client had half of this. web_client/hvueCompUSBSettings.go
 * defined a `ums-settings` modal that picked a name from these same two lists,
 * but it only edited GadgetSettings.UmsSettings.File ahead of a gadget deploy,
 * and its rpcClient.go never called MountUMSFile at all. Changing the disk
 * therefore meant re-composing the whole gadget: every USB function torn down
 * and rebuilt, including the USB ethernet link the console normally arrives
 * over.
 *
 * MountUMSFile does not do that. It writes one configfs attribute on the LUN
 * that is already running (service/SubSysUSB.go, MountUMSFile), so swapping
 * the disk is as disruptive to the host as swapping a USB stick, and not
 * disruptive to this console at all. That is the one thing this page is for.
 *
 * THREE FACTS THAT SHAPE EVERY CONTROL HERE
 *
 *   - The listing RPCs return BARE FILENAMES, and umsBackingPath() resolves a
 *     bare name against PATH_IMAGE_FLASHDRIVE or PATH_IMAGE_CDROM depending on
 *     the cdrom flag. The flag is not cosmetic: send an .iso with cdrom:false
 *     and the service looks for it among the flashdrives and fails. So every
 *     mount button here carries the flag of the folder its image was listed
 *     from, and there is no free-text filename field to get it wrong with.
 *
 *   - An EMPTY filename DETACHES the LUN. That is a real and useful
 *     operation, and it is also the one thing you must never do by accident,
 *     so it is a single clearly-labelled Eject button with its own
 *     confirmation -- never a text box someone could clear.
 *
 *   - MountUMSFile writes lun.0/file and nothing else. It does NOT touch
 *     lun.0/cdrom, so it cannot turn a flash drive into a CD-ROM. Only a
 *     gadget deploy does that, which is Cable's job and costs the connection.
 *     This page says so wherever the choice would otherwise look available,
 *     rather than offering a toggle that silently does less than it looks.
 */
'use strict';

/* The image folders, mirrored from common/globals.go -- PATH_IMAGE_FLASHDRIVE
 * and PATH_IMAGE_CDROM, both under PATH_ROOT = /usr/local/P4wnP1. They are
 * hardcoded for the same reason app.js hardcodes the HIDScripts path for
 * FSGetFileInfo: no RPC reports them, and the operator needs to be told where
 * to put a file. If the install root ever moves, the paths shown here go stale
 * and the per-image sizes stop resolving -- both visible, neither destructive. */
const UMS_FLASHDRIVE_DIR = '/usr/local/P4wnP1/ums/flashdrive';
const UMS_CDROM_DIR = '/usr/local/P4wnP1/ums/cdrom';

/* The extension filters are in the service, not here: ListUmsImageFlashdrive
 * passes ".img", ".bin" and ListUmsImageCdrom passes ".iso" to
 * ListFilesOfFolder. An image with any other suffix is on the device and will
 * never appear in either list, which is worth printing on the page -- it is
 * otherwise an invisible rule that looks like a missing file. */
const UMS_FLASHDRIVE_EXT = '.img or .bin';
const UMS_CDROM_EXT = '.iso';

/* Each size costs one FSGetFileInfo round trip. A handful is nothing; a folder
 * with a hundred images would make opening this page a burst of requests on a
 * single-core Pi, so it stops and the rows say the size was not checked rather
 * than implying the file is unreadable. */
const UMS_STAT_CAP = 24;

/* What is actually in the drive, or null.
 *
 * ParseGadgetState reads lun.0/file, trims a trailing NUL+newline and runs
 * filepath.Base() over the result. filepath.Base("") is ".", and configfs
 * returns a bare newline for a detached LUN, so an empty drive can arrive here
 * as "", ".", or "\n" depending on what the kernel wrote. None of those name
 * an image, and showing "." as the loaded disk would be a plausible-looking
 * lie. Anything unrecognisable is reported as empty, which is what it means. */
function umsAttachedName(ums) {
  if (!ums) return null;
  const raw = String(ums.file === null || ums.file === undefined ? '' : ums.file)
    .replace(/\0/g, '').trim();
  if (!raw || raw === '.' || raw === './' || raw === '/') return null;
  return raw;
}

/* fmtBytes in ui.js stops at MB, which is right for a HIDScript and wrong
 * here: disk images are routinely gigabytes, and "4096.0 MB" is a number you
 * have to do arithmetic on before it means anything. */
function umsBytes(n) {
  const v = Number(n);
  if (!isFinite(v) || v < 0) return null;
  if (v >= 1024 * 1024 * 1024) return (v / 1024 / 1024 / 1024).toFixed(1) + ' GB';
  return fmtBytes(v);
}

/* What an image is, as far as can honestly be said without reading it. The
 * extension and the folder are the only evidence available -- nothing here
 * opens the file or checks a filesystem signature, so this describes how the
 * device will treat it, not what is inside. */
function umsImageKind(name, cdrom) {
  const lower = String(name || '').toLowerCase();
  if (cdrom) return 'ISO 9660 disc image';
  if (lower.endsWith('.img')) return 'raw disk image';
  if (lower.endsWith('.bin')) return 'raw disk image';
  return 'disk image';
}

/* Sizes for the listed images, keyed by full path. Never throws: a size that
 * cannot be read is left out and the row says so, which is true and is not a
 * reason to fail the page.
 *
 * `tried` is returned alongside because "we asked and the device would not
 * say" and "we did not ask" are different claims, and a row that reports the
 * second as the first is a small lie about what the console knows. */
async function umsReadSizes(images) {
  const sizes = new Map();
  const tried = new Set();
  await Promise.all(images.slice(0, UMS_STAT_CAP).map(async (img) => {
    const path = img.dir + '/' + img.name;
    tried.add(path);
    try {
      const info = await Api.rpc('FSGetFileInfo', { path: path });
      if (info && info.size !== undefined && info.size !== null) {
        sizes.set(path, Number(info.size));
      }
    } catch (_) { /* size stays unknown; the row says so */ }
  }));
  return { sizes: sizes, tried: tried };
}

/* Put an image in the running drive. Returns true if the device accepted it.
 *
 * `cdrom` is the flag of the FOLDER the name was listed from, because that is
 * what umsBackingPath() resolves the bare name against. `deployed` is the
 * gadget's live lun.0/cdrom setting, which is a different thing and only used
 * to warn when the two disagree. */
async function umsMountImage(name, cdrom, deployed) {
  const consequence = [
    'The attached host sees the media change under it. Anything it has mounted from '
    + 'the image currently in the drive is pulled away without being unmounted, so '
    + 'unwritten data on the host side is lost and some hosts -- Windows especially -- '
    + 'need the drive re-scanned before they notice the new disk.',
  ];
  /* The mismatch warning. MountUMSFile cannot change the emulation mode, so
     this is a real and permanent property of the mount, not a setting the
     operator forgot to tick somewhere on this page. */
  if (deployed !== null && deployed !== cdrom) {
    consequence.push(deployed
      ? 'This gadget is deployed as a CD-ROM, so the host will treat this flash-drive '
        + 'image as read-only optical media and cannot write to it. Only re-composing '
        + 'the USB gadget on the Cable page changes that.'
      : 'This gadget is deployed as a writable flash drive, not a CD-ROM, so the host '
        + 'will see this ISO as a plain removable disk. Anything that waits for optical '
        + 'media -- autorun, an installer polling for a disc -- will not react. Only '
        + 're-composing the USB gadget on the Cable page changes that.');
  }

  const yes = await confirmAction({
    title: 'Put "' + name + '" in the drive?',
    body: 'The device points the mass-storage drive it is already running at this image. '
        + 'The USB gadget is not rebuilt, so no other function the cable presents is '
        + "disturbed and this console's own link is not touched.",
    consequence: consequence.join(' '),
    confirmLabel: 'Put it in the drive',
  });
  if (!yes) return false;

  const r = await guard(
    () => Api.rpc('MountUMSFile', { cdrom: cdrom, file: name }),
    'put "' + name + '" in the drive');
  if (r === undefined) return false;
  toast('"' + name + '" is in the drive.');
  return true;
}

/* Take the disk out and leave the drive empty.
 *
 * This is the empty-filename case, kept apart from mounting on purpose. The
 * cdrom flag is sent as the deployed one for honesty; it has no effect here,
 * because umsBackingPath() passes an empty name straight through without
 * looking at a folder. */
async function umsEjectImage(deployed) {
  const yes = await confirmAction({
    title: 'Eject the disk?',
    body: 'The device clears the backing file on the running mass-storage drive. The '
        + 'drive stays present and reports no media, like a card reader with no card.',
    consequence: 'The attached host loses the disk immediately, with no unmount. Anything '
        + 'it had open on that image fails. Nothing is deleted from this device -- the '
        + 'image file stays in its folder and can be put back from the lists below.',
    confirmLabel: 'Eject',
    danger: true,
  });
  if (!yes) return false;

  const r = await guard(
    () => Api.rpc('MountUMSFile', { cdrom: deployed === true, file: '' }),
    'eject the disk');
  if (r === undefined) return false;
  toast('The drive is empty.');
  return true;
}

/* One image library: a folder, what it accepts, and what is in it. */
function umsLibraryCard(opts) {
  const rows = opts.images.map((img) => {
    const path = img.dir + '/' + img.name;
    const size = opts.sizes.has(path) ? umsBytes(opts.sizes.get(path)) : null;
    const isLoaded = opts.attached !== null && opts.attached === img.name;

    let action;
    if (isLoaded) {
      action = h('span.pill', h('span.dot.dot-ok'), 'in the drive');
    } else if (!opts.canMount) {
      action = h('span.field-hint', 'needs the Storage function');
    } else {
      action = h('button.btn.btn-sm.btn-primary', {
        type: 'button',
        onclick: async (e) => {
          e.target.disabled = true;
          const done = await umsMountImage(img.name, img.cdrom, opts.deployed);
          e.target.disabled = false;
          /* Only repaint if this is still the page on screen. The confirmation
             dialog tears itself down on navigation and resolves false, so the
             RPC cannot start after a move -- but it can finish after one, and
             painting Storage over whatever the operator moved to would be a
             worse surprise than a stale row. */
          if (done && State.view === 'storage') Views.storage();
        },
      }, 'Put in drive');
    }

    return h('tr',
      h('td.mono', img.name),
      h('td',
        h('span', umsImageKind(img.name, img.cdrom)),
        h('div.field-hint', size !== null ? size
          : opts.tried.has(path) ? 'size unknown' : 'size not checked')),
      h('td', action));
  });

  return h('div.card',
    h('h2.card-title', opts.title),
    h('p.field-hint', { style: 'margin:-4px 0 14px' }, opts.note),
    opts.error
      ? h('div.banner.banner-danger',
        h('p.banner-title', 'This folder could not be listed'),
        h('p', opts.error),
        h('p', 'The folder is expected at ' + opts.dir + '. Either the device could not '
             + 'be reached just now, or the folder is not there -- the install copies it '
             + 'from dist/ums. Either way the console cannot say what images exist.'))
      : rows.length
        ? dataTable(
          h('thead', h('tr',
            h('th', 'Image'), h('th', 'What it is'), h('th', ''))),
          h('tbody', ...rows))
        : [
          h('div.empty', opts.emptyLine),
          h('p.field-hint', { style: 'margin-top:12px' },
            'Put a file in ', h('span.mono', opts.dir), ' whose name ends ', opts.ext,
            '. The console cannot upload it: the file-write API only reaches the script '
            + 'folders and /tmp, so the image has to arrive over SSH -- scp it across the '
            + 'USB ethernet or WiFi link -- or be written to the SD card directly.'),
        ]);
}

/* --- the view ---------------------------------------------------------- */

Views.storage = async function () {
  const main = clear($('#view'));
  if (!main) return;
  main.append(pageHead('Storage',
    'The disk this device shows an attached host: what is in the drive now, and how to '
    + 'swap it without pulling the cable.'));

  /* The gadget read is guarded -- it is the one call whose failure means the
     page cannot say anything true, and a 401 here is what signs the operator
     back in, which covers the two list calls alongside it.

     The lists are NOT guarded. A missing image folder is a fact about this
     device, and reporting it as a toast means it scrolls away and is repeated
     on every visit; reported in the card it belongs to, it stays next to the
     thing it explains. Neither call can reject out of this function. */
  const readList = async (call) => {
    try {
      const r = await call();
      return { names: (r && r.msgArray) || [] };
    } catch (e) {
      return { names: [], error: (e && e.message) ? String(e.message) : 'unknown error' };
    }
  };

  const [gadget, flash, cd] = await Promise.all([
    guard(() => Api.rpc('GetDeployedGadgetSetting'), 'read the USB gadget'),
    readList(() => Api.rpc('ListUmsImageFlashdrive')),
    readList(() => Api.rpc('ListUmsImageCdrom')),
  ]);

  /* Only on success: clobbering a good reading with undefined would make the
     Overview cable strip claim the composition is unknown when it is not. */
  if (gadget) State.usb = gadget;

  const ums = gadget ? (gadget.ums_settings || null) : null;
  /* null, not false, when the gadget could not be read. "Not enabled" and
     "not known" lead to different advice and must not be collapsed. */
  const umsOn = gadget ? !!gadget.use_UMS : null;
  const deployedCdrom = gadget && umsOn ? !!(ums && ums.cdrom) : null;
  const attached = umsOn ? umsAttachedName(ums) : null;

  const images = [
    ...flash.names.map(n => ({ name: n, cdrom: false, dir: UMS_FLASHDRIVE_DIR })),
    ...cd.names.map(n => ({ name: n, cdrom: true, dir: UMS_CDROM_DIR })),
  ];
  const stats = await umsReadSizes(images);

  /* "0 images on device" is a CLAIM, and a folder that could not be listed
     does not support it. readList() swallows a failed listing into an empty
     name array so the error can be reported in the card it belongs to -- but
     images.length then reads 0, and the figure strip was stating that 0 at
     hero size directly above two cards each saying the folder could not be
     listed. The operator most likely to see it is the one whose link just
     dropped, which on this device is the NORMAL failure: deploying a USB or
     WiFi template reconfigures the very interface the console arrived over.
     "0 images on device" tells them their images are gone. Unknown is the
     only thing the console actually knows here, and "--" is how every other
     figure on this page says it. */
  const imageCount = (flash.error || cd.error) ? '--' : String(images.length);

  /* The same base name in both folders is indistinguishable from here: the
     device reports lun.0/file through filepath.Base(), so the folder is gone
     by the time it arrives. Say so rather than marking the wrong row with
     confidence. */
  const ambiguous = attached !== null
    && flash.names.indexOf(attached) !== -1
    && cd.names.indexOf(attached) !== -1;

  /* --- what is in the drive right now --------------------------------- */

  const modeText = deployedCdrom === null ? 'unknown'
    : deployedCdrom ? 'CD-ROM' : 'flash drive';

  const nowRows = [
    kvRow('Mass storage function',
      umsOn === null ? 'unknown' : umsOn ? 'enabled' : 'not enabled',
      umsOn === null
        ? 'The deployed USB composition could not be read, so nothing on this page is '
          + 'confirmed.'
        : umsOn
          ? 'The host enumerates a mass-storage device. It costs 2 of the 7 USB '
            + 'endpoints a Pi Zero W has.'
          : 'The host sees no disk at all, and attaching an image would fail -- there is '
            + 'no running drive to put it in.'),
    kvRow('Image in the drive',
      umsOn === null ? 'unknown' : attached === null ? 'none' : attached,
      umsOn === null
        ? 'Not readable without the gadget settings.'
        : umsOn === false
          ? 'Nothing is loaded, because there is no drive.'
          : attached === null
            ? 'The drive is present and empty. The host sees a removable drive with no '
              + 'media in it, which is also what a failed mount looks like.'
            : ambiguous
              ? 'A file of this name exists in both image folders. The device reports the '
                + 'base name only, so the console cannot tell which one is loaded.'
              : 'Reported by the device as the backing file of the running drive.'),
    kvRow('Emulated as', modeText,
      deployedCdrom === null
        ? 'Only meaningful once mass storage is enabled.'
        : deployedCdrom
          ? 'The host treats the image as read-only optical media. Changing this to a '
            + 'writable flash drive means re-composing the USB gadget on the Cable page, '
            + 'which drops every USB function.'
          : 'The host treats the image as a writable removable disk and can change it. '
            + 'Changing this to a CD-ROM means re-composing the USB gadget on the Cable '
            + 'page, which drops every USB function.'),
    kvRow('USB gadget',
      gadget === undefined || gadget === null ? 'unknown'
        : gadget.enabled === false ? 'disabled' : 'enabled',
      gadget && gadget.enabled === false
        ? 'The composite gadget is disabled, so the host sees nothing at all -- including '
          + 'this drive, however it is configured.'
        : null),
    kvRow('Flash-drive images', UMS_FLASHDRIVE_DIR,
      'Listed only if the name ends ' + UMS_FLASHDRIVE_EXT + '.'),
    kvRow('CD-ROM images', UMS_CDROM_DIR,
      'Listed only if the name ends ' + UMS_CDROM_EXT + '.'),
  ];

  const nowCard = h('div.card',
    h('h2.card-title', 'In the drive right now'),
    /* Three short words, not the filename: a figure is set at hero size and a
       disk image name is routinely long enough to break the row. The name
       itself is in the table underneath, where it can wrap. */
    h('div.figures',
      figure(umsOn === null ? '--' : umsOn ? 'on' : 'off', 'mass storage'),
      figure(umsOn !== true ? '--' : attached === null ? 'empty' : 'loaded', 'the drive'),
      figure(imageCount, 'images on device')),
    dataTable({ style: 'margin-top:24px' }, h('tbody', ...nowRows)));
  main.append(nowCard);

  /* --- the blockers, stated before anything is offered ----------------- */

  if (gadget === undefined || gadget === null) {
    main.append(h('div.card',
      h('div.banner.banner-danger',
        h('p.banner-title', 'The USB gadget could not be read'),
        h('p', 'Without it there is no way to know whether a drive is running or what is '
             + 'in it, so nothing on this page is offered. If USB gadget mode is not '
             + 'active at all, no USB function can work -- check that config.txt contains '
             + '"dtoverlay=dwc2" and cmdline.txt contains "modules-load=dwc2".'))));
  } else if (umsOn === false) {
    main.append(h('div.card',
      h('div.banner',
        h('p.banner-title', 'Mass storage is not part of the deployed composition'),
        h('p', 'There is no drive to put a disk in, so the images below are listed but '
             + 'cannot be attached. Turn on Storage on the Cable page and deploy. It '
             + 'costs 2 of the 7 USB endpoints this board has, so you may have to turn '
             + 'off RNDIS or CDC ECM first -- and deploying re-composes the gadget, '
             + 'which drops the USB ethernet link if that is how you are reading this.')),
      h('div.btn-row', { style: 'margin-top:14px' },
        h('button.btn.btn-primary', { type: 'button', onclick: () => go('cable') },
          'Open Cable'))));
  }

  /* --- eject, kept apart from mounting -------------------------------- */

  if (umsOn === true) {
    main.append(h('div.card',
      h('h2.card-title', 'Eject'),
      attached === null
        ? h('p.field-hint',
          'The drive is already empty. Pick an image below to put one in.')
        : [
          h('p.field-hint', { style: 'margin:-4px 0 14px' },
            'Takes ', h('span.mono', attached), ' out and leaves the drive present but '
            + 'empty. The image file is not deleted.'),
          h('div.btn-row',
            h('button.btn.btn-danger', {
              type: 'button',
              onclick: async (e) => {
                e.target.disabled = true;
                const done = await umsEjectImage(deployedCdrom);
                e.target.disabled = false;
                if (done && State.view === 'storage') Views.storage();
              },
            }, 'Eject the disk')),
        ]));
  }

  /* --- the two libraries ----------------------------------------------- */

  main.append(umsLibraryCard({
    title: 'Flash-drive images',
    note: 'A writable removable disk. The host can read and write it, and whatever it '
        + 'writes stays in the image file on this device.',
    dir: UMS_FLASHDRIVE_DIR,
    ext: UMS_FLASHDRIVE_EXT,
    images: images.filter(i => !i.cdrom),
    sizes: stats.sizes,
    tried: stats.tried,
    attached: attached,
    canMount: umsOn === true,
    deployed: deployedCdrom,
    error: flash.error || null,
    emptyLine: 'No flash-drive images on this device.',
  }));

  main.append(umsLibraryCard({
    title: 'CD-ROM images',
    note: 'An optical disc. The host cannot write to it, and software that expects a '
        + 'CD -- autorun, an installer waiting for media -- reacts to it as one, but '
        + 'only if the gadget is deployed as a CD-ROM.',
    dir: UMS_CDROM_DIR,
    ext: UMS_CDROM_EXT,
    images: images.filter(i => i.cdrom),
    sizes: stats.sizes,
    tried: stats.tried,
    attached: attached,
    canMount: umsOn === true,
    deployed: deployedCdrom,
    error: cd.error || null,
    emptyLine: 'No CD-ROM images on this device.',
  }));
};
