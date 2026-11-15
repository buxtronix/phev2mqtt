package cmd

import (
	"sync/atomic"

	log "github.com/sirupsen/logrus"
)

// execSelf replaces the process with a new start of the binary; a variable
// so that tests do not.
var execSelf = execBinary

var restarting atomic.Bool

// restart starts phev2mqtt afresh, from MQTT (/restart) or the web
// interface. The car accepts one client at a time, so its connection is
// closed first; the broker hears offline, as the will only fires when the
// connection breaks.
func (m *mqttClient) restart() {
	if !restarting.CompareAndSwap(false, true) {
		return
	}
	defer restarting.Store(false)
	log.Warn("Restarting phev2mqtt")
	m.state.setEnabled(false)
	if phev := m.state.car(); phev != nil {
		phev.Close()
	}
	// QoS 1 waits (up to 5s) until the broker has it.
	m.setAvailable(false, "restarting")
	m.mqttPublish(m.topic(availableTopic), 1, true, "offline")
	m.mqttPublish(m.topic(gatewayAvailableTopic), 1, true, "offline")
	if c := m.mqtt(); c != nil {
		c.Disconnect(250)
	}

	err := execSelf()
	// Only reached when the new start failed: carry on as before.
	log.Errorf("Restart failed: %v", err)
	m.state.setEnabled(true)
	m.connectMQTT()
}
