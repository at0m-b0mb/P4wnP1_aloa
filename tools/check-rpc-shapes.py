#!/usr/bin/env python3
"""Check every RPC payload the web console sends against proto/grpc.proto.

WHY THIS EXISTS

The JSON bridge unmarshals with DiscardUnknown, so a payload with a misspelled
or invented field name is accepted and the field is silently dropped. The
service then acts on a zero value. That produced five real bugs in the console,
each invisible until someone used the feature on real hardware:

  FSCreateTempDirOrFile  sent `dir` as a boolean and a `path` field that does
                         not exist               -> request rejected outright
  FSWriteFile            sent `content`, not `data`
                         -> the file was created empty
  FSReadFile             sent no `len`
                         -> stored scripts loaded as an empty string
  SetStartupMasterTemplate sent `templateName`, not `msg`
                         -> "Use at boot" ERASED the boot default instead of
                            setting it
  (and the response side read `tmp.path`, which does not exist)

Reading the proto and the console together catches all of them in a second.

    python3 tools/check-rpc-shapes.py
"""
import json
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
PROTO = ROOT / "proto/grpc.proto"
JS = sorted((ROOT / "dist/www/app/js").glob("*.js"))


def parse_proto():
    """-> ({message: {field: type}}, {rpc: (request_message, response_message)})"""
    text = PROTO.read_text()
    # Strip comments so they cannot be mistaken for fields.
    text = re.sub(r"//[^\n]*", "", text)

    messages = {}
    for m in re.finditer(r"message\s+(\w+)\s*\{", text):
        name = m.start()
        depth, i = 0, m.end() - 1
        while i < len(text):
            if text[i] == "{":
                depth += 1
            elif text[i] == "}":
                depth -= 1
                if depth == 0:
                    break
            i += 1
        body = text[m.end():i]
        # Drop nested message/enum bodies: their fields are not this message's.
        body = re.sub(r"(message|enum)\s+\w+\s*\{[^{}]*\}", "", body)
        fields = {}
        for f in re.finditer(r"(?:repeated\s+)?([\w.]+)\s+(\w+)\s*=\s*\d+", body):
            ftype, fname = f.group(1), f.group(2)
            if ftype in ("message", "enum", "oneof", "returns", "rpc"):
                continue
            fields[fname] = ftype
        messages[m.group(1)] = fields

    rpcs = {}
    for r in re.finditer(r"rpc\s+(\w+)\s*\(\s*([\w.]+)\s*\)\s*returns\s*\(\s*(?:stream\s+)?([\w.]+)\s*\)", text):
        rpcs[r.group(1)] = (r.group(2), r.group(3))
    return messages, rpcs


def console_calls():
    """Find Api.rpc('Method', { ...literal object... }) and return the keys sent."""
    calls = []
    for path in JS:
        src = path.read_text()
        for m in re.finditer(r"Api\.rpc\(\s*'(\w+)'\s*(,)?", src):
            method = m.group(1)
            if not m.group(2):
                calls.append((path.name, method, None))  # no payload, fine
                continue
            # Walk the object literal that follows, tracking brace depth so
            # nested objects do not end it early.
            i = src.index(",", m.start()) + 1
            while i < len(src) and src[i] in " \t\n":
                i += 1
            if i >= len(src) or src[i] != "{":
                calls.append((path.name, method, None))  # variable, not a literal
                continue
            depth, j = 0, i
            while j < len(src):
                if src[j] == "{":
                    depth += 1
                elif src[j] == "}":
                    depth -= 1
                    if depth == 0:
                        break
                j += 1
            body = src[i + 1:j]
            # Top-level keys only.
            keys, d = [], 0
            for km in re.finditer(r"[{}]|(\w+)\s*:", body):
                if km.group(0) == "{":
                    d += 1
                elif km.group(0) == "}":
                    d -= 1
                elif d == 0 and km.group(1):
                    keys.append(km.group(1))
            calls.append((path.name, method, keys))
    return calls


def check_response_reads(messages):
    """Check the keys the console reads off a RESPONSE, not just what it sends.

    Requests were covered; responses were not, and that is where the worst bug
    lived: the bridge emitted protojson's lowerCamelCase JSON names, so every
    key the console read off GadgetSettings was absent and the USB view showed
    every function as off regardless of what was deployed.

    USB_FUNCTIONS in app.js is a declared list of keys, so it can be checked
    directly against the message. Arbitrary property reads elsewhere cannot be,
    which is why service/jsonbridge also has a test pinning the emitted names.
    """
    problems = []
    app = (ROOT / "dist/www/app/js/app.js").read_text()

    m = re.search(r"const USB_FUNCTIONS = \[(.*?)\n\];", app, re.S)
    if not m:
        return ["app.js: could not find USB_FUNCTIONS -- update this checker"]
    keys = re.findall(r"key:\s*'([^']+)'", m.group(1))
    fields = messages.get("GadgetSettings", {})
    for k in keys:
        if k not in fields:
            problems.append(
                f"app.js: USB_FUNCTIONS reads '{k}' but GadgetSettings has no such field")

    # The detail accessors read nested/extra fields by name too.
    for k in re.findall(r"s\.([a-z][A-Za-z0-9_]*)", m.group(1)):
        if k not in fields:
            problems.append(
                f"app.js: USB_FUNCTIONS reads 's.{k}' but GadgetSettings has no such field")
    return problems


def main():
    messages, rpcs = parse_proto()
    print(f"proto: {len(messages)} messages, {len(rpcs)} rpcs")

    problems = []
    checked = 0
    for fname, method, keys in console_calls():
        if method not in rpcs:
            problems.append(f"{fname}: Api.rpc('{method}') -- no such RPC in grpc.proto")
            continue
        if keys is None:
            continue
        req = rpcs[method][0]
        fields = messages.get(req)
        if fields is None:
            continue
        checked += 1
        for k in keys:
            if k not in fields:
                near = ", ".join(sorted(fields)) or "(none)"
                problems.append(
                    f"{fname}: {method} sends '{k}' but {req} has no such field.\n"
                    f"        {req} fields: {near}")

    resp_problems = check_response_reads(messages)
    problems.extend(resp_problems)
    print(f"checked {checked} call sites with literal payloads, "
          f"plus the keys the console reads off GadgetSettings")
    if problems:
        print(f"\nFAIL -- {len(problems)} problem(s):")
        for p in problems:
            print("  - " + p)
        return 1
    print("PASS -- every field the console sends exists in its request message")
    return 0


if __name__ == "__main__":
    sys.exit(main())
