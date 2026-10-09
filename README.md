<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="docs/img/banner-dark.svg">
    <img src="docs/img/banner-light.svg" alt="P4wnP1 A.L.O.A. — a little offensive appliance" width="100%">
  </picture>
</p>

<p align="center">
  <strong>A Raspberry Pi that becomes whatever USB device the job needs.</strong><br>
  Keyboard, mouse, network adapter, mass storage, serial — alone or all at once,<br>
  reconfigurable from a browser, scriptable, and able to act on its own once the cable is in.
</p>

<p align="center">
  <a href="https://github.com/at0m-b0mb/P4wnP1_aloa/releases/latest"><strong>Download an image</strong></a>
  &nbsp;·&nbsp;
  <a href="#the-console">The console</a>
  &nbsp;·&nbsp;
  <a href="#building">Build it yourself</a>
  &nbsp;·&nbsp;
  <a href="#what-is-not-verified">What is not verified</a>
</p>

---

> **Authorized testing only.** This is a red-team tool. Point it only at systems you own or
> have written permission to test. See [DISCLAIMER.md](DISCLAIMER.md).

> **Hardware-verification status.** The images below are built from official Raspberry Pi OS
> releases and automatically verified before publishing — but **they have never been booted on
> a physical Pi.** Read [What is not verified](#what-is-not-verified) before you rely on this.

---

## Download

Built from `v0.4.0`, on **Raspberry Pi OS Lite (Debian 13 "trixie"), 2026-09-15**, kernel
`6.18.50+rpt-rpi`.

> **If you are running v0.3.0 or earlier, replace it.** Every image up to and including
> v0.3.0 shipped the same SSH password (`p4wnp1:p4wnp1`) with passwordless sudo, so anyone
> who could reach the device — including the host it was plugged into — could take root on
> it. See [KNOWN_ISSUES.md](KNOWN_ISSUES.md). v0.3.1 ships no usable account at all.

| Image | Size | Boards |
|---|---|---|
| `P4wnP1-ALOA-v0.4.0-armhf.img.xz` | 656M | Pi Zero, **Pi Zero W**, Pi 1 |
| `P4wnP1-ALOA-v0.4.0-arm64.img.xz` | 608M | **Pi Zero 2 W**, Pi 3, Pi 4, Pi 5 |

```
armhf  sha256  b38240a35b56afebccc193cda4859056f43a2982140f72077a84e8ca69875e24
arm64  sha256  e10f76236d87d53903f736c6336fe370b74110e01b21075405b56a59bae2a355
```

Verify, flash, and boot with the cable in the **data** port (the inner one on a Zero):

```bash
sha256sum -c P4wnP1-ALOA-v0.4.0-armhf.img.xz.sha256
xz -d P4wnP1-ALOA-v0.4.0-armhf.img.xz
sudo dd if=P4wnP1-ALOA-v0.4.0-armhf.img of=/dev/sdX bs=4M conv=fsync status=progress
```

**Before you flash**, set a username and password in Raspberry Pi Imager ("Set username and
password" under the gear icon). The image ships with **no usable account**, so this is how you
get in. If you skip it, boot the device once, put the card back in your laptop, and read
`p4wnp1-credentials.txt` on the boot partition — first boot generates a password for this one
device and writes it there.

Then, over the USB ethernet link the device brings up:

```bash
ssh p4wnp1@172.16.0.1                      # or the account you set at flash time
sudo cat /root/INITIAL_CREDENTIALS.txt     # console password and WiFi PSK for THIS device
```

Open **<http://172.16.0.1:8000>**.

Nothing is shared between devices: there is no password in the image at all, host keys are
generated per device, and the console password and WiFi PSK are random per device. That is why
the first login has to come over USB or serial — the WiFi key is not knowable until you have
read it off the device.

Earlier images shipped `p4wnp1:p4wnp1`. If you flashed one of those, change that password: the
forced change at first login did not protect it, because whoever logs in first is the one who
answers the prompt.

### The cable presents no keyboard until you ask it to

A freshly flashed device composes **ethernet only** — RNDIS and CDC ECM, so you can reach it —
and **no keyboard, no mouse, no mass storage**. Payloads will refuse to run until you turn one on.

That is deliberate, and it is the behaviour to keep. This is a device whose entire purpose is to
be plugged into someone else's computer. A host that enumerates a *keyboard* the instant the
cable goes in is a host that has already accepted an input device from you, before you have
decided to use it that way — in front of whoever is standing there, and in whatever logs that
machine keeps. Composing the keyboard is a decision, so the device makes you make it.

Turning it on takes seconds, from either console:

| | |
|---|---|
| **OLED panel** | `Cable` → tick **Keyboard** → **KEY1** → **Yes** |
| **Web console** | `Cable` → tick **Keyboard** → **Deploy** |

The USB link re-enumerates and comes back about three seconds later with the keyboard present.

Because an opt-in default is only a good one if the device explains it at the moment you meet
it, the panel does:

* the **Payloads** list shows `no keyboard: see Cable` on its hint line whenever nothing is
  listening;
* choosing a payload there **refuses and names the remedy** — `Open Cable, tick Keyboard, KEY1
  to deploy` — instead of letting the service answer `HIDScript not available (mouse and
  keyboard disabled)`, which is true and tells an operator holding the device nothing;
* the remedy is the **first thing on the screen**, not below the fold. The panel has six body
  rows, and an instruction on row seven is an instruction nobody reads. There is a test for
  exactly that, because the first version got it wrong.

The web console does the same thing in its own idiom: a banner on **Keystrokes** when no HID
function is deployed, with a button that takes you to Cable.

### First boot takes a few minutes. Do not pull the power.

On a Pi Zero W the first boot after flashing takes **two to five minutes**, and for most of it
the device looks idle: no network yet, no obvious activity. It is not idle. It is growing the
root filesystem to fill the card, generating this device's own three SSH host keys on a single
1&nbsp;GHz core, creating the web console administrator, and adopting any `authorized_keys` you
left on the boot partition.

Pulling the power in that window is the one genuinely destructive thing available. You can end
up with a half-written credentials file, host keys that were generated but never installed, or
an interrupted resize — and the symptoms turn up much later, looking like something else.

So the device tells you, three ways, and you only need one of them:

| | while setting up | when finished |
|---|---|---|
| **OLED panel** | `DO NOT POWER OFF` in double-height type, with an elapsed-time bar | `SETUP COMPLETE / READY / Safe to power off` |
| **Green ACT LED** | steady heartbeat, about four blinks a second | back to ordinary flickering on card access |
| **The card itself** | `DO-NOT-POWER-OFF.txt` on the boot partition | replaced by `SETUP-COMPLETE.txt` |

The LED and the two files are on **every** image, with or without a screen — a plain build has
no panel to read, which is exactly when a blinking LED earns its keep.

The file pair is deliberately a pair rather than a flag inside one file. If you ever take a card
out of a device and find `DO-NOT-POWER-OFF.txt` still on it, that tells you something true and
useful: **setup never finished on that card**, and it should be reflashed. The completion file
also records when it finished, and how to reach the device given what you did or did not leave
on the partition.

Nothing claims success it has not got. If first boot fails partway, the warning file **stays**
and no completion file is written — checked by reintroducing a mid-script failure and asserting
exactly that.

---

## The OLED screen

<p align="center">
  <img src="docs/img/oled-screens.png" alt="Every screen of the OLED interface" width="760">
</p>

Fit a **Waveshare 1.3inch OLED HAT** and the device gains a second way to be driven: the screen
and its five-way stick, with no laptop and no network. Not a status display — the same control
surface as the console. Loadouts, the USB composition, payloads, running jobs, radio and
network configs, reflex sets, backups, LED, reboot and shutdown.

```
up / down     move            KEY1  the action named on the bottom line
right, press  enter / confirm KEY2  refresh
left          back            KEY3  home
```

The bottom line always says what KEY1 does on that screen. Eight unlabelled controls and no
manual within reach is otherwise a thing you have to memorise, and a thing you have to memorise
is a thing you stop using.

It authenticates with the machine-local credential the service writes to
`/run/p4wnp1/local.token`, so there is nothing to configure: it is a local script like any
other, holding an ordinary session that expires and can be revoked. The image enables SPI and
installs the unit; a board with **no** HAT fitted boots exactly as before, and the daemon says
so once in the journal and exits.

**Try it without any hardware at all:**

```bash
make oled-sim
```

That runs the whole interface in a browser against a fake device — the same UI, fonts, menus
and state machine the hardware runs, with a PNG for the panel and buttons for the stick. Point
`--url` at a real device to drive that instead. `make oled-shots` regenerates the sheet above.

Two details worth knowing if you are wiring your own panel. The HAT is **SH1106**, not SSD1306:
its RAM is 132 columns wide with the 128-pixel panel centred, so the first visible column is 2,
and driving it as an SSD1306 shifts every frame two pixels and wraps it. The charge-pump
command differs too, and sending the wrong one leaves the panel initialised but **unlit**,
which is indistinguishable from a wiring fault. Both controllers and both buses are supported;
`--controller ssd1306` and `--bus i2c` are there when you need them.

---

## The console

<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="docs/img/console-cable-dark.jpg">
    <img src="docs/img/console-cable-light.jpg" alt="The Cable view: a live strip of the USB functions the target host enumerates" width="100%">
  </picture>
</p>

The first thing it shows you is **what the cable presents** — a live picture of the USB functions
the machine on the other end actually enumerates, each segment carrying its device path or host
address, with the attach state driven by real gadget events.

| View | What it is |
|---|---|
| **Overview** | What the device is presenting and doing right now, and a plain-language primer if you are new to it |
| **Cable** | USB composition and the identity the device claims — vendor, product, serial |
| **Radio** | WiFi state, the Bluetooth controller, and the interface table |
| **Keystrokes** | HIDScript editor and runner, with the full function reference on the page |
| **Reflexes** | Build, arm and delete the trigger/action rules the device runs by itself |
| **Loadouts** | Whole-device configurations, backup, reboot, shutdown |
| **Journal** | Everything the device reports, live |

<p align="center">
  <img src="docs/img/console-reflexes-dark.jpg" alt="The Reflexes view, listing trigger and action pairs" width="49%">
  <img src="docs/img/console-journal-dark.jpg" alt="The Journal view, streaming device events live" width="49%">
</p>

It is built to be learnable by someone who has never used a P4wnP1. The
Overview says in plain words what the device is and what it is doing; every USB
function explains what the host will see; the HIDScript reference lists all 24
functions on the page you write scripts on, because they are injected into a
runtime VM and appear in no file you can open; and failures are translated into
what went wrong and what to do about it, with a button to the view that fixes
it, rather than showing the service's own wording.

**196KB total, no framework and no build step** — 120KB of that is two variable
webfonts, served from the device because this appliance is routinely operated with no internet
route at all. The smallest supported target is a Pi Zero W: one 1GHz ARM11 core and 512MB of
RAM. Light and dark themes, and all 18 text and UI colour pairings are checked against WCAG AA
in both by `make contrast`.

---

## What it does

| | |
|---|---|
| **USB functions** | HID keyboard, HID mouse, raw HID, RNDIS, CDC ECM, mass storage (disk or CD-ROM), serial — individually or as one composite device, reconfigurable at runtime |
| **HIDScript** | 24 functions exposed into a JavaScript VM running on the device; 15 keyboard layouts; human-cadence typing; absolute mouse positioning; LED-state branching; up to 8 parallel jobs |
| **DuckyScript** | Converts to HIDScript, 66 key names recognised |
| **Triggers** | 9 event types — USB host attach/detach, AP started, joined WiFi, SSH login, DHCP lease, GPIO, group values |
| **Control** | 82 RPCs over gRPC and JSON, plus a live event stream |

Existing DuckyScript payloads run here:

```bash
P4wnP1_cli ducky convert payload.txt -o payload.js --layout gb --speed 80 --jitter 20
```

DuckyScript 1.0 converts in full. Later Hak5 dialects and Bash Bunny directives are reported as
warnings **and** left in the output as `// UNCONVERTED:` comments — a payload that converts
cleanly while silently losing a third of its logic is worse than one that refuses.

---

## Hardware

| Board | Image | Status |
|---|---|---|
| **Pi Zero W** | `armhf` | The classic target. The only board with a Nexmon-patched firmware, so the only one where KARMA and multi-SSID can work. |
| **Pi Zero** | `armhf` | No WiFi or Bluetooth. USB surface only. |
| **Pi Zero 2 W** | `arm64` | USB, normal AP and Bluetooth all build. No Nexmon for its BCM43436, so no KARMA. |
| **Pi 3 / 4 / 5** | `arm64` | On a Pi 4/5 the USB-C port is the one that does peripheral mode. |

> Upstream and earlier versions of this README said the Pi Zero 2 W was unsupported and blamed
> the WiFi chipset. That was not the reason. The core service was gated to 32-bit ARM by build
> tags (`+build linux,arm`) that turned out to be incidental rather than a genuine dependency —
> removing them builds arm64 with no code changes. The chipset only limits KARMA, not the device.

You also need a microSD card (8GB minimum), a USB cable or OTG adapter, and ideally an external
5V supply so the Pi can stay powered while detached from the target.

---

## Building

Everything cross-compiles from macOS or Linux. No Pi required to build.

```bash
make build-armv6     # binaries for Pi Zero / Zero W
make build-arm64     # binaries for Pi Zero 2 W / 3 / 4 / 5
make image           # flashable .img.xz: both architectures x both variants
make image-plain     # only the images WITHOUT OLED support
make image-oled      # only the images WITH the OLED console
make test            # Go unit tests
make verify          # every gate below, in order, cheapest failure first
make smoke           # run the service in a container, end to end (28 checks)
make feature-test    # call all 83 RPCs against the real binary (85 checks)
make access-control  # attack the running service (60 checks, all must FAIL)
make oled-sim        # drive the OLED interface in a browser, no hardware needed
make oled-shots      # render every OLED screen to one sheet
make check-quoting   # values install.sh writes must survive being sourced
make check-render    # render every console view in jsdom (11 checks)
make check-rpc       # console RPC payloads vs the .proto
make check-js        # parse the console JavaScript
make contrast        # WCAG check on the console palette (21 pairings x 2 themes)
make mock            # serve the console against a mock device, no Pi needed
```

To use the console yourself without a Pi, `./tools/live-console.sh` serves it from the **real
service binary** in a container on `http://127.0.0.1:8000/app/`. Hardware-backed features report
that they are unavailable, which is correct there; everything else is live. Prefer it to
`make mock`: the mock has twice disagreed with the service and hidden a bug until it shipped.

Image building is documented in **[image/README.md](image/README.md)**. Every built image is
**mounted and verified before it is published**: MBR signature, both partitions, that the
partition table fits inside the file, that both filesystems mount, the payload and the console,
that the units are enabled, that `config.txt` carries the dwc2 overlay and `cmdline.txt` loads it
as a single line, that `root=PARTUUID` still matches the disk identifier, that no SSH host keys
were baked in, and that the binaries are the right architecture. A failure refuses to publish.

That check exists because a build once produced 2.6GB of zeros, compressed it to 410KB, wrote a
checksum for it and reported success.

Installing onto a Pi you already have:

```bash
sudo ./install.sh --ssid MyAP --wifi-country GB
```

---

## Security posture

This is a tool for attacking systems, which makes its own security worth stating plainly.

- **The API authenticates.** Every gRPC method and every JSON endpoint requires a bearer token.
  Tokens are opaque, random, expire on a sliding window, and can be revoked.
- **Per-device secrets, including the SSH login.** Nothing meaningful is shared between two
  flashed devices, and the access point refuses to broadcast on a PSK published in this
  repository. The image ships with **no usable account at all** -- every password is generated
  on the device at first boot, and the image build refuses to publish an image in which any
  account has a password. Set your own at flash time (Raspberry Pi Imager's "Set username and
  password") and the device uses that; otherwise first boot writes a per-device password to
  `p4wnp1-credentials.txt` on the boot partition, where you can read it by putting the card back
  in your laptop. Log in, change it, delete the file -- the file says so itself.
- **Path handling is allowlisted**, and reads are bounds-checked. The allowlist
  resolves symlinks rather than only cleaning the string, and the file opens use
  `O_NOFOLLOW`. Both are needed: before this, a local user could leave a symlink
  in `/tmp` and have the root service write a cron job through it. The kernel's
  `fs.protected_symlinks` does not cover that case -- it only guards symlinks
  sitting directly in a sticky directory, not one level down.
- **The device is reached by IP, and says so.** A `Host` naming this device by
  a public DNS name is refused, because the origin check compares `Origin`
  against `Host` and an attacker who controls a domain controls both. An IP
  literal cannot be rebound, so the legitimate routes are unaffected.
- **Brute force is rate-limited in a way that survives parallelism.** Rejected
  logins serialise; the one-second delay used to run per-goroutine, so twenty
  simultaneous guesses cost one second rather than twenty.
- **You can see who is signed in**, and end one session without ending them
  all. The list carries no tokens, only one-way identifiers.
- **Secrets stay off command lines and out of `/tmp`.** Anything on a command
  line is in `/proc`, which every local account can read.
- **The console is same-origin only.** It emits no CORS headers and rejects foreign origins,
  because this device is often reached from a browser that is simultaneously visiting untrusted
  pages.
- **Payload text cannot become code.** The DuckyScript converter escapes its output so a crafted
  payload cannot close the generated JavaScript literal and run as root.
- **The device's own scripts hold an ordinary credential, not a back door.** `servicestart.sh`
  and your trigger actions drive the box through `P4wnP1_cli`, so they need to authenticate. At
  startup the service logs in as itself and writes that session token to
  `/run/p4wnp1/local.token` — mode `0600`, on a tmpfs, gone at power-off. It is the same kind of
  token `auth login` returns: it appears in the session list, expires on the normal schedule, and
  revoking all sessions revokes it too. There is no bypass in the token validator and no second
  credential type. Root on the device can already read the password hashes and every stored WiFi
  key, so a root-only file grants root nothing it could not already take.

**Still open:** there is no TLS. The console is served over plain HTTP, so a bearer token rides
in cleartext over whatever link you reach it on. On the device's own WPA2 access point or a USB
cable that is survivable; over anything else it is not. Treat this as a device you reach
directly, not one you expose.

---

## What is not verified

Being specific about this matters more than the feature list.

**Tested on hardware: a Raspberry Pi Zero W with a Waveshare 1.3inch OLED HAT.**
Everything below that says "verified" without qualification was verified in a container; the
section immediately after this one says exactly what a real board was shown to do, and what it
was not.

### What a real board was shown to do

A Pi Zero W, flashed from the published `-oled-armhf` image and plugged into a laptop by USB.
`tools/hardware-check.sh` talks to it over the USB ethernet link and reports **28 of 28**. It
earned that number the hard way: on the run before, it was 27, and the one failure was real --
the CLI printed the WiFi pre-shared key with `%+v`, `servicestart.sh` calls that CLI, and the
service captures the script's stdout into the journal. The check greps the journal for the
*actual secret* rather than for the word "psk", which is why it found it.

```
the link        ping; the device's own DHCP server handed this laptop 172.16.0.2
the console     served on :8000
access control  GetDeployedGadgetSetting, HIDRunScript, Reboot and DBBackup all 401 without a
                token; a junk bearer 401; a cross-origin login 403; a rebound Host header 403;
                a wrong password costs 3-4 seconds, every time, however you ask
the API         gadget composed CDC_ECM + RNDIS + HID_KEYBOARD + HID_MOUSE
                wlan0 172.24.0.1, bteth 172.26.0.1, usbeth 172.16.0.1
                radio AP_UP on channel 6 -- the access point is on air
                seven stored payloads listed; HIDGetRunningScriptJobs returns `ids`
the engine      a HIDScript written to /tmp, run, and its result read back
on the device   P4wnP1, ssh and p4wnp1-oled all active
                /run/p4wnp1/local.token is 600 root
                the AP PSK appears nowhere in the journal
                GPIO 5,6,13,16,19,20,21 all `ip -- | hi` -- the HAT's eight controls are
                inputs sitting idle, and nothing else on the board is holding them
```

The OLED console was driven by hand on the panel, and then driven **remotely over HTTP** --
`GET /api/v1/panel.txt` returns the live screen as text, `panel.png` as a 128x64 image, and
`POST /api/v1/panel/press` delivers a button to the same handler a physical press reaches. The
Cable screen was navigated from a laptop, two USB functions ticked, and the change deployed; the
link re-enumerated and came back with the keyboard present in three seconds.

That last sequence is also the proof of a fix. Until v0.4.2 the Cable screen could not do it:
`GetDeployedGadgetSetting` reports configfs as it is now, so on a torn-down gadget it returns
`enabled:false`, and the read-modify-write sent that straight back -- ticking every function and
deploying built another *disabled* gadget and reported success. The one screen whose job is to
revive a dead USB link could not, and said nothing about why.

Running that check on a real board is also what found the three bugs v0.4.1 fixes, none of
which any amount of green CI had noticed:

- **every payload failed.** `ListStoredHIDScripts` returns bare names; `HIDRunScript` demands an
  absolute path and refuses anything else. The panel passed one straight to the other. The unit
  test asserted the bare name — it pinned what the code did rather than what the service accepts.
- **the Jobs screen was always empty.** `HIDScriptJobList` is a list of `ids`; the client decoded
  a shape the service has never sent. Unmarshalling into tags that match nothing is a zero value
  in Go, not an error, so the screen said "nothing running" while payloads ran.
- **three keys appeared dead.** They were read, debounced and delivered correctly, then dropped:
  the menu screens bound neither KEY1 nor KEY2. A control that silently changes nothing is
  indistinguishable from one that is not wired up.

The checker itself was wrong six times before it was right, and every one of its faults looked
like a device failure: a login posted as `login` instead of `username`, `RPC_CODE` assigned
inside a command substitution and lost with the subshell, `\{\}` sent as a request body, the Pi OS
login banner captured into every value read over ssh, a `stat` without `sudo` on a root-only
directory — and worst, a GPIO check that could not find its tool, grepped the string `nogpio`,
and announced that all eight pins were healthy. A green check that verified nothing is the
reason the other five were worth chasing.

*Verified by automation.* `make smoke` runs the **real service binary against the real data
tree in a container** and checks 28 things end to end — all passing:

```
PASS  firstboot bootstrap writes auth.json 0600      PASS  login returns a token (43 chars)
PASS  service survived startup without hardware      PASS  JSON bridge exposes 82 RPCs
PASS  reached 'service initialized'                  PASS  wrong password is refused
PASS  gRPC listener up                               PASS  login without Content-Type is refused
PASS  HTTP listener up                               PASS  changepw without a token is refused
PASS  no panic in the log                            PASS  cross-origin is refused even with a token
PASS  GET / redirects to the console                 PASS  unimplemented RPC returns 501, not a corpse
PASS  the console is served                          PASS  a hostile read length is rejected
PASS  console JS is served (3 files)                 PASS  USB settings use proto field names
PASS  console CSS is served                          PASS  the device's own scripts can authenticate
PASS  console favicon is served                      PASS  the local script credential exists, root-only
PASS  unauthenticated API is refused                 PASS  P4wnP1_cli works with no interactive login
                                                     PASS  service still alive after hostile input
                                                     PASS  clean shutdown on SIGTERM
```

`make access-control` is the adversarial gate: **every check in it is an attack that must fail** --
60 of them, covering unauthenticated reach, cross-origin and DNS rebinding, path traversal on all
three folders, symlink escape, token revocation, and the machine-local credential. Most were
written by first demonstrating the attack *succeeding* against the real binary, then fixing the
code, then confirming the check flipped. That is how the symlink escape above was found: a
non-root user planted `/tmp/sub/escalate -> /etc/cron.d/pwned` and the API wrote a root-owned
cron job through it.

`make feature-test` goes a layer deeper and **calls every one of the 83 RPCs** against that same
binary, sorting the answers into passed, correctly-unavailable-without-hardware, and failed.
On a machine with no USB gadget and no WiFi: **47 passed, 32 correctly unavailable, 6 skipped,
0 failed.** The middle class is the point — it matches each failure against the exact error text
that condition should produce, so an RPC that starts failing for a *new* reason is a failure, not
a shrug. It found `StoreDeployedWifiSettings` answering `proto: Marshal called with nil` on every
board without WiFi.

That test exists because "it compiles" and "the unit tests pass" were both true the whole time
the service was panicking on every cold boot. The panic was on a success path, inside a
constructor, behind a `modprobe` that made a manual restart look healthy. Nothing short of
starting the binary would have caught it — and it caught a second one while being written.

The console has its own gates, each built after a bug got past the previous one:
`make check-rpc` compares every RPC payload the console sends against the `.proto`
(the JSON bridge discards unknown fields, so a misspelled name is silently dropped —
that produced five real bugs, one of which erased the boot configuration);
`make check-render` renders all seven views in jsdom (`node --check` only parses, and
happily accepted a helper that called itself and removed every table in the console).

Also verified: both architectures cross-compile and `go vet` is clean for both; 93 test
functions across the auth, JSON-bridge, PSK-guard and DuckyScript packages pass on linux/arm64;
images build from official Raspberry Pi OS releases and pass the mount-and-inspect check above;
the console was driven **in a real browser against the real service binary** — sign-in, all
seven views, the live event stream, adding a reflex and reading it back off the API, both themes,
and a 375px phone viewport with no sideways page scroll; and the OLED interface has 41 tests of
its own, covering the SH1106 addressing and charge-pump commands, the debounce timing on an
injected clock, every RPC name checked against the `.proto`, and twenty thousand random button
presses that must never panic, never blank the screen and never strand you on a screen you
cannot leave.

That last one is not a formality. Driving it by hand is what found that the device could not
authenticate to *itself*: `servicestart.sh` — the fallback that brings up the USB gadget, the
DHCP servers and the access point when a startup template fails — is a shell script full of
`P4wnP1_cli` calls, and every one of them had been failing `Unauthenticated` since the API
started requiring a token. A device whose template failed came up with no network at all, and the
fallback meant to rescue it was the broken part. The console looked perfect throughout, because
the console authenticates normally. Nothing in the test suite had ever read the service's own
log; three checks now do.

**Keystroke injection is verified.** A payload written to `/tmp`, started as a job and typed
into a MacBook over the cable:

```
HIDScript layout: Setting layout to 'US'
HIDScript type: Typing 'P4wnP1 HID test ...' on HID keyboard device '/dev/hidg0'
JOB 2 on VM 0 SUCCEEDED WITH RESULT: null
```

and the line arrived in the host's editor. The device log proves only that it transmitted; the
host is the only thing that can prove receipt, and it did.

The payload sent **no Enter and no modifier keys**, deliberately. Had the focus been a terminal
rather than an editor, the text would have sat there inertly instead of executing. That is the
difference between a test and an accident, and `tools/hardware-check.sh` keeps the distinction:
the script it runs by itself presses no keys at all, because a health check that types into
whatever window you have focused is not a health check.

*Still not verified:* that Bluetooth pairs. That mass storage or the serial function work. That
the trigger engine fires on real events. That any of it survives the cable being pulled
mid-write.

If you are evaluating this for real work, **boot it on a Pi and check those yourself** —
`tools/hardware-check.sh` does the other twenty-eight for you.
[KNOWN_ISSUES.md](KNOWN_ISSUES.md) tracks what is fixed and what is still open, including the
cold-boot panic that made every freshly flashed device dead on arrival until this release.

---

## Licence and selling this

P4wnP1 A.L.O.A. is **GPL-3.0** (see [LICENSE](LICENSE)), inherited from
[MaMe82's original](https://github.com/mame82/P4wnP1_aloa).

GPL-3.0 **permits selling** devices with this software on them. What it requires is that every
buyer gets the complete corresponding source for the version on their device, under GPL-3.0,
including your modifications, and that you do not add restrictions on their right to use, modify
and redistribute it. You cannot keep changes to this codebase proprietary while shipping them.

In practice the viable commercial shape is hardware, assembly, support, documentation, training
and engagement services — not licence fees for the code. If you intend to build a business on
this, get the position reviewed by someone qualified; the paragraph above describes what the
licence says, it is not legal advice.

---

## Documentation

- **[INSTALL.md](INSTALL.md)** — installing onto an existing Raspberry Pi OS
- **[image/README.md](image/README.md)** — building flashable images
- **[docs/TUTORIAL.md](docs/TUTORIAL.md)** — HIDScript, the CLI, and trigger actions
- **[KNOWN_ISSUES.md](KNOWN_ISSUES.md)** — the defect backlog, prioritised
- **[CHANGELOG.md](CHANGELOG.md)** — what changed and why

## Credits

Created by **[MaMe82](https://github.com/mame82)** (Marcus Mengs), whose design — the composite
gadget, HIDScript, and the trigger/action engine — is what this is. Maintained upstream at
[RoganDawes/P4wnP1_aloa](https://github.com/RoganDawes/P4wnP1_aloa). Kali Linux ship a prebuilt
image for the Pi Zero W.

This fork modernises the toolchain, adds authentication, a JSON API, arm64 support, a new
console, DuckyScript conversion and a verified image pipeline — and fixes the reason a flashed
device never worked.

<p align="center"><sub>
Fraunces for identity and figures · Inter for interface text · system monospace for measured values<br>
Warm paper with two golds; dark mode is true black
</sub></p>
