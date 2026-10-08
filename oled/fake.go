package oled

import (
	"fmt"
	"sort"
	"strings"
)

// FakeClient is a device that exists only in memory.
//
// Not a test fixture tucked into a _test.go file: the simulator uses it too,
// so the UI can be driven on a laptop by someone who has no Raspberry Pi, no
// OLED HAT, or -- as happened here -- no SD card reader to flash one with.
// It also records every call, so a test can assert that pressing a key
// actually reached the device rather than only that the screen changed.
type FakeClient struct {
	Stored  map[Kind][]string
	Toggles []Toggle
	Jobs    []Job
	St      Status
	Startup string

	// Fail makes the named RPC-ish operation return an error, so the error
	// paths get exercised as thoroughly as the happy ones.
	Fail map[string]string

	Calls              []string
	LED                int
	Rebooted, ShutDown bool

	// FinishJobsImmediately makes StartHIDScript return an id that is
	// already finished, so a screen test does not have to drive the poll.
	FinishJobsImmediately bool
	nextJobID             int
}

// NewFakeClient returns a device with plausible contents.
func NewFakeClient() *FakeClient {
	return &FakeClient{
		Stored: map[Kind][]string{
			KindMasterTemplate:    {"default", "usb_ethernet_only", "keyboard_attack", "silent_recon"},
			KindUSBSettings:       {"rndis_hid", "storage_only"},
			KindWifiSettings:      {"ap_wpa2", "client_home", "client_office"},
			KindEthernetSettings:  {"usbeth_dhcp_server", "wlan0_dhcp_client"},
			KindBluetoothSettings: {"nap_default"},
			KindTriggerActionSet:  {"on_boot", "on_usb_attach"},
			KindHIDScript:         {"hello.js", "win_recon.js", "mac_prompt.js", "linux_enum.js", "exfil_wifi.js", "very_long_payload_name_here.js"},
			KindBashScript:        {"startup.sh", "servicestart.sh"},
			KindDBBackup:          {"before_demo.db"},
		},
		Toggles: []Toggle{
			{"use_HID_KEYBOARD", "Keyboard", true},
			{"use_HID_MOUSE", "Mouse", false},
			{"use_HID_RAW", "Raw HID", false},
			{"use_RNDIS", "RNDIS net", true},
			{"use_CDC_ECM", "CDC ECM net", true},
			{"use_UMS", "Mass storage", false},
			{"use_SERIAL", "Serial", false},
		},
		Jobs: []Job{{ID: 3, Name: "win_recon.js"}},
		St: Status{
			USBHost:      "composed",
			USBFunctions: []string{"Keyboard", "RNDIS net", "CDC ECM net"},
			Interfaces: []Iface{
				{"usbeth", "172.16.0.1", "DHCP_SERVER"},
				{"wlan0", "172.24.0.1", "DHCP_SERVER"},
				{"bteth", "172.26.0.1", "DHCP_SERVER"},
			},
			WiFi: "AP P4wnP1", WiFiOK: true,
			Reflexes: 4, ReflexesArmed: 4,
			RunningJobs: 1,
		},
		Startup: "default",
		Fail:    map[string]string{},
	}
}

func (f *FakeClient) note(format string, args ...interface{}) {
	f.Calls = append(f.Calls, fmt.Sprintf(format, args...))
}

func (f *FakeClient) fail(op string) error {
	if msg, ok := f.Fail[op]; ok {
		return fmt.Errorf("%s", msg)
	}
	return nil
}

func (f *FakeClient) Status() (Status, error) {
	f.note("Status")
	if err := f.fail("Status"); err != nil {
		return Status{}, err
	}
	return f.St, nil
}

func (f *FakeClient) List(k Kind) ([]string, error) {
	f.note("List(%s)", k.Label())
	if err := f.fail("List"); err != nil {
		return nil, err
	}
	out := append([]string(nil), f.Stored[k]...)
	sort.Strings(out)
	return out, nil
}

func (f *FakeClient) DeployStored(k Kind, name string) error {
	f.note("Deploy(%s,%s)", k.Label(), name)
	return f.fail("Deploy")
}

func (f *FakeClient) DeleteStored(k Kind, name string) error {
	f.note("Delete(%s,%s)", k.Label(), name)
	if err := f.fail("Delete"); err != nil {
		return err
	}
	cur := f.Stored[k]
	out := cur[:0]
	for _, n := range cur {
		if n != name {
			out = append(out, n)
		}
	}
	f.Stored[k] = out
	return nil
}

