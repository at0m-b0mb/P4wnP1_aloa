#!/usr/bin/env python3
"""A stand-in P4wnP1 service, for developing the console without a Pi.

Serves dist/www/app plus the same endpoints the real service does:
/api/auth/*, /api/v1/rpc/<Method>, /api/v1/events.

The response and REQUEST shapes here are copied from proto/grpc.proto on
purpose. A mock that accepts whatever the console happens to send is worse than
no mock: the console shipped with four wrong field names in the HIDScript path
(`dir` as a boolean, `content` instead of `data`, an absolute `filename`, and a
read with no `len`) and a permissive mock would have let all four through.
So this one rejects a malformed request the way the real service does.

    python3 tools/mock-service.py          # http://127.0.0.1:8111
"""
import base64
import json
import time
from http.server import ThreadingHTTPServer, SimpleHTTPRequestHandler
from pathlib import Path
from urllib.parse import urlparse

ROOT = str(Path(__file__).resolve().parent.parent / "dist/www/app")
TOKEN = "mock-token-abc123"
USER, PASS = "admin", "letmein-please"


class RpcError(Exception):
    """Maps to a 400, like a protojson/validation failure on the real service."""


def err(msg):
    raise RpcError(msg)


# --- mutable device state ---------------------------------------------------

USB = {
    "enabled": True, "vid": "0x1d6b", "pid": "0x0137",
    "manufacturer": "MaMe82", "product": "P4wnP1 by MaMe82", "serial": "deadbeef1337",
    "use_CDC_ECM": False, "use_RNDIS": True, "use_HID_KEYBOARD": True,
    "use_HID_MOUSE": True, "use_HID_RAW": True, "use_UMS": False, "use_SERIAL": False,
    "rndis_settings": {"host_addr": "02:1a:11:00:00:01", "dev_addr": "02:1a:11:00:00:02"},
    "cdc_ecm_settings": {"host_addr": "", "dev_addr": ""},
    "ums_settings": {"cdrom": False, "file": ""},
    "dev_path_hid_keyboard": "/dev/hidg0",
    "dev_path_hid_mouse": "/dev/hidg1",
    "dev_path_hid_raw": "/dev/hidg2",
}

SCRIPTS = {
    "hidtest1.js": "// stored script (mock)\nlayout('US');\ntype('hello\\n');\n",
    "mousejiggle.js": "// keep the host awake\nwhile (true) { move(1,0); delay(30000); move(-1,0); delay(30000); }\n",
    "cosmouse.js": "// cosine mouse path\nfor (var i=0;i<360;i++) { move(2, Math.round(Math.sin(i/10)*4)); delay(10); }\n",
}
TMP = {}
JOBS = [7]
STARTUP = {"name": "initial"}


def _set_startup(b):
    if not isinstance(b.get("msg"), str):
        err("malformed request body: StringMessage has no field other than 'msg'")
    STARTUP["name"] = b["msg"]
    return {}

REFLEXES = [
    {"id": 1, "isActive": True, "oneShot": False, "immutable": True,
     "serviceStarted": {}, "bashScript": {"scriptName": "startup.sh"}},
    {"id": 2, "isActive": True, "oneShot": True, "immutable": False,
     "usbGadgetConnected": {}, "hidScript": {"scriptName": "hidtest1.js"}},
    {"id": 3, "isActive": False, "oneShot": False, "immutable": False,
     "dhcpLeaseGranted": {}, "log": {}},
]


def _add_reflex(b):
    for ta in b.get("TriggerActions", []):
        ta = dict(ta)
        ta["id"] = max([r["id"] for r in REFLEXES] + [0]) + 1
        REFLEXES.append(ta)
    return {"TriggerActions": REFLEXES}


def _remove_reflex(b):
    ids = {ta.get("id") for ta in b.get("TriggerActions", [])}
    REFLEXES[:] = [r for r in REFLEXES if r["id"] not in ids]
    return {"TriggerActions": REFLEXES}


def _update_reflex(b):
    for ta in b.get("TriggerActions", []):
        for i, r in enumerate(REFLEXES):
            if r["id"] == ta.get("id"):
                REFLEXES[i] = dict(r, **ta)
    return {"TriggerActions": REFLEXES}


def fs_create_temp(b):
    # TempDirOrFileRequest{ string dir = 1; string prefix = 2; bool onlyFolder = 3; }
    if not isinstance(b.get("dir"), str):
        err("malformed request body: invalid value for string field dir")
    if "onlyFolder" in b and not isinstance(b["onlyFolder"], bool):
        err("malformed request body: invalid value for bool field onlyFolder")
    name = f"{b.get('prefix','tmp')}{int(time.time()*1000) % 10**9}"
    path = "/tmp/" + name
    TMP[name] = ""
    return {"resultPath": path}


