package trafficlogger

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/apernet/hysteria/core/v2/server"
)

type accountBudget struct {
	budget    *server.PayloadBudget
	active    int
	idleUntil time.Time
}

// Retain spent tokens across short reconnects. At 1 Mbps, a full bucket
// replenishes in less than a second, so longer idle gaps need no history.
func (s *trafficStatsServerImpl) AcquireBandwidth(id int64, policy server.BandwidthPolicy, revision uint64) (*server.PayloadBudget, func(), bool) {
	s.Mutex.Lock()
	defer s.Mutex.Unlock()
	if id <= 0 || !policy.Valid() || s.revision != revision {
		return nil, nil, false
	}
	now := time.Now()
	if !now.Before(s.nextBudgetSweep) {
		for key, entry := range s.budgets {
			if entry.active == 0 && !now.Before(entry.idleUntil) {
				delete(s.budgets, key)
			}
		}
		s.nextBudgetSweep = now.Add(time.Second)
	}
	entry := s.budgets[id]
	if entry == nil {
		if len(s.budgets) >= maxBatchIdentities {
			return nil, nil, false
		}
		entry = &accountBudget{budget: server.NewPayloadBudget(policy)}
		s.budgets[id] = entry
	} else if !entry.budget.Apply(policy) {
		return nil, nil, false
	}
	entry.active++
	return entry.budget, func() {
		s.Mutex.Lock()
		defer s.Mutex.Unlock()
		entry.active--
		if entry.active == 0 {
			entry.idleUntil = time.Now().Add(time.Second)
		}
	}, true
}

func (s *trafficStatsServerImpl) updateBandwidthPolicy(w http.ResponseWriter, r *http.Request) {
	// This optional endpoint must never be available in secret-less setups.
	if s.Secret == "" {
		http.Error(w, "authorization required", http.StatusUnauthorized)
		return
	}
	var update struct {
		AccountID int64 `json:"accountId"`
		server.BandwidthPolicy
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&update) != nil ||
		update.AccountID <= 0 || !update.Valid() {
		http.Error(w, "invalid bandwidth policy", http.StatusBadRequest)
		return
	}
	s.Mutex.Lock()
	defer s.Mutex.Unlock()
	if entry := s.budgets[update.AccountID]; entry != nil && !entry.budget.Apply(update.BandwidthPolicy) {
		http.Error(w, "stale or conflicting bandwidth policy", http.StatusConflict)
		return
	}
	// An offline account has no budget to update. Fence callbacks begun
	// before this acknowledgement so they must reread persisted policy.
	s.revision++
	w.WriteHeader(http.StatusNoContent)
}
