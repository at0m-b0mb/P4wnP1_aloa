//go:build linux
// +build linux

package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/improbable-eng/grpc-web/go/grpcweb"
	"github.com/mame82/P4wnP1_aloa/common"
	"github.com/mame82/P4wnP1_aloa/common_web"
	pb "github.com/mame82/P4wnP1_aloa/proto"
	"github.com/mame82/P4wnP1_aloa/service/auth"
	"github.com/mame82/P4wnP1_aloa/service/bluetooth"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

var (
	rpcErrNoHid = errors.New("HIDScript engine disabled, as current USB configuration has mouse and keyboard disable")
)

const (
	cSTORE_PREFIX_WIFI_SETTINGS               = "ws_"
	cSTORE_PREFIX_USB_SETTINGS                = "usbs_"
	cSTORE_PREFIX_ETHERNET_INTERFACE_SETTINGS = "eis_"
	cSTORE_PREFIX_TRIGGER_ACTION_SET          = "tas_"
	cSTORE_PREFIX_BLUETOOTH_SETTINGS          = "bt_"
	cSTORE_PREFIX_MASTER_TEMPLATE             = "master_"
	cSTORE_STARTUP_MASTER_TEMPLATE            = "startup_master"
)

func NewRpcServerService(root *Service) *server {
	return &server{
		rootSvc: root,
	}
}

type server struct {
	rootSvc *Service

	listenAddrGrpc string
	listenAddrWeb  string
}

func (s *server) ListUmsImageFlashdrive(ctx context.Context, e *pb.Empty) (sa *pb.StringMessageArray, err error) {
	sa = &pb.StringMessageArray{}
	scripts, err := ListFilesOfFolder(common.PATH_IMAGE_FLASHDRIVE, ".img", ".bin")
	if err != nil {
		return sa, err
	}
	sa.MsgArray = scripts
	return
}

func (s *server) ListUmsImageCdrom(ctx context.Context, e *pb.Empty) (sa *pb.StringMessageArray, err error) {
	sa = &pb.StringMessageArray{}
	scripts, err := ListFilesOfFolder(common.PATH_IMAGE_CDROM, ".iso")
	if err != nil {
		return sa, err
	}
	sa.MsgArray = scripts
	return
}

func (s *server) GetStartupMasterTemplate(ctx context.Context, e *pb.Empty) (msg *pb.StringMessage, err error) {
	msg = &pb.StringMessage{}
	err = s.rootSvc.SubSysDataStore.Get(cSTORE_STARTUP_MASTER_TEMPLATE, msg)
	fmt.Printf("Retrieved startup MasterTemplate name '%s'\n", msg.Msg)
	return
}

func (s *server) SetStartupMasterTemplate(ctx context.Context, msg *pb.StringMessage) (e *pb.Empty, err error) {
	fmt.Printf("Setting startup MasterTemplate name to '%s'\n", msg.Msg)
	e = &pb.Empty{}
	err = s.rootSvc.SubSysDataStore.Put(cSTORE_STARTUP_MASTER_TEMPLATE, msg, true)
	return
}

func (s *server) Shutdown(context.Context, *pb.Empty) (e *pb.Empty, err error) {
	e = &pb.Empty{}
	s.rootSvc.Shutdown()
	return
}

func (s *server) Reboot(context.Context, *pb.Empty) (e *pb.Empty, err error) {
	e = &pb.Empty{}
	s.rootSvc.Reboot()
	return
}

func (s *server) DeployTriggerActionSetUpdate(ctx context.Context, updateTas *pb.TriggerActionSet) (resultingTas *pb.TriggerActionSet, err error) {
	defer s.rootSvc.SubSysEvent.Emit(ConstructEventNotifyStateChange(common_web.STATE_CHANGE_EVT_TYPE_TRIGGER_ACTIONS))
	for _, updateTa := range updateTas.TriggerActions {
		// try to find the trigger action to update by ID
		fmt.Printf("Updating TriggerAction %d ...\n", updateTa.Id)
		if s.rootSvc.SubSysTriggerActions.UpdateTriggerAction(updateTa, false) != nil {
			fmt.Printf("Updating TriggerAction %d failed: %v\n", updateTa.Id, err)
			// coudln't find the given action, return with error
			return &s.rootSvc.SubSysTriggerActions.registeredTriggerActions, errors.New(fmt.Sprintf("Couldn't find trigger action with id %d", updateTa.Id))
		}
		fmt.Printf("Updating TriggerAction %d succeeded\n", updateTa.Id)
	}

	resultingTas = &s.rootSvc.SubSysTriggerActions.registeredTriggerActions
	return
}

func (s *server) GetAvailableGpios(context.Context, *pb.Empty) (res *pb.StringMessageArray, err error) {
	res = &pb.StringMessageArray{}
	res.MsgArray, err = s.rootSvc.SubSysGpio.GetAvailableGpioNames()
	return
}

func (s *server) DeployMasterTemplate(ctx context.Context, mt *pb.MasterTemplate) (e *pb.Empty, err error) {
	e = &pb.Empty{}

	fmt.Println("Deploying master template ...")

	//ignore templates with name of length 0
	if len(mt.TemplateNameTriggerActions) > 0 {
		fmt.Printf("... deploying TriggerActions '%s' ...\n", mt.TemplateNameTriggerActions)
		_, err = s.DeployStoredTriggerActionSetReplace(ctx, &pb.StringMessage{Msg: mt.TemplateNameTriggerActions})
		if err != nil {
			fmt.Printf("... error deploying TriggerActions '%s'\n", mt.TemplateNameTriggerActions)
			return
		}
		fmt.Printf("... succeeded deploying TriggerActions '%s'\n", mt.TemplateNameTriggerActions)
	}

	for _, nnw := range mt.TemplateNamesNetwork {
		fmt.Printf("... deploying Network Interface Settings '%s' ...\n", nnw)
		_, err = s.DeployStoredEthernetInterfaceSettings(ctx, &pb.StringMessage{Msg: nnw})
		if err != nil {
			fmt.Printf("... error deploying Network Interface Settings '%s'\n", nnw)
			return
		}
		fmt.Printf("... succeeded deploying Network Interface Settings '%s'\n", nnw)
	}

	if len(mt.TemplateNameBluetooth) > 0 {
		fmt.Printf("... deploying Bluetooth settings '%s' ...\n", mt.TemplateNameBluetooth)
		_, btErr := s.DeployStoredBluetoothSettings(ctx, &pb.StringMessage{Msg: mt.TemplateNameBluetooth})
		if btErr != nil {
			if btErr == bluetooth.ErrBtSvcNotAvailable {
				fmt.Printf("... ignoring Bluetooth error '%s'\n", mt.TemplateNameBluetooth)

			} else {
				fmt.Printf("... error deploying Bluetooth settings '%s'\n", mt.TemplateNameBluetooth)
				return
			}
			fmt.Printf("... error deploying Bluetooth settings '%s'\n", mt.TemplateNameBluetooth)
		}
		fmt.Printf("... succeeded deploying Bluetooth settings '%s'\n", mt.TemplateNameBluetooth)
	}
	if len(mt.TemplateNameUsb) > 0 {
		fmt.Printf("... deploying USB settings '%s' ...\n", mt.TemplateNameUsb)
		_, err = s.DeployStoredUSBSettings(ctx, &pb.StringMessage{Msg: mt.TemplateNameUsb})
		if err != nil {
			fmt.Printf("... error deploying USB settings '%s'\n", mt.TemplateNameUsb)
			return
		}
		fmt.Printf("... succeeded deploying USB settings '%s'\n", mt.TemplateNameUsb)
	}
	if len(mt.TemplateNameWifi) > 0 {
		fmt.Printf("... deploying WiFi settings '%s' ...\n", mt.TemplateNameWifi)
		_, err = s.DeployStoredWifiSettings(ctx, &pb.StringMessage{Msg: mt.TemplateNameWifi})
		if err != nil {
			fmt.Printf("... error deploying WiFi settings '%s'\n", mt.TemplateNameWifi)
			return
		}
		fmt.Printf("... succeeded deploying WiFi settings '%s'\n", mt.TemplateNameWifi)
	}

	fmt.Println("... master template deployed successfully")
	return
}

