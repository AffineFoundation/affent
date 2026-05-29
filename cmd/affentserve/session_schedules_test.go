package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/affinefoundation/affent/internal/loopstate"
	"github.com/affinefoundation/affent/internal/sse"
)

func TestSessionPool_RunDueSessionSchedulesOnceFiresOneShot(t *testing.T) {
	memRoot := t.TempDir()
	pool := newPoolWithMemoryRoot(t, memRoot)
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
	file, found, err := readSessionSchedulesFile(sessionSchedulesPath(pool, "due-one"))
	if err != nil || !found {
		t.Fatalf("read schedules found=%v err=%v", found, err)
	}
	if len(file.Schedules) != 1 {
		t.Fatalf("schedules = %+v, want one", file.Schedules)
	}
	schedule := file.Schedules[0]
	if schedule.Enabled {
		t.Fatalf("schedule = %+v, want one-shot disabled after firing", schedule)
	}
	if schedule.RunCount != 1 || schedule.LastTurnID == "" || schedule.LastRunAt != now.Format(time.RFC3339) || schedule.LastError != "" {
		t.Fatalf("schedule = %+v, want successful run metadata", schedule)
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

func TestSessionPool_RunDueSessionSchedulesOnceFiresRecurringLoopTickWithoutProtocol(t *testing.T) {
	memRoot := t.TempDir()
	pool := newPoolWithMemoryRoot(t, memRoot)
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
	file, found, err := readSessionSchedulesFile(sessionSchedulesPath(pool, "draft-loop"))
	if err != nil || !found {
		t.Fatalf("read schedules found=%v err=%v", found, err)
	}
	schedule := file.Schedules[0]
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
	pool := newPoolWithMemoryRoot(t, memRoot)
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
	file, found, err := readSessionSchedulesFile(sessionSchedulesPath(pool, "stale-draft-loop"))
	if err != nil || !found {
		t.Fatalf("read schedules found=%v err=%v", found, err)
	}
	schedule := file.Schedules[0]
	if schedule.RunCount != 1 || schedule.LastTurnID == "" || schedule.LastError != "" {
		t.Fatalf("schedule = %+v, want fired loop tick independent from sidecar state", schedule)
	}
}

func TestSessionPool_RunDueSessionSchedulesOnceFiresOneShotLoopTickWithoutProtocol(t *testing.T) {
	memRoot := t.TempDir()
	pool := newPoolWithMemoryRoot(t, memRoot)
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
	file, found, err := readSessionSchedulesFile(sessionSchedulesPath(pool, "draft-one-shot-loop"))
	if err != nil || !found {
		t.Fatalf("read schedules found=%v err=%v", found, err)
	}
	schedule := file.Schedules[0]
	if schedule.Enabled || schedule.RunCount != 1 || schedule.LastTurnID == "" || schedule.LastError != "" {
		t.Fatalf("schedule = %+v, want one-shot timer fired and disabled", schedule)
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
	pool := newPoolWithMemoryRoot(t, memRoot)
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
	file, found, err := readSessionSchedulesFile(sessionSchedulesPath(pool, "due-recurring"))
	if err != nil || !found {
		t.Fatalf("read schedules found=%v err=%v", found, err)
	}
	schedule := file.Schedules[0]
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
