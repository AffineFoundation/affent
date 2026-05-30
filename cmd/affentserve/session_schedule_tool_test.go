package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	agent "github.com/affinefoundation/affent/internal/agent"
	"github.com/affinefoundation/affent/internal/loopstate"
	"github.com/affinefoundation/affent/internal/sse"
	"github.com/rs/zerolog"
)

func TestSessionScheduleToolCreatesRecurringTimerWithoutLoopProtocol(t *testing.T) {
	pool := newPoolWithMemoryRoot(t, t.TempDir())
	reg := agent.NewRegistry()
	registerSessionScheduleTool(reg, pool, "timer-tool")
	tool, ok := reg.Get(sessionScheduleToolName)
	if !ok {
		t.Fatal("session_schedule tool not registered")
	}
	next := time.Date(2026, 5, 29, 6, 30, 0, 0, time.UTC).Format(time.RFC3339)
	out, err := tool.Execute(context.Background(), json.RawMessage(`{
		"action":"create",
		"kind":"custom",
		"prompt":"Query BTC price through the public API and report the 24h change.",
		"display_text":"BTC price check",
		"next_run_at":`+strconv.Quote(next)+`,
		"repeat_interval_seconds":1800
	}`))
	if err != nil {
		t.Fatalf("session_schedule create: %v", err)
	}
	if !strings.Contains(out, `"repeat_interval_seconds": 1800`) || !strings.Contains(out, `"display_text": "BTC price check"`) {
		t.Fatalf("tool output missing schedule fields:\n%s", out)
	}
	file, found, err := readSessionSchedulesFile(sessionSchedulesPath(pool, "timer-tool"))
	if err != nil || !found {
		t.Fatalf("read schedules found=%v err=%v", found, err)
	}
	if len(file.Schedules) != 1 || file.Schedules[0].Kind != sessionScheduleKindCustom || file.Schedules[0].RepeatIntervalSeconds != 1800 {
		t.Fatalf("schedules = %+v, want one custom recurring schedule", file.Schedules)
	}
	if _, found, err := loopstate.ReadProtocol(sessionLoopProtocolPath(pool, "timer-tool")); err != nil || found {
		t.Fatalf("loop protocol found=%v err=%v, want no LOOP.md", found, err)
	}
	if _, err := os.Stat(filepath.Join(pool.sessionDirPath("timer-tool"), "schedules.json")); err != nil {
		t.Fatalf("schedules file not persisted: %v", err)
	}
}

func TestSessionScheduleToolLoopTickRequiresRunningProtocol(t *testing.T) {
	pool := newPoolWithMemoryRoot(t, t.TempDir())
	reg := agent.NewRegistry()
	registerSessionScheduleTool(reg, pool, "loop-tick-tool")
	tool, ok := reg.Get(sessionScheduleToolName)
	if !ok {
		t.Fatal("session_schedule tool not registered")
	}
	next := time.Date(2026, 5, 29, 6, 30, 0, 0, time.UTC).Format(time.RFC3339)
	_, err := tool.Execute(context.Background(), json.RawMessage(`{
		"action":"create",
		"kind":"loop_tick",
		"prompt":"Nudge the active loop.",
		"display_text":"Loop tick",
		"next_run_at":`+strconv.Quote(next)+`,
		"repeat_interval_seconds":1800
	}`))
	if err == nil ||
		!strings.Contains(err.Error(), "running LOOP.md") ||
		!strings.Contains(err.Error(), "Next:") ||
		!strings.Contains(err.Error(), "Failure: kind="+sessionScheduleLoopTickUnavailableFailureKind) {
		t.Fatalf("session_schedule loop_tick error = %v, want structured running LOOP.md guidance", err)
	}
	if _, found, readErr := readSessionSchedulesFile(sessionSchedulesPath(pool, "loop-tick-tool")); readErr != nil || found {
		t.Fatalf("schedules found=%v err=%v, want no schedule persisted", found, readErr)
	}
}