func (s *server) StoreMasterTemplate(ctx context.Context, r *pb.RequestMasterTemplateStorage) (e *pb.Empty, err error) {
	e = &pb.Empty{}

	defer s.rootSvc.SubSysEvent.Emit(ConstructEventNotifyStateChange(common_web.STATE_CHANGE_EVT_TYPE_STORED_GLOBAL_SETTINGS_LIST))
	err = s.rootSvc.SubSysDataStore.Put(cSTORE_PREFIX_MASTER_TEMPLATE+r.TemplateName, r.Template, true)
	return

}

func (s *server) GetStoredMasterTemplate(ctx context.Context, templateName *pb.StringMessage) (result *pb.MasterTemplate, err error) {
	result = &pb.MasterTemplate{}
	err = s.rootSvc.SubSysDataStore.Get(cSTORE_PREFIX_MASTER_TEMPLATE+templateName.Msg, result)
	return
}

func (s *server) DeployStoredMasterTemplate(ctx context.Context, templateName *pb.StringMessage) (re *pb.MasterTemplate, err error) {
	re, err = s.GetStoredMasterTemplate(ctx, templateName)
	if err != nil {
		return
	}
	_, err = s.DeployMasterTemplate(ctx, re)
	return
}

func (s *server) DeleteStoredMasterTemplate(ctx context.Context, templateName *pb.StringMessage) (e *pb.Empty, err error) {
	defer s.rootSvc.SubSysEvent.Emit(ConstructEventNotifyStateChange(common_web.STATE_CHANGE_EVT_TYPE_STORED_GLOBAL_SETTINGS_LIST))
	e = &pb.Empty{}
	err = s.rootSvc.SubSysDataStore.Delete(cSTORE_PREFIX_MASTER_TEMPLATE + templateName.Msg)
	return
}

func (s *server) ListStoredMasterTemplate(ctx context.Context, e *pb.Empty) (sa *pb.StringMessageArray, err error) {
	sa = &pb.StringMessageArray{}
	res, err := s.rootSvc.SubSysDataStore.KeysPrefix(cSTORE_PREFIX_MASTER_TEMPLATE, true)
	if err != nil {
		return sa, err
	}
	sa.MsgArray = res
	return
}

func (s *server) WaitTriggerGroupReceive(rpcCtx context.Context, triggerGR *pb.TriggerGroupReceive) (e *pb.Empty, err error) {
	e = &pb.Empty{}
	triggerVal := triggerGR.Value
	triggerGroupName := triggerGR.GroupName

	//register a proper event listener
	evtRcv := s.rootSvc.SubSysEvent.RegisterReceiver(common_web.EVT_TRIGGER)
	defer evtRcv.Cancel()

Outer:
	for {
		select {
		case evt := <-evtRcv.EventQueue:
			// avoid consuming empty messages, because channel is closed
			if evt == nil {
				break Outer // abort loop on "nil" event, as this indicates the EventQueue channel has been closed
			}
			// check if received trigger event applies to TriggerGroupReceive
			if ttEvt := common_web.EvtTriggerType(evt.Values[0].GetTint64()); ttEvt == common_web.TRIGGER_EVT_TYPE_GROUP_RECEIVE {
				evGroupName, evValue, err := DeconstructEventTriggerGroupReceive(evt)
				if err != nil {
					continue // error parsing as groupReceiveEvent --> ignore
				}
				//check if group matches
				if evGroupName != triggerGroupName {
					continue // don't handle on group mismatch, but return without error
				}
				// check if received value matches
				if evValue != triggerVal {
					continue // don't handle on value mismatch, but return without error
				}

				//consume remaining events (shouldn't be necessary)
				//for len(evtRcv.EventQueue) > 0 { <- evtRcv.EventQueue }

				// if here, we have a hit and exit the loop without error
				break Outer
			}
		case <-evtRcv.Ctx.Done():
			// evvent Receiver cancelled or unregistered
			err = errors.New("EventListener for WaitTriggerGroupReceive aborted")
			break Outer
		case <-rpcCtx.Done():
			// evvent Receiver cancelled or unregistered
			err = errors.New("RPC call to WaitTriggerGroupReceive aborted")
			break Outer
		}
	}

	/*
		if err != nil {
			fmt.Println("Aborted")
		}
	*/
	return
}

func (s *server) FireActionGroupSend(ctx context.Context, gs *pb.ActionGroupSend) (e *pb.Empty, err error) {
	e = &pb.Empty{}
	s.rootSvc.SubSysEvent.Emit(ConstructEventTriggerGroupReceive(gs.GroupName, gs.Value))
	return
}

func (s *server) DeployBluetoothSettings(ctx context.Context, settings *pb.BluetoothSettings) (resultSettings *pb.BluetoothSettings, err error) {
	defer s.rootSvc.SubSysEvent.Emit(ConstructEventNotifyStateChange(common_web.STATE_CHANGE_EVT_TYPE_BLUETOOTH))
	//Overwrite default settings, in case the bluetooth sub system comes up later
	s.rootSvc.SubSysBluetooth.ReplaceDefaultSettings(settings)

	as := settings.As
	ci := settings.Ci
	resultSettings = &pb.BluetoothSettings{}
	resultSettings.Ci, err = s.DeployBluetoothControllerInformation(ctx, ci)
	if err != nil {
		resultSettings.As, _ = s.GetBluetoothAgentSettings(ctx, &pb.Empty{})
		return
	}
	resultSettings.As, err = s.DeployBluetoothAgentSettings(ctx, as)
	return
}

func (s *server) StoreBluetoothSettings(ctx context.Context, req *pb.BluetoothRequestSettingsStorage) (e *pb.Empty, err error) {
	defer s.rootSvc.SubSysEvent.Emit(ConstructEventNotifyStateChange(common_web.STATE_CHANGE_EVT_TYPE_STORED_BLUETOOTH_SETTINGS_LIST))
	e = &pb.Empty{}
	err = s.rootSvc.SubSysDataStore.Put(cSTORE_PREFIX_BLUETOOTH_SETTINGS+req.TemplateName, req.Settings, true)
	return
}

func (s *server) GetStoredBluetoothSettings(ctx context.Context, templateName *pb.StringMessage) (result *pb.BluetoothSettings, err error) {
	result = &pb.BluetoothSettings{}
	err = s.rootSvc.SubSysDataStore.Get(cSTORE_PREFIX_BLUETOOTH_SETTINGS+templateName.Msg, result)
	return
}

func (s *server) DeployStoredBluetoothSettings(ctx context.Context, templateName *pb.StringMessage) (e *pb.BluetoothSettings, err error) {
	defer s.rootSvc.SubSysEvent.Emit(ConstructEventNotifyStateChange(common_web.STATE_CHANGE_EVT_TYPE_BLUETOOTH))
	bts, err := s.GetStoredBluetoothSettings(ctx, templateName)
	if err != nil {
		return bts, err
	}
	return s.DeployBluetoothSettings(ctx, bts)
}

func (s *server) DeleteStoredBluetoothSettings(ctx context.Context, templateName *pb.StringMessage) (e *pb.Empty, err error) {
	defer s.rootSvc.SubSysEvent.Emit(ConstructEventNotifyStateChange(common_web.STATE_CHANGE_EVT_TYPE_STORED_BLUETOOTH_SETTINGS_LIST))
	e = &pb.Empty{}
	err = s.rootSvc.SubSysDataStore.Delete(cSTORE_PREFIX_BLUETOOTH_SETTINGS + templateName.Msg)
	return
}

func (s *server) StoreDeployedBluetoothSettings(ctx context.Context, templateName *pb.StringMessage) (e *pb.Empty, err error) {
	defer s.rootSvc.SubSysEvent.Emit(ConstructEventNotifyStateChange(common_web.STATE_CHANGE_EVT_TYPE_STORED_BLUETOOTH_SETTINGS_LIST))
	e = &pb.Empty{}
	currentSettings := &pb.BluetoothSettings{}
	currentSettings.Ci, err = s.GetBluetoothControllerInformation(ctx, e)
	if err != nil {
		return e, err
	}
	currentSettings.As, err = s.GetBluetoothAgentSettings(ctx, e)
	if err != nil {
		return e, err
	}

	return s.StoreBluetoothSettings(ctx, &pb.BluetoothRequestSettingsStorage{
		Settings:     currentSettings,
		TemplateName: templateName.Msg,
	})
}

