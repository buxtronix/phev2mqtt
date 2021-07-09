package protocol

import (
	"encoding/hex"
	"fmt"
)

const (
	CmdOutPingReq = 0xf3
	CmdInPingResp = 0x3f

	CmdOutSend = 0xf6
	CmdInResp  = 0x6f

	CmdInMy18StartReq   = 0x5e
	CmdOutMy18StartResp = 0xe5

	CmdInUnkn1  = 0x4e
	CmdOutUnkn1 = 0xe4

	CmdInBadEncoding = 0xbb
	CmdInUnkn3       = 0xcc

	CmdInStartResp      = 0x2f
	CmdOutStartSendMy18 = 0xf2

	CmdInUnkn4 = 0x2e
)

const (
	Request byte = 0x0
	Ack     byte = 0x1
)

var ackStr = map[byte]string{
	0x0: "REQ",
	0x1: "ACK",
}

var messageStr = map[byte]string{
	0xf3: "PingReq",
	0x3f: "PingResp",
	0xf6: "SendCmd",
	0x6f: "RespCmd",
	0xe5: "StartResp18",
	0x5e: "StartReq18",
	0xf2: "StartSend",
	0x2f: "StartResp",
}

type PhevMessage struct {
	Type     byte
	Length   byte
	Ack      byte
	Register byte
	Data     []byte
	Checksum byte
	Xor      byte
	Original []byte
	Reg      Register
}

func (p *PhevMessage) ShortForm() string {
	switch p.Type {
	case CmdInPingResp:
		return fmt.Sprintf("PING RESP     (id %x)", p.Register)

	case CmdOutPingReq:
		return fmt.Sprintf("PING REQ      (id %x)", p.Register)

	case CmdOutStartSendMy18:
		return fmt.Sprintf("START SEND18  (orig  %s)", hex.EncodeToString(p.Original))

	case CmdInStartResp:
		return fmt.Sprintf("START RESP18  (orig: %s)", hex.EncodeToString(p.Original))

	case CmdOutSend:
		if p.Ack == Ack {
			return fmt.Sprintf("REGISTER ACK  (reg 0x%02x data %s)", p.Register, hex.EncodeToString(p.Data))
		}
		return fmt.Sprintf("REGISTER SET  (reg 0x%02x data %s)", p.Register, hex.EncodeToString(p.Data))

	case CmdInResp:
		if p.Ack == Request {
			return fmt.Sprintf("REGISTER NTFY (reg 0x%02x data %s)", p.Register, hex.EncodeToString(p.Data))
		}
		return fmt.Sprintf("REGISTER SETACK (reg 0x%02x data %s)", p.Register, hex.EncodeToString(p.Data))

	case CmdInMy18StartReq:
		return fmt.Sprintf("START RECV18  (orig %s)", hex.EncodeToString(p.Original))

	case CmdOutMy18StartResp:
		return fmt.Sprintf("START SEND    (orig %s)", hex.EncodeToString(p.Original))

	case CmdInBadEncoding:
		return fmt.Sprintf("BAD ENCODING  (exp: 0x%02x)", p.Data[0])

	default:
		return p.String()
	}
}

func (p *PhevMessage) RawString() string {
	return hex.EncodeToString(p.Original)
}

func (p *PhevMessage) EncodeToBytes() []byte {
	length := byte(len(p.Data) + 3)
	data := []byte{
		p.Type,
		length,
		p.Ack,
		p.Register,
	}
	data = append(data, p.Data...)
	data = append(data, Checksum(data))
	msg := data
	if p.Xor > 0 {
		msg = XorMessageWith(data, p.Xor)
	}
	return msg
}

