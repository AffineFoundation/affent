package sse

// Canonical event type strings. Frontend imports the same set via OpenAPI/types
// generation later; for now the source of truth is this file + docs.
// Event-type naming: ".done" means a streaming accumulation is complete
// (the carried payload is the final value, more events for the same turn
// may still follow). ".end" is a turn-level boundary: the loop has
// finished and there will be no more events for that turn. Don't use
// ".end" for stream-completion events — readers misread it as a boundary
// and fail to wait for the next message.* / tool.* / etc. events.
const (
	TypeTraceMeta              = "trace.meta"
	TypeConversationRepaired   = "conversation.repaired"
	TypeTurnStart              = "turn.start"
	TypeUserMessage            = "user.message"
	TypeRuntimeSurface         = "runtime.surface"
	TypeContextInjected        = "context.injected"
	TypeMessageDelta           = "message.delta"
	TypeMessageDone            = "message.done"
	TypeMessageRejected        = "message.rejected"
	TypeThinkingDelta          = "thinking.delta"
	TypeThinkingDone           = "thinking.done"
	TypeToolRequest            = "tool.request"
	TypeToolResult             = "tool.result"
	TypeUsage                  = "usage"
	TypeTurnEnd                = "turn.end"
	TypeLoopProtocolFeed       = "loop.protocol_feed"
	TypeLoopCalibrationRequest = "loop.protocol_calibration_request"
	TypeLoopCalibration        = "loop.protocol_calibration"
	TypeLoopActivation         = "loop.protocol_activate"
	TypeLoopDecision           = "loop.decision"
	TypeLoopTurnCheckpoint     = "loop.turn_checkpoint"
	TypeContextCompact         = "context.compacted"
	TypeContextCompactSkipped  = "context.compaction_skipped"
	TypeError                  = "error"
)

const TraceSchemaVersion = 1

type TraceMetaPayload struct {
	SchemaVersion int `json:"schema_version"`
}

type ConversationRepairedPayload struct {
	SessionID             string `json:"session_id,omitempty"`
	MissingToolResults    int    `json:"missing_tool_results,omitempty"`
	DuplicateToolResults  int    `json:"duplicate_tool_results,omitempty"`
	UnexpectedToolResults int    `json:"unexpected_tool_results,omitempty"`
	FailureKind           string `json:"failure_kind,omitempty"`
	Next                  string `json:"next,omitempty"`
}

type TurnStartPayload struct {
	TurnID string `json:"turn_id"`
}

// UserMessagePayload is emitted right after turn.start with the literal
// text the user (or the cron scheduler) just sent. Persisting it in the
// event stream means SSE replays show the full conversation, not just
// assistant output.
type UserMessagePayload struct {
	TurnID       string `json:"turn_id"`
	Text         string `json:"text"`
	DisplayText  string `json:"display_text,omitempty"`
	Mode         string `json:"mode,omitempty"`
	Source       string `json:"source,omitempty"`
	ScheduleID   string `json:"schedule_id,omitempty"`
	ScheduleKind string `json:"schedule_kind,omitempty"`
}

