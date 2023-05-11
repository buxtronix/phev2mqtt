/*
Copyright © 2021 Ben Buxton <bbuxton@gmail.com>

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
GNU General Public License for more details.

You should have received a copy of the GNU General Public License
along with this program. If not, see <http://www.gnu.org/licenses/>.
*/
package cmd

import (
	"encoding/hex"
	"fmt"
	"github.com/buxtronix/phev2mqtt/client"
	"github.com/buxtronix/phev2mqtt/protocol"
	"github.com/spf13/cobra"
	"os/exec"
	"strings"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	log "github.com/sirupsen/logrus"
)

const defaultWifiRestartCmd = "sudo ip link set wlan0 down && sleep 3 && sudo ip link set wlan0 up"

// mqttCmd represents the mqtt command
var mqttCmd = &cobra.Command{
	Use:   "mqtt",
	Short: "Start an MQTT bridge.",
	Long: `Maintains a connected to the Phev (retry as needed) and also to an MQTT server.

Status data from the car is passed to the MQTT topics, and also some commands from MQTT
are sent to control certain aspects of the car. See the phev2mqtt Github page for
more details on the topics.
`,
	RunE: func(cmd *cobra.Command, args []string) error {
		mc := &mqttClient{climate: new(climate)}
		return mc.Run(cmd, args)
	},
}

// Tracks complete climate state as on and mode are separately
// sent by the car.
type climate struct {
	state *bool
	mode  *string
}

func (c *climate) setMode(m string) {
	c.mode = &m
}
func (c *climate) setState(state bool) {
	c.state = &state
}

func (c *climate) mqttStates() map[string]string {
	m := map[string]string{
		"/climate/cool":       "off",
		"/climate/heat":       "off",
		"/climate/windscreen": "off",
		"/climate/mode":       "off",
	}
	if !c.ready() || !*c.state {
		return m
	}
	m["/climate/mode"] = *c.mode
	switch *c.mode {
	case "cool":
		m["/climate/cool"] = "on"
	case "heat":
		m["/climate/heat"] = "on"
	case "windscreen":
		m["/climate/windscreen"] = "on"
	}
	return m
}

func (c *climate) ready() bool {
	return c.mode != nil && c.state != nil
}

var lastWifiRestart time.Time

func restartWifi(cmd *cobra.Command) error {
	restartRetryTime, err := cmd.Flags().GetDuration("wifi_restart_retry_time")
	if err != nil {
		return err
	}
	if time.Now().Sub(lastWifiRestart) < restartRetryTime {
		return nil
	}
	defer func() {
		lastWifiRestart = time.Now()
	}()

	restartCommand, _ := cmd.Flags().GetString("wifi_restart_command")
	if restartCommand == "" {
		log.Debugf("wifi restart disabled")
		return nil
	}

	log.Infof("Attempting to restart wifi")

	restartCmd := exec.Command("sh", "-c", restartCommand)

	stdoutStderr, err := restartCmd.CombinedOutput()
	log.Infof("Output from wifi restart: %s", stdoutStderr)
	return err
}

type mqttClient struct {
	client         mqtt.Client
	options        *mqtt.ClientOptions
	mqttData       map[string]string
	updateInterval time.Duration

	phev        *client.Client
	lastConnect time.Time

	prefix string

	haDiscovery       bool
	haDiscoveryPrefix string

	climate *climate
	enabled bool
}

func (m *mqttClient) topic(topic string) string {
	return fmt.Sprintf("%s%s", m.prefix, topic)
}

