package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	agent "github.com/affinefoundation/affent/internal/agent"
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
	if err := os.WriteFile(filepath.Join(pool1.sessionDirPath("restartable"), "plan.json"), []byte(`{"version":1,"steps":[{"text":"inspect","status":"completed"},{"text":"resume work","status":"in_progress"}]}`+"\n"), 0o644); err != nil {
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
	if resp.Session.PlanSummary == nil || resp.Session.PlanSummary.Label != "plan:1/2:active" || resp.Session.PlanSummary.CompletedSteps != 1 || resp.Session.PlanSummary.TotalSteps != 2 || !resp.Session.PlanSummary.Active || resp.Session.PlanSummary.CurrentStep != "resume work" || resp.Session.PlanSummary.CurrentStepIndex != 2 {
		t.Fatalf("plan summary = %+v, want active 1/2", resp.Session.PlanSummary)
	}
	if resp.Session.Capabilities != nil {
		t.Fatalf("durable-only session should not report active capabilities, got %+v", resp.Session.Capabilities)
	}
}

func TestHandleSessionList_ReportsPlanSummary(t *testing.T) {
	memRoot := t.TempDir()
	pool := newPoolWithMemoryRoot(t, memRoot)
	createDurableSessionDir(t, pool, "planned-list")
	if err := os.WriteFile(filepath.Join(pool.sessionDirPath("planned-list"), "plan.json"), []byte(`{"version":1,"steps":[{"text":"done","status":"completed"},{"text":"blocked","status":"blocked"}]}`+"\n"), 0o644); err != nil {
		t.Fatalf("write plan: %v", err)
	}

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
	if len(resp.Sessions) != 1 {
		t.Fatalf("sessions = %+v, want one session", resp.Sessions)
	}
	summary := resp.Sessions[0].PlanSummary
	if summary == nil || summary.Label != "plan:1/2:blocked" || summary.CompletedSteps != 1 || summary.TotalSteps != 2 || !summary.Blocked || summary.CurrentStep != "blocked" || summary.CurrentStepIndex != 2 {
		t.Fatalf("plan summary = %+v, want blocked 1/2", summary)
	}
}

