package emulator

import (
	"github.com/buxtronix/phev2mqtt/protocol"
	log "github.com/sirupsen/logrus"
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
					s.car.Settings.ResetRegisters()
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
	log.Debugf("Incoming command: %s", msg.Reg.String())
	s.Send <- protocol.NewMessage(protocol.CmdInResp, msg.Register, true, []byte{0x0})
	return
}

func (s *Connection) sendSettings() bool {
	register, done := s.car.Settings.NextRegister()
	reg := &protocol.RegisterGeneric{Reg: 0x16, Value: register}
	msg := reg.Encode()
	msg.Type = protocol.CmdInResp
	msg.Ack = protocol.Request
	s.Send <- msg
	return done
}

func (s *Connection) sendNextRegister() {
	if s.registerIndex < 0 {
		return
	}
	if s.registerIndex == 0 {
		if done := s.sendSettings(); !done {
			return
		}
	}
	msg := s.car.Registers[s.registerIndex].Encode()
	msg.Type = protocol.CmdInResp
	msg.Ack = protocol.Request
	s.Send <- msg

	s.registerIndex++
	if s.registerIndex >= len(s.car.Registers) {
		s.registerIndex = -1
		s.car.Settings.ResetRegisters()
		log.Debug("Finished sending registers")
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
