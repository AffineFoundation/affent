package main

import (
	"bytes"
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

	"github.com/affinefoundation/affent/internal/loopstate"
	"github.com/affinefoundation/affent/internal/sessionstate"
	"github.com/affinefoundation/affent/internal/sse"
)

func TestSessionPool_RunDueSessionSchedulesOnceFiresOneShot(t *testing.T) {
	memRoot := t.TempDir()
	pool := newPoolWithSuccessfulScheduledTurns(t, memRoot)
	createDurableSessionDir(t, pool, "due-one")
	writeLoopProtocolStatusFixture(t, pool, "due-one", "running")
	now := time.Date(2026, 5, 27, 14, 0, 0, 0, time.UTC)
	writeScheduleFixture(t, pool, "due-one", sessionSchedule{
		ID:          "sched_due",
		Kind:        sessionScheduleKindLoopTick,
		Prompt:      "Scheduled check-in for session: due-one",
		DisplayText: "Loop tick: due-one",
		Enabled:     true,
		NextRunAt:   now.Add(-time.Minute).Format(time.RFC3339),
		CreatedAt:   now.Add(-time.Hour).Format(time.RFC3339),
		UpdatedAt:   now.Add(-time.Hour).Format(time.RFC3339),
	})

	if got := pool.runDueSessionSchedulesOnce(now); got != 1 {
		t.Fatalf("runDueSessionSchedulesOnce = %d, want 1", got)
	}
	schedule := waitSchedule(t, pool, "due-one", "sched_due", func(schedule sessionSchedule) bool {
		return schedule.LastError != ""
	})
	if !schedule.Enabled {
		t.Fatalf("schedule = %+v, want failed one-shot re-enabled for retry", schedule)
	}
	if schedule.RunCount != 0 || schedule.LastTurnID == "" || !strings.Contains(schedule.LastError, sse.TurnEndMaxTurns) {
		t.Fatalf("schedule = %+v, want failed turn id and turn-end failure metadata instead of false success", schedule)
	}
	userMessage := waitScheduleUserMessage(t, pool, "due-one")
	if userMessage.Source != "schedule" || userMessage.ScheduleID != "sched_due" || userMessage.ScheduleKind != sessionScheduleKindLoopTick || userMessage.Text != "Scheduled check-in for session: due-one" || userMessage.DisplayText != "Loop tick: due-one" {
		t.Fatalf("user.message = %+v, want schedule metadata", userMessage)
	}
	summary, found, err := summarizeDurableSession(pool, "due-one")
	if err != nil {
		t.Fatalf("summarizeDurableSession: %v", err)
	}
	if !found || summary.TaskState == nil {
		t.Fatalf("task_state missing after scheduled turn: found=%v summary=%+v", found, summary)
	}
	task := summary.TaskState
	if task.RequestMode != "normal" || task.RequestSource != "schedule" || task.ScheduleID != "sched_due" || task.ScheduleKind != sessionScheduleKindLoopTick {
		t.Fatalf("task request provenance = mode:%q source:%q schedule:%q kind:%q, want scheduled loop tick", task.RequestMode, task.RequestSource, task.ScheduleID, task.ScheduleKind)
	}
	if !stringSliceContains(task.KnownFacts, "latest request source: schedule "+sessionScheduleKindLoopTick+" sched_due") {
		t.Fatalf("known_facts = %+v, want scheduled request provenance", task.KnownFacts)
	}
	if !stringSliceContains(task.Sources, "schedule") {
		t.Fatalf("sources = %+v, want schedule source", task.Sources)
	}
}

