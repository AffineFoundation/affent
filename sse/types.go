package sse

// Canonical event type strings. Frontend imports the same set via OpenAPI/types
// generation later; for now the source of truth is this file + docs.
const (
	TypeTurnStart      = "turn.start"
	TypeUserMessage    = "user.message"
	TypeMessageDelta   = "message.delta"
	TypeMessageEnd     = "message.end"
	TypeThinkingDelta  = "thinking.delta"
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

type MessageEndPayload struct {
	TurnID string `json:"turn_id"`
	Text   string `json:"text"`
}

type ThinkingDeltaPayload struct {
	TurnID string `json:"turn_id"`
	Delta  string `json:"delta"`
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
	CallID         string `json:"call_id"`
	ExitCode       int    `json:"exit_code"`
	ResultSummary  string `json:"result_summary"`
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
