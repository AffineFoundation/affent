package main

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/affinefoundation/affent/internal/agent"
	"github.com/affinefoundation/affent/internal/sse"
)

func TestHandleStats_EmptyPool(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	h := handleStats(pool.cfg, pool)

	r := httptest.NewRequest("GET", "/v1/stats", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if got := w.Result().StatusCode; got != 200 {
		t.Fatalf("status = %d, want 200", got)
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("content-type = %q", ct)
	}
	var resp statsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v body=%s", err, w.Body.String())
	}
	if resp.ActiveSessions != 0 {
		t.Fatalf("ActiveSessions = %d, want 0", resp.ActiveSessions)
	}
	if resp.ShuttingDown {
		t.Fatal("ShuttingDown = true, want false for fresh pool")
	}
	if len(resp.Sessions) != 0 {
		t.Fatalf("Sessions = %d entries, want 0", len(resp.Sessions))
	}
	if resp.MaxSessions != pool.cfg.MaxSessions {
		t.Fatalf("MaxSessions = %d, want %d", resp.MaxSessions, pool.cfg.MaxSessions)
	}
	if resp.WorkspaceRoot != pool.cfg.WorkspaceRoot {
		t.Fatalf("WorkspaceRoot = %q, want %q", resp.WorkspaceRoot, pool.cfg.WorkspaceRoot)
	}
	if resp.MemoryRoot != pool.cfg.MemoryRoot {
		t.Fatalf("MemoryRoot = %q, want %q", resp.MemoryRoot, pool.cfg.MemoryRoot)
	}
	if resp.SessionStateRoot != pool.sessionRootPath() {
		t.Fatalf("SessionStateRoot = %q, want %q", resp.SessionStateRoot, pool.sessionRootPath())
	}
	if resp.Boundaries.MaxTurnSteps != agent.DefaultMaxTurnSteps {
		t.Fatalf("Boundaries.MaxTurnSteps = %d, want default %d", resp.Boundaries.MaxTurnSteps, agent.DefaultMaxTurnSteps)
	}
	if resp.Boundaries.PerCallTimeout != agent.DefaultPerCallTimeout.String() {
		t.Fatalf("Boundaries.PerCallTimeout = %q, want %q", resp.Boundaries.PerCallTimeout, agent.DefaultPerCallTimeout.String())
	}
	if resp.Boundaries.ToolResultEvent != agent.DefaultRuntimeBoundaries().ToolResultEventBytes {
		t.Fatalf("Boundaries.ToolResultEvent = %d, want %d", resp.Boundaries.ToolResultEvent, agent.DefaultRuntimeBoundaries().ToolResultEventBytes)
	}
	if resp.Boundaries.MCPToolResultBytes <= 0 {
		t.Fatalf("Boundaries.MCPToolResultBytes = %d, want positive", resp.Boundaries.MCPToolResultBytes)
	}
	if resp.Boundaries.MCPHTTPJSONResponse <= 0 || resp.Boundaries.MCPHTTPSSELine <= 0 || resp.Boundaries.MCPStdioFrame <= 0 {
		t.Fatalf("MCP transport boundaries must be positive: %+v", resp.Boundaries)
	}
	if resp.Boundaries.JSONLRecordBytes <= 0 {
		t.Fatalf("Boundaries.JSONLRecordBytes = %d, want positive", resp.Boundaries.JSONLRecordBytes)
	}
	if resp.Boundaries.LoopGuardIdenticalCalls <= 0 || resp.Boundaries.LoopGuardFailureWarn <= 0 ||
		resp.Boundaries.LoopGuardFailureHalt <= 0 || resp.Boundaries.LoopGuardWebFetchWarn <= 0 ||
		resp.Boundaries.LoopGuardWebFetchHalt <= 0 || resp.Boundaries.PlanPerTurnCalls <= 0 {
		t.Fatalf("loop guard boundaries must be positive: %+v", resp.Boundaries)
	}
	if resp.Boundaries.PlanSteps <= 0 || resp.Boundaries.PlanStepTextBytes <= 0 || resp.Boundaries.PlanNoteBytes <= 0 ||
		resp.Boundaries.PlanEvidenceRefs <= 0 || resp.Boundaries.PlanEvidenceRefBytes <= 0 || resp.Boundaries.PlanStateBytes <= 0 ||
		resp.Boundaries.ActivePlanStepBytes <= 0 || resp.Boundaries.ActivePlanNoteBytes <= 0 ||
		resp.Boundaries.ActivePlanEvidenceRefs <= 0 || resp.Boundaries.ActivePlanEvidenceRefBytes <= 0 {
		t.Fatalf("plan boundaries must be positive: %+v", resp.Boundaries)
	}
	if resp.Boundaries.FocusedTaskDefaultTurns <= 0 || resp.Boundaries.FocusedTaskMaxTurns <= 0 || resp.Boundaries.FocusedTaskPerTurnCalls <= 0 ||
		resp.Boundaries.FocusedTaskTypeBytes <= 0 || resp.Boundaries.FocusedTaskObjectiveBytes <= 0 || resp.Boundaries.FocusedTaskToolResult <= 0 ||
		resp.Boundaries.FocusedTaskSummaryBytes <= 0 || resp.Boundaries.FocusedTaskFindingEvidence <= 0 ||
		resp.Boundaries.FocusedTaskFindings <= 0 || resp.Boundaries.FocusedTaskListEntries <= 0 || resp.Boundaries.FocusedTaskToolCalls <= 0 {
		t.Fatalf("focused task boundaries must be positive: %+v", resp.Boundaries)
	}
	if resp.Boundaries.SubagentDefaultTurns <= 0 || resp.Boundaries.SubagentMaxTurns <= 0 ||
		resp.Boundaries.SubagentTaskBytes <= 0 || resp.Boundaries.SubagentModeBytes <= 0 ||
		resp.Boundaries.SubagentToolResult <= 0 || resp.Boundaries.SubagentDefaultDepth <= 0 ||
		resp.Boundaries.SubagentConfiguredMaxDepth <= 0 || resp.Boundaries.SubagentHardMaxDepth <= 0 {
		t.Fatalf("subagent boundaries must be positive: %+v", resp.Boundaries)
	}
	if resp.Boundaries.SkillActionBytes <= 0 || resp.Boundaries.SkillNameBytes <= 0 ||
		resp.Boundaries.SkillDescriptionBytes <= 0 || resp.Boundaries.SkillBodyBytes <= 0 ||
		resp.Boundaries.SkillSourceBytes <= 0 || resp.Boundaries.SkillTriggers <= 0 ||
		resp.Boundaries.SkillTriggerBytes <= 0 || resp.Boundaries.SkillRequiredTools <= 0 ||
		resp.Boundaries.SkillRequiredToolBytes <= 0 || resp.Boundaries.RuntimeSkills <= 0 ||
		resp.Boundaries.RuntimeSkillDirReadBatch <= 0 || resp.Boundaries.RuntimeSkillManifestBytes <= 0 ||
		resp.Boundaries.RuntimeSkillProposalBytes <= 0 || resp.Boundaries.RuntimeSkillProposalID <= 0 {
		t.Fatalf("runtime skill boundaries must be positive: %+v", resp.Boundaries)
	}
	if resp.Boundaries.MemoryFileBytes <= 0 || resp.Boundaries.MemorySearchQuery <= 0 || resp.Boundaries.MemorySearchTerms <= 0 ||
		resp.Boundaries.MemorySearchSnippet <= 0 || resp.Boundaries.MemoryResponseEntry <= 0 {
		t.Fatalf("memory boundaries must be positive: %+v", resp.Boundaries)
	}
	if resp.ServerTime == "" {
		t.Fatal("ServerTime must be populated")
	}
}

