package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	agent "github.com/affinefoundation/affent/internal/agent"
	"github.com/affinefoundation/affent/internal/sse"
)

// sseKeepAliveInterval is how often an idle SSE stream emits a
// comment line (`: ping\n\n`) to keep intermediaries (nginx default
// proxy_read_timeout 60s, AWS ALB 60s idle, Cloudflare 100s) from
// dropping the connection. SSE clients treat `:`-prefixed lines as
// comments and ignore them; the bytes are only there to flow.
const sseKeepAliveInterval = 25 * time.Second

const (
	defaultHistoryLimit = 100
	maxHistoryLimit     = 500
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

type sessionHistoryResponse struct {
	SessionID           string      `json:"session_id"`
	Events              []sse.Event `json:"events"`
	NextAfter           int64       `json:"next_after"`
	HasMore             bool        `json:"has_more"`
	TraceSchemaVersion  int         `json:"trace_schema_version,omitempty"`
	TraceSchemaDetected bool        `json:"trace_schema_detected"`
}

// handleSessionHistory replays persisted session events from
// events.jsonl. The cursor is the zero-based JSONL line number, not
// sse.Event.ID: Loop event ids are per-process and can repeat after a
// server restart, while line numbers remain stable for append-only
// durable replay.
func handleSessionHistory(pool *SessionPool, sessionID string, w http.ResponseWriter, r *http.Request) {
	if pool == nil {
		writeJSONError(w, http.StatusNotFound, "session not found", nil)
		return
	}
	if err := agent.ValidateSessionID(sessionID); err != nil {
		writeJSONErrorTyped(w, http.StatusBadRequest, "invalid session id", err, "bad_request")
		return
	}
	after, limit, err := parseHistoryQuery(r)
	if err != nil {
		writeJSONErrorTyped(w, http.StatusBadRequest, "invalid history query", err, "bad_request")
		return
	}
	resp, err := readSessionHistory(pool.sessionDirPath(sessionID), sessionID, after, limit)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeJSONError(w, http.StatusNotFound, "session history not found", err)
			return
		}
		writeJSONError(w, http.StatusInternalServerError, "read session history", err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func parseHistoryQuery(r *http.Request) (after int64, limit int, err error) {
	after = -1
	limit = defaultHistoryLimit
	q := r.URL.Query()
	if raw := q.Get("after"); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return 0, 0, err
		}
		if n < -1 {
			return 0, 0, errors.New("after must be -1 or greater")
		}
		after = n
	}
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			return 0, 0, err
		}
		if n <= 0 {
			return 0, 0, errors.New("limit must be positive")
		}
		if n > maxHistoryLimit {
			n = maxHistoryLimit
		}
		limit = n
	}
	return after, limit, nil
}

func readSessionHistory(sessionDir, sessionID string, after int64, limit int) (sessionHistoryResponse, error) {
	path := filepath.Join(sessionDir, "events.jsonl")
	info, err := os.Lstat(path)
	if err != nil {
		return sessionHistoryResponse{}, err
	}
	if info.IsDir() {
		return sessionHistoryResponse{}, fmt.Errorf("events path is a directory")
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return sessionHistoryResponse{}, fmt.Errorf("events path must not be a symlink")
	}
	f, err := os.Open(path)
	if err != nil {
		return sessionHistoryResponse{}, err
	}
	defer f.Close()

	resp := sessionHistoryResponse{
		SessionID: sessionID,
		Events:    []sse.Event{},
		NextAfter: after,
	}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	lineNo := int64(-1)
	for sc.Scan() {
		lineNo++
		var ev sse.Event
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
			continue
		}
		if ev.Type == sse.TypeTraceMeta {
			var meta sse.TraceMetaPayload
			if err := json.Unmarshal(ev.Data, &meta); err == nil {
				resp.TraceSchemaVersion = meta.SchemaVersion
				resp.TraceSchemaDetected = true
			}
		}
		if lineNo <= after {
			continue
		}
		if len(resp.Events) >= limit {
			resp.HasMore = true
			break
		}
		resp.Events = append(resp.Events, ev)
		resp.NextAfter = lineNo
	}
	if err := sc.Err(); err != nil {
		return sessionHistoryResponse{}, err
	}
	return resp, nil
}

// handleSessionDelete closes a session immediately. Returns 204 even
// when the id is unknown (idempotent — clients calling
// "make sure this session is gone" don't need a 404).
func handleSessionDelete(pool *SessionPool, sessionID string, w http.ResponseWriter, _ *http.Request) {
	pool.Delete(sessionID)
	w.WriteHeader(http.StatusNoContent)
}
