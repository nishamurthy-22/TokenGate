package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nishamurthy-22/TokenGate/internal/api"
	"github.com/nishamurthy-22/TokenGate/internal/config"
	"github.com/nishamurthy-22/TokenGate/internal/mock"
	"github.com/nishamurthy-22/TokenGate/internal/ratelimit"
	"github.com/nishamurthy-22/TokenGate/internal/routing"
)

const body5 = `{"messages":[{"role":"user","content":"hi"}],"max_tokens":5}`

var bigQuota = map[string]config.Quota{"app": {Capacity: 100000}}

func startMock(t *testing.T, id string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(mock.NewServer(mock.Config{
		ID: id, TokensPerSec: 10000, OutputMinFrac: 1, OutputMaxFrac: 1,
		Seed: 1, MaxConcurrency: 8, MaxQueue: 16,
	}).Handler())
	t.Cleanup(srv.Close)
	return srv
}

func breakMock(t *testing.T, srv *httptest.Server) {
	t.Helper()
	resp, err := http.Post(srv.URL+"/admin/fail?mode=error", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
}

func startGateway(t *testing.T, backends []*httptest.Server, lim ratelimit.Limiter,
	quotas map[string]config.Quota, change func(*config.Gateway)) *httptest.Server {
	t.Helper()
	var cfgs []config.BackendConfig
	for i, b := range backends {
		cfgs = append(cfgs, config.BackendConfig{ID: fmt.Sprintf("b%d", i), URL: b.URL})
	}
	cfg := config.Gateway{ID: "gw-test", FailMode: "fail_closed", GatewayCount: 1, MaxRetries: 1}
	if change != nil {
		change(&cfg)
	}
	policy, err := routing.NewPolicy("round_robin")
	if err != nil {
		t.Fatal(err)
	}
	g, err := New(cfg, quotas, routing.NewPool(cfgs), policy, lim)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(g.Handler())
	t.Cleanup(srv.Close)
	return srv
}

func chat(t *testing.T, url, app, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url+"/v1/chat/completions", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if app != "" {
		req.Header.Set(api.HeaderAppID, app)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// readStream returns the number of token events and the final event.
func readStream(t *testing.T, resp *http.Response) (int, api.StreamEvent) {
	t.Helper()
	defer resp.Body.Close()
	r := api.NewEventReader(resp.Body)
	tokens := 0
	for {
		data, err := r.Next()
		if err != nil {
			t.Fatalf("stream ended without final event after %d tokens: %v", tokens, err)
		}
		var ev api.StreamEvent
		if err := json.Unmarshal(data, &ev); err != nil {
			t.Fatal(err)
		}
		if ev.Done {
			return tokens, ev
		}
		tokens++
	}
}

func errorCode(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	var body api.ErrorBody
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	return body.Error
}

func TestStreamsThroughGateway(t *testing.T) {
	gw := startGateway(t, []*httptest.Server{startMock(t, "m")}, ratelimit.Noop{}, bigQuota, nil)
	resp := chat(t, gw.URL, "app", body5)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if got := resp.Header.Get(api.HeaderBackendID); got != "b0" {
		t.Errorf("backend header %q", got)
	}
	tokens, final := readStream(t, resp)
	if tokens != 5 || final.Usage == nil || final.Usage.CompletionTokens != 5 || final.Error != "" {
		t.Fatalf("got %d tokens, final %+v", tokens, final)
	}
}

func TestNonStreamThroughGateway(t *testing.T) {
	gw := startGateway(t, []*httptest.Server{startMock(t, "m")}, ratelimit.Noop{}, bigQuota, nil)
	resp := chat(t, gw.URL, "app", `{"messages":[{"role":"user","content":"hi"}],"max_tokens":3,"stream":false}`)
	defer resp.Body.Close()
	var out api.ChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Content != "tok0 tok1 tok2" || out.Usage.CompletionTokens != 3 {
		t.Fatalf("unexpected response %+v", out)
	}
}

func TestUnknownApp(t *testing.T) {
	gw := startGateway(t, []*httptest.Server{startMock(t, "m")}, ratelimit.Noop{}, bigQuota, nil)
	resp := chat(t, gw.URL, "", body5)
	if resp.StatusCode != http.StatusUnauthorized || errorCode(t, resp) != api.ErrUnknownApp {
		t.Fatalf("expected 401 unknown_app, got %d", resp.StatusCode)
	}
}

func TestOverBudget(t *testing.T) {
	quotas := map[string]config.Quota{"app": {Capacity: 100}}
	gw := startGateway(t, []*httptest.Server{startMock(t, "m")}, ratelimit.NewLocal(quotas, 1), quotas, nil)
	body := `{"messages":[{"role":"user","content":"hi"}],"max_tokens":90}`

	first := chat(t, gw.URL, "app", body) // costs 91 of 100
	if first.StatusCode != http.StatusOK {
		t.Fatalf("first request: status %d", first.StatusCode)
	}
	readStream(t, first)

	second := chat(t, gw.URL, "app", body)
	if second.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("second request: expected 429, got %d", second.StatusCode)
	}
	if second.Header.Get("Retry-After") == "" {
		t.Error("missing Retry-After header")
	}
	if code := errorCode(t, second); code != api.ErrOverBudget {
		t.Fatalf("expected over_budget, got %s", code)
	}
}

func TestExceedsCapacity(t *testing.T) {
	quotas := map[string]config.Quota{"app": {Capacity: 100}}
	gw := startGateway(t, []*httptest.Server{startMock(t, "m")}, ratelimit.NewLocal(quotas, 1), quotas, nil)
	resp := chat(t, gw.URL, "app", `{"messages":[{"role":"user","content":"hi"}],"max_tokens":200}`)
	if resp.StatusCode != http.StatusTooManyRequests || errorCode(t, resp) != api.ErrExceedsCapacity {
		t.Fatalf("expected 429 exceeds_capacity, got %d", resp.StatusCode)
	}
}

func TestRetriesOnFailedBackend(t *testing.T) {
	bad, good := startMock(t, "bad"), startMock(t, "good")
	breakMock(t, bad)
	gw := startGateway(t, []*httptest.Server{bad, good}, ratelimit.Noop{}, bigQuota, nil)
	for i := 0; i < 4; i++ {
		resp := chat(t, gw.URL, "app", body5)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("request %d: status %d", i, resp.StatusCode)
		}
		if got := resp.Header.Get(api.HeaderBackendID); got != "b1" {
			t.Fatalf("request %d served by %s, want b1", i, got)
		}
		readStream(t, resp)
	}
}

func TestNoRetryGivesBackendError(t *testing.T) {
	bad, good := startMock(t, "bad"), startMock(t, "good")
	breakMock(t, bad)
	gw := startGateway(t, []*httptest.Server{bad, good}, ratelimit.Noop{}, bigQuota,
		func(c *config.Gateway) { c.MaxRetries = 0 })

	first := chat(t, gw.URL, "app", body5) // round robin starts with b0 (broken)
	if first.StatusCode != http.StatusBadGateway || errorCode(t, first) != api.ErrBackendError {
		t.Fatalf("expected 502 backend_error, got %d", first.StatusCode)
	}
	second := chat(t, gw.URL, "app", body5)
	if second.StatusCode != http.StatusOK {
		t.Fatalf("second request: status %d", second.StatusCode)
	}
	readStream(t, second)
}

type downLimiter struct{}

func (downLimiter) Reserve(context.Context, string, string, int) (bool, error) {
	return false, errors.New("redis down")
}

func (downLimiter) Settle(context.Context, string, string, int, int) error {
	return errors.New("redis down")
}

func TestFailModes(t *testing.T) {
	backend := startMock(t, "m")

	closed := startGateway(t, []*httptest.Server{backend}, downLimiter{}, bigQuota, nil)
	resp := chat(t, closed.URL, "app", body5)
	if resp.StatusCode != http.StatusServiceUnavailable || errorCode(t, resp) != api.ErrLimiterUnavailable {
		t.Fatalf("fail_closed: expected 503 limiter_unavailable, got %d", resp.StatusCode)
	}

	for _, mode := range []string{"fail_open", "fail_local"} {
		mode := mode
		gw := startGateway(t, []*httptest.Server{backend}, downLimiter{}, bigQuota,
			func(c *config.Gateway) { c.FailMode = mode })
		resp := chat(t, gw.URL, "app", body5)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: expected 200, got %d", mode, resp.StatusCode)
		}
		readStream(t, resp)
	}
}
