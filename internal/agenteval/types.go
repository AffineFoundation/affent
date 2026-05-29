package agenteval

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/affinefoundation/affent/internal/agent"
	"github.com/affinefoundation/affent/internal/executor"
	"github.com/affinefoundation/affent/internal/memory"
	"github.com/affinefoundation/affent/internal/sourceaccess"
	"github.com/affinefoundation/affent/internal/sse"
	"github.com/affinefoundation/affent/internal/taskstate"
	"github.com/affinefoundation/affent/internal/textutil"
	"github.com/affinefoundation/affent/internal/toolrepair"
)

// Scenario describes one bounded evaluation task. Scenarios are
// deterministic and reproducible: same Setup + same LLM (real or
// fake) + same Prompt/Prompts → same Trace.
//
// A Scenario does not own the LLM client or the runtime. Those come
// from the Runner so the same scenario can be replayed against
// multiple models / executors without rewriting it.
type Scenario struct {
	// Name is the short identifier used in logs and Outcome.Scenario.
	// Lowercase snake_case keeps it grep-friendly (e.g.
	// "go_failing_test_repair", "web_snapshot_taostats").
	Name string

	// Description is one sentence on what the scenario is measuring.
	// Surfaced in human-readable reports; not used programmatically.
	Description string

	// Prompt is the user message sent to the agent. May be the empty
	// string if Setup writes a longer multi-turn fixture (in which
	// case Prompts should carry the actual turn messages).
	Prompt string

	// Prompts is an optional ordered multi-turn prompt list. When set,
	// Prompt is only a display fallback and the runner sends each item
	// as a separate user turn against the same conversation.
	Prompts []string

	// PromptOptions carries per-turn runtime provenance for Prompts. Empty
	// entries use the default direct-user turn. When Prompts is empty, the
	// first option applies to Prompt.
	PromptOptions []PromptOptions

	// Setup populates the workspace directory before the run. May
	// be nil for scenarios that need no fixture (a "does the agent
	// refuse find /" scenario doesn't need any files on disk). The
	// directory is freshly created and removed by the Runner.
	Setup func(workspaceDir string) error

	// MaxTurnSteps overrides the Runner's default for this scenario.
	// Zero falls back to Runner.MaxTurnSteps. Long-horizon scenarios
	// raise this; quick refusal scenarios lower it.
	MaxTurnSteps int

	// Checks are the trace assertions that determine pass/fail. Each
	// Check sees the full captured Trace. Order is preserved in
	// Outcome.Results so reporting is stable.
	Checks []Check
}

type PromptOptions struct {
	UserSource      string
	UserDisplayText string
	ScheduleID      string
	ScheduleKind    string
}

func (o PromptOptions) turnOptions() agent.TurnOptions {
	return agent.TurnOptions{
		UserSource:      strings.TrimSpace(o.UserSource),
		UserDisplayText: strings.TrimSpace(o.UserDisplayText),
		ScheduleID:      strings.TrimSpace(o.ScheduleID),
		ScheduleKind:    strings.TrimSpace(o.ScheduleKind),
	}
}

// Check is one named binary assertion over a Trace. Checks should be
// deterministic from the trace and its workspace artifacts: no network,
// clocks, mutation, or unbounded filesystem walks.
type Check struct {
	// Name shows up in CheckResult.Check and reports. Should be a
	// short rule statement, not a description of the implementation
	// (e.g. "ran_test_before_edit", not "checks_tool_order").
	Name string

	// Eval is the predicate. Should produce a non-empty Detail when
	// Pass is false — that's the diagnostic the caller gets in
	// failure reports.
	Eval func(t Trace) CheckResult
}

// CheckResult is one Check's verdict on a Trace.
type CheckResult struct {
	// Check is the Check.Name that produced this result.
	Check string
	// Pass is true when the assertion held.
	Pass bool
	// Detail is a short human-readable diagnostic. Required when
	// Pass is false; optional but allowed when Pass is true (some
	// checks want to record "matched on line 7" even on success).
	Detail string
}

// Trace is the frozen record of one agent run. Built by the Runner
// from the Loop's event stream; consumed by Checks. Trace has no
// methods that mutate; once Run returns it, treat it as immutable.
type Trace struct {
	// SchemaVersion is the trace JSONL contract version from trace.meta.
	// Zero means the trace was produced before versioned headers existed.
	SchemaVersion int

	// Scenario is the Scenario.Name that produced this Trace.
	Scenario string

	// WorkspaceDir is the per-run workspace path. Checks may use it only
	// for bounded validation of files referenced by the trace, such as
	// tool result artifacts.
	WorkspaceDir string

	// Prompt is the user message that was sent.
	Prompt string

	// FinalText is the last assistant message.done text. Empty if
	// the run ended without one (max_turns, error).
	FinalText string

	// FinishReason is the upstream OpenAI-compat finish_reason from
	// the final assistant message ("stop", "length", "tool_calls").
	// Useful for detecting "the model thought it was done" vs
	// "the model was cut off at max_tokens".
	FinishReason string

	// TurnEndReason is the Loop's reason for ending the turn:
	// "completed", "cancelled", "error", "max_turns".
	TurnEndReason string

	// ToolStats is the Loop's per-turn tool correction summary when
	// emitted by turn.end.
	ToolStats ToolRuntimeStats

	// Tools is the synthesized tool-call timeline. Each ToolCall
	// combines a tool.request with its later tool.result, in the
	// order the LLM emitted them. Empty when the agent answered
	// without using a tool.
	Tools []ToolCall
	// ChildTranscripts references isolated focused-task/subagent
	// conversation logs produced under the workspace. Checks may inspect
	// these for hygiene regressions that parent trace isolation would
	// otherwise hide.
	ChildTranscripts []DebugTranscriptRef
	// ChildTranscriptRootDir is the filesystem root used to resolve
	// ChildTranscripts. Empty means WorkspaceDir. Server-managed sessions
	// keep child transcripts in durable session state while workspace
	// hygiene checks still need the active workspace path.
	ChildTranscriptRootDir string
	// UserMessages records user.message trace metadata, including product
	// modes such as plan_only and execute_plan. This lets evals verify the
	// requested control path instead of inferring it from prompt text.
	UserMessages []UserMessage

	// Usage is the aggregated token accounting for the run.
	Usage Usage

	// LoopErrors holds any error events the Loop emitted that did
	// NOT kill the run (transient retries that ultimately succeeded
	// etc.). A non-empty list with TurnEndReason="completed" is
	// fine; non-empty with TurnEndReason="error" is the kill signal.
	LoopErrors []string
	// LoopErrorKinds is the machine-readable primary failure class from
	// error events when the runtime can classify it, such as llm_timeout,
	// llm_incomplete_stream, or context_overflow.
	LoopErrorKinds []string
	// RuntimeErrors preserves classified error events with their message so
	// eval reports can show examples without asking operators to open the
	// full trace.
	RuntimeErrors []RuntimeErrorExample
	// ConversationRepairs records startup repairs applied to persisted
	// conversation logs before the next turn. These are resume/recovery
	// signals, not model actions.
	ConversationRepairs []sse.ConversationRepairedPayload
	// LoopDecisions records structured protocol/runtime decisions such as
	// evidence_quality defer events. These are separate from assistant text so
	// evals can measure when guardrails fired and whether they were actionable.
	LoopDecisions []LoopDecision
	// MessageRejections records assistant completion candidates that the
	// runtime rejected before committing message.done. These are useful for
	// proving a completion guard prevented a premature final answer from
	// becoming the authoritative final text.
	MessageRejections []MessageRejected
	// LoopProtocolFeeds records LOOP.md injections into model context. These
	// events let long-run evals measure protocol feed cadence and full/digest
	// context pressure without reading sidecar loop files.
	LoopProtocolFeeds []LoopProtocolFeed
	// LoopProtocolCalibrationRequests records assistant setup questions for a
	// draft LOOP.md. These events prove the model asked before activation,
	// not only that a later user answer was accepted.
	LoopProtocolCalibrationRequests []LoopProtocolCalibration
	// LoopProtocolCalibrations records accepted user calibration answers for
	// draft LOOP.md activation. These events prove setup progress even before
	// a later protocol feed exposes calibration counters.
	LoopProtocolCalibrations []LoopProtocolCalibration
	// LoopTurnCheckpoints records successful durable sidecar checkpoint writes
	// mirrored into the trace. These events prove that a long-run turn's
	// recovery summary was persisted before the trace claimed the turn ended.
	LoopTurnCheckpoints []LoopTurnCheckpoint
	// ContextInjections records hidden system-context blocks injected into the
	// model prompt. These summaries let evals measure prompt pressure from
	// account access hints, active plans, and auto-activated skills without
	// exposing the full prompt body.
	ContextInjections []ContextInjection
	// ContextCompactions records model-context rewrites produced by the
	// rolling compactor. The full user-visible trace remains in events.jsonl;
	// these entries let long-run evals assert that context pressure was handled.
	ContextCompactions []ContextCompaction
	// ContextCompactionSkips records compaction candidates that were rejected
	// before replacing conversation state. These are audit events, not task
	// progress, and help explain token-pressure behavior without prompt bloat.
	ContextCompactionSkips []ContextCompactionSkip
	// EventOrder preserves a compact chronological index for trace assertions
	// that depend on sequencing across event families, such as confirming a
	// full LOOP.md feed occurred after context compaction.
	EventOrder []TraceEventRef
	// RuntimeSurfaces records the effective tool/runtime surface at turn
	// start. This lets eval/debug tooling explain missing web/browser/memory
	// behavior without inferring availability from later tool calls.
	RuntimeSurfaces []sse.RuntimeSurfacePayload
	// TaskState is a read-only task snapshot derived from trace facts. It is
	// not fed back into the runtime; eval/debug tooling uses it to review
	// objective, progress, verification, changes, failures, and evidence.
	TaskState TaskStateSnapshot

	// RawTypes counts every event type the run produced, by name
	// (e.g. {"tool.request": 5, "message.delta": 1300}). Populated
	// by both the in-process Runner and the disk-replay
	// ParseTraceFile path so checks that just want "did at least
	// one usage event arrive" can read this without scanning Tools.
	RawTypes map[string]int
}

type UserMessage struct {
	TurnID       string
	Text         string
	DisplayText  string
	Mode         string
	Source       string
	ScheduleID   string
	ScheduleKind string
}