func (m *mqttClient) Run(cmd *cobra.Command, args []string) error {
	var err error

	m.enabled = true  // Default.
	mqttServer, _ := cmd.Flags().GetString("mqtt_server")
	mqttUsername, _ := cmd.Flags().GetString("mqtt_username")
	mqttPassword, _ := cmd.Flags().GetString("mqtt_password")
	m.prefix, _ = cmd.Flags().GetString("mqtt_topic_prefix")
	m.haDiscovery, _ = cmd.Flags().GetBool("ha_discovery")
	m.haDiscoveryPrefix, _ = cmd.Flags().GetString("ha_discovery_prefix")

	m.updateInterval, err = cmd.Flags().GetDuration("update_interval")
	if err != nil {
		return err
	}
	wifiRestartTime, err := cmd.Flags().GetDuration("wifi_restart_time")
	if err != nil {
		return err
	}

	m.options = mqtt.NewClientOptions().
		AddBroker(mqttServer).
		SetClientID("phev2mqtt").
		SetUsername(mqttUsername).
		SetPassword(mqttPassword).
		SetAutoReconnect(true).
		SetDefaultPublishHandler(m.handleIncomingMqtt).
		SetWill(m.topic("/available"), "offline", 0, true)

	m.client = mqtt.NewClient(m.options)
	if token := m.client.Connect(); token.Wait() && token.Error() != nil {
		return token.Error()
	}

	if token := m.client.Subscribe(m.topic("/set/#"), 0, nil); token.Wait() && token.Error() != nil {
		return token.Error()
	}
	if token := m.client.Subscribe(m.topic("/connection"), 0, nil); token.Wait() && token.Error() != nil {
		return token.Error()
	}

	m.mqttData = map[string]string{}

	for {
		if m.enabled {
			if err := m.handlePhev(cmd); err != nil {
				log.Error(err)
			}
			// Publish as offline if last connection was >30s ago.
			if time.Now().Sub(m.lastConnect) > 30*time.Second {
				m.client.Publish(m.topic("/available"), 0, true, "offline")
			}
			// Restart Wifi interface if > wifi_restart_time.
			if wifiRestartTime > 0 && time.Now().Sub(m.lastConnect) > wifiRestartTime {
				if err := restartWifi(cmd); err != nil {
					log.Errorf("Error restarting wifi: %v", err)
				}
			}
		}

		time.Sleep(time.Second)
	}
}

func (m *mqttClient) publish(topic, payload string) {
	if cache := m.mqttData[topic]; cache != payload {
		m.client.Publish(m.topic(topic), 0, false, payload)
		m.mqttData[topic] = payload
	}
}