// RuntimeSurfacePayload snapshots the effective runtime surface for a turn.
// It is trace/UI-only metadata: not fed back into the model. Operators use it
// to debug "why did the agent not use web/browser/memory?" without inferring
// the registered tools from later model behavior.
type RuntimeSurfacePayload struct {
	TurnID                             string               `json:"turn_id"`
	ToolCount                          int                  `json:"tool_count"`
	Tools                              []RuntimeSurfaceTool `json:"tools,omitempty"`
	AvailableToolCount                 int                  `json:"available_tool_count,omitempty"`
	ExcludedToolCount                  int                  `json:"excluded_tool_count,omitempty"`
	ExcludedTools                      []RuntimeSurfaceTool `json:"excluded_tools,omitempty"`
	ToolCallCaps                       []RuntimeToolCallCap `json:"tool_call_caps,omitempty"`
	CompletionGuards                   []string             `json:"completion_guards,omitempty"`
	Capabilities                       RuntimeCapabilities  `json:"capabilities"`
	Workspace                          *RuntimeWorkspace    `json:"workspace,omitempty"`
	MaxTurnSteps                       int                  `json:"max_turn_steps,omitempty"`
	MaxToolCalls                       int                  `json:"max_tool_calls,omitempty"`
	MaxTurnInputTokens                 int                  `json:"max_turn_input_tokens,omitempty"`
	ModelContextWindowTokens           int                  `json:"model_context_window_tokens,omitempty"`
	ModelContextWindowAuto             bool                 `json:"model_context_window_auto,omitempty"`
	ModelContextWindowEffectivePercent int                  `json:"model_context_window_effective_percent,omitempty"`
	ReservedOutputTokens               int                  `json:"reserved_output_tokens,omitempty"`
	CompactTriggerInputTokens          int                  `json:"compact_trigger_input_tokens,omitempty"`
	CompactTriggerInputPercent         int                  `json:"compact_trigger_input_percent,omitempty"`
	CompactScopeActive                 bool                 `json:"compact_scope_active,omitempty"`
	CompactWindowOrdinal               int64                `json:"compact_window_ordinal,omitempty"`
	CompactWindowPrefillInputTokens    int                  `json:"compact_window_prefill_input_tokens,omitempty"`
	CompactWindowPrefillSource         string               `json:"compact_window_prefill_source,omitempty"`
	CompactScopedInputTokens           int                  `json:"compact_scoped_input_tokens,omitempty"`
	CompactHardInputLimitTokens        int                  `json:"compact_hard_input_limit_tokens,omitempty"`
	CompactSummaryPromptMaxBytes       int                  `json:"compact_summary_prompt_max_bytes,omitempty"`
	ConversationBytes                  int                  `json:"conversation_bytes,omitempty"`
	ToolSchemaBytes                    int                  `json:"tool_schema_bytes,omitempty"`
	ToolSchemaBudgetTokens             int                  `json:"tool_schema_budget_tokens,omitempty"`
	EstimatedConversationTokens        int                  `json:"estimated_conversation_tokens,omitempty"`
	EstimatedToolSchemaTokens          int                  `json:"estimated_tool_schema_tokens,omitempty"`
	EstimatedRequestInputTokens        int                  `json:"estimated_request_input_tokens,omitempty"`
	ToolResultEventCapBytes            int                  `json:"tool_result_event_cap_bytes,omitempty"`
	ToolResultContextMaxBytes          int                  `json:"tool_result_context_max_bytes,omitempty"`
	ToolResultContextBudgetBytes       int                  `json:"tool_result_context_budget_bytes,omitempty"`
	ToolResultArtifactPrefix           string               `json:"tool_result_artifact_prefix,omitempty"`
	TurnToolOverride                   bool                 `json:"turn_tool_override,omitempty"`
}

type RuntimeWorkspace struct {
	DefaultCWD           string                  `json:"default_cwd,omitempty"`
	PathMode             string                  `json:"path_mode,omitempty"`
	Root                 string                  `json:"root,omitempty"`
	RootEntries          []RuntimeWorkspaceEntry `json:"root_entries,omitempty"`
	RootEntryCount       int                     `json:"root_entry_count,omitempty"`
	RootEntriesTruncated bool                    `json:"root_entries_truncated,omitempty"`
}

type RuntimeWorkspaceEntry struct {
	Name string `json:"name"`
	Kind string `json:"kind,omitempty"`
}

type RuntimeSurfaceTool struct {
	Name      string                `json:"name"`
	RawName   string                `json:"raw_name,omitempty"`
	Group     string                `json:"group,omitempty"`
	Source    string                `json:"source,omitempty"`
	ArgPolicy *RuntimeToolArgPolicy `json:"arg_policy,omitempty"`
}

type RuntimeToolArgPolicy struct {
	WorkspacePathArgs []string `json:"workspace_path_args,omitempty"`
}

type RuntimeToolCallCap struct {
	Tool string `json:"tool"`
	Max  int    `json:"max"`
}

type RuntimeCapabilities struct {
	Builtins        bool     `json:"builtins,omitempty"`
	WorkspaceTools  []string `json:"workspace_tools,omitempty"`
	Memory          bool     `json:"memory,omitempty"`
	Plan            bool     `json:"plan,omitempty"`
	LoopProtocol    bool     `json:"loop_protocol,omitempty"`
	SessionSchedule bool     `json:"session_schedule,omitempty"`
	// SessionScheduleRunner means scheduled turns are owned by the server
	// process and can fire without an attached browser/WebUI client.
	SessionScheduleRunner bool `json:"session_schedule_runner,omitempty"`
	SessionSearch         bool `json:"session_search,omitempty"`
	WebFetch              bool `json:"web_fetch,omitempty"`
	WebSearch             bool `json:"web_search,omitempty"`
	Browser               bool `json:"browser,omitempty"`
	Subagent              bool `json:"subagent,omitempty"`
	FocusedTasks          bool `json:"focused_tasks,omitempty"`
	Skill                 bool `json:"skill,omitempty"`
	MCP                   bool `json:"mcp,omitempty"`
}

