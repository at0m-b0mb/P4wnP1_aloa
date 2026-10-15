package bluetooth

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"

	"github.com/godbus/dbus"
	"github.com/mame82/mblue-toolz/bt_uuid"
	"github.com/mame82/mblue-toolz/dbusHelper"
	"github.com/mame82/mblue-toolz/toolz"
)

type DefaultAgent struct {
	pincode    string // only used if Secure Simple Pairing is disabled and currentCap = DISPLAY_ONLY
	currentCap toolz.AgentCapability
}

// ------------ START OF AGENT INTERFACE IMPLEMENTATION ------------
func (a *DefaultAgent) RegistrationPath() string {
	return toolz.AgentDefaultRegisterPath
}

func (a *DefaultAgent) Release() *dbus.Error {
	fmt.Println("DefaultAgent release called")
	return nil
}

func (a *DefaultAgent) RequestPinCode(device dbus.ObjectPath) (pincode string, err *dbus.Error) {
	// NEVER print the PIN. It used to be logged in full on every pairing
	// attempt, which was merely pointless while the PIN was the published
	// constant "1337" and is a credential leak now that it is per-device --
	// the journal is readable by more things than the pairing dialog is, and
	// an operator pasting logs into a bug report would paste their PIN.
	//
	// Whether one is configured at all is worth saying, because an empty PIN
	// fails pairing silently and that is otherwise very hard to diagnose.
	if a.pincode == "" {
		fmt.Println("DefaultAgent: pairing requested but NO PIN is configured; refusing")
	} else {
		fmt.Println("DefaultAgent: pairing requested, returning the configured PIN")
	}
	// Called when SSP is off and currentCap != CAP_NO_INPUT_NO_OUTPUT, we could use a pre-generated PIN
	// and the remote device has to enter the same one
	return a.pincode, nil
}

func (a *DefaultAgent) DisplayPinCode(device dbus.ObjectPath, pincode string) *dbus.Error {
	fmt.Printf("DefaultAgent display pincode called, code: '%s'\n", pincode)
	return nil
}

func (a *DefaultAgent) RequestPasskey(device dbus.ObjectPath) (passkey uint32, err *dbus.Error) {
	// Called with SSP on and Cap == AGENT_CAP_KEYBOARD_ONLY. The upstream
	// comment below is right that we cannot know the correct value -- the
	// remote device displays it and a human is supposed to type it here, and
	// this appliance has no keyboard to type it with. So whatever we return is
	// a guess and pairing is meant to fail.
	//
	// It returned the literal 1337 regardless. That is a one-in-a-million
	// guess rather than a secure failure, and it was the third hardcoded 1337
	// in the tree. A random value fails just as surely and does not advertise
	// a constant worth trying.
	//
	// This device registers AGENT_CAP_NO_INPUT_NO_OUTPUT (see Start and
	// NewDefaultAgent), so BlueZ should never call this at all. It is fixed
	// because "unreachable today" is a property of the current capability, not
	// of this function.
	var b [4]byte
	if _, rerr := rand.Read(b[:]); rerr != nil {
		// Refusing beats returning a predictable number.
		fmt.Println("DefaultAgent: passkey requested but no randomness available; rejecting")
		return 0, toolz.ErrRejected
	}
	// Bluetooth passkeys are 6 decimal digits, i.e. 0..999999.
	pk := binary.BigEndian.Uint32(b[:]) % 1000000
	fmt.Println("DefaultAgent: passkey requested; this device cannot know it, returning a random value (pairing will fail)")
	return pk, nil
}

func (a *DefaultAgent) DisplayPasskey(device dbus.ObjectPath, passkey uint32, entered uint16) *dbus.Error {
	fmt.Printf("DefaultAgent display passkey called, passkey: %d\n", passkey)
	return nil
}

func (a *DefaultAgent) RequestConfirmation(device dbus.ObjectPath, passkey uint32) *dbus.Error {
	fmt.Printf("DefaultAgent request confirmation called for passkey: %d\n", passkey)
	// Called when SSP on and
	// currentCap == AGENT_CAP_DISPLAY_ONLY ||
	// currentCap == AGENT_CAP_KEYBOARD_DISPLAY ||
	// currentCap == AGENT_CAP_DISPLAY_YES_NO
	fmt.Println("... rejecting passkey")
	return toolz.ErrRejected
}

func (a *DefaultAgent) RequestAuthorization(device dbus.ObjectPath) *dbus.Error {
	fmt.Println("DefaultAgent request authorization called")
	fmt.Println("... accepting")
	return nil
}

func (a *DefaultAgent) AuthorizeService(device dbus.ObjectPath, uuid string) *dbus.Error {
	devStr, _ := dbusHelper.DBusDevPathToHwAddr(device) // ignore error
	/*
		// alternate way to retrieve the device address (and call functions of the device)
		if d,e := toolz.Device(device); e == nil {
			addr,_ := d.GetAddress()
			fmt.Println(addr)
		}
	*/

	fmt.Printf("DefaultAgent authorize service called for UUID: %s from %s\n", uuid, devStr)
	switch uuid {
	case bt_uuid.BNEP_SVC_UUID:
		fmt.Println("... granting BNEP access")
		return nil
	default:
		fmt.Println("... rejecting")
		return toolz.ErrRejected
	}
}

func (a *DefaultAgent) Cancel() *dbus.Error {
	fmt.Println("DefaultAgent cancel called")
	return nil
}

// ------------ END OF AGENT INTERFACE IMPLEMENTATION ------------

func (a *DefaultAgent) Start(cap toolz.AgentCapability) (err error) {
	a.currentCap = cap
	return toolz.RegisterDefaultAgent(a, cap)
}

func (a *DefaultAgent) Stop() (err error) {
	return toolz.UnregisterAgent(a.RegistrationPath())
}

func (a *DefaultAgent) SetPIN(pin string) {
	a.pincode = pin
}

func (a *DefaultAgent) GetPIN() (pin string) {
	return a.pincode
}

func NewDefaultAgent(pincode string) (res *DefaultAgent) {
	return &DefaultAgent{
		pincode:    pincode,
		currentCap: toolz.AGENT_CAP_NO_INPUT_NO_OUTPUT,
	}
}