func (s *server) ListStoredBluetoothSettings(ctx context.Context, e *pb.Empty) (sa *pb.StringMessageArray, err error) {
	sa = &pb.StringMessageArray{}
	res, err := s.rootSvc.SubSysDataStore.KeysPrefix(cSTORE_PREFIX_BLUETOOTH_SETTINGS, true)
	if err != nil {
		return sa, err
	}
	sa.MsgArray = res
	return
}

func (s *server) DeleteStoredUSBSettings(ctx context.Context, name *pb.StringMessage) (e *pb.Empty, err error) {
	defer s.rootSvc.SubSysEvent.Emit(ConstructEventNotifyStateChange(common_web.STATE_CHANGE_EVT_TYPE_STORED_USB_SETTINGS_LIST))
	e = &pb.Empty{}
	err = s.rootSvc.SubSysDataStore.Delete(cSTORE_PREFIX_USB_SETTINGS + name.Msg)
	return
}

func (s *server) DeleteStoredWifiSettings(ctx context.Context, name *pb.StringMessage) (e *pb.Empty, err error) {
	defer s.rootSvc.SubSysEvent.Emit(ConstructEventNotifyStateChange(common_web.STATE_CHANGE_EVT_TYPE_STORED_WIFI_SETTINGS_LIST))
	e = &pb.Empty{}
	err = s.rootSvc.SubSysDataStore.Delete(cSTORE_PREFIX_WIFI_SETTINGS + name.Msg)
	return
}

func (s *server) DeleteStoredEthernetInterfaceSettings(ctx context.Context, name *pb.StringMessage) (e *pb.Empty, err error) {
	defer s.rootSvc.SubSysEvent.Emit(ConstructEventNotifyStateChange(common_web.STATE_CHANGE_EVT_TYPE_STORED_ETHERNET_INTERFACE_SETTINGS_LIST))
	e = &pb.Empty{}
	err = s.rootSvc.SubSysDataStore.Delete(cSTORE_PREFIX_ETHERNET_INTERFACE_SETTINGS + name.Msg)
	return
}

func (s *server) DeleteStoredTriggerActionSet(ctx context.Context, name *pb.StringMessage) (e *pb.Empty, err error) {
	defer s.rootSvc.SubSysEvent.Emit(ConstructEventNotifyStateChange(common_web.STATE_CHANGE_EVT_TYPE_STORED_TRIGGER_ACTION_SETS_LIST))
	e = &pb.Empty{}
	err = s.rootSvc.SubSysDataStore.Delete(cSTORE_PREFIX_TRIGGER_ACTION_SET + name.Msg)
	return
}

func (s *server) DBBackup(ctx context.Context, filename *pb.StringMessage) (e *pb.Empty, err error) {
	e = &pb.Empty{}
	fname := filename.Msg
	ext := filepath.Ext(fname)
	if lext := strings.ToLower(ext); lext != ".db" {
		fname = fname + ".db"
	}

	err = s.rootSvc.SubSysDataStore.Backup(common.PATH_DATA_STORE_BACKUP + "/" + fname)
	return
}

func (s *server) DBRestore(ctx context.Context, filename *pb.StringMessage) (e *pb.Empty, err error) {
	fmt.Println("DB restore: ", filename.Msg)
	e = &pb.Empty{}
	fname := filename.Msg
	ext := filepath.Ext(fname)
	if lext := strings.ToLower(ext); lext != ".db" {
		fname = fname + ".db"
	}
	err = s.rootSvc.SubSysDataStore.Restore(common.PATH_DATA_STORE_BACKUP+"/"+fname, true)
	return
}

func (s *server) ListStoredDBBackups(ctx context.Context, e *pb.Empty) (ma *pb.StringMessageArray, err error) {
	ma = &pb.StringMessageArray{}
	scripts, err := ListFilesOfFolder(common.PATH_DATA_STORE_BACKUP, ".db")
	if err != nil {
		return ma, err
	}
	ma.MsgArray = scripts
	return
}

func (s *server) GetBluetoothAgentSettings(ctx context.Context, e *pb.Empty) (as *pb.BluetoothAgentSettings, err error) {
	return s.rootSvc.SubSysBluetooth.GetBluetoothAgentSettings()
}

func (s *server) DeployBluetoothAgentSettings(ctx context.Context, src *pb.BluetoothAgentSettings) (res *pb.BluetoothAgentSettings, err error) {
	defer s.rootSvc.SubSysEvent.Emit(ConstructEventNotifyStateChange(common_web.STATE_CHANGE_EVT_TYPE_BLUETOOTH))
	return s.rootSvc.SubSysBluetooth.DeployBluetoothAgentSettings(src)
}

// Unused, Server services are deployed via BluetoothControllerInformation
func (s *server) SetBluetoothNetworkService(ctx context.Context, btNwSvc *pb.BluetoothNetworkService) (e *pb.Empty, err error) {
	defer s.rootSvc.SubSysEvent.Emit(ConstructEventNotifyStateChange(common_web.STATE_CHANGE_EVT_TYPE_BLUETOOTH))
	e = &pb.Empty{}
	err = s.rootSvc.SubSysBluetooth.DeployBluetoothNetworkService(btNwSvc)
	return
}

func (s *server) DeployBluetoothControllerInformation(ctx context.Context, newBtCiRpc *pb.BluetoothControllerInformation) (updateBtCiRpc *pb.BluetoothControllerInformation, err error) {
	defer s.rootSvc.SubSysEvent.Emit(ConstructEventNotifyStateChange(common_web.STATE_CHANGE_EVT_TYPE_BLUETOOTH))
	return s.rootSvc.SubSysBluetooth.DeployBluetoothControllerInformation(newBtCiRpc)
}

func (s *server) GetBluetoothControllerInformation(ctx context.Context, e *pb.Empty) (res *pb.BluetoothControllerInformation, err error) {
	res = &pb.BluetoothControllerInformation{}
	return s.rootSvc.SubSysBluetooth.GetControllerInformation()
}

func (s *server) StoreUSBSettings(ctx context.Context, r *pb.USBRequestSettingsStorage) (e *pb.Empty, err error) {
	defer s.rootSvc.SubSysEvent.Emit(ConstructEventNotifyStateChange(common_web.STATE_CHANGE_EVT_TYPE_STORED_USB_SETTINGS_LIST))
	e = &pb.Empty{}
	err = s.rootSvc.SubSysDataStore.Put(cSTORE_PREFIX_USB_SETTINGS+r.TemplateName, r.Settings, true)
	return
}

func (s *server) GetStoredUSBSettings(ctx context.Context, m *pb.StringMessage) (gs *pb.GadgetSettings, err error) {
	gs = &pb.GadgetSettings{}
	err = s.rootSvc.SubSysDataStore.Get(cSTORE_PREFIX_USB_SETTINGS+m.Msg, gs)
	return
}

func (s *server) DeployStoredUSBSettings(ctx context.Context, m *pb.StringMessage) (st *pb.GadgetSettings, err error) {
	defer s.rootSvc.SubSysEvent.Emit(ConstructEventNotifyStateChange(common_web.STATE_CHANGE_EVT_TYPE_USB))
	ws, err := s.GetStoredUSBSettings(ctx, m)
	if err != nil {
		return &pb.GadgetSettings{}, err
	}
	st, err = s.DeployGadgetSetting(ctx, ws)
	return
}

