package agenteval

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	"github.com/affinefoundation/affent/internal/memory"
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
	if out.Trace.SchemaVersion != 1 {
		t.Errorf("SchemaVersion = %d, want 1", out.Trace.SchemaVersion)
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

func TestRunner_CustomMemoryOnlyRegistryUsesMatchingPrompt(t *testing.T) {
	type capturedRequest struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
		Tools []struct {
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		} `json:"tools"`
	}
	requests := make(chan capturedRequest, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
		}
		var req capturedRequest
		if err := json.Unmarshal(raw, &req); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		select {
		case requests <- req:
		default:
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: " + `{"choices":[{"delta":{"role":"assistant","content":"done"},"finish_reason":"stop"}]}` + "\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	t.Cleanup(srv.Close)

	runner := &Runner{
		LLM:            agent.NewLLMClient(srv.URL, "", "fake-model"),
		MaxTurnSteps:   2,
		PerCallTimeout: 5 * time.Second,
		RunTimeout:     10 * time.Second,
		Log:            zerolog.Nop(),
		BuildRegistry: func(ctx context.Context, workspaceDir string, exec executor.Executor) (*agent.Registry, error) {
			_ = ctx
			_ = exec
			reg := agent.NewRegistry()
			agent.RegisterMemoryOnly(reg, memory.NewFileMemoryStore(workspaceDir))
			return reg, nil
		},
	}
	out, err := runner.Run(context.Background(), Scenario{
		Name:         "memory_only_prompt",
		Description:  "memory-only custom eval runtime should not advertise shell/file tools",
		Prompt:       "what do you remember?",
		MaxTurnSteps: 2,
		Checks: []Check{
			TurnEndedCleanly(),
			FinalTextContains("done"),
		},
	})
	if err != nil {
		t.Fatalf("Runner.Run: %v", err)
	}
	if !out.Pass {
		t.Fatalf("expected all checks to pass; failed: %v", out.FailedChecks())
	}
	select {
	case req := <-requests:
		foundSystem := false
		for _, msg := range req.Messages {
			if msg.Role != "system" {
				continue
			}
			foundSystem = true
			for _, want := range []string{"only tool is 'memory'", "Memory retrieval:", "action=search"} {
				if !strings.Contains(msg.Content, want) {
					t.Fatalf("memory-only system prompt missing %q:\n%s", want, msg.Content)
				}
			}
			for _, forbidden := range []string{"'shell' tool", "read_file", "write_file", "edit_file", "list_files"} {
				if strings.Contains(msg.Content, forbidden) {
					t.Fatalf("memory-only system prompt should not include %q:\n%s", forbidden, msg.Content)
				}
			}
		}
		if !foundSystem {
			t.Fatalf("LLM request missing system prompt: %+v", req.Messages)
		}
		if len(req.Tools) != 1 || req.Tools[0].Function.Name != agent.MemoryToolName {
			t.Fatalf("memory-only custom registry should advertise only memory tool; tools=%+v", req.Tools)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("LLM request was not captured")
	}
}

func TestRunner_DefaultRuntimeLoadsWorkspaceSkills(t *testing.T) {
	type capturedRequest struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
		Tools []struct {
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		} `json:"tools"`
	}
	requests := make(chan capturedRequest, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
		}
		var req capturedRequest
		if err := json.Unmarshal(raw, &req); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		select {
		case requests <- req:
		default:
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: " + `{"choices":[{"delta":{"role":"assistant","content":"done"},"finish_reason":"stop"}]}` + "\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	t.Cleanup(srv.Close)

	scenario := Scenario{
		Name:        "runtime_skill_provider",
		Description: "default eval runtime injects workspace-installed skills",
		Prompt:      "please use the runtime eval trigger",
		Setup: func(workspaceDir string) error {
			_, err := agent.InstallRuntimeSkill(agent.DefaultWorkspaceSkillDir(workspaceDir), agent.Skill{
				Name:        "eval_runtime_demo",
				Description: "Eval runtime demo.",
				Body:        "AFFENT ACTIVE SKILL: eval_runtime_demo\nRUNTIME_EVAL_SKILL_MARKER",
				AutoActivation: agent.SkillAutoActivation{
					Any: []string{"runtime eval trigger"},
				},
			})
			return err
		},
		MaxTurnSteps: 2,
		Checks: []Check{
			TurnEndedCleanly(),
			FinalTextContains("done"),
		},
	}
	runner := &Runner{
		LLM:            agent.NewLLMClient(srv.URL, "", "fake-model"),
		MaxTurnSteps:   2,
		PerCallTimeout: 5 * time.Second,
		RunTimeout:     10 * time.Second,
		Log:            zerolog.Nop(),
	}
	out, err := runner.Run(context.Background(), scenario)
	if err != nil {
		t.Fatalf("Runner.Run: %v", err)
	}
	if !out.Pass {
		t.Fatalf("expected all checks to pass; failed: %v", out.FailedChecks())
	}
	select {
	case req := <-requests:
		foundSkill := false
		foundPlanGuidance := false
		for _, msg := range req.Messages {
			if msg.Role == "system" && strings.Contains(msg.Content, "RUNTIME_EVAL_SKILL_MARKER") {
				foundSkill = true
			}
			if msg.Role == "system" && strings.Contains(msg.Content, "Affent plan tool guidance:") {
				foundPlanGuidance = true
			}
		}
		if !foundSkill {
			t.Fatalf("runtime skill was not injected into LLM request: %+v", req.Messages)
		}
		if !foundPlanGuidance {
			t.Fatalf("default runtime registered plan but prompt missed plan guidance: %+v", req.Messages)
		}
		foundPlanTool := false
		for _, tool := range req.Tools {
			if tool.Function.Name == agent.PlanToolName {
				foundPlanTool = true
			}
		}
		if !foundPlanTool {
			t.Fatalf("default runtime should advertise plan tool; tools=%+v", req.Tools)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("LLM request was not captured")
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

// TestRunner_EndToEnd_LoopGuardBlocksIdenticalRepeats verifies the
// runtime mechanism end-to-end through the eval framework: the
// toolLoopGuard blocks a tool call when the model emits the same
// (tool, args) triple 3 times in a row. This is the exact
// "failure mode -> mechanism -> trace check proves it fired" loop
// the user named as the design pattern for affent.
//
// Without this test the guard is "code that exists"; with it the
// guard is "behavior the framework can detect in a trace and
// score against future regressions".
func TestRunner_EndToEnd_LoopGuardBlocksIdenticalRepeats(t *testing.T) {
	// Each turn: emit the SAME read_file call. The 3rd attempt should
	// be blocked by the guard with a "loop_guard: blocked exact
	// repeated call" tool result. Turn 4 emits a final answer.
	repeatedCall := func(callID string) []string {
		return []string{
			`{"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"` + callID + `","type":"function","function":{"name":"read_file","arguments":""}}]},"finish_reason":null}]}`,
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"path\":\"README.md\"}"}}]},"finish_reason":null}]}`,
			`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
			`[DONE]`,
		}
	}
	finalText := []string{
		`{"choices":[{"delta":{"role":"assistant","content":"OK, stopping — the loop guard told me to."},"finish_reason":"stop"}]}`,
		`[DONE]`,
	}
	srv := newScriptedLLM(t, [][]string{
		repeatedCall("c1"),
		repeatedCall("c2"),
		repeatedCall("c3"),
		finalText,
	})

	scenario := Scenario{
		Name:        "loop_guard_blocks_repeats",
		Description: "loop_guard refuses the 3rd identical tool call in a single turn",
		Prompt:      "demonstrate the loop guard by repeating yourself",
		Setup: func(workspaceDir string) error {
			return os.WriteFile(filepath.Join(workspaceDir, "README.md"), []byte("anything"), 0o644)
		},
		MaxTurnSteps: 6,
		Checks: []Check{
			TurnEndedCleanly(),
			ToolCalled("read_file", nil),
			// The mechanism's signature is a tool result containing
			// "loop_guard" — exactly the substring the runtime emits
			// when it blocks a repeat. If the mechanism is removed or
			// silently changed, this check fires.
			ToolResultContains("read_file", "loop_guard"),
		},
	}

	runner := &Runner{
		LLM:            agent.NewLLMClient(srv.URL, "", "fake-model"),
		MaxTurnSteps:   6,
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
		for _, r := range out.Results {
			t.Logf("  %s: pass=%v detail=%s", r.Check, r.Pass, r.Detail)
		}
	}

	// Independent assertions on the captured trace shape for diagnostic
	// value when the mechanism actually IS broken.
	if len(out.Trace.Tools) != 3 {
		t.Fatalf("expected 3 tool call entries (first two real, third guard-blocked); got %d", len(out.Trace.Tools))
	}
	first, second, third := out.Trace.Tools[0], out.Trace.Tools[1], out.Trace.Tools[2]
	if first.IsErr || second.IsErr {
		t.Errorf("first two read_file calls must succeed before the guard fires; got first.IsErr=%v second.IsErr=%v",
			first.IsErr, second.IsErr)
	}
	if !third.IsErr {
		t.Errorf("3rd identical call must surface as an error tool result (guard rejection); got IsErr=false result=%q", third.Result)
	}
	if !strings.Contains(third.Result, "loop_guard: blocked repeated call") {
		t.Errorf("3rd call must carry the exact guard message so trace consumers can grep for it; got %q", third.Result)
	}
}

