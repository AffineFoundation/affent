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
	TypeTraceMeta     = "trace.meta"
	TypeTurnStart     = "turn.start"
	TypeUserMessage   = "user.message"
	TypeMessageDelta  = "message.delta"
	TypeMessageDone   = "message.done"
	TypeThinkingDelta = "thinking.delta"
	TypeThinkingDone  = "thinking.done"
	TypeToolRequest   = "tool.request"
	TypeToolResult    = "tool.result"
	TypeUsage         = "usage"
	TypeTurnEnd       = "turn.end"
	TypeLoopDecision  = "loop.decision"
	TypeError         = "error"
)

const TraceSchemaVersion = 1

type TraceMetaPayload struct {
	SchemaVersion int `json:"schema_version"`
}

type TurnStartPayload struct {
	TurnID string `json:"turn_id"`
}

// UserMessagePayload is emitted right after turn.start with the literal
// text the user (or the cron scheduler) just sent. Persisting it in the
// event stream means SSE replays show the full conversation, not just
// assistant output.
type UserMessagePayload struct {
	TurnID string `json:"turn_id"`
	Text   string `json:"text"`
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
	// ResultArtifactPath is a workspace-relative path to the complete
	// tool output when ResultTruncated is true and the loop has artifact
	// persistence configured. Empty means no full artifact is available.
	ResultArtifactPath string `json:"result_artifact_path,omitempty"`
	// Delegation mirrors the value on the matching ToolRequestPayload so
	// trace consumers that subscribe mid-stream (or render events out
	// of order) can classify a result without having to JOIN by CallID
	// against the earlier request event.
	Delegation *DelegationMeta `json:"delegation,omitempty"`
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
	// VisibleInUI is nil by default, which consumers should treat as visible.
	// Use a pointer so an explicit false still survives JSON omitempty.
	VisibleInUI *bool `json:"visible_in_ui,omitempty"`
}

type ToolRuntimeStats struct {
	ToolRequests          int            `json:"tool_requests,omitempty"`
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