func TestSessionPool_ScheduleLoopSweepsDueSchedulesOnStartup(t *testing.T) {
	memRoot := t.TempDir()
	sessionID := "startup-due"
	now := time.Now().UTC()
	if err := os.MkdirAll(filepath.Join(memRoot, sessionID), 0o755); err != nil {
		t.Fatalf("mkdir session state: %v", err)
	}
	if err := writeSessionSchedulesFile(filepath.Join(memRoot, sessionID, sessionSchedulesFileName), sessionSchedulesFile{
		Version: 1,
		Schedules: []sessionSchedule{
			{
				ID:        "sched_startup",
				Kind:      sessionScheduleKindCustom,
				Prompt:    "Startup schedule should run immediately.",
				Enabled:   true,
				NextRunAt: now.Add(-time.Minute).Format(time.RFC3339),
				CreatedAt: now.Add(-time.Hour).Format(time.RFC3339),
				UpdatedAt: now.Add(-time.Hour).Format(time.RFC3339),
			},
		},
	}); err != nil {
		t.Fatalf("write startup schedule: %v", err)
	}
	srv := newSuccessfulScheduledTurnServer(t)
	cfg := Config{
		Listen:         "127.0.0.1:0",
		MaxSessions:    8,
		SessionIdleTTL: "5m",
		WorkspaceRoot:  t.TempDir(),
		MemoryRoot:     memRoot,
		BaseURL:        srv.URL,
		APIKey:         "test",
		Model:          "fake",
	}
	pool, err := NewSessionPool(cfg, zerologDiscard())
	if err != nil {
		t.Fatalf("NewSessionPool: %v", err)
	}
	t.Cleanup(pool.Shutdown)

	schedule := waitSchedule(t, pool, sessionID, "sched_startup", func(schedule sessionSchedule) bool {
		return schedule.RunCount == 1 && schedule.LastTurnID != ""
	})
	if schedule.Enabled || schedule.LastError != "" {
		t.Fatalf("schedule = %+v, want one-shot startup schedule completed without waiting for first ticker interval", schedule)
	}
}

func TestSessionPool_StartupScheduleRestoresActiveWorkspace(t *testing.T) {
	memRoot := t.TempDir()
	workspaceRoot := t.TempDir()
	sessionID := "startup-workspace"
	sessionDir := filepath.Join(memRoot, sessionID)
	project := filepath.Join(workspaceRoot, "app")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}
	if err := os.WriteFile(filepath.Join(project, "marker.txt"), []byte("scheduled-workspace-marker\n"), 0o644); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	if err := sessionstate.WriteMetadata(sessionDir, sessionstate.Metadata{
		SessionID:     sessionID,
		WorkspaceRoot: workspaceRoot,
		WorkspacePath: project,
	}); err != nil {
		t.Fatalf("write session metadata: %v", err)
	}
	now := time.Now().UTC()
	if err := writeSessionSchedulesFile(filepath.Join(sessionDir, sessionSchedulesFileName), sessionSchedulesFile{
		Version: 1,
		Schedules: []sessionSchedule{{
			ID:          "sched_workspace",
			Kind:        sessionScheduleKindCustom,
			Prompt:      "Read marker.txt from the active project workspace.",
			DisplayText: "Read project marker",
			Enabled:     true,
			NextRunAt:   now.Add(-time.Minute).Format(time.RFC3339),
			CreatedAt:   now.Add(-time.Hour).Format(time.RFC3339),
			UpdatedAt:   now.Add(-time.Hour).Format(time.RFC3339),
		}},
	}); err != nil {
		t.Fatalf("write startup schedule: %v", err)
	}

	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		switch calls.Add(1) {
		case 1:
			streamToolCall(t, w, "read_marker", "shell", `{"command":"pwd; cat marker.txt","timeout_sec":5}`)
		case 2:
			fmt.Fprint(w, `data: {"choices":[{"delta":{"role":"assistant","content":"Read marker from active project workspace."},"finish_reason":"stop"}]}`+"\n\n")
		default:
			t.Errorf("unexpected LLM call %d", calls.Load())
			fmt.Fprint(w, `data: {"choices":[{"delta":{"role":"assistant","content":"unexpected"},"finish_reason":"stop"}]}`+"\n\n")
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(srv.Close)
	cfg := Config{
		Listen:         "127.0.0.1:0",
		MaxSessions:    8,
		SessionIdleTTL: "5m",
		WorkspaceRoot:  workspaceRoot,
		MemoryRoot:     memRoot,
		BaseURL:        srv.URL,
		APIKey:         "test",
		Model:          "fake",
		EnableBuiltins: true,
		MaxTurnSteps:   4,
	}
	pool, err := NewSessionPool(cfg, zerologDiscard())
	if err != nil {
		t.Fatalf("NewSessionPool: %v", err)
	}
	t.Cleanup(pool.Shutdown)

	schedule := waitSchedule(t, pool, sessionID, "sched_workspace", func(schedule sessionSchedule) bool {
		return schedule.RunCount == 1 && schedule.LastTurnID != ""
	})
	if schedule.Enabled || schedule.LastError != "" {
		t.Fatalf("schedule = %+v, want one-shot active-workspace schedule completed", schedule)
	}
	sess := activeSessionByID(pool, sessionID)
	if sess == nil {
		t.Fatal("scheduled startup turn should reopen the durable session")
	}
	if sess.Workspace() != project {
		t.Fatalf("active workspace = %q, want restored project %q", sess.Workspace(), project)
	}
	userMessage := waitScheduleUserMessage(t, pool, sessionID)
	if userMessage.Source != "schedule" || userMessage.ScheduleID != "sched_workspace" || userMessage.DisplayText != "Read project marker" {
		t.Fatalf("user.message = %+v, want scheduled provenance", userMessage)
	}
	result := waitScheduleToolResult(t, pool, sessionID, "read_marker")
	if !strings.Contains(result.Result, "scheduled-workspace-marker") || !strings.Contains(result.Result, "[exit 0]") {
		t.Fatalf("scheduled shell result did not read marker from active workspace:\n%s", result.Result)
	}
}

