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
	C      chan *protocol.PhevMessage
	mu     sync.Mutex
	stop   bool
	closed bool
}

func (l *Listener) Start() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.stop = false
	l.closed = false
	l.C = make(chan *protocol.PhevMessage, 5)
}

func (l *Listener) Stop() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.stop = true
}

func (l *Listener) Send(m *protocol.PhevMessage) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return
	}
	select {
	case l.C <- m:
	default:
		log.Debug("%PHEV_RECV_LISTENER% message not sent")
	}
}

func (l *Listener) ProcessStop() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.stop && !l.closed {
		close(l.C)
		l.closed = true
		l.stop = false
		return true
	}
	return false
}

type ModelYear int64

const (
	ModelYearUnknown ModelYear = iota
	ModelYear14
	ModelYear18
	ModelYear24
)

// A Client is a TCP client to a Phev.
type Client struct {
	// Recv is a channel where incoming messages from the Phev are sent.
	Recv chan *protocol.PhevMessage
	// Send is a channel to send messages to the Phev.
	Send chan *protocol.PhevMessage

	// Settings are settings for the car.
	Settings *protocol.Settings

	listeners []*Listener
	lMu       sync.Mutex

	address string
	conn    net.Conn
	lastRx  time.Time
	started chan struct{}

	key *protocol.SecurityKey

	// Keep track of the model year so we can use the correct registers
	ModelYear ModelYear

	done      chan struct{}
	closeOnce sync.Once
}

// isClosed reports whether Close has been called.
func (c *Client) isClosed() bool {
	select {
	case <-c.done:
		return true
	default:
		return false
	}
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
		Settings:  &protocol.Settings{},
		started:   make(chan struct{}, 2),
		listeners: []*Listener{},
		address:   DefaultAddress,
		key:       &protocol.SecurityKey{},
		ModelYear: ModelYearUnknown,
		done:      make(chan struct{}),
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
	l.Start()
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
	var err error
	c.closeOnce.Do(func() {
		close(c.done)
		if c.conn != nil {
			err = c.conn.Close()
		}
	})
	return err
}

// SendMessage queues a message for sending to the car. Unlike sending
// directly to the Send channel, it will not block if the client is closed.
func (c *Client) SendMessage(m *protocol.PhevMessage) error {
	if c.isClosed() {
		return fmt.Errorf("client closed")
	}
	select {
	case c.Send <- m:
		return nil
	case <-c.done:
		return fmt.Errorf("client closed")
	}
}

// Connect connects to the Phev.
func (c *Client) Connect() error {
	conn, err := net.Dial("tcp", c.address)
	if err != nil {
		return err
	}
	log.Info("%PHEV_TCP_CONNECTED%")
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
	setRegister := func(xor byte) error {
		return c.SendMessage(&protocol.PhevMessage{
			Type:     protocol.CmdOutSend,
			Ack:      protocol.Request,
			Register: register,
			Data:     value,
			Xor:      xor,
		})
	}
	xor := byte(0)
	timer := time.After(10 * time.Second)
	l := c.AddListener()
	defer c.RemoveListener(l)
SETREG:
	if err := setRegister(xor); err != nil {
		return err
	}
	for {
		select {
		case <-c.done:
			return fmt.Errorf("client closed")
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
		case c.isClosed():
			return
		case t.Sub(c.lastRx) < 500*time.Millisecond:
			continue
		}
		if err := c.SendMessage(protocol.NewPingRequestMessage(pingSeq)); err != nil {
			return
		}
		pingSeq++
		if pingSeq > 0x63 {
			pingSeq = 0
		}
	}
}

