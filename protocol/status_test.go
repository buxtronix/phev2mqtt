// Some test values are from PHEV Link (phev-remote-plus,
// https://github.com/Anwo91/phev-remote-plus) LogicTest.kt and
// ScheduleDecoderTest.kt, Copyright (C) 2026 Anwo91 and contributors, from
// its source as published on 14 October 2026 under the GNU General Public
// License version 3 or later. See README.md, Credits.

package protocol

import (
	"encoding/hex"
	"math/rand"
	"reflect"
	"testing"
	"time"
)

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// pack mirrors the official app's schedule packing: seven little-endian bit
// fields of width bits, Sunday first, unset days filled with unset.
func pack(fields map[int]int, width, byteLen, unset int) []byte {
	var value uint64
	for d := 0; d < 7; d++ {
		f, ok := fields[d]
		if !ok {
			f = unset
		}
		value |= uint64(f) << (width * d)
	}
	out := make([]byte, byteLen)
	for i := range out {
		out[i] = byte(value >> (8 * i))
	}
	return out
}

func localRFC3339(y, mo, d, h, mi, s int) string {
	return time.Date(y, time.Month(mo), d, h, mi, s, 0, time.Local).Format(time.RFC3339)
}

func TestStatusTrackerSingleRegisters(t *testing.T) {
	// A made-up VIN, so it can't match a real car.
	vin := "00" + hex.EncodeToString([]byte("PHEVTEST000000001")) + "0102"
	tests := []struct {
		name string
		my14 bool
		reg  byte
		data string
		want map[string]string
	}{
		{"power off", false, 0x1a, "0000", map[string]string{"/power/switch": "off"}},
		{"power acc", false, 0x1a, "0300", map[string]string{"/power/switch": "acc"}},
		// phev-remote-plus LogicTest: 0x1a=0400 is ignition ON.
		{"power on", false, 0x1a, "0400", map[string]string{"/power/switch": "on"}},
		{"power unknown", false, 0x1a, "07", map[string]string{"/power/switch": "unknown"}},
		{"battery too cold", false, 0x1d, "5a030000", map[string]string{"/battery/too_cold": "on"}},
		{"battery fine", false, 0x1d, "5a000100", map[string]string{"/battery/too_cold": "off"}},
		{"battery heater MY14", true, 0x02, "00010000", map[string]string{"/battery/heater": "on"}},
		{"battery heater not MY14", false, 0x02, "00010000", map[string]string{}},
		{"timer error flags", false, 0x1e, "0003", map[string]string{"/charge/timer_waiting": "off", "/charge/timer_failed": "on"}},
		{"charge timer on", false, 0x1b, "12", map[string]string{"/charge/timer": "on"}},
		{"charge timer off", false, 0x1b, "01", map[string]string{"/charge/timer": "off"}},
		{"screen in use", false, 0x26, "01", map[string]string{"/car/screen_in_use": "on"}},
		{"ev-ecu ok", false, 0x2c, "00", map[string]string{"/car/ev_ecu_error": "off"}},
		{"registration standby", false, 0x15, vin, map[string]string{"/car/registration_standby": "on"}},
		{"car clock", false, 0x12, "1a0a0e0c1e0002", map[string]string{"/car/clock": localRFC3339(2026, 10, 14, 12, 30, 0), "/car/clock_offset": "0"}},
		// Field log (2014 car): every climate timer slot unset, timer off.
		{"climate timer field log", false, 0x19, "01ffffffff07ffff1f", map[string]string{"/climate/timer": "off", "/climate/timer_schedule": "{}"}},
		// Field log (2014 car): every charge timer slot unset.
		{"charge schedule field log", false, 0x18, "ffffffff07ffff1fffffffff07ffff1f", map[string]string{"/charge/timer_schedule": "{}"}},
		// Not every car has an alarm, so 0x23[1] is not published.
		{"alarm not published", false, 0x23, "0002000202", map[string]string{}},
		{"unrelated register", false, 0x28, "41424300", map[string]string{}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tr := &StatusTracker{MY14: test.my14, Now: clockAt(2026, 10, 14, 12, 30, 0)}
			got := tr.Update(test.reg, mustHex(t, test.data))
			if !reflect.DeepEqual(got, test.want) {
				t.Errorf("got %v, want %v", got, test.want)
			}
		})
	}
}

func TestStatusTrackerSchedules(t *testing.T) {
	// Same programme as phev-remote-plus ScheduleDecoderTest: charge Monday
	// 22:30 -> 06:00 with full charge, climate Tuesday 07:00.
	var charge []byte
	charge = append(charge, pack(map[int]int{1: 22}, 5, 5, 31)...)
	charge = append(charge, pack(map[int]int{1: 3}, 3, 3, 7)...)
	charge = append(charge, pack(map[int]int{1: 6}, 5, 5, 31)...)
	charge = append(charge, pack(map[int]int{1: 0}, 3, 3, 7)...)
	climate := append([]byte{0x02}, pack(map[int]int{2: 7}, 5, 5, 31)...)
	climate = append(climate, pack(map[int]int{2: 0}, 3, 3, 7)...)

	tr := &StatusTracker{}
	if got, want := tr.Update(0x18, charge)["/charge/timer_schedule"], `{"mon":"22:30-06:00"}`; got != want {
		t.Errorf("charge schedule = %s, want %s", got, want)
	}
	if got, want := tr.Update(0x0e, []byte{0x02})["/charge/timer_schedule"], `{"mon":"22:30-06:00 full"}`; got != want {
		t.Errorf("charge schedule with full day = %s, want %s", got, want)
	}
	got := tr.Update(0x19, climate)
	if got["/climate/timer"] != "on" || got["/climate/timer_schedule"] != `{"tue":"07:00"}` {
		t.Errorf("climate = %v", got)
	}
}

