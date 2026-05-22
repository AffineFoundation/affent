package main

import (
	"net/http"
	"time"

	"github.com/affinefoundation/affent/internal/sse"
)

// sseKeepAliveInterval is how often an idle SSE stream emits a
// comment line (`: ping\n\n`) to keep intermediaries (nginx default
// proxy_read_timeout 60s, AWS ALB 60s idle, Cloudflare 100s) from
// dropping the connection. SSE clients treat `:`-prefixed lines as
// comments and ignore them; the bytes are only there to flow.
const sseKeepAliveInterval = 25 * time.Second

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

	keepAlive := time.NewTicker(sseKeepAliveInterval)
	defer keepAlive.Stop()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case <-keepAlive.C:
			if _, err := w.Write([]byte(": ping\n\n")); err != nil {
				return
			}
			flusher.Flush()
		case ev, ok := <-ch:
			if !ok {
				return
			}
			if err := writeSSE(w, flusher, ev); err != nil {
				// Client disconnected (broken pipe, RST). Detecting
				// this via write failure is faster than waiting for
				// ctx.Done — Go marks the request ctx Done lazily when
				// the runtime notices the closed conn, which can be
				// many seconds for an SSE stream that doesn't try to
				// read. The keep-alive case above already returns on
				// write error; this brings the event-emit path in line.
				return
			}
		}
	}
}

func writeSSE(w http.ResponseWriter, flusher http.Flusher, ev sse.Event) error {
	out := ev.Encode()
	if _, err := w.Write(out); err != nil {
		return err
	}
	flusher.Flush()
	return nil
}

// handleSessionDelete closes a session immediately. Returns 204 even
// when the id is unknown (idempotent — clients calling
// "make sure this session is gone" don't need a 404).
func handleSessionDelete(pool *SessionPool, sessionID string, w http.ResponseWriter, _ *http.Request) {
	pool.Delete(sessionID)
	w.WriteHeader(http.StatusNoContent)
}
