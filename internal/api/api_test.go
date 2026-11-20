package api

import (
	"bytes"
	"encoding/json"
	"io"
	"testing"
)

func TestNormalizeAppliesDefaults(t *testing.T) {
	r := ChatRequest{Messages: []Message{{Role: "user", Content: "hi"}}}
	if err := r.Normalize(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.MaxTokens != DefaultMaxTokens {
		t.Errorf("MaxTokens = %d, want %d", r.MaxTokens, DefaultMaxTokens)
	}
	if !r.IsStream() {
		t.Error("streaming should be the default")
	}
}

func TestNormalizeRejectsInvalid(t *testing.T) {
	cases := []ChatRequest{
		{},
		{Messages: []Message{{Content: "x"}}, MaxTokens: -1},
		{Messages: []Message{{Content: "x"}}, MaxTokens: MaxMaxTokens + 1},
	}
	for i, c := range cases {
		if err := c.Normalize(); err == nil {
			t.Errorf("case %d: expected an error", i)
		}
	}
}

func TestEstimatePromptTokens(t *testing.T) {
	r := ChatRequest{Messages: []Message{{Content: "abcdefgh"}, {Content: "abc"}}}
	if got := r.EstimatePromptTokens(); got != 3 {
		t.Errorf("got %d, want 3", got)
	}
}

func TestEventRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteEvent(&buf, TokenEvent{Token: "tok0", Index: 0}); err != nil {
		t.Fatal(err)
	}
	if err := WriteEvent(&buf, FinalEvent{Done: true, Usage: Usage{PromptTokens: 1, CompletionTokens: 2}}); err != nil {
		t.Fatal(err)
	}

	r := NewEventReader(&buf)
	var ev StreamEvent

	data, err := r.Next()
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &ev); err != nil || ev.Token != "tok0" || ev.Done {
		t.Fatalf("bad token event: %s (%v)", data, err)
	}

	ev = StreamEvent{}
	data, err = r.Next()
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &ev); err != nil || !ev.Done || ev.Usage == nil || ev.Usage.CompletionTokens != 2 {
		t.Fatalf("bad final event: %s (%v)", data, err)
	}

	if _, err := r.Next(); err != io.EOF {
		t.Fatalf("expected io.EOF, got %v", err)
	}
}