func (m *mqttClient) handleIncomingMqtt(client mqtt.Client, msg mqtt.Message) {
	log.Infof("Topic: [%s] Payload: [%s]", msg.Topic(), msg.Payload())

	topicParts := strings.Split(msg.Topic(), "/")
	if strings.HasPrefix(msg.Topic(), m.topic("/set/register/")) {
		if len(topicParts) != 4 {
			log.Infof("Bad topic format [%s]", msg.Topic())
			return
		}
		register, err := hex.DecodeString(topicParts[3])
		if err != nil {
			log.Infof("Bad register in topic [%s]: %v", msg.Topic(), err)
			return
		}
		data, err := hex.DecodeString(string(msg.Payload()))
		if err != nil {
			log.Infof("Bad payload [%s]: %v", msg.Payload(), err)
			return
		}
		if err := m.phev.SetRegister(register[0], data); err != nil {
			log.Infof("Error setting register %02x: %v", register[0], err)
			return
		}
	} else if msg.Topic() == m.topic("/connection") {
		payload := strings.ToLower(string(msg.Payload()))
		switch payload {
		case "off":
			m.enabled = false
			m.phev.Close()
			m.client.Publish(m.topic("/available"), 0, true, "offline")
		case "on":
			m.enabled = true
		case "restart":
			m.enabled = true
			m.client.Publish(m.topic("/available"), 0, true, "offline")
			m.phev.Close()
		}
	} else if msg.Topic() == m.topic("/set/parkinglights") {
		values := map[string]byte{"on": 0x1, "off": 0x2}
		if v, ok := values[strings.ToLower(string(msg.Payload()))]; ok {
			if err := m.phev.SetRegister(0xb, []byte{v}); err != nil {
				log.Infof("Error setting register 0xb: %v", err)
				return
			}
		}
	} else if msg.Topic() == m.topic("/set/headlights") {
		values := map[string]byte{"on": 0x1, "off": 0x2}
		if v, ok := values[strings.ToLower(string(msg.Payload()))]; ok {
			if err := m.phev.SetRegister(0xa, []byte{v}); err != nil {
				log.Infof("Error setting register 0xb: %v", err)
				return
			}
		}
	} else if msg.Topic() == m.topic("/set/cancelchargetimer") {
		if err := m.phev.SetRegister(0x17, []byte{0x1}); err != nil {
			log.Infof("Error setting register 0x17: %v", err)
			return
		}
		if err := m.phev.SetRegister(0x17, []byte{0x11}); err != nil {
			log.Infof("Error setting register 0x17: %v", err)
			return
		}
	} else if strings.HasPrefix(msg.Topic(), m.topic("/set/climate/")) {
		topic := msg.Topic()
		payload := strings.ToLower(string(msg.Payload()))

		modeMap := map[string]byte{"off": 0x0, "OFF": 0x0, "cool": 0x1, "heat": 0x2, "windscreen": 0x3, "mode": 0x4}
		durMap := map[string]byte{"10": 0x0, "20": 0x10, "30": 0x20, "on": 0x0, "off": 0x0}
		parts := strings.Split(topic, "/")
		state := byte(0x02) // initial.
		mode, ok := modeMap[parts[len(parts)-1]]
		if !ok {
			log.Errorf("Unknown climate mode: %s", parts[len(parts)-1])
			return
		}
		if mode == 0x4 { // set/climate/mode -> "heat"
			mode = modeMap[payload]
			payload = "on"
		}
		if payload == "off" {
			mode = 0x0
		}
		duration, ok := durMap[payload]
		if mode != 0x0 && !ok {
			log.Errorf("Unknown climate duration: %s", payload)
			return
		}
		if mode == 0x0 {
			state = 0x1
		}
		if err := m.phev.SetRegister(0x1b, []byte{state, mode, duration, 0x0}); err != nil {
			log.Infof("Error setting register 0x1b: %v", err)
			return
		}
	} else {
		log.Errorf("Unknown topic from mqtt: %s", msg.Topic())
	}
}

func (m *mqttClient) handlePhev(cmd *cobra.Command) error {
	var err error
	address, _ := cmd.Flags().GetString("address")
	m.phev, err = client.New(client.AddressOption(address))
	if err != nil {
		return err
	}

	if err := m.phev.Connect(); err != nil {
		return err
	}

	if err := m.phev.Start(); err != nil {
		return err
	}
	m.client.Publish(m.topic("/available"), 0, true, "online")
	defer func() {
		m.lastConnect = time.Now()
	}()

	var encodingErrorCount = 0
	var lastEncodingError time.Time

	updaterTicker := time.NewTicker(m.updateInterval)
	for {
		select {
		case <-updaterTicker.C:
			m.phev.SetRegister(0x6, []byte{0x3})
		case msg, ok := <-m.phev.Recv:
			if !ok {
				log.Infof("Connection closed.")
				updaterTicker.Stop()
				return fmt.Errorf("Connection closed.")
			}
			switch msg.Type {
			case protocol.CmdInBadEncoding:
				if time.Now().Sub(lastEncodingError) > 15*time.Second {
					encodingErrorCount = 0
				}
				if encodingErrorCount > 50 {
					m.phev.Close()
					updaterTicker.Stop()
					return fmt.Errorf("Disconnecting due to too many errors")
				}
				encodingErrorCount += 1
				lastEncodingError = time.Now()
			case protocol.CmdInResp:
				if msg.Ack != protocol.Request {
					break
				}
				m.publishRegister(msg)
				m.phev.Send <- &protocol.PhevMessage{
					Type:     protocol.CmdOutSend,
					Register: msg.Register,
					Ack:      protocol.Ack,
					Xor:      msg.Xor,
					Data:     []byte{0x0},
				}
			}
		}
	}
}