func TestStatsBoundarySnapshotUsesConfiguredTurnLimits(t *testing.T) {
	got := statsBoundarySnapshot(Config{
		MaxTurnSteps:     7,
		PerCallTimeout:   "9s",
		SubagentMaxDepth: 3,
	})
	if got.MaxTurnSteps != 7 {
		t.Fatalf("MaxTurnSteps = %d, want 7", got.MaxTurnSteps)
	}
	if got.PerCallTimeout != "9s" {
		t.Fatalf("PerCallTimeout = %q, want 9s", got.PerCallTimeout)
	}
	if got.StreamReasoningBytes != agent.DefaultRuntimeBoundaries().StreamReasoningBytes {
		t.Fatalf("StreamReasoningBytes = %d, want %d", got.StreamReasoningBytes, agent.DefaultRuntimeBoundaries().StreamReasoningBytes)
	}
	if got.PlanStateBytes != agent.DefaultRuntimeBoundaries().PlanStateBytes {
		t.Fatalf("PlanStateBytes = %d, want %d", got.PlanStateBytes, agent.DefaultRuntimeBoundaries().PlanStateBytes)
	}
	if got.LoopGuardFailureHalt != agent.DefaultRuntimeBoundaries().LoopGuardFailureHalt {
		t.Fatalf("LoopGuardFailureHalt = %d, want %d", got.LoopGuardFailureHalt, agent.DefaultRuntimeBoundaries().LoopGuardFailureHalt)
	}
	if got.LoopGuardWebFetchWarn != agent.DefaultRuntimeBoundaries().LoopGuardWebFetchFailureWarn {
		t.Fatalf("LoopGuardWebFetchWarn = %d, want %d", got.LoopGuardWebFetchWarn, agent.DefaultRuntimeBoundaries().LoopGuardWebFetchFailureWarn)
	}
	if got.LoopGuardWebFetchHalt != agent.DefaultRuntimeBoundaries().LoopGuardWebFetchFailureHalt {
		t.Fatalf("LoopGuardWebFetchHalt = %d, want %d", got.LoopGuardWebFetchHalt, agent.DefaultRuntimeBoundaries().LoopGuardWebFetchFailureHalt)
	}
	if got.FocusedTaskToolResult != agent.DefaultRuntimeBoundaries().FocusedTaskToolResultBytes {
		t.Fatalf("FocusedTaskToolResult = %d, want %d", got.FocusedTaskToolResult, agent.DefaultRuntimeBoundaries().FocusedTaskToolResultBytes)
	}
	if got.SubagentToolResult != agent.DefaultRuntimeBoundaries().SubagentToolResultBytes {
		t.Fatalf("SubagentToolResult = %d, want %d", got.SubagentToolResult, agent.DefaultRuntimeBoundaries().SubagentToolResultBytes)
	}
	if got.SubagentConfiguredMaxDepth != 3 {
		t.Fatalf("SubagentConfiguredMaxDepth = %d, want 3", got.SubagentConfiguredMaxDepth)
	}
	if got.SkillBodyBytes != agent.DefaultRuntimeBoundaries().SkillBodyBytes {
		t.Fatalf("SkillBodyBytes = %d, want %d", got.SkillBodyBytes, agent.DefaultRuntimeBoundaries().SkillBodyBytes)
	}
}

