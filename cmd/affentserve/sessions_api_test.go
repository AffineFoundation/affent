package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/rs/zerolog"
)

func TestHandleSessionCreate_ExplicitIDAndDetail(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	pool.cfg.EnableBuiltins = true
	pool.cfg.EnableMemory = true
	pool.cfg.EnableWeb = true
	pool.cfg.EnableSubagent = true
	pool.cfg.SubagentMaxDepth = 3
	pool.cfg.EnableFocusedTasks = true
	h := handleSessionsCollection(pool)

	r := httptest.NewRequest(http.MethodPost, "/v1/sessions", bytes.NewBufferString(`{"session_id":"client-one"}`))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusCreated {
		t.Fatalf("create status = %d, want 201; body=%s", got, w.Body.String())
	}
	var created sessionCreateResponse
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	if created.Session.ID != "client-one" || !created.Session.Active || !created.Session.Durable {
		t.Fatalf("created session = %+v, want active durable client-one", created.Session)
	}
	assertSessionCapabilities(t, created.Session.Capabilities, sessionCapabilities{
		Builtins:         true,
		SkillInstall:     true,
		Plan:             true,
		Memory:           true,
		SessionSearch:    false,
		Web:              true,
		Subagent:         true,
		SubagentMaxDepth: 3,
		FocusedTasks:     true,
	})

	r = httptest.NewRequest(http.MethodPost, "/v1/sessions", bytes.NewBufferString(`{"session_id":"client-one"}`))
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusOK {
		t.Fatalf("duplicate create status = %d, want 200; body=%s", got, w.Body.String())
	}

	r = httptest.NewRequest(http.MethodGet, "/v1/sessions/client-one", nil)
	w = httptest.NewRecorder()
	handleSessionRoutes(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusOK {
		t.Fatalf("detail status = %d, want 200; body=%s", got, w.Body.String())
	}
	var detail sessionDetailResponse
	if err := json.Unmarshal(w.Body.Bytes(), &detail); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	if detail.Session.ID != "client-one" || !detail.Session.Active || !detail.Session.Durable {
		t.Fatalf("detail session = %+v, want active durable client-one", detail.Session)
	}
	assertSessionCapabilities(t, detail.Session.Capabilities, *created.Session.Capabilities)
}

func TestHandleSessionCreate_GeneratedID(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	r := httptest.NewRequest(http.MethodPost, "/v1/sessions", http.NoBody)
	w := httptest.NewRecorder()
	handleSessionsCollection(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", got, w.Body.String())
	}
	var resp sessionCreateResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Session.ID == "" || !resp.Session.Active || !resp.Session.Durable {
		t.Fatalf("generated session = %+v, want id active durable", resp.Session)
	}
}

func TestHandleSessionCreate_RejectsInvalidID(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	r := httptest.NewRequest(http.MethodPost, "/v1/sessions", bytes.NewBufferString(`{"session_id":"../escape"}`))
	w := httptest.NewRecorder()
	handleSessionsCollection(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", got, w.Body.String())
	}
}

func TestHandleSessionList_MergesActiveAndDurableSessions(t *testing.T) {
	pool := newTestPool(t, 8, "5m")
	if _, err := pool.GetOrCreate("active"); err != nil {
		t.Fatalf("GetOrCreate active: %v", err)
	}
	createDurableSessionDir(t, pool, "archived")

	r := httptest.NewRequest(http.MethodGet, "/v1/sessions", nil)
	w := httptest.NewRecorder()
	handleSessionsCollection(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", got, w.Body.String())
	}
	var resp sessionListResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Sessions) != 2 {
		t.Fatalf("sessions = %+v, want 2", resp.Sessions)
	}
	if resp.Sessions[0].ID != "active" || !resp.Sessions[0].Active || !resp.Sessions[0].Durable {
		t.Fatalf("first session = %+v, want active durable active", resp.Sessions[0])
	}
	if resp.Sessions[1].ID != "archived" || resp.Sessions[1].Active || !resp.Sessions[1].Durable {
		t.Fatalf("second session = %+v, want durable-only archived", resp.Sessions[1])
	}
}

