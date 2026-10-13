/*
 * ios-02-spotlight.js -- reach Spotlight on iPadOS with CMD+SPACE and search,
 *                        the one launcher-shaped thing iOS offers a keyboard.
 *
 * TARGET:      iPadOS 13 and later. ON AN iPHONE THIS DOES NOTHING -- see below.
 * NEEDS:       a working physical connection (read ios-01-hello.js, that is
 *              where the real difficulty is), the iPad UNLOCKED, and the home
 *              screen or an app that does not capture CMD+SPACE itself.
 * TOUCHES:     it opens Spotlight and types a search term. It does NOT press
 *              ENTER, so nothing is launched -- the result list is left on
 *              screen for you to look at. Press escape on the iPad to dismiss.
 * LIMITATION:  iPhone does not have this. Spotlight-by-keyboard is an iPadOS
 *              feature; on iOS proper, CMD+SPACE does nothing from the home
 *              screen. Running this against an iPhone is not a failure of the
 *              script.
 * VERIFIED:    not yet tested on hardware, and more uncertain than anything else
 *              in this directory. Do not demonstrate it to anyone before seeing
 *              it work on the exact iPad and iPadOS version in the room.
 *
 * WHY IT DELIBERATELY DOES NOT PRESS ENTER
 *
 * Every other launcher script here commits -- it types a name and presses ENTER
 * to open the thing. This one stops short, for two reasons.
 *
 * First, Spotlight's top result on iPadOS is not predictable. It mixes apps,
 * contacts, messages, web suggestions and Siri suggestions, and it reorders
 * them as you type. Pressing ENTER blind on someone else's iPad could open a
 * conversation, place a call, or run a web search in front of a client. This
 * device cannot see the screen, so it cannot check first.
 *
 * Second, not pressing ENTER is enough. The demonstration is that a USB
 * keyboard reached the system launcher of a locked-down mobile OS and made it
 * respond. The populated result list on screen proves exactly that, and it
 * proves it without touching anything. Where a payload can prove its point
 * without committing an action, it should.
 *
 * WHAT TO SEARCH FOR
 *
 * "Settings" is chosen because every iPad has it, it is unambiguous, and seeing
 * it appear as the top hit tells you the search text arrived intact -- which is
 * also a free keymap check. Change SEARCH_FOR if you want to show something
 * else; keep it to plain letters, since punctuation is where layouts disagree.
 */

var SEARCH_FOR = "Settings";

layout('us');
typingSpeed(40, 50);   // Slow, like ios-01-hello.js, and for the same reason.

delay(1500);           // iOS binds a new keyboard slowly.

press("GUI SPACE");    // GUI is CMD. Spotlight, on iPadOS.
delay(1200);           // Spotlight animates in and drops keys typed during it.

type(SEARCH_FOR);
delay(1500);           // let the result list populate so there is something to see

// No ENTER. See the header -- this is the point of the script, not an omission.

"ios-02-spotlight: sent CMD+SPACE and typed '" + SEARCH_FOR + "'. Look at the " +
"iPad: a populated result list is the result. Nothing was launched. On an " +
"iPhone, expect nothing -- this is iPadOS only.";
