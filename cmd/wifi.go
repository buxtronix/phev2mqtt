package cmd

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
)

// The NetworkManager profile the web interface manages for the car's WiFi.
const carConnectionName = "phev2mqtt-car"

// wifiManager drives NetworkManager through nmcli. As the phev2mqtt service
// user that needs the polkit rule from the package
// (/usr/share/polkit-1/rules.d/50-phev2mqtt.rules).
type wifiManager struct {
	iface string
	// One nmcli change at a time; scans and status may run alongside.
	mu sync.Mutex
}

type wifiStatus struct {
	Available  bool   `json:"available"` // nmcli works at all
	Error      string `json:"error,omitempty"`
	Interface  string `json:"interface"`
	State      string `json:"state,omitempty"`
	Connection string `json:"connection,omitempty"`
	Address    string `json:"address,omitempty"`
	// The car profile, if there is one.
	CarSSID       string `json:"car_ssid,omitempty"`
	CarClonedMAC  string `json:"car_cloned_mac,omitempty"`
	CarConfigured bool   `json:"car_configured"`
}

type wifiNetwork struct {
	SSID     string `json:"ssid"`
	Signal   int    `json:"signal"`
	Security string `json:"security"`
	Active   bool   `json:"active"`
}

var nmcliTimeout = 60 * time.Second

func nmcli(args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), nmcliTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "nmcli", args...).CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return "", errors.New(msg)
	}
	return string(out), nil
}

// splitTerse splits one line of `nmcli -t` output, where a literal ':' in a
// value is escaped as '\:' and '\' as '\\'.
func splitTerse(line string) []string {
	var fields []string
	var b strings.Builder
	for i := 0; i < len(line); i++ {
		switch c := line[i]; {
		case c == '\\' && i+1 < len(line):
			i++
			b.WriteByte(line[i])
		case c == ':':
			fields = append(fields, b.String())
			b.Reset()
		default:
			b.WriteByte(c)
		}
	}
	return append(fields, b.String())
}

// terseValues parses `nmcli -t -f a,b,c <object> show` (lines "key:value").
func terseValues(out string) map[string]string {
	values := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if f := splitTerse(line); len(f) >= 2 {
			values[f[0]] = strings.Join(f[1:], ":")
		}
	}
	return values
}

func (w *wifiManager) status() wifiStatus {
	st := wifiStatus{Interface: w.iface}
	out, err := nmcli("-t", "-f", "GENERAL.STATE,GENERAL.CONNECTION,IP4.ADDRESS", "device", "show", w.iface)
	if err != nil {
		st.Error = err.Error()
		return st
	}
	st.Available = true
	v := terseValues(out)
	st.State = v["GENERAL.STATE"]
	st.Connection = v["GENERAL.CONNECTION"]
	st.Address = v["IP4.ADDRESS[1]"]
	if out, err := nmcli("-t", "-f", "802-11-wireless.ssid,802-11-wireless.cloned-mac-address", "connection", "show", carConnectionName); err == nil {
		v := terseValues(out)
		st.CarConfigured = true
		st.CarSSID = v["802-11-wireless.ssid"]
		st.CarClonedMAC = v["802-11-wireless.cloned-mac-address"]
	}
	return st
}

func (w *wifiManager) scan() ([]wifiNetwork, error) {
	out, err := nmcli("-t", "-f", "IN-USE,SSID,SIGNAL,SECURITY", "device", "wifi", "list", "ifname", w.iface, "--rescan", "yes")
	if err != nil {
		return nil, err
	}
	return parseWifiList(out), nil
}

func parseWifiList(out string) []wifiNetwork {
	best := map[string]int{}
	var networks []wifiNetwork
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		f := splitTerse(line)
		if len(f) != 4 || f[1] == "" {
			continue // Hidden network.
		}
		signal, _ := strconv.Atoi(f[2])
		n := wifiNetwork{SSID: f[1], Signal: signal, Security: f[3], Active: f[0] == "*"}
		// One entry per SSID: the strongest access point.
		if i, ok := best[n.SSID]; ok {
			if n.Signal > networks[i].Signal || n.Active {
				n.Active = n.Active || networks[i].Active
				networks[i] = n
			}
			continue
		}
		best[n.SSID] = len(networks)
		networks = append(networks, n)
	}
	return networks
}

var macAddress = regexp.MustCompile(`^([0-9A-Fa-f]{2}:){5}[0-9A-Fa-f]{2}$`)

