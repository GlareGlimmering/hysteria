package integration_tests

import (
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/apernet/hysteria/core/v2/client"
	coreErrors "github.com/apernet/hysteria/core/v2/errors"
	"github.com/apernet/hysteria/core/v2/server"
)

type pocAuthenticator struct{}

func (pocAuthenticator) Authenticate(_ net.Addr, auth string, _ uint64) (bool, string) {
	switch auth {
	case "test_direct", "test_tm1", "test_unbound":
		return true, auth
	default:
		return false, ""
	}
}

type pocSelector map[string]struct {
	name     string
	outbound server.Outbound
}

func (s pocSelector) Select(id string) (string, server.Outbound, bool) {
	entry, ok := s[id]
	return entry.name, entry.outbound, ok
}

type pocDirectOutbound struct {
	mu       sync.Mutex
	tcpCalls []string
	udpCalls []string
	fail     bool
}

func (o *pocDirectOutbound) TCP(addr string) (net.Conn, error) {
	o.mu.Lock()
	o.tcpCalls = append(o.tcpCalls, addr)
	fail := o.fail
	o.mu.Unlock()
	if fail {
		return nil, errors.New("selected outbound unavailable")
	}
	return net.DialTimeout("tcp", addr, 2*time.Second)
}

func (o *pocDirectOutbound) UDP(addr string) (server.UDPConn, error) {
	o.mu.Lock()
	o.udpCalls = append(o.udpCalls, addr)
	fail := o.fail
	o.mu.Unlock()
	if fail {
		return nil, errors.New("selected outbound unavailable")
	}
	conn, err := net.ListenUDP("udp", nil)
	if err != nil {
		return nil, err
	}
	return &pocUDPConn{UDPConn: conn}, nil
}

func (o *pocDirectOutbound) CheckUDP(string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.fail {
		return errors.New("selected outbound unavailable")
	}
	return nil
}

func (o *pocDirectOutbound) counts() (int, int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.tcpCalls), len(o.udpCalls)
}

type pocUDPConn struct{ *net.UDPConn }

func (c *pocUDPConn) ReadFrom(b []byte) (int, string, error) {
	n, addr, err := c.UDPConn.ReadFrom(b)
	if addr == nil {
		return n, "", err
	}
	return n, addr.String(), err
}

func (c *pocUDPConn) WriteTo(b []byte, addr string) (int, error) {
	resolved, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return 0, err
	}
	return c.UDPConn.WriteTo(b, resolved)
}

type pocEventLogger struct {
	mu       sync.Mutex
	requests map[string]string
}

func (l *pocEventLogger) Connect(net.Addr, string, uint64)            {}
func (l *pocEventLogger) Disconnect(net.Addr, string, error)          {}
func (l *pocEventLogger) TCPRequest(net.Addr, string, string)         {}
func (l *pocEventLogger) TCPError(net.Addr, string, string, error)    {}
func (l *pocEventLogger) UDPRequest(net.Addr, string, uint32, string) {}
func (l *pocEventLogger) UDPError(net.Addr, string, uint32, error)    {}
func (l *pocEventLogger) TCPErrorOutbound(net.Addr, string, string, string, error) {
}
func (l *pocEventLogger) UDPErrorOutbound(net.Addr, string, uint32, string, error) {
}

func (l *pocEventLogger) TCPRequestOutbound(_ net.Addr, id, _ string, outbound string) {
	l.record("tcp:"+id, outbound)
}

func (l *pocEventLogger) UDPRequestOutbound(_ net.Addr, id string, _ uint32, _ string, outbound string) {
	l.record("udp:"+id, outbound)
}

func (l *pocEventLogger) record(key, outbound string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.requests[key] = outbound
}

func (l *pocEventLogger) waitFor(t *testing.T, key, outbound string) {
	t.Helper()
	require.Eventually(t, func() bool {
		l.mu.Lock()
		defer l.mu.Unlock()
		return l.requests[key] == outbound
	}, 3*time.Second, 10*time.Millisecond)
}

func TestUserOutboundRoutingTCPUDPAndUnbound(t *testing.T) {
	udpConn, udpAddr, err := serverConn()
	require.NoError(t, err)
	direct := &pocDirectOutbound{}
	tm1 := &pocDirectOutbound{}
	fallback := &pocDirectOutbound{}
	events := &pocEventLogger{requests: make(map[string]string)}
	s, err := server.NewServer(&server.Config{
		TLSConfig:     serverTLSConfig(),
		Conn:          udpConn,
		Authenticator: pocAuthenticator{},
		Outbound:      fallback,
		OutboundSelector: pocSelector{
			"test_direct": {name: "DIRECT", outbound: direct},
			"test_tm1":    {name: "TM_NET_1", outbound: tm1},
		},
		EventLogger: events,
	})
	require.NoError(t, err)
	defer s.Close()
	go s.Serve()

	tcpListener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	tcpEcho := &tcpEchoServer{Listener: tcpListener}
	defer tcpEcho.Close()
	go tcpEcho.Serve()
	udpEchoConn, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	udpEcho := &udpEchoServer{Conn: udpEchoConn}
	defer udpEcho.Close()
	go udpEcho.Serve()

	for _, tc := range []struct {
		id       string
		outbound string
	}{
		{id: "test_direct", outbound: "DIRECT"},
		{id: "test_tm1", outbound: "TM_NET_1"},
	} {
		c, _, err := client.NewClient(&client.Config{
			ServerAddr: udpAddr,
			Auth:       tc.id,
			TLSConfig:  client.TLSConfig{InsecureSkipVerify: true},
		})
		require.NoError(t, err)

		tcp, err := c.TCP(tcpListener.Addr().String())
		require.NoError(t, err)
		payload := []byte(tc.id)
		_, err = tcp.Write(payload)
		require.NoError(t, err)
		got := make([]byte, len(payload))
		_, err = io.ReadFull(tcp, got)
		require.NoError(t, err)
		require.Equal(t, payload, got)
		require.NoError(t, tcp.Close())

		udp, err := c.UDP()
		require.NoError(t, err)
		require.NoError(t, udp.Send(payload, udpEchoConn.LocalAddr().String()))
		got, _, err = udp.Receive()
		require.NoError(t, err)
		require.Equal(t, payload, got)
		require.NoError(t, udp.Close())
		require.NoError(t, c.Close())

		events.waitFor(t, "tcp:"+tc.id, tc.outbound)
		events.waitFor(t, "udp:"+tc.id, tc.outbound)
	}

	c, _, err := client.NewClient(&client.Config{
		ServerAddr: udpAddr,
		Auth:       "test_unbound",
		TLSConfig:  client.TLSConfig{InsecureSkipVerify: true},
	})
	require.Nil(t, c)
	var authErr coreErrors.AuthError
	require.ErrorAs(t, err, &authErr)

	require.Equal(t, []int{1, 1}, func() []int { tcp, udp := direct.counts(); return []int{tcp, udp} }())
	require.Equal(t, []int{1, 1}, func() []int { tcp, udp := tm1.counts(); return []int{tcp, udp} }())
	require.Equal(t, []int{0, 0}, func() []int { tcp, udp := fallback.counts(); return []int{tcp, udp} }())
}
