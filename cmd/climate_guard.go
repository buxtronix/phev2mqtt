// Parts of this file are adapted from corautem/phev2mqtt, which adapted them
// from PHEV Link (phev-remote-plus, https://github.com/Anwo91/phev-remote-plus,
// core/model/CarCommand.kt), Copyright (C) 2026 Anwo91 and contributors, from
// its source as published on 14 October 2026 under the GNU General Public
// License version 3 or later. See README.md, Credits.

package cmd

import (
	"bytes"
	"fmt"
	"sync"
	"time"

	"github.com/buxtronix/phev2mqtt/client"
	"github.com/buxtronix/phev2mqtt/protocol"
	log "github.com/sirupsen/logrus"
)

// Climate guard (climate_guard): starts the climate the way the official
// app does. On 2014-2017 cars the climate write echoes the car's own climate
// timer registers instead of overwriting them; a start is refused, with a
// reason, while the car is switched on or quick charging (the car would
// accept it and do nothing); and a start is confirmed from the car's
// registers. Results go to phev/command/result.

const (
	regRequestUpdate = 0x06 // [3]: send all registers
	regClimateSets   = 0x22 // MY14 climate timer presets

	commandResultTopic = "/command/result"
)

var (
	// climateRegisterWait is how long a MY14 climate start waits for the
	// timer registers it echoes, after asking the car for them.
	climateRegisterWait = 5 * time.Second
	// climateConfirmWait is how long a climate start waits for the car to
	// report the climate running.
	climateConfirmWait = 30 * time.Second
)

// regCache holds the latest value of each register in the current session,
// and when the car sent it.
type regCache struct {
	mu   sync.Mutex
	data map[byte][]byte
	at   map[byte]time.Time
}

func (r *regCache) reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.data = map[byte][]byte{}
	r.at = map[byte]time.Time{}
}

func (r *regCache) put(register byte, data []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.data == nil {
		r.data = map[byte][]byte{}
		r.at = map[byte]time.Time{}
	}
	r.data[register] = append([]byte(nil), data...)
	r.at[register] = time.Now()
}

func (r *regCache) get(register byte) ([]byte, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	d, ok := r.data[register]
	return d, ok
}

// since returns a register's value if the car sent it after t.
func (r *regCache) since(register byte, t time.Time) ([]byte, bool) {
	d, _, ok := r.sinceAt(register, t)
	return d, ok
}

// sinceAt is since, with when the car sent the value.
func (r *regCache) sinceAt(register byte, t time.Time) ([]byte, time.Time, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	d, ok := r.data[register]
	at := r.at[register]
	return d, at, ok && at.After(t)
}

// carWriter writes a register on the car: client.Client's SetRegister, or a
// fake in tests.
type carWriter interface {
	SetRegister(register byte, value []byte) error
}

func (m *mqttClient) reportCommand(text string) {
	text = time.Now().Format("2006-01-02 15:04") + " " + text
	log.Infof("Command: %s", text)
	m.publish(commandResultTopic, text)
}

func climateLabel(mode, duration byte) string {
	if mode == 0 {
		return "Climate off"
	}
	names := map[byte]string{1: "Cool", 2: "Heat", 3: "Windscreen"}
	return fmt.Sprintf("%s %d min", names[mode], 10*(int(duration)+1))
}

// climateRefusal is why the car would accept a climate start and do nothing.
func (m *mqttClient) climateRefusal() string {
	if d, ok := m.regs.get(protocol.ACOperStatusRegister); ok && len(d) >= 1 && (d[0] == 3 || d[0] == 4) {
		return "the car only runs the climate remotely when it is switched off"
	}
	quickD, _ := m.regs.get(protocol.QuickChargeStateRegister)
	quickC, _ := m.regs.get(protocol.QuickChargePhaseRegister)
	if (len(quickD) > 0 && quickD[0] == 3) || (len(quickC) > 0 && quickC[0] == 5) {
		return "the car doesn't run the climate while quick charging"
	}
	return ""
}

