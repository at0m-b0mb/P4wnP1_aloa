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



# ---------------------------------------------------------------------------
# The OLED client speaks the same JSON API from Go, with the same hazard.
#
# DiscardUnknown drops a misspelled request field in silence, and a response
# struct whose json tag does not exist just stays at its zero value. The OLED
# client was never checked here, and it shipped with the bug this whole file
# was written to catch -- a different shape of it: HIDRunScript was handed a
# bare payload name where the service demands an absolute path, so every
# payload run from the panel failed with "path must be absolute". A field-name
# checker could not have caught that one, but it can catch its siblings, and
# the client has twenty more call sites that nothing was reading.
# ---------------------------------------------------------------------------

GO_CLIENT = ROOT / "oled/client.go"


def _balanced(src, i, open_ch, close_ch):
    """Index just past the group starting at src[i] == open_ch."""
    depth = 0
    while i < len(src):
        c = src[i]
        if c == '"':                       # skip string literals
            i += 1
            while i < len(src) and src[i] != '"':
                i += 2 if src[i] == "\\" else 1
        elif c == '`':
            i = src.index('`', i + 1)
        elif c == open_ch:
            depth += 1
        elif c == close_ch:
            depth -= 1
            if depth == 0:
                return i + 1
        i += 1
    return len(src)


def _split_args(s):
    """Split a call's argument list on top-level commas."""
    args, depth, cur, i = [], 0, "", 0
    while i < len(s):
        c = s[i]
        if c == '"':
            j = i + 1
            while j < len(s) and s[j] != '"':
                j += 2 if s[j] == "\\" else 1
            cur += s[i:j + 1]
            i = j + 1
            continue
        if c in "([{":
            depth += 1
        elif c in ")]}":
            depth -= 1
        if c == "," and depth == 0:
            args.append(cur.strip())
            cur = ""
        else:
            cur += c
        i += 1
    if cur.strip():
        args.append(cur.strip())
    return args


def _map_keys(expr):
    """Top-level string keys of a Go map literal, or None if not a literal."""
    m = re.match(r"map\[string\][\w.\[\]{}]*\{", expr)
    if not m:
        return None
    body = expr[m.end():expr.rindex("}")]
    keys, depth, i = [], 0, 0
    while i < len(body):
        c = body[i]
        if c == '"':
            j = i + 1
            while j < len(body) and body[j] != '"':
                j += 2 if body[j] == "\\" else 1
            if depth == 0 and re.match(r"\s*:", body[j + 1:]):
                keys.append(body[i + 1:j])
            i = j + 1
            continue
        if c in "{[(":
            depth += 1
        elif c in "}])":
            depth -= 1
        i += 1
    return keys


def _struct_tags(body):
    """Top-level json tags of a Go struct literal body.

    Returns [(tag, nested_body_or_None)]. A nested anonymous struct is
    returned with its own body so the caller can recurse into the message
    that field's proto type names -- the first version of this scanned line
    by line and reported the INNER tags as if they were top level, which made
    three correctly-written response structs look broken and would have hidden
    a genuinely wrong nested tag behind the noise.
    """
    out, i, n = [], 0, len(body)
    while i < n:
        if body[i] in " \t\n":
            i += 1
            continue
        # Consume one field declaration, which ends at a newline unless it
        # opens an anonymous struct.
        j, nested, after = i, None, i
        while j < n and body[j] != "\n":
            if body[j] == "{":
                k = _balanced(body, j, "{", "}")
                nested = body[j + 1:k - 1]
                j = k
                # The field's OWN tag follows the closing brace. Searching the
                # whole declaration found the first tag INSIDE the nested
                # struct instead, so every nested field reported against the
                # outer message and the outer field was never checked at all.
                after = k
                continue
            if body[j] == "`":
                j = body.index("`", j + 1) + 1
                continue
            j += 1
        decl = body[i:j]
        m = re.search(r'`json:"([^",]+)', body[after:j])
        if m:
            out.append((m.group(1), nested))
        i = j + 1
    return out


def _check_tags(tags, msg, messages, where, problems):
    fields = messages.get(msg)
    if fields is None:
        return
    for tag, nested in tags:
        if tag not in fields:
            problems.append(
                f"{where}: reads '{tag}' off {msg}, which has no such field.\n"
                f"        {msg} fields: {', '.join(sorted(fields)) or '(none)'}")
            continue
        if nested:
            _check_tags(_struct_tags(nested), fields[tag], messages, where, problems)


def check_go_client(messages, rpcs):
    problems, checked = [], 0
    src = GO_CLIENT.read_text()
    # Function bodies, so a response variable is resolved in its own scope.
    funcs = [(m.start(), m.end()) for m in re.finditer(r"\nfunc ", src)]
    bounds = [(a, funcs[i + 1][0] if i + 1 < len(funcs) else len(src))
              for i, (a, _) in enumerate(funcs)]

    for a, b in bounds:
        body = src[a:b]
        for m in re.finditer(r"c\.call\(", body):
            end = _balanced(body, m.end() - 1, "(", ")")
            args = _split_args(body[m.end():end - 1])
            if not args or not args[0].startswith('"'):
                continue
            method = args[0].strip('"')
            if method not in rpcs:
                problems.append(f"client.go: c.call({method!r}) -- no such RPC in grpc.proto")
                continue
            req_msg, resp_msg = rpcs[method]
            checked += 1

            # Request: literal map keys only.
            if len(args) > 1:
                keys = _map_keys(args[1])
                if keys is not None:
                    fields = messages.get(req_msg, {})
                    for k in keys:
                        if k not in fields:
                            problems.append(
                                f"client.go: {method} sends '{k}' but {req_msg} has no such field.\n"
                                f"        {req_msg} fields: {', '.join(sorted(fields)) or '(none)'}")

            # Response: the struct the reply is decoded into.
            if len(args) > 2 and args[2].startswith("&"):
                var = args[2][1:].strip()
                d = re.search(r"var\s+" + re.escape(var) + r"\s+struct\s*\{", body)
                if d:
                    j = _balanced(body, d.end() - 1, "{", "}")
                    _check_tags(_struct_tags(body[d.end():j - 1]), resp_msg,
                                messages, f"client.go: {method}", problems)
                elif var == "out" and "stringArray" in body:
                    _check_tags([("msgArray", None)], resp_msg, messages,
                                f"client.go: {method}", problems)
    return problems, checked