// ToolCall is one tool invocation by the agent, with its result.
// ToolCall is what Checks like ToolCalled / ToolCalledBefore inspect
// — so the field shape matters for the check library.
type ToolCall struct {
	// TurnID is the turn that emitted the tool.request or tool.result
	// event. Older traces may leave it empty.
	TurnID string
	// CallID is the upstream tool_call id. Lets a Check correlate a
	// specific request with its result when the agent issued
	// duplicates (e.g. retried after a transient error).
	CallID string
	// Tool is the tool name (e.g. "read_file", "shell", "edit_file").
	Tool string
	// Args is the JSON-decoded argument object captured from the
	// tool.request event. Small values are exact; large argument values
	// may be event-capped by the runtime.
	Args map[string]any
	// ArgsTruncated reports whether tool.request args hit a value or
	// event cap. ArgsBytes is the repaired argument JSON byte count;
	// ArgsOmittedBytes is the number of original argument bytes omitted
	// from Args; ArgsCapBytes is the event cap used by the runtime.
	ArgsTruncated    bool
	ArgsBytes        int
	ArgsOmittedBytes int
	ArgsCapBytes     int
	// OriginalTool is the model-emitted tool name before runtime
	// canonicalization, when different from Tool or when trace producers
	// include it for diagnostics.
	OriginalTool string
	// OriginalArgsSummary is a bounded preview of model-emitted arguments
	// before runtime JSON/schema repair.
	OriginalArgsSummary string
	// Canonicalized reports that the runtime changed the tool name before
	// dispatch, e.g. readFile -> read_file.
	Canonicalized bool
	// ArgsRepaired reports that the runtime repaired malformed JSON,
	// schema aliases, or scalar types before dispatch.
	ArgsRepaired bool
	// RepairNotes are short runtime diagnostics explaining
	// canonicalization or argument repair.
	RepairNotes []string
	// Skipped marks runtime protocol placeholders that were not admitted
	// to tool dispatch.
	Skipped         bool
	SkipFailureKind string
	// Result is the tool output carried by the tool.result event. It may
	// be clipped by the runtime's event cap; inspect ResultTruncated and
	// the byte counters before treating it as complete.
	Result string
	// ResultSummary is the UI-friendly bounded preview emitted alongside
	// Result. It is useful for diagnostics when Result is too large or
	// mostly structured boilerplate.
	ResultSummary string
	// ResultTruncated reports whether the tool.result event hit its
	// event transport cap. ResultBytes is the original output byte count;
	// ResultOmittedBytes is the byte count omitted from Result; and
	// ResultCapBytes is the event cap used by the runtime.
	ResultTruncated    bool
	ResultBytes        int
	ResultOmittedBytes int
	ResultCapBytes     int
	// ResultArtifactPath is a relative path to the complete tool result when
	// the event payload was truncated and the runtime persisted an artifact.
	// Eval traces resolve it under WorkspaceDir; affentserve exposes the same
	// relative path through the session artifact API.
	ResultArtifactPath string
	// ContextBytes/ContextOmittedBytes describe the tool-result text that was
	// actually appended to the model conversation after context caps. These are
	// distinct from Result* event transport truncation fields.
	ContextBytes           int
	ContextOmittedBytes    int
	ContextEstimatedTokens int
	// FailureKind is the machine-readable structured failure kind from
	// tool.result, when the tool surfaced one.
	FailureKind string
	// FailureKinds carries all structured failure kinds from tool.result.
	// FailureKind is retained as the first/primary value for older consumers.
	FailureKinds []string
	// ExitCode is the tool's reported exit code. -1 marks abnormal
	// exits (timeout, killed). Non-zero is a failure even if the
	// tool returned without a Go error.
	ExitCode int
	// DurationMS is the runtime-measured implementation time for a
	// dispatched tool. Zero means unavailable or shorter than 1ms.
	DurationMS int64
	// IsErr is true when the tool returned a Go error (vs returning
	// a non-zero exit code via a successful execution).
	IsErr bool
	// Delegation, when set, classifies the call as a bounded child-Loop
	// delegation (focused_task or subagent) and carries the small
	// metadata block trace consumers most often filter on. Nil for
	// non-delegation tools.
	Delegation *sse.DelegationMeta
	// MemoryUpdate, when set, is the runtime's structured summary of a
	// confirmed memory add/replace/remove operation. Prefer this over
	// reparsing capped request args or memory response JSON.
	MemoryUpdate *sse.MemoryUpdateMeta
}

type ToolRuntimeStats struct {
	ToolRequests               int
	ToolRequestsAdmitted       int
	ToolRequestsSkipped        int
	ToolNameCanonicalized      int
	ToolArgsRepaired           int
	ToolRepairCalls            int
	ToolRepairSucceeded        int
	ToolRepairFailed           int
	ToolRepairNotes            int
	ToolRepairByKind           map[string]int
	ToolFailureByKind          map[string]int
	ToolErrors                 int
	ToolUnclassifiedErrors     int
	ToolDurationMS             int64
	LoopGuardInterventions     int
	ForcedNoTools              int
	SourceAccessResults        int
	SourceAccessVerified       int
	SourceAccessDiscoveryOnly  int
	SourceAccessNetwork        int
	SourceAccessDynamicPartial int
	MemoryUpdates              int
	MemoryUpdateAdd            int
	MemoryUpdateReplace        int
	MemoryUpdateRemove         int
	MemorySearchCalls          int
	MemorySearchMisses         int
	SessionSearchCalls         int
	SessionSearchResults       int
	SessionSearchContextHits   int
	SessionSearchMatchedTerms  int
	SessionSearchRecent        int
	ToolContextTruncated       int
	ToolContextOmittedBytes    int
}

type ToolTruncationStats struct {
	ArgsTruncated           int
	ArgsOmittedBytes        int
	ResultsTruncated        int
	ResultsOmittedBytes     int
	ResultArtifacts         int
	ResultMissingArtifacts  int
	ContextTruncated        int
	ContextOmittedBytes     int
	ContextArtifacts        int
	ContextMissingArtifacts int
}

type ToolTruncationExample struct {
	Scenario               string `json:"scenario,omitempty"`
	ToolIndex              int    `json:"tool_index"`
	CallID                 string `json:"call_id,omitempty"`
	Tool                   string `json:"tool"`
	ArgsTruncated          bool   `json:"args_truncated,omitempty"`
	ArgsBytes              int    `json:"args_bytes,omitempty"`
	ArgsOmittedBytes       int    `json:"args_omitted_bytes,omitempty"`
	ArgsCapBytes           int    `json:"args_cap_bytes,omitempty"`
	ResultTruncated        bool   `json:"result_truncated,omitempty"`
	ResultSummary          string `json:"result_summary,omitempty"`
	ResultBytes            int    `json:"result_bytes,omitempty"`
	ResultOmittedBytes     int    `json:"result_omitted_bytes,omitempty"`
	ResultCapBytes         int    `json:"result_cap_bytes,omitempty"`
	ResultArtifactPath     string `json:"result_artifact_path,omitempty"`
	ContextBytes           int    `json:"context_bytes,omitempty"`
	ContextOmittedBytes    int    `json:"context_omitted_bytes,omitempty"`
	ContextEstimatedTokens int    `json:"context_estimated_tokens,omitempty"`
}

// ToolRepairStats classifies runtime tool-call recovery work. New traces carry
// authoritative turn-level repair stats on turn.end; older traces fall back to
// the human-readable repair notes already emitted on tool.request events.
// A single tool call can contribute to multiple kinds (for example,
// wrapper_unwrap + type_coercion), so Notes can be greater than Calls.
type ToolRepairStats struct {
	Calls          int
	SucceededCalls int
	FailedCalls    int
	Notes          int
	ByKind         map[string]int
}

type ToolRepairExample struct {
	Scenario            string   `json:"scenario,omitempty"`
	ToolIndex           int      `json:"tool_index"`
	CallID              string   `json:"call_id,omitempty"`
	Tool                string   `json:"tool"`
	OriginalTool        string   `json:"original_tool,omitempty"`
	Canonicalized       bool     `json:"canonicalized,omitempty"`
	ArgsRepaired        bool     `json:"args_repaired,omitempty"`
	OriginalArgsSummary string   `json:"original_args_summary,omitempty"`
	RepairNotes         []string `json:"repair_notes,omitempty"`
	RepairKinds         []string `json:"repair_kinds,omitempty"`
	ExitCode            int      `json:"exit_code"`
	Succeeded           bool     `json:"succeeded"`
}

type ToolFailureExample struct {
	Scenario          string `json:"scenario,omitempty"`
	Kind              string `json:"kind"`
	ToolIndex         int    `json:"tool_index"`
	CallID            string `json:"call_id,omitempty"`
	Tool              string `json:"tool"`
	ArgsSummary       string `json:"args_summary,omitempty"`
	ResultSummary     string `json:"result_summary,omitempty"`
	SuggestedNextStep string `json:"suggested_next_step,omitempty"`
	ExitCode          int    `json:"exit_code"`
}

type LoopGuardExample struct {
	Scenario          string `json:"scenario,omitempty"`
	Kind              string `json:"kind"`
	Category          string `json:"category"`
	ToolIndex         int    `json:"tool_index"`
	CallID            string `json:"call_id,omitempty"`
	Tool              string `json:"tool"`
	ArgsSummary       string `json:"args_summary,omitempty"`
	GuardSummary      string `json:"guard_summary,omitempty"`
	SuggestedNextStep string `json:"suggested_next_step,omitempty"`
	ResultSummary     string `json:"result_summary,omitempty"`
	ExitCode          int    `json:"exit_code"`
}

type MemoryUpdateExample struct {
	Scenario        string `json:"scenario,omitempty"`
	ToolIndex       int    `json:"tool_index"`
	CallID          string `json:"call_id,omitempty"`
	Action          string `json:"action"`
	Target          string `json:"target"`
	Topic           string `json:"topic,omitempty"`
	Location        string `json:"location"`
	Preview         string `json:"preview,omitempty"`
	PreviousPreview string `json:"previous_preview,omitempty"`
	NextPreview     string `json:"next_preview,omitempty"`
}

type MemorySearchMissExample struct {
	Scenario   string   `json:"scenario,omitempty"`
	ToolIndex  int      `json:"tool_index"`
	CallID     string   `json:"call_id,omitempty"`
	Target     string   `json:"target,omitempty"`
	Topic      string   `json:"topic,omitempty"`
	Query      string   `json:"query,omitempty"`
	Message    string   `json:"message,omitempty"`
	TopicCount int      `json:"topic_count,omitempty"`
	Topics     []string `json:"topics,omitempty"`
}

func cloneMemorySearchMissExamples(in []MemorySearchMissExample) []MemorySearchMissExample {
	if len(in) == 0 {
		return nil
	}
	out := make([]MemorySearchMissExample, 0, len(in))
	for _, ex := range in {
		if len(ex.Topics) > 0 {
			ex.Topics = append([]string(nil), ex.Topics...)
		}
		out = append(out, ex)
	}
	return out
}

type SourceAccessExample struct {
	Scenario      string `json:"scenario,omitempty"`
	ToolIndex     int    `json:"tool_index"`
	CallID        string `json:"call_id,omitempty"`
	Tool          string `json:"tool"`
	Status        string `json:"status"`
	URL           string `json:"url,omitempty"`
	RequestedURL  string `json:"requested_url,omitempty"`
	URLField      string `json:"url_field,omitempty"`
	SourceMethod  string `json:"source_method,omitempty"`
	JSONPath      string `json:"json_path,omitempty"`
	Ref           string `json:"ref,omitempty"`
	HTTPStatus    string `json:"http_status,omitempty"`
	ContentType   string `json:"content_type,omitempty"`
	BodyBytes     int    `json:"body_bytes,omitempty"`
	BodyOffset    int    `json:"body_offset,omitempty"`
	ShowingBytes  int    `json:"showing_bytes,omitempty"`
	OmittedBefore int    `json:"omitted_before,omitempty"`
	OmittedAfter  int    `json:"omitted_after,omitempty"`
	NextOffset    int    `json:"next_offset,omitempty"`
	HasMore       bool   `json:"has_more,omitempty"`
	ResultPreview string `json:"result_preview,omitempty"`
}

type BrowserScrollExample struct {
	Scenario          string `json:"scenario,omitempty"`
	ToolIndex         int    `json:"tool_index"`
	CallID            string `json:"call_id,omitempty"`
	URL               string `json:"url,omitempty"`
	Direction         string `json:"direction,omitempty"`
	BeforeY           string `json:"before_y,omitempty"`
	AfterY            string `json:"after_y,omitempty"`
	MaxY              string `json:"max_y,omitempty"`
	Movement          string `json:"movement,omitempty"`
	Boundary          string `json:"boundary,omitempty"`
	Status            string `json:"status,omitempty"`
	SuggestedNextStep string `json:"suggested_next_step,omitempty"`
	ResultPreview     string `json:"result_preview,omitempty"`
}

type BrowserNetworkSearchExample struct {
	Scenario          string   `json:"scenario,omitempty"`
	ToolIndex         int      `json:"tool_index"`
	CallID            string   `json:"call_id,omitempty"`
	CurrentPageURL    string   `json:"current_page_url,omitempty"`
	Query             string   `json:"query,omitempty"`
	Status            string   `json:"status"`
	EvidenceStatus    string   `json:"evidence_status,omitempty"`
	Refs              []string `json:"refs,omitempty"`
	Previews          []string `json:"previews,omitempty"`
	RequiresRead      bool     `json:"requires_read,omitempty"`
	NotCitable        bool     `json:"not_citable,omitempty"`
	SuggestedNextStep string   `json:"suggested_next_step,omitempty"`
}

