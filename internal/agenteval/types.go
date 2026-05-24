package agenteval

import (
	"context"

	"github.com/affinefoundation/affent/internal/agent"
	"github.com/affinefoundation/affent/internal/executor"
	"github.com/affinefoundation/affent/internal/sse"
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
}

type ToolRuntimeStats struct {
	ToolRequests           int
	ToolNameCanonicalized  int
	ToolArgsRepaired       int
	ToolRepairCalls        int
	ToolRepairSucceeded    int
	ToolRepairFailed       int
	ToolRepairNotes        int
	ToolRepairByKind       map[string]int
	ToolErrors             int
	ToolDurationMS         int64
	LoopGuardInterventions int
	ForcedNoTools          int
}

type ToolTruncationStats struct {
	ArgsTruncated       int
	ArgsOmittedBytes    int
	ResultsTruncated    int
	ResultsOmittedBytes int
	ResultArtifacts     int
}

// ToolRepairStats classifies runtime tool-call recovery work by the
// human-readable repair notes already emitted on tool.request events.
// A single tool call can contribute to multiple kinds (for example,
// wrapper_unwrap + type_coercion), so Notes can be greater than Calls.
type ToolRepairStats struct {
	Calls          int
	SucceededCalls int
	FailedCalls    int
	Notes          int
	ByKind         map[string]int
}

func (s ToolRepairStats) HasAny() bool {
	return s.Calls > 0 || s.Notes > 0
}

func (t Trace) RepairStats() ToolRepairStats {
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
	// (recall / explore / research / verify / review). Keys with zero
	// counts are not included.
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
