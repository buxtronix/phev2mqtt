package cmd

import (
	"time"

	log "github.com/sirupsen/logrus"
)

// phev/available tells whether the car is there: online once it is connected
// and has sent data, offline when its WiFi goes down, when the connection has
// been gone for disconnectGrace, or when the car sent nothing for a whole
// update_interval (staleCar). phev/gateway/available is the gateway itself,
// online on every broker connection and offline through the MQTT will.
//
// Everything here runs on events (data, WiFi, disconnect, the update tick),
// without a timer of its own apart from the one-off disconnect grace.

// disconnectGrace absorbs a quick reconnect, which the car does now and then,
// without Home Assistant seeing the car go away. A variable for tests.
var disconnectGrace = 30 * time.Second

const (
	availableTopic        = "/available"
	gatewayAvailableTopic = "/gateway/available"
)

// setAvailable publishes phev/available when it changes, and logs why.
func (m *mqttClient) setAvailable(available bool, reason string) {
	if !m.state.setAvailable(available) {
		return
	}
	payload := "offline"
	if available {
		payload = "online"
	}
	log.Infof("Car %s: %s", payload, reason)
	m.mqttPublish(m.topic(availableTopic), 0, true, payload)
}

// updateAvailable brings phev/available in line with the current state.
func (m *mqttClient) updateAvailable(reason string) {
	m.setAvailable(m.state.carAvailable(), reason)
}

// carData records data from the car; the first on a connection makes the
// car available.
func (m *mqttClient) carData() {
	m.state.gotCarData()
	m.updateAvailable("data from the car")
}

// carDisconnected makes the car unavailable unless it is back with data
// within disconnectGrace.
func (m *mqttClient) carDisconnected() {
	grace := disconnectGrace
	time.AfterFunc(grace, func() {
		m.updateAvailable("no connection with data for " + grace.String())
	})
}

// staleCar reports whether the car sent nothing for a whole update interval,
// despite the update request on the previous tick: connected but asleep or
// gone. Checked on that tick only.
func (m *mqttClient) staleCar() bool {
	return m.state.sinceCarData() > m.updateInterval
}

// wifiChanged follows the car's WiFi: when it goes down, the car is gone at
// once, and its connection is closed instead of waiting for it to time out.
func (m *mqttClient) wifiChanged(up bool) {
	if !up {
		if phev := m.state.car(); phev != nil {
			phev.Close()
		}
	}
	reason := "WiFi up"
	if !up {
		reason = "WiFi down"
	}
	m.updateAvailable(reason)
}

// availabilityPayload is phev/available as it currently stands.
func (m *mqttClient) availabilityPayload() string {
	if m.state.isAvailable() {
		return "online"
	}
	return "offline"
}
