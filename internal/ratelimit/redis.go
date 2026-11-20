package ratelimit

import (
	"context"
	_ "embed"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/nishamurthy-22/TokenGate/internal/config"
	"github.com/nishamurthy-22/TokenGate/internal/metrics"
)

//go:embed scripts/reserve.lua
var reserveSrc string

//go:embed scripts/settle.lua
var settleSrc string

const settledTTLSeconds = 600

// BudgetKey is the Redis key holding an application's token bucket.
func BudgetKey(appID string) string { return "budget:" + appID }

func settledKey(reqID string) string { return "settled:" + reqID }

// NewRedisClient creates a Redis client sized for many concurrent requests.
func NewRedisClient(addr string) *redis.Client {
	return redis.NewClient(&redis.Options{
		Addr:         addr,
		PoolSize:     256,
		DialTimeout:  time.Second,
		ReadTimeout:  time.Second,
		WriteTimeout: time.Second,
	})
}

// RedisLimiter keeps shared token buckets in Redis and updates them with
// atomic Lua scripts, so all gateways see one consistent budget.
type RedisLimiter struct {
	client     *redis.Client
	quotas     map[string]config.Quota
	gatewayID  string
	timeout    time.Duration
	localClock bool
	skew       time.Duration
	reserve    *redis.Script
	settle     *redis.Script
}

// NewRedis creates a Redis-backed limiter.
func NewRedis(opts Options) (*RedisLimiter, error) {
	if opts.ClockSource != "" && opts.ClockSource != "redis" && opts.ClockSource != "local" {
		return nil, fmt.Errorf("unknown clock source %q", opts.ClockSource)
	}
	return &RedisLimiter{
		client:     NewRedisClient(opts.RedisAddr),
		quotas:     opts.Quotas,
		gatewayID:  opts.GatewayID,
		timeout:    opts.Timeout,
		localClock: opts.ClockSource == "local",
		skew:       opts.ClockSkew,
		reserve:    redis.NewScript(reserveSrc),
		settle:     redis.NewScript(settleSrc),
	}, nil
}

// Client exposes the Redis client (used by the benchmark to reset keys).
func (l *RedisLimiter) Client() *redis.Client { return l.client }

// now returns the time argument for the scripts: empty means the scripts use
// the Redis clock; otherwise the gateway's own clock plus the configured skew.
func (l *RedisLimiter) now() string {
	if !l.localClock {
		return ""
	}
	t := time.Now().Add(l.skew)
	return strconv.FormatFloat(float64(t.UnixNano())/1e9, 'f', 6, 64)
}

func (l *RedisLimiter) observe(op string, start time.Time, err error) {
	metrics.RedisLatency.WithLabelValues(l.gatewayID, op).Observe(time.Since(start).Seconds())
	if err != nil {
		metrics.RedisErrorsTotal.WithLabelValues(l.gatewayID, op).Inc()
	}
}

// Reserve implements Limiter.
func (l *RedisLimiter) Reserve(ctx context.Context, appID, reqID string, cost int) (bool, error) {
	q, ok := l.quotas[appID]
	if !ok {
		return false, fmt.Errorf("unknown app %q", appID)
	}
	ctx, cancel := context.WithTimeout(ctx, l.timeout)
	defer cancel()

	start := time.Now()
	res, err := l.reserve.Run(ctx, l.client, []string{BudgetKey(appID)},
		q.Capacity, q.RefillPerSec, cost, l.now()).Slice()
	l.observe("reserve", start, err)
	if err != nil {
		return false, err
	}
	if len(res) == 0 {
		return false, fmt.Errorf("unexpected reply from reserve script")
	}
	allowed, _ := res[0].(int64)
	return allowed == 1, nil
}

// Settle implements Limiter. It is idempotent per request ID.
func (l *RedisLimiter) Settle(ctx context.Context, appID, reqID string, reserved, used int) error {
	q, ok := l.quotas[appID]
	if !ok {
		return fmt.Errorf("unknown app %q", appID)
	}
	ctx, cancel := context.WithTimeout(ctx, l.timeout)
	defer cancel()

	start := time.Now()
	err := l.settle.Run(ctx, l.client, []string{BudgetKey(appID), settledKey(reqID)},
		q.Capacity, q.RefillPerSec, reserved, used, settledTTLSeconds, l.now()).Err()
	l.observe("settle", start, err)
	return err
}
