package cli_client

import (
	"errors"
	"fmt"
	pb "github.com/mame82/P4wnP1_aloa/proto"
	"github.com/spf13/cobra"
	"google.golang.org/grpc/status"
	"os"
	"strings"
)

// Empty settings used to store cobra flags
var (
	tmpWifiStrReg        string = ""
	tmpWifiStrChannel    uint8  = 0
	tmpWifiHideSSID      bool   = false
	tmpWifiDisabled      bool   = false
	tmpWifiDisableNexmon bool   = false
	tmpWifiSSID          string = ""
	tmpWifiPSK           string = ""
)

/*
func init(){
	//Configure spew for struct deep printing (disable using printer interface for gRPC structs)
	spew.Config.Indent="\t"
	spew.Config.DisableMethods = true
	spew.Config.DisablePointerAddresses = true
}
*/

var wifiCmd = &cobra.Command{
	Use:   "wifi",
	Short: "Configure WiFi (spawn Access Point or join WiFi networks)",
}

var wifiSetCmd = &cobra.Command{
	Use:   "set",
	Short: "set WiFi settings",
	Long:  ``,
}

var wifiSetAPCmd = &cobra.Command{
	Use:   "ap",
	Short: "Configure WiFi interface as access point",
	Long:  ``,
	Run:   cobraWifiSetAP,
}

var wifiSetStaCmd = &cobra.Command{
	Use:   "sta",
	Short: "Configure WiFi interface to join a network as station",
	Long:  ``,
	Run:   cobraWifiSetSta,
}

var wifiGetCmd = &cobra.Command{
	Use:   "get",
	Short: "get WiFi settings",
	Long:  ``,
	Run:   cobraWifiGet,
}

func cobraWifiGet(cmd *cobra.Command, args []string) {
	return
}

// Printing WiFi settings and state WITHOUT the pre-shared key.
//
// These four call sites used fmt.Printf("%+v") on a WiFiSettings and a
// WiFiState. Both carry ap_BSS.PSK and client_BSS.PSK, so every one of them
// printed the access point's pre-shared key to stdout.
//
// That is not only an interactive concern. servicestart.sh -- the fallback
// the service runs on every boot whose startup template fails -- calls
// `P4wnP1_cli wifi set ap ... -k ...`, and the service captures that script's
// stdout into the journal. So the PSK landed in journalctl on a real device,
// found by tools/hardware-check.sh grepping the journal for the actual
// secret rather than for the word "psk".
//
// The service was fixed for exactly this once before, in
// describeWifiSettings. The CLI was never done. Same bug, same %+v, four
// more places.
func describePSK(psk string) string {
	if psk == "" {
		return "(none)"
	}
	return fmt.Sprintf("(set, %d chars)", len(psk))
}

func describeWifiSettings(w *pb.WiFiSettings) string {
	if w == nil {
		return "(none)"
	}
	out := fmt.Sprintf("name:%q mode:%v reg:%q channel:%d disabled:%v",
		w.Name, w.WorkingMode, w.Regulatory, w.Channel, w.Disabled)
	if w.Ap_BSS != nil {
		out += fmt.Sprintf(" ap{ssid:%q psk:%s}", w.Ap_BSS.SSID, describePSK(w.Ap_BSS.PSK))
	}
	for i, c := range w.Client_BSSList {
		if c == nil {
			continue
		}
		out += fmt.Sprintf(" client%d{ssid:%q psk:%s}", i, c.SSID, describePSK(c.PSK))
	}
	return out
}

func describeWifiState(st *pb.WiFiState) string {
	if st == nil {
		return "(none)"
	}
	return fmt.Sprintf("mode:%v channel:%d ssid:%q settings:{%s}",
		st.Mode, st.Channel, st.Ssid, describeWifiSettings(st.CurrentSettings))
}

func cobraWifiSetAP(cmd *cobra.Command, args []string) {
	settings, err := createWifiAPSettings(tmpWifiStrChannel, tmpWifiStrReg, tmpWifiSSID, tmpWifiPSK, tmpWifiHideSSID, tmpWifiDisableNexmon, tmpWifiDisabled)
	if err != nil {
		fmt.Printf("Error: %v\n", err)

		os.Exit(-1) //exit with error
		return
	}

	fmt.Printf("Deploying WiFi interface settings:\n\t%s\n", describeWifiSettings(settings))

	state, err := ClientDeployWifiSettings(StrRemoteHost, StrRemotePort, settings)
	if err != nil {
		fmt.Println(status.Convert(err).Message())
		os.Exit(-1) //exit with error
	} else {
		fmt.Printf("%s\n", describeWifiState(state))
	}
	return
}

func cobraWifiSetSta(cmd *cobra.Command, args []string) {
	settings, err := createWifiStaSettings(tmpWifiStrReg, tmpWifiSSID, tmpWifiPSK, tmpWifiDisableNexmon, tmpWifiDisabled)

	if err != nil {
		fmt.Printf("Error: %v\n", err)

		os.Exit(-1) //exit with error
		return
	}

	fmt.Printf("Deploying WiFi interface settings:\n\t%s\n", describeWifiSettings(settings))

	state, err := ClientDeployWifiSettings(StrRemoteHost, StrRemotePort, settings)
	if err != nil {
		fmt.Println(status.Convert(err).Message())
		os.Exit(-1) //exit with error
	} else {
		fmt.Printf("%s\n", describeWifiState(state))
	}
	return
}

