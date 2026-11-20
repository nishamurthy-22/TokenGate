package mock

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nishamurthy-22/TokenGate/internal/api"
)

func testConfig() Config {
	return Config{
		ID:             "test",
		TokensPerSec:   10000,
		OutputMinFrac:  1,
		OutputMaxFrac:  1,
		Seed:           1,
		MaxConcurrency: 4,
		MaxQueue:       8,
	}
}

func post(t *testing.T, url, body string) *http.Response {
	t.Helper()
	resp, err := http.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestStreamsRequestedTokens(t *testing.T) {
	srv := httptest.NewServer(NewServer(testConfig()).Handler())
	defer srv.Close()

	resp := post(t, srv.URL+"/v1/chat/completions", `{"messages":[{"role":"user","content":"hello"}],"max_tokens":5}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}

	reader := api.NewEventReader(resp.Body)
	tokens := 0
	for {
		data, err := reader.Next()
		if err != nil {
			t.Fatalf("stream ended without final event: %v", err)
		}
		var ev api.StreamEvent
		if err := json.Unmarshal(data, &ev); err != nil {
			t.Fatal(err)
		}
		if ev.Done {
			if ev.Usage == nil || ev.Usage.CompletionTokens != 5 || tokens != 5 {
				t.Fatalf("got %d tokens, final event %s", tokens, data)
			}
			return
		}
		tokens++
	}
}

func TestNonStreamResponse(t *testing.T) {
	srv := httptest.NewServer(NewServer(testConfig()).Handler())
	defer srv.Close()

	resp := post(t, srv.URL+"/v1/chat/completions", `{"messages":[{"role":"user","content":"hello"}],"max_tokens":3,"stream":false}`)
	defer resp.Body.Close()
	var out api.ChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Usage.CompletionTokens != 3 || out.Content != "tok0 tok1 tok2" {
		t.Fatalf("unexpected response %+v", out)
	}
}

func TestFailAndRecover(t *testing.T) {
	srv := httptest.NewServer(NewServer(testConfig()).Handler())
	defer srv.Close()

	post(t, srv.URL+"/admin/fail?mode=error", "").Body.Close()

	resp := post(t, srv.URL+"/v1/chat/completions", `{"messages":[{"role":"user","content":"hi"}],"max_tokens":2}`)
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 in fail mode, got %d", resp.StatusCode)
	}
	health, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	health.Body.Close()
	if health.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected unhealthy, got %d", health.StatusCode)
	}

	post(t, srv.URL+"/admin/recover", "").Body.Close()
	health, err = http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	health.Body.Close()
	if health.StatusCode != http.StatusOK {
		t.Fatalf("expected healthy after recover, got %d", health.StatusCode)
	}
}

func TestBadAdminInput(t *testing.T) {
	srv := httptest.NewServer(NewServer(testConfig()).Handler())
	defer srv.Close()
	for _, path := range []string{"/admin/slow?factor=0", "/admin/fail?mode=explode"} {
		resp := post(t, srv.URL+path, "")
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: expected 400, got %d", path, resp.StatusCode)
		}
	}
}
