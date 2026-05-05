package sse

import (
	"encoding/json"
	"fmt"
	"strconv"
)

// Event is the canonical SSE payload, persisted in the per-session ring so
// reconnecting clients can replay via Last-Event-ID.
type Event struct {
	ID   int64           `json:"id"`
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

// Encode renders an event in SSE wire format.
func (e Event) Encode() []byte {
	out := make([]byte, 0, 64+len(e.Data))
	out = append(out, "event: "...)
	out = append(out, e.Type...)
	out = append(out, '\n')
	out = append(out, "id: "...)
	out = strconv.AppendInt(out, e.ID, 10)
	out = append(out, '\n')
	out = append(out, "data: "...)
	out = append(out, e.Data...)
	out = append(out, '\n', '\n')
	return out
}

// NewEvent builds an event from any json-serializable payload. Caller assigns
// the ID later (the ring buffer owns ID allocation).
func NewEvent(eventType string, payload any) (Event, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return Event{}, fmt.Errorf("encode %s: %w", eventType, err)
	}
	return Event{Type: eventType, Data: raw}, nil
}
