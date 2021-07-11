package protocol

import (
	"encoding/hex"
	"fmt"
)

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

func GetDecodedMessages(message []byte) [][]byte {
	msgs := [][]byte{}
	for {
		dec, _, rem := ValidateAndDecodeMessage(message)
		if dec != nil {
			msgs = append(msgs, dec)
		}
		if rem == nil {
			break
		}
		message = rem
	}
	return msgs
}
