package client

import (
	"testing"
	"time"

	"github.com/buxtronix/phev2mqtt/protocol"
)

func badEncoding(hint byte) *protocol.PhevMessage {
	return &protocol.PhevMessage{Type: protocol.CmdInBadEncoding, Data: []byte{hint}}
}

func TestOnBadEncodingDropsRepeats(t *testing.T) {
	c, _ := New(KeyResyncOption(true))
	if !c.onBadEncoding(badEncoding(0x10)) {
		t.Error("first complaint dropped")
	}
	if c.onBadEncoding(badEncoding(0x10)) {
		t.Error("repeated complaint passed on")
	}
	if !c.onBadEncoding(badEncoding(0x11)) {
		t.Error("complaint with another key dropped")
	}
	c.lastBadAt = time.Now().Add(-duplicateComplaintWindow)
	if !c.onBadEncoding(badEncoding(0x11)) {
		t.Error("same key after the window dropped")
	}
}

func TestOnBadEncodingAsksForNewKey(t *testing.T) {
	c, _ := New(KeyResyncOption(true))
	c.startRespType.Store(uint32(protocol.CmdOutMy14StartResp))
	for i := 0; i < rekeyComplaints; i++ {
		c.onBadEncoding(badEncoding(byte(i)))
	}
	if c.lastRekey.IsZero() {
		t.Fatalf("no new key asked after %d complaints", rekeyComplaints)
	}
	select {
	case m := <-c.Send:
		if m.Type != protocol.CmdOutMy14StartResp || m.Ack != protocol.Request {
			t.Errorf("sent %s, want a MY14 start response with ack 0", m.ShortForm())
		}
	case <-time.After(time.Second):
		t.Error("nothing sent")
	}
	// Not again straight away.
	last := c.lastRekey
	for i := 0; i < rekeyComplaints; i++ {
		c.onBadEncoding(badEncoding(byte(0x80 + i)))
	}
	if c.lastRekey != last {
		t.Error("asked for a new key again within rekeyInterval")
	}
}

func TestSignalStartedNeverBlocks(t *testing.T) {
	c, _ := New()
	done := make(chan struct{})
	go func() {
		for i := 0; i < 10; i++ {
			c.signalStarted()
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("signalStarted blocked with nobody waiting")
	}
}