type SessionSearchExample struct {
	Scenario               string   `json:"scenario,omitempty"`
	ToolIndex              int      `json:"tool_index"`
	CallID                 string   `json:"call_id,omitempty"`
	Query                  string   `json:"query,omitempty"`
	Total                  int      `json:"total,omitempty"`
	RecentSessions         int      `json:"recent_sessions,omitempty"`
	SessionID              string   `json:"session_id,omitempty"`
	RecentSessionID        string   `json:"recent_session_id,omitempty"`
	TurnIdx                int      `json:"turn_idx,omitempty"`
	MessageIdx             int      `json:"message_idx,omitempty"`
	Role                   string   `json:"role,omitempty"`
	Score                  float64  `json:"score,omitempty"`
	ModTime                string   `json:"mod_time,omitempty"`
	RecentModTime          string   `json:"recent_mod_time,omitempty"`
	MatchedTerms           []string `json:"matched_terms,omitempty"`
	ContextIncluded        bool     `json:"context_included,omitempty"`
	SnippetPreview         string   `json:"snippet_preview,omitempty"`
	RecentUserPreview      string   `json:"recent_user_preview,omitempty"`
	RecentAssistantPreview string   `json:"recent_assistant_preview,omitempty"`
	RecentPlanPreview      string   `json:"recent_plan_preview,omitempty"`
	RecentLoopPreview      string   `json:"recent_loop_preview,omitempty"`
	RecentTaskStatePreview string   `json:"recent_task_state_preview,omitempty"`
	RecentRecoveryPreview  string   `json:"recent_recovery_preview,omitempty"`
	Message                string   `json:"message,omitempty"`
}

type RuntimeErrorExample struct {
	Scenario string `json:"scenario,omitempty"`
	Kind     string `json:"kind"`
	Message  string `json:"message"`
}

type LoopDecision struct {
	Scenario       string `json:"scenario,omitempty"`
	Kind           string `json:"kind"`
	Decision       string `json:"decision"`
	Trigger        string `json:"trigger,omitempty"`
	Confidence     string `json:"confidence,omitempty"`
	Reason         string `json:"reason,omitempty"`
	RequiredAction string `json:"required_action,omitempty"`
	TokenBudget    int    `json:"token_budget,omitempty"`
	// Input-token budget decisions preserve these raw fields so eval output
	// can diagnose budget pressure without parsing human-readable reason text.
	ObservedInputTokens  int    `json:"observed_input_tokens,omitempty"`
	ProjectedInputTokens int    `json:"projected_input_tokens,omitempty"`
	BudgetBytes          int    `json:"budget_bytes,omitempty"`
	TurnID               string `json:"turn_id,omitempty"`
	DecisionID           string `json:"decision_id,omitempty"`
}

type LoopDecisionStats struct {
	Count      int
	ByKind     map[string]int
	ByDecision map[string]int
	ByMatch    map[string]int
	Examples   []LoopDecision
}

type MessageRejected struct {
	Scenario       string `json:"scenario,omitempty"`
	TurnID         string `json:"turn_id,omitempty"`
	Text           string `json:"text,omitempty"`
	Reason         string `json:"reason,omitempty"`
	Trigger        string `json:"trigger,omitempty"`
	RequiredAction string `json:"required_action,omitempty"`
}

type MessageRejectedStats struct {
	Count     int
	ByTrigger map[string]int
	Examples  []MessageRejected
}

