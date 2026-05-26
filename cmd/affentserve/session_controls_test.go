package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/affinefoundation/affent/internal/agent"
)

func TestHandleSessionMessage_StartsTurn(t *testing.T) {
	pool := newTestPool(t, 4, "5m")

	r := httptest.NewRequest(http.MethodPost, "/v1/sessions/message-active/messages", strings.NewReader(`{"content":"hello"}`))
	w := httptest.NewRecorder()
	handleSessionRoutes(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%s", got, w.Body.String())
	}
	var resp sessionMessageResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v body=%s", err, w.Body.String())
	}
	if resp.SessionID != "message-active" || !strings.HasPrefix(resp.TurnID, "turn_") {
		t.Fatalf("response = %+v, want session id + turn id", resp)
	}
}

func TestHandleSessionMessage_ReopensDurableSession(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	createDurableSessionDir(t, pool, "message-durable")

	r := httptest.NewRequest(http.MethodPost, "/v1/sessions/message-durable/messages", strings.NewReader(`{"content":"resume this"}`))
	w := httptest.NewRecorder()
	handleSessionRoutes(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%s", got, w.Body.String())
	}
	if activeSessionByID(pool, "message-durable") == nil {
		t.Fatal("POST messages should reopen durable session")
	}
}

func TestHandleSessionMessage_RejectsInvalidBody(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	cases := []struct {
		name  string
		body  string
		wants []string
	}{
		{"empty content", `{"content":"   "}`, []string{"content is required"}},
		{"bad mode", `{"content":"hello","mode":"fast"}`, []string{"mode must be one of", "plan_only", "execute_plan"}},
		{"unknown field", `{"content":"hello","role":"user"}`, []string{"unknown field", "role"}},
		{"multiple objects", `{"content":"hello"} {"content":"again"}`, []string{"single JSON object"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/v1/sessions/message-bad/messages", strings.NewReader(c.body))
			w := httptest.NewRecorder()
			handleSessionRoutes(pool).ServeHTTP(w, r)
			if got := w.Result().StatusCode; got != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", got, w.Body.String())
			}
			for _, want := range c.wants {
				if !strings.Contains(w.Body.String(), want) {
					t.Fatalf("body %q does not contain %q", w.Body.String(), want)
				}
			}
		})
	}
}

func TestHandleSessionMessage_PlanOnlyStartsConstrainedTurn(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	pool.cfg.EnableBuiltins = true

	r := httptest.NewRequest(http.MethodPost, "/v1/sessions/plan-only/messages", strings.NewReader(`{"mode":"plan_only","content":"draft the migration"}`))
	w := httptest.NewRecorder()
	handleSessionRoutes(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%s", got, w.Body.String())
	}
	s := activeSessionByID(pool, "plan-only")
	if s == nil {
		t.Fatal("plan-only message should create an active session")
	}
	msgs := s.conv.Snapshot()
	if len(msgs) == 0 || !strings.Contains(msgs[len(msgs)-1].Content, "Plan-only mode is enabled.") || !strings.Contains(msgs[len(msgs)-1].Content, "draft the migration") {
		t.Fatalf("last conversation message should be wrapped plan-only prompt, got %+v", msgs)
	}
	opts, err := sessionMessageTurnOptions(s, sessionMessageModePlanOnly)
	if err != nil {
		t.Fatal(err)
	}
	defs := opts.Tools.Defs()
	if len(defs) != 1 || defs[0].Function.Name != agent.PlanToolName {
		t.Fatalf("plan-only tool defs = %+v, want only plan", defs)
	}
	if opts.FirstToolPolicy == nil || opts.FirstToolPolicy.ToolName != agent.PlanToolName || opts.MaxToolCalls != sessionPlanOnlyMaxToolCalls || !opts.FinalNoToolsOnMaxTurns {
		t.Fatalf("plan-only options = %+v, want plan policy + tight budget", opts)
	}
}

func TestHandleSessionMessage_PlanOnlyRequiresPlanToolWithoutCreatingSession(t *testing.T) {
	pool := newTestPool(t, 4, "5m")

	r := httptest.NewRequest(http.MethodPost, "/v1/sessions/no-plan-tool/messages", strings.NewReader(`{"mode":"plan_only","content":"draft plan"}`))
	w := httptest.NewRecorder()
	handleSessionRoutes(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", got, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "mode_unavailable") || !strings.Contains(w.Body.String(), "plan tool is not available") {
		t.Fatalf("body should explain missing plan tool: %s", w.Body.String())
	}
	if activeSessionByID(pool, "no-plan-tool") != nil {
		t.Fatal("plan-only rejection must not create a session")
	}
}

func TestHandleSessionMessage_ExecutePlanRequiresPlanToolBeforeReadingPlan(t *testing.T) {
	pool := newTestPool(t, 4, "5m")

	r := httptest.NewRequest(http.MethodPost, "/v1/sessions/no-plan-tool/messages", strings.NewReader(`{"mode":"execute_plan"}`))
	w := httptest.NewRecorder()
	handleSessionRoutes(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", got, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "mode_unavailable") || !strings.Contains(w.Body.String(), "plan tool is not available") {
		t.Fatalf("body should explain missing plan tool: %s", w.Body.String())
	}
	if strings.Contains(w.Body.String(), "no persisted plan") {
		t.Fatalf("mode-unavailable response should not inspect plan state first: %s", w.Body.String())
	}
	if activeSessionByID(pool, "no-plan-tool") != nil {
		t.Fatal("execute-plan rejection must not create a session")
	}
}