# ---------------------------------------------------------------------------
# The USB endpoint budget, in two places that must agree.
#
# service/SubSysUSB.go refuses a composition that needs more endpoints than
# the controller has, and the OLED panel counts the same budget so it can say
# so BEFORE the deploy rather than after. Two copies of the same seven.
#
# A panel that thinks the ceiling is higher than the service does will offer
# compositions that cannot deploy; one that thinks it is lower will refuse
# ones that would have worked. Both are worse than no counting at all,
# because the operator now has a second opinion that is wrong.
# ---------------------------------------------------------------------------

def check_endpoint_budget():
    problems = []
    svc = (ROOT / "service/SubSysUSB.go").read_text()
    panel = (ROOT / "oled/client.go").read_text()

    svc_consts = dict(
        (m.group(1), int(m.group(2)))
        for m in re.finditer(r"USB_EP_USAGE_(\w+)\s*=\s*(\d+)", svc)
    )
    if not svc_consts:
        return ["could not read USB_EP_USAGE_* from service/SubSysUSB.go"]

    svc_max = svc_consts.pop("MAX", None)
    panel_max = re.search(r"EndpointMax\s*=\s*(\d+)", panel)
    if svc_max is None or not panel_max:
        return ["could not read the endpoint ceiling from both sides"]
    if int(panel_max.group(1)) != svc_max:
        problems.append(
            f"endpoint ceiling disagrees: service says {svc_max}, "
            f"oled/client.go says {panel_max.group(1)}")

    # service name -> the proto field the panel keys on
    want = {
        "HID_KEYBOARD": "use_HID_KEYBOARD", "HID_MOUSE": "use_HID_MOUSE",
        "HID_RAW": "use_HID_RAW", "RNDIS": "use_RNDIS",
        "CDC_ECM": "use_CDC_ECM", "CDC_SERIAL": "use_SERIAL", "UMS": "use_UMS",
    }
    panel_costs = dict(
        (m.group(1), int(m.group(2)))
        for m in re.finditer(r'"(use_[A-Za-z_]+)":\s*(\d+)', panel)
    )
    for sname, cost in svc_consts.items():
        key = want.get(sname)
        if key is None:
            problems.append(f"service has USB_EP_USAGE_{sname} and this checker does not know it")
            continue
        if key not in panel_costs:
            problems.append(f"the panel does not cost {key} (service charges {cost})")
        elif panel_costs[key] != cost:
            problems.append(
                f"{key} costs {cost} in the service but {panel_costs[key]} in the panel")
    for key in panel_costs:
        if key not in want.values():
            problems.append(f"the panel costs {key}, which the service does not charge for")

    # THREE copies now, not two: the web console counts as well, so it can
    # refuse before making someone confirm a disconnect warning for a
    # composition that cannot deploy.
    js = (ROOT / "dist/www/app/js/app.js").read_text()
    js_max = re.search(r"USB_ENDPOINT_MAX\s*=\s*(\d+)", js)
    if not js_max:
        problems.append("the web console does not define USB_ENDPOINT_MAX")
    elif int(js_max.group(1)) != svc_max:
        problems.append(
            f"endpoint ceiling disagrees: service says {svc_max}, "
            f"the web console says {js_max.group(1)}")

    m = re.search(r"const USB_ENDPOINT_COST = \{(.*?)\}", js, re.S)
    if not m:
        problems.append("the web console does not define USB_ENDPOINT_COST")
    else:
        js_costs = dict((k, int(v)) for k, v in re.findall(r"(use_\w+):\s*(\d+)", m.group(1)))
        for sname, cost in svc_consts.items():
            key = want.get(sname)
            if key is None:
                continue
            if key not in js_costs:
                problems.append(f"the web console does not cost {key} (service charges {cost})")
            elif js_costs[key] != cost:
                problems.append(
                    f"{key} costs {cost} in the service but {js_costs[key]} in the web console")
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

    problems.extend(check_response_reads(messages))

    go_problems, go_checked = check_go_client(messages, rpcs)
    problems.extend(go_problems)
    problems.extend(check_endpoint_budget())
    print(f"checked {checked} console call sites with literal payloads, "
          f"{go_checked} OLED client call sites, "
          f"plus the keys the console reads off GadgetSettings")
    if problems:
        print(f"\nFAIL -- {len(problems)} problem(s):")
        for p in problems:
            print("  - " + p)
        return 1
    print("PASS -- every field the console and the OLED client send or read exists,")
    print("        and the USB endpoint budget agrees between the service and the panel")
    return 0


if __name__ == "__main__":
    sys.exit(main())
