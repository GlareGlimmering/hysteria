package server

import (
	"context"
	"errors"
	"io"
	"math"
	"net"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

const (
	MaxAccountMbps = 100000
	// Keep one maximum-size UDP payload admissible even at the lowest rate.
	payloadBurst      = 65535
	maxPayloadWaiters = 128
)

var ErrPayloadWaitLimit = errors.New("account bandwidth wait limit reached")

type BandwidthPolicy struct {
	Version      uint64 `json:"version"`
	UploadMbps   int    `json:"uploadMbps"`
	DownloadMbps int    `json:"downloadMbps"`
}

func (p BandwidthPolicy) Valid() bool {
	return p.Version > 0 && p.UploadMbps >= 0 && p.UploadMbps <= MaxAccountMbps &&
		p.DownloadMbps >= 0 && p.DownloadMbps <= MaxAccountMbps
}

type BandwidthAuthenticator interface {
	AuthenticateWithBandwidth(net.Addr, string, uint64) (bool, string, int, int64, *BandwidthPolicy)
}

type BandwidthRegistry interface {
	AcquireBandwidth(int64, BandwidthPolicy, uint64) (*PayloadBudget, func(), bool)
}

// PayloadBudget is shared by all authenticated connections of one account.
type PayloadBudget struct {
	mu               sync.Mutex
	policy           BandwidthPolicy
	upload, download *rate.Limiter
	changed          chan struct{}
	waiters          int
}

func NewPayloadBudget(policy BandwidthPolicy) *PayloadBudget {
	return &PayloadBudget{
		policy:   policy,
		upload:   rate.NewLimiter(payloadRate(policy.UploadMbps), payloadBurst),
		download: rate.NewLimiter(payloadRate(policy.DownloadMbps), payloadBurst),
		changed:  make(chan struct{}),
	}
}

func payloadRate(mbps int) rate.Limit {
	if mbps == 0 {
		return rate.Inf
	}
	return rate.Limit(mbps) * 125000
}

// Apply returns false for invalid, stale or conflicting policy updates.
func (b *PayloadBudget) Apply(policy BandwidthPolicy) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !policy.Valid() || policy.Version < b.policy.Version ||
		(policy.Version == b.policy.Version && policy != b.policy) {
		return false
	}
	if policy == b.policy {
		return true
	}
	now := time.Now()
	b.upload.SetLimitAt(now, payloadRate(policy.UploadMbps))
	b.download.SetLimitAt(now, payloadRate(policy.DownloadMbps))
	b.policy = policy
	close(b.changed)
	b.changed = make(chan struct{})
	return true
}

func (b *PayloadBudget) direction(upload bool) *rate.Limiter {
	if upload {
		return b.upload
	}
	return b.download
}

func (b *PayloadBudget) AllowAt(now time.Time, upload bool, n int) bool {
	if b == nil {
		return true
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.direction(upload).AllowN(now, n)
}

func (b *PayloadBudget) Wait(ctx context.Context, upload bool, n int) error {
	if b == nil {
		return ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if b.AllowAt(time.Now(), upload, n) {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	queued := false
	defer func() {
		if queued {
			b.mu.Lock()
			b.waiters--
			b.mu.Unlock()
		}
	}()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		b.mu.Lock()
		now := time.Now()
		limiter := b.direction(upload)
		if limiter.AllowN(now, n) {
			b.mu.Unlock()
			return nil
		}
		if !queued {
			if b.waiters >= maxPayloadWaiters {
				b.mu.Unlock()
				return ErrPayloadWaitLimit
			}
			b.waiters++
			queued = true
		}
		changed := b.changed
		delay := time.Duration(math.Ceil((float64(n) - limiter.TokensAt(now)) / float64(limiter.Limit()) * float64(time.Second)))
		b.mu.Unlock()
		timer := time.NewTimer(max(delay, time.Millisecond))
		select {
		case <-ctx.Done():
		case <-changed:
		case <-timer.C:
		}
		timer.Stop()
	}
}

type payloadWriter struct {
	io.ReadWriter
	ctx    context.Context
	budget *PayloadBudget
	upload bool
}

func (w payloadWriter) Write(data []byte) (int, error) {
	total := 0
	for len(data) != 0 {
		chunk := data[:min(len(data), 4096)]
		if err := w.budget.Wait(w.ctx, w.upload, len(chunk)); err != nil {
			return total, err
		}
		n, err := w.ReadWriter.Write(chunk)
		total += n
		if err != nil {
			return total, err
		}
		if n != len(chunk) {
			return total, io.ErrShortWrite
		}
		data = data[n:]
	}
	return total, nil
}