func TestSessionScheduleToolResumeLoopTickRequiresRunningProtocol(t *testing.T) {
	pool := newPoolWithMemoryRoot(t, t.TempDir())
	reg := agent.NewRegistry()
	registerSessionScheduleTool(reg, pool, "loop-tick-resume")
	tool, ok := reg.Get(sessionScheduleToolName)
	if !ok {
		t.Fatal("session_schedule tool not registered")
	}
	now := time.Date(2026, 5, 29, 6, 30, 0, 0, time.UTC)
	createDurableSessionDir(t, pool, "loop-tick-resume")
	writeScheduleFixture(t, pool, "loop-tick-resume", sessionSchedule{
		ID:        "sched_loop",
		Kind:      sessionScheduleKindLoopTick,
		Prompt:    "Nudge the active loop.",
		Enabled:   false,
		NextRunAt: now.Add(time.Hour).Format(time.RFC3339),
		CreatedAt: now.Format(time.RFC3339),
		UpdatedAt: now.Format(time.RFC3339),
	})
	_, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"update","schedule_id":"sched_loop","enabled":true}`))
	if err == nil ||
		!strings.Contains(err.Error(), "running LOOP.md") ||
		!strings.Contains(err.Error(), "Next:") ||
		!strings.Contains(err.Error(), "Failure: kind="+sessionScheduleLoopTickUnavailableFailureKind) {
		t.Fatalf("session_schedule loop_tick resume err = %v, want structured running LOOP.md guidance", err)
	}
	file, found, readErr := readSessionSchedulesFile(sessionSchedulesPath(pool, "loop-tick-resume"))
	if readErr != nil || !found || len(file.Schedules) != 1 || file.Schedules[0].Enabled {
		t.Fatalf("schedule after rejected resume found=%v err=%v schedules=%+v, want disabled", found, readErr, file.Schedules)
	}

	writeLoopProtocolStatusFixture(t, pool, "loop-tick-resume", "running")
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"update","schedule_id":"sched_loop","enabled":true}`))
	if err != nil {
		t.Fatalf("session_schedule loop_tick resume with running protocol: %v", err)
	}
	if !strings.Contains(out, `"enabled": true`) || !strings.Contains(out, `"kind": "loop_tick"`) {
		t.Fatalf("resume output missing enabled loop_tick:\n%s", out)
	}
	file, found, readErr = readSessionSchedulesFile(sessionSchedulesPath(pool, "loop-tick-resume"))
	if readErr != nil || !found || len(file.Schedules) != 1 || file.Schedules[0].LastError != "" || file.Schedules[0].LastErrorKind != "" {
		t.Fatalf("schedule after accepted resume found=%v err=%v schedules=%+v, want error state cleared", found, readErr, file.Schedules)
	}
}

