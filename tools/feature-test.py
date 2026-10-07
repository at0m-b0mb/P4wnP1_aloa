#!/usr/bin/env python3
"""Exercise EVERY RPC against a running P4wnP1 service and report what works.

WHY

make smoke proves the service comes up and the plumbing is sound. It does not
prove a single FEATURE works. Every console bug found so far -- four wrong
request shapes in the HIDScript path, a boot default that was erased, an API
emitting field names nothing reads -- lived in territory no test visited.

This visits all 83 of them. Each RPC gets a realistic payload and a stated
expectation, and the result is one of:

  PASS      did what it should
  EXPECTED  failed for a reason that is CORRECT in a container (no USB device
            controller, no radio, no bluetoothd). Recorded, not excused: the
            message is matched against a pattern, so a DIFFERENT failure still
            shows up as FAIL.
  FAIL      genuinely broken -- wrong shape, unexpected error, panic
  SKIP      deliberately not called (Reboot and Shutdown would end the run)

Ordering matters: stores run before lists, lists before deletes, so each area
is checked as a round trip rather than in isolation.

Run via tools/feature-test.sh, which supplies the service.
"""
import json
import re
import sys
import urllib.error
import urllib.request

BASE = "http://127.0.0.1:8000"
USER = "admin"
PASSWORD = "feature-test-password-long"

# Failures that are correct when there is no real hardware. Matched as a regex
# against the error text, so an unexpected failure in the same RPC still fails.
NO_HARDWARE = re.compile(
    r"gadget .* doesn't exist|USB subsystem not available|UDC|"
    r"HIDScript not available|mouse and keyboard disable|"
    r"WiFi (subsystem is unavailable|interface)|wpa_supplicant|hostapd|iw |"
    r"bluetooth|bluez|Couldn't access|controller|"
    r"no such file or directory|No such file|"
    r"No stored \(or used\) settings for ethernet interface|"
    r"Not a managed network interface|sub system GPIO not available",
    re.I,
)

results = []
token = None


def call(method, payload=None):
    body = None if payload is None else json.dumps(payload).encode()
    req = urllib.request.Request(
        f"{BASE}/api/v1/rpc/{method}", data=body or b"{}", method="POST",
        headers={"Content-Type": "application/json",
                 "Authorization": f"Bearer {token}"})
    try:
        with urllib.request.urlopen(req, timeout=25) as r:
            return r.status, json.loads(r.read() or b"{}")
    except urllib.error.HTTPError as e:
        raw = e.read().decode(errors="replace")
        try:
            return e.code, json.loads(raw)
        except Exception:
            return e.code, {"error": raw[:300]}
    except Exception as e:  # noqa: BLE001
        return 0, {"error": f"{type(e).__name__}: {e}"}


def check(area, method, payload=None, want=None, note="", label=None):
    """want: None = expect success. callable = predicate on the response body.

    `label` is what appears in the report; `method` is always the real RPC name,
    so a human-readable label can never end up in the URL.
    """
    shown = label or method
    status, body = call(method, payload)
    err = body.get("error") if isinstance(body, dict) else None

    if status == 200:
        if want is not None:
            ok, why = want(body)
            if not ok:
                results.append((area, shown, "FAIL", why, body))
                return body
        results.append((area, shown, "PASS", note, body))
        return body

    if err and NO_HARDWARE.search(err):
        results.append((area, shown, "EXPECTED", err[:110], body))
        return None

    results.append((area, shown, "FAIL", f"HTTP {status}: {str(err)[:150]}", body))
    return None


def skip(area, method, why):
    results.append((area, method, "SKIP", why, None))


def has(*keys):
    def f(b):
        missing = [k for k in keys if k not in b]
        return (not missing, "response missing " + ", ".join(missing) if missing else "")
    return f


def login():
    global token
    req = urllib.request.Request(
        f"{BASE}/api/auth/login",
        data=json.dumps({"username": USER, "password": PASSWORD}).encode(),
        headers={"Content-Type": "application/json"})
    with urllib.request.urlopen(req, timeout=15) as r:
        token = json.loads(r.read())["token"]
    return token


