package mcp

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/affinefoundation/affent/internal/jsonl"
	"github.com/rs/zerolog"
)

// httpWire implements MCP streamable-http transport (spec rev
// 2025-03-26). Single endpoint URL; client posts JSON-RPC requests; the
// server replies either as application/json (single response) or as
// text/event-stream (one or more JSON-RPC messages over SSE). Session
// id is established by the server in initialize and echoed back on
// every subsequent request via Mcp-Session-Id.
type httpWire struct {
	url     string
	headers map[string]string
	hc      *http.Client

	out     chan []byte   // server-originated frames
	closeCh chan struct{} // closed when close() begins; releases blocked emit() callers

	mu        sync.Mutex
	sessionID string

	closed atomic.Bool
	cancel context.CancelFunc
	wg     sync.WaitGroup

	log zerolog.Logger
}

const (
	maxHTTPJSONResponseBytes = 4 * 1024 * 1024
	maxHTTPSSELineBytes      = maxHTTPJSONResponseBytes
)

func newHTTPWire(_ context.Context, spec ServerSpec, log zerolog.Logger) (wire, error) {
	if spec.URL == "" {
		return nil, errors.New("mcp http: ServerSpec.URL is empty")
	}
	wireCtx, cancel := context.WithCancel(context.Background())
	w := &httpWire{
		url:     spec.URL,
		headers: spec.Headers,
		hc: &http.Client{
			// Per-request timeout is set on the request's ctx; the
			// transport-level timeout protects against open-but-idle
			// SSE responses that never finish.
			Timeout: 10 * time.Minute,
		},
		out:     make(chan []byte, 64),
		closeCh: make(chan struct{}),
		cancel:  cancel,
		log:     log,
	}
	_ = wireCtx // reserved for an optional GET-stream listener; not used in v0.
	return w, nil
}

func (w *httpWire) sendRequest(ctx context.Context, raw []byte) error {
	return w.post(ctx, raw, true)
}

func (w *httpWire) sendNotification(ctx context.Context, raw []byte) error {
	return w.post(ctx, raw, false)
}

func (w *httpWire) replies() <-chan []byte { return w.out }

func (w *httpWire) close() error {
	if !w.closed.CompareAndSwap(false, true) {
		return nil
	}
	// Free any emit() blocked on a full out chan before anything else.
	close(w.closeCh)
	w.cancel()
	// wg covers both drainSSE goroutines and post() bodies; once all
	// emit() callers have exited, closing out is panic-safe.
	w.wg.Wait()
	close(w.out)
	return nil
}

func (w *httpWire) post(ctx context.Context, body []byte, expectReply bool) error {
	// Track the post body in wg so close() waits for any in-flight
	// emit() the JSON branch is about to do before close(out) runs.
	w.wg.Add(1)
	defer w.wg.Done()
	if w.closed.Load() {
		return errors.New("http wire closed")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if sid := w.sid(); sid != "" {
		req.Header.Set("Mcp-Session-Id", sid)
	}
	for k, v := range w.headers {
		req.Header.Set(k, v)
	}

	resp, err := w.hc.Do(req)
	if err != nil {
		return fmt.Errorf("mcp http POST: %w", err)
	}

	// 202 Accepted means "got it, no body". Notifications go this way.
	if resp.StatusCode == http.StatusAccepted {
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		return nil
	}
	if resp.StatusCode/100 != 2 {
		// Cap the error-body read at 64 KiB; same rationale as
		// agent.LLMClient's non-2xx path — real MCP error envelopes
		// (jsonrpc error objects, nginx 502 pages) fit comfortably,
		// and an unbounded ReadAll would let a misbehaving server
		// OOM us with a multi-GB error body.
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		resp.Body.Close()
		return fmt.Errorf("mcp http %d: %s", resp.StatusCode, errBody)
	}

	// Server-assigned session id arrives in the initialize response. We
	// pin it for the rest of the connection's lifetime — server may
	// rotate, but for v0 we trust the first one it gives us.
	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" && w.sid() == "" {
		w.setSID(sid)
	}

	ct := resp.Header.Get("Content-Type")
	switch {
	case strings.HasPrefix(ct, "application/json"):
		body, err := readHTTPBodyCapped(resp.Body, maxHTTPJSONResponseBytes)
		resp.Body.Close()
		if err != nil {
			return err
		}
		// Some servers return an empty body even with 200; treat as
		// notification-style ack.
		if len(bytes.TrimSpace(body)) == 0 {
			return nil
		}
		w.emit(body)
		return nil
	case strings.HasPrefix(ct, "text/event-stream"):
		// Drain the SSE stream until the server closes. Each "data:"
		// line is a JSON-RPC message.
		w.wg.Add(1)
		go w.drainSSE(resp)
		return nil
	default:
		// Body is only used for the error message; 4 KiB is plenty
		// of context and bounds a misbehaving server's blast radius.
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4*1024))
		resp.Body.Close()
		if !expectReply && len(bytes.TrimSpace(body)) == 0 {
			return nil
		}
		return fmt.Errorf("mcp http: unexpected content-type %q (body: %s)", ct, body)
	}
}

func readHTTPBodyCapped(r io.Reader, limit int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("mcp http response body exceeds %d-byte limit", limit)
	}
	return body, nil
}

func (w *httpWire) drainSSE(resp *http.Response) {
	defer w.wg.Done()
	defer resp.Body.Close()
	r := bufio.NewReaderSize(resp.Body, 64*1024)
	for {
		line, overLimit, err := jsonl.ReadBoundedLine(r, maxHTTPSSELineBytes)
		if errors.Is(err, io.EOF) {
			return
		}
		if err != nil {
			w.log.Debug().Err(err).Msg("mcp http SSE")
			return
		}
		if overLimit {
			w.log.Debug().Int("limit_bytes", maxHTTPSSELineBytes).Msg("mcp http SSE line too large")
			continue
		}
		line = bytes.TrimRight(line, "\r\n")
		if len(line) == 0 {
			continue
		}
		// SSE frames: "event: ...", "data: ...", "id: ...", "retry: ...".
		// We only care about data lines; the JSON-RPC payload is in there.
		if !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		payload := bytes.TrimSpace(line[len("data:"):])
		if len(payload) == 0 || bytes.Equal(payload, []byte("[DONE]")) {
			continue
		}
		cp := make([]byte, len(payload))
		copy(cp, payload)
		w.emit(cp)
	}
}

func (w *httpWire) emit(frame []byte) {
	// Block on out so a slow consumer naturally backpressures the
	// server side instead of silently dropping JSON-RPC responses.
	// closeCh keeps shutdown from deadlocking when out is full.
	select {
	case w.out <- frame:
	case <-w.closeCh:
	}
}

func (w *httpWire) sid() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.sessionID
}

func (w *httpWire) setSID(s string) {
	w.mu.Lock()
	w.sessionID = s
	w.mu.Unlock()
}
