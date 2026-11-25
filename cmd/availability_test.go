package cmd

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/buxtronix/phev2mqtt/client"
)

func (f *fakeBroker) get(topic string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.published[topic]
}

func shortGrace(t *testing.T, d time.Duration) {
	old := disconnectGrace
	disconnectGrace = d
	t.Cleanup(func() { disconnectGrace = old })
}

func waitPublished(t *testing.T, b *fakeBroker, topic, want string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for b.get(topic) != want {
		if time.Now().After(deadline) {
			t.Fatalf("%s = %q, want %q", topic, b.get(topic), want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestAvailableNeedsData(t *testing.T) {
	shortGrace(t, 20*time.Millisecond)
	m, b := newTestClient("phev")
	m.state.setConnected(true, client.ModelYear18)
	m.updateAvailable("test")
	if got := b.get("phev/available"); got != "" {
		t.Errorf("phev/available = %q when connected without data, want nothing", got)
	}
	m.carData()
	if got := b.get("phev/available"); got != "online" {
		t.Errorf("phev/available = %q after data, want online", got)
	}

	// Gone for longer than the grace.
	m.state.setConnected(false, client.ModelYearUnknown)
	m.carDisconnected()
	waitPublished(t, b, "phev/available", "offline")

	// Connected again, but online only with data.
	m.state.setConnected(true, client.ModelYear18)
	m.updateAvailable("test")
	if got := b.get("phev/available"); got != "offline" {
		t.Errorf("phev/available = %q when reconnected without data, want offline", got)
	}
	m.carData()
	if got := b.get("phev/available"); got != "online" {
		t.Errorf("phev/available = %q after data, want online", got)
	}
}

func TestAvailableSurvivesQuickReconnect(t *testing.T) {
	shortGrace(t, 50*time.Millisecond)
	m, b := newTestClient("phev")
	m.state.setConnected(true, client.ModelYear18)
	m.carData()
	m.state.setConnected(false, client.ModelYearUnknown)
	m.carDisconnected()
	m.state.setConnected(true, client.ModelYear18)
	m.carData()
	time.Sleep(100 * time.Millisecond)
	if got := b.get("phev/available"); got != "online" {
		t.Errorf("phev/available = %q after a quick reconnect, want online", got)
	}
}

func TestAvailableFollowsWifi(t *testing.T) {
	m, b := newTestClient("phev")
	cl, err := client.New()
	if err != nil {
		t.Fatal(err)
	}
	m.state.setCar(cl)
	m.state.setWifiUp(true)
	m.state.setConnected(true, client.ModelYear18)
	m.carData()
	if got := b.get("phev/available"); got != "online" {
		t.Fatalf("phev/available = %q, want online", got)
	}

	m.state.setWifiUp(false)
	m.wifiChanged(false)
	if got := b.get("phev/available"); got != "offline" {
		t.Errorf("phev/available = %q with the WiFi down, want offline", got)
	}
	if err := cl.SendMessage(nil); err == nil {
		t.Error("car connection still open with the WiFi down")
	}
	// Data still on its way from the closed connection changes nothing.
	m.carData()
	if got := b.get("phev/available"); got != "offline" {
		t.Errorf("phev/available = %q after late data, want offline", got)
	}
}

func TestStaleCar(t *testing.T) {
	m, _ := newTestClient("phev")
	m.updateInterval = time.Minute
	if !m.staleCar() {
		t.Error("never any data, but not stale")
	}
	m.carData()
	if m.staleCar() {
		t.Error("stale right after data")
	}
	m.state.mu.Lock()
	m.state.lastData = time.Now().Add(-61 * time.Second)
	m.state.mu.Unlock()
	if !m.staleCar() {
		t.Error("no data for longer than update_interval, but not stale")
	}
}

func TestConnectionOffMakesUnavailable(t *testing.T) {
	m, b := newTestClient("phev")
	m.state.setConnected(true, client.ModelYear18)
	m.carData()
	handle(t, m, client.ModelYear18, "phev/connection", "off")
	if got := b.get("phev/available"); got != "offline" {
		t.Errorf("phev/available = %q after connection off, want offline", got)
	}
}

type availabilityConfig struct {
	Topic        string `json:"avty_t"`
	Availability []struct {
		Topic string `json:"topic"`
	} `json:"availability"`
	Mode string `json:"availability_mode"`
}

// Car entities need both the car and the gateway: phev/available is not
// the will, so it stays online when the gateway dies.
func TestDiscoveryCarEntitiesNeedGateway(t *testing.T) {
	m, b := newTestClient("phev")
	m.extraSensors = true
	m.publishHomeAssistantDiscovery("VIN1", "phev")
	cars := 0
	for topic, payload := range b.published {
		if payload == "" {
			continue // removes an entity
		}
		var c availabilityConfig
		if err := json.Unmarshal([]byte(payload), &c); err != nil {
			t.Fatalf("%s: %v", topic, err)
		}
		if c.Topic == "~/available" {
			t.Errorf("%s: only phev/available, not the gateway", topic)
		}
		var car, gateway bool
		for _, a := range c.Availability {
			car = car || a.Topic == "~/available"
			gateway = gateway || a.Topic == "~/gateway/available"
		}
		if car {
			cars++
			if !gateway || c.Mode != "all" {
				t.Errorf("%s: availability %+v, mode %q: want the gateway too, mode all", topic, c.Availability, c.Mode)
			}
		}
	}
	if cars < 15 {
		t.Errorf("%d car entities with availability, want all of them", cars)
	}
}
