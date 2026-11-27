package cmd

import (
	"encoding/hex"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"

	"github.com/buxtronix/phev2mqtt/client"
	"github.com/buxtronix/phev2mqtt/protocol"
)

type fakeToken struct{}

func (fakeToken) Wait() bool                     { return true }
func (fakeToken) WaitTimeout(time.Duration) bool { return true }
func (fakeToken) Done() <-chan struct{} {
	c := make(chan struct{})
	close(c)
	return c
}
func (fakeToken) Error() error { return nil }

// fakeBroker records publishes; any other mqtt.Client method panics.
type fakeBroker struct {
	mqtt.Client
	mu        sync.Mutex
	published map[string]string
}

func (f *fakeBroker) Publish(topic string, qos byte, retained bool, payload interface{}) mqtt.Token {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.published[topic] = payload.(string)
	return fakeToken{}
}

func (f *fakeBroker) IsConnected() bool { return true }

func newTestClient(prefix string) (*mqttClient, *fakeBroker) {
	b := &fakeBroker{published: map[string]string{}}
	return &mqttClient{
		client:            b,
		prefix:            prefix,
		haDiscovery:       true,
		haDiscoveryPrefix: "homeassistant",
		climate:           new(climate),
		state:             newGatewayState(),
	}, b
}

// handle runs the handler with an unconnected car client attached, and
// returns the first message it tried to send to the car (nil if none).
func handle(t *testing.T, m *mqttClient, my client.ModelYear, topic, payload string) *protocol.PhevMessage {
	t.Helper()
	cl, err := client.New()
	if err != nil {
		t.Fatal(err)
	}
	m.state.setCar(cl)
	m.state.setConnected(true, my)
	done := make(chan struct{})
	go func() {
		defer close(done)
		m.handleIncomingMqtt(nil, fakeMessage{topic, payload})
	}()
	var sent *protocol.PhevMessage
	select {
	case sent = <-cl.Send:
	case <-done:
	case <-time.After(time.Second):
	}
	// Releases a SetRegister that is waiting for the car's ack.
	cl.Close()
	<-done
	return sent
}

func TestSetRegisterTopic(t *testing.T) {
	for _, tc := range []struct {
		name, prefix, topic string
		want                *byte
	}{
		{"valid", "phev", "phev/set/register/0a", ptr(0x0a)},
		{"prefix with slash", "home/phev", "home/phev/set/register/0a", ptr(0x0a)},
		{"empty register", "phev", "phev/set/register/", nil},
		{"two bytes", "phev", "phev/set/register/0a0b", nil},
		{"extra level", "phev", "phev/set/register/0a/x", nil},
		{"not hex", "phev", "phev/set/register/zz", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := newTestClient(tc.prefix)
			sent := handle(t, m, client.ModelYear18, tc.topic, "01")
			switch {
			case tc.want == nil && sent != nil:
				t.Errorf("sent register %02x, want nothing sent", sent.Register)
			case tc.want != nil && sent == nil:
				t.Errorf("nothing sent, want register %02x", *tc.want)
			case tc.want != nil && sent.Register != *tc.want:
				t.Errorf("sent register %02x, want %02x", sent.Register, *tc.want)
			}
		})
	}
}

func TestCommandsWithoutCarClient(t *testing.T) {
	for _, tc := range []struct{ topic, payload string }{
		{"phev/connection", "off"},
		{"phev/connection", "restart"},
		{"phev/set/headlights", "on"},
		{"phev/set/register/0a", "01"},
		{"phev/set/climate/heat", "on"},
		{"phev/settings/dump", ""},
	} {
		t.Run(tc.topic+"="+tc.payload, func(t *testing.T) {
			m, _ := newTestClient("phev")
			m.handleIncomingMqtt(nil, fakeMessage{tc.topic, tc.payload}) // must not panic
		})
	}
}

func TestClimateMode(t *testing.T) {
	for _, tc := range []struct {
		payload string
		want    []byte // start of the MY18 register data, nil if nothing may be sent
	}{
		{"heat", []byte{0x2, 0x2, 0x2, 0x0}},
		{"off", []byte{0x1, 0x0}}, // state off, mode off; the duration byte is not used
		{"foo", nil},
		{"mode", nil},
	} {
		t.Run(tc.payload, func(t *testing.T) {
			m, _ := newTestClient("phev")
			sent := handle(t, m, client.ModelYear18, "phev/set/climate/mode", tc.payload)
			switch {
			case tc.want == nil && sent != nil:
				t.Errorf("sent % x, want nothing sent", sent.Data)
			case tc.want != nil && sent == nil:
				t.Errorf("nothing sent, want % x", tc.want)
			case tc.want != nil && (len(sent.Data) < len(tc.want) || string(sent.Data[:len(tc.want)]) != string(tc.want)):
				t.Errorf("sent % x, want % x", sent.Data, tc.want)
			}
		})
	}
}

// Every state_topic in the Home Assistant discovery must be one that
// publishRegister, the car loop or watchWifi actually publishes.
func TestDiscoveryStateTopicsArePublished(t *testing.T) {
	published := map[string]bool{}
	for _, topic := range []string{
		"/door/locked", "/door/bonnet", "/door/boot", "/door/front_passenger",
		"/door/driver", "/door/rear_left", "/door/rear_right",
		"/battery/level", "/charge/remaining", "/charge/plug", "/charge/charging",
		"/lights/parking", "/lights/head", "/lights/interior", "/lights/hazard",
		"/available", "/gateway/available", "/connection/last_connected", "/connection/last_disconnected",
		"/wifi/state", "/wifi/signal",
	} {
		published["~"+topic] = true
	}
	for topic := range new(climate).mqttStates() {
		published["~"+topic] = true
	}

	// The extra sensors' topics, from the registers they are decoded from.
	tracker := &protocol.StatusTracker{MY14: true}
	for reg, data := range map[byte]string{
		0x02: "00010000", 0x12: "1a0a0e0c1e0002", 0x14: "1a0a0e12341e01",
		0x15: "00" + strings.Repeat("41", 17) + "0102", 0x18: "ffffffff07ffff1fffffffff07ffff1f",
		0x19: "01ffffffff07ffff1f", 0x1a: "0400", 0x1b: "12", 0x1d: "5a030000",
		0x1e: "0001", 0x1f: "011e00", 0x23: "0000000202", 0x26: "01", 0x2c: "00",
	} {
		raw, err := hex.DecodeString(data)
		if err != nil {
			t.Fatal(err)
		}
		for topic := range tracker.Update(reg, raw) {
			published["~"+topic] = true
		}
	}

	published["~"+commandResultTopic] = true // reportCommand

	m, b := newTestClient("phev")
	m.extraSensors = true
	m.climateGuard = true
	m.extraCommands = true
	m.publishHomeAssistantDiscovery("VIN123", "phev")
	if len(b.published) == 0 {
		t.Fatal("no discovery published")
	}
	for topic, payload := range b.published {
		if payload == "" {
			continue // removes an entity
		}
		var config struct {
			StateTopic string `json:"state_topic"`
		}
		if err := json.Unmarshal([]byte(payload), &config); err != nil {
			t.Errorf("%s: invalid JSON: %v", topic, err)
			continue
		}
		if config.StateTopic != "" && !published[config.StateTopic] {
			t.Errorf("%s: state_topic %s is never published", topic, config.StateTopic)
		}
	}
}

func ptr(b byte) *byte { return &b }
