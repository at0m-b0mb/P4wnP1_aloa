package oled

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client is everything the UI needs from the device. An interface, so the
// simulator and the tests drive the real screens against a fake device and
// the only thing that changes is where the numbers come from.
//
// Deliberately narrow and shaped like the UI's questions rather than like the
// 82-method RPC surface: the screens should not know that "deploy a stored
// WiFi config" and "deploy a stored USB config" are different endpoints.
type Client interface {
	Status() (Status, error)

	// List returns the names stored under a kind.
	List(kind Kind) ([]string, error)
	// DeployStored applies a stored item by name.
	DeployStored(kind Kind, name string) error
	// DeleteStored removes one.
	DeleteStored(kind Kind, name string) error

	// RunHIDScript types a stored payload into the attached host.
	RunHIDScript(name string, background bool) (string, error)
	RunningJobs() ([]Job, error)
	// DescribeJob labels one running job. Separate from RunningJobs because
	// it costs an extra call per job and the dashboard only wants the count.
	DescribeJob(id int) string
	CancelAllJobs() error

	// USB composition.
	USBFunctions() ([]Toggle, error)
	SetUSBFunctions(on map[string]bool) error

	SetStartupTemplate(name string) error
	StartupTemplate() (string, error)

	SetLEDBlink(count int) error
	Reboot() error
	Shutdown() error
}

// Kind is a category of stored thing. One enum instead of a method per
// category keeps the generic picker generic.
type Kind int

const (
	KindMasterTemplate Kind = iota
	KindUSBSettings
	KindWifiSettings
	KindEthernetSettings
	KindBluetoothSettings
	KindTriggerActionSet
	KindHIDScript
	KindBashScript
	KindDBBackup
)

var kindMeta = map[Kind]struct {
	Label      string
	ListRPC    string
	DeployRPC  string
	DeleteRPC  string
	Deployable bool
	Deletable  bool
}{
	KindMasterTemplate:    {"Loadouts", "ListStoredMasterTemplate", "DeployStoredMasterTemplate", "DeleteStoredMasterTemplate", true, true},
	KindUSBSettings:       {"USB configs", "ListStoredUSBSettings", "DeployStoredUSBSettings", "DeleteStoredUSBSettings", true, true},
	KindWifiSettings:      {"WiFi configs", "ListStoredWifiSettings", "DeployStoredWifiSettings", "DeleteStoredWifiSettings", true, true},
	KindEthernetSettings:  {"Net configs", "ListStoredEthernetInterfaceSettings", "DeployStoredEthernetInterfaceSettings", "DeleteStoredEthernetInterfaceSettings", true, true},
	KindBluetoothSettings: {"BT configs", "ListStoredBluetoothSettings", "DeployStoredBluetoothSettings", "DeleteStoredBluetoothSettings", true, true},
	KindTriggerActionSet:  {"Reflex sets", "ListStoredTriggerActionSets", "DeployStoredTriggerActionSetAdd", "DeleteStoredTriggerActionSet", true, true},
	KindHIDScript:         {"Payloads", "ListStoredHIDScripts", "", "", false, false},
	KindBashScript:        {"Scripts", "ListStoredBashScripts", "", "", false, false},
	KindDBBackup:          {"Backups", "ListStoredDBBackups", "DBRestore", "", true, false},
}

func (k Kind) Label() string    { return kindMeta[k].Label }
func (k Kind) Deployable() bool { return kindMeta[k].Deployable }
func (k Kind) Deletable() bool  { return kindMeta[k].Deletable }

// Status is the one-screen summary of what the device is doing.
type Status struct {
	USBHost       string // "attached" / "none" / "unknown"
	USBFunctions  []string
	Interfaces    []Iface
	WiFi          string
	WiFiOK        bool
	Reflexes      int
	ReflexesArmed int
	RunningJobs   int
	Err           string // set when part of the status could not be read
}

type Iface struct {
	Name string
	IP   string
	Mode string
}

type Job struct {
	ID   int
	Name string
}

type Toggle struct {
	Key   string // the proto field name, e.g. "use_HID_KEYBOARD"
	Label string // what the operator sees
	On    bool
}

