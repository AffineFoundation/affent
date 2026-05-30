package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	agent "github.com/affinefoundation/affent/internal/agent"
	"github.com/affinefoundation/affent/internal/jsonl"
	"github.com/affinefoundation/affent/internal/loopstate"
	"github.com/affinefoundation/affent/internal/memory"
	"github.com/affinefoundation/affent/internal/sessionsearch"
	"github.com/affinefoundation/affent/internal/sessionstate"
	"github.com/affinefoundation/affent/internal/sse"
	"github.com/affinefoundation/affent/internal/taskstate"
	"github.com/affinefoundation/affent/internal/textutil"
	"github.com/affinefoundation/affent/internal/toolfailure"
)

const (
	defaultSessionListLimit     = 100
	maxSessionListLimit         = 1000
	sessionReadDirBatch         = 128
	maxSessionCreateBodyBytes   = 4096
	maxSessionTaskSummaryChars  = 160
	maxSessionSummaryLineBytes  = 1024 * 1024
	maxSessionSummaryTailBytes  = 2 * 1024 * 1024
	maxSessionEventSummaryHead  = 512 * 1024
	maxSessionRuntimeSkillNames = 128
	maxSessionRecoveryHintChars = 160
	maxSessionMemoryTopics      = 64
)

type sessionListResponse struct {
	Sessions  []sessionSummary `json:"sessions"`
	NextAfter string           `json:"next_after,omitempty"`
	HasMore   bool             `json:"has_more"`
}

type sessionDetailResponse struct {
	Session sessionSummary `json:"session"`
}

type sessionCreateRequest struct {
	SessionID string `json:"session_id,omitempty"`
}

type sessionCreateResponse struct {
	Session sessionSummary `json:"session"`
}

type sessionSummary struct {
	ID                 string                           `json:"id"`
	SummaryTitle       string                           `json:"summary_title,omitempty"`
	Active             bool                             `json:"active"`
	Durable            bool                             `json:"durable"`
	CreatedAt          string                           `json:"created_at,omitempty"`
	LastUsedAt         string                           `json:"last_used_at,omitempty"`
	WorkspacePath      string                           `json:"workspace_path,omitempty"`
	WorkspaceLabel     string                           `json:"workspace_label,omitempty"`
	DefaultBranch      string                           `json:"default_branch,omitempty"`
	DirtyState         string                           `json:"dirty_state,omitempty"`
	LastAgentCWD       string                           `json:"last_agent_cwd,omitempty"`
	Capabilities       *sessionCapabilities             `json:"capabilities,omitempty"`
	HasConversation    bool                             `json:"has_conversation"`
	LatestUserMessage  string                           `json:"latest_user_message,omitempty"`
	TopicUserMessage   string                           `json:"topic_user_message,omitempty"`
	LatestRecoveryHint string                           `json:"latest_recovery_hint,omitempty"`
	LatestMemoryUpdate *sse.MemoryUpdateMeta            `json:"latest_memory_update,omitempty"`
	HasEvents          bool                             `json:"has_events"`
	HasPlan            bool                             `json:"has_plan"`
	PlanSummary        *sessionPlanSummary              `json:"plan_summary,omitempty"`
	HasLoopProtocol    bool                             `json:"has_loop_protocol"`
	LoopProtocol       *sessionLoopProtocolSummary      `json:"loop_protocol,omitempty"`
	HasLoopState       bool                             `json:"has_loop_state"`
	LoopState          *loopstate.State                 `json:"loop_state,omitempty"`
	HasSchedules       bool                             `json:"has_schedules"`
	Schedules          *sessionSchedulesSummary         `json:"schedules,omitempty"`
	HasArtifacts       bool                             `json:"has_artifacts"`
	Artifacts          *sessionArtifactsSummary         `json:"artifacts,omitempty"`
	HasMemory          bool                             `json:"has_memory"`
	Memory             *sessionMemorySummary            `json:"memory,omitempty"`
	HasRuntimeSkills   bool                             `json:"has_runtime_skills"`
	RuntimeSkillNames  []string                         `json:"runtime_skill_names,omitempty"`
	Context            *sessionContextSummary           `json:"context,omitempty"`
	ContextCompactions *sessionContextCompactionSummary `json:"context_compactions,omitempty"`
	TaskState          *sessionTaskStateSummary         `json:"task_state,omitempty"`
	Usage              *UsageSnapshot                   `json:"usage,omitempty"`
	Tools              *ToolStatsSnapshot               `json:"tools,omitempty"`
	Runtime            *RuntimeStatsSnapshot            `json:"runtime,omitempty"`
	Browser            *BrowserStatsSnapshot            `json:"browser,omitempty"`
}

type sessionContextSummary struct {
	MessageCount                       int    `json:"message_count"`
	CompactTrigger                     int    `json:"compact_trigger"`
	CompactPercent                     int    `json:"compact_percent"`
	MessagesUntilCompact               int    `json:"messages_until_compact"`
	ContextBytes                       int    `json:"context_bytes,omitempty"`
	ConversationBytes                  int    `json:"conversation_bytes,omitempty"`
	ToolSchemaBytes                    int    `json:"tool_schema_bytes,omitempty"`
	CompactTriggerBytes                int    `json:"compact_trigger_bytes,omitempty"`
	ByteCompactPercent                 int    `json:"byte_compact_percent,omitempty"`
	BytesUntilCompact                  int    `json:"bytes_until_compact,omitempty"`
	MessageCompactPercent              int    `json:"message_compact_percent,omitempty"`
	EstimatedRequestInputTokens        int    `json:"estimated_request_input_tokens,omitempty"`
	EstimatedConversationTokens        int    `json:"estimated_conversation_tokens,omitempty"`
	EstimatedToolSchemaTokens          int    `json:"estimated_tool_schema_tokens,omitempty"`
	ToolSchemaBudgetTokens             int    `json:"tool_schema_budget_tokens,omitempty"`
	ModelContextWindowTokens           int    `json:"model_context_window_tokens,omitempty"`
	ModelContextWindowAuto             bool   `json:"model_context_window_auto,omitempty"`
	ModelContextWindowSource           string `json:"model_context_window_source,omitempty"`
	ModelContextWindowEffectivePercent int    `json:"model_context_window_effective_percent,omitempty"`
	ReservedOutputTokens               int    `json:"reserved_output_tokens,omitempty"`
	CompactTriggerInputPercent         int    `json:"compact_trigger_input_percent,omitempty"`
	CompactTriggerInputTokens          int    `json:"compact_trigger_input_tokens,omitempty"`
	CompactSummaryPromptMaxBytes       int    `json:"compact_summary_prompt_max_bytes,omitempty"`
	RequestInputCompactPercent         int    `json:"request_input_compact_percent,omitempty"`
	RequestInputTokensUntilCompact     int    `json:"request_input_tokens_until_compact,omitempty"`
}

type sessionContextCompactionSummary struct {
	Count                            int    `json:"count"`
	Reactive                         int    `json:"reactive"`
	RemovedMessages                  int    `json:"removed_messages"`
	SummaryBytes                     int    `json:"summary_bytes,omitempty"`
	SummaryMissing                   int    `json:"summary_missing,omitempty"`
	SummaryEmpty                     int    `json:"summary_empty,omitempty"`
	LatestReason                     string `json:"latest_reason,omitempty"`
	LatestReactive                   bool   `json:"latest_reactive,omitempty"`
	LatestSummaryState               string `json:"latest_summary_state,omitempty"`
	LatestEstimatedInputTokens       int    `json:"latest_estimated_input_tokens,omitempty"`
	LatestAfterEstimatedInputTokens  int    `json:"latest_after_estimated_input_tokens,omitempty"`
	LatestTriggerInputTokens         int    `json:"latest_trigger_input_tokens,omitempty"`
	LatestModelContextWindowTokens   int    `json:"latest_model_context_window_tokens,omitempty"`
	LatestModelContextWindowSource   string `json:"latest_model_context_window_source,omitempty"`
	LatestReservedOutputTokens       int    `json:"latest_reserved_output_tokens,omitempty"`
	LatestTriggerInputPercent        int    `json:"latest_trigger_input_percent,omitempty"`
	LatestCompactScopeActive         bool   `json:"latest_compact_scope_active,omitempty"`
	LatestCompactWindowOrdinal       int64  `json:"latest_compact_window_ordinal,omitempty"`
	LatestCompactWindowPrefill       int    `json:"latest_compact_window_prefill_input_tokens,omitempty"`
	LatestCompactWindowPrefillSource string `json:"latest_compact_window_prefill_source,omitempty"`
	LatestCompactScopedInputTokens   int    `json:"latest_compact_scoped_input_tokens,omitempty"`
	LatestCompactHardInputLimit      int    `json:"latest_compact_hard_input_limit_tokens,omitempty"`
	TailOnly                         bool   `json:"tail_only,omitempty"`
}

type sessionTaskStateSummary = taskstate.Snapshot
type sessionTaskStateFile = taskstate.File
type sessionTaskStateAction = taskstate.Action
type sessionTaskStateFailure = taskstate.Failure
type sessionTaskStateEvidence = taskstate.Evidence

type sessionArtifactsSummary struct {
	Count         int    `json:"count"`
	TotalBytes    int64  `json:"total_bytes"`
	LatestPath    string `json:"latest_path,omitempty"`
	LatestModTime string `json:"latest_mod_time,omitempty"`
}

type sessionMemorySummary struct {
	SharedUserMemory bool   `json:"shared_user_memory,omitempty"`
	BucketCount      int    `json:"bucket_count"`
	EntryCount       int    `json:"entry_count"`
	CharsUsed        int    `json:"chars_used"`
	LatestTarget     string `json:"latest_target,omitempty"`
	LatestTopic      string `json:"latest_topic,omitempty"`
	LatestAt         string `json:"latest_at,omitempty"`
}

type sessionCapabilities struct {
	EvalMode              bool     `json:"eval_mode"`
	EvalTools             string   `json:"eval_tools,omitempty"`
	EvalAllTools          bool     `json:"eval_all_tools,omitempty"`
	WorkspaceTools        []string `json:"workspace_tools,omitempty"`
	Builtins              bool     `json:"builtins"`
	SkillInstall          bool     `json:"skill_install"`
	Plan                  bool     `json:"plan"`
	LoopProtocol          bool     `json:"loop_protocol"`
	SessionSchedule       bool     `json:"session_schedule"`
	SessionScheduleRunner bool     `json:"session_schedule_runner"`
	Memory                bool     `json:"memory"`
	SessionSearch         bool     `json:"session_search"`
	SymbolContext         bool     `json:"symbol_context"`
	RepoSearch            bool     `json:"repo_search"`
	Browser               bool     `json:"browser"`
	BrowserScreenshot     bool     `json:"browser_screenshot"`
	Web                   bool     `json:"web"`
	WebSearch             bool     `json:"web_search"`
	WebSearchBackend      string   `json:"web_search_backend,omitempty"`
	Subagent              bool     `json:"subagent"`
	SubagentMaxDepth      int      `json:"subagent_max_depth"`
	FocusedTasks          bool     `json:"focused_tasks"`
	// FocusedTaskProfiles enumerates the run_task task_type values the
	// model can actually request under this session's wiring. Omitted
	// when focused tasks are disabled or no profile's deps are
	// satisfied; lets clients build a typed task_type picker without
	// having to parse the run_task tool's JSON schema themselves.
	FocusedTaskProfiles []string `json:"focused_task_profiles,omitempty"`
}

type sessionListOptions struct {
	After string
	Limit int
}

func handleSessionsCollection(pool *SessionPool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			handleSessionList(pool, w, r)
		case http.MethodPost:
			handleSessionCreate(pool, w, r)
		default:
			writeJSONErrorTyped(w, http.StatusMethodNotAllowed, "method not allowed", nil, "bad_request")
		}
	}
}

func handleSessionList(pool *SessionPool, w http.ResponseWriter, r *http.Request) {
	if pool == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "session pool unavailable", nil)
		return
	}
	opts, err := parseSessionListQuery(r)
	if err != nil {
		writeJSONErrorTyped(w, http.StatusBadRequest, "invalid sessions query", err, "bad_request")
		return
	}
	summaries, hasMore, err := listSessionSummaries(pool, opts)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "list sessions", err)
		return
	}
	resp := sessionListResponse{Sessions: summaries, HasMore: hasMore}
	if len(summaries) > 0 && hasMore {
		resp.NextAfter = summaries[len(summaries)-1].ID
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func handleSessionCreate(pool *SessionPool, w http.ResponseWriter, r *http.Request) {
	if pool == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "session pool unavailable", nil)
		return
	}
	req, err := decodeSessionCreateRequest(w, r)
	if err != nil {
		writeJSONErrorTyped(w, http.StatusBadRequest, "invalid create session request", err, "bad_request")
		return
	}
	if req.SessionID != "" {
		if err := agent.ValidateSessionID(req.SessionID); err != nil {
			writeJSONErrorTyped(w, http.StatusBadRequest, "invalid session id", err, "bad_request")
			return
		}
	}
	existed := req.SessionID != "" && sessionKnown(pool, req.SessionID)
	sess, err := pool.GetOrCreate(req.SessionID)
	if err != nil {
		if errors.Is(err, ErrShuttingDown) {
			writeJSONError(w, http.StatusServiceUnavailable, "server shutting down", err)
			return
		}
		writeJSONError(w, http.StatusInternalServerError, "create session", err)
		return
	}
	summary, found, err := summarizeSession(pool, sess.ID, sess)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "read session", err)
		return
	}
	if !found {
		writeJSONError(w, http.StatusInternalServerError, "created session not found", nil)
		return
	}
	status := http.StatusCreated
	if existed {
		status = http.StatusOK
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(sessionCreateResponse{Session: summary})
}

func handleSessionDetail(pool *SessionPool, sessionID string, w http.ResponseWriter, _ *http.Request) {
	if pool == nil {
		writeJSONError(w, http.StatusNotFound, "session not found", nil)
		return
	}
	if err := agent.ValidateSessionID(sessionID); err != nil {
		writeJSONErrorTyped(w, http.StatusBadRequest, "invalid session id", err, "bad_request")
		return
	}
	active := activeSessionByID(pool, sessionID)
	summary, found, err := summarizeSession(pool, sessionID, active)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "read session", err)
		return
	}
	if !found {
		writeJSONError(w, http.StatusNotFound, "session not found", nil)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(sessionDetailResponse{Session: summary})
}

func parseSessionListQuery(r *http.Request) (sessionListOptions, error) {
	opts := sessionListOptions{Limit: defaultSessionListLimit}
	q := r.URL.Query()
	if raw := q.Get("after"); raw != "" {
		if err := agent.ValidateSessionID(raw); err != nil {
			return opts, err
		}
		opts.After = raw
	}
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			return opts, err
		}
		if n <= 0 {
			return opts, errors.New("limit must be positive")
		}
		if n > maxSessionListLimit {
			n = maxSessionListLimit
		}
		opts.Limit = n
	}
	return opts, nil
}

func decodeSessionCreateRequest(w http.ResponseWriter, r *http.Request) (sessionCreateRequest, error) {
	var req sessionCreateRequest
	if r.Body == nil || r.Body == http.NoBody {
		return req, nil
	}
	defer r.Body.Close()
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSessionCreateBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		if errors.Is(err, io.EOF) {
			return req, nil
		}
		return req, err
	}
	var extra struct{}
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return req, errors.New("request body must contain a single JSON object")
	}
	return req, nil
}

