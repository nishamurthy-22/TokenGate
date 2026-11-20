// Package gateway implements the TokenGate HTTP server: it checks the
// application's token budget, picks a backend, streams the response back and
// settles the budget with the real usage. See docs/DESIGN.md.
package gateway

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/nishamurthy-22/TokenGate/internal/api"
	"github.com/nishamurthy-22/TokenGate/internal/config"
	"github.com/nishamurthy-22/TokenGate/internal/metrics"
	"github.com/nishamurthy-22/TokenGate/internal/ratelimit"
	"github.com/nishamurthy-22/TokenGate/internal/routing"
)

// Gateway is one stateless TokenGate instance.
type Gateway struct {
	cfg      config.Gateway
	quotas   map[string]config.Quota
	pool     *routing.Pool
	policy   routing.Policy
	limiter  ratelimit.Limiter
	fallback ratelimit.Limiter // used only with fail_local
	client   *http.Client

	firstTokenTimeout time.Duration
	tokenGapTimeout   time.Duration
}

// New creates a gateway.
func New(cfg config.Gateway, quotas map[string]config.Quota, pool *routing.Pool,
	policy routing.Policy, limiter ratelimit.Limiter) (*Gateway, error) {
	switch cfg.FailMode {
	case "fail_closed", "fail_open", "fail_local":
	default:
		return nil, fmt.Errorf("unknown fail mode %q", cfg.FailMode)
	}
	transport := &http.Transport{
		DialContext:         (&net.Dialer{Timeout: time.Second, KeepAlive: 30 * time.Second}).DialContext,
		MaxIdleConns:        1024,
		MaxIdleConnsPerHost: 256,
		IdleConnTimeout:     90 * time.Second,
	}
	g := &Gateway{
		cfg:               cfg,
		quotas:            quotas,
		pool:              pool,
		policy:            policy,
		limiter:           limiter,
		client:            &http.Client{Transport: transport},
		firstTokenTimeout: 10 * time.Second,
		tokenGapTimeout:   5 * time.Second,
	}
	if cfg.FailMode == "fail_local" {
		g.fallback = ratelimit.NewLocal(quotas, cfg.GatewayCount)
	}
	return g, nil
}

// Handler returns the gateway's HTTP routes.
func (g *Gateway) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/chat/completions", g.handleChat)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	mux.Handle("GET /metrics", metrics.Handler())
	return mux
}

