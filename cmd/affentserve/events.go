package main

import (
	"net/http"

	"github.com/affinefoundation/affent/sse"
)

// handleSessionEvents tails the session's affent event stream and
// passes events through to the SSE client verbatim. The client gets
// affent's native 13-event schema, not an OpenAI-translated view.
// This is the right endpoint for trace consumers (evidence
// extractors, debuggers) that want richer signals than OpenAI's delta
// protocol carries.
//
// The handler subscribes after the session is fetched, so it captures
// every event from now until the connection drops or the session is
// closed. There is no per-session ring buffer in v1 — late
// subscribers miss historical events. (Add Last-Event-ID replay when
// a real client needs it.)
func handleSessionEvents(pool *SessionPool, sessionID string, w http.ResponseWriter, r *http.Request) {
	sess, err := pool.Get(sessionID)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, "session not found", err)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "streaming unsupported", nil)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	subID, ch := sess.Subscribe(256)
	defer sess.Unsubscribe(subID)

	// Initial flush so curl / fetch unblocks waiting for headers.
	flusher.Flush()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			writeSSE(w, flusher, ev)
		}
	}
}

func writeSSE(w http.ResponseWriter, flusher http.Flusher, ev sse.Event) {
	out := ev.Encode()
	_, _ = w.Write(out)
	flusher.Flush()
}

// handleSessionDelete closes a session immediately. Returns 204 even
// when the id is unknown (idempotent — clients calling
// "make sure this session is gone" don't need a 404).
func handleSessionDelete(pool *SessionPool, sessionID string, w http.ResponseWriter, _ *http.Request) {
	pool.Delete(sessionID)
	w.WriteHeader(http.StatusNoContent)
}