// hardwareUnavailable classifies an error that means "this hardware is not
// present or not configured" as codes.Unavailable rather than letting it
// become codes.Unknown and, over HTTP, a 500.
//
// A 500 asserts that the SERVICE is broken. On a board with no USB device
// controller bound, no WiFi adapter or no Bluetooth controller -- which is a
// normal, supported state for this device -- the service is working perfectly
// and the hardware simply is not there. Reporting that as an internal fault
// misleads an operator reading logs and misleads anything that retries on 5xx.
//
// Everything that is NOT one of these sentinels is passed through untouched,
// so a genuine bug still surfaces as one. That distinction is the whole point;
// a blanket conversion would be worse than the original problem.
func hardwareUnavailable(err error) error {
	if err == nil {
		return nil
	}
	for _, sentinel := range []error{
		ErrUsbNotUsable,
		ErrHidNotUsable,
		bluetooth.ErrBtSvcNotAvailable,
	} {
		if errors.Is(err, sentinel) {
			return status.Error(codes.Unavailable, err.Error())
		}
	}
	return err
}

// usbStatus is the USB-specific spelling, kept for call-site readability.
func usbStatus(err error) error { return hardwareUnavailable(err) }

func (s *server) StoreDeployedUSBSettings(ctx context.Context, m *pb.StringMessage) (e *pb.Empty, err error) {
	gstate, err := s.rootSvc.SubSysUSB.ParseGadgetState(USB_GADGET_NAME)
	if err != nil {
		return &pb.Empty{}, usbStatus(err)
	}

	return s.StoreUSBSettings(ctx, &pb.USBRequestSettingsStorage{
		Settings:     gstate,
		TemplateName: m.Msg,
	})
}

func (s *server) ListStoredUSBSettings(ctx context.Context, e *pb.Empty) (sa *pb.StringMessageArray, err error) {
	sa = &pb.StringMessageArray{}
	res, err := s.rootSvc.SubSysDataStore.KeysPrefix(cSTORE_PREFIX_USB_SETTINGS, true)
	if err != nil {
		return sa, err
	}
	sa.MsgArray = res
	return
}

func (s *server) ListStoredHIDScripts(context.Context, *pb.Empty) (sa *pb.StringMessageArray, err error) {
	sa = &pb.StringMessageArray{}
	scripts, err := ListFilesOfFolder(common.PATH_HID_SCRIPTS, ".js", ".javascript")
	if err != nil {
		return sa, err
	}
	sa.MsgArray = scripts
	return
}

func (s *server) ListStoredBashScripts(context.Context, *pb.Empty) (sa *pb.StringMessageArray, err error) {
	sa = &pb.StringMessageArray{}
	scripts, err := ListFilesOfFolder(common.PATH_BASH_SCRIPTS, ".sh", ".bash")
	if err != nil {
		return sa, err
	}
	sa.MsgArray = scripts
	return
}

func (s *server) DeployStoredTriggerActionSetReplace(ctx context.Context, msg *pb.StringMessage) (tas *pb.TriggerActionSet, err error) {
	defer s.rootSvc.SubSysEvent.Emit(ConstructEventNotifyStateChange(common_web.STATE_CHANGE_EVT_TYPE_TRIGGER_ACTIONS))
	// load set from store
	tas = &pb.TriggerActionSet{}
	err = s.rootSvc.SubSysDataStore.Get(cSTORE_PREFIX_TRIGGER_ACTION_SET+msg.Msg, tas)
	if err != nil {
		return
	}

	return s.DeployTriggerActionSetReplace(ctx, tas)
}

func (s *server) DeployStoredTriggerActionSetAdd(ctx context.Context, msg *pb.StringMessage) (tas *pb.TriggerActionSet, err error) {
	defer s.rootSvc.SubSysEvent.Emit(ConstructEventNotifyStateChange(common_web.STATE_CHANGE_EVT_TYPE_TRIGGER_ACTIONS))
	// load set from store
	tas = &pb.TriggerActionSet{}
	err = s.rootSvc.SubSysDataStore.Get(cSTORE_PREFIX_TRIGGER_ACTION_SET+msg.Msg, tas)
	if err != nil {
		return
	}

	return s.DeployTriggerActionSetAdd(ctx, tas)
}

func (s *server) StoreTriggerActionSet(ctx context.Context, set *pb.TriggerActionSet) (e *pb.Empty, err error) {
	defer s.rootSvc.SubSysEvent.Emit(ConstructEventNotifyStateChange(common_web.STATE_CHANGE_EVT_TYPE_STORED_TRIGGER_ACTION_SETS_LIST))
	e = &pb.Empty{}
	err = s.rootSvc.SubSysDataStore.Put(cSTORE_PREFIX_TRIGGER_ACTION_SET+set.Name, set, true)
	return
}

func (s *server) ListStoredTriggerActionSets(ctx context.Context, e *pb.Empty) (tas *pb.StringMessageArray, err error) {
	tas = &pb.StringMessageArray{}
	res, err := s.rootSvc.SubSysDataStore.KeysPrefix(cSTORE_PREFIX_TRIGGER_ACTION_SET, true)
	if err != nil {
		return tas, err
	}
	tas.MsgArray = res
	return
}

func (s *server) GetDeployedTriggerActionSet(context.Context, *pb.Empty) (*pb.TriggerActionSet, error) {
	return s.rootSvc.SubSysTriggerActions.GetCurrentTriggerActionSet(), nil
}

func (s *server) DeployTriggerActionSetReplace(ctx context.Context, tas *pb.TriggerActionSet) (resTas *pb.TriggerActionSet, err error) {
	defer s.rootSvc.SubSysEvent.Emit(ConstructEventNotifyStateChange(common_web.STATE_CHANGE_EVT_TYPE_TRIGGER_ACTIONS))
	// Clear old set, but keep immutables
	s.rootSvc.SubSysTriggerActions.ClearTriggerActions(true)
	// Add the new set
	_, err = s.DeployTriggerActionSetAdd(ctx, tas)
	if err != nil {
		return s.rootSvc.SubSysTriggerActions.GetCurrentTriggerActionSet(), err
	}
	return s.GetDeployedTriggerActionSet(ctx, &pb.Empty{})
}

func (s *server) DeployTriggerActionSetAdd(ctx context.Context, tas *pb.TriggerActionSet) (resTas *pb.TriggerActionSet, err error) {
	defer s.rootSvc.SubSysEvent.Emit(ConstructEventNotifyStateChange(common_web.STATE_CHANGE_EVT_TYPE_TRIGGER_ACTIONS))
	addedTA := make([]*pb.TriggerAction, 0)
	for _, ta := range tas.TriggerActions {
		// we don't allow adding immutable settings via RPC call
		if !ta.Immutable {
			added, err := s.rootSvc.SubSysTriggerActions.AddTriggerAction(ta)
			if err != nil {
				return s.rootSvc.SubSysTriggerActions.GetCurrentTriggerActionSet(), err
			}
			addedTA = append(addedTA, added)
		}
	}

	resTas = &pb.TriggerActionSet{TriggerActions: addedTA, Name: "Added TriggerActions"}
	return
}

func (s *server) DeployTriggerActionSetRemove(ctx context.Context, removeTas *pb.TriggerActionSet) (removedTas *pb.TriggerActionSet, err error) {
	defer s.rootSvc.SubSysEvent.Emit(ConstructEventNotifyStateChange(common_web.STATE_CHANGE_EVT_TYPE_TRIGGER_ACTIONS))
	removedOnes := make([]*pb.TriggerAction, 0)
	for _, removeTa := range removeTas.TriggerActions {
		removed, err := s.rootSvc.SubSysTriggerActions.RemoveTriggerAction(removeTa)
		if err != nil {
			return s.rootSvc.SubSysTriggerActions.GetCurrentTriggerActionSet(), err
		}
		removedOnes = append(removedOnes, removed)
	}

	removedTas = &pb.TriggerActionSet{TriggerActions: removedOnes, Name: "removed TriggerActions"}
	return
}

func (s *server) Start() error {
	return nil
}

func (s *server) Stop() error {
	return nil
}

