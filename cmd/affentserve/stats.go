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
	Listen           string                 `json:"listen"`
	Model            string                 `json:"model"`
	MaxSessions      int                    `json:"max_sessions"`
	ActiveSessions   int                    `json:"active_sessions"`
	ShuttingDown     bool                   `json:"shutting_down"`
	WorkspaceRoot    string                 `json:"workspace_root,omitempty"`
	MemoryRoot       string                 `json:"memory_root,omitempty"`
	SessionStateRoot string                 `json:"session_state_root"`
	BrowserCacheDir  string                 `json:"browser_cache_dir,omitempty"`
	ServerTime       string                 `json:"server_time"`
	Sessions         []sessionStatsResponse `json:"sessions"`
	Aggregate        aggregateStats         `json:"aggregate"`
	Boundaries       statsBoundaries        `json:"boundaries"`
}

type statsBoundaries struct {
	MaxTurnSteps               int    `json:"max_turn_steps"`
	PerCallTimeout             string `json:"per_call_timeout"`
	LLMRequestBodyBytes        int    `json:"llm_request_body_bytes"`
	LLMErrorBodyBytes          int    `json:"llm_error_body_bytes"`
	StreamContentBytes         int    `json:"stream_content_bytes"`
	StreamReasoningBytes       int    `json:"stream_reasoning_bytes"`
	StreamToolArgBytes         int    `json:"stream_tool_arg_bytes"`
	StreamToolCalls            int    `json:"stream_tool_calls"`
	StreamScannerBytes         int    `json:"stream_scanner_bytes"`
	ToolRequestArgsEvent       int    `json:"tool_request_args_event_bytes"`
	ToolRequestArgString       int    `json:"tool_request_arg_string_bytes"`
	ToolResultContext          int    `json:"tool_result_context_bytes"`
	ToolResultEvent            int    `json:"tool_result_event_bytes"`
	ToolResultPreview          int    `json:"tool_result_preview_bytes"`
	RepairableToolArg          int    `json:"repairable_tool_arg_bytes"`
	ProjectContextBytes        int    `json:"project_context_bytes"`
	PlanSteps                  int    `json:"plan_steps"`
	PlanStepTextBytes          int    `json:"plan_step_text_bytes"`
	PlanNoteBytes              int    `json:"plan_note_bytes"`
	PlanEvidenceRefs           int    `json:"plan_evidence_refs"`
	PlanEvidenceRefBytes       int    `json:"plan_evidence_ref_bytes"`
	PlanStateBytes             int    `json:"plan_state_bytes"`
	ActivePlanStepBytes        int    `json:"active_plan_step_bytes"`
	ActivePlanNoteBytes        int    `json:"active_plan_note_bytes"`
	ActivePlanEvidenceRefs     int    `json:"active_plan_evidence_refs"`
	ActivePlanEvidenceRefBytes int    `json:"active_plan_evidence_ref_bytes"`
	MCPToolResultBytes         int    `json:"mcp_tool_result_bytes"`
	MCPHTTPJSONResponse        int    `json:"mcp_http_json_response_bytes"`
	MCPHTTPSSELine             int    `json:"mcp_http_sse_line_bytes"`
	MCPStdioFrame              int    `json:"mcp_stdio_frame_bytes"`
	JSONLRecordBytes           int    `json:"jsonl_record_bytes"`
	MemoryFileBytes            int    `json:"memory_file_bytes"`
	MemorySearchQuery          int    `json:"memory_search_query_bytes"`
	MemorySearchTerms          int    `json:"memory_search_terms"`
	MemorySearchSnippet        int    `json:"memory_search_snippet_chars"`
	MemoryResponseEntry        int    `json:"memory_response_entry_chars"`
}

type sessionStatsResponse struct {
	ID         string               `json:"id"`
	CreatedAt  string               `json:"created_at"`
	LastUsedAt string               `json:"last_used_at"`
	Usage      UsageSnapshot        `json:"usage"`
	Browser    BrowserStatsSnapshot `json:"browser"`
}

