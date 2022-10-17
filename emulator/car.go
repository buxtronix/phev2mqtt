package emulator

import (
	"encoding/hex"
	"github.com/buxtronix/phev2mqtt/protocol"
	log "github.com/sirupsen/logrus"
	"math/rand"
	"net"
	"time"
)

const listenAddress = ":8080"

// A Car represents a virtual emulated car.
type Car struct {
	// Registers are the current registers and settings for Car.
	Registers []protocol.Register
	// Settings are the vehicle settings.
	Settings    *Settings
	address     string
	connections []*Connection
}

// Begin starts the emulator.
func (c *Car) Begin() error {
	l, err := net.Listen("tcp4", c.address)
	if err != nil {
		return err
	}
	rand.Seed(time.Now().Unix())

	go func() {
		defer l.Close()
		for {
			conn, err := l.Accept()
			if err != nil {
				log.Errorf("Accept() error: %v")
				return
			}
			svc := NewConnection(conn, c)
			c.connections = append(c.connections, svc)

			go svc.Start()
		}
	}()
	return nil
}

// An Option configures the emulator.
type Option func(c *Car)

// AddressOption configures the address to the Phev.
func AddressOption(address string) func(*Car) {
	return func(c *Car) {
		c.address = address
	}
}

// NewCar returns a new Car. You get a Car! Everyone gets a Car!
func NewCar(opts ...Option) (*Car, error) {
	s, err := NewSettings()
	if err != nil {
		return nil, err
	}
	c := &Car{
		Registers: defaultRegisters,
		Settings:  s,
	}
	for _, o := range opts {
		o(c)
	}
	return c, nil
}

// Returns a generic register with the provided value.
func mustRegister(register byte, value string) protocol.Register {
	v, err := hex.DecodeString(value)
	if err != nil {
		panic(err)
	}
	return &protocol.RegisterGeneric{Reg: register, Value: v}
}

var defaultRegisters = []protocol.Register{
	mustRegister(0x06, "002D2D2D2D2D2D2D2D2D2D2D2D2D2D2D2D2D0100"),
	mustRegister(0x7, "00"),
	mustRegister(0x0b, "00"),
	mustRegister(0x0c, "01"),
	mustRegister(0x0d, "04"),
	mustRegister(0x0f, "00"),
	mustRegister(0x10, "000000"),
	mustRegister(0x11, "00"),
	mustRegister(0x12, "160a0712391805"),
	mustRegister(0x13, "00"),
	mustRegister(0x14, "00000000000000"),
	//	&protocol.RegisterVIN{VIN: "JMFXDGG2WJZ00048", Registrations: 2},
	mustRegister(0x15, "032E2E2E2E2E2E2E2E2E2E2E2E2E2E2E2E2E0100"),
	mustRegister(0x17, "01"),
	mustRegister(0x1a, "0300000000"),
	mustRegister(0x1b, "11"),
	mustRegister(0x1c, "03"),
	mustRegister(0x1d, "06000000"),
	mustRegister(0x1e, "0000"),
	mustRegister(0x1f, "00ffff"),
	mustRegister(0x21, "00"),
	mustRegister(0x22, "000000000000"),
	mustRegister(0x23, "0000000202"),
	mustRegister(0x24, "02000000000000000000"),
	mustRegister(0x25, "0e00ff"),
	mustRegister(0x26, "00"),
	mustRegister(0x27, "00"),
	mustRegister(0x28, "00"),
	mustRegister(0x29, "000200"),
	mustRegister(0x2C, "00"),
	mustRegister(0xC0, "30303532303232303030110000"),
	mustRegister(0x01, "0100"),
	mustRegister(0x02, "0100"),
	mustRegister(0x3, "011563"),
	mustRegister(0x04, "7d38b00183bd00017c70380100ffff0300ffff03"),
	mustRegister(0x5, "0100fe0700fe0700fe0700fe0700fe07"),
	mustRegister(0x06, "002D2D2D2D2D2D2D2D2D2D2D2D2D2D2D2D2D0100"),
	//	&protocol.RegisterVIN{VIN: "JMFXDGG2WJZ00048", Registrations: 2},
	mustRegister(0x15, "032E2E2E2E2E2E2E2E2E2E2E2E2E2E2E2E2E0100"),
	mustRegister(0x2A, "00"),
	mustRegister(0x2C, "00"),
	mustRegister(0x3, "011563"),
	&protocol.RegisterBatteryWarning{Warning: 0},
	&protocol.RegisterWIFISSID{SSID: "REMOTEc0ffee"},
}
