package sse

import (
	"encoding/json"
	"fmt"
	"strconv"
)

// Event is the canonical SSE payload. ID is a monotonically increasing
// sequence number scoped to a single Loop — it shows up in the wire
// format's `id:` line so clients can detect gaps, dedupe on reconnect,
// and feed it back in `Last-Event-ID` if a future server-side replay
// path lands. Today no ring buffer stores events, so a reconnect just
// resumes the live stream from the next event; any missed events are
// gone.
type Event struct {
	ID   int64           `json:"id"`
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

// Encode renders an event in SSE wire format.
//
// Each newline-separated chunk of Data gets its own "data: " prefix
// per the SSE spec — without that, the parser treats every line
// after the first as a separate (unknown) field and silently drops
// the rest of the payload. json.Marshal doesn't produce literal
// newlines, so today's callers never trip this; the split here makes
// the encoder safe for any future payload (MarshalIndent output,
// raw text payloads passed via the exported Data field, etc.).
func (e Event) Encode() []byte {
	out := make([]byte, 0, 64+len(e.Data))
	out = append(out, "event: "...)
	out = append(out, e.Type...)
	out = append(out, '\n')
	out = append(out, "id: "...)
	out = strconv.AppendInt(out, e.ID, 10)
	out = append(out, '\n')
	if len(e.Data) == 0 {
		out = append(out, "data: \n\n"...)
		return out
	}
	start := 0
	for i := 0; i < len(e.Data); i++ {
		if e.Data[i] != '\n' {
			continue
		}
		out = append(out, "data: "...)
		// Drop a trailing CR so \r\n line endings parse cleanly too.
		seg := e.Data[start:i]
		if n := len(seg); n > 0 && seg[n-1] == '\r' {
			seg = seg[:n-1]
		}
		out = append(out, seg...)
		out = append(out, '\n')
		start = i + 1
	}
	out = append(out, "data: "...)
	out = append(out, e.Data[start:]...)
	out = append(out, '\n', '\n')
	return out
}


// NewEvent builds an event from any json-serializable payload. ID is
// left at zero — the Loop assigns it from its per-session sequence
// counter just before publishing on Events.
func NewEvent(eventType string, payload any) (Event, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return Event{}, fmt.Errorf("encode %s: %w", eventType, err)
	}
	return Event{Type: eventType, Data: raw}, nil
}