type LoopProtocolFeed struct {
	Scenario                     string `json:"scenario,omitempty"`
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

type LoopProtocolCalibration struct {
	Scenario                string `json:"scenario,omitempty"`
	LoopID                  string `json:"loop_id,omitempty"`
	Status                  string `json:"status,omitempty"`
	CalibrationQuestions    int    `json:"calibration_questions,omitempty"`
	LastCalibrationQuestion string `json:"last_calibration_question_preview,omitempty"`
	CalibrationAnswers      int    `json:"calibration_answers,omitempty"`
	LastCalibrationAnswer   string `json:"last_calibration_answer_preview,omitempty"`
	ProtocolPath            string `json:"protocol_path,omitempty"`
	EventSeq                int    `json:"event_seq,omitempty"`
}

type LoopTurnCheckpoint struct {
	Scenario                 string `json:"scenario,omitempty"`
	TurnID                   string `json:"turn_id,omitempty"`
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

type TraceEventRef struct {
	Index            int    `json:"index"`
	Type             string `json:"type"`
	TurnID           string `json:"turn_id,omitempty"`
	LoopProtocolMode string `json:"loop_protocol_mode,omitempty"`
	LoopProtocolPath string `json:"loop_protocol_path,omitempty"`
	ContextSource    string `json:"context_source,omitempty"`
	ContextReason    string `json:"context_reason,omitempty"`
	ContextReactive  bool   `json:"context_reactive,omitempty"`
}

type LoopProtocolFeedStats struct {
	Count    int
	ByMode   map[string]int
	Latest   LoopProtocolFeed
	Examples []LoopProtocolFeed
}

type LoopProtocolCalibrationStats struct {
	Count    int
	Latest   LoopProtocolCalibration
	Examples []LoopProtocolCalibration
}

type LoopProtocolSetupOverrunStats struct {
	Initializations     int
	PostSetupToolCalls  int
	NonSkippedToolCalls int
	SkippedToolCalls    int
	Examples            []string
}

type LoopTurnCheckpointStats struct {
	Count                   int
	MaxToolRequests         int
	MaxToolRequestsAdmitted int
	MaxToolRequestsSkipped  int
	MaxInputTokens          int
	MaxTotalTokens          int
	Latest                  LoopTurnCheckpoint
	Examples                []LoopTurnCheckpoint
}

type ContextInjection struct {
	Scenario        string `json:"scenario,omitempty"`
	TurnID          string `json:"turn_id,omitempty"`
	Source          string `json:"source"`
	Name            string `json:"name,omitempty"`
	Title           string `json:"title"`
	Summary         string `json:"summary,omitempty"`
	Preview         string `json:"preview,omitempty"`
	ContentSHA256   string `json:"content_sha256,omitempty"`
	Bytes           int    `json:"bytes,omitempty"`
	EstimatedTokens int    `json:"estimated_tokens,omitempty"`
}

type ContextInjectionStats struct {
	Count           int
	BySource        map[string]int
	Bytes           int
	EstimatedTokens int
	Latest          ContextInjection
	Examples        []ContextInjection
}

type ContextCompaction struct {
	Scenario                           string `json:"scenario,omitempty"`
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
	CompactScopedInputTokens           int    `json:"compact_scoped_input_tokens,omitempty"`
	CompactHardInputLimitTokens        int    `json:"compact_hard_input_limit_tokens,omitempty"`
	Reactive                           bool   `json:"reactive"`
	Reason                             string `json:"reason"`
	SummaryPresent                     bool   `json:"summary_present,omitempty"`
	SummaryPresentKnown                bool   `json:"summary_present_known,omitempty"`
	SummaryBytes                       int    `json:"summary_bytes,omitempty"`
	SummaryPreview                     string `json:"summary_preview,omitempty"`
	LoopProtocolAnchor                 string `json:"loop_protocol_anchor,omitempty"`
}

type ContextCompactionStats struct {
	Count                        int
	Reactive                     int
	Proactive                    int
	RemovedMessages              int
	ReducedBytes                 int
	SummaryBytes                 int
	SummaryMissing               int
	SummaryEmpty                 int
	PolicyObserved               int
	MaxPolicyPressurePercent     int
	PostPolicyObserved           int
	PostPolicyStillOverTrigger   int
	MaxPostPolicyPressurePercent int
	ByReason                     map[string]int
	Examples                     []ContextCompaction
}

type ContextCompactionSkip struct {
	Scenario                           string `json:"scenario,omitempty"`
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
	CompactScopedInputTokens           int    `json:"compact_scoped_input_tokens,omitempty"`
	CompactHardInputLimitTokens        int    `json:"compact_hard_input_limit_tokens,omitempty"`
}

type ContextCompactionSkipStats struct {
	Count                        int
	PolicyObserved               int
	PostPolicyObserved           int
	PostPolicyStillOverTrigger   int
	MaxPolicyPressurePercent     int
	MaxPostPolicyPressurePercent int
	ByCause                      map[string]int
	ByReason                     map[string]int
	Examples                     []ContextCompactionSkip
}

func (s ToolRepairStats) HasAny() bool {
	return s.Calls > 0 ||
		s.SucceededCalls > 0 ||
		s.FailedCalls > 0 ||
		s.Notes > 0 ||
		len(s.ByKind) > 0
}

func (t Trace) RepairStats() ToolRepairStats {
	stats := t.repairStatsFromRequests()
	if !t.ToolStats.hasRepairStats() {
		return stats
	}
	if t.ToolStats.ToolRepairCalls > 0 ||
		t.ToolStats.ToolRepairSucceeded > 0 ||
		t.ToolStats.ToolRepairFailed > 0 {
		stats.Calls = t.ToolStats.ToolRepairCalls
		stats.SucceededCalls = t.ToolStats.ToolRepairSucceeded
		stats.FailedCalls = t.ToolStats.ToolRepairFailed
	}
	if t.ToolStats.ToolRepairNotes > 0 || len(t.ToolStats.ToolRepairByKind) > 0 {
		stats.Notes = t.ToolStats.ToolRepairNotes
		stats.ByKind = cloneStringIntMap(t.ToolStats.ToolRepairByKind)
		if stats.Notes == 0 {
			for _, count := range stats.ByKind {
				stats.Notes += count
			}
		}
	}
	return stats
}

func (t Trace) LoopProtocolSetupOverrunStats(maxExamples int) LoopProtocolSetupOverrunStats {
	var stats LoopProtocolSetupOverrunStats
	for i, call := range t.Tools {
		if !loopProtocolFreshStartSetup(call) {
			continue
		}
		stats.Initializations++
		for j := i + 1; j < len(t.Tools); j++ {
			next := t.Tools[j]
			if next.TurnID != call.TurnID {
				continue
			}
			stats.PostSetupToolCalls++
			if loopProtocolSetupSkippedTool(next) {
				stats.SkippedToolCalls++
			} else {
				stats.NonSkippedToolCalls++
				if maxExamples > 0 && len(stats.Examples) < maxExamples {
					stats.Examples = append(stats.Examples, fmt.Sprintf("after %s start_setup, %s call_id=%s ran in same turn", call.CallID, next.Tool, next.CallID))
				}
			}
		}
	}
	return stats
}

func loopProtocolFreshStartSetup(call ToolCall) bool {
	if call.Tool != agent.LoopProtocolToolName || call.ExitCode != 0 {
		return false
	}
	action := strings.ToLower(strings.TrimSpace(fmt.Sprint(call.Args["action"])))
	if action != "start_setup" {
		return false
	}
	return strings.Contains(call.Result, "initialized LOOP.md draft status=draft")
}

func loopProtocolSetupSkippedTool(call ToolCall) bool {
	if call.Skipped {
		return true
	}
	return call.ExitCode != 0 && strings.Contains(call.Result, "calibration question required before more tools")
}

func (t Trace) ToolRepairExamples(maxExamples int) []ToolRepairExample {
	if maxExamples <= 0 {
		return nil
	}
	var out []ToolRepairExample
	for i, c := range t.Tools {
		if len(out) >= maxExamples {
			break
		}
		if !c.Canonicalized && !c.ArgsRepaired && len(c.RepairNotes) == 0 {
			continue
		}
		out = append(out, ToolRepairExample{
			ToolIndex:           i + 1,
			CallID:              c.CallID,
			Tool:                c.Tool,
			OriginalTool:        c.OriginalTool,
			Canonicalized:       c.Canonicalized,
			ArgsRepaired:        c.ArgsRepaired,
			OriginalArgsSummary: compactOneLine(c.OriginalArgsSummary, 220),
			RepairNotes:         compactStringSlice(c.RepairNotes, 8, 220),
			RepairKinds:         repairKindsForCall(c),
			ExitCode:            c.ExitCode,
			Succeeded:           c.ExitCode == 0,
		})
	}
	return out
}

func (t Trace) ToolFailureKindCounts() map[string]int {
	out := cloneStringIntMap(t.ToolStats.ToolFailureByKind)
	var derived map[string]int
	for _, c := range t.Tools {
		kinds := toolFailureKindsForCall(c)
		if len(kinds) == 0 {
			continue
		}
		if derived == nil {
			derived = map[string]int{}
		}
		for _, kind := range kinds {
			derived[kind]++
		}
	}
	for kind, count := range derived {
		if out == nil {
			out = map[string]int{}
		}
		if count > out[kind] {
			out[kind] = count
		}
	}
	return out
}

func (t Trace) UnclassifiedToolErrorCount() int {
	var count int
	for _, c := range t.Tools {
		if c.ExitCode == 0 || len(toolFailureKindsForCall(c)) > 0 {
			continue
		}
		count++
	}
	return count
}

func (t Trace) ToolFailureExamples(maxPerKind int) map[string][]ToolFailureExample {
	if maxPerKind <= 0 {
		return nil
	}
	out := map[string][]ToolFailureExample{}
	for i, c := range t.Tools {
		for _, kind := range toolFailureKindsForCall(c) {
			if len(out[kind]) >= maxPerKind {
				continue
			}
			out[kind] = append(out[kind], ToolFailureExample{
				Kind:              kind,
				ToolIndex:         i + 1,
				CallID:            c.CallID,
				Tool:              c.Tool,
				ArgsSummary:       summarizeToolCallArgs(c.Args),
				ResultSummary:     summarizeToolFailureResult(c.Result),
				SuggestedNextStep: summarizeToolNextStep(c.Result),
				ExitCode:          c.ExitCode,
			})
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (t Trace) LoopGuardExamples(maxExamples int) []LoopGuardExample {
	if maxExamples <= 0 {
		return nil
	}
	var out []LoopGuardExample
	for i, c := range t.Tools {
		if len(out) >= maxExamples {
			break
		}
		for _, kind := range toolFailureKindsForCall(c) {
			category, ok := loopGuardExampleCategory(kind)
			if !ok {
				continue
			}
			out = append(out, LoopGuardExample{
				Kind:              kind,
				Category:          category,
				ToolIndex:         i + 1,
				CallID:            c.CallID,
				Tool:              c.Tool,
				ArgsSummary:       summarizeToolCallArgs(c.Args),
				GuardSummary:      summarizeLoopGuardMessage(c.Result),
				SuggestedNextStep: summarizeLoopGuardNextStep(c.Result),
				ResultSummary:     summarizeToolFailureResult(c.Result),
				ExitCode:          c.ExitCode,
			})
			if len(out) >= maxExamples {
				break
			}
		}
	}
	return out
}

func (t Trace) MemoryUpdateExamples(maxExamples int) []MemoryUpdateExample {
	if maxExamples <= 0 {
		return nil
	}
	var out []MemoryUpdateExample
	for i, c := range t.Tools {
		if len(out) >= maxExamples {
			break
		}
		ex, ok := memoryUpdateExampleForTool(i+1, c)
		if ok {
			out = append(out, ex)
		}
	}
	return out
}

func (t Trace) MemorySearchMissExamples(maxExamples int) []MemorySearchMissExample {
	if maxExamples <= 0 {
		return nil
	}
	var out []MemorySearchMissExample
	for i, c := range t.Tools {
		if len(out) >= maxExamples {
			break
		}
		ex, ok := memorySearchMissExampleForTool(i+1, c)
		if ok {
			out = append(out, ex)
		}
	}
	return out
}

func (t Trace) SourceAccessExamples(maxExamples int) []SourceAccessExample {
	if maxExamples <= 0 {
		return nil
	}
	var out []SourceAccessExample
	for i, c := range t.Tools {
		if len(out) >= maxExamples {
			break
		}
		info, ok := sourceaccess.FirstInfoFromResult(c.Result)
		if !ok {
			continue
		}
		status := "verified"
		switch {
		case info.IsNetworkSource():
			status = "network"
		case info.IsDynamicPartial() || sourceaccess.HasDynamicPartialEvidence(c.Result):
			status = "dynamic_partial"
		case info.IsDiscoveryOnly():
			status = "discovery_only"
		}
		bodyPage := sourceAccessBodyPageInfo(c.Result)
		out = append(out, SourceAccessExample{
			ToolIndex:     i + 1,
			CallID:        c.CallID,
			Tool:          c.Tool,
			Status:        status,
			URL:           info.AccessedURL,
			RequestedURL:  info.RequestedURL,
			URLField:      info.URLField,
			SourceMethod:  info.SourceMethod,
			JSONPath:      info.JSONPath,
			Ref:           info.Ref,
			HTTPStatus:    info.HTTPStatus,
			ContentType:   info.ContentType,
			BodyBytes:     bodyPage.BodyBytes,
			BodyOffset:    bodyPage.BodyOffset,
			ShowingBytes:  bodyPage.ShowingBytes,
			OmittedBefore: bodyPage.OmittedBefore,
			OmittedAfter:  bodyPage.OmittedAfter,
			NextOffset:    bodyPage.NextOffset,
			HasMore:       bodyPage.HasMore,
			ResultPreview: sourceAccessResultPreview(c.Result, c.ResultSummary),
		})
	}
	return out
}

type sourceAccessBodyPage struct {
	BodyBytes     int
	BodyOffset    int
	ShowingBytes  int
	OmittedBefore int
	OmittedAfter  int
	NextOffset    int
	HasMore       bool
}

func sourceAccessBodyPageInfo(result string) sourceAccessBodyPage {
	for _, raw := range strings.Split(result, "\n") {
		line := strings.TrimSpace(raw)
		if !strings.HasPrefix(line, "BODY_BYTES:") {
			continue
		}
		return parseSourceAccessBodyBytesLine(line)
	}
	return sourceAccessBodyPage{}
}

func parseSourceAccessBodyBytesLine(line string) sourceAccessBodyPage {
	rest := strings.TrimSpace(strings.TrimPrefix(line, "BODY_BYTES:"))
	if rest == "" {
		return sourceAccessBodyPage{}
	}
	head := rest
	detail := ""
	if before, after, ok := strings.Cut(rest, "("); ok {
		head = strings.TrimSpace(before)
		if inside, _, ok := strings.Cut(after, ")"); ok {
			detail = inside
		} else {
			detail = after
		}
	}
	fields := strings.Fields(head)
	if len(fields) == 0 {
		return sourceAccessBodyPage{}
	}
	bodyBytes, err := strconv.Atoi(fields[0])
	if err != nil {
		return sourceAccessBodyPage{}
	}
	page := sourceAccessBodyPage{BodyBytes: bodyBytes}
	for _, part := range strings.Split(detail, ",") {
		kv := strings.Fields(strings.TrimSpace(part))
		if len(kv) < 2 {
			continue
		}
		value, err := strconv.Atoi(kv[1])
		if err != nil {
			continue
		}
		switch kv[0] {
		case "offset":
			page.BodyOffset = value
		case "showing":
			page.ShowingBytes = value
		case "omitted_before":
			page.OmittedBefore = value
		case "omitted_after":
			page.OmittedAfter = value
		case "next_offset":
			page.NextOffset = value
		}
	}
	page.HasMore = page.OmittedAfter > 0 || page.NextOffset > 0 && page.NextOffset < page.BodyBytes
	return page
}

func sourceAccessResultPreview(result, summary string) string {
	body := strings.TrimSpace(result)
	if idx := strings.IndexByte(body, '\n'); idx >= 0 {
		body = strings.TrimSpace(body[idx+1:])
	} else {
		body = ""
	}
	if body == "" {
		body = strings.TrimSpace(summary)
	}
	return compactOneLine(body, 260)
}

func (t Trace) BrowserNetworkSearchExamples(maxExamples int) []BrowserNetworkSearchExample {
	if maxExamples <= 0 {
		return nil
	}
	var out []BrowserNetworkSearchExample
	for i, c := range t.Tools {
		if len(out) >= maxExamples {
			break
		}
		ex, ok := browserNetworkSearchExampleForTool(i+1, c)
		if ok {
			out = append(out, ex)
		}
	}
	return out
}

func (t Trace) BrowserScrollExamples(maxExamples int) []BrowserScrollExample {
	if maxExamples <= 0 {
		return nil
	}
	var out []BrowserScrollExample
	for i, c := range t.Tools {
		if len(out) >= maxExamples {
			break
		}
		ex, ok := browserScrollExampleForTool(i+1, c)
		if ok {
			out = append(out, ex)
		}
	}
	return out
}

func (t Trace) SessionSearchExamples(maxExamples int) []SessionSearchExample {
	if maxExamples <= 0 {
		return nil
	}
	var out []SessionSearchExample
	for i, c := range t.Tools {
		if len(out) >= maxExamples {
			break
		}
		if c.Tool != agent.SessionSearchToolName || c.ExitCode != 0 || c.IsErr || c.ResultTruncated {
			continue
		}
		var resp agent.SessionSearchResponse
		if err := json.Unmarshal([]byte(c.Result), &resp); err != nil {
			continue
		}
		if len(resp.Results) == 0 {
			if len(resp.RecentSessions) == 0 {
				out = append(out, SessionSearchExample{
					ToolIndex:      i + 1,
					CallID:         c.CallID,
					Query:          compactOneLine(resp.Query, 220),
					Total:          resp.Total,
					RecentSessions: len(resp.RecentSessions),
					Message:        compactOneLine(resp.Message, 220),
				})
				continue
			}
			for _, recent := range resp.RecentSessions {
				if len(out) >= maxExamples {
					break
				}
				out = append(out, SessionSearchExample{
					ToolIndex:              i + 1,
					CallID:                 c.CallID,
					Query:                  compactOneLine(resp.Query, 220),
					Total:                  resp.Total,
					RecentSessions:         len(resp.RecentSessions),
					RecentSessionID:        compactOneLine(recent.SessionID, 120),
					RecentModTime:          compactOneLine(recent.ModTime, 80),
					RecentUserPreview:      compactOneLine(recent.LatestUser, 180),
					RecentAssistantPreview: compactOneLine(recent.LatestAssistant, 180),
					RecentPlanPreview:      compactOneLine(recent.Plan, 180),
					RecentLoopPreview:      compactOneLine(recent.Loop, 180),
					RecentTaskStatePreview: compactOneLine(recent.TaskState, 220),
					RecentRecoveryPreview:  compactOneLine(recent.Recovery, 180),
					Message:                compactOneLine(resp.Message, 220),
				})
			}
			continue
		}
		for _, hit := range resp.Results {
			if len(out) >= maxExamples {
				break
			}
			out = append(out, SessionSearchExample{
				ToolIndex:       i + 1,
				CallID:          c.CallID,
				Query:           compactOneLine(resp.Query, 220),
				Total:           resp.Total,
				SessionID:       hit.SessionID,
				TurnIdx:         hit.TurnIdx,
				MessageIdx:      hit.MessageIdx,
				Role:            hit.Role,
				Score:           hit.Score,
				ModTime:         compactOneLine(hit.ModTime, 80),
				MatchedTerms:    append([]string(nil), hit.MatchedTerms...),
				ContextIncluded: hit.ContextIncluded,
				SnippetPreview:  compactOneLine(hit.Snippet, 220),
			})
		}
	}
	return out
}

func (t Trace) ToolTruncationExamples(maxExamples int) []ToolTruncationExample {
	if maxExamples <= 0 {
		return nil
	}
	var out []ToolTruncationExample
	selected := make([]bool, len(t.Tools))
	for i, c := range t.Tools {
		if len(out) >= maxExamples {
			return out
		}
		if !toolTruncationMissingArtifact(c) {
			continue
		}
		out = append(out, toolTruncationExample(i, c))
		selected[i] = true
	}
	for i, c := range t.Tools {
		if len(out) >= maxExamples {
			break
		}
		if selected[i] || !toolTruncationExampleWorthIncluding(c) {
			continue
		}
		out = append(out, toolTruncationExample(i, c))
	}
	return out
}

func toolTruncationMissingArtifact(c ToolCall) bool {
	return c.ResultArtifactPath == "" && (c.ResultTruncated || c.ContextOmittedBytes > 0)
}

func toolTruncationExampleWorthIncluding(c ToolCall) bool {
	return c.ArgsTruncated || c.ResultTruncated || c.ResultArtifactPath != "" || c.ContextOmittedBytes > 0
}

func toolTruncationExample(index int, c ToolCall) ToolTruncationExample {
	return ToolTruncationExample{
		ToolIndex:              index + 1,
		CallID:                 c.CallID,
		Tool:                   c.Tool,
		ArgsTruncated:          c.ArgsTruncated,
		ArgsBytes:              c.ArgsBytes,
		ArgsOmittedBytes:       c.ArgsOmittedBytes,
		ArgsCapBytes:           c.ArgsCapBytes,
		ResultTruncated:        c.ResultTruncated,
		ResultSummary:          compactOneLine(c.ResultSummary, 260),
		ResultBytes:            c.ResultBytes,
		ResultOmittedBytes:     c.ResultOmittedBytes,
		ResultCapBytes:         c.ResultCapBytes,
		ResultArtifactPath:     c.ResultArtifactPath,
		ContextBytes:           c.ContextBytes,
		ContextOmittedBytes:    c.ContextOmittedBytes,
		ContextEstimatedTokens: c.ContextEstimatedTokens,
	}
}

func browserScrollExampleForTool(index int, c ToolCall) (BrowserScrollExample, bool) {
	if c.Tool != "browser_scroll" || c.ExitCode != 0 || c.IsErr {
		return BrowserScrollExample{}, false
	}
	body := strings.TrimSpace(c.Result)
	if !strings.Contains(body, "SCROLL:") {
		return BrowserScrollExample{}, false
	}
	var ex BrowserScrollExample
	ex.ToolIndex = index
	ex.CallID = c.CallID
	ex.Status = "unknown"
	if info, ok := sourceaccess.FirstInfoFromResult(c.Result); ok {
		ex.URL = compactOneLine(info.AccessedURL, 500)
	}
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "SCROLL:"):
			fields := scrollTelemetryFields(strings.TrimSpace(strings.TrimPrefix(trimmed, "SCROLL:")))
			ex.Direction = compactOneLine(fields["direction"], 80)
			ex.BeforeY = compactOneLine(fields["before_y"], 80)
			ex.AfterY = compactOneLine(fields["after_y"], 80)
			ex.MaxY = compactOneLine(fields["max_y"], 80)
			ex.Movement = compactOneLine(fields["movement"], 80)
			ex.Boundary = compactOneLine(fields["boundary"], 80)
		case strings.HasPrefix(trimmed, "Next:"):
			ex.SuggestedNextStep = compactOneLine(strings.TrimSpace(strings.TrimPrefix(trimmed, "Next:")), 320)
		}
	}
	ex.Status = browserScrollStatus(ex)
	ex.ResultPreview = sourceAccessResultPreview(c.Result, c.ResultSummary)
	return ex, true
}

func scrollTelemetryFields(s string) map[string]string {
	out := map[string]string{}
	for _, field := range strings.Fields(s) {
		key, value, ok := strings.Cut(field, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), `"`)
		if key != "" && value != "" {
			out[key] = value
		}
	}
	return out
}

func browserScrollStatus(ex BrowserScrollExample) string {
	movement := strings.ToLower(strings.TrimSpace(ex.Movement))
	boundary := strings.ToLower(strings.TrimSpace(ex.Boundary))
	switch {
	case boundary != "":
		return "boundary"
	case movement == "none" || movement == "0" || strings.Contains(movement, "no"):
		return "stuck"
	case movement != "":
		return "moved"
	default:
		return "unknown"
	}
}

func browserNetworkSearchExampleForTool(index int, c ToolCall) (BrowserNetworkSearchExample, bool) {
	if c.Tool != "browser_network" || c.ExitCode != 0 || c.IsErr {
		return BrowserNetworkSearchExample{}, false
	}
	body := strings.TrimSpace(c.Result)
	if !strings.HasPrefix(body, "BROWSER NETWORK EVIDENCE") {
		return BrowserNetworkSearchExample{}, false
	}
	var ex BrowserNetworkSearchExample
	ex.ToolIndex = index
	ex.CallID = c.CallID
	ex.Status = "unknown"
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "CURRENT_PAGE:"):
			ex.CurrentPageURL = compactOneLine(strings.TrimSpace(strings.TrimPrefix(trimmed, "CURRENT_PAGE:")), 500)
		case strings.HasPrefix(trimmed, "query:"):
			ex.Query = strings.Trim(strings.TrimSpace(strings.TrimPrefix(trimmed, "query:")), `"`)
			ex.Query = compactOneLine(ex.Query, 220)
		case strings.HasPrefix(trimmed, "EVIDENCE_STATUS:"):
			ex.EvidenceStatus = compactOneLine(strings.TrimSpace(strings.TrimPrefix(trimmed, "EVIDENCE_STATUS:")), 220)
		case trimmed == "MATCHES: none":
			ex.Status = "no_matches"
			ex.NotCitable = true
		case trimmed == "MATCHES:":
			ex.Status = "matches"
			ex.RequiresRead = true
			ex.NotCitable = true
		case strings.HasPrefix(trimmed, "- ") && ex.Status == "matches":
			if len(ex.Refs) < 8 {
				ref := strings.Fields(strings.TrimSpace(strings.TrimPrefix(trimmed, "- ")))
				if len(ref) > 0 {
					ex.Refs = append(ex.Refs, compactOneLine(ref[0], 80))
				}
			}
		case strings.HasPrefix(strings.ToLower(trimmed), "preview:"):
			if len(ex.Previews) < 4 {
				preview := compactOneLine(strings.TrimSpace(trimmed[len("preview:"):]), 220)
				if preview != "" {
					ex.Previews = append(ex.Previews, preview)
				}
			}
		case strings.HasPrefix(trimmed, "Next:"):
			ex.SuggestedNextStep = compactOneLine(strings.TrimSpace(strings.TrimPrefix(trimmed, "Next:")), 260)
		}
	}
	if ex.Status == "unknown" && len(ex.Refs) == 0 && ex.CurrentPageURL == "" && ex.Query == "" {
		return BrowserNetworkSearchExample{}, false
	}
	if ex.Status == "matches" && len(ex.Refs) == 0 {
		ex.RequiresRead = true
	}
	return ex, true
}

