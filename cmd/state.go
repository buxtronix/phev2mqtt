package cmd

import (
	"sync"
	"time"

	"github.com/buxtronix/phev2mqtt/client"
)

// gatewayState is the state shared by the car loop (handlePhev), the MQTT
// callbacks and the web interface, which all run in their own goroutines.
// Everything in it is guarded by mu.
type gatewayState struct {
	mu sync.Mutex

	// Last published value per topic, without the prefix ("/battery/level").
	values map[string]string
	// When a value last arrived from the car.
	updated time.Time

	// The current car client; nil until the first connection attempt.
	phev         *client.Client
	enabled      bool
	carConnected bool
	lastConnect  time.Time
	vin          string
	modelYear    client.ModelYear

	// carData: the car sent register data on the current connection, last
	// at lastData. available is what phev/available last said.
	carData   bool
	lastData  time.Time
	available bool
	// The car's WiFi interface; wifiKnown is false until watchWifi reports.
	wifi      bool
	wifiKnown bool

	// Closed and replaced on every change, see changes().
	changed chan struct{}
}

func newGatewayState() *gatewayState {
	return &gatewayState{
		values:  map[string]string{},
		enabled: true,
		changed: make(chan struct{}),
	}
}

// changes returns a channel that is closed at the next change.
func (s *gatewayState) changes() <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.changed
}

// notifyLocked wakes up everyone waiting in changes(); s.mu must be held.
func (s *gatewayState) notifyLocked() {
	close(s.changed)
	s.changed = make(chan struct{})
}

func (s *gatewayState) notify() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.notifyLocked()
}

func (s *gatewayState) setValue(topic, payload string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values[topic] = payload
	s.updated = time.Now()
	s.notifyLocked()
}

func (s *gatewayState) value(topic string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.values[topic]
}

func (s *gatewayState) car() *client.Client {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.phev
}

func (s *gatewayState) setCar(c *client.Client) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.phev = c
}

func (s *gatewayState) isEnabled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.enabled
}

func (s *gatewayState) setEnabled(enabled bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.enabled = enabled
	s.notifyLocked()
}

// setConnected records the car connecting (with its model year, known once
// Start returned) or disconnecting.
func (s *gatewayState) setConnected(connected bool, my client.ModelYear) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.carConnected = connected
	s.carData = false
	if connected {
		s.modelYear = my
	} else {
		s.lastConnect = time.Now()
	}
	s.notifyLocked()
}

func (s *gatewayState) setWifiUp(up bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.wifi = up
	s.wifiKnown = true
	s.notifyLocked()
}

// wifiUp reports whether the car's WiFi is connected; known is false when
// nothing watches it.
func (s *gatewayState) wifiUp() (up, known bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.wifi, s.wifiKnown
}

func (s *gatewayState) isConnected() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.carConnected
}

// gotCarData records register data arriving from the car.
func (s *gatewayState) gotCarData() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.carData = true
	s.lastData = time.Now()
}

// sinceCarData is how long ago the car last sent register data.
func (s *gatewayState) sinceCarData() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return time.Since(s.lastData)
}

// carAvailable reports whether the car counts as available: connected,
// with data on this connection, over WiFi that is up (when watched).
func (s *gatewayState) carAvailable() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.enabled && s.carConnected && s.carData && (s.wifi || !s.wifiKnown)
}

// setAvailable records what phev/available says and reports whether that
// changed.
func (s *gatewayState) setAvailable(available bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.available == available {
		return false
	}
	s.available = available
	s.notifyLocked()
	return true
}

func (s *gatewayState) isAvailable() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.available
}

func (s *gatewayState) sinceLastConnect() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return time.Since(s.lastConnect)
}

// getModelYear is the model year the car reported when it last connected.
func (s *gatewayState) getModelYear() client.ModelYear {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.modelYear
}

func (s *gatewayState) setVIN(vin string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.vin = vin
	s.notifyLocked()
}

func (s *gatewayState) getVIN() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.vin
}

// stateSnapshot is a copy of gatewayState for the web interface.
type stateSnapshot struct {
	Enabled      bool              `json:"enabled"`
	CarConnected bool              `json:"car_connected"`
	Available    bool              `json:"available"`
	LastConnect  *time.Time        `json:"last_connect,omitempty"`
	Updated      *time.Time        `json:"updated,omitempty"`
	VIN          string            `json:"vin,omitempty"`
	ModelYear    string            `json:"model_year,omitempty"`
	Values       map[string]string `json:"values"`
}

var modelYearNames = map[client.ModelYear]string{
	client.ModelYear14: "MY14",
	client.ModelYear18: "MY18",
	client.ModelYear24: "MY24",
}

func (s *gatewayState) snapshot() stateSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap := stateSnapshot{
		Enabled:      s.enabled,
		CarConnected: s.carConnected,
		Available:    s.available,
		VIN:          s.vin,
		ModelYear:    modelYearNames[s.modelYear],
		Values:       make(map[string]string, len(s.values)),
	}
	for k, v := range s.values {
		snap.Values[k] = v
	}
	if !s.lastConnect.IsZero() {
		t := s.lastConnect
		snap.LastConnect = &t
	}
	if !s.updated.IsZero() {
		t := s.updated
		snap.Updated = &t
	}
	return snap
}