func TestHandleSessionMessage_ExecutePlanRequiresExistingRunnablePlan(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	pool.cfg.EnableBuiltins = true

	r := httptest.NewRequest(http.MethodPost, "/v1/sessions/missing-plan/messages", strings.NewReader(`{"mode":"execute_plan"}`))
	w := httptest.NewRecorder()
	handleSessionRoutes(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", got, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "no persisted plan") {
		t.Fatalf("body should explain missing plan: %s", w.Body.String())
	}
	if activeSessionByID(pool, "missing-plan") != nil {
		t.Fatal("execute-plan rejection must not create a session")
	}
}

func TestHandleSessionMessage_ExecutePlanStartsConfirmedPlanTurn(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	pool.cfg.EnableBuiltins = true
	createDurableSessionDir(t, pool, "execute-plan")
	if err := os.WriteFile(filepath.Join(pool.sessionDirPath("execute-plan"), "plan.json"), []byte(`{"version":1,"steps":[{"text":"implement backend plan mode","status":"in_progress"}]}`+"\n"), 0o644); err != nil {
		t.Fatalf("write plan: %v", err)
	}

	r := httptest.NewRequest(http.MethodPost, "/v1/sessions/execute-plan/messages", strings.NewReader(`{"mode":"execute_plan"}`))
	w := httptest.NewRecorder()
	handleSessionRoutes(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%s", got, w.Body.String())
	}
	s := activeSessionByID(pool, "execute-plan")
	if s == nil {
		t.Fatal("execute-plan should reopen the durable session")
	}
	msgs := s.conv.Snapshot()
	if len(msgs) == 0 || !strings.Contains(msgs[len(msgs)-1].Content, "Execute-plan mode is enabled.") || !strings.Contains(msgs[len(msgs)-1].Content, "plan:0/1:active") {
		t.Fatalf("last conversation message should be execute-plan prompt, got %+v", msgs)
	}
	if !strings.Contains(msgs[len(msgs)-1].Content, "Execute only the current unfinished step first") ||
		!strings.Contains(msgs[len(msgs)-1].Content, "call plan with action=update for that same step") {
		t.Fatalf("execute-plan prompt should enforce step execution/update discipline, got %q", msgs[len(msgs)-1].Content)
	}
}

func TestHandleSessionMessage_BusySessionReturnsConflict(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		select {
		case <-block:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() {
		close(block)
		srv.Close()
	})

	pool := newTestPool(t, 4, "5m")
	pool.cfg.BaseURL = srv.URL

	r := httptest.NewRequest(http.MethodPost, "/v1/sessions/message-busy/messages", strings.NewReader(`{"content":"first"}`))
	w := httptest.NewRecorder()
	handleSessionRoutes(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusAccepted {
		t.Fatalf("first status = %d, want 202; body=%s", got, w.Body.String())
	}

	r = httptest.NewRequest(http.MethodPost, "/v1/sessions/message-busy/messages", strings.NewReader(`{"content":"second"}`))
	w = httptest.NewRecorder()
	handleSessionRoutes(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusConflict {
		t.Fatalf("second status = %d, want 409; body=%s", got, w.Body.String())
	}
	if got := w.Result().Header.Get("Retry-After"); got != "1" {
		t.Fatalf("Retry-After = %q, want 1", got)
	}
}

func TestHandleSessionCancel_AcceptsActiveSession(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	if _, err := pool.GetOrCreate("cancel-active"); err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}

	r := httptest.NewRequest(http.MethodPost, "/v1/sessions/cancel-active/cancel", nil)
	w := httptest.NewRecorder()
	handleSessionRoutes(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%s", got, w.Body.String())
	}
	var resp sessionCancelResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v body=%s", err, w.Body.String())
	}
	if resp.SessionID != "cancel-active" || !resp.Accepted {
		t.Fatalf("response = %+v, want accepted cancel-active", resp)
	}
}

func TestHandleSessionCancel_InactiveDurableSessionReturnsConflict(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	createDurableSessionDir(t, pool, "cancel-durable")

	r := httptest.NewRequest(http.MethodPost, "/v1/sessions/cancel-durable/cancel", nil)
	w := httptest.NewRecorder()
	handleSessionRoutes(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", got, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "session_inactive") {
		t.Fatalf("body should explain inactive session: %s", w.Body.String())
	}
	if activeSessionByID(pool, "cancel-durable") != nil {
		t.Fatal("POST cancel must not reopen an inactive durable session")
	}
}

func TestHandleSessionCancel_RejectsUnsafeID(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	r := httptest.NewRequest(http.MethodPost, "/v1/sessions/../cancel", nil)
	w := httptest.NewRecorder()
	handleSessionCancel(pool, "..", w, r)
	if got := w.Result().StatusCode; got != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", got, w.Body.String())
	}
}