def fs_write(b):
    # WriteFileRequest{ AccessibleFolder folder; string filename; bool append;
    #                   bool mustNotExist; bytes data; }
    name = b.get("filename", "")
    if name.startswith("/"):
        err("filename must be relative")
    if not isinstance(b.get("data"), str):
        err("malformed request body: 'data' must be base64 bytes (did you send 'content'?)")
    try:
        text = base64.b64decode(b["data"]).decode("utf-8", "replace")
    except Exception as e:
        err(f"bad base64 in data: {e}")
    (TMP if b.get("folder", 0) == 0 else SCRIPTS)[name] = text
    return {}


def fs_read(b):
    # ReadFileRequest{ folder, filename, int64 start, int64 len }
    name = b.get("filename", "")
    src = (TMP if b.get("folder", 0) == 0 else SCRIPTS).get(name, "")
    start = int(b.get("start", 0) or 0)
    n = int(b.get("len", 0) or 0)
    if n < 0:
        err(f"negative read length {n}")
    if n > 8 << 20:
        err(f"read length {n} exceeds the 8388608 byte limit")
    chunk = src.encode()[start:start + n] if n else b""
    return {"readCount": str(len(chunk)), "data": base64.b64encode(chunk).decode()}


def fs_info(b):
    name = b.get("path", "").split("/")[-1]
    src = SCRIPTS.get(name, TMP.get(name, ""))
    return {"name": name, "size": str(len(src.encode())), "mode": 420,
            "modTime": str(int(time.time())), "isDir": False}


def run_job(b):
    if not isinstance(b.get("scriptPath"), str) or not b["scriptPath"]:
        err("scriptPath is required")
    if not (USB["use_HID_KEYBOARD"] or USB["use_HID_MOUSE"]):
        err("HIDScript not available (mouse and keyboard disabled)")
    JOBS.append((JOBS[-1] if JOBS else 0) + 1)
    return {"id": JOBS[-1]}


RPC = {
    "GetDeployedGadgetSetting": lambda b: USB,
    "DeployGadgetSetting": lambda b: (USB.update(b or {}), USB)[1],
    "StoreDeployedUSBSettings": lambda b: {},
    "GetWiFiState": lambda b: {
        "mode": 1, "channel": 6, "ssid": "HackProKP",
        "currentSettings": {"name": "default_ap", "disabled": False, "regulatory": "US",
                            "working_mode": 1, "auth_mode": 1, "channel": 6,
                            "hide_ssid": False, "nexmon": False,
                            "ap_BSS": {"SSID": "HackProKP", "PSK": ""}, "client_BSS_list": []}},
    "GetBluetoothControllerInformation": lambda b: {
        "address": base64.b64encode(b"\xb8\x27\xeb\x01\x02\x03").decode(),
        "bluetooth_version": 6, "manufacturer": 15, "name": "P4wnP1",
        "short_name": "P4wnP1", "is_available": True,
        "service_network_server_nap": True,
        "service_network_server_panu": False, "service_network_server_gn": False},
    "GetAllDeployedEthernetInterfaceSettings": lambda b: {"list": [
        {"name": "usb0", "mode": 2, "ipAddress4": "172.16.0.1", "netmask4": "255.255.255.252",
         "enabled": True, "settingsInUse": True},
        {"name": "wlan0", "mode": 2, "ipAddress4": "172.24.0.1", "netmask4": "255.255.255.0",
         "enabled": True, "settingsInUse": True},
        {"name": "eth0", "mode": 3, "ipAddress4": "", "netmask4": "", "enabled": False,
         "settingsInUse": False}]},
    "ListStoredHIDScripts": lambda b: {"msgArray": sorted(SCRIPTS)},
    "HIDGetRunningScriptJobs": lambda b: {"ids": list(JOBS)},
    "HIDRunScriptJob": run_job,
    "HIDCancelAllScriptJobs": lambda b: (JOBS.clear(), {})[1],
    "HIDCancelScriptJob": lambda b: (JOBS.remove(b["id"]) if b.get("id") in JOBS else None, {})[1],
    "HIDGetScriptJobResult": lambda b: {"job": {"id": b.get("id", 0)}, "isFinished": True,
                                        "resultJson": '{"typed":42,"layout":"US"}'},
    "FSCreateTempDirOrFile": fs_create_temp,
    "FSWriteFile": fs_write,
    "FSReadFile": fs_read,
    "FSGetFileInfo": fs_info,
    "GetDeployedTriggerActionSet": lambda b: {"Name": "default", "TriggerActions": REFLEXES},
    "FireActionGroupSend": lambda b: {},
    "ListStoredBashScripts": lambda b: {"msgArray": ["startup.sh", "servicestart.sh", "trigger-aware.sh"]},
    "DeployTriggerActionSetAdd": lambda b: _add_reflex(b),
    "DeployTriggerActionSetRemove": lambda b: _remove_reflex(b),
    "DeployTriggerActionSetUpdate": lambda b: _update_reflex(b),
    "ListStoredMasterTemplate": lambda b: {"msgArray": ["initial", "hid_only", "rogue_ap", "usb_net"]},
    # StringMessage{msg}. Returning the real field name matters: the console
    # previously SENT `templateName` here, which the bridge discarded, so this
    # silently set the boot default to "". A mock that accepted it would have
    # hidden the bug.
    "GetStartupMasterTemplate": lambda b: {"msg": STARTUP["name"]},
    "DeployStoredMasterTemplate": lambda b: {},
    "SetStartupMasterTemplate": lambda b: _set_startup(b),
    "DBBackup": lambda b: {},
    "Reboot": lambda b: {},
    "Shutdown": lambda b: {},
}