func (s *server) StoreDeployedWifiSettings(ctx context.Context, m *pb.StringMessage) (e *pb.Empty, err error) {
	//defer s.rootSvc.SubSysEvent.Emit(ConstructEventNotifyStateChange(common_web.STATE_CHANGE_EVT_TYPE_STORED_WIFI_SETTINGS_LIST))
	cur := wifiCurrentSettings(s)
	if cur == nil {
		// There is nothing deployed to store. Without this the nil travelled
		// into the datastore and surfaced as "proto: Marshal called with nil",
		// which tells an operator nothing. Says the real reason instead.
		return &pb.Empty{}, status.Error(codes.Unavailable,
			"there are no deployed WiFi settings to store; "+errWifiUnavailable)
	}
	return s.StoreWifiSettings(ctx, &pb.WifiRequestSettingsStorage{
		Settings:     cur,
		TemplateName: m.Msg,
	})
}

func (s *server) DeployStoredWifiSettings(ctx context.Context, m *pb.StringMessage) (st *pb.WiFiState, err error) {
	defer s.rootSvc.SubSysEvent.Emit(ConstructEventNotifyStateChange(common_web.STATE_CHANGE_EVT_TYPE_WIFI))
	ws, err := s.GetStoredWifiSettings(ctx, m)
	if err != nil {
		return &pb.WiFiState{}, err
	}
	return s.DeployWiFiSettings(ctx, ws)
}

func (s *server) StoreWifiSettings(ctx context.Context, r *pb.WifiRequestSettingsStorage) (e *pb.Empty, err error) {
	defer s.rootSvc.SubSysEvent.Emit(ConstructEventNotifyStateChange(common_web.STATE_CHANGE_EVT_TYPE_STORED_WIFI_SETTINGS_LIST))
	e = &pb.Empty{}
	err = s.rootSvc.SubSysDataStore.Put(cSTORE_PREFIX_WIFI_SETTINGS+r.TemplateName, r.Settings, true)
	return
}

func (s *server) GetStoredWifiSettings(ctx context.Context, m *pb.StringMessage) (ws *pb.WiFiSettings, err error) {
	ws = &pb.WiFiSettings{}
	err = s.rootSvc.SubSysDataStore.Get(cSTORE_PREFIX_WIFI_SETTINGS+m.Msg, ws)
	return
}

func (s *server) ListStoredWifiSettings(ctx context.Context, e *pb.Empty) (sa *pb.StringMessageArray, err error) {
	sa = &pb.StringMessageArray{}
	res, err := s.rootSvc.SubSysDataStore.KeysPrefix(cSTORE_PREFIX_WIFI_SETTINGS, true)
	if err != nil {
		return sa, err
	}
	sa.MsgArray = res
	return
}

func (s *server) DeployWiFiSettings(ctx context.Context, wset *pb.WiFiSettings) (wstate *pb.WiFiState, err error) {
	defer s.rootSvc.SubSysEvent.Emit(ConstructEventNotifyStateChange(common_web.STATE_CHANGE_EVT_TYPE_WIFI))
	if s.rootSvc.SubSysWifi == nil {
		return nil, status.Error(codes.Unavailable, errWifiUnavailable)
	}
	return s.rootSvc.SubSysWifi.DeploySettings(wset)
}

func (s *server) GetWiFiState(ctx context.Context, empty *pb.Empty) (wstate *pb.WiFiState, err error) {
	// Update state before transmitting back
	if s.rootSvc.SubSysWifi == nil {
		return nil, status.Error(codes.Unavailable, errWifiUnavailable)
	}
	s.rootSvc.SubSysWifi.UpdateStateFromIw()

	st := s.rootSvc.SubSysWifi.State
	return st, nil
}

// ListenWiFiStateChanges was never implemented and its body was
// `panic("implement me")`. grpc-go does not recover panics raised inside a
// handler, so any authenticated caller -- and, once the JSON API landed, any
// HTTP client that could reach /api/v1/rpc/ListenWiFiStateChanges -- could
// take the entire service down with a single request. An honest Unimplemented
// is the correct answer until it is written.
func (s *server) ListenWiFiStateChanges(ctx context.Context, empty *pb.Empty) (wstate *pb.WiFiState, err error) {
	return nil, status.Error(codes.Unimplemented, "ListenWiFiStateChanges is not implemented; poll GetWiFiState or subscribe to the event stream instead")
}

func (s *server) EchoRequest(ctx context.Context, req *pb.StringMessage) (resp *pb.StringMessage, err error) {
	return &pb.StringMessage{Msg: req.Msg}, nil
}

func (s *server) EventListen(eReq *pb.EventRequest, eStream pb.P4WNP1_EventListenServer) (err error) {
	//ToDo: check dependency from state (EvMgr initialized)
	// The browser event stream only displays events, so it is the one receiver
	// that may lose them rather than stall the dispatcher for everything else.
	rcv := s.rootSvc.SubSysEvent.RegisterReceiverDroppable(eReq.ListenType)

	for {
		select {
		case ev := <-rcv.EventQueue:
			//fmt.Printf("Event dequed to send: %+v\n", ev)

			//send Event to stream
			err = eStream.Send(ev)
			if err != nil {
				rcv.Cancel()
				log.Println(err)
				return err
			}

		case <-eStream.Context().Done():
			fmt.Println("Receiver aborted ...")
			rcv.Cancel()
			return errors.New("Event listening request aborted")
		case <-rcv.Ctx.Done():
			return errors.New("Service stopped event manager")
		}
	}
}

// resolveAccessibleFolder maps a (folder, filename) pair from a gRPC file IO
// request to a path-traversal-safe absolute path and the permission bits to
// use. Returns an error if the filename escapes the chosen folder.
func resolveAccessibleFolder(folder pb.AccessibleFolder, filename string) (string, os.FileMode, error) {
	var base string
	var perm os.FileMode
	switch folder {
	case pb.AccessibleFolder_TMP:
		// 0600, not os.ModePerm (0777). /tmp is world-writable and shared, and
		// this is the folder the web console writes a HIDScript into before
		// asking the service to RUN it as root -- so 0777 gave any local
		// account a window to rewrite a pending script between the write and
		// the execution. The contents are sensitive too: a script frequently
		// contains whatever it is about to type into the host.
		base, perm = "/tmp", 0600
	case pb.AccessibleFolder_BASH_SCRIPTS:
		base, perm = common.PATH_BASH_SCRIPTS, 0700
	case pb.AccessibleFolder_HID_SCRIPTS:
		base, perm = common.PATH_HID_SCRIPTS, 0600
	default:
		return "", 0, errors.New("unknown folder")
	}
	safe, err := safeJoinUnderBase(base, filename)
	if err != nil {
		return "", 0, err
	}
	return safe, perm, nil
}

func (s *server) FSWriteFile(ctx context.Context, req *pb.WriteFileRequest) (empty *pb.Empty, err error) {
	filePath, perm, err := resolveAccessibleFolder(req.Folder, req.Filename)
	if err != nil {
		return &pb.Empty{}, err
	}
	return &pb.Empty{}, common.WriteFile(filePath, req.MustNotExist, req.Append, req.Data, perm)
}

func (s *server) FSReadFile(ctx context.Context, req *pb.ReadFileRequest) (resp *pb.ReadFileResponse, err error) {
	filePath, perm, err := resolveAccessibleFolder(req.Folder, req.Filename)
	if err != nil {
		return nil, err
	}

	// req.Len is an int64 straight off the wire. Allocating it unchecked is a
	// remote kill switch: one malformed request from any token holder asks for
	// a multi-gigabyte slice on a 512MB device.
	//
	// Note what this does and does not mitigate. A huge-but-representable
	// length is not a catchable panic -- it is a runtime out-of-memory fatal
	// throw, or the OOM killer -- and recover() cannot intercept either. The
	// bounds check below IS the mitigation; the panic-recovery interceptor
	// added alongside it does not help here.
	//
	// Len == 0 is left legal on purpose: a zero-length read returning
	// ReadCount 0 is the end-of-file sentinel the client download loop keys on,
	// so rejecting it would break file downloads.
	if req.Len < 0 {
		return nil, status.Errorf(codes.InvalidArgument, "negative read length %d", req.Len)
	}
	if req.Len > maxReadChunk {
		return nil, status.Errorf(codes.InvalidArgument,
			"read length %d exceeds the %d byte limit", req.Len, maxReadChunk)
	}
	if req.Start < 0 {
		return nil, status.Errorf(codes.InvalidArgument, "negative read offset %d", req.Start)
	}

	chunk := make([]byte, req.Len)
	n, err := common.ReadFile(filePath, req.Start, chunk, perm)
	if err == io.EOF {
		err = nil
	} //we ignore eof error, as eof is indicated by n = 0
	if err != nil {
		return nil, err
	}
	resp = &pb.ReadFileResponse{ReadCount: int64(n), Data: chunk[:n]}
	return
}