// ContextInjectedPayload records a bounded, redacted summary of hidden system
// context appended for this turn. It lets trace consumers understand prompt
// pressure and dynamic guidance without exposing the full prompt block.
type ContextInjectedPayload struct {
	TurnID          string `json:"turn_id"`
	Source          string `json:"source"`
	Name            string `json:"name,omitempty"`
	Title           string `json:"title"`
	Summary         string `json:"summary,omitempty"`
	Preview         string `json:"preview,omitempty"`
	ContentSHA256   string `json:"content_sha256,omitempty"`
	Bytes           int    `json:"bytes,omitempty"`
	EstimatedTokens int    `json:"estimated_tokens,omitempty"`
}

type MessageDeltaPayload struct {
	TurnID string `json:"turn_id"`
	Delta  string `json:"delta"`
}

type MessageDonePayload struct {
	TurnID string `json:"turn_id"`
	Text   string `json:"text"`
	// FinishReason mirrors the upstream OpenAI-compat `finish_reason`
	// for this assistant message: "stop" (model finished naturally),
	// "length" (max_tokens hit — content is TRUNCATED), "tool_calls"
	// (turn continues with a tool round-trip), "content_filter", and
	// provider-specific extensions. Empty on rare streams that close
	// without one. Consumers use it to flag "this answer was cut off
	// at the model's output cap, not because the model thought it was
	// done" — otherwise a length-truncated reply looks identical to a
	// short complete one and confuses UIs and eval rigs alike.
	FinishReason string `json:"finish_reason,omitempty"`
}

type MessageRejectedPayload struct {
	TurnID         string `json:"turn_id"`
	Text           string `json:"text,omitempty"`
	Reason         string `json:"reason,omitempty"`
	Trigger        string `json:"trigger,omitempty"`
	RequiredAction string `json:"required_action,omitempty"`
}

type ThinkingDeltaPayload struct {
	TurnID string `json:"turn_id"`
	Delta  string `json:"delta"`
}

// ThinkingDonePayload closes a reasoning stream with the full accumulated
// text. Mirrors MessageDonePayload so trace consumers running with
// --trace-skip-deltas still see the reasoning content for the turn.
type ThinkingDonePayload struct {
	TurnID string `json:"turn_id"`
	Text   string `json:"text"`
}

// DelegationMeta classifies a tool call as a bounded child-Loop
// delegation (focused task, subagent) and carries the small set of
// fields a trace UI or eval pipeline most often wants to filter on.
// Surfaced as a side-channel on Tool{Request,Result}Payload so
// consumers do not need to parse tool-specific argument or result
// schemas to ask "which delegation kind was this?".
//
// Kind values are stable: "focused_task" or "subagent". Add new kinds
// as new delegation surfaces appear; do not reuse existing values for
// different semantics.
type DelegationMeta struct {
	// Kind is the delegation surface ("focused_task" or "subagent").
	// Always set when DelegationMeta is non-nil.
	Kind string `json:"kind"`
	// TaskType is set when Kind == "focused_task" — the run_task tool's
	// task_type argument (recall, explore, web_extract, research, verify,
	// review).
	TaskType string `json:"task_type,omitempty"`
	// Mode is set when Kind == "subagent" — the subagent_run tool's
	// mode argument (explore, review, test, research, ...).
	Mode string `json:"mode,omitempty"`
}

