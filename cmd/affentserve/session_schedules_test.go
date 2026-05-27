package main

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/affinefoundation/affent/internal/sse"
)

func TestSessionPool_RunDueSessionSchedulesOnceFiresOneShot(t *testing.T) {
	memRoot := t.TempDir()
	pool := newPoolWithMemoryRoot(t, memRoot)
	createDurableSessionDir(t, pool, "due-one")
	now := time.Date(2026, 5, 27, 14, 0, 0, 0, time.UTC)
	writeScheduleFixture(t, pool, "due-one", sessionSchedule{
		ID:        "sched_due",
		Kind:      sessionScheduleKindLoopTick,
		Prompt:    "Scheduled check-in for session: due-one",
		Enabled:   true,
		NextRunAt: now.Add(-time.Minute).Format(time.RFC3339),
		CreatedAt: now.Add(-time.Hour).Format(time.RFC3339),
		UpdatedAt: now.Add(-time.Hour).Format(time.RFC3339),
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
	if userMessage.Source != "schedule" || userMessage.ScheduleID != "sched_due" || userMessage.ScheduleKind != sessionScheduleKindLoopTick || userMessage.Text != "Scheduled check-in for session: due-one" {
		t.Fatalf("user.message = %+v, want schedule metadata", userMessage)
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