func (s *server) FSGetFileInfo(ctx context.Context, req *pb.FileInfoRequest) (resp *pb.FileInfoResponse, err error) {
	// FSGetFileInfo previously accepted any absolute path. Restrict to the
	// same directories the read/write RPCs allow, plus /tmp for HIDScript
	// temp files created by the CLI client.
	safePath, err := safePathInAllowlist(req.Path,
		common.PATH_ROOT, "/tmp")
	if err != nil {
		return &pb.FileInfoResponse{}, err
	}
	fi, err := os.Stat(safePath)
	resp = &pb.FileInfoResponse{}
	if err != nil {
		return
	}
	resp.Name = fi.Name()
	resp.IsDir = fi.IsDir()
	resp.Mode = uint32(fi.Mode())
	resp.ModTime = fi.ModTime().Unix()
	resp.Size = fi.Size()
	return
}

func (s *server) FSCreateTempDirOrFile(ctx context.Context, req *pb.TempDirOrFileRequest) (resp *pb.TempDirOrFileResponse, err error) {
	resp = &pb.TempDirOrFileResponse{}
	if req.OnlyFolder {
		name, err := os.MkdirTemp(req.Dir, req.Prefix)
		if err != nil {
			return resp, err
		}
		resp.ResultPath = name
		return resp, err
	} else {
		var f *os.File
		f, err = os.CreateTemp(req.Dir, req.Prefix)
		if err != nil {
			return resp, err
		}
		defer f.Close()
		resp.ResultPath = f.Name()
		return resp, err
	}
}

func (s *server) HIDGetRunningJobState(ctx context.Context, req *pb.HIDScriptJob) (res *pb.HIDRunningJobStateResult, err error) {
	targetJob, err := s.rootSvc.SubSysUSB.HidScriptGetBackgroundJobByID(int(req.Id))
	if err != nil {
		return nil, err
	}

	vmID, _ := targetJob.GetVMId() // ignore error, as VM ID would be -1 in error case

	//try to convert source to string
	source, ok := targetJob.Source.(string)
	if !ok {
		source = "Couldn't retrieve job's script source"
	}

	return &pb.HIDRunningJobStateResult{
		Id:     int64(targetJob.Id),
		VmId:   int64(vmID),
		Source: source,
	}, nil

}

func (s *server) HIDGetRunningScriptJobs(ctx context.Context, rEmpty *pb.Empty) (jobs *pb.HIDScriptJobList, err error) {
	retJobs, err := s.rootSvc.SubSysUSB.HidScriptGetAllRunningBackgroundJobs()
	if err != nil {
		return nil, err
	}
	jobs = &pb.HIDScriptJobList{}
	for _, aJob := range retJobs {
		jobs.Ids = append(jobs.Ids, uint32(aJob.Id))
	}
	return
}

func (s *server) HIDCancelAllScriptJobs(ctx context.Context, rEmpty *pb.Empty) (empty *pb.Empty, err error) {
	defer s.rootSvc.SubSysEvent.Emit(ConstructEventNotifyStateChange(common_web.STATE_CHANGE_EVT_TYPE_HID))
	err = s.rootSvc.SubSysUSB.HidScriptCancelAllRunningBackgroundJobs()
	return
}

func (s *server) HIDCancelScriptJob(ctx context.Context, sJob *pb.HIDScriptJob) (empty *pb.Empty, err error) {
	defer s.rootSvc.SubSysEvent.Emit(ConstructEventNotifyStateChange(common_web.STATE_CHANGE_EVT_TYPE_HID))
	empty = &pb.Empty{}
	job, err := s.rootSvc.SubSysUSB.HidScriptGetBackgroundJobByID(int(sJob.Id))
	if err != nil {
		return empty, err
	}

	job.Cancel()
	return
}

func (s *server) HIDRunScript(ctx context.Context, scriptReq *pb.HIDScriptRequest) (scriptRes *pb.HIDScriptResult, err error) {
	defer s.rootSvc.SubSysEvent.Emit(ConstructEventNotifyStateChange(common_web.STATE_CHANGE_EVT_TYPE_HID))
	err = s.rootSvc.SubSysUSB.HidScriptUsable()
	if err != nil {
		return
	}

	safePath, err := safePathInAllowlist(scriptReq.ScriptPath, common.PATH_HID_SCRIPTS, "/tmp")
	if err != nil {
		return nil, errors.New(fmt.Sprintf("HIDScript path rejected: %v", err))
	}
	scriptFile, err := os.ReadFile(safePath)
	if err != nil {
		return nil, errors.New(fmt.Sprintf("Couldn't load HIDScript '%s': %v\n", safePath, err))
	}

	if scriptReq.TimeoutSeconds > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Second*time.Duration(scriptReq.TimeoutSeconds))
		defer cancel()
	}

	val, err := s.rootSvc.SubSysUSB.HidScriptRun(ctx, string(scriptFile))
	if err != nil {
		return nil, err
	}

	if jsonVal, err := json.Marshal(val); err == nil {
		scriptRes = &pb.HIDScriptResult{
			IsFinished: true,
			Job:        &pb.HIDScriptJob{Id: 0},
			ResultJson: string(jsonVal),
		}
		return scriptRes, nil
	} else {
		return nil, errors.New(fmt.Sprintf("Script seems to have succeeded but result couldn't be converted to JSON: %v\n", err))
	}

}

func (s *server) HIDRunScriptJob(ctx context.Context, scriptReq *pb.HIDScriptRequest) (rJob *pb.HIDScriptJob, err error) {
	defer s.rootSvc.SubSysEvent.Emit(ConstructEventNotifyStateChange(common_web.STATE_CHANGE_EVT_TYPE_HID))
	err = s.rootSvc.SubSysUSB.HidScriptUsable()
	if err != nil {
		return
	}

	safePath, err := safePathInAllowlist(scriptReq.ScriptPath, common.PATH_HID_SCRIPTS, "/tmp")
	if err != nil {
		return nil, errors.New(fmt.Sprintf("HIDScript path rejected: %v", err))
	}
	scriptFile, err := os.ReadFile(safePath)
	if err != nil {
		return nil, errors.New(fmt.Sprintf("Couldn't load HIDScript '%s': %v\n", safePath, err))
	}

	//Note: Don't use the gRPC context, it would cancel after this call and thus interrupt the job immediately
	jobCtx := context.Background()
	if scriptReq.TimeoutSeconds > 0 {
		var cancel context.CancelFunc
		jobCtx, cancel = context.WithTimeout(jobCtx, time.Second*time.Duration(scriptReq.TimeoutSeconds))
		// The job runs asynchronously so we can't defer cancel(). Instead
		// release the timer as soon as the context is done -- either by the
		// timeout firing or by an external cancel. Prevents the timer leak
		// that go vet -lostcancel would flag.
		go func() {
			<-jobCtx.Done()
			cancel()
		}()
	}
	job, err := s.rootSvc.SubSysUSB.HidScriptStartBackground(jobCtx, string(scriptFile))
	if err != nil {
		return nil, err
	}

	rJob = &pb.HIDScriptJob{
		Id: uint32(job.Id),
	}
	return rJob, nil
}

