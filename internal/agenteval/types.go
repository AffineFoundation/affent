package agenteval

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/affinefoundation/affent/internal/agent"
	"github.com/affinefoundation/affent/internal/executor"
	"github.com/affinefoundation/affent/internal/sourceaccess"
	"github.com/affinefoundation/affent/internal/sse"
	"github.com/affinefoundation/affent/internal/textutil"
	"github.com/affinefoundation/affent/internal/toolfailure"
	"github.com/affinefoundation/affent/internal/toolrepair"
)

// Scenario describes one bounded evaluation task. Scenarios are
// deterministic and reproducible: same Setup + same LLM (real or
// fake) + same Prompt → same Trace.
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
	// case Setup must SendUser itself — but the v0 framework only
	// supports single-prompt scenarios).
	Prompt string

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
	// LoopDecisions records structured protocol/runtime decisions such as
	// evidence_quality defer events. These are separate from assistant text so
	// evals can measure when guardrails fired and whether they were actionable.
	LoopDecisions []LoopDecision
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
	// ContextCompactions records model-context rewrites produced by the
	// rolling compactor. The full user-visible trace remains in events.jsonl;
	// these entries let long-run evals assert that context pressure was handled.
	ContextCompactions []ContextCompaction
	// EventOrder preserves a compact chronological index for trace assertions
	// that depend on sequencing across event families, such as confirming a
	// full LOOP.md feed occurred after context compaction.
	EventOrder []TraceEventRef
	// RuntimeSurfaces records the effective tool/runtime surface at turn
	// start. This lets eval/debug tooling explain missing web/browser/memory
	// behavior without inferring availability from later tool calls.
	RuntimeSurfaces []sse.RuntimeSurfacePayload

	// RawTypes counts every event type the run produced, by name
	// (e.g. {"tool.request": 5, "message.delta": 1300}). Populated
	// by both the in-process Runner and the disk-replay
	// ParseTraceFile path so checks that just want "did at least
	// one usage event arrive" can read this without scanning Tools.
	RawTypes map[string]int
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
	// ResultArtifactPath is a workspace-relative path to the complete
	// tool result when the event payload was truncated and the runtime
	// persisted an artifact.
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
	ToolNameCanonicalized      int
	ToolArgsRepaired           int
	ToolRepairCalls            int
	ToolRepairSucceeded        int
	ToolRepairFailed           int
	ToolRepairNotes            int
	ToolRepairByKind           map[string]int
	ToolFailureByKind          map[string]int
	ToolErrors                 int
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
	SessionSearchCalls         int
	SessionSearchResults       int
	SessionSearchContextHits   int
	SessionSearchMatchedTerms  int
	ToolContextTruncated       int
	ToolContextOmittedBytes    int
}

type ToolTruncationStats struct {
	ArgsTruncated       int
	ArgsOmittedBytes    int
	ResultsTruncated    int
	ResultsOmittedBytes int
	ResultArtifacts     int
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
	Refs              []string `json:"refs,omitempty"`
	Previews          []string `json:"previews,omitempty"`
	RequiresRead      bool     `json:"requires_read,omitempty"`
	NotCitable        bool     `json:"not_citable,omitempty"`
	SuggestedNextStep string   `json:"suggested_next_step,omitempty"`
}

type SessionSearchExample struct {
	Scenario        string   `json:"scenario,omitempty"`
	ToolIndex       int      `json:"tool_index"`
	CallID          string   `json:"call_id,omitempty"`
	Query           string   `json:"query,omitempty"`
	Total           int      `json:"total,omitempty"`
	SessionID       string   `json:"session_id,omitempty"`
	TurnIdx         int      `json:"turn_idx,omitempty"`
	MessageIdx      int      `json:"message_idx,omitempty"`
	Role            string   `json:"role,omitempty"`
	Score           float64  `json:"score,omitempty"`
	MatchedTerms    []string `json:"matched_terms,omitempty"`
	ContextIncluded bool     `json:"context_included,omitempty"`
	SnippetPreview  string   `json:"snippet_preview,omitempty"`
	Message         string   `json:"message,omitempty"`
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
	TurnID         string `json:"turn_id,omitempty"`
	DecisionID     string `json:"decision_id,omitempty"`
}

