package server

import (
	"errors"
	"net"
	"sync"
	"testing"
)

type mapOutboundSelector map[string]struct {
	name     string
	outbound Outbound
}

func (s mapOutboundSelector) Select(authID string) (string, Outbound, bool) {
	entry, ok := s[authID]
	return entry.name, entry.outbound, ok
}

type recordingOutbound struct {
	mu         sync.Mutex
	tcpCalls   int
	udpCalls   int
	checkCalls int
	fail       bool
}

func (o *recordingOutbound) TCP(string) (net.Conn, error) {
	o.mu.Lock()
	o.tcpCalls++
	o.mu.Unlock()
	if o.fail {
		return nil, errors.New("outbound unavailable")
	}
	left, right := net.Pipe()
	_ = right.Close()
	return left, nil
}

func (o *recordingOutbound) UDP(string) (UDPConn, error) {
	o.mu.Lock()
	o.udpCalls++
	o.mu.Unlock()
	if o.fail {
		return nil, errors.New("outbound unavailable")
	}
	return &recordingUDPConn{}, nil
}

func (o *recordingOutbound) CheckUDP(string) error {
	o.mu.Lock()
	o.checkCalls++
	o.mu.Unlock()
	if o.fail {
		return errors.New("outbound unavailable")
	}
	return nil
}

func (o *recordingOutbound) calls() (int, int, int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.tcpCalls, o.udpCalls, o.checkCalls
}

type recordingUDPConn struct{}

func (*recordingUDPConn) ReadFrom([]byte) (int, string, error)    { return 0, "", errors.New("closed") }
func (*recordingUDPConn) WriteTo(b []byte, _ string) (int, error) { return len(b), nil }
func (*recordingUDPConn) Close() error                            { return nil }

func TestUserOutboundSelectionUsesSameBindingForTCPAndUDP(t *testing.T) {
	direct := &recordingOutbound{}
	tm1 := &recordingOutbound{}
	fallback := &recordingOutbound{}
	selector := mapOutboundSelector{
		"test_direct": {name: "DIRECT", outbound: direct},
		"test_tm1":    {name: "TM_NET_1", outbound: tm1},
	}

	for _, tc := range []struct {
		id   string
		name string
		want *recordingOutbound
	}{
		{id: "test_direct", name: "DIRECT", want: direct},
		{id: "test_tm1", name: "TM_NET_1", want: tm1},
	} {
		t.Run(tc.id, func(t *testing.T) {
			h := &h3sHandler{config: &Config{Outbound: fallback, OutboundSelector: selector}, outbound: fallback, outboundName: "default"}
			if !h.selectOutbound(tc.id) || h.outboundName != tc.name {
				t.Fatalf("selection failed: name=%q", h.outboundName)
			}
			conn, err := h.outbound.TCP("example.com:443")
			if err != nil {
				t.Fatal(err)
			}
			_ = conn.Close()
			udp, err := h.outbound.UDP("example.com:53")
			if err != nil {
				t.Fatal(err)
			}
			_ = udp.Close()
			if err = h.outbound.CheckUDP("example.com:53"); err != nil {
				t.Fatal(err)
			}
		})
	}

	if tcp, udp, check := direct.calls(); tcp != 1 || udp != 1 || check != 1 {
		t.Fatalf("DIRECT calls = %d/%d/%d", tcp, udp, check)
	}
	if tcp, udp, check := tm1.calls(); tcp != 1 || udp != 1 || check != 1 {
		t.Fatalf("TM_NET_1 calls = %d/%d/%d", tcp, udp, check)
	}
	if tcp, udp, check := fallback.calls(); tcp != 0 || udp != 0 || check != 0 {
		t.Fatalf("fallback was used: %d/%d/%d", tcp, udp, check)
	}
}

func TestUnboundIdentityIsFailClosed(t *testing.T) {
	fallback := &recordingOutbound{}
	h := &h3sHandler{
		config:       &Config{Outbound: fallback, OutboundSelector: mapOutboundSelector{}},
		outbound:     fallback,
		outboundName: "default",
	}
	if h.selectOutbound("test_unbound") {
		t.Fatal("unbound identity was accepted")
	}
	if h.outbound != nil || h.outboundName != "" {
		t.Fatal("unbound identity retained a fallback outbound")
	}
	if tcp, udp, check := fallback.calls(); tcp != 0 || udp != 0 || check != 0 {
		t.Fatalf("fallback was used: %d/%d/%d", tcp, udp, check)
	}
}

