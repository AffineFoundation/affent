package main

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/affinefoundation/affent/internal/agent"
	"github.com/affinefoundation/affent/internal/sse"
)

func TestHandleStats_EmptyPool(t *testing.T) {
	prevRevision, prevDate := buildRevision, buildDate
	buildRevision, buildDate = "stats-rev", "2026-05-25T11:00:00Z"
	t.Cleanup(func() {
		buildRevision, buildDate = prevRevision, prevDate
	})

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
	if !strings.Contains(w.Body.String(), "\"domain_relaxations\":0") {
		t.Fatalf("response JSON missing domain_relaxations field: %s", w.Body.String())
	}
	if resp.ActiveSessions != 0 {
		t.Fatalf("ActiveSessions = %d, want 0", resp.ActiveSessions)
	}
	if resp.RunningTurns != 0 {
		t.Fatalf("RunningTurns = %d, want 0", resp.RunningTurns)
	}
	if resp.ExecutorMode != "off" {
		t.Fatalf("ExecutorMode = %q, want off", resp.ExecutorMode)
	}
	if resp.EnableBrowser || resp.EnableWeb || resp.EnableWebSearch || resp.EnableMemory || resp.EnableBuiltins || resp.EnableSubagent || resp.EnableFocusedTasks {
		t.Fatalf("runtime switches should default to off: %+v", resp)
	}
	if resp.EvalMode || resp.EvalTools != "" || resp.EvalAllTools {
		t.Fatalf("eval stats should default to off: %+v", resp)
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
	if resp.Build.Revision != "stats-rev" || resp.Build.Date != "2026-05-25T11:00:00Z" {
		t.Fatalf("Build = %+v, want injected revision/date", resp.Build)
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
	if resp.Boundaries.ToolResultContextBudget != agent.DefaultRuntimeBoundaries().ToolResultContextBudgetBytes {
		t.Fatalf("Boundaries.ToolResultContextBudget = %d, want %d", resp.Boundaries.ToolResultContextBudget, agent.DefaultRuntimeBoundaries().ToolResultContextBudgetBytes)
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
	if resp.Aggregate.DomainRelaxations != 0 {
		t.Fatalf("Aggregate.DomainRelaxations = %d, want 0 for empty pool", resp.Aggregate.DomainRelaxations)
	}
	if resp.Boundaries.LoopGuardIdenticalCalls <= 0 || resp.Boundaries.LoopGuardFailureWarn <= 0 ||
		resp.Boundaries.LoopGuardFailureHalt <= 0 || resp.Boundaries.LoopGuardWebFetchWarn <= 0 ||
		resp.Boundaries.LoopGuardWebFetchHalt <= 0 || resp.Boundaries.LoopGuardBrowserFindNoMatch <= 0 ||
		resp.Boundaries.PlanPerTurnCalls <= 0 {
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
	if got.ToolResultContextBudget != agent.DefaultRuntimeBoundaries().ToolResultContextBudgetBytes {
		t.Fatalf("ToolResultContextBudget = %d, want %d", got.ToolResultContextBudget, agent.DefaultRuntimeBoundaries().ToolResultContextBudgetBytes)
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
	if got.LoopGuardBrowserFindNoMatch != agent.DefaultRuntimeBoundaries().LoopGuardBrowserFindNoMatch {
		t.Fatalf("LoopGuardBrowserFindNoMatch = %d, want %d", got.LoopGuardBrowserFindNoMatch, agent.DefaultRuntimeBoundaries().LoopGuardBrowserFindNoMatch)
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

func TestHandleStats_ReportsWebSearchBackend(t *testing.T) {
	t.Setenv("AFFENT_WEB_SEARCH_PROVIDER", "google")
	t.Setenv("GOOGLE_API_KEY", "google-key")
	t.Setenv("GOOGLE_SEARCH_ENGINE_ID", "google-cx")
	pool := newTestPool(t, 4, "5m")
	pool.cfg.EnableWeb = true
	pool.cfg.EnableWebSearch = true
	h := handleStats(pool.cfg, pool)

	r := httptest.NewRequest("GET", "/v1/stats", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	var resp statsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v body=%s", err, w.Body.String())
	}
	if resp.WebSearchBackend != "google" {
		t.Fatalf("WebSearchBackend = %q, want google", resp.WebSearchBackend)
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
	if resp.RunningTurns != 0 {
		t.Fatalf("RunningTurns = %d, want 0 for idle sessions", resp.RunningTurns)
	}
	if resp.ExecutorMode != "off" {
		t.Fatalf("ExecutorMode = %q, want off for default builtins-disabled pool", resp.ExecutorMode)
	}
	if resp.EnableBrowser || resp.EnableWeb || resp.EnableWebSearch || resp.EnableMemory || resp.EnableBuiltins || resp.EnableSubagent || resp.EnableFocusedTasks {
		t.Fatalf("runtime switches should be off in default pool: %+v", resp)
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

func TestHandleStats_ReportsRunningTurns(t *testing.T) {
	pool := newTestPool(t, 8, "5m")
	s, err := pool.GetOrCreate("running")
	if err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	s.activeTurns.Store(1)

	h := handleStats(pool.cfg, pool)
	r := httptest.NewRequest("GET", "/v1/stats", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	var resp statsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.RunningTurns != 1 {
		t.Fatalf("RunningTurns = %d, want 1", resp.RunningTurns)
	}
}

func TestHandleStats_ReportsExecutorMode(t *testing.T) {
	pool := newTestPool(t, 8, "5m")
	pool.cfg.EnableBuiltins = true
	h := handleStats(pool.cfg, pool)
	r := httptest.NewRequest("GET", "/v1/stats", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	var resp statsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.ExecutorMode != "local" {
		t.Fatalf("ExecutorMode = %q, want local", resp.ExecutorMode)
	}
}

func TestHandleStats_ReportsRuntimeSwitches(t *testing.T) {
	pool := newTestPool(t, 8, "5m")
	pool.cfg.EnableBrowser = true
	pool.cfg.EnableWeb = true
	pool.cfg.EnableWebSearch = true
	pool.cfg.EnableMemory = true
	pool.cfg.EnableBuiltins = true
	pool.cfg.EnableSubagent = true
	pool.cfg.EnableFocusedTasks = true
	pool.cfg.EvalMode = true
	pool.cfg.EvalTools = "read_file,shell"
	pool.cfg.EvalAllTools = true
	h := handleStats(pool.cfg, pool)
	r := httptest.NewRequest("GET", "/v1/stats", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	var resp statsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.EnableBrowser || !resp.EnableWeb || !resp.EnableWebSearch || !resp.EnableMemory || !resp.EnableBuiltins || !resp.EnableSubagent || !resp.EnableFocusedTasks {
		t.Fatalf("runtime switches = %+v, want all true", resp)
	}
	if !resp.EvalMode || resp.EvalTools != "read_file,shell" || !resp.EvalAllTools {
		t.Fatalf("eval switches = %+v, want eval mode/tool allowlist/all-tools visible", resp)
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

func TestSession_StatsSnapshotsSeedFromDurableEventsOnReopen(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	createDurableSessionDir(t, pool, "seeded-stats")
	dir := pool.sessionDirPath("seeded-stats")
	body := sessionEventLine(t, sse.TypeUsage, sse.UsagePayload{TurnID: "t1", InputTokens: 100, OutputTokens: 20}) +
		sessionEventLine(t, sse.TypeTurnEnd, sse.TurnEndPayload{
			TurnID: "t1",
			Reason: sse.TurnEndMaxTurns,
			ToolStats: &sse.ToolRuntimeStats{
				ToolRequests:           2,
				ToolErrors:             1,
				LoopGuardInterventions: 1,
				MemorySearchCalls:      1,
				ToolFailureByKind:      map[string]int{"no_matches": 1},
			},
		}) +
		sessionEventLine(t, sse.TypeError, sse.ErrorPayload{TurnID: "t1", Code: "llm_timeout", FailureKind: "llm_timeout", Recoverable: true}) +
		sessionEventLine(t, sse.TypeContextCompact, sse.ContextCompactPayload{
			TurnID:          "t1",
			BeforeMessages:  80,
			AfterMessages:   40,
			RemovedMessages: 40,
			Reactive:        true,
			Reason:          "context_overflow",
			SummaryPresent:  false,
		})
	if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	s, err := pool.GetOrCreate("seeded-stats")
	if err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	usage := s.UsageSnapshot()
	if usage.InputTokens != 100 || usage.OutputTokens != 20 || usage.Turns != 1 {
		t.Fatalf("seeded usage = %+v, want durable event totals", usage)
	}
	tools := s.ToolStatsSnapshot()
	if tools.ToolRequests != 2 ||
		tools.ToolErrors != 1 ||
		tools.LoopGuardInterventions != 1 ||
		tools.MemorySearchCalls != 1 ||
		tools.ToolFailureByKind["no_matches"] != 1 {
		t.Fatalf("seeded tool stats = %+v, want durable event totals", tools)
	}
	runtime := s.RuntimeStatsSnapshot()
	if runtime.TurnEndByReason[sse.TurnEndMaxTurns] != 1 ||
		runtime.RuntimeErrors != 1 ||
		runtime.RuntimeErrorByKind["llm_timeout"] != 1 ||
		runtime.ContextCompactions != 1 ||
		runtime.ContextCompactionsReactive != 1 ||
		runtime.ContextCompactionRemovedMessages != 40 ||
		runtime.ContextCompactionLatestReason != "context_overflow" ||
		runtime.ContextCompactionLatestState != "missing" {
		t.Fatalf("seeded runtime stats = %+v, want durable event totals", runtime)
	}

	h := handleStats(pool.cfg, pool)
	r := httptest.NewRequest("GET", "/v1/stats", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var resp statsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode stats: %v body=%s", err, w.Body.String())
	}
	if resp.Aggregate.InputTokens != 100 ||
		resp.Aggregate.Tools.ToolFailureByKind["no_matches"] != 1 ||
		resp.Aggregate.Runtime.RuntimeErrorByKind["llm_timeout"] != 1 {
		t.Fatalf("seeded aggregate stats = %+v, want durable event totals", resp.Aggregate)
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
				ToolRequests:               2,
				ToolNameCanonicalized:      1,
				ToolArgsRepaired:           1,
				ToolRepairCalls:            1,
				ToolRepairSucceeded:        1,
				ToolRepairNotes:            3,
				ToolRepairByKind:           map[string]int{"tool_name": 1, "alias_rename": 2},
				ToolErrors:                 0,
				ToolDurationMS:             15,
				LoopGuardInterventions:     1,
				SourceAccessResults:        2,
				SourceAccessVerified:       1,
				SourceAccessDiscoveryOnly:  1,
				SourceAccessDynamicPartial: 1,
				MemoryUpdates:              2,
				MemoryUpdateAdd:            1,
				MemoryUpdateReplace:        1,
				MemorySearchCalls:          2,
				MemorySearchMisses:         1,
				SessionSearchCalls:         1,
				SessionSearchResults:       2,
				SessionSearchContextHits:   1,
				SessionSearchMatchedTerms:  2,
				SessionSearchRecent:        1,
				ToolContextTruncated:       2,
				ToolContextOmittedBytes:    2048,
			},
		},
		{
			TurnID: "t2",
			Reason: sse.TurnEndMaxTurns,
			ToolStats: &sse.ToolRuntimeStats{
				ToolRequests:               1,
				ToolArgsRepaired:           1,
				ToolRepairCalls:            1,
				ToolRepairFailed:           1,
				ToolRepairNotes:            1,
				ToolRepairByKind:           map[string]int{"alias_rename": 1},
				ToolFailureByKind:          map[string]int{"invalid_args": 1},
				ToolErrors:                 1,
				ToolDurationMS:             7,
				ForcedNoTools:              1,
				SourceAccessResults:        1,
				SourceAccessVerified:       1,
				SourceAccessNetwork:        1,
				SourceAccessDynamicPartial: 1,
				MemoryUpdates:              1,
				MemoryUpdateRemove:         1,
				MemorySearchCalls:          3,
				MemorySearchMisses:         2,
				SessionSearchCalls:         1,
				SessionSearchResults:       1,
				SessionSearchContextHits:   1,
				SessionSearchMatchedTerms:  1,
				SessionSearchRecent:        2,
				ToolContextTruncated:       1,
				ToolContextOmittedBytes:    512,
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
			got.ForcedNoTools == 1 &&
			got.SourceAccessResults == 3 &&
			got.SourceAccessVerified == 2 &&
			got.SourceAccessDiscovery == 1 &&
			got.SourceAccessNetwork == 1 &&
			got.SourceAccessDynamic == 2 &&
			got.MemoryUpdates == 3 &&
			got.MemoryUpdateAdd == 1 &&
			got.MemoryUpdateReplace == 1 &&
			got.MemoryUpdateRemove == 1 &&
			got.MemorySearchCalls == 5 &&
			got.MemorySearchMisses == 3 &&
			got.SessionSearchCalls == 2 &&
			got.SessionSearchResults == 3 &&
			got.SessionSearchContext == 2 &&
			got.SessionSearchTerms == 3 &&
			got.SessionSearchRecent == 3 &&
			got.ToolContextTruncated == 3 &&
			got.ToolContextOmitted == 2560 {
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
	if resp.Sessions[0].Tools.ToolRepairFailed != 1 || resp.Aggregate.Tools.ToolRepairSucceeded != 1 ||
		resp.Sessions[0].Tools.SourceAccessVerified != 2 || resp.Aggregate.Tools.SourceAccessNetwork != 1 ||
		resp.Aggregate.Tools.SourceAccessDynamic != 2 ||
		resp.Sessions[0].Tools.MemoryUpdates != 3 || resp.Aggregate.Tools.MemoryUpdateRemove != 1 ||
		resp.Aggregate.Tools.MemorySearchCalls != 5 ||
		resp.Aggregate.Tools.MemorySearchMisses != 3 ||
		resp.Sessions[0].Tools.SessionSearchCalls != 2 || resp.Aggregate.Tools.SessionSearchResults != 3 ||
		resp.Aggregate.Tools.SessionSearchContext != 2 || resp.Sessions[0].Tools.SessionSearchTerms != 3 ||
		resp.Aggregate.Tools.SessionSearchRecent != 3 ||
		resp.Sessions[0].Tools.ToolContextTruncated != 3 || resp.Aggregate.Tools.ToolContextOmitted != 2560 {
		t.Fatalf("stats tool snapshots = session:%+v aggregate:%+v", resp.Sessions[0].Tools, resp.Aggregate.Tools)
	}
	if resp.Sessions[0].Tools.ToolRepairByKind["alias_rename"] != 3 || resp.Aggregate.Tools.ToolRepairByKind["tool_name"] != 1 {
		t.Fatalf("stats repair kinds = session:%+v aggregate:%+v", resp.Sessions[0].Tools.ToolRepairByKind, resp.Aggregate.Tools.ToolRepairByKind)
	}
	if resp.Sessions[0].Tools.ToolFailureByKind["invalid_args"] != 1 || resp.Aggregate.Tools.ToolFailureByKind["invalid_args"] != 1 {
		t.Fatalf("stats failure kinds = session:%+v aggregate:%+v", resp.Sessions[0].Tools.ToolFailureByKind, resp.Aggregate.Tools.ToolFailureByKind)
	}
	summary := summarizeActiveSession(s, pool.cfg)
	if summary.Tools == nil || summary.Tools.ToolRepairCalls != 2 || summary.Tools.ToolErrors != 1 ||
		summary.Tools.SourceAccessResults != 3 || summary.Tools.SourceAccessDiscovery != 1 ||
		summary.Tools.SourceAccessDynamic != 2 ||
		summary.Tools.MemoryUpdates != 3 || summary.Tools.MemoryUpdateAdd != 1 ||
		summary.Tools.MemorySearchCalls != 5 ||
		summary.Tools.MemorySearchMisses != 3 ||
		summary.Tools.SessionSearchCalls != 2 || summary.Tools.SessionSearchResults != 3 ||
		summary.Tools.SessionSearchContext != 2 || summary.Tools.SessionSearchTerms != 3 ||
		summary.Tools.SessionSearchRecent != 3 ||
		summary.Tools.ToolContextTruncated != 3 || summary.Tools.ToolContextOmitted != 2560 {
		t.Fatalf("active session summary tools = %+v", summary.Tools)
	}
	if summary.Tools.ToolRepairByKind["alias_rename"] != 3 {
		t.Fatalf("active session summary repair kinds = %+v", summary.Tools.ToolRepairByKind)
	}
	if summary.Tools.ToolFailureByKind["invalid_args"] != 1 {
		t.Fatalf("active session summary failure kinds = %+v", summary.Tools.ToolFailureByKind)
	}
}

func TestSession_ToolStatsSnapshot_CountsNoEvidenceWebResults(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	s, err := pool.GetOrCreate("no-evidence-web")
	if err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}

	ev, err := sse.NewEvent(sse.TypeTurnEnd, sse.TurnEndPayload{
		TurnID: "t1",
		Reason: sse.TurnEndCompleted,
		ToolStats: &sse.ToolRuntimeStats{
			ToolRequests:      1,
			ToolFailureByKind: map[string]int{"dynamic_shell": 1},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	s.events <- ev

	deadline := time.Now().Add(2 * time.Second)
	for {
		got := s.ToolStatsSnapshot()
		if got.ToolFailureByKind["dynamic_shell"] == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("ToolStatsSnapshot never counted dynamic_shell: %+v", got)
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
	if resp.Aggregate.Tools.ToolFailureByKind["dynamic_shell"] != 1 {
		t.Fatalf("aggregate failure kinds = %+v, want dynamic_shell=1", resp.Aggregate.Tools.ToolFailureByKind)
	}
}

func TestSession_RuntimeStatsSnapshot_AccumulatesTurnReasonsAndErrors(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	s, err := pool.GetOrCreate("runtime-stats-test")
	if err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}

	events := []sse.Event{}
	for _, p := range []sse.TurnEndPayload{
		{TurnID: "t1", Reason: sse.TurnEndMaxTurns},
		{TurnID: "t2", Reason: sse.TurnEndError},
		{TurnID: "t3", Reason: sse.TurnEndMaxTurns},
	} {
		ev, err := sse.NewEvent(sse.TypeTurnEnd, p)
		if err != nil {
			t.Fatal(err)
		}
		events = append(events, ev)
	}
	for _, p := range []sse.ErrorPayload{
		{TurnID: "t2", Code: "upstream_error", Message: "context deadline exceeded", FailureKind: "llm_timeout"},
		{TurnID: "t2", Code: "upstream_error", Message: "stream ended without finish", FailureKind: "llm_incomplete_stream"},
		{TurnID: "t3", Code: "runtime_error", Message: "unclassified"},
	} {
		ev, err := sse.NewEvent(sse.TypeError, p)
		if err != nil {
			t.Fatal(err)
		}
		events = append(events, ev)
	}
	for _, p := range []sse.ContextCompactPayload{
		{TurnID: "t2", BeforeMessages: 60, AfterMessages: 18, RemovedMessages: 42, Reactive: true, Reason: "context_overflow", SummaryPresent: true, SummaryBytes: 2048},
		{TurnID: "t3", BeforeMessages: 48, AfterMessages: 20, RemovedMessages: 28, Reactive: false, Reason: "proactive_threshold", SummaryPresent: true, SummaryBytes: 1024},
		{TurnID: "t4", BeforeMessages: 44, AfterMessages: 18, RemovedMessages: 26, Reactive: true, Reason: "context_overflow", SummaryPresent: false},
	} {
		ev, err := sse.NewEvent(sse.TypeContextCompact, p)
		if err != nil {
			t.Fatal(err)
		}
		events = append(events, ev)
	}
	for _, ev := range events {
		s.events <- ev
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		got := s.RuntimeStatsSnapshot()
		if got.TurnEndByReason[sse.TurnEndMaxTurns] == 2 &&
			got.TurnEndByReason[sse.TurnEndError] == 1 &&
			got.RuntimeErrors == 3 &&
			got.RuntimeErrorByKind["llm_timeout"] == 1 &&
			got.RuntimeErrorByKind["llm_incomplete_stream"] == 1 &&
			got.ContextCompactions == 3 &&
			got.ContextCompactionsReactive == 2 &&
			got.ContextCompactionRemovedMessages == 96 &&
			got.ContextCompactionSummaryBytes == 3072 &&
			got.ContextCompactionSummaryMissing == 1 &&
			got.ContextCompactionLatestReason == "context_overflow" &&
			got.ContextCompactionLatestReactive &&
			got.ContextCompactionLatestState == "missing" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("RuntimeStatsSnapshot never reached expected totals: got %+v", got)
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
	if resp.Sessions[0].Runtime.TurnEndByReason[sse.TurnEndMaxTurns] != 2 ||
		resp.Aggregate.Runtime.TurnEndByReason[sse.TurnEndError] != 1 ||
		resp.Sessions[0].Runtime.RuntimeErrors != 3 ||
		resp.Aggregate.Runtime.RuntimeErrorByKind["llm_timeout"] != 1 ||
		resp.Aggregate.Runtime.RuntimeErrorByKind["llm_incomplete_stream"] != 1 ||
		resp.Sessions[0].Runtime.ContextCompactions != 3 ||
		resp.Sessions[0].Runtime.ContextCompactionLatestReason != "context_overflow" ||
		!resp.Sessions[0].Runtime.ContextCompactionLatestReactive ||
		resp.Sessions[0].Runtime.ContextCompactionLatestState != "missing" ||
		resp.Aggregate.Runtime.ContextCompactionRemovedMessages != 96 ||
		resp.Aggregate.Runtime.ContextCompactionSummaryMissing != 1 {
		t.Fatalf("runtime stats = session:%+v aggregate:%+v", resp.Sessions[0].Runtime, resp.Aggregate.Runtime)
	}
	summary := summarizeActiveSession(s, pool.cfg)
	if summary.Runtime == nil ||
		summary.Runtime.TurnEndByReason[sse.TurnEndMaxTurns] != 2 ||
		summary.Runtime.RuntimeErrorByKind["llm_timeout"] != 1 ||
		summary.Runtime.ContextCompactions != 3 {
		t.Fatalf("active session runtime summary = %+v", summary.Runtime)
	}
	if summary.ContextCompactions == nil ||
		summary.ContextCompactions.Count != 3 ||
		summary.ContextCompactions.Reactive != 2 ||
		summary.ContextCompactions.RemovedMessages != 96 ||
		summary.ContextCompactions.SummaryBytes != 3072 ||
		summary.ContextCompactions.SummaryMissing != 1 ||
		summary.ContextCompactions.LatestReason != "context_overflow" ||
		!summary.ContextCompactions.LatestReactive ||
		summary.ContextCompactions.LatestSummaryState != "missing" {
		t.Fatalf("active session context compactions = %+v", summary.ContextCompactions)
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
