package mock

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nishamurthy-22/TokenGate/internal/api"
	"github.com/nishamurthy-22/TokenGate/internal/metrics"
)

// Server is a mock LLM backend.
type Server struct {
	cfg    Config
	faults faults
	slots  chan struct{}
	queued atomic.Int64

	rngMu sync.Mutex
	rng   *rand.Rand
}

// NewServer creates a mock backend with the given settings.
func NewServer(cfg Config) *Server {
	s := &Server{
		cfg:   cfg,
		slots: make(chan struct{}, cfg.MaxConcurrency),
		rng:   rand.New(rand.NewSource(cfg.Seed)),
	}
	s.faults.set(1, modeNone)
	return s
}

// Handler returns the HTTP routes of the mock backend.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/chat/completions", s.handleCompletion)
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.Handle("GET /metrics", metrics.Handler())
	mux.HandleFunc("POST /admin/slow", s.handleSlow)
	mux.HandleFunc("POST /admin/fail", s.handleFail)
	mux.HandleFunc("POST /admin/recover", s.handleRecover)
	return mux
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if _, mode := s.faults.get(); mode == modeError {
		http.Error(w, "unhealthy", http.StatusServiceUnavailable)
		return
	}
	_, _ = w.Write([]byte("ok"))
}

func (s *Server) handleCompletion(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := s.cfg.ID
	status := "200"
	defer func() { metrics.MockRequestsTotal.WithLabelValues(id, status).Inc() }()

	var req api.ChatRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		status = "400"
		api.WriteError(w, http.StatusBadRequest, api.ErrBadRequest, "invalid JSON body")
		return
	}
	if err := req.Normalize(); err != nil {
		status = "400"
		api.WriteError(w, http.StatusBadRequest, api.ErrBadRequest, err.Error())
		return
	}

	switch _, mode := s.faults.get(); mode {
	case modeError:
		status = "503"
		api.WriteError(w, http.StatusServiceUnavailable, api.ErrBackendError, "injected failure")
		return
	case modeHang:
		status = "hang"
		<-ctx.Done()
		return
	}

	if !s.acquireSlot(ctx) {
		if ctx.Err() != nil {
			status = "cancelled"
			return
		}
		status = "503"
		api.WriteError(w, http.StatusServiceUnavailable, api.ErrBackendError, "queue full")
		return
	}
	defer func() { <-s.slots }()
	metrics.MockActiveRequests.WithLabelValues(id).Inc()
	defer metrics.MockActiveRequests.WithLabelValues(id).Dec()

	n := s.outputLength(req.MaxTokens)
	usage := api.Usage{PromptTokens: req.EstimatePromptTokens()}

	if !sleep(ctx, s.scaled(s.cfg.TTFT)) {
		status = "cancelled"
		return
	}
	w.Header().Set(api.HeaderBackendID, id)

	if !req.IsStream() {
		tokens := make([]string, 0, n)
		for i := 0; i < n; i++ {
			if i > 0 && !sleep(ctx, s.tokenDelay()) {
				status = "cancelled"
				return
			}
			tokens = append(tokens, tokenText(i))
		}
		metrics.MockTokensGenerated.WithLabelValues(id).Add(float64(n))
		usage.CompletionTokens = n
		api.WriteJSON(w, http.StatusOK, api.ChatResponse{
			ID:      r.Header.Get(api.HeaderRequestID),
			Content: strings.Join(tokens, " "),
			Usage:   usage,
		})
		return
	}

	api.SetSSEHeaders(w.Header())
	w.WriteHeader(http.StatusOK)
	for i := 0; i < n; i++ {
		if _, mode := s.faults.get(); mode == modeError {
			// Failure in the middle of a stream: tell the caller how far we got.
			usage.CompletionTokens = i
			_ = api.WriteEvent(w, api.FinalEvent{Done: true, Error: api.ErrBackendError, Usage: usage})
			status = "stream_error"
			return
		}
		if err := api.WriteEvent(w, api.TokenEvent{Token: tokenText(i), Index: i}); err != nil {
			status = "cancelled"
			return
		}
		metrics.MockTokensGenerated.WithLabelValues(id).Inc()
		if i < n-1 && !sleep(ctx, s.tokenDelay()) {
			status = "cancelled"
			return
		}
	}
	usage.CompletionTokens = n
	_ = api.WriteEvent(w, api.FinalEvent{Done: true, Usage: usage})
}

// acquireSlot waits for a free generation slot. It returns false if the queue
// is full or the client went away.
func (s *Server) acquireSlot(ctx context.Context) bool {
	if s.queued.Load() >= int64(s.cfg.MaxQueue) {
		select {
		case s.slots <- struct{}{}:
			return true
		default:
			return false
		}
	}
	queue := metrics.MockQueueDepth.WithLabelValues(s.cfg.ID)
	queue.Set(float64(s.queued.Add(1)))
	defer func() { queue.Set(float64(s.queued.Add(-1))) }()

	select {
	case s.slots <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}

// outputLength picks how many tokens to generate: a random fraction of maxTokens.
func (s *Server) outputLength(maxTokens int) int {
	s.rngMu.Lock()
	frac := s.cfg.OutputMinFrac + s.rng.Float64()*(s.cfg.OutputMaxFrac-s.cfg.OutputMinFrac)
	s.rngMu.Unlock()
	n := int(math.Round(frac * float64(maxTokens)))
	if n < 1 {
		n = 1
	}
	if n > maxTokens {
		n = maxTokens
	}
	return n
}

func (s *Server) tokenDelay() time.Duration {
	return s.scaled(time.Duration(float64(time.Second) / s.cfg.TokensPerSec))
}

func (s *Server) scaled(d time.Duration) time.Duration {
	slow, _ := s.faults.get()
	return time.Duration(float64(d) * slow)
}

func tokenText(i int) string {
	return "tok" + strconv.Itoa(i)
}

// sleep waits for d or until ctx is cancelled. It returns false if cancelled.
func sleep(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}