func TestSummarizeDurableSessionReportsBadPlanSummaryWithoutFailing(t *testing.T) {
	memRoot := t.TempDir()
	pool := newPoolWithMemoryRoot(t, memRoot)
	createDurableSessionDir(t, pool, "bad-plan")
	if err := os.WriteFile(filepath.Join(pool.sessionDirPath("bad-plan"), "plan.json"), []byte(`{`), 0o644); err != nil {
		t.Fatalf("write plan: %v", err)
	}

	summary, found, err := summarizeDurableSession(pool, "bad-plan")
	if err != nil {
		t.Fatalf("summarizeDurableSession: %v", err)
	}
	if !found {
		t.Fatal("durable session should be found")
	}
	if !summary.HasPlan || summary.PlanSummary == nil || !summary.PlanSummary.Error || summary.PlanSummary.Label != "plan:error" {
		t.Fatalf("bad plan summary = %+v", summary)
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
	if resp.Summary == nil || resp.Summary.Label != "plan:0/1:active" || resp.Summary.TotalSteps != 1 || !resp.Summary.Active || resp.Summary.CurrentStep != "resume work" || resp.Summary.CurrentStepIndex != 1 {
		t.Fatalf("summary = %+v, want active 0/1", resp.Summary)
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

func TestHandleSessionPlanDelete_RemovesDurablePlanWithoutReopeningSession(t *testing.T) {
	memRoot := t.TempDir()
	pool := newPoolWithMemoryRoot(t, memRoot)
	createDurableSessionDir(t, pool, "clear-plan")
	planPath := filepath.Join(pool.sessionDirPath("clear-plan"), "plan.json")
	if err := os.WriteFile(planPath, []byte(`{"version":1,"steps":[{"text":"stale"}]}`+"\n"), 0o644); err != nil {
		t.Fatalf("write plan: %v", err)
	}

	r := httptest.NewRequest(http.MethodDelete, "/v1/sessions/clear-plan/plan", nil)
	w := httptest.NewRecorder()
	handleSessionRoutes(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", got, w.Body.String())
	}
	if activeSessionByID(pool, "clear-plan") != nil {
		t.Fatal("DELETE plan must not reopen an inactive durable session")
	}
	var resp sessionPlanDeleteResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.SessionID != "clear-plan" || !resp.Cleared {
		t.Fatalf("delete response = %+v, want cleared clear-plan", resp)
	}
	if _, err := os.Lstat(planPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("plan path after delete err = %v, want not exists", err)
	}

	r = httptest.NewRequest(http.MethodDelete, "/v1/sessions/clear-plan/plan", nil)
	w = httptest.NewRecorder()
	handleSessionRoutes(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusOK {
		t.Fatalf("second status = %d, want 200; body=%s", got, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode second: %v", err)
	}
	if resp.Cleared {
		t.Fatalf("second delete response = %+v, want cleared=false", resp)
	}
}

func TestHandleSessionPlanDelete_RejectsSymlinkPlan(t *testing.T) {
	memRoot := t.TempDir()
	pool := newPoolWithMemoryRoot(t, memRoot)
	createDurableSessionDir(t, pool, "clear-link-plan")
	outside := filepath.Join(t.TempDir(), "outside-plan.json")
	if err := os.WriteFile(outside, []byte(`{"version":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(pool.sessionDirPath("clear-link-plan"), "plan.json")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	r := httptest.NewRequest(http.MethodDelete, "/v1/sessions/clear-link-plan/plan", nil)
	w := httptest.NewRecorder()
	handleSessionRoutes(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body=%s", got, w.Body.String())
	}
	if _, err := os.Lstat(link); err != nil {
		t.Fatalf("symlink plan should remain for operator inspection: %v", err)
	}
	if _, err := os.Lstat(outside); err != nil {
		t.Fatalf("outside plan should remain: %v", err)
	}
}

func TestHandleSessionPlanRejectsSymlinkPlan(t *testing.T) {
	memRoot := t.TempDir()
	pool := newPoolWithMemoryRoot(t, memRoot)
	createDurableSessionDir(t, pool, "link-plan")
	outside := filepath.Join(t.TempDir(), "outside-plan.json")
	if err := os.WriteFile(outside, []byte(`{"version":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(pool.sessionDirPath("link-plan"), "plan.json")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	_, found, err := readSessionPlan(pool, "link-plan")
	if err == nil || found {
		t.Fatalf("readSessionPlan symlink = found:%v err:%v, want error", found, err)
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("error = %v, want symlink", err)
	}
	summary, foundSummary, err := summarizeDurableSession(pool, "link-plan")
	if err != nil {
		t.Fatalf("summarizeDurableSession: %v", err)
	}
	if !foundSummary {
		t.Fatal("durable session should still be found")
	}
	if summary.HasPlan {
		t.Fatalf("symlink plan must not set has_plan: %+v", summary)
	}
}

func TestSummarizeDurableSessionIgnoresSymlinkConversation(t *testing.T) {
	memRoot := t.TempDir()
	pool := newPoolWithMemoryRoot(t, memRoot)
	createDurableSessionDir(t, pool, "link-conversation")
	convPath := filepath.Join(pool.sessionDirPath("link-conversation"), "conversation.jsonl")
	if err := os.Remove(convPath); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside-conversation.jsonl")
	if err := os.WriteFile(outside, []byte(`{"role":"user","content":"outside"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, convPath); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	summary, found, err := summarizeDurableSession(pool, "link-conversation")
	if err != nil {
		t.Fatalf("summarizeDurableSession: %v", err)
	}
	if !found {
		t.Fatal("durable session should still be found")
	}
	if summary.HasConversation {
		t.Fatalf("symlink conversation must not set has_conversation: %+v", summary)
	}
}

func TestSummarizeDurableSessionIgnoresSymlinkSessionDir(t *testing.T) {
	memRoot := t.TempDir()
	pool := newPoolWithMemoryRoot(t, memRoot)
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "conversation.jsonl"), []byte(`{"role":"user","content":"outside"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, pool.sessionDirPath("link-dir")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	summary, found, err := summarizeDurableSession(pool, "link-dir")
	if err != nil {
		t.Fatalf("summarizeDurableSession: %v", err)
	}
	if found || summary.ID != "" {
		t.Fatalf("symlink session dir should not be durable: found=%v summary=%+v", found, summary)
	}
	if sessionKnown(pool, "link-dir") {
		t.Fatal("sessionKnown must not follow symlink session dirs")
	}
}

func TestSummarizeDurableSessionIgnoresSymlinkDurableStateMarkers(t *testing.T) {
	memRoot := t.TempDir()
	pool := newPoolWithMemoryRoot(t, memRoot)
	createDurableSessionDir(t, pool, "link-state")
	dir := pool.sessionDirPath("link-state")

	artifactDir := filepath.Join(dir, filepath.FromSlash(artifactPathPrefix))
	if err := os.MkdirAll(filepath.Dir(artifactDir), 0o755); err != nil {
		t.Fatal(err)
	}
	outsideArtifacts := t.TempDir()
	if err := os.WriteFile(filepath.Join(outsideArtifacts, "artifact.txt"), []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideArtifacts, artifactDir); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	skillDir := agent.DefaultWorkspaceSkillDir(dir)
	if err := os.MkdirAll(filepath.Dir(skillDir), 0o755); err != nil {
		t.Fatal(err)
	}
	outsideSkills := t.TempDir()
	if err := os.WriteFile(filepath.Join(outsideSkills, "skill.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideSkills, skillDir); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	topics := filepath.Join(dir, "topics")
	if err := os.RemoveAll(topics); err != nil {
		t.Fatal(err)
	}
	outsideTopics := t.TempDir()
	if err := os.WriteFile(filepath.Join(outsideTopics, "topic.md"), []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideTopics, topics); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	summary, found, err := summarizeDurableSession(pool, "link-state")
	if err != nil {
		t.Fatalf("summarizeDurableSession: %v", err)
	}
	if !found {
		t.Fatal("durable session should still be found")
	}
	if summary.HasArtifacts || summary.HasRuntimeSkills || summary.HasMemory {
		t.Fatalf("symlink state markers must not be reported: %+v", summary)
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