func memoryUpdateExampleForTool(index int, c ToolCall) (MemoryUpdateExample, bool) {
	if c.MemoryUpdate != nil {
		return memoryUpdateExampleFromMeta(index, c.CallID, c.MemoryUpdate)
	}
	if c.Tool != "memory" || c.ExitCode != 0 || c.IsErr {
		return MemoryUpdateExample{}, false
	}
	var resp struct {
		OK     bool   `json:"ok"`
		Target string `json:"target"`
		Topic  string `json:"topic"`
	}
	if err := json.Unmarshal([]byte(c.Result), &resp); err != nil || !resp.OK {
		return MemoryUpdateExample{}, false
	}
	action := strings.ToLower(strings.TrimSpace(memoryUpdateArg(c.Args, "action")))
	switch action {
	case "add", "replace", "remove":
	default:
		return MemoryUpdateExample{}, false
	}
	target := firstNonEmptyMemoryUpdateValue(resp.Target, memoryUpdateArg(c.Args, "target"), "memory")
	topic := firstNonEmptyMemoryUpdateValue(resp.Topic, memoryUpdateArg(c.Args, "topic"), "general")
	if target == "user" {
		topic = "user"
	}
	previousPreview := compactOneLine(memoryUpdateArg(c.Args, "old_text"), 160)
	nextPreview := compactOneLine(memoryUpdateArg(c.Args, "content"), 160)
	preview := memoryUpdateExamplePreview(action, previousPreview, nextPreview)
	return MemoryUpdateExample{
		ToolIndex:       index,
		CallID:          c.CallID,
		Action:          action,
		Target:          target,
		Topic:           topic,
		Location:        target + ":" + topic,
		Preview:         preview,
		PreviousPreview: previousPreview,
		NextPreview:     nextPreview,
	}, true
}

func memorySearchMissExampleForTool(index int, c ToolCall) (MemorySearchMissExample, bool) {
	if c.Tool != agent.MemoryToolName || c.ExitCode != 0 || c.IsErr || c.ResultTruncated {
		return MemorySearchMissExample{}, false
	}
	action := strings.TrimSpace(strings.ToLower(memoryUpdateArg(c.Args, "action")))
	if action != "search" {
		return MemorySearchMissExample{}, false
	}
	var resp memory.MemoryResponse
	if err := json.Unmarshal([]byte(c.Result), &resp); err != nil {
		return MemorySearchMissExample{}, false
	}
	if !resp.OK || len(resp.Results) > 0 || !strings.Contains(resp.Message, "no entries matched") {
		return MemorySearchMissExample{}, false
	}
	topics := make([]string, 0, min(len(resp.Topics), 5))
	for _, topic := range resp.Topics {
		name := strings.TrimSpace(topic.Topic)
		if name == "" {
			continue
		}
		topics = append(topics, compactOneLine(name, 80))
		if len(topics) >= 5 {
			break
		}
	}
	return MemorySearchMissExample{
		ToolIndex:  index,
		CallID:     c.CallID,
		Target:     compactOneLine(string(resp.Target), 80),
		Topic:      compactOneLine(resp.Topic, 120),
		Query:      compactOneLine(memoryUpdateArg(c.Args, "query"), 220),
		Message:    compactOneLine(resp.Message, 260),
		TopicCount: len(resp.Topics),
		Topics:     topics,
	}, true
}

func memoryUpdateExampleFromMeta(index int, callID string, meta *sse.MemoryUpdateMeta) (MemoryUpdateExample, bool) {
	if meta == nil {
		return MemoryUpdateExample{}, false
	}
	action := strings.ToLower(strings.TrimSpace(meta.Action))
	switch action {
	case "add", "replace", "remove":
	default:
		return MemoryUpdateExample{}, false
	}
	target := firstNonEmptyMemoryUpdateValue(meta.Target, "memory")
	topic := firstNonEmptyMemoryUpdateValue(meta.Topic, "general")
	if target == "user" {
		topic = "user"
	}
	location := strings.TrimSpace(meta.Location)
	if location == "" {
		location = target + ":" + topic
	}
	previousPreview := compactOneLine(meta.PreviousPreview, 160)
	nextPreview := compactOneLine(meta.NextPreview, 160)
	preview := compactOneLine(meta.Preview, 220)
	if preview == "" {
		preview = memoryUpdateExamplePreview(action, previousPreview, nextPreview)
	}
	return MemoryUpdateExample{
		ToolIndex:       index,
		CallID:          callID,
		Action:          action,
		Target:          target,
		Topic:           topic,
		Location:        location,
		Preview:         preview,
		PreviousPreview: previousPreview,
		NextPreview:     nextPreview,
	}, true
}

func memoryUpdateExamplePreview(action, previousPreview, nextPreview string) string {
	switch action {
	case "add":
		return nextPreview
	case "replace":
		if previousPreview != "" && nextPreview != "" {
			return previousPreview + " -> " + nextPreview
		}
		return firstNonEmptyMemoryUpdateValue(nextPreview, previousPreview)
	case "remove":
		return previousPreview
	default:
		return ""
	}
}

