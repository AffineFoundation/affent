package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandleSessionTools_ListsActiveSessionRegistry(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	pool.cfg.EnableBuiltins = true
	if _, err := pool.GetOrCreate("tools-active"); err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}

	r := httptest.NewRequest(http.MethodGet, "/v1/sessions/tools-active/tools", nil)
	w := httptest.NewRecorder()
	handleSessionRoutes(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", got, w.Body.String())
	}
	var resp sessionToolsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v body=%s", err, w.Body.String())
	}
	if resp.SessionID != "tools-active" {
		t.Fatalf("session_id = %q, want tools-active", resp.SessionID)
	}
	if resp.Count != len(resp.Tools) || resp.Count == 0 {
		t.Fatalf("count/tools mismatch or empty: count=%d tools=%+v", resp.Count, resp.Tools)
	}
	if !toolCatalogHas(resp.Tools, "skill") {
		t.Fatalf("tool catalog missing skill: %+v", resp.Tools)
	}
	for _, tool := range resp.Tools {
		if strings.TrimSpace(tool.Name) == "" {
			t.Fatalf("tool has blank name: %+v", tool)
		}
		if len(tool.Parameters) == 0 {
			t.Fatalf("tool %s has empty parameters schema", tool.Name)
		}
	}
}

func TestHandleSessionTools_InactiveDurableSessionReturnsConflict(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	createDurableSessionDir(t, pool, "durable-only-tools")

	r := httptest.NewRequest(http.MethodGet, "/v1/sessions/durable-only-tools/tools", nil)
	w := httptest.NewRecorder()
	handleSessionRoutes(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", got, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "session_inactive") {
		t.Fatalf("body should explain inactive session: %s", w.Body.String())
	}
	if activeSessionByID(pool, "durable-only-tools") != nil {
		t.Fatal("GET tools must not reopen an inactive durable session")
	}
}

func TestHandleSessionTools_RejectsUnsafeID(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	r := httptest.NewRequest(http.MethodGet, "/v1/sessions/../tools", nil)
	w := httptest.NewRecorder()
	handleSessionTools(pool, "..", w, r)
	if got := w.Result().StatusCode; got != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", got, w.Body.String())
	}
}

func toolCatalogHas(tools []toolInfo, name string) bool {
	for _, tool := range tools {
		if tool.Name == name {
			return true
		}
	}
	return false
}
