package routing

import (
	"fmt"
	"sync/atomic"
)

// Policy chooses a backend for a request.
type Policy interface {
	Name() string
	// Pick returns one of the healthy backends, or nil if the list is empty.
	Pick(healthy []*Backend) *Backend
}

// NewPolicy creates a policy by name: round_robin, least_requests or least_tokens.
func NewPolicy(name string) (Policy, error) {
	switch name {
	case "round_robin":
		return &RoundRobin{}, nil
	case "least_requests":
		return &LeastRequests{}, nil
	case "least_tokens":
		return &LeastTokens{}, nil
	}
	return nil, fmt.Errorf("unknown routing policy %q", name)
}

// RoundRobin sends requests to backends in turn, ignoring load.
type RoundRobin struct {
	next atomic.Uint64
}

func (p *RoundRobin) Name() string { return "round_robin" }

func (p *RoundRobin) Pick(healthy []*Backend) *Backend {
	if len(healthy) == 0 {
		return nil
	}
	i := p.next.Add(1) - 1
	return healthy[i%uint64(len(healthy))]
}

// LeastRequests picks the backend with the fewest requests in flight.
type LeastRequests struct {
	offset atomic.Uint64
}

func (p *LeastRequests) Name() string { return "least_requests" }

func (p *LeastRequests) Pick(healthy []*Backend) *Backend {
	return pickMin(healthy, &p.offset, func(b *Backend) int64 { return b.OpenRequests.Load() })
}

// LeastTokens picks the backend with the fewest reserved tokens in flight.
type LeastTokens struct {
	offset atomic.Uint64
}

func (p *LeastTokens) Name() string { return "least_tokens" }

func (p *LeastTokens) Pick(healthy []*Backend) *Backend {
	return pickMin(healthy, &p.offset, func(b *Backend) int64 { return b.OutstandingTokens.Load() })
}

// pickMin returns the backend with the lowest load. The scan starts at a
// rotating position so ties are spread across backends instead of always
// going to the first one.
func pickMin(healthy []*Backend, offset *atomic.Uint64, load func(*Backend) int64) *Backend {
	n := len(healthy)
	if n == 0 {
		return nil
	}
	start := int(offset.Add(1) % uint64(n))
	var best *Backend
	var bestLoad int64
	for i := 0; i < n; i++ {
		b := healthy[(start+i)%n]
		if l := load(b); best == nil || l < bestLoad {
			best, bestLoad = b, l
		}
	}
	return best
}