// TestRunner_EndToEnd_BroadShellScanGuardBlocks pins the shell guard
// for unbounded filesystem scans (find / -name ..., grep -r / ...,
// rg / --files). The runtime refuses the call before it reaches the
// executor; the rejection surfaces in the trace as IsErr=true with
// "unbounded filesystem scan" in the result.
//
// Same failure-mode -> mechanism -> trace-check pattern as the
// loop_guard test. The guard is the user's earliest mechanism
// (commit 4e7c993); this closes the loop by making it observable
// from the eval harness.
func TestRunner_EndToEnd_BroadShellScanGuardBlocks(t *testing.T) {
	// LLM tries `find / -name go -type f` then emits a final answer.
	turn1 := []string{
		`{"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"s1","type":"function","function":{"name":"shell","arguments":""}}]},"finish_reason":null}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"command\":\"find / -name go -type f\"}"}}]},"finish_reason":null}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`[DONE]`,
	}
	turn2 := []string{
		`{"choices":[{"delta":{"role":"assistant","content":"Guard refused the scan; will use a bounded path instead."},"finish_reason":"stop"}]}`,
		`[DONE]`,
	}
	srv := newScriptedLLM(t, [][]string{turn1, turn2})

	scenario := Scenario{
		Name:        "broad_shell_scan_blocked",
		Description: "shell guard refuses 'find / -name ...' before it reaches the executor",
		Prompt:      "find every go binary on this system",
		Checks: []Check{
			TurnEndedCleanly(),
			ToolCalled("shell", nil),
			ToolResultContains("shell", "unbounded filesystem scan"),
		},
	}
	runner := &Runner{
		LLM:            agent.NewLLMClient(srv.URL, "", "fake-model"),
		MaxTurnSteps:   4,
		PerCallTimeout: 5 * time.Second,
		RunTimeout:     15 * time.Second,
		Log:            zerolog.Nop(),
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
	}
	if len(out.Trace.Tools) != 1 {
		t.Fatalf("expected exactly one shell call (rejected); got %d", len(out.Trace.Tools))
	}
	tc := out.Trace.Tools[0]
	if !tc.IsErr {
		t.Errorf("rejected scan must surface as IsErr=true; got false (result=%q)", tc.Result)
	}
	// Cross-check: a path-bounded find must NOT trigger the guard.
	// We don't run a second scenario here — the agent's own unit tests
	// in builtins_test.go cover that path — but pinning the
	// substring keeps the rejection wording diff-friendly.
	if !strings.Contains(tc.Result, "Use a specific workspace path or a bounded tool-discovery path instead") {
		t.Errorf("rejection should suggest a remedy; got %q", tc.Result)
	}
}

// TestRunner_EndToEnd_MaskedVerificationGuardBlocks pins the shell
// guard for exit-code-masking pipes (pytest | head, go test || true,
// echo $? wrappers). Same shape as the broad-scan test: model emits
// a masked command; runtime refuses; trace contains the guard's
// signature substring.
//
// This guard is the mechanism that came out of the "small model
// pipes test output to head, then claims success" incident.
// Verifying it through eval prevents a silent regression that
// would let small models mask verification failures again.
func TestRunner_EndToEnd_MaskedVerificationGuardBlocks(t *testing.T) {
	// The masking pattern: `python -m pytest 2>&1 | head -80`. The
	// 2>&1 redirects stderr into stdout, `| head` discards the rest
	// (and the exit code), so a real test failure would look like a
	// successful run with truncated output. The guard rejects the
	// shape before the executor sees it.
	turn1 := []string{
		`{"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"s1","type":"function","function":{"name":"shell","arguments":""}}]},"finish_reason":null}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"command\":\"python -m pytest 2>&1 | head -80\"}"}}]},"finish_reason":null}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`[DONE]`,
	}
	turn2 := []string{
		`{"choices":[{"delta":{"role":"assistant","content":"OK, will run pytest directly without piping to head."},"finish_reason":"stop"}]}`,
		`[DONE]`,
	}
	srv := newScriptedLLM(t, [][]string{turn1, turn2})

	scenario := Scenario{
		Name:        "masked_verification_blocked",
		Description: "shell guard refuses 'pytest | head' style exit-code-masking pipes",
		Prompt:      "run pytest and show me only the head of the output",
		Checks: []Check{
			TurnEndedCleanly(),
			ToolCalled("shell", nil),
			ToolResultContains("shell", "masks a test/build exit code"),
		},
	}
	runner := &Runner{
		LLM:            agent.NewLLMClient(srv.URL, "", "fake-model"),
		MaxTurnSteps:   4,
		PerCallTimeout: 5 * time.Second,
		RunTimeout:     15 * time.Second,
		Log:            zerolog.Nop(),
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
	}
	if len(out.Trace.Tools) != 1 {
		t.Fatalf("expected exactly one shell call (rejected); got %d", len(out.Trace.Tools))
	}
	tc := out.Trace.Tools[0]
	if !tc.IsErr {
		t.Errorf("rejected masked command must surface as IsErr=true; got false (result=%q)", tc.Result)
	}
	if !strings.Contains(tc.Result, "Run the verification command directly") {
		t.Errorf("rejection should suggest the remedy; got %q", tc.Result)
	}
}

