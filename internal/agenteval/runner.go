package agenteval

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/affinefoundation/affent/internal/agent"
	"github.com/affinefoundation/affent/internal/executor"
	"github.com/affinefoundation/affent/internal/sse"
	"github.com/rs/zerolog"
)

const DefaultRunnerMaxTurnSteps = 8

// Runner drives one Scenario through a fresh agent.Loop and captures
// the result as a Trace. It is deliberately not reusable across
// concurrent scenarios — make a new Runner per goroutine. The Loop,
// workspace, and event channel are all per-run state owned by Run.
type Runner struct {
	// LLM is the upstream client. Production eval points at a real
	// model; unit tests point at an httptest.Server returning canned
	// SSE chunks. Required.
	LLM *agent.LLMClient

	// BuildExecutor mints the Executor the scenario will use. Nil
	// falls back to a LocalExecutor scoped to the per-run workspace,
	// with the standard PATH augmentation.
	BuildExecutor RunnerExecutorBuilder

	// BuildRegistry mints the tool Registry. Nil falls back to
	// agent.RegisterBuiltins on the executor. Pass a custom builder
	// to register browser/subagent/MCP tools for richer scenarios.
	BuildRegistry RunnerRegistryBuilder

	// MaxTurnSteps caps assistant<->tool round trips. Scenario value
	// overrides; zero here falls back to 8.
	MaxTurnSteps int

	// PerCallTimeout overrides the Loop default for each LLM call.
	// Zero leaves the Loop default.
	PerCallTimeout time.Duration

	// RunTimeout caps the whole Run. Zero means no Runner-level
	// timeout; ctx.Done remains the kill signal. Use a positive value
	// to bound a scenario that might otherwise sit forever waiting on
	// a hung canned LLM.
	RunTimeout time.Duration

	// Log is the structured log target. Zero-value (zerolog.Nop) is
	// fine — Run does not depend on log being writable.
	Log zerolog.Logger
}

// Run executes the scenario end-to-end and returns the Outcome.
// Run cleans up the per-run workspace before returning unless the
// scenario's Setup explicitly handed back a non-temp directory (it
// can't today — the v0 framework owns the workspace lifecycle).
//
// Run never panics on a check failure. A failing check produces a
// CheckResult with Pass=false; only runtime errors (LLM transport,
// executor build, system prompt write) return non-nil error from Run.
// This split lets callers iterate over Outcome.Results without
// having to also handle error from each check.
func (r *Runner) Run(ctx context.Context, s Scenario) (Outcome, error) {
	if r.LLM == nil {
		return Outcome{}, errors.New("agenteval: Runner.LLM is required")
	}
	if s.Name == "" {
		return Outcome{}, errors.New("agenteval: Scenario.Name is required")
	}
	if r.RunTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, r.RunTimeout)
		defer cancel()
	}

	workspace, cleanup, err := makeWorkspace(s.Name)
	if err != nil {
		return Outcome{}, fmt.Errorf("workspace: %w", err)
	}
	defer cleanup()
	if s.Setup != nil {
		if err := s.Setup(workspace); err != nil {
			return Outcome{}, fmt.Errorf("scenario setup: %w", err)
		}
	}

	buildExec := r.BuildExecutor
	if buildExec == nil {
		buildExec = defaultBuildExecutor(s.Name)
	}
	exec, err := buildExec(workspace)
	if err != nil {
		return Outcome{}, fmt.Errorf("build executor: %w", err)
	}

	buildReg := r.BuildRegistry
	if buildReg == nil {
		buildReg = defaultBuildRegistry
	}
	reg, err := buildReg(ctx, workspace, exec)
	if err != nil {
		return Outcome{}, fmt.Errorf("build registry: %w", err)
	}

	convPath := filepath.Join(workspace, ".agenteval-conv.jsonl")
	conv, err := agent.OpenConversationAt(convPath)
	if err != nil {
		return Outcome{}, fmt.Errorf("conversation: %w", err)
	}

	events := make(chan sse.Event, 256)
	maxTurns := s.MaxTurnSteps
	if maxTurns <= 0 {
		maxTurns = r.MaxTurnSteps
	}
	if maxTurns <= 0 {
		maxTurns = DefaultRunnerMaxTurnSteps
	}
	loop := &agent.Loop{
		LLM:            r.LLM,
		Tools:          reg,
		Conv:           conv,
		Events:         events,
		Log:            r.Log.With().Str("component", "agenteval").Str("scenario", s.Name).Logger(),
		MaxTurnSteps:   maxTurns,
		MaxToolCalls:   maxTurns,
		PerCallTimeout: r.PerCallTimeout,
		ToolResultArtifactDir: filepath.Join(
			workspace,
			".affent",
			"artifacts",
			"tool-results",
		),
		ToolResultArtifactPathPrefix: ".affent/artifacts/tool-results",
	}
	if err := loop.EnsureSystemPrompt(""); err != nil {
		return Outcome{}, fmt.Errorf("system prompt: %w", err)
	}
	turnID, err := loop.SendUser(ctx, s.Prompt)
	if err != nil {
		return Outcome{}, fmt.Errorf("send user: %w", err)
	}

	trace := drainTrace(ctx, events, turnID, s, workspace)
	results := evaluateChecks(trace, s.Checks)
	return Outcome{
		Scenario: s.Name,
		Trace:    trace,
		Results:  results,
		Pass:     allPass(results),
	}, nil
}

