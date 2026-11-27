package cmd

import (
	"errors"
	"strings"
	"testing"
	"time"
)

type retainedMessage struct{ fakeMessage }

func (retainedMessage) Retained() bool { return true }

// stubExec replaces execSelf; the returned channel gets a value per restart.
func stubExec(t *testing.T) <-chan struct{} {
	t.Helper()
	called := make(chan struct{}, 4)
	old := execSelf
	execSelf = func() error {
		called <- struct{}{}
		return errors.New("stub")
	}
	t.Cleanup(func() { execSelf = old })
	return called
}

func TestRestartTopic(t *testing.T) {
	called := stubExec(t)
	m, b := newTestClient("phev")
	setConfig(t, "mqtt_server", "")

	// Retained: would restart every new start again.
	m.handleIncomingMqtt(nil, retainedMessage{fakeMessage{"phev/restart", "restart"}})
	select {
	case <-called:
		t.Fatal("restarted for a retained message")
	case <-time.After(100 * time.Millisecond):
	}

	// Works without a car connection too.
	m.handleIncomingMqtt(nil, fakeMessage{"phev/restart", "restart"})
	select {
	case <-called:
	case <-time.After(time.Second):
		t.Fatal("not restarted")
	}
	if got := b.published["phev/available"]; got != "offline" {
		t.Errorf("phev/available = %q before restarting, want offline", got)
	}
	if got := b.published["phev/gateway/available"]; got != "offline" {
		t.Errorf("phev/gateway/available = %q before restarting, want offline", got)
	}
	// The stub failed, so the gateway must carry on.
	waitEnabled(t, m)
}

// waitEnabled waits for restart to give up, as it does when execSelf fails.
func waitEnabled(t *testing.T, m *mqttClient) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for !m.state.isEnabled() || restarting.Load() {
		if time.Now().After(deadline) {
			t.Fatal("car connection still disabled after a failed restart")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestWebRestart(t *testing.T) {
	called := stubExec(t)
	ws, m := newTestWeb(t, "")
	setConfig(t, "mqtt_server", "")
	rr := request(t, ws, "POST", "/api/command", `{"command":"restart"}`, "", true)
	if rr.Code != 202 {
		t.Fatalf("status %d: %s", rr.Code, rr.Body)
	}
	select {
	case <-called:
	case <-time.After(2 * time.Second):
		t.Fatal("not restarted")
	}
	waitEnabled(t, m)
}

func TestDiscoveryRestartButton(t *testing.T) {
	m, b := newTestClient("phev")
	m.publishHomeAssistantDiscovery("VIN1", "phev")
	d := b.published["homeassistant/button/VIN1_restart/config"]
	if !strings.Contains(d, `"command_topic": "~/restart"`) || strings.Contains(d, "avty_t") {
		t.Errorf("restart button = %s; want ~/restart without an availability topic", d)
	}
}