func TestSessionScheduleToolCreatedTimerIsVisibleThroughSessionAPI(t *testing.T) {
	pool := newPoolWithMemoryRoot(t, t.TempDir())
	reg := agent.NewRegistry()
	registerSessionScheduleTool(reg, pool, "timer-api")
	tool, ok := reg.Get(sessionScheduleToolName)
	if !ok {
		t.Fatal("session_schedule tool not registered")
	}
	next := time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC).Format(time.RFC3339)
	if _, err := tool.Execute(context.Background(), json.RawMessage(`{
		"action":"create",
		"kind":"custom",
		"prompt":"Fetch the current BTC price and summarize the source.",
		"display_text":"BTC price check",
		"next_run_at":`+strconv.Quote(next)+`,
		"repeat_interval_seconds":1800
	}`)); err != nil {
		t.Fatalf("session_schedule create: %v", err)
	}
	if activeSessionByID(pool, "timer-api") != nil {
		t.Fatal("session_schedule tool must not reopen the session just to persist a timer")
	}

	r := httptest.NewRequest(http.MethodGet, "/v1/sessions/timer-api/schedules", nil)
	w := httptest.NewRecorder()
	handleSessionRoutes(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusOK {
		t.Fatalf("list schedules status = %d, want 200; body=%s", got, w.Body.String())
	}
	var listed sessionSchedulesResponse
	if err := json.Unmarshal(w.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode list schedules: %v", err)
	}
	if listed.SessionID != "timer-api" || len(listed.Schedules) != 1 {
		t.Fatalf("listed schedules = %+v, want one persisted timer", listed)
	}
	if listed.Summary == nil || listed.Summary.Enabled != 1 || listed.Summary.NextRunAt != next || listed.Summary.NextPromptPreview != "BTC price check" {
		t.Fatalf("schedule summary = %+v, want visible enabled timer", listed.Summary)
	}

	r = httptest.NewRequest(http.MethodGet, "/v1/sessions/timer-api", nil)
	w = httptest.NewRecorder()
	handleSessionRoutes(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusOK {
		t.Fatalf("session detail status = %d, want 200; body=%s", got, w.Body.String())
	}
	var detail sessionDetailResponse
	if err := json.Unmarshal(w.Body.Bytes(), &detail); err != nil {
		t.Fatalf("decode session detail: %v", err)
	}
	if !detail.Session.HasSchedules || detail.Session.Schedules == nil || detail.Session.Schedules.NextRunAt != next {
		t.Fatalf("session detail = %+v, want schedule summary from durable session state", detail.Session)
	}

	info, err := os.Stat(sessionSchedulesPath(pool, "timer-api"))
	if err != nil {
		t.Fatalf("stat schedules file: %v", err)
	}
	if got := info.Mode().Perm(); got != sessionSchedulesFileMode {
		t.Fatalf("schedules file mode = %v, want %v", got, os.FileMode(sessionSchedulesFileMode))
	}
}

func TestSessionScheduleToolIsRegisteredForServeSessions(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	session, err := pool.GetOrCreate("schedule-tool-surface")
	if err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	if _, ok := session.registry.Get(sessionScheduleToolName); !ok {
		t.Fatal("session registry missing session_schedule tool")
	}
}

func TestSessionChatRecurringTimerUsesScheduleToolWithoutLoopProtocol(t *testing.T) {
	nextRunAt := time.Now().UTC().Add(time.Hour).Truncate(time.Second).Format(time.RFC3339)
	createArgs := fmt.Sprintf(`{
		"action":"create",
		"kind":"custom",
		"prompt":"Fetch current BTC USD price, compare with the previous recorded price, append the result to btc_price_log.csv, and report the delta.",
		"display_text":"BTC price every 30m",
		"next_run_at":%s,
		"repeat_interval_seconds":1800
	}`, jsonStringLiteral(nextRunAt))
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		switch calls.Add(1) {
		case 1:
			assertLLMRequestToolBefore(t, r, sessionScheduleToolName, "shell")
			fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"role\":\"assistant\",\"tool_calls\":[{\"index\":0,\"id\":\"schedule_btc\",\"type\":\"function\",\"function\":{\"name\":\"session_schedule\",\"arguments\":%s}}]},\"finish_reason\":\"tool_calls\"}]}\n\n", jsonStringLiteral(createArgs))
		case 2:
			fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"role\":\"assistant\",\"content\":\"Scheduled BTC price checks every 30 minutes.\"},\"finish_reason\":\"stop\"}]}\n\n")
		default:
			t.Errorf("unexpected LLM call %d", calls.Load())
			fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"role\":\"assistant\",\"content\":\"unexpected\"},\"finish_reason\":\"stop\"}]}\n\n")
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(srv.Close)

	cfg := Config{
		Listen:             "127.0.0.1:0",
		MaxSessions:        4,
		SessionIdleTTL:     "5m",
		WorkspaceRoot:      t.TempDir(),
		MemoryRoot:         t.TempDir(),
		BaseURL:            srv.URL,
		APIKey:             "test",
		Model:              "fake",
		EnableLoopProtocol: true,
	}
	pool, err := NewSessionPool(cfg, zerolog.New(io.Discard))
	if err != nil {
		t.Fatalf("NewSessionPool: %v", err)
	}
	t.Cleanup(pool.Shutdown)
	session, err := pool.GetOrCreate("btc-timer-chat")
	if err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	subID, ch := session.Subscribe(32)
	defer session.Unsubscribe(subID)
	turnID, err := session.SendUser(context.Background(), "Every 30 minutes, query BTC price and update a file.")
	if err != nil {
		t.Fatalf("SendUser: %v", err)
	}

	deadline := time.After(10 * time.Second)
	sawScheduleInSurface := false
	sawScheduleTool := false
	sawLoopTool := false
	var finalText string
	for {
		select {
		case ev := <-ch:
			switch ev.Type {
			case sse.TypeRuntimeSurface:
				var p sse.RuntimeSurfacePayload
				if err := json.Unmarshal(ev.Data, &p); err != nil {
					t.Fatalf("decode runtime.surface: %v", err)
				}
				for _, tool := range p.Tools {
					if tool.Name == sessionScheduleToolName {
						sawScheduleInSurface = true
					}
				}
				if !p.Capabilities.SessionSchedule {
					t.Fatalf("runtime surface capabilities = %+v, want session_schedule", p.Capabilities)
				}
			case sse.TypeToolRequest:
				var p sse.ToolRequestPayload
				if err := json.Unmarshal(ev.Data, &p); err != nil {
					t.Fatalf("decode tool.request: %v", err)
				}
				switch p.Tool {
				case sessionScheduleToolName:
					sawScheduleTool = true
				case agent.LoopProtocolToolName:
					sawLoopTool = true
				}
			case sse.TypeMessageDone:
				var p sse.MessageDonePayload
				if err := json.Unmarshal(ev.Data, &p); err != nil {
					t.Fatalf("decode message.done: %v", err)
				}
				finalText = p.Text
			case sse.TypeTurnEnd:
				var p sse.TurnEndPayload
				if err := json.Unmarshal(ev.Data, &p); err != nil {
					t.Fatalf("decode turn.end: %v", err)
				}
				if p.TurnID != turnID {
					continue
				}
				if p.Reason != sse.TurnEndCompleted {
					t.Fatalf("turn end reason = %q, want completed", p.Reason)
				}
				if !sawScheduleInSurface {
					t.Fatal("runtime surface did not expose session_schedule")
				}
				if !sawScheduleTool {
					t.Fatal("turn ended without session_schedule tool call")
				}
				if sawLoopTool {
					t.Fatal("recurring timer setup used loop_protocol")
				}
				if !strings.Contains(finalText, "Scheduled BTC") {
					t.Fatalf("final text = %q, want scheduled confirmation", finalText)
				}
				assertBTCTimerScheduleWithoutLoopProtocol(t, pool, "btc-timer-chat", nextRunAt)
				if got := calls.Load(); got != 2 {
					t.Fatalf("LLM calls = %d, want 2", got)
				}
				return
			}
		case <-deadline:
			t.Fatal("timeout waiting for BTC timer turn.end")
		}
	}
}

