package emulator

import (
	"github.com/buxtronix/phev2mqtt/protocol"
	log "github.com/sirupsen/logrus"
	"time"
)

func (s *Connection) manage() {
	l := s.AddListener()
	pingCount := 0
	defer func() {
		l.Stop()
		s.RemoveListener(l)
	}()
	for {
		select {
		case msg := <-l.C:
			switch msg.Type {
			case protocol.CmdOutPingReq:
				// Ping request from client.
				pingCount++
				s.Send <- protocol.NewPingResponseMessage(msg.Register)
				if s.key.State == protocol.SecurityEmpty && pingCount == 10 {
					// Establish initial key after 10th ping.
					s.state = conSecInit
					s.rekey()
				}
			case protocol.CmdOutMy18StartResp:
				if msg.Original[2] == 0x0 {
					s.rekey()
					break
				}
				if s.key.State == protocol.SecurityKeyProposed {
					s.key.AcceptProposal()
					s.sendNextRegister()
				}
				/*
					if s.state == conSecInit {
						s.state = conRegisterStart
						s.registerIndex = 0
						s.sendNextRegister()
					}
				*/
			case protocol.CmdOutSend:
				if msg.Ack == protocol.Ack {
					s.sendNextRegister()
				}
				if msg.Ack == protocol.Request {
					s.handleSetRegister(msg)
				}
			}
		}
	}
}

func (s *Connection) getRegister(r byte) protocol.Register {
	for _, reg := range s.car.Registers {
		if reg.Register() == r {
			return reg
		}
	}
	return nil
}

func (s *Connection) handleSetRegister(msg *protocol.PhevMessage) {
	// Ack the message that came in.
	s.Send <- protocol.NewMessage(protocol.CmdInResp, msg.Register, true, []byte{0x0})
	switch msg.Register {
	case 0x05:
		s.Send <- protocol.NewMessage(protocol.CmdInResp, protocol.TimeRegister, false, msg.Data)
		time.Sleep(20 * time.Millisecond)
		s.Send <- protocol.NewMessage(protocol.CmdInResp, protocol.BatteryLevelRegister, false, []byte{0x50, 0x00, 0x00, 0x00})
	}

}

func (s *Connection) sendNextRegister() {
	if s.settingsSender != nil {
		if setting, ok := <-s.settingsSender.C; ok {
			s.Send <- setting
		} else {
			s.settingsSender = nil
		}
		return
	}
	if s.registerIndex < 0 {
		return
	}
	msg := s.car.Registers[s.registerIndex].Encode()
	msg.Type = protocol.CmdInResp
	msg.Ack = protocol.Request
	s.Send <- msg

	s.registerIndex++
	if s.registerIndex >= len(s.car.Registers) {
		s.registerIndex = -1
		log.Debug("Finished sending registers, sending settings")
		s.settingsSender = s.car.Settings.NewSender()
		s.settingsSender.Start()
	}
}

// Generate and send new key request.
func (s *Connection) rekey() {
	if s.state == conRegisterStart {
		s.Send <- protocol.NewMessage(protocol.CmdInMy18StartReq, 0x1, true, []byte{0x0})
	}
	data := s.key.GenerateProposal()
	s.Send <- protocol.NewMessage(protocol.CmdInMy18StartReq, 0x1, false, append(data, 0x1))
	s.key.State = protocol.SecurityKeyProposed
}