func TestSessionPool_RunDueSessionSchedulesOnceFiresRecurringLoopTickWithoutProtocol(t *testing.T) {
	memRoot := t.TempDir()
	pool := newPoolWithSuccessfulScheduledTurns(t, memRoot)
	createDurableSessionDir(t, pool, "draft-loop")
	now := time.Date(2026, 5, 27, 14, 0, 0, 0, time.UTC)
	writeScheduleFixture(t, pool, "draft-loop", sessionSchedule{
		ID:                    "sched_loop",
		Kind:                  sessionScheduleKindLoopTick,
		Prompt:                "Scheduled loop tick for session: draft-loop",
		Enabled:               true,
		NextRunAt:             now.Add(-time.Minute).Format(time.RFC3339),
		RepeatIntervalSeconds: 1800,
		CreatedAt:             now.Add(-time.Hour).Format(time.RFC3339),
		UpdatedAt:             now.Add(-time.Hour).Format(time.RFC3339),
	})

	if got := pool.runDueSessionSchedulesOnce(now); got != 1 {
		t.Fatalf("runDueSessionSchedulesOnce = %d, want 1", got)
	}
	schedule := waitSchedule(t, pool, "draft-loop", "sched_loop", func(schedule sessionSchedule) bool {
		return schedule.RunCount == 1 && schedule.LastTurnID != ""
	})
	if !schedule.Enabled || schedule.RunCount != 1 || schedule.LastTurnID == "" || schedule.LastError != "" {
		t.Fatalf("schedule = %+v, want recurring timer fired without LOOP.md", schedule)
	}
	if got, want := schedule.NextRunAt, now.Add(29*time.Minute).Format(time.RFC3339); got != want {
		t.Fatalf("next_run_at = %q, want advanced to %q", got, want)
	}
	userMessage := waitScheduleUserMessage(t, pool, "draft-loop")
	if userMessage.Source != "schedule" || userMessage.ScheduleKind != sessionScheduleKindLoopTick {
		t.Fatalf("user.message = %+v, want scheduled loop tick turn", userMessage)
	}
}

