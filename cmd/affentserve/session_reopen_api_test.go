package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestHandleSessionCreate_ReopensDurableSessionAfterRestart(t *testing.T) {
	memRoot := t.TempDir()
	pool1 := newPoolWithMemoryRoot(t, memRoot)
	createDurableSessionDir(t, pool1, "restart-create")
	if err := os.WriteFile(filepath.Join(pool1.sessionDirPath("restart-create"), "conversation.jsonl"), []byte(
		`{"role":"system","content":"test"}`+"\n"+
			`{"role":"user","content":"resume my durable task"}`+"\n",
	), 0o644); err != nil {
		t.Fatalf("write durable conversation: %v", err)
	}
	pool1.Shutdown()

	pool2 := newPoolWithMemoryRoot(t, memRoot)
	r := httptest.NewRequest(http.MethodPost, "/v1/sessions", bytes.NewBufferString(`{"session_id":"restart-create"}`))
	w := httptest.NewRecorder()
	handleSessionsCollection(pool2).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusOK {
		t.Fatalf("status = %d, want 200 reopen; body=%s", got, w.Body.String())
	}

	var resp sessionCreateResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Session.ID != "restart-create" || !resp.Session.Active || !resp.Session.Durable || !resp.Session.HasConversation || !resp.Session.HasEvents {
		t.Fatalf("session = %+v, want reopened active durable session with persisted state markers", resp.Session)
	}
	if resp.Session.LatestUserMessage != "resume my durable task" {
		t.Fatalf("latest_user_message = %q, want durable conversation summary", resp.Session.LatestUserMessage)
	}
	if activeSessionByID(pool2, "restart-create") == nil {
		t.Fatal("POST create must reopen the durable session into the active pool")
	}
}
