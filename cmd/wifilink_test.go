package cmd

import (
	"errors"
	"strings"
	"testing"
)

const procWireless = `Inter-| sta-|   Quality        |   Discarded packets               | Missed | WE
 face | tus | link level noise |  nwid  crypt   frag  retry   misc | beacon | 22
 wlan1: 0000   70.  -40.  -256        0      0      0      0      0        0
 wlan0: 0000   44.  -66.  -256        0      0      0      2     32        0
`

func TestParseWireless(t *testing.T) {
	for _, tc := range []struct {
		data, iface string
		want        int
		ok          bool
	}{
		{procWireless, "wlan0", -66, true},
		{procWireless, "wlan1", -40, true},
		{procWireless, "wlan2", 0, false},
		{procWireless, "", 0, false},
		// Not associated: the driver reports no level.
		{" wlan0: 0000    0.    0.     0        0      0      0      0      0        0\n", "wlan0", 0, false},
		{"", "wlan0", 0, false},
	} {
		got, ok := parseWireless(tc.data, tc.iface)
		if got != tc.want || ok != tc.ok {
			t.Errorf("parseWireless(%q) = %d, %v; want %d, %v", tc.iface, got, ok, tc.want, tc.ok)
		}
	}
}

func TestConnectError(t *testing.T) {
	// What nmcli said when the car refused the password.
	wrongKey := "Passwords or encryption keys are required to access the wireless network 'REMOTE34fuvm'.\n" +
		"Warning: password for '802-11-wireless-security.psk' not given in 'passwd-file' and nmcli cannot ask without '--ask' option.\n" +
		"Error: Connection activation failed: Secrets were required, but not provided\n" +
		"Hint: use 'journalctl -xe NM_CONNECTION=9cddece2-7ad3-4613-908b-74d20e8461d2 + NM_DEVICE=wlan0' to get more details."
	for _, tc := range []struct {
		nmcli, want string
	}{
		{wrongKey, `the car refused the WiFi password for "REMOTE34fuvm"`},
		{"Error: No network with SSID 'REMOTE34fuvm' found.", `WiFi "REMOTE34fuvm" not found`},
		{"Error: Timeout expired (90 seconds)", `no connection to "REMOTE34fuvm" within`},
		{"Error: Connection activation failed: Not authorized to control networking.", "polkit"},
		{"Error: Connection activation failed: something else\nHint: more", "Connection activation failed: something else"},
	} {
		got := connectError("REMOTE34fuvm", errors.New(tc.nmcli)).Error()
		if !strings.Contains(got, tc.want) || strings.Contains(got, "\n") {
			t.Errorf("connectError(%q) = %q; want one line with %q", tc.nmcli, got, tc.want)
		}
	}
}

func TestPublishWifi(t *testing.T) {
	m, b := newTestClient("phev")
	m.publishWifi("wlan0")
	if len(b.published) != 0 {
		t.Errorf("published %v before the WiFi state was known", b.published)
	}
	m.state.setWifiUp(false)
	m.publishWifi("wlan0")
	if got := b.published["phev/wifi/state"]; got != "disconnected" {
		t.Errorf("phev/wifi/state = %q, want disconnected", got)
	}
	if _, ok := b.published["phev/wifi/signal"]; ok {
		t.Error("published a signal while disconnected")
	}
	m.state.setWifiUp(true)
	m.publishWifi("wlan0")
	if got := b.published["phev/wifi/state"]; got != "connected" {
		t.Errorf("phev/wifi/state = %q, want connected", got)
	}
	if got := m.state.value("/wifi/state"); got != "connected" {
		t.Errorf("web interface value = %q, want connected", got)
	}
}

func TestDiscoveryConnectionEntities(t *testing.T) {
	m, b := newTestClient("phev")
	m.publishHomeAssistantDiscovery("VIN1", "phev")
	for _, topic := range []string{
		"homeassistant/binary_sensor/VIN1_connection/config",
		"homeassistant/binary_sensor/VIN1_wifi/config",
		"homeassistant/sensor/VIN1_wifi_signal/config",
		"homeassistant/sensor/VIN1_last_connected/config",
		"homeassistant/sensor/VIN1_last_disconnected/config",
	} {
		d, ok := b.published[topic]
		if !ok {
			t.Errorf("%s not published", topic)
			continue
		}
		if strings.Contains(d, "__") {
			t.Errorf("%s has an unreplaced placeholder: %s", topic, d)
		}
	}
	// The connection sensor must show the car being away, so it is only
	// unavailable along with the gateway, not with the car.
	for _, e := range []string{"binary_sensor/VIN1_connection", "binary_sensor/VIN1_wifi", "binary_sensor/VIN1_gateway"} {
		d := b.published["homeassistant/"+e+"/config"]
		if strings.Contains(d, `"~/available"}`) || strings.Contains(d, `"avty_t": "~/available"`) {
			t.Errorf("%s turns unavailable with the car: %s", e, d)
		}
	}
}
