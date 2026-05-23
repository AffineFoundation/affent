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
	TypeError         = "error"
)

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

type ToolRequestPayload struct {
	TurnID string `json:"turn_id"`
	CallID string `json:"call_id"`
	Tool   string `json:"tool"`
	// Args is the repaired argument object capped for event transport.
	// Small values are exact; large strings may include a truncation
	// marker, and extremely large argument objects may be replaced with
	// __affent_truncated metadata.
	Args                map[string]any `json:"args"`
	OriginalTool        string         `json:"original_tool,omitempty"`
	OriginalArgsSummary string         `json:"original_args_summary,omitempty"`
	Canonicalized       bool           `json:"canonicalized,omitempty"`
	ArgsRepaired        bool           `json:"args_repaired,omitempty"`
	RepairNotes         []string       `json:"repair_notes,omitempty"`
}

type ToolResultPayload struct {
	CallID   string `json:"call_id"`
	ExitCode int    `json:"exit_code"`
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
	Result string `json:"result"`
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

type ToolRuntimeStats struct {
	ToolRequests          int `json:"tool_requests,omitempty"`
	ToolNameCanonicalized int `json:"tool_name_canonicalized,omitempty"`
	ToolArgsRepaired      int `json:"tool_args_repaired,omitempty"`
	// ToolErrors counts tool results emitted with exit_code != 0,
	// including guard rejections and skipped calls.
	ToolErrors             int `json:"tool_errors,omitempty"`
	LoopGuardInterventions int `json:"loop_guard_interventions,omitempty"`
	ForcedNoTools          int `json:"forced_no_tools,omitempty"`
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
	Recoverable bool   `json:"recoverable"`
}
