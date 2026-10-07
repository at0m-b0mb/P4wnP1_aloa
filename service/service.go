//go:build linux
// +build linux

package service

import (
	"context"
	"fmt"
	"github.com/mame82/P4wnP1_aloa/common"
	"github.com/mame82/P4wnP1_aloa/common_web"
	pb "github.com/mame82/P4wnP1_aloa/proto"
	"github.com/mame82/P4wnP1_aloa/service/auth"
	"github.com/mame82/P4wnP1_aloa/service/datastore"
	"log"
	"syscall"
	"time"
)

// AuthFilePath is the on-disk location of the bcrypt password file. The
// installer + first-boot helper write it; the service reads it. Mode 0600
// owned by root.
const AuthFilePath = "/etc/p4wnp1/auth.json"

func RegisterDefaultTriggerActions(tam *TriggerActionManager) {
	// create test trigger

	// Trigger to run startup script
	serviceUpRunScript := &pb.TriggerAction{
		IsActive:  true,
		Immutable: true,
		OneShot:   false,
		Trigger: &pb.TriggerAction_ServiceStarted{
			ServiceStarted: &pb.TriggerServiceStarted{},
		},
		Action: &pb.TriggerAction_BashScript{
			BashScript: &pb.ActionStartBashScript{
				ScriptName: "servicestart.sh",
			},
		},
	}
	tam.AddTriggerAction(serviceUpRunScript)

	/*
		logServiceStart := &pb.TriggerAction{
			IsActive: true,
			Trigger: &pb.TriggerAction_ServiceStarted{
				ServiceStarted: &pb.TriggerServiceStarted{},
			},
			Action: &pb.TriggerAction_Log{
				Log: &pb.ActionLog{},
			},
		}
		tam.AddTriggerAction(logServiceStart)

		logDHCPLease := &pb.TriggerAction{
			IsActive: true,
			Trigger: &pb.TriggerAction_DhcpLeaseGranted{
				DhcpLeaseGranted: &pb.TriggerDHCPLeaseGranted{},
			},
			Action: &pb.TriggerAction_Log{
				Log: &pb.ActionLog{},
			},
		}
		tam.AddTriggerAction(logDHCPLease)

		logUSBGadgetConnected := &pb.TriggerAction{
			IsActive: true,
			Trigger: &pb.TriggerAction_UsbGadgetConnected{
				UsbGadgetConnected: &pb.TriggerUSBGadgetConnected{},
			},
			Action: &pb.TriggerAction_Log{
				Log: &pb.ActionLog{},
			},
		}
		tam.AddTriggerAction(logUSBGadgetConnected)

		logUSBGadgetDisconnected := &pb.TriggerAction{
			IsActive: true,
			Trigger: &pb.TriggerAction_UsbGadgetDisconnected{
				UsbGadgetDisconnected: &pb.TriggerUSBGadgetDisconnected{},
			},
			Action: &pb.TriggerAction_Log{
				Log: &pb.ActionLog{},
			},
		}
		tam.AddTriggerAction(logUSBGadgetDisconnected)

		logWifiAp := &pb.TriggerAction{
			IsActive: true,
			Trigger: &pb.TriggerAction_WifiAPStarted{
				WifiAPStarted: &pb.TriggerWifiAPStarted{},
			},
			Action: &pb.TriggerAction_Log{
				Log: &pb.ActionLog{},
			},
		}
		tam.AddTriggerAction(logWifiAp)

		logWifiSta := &pb.TriggerAction{
			IsActive: true,
			Trigger: &pb.TriggerAction_WifiConnectedAsSta{
				WifiConnectedAsSta: &pb.TriggerWifiConnectedAsSta{},
			},
			Action: &pb.TriggerAction_Log{
				Log: &pb.ActionLog{},
			},
		}
		tam.AddTriggerAction(logWifiSta)

		logSSHLogin := &pb.TriggerAction{
			IsActive: true,
			Trigger: &pb.TriggerAction_SshLogin{
				SshLogin: &pb.TriggerSSHLogin{},
			},
			Action: &pb.TriggerAction_Log{
				Log: &pb.ActionLog{},
			},
		}
		tam.AddTriggerAction(logSSHLogin)
	*/
}

type Service struct {
	SubSysDataStore *datastore.Store // very first service
	//	SubSysState          interface{}
	//	SubSysLogging        interface{}
	SubSysNetwork *NetworkManager

	SubSysEvent          *EventManager
	SubSysUSB            *UsbGadgetManager
	SubSysLed            *LedService
	SubSysWifi           *WiFiService
	SubSysBluetooth      *BtService
	SubSysRPC            *server
	SubSysAuth           *auth.Manager
	SubSysTriggerActions *TriggerActionManager
	SubSysGpio           *GpioManager

	SubSysDwc2ConnectWatcher *Dwc2ConnectWatcher

	Ctx            context.Context
	Cancel         context.CancelFunc
	rebootOnStop   bool
	shutdownOnStop bool
}