func (g *Gateway) handleChat(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	reqID := r.Header.Get(api.HeaderRequestID)
	if reqID == "" {
		reqID = newRequestID()
	}
	w.Header().Set(api.HeaderRequestID, reqID)
	w.Header().Set(api.HeaderGatewayID, g.cfg.ID)

	appID := r.Header.Get(api.HeaderAppID)
	quota, known := g.quotas[appID]
	appLabel := appID
	if !known {
		appLabel = "unknown" // never use untrusted header values as metric labels
	}

	status := "200"
	backendID := "none"
	defer func() {
		metrics.RequestsTotal.WithLabelValues(g.cfg.ID, appLabel, status).Inc()
		metrics.RequestDuration.WithLabelValues(g.cfg.ID, appLabel, backendID).Observe(time.Since(start).Seconds())
	}()

	if !known {
		status = g.reject(w, http.StatusUnauthorized, api.ErrUnknownApp, "missing or unknown "+api.HeaderAppID+" header", appLabel)
		return
	}

	var req api.ChatRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		status = g.reject(w, http.StatusBadRequest, api.ErrBadRequest, "invalid JSON body", appLabel)
		return
	}
	if err := req.Normalize(); err != nil {
		status = g.reject(w, http.StatusBadRequest, api.ErrBadRequest, err.Error(), appLabel)
		return
	}

	// Reserve the worst case: estimated prompt plus the maximum reply length.
	promptTokens := req.EstimatePromptTokens()
	cost := promptTokens + req.MaxTokens
	if float64(cost) > quota.Capacity {
		status = g.reject(w, http.StatusTooManyRequests, api.ErrExceedsCapacity,
			fmt.Sprintf("request needs %d tokens but the app's capacity is %.0f", cost, quota.Capacity), appLabel)
		return
	}

	holder, admitted, unavailable := g.reserve(r.Context(), appID, reqID, cost)
	if !admitted {
		if unavailable && holder == nil {
			status = g.reject(w, http.StatusServiceUnavailable, api.ErrLimiterUnavailable, "token budget store is unavailable", appLabel)
		} else {
			w.Header().Set("Retry-After", retryAfter(cost, quota))
			status = g.reject(w, http.StatusTooManyRequests, api.ErrOverBudget, "not enough tokens in the app's budget", appLabel)
		}
		return
	}
	if holder != nil {
		metrics.TokensTotal.WithLabelValues(g.cfg.ID, appLabel, "reserved").Add(float64(cost))
	}

	res := g.route(r.Context(), w, &req, reqID, cost)
	if res.backendID != "" {
		backendID = res.backendID
	}

	switch {
	case r.Context().Err() != nil:
		status = "499" // client went away
	case !res.started && errors.Is(res.err, errNoBackend):
		status = g.reject(w, http.StatusServiceUnavailable, api.ErrNoBackend, "no healthy backend available", appLabel)
	case !res.started:
		msg := "backend failed"
		if res.err != nil {
			msg += ": " + res.err.Error()
		}
		status = g.reject(w, http.StatusBadGateway, api.ErrBackendError, msg, appLabel)
	case res.err != nil && !req.IsStream():
		status = "502"
	case res.err != nil:
		status = "partial" // stream started, then the backend failed
	}

	if holder != nil {
		g.settle(holder, appID, reqID, cost, res.used(promptTokens), appLabel)
	}
}

// reserve asks the limiter for tokens and applies the fail mode if the budget
// store cannot be reached. holder is the limiter to settle with later (nil
// when nothing was reserved).
func (g *Gateway) reserve(ctx context.Context, appID, reqID string, cost int) (holder ratelimit.Limiter, admitted, unavailable bool) {
	ok, err := g.limiter.Reserve(ctx, appID, reqID, cost)
	if err == nil {
		return g.limiter, ok, false
	}
	switch g.cfg.FailMode {
	case "fail_open":
		return nil, true, true
	case "fail_local":
		ok, err := g.fallback.Reserve(ctx, appID, reqID, cost)
		if err != nil {
			return nil, false, true
		}
		return g.fallback, ok, true
	default:
		return nil, false, true
	}
}

func (g *Gateway) settle(l ratelimit.Limiter, appID, reqID string, reserved, used int, appLabel string) {
	// The client's context may already be cancelled, so use a fresh one.
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	if err := l.Settle(ctx, appID, reqID, reserved, used); err != nil {
		log.Printf("settle %s for %s failed: %v", reqID, appID, err)
	}
	metrics.TokensTotal.WithLabelValues(g.cfg.ID, appLabel, "used").Add(float64(used))
	if d := reserved - used; d > 0 {
		metrics.TokensTotal.WithLabelValues(g.cfg.ID, appLabel, "refunded").Add(float64(d))
	} else if d < 0 {
		metrics.TokensTotal.WithLabelValues(g.cfg.ID, appLabel, "overshoot").Add(float64(-d))
	}
}

func (g *Gateway) reject(w http.ResponseWriter, status int, code, message, appLabel string) string {
	metrics.RejectionsTotal.WithLabelValues(g.cfg.ID, appLabel, code).Inc()
	api.WriteError(w, status, code, message)
	return strconv.Itoa(status)
}

// retryAfter estimates how many seconds until the budget has refilled enough.
func retryAfter(cost int, q config.Quota) string {
	secs := 60.0
	if q.RefillPerSec > 0 {
		secs = math.Ceil(float64(cost) / q.RefillPerSec)
	}
	secs = math.Max(1, math.Min(60, secs))
	return strconv.Itoa(int(secs))
}

func newRequestID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return hex.EncodeToString(b)
}
