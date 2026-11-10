package server

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestPayloadBudgetControlledClock(t *testing.T) {
	now := time.Unix(123, 0)
	b := NewPayloadBudget(BandwidthPolicy{Version: 1, UploadMbps: 1, DownloadMbps: 2})
	if !b.AllowAt(now, true, payloadBurst) || !b.AllowAt(now, false, payloadBurst) {
		t.Fatal("each direction should have an independent initial burst")
	}
	next := now.Add(8 * time.Millisecond)
	if b.AllowAt(next, true, 1001) || !b.AllowAt(next, true, 1000) || !b.AllowAt(next, false, 2000) {
		t.Fatal("refill or direction isolation failed")
	}
	other := NewPayloadBudget(BandwidthPolicy{Version: 1, UploadMbps: 1})
	if !other.AllowAt(next, true, payloadBurst) {
		t.Fatal("other account shared budget")
	}
}

func TestPayloadBudgetCancellationAndUpdate(t *testing.T) {
	b := NewPayloadBudget(BandwidthPolicy{Version: 2, UploadMbps: 1})
	if !b.AllowAt(time.Now(), true, payloadBurst) {
		t.Fatal("expected initial burst")
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- b.Wait(ctx, true, payloadBurst) }()
	deadline := time.Now().Add(200 * time.Millisecond)
	for {
		b.mu.Lock()
		waiting := b.waiters == 1
		b.mu.Unlock()
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("goroutine never entered bandwidth wait")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("wait returned %v", err)
	}
	if b.waiters != 0 {
		t.Fatal("canceled waiter leaked")
	}
	if b.Apply(BandwidthPolicy{Version: 1}) || b.Apply(BandwidthPolicy{Version: 2}) || !b.Apply(BandwidthPolicy{Version: 3}) {
		t.Fatal("stale/conflicting policy handling failed")
	}
	if err := b.Wait(context.Background(), true, payloadBurst); err != nil {
		t.Fatal(err)
	}
}

func BenchmarkPayloadBudgetParallel(b *testing.B) {
	budget := NewPayloadBudget(BandwidthPolicy{Version: 1})
	now := time.Now()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			budget.AllowAt(now, true, 4096)
		}
	})
}
