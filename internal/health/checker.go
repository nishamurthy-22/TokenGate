// Package health runs active health checks against backends.
// See docs/DESIGN.md, section 7.
package health

import (
	"context"
	"log"
	"net/http"
	"time"

	"github.com/nishamurthy-22/TokenGate/internal/metrics"
	"github.com/nishamurthy-22/TokenGate/internal/routing"
)

// Checker probes GET /healthz on every backend at a fixed interval.
// A backend becomes unhealthy after Threshold failed probes in a row and
// healthy again after Threshold successful probes in a row.
type Checker struct {
	Pool      *routing.Pool
	GatewayID string
	Interval  time.Duration
	Timeout   time.Duration
	Threshold int

	client *http.Client
}

// NewChecker creates a checker with the defaults from DESIGN.md.
func NewChecker(pool *routing.Pool, gatewayID string) *Checker {
	return &Checker{
		Pool:      pool,
		GatewayID: gatewayID,
		Interval:  2 * time.Second,
		Timeout:   500 * time.Millisecond,
		Threshold: 2,
		client:    &http.Client{},
	}
}

// Run probes until ctx is cancelled.
func (c *Checker) Run(ctx context.Context) {
	fails := map[string]int{}
	oks := map[string]int{}
	ticker := time.NewTicker(c.Interval)
	defer ticker.Stop()
	for {
		c.checkAll(ctx, fails, oks)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (c *Checker) checkAll(ctx context.Context, fails, oks map[string]int) {
	for _, b := range c.Pool.All() {
		if c.probe(ctx, b) {
			oks[b.ID]++
			fails[b.ID] = 0
			if !b.Healthy() && oks[b.ID] >= c.Threshold {
				b.SetHealthy(true)
				log.Printf("backend %s is healthy again", b.ID)
			}
		} else {
			fails[b.ID]++
			oks[b.ID] = 0
			if b.Healthy() && fails[b.ID] >= c.Threshold {
				b.SetHealthy(false)
				log.Printf("backend %s marked unhealthy", b.ID)
			}
		}
		v := 0.0
		if b.Healthy() {
			v = 1
		}
		metrics.BackendHealthy.WithLabelValues(c.GatewayID, b.ID).Set(v)
	}
}

func (c *Checker) probe(ctx context.Context, b *routing.Backend) bool {
	ctx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, b.URL+"/healthz", nil)
	if err != nil {
		return false
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}
