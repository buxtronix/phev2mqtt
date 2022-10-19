package protocol

import (
	"encoding/binary"
	"fmt"
	"strings"
	//        log "github.com/sirupsen/logrus"
)

// Vehicle settings are sent to the client in register 0x16.
// The client sends updated settings to the vehicle via register 0x0f.
type Settings struct {
	settings []uint64
}

// FromRegister extracts settings from the 0x16 register. They appear
// to be little endian and need decoding, though this is not yet certain.
func (s *Settings) FromRegister(reg []byte) error {
	switch {
	case len(reg) != 8:
		return fmt.Errorf("register wrong length got=%d want=8", len(reg))
	case reg[0] != 0x2:
		return fmt.Errorf("register must start with 0x2, is 0x%x", reg[0])
	case reg[7] != 0x0:
		return fmt.Errorf("register must end with 0x0, is 0x%x", reg[7])
	}
	value := binary.LittleEndian.Uint64(reg)
	for _, v := range s.settings {
		if value == v {
			return nil
		}
	}
	s.settings = append(s.settings, value)
	return nil
}

func (s *Settings) Clear() {
	s.settings = []uint64{}
}

func (s *Settings) NewSender() *SettingsSender {
	return &SettingsSender{settings: s.settings}
}

func (s *Settings) Dump() string {
	ret := []string{}
	for _, v := range s.settings {
		ret = append(ret, fmt.Sprintf("%016x", v))
	}
	return strings.Join(ret, "\n")
}

type SettingsSender struct {
	C        chan *PhevMessage
	settings []uint64
}

func (s *SettingsSender) Start() {
	s.C = make(chan *PhevMessage)
	go func() {
		for i := 0; i < len(s.settings); i++ {
			buf := make([]byte, 8)
			binary.LittleEndian.PutUint64(buf, s.settings[i])
			s.C <- NewMessage(CmdInResp, 0x16, false, buf)
		}
		close(s.C)
	}()
}