func assertLLMRequestToolBefore(t *testing.T, r *http.Request, earlier, later string) {
	t.Helper()
	var body struct {
		Tools []struct {
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		} `json:"tools"`
	}
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("read LLM request body: %v", err)
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode LLM request body: %v body=%s", err, string(raw))
	}
	earlierIdx, laterIdx := -1, -1
	for idx, tool := range body.Tools {
		switch tool.Function.Name {
		case earlier:
			earlierIdx = idx
		case later:
			laterIdx = idx
		}
	}
	if earlierIdx < 0 {
		t.Fatalf("LLM request tools missing %s", earlier)
	}
	if laterIdx >= 0 && earlierIdx > laterIdx {
		t.Fatalf("LLM request tool order %s=%d %s=%d, want %s before %s", earlier, earlierIdx, later, laterIdx, earlier, later)
	}
}

func TestSessionChatRecurringTimerRejectsLoopSetupAndUsesSchedule(t *testing.T) {
	nextRunAt := time.Now().UTC().Add(time.Hour).Truncate(time.Second).Format(time.RFC3339)
	loopArgs := `{"action":"start_setup","goal":"Every 30 minutes query BTC price and update a file."}`
	createArgs := fmt.Sprintf(`{
		"action":"create",
		"kind":"custom",
		"prompt":"Fetch current BTC USD price, compare with the previous recorded price, append the result to btc_price_log.csv, and report the delta.",
		"display_text":"BTC price every 30m",
		"next_run_at":%s,
		"repeat_interval_seconds":1800
	}`, jsonStringLiteral(nextRunAt))
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		switch calls.Add(1) {
		case 1:
			fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"role\":\"assistant\",\"tool_calls\":[{\"index\":0,\"id\":\"bad_loop\",\"type\":\"function\",\"function\":{\"name\":\"loop_protocol\",\"arguments\":%s}}]},\"finish_reason\":\"tool_calls\"}]}\n\n", jsonStringLiteral(loopArgs))
		case 2:
			fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"role\":\"assistant\",\"tool_calls\":[{\"index\":0,\"id\":\"schedule_btc\",\"type\":\"function\",\"function\":{\"name\":\"session_schedule\",\"arguments\":%s}}]},\"finish_reason\":\"tool_calls\"}]}\n\n", jsonStringLiteral(createArgs))
		case 3:
			fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"role\":\"assistant\",\"content\":\"Scheduled BTC price checks every 30 minutes.\"},\"finish_reason\":\"stop\"}]}\n\n")
		default:
			t.Errorf("unexpected LLM call %d", calls.Load())
			fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"role\":\"assistant\",\"content\":\"unexpected\"},\"finish_reason\":\"stop\"}]}\n\n")
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(srv.Close)

	cfg := Config{
		Listen:             "127.0.0.1:0",
		MaxSessions:        4,
		SessionIdleTTL:     "5m",
		WorkspaceRoot:      t.TempDir(),
		MemoryRoot:         t.TempDir(),
		BaseURL:            srv.URL,
		APIKey:             "test",
		Model:              "fake",
		EnableLoopProtocol: true,
	}
	pool, err := NewSessionPool(cfg, zerolog.New(io.Discard))
	if err != nil {
		t.Fatalf("NewSessionPool: %v", err)
	}
	t.Cleanup(pool.Shutdown)
	session, err := pool.GetOrCreate("btc-timer-loop-rejected")
	if err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	subID, ch := session.Subscribe(32)
	defer session.Unsubscribe(subID)
	turnID, err := session.SendUser(context.Background(), "Every 30 minutes, query BTC price and update a file.")
	if err != nil {
		t.Fatalf("SendUser: %v", err)
	}

	deadline := time.After(10 * time.Second)
	sawLoopPolicyReject := false
	sawScheduleTool := false
	for {
		select {
		case ev := <-ch:
			switch ev.Type {
			case sse.TypeToolResult:
				var p sse.ToolResultPayload
				if err := json.Unmarshal(ev.Data, &p); err != nil {
					t.Fatalf("decode tool.result: %v", err)
				}
				if p.CallID == "bad_loop" &&
					p.ExitCode != 0 &&
					strings.Contains(p.Result, "Failure: kind=tool_policy_rejected") &&
					strings.Contains(p.Result, "session_schedule") {
					sawLoopPolicyReject = true
				}
			case sse.TypeToolRequest:
				var p sse.ToolRequestPayload
				if err := json.Unmarshal(ev.Data, &p); err != nil {
					t.Fatalf("decode tool.request: %v", err)
				}
				if p.Tool == sessionScheduleToolName {
					sawScheduleTool = true
				}
			case sse.TypeTurnEnd:
				var p sse.TurnEndPayload
				if err := json.Unmarshal(ev.Data, &p); err != nil {
					t.Fatalf("decode turn.end: %v", err)
				}
				if p.TurnID != turnID {
					continue
				}
				if !sawLoopPolicyReject {
					t.Fatal("turn ended without rejecting normal-mode loop setup")
				}
				if !sawScheduleTool {
					t.Fatal("turn ended without session_schedule recovery")
				}
				assertBTCTimerScheduleWithoutLoopProtocol(t, pool, "btc-timer-loop-rejected", nextRunAt)
				if got := calls.Load(); got != 3 {
					t.Fatalf("LLM calls = %d, want loop rejection, schedule, final", got)
				}
				return
			}
		case <-deadline:
			t.Fatal("timeout waiting for BTC timer loop rejection turn.end")
		}
	}
}