func memoryUpdateArg(args map[string]any, key string) string {
	if len(args) == 0 {
		return ""
	}
	value, ok := args[key]
	if !ok {
		return ""
	}
	if s, ok := value.(string); ok {
		return strings.TrimSpace(s)
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func firstNonEmptyMemoryUpdateValue(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func toolFailureKindsForCall(c ToolCall) []string {
	return taskstate.ToolFailureKinds(taskstate.ToolResult{
		Tool:          c.Tool,
		TurnID:        c.TurnID,
		CallID:        c.CallID,
		Result:        c.Result,
		ResultSummary: c.ResultSummary,
		FailureKind:   c.FailureKind,
		FailureKinds:  c.FailureKinds,
		ExitCode:      c.ExitCode,
	}, taskstate.DefaultMaxItems)
}

func summarizeToolCallArgs(args map[string]any) string {
	if len(args) == 0 {
		return ""
	}
	for _, key := range []string{"command", "path", "url", "query", "name", "id"} {
		if value, ok := args[key]; ok {
			return compactOneLine(fmt.Sprintf("%s=%q", key, fmt.Sprint(value)), 180)
		}
	}
	raw, err := json.Marshal(args)
	if err != nil {
		return ""
	}
	return compactOneLine(string(raw), 180)
}

func summarizeToolFailureResult(result string) string {
	var parts []string
	for _, raw := range strings.Split(result, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "Failure: kind=") {
			continue
		}
		parts = append(parts, line)
		if strings.HasPrefix(line, "Next:") || len(parts) >= 2 {
			break
		}
	}
	return compactOneLine(strings.Join(parts, " | "), 260)
}

func summarizeLoopGuardMessage(result string) string {
	for _, raw := range strings.Split(result, "\n") {
		line := strings.TrimSpace(raw)
		for _, prefix := range []string{"loop_guard:", "first_tool_policy:", "post_tool_policy:"} {
			if !strings.HasPrefix(line, prefix) {
				continue
			}
			return compactOneLine(strings.TrimSpace(strings.TrimPrefix(line, prefix)), 260)
		}
	}
	return ""
}

func summarizeLoopGuardNextStep(result string) string {
	return summarizeToolNextStep(result)
}

func summarizeToolNextStep(result string) string {
	for _, raw := range strings.Split(result, "\n") {
		line := strings.TrimSpace(raw)
		if !strings.HasPrefix(line, "Next:") {
			continue
		}
		return compactOneLine(strings.TrimSpace(strings.TrimPrefix(line, "Next:")), 260)
	}
	return ""
}

func loopGuardExampleCategory(kind string) (string, bool) {
	switch {
	case strings.HasPrefix(kind, "loop_guard_"):
		return "loop_guard", true
	case strings.HasPrefix(kind, "tool_policy_"):
		return "tool_policy", true
	default:
		return "", false
	}
}

func compactOneLine(s string, max int) string {
	s = textutil.CompactWhitespace(s)
	if max <= 0 || len(s) <= max {
		return s
	}
	if max <= 3 {
		return textutil.PreviewRunes(s, max, "")
	}
	return textutil.PreviewRunes(s, max-3, "...")
}

func (t Trace) LoopErrorKindCounts() map[string]int {
	if len(t.LoopErrorKinds) == 0 {
		return nil
	}
	out := make(map[string]int, len(t.LoopErrorKinds))
	for _, kind := range t.LoopErrorKinds {
		if kind == "" {
			continue
		}
		out[kind]++
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (t Trace) RuntimeErrorExamples(maxPerKind int) map[string][]RuntimeErrorExample {
	if maxPerKind <= 0 {
		return nil
	}
	out := map[string][]RuntimeErrorExample{}
	for _, ex := range t.RuntimeErrors {
		if ex.Kind == "" || strings.TrimSpace(ex.Message) == "" {
			continue
		}
		if len(out[ex.Kind]) >= maxPerKind {
			continue
		}
		out[ex.Kind] = append(out[ex.Kind], RuntimeErrorExample{
			Kind:    ex.Kind,
			Message: compactOneLine(ex.Message, 320),
		})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (t Trace) LoopDecisionStats(maxExamples int) LoopDecisionStats {
	stats := LoopDecisionStats{}
	for _, decision := range t.LoopDecisions {
		stats.Count++
		if decision.Kind != "" {
			if stats.ByKind == nil {
				stats.ByKind = map[string]int{}
			}
			stats.ByKind[decision.Kind]++
		}
		if decision.Decision != "" {
			if stats.ByDecision == nil {
				stats.ByDecision = map[string]int{}
			}
			stats.ByDecision[decision.Decision]++
		}
		matchKey := loopDecisionMatchKey(decision.Kind, decision.Decision, decision.Trigger)
		if matchKey != "" {
			if stats.ByMatch == nil {
				stats.ByMatch = map[string]int{}
			}
			stats.ByMatch[matchKey]++
		}
		if maxExamples <= 0 || len(stats.Examples) >= maxExamples {
			continue
		}
		stats.Examples = append(stats.Examples, LoopDecision{
			Kind:                 decision.Kind,
			Decision:             decision.Decision,
			Trigger:              decision.Trigger,
			Confidence:           decision.Confidence,
			Reason:               compactOneLine(decision.Reason, 260),
			RequiredAction:       compactOneLine(decision.RequiredAction, 260),
			TokenBudget:          decision.TokenBudget,
			ObservedInputTokens:  decision.ObservedInputTokens,
			ProjectedInputTokens: decision.ProjectedInputTokens,
			BudgetBytes:          decision.BudgetBytes,
			TurnID:               decision.TurnID,
			DecisionID:           decision.DecisionID,
		})
	}
	return stats
}

func (t Trace) MessageRejectedStats(maxExamples int) MessageRejectedStats {
	stats := MessageRejectedStats{}
	for _, rejected := range t.MessageRejections {
		stats.Count++
		if rejected.Trigger != "" {
			if stats.ByTrigger == nil {
				stats.ByTrigger = map[string]int{}
			}
			stats.ByTrigger[rejected.Trigger]++
		}
		if maxExamples <= 0 || len(stats.Examples) >= maxExamples {
			continue
		}
		stats.Examples = append(stats.Examples, MessageRejected{
			Scenario:       rejected.Scenario,
			TurnID:         rejected.TurnID,
			Text:           compactOneLine(rejected.Text, 260),
			Reason:         compactOneLine(rejected.Reason, 260),
			Trigger:        rejected.Trigger,
			RequiredAction: compactOneLine(rejected.RequiredAction, 260),
		})
	}
	return stats
}

func (t Trace) LoopProtocolFeedStats(maxExamples int) LoopProtocolFeedStats {
	stats := LoopProtocolFeedStats{}
	for _, feed := range t.LoopProtocolFeeds {
		stats.Count++
		if feed.Mode != "" {
			if stats.ByMode == nil {
				stats.ByMode = map[string]int{}
			}
			stats.ByMode[feed.Mode]++
		}
		stats.Latest = feed
		if maxExamples <= 0 || len(stats.Examples) >= maxExamples {
			continue
		}
		stats.Examples = append(stats.Examples, LoopProtocolFeed{
			TurnID:                       feed.TurnID,
			LoopID:                       feed.LoopID,
			Status:                       feed.Status,
			Mode:                         feed.Mode,
			FeedNumber:                   feed.FeedNumber,
			ProtocolFeeds:                feed.ProtocolFeeds,
			CalibrationAnswers:           feed.CalibrationAnswers,
			LastCalibrationAnswer:        feed.LastCalibrationAnswer,
			ProtocolPath:                 feed.ProtocolPath,
			CurrentSituation:             feed.CurrentSituation,
			PlanLabel:                    feed.PlanLabel,
			PlanCurrentStepIndex:         feed.PlanCurrentStepIndex,
			PlanCurrentStepStatus:        feed.PlanCurrentStepStatus,
			PlanCurrentStep:              feed.PlanCurrentStep,
			LastTurnID:                   feed.LastTurnID,
			LastTurnEndReason:            feed.LastTurnEndReason,
			LastTurnToolRequests:         feed.LastTurnToolRequests,
			LastTurnToolRequestsAdmitted: feed.LastTurnToolRequestsAdmitted,
			LastTurnToolRequestsSkipped:  feed.LastTurnToolRequestsSkipped,
			LastTurnToolErrors:           feed.LastTurnToolErrors,
			LastTurnForcedNoTools:        feed.LastTurnForcedNoTools,
			LastTurnMemoryUpdates:        feed.LastTurnMemoryUpdates,
			LastTurnMemorySearchCalls:    feed.LastTurnMemorySearchCalls,
			LastTurnMemorySearchMisses:   feed.LastTurnMemorySearchMisses,
			LastTurnSessionSearchCalls:   feed.LastTurnSessionSearchCalls,
			LastTurnLoopGuards:           feed.LastTurnLoopGuards,
			LastDecisionKind:             feed.LastDecisionKind,
			LastDecisionTrigger:          feed.LastDecisionTrigger,
			LastDecision:                 feed.LastDecision,
			LastDecisionConfidence:       feed.LastDecisionConfidence,
			LastDecisionReason:           feed.LastDecisionReason,
			LastDecisionAction:           feed.LastDecisionAction,
			LastDecisionTokenBudget:      feed.LastDecisionTokenBudget,
			LastDecisionObservedInput:    feed.LastDecisionObservedInput,
			LastDecisionProjectedInput:   feed.LastDecisionProjectedInput,
			LastDecisionBudgetBytes:      feed.LastDecisionBudgetBytes,
		})
	}
	return stats
}

func (t Trace) LoopProtocolCalibrationStats(maxExamples int) LoopProtocolCalibrationStats {
	stats := LoopProtocolCalibrationStats{}
	for _, calibration := range t.LoopProtocolCalibrations {
		stats.Count++
		stats.Latest = calibration
		if maxExamples <= 0 || len(stats.Examples) >= maxExamples {
			continue
		}
		stats.Examples = append(stats.Examples, LoopProtocolCalibration{
			LoopID:                  calibration.LoopID,
			Status:                  calibration.Status,
			CalibrationQuestions:    calibration.CalibrationQuestions,
			LastCalibrationQuestion: calibration.LastCalibrationQuestion,
			CalibrationAnswers:      calibration.CalibrationAnswers,
			LastCalibrationAnswer:   calibration.LastCalibrationAnswer,
			ProtocolPath:            calibration.ProtocolPath,
			EventSeq:                calibration.EventSeq,
		})
	}
	return stats
}

func (t Trace) LoopTurnCheckpointStats(maxExamples int) LoopTurnCheckpointStats {
	stats := LoopTurnCheckpointStats{}
	for _, checkpoint := range t.LoopTurnCheckpoints {
		stats.Count++
		if checkpoint.ToolRequests > stats.MaxToolRequests {
			stats.MaxToolRequests = checkpoint.ToolRequests
		}
		if checkpoint.ToolRequestsAdmitted > stats.MaxToolRequestsAdmitted {
			stats.MaxToolRequestsAdmitted = checkpoint.ToolRequestsAdmitted
		}
		if checkpoint.ToolRequestsSkipped > stats.MaxToolRequestsSkipped {
			stats.MaxToolRequestsSkipped = checkpoint.ToolRequestsSkipped
		}
		if checkpoint.InputTokens > stats.MaxInputTokens {
			stats.MaxInputTokens = checkpoint.InputTokens
		}
		if total := checkpoint.InputTokens + checkpoint.OutputTokens; total > stats.MaxTotalTokens {
			stats.MaxTotalTokens = total
		}
		stats.Latest = checkpoint
		if maxExamples <= 0 || len(stats.Examples) >= maxExamples {
			continue
		}
		stats.Examples = append(stats.Examples, LoopTurnCheckpoint{
			TurnID:                   checkpoint.TurnID,
			LoopID:                   checkpoint.LoopID,
			Status:                   checkpoint.Status,
			ProtocolPath:             checkpoint.ProtocolPath,
			FinalizationPolicy:       checkpoint.FinalizationPolicy,
			RequiresCloseBeforeFinal: checkpoint.RequiresCloseBeforeFinal,
			EventSeq:                 checkpoint.EventSeq,
			TurnCheckpoints:          checkpoint.TurnCheckpoints,
			EndReason:                checkpoint.EndReason,
			InputTokens:              checkpoint.InputTokens,
			OutputTokens:             checkpoint.OutputTokens,
			ToolRequests:             checkpoint.ToolRequests,
			ToolRequestsAdmitted:     checkpoint.ToolRequestsAdmitted,
			ToolRequestsSkipped:      checkpoint.ToolRequestsSkipped,
			ToolErrors:               checkpoint.ToolErrors,
			LoopGuards:               checkpoint.LoopGuards,
			ForcedNoTools:            checkpoint.ForcedNoTools,
			MemoryUpdates:            checkpoint.MemoryUpdates,
			MemorySearchCalls:        checkpoint.MemorySearchCalls,
			MemoryMisses:             checkpoint.MemoryMisses,
			SessionSearchCalls:       checkpoint.SessionSearchCalls,
		})
	}
	return stats
}

func (t Trace) ContextInjectionStats(maxExamples int) ContextInjectionStats {
	stats := ContextInjectionStats{}
	for _, injection := range t.ContextInjections {
		stats.Count++
		stats.Bytes += injection.Bytes
		stats.EstimatedTokens += injection.EstimatedTokens
		if injection.Source != "" {
			if stats.BySource == nil {
				stats.BySource = map[string]int{}
			}
			stats.BySource[injection.Source]++
		}
		stats.Latest = injection
		if maxExamples <= 0 || len(stats.Examples) >= maxExamples {
			continue
		}
		stats.Examples = append(stats.Examples, ContextInjection{
			TurnID:          injection.TurnID,
			Source:          injection.Source,
			Name:            injection.Name,
			Title:           compactOneLine(injection.Title, 160),
			Summary:         compactOneLine(injection.Summary, 260),
			Preview:         compactOneLine(injection.Preview, 360),
			ContentSHA256:   injection.ContentSHA256,
			Bytes:           injection.Bytes,
			EstimatedTokens: injection.EstimatedTokens,
		})
	}
	return stats
}

func (t Trace) LoopProtocolCalibrationRequestStats(maxExamples int) LoopProtocolCalibrationStats {
	stats := LoopProtocolCalibrationStats{}
	for _, request := range t.LoopProtocolCalibrationRequests {
		stats.Count++
		stats.Latest = request
		if maxExamples <= 0 || len(stats.Examples) >= maxExamples {
			continue
		}
		stats.Examples = append(stats.Examples, LoopProtocolCalibration{
			LoopID:                  request.LoopID,
			Status:                  request.Status,
			CalibrationQuestions:    request.CalibrationQuestions,
			LastCalibrationQuestion: request.LastCalibrationQuestion,
			CalibrationAnswers:      request.CalibrationAnswers,
			LastCalibrationAnswer:   request.LastCalibrationAnswer,
			ProtocolPath:            request.ProtocolPath,
			EventSeq:                request.EventSeq,
		})
	}
	return stats
}

func loopDecisionMatchKey(kind, decision, trigger string) string {
	if kind == "" || decision == "" || trigger == "" {
		return ""
	}
	return kind + "\x00" + decision + "\x00" + trigger
}

func (t Trace) ContextCompactionStats(maxExamples int) ContextCompactionStats {
	stats := ContextCompactionStats{}
	for _, compaction := range t.ContextCompactions {
		stats.Count++
		if stats.ByReason == nil {
			stats.ByReason = map[string]int{}
		}
		reason := strings.TrimSpace(compaction.Reason)
		if reason == "" {
			reason = "unknown"
		}
		stats.ByReason[reason]++
		if compaction.Reactive {
			stats.Reactive++
		} else {
			stats.Proactive++
		}
		stats.RemovedMessages += compaction.RemovedMessages
		stats.ReducedBytes += compaction.ReducedBytes
		stats.SummaryBytes += compaction.SummaryBytes
		if compaction.EstimatedInputTokens > 0 && compaction.TriggerInputTokens > 0 {
			stats.PolicyObserved++
			pressure := contextCompactionPolicyPressurePercent(compaction.EstimatedInputTokens, compaction.TriggerInputTokens)
			if pressure > stats.MaxPolicyPressurePercent {
				stats.MaxPolicyPressurePercent = pressure
			}
		}
		if compaction.AfterEstimatedInputTokens > 0 && compaction.TriggerInputTokens > 0 {
			stats.PostPolicyObserved++
			pressure := contextCompactionPolicyPressurePercent(compaction.AfterEstimatedInputTokens, compaction.TriggerInputTokens)
			if pressure > stats.MaxPostPolicyPressurePercent {
				stats.MaxPostPolicyPressurePercent = pressure
			}
			if compaction.AfterEstimatedInputTokens >= compaction.TriggerInputTokens {
				stats.PostPolicyStillOverTrigger++
			}
		}
		if contextCompactionSummaryMissing(compaction) {
			stats.SummaryMissing++
		}
		if contextCompactionSummaryEmpty(compaction) {
			stats.SummaryEmpty++
		}
		if maxExamples <= 0 || len(stats.Examples) >= maxExamples {
			continue
		}
		stats.Examples = append(stats.Examples, ContextCompaction{
			TurnID:                             compaction.TurnID,
			BeforeMessages:                     compaction.BeforeMessages,
			AfterMessages:                      compaction.AfterMessages,
			RemovedMessages:                    compaction.RemovedMessages,
			BeforeBytes:                        compaction.BeforeBytes,
			AfterBytes:                         compaction.AfterBytes,
			ReducedBytes:                       compaction.ReducedBytes,
			EstimatedInputTokens:               compaction.EstimatedInputTokens,
			AfterEstimatedInputTokens:          compaction.AfterEstimatedInputTokens,
			TriggerInputTokens:                 compaction.TriggerInputTokens,
			ModelContextWindowTokens:           compaction.ModelContextWindowTokens,
			ModelContextWindowEffectivePercent: compaction.ModelContextWindowEffectivePercent,
			ReservedOutputTokens:               compaction.ReservedOutputTokens,
			CompactTriggerInputPercent:         compaction.CompactTriggerInputPercent,
			CompactScopeActive:                 compaction.CompactScopeActive,
			CompactWindowOrdinal:               compaction.CompactWindowOrdinal,
			CompactWindowPrefillInputTokens:    compaction.CompactWindowPrefillInputTokens,
			CompactScopedInputTokens:           compaction.CompactScopedInputTokens,
			CompactHardInputLimitTokens:        compaction.CompactHardInputLimitTokens,
			Reactive:                           compaction.Reactive,
			Reason:                             compaction.Reason,
			SummaryPresent:                     compaction.SummaryPresent,
			SummaryPresentKnown:                compaction.SummaryPresentKnown,
			SummaryBytes:                       compaction.SummaryBytes,
			SummaryPreview:                     compactOneLine(compaction.SummaryPreview, 600),
			LoopProtocolAnchor:                 compaction.LoopProtocolAnchor,
		})
	}
	return stats
}

func (t Trace) ContextCompactionSkipStats(maxExamples int) ContextCompactionSkipStats {
	stats := ContextCompactionSkipStats{}
	for _, skipped := range t.ContextCompactionSkips {
		stats.Count++
		if stats.ByCause == nil {
			stats.ByCause = map[string]int{}
		}
		if stats.ByReason == nil {
			stats.ByReason = map[string]int{}
		}
		cause := strings.TrimSpace(skipped.Cause)
		if cause == "" {
			cause = "unknown"
		}
		reason := strings.TrimSpace(skipped.Reason)
		if reason == "" {
			reason = "unknown"
		}
		stats.ByCause[cause]++
		stats.ByReason[reason]++
		if skipped.EstimatedInputTokens > 0 && skipped.TriggerInputTokens > 0 {
			stats.PolicyObserved++
			pressure := contextCompactionPolicyPressurePercent(skipped.EstimatedInputTokens, skipped.TriggerInputTokens)
			if pressure > stats.MaxPolicyPressurePercent {
				stats.MaxPolicyPressurePercent = pressure
			}
		}
		if skipped.AfterEstimatedInputTokens > 0 && skipped.TriggerInputTokens > 0 {
			stats.PostPolicyObserved++
			pressure := contextCompactionPolicyPressurePercent(skipped.AfterEstimatedInputTokens, skipped.TriggerInputTokens)
			if pressure > stats.MaxPostPolicyPressurePercent {
				stats.MaxPostPolicyPressurePercent = pressure
			}
			if skipped.AfterEstimatedInputTokens >= skipped.TriggerInputTokens {
				stats.PostPolicyStillOverTrigger++
			}
		}
		if maxExamples <= 0 || len(stats.Examples) >= maxExamples {
			continue
		}
		stats.Examples = append(stats.Examples, skipped)
	}
	return stats
}

func contextCompactionPolicyPressurePercent(estimatedInputTokens, triggerInputTokens int) int {
	if estimatedInputTokens <= 0 || triggerInputTokens <= 0 {
		return 0
	}
	return (estimatedInputTokens*100 + triggerInputTokens - 1) / triggerInputTokens
}

func contextCompactionSummaryMissing(compaction ContextCompaction) bool {
	return compaction.SummaryPresentKnown && !compaction.SummaryPresent && compaction.SummaryBytes == 0 && strings.TrimSpace(compaction.SummaryPreview) == ""
}

func contextCompactionSummaryEmpty(compaction ContextCompaction) bool {
	return compaction.SummaryPresentKnown && compaction.SummaryPresent && compaction.SummaryBytes == 0 && strings.TrimSpace(compaction.SummaryPreview) == ""
}

func contextCompactionSummaryState(compaction ContextCompaction) string {
	if compaction.SummaryBytes > 0 || strings.TrimSpace(compaction.SummaryPreview) != "" {
		return "present"
	}
	if !compaction.SummaryPresentKnown {
		return "unknown"
	}
	if !compaction.SummaryPresent {
		return "missing"
	}
	return "empty"
}

func containsString(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func (s ToolRuntimeStats) hasRepairStats() bool {
	return s.ToolRepairCalls > 0 ||
		s.ToolRepairSucceeded > 0 ||
		s.ToolRepairFailed > 0 ||
		s.ToolRepairNotes > 0 ||
		len(s.ToolRepairByKind) > 0
}

func (t Trace) repairStatsFromRequests() ToolRepairStats {
	var s ToolRepairStats
	for _, c := range t.Tools {
		if !c.Canonicalized && !c.ArgsRepaired && len(c.RepairNotes) == 0 {
			continue
		}
		s.Calls++
		if c.ExitCode == 0 {
			s.SucceededCalls++
		} else {
			s.FailedCalls++
		}
		seenNote := false
		for _, note := range c.RepairNotes {
			kind := toolrepair.Kind(note)
			if kind == "" {
				continue
			}
			seenNote = true
			s.Notes++
			if s.ByKind == nil {
				s.ByKind = map[string]int{}
			}
			s.ByKind[kind]++
		}
		if !seenNote && c.Canonicalized {
			s.addKind("tool_name")
		}
		if !seenNote && c.ArgsRepaired {
			s.addKind("malformed_json")
		}
	}
	return s
}

func (s *ToolRepairStats) addKind(kind string) {
	s.Notes++
	if s.ByKind == nil {
		s.ByKind = map[string]int{}
	}
	s.ByKind[kind]++
}

func repairKindsForCall(c ToolCall) []string {
	seen := map[string]bool{}
	var out []string
	for _, note := range c.RepairNotes {
		kind := toolrepair.Kind(note)
		if kind == "" || seen[kind] {
			continue
		}
		seen[kind] = true
		out = append(out, kind)
	}
	classified := len(out) > 0
	if !classified && c.Canonicalized {
		out = append(out, "tool_name")
	}
	if !classified && c.ArgsRepaired {
		out = append(out, "malformed_json")
	}
	return out
}

func cloneStringIntMap(in map[string]int) map[string]int {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]int, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneStringSliceMap(in map[string][]string) map[string][]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string][]string, len(in))
	for k, v := range in {
		out[k] = append([]string(nil), v...)
	}
	return out
}

func sumStringIntMap(in map[string]int) int {
	var total int
	for _, v := range in {
		total += v
	}
	return total
}

// Usage aggregates per-turn token accounting summed across every LLM
// call in the run.
type Usage struct {
	InputTokens  int
	OutputTokens int
}

// DelegationStats is a per-kind breakdown of delegation tool calls
// observed in a Trace. It is computed from the Tools timeline by
// Trace.DelegationStats(); the per-kind sub-map keys (task_type for
// focused_task, mode for subagent) come straight from the
// sse.DelegationMeta the runtime stamped on each tool event.
//
// Empty when the run had no delegation calls; absent fields when a
// kind wasn't used.
type DelegationStats struct {
	// FocusedTaskCalls is the total number of run_task tool calls.
	FocusedTaskCalls int
	// FocusedTaskByType breaks the total down by task_type
	// (recall / explore / web_extract / research / verify / review).
	// Keys with zero counts are not included.
	FocusedTaskByType map[string]int
	// FocusedTaskSourceFindingsByType counts findings with non-empty source
	// fields in successful structured run_task results, grouped by task_type.
	// It lets evals distinguish "delegated to research" from "delegated
	// research returned cited evidence".
	FocusedTaskSourceFindingsByType map[string]int
	// FocusedTaskErrors counts run_task calls that failed at runtime or
	// returned ok:false for non-verify task types. Verify tasks may use
	// ok:false to mean "claim falsified", which is a valid outcome.
	FocusedTaskErrors int
	// FocusedTaskIncomplete is the subset of FocusedTaskErrors caused
	// by a completed run_task result JSON returning ok:false rather
	// than by transport/runtime failure.
	FocusedTaskIncomplete int
	// SubagentCalls is the total number of subagent_run tool calls.
	SubagentCalls int
	// SubagentByMode breaks the subagent total down by mode
	// (explore / review / test / research). Keys with zero counts
	// are not included.
	SubagentByMode map[string]int
	// SubagentSourceEvidenceByMode counts source-bearing report lines in
	// successful structured subagent_run results, grouped by mode.
	// Subagent reports are prose, so this uses conservative Evidence /
	// Files inspected / Commands run section parsing instead of treating a
	// bare mode=research call as externally evidenced.
	SubagentSourceEvidenceByMode map[string]int
	// SubagentErrors counts subagent_run calls that failed at runtime or
	// returned ok:false, which means the child report is partial or has
	// unresolved gaps.
	SubagentErrors int
	// SubagentIncomplete is the subset of SubagentErrors caused by an
	// ok:false child report rather than a tool/runtime failure.
	SubagentIncomplete int
}

// HasAny reports whether any delegation calls were observed. Helps
// summary writers decide whether to include the DelegationStats block
// at all (most scenarios won't use any delegation tool).
func (d DelegationStats) HasAny() bool {
	return d.FocusedTaskCalls > 0 || d.SubagentCalls > 0
}

// DelegationStats walks the Tools timeline and aggregates per-kind
// counts. Costs O(len(Tools)) and allocates only when a delegation
// was actually observed; cheap to call on every scenario summary.
func (t Trace) DelegationStats() DelegationStats {
	var s DelegationStats
	for _, c := range t.Tools {
		if c.Delegation == nil {
			continue
		}
		switch c.Delegation.Kind {
		case agent.DelegationKindFocusedTask:
			s.FocusedTaskCalls++
			incomplete := focusedTaskResultCountsAsError(c)
			if incomplete {
				s.FocusedTaskIncomplete++
			}
			if c.IsErr || c.ExitCode != 0 || incomplete {
				s.FocusedTaskErrors++
			}
			if tt := c.Delegation.TaskType; tt != "" {
				if s.FocusedTaskByType == nil {
					s.FocusedTaskByType = map[string]int{}
				}
				s.FocusedTaskByType[tt]++
				if sourced := focusedTaskSourceFindingCount(c, tt); sourced > 0 {
					if s.FocusedTaskSourceFindingsByType == nil {
						s.FocusedTaskSourceFindingsByType = map[string]int{}
					}
					s.FocusedTaskSourceFindingsByType[tt] += sourced
				}
			}
		case agent.DelegationKindSubagent:
			s.SubagentCalls++
			incomplete := subagentResultCountsAsError(c)
			if incomplete {
				s.SubagentIncomplete++
			}
			if c.IsErr || c.ExitCode != 0 || incomplete {
				s.SubagentErrors++
			}
			if m := c.Delegation.Mode; m != "" {
				if s.SubagentByMode == nil {
					s.SubagentByMode = map[string]int{}
				}
				s.SubagentByMode[m]++
				if sourced := subagentSourceEvidenceCount(c, m); sourced > 0 {
					if s.SubagentSourceEvidenceByMode == nil {
						s.SubagentSourceEvidenceByMode = map[string]int{}
					}
					s.SubagentSourceEvidenceByMode[m] += sourced
				}
			}
		}
	}
	return s
}

func focusedTaskSourceFindingCount(c ToolCall, fallbackTaskType string) int {
	if c.Tool != agent.FocusedTaskToolName || strings.TrimSpace(c.Result) == "" {
		return 0
	}
	var payload struct {
		OK       *bool  `json:"ok"`
		TaskType string `json:"task_type"`
		Findings []struct {
			Source string `json:"source"`
		} `json:"findings"`
	}
	if err := json.Unmarshal([]byte(c.Result), &payload); err != nil {
		return 0
	}
	if payload.OK != nil && !*payload.OK {
		return 0
	}
	taskType := strings.TrimSpace(payload.TaskType)
	if taskType == "" {
		taskType = strings.TrimSpace(fallbackTaskType)
	}
	if taskType == "" {
		return 0
	}
	var count int
	for _, finding := range payload.Findings {
		if strings.TrimSpace(finding.Source) != "" {
			count++
		}
	}
	return count
}

func focusedTaskResultCountsAsError(c ToolCall) bool {
	if c.Tool != agent.FocusedTaskToolName || strings.TrimSpace(c.Result) == "" {
		return false
	}
	var payload struct {
		OK       *bool  `json:"ok"`
		TaskType string `json:"task_type"`
	}
	if err := json.Unmarshal([]byte(c.Result), &payload); err != nil || payload.OK == nil || *payload.OK {
		return false
	}
	taskType := strings.TrimSpace(payload.TaskType)
	if taskType == "" && c.Delegation != nil {
		taskType = strings.TrimSpace(c.Delegation.TaskType)
	}
	return taskType != string(agent.FocusedTaskVerify)
}

func subagentSourceEvidenceCount(c ToolCall, fallbackMode string) int {
	if c.Tool != agent.SubagentToolName || strings.TrimSpace(c.Result) == "" {
		return 0
	}
	var payload struct {
		OK     *bool  `json:"ok"`
		Mode   string `json:"mode"`
		Report string `json:"report"`
	}
	if err := json.Unmarshal([]byte(c.Result), &payload); err != nil {
		return 0
	}
	if payload.OK != nil && !*payload.OK {
		return 0
	}
	mode := strings.TrimSpace(payload.Mode)
	if mode == "" {
		mode = strings.TrimSpace(fallbackMode)
	}
	if mode == "" {
		return 0
	}
	return sourceEvidenceLinesInSubagentReport(payload.Report)
}

func sourceEvidenceLinesInSubagentReport(report string) int {
	var count int
	section := ""
	for _, line := range strings.Split(report, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if name, ok := subagentReportSectionName(trimmed); ok {
			section = name
			continue
		}
		if section == "" || subagentReportSectionBoundary(trimmed) {
			if subagentReportSectionBoundary(trimmed) {
				section = ""
			}
			continue
		}
		item := strings.TrimSpace(strings.TrimLeft(trimmed, "-*0123456789.、) \t"))
		if item == "" || reportSectionItemMeansNoneForEval(item) {
			continue
		}
		if subagentSectionItemHasSource(section, item) {
			count++
		}
	}
	return count
}

func subagentReportSectionName(line string) (string, bool) {
	trimmed := strings.ToLower(strings.TrimSpace(strings.Trim(line, "#* ")))
	trimmed = strings.TrimSuffix(strings.TrimSuffix(trimmed, ":"), "：")
	switch trimmed {
	case "evidence", "files inspected", "commands run", "sources", "source":
		return trimmed, true
	default:
		return "", false
	}
}

func subagentReportSectionBoundary(line string) bool {
	trimmed := strings.TrimSpace(strings.Trim(line, "#* "))
	if trimmed == "" {
		return false
	}
	if strings.HasPrefix(trimmed, "-") || strings.HasPrefix(trimmed, "*") {
		return false
	}
	if strings.HasPrefix(trimmed, "#") {
		return true
	}
	return strings.HasSuffix(trimmed, ":") || strings.HasSuffix(trimmed, "：")
}

func subagentSectionItemHasSource(section, item string) bool {
	lower := strings.ToLower(item)
	switch section {
	case "files inspected", "commands run", "sources", "source":
		return true
	case "evidence":
		return strings.Contains(lower, "source:") ||
			strings.Contains(lower, "source=") ||
			strings.Contains(lower, "file:") ||
			strings.Contains(lower, "path:") ||
			strings.Contains(lower, "session") ||
			strings.Contains(lower, "memory") ||
			strings.Contains(lower, "http://") ||
			strings.Contains(lower, "https://") ||
			strings.Contains(lower, ".md") ||
			strings.Contains(lower, ".go") ||
			strings.Contains(lower, ".json") ||
			strings.Contains(lower, ".txt") ||
			strings.Contains(item, "/")
	default:
		return false
	}
}

func reportSectionItemMeansNoneForEval(item string) bool {
	switch strings.ToLower(strings.TrimSpace(item)) {
	case "none", "none.", "n/a", "na", "no", "no.", "无", "无。", "没有", "没有。":
		return true
	default:
		return false
	}
}

func subagentResultCountsAsError(c ToolCall) bool {
	if c.Tool != agent.SubagentToolName || strings.TrimSpace(c.Result) == "" {
		return false
	}
	var payload struct {
		OK *bool `json:"ok"`
	}
	if err := json.Unmarshal([]byte(c.Result), &payload); err != nil || payload.OK == nil {
		return false
	}
	return !*payload.OK
}

// Outcome is the result of running one scenario through its checks.
// Pass is the conjunction of every CheckResult.Pass — a single
// failing check fails the whole scenario.
type Outcome struct {
	Scenario string
	Trace    Trace
	Results  []CheckResult
	Pass     bool
}

// PassCount returns how many checks passed. Pair with len(Results)
// for a "k of N" report.
func (o Outcome) PassCount() int {
	n := 0
	for _, r := range o.Results {
		if r.Pass {
			n++
		}
	}
	return n
}

// FailedChecks returns the names of the checks that did not pass.
// Convenience for logs and assertions.
func (o Outcome) FailedChecks() []string {
	var out []string
	for _, r := range o.Results {
		if !r.Pass {
			out = append(out, r.Check)
		}
	}
	return out
}

// RunnerExecutorBuilder is the constructor signature the Runner uses
// to mint a fresh executor per scenario. Exported so callers can
// swap LocalExecutor for a sandboxed/docker variant during eval.
type RunnerExecutorBuilder func(workspaceDir string) (executor.Executor, error)

// RunnerRegistryBuilder mints the tool registry the scenario sees.
// Defaults to agent.RegisterBuiltins on a LocalExecutor; callers
// composing additional capabilities (browser, subagent) replace it.
type RunnerRegistryBuilder func(ctx context.Context, workspaceDir string, exec executor.Executor) (*agent.Registry, error)
