// Package eventlog persists a stream of sse.Events as JSONL — the
// canonical Affent trace format. One trace.meta record carrying the
// schema version leads the file, followed by one JSON object per event
// (the marshaled sse.Event, including its monotonic id).
//
// The same writer backs affentctl --trace and affentserve per-session
// event logs, so a CLI trace file and a server session history are
// byte-compatible: a single frontend parser (and the eval trace
// reader) handle both. Keeping the format in one place means the
// skip-deltas rule and the meta header cannot drift between the two
// producers.
package eventlog

import (
	"encoding/json"
	"io"

	"github.com/affinefoundation/affent/internal/sse"
)

// Recorder writes sse.Events to an io.Writer as JSONL.
//
// It is a single-writer sink: callers must not invoke its methods
// concurrently (json.Encoder is not safe for concurrent Encode). Both
// current producers satisfy this — affentctl drains its loop on one
// goroutine, and affentserve writes from the single per-session fanout
// goroutine.
type Recorder struct {
	enc        *json.Encoder
	skipDeltas bool
}

// Options configures a Recorder.
type Options struct {
	// SkipDeltas drops message.delta and thinking.delta events. The
	// accumulated text still lands via message.done / thinking.done, so
	// the turn's content survives while batch/eval traces stay small and
	// free of token-level noise.
	SkipDeltas bool
}

// NewRecorder returns a Recorder writing to w. HTML escaping is
// disabled so tool output and model text round-trip verbatim, matching
// the trace format affentctl has always emitted.
func NewRecorder(w io.Writer, opts Options) *Recorder {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return &Recorder{enc: enc, skipDeltas: opts.SkipDeltas}
}

// WriteMeta writes the leading trace.meta record. Call it once when
// starting a fresh trace; skip it when appending to an existing trace
// (a resumed session) so the file keeps a single meta header.
func (r *Recorder) WriteMeta() error {
	ev, err := sse.NewEvent(sse.TypeTraceMeta, sse.TraceMetaPayload{SchemaVersion: sse.TraceSchemaVersion})
	if err != nil {
		return err
	}
	return r.enc.Encode(ev)
}

// Write records one event. Delta events are dropped when SkipDeltas is
// set; the call is then a no-op returning nil. Callers should still
// feed every event to Write (and observe it separately for state) so
// the skip rule lives in exactly one place.
func (r *Recorder) Write(ev sse.Event) error {
	if r.Skips(ev) {
		return nil
	}
	return r.enc.Encode(ev)
}

// Skips reports whether Write would drop ev under the current options
// without writing it. Callers that distinguish "dropped by policy" from
// "write failed" can check this first instead of treating a nil return
// as proof the event was persisted.
func (r *Recorder) Skips(ev sse.Event) bool {
	return r.skipDeltas && (ev.Type == sse.TypeMessageDelta || ev.Type == sse.TypeThinkingDelta)
}
