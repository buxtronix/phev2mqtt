// Package client implements a client for communicating with a Mitsubishi
// Outlander Phev.
package client

import (
	"encoding/hex"
	"fmt"
	log "github.com/sirupsen/logrus"
	"net"
	"sync"
	"time"

	"github.com/buxtronix/phev2mqtt/protocol"
)

const DefaultAddress = "192.168.8.46:8080"

// A Listener is for communicating messages from the vehicle to
// interested clients.
type Listener struct {
	// C has received messages.
	C    chan *protocol.PhevMessage
	stop bool
}

func (l *Listener) start() {
	l.stop = false
	l.C = make(chan *protocol.PhevMessage, 5)
}

func (l *Listener) Stop() {
	l.stop = true
}

func (l *Listener) send(m *protocol.PhevMessage) {
	select {
	case l.C <- m:
	default:
		log.Debug("%PHEV_RECV_LISTENER% message not sent")
	}
}

func (l *Listener) ProcessStop() bool {
	if l.stop {
		close(l.C)
		l.stop = false
		return true
	}
	return false
}

// A Client is a TCP client to a Phev.
type Client struct {
	// Recv is a channel where incoming messages from the Phev are sent.
	Recv chan *protocol.PhevMessage
	// Send is a channel to send messages to the Phev.
	Send chan *protocol.PhevMessage

	// Settings are vehicle settings.
	Settings *protocol.Settings

	listeners []*Listener
	lMu       sync.Mutex

	address string
	conn    net.Conn
	lastRx  time.Time
	started chan struct{}

	key *protocol.SecurityKey

	closed bool
}

// An Option configures the client.
type Option func(c *Client)

// AddressOption configures the address to the Phev.
func AddressOption(address string) func(*Client) {
	return func(c *Client) {
		c.address = address
	}
}

// New returns a new client, not yet connected.
func New(opts ...Option) (*Client, error) {
	cl := &Client{
		Recv:      make(chan *protocol.PhevMessage, 5),
		Send:      make(chan *protocol.PhevMessage, 5),
		Settings: &protocol.Settings{},
		started:   make(chan struct{}, 2),
		listeners: []*Listener{},
		address:   DefaultAddress,
		key:       &protocol.SecurityKey{},
	}
	for _, o := range opts {
		o(cl)
	}
	return cl, nil
}

// Create and return a new Listener.
func (c *Client) AddListener() *Listener {
	c.lMu.Lock()
	defer c.lMu.Unlock()
	l := &Listener{}
	l.start()
	c.listeners = append(c.listeners, l)
	return l
}

func (c *Client) RemoveListener(l *Listener) {
	newL := []*Listener{}
	c.lMu.Lock()
	defer c.lMu.Unlock()
	for _, lis := range c.listeners {
		if lis != l {
			newL = append(newL, lis)
		}
	}
	c.listeners = newL
}