func listSessionSummaries(pool *SessionPool, opts sessionListOptions) ([]sessionSummary, bool, error) {
	candidates := map[string]sessionSummary{}

	pool.mu.Lock()
	active := make([]*Session, 0, len(pool.sessions))
	for _, s := range pool.sessions {
		active = append(active, s)
	}
	pool.mu.Unlock()

	for _, s := range active {
		if s.ID <= opts.After {
			continue
		}
		summary, found, err := summarizeSession(pool, s.ID, s)
		if err != nil {
			return nil, false, err
		}
		if found {
			addSessionCandidate(candidates, summary, opts.Limit+1)
		}
	}

	root := pool.sessionRootPath()
	dir, err := os.Open(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return sortedSessionCandidates(candidates, opts.Limit), len(candidates) > opts.Limit, nil
		}
		return nil, false, err
	}
	defer dir.Close()

	durableIDs := map[string]struct{}{}
	for {
		entries, err := dir.ReadDir(sessionReadDirBatch)
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, false, err
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			id := entry.Name()
			if strings.HasPrefix(id, ".") || id <= opts.After || agent.ValidateSessionID(id) != nil {
				continue
			}
			if _, ok := candidates[id]; ok {
				continue
			}
			addSessionIDCandidate(durableIDs, id, opts.Limit+1)
		}
		if errors.Is(err, io.EOF) {
			break
		}
	}

	for _, id := range sortedSessionIDCandidates(durableIDs, opts.Limit+1) {
		summary, found, err := summarizeSession(pool, id, nil)
		if err != nil {
			continue
		}
		if found {
			addSessionCandidate(candidates, summary, opts.Limit+1)
		}
	}

	return sortedSessionCandidates(candidates, opts.Limit), len(candidates) > opts.Limit, nil
}

func addSessionIDCandidate(candidates map[string]struct{}, id string, cap int) {
	if cap <= 0 {
		return
	}
	candidates[id] = struct{}{}
	for len(candidates) > cap {
		var highest string
		for id := range candidates {
			if highest == "" || id > highest {
				highest = id
			}
		}
		delete(candidates, highest)
	}
}

func sortedSessionIDCandidates(candidates map[string]struct{}, limit int) []string {
	ids := make([]string, 0, len(candidates))
	for id := range candidates {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	if len(ids) > limit {
		ids = ids[:limit]
	}
	return ids
}

func addSessionCandidate(candidates map[string]sessionSummary, summary sessionSummary, cap int) {
	if cap <= 0 {
		return
	}
	if existing, ok := candidates[summary.ID]; ok {
		candidates[summary.ID] = mergeSessionSummaries(existing, summary)
	} else {
		candidates[summary.ID] = summary
	}
	for len(candidates) > cap {
		var highest string
		for id := range candidates {
			if highest == "" || id > highest {
				highest = id
			}
		}
		delete(candidates, highest)
	}
}

func sortedSessionCandidates(candidates map[string]sessionSummary, limit int) []sessionSummary {
	ids := make([]string, 0, len(candidates))
	for id := range candidates {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	if len(ids) > limit {
		ids = ids[:limit]
	}
	out := make([]sessionSummary, 0, len(ids))
	for _, id := range ids {
		out = append(out, candidates[id])
	}
	return out
}

func sessionKnown(pool *SessionPool, id string) bool {
	if activeSessionByID(pool, id) != nil {
		return true
	}
	_, found, err := durableSessionDirInfo(pool.sessionDirPath(id))
	return err == nil && found
}

func activeSessionByID(pool *SessionPool, id string) *Session {
	pool.mu.Lock()
	s := pool.sessions[id]
	pool.mu.Unlock()
	return s
}

func summarizeSession(pool *SessionPool, id string, active *Session) (sessionSummary, bool, error) {
	summary := sessionSummary{ID: id}
	if active != nil {
		summary = mergeSessionSummaries(summary, summarizeActiveSession(active, pool.cfg))
	}
	durable, found, err := summarizeDurableSession(pool, id)
	if err != nil {
		return sessionSummary{}, false, err
	}
	if found {
		summary = mergeSessionSummaries(summary, durable)
	}
	return summary, active != nil || found, nil
}

func summarizeActiveSession(s *Session, cfg Config) sessionSummary {
	s.mu.Lock()
	createdAt, lastUsedAt := s.createdAt, s.lastUsed
	s.mu.Unlock()
	messages := s.conv.Snapshot()
	latestUser, topicUser := userMessageSummariesFromMessages(messages)
	if s.sessionDir != "" {
		if latest, topic, err := userMessageSummariesFromEventsFile(filepath.Join(s.sessionDir, "events.jsonl")); err == nil {
			if latest != "" {
				latestUser = latest
			}
			if topic != "" {
				topicUser = topic
			}
		}
	}
	toolSurface := agent.ToolSurfaceSelection{}
	if s.registry != nil {
		toolSurface = s.registry.SelectModelTools(agent.ToolSurfacePolicy{
			SchemaTokenBudget: sessionToolSchemaBudgetTokens(messages, cfg),
		})
	}
	inputEstimate := agent.EstimateRequestInput(messages, toolSurface.Defs)
	context := sessionContextSnapshot(len(messages), inputEstimate, cfg)
	context.ToolSchemaBudgetTokens = toolSurface.SchemaBudgetTokens
	usage := s.UsageSnapshot()
	tools := s.ToolStatsSnapshot()
	runtime := s.RuntimeStatsSnapshot()
	browser := s.BrowserStatsSnapshot()
	caps := summarizeActiveCapabilities(s, cfg)
	summary := sessionSummary{
		ID:                s.ID,
		Active:            true,
		CreatedAt:         formatTime(createdAt),
		LastUsedAt:        formatTime(lastUsedAt),
		WorkspacePath:     s.Workspace(),
		WorkspaceLabel:    workspaceLabel(s.Workspace()),
		LatestUserMessage: latestUser,
		TopicUserMessage:  topicUser,
		Capabilities:      &caps,
		Context:           &context,
		Usage:             &usage,
		Tools:             &tools,
		Runtime:           &runtime,
		Browser:           &browser,
	}
	if s.loopProtocolPath != "" {
		if lp, found, err := loopstate.SummarizeFile(s.loopProtocolPath, loopstate.ProtocolRelPath(s.ID)); err == nil && found {
			summary.HasLoopProtocol = true
			summary.LoopProtocol = &lp
		}
	}
	if s.loopProtocolPath != "" {
		statePath := filepath.Join(filepath.Dir(s.loopProtocolPath), loopstate.StateFileName)
		if state, found, err := loopstate.ReadState(statePath); err == nil && found {
			summary.HasLoopState = true
			summary.LoopState = &state
		}
	}
	if schedules := summarizeSessionSchedulesFileForDir(activeSessionStateDir(s), s.ID); schedules != nil {
		summary.HasSchedules = true
		summary.Schedules = schedules
	}
	if compactions := contextCompactionSummaryFromRuntimeStats(runtime); compactions != nil {
		summary.ContextCompactions = compactions
	}
	if s.sessionDir != "" {
		eventsPath := filepath.Join(s.sessionDir, "events.jsonl")
		if hint, err := latestRecoveryHintFromEventsFile(eventsPath); err == nil && hint != "" {
			summary.LatestRecoveryHint = hint
		}
		if cwd, hasShell, err := latestShellCWDFromEventsFile(eventsPath); err == nil {
			if cwd != "" {
				summary.LastAgentCWD = cwd
			} else if hasShell {
				summary.LastAgentCWD = s.Workspace()
			}
		}
	}
	if s.loopProtocolPath != "" {
		eventsPath := filepath.Join(filepath.Dir(s.loopProtocolPath), "events.jsonl")
		if hint, err := latestRecoveryHintFromEventsFile(eventsPath); err == nil && summary.LatestRecoveryHint == "" {
			summary.LatestRecoveryHint = hint
		}
		if cwd, hasShell, err := latestShellCWDFromEventsFile(eventsPath); err == nil && summary.LastAgentCWD == "" {
			if cwd != "" {
				summary.LastAgentCWD = cwd
			} else if hasShell {
				summary.LastAgentCWD = s.Workspace()
			}
		}
	}
	taskEventsPath := ""
	if s.sessionDir != "" {
		taskEventsPath = filepath.Join(s.sessionDir, "events.jsonl")
	} else if s.loopProtocolPath != "" {
		taskEventsPath = filepath.Join(filepath.Dir(s.loopProtocolPath), "events.jsonl")
	}
	_ = populateSessionTaskState(&summary, taskEventsPath)
	populateSessionSummaryTitle(&summary)
	return summary
}

func activeSessionStateDir(s *Session) string {
	if s == nil {
		return ""
	}
	if strings.TrimSpace(s.sessionDir) != "" {
		return s.sessionDir
	}
	if strings.TrimSpace(s.loopProtocolPath) != "" {
		return filepath.Dir(s.loopProtocolPath)
	}
	return ""
}

func contextCompactionSummaryFromRuntimeStats(stats RuntimeStatsSnapshot) *sessionContextCompactionSummary {
	if stats.ContextCompactions <= 0 {
		return nil
	}
	return &sessionContextCompactionSummary{
		Count:                            int(stats.ContextCompactions),
		Reactive:                         int(stats.ContextCompactionsReactive),
		RemovedMessages:                  int(stats.ContextCompactionRemovedMessages),
		SummaryBytes:                     int(stats.ContextCompactionSummaryBytes),
		SummaryMissing:                   int(stats.ContextCompactionSummaryMissing),
		SummaryEmpty:                     int(stats.ContextCompactionSummaryEmpty),
		LatestReason:                     stats.ContextCompactionLatestReason,
		LatestReactive:                   stats.ContextCompactionLatestReactive,
		LatestSummaryState:               stats.ContextCompactionLatestState,
		LatestEstimatedInputTokens:       int(stats.ContextCompactionLatestEstimatedInputTokens),
		LatestAfterEstimatedInputTokens:  int(stats.ContextCompactionLatestAfterEstimatedInputTokens),
		LatestTriggerInputTokens:         int(stats.ContextCompactionLatestTriggerInputTokens),
		LatestModelContextWindowTokens:   int(stats.ContextCompactionLatestModelContextWindowTokens),
		LatestModelContextWindowSource:   stats.ContextCompactionLatestModelContextWindowSource,
		LatestReservedOutputTokens:       int(stats.ContextCompactionLatestReservedOutputTokens),
		LatestTriggerInputPercent:        int(stats.ContextCompactionLatestTriggerInputPercent),
		LatestCompactScopeActive:         stats.ContextCompactionLatestCompactScopeActive,
		LatestCompactWindowOrdinal:       stats.ContextCompactionLatestCompactWindowOrdinal,
		LatestCompactWindowPrefill:       int(stats.ContextCompactionLatestCompactWindowPrefill),
		LatestCompactWindowPrefillSource: stats.ContextCompactionLatestCompactWindowPrefillSource,
		LatestCompactScopedInputTokens:   int(stats.ContextCompactionLatestCompactScopedInputTokens),
		LatestCompactHardInputLimit:      int(stats.ContextCompactionLatestCompactHardInputLimit),
	}
}

func summarizeActiveCapabilities(s *Session, cfg Config) sessionCapabilities {
	hasTool := func(name string) bool {
		_, ok := s.registry.Get(name)
		return ok
	}
	focusedRegistered := hasTool(agent.FocusedTaskToolName)
	webSearch := hasTool("web_search")
	workspaceTools := activeWorkspaceTools(s.registry)
	caps := sessionCapabilities{
		EvalMode:              cfg.EvalMode,
		EvalTools:             strings.TrimSpace(cfg.EvalTools),
		EvalAllTools:          cfg.EvalAllTools,
		WorkspaceTools:        workspaceTools,
		Builtins:              hasAllWorkspaceTools(workspaceTools),
		SkillInstall:          hasTool("skill"),
		Plan:                  hasTool(agent.PlanToolName),
		LoopProtocol:          hasTool(agent.LoopProtocolToolName),
		SessionSchedule:       hasTool(agent.SessionScheduleToolName),
		SessionScheduleRunner: hasTool(agent.SessionScheduleToolName) && !cfg.EvalMode,
		Memory:                hasTool("memory"),
		SessionSearch:         hasTool("session_search"),
		SymbolContext:         hasTool("symbol_context"),
		RepoSearch:            hasTool("repo_search"),
		Browser:               hasTool("browser_navigate") || hasTool("browser_snapshot") || hasTool("browser_find") || hasTool("browser_network") || hasTool("browser_network_read"),
		BrowserScreenshot:     hasTool("browser_screenshot"),
		Web:                   hasTool("web_fetch"),
		WebSearch:             webSearch,
		Subagent:              hasTool(agent.SubagentToolName),
		SubagentMaxDepth:      cfg.SubagentMaxDepth,
		FocusedTasks:          focusedRegistered,
	}
	if webSearch {
		caps.WebSearchBackend = configuredSearchBackendName()
	}
	// Surface the available focused-task profiles whenever the tool
	// itself is registered. Computed via the same probe doctor uses
	// so the CLI diagnostic and the server API agree for matching
	// configurations.
	if focusedRegistered {
		caps.FocusedTaskProfiles = focusedTaskProfilesForLog(cfg)
	}
	return caps
}

func activeWorkspaceTools(reg *agent.Registry) []string {
	if reg == nil {
		return nil
	}
	names := serveEvalWorkspaceToolNames()
	out := make([]string, 0, len(names))
	for _, name := range names {
		if _, ok := reg.Get(name); ok {
			out = append(out, name)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func hasAllWorkspaceTools(names []string) bool {
	seen := make(map[string]bool, len(names))
	for _, name := range names {
		seen[name] = true
	}
	for _, name := range []string{"shell", "read_file", "write_file", "edit_file", "list_files"} {
		if !seen[name] {
			return false
		}
	}
	return true
}

func summarizeDurableSession(pool *SessionPool, id string) (sessionSummary, bool, error) {
	dir := pool.sessionDirPath(id)
	info, found, err := durableSessionDirInfo(dir)
	if err != nil {
		return sessionSummary{}, false, err
	}
	if !found {
		return sessionSummary{}, false, nil
	}
	summary := sessionSummary{
		ID:         id,
		Durable:    true,
		LastUsedAt: formatTime(info.ModTime()),
	}
	if meta, found, err := sessionstate.ReadMetadata(dir); err != nil {
		return sessionSummary{}, false, err
	} else if found && strings.TrimSpace(meta.WorkspacePath) != "" {
		summary.WorkspacePath = strings.TrimSpace(meta.WorkspacePath)
		summary.WorkspaceLabel = workspaceLabel(summary.WorkspacePath)
	}
	newest := info.ModTime()
	mergeStat := func(path string) (bool, error) {
		fi, err := os.Stat(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return false, nil
			}
			return false, err
		}
		if fi.ModTime().After(newest) {
			newest = fi.ModTime()
		}
		return true, nil
	}
	var exists bool
	var convMod time.Time
	if exists, convMod, err = durableRegularFileModTime(filepath.Join(dir, "conversation.jsonl")); err != nil {
		return sessionSummary{}, false, err
	}
	summary.HasConversation = exists
	if exists && convMod.After(newest) {
		newest = convMod
	}
	if exists {
		summary.LatestUserMessage, summary.TopicUserMessage, err = userMessageSummariesFromConversationFile(filepath.Join(dir, "conversation.jsonl"))
		if err != nil {
			return sessionSummary{}, false, err
		}
		recovery, err := latestRecoveryHintFromConversationFile(filepath.Join(dir, "conversation.jsonl"))
		if err != nil {
			return sessionSummary{}, false, err
		}
		summary.LatestRecoveryHint = recovery
	}
	var eventMod time.Time
	if exists, eventMod, err = durableRegularFileModTime(filepath.Join(dir, "events.jsonl")); err != nil {
		return sessionSummary{}, false, err
	}
	summary.HasEvents = exists
	if exists && eventMod.After(newest) {
		newest = eventMod
	}
	if exists {
		latest, topic, err := userMessageSummariesFromEventsFile(filepath.Join(dir, "events.jsonl"))
		if err != nil {
			return sessionSummary{}, false, err
		}
		if latest != "" {
			summary.LatestUserMessage = latest
		}
		if topic != "" {
			summary.TopicUserMessage = topic
		}
		compactions, err := contextCompactionSummaryFromEventsFile(filepath.Join(dir, "events.jsonl"))
		if err != nil {
			return sessionSummary{}, false, err
		}
		summary.ContextCompactions = compactions
		recovery, err := latestRecoveryHintFromEventsFile(filepath.Join(dir, "events.jsonl"))
		if err != nil {
			return sessionSummary{}, false, err
		}
		if recovery != "" {
			summary.LatestRecoveryHint = recovery
		}
		memoryUpdate, err := latestMemoryUpdateFromEventsFile(filepath.Join(dir, "events.jsonl"))
		if err != nil {
			return sessionSummary{}, false, err
		}
		summary.LatestMemoryUpdate = memoryUpdate
		usage, err := usageSummaryFromEventsFile(filepath.Join(dir, "events.jsonl"))
		if err != nil {
			return sessionSummary{}, false, err
		}
		summary.Usage = usage
		tools, err := toolStatsSummaryFromEventsFile(filepath.Join(dir, "events.jsonl"))
		if err != nil {
			return sessionSummary{}, false, err
		}
		summary.Tools = tools
		runtime, err := runtimeStatsSummaryFromEventsFile(filepath.Join(dir, "events.jsonl"))
		if err != nil {
			return sessionSummary{}, false, err
		}
		summary.Runtime = runtime
		cwd, _, err := latestShellCWDFromEventsFile(filepath.Join(dir, "events.jsonl"))
		if err != nil {
			return sessionSummary{}, false, err
		}
		summary.LastAgentCWD = cwd
	}
	var planMod time.Time
	if exists, planMod, err = durableRegularFileModTime(filepath.Join(dir, "plan.json")); err != nil {
		return sessionSummary{}, false, err
	}
	if exists {
		summary.PlanSummary = summarizeSessionPlanFile(pool, id)
		summary.HasPlan = summary.PlanSummary != nil
	}
	if summary.HasPlan && planMod.After(newest) {
		newest = planMod
	}
	var loopProtocolMod time.Time
	loopProtocolPath := sessionLoopProtocolPath(pool, id)
	if exists, loopProtocolMod, err = durableRegularFileModTime(loopProtocolPath); err != nil {
		return sessionSummary{}, false, err
	}
	if exists {
		summary.LoopProtocol = summarizeSessionLoopProtocolFile(pool, id)
		summary.HasLoopProtocol = summary.LoopProtocol != nil
	}
	if summary.HasLoopProtocol && loopProtocolMod.After(newest) {
		newest = loopProtocolMod
	}
	var loopStateMod time.Time
	loopStatePath := sessionLoopStatePath(pool, id)
	if exists, loopStateMod, err = durableRegularFileModTime(loopStatePath); err != nil {
		return sessionSummary{}, false, err
	}
	if exists {
		if state, found, err := loopstate.ReadState(loopStatePath); err != nil {
			return sessionSummary{}, false, err
		} else if found {
			summary.HasLoopState = true
			summary.LoopState = &state
			if summary.LatestMemoryUpdate == nil {
				summary.LatestMemoryUpdate = memoryUpdateFromLoopState(state)
			}
			if summary.LatestRecoveryHint == "" {
				summary.LatestRecoveryHint = recoveryHintFromLoopState(state)
			}
		}
	}
	if summary.HasLoopState && loopStateMod.After(newest) {
		newest = loopStateMod
	}
	var schedulesMod time.Time
	if exists, schedulesMod, err = durableRegularFileModTime(filepath.Join(dir, sessionSchedulesFileName)); err != nil {
		return sessionSummary{}, false, err
	}
	if exists {
		summary.Schedules = summarizeSessionSchedulesFile(pool, id)
		summary.HasSchedules = summary.Schedules != nil
	}
	if summary.HasSchedules && schedulesMod.After(newest) {
		newest = schedulesMod
	}
	artifactRoot := filepath.Join(dir, filepath.FromSlash(artifactPathPrefix))
	summary.Artifacts = summarizeSessionArtifactsDir(artifactRoot)
	summary.HasArtifacts = summary.Artifacts != nil && summary.Artifacts.Count > 0
	summary.RuntimeSkillNames = durableRuntimeSkillNames(agent.DefaultWorkspaceSkillDir(dir))
	summary.HasRuntimeSkills = len(summary.RuntimeSkillNames) > 0
	userMemoryPath := pool.userMemoryPath(dir)
	summary.HasMemory = durableMemoryExists(dir, userMemoryPath)
	summary.Memory = summarizeSessionMemory(pool.cfg.SharedUserMemory, dir, userMemoryPath)
	summary.HasMemory = summary.HasMemory || summary.Memory != nil
	if !sessionSummaryHasDurableState(summary) {
		return sessionSummary{}, false, nil
	}
	if err := populateSessionTaskState(&summary, filepath.Join(dir, "events.jsonl")); err != nil {
		return sessionSummary{}, false, err
	}
	if summary.HasArtifacts {
		_, _ = mergeStat(artifactRoot)
		if parsed, err := time.Parse(time.RFC3339, summary.Artifacts.LatestModTime); err == nil && parsed.After(newest) {
			newest = parsed
		}
	}
	if summary.HasRuntimeSkills {
		_, _ = mergeStat(agent.DefaultWorkspaceSkillDir(dir))
	}
	if summary.HasMemory {
		memoryStatPaths := []string{
			filepath.Join(dir, "core.md"),
			filepath.Join(dir, "topics"),
		}
		if !pool.cfg.SharedUserMemory {
			memoryStatPaths = append(memoryStatPaths, userMemoryPath)
		}
		for _, p := range memoryStatPaths {
			_, _ = mergeStat(p)
		}
	}
	summary.LastUsedAt = formatTime(newest)
	populateSessionSummaryTitle(&summary)
	return summary, true, nil
}

func sessionSummaryHasDurableState(summary sessionSummary) bool {
	return summary.HasConversation ||
		summary.HasEvents ||
		summary.HasPlan ||
		summary.HasLoopProtocol ||
		summary.HasLoopState ||
		summary.HasSchedules ||
		summary.HasArtifacts ||
		summary.HasMemory ||
		summary.HasRuntimeSkills
}

func summarizeSessionArtifactsDir(root string) *sessionArtifactsSummary {
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil
	}
	dir, err := os.Open(root)
	if err != nil {
		return nil
	}
	defer dir.Close()
	var summary sessionArtifactsSummary
	var latest time.Time
	for {
		entries, err := dir.ReadDir(artifactReadDirBatch)
		if err != nil && !errors.Is(err, io.EOF) {
			return nil
		}
		for _, ent := range entries {
			if ent.IsDir() || durableDirEntryIsSymlink(ent) {
				continue
			}
			info, err := ent.Info()
			if err != nil || info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				continue
			}
			rel := path.Join(artifactPathPrefix, ent.Name())
			summary.Count++
			summary.TotalBytes += info.Size()
			mod := info.ModTime()
			if summary.LatestPath == "" || mod.After(latest) || (mod.Equal(latest) && rel > summary.LatestPath) {
				latest = mod
				summary.LatestPath = rel
				summary.LatestModTime = mod.UTC().Format(time.RFC3339)
			}
		}
		if errors.Is(err, io.EOF) {
			break
		}
	}
	if summary.Count == 0 {
		return nil
	}
	return &summary
}

func summarizeSessionMemory(sharedUserMemory bool, sessionDir, userMemoryPath string) *sessionMemorySummary {
	summary := sessionMemorySummary{SharedUserMemory: sharedUserMemory}
	memoryBuckets := 0
	addTopic := func(target memory.MemoryTarget, topic string, path string) {
		entries, chars, newest := readMemoryBucketSummary(path)
		if entries <= 0 && chars <= 0 {
			return
		}
		if target == memory.TargetMemory {
			memoryBuckets++
		}
		summary.BucketCount++
		summary.EntryCount += entries
		summary.CharsUsed += chars
		if newest != "" && (summary.LatestAt == "" || newest > summary.LatestAt) {
			summary.LatestAt = newest
			summary.LatestTarget = string(target)
			summary.LatestTopic = topic
		}
	}
	addTopic(memory.TargetUser, "user", userMemoryPath)
	addTopic(memory.TargetMemory, memory.CoreTopic, filepath.Join(sessionDir, "core.md"))
	for _, topic := range sessionMemoryTopicFiles(filepath.Join(sessionDir, "topics"), maxSessionMemoryTopics) {
		addTopic(memory.TargetMemory, strings.TrimSuffix(topic.name, ".md"), topic.path)
	}
	if memoryBuckets == 0 {
		addTopic(memory.TargetMemory, memory.DefaultTopic, filepath.Join(sessionDir, "MEMORY.md"))
	}
	if summary.BucketCount == 0 {
		return nil
	}
	return &summary
}

type sessionMemoryTopicFile struct {
	name string
	path string
}

func sessionMemoryTopicFiles(dir string, limit int) []sessionMemoryTopicFile {
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil
	}
	f, err := os.Open(dir)
	if err != nil {
		return nil
	}
	defer f.Close()
	var files []sessionMemoryTopicFile
	for {
		entries, err := f.ReadDir(sessionReadDirBatch)
		if err != nil && !errors.Is(err, io.EOF) {
			return nil
		}
		for _, ent := range entries {
			if limit > 0 && len(files) >= limit {
				sort.Slice(files, func(i, j int) bool { return files[i].name < files[j].name })
				return files
			}
			if ent.IsDir() || durableDirEntryIsSymlink(ent) || !strings.HasSuffix(ent.Name(), ".md") {
				continue
			}
			files = append(files, sessionMemoryTopicFile{name: ent.Name(), path: filepath.Join(dir, ent.Name())})
		}
		if errors.Is(err, io.EOF) {
			break
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].name < files[j].name })
	return files
}