// usbToggles is the subset of the USB composition worth exposing on a 21-column
// screen, in the order an operator thinks about them.
var usbToggles = []struct{ Key, Label string }{
	{"use_HID_KEYBOARD", "Keyboard"},
	{"use_HID_MOUSE", "Mouse"},
	{"use_HID_RAW", "Raw HID"},
	{"use_RNDIS", "RNDIS net"},
	{"use_CDC_ECM", "CDC ECM net"},
	{"use_UMS", "Mass storage"},
	{"use_SERIAL", "Serial"},
}

// --- HTTP implementation ----------------------------------------------------

// APIClient talks to the local P4wnP1 service over its JSON API.
//
// It authenticates with the machine-local credential the service writes to
// /run/p4wnp1/local.token -- the same mechanism the device's own startup and
// trigger scripts use. That is the whole reason this daemon needs no password
// and no configuration: it is a local script like any other, holding an
// ordinary session that expires and can be revoked.
type APIClient struct {
	BaseURL   string
	TokenPath string
	HTTP      *http.Client

	token string
}

func NewAPIClient(baseURL, tokenPath string) *APIClient {
	return &APIClient{
		BaseURL:   strings.TrimRight(baseURL, "/"),
		TokenPath: tokenPath,
		// A short timeout: the UI must stay responsive, and every call here
		// is to a service on the same box. A wedged request should surface as
		// an error on screen, not a frozen menu.
		HTTP: &http.Client{Timeout: 6 * time.Second},
	}
}