// manages the connection, handling control messages.
func (c *Client) manage() {
	defer close(c.started)
	defer log.Debug("%PHEV_MANAGER_END%%")
	ml := c.AddListener()
	defer ml.Stop()
	for m := range ml.C {
		switch m.Type {
		case protocol.CmdInResp:
			if m.Ack == protocol.Request && m.Register == protocol.SettingsRegister {
				c.Settings.FromRegister(m.Data)
			}
		case protocol.CmdInStartResp:
			if err := c.SendMessage(protocol.NewPingRequestMessage(0xa)); err != nil {
				return
			}
		case protocol.CmdInMy24StartReq:
			c.ModelYear = ModelYear24
			if err := c.SendMessage(&protocol.PhevMessage{
				Type:     protocol.CmdOutMy24StartResp,
				Register: 0x1,
				Ack:      protocol.Ack,
				Xor:      m.Xor,
				Data:     []byte{0x0},
			}); err != nil {
				return
			}
			log.Debug("%%PHEV_START24_RECV%%")
			c.started <- struct{}{}
		case protocol.CmdInMy18StartReq:
			c.ModelYear = ModelYear18
			if err := c.SendMessage(&protocol.PhevMessage{
				Type:     protocol.CmdOutMy18StartResp,
				Register: 0x1,
				Ack:      protocol.Ack,
				Xor:      m.Xor,
				Data:     []byte{0x0},
			}); err != nil {
				return
			}
			log.Debug("%%PHEV_START18_RECV%%")
			c.started <- struct{}{}
		case protocol.CmdInMy14StartReq:
			c.ModelYear = ModelYear14
			if err := c.SendMessage(&protocol.PhevMessage{
				Type:     protocol.CmdOutMy14StartResp,
				Register: 0x1,
				Ack:      protocol.Ack,
				Xor:      m.Xor,
				Data:     []byte{0x0},
			}); err != nil {
				return
			}
			log.Debug("%%PHEV_START14_RECV%%")
			c.started <- struct{}{}
		}
	}
}

func (c *Client) reader() {
	defer func() {
		c.Close()
		close(c.Recv)
		c.lMu.Lock()
		for _, l := range c.listeners {
			l.Stop()
			l.ProcessStop()
		}
		c.lMu.Unlock()
	}()
	for {
		c.conn.(*net.TCPConn).SetReadDeadline(time.Now().Add(30 * time.Second))
		data := make([]byte, 4096)
		n, err := c.conn.Read(data)
		if err != nil {
			if !c.isClosed() {
				log.Debug("%%PHEV_TCP_READER_ERROR%%: ", err)
			}
			log.Debug("%PHEV_TCP_READER_CLOSE%")
			return
		}
		c.lastRx = time.Now()
		log.Tracef("%%PHEV_TCP_RECV_DATA%%: %s", hex.EncodeToString(data[:n]))
		messages := protocol.NewFromBytes(data[:n], c.key)
		for _, m := range messages {
			log.Debugf("%%PHEV_TCP_RECV_MSG%%: [%02x] %s", m.Xor, m.ShortForm())
			c.lMu.Lock()
			for _, l := range c.listeners {
				l.Send(m)
			}
			c.lMu.Unlock()
			select {
			case c.Recv <- m:
			case <-c.done:
				return
			}
		}
	}
}

func (c *Client) writer() {
	for {
		select {
		case <-c.done:
			log.Debug("%PHEV_TCP_WRITER_CLOSE%")
			return
		case msg, ok := <-c.Send:
			if !ok {
				log.Debug("%PHEV_TCP_WRITER_CLOSE%")
				c.Close()
				return
			}
			msg.Xor = 0
			data := msg.EncodeToBytes(c.key)
			log.Debugf("%%PHEV_TCP_SEND_MSG%%: [%02x] %s", msg.Xor, msg.ShortForm())
			log.Tracef("%%PHEV_TCP_SEND_DATA%%: %s", hex.EncodeToString(data))
			c.conn.(*net.TCPConn).SetWriteDeadline(time.Now().Add(15 * time.Second))
			if _, err := c.conn.Write(data); err != nil {
				if !c.isClosed() {
					log.Errorf("%%PHEV_TCP_WRITER_ERROR%%: %v", err)
				}
				log.Debug("%PHEV_TCP_WRITER_CLOSE%")
				c.Close()
				return
			}
		}
	}
}