func TestHandleSessionList_PaginatesBySessionID(t *testing.T) {
	memRoot := t.TempDir()
	pool := newPoolWithMemoryRoot(t, memRoot)
	for _, id := range []string{"alpha", "bravo", "charlie"} {
		createDurableSessionDir(t, pool, id)
	}

	r := httptest.NewRequest(http.MethodGet, "/v1/sessions?limit=2", nil)
	w := httptest.NewRecorder()
	handleSessionsCollection(pool).ServeHTTP(w, r)
	var page1 sessionListResponse
	if err := json.Unmarshal(w.Body.Bytes(), &page1); err != nil {
		t.Fatalf("decode page1: %v body=%s", err, w.Body.String())
	}
	if got, want := sessionIDs(page1.Sessions), []string{"alpha", "bravo"}; !sameStrings(got, want) {
		t.Fatalf("page1 ids = %v, want %v", got, want)
	}
	if !page1.HasMore || page1.NextAfter != "bravo" {
		t.Fatalf("page1 cursor = has_more:%v next:%q, want true/bravo", page1.HasMore, page1.NextAfter)
	}

	r = httptest.NewRequest(http.MethodGet, "/v1/sessions?limit=2&after=bravo", nil)
	w = httptest.NewRecorder()
	handleSessionsCollection(pool).ServeHTTP(w, r)
	var page2 sessionListResponse
	if err := json.Unmarshal(w.Body.Bytes(), &page2); err != nil {
		t.Fatalf("decode page2: %v body=%s", err, w.Body.String())
	}
	if got, want := sessionIDs(page2.Sessions), []string{"charlie"}; !sameStrings(got, want) {
		t.Fatalf("page2 ids = %v, want %v", got, want)
	}
	if page2.HasMore || page2.NextAfter != "" {
		t.Fatalf("page2 cursor = has_more:%v next:%q, want false/empty", page2.HasMore, page2.NextAfter)
	}
}

func TestHandleSessionDetail_ReadsDurableSessionAfterRestart(t *testing.T) {
	memRoot := t.TempDir()
	pool1 := newPoolWithMemoryRoot(t, memRoot)
	createDurableSessionDir(t, pool1, "restartable")
	if err := os.WriteFile(filepath.Join(pool1.sessionDirPath("restartable"), "plan.json"), []byte(`{"version":1,"steps":[{"text":"resume work","status":"pending"}]}`+"\n"), 0o644); err != nil {
		t.Fatalf("write plan: %v", err)
	}
	pool1.Shutdown()

	pool2 := newPoolWithMemoryRoot(t, memRoot)
	r := httptest.NewRequest(http.MethodGet, "/v1/sessions/restartable", nil)
	w := httptest.NewRecorder()
	handleSessionRoutes(pool2).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", got, w.Body.String())
	}
	var resp sessionDetailResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Session.ID != "restartable" || resp.Session.Active || !resp.Session.Durable || !resp.Session.HasConversation {
		t.Fatalf("session = %+v, want durable-only restartable with conversation", resp.Session)
	}
	if !resp.Session.HasPlan {
		t.Fatalf("session = %+v, want durable-only restartable with plan", resp.Session)
	}
	if resp.Session.Capabilities != nil {
		t.Fatalf("durable-only session should not report active capabilities, got %+v", resp.Session.Capabilities)
	}
}

