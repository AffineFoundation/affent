package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	agent "github.com/affinefoundation/affent/internal/agent"
	"github.com/affinefoundation/affent/internal/loopstate"
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
