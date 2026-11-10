package trafficlogger

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func callStats(s TrafficStatsServer, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Authorization", "fixture")
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	return rec
}

func TestSettlementReplaysUntilAck(t *testing.T) {
	s := NewTrafficStatsServer("fixture")
	s.LogTraffic("alice", 7, 11)
	if r := callStats(s, http.MethodGet, "/traffic?clear=1", ""); r.Code != 409 {
		t.Fatal("legacy request could destructively clear counters")
	}
	first := callStats(s, http.MethodGet, "/traffic/batch", "")
	if first.Code != 200 {
		t.Fatalf("snapshot status=%d", first.Code)
	}
	s.LogTraffic("alice", 3, 5)
	s.LogTraffic("bob", 13, 17)
	retry := callStats(s, http.MethodGet, "/traffic/batch", "")
	if first.Body.String() != retry.Body.String() {
		t.Fatal("lost response changed the pending batch")
	}
	var receipt struct {
		Epoch    string `json:"epoch"`
		Sequence uint64 `json:"sequence"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &receipt); err != nil || receipt.Epoch == "" || receipt.Sequence != 1 {
		t.Fatal("invalid batch identity")
	}
	ack, _ := json.Marshal(receipt)
	for i := 0; i < 2; i++ {
		if r := callStats(s, http.MethodPost, "/traffic/ack", string(ack)); r.Code != 204 {
			t.Fatalf("ack status=%d", r.Code)
		}
	}
	next := callStats(s, http.MethodGet, "/traffic/batch", "")
	if next.Code != 200 || !strings.Contains(next.Body.String(), `"bob":{"tx":13,"rx":17}`) {
		t.Fatal("post-snapshot traffic was lost")
	}
	if r := callStats(s, http.MethodPost, "/traffic/ack", string(ack)); r.Code != 204 {
		t.Fatal("old receipt must be an idempotent no-op")
	}
	if r := callStats(s, http.MethodGet, "/traffic/batch", ""); r.Body.String() != next.Body.String() {
		t.Fatal("old acknowledgment removed the newer batch")
	}
	restarted := NewTrafficStatsServer("fixture")
	if r := callStats(restarted, http.MethodPost, "/traffic/ack", string(ack)); r.Code != 409 {
		t.Fatal("receipt from the previous kernel generation accepted")
	}
}

func TestKickFencesPendingAuthenticationAndSettlementLeaseExpires(t *testing.T) {
	s := NewTrafficStatsServer("fixture").(*trafficStatsServerImpl)
	s.lastAck = time.Now()
	old := s.AdmissionRevision()
	if r := callStats(s, "POST", "/kick", `["alice"]`); r.Code != 200 {
		t.Fatal("kick request failed")
	}
	if _, ok := s.RegisterConnection("alice", 1, old, func() {}); ok {
		t.Fatal("in-flight authentication predating the kick was accepted")
	}
	release, ok := s.RegisterConnection("alice", 1, s.AdmissionRevision(), func() {})
	if !ok {
		t.Fatal("fresh authorization could not reconnect")
	}
	release()
	release()
	s.lastAck = time.Now().Add(-settlementLease)
	if s.LogTraffic("account:1", 1, 1) {
		t.Fatal("data remained permitted without committed settlement")
	}
	if _, ok := s.RegisterConnection("alice", 1, s.AdmissionRevision(), func() {}); ok {
		t.Fatal("expired settlement lease accepted a new connection")
	}
}
