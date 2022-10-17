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
	Settings    *protocol.Settings
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
	log.Debugf("%%PHEV_EMULATOR_START%% Started PHEV emulator, address=%s", c.address)
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
	c := &Car{
		Registers: defaultRegisters,
		Settings:  &protocol.Settings{},
	}
	for _, o := range opts {
		o(c)
	}
	for _, s := range defaultSettings {
		setting, err := hex.DecodeString(s)
		if err != nil {
			return nil, err
		}
		if err := c.Settings.FromRegister(setting); err != nil {
			return nil, err
		}
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

var defaultSettings = []string{
	"023a003b003c0000",
	"02e201e301640e00",
	"02e501a61ea71e00",
	"026b0e2c002d0000",
	"022e006f00300000",
	"0231007206730600",
	"02b4003506360e00",
	"02370e3806390000",
	"023a003b003c0000",
	"024106420603fe00",
	"02c41e850e861e00",
	"02473ec81e093f00",
	"02ca018bfe4c4300",
	"024d1e0e064f0600",
	"0210121106d20100",
	"02d301d401551e00",
	"02161ed701d80100",
	"0219061a23db0100",
	"02dc015d065e0600",
	"021f00600ee10100",
	"02e801e9016a3e00",
}
