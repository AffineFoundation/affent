package main

import (
	"encoding/json"
	"net/http"
	"sort"
	"time"

	"github.com/affinefoundation/affent/internal/agent"
	"github.com/affinefoundation/affent/internal/jsonl"
	"github.com/affinefoundation/affent/internal/mcp"
	"github.com/affinefoundation/affent/internal/memory"
)

// statsResponse summarizes server + per-session activity at one
// snapshot in time. Useful for operators running large benchmark
// passes who want a quick "is the browser cache actually helping?"
// signal without standing up Prometheus.
type statsResponse struct {
	Listen             string                    `json:"listen"`
	Model              string                    `json:"model"`
	Build              buildInfo                 `json:"build"`
	MaxSessions        int                       `json:"max_sessions"`
	ActiveSessions     int                       `json:"active_sessions"`
	RunningTurns       int                       `json:"running_turns"`
	ExecutorMode       string                    `json:"executor_mode"`
	EnableBrowser      bool                      `json:"enable_browser"`
	EnableWeb          bool                      `json:"enable_web"`
	EnableWebSearch    bool                      `json:"enable_web_search"`
	EnableMemory       bool                      `json:"enable_memory"`
	SharedUserMemory   bool                      `json:"shared_user_memory"`
	EnableBuiltins     bool                      `json:"enable_builtins"`
	EnableSubagent     bool                      `json:"enable_subagent"`
	EnableFocusedTasks bool                      `json:"enable_focused_tasks"`
	EnableLoopProtocol bool                      `json:"enable_loop_protocol"`
	EvalMode           bool                      `json:"eval_mode"`
	EvalTools          string                    `json:"eval_tools,omitempty"`
	EvalAllTools       bool                      `json:"eval_all_tools,omitempty"`
	ShuttingDown       bool                      `json:"shutting_down"`
	WorkspaceRoot      string                    `json:"workspace_root,omitempty"`
	MemoryRoot         string                    `json:"memory_root,omitempty"`
	SessionStateRoot   string                    `json:"session_state_root"`
	BrowserCacheDir    string                    `json:"browser_cache_dir,omitempty"`
	WebSearchBackend   string                    `json:"web_search_backend,omitempty"`
	ScheduleRunner     scheduleRunnerStats       `json:"schedule_runner"`
	ServerTime         string                    `json:"server_time"`
	Sessions           []sessionStatsResponse    `json:"sessions"`
	Aggregate          aggregateStats            `json:"aggregate"`
	Boundaries         statsBoundaries           `json:"boundaries"`
	RuntimeContract    runtimeCapabilityContract `json:"runtime_contract"`
}

type scheduleRunnerStats struct {
	Enabled                bool   `json:"enabled"`
	Active                 bool   `json:"active"`
	FrontendIndependent    bool   `json:"frontend_independent"`
	SweepInterval          string `json:"sweep_interval,omitempty"`
	DurableSessionStateDir string `json:"durable_session_state_dir,omitempty"`
	DisabledReason         string `json:"disabled_reason,omitempty"`
}