def main():
    login()

    # ---------------- System ----------------
    check("System", "EchoRequest", {"msg": "ping"},
          lambda b: (b.get("msg") == "ping", f"echo returned {b!r}"))
    check("System", "GetLEDSettings", None, has("blink_count"))
    check("System", "SetLEDSettings", {"blink_count": 3})
    check("System", "GetAvailableGpios", None, has("msgArray"))
    skip("System", "Reboot", "would end the test run")
    skip("System", "Shutdown", "would end the test run")
    skip("System", "EventListen", "streaming; covered by the SSE checks in smoke-test.sh")

    # ---------------- Files ----------------
    tmp = check("Files", "FSCreateTempDirOrFile",
                {"dir": "", "prefix": "ftest", "onlyFolder": False}, has("resultPath"))
    base = tmp["resultPath"].rsplit("/", 1)[-1] if tmp else "ftest-fallback"
    import base64
    payload = base64.b64encode(b"layout('US');\ntype('feature test');\n").decode()
    check("Files", "FSWriteFile",
          {"folder": 0, "filename": base, "data": payload, "append": False})
    check("Files", "FSGetFileInfo", {"path": f"/tmp/{base}"},
          lambda b: (int(b.get("size", 0)) > 0, f"size was {b.get('size')!r}"))
    check("Files", "FSReadFile", {"folder": 0, "filename": base, "start": 0, "len": 64},
          lambda b: (int(b.get("readCount", 0)) > 0, "read returned nothing"))
    check("Files", "ListStoredHIDScripts", None, has("msgArray"))
    check("Files", "ListStoredBashScripts", None, has("msgArray"))

    # ---------------- USB ----------------
    usb = check("USB", "GetDeployedGadgetSetting", None)
    if usb:
        # Field names must be the proto ones, or the console cannot read them.
        missing = [k for k in ("use_HID_KEYBOARD", "rndis_settings") if k not in usb]
        if missing:
            results.append(("USB", "GetDeployedGadgetSetting(field names)", "FAIL",
                            f"missing {missing}; console reads proto names", None))
        else:
            results.append(("USB", "GetDeployedGadgetSetting(field names)", "PASS",
                            "proto field names", None))
    check("USB", "DeployGadgetSetting",
          {"enabled": True, "vid": "0x1d6b", "pid": "0x0137",
           "use_HID_KEYBOARD": True, "use_RNDIS": True,
           "rndis_settings": {"host_addr": "02:1a:11:00:00:01",
                              "dev_addr": "02:1a:11:00:00:02"}})
    check("USB", "StoreUSBSettings",
          {"TemplateName": "ftest_usb",
           "settings": {"enabled": True, "vid": "0x1d6b", "pid": "0x0137",
                        "use_HID_KEYBOARD": True}})
    check("USB", "ListStoredUSBSettings", None,
          lambda b: ("ftest_usb" in (b.get("msgArray") or []),
                     f"stored template not listed: {b.get('msgArray')}"))
    check("USB", "GetStoredUSBSettings", {"msg": "ftest_usb"}, has("vid"))
    check("USB", "DeployStoredUSBSettings", {"msg": "ftest_usb"})
    check("USB", "StoreDeployedUSBSettings", {"msg": "ftest_usb2"})
    check("USB", "DeleteStoredUSBSettings", {"msg": "ftest_usb"})
    check("USB", "ListUmsImageCdrom", None, has("msgArray"))
    check("USB", "ListUmsImageFlashdrive", None, has("msgArray"))
    check("USB", "MountUMSFile", {"cdrom": False, "file": "nonexistent.img"})

    # ---------------- HID ----------------
    check("HID", "HIDGetRunningScriptJobs", None, has("ids"))
    check("HID", "HIDRunScriptJob", {"scriptPath": f"/tmp/{base}", "timeoutSeconds": 3})
    check("HID", "HIDRunScript", {"scriptPath": f"/tmp/{base}", "timeoutSeconds": 3})
    check("HID", "HIDGetRunningJobState", {"id": 1})
    check("HID", "HIDGetScriptJobResult", {"id": 1})
    check("HID", "HIDCancelScriptJob", {"id": 1})
    check("HID", "HIDCancelAllScriptJobs", None)

    # ---------------- WiFi ----------------
    check("WiFi", "GetWiFiState", None)
    wifi_settings = {
        "name": "ftest_wifi", "disabled": True, "regulatory": "US",
        "working_mode": 1, "auth_mode": 1, "channel": 6,
        "ap_BSS": {"SSID": "ftest-ap", "PSK": "a-genuinely-chosen-passphrase"},
        "client_BSS_list": [],
    }
    check("WiFi", "StoreWifiSettings",
          {"TemplateName": "ftest_wifi", "settings": wifi_settings})
    check("WiFi", "ListStoredWifiSettings", None,
          lambda b: ("ftest_wifi" in (b.get("msgArray") or []),
                     f"not listed: {b.get('msgArray')}"))
    check("WiFi", "GetStoredWifiSettings", {"msg": "ftest_wifi"}, has("name"))
    check("WiFi", "DeployWiFiSettings", wifi_settings)
    check("WiFi", "DeployStoredWifiSettings", {"msg": "ftest_wifi"})
    check("WiFi", "StoreDeployedWifiSettings", {"msg": "ftest_wifi2"})
    check("WiFi", "DeleteStoredWifiSettings", {"msg": "ftest_wifi"})
    # Deliberately Unimplemented: its body used to be panic("implement me"),
    # which any caller could use to end the process. 501 is the correct answer.
    st, body = call("ListenWiFiStateChanges")
    if st == 501:
        results.append(("WiFi", "ListenWiFiStateChanges", "PASS",
                        "501 Unimplemented, by design", body))
    else:
        results.append(("WiFi", "ListenWiFiStateChanges", "FAIL",
                        f"expected 501, got {st}", body))

    # ---------------- Bluetooth ----------------
    check("Bluetooth", "GetBluetoothControllerInformation", None)
    check("Bluetooth", "GetBluetoothAgentSettings", None)
    check("Bluetooth", "DeployBluetoothAgentSettings", {"pin": "1337"})
    check("Bluetooth", "ListStoredBluetoothSettings", None, has("msgArray"))
    check("Bluetooth", "StoreBluetoothSettings",
          {"TemplateName": "ftest_bt", "settings": {"as": {"pin": "1337"}}})
    check("Bluetooth", "GetStoredBluetoothSettings", {"msg": "ftest_bt"})
    check("Bluetooth", "DeployStoredBluetoothSettings", {"msg": "ftest_bt"})
    check("Bluetooth", "DeployBluetoothSettings", {"as": {"pin": "1337"}})
    check("Bluetooth", "DeployBluetoothControllerInformation", {"name": "ftest"})
    check("Bluetooth", "SetBluetoothNetworkService",
          {"register_or_unregister": False, "server_or_connect": True, "type": 0})
    check("Bluetooth", "StoreDeployedBluetoothSettings", {"msg": "ftest_bt2"})
    check("Bluetooth", "DeleteStoredBluetoothSettings", {"msg": "ftest_bt"})

    # ---------------- Network ----------------
    check("Network", "GetAllDeployedEthernetInterfaceSettings", None, has("list"))
    eth = {"name": "usb0", "mode": 0, "ipAddress4": "172.16.0.1",
           "netmask4": "255.255.255.252", "enabled": True}
    check("Network", "GetDeployedEthernetInterfaceSettings", {"msg": "usb0"})
    check("Network", "StoreEthernetInterfaceSettings",
          {"TemplateName": "ftest_eth", "settings": eth})
    # Asymmetry worth pinning: you STORE as "ftest_eth" and it comes back as
    # "usb0_ftest_eth" -- the store prefixes the interface name, and every
    # read-side call wants the prefixed key.
    ETH_KEY = "usb0_ftest_eth"
    check("Network", "ListStoredEthernetInterfaceSettings", None,
          lambda b: (ETH_KEY in (b.get("msgArray") or []),
                     f"expected the interface-prefixed key {ETH_KEY!r}, got {b.get('msgArray')}"),
          note="stored name is prefixed with the interface")
    check("Network", "GetStoredEthernetInterfaceSettings", {"msg": ETH_KEY}, has("name"))
    check("Network", "DeployEthernetInterfaceSettings", eth)
    check("Network", "DeployStoredEthernetInterfaceSettings", {"msg": ETH_KEY})
    check("Network", "DeleteStoredEthernetInterfaceSettings", {"msg": ETH_KEY})

    # ---------------- Triggers ----------------
    ta = {"isActive": True, "oneShot": False, "immutable": False,
          "groupReceive": {"groupName": "ftest", "value": 1},
          "log": {}}
    added = check("Triggers", "DeployTriggerActionSetAdd", {"TriggerActions": [ta]},
                  lambda b: (len(b.get("TriggerActions") or []) > 0, "nothing added"))
    new_id = added["TriggerActions"][0]["id"] if added and added.get("TriggerActions") else None
    check("Triggers", "GetDeployedTriggerActionSet", None, has("TriggerActions"))
    if new_id is not None:
        upd = dict(ta, id=new_id, isActive=False)
        check("Triggers", "DeployTriggerActionSetUpdate", {"TriggerActions": [upd]})
    check("Triggers", "StoreTriggerActionSet",
          {"Name": "ftest_tas", "TriggerActions": [ta]})
    check("Triggers", "ListStoredTriggerActionSets", None,
          lambda b: ("ftest_tas" in (b.get("msgArray") or []),
                     f"not listed: {b.get('msgArray')}"))
    check("Triggers", "DeployStoredTriggerActionSetAdd", {"msg": "ftest_tas"})
    check("Triggers", "DeployStoredTriggerActionSetReplace", {"msg": "ftest_tas"})
    check("Triggers", "FireActionGroupSend", {"groupName": "ftest", "value": 1})
    # DeployStoredTriggerActionSetReplace above swapped the whole deployed set,
    # so the id captured earlier is gone. Add a fresh one to remove.
    again = check("Triggers", "DeployTriggerActionSetAdd", {"TriggerActions": [ta]},
                  lambda b: (len(b.get("TriggerActions") or []) > 0, "nothing added"),
                  label="DeployTriggerActionSetAdd (re-add for removal)")
    rm_id = again["TriggerActions"][0]["id"] if again and again.get("TriggerActions") else None
    if rm_id is not None:
        check("Triggers", "DeployTriggerActionSetRemove",
              {"TriggerActions": [{"id": rm_id}]})
    check("Triggers", "DeleteStoredTriggerActionSet", {"msg": "ftest_tas"})
    skip("Triggers", "WaitTriggerGroupReceive", "blocks until the value arrives")
    skip("Triggers", "DeployTriggerActionSetReplace",
         "would wipe the deployed set the rest of the run depends on")

    # ---------------- Templates ----------------
    check("Templates", "ListStoredMasterTemplate", None, has("msgArray"))
    # Store a USB template this master can actually reference. The earlier
    # ftest_usb was deleted, and StoreDeployedUSBSettings cannot run without a
    # gadget, so make one here explicitly.
    check("Templates", "StoreUSBSettings",
          {"TemplateName": "ftest_master_usb",
           "settings": {"enabled": True, "vid": "0x1d6b", "pid": "0x0137"}})
    check("Templates", "StoreMasterTemplate",
          {"TemplateName": "ftest_master",
           "template": {"template_name_usb": "ftest_master_usb"}})
    check("Templates", "GetStoredMasterTemplate", {"msg": "ftest_master"})
    check("Templates", "DeployMasterTemplate", {"template_name_usb": "ftest_master_usb"})
    check("Templates", "DeployStoredMasterTemplate", {"msg": "ftest_master"})
    check("Templates", "SetStartupMasterTemplate", {"msg": "ftest_master"})
    check("Templates", "GetStartupMasterTemplate", None,
          lambda b: (b.get("msg") == "ftest_master",
                     f"startup template is {b.get('msg')!r}, expected ftest_master"))
    check("Templates", "DBBackup", {"msg": "ftest_backup"})
    check("Templates", "ListStoredDBBackups", None,
          lambda b: ("ftest_backup.db" in (b.get("msgArray") or []),
                     f"backup not listed: {b.get('msgArray')}"),
          note="backups are listed with a .db suffix")
    check("Templates", "DeleteStoredMasterTemplate", {"msg": "ftest_master"})
    skip("Templates", "DBRestore", "would replace the running database mid-run")

    # ---------------- report ----------------
    by = {}
    for area, method, verdict, note, _ in results:
        by.setdefault(area, []).append((method, verdict, note))

    C = {"PASS": "\033[1;32m", "FAIL": "\033[1;31m",
         "EXPECTED": "\033[1;33m", "SKIP": "\033[1;90m"}
    counts = {"PASS": 0, "FAIL": 0, "EXPECTED": 0, "SKIP": 0}

    for area in sorted(by):
        print(f"\n  \033[1m{area}\033[0m")
        for method, verdict, note in by[area]:
            counts[verdict] += 1
            tail = f"  {note}" if note else ""
            print(f"    {C[verdict]}{verdict:<9}\033[0m {method}{tail}")

    total = sum(counts.values())
    print(f"\n  ---- {total} RPC checks: "
          f"{counts['PASS']} passed, {counts['FAIL']} FAILED, "
          f"{counts['EXPECTED']} expected-without-hardware, {counts['SKIP']} skipped ----")
    return 1 if counts["FAIL"] else 0


if __name__ == "__main__":
    sys.exit(main())
