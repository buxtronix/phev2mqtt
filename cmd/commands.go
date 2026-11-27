// The clock sync follows corautem/phev2mqtt, which took it from PHEV Link
// (phev-remote-plus, https://github.com/Anwo91/phev-remote-plus,
// core/model/CarCommand.kt), Copyright (C) 2026 Anwo91 and contributors, from
// its source as published on 14 October 2026 under the GNU General Public
// License version 3 or later. See README.md, Credits.

package cmd

import (
	"fmt"
	"time"

	"github.com/buxtronix/phev2mqtt/protocol"
	log "github.com/sirupsen/logrus"
)

// Extra commands (extra_commands): buttons for commands the car has no
// switch for, with their results in phev/command/result.

const regClockSync = 0x05 // write [yy mm dd hh mi ss dow 0]

// clockConfirmWait is how long a clock sync waits for the car to report its
// clock again.
var clockConfirmWait = 10 * time.Second

// extraCommandEntities are published with extra_commands.
var extraCommandEntities = []extraSensor{
	{"button", "sync_clock", "Sync Car Clock", "/set/sync_clock",
		`"icon": "mdi:clock-check-outline"`},
}

// commandResultEntities are published with climate_guard or extra_commands.
var commandResultEntities = []extraSensor{
	{"sensor", "last_command_result", "Last Command Result", commandResultTopic,
		`"icon": "mdi:history", "entity_category": "diagnostic"`},
}

// clockBytes encodes a time as the car's clock: [yy mm dd hh mi ss dow 0],
// Sunday = 0.
func clockBytes(t time.Time) []byte {
	return []byte{byte(t.Year() - 2000), byte(t.Month()), byte(t.Day()),
		byte(t.Hour()), byte(t.Minute()), byte(t.Second()), byte(t.Weekday()), 0}
}

// clockOffset is how far the car's clock in register 0x12 was ahead of the
// gateway's when it arrived.
func (m *mqttClient) clockOffset(after time.Time) (time.Duration, bool) {
	d, at, ok := m.regs.sinceAt(protocol.TimeRegister, after)
	if !ok {
		return 0, false
	}
	t, ok := protocol.CarTime(d)
	if !ok {
		return 0, false
	}
	return t.Sub(at).Round(time.Second), true
}

func describeOffset(d time.Duration) string {
	a := d
	if a < 0 {
		a = -a
	}
	if a < 30*time.Second {
		return "on time"
	}
	var s string
	switch {
	case a < 90*time.Second:
		s = fmt.Sprintf("%d s", int(a/time.Second))
	case a < 90*time.Minute:
		s = fmt.Sprintf("%d min", int(a.Round(time.Minute)/time.Minute))
	default:
		s = fmt.Sprintf("%.1f h", a.Hours())
	}
	if d > 0 {
		return s + " ahead"
	}
	return s + " behind"
}

// syncClock sets the car's clock to the gateway's and checks it from the
// clock the car reports afterwards.
func (m *mqttClient) syncClock(car carWriter) {
	m.commandMu.Lock()
	defer m.commandMu.Unlock()
	const what = "Sync car clock"
	if !m.state.isConnected() {
		m.reportCommand(what + ": refused, the car is not connected")
		return
	}
	was := ""
	if off, ok := m.clockOffset(time.Time{}); ok {
		was = " (it was " + describeOffset(off) + ")"
	}
	now := time.Now()
	if err := car.SetRegister(regClockSync, clockBytes(now)); err != nil {
		m.reportCommand(what + ": failed, " + err.Error())
		return
	}
	// The car reports its clock now and then; ask for it.
	if err := car.SetRegister(regRequestUpdate, []byte{0x03}); err != nil {
		log.Debugf("Clock: requesting an update: %v", err)
	}
	for deadline := time.Now().Add(clockConfirmWait); ; time.Sleep(200 * time.Millisecond) {
		if off, ok := m.clockOffset(now); ok {
			if describeOffset(off) == "on time" {
				m.reportCommand(what + ": done, the car's clock is on time" + was)
			} else {
				m.reportCommand(what + ": the car accepted it, but its clock is still " + describeOffset(off))
			}
			return
		}
		if time.Now().After(deadline) {
			m.reportCommand(what + ": sent, but the car did not report its clock" + was)
			return
		}
	}
}