type statsBoundaries struct {
	MaxTurnSteps                int    `json:"max_turn_steps"`
	MaxTurnInputTokens          int    `json:"max_turn_input_tokens"`
	PerCallTimeout              string `json:"per_call_timeout"`
	LLMRequestBodyBytes         int    `json:"llm_request_body_bytes"`
	LLMErrorBodyBytes           int    `json:"llm_error_body_bytes"`
	StreamContentBytes          int    `json:"stream_content_bytes"`
	StreamReasoningBytes        int    `json:"stream_reasoning_bytes"`
	StreamToolArgBytes          int    `json:"stream_tool_arg_bytes"`
	StreamToolCalls             int    `json:"stream_tool_calls"`
	StreamScannerBytes          int    `json:"stream_scanner_bytes"`
	ToolRequestArgsEvent        int    `json:"tool_request_args_event_bytes"`
	ToolRequestArgString        int    `json:"tool_request_arg_string_bytes"`
	ToolResultContext           int    `json:"tool_result_context_bytes"`
	ToolResultContextBudget     int    `json:"tool_result_context_budget_bytes"`
	ToolResultEvent             int    `json:"tool_result_event_bytes"`
	ToolResultPreview           int    `json:"tool_result_preview_bytes"`
	RepairableToolArg           int    `json:"repairable_tool_arg_bytes"`
	ProjectContextBytes         int    `json:"project_context_bytes"`
	LoopGuardIdenticalCalls     int    `json:"loop_guard_identical_calls"`
	LoopGuardFailureWarn        int    `json:"loop_guard_failure_warn"`
	LoopGuardFailureHalt        int    `json:"loop_guard_failure_halt"`
	LoopGuardWebFetchWarn       int    `json:"loop_guard_web_fetch_failure_warn"`
	LoopGuardWebFetchHalt       int    `json:"loop_guard_web_fetch_failure_halt"`
	LoopGuardBrowserFindNoMatch int    `json:"loop_guard_browser_find_no_match"`
	PlanPerTurnCalls            int    `json:"plan_per_turn_calls"`
	PlanSteps                   int    `json:"plan_steps"`
	PlanStepTextBytes           int    `json:"plan_step_text_bytes"`
	PlanNoteBytes               int    `json:"plan_note_bytes"`
	PlanEvidenceRefs            int    `json:"plan_evidence_refs"`
	PlanEvidenceRefBytes        int    `json:"plan_evidence_ref_bytes"`
	PlanStateBytes              int    `json:"plan_state_bytes"`
	ActivePlanStepBytes         int    `json:"active_plan_step_bytes"`
	ActivePlanNoteBytes         int    `json:"active_plan_note_bytes"`
	ActivePlanEvidenceRefs      int    `json:"active_plan_evidence_refs"`
	ActivePlanEvidenceRefBytes  int    `json:"active_plan_evidence_ref_bytes"`
	FocusedTaskDefaultTurns     int    `json:"focused_task_default_turns"`
	FocusedTaskMaxTurns         int    `json:"focused_task_max_turns"`
	FocusedTaskPerTurnCalls     int    `json:"focused_task_per_turn_calls"`
	FocusedTaskTypeBytes        int    `json:"focused_task_type_bytes"`
	FocusedTaskObjectiveBytes   int    `json:"focused_task_objective_bytes"`
	FocusedTaskToolResult       int    `json:"focused_task_tool_result_bytes"`
	FocusedTaskSummaryBytes     int    `json:"focused_task_summary_bytes"`
	FocusedTaskFindingEvidence  int    `json:"focused_task_finding_evidence_bytes"`
	FocusedTaskFindings         int    `json:"focused_task_findings"`
	FocusedTaskListEntries      int    `json:"focused_task_list_entries"`
	FocusedTaskToolCalls        int    `json:"focused_task_tool_calls"`
	SubagentDefaultTurns        int    `json:"subagent_default_turns"`
	SubagentMaxTurns            int    `json:"subagent_max_turns"`
	SubagentTaskBytes           int    `json:"subagent_task_bytes"`
	SubagentModeBytes           int    `json:"subagent_mode_bytes"`
	SubagentToolResult          int    `json:"subagent_tool_result_bytes"`
	SubagentDefaultDepth        int    `json:"subagent_default_depth"`
	SubagentConfiguredMaxDepth  int    `json:"subagent_configured_max_depth"`
	SubagentHardMaxDepth        int    `json:"subagent_hard_max_depth"`
	SkillActionBytes            int    `json:"skill_action_bytes"`
	SkillNameBytes              int    `json:"skill_name_bytes"`
	SkillDescriptionBytes       int    `json:"skill_description_bytes"`
	SkillBodyBytes              int    `json:"skill_body_bytes"`
	SkillSourceBytes            int    `json:"skill_source_bytes"`
	SkillTriggers               int    `json:"skill_triggers"`
	SkillTriggerBytes           int    `json:"skill_trigger_bytes"`
	SkillRequiredTools          int    `json:"skill_required_tools"`
	SkillRequiredToolBytes      int    `json:"skill_required_tool_bytes"`
	RuntimeSkills               int    `json:"runtime_skills"`
	RuntimeSkillDirReadBatch    int    `json:"runtime_skill_dir_read_batch"`
	RuntimeSkillManifestBytes   int    `json:"runtime_skill_manifest_bytes"`
	RuntimeSkillProposalBytes   int    `json:"runtime_skill_proposal_bytes"`
	RuntimeSkillProposalID      int    `json:"runtime_skill_proposal_id_bytes"`
	MCPToolResultBytes          int    `json:"mcp_tool_result_bytes"`
	MCPHTTPJSONResponse         int    `json:"mcp_http_json_response_bytes"`
	MCPHTTPSSELine              int    `json:"mcp_http_sse_line_bytes"`
	MCPStdioFrame               int    `json:"mcp_stdio_frame_bytes"`
	JSONLRecordBytes            int    `json:"jsonl_record_bytes"`
	MemoryFileBytes             int    `json:"memory_file_bytes"`
	MemorySearchQuery           int    `json:"memory_search_query_bytes"`
	MemorySearchTerms           int    `json:"memory_search_terms"`
	MemorySearchSnippet         int    `json:"memory_search_snippet_chars"`
	MemoryResponseEntry         int    `json:"memory_response_entry_chars"`
}