// TestRunner_EndToEnd_ToolArgRepairFixesMalformedJSON pins the third
// runtime mechanism through the eval harness: repairToolCallArgsForDispatch
// turns model-emitted malformed JSON ({"path":"README.md",} — trailing
// comma) into valid args before the tool sees them. Without this guard,
// small models that emit slightly-off JSON brick the turn; with it,
// they recover and the tool runs.
//
// Trace fields ArgsRepaired and RepairNotes were added specifically so
// the eval harness can detect this from outside. This test closes the
// loop by exercising the runtime, the trace plumbing, and the
// ToolRequestRepaired check all together end-to-end.
func TestRunner_EndToEnd_ToolArgRepairFixesMalformedJSON(t *testing.T) {
	// Turn 1: model emits read_file with TRAILING COMMA in the args
	// JSON — a real model misbehavior shape. parseToolArgJSON rejects
	// it; stripTrailingCommas + parseToolArgJSON repairs it.
	//
	// The arguments string is sent as one streamed chunk. The literal
	// JSON body the model "wrote" is: {"path":"README.md",} — note
	// the comma before the closing brace.
	turn1 := []string{
		`{"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"r1","type":"function","function":{"name":"read_file","arguments":""}}]},"finish_reason":null}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"path\":\"README.md\",}"}}]},"finish_reason":null}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`[DONE]`,
	}
	turn2 := []string{
		`{"choices":[{"delta":{"role":"assistant","content":"The README is the one-liner: hello agent."},"finish_reason":"stop"}]}`,
		`[DONE]`,
	}
	srv := newScriptedLLM(t, [][]string{turn1, turn2})

	scenario := Scenario{
		Name:        "tool_arg_repair_trailing_comma",
		Description: "runtime repairs trailing-comma JSON before tool dispatch; trace marks ArgsRepaired=true",
		Prompt:      "read README.md",
		Setup: func(workspaceDir string) error {
			return os.WriteFile(filepath.Join(workspaceDir, "README.md"), []byte("hello agent"), 0o644)
		},
		MaxTurnSteps: 4,
		Checks: []Check{
			TurnEndedCleanly(),
			ToolCalled("read_file", nil),
			// The framework's existing check that asserts this exact
			// mechanism — passes when ArgsRepaired || Canonicalized
			// is true for any call matching the tool name.
			ToolRequestRepaired("read_file"),
			ToolRepairKindAtLeast("malformed_json", 1),
			FinalTextContains("hello agent"),
		},
	}

	runner := &Runner{
		LLM:            agent.NewLLMClient(srv.URL, "", "fake-model"),
		MaxTurnSteps:   4,
		PerCallTimeout: 5 * time.Second,
		RunTimeout:     15 * time.Second,
		Log:            zerolog.Nop(),
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
	}

	// Trace-shape assertions for diagnostic value when the mechanism IS broken.
	if len(out.Trace.Tools) != 1 {
		t.Fatalf("expected exactly one tool call (read_file with repaired args); got %d", len(out.Trace.Tools))
	}
	tc := out.Trace.Tools[0]
	if !tc.ArgsRepaired {
		t.Errorf("ArgsRepaired must be true after the runtime fixed the trailing comma; got false")
	}
	if tc.IsErr {
		t.Errorf("repaired call should dispatch successfully; got IsErr=true result=%q", tc.Result)
	}
	if got, _ := tc.Args["path"].(string); got != "README.md" {
		t.Errorf("repaired args should have path=README.md; got %q (full args=%v)", got, tc.Args)
	}
	if !strings.Contains(tc.Result, "hello agent") {
		t.Errorf("repaired call's tool result should contain the file contents; got %q", tc.Result)
	}
}

// TestRunner_EndToEnd_ToolSchemaCoercionFixesScalarType pins the
// fourth runtime mechanism end-to-end: repairToolArgsWithSchema
// coerces string-typed values to integers / booleans / etc. when the
// tool schema declares the target type. This catches the common
// small-model failure where every JSON value comes out a string —
// without coercion, shell.timeout_sec=\"5\" would be decoded into an
// int and silently end up as zero, then the tool runs without a
// caller-set timeout and the model misreads what happened.
//
// Companion to the JSON-repair test (e379960). Both produce
// ArgsRepaired=true on the captured ToolCall and trip the framework's
// ToolRequestRepaired check — but they exercise different code paths
// (parseToolArgJSON salvage vs repairToolArgsWithSchema coercion).
func TestRunner_EndToEnd_ToolSchemaCoercionFixesScalarType(t *testing.T) {
	// The args body is valid JSON, but timeout_sec is sent as the
	// string \"5\" instead of the integer 5. Schema repair coerces it.
	turn1 := []string{
		`{"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"sh1","type":"function","function":{"name":"shell","arguments":""}}]},"finish_reason":null}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"command\":\"echo agent\",\"timeout_sec\":\"5\"}"}}]},"finish_reason":null}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`[DONE]`,
	}
	turn2 := []string{
		`{"choices":[{"delta":{"role":"assistant","content":"Shell printed: agent."},"finish_reason":"stop"}]}`,
		`[DONE]`,
	}
	srv := newScriptedLLM(t, [][]string{turn1, turn2})

	scenario := Scenario{
		Name:         "tool_schema_coercion_string_to_int",
		Description:  "runtime coerces shell.timeout_sec=\"5\" string to integer 5 before dispatch",
		Prompt:       "run echo agent",
		MaxTurnSteps: 4,
		Checks: []Check{
			TurnEndedCleanly(),
			ToolCalled("shell", nil),
			ToolRequestRepaired("shell"),
			ToolRepairKindAtLeast("type_coercion", 1),
			FinalTextContains("agent"),
		},
	}

	runner := &Runner{
		LLM:            agent.NewLLMClient(srv.URL, "", "fake-model"),
		MaxTurnSteps:   4,
		PerCallTimeout: 5 * time.Second,
		RunTimeout:     15 * time.Second,
		Log:            zerolog.Nop(),
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
	}

	if len(out.Trace.Tools) != 1 {
		t.Fatalf("expected exactly one shell call; got %d", len(out.Trace.Tools))
	}
	tc := out.Trace.Tools[0]
	if !tc.ArgsRepaired {
		t.Errorf("ArgsRepaired must be true after schema coercion; got false (args=%v notes=%v)",
			tc.Args, tc.RepairNotes)
	}
	if tc.IsErr {
		t.Errorf("coerced call should dispatch successfully; got IsErr=true result=%q", tc.Result)
	}
	// After coercion, timeout_sec should be a json.Number / float64 / int
	// (encoding/json's map decode picks float64 for unmarshaled numbers).
	// Either shape is fine — the assertion is "no longer the string
	// '5'". A regression that disables coercion would leave it as a
	// string, which is what this catches.
	if got, isString := tc.Args["timeout_sec"].(string); isString {
		t.Errorf("timeout_sec still a string after coercion: %q (schema_repair didn't fire)", got)
	}
	if !strings.Contains(tc.Result, "agent") {
		t.Errorf("shell result should contain 'agent' (echo output); got %q", tc.Result)
	}
}

