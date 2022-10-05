package protocol

import (
	"encoding/hex"
	"fmt"
)

// SecurityKey implements the algorithm for the session encoding/decoding
// keys.
type SecurityKey struct {
	securityKey byte
	keyMap      []byte
	sNum, rNum  byte
}

// Generate the security keys from the 0x5e/0x4e initialisation
// packets. The payload for these packets runs through the below
// algorithm which initially generates a security key from the data,
// then from this security key a key map is generated, essentially
// an array of session keys which are rotated through.
func (s *SecurityKey) Update(packet []byte) {
	if len(packet) < 12 {
		fmt.Printf("SecurityKey.Update() on short packet!\n")
		return
	}
	// Calculate security key from provided packet.
	result := (packet[4] & 0x8) >> 3
	result |= (packet[5] & 0x8) >> 2
	result |= (packet[6] & 0x8) >> 1
	result |= (packet[7] & 0x8)
	result |= (packet[8] & 0x8) << 1
	result |= (packet[9] & 0x8) << 2
	result |= (packet[10] & 0x8) << 3
	result |= (packet[11] & 0x8) << 4
	s.securityKey = byte(result)
	// From this key, generate the key map.
	s_key := int(s.securityKey)
	s.keyMap = make([]byte, 256)
	for i := 0; i < len(s.keyMap); i++ {
		s.keyMap[i] = byte(i)
	}

	index := 0
	for i := 0; i < 256; i++ {
		index += int(s.keyMap[i])
		index += s_key
		index %= 256
		temp := s.keyMap[i]
		s.keyMap[i] = s.keyMap[index]
		s.keyMap[index] = temp
	}
	// Reset the keymap send/receive indices.
	s.sNum = 0
	s.rNum = 0
}

// Fetch and optionally increment the index for the received
// key (sent from the car). The key is incremented after a packet
// of type 0x6f is sent from the car. Otherwise the same key index
// is used.
// The returned value is XORed with the raw packet from the car before
// decoding it.
func (s *SecurityKey) RKey(increment bool) byte {
	if len(s.keyMap) == 0 {
		return 0
	}
	ret := s.rNum
	if increment {
		s.rNum++
	}
	return s.keyMap[ret]
}

// Fetch and optionally increment the index for the send
// key (sent to the car). The key is incremented after a packet
// of type 0xf6 is sent to the car. Otherwise the same key index
// is used.
// The returned value is XORed with the raw packet before sending
// it to the car.
func (s *SecurityKey) SKey(increment bool) byte {
	if len(s.keyMap) == 0 {
		return 0
	}
	ret := s.sNum
	if increment {
		s.sNum++
	}
	return s.keyMap[ret]
}

func XorMessageWith(message []byte, xor byte) []byte {
	msg := make([]byte, len(message))
	for i := range message {
		msg[i] = message[i] ^ xor
	}
	return msg
}

func Checksum(message []byte) byte {
	length := message[1] + 2

	b := byte(0)
	for i := byte(0); ; i++ {
		if i >= length-1 {
			break
		}
		b = (byte)(message[i] + b)
	}
	return b
}

func ValidateChecksum(message []byte) bool {
	length := int(message[1]) + 2
	if len(message) < length {
		return false
	}
	wantSum := message[length-1]

	return Checksum(message) == wantSum
}

// Validate and decode message. Returns the decoded/validated message,
// plus any trailing data.
func ValidateAndDecodeMessage(message []byte) ([]byte, byte, []byte) {
	if len(message) < 4 {
		fmt.Printf("Short msg\n")
		return nil, 0, nil
	}
	xor := message[2]
	msg := XorMessageWith(message, xor)
	if !ValidateChecksum(msg) {
		xor ^= 1
		msg = XorMessageWith(message, xor)
		if !ValidateChecksum(msg) {
			fmt.Printf("Bad sum for (%s)\n", hex.EncodeToString(message))
			return nil, 0, nil
		}
	}
	length := msg[1] + 2
	if len(message) > int(length) {
		return msg[:length], xor, message[length:]
	}
	return msg[:length], xor, nil
}
