package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/affinefoundation/affent/internal/memory"
)

func TestHandleSessionMemoryReadsDurableBuckets(t *testing.T) {
	pool := newPoolWithMemoryRoot(t, t.TempDir())
	createDurableSessionDir(t, pool, "memory-read")
	store := memory.NewFileMemoryStore("")
	store.MemoryDir = pool.sessionDirPath("memory-read")
	store.UserPath = filepath.Join(pool.sessionDirPath("memory-read"), "USER.md")
	if resp, err := store.Add(memory.TargetUser, "", "prefers concise reports"); err != nil || !resp.OK {
		t.Fatalf("add user memory: resp=%+v err=%v", resp, err)
	}
	if resp, err := store.Add(memory.TargetMemory, memory.CoreTopic, "project runs in containers"); err != nil || !resp.OK {
		t.Fatalf("add core memory: resp=%+v err=%v", resp, err)
	}
	if resp, err := store.Add(memory.TargetMemory, "research", "taostats pages are dynamic"); err != nil || !resp.OK {
		t.Fatalf("add topic memory: resp=%+v err=%v", resp, err)
	}

	r := httptest.NewRequest(http.MethodGet, "/v1/sessions/memory-read/memory", nil)
	w := httptest.NewRecorder()
	handleSessionRoutes(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", got, w.Body.String())
	}
	var out sessionMemoryResponse
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v\n%s", err, w.Body.String())
	}
	if out.SessionID != "memory-read" || !out.HasMemory {
		t.Fatalf("memory response identity = %+v", out)
	}
	if out.User == nil || out.User.EntryCount != 1 || out.User.Entries[0] != "prefers concise reports" {
		t.Fatalf("user bucket = %+v", out.User)
	}
	if out.User.Preview != "prefers concise reports" {
		t.Fatalf("user preview = %q", out.User.Preview)
	}
	if out.Core == nil || out.Core.EntryCount != 1 || out.Core.Entries[0] != "project runs in containers" {
		t.Fatalf("core bucket = %+v", out.Core)
	}
	if len(out.Topics) != 1 || out.Topics[0].Topic != "research" || out.Topics[0].Entries[0] != "taostats pages are dynamic" {
		t.Fatalf("topics = %+v", out.Topics)
	}
	if out.Topics[0].Preview != "taostats pages are dynamic" {
		t.Fatalf("topic preview = %q", out.Topics[0].Preview)
	}
}

func TestHandleSessionMemoryAddsDurableMemory(t *testing.T) {
	pool := newPoolWithMemoryRoot(t, t.TempDir())
	createDurableSessionDir(t, pool, "memory-add")

	r := httptest.NewRequest(http.MethodPost, "/v1/sessions/memory-add/memory", bytes.NewBufferString(`{
		"target": "memory",
		"topic": "research",
		"content": "CoinGecko pages require browser fallback"
	}`))
	w := httptest.NewRecorder()
	handleSessionRoutes(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", got, w.Body.String())
	}
	var out sessionMemoryResponse
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v\n%s", err, w.Body.String())
	}
	if !out.HasMemory || len(out.Topics) != 1 || out.Topics[0].Topic != "research" {
		t.Fatalf("memory response = %+v", out)
	}
	if got := strings.Join(out.Topics[0].Entries, "\n"); !strings.Contains(got, "CoinGecko pages require browser fallback") {
		t.Fatalf("topic entries = %q", got)
	}

	r = httptest.NewRequest(http.MethodGet, "/v1/sessions/memory-add/memory", nil)
	w = httptest.NewRecorder()
	handleSessionRoutes(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusOK {
		t.Fatalf("get status = %d, want 200; body=%s", got, w.Body.String())
	}
	out = sessionMemoryResponse{}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode get response: %v\n%s", err, w.Body.String())
	}
	if len(out.Topics) != 1 || out.Topics[0].EntryCount != 1 {
		t.Fatalf("durable topics = %+v", out.Topics)
	}
}

func TestHandleSessionMemoryRemovesDurableMemory(t *testing.T) {
	pool := newPoolWithMemoryRoot(t, t.TempDir())
	createDurableSessionDir(t, pool, "memory-remove")
	store := memory.NewFileMemoryStore("")
	store.MemoryDir = pool.sessionDirPath("memory-remove")
	if resp, err := store.Add(memory.TargetMemory, "research", "obsolete browser fallback rule"); err != nil || !resp.OK {
		t.Fatalf("add topic memory: resp=%+v err=%v", resp, err)
	}
	if resp, err := store.Add(memory.TargetMemory, "research", "keep current evidence rule"); err != nil || !resp.OK {
		t.Fatalf("add topic memory: resp=%+v err=%v", resp, err)
	}

	r := httptest.NewRequest(http.MethodPost, "/v1/sessions/memory-remove/memory", bytes.NewBufferString(`{
		"action": "remove",
		"target": "memory",
		"topic": "research",
		"old_text": "obsolete browser fallback"
	}`))
	w := httptest.NewRecorder()
	handleSessionRoutes(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", got, w.Body.String())
	}
	var out sessionMemoryResponse
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v\n%s", err, w.Body.String())
	}
	if !out.HasMemory || len(out.Topics) != 1 || out.Topics[0].EntryCount != 1 {
		t.Fatalf("memory response = %+v", out)
	}
	gotEntries := strings.Join(out.Topics[0].Entries, "\n")
	if strings.Contains(gotEntries, "obsolete browser fallback") || !strings.Contains(gotEntries, "keep current evidence rule") {
		t.Fatalf("topic entries after remove = %q", gotEntries)
	}
}

