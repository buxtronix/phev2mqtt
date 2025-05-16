package protocol

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	log "github.com/sirupsen/logrus"
	"time"
)

const (
	CmdOutPingReq = 0xf3
	CmdInPingResp = 0x3f

	CmdOutSend = 0xf6
	CmdInResp  = 0x6f

	CmdInMy18StartReq   = 0x5e
	CmdOutMy18StartResp = 0xe5

	CmdInMy14StartReq   = 0x4e
	CmdOutMy14StartResp = 0xe4

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
	0xe4: "StartResp14",
	0x4e: "StartReq14",
}

type PhevMessage struct {
	Type          byte
	Length        byte
	Ack           byte
	Register      byte
	Data          []byte
	Checksum      byte
	Xor           byte
	Original      []byte
	OriginalXored []byte
	Reg           Register
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
		return fmt.Sprintf("START RESP    (orig: %s)", hex.EncodeToString(p.Original))

	case CmdOutSend:
		if p.Ack == Ack {
			return fmt.Sprintf("REGISTER ACK  (reg 0x%02x data %s)", p.Register, hex.EncodeToString(p.Data))
		}
		return fmt.Sprintf("REGISTER SET  (reg 0x%02x data %s)", p.Register, hex.EncodeToString(p.Data))

	case CmdInResp:
		if p.Ack == Request {
			if p.Reg != nil {
				return fmt.Sprintf("REGISTER NTFY (reg 0x%02x data %s) [%s]", p.Register, hex.EncodeToString(p.Data), p.Reg.String())
			} else {
				return fmt.Sprintf("REGISTER NTFY (reg 0x%02x data %s)", p.Register, hex.EncodeToString(p.Data))
			}
		}
		return fmt.Sprintf("REGISTER SETACK (reg 0x%02x data %s)", p.Register, hex.EncodeToString(p.Data))

	case CmdInMy18StartReq:
		return fmt.Sprintf("START RECV18  (orig %s)", hex.EncodeToString(p.Original))

	case CmdOutMy18StartResp:
		return fmt.Sprintf("START SEND18  (orig %s)", hex.EncodeToString(p.Original))

	case CmdInMy14StartReq:
		return fmt.Sprintf("START RECV14  (orig %s)", hex.EncodeToString(p.Original))

	case CmdOutMy14StartResp:
		return fmt.Sprintf("START SEND14  (orig %s)", hex.EncodeToString(p.Original))

	case CmdInBadEncoding:
		return fmt.Sprintf("BAD ENCODING  (exp: 0x%02x)", p.Data[0])

	default:
		return p.String()
	}
}

func (p *PhevMessage) RawString() string {
	return hex.EncodeToString(p.Original)
}

func (p *PhevMessage) EncodeToBytes(key *SecurityKey) []byte {
	length := byte(len(p.Data) + 3)
	data := []byte{
		p.Type,
		length,
		p.Ack,
		p.Register,
	}
	data = append(data, p.Data...)
	data = append(data, Checksum(data))
	var xor byte
	switch p.Type {
	case CmdInMy18StartReq, CmdOutMy18StartResp, CmdInMy14StartReq, CmdOutMy14StartResp:
		// No xor/key for these messages.
	case CmdOutSend:
		// Use then increment send key.
		xor = key.SKey(true)
	default:
		// Use but do not increment send key.
		xor = key.SKey(false)
	}
	p.Xor = xor
	return XorMessageWith(data, xor)
}