type ToolRequestPayload struct {
	TurnID string `json:"turn_id"`
	CallID string `json:"call_id"`
	Tool   string `json:"tool"`
	// Args is the repaired argument object capped for event transport.
	// Small values are exact; large strings may include a truncation
	// marker, and extremely large argument objects may be replaced with
	// __affent_truncated metadata.
	Args map[string]any `json:"args"`
	// Args* fields describe event-level argument capping so UIs/evals
	// do not need to parse marker strings in Args.
	ArgsTruncated       bool     `json:"args_truncated"`
	ArgsBytes           int      `json:"args_bytes"`
	ArgsOmittedBytes    int      `json:"args_omitted_bytes"`
	ArgsCapBytes        int      `json:"args_cap_bytes"`
	OriginalTool        string   `json:"original_tool,omitempty"`
	OriginalArgsSummary string   `json:"original_args_summary,omitempty"`
	Canonicalized       bool     `json:"canonicalized,omitempty"`
	ArgsRepaired        bool     `json:"args_repaired,omitempty"`
	RepairNotes         []string `json:"repair_notes,omitempty"`
	// Skipped marks protocol placeholder requests that were not admitted
	// to runtime dispatch because the turn was cancelled, exhausted a
	// budget, or a guard forced a no-tool continuation. These events keep
	// the conversation protocol consistent while allowing UIs/evals to
	// distinguish model-emitted calls from admitted runtime work.
	Skipped         bool   `json:"skipped,omitempty"`
	SkipFailureKind string `json:"skip_failure_kind,omitempty"`
	// Delegation, when set, classifies this tool call as a bounded
	// child-Loop delegation. Nil for normal tools.
	Delegation *DelegationMeta `json:"delegation,omitempty"`
}

type ToolResultPayload struct {
	TurnID   string `json:"turn_id,omitempty"`
	CallID   string `json:"call_id"`
	ExitCode int    `json:"exit_code"`
	// FailureKind is a machine-readable classification extracted from
	// structured tool output lines such as "Failure: kind=blocked".
	// Empty means the tool did not publish a structured failure kind.
	FailureKind string `json:"failure_kind,omitempty"`
	// FailureKinds carries every structured failure kind present on the result.
	// It is a superset of FailureKind for results that combine an underlying
	// tool failure with a runtime guard classification.
	FailureKinds []string `json:"failure_kinds,omitempty"`
	// DurationMS is present only for tool implementations that were
	// actually dispatched. Guard rejections and skipped calls omit it.
	DurationMS int64 `json:"duration_ms,omitempty"`
	// ResultSummary is a short, UI-friendly preview of the tool's
	// output (capped for chat-bubble rendering). It may be truncated
	// with an ellipsis suffix and is NOT safe to JSON-parse.
	ResultSummary string `json:"result_summary"`
	// Result is the tool output capped by the agent event budget. Trace
	// and evaluation consumers that need to parse structured tool
	// responses should read Result, but must still tolerate a trailing
	// truncation marker for oversized outputs.
	// Front-ends that only render the value should read ResultSummary.
	Result             string `json:"result"`
	ResultTruncated    bool   `json:"result_truncated"`
	ResultBytes        int    `json:"result_bytes"`
	ResultOmittedBytes int    `json:"result_omitted_bytes"`
	ResultCapBytes     int    `json:"result_cap_bytes"`
	// ContextBytes is the exact byte length of the tool result text that was
	// appended to the parent model conversation after per-tool and per-turn
	// context caps. ContextEstimatedTokens is a tokenizer-free estimate for UI
	// budgeting; provider tokenizers differ, so exact token attribution is only
	// available from the next upstream usage event.
	ContextBytes           int `json:"context_bytes,omitempty"`
	ContextOmittedBytes    int `json:"context_omitted_bytes,omitempty"`
	ContextEstimatedTokens int `json:"context_estimated_tokens,omitempty"`
	// ResultArtifactPath is the relative artifact path to the complete tool
	// output when the event result or model-context result was truncated and
	// the loop has artifact persistence configured. In CLI/eval runs this is
	// usually workspace-relative; affentserve resolves it under the durable
	// session artifact API. Empty means no full artifact is available.
	ResultArtifactPath string `json:"result_artifact_path,omitempty"`
	// Delegation mirrors the value on the matching ToolRequestPayload so
	// trace consumers that subscribe mid-stream (or render events out
	// of order) can classify a result without having to JOIN by CallID
	// against the earlier request event.
	Delegation *DelegationMeta `json:"delegation,omitempty"`
	// MemoryUpdate summarizes a confirmed durable memory mutation so trace
	// consumers and WebUI do not need to recover update content from capped
	// tool.request args or large memory response JSON.
	MemoryUpdate *MemoryUpdateMeta `json:"memory_update,omitempty"`
}

type MemoryUpdateMeta struct {
	Action          string `json:"action"`
	Target          string `json:"target"`
	Topic           string `json:"topic,omitempty"`
	Location        string `json:"location"`
	Preview         string `json:"preview"`
	PreviousPreview string `json:"previous_preview,omitempty"`
	NextPreview     string `json:"next_preview,omitempty"`
}

