package cmd

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/buxtronix/phev2mqtt/client"
	"github.com/buxtronix/phev2mqtt/protocol"
)

func TestClockBytes(t *testing.T) {
	// A Friday.
	got := clockBytes(time.Date(2026, 11, 27, 7, 50, 41, 0, time.Local))
	if want := []byte{26, 11, 27, 7, 50, 41, 5, 0}; string(got) != string(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestDescribeOffset(t *testing.T) {
	for d, want := range map[time.Duration]string{
		0:                 "on time",
		-29 * time.Second: "on time",
		45 * time.Second:  "45 s ahead",
		-41 * time.Second: "41 s behind",
		185 * time.Second: "3 min ahead",
		time.Hour:         "60 min ahead",
		-2 * time.Hour:    "2.0 h behind",
	} {
		if got := describeOffset(d); got != want {
			t.Errorf("%v: got %q, want %q", d, got, want)
		}
	}
}

func shortClockWait(t *testing.T) {
	old := clockConfirmWait
	clockConfirmWait = 300 * time.Millisecond
	t.Cleanup(func() { clockConfirmWait = old })
}

func TestSyncClock(t *testing.T) {
	shortClockWait(t)
	// The car's clock, 3 minutes ahead before the sync.
	m, b := guardClient(t, map[byte]string{})
	m.regs.put(protocol.TimeRegister, clockBytes(time.Now().Add(3 * time.Minute))[:7])
	car := &fakeCar{onWrite: func(register byte, value []byte) {
		if register == regRequestUpdate {
			// The car reports the clock it was just given.
			m.regs.put(protocol.TimeRegister, clockBytes(time.Now())[:7])
		}
	}}
	m.syncClock(car)
	got := car.got()
	if len(got) != 2 || !strings.HasPrefix(got[0], "05=") || len(got[0]) != 3+16 || got[1] != "06=03" {
		t.Errorf("writes %v", got)
	}
	if r := b.get("phev/command/result"); !strings.HasSuffix(r, "Sync car clock: done, the car's clock is on time (it was 3 min ahead)") {
		t.Errorf("result %q", r)
	}
}

func TestSyncClockStillOff(t *testing.T) {
	shortClockWait(t)
	m, b := guardClient(t, map[byte]string{})
	car := &fakeCar{onWrite: func(register byte, value []byte) {
		if register == regRequestUpdate {
			m.regs.put(protocol.TimeRegister, clockBytes(time.Now().Add(-time.Hour))[:7])
		}
	}}
	m.syncClock(car)
	if r := b.get("phev/command/result"); !strings.HasSuffix(r, "the car accepted it, but its clock is still 60 min behind") {
		t.Errorf("result %q", r)
	}
}

func TestSyncClockUnconfirmed(t *testing.T) {
	shortClockWait(t)
	m, b := guardClient(t, map[byte]string{})
	m.syncClock(&fakeCar{})
	if r := b.get("phev/command/result"); !strings.HasSuffix(r, "sent, but the car did not report its clock") {
		t.Errorf("result %q", r)
	}
}

func TestSyncClockNotConnected(t *testing.T) {
	m, b := guardClient(t, map[byte]string{})
	m.state.setConnected(false, client.ModelYearUnknown)
	car := &fakeCar{}
	m.syncClock(car)
	if len(car.got()) != 0 || !strings.HasSuffix(b.get("phev/command/result"), "refused, the car is not connected") {
		t.Errorf("writes %v, result %q", car.got(), b.get("phev/command/result"))
	}
}

func TestSyncClockFromMQTT(t *testing.T) {
	shortClockWait(t)
	for _, on := range []bool{false, true} {
		m, b := guardClient(t, map[byte]string{})
		m.extraCommands = on
		handle(t, m, client.ModelYear14, "phev/set/sync_clock", "PRESS")
		deadline := time.Now().Add(2 * time.Second)
		for on && b.get("phev/command/result") == "" && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		if r := b.get("phev/command/result"); (r != "") != on {
			t.Errorf("extra_commands %v: result %q", on, r)
		}
	}
}

// The WiFi restart works over MQTT without the car, like the web button.
func TestWifiRestartFromMQTT(t *testing.T) {
	marker := t.TempDir() + "/ran"
	setConfig(t, "wifi_restart_command", "touch "+marker)
	m, _ := newTestClient("phev")
	m.handleIncomingMqtt(nil, fakeMessage{"phev/wifi/restart", ""})
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("wifi_restart_command did not run")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// Every button the web interface has is also in Home Assistant discovery.
func TestWebCommandsInDiscovery(t *testing.T) {
	m, b := newTestClient("phev")
	m.extraSensors, m.climateGuard, m.extraCommands = true, true, true
	m.publishHomeAssistantDiscovery("VIN123", "phev")
	var all strings.Builder
	for _, p := range b.published {
		all.WriteString(p)
	}
	for _, topic := range []string{"~/set/refresh", "~/wifi/restart", "~/set/climate/state", "~/set/sync_clock",
		"~/set/headlights", "~/set/parkinglights", "~/set/cancelchargetimer", "~/connection", "~/restart"} {
		if !strings.Contains(all.String(), `"command_topic": "`+topic+`"`) {
			t.Errorf("no Home Assistant entity for %s", topic)
		}
	}
}
