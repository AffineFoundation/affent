package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
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
	if out.Core == nil || out.Core.EntryCount != 1 || out.Core.Entries[0] != "project runs in containers" {
		t.Fatalf("core bucket = %+v", out.Core)
	}
	if len(out.Topics) != 1 || out.Topics[0].Topic != "research" || out.Topics[0].Entries[0] != "taostats pages are dynamic" {
		t.Fatalf("topics = %+v", out.Topics)
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