func readMemoryBucketSummary(path string) (entries int, chars int, newest string) {
	rawEntries := readMemoryEntriesForSummary(path)
	for _, entry := range rawEntries {
		ts, content := splitMemoryEntryForSummary(entry)
		if content == "" {
			continue
		}
		if entries > 0 {
			chars += len("\n§\n")
		}
		entries++
		chars += len(content)
		if ts > newest {
			newest = ts
		}
	}
	return entries, chars, newest
}

func readMemoryEntriesForSummary(path string) []string {
	info, err := os.Lstat(path)
	if err != nil || info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, memory.MaxMemoryFileBytes+1))
	if err != nil || len(raw) > memory.MaxMemoryFileBytes {
		return nil
	}
	text := strings.TrimSpace(string(raw))
	if text == "" {
		return nil
	}
	parts := strings.Split(text, "\n§\n")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func splitMemoryEntryForSummary(raw string) (string, string) {
	raw = strings.TrimSpace(raw)
	if len(raw) >= len("[2006-01-02T15:04:05Z]\n") && strings.HasPrefix(raw, "[") {
		if end := strings.Index(raw, "]\n"); end > 0 {
			ts := raw[1:end]
			if _, err := time.Parse(time.RFC3339, ts); err == nil {
				return ts, strings.TrimSpace(raw[end+2:])
			}
		}
	}
	return "", raw
}

func mergeSessionSummaries(a, b sessionSummary) sessionSummary {
	if a.ID == "" {
		a.ID = b.ID
	}
	aWasActive := a.Active
	a.Active = a.Active || b.Active
	a.Durable = a.Durable || b.Durable
	a.HasConversation = a.HasConversation || b.HasConversation
	if a.LatestUserMessage == "" && b.LatestUserMessage != "" {
		a.LatestUserMessage = b.LatestUserMessage
	}
	if a.TopicUserMessage == "" && b.TopicUserMessage != "" {
		a.TopicUserMessage = b.TopicUserMessage
	} else if b.TopicUserMessage != "" && isContinuationSessionPrompt(a.TopicUserMessage) {
		a.TopicUserMessage = b.TopicUserMessage
	}
	if b.LatestRecoveryHint != "" {
		a.LatestRecoveryHint = b.LatestRecoveryHint
	}
	if b.LatestMemoryUpdate != nil {
		a.LatestMemoryUpdate = b.LatestMemoryUpdate
	}
	if b.Memory != nil {
		a.Memory = b.Memory
	}
	if a.SummaryTitle == "" && b.SummaryTitle != "" {
		a.SummaryTitle = b.SummaryTitle
	}
	a.HasEvents = a.HasEvents || b.HasEvents
	a.HasPlan = a.HasPlan || b.HasPlan
	if b.PlanSummary != nil {
		a.PlanSummary = b.PlanSummary
	}
	a.HasLoopProtocol = a.HasLoopProtocol || b.HasLoopProtocol
	if b.LoopProtocol != nil {
		a.LoopProtocol = b.LoopProtocol
	}
	a.HasLoopState = a.HasLoopState || b.HasLoopState
	if b.LoopState != nil {
		a.LoopState = b.LoopState
	}
	a.HasSchedules = a.HasSchedules || b.HasSchedules
	if b.Schedules != nil {
		a.Schedules = b.Schedules
	}
	a.HasArtifacts = a.HasArtifacts || b.HasArtifacts
	if b.Artifacts != nil {
		a.Artifacts = b.Artifacts
	}
	a.HasMemory = a.HasMemory || b.HasMemory
	a.HasRuntimeSkills = a.HasRuntimeSkills || b.HasRuntimeSkills
	a.RuntimeSkillNames = mergeStringLists(a.RuntimeSkillNames, b.RuntimeSkillNames)
	if b.Context != nil {
		a.Context = b.Context
	}
	if b.ContextCompactions != nil {
		a.ContextCompactions = b.ContextCompactions
	}
	if a.CreatedAt == "" {
		a.CreatedAt = b.CreatedAt
	}
	a.LastUsedAt = newerFormattedTime(a.LastUsedAt, b.LastUsedAt)
	if a.WorkspacePath == "" && b.WorkspacePath != "" {
		a.WorkspacePath = b.WorkspacePath
	}
	if a.WorkspaceLabel == "" && b.WorkspaceLabel != "" {
		a.WorkspaceLabel = b.WorkspaceLabel
	}
	if a.DefaultBranch == "" && b.DefaultBranch != "" {
		a.DefaultBranch = b.DefaultBranch
	}
	if a.DirtyState == "" && b.DirtyState != "" {
		a.DirtyState = b.DirtyState
	}
	if a.LastAgentCWD == "" && b.LastAgentCWD != "" {
		a.LastAgentCWD = b.LastAgentCWD
	}
	if shouldReplaceUsageSummary(a.Usage, b.Usage, b.Active, aWasActive) {
		a.Usage = b.Usage
	}
	if shouldReplaceToolSummary(a.Tools, b.Tools, b.Active, aWasActive) {
		a.Tools = b.Tools
	}
	if shouldReplaceRuntimeSummary(a.Runtime, b.Runtime, b.Active, aWasActive) {
		a.Runtime = b.Runtime
	}
	if shouldReplaceTaskStateSummary(a.TaskState, b.TaskState, b.Active, aWasActive) {
		a.TaskState = b.TaskState
	}
	if b.Browser != nil {
		a.Browser = b.Browser
	}
	if b.Capabilities != nil {
		a.Capabilities = b.Capabilities
	}
	return a
}

func shouldReplaceUsageSummary(existing, incoming *UsageSnapshot, incomingActive bool, existingWasActive bool) bool {
	if incoming == nil {
		return false
	}
	if existing == nil {
		return true
	}
	return shouldReplaceStatsSnapshot(usageSnapshotEvidence(existing), usageSnapshotEvidence(incoming), incomingActive, existingWasActive)
}

func shouldReplaceToolSummary(existing, incoming *ToolStatsSnapshot, incomingActive bool, existingWasActive bool) bool {
	if incoming == nil {
		return false
	}
	if existing == nil {
		return true
	}
	return shouldReplaceStatsSnapshot(toolStatsSnapshotEvidence(existing), toolStatsSnapshotEvidence(incoming), incomingActive, existingWasActive)
}

func shouldReplaceRuntimeSummary(existing, incoming *RuntimeStatsSnapshot, incomingActive bool, existingWasActive bool) bool {
	if incoming == nil {
		return false
	}
	if existing == nil {
		return true
	}
	return shouldReplaceStatsSnapshot(runtimeStatsSnapshotEvidence(existing), runtimeStatsSnapshotEvidence(incoming), incomingActive, existingWasActive)
}