func NewService() (svc *Service, err error) {
	svc = &Service{}
	svc.Ctx, svc.Cancel = context.WithCancel(context.Background())

	svc.SubSysDataStore, err = datastore.Open(common.PATH_DATA_STORE, common.PATH_DATA_STORE_BACKUP+"/init.db")
	if err != nil {
		return nil, err
	}

	svc.SubSysEvent = NewEventManager(20)

	svc.SubSysLed = NewLedService()
	svc.SubSysNetwork, err = NewNetworkManager(svc) //Depends on EvenSubSys
	if err != nil {
		return nil, err
	}
	svc.SubSysUSB, err = NewUSBGadgetManager(svc) //Depends on NetworkSubSys, EvenSubSys
	//	if err == ErrUsbNotUsable { err = nil } //ToDo: delete this
	if err != nil {
		return nil, err
	}

	// Depends on NetworkSubSys. A WiFi failure is NOT fatal: a Pi Zero has no
	// WiFi at all, and on a cold boot wlan0 may simply not have appeared yet.
	// Everything else -- USB gadget, HID, the console -- must still come up.
	if svc.SubSysWifi, err = NewWifiService(svc); err != nil {
		log.Printf("WARNING: WiFi subsystem unavailable: %v", err)
		log.Printf("         The device will run without WiFi; the WiFi RPCs will report this.")
		svc.SubSysWifi = nil
		// Clear it. NewService returns this same `err` at the end, and main()
		// panics on a non-nil return -- so logging "we will carry on without
		// WiFi" and then leaving err set took the whole device down two lines
		// later, which is the exact failure this block exists to prevent.
		err = nil
	}

	svc.SubSysGpio = NewGpioManager(svc) //Depends on event subsys

	svc.SubSysTriggerActions = NewTriggerActionManager(svc) //Depends on EventManager, UsbGadgetManager (to trigger HID scripts) and GpioManager

	svc.SubSysDwc2ConnectWatcher = NewDwc2ConnectWatcher(svc) // Depends on EventManager, should be started before USB gadget settings are deployed (to avoid missing initial state change)

	svc.SubSysBluetooth = NewBtService(svc, time.Second*120) //Depends on NetworkSubSys (try to bring up bluetooth for up to 120s in background)

	// Auth subsystem: load the password file (mode 0600). A missing file is
	// treated as "no users configured yet" -- the service still starts but
	// every gRPC and HTTP request is rejected until the first-boot helper
	// (or install.sh) bootstraps an admin account. We log loudly so the
	// operator notices.
	authStore, authErr := auth.NewStore(AuthFilePath)
	if authErr != nil {
		log.Printf("WARNING: auth: failed to load %s: %v", AuthFilePath, authErr)
		log.Printf("WARNING: auth: starting with an empty user table -- ALL gRPC/HTTP CALLS WILL FAIL")
		authStore, _ = auth.NewStore("") // empty store, no persistence
	}
	if !authStore.HasAnyUsers() {
		log.Printf("WARNING: auth: no users in %s -- ALL gRPC/HTTP CALLS WILL FAIL until firstboot runs", AuthFilePath)
	}
	svc.SubSysAuth = auth.NewManager(authStore, auth.NewSessions(), auth.DefaultSessionTTL)

	svc.SubSysRPC = NewRpcServerService(svc) //Depends on all other
	return
}

