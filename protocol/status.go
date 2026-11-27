// Parts of this file are adapted from PHEV Link (phev-remote-plus,
// https://github.com/Anwo91/phev-remote-plus), Copyright (C) 2026 Anwo91 and
// contributors, from its source as published on 14 October 2026 under the
// GNU General Public License version 3 or later. Translated to Go and changed
// for phev2mqtt in October 2026. See README.md, Credits.

package protocol

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Registers read by StatusTracker that have no decoder of their own.
// Layouts follow phev-remote-plus (https://github.com/Anwo91/phev-remote-plus,
// core/model/RegisterDecoder.kt and ScheduleDecoder.kt), which were checked
// against field logs from a 2014 car.
const (
	QuickChargePhaseRegister = 0x0c // ==5 while quick charging (CHAdeMO)
	QuickChargeStateRegister = 0x0d // ==3 while quick charging (CHAdeMO)
	FullChargeDaysRegister   = 0x0e // bit d set = "full charge" on day d (Sunday 0)
	ChargeTimerProgRegister  = 0x18 // per-day charge windows, see chargeSchedule
	ClimateTimerProgRegister = 0x19 // [state][per-day start hours][per-day start minutes]
	ChargeTimerStateRegister = 0x1b // low nibble >= 2: the car's charge timer is on
	NavInUseRegister         = 0x26 // ==1 while the car's own screen is in use
	EVECUErrorRegister       = 0x2c // ==1 on an EV-ECU communication error
)

var dayNames = [7]string{"sun", "mon", "tue", "wed", "thu", "fri", "sat"}

// StatusTracker folds register notifications into the extra status values
// published by the MQTT gateway (--extra_sensors). Values that depend on
// several registers, like the charge type, are recomputed whenever one of
// them arrives.
type StatusTracker struct {
	// MY14 enables values whose layout is only known for 2014-2017 cars.
	MY14 bool
	// Now is the gateway's clock, compared with the car's; nil is time.Now.
	Now func() time.Time
	raw map[byte][]byte
}

// Reset forgets all registers, e.g. when a new session with the car starts.
func (t *StatusTracker) Reset() {
	t.raw = nil
}