func shouldReplaceStatsSnapshot(existingEvidence, incomingEvidence int64, incomingActive bool, existingWasActive bool) bool {
	if incomingEvidence > existingEvidence {
		return true
	}
	if incomingEvidence < existingEvidence {
		return false
	}
	return incomingActive && !existingWasActive && incomingEvidence > 0
}

func shouldReplaceTaskStateSummary(existing, incoming *sessionTaskStateSummary, incomingActive bool, existingWasActive bool) bool {
	if incoming == nil {
		return false
	}
	if existing == nil {
		return true
	}
	return shouldReplaceStatsSnapshot(taskStateSummaryEvidence(existing), taskStateSummaryEvidence(incoming), incomingActive, existingWasActive)
}

func taskStateSummaryEvidence(s *sessionTaskStateSummary) int64 {
	if s == nil {
		return 0
	}
	var total int64
	if s.Objective != "" {
		total++
	}
	if s.Status != "" && s.Status != "unknown" {
		total++
	}
	if s.CurrentStep != "" {
		total++
	}
	if s.RequestMode != "" {
		total++
	}
	if s.RequestSource != "" {
		total++
	}
	if s.ScheduleID != "" {
		total++
	}
	if s.ScheduleKind != "" {
		total++
	}
	if s.NextStep != "" {
		total++
	}
	if s.VerificationState != "" && s.VerificationState != "unknown" {
		total++
	}
	return total +
		int64(len(s.Constraints)) +
		int64(len(s.KnownFacts)) +
		int64(len(s.ChangedFiles)) +
		int64(len(s.AttemptedActions)) +
		int64(len(s.FailedActions)) +
		int64(len(s.Evidence)) +
		int64(len(s.OpenQuestions)) +
		int64(len(s.Sources))
}

func usageSnapshotEvidence(s *UsageSnapshot) int64 {
	if s == nil {
		return 0
	}
	return positiveInt64(s.InputTokens) + positiveInt64(s.OutputTokens) + positiveInt64(s.Turns)
}

func toolStatsSnapshotEvidence(s *ToolStatsSnapshot) int64 {
	if s == nil {
		return 0
	}
	total := positiveInt64(s.ToolRequests) +
		positiveInt64(s.ToolNameCanonicalized) +
		positiveInt64(s.ToolArgsRepaired) +
		positiveInt64(s.ToolRepairCalls) +
		positiveInt64(s.ToolRepairSucceeded) +
		positiveInt64(s.ToolRepairFailed) +
		positiveInt64(s.ToolRepairNotes) +
		positiveInt64(s.ToolErrors) +
		positiveInt64(s.ToolDurationMS) +
		positiveInt64(s.LoopGuardInterventions) +
		positiveInt64(s.ForcedNoTools) +
		positiveInt64(s.SourceAccessResults) +
		positiveInt64(s.SourceAccessVerified) +
		positiveInt64(s.SourceAccessDiscovery) +
		positiveInt64(s.SourceAccessNetwork) +
		positiveInt64(s.SourceAccessDynamic) +
		positiveInt64(s.MemoryUpdates) +
		positiveInt64(s.MemoryUpdateAdd) +
		positiveInt64(s.MemoryUpdateReplace) +
		positiveInt64(s.MemoryUpdateRemove) +
		positiveInt64(s.MemorySearchCalls) +
		positiveInt64(s.MemorySearchMisses) +
		positiveInt64(s.SessionSearchCalls) +
		positiveInt64(s.SessionSearchResults) +
		positiveInt64(s.SessionSearchContext) +
		positiveInt64(s.SessionSearchTerms) +
		positiveInt64(s.SessionSearchRecent) +
		positiveInt64(s.ToolContextTruncated) +
		positiveInt64(s.ToolContextOmitted) +
		positiveInt64(s.PlanCalls) +
		positiveInt64(s.PlanErrors) +
		positiveInt64(s.FocusedTaskCalls) +
		positiveInt64(s.FocusedTaskErrors) +
		positiveInt64(s.SubagentCalls) +
		positiveInt64(s.SubagentErrors)
	for _, count := range s.ToolRepairByKind {
		total += positiveInt64(count)
	}
	for _, count := range s.ToolFailureByKind {
		total += positiveInt64(count)
	}
	for _, count := range s.PlanByAction {
		total += positiveInt64(count)
	}
	for _, count := range s.FocusedTaskByType {
		total += positiveInt64(count)
	}
	for _, count := range s.SubagentByMode {
		total += positiveInt64(count)
	}
	return total
}

func runtimeStatsSnapshotEvidence(s *RuntimeStatsSnapshot) int64 {
	if s == nil {
		return 0
	}
	total := positiveInt64(s.RuntimeErrors) +
		positiveInt64(s.ContextCompactions) +
		positiveInt64(s.ContextCompactionsReactive) +
		positiveInt64(s.ContextCompactionRemovedMessages) +
		positiveInt64(s.ContextCompactionSummaryBytes) +
		positiveInt64(s.ContextCompactionSummaryMissing) +
		positiveInt64(s.ContextCompactionSummaryEmpty) +
		positiveInt64(s.ContextCompactionLatestEstimatedInputTokens) +
		positiveInt64(s.ContextCompactionLatestAfterEstimatedInputTokens) +
		positiveInt64(s.ContextCompactionLatestTriggerInputTokens) +
		positiveInt64(s.ContextCompactionLatestModelContextWindowTokens) +
		positiveInt64(s.ContextCompactionLatestReservedOutputTokens) +
		positiveInt64(s.ContextCompactionLatestTriggerInputPercent) +
		positiveInt64(s.ContextCompactionLatestCompactWindowOrdinal) +
		positiveInt64(s.ContextCompactionLatestCompactWindowPrefill) +
		positiveInt64(s.ContextCompactionLatestCompactScopedInputTokens) +
		positiveInt64(s.ContextCompactionLatestCompactHardInputLimit)
	for _, count := range s.TurnEndByReason {
		total += positiveInt64(count)
	}
	for _, count := range s.RuntimeErrorByKind {
		total += positiveInt64(count)
	}
	for _, count := range s.RuntimeSurfaceRefreshByReason {
		total += positiveInt64(count)
	}
	if s.ContextCompactionLatestReason != "" {
		total++
	}
	if s.ContextCompactionLatestState != "" {
		total++
	}
	if s.ContextCompactionLatestCompactScopeActive {
		total++
	}
	if s.ContextCompactionLatestCompactWindowPrefillSource != "" {
		total++
	}
	if s.RuntimeSurfaceLatestRefreshReason != "" {
		total++
	}
	return total
}

func positiveInt64(v int64) int64 {
	if v > 0 {
		return v
	}
	return 0
}

func workspaceLabel(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if base := filepath.Base(path); base != "." && base != string(filepath.Separator) {
		return base
	}
	return path
}

func sessionContextSnapshot(messageCount int, inputEstimate agent.RequestInputEstimate, cfg Config) sessionContextSummary {
	trigger := cfg.CompactTrigger
	if trigger <= 0 {
		trigger = agent.DefaultSummaryTriggerMsgs
	}
	remaining := trigger - messageCount
	if remaining < 0 {
		remaining = 0
	}
	messagePercent := 0
	if trigger > 0 {
		messagePercent = (messageCount*100 + trigger/2) / trigger
	}
	contextBytes := inputEstimate.ConversationBytes
	estimatedRequestInputTokens := inputEstimate.EstimatedInputTokens
	byteTrigger := compactTriggerBytesForConfig(cfg)
	bytesUntilCompact := byteTrigger - contextBytes
	if bytesUntilCompact < 0 {
		bytesUntilCompact = 0
	}
	bytePercent := 0
	if byteTrigger > 0 && contextBytes > 0 {
		bytePercent = (contextBytes*100 + byteTrigger/2) / byteTrigger
	}
	inputTrigger := compactTriggerInputTokensForConfig(cfg)
	inputTokensUntilCompact := inputTrigger - estimatedRequestInputTokens
	if inputTokensUntilCompact < 0 {
		inputTokensUntilCompact = 0
	}
	inputPercent := 0
	if inputTrigger > 0 && estimatedRequestInputTokens > 0 {
		inputPercent = (estimatedRequestInputTokens*100 + inputTrigger/2) / inputTrigger
	}
	percent := messagePercent
	if bytePercent > percent {
		percent = bytePercent
	}
	if inputPercent > percent {
		percent = inputPercent
	}
	return sessionContextSummary{
		MessageCount:                       messageCount,
		CompactTrigger:                     trigger,
		CompactPercent:                     percent,
		MessagesUntilCompact:               remaining,
		ContextBytes:                       contextBytes,
		ConversationBytes:                  inputEstimate.ConversationBytes,
		ToolSchemaBytes:                    inputEstimate.ToolSchemaBytes,
		CompactTriggerBytes:                byteTrigger,
		ByteCompactPercent:                 bytePercent,
		BytesUntilCompact:                  bytesUntilCompact,
		MessageCompactPercent:              messagePercent,
		EstimatedRequestInputTokens:        estimatedRequestInputTokens,
		EstimatedConversationTokens:        inputEstimate.ConversationTokens,
		EstimatedToolSchemaTokens:          inputEstimate.ToolSchemaTokens,
		ModelContextWindowTokens:           cfg.ModelContextWindowTokens,
		ModelContextWindowAuto:             cfg.ModelContextWindowAuto,
		ModelContextWindowSource:           modelContextWindowSourceForConfig(cfg),
		ModelContextWindowEffectivePercent: cfg.ModelContextWindowEffectivePercent,
		ReservedOutputTokens:               reservedOutputTokensForConfig(cfg),
		CompactTriggerInputPercent:         compactTriggerInputPercentForConfig(cfg),
		CompactTriggerInputTokens:          inputTrigger,
		CompactSummaryPromptMaxBytes:       compactSummaryPromptMaxBytesForConfig(cfg),
		RequestInputCompactPercent:         inputPercent,
		RequestInputTokensUntilCompact:     inputTokensUntilCompact,
	}
}

func sessionToolSchemaBudgetTokens(messages []agent.ChatMessage, cfg Config) int {
	conversationTokens := agent.EstimateRequestInput(messages, nil).ConversationTokens
	return agent.ToolSchemaBudgetTokensForRequestPolicy(compactTriggerInputTokensForConfig(cfg), conversationTokens)
}

func compactTriggerInputTokensForConfig(cfg Config) int {
	return agent.CompactTriggerInputTokensForModelPolicy(cfg.CompactTriggerInputTokens, cfg.ModelContextWindowTokens, cfg.CompactTriggerInputPercent, reservedOutputTokensForConfig(cfg), agent.DefaultSummaryTriggerInputTokens)
}

func compactTriggerBytesForConfig(cfg Config) int {
	if cfg.ModelContextWindowTokens > 0 && cfg.CompactTriggerInputTokens == 0 {
		return agent.CompactTriggerBytesForModelPolicy(0, cfg.ModelContextWindowTokens, cfg.CompactTriggerInputPercent, reservedOutputTokensForConfig(cfg), agent.DefaultSummaryTriggerBytes)
	}
	return agent.DefaultSummaryTriggerBytes
}

func compactSummaryPromptMaxBytesForConfig(cfg Config) int {
	return agent.SummaryPromptMaxBytesForModelPolicy(cfg.ModelContextWindowTokens, cfg.CompactTriggerInputPercent, reservedOutputTokensForConfig(cfg), agent.DefaultSummaryPromptMaxBytes)
}

func modelContextWindowSourceForConfig(cfg Config) string {
	if cfg.ModelContextWindowTokens <= 0 {
		return ""
	}
	source := strings.TrimSpace(cfg.ModelContextWindowSource)
	if source != "" {
		return source
	}
	return "explicit"
}

func reservedOutputTokensForConfig(cfg Config) int {
	if cfg.MaxTokens == nil || *cfg.MaxTokens <= 0 {
		return 0
	}
	return *cfg.MaxTokens
}

func compactTriggerInputPercentForConfig(cfg Config) int {
	if cfg.CompactTriggerInputPercent > 0 {
		return cfg.CompactTriggerInputPercent
	}
	return agent.DefaultCompactTriggerInputPercent
}

func durableSessionDirInfo(path string) (os.FileInfo, bool, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, err
	}
	if !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
		return nil, false, nil
	}
	return fi, true, nil
}

func latestUserMessageFromConversationFile(path string) (string, error) {
	latest, _, err := userMessageSummariesFromConversationFile(path)
	return latest, err
}

func userMessageSummariesFromConversationFile(path string) (string, string, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", "", nil
		}
		return "", "", err
	}
	defer f.Close()
	if err := seekSessionSummaryTail(f); err != nil {
		return "", "", err
	}

	var latest string
	var topic string
	r := bufio.NewReaderSize(f, 64*1024)
	for {
		line, tooLong, err := jsonl.ReadBoundedLine(r, maxSessionSummaryLineBytes)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", "", err
		}
		if tooLong {
			continue
		}
		line = bytes.TrimRight(line, "\r\n")
		var msg agent.ChatMessage
		if err := json.Unmarshal(line, &msg); err != nil {
			continue
		}
		if msg.Role != "user" {
			continue
		}
		if summary := summarizeLatestUserMessage(msg.Content); summary != "" {
			latest = summary
			if !isContinuationSessionPrompt(summary) {
				topic = summary
			}
		}
	}
	if topic == "" {
		topic = latest
	}
	return latest, topic, nil
}

func latestRecoveryHintFromConversationFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	defer f.Close()
	if err := seekSessionSummaryTail(f); err != nil {
		return "", err
	}

	latest := ""
	r := bufio.NewReaderSize(f, 64*1024)
	for {
		line, tooLong, err := jsonl.ReadBoundedLine(r, maxSessionSummaryLineBytes)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", err
		}
		if tooLong {
			continue
		}
		line = bytes.TrimRight(line, "\r\n")
		var msg agent.ChatMessage
		if err := json.Unmarshal(line, &msg); err != nil {
			continue
		}
		if hint := recoveryHintFromConversationMessage(msg); hint != "" {
			latest = hint
		}
	}
	return latest, nil
}

type sessionUserMessageScan struct {
	Latest string
	Topic  string
}

func userMessageSummariesFromEventsFile(path string) (string, string, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", "", nil
		}
		return "", "", err
	}
	defer f.Close()

	head, err := scanUserMessagesFromEvents(bufio.NewReaderSize(io.LimitReader(f, maxSessionEventSummaryHead), 64*1024))
	if err != nil {
		return "", "", err
	}
	if err := seekSessionSummaryTail(f); err != nil {
		return "", "", err
	}
	tail, err := scanUserMessagesFromEvents(bufio.NewReaderSize(f, 64*1024))
	if err != nil {
		return "", "", err
	}

	latest := tail.Latest
	if latest == "" {
		latest = head.Latest
	}
	topic := head.Topic
	if topic == "" {
		topic = tail.Topic
	}
	if topic == "" {
		topic = latest
	}
	return latest, topic, nil
}

func scanUserMessagesFromEvents(r *bufio.Reader) (sessionUserMessageScan, error) {
	var scan sessionUserMessageScan
	for {
		line, tooLong, err := jsonl.ReadBoundedLine(r, maxSessionSummaryLineBytes)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return sessionUserMessageScan{}, err
		}
		if tooLong {
			continue
		}
		line = bytes.TrimRight(line, "\r\n")
		var ev sse.Event
		if err := json.Unmarshal(line, &ev); err != nil || ev.Type != sse.TypeUserMessage {
			continue
		}
		var p sse.UserMessagePayload
		if err := json.Unmarshal(ev.Data, &p); err != nil {
			continue
		}
		summary := summarizeLatestUserMessage(firstNonEmpty(p.DisplayText, p.Text))
		if summary == "" {
			continue
		}
		scan.Latest = summary
		if scan.Topic == "" && !isContinuationSessionPrompt(summary) {
			scan.Topic = summary
		}
	}
	return scan, nil
}

