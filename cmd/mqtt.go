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
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/buxtronix/phev2mqtt/client"
	"github.com/buxtronix/phev2mqtt/protocol"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"os/exec"
	"strings"
	"sync"
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
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		mc := &mqttClient{climate: new(climate)}
		return mc.Run(cmd, args)
	},
}

// Tracks complete climate state as on and mode are separately
// sent by the car.
type climate struct {
	state *protocol.PreACState
	mode  *string
}

func (c *climate) setMode(m string) {
	c.mode = &m
}
func (c *climate) setState(state protocol.PreACState) {
	c.state = &state
}

func (c *climate) mqttStates() map[string]string {
	m := map[string]string{
		"/climate/state":      "off",
		"/climate/cool":       "off",
		"/climate/heat":       "off",
		"/climate/windscreen": "off",
	}
	if c.mode == nil || c.state == nil {
		return m
	}
	switch *c.state {
	case protocol.PreACOn:
		m["/climate/state"] = *c.mode
	case protocol.PreACOff:
		{
			m["/climate/state"] = "off"
			return m
		}
	case protocol.PreACTerminated:
		{
			m["/climate/state"] = "terminated"
			return m
		}
	default:
		{
			m["/climate/state"] = "unknown"
			return m
		}
	}
	m["/climate/state"] = *c.mode
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

var lastWifiRestart time.Time

func restartWifi(cmd *cobra.Command) error {
	restartRetryTime := cfgDuration("wifi_restart_retry_time")

	if time.Since(lastWifiRestart) < restartRetryTime {
		return nil
	}
	defer func() {
		lastWifiRestart = time.Now()
	}()
	return runWifiRestart()
}

// runWifiRestart runs wifi_restart_command. Never for longer than a minute:
// a hanging command would otherwise stall the car loop.
func runWifiRestart() error {
	restartCommand := cfgString("wifi_restart_command")
	if restartCommand == "" {
		log.Debugf("wifi restart disabled")
		return nil
	}

	log.Debugf("Attempting to restart wifi")

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	restartCmd := exec.CommandContext(ctx, "/bin/sh", "-c", restartCommand)

	stdoutStderr, err := restartCmd.CombinedOutput()
	if len(stdoutStderr) > 0 {
		log.Infof("Output from wifi restart: %s", stdoutStderr)
	}
	return err
}

type mqttClient struct {
	updateInterval time.Duration
	lastError      error
	climate        *climate
	// The car's WiFi interface (wifi_interface) and its NetworkManager
	// profile, shared with the web interface; set before anything runs.
	wifiIface string
	wifi      *wifiManager
	// extra_sensors, and the registers they are decoded from; the tracker
	// is only used by the car loop.
	extraSensors bool
	status       protocol.StatusTracker
	// climate_guard: the registers of the current session, and one guarded
	// climate command at a time.
	climateGuard bool
	// extra_commands: buttons such as Sync Car Clock (commands.go).
	extraCommands bool
	regs          regCache
	commandMu     sync.Mutex

	// Shared with the MQTT callbacks and the web interface.
	state *gatewayState
	web   *webServer

	// The broker connection and its settings; the web interface can change
	// them at runtime (connectMQTT), so they are guarded by mqttMu.
	mqttMu               sync.Mutex
	client               mqtt.Client
	prefix               string
	haDiscovery          bool
	haDiscoveryPrefix    string
	haDeviceName         string
	haPublishedDiscovery bool
}

func (m *mqttClient) topic(topic string) string {
	m.mqttMu.Lock()
	defer m.mqttMu.Unlock()
	return m.prefix + topic
}

// haStatusTopic is where Home Assistant announces that it (re)started.
func (m *mqttClient) haStatusTopic() string {
	m.mqttMu.Lock()
	defer m.mqttMu.Unlock()
	return m.haDiscoveryPrefix + "/status"
}

func (m *mqttClient) mqtt() mqtt.Client {
	m.mqttMu.Lock()
	defer m.mqttMu.Unlock()
	return m.client
}

func (m *mqttClient) mqttConnected() bool {
	c := m.mqtt()
	return c != nil && c.IsConnected()
}

// mqttPublish publishes topic (a full topic, prefix included). It never
// blocks the caller for long: a broker that is down or not configured must
// not stall the car loop.
func (m *mqttClient) mqttPublish(topic string, qos byte, retained bool, payload string) {
	c := m.mqtt()
	if c == nil {
		return
	}
	token := c.Publish(topic, qos, retained, payload)
	if qos == 0 {
		return
	}
	if !token.WaitTimeout(5 * time.Second) {
		log.Warnf("Timed out publishing to %s", topic)
	} else if token.Error() != nil {
		log.Error(token.Error())
	}
}

// connectMQTT (re)connects to the broker with the current settings. The
// connection is retried in the background, so a broker that is down or not
// configured yet does not stop the gateway or its web interface.
func (m *mqttClient) connectMQTT() {
	server := cfgString("mqtt_server")
	username := cfgString("mqtt_username")
	password := cfgString("mqtt_password")
	disableSet := cfgBool("mqtt_disable_register_set_command")

	m.mqttMu.Lock()
	old := m.client
	m.client = nil
	m.prefix = cfgString("mqtt_topic_prefix")
	m.haDiscovery = cfgBool("ha_discovery")
	m.haDiscoveryPrefix = cfgString("ha_discovery_prefix")
	m.haDeviceName = cfgString("ha_device_name")
	m.haPublishedDiscovery = false
	m.mqttMu.Unlock()
	if old != nil {
		old.Disconnect(250)
	}
	defer m.state.notify()

	if server == "" {
		log.Warn("No mqtt_server configured, not connecting to MQTT")
		return
	}
	if disableSet {
		log.Info("Setting vechicle registers via MQTT is disabled")
	}

	// Subscriptions and retained state must be re-established on every
	// (re)connection - a broker restart silently drops both.
	onConnect := func(mqtt_client mqtt.Client) {
		log.Info("Connected to MQTT broker")
		topics := []string{m.topic("/connection"), m.topic("/restart"), m.topic("/settings/#"), m.haStatusTopic()}
		if !disableSet {
			topics = append(topics, m.topic("/set/#"))
		}
		for _, t := range topics {
			if token := mqtt_client.Subscribe(t, 0, nil); token.Wait() && token.Error() != nil {
				log.Errorf("Error subscribing to %s: %v", t, token.Error())
			}
		}
		// The will says offline when this connection breaks.
		mqtt_client.Publish(m.topic(gatewayAvailableTopic), 0, true, "online")
		mqtt_client.Publish(m.topic(availableTopic), 0, true, m.availabilityPayload())
		// A broker without persistence forgets retained values on restart.
		for _, t := range []string{"/connection/last_connected", "/connection/last_disconnected"} {
			if v := m.state.value(t); v != "" {
				mqtt_client.Publish(m.topic(t), 0, true, v)
			}
		}
		m.publishWifi(m.wifiIface)
		m.mqttMu.Lock()
		m.haPublishedDiscovery = false
		m.mqttMu.Unlock()
		m.republishDiscovery()
		m.state.notify()
	}

	options := mqtt.NewClientOptions().
		AddBroker(server).
		SetClientID("phev2mqtt").
		SetUsername(username).
		SetPassword(password).
		SetAutoReconnect(true).
		SetConnectRetry(true).
		SetConnectRetryInterval(10*time.Second).
		SetMaxReconnectInterval(time.Minute).
		SetDefaultPublishHandler(m.safeHandleIncomingMqtt).
		SetOnConnectHandler(onConnect).
		SetConnectionLostHandler(func(_ mqtt.Client, err error) {
			log.Warnf("Lost connection to MQTT broker: %v", err)
			m.state.notify()
		}).
		SetWill(m.topic(gatewayAvailableTopic), "offline", 1, true)

	c := mqtt.NewClient(options)
	m.mqttMu.Lock()
	m.client = c
	m.mqttMu.Unlock()
	log.Infof("Connecting to MQTT broker %s", server)
	c.Connect() // With ConnectRetry this keeps trying in the background.
}

func (m *mqttClient) Run(cmd *cobra.Command, args []string) error {
	if err := loadWebSettings(); err != nil {
		return err
	}
	m.updateInterval = cfgDuration("update_interval")
	wifiRestartTime := cfgDuration("wifi_restart_time")
	restartCommand := cfgString("wifi_restart_command")

	if restartCommand == "" {
		log.Infof("WiFi restart disabled")
	}

	if m.updateInterval <= 0 {
		return fmt.Errorf("update_interval must be positive, got %v", m.updateInterval)
	}

	m.state = newGatewayState()
	m.lastError = nil
	m.wifiIface = cfgString("wifi_interface")
	m.extraSensors = cfgBool("extra_sensors")
	m.climateGuard = cfgBool("climate_guard")
	m.extraCommands = cfgBool("extra_commands")
	m.wifi = &wifiManager{iface: m.wifiIface}
	carReconnect := m.wifiIface != "" && nmcliAvailable()
	if m.wifiIface != "" && !carReconnect {
		log.Infof("nmcli not found: the car's WiFi is left to the system, also when it stops reconnecting")
	}

	if listen := cfgString("web_listen"); listen != "" {
		web, err := newWebServer(m, listen, cfgString("web_password"), cfgBool("web_auth"))
		if err != nil {
			return err
		}
		m.web = web
		go web.serve()
	}

	m.connectMQTT()
	if m.wifiIface != "" {
		go m.watchWifi(m.wifiIface)
	}
	var lastCarReconnect time.Time

	for {
		if m.state.isEnabled() {
			if err := m.handlePhev(cmd); err != nil {
				// Do not flood the log with the same messages every second
				if m.lastError == nil || m.lastError.Error() != err.Error() {
					log.Error(err)
					m.lastError = err
				}
			}
			// Restart Wifi interface if > wifi_restart_time.
			if wifiRestartTime > 0 && m.state.sinceLastConnect() > wifiRestartTime {
				if err := restartWifi(cmd); err != nil {
					log.Errorf("Error restarting wifi: %v", err)
				}
			}
			// Bring the car's WiFi back when NetworkManager gave up on it.
			if carReconnect && m.state.wifiDownFor() >= carReconnectInterval &&
				time.Since(lastCarReconnect) >= carReconnectInterval {
				lastCarReconnect = time.Now()
				// In the background: bringing a connection up takes up to
				// nmcliTimeout, which must not hold up the car loop.
				go m.wifi.reconnectCar()
			}
		}

		time.Sleep(time.Second)
	}
}

// Republish Home Assistant discovery, if the VIN is already known.
func (m *mqttClient) republishDiscovery() {
	vin := m.state.getVIN()
	if vin == "" {
		return
	}
	m.publishHomeAssistantDiscovery(vin, m.topic(""))
}

// renameDevice gives the car another name in Home Assistant ("" for the
// default) and republishes the discovery. It reports false when that has to
// wait for the car's VIN, which comes with the first connection.
func (m *mqttClient) renameDevice(name string) bool {
	m.mqttMu.Lock()
	m.haDeviceName = name
	m.haPublishedDiscovery = false
	m.mqttMu.Unlock()
	if m.state.getVIN() == "" || !m.mqttConnected() {
		return false
	}
	m.republishDiscovery()
	return true
}

// deviceName is the name of the car in Home Assistant.
func deviceName(name, vin string) string {
	if name == "" {
		return "PHEV " + vin
	}
	return name
}

// jsonString escapes s for use inside a JSON string.
func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b[1 : len(b)-1])
}

