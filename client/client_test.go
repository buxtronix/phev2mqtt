package client

import (
	"net"
	"testing"
	"time"

	"github.com/buxtronix/phev2mqtt/protocol"
)

// startTestServer returns a listener emulating the car's TCP endpoint,
// accepting a single connection and discarding anything sent to it.
func startTestServer(t *testing.T) (net.Listener, chan net.Conn) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	connCh := make(chan net.Conn, 1)
	go func() {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		connCh <- conn
		buf := make([]byte, 1024)
		for {
			if _, err := conn.Read(buf); err != nil {
				return
			}
		}
	}()
	return l, connCh
}

func TestSendsDoNotBlockAfterClose(t *testing.T) {
	l, connCh := startTestServer(t)
	defer l.Close()

	cl, err := New(AddressOption(l.Addr().String()))
	if err != nil {
		t.Fatal(err)
	}
	if err := cl.Connect(); err != nil {
		t.Fatal(err)
	}
	<-connCh
	cl.Close()

	done := make(chan error, 1)
	go func() {
		done <- cl.SetRegister(0x6, []byte{0x3})
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Error("SetRegister after Close: got nil error, want error")
		}
	case <-time.After(3 * time.Second):
		t.Error("SetRegister blocked after Close")
	}

	// Repeated queued sends must return even with the writer gone
	// and the Send buffer full.
	sends := make(chan struct{})
	go func() {
		for i := 0; i < 10; i++ {
			cl.SendMessage(protocol.NewPingRequestMessage(byte(i)))
		}
		close(sends)
	}()
	select {
	case <-sends:
	case <-time.After(3 * time.Second):
		t.Error("SendMessage blocked after Close")
	}
}

func TestListenersClosedOnDisconnect(t *testing.T) {
	l, connCh := startTestServer(t)
	defer l.Close()

	cl, err := New(AddressOption(l.Addr().String()))
	if err != nil {
		t.Fatal(err)
	}
	if err := cl.Connect(); err != nil {
		t.Fatal(err)
	}
	lis := cl.AddListener()

	// Simulate the car dropping the connection.
	conn := <-connCh
	conn.Close()

	for {
		select {
		case _, ok := <-lis.C:
			if !ok {
				return // Channel closed as expected.
			}
		case <-time.After(3 * time.Second):
			t.Fatal("listener channel not closed after disconnect")
		}
	}
}
