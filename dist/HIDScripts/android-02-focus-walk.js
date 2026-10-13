/*
 * android-02-focus-walk.js -- demonstrate TAB and arrow-key focus traversal on
 *                             Android, the mechanism every real Android HID
 *                             payload is actually built on.
 *
 * TARGET:      Android 9 and later, over USB OTG.
 * NEEDS:       the phone UNLOCKED, on a screen with more than one focusable
 *              control -- a settings list, a form, or a web page work well. The
 *              home screen does not; it has little for TAB to move between.
 * TOUCHES:     it moves the focus highlight and presses ENTER once, which
 *              ACTIVATES whatever is focused at that moment. That is a real
 *              action on a real phone, so run it on a screen where you are
 *              content for something to be tapped. A settings list is a good
 *              choice; a payment screen is not.
 * LIMITATION:  this device cannot see the screen, so it cannot know what is
 *              focused when it presses ENTER. The whole point of the script is
 *              to let a human watch the focus ring move -- the demonstration is
 *              visual and it needs an observer.
 * VERIFIED:    not yet tested on hardware. Focus traversal is one of the most
 *              OEM-dependent behaviours on Android: Samsung One UI, Pixel stock
 *              and MIUI all differ in which views accept focus and how the ring
 *              is drawn. Confirm on the handset you care about.
 *
 * WHY THIS MATTERS MORE THAN IT LOOKS
 *
 * android-01-hello.js shows text going into a field someone already focused.
 * That is a fine proof but it is not leverage, because the operator had to set
 * it up by hand. Focus traversal is the leverage: TAB, the arrow keys and ENTER
 * are ordinary Keyboard/Keypad usages, well inside what this device's HID
 * descriptor can send (see android-01-hello.js for what it cannot), and
 * together they let a keyboard reach controls nobody focused for it.
 *
 * Every genuinely useful Android keyboard payload is built from this primitive:
 * move focus a known number of steps, then activate. It is also why such
 * payloads are brittle -- the step count is specific to one screen on one
 * Android version on one OEM skin, and this device gets no feedback to tell it
 * when the count was wrong. Watch the ring and count the steps yourself; do not
 * trust a step count you did not verify on that exact screen.
 *
 * ESC IS THE INTERESTING ONE
 *
 * Many Android versions map ESC to Back, which is the closest this device gets
 * to a system key -- remember that the real BACK button is a consumer-control
 * usage the descriptor cannot send. It is tested last and deliberately: if it
 * works you leave the screen you started on, so nothing after it would be
 * predictable anyway.
 */

layout('us');
typingSpeed(30, 40);

delay(1000);

// Step 1: TAB forward. Watch the focus ring move. Five steps is enough to see
// the pattern and few enough to follow by eye.
var i;
for (i = 0; i < 5; i++) {
	press("TAB");
	delay(500);        // slow on purpose: this is for a human to watch
}

// Step 2: TAB backward with SHIFT, same path in reverse. If forward worked and
// backward does not, the app is handling TAB itself rather than letting the
// framework traverse -- worth knowing, and a thing you only learn by trying.
for (i = 0; i < 5; i++) {
	press("SHIFT TAB");
	delay(500);
}

// Step 3: the arrow keys. On list and grid layouts these often traverse where
// TAB will not, so a payload that fails with TAB is worth retrying with these.
press("DOWN");
delay(500);
press("DOWN");
delay(500);
press("UP");
delay(500);
press("RIGHT");
delay(500);
press("LEFT");
delay(700);

// Step 4: activate whatever is focused. See the TOUCHES line -- this is a real
// tap on a real phone and this device cannot see what it is about to press.
press("ENTER");
delay(1200);

// Step 5: ESC, last, because success here means leaving the screen.
press("ESC");
delay(500);

"android-02-focus-walk: sent 5 TAB, 5 SHIFT+TAB, 5 arrows, ENTER, then ESC. " +
"Watch the phone -- whether the focus ring moved is the result, and only you " +
"can see it.";
