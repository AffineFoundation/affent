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
	"github.com/affinefoundation/affent/internal/loopstate"
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
		{"display too large", `{"content":"hello","display_text":"` + strings.Repeat("x", maxSessionMessageDisplay+1) + `"}`, []string{"display_text too large"}},
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

func TestHandleSessionMessage_PublishesDisplayTextForGeneratedPrompts(t *testing.T) {
	pool := newTestPool(t, 4, "5m")

	r := httptest.NewRequest(http.MethodPost, "/v1/sessions/display-text/messages", strings.NewReader(`{"content":"internal loop setup prompt with detailed tool instructions","display_text":"Set up loop: market monitor"}`))
	w := httptest.NewRecorder()
	handleSessionRoutes(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%s", got, w.Body.String())
	}
	waitForFileSubstring(t, filepath.Join(pool.sessionDirPath("display-text"), "events.jsonl"), `"display_text":"Set up loop: market monitor"`)

	h := httptest.NewRequest(http.MethodGet, "/v1/sessions/display-text/history", nil)
	hw := httptest.NewRecorder()
	handleSessionRoutes(pool).ServeHTTP(hw, h)
	if got := hw.Result().StatusCode; got != http.StatusOK {
		t.Fatalf("history status = %d, want 200; body=%s", got, hw.Body.String())
	}
	var history sessionHistoryResponse
	if err := json.Unmarshal(hw.Body.Bytes(), &history); err != nil {
		t.Fatalf("decode history: %v", err)
	}
	for _, ev := range history.Events {
		if ev.Type != "user.message" {
			continue
		}
		var payload struct {
			Text        string `json:"text"`
			DisplayText string `json:"display_text"`
			Mode        string `json:"mode"`
		}
		if err := json.Unmarshal(ev.Data, &payload); err != nil {
			t.Fatalf("decode user payload: %v", err)
		}
		if payload.Text != "internal loop setup prompt with detailed tool instructions" || payload.DisplayText != "Set up loop: market monitor" {
			t.Fatalf("user payload = %+v", payload)
		}
		if payload.Mode != sessionMessageModeNormal {
			t.Fatalf("user payload mode = %q, want %q", payload.Mode, sessionMessageModeNormal)
		}
		return
	}
	t.Fatalf("history missing user.message: %+v", history.Events)
}

func TestHandleSessionMessage_LoopSetupMarkerCreatesDraftBeforeTurn(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(`data: {"choices":[{"delta":{"role":"assistant","content":"What should pause this loop?"},"finish_reason":"stop"}]}` + "\n\n"))
	}))
	defer srv.Close()
	pool := newTestPool(t, 4, "5m")
	pool.cfg.BaseURL = srv.URL
	pool.cfg.EnableLoopProtocol = true

	body := `{"content":"Start a long-running loop for this goal: 持续分析最近的世界形势"}`
	r := httptest.NewRequest(http.MethodPost, "/v1/sessions/loop-marker/messages", strings.NewReader(body))
	w := httptest.NewRecorder()
	handleSessionRoutes(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%s", got, w.Body.String())
	}
	protocol, found, err := loopstate.ReadProtocol(sessionLoopProtocolPath(pool, "loop-marker"))
	if err != nil || !found {
		t.Fatalf("ReadProtocol found=%v err=%v", found, err)
	}
	if loopstate.ProtocolStatus(protocol) != "draft" ||
		!strings.Contains(protocol, "持续分析最近的世界形势") {
		t.Fatalf("loop setup protocol:\n%s", protocol)
	}
	waitForFileSubstring(t, filepath.Join(pool.sessionDirPath("loop-marker"), "events.jsonl"), `"display_text":"Set up loop: 持续分析最近的世界形势"`)
	s := activeSessionByID(pool, "loop-marker")
	if s == nil {
		t.Fatal("loop setup should create active session")
	}
	messages := s.conv.Snapshot()
	sawSetupPrompt := false
	for _, msg := range messages {
		if msg.Role != "user" {
			continue
		}
		if strings.Contains(msg.Content, "Loop protocol activation is pending, not active yet.") &&
			!strings.Contains(msg.Content, sessionLoopSetupMarker) {
			sawSetupPrompt = true
		}
	}
	if !sawSetupPrompt {
		t.Fatalf("conversation should include backend loop setup prompt, got %+v", messages)
	}
}