func (p *PhevMessage) DecodeFromBytes(data []byte, key *SecurityKey) error {
	if len(data) < 4 {
		return fmt.Errorf("invalid packet length")
	}
	p.OriginalXored = data
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
	switch p.Type {
	case CmdInMy18StartReq, CmdInMy14StartReq:
		key.Update(p.OriginalXored)
	case CmdInResp:
		key.RKey(true)
	case CmdOutSend:
		key.SKey(true)
	}
	if p.Type == CmdInResp && p.Ack == Request {
		switch p.Register {
		case VINRegister:
			p.Reg = new(RegisterVIN)
		case SettingsRegister:
			p.Reg = new(RegisterSettings)
		case TimeRegister:
			p.Reg = new(RegisterTime)
		case ECUVersionRegister:
			p.Reg = new(RegisterECUVersion)
		case BatteryLevelRegister:
			p.Reg = new(RegisterBatteryLevel)
		case BatteryWarningRegister:
			p.Reg = new(RegisterBatteryWarning)
		case DoorStatusRegister:
			p.Reg = new(RegisterDoorStatus)
		case ChargePlugRegister:
			p.Reg = new(RegisterChargePlug)
		case ChargeStatusRegister:
			p.Reg = new(RegisterChargeStatus)
		case PreACStateRegister:
			p.Reg = new(RegisterPreACState)
		case ACOperStatusRegister:
			p.Reg = new(RegisterACOperStatus)
		case ACModeRegister:
			p.Reg = new(RegisterACMode)
		case WIFISSIDRegister:
			p.Reg = new(RegisterWIFISSID)
		case LightStatusRegister:
			p.Reg = new(RegisterLightStatus)
		default:
			p.Reg = new(RegisterGeneric)
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

func NewFromBytes(data []byte, key *SecurityKey) []*PhevMessage {
	msgs := []*PhevMessage{}

	log.Tracef("%%PHEV_DECODE_FROM_BYTES%%: Raw: %s", hex.EncodeToString(data))
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
		log.Tracef("%%PHEV_DECODED_FROM_BYTES%%: Raw: %s", hex.EncodeToString(dat))
		dat = XorMessageWith(dat, xor)
		p := &PhevMessage{}
		err := p.DecodeFromBytes(dat, key)
		p.OriginalXored = data[offset : offset+len(dat)]
		p.Xor = xor
		if err != nil {
			log.Errorf("decode error: %v\n", err)
			break
		}
		msgs = append(msgs, p)
		if len(rem) < 1 {
			break
		}
		data = rem
		offset = 0
	}
	return msgs
}

func encodeTime(t time.Time) []byte {
	return []byte{
		byte(t.Year() - 2000),
		byte(t.Month()),
		byte(t.Day()),
		byte(t.Hour()),
		byte(t.Minute()),
		byte(t.Second()),
		byte(t.Weekday())}
}

func decodeTime(m []byte) time.Time {
	return time.Date(
		2000+int(m[0]),   // Year
		time.Month(m[1]), // Month
		int(m[2]),        // Day of month
		int(m[3]),        // Hour
		int(m[4]),        // Minute
		int(m[5]),        // Second
		0, time.Local)
}

const (
	BatteryWarningRegister   = 0x02
	SetACModeRegisterMY14    = 0x02
	SetACEnabledRegisterMY14 = 0x04
	PreACStateRegister       = 0x10
	TimeRegister             = 0x12
	SetAckPreACTermRegister  = 0x13
	VINRegister              = 0x15
	SettingsRegister         = 0x16
	ACOperStatusRegister     = 0x1a
	SetACModeRegisterMY18    = 0x1b
	ACModeRegister           = 0x1c
	BatteryLevelRegister     = 0x1d
	ChargePlugRegister       = 0x1e
	ChargeStatusRegister     = 0x1f
	LightStatusRegister      = 0x23
	DoorStatusRegister       = 0x24
	WIFISSIDRegister         = 0x28
	ECUVersionRegister       = 0xc0
)

type Register interface {
	Decode(*PhevMessage)
	Encode() *PhevMessage
	Raw() string
	String() string
	Register() byte
}

type RegisterGeneric struct {
	Reg   byte
	Value []byte
}

func (r *RegisterGeneric) Decode(m *PhevMessage) {
	r.Reg = m.Register
	r.Value = m.Data
}

func (r *RegisterGeneric) Encode() *PhevMessage {
	return &PhevMessage{
		Register: r.Register(),
		Data:     r.Value,
	}
}

func (r *RegisterGeneric) Raw() string {
	return hex.EncodeToString(r.Value)
}

func (r *RegisterGeneric) String() string {
	return fmt.Sprintf("g(0x%02x): %s", r.Reg, r.Raw())
}

func (r *RegisterGeneric) Register() byte {
	return r.Reg
}

type RegisterTime struct {
	Time time.Time
	raw  []byte
}

func (r *RegisterTime) Decode(m *PhevMessage) {
	if len(m.Data) != 7 {
		return
	}
	r.Time = decodeTime(m.Data)
	r.raw = m.Data
}

func (r *RegisterTime) Encode() *PhevMessage {
	return &PhevMessage{
		Register: r.Register(),
		Data:     encodeTime(r.Time),
	}
}

func (r *RegisterTime) Raw() string {
	return hex.EncodeToString(r.raw)
}

func (r *RegisterTime) String() string {
	return r.Time.String()
}

func (r *RegisterTime) Register() byte {
	return TimeRegister
}

type RegisterSettings struct {
	register byte
	raw      []byte
}

func (r *RegisterSettings) Decode(m *PhevMessage) {
	r.register = m.Register
	r.raw = m.Data
}

func (r *RegisterSettings) Encode() *PhevMessage {
	return &PhevMessage{
		Register: r.Register(),
		Data:     r.raw,
	}
}

func (r *RegisterSettings) Raw() string {
	return hex.EncodeToString(r.raw)
}

func (r *RegisterSettings) String() string {
	value := binary.LittleEndian.Uint64(r.raw)
	return fmt.Sprintf("Car Settings: %016x", value)
}

func (r *RegisterSettings) Register() byte {
	return r.register
}

type RegisterVIN struct {
	VIN           string
	Registrations int
	raw           []byte
}

func (r *RegisterVIN) Decode(m *PhevMessage) {
	if m.Register != VINRegister || len(m.Data) != 20 {
		return
	}
	r.VIN = string(m.Data[1:17])
	r.Registrations = int(m.Data[19])
	r.raw = m.Data
}

func (r *RegisterVIN) Encode() *PhevMessage {
	data := []byte{0x3}
	data = append(data, []byte(r.VIN)...)
	data = append(data, 0x0)
	data = append(data, byte(r.Registrations))
	return &PhevMessage{
		Register: r.Register(),
		Data:     data,
	}
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
	if m.Register != ECUVersionRegister || len(m.Data) != 13 {
		return
	}
	r.Version = string(m.Data[:9])
	r.raw = m.Data
}

func (r *RegisterECUVersion) Encode() *PhevMessage {
	data := []byte(r.Version)
	data = append(data, []byte{0x11, 0x00, 0x00}...)
	return &PhevMessage{
		Register: r.Register(),
		Data:     data,
	}
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
	Level         int
	ParkingLights bool // yes parking lights here.
	raw           []byte
}

func (r *RegisterBatteryLevel) Decode(m *PhevMessage) {
	if m.Register != BatteryLevelRegister || len(m.Data) != 4 {
		return
	}
	r.Level = int(m.Data[0])
	r.ParkingLights = m.Data[2] == 0x1
	r.raw = m.Data
}

func (r *RegisterBatteryLevel) Encode() *PhevMessage {
	data := []byte{byte(r.Level), 0x0, 0x0}
	if r.ParkingLights {
		data[2] = 0x1
	}

	return &PhevMessage{
		Register: r.Register(),
		Data:     data,
	}
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
	if m.Register != BatteryWarningRegister || len(m.Data) != 4 {
		return
	}
	r.Warning = int(m.Data[2])
	r.raw = m.Data
}

func (r *RegisterBatteryWarning) Encode() *PhevMessage {
	data := []byte{0x0, 0x0, byte(r.Warning), 0x0}
	return &PhevMessage{
		Register: r.Register(),
		Data:     data,
	}
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
	// Locked is true if the vehicle is locked.
	Locked bool
	// The below are true if the corresponding door is open.
	Driver, FrontPassenger, RearLeft, RearRight bool
	Bonnet, Boot                                bool
	// Headlight state is in this register!
	Headlights bool
	raw        []byte
}

func (r *RegisterDoorStatus) Decode(m *PhevMessage) {
	if m.Register != DoorStatusRegister || len(m.Data) != 10 {
		return
	}
	r.Locked = m.Data[0] == 0x1
	r.Driver = m.Data[3] == 0x1
	r.FrontPassenger = m.Data[4] == 0x1
	r.RearRight = m.Data[5] == 0x1
	r.RearLeft = m.Data[6] == 0x1
	r.Boot = m.Data[7] == 0x1
	r.Bonnet = m.Data[8] == 0x1
	r.Headlights = m.Data[9] == 0x1
	r.raw = m.Data
}

func (r *RegisterDoorStatus) Encode() *PhevMessage {
	data := make([]byte, 10)
	if r.Locked {
		data[0] = 0x1
	}
	if r.Driver {
		data[3] = 0x1
	}
	if r.FrontPassenger {
		data[4] = 0x1
	}
	if r.RearRight {
		data[5] = 0x1
	}
	if r.RearLeft {
		data[6] = 0x1
	}
	if r.Boot {
		data[7] = 0x1
	}
	if r.Bonnet {
		data[8] = 0x1
	}
	if r.Headlights {
		data[9] = 0x1
	}
	return &PhevMessage{
		Register: r.Register(),
		Data:     data,
	}
}

func (r *RegisterDoorStatus) Raw() string {
	return hex.EncodeToString(r.raw)
}

func (r *RegisterDoorStatus) String() string {
	openStr := ""
	if r.FrontPassenger || r.Driver || r.RearRight || r.RearLeft || r.Boot || r.Bonnet {

		openStr = " Open:"
		if r.Driver {
			openStr += " driver"
		}
		if r.FrontPassenger {
			openStr += " front_passenger"
		}
		if r.RearRight {
			openStr += " rear_right"
		}
		if r.RearLeft {
			openStr += " rear_left"
		}
		if r.Bonnet {
			openStr += " bonnet"
		}
		if r.Boot {
			openStr += " boot"
		}
	}
	if r.Locked {
		return "Doors locked." + openStr
	}
	return "Doors unlocked." + openStr
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
	if m.Register != ChargeStatusRegister || len(m.Data) != 3 {
		return
	}
	r.Charging = m.Data[0] == 0x1
	r.Remaining = 0
	if m.Data[2] != 0xff {
		r.Remaining = int(m.Data[2])<<8 | int(m.Data[1])
	}
	r.raw = m.Data
}

func (r *RegisterChargeStatus) Encode() *PhevMessage {
	data := make([]byte, 3)
	if r.Charging {
		data[0] = 0x1
		data[1] = byte(r.Remaining % 256)
		data[2] = byte(r.Remaining / 256)
	}
	return &PhevMessage{
		Register: r.Register(),
		Data:     data,
	}
}

func (r *RegisterACOperStatus) Encode() *PhevMessage {
	data := make([]byte, 5)
	if r.Operating {
		data[1] = 0x1
	}
	return &PhevMessage{
		Register: r.Register(),
		Data:     data,
	}
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

type PreACState int8

const (
	PreACOff        PreACState = 0
	PreACOn         PreACState = 2
	PreACTerminated PreACState = 3
)

type RegisterPreACState struct {
	State PreACState
	raw   []byte
}

func (r *RegisterPreACState) Encode() *PhevMessage {
	panic("unimplemented")
	return nil
}

func (r *RegisterPreACState) Decode(m *PhevMessage) {
	// MY'18 data length is 3 bytes, MY'14 uses 1 byte
	// We only decode the operating state in 0th byte
	if len(m.Data) < 1 {
		return
	}
	r.State = PreACState(m.Data[0])
	r.raw = m.Data
}

func (r *RegisterPreACState) Raw() string {
	return hex.EncodeToString(r.raw)
}

func (r *RegisterPreACState) String() string {
	switch r.State {
	case PreACOff:
		return "Pre-AC off"
	case PreACOn:
		return "Pre-AC on"
	case PreACTerminated:
		return "Pre-AC terminated (door opened or battery low?)"
	default:
		return fmt.Sprintf("Pre-AC: unknown (%v)", r.State)
	}
}

func (r *RegisterPreACState) Register() byte {
	return PreACStateRegister
}

type RegisterACOperStatus struct {
	Operating bool
	raw       []byte
}

func (r *RegisterACOperStatus) Decode(m *PhevMessage) {
	// MY'18 data length is 5 bytes, MY'14 uses 2 bytes
	// We only decode the operating state in byte 2
	if m.Register != ACOperStatusRegister || len(m.Data) < 2 {
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
	Mode     string
	Duration uint8
	raw      []byte
}

func (r *RegisterACMode) Decode(m *PhevMessage) {
	if len(m.Data) != 1 {
		return
	}
	switch m.Data[0] & 0x0f {
	case 0:
		r.Mode = "unknown"
	case 1:
		r.Mode = "cool"
	case 2:
		r.Mode = "heat"
	case 3:
		r.Mode = "windscreen"
	}
	switch m.Data[0] & 0xf0 {
	case 0x00:
		r.Duration = 10
	case 0x10:
		r.Duration = 20
	case 0x20:
		r.Duration = 30
	}
	r.raw = m.Data
}

func (r *RegisterACMode) Encode() *PhevMessage {
	var data byte
	switch r.Mode {
	case "unknown":
		data = 0x0
	case "cool":
		data = 0x1
	case "heat":
		data = 0x2
	case "windscreen":
		data = 0x3
	}
	return &PhevMessage{
		Register: r.Register(),
		Data:     []byte{data},
	}
}

func (r *RegisterACMode) Raw() string {
	return hex.EncodeToString(r.raw)
}

func (r *RegisterACMode) String() string {
	return r.Mode
}

func (r *RegisterACMode) Register() byte {
	return ACModeRegister
}

type RegisterChargePlug struct {
	Connected bool
	raw       []byte
}

func (r *RegisterChargePlug) Decode(m *PhevMessage) {
	if len(m.Data) != 2 {
		return
	}
	r.Connected = (m.Data[1] == 1 || m.Data[0] > 0)
	r.raw = m.Data
}

func (r *RegisterChargePlug) Encode() *PhevMessage {
	data := make([]byte, 2)
	if r.Connected {
		data[1] = 0x1
	}
	return &PhevMessage{
		Register: r.Register(),
		Data:     data,
	}
}

func (r *RegisterChargePlug) Raw() string {
	return hex.EncodeToString(r.raw)
}

func (r *RegisterChargePlug) String() string {
	if r.Connected {
		return "Charger connected"
	}
	return "Charger disconnected"
}

func (r *RegisterChargePlug) Register() byte {
	return ChargePlugRegister
}

type RegisterWIFISSID struct {
	SSID string
	raw  []byte
}

func (r *RegisterWIFISSID) Decode(m *PhevMessage) {
	if m.Register != WIFISSIDRegister || len(m.Data) != 32 {
		return
	}
	r.raw = []byte(m.Data)
	r.raw = append([]byte{}, m.Data...)
	dat := append([]byte{}, m.Data...)
	for i, b := range dat {
		if b == 0xff {
			dat[i] = 0x0
		}
	}
	r.SSID = string(dat)
}

func (r *RegisterWIFISSID) Encode() *PhevMessage {
	data := []byte(r.SSID)
	padding := make([]byte, 32-len(r.SSID))
	return &PhevMessage{
		Register: r.Register(),
		Data:     append(data, padding...),
	}
}

func (r *RegisterWIFISSID) Raw() string {
	return hex.EncodeToString(r.raw)
}

func (r *RegisterWIFISSID) String() string {
	return fmt.Sprintf("SSID: %s", r.SSID)
}

func (r *RegisterWIFISSID) Register() byte {
	return WIFISSIDRegister
}

func NewPingRequestMessage(id byte) *PhevMessage {
	return NewMessage(CmdOutPingReq, id, false, []byte{0x0})
}

func NewPingResponseMessage(id byte) *PhevMessage {
	return NewMessage(CmdInPingResp, id, true, []byte{0x0})
}

func NewMessage(typ, register byte, ack bool, data []byte) *PhevMessage {
	msg := &PhevMessage{
		Type:     typ,
		Register: register,
		Data:     data,
	}
	if ack {
		msg.Ack = Ack
	}
	return msg
}

type RegisterLightStatus struct {
	Interior bool
	Hazard   bool
	raw      []byte
}

func (r *RegisterLightStatus) Encode() *PhevMessage {
	panic("unimplemented")
	return nil
}

func (r *RegisterLightStatus) Decode(m *PhevMessage) {
	if len(m.Data) != 5 {
		return
	}
	// Switches between 2 for Off and 1 for On.
	r.Interior = m.Data[4]&0b11 == 1
	r.Hazard = m.Data[3]&0b11 == 1
	r.raw = m.Data
}

func (r *RegisterLightStatus) Raw() string {
	return hex.EncodeToString(r.raw)
}

func (r *RegisterLightStatus) String() string {
	return fmt.Sprintf("Hazard lights: %t; Interior lights: %t.", r.Hazard, r.Interior)
}

func (r *RegisterLightStatus) Register() byte {
	return LightStatusRegister
}