func createWifiAPSettings(channel uint8, reg string, strSSID string, strPSK string, hideSsid bool, nonexmon bool, disabled bool) (settings *pb.WiFiSettings, err error) {
	if channel < 1 || channel > 14 {
		return nil, errors.New(fmt.Sprintf("Only 2.4GHz channels between 1 and 14 are supported, but '%d' was given\n", channel))
	}

	if len(reg) != 2 {
		return nil, errors.New(fmt.Sprintf("Regulatory domain has to consist of two uppercase letters (ISO/IEC 3166-1 alpha2), but '%s' was given\n", reg))
	}
	reg = strings.ToUpper(reg)

	if len(strSSID) < 1 || len(strSSID) > 32 {
		return nil, errors.New(fmt.Sprintf("SSID has to consist of 1 to 32 ASCII letters (even if hidden), but '%s' was given\n", strSSID))
	}

	if len(strPSK) > 0 && len(strPSK) < 8 {
		return nil, errors.New(fmt.Sprintf("A non-empty PSK implies WPA2 and has to have a minimum of 8 characters, but given PSK has '%d' charactres\n", len(strPSK)))
	}

	settings = &pb.WiFiSettings{
		WorkingMode: pb.WiFiWorkingMode_AP,
		AuthMode:    pb.WiFiAuthMode_OPEN,
		Disabled:    disabled,
		Regulatory:  reg,
		Channel:     uint32(channel),
		HideSsid:    hideSsid,
		Ap_BSS: &pb.WiFiBSSCfg{
			SSID: strSSID,
			PSK:  strPSK,
		},
		Client_BSSList: []*pb.WiFiBSSCfg{},
		Nexmon:         !nonexmon,
		Name:           "default",
	}

	if len(strPSK) > 0 {
		settings.AuthMode = pb.WiFiAuthMode_WPA2_PSK //if PSK is given use WPA2
	}

	return settings, err
}

func createWifiStaSettings(reg string, strSSID string, strPSK string, nonexmon bool, disabled bool) (settings *pb.WiFiSettings, err error) {
	if len(reg) != 2 {
		return nil, errors.New(fmt.Sprintf("Regulatory domain has to consist of two uppercase letters (ISO/IEC 3166-1 alpha2), but '%s' was given\n", reg))
	}
	reg = strings.ToUpper(reg)

	if len(strSSID) < 1 || len(strSSID) > 32 {
		return nil, errors.New(fmt.Sprintf("SSID has to consist of 1 to 32 ASCII letters (even if hidden), but '%s' was given\n", strSSID))
	}

	if len(strPSK) > 0 && len(strPSK) < 8 {
		return nil, errors.New(fmt.Sprintf("A non-empty PSK implies WPA2 and has to have a minimum of 8 characters, but given PSK has '%d' charactres\n", len(strPSK)))
	}

	settings = &pb.WiFiSettings{
		WorkingMode: pb.WiFiWorkingMode_STA,
		AuthMode:    pb.WiFiAuthMode_OPEN,
		Disabled:    disabled,
		Regulatory:  reg,
		Client_BSSList: []*pb.WiFiBSSCfg{
			&pb.WiFiBSSCfg{
				SSID: strSSID,
				PSK:  strPSK,
			},
		},
		Nexmon:   !nonexmon,
		Ap_BSS:   &pb.WiFiBSSCfg{}, //not needed
		Name:     "default",
		HideSsid: false,
	}

	if len(strPSK) > 0 {
		settings.AuthMode = pb.WiFiAuthMode_WPA2_PSK //if PSK is given use WPA2
	}

	return settings, err
}

func init() {
	rootCmd.AddCommand(wifiCmd)
	//wifiCmd.AddCommand(wifiGetCmd)
	wifiCmd.AddCommand(wifiSetCmd)
	wifiSetCmd.AddCommand(wifiSetAPCmd)
	wifiSetCmd.AddCommand(wifiSetStaCmd)

	wifiSetCmd.PersistentFlags().StringVarP(&tmpWifiStrReg, "reg", "r", "US", "Sets the regulatory domain according to ISO/IEC 3166-1 alpha2")
	wifiSetCmd.PersistentFlags().BoolVarP(&tmpWifiDisabled, "disable", "d", false, "The flag disables the WiFi interface (omitting the flag enables the interface")
	wifiSetCmd.PersistentFlags().BoolVarP(&tmpWifiDisableNexmon, "nonexmon", "n", false, "Don't use the modified nexmon firmware")
	wifiSetCmd.PersistentFlags().StringVarP(&tmpWifiSSID, "ssid", "s", "", "The SSID to use for an Access Point or to join as station")
	wifiSetCmd.PersistentFlags().StringVarP(&tmpWifiPSK, "psk", "k", "", "The Pre-Shared-Key to use for the Access Point (if empty, an OPEN AP is created) or for the network")

	wifiSetAPCmd.Flags().Uint8VarP(&tmpWifiStrChannel, "channel", "c", 1, "The WiFi channel to use for the Access Point")
	wifiSetAPCmd.Flags().BoolVarP(&tmpWifiHideSSID, "hide", "x", false, "Hide the SSID of the Access Point")
}