// Update records a register notification and returns the status topics
// (relative to the MQTT topic prefix) whose values it determines.
func (t *StatusTracker) Update(register byte, data []byte) map[string]string {
	if t.raw == nil {
		t.raw = map[byte][]byte{}
	}
	t.raw[register] = append([]byte(nil), data...)
	out := map[string]string{}

	switch register {
	case ACOperStatusRegister:
		if len(data) >= 1 {
			out["/power/switch"] = powerSwitch(data[0])
		}
	case BatteryLevelRegister:
		if len(data) >= 2 {
			// The car asks for the cable to protect a cold drive battery.
			out["/battery/too_cold"] = onOff(data[1] == 3)
		}
	case BatteryWarningRegister:
		// On 2018+ cars this register is a "battery warning" with another layout.
		if t.MY14 && len(data) >= 2 {
			out["/battery/heater"] = onOff(data[1] == 1)
		}
	case ChargePlugRegister:
		if len(data) >= 2 {
			out["/charge/timer_waiting"] = onOff(data[1] == 1)
			out["/charge/timer_failed"] = onOff(data[1] == 3)
		}
	case ChargeTimerStateRegister:
		if len(data) >= 1 {
			out["/charge/timer"] = onOff(data[0]&0x0f >= 2)
		}
	case ChargeTimerProgRegister, FullChargeDaysRegister:
		if s, ok := t.chargeSchedule(); ok {
			out["/charge/timer_schedule"] = s
		}
	case ClimateTimerProgRegister:
		if len(data) >= 9 {
			out["/climate/timer"] = onOff(data[0] == 2)
			out["/climate/timer_schedule"] = climateSchedule(data)
		}
	case NavInUseRegister:
		if len(data) >= 1 {
			out["/car/screen_in_use"] = onOff(data[0] == 1)
		}
	case EVECUErrorRegister:
		if len(data) >= 1 {
			out["/car/ev_ecu_error"] = onOff(data[0] == 1)
		}
	case VINRegister:
		if len(data) >= 19 {
			out["/car/registration_standby"] = onOff(data[18] == 1)
		}
	case TimeRegister:
		if ts, ok := CarTime(data); ok {
			out["/car/clock"] = ts.Format(time.RFC3339)
			// How far the car's clock is ahead (+) or behind (-), in seconds.
			// The register arrives within a second of the car sending it.
			out["/car/clock_offset"] = fmt.Sprint(int64(ts.Sub(t.now()).Round(time.Second) / time.Second))
		}
	}

	switch register {
	case ChargeStatusRegister, ChargePlugRegister, QuickChargePhaseRegister, QuickChargeStateRegister:
		if ct, ok := t.chargeType(); ok {
			out["/charge/type"] = ct
		}
	}
	return out
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

func powerSwitch(b byte) string {
	switch b {
	case 0:
		return "off"
	case 3:
		return "acc"
	case 4:
		return "on"
	}
	return "unknown"
}

// chargeType tells how the car is charging. A CHAdeMO quick charge can read
// as "not charging" in 0x1f, so the quick-charge flags are checked first.
func (t *StatusTracker) chargeType() (string, bool) {
	if t.firstByte(QuickChargeStateRegister) == 3 || t.firstByte(QuickChargePhaseRegister) == 5 {
		return "quick", true
	}
	charging := t.firstByte(ChargeStatusRegister)
	if charging < 0 {
		return "", false
	}
	if charging != 1 {
		return "none", true
	}
	switch t.firstByte(ChargePlugRegister) {
	case 2:
		return "normal", true
	case 1:
		return "trickle", true
	}
	return "unknown", true
}

func (t *StatusTracker) firstByte(register byte) int {
	if b := t.raw[register]; len(b) > 0 {
		return int(b[0])
	}
	return -1
}

// chargeSchedule describes the car's own charge timer, one entry per
// programmed day, e.g. {"mon":"22:30-06:00 full"}, as JSON for HA attributes.
func (t *StatusTracker) chargeSchedule() (string, bool) {
	b := t.raw[ChargeTimerProgRegister]
	if len(b) < 16 {
		return "", false
	}
	full := t.firstByte(FullChargeDaysRegister)
	startH, startM := scheduleField(b, 0, 5, 31), scheduleField(b, 5, 3, 7)
	endH, endM := scheduleField(b, 8, 5, 31), scheduleField(b, 13, 3, 7)
	days := map[string]string{}
	for d := 0; d < 7; d++ {
		var parts []string
		start, sok := scheduleTime(startH[d], startM[d])
		end, eok := scheduleTime(endH[d], endM[d])
		if sok || eok {
			parts = append(parts, fmt.Sprintf("%s-%s", start, end))
		}
		if full >= 0 && full&(1<<d) != 0 {
			parts = append(parts, "full")
		}
		if len(parts) > 0 {
			days[dayNames[d]] = strings.Join(parts, " ")
		}
	}
	return mustJSON(days), true
}

// climateSchedule describes the car's own climate timer start time per
// programmed day, e.g. {"tue":"07:00"}, as JSON for HA attributes.
func climateSchedule(b []byte) string {
	hours, mins := scheduleField(b, 1, 5, 31), scheduleField(b, 6, 3, 7)
	days := map[string]string{}
	for d := 0; d < 7; d++ {
		if s, ok := scheduleTime(hours[d], mins[d]); ok {
			days[dayNames[d]] = s
		}
	}
	return mustJSON(days)
}

// scheduleField unpacks seven little-endian bit fields of width bits from
// b[offset:], one per day (Sunday first). An unset field holds unset.
func scheduleField(b []byte, offset, width int, unset int) [7]int {
	var value uint64
	byteLen := (7*width + 7) / 8
	for i := 0; i < byteLen; i++ {
		if offset+i < len(b) {
			value |= uint64(b[offset+i]) << (8 * i)
		}
	}
	var out [7]int
	for d := range out {
		out[d] = int((value >> (width * d)) & (1<<width - 1))
	}
	return out
}

// scheduleTime formats an hour and a minutes/10 field; unset hours (> 23)
// give false. Minutes are stored in 10-minute steps.
func scheduleTime(hour, min10 int) (string, bool) {
	if hour > 23 {
		return "--:--", false
	}
	if min10 > 5 {
		min10 = 0
	}
	return fmt.Sprintf("%02d:%02d", hour, min10*10), true
}

func (t *StatusTracker) now() time.Time {
	if t.Now != nil {
		return t.Now()
	}
	return time.Now()
}

// CarTime decodes [yy mm dd hh mi ss], the car's clock in local time, or
// false for an unset or out-of-range value.
func CarTime(b []byte) (time.Time, bool) {
	if len(b) < 6 {
		return time.Time{}, false
	}
	if b[1] < 1 || b[1] > 12 || b[2] < 1 || b[2] > 31 || b[3] > 23 || b[4] > 59 || b[5] > 59 {
		return time.Time{}, false
	}
	return time.Date(2000+int(b[0]), time.Month(b[1]), int(b[2]), int(b[3]), int(b[4]), int(b[5]), 0, time.Local), true
}

func mustJSON(v interface{}) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}
