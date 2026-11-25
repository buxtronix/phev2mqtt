package cmd

import (
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/buxtronix/phev2mqtt/client"
	"github.com/buxtronix/phev2mqtt/protocol"
)

// fakeCar records register writes; onWrite can play the car's answer.
type fakeCar struct {
	mu      sync.Mutex
	writes  []string
	onWrite func(register byte, value []byte)
	err     error
}

func (f *fakeCar) SetRegister(register byte, value []byte) error {
	f.mu.Lock()
	f.writes = append(f.writes, fmt.Sprintf("%02x=%s", register, hex.EncodeToString(value)))
	f.mu.Unlock()
	if f.onWrite != nil {
		f.onWrite(register, value)
	}
	return f.err
}

func (f *fakeCar) got() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.writes...)
}

func guardClient(t *testing.T, regs map[byte]string) (*mqttClient, *fakeBroker) {
	t.Helper()
	m, b := newTestClient("phev")
	m.climateGuard = true
	m.state.setConnected(true, client.ModelYear14)
	m.regs.reset()
	for r, v := range regs {
		d, err := hex.DecodeString(v)
		if err != nil {
			t.Fatal(err)
		}
		m.regs.put(r, d)
	}
	return m, b
}

func shortWaits(t *testing.T) {
	oldReg, oldConfirm := climateRegisterWait, climateConfirmWait
	climateRegisterWait, climateConfirmWait = 50*time.Millisecond, 300*time.Millisecond
	t.Cleanup(func() { climateRegisterWait, climateConfirmWait = oldReg, oldConfirm })
}

// Register values from the MY14 test car (no climate timers programmed).
var my14Regs = map[byte]string{
	0x10: "000000",
	0x19: "01ffffffff07ffff1f",
	0x1a: "0000",
	0x22: "0000ff7fff01",
}

func TestClimateGuardMY14StartKeepsTimers(t *testing.T) {
	shortWaits(t)
	m, b := guardClient(t, my14Regs)
	car := &fakeCar{onWrite: func(register byte, value []byte) {
		if register == protocol.SetACEnabledRegisterMY14 && value[0] == 0x02 {
			m.regs.put(protocol.PreACStateRegister, []byte{0x02, 0, 0})
		}
	}}
	m.setClimateGuarded(car, client.ModelYear14, 0x2, 0x1) // heat, 20 min
	want := []string{
		// 0x22, the mode byte, 0x19[1:9]
		"02=0000ff7fff01" + "12" + "ffffffff07ffff1f",
		"04=02",
	}
	if got := car.got(); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("writes %v, want %v", got, want)
	}
	if got := b.get("phev/command/result"); !strings.HasSuffix(got, "Heat 20 min: done, the climate is running") {
		t.Errorf("result %q", got)
	}
}

func TestClimateGuardMY14StopOnlyFlipsTheSwitch(t *testing.T) {
	m, b := guardClient(t, my14Regs)
	car := &fakeCar{}
	m.setClimateGuarded(car, client.ModelYear14, 0, 0)
	if got := car.got(); len(got) != 1 || got[0] != "04=01" {
		t.Errorf("writes %v, want [04=01]", got)
	}
	if got := b.get("phev/command/result"); !strings.HasSuffix(got, "Climate off: done") {
		t.Errorf("result %q", got)
	}
}

func TestClimateGuardRefuses(t *testing.T) {
	for _, tc := range []struct {
		name, reg, value, want string
	}{
		{"switched on", "1a", "0400", "refused, the car only runs the climate remotely when it is switched off"},
		{"acc", "1a", "0300", "refused, the car only runs the climate remotely when it is switched off"},
		{"quick charging", "0d", "03", "refused, the car doesn't run the climate while quick charging"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			regs := map[byte]string{}
			for k, v := range my14Regs {
				regs[k] = v
			}
			r, _ := hex.DecodeString(tc.reg)
			regs[r[0]] = tc.value
			m, b := guardClient(t, regs)
			car := &fakeCar{}
			m.setClimateGuarded(car, client.ModelYear14, 0x1, 0x0)
			if got := car.got(); len(got) != 0 {
				t.Errorf("wrote %v, want nothing", got)
			}
			if got := b.get("phev/command/result"); !strings.HasSuffix(got, "Cool 10 min: "+tc.want) {
				t.Errorf("result %q", got)
			}
		})
	}
}

func TestClimateGuardNotConnected(t *testing.T) {
	m, b := guardClient(t, my14Regs)
	m.state.setConnected(false, client.ModelYearUnknown)
	car := &fakeCar{}
	m.setClimateGuarded(car, client.ModelYear14, 0x1, 0x0)
	if len(car.got()) != 0 || !strings.HasSuffix(b.get("phev/command/result"), "refused, the car is not connected") {
		t.Errorf("writes %v, result %q", car.got(), b.get("phev/command/result"))
	}
}

func TestClimateGuardNotStarted(t *testing.T) {
	shortWaits(t)
	m, b := guardClient(t, my14Regs)
	m.setClimateGuarded(&fakeCar{}, client.ModelYear14, 0x3, 0x0)
	if got := b.get("phev/command/result"); !strings.Contains(got, "Windscreen 10 min: the car accepted it but did not start the climate") {
		t.Errorf("result %q", got)
	}
}

func TestClimateGuardMY14WithoutRegisters(t *testing.T) {
	shortWaits(t)
	m, _ := guardClient(t, map[byte]string{})
	car := &fakeCar{}
	m.setClimateGuarded(car, client.ModelYear14, 0x1, 0x0)
	got := car.got()
	// Asks for the registers, then sends phev2mqtt's original pattern.
	if len(got) < 3 || got[0] != "06=03" || got[1] != "02=0000ffffffff01ffffffffffffffff" || got[2] != "04=02" {
		t.Errorf("writes %v", got)
	}
}

func TestClimateGuardMY18(t *testing.T) {
	shortWaits(t)
	m, _ := guardClient(t, map[byte]string{0x1a: "0000"})
	car := &fakeCar{}
	m.setClimateGuarded(car, client.ModelYear18, 0x2, 0x2)
	if got := car.got(); len(got) != 1 || got[0] != "1b=02020200" {
		t.Errorf("writes %v, want [1b=02020200]", got)
	}
}

func TestClimateGuardFromMQTT(t *testing.T) {
	shortWaits(t)
	m, b := guardClient(t, my14Regs)
	m.regs.put(protocol.ACOperStatusRegister, []byte{0x04, 0})
	// The handler hands the command to a goroutine, which refuses it: the
	// car is switched on.
	handle(t, m, client.ModelYear14, "phev/set/climate/heat", "10")
	deadline := time.Now().Add(2 * time.Second)
	for !strings.Contains(b.get("phev/command/result"), "refused") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := b.get("phev/command/result"); !strings.HasSuffix(got, "Heat 10 min: refused, the car only runs the climate remotely when it is switched off") {
		t.Errorf("result %q", got)
	}
}

func TestClimateGuardIgnoresOldRunningState(t *testing.T) {
	shortWaits(t)
	regs := map[byte]string{}
	for k, v := range my14Regs {
		regs[k] = v
	}
	regs[0x10] = "020000" // still running from an earlier start
	m, b := guardClient(t, regs)
	m.setClimateGuarded(&fakeCar{}, client.ModelYear14, 0x2, 0x0)
	if got := b.get("phev/command/result"); !strings.Contains(got, "did not start the climate") {
		t.Errorf("result %q, want unconfirmed: 0x10 was from before the start", got)
	}
}