func (s *server) HIDGetScriptJobResult(ctx context.Context, sJob *pb.HIDScriptJob) (scriptRes *pb.HIDScriptResult, err error) {
	// Try to find script
	job, err := s.rootSvc.SubSysUSB.HidScriptGetBackgroundJobByID(int(sJob.Id))
	if err != nil {
		return nil, err
	}

	val, err := s.rootSvc.SubSysUSB.HidScriptWaitBackgroundJobResult(ctx, job)
	if err != nil {
		return nil, err
	}
	jsonVal, err := json.Marshal(val)
	if err != nil {
		return nil, errors.New(fmt.Sprintf("Script seems to have succeeded but result couldn't be converted to JSON: %v\n", err))
	}
	scriptRes = &pb.HIDScriptResult{
		IsFinished: true,
		Job:        &pb.HIDScriptJob{Id: 0},
		ResultJson: string(jsonVal),
	}
	return scriptRes, nil
}

func (s *server) GetDeployedEthernetInterfaceSettings(ctx context.Context, req *pb.StringMessage) (resp *pb.EthernetInterfaceSettings, err error) {
	if mi, err := s.rootSvc.SubSysNetwork.GetManagedInterface(req.Msg); err == nil {
		return mi.GetState().CurrentSettings, nil
	} else {
		return nil, errors.New(fmt.Sprintf("No stored (or used) settings for ethernet interface '%s'", req.Msg))
	}
	/*
		if settings,exist := ServiceState.StoredNetworkSettings[req.Msg]; exist && settings.SettingsInUse {
			return settings, nil
		} else {
			return nil, errors.New(fmt.Sprintf("No stored (or used) settings for ethernet interface '%s'", req.Msg))
		}
	*/
}

func (s *server) GetAllDeployedEthernetInterfaceSettings(ctx context.Context, empty *pb.Empty) (resp *pb.DeployedEthernetInterfaceSettings, err error) {
	miList := s.rootSvc.SubSysNetwork.GetManagedInterfaceNames()
	deployed := make([]*pb.EthernetInterfaceSettings, len(miList))
	for idx, name := range miList {
		mi, err := s.rootSvc.SubSysNetwork.GetManagedInterface(name)
		if err != nil {
			return nil, err
		}
		deployed[idx] = mi.GetState().CurrentSettings
	}
	resp = &pb.DeployedEthernetInterfaceSettings{
		List: deployed,
	}
	return resp, nil
}

func (s *server) DeployEthernetInterfaceSettings(ctx context.Context, es *pb.EthernetInterfaceSettings) (empty *pb.Empty, err error) {
	defer s.rootSvc.SubSysEvent.Emit(ConstructEventNotifyStateChange(common_web.STATE_CHANGE_EVT_TYPE_NETWORK))
	log.Printf("Trying to deploy ethernet interface settings %v\n", es)

	empty = &pb.Empty{}
	iname := es.Name
	nim, err := s.rootSvc.SubSysNetwork.GetManagedInterface(iname)
	if err != nil {
		return empty, err
	}

	err = nim.DeploySettings(es)
	if err != nil {
		log.Printf("Error deploying ethernet interface settings %v\n", err)
	}
	return
}

func (s *server) StoreEthernetInterfaceSettings(ctx context.Context, req *pb.EthernetRequestSettingsStorage) (empty *pb.Empty, err error) {
	defer s.rootSvc.SubSysEvent.Emit(ConstructEventNotifyStateChange(common_web.STATE_CHANGE_EVT_TYPE_STORED_ETHERNET_INTERFACE_SETTINGS_LIST))
	empty = &pb.Empty{}
	ifName := req.Settings.Name
	storageKey := cSTORE_PREFIX_ETHERNET_INTERFACE_SETTINGS + ifName + "_" + req.TemplateName
	err = s.rootSvc.SubSysDataStore.Put(storageKey, req.Settings, true)
	return
}

func (s *server) GetStoredEthernetInterfaceSettings(ctx context.Context, m *pb.StringMessage) (eis *pb.EthernetInterfaceSettings, err error) {
	eis = &pb.EthernetInterfaceSettings{}
	err = s.rootSvc.SubSysDataStore.Get(cSTORE_PREFIX_ETHERNET_INTERFACE_SETTINGS+m.Msg, eis)
	return
}

func (s *server) DeployStoredEthernetInterfaceSettings(ctx context.Context, msg *pb.StringMessage) (empty *pb.Empty, err error) {
	defer s.rootSvc.SubSysEvent.Emit(ConstructEventNotifyStateChange(common_web.STATE_CHANGE_EVT_TYPE_HID))
	eis, err := s.GetStoredEthernetInterfaceSettings(ctx, msg)
	if err != nil {
		return empty, err
	}
	return s.DeployEthernetInterfaceSettings(ctx, eis)
}

func (s *server) ListStoredEthernetInterfaceSettings(ctx context.Context, empty *pb.Empty) (messages *pb.StringMessageArray, err error) {
	messages = &pb.StringMessageArray{}
	res, err := s.rootSvc.SubSysDataStore.KeysPrefix(cSTORE_PREFIX_ETHERNET_INTERFACE_SETTINGS, true)
	if err != nil {
		return messages, err
	}
	messages.MsgArray = res
	return
}

func (s *server) MountUMSFile(ctx context.Context, gsu *pb.GadgetSettingsUMS) (*pb.Empty, error) {
	defer s.rootSvc.SubSysEvent.Emit(ConstructEventNotifyStateChange(common_web.STATE_CHANGE_EVT_TYPE_USB))
	log.Printf("Trying to mount iamge `%s` to UMS ...", gsu.File)
	err := MountUMSFile(gsu.File)
	return nil, err
}

func (s *server) GetDeployedGadgetSetting(ctx context.Context, e *pb.Empty) (gs *pb.GadgetSettings, err error) {
	log.Printf("Called get deployed gadget settings\n")
	gs, err = s.rootSvc.SubSysUSB.ParseGadgetState(USB_GADGET_NAME)

	if err != nil {
		log.Printf("Error parsing current gadget config: %v", err)
		err = usbStatus(err)
		return
	}

	gs.DevPathHidKeyboard = s.rootSvc.SubSysUSB.State.DevicePath[USB_FUNCTION_HID_KEYBOARD_name]
	gs.DevPathHidMouse = s.rootSvc.SubSysUSB.State.DevicePath[USB_FUNCTION_HID_MOUSE_name]
	gs.DevPathHidRaw = s.rootSvc.SubSysUSB.State.DevicePath[USB_FUNCTION_HID_RAW_name]

	return
}

func (s *server) DeployGadgetSetting(ctx context.Context, newGs *pb.GadgetSettings) (gs *pb.GadgetSettings, err error) {
	log.Printf("Called DeployGadgetSettings\n")
	defer s.rootSvc.SubSysEvent.Emit(ConstructEventNotifyStateChange(common_web.STATE_CHANGE_EVT_TYPE_USB))
	gs_backup, _ := s.rootSvc.SubSysUSB.ParseGadgetState(USB_GADGET_NAME)

	errg := s.rootSvc.SubSysUSB.DeployGadgetSettings(newGs)
	err = nil
	if errg != nil {
		err = usbStatus(fmt.Errorf("Deploying new gadget settings failed, reverted to old ones: %w", errg))
		s.rootSvc.SubSysUSB.DeployGadgetSettings(gs_backup) //We don't catch the error, as the old settings should have been working
	}

	gs, _ = s.rootSvc.SubSysUSB.ParseGadgetState(USB_GADGET_NAME) //Return settings from deployed gadget
	return
}

func (s *server) GetLEDSettings(context.Context, *pb.Empty) (res *pb.LEDSettings, err error) {
	//	res, err = ServiceState.Led.GetLed()
	state := s.rootSvc.SubSysLed.GetState()
	res = &pb.LEDSettings{
		BlinkCount: *state.BlinkCount,
	}
	log.Printf("GetLEDSettings, result: %+v", res)
	return
}

func (s *server) SetLEDSettings(ctx context.Context, ls *pb.LEDSettings) (*pb.Empty, error) {
	defer s.rootSvc.SubSysEvent.Emit(ConstructEventNotifyStateChange(common_web.STATE_CHANGE_EVT_TYPE_LED))
	log.Printf("SetLEDSettings %+v", ls)
	s.rootSvc.SubSysLed.DeploySettings(ls)
	return &pb.Empty{}, nil
}