func makeWorkspace(scenarioName string) (string, func(), error) {
	dir, err := os.MkdirTemp("", "agenteval-"+scenarioName+"-*")
	if err != nil {
		return "", func() {}, err
	}
	return dir, func() { _ = os.RemoveAll(dir) }, nil
}

func defaultBuildExecutor(scenarioName string) RunnerExecutorBuilder {
	return func(workspaceDir string) (executor.Executor, error) {
		return executor.NewLocalExecutor("agenteval-"+scenarioName, workspaceDir), nil
	}
}

func defaultBuildRegistry(_ context.Context, workspaceDir string, exec executor.Executor) (*agent.Registry, error) {
	reg := agent.NewRegistry()
	skillDir := agent.DefaultWorkspaceSkillDir(workspaceDir)
	skillReg, err := agent.RuntimeSkillRegistry(skillDir)
	if err != nil {
		return nil, err
	}
	agent.RegisterBuiltins(reg, agent.BuiltinDeps{
		Executor:         exec,
		HostWorkspaceDir: workspaceDir,
		SkillRegistry:    skillReg,
		SkillDir:         skillDir,
	})
	return reg, nil
}

// drainTrace consumes Loop events until turn.end for turnID arrives
// (or the event channel closes / ctx fires) and synthesizes the
// frozen Trace. Pairs tool.request with its later tool.result by
// CallID; out-of-order pairs work the same way the subagent's drain
// handles them.
func drainTrace(ctx context.Context, events <-chan sse.Event, turnID string, s Scenario, workspaceDir string) Trace {
	t := Trace{
		SchemaVersion: sse.TraceSchemaVersion,
		Scenario:      s.Name,
		WorkspaceDir:  workspaceDir,
		Prompt:        s.Prompt,
		RawTypes:      map[string]int{},
	}
	pending := map[string]int{}
	for {
		select {
		case <-ctx.Done():
			t.TurnEndReason = "cancelled"
			return t
		case ev, ok := <-events:
			if !ok {
				return t
			}
			t.RawTypes[ev.Type]++
			done, err := applyTraceEvent(&t, pending, ev.Type, ev.Data, turnID)
			if err != nil {
				t.LoopErrors = append(t.LoopErrors, err.Error())
			}
			if done {
				return t
			}
		}
	}
}

func evaluateChecks(trace Trace, checks []Check) []CheckResult {
	out := make([]CheckResult, 0, len(checks))
	for _, c := range checks {
		if c.Eval == nil {
			out = append(out, CheckResult{
				Check:  c.Name,
				Pass:   false,
				Detail: "check has no Eval function",
			})
			continue
		}
		res := c.Eval(trace)
		// Ensure CheckResult always carries the Check name even if a
		// custom Eval forgot to populate it — keeps reports useful.
		if res.Check == "" {
			res.Check = c.Name
		}
		out = append(out, res)
	}
	return out
}

func allPass(results []CheckResult) bool {
	if len(results) == 0 {
		// A scenario with zero checks is a smoke-test only — it
		// asserts "the agent reached turn.end without erroring".
		// Treat as pass unless we eventually want strict mode.
		return true
	}
	for _, r := range results {
		if !r.Pass {
			return false
		}
	}
	return true
}