// my14ClimatePayload builds the MY14 climate register like the official app:
// the car's register 0x22 (6 bytes), the mode byte, then bytes 1..8 of
// register 0x19, which leaves the car's own climate timers as they are.
// Without those registers it falls back to phev2mqtt's original pattern.
func (m *mqttClient) my14ClimatePayload(car carWriter, modeByte byte) []byte {
	presets := func() ([]byte, []byte, bool) {
		r22, ok22 := m.regs.get(regClimateSets)
		r19, ok19 := m.regs.get(protocol.ClimateTimerProgRegister)
		return r22, r19, ok22 && ok19 && len(r22) >= 6 && len(r19) >= 9
	}
	r22, r19, ok := presets()
	if !ok {
		if err := car.SetRegister(regRequestUpdate, []byte{0x03}); err != nil {
			log.Debugf("Climate: requesting an update: %v", err)
		}
		for deadline := time.Now().Add(climateRegisterWait); !ok && time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
			r22, r19, ok = presets()
		}
	}
	d := bytes.Repeat([]byte{0xff}, 15)
	if ok {
		copy(d[0:6], r22[:6])
		copy(d[7:15], r19[1:9])
	} else {
		log.Infof("Climate: the car's climate timer registers are unknown, sending the original pattern")
		d[0], d[1] = 0, 0
	}
	d[6] = modeByte
	return d
}

// climateRunning waits for the car to report the climate running, in a
// register it sent after sent.
func (m *mqttClient) climateRunning(sent time.Time) bool {
	for deadline := time.Now().Add(climateConfirmWait); ; time.Sleep(200 * time.Millisecond) {
		if d, ok := m.regs.since(protocol.PreACStateRegister, sent); ok && len(d) >= 1 && d[0] == 2 {
			return true
		}
		if d, ok := m.regs.since(protocol.ACOperStatusRegister, sent); ok && len(d) >= 2 && d[1] == 1 {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
	}
}

// setClimateGuarded starts or stops the climate with the climate guard.
// It runs in its own goroutine, one command at a time.
func (m *mqttClient) setClimateGuarded(car carWriter, my client.ModelYear, mode, duration byte) {
	m.commandMu.Lock()
	defer m.commandMu.Unlock()
	what := climateLabel(mode, duration)
	if !m.state.isConnected() {
		m.reportCommand(what + ": refused, the car is not connected")
		return
	}
	if mode != 0 {
		if reason := m.climateRefusal(); reason != "" {
			m.reportCommand(what + ": refused, " + reason)
			return
		}
	}
	// The start is confirmed from what the car reports after it, not from
	// values left over from an earlier start.
	sent := time.Now()
	var err error
	switch my {
	case client.ModelYear14:
		if mode == 0 {
			// The official app only flips the switch to stop.
			err = car.SetRegister(protocol.SetACEnabledRegisterMY14, []byte{0x01})
		} else if err = car.SetRegister(protocol.SetACModeRegisterMY14, m.my14ClimatePayload(car, mode|duration<<4)); err == nil {
			err = car.SetRegister(protocol.SetACEnabledRegisterMY14, []byte{0x02})
		}
	case client.ModelYear18, client.ModelYear24:
		state := byte(0x02)
		if mode == 0 {
			state = 0x01
		}
		err = car.SetRegister(protocol.SetACModeRegisterMY18, []byte{state, mode, duration, 0x0})
	default:
		m.reportCommand(what + ": refused, the car's model year isn't known yet")
		return
	}
	if err != nil {
		m.reportCommand(what + ": failed, " + err.Error())
		return
	}
	if mode == 0 {
		m.reportCommand(what + ": done")
		return
	}
	if !m.climateRunning(sent) {
		m.reportCommand(what + ": the car accepted it but did not start the climate (a door open, or the battery low?)")
		return
	}
	m.reportCommand(what + ": done, the climate is running")
}