/*
func StartRpcServer(host string, port string) {
	listen_address := host + ":" + port
	//Open TCP listener
	log.Printf("P4wnP1 RPC server listening on " + listen_address)
	lis, err := net.Listen("tcp", listen_address)
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	//Create gRPC Server
	s := grpc.NewServer()
	pb.RegisterP4WNP1Server(s, &server{})
	if err := s.Serve(lis); err != nil {
		log.Fatalf("Failed to serve: %v", err)
	}
}
*/

func folderReader(fn http.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if strings.HasSuffix(req.URL.Path, "/") {
			// Use contents of index.html for directory, if present.
			req.URL.Path = path.Join(req.URL.Path, "index.html")
		}
		fn.ServeHTTP(w, req)
	}
}

/*
func StartRpcWebServer(host string, port string) {
	//Create gRPC Server
	s := grpc.NewServer()
	pb.RegisterP4WNP1Server(s, &server{})

	//grpc_web_srv := grpcweb.WrapServer(s, grpcweb.WithWebsockets(true)) //Wrap server to improbable grpc-web with websockets
	grpc_web_srv := grpcweb.WrapServer(s) //Wrap server to improbable grpc-web with websockets

	http_handler := func(resp http.ResponseWriter, req *http.Request) {
		grpc_web_srv.ServeHTTP(resp, req)
	}

	listen_address := host + ":" + port
	http_srv := &http.Server{
		Addr: listen_address,
		Handler: http.HandlerFunc(http_handler),
		//ReadHeaderTimeout: 5*time.Second,
		//IdleTimeout: 120*time.Second,
	}


	//Open TCP listener
	log.Printf("P4wnP1 gRPC-web server listening on " + listen_address)
	log.Fatal(http_srv.ListenAndServe())
}
*/

// errWifiUnavailable is returned by the WiFi RPCs when the subsystem failed to
// initialise (no wlan0 on this board, or hostapd/wpa_supplicant missing).
// maxReadChunk caps a single FSReadFile allocation. The largest thing anyone
// legitimately reads through this API is a HIDScript or a template; 8MiB is
// generous for both and small enough to be harmless on a 512MB Pi Zero W.
const maxReadChunk = 8 << 20

const errWifiUnavailable = "the WiFi subsystem is unavailable on this device"

// wifiCurrentSettings returns the deployed WiFi settings, or nil when the
// subsystem is unavailable, so callers building a composite message do not
// nil-dereference.
func wifiCurrentSettings(s *server) *pb.WiFiSettings {
	if s.rootSvc.SubSysWifi == nil || s.rootSvc.SubSysWifi.State == nil {
		return nil
	}
	return s.rootSvc.SubSysWifi.State.CurrentSettings
}

func (srv *server) StartRpcServerAndWeb(host string, gRPCPort string, webPort string, absWebRoot string) {
	//ToDo: Return servers/TCP listener to allow closing from caller
	listen_address_grpc := host + ":" + gRPCPort
	listen_address_web := host + ":" + webPort

	authMgr := srv.rootSvc.SubSysAuth // populated by NewService()

	// gRPC server with auth interceptors. EVERY RPC requires a valid
	// bearer token in metadata. There is no exempt-method list -- there
	// are no RPCs that should be reachable without auth. (The HTTP
	// /api/auth/login endpoint is what callers use to obtain a token.)
	// Interceptor order matters: recovery is OUTERMOST so it also covers a
	// panic raised inside the auth interceptor itself. grpc-go does not
	// recover handler panics on its own, so without this any panic anywhere in
	// a handler takes down the whole process -- and this device is an
	// appliance, so that means it stops being reachable at all.
	s := grpc.NewServer(
		grpc.ChainUnaryInterceptor(
			recoveryUnaryInterceptor,
			auth.UnaryInterceptor(authMgr),
		),
		grpc.ChainStreamInterceptor(
			recoveryStreamInterceptor,
			auth.StreamInterceptor(authMgr),
		),
	)
	pb.RegisterP4WNP1Server(s, srv)

	//Open TCP listener
	lis, err := net.Listen("tcp", listen_address_grpc)
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	// run gRPC server in go routine
	go func() {
		if err := s.Serve(lis); err != nil {
			log.Fatalf("Failed to serve: %v", err)
		}
	}()
	log.Printf("P4wnP1 gRPC server listening on %s", listen_address_grpc)

	//Wrap the server into a gRPC-web server
	// The library default is originFunc = allow-everything with
	// AllowCredentials:true, which answers CORS preflights from any website.
	// Every shipped client is same-origin, so deny cross-origin outright.
	grpc_web_srv := grpcweb.WrapServer(s,
		grpcweb.WithWebsockets(true),
		grpcweb.WithOriginFunc(func(string) bool { return false }),
	)

	// HTTP auth handler -- serves /api/auth/login + whoami + changepw +
	// logout + health. These bypass the gRPC interceptor entirely; the
	// handler itself enforces token requirements for the operations that
	// need them.
	authHTTPHandler := auth.HTTPHandler(authMgr)

	// JSON API (see rest_api.go). This is what the modern web client talks to.
	// It is a peer of the gRPC-web surface, not a replacement: the CLI still
	// uses gRPC directly. Build failure here is non-fatal -- the device should
	// still boot and be reachable over gRPC/CLI even if the JSON face is
	// broken, because that is the operator's route to fixing it.
	apiHandler, apiErr := NewAPIHandler(srv, authMgr)
	if apiErr != nil {
		log.Printf("WARNING: JSON API unavailable: %v", apiErr)
	}

	// Master HTTP router. Four buckets, in order:
	//   1. /api/auth/* -> auth HTTP handler (login, whoami, changepw, health)
	//   2. /api/v1/*   -> JSON API (rest_api.go); what the web client uses
	//   3. gRPC / gRPC-web (Content-Type or websocket header) -> grpc_web_srv
	//   4. everything else -> static files from the web root
	//
	// The static file server is intentionally unauthenticated: the SPA
	// needs to load BEFORE the user can log in. JS code in the SPA is
	// responsible for redirecting to /login when /api/auth/whoami 401s.
	fileServer := http.FileServer(http.Dir(absWebRoot))
	http_handler := func(resp http.ResponseWriter, req *http.Request) {
		if strings.HasPrefix(req.URL.Path, auth.HTTPPrefix) {
			authHTTPHandler.ServeHTTP(resp, req)
			return
		}
		if apiHandler != nil && strings.HasPrefix(req.URL.Path, APIPrefix) {
			apiHandler.ServeHTTP(resp, req)
			return
		}
		if strings.Contains(req.Header.Get("Content-Type"), "application/grpc") ||
			req.Method == "OPTIONS" ||
			strings.Contains(req.Header.Get("Sec-Websocket-Protocol"), "grpc-websockets") {
			grpc_web_srv.ServeHTTP(resp, req)
			return
		}
		// The operator console lives under /app/. Send the bare root there
		// rather than serving its index.html from "/", because the page loads
		// its CSS and JS with relative paths -- served from "/" those would
		// resolve to /css/... and 404.
		//
		// The legacy GopherJS client is still present at /index.html but is
		// NOT functional: it predates the auth layer and has no code to send a
		// bearer token, so every RPC it makes returns Unauthenticated. It is
		// kept only so an existing deployment's bookmarks do not 404.
		if req.URL.Path == "/" {
			http.Redirect(resp, req, "/app/", http.StatusFound)
			return
		}
		fileServer.ServeHTTP(resp, req)
	}

	//Setup our HTTP server
	//
	// Everything is wrapped in RecoverHandler. The JSON bridge calls service
	// methods directly by reflection, so the gRPC interceptor chain does NOT
	// run for requests arriving over /api/v1/ -- without this, a panic in any
	// RPC reached from the web console ends the process.
	http_srv := &http.Server{
		Addr:              listen_address_web, //listen on port 80 with webservice
		Handler:           RecoverHandler(http.HandlerFunc(http_handler)),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	go func() {
		if err_http := http_srv.ListenAndServe(); err_http != nil {
			log.Fatal(err)
		}
	}()
	log.Printf("P4wnP1 gRPC-web server listening on %s", http_srv.Addr)
}