// Close closes the client.
func (c *Client) Close() error {
	c.closed = true
	if c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

// Connect connects to the Phev.
func (c *Client) Connect() error {
	conn, err := net.Dial("tcp", c.address)
	if err != nil {
		return err
	}
	log.Info("%PHEV_TCP_CONNECTED%")
	c.closed = false
	c.conn = conn
	go c.reader()
	go c.writer()
	go c.manage()
	go c.pinger()

	return nil
}

var startTimeout = 20 * time.Second

// Start waits for the client to start.
func (c *Client) Start() error {
	log.Debug("%%PHEV_START_AWAIT%%")
	startTimer := time.After(startTimeout)
	for {
		select {
		case _, ok := <-c.started:
			if !ok {
				log.Debug("%%PHEV_START_CLOSED%%")
				return fmt.Errorf("receiver closed before getting start request")
			}
			log.Debug("%%PHEV_START_DONE%%")
		case <-startTimer:
			log.Debug("%%PHEV_START_TIMEOUT%%")
			return fmt.Errorf("timed out waiting for start")
		}
		return nil
	}
}

// SetRegister sets a register on the car.
func (c *Client) SetRegister(register byte, value []byte) error {
	setRegister := func(xor byte) {
		c.Send <- &protocol.PhevMessage{
			Type:     protocol.CmdOutSend,
			Ack:      protocol.Request,
			Register: register,
			Data:     value,
			Xor:      xor,
		}
	}
	xor := byte(0)
	timer := time.After(10 * time.Second)
	l := c.AddListener()
	defer c.RemoveListener(l)
SETREG:
	setRegister(xor)
	for {
		select {
		case <-timer:
			return fmt.Errorf("timed out attempting to set register %02x", register)
		case msg, ok := <-l.C:
			if !ok {
				return fmt.Errorf("listener channel closed")
			}
			if msg.Type == protocol.CmdInBadEncoding {
				xor = msg.Data[0]
				goto SETREG
			}
			if msg.Type == protocol.CmdInResp && msg.Ack == protocol.Ack && msg.Register == register {
				return nil
			}

		}
	}
}

func (c *Client) nextRecvMsg(deadline time.Time) (*protocol.PhevMessage, error) {
	timer := time.After(deadline.Sub(time.Now()))
	for {
		select {
		case <-timer:
			return nil, fmt.Errorf("timed out waiting for message")
		case m, ok := <-c.Recv:
			if !ok {
				return nil, fmt.Errorf("error: receive channel closed")
			}
			return m, nil
		}
	}
}

// Sends periodic pings to the car.
func (c *Client) pinger() {
	pingSeq := byte(0xa)
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for t := range ticker.C {
		switch {
		case c.closed:
			return
		case t.Sub(c.lastRx) < 500*time.Millisecond:
			continue
		}
		c.Send <- &protocol.PhevMessage{
			Type:     protocol.CmdOutPingReq,
			Ack:      protocol.Request,
			Register: pingSeq,
			Data:     []byte{0x0},
		}
		pingSeq++
		if pingSeq > 0x63 {
			pingSeq = 0
		}
	}
}

// manages the connection, handling control messages.
func (c *Client) manage() {
	ml := c.AddListener()
	defer ml.Stop()
	for m := range ml.C {
		switch m.Type {
		case protocol.CmdInResp:
			if m.Ack == protocol.Request && m.Register == protocol.SettingsRegister {
				c.Settings.FromRegister(m.Data)
			}
		case protocol.CmdInStartResp:
			c.Send <- &protocol.PhevMessage{
				Type:     protocol.CmdOutPingReq,
				Ack:      protocol.Request,
				Register: 0xa,
				Data:     []byte{0x0},
				Xor:      m.Xor,
			}
		case protocol.CmdInMy18StartReq:
			c.Send <- &protocol.PhevMessage{
				Type:     protocol.CmdOutMy18StartResp,
				Register: 0x1,
				Ack:      protocol.Ack,
				Xor:      m.Xor,
				Data:     []byte{0x0},
			}
			log.Debug("%%PHEV_START18_RECV%%")
			c.started <- struct{}{}
		case protocol.CmdInMy14StartReq:
			c.Send <- &protocol.PhevMessage{
				Type:     protocol.CmdOutMy14StartResp,
				Register: 0x1,
				Ack:      protocol.Ack,
				Xor:      m.Xor,
				Data:     []byte{0x0},
			}
			log.Debug("%%PHEV_START14_RECV%%")
			c.started <- struct{}{}
		}
	}
	close(c.started)
	log.Debug("%PHEV_MANAGER_END%%")
}

func (c *Client) reader() {
	for {
		c.conn.(*net.TCPConn).SetReadDeadline(time.Now().Add(30 * time.Second))
		data := make([]byte, 4096)
		n, err := c.conn.Read(data)
		if err != nil {
			if !c.closed {
				log.Debug("%%PHEV_TCP_READER_ERROR%%: ", err)
			}
			log.Debug("%PHEV_TCP_READER_CLOSE%")
			c.conn.Close()
			close(c.Recv)
			c.lMu.Lock()
			for _, l := range c.listeners {
				l.Stop()
			}
			c.lMu.Unlock()
			return
		}
		c.lastRx = time.Now()
		log.Tracef("%%PHEV_TCP_RECV_DATA%%: %s", hex.EncodeToString(data[:n]))
		messages := protocol.NewFromBytes(data[:n], c.key)
		for _, m := range messages {
			if m.Type == protocol.CmdInPingResp {
				log.Tracef("%%PHEV_TCP_RECV_MSG%%: [%02x] %s", m.Xor, m.ShortForm())
			} else {
				log.Debugf("%%PHEV_TCP_RECV_MSG%%: [%02x] %s", m.Xor, m.ShortForm())
			}
			c.lMu.Lock()
			for _, l := range c.listeners {
				l.send(m)
			}
			c.lMu.Unlock()
			c.Recv <- m
		}
	}
}

func (c *Client) writer() {
	for {
		select {
		case msg, ok := <-c.Send:
			if !ok {
				log.Debug("%PHEV_TCP_WRITER_CLOSE%")
				c.conn.Close()
				return
			}
			msg.Xor = 0
			data := msg.EncodeToBytes(c.key)
			if msg.Type == protocol.CmdOutPingReq {
				log.Tracef("%%PHEV_TCP_SEND_MSG%%: [%02x] %s", msg.Xor, msg.ShortForm())
			} else {
				log.Debugf("%%PHEV_TCP_SEND_MSG%%: [%02x] %s", msg.Xor, msg.ShortForm())
			}
			log.Tracef("%%PHEV_TCP_SEND_DATA%%: %s", hex.EncodeToString(data))
			c.conn.(*net.TCPConn).SetWriteDeadline(time.Now().Add(15 * time.Second))
			if _, err := c.conn.Write(data); err != nil {
				if !c.closed {
					log.Errorf("%%PHEV_TCP_WRITER_ERROR%%: %v", err)
				}
				log.Debug("%PHEV_TCP_WRITER_CLOSE%")
				c.conn.Close()
				return
			}
		}
	}
}