func contextCompactionSummaryFromEventsFile(path string) (*sessionContextCompactionSummary, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	tailOnly := info.Size() > maxSessionSummaryTailBytes
	if err := seekSessionSummaryTail(f); err != nil {
		return nil, err
	}
	summary, err := scanContextCompactionsFromEvents(bufio.NewReaderSize(f, 64*1024))
	if err != nil {
		return nil, err
	}
	if summary.Count == 0 {
		return nil, nil
	}
	summary.TailOnly = tailOnly
	return &summary, nil
}

func scanContextCompactionsFromEvents(r *bufio.Reader) (sessionContextCompactionSummary, error) {
	var summary sessionContextCompactionSummary
	for {
		line, tooLong, err := jsonl.ReadBoundedLine(r, maxSessionSummaryLineBytes)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return sessionContextCompactionSummary{}, err
		}
		if tooLong {
			continue
		}
		line = bytes.TrimRight(line, "\r\n")
		var ev sse.Event
		if err := json.Unmarshal(line, &ev); err != nil || ev.Type != sse.TypeContextCompact {
			continue
		}
		var p sse.ContextCompactPayload
		if err := json.Unmarshal(ev.Data, &p); err != nil {
			continue
		}
		var raw struct {
			SummaryPresent *bool `json:"summary_present"`
		}
		_ = json.Unmarshal(ev.Data, &raw)
		summary.Count++
		if p.Reactive {
			summary.Reactive++
		}
		summary.RemovedMessages += p.RemovedMessages
		summary.SummaryBytes += p.SummaryBytes
		summary.LatestReason = p.Reason
		summary.LatestReactive = p.Reactive
		summary.LatestEstimatedInputTokens = p.EstimatedInputTokens
		summary.LatestAfterEstimatedInputTokens = p.AfterEstimatedInputTokens
		summary.LatestTriggerInputTokens = p.TriggerInputTokens
		summary.LatestModelContextWindowTokens = p.ModelContextWindowTokens
		summary.LatestModelContextWindowSource = p.ModelContextWindowSource
		summary.LatestReservedOutputTokens = p.ReservedOutputTokens
		summary.LatestTriggerInputPercent = p.CompactTriggerInputPercent
		summary.LatestCompactScopeActive = p.CompactScopeActive
		summary.LatestCompactWindowOrdinal = p.CompactWindowOrdinal
		summary.LatestCompactWindowPrefill = p.CompactWindowPrefillInputTokens
		summary.LatestCompactWindowPrefillSource = p.CompactWindowPrefillSource
		summary.LatestCompactScopedInputTokens = p.CompactScopedInputTokens
		summary.LatestCompactHardInputLimit = p.CompactHardInputLimitTokens
		state := contextCompactSummaryState(p.SummaryPresent, p.SummaryBytes, p.SummaryPreview, raw.SummaryPresent != nil)
		switch state {
		case "missing":
			summary.SummaryMissing++
		case "empty":
			summary.SummaryEmpty++
		}
		summary.LatestSummaryState = state
	}
	return summary, nil
}

var sessionRecoveryNextRe = regexp.MustCompile(`(?m)(?:^|\n)Next:\s*([\s\S]*?)(?:\nFailure:|\n[A-Z][A-Za-z _-]{0,40}:|$)`)

func latestRecoveryHintFromEventsFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	defer f.Close()
	if err := seekSessionSummaryTail(f); err != nil {
		return "", err
	}
	return scanRecoveryHintsFromEvents(bufio.NewReaderSize(f, 64*1024))
}

func latestMemoryUpdateFromEventsFile(path string) (*sse.MemoryUpdateMeta, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	if err := seekSessionSummaryTail(f); err != nil {
		return nil, err
	}
	return scanMemoryUpdatesFromEvents(bufio.NewReaderSize(f, 64*1024))
}

func latestShellCWDFromEventsFile(path string) (string, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", false, nil
		}
		return "", false, err
	}
	defer f.Close()
	if err := seekSessionSummaryTail(f); err != nil {
		return "", false, err
	}
	return scanShellCWDFromEvents(bufio.NewReaderSize(f, 64*1024))
}

func usageSummaryFromEventsFile(path string) (*UsageSnapshot, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	if err := seekSessionSummaryTail(f); err != nil {
		return nil, err
	}
	return scanUsageFromEvents(bufio.NewReaderSize(f, 64*1024))
}

func scanUsageFromEvents(r *bufio.Reader) (*UsageSnapshot, error) {
	var summary UsageSnapshot
	seen := false
	for {
		line, tooLong, err := jsonl.ReadBoundedLine(r, maxSessionSummaryLineBytes)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if tooLong {
			continue
		}
		line = bytes.TrimRight(line, "\r\n")
		var ev sse.Event
		if err := json.Unmarshal(line, &ev); err != nil {
			continue
		}
		switch ev.Type {
		case sse.TypeUsage:
			var p sse.UsagePayload
			if err := json.Unmarshal(ev.Data, &p); err != nil {
				continue
			}
			if p.InputTokens > 0 {
				summary.InputTokens += int64(p.InputTokens)
			}
			if p.OutputTokens > 0 {
				summary.OutputTokens += int64(p.OutputTokens)
			}
			seen = true
		case sse.TypeTurnEnd:
			summary.Turns++
			seen = true
		}
	}
	if !seen {
		return nil, nil
	}
	return &summary, nil
}

func toolStatsSummaryFromEventsFile(path string) (*ToolStatsSnapshot, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	if err := seekSessionSummaryTail(f); err != nil {
		return nil, err
	}
	return scanToolStatsFromEvents(bufio.NewReaderSize(f, 64*1024))
}

func runtimeStatsSummaryFromEventsFile(path string) (*RuntimeStatsSnapshot, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	if err := seekSessionSummaryTail(f); err != nil {
		return nil, err
	}
	return scanRuntimeStatsFromEvents(bufio.NewReaderSize(f, 64*1024))
}

func scanRuntimeStatsFromEvents(r *bufio.Reader) (*RuntimeStatsSnapshot, error) {
	var summary RuntimeStatsSnapshot
	seen := false
	for {
		line, tooLong, err := jsonl.ReadBoundedLine(r, maxSessionSummaryLineBytes)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if tooLong {
			continue
		}
		line = bytes.TrimRight(line, "\r\n")
		var ev sse.Event
		if err := json.Unmarshal(line, &ev); err != nil {
			continue
		}
		switch ev.Type {
		case sse.TypeTurnEnd:
			var p sse.TurnEndPayload
			if err := json.Unmarshal(ev.Data, &p); err != nil {
				continue
			}
			addRuntimeTurnEndReason(&summary, p.Reason)
			seen = true
		case sse.TypeError:
			var p sse.ErrorPayload
			if err := json.Unmarshal(ev.Data, &p); err != nil {
				continue
			}
			addRuntimeErrorKind(&summary, p.FailureKind)
			seen = true
		case sse.TypeContextCompact:
			var p sse.ContextCompactPayload
			if err := json.Unmarshal(ev.Data, &p); err != nil {
				continue
			}
			var raw struct {
				SummaryPresent *bool `json:"summary_present"`
			}
			_ = json.Unmarshal(ev.Data, &raw)
			addRuntimeContextCompaction(&summary, p, raw.SummaryPresent != nil)
			seen = true
		case sse.TypeRuntimeSurface:
			var p sse.RuntimeSurfacePayload
			if err := json.Unmarshal(ev.Data, &p); err != nil {
				continue
			}
			if addRuntimeSurfaceRefreshReason(&summary, p.RefreshReason) {
				seen = true
			}
			if addRuntimeSurfaceCompactWindow(&summary, p) {
				seen = true
			}
		}
	}
	if !seen {
		return nil, nil
	}
	return &summary, nil
}

func addRuntimeTurnEndReason(summary *RuntimeStatsSnapshot, reason string) {
	if reason == "" {
		reason = "unknown"
	}
	if summary.TurnEndByReason == nil {
		summary.TurnEndByReason = map[string]int64{}
	}
	summary.TurnEndByReason[reason]++
}

func addRuntimeErrorKind(summary *RuntimeStatsSnapshot, kind string) {
	summary.RuntimeErrors++
	if kind == "" {
		return
	}
	if summary.RuntimeErrorByKind == nil {
		summary.RuntimeErrorByKind = map[string]int64{}
	}
	summary.RuntimeErrorByKind[kind]++
}

func addRuntimeSurfaceRefreshReason(summary *RuntimeStatsSnapshot, reason string) bool {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return false
	}
	if summary.RuntimeSurfaceRefreshByReason == nil {
		summary.RuntimeSurfaceRefreshByReason = map[string]int64{}
	}
	summary.RuntimeSurfaceRefreshByReason[reason]++
	summary.RuntimeSurfaceLatestRefreshReason = reason
	return true
}

func addRuntimeContextCompaction(summary *RuntimeStatsSnapshot, p sse.ContextCompactPayload, summaryPresentKnown bool) {
	summary.ContextCompactions++
	if p.Reactive {
		summary.ContextCompactionsReactive++
	}
	summary.ContextCompactionRemovedMessages += int64(p.RemovedMessages)
	summary.ContextCompactionSummaryBytes += int64(p.SummaryBytes)
	state := contextCompactSummaryState(p.SummaryPresent, p.SummaryBytes, p.SummaryPreview, summaryPresentKnown)
	switch state {
	case "missing":
		summary.ContextCompactionSummaryMissing++
	case "empty":
		summary.ContextCompactionSummaryEmpty++
	}
	summary.ContextCompactionLatestReason = p.Reason
	summary.ContextCompactionLatestReactive = p.Reactive
	summary.ContextCompactionLatestState = state
	summary.ContextCompactionLatestEstimatedInputTokens = int64(p.EstimatedInputTokens)
	summary.ContextCompactionLatestAfterEstimatedInputTokens = int64(p.AfterEstimatedInputTokens)
	summary.ContextCompactionLatestTriggerInputTokens = int64(p.TriggerInputTokens)
	summary.ContextCompactionLatestModelContextWindowTokens = int64(p.ModelContextWindowTokens)
	summary.ContextCompactionLatestModelContextWindowSource = p.ModelContextWindowSource
	summary.ContextCompactionLatestReservedOutputTokens = int64(p.ReservedOutputTokens)
	summary.ContextCompactionLatestTriggerInputPercent = int64(p.CompactTriggerInputPercent)
	summary.ContextCompactionLatestCompactScopeActive = p.CompactScopeActive
	summary.ContextCompactionLatestCompactWindowOrdinal = p.CompactWindowOrdinal
	summary.ContextCompactionLatestCompactWindowPrefill = int64(p.CompactWindowPrefillInputTokens)
	summary.ContextCompactionLatestCompactWindowPrefillSource = p.CompactWindowPrefillSource
	summary.ContextCompactionLatestCompactScopedInputTokens = int64(p.CompactScopedInputTokens)
	summary.ContextCompactionLatestCompactHardInputLimit = int64(p.CompactHardInputLimitTokens)
}

func addRuntimeSurfaceCompactWindow(summary *RuntimeStatsSnapshot, p sse.RuntimeSurfacePayload) bool {
	if !runtimeSurfaceHasCompactWindowState(p) {
		return false
	}
	summary.ContextCompactionLatestTriggerInputTokens = int64(p.CompactTriggerInputTokens)
	summary.ContextCompactionLatestModelContextWindowTokens = int64(p.ModelContextWindowTokens)
	summary.ContextCompactionLatestModelContextWindowSource = p.ModelContextWindowSource
	summary.ContextCompactionLatestReservedOutputTokens = int64(p.ReservedOutputTokens)
	summary.ContextCompactionLatestTriggerInputPercent = int64(p.CompactTriggerInputPercent)
	summary.ContextCompactionLatestCompactScopeActive = p.CompactScopeActive
	summary.ContextCompactionLatestCompactWindowOrdinal = p.CompactWindowOrdinal
	summary.ContextCompactionLatestCompactWindowPrefill = int64(p.CompactWindowPrefillInputTokens)
	summary.ContextCompactionLatestCompactWindowPrefillSource = p.CompactWindowPrefillSource
	summary.ContextCompactionLatestCompactScopedInputTokens = int64(p.CompactScopedInputTokens)
	summary.ContextCompactionLatestCompactHardInputLimit = int64(p.CompactHardInputLimitTokens)
	return true
}

func scanToolStatsFromEvents(r *bufio.Reader) (*ToolStatsSnapshot, error) {
	var summary ToolStatsSnapshot
	toolsByCallID := map[string]string{}
	governanceByCallID := map[string]sessionToolGovernanceClass{}
	derivedFailureByKind := map[string]int64{}
	seen := false
	for {
		line, tooLong, err := jsonl.ReadBoundedLine(r, maxSessionSummaryLineBytes)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if tooLong {
			continue
		}
		line = bytes.TrimRight(line, "\r\n")
		var ev sse.Event
		if err := json.Unmarshal(line, &ev); err != nil {
			continue
		}
		switch ev.Type {
		case sse.TypeToolRequest:
			var p sse.ToolRequestPayload
			if err := json.Unmarshal(ev.Data, &p); err != nil || p.CallID == "" {
				continue
			}
			toolsByCallID[p.CallID] = p.Tool
			if class, ok := classifySessionToolGovernanceRequest(p); ok {
				addSessionToolGovernanceRequest(&summary, class)
				governanceByCallID[p.CallID] = class
				seen = true
			}
		case sse.TypeToolResult:
			var p sse.ToolResultPayload
			if err := json.Unmarshal(ev.Data, &p); err != nil {
				continue
			}
			tool := toolsByCallID[p.CallID]
			for _, kind := range toolResultFailureKindsForSessionSummary(tool, p) {
				derivedFailureByKind[kind]++
				seen = true
			}
			if class, ok := classifySessionToolGovernanceResult(p); ok {
				addSessionToolGovernanceResult(&summary, class, p.ExitCode)
				delete(governanceByCallID, p.CallID)
				seen = true
			} else if class, ok := governanceByCallID[p.CallID]; ok {
				addSessionToolGovernanceResult(&summary, class, p.ExitCode)
				delete(governanceByCallID, p.CallID)
				seen = true
			}
		case sse.TypeTurnEnd:
			var p sse.TurnEndPayload
			if err := json.Unmarshal(ev.Data, &p); err != nil || p.ToolStats == nil {
				continue
			}
			addToolStatsSnapshot(&summary, toolStatsSnapshotFromRuntime(*p.ToolStats))
			seen = true
		}
	}
	mergeMaxToolFailureKinds(&summary, derivedFailureByKind)
	if !seen {
		return nil, nil
	}
	return &summary, nil
}

func toolResultFailureKindsForSessionSummary(tool string, p sse.ToolResultPayload) []string {
	kinds := append([]string(nil), p.FailureKinds...)
	if p.FailureKind != "" && !containsString(kinds, p.FailureKind) {
		kinds = append([]string{p.FailureKind}, kinds...)
	}
	for _, kind := range toolfailure.KindsForResult(tool, p.Result, p.ExitCode != 0) {
		if !containsString(kinds, kind) {
			kinds = append(kinds, kind)
		}
	}
	return kinds
}

const (
	sessionToolGovernancePlan        = "plan"
	sessionToolGovernanceFocusedTask = "focused_task"
	sessionToolGovernanceSubagent    = "subagent"
)

type sessionToolGovernanceClass struct {
	Kind   string
	Bucket string
}

func classifySessionToolGovernanceRequest(p sse.ToolRequestPayload) (sessionToolGovernanceClass, bool) {
	tool := strings.TrimSpace(p.Tool)
	if p.Delegation != nil {
		return classifySessionDelegation(*p.Delegation, p.Args)
	}
	switch tool {
	case "plan":
		return sessionToolGovernanceClass{
			Kind:   sessionToolGovernancePlan,
			Bucket: sessionGovernanceBucket(argString(p.Args, "action")),
		}, true
	case "run_task":
		return sessionToolGovernanceClass{
			Kind:   sessionToolGovernanceFocusedTask,
			Bucket: sessionGovernanceBucket(argString(p.Args, "task_type")),
		}, true
	case "subagent_run":
		return sessionToolGovernanceClass{
			Kind:   sessionToolGovernanceSubagent,
			Bucket: sessionGovernanceBucket(argString(p.Args, "mode")),
		}, true
	default:
		return sessionToolGovernanceClass{}, false
	}
}