// call POSTs a JSON body to one RPC and decodes the reply into out.
func (c *APIClient) call(method string, in interface{}, out interface{}) error {
	body := []byte("{}")
	if in != nil {
		var err error
		if body, err = json.Marshal(in); err != nil {
			return err
		}
	}
	req, err := http.NewRequest(http.MethodPost, c.BaseURL+"/api/v1/rpc/"+method, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if err := c.authorise(req); err != nil {
		return err
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("service unreachable")
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	if resp.StatusCode == http.StatusUnauthorized {
		// The local credential is re-issued when sessions are revoked, so a
		// 401 usually means ours is stale rather than that we are unwelcome.
		// Drop it and let the next call pick up the new one.
		c.token = ""
		return fmt.Errorf("not authorised")
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s", apiErrMessage(raw, resp.StatusCode))
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(raw, out)
}

// apiErrMessage digs the service's own message out of an error body, because
// "deploy failed: no WiFi adapter" fits on the screen and is worth reading,
// while "HTTP 503" is not.
func apiErrMessage(raw []byte, code int) string {
	var e struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(raw, &e) == nil && e.Error != "" {
		return e.Error
	}
	switch code {
	case http.StatusServiceUnavailable:
		return "hardware not available"
	case http.StatusForbidden:
		return "refused"
	}
	return fmt.Sprintf("error %d", code)
}

func (c *APIClient) authorise(req *http.Request) error {
	if c.token == "" {
		tok, err := readLocalToken(c.TokenPath)
		if err != nil || tok == "" {
			return fmt.Errorf("no local credential (is the service running?)")
		}
		c.token = tok
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	return nil
}

type stringArray struct {
	MsgArray []string `json:"msgArray"`
}

func (c *APIClient) List(kind Kind) ([]string, error) {
	m := kindMeta[kind]
	if m.ListRPC == "" {
		return nil, fmt.Errorf("%s cannot be listed", m.Label)
	}
	var out stringArray
	if err := c.call(m.ListRPC, nil, &out); err != nil {
		return nil, err
	}
	return out.MsgArray, nil
}

func (c *APIClient) DeployStored(kind Kind, name string) error {
	m := kindMeta[kind]
	if m.DeployRPC == "" {
		return fmt.Errorf("%s cannot be deployed", m.Label)
	}
	return c.call(m.DeployRPC, map[string]string{"msg": name}, nil)
}

func (c *APIClient) DeleteStored(kind Kind, name string) error {
	m := kindMeta[kind]
	if m.DeleteRPC == "" {
		return fmt.Errorf("%s cannot be deleted", m.Label)
	}
	return c.call(m.DeleteRPC, map[string]string{"msg": name}, nil)
}

func (c *APIClient) SetStartupTemplate(name string) error {
	return c.call("SetStartupMasterTemplate", map[string]string{"msg": name}, nil)
}

func (c *APIClient) StartupTemplate() (string, error) {
	var out struct {
		Msg string `json:"msg"`
	}
	if err := c.call("GetStartupMasterTemplate", nil, &out); err != nil {
		return "", err
	}
	return out.Msg, nil
}

// HIDScriptDir is where the service keeps stored payloads.
//
// It has to be spelled out here because the two RPCs disagree about what a
// payload is called: ListStoredHIDScripts returns BARE NAMES ("hidtest1.js"),
// and HIDRunScript takes an ABSOLUTE PATH, which it then checks is inside this
// directory or /tmp. Feeding the name straight back from one to the other --
// which is what this client did -- is rejected on the device with
//
//	HIDScript path rejected: path must be absolute
//
// and that is exactly what the OLED showed the first time a payload was run on
// real hardware. Nothing off-device catches it: the fake client never saw a
// path, so every test passed.
//
// Kept in step with common.PATH_HID_SCRIPTS by TestHIDScriptDirMatchesService.
const HIDScriptDir = "/usr/local/P4wnP1/HIDScripts"

// hidScriptPath turns a stored payload name into the absolute path the service
// demands.
//
// Only a bare name is accepted. This screen runs what ListStoredHIDScripts
// reported and nothing else, so a separator in the name means the list is not
// what we think it is; refusing is both the narrower contract and a better
// thing to read on a 21-column screen than a path-traversal message from the
// far side of the API.
func hidScriptPath(name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("no payload selected")
	}
	if strings.ContainsAny(name, `/\`) || name == "." || name == ".." {
		return "", fmt.Errorf("bad payload name")
	}
	return HIDScriptDir + "/" + name, nil
}

func (c *APIClient) RunHIDScript(name string, background bool) (string, error) {
	path, err := hidScriptPath(name)
	if err != nil {
		return "", err
	}
	req := map[string]interface{}{
		"scriptPath":     path,
		"timeoutSeconds": 0,
	}
	if background {
		var job struct {
			Id int `json:"id"`
		}
		if err := c.call("HIDRunScriptJob", req, &job); err != nil {
			return "", err
		}
		return fmt.Sprintf("started job %d", job.Id), nil
	}
	var res struct {
		ResultJson string `json:"resultJson"`
	}
	if err := c.call("HIDRunScript", req, &res); err != nil {
		return "", err
	}
	if res.ResultJson == "" {
		return "done", nil
	}
	return res.ResultJson, nil
}

// RunningJobs lists the HIDScript jobs the service has running.
//
// The reply is HIDScriptJobList, which is a bare list of IDS -- nothing else.
// This client decoded it as {"jobs":[{"id","scriptPath"}]}, a shape the
// service has never sent, so the list came back empty every single time:
// the Jobs screen always said "nothing running" and the dashboard always said
// "Jobs 0 running", while payloads were in fact running. Unmarshalling into a
// struct whose tags match nothing is not an error in Go, it is a zero value,
// so nothing anywhere reported a problem.
//
// One call. The dashboard polls this every five seconds and only wants the
// count, so it must stay cheap -- names are fetched separately by the screen
// that displays them.
func (c *APIClient) RunningJobs() ([]Job, error) {
	var out struct {
		Ids []int `json:"ids"`
	}
	if err := c.call("HIDGetRunningScriptJobs", nil, &out); err != nil {
		return nil, err
	}
	jobs := make([]Job, 0, len(out.Ids))
	for _, id := range out.Ids {
		jobs = append(jobs, Job{ID: id})
	}
	return jobs, nil
}

// DescribeJob returns a short label for one running job.
//
// There is no RPC that reports what a job was started FROM: HIDScriptJobList
// carries ids and HIDRunningJobStateResult carries the script's source text,
// not its path. So the label is the first meaningful line of the source, which
// for every payload in this tree is its header comment. Costs one call, which
// is why only the Jobs screen asks and only for the rows it can show.
func (c *APIClient) DescribeJob(id int) string {
	var st struct {
		Source string `json:"source"`
	}
	if err := c.call("HIDGetRunningJobState", map[string]int{"id": id}, &st); err != nil {
		return ""
	}
	return firstMeaningfulLine(st.Source)
}

// firstMeaningfulLine picks the first line of a script worth showing, with its
// comment marker stripped.
func firstMeaningfulLine(src string) string {
	for _, raw := range strings.Split(src, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		line = strings.TrimLeft(line, "/*# \t")
		line = strings.TrimRight(line, "*/ \t")
		if line == "" {
			continue
		}
		return Truncate(line, 16)
	}
	return ""
}

func (c *APIClient) CancelAllJobs() error {
	return c.call("HIDCancelAllScriptJobs", nil, nil)
}

func (c *APIClient) USBFunctions() ([]Toggle, error) {
	var gs map[string]interface{}
	if err := c.call("GetDeployedGadgetSetting", nil, &gs); err != nil {
		return nil, err
	}
	out := make([]Toggle, 0, len(usbToggles))
	for _, t := range usbToggles {
		on, _ := gs[t.Key].(bool)
		out = append(out, Toggle{Key: t.Key, Label: t.Label, On: on})
	}
	return out, nil
}

func (c *APIClient) SetUSBFunctions(on map[string]bool) error {
	// Read-modify-write: the gadget message carries vendor and product IDs,
	// the serial and more besides, and deploying a message built only from
	// the toggles would silently reset all of it.
	var gs map[string]interface{}
	if err := c.call("GetDeployedGadgetSetting", nil, &gs); err != nil {
		return err
	}
	for k, v := range on {
		gs[k] = v
	}
	return c.call("DeployGadgetSetting", gs, nil)
}

func (c *APIClient) SetLEDBlink(count int) error {
	return c.call("SetLEDSettings", map[string]int{"blink_count": count}, nil)
}

func (c *APIClient) Reboot() error   { return c.call("Reboot", nil, nil) }
func (c *APIClient) Shutdown() error { return c.call("Shutdown", nil, nil) }

func (c *APIClient) Status() (Status, error) {
	s := Status{USBHost: "unknown", WiFi: "unknown"}
	var problems []string

	var gs map[string]interface{}
	if err := c.call("GetDeployedGadgetSetting", nil, &gs); err != nil {
		problems = append(problems, "usb")
		s.USBHost = "no gadget"
	} else {
		for _, t := range usbToggles {
			if on, _ := gs[t.Key].(bool); on {
				s.USBFunctions = append(s.USBFunctions, t.Label)
			}
		}
		s.USBHost = "composed"
	}

	var eth struct {
		List []struct {
			Name       string `json:"name"`
			IpAddress4 string `json:"ipAddress4"`
			Mode       string `json:"mode"`
		} `json:"list"`
	}
	if err := c.call("GetAllDeployedEthernetInterfaceSettings", nil, &eth); err != nil {
		problems = append(problems, "net")
	} else {
		for _, i := range eth.List {
			s.Interfaces = append(s.Interfaces, Iface{Name: i.Name, IP: i.IpAddress4, Mode: i.Mode})
		}
	}

	var wifi struct {
		Mode string `json:"mode"`
		Ssid string `json:"ssid"`
	}
	if err := c.call("GetWiFiState", nil, &wifi); err != nil {
		s.WiFi = "unavailable"
	} else {
		s.WiFiOK = true
		s.WiFi = strings.TrimSpace(wifi.Mode + " " + wifi.Ssid)
		if s.WiFi == "" {
			s.WiFi = "idle"
		}
	}

	var tas struct {
		TriggerActions []struct {
			IsActive bool `json:"isActive"`
		} `json:"TriggerActions"`
	}
	if err := c.call("GetDeployedTriggerActionSet", nil, &tas); err == nil {
		s.Reflexes = len(tas.TriggerActions)
		for _, t := range tas.TriggerActions {
			if t.IsActive {
				s.ReflexesArmed++
			}
		}
	}

	if jobs, err := c.RunningJobs(); err == nil {
		s.RunningJobs = len(jobs)
	}

	if len(problems) > 0 {
		s.Err = strings.Join(problems, ",") + " unavailable"
	}
	return s, nil
}
