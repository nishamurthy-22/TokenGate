// Package routing contains the backend pool and the routing policies.
// See docs/DESIGN.md, section 5.
package routing

import (
	"sync/atomic"

	"github.com/nishamurthy-22/TokenGate/internal/config"
)

// Backend is one mock LLM server as seen by one gateway. The load counters
// only include requests from this gateway: gateways do not share load data.
type Backend struct {
	ID                string
	URL               string
	OpenRequests      atomic.Int64
	OutstandingTokens atomic.Int64
	healthy           atomic.Bool
}

// Healthy reports whether the backend may receive traffic.
func (b *Backend) Healthy() bool { return b.healthy.Load() }

// SetHealthy marks the backend healthy or unhealthy.
func (b *Backend) SetHealthy(v bool) { b.healthy.Store(v) }

// Pool is the fixed set of backends a gateway can route to.
type Pool struct {
	backends []*Backend
}

// NewPool creates a pool. All backends start healthy.
func NewPool(cfgs []config.BackendConfig) *Pool {
	p := &Pool{}
	for _, c := range cfgs {
		b := &Backend{ID: c.ID, URL: c.URL}
		b.SetHealthy(true)
		p.backends = append(p.backends, b)
	}
	return p
}

// All returns every backend.
func (p *Pool) All() []*Backend {
	return p.backends
}

// Healthy returns healthy backends whose IDs are not in exclude.
func (p *Pool) Healthy(exclude map[string]bool) []*Backend {
	out := make([]*Backend, 0, len(p.backends))
	for _, b := range p.backends {
		if b.Healthy() && !exclude[b.ID] {
			out = append(out, b)
		}
	}
	return out
}
