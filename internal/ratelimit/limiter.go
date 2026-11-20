// Package ratelimit implements per-application token budgets.
// See docs/DESIGN.md, sections 5 and 6.
package ratelimit

import (
	"context"
	"fmt"
	"time"

	"github.com/nishamurthy-22/TokenGate/internal/config"
)

// Limiter holds and settles token budgets.
type Limiter interface {
	// Reserve holds cost tokens for appID. It returns false if the budget is
	// too low. A non-nil error means the budget store could not be reached.
	Reserve(ctx context.Context, appID, reqID string, cost int) (bool, error)

	// Settle reports actual usage. It refunds reserved-used, or charges the
	// extra if used > reserved.
	Settle(ctx context.Context, appID, reqID string, reserved, used int) error
}

// Options configures a limiter.
type Options struct {
	Quotas       map[string]config.Quota
	RedisAddr    string
	GatewayID    string
	GatewayCount int           // used by the local limiter to split budgets
	ClockSource  string        // "redis" or "local"
	ClockSkew    time.Duration // added to the local clock (drift experiment)
	Timeout      time.Duration // per Redis call; 50 ms if zero
}

// New creates a limiter by name: redis, naive, local or none.
func New(kind string, opts Options) (Limiter, error) {
	if opts.Timeout == 0 {
		opts.Timeout = 50 * time.Millisecond
	}
	switch kind {
	case "redis":
		return NewRedis(opts)
	case "naive":
		return NewNaive(opts)
	case "local":
		return NewLocal(opts.Quotas, opts.GatewayCount), nil
	case "none":
		return Noop{}, nil
	}
	return nil, fmt.Errorf("unknown limiter %q", kind)
}

// Noop admits everything. Used to measure the system without rate limiting.
type Noop struct{}

func (Noop) Reserve(context.Context, string, string, int) (bool, error) { return true, nil }

func (Noop) Settle(context.Context, string, string, int, int) error { return nil }