// connectCar creates or updates the car profile and brings it up. The
// addressing matches the car's own network: it serves 192.168.8.46 and a
// client must not get the default route through it. An empty psk keeps the
// stored one; an empty mac uses the interface's own address.
func (w *wifiManager) connectCar(ssid, psk, mac string) error {
	switch {
	case ssid == "" || len(ssid) > 32:
		return errors.New("the SSID must be 1 to 32 characters")
	case psk != "" && (len(psk) < 8 || len(psk) > 63):
		return errors.New("a WPA password is 8 to 63 characters")
	case mac != "" && !macAddress.MatchString(mac):
		return fmt.Errorf("not a MAC address: %q", mac)
	}
	w.mu.Lock()
	defer w.mu.Unlock()

	clonedMAC := mac
	if clonedMAC == "" {
		clonedMAC = "preserve"
	}
	settings := []string{
		"connection.interface-name", w.iface,
		"connection.autoconnect", "yes",
		"connection.autoconnect-priority", "10",
		"802-11-wireless.ssid", ssid,
		"802-11-wireless.cloned-mac-address", clonedMAC,
		"wifi-sec.key-mgmt", "wpa-psk",
		"ipv4.method", "manual",
		"ipv4.addresses", "192.168.8.47/24",
		"ipv4.never-default", "yes",
		"ipv4.ignore-auto-dns", "yes",
		"ipv4.route-metric", "40",
		"ipv6.method", "disabled",
	}
	// The password is briefly visible in the process list of this machine;
	// nmcli has no other way to set it non-interactively.
	if psk != "" {
		settings = append(settings, "wifi-sec.psk", psk)
	}
	if _, err := nmcli("connection", "show", carConnectionName); err == nil {
		if _, err := nmcli(append([]string{"connection", "modify", carConnectionName}, settings...)...); err != nil {
			return err
		}
	} else {
		if psk == "" {
			return errors.New("a password is needed for a new connection")
		}
		args := append([]string{"connection", "add", "type", "wifi", "con-name", carConnectionName, "ifname", w.iface, "ssid", ssid}, settings...)
		if _, err := nmcli(args...); err != nil {
			return err
		}
	}
	if _, err := nmcli("connection", "up", carConnectionName); err != nil {
		log.Debugf("nmcli connection up: %v", err)
		return connectError(ssid, err)
	}
	return nil
}

// connectError turns what nmcli says about a failed connection into one
// line that says what to do. NetworkManager reports a password the car
// refused as missing secrets, since it then asks for a new one.
func connectError(ssid string, err error) error {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "Secrets were required"):
		return fmt.Errorf("the car refused the WiFi password for %q; check it, letters like l, I and 1 or 0 and O look alike", ssid)
	case strings.Contains(msg, "No network with SSID"):
		return fmt.Errorf("WiFi %q not found; is the car within range and its WiFi on?", ssid)
	case strings.Contains(msg, "Timeout") || strings.Contains(msg, "timed out") || strings.Contains(msg, "deadline exceeded"):
		return fmt.Errorf("no connection to %q within %v; the signal may be too weak", ssid, nmcliTimeout)
	case strings.Contains(msg, "Not authorized") || strings.Contains(msg, "Insufficient privileges"):
		return errors.New("not allowed to manage the WiFi; is the polkit rule of the package installed?")
	}
	// Otherwise the first line, without nmcli's "Error: " and its hints.
	line, _, _ := strings.Cut(msg, "\n")
	return errors.New(strings.TrimPrefix(line, "Error: "))
}

// carReconnectInterval is how long the car's WiFi must be down before
// reconnectCar steps in, and how often it tries after that.
const carReconnectInterval = 2 * time.Minute

// reconnectCar brings the car profile up when the car is in range but
// NetworkManager is not connecting to it. NetworkManager gives up on a
// profile for good once an activation fails for lack of secrets, and that is
// what the car driving off during the WPA handshake looks like: it then needs
// a manual `nmcli connection up` even with the car back in range. Only
// NetworkManager's own scan results are used, so this scans nothing itself.
// It reports whether it tried.
func (w *wifiManager) reconnectCar() (bool, error) {
	if !w.mu.TryLock() {
		return false, nil // the web interface is changing the WiFi
	}
	defer w.mu.Unlock()
	out, err := nmcli("-t", "-f", "GENERAL.STATE", "device", "show", w.iface)
	if err != nil {
		return false, err
	}
	if !deviceIdle(terseValues(out)["GENERAL.STATE"]) {
		return false, nil
	}
	out, err = nmcli("-t", "-f", "802-11-wireless.ssid", "connection", "show", carConnectionName)
	if err != nil {
		return false, nil // no car profile, the WiFi is set up some other way
	}
	ssid := terseValues(out)["802-11-wireless.ssid"]
	out, err = nmcli("-t", "-f", "SSID", "device", "wifi", "list", "ifname", w.iface, "--rescan", "no")
	if err != nil {
		return false, err
	}
	if !ssidListed(out, ssid) {
		return false, nil
	}
	log.Infof("WiFi %q in range but %s is not connecting; bringing up %s", ssid, w.iface, carConnectionName)
	if _, err := nmcli("connection", "up", carConnectionName); err != nil {
		return true, connectError(ssid, err)
	}
	return true, nil
}

// deviceIdle reports whether a GENERAL.STATE from nmcli, e.g.
// "30 (disconnected)", is a device that could connect but is not trying to:
// not while it connects, nor when it is unavailable or unmanaged.
func deviceIdle(state string) bool {
	code, _, _ := strings.Cut(state, " ")
	return code == "30"
}

// ssidListed reports whether ssid is in `nmcli -t -f SSID device wifi list`.
func ssidListed(out, ssid string) bool {
	if ssid == "" {
		return false
	}
	for _, line := range strings.Split(out, "\n") {
		if f := splitTerse(strings.TrimRight(line, "\r")); len(f) == 1 && f[0] == ssid {
			return true
		}
	}
	return false
}
