package agenteval

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

	// SkillProvider optionally injects task-relevant skill blocks into
	// the eval conversation. Nil uses the default runtime skill provider
	// when BuildRegistry is also nil; custom registries opt in by setting
	// this field explicitly.
	SkillProvider agent.SkillProvider

	// MaxTurnSteps caps assistant<->tool round trips. Scenario value
	// overrides; zero here falls back to 8.
	MaxTurnSteps int

	// CompactTriggerMsgs controls the rolling-summary message trigger.
	// Zero falls back to agent.DefaultSummaryTriggerMsgs.
	CompactTriggerMsgs int

	// CompactTriggerInputTokens controls proactive request-input pressure
	// compaction. Zero keeps the runtime-derived default, positive sets an
	// explicit threshold, and negative disables the request-pressure path.
	CompactTriggerInputTokens int

	// CompactKeepLast controls how many tail messages survive compaction.
	// Zero falls back to agent.DefaultSummaryKeepLast.
	CompactKeepLast int

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

	convPath := filepath.Join(workspace, ".agenteval-conv.jsonl")
	conv, err := agent.OpenConversationAt(convPath)
	if err != nil {
		return Outcome{}, fmt.Errorf("conversation: %w", err)
	}

	var reg *agent.Registry
	var skillProvider agent.SkillProvider
	if r.BuildRegistry == nil {
		rt, err := defaultBuildRuntime(workspace, exec, conv)
		if err != nil {
			return Outcome{}, fmt.Errorf("build registry: %w", err)
		}
		reg = rt.Registry
		skillProvider = rt.SkillProvider
	} else {
		var err error
		reg, err = r.BuildRegistry(ctx, workspace, exec)
		if err != nil {
			return Outcome{}, fmt.Errorf("build registry: %w", err)
		}
	}
	if r.SkillProvider != nil {
		skillProvider = r.SkillProvider
	}

	events := make(chan sse.Event, 256)
	maxTurns := s.MaxTurnSteps
	if maxTurns <= 0 {
		maxTurns = r.MaxTurnSteps
	}
	if maxTurns <= 0 {
		maxTurns = DefaultRunnerMaxTurnSteps
	}
	compactTriggerMsgs := r.CompactTriggerMsgs
	if compactTriggerMsgs <= 0 {
		compactTriggerMsgs = agent.DefaultSummaryTriggerMsgs
	}
	compactKeepLast := r.CompactKeepLast
	if compactKeepLast <= 0 {
		compactKeepLast = agent.DefaultSummaryKeepLast
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
		WorkspaceRoot:  workspace,
		Compactor: &agent.LLMSummaryCompactor{
			LLM:          r.LLM,
			TriggerMsgs:  compactTriggerMsgs,
			TriggerBytes: agent.DefaultSummaryTriggerBytes,
			KeepLast:     compactKeepLast,
		},
		CompactTriggerInputTokens: r.CompactTriggerInputTokens,
		ToolResultArtifactDir: filepath.Join(
			workspace,
			".affent",
			"artifacts",
			"tool-results",
		),
		ToolResultArtifactPathPrefix: ".affent/artifacts/tool-results",
		SkillProvider:                skillProvider,
	}
	systemPrompt := agent.BaseSystemPromptForRegistry(reg)
	systemPrompt = agent.WithRegistrySystemGuidance(systemPrompt, reg)
	if err := loop.EnsureSystemPrompt(systemPrompt); err != nil {
		return Outcome{}, fmt.Errorf("system prompt: %w", err)
	}
	prompts := runnerScenarioPrompts(s)
	trace := newRunnerTrace(s, workspace, runnerPromptDisplay(prompts))
	for _, prompt := range prompts {
		turnID, err := loop.SendUser(ctx, prompt)
		if err != nil {
			return Outcome{}, fmt.Errorf("send user: %w", err)
		}
		drainTraceInto(ctx, events, turnID, &trace)
	}

	trace.ChildTranscripts = collectDebugChildTranscripts(workspace, maxDebugChildTranscriptRefs)
	trace.TaskState = DeriveTaskState(trace)
	results := evaluateChecks(trace, s.Checks)
	return Outcome{
		Scenario: s.Name,
		Trace:    trace,
		Results:  results,
		Pass:     allPass(results),
	}, nil
}

