package trafficlogger

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/apernet/hysteria/core/v2/client"
	"github.com/apernet/hysteria/core/v2/server"
	"github.com/stretchr/testify/require"
)

func TestBandwidthRegistryRetainsBudgetOnReconnect(t *testing.T) {
	s := NewTrafficStatsServer("fixture").(*trafficStatsServerImpl)
	policy := server.BandwidthPolicy{Version: 1, UploadMbps: 1}
	first, release, ok := s.AcquireBandwidth(42, policy, s.AdmissionRevision())
	require.True(t, ok)
	release()
	next, release, ok := s.AcquireBandwidth(42, policy, s.AdmissionRevision())
	require.True(t, ok)
	require.Same(t, first, next)
	release()
	require.Equal(t, 0, s.budgets[42].active)
	s.budgets[42].idleUntil = time.Now().Add(-time.Second)
	s.nextBudgetSweep = time.Time{}
	_, release, ok = s.AcquireBandwidth(43, policy, s.AdmissionRevision())
	require.True(t, ok)
	defer release()
	require.NotContains(t, s.budgets, int64(42))
}

type putbackHook struct{}

func (putbackHook) Check(udp bool, _ string) bool { return !udp }
func (putbackHook) TCP(stream server.HyStream, _ *string) ([]byte, error) {
	payload := make([]byte, 128000)
	_, err := io.ReadFull(stream, payload)
	return payload, err
}
func (putbackHook) UDP(_ [][]byte, _ *string) (bool, error) { return true, nil }

func TestQUICBandwidthTCP(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		connections, streams int
		upload, download     int
	}{
		{"single-upload", 1, 1, 2, 0},
		{"upload-with-putback", 1, 1, 2, 0},
		{"single-download", 1, 1, 0, 2},
		{"aggregate-upload", 2, 10, 2, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var hooks []server.RequestHook
			if tc.name == "upload-with-putback" {
				hooks = append(hooks, putbackHook{})
			}
			_, dial := settlementServerWithBandwidth(t, &server.BandwidthPolicy{Version: 1, UploadMbps: tc.upload, DownloadMbps: tc.download}, hooks...)
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			require.NoError(t, err)
			defer listener.Close()
			go func() {
				for {
					conn, err := listener.Accept()
					if err != nil {
						return
					}
					go func() { defer conn.Close(); _, _ = io.Copy(conn, conn) }()
				}
			}()
			var conns []client.Client
			for range tc.connections {
				c, err := dial("bandwidth")
				require.NoError(t, err)
				defer c.Close()
				conns = append(conns, c)
			}
			var streams []net.Conn
			for i := range tc.streams {
				stream, err := conns[i%len(conns)].TCP(listener.Addr().String())
				require.NoError(t, err)
				defer stream.Close()
				require.NoError(t, stream.SetDeadline(time.Now().Add(10*time.Second)))
				streams = append(streams, stream)
			}
			const total = 512000
			payload := bytes.Repeat([]byte{17}, total/tc.streams)
			results := make(chan error, tc.streams)
			var workers sync.WaitGroup
			start := time.Now()
			for _, stream := range streams {
				workers.Go(func() {
					write := make(chan error, 1)
					go func() { _, err := stream.Write(payload); write <- err }()
					got := make([]byte, len(payload))
					_, err := io.ReadFull(stream, got)
					if writeErr := <-write; err == nil {
						err = writeErr
					}
					if err == nil && !bytes.Equal(got, payload) {
						err = fmt.Errorf("payload mismatch")
					}
					results <- err
				})
			}
			workers.Wait()
			elapsed := time.Since(start)
			close(results)
			for err := range results {
				require.NoError(t, err)
			}
			// A full 65535-byte burst is available at t=0. Require total
			// delivery to fit that envelope, with scheduler/clock tolerance.
			require.GreaterOrEqual(t, elapsed, 1700*time.Millisecond)
			t.Logf("%d QUIC connections, %d TCP streams, %d bytes in %v: %.3f Mbps",
				tc.connections, tc.streams, total, elapsed, float64(total)*8/elapsed.Seconds()/1e6)
		})
	}
}

func TestQUICBandwidthUDPAndLiveUpdate(t *testing.T) {
	stats, dial := settlementServerWithBandwidth(t, &server.BandwidthPolicy{Version: 1, UploadMbps: 1, DownloadMbps: 1})
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	defer conn.Close()
	go func() {
		buf := make([]byte, 8192)
		for {
			n, addr, err := conn.ReadFrom(buf)
			if err != nil {
				return
			}
			_, _ = conn.WriteTo(buf[:n], addr)
		}
	}()
	var sessions []client.HyUDPConn
	received := make(chan []byte, 64)
	for range 2 {
		c, err := dial("bandwidth")
		require.NoError(t, err)
		defer c.Close()
		s, err := c.UDP()
		require.NoError(t, err)
		defer s.Close()
		sessions = append(sessions, s)
		go func() {
			for {
				data, _, err := s.Receive()
				if err != nil {
					return
				}
				received <- data
			}
		}()
	}
	payload := bytes.Repeat([]byte{19}, 3500)
	// Packets exceed QUIC datagram size and exercise fragmentation.
	for i := range 32 {
		require.NoError(t, sessions[i%2].Send(payload, conn.LocalAddr().String()))
	}
	time.Sleep(250 * time.Millisecond)
	n := len(received)
	require.Greater(t, n, 0)
	require.Less(t, n, 32)
	for range n {
		require.Equal(t, payload, <-received)
	}
	control := httptest.NewServer(stats)
	defer control.Close()
	update := func(secret string) int {
		req, err := http.NewRequest(http.MethodPut, control.URL+"/bandwidth/policy", bytes.NewBufferString(`{"accountId":42,"version":2,"uploadMbps":0,"downloadMbps":0}`))
		require.NoError(t, err)
		req.Header.Set("Authorization", secret)
		resp, err := control.Client().Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		return resp.StatusCode
	}
	require.Equal(t, 401, update(""))
	start := time.Now()
	require.Equal(t, 204, update("fixture"))
	ack := time.Since(start)
	require.Equal(t, 409, callStats(stats, "PUT", "/bandwidth/policy", `{"accountId":42,"version":1,"uploadMbps":1,"downloadMbps":1}`).Code)
	for i := range 20 {
		require.NoError(t, sessions[i%2].Send(payload, conn.LocalAddr().String()))
		select {
		case got := <-received:
			require.Equal(t, payload, got)
		case <-time.After(time.Second):
			t.Fatal("updated policy did not pass UDP payload")
		}
	}
	t.Logf("limited burst echoed %d/32 whole datagrams; live update ack %v; 20/20 after update", n, ack)
}