func (m *mqttClient) publish(topic, payload string) {
	m.mqttPublish(m.topic(topic), 0, false, payload)
	m.state.setValue(topic, payload)
}

// A panic in a paho callback takes down the whole daemon, so one bad
// message must not get that far.
func (m *mqttClient) safeHandleIncomingMqtt(mqtt_client mqtt.Client, msg mqtt.Message) {
	defer func() {
		if r := recover(); r != nil {
			log.Errorf("Panic handling MQTT topic [%s]: %v", msg.Topic(), r)
		}
	}()
	m.handleIncomingMqtt(mqtt_client, msg)
}

func (m *mqttClient) handleIncomingMqtt(mqtt_client mqtt.Client, msg mqtt.Message) {
	log.Infof("Topic: [%s] Payload: [%s]", msg.Topic(), msg.Payload())

	// The car client only exists once handlePhev has run; commands that
	// arrive before that (e.g. retained ones, right after subscribing) are
	// dropped. One snapshot of it for the whole command: handlePhev replaces
	// it on every connection attempt.
	phev := m.state.car()
	haStatus := m.haStatusTopic()
	if phev == nil && msg.Topic() != haStatus && msg.Topic() != m.topic("/connection") && msg.Topic() != m.topic("/restart") {
		log.Infof("Not connected to the car yet, ignoring [%s]", msg.Topic())
		return
	}

	if msg.Topic() == haStatus {
		// Home Assistant birth message - it has restarted, so needs
		// the discovery configs and availability republished.
		if strings.ToLower(string(msg.Payload())) == "online" {
			m.mqttMu.Lock()
			m.haPublishedDiscovery = false
			m.mqttMu.Unlock()
			m.republishDiscovery()
			m.mqttPublish(m.topic(gatewayAvailableTopic), 0, true, "online")
			m.mqttPublish(m.topic(availableTopic), 0, true, m.availabilityPayload())
		}
	} else if msg.Topic() == m.topic("/restart") {
		// A retained one would restart every new start again.
		if msg.Retained() {
			log.Warnf("Ignoring retained [%s]; clear it on the broker", msg.Topic())
			return
		}
		// Not in this callback: restart waits for the broker.
		go m.restart()
	} else if strings.HasPrefix(msg.Topic(), m.topic("/set/register/")) {
		// Strip the prefix instead of splitting the whole topic: the prefix
		// itself may contain slashes (e.g. "home/phev").
		register, err := hex.DecodeString(strings.TrimPrefix(msg.Topic(), m.topic("/set/register/")))
		if err != nil || len(register) != 1 {
			log.Infof("Bad register in topic [%s], want one hex byte", msg.Topic())
			return
		}
		data, err := hex.DecodeString(string(msg.Payload()))
		if err != nil {
			log.Infof("Bad payload [%s]: %v", msg.Payload(), err)
			return
		}
		if err := phev.SetRegister(register[0], data); err != nil {
			log.Infof("Error setting register %02x: %v", register[0], err)
			return
		}
	} else if msg.Topic() == m.topic("/connection") {
		payload := strings.ToLower(string(msg.Payload()))
		switch payload {
		case "off":
			m.state.setEnabled(false)
			if phev != nil {
				phev.Close()
			}
			m.setAvailable(false, "connection turned off")
		case "on":
			m.state.setEnabled(true)
		case "restart":
			m.state.setEnabled(true)
			m.setAvailable(false, "reconnecting on request")
			if phev != nil {
				phev.Close()
			}
		}
	} else if msg.Topic() == m.topic("/set/sync_clock") {
		if !m.extraCommands || msg.Retained() {
			// A retained command would set the clock on every connect.
			log.Infof("Ignoring %s: extra_commands is off, or the message is retained", msg.Topic())
			return
		}
		go m.syncClock(phev)
	} else if msg.Topic() == m.topic("/set/parkinglights") {
		values := map[string]byte{"on": 0x1, "off": 0x2}
		if v, ok := values[strings.ToLower(string(msg.Payload()))]; ok {
			if err := phev.SetRegister(0xb, []byte{v}); err != nil {
				log.Infof("Error setting register 0xb: %v", err)
				return
			}
		}
	} else if msg.Topic() == m.topic("/set/headlights") {
		values := map[string]byte{"on": 0x1, "off": 0x2}
		if v, ok := values[strings.ToLower(string(msg.Payload()))]; ok {
			if err := phev.SetRegister(0xa, []byte{v}); err != nil {
				log.Infof("Error setting register 0xa: %v", err)
				return
			}
		}
	} else if msg.Topic() == m.topic("/set/cancelchargetimer") {
		if err := phev.SetRegister(0x17, []byte{0x1}); err != nil {
			log.Infof("Error setting register 0x17: %v", err)
			return
		}
		if err := phev.SetRegister(0x17, []byte{0x11}); err != nil {
			log.Infof("Error setting register 0x17: %v", err)
			return
		}
	} else if strings.HasPrefix(msg.Topic(), m.topic("/set/climate/state")) {
		payload := strings.ToLower(string(msg.Payload()))
		if payload == "reset" {
			if err := phev.SetRegister(protocol.SetAckPreACTermRegister, []byte{0x1}); err != nil {
				log.Infof("Error acknowledging Pre-AC termination: %v", err)
				return
			}
		}
	} else if strings.HasPrefix(msg.Topic(), m.topic("/set/climate/")) {
		topic := msg.Topic()
		payload := strings.ToLower(string(msg.Payload()))

		modeMap := map[string]byte{"off": 0x0, "OFF": 0x0, "cool": 0x1, "heat": 0x2, "windscreen": 0x3, "mode": 0x4}
		durMap := map[string]byte{"10": 0x0, "20": 0x1, "30": 0x2, "on": 0x2, "off": 0x0}
		parts := strings.Split(topic, "/")
		mode, ok := modeMap[parts[len(parts)-1]]
		if !ok {
			log.Errorf("Unknown climate mode: %s", parts[len(parts)-1])
			return
		}
		if mode == 0x4 { // set/climate/mode -> "heat"
			// An unknown payload would otherwise become mode 0, i.e. "off".
			if mode, ok = modeMap[payload]; !ok || mode == 0x4 {
				log.Errorf("Unknown climate mode: %s", payload)
				return
			}
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

		my := m.state.getModelYear()
		if m.climateGuard {
			// Not in this callback: it waits up to half a minute for the
			// climate to start.
			go m.setClimateGuarded(phev, my, mode, duration)
			return
		}
		switch my {
		case client.ModelYear14:
			// Set the AC mode first
			registerPayload := bytes.Repeat([]byte{0xff}, 15)
			registerPayload[0] = 0x0
			registerPayload[1] = 0x0
			// MY14 encodes the duration in the high nibble (10min=0x00, 20min=0x10, 30min=0x20).
			registerPayload[6] = mode | (duration << 4)
			if err := phev.SetRegister(protocol.SetACModeRegisterMY14, registerPayload); err != nil {
				log.Infof("Error setting AC mode: %v", err)
				return
			}

			// Then, enable/disable the AC
			acEnabled := byte(0x02)
			if mode == 0x0 {
				acEnabled = 0x01
			}
			if err := phev.SetRegister(protocol.SetACEnabledRegisterMY14, []byte{acEnabled}); err != nil {
				log.Infof("Error setting AC enabled state: %v", err)
				return
			}
		case client.ModelYear18, client.ModelYear24:
			state := byte(0x02)
			if mode == 0x0 {
				state = 0x1
			}
			if err := phev.SetRegister(protocol.SetACModeRegisterMY18, []byte{state, mode, duration, 0x0}); err != nil {
				log.Infof("Error setting AC mode: %v", err)
				return
			}
		default:
			log.Errorf("Ignoring climate command: model year of the car not known yet")
		}
	} else if msg.Topic() == m.topic("/settings/dump") {
		log.Infof("CURRENT_SETTINGS:")
		log.Infof("\n%s", phev.Settings.Dump())
		phev.Settings.Clear()
	} else {
		log.Errorf("Unknown topic from mqtt: %s", msg.Topic())
	}
}

func (m *mqttClient) handlePhev(cmd *cobra.Command) error {
	address := cfgString("address")
	phev, err := client.New(client.AddressOption(address), client.KeyResyncOption(cfgBool("key_resync")))
	if err != nil {
		return err
	}
	m.state.setCar(phev)

	if err := phev.Connect(); err != nil {
		return err
	}
	// A connection that outlives this attempt keeps pinging the car and
	// occupies its single client slot, blocking all future reconnects.
	defer phev.Close()

	if err := phev.Start(); err != nil {
		return err
	}
	// ModelYear is set before Start returns; later changes would race.
	m.state.setConnected(true, phev.ModelYear)
	m.status.Reset()
	m.regs.reset()
	m.status.MY14 = phev.ModelYear == client.ModelYear14
	// phev/available goes online with the first data from the car (carData).
	m.publishRetained("/connection/last_connected", time.Now().Format(time.RFC3339))
	m.publishWifiSignal(m.wifiIface)

	m.lastError = nil

	defer func() {
		m.state.setConnected(false, client.ModelYearUnknown)
		m.carDisconnected()
		m.publishRetained("/connection/last_disconnected", time.Now().Format(time.RFC3339))
	}()

	var encodingErrorCount = 0
	var lastEncodingError time.Time

	updaterTicker := time.NewTicker(m.updateInterval)
	for {
		select {
		case <-updaterTicker.C:
			if m.staleCar() {
				// Answers pings, perhaps, but no data: asleep or gone.
				m.setAvailable(false, fmt.Sprintf("no data for %v, reconnecting", m.updateInterval))
				updaterTicker.Stop()
				return fmt.Errorf("no data from the car")
			}
			m.publishWifiSignal(m.wifiIface)
			// Not inline: SetRegister waits up to 10s for the ack, which the
			// reader can only deliver while this loop keeps draining Recv.
			go func(phev *client.Client) {
				if err := phev.SetRegister(0x6, []byte{0x3}); err != nil {
					log.Debugf("Error requesting update: %v", err)
				}
			}(phev)
		case msg, ok := <-phev.Recv:
			if !ok {
				log.Infof("Connection closed.")
				updaterTicker.Stop()
				return fmt.Errorf("connection closed")
			}
			switch msg.Type {
			case protocol.CmdInBadEncoding:
				if time.Since(lastEncodingError) > 15*time.Second {
					encodingErrorCount = 0
				}
				if encodingErrorCount > 50 {
					updaterTicker.Stop()
					return fmt.Errorf("disconnecting due to too many errors")
				}
				encodingErrorCount += 1
				lastEncodingError = time.Now()
			case protocol.CmdInResp:
				if msg.Ack != protocol.Request {
					break
				}
				m.publishRegister(msg)
				m.carData()
				if err := phev.SendMessage(&protocol.PhevMessage{
					Type:     protocol.CmdOutSend,
					Register: msg.Register,
					Ack:      protocol.Ack,
					Xor:      msg.Xor,
					Data:     []byte{0x0},
				}); err != nil {
					updaterTicker.Stop()
					return err
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
	m.publishExtraStatus(msg.Register, msg.Data)
	m.regs.put(msg.Register, msg.Data)
	switch reg := msg.Reg.(type) {
	case *protocol.RegisterVIN:
		m.state.setVIN(reg.VIN)
		m.publish("/vin", reg.VIN)
		m.publishHomeAssistantDiscovery(reg.VIN, m.topic(""))
		m.publish("/registrations", fmt.Sprintf("%d", reg.Registrations))
	case *protocol.RegisterECUVersion:
		m.publish("/ecuversion", reg.Version)
	case *protocol.RegisterACMode:
		m.climate.setMode(reg.Mode)
		for t, p := range m.climate.mqttStates() {
			m.publish(t, p)
		}
	case *protocol.RegisterPreACState:
		m.climate.setState(reg.State)
		for t, p := range m.climate.mqttStates() {
			m.publish(t, p)
		}
	case *protocol.RegisterChargeStatus:
		m.publish("/charge/charging", boolOnOff[reg.Charging])
		if reg.Remaining < 1000 {
			m.publish("/charge/remaining", fmt.Sprintf("%d", reg.Remaining))
		} else {
			log.Debugf("Ignoring charge remanining reading: %v", reg.Remaining)
			if cache := m.state.value("/charge/remaining"); cache != "" {
				m.publish("/charge/remaining", cache)
				log.Debugf("Publishing last best known charge remaining reading: %v", cache)
			}
		}
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
		if (reg.Level > 5) && (reg.Level < 255) {
			m.publish("/battery/level", fmt.Sprintf("%d", reg.Level))
		} else {
			if cache := m.state.value("/battery/level"); cache != "" {
				m.publish("/battery/level", cache)
				log.Debugf("Ignoring battery level reading: %v, publishing last best known: %v", reg.Level, cache)
			}
		}
		m.publish("/lights/parking", boolOnOff[reg.ParkingLights])
	case *protocol.RegisterLightStatus:
		m.publish("/lights/interior", boolOnOff[reg.Interior])
		m.publish("/lights/hazard", boolOnOff[reg.Hazard])
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
func (m *mqttClient) publishHomeAssistantDiscovery(vin, topic string) {

	// Only to a connected broker: onConnect publishes it again anyway, and
	// waiting for 20+ publish timeouts would stall the car loop.
	m.mqttMu.Lock()
	if m.haPublishedDiscovery || !m.haDiscovery || m.client == nil || !m.client.IsConnected() {
		m.mqttMu.Unlock()
		return
	}
	m.haPublishedDiscovery = true
	discoveryPrefix := m.haDiscoveryPrefix
	device := deviceName(m.haDeviceName, vin)
	m.mqttMu.Unlock()
	discoveryData := map[string]string{
		// Doors.
		"%s/binary_sensor/%s_door_locked/config": `{
		"device_class": "lock",
		"name": "Locked",
		"state_topic": "~/door/locked",
		"payload_off": "closed",
		"payload_on": "open",
		"availability": [{"topic": "~/gateway/available"}, {"topic": "~/available"}],
		"availability_mode": "all",
		"unique_id": "__VIN___door_locked",
		"device": {
			"name": "__DEVICE__",
			"identifiers": ["phev-__VIN__"],
			"manufacturer": "Mitsubishi",
			"model": "Outlander PHEV"},
		"~": "__TOPIC__"}`,
		"%s/binary_sensor/%s_door_bonnet/config": `{
		"device_class": "door",
		"name": "Bonnet",
		"state_topic": "~/door/bonnet",
		"payload_off": "closed",
		"payload_on": "open",
		"availability": [{"topic": "~/gateway/available"}, {"topic": "~/available"}],
		"availability_mode": "all",
		"unique_id": "__VIN___door_bonnet",
		"device": {
			"name": "__DEVICE__",
			"identifiers": ["phev-__VIN__"],
			"manufacturer": "Mitsubishi",
			"model": "Outlander PHEV"},
		"~": "__TOPIC__"}`,
		"%s/binary_sensor/%s_door_boot/config": `{
		"device_class": "door",
		"name": "Boot",
		"state_topic": "~/door/boot",
		"payload_off": "closed",
		"payload_on": "open",
		"availability": [{"topic": "~/gateway/available"}, {"topic": "~/available"}],
		"availability_mode": "all",
		"unique_id": "__VIN___door_boot",
		"dev": {
			"name": "__DEVICE__",
			"identifiers": ["phev-__VIN__"],
			"manufacturer": "Mitsubishi",
			"model": "Outlander PHEV"
		},
		"~": "__TOPIC__"}`,
		"%s/binary_sensor/%s_door_front_passenger/config": `{
		"device_class": "door",
		"name": "Front Passenger Door",
		"state_topic": "~/door/front_passenger",
		"payload_off": "closed",
		"payload_on": "open",
		"availability": [{"topic": "~/gateway/available"}, {"topic": "~/available"}],
		"availability_mode": "all",
		"unique_id": "__VIN___door_front_passenger",
		"dev": {
			"name": "__DEVICE__",
			"identifiers": ["phev-__VIN__"],
			"manufacturer": "Mitsubishi",
			"model": "Outlander PHEV"
		},
		"~": "__TOPIC__"}`,
		"%s/binary_sensor/%s_door_driver/config": `{
		"device_class": "door",
		"name": "Driver Door",
		"state_topic": "~/door/driver",
		"payload_off": "closed",
		"payload_on": "open",
		"availability": [{"topic": "~/gateway/available"}, {"topic": "~/available"}],
		"availability_mode": "all",
		"unique_id": "__VIN___door_driver",
		"dev": {
			"name": "__DEVICE__",
			"identifiers": ["phev-__VIN__"],
			"manufacturer": "Mitsubishi",
			"model": "Outlander PHEV"
		},
		"~": "__TOPIC__"}`,
		"%s/binary_sensor/%s_door_rear_left/config": `{
		"device_class": "door",
		"name": "Rear Left Door",
		"state_topic": "~/door/rear_left",
		"payload_off": "closed",
		"payload_on": "open",
		"availability": [{"topic": "~/gateway/available"}, {"topic": "~/available"}],
		"availability_mode": "all",
		"unique_id": "__VIN___door_rear_left",
		"dev": {
			"name": "__DEVICE__",
			"identifiers": ["phev-__VIN__"],
			"manufacturer": "Mitsubishi",
			"model": "Outlander PHEV"
		},
		"~": "__TOPIC__"}`,
		"%s/binary_sensor/%s_door_rear_right/config": `{
		"device_class": "door",
		"name": "Rear Right Door",
		"state_topic": "~/door/rear_right",
		"payload_off": "closed",
		"payload_on": "open",
		"availability": [{"topic": "~/gateway/available"}, {"topic": "~/available"}],
		"availability_mode": "all",
		"unique_id": "__VIN___door_rear_right",
		"dev": {
			"name": "__DEVICE__",
			"identifiers": ["phev-__VIN__"],
			"manufacturer": "Mitsubishi",
			"model": "Outlander PHEV"
		},
		"~": "__TOPIC__"}`,

		// Battery and charging
		"%s/sensor/%s_battery_level/config": `{
		"device_class": "battery",
		"name": "Battery",
		"state_topic": "~/battery/level",
		"state_class": "measurement",
		"unit_of_measurement": "%",
		"availability": [{"topic": "~/gateway/available"}, {"topic": "~/available"}],
		"availability_mode": "all",
		"unique_id": "__VIN___battery_level",
		"dev": {
			"name": "__DEVICE__",
			"identifiers": ["phev-__VIN__"],
			"manufacturer": "Mitsubishi",
			"model": "Outlander PHEV"
		},
		"~": "__TOPIC__"}`,
		"%s/sensor/%s_battery_charge_remaining/config": `{
		"name": "Charge Remaining",
		"state_topic": "~/charge/remaining",
		"unit_of_measurement": "min",
		"availability": [{"topic": "~/gateway/available"}, {"topic": "~/available"}],
		"availability_mode": "all",
		"unique_id": "__VIN___battery_charge_remaining",
		"dev": {
			"name": "__DEVICE__",
			"identifiers": ["phev-__VIN__"],
			"manufacturer": "Mitsubishi",
			"model": "Outlander PHEV"
		},
		"~": "__TOPIC__"}`,
		"%s/binary_sensor/%s_charger_connected/config": `{
		"device_class": "plug",
		"name": "Charger Connected",
		"state_topic": "~/charge/plug",
		"payload_on": "connected",
		"payload_off": "unplugged",
		"availability": [{"topic": "~/gateway/available"}, {"topic": "~/available"}],
		"availability_mode": "all",
		"unique_id": "__VIN___charger_connected",
		"dev": {
			"name": "__DEVICE__",
			"identifiers": ["phev-__VIN__"],
			"manufacturer": "Mitsubishi",
			"model": "Outlander PHEV"
		},
		"~": "__TOPIC__"}`,
		"%s/binary_sensor/%s_battery_charging/config": `{
		"device_class": "battery_charging",
		"name": "Charging",
		"state_topic": "~/charge/charging",
		"payload_on": "on",
		"payload_off": "off",
		"availability": [{"topic": "~/gateway/available"}, {"topic": "~/available"}],
		"availability_mode": "all",
		"unique_id": "__VIN___battery_charging",
		"dev": {
			"name": "__DEVICE__",
			"identifiers": ["phev-__VIN__"],
			"manufacturer": "Mitsubishi",
			"model": "Outlander PHEV"
		},
		"~": "__TOPIC__"}`,
		"%s/switch/%s_cancel_charge_timer/config": `{
		"name": "Disable Charge Timer",
		"icon": "mdi:timer-off",
		"state_topic": "~/charge/charging",
		"state_on": "on",
		"state_off": "off",
		"command_topic": "~/set/cancelchargetimer",
		"availability": [{"topic": "~/gateway/available"}, {"topic": "~/available"}],
		"availability_mode": "all",
		"unique_id": "__VIN___cancel_charge_timer",
		"dev": {
			"name": "__DEVICE__",
			"identifiers": ["phev-__VIN__"],
			"manufacturer": "Mitsubishi",
			"model": "Outlander PHEV"
		},
		"~": "__TOPIC__"}`,
		// Climate
		"%s/switch/%s_climate_heat/config": `{
		"name": "Heat",
		"icon": "mdi:weather-sunny",
		"state_topic": "~/climate/heat",
		"command_topic": "~/set/climate/heat",
		"payload_off": "off",
		"payload_on": "on",
		"availability": [{"topic": "~/gateway/available"}, {"topic": "~/available"}],
		"availability_mode": "all",
		"unique_id": "__VIN___climate_heat",
		"dev": {
			"name": "__DEVICE__",
			"identifiers": ["phev-__VIN__"],
			"manufacturer": "Mitsubishi",
			"model": "Outlander PHEV"
		},
		"~": "__TOPIC__"}`,
		"%s/switch/%s_climate_cool/config": `{
		"name": "Cool",
		"icon": "mdi:air-conditioner",
		"state_topic": "~/climate/cool",
		"command_topic": "~/set/climate/cool",
		"payload_off": "off",
		"payload_on": "on",
		"availability": [{"topic": "~/gateway/available"}, {"topic": "~/available"}],
		"availability_mode": "all",
		"unique_id": "__VIN___climate_cool",
		"dev": {
			"name": "__DEVICE__",
			"identifiers": ["phev-__VIN__"],
			"manufacturer": "Mitsubishi",
			"model": "Outlander PHEV"
		},
		"~": "__TOPIC__"}`,
		"%s/switch/%s_climate_windscreen/config": `{
		"name": "Windscreen",
		"state_topic": "~/climate/windscreen",
		"command_topic": "~/set/climate/windscreen",
		"payload_off": "off",
		"payload_on": "on",
		"availability": [{"topic": "~/gateway/available"}, {"topic": "~/available"}],
		"availability_mode": "all",
		"unique_id": "__VIN___climate_windscreen",
		"dev": {
			"name": "__DEVICE__",
			"identifiers": ["phev-__VIN__"],
			"manufacturer": "Mitsubishi",
			"model": "Outlander PHEV"
		},
		"icon": "mdi:car-defrost-front",
		"~": "__TOPIC__"}`,
		"%s/select/%s_climate_on/config": `{
				"name": "Climate state",
				"state_topic": "~/climate/state",
				"value_template": "{{ value if value in ['off', 'heat', 'cool', 'windscreen'] else 'off' }}",
				"command_topic": "~/set/climate/mode",
				"options": [ "off", "heat", "cool", "windscreen"],
				"availability": [{"topic": "~/gateway/available"}, {"topic": "~/available"}],
				"availability_mode": "all",
				"unique_id": "__VIN___climate_on",
		"dev": {
			"name": "__DEVICE__",
			"identifiers": ["phev-__VIN__"],
			"manufacturer": "Mitsubishi",
			"model": "Outlander PHEV"
		},
				"icon": "mdi:car-seat-heater",
				"~": "__TOPIC__"}`,
		// Lights.
		"%s/light/%s_parkinglights/config": `{
		"name": "Park Lights",
		"icon": "mdi:car-parking-lights",
		"state_topic": "~/lights/parking",
		"command_topic": "~/set/parkinglights",
		"payload_off": "off",
		"payload_on": "on",
		"availability": [{"topic": "~/gateway/available"}, {"topic": "~/available"}],
		"availability_mode": "all",
		"unique_id": "__VIN___parkinglights",
		"dev": {
			"name": "__DEVICE__",
			"identifiers": ["phev-__VIN__"],
			"manufacturer": "Mitsubishi",
			"model": "Outlander PHEV"
		},
		"~": "__TOPIC__"}`,
		"%s/light/%s_headlights/config": `{
		"name": "Head Lights",
		"icon": "mdi:car-light-dimmed",
		"state_topic": "~/lights/head",
		"command_topic": "~/set/headlights",
		"payload_off": "off",
		"payload_on": "on",
		"availability": [{"topic": "~/gateway/available"}, {"topic": "~/available"}],
		"availability_mode": "all",
		"unique_id": "__VIN___headlights",
		"dev": {
			"name": "__DEVICE__",
			"identifiers": ["phev-__VIN__"],
			"manufacturer": "Mitsubishi",
			"model": "Outlander PHEV"
		},
		"~": "__TOPIC__"}`,
		"%s/binary_sensor/%s_interiorlights/config": `{
		"device_class": "light",
		"name": "Interior Lights",
		"icon": "mdi:lightbulb",
		"state_topic": "~/lights/interior",
		"payload_off": "off",
		"payload_on": "on",
		"availability": [{"topic": "~/gateway/available"}, {"topic": "~/available"}],
		"availability_mode": "all",
		"unique_id": "__VIN___interiorlights",
		"dev": {
			"name": "__DEVICE__",
			"identifiers": ["phev-__VIN__"],
			"manufacturer": "Mitsubishi",
			"model": "Outlander PHEV"
		},
		"~": "__TOPIC__"}`,
		"%s/binary_sensor/%s_hazardlights/config": `{
		"device_class": "light",
		"name": "Hazard Lights",
		"icon": "mdi:hazard-lights",
		"state_topic": "~/lights/hazard",
		"payload_off": "off",
		"payload_on": "on",
		"availability": [{"topic": "~/gateway/available"}, {"topic": "~/available"}],
		"availability_mode": "all",
		"unique_id": "__VIN___hazardlights",
		"dev": {
			"name": "__DEVICE__",
			"identifiers": ["phev-__VIN__"],
			"manufacturer": "Mitsubishi",
			"model": "Outlander PHEV"
		},
		"~": "__TOPIC__"}`,
		// Connection and WiFi: only unavailable with the gateway, so that
		// they show the car being away.
		"%s/binary_sensor/%s_connection/config": `{
		"device_class": "connectivity",
		"name": "Connection",
		"state_topic": "~/available",
		"payload_on": "online",
		"payload_off": "offline",
		"avty_t": "~/gateway/available",
		"unique_id": "__VIN___connection",
		"dev": {
			"name": "__DEVICE__",
			"identifiers": ["phev-__VIN__"],
			"manufacturer": "Mitsubishi",
			"model": "Outlander PHEV"
		},
		"~": "__TOPIC__"}`,
		// The gateway itself, from its MQTT will: tells "car away" from
		// "gateway down".
		"%s/binary_sensor/%s_gateway/config": `{
		"device_class": "connectivity",
		"name": "Gateway",
		"state_topic": "~/gateway/available",
		"payload_on": "online",
		"payload_off": "offline",
		"entity_category": "diagnostic",
		"unique_id": "__VIN___gateway",
		"dev": {
			"name": "__DEVICE__",
			"identifiers": ["phev-__VIN__"],
			"manufacturer": "Mitsubishi",
			"model": "Outlander PHEV"
		},
		"~": "__TOPIC__"}`,
		"%s/binary_sensor/%s_wifi/config": `{
		"device_class": "connectivity",
		"name": "WiFi",
		"state_topic": "~/wifi/state",
		"payload_on": "connected",
		"payload_off": "disconnected",
		"avty_t": "~/gateway/available",
		"entity_category": "diagnostic",
		"unique_id": "__VIN___wifi",
		"dev": {
			"name": "__DEVICE__",
			"identifiers": ["phev-__VIN__"],
			"manufacturer": "Mitsubishi",
			"model": "Outlander PHEV"
		},
		"~": "__TOPIC__"}`,
		"%s/sensor/%s_wifi_signal/config": `{
		"device_class": "signal_strength",
		"name": "WiFi signal",
		"state_topic": "~/wifi/signal",
		"unit_of_measurement": "dBm",
		"state_class": "measurement",
		"availability": [
			{"topic": "~/gateway/available"},
			{"topic": "~/wifi/state", "payload_available": "connected", "payload_not_available": "disconnected"}],
		"availability_mode": "all",
		"entity_category": "diagnostic",
		"unique_id": "__VIN___wifi_signal",
		"dev": {
			"name": "__DEVICE__",
			"identifiers": ["phev-__VIN__"],
			"manufacturer": "Mitsubishi",
			"model": "Outlander PHEV"
		},
		"~": "__TOPIC__"}`,
		"%s/sensor/%s_last_connected/config": `{
		"device_class": "timestamp",
		"name": "Last connected",
		"avty_t": "~/gateway/available",
		"state_topic": "~/connection/last_connected",
		"entity_category": "diagnostic",
		"unique_id": "__VIN___last_connected",
		"dev": {
			"name": "__DEVICE__",
			"identifiers": ["phev-__VIN__"],
			"manufacturer": "Mitsubishi",
			"model": "Outlander PHEV"
		},
		"~": "__TOPIC__"}`,
		"%s/sensor/%s_last_disconnected/config": `{
		"device_class": "timestamp",
		"name": "Last disconnected",
		"avty_t": "~/gateway/available",
		"state_topic": "~/connection/last_disconnected",
		"entity_category": "diagnostic",
		"unique_id": "__VIN___last_disconnected",
		"dev": {
			"name": "__DEVICE__",
			"identifiers": ["phev-__VIN__"],
			"manufacturer": "Mitsubishi",
			"model": "Outlander PHEV"
		},
		"~": "__TOPIC__"}`,
		// General topics.
		// Without avty_t: the gateway can restart while the car is away.
		"%s/button/%s_restart/config": `{
		"device_class": "restart",
		"name": "Restart gateway",
		"command_topic": "~/restart",
		"payload_press": "restart",
		"entity_category": "config",
		"unique_id": "__VIN___restart",
		"dev": {
			"name": "__DEVICE__",
			"identifiers": ["phev-__VIN__"],
			"manufacturer": "Mitsubishi",
			"model": "Outlander PHEV"
		},
		"~": "__TOPIC__"}`,
		"%s/button/%s_reconnect_wifi/config": `{
		"name": "Reconnect to car",
		"icon": "mdi:car-connected",
		"command_topic": "~/connection",
		"payload_press": "restart",
		"avty_t": "~/gateway/available",
		"unique_id": "__VIN___restart_wifi",
		"dev": {
			"name": "__DEVICE__",
			"identifiers": ["phev-__VIN__"],
			"manufacturer": "Mitsubishi",
			"model": "Outlander PHEV"
		},
		"~": "__TOPIC__"}`,
	}
	addExtraSensorDiscovery(discoveryData, m.extraSensors)
	addDiscovery(discoveryData, commandResultEntities, m.climateGuard || m.extraCommands)
	addDiscovery(discoveryData, extraCommandEntities, m.extraCommands)
	// Entity names go without the device name: Home Assistant puts it in
	// front of them itself.
	mappings := map[string]string{
		"__DEVICE__": jsonString(device),
		"__VIN__":    vin,
		"__TOPIC__":  topic,
	}
	for topic, d := range discoveryData {
		topic = fmt.Sprintf(topic, discoveryPrefix, vin)
		for in, out := range mappings {
			d = strings.ReplaceAll(d, in, out)
		}
		m.mqttPublish(topic, 1, true, d)
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
	mqttCmd.Flags().Bool("mqtt_disable_register_set_command", false, "Disable vechicle register setting via MQTT")
	mqttCmd.Flags().Bool("ha_discovery", true, "Enable Home Assistant MQTT discovery")
	mqttCmd.Flags().String("ha_discovery_prefix", "homeassistant", "Prefix for Home Assistant MQTT discovery")
	mqttCmd.Flags().String("ha_device_name", "", "Name of the car in Home Assistant (default PHEV <VIN>)")
	mqttCmd.Flags().Duration("update_interval", 5*time.Minute, "How often to request force updates")
	mqttCmd.Flags().Duration("wifi_restart_time", 0, "Attempt to restart Wifi if no connection for this long")
	mqttCmd.Flags().Duration("wifi_restart_retry_time", 2*time.Minute, "Interval to attempt Wifi restart")
	mqttCmd.Flags().String("wifi_restart_command", defaultWifiRestartCmd, "Command to restart Wifi connection to Phev")
	mqttCmd.Flags().String("wifi_interface", "wlan0", "WiFi interface for the car: its state goes to MQTT, and the web interface manages it")
	// web_password and web_settings_file only come from the config file, so
	// the password never shows up in the process list.
	mqttCmd.Flags().String("web_listen", "", "Serve the web interface on this address, e.g. 127.0.0.1:8080 (off if empty)")
	mqttCmd.Flags().Bool("key_resync", false, "Recover from the car's encoding complaints: resync to the key it expects, cap resends, ask for a new key")
	mqttCmd.Flags().Bool("climate_guard", false, "Start the climate like the official app: keep the car's climate timers, refuse when it can't run, confirm it started")
	mqttCmd.Flags().Bool("extra_commands", false, "Extra command buttons (Sync Car Clock), with their results in phev/command/result")
	mqttCmd.Flags().Bool("extra_sensors", false, "Publish extra status sensors: ignition, charge type, the car's timers, battery heater, ...")

	viper.BindPFlag("mqtt_server", mqttCmd.Flags().Lookup("mqtt_server"))
	viper.BindPFlag("mqtt_username", mqttCmd.Flags().Lookup("mqtt_username"))
	viper.BindPFlag("mqtt_password", mqttCmd.Flags().Lookup("mqtt_password"))
	viper.BindPFlag("mqtt_topic_prefix", mqttCmd.Flags().Lookup("mqtt_topic_prefix"))
	viper.BindPFlag("mqtt_disable_register_set_command", mqttCmd.Flags().Lookup("mqtt_disable_register_set_command"))
	viper.BindPFlag("ha_discovery", mqttCmd.Flags().Lookup("ha_discovery"))
	viper.BindPFlag("ha_discovery_prefix", mqttCmd.Flags().Lookup("ha_discovery_prefix"))
	viper.BindPFlag("ha_device_name", mqttCmd.Flags().Lookup("ha_device_name"))
	viper.BindPFlag("update_interval", mqttCmd.Flags().Lookup("update_interval"))
	viper.BindPFlag("wifi_interface", mqttCmd.Flags().Lookup("wifi_interface"))
	viper.BindPFlag("web_listen", mqttCmd.Flags().Lookup("web_listen"))
	viper.BindPFlag("extra_sensors", mqttCmd.Flags().Lookup("extra_sensors"))
	viper.BindPFlag("key_resync", mqttCmd.Flags().Lookup("key_resync"))
	viper.BindPFlag("climate_guard", mqttCmd.Flags().Lookup("climate_guard"))
	viper.BindPFlag("extra_commands", mqttCmd.Flags().Lookup("extra_commands"))
	// Config file only, like web_password; turning the login off must be a
	// deliberate choice.
	viper.SetDefault("web_auth", true)
	viper.BindPFlag("wifi_restart_time", mqttCmd.Flags().Lookup("wifi_restart_time"))
	viper.BindPFlag("wifi_restart_retry_time", mqttCmd.Flags().Lookup("wifi_restart_retry_time"))
	viper.BindPFlag("wifi_restart_command", mqttCmd.Flags().Lookup("wifi_restart_command"))
}
