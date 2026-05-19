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
	TypeTurnStart      = "turn.start"
	TypeUserMessage    = "user.message"
	TypeMessageDelta   = "message.delta"
	TypeMessageDone    = "message.done"
	TypeThinkingDelta  = "thinking.delta"
	TypeThinkingDone   = "thinking.done"
	TypeToolRequest    = "tool.request"
	TypeToolOutput     = "tool.output"
	TypeToolResult     = "tool.result"
	TypeFileChanged    = "file.changed"
	TypeUsage          = "usage"
	TypeTurnEnd        = "turn.end"
	TypeError          = "error"
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
	TurnID string         `json:"turn_id"`
	CallID string         `json:"call_id"`
	Tool   string         `json:"tool"`
	Args   map[string]any `json:"args"`
}

type ToolOutputPayload struct {
	CallID string `json:"call_id"`
	Stream string `json:"stream"` // "stdout" | "stderr"
	Chunk  string `json:"chunk"`
}

type ToolResultPayload struct {
	CallID   string `json:"call_id"`
	ExitCode int    `json:"exit_code"`
	// ResultSummary is a short, UI-friendly preview of the tool's
	// output (capped for chat-bubble rendering). It may be truncated
	// with an ellipsis suffix and is NOT safe to JSON-parse.
	ResultSummary string `json:"result_summary"`
	// Result is the full tool output as the tool itself returned it
	// (no event-side truncation). Trace and evaluation consumers that
	// need to parse structured tool responses should read Result.
	// Front-ends that only render the value should read ResultSummary.
	Result string `json:"result"`
}

type FileChangedPayload struct {
	Path   string `json:"path"`
	Change string `json:"change"` // "created" | "modified" | "deleted"
}

type UsagePayload struct {
	TurnID       string  `json:"turn_id"`
	InputTokens  int     `json:"input_tokens"`
	OutputTokens int     `json:"output_tokens"`
	CostUSD      float64 `json:"cost_usd"`
}

// TurnEndReason values.
const (
	TurnEndCompleted = "completed"
	TurnEndCancelled = "cancelled"
	TurnEndError     = "error"
)

type TurnEndPayload struct {
	TurnID string `json:"turn_id"`
	Reason string `json:"reason"`
}

type ErrorPayload struct {
	Code        string `json:"code"`
	Message     string `json:"message"`
	Recoverable bool   `json:"recoverable"`
}