type UsagePayload struct {
	TurnID       string `json:"turn_id"`
	InputTokens  int    `json:"input_tokens"`
	OutputTokens int    `json:"output_tokens"`
}

// TurnEndReason values.
const (
	TurnEndCompleted = "completed"
	TurnEndCancelled = "cancelled"
	TurnEndError     = "error"
	// TurnEndMaxTurns fires when the loop exhausted Loop.MaxTurnSteps
	// while the model was still issuing tool calls. The conversation is
	// left consistent (last role=tool result is the final entry); the
	// next user message starts a new turn from there.
	TurnEndMaxTurns = "max_turns"
)

type TurnEndPayload struct {
	TurnID    string            `json:"turn_id"`
	Reason    string            `json:"reason"`
	ToolStats *ToolRuntimeStats `json:"tool_stats,omitempty"`
}

// LoopProtocolFeedPayload records when a session's LOOP.md was injected into
// model context. The durable loop state owns the cadence; this event mirrors
// that decision into the normal session trace/SSE stream so WebUI and evals can
// inspect long-run context pressure without reading sidecar loop files.
type LoopProtocolFeedPayload struct {
	TurnID                       string `json:"turn_id,omitempty"`
	LoopID                       string `json:"loop_id,omitempty"`
	Status                       string `json:"status,omitempty"`
	Mode                         string `json:"mode"`
	FeedNumber                   int    `json:"feed_number"`
	ProtocolFeeds                int    `json:"protocol_feeds,omitempty"`
	CalibrationAnswers           int    `json:"calibration_answers,omitempty"`
	LastCalibrationAnswer        string `json:"last_calibration_answer_preview,omitempty"`
	ProtocolPath                 string `json:"protocol_path,omitempty"`
	CurrentSituation             string `json:"current_situation_preview,omitempty"`
	PlanLabel                    string `json:"plan_label,omitempty"`
	PlanCurrentStepIndex         int    `json:"plan_current_step_index,omitempty"`
	PlanCurrentStepStatus        string `json:"plan_current_step_status,omitempty"`
	PlanCurrentStep              string `json:"plan_current_step,omitempty"`
	LastTurnID                   string `json:"last_turn_id,omitempty"`
	LastTurnEndReason            string `json:"last_turn_end_reason,omitempty"`
	LastTurnToolRequests         int    `json:"last_turn_tool_requests,omitempty"`
	LastTurnToolRequestsAdmitted int    `json:"last_turn_tool_requests_admitted,omitempty"`
	LastTurnToolRequestsSkipped  int    `json:"last_turn_tool_requests_skipped,omitempty"`
	LastTurnToolErrors           int    `json:"last_turn_tool_errors,omitempty"`
	LastTurnForcedNoTools        int    `json:"last_turn_forced_no_tools,omitempty"`
	LastTurnMemoryUpdates        int    `json:"last_turn_memory_updates,omitempty"`
	LastTurnMemorySearchCalls    int    `json:"last_turn_memory_search_calls,omitempty"`
	LastTurnMemorySearchMisses   int    `json:"last_turn_memory_search_misses,omitempty"`
	LastTurnSessionSearchCalls   int    `json:"last_turn_session_search_calls,omitempty"`
	LastTurnLoopGuards           int    `json:"last_turn_loop_guards,omitempty"`
	LastDecisionKind             string `json:"last_decision_kind,omitempty"`
	LastDecisionTrigger          string `json:"last_decision_trigger,omitempty"`
	LastDecision                 string `json:"last_decision,omitempty"`
	LastDecisionConfidence       string `json:"last_decision_confidence,omitempty"`
	LastDecisionReason           string `json:"last_decision_reason,omitempty"`
	LastDecisionAction           string `json:"last_decision_required_action,omitempty"`
	LastDecisionTokenBudget      int    `json:"last_decision_token_budget,omitempty"`
	LastDecisionObservedInput    int    `json:"last_decision_observed_input_tokens,omitempty"`
	LastDecisionProjectedInput   int    `json:"last_decision_projected_input_tokens,omitempty"`
	LastDecisionBudgetBytes      int    `json:"last_decision_budget_bytes,omitempty"`
}

