package common_web

// VERSION is what the device reports itself to be.
//
// A VAR, not a const, so the build can stamp the real tag into it:
//
//	-ldflags "-X github.com/mame82/P4wnP1_aloa/common_web.VERSION=$(git describe --tags)"
//
// It used to be `const VERSION = "v0.1.1" + SUFFIX`, hardcoded, and nothing
// updated it — so a device running v0.8.0 introduced itself as v0.1.1 in its
// own startup log. Meanwhile image/build.sh passed `-X main.version=...`, which
// names a symbol that does not exist; Go silently ignores an -X for a missing
// symbol, so the real version reached nothing at all.
//
// That is not cosmetic. An appliance that cannot say what it is running cannot
// be sensibly updated: `p4wnp1-update --check` has nothing to compare, and an
// operator filing a bug reports a version number that was last true in 2019.
//
// The default below is what you get from a plain `go build` with no stamp, and
// it is deliberately not a version number — "dev" is honest, "v0.1.1" was not.
var VERSION = "dev"