func (p *PhevMessage) DecodeFromBytes(data []byte) error {
	if len(data) < 4 {
		return fmt.Errorf("invalid packet length")
	}
	data, xor, _ := ValidateAndDecodeMessage(data)
	if len(data) < 4 || len(data) < int(data[1]+2) {
		return fmt.Errorf("invalid packet length")
	}
	p.Type = data[0]
	p.Length = data[1] + 2
	p.Register = data[3]
	p.Data = data[4 : p.Length-1]
	p.Checksum = data[p.Length-1]
	p.Ack = data[2]
	p.Xor = xor
	p.Original = data
	if p.Type == CmdInResp && p.Ack == Request {
		switch p.Register {
		case VINRegister:
			p.Reg = &RegisterVIN{}
		case ECUVersionRegister:
			p.Reg = &RegisterECUVersion{}
		case BatteryLevelRegister:
			p.Reg = &RegisterBatteryLevel{}
		case BatteryWarningRegister:
			p.Reg = &RegisterBatteryWarning{}
		case DoorStatusRegister:
			p.Reg = &RegisterDoorStatus{}
		case ChargeStatusRegister:
			p.Reg = &RegisterChargeStatus{}
		case ACOperStatusRegister:
			p.Reg = &RegisterACOperStatus{}
		case ACModeRegister:
			p.Reg = &RegisterACMode{}
		default:
			p.Reg = &RegisterGeneric{}
		}
		p.Reg.Decode(p)
	}

	return nil
}

func (p *PhevMessage) String() string {
	return fmt.Sprintf(
		`Cmd: 0x%x (%s) (len %d), Register 0x%x, Data: %s`,
		p.Type, messageStr[p.Type], p.Length, p.Register, hex.EncodeToString(p.Data))
}

func NewFromBytes(data []byte) []*PhevMessage {
	msgs := []*PhevMessage{}

	offset := 0
	for {
		dat, xor, rem := ValidateAndDecodeMessage(data[offset:])
		if len(dat) == 0 {
			offset += 1
			if offset >= len(data)-6 {
				break
			}
			continue
		}
		p := &PhevMessage{}
		err := p.DecodeFromBytes(dat)
		p.Xor = xor
		if err != nil {
			fmt.Printf("decode error: %v\n", err)
			break
		}
		msgs = append(msgs, p)
		if len(rem) < 1 {
			break
		}
		data = rem
	}
	return msgs
}

const (
	VINRegister            = 0x15
	ECUVersionRegister     = 0xc0
	BatteryLevelRegister   = 0x1d
	BatteryWarningRegister = 0x02
	DoorStatusRegister     = 0x24
	ChargeStatusRegister   = 0x1f
	ACOperStatusRegister   = 0x10
	ACModeRegister         = 0x1c
)

type Register interface {
	Decode(*PhevMessage)
	Raw() string
	String() string
	Register() byte
}

type RegisterGeneric struct {
	register byte
	raw      []byte
}

func (r *RegisterGeneric) Decode(m *PhevMessage) {
	r.register = m.Register
	r.raw = m.Data
}
func (r *RegisterGeneric) Raw() string {
	return hex.EncodeToString(r.raw)
}

func (r *RegisterGeneric) String() string {
	return fmt.Sprintf("g(0x%02x): %s", r.register, r.Raw())
}

func (r *RegisterGeneric) Register() byte {
	return r.register
}

type RegisterVIN struct {
	VIN           string
	Registrations int
	raw           []byte
}

func (r *RegisterVIN) Decode(m *PhevMessage) {
	if m.Register != VINRegister {
		return
	}
	r.VIN = string(m.Data[1:17])
	r.Registrations = int(m.Data[19])
	r.raw = m.Data
}

func (r *RegisterVIN) Raw() string {
	return hex.EncodeToString(r.raw)
}

func (r *RegisterVIN) String() string {
	return fmt.Sprintf("VIN: %s Registrations: %d", r.VIN, r.Registrations)
}

func (r *RegisterVIN) Register() byte {
	return VINRegister
}

type RegisterECUVersion struct {
	Version string
	raw     []byte
}

func (r *RegisterECUVersion) Decode(m *PhevMessage) {
	if m.Register != ECUVersionRegister {
		return
	}
	r.Version = string(m.Data[:9])
	r.raw = m.Data
}

func (r *RegisterECUVersion) Raw() string {
	return hex.EncodeToString(r.raw)
}

func (r *RegisterECUVersion) String() string {
	return fmt.Sprintf("ECU Version: %s", r.Version)
}

func (r *RegisterECUVersion) Register() byte {
	return ECUVersionRegister
}

type RegisterBatteryLevel struct {
	Level int
	raw   []byte
}