func TestFailedSelectedOutboundNeverFallsBack(t *testing.T) {
	tm1 := &recordingOutbound{fail: true}
	fallback := &recordingOutbound{}
	h := &h3sHandler{
		config: &Config{Outbound: fallback, OutboundSelector: mapOutboundSelector{
			"test_tm1": {name: "TM_NET_1", outbound: tm1},
		}},
		outbound:     fallback,
		outboundName: "default",
	}
	if !h.selectOutbound("test_tm1") {
		t.Fatal("bound identity was rejected")
	}
	if _, err := h.outbound.TCP("example.com:443"); err == nil {
		t.Fatal("TCP failure was hidden")
	}
	if _, err := h.outbound.UDP("example.com:53"); err == nil {
		t.Fatal("UDP failure was hidden")
	}
	if err := h.outbound.CheckUDP("example.com:53"); err == nil {
		t.Fatal("UDP check failure was hidden")
	}
	if tcp, udp, check := fallback.calls(); tcp != 0 || udp != 0 || check != 0 {
		t.Fatalf("fallback was used after TM_NET_1 failure: %d/%d/%d", tcp, udp, check)
	}
}

func TestUserOutboundSelectorConcurrentReads(t *testing.T) {
	selector := mapOutboundSelector{"test_tm1": {name: "TM_NET_1", outbound: &recordingOutbound{}}}
	var wg sync.WaitGroup
	for range 100 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			h := &h3sHandler{config: &Config{OutboundSelector: selector}}
			if !h.selectOutbound("test_tm1") {
				t.Error("selection failed")
			}
		}()
	}
	wg.Wait()
}

func TestFailedUserOutboundDoesNotAffectOtherUserOrFallback(t *testing.T) {
	direct := &recordingOutbound{}
	broken := &recordingOutbound{fail: true}
	fallback := &recordingOutbound{}
	selector := mapOutboundSelector{
		"test_direct":  {name: "DIRECT", outbound: direct},
		"test_failure": {name: "BROKEN_OUTBOUND", outbound: broken},
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		h := &h3sHandler{config: &Config{Outbound: fallback, OutboundSelector: selector}}
		if !h.selectOutbound("test_direct") {
			t.Error("DIRECT selection failed")
			return
		}
		if _, err := h.outbound.TCP("example.com:443"); err != nil {
			t.Errorf("DIRECT TCP unexpectedly failed: %v", err)
		}
		if _, err := h.outbound.UDP("example.com:53"); err != nil {
			t.Errorf("DIRECT UDP unexpectedly failed: %v", err)
		}
	}()
	go func() {
		defer wg.Done()
		h := &h3sHandler{config: &Config{Outbound: fallback, OutboundSelector: selector}}
		if !h.selectOutbound("test_failure") {
			t.Error("BROKEN_OUTBOUND selection failed")
			return
		}
		if _, err := h.outbound.TCP("example.com:443"); err == nil {
			t.Error("broken TCP outbound unexpectedly succeeded")
		}
		if _, err := h.outbound.UDP("example.com:53"); err == nil {
			t.Error("broken UDP outbound unexpectedly succeeded")
		}
	}()
	wg.Wait()

	if tcp, udp, check := direct.calls(); tcp != 1 || udp != 1 || check != 0 {
		t.Fatalf("DIRECT calls = %d/%d/%d, want 1/1/0", tcp, udp, check)
	}
	if tcp, udp, check := broken.calls(); tcp != 1 || udp != 1 || check != 0 {
		t.Fatalf("BROKEN_OUTBOUND calls = %d/%d/%d, want 1/1/0", tcp, udp, check)
	}
	if tcp, udp, check := fallback.calls(); tcp != 0 || udp != 0 || check != 0 {
		t.Fatalf("fallback was used during isolated failure: %d/%d/%d", tcp, udp, check)
	}
}