func TestStatusTrackerFullDaysBeforeSchedule(t *testing.T) {
	tr := &StatusTracker{}
	if got := tr.Update(0x0e, []byte{0x02}); len(got) != 0 {
		t.Errorf("full-charge days without a schedule published %v", got)
	}
}

func TestStatusTrackerChargeType(t *testing.T) {
	tr := &StatusTracker{}
	if got := tr.Update(0x1e, mustHex(t, "0202")); got["/charge/type"] != "" {
		t.Errorf("charge type before 0x1f = %q, want unpublished", got["/charge/type"])
	}
	steps := []struct {
		reg  byte
		data string
		want string
	}{
		{0x1f, "000000", "none"},
		{0x1f, "011e00", "normal"},
		{0x1e, "0102", "trickle"},
		{0x1e, "0002", "unknown"},
		// CHAdeMO: 0x1f may read "not charging", the quick flags win.
		{0x1f, "000000", "none"},
		{0x0d, "03", "quick"},
		{0x0d, "00", "none"},
		{0x0c, "05", "quick"},
	}
	for i, s := range steps {
		if got := tr.Update(s.reg, mustHex(t, s.data))["/charge/type"]; got != s.want {
			t.Errorf("step %d (0x%02x=%s): charge type %q, want %q", i, s.reg, s.data, got, s.want)
		}
	}
}

func TestStatusTrackerReset(t *testing.T) {
	tr := &StatusTracker{}
	tr.Update(0x1f, mustHex(t, "011e00"))
	tr.Reset()
	if got := tr.Update(0x1e, mustHex(t, "0202")); got["/charge/type"] != "" {
		t.Errorf("charge type after Reset = %q, want unpublished", got["/charge/type"])
	}
}

func TestStatusTrackerCopiesData(t *testing.T) {
	tr := &StatusTracker{}
	data := mustHex(t, "011e00")
	tr.Update(0x1f, data)
	data[0] = 0
	if got := tr.Update(0x1e, mustHex(t, "0202"))["/charge/type"]; got != "normal" {
		t.Errorf("charge type = %q after caller reused its buffer, want normal", got)
	}
}

// The car's data is untrusted input: no register value, of any length,
// may make the tracker panic.
func TestStatusTrackerArbitraryInput(t *testing.T) {
	rnd := rand.New(rand.NewSource(1))
	tr := &StatusTracker{MY14: true}
	for round := 0; round < 20; round++ {
		for reg := 0; reg < 256; reg++ {
			for n := 0; n <= 40; n++ {
				data := make([]byte, n)
				rnd.Read(data)
				tr.Update(byte(reg), data)
			}
		}
	}
}

func TestChargePlugAndInteriorDecoding(t *testing.T) {
	for data, want := range map[string]bool{"0000": false, "0001": true, "0002": true, "0202": true} {
		r := &RegisterChargePlug{}
		r.Decode(&PhevMessage{Register: ChargePlugRegister, Data: mustHex(t, data)})
		if r.Connected != want {
			t.Errorf("0x1e=%s: connected %v, want %v", data, r.Connected, want)
		}
	}
	for data, want := range map[string]bool{"0000000201": true, "0000000202": false, "0000000203": true} {
		r := &RegisterLightStatus{}
		r.Decode(&PhevMessage{Register: LightStatusRegister, Data: mustHex(t, data)})
		if r.Interior != want {
			t.Errorf("0x23=%s: interior %v, want %v", data, r.Interior, want)
		}
	}
}

func clockAt(y, mo, d, h, mi, sec int) func() time.Time {
	return func() time.Time { return time.Date(y, time.Month(mo), d, h, mi, sec, 0, time.Local) }
}

func TestCarClockOffset(t *testing.T) {
	for _, test := range []struct {
		name, data, want string
		now              func() time.Time
	}{
		// Field value from the test car, 2026-11-26 20:45:41.
		{"on time", "1a0b1a142d2904", "0", clockAt(2026, 11, 26, 20, 45, 41)},
		{"ahead", "1a0b1a14302e04", "185", clockAt(2026, 11, 26, 20, 45, 41)},
		{"behind", "1a0b1a142d0004", "-41", clockAt(2026, 11, 26, 20, 45, 41)},
		// The car still on summer time after the change to winter time.
		{"an hour ahead", "1a0a19040000", "3600", clockAt(2026, 10, 25, 3, 0, 0)},
	} {
		t.Run(test.name, func(t *testing.T) {
			tr := &StatusTracker{Now: test.now}
			if got := tr.Update(TimeRegister, mustHex(t, test.data))["/car/clock_offset"]; got != test.want {
				t.Errorf("offset %q, want %q", got, test.want)
			}
		})
	}
}