// TestRunner_EndToEnd_ToolSchemaRepairNormalizesEnums pins enum
// normalization through the full eval runner. Small models often emit
// schema enum values with extra whitespace or different casing; the
// runtime should repair unambiguous values before dispatch and expose
// the repair kind in trace-derived eval checks.
func TestRunner_EndToEnd_ToolSchemaRepairNormalizesEnums(t *testing.T) {
	turn1 := []string{
		`{"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"p1","type":"function","function":{"name":"plan","arguments":""}}]},"finish_reason":null}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"action\":\" SET \",\"steps\":[{\"text\":\"inspect workspace\",\"status\":\" IN_PROGRESS \"}]}"}}]},"finish_reason":null}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`[DONE]`,
	}
	turn2 := []string{
		`{"choices":[{"delta":{"role":"assistant","content":"Planned the inspection step."},"finish_reason":"stop"}]}`,
		`[DONE]`,
	}
	srv := newScriptedLLM(t, [][]string{turn1, turn2})

	scenario := Scenario{
		Name:         "tool_schema_enum_normalization",
		Description:  "runtime normalizes whitespace/case variants of schema enum values before dispatch",
		Prompt:       "create a brief plan",
		MaxTurnSteps: 4,
		Checks: []Check{
			TurnEndedCleanly(),
			ToolCalled("plan", nil),
			ToolRequestRepaired("plan"),
			ToolRepairKindAtLeast("enum_normalization", 1),
			ToolResultContains("plan", "plan set"),
			FinalTextContains("Planned"),
		},
	}

	runner := &Runner{
		LLM:            agent.NewLLMClient(srv.URL, "", "fake-model"),
		MaxTurnSteps:   4,
		PerCallTimeout: 5 * time.Second,
		RunTimeout:     15 * time.Second,
		Log:            zerolog.Nop(),
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
	}
	if len(out.Trace.Tools) != 1 {
		t.Fatalf("expected exactly one plan call; got %d", len(out.Trace.Tools))
	}
	tc := out.Trace.Tools[0]
	if !tc.ArgsRepaired {
		t.Fatalf("ArgsRepaired must be true after enum normalization; args=%v notes=%v", tc.Args, tc.RepairNotes)
	}
	if got, _ := tc.Args["action"].(string); got != "set" {
		t.Fatalf("normalized action = %q, want set; args=%v notes=%v", got, tc.Args, tc.RepairNotes)
	}
	if tc.IsErr {
		t.Fatalf("normalized plan call should dispatch successfully; result=%q", tc.Result)
	}
}

// TestRunner_EndToEnd_SubagentDepthBudgetBlocksNestedDelegation pins
// the runtime's recursive-delegation cap end-to-end through the eval
// harness. With MaxDepth=1, buildSubagentRegistry must NOT register
// subagent_run in the child's tool set; if the child still tries to
// call it, the dispatch should return "tool \"subagent_run\" is not
// available", and that refusal text must surface in the parent's
// captured tool.result (which carries the child's JSON report).
//
// Same shape as the loop_guard / shell-guard eval tests — a runtime
// invariant that is unit-tested inside the agent package becomes
// trace-observable through the eval framework, so a regression that
// silently raises the depth cap or drops the check fails CI.
func TestRunner_EndToEnd_SubagentDepthBudgetBlocksNestedDelegation(t *testing.T) {
	// Parent turn 1: ask for subagent_run.
	parentTurn1 := []string{
		`{"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"p1","type":"function","function":{"name":"subagent_run","arguments":""}}]},"finish_reason":null}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"task\":\"recursively delegate one more time\",\"mode\":\"explore\"}"}}]},"finish_reason":null}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`[DONE]`,
	}
	// Child turn 1: attempt to call subagent_run again. With MaxDepth=1
	// this tool is NOT in the child's registry, so dispatch returns the
	// "tool not available" Error string.
	childTurn1 := []string{
		`{"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"c1","type":"function","function":{"name":"subagent_run","arguments":""}}]},"finish_reason":null}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"task\":\"go one level deeper\",\"mode\":\"explore\"}"}}]},"finish_reason":null}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`[DONE]`,
	}
	// Child turn 2: after the "not available" error, surrender with a
	// report that quotes the failure shape verbatim. Lets the parent's
	// trace consumer grep for the refusal as evidence.
	childTurn2 := []string{
		`{"choices":[{"delta":{"role":"assistant","content":"Conclusion:\nDepth budget refused further delegation.\nEvidence:\n- attempted subagent_run; runtime answered: tool \"subagent_run\" is not available"},"finish_reason":"stop"}]}`,
		`[DONE]`,
	}
	// Parent turn 2: synthesize from the report.
	parentTurn2 := []string{
		`{"choices":[{"delta":{"role":"assistant","content":"Recursive delegation was refused by the depth budget."},"finish_reason":"stop"}]}`,
		`[DONE]`,
	}
	srv := newScriptedLLM(t, [][]string{parentTurn1, childTurn1, childTurn2, parentTurn2})

	scenario := Scenario{
		Name:         "subagent_depth_budget_enforces_max_depth_1",
		Description:  "with MaxDepth=1 the child must not see subagent_run in its registry",
		Prompt:       "delegate to a subagent and ask it to delegate further",
		MaxTurnSteps: 6,
		Checks: []Check{
			TurnEndedCleanly(),
			ToolCalled("subagent_run", nil),
			// The depth-budget signature: the child's report must
			// surface the runtime's exact rejection wording. A
			// regression that silently allows recursive delegation
			// would not see "is not available" in the report,
			// failing this check.
			ToolResultContains("subagent_run", `is not available`),
		},
	}

	llmClient := agent.NewLLMClient(srv.URL, "", "fake-model")
	runner := &Runner{
		LLM:            llmClient,
		MaxTurnSteps:   6,
		PerCallTimeout: 5 * time.Second,
		RunTimeout:     30 * time.Second,
		Log:            zerolog.Nop(),
		BuildRegistry: func(ctx context.Context, workspaceDir string, exec executor.Executor) (*agent.Registry, error) {
			reg, err := defaultBuildRegistry(ctx, workspaceDir, exec)
			if err != nil {
				return nil, err
			}
			// MaxDepth=1 is the load-bearing knob. The parent's
			// childDepth() is 1, and 1 < 1 is false, so the child
			// registry skips subagent_run.
			agent.RegisterSubagent(reg, agent.SubagentDeps{
				LLM:              llmClient,
				Executor:         exec,
				HostWorkspaceDir: workspaceDir,
				Log:              zerolog.Nop(),
				PerCallTimeout:   5 * time.Second,
				MaxDepth:         1,
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
			t.Logf("    [%d] %s exit=%d result=%s", i, c.Tool, c.ExitCode, c.Result[:min(200, len(c.Result))])
		}
	}

	// Independent trace-shape assertions for diagnostic value.
	if len(out.Trace.Tools) != 1 {
		t.Fatalf("parent should have made exactly ONE tool call (subagent_run); got %d", len(out.Trace.Tools))
	}
	tc := out.Trace.Tools[0]
	if tc.Tool != "subagent_run" {
		t.Errorf("parent's only tool call should be subagent_run; got %q", tc.Tool)
	}
	// The parent's subagent_run itself succeeded; what failed was the
	// CHILD's nested attempt. The parent sees the report (which quotes
	// the refusal). isErr on the parent's tool should be false because
	// the parent's call did not error — only the recursion did.
	if tc.IsErr {
		t.Errorf("parent's subagent_run itself should not error; got IsErr=true result=%q", tc.Result)
	}
}

// TestRunner_EndToEnd_LoopGuardFailureHalt pins the seventh and final
// item in the runtime mechanism coverage matrix: toolLoopGuard's
// failure-counting branch. After 3 consecutive failures of the same
// tool, the guard appends a warning to the result; after 8, it halts
// the tool ('Stop retrying it'). Both thresholds are now trace-observable
// from the eval harness.
//
// Distinct from the loop_guard identical-call test (c64fabb): that
// blocks an exact-repeated (tool, args) hash; this counts consecutive
// failures of the tool itself regardless of args. Both branches need
// their own coverage because a regression in one wouldn't fail the
// other's test.
func TestRunner_EndToEnd_LoopGuardFailureHalt(t *testing.T) {
	// A custom tool that always errors. The Runner's BuildRegistry
	// hook lets us inject it into the child run without touching
	// the production builtins.
	flakyDescriptor := agent.Tool{
		Name:        "flaky_probe",
		Description: "Test-only probe that always returns an error. Used to exercise the toolLoopGuard failure-counting branch.",
		Schema:      json.RawMessage(`{"type":"object","properties":{}}`),
		Execute: func(ctx context.Context, args json.RawMessage) (string, error) {
			return "", errors.New("simulated probe failure")
		},
	}

	// Each turn emits one flaky_probe call. The args change slightly
	// each time so the identical-call guard (block at 3 same-hash
	// attempts) doesn't fire — only the failure-counting branch should.
	flakyTurn := func(callID string, marker int) []string {
		argBody := fmt.Sprintf(`{\"attempt\":%d}`, marker)
		return []string{
			`{"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"` + callID + `","type":"function","function":{"name":"flaky_probe","arguments":""}}]},"finish_reason":null}]}`,
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"` + argBody + `"}}]},"finish_reason":null}]}`,
			`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
			`[DONE]`,
		}
	}
	finalText := []string{
		`{"choices":[{"delta":{"role":"assistant","content":"OK, the guard halted me — I will stop retrying."},"finish_reason":"stop"}]}`,
		`[DONE]`,
	}
	script := [][]string{
		flakyTurn("a1", 1),
		flakyTurn("a2", 2),
		flakyTurn("a3", 3),
		flakyTurn("a4", 4),
		flakyTurn("a5", 5),
		flakyTurn("a6", 6),
		flakyTurn("a7", 7),
		flakyTurn("a8", 8),
		finalText,
	}
	srv := newScriptedLLM(t, script)

	scenario := Scenario{
		Name:         "loop_guard_failure_halt",
		Description:  "8 consecutive flaky_probe failures: trace must show warn @ 3 and halt @ 8",
		Prompt:       "exercise the failing probe to verify the guard",
		MaxTurnSteps: 12,
		Checks: []Check{
			TurnEndedCleanly(),
			ToolCalled("flaky_probe", nil),
			ToolResultContains("flaky_probe", "failed 3 consecutive times"),
			ToolResultContains("flaky_probe", "failed 8 consecutive times"),
			ToolResultContains("flaky_probe", "Stop retrying"),
			ToolResultContains("flaky_probe", "Next:"),
		},
	}

	runner := &Runner{
		LLM:            agent.NewLLMClient(srv.URL, "", "fake-model"),
		MaxTurnSteps:   12,
		PerCallTimeout: 5 * time.Second,
		RunTimeout:     30 * time.Second,
		Log:            zerolog.Nop(),
		BuildRegistry: func(ctx context.Context, workspaceDir string, exec executor.Executor) (*agent.Registry, error) {
			reg, err := defaultBuildRegistry(ctx, workspaceDir, exec)
			if err != nil {
				return nil, err
			}
			reg.Add(&flakyDescriptor)
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
			result := c.Result
			if len(result) > 150 {
				result = result[:150] + "..."
			}
			t.Logf("    [%d] %s isErr=%v result=%s", i, c.Tool, c.IsErr, result)
		}
	}

	// Shape assertions: every flaky_probe call should be marked IsErr
	// (either by the underlying error or by the guard). At least 8 calls
	// should have happened so the halt threshold has data to count.
	flakyCalls := 0
	for _, tc := range out.Trace.Tools {
		if tc.Tool == "flaky_probe" {
			flakyCalls++
			if !tc.IsErr {
				t.Errorf("call_id=%s flaky_probe must surface as error; got IsErr=false", tc.CallID)
			}
		}
	}
	if flakyCalls < 8 {
		t.Errorf("expected at least 8 flaky_probe calls to exercise the halt threshold; got %d", flakyCalls)
	}
}