func (r *RegisterBatteryLevel) Decode(m *PhevMessage) {
	if m.Register != BatteryLevelRegister {
		return
	}
	r.Level = int(m.Data[0])
	r.raw = m.Data
}

func (r *RegisterBatteryLevel) Raw() string {
	return hex.EncodeToString(r.raw)
}

func (r *RegisterBatteryLevel) String() string {
	return fmt.Sprintf("Battery level: %d", r.Level)
}

func (r *RegisterBatteryLevel) Register() byte {
	return BatteryLevelRegister
}

type RegisterBatteryWarning struct {
	Warning int
	raw     []byte
}

func (r *RegisterBatteryWarning) Decode(m *PhevMessage) {
	if m.Register != BatteryWarningRegister {
		return
	}
	r.Warning = int(m.Data[2])
	r.raw = m.Data
}

func (r *RegisterBatteryWarning) Raw() string {
	return hex.EncodeToString(r.raw)
}

func (r *RegisterBatteryWarning) String() string {
	return fmt.Sprintf("Battery warning: %d", r.Warning)
}

func (r *RegisterBatteryWarning) Register() byte {
	return BatteryWarningRegister
}

type RegisterDoorStatus struct {
	Locked bool
	raw    []byte
}

func (r *RegisterDoorStatus) Decode(m *PhevMessage) {
	if m.Register != DoorStatusRegister {
		return
	}
	r.Locked = m.Data[0] == 0x1
	r.raw = m.Data
}

func (r *RegisterDoorStatus) Raw() string {
	return hex.EncodeToString(r.raw)
}

func (r *RegisterDoorStatus) String() string {
	if r.Locked {
		return "Doors locked."
	}
	return "Doors unlocked."
}

func (r *RegisterDoorStatus) Register() byte {
	return DoorStatusRegister
}

type RegisterChargeStatus struct {
	Charging  bool
	Remaining int // minutes.
	raw       []byte
}

func (r *RegisterChargeStatus) Decode(m *PhevMessage) {
	if m.Register != ChargeStatusRegister {
		return
	}
	r.Charging = m.Data[0] == 0x1
	if r.Charging {
		high := int(m.Data[1])
		low := int(m.Data[2])
		if low < 0 {
			low += 0x100
		}
		low *= 0x100

		if high < 0 {
			high += 0x100
		}
		r.Remaining = low + high
	}
	r.raw = m.Data
}

func (r *RegisterChargeStatus) Raw() string {
	return hex.EncodeToString(r.raw)
}

func (r *RegisterChargeStatus) String() string {
	if r.Charging {
		return fmt.Sprintf("Charging, %d remaining.", r.Remaining)
	}
	return "Not charging"
}

func (r *RegisterChargeStatus) Register() byte {
	return ChargeStatusRegister
}

type RegisterACOperStatus struct {
	Operating bool
	raw       []byte
}

func (r *RegisterACOperStatus) Decode(m *PhevMessage) {
	if m.Register != ACOperStatusRegister {
		return
	}
	r.Operating = m.Data[1] == 1
	r.raw = m.Data
}

func (r *RegisterACOperStatus) Raw() string {
	return hex.EncodeToString(r.raw)
}

func (r *RegisterACOperStatus) String() string {
	if r.Operating {
		return "AC on"
	}
	return "AC off"
}

func (r *RegisterACOperStatus) Register() byte {
	return ACOperStatusRegister
}

type RegisterACMode struct {
	Mode string
	mode int
	raw  []byte
}

var acModes = map[int]string{
	0: "none",
	1: "heat",
	2: "cool",
	3: "windscreen",
}

func (r *RegisterACMode) Decode(m *PhevMessage) {
	if m.Register != ACModeRegister {
		return
	}
	r.mode = int(m.Data[0])
	r.Mode = acModes[r.mode]
	r.raw = m.Data
}

func (r *RegisterACMode) Raw() string {
	return hex.EncodeToString(r.raw)
}

func (r *RegisterACMode) String() string {
	return fmt.Sprintf("AC mode %s", r.Mode)
}

func (r *RegisterACMode) Register() byte {
	return ACModeRegister
}