func classifySessionToolGovernanceResult(p sse.ToolResultPayload) (sessionToolGovernanceClass, bool) {
	if p.Delegation == nil {
		return sessionToolGovernanceClass{}, false
	}
	return classifySessionDelegation(*p.Delegation, nil)
}

func classifySessionDelegation(meta sse.DelegationMeta, args map[string]any) (sessionToolGovernanceClass, bool) {
	switch strings.TrimSpace(meta.Kind) {
	case sessionToolGovernanceFocusedTask:
		bucket := meta.TaskType
		if bucket == "" {
			bucket = argString(args, "task_type")
		}
		return sessionToolGovernanceClass{
			Kind:   sessionToolGovernanceFocusedTask,
			Bucket: sessionGovernanceBucket(bucket),
		}, true
	case sessionToolGovernanceSubagent:
		bucket := meta.Mode
		if bucket == "" {
			bucket = argString(args, "mode")
		}
		return sessionToolGovernanceClass{
			Kind:   sessionToolGovernanceSubagent,
			Bucket: sessionGovernanceBucket(bucket),
		}, true
	default:
		return sessionToolGovernanceClass{}, false
	}
}

func addSessionToolGovernanceRequest(summary *ToolStatsSnapshot, class sessionToolGovernanceClass) {
	switch class.Kind {
	case sessionToolGovernancePlan:
		summary.PlanCalls++
		addSessionGovernanceBucket(&summary.PlanByAction, class.Bucket)
	case sessionToolGovernanceFocusedTask:
		summary.FocusedTaskCalls++
		addSessionGovernanceBucket(&summary.FocusedTaskByType, class.Bucket)
	case sessionToolGovernanceSubagent:
		summary.SubagentCalls++
		addSessionGovernanceBucket(&summary.SubagentByMode, class.Bucket)
	}
}

func addSessionToolGovernanceResult(summary *ToolStatsSnapshot, class sessionToolGovernanceClass, exitCode int) {
	if exitCode == 0 {
		return
	}
	switch class.Kind {
	case sessionToolGovernancePlan:
		summary.PlanErrors++
	case sessionToolGovernanceFocusedTask:
		summary.FocusedTaskErrors++
	case sessionToolGovernanceSubagent:
		summary.SubagentErrors++
	}
}

func addSessionGovernanceBucket(dst *map[string]int64, bucket string) {
	bucket = sessionGovernanceBucket(bucket)
	if *dst == nil {
		*dst = map[string]int64{}
	}
	(*dst)[bucket]++
}

func sessionGovernanceBucket(raw string) string {
	raw = strings.TrimSpace(strings.ToLower(raw))
	if raw == "" {
		return "unknown"
	}
	return raw
}

func argString(args map[string]any, key string) string {
	if len(args) == 0 {
		return ""
	}
	value, ok := args[key]
	if !ok || value == nil {
		return ""
	}
	switch v := value.(type) {
	case string:
		return v
	default:
		return fmt.Sprint(v)
	}
}

func mergeMaxToolFailureKinds(summary *ToolStatsSnapshot, derived map[string]int64) {
	if len(derived) == 0 {
		return
	}
	if summary.ToolFailureByKind == nil {
		summary.ToolFailureByKind = map[string]int64{}
	}
	for kind, count := range derived {
		if count > summary.ToolFailureByKind[kind] {
			summary.ToolFailureByKind[kind] = count
		}
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func toolStatsSnapshotFromRuntime(stats sse.ToolRuntimeStats) ToolStatsSnapshot {
	return ToolStatsSnapshot{
		ToolRequests:           int64(stats.ToolRequests),
		ToolNameCanonicalized:  int64(stats.ToolNameCanonicalized),
		ToolArgsRepaired:       int64(stats.ToolArgsRepaired),
		ToolRepairCalls:        int64(stats.ToolRepairCalls),
		ToolRepairSucceeded:    int64(stats.ToolRepairSucceeded),
		ToolRepairFailed:       int64(stats.ToolRepairFailed),
		ToolRepairNotes:        int64(stats.ToolRepairNotes),
		ToolRepairByKind:       stringIntMapToInt64(stats.ToolRepairByKind),
		ToolFailureByKind:      stringIntMapToInt64(stats.ToolFailureByKind),
		ToolErrors:             int64(stats.ToolErrors),
		ToolDurationMS:         stats.ToolDurationMS,
		LoopGuardInterventions: int64(stats.LoopGuardInterventions),
		ForcedNoTools:          int64(stats.ForcedNoTools),
		SourceAccessResults:    int64(stats.SourceAccessResults),
		SourceAccessVerified:   int64(stats.SourceAccessVerified),
		SourceAccessDiscovery:  int64(stats.SourceAccessDiscoveryOnly),
		SourceAccessNetwork:    int64(stats.SourceAccessNetwork),
		SourceAccessDynamic:    int64(stats.SourceAccessDynamicPartial),
		MemoryUpdates:          int64(stats.MemoryUpdates),
		MemoryUpdateAdd:        int64(stats.MemoryUpdateAdd),
		MemoryUpdateReplace:    int64(stats.MemoryUpdateReplace),
		MemoryUpdateRemove:     int64(stats.MemoryUpdateRemove),
		MemorySearchCalls:      int64(stats.MemorySearchCalls),
		MemorySearchMisses:     int64(stats.MemorySearchMisses),
		SessionSearchCalls:     int64(stats.SessionSearchCalls),
		SessionSearchResults:   int64(stats.SessionSearchResults),
		SessionSearchContext:   int64(stats.SessionSearchContextHits),
		SessionSearchTerms:     int64(stats.SessionSearchMatchedTerms),
		SessionSearchRecent:    int64(stats.SessionSearchRecent),
		ToolContextTruncated:   int64(stats.ToolContextTruncated),
		ToolContextOmitted:     int64(stats.ToolContextOmittedBytes),
	}
}

func stringIntMapToInt64(in map[string]int) map[string]int64 {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]int64, len(in))
	for key, value := range in {
		out[key] = int64(value)
	}
	return out
}

func scanShellCWDFromEvents(r *bufio.Reader) (string, bool, error) {
	latest := ""
	hasShell := false
	for {
		line, tooLong, err := jsonl.ReadBoundedLine(r, maxSessionSummaryLineBytes)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", false, err
		}
		if tooLong {
			continue
		}
		line = bytes.TrimRight(line, "\r\n")
		var ev sse.Event
		if err := json.Unmarshal(line, &ev); err != nil || ev.Type != sse.TypeToolRequest {
			continue
		}
		var p sse.ToolRequestPayload
		if err := json.Unmarshal(ev.Data, &p); err != nil || p.Tool != "shell" {
			continue
		}
		hasShell = true
		if cwd, ok := p.Args["cwd"].(string); ok && strings.TrimSpace(cwd) != "" {
			latest = strings.TrimSpace(cwd)
		}
	}
	return latest, hasShell, nil
}

func scanMemoryUpdatesFromEvents(r *bufio.Reader) (*sse.MemoryUpdateMeta, error) {
	var latest *sse.MemoryUpdateMeta
	for {
		line, tooLong, err := jsonl.ReadBoundedLine(r, maxSessionSummaryLineBytes)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if tooLong {
			continue
		}
		line = bytes.TrimRight(line, "\r\n")
		var ev sse.Event
		if err := json.Unmarshal(line, &ev); err != nil || ev.Type != sse.TypeToolResult {
			continue
		}
		var p sse.ToolResultPayload
		if err := json.Unmarshal(ev.Data, &p); err != nil || p.MemoryUpdate == nil {
			continue
		}
		update := *p.MemoryUpdate
		latest = &update
	}
	return latest, nil
}

func memoryUpdateFromLoopState(state loopstate.State) *sse.MemoryUpdateMeta {
	if strings.TrimSpace(state.LastMemoryUpdateAction) == "" &&
		strings.TrimSpace(state.LastMemoryUpdateLoc) == "" &&
		strings.TrimSpace(state.LastMemoryUpdate) == "" {
		return nil
	}
	target := strings.TrimSpace(state.LastMemoryUpdateTarget)
	if target == "" {
		target = "memory"
	}
	topic := strings.TrimSpace(state.LastMemoryUpdateTopic)
	location := strings.TrimSpace(state.LastMemoryUpdateLoc)
	if location == "" && topic != "" {
		location = target + ":" + topic
	}
	preview := strings.TrimSpace(state.LastMemoryUpdate)
	if preview == "" {
		preview = strings.TrimSpace(state.LastMemoryUpdateNext)
	}
	return &sse.MemoryUpdateMeta{
		Action:          strings.TrimSpace(state.LastMemoryUpdateAction),
		Target:          target,
		Topic:           topic,
		Location:        location,
		Preview:         preview,
		PreviousPreview: strings.TrimSpace(state.LastMemoryUpdatePrev),
		NextPreview:     strings.TrimSpace(state.LastMemoryUpdateNext),
	}
}

func scanRecoveryHintsFromEvents(r *bufio.Reader) (string, error) {
	latest := ""
	latestTurnID := ""
	skippedLines := 0
	oversizedLines := 0
	invalidLines := 0
	for {
		line, tooLong, err := jsonl.ReadBoundedLine(r, maxSessionSummaryLineBytes)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", err
		}
		if tooLong {
			skippedLines++
			oversizedLines++
			continue
		}
		setLatest := func(hint, turnID string) {
			if hint == "" {
				return
			}
			latest = hint
			latestTurnID = strings.TrimSpace(turnID)
		}
		line = bytes.TrimRight(line, "\r\n")
		var ev sse.Event
		if err := json.Unmarshal(line, &ev); err != nil {
			skippedLines++
			invalidLines++
			continue
		}
		if ev.Type == sse.TypeConversationRepaired {
			var p sse.ConversationRepairedPayload
			if err := json.Unmarshal(ev.Data, &p); err != nil {
				continue
			}
			if hint := recoveryHintFromText(p.Next); hint != "" {
				setLatest(hint, "")
			}
			continue
		}
		if ev.Type == sse.TypeError {
			var p sse.ErrorPayload
			if err := json.Unmarshal(ev.Data, &p); err != nil {
				continue
			}
			setLatest(recoveryHintFromErrorPayload(p), p.TurnID)
			continue
		}
		if ev.Type == sse.TypeTurnEnd {
			var p sse.TurnEndPayload
			if err := json.Unmarshal(ev.Data, &p); err != nil {
				continue
			}
			if hint := recoveryHintFromTurnEnd(p, latestTurnID); hint != "" {
				setLatest(hint, p.TurnID)
			}
			continue
		}
		if ev.Type == sse.TypeLoopProtocolFeed {
			var p sse.LoopProtocolFeedPayload
			if err := json.Unmarshal(ev.Data, &p); err != nil {
				continue
			}
			if hint := recoveryHintFromLoopProtocolFeed(p); hint != "" {
				setLatest(hint, p.TurnID)
			}
			continue
		}
		if ev.Type == sse.TypeLoopTurnCheckpoint {
			var p sse.LoopTurnCheckpointPayload
			if err := json.Unmarshal(ev.Data, &p); err != nil {
				continue
			}
			if hint := recoveryHintFromLoopTurnCheckpoint(p); hint != "" {
				setLatest(hint, p.TurnID)
			}
			continue
		}
		if ev.Type == sse.TypeLoopDecision {
			var p sse.LoopDecisionPayload
			if err := json.Unmarshal(ev.Data, &p); err != nil {
				continue
			}
			if hint := recoveryHintFromLoopDecision(p); hint != "" {
				setLatest(hint, p.TurnID)
			}
			continue
		}
		if ev.Type == sse.TypeContextCompact {
			var p sse.ContextCompactPayload
			if err := json.Unmarshal(ev.Data, &p); err != nil {
				continue
			}
			var raw struct {
				SummaryPresent *bool `json:"summary_present"`
			}
			_ = json.Unmarshal(ev.Data, &raw)
			if hint := recoveryHintFromContextCompaction(p, raw.SummaryPresent != nil); hint != "" {
				setLatest(hint, p.TurnID)
			}
			continue
		}
		if ev.Type != sse.TypeToolResult {
			continue
		}
		var p sse.ToolResultPayload
		if err := json.Unmarshal(ev.Data, &p); err != nil {
			continue
		}
		if hint := recoveryHintFromSessionSearchResult(p.Result); hint != "" {
			setLatest(hint, p.TurnID)
			continue
		}
		if hint := recoveryHintFromMemorySearchMissResult(p.Result); hint != "" {
			setLatest(hint, p.TurnID)
			continue
		}
		if hint := recoveryHintFromBrowserNetworkRefsResult(p.Result); hint != "" {
			setLatest(hint, p.TurnID)
			continue
		}
		if hint := recoveryHintFromToolArtifact(p); hint != "" {
			setLatest(hint, p.TurnID)
			continue
		}
		if p.ExitCode == 0 && p.FailureKind == "" && len(p.FailureKinds) == 0 {
			continue
		}
		hint := recoveryHintFromToolResult(p.ResultSummary, p.Result)
		if hint != "" {
			setLatest(hint, p.TurnID)
		}
	}
	if latest == "" {
		latest = recoveryHintFromEventLogIntegrity(skippedLines, oversizedLines, invalidLines)
	}
	return latest, nil
}

func recoveryHintFromEventLogIntegrity(skippedLines, oversizedLines, invalidLines int) string {
	if skippedLines <= 0 {
		return ""
	}
	parts := []string{fmt.Sprintf("event log skipped %d malformed or oversized record(s); inspect /history skipped_lines before trusting trace completeness", skippedLines)}
	if oversizedLines > 0 {
		parts = append(parts, fmt.Sprintf("oversized=%d", oversizedLines))
	}
	if invalidLines > 0 {
		parts = append(parts, fmt.Sprintf("invalid=%d", invalidLines))
	}
	return recoveryHintFromText(strings.Join(parts, "; "))
}

func recoveryHintFromToolArtifact(p sse.ToolResultPayload) string {
	if !p.ResultTruncated && p.ResultOmittedBytes == 0 && p.ContextOmittedBytes == 0 {
		return ""
	}
	artifactPath := strings.TrimSpace(p.ResultArtifactPath)
	if artifactPath == "" {
		return recoveryHintFromText("truncated tool output without saved artifact; rerun a narrower command or inspect trace before relying on omitted output.")
	}
	parts := []string{"truncated tool output; inspect artifact " + artifactPath}
	if p.ResultOmittedBytes > 0 {
		parts = append(parts, fmt.Sprintf("result omitted %d bytes", p.ResultOmittedBytes))
	}
	if p.ContextOmittedBytes > 0 {
		parts = append(parts, fmt.Sprintf("context omitted %d bytes", p.ContextOmittedBytes))
	}
	return recoveryHintFromText(strings.Join(parts, "; "))
}

func recoveryHintFromErrorPayload(p sse.ErrorPayload) string {
	msg := strings.Join(strings.Fields(strings.TrimSpace(p.Message)), " ")
	if msg == "" {
		return ""
	}
	if p.Recoverable {
		return recoveryHintFromText("runtime error: " + msg + "; inspect recent trace/tool evidence, then retry or continue from persisted state if safe.")
	}
	return recoveryHintFromText("runtime error: " + msg + "; inspect recent trace/tool evidence before continuing.")
}

func recoveryHintFromTurnEnd(p sse.TurnEndPayload, latestTurnID string) string {
	switch p.Reason {
	case sse.TurnEndMaxTurns:
		parts := []string{"turn reached the tool-step budget; change strategy before retrying; continue from evidence"}
		if p.ToolStats != nil {
			if kind, count := topToolFailureKind(p.ToolStats.ToolFailureByKind); kind != "" {
				parts = append(parts, fmt.Sprintf("top tool failure kind=%s (%d)", kind, count))
			}
			if p.ToolStats.LoopGuardInterventions > 0 {
				parts = append(parts, "loop guards fired")
			}
			appendToolRepairFailureHint(&parts, p.ToolStats)
			if p.ToolStats.ToolContextTruncated > 0 {
				parts = append(parts, "inspect artifacts/trace for omitted output")
			}
		}
		return recoveryHintFromText(strings.Join(parts, "; "))
	case sse.TurnEndError:
		if p.TurnID != "" && latestTurnID == p.TurnID {
			return ""
		}
		return recoveryHintFromText("turn ended with a runtime error; inspect recent error/tool events and resume from persisted evidence before retrying.")
	default:
		return recoveryHintFromToolRepairFailure(p)
	}
}

