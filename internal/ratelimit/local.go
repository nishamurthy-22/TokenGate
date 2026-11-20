package ratelimit

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/nishamurthy-22/TokenGate/internal/config"
)

type bucket struct {
	tokens float64
	ts     time.Time
}

// LocalLimiter keeps token buckets in this gateway's memory. Each gateway gets
// capacity/N and refill/N, where N is the number of gateways. No coordination,
// so it is fast and keeps working when Redis is down, but the global limit is
// only approximate. Used as the comparison variant and for fail_local.
type LocalLimiter struct {
	mu      sync.Mutex
	quotas  map[string]config.Quota
	share   float64
	buckets map[string]*bucket
	now     func() time.Time
}

// NewLocal creates a local limiter for a deployment of gatewayCount gateways.
func NewLocal(quotas map[string]config.Quota, gatewayCount int) *LocalLimiter {
	if gatewayCount < 1 {
		gatewayCount = 1
	}
	return &LocalLimiter{
		quotas:  quotas,
		share:   1 / float64(gatewayCount),
		buckets: map[string]*bucket{},
		now:     time.Now,
	}
}

// refill returns the app's bucket after refilling it. Caller holds l.mu.
func (l *LocalLimiter) refill(appID string) (*bucket, float64, error) {
	q, ok := l.quotas[appID]
	if !ok {
		return nil, 0, fmt.Errorf("unknown app %q", appID)
	}
	capacity := q.Capacity * l.share
	now := l.now()
	b, ok := l.buckets[appID]
	if !ok {
		b = &bucket{tokens: capacity, ts: now}
		l.buckets[appID] = b
	}
	if elapsed := now.Sub(b.ts).Seconds(); elapsed > 0 {
		b.tokens = math.Min(capacity, b.tokens+elapsed*q.RefillPerSec*l.share)
		b.ts = now
	}
	return b, capacity, nil
}

// Reserve implements Limiter.
func (l *LocalLimiter) Reserve(ctx context.Context, appID, reqID string, cost int) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	b, _, err := l.refill(appID)
	if err != nil {
		return false, err
	}
	if b.tokens < float64(cost) {
		return false, nil
	}
	b.tokens -= float64(cost)
	return true, nil
}

// Settle implements Limiter.
func (l *LocalLimiter) Settle(ctx context.Context, appID, reqID string, reserved, used int) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	b, capacity, err := l.refill(appID)
	if err != nil {
		return err
	}
	b.tokens = math.Min(capacity, b.tokens+float64(reserved-used))
	return nil
}