func TestHandleSessionPlan_ReadsDurablePlanWithoutReopeningSession(t *testing.T) {
	memRoot := t.TempDir()
	pool := newPoolWithMemoryRoot(t, memRoot)
	createDurableSessionDir(t, pool, "planned")
	planJSON := `{"version":1,"steps":[{"text":"resume work","status":"in_progress"}]}`
	if err := os.WriteFile(filepath.Join(pool.sessionDirPath("planned"), "plan.json"), []byte(planJSON+"\n"), 0o644); err != nil {
		t.Fatalf("write plan: %v", err)
	}

	r := httptest.NewRequest(http.MethodGet, "/v1/sessions/planned/plan", nil)
	w := httptest.NewRecorder()
	handleSessionRoutes(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", got, w.Body.String())
	}
	if activeSessionByID(pool, "planned") != nil {
		t.Fatal("GET plan must not reopen an inactive durable session")
	}
	var resp sessionPlanResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.SessionID != "planned" {
		t.Fatalf("session_id = %q, want planned", resp.SessionID)
	}
	var plan struct {
		Steps []struct {
			Text   string `json:"text"`
			Status string `json:"status"`
		} `json:"steps"`
	}
	if err := json.Unmarshal(resp.Plan, &plan); err != nil {
		t.Fatalf("decode plan: %v", err)
	}
	if len(plan.Steps) != 1 || plan.Steps[0].Text != "resume work" || plan.Steps[0].Status != "in_progress" {
		t.Fatalf("plan = %+v", plan)
	}
}

func TestHandleSessionPlan_Returns404WhenNoPlan(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	createDurableSessionDir(t, pool, "no-plan")

	r := httptest.NewRequest(http.MethodGet, "/v1/sessions/no-plan/plan", nil)
	w := httptest.NewRecorder()
	handleSessionRoutes(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", got, w.Body.String())
	}
}

func TestSessionCapabilitiesReflectActualRegisteredTools(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	pool.cfg.EnableBuiltins = false
	pool.cfg.EnableMemory = true
	pool.cfg.EnableSubagent = true
	pool.cfg.EnableFocusedTasks = true
	s, err := pool.GetOrCreate("tool-light")
	if err != nil {
		t.Fatal(err)
	}
	caps := summarizeActiveCapabilities(s, pool.cfg)
	if caps.Builtins || caps.SkillInstall || caps.Plan || caps.SessionSearch {
		t.Fatalf("tool-light session should not report builtin-only tools: %+v", caps)
	}
	if !caps.Memory || !caps.Subagent || !caps.FocusedTasks {
		t.Fatalf("tool-light session should report actually registered non-builtin tools: %+v", caps)
	}
}

func TestHandleSessionDetail_RejectsUnsafeID(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	r := httptest.NewRequest(http.MethodGet, "/v1/sessions/..", nil)
	w := httptest.NewRecorder()
	handleSessionDetail(pool, "..", w, r)
	if got := w.Result().StatusCode; got != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", got, w.Body.String())
	}
}

func newPoolWithMemoryRoot(t *testing.T, memRoot string) *SessionPool {
	t.Helper()
	cfg := Config{
		Listen:         "127.0.0.1:0",
		MaxSessions:    8,
		SessionIdleTTL: "5m",
		WorkspaceRoot:  t.TempDir(),
		MemoryRoot:     memRoot,
		BaseURL:        "http://127.0.0.1:0",
		APIKey:         "test",
		Model:          "fake",
	}
	pool, err := NewSessionPool(cfg, zerolog.New(io.Discard))
	if err != nil {
		t.Fatalf("NewSessionPool: %v", err)
	}
	t.Cleanup(pool.Shutdown)
	return pool
}

func createDurableSessionDir(t *testing.T, pool *SessionPool, id string) {
	t.Helper()
	dir := pool.sessionDirPath(id)
	if err := os.MkdirAll(filepath.Join(dir, "topics"), 0o755); err != nil {
		t.Fatalf("mkdir session dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "conversation.jsonl"), []byte(`{"role":"system","content":"test"}`+"\n"), 0o644); err != nil {
		t.Fatalf("write conversation: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write events: %v", err)
	}
}

func sessionIDs(sessions []sessionSummary) []string {
	out := make([]string, 0, len(sessions))
	for _, s := range sessions {
		out = append(out, s.ID)
	}
	return out
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func assertSessionCapabilities(t *testing.T, got *sessionCapabilities, want sessionCapabilities) {
	t.Helper()
	if got == nil {
		t.Fatalf("capabilities = nil, want %+v", want)
	}
	if *got != want {
		t.Fatalf("capabilities = %+v, want %+v", *got, want)
	}
}