type aggregateStats struct {
	BlockedByType   int64 `json:"blocked_by_type"`
	BlockedByDomain int64 `json:"blocked_by_domain"`
	CacheHit        int64 `json:"cache_hit"`
	CacheMiss       int64 `json:"cache_miss"`
	NetworkFetch    int64 `json:"network_fetch"`
	InputTokens     int64 `json:"input_tokens"`
	OutputTokens    int64 `json:"output_tokens"`
	Turns           int64 `json:"turns"`
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
		for _, s := range snap {
			s.mu.Lock()
			created, lastUsed := s.createdAt, s.lastUsed
			s.mu.Unlock()
			b := s.BrowserStatsSnapshot()
			u := s.UsageSnapshot()
			sess = append(sess, sessionStatsResponse{
				ID:         s.ID,
				CreatedAt:  created.UTC().Format(time.RFC3339),
				LastUsedAt: lastUsed.UTC().Format(time.RFC3339),
				Usage:      u,
				Browser:    b,
			})
			agg.BlockedByType += b.BlockedByType
			agg.BlockedByDomain += b.BlockedByDomain
			agg.CacheHit += b.CacheHit
			agg.CacheMiss += b.CacheMiss
			agg.NetworkFetch += b.NetworkFetch
			agg.InputTokens += u.InputTokens
			agg.OutputTokens += u.OutputTokens
			agg.Turns += u.Turns
		}

		resp := statsResponse{
			Listen:           cfg.Listen,
			Model:            cfg.Model,
			MaxSessions:      cfg.MaxSessions,
			ActiveSessions:   len(sess),
			ShuttingDown:     pool.IsShuttingDown(),
			WorkspaceRoot:    cfg.WorkspaceRoot,
			MemoryRoot:       cfg.MemoryRoot,
			SessionStateRoot: pool.sessionRootPath(),
			BrowserCacheDir:  cfg.BrowserCacheDir,
			ServerTime:       time.Now().UTC().Format(time.RFC3339),
			Sessions:         sess,
			Aggregate:        agg,
			Boundaries:       statsBoundarySnapshot(cfg),
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
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
	perCallTimeout := agent.DefaultPerCallTimeout
	if d, err := cfg.PerCallTimeoutDuration(); err == nil && d > 0 {
		perCallTimeout = d
	}
	return statsBoundaries{
		MaxTurnSteps:               maxTurnSteps,
		PerCallTimeout:             perCallTimeout.String(),
		LLMRequestBodyBytes:        ab.LLMRequestBodyBytes,
		LLMErrorBodyBytes:          ab.LLMErrorBodyBytes,
		StreamContentBytes:         ab.StreamContentBytes,
		StreamReasoningBytes:       ab.StreamReasoningBytes,
		StreamToolArgBytes:         ab.StreamToolArgBytes,
		StreamToolCalls:            ab.StreamToolCalls,
		StreamScannerBytes:         ab.StreamScannerBytes,
		ToolRequestArgsEvent:       ab.ToolRequestArgsEvent,
		ToolRequestArgString:       ab.ToolRequestArgString,
		ToolResultContext:          ab.ToolResultContextBytes,
		ToolResultEvent:            ab.ToolResultEventBytes,
		ToolResultPreview:          ab.ToolResultPreviewBytes,
		RepairableToolArg:          ab.RepairableToolArgBytes,
		ProjectContextBytes:        ab.ProjectContextBytes,
		PlanSteps:                  ab.PlanSteps,
		PlanStepTextBytes:          ab.PlanStepTextBytes,
		PlanNoteBytes:              ab.PlanNoteBytes,
		PlanEvidenceRefs:           ab.PlanEvidenceRefs,
		PlanEvidenceRefBytes:       ab.PlanEvidenceRefBytes,
		PlanStateBytes:             ab.PlanStateBytes,
		ActivePlanStepBytes:        ab.ActivePlanStepBytes,
		ActivePlanNoteBytes:        ab.ActivePlanNoteBytes,
		ActivePlanEvidenceRefs:     ab.ActivePlanEvidenceRefs,
		ActivePlanEvidenceRefBytes: ab.ActivePlanEvidenceRef,
		MCPToolResultBytes:         mb.ToolResultBytes,
		MCPHTTPJSONResponse:        mb.HTTPJSONResponseBytes,
		MCPHTTPSSELine:             mb.HTTPSSELineBytes,
		MCPStdioFrame:              mb.StdioFrameBytes,
		JSONLRecordBytes:           jsonl.DefaultMaxRecordBytes,
		MemoryFileBytes:            mem.FileBytes,
		MemorySearchQuery:          mem.SearchQueryBytes,
		MemorySearchTerms:          mem.SearchQueryTerms,
		MemorySearchSnippet:        mem.SearchSnippet,
		MemoryResponseEntry:        mem.ResponseEntry,
	}
}