type LoopDecisionStats struct {
	Count      int
	ByKind     map[string]int
	ByDecision map[string]int
	ByMatch    map[string]int
	Examples   []LoopDecision
}

type LoopProtocolFeed struct {
	Scenario              string `json:"scenario,omitempty"`
	TurnID                string `json:"turn_id,omitempty"`
	LoopID                string `json:"loop_id,omitempty"`
	Status                string `json:"status,omitempty"`
	Mode                  string `json:"mode"`
	FeedNumber            int    `json:"feed_number"`
	ProtocolFeeds         int    `json:"protocol_feeds,omitempty"`
	CalibrationAnswers    int    `json:"calibration_answers,omitempty"`
	LastCalibrationAnswer string `json:"last_calibration_answer_preview,omitempty"`
	ProtocolPath          string `json:"protocol_path,omitempty"`
	CurrentSituation      string `json:"current_situation_preview,omitempty"`
	PlanLabel             string `json:"plan_label,omitempty"`
	PlanCurrentStepIndex  int    `json:"plan_current_step_index,omitempty"`
	PlanCurrentStepStatus string `json:"plan_current_step_status,omitempty"`
	PlanCurrentStep       string `json:"plan_current_step,omitempty"`
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

type TraceEventRef struct {
	Index            int    `json:"index"`
	Type             string `json:"type"`
	TurnID           string `json:"turn_id,omitempty"`
	LoopProtocolMode string `json:"loop_protocol_mode,omitempty"`
	LoopProtocolPath string `json:"loop_protocol_path,omitempty"`
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

type ContextCompaction struct {
	Scenario           string `json:"scenario,omitempty"`
	TurnID             string `json:"turn_id,omitempty"`
	BeforeMessages     int    `json:"before_messages"`
	AfterMessages      int    `json:"after_messages"`
	RemovedMessages    int    `json:"removed_messages"`
	Reactive           bool   `json:"reactive"`
	Reason             string `json:"reason"`
	SummaryPresent     bool   `json:"summary_present,omitempty"`
	SummaryBytes       int    `json:"summary_bytes,omitempty"`
	SummaryPreview     string `json:"summary_preview,omitempty"`
	LoopProtocolAnchor string `json:"loop_protocol_anchor,omitempty"`
}

type ContextCompactionStats struct {
	Count           int
	Reactive        int
	Proactive       int
	RemovedMessages int
	SummaryBytes    int
	SummaryMissing  int
	SummaryEmpty    int
	Examples        []ContextCompaction
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
	if len(t.ToolStats.ToolFailureByKind) > 0 {
		return cloneStringIntMap(t.ToolStats.ToolFailureByKind)
	}
	var out map[string]int
	for _, c := range t.Tools {
		kinds := toolFailureKindsForCall(c)
		if len(kinds) == 0 {
			continue
		}
		if out == nil {
			out = map[string]int{}
		}
		for _, kind := range kinds {
			out[kind]++
		}
	}
	return out
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
			ResultPreview: sourceAccessResultPreview(c.Result, c.ResultSummary),
		})
	}
	return out
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
			out = append(out, SessionSearchExample{
				ToolIndex: i + 1,
				CallID:    c.CallID,
				Query:     compactOneLine(resp.Query, 220),
				Total:     resp.Total,
				Message:   compactOneLine(resp.Message, 220),
			})
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
	for i, c := range t.Tools {
		if len(out) >= maxExamples {
			break
		}
		if !c.ArgsTruncated && !c.ResultTruncated && c.ResultArtifactPath == "" && c.ContextOmittedBytes <= 0 {
			continue
		}
		out = append(out, ToolTruncationExample{
			ToolIndex:              i + 1,
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
		})
	}
	return out
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
	var kinds []string
	if c.FailureKind != "" {
		kinds = append(kinds, c.FailureKind)
	}
	for _, kind := range c.FailureKinds {
		if !containsString(kinds, kind) {
			kinds = append(kinds, kind)
		}
	}
	for _, kind := range toolfailure.KindsForResult(c.Tool, c.Result, c.ExitCode != 0) {
		if !containsString(kinds, kind) {
			kinds = append(kinds, kind)
		}
	}
	return kinds
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
			Kind:           decision.Kind,
			Decision:       decision.Decision,
			Trigger:        decision.Trigger,
			Confidence:     decision.Confidence,
			Reason:         compactOneLine(decision.Reason, 260),
			RequiredAction: compactOneLine(decision.RequiredAction, 260),
			TurnID:         decision.TurnID,
			DecisionID:     decision.DecisionID,
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
			TurnID:                feed.TurnID,
			LoopID:                feed.LoopID,
			Status:                feed.Status,
			Mode:                  feed.Mode,
			FeedNumber:            feed.FeedNumber,
			ProtocolFeeds:         feed.ProtocolFeeds,
			CalibrationAnswers:    feed.CalibrationAnswers,
			LastCalibrationAnswer: feed.LastCalibrationAnswer,
			ProtocolPath:          feed.ProtocolPath,
			CurrentSituation:      feed.CurrentSituation,
			PlanLabel:             feed.PlanLabel,
			PlanCurrentStepIndex:  feed.PlanCurrentStepIndex,
			PlanCurrentStepStatus: feed.PlanCurrentStepStatus,
			PlanCurrentStep:       feed.PlanCurrentStep,
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
		if compaction.Reactive {
			stats.Reactive++
		} else {
			stats.Proactive++
		}
		stats.RemovedMessages += compaction.RemovedMessages
		stats.SummaryBytes += compaction.SummaryBytes
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
			TurnID:             compaction.TurnID,
			BeforeMessages:     compaction.BeforeMessages,
			AfterMessages:      compaction.AfterMessages,
			RemovedMessages:    compaction.RemovedMessages,
			Reactive:           compaction.Reactive,
			Reason:             compaction.Reason,
			SummaryPresent:     compaction.SummaryPresent,
			SummaryBytes:       compaction.SummaryBytes,
			SummaryPreview:     compactOneLine(compaction.SummaryPreview, 600),
			LoopProtocolAnchor: compaction.LoopProtocolAnchor,
		})
	}
	return stats
}