// LoopProtocolCalibrationPayload mirrors draft LOOP.md calibration questions
// and accepted user answers from the sidecar loop event log into the normal
// session trace/SSE stream.
type LoopProtocolCalibrationPayload struct {
	LoopID                  string `json:"loop_id,omitempty"`
	Status                  string `json:"status,omitempty"`
	CalibrationQuestions    int    `json:"calibration_questions,omitempty"`
	LastCalibrationQuestion string `json:"last_calibration_question_preview,omitempty"`
	CalibrationAnswers      int    `json:"calibration_answers,omitempty"`
	LastCalibrationAnswer   string `json:"last_calibration_answer_preview,omitempty"`
	ProtocolPath            string `json:"protocol_path,omitempty"`
	EventSeq                int    `json:"event_seq,omitempty"`
}

// LoopProtocolActivationPayload mirrors successful draft-to-running LOOP.md
// activation into the normal session trace/SSE stream.
type LoopProtocolActivationPayload struct {
	TurnID          string `json:"turn_id,omitempty"`
	LoopID          string `json:"loop_id,omitempty"`
	Status          string `json:"status,omitempty"`
	ProtocolUpdates int    `json:"protocol_updates,omitempty"`
	ProtocolPath    string `json:"protocol_path,omitempty"`
	EventSeq        int    `json:"event_seq,omitempty"`
}

// LoopDecisionPayload records one short protocol decision made outside the
// normal assistant message stream. It is intended for checkpoint/feed/stop
// decisions that must be visible in trace and WebUI without requiring users
// to read hidden prompt text.
type LoopDecisionPayload struct {
	TurnID         string `json:"turn_id,omitempty"`
	LoopID         string `json:"loop_id,omitempty"`
	DecisionID     string `json:"decision_id,omitempty"`
	Kind           string `json:"kind"`
	Trigger        string `json:"trigger,omitempty"`
	Decision       string `json:"decision"`
	Confidence     string `json:"confidence,omitempty"`
	Reason         string `json:"reason,omitempty"`
	RequiredAction string `json:"required_action,omitempty"`
	TokenBudget    int    `json:"token_budget,omitempty"`
	// Input-token budget decisions use these fields so Workbench and evals do
	// not need to parse human-readable Reason text.
	ObservedInputTokens  int `json:"observed_input_tokens,omitempty"`
	ProjectedInputTokens int `json:"projected_input_tokens,omitempty"`
	BudgetBytes          int `json:"budget_bytes,omitempty"`
	// VisibleInUI is nil by default, which consumers should treat as visible.
	// Use a pointer so an explicit false still survives JSON omitempty.
	VisibleInUI *bool `json:"visible_in_ui,omitempty"`
}

// LoopTurnCheckpointPayload mirrors a successfully persisted per-turn loop
// checkpoint into the normal trace stream. The durable sidecar files remain
// authoritative; this event lets WebUI/eval consumers prove the checkpoint was
// written without opening .affent/loops/<id>/state.json.
type LoopTurnCheckpointPayload struct {
	TurnID                   string `json:"turn_id"`
	LoopID                   string `json:"loop_id,omitempty"`
	Status                   string `json:"status,omitempty"`
	ProtocolPath             string `json:"protocol_path,omitempty"`
	FinalizationPolicy       string `json:"finalization_policy,omitempty"`
	RequiresCloseBeforeFinal bool   `json:"requires_close_before_final,omitempty"`
	EventSeq                 int    `json:"event_seq,omitempty"`
	TurnCheckpoints          int    `json:"turn_checkpoints,omitempty"`
	EndReason                string `json:"end_reason,omitempty"`
	InputTokens              int    `json:"input_tokens,omitempty"`
	OutputTokens             int    `json:"output_tokens,omitempty"`
	ToolRequests             int    `json:"tool_requests,omitempty"`
	ToolRequestsAdmitted     int    `json:"tool_requests_admitted,omitempty"`
	ToolRequestsSkipped      int    `json:"tool_requests_skipped,omitempty"`
	ToolErrors               int    `json:"tool_errors,omitempty"`
	LoopGuards               int    `json:"loop_guards,omitempty"`
	ForcedNoTools            int    `json:"forced_no_tools,omitempty"`
	MemoryUpdates            int    `json:"memory_updates,omitempty"`
	MemorySearchCalls        int    `json:"memory_search_calls,omitempty"`
	MemoryMisses             int    `json:"memory_search_misses,omitempty"`
	SessionSearchCalls       int    `json:"session_search_calls,omitempty"`
}