func TestHandleSessionMessage_LoopSetupModeUsesContentAsGoal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(`data: {"choices":[{"delta":{"role":"assistant","content":"Which implementation language should I use?"},"finish_reason":"stop"}]}` + "\n\n"))
	}))
	defer srv.Close()
	pool := newTestPool(t, 4, "5m")
	pool.cfg.BaseURL = srv.URL
	pool.cfg.EnableLoopProtocol = true

	r := httptest.NewRequest(http.MethodPost, "/v1/sessions/loop-mode/messages", strings.NewReader(`{"mode":"loop_setup","content":"market monitor","display_text":"Set up loop: market monitor"}`))
	w := httptest.NewRecorder()
	handleSessionRoutes(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%s", got, w.Body.String())
	}
	protocol, found, err := loopstate.ReadProtocol(sessionLoopProtocolPath(pool, "loop-mode"))
	if err != nil || !found {
		t.Fatalf("ReadProtocol found=%v err=%v", found, err)
	}
	if loopstate.ProtocolStatus(protocol) != "draft" || !strings.Contains(protocol, "market monitor") {
		t.Fatalf("loop setup mode protocol:\n%s", protocol)
	}
	s := activeSessionByID(pool, "loop-mode")
	if s == nil {
		t.Fatal("loop setup should create active session")
	}
	messages := s.conv.Snapshot()
	sawSetupPrompt := false
	for _, msg := range messages {
		if msg.Role != "user" {
			continue
		}
		if strings.Contains(msg.Content, "Loop protocol activation is pending, not active yet.") &&
			strings.Contains(msg.Content, "Set up loop for: market monitor") &&
			strings.Contains(msg.Content, "Do not use update_draft to write status: running") {
			sawSetupPrompt = true
		}
		if msg.Content == "market monitor" {
			t.Fatalf("conversation should contain backend loop setup prompt, not raw WebUI goal: %+v", messages)
		}
	}
	if !sawSetupPrompt {
		t.Fatalf("conversation should include backend loop setup prompt, got %+v", messages)
	}
	waitForFileSubstring(t, filepath.Join(pool.sessionDirPath("loop-mode"), "events.jsonl"), `"display_text":"Set up loop: market monitor"`)
	waitForFileSubstring(t, filepath.Join(pool.sessionDirPath("loop-mode"), "events.jsonl"), `"mode":"loop_setup"`)
	waitForFileSubstring(t, filepath.Join(pool.sessionDirPath("loop-mode"), "events.jsonl"), `"type":"loop.protocol_calibration_request"`)
	waitForFileSubstring(t, filepath.Join(pool.sessionDirPath("loop-mode"), "events.jsonl"), `Which implementation language should I use?`)
	waitForFileSubstring(t, filepath.Join(pool.sessionDirPath("loop-mode"), "events.jsonl"), `"type":"turn.end"`)
}

func TestHandleSessionMessage_LoopSetupMarkerRequiresLoopProtocol(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	pool.cfg.EnableLoopProtocol = false

	r := httptest.NewRequest(http.MethodPost, "/v1/sessions/no-loop-mode/messages", strings.NewReader(`{"content":"Start a long-running loop for this goal: market monitor"}`))
	w := httptest.NewRecorder()
	handleSessionRoutes(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", got, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "mode_unavailable") || !strings.Contains(w.Body.String(), "loop protocol is not available") {
		t.Fatalf("body should explain missing loop protocol: %s", w.Body.String())
	}
	if activeSessionByID(pool, "no-loop-mode") != nil {
		t.Fatal("loop setup rejection must not create a session")
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
	opts, err := sessionMessageTurnOptions(s, sessionMessageModePlanOnly, 0)
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
	opts, err := sessionMessageTurnOptions(s, sessionMessageModeExecutePlan, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(opts.ToolCallPolicies) != 1 {
		t.Fatalf("execute-plan tool policies = %+v, want plan policy", opts.ToolCallPolicies)
	}
	if got, reject := opts.ToolCallPolicies[0].Reject(agent.ToolCallPolicyContext{
		ToolName: agent.PlanToolName,
		Args:     json.RawMessage(`{"action":"update","index":2,"status":"completed"}`),
	}); !reject || !strings.Contains(got, "update only the current active step 1") {
		t.Fatalf("execute-plan policy should reject wrong step update, reject=%v got=%q", reject, got)
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
