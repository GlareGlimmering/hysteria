package trafficlogger

import (
	"crypto/tls"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/apernet/hysteria/core/v2/client"
	"github.com/apernet/hysteria/core/v2/server"
	"github.com/apernet/hysteria/extras/v2/auth"
	"github.com/stretchr/testify/require"
)

func settlementServer(t *testing.T) (TrafficStatsServer, func(string) (client.Client, error)) {
	t.Helper()
	return settlementServerWithBandwidth(t, nil)
}

func settlementServerWithBandwidth(t *testing.T, policy *server.BandwidthPolicy, hooks ...server.RequestHook) (TrafficStatsServer, func(string) (client.Client, error)) {
	t.Helper()
	stats := NewTrafficStatsServer("fixture")
	first := callStats(stats, "GET", "/traffic/batch", "")
	// Let the legacy implementation reach the admissions assertion too.
	if first.Code == http.StatusOK {
		var receipt batchReceipt
		require.NoError(t, json.Unmarshal(first.Body.Bytes(), &receipt))
		body, err := json.Marshal(receipt)
		require.NoError(t, err)
		require.Equal(t, 204, callStats(stats, "POST", "/traffic/ack", string(body)).Code)
	}
	callback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Auth string }
		if json.NewDecoder(r.Body).Decode(&req) != nil {
			w.WriteHeader(400)
			return
		}
		limit := 3
		if req.Auth == "single" {
			limit = 1
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": req.Auth != "invalid", "id": req.Auth, "accountId": 42, "deviceLimit": limit, "bandwidth": policy,
		})
	}))
	t.Cleanup(callback.Close)
	cert, err := tls.LoadX509KeyPair("../../core/internal/integration_tests/test.crt", "../../core/internal/integration_tests/test.key")
	require.NoError(t, err)
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	var hook server.RequestHook
	if len(hooks) > 0 {
		hook = hooks[0]
	}
	srv, err := server.NewServer(&server.Config{
		RequestHook: hook,
		Conn:        conn, TrafficLogger: stats, Authenticator: auth.NewHTTPAuthenticator(callback.URL, false),
		TLSConfig: server.TLSConfig{Certificates: []tls.Certificate{cert}},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = srv.Close() })
	go srv.Serve()
	return stats, func(id string) (client.Client, error) {
		c, _, err := client.NewClient(&client.Config{
			ServerAddr: conn.LocalAddr(), Auth: id, TLSConfig: client.TLSConfig{InsecureSkipVerify: true},
		})
		return c, err
	}
}

func onlineCount(t *testing.T, s TrafficStatsServer, id string) int {
	t.Helper()
	var users map[string]int
	require.NoError(t, json.Unmarshal(callStats(s, "GET", "/online", "").Body.Bytes(), &users))
	return users[id]
}

func TestConcurrentQUICAdmissionReservesLastSlot(t *testing.T) {
	stats, dial := settlementServer(t)
	_, err := dial("invalid")
	require.Error(t, err)
	require.Zero(t, onlineCount(t, stats, "invalid"))
	const attempts = 8
	start := make(chan struct{})
	connections := make(chan client.Client, attempts)
	var wg sync.WaitGroup
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if c, err := dial("single"); err == nil {
				connections <- c
			}
		}()
	}
	close(start)
	wg.Wait()
	close(connections)
	var accepted []client.Client
	for c := range connections {
		accepted = append(accepted, c)
		t.Cleanup(func() { _ = c.Close() })
	}
	require.Len(t, accepted, 1)
	require.Equal(t, 1, onlineCount(t, stats, "single"))
	require.NoError(t, accepted[0].Close())
	require.Eventually(t, func() bool { return onlineCount(t, stats, "single") == 0 }, time.Second, 10*time.Millisecond)
	c, err := dial("single")
	require.NoError(t, err)
	require.NoError(t, c.Close())
}

func TestKickClosesAllIdleTCPAndUDPSessions(t *testing.T) {
	stats, dial := settlementServer(t)
	tcp, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer tcp.Close()
	go func() {
		for {
			c, err := tcp.Accept()
			if err != nil {
				return
			}
			go func() { defer c.Close(); _, _ = io.Copy(c, c) }()
		}
	}()
	udp, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	defer udp.Close()
	go func() {
		b := make([]byte, 64)
		for {
			n, a, err := udp.ReadFrom(b)
			if err != nil {
				return
			}
			_, _ = udp.WriteTo(b[:n], a)
		}
	}()
	var streams []net.Conn
	var sessions []client.HyUDPConn
	for i := 0; i < 2; i++ {
		c, err := dial("target")
		require.NoError(t, err)
		defer c.Close()
		stream, err := c.TCP(tcp.Addr().String())
		require.NoError(t, err)
		defer stream.Close()
		streams = append(streams, stream)
		session, err := c.UDP()
		require.NoError(t, err)
		defer session.Close()
		require.NoError(t, session.Send([]byte("fixture"), udp.LocalAddr().String()))
		ready := make(chan error, 1)
		go func() { _, _, err := session.Receive(); ready <- err }()
		select {
		case err := <-ready:
			require.NoError(t, err)
		case <-time.After(3 * time.Second):
			t.Fatal("UDP echo did not arrive")
		}
		sessions = append(sessions, session)
	}
	other, err := dial("other")
	require.NoError(t, err)
	defer other.Close()
	require.Equal(t, 2, onlineCount(t, stats, "target"))
	require.Equal(t, 200, callStats(stats, "POST", "/kick", `["target"]`).Code)
	require.Eventually(t, func() bool { return onlineCount(t, stats, "target") == 0 }, time.Second, 10*time.Millisecond)
	require.Equal(t, 1, onlineCount(t, stats, "other"))
	for _, stream := range streams {
		require.NoError(t, stream.SetReadDeadline(time.Now().Add(time.Second)))
		_, err := stream.Read(make([]byte, 1))
		require.Error(t, err)
		if timeout, ok := err.(net.Error); ok {
			require.False(t, timeout.Timeout())
		}
	}
	for _, session := range sessions {
		done := make(chan error, 1)
		go func() { _, _, err := session.Receive(); done <- err }()
		select {
		case err := <-done:
			require.Error(t, err)
		case <-time.After(time.Second):
			t.Fatal("idle UDP session remained active")
		}
	}
}
