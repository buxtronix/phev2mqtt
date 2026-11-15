package cmd

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
)

var errNoNetlink = errors.New("WiFi status needs Linux")

// wifiSignal is the signal level of iface in dBm, as the driver measured it
// from what the car sends anyway: reading it transmits nothing.
func wifiSignal(iface string) (int, bool) {
	data, err := os.ReadFile("/proc/net/wireless")
	if err != nil {
		return 0, false
	}
	return parseWireless(string(data), iface)
}

// parseWireless finds the signal level of iface in /proc/net/wireless:
//
//	Inter-| sta-|   Quality        |   Discarded packets ...
//	 face | tus | link level noise |  nwid  crypt ...
//	 wlan0: 0000   44.  -66.  -256        0      0 ...
func parseWireless(data, iface string) (int, bool) {
	for _, line := range strings.Split(data, "\n") {
		name, rest, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok || name != iface {
			continue
		}
		f := strings.Fields(rest)
		if len(f) < 3 {
			return 0, false
		}
		level, err := strconv.Atoi(strings.TrimSuffix(f[2], "."))
		// 0 means the driver has no value; real levels are negative.
		if err != nil || level >= 0 {
			return 0, false
		}
		return level, true
	}
	return 0, false
}

// watchWifi publishes the state of the car's WiFi interface on every change.
// The signal goes along, and again with each status update from the car
// (publishWifiSignal), so there is no timer of its own.
func (m *mqttClient) watchWifi(iface string) {
	for {
		err := watchLink(iface, func(up bool) {
			if up {
				log.Infof("WiFi %s connected", iface)
			} else {
				log.Infof("WiFi %s disconnected", iface)
			}
			m.state.setWifiUp(up)
			m.publishWifi(iface)
			m.wifiChanged(up)
		})
		log.Warnf("Not watching WiFi %s: %v", iface, err)
		if errors.Is(err, errNoNetlink) {
			return
		}
		// Only after an error; watchLink itself waits for the kernel.
		time.Sleep(time.Minute)
	}
}

// publishWifi publishes the WiFi state and, when connected, the signal.
func (m *mqttClient) publishWifi(iface string) {
	up, known := m.state.wifiUp()
	if !known {
		return
	}
	state := "disconnected"
	if up {
		state = "connected"
	}
	m.publishRetained("/wifi/state", state)
	m.publishWifiSignal(iface)
}

func (m *mqttClient) publishWifiSignal(iface string) {
	if up, _ := m.state.wifiUp(); !up {
		return
	}
	if dbm, ok := wifiSignal(iface); ok {
		m.publishRetained("/wifi/signal", strconv.Itoa(dbm))
	}
}

// publishRetained is publish, but the broker keeps the value for whoever
// subscribes later, e.g. Home Assistant after a restart.
func (m *mqttClient) publishRetained(topic, payload string) {
	m.mqttPublish(m.topic(topic), 0, true, payload)
	m.state.setValue(topic, payload)
}