func contextCompactionSummaryMissing(compaction ContextCompaction) bool {
	return !compaction.SummaryPresent && compaction.SummaryBytes == 0 && strings.TrimSpace(compaction.SummaryPreview) == ""
}

func contextCompactionSummaryEmpty(compaction ContextCompaction) bool {
	return compaction.SummaryPresent && compaction.SummaryBytes == 0 && strings.TrimSpace(compaction.SummaryPreview) == ""
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
	// FocusedTaskErrors counts run_task calls whose ExitCode != 0.
	// This includes loop-guard rejections (cap-exceeded) and child
	// runtime errors; it does NOT include semantic "ok=false" inside
	// the result JSON because that's a model judgment, not a runtime
	// failure.
	FocusedTaskErrors int
	// SubagentCalls is the total number of subagent_run tool calls.
	SubagentCalls int
	// SubagentByMode breaks the subagent total down by mode
	// (explore / review / test / research). Keys with zero counts
	// are not included.
	SubagentByMode map[string]int
	// SubagentErrors counts subagent_run calls whose ExitCode != 0.
	SubagentErrors int
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
			if c.IsErr || c.ExitCode != 0 {
				s.FocusedTaskErrors++
			}
			if tt := c.Delegation.TaskType; tt != "" {
				if s.FocusedTaskByType == nil {
					s.FocusedTaskByType = map[string]int{}
				}
				s.FocusedTaskByType[tt]++
			}
		case agent.DelegationKindSubagent:
			s.SubagentCalls++
			if c.IsErr || c.ExitCode != 0 {
				s.SubagentErrors++
			}
			if m := c.Delegation.Mode; m != "" {
				if s.SubagentByMode == nil {
					s.SubagentByMode = map[string]int{}
				}
				s.SubagentByMode[m]++
			}
		}
	}
	return s
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