func assertBTCTimerScheduleWithoutLoopProtocol(t *testing.T, pool *SessionPool, sessionID, nextRunAt string) {
	t.Helper()
	file, found, err := readSessionSchedulesFile(sessionSchedulesPath(pool, sessionID))
	if err != nil || !found {
		t.Fatalf("read schedules found=%v err=%v", found, err)
	}
	if len(file.Schedules) != 1 {
		t.Fatalf("schedules = %+v, want one", file.Schedules)
	}
	schedule := file.Schedules[0]
	if schedule.Kind != sessionScheduleKindCustom ||
		schedule.DisplayText != "BTC price every 30m" ||
		schedule.NextRunAt != nextRunAt ||
		schedule.RepeatIntervalSeconds != 1800 ||
		!schedule.Enabled ||
		!strings.Contains(schedule.Prompt, "BTC USD price") ||
		!strings.Contains(schedule.Prompt, "btc_price_log.csv") {
		t.Fatalf("schedule = %+v, want recurring BTC file update timer", schedule)
	}
	if _, found, err := loopstate.ReadProtocol(sessionLoopProtocolPath(pool, sessionID)); err != nil || found {
		t.Fatalf("loop protocol found=%v err=%v, want no LOOP.md for a timer", found, err)
	}
	if _, found, err := loopstate.ReadState(sessionLoopStatePath(pool, sessionID)); err != nil || found {
		t.Fatalf("loop state found=%v err=%v, want no loop state for a timer", found, err)
	}
}