func (s *Service) Start() (context.Context, context.CancelFunc) {
	log.Println("Starting service ...")

	s.SubSysEvent.Start()
	s.SubSysDwc2ConnectWatcher.Start()
	s.SubSysGpio.Start()
	s.SubSysLed.Start()
	s.SubSysRPC.StartRpcServerAndWeb("0.0.0.0", "50051", "8000", common.PATH_WEBROOT) //start gRPC service

	// Issue the credential the device's own scripts authenticate with, BEFORE
	// anything that runs one. servicestart.sh and every user trigger action
	// drive the box through P4wnP1_cli, which talks to the API we just
	// started; without this they all fail Unauthenticated and a device whose
	// startup template failed comes up with no USB gadget, no DHCP and no
	// WiFi AP. See service/auth/localcred.go.
	if err := s.SubSysAuth.ProvisionLocalToken(auth.LocalTokenPath); err != nil {
		// Not fatal -- the console still works, and saying so beats dying
		// silently -- but local scripts will not be able to authenticate.
		log.Printf("WARNING: auth: could not issue the local script credential: %v", err)
		log.Printf("WARNING: auth: P4wnP1_cli calls from scripts on this device will fail")
	} else {
		log.Printf("auth: issued local script credential at %s (root-only)", auth.LocalTokenPath)
		go s.SubSysAuth.KeepLocalTokenFresh(auth.LocalTokenPath, s.Ctx.Done())
	}

	log.Println("Starting TriggerAction event listener ...")
	s.SubSysTriggerActions.Start()

	// Register TriggerActions
	/*
		log.Println("Register default TriggerActions ...")
		RegisterDefaultTriggerActions(s.SubSysTriggerActions)
	*/

	scriptFallback := false
	//retrieve Startup MasterTemplate name from store
	msgTemplateName := &pb.StringMessage{}
	errTemplateName := s.SubSysDataStore.Get(cSTORE_STARTUP_MASTER_TEMPLATE, msgTemplateName)
	if errTemplateName == nil {
		startupTemplate := msgTemplateName.Msg
		fmt.Printf("Loading MasterTemplate '%s' for startup ...\n", startupTemplate)

		// Deploy MasterTemplate
		_, errDeployStartupTemplate := s.SubSysRPC.DeployStoredMasterTemplate(context.Background(), &pb.StringMessage{Msg: startupTemplate})
		if errDeployStartupTemplate != nil {
			fmt.Printf("... error deploying Startup MasterTemplate '%s': %v\n", startupTemplate, errDeployStartupTemplate)
			scriptFallback = true
		}
	} else {
		fmt.Println("... error retrieving name for Startup MasterTemplate")
		scriptFallback = true
	}

	if scriptFallback {
		fmt.Println("... Fallback: Deploying TriggerAction for script based startup with 'servicestart.sh'")
		RegisterDefaultTriggerActions(s.SubSysTriggerActions)
	}

	// fire service started Event
	log.Println("Fire service started event ...")
	s.SubSysEvent.Emit(ConstructEventTrigger(common_web.TRIGGER_EVT_TYPE_SERVICE_STARTED))

	return s.Ctx, s.Cancel
}

func (s *Service) Reboot() {
	s.rebootOnStop = true
	s.Cancel()
}

func (s *Service) Shutdown() {
	s.shutdownOnStop = true
	s.Cancel()
}

// safeStop runs one subsystem teardown, converting a panic into a log line.
//
// Each teardown is wrapped INDIVIDUALLY and deliberately. A single
// `defer recover()` at the top of Stop() would not do: by the time it ran, the
// frame would already have unwound past the reboot and poweroff syscalls
// below, so the panic would be caught, logged -- and the device still would
// not reboot. That is the bug this is here to prevent, not a hypothetical.
func safeStop(name string, stop func()) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("WARNING: panic while stopping %s: %v (continuing shutdown)", name, r)
		}
	}()
	if stop != nil {
		stop()
	}
}

func (s *Service) Stop() {
	// Reaching the syscalls below matters more than any individual teardown
	// succeeding. Before this, a nil Bluetooth controller (every board with no
	// adapter) or a nil netlink family (every stock kernel) panicked here, and
	// `P4wnP1_cli system reboot`, the console's power buttons and
	// `systemctl stop P4wnP1` all silently did nothing.
	if s.SubSysTriggerActions != nil {
		safeStop("trigger actions", s.SubSysTriggerActions.Stop)
	}
	if s.SubSysLed != nil {
		safeStop("LED", s.SubSysLed.Stop)
	}
	if s.SubSysGpio != nil {
		safeStop("GPIO", s.SubSysGpio.Stop)
	}
	if s.SubSysBluetooth != nil {
		safeStop("Bluetooth", s.SubSysBluetooth.Stop)
	}
	if s.SubSysDwc2ConnectWatcher != nil {
		safeStop("dwc2 connect watcher", func() { _ = s.SubSysDwc2ConnectWatcher.Stop() })
	}
	if s.SubSysEvent != nil {
		safeStop("event manager", s.SubSysEvent.Stop)
	}
	// Drop the local script credential. /run is a tmpfs so the file would go
	// on reboot anyway, but `systemctl stop` is not a reboot.
	if s.SubSysAuth != nil {
		safeStop("local credential", func() {
			if err := s.SubSysAuth.RemoveLocalToken(); err != nil {
				log.Printf("WARNING: auth: could not remove %s: %v", auth.LocalTokenPath, err)
			}
			s.SubSysAuth.Close()
		})
	}
	// The datastore was never closed. badger needs a clean close to flush its
	// value log; skipping it is how template edits get lost on a reboot.
	if s.SubSysDataStore != nil {
		safeStop("datastore", func() { s.SubSysDataStore.Close() })
	}

	if s.rebootOnStop {
		fmt.Println("Rebooting...")
		syscall.Sync()
		syscall.Reboot(syscall.LINUX_REBOOT_CMD_RESTART)
	}

	if s.shutdownOnStop {
		fmt.Println("Shutdown...")
		syscall.Sync()
		syscall.Reboot(syscall.LINUX_REBOOT_CMD_POWER_OFF)
	}
}
