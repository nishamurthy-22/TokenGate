package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/nishamurthy-22/TokenGate/internal/api"
	"github.com/nishamurthy-22/TokenGate/internal/metrics"
	"github.com/nishamurthy-22/TokenGate/internal/routing"
)

var errNoBackend = errors.New("no healthy backend")

// result describes what happened when forwarding one request.
type result struct {
	backendID string
	started   bool      // the backend sent at least one event; no retry after this
	gotFinal  bool      // the backend sent its final event
	usage     api.Usage // from the final event
	tokens    int       // token events received
	err       error
}

// used returns the tokens to charge for this request.
func (r result) used(promptTokens int) int {
	if r.gotFinal {
		return r.usage.Total()
	}
	if r.tokens > 0 {
		return promptTokens + r.tokens
	}
	return 0
}

// route picks a backend and forwards the request. If the backend fails before
// sending anything, it retries on a different backend up to MaxRetries times.
func (g *Gateway) route(ctx context.Context, w http.ResponseWriter, req *api.ChatRequest, reqID string, cost int) result {
	res := result{err: errNoBackend}
	tried := map[string]bool{}
	for attempt := 0; attempt <= g.cfg.MaxRetries; attempt++ {
		b := g.policy.Pick(g.pool.Healthy(tried))
		if b == nil {
			break
		}
		tried[b.ID] = true
		if attempt > 0 {
			metrics.RetriesTotal.WithLabelValues(g.cfg.ID, b.ID).Inc()
		}
		res = g.forward(ctx, w, b, req, reqID, cost)
		if res.err == nil || res.started || ctx.Err() != nil {
			break
		}
	}
	return res
}

// forward sends the request to one backend and relays the response. The
// backend is always asked to stream, so tokens can be counted; if the client
// asked for a non-streamed reply, the tokens are joined at the end.
// It writes to the client only once the backend has sent its first event.
func (g *Gateway) forward(parent context.Context, w http.ResponseWriter, b *routing.Backend,
	req *api.ChatRequest, reqID string, cost int) result {
	res := result{backendID: b.ID}

	b.OpenRequests.Add(1)
	b.OutstandingTokens.Add(int64(cost))
	g.updateGauges(b)
	defer func() {
		b.OpenRequests.Add(-1)
		b.OutstandingTokens.Add(-int64(cost))
		g.updateGauges(b)
	}()

	stream := true
	upstream := *req
	upstream.Stream = &stream
	body, err := json.Marshal(upstream)
	if err != nil {
		res.err = err
		return res
	}

	// The timer cancels the backend call if the first token, or the next
	// token, takes too long.
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	timer := time.AfterFunc(g.firstTokenTimeout, cancel)
	defer timer.Stop()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, b.URL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		res.err = err
		return res
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set(api.HeaderRequestID, reqID)

	sent := time.Now()
	resp, err := g.client.Do(httpReq)
	if err != nil {
		res.err = describe(b, parent, ctx, err)
		return res
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		res.err = fmt.Errorf("backend %s returned status %d", b.ID, resp.StatusCode)
		return res
	}

	clientStream := req.IsStream()
	var content []string
	reader := api.NewEventReader(resp.Body)
	for {
		data, err := reader.Next()
		if err != nil {
			res.err = describe(b, parent, ctx, err)
			break
		}
		timer.Reset(g.tokenGapTimeout)

		var ev api.StreamEvent
		if err := json.Unmarshal(data, &ev); err != nil {
			res.err = fmt.Errorf("backend %s sent a bad event: %w", b.ID, err)
			break
		}

		// Failed before any token: nothing reached the client, so a retry is safe.
		if ev.Done && ev.Error != "" && !res.started {
			res.err = fmt.Errorf("backend %s failed: %s", b.ID, ev.Error)
			break
		}

		if !res.started {
			res.started = true
			metrics.TTFT.WithLabelValues(g.cfg.ID, b.ID).Observe(time.Since(sent).Seconds())
			w.Header().Set(api.HeaderBackendID, b.ID)
			if clientStream {
				api.SetSSEHeaders(w.Header())
				w.WriteHeader(http.StatusOK)
			}
		}

		if ev.Done {
			res.gotFinal = true
			if ev.Usage != nil {
				res.usage = *ev.Usage
			}
			if ev.Error != "" {
				res.err = fmt.Errorf("backend %s failed mid-stream: %s", b.ID, ev.Error)
			}
			if clientStream {
				_ = api.WriteRawEvent(w, data)
			}
			break
		}

		res.tokens++
		if clientStream {
			if err := api.WriteRawEvent(w, data); err != nil {
				res.err = err
				break
			}
		} else {
			content = append(content, ev.Token)
		}
	}

	// The stream broke after tokens were sent: finish it with an error event.
	if res.started && res.err != nil && !res.gotFinal && clientStream && parent.Err() == nil {
		usage := api.Usage{PromptTokens: req.EstimatePromptTokens(), CompletionTokens: res.tokens}
		_ = api.WriteEvent(w, api.FinalEvent{Done: true, Error: api.ErrBackendError, Usage: usage})
	}
	if res.started && !clientStream {
		if res.err != nil {
			api.WriteError(w, http.StatusBadGateway, api.ErrBackendError, res.err.Error())
		} else {
			api.WriteJSON(w, http.StatusOK, api.ChatResponse{ID: reqID, Content: strings.Join(content, " "), Usage: res.usage})
		}
	}
	return res
}

// describe turns a transport error into a readable one, separating our own
// timeouts from other failures.
func describe(b *routing.Backend, parent, ctx context.Context, err error) error {
	if parent.Err() == nil && ctx.Err() != nil {
		return fmt.Errorf("backend %s timed out", b.ID)
	}
	return fmt.Errorf("backend %s: %w", b.ID, err)
}

func (g *Gateway) updateGauges(b *routing.Backend) {
	metrics.BackendOpenRequests.WithLabelValues(g.cfg.ID, b.ID).Set(float64(b.OpenRequests.Load()))
	metrics.BackendOutstandingTokens.WithLabelValues(g.cfg.ID, b.ID).Set(float64(b.OutstandingTokens.Load()))
}