// ContextCompactPayload records when the model conversation was rewritten into
// a shorter rolling summary. SummaryPreview is bounded by the runtime before
// publication so trace/UI can inspect what state survived without receiving an
// unbounded copy of the conversation.
type ContextCompactPayload struct {
	TurnID                             string `json:"turn_id,omitempty"`
	BeforeMessages                     int    `json:"before_messages"`
	AfterMessages                      int    `json:"after_messages"`
	RemovedMessages                    int    `json:"removed_messages"`
	BeforeBytes                        int    `json:"before_bytes,omitempty"`
	AfterBytes                         int    `json:"after_bytes,omitempty"`
	ReducedBytes                       int    `json:"reduced_bytes,omitempty"`
	EstimatedInputTokens               int    `json:"estimated_input_tokens,omitempty"`
	AfterEstimatedInputTokens          int    `json:"after_estimated_input_tokens,omitempty"`
	TriggerInputTokens                 int    `json:"trigger_input_tokens,omitempty"`
	ModelContextWindowTokens           int    `json:"model_context_window_tokens,omitempty"`
	ModelContextWindowEffectivePercent int    `json:"model_context_window_effective_percent,omitempty"`
	ReservedOutputTokens               int    `json:"reserved_output_tokens,omitempty"`
	CompactTriggerInputPercent         int    `json:"compact_trigger_input_percent,omitempty"`
	CompactScopeActive                 bool   `json:"compact_scope_active,omitempty"`
	CompactWindowOrdinal               int64  `json:"compact_window_ordinal,omitempty"`
	CompactWindowPrefillInputTokens    int    `json:"compact_window_prefill_input_tokens,omitempty"`
	CompactWindowPrefillSource         string `json:"compact_window_prefill_source,omitempty"`
	CompactScopedInputTokens           int    `json:"compact_scoped_input_tokens,omitempty"`
	CompactHardInputLimitTokens        int    `json:"compact_hard_input_limit_tokens,omitempty"`
	Reactive                           bool   `json:"reactive"`
	Reason                             string `json:"reason"`
	SummaryPresent                     bool   `json:"summary_present"`
	SummaryBytes                       int    `json:"summary_bytes,omitempty"`
	SummaryPreview                     string `json:"summary_preview,omitempty"`
	LoopProtocolAnchor                 string `json:"loop_protocol_anchor,omitempty"`
}

// ContextCompactSkippedPayload records a compaction candidate that was
// intentionally discarded before replacing conversation state.
type ContextCompactSkippedPayload struct {
	TurnID                             string `json:"turn_id,omitempty"`
	Cause                              string `json:"cause"`
	Reason                             string `json:"reason,omitempty"`
	BeforeMessages                     int    `json:"before_messages,omitempty"`
	CandidateMessages                  int    `json:"candidate_messages,omitempty"`
	BeforeBytes                        int    `json:"before_bytes,omitempty"`
	CandidateBytes                     int    `json:"candidate_bytes,omitempty"`
	EstimatedInputTokens               int    `json:"estimated_input_tokens,omitempty"`
	AfterEstimatedInputTokens          int    `json:"after_estimated_input_tokens,omitempty"`
	TriggerInputTokens                 int    `json:"trigger_input_tokens,omitempty"`
	ModelContextWindowTokens           int    `json:"model_context_window_tokens,omitempty"`
	ModelContextWindowEffectivePercent int    `json:"model_context_window_effective_percent,omitempty"`
	ReservedOutputTokens               int    `json:"reserved_output_tokens,omitempty"`
	CompactTriggerInputPercent         int    `json:"compact_trigger_input_percent,omitempty"`
	CompactScopeActive                 bool   `json:"compact_scope_active,omitempty"`
	CompactWindowOrdinal               int64  `json:"compact_window_ordinal,omitempty"`
	CompactWindowPrefillInputTokens    int    `json:"compact_window_prefill_input_tokens,omitempty"`
	CompactWindowPrefillSource         string `json:"compact_window_prefill_source,omitempty"`
	CompactScopedInputTokens           int    `json:"compact_scoped_input_tokens,omitempty"`
	CompactHardInputLimitTokens        int    `json:"compact_hard_input_limit_tokens,omitempty"`
}

