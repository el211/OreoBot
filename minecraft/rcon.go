package minecraft

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

const (
	packetTypeCommand  = 2
	packetTypeAuth     = 3
	packetTypeResponse = 0
)

type Client struct {
	addr     string
	password string
	conn     net.Conn
	mu       sync.Mutex
	reqID    int32
}

func NewClient(ip string, port int, password string) *Client {
	return &Client{
		addr:     fmt.Sprintf("%s:%d", ip, port),
		password: password,
	}
}

func (c *Client) Connect() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	conn, err := net.DialTimeout("tcp", c.addr, 5*time.Second)
	if err != nil {
		return fmt.Errorf("rcon connect: %w", err)
	}
	c.conn = conn

	resp, err := c.sendPacketLocked(packetTypeAuth, c.password)
	if err != nil {
		c.conn.Close()
		c.conn = nil
		return fmt.Errorf("rcon auth send: %w", err)
	}
	if resp.ID == -1 {
		c.conn.Close()
		c.conn = nil
		return errors.New("rcon auth failed: invalid password")
	}
	return nil
}

func (c *Client) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn != nil {
		c.conn.Close()
		c.conn = nil
	}
}

func (c *Client) Command(cmd string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn == nil {
		return "", errors.New("not connected")
	}

	resp, err := c.sendPacketLocked(packetTypeCommand, cmd)
	if err != nil {

		c.conn.Close()
		c.conn = nil
		if reconnErr := c.reconnectLocked(); reconnErr != nil {
			return "", fmt.Errorf("rcon reconnect failed: %w (original: %v)", reconnErr, err)
		}
		resp, err = c.sendPacketLocked(packetTypeCommand, cmd)
		if err != nil {
			return "", err
		}
	}
	return resp.Body, nil
}

func (c *Client) IsConnected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn != nil
}

type rconPacket struct {
	ID   int32
	Type int32
	Body string
}

func (c *Client) reconnectLocked() error {
	conn, err := net.DialTimeout("tcp", c.addr, 5*time.Second)
	if err != nil {
		return err
	}
	c.conn = conn

	resp, err := c.sendPacketLocked(packetTypeAuth, c.password)
	if err != nil {
		c.conn.Close()
		c.conn = nil
		return err
	}
	if resp.ID == -1 {
		c.conn.Close()
		c.conn = nil
		return errors.New("auth failed")
	}
	return nil
}

func (c *Client) sendPacketLocked(pktType int32, body string) (*rconPacket, error) {
	id := atomic.AddInt32(&c.reqID, 1)

	payload := []byte(body)
	pktLen := int32(4 + 4 + len(payload) + 2)

	buf := new(bytes.Buffer)
	binary.Write(buf, binary.LittleEndian, pktLen)
	binary.Write(buf, binary.LittleEndian, id)
	binary.Write(buf, binary.LittleEndian, pktType)
	buf.Write(payload)
	buf.Write([]byte{0, 0})

	_ = c.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if _, err := c.conn.Write(buf.Bytes()); err != nil {
		return nil, err
	}

	_ = c.conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	return readPacket(c.conn)
}

func readPacket(r io.Reader) (*rconPacket, error) {
	var pktLen int32
	if err := binary.Read(r, binary.LittleEndian, &pktLen); err != nil {
		return nil, err
	}
	if pktLen < 10 || pktLen > 4096+10 {
		return nil, fmt.Errorf("invalid packet length: %d", pktLen)
	}

	data := make([]byte, pktLen)
	if _, err := io.ReadFull(r, data); err != nil {
		return nil, err
	}

	pkt := &rconPacket{
		ID:   int32(binary.LittleEndian.Uint32(data[0:4])),
		Type: int32(binary.LittleEndian.Uint32(data[4:8])),
	}

	if len(data) > 10 {
		pkt.Body = string(data[8 : len(data)-2])
	}
	return pkt, nil
}