var boolOnOff = map[bool]string{
	false: "off",
	true:  "on",
}
var boolOpen = map[bool]string{
	false: "closed",
	true:  "open",
}

func (m *mqttClient) publishRegister(msg *protocol.PhevMessage) {
	dataStr := hex.EncodeToString(msg.Data)
	m.publish(fmt.Sprintf("/register/%02x", msg.Register), dataStr)
	switch reg := msg.Reg.(type) {
	case *protocol.RegisterVIN:
		m.publish("/vin", reg.VIN)
		m.publishHomeAssistantDiscovery(reg.VIN, m.prefix, "Phev")
		m.publish("/registrations", fmt.Sprintf("%d", reg.Registrations))
	case *protocol.RegisterECUVersion:
		m.publish("/ecuversion", reg.Version)
	case *protocol.RegisterACMode:
		m.climate.setMode(reg.Mode)
		for t, p := range m.climate.mqttStates() {
			m.publish(t, p)
		}
	case *protocol.RegisterACOperStatus:
		m.climate.setState(reg.Operating)
		for t, p := range m.climate.mqttStates() {
			m.publish(t, p)
		}
	case *protocol.RegisterChargeStatus:
		m.publish("/charge/charging", boolOnOff[reg.Charging])
		m.publish("/charge/remaining", fmt.Sprintf("%d", reg.Remaining))
	case *protocol.RegisterDoorStatus:
		m.publish("/door/locked", boolOpen[!reg.Locked])
		m.publish("/door/rear_left", boolOpen[reg.RearLeft])
		m.publish("/door/rear_right", boolOpen[reg.RearRight])
		m.publish("/door/front_right", boolOpen[reg.Driver])
		m.publish("/door/driver", boolOpen[reg.Driver])
		m.publish("/door/front_left", boolOpen[reg.FrontPassenger])
		m.publish("/door/front_passenger", boolOpen[reg.FrontPassenger])
		m.publish("/door/bonnet", boolOpen[reg.Bonnet])
		m.publish("/door/boot", boolOpen[reg.Boot])
		m.publish("/lights/head", boolOnOff[reg.Headlights])
	case *protocol.RegisterBatteryLevel:
		m.publish("/battery/level", fmt.Sprintf("%d", reg.Level))
		m.publish("/lights/parking", boolOnOff[reg.ParkingLights])
	case *protocol.RegisterChargePlug:
		if reg.Connected {
			m.publish("/charge/plug", "connected")
		} else {
			m.publish("/charge/plug", "unplugged")
		}
	}
}

// Publish home assistant discovery message.
// Uses the vehicle VIN, so sent after VIN discovery.
var publishedDiscovery = false