func TestHandleStats_ReportsShuttingDown(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	pool.SignalShutdown()
	h := handleStats(pool.cfg, pool)

	r := httptest.NewRequest("GET", "/v1/stats", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if got := w.Result().StatusCode; got != 200 {
		t.Fatalf("status = %d, want 200", got)
	}
	var resp statsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v body=%s", err, w.Body.String())
	}
	if !resp.ShuttingDown {
		t.Fatalf("ShuttingDown = false, want true: %+v", resp)
	}
}

func TestHandleStats_ListsSessionsSorted(t *testing.T) {
	pool := newTestPool(t, 8, "5m")
	for _, id := range []string{"charlie", "alpha", "bravo"} {
		if _, err := pool.GetOrCreate(id); err != nil {
			t.Fatalf("GetOrCreate %s: %v", id, err)
		}
	}
	h := handleStats(pool.cfg, pool)
	r := httptest.NewRequest("GET", "/v1/stats", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	var resp statsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.ActiveSessions != 3 {
		t.Fatalf("ActiveSessions = %d, want 3", resp.ActiveSessions)
	}
	got := make([]string, len(resp.Sessions))
	for i, s := range resp.Sessions {
		got[i] = s.ID
	}
	want := []string{"alpha", "bravo", "charlie"}
	for i, w := range want {
		if got[i] != w {
			t.Fatalf("session order: got %v, want %v", got, want)
		}
	}
	for _, s := range resp.Sessions {
		if s.CreatedAt == "" || s.LastUsedAt == "" {
			t.Fatalf("session %s has empty timestamps: %+v", s.ID, s)
		}
	}
}

func TestSession_CancelTurn_IsIdempotentWithoutActiveTurn(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	s, err := pool.GetOrCreate("idle")
	if err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	// Loop has no active turn; CancelTurn must not panic and must
	// return promptly. Calling it twice in a row must be safe.
	s.CancelTurn()
	s.CancelTurn()
}

// TestSession_UsageSnapshot_AccumulatesFromEvents pins the per-session
// token counter contract. fanout observes every event flowing through
// and bumps the counters on sse.TypeUsage / sse.TypeTurnEnd. Operators
// polling /v1/stats use this to track spend per session without
// subscribing to the event stream.
//
// The test bypasses the real Loop and feeds events directly to the
// session's events channel, which fanout drains in a background
// goroutine. We poll UsageSnapshot until the counters reflect the
// planted events, with a generous deadline to absorb scheduler jitter.
func TestSession_UsageSnapshot_AccumulatesFromEvents(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	s, err := pool.GetOrCreate("usage-test")
	if err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}

	// Plant two usage events and two turn-end events. Counters should
	// sum the usage payloads and increment Turns by 2.
	for _, p := range []sse.UsagePayload{
		{TurnID: "t1", InputTokens: 100, OutputTokens: 20},
		{TurnID: "t2", InputTokens: 200, OutputTokens: 40},
	} {
		ev, err := sse.NewEvent(sse.TypeUsage, p)
		if err != nil {
			t.Fatal(err)
		}
		s.events <- ev
	}
	for _, p := range []sse.TurnEndPayload{
		{TurnID: "t1", Reason: sse.TurnEndCompleted},
		{TurnID: "t2", Reason: sse.TurnEndCompleted},
	} {
		ev, err := sse.NewEvent(sse.TypeTurnEnd, p)
		if err != nil {
			t.Fatal(err)
		}
		s.events <- ev
	}

	// fanout is async; poll briefly until counters reach the expected
	// totals.
	deadline := time.Now().Add(2 * time.Second)
	for {
		u := s.UsageSnapshot()
		if u.InputTokens == 300 && u.OutputTokens == 60 && u.Turns == 2 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("UsageSnapshot never reached expected totals: got %+v, want input=300 output=60 turns=2", u)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestSession_ToolStatsSnapshot_AccumulatesFromTurnEnd(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	s, err := pool.GetOrCreate("tool-stats-test")
	if err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}

	for _, p := range []sse.TurnEndPayload{
		{
			TurnID: "t1",
			Reason: sse.TurnEndCompleted,
			ToolStats: &sse.ToolRuntimeStats{
				ToolRequests:           2,
				ToolNameCanonicalized:  1,
				ToolArgsRepaired:       1,
				ToolRepairCalls:        1,
				ToolRepairSucceeded:    1,
				ToolRepairNotes:        3,
				ToolRepairByKind:       map[string]int{"tool_name": 1, "alias_rename": 2},
				ToolErrors:             0,
				ToolDurationMS:         15,
				LoopGuardInterventions: 1,
			},
		},
		{
			TurnID: "t2",
			Reason: sse.TurnEndMaxTurns,
			ToolStats: &sse.ToolRuntimeStats{
				ToolRequests:      1,
				ToolArgsRepaired:  1,
				ToolRepairCalls:   1,
				ToolRepairFailed:  1,
				ToolRepairNotes:   1,
				ToolRepairByKind:  map[string]int{"alias_rename": 1},
				ToolFailureByKind: map[string]int{"invalid_args": 1},
				ToolErrors:        1,
				ToolDurationMS:    7,
				ForcedNoTools:     1,
			},
		},
	} {
		ev, err := sse.NewEvent(sse.TypeTurnEnd, p)
		if err != nil {
			t.Fatal(err)
		}
		s.events <- ev
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		got := s.ToolStatsSnapshot()
		if got.ToolRequests == 3 &&
			got.ToolNameCanonicalized == 1 &&
			got.ToolArgsRepaired == 2 &&
			got.ToolRepairCalls == 2 &&
			got.ToolRepairSucceeded == 1 &&
			got.ToolRepairFailed == 1 &&
			got.ToolRepairNotes == 4 &&
			got.ToolRepairByKind["tool_name"] == 1 &&
			got.ToolRepairByKind["alias_rename"] == 3 &&
			got.ToolFailureByKind["invalid_args"] == 1 &&
			got.ToolErrors == 1 &&
			got.ToolDurationMS == 22 &&
			got.LoopGuardInterventions == 1 &&
			got.ForcedNoTools == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("ToolStatsSnapshot never reached expected totals: got %+v", got)
		}
		time.Sleep(10 * time.Millisecond)
	}

	h := handleStats(pool.cfg, pool)
	r := httptest.NewRequest("GET", "/v1/stats", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	var resp statsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode stats: %v body=%s", err, w.Body.String())
	}
	if len(resp.Sessions) != 1 {
		t.Fatalf("sessions = %d, want 1", len(resp.Sessions))
	}
	if resp.Sessions[0].Tools.ToolRepairFailed != 1 || resp.Aggregate.Tools.ToolRepairSucceeded != 1 {
		t.Fatalf("stats tool snapshots = session:%+v aggregate:%+v", resp.Sessions[0].Tools, resp.Aggregate.Tools)
	}
	if resp.Sessions[0].Tools.ToolRepairByKind["alias_rename"] != 3 || resp.Aggregate.Tools.ToolRepairByKind["tool_name"] != 1 {
		t.Fatalf("stats repair kinds = session:%+v aggregate:%+v", resp.Sessions[0].Tools.ToolRepairByKind, resp.Aggregate.Tools.ToolRepairByKind)
	}
	if resp.Sessions[0].Tools.ToolFailureByKind["invalid_args"] != 1 || resp.Aggregate.Tools.ToolFailureByKind["invalid_args"] != 1 {
		t.Fatalf("stats failure kinds = session:%+v aggregate:%+v", resp.Sessions[0].Tools.ToolFailureByKind, resp.Aggregate.Tools.ToolFailureByKind)
	}
	summary := summarizeActiveSession(s, pool.cfg)
	if summary.Tools == nil || summary.Tools.ToolRepairCalls != 2 || summary.Tools.ToolErrors != 1 {
		t.Fatalf("active session summary tools = %+v", summary.Tools)
	}
	if summary.Tools.ToolRepairByKind["alias_rename"] != 3 {
		t.Fatalf("active session summary repair kinds = %+v", summary.Tools.ToolRepairByKind)
	}
	if summary.Tools.ToolFailureByKind["invalid_args"] != 1 {
		t.Fatalf("active session summary failure kinds = %+v", summary.Tools.ToolFailureByKind)
	}
}

func TestSession_BrowserStatsSnapshot_ZeroWhenNoBrowser(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	s, err := pool.GetOrCreate("no-browser")
	if err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	stats := s.BrowserStatsSnapshot()
	if stats != (BrowserStatsSnapshot{}) {
		t.Fatalf("session without browser must yield zero stats, got %+v", stats)
	}
}
