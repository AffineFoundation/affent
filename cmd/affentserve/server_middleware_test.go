package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rs/zerolog"
)

// TestLogRequests_CapturesStatusFromInner pins the access-log
// middleware's load-bearing detail: the response status logged
// must match what the inner handler returned. A regression where
// the recorder fails to capture the WriteHeader call would log
// 200 for every request, even errors — silently corrupting the
// operator's audit trail.
func TestLogRequests_CapturesStatusFromInner(t *testing.T) {
	var buf bytes.Buffer
	logger := zerolog.New(&buf)

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot) // 418 — distinctive enough to spot
		_, _ = w.Write([]byte("nope"))
	})
	h := logRequests(logger, inner)

	r := httptest.NewRequest(http.MethodGet, "/echo", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if got := w.Result().StatusCode; got != http.StatusTeapot {
		t.Errorf("downstream status not preserved: got %d", got)
	}
	logged := buf.String()
	if !strings.Contains(logged, `"status":418`) {
		t.Errorf("access log did not record 418; got %s", logged)
	}
	if !strings.Contains(logged, `"method":"GET"`) || !strings.Contains(logged, `"path":"/echo"`) {
		t.Errorf("access log missing method/path; got %s", logged)
	}
}

// TestLogRequests_DefaultStatusWhenInnerSkipsWriteHeader pins the
// case where the inner handler calls Write() without an explicit
// WriteHeader — Go's net/http implicitly writes 200 in that path.
// Our recorder must report 200 too, not the zero value.
func TestLogRequests_DefaultStatusWhenInnerSkipsWriteHeader(t *testing.T) {
	var buf bytes.Buffer
	logger := zerolog.New(&buf)
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("implicit 200"))
	})
	h := logRequests(logger, inner)

	r := httptest.NewRequest(http.MethodGet, "/ok", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if !strings.Contains(buf.String(), `"status":200`) {
		t.Errorf("expected default 200 in log; got %s", buf.String())
	}
}

// TestResponseRecorder_WriteHeaderIsIdempotent pins the double-
// write guard: a buggy handler that calls WriteHeader twice
// should not corrupt the captured status (and net/http itself
// already complains about double-writes; we don't want to make
// it worse).
func TestResponseRecorder_WriteHeaderIsIdempotent(t *testing.T) {
	rec := httptest.NewRecorder()
	rr := &responseRecorder{ResponseWriter: rec, status: 200}

	rr.WriteHeader(201)
	rr.WriteHeader(500) // must be ignored by our recorder

	if rr.status != 201 {
		t.Errorf("captured status = %d, want 201 (second WriteHeader must be ignored)", rr.status)
	}
}

// TestResponseRecorder_FlushDelegates pins the SSE-keepalive
// requirement: handlers that need to push partial responses
// (streamChatCompletion / handleSessionEvents) call Flush(); if
// our recorder swallows the call, those streams would never
// reach the client until the handler returned.
func TestResponseRecorder_FlushDelegates(t *testing.T) {
	// flushySpy records whether Flush was forwarded.
	spy := &flushySpy{ResponseWriter: httptest.NewRecorder()}
	rr := &responseRecorder{ResponseWriter: spy, status: 200}

	rr.Flush()
	if !spy.flushed {
		t.Error("Flush must delegate to wrapped writer when it implements http.Flusher")
	}
}

type flushySpy struct {
	http.ResponseWriter
	flushed bool
}

func (s *flushySpy) Flush() { s.flushed = true }

// TestResponseRecorder_FlushOnNonFlushyWriterIsNoOp pins the
// type-assertion guard: if the wrapped writer doesn't implement
// Flusher (e.g. a test recorder that lacks it), the recorder's
// Flush must silently no-op rather than panic.
func TestResponseRecorder_FlushOnNonFlushyWriterIsNoOp(t *testing.T) {
	rr := &responseRecorder{ResponseWriter: nonFlushy{}, status: 200}
	rr.Flush() // must not panic
}

type nonFlushy struct{}

func (nonFlushy) Header() http.Header         { return http.Header{} }
func (nonFlushy) Write(b []byte) (int, error) { return len(b), nil }
func (nonFlushy) WriteHeader(int)             {}

// TestNewRouter_RegistersExpectedRoutes pins that the router has
// the documented endpoints wired. A registered route may legitimately
// 404 (e.g. /v1/sessions/{unknown}/events returns 404 from the
// handler), so we can't just check status — we check Content-Type:
// net/http's default-mux 404 returns text/html, while affentserve's
// writeJSONError returns application/json. The Content-Type
// distinguishes "route not wired" from "route handled the request".
func TestNewRouter_RegistersExpectedRoutes(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	cfg := Config{Model: "fake"}
	router := newRouter(cfg, pool, zerolog.New(io.Discard))

	cases := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/healthz"},
		{http.MethodGet, "/v1/models"},
		{http.MethodPost, "/v1/chat/completions"},
		{http.MethodGet, "/v1/sessions"},
		{http.MethodPost, "/v1/sessions"},
		{http.MethodGet, "/v1/sessions/abc"},
		{http.MethodGet, "/v1/sessions/abc/events"}, // handler-404 (session unknown) is OK
		{http.MethodGet, "/v1/sessions/abc/tools"},
		{http.MethodDelete, "/v1/sessions/abc"},
		{http.MethodGet, "/v1/stats"},
	}
	for _, c := range cases {
		t.Run(c.method+" "+c.path, func(t *testing.T) {
			r := httptest.NewRequest(c.method, c.path, nil)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, r)
			ct := w.Result().Header.Get("Content-Type")
			if strings.Contains(ct, "text/html") {
				t.Errorf("router didn't recognize %s %s (got default-mux html 404)", c.method, c.path)
			}
		})
	}
}

// TestLoadDotEnv_LooksAtCwdAndHomeConfig pins the wrapper's
// lookup order: ./.env first, then ~/.config/affent/.env. Either
// missing is fine; either present and unset-by-env populates.
func TestLoadDotEnv_LooksAtCwdAndHomeConfig(t *testing.T) {
	// Isolate HOME + cwd so the real user's environment doesn't
	// interfere.
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".config", "affent"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Write a HOME-scope .env with a key the test can detect.
	if err := os.WriteFile(filepath.Join(home, ".config", "affent", ".env"),
		[]byte("AFFENT_LOAD_DOTENV_HOME=from-home\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Cwd-scope .env in a temp dir.
	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, ".env"),
		[]byte("AFFENT_LOAD_DOTENV_CWD=from-cwd\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// chdir into cwd for the duration of this test.
	orig, _ := os.Getwd()
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })

	// Pre-condition: neither var is set.
	os.Unsetenv("AFFENT_LOAD_DOTENV_CWD")
	os.Unsetenv("AFFENT_LOAD_DOTENV_HOME")
	t.Cleanup(func() {
		os.Unsetenv("AFFENT_LOAD_DOTENV_CWD")
		os.Unsetenv("AFFENT_LOAD_DOTENV_HOME")
	})

	if err := loadDotEnv(); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("AFFENT_LOAD_DOTENV_CWD"); got != "from-cwd" {
		t.Errorf("cwd .env not loaded; AFFENT_LOAD_DOTENV_CWD=%q", got)
	}
	if got := os.Getenv("AFFENT_LOAD_DOTENV_HOME"); got != "from-home" {
		t.Errorf("home .env not loaded; AFFENT_LOAD_DOTENV_HOME=%q", got)
	}
}