// StartHIDScript begins a job and returns at once, like the real one.
//
// FinishJobsImmediately makes a started job complete before the next poll,
// which is what most screen tests want. Leave it false to watch the
// "running" state.
func (f *FakeClient) StartHIDScript(name string) (int, error) {
	f.note("StartHID(%s)", name)
	if err := f.fail("RunHID"); err != nil {
		return 0, err
	}
	f.nextJobID++
	id := f.nextJobID
	if !f.FinishJobsImmediately {
		f.Jobs = append(f.Jobs, Job{ID: id, Name: name})
	}
	return id, nil
}

func (f *FakeClient) JobRunning(id int) (bool, error) {
	f.note("JobRunning(%d)", id)
	if err := f.fail("RunningJobs"); err != nil {
		return false, err
	}
	for _, j := range f.Jobs {
		if j.ID == id {
			return true, nil
		}
	}
	return false, nil
}

func (f *FakeClient) CollectResult(id int) (string, error) {
	f.note("CollectResult(%d)", id)
	if err := f.fail("CollectResult"); err != nil {
		return "", err
	}
	return "typed into the host, 142 keystrokes, no errors", nil
}

func (f *FakeClient) RunningJobs() ([]Job, error) {
	f.note("RunningJobs")
	if err := f.fail("RunningJobs"); err != nil {
		return nil, err
	}
	return append([]Job(nil), f.Jobs...), nil
}

func (f *FakeClient) CancelAllJobs() error {
	f.note("CancelAllJobs")
	if err := f.fail("CancelAllJobs"); err != nil {
		return err
	}
	f.Jobs = nil
	return nil
}

func (f *FakeClient) USBFunctions() ([]Toggle, error) {
	f.note("USBFunctions")
	if err := f.fail("USBFunctions"); err != nil {
		return nil, err
	}
	return append([]Toggle(nil), f.Toggles...), nil
}

func (f *FakeClient) SetUSBFunctions(on map[string]bool) error {
	keys := make([]string, 0, len(on))
	for k := range on {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%v", k, on[k]))
	}
	f.note("SetUSB(%s)", strings.Join(parts, ","))
	if err := f.fail("SetUSB"); err != nil {
		return err
	}
	for i := range f.Toggles {
		if v, ok := on[f.Toggles[i].Key]; ok {
			f.Toggles[i].On = v
		}
	}
	return nil
}

func (f *FakeClient) SetStartupTemplate(name string) error {
	f.note("SetStartup(%s)", name)
	if err := f.fail("SetStartup"); err != nil {
		return err
	}
	f.Startup = name
	return nil
}

func (f *FakeClient) StartupTemplate() (string, error) {
	f.note("StartupTemplate")
	return f.Startup, f.fail("StartupTemplate")
}

func (f *FakeClient) SetLEDBlink(n int) error {
	f.note("SetLED(%d)", n)
	if err := f.fail("SetLED"); err != nil {
		return err
	}
	f.LED = n
	return nil
}

func (f *FakeClient) Reboot() error {
	f.note("Reboot")
	if err := f.fail("Reboot"); err != nil {
		return err
	}
	f.Rebooted = true
	return nil
}

func (f *FakeClient) Shutdown() error {
	f.note("Shutdown")
	if err := f.fail("Shutdown"); err != nil {
		return err
	}
	f.ShutDown = true
	return nil
}

// CountCalls reports how many recorded calls contain sub. "Did it happen" is
// not always the question: a refresh that fires twice per press is a bug you
// can only see by counting.
func (f *FakeClient) CountCalls(sub string) int {
	n := 0
	for _, c := range f.Calls {
		if strings.Contains(c, sub) {
			n++
		}
	}
	return n
}

func (f *FakeClient) DescribeJob(id int) string {
	f.note("DescribeJob(%d)", id)
	for _, j := range f.Jobs {
		if j.ID == id {
			return j.Name
		}
	}
	return ""
}

// Called reports whether any recorded call contains sub.
func (f *FakeClient) Called(sub string) bool {
	for _, c := range f.Calls {
		if strings.Contains(c, sub) {
			return true
		}
	}
	return false
}
