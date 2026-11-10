package trafficlogger

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"time"
)

const (
	settlementLease    = 90 * time.Second
	maxBatchIdentities = 65536
)

type batchReceipt struct {
	Epoch    string `json:"epoch"`
	Sequence uint64 `json:"sequence"`
}

type trafficBatch struct {
	batchReceipt
	Users map[string]*trafficStatsEntry `json:"users"`
}

func newEpoch() string {
	var data [16]byte
	rand.Read(data[:]) // Go 1.26 terminates the process if OS randomness fails.
	return hex.EncodeToString(data[:])
}

func (s *trafficStatsServerImpl) getBatch(w http.ResponseWriter) {
	s.Mutex.Lock()
	if s.pending == nil {
		s.sequence++
		s.pending = &trafficBatch{batchReceipt{s.epoch, s.sequence}, s.StatsMap}
		s.StatsMap = make(map[string]*trafficStatsEntry)
	}
	batch := s.pending
	s.Mutex.Unlock()
	// The pending map is immutable; an ack does not invalidate this response.
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(batch)
}

func (s *trafficStatsServerImpl) ackBatch(w http.ResponseWriter, r *http.Request) {
	var receipt batchReceipt
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&receipt) != nil {
		http.Error(w, "invalid receipt", http.StatusBadRequest)
		return
	}
	s.Mutex.Lock()
	defer s.Mutex.Unlock()
	if receipt.Epoch != s.epoch || receipt.Sequence == 0 || receipt.Sequence > s.sequence {
		http.Error(w, "unknown receipt", http.StatusConflict)
		return
	}
	if s.pending != nil && receipt.Sequence == s.pending.Sequence {
		s.pending = nil
		s.lastAck = time.Now()
	}
	w.WriteHeader(http.StatusNoContent)
}

type trackedConnection struct {
	close func()
}

func (s *trafficStatsServerImpl) AdmissionRevision() uint64 {
	s.Mutex.RLock()
	defer s.Mutex.RUnlock()
	return s.revision
}

func (s *trafficStatsServerImpl) RegisterConnection(id string, limit int, revision uint64, closeConnection func()) (func(), bool) {
	s.Mutex.Lock()
	defer s.Mutex.Unlock()
	if s.paused || revision != s.revision || (limit >= 0 && (s.lastAck.IsZero() || time.Since(s.lastAck) >= settlementLease || s.OnlineMap[id] >= limit)) {
		return nil, false
	}
	conn := &trackedConnection{close: closeConnection}
	if s.connections[id] == nil {
		s.connections[id] = make(map[*trackedConnection]struct{})
	}
	s.connections[id][conn] = struct{}{}
	s.OnlineMap[id]++
	return func() {
		s.Mutex.Lock()
		defer s.Mutex.Unlock()
		if _, exists := s.connections[id][conn]; !exists {
			return
		}
		delete(s.connections[id], conn)
		s.OnlineMap[id]--
		if len(s.connections[id]) == 0 {
			delete(s.connections, id)
			delete(s.OnlineMap, id)
		}
	}, true
}

func (s *trafficStatsServerImpl) closeConnections(ids []string) {
	if len(ids) == 0 {
		return
	}
	s.Mutex.Lock()
	s.revision++
	var closing []*trackedConnection
	for _, id := range ids {
		for conn := range s.connections[id] {
			closing = append(closing, conn)
		}
	}
	s.Mutex.Unlock()
	for _, conn := range closing {
		conn.close()
	}
}