func TestSessionPool_RunDueSessionSchedulesOnceFiresLoopTickWhenStateStillDraft(t *testing.T) {
	memRoot := t.TempDir()
	pool := newPoolWithSuccessfulScheduledTurns(t, memRoot)
	createDurableSessionDir(t, pool, "stale-draft-loop")
	writeLoopProtocolStatusFixture(t, pool, "stale-draft-loop", "running")
	if err := loopstate.WriteState(sessionLoopStatePath(pool, "stale-draft-loop"), loopstate.State{
		Version: 1,
		LoopID:  "stale-draft-loop",
		Status:  "draft",
	}); err != nil {
		t.Fatalf("write loop state: %v", err)
	}
	now := time.Date(2026, 5, 27, 14, 0, 0, 0, time.UTC)
	writeScheduleFixture(t, pool, "stale-draft-loop", sessionSchedule{
		ID:                    "sched_loop",
		Kind:                  sessionScheduleKindLoopTick,
		Prompt:                "Scheduled loop tick for stale draft state",
		Enabled:               true,
		NextRunAt:             now.Add(-time.Minute).Format(time.RFC3339),
		RepeatIntervalSeconds: 1800,
		CreatedAt:             now.Add(-time.Hour).Format(time.RFC3339),
		UpdatedAt:             now.Add(-time.Hour).Format(time.RFC3339),
	})

	if got := pool.runDueSessionSchedulesOnce(now); got != 1 {
		t.Fatalf("runDueSessionSchedulesOnce = %d, want 1", got)
	}
	schedule := waitSchedule(t, pool, "stale-draft-loop", "sched_loop", func(schedule sessionSchedule) bool {
		return schedule.RunCount == 1 && schedule.LastTurnID != ""
	})
	if schedule.RunCount != 1 || schedule.LastTurnID == "" || schedule.LastError != "" {
		t.Fatalf("schedule = %+v, want fired loop tick independent from sidecar state", schedule)
	}
}

func TestSessionPool_RunDueSessionSchedulesOnceFiresOneShotLoopTickWithoutProtocol(t *testing.T) {
	memRoot := t.TempDir()
	pool := newPoolWithSuccessfulScheduledTurns(t, memRoot)
	createDurableSessionDir(t, pool, "draft-one-shot-loop")
	now := time.Date(2026, 5, 27, 14, 0, 0, 0, time.UTC)
	writeScheduleFixture(t, pool, "draft-one-shot-loop", sessionSchedule{
		ID:        "sched_loop_once",
		Kind:      sessionScheduleKindLoopTick,
		Prompt:    "Scheduled one-shot loop tick for session: draft-one-shot-loop",
		Enabled:   true,
		NextRunAt: now.Add(-time.Minute).Format(time.RFC3339),
		CreatedAt: now.Add(-time.Hour).Format(time.RFC3339),
		UpdatedAt: now.Add(-time.Hour).Format(time.RFC3339),
	})

	if got := pool.runDueSessionSchedulesOnce(now); got != 1 {
		t.Fatalf("runDueSessionSchedulesOnce = %d, want 1", got)
	}
	schedule := waitSchedule(t, pool, "draft-one-shot-loop", "sched_loop_once", func(schedule sessionSchedule) bool {
		return schedule.RunCount == 1 && schedule.LastTurnID != ""
	})
	if schedule.Enabled || schedule.RunCount != 1 || schedule.LastTurnID == "" || schedule.LastError != "" {
		t.Fatalf("schedule = %+v, want one-shot timer fired and disabled", schedule)
	}
}