type ToolRuntimeStats struct {
	ToolRequests          int            `json:"tool_requests,omitempty"`
	ToolRequestsAdmitted  int            `json:"tool_requests_admitted,omitempty"`
	ToolRequestsSkipped   int            `json:"tool_requests_skipped,omitempty"`
	ToolNameCanonicalized int            `json:"tool_name_canonicalized,omitempty"`
	ToolArgsRepaired      int            `json:"tool_args_repaired,omitempty"`
	ToolRepairCalls       int            `json:"tool_repair_calls,omitempty"`
	ToolRepairSucceeded   int            `json:"tool_repair_succeeded,omitempty"`
	ToolRepairFailed      int            `json:"tool_repair_failed,omitempty"`
	ToolRepairNotes       int            `json:"tool_repair_notes,omitempty"`
	ToolRepairByKind      map[string]int `json:"tool_repair_by_kind,omitempty"`
	// ToolFailureByKind classifies structured tool failures and
	// no-evidence web results. Some entries can come from exit_code=0
	// tool.results, for example web_fetch dynamic_shell or web_search
	// no_results, so this is not equivalent to ToolErrors.
	ToolFailureByKind map[string]int `json:"tool_failure_by_kind,omitempty"`
	// ToolErrors counts tool results emitted with exit_code != 0,
	// including guard rejections and skipped calls.
	ToolErrors int `json:"tool_errors,omitempty"`
	// ToolDurationMS sums measured implementation time for dispatched
	// tools. Guard rejections and skipped calls do not contribute.
	ToolDurationMS         int64 `json:"tool_duration_ms,omitempty"`
	LoopGuardInterventions int   `json:"loop_guard_interventions,omitempty"`
	ForcedNoTools          int   `json:"forced_no_tools,omitempty"`
	// SourceAccess counters classify tool results that expose normalized source
	// evidence headers. They measure evidence quality separately from raw tool
	// volume, so evals can distinguish verified reads from discovery-only pages.
	SourceAccessResults        int `json:"source_access_results,omitempty"`
	SourceAccessVerified       int `json:"source_access_verified,omitempty"`
	SourceAccessDiscoveryOnly  int `json:"source_access_discovery_only,omitempty"`
	SourceAccessNetwork        int `json:"source_access_network,omitempty"`
	SourceAccessDynamicPartial int `json:"source_access_dynamic_partial,omitempty"`
	// MemoryUpdate counters count confirmed durable memory mutations only:
	// memory tool calls with ok=true and action add/replace/remove.
	MemoryUpdates       int `json:"memory_updates,omitempty"`
	MemoryUpdateAdd     int `json:"memory_update_add,omitempty"`
	MemoryUpdateReplace int `json:"memory_update_replace,omitempty"`
	MemoryUpdateRemove  int `json:"memory_update_remove,omitempty"`
	// MemorySearchCalls counts dispatched memory search attempts.
	MemorySearchCalls int `json:"memory_search_calls,omitempty"`
	// MemorySearchMisses counts successful memory search calls that returned
	// no direct hits. It helps long-run operators distinguish "never looked"
	// from "looked, missed, and should retry from available anchors".
	MemorySearchMisses int `json:"memory_search_misses,omitempty"`
	// SessionSearch counters measure durable transcript recall quality.
	// Calls count dispatched attempts; result/context/term counters are
	// populated only when the session_search JSON response can be parsed.
	SessionSearchCalls        int `json:"session_search_calls,omitempty"`
	SessionSearchResults      int `json:"session_search_results,omitempty"`
	SessionSearchContextHits  int `json:"session_search_context_hits,omitempty"`
	SessionSearchMatchedTerms int `json:"session_search_matched_terms,omitempty"`
	SessionSearchRecent       int `json:"session_search_recent_sessions,omitempty"`
	// ToolContextTruncated counts tool results that were shortened before
	// being appended back into the model conversation. This is separate
	// from ToolResultPayload.ResultTruncated, which reports event payload
	// truncation for trace transport.
	ToolContextTruncated    int `json:"tool_context_truncated,omitempty"`
	ToolContextOmittedBytes int `json:"tool_context_omitted_bytes,omitempty"`
}

type ErrorPayload struct {
	// TurnID names the in-flight turn this error belongs to. Without
	// it, chat-completions handlers' eventForTurn filter silently
	// drops every error event — so the streaming path emits no
	// `data: error` chunk and the buffered path falls through to
	// the generic "turn ended with error" message instead of the
	// specific upstream cause. Populated by Loop.runStep at both
	// publish sites.
	TurnID      string `json:"turn_id"`
	Code        string `json:"code"`
	Message     string `json:"message"`
	FailureKind string `json:"failure_kind,omitempty"`
	Recoverable bool   `json:"recoverable"`
}