class Handler(SimpleHTTPRequestHandler):
    def __init__(self, *a, **k):
        super().__init__(*a, directory=ROOT, **k)

    def log_message(self, *a):
        pass

    def _json(self, code, obj):
        body = json.dumps(obj).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def _authed(self):
        hdr = self.headers.get("Authorization", "")
        return hdr.lower().startswith("bearer ") and hdr[7:].strip() == TOKEN

    def _same_origin(self):
        origin = self.headers.get("Origin")
        if not origin:
            return True
        return origin.split("//")[-1].split(":")[0] == self.headers.get("Host", "").split(":")[0]

    def _body(self):
        n = int(self.headers.get("Content-Length") or 0)
        if not n:
            return {}
        try:
            return json.loads(self.rfile.read(n) or b"{}")
        except Exception:
            return {}

    def do_POST(self):
        path = urlparse(self.path).path
        ctype = (self.headers.get("Content-Type") or "").split(";")[0].strip().lower()
        if path == "/api/auth/login":
            if ctype != "application/json":
                return self._json(415, {"error": "Content-Type: application/json required"})
            b = self._body()
            if b.get("username") == USER and b.get("password") == PASS:
                return self._json(200, {"token": TOKEN, "expires_at": int(time.time()) + 86400})
            return self._json(401, {"error": "invalid credentials"})
        if path == "/api/auth/logout":
            return self._json(204, {})
        if path == "/api/auth/changepw":
            if not self._authed():
                return self._json(401, {"error": "a valid bearer token is required to change a password"})
            if ctype != "application/json":
                return self._json(415, {"error": "Content-Type: application/json required"})
            return self._json(204, {})
        if path.startswith("/api/v1/rpc/"):
            if not self._same_origin():
                return self._json(403, {"error": "cross-origin requests are not permitted"})
            if not self._authed():
                return self._json(401, {"error": "missing Authorization header"})
            method = path[len("/api/v1/rpc/"):]
            if method not in RPC:
                return self._json(404, {"error": f'no such method "{method}"'})
            try:
                return self._json(200, RPC[method](self._body()))
            except RpcError as e:
                return self._json(400, {"error": str(e)})
            except Exception as e:  # noqa: BLE001 - mock, surface everything
                return self._json(500, {"error": f"{type(e).__name__}: {e}"})
        self._json(404, {"error": "not found"})

    def do_GET(self):
        path = urlparse(self.path).path
        if path == "/api/auth/whoami":
            if not self._authed():
                return self._json(401, {"error": "unauthenticated"})
            return self._json(200, {"username": USER, "expires_at": int(time.time()) + 86400})
        if path == "/api/v1/rpc":
            if not self._same_origin():
                return self._json(403, {"error": "cross-origin requests are not permitted"})
            if not self._authed():
                return self._json(401, {"error": "unauthenticated"})
            return self._json(200, {"methods": sorted(RPC)})
        if path == "/api/v1/events":
            if not self._authed():
                return self._json(401, {"error": "unauthenticated"})
            self.send_response(200)
            self.send_header("Content-Type", "text/event-stream")
            self.send_header("Cache-Control", "no-cache")
            self.end_headers()
            srcs = ["TriggerAction", "USB", "WiFi", "HIDScript", "service"]
            msgs = ["gadget settings deployed", "AP started on channel 6",
                    "dhcp lease 172.16.0.2 granted to DESKTOP-7F2K",
                    "HIDScript job 7 started", "wpa_supplicant stopped"]
            i = 0
            try:
                while True:
                    ev = {"type": 1, "values": [
                        {"tstring": srcs[i % len(srcs)]},
                        {"tint64": str(1 if i % 5 else 2)},
                        {"tstring": msgs[i % len(msgs)]},
                        {"tint64": str(int(time.time() * 1000))}]}
                    self.wfile.write(f"data: {json.dumps(ev)}\n\n".encode())
                    self.wfile.flush()
                    if i == 2:
                        usb = {"type": 4, "values": [{"tint64": "1"}]}
                        self.wfile.write(f"data: {json.dumps(usb)}\n\n".encode())
                        self.wfile.flush()
                    i += 1
                    time.sleep(2)
            except (BrokenPipeError, ConnectionResetError):
                return
        return super().do_GET()


if __name__ == "__main__":
    print(f"mock P4wnP1 on http://127.0.0.1:8111   (sign in: {USER} / {PASS})")
    ThreadingHTTPServer(("127.0.0.1", 8111), Handler).serve_forever()