func TestSessionPool_ClaimScheduleDoesNotAdvanceBeforeTurnEnd(t *testing.T) {
	memRoot := t.TempDir()
	pool := newPoolWithMemoryRoot(t, memRoot)
	createDurableSessionDir(t, pool, "claim-recovery")
	now := time.Date(2026, 5, 27, 14, 0, 0, 0, time.UTC)
	nextRunAt := now.Add(-time.Minute).Format(time.RFC3339)
	writeScheduleFixture(t, pool, "claim-recovery", sessionSchedule{
		ID:          "sched_claim",
		Prompt:      "Run later.",
		DisplayText: "Claim recovery",
		Enabled:     true,
		NextRunAt:   nextRunAt,
		CreatedAt:   now.Add(-time.Hour).Format(time.RFC3339),
		UpdatedAt:   now.Add(-time.Hour).Format(time.RFC3339),
	})

	run, ok, err := pool.claimNextDueSessionSchedule("claim-recovery", now)
	if err != nil || !ok {
		t.Fatalf("claimNextDueSessionSchedule ok=%v err=%v", ok, err)
	}
	if run.ScheduleID != "sched_claim" || run.DisplayText != "Claim recovery" {
		t.Fatalf("run = %+v, want claimed schedule metadata", run)
	}
	file, found, err := readSessionSchedulesFile(sessionSchedulesPath(pool, "claim-recovery"))
	if err != nil || !found {
		t.Fatalf("read schedules found=%v err=%v", found, err)
	}
	claimed := file.Schedules[0]
	if !claimed.Enabled || claimed.NextRunAt != nextRunAt || claimed.LastRunAt != "" || claimed.LastTurnID != "" || claimed.RunCount != 0 {
		t.Fatalf("schedule after claim = %+v, want unchanged durable schedule until turn.end", claimed)
	}

	if err := pool.recordSessionScheduleSuccess(run, now, "turn_claim"); err != nil {
		t.Fatalf("record success: %v", err)
	}
	completed := waitSchedule(t, pool, "claim-recovery", "sched_claim", func(schedule sessionSchedule) bool {
		return schedule.LastTurnID == "turn_claim"
	})
	if completed.Enabled || completed.NextRunAt != nextRunAt || completed.RunCount != 1 || completed.LastRunAt == "" || completed.LastError != "" {
		t.Fatalf("schedule after success = %+v, want one-shot disabled only after turn.end", completed)
	}
}

func TestSummarizeSessionSchedulesCountsPendingLoopTicks(t *testing.T) {
	schedules := []sessionSchedule{
		{ID: "loop", Kind: sessionScheduleKindLoopTick, Enabled: true, NextRunAt: "2026-05-27T10:00:00Z"},
		{ID: "checkin", Kind: sessionScheduleKindCheckIn, Enabled: true, NextRunAt: "2026-05-27T11:00:00Z"},
		{ID: "paused-loop", Kind: sessionScheduleKindLoopTick, Enabled: false, NextRunAt: "2026-05-27T09:00:00Z"},
	}

	draft := summarizeSessionSchedulesWithLoopState(schedules, false)
	if draft.EnabledLoopTicks != 1 || draft.PendingLoopTicks != 0 {
		t.Fatalf("draft summary = %+v, want loop tick counted without pending calibration", draft)
	}

	running := summarizeSessionSchedulesWithLoopState(schedules, true)
	if running.EnabledLoopTicks != 1 || running.PendingLoopTicks != 0 {
		t.Fatalf("running summary = %+v, want enabled loop tick without pending", running)
	}
}

func TestCreateSessionScheduleLoopTickDoesNotInitializeProtocol(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	next := time.Date(2026, 5, 27, 14, 30, 0, 0, time.UTC).Format(time.RFC3339)
	body := `{
		"kind":"loop_tick",
		"prompt":"Scheduled loop tick for session: direct API",
		"display_text":"Loop every 30m: direct API",
		"next_run_at":` + strconv.Quote(next) + `,
		"repeat_interval_seconds":1800,
		"enabled":true
	}`
	r := httptest.NewRequest(http.MethodPost, "/v1/sessions/direct-loop/schedules", bytes.NewBufferString(body))
	w := httptest.NewRecorder()

	handleSessionSchedules(pool, "direct-loop", w, r)
	if got := w.Result().StatusCode; got != http.StatusCreated {
		t.Fatalf("create schedule status = %d, want 201; body=%s", got, w.Body.String())
	}
	var resp sessionSchedulesResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Summary == nil || resp.Summary.EnabledLoopTicks != 1 || resp.Summary.PendingLoopTicks != 0 {
		t.Fatalf("summary = %+v, want one schedule without pending protocol calibration", resp.Summary)
	}
	if _, found, err := loopstate.ReadProtocol(sessionLoopProtocolPath(pool, "direct-loop")); err != nil || found {
		t.Fatalf("read loop protocol found=%v err=%v, want no protocol created", found, err)
	}
	if _, found, err := loopstate.ReadState(sessionLoopStatePath(pool, "direct-loop")); err != nil || found {
		t.Fatalf("read loop state found=%v err=%v, want no loop state created", found, err)
	}
}

