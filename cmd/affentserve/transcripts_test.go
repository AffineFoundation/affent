package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHandleSessionTranscripts_ListsFocusedAndSubagentTranscripts(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	writeTranscriptFixture(t, pool, "traceable", "focused-tasks", "focused_one", "focused body\n")
	writeTranscriptFixture(t, pool, "traceable", "subagents", "subagent_one", "subagent body\n")

	r := httptest.NewRequest(http.MethodGet, "/v1/sessions/traceable/transcripts", nil)
	w := httptest.NewRecorder()
	handleSessionRoutes(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", got, w.Body.String())
	}
	var resp transcriptListResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v body=%s", err, w.Body.String())
	}
	if resp.SessionID != "traceable" {
		t.Fatalf("session_id = %q, want traceable", resp.SessionID)
	}
	if len(resp.Transcripts) != 2 {
		t.Fatalf("transcripts = %+v, want 2", resp.Transcripts)
	}
	got := map[string]transcriptInfo{}
	for _, tr := range resp.Transcripts {
		got[tr.Path] = tr
	}
	focusedPath := "focused-tasks/traceable/focused_one.jsonl"
	if got[focusedPath].Kind != "focused_task" || got[focusedPath].ChildID != "focused_one" {
		t.Fatalf("focused transcript info = %+v", got[focusedPath])
	}
	subagentPath := "subagents/traceable/subagent_one.jsonl"
	if got[subagentPath].Kind != "subagent" || got[subagentPath].ChildID != "subagent_one" {
		t.Fatalf("subagent transcript info = %+v", got[subagentPath])
	}
}

func TestHandleSessionTranscripts_ListSkipsSymlinks(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	root := filepath.Join(pool.sessionDirPath("traceable-links"), "focused-tasks", "traceable-links")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "real.jsonl"), []byte("real\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.jsonl")
	if err := os.WriteFile(outside, []byte("outside\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "linked.jsonl")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	r := httptest.NewRequest(http.MethodGet, "/v1/sessions/traceable-links/transcripts", nil)
	w := httptest.NewRecorder()
	handleSessionRoutes(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", got, w.Body.String())
	}
	var resp transcriptListResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v body=%s", err, w.Body.String())
	}
	if len(resp.Transcripts) != 1 || resp.Transcripts[0].ChildID != "real" {
		t.Fatalf("transcript list should include only real transcript: %+v", resp.Transcripts)
	}
}

func TestHandleSessionTranscripts_ReadsBoundedChunk(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	writeTranscriptFixture(t, pool, "traceable", "focused-tasks", "focused_one", "0123456789")

	r := httptest.NewRequest(http.MethodGet, "/v1/sessions/traceable/transcripts/focused-tasks/traceable/focused_one.jsonl?offset=2&limit=4", nil)
	w := httptest.NewRecorder()
	handleSessionRoutes(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", got, w.Body.String())
	}
	if got := w.Body.String(); got != "2345" {
		t.Fatalf("body = %q, want 2345", got)
	}
	if got := w.Result().Header.Get("Content-Type"); !strings.Contains(got, "application/x-ndjson") {
		t.Fatalf("content-type = %q, want application/x-ndjson", got)
	}
	if got := w.Result().Header.Get("X-Affent-Transcript-Path"); got != "focused-tasks/traceable/focused_one.jsonl" {
		t.Fatalf("transcript path header = %q", got)
	}
}

func TestHandleSessionTranscripts_RejectsTraversalAndWrongSession(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	writeTranscriptFixture(t, pool, "safe", "focused-tasks", "focused_one", "ok\n")

	cases := []string{
		"/v1/sessions/safe/transcripts/../conversation.jsonl",
		"/v1/sessions/safe/transcripts/focused-tasks/other/focused_one.jsonl",
		"/v1/sessions/safe/transcripts/focused-tasks/safe/nested/focused_one.jsonl",
	}
	for _, url := range cases {
		r := httptest.NewRequest(http.MethodGet, url, nil)
		w := httptest.NewRecorder()
		handleSessionRoutes(pool).ServeHTTP(w, r)
		if got := w.Result().StatusCode; got != http.StatusBadRequest {
			t.Fatalf("%s status = %d, want 400; body=%s", url, got, w.Body.String())
		}
	}
}

func TestHandleSessionTranscripts_RejectsSymlinkEscape(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	root := filepath.Join(pool.sessionDirPath("safe"), "focused-tasks", "safe")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.jsonl")
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape.jsonl")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	r := httptest.NewRequest(http.MethodGet, "/v1/sessions/safe/transcripts/focused-tasks/safe/escape.jsonl", nil)
	w := httptest.NewRecorder()
	handleSessionRoutes(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", got, w.Body.String())
	}
}

func TestHandleSessionTranscripts_RejectsSymlinkInsideTranscriptRoot(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	root := filepath.Join(pool.sessionDirPath("safe"), "focused-tasks", "safe")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "real.jsonl"), []byte("real\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "real.jsonl"), filepath.Join(root, "alias.jsonl")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	r := httptest.NewRequest(http.MethodGet, "/v1/sessions/safe/transcripts/focused-tasks/safe/alias.jsonl", nil)
	w := httptest.NewRecorder()
	handleSessionRoutes(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", got, w.Body.String())
	}
}

func TestHandleSessionTranscripts_MissingTranscriptReturns404(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	root := filepath.Join(pool.sessionDirPath("safe"), "focused-tasks", "safe")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}

	r := httptest.NewRequest(http.MethodGet, "/v1/sessions/safe/transcripts/focused-tasks/safe/missing.jsonl", nil)
	w := httptest.NewRecorder()
	handleSessionRoutes(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", got, w.Body.String())
	}
}

func TestHandleSessionTranscripts_MissingSession(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	r := httptest.NewRequest(http.MethodGet, "/v1/sessions/missing/transcripts", nil)
	w := httptest.NewRecorder()
	handleSessionRoutes(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", got, w.Body.String())
	}
}

func writeTranscriptFixture(t *testing.T, pool *SessionPool, sessionID, kindDir, childID, body string) {
	t.Helper()
	dir := filepath.Join(pool.sessionDirPath(sessionID), kindDir, sessionID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, childID+".jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
