// Package api defines the request, response and streaming (SSE) formats shared
// by the gateway and the mock backends. See docs/DESIGN.md, sections 2 and 3.
package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

// Header names used between clients, gateways and backends.
const (
	HeaderAppID     = "X-App-ID"
	HeaderRequestID = "X-Request-ID"
	HeaderGatewayID = "X-Gateway-ID"
	HeaderBackendID = "X-Backend-ID"
)

// Limits for max_tokens.
const (
	DefaultMaxTokens = 256
	MaxMaxTokens     = 4096
)

// Error codes returned in ErrorBody.Error.
const (
	ErrBadRequest         = "bad_request"
	ErrUnknownApp         = "unknown_app"
	ErrOverBudget         = "over_budget"
	ErrExceedsCapacity    = "exceeds_capacity"
	ErrBackendError       = "backend_error"
	ErrNoBackend          = "no_backend"
	ErrLimiterUnavailable = "limiter_unavailable"
)

// Message is one chat message.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ChatRequest is the body of POST /v1/chat/completions.
type ChatRequest struct {
	Messages  []Message `json:"messages"`
	MaxTokens int       `json:"max_tokens"`
	Stream    *bool     `json:"stream,omitempty"`
}

// IsStream reports whether the client wants a streamed (SSE) response.
// Streaming is the default when the field is missing.
func (r *ChatRequest) IsStream() bool {
	return r.Stream == nil || *r.Stream
}

// Normalize applies defaults and validates the request.
func (r *ChatRequest) Normalize() error {
	if len(r.Messages) == 0 {
		return errors.New("messages must not be empty")
	}
	if r.MaxTokens == 0 {
		r.MaxTokens = DefaultMaxTokens
	}
	if r.MaxTokens < 1 || r.MaxTokens > MaxMaxTokens {
		return fmt.Errorf("max_tokens must be between 1 and %d", MaxMaxTokens)
	}
	return nil
}

// EstimatePromptTokens estimates prompt tokens as the total characters of all
// message contents divided by 4, rounded up.
func (r *ChatRequest) EstimatePromptTokens() int {
	chars := 0
	for _, m := range r.Messages {
		chars += len(m.Content)
	}
	return (chars + 3) / 4
}

// Usage reports how many tokens a request actually used.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

// Total returns prompt plus completion tokens.
func (u Usage) Total() int {
	return u.PromptTokens + u.CompletionTokens
}

// TokenEvent is one streamed token.
type TokenEvent struct {
	Token string `json:"token"`
	Index int    `json:"index"`
}

// FinalEvent is the last event of every stream. Error is set when the stream
// ended because of a backend failure.
type FinalEvent struct {
	Done  bool   `json:"done"`
	Error string `json:"error,omitempty"`
	Usage Usage  `json:"usage"`
}

// StreamEvent can decode either a TokenEvent or a FinalEvent.
type StreamEvent struct {
	Token string `json:"token,omitempty"`
	Index int    `json:"index,omitempty"`
	Done  bool   `json:"done,omitempty"`
	Error string `json:"error,omitempty"`
	Usage *Usage `json:"usage,omitempty"`
}

// ChatResponse is the body of a non-streamed response.
type ChatResponse struct {
	ID      string `json:"id"`
	Content string `json:"content"`
	Usage   Usage  `json:"usage"`
}

// ErrorBody is the JSON body of every error response.
type ErrorBody struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

// WriteJSON writes v as a JSON response with the given status.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// WriteError writes an ErrorBody response.
func WriteError(w http.ResponseWriter, status int, code, message string) {
	WriteJSON(w, status, ErrorBody{Error: code, Message: message})
}
