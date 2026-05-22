package agenteval

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/affinefoundation/affent/internal/agent"
	"github.com/affinefoundation/affent/internal/executor"
	"github.com/rs/zerolog"
)

// scriptedLLM serves OpenAI-compat /chat/completions with a queue of
// canned SSE response bodies. One request consumes one entry; running
// past the script returns a generic finish_reason=stop body so the
// test doesn't hang on an unscripted call.
type scriptedLLM struct {
	t      *testing.T
	script [][]string // each entry is a list of `data: <line>` payloads
	calls  atomic.Int32
}

func newScriptedLLM(t *testing.T, script [][]string) *httptest.Server {
	t.Helper()
	lm := &scriptedLLM{t: t, script: script}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		idx := int(lm.calls.Add(1)) - 1
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		var payloads []string
		if idx < len(lm.script) {
			payloads = lm.script[idx]
		} else {
			// Past the script: emit a generic completion so the loop
			// terminates instead of hanging if the agent issues an
			// unexpected extra call.
			payloads = []string{
				`{"choices":[{"delta":{"role":"assistant","content":"unscripted completion"},"finish_reason":"stop"}]}`,
				`[DONE]`,
			}
		}
		for _, p := range payloads {
			_, _ = w.Write([]byte("data: " + p + "\n\n"))
			flusher.Flush()
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestRunner_EndToEnd_OneToolCall pins the full Runner pipeline on a
// minimal scenario: the LLM requests one read_file, the runtime
// returns the file contents, the LLM produces a final text answer.
// Asserts that the captured Trace has the tool call, its result, the
// final text, the turn-end reason, AND that the Scenario's Checks fire
// against that Trace correctly — exactly the integration loop a real
// eval depends on.
func TestRunner_EndToEnd_OneToolCall(t *testing.T) {
	// Turn 1: ask for read_file via tool_calls.
	turn1 := []string{
		`{"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"read_file","arguments":""}}]},"finish_reason":null}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"path\":\"README.md\"}"}}]},"finish_reason":null}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`[DONE]`,
	}
	// Turn 2: respond with a final text answer referencing the file.
	turn2 := []string{
		`{"choices":[{"delta":{"role":"assistant","content":"The README says hello agent."},"finish_reason":"stop"}]}`,
		`[DONE]`,
	}
	srv := newScriptedLLM(t, [][]string{turn1, turn2})

	scenario := Scenario{
		Name:        "readme_smoke",
		Description: "agent reads README.md and answers from its contents",
		Prompt:      "what does README.md say?",
		Setup: func(workspaceDir string) error {
			return os.WriteFile(filepath.Join(workspaceDir, "README.md"), []byte("hello agent"), 0o644)
		},
		MaxTurnSteps: 4,
		Checks: []Check{
			TurnEndedCleanly(),
			ToolCalled("read_file", func(args map[string]any) bool {
				p, _ := args["path"].(string)
				return p == "README.md"
			}),
			ToolNotCalled("write_file", nil),
			FinalTextContains("hello agent"),
		},
	}

	runner := &Runner{
		LLM:            agent.NewLLMClient(srv.URL, "", "fake-model"),
		MaxTurnSteps:   4,
		PerCallTimeout: 5 * time.Second,
		RunTimeout:     20 * time.Second,
		Log:            zerolog.Nop(),
	}

	out, err := runner.Run(context.Background(), scenario)
	if err != nil {
		t.Fatalf("Runner.Run: %v", err)
	}
	if !out.Pass {
		t.Errorf("expected all checks to pass; failed: %v", out.FailedChecks())
	}
	if out.Trace.TurnEndReason != "completed" {
		t.Errorf("TurnEndReason = %q, want completed", out.Trace.TurnEndReason)
	}
	if len(out.Trace.Tools) != 1 || out.Trace.Tools[0].Tool != "read_file" {
		t.Fatalf("expected exactly one read_file tool call; got %+v", out.Trace.Tools)
	}
	if !strings.Contains(out.Trace.Tools[0].Result, "hello agent") {
		t.Errorf("tool result should contain file contents; got %q", out.Trace.Tools[0].Result)
	}
	if out.Trace.Tools[0].ExitCode != 0 || out.Trace.Tools[0].IsErr {
		t.Errorf("read_file should report success; got exit=%d isErr=%v", out.Trace.Tools[0].ExitCode, out.Trace.Tools[0].IsErr)
	}
	if !strings.Contains(out.Trace.FinalText, "hello agent") {
		t.Errorf("FinalText should reference file content; got %q", out.Trace.FinalText)
	}
	if got := out.Trace.RawTypes["tool.request"]; got != 1 {
		t.Errorf("RawTypes[tool.request] = %d, want 1", got)
	}
}

// TestRunner_EndToEnd_NoToolPath pins the path where the LLM answers
// without any tool call. Tools timeline must be empty; FinalText must
// be set; the smoke-level TurnEndedCleanly check still passes.
func TestRunner_EndToEnd_NoToolPath(t *testing.T) {
	turn1 := []string{
		`{"choices":[{"delta":{"role":"assistant","content":"42 because the answer was already in my head."},"finish_reason":"stop"}]}`,
		`[DONE]`,
	}
	srv := newScriptedLLM(t, [][]string{turn1})

	scenario := Scenario{
		Name:   "no_tool_path",
		Prompt: "what's 6 * 7?",
		Checks: []Check{
			TurnEndedCleanly(),
			MaxToolCalls(0),
			FinalTextContains("42"),
		},
	}

	runner := &Runner{
		LLM:            agent.NewLLMClient(srv.URL, "", "fake-model"),
		PerCallTimeout: 5 * time.Second,
		RunTimeout:     10 * time.Second,
		Log:            zerolog.Nop(),
	}

	out, err := runner.Run(context.Background(), scenario)
	if err != nil {
		t.Fatalf("Runner.Run: %v", err)
	}
	if !out.Pass {
		t.Errorf("expected all checks to pass; failed: %v", out.FailedChecks())
	}
	if len(out.Trace.Tools) != 0 {
		t.Errorf("no-tool path should produce empty Tools slice; got %v", out.Trace.Tools)
	}
}

// TestRunner_EndToEnd_FailedChecksDoNotFailRun pins the responsibility
// split: Run returns nil error even when scenario checks fail — only
// runtime errors (LLM transport, setup, registry build) bubble up as
// the second return value. Callers iterate Outcome.Results to find
// quality failures, NOT err.
func TestRunner_EndToEnd_FailedChecksDoNotFailRun(t *testing.T) {
	turn1 := []string{
		`{"choices":[{"delta":{"role":"assistant","content":"nothing to see here"},"finish_reason":"stop"}]}`,
		`[DONE]`,
	}
	srv := newScriptedLLM(t, [][]string{turn1})

	scenario := Scenario{
		Name:   "intentional_check_failure",
		Prompt: "say something",
		Checks: []Check{
			FinalTextContains("THIS WILL NOT APPEAR"),
		},
	}

	runner := &Runner{
		LLM:            agent.NewLLMClient(srv.URL, "", "fake-model"),
		PerCallTimeout: 5 * time.Second,
		RunTimeout:     10 * time.Second,
		Log:            zerolog.Nop(),
	}

	out, err := runner.Run(context.Background(), scenario)
	if err != nil {
		t.Fatalf("Runner.Run should not error on check failure; got %v", err)
	}
	if out.Pass {
		t.Errorf("scenario should not pass when a Check fails")
	}
	if len(out.FailedChecks()) != 1 {
		t.Errorf("expected exactly one failed check; got %v", out.FailedChecks())
	}
}

// TestRunner_EndToEnd_SubagentDelegation drives the full delegation
// loop end-to-end with a scripted LLM:
//
//  1. Parent calls subagent_run with a task
//  2. Child (same scripted LLM, sequentially advanced) calls
//     read_file inside its isolated context
//  3. Child returns a structured report
//  4. Parent answers from the report without re-reading the file
//
// Pins the user-named subagent design contract — the parent's Trace
// must contain subagent_run and NOTHING else exploration-shaped
// after it, even though the user asked a question whose answer is
// on disk. ToolNotCalledAfter is the load-bearing check that
// captures this.
func TestRunner_EndToEnd_SubagentDelegation(t *testing.T) {
	// Parent turn 1: ask for subagent_run.
	parentTurn1 := []string{
		`{"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"p1","type":"function","function":{"name":"subagent_run","arguments":""}}]},"finish_reason":null}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"task\":\"read README.md and tell me what it says\",\"mode\":\"explore\"}"}}]},"finish_reason":null}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`[DONE]`,
	}
	// Child turn 1: read_file.
	childTurn1 := []string{
		`{"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"c1","type":"function","function":{"name":"read_file","arguments":""}}]},"finish_reason":null}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"path\":\"README.md\"}"}}]},"finish_reason":null}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`[DONE]`,
	}
	// Child turn 2: emit structured report and stop.
	childTurn2 := []string{
		`{"choices":[{"delta":{"role":"assistant","content":"Conclusion:\nREADME announces the project.\nEvidence:\n- README.md contains: hello agent"},"finish_reason":"stop"}]}`,
		`[DONE]`,
	}
	// Parent turn 2: synthesize final answer from the subagent report
	// WITHOUT calling any parent-side exploration tools.
	parentTurn2 := []string{
		`{"choices":[{"delta":{"role":"assistant","content":"Based on the subagent's report, the README says: hello agent."},"finish_reason":"stop"}]}`,
		`[DONE]`,
	}
	srv := newScriptedLLM(t, [][]string{parentTurn1, childTurn1, childTurn2, parentTurn2})

	scenario := Scenario{
		Name:        "subagent_delegation_readme",
		Description: "parent delegates a small read task to subagent_run and answers from its report",
		Prompt:      "what does README.md say? please use a subagent to investigate",
		Setup: func(workspaceDir string) error {
			return os.WriteFile(filepath.Join(workspaceDir, "README.md"), []byte("hello agent"), 0o644)
		},
		MaxTurnSteps: 6,
		Checks: []Check{
			TurnEndedCleanly(),
			ToolCalled("subagent_run", nil),
			// The load-bearing pollution-reduction assertion: after the
			// subagent returns a successful report, the parent must NOT
			// re-do the same exploration in its own context.
			ToolNotCalledAfter("subagent_run", []string{
				"read_file", "list_files", "shell", "edit_file", "write_file",
			}),
			MaxToolCallsAfter("subagent_run", 0),
			FinalTextContains("hello agent"),
		},
	}

	llmClient := agent.NewLLMClient(srv.URL, "", "fake-model")
	runner := &Runner{
		LLM:            llmClient,
		MaxTurnSteps:   6,
		PerCallTimeout: 5 * time.Second,
		RunTimeout:     30 * time.Second,
		Log:            zerolog.Nop(),
		// Custom registry that also wires the subagent tool. Reuses
		// the default builtins under the hood so the child's
		// read_file works through the same LocalExecutor.
		BuildRegistry: func(ctx context.Context, workspaceDir string, exec executor.Executor) (*agent.Registry, error) {
			reg, err := defaultBuildRegistry(ctx, workspaceDir, exec)
			if err != nil {
				return nil, err
			}
			agent.RegisterSubagent(reg, agent.SubagentDeps{
				LLM:              llmClient,
				Executor:         exec,
				HostWorkspaceDir: workspaceDir,
				Log:              zerolog.Nop(),
				PerCallTimeout:   5 * time.Second,
			})
			return reg, nil
		},
	}

	out, err := runner.Run(context.Background(), scenario)
	if err != nil {
		t.Fatalf("Runner.Run: %v", err)
	}
	if !out.Pass {
		t.Errorf("expected all checks to pass; failed: %v", out.FailedChecks())
		for _, r := range out.Results {
			t.Logf("  %s: pass=%v detail=%s", r.Check, r.Pass, r.Detail)
		}
		t.Logf("trace.Tools count=%d:", len(out.Trace.Tools))
		for i, c := range out.Trace.Tools {
			t.Logf("    [%d] %s exit=%d", i, c.Tool, c.ExitCode)
		}
	}
	// Independent assertions on the captured trace for diagnostic value
	// when the test does fail.
	if len(out.Trace.Tools) != 1 {
		t.Errorf("parent should have made exactly ONE tool call (subagent_run); got %d", len(out.Trace.Tools))
	}
	if len(out.Trace.Tools) > 0 && out.Trace.Tools[0].Tool != "subagent_run" {
		t.Errorf("first parent tool call should be subagent_run; got %q", out.Trace.Tools[0].Tool)
	}
}

// TestRunner_RequiresLLM pins the early-validation: Runner without an
// LLM client returns a clear error instead of nil-deref'ing inside
// EnsureSystemPrompt / SendUser.
func TestRunner_RequiresLLM(t *testing.T) {
	runner := &Runner{}
	_, err := runner.Run(context.Background(), Scenario{Name: "x"})
	if err == nil {
		t.Fatal("expected error when LLM is nil")
	}
	if !strings.Contains(err.Error(), "LLM is required") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestRunner_RequiresScenarioName pins the second early-validation:
// an unnamed Scenario is operator error and should fail before we
// build a workspace / mint an executor.
func TestRunner_RequiresScenarioName(t *testing.T) {
	runner := &Runner{LLM: agent.NewLLMClient("http://x", "", "m")}
	_, err := runner.Run(context.Background(), Scenario{})
	if err == nil {
		t.Fatal("expected error when Scenario.Name is empty")
	}
	if !strings.Contains(err.Error(), "Scenario.Name is required") {
		t.Errorf("unexpected error: %v", err)
	}
}