func (m *mqttClient) publishHomeAssistantDiscovery(vin, topic, name string) {

	if publishedDiscovery || !m.haDiscovery {
		return
	}
	publishedDiscovery = true
	discoveryData := map[string]string{
		// Doors.
		"%s/binary_sensor/%s_door_locked/config": `{
		"device_class": "lock",
		"name": "__NAME__ Locked",
		"state_topic": "~/door/locked",
		"payload_off": "closed",
		"payload_on": "open",
		"avty_t": "~/available",
		"unique_id": "__VIN___door_locked",
		"device": {
			"name": "PHEV __VIN__",
			"identifiers": ["phev-__VIN__"],
			"manufacturer": "Mitsubishi",
			"model": "Outlander PHEV"},
		"~": "__TOPIC__"}`,
		"%s/binary_sensor/%s_door_bonnet/config": `{
		"device_class": "door",
		"name": "__NAME__ Bonnet",
		"state_topic": "~/door/bonnet",
		"payload_off": "closed",
		"payload_on": "open",
		"avty_t": "~/available",
		"unique_id": "__VIN___door_bonnet",
		"device": {
			"name": "PHEV __VIN__",
			"identifiers": ["phev-__VIN__"],
			"manufacturer": "Mitsubishi",
			"model": "Outlander PHEV"},
		"~": "__TOPIC__"}`,
		"%s/binary_sensor/%s_door_boot/config": `{
		"device_class": "door",
		"name": "__NAME__ Boot",
		"state_topic": "~/door/boot",
		"payload_off": "closed",
		"payload_on": "open",
		"avty_t": "~/available",
		"unique_id": "__VIN___door_boot",
		"dev": {
			"name": "PHEV __VIN__",
			"identifiers": ["phev-__VIN__"],
			"manufacturer": "Mitsubishi",
			"model": "Outlander PHEV"
		},
		"~": "__TOPIC__"}`,
		"%s/binary_sensor/%s_door_front_passenger/config": `{
		"device_class": "door",
		"name": "__NAME__ Front Passenger Door",
		"state_topic": "~/door/front_passenger",
		"payload_off": "closed",
		"payload_on": "open",
		"avty_t": "~/available",
		"unique_id": "__VIN___door_front_passenger",
		"dev": {
			"name": "PHEV __VIN__",
			"identifiers": ["phev-__VIN__"],
			"manufacturer": "Mitsubishi",
			"model": "Outlander PHEV"
		},
		"~": "__TOPIC__"}`,
		"%s/binary_sensor/%s_door_driver/config": `{
		"device_class": "door",
		"name": "__NAME__ Driver Door",
		"state_topic": "~/door/driver",
		"payload_off": "closed",
		"payload_on": "open",
		"avty_t": "~/available",
		"unique_id": "__VIN___door_driver",
		"dev": {
			"name": "PHEV __VIN__",
			"identifiers": ["phev-__VIN__"],
			"manufacturer": "Mitsubishi",
			"model": "Outlander PHEV"
		},
		"~": "__TOPIC__"}`,
		"%s/binary_sensor/%s_door_rear_left/config": `{
		"device_class": "door",
		"name": "__NAME__ Rear Left Door",
		"state_topic": "~/door/rear_left",
		"payload_off": "closed",
		"payload_on": "open",
		"avty_t": "~/available",
		"unique_id": "__VIN___door_rear_left",
		"dev": {
			"name": "PHEV __VIN__",
			"identifiers": ["phev-__VIN__"],
			"manufacturer": "Mitsubishi",
			"model": "Outlander PHEV"
		},
		"~": "__TOPIC__"}`,
		"%s/binary_sensor/%s_door_rear_right/config": `{
		"device_class": "door",
		"name": "__NAME__ Rear Right Door",
		"state_topic": "~/door/rear_right",
		"payload_off": "closed",
		"payload_on": "open",
		"avty_t": "~/available",
		"unique_id": "__VIN___door_rear_right",
		"dev": {
			"name": "PHEV __VIN__",
			"identifiers": ["phev-__VIN__"],
			"manufacturer": "Mitsubishi",
			"model": "Outlander PHEV"
		},
		"~": "__TOPIC__"}`,

		// Battery and charging
		"%s/sensor/%s_battery_level/config": `{
		"device_class": "battery",
		"name": "__NAME__ Battery",
		"state_topic": "~/battery/level",
		"state_class": "measurement",
		"unit_of_measurement": "%",
		"avty_t": "~/available",
		"unique_id": "__VIN___battery_level",
		"dev": {
			"name": "PHEV __VIN__",
			"identifiers": ["phev-__VIN__"],
			"manufacturer": "Mitsubishi",
			"model": "Outlander PHEV"
		},
		"~": "__TOPIC__"}`,
		"%s/sensor/%s_battery_charge_remaining/config": `{
		"name": "__NAME__ Charge Remaining",
		"state_topic": "~/charge/remaining",
		"unit_of_measurement": "min",
		"avty_t": "~/available",
		"unique_id": "__VIN___battery_charge_remaining",
		"dev": {
			"name": "PHEV __VIN__",
			"identifiers": ["phev-__VIN__"],
			"manufacturer": "Mitsubishi",
			"model": "Outlander PHEV"
		},
		"~": "__TOPIC__"}`,
		"%s/binary_sensor/%s_charger_connected/config": `{
		"device_class": "plug",
		"name": "__NAME__ Charger Connected",
		"state_topic": "~/charge/plug",
		"payload_on": "connected",
		"payload_off": "unplugged",
		"avty_t": "~/available",
		"unique_id": "__VIN___charger_connected",
		"dev": {
			"name": "PHEV __VIN__",
			"identifiers": ["phev-__VIN__"],
			"manufacturer": "Mitsubishi",
			"model": "Outlander PHEV"
		},
		"~": "__TOPIC__"}`,
		"%s/binary_sensor/%s_battery_charging/config": `{
		"device_class": "battery_charging",
		"name": "__NAME__ Charging",
		"state_topic": "~/charge/charging",
		"payload_on": "on",
		"payload_off": "off",
		"avty_t": "~/available",
		"unique_id": "__VIN___battery_charging",
		"dev": {
			"name": "PHEV __VIN__",
			"identifiers": ["phev-__VIN__"],
			"manufacturer": "Mitsubishi",
			"model": "Outlander PHEV"
		},
		"~": "__TOPIC__"}`,
		"%s/switch/%s_cancel_charge_timer/config": `{
		"name": "__NAME__ Disable Charge Timer",
		"icon": "mdi:timer-off",
		"state_topic": "~/battery/charging",
		"command_topic": "~/set/cancelchargetimer",
		"avty_t": "~/available",
		"unique_id": "__VIN___cancel_charge_timer",
		"dev": {
			"name": "PHEV __VIN__",
			"identifiers": ["phev-__VIN__"],
			"manufacturer": "Mitsubishi",
			"model": "Outlander PHEV"
		},
		"~": "__TOPIC__"}`,
		// Climate
		"%s/switch/%s_climate_heat/config": `{
		"name": "__NAME__ Heat",
		"icon": "mdi:weather-sunny",
		"state_topic": "~/climate/heat",
		"command_topic": "~/set/climate/heat",
		"payload_off": "off",
		"payload_on": "on",
		"avty_t": "~/available",
		"unique_id": "__VIN___climate_heat",
		"dev": {
			"name": "PHEV __VIN__",
			"identifiers": ["phev-__VIN__"],
			"manufacturer": "Mitsubishi",
			"model": "Outlander PHEV"
		},
		"~": "__TOPIC__"}`,
		"%s/switch/%s_climate_cool/config": `{
		"name": "__NAME__ cool",
		"icon": "mdi:air-conditioner",
		"state_topic": "~/climate/cool",
		"command_topic": "~/set/climate/cool",
		"payload_off": "off",
		"payload_on": "on",
		"avty_t": "~/available",
		"unique_id": "__VIN___climate_cool",
		"dev": {
			"name": "PHEV __VIN__",
			"identifiers": ["phev-__VIN__"],
			"manufacturer": "Mitsubishi",
			"model": "Outlander PHEV"
		},
		"~": "__TOPIC__"}`,
		"%s/switch/%s_climate_windscreen/config": `{
		"name": "__NAME__ windscreen",
		"state_topic": "~/climate/windscreen",
		"command_topic": "~/set/climate/windscreen",
		"payload_off": "off",
		"payload_on": "on",
		"avty_t": "~/available",
		"unique_id": "__VIN___climate_windscreen",
		"dev": {
			"name": "PHEV __VIN__",
			"identifiers": ["phev-__VIN__"],
			"manufacturer": "Mitsubishi",
			"model": "Outlander PHEV"
		},
		"icon": "mdi:car-defrost-front",
		"~": "__TOPIC__"}`,
		"%s/select/%s_climate_on/config": `{
				"name": "__NAME__ climate state",
				"state_topic": "~/climate/mode",
				"command_topic": "~/set/climate/mode",
				"options": [ "off", "heat", "cool", "windscreen"],
				"avty_t": "~/available",
				"unique_id": "__VIN___climate_on",
		"dev": {
			"name": "PHEV __VIN__",
			"identifiers": ["phev-__VIN__"],
			"manufacturer": "Mitsubishi",
			"model": "Outlander PHEV"
		},
				"icon": "mdi:car-seat-heater",
				"~": "__TOPIC__"}`,
		// Lights.
		"%s/light/%s_parkinglights/config": `{
		"name": "__NAME__ Park Lights",
		"icon": "mdi:car-parking-lights",
		"state_topic": "~/lights/parking",
		"command_topic": "~/set/parkinglights",
		"payload_off": "off",
		"payload_on": "on",
		"avty_t": "~/available",
		"unique_id": "__VIN___parkinglights",
		"dev": {
			"name": "PHEV __VIN__",
			"identifiers": ["phev-__VIN__"],
			"manufacturer": "Mitsubishi",
			"model": "Outlander PHEV"
		},
		"~": "__TOPIC__"}`,
		"%s/light/%s_headlights/config": `{
		"name": "__NAME__ Head Lights",
		"icon": "mdi:car-light-high",
		"state_topic": "~/lights/head",
		"command_topic": "~/set/headlights",
		"payload_off": "off",
		"payload_on": "on",
		"avty_t": "~/available",
		"unique_id": "__VIN___headlights",
		"dev": {
			"name": "PHEV __VIN__",
			"identifiers": ["phev-__VIN__"],
			"manufacturer": "Mitsubishi",
			"model": "Outlander PHEV"
		},
		"~": "__TOPIC__"}`,
		// General topics.
		"%s/button/%s_reconnect_wifi/config": `{
		"name": "__NAME__ Restart Wifi connetion",
		"icon": "mdi:timer-off",
		"command_topic": "~/connection",
		"payload_press": "restart",
		"avty_t": "~/available",
		"unique_id": "__VIN___restart_wifi",
		"dev": {
			"name": "PHEV __VIN__",
			"identifiers": ["phev-__VIN__"],
			"manufacturer": "Mitsubishi",
			"model": "Outlander PHEV"
		},
		"~": "__TOPIC__"}`,
	}
	mappings := map[string]string{
		"__NAME__":  name,
		"__VIN__":   vin,
		"__TOPIC__": topic,
	}
	for topic, d := range discoveryData {
		topic = fmt.Sprintf(topic, m.haDiscoveryPrefix, vin)
		for in, out := range mappings {
			d = strings.Replace(d, in, out, -1)
		}
		m.client.Publish(topic, 0, false, d)
		//m.client.Publish(topic, 0, false, "{}")
	}
}