func runnerScenarioPrompts(s Scenario) []string {
	if len(s.Prompts) > 0 {
		return append([]string(nil), s.Prompts...)
	}
	return []string{s.Prompt}
}

func runnerPromptDisplay(prompts []string) string {
	if len(prompts) == 0 {
		return ""
	}
	if len(prompts) == 1 {
		return prompts[0]
	}
	var out strings.Builder
	for i, prompt := range prompts {
		if i > 0 {
			out.WriteString("\n\n")
		}
		fmt.Fprintf(&out, "Turn %d:\n%s", i+1, prompt)
	}
	return out.String()
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

type runnerRuntime struct {
	Registry      *agent.Registry
	SkillProvider agent.SkillProvider
}

func defaultBuildRuntime(workspaceDir string, exec executor.Executor, conv *agent.Conversation) (runnerRuntime, error) {
	reg := agent.NewRegistry()
	skillDir := agent.DefaultWorkspaceSkillDir(workspaceDir)
	skillReg, err := agent.RuntimeSkillRegistry(skillDir)
	if err != nil {
		return runnerRuntime{}, err
	}
	agent.RegisterBuiltins(reg, agent.BuiltinDeps{
		Executor:         exec,
		HostWorkspaceDir: workspaceDir,
		PlanPath:         filepath.Join(workspaceDir, ".affent", "plan.json"),
		SkillRegistry:    skillReg,
		SkillDir:         skillDir,
		SkillInstallConfirmer: func(proposalID string) bool {
			return agent.UserConfirmedRuntimeSkillProposal(conv, proposalID)
		},
	})
	registerEvalSessionScheduleTool(reg, workspaceDir)
	return runnerRuntime{Registry: reg, SkillProvider: agent.SkillProviderForTools(skillReg, reg)}, nil
}

func defaultBuildRegistry(_ context.Context, workspaceDir string, exec executor.Executor) (*agent.Registry, error) {
	rt, err := defaultBuildRuntime(workspaceDir, exec, nil)
	if err != nil {
		return nil, err
	}
	return rt.Registry, nil
}

// drainTrace consumes Loop events until turn.end for turnID arrives
// (or the event channel closes / ctx fires) and synthesizes the
// frozen Trace. Pairs tool.request with its later tool.result by
// CallID; out-of-order pairs work the same way the subagent's drain
// handles them.
func newRunnerTrace(s Scenario, workspaceDir, prompt string) Trace {
	return Trace{
		SchemaVersion: sse.TraceSchemaVersion,
		Scenario:      s.Name,
		WorkspaceDir:  workspaceDir,
		Prompt:        prompt,
		RawTypes:      map[string]int{},
	}
}

func drainTrace(ctx context.Context, events <-chan sse.Event, turnID string, s Scenario, workspaceDir string) Trace {
	t := newRunnerTrace(s, workspaceDir, runnerPromptDisplay(runnerScenarioPrompts(s)))
	drainTraceInto(ctx, events, turnID, &t)
	return t
}

func drainTraceInto(ctx context.Context, events <-chan sse.Event, turnID string, t *Trace) {
	pending := map[string]int{}
	for {
		select {
		case <-ctx.Done():
			t.TurnEndReason = "cancelled"
			return
		case ev, ok := <-events:
			if !ok {
				return
			}
			t.RawTypes[ev.Type]++
			done, err := applyTraceEvent(t, pending, ev.Type, ev.Data, turnID)
			if err != nil {
				t.LoopErrors = append(t.LoopErrors, err.Error())
			} else {
				appendTraceEventRef(t, ev.Type, ev.Data, turnID)
			}
			if done {
				return
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