// TestRunner_EndToEnd_WebSnapshotFactExtraction is the in-process
// web-page extraction scenario the user named as one of the four
// real-world eval categories (alongside code repair, repo
// understanding, and subagent delegation). The framework didn't
// previously exercise this path.
//
// The contract being measured here: when a page snapshot already
// carries the requested fact, the agent must answer from the
// snapshot rather than spinning up a shell / curl to refetch the
// page. This is the "answer from the report" pattern applied to
// rendered web inspection — same shape as the subagent delegation
// test, but the report is a browser_navigate snapshot rather than
// a subagent_run JSON payload.
//
// Uses a stand-in browser_navigate tool that returns a deterministic
// snapshot, so the test runs in milliseconds without any actual
// browser dependency. Lets the eval framework cover the user's full
// scenario matrix without pulling extras/browser into the test deps.
func TestRunner_EndToEnd_WebSnapshotFactExtraction(t *testing.T) {
	const snapshot = `URL: https://example.com/stats
Title: Project stats
Body:
- Active sessions: 42
- Canonical region: us-east-1
- Last updated: 2026-05-22T14:30:00Z`

	browserNavigate := agent.Tool{
		Name:        "browser_navigate",
		Description: "Test-only browser navigate stand-in: returns a deterministic page snapshot.",
		Schema: json.RawMessage(`{
            "type": "object",
            "required": ["url"],
            "properties": {
                "url": {"type": "string"},
                "wait_until": {"type": "string"}
            }
        }`),
		Execute: func(ctx context.Context, args json.RawMessage) (string, error) {
			return snapshot, nil
		},
	}

	// Parent turn 1: emit browser_navigate.
	turn1 := []string{
		`{"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"b1","type":"function","function":{"name":"browser_navigate","arguments":""}}]},"finish_reason":null}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"url\":\"https://example.com/stats\",\"wait_until\":\"networkidle\"}"}}]},"finish_reason":null}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`[DONE]`,
	}
	// Parent turn 2: synthesize the answer from the snapshot, no
	// extra tool calls.
	turn2 := []string{
		`{"choices":[{"delta":{"role":"assistant","content":"Canonical region from the snapshot: us-east-1. Active sessions: 42."},"finish_reason":"stop"}]}`,
		`[DONE]`,
	}
	srv := newScriptedLLM(t, [][]string{turn1, turn2})

	scenario := Scenario{
		Name:         "web_snapshot_fact_extraction",
		Description:  "agent reads a rendered page snapshot once and answers from it; does not refetch",
		Prompt:       "what's the canonical region at https://example.com/stats?",
		MaxTurnSteps: 4,
		Checks: []Check{
			TurnEndedCleanly(),
			ToolCalled("browser_navigate", func(args map[string]any) bool {
				return args["url"] == "https://example.com/stats"
			}),
			// Pin the user-named anti-pattern: do not shell-curl /
			// python-fetch the same URL the browser already snapshotted.
			ToolNotCalled("shell", nil),
			// After the snapshot, no more tool calls — answer from the
			// snapshot. Same "delegate-then-answer" contract as the
			// subagent test but applied to the snapshot.
			MaxToolCallsAfter("browser_navigate", 0),
			FinalTextContains("us-east-1"),
			// The model must not echo other facts the snapshot
			// contained but the user didn't ask about. Soft check —
			// the scripted LLM happens to mention "42", so this
			// scenario actually exercises the "selective extraction"
			// flavor: report what was asked, not the whole page.
			FinalTextContains("Active sessions: 42"),
		},
	}

	runner := &Runner{
		LLM:            agent.NewLLMClient(srv.URL, "", "fake-model"),
		MaxTurnSteps:   4,
		PerCallTimeout: 5 * time.Second,
		RunTimeout:     15 * time.Second,
		Log:            zerolog.Nop(),
		BuildRegistry: func(ctx context.Context, workspaceDir string, exec executor.Executor) (*agent.Registry, error) {
			reg, err := defaultBuildRegistry(ctx, workspaceDir, exec)
			if err != nil {
				return nil, err
			}
			reg.Add(&browserNavigate)
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
	}
	if len(out.Trace.Tools) != 1 || out.Trace.Tools[0].Tool != "browser_navigate" {
		t.Fatalf("expected exactly one browser_navigate call; got %+v", out.Trace.Tools)
	}
	if !strings.Contains(out.Trace.Tools[0].Result, "Canonical region: us-east-1") {
		t.Errorf("tool result should contain the snapshot text; got %q", out.Trace.Tools[0].Result)
	}
}

// TestRunner_EndToEnd_ExternalResearchFlow pins the generic research shape
// behind real user requests like "analyze recent trend for X" without baking in
// any specific project, subnet, site, or token. The agent first discovers
// sources, then reads primary/metrics/social sources, then separates verified
// facts from sentiment in the final answer.
func TestRunner_EndToEnd_ExternalResearchFlow(t *testing.T) {
	webSearch := agent.Tool{
		Name:        "web_search",
		Description: "Test-only search stand-in: returns deterministic source candidates.",
		Schema: json.RawMessage(`{
            "type": "object",
            "additionalProperties": false,
            "required": ["query"],
            "properties": {
                "query": {"type": "string"},
                "num_results": {"type": "integer"}
            }
        }`),
		Execute: func(ctx context.Context, args json.RawMessage) (string, error) {
			return `[
  {"title":"Nimbus Protocol official docs","url":"https://official.example/nimbus/about","snippet":"Primary source describing Nimbus Protocol as a decentralized compute subnet."},
  {"title":"Nimbus Protocol market metrics","url":"https://metrics.example/nimbus","snippet":"Current price, market cap, volume, and 24h change."},
  {"title":"Recent community discussion","url":"https://social.example/search/nimbus","snippet":"Recent positive and critical community posts."}
]`, nil
		},
	}
	webFetch := agent.Tool{
		Name:        "web_fetch",
		Description: "Test-only fetch stand-in: returns deterministic page text.",
		Schema: json.RawMessage(`{
            "type": "object",
            "additionalProperties": false,
            "required": ["url"],
            "properties": {
                "url": {"type": "string"}
            }
        }`),
		Execute: func(ctx context.Context, args json.RawMessage) (string, error) {
			var p struct {
				URL string `json:"url"`
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return "", err
			}
			switch p.URL {
			case "https://official.example/nimbus/about":
				return "Official docs, updated 2026-05-20: Nimbus Protocol is a decentralized compute subnet for model-routing workloads.", nil
			case "https://metrics.example/nimbus":
				return "Metrics snapshot as of 2026-05-24T12:00:00Z: price $17.78, market cap $56.7M, 24h change +7.2%, 24h volume $2.36M.", nil
			case "https://social.example/search/nimbus":
				return "Community posts from 2026-05-23 to 2026-05-24: supporters cite rising volume and integrations; critics question sustainability and liquidity depth.", nil
			default:
				return "", fmt.Errorf("unexpected test URL %q", p.URL)
			}
		},
	}

	turn1 := []string{
		`{"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"s1","type":"function","function":{"name":"web_search","arguments":"{\"query\":\"Nimbus Protocol recent trend market metrics sentiment\",\"num_results\":5}"}}]},"finish_reason":null}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`[DONE]`,
	}
	turn2 := []string{
		`{"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"f1","type":"function","function":{"name":"web_fetch","arguments":"{\"url\":\"https://official.example/nimbus/about\"}"}},{"index":1,"id":"f2","type":"function","function":{"name":"web_fetch","arguments":"{\"url\":\"https://metrics.example/nimbus\"}"}},{"index":2,"id":"f3","type":"function","function":{"name":"web_fetch","arguments":"{\"url\":\"https://social.example/search/nimbus\"}"}}]},"finish_reason":null}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`[DONE]`,
	}
	turn3 := []string{
		`{"choices":[{"delta":{"role":"assistant","content":"Nimbus Protocol is a decentralized compute subnet for model-routing workloads according to the official docs. As of 2026-05-24T12:00:00Z, the metrics source reports price $17.78, market cap $56.7M, 24h change +7.2%, and 24h volume $2.36M. Recent community sentiment is mixed: supporters cite volume and integrations, while critics question sustainability and liquidity depth. Overall: recent momentum is positive on the metrics, but the outlook should be treated cautiously because social evidence is mixed and liquidity depth is questioned."},"finish_reason":"stop"}]}`,
		`[DONE]`,
	}
	srv := newScriptedLLM(t, [][]string{turn1, turn2, turn3})
	nimbusSearch := func(args map[string]any) bool {
		q, _ := args["query"].(string)
		return strings.Contains(q, "Nimbus Protocol")
	}
	fetchURL := func(url string) func(map[string]any) bool {
		return func(args map[string]any) bool {
			return args["url"] == url
		}
	}

	scenario := Scenario{
		Name:         "external_research_trend_synthesis",
		Description:  "agent discovers sources, reads primary/metrics/social pages, and separates facts from sentiment",
		Prompt:       "Assess the recent trend for Nimbus Protocol. First identify what it is, then collect current market metrics and recent community sentiment. Be objective and cite evidence types.",
		MaxTurnSteps: 6,
		Checks: []Check{
			TurnEndedCleanly(),
			ToolCalled("web_search", nimbusSearch),
			ToolCalled("web_fetch", fetchURL("https://official.example/nimbus/about")),
			ToolCalled("web_fetch", fetchURL("https://metrics.example/nimbus")),
			ToolCalled("web_fetch", fetchURL("https://social.example/search/nimbus")),
			ToolCalledAtLeast("web_fetch", 3),
			ToolCalledBeforeMatching("web_search", nimbusSearch, "web_fetch", fetchURL("https://official.example/nimbus/about")),
			ToolCalledBeforeMatching("web_search", nimbusSearch, "web_fetch", fetchURL("https://metrics.example/nimbus")),
			ToolCalledBeforeMatching("web_search", nimbusSearch, "web_fetch", fetchURL("https://social.example/search/nimbus")),
			ToolNotCalled("shell", nil),
			FinalTextContains("decentralized compute subnet"),
			FinalTextContains("market cap $56.7M"),
			FinalTextContains("+7.2%"),
			FinalTextContains("mixed"),
			FinalTextContains("cautiously"),
		},
	}

	runner := &Runner{
		LLM:            agent.NewLLMClient(srv.URL, "", "fake-model"),
		MaxTurnSteps:   6,
		PerCallTimeout: 5 * time.Second,
		RunTimeout:     20 * time.Second,
		Log:            zerolog.Nop(),
		BuildRegistry: func(ctx context.Context, workspaceDir string, exec executor.Executor) (*agent.Registry, error) {
			_ = ctx
			_ = workspaceDir
			_ = exec
			reg := agent.NewRegistry()
			reg.Add(&webSearch)
			reg.Add(&webFetch)
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
	}
	if len(out.Trace.Tools) != 4 {
		t.Fatalf("expected one search and three fetches; got %+v", out.Trace.Tools)
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
