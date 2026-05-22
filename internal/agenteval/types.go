package agenteval

import (
	"context"

	"github.com/affinefoundation/affent/internal/agent"
	"github.com/affinefoundation/affent/internal/executor"
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

// Check is one named binary assertion over a Trace. Checks must be
// pure functions of the Trace — no I/O, no time-dependent behavior
// — so reruns of the same Trace produce the same CheckResult.
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
	// Scenario is the Scenario.Name that produced this Trace.
	Scenario string

	// WorkspaceDir is the per-run workspace path (cleaned up after
	// Run returns). Checks that want to inspect on-disk state must
	// read it BEFORE the Runner cleans up — meaning checks today are
	// trace-only. A future FilePostCheck phase can change this.
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
}

// ToolCall is one tool invocation by the agent, with its result.
// ToolCall is what Checks like ToolCalled / ToolCalledBefore inspect
// — so the field shape matters for the check library.
type ToolCall struct {
	// CallID is the upstream tool_call id. Lets a Check correlate a
	// specific request with its result when the agent issued
	// duplicates (e.g. retried after a transient error).
	CallID string
	// Tool is the tool name (e.g. "read_file", "shell", "edit_file").
	Tool string
	// Args is the JSON-decoded argument object the LLM sent.
	Args map[string]any
	// Result is the full tool output. Truncated only when the tool
	// itself truncates; the framework does not clip.
	Result string
	// ExitCode is the tool's reported exit code. -1 marks abnormal
	// exits (timeout, killed). Non-zero is a failure even if the
	// tool returned without a Go error.
	ExitCode int
	// IsErr is true when the tool returned a Go error (vs returning
	// a non-zero exit code via a successful execution).
	IsErr bool
}

// Usage aggregates per-turn token accounting summed across every LLM
// call in the run.
type Usage struct {
	InputTokens  int
	OutputTokens int
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