func appendToolRepairFailureHint(parts *[]string, stats *sse.ToolRuntimeStats) {
	if parts == nil || stats == nil || stats.ToolRepairFailed <= 0 {
		return
	}
	item := fmt.Sprintf("tool repair failed=%d", stats.ToolRepairFailed)
	if kind, count := topToolFailureKind(stats.ToolRepairByKind); kind != "" {
		item += fmt.Sprintf(" kind=%s:%d", kind, count)
	}
	*parts = append(*parts, item)
}

func recoveryHintFromToolRepairFailure(p sse.TurnEndPayload) string {
	if p.ToolStats == nil || p.ToolStats.ToolRepairFailed <= 0 {
		return ""
	}
	parts := []string{"tool-call repair failed; inspect tool name/args before retrying"}
	appendToolRepairFailureHint(&parts, p.ToolStats)
	if p.ToolStats.ToolErrors > 0 {
		parts = append(parts, fmt.Sprintf("tool_errors=%d", p.ToolStats.ToolErrors))
	}
	return recoveryHintFromText(strings.Join(parts, "; "))
}

func topToolFailureKind(counts map[string]int) (string, int) {
	var top string
	var topCount int
	for kind, count := range counts {
		kind = strings.TrimSpace(kind)
		if kind == "" || count <= 0 {
			continue
		}
		if count > topCount || (count == topCount && (top == "" || kind < top)) {
			top = kind
			topCount = count
		}
	}
	return top, topCount
}

func recoveryHintFromLoopProtocolFeed(p sse.LoopProtocolFeedPayload) string {
	endReason := strings.TrimSpace(p.LastTurnEndReason)
	hasRecoverySignal := endReason == sse.TurnEndMaxTurns ||
		endReason == sse.TurnEndError ||
		p.LastTurnToolErrors > 0 ||
		p.LastTurnForcedNoTools > 0 ||
		p.LastTurnLoopGuards > 0 ||
		p.LastTurnMemorySearchMisses > 0
	if !hasRecoverySignal {
		return ""
	}
	parts := []string{"loop feed recovery"}
	if endReason != "" {
		parts = append(parts, "end="+endReason)
	}
	if p.LastTurnLoopGuards > 0 {
		parts = append(parts, fmt.Sprintf("guards=%d", p.LastTurnLoopGuards))
	}
	if p.LastTurnToolErrors > 0 {
		parts = append(parts, fmt.Sprintf("tool_errors=%d", p.LastTurnToolErrors))
	}
	if p.LastTurnForcedNoTools > 0 {
		parts = append(parts, fmt.Sprintf("forced_no_tools=%d", p.LastTurnForcedNoTools))
	}
	if p.LastTurnMemorySearchMisses > 0 {
		parts = append(parts, fmt.Sprintf("mem_miss=%d", p.LastTurnMemorySearchMisses))
	}
	if p.LastTurnSessionSearchCalls > 0 {
		parts = append(parts, fmt.Sprintf("sess_search=%d", p.LastTurnSessionSearchCalls))
	}
	if step := strings.TrimSpace(p.PlanCurrentStep); step != "" {
		parts = append(parts, "step="+step)
	} else if label := strings.TrimSpace(p.PlanLabel); label != "" {
		parts = append(parts, "plan="+label)
	}
	parts = append(parts, "inspect LOOP/plan")
	return recoveryHintFromText(strings.Join(parts, "; "))
}

func recoveryHintFromLoopTurnCheckpoint(p sse.LoopTurnCheckpointPayload) string {
	endReason := strings.TrimSpace(p.EndReason)
	hasRecoverySignal := endReason == sse.TurnEndMaxTurns ||
		endReason == sse.TurnEndError ||
		p.ToolErrors > 0 ||
		p.ForcedNoTools > 0 ||
		p.LoopGuards > 0 ||
		p.MemoryMisses > 0
	if !hasRecoverySignal {
		return ""
	}
	parts := []string{"loop turn checkpoint recovery"}
	if endReason != "" {
		parts = append(parts, "end="+endReason)
	}
	if p.LoopGuards > 0 {
		parts = append(parts, fmt.Sprintf("guards=%d", p.LoopGuards))
	}
	if p.ToolErrors > 0 {
		parts = append(parts, fmt.Sprintf("tool_errors=%d", p.ToolErrors))
	}
	if p.ForcedNoTools > 0 {
		parts = append(parts, fmt.Sprintf("forced_no_tools=%d", p.ForcedNoTools))
	}
	if p.MemoryMisses > 0 {
		parts = append(parts, fmt.Sprintf("mem_miss=%d", p.MemoryMisses))
	}
	if p.SessionSearchCalls > 0 {
		parts = append(parts, fmt.Sprintf("sess_search=%d", p.SessionSearchCalls))
	}
	if loopID := strings.TrimSpace(p.LoopID); loopID != "" {
		parts = append(parts, "loop="+loopID)
	}
	parts = append(parts, "inspect LOOP/plan")
	return recoveryHintFromText(strings.Join(parts, "; "))
}

func recoveryHintFromLoopState(state loopstate.State) string {
	var parts []string
	if hint := recoveryHintFromLoopStateDecision(state); hint != "" {
		parts = append(parts, hint)
	}
	endReason := strings.TrimSpace(state.LastTurnEndReason)
	if endReason == sse.TurnEndMaxTurns || endReason == sse.TurnEndError || state.LastTurnToolErrors > 0 || state.LastTurnForcedNoTools > 0 || state.LastTurnLoopGuards > 0 || state.LastTurnMemoryMisses > 0 {
		if len(parts) == 0 {
			parts = append(parts, "loop state recovery")
		}
		if endReason != "" {
			parts = append(parts, "end="+endReason)
		}
		if state.LastTurnLoopGuards > 0 {
			parts = append(parts, fmt.Sprintf("guards=%d", state.LastTurnLoopGuards))
		}
		if state.LastTurnToolErrors > 0 {
			parts = append(parts, fmt.Sprintf("tool_errors=%d", state.LastTurnToolErrors))
		}
		if state.LastTurnForcedNoTools > 0 {
			parts = append(parts, fmt.Sprintf("forced_no_tools=%d", state.LastTurnForcedNoTools))
		}
		if state.LastTurnMemoryMisses > 0 {
			parts = append(parts, fmt.Sprintf("mem_miss=%d", state.LastTurnMemoryMisses))
		}
		if state.LastTurnSessionSearch > 0 {
			parts = append(parts, fmt.Sprintf("sess_search=%d", state.LastTurnSessionSearch))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	parts = append(parts, "inspect LOOP/plan")
	if step := strings.TrimSpace(state.LastPlanStep); step != "" {
		parts = append(parts, "step="+step)
	}
	return recoveryHintFromText(strings.Join(parts, "; "))
}

func recoveryHintFromLoopStateDecision(state loopstate.State) string {
	action := strings.TrimSpace(state.LastDecisionAction)
	if action == "" {
		return ""
	}
	decision := strings.TrimSpace(state.LastDecision)
	switch decision {
	case "defer", "trigger", "stop", "pause", "request_input":
		kind := strings.TrimSpace(state.LastDecisionKind)
		if kind != "" {
			return "loop decision " + kind + "=" + decision + "; action=" + action
		}
		return "loop decision " + decision + "; action=" + action
	default:
		return ""
	}
}

func recoveryHintFromLoopDecision(p sse.LoopDecisionPayload) string {
	if p.VisibleInUI != nil && !*p.VisibleInUI {
		return ""
	}
	if strings.TrimSpace(p.RequiredAction) == "" {
		return ""
	}
	switch strings.TrimSpace(p.Decision) {
	case "defer", "trigger", "stop", "pause", "request_input":
		return recoveryHintFromText(p.RequiredAction)
	default:
		return ""
	}
}

func recoveryHintFromContextCompaction(p sse.ContextCompactPayload, summaryPresentKnown bool) string {
	state := contextCompactSummaryState(p.SummaryPresent, p.SummaryBytes, p.SummaryPreview, summaryPresentKnown)
	if state != "missing" && state != "empty" {
		return ""
	}
	parts := []string{"context compaction summary " + state}
	if p.RemovedMessages > 0 {
		parts = append(parts, fmt.Sprintf("removed %d message(s)", p.RemovedMessages))
	}
	if reason := strings.TrimSpace(p.Reason); reason != "" {
		parts = append(parts, "reason="+reason)
	}
	if p.Reactive {
		parts = append(parts, "reactive=true")
	}
	parts = append(parts, "recover from durable plan, LOOP, memory, or session_search; inspect trace/context_compactions")
	return recoveryHintFromText(strings.Join(parts, "; "))
}

func recoveryHintFromBrowserNetworkRefsResult(text string) string {
	text = strings.TrimSpace(text)
	if text == "" || !strings.HasPrefix(text, "BROWSER NETWORK EVIDENCE") {
		return ""
	}
	if !strings.Contains(text, "refs_only_not_citable") && !strings.Contains(text, "read_required=true") {
		return ""
	}
	if hint := recoveryHintFromToolResult("", text); hint != "" {
		return hint
	}
	return recoveryHintFromText("browser network returned refs only; call browser_network_read before citing hidden JSON/text values.")
}

func recoveryHintFromConversationMessage(msg agent.ChatMessage) string {
	switch msg.Role {
	case "tool":
		return recoveryHintFromToolResult("", msg.Content)
	case "user":
		if !strings.Contains(msg.Content, "Failure: kind=resume_") {
			return ""
		}
		return recoveryHintFromToolResult("", msg.Content)
	default:
		return ""
	}
}

func recoveryHintFromToolResult(summary, result string) string {
	for _, candidate := range []string{result, summary} {
		if hint := recoveryHintFromSessionSearchResult(candidate); hint != "" {
			return hint
		}
		if hint := recoveryHintFromMemorySearchMissResult(candidate); hint != "" {
			return hint
		}
	}
	text := summary
	if result != "" && result != summary {
		if text != "" {
			text += "\n"
		}
		text += result
	}
	match := sessionRecoveryNextRe.FindStringSubmatch(text)
	if len(match) < 2 {
		return ""
	}
	return recoveryHintFromText(match[1])
}

func recoveryHintFromSessionSearchResult(text string) string {
	text = strings.TrimSpace(text)
	if text == "" || !strings.HasPrefix(text, "{") || (!strings.Contains(text, `"recent_sessions"`) && !strings.Contains(text, `"session_id"`)) {
		return ""
	}
	var resp agent.SessionSearchResponse
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		return ""
	}
	if resp.Total > 0 || len(resp.Results) > 0 {
		return recoveryHintFromWeakSessionSearchResults(resp)
	}
	if len(resp.RecentSessions) == 0 {
		return ""
	}
	recent := resp.RecentSessions[0]
	parts := []string{"session recall found no direct hits"}
	if sid := strings.TrimSpace(recent.SessionID); sid != "" {
		parts = append(parts, "retry from recent session "+sid)
	}
	if preview := recoveryHintRecentSessionPreview(recent); preview != "" {
		parts = append(parts, preview)
	}
	return recoveryHintFromText(strings.Join(parts, "; "))
}

func recoveryHintRecentSessionPreview(recent sessionsearch.RecentSession) string {
	var parts []string
	appendPreview := func(label, value string) {
		value = compactRecentSessionAnchor(label, value)
		if value == "" {
			return
		}
		parts = append(parts, label+"="+value)
	}
	appendPreview("recovery", recent.Recovery)
	appendPreview("loop", recent.Loop)
	appendPreview("plan", recent.Plan)
	if len(parts) == 0 {
		appendPreview("user", recent.LatestUser)
		appendPreview("assistant", recent.LatestAssistant)
	}
	return strings.Join(parts, " ")
}

func compactRecentSessionAnchor(label, value string) string {
	value = strings.Join(strings.Fields(value), " ")
	if value == "" {
		return ""
	}
	switch label {
	case "recovery":
		return compactRecoveryAnchor(value)
	case "loop":
		return compactLoopAnchor(value)
	case "plan":
		return compactPlanAnchor(value)
	case "user", "assistant":
		return textutil.Preview(value, 80)
	default:
		return textutil.Preview(value, 80)
	}
}

func compactRecoveryAnchor(value string) string {
	var parts []string
	for i, field := range strings.Fields(value) {
		field = strings.Trim(field, ";,")
		switch {
		case i == 0 && !strings.Contains(field, "="):
			parts = append(parts, strings.TrimSuffix(field, ":"))
		case strings.HasPrefix(field, "reason="):
			parts = append(parts, field)
		case strings.HasPrefix(field, "top_failure="):
			parts = append(parts, field)
		case strings.HasPrefix(field, "loop_guards="):
			parts = append(parts, field)
		}
	}
	if len(parts) == 0 {
		return textutil.Preview(value, 80)
	}
	return textutil.Preview(strings.Join(parts, " "), 95)
}

func compactLoopAnchor(value string) string {
	var parts []string
	if strings.Contains(value, "recent_loop_events") {
		parts = append(parts, "recent_loop_events")
	}
	for _, field := range strings.Fields(value) {
		field = strings.Trim(field, ";,")
		if strings.HasPrefix(field, "type=") {
			parts = append(parts, strings.TrimPrefix(field, "type="))
		}
	}
	if len(parts) == 0 {
		return textutil.Preview(value, 80)
	}
	return textutil.Preview(strings.Join(parts, " "), 95)
}

func compactPlanAnchor(value string) string {
	value = strings.ReplaceAll(value, "plan_status: ", "")
	if idx := strings.Index(value, "current_step:"); idx >= 0 {
		value = value[idx:]
	}
	return textutil.Preview(value, 85)
}

func recoveryHintFromWeakSessionSearchResults(resp agent.SessionSearchResponse) string {
	if len(resp.Results) == 0 {
		return ""
	}
	for _, hit := range resp.Results {
		if hit.ContextIncluded || sessionSearchResultRoleIsRecoveryAnchor(hit.Role) {
			return ""
		}
	}
	first := resp.Results[0]
	parts := []string{"session recall hits lack adjacent context/recovery anchors"}
	if strings.TrimSpace(first.SessionID) != "" {
		anchor := "verify with narrower session_search before relying on " + strings.TrimSpace(first.SessionID)
		if first.TurnIdx > 0 {
			anchor += fmt.Sprintf(" turn=%d", first.TurnIdx)
		}
		if first.MessageIdx > 0 {
			anchor += fmt.Sprintf(" message=%d", first.MessageIdx)
		}
		parts = append(parts, anchor)
	} else {
		parts = append(parts, "verify with narrower session_search before relying on the hits")
	}
	hasMatchedTerms := false
	for _, hit := range resp.Results {
		if len(hit.MatchedTerms) > 0 {
			hasMatchedTerms = true
			break
		}
	}
	if !hasMatchedTerms {
		parts = append(parts, "matched terms missing")
	}
	return recoveryHintFromText(strings.Join(parts, "; "))
}

func sessionSearchResultRoleIsRecoveryAnchor(role string) bool {
	switch strings.TrimSpace(role) {
	case "plan", "loop", "event":
		return true
	default:
		return false
	}
}

func recoveryHintFromMemorySearchMissResult(text string) string {
	text = strings.TrimSpace(text)
	if text == "" || !strings.HasPrefix(text, "{") || !strings.Contains(text, "no entries matched") {
		return ""
	}
	var resp memory.MemoryResponse
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		return ""
	}
	if !resp.OK || len(resp.Results) > 0 || !strings.Contains(resp.Message, "no entries matched") {
		return ""
	}
	parts := []string{"memory search found no direct hits"}
	if resp.Target != "" {
		parts = append(parts, "target="+string(resp.Target))
	}
	topicNames := make([]string, 0, min(len(resp.Topics), 3))
	for _, topic := range resp.Topics {
		name := strings.TrimSpace(topic.Topic)
		if name != "" {
			topicNames = append(topicNames, name)
		}
		if len(topicNames) >= 3 {
			break
		}
	}
	if len(topicNames) > 0 {
		parts = append(parts, "retry action=search with a specific topic such as "+topicNames[0])
		if len(topicNames) > 1 {
			parts = append(parts, "available topics: "+strings.Join(topicNames, ", "))
		}
	}
	if len(topicNames) == 0 {
		switch resp.Target {
		case memory.TargetMemory:
			parts = append(parts, "no topic anchors returned")
			parts = append(parts, "retry action=list for topic discovery or use session_search for transcript recall")
		case memory.TargetUser:
			parts = append(parts, "no user-memory anchors returned")
			parts = append(parts, "confirm the preference is saved or use session_search for prior wording")
		default:
			parts = append(parts, "retry with action=list or a more specific target/topic before assuming memory is empty")
		}
	}
	return recoveryHintFromText(strings.Join(parts, "; "))
}

