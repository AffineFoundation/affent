package sse

import (
	"bufio"
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestEvent_Encode_SingleLineRoundTrip(t *testing.T) {
	ev, err := NewEvent("turn.start", map[string]any{"turn_id": "t_1"})
	if err != nil {
		t.Fatalf("NewEvent: %v", err)
	}
	ev.ID = 42
	wire := ev.Encode()
	gotType, gotID, gotData := parseSSE(t, wire)
	if gotType != "turn.start" {
		t.Errorf("event = %q, want turn.start", gotType)
	}
	if gotID != "42" {
		t.Errorf("id = %q, want 42", gotID)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(gotData), &decoded); err != nil {
		t.Fatalf("data did not parse as JSON: %v (raw=%q)", err, gotData)
	}
	if decoded["turn_id"] != "t_1" {
		t.Errorf("payload.turn_id = %v, want t_1", decoded["turn_id"])
	}
}

// TestEvent_Encode_MultiLineDataSurvivesParse pins the spec-compliant
// behavior: when Data spans multiple physical lines (because the caller
// passed MarshalIndent output or non-JSON multi-line content), each
// chunk gets its own "data: " prefix so the parser concatenates them
// back instead of dropping the tail as unknown fields.
func TestEvent_Encode_MultiLineDataSurvivesParse(t *testing.T) {
	multi := []byte("first line\nsecond line\nthird line")
	ev := Event{Type: "noisy", ID: 7, Data: multi}
	wire := ev.Encode()

	// Count the "data: " prefixes — must equal the number of source lines.
	got := bytes.Count(wire, []byte("data: "))
	want := 3
	if got != want {
		t.Fatalf("expected %d data: prefixes, got %d\nwire=%q", want, got, wire)
	}

	// Parser reconstructs the original payload by joining lines with \n.
	_, _, gotData := parseSSE(t, wire)
	if gotData != string(multi) {
		t.Fatalf("parsed data = %q, want %q", gotData, string(multi))
	}
}

func TestEvent_Encode_CRLFLineEndings(t *testing.T) {
	ev := Event{Type: "crlf", ID: 1, Data: []byte("alpha\r\nbeta")}
	wire := ev.Encode()
	_, _, gotData := parseSSE(t, wire)
	// CR should be stripped from before the \n so the parser sees a clean
	// "alpha" followed by "beta" joined by \n.
	if gotData != "alpha\nbeta" {
		t.Fatalf("CRLF data parsed as %q, want %q", gotData, "alpha\nbeta")
	}
}

func TestEvent_Encode_EmptyData(t *testing.T) {
	ev := Event{Type: "ping", ID: 5}
	wire := ev.Encode()
	gotType, gotID, gotData := parseSSE(t, wire)
	if gotType != "ping" || gotID != "5" || gotData != "" {
		t.Fatalf("(%q, %q, %q) = parsed; want (ping, 5, \"\")", gotType, gotID, gotData)
	}
}

func TestContextCompactPayloadKeepsFalseSummaryPresent(t *testing.T) {
	ev, err := NewEvent(TypeContextCompact, ContextCompactPayload{
		TurnID:          "turn-1",
		BeforeMessages:  40,
		AfterMessages:   12,
		RemovedMessages: 28,
		Reactive:        true,
		Reason:          "context_overflow",
		SummaryPresent:  false,
	})
	if err != nil {
		t.Fatalf("NewEvent: %v", err)
	}
	if !bytes.Contains(ev.Data, []byte(`"summary_present":false`)) {
		t.Fatalf("context.compacted event must preserve explicit false summary_present, data=%s", ev.Data)
	}
}

// parseSSE is a minimal SSE field-parser used to round-trip the
// encoder under test. Reads exactly one event from buf.
func parseSSE(t *testing.T, buf []byte) (eventType, id, data string) {
	t.Helper()
	sc := bufio.NewScanner(bytes.NewReader(buf))
	var dataLines []string
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			break // event terminator
		}
		switch {
		case strings.HasPrefix(line, "event: "):
			eventType = line[len("event: "):]
		case strings.HasPrefix(line, "id: "):
			id = line[len("id: "):]
		case strings.HasPrefix(line, "data: "):
			dataLines = append(dataLines, line[len("data: "):])
		case strings.HasPrefix(line, "data:"): // "data:" without trailing space
			dataLines = append(dataLines, line[len("data:"):])
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan: %v", err)
	}
	return eventType, id, strings.Join(dataLines, "\n")
}