type sessionStatsResponse struct {
	ID              string                    `json:"id"`
	CreatedAt       string                    `json:"created_at"`
	LastUsedAt      string                    `json:"last_used_at"`
	Usage           UsageSnapshot             `json:"usage"`
	Tools           ToolStatsSnapshot         `json:"tools"`
	Runtime         RuntimeStatsSnapshot      `json:"runtime"`
	Browser         BrowserStatsSnapshot      `json:"browser"`
	RuntimeContract runtimeCapabilityContract `json:"runtime_contract"`
}

type aggregateStats struct {
	BlockedByType     int64                `json:"blocked_by_type"`
	BlockedByDomain   int64                `json:"blocked_by_domain"`
	DomainRelaxations int64                `json:"domain_relaxations"`
	CacheHit          int64                `json:"cache_hit"`
	CacheMiss         int64                `json:"cache_miss"`
	NetworkFetch      int64                `json:"network_fetch"`
	InputTokens       int64                `json:"input_tokens"`
	OutputTokens      int64                `json:"output_tokens"`
	Turns             int64                `json:"turns"`
	Tools             ToolStatsSnapshot    `json:"tools"`
	Runtime           RuntimeStatsSnapshot `json:"runtime"`
}

func handleStats(cfg Config, pool *SessionPool) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		// Snapshot the session pointers under the pool lock, then
		// release it before collecting per-session stats. Browser
		// counter reads are atomic but iterating them under pool.mu
		// would block GetOrCreate / GC / Shutdown for no reason.
		pool.mu.Lock()
		snap := make([]*Session, 0, len(pool.sessions))
		for _, s := range pool.sessions {
			snap = append(snap, s)
		}
		pool.mu.Unlock()

		sort.Slice(snap, func(i, j int) bool { return snap[i].ID < snap[j].ID })

		sess := make([]sessionStatsResponse, 0, len(snap))
		var agg aggregateStats
		runningTurns := 0
		for _, s := range snap {
			s.mu.Lock()
			created, lastUsed := s.createdAt, s.lastUsed
			s.mu.Unlock()
			if s.isActiveTurn() {
				runningTurns++
			}
			b := s.BrowserStatsSnapshot()
			u := s.UsageSnapshot()
			tools := s.ToolStatsSnapshot()
			runtime := s.RuntimeStatsSnapshot()
			sess = append(sess, sessionStatsResponse{
				ID:              s.ID,
				CreatedAt:       created.UTC().Format(time.RFC3339),
				LastUsedAt:      lastUsed.UTC().Format(time.RFC3339),
				Usage:           u,
				Tools:           tools,
				Runtime:         runtime,
				Browser:         b,
				RuntimeContract: buildSessionRuntimeContract(s, cfg),
			})
			agg.BlockedByType += b.BlockedByType
			agg.BlockedByDomain += b.BlockedByDomain
			agg.DomainRelaxations += b.DomainRelaxations
			agg.CacheHit += b.CacheHit
			agg.CacheMiss += b.CacheMiss
			agg.NetworkFetch += b.NetworkFetch
			agg.InputTokens += u.InputTokens
			agg.OutputTokens += u.OutputTokens
			agg.Turns += u.Turns
			addToolStatsSnapshot(&agg.Tools, tools)
			addRuntimeStatsSnapshot(&agg.Runtime, runtime)
		}

		resp := statsResponse{
			Listen:             cfg.Listen,
			Model:              cfg.Model,
			Build:              currentBuildInfo(),
			MaxSessions:        cfg.MaxSessions,
			ActiveSessions:     len(sess),
			RunningTurns:       runningTurns,
			ExecutorMode:       executorMode(cfg),
			EnableBrowser:      cfg.EnableBrowser,
			EnableWeb:          cfg.EnableWeb,
			EnableWebSearch:    cfg.EnableWebSearch,
			EnableMemory:       cfg.EnableMemory,
			SharedUserMemory:   cfg.SharedUserMemory,
			EnableBuiltins:     cfg.EnableBuiltins,
			EnableSubagent:     cfg.EnableSubagent,
			EnableFocusedTasks: cfg.EnableFocusedTasks,
			EnableLoopProtocol: cfg.EnableLoopProtocol,
			EvalMode:           cfg.EvalMode,
			EvalTools:          cfg.EvalTools,
			EvalAllTools:       cfg.EvalAllTools,
			ShuttingDown:       pool.IsShuttingDown(),
			WorkspaceRoot:      cfg.WorkspaceRoot,
			MemoryRoot:         cfg.MemoryRoot,
			SessionStateRoot:   pool.sessionRootPath(),
			BrowserCacheDir:    cfg.BrowserCacheDir,
			WebSearchBackend:   statsWebSearchBackend(cfg),
			ScheduleRunner:     scheduleRunnerStatsForPool(cfg, pool),
			ServerTime:         time.Now().UTC().Format(time.RFC3339),
			Sessions:           sess,
			Aggregate:          agg,
			Boundaries:         statsBoundarySnapshot(cfg),
			RuntimeContract:    buildServeRuntimeContract(cfg),
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

func scheduleRunnerStatsForPool(cfg Config, pool *SessionPool) scheduleRunnerStats {
	stats := scheduleRunnerStats{
		Enabled:             !cfg.EvalMode,
		FrontendIndependent: !cfg.EvalMode,
		SweepInterval:       sessionScheduleSweepInterval.String(),
	}
	if pool != nil {
		stats.DurableSessionStateDir = pool.sessionRootPath()
	}
	if cfg.EvalMode {
		stats.DisabledReason = "eval mode disables background scheduled turns"
		stats.SweepInterval = ""
		stats.DurableSessionStateDir = ""
		return stats
	}
	stats.Active = pool != nil && !pool.IsShuttingDown()
	if !stats.Active {
		stats.DisabledReason = "session pool is shutting down"
	}
	return stats
}

func statsWebSearchBackend(cfg Config) string {
	if !resolveServeRuntimeCapabilities(cfg).WebSearch {
		return ""
	}
	return configuredSearchBackendName()
}

func executorMode(cfg Config) string {
	if cfg.EnableBuiltins {
		return "local"
	}
	return "off"
}

func addToolStatsSnapshot(dst *ToolStatsSnapshot, src ToolStatsSnapshot) {
	dst.ToolRequests += src.ToolRequests
	dst.ToolNameCanonicalized += src.ToolNameCanonicalized
	dst.ToolArgsRepaired += src.ToolArgsRepaired
	dst.ToolRepairCalls += src.ToolRepairCalls
	dst.ToolRepairSucceeded += src.ToolRepairSucceeded
	dst.ToolRepairFailed += src.ToolRepairFailed
	dst.ToolRepairNotes += src.ToolRepairNotes
	if len(src.ToolRepairByKind) > 0 {
		if dst.ToolRepairByKind == nil {
			dst.ToolRepairByKind = make(map[string]int64, len(src.ToolRepairByKind))
		}
		for kind, count := range src.ToolRepairByKind {
			dst.ToolRepairByKind[kind] += count
		}
	}
	if len(src.ToolFailureByKind) > 0 {
		if dst.ToolFailureByKind == nil {
			dst.ToolFailureByKind = make(map[string]int64, len(src.ToolFailureByKind))
		}
		for kind, count := range src.ToolFailureByKind {
			dst.ToolFailureByKind[kind] += count
		}
	}
	dst.ToolErrors += src.ToolErrors
	dst.ToolDurationMS += src.ToolDurationMS
	dst.LoopGuardInterventions += src.LoopGuardInterventions
	dst.ForcedNoTools += src.ForcedNoTools
	dst.SourceAccessResults += src.SourceAccessResults
	dst.SourceAccessVerified += src.SourceAccessVerified
	dst.SourceAccessDiscovery += src.SourceAccessDiscovery
	dst.SourceAccessNetwork += src.SourceAccessNetwork
	dst.SourceAccessDynamic += src.SourceAccessDynamic
	dst.MemoryUpdates += src.MemoryUpdates
	dst.MemoryUpdateAdd += src.MemoryUpdateAdd
	dst.MemoryUpdateReplace += src.MemoryUpdateReplace
	dst.MemoryUpdateRemove += src.MemoryUpdateRemove
	dst.MemorySearchCalls += src.MemorySearchCalls
	dst.MemorySearchMisses += src.MemorySearchMisses
	dst.SessionSearchCalls += src.SessionSearchCalls
	dst.SessionSearchResults += src.SessionSearchResults
	dst.SessionSearchContext += src.SessionSearchContext
	dst.SessionSearchTerms += src.SessionSearchTerms
	dst.SessionSearchRecent += src.SessionSearchRecent
	dst.ToolContextTruncated += src.ToolContextTruncated
	dst.ToolContextOmitted += src.ToolContextOmitted
	dst.PlanCalls += src.PlanCalls
	dst.PlanErrors += src.PlanErrors
	if len(src.PlanByAction) > 0 {
		if dst.PlanByAction == nil {
			dst.PlanByAction = make(map[string]int64, len(src.PlanByAction))
		}
		for action, count := range src.PlanByAction {
			dst.PlanByAction[action] += count
		}
	}
	dst.FocusedTaskCalls += src.FocusedTaskCalls
	dst.FocusedTaskErrors += src.FocusedTaskErrors
	if len(src.FocusedTaskByType) > 0 {
		if dst.FocusedTaskByType == nil {
			dst.FocusedTaskByType = make(map[string]int64, len(src.FocusedTaskByType))
		}
		for taskType, count := range src.FocusedTaskByType {
			dst.FocusedTaskByType[taskType] += count
		}
	}
	dst.SubagentCalls += src.SubagentCalls
	dst.SubagentErrors += src.SubagentErrors
	if len(src.SubagentByMode) > 0 {
		if dst.SubagentByMode == nil {
			dst.SubagentByMode = make(map[string]int64, len(src.SubagentByMode))
		}
		for mode, count := range src.SubagentByMode {
			dst.SubagentByMode[mode] += count
		}
	}
}

func addRuntimeStatsSnapshot(dst *RuntimeStatsSnapshot, src RuntimeStatsSnapshot) {
	dst.RuntimeErrors += src.RuntimeErrors
	dst.ContextCompactions += src.ContextCompactions
	dst.ContextCompactionsReactive += src.ContextCompactionsReactive
	dst.ContextCompactionRemovedMessages += src.ContextCompactionRemovedMessages
	dst.ContextCompactionSummaryBytes += src.ContextCompactionSummaryBytes
	dst.ContextCompactionSummaryMissing += src.ContextCompactionSummaryMissing
	dst.ContextCompactionSummaryEmpty += src.ContextCompactionSummaryEmpty
	if len(src.TurnEndByReason) > 0 {
		if dst.TurnEndByReason == nil {
			dst.TurnEndByReason = make(map[string]int64, len(src.TurnEndByReason))
		}
		for reason, count := range src.TurnEndByReason {
			dst.TurnEndByReason[reason] += count
		}
	}
	if len(src.RuntimeErrorByKind) > 0 {
		if dst.RuntimeErrorByKind == nil {
			dst.RuntimeErrorByKind = make(map[string]int64, len(src.RuntimeErrorByKind))
		}
		for kind, count := range src.RuntimeErrorByKind {
			dst.RuntimeErrorByKind[kind] += count
		}
	}
}

func statsBoundarySnapshot(cfg Config) statsBoundaries {
	ab := agent.DefaultRuntimeBoundaries()
	mb := mcp.DefaultRuntimeBoundaries()
	mem := memory.DefaultRuntimeBoundaries()
	maxTurnSteps := cfg.MaxTurnSteps
	if maxTurnSteps <= 0 {
		maxTurnSteps = agent.DefaultMaxTurnSteps
	}
	maxTurnInputTokens := cfg.MaxTurnInputTokens
	if maxTurnInputTokens <= 0 {
		maxTurnInputTokens = agent.DefaultMaxTurnInputTokens
	}
	perCallTimeout := agent.DefaultPerCallTimeout
	if d, err := cfg.PerCallTimeoutDuration(); err == nil && d > 0 {
		perCallTimeout = d
	}
	subagentMaxDepth := cfg.SubagentMaxDepth
	if subagentMaxDepth <= 0 {
		subagentMaxDepth = agent.DefaultSubagentMaxDepth
	} else if subagentMaxDepth > agent.MaxSubagentDepth {
		subagentMaxDepth = agent.MaxSubagentDepth
	}
	return statsBoundaries{
		MaxTurnSteps:                maxTurnSteps,
		MaxTurnInputTokens:          maxTurnInputTokens,
		PerCallTimeout:              perCallTimeout.String(),
		LLMRequestBodyBytes:         ab.LLMRequestBodyBytes,
		LLMErrorBodyBytes:           ab.LLMErrorBodyBytes,
		StreamContentBytes:          ab.StreamContentBytes,
		StreamReasoningBytes:        ab.StreamReasoningBytes,
		StreamToolArgBytes:          ab.StreamToolArgBytes,
		StreamToolCalls:             ab.StreamToolCalls,
		StreamScannerBytes:          ab.StreamScannerBytes,
		ToolRequestArgsEvent:        ab.ToolRequestArgsEvent,
		ToolRequestArgString:        ab.ToolRequestArgString,
		ToolResultContext:           ab.ToolResultContextBytes,
		ToolResultContextBudget:     ab.ToolResultContextBudgetBytes,
		ToolResultEvent:             ab.ToolResultEventBytes,
		ToolResultPreview:           ab.ToolResultPreviewBytes,
		RepairableToolArg:           ab.RepairableToolArgBytes,
		ProjectContextBytes:         ab.ProjectContextBytes,
		LoopGuardIdenticalCalls:     ab.LoopGuardIdenticalCalls,
		LoopGuardFailureWarn:        ab.LoopGuardFailureWarn,
		LoopGuardFailureHalt:        ab.LoopGuardFailureHalt,
		LoopGuardWebFetchWarn:       ab.LoopGuardWebFetchFailureWarn,
		LoopGuardWebFetchHalt:       ab.LoopGuardWebFetchFailureHalt,
		LoopGuardBrowserFindNoMatch: ab.LoopGuardBrowserFindNoMatch,
		PlanPerTurnCalls:            ab.PlanPerTurnCalls,
		PlanSteps:                   ab.PlanSteps,
		PlanStepTextBytes:           ab.PlanStepTextBytes,
		PlanNoteBytes:               ab.PlanNoteBytes,
		PlanEvidenceRefs:            ab.PlanEvidenceRefs,
		PlanEvidenceRefBytes:        ab.PlanEvidenceRefBytes,
		PlanStateBytes:              ab.PlanStateBytes,
		ActivePlanStepBytes:         ab.ActivePlanStepBytes,
		ActivePlanNoteBytes:         ab.ActivePlanNoteBytes,
		ActivePlanEvidenceRefs:      ab.ActivePlanEvidenceRefs,
		ActivePlanEvidenceRefBytes:  ab.ActivePlanEvidenceRef,
		FocusedTaskDefaultTurns:     ab.FocusedTaskDefaultTurns,
		FocusedTaskMaxTurns:         ab.FocusedTaskMaxTurns,
		FocusedTaskPerTurnCalls:     ab.FocusedTaskPerTurnCalls,
		FocusedTaskTypeBytes:        ab.FocusedTaskTypeBytes,
		FocusedTaskObjectiveBytes:   ab.FocusedTaskObjectiveBytes,
		FocusedTaskToolResult:       ab.FocusedTaskToolResultBytes,
		FocusedTaskSummaryBytes:     ab.FocusedTaskSummaryBytes,
		FocusedTaskFindingEvidence:  ab.FocusedTaskFindingEvidenceBytes,
		FocusedTaskFindings:         ab.FocusedTaskFindings,
		FocusedTaskListEntries:      ab.FocusedTaskListEntries,
		FocusedTaskToolCalls:        ab.FocusedTaskToolCalls,
		SubagentDefaultTurns:        ab.SubagentDefaultTurns,
		SubagentMaxTurns:            ab.SubagentMaxTurns,
		SubagentTaskBytes:           ab.SubagentTaskBytes,
		SubagentModeBytes:           ab.SubagentModeBytes,
		SubagentToolResult:          ab.SubagentToolResultBytes,
		SubagentDefaultDepth:        ab.SubagentDefaultDepth,
		SubagentConfiguredMaxDepth:  subagentMaxDepth,
		SubagentHardMaxDepth:        ab.SubagentHardMaxDepth,
		SkillActionBytes:            ab.SkillActionBytes,
		SkillNameBytes:              ab.SkillNameBytes,
		SkillDescriptionBytes:       ab.SkillDescriptionBytes,
		SkillBodyBytes:              ab.SkillBodyBytes,
		SkillSourceBytes:            ab.SkillSourceBytes,
		SkillTriggers:               ab.SkillTriggers,
		SkillTriggerBytes:           ab.SkillTriggerBytes,
		SkillRequiredTools:          ab.SkillRequiredTools,
		SkillRequiredToolBytes:      ab.SkillRequiredToolBytes,
		RuntimeSkills:               ab.RuntimeSkills,
		RuntimeSkillDirReadBatch:    ab.RuntimeSkillDirReadBatch,
		RuntimeSkillManifestBytes:   ab.RuntimeSkillManifestBytes,
		RuntimeSkillProposalBytes:   ab.RuntimeSkillProposalBytes,
		RuntimeSkillProposalID:      ab.RuntimeSkillProposalIDBytes,
		MCPToolResultBytes:          mb.ToolResultBytes,
		MCPHTTPJSONResponse:         mb.HTTPJSONResponseBytes,
		MCPHTTPSSELine:              mb.HTTPSSELineBytes,
		MCPStdioFrame:               mb.StdioFrameBytes,
		JSONLRecordBytes:            jsonl.DefaultMaxRecordBytes,
		MemoryFileBytes:             mem.FileBytes,
		MemorySearchQuery:           mem.SearchQueryBytes,
		MemorySearchTerms:           mem.SearchQueryTerms,
		MemorySearchSnippet:         mem.SearchSnippet,
		MemoryResponseEntry:         mem.ResponseEntry,
	}
}
