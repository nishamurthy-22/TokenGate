package ratelimit

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/nishamurthy-22/TokenGate/internal/config"
	"github.com/nishamurthy-22/TokenGate/internal/metrics"
)

// NaiveBudgetKey is the Redis key used by the naive limiter.
func NaiveBudgetKey(appID string) string { return "naive:budget:" + appID }

// NaiveLimiter stores budgets in Redis but reads and writes them in separate
// steps. Two gateways can read the same balance and both spend it, so it
// overshoots under concurrency. It exists only as a baseline for the
// rate-limit correctness experiment.
type NaiveLimiter struct {
	client    *redis.Client
	quotas    map[string]config.Quota
	gatewayID string
	timeout   time.Duration
}

// NewNaive creates the naive limiter.
func NewNaive(opts Options) (*NaiveLimiter, error) {
	return &NaiveLimiter{
		client:    NewRedisClient(opts.RedisAddr),
		quotas:    opts.Quotas,
		gatewayID: opts.GatewayID,
		timeout:   opts.Timeout,
	}, nil
}

// Client exposes the Redis client (used by the benchmark to reset keys).
func (l *NaiveLimiter) Client() *redis.Client { return l.client }

// Reserve implements Limiter without atomicity: read, compute, then write.
func (l *NaiveLimiter) Reserve(ctx context.Context, appID, reqID string, cost int) (bool, error) {
	q, ok := l.quotas[appID]
	if !ok {
		return false, fmt.Errorf("unknown app %q", appID)
	}
	ctx, cancel := context.WithTimeout(ctx, l.timeout)
	defer cancel()
	start := time.Now()
	defer func() {
		metrics.RedisLatency.WithLabelValues(l.gatewayID, "reserve").Observe(time.Since(start).Seconds())
	}()

	key := NaiveBudgetKey(appID)
	vals, err := l.client.HMGet(ctx, key, "tokens", "ts").Result()
	if err != nil {
		metrics.RedisErrorsTotal.WithLabelValues(l.gatewayID, "reserve").Inc()
		return false, err
	}
	now := float64(time.Now().UnixNano()) / 1e9
	tokens, ts := q.Capacity, now
	if t, ok := parseFloat(vals[0]); ok {
		tokens = t
		if s, ok := parseFloat(vals[1]); ok {
			ts = s
		}
	}
	if now > ts {
		tokens = math.Min(q.Capacity, tokens+(now-ts)*q.RefillPerSec)
		ts = now
	}
	if tokens < float64(cost) {
		return false, nil
	}
	err = l.client.HSet(ctx, key, "tokens", tokens-float64(cost), "ts", ts).Err()
	if err != nil {
		metrics.RedisErrorsTotal.WithLabelValues(l.gatewayID, "reserve").Inc()
		return false, err
	}
	return true, nil
}

// Settle adds the difference back without a capacity check or idempotency.
func (l *NaiveLimiter) Settle(ctx context.Context, appID, reqID string, reserved, used int) error {
	ctx, cancel := context.WithTimeout(ctx, l.timeout)
	defer cancel()
	return l.client.HIncrByFloat(ctx, NaiveBudgetKey(appID), "tokens", float64(reserved-used)).Err()
}

func parseFloat(v any) (float64, bool) {
	s, ok := v.(string)
	if !ok {
		return 0, false
	}
	f, err := strconv.ParseFloat(s, 64)
	return f, err == nil
}
