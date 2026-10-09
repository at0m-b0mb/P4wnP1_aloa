#!/usr/bin/env bash
#
# check-cli-commands.sh -- every P4wnP1_cli command the shipped scripts and
# documents print or run must actually be RUNNABLE.
#
# WHY THIS EXISTS
#
# It has gone wrong twice, both times in text an operator is told to follow:
#
#   1. The first-boot credentials file printed `P4wnP1_cli wifi set --ssid ...`.
#      `wifi set` exists, but it is a GROUP with no Run -- the SSID belongs to
#      `wifi set ap` or `wifi set sta`. Running the printed line prints help
#      and changes nothing.
#   2. The same file later printed `P4wnP1_cli wifi state`. There is no read
#      command at all: wifiGetCmd is commented out in cli_client/cmd_wifi.go.
#      `wifi` resolves, so the line looks plausible, and does nothing.
#
# Neither failed anything, because the scripts do not RUN those lines -- they
# print them. The device boots, the file looks authoritative, and the operator
# gets help text from the tool that was supposed to be the recovery path.
#
# THE RULE: for each invocation, take the longest prefix of words that resolves
# to a real command, and require that command to be runnable (Run or RunE).
# Words after it are positional arguments, which is fine. Merely EXISTING is
# not enough -- that is precisely what let both bugs through, and an earlier
# version of this script passed them both for exactly that reason.
#
# Usage:  ./tools/check-cli-commands.sh
set -uo pipefail
cd "$(dirname "$0")/.."

python3 -I - <<'PY'
import glob, io, re, sys

RED, GRN, OFF = "\033[31m", "\033[32m", "\033[0m"

def strip_block_comments(src):
    # A command inside /* ... */ does not exist. cmd_wifi.go has one.
    return re.sub(r'/\*.*?\*/', '', src, flags=re.S)

def literal_body(src, open_brace_idx):
    """Return the text of a brace-balanced struct literal."""
    depth, i, n = 0, open_brace_idx, len(src)
    while i < n:
        c = src[i]
        if c == '{':
            depth += 1
        elif c == '}':
            depth -= 1
            if depth == 0:
                return src[open_brace_idx + 1:i]
        i += 1
    return src[open_brace_idx + 1:]

use_of, runnable, parent_of = {}, {}, {}

for path in glob.glob('cli_client/*.go'):
    src = strip_block_comments(io.open(path, encoding='utf-8').read())

    # BOTH declaration styles are in use and both must be parsed. cmd_wifi.go
    # and friends use package-level `var xCmd = &cobra.Command{...}`;
    # cmd_usb_new.go builds its tree from function-local `cmdUsb := &cobra.
    # Command{...}`. Parsing only the first missed the whole `usb` subtree,
    # which is most of what servicestart.sh actually calls.
    for m in re.finditer(r'(?:var\s+)?(\w+)\s*:?=\s*&cobra\.Command\s*\{', src):
        var = m.group(1)
        body = literal_body(src, src.index('{', m.end() - 1))
        u = re.search(r'Use:\s*"([^"\s]+)', body)
        if not u:
            continue
        use_of[var] = u.group(1)
        # A group command has no Run. Invoking it prints help.
        runnable[var] = bool(re.search(r'\bRunE?\s*:', body))

    for line in src.split('\n'):
        if line.lstrip().startswith('//'):
            continue          # commented-out wiring means unreachable
        # AddCommand IS VARIADIC and is used that way:
        #   cmdUsb.AddCommand(cmdUsbDeploy, cmdUsbSet, cmdUsbGet, cmdUsbMount)
        # A single-argument pattern parsed only cmdUsbDeploy and left the rest
        # of the usb subtree unreachable, which this script then reported as
        # three bogus failures in scripts that were perfectly correct.
        m = re.search(r'(\w+)\.AddCommand\(([^)]*)\)', line)
        if m:
            for child in re.findall(r'\w+', m.group(2)):
                parent_of[child] = m.group(1)

# Resolve each command to its full word path from the root.
root = None
for var, use in use_of.items():
    if var == 'rootCmd':
        root = use

paths = {}       # ('wifi','set','ap') -> runnable?
for var in use_of:
    chain, cur, ok = [], var, True
    while True:
        chain.append(use_of[cur])
        if cur == 'rootCmd':
            break
        cur = parent_of.get(cur)
        if cur is None or cur not in use_of:
            ok = False
            break
    if not ok:
        continue             # declared but never wired: unreachable
    chain.reverse()
    chain = chain[1:]        # drop the root's own name
    if chain:
        paths[tuple(chain)] = runnable[var]

# --- the two vacuity guards -------------------------------------------------
if len(paths) < 10:
    print("  %sFAIL%s only %d CLI commands were parsed out of cli_client/." % (RED, OFF, len(paths)))
    print("        The parser has stopped matching, so this check would pass")
    print("        anything. Fix the parser; do not trust a green run.")
    sys.exit(1)
if not any(paths.values()):
    print("  %sFAIL%s no command was detected as runnable." % (RED, OFF))
    print("        The Run: detection has broken, so every invocation would")
    print("        be reported as a group and the check would fail uselessly.")
    sys.exit(1)

TARGETS = sorted(set(glob.glob('dist/scripts/*.sh') + ['install.sh']))
INVOKE = re.compile(r'P4wnP1_cli((?:\s+[a-z][a-z0-9-]*)+)')

found, bad = 0, []
for path in TARGETS:
    try:
        src = io.open(path, encoding='utf-8').read()
    except OSError:
        continue
    for lineno, line in enumerate(src.split('\n'), 1):
        for m in INVOKE.finditer(line):
            words = tuple(m.group(1).split())
            found += 1
            # longest resolving prefix
            best = None
            for k in range(len(words), 0, -1):
                if words[:k] in paths:
                    best = words[:k]
                    break
            if best is None:
                bad.append((path, lineno, ' '.join(words), line.strip(),
                            "is not a command at all"))
            elif not paths[best]:
                extra = ' '.join(words[len(best):])
                sub = sorted(p[len(best)] for p in paths
                             if len(p) == len(best) + 1 and p[:len(best)] == best)
                why = "'%s' is a command GROUP with no action" % ' '.join(best)
                if extra:
                    why += "; '%s' is not one of its subcommands" % extra
                if sub:
                    why += " (it has: %s)" % ', '.join(sub)
                bad.append((path, lineno, ' '.join(words), line.strip(), why))

if found < 5:
    print("  %sFAIL%s only %d P4wnP1_cli invocations were found in %d files."
          % (RED, OFF, found, len(TARGETS)))
    print("        The scanner has stopped matching; this check cannot fail.")
    sys.exit(1)

print("  parsed %d command paths (%d runnable); checked %d invocations in %d files"
      % (len(paths), sum(1 for v in paths.values() if v), found, len(TARGETS)))

if bad:
    print()
    for path, lineno, words, line, why in bad:
        print("  %sFAIL%s %s:%d  'P4wnP1_cli %s' %s" % (RED, OFF, path, lineno, words, why))
        print("        %s" % line)
    sys.exit(1)

print("  %sok%s    every printed and executed P4wnP1_cli command is runnable" % (GRN, OFF))
PY
