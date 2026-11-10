package utils_test

import (
	"bytes"
	"io"
	"net"
	"sync"
	"time"

	"github.com/apernet/hysteria/core/v2/client"
)

type MockEchoHyClient struct{}

func (c *MockEchoHyClient) TCP(addr string) (net.Conn, error) {
	return &mockEchoTCPConn{
		BufChan: make(chan []byte, 10),
		done:    make(chan struct{}),
	}, nil
}

func (c *MockEchoHyClient) UDP() (client.HyUDPConn, error) {
	return &mockEchoUDPConn{
		BufChan: make(chan mockEchoUDPPacket, 10),
		done:    make(chan struct{}),
	}, nil
}

func (c *MockEchoHyClient) Close() error {
	return nil
}

type mockEchoTCPConn struct {
	BufChan   chan []byte
	done      chan struct{}
	closeOnce sync.Once
}

func (c *mockEchoTCPConn) Read(b []byte) (n int, err error) {
	select {
	case buf := <-c.BufChan:
		return copy(b, buf), nil
	case <-c.done:
		return 0, io.EOF
	}
}

func (c *mockEchoTCPConn) Write(b []byte) (n int, err error) {
	select {
	case <-c.done:
		return 0, net.ErrClosed
	case c.BufChan <- bytes.Clone(b):
		return len(b), nil
	}
}

func (c *mockEchoTCPConn) Close() error {
	c.closeOnce.Do(func() { close(c.done) })
	return nil
}

func (c *mockEchoTCPConn) LocalAddr() net.Addr {
	// Not implemented
	return nil
}

func (c *mockEchoTCPConn) RemoteAddr() net.Addr {
	// Not implemented
	return nil
}

func (c *mockEchoTCPConn) SetDeadline(t time.Time) error {
	// Not implemented
	return nil
}

func (c *mockEchoTCPConn) SetReadDeadline(t time.Time) error {
	// Not implemented
	return nil
}

func (c *mockEchoTCPConn) SetWriteDeadline(t time.Time) error {
	// Not implemented
	return nil
}

type mockEchoUDPPacket struct {
	Data []byte
	Addr string
}

type mockEchoUDPConn struct {
	BufChan   chan mockEchoUDPPacket
	done      chan struct{}
	closeOnce sync.Once
}

func (c *mockEchoUDPConn) Receive() ([]byte, string, error) {
	select {
	case p := <-c.BufChan:
		return p.Data, p.Addr, nil
	case <-c.done:
		return nil, "", io.EOF
	}
}

func (c *mockEchoUDPConn) Send(b []byte, addr string) error {
	select {
	case <-c.done:
		return net.ErrClosed
	case c.BufChan <- mockEchoUDPPacket{Data: bytes.Clone(b), Addr: addr}:
		return nil
	}
}

func (c *mockEchoUDPConn) Close() error {
	c.closeOnce.Do(func() { close(c.done) })
	return nil
}
