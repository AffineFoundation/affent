package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	agent "github.com/affinefoundation/affent/internal/agent"
	"github.com/affinefoundation/affent/internal/jsonl"
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
	maxHistoryLineBytes = jsonl.DefaultMaxRecordBytes
)

var errEventCursorAhead = errors.New("event cursor is ahead of latest event")

// handleSessionEvents tails the session's affent event stream and
// passes events through to the SSE client verbatim. The client gets
// affent's native 13-event schema, not an OpenAI-translated view.
// This is the right endpoint for trace consumers (evidence
// extractors, debuggers) that want richer signals than OpenAI's delta
// protocol carries.
//
// Event IDs on this endpoint are durable events.jsonl line cursors.
// Clients may reconnect with Last-Event-ID and the handler will first
// replay persisted events after that cursor, then continue with live
// events.
func handleSessionEvents(pool *SessionPool, sessionID string, w http.ResponseWriter, r *http.Request) {
	lastEventID, replay, err := parseLastEventID(r.Header.Get("Last-Event-ID"))
	if err != nil {
		writeJSONErrorTyped(w, http.StatusBadRequest, "invalid Last-Event-ID", err, "bad_request")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "streaming unsupported", nil)
		return
	}
	sess, err := sessionForEvents(pool, sessionID)
	if err != nil {
		if errors.Is(err, ErrSessionNotFound) {
			writeJSONError(w, http.StatusNotFound, "session not found", err)
			return
		}
		writeJSONErrorTyped(w, http.StatusBadRequest, "invalid session id", err, "bad_request")
		return
	}
	if replay {
		if err := validateSessionReplayCursor(pool.sessionDirPath(sessionID), lastEventID); err != nil {
			if errors.Is(err, errEventCursorAhead) {
				writeJSONErrorTyped(w, http.StatusConflict, "Last-Event-ID is ahead of latest session event", err, "cursor_ahead")
				return
			}
			writeJSONError(w, http.StatusInternalServerError, "validate session event cursor", err)
			return
		}
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	subID, ch := sess.Subscribe(256)
	defer sess.Unsubscribe(subID)

	// Initial flush so curl / fetch unblocks waiting for headers.
	flusher.Flush()

	replayedThrough := lastEventID
	if replay {
		replayedThrough, err = replaySessionEvents(w, flusher, pool.sessionDirPath(sessionID), lastEventID)
		if err != nil {
			return
		}
	}

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
			if replay && ev.ID <= replayedThrough {
				continue
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

func sessionForEvents(pool *SessionPool, sessionID string) (*Session, error) {
	if pool == nil {
		return nil, ErrSessionNotFound
	}
	if err := agent.ValidateSessionID(sessionID); err != nil {
		return nil, err
	}
	sess, err := pool.Get(sessionID)
	if err == nil {
		return sess, nil
	}
	if !errors.Is(err, ErrSessionNotFound) {
		return nil, err
	}
	if _, found, err := durableSessionDirInfo(pool.sessionDirPath(sessionID)); err != nil {
		return nil, err
	} else if !found {
		return nil, ErrSessionNotFound
	}
	return pool.GetOrCreate(sessionID)
}

func parseLastEventID(raw string) (int64, bool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return -1, false, nil
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, false, err
	}
	if n < -1 {
		return 0, false, errors.New("Last-Event-ID must be -1 or greater")
	}
	return n, true, nil
}

func validateSessionReplayCursor(sessionDir string, cursor int64) error {
	if cursor < 0 {
		return nil
	}
	last, err := sessionEventLogLastCursor(sessionDir)
	if err != nil {
		return err
	}
	if cursor > last {
		return fmt.Errorf("%w: Last-Event-ID %d is ahead of latest event cursor %d", errEventCursorAhead, cursor, last)
	}
	return nil
}

func sessionEventLogLastCursor(sessionDir string) (int64, error) {
	lines, err := countJSONLLines(filepath.Join(sessionDir, "events.jsonl"))
	if err != nil {
		return 0, err
	}
	return lines - 1, nil
}

func replaySessionEvents(w http.ResponseWriter, flusher http.Flusher, sessionDir string, after int64) (int64, error) {
	cursor := after
	for {
		page, err := readSessionHistoryPage(sessionDir, cursor, maxHistoryLimit)
		if err != nil {
			return cursor, err
		}
		for _, rec := range page.Records {
			if err := writeSSEWithID(w, flusher, rec.Event, rec.Cursor); err != nil {
				return cursor, err
			}
			cursor = rec.Cursor
		}
		if !page.HasMore {
			return cursor, nil
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

func writeSSEWithID(w http.ResponseWriter, flusher http.Flusher, ev sse.Event, id int64) error {
	ev.ID = id
	return writeSSE(w, flusher, ev)
}

type sessionHistoryResponse struct {
	SessionID           string      `json:"session_id"`
	Events              []sse.Event `json:"events"`
	NextAfter           int64       `json:"next_after"`
	HasMore             bool        `json:"has_more"`
	TraceSchemaVersion  int         `json:"trace_schema_version,omitempty"`
	TraceSchemaDetected bool        `json:"trace_schema_detected"`
}

type sessionHistoryRecord struct {
	Cursor int64
	Event  sse.Event
}

type sessionHistoryPage struct {
	Records             []sessionHistoryRecord
	NextAfter           int64
	HasMore             bool
	TraceSchemaVersion  int
	TraceSchemaDetected bool
}

func validateTraceSchemaVersion(version int) error {
	if version <= 0 {
		return nil
	}
	if version > sse.TraceSchemaVersion {
		return fmt.Errorf("unsupported trace schema_version %d (max %d)", version, sse.TraceSchemaVersion)
	}
	return nil
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
		if errors.Is(err, errEventCursorAhead) {
			writeJSONErrorTyped(w, http.StatusConflict, "history cursor is ahead of latest session event", err, "cursor_ahead")
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
	page, err := readSessionHistoryPage(sessionDir, after, limit)
	if err != nil {
		return sessionHistoryResponse{}, err
	}
	resp := sessionHistoryResponse{
		SessionID:           sessionID,
		Events:              make([]sse.Event, 0, len(page.Records)),
		NextAfter:           page.NextAfter,
		HasMore:             page.HasMore,
		TraceSchemaVersion:  page.TraceSchemaVersion,
		TraceSchemaDetected: page.TraceSchemaDetected,
	}
	for _, rec := range page.Records {
		resp.Events = append(resp.Events, rec.Event)
	}
	return resp, nil
}

func readSessionHistoryPage(sessionDir string, after int64, limit int) (sessionHistoryPage, error) {
	path := filepath.Join(sessionDir, "events.jsonl")
	info, err := os.Lstat(path)
	if err != nil {
		return sessionHistoryPage{}, err
	}
	if info.IsDir() {
		return sessionHistoryPage{}, fmt.Errorf("events path is a directory")
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return sessionHistoryPage{}, fmt.Errorf("events path must not be a symlink")
	}
	f, err := os.Open(path)
	if err != nil {
		return sessionHistoryPage{}, err
	}
	defer f.Close()

	page := sessionHistoryPage{
		Records:   []sessionHistoryRecord{},
		NextAfter: after,
	}
	reader := bufio.NewReaderSize(f, 64*1024)
	lineNo := int64(-1)
	for {
		line, overLimit, err := jsonl.ReadBoundedLine(reader, maxHistoryLineBytes)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return sessionHistoryPage{}, err
		}
		lineNo++
		if overLimit {
			continue
		}
		var ev sse.Event
		if err := json.Unmarshal(line, &ev); err != nil {
			continue
		}
		if ev.Type == sse.TypeTraceMeta {
			var meta sse.TraceMetaPayload
			if err := json.Unmarshal(ev.Data, &meta); err == nil {
				if err := validateTraceSchemaVersion(meta.SchemaVersion); err != nil {
					return sessionHistoryPage{}, err
				}
				page.TraceSchemaVersion = meta.SchemaVersion
				page.TraceSchemaDetected = true
			}
		}
		if lineNo <= after {
			continue
		}
		if len(page.Records) >= limit {
			page.HasMore = true
			break
		}
		ev.ID = lineNo
		page.Records = append(page.Records, sessionHistoryRecord{Cursor: lineNo, Event: ev})
		page.NextAfter = lineNo
	}
	if after > lineNo {
		return sessionHistoryPage{}, fmt.Errorf("%w: after %d is ahead of latest event cursor %d", errEventCursorAhead, after, lineNo)
	}
	return page, nil
}

// handleSessionDelete closes a session immediately. Returns 204 even
// when the id is unknown (idempotent — clients calling
// "make sure this session is gone" don't need a 404).
func handleSessionDelete(pool *SessionPool, sessionID string, w http.ResponseWriter, _ *http.Request) {
	pool.Delete(sessionID)
	w.WriteHeader(http.StatusNoContent)
}