func TestSessionPool_RunDueSessionSchedulesOnceAdvancesRecurring(t *testing.T) {
	memRoot := t.TempDir()
	pool := newPoolWithSuccessfulScheduledTurns(t, memRoot)
	createDurableSessionDir(t, pool, "due-recurring")
	now := time.Date(2026, 5, 27, 14, 0, 0, 0, time.UTC)
	writeScheduleFixture(t, pool, "due-recurring", sessionSchedule{
		ID:                    "sched_daily",
		Prompt:                "Scheduled recurring check-in",
		Enabled:               true,
		NextRunAt:             now.Add(-3 * time.Hour).Format(time.RFC3339),
		RepeatIntervalSeconds: 3600,
		CreatedAt:             now.Add(-24 * time.Hour).Format(time.RFC3339),
		UpdatedAt:             now.Add(-24 * time.Hour).Format(time.RFC3339),
	})

	if got := pool.runDueSessionSchedulesOnce(now); got != 1 {
		t.Fatalf("runDueSessionSchedulesOnce = %d, want 1", got)
	}
	schedule := waitSchedule(t, pool, "due-recurring", "sched_daily", func(schedule sessionSchedule) bool {
		return schedule.RunCount == 1 && schedule.LastTurnID != ""
	})
	if !schedule.Enabled {
		t.Fatalf("schedule = %+v, want recurring schedule to stay enabled", schedule)
	}
	if got, want := schedule.NextRunAt, now.Add(time.Hour).Format(time.RFC3339); got != want {
		t.Fatalf("next_run_at = %q, want %q", got, want)
	}
	if schedule.RunCount != 1 || schedule.LastTurnID == "" {
		t.Fatalf("schedule = %+v, want successful recurring metadata", schedule)
	}
}

func TestSessionPool_RunDueSessionSchedulesOnceSkipsBusySession(t *testing.T) {
	memRoot := t.TempDir()
	pool := newPoolWithMemoryRoot(t, memRoot)
	createDurableSessionDir(t, pool, "busy")
	active, err := pool.GetOrCreate("busy")
	if err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	active.activeTurns.Store(1)
	t.Cleanup(func() { active.activeTurns.Store(0) })
	now := time.Date(2026, 5, 27, 14, 0, 0, 0, time.UTC)
	nextRunAt := now.Add(-time.Minute).Format(time.RFC3339)
	writeScheduleFixture(t, pool, "busy", sessionSchedule{
		ID:        "sched_busy",
		Prompt:    "Should wait",
		Enabled:   true,
		NextRunAt: nextRunAt,
		CreatedAt: now.Add(-time.Hour).Format(time.RFC3339),
		UpdatedAt: now.Add(-time.Hour).Format(time.RFC3339),
	})

	if got := pool.runDueSessionSchedulesOnce(now); got != 0 {
		t.Fatalf("runDueSessionSchedulesOnce = %d, want 0", got)
	}
	file, found, err := readSessionSchedulesFile(sessionSchedulesPath(pool, "busy"))
	if err != nil || !found {
		t.Fatalf("read schedules found=%v err=%v", found, err)
	}
	schedule := file.Schedules[0]
	if !schedule.Enabled || schedule.NextRunAt != nextRunAt || schedule.RunCount != 0 {
		t.Fatalf("schedule = %+v, want unchanged busy schedule", schedule)
	}
}

func writeScheduleFixture(t *testing.T, pool *SessionPool, sessionID string, schedules ...sessionSchedule) {
	t.Helper()
	if err := writeSessionSchedulesFile(filepath.Join(pool.sessionDirPath(sessionID), sessionSchedulesFileName), sessionSchedulesFile{
		Version:   1,
		Schedules: schedules,
	}); err != nil {
		t.Fatalf("write schedules: %v", err)
	}
}