func TestHandleSessionMemoryReplacesDurableMemory(t *testing.T) {
	pool := newPoolWithMemoryRoot(t, t.TempDir())
	createDurableSessionDir(t, pool, "memory-replace")
	store := memory.NewFileMemoryStore("")
	store.MemoryDir = pool.sessionDirPath("memory-replace")
	if resp, err := store.Add(memory.TargetMemory, "research", "stale browser fallback rule"); err != nil || !resp.OK {
		t.Fatalf("add topic memory: resp=%+v err=%v", resp, err)
	}

	r := httptest.NewRequest(http.MethodPost, "/v1/sessions/memory-replace/memory", bytes.NewBufferString(`{
		"action": "replace",
		"target": "memory",
		"topic": "research",
		"old_text": "stale browser fallback",
		"new_content": "current browser fallback rule"
	}`))
	w := httptest.NewRecorder()
	handleSessionRoutes(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", got, w.Body.String())
	}
	var out sessionMemoryResponse
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v\n%s", err, w.Body.String())
	}
	if !out.HasMemory || len(out.Topics) != 1 || out.Topics[0].EntryCount != 1 {
		t.Fatalf("memory response = %+v", out)
	}
	gotEntries := strings.Join(out.Topics[0].Entries, "\n")
	if strings.Contains(gotEntries, "stale browser fallback") || !strings.Contains(gotEntries, "current browser fallback rule") {
		t.Fatalf("topic entries after replace = %q", gotEntries)
	}
}

func TestHandleSessionMemoryAddRejectsInvalidContent(t *testing.T) {
	pool := newPoolWithMemoryRoot(t, t.TempDir())
	createDurableSessionDir(t, pool, "memory-add-invalid")

	r := httptest.NewRequest(http.MethodPost, "/v1/sessions/memory-add-invalid/memory", bytes.NewBufferString(`{"target":"memory","topic":"research","content":"   "}`))
	w := httptest.NewRecorder()
	handleSessionRoutes(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", got, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "content is required") {
		t.Fatalf("body = %s, want content validation", w.Body.String())
	}
}

func TestHandleSessionMemoryRemoveRejectsMissingOldText(t *testing.T) {
	pool := newPoolWithMemoryRoot(t, t.TempDir())
	createDurableSessionDir(t, pool, "memory-remove-invalid")

	r := httptest.NewRequest(http.MethodPost, "/v1/sessions/memory-remove-invalid/memory", bytes.NewBufferString(`{"action":"remove","target":"memory","topic":"research"}`))
	w := httptest.NewRecorder()
	handleSessionRoutes(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", got, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "old_text is required") {
		t.Fatalf("body = %s, want old_text validation", w.Body.String())
	}
}

func TestHandleSessionMemoryReadsSharedUserBucket(t *testing.T) {
	memRoot := t.TempDir()
	pool := newPoolWithMemoryRoot(t, memRoot)
	pool.cfg.SharedUserMemory = true
	createDurableSessionDir(t, pool, "shared-memory-read")
	store := memory.NewFileMemoryStore("")
	store.MemoryDir = pool.sessionDirPath("shared-memory-read")
	store.UserPath = pool.sharedUserMemoryPath()
	if resp, err := store.Add(memory.TargetUser, "", "shared user preference"); err != nil || !resp.OK {
		t.Fatalf("add shared user memory: resp=%+v err=%v", resp, err)
	}

	r := httptest.NewRequest(http.MethodGet, "/v1/sessions/shared-memory-read/memory", nil)
	w := httptest.NewRecorder()
	handleSessionRoutes(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", got, w.Body.String())
	}
	var out sessionMemoryResponse
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v\n%s", err, w.Body.String())
	}
	if !out.HasMemory || !out.SharedUserMemory {
		t.Fatalf("shared memory flags = has:%v shared:%v", out.HasMemory, out.SharedUserMemory)
	}
	if out.User == nil || out.User.EntryCount != 1 || out.User.Entries[0] != "shared user preference" {
		t.Fatalf("shared user bucket = %+v", out.User)
	}
}

func TestHandleSessionMemoryReturnsBoundedBucketPreview(t *testing.T) {
	pool := newPoolWithMemoryRoot(t, t.TempDir())
	createDurableSessionDir(t, pool, "memory-preview")
	store := memory.NewFileMemoryStore("")
	store.MemoryDir = pool.sessionDirPath("memory-preview")
	long := strings.Repeat("alpha ", 80)
	if resp, err := store.Add(memory.TargetMemory, "research", long); err != nil || !resp.OK {
		t.Fatalf("add topic memory: resp=%+v err=%v", resp, err)
	}

	r := httptest.NewRequest(http.MethodGet, "/v1/sessions/memory-preview/memory", nil)
	w := httptest.NewRecorder()
	handleSessionRoutes(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", got, w.Body.String())
	}
	var out sessionMemoryResponse
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v\n%s", err, w.Body.String())
	}
	if len(out.Topics) != 1 || out.Topics[0].Preview == "" {
		t.Fatalf("topics = %+v, want preview", out.Topics)
	}
	if !strings.HasPrefix(out.Topics[0].Preview, "alpha alpha") || !strings.HasSuffix(out.Topics[0].Preview, "...") {
		t.Fatalf("preview = %q, want bounded compact preview", out.Topics[0].Preview)
	}
	if len(out.Topics[0].Preview) > sessionMemoryBucketPreviewChars+3 {
		t.Fatalf("preview len = %d, want <= %d", len(out.Topics[0].Preview), sessionMemoryBucketPreviewChars+3)
	}
}

func TestHandleSessionMemoryMissingSession(t *testing.T) {
	pool := newPoolWithMemoryRoot(t, t.TempDir())
	r := httptest.NewRequest(http.MethodGet, "/v1/sessions/missing/memory", nil)
	w := httptest.NewRecorder()
	handleSessionRoutes(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", got, w.Body.String())
	}
}
