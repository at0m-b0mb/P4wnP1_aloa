# Web root

Everything the device serves over HTTP lives here. It is copied verbatim to
`/usr/local/P4wnP1/www/` at install time, and `/` redirects to `/app/`.

`app/` is the operator console: hand-written HTML, CSS and JavaScript, no
build step and no bundler. Edit the files, reload the page. `index.html`
loads `js/*.js` with a `?v=` query string, so bump that when you change one
or a browser that has visited before will keep the old copy.

## What used to be here

This directory previously also held a GopherJS-compiled client -- an
`index.html` loading `webapp.js`, plus vendored CodeMirror, FontAwesome, Vue
and Vuex: about 29MB, roughly a hundred times the size of the console that
replaced it.

None of it worked. The client predates the authentication layer and has no
code to send a bearer token, so every RPC it made came back
Unauthenticated, and `webapp.js` had not been built in a long time because
`web_client/` no longer compiles. The page loaded, drew a UI, and could not
talk to the device.

It is all deleted. `web_client/` is still in the repository as the
reference for how the original upstream client worked, but nothing ships
from it and nothing builds it.
