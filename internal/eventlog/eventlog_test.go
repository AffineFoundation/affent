package eventlog

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/affinefoundation/affent/internal/sse"
)

func mustEvent(t *testing.T, typ string, payload any) sse.Event {
	t.Helper()
	ev, err := sse.NewEvent(typ, payload)
	if err != nil {
		t.Fatalf("NewEvent(%s): %v", typ, err)
	}
	return ev
}

func lines(s string) []string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func TestWriteMetaMatchesTraceFormat(t *testing.T) {
	var buf bytes.Buffer
	r := NewRecorder(&buf, Options{})
	if err := r.WriteMeta(); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}
	// The meta line must be the exact JSONL affentctl has always
	// written: a marshaled sse.Event with id 0 and the schema payload.
	want := `{"id":0,"type":"trace.meta","data":{"schema_version":1}}`
	if got := strings.TrimRight(buf.String(), "\n"); got != want {
		t.Fatalf("meta line\n got: %s\nwant: %s", got, want)
	}
}

func TestWriteEncodesEachEventAsOneLine(t *testing.T) {
	var buf bytes.Buffer
	r := NewRecorder(&buf, Options{})

	evs := []sse.Event{
		mustEvent(t, sse.TypeTurnStart, sse.TurnStartPayload{TurnID: "t1"}),
		mustEvent(t, sse.TypeMessageDone, sse.MessageDonePayload{TurnID: "t1", Text: "hi"}),
		mustEvent(t, sse.TypeTurnEnd, sse.TurnEndPayload{TurnID: "t1", Reason: sse.TurnEndCompleted}),
	}
	for i, ev := range evs {
		if err := r.Write(ev); err != nil {
			t.Fatalf("Write[%d]: %v", i, err)
		}
	}
	got := lines(buf.String())
	if len(got) != len(evs) {
		t.Fatalf("got %d lines, want %d: %q", len(got), len(evs), buf.String())
	}
	if !strings.Contains(got[0], `"type":"turn.start"`) || !strings.Contains(got[2], `"reason":"completed"`) {
		t.Fatalf("unexpected lines: %#v", got)
	}
}

func TestSkipDeltasDropsOnlyDeltaEvents(t *testing.T) {
	var buf bytes.Buffer
	r := NewRecorder(&buf, Options{SkipDeltas: true})

	feed := []sse.Event{
		mustEvent(t, sse.TypeMessageDelta, sse.MessageDeltaPayload{TurnID: "t1", Delta: "h"}),
		mustEvent(t, sse.TypeThinkingDelta, sse.ThinkingDeltaPayload{TurnID: "t1", Delta: "?"}),
		mustEvent(t, sse.TypeMessageDone, sse.MessageDonePayload{TurnID: "t1", Text: "hi"}),
		mustEvent(t, sse.TypeToolResult, sse.ToolResultPayload{CallID: "c1", ResultSummary: "ok"}),
	}
	for _, ev := range feed {
		if err := r.Write(ev); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	got := lines(buf.String())
	// message.done and tool.result survive; the two deltas are dropped.
	if len(got) != 2 {
		t.Fatalf("got %d lines, want 2: %q", len(got), buf.String())
	}
	for _, ln := range got {
		if strings.Contains(ln, `"type":"message.delta"`) || strings.Contains(ln, `"type":"thinking.delta"`) {
			t.Fatalf("delta leaked into trace: %s", ln)
		}
	}
}

func TestSkipsReportsDeltaPolicy(t *testing.T) {
	r := NewRecorder(&bytes.Buffer{}, Options{SkipDeltas: true})
	delta := mustEvent(t, sse.TypeMessageDelta, sse.MessageDeltaPayload{TurnID: "t1", Delta: "x"})
	done := mustEvent(t, sse.TypeMessageDone, sse.MessageDonePayload{TurnID: "t1", Text: "x"})
	if !r.Skips(delta) {
		t.Fatal("expected message.delta to be skipped under SkipDeltas")
	}
	if r.Skips(done) {
		t.Fatal("message.done must never be skipped")
	}

	off := NewRecorder(&bytes.Buffer{}, Options{})
	if off.Skips(delta) {
		t.Fatal("deltas must not be skipped when SkipDeltas is off")
	}
}

func TestEventTextRoundTrips(t *testing.T) {
	var buf bytes.Buffer
	r := NewRecorder(&buf, Options{})
	// Tool output and model text routinely contain <, >, &. sse.NewEvent
	// marshals the payload (unicode-escaping those bytes into Data)
	// before the recorder sees it, so the on-disk line carries <
	// etc. — the same bytes affentctl has always written. What the
	// contract guarantees is that the line decodes back to the original
	// text, which is what every consumer (frontend, eval) relies on.
	want := "a < b && c > d"
	ev := mustEvent(t, sse.TypeMessageDone, sse.MessageDonePayload{TurnID: "t1", Text: want})
	if err := r.Write(ev); err != nil {
		t.Fatalf("Write: %v", err)
	}

	var line sse.Event
	if err := json.Unmarshal([]byte(strings.TrimRight(buf.String(), "\n")), &line); err != nil {
		t.Fatalf("decode event line: %v", err)
	}
	var got sse.MessageDonePayload
	if err := json.Unmarshal(line.Data, &got); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if got.Text != want {
		t.Fatalf("round-trip text = %q, want %q", got.Text, want)
	}
}
