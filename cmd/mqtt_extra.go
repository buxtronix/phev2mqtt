package cmd

import (
	"fmt"
	"strings"
)

// Extra status sensors (extra_sensors): values decoded by
// protocol.StatusTracker from registers that have no sensor of their own.
// The register layouts come from PHEV Link (see protocol/status.go and the
// README credits) and are best known for 2014-2017 cars. Off by default;
// with it off, the entities are removed from Home Assistant by publishing
// empty discovery configs.

// extraSensor is one Home Assistant entity for an extra status value.
type extraSensor struct {
	component string // sensor, binary_sensor or button
	key       string // object and unique id suffix
	name      string
	topic     string // state topic (command topic for a button), after the prefix
	fields    string // extra discovery fields, JSON object members
}

var extraSensors = []extraSensor{
	{"sensor", "power_switch", "Ignition", "/power/switch",
		`"device_class": "enum", "options": ["off", "acc", "on", "unknown"], "icon": "mdi:car-key"`},
	{"sensor", "charge_type", "Charge Type", "/charge/type",
		`"device_class": "enum", "options": ["none", "normal", "trickle", "quick", "unknown"], "icon": "mdi:ev-station"`},
	{"binary_sensor", "charge_timer_waiting", "Waiting For Car Charge Timer", "/charge/timer_waiting",
		`"icon": "mdi:timer-sand"`},
	{"binary_sensor", "charge_timer_failed", "Scheduled Charge Failed", "/charge/timer_failed",
		`"device_class": "problem"`},
	{"binary_sensor", "car_charge_timer", "Car Charge Timer", "/charge/timer",
		`"icon": "mdi:timer-outline", "json_attributes_topic": "~/charge/timer_schedule"`},
	{"binary_sensor", "car_climate_timer", "Car Climate Timer", "/climate/timer",
		`"icon": "mdi:fan-clock", "json_attributes_topic": "~/climate/timer_schedule"`},
	{"binary_sensor", "battery_heater", "Battery Heater", "/battery/heater",
		`"device_class": "heat"`},
	{"binary_sensor", "battery_too_cold", "Battery Too Cold", "/battery/too_cold",
		`"device_class": "problem", "icon": "mdi:snowflake-alert"`},
	{"binary_sensor", "ev_ecu_error", "EV-ECU Error", "/car/ev_ecu_error",
		`"device_class": "problem", "entity_category": "diagnostic"`},
	{"binary_sensor", "screen_in_use", "Car Screen In Use", "/car/screen_in_use",
		`"icon": "mdi:monitor-dashboard"`},
	{"binary_sensor", "registration_standby", "Registration Standby", "/car/registration_standby",
		`"icon": "mdi:cellphone-link", "entity_category": "diagnostic"`},
	{"sensor", "car_clock", "Car Clock", "/car/clock",
		`"device_class": "timestamp", "icon": "mdi:clock-outline", "entity_category": "diagnostic"`},
	{"sensor", "car_clock_offset", "Car Clock Offset", "/car/clock_offset",
		`"device_class": "duration", "unit_of_measurement": "s", "state_class": "measurement", "icon": "mdi:clock-alert-outline", "entity_category": "diagnostic"`},
}

// discovery returns the entity's discovery topic and config, in the same
// template form as publishHomeAssistantDiscovery: %s for the discovery
// prefix and the VIN in the topic, __VIN__, __DEVICE__ and __TOPIC__ in the
// config.
func (e extraSensor) discovery() (string, string) {
	topic := "%s/" + e.component + "/%s_" + e.key + "/config"
	topicKey := "state_topic"
	if e.component == "button" {
		topicKey = "command_topic"
	}
	var b strings.Builder
	fmt.Fprintf(&b, `{
		"name": %q,
		"%s": "~%s",
		%s,`, e.name, topicKey, e.topic, e.fields)
	if e.component == "binary_sensor" {
		b.WriteString(`
		"payload_on": "on",
		"payload_off": "off",`)
	}
	fmt.Fprintf(&b, `
		"availability": [{"topic": "~/gateway/available"}, {"topic": "~/available"}],
		"availability_mode": "all",
		"unique_id": "__VIN___%s",
		"dev": {
			"name": "__DEVICE__",
			"identifiers": ["phev-__VIN__"],
			"manufacturer": "Mitsubishi",
			"model": "Outlander PHEV"
		},
		"~": "__TOPIC__"}`, e.key)
	return topic, b.String()
}

// retiredExtraSensors were published by a test build and are removed from
// Home Assistant with empty discovery configs. The security alarm sensors
// went because the alarm is optional: without one, 0x23[1] always reads 0.
var retiredExtraSensors = []extraSensor{
	{component: "sensor", key: "alarm_state"},
	{component: "sensor", key: "alarm_last_event"},
}

// addDiscovery adds an optional feature's entities to a discovery template
// map: their configs when enabled, or empty ones that remove them.
func addDiscovery(data map[string]string, entities []extraSensor, enabled bool) {
	for _, e := range entities {
		topic, config := e.discovery()
		if !enabled {
			config = ""
		}
		data[topic] = config
	}
}

// addExtraSensorDiscovery adds the extra sensors to a discovery template
// map, and removes the retired ones.
func addExtraSensorDiscovery(data map[string]string, enabled bool) {
	addDiscovery(data, extraSensors, enabled)
	for _, e := range retiredExtraSensors {
		topic, _ := e.discovery()
		data[topic] = ""
	}
}

// publishExtraStatus publishes the extra status values a register
// notification determines. Only called from the car loop.
func (m *mqttClient) publishExtraStatus(register byte, data []byte) {
	if !m.extraSensors {
		return
	}
	for t, p := range m.status.Update(register, data) {
		m.publish(t, p)
	}
}