func init() {
	clientCmd.AddCommand(mqttCmd)

	// Here you will define your flags and configuration settings.

	// Cobra supports Persistent Flags which will work for this command
	// and all subcommands, e.g.:
	// mqttCmd.PersistentFlags().String("foo", "", "A help for foo")

	// Cobra supports local flags which will only run when this command
	// is called directly, e.g.:
	// mqttCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")
	mqttCmd.Flags().String("mqtt_server", "tcp://127.0.0.1:1883", "Address of MQTT server")
	mqttCmd.Flags().String("mqtt_username", "", "Username to login to MQTT server")
	mqttCmd.Flags().String("mqtt_password", "", "Password to login to MQTT server")
	mqttCmd.Flags().String("mqtt_topic_prefix", "phev", "Prefix for MQTT topics")
	mqttCmd.Flags().Bool("ha_discovery", true, "Enable Home Assistant MQTT discovery")
	mqttCmd.Flags().String("ha_discovery_prefix", "homeassistant", "Prefix for Home Assistant MQTT discovery")
	mqttCmd.Flags().Duration("update_interval", 5*time.Minute, "How often to request force updates")
	mqttCmd.Flags().Duration("wifi_restart_time", 0, "Attempt to restart Wifi if no connection for this long")
	mqttCmd.Flags().Duration("wifi_restart_retry_time", 2*time.Minute, "Interval to attempt Wifi restart")
	mqttCmd.Flags().String("wifi_restart_command", defaultWifiRestartCmd, "Command to restart Wifi connection to Phev")
}
