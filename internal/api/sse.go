package api

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// SetSSEHeaders sets the headers for a server-sent events response.
func SetSSEHeaders(h http.Header) {
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no")
}

// WriteEvent writes v as one SSE event and flushes it.
func WriteEvent(w io.Writer, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return WriteRawEvent(w, data)
}

// WriteRawEvent writes an already encoded JSON payload as one SSE event and flushes it.
func WriteRawEvent(w io.Writer, data []byte) error {
	if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
		return err
	}
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	return nil
}

// EventReader reads SSE events and returns their data payloads.
type EventReader struct {
	r *bufio.Reader
}

// NewEventReader wraps r.
func NewEventReader(r io.Reader) *EventReader {
	return &EventReader{r: bufio.NewReader(r)}
}

// Next returns the data payload of the next event. It returns io.EOF when the
// stream ends cleanly.
func (e *EventReader) Next() ([]byte, error) {
	var data []byte
	for {
		line, err := e.r.ReadBytes('\n')
		if len(line) > 0 {
			line = bytes.TrimRight(line, "\r\n")
			if len(line) == 0 {
				if data != nil {
					return data, nil
				}
			} else if bytes.HasPrefix(line, []byte("data:")) {
				data = append(data, bytes.TrimSpace(line[len("data:"):])...)
			}
		}
		if err != nil {
			if err == io.EOF && data != nil {
				return data, nil
			}
			return nil, err
		}
	}
}