func newPoolWithSuccessfulScheduledTurns(t *testing.T, memRoot string) *SessionPool {
	t.Helper()
	srv := newSuccessfulScheduledTurnServer(t)
	cfg := Config{
		Listen:         "127.0.0.1:0",
		MaxSessions:    8,
		SessionIdleTTL: "5m",
		WorkspaceRoot:  t.TempDir(),
		MemoryRoot:     memRoot,
		BaseURL:        srv.URL,
		APIKey:         "test",
		Model:          "fake",
	}
	pool, err := NewSessionPool(cfg, zerologDiscard())
	if err != nil {
		t.Fatalf("NewSessionPool: %v", err)
	}
	t.Cleanup(pool.Shutdown)
	return pool
}

func newSuccessfulScheduledTurnServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read LLM request: %v", err)
		}
		body := string(raw)
		if strings.Contains(body, "AFFENT LOOP PROTOCOL") || (strings.Contains(body, "loop_protocol") && strings.Contains(body, "close")) {
			args := `{"action":"close","status":"paused","reason":"scheduled test turn reached a clean checkpoint"}`
			fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"role\":\"assistant\",\"tool_calls\":[{\"index\":0,\"id\":\"close_loop\",\"type\":\"function\",\"function\":{\"name\":\"loop_protocol\",\"arguments\":%s}}]},\"finish_reason\":\"tool_calls\"}]}\n\n", jsonStringLiteral(args))
			fmt.Fprint(w, "data: [DONE]\n\n")
			return
		}
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"role\":\"assistant\",\"content\":\"Scheduled turn completed.\"},\"finish_reason\":\"stop\"}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(srv.Close)
	return srv
}

func waitSchedule(t *testing.T, pool *SessionPool, sessionID, scheduleID string, ready func(sessionSchedule) bool) sessionSchedule {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	var last sessionSchedule
	for time.Now().Before(deadline) {
		file, found, err := readSessionSchedulesFile(sessionSchedulesPath(pool, sessionID))
		if err != nil {
			t.Fatalf("read schedules: %v", err)
		}
		if found {
			for _, schedule := range file.Schedules {
				if schedule.ID != scheduleID {
					continue
				}
				last = schedule
				if ready == nil || ready(schedule) {
					return schedule
				}
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for schedule %s in %s; last=%+v", scheduleID, sessionID, last)
	return sessionSchedule{}
}

func writeLoopProtocolStatusFixture(t *testing.T, pool *SessionPool, sessionID string, status string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(sessionLoopProtocolPath(pool, sessionID)), 0o755); err != nil {
		t.Fatalf("mkdir loop protocol dir: %v", err)
	}
	if err := loopstate.WriteProtocol(sessionLoopProtocolPath(pool, sessionID), "# Loop Protocol\n\n## 0. Metadata\n\n- status: "+status+"\n"); err != nil {
		t.Fatalf("write loop protocol: %v", err)
	}
}

func waitScheduleUserMessage(t *testing.T, pool *SessionPool, sessionID string) sse.UserMessagePayload {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		history, err := readSessionHistory(pool.sessionDirPath(sessionID), sessionID, -1, 100)
		if err == nil {
			for _, ev := range history.Events {
				if ev.Type != sse.TypeUserMessage {
					continue
				}
				var payload sse.UserMessagePayload
				if err := json.Unmarshal(ev.Data, &payload); err != nil {
					t.Fatalf("decode user.message: %v", err)
				}
				if payload.Source == "schedule" {
					return payload
				}
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for schedule user.message in %s", sessionID)
	return sse.UserMessagePayload{}
}

func waitScheduleToolResult(t *testing.T, pool *SessionPool, sessionID, callID string) sse.ToolResultPayload {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		history, err := readSessionHistory(pool.sessionDirPath(sessionID), sessionID, -1, 100)
		if err == nil {
			for _, ev := range history.Events {
				if ev.Type != sse.TypeToolResult {
					continue
				}
				var payload sse.ToolResultPayload
				if err := json.Unmarshal(ev.Data, &payload); err != nil {
					t.Fatalf("decode tool.result: %v", err)
				}
				if payload.CallID == callID {
					return payload
				}
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for schedule tool.result %s in %s", callID, sessionID)
	return sse.ToolResultPayload{}
}