func recoveryHintFromText(text string) string {
	return textutil.Preview(strings.Join(strings.Fields(text), " "), maxSessionRecoveryHintChars)
}

func contextCompactSummaryState(summaryPresent bool, summaryBytes int, summaryPreview string, summaryPresentKnown bool) string {
	if summaryBytes > 0 || strings.TrimSpace(summaryPreview) != "" {
		return "present"
	}
	if !summaryPresentKnown {
		return ""
	}
	if !summaryPresent {
		return "missing"
	}
	return "empty"
}

func seekSessionSummaryTail(f *os.File) error {
	info, err := f.Stat()
	if err != nil {
		return err
	}
	size := info.Size()
	if size <= maxSessionSummaryTailBytes {
		_, err := f.Seek(0, io.SeekStart)
		return err
	}
	start := size - maxSessionSummaryTailBytes
	if _, err := f.Seek(start-1, io.SeekStart); err != nil {
		return err
	}
	var prev [1]byte
	n, err := f.Read(prev[:])
	if err != nil {
		return err
	}
	if n == 1 && prev[0] == '\n' {
		return nil
	}
	buf := make([]byte, 64*1024)
	for {
		n, err := f.Read(buf)
		if n > 0 {
			if idx := bytes.IndexByte(buf[:n], '\n'); idx >= 0 {
				_, seekErr := f.Seek(int64(idx-n+1), io.SeekCurrent)
				return seekErr
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func latestUserMessageFromMessages(messages []agent.ChatMessage) string {
	latest, _ := userMessageSummariesFromMessages(messages)
	return latest
}

func userMessageSummariesFromMessages(messages []agent.ChatMessage) (string, string) {
	var latest string
	var topic string
	for _, msg := range messages {
		if msg.Role != "user" {
			continue
		}
		summary := summarizeLatestUserMessage(msg.Content)
		if summary == "" {
			continue
		}
		latest = summary
		if !isContinuationSessionPrompt(summary) {
			topic = summary
		}
	}
	if topic == "" {
		topic = latest
	}
	return latest, topic
}

var (
	cnSessionTitleActionRE = regexp.MustCompile(`^(?:请你?|麻烦你?|帮我|帮忙|真实地?|真实|实际地?|完整地?|详细地?|认真地?)?\s*(?:新增|添加|实现|开发|构建|创建|写|编写|修复|解决|优化|重构|完善|改进|设计|理解|查看|检查|审查|收集|检索|查询|查找|搜索|调研|研究|介绍|分析|总结|梳理|说明|整理)\s*(?:一下|下|一个|一种|一类|一份|这个|当前)?\s*([^。！？；;\n]{2,120})`)
	enSessionTitleActionRE = regexp.MustCompile(`(?i)^(?:please\s+|can you\s+|could you\s+)?(?:review|research|inspect|summarize|analyze|explain|fix|debug|improve|refactor|implement|build|create|design|understand)\s+(?:the\s+|a\s+|an\s+|current\s+)?([^!?;\n]{2,120})`)
	cnSessionTitleTopicRE  = regexp.MustCompile(`^(.{1,40}?)\s*(?:是|为)\s*(.{1,50}?)\s*的\s*(?:一个|一种|一类)?\s*(子网|项目|协议|平台|工具|框架|网络)\s*$`)
	enSessionTitleTopicRE  = regexp.MustCompile(`(?i)^(.{1,40}?)\s+(?:is|as)\s+(?:an?\s+)?(.{1,50}?)\s+(subnet|project|protocol|platform|tool|framework|network)\s*$`)
)

func populateSessionSummaryTitle(summary *sessionSummary) {
	source := summary.TopicUserMessage
	if source == "" {
		source = summary.LatestUserMessage
	}
	title := summarizeSessionTitleFromUserMessage(source)
	if title == "" || normalizeSessionTitleComparable(title) == normalizeSessionTitleComparable(source) {
		return
	}
	summary.SummaryTitle = truncateSessionTitle(title, 58)
}

func summarizeSessionTitleFromUserMessage(text string) string {
	cleaned := textutil.CompactWhitespace(text)
	if cleaned == "" {
		return ""
	}
	if title := summarizeSessionTitleFeedback(cleaned); title != "" {
		return title
	}
	if title := summarizeSessionFocusTitle(cleaned); title != "" {
		return title
	}
	if title := summarizeSessionActionTitle(cleaned); title != "" {
		return title
	}
	firstClause := firstSessionTitleClause(cleaned)
	topicInput := prettySessionTopicName(trimSessionTitleSuffix(stripSessionTitlePrefix(firstClause)))
	if title := summarizeSessionTopicStatement(topicInput); title != "" {
		return title
	}
	return normalizeSessionTitlePhrase(topicInput)
}

func summarizeSessionTitleFeedback(text string) string {
	lower := strings.ToLower(text)
	if (strings.Contains(text, "会话") || strings.Contains(text, "聊天") || strings.Contains(lower, "session") || strings.Contains(lower, "chat")) &&
		(strings.Contains(text, "标题") || strings.Contains(lower, "title")) &&
		(strings.Contains(text, "总结") || strings.Contains(text, "摘要") || strings.Contains(text, "归纳") || strings.Contains(text, "概括") || strings.Contains(lower, "summar")) {
		return "会话标题摘要"
	}
	return ""
}

func summarizeSessionFocusTitle(text string) string {
	for _, marker := range []string{"重点关注", "主要关注", "优先关注", "关注", "围绕", "关于", "针对"} {
		if _, tail, ok := strings.Cut(text, marker); ok {
			return normalizeSessionTitlePhrase(firstSessionTitleClause(tail))
		}
	}
	lower := strings.ToLower(text)
	for _, marker := range []string{"focus on ", "focused on ", "focusing on ", "around ", "about ", "regarding "} {
		if idx := strings.Index(lower, marker); idx >= 0 {
			return normalizeSessionTitlePhrase(firstSessionTitleClause(text[idx+len(marker):]))
		}
	}
	return ""
}

func summarizeSessionActionTitle(text string) string {
	for _, re := range []*regexp.Regexp{cnSessionTitleActionRE, enSessionTitleActionRE} {
		match := re.FindStringSubmatch(text)
		if len(match) < 2 {
			continue
		}
		if title := normalizeSessionTitlePhrase(match[1]); title != "" {
			return title
		}
	}
	return ""
}

func summarizeSessionTopicStatement(text string) string {
	if match := cnSessionTitleTopicRE.FindStringSubmatch(text); len(match) == 4 {
		return prettySessionTopicName(match[1]) + "（" + prettySessionTopicName(match[2]) + " " + prettySessionTopicName(match[3]) + "）"
	}
	if match := enSessionTitleTopicRE.FindStringSubmatch(text); len(match) == 4 {
		return prettySessionTopicName(match[1]) + " (" + prettySessionTopicName(match[2]) + " " + strings.ToLower(match[3]) + ")"
	}
	return ""
}

func firstSessionTitleClause(text string) string {
	value := strings.TrimSpace(text)
	for _, sep := range []string{"。", "！", "？", "；", ";", "!", "?", "，", ","} {
		if head, _, ok := strings.Cut(value, sep); ok && strings.TrimSpace(head) != "" {
			value = strings.TrimSpace(head)
			break
		}
	}
	return value
}

func normalizeSessionTitlePhrase(text string) string {
	value := strings.TrimSpace(text)
	for {
		next := stripSessionTitlePrefix(value)
		next = trimSessionTitleSuffix(next)
		if next == value {
			break
		}
		value = next
	}
	value = strings.ReplaceAll(value, "的", " ")
	value = textutil.CompactWhitespace(value)
	return prettySessionTopicName(value)
}

func stripSessionTitlePrefix(text string) string {
	value := strings.TrimSpace(text)
	prefixes := []string{
		"请你", "请", "麻烦你", "麻烦", "帮我", "帮忙", "真实地", "真实", "实际地", "实际", "完整地", "完整", "详细地", "详细", "认真地", "认真",
		"收集", "检索", "查询", "查找", "搜索", "调研", "研究", "介绍", "分析", "总结", "梳理", "说明", "整理", "获取", "输出", "生成",
	}
	for _, prefix := range prefixes {
		if strings.HasPrefix(value, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(value, prefix))
		}
	}
	lower := strings.ToLower(value)
	for _, prefix := range []string{"please ", "can you ", "could you ", "the ", "a ", "an ", "review ", "research ", "inspect ", "summarize ", "analyze ", "explain "} {
		if strings.HasPrefix(lower, prefix) {
			return strings.TrimSpace(value[len(prefix):])
		}
	}
	return value
}

func trimSessionTitleSuffix(text string) string {
	value := strings.TrimSpace(text)
	for _, sep := range []string{"并向我介绍", "并介绍", "并说明", "并分析", "并总结", "向我介绍", "相关信息", "相关资料", "相关内容", "信息", "资料", "内容"} {
		if idx := strings.Index(value, sep); idx >= 0 {
			value = strings.TrimSpace(value[:idx])
		}
	}
	for _, suffix := range []string{"是什么", "是啥", "是什麼"} {
		value = strings.TrimSuffix(value, suffix)
	}
	return strings.TrimSpace(value)
}

func prettySessionTopicName(text string) string {
	value := textutil.CompactWhitespace(strings.Trim(text, "“”\"'"))
	replacements := []struct {
		from string
		to   string
	}{
		{"affine", "Affine"},
		{"bittensor", "Bittensor"},
		{"webui", "WebUI"},
		{"api", "API"},
		{"mcp", "MCP"},
		{"llm", "LLM"},
		{"tao", "TAO"},
	}
	for _, repl := range replacements {
		value = replaceSessionTitleWord(value, repl.from, repl.to)
	}
	return value
}

func replaceSessionTitleWord(text, from, to string) string {
	re := regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(from) + `\b`)
	return re.ReplaceAllString(text, to)
}

func normalizeSessionTitleComparable(text string) string {
	return strings.ToLower(textutil.CompactWhitespace(text))
}

func truncateSessionTitle(text string, limit int) string {
	return textutil.PreviewRunes(text, limit, "...")
}

func summarizeLatestUserMessage(text string) string {
	text = unwrapSessionSummaryUserPrompt(text)
	singleLine := textutil.CompactWhitespace(text)
	return textutil.PreviewRunes(singleLine, maxSessionTaskSummaryChars, "...")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func unwrapSessionSummaryUserPrompt(text string) string {
	for _, marker := range []string{
		"\nOriginal user request:\n",
		"\nUser confirmation/request:\n",
	} {
		if _, tail, ok := strings.Cut(text, marker); ok {
			tail = strings.TrimSpace(tail)
			if tail != "" {
				return tail
			}
		}
	}
	return text
}

func isContinuationSessionPrompt(text string) bool {
	normalized := strings.ToLower(textutil.CompactWhitespace(text))
	if normalized == "" {
		return false
	}
	for _, prefix := range []string{
		"continue",
		"resume",
		"please continue",
		"continue from",
		"continue after",
		"continue with",
		"continue the same",
		"same task",
		"use this",
		"use the already",
		"based on previous",
		"based on the previous",
		"based on already collected",
		"based on existing",
		"go on",
		"pick up",
		"继续",
		"请继续",
		"继续完成",
		"从这里继续",
		"接着",
		"同一个任务",
		"上一轮",
		"基于本",
		"基于已有",
		"基于前面",
		"基于上面",
		"不要再调用",
		"不要使用工具",
		"直接基于",
	} {
		if strings.HasPrefix(normalized, prefix) {
			return true
		}
	}
	return false
}

func durableRegularFileModTime(path string) (bool, time.Time, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, time.Time{}, nil
		}
		return false, time.Time{}, err
	}
	if fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
		return false, time.Time{}, nil
	}
	return true, fi.ModTime(), nil
}

func durableMemoryExists(sessionDir, userMemoryPath string) bool {
	for _, path := range []string{
		userMemoryPath,
		filepath.Join(sessionDir, "core.md"),
		filepath.Join(sessionDir, "MEMORY.md"),
		filepath.Join(sessionDir, "topics"),
	} {
		if durableStatePathExists(path) {
			return true
		}
	}
	return false
}

func durableRuntimeSkillNames(dir string) []string {
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil
	}
	f, err := os.Open(dir)
	if err != nil {
		return nil
	}
	defer f.Close()

	var names []string
	for len(names) < maxSessionRuntimeSkillNames {
		entries, err := f.ReadDir(sessionReadDirBatch)
		for _, ent := range entries {
			if len(names) >= maxSessionRuntimeSkillNames {
				break
			}
			if ent.Type()&os.ModeSymlink != 0 || !ent.IsDir() || strings.HasPrefix(ent.Name(), ".") {
				continue
			}
			if !durableRuntimeSkillDirComplete(filepath.Join(dir, ent.Name())) {
				continue
			}
			names = append(names, ent.Name())
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			break
		}
	}
	sort.Strings(names)
	return names
}

func durableRuntimeSkillDirComplete(dir string) bool {
	for _, name := range []string{"skill.json", "SKILL.md"} {
		exists, _, err := durableRegularFileModTime(filepath.Join(dir, name))
		if err != nil || !exists {
			return false
		}
	}
	return true
}

func mergeStringLists(a, b []string) []string {
	if len(a) == 0 {
		return append([]string(nil), b...)
	}
	if len(b) == 0 {
		return a
	}
	seen := make(map[string]bool, len(a)+len(b))
	out := make([]string, 0, len(a)+len(b))
	for _, v := range a {
		if seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	for _, v := range b {
		if seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

func durableStatePathExists(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode()&os.ModeSymlink == 0
}

func durableReadDir(dir string) ([]os.DirEntry, error) {
	info, err := os.Lstat(dir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, errors.New("durable path is not a directory")
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("durable path must not be a symlink")
	}
	return os.ReadDir(dir)
}

func rejectSymlinkUnderDir(root, rel string) error {
	clean := filepath.Clean(rel)
	if clean == "." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || filepath.IsAbs(clean) {
		return errors.New("path escapes root")
	}
	cur := root
	for _, part := range strings.Split(clean, string(filepath.Separator)) {
		if part == "" || part == "." {
			continue
		}
		cur = filepath.Join(cur, part)
		info, err := os.Lstat(cur)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("durable path must not contain symlinks")
		}
	}
	return nil
}

func durableDirEntryIsSymlink(ent os.DirEntry) bool {
	return ent.Type()&os.ModeSymlink != 0
}

func dirHasAnyEntry(dir string) bool {
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return false
	}
	f, err := os.Open(dir)
	if err != nil {
		return false
	}
	defer f.Close()
	entries, err := f.ReadDir(1)
	return err == nil && len(entries) > 0
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func newerFormattedTime(a, b string) string {
	if a == "" {
		return b
	}
	if b == "" {
		return a
	}
	at, errA := time.Parse(time.RFC3339, a)
	bt, errB := time.Parse(time.RFC3339, b)
	if errA == nil && errB == nil && bt.After(at) {
		return b
	}
	return a
}
