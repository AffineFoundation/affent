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

func writeRecallSessionLog(t *testing.T, sessionsDir, sid string, msgs ...agent.ChatMessage) {
	t.Helper()
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(sessionsDir, sid+".jsonl")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetEscapeHTML(false)
	for _, msg := range msgs {
		if err := enc.Encode(msg); err != nil {
			t.Fatal(err)
		}
	}
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

// TestRunner_DefaultRuntimeAdvertisesRepoSearchAndCanUseIt pins the
// repo discovery path end-to-end: the default runtime must expose
// repo_search and the model must be able to call it and answer from
// the compact result.
func TestRunner_DefaultRuntimeAdvertisesRepoSearchAndCanUseIt(t *testing.T) {
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
	var calls atomic.Int32
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
		flusher := w.(http.Flusher)
		idx := int(calls.Add(1)) - 1
		payloads := [][]string{
			{
				`{"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"r1","type":"function","function":{"name":"repo_search","arguments":""}}]},"finish_reason":null}]}`,
				`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"query\":\"repoSearchTool\",\"path\":\"internal/agent\",\"max_results\":3}"}}]},"finish_reason":null}]}`,
				`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
				`[DONE]`,
			},
			{
				`{"choices":[{"delta":{"role":"assistant","content":"repo_search found internal/agent/repo_search.go, so the built-in discovery path is available."},"finish_reason":"stop"}]}`,
				`[DONE]`,
			},
		}
		chosen := payloads[len(payloads)-1]
		if idx < len(payloads) {
			chosen = payloads[idx]
		}
		for _, p := range chosen {
			_, _ = w.Write([]byte("data: " + p + "\n\n"))
			flusher.Flush()
		}
	}))
	t.Cleanup(srv.Close)

	scenario := Scenario{
		Name:        "default_runtime_repo_search",
		Description: "default eval runtime advertises repo_search and can use it for workspace discovery",
		Prompt:      "find the repo_search implementation in this workspace and answer from the result",
		Setup: func(workspaceDir string) error {
			if err := os.MkdirAll(filepath.Join(workspaceDir, "internal", "agent"), 0o755); err != nil {
				return err
			}
			return os.WriteFile(filepath.Join(workspaceDir, "internal", "agent", "repo_search.go"), []byte("package agent\n\nfunc repoSearchTool() {}\n"), 0o644)
		},
		MaxTurnSteps: 4,
		Checks: []Check{
			TurnEndedCleanly(),
			ToolCalled("repo_search", func(args map[string]any) bool {
				return args["query"] == "repoSearchTool" && args["path"] == "internal/agent"
			}),
			FinalTextContains("repo_search found internal/agent/repo_search.go"),
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
		t.Fatalf("expected all checks to pass; failed: %v", out.FailedChecks())
	}
	select {
	case req := <-requests:
		foundTool := false
		for _, tool := range req.Tools {
			if tool.Function.Name == "repo_search" {
				foundTool = true
			}
		}
		if !foundTool {
			t.Fatalf("default runtime should advertise repo_search; tools=%+v", req.Tools)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("LLM request was not captured")
	}
}

// TestRunner_DefaultRuntimeAdvertisesSymbolContextAndCanUseIt pins the
// symbol discovery path end-to-end: the default runtime must expose
// symbol_context and the model must be able to call it and answer from
// the compact result.
func TestRunner_DefaultRuntimeAdvertisesSymbolContextAndCanUseIt(t *testing.T) {
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
	var calls atomic.Int32
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
		flusher := w.(http.Flusher)
		idx := int(calls.Add(1)) - 1
		payloads := [][]string{
			{
				`{"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"s1","type":"function","function":{"name":"symbol_context","arguments":""}}]},"finish_reason":null}]}`,
				`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"query\":\"SymbolContextToolName\",\"path\":\"internal/agent\",\"max_results\":3}"}}]},"finish_reason":null}]}`,
				`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
				`[DONE]`,
			},
			{
				`{"choices":[{"delta":{"role":"assistant","content":"symbol_context found internal/agent/symbol_context.go, so the built-in symbol discovery path is available."},"finish_reason":"stop"}]}`,
				`[DONE]`,
			},
		}
		chosen := payloads[len(payloads)-1]
		if idx < len(payloads) {
			chosen = payloads[idx]
		}
		for _, p := range chosen {
			_, _ = w.Write([]byte("data: " + p + "\n\n"))
			flusher.Flush()
		}
	}))
	t.Cleanup(srv.Close)

	scenario := Scenario{
		Name:        "default_runtime_symbol_context",
		Description: "default eval runtime advertises symbol_context and can use it for symbol discovery",
		Prompt:      "find the symbol_context implementation in this workspace and answer from the result",
		Setup: func(workspaceDir string) error {
			if err := os.MkdirAll(filepath.Join(workspaceDir, "internal", "agent"), 0o755); err != nil {
				return err
			}
			return os.WriteFile(filepath.Join(workspaceDir, "internal", "agent", "symbol_context.go"), []byte("package agent\n\nconst SymbolContextToolName = \"symbol_context\"\n"), 0o644)
		},
		MaxTurnSteps: 4,
		Checks: []Check{
			TurnEndedCleanly(),
			ToolCalled("symbol_context", func(args map[string]any) bool {
				return args["query"] == "SymbolContextToolName" && args["path"] == "internal/agent"
			}),
			FinalTextContains("symbol_context found internal/agent/symbol_context.go"),
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
		t.Fatalf("expected all checks to pass; failed: %v", out.FailedChecks())
	}
	select {
	case req := <-requests:
		foundTool := false
		for _, tool := range req.Tools {
			if tool.Function.Name == "symbol_context" {
				foundTool = true
			}
		}
		if !foundTool {
			t.Fatalf("default runtime should advertise symbol_context; tools=%+v", req.Tools)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("LLM request was not captured")
	}
}

// TestRunner_DefaultRuntimeCanUseSymbolContextForCapabilityDiscovery pins a
// more natural lookup: the model only knows the feature description, not the
// exact file, so it should use symbol_context with a general query and answer
// from the declaration.
func TestRunner_DefaultRuntimeCanUseSymbolContextForCapabilityDiscovery(t *testing.T) {
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
	var calls atomic.Int32
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
		flusher := w.(http.Flusher)
		idx := int(calls.Add(1)) - 1
		payloads := [][]string{
			{
				`{"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"s1","type":"function","function":{"name":"symbol_context","arguments":""}}]},"finish_reason":null}]}`,
				`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"query\":\"runtime capabilities\",\"max_results\":3}"}}]},"finish_reason":null}]}`,
				`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
				`[DONE]`,
			},
			{
				`{"choices":[{"delta":{"role":"assistant","content":"symbol_context found cmd/affentctl/common.go, where resolveRuntimeCapabilities is defined."},"finish_reason":"stop"}]}`,
				`[DONE]`,
			},
		}
		chosen := payloads[len(payloads)-1]
		if idx < len(payloads) {
			chosen = payloads[idx]
		}
		for _, p := range chosen {
			_, _ = w.Write([]byte("data: " + p + "\n\n"))
			flusher.Flush()
		}
	}))
	t.Cleanup(srv.Close)

	scenario := Scenario{
		Name:        "default_runtime_symbol_context_runtime_capabilities",
		Description: "default runtime uses symbol_context to resolve capability-related declarations from a general description",
		Prompt:      "find where runtime capabilities are resolved in this workspace and answer from the declaration",
		Setup: func(workspaceDir string) error {
			if err := os.MkdirAll(filepath.Join(workspaceDir, "cmd", "affentctl"), 0o755); err != nil {
				return err
			}
			return os.WriteFile(filepath.Join(workspaceDir, "cmd", "affentctl", "common.go"), []byte(`package main

type runtimeCapabilities struct {
	SymbolContext bool
}

func resolveRuntimeCapabilities() runtimeCapabilities {
	return runtimeCapabilities{SymbolContext: true}
}
`), 0o644)
		},
		MaxTurnSteps: 4,
		Checks: []Check{
			TurnEndedCleanly(),
			ToolCalled("symbol_context", func(args map[string]any) bool {
				_, hasPath := args["path"]
				return args["query"] == "runtime capabilities" && !hasPath
			}),
			FinalTextContains("resolveRuntimeCapabilities"),
			FinalTextContains("cmd/affentctl/common.go"),
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
		t.Fatalf("expected all checks to pass; failed: %v", out.FailedChecks())
	}
	select {
	case req := <-requests:
		foundTool := false
		for _, tool := range req.Tools {
			if tool.Function.Name == "symbol_context" {
				foundTool = true
			}
		}
		if !foundTool {
			t.Fatalf("default runtime should advertise symbol_context; tools=%+v", req.Tools)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("LLM request was not captured")
	}
}

// TestRunner_DefaultRuntimeCanUseSymbolContextThenReadFile pins the real
// workflow for a weak-to-strong lookup: symbol_context narrows the file, then
// read_file confirms the exact default value before answering.
func TestRunner_DefaultRuntimeCanUseSymbolContextThenReadFile(t *testing.T) {
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
	var calls atomic.Int32
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
		flusher := w.(http.Flusher)
		idx := int(calls.Add(1)) - 1
		payloads := [][]string{
			{
				`{"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"s1","type":"function","function":{"name":"symbol_context","arguments":""}}]},"finish_reason":null}]}`,
				`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"query\":\"runtime capabilities\",\"max_results\":3}"}}]},"finish_reason":null}]}`,
				`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
				`[DONE]`,
			},
			{
				`{"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"r1","type":"function","function":{"name":"read_file","arguments":""}}]},"finish_reason":null}]}`,
				`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"path\":\"cmd/affentctl/common.go\"}"}}]},"finish_reason":null}]}`,
				`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
				`[DONE]`,
			},
			{
				`{"choices":[{"delta":{"role":"assistant","content":"resolveRuntimeCapabilities sets SymbolContext to true by default in cmd/affentctl/common.go."},"finish_reason":"stop"}]}`,
				`[DONE]`,
			},
		}
		chosen := payloads[len(payloads)-1]
		if idx < len(payloads) {
			chosen = payloads[idx]
		}
		for _, p := range chosen {
			_, _ = w.Write([]byte("data: " + p + "\n\n"))
			flusher.Flush()
		}
	}))
	t.Cleanup(srv.Close)

	scenario := Scenario{
		Name:        "default_runtime_symbol_context_then_read_file",
		Description: "default runtime uses symbol_context to narrow a file and then read_file to confirm the default capability value",
		Prompt:      "find where runtime capabilities are resolved, then inspect the file and tell me whether SymbolContext is enabled by default",
		Setup: func(workspaceDir string) error {
			if err := os.MkdirAll(filepath.Join(workspaceDir, "cmd", "affentctl"), 0o755); err != nil {
				return err
			}
			return os.WriteFile(filepath.Join(workspaceDir, "cmd", "affentctl", "common.go"), []byte(`package main

type runtimeCapabilities struct {
	SymbolContext bool
}

func resolveRuntimeCapabilities() runtimeCapabilities {
	return runtimeCapabilities{SymbolContext: true}
}
`), 0o644)
		},
		MaxTurnSteps: 5,
		Checks: []Check{
			TurnEndedCleanly(),
			ToolCalled("symbol_context", func(args map[string]any) bool {
				_, hasPath := args["path"]
				return args["query"] == "runtime capabilities" && !hasPath
			}),
			ToolCalled("read_file", func(args map[string]any) bool {
				return args["path"] == "cmd/affentctl/common.go"
			}),
			ToolCalledBefore("symbol_context", "read_file"),
			FinalTextContains("resolveRuntimeCapabilities sets SymbolContext to true by default"),
			FinalTextContains("cmd/affentctl/common.go"),
		},
	}

	runner := &Runner{
		LLM:            agent.NewLLMClient(srv.URL, "", "fake-model"),
		MaxTurnSteps:   5,
		PerCallTimeout: 5 * time.Second,
		RunTimeout:     20 * time.Second,
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
		foundTool := false
		for _, tool := range req.Tools {
			if tool.Function.Name == "symbol_context" {
				foundTool = true
			}
		}
		if !foundTool {
			t.Fatalf("default runtime should advertise symbol_context; tools=%+v", req.Tools)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("LLM request was not captured")
	}
}

// TestRunner_DefaultRuntimeCanUseFileContext pins the long-file path:
// file_context compresses a noisy file, then the model answers from the
// compact result without needing a full read_file dump.
func TestRunner_DefaultRuntimeCanUseFileContext(t *testing.T) {
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
	var calls atomic.Int32
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
		flusher := w.(http.Flusher)
		idx := int(calls.Add(1)) - 1
		payloads := [][]string{
			{
				`{"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"f1","type":"function","function":{"name":"file_context","arguments":""}}]},"finish_reason":null}]}`,
				`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"query\":\"file_context\",\"path\":\"internal/agent/focusedtask.go\",\"max_bytes\":1200,\"context_lines\":3,\"max_matches\":2}"}}]},"finish_reason":null}]}`,
				`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
				`[DONE]`,
			},
			{
				`{"choices":[{"delta":{"role":"assistant","content":"file_context before read_file when the target file is long or noisy, and the compact result points at internal/agent/focusedtask.go."},"finish_reason":"stop"}]}`,
				`[DONE]`,
			},
		}
		chosen := payloads[len(payloads)-1]
		if idx < len(payloads) {
			chosen = payloads[idx]
		}
		for _, p := range chosen {
			_, _ = w.Write([]byte("data: " + p + "\n\n"))
			flusher.Flush()
		}
	}))
	t.Cleanup(srv.Close)

	scenario := Scenario{
		Name:        "default_runtime_file_context",
		Description: "default runtime uses file_context to compress a long file before answering",
		Prompt:      "inspect the long focused-task guidance file and tell me whether it says to use file_context before read_file on large files",
		Setup: func(workspaceDir string) error {
			if err := os.MkdirAll(filepath.Join(workspaceDir, "internal", "agent"), 0o755); err != nil {
				return err
			}
			var b strings.Builder
			b.WriteString("package agent\n\n")
			for i := 1; i <= 80; i++ {
				fmt.Fprintf(&b, "// filler line %02d\n", i)
			}
			b.WriteString("// Use file_context before read_file when the target file is long or noisy.\n")
			b.WriteString("func guide() {}\n")
			return os.WriteFile(filepath.Join(workspaceDir, "internal", "agent", "focusedtask.go"), []byte(b.String()), 0o644)
		},
		MaxTurnSteps: 4,
		Checks: []Check{
			TurnEndedCleanly(),
			MaxToolCalls(1),
			ToolCalled("file_context", func(args map[string]any) bool {
				return args["query"] == "file_context" && args["path"] == "internal/agent/focusedtask.go"
			}),
			FinalTextContains("file_context before read_file"),
			FinalTextContains("internal/agent/focusedtask.go"),
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
		t.Fatalf("expected all checks to pass; failed: %v", out.FailedChecks())
	}
	select {
	case req := <-requests:
		foundTool := false
		for _, tool := range req.Tools {
			if tool.Function.Name == "file_context" {
				foundTool = true
			}
		}
		if !foundTool {
			t.Fatalf("default runtime should advertise file_context; tools=%+v", req.Tools)
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

// TestRunner_EndToEnd_FocusedTaskWebExtractDelegation mirrors the real
// page-level isolation shape: the parent delegates a bounded web page read
// to run_task(web_extract), the child reads the page with web_fetch, and the
// parent answers from the structured report without doing its own direct page
// inspection.
func TestRunner_EndToEnd_FocusedTaskWebExtractDelegation(t *testing.T) {
	parentTurn1 := []string{
		`{"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"p1","type":"function","function":{"name":"run_task","arguments":"{\"task_type\":\"web_extract\",\"objective\":\"inspect the Affine page and extract the market snapshot\"}"}}]},"finish_reason":"tool_calls"}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`[DONE]`,
	}
	childTurn1 := []string{
		`{"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"c1","type":"function","function":{"name":"web_fetch","arguments":"{\"url\":\"https://example.com/affine\"}"}}]},"finish_reason":"tool_calls"}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`[DONE]`,
	}
	childTurn2 := []string{
		`{"choices":[{"delta":{"role":"assistant","content":"{\"task_type\":\"web_extract\",\"ok\":true,\"summary\":\"extracted the market snapshot from the page\",\"findings\":[{\"claim\":\"Affine is SN120 on the inspected page\",\"evidence\":\"page row shows Affine SN120 and the market snapshot fields\",\"source\":\"https://example.com/affine\"},{\"claim\":\"Affine market snapshot is current enough for trend analysis\",\"evidence\":\"page text includes price and market cap fields for the current snapshot\",\"source\":\"https://example.com/affine\"}]}"},"finish_reason":"stop"}]}`,
		`[DONE]`,
	}
	parentTurn2 := []string{
		`{"choices":[{"delta":{"role":"assistant","content":"The focused task extracted the Affine market snapshot from the page and reported the evidence without the parent re-reading the source. Final answer: Affine is SN120 and the page included a current market snapshot suitable for trend analysis."},"finish_reason":"stop"}]}`,
		`[DONE]`,
	}
	srv := newScriptedLLM(t, [][]string{parentTurn1, childTurn1, childTurn2, parentTurn2})
	llmClient := agent.NewLLMClient(srv.URL, "", "fake-model")
	webFetch := agent.Tool{
		Name:        "web_fetch",
		Description: "Test-only fetch stand-in for focused web extraction.",
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
			if p.URL != "https://example.com/affine" {
				return "", fmt.Errorf("unexpected test URL %q", p.URL)
			}
			return "Affine SN120\nPrice: $17.62\nMarket cap: $56.3M\n24h change: -2.2%\n", nil
		},
	}

	scenario := Scenario{
		Name:         "focused_task_web_extract_delegation",
		Description:  "parent delegates page-level web extraction to run_task and answers from the structured report",
		Prompt:       "inspect the Affine page and extract the market snapshot. please use run_task so the page reading stays isolated.",
		MaxTurnSteps: 4,
		Checks: []Check{
			TurnEndedCleanly(),
			ToolCalledAtLeast("run_task", 1),
			ToolArgContainsAtLeast("run_task", "task_type", "web_extract", 1),
			FocusedTaskCalledAtLeast("web_extract", 1),
			ToolNotCalledAfter("run_task", []string{"read_file", "list_files", "shell", "edit_file", "write_file"}),
			MaxToolCallsAfter("run_task", 0),
			FinalTextContains("SN120"),
			FinalTextContains("market snapshot"),
		},
	}

	runner := &Runner{
		LLM:            llmClient,
		MaxTurnSteps:   4,
		PerCallTimeout: 5 * time.Second,
		RunTimeout:     20 * time.Second,
		Log:            zerolog.Nop(),
		BuildRegistry: func(ctx context.Context, workspaceDir string, exec executor.Executor) (*agent.Registry, error) {
			reg, err := defaultBuildRegistry(ctx, workspaceDir, exec)
			if err != nil {
				return nil, err
			}
			agent.RegisterFocusedTasks(reg, agent.FocusedTaskDeps{
				LLM:              llmClient,
				HostWorkspaceDir: workspaceDir,
				TranscriptDir:    filepath.Join(workspaceDir, "focused-tasks"),
				Log:              zerolog.Nop(),
				RegisterWebTools: func(ctx context.Context, reg *agent.Registry) (func(), error) {
					reg.Add(&webFetch)
					return nil, nil
				},
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
		for i, c := range out.Trace.Tools {
			t.Logf("  trace[%d]: tool=%s delegation=%+v args=%+v result=%s", i, c.Tool, c.Delegation, c.Args, c.Result)
		}
	}
	if len(out.Trace.Tools) == 0 || out.Trace.Tools[0].Tool != "run_task" {
		t.Fatalf("expected parent to delegate via run_task; got %+v", out.Trace.Tools)
	}
	if !strings.Contains(out.Trace.Tools[0].Result, `"task_type":"web_extract"`) || !strings.Contains(out.Trace.Tools[0].Result, `"findings"`) {
		t.Fatalf("run_task result should be compact web_extract JSON; got %q", out.Trace.Tools[0].Result)
	}
	for _, raw := range []string{"Price: $17.62", "Market cap: $56.3M"} {
		if strings.Contains(out.Trace.Tools[0].Result, raw) {
			t.Fatalf("parent trace should not contain raw page text %q: %q", raw, out.Trace.Tools[0].Result)
		}
	}
}

// TestRunner_EndToEnd_FocusedTaskWebExtractPriceDisambiguation mirrors the
// page-level isolation path on a price-heavy page: the child extracts the
// visible values into a compact report, and the parent must preserve the
// TAO top-bar price and the Affine subnet/body price as separate facts.
func TestRunner_EndToEnd_FocusedTaskWebExtractPriceDisambiguation(t *testing.T) {
	parentTurn1 := []string{
		`{"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"p1","type":"function","function":{"name":"run_task","arguments":"{\"task_type\":\"web_extract\",\"objective\":\"inspect the TAO.app subnet page and extract the market snapshot with the price values kept separate\"}"}}]},"finish_reason":"tool_calls"}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`[DONE]`,
	}
	childTurn1 := []string{
		`{"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"c1","type":"function","function":{"name":"web_fetch","arguments":"{\"url\":\"https://www.tao.app/subnets/120?active_tab=about\"}"}}]},"finish_reason":"tool_calls"}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`[DONE]`,
	}
	childTurn2 := []string{
		`{"choices":[{"delta":{"role":"assistant","content":"{\"task_type\":\"web_extract\",\"ok\":true,\"summary\":\"extracted the subnet page snapshot\",\"findings\":[{\"claim\":\"The TAO.app top bar shows TAO Price $277.32\",\"evidence\":\"the chrome/top bar price is separate from the subnet body\",\"source\":\"https://www.tao.app/subnets/120?active_tab=about\"},{\"claim\":\"The Affine SN120 subnet body shows Price 0.06342 T\",\"evidence\":\"the subnet table/body row lists the subnet price in T\",\"source\":\"https://www.tao.app/subnets/120?active_tab=about\"},{\"claim\":\"The subnet page also shows Market Cap 201.04K T and FDV 1.32M T\",\"evidence\":\"those values are shown alongside the subnet body price\",\"source\":\"https://www.tao.app/subnets/120?active_tab=about\"}]}"},"finish_reason":"stop"}]}`,
		`[DONE]`,
	}
	parentTurn2 := []string{
		`{"choices":[{"delta":{"role":"assistant","content":"The TAO.app snapshot keeps two price-like values separate: the top bar shows TAO Price $277.32, while the Affine SN120 subnet body shows Price 0.06342 T with Market Cap 201.04K T and FDV 1.32M T. The parent kept the extracted facts compact instead of re-reading the page dump."},"finish_reason":"stop"}]}`,
		`[DONE]`,
	}
	srv := newScriptedLLM(t, [][]string{parentTurn1, childTurn1, childTurn2, parentTurn2})
	llmClient := agent.NewLLMClient(srv.URL, "", "fake-model")
	webFetch := agent.Tool{
		Name:        "web_fetch",
		Description: "Test-only fetch stand-in for focused web extraction.",
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
			if p.URL != "https://www.tao.app/subnets/120?active_tab=about" {
				return "", fmt.Errorf("unexpected test URL %q", p.URL)
			}
			return "SourceAccess: browser_rendered_url=https://www.tao.app/subnets/120?active_tab=about; snapshot_id=14; page_text_below=verified_page_evidence\nTAO Price $277.32\nAffine SN120\nPrice 0.06342 T\nMarket Cap 201.04K T\nFDV 1.32M T\n", nil
		},
	}

	scenario := Scenario{
		Name:         "focused_task_web_extract_price_disambiguation",
		Description:  "parent delegates price-heavy page reading to run_task(web_extract) and keeps top-bar and subnet price facts separate",
		Prompt:       "inspect the TAO.app subnet page and extract the market snapshot. keep the price values separate and use run_task so the page reading stays isolated.",
		MaxTurnSteps: 4,
		Checks: []Check{
			TurnEndedCleanly(),
			ToolCalledAtLeast("run_task", 1),
			ToolArgContainsAtLeast("run_task", "task_type", "web_extract", 1),
			FocusedTaskCalledAtLeast("web_extract", 1),
			ToolNotCalledAfter("run_task", []string{"read_file", "list_files", "shell", "edit_file", "write_file"}),
			MaxToolCallsAfter("run_task", 0),
			FinalTextContains("TAO Price $277.32"),
			FinalTextContains("Price 0.06342 T"),
			FinalTextContains("Market Cap 201.04K T"),
			FinalTextContains("FDV 1.32M T"),
		},
	}

	runner := &Runner{
		LLM:            llmClient,
		MaxTurnSteps:   4,
		PerCallTimeout: 5 * time.Second,
		RunTimeout:     20 * time.Second,
		Log:            zerolog.Nop(),
		BuildRegistry: func(ctx context.Context, workspaceDir string, exec executor.Executor) (*agent.Registry, error) {
			reg, err := defaultBuildRegistry(ctx, workspaceDir, exec)
			if err != nil {
				return nil, err
			}
			agent.RegisterFocusedTasks(reg, agent.FocusedTaskDeps{
				LLM:              llmClient,
				HostWorkspaceDir: workspaceDir,
				TranscriptDir:    filepath.Join(workspaceDir, "focused-tasks"),
				Log:              zerolog.Nop(),
				RegisterWebTools: func(ctx context.Context, reg *agent.Registry) (func(), error) {
					reg.Add(&webFetch)
					return nil, nil
				},
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
		for i, c := range out.Trace.Tools {
			t.Logf("  trace[%d]: tool=%s delegation=%+v args=%+v result=%s", i, c.Tool, c.Delegation, c.Args, c.Result)
		}
	}
	if len(out.Trace.Tools) == 0 || out.Trace.Tools[0].Tool != "run_task" {
		t.Fatalf("expected parent to delegate via run_task; got %+v", out.Trace.Tools)
	}
	if !strings.Contains(out.Trace.Tools[0].Result, `"task_type":"web_extract"`) || !strings.Contains(out.Trace.Tools[0].Result, `"findings"`) {
		t.Fatalf("run_task result should be compact web_extract JSON; got %q", out.Trace.Tools[0].Result)
	}
	for _, raw := range []string{
		"SourceAccess: browser_rendered_url=",
		"TAO Price $277.32\nAffine SN120\nPrice 0.06342 T\nMarket Cap 201.04K T\nFDV 1.32M T",
	} {
		if strings.Contains(out.Trace.Tools[0].Result, raw) {
			t.Fatalf("parent trace should not contain raw page dump %q: %q", raw, out.Trace.Tools[0].Result)
		}
	}
}

// TestRunner_EndToEnd_FocusedTaskExploreRepoSearchDelegation mirrors
// the workspace-discovery shape: the parent delegates code discovery
// to run_task(explore), the child uses repo_search instead of a broad
// shell sweep, and the parent answers from the compressed report.
func TestRunner_EndToEnd_FocusedTaskExploreRepoSearchDelegation(t *testing.T) {
	parentTurn1 := []string{
		`{"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"p1","type":"function","function":{"name":"run_task","arguments":"{\"task_type\":\"explore\",\"objective\":\"find the repo_search implementation in this workspace\"}"}}]},"finish_reason":"tool_calls"}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`[DONE]`,
	}
	childTurn1 := []string{
		`{"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"c1","type":"function","function":{"name":"repo_search","arguments":"{\"query\":\"repoSearchTool\",\"path\":\"internal/agent\",\"max_results\":3}"}}]},"finish_reason":"tool_calls"}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`[DONE]`,
	}
	childTurn2 := []string{
		`{"choices":[{"delta":{"role":"assistant","content":"{\"task_type\":\"explore\",\"ok\":true,\"summary\":\"located the repo_search implementation\",\"findings\":[{\"claim\":\"repo_search is implemented in internal/agent/repo_search.go\",\"evidence\":\"repo_search found the implementation file and the repoSearchTool symbol\",\"source\":\"internal/agent/repo_search.go\"}]}"},"finish_reason":"stop"}]}`,
		`[DONE]`,
	}
	parentTurn2 := []string{
		`{"choices":[{"delta":{"role":"assistant","content":"The focused task found the repo_search implementation in internal/agent/repo_search.go and returned a compact report."},"finish_reason":"stop"}]}`,
		`[DONE]`,
	}
	srv := newScriptedLLM(t, [][]string{parentTurn1, childTurn1, childTurn2, parentTurn2})
	llmClient := agent.NewLLMClient(srv.URL, "", "fake-model")

	scenario := Scenario{
		Name:        "focused_task_explore_repo_search_delegation",
		Description: "parent delegates workspace code discovery to run_task(explore) and child uses repo_search",
		Prompt:      "find the repo_search implementation in this workspace. please use run_task so the lookup stays isolated.",
		Setup: func(workspaceDir string) error {
			if err := os.MkdirAll(filepath.Join(workspaceDir, "internal", "agent"), 0o755); err != nil {
				return err
			}
			return os.WriteFile(filepath.Join(workspaceDir, "internal", "agent", "repo_search.go"), []byte("package agent\n\nfunc repoSearchTool() {}\n"), 0o644)
		},
		MaxTurnSteps: 4,
		Checks: []Check{
			TurnEndedCleanly(),
			ToolCalledAtLeast("run_task", 1),
			ToolArgContainsAtLeast("run_task", "task_type", "explore", 1),
			FocusedTaskCalledAtLeast("explore", 1),
			ToolResultContains("run_task", "repo_search found the implementation file"),
			FinalTextContains("repo_search implementation in internal/agent/repo_search.go"),
		},
	}

	runner := &Runner{
		LLM:            llmClient,
		MaxTurnSteps:   4,
		PerCallTimeout: 5 * time.Second,
		RunTimeout:     20 * time.Second,
		Log:            zerolog.Nop(),
		BuildRegistry: func(ctx context.Context, workspaceDir string, exec executor.Executor) (*agent.Registry, error) {
			reg, err := defaultBuildRegistry(ctx, workspaceDir, exec)
			if err != nil {
				return nil, err
			}
			agent.RegisterFocusedTasks(reg, agent.FocusedTaskDeps{
				LLM:              llmClient,
				Executor:         exec,
				HostWorkspaceDir: workspaceDir,
				Log:              zerolog.Nop(),
			})
			return reg, nil
		},
	}

	out, err := runner.Run(context.Background(), scenario)
	if err != nil {
		t.Fatalf("Runner.Run: %v", err)
	}
	if !out.Pass {
		t.Fatalf("expected all checks to pass; failed: %v", out.FailedChecks())
	}
	if len(out.Trace.Tools) == 0 || out.Trace.Tools[0].Tool != "run_task" {
		t.Fatalf("expected parent to delegate via run_task; got %+v", out.Trace.Tools)
	}
	if !strings.Contains(out.Trace.Tools[0].Result, `"task_type":"explore"`) || !strings.Contains(out.Trace.Tools[0].Result, "repo_search found the implementation file") {
		t.Fatalf("run_task result should include the repo_search evidence; got %q", out.Trace.Tools[0].Result)
	}
}

// TestRunner_EndToEnd_FocusedTaskRecallMemoryAndSessionSearch mirrors the
// recall workflow: the parent delegates to run_task(recall), the child
// searches durable memory first, then prior sessions, and the parent
// answers from the compact structured report instead of redoing the lookup
// itself. This is the narrowest end-to-end proof that recall is a bounded
// isolation surface rather than ad hoc prompt glue.
func TestRunner_EndToEnd_FocusedTaskRecallMemoryAndSessionSearch(t *testing.T) {
	parentTurn1 := []string{
		`{"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"p1","type":"function","function":{"name":"run_task","arguments":"{\"task_type\":\"recall\",\"objective\":\"find the user's response preference from memory and prior session history\"}"}}]},"finish_reason":"tool_calls"}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`[DONE]`,
	}
	childTurn1 := []string{
		`{"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"c1","type":"function","function":{"name":"memory","arguments":"{\"action\":\"search\",\"topic\":\"prefs\",\"query\":\"terse responses no bullet lists\",\"top_k\":3}"}}]},"finish_reason":"tool_calls"}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`[DONE]`,
	}
	childTurn2 := []string{
		`{"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"c2","type":"function","function":{"name":"session_search","arguments":"{\"query\":\"terse responses no bullet lists\",\"top_k\":3,\"max_per_session\":2}"}}]},"finish_reason":"tool_calls"}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`[DONE]`,
	}
	recallReport := fmt.Sprintf("```json\n%s\n```", `{"task_type":"recall","ok":true,"summary":"recalled the user's terse-response preference from memory and a prior session","findings":[{"claim":"the user prefers terse responses and no bullet lists","evidence":"memory topic prefs says the user prefers terse responses and no bullet lists","source":"memory:prefs","confidence":"high"},{"claim":"a past session repeats the same terse preference","evidence":"the prior session says to keep replies terse and skip bullet lists","source":"past-style","confidence":"medium"}],"warnings":[],"not_found":[],"suggested_next":[]}`)
	childTurn3 := []string{
		fmt.Sprintf(`{"choices":[{"delta":{"role":"assistant","content":%q},"finish_reason":"stop"}]}`, recallReport),
		`[DONE]`,
	}
	parentTurn2 := []string{
		`{"choices":[{"delta":{"role":"assistant","content":"The user prefers terse responses and no bullet lists, confirmed by durable memory and a prior session. I kept the recall isolated instead of re-searching in the parent turn."},"finish_reason":"stop"}]}`,
		`[DONE]`,
	}
	parentTurn3 := []string{
		`{"choices":[{"delta":{"role":"assistant","content":"The user prefers terse responses and no bullet lists, confirmed by durable memory and a prior session. I kept the recall isolated instead of re-searching in the parent turn."},"finish_reason":"stop"}]}`,
		`[DONE]`,
	}
	srv := newScriptedLLM(t, [][]string{parentTurn1, childTurn1, childTurn2, childTurn3, parentTurn2, parentTurn3})
	llmClient := agent.NewLLMClient(srv.URL, "", "fake-model")

	scenario := Scenario{
		Name:        "focused_task_recall_memory_session_search",
		Description: "parent delegates recall to run_task and child uses memory plus session_search before answering",
		Prompt:      "find the user's response preference from memory and prior session history. use run_task so the recall stays isolated.",
		Setup: func(workspaceDir string) error {
			memStore := memory.NewFileMemoryStore(workspaceDir)
			if _, err := memStore.Add(memory.TargetMemory, "prefs", "The user prefers terse responses and no bullet lists."); err != nil {
				return err
			}
			sessionsDir := filepath.Join(workspaceDir, ".affentctl")
			writeRecallSessionLog(t, sessionsDir, "past-style",
				agent.ChatMessage{Role: "user", Content: "please keep it terse and no bullet lists"},
				agent.ChatMessage{Role: "assistant", Content: "Got it. Terse responses only."},
			)
			writeRecallSessionLog(t, sessionsDir, "distractor",
				agent.ChatMessage{Role: "user", Content: "please explain everything in detail"},
				agent.ChatMessage{Role: "assistant", Content: "Sure, I will be verbose."},
			)
			return nil
		},
		MaxTurnSteps: 6,
		Checks: []Check{
			TurnEndedCleanly(),
			ToolCalledAtLeast("run_task", 1),
			ToolArgContainsAtLeast("run_task", "task_type", "recall", 1),
			FocusedTaskCalledAtLeast("recall", 1),
			ToolResultContains("run_task", "\"tool\":\"memory\""),
			ToolResultContains("run_task", "\"tool\":\"session_search\""),
			ToolResultContains("run_task", "terse responses and no bullet lists"),
			FinalTextContains("terse responses"),
			FinalTextContains("no bullet lists"),
		},
	}

	runner := &Runner{
		LLM:            llmClient,
		MaxTurnSteps:   6,
		PerCallTimeout: 5 * time.Second,
		RunTimeout:     20 * time.Second,
		Log:            zerolog.Nop(),
		BuildRegistry: func(ctx context.Context, workspaceDir string, exec executor.Executor) (*agent.Registry, error) {
			reg, err := defaultBuildRegistry(ctx, workspaceDir, exec)
			if err != nil {
				return nil, err
			}
			agent.RegisterFocusedTasks(reg, agent.FocusedTaskDeps{
				LLM:              llmClient,
				Executor:         exec,
				HostWorkspaceDir: workspaceDir,
				Memory:           memory.NewFileMemoryStore(workspaceDir),
				SessionsDir:      filepath.Join(workspaceDir, ".affentctl"),
				ParentSessionID:  "recall-parent",
				TranscriptDir:    filepath.Join(workspaceDir, "focused-tasks"),
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
		for i, c := range out.Trace.Tools {
			t.Logf("  trace[%d]: tool=%s delegation=%+v args=%+v result=%s", i, c.Tool, c.Delegation, c.Args, c.Result)
		}
	}
	if len(out.Trace.Tools) == 0 || out.Trace.Tools[0].Tool != "run_task" {
		t.Fatalf("expected parent to delegate via run_task; got %+v", out.Trace.Tools)
	}
	if !strings.Contains(out.Trace.Tools[0].Result, `"task_type":"recall"`) || !strings.Contains(out.Trace.Tools[0].Result, "memory:prefs") {
		t.Fatalf("run_task result should include the recall evidence; got %q", out.Trace.Tools[0].Result)
	}
	for _, raw := range []string{"memory search"} {
		if strings.Contains(out.Trace.Tools[0].Result, raw) {
			t.Fatalf("parent trace should not contain the raw recall workflow %q: %q", raw, out.Trace.Tools[0].Result)
		}
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
			ToolFailureKindAtLeast("loop_guard_repeated_call", 1),
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
	if third.FailureKind != "loop_guard_repeated_call" {
		t.Errorf("3rd call FailureKind = %q, want loop_guard_repeated_call", third.FailureKind)
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

// TestRunner_EndToEnd_WebSnapshotPriceDisambiguation locks in the browser path
// for pages that expose both a top-bar market price and a subnet/body price on
// the same rendered page.
func TestRunner_EndToEnd_WebSnapshotPriceDisambiguation(t *testing.T) {
	const snapshot = `SourceAccess: browser_rendered_url=https://www.tao.app/subnets/120?active_tab=about; snapshot_id=14; page_text_below=verified_page_evidence
URL: https://www.tao.app/subnets/120?active_tab=about
TITLE: SN120 - Affine | TAO.app | Your Gateway to Bittensor
INTERACTIVE ELEMENTS:
[1] button "About"
[2] button "Metrics"

PAGE TEXT:
[text span] TAO Price $ 277.32 -1.02 % 1D Vol $ 168.66M -39 % MC $ 3.03B FDV $ 5.82B Circ. Supply 10.94M
[text div] Price 0.06342 T/ d L: 0.060 T H: 0.086 T Market Cap 201.04K T FDV 1.32M T`

	browserNavigate := agent.Tool{
		Name:        "browser_navigate",
		Description: "Test-only browser navigate stand-in: returns a deterministic TAO.app snapshot.",
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
	browserFind := agent.Tool{
		Name:        "browser_find",
		Description: "Test-only browser find stand-in: returns compact price-like snippets.",
		Schema: json.RawMessage(`{
            "type": "object",
            "required": ["query"],
            "properties": {
                "query": {"type": "string"},
                "max_results": {"type": "integer"}
            }
        }`),
		Execute: func(ctx context.Context, args json.RawMessage) (string, error) {
			return `SourceAccess: browser_rendered_url=https://www.tao.app/subnets/120?active_tab=about; snapshot_id=15; page_text_below=verified_page_evidence
URL: https://www.tao.app/subnets/120?active_tab=about
TITLE: SN120 - Affine | TAO.app | Your Gateway to Bittensor
QUERY: "price market cap fdv volume"
[text span] TAO Price $ 277.32 -1.02 % 1D Vol $ 168.66M -39 % MC $ 3.03B FDV $ 5.82B Circ. Supply 10.94M
[text div] Price 0.06342 T | Market Cap 201.04K T | FDV 1.32M T`, nil
		},
	}

	turn1 := []string{
		`{"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"b1","type":"function","function":{"name":"browser_navigate","arguments":"{\"url\":\"https://www.tao.app/subnets/120?active_tab=about\",\"wait_until\":\"networkidle\"}"}}]},"finish_reason":null}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`[DONE]`,
	}
	turn2 := []string{
		`{"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"f1","type":"function","function":{"name":"browser_find","arguments":"{\"query\":\"price market cap fdv volume\",\"max_results\":5}"}}]},"finish_reason":null}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`[DONE]`,
	}
	turn3 := []string{
		`{"choices":[{"delta":{"role":"assistant","content":"The browser snapshot shows two distinct price-like values on the same TAO.app page. The top bar has TAO Price $277.32, which is the broader TAO market price in the site chrome. The body has the Affine SN120 subnet price Price 0.06342 T, along with Market Cap 201.04K T and FDV 1.32M T. These should be kept separate and not conflated."},"finish_reason":"stop"}]}`,
		`[DONE]`,
	}
	srv := newScriptedLLM(t, [][]string{turn1, turn2, turn3})
	browserArgs := func(tool string, want string) func(map[string]any) bool {
		return func(args map[string]any) bool {
			if tool == "browser_navigate" {
				return args["url"] == want
			}
			if tool == "browser_find" {
				return strings.Contains(fmt.Sprint(args["query"]), want)
			}
			return false
		}
	}

	scenario := Scenario{
		Name:         "web_snapshot_price_disambiguation",
		Description:  "browser snapshot and find should preserve top-bar TAO price and subnet/body price separately",
		Prompt:       "Read the TAO.app page for Affine SN120 and report the different price-like values without conflating them.",
		MaxTurnSteps: 5,
		Checks: []Check{
			TurnEndedCleanly(),
			ToolCalled("browser_navigate", browserArgs("browser_navigate", "https://www.tao.app/subnets/120?active_tab=about")),
			ToolCalled("browser_find", browserArgs("browser_find", "price market cap fdv volume")),
			ToolCalledBeforeMatching("browser_navigate", browserArgs("browser_navigate", "https://www.tao.app/subnets/120?active_tab=about"), "browser_find", browserArgs("browser_find", "price market cap fdv volume")),
			FinalTextContains("TAO Price $277.32"),
			FinalTextContains("Price 0.06342 T"),
			FinalTextContains("Market Cap 201.04K T"),
			FinalTextContains("broader TAO market price"),
			FinalTextContains("Affine SN120 subnet price"),
		},
	}

	runner := &Runner{
		LLM:            agent.NewLLMClient(srv.URL, "", "fake-model"),
		MaxTurnSteps:   5,
		PerCallTimeout: 5 * time.Second,
		RunTimeout:     15 * time.Second,
		Log:            zerolog.Nop(),
		BuildRegistry: func(ctx context.Context, workspaceDir string, exec executor.Executor) (*agent.Registry, error) {
			_ = ctx
			_ = workspaceDir
			_ = exec
			reg := agent.NewRegistry()
			reg.Add(&browserNavigate)
			reg.Add(&browserFind)
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
	if len(out.Trace.Tools) != 2 {
		t.Fatalf("expected navigate + find; got %+v", out.Trace.Tools)
	}
}

func TestRunner_EndToEnd_WebSnapshotNetworkEvidenceFlow(t *testing.T) {
	const snapshot = `SourceAccess: browser_rendered_url=https://taostats.io/subnets/120; snapshot_id=18; page_text_below=verified_page_evidence
URL: https://taostats.io/subnets/120
TITLE: Affine SN120 · TaoStats
PAGE DIAGNOSTICS:
- empty_dynamic_metric_widgets: 2 visible custom metric widget(s) exposed no text value; use browser_network/browser_network_read, API/text/source endpoint, or mark those fields unverified
CAPTURED NETWORK RESPONSES:
- n1 status=200 resource=fetch content_type=application/json url=https://taostats.io/api/subnets/120
Next: use browser_network with a specific metric/entity/API-path query, then browser_network_read on the best ref before citing hidden dashboard values.

PAGE TEXT:
p: Affine SN120
p: Market Cap
p: 24hr Volume`

	browserNavigate := agent.Tool{
		Name:        "browser_navigate",
		Description: "Test-only browser navigate stand-in: returns a dynamic dashboard snapshot with network refs.",
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
	browserNetwork := agent.Tool{
		Name:        "browser_network",
		Description: "Test-only browser network search stand-in.",
		Schema: json.RawMessage(`{
            "type": "object",
            "properties": {
                "query": {"type": "string"},
                "max_results": {"type": "integer"}
            }
        }`),
		Execute: func(ctx context.Context, args json.RawMessage) (string, error) {
			return `BROWSER NETWORK EVIDENCE
query: "market_cap"
MATCHES:
- n1 status=200 resource=fetch content_type=application/json url=https://taostats.io/api/subnets/120
  preview: {"name":"Affine","netuid":120,"market_cap":"201.04K T","price":"0.06342 T"}
Next: call browser_network_read with the most relevant ref before citing values.`, nil
		},
	}
	browserNetworkRead := agent.Tool{
		Name:        "browser_network_read",
		Description: "Test-only browser network read stand-in.",
		Schema: json.RawMessage(`{
            "type": "object",
            "required": ["ref"],
            "properties": {
                "ref": {"type": "string"},
                "max_bytes": {"type": "integer"}
            }
        }`),
		Execute: func(ctx context.Context, args json.RawMessage) (string, error) {
			return `SourceAccess: browser_network_url=https://taostats.io/api/subnets/120; ref=n1; status=200; content_type=application/json; source_method=network_xhr_fetch
BODY_BYTES: 77
{"name":"Affine","netuid":120,"market_cap":"201.04K T","price":"0.06342 T"}`, nil
		},
	}

	turn1 := []string{
		`{"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"b1","type":"function","function":{"name":"browser_navigate","arguments":"{\"url\":\"https://taostats.io/subnets/120\",\"wait_until\":\"networkidle\"}"}}]},"finish_reason":null}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`[DONE]`,
	}
	turn2 := []string{
		`{"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"n1","type":"function","function":{"name":"browser_network","arguments":"{\"query\":\"market_cap\",\"max_results\":5}"}}]},"finish_reason":null}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`[DONE]`,
	}
	turn3 := []string{
		`{"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"r1","type":"function","function":{"name":"browser_network_read","arguments":"{\"ref\":\"n1\",\"max_bytes\":512}"}}]},"finish_reason":null}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`[DONE]`,
	}
	turn4 := []string{
		`{"choices":[{"delta":{"role":"assistant","content":"The rendered TaoStats page showed Affine SN120 but hid the metric values in dynamic widgets, so I used the captured same-site network response before citing numbers. The network evidence at https://taostats.io/api/subnets/120 reports market cap 201.04K T and price 0.06342 T for Affine netuid 120."},"finish_reason":"stop"}]}`,
		`[DONE]`,
	}
	srv := newScriptedLLM(t, [][]string{turn1, turn2, turn3, turn4})
	navigateArgs := func(args map[string]any) bool {
		return args["url"] == "https://taostats.io/subnets/120"
	}
	networkArgs := func(args map[string]any) bool {
		return strings.Contains(fmt.Sprint(args["query"]), "market_cap")
	}
	readArgs := func(args map[string]any) bool {
		return args["ref"] == "n1"
	}

	scenario := Scenario{
		Name:         "web_snapshot_network_evidence_flow",
		Description:  "agent follows browser snapshot network refs and reads XHR evidence before citing hidden dashboard values",
		Prompt:       "Read the TaoStats Affine SN120 page. If rendered metric values are hidden in dynamic widgets, use captured browser network evidence before reporting market cap and price.",
		MaxTurnSteps: 6,
		Checks: []Check{
			TurnEndedCleanly(),
			ToolCalled("browser_navigate", navigateArgs),
			ToolResultContains("browser_navigate", "CAPTURED NETWORK RESPONSES"),
			ToolCalled("browser_network", networkArgs),
			ToolCalled("browser_network_read", readArgs),
			ToolCalledBeforeMatching("browser_navigate", navigateArgs, "browser_network", networkArgs),
			ToolCalledBeforeMatching("browser_network", networkArgs, "browser_network_read", readArgs),
			ToolStatsAtLeast("source_access_results", 2),
			ToolStatsAtLeast("source_access_verified", 1),
			ToolStatsAtLeast("source_access_network", 1),
			FinalTextContains("market cap 201.04K T"),
			FinalTextContains("price 0.06342 T"),
			FinalTextContains("captured same-site network response"),
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
			reg.Add(&browserNavigate)
			reg.Add(&browserNetwork)
			reg.Add(&browserNetworkRead)
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
	if len(out.Trace.Tools) != 3 {
		t.Fatalf("expected navigate + network search + network read; got %+v", out.Trace.Tools)
	}
}

// TestRunner_EndToEnd_Browser404DiscoveryOnly keeps the browser 404 / page
// not found path honest: the model should treat it as a discovery page and use
// the visible navigation links rather than the body as evidence.
func TestRunner_EndToEnd_Browser404DiscoveryOnly(t *testing.T) {
	const snapshot = `SourceAccess: browser_rendered_url=https://example.test/missing; snapshot_id=21; page_text_below=not_found_page_discovery_only; links_in_snapshot=discovered_unverified_until_opened
URL: https://example.test/missing
TITLE: 404 - Page Not Found
SNAPSHOT_ID: 21

INTERACTIVE ELEMENTS:
[1] link "Docs" → /docs
[2] link "Subnets" → /subnets

PAGE TEXT:
[text h1] Page not found
[text p] Use the navigation links to reach /docs or /subnets.`

	const find = `SourceAccess: browser_rendered_url=https://example.test/missing; snapshot_id=22; page_text_below=not_found_page_discovery_only; links_in_snapshot=discovered_unverified_until_opened
URL: https://example.test/missing
TITLE: 404 - Page Not Found
SNAPSHOT_ID: 22
QUERY: "docs"

MATCHES:
[interactive ref=1] link "Docs" → /docs
[text p] Use the navigation links to reach /docs or /subnets.`

	browserNavigate := agent.Tool{
		Name:        "browser_navigate",
		Description: "Test-only browser navigate stand-in: returns a deterministic 404 discovery page.",
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
	browserFind := agent.Tool{
		Name:        "browser_find",
		Description: "Test-only browser find stand-in: returns compact 404 discovery matches.",
		Schema: json.RawMessage(`{
            "type": "object",
            "required": ["query"],
            "properties": {
                "query": {"type": "string"},
                "max_results": {"type": "integer"}
            }
        }`),
		Execute: func(ctx context.Context, args json.RawMessage) (string, error) {
			return find, nil
		},
	}

	turn1 := []string{
		`{"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"b1","type":"function","function":{"name":"browser_navigate","arguments":"{\"url\":\"https://example.test/missing\",\"wait_until\":\"networkidle\"}"}}]},"finish_reason":null}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`[DONE]`,
	}
	turn2 := []string{
		`{"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"f1","type":"function","function":{"name":"browser_find","arguments":"{\"query\":\"docs\",\"max_results\":5}"}}]},"finish_reason":null}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`[DONE]`,
	}
	turn3 := []string{
		`{"choices":[{"delta":{"role":"assistant","content":"This URL is a 404 discovery page, not verified evidence. The browser output shows a Docs link and a Subnets link, so the next step is to follow one of those navigation links rather than rely on the page body. That keeps the missing page discovery-only while still letting me continue to the correct canonical source."},"finish_reason":"stop"}]}`,
		`[DONE]`,
	}
	srv := newScriptedLLM(t, [][]string{turn1, turn2, turn3})

	scenario := Scenario{
		Name:         "browser_404_discovery_only",
		Description:  "browser 404 pages should be treated as discovery-only and followed via visible navigation links",
		Prompt:       "Open the missing page and continue from whatever navigation links it exposes. Do not treat the 404 body as verified evidence.",
		MaxTurnSteps: 5,
		Checks: []Check{
			TurnEndedCleanly(),
			ToolCalled("browser_navigate", func(args map[string]any) bool {
				return args["url"] == "https://example.test/missing"
			}),
			ToolCalled("browser_find", func(args map[string]any) bool {
				return strings.Contains(fmt.Sprint(args["query"]), "docs")
			}),
			ToolCalledBeforeMatching("browser_navigate", func(args map[string]any) bool {
				return args["url"] == "https://example.test/missing"
			}, "browser_find", func(args map[string]any) bool {
				return strings.Contains(fmt.Sprint(args["query"]), "docs")
			}),
			FinalTextContains("404 discovery page"),
			FinalTextContains("not verified evidence"),
			FinalTextContains("Docs link"),
			FinalTextContains("missing page discovery-only"),
		},
	}

	runner := &Runner{
		LLM:            agent.NewLLMClient(srv.URL, "", "fake-model"),
		MaxTurnSteps:   5,
		PerCallTimeout: 5 * time.Second,
		RunTimeout:     15 * time.Second,
		Log:            zerolog.Nop(),
		BuildRegistry: func(ctx context.Context, workspaceDir string, exec executor.Executor) (*agent.Registry, error) {
			_ = ctx
			_ = workspaceDir
			_ = exec
			reg := agent.NewRegistry()
			reg.Add(&browserNavigate)
			reg.Add(&browserFind)
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
	if len(out.Trace.Tools) != 2 {
		t.Fatalf("expected navigate + find; got %+v", out.Trace.Tools)
	}
}

// TestRunner_EndToEnd_ExternalResearchFlow pins the generic research shape
// behind real user requests like "analyze recent trend for X" without baking in
// any specific project, subnet, site, or token. The agent first discovers
// sources, then reads primary/metrics pages while using direct-reader-warning
// social snippets as weak sentiment evidence instead of fetching them.
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
  {"title":"Recent community discussion","url":"https://social.example/search/nimbus","snippet":"Direct-reader warning: do not direct-fetch this social result. Recent positive and critical community posts."}
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
		`{"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"f1","type":"function","function":{"name":"web_fetch","arguments":"{\"url\":\"https://official.example/nimbus/about\"}"}},{"index":1,"id":"f2","type":"function","function":{"name":"web_fetch","arguments":"{\"url\":\"https://metrics.example/nimbus\"}"}}]},"finish_reason":null}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`[DONE]`,
	}
	turn3 := []string{
		`{"choices":[{"delta":{"role":"assistant","content":"Nimbus Protocol is a decentralized compute subnet for model-routing workloads according to the official docs. As of 2026-05-24T12:00:00Z, the metrics source reports price $17.78, market cap $56.7M, 24h change +7.2%, and 24h volume $2.36M. A search snippet marked the social result with a Direct-reader warning, so I did not fetch it; it is only weak sentiment evidence suggesting mixed community reaction. Overall: recent momentum is positive on the metrics, but the outlook should be treated cautiously because social evidence is weak and mixed."},"finish_reason":"stop"}]}`,
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
		Description:  "agent discovers sources, reads primary/metrics pages, and keeps direct-reader-warning social evidence weak",
		Prompt:       "Assess the recent trend for Nimbus Protocol. First identify what it is, then collect current market metrics and recent community sentiment. Be objective and cite evidence types.",
		MaxTurnSteps: 6,
		Checks: []Check{
			TurnEndedCleanly(),
			ToolCalled("web_search", nimbusSearch),
			ToolCalled("web_fetch", fetchURL("https://official.example/nimbus/about")),
			ToolCalled("web_fetch", fetchURL("https://metrics.example/nimbus")),
			ToolNotCalled("web_fetch", fetchURL("https://social.example/search/nimbus")),
			ToolCalledAtLeast("web_fetch", 2),
			ToolResultContains("web_search", "Direct-reader warning"),
			ToolCalledBeforeMatching("web_search", nimbusSearch, "web_fetch", fetchURL("https://official.example/nimbus/about")),
			ToolCalledBeforeMatching("web_search", nimbusSearch, "web_fetch", fetchURL("https://metrics.example/nimbus")),
			ToolNotCalled("shell", nil),
			FinalTextContains("decentralized compute subnet"),
			FinalTextContains("market cap $56.7M"),
			FinalTextContains("+7.2%"),
			FinalTextContains("Direct-reader warning"),
			FinalTextContains("weak sentiment evidence"),
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
	if len(out.Trace.Tools) != 3 {
		t.Fatalf("expected one search and two fetches; got %+v", out.Trace.Tools)
	}
}

func TestRunner_EndToEnd_ExternalResearchSourceHint(t *testing.T) {
	webSearch := agent.Tool{
		Name:        "web_search",
		Description: "Test-only search stand-in: returns a dynamic page plus readable source hints.",
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
			return `1. Zenith Analytics docs
   https://docs.example/zenith
   For AI agents, read https://docs.example/zenith/llms.txt. Metrics endpoint: https://api.example/zenith/metrics.json.
   Source hint: snippet mentions readable endpoint https://docs.example/zenith/llms.txt
   Source hint: snippet mentions readable endpoint https://api.example/zenith/metrics.json

2. Zenith live dashboard
   https://dashboard.example/zenith
   Client-rendered market dashboard that requires JavaScript.
   Direct-reader caution: result appears to be a dynamic or JavaScript-rendered page; prefer an official API/text/source URL before spending a direct page-reading call.

Next: choose the 1-3 most authoritative/current result URLs, prefer official or primary sources, and read them with an available page-reading tool before answering.`, nil
		},
	}
	webFetch := agent.Tool{
		Name:        "web_fetch",
		Description: "Test-only fetch stand-in: returns deterministic source-hint content.",
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
			case "https://docs.example/zenith/llms.txt":
				return "# Zenith Network\n\nUpdated 2026-05-22. Zenith Network is a decentralized analytics subnet for Bittensor.", nil
			case "https://api.example/zenith/metrics.json":
				return `{"as_of":"2026-05-24T12:00:00Z","price_usd":2.41,"market_cap_usd":12400000,"change_24h_pct":5.6}`, nil
			case "https://dashboard.example/zenith":
				return "[dynamic page shell: URL=https://dashboard.example/zenith, Content-Type=\"text/html\", Reason=\"client-rendered app shell\"]\nFailure: kind=dynamic_shell\nNext: use a canonical API/text/source page.", nil
			default:
				return "", fmt.Errorf("unexpected test URL %q", p.URL)
			}
		},
	}

	turn1 := []string{
		`{"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"s1","type":"function","function":{"name":"web_search","arguments":"{\"query\":\"Zenith Network Bittensor subnet market metrics\",\"num_results\":5}"}}]},"finish_reason":null}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`[DONE]`,
	}
	turn2 := []string{
		`{"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"f1","type":"function","function":{"name":"web_fetch","arguments":"{\"url\":\"https://docs.example/zenith/llms.txt\"}"}},{"index":1,"id":"f2","type":"function","function":{"name":"web_fetch","arguments":"{\"url\":\"https://api.example/zenith/metrics.json\"}"}}]},"finish_reason":null}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`[DONE]`,
	}
	turn3 := []string{
		`{"choices":[{"delta":{"role":"assistant","content":"Zenith Network is a decentralized analytics subnet for Bittensor according to the readable docs index. As of 2026-05-24T12:00:00Z, the API source reports price $2.41, market cap $12.4M, and 24h change +5.6%. I followed the Source hint URLs and did not fetch the dynamic dashboard, so dashboard evidence remains unverified."},"finish_reason":"stop"}]}`,
		`[DONE]`,
	}
	srv := newScriptedLLM(t, [][]string{turn1, turn2, turn3})
	searchArgs := func(args map[string]any) bool {
		q, _ := args["query"].(string)
		return strings.Contains(q, "Zenith Network")
	}
	fetchURL := func(url string) func(map[string]any) bool {
		return func(args map[string]any) bool {
			return args["url"] == url
		}
	}

	scenario := Scenario{
		Name:         "external_research_source_hint_followup",
		Description:  "agent follows readable Source hint URLs from search snippets instead of fetching a dynamic dashboard route",
		Prompt:       "Assess current Zenith Network Bittensor subnet metrics. Use readable source/API hints when search exposes them, and do not treat dashboard shell text as evidence.",
		MaxTurnSteps: 6,
		Checks: []Check{
			TurnEndedCleanly(),
			ToolCalled("web_search", searchArgs),
			ToolResultContains("web_search", "Source hint"),
			ToolCalled("web_fetch", fetchURL("https://docs.example/zenith/llms.txt")),
			ToolCalled("web_fetch", fetchURL("https://api.example/zenith/metrics.json")),
			ToolNotCalled("web_fetch", fetchURL("https://dashboard.example/zenith")),
			ToolCalledBeforeMatching("web_search", searchArgs, "web_fetch", fetchURL("https://docs.example/zenith/llms.txt")),
			ToolNotCalled("shell", nil),
			FinalTextContains("decentralized analytics subnet"),
			FinalTextContains("market cap $12.4M"),
			FinalTextContains("Source hint"),
			FinalTextContains("dynamic dashboard"),
			FinalTextContains("unverified"),
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
}

// TestRunner_EndToEnd_ExternalResearchFetchRecovery pins the failure path for
// public web research: a blocked fetch result must be treated as recoverable,
// and the model should switch to another source instead of repeating the same
// failing URL or presenting the blocked page as evidence.
func TestRunner_EndToEnd_ExternalResearchFetchRecovery(t *testing.T) {
	webSearch := agent.Tool{
		Name:        "web_search",
		Description: "Test-only search stand-in: returns primary and fallback source candidates.",
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
  {"title":"Orion Network official docs","url":"https://official.example/orion/about","snippet":"Primary source describing Orion Network as a decentralized storage subnet."},
  {"title":"Orion Network primary metrics","url":"https://blocked.example/orion/metrics","snippet":"Market metrics page that may block automated fetches."},
  {"title":"Orion Network mirror metrics","url":"https://metrics.example/orion","snippet":"Alternative text metrics endpoint with price, market cap, and 24h change."},
  {"title":"Recent community discussion","url":"https://social.example/search/orion","snippet":"Direct-reader warning: do not direct-fetch this social result. Recent positive and critical community posts."}
]`, nil
		},
	}
	webFetch := agent.Tool{
		Name:        "web_fetch",
		Description: "Test-only fetch stand-in: returns deterministic page text and one recoverable failure.",
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
			case "https://official.example/orion/about":
				return "Official docs, updated 2026-05-20: Orion Network is a decentralized storage subnet for encrypted archival workloads.", nil
			case "https://blocked.example/orion/metrics":
				return "", errors.New("web_fetch failed: HTTP 403 Forbidden for https://blocked.example/orion/metrics\nFailure: kind=blocked, status=403\nNext: do not retry this exact URL; fetch an HTML/API/text fallback, use another available source, or mark this source as unverified")
			case "https://metrics.example/orion":
				return "Alternative metrics snapshot as of 2026-05-24T12:00:00Z: price $4.12, market cap $41.3M, 24h change -2.1%, 24h volume $980K.", nil
			default:
				return "", fmt.Errorf("unexpected test URL %q", p.URL)
			}
		},
	}

	turn1 := []string{
		`{"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"s1","type":"function","function":{"name":"web_search","arguments":"{\"query\":\"Orion Network recent trend market metrics sentiment\",\"num_results\":5}"}}]},"finish_reason":null}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`[DONE]`,
	}
	turn2 := []string{
		`{"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"f1","type":"function","function":{"name":"web_fetch","arguments":"{\"url\":\"https://official.example/orion/about\"}"}},{"index":1,"id":"f2","type":"function","function":{"name":"web_fetch","arguments":"{\"url\":\"https://blocked.example/orion/metrics\"}"}}]},"finish_reason":null}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`[DONE]`,
	}
	turn3 := []string{
		`{"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"f3","type":"function","function":{"name":"web_fetch","arguments":"{\"url\":\"https://metrics.example/orion\"}"}}]},"finish_reason":null}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`[DONE]`,
	}
	turn4 := []string{
		`{"choices":[{"delta":{"role":"assistant","content":"Orion Network is a decentralized storage subnet for encrypted archival workloads according to the official docs. The primary metrics page was blocked, so it should not be treated as verified evidence. A fallback metrics endpoint reports, as of 2026-05-24T12:00:00Z, price $4.12, market cap $41.3M, 24h change -2.1%, and 24h volume $980K. The social result had a Direct-reader warning, so I used only the snippet as weak sentiment evidence. Overall: recent market momentum is weak, and the blocked primary metrics source remains an unverified gap."},"finish_reason":"stop"}]}`,
		`[DONE]`,
	}
	srv := newScriptedLLM(t, [][]string{turn1, turn2, turn3, turn4})
	orionSearch := func(args map[string]any) bool {
		q, _ := args["query"].(string)
		return strings.Contains(q, "Orion Network")
	}
	fetchURL := func(url string) func(map[string]any) bool {
		return func(args map[string]any) bool {
			return args["url"] == url
		}
	}

	scenario := Scenario{
		Name:         "external_research_fetch_recovery",
		Description:  "agent follows web_fetch recovery guidance, switches to a fallback source, and marks blocked evidence as unverified",
		Prompt:       "Assess the recent trend for Orion Network. First identify what it is, then collect current market metrics and recent community sentiment. If a web fetch is blocked, follow the tool's recovery guidance and separate verified facts from unverified gaps.",
		MaxTurnSteps: 8,
		Checks: []Check{
			TurnEndedCleanly(),
			ToolCalled("web_search", orionSearch),
			ToolCalled("web_fetch", fetchURL("https://official.example/orion/about")),
			ToolCalled("web_fetch", fetchURL("https://blocked.example/orion/metrics")),
			ToolCalled("web_fetch", fetchURL("https://metrics.example/orion")),
			ToolNotCalled("web_fetch", fetchURL("https://social.example/search/orion")),
			ToolCalledAtLeast("web_fetch", 3),
			ToolCalledAtMostMatching("web_fetch", 1, fetchURL("https://blocked.example/orion/metrics")),
			ToolFailureKindAtLeast("blocked", 1),
			ToolResultContains("web_search", "Direct-reader warning"),
			ToolResultContains("web_fetch", "Next: do not retry this exact URL"),
			ToolCalledBeforeMatching("web_search", orionSearch, "web_fetch", fetchURL("https://blocked.example/orion/metrics")),
			ToolCalledBeforeMatching("web_fetch", fetchURL("https://blocked.example/orion/metrics"), "web_fetch", fetchURL("https://metrics.example/orion")),
			ToolNotCalled("shell", nil),
			FinalTextContains("decentralized storage subnet"),
			FinalTextContains("primary metrics page was blocked"),
			FinalTextContains("fallback metrics endpoint"),
			FinalTextContains("market cap $41.3M"),
			FinalTextContains("weak sentiment evidence"),
			FinalTextContains("unverified gap"),
		},
	}

	runner := &Runner{
		LLM:            agent.NewLLMClient(srv.URL, "", "fake-model"),
		MaxTurnSteps:   8,
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

// TestRunner_EndToEnd_ExternalResearchDynamicShellRecovery pins the common
// public-web failure mode behind app dashboards and social/search pages:
// direct fetch returns a readable-but-useless client shell. The shell must be
// treated as non-evidence, surfaced as dynamic_shell, and followed by a
// canonical text/API fallback when one is available.
func TestRunner_EndToEnd_ExternalResearchDynamicShellRecovery(t *testing.T) {
	webSearch := agent.Tool{
		Name:        "web_search",
		Description: "Test-only search stand-in: returns a dynamic dashboard and text fallback.",
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
  {"title":"Helio Subnet official docs","url":"https://official.example/helio/about","snippet":"Primary source describing Helio as a decentralized routing subnet."},
  {"title":"Helio live dashboard","url":"https://dashboard.example/helio","snippet":"Client-rendered market dashboard that may require JavaScript."},
  {"title":"Helio metrics API","url":"https://api.example/helio/metrics.txt","snippet":"Text metrics endpoint with price, market cap, and 24h change."},
  {"title":"Recent community discussion","url":"https://social.example/search/helio","snippet":"Direct-reader warning: do not direct-fetch this social result. Recent positive and critical community posts."}
]`, nil
		},
	}
	webFetch := agent.Tool{
		Name:        "web_fetch",
		Description: "Test-only fetch stand-in: returns one dynamic shell and deterministic fallbacks.",
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
			case "https://official.example/helio/about":
				return "SourceAccess: fetched_url=https://official.example/helio/about; linked_urls_in_content=discovered_unverified_until_fetched\n" +
					"Official docs, updated 2026-05-21: Helio is a decentralized routing subnet for inference traffic.", nil
			case "https://dashboard.example/helio":
				return "[dynamic page shell: URL=https://dashboard.example/helio, Content-Type=\"text/html\", Reason=\"low evidence app shell\"]\nFailure: kind=dynamic_shell\nNext: do not treat this loading/app shell as source evidence; use a canonical API/text/source page, another available source, or answer with this source marked as dynamic/unverified.", nil
			case "https://api.example/helio/metrics.txt":
				return "Text metrics snapshot as of 2026-05-24T12:00:00Z: price $6.42, market cap $32.5M, 24h change +4.8%, 24h volume $1.1M.", nil
			default:
				return "", fmt.Errorf("unexpected test URL %q", p.URL)
			}
		},
	}

	turn1 := []string{
		`{"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"s1","type":"function","function":{"name":"web_search","arguments":"{\"query\":\"Helio Subnet recent trend market metrics sentiment\",\"num_results\":5}"}}]},"finish_reason":null}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`[DONE]`,
	}
	turn2 := []string{
		`{"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"f1","type":"function","function":{"name":"web_fetch","arguments":"{\"url\":\"https://official.example/helio/about\"}"}},{"index":1,"id":"f2","type":"function","function":{"name":"web_fetch","arguments":"{\"url\":\"https://dashboard.example/helio\"}"}}]},"finish_reason":null}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`[DONE]`,
	}
	turn3 := []string{
		`{"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"f3","type":"function","function":{"name":"web_fetch","arguments":"{\"url\":\"https://api.example/helio/metrics.txt\"}"}}]},"finish_reason":null}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`[DONE]`,
	}
	turn4 := []string{
		`{"choices":[{"delta":{"role":"assistant","content":"Helio is a decentralized routing subnet for inference traffic according to the official docs. The live dashboard returned only a dynamic app shell, so I did not use it as verified evidence. The text metrics API reports, as of 2026-05-24T12:00:00Z, price $6.42, market cap $32.5M, 24h change +4.8%, and 24h volume $1.1M. The social result had a Direct-reader warning, so I used only its snippet as weak sentiment evidence. Overall: recent market momentum is positive, but the dashboard remains a dynamic/unverified gap."},"finish_reason":"stop"}]}`,
		`[DONE]`,
	}
	srv := newScriptedLLM(t, [][]string{turn1, turn2, turn3, turn4})
	helioSearch := func(args map[string]any) bool {
		q, _ := args["query"].(string)
		return strings.Contains(q, "Helio Subnet")
	}
	fetchURL := func(url string) func(map[string]any) bool {
		return func(args map[string]any) bool {
			return args["url"] == url
		}
	}

	scenario := Scenario{
		Name:         "external_research_dynamic_shell_recovery",
		Description:  "agent treats a client-rendered shell as non-evidence and switches to a text/API source",
		Prompt:       "Assess the recent trend for Helio Subnet. First identify what it is, then collect current market metrics and recent community sentiment. If a fetched page is a dynamic app shell, do not use it as evidence; switch to a text/API/source fallback and mark the gap.",
		MaxTurnSteps: 8,
		Checks: []Check{
			TurnEndedCleanly(),
			ToolCalled("web_search", helioSearch),
			ToolCalled("web_fetch", fetchURL("https://official.example/helio/about")),
			ToolCalled("web_fetch", fetchURL("https://dashboard.example/helio")),
			ToolCalled("web_fetch", fetchURL("https://api.example/helio/metrics.txt")),
			ToolNotCalled("web_fetch", fetchURL("https://social.example/search/helio")),
			ToolCalledAtLeast("web_fetch", 3),
			ToolCalledAtMostMatching("web_fetch", 1, fetchURL("https://dashboard.example/helio")),
			ToolFailureKindAtLeast("dynamic_shell", 1),
			ToolResultContains("web_search", "Direct-reader warning"),
			ToolResultContains("web_fetch", "Failure: kind=dynamic_shell"),
			ToolResultContains("web_fetch", "loading/app shell"),
			ToolCalledBeforeMatching("web_search", helioSearch, "web_fetch", fetchURL("https://dashboard.example/helio")),
			ToolCalledBeforeMatching("web_fetch", fetchURL("https://dashboard.example/helio"), "web_fetch", fetchURL("https://api.example/helio/metrics.txt")),
			ToolNotCalled("shell", nil),
			FinalTextContains("decentralized routing subnet"),
			FinalTextContains("dynamic app shell"),
			FinalTextContains("text metrics API"),
			FinalTextContains("market cap $32.5M"),
			FinalTextContains("weak sentiment evidence"),
			FinalTextContains("dynamic/unverified gap"),
		},
	}

	runner := &Runner{
		LLM:            agent.NewLLMClient(srv.URL, "", "fake-model"),
		MaxTurnSteps:   8,
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

// TestRunner_EndToEnd_ExternalResearchDynamicShellDiscoveryLinks pins the
// recovery path where search finds only a dynamic dashboard, and the usable
// API/text source is surfaced later by web_fetch's discovery links. The agent
// must treat the shell as non-evidence, follow the discovery link, and cite the
// API result rather than the shell preview.
func TestRunner_EndToEnd_ExternalResearchDynamicShellDiscoveryLinks(t *testing.T) {
	webSearch := agent.Tool{
		Name:        "web_search",
		Description: "Test-only search stand-in: returns docs plus a dynamic dashboard, but no metrics API result.",
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
  {"title":"Vela Subnet official docs","url":"https://official.example/vela/about","snippet":"Primary source describing Vela as a decentralized routing subnet."},
  {"title":"Vela market dashboard","url":"https://dashboard.example/vela","snippet":"Client-rendered dashboard. Metrics may require JavaScript."}
]`, nil
		},
	}
	webFetch := agent.Tool{
		Name:        "web_fetch",
		Description: "Test-only fetch stand-in: dynamic shell exposes a discovery link to the metrics API.",
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
			case "https://official.example/vela/about":
				return "Official docs, updated 2026-05-20: Vela is a decentralized routing subnet for inference traffic.", nil
			case "https://dashboard.example/vela":
				return "[dynamic page shell: URL=https://dashboard.example/vela, Content-Type=\"text/html\", Reason=\"client-rendered app shell\"]\nDiscovery preview (not source evidence): Home Documentation API API Keys Portfolio Validators Subnets\nDiscovery links (not source evidence):\n- API — https://dashboard.example/api/vela/metrics.json\n- Documentation — https://docs.example/vela\nFailure: kind=dynamic_shell\nNext: do not treat this loading/app shell as source evidence; use the discovery preview/links only to choose a canonical API/text/source page.", nil
			case "https://dashboard.example/api/vela/metrics.json":
				return `{"as_of":"2026-05-24T12:00:00Z","price_usd":9.25,"market_cap_usd":"44.0M","change_24h":"+3.1%"}`, nil
			default:
				return "", fmt.Errorf("unexpected test URL %q", p.URL)
			}
		},
	}

	turn1 := []string{
		`{"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"s1","type":"function","function":{"name":"web_search","arguments":"{\"query\":\"Vela Subnet recent trend market metrics\",\"num_results\":5}"}}]},"finish_reason":null}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`[DONE]`,
	}
	turn2 := []string{
		`{"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"f1","type":"function","function":{"name":"web_fetch","arguments":"{\"url\":\"https://official.example/vela/about\"}"}},{"index":1,"id":"f2","type":"function","function":{"name":"web_fetch","arguments":"{\"url\":\"https://dashboard.example/vela\"}"}}]},"finish_reason":null}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`[DONE]`,
	}
	turn3 := []string{
		`{"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"f3","type":"function","function":{"name":"web_fetch","arguments":"{\"url\":\"https://dashboard.example/api/vela/metrics.json\"}"}}]},"finish_reason":null}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`[DONE]`,
	}
	turn4 := []string{
		`{"choices":[{"delta":{"role":"assistant","content":"Vela is a decentralized routing subnet for inference traffic according to the official docs. The dashboard fetch returned only a dynamic app shell, so I did not use the shell preview as verified evidence. I followed its discovery link to the metrics API, which reports as of 2026-05-24T12:00:00Z: price $9.25, market cap $44.0M, and 24h change +3.1%. Dashboard evidence remains dynamic/unverified."},"finish_reason":"stop"}]}`,
		`[DONE]`,
	}
	srv := newScriptedLLM(t, [][]string{turn1, turn2, turn3, turn4})
	velaSearch := func(args map[string]any) bool {
		q, _ := args["query"].(string)
		return strings.Contains(q, "Vela Subnet")
	}
	fetchURL := func(url string) func(map[string]any) bool {
		return func(args map[string]any) bool {
			return args["url"] == url
		}
	}

	scenario := Scenario{
		Name:         "external_research_dynamic_shell_discovery_links",
		Description:  "agent follows a web_fetch dynamic-shell discovery link to a metrics API not present in search results",
		Prompt:       "Assess current Vela Subnet market metrics. If a fetched dashboard is a dynamic shell, use any discovery links only to find a text/API/source endpoint; do not use the shell preview as evidence.",
		MaxTurnSteps: 8,
		Checks: []Check{
			TurnEndedCleanly(),
			ToolCalled("web_search", velaSearch),
			ToolCalled("web_fetch", fetchURL("https://official.example/vela/about")),
			ToolCalled("web_fetch", fetchURL("https://dashboard.example/vela")),
			ToolCalled("web_fetch", fetchURL("https://dashboard.example/api/vela/metrics.json")),
			ToolCalledBeforeMatching("web_fetch", fetchURL("https://dashboard.example/vela"), "web_fetch", fetchURL("https://dashboard.example/api/vela/metrics.json")),
			ToolFailureKindAtLeast("dynamic_shell", 1),
			ToolResultContains("web_fetch", "Discovery links (not source evidence)"),
			ToolResultContains("web_fetch", "https://dashboard.example/api/vela/metrics.json"),
			ToolNotCalled("web_fetch", fetchURL("https://dashboard.example/pro/api-keys")),
			FinalTextContains("did not use the shell preview as verified evidence"),
			FinalTextContains("followed its discovery link"),
			FinalTextContains("metrics API"),
			FinalTextContains("market cap $44.0M"),
			FinalTextContains("dynamic/unverified"),
		},
	}

	runner := &Runner{
		LLM:            agent.NewLLMClient(srv.URL, "", "fake-model"),
		MaxTurnSteps:   8,
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

// TestRunner_EndToEnd_ExternalResearchDynamicShellEmbeddedData pins the
// positive path for client-rendered dashboards whose HTML source already
// contains URL-relevant structured data. In that case web_fetch returns an
// Embedded data preview, the result is evidence rather than dynamic_shell
// failure, and the agent should answer without spending another turn on a
// fallback metrics API.
func TestRunner_EndToEnd_ExternalResearchDynamicShellEmbeddedData(t *testing.T) {
	webSearch := agent.Tool{
		Name:        "web_search",
		Description: "Test-only search stand-in: returns docs plus a dashboard with embedded data.",
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
  {"title":"Orchid Subnet official docs","url":"https://official.example/orchid/about","snippet":"Primary source describing Orchid as a decentralized reasoning subnet."},
  {"title":"Orchid market dashboard","url":"https://dashboard.example/subnets/120","snippet":"Client-rendered dashboard. HTML source contains subnet app state."},
  {"title":"Orchid metrics API","url":"https://api.example/orchid/metrics.json","snippet":"Fallback metrics endpoint if dashboard source has no evidence."}
]`, nil
		},
	}
	webFetch := agent.Tool{
		Name:        "web_fetch",
		Description: "Test-only fetch stand-in: dashboard returns embedded page-source evidence.",
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
			case "https://official.example/orchid/about":
				return "Official docs, updated 2026-05-20: Orchid is a decentralized reasoning subnet.", nil
			case "https://dashboard.example/subnets/120":
				return `[dynamic page shell: URL=https://dashboard.example/subnets/120, Content-Type="text/html", Reason="client-rendered app shell"]
Embedded data preview (page source evidence; verify relevance before using):
- {"netuid":120,"subnet_name":"Orchid","github_repo":"https://github.com/example/orchid","subnet_url":"orchid.example","contact":"hello@orchid.example"}
- {"netuid":120,"name":"Orchid","price":"0.061","market_cap":"195094","volume_24h":"5001","rank":5}
Next: the rendered page shell itself is not evidence; use the embedded data preview only when it directly matches the requested entity/URL, otherwise switch to a canonical API/text/source page or mark rendered-only fields as unverified.`, nil
			case "https://api.example/orchid/metrics.json":
				return "", fmt.Errorf("fallback metrics API should not be fetched when dashboard source contains structured embedded evidence")
			default:
				return "", fmt.Errorf("unexpected test URL %q", p.URL)
			}
		},
	}

	turn1 := []string{
		`{"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"s1","type":"function","function":{"name":"web_search","arguments":"{\"query\":\"Orchid Subnet recent trend market metrics\",\"num_results\":5}"}}]},"finish_reason":null}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`[DONE]`,
	}
	turn2 := []string{
		`{"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"f1","type":"function","function":{"name":"web_fetch","arguments":"{\"url\":\"https://official.example/orchid/about\"}"}},{"index":1,"id":"f2","type":"function","function":{"name":"web_fetch","arguments":"{\"url\":\"https://dashboard.example/subnets/120\"}"}}]},"finish_reason":null}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`[DONE]`,
	}
	turn3 := []string{
		`{"choices":[{"delta":{"role":"assistant","content":"Orchid is a decentralized reasoning subnet according to the official docs. The dashboard page is client-rendered, but its page source exposed embedded data directly matching subnet netuid 120, including github_repo https://github.com/example/orchid, price 0.061, market cap 195094, volume 5001, and rank 5. I used that embedded page-source evidence and did not need the fallback metrics API."},"finish_reason":"stop"}]}`,
		`[DONE]`,
	}
	srv := newScriptedLLM(t, [][]string{turn1, turn2, turn3})
	orchidSearch := func(args map[string]any) bool {
		q, _ := args["query"].(string)
		return strings.Contains(q, "Orchid Subnet")
	}
	fetchURL := func(url string) func(map[string]any) bool {
		return func(args map[string]any) bool {
			return args["url"] == url
		}
	}

	scenario := Scenario{
		Name:         "external_research_dynamic_shell_embedded_data",
		Description:  "agent uses URL-relevant embedded dashboard data as evidence instead of treating the dashboard as a dynamic-shell failure",
		Prompt:       "Assess current Orchid Subnet market metrics. If a fetched dashboard contains embedded page-source data for the requested subnet, use it as evidence and do not fetch fallback metrics unnecessarily.",
		MaxTurnSteps: 6,
		Checks: []Check{
			TurnEndedCleanly(),
			ToolCalled("web_search", orchidSearch),
			ToolCalled("web_fetch", fetchURL("https://official.example/orchid/about")),
			ToolCalled("web_fetch", fetchURL("https://dashboard.example/subnets/120")),
			ToolNotCalled("web_fetch", fetchURL("https://api.example/orchid/metrics.json")),
			ToolFailureKindAtMost("dynamic_shell", 0),
			ToolResultContains("web_fetch", "Embedded data preview (page source evidence"),
			ToolResultContains("web_fetch", `"netuid":120`),
			ToolResultContains("web_fetch", `"market_cap":"195094"`),
			ToolCalledBeforeMatching("web_search", orchidSearch, "web_fetch", fetchURL("https://dashboard.example/subnets/120")),
			FinalTextContains("decentralized reasoning subnet"),
			FinalTextContains("embedded data directly matching subnet netuid 120"),
			FinalTextContains("market cap 195094"),
			FinalTextContains("did not need the fallback metrics API"),
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
	if len(out.Trace.Tools) != 3 {
		t.Fatalf("expected one search and two fetches; got %+v", out.Trace.Tools)
	}
}

// TestRunner_EndToEnd_ExternalResearchDynamicHostGuard pins the runtime side
// of dynamic-shell recovery: if a model keeps trying dashboard/page routes on
// the same host after two dynamic shells, the loop guard blocks more page-route
// fetches while still allowing a likely API/text/export fallback on that host.
func TestRunner_EndToEnd_ExternalResearchDynamicHostGuard(t *testing.T) {
	var dynamicDispatches atomic.Int32
	var guardedPageDispatches atomic.Int32
	var apiDispatches atomic.Int32
	webSearch := agent.Tool{
		Name:        "web_search",
		Description: "Test-only search stand-in: returns repeated dashboard routes and an API fallback.",
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
  {"title":"Helio official docs","url":"https://official.example/helio/about","snippet":"Primary source describing Helio."},
  {"title":"Helio dashboard","url":"https://metrics.example/helio","snippet":"Client-rendered live dashboard."},
  {"title":"Helio validators dashboard","url":"https://metrics.example/helio/validators","snippet":"Another JavaScript dashboard route."},
  {"title":"Helio metrics API","url":"https://metrics.example/api/helio/metrics.json","snippet":"Text/API metrics endpoint with current market data."}
]`, nil
		},
	}
	webFetch := agent.Tool{
		Name:        "web_fetch",
		Description: "Test-only fetch stand-in: records dashboard dispatches so host guard behavior is observable.",
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
			case "https://official.example/helio/about":
				return "SourceAccess: fetched_url=https://official.example/helio/about; linked_urls_in_content=discovered_unverified_until_fetched\n" +
					"Official docs, updated 2026-05-21: Helio is a decentralized routing subnet for inference traffic.", nil
			case "https://metrics.example/helio", "https://metrics.example/helio/validators":
				dynamicDispatches.Add(1)
				return "[dynamic page shell: URL=" + p.URL + ", Content-Type=\"text/html\", Reason=\"client-rendered app shell\"]\nFailure: kind=dynamic_shell\nNext: do not treat this loading/app shell as source evidence; use a canonical API/text/source page.", nil
			case "https://metrics.example/helio/emissions":
				guardedPageDispatches.Add(1)
				return "this page route should have been blocked before dispatch", nil
			case "https://metrics.example/api/helio/metrics.json":
				apiDispatches.Add(1)
				return "SourceAccess: fetched_url=https://metrics.example/api/helio/metrics.json; linked_urls_in_content=discovered_unverified_until_fetched\n" +
					`{"as_of":"2026-05-24T12:00:00Z","price_usd":6.42,"market_cap_usd":"32.5M","change_24h":"+4.8%"}`, nil
			default:
				return "", fmt.Errorf("unexpected test URL %q", p.URL)
			}
		},
	}

	turn1 := []string{
		`{"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"s1","type":"function","function":{"name":"web_search","arguments":"{\"query\":\"Helio Subnet recent trend market metrics\",\"num_results\":5}"}}]},"finish_reason":null}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`[DONE]`,
	}
	turn2 := []string{
		`{"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"f1","type":"function","function":{"name":"web_fetch","arguments":"{\"url\":\"https://official.example/helio/about\"}"}},{"index":1,"id":"f2","type":"function","function":{"name":"web_fetch","arguments":"{\"url\":\"https://metrics.example/helio\"}"}}]},"finish_reason":null}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`[DONE]`,
	}
	turn3 := []string{
		`{"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"f3","type":"function","function":{"name":"web_fetch","arguments":"{\"url\":\"https://metrics.example/helio/validators\"}"}}]},"finish_reason":null}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`[DONE]`,
	}
	turn4 := []string{
		`{"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"f4","type":"function","function":{"name":"web_fetch","arguments":"{\"url\":\"https://metrics.example/helio/emissions\"}"}}]},"finish_reason":null}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`[DONE]`,
	}
	turn5 := []string{
		`{"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"f5","type":"function","function":{"name":"web_fetch","arguments":"{\"url\":\"https://metrics.example/api/helio/metrics.json\"}"}}]},"finish_reason":null}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`[DONE]`,
	}
	turn6 := []string{
		`{"choices":[{"delta":{"role":"assistant","content":"Helio is a decentralized routing subnet for inference traffic according to the official docs. Two metrics dashboard routes returned dynamic app shells and a third dashboard route was blocked by the loop guard, so dashboard evidence remains unverified. The API metrics endpoint reports, as of 2026-05-24T12:00:00Z, price $6.42, market cap $32.5M, and 24h change +4.8%."},"finish_reason":"stop"}]}`,
		`[DONE]`,
	}
	srv := newScriptedLLM(t, [][]string{turn1, turn2, turn3, turn4, turn5, turn6})
	helioSearch := func(args map[string]any) bool {
		q, _ := args["query"].(string)
		return strings.Contains(q, "Helio Subnet")
	}
	fetchURL := func(url string) func(map[string]any) bool {
		return func(args map[string]any) bool {
			return args["url"] == url
		}
	}

	scenario := Scenario{
		Name:         "external_research_dynamic_host_guard",
		Description:  "runtime blocks repeated dynamic dashboard routes while allowing API fallback",
		Prompt:       "Assess current Helio Subnet market metrics. If dashboard page routes return dynamic app shells, do not keep trying page routes; switch to an API/text/export endpoint and mark dashboard evidence as unverified.",
		MaxTurnSteps: 8,
		Checks: []Check{
			TurnEndedCleanly(),
			ToolCalled("web_search", helioSearch),
			ToolCalled("web_fetch", fetchURL("https://metrics.example/helio")),
			ToolCalled("web_fetch", fetchURL("https://metrics.example/helio/validators")),
			ToolCalled("web_fetch", fetchURL("https://metrics.example/helio/emissions")),
			ToolCalled("web_fetch", fetchURL("https://metrics.example/api/helio/metrics.json")),
			ToolStatsAtLeast("source_access_results", 2),
			ToolStatsAtLeast("source_access_verified", 2),
			ToolFailureKindAtLeast("dynamic_shell", 2),
			ToolFailureKindAtLeast("loop_guard_repeated_failed_input", 1),
			ToolResultContains("web_fetch", "blocked web_fetch to host"),
			ToolResultContains("web_fetch", "Failure kind=dynamic_shell"),
			ToolCalledBeforeMatching("web_fetch", fetchURL("https://metrics.example/helio/emissions"), "web_fetch", fetchURL("https://metrics.example/api/helio/metrics.json")),
			FinalTextContains("third dashboard route was blocked by the loop guard"),
			FinalTextContains("API metrics endpoint"),
			FinalTextContains("market cap $32.5M"),
			FinalTextContains("unverified"),
		},
	}

	runner := &Runner{
		LLM:            agent.NewLLMClient(srv.URL, "", "fake-model"),
		MaxTurnSteps:   8,
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
	if got := dynamicDispatches.Load(); got != 2 {
		t.Fatalf("dashboard routes should dispatch exactly twice before host guard, got %d", got)
	}
	if got := guardedPageDispatches.Load(); got != 0 {
		t.Fatalf("third dashboard route should be guard-blocked before dispatch, got %d dispatches", got)
	}
	if got := apiDispatches.Load(); got != 1 {
		t.Fatalf("API fallback should remain dispatchable after dynamic host guard, got %d", got)
	}
	if len(out.Trace.Tools) != 6 {
		t.Fatalf("expected one search and five fetch trace entries; got %+v", out.Trace.Tools)
	}
}

// TestRunner_EndToEnd_ExternalResearchRepeatedFailedInputGuard pins the
// runtime side of web recovery: even if the model ignores web_fetch's Next
// guidance and repeats the same blocked URL, the loop guard blocks the repeat
// before dispatch and surfaces a machine-readable failure kind the eval stack
// can diagnose.
func TestRunner_EndToEnd_ExternalResearchRepeatedFailedInputGuard(t *testing.T) {
	var blockedDispatches atomic.Int32
	var fallbackDispatches atomic.Int32
	webSearch := agent.Tool{
		Name:        "web_search",
		Description: "Test-only search stand-in: returns blocked and fallback metric candidates.",
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
  {"title":"Lyra Network primary metrics","url":"https://blocked.example/lyra/metrics","snippet":"Primary metrics page that blocks direct fetches."},
  {"title":"Lyra Network fallback metrics","url":"https://metrics.example/lyra","snippet":"Alternative text metrics endpoint with current market data."}
]`, nil
		},
	}
	webFetch := agent.Tool{
		Name:        "web_fetch",
		Description: "Test-only fetch stand-in: records dispatches so guard-blocked repeats are observable.",
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
			case "https://blocked.example/lyra/metrics":
				blockedDispatches.Add(1)
				return "", errors.New("web_fetch failed: HTTP 403 Forbidden for https://blocked.example/lyra/metrics\nFailure: kind=blocked, status=403\nNext: do not retry this exact URL; fetch an HTML/API/text fallback or mark this source as unverified")
			case "https://metrics.example/lyra":
				fallbackDispatches.Add(1)
				return "Fallback metrics snapshot as of 2026-05-24T12:00:00Z: price $8.10, market cap $19.4M, 24h change +0.9%.", nil
			default:
				return "", fmt.Errorf("unexpected test URL %q", p.URL)
			}
		},
	}

	turn1 := []string{
		`{"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"s1","type":"function","function":{"name":"web_search","arguments":"{\"query\":\"Lyra Network market metrics\",\"num_results\":5}"}}]},"finish_reason":null}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`[DONE]`,
	}
	turn2 := []string{
		`{"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"f1","type":"function","function":{"name":"web_fetch","arguments":"{\"url\":\"https://blocked.example/lyra/metrics\"}"}}]},"finish_reason":null}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`[DONE]`,
	}
	turn3 := []string{
		`{"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"f2","type":"function","function":{"name":"web_fetch","arguments":"{\"url\":\"https://blocked.example/lyra/metrics\"}"}}]},"finish_reason":null}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`[DONE]`,
	}
	turn4 := []string{
		`{"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"f3","type":"function","function":{"name":"web_fetch","arguments":"{\"url\":\"https://metrics.example/lyra\"}"}}]},"finish_reason":null}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`[DONE]`,
	}
	turn5 := []string{
		`{"choices":[{"delta":{"role":"assistant","content":"The primary Lyra metrics URL was blocked and a repeat was rejected by the loop guard, so it is not verified evidence. I used the fallback metrics endpoint instead: as of 2026-05-24T12:00:00Z it reports price $8.10, market cap $19.4M, and 24h change +0.9%. The answer should keep the blocked primary source as an unverified gap."},"finish_reason":"stop"}]}`,
		`[DONE]`,
	}
	srv := newScriptedLLM(t, [][]string{turn1, turn2, turn3, turn4, turn5})
	lyraSearch := func(args map[string]any) bool {
		q, _ := args["query"].(string)
		return strings.Contains(q, "Lyra Network")
	}
	fetchURL := func(url string) func(map[string]any) bool {
		return func(args map[string]any) bool {
			return args["url"] == url
		}
	}

	scenario := Scenario{
		Name:         "external_research_repeated_failed_input_guard",
		Description:  "runtime blocks repeated failed web_fetch input and the model can still switch source",
		Prompt:       "Assess current Lyra Network market metrics. If a fetch is blocked, do not reuse that evidence; switch to an alternate source and mark gaps.",
		MaxTurnSteps: 8,
		Checks: []Check{
			TurnEndedCleanly(),
			ToolCalled("web_search", lyraSearch),
			ToolCalled("web_fetch", fetchURL("https://blocked.example/lyra/metrics")),
			ToolCalled("web_fetch", fetchURL("https://metrics.example/lyra")),
			ToolCalledAtLeast("web_fetch", 3),
			ToolFailureKindAtLeast("blocked", 1),
			ToolFailureKindAtLeast("loop_guard_repeated_failed_input", 1),
			ToolResultContains("web_fetch", "same effective URL"),
			ToolCalledBeforeMatching("web_fetch", fetchURL("https://blocked.example/lyra/metrics"), "web_fetch", fetchURL("https://metrics.example/lyra")),
			FinalTextContains("repeat was rejected by the loop guard"),
			FinalTextContains("market cap $19.4M"),
			FinalTextContains("unverified gap"),
		},
	}

	runner := &Runner{
		LLM:            agent.NewLLMClient(srv.URL, "", "fake-model"),
		MaxTurnSteps:   8,
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
	if got := blockedDispatches.Load(); got != 1 {
		t.Fatalf("blocked URL should dispatch exactly once; repeated attempt must be guard-blocked, got %d", got)
	}
	if got := fallbackDispatches.Load(); got != 1 {
		t.Fatalf("fallback URL should dispatch exactly once, got %d", got)
	}
	if len(out.Trace.Tools) != 4 {
		t.Fatalf("expected one search and three fetch trace entries; got %+v", out.Trace.Tools)
	}
}

// TestRunner_EndToEnd_ExternalResearchDirectReaderWarningGuard pins the
// runtime side of search-result warnings: if a model ignores a
// Direct-reader warning and tries to fetch that URL anyway, the loop guard
// blocks the dispatch, emits a structured failure kind, and the model can
// continue with canonical sources.
func TestRunner_EndToEnd_ExternalResearchDirectReaderWarningGuard(t *testing.T) {
	var socialDispatches atomic.Int32
	var canonicalDispatches atomic.Int32
	webSearch := agent.Tool{
		Name:        "web_search",
		Description: "Test-only search stand-in: returns canonical sources and one direct-reader warning.",
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
			return `1. Mira Network official docs
   https://official.example/mira/about
   Primary source describing Mira Network.

2. Mira Network metrics API
   https://metrics.example/mira
   Text metrics endpoint with current market data.

3. Recent social discussion
   https://x.com/example/status/777
   Community reaction.
   Direct-reader warning: do not use direct page fetch on this URL; use snippet only as weak sentiment evidence.

Next: choose authoritative sources and do not spend direct page-reading calls on URLs marked with Direct-reader warning.`, nil
		},
	}
	webFetch := agent.Tool{
		Name:        "web_fetch",
		Description: "Test-only fetch stand-in: records whether warned URLs dispatch.",
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
			case "https://x.com/example/status/777":
				socialDispatches.Add(1)
				return "social page should have been blocked before dispatch", nil
			case "https://official.example/mira/about":
				canonicalDispatches.Add(1)
				return "Official docs, updated 2026-05-23: Mira Network is a decentralized indexing subnet for retrieval workloads.", nil
			case "https://metrics.example/mira":
				canonicalDispatches.Add(1)
				return "Metrics snapshot as of 2026-05-24T12:00:00Z: price $3.30, market cap $18.2M, 24h change +2.6%.", nil
			default:
				return "", fmt.Errorf("unexpected test URL %q", p.URL)
			}
		},
	}

	turn1 := []string{
		`{"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"s1","type":"function","function":{"name":"web_search","arguments":"{\"query\":\"Mira Network recent trend market metrics sentiment\",\"num_results\":5}"}}]},"finish_reason":null}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`[DONE]`,
	}
	turn2 := []string{
		`{"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"f1","type":"function","function":{"name":"web_fetch","arguments":"{\"url\":\"https://x.com/example/status/777\"}"}}]},"finish_reason":null}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`[DONE]`,
	}
	turn3 := []string{
		`{"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"f2","type":"function","function":{"name":"web_fetch","arguments":"{\"url\":\"https://official.example/mira/about\"}"}},{"index":1,"id":"f3","type":"function","function":{"name":"web_fetch","arguments":"{\"url\":\"https://metrics.example/mira\"}"}}]},"finish_reason":null}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`[DONE]`,
	}
	turn4 := []string{
		`{"choices":[{"delta":{"role":"assistant","content":"Mira Network is a decentralized indexing subnet for retrieval workloads according to the official docs. The social URL was blocked by the Direct-reader warning guard, so I used its snippet only as weak sentiment evidence and did not treat it as page evidence. The metrics endpoint reports, as of 2026-05-24T12:00:00Z, price $3.30, market cap $18.2M, and 24h change +2.6%. Overall, recent market momentum is positive but social evidence remains weak."},"finish_reason":"stop"}]}`,
		`[DONE]`,
	}
	srv := newScriptedLLM(t, [][]string{turn1, turn2, turn3, turn4})
	miraSearch := func(args map[string]any) bool {
		q, _ := args["query"].(string)
		return strings.Contains(q, "Mira Network")
	}
	fetchURL := func(url string) func(map[string]any) bool {
		return func(args map[string]any) bool {
			return args["url"] == url
		}
	}

	scenario := Scenario{
		Name:         "external_research_direct_reader_warning_guard",
		Description:  "runtime blocks web_fetch to a search result marked Direct-reader warning and allows canonical fallback sources",
		Prompt:       "Assess the recent trend for Mira Network. First identify what it is, then collect current market metrics and recent community sentiment.",
		MaxTurnSteps: 8,
		Checks: []Check{
			TurnEndedCleanly(),
			ToolCalled("web_search", miraSearch),
			ToolCalled("web_fetch", fetchURL("https://x.com/example/status/777")),
			ToolCalled("web_fetch", fetchURL("https://official.example/mira/about")),
			ToolCalled("web_fetch", fetchURL("https://metrics.example/mira")),
			ToolFailureKindAtLeast("loop_guard_direct_reader_warning", 1),
			ToolResultContains("web_fetch", "web_search marked that URL with Direct-reader warning"),
			ToolCalledBeforeMatching("web_fetch", fetchURL("https://x.com/example/status/777"), "web_fetch", fetchURL("https://official.example/mira/about")),
			ToolNotCalled("shell", nil),
			FinalTextContains("Direct-reader warning guard"),
			FinalTextContains("weak sentiment evidence"),
			FinalTextContains("market cap $18.2M"),
		},
	}

	runner := &Runner{
		LLM:            agent.NewLLMClient(srv.URL, "", "fake-model"),
		MaxTurnSteps:   8,
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
	if got := socialDispatches.Load(); got != 0 {
		t.Fatalf("direct-reader warning URL must be guard-blocked before dispatch, got %d dispatches", got)
	}
	if got := canonicalDispatches.Load(); got != 2 {
		t.Fatalf("canonical sources should dispatch exactly twice, got %d", got)
	}
	if len(out.Trace.Tools) != 4 {
		t.Fatalf("expected one search and three fetch trace entries; got %+v", out.Trace.Tools)
	}
}

// TestRunner_EndToEnd_ExternalResearchSearchRecovery pins the discovery-side
// recovery path: a no-results search is not evidence, and the model should
// preserve user-provided disambiguators while refining with source terms
// before fetching.
func TestRunner_EndToEnd_ExternalResearchSearchRecovery(t *testing.T) {
	webSearch := agent.Tool{
		Name:        "web_search",
		Description: "Test-only search stand-in: first broad query returns no evidence; refined query returns source candidates.",
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
			var p struct {
				Query string `json:"query"`
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return "", err
			}
			if strings.Contains(p.Query, "Bittensor") &&
				strings.Contains(p.Query, "subnet 88") &&
				strings.Contains(p.Query, "official domain") {
				return `[
  {"title":"Vega Subnet official docs","url":"https://official.example/vega/about","snippet":"Primary source describing Vega as a decentralized inference subnet."},
  {"title":"Vega Subnet metrics","url":"https://metrics.example/vega","snippet":"Current subnet price, market cap, and 24h change."}
]`, nil
			}
			return "(no results)\nFailure: kind=no_results\nNext: retry web_search with fewer or different keywords, preserve user-provided disambiguators such as ecosystem, ticker, network/subnet id, official domain, or date range, or use another available source URL if already known.", nil
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
			case "https://official.example/vega/about":
				return "Official docs, updated 2026-05-22: Vega is a decentralized inference subnet for low-latency model serving.", nil
			case "https://metrics.example/vega":
				return "Metrics snapshot as of 2026-05-24T12:00:00Z: price $2.40, market cap $24.8M, 24h change +1.4%.", nil
			default:
				return "", fmt.Errorf("unexpected test URL %q", p.URL)
			}
		},
	}

	turn1 := []string{
		`{"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"s1","type":"function","function":{"name":"web_search","arguments":"{\"query\":\"Vega Bittensor subnet 88 recent trend market metrics sentiment\",\"num_results\":5}"}}]},"finish_reason":null}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`[DONE]`,
	}
	turn2 := []string{
		`{"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"s2","type":"function","function":{"name":"web_search","arguments":"{\"query\":\"Vega Bittensor subnet 88 official domain metrics\",\"num_results\":5}"}}]},"finish_reason":null}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`[DONE]`,
	}
	turn3 := []string{
		`{"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"f1","type":"function","function":{"name":"web_fetch","arguments":"{\"url\":\"https://official.example/vega/about\"}"}},{"index":1,"id":"f2","type":"function","function":{"name":"web_fetch","arguments":"{\"url\":\"https://metrics.example/vega\"}"}}]},"finish_reason":null}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`[DONE]`,
	}
	turn4 := []string{
		`{"choices":[{"delta":{"role":"assistant","content":"The initial broad search preserved the Bittensor subnet 88 disambiguator but returned no usable results, so I treated it as an evidence gap and refined the query with official-domain terms. The official docs say Vega is a decentralized inference subnet for low-latency model serving. The metrics source reports, as of 2026-05-24T12:00:00Z, price $2.40, market cap $24.8M, and 24h change +1.4%. Overall, the current market move is mildly positive, but sentiment remains unverified because no readable social source was found."},"finish_reason":"stop"}]}`,
		`[DONE]`,
	}
	srv := newScriptedLLM(t, [][]string{turn1, turn2, turn3, turn4})
	broadSearch := func(args map[string]any) bool {
		q, _ := args["query"].(string)
		return strings.Contains(q, "Vega") &&
			strings.Contains(q, "Bittensor") &&
			strings.Contains(q, "subnet 88") &&
			strings.Contains(q, "recent trend")
	}
	refinedSearch := func(args map[string]any) bool {
		q, _ := args["query"].(string)
		return strings.Contains(q, "Vega") &&
			strings.Contains(q, "Bittensor") &&
			strings.Contains(q, "subnet 88") &&
			strings.Contains(q, "official domain")
	}
	fetchURL := func(url string) func(map[string]any) bool {
		return func(args map[string]any) bool {
			return args["url"] == url
		}
	}

	scenario := Scenario{
		Name:         "external_research_search_recovery",
		Description:  "agent treats no-results search as non-evidence, preserves disambiguators, refines once, then fetches source candidates",
		Prompt:       "Vega is a Bittensor subnet 88. Assess its recent trend. First identify what it is, then collect current market metrics. If search returns no results, refine once while preserving the Bittensor/subnet 88 disambiguator and clearly mark any remaining gaps.",
		MaxTurnSteps: 8,
		Checks: []Check{
			TurnEndedCleanly(),
			ToolCalled("web_search", broadSearch),
			ToolCalled("web_search", refinedSearch),
			ToolArgContainsAtLeast("web_search", "query", "Bittensor", 2),
			ToolArgContainsAtLeast("web_search", "query", "subnet 88", 2),
			ToolCalledAtMostMatching("web_search", 1, broadSearch),
			ToolFailureKindAtLeast("no_results", 1),
			ToolResultContains("web_search", "Failure: kind=no_results"),
			ToolCalledBeforeMatching("web_search", broadSearch, "web_search", refinedSearch),
			ToolCalledBeforeMatching("web_search", refinedSearch, "web_fetch", fetchURL("https://official.example/vega/about")),
			ToolCalled("web_fetch", fetchURL("https://official.example/vega/about")),
			ToolCalled("web_fetch", fetchURL("https://metrics.example/vega")),
			ToolNotCalled("shell", nil),
			FinalTextContains("Bittensor subnet 88 disambiguator"),
			FinalTextContains("decentralized inference subnet"),
			FinalTextContains("market cap $24.8M"),
			FinalTextContains("sentiment remains unverified"),
		},
	}

	runner := &Runner{
		LLM:            agent.NewLLMClient(srv.URL, "", "fake-model"),
		MaxTurnSteps:   8,
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
		t.Fatalf("expected two searches and two fetches; got %+v", out.Trace.Tools)
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

// TestRunner_EndToEnd_ExternalResearchAffineBittensorFlow mirrors the real
// Affine/Bittensor investigation shape so future changes don't regress back to
// broad retries that burn the turn budget before the answer is assembled.
func TestRunner_EndToEnd_ExternalResearchAffineBittensorFlow(t *testing.T) {
	webSearch := agent.Tool{
		Name:        "web_search",
		Description: "Test-only search stand-in: returns an official source, a market page, and a social snippet.",
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
  {"title":"Affine SN120 official docs","url":"https://official.example/affine/about","snippet":"Affine is a Bittensor subnet focused on model-training and reasoning workloads."},
  {"title":"Affine SN120 market metrics","url":"https://metrics.example/affine","snippet":"Current price, market cap, 24h volume, and recent change for Affine SN120."},
  {"title":"Recent community discussion","url":"https://social.example/search/affine","snippet":"Direct-reader warning: do not direct-fetch this social result. Mixed reactions about recent momentum and liquidity."}
]`, nil
		},
	}
	webFetch := agent.Tool{
		Name:        "web_fetch",
		Description: "Test-only fetch stand-in: returns deterministic Affine page text.",
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
			case "https://official.example/affine/about":
				return "SourceAccess: fetched_url=https://official.example/affine/about; linked_urls_in_content=discovered_unverified_until_fetched\n" +
					"Official docs, updated 2026-05-24: Affine is a Bittensor SN120 subnet for training-and-reasoning workloads.", nil
			case "https://metrics.example/affine":
				return "SourceAccess: fetched_url=https://metrics.example/affine; linked_urls_in_content=discovered_unverified_until_fetched\n" +
					"Metrics snapshot as of 2026-05-24T12:00:00Z: price $0.0632, market cap $195094, 24h volume $5001, 24h change +0.4%.", nil
			default:
				return "", fmt.Errorf("unexpected test URL %q", p.URL)
			}
		},
	}

	srv := newScriptedLLM(t, [][]string{
		{
			`{"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"s1","type":"function","function":{"name":"web_search","arguments":"{\"query\":\"Affine Bittensor SN120 market price market cap official domain\",\"num_results\":5}"}}]},"finish_reason":null}]}`,
			`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
			`[DONE]`,
		},
		{
			`{"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"f1","type":"function","function":{"name":"web_fetch","arguments":"{\"url\":\"https://official.example/affine/about\"}"}},{"index":1,"id":"f2","type":"function","function":{"name":"web_fetch","arguments":"{\"url\":\"https://metrics.example/affine\"}"}}]},"finish_reason":null}]}`,
			`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
			`[DONE]`,
		},
		{
			`{"choices":[{"delta":{"role":"assistant","content":"Affine is a Bittensor SN120 subnet focused on training-and-reasoning workloads according to the official docs. As of 2026-05-24T12:00:00Z, the metrics source reports price $0.0632, market cap $195094, 24h volume $5001, and a small positive daily change. The social result had a Direct-reader warning, so I treated it only as weak mixed sentiment evidence and did not fetch it. Overall, the current trend looks stable but still early and volatile."},"finish_reason":"stop"}]}`,
			`[DONE]`,
		},
	})
	searchArgs := func(args map[string]any) bool {
		q, _ := args["query"].(string)
		return strings.Contains(q, "Affine") &&
			strings.Contains(q, "Bittensor") &&
			strings.Contains(q, "market") &&
			strings.Contains(q, "price")
	}
	fetchURL := func(url string) func(map[string]any) bool {
		return func(args map[string]any) bool { return args["url"] == url }
	}

	scenario := Scenario{
		Name:         "external_research_affine_bittensor_flow",
		Description:  "agent discovers official and metrics sources, skips direct social fetch, and ends from verified evidence",
		Prompt:       "Affine is a Bittensor SN120 subnet. Assess its recent trend and market position. Start from official and market sources, preserve the Bittensor/SN120 disambiguator, and keep social snippets as weak evidence only.",
		MaxTurnSteps: 6,
		Checks: []Check{
			TurnEndedCleanly(),
			ToolCalled("web_search", searchArgs),
			ToolResultContains("web_search", "Direct-reader warning"),
			ToolCalled("web_fetch", fetchURL("https://official.example/affine/about")),
			ToolCalled("web_fetch", fetchURL("https://metrics.example/affine")),
			ToolStatsAtLeast("source_access_results", 2),
			ToolStatsAtLeast("source_access_verified", 2),
			ToolNotCalled("web_fetch", fetchURL("https://social.example/search/affine")),
			ToolCalledBeforeMatching("web_search", searchArgs, "web_fetch", fetchURL("https://official.example/affine/about")),
			ToolCalledBeforeMatching("web_search", searchArgs, "web_fetch", fetchURL("https://metrics.example/affine")),
			FinalTextContains("Bittensor SN120 subnet"),
			FinalTextContains("price $0.0632"),
			FinalTextContains("market cap $195094"),
			FinalTextContains("weak mixed sentiment evidence"),
			FinalTextContains("stable but still early and volatile"),
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
	if len(out.Trace.Tools) != 3 {
		t.Fatalf("expected one search and two fetches; got %+v", out.Trace.Tools)
	}
}

// TestRunner_EndToEnd_ExternalResearchAffinePriceDisambiguation locks in the
// mixed-price failure mode from the real TAO.app page: the title/top bar shows
// the broader TAO price, while the body shows the subnet price for Affine.
func TestRunner_EndToEnd_ExternalResearchAffinePriceDisambiguation(t *testing.T) {
	webSearch := agent.Tool{
		Name:        "web_search",
		Description: "Test-only search stand-in: returns a TAO.app page candidate.",
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
  {"title":"Affine SN120 on TAO.app","url":"https://www.tao.app/subnets/120?active_tab=about","snippet":"TAO Price $277.32 in the top bar; body shows Price 0.06342 T, Market Cap 201.04K T, and FDV 1.32M T."},
  {"title":"Affine SN120 metrics","url":"https://metrics.example/affine","snippet":"Subnet metrics page with price, market cap, and 24h volume."}
]`, nil
		},
	}
	webFetch := agent.Tool{
		Name:        "web_fetch",
		Description: "Test-only fetch stand-in: returns deterministic TAO.app page text with two price-like values.",
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
			case "https://www.tao.app/subnets/120?active_tab=about":
				return "SourceAccess: browser_rendered_url=https://www.tao.app/subnets/120?active_tab=about; snapshot_id=14; page_text_below=verified_page_evidence\n" +
					"URL: https://www.tao.app/subnets/120?active_tab=about\n" +
					"TITLE: SN120 - Affine | TAO.app | Your Gateway to Bittensor\n" +
					"[text span] TAO Price $ 277.32 -1.02 % 1D Vol $ 168.66M -39 % MC $ 3.03B FDV $ 5.82B Circ. Supply 10.94M\n" +
					"[text div] Price 0.06342 T/ d L: 0.060 T H: 0.086 T Market Cap 201.04K T FDV 1.32M T\n", nil
			default:
				return "", fmt.Errorf("unexpected test URL %q", p.URL)
			}
		},
	}

	turn1 := []string{
		`{"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"s1","type":"function","function":{"name":"web_search","arguments":"{\"query\":\"Affine SN120 TAO.app price market cap subnet price\",\"num_results\":5}"}}]},"finish_reason":null}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`[DONE]`,
	}
	turn2 := []string{
		`{"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"f1","type":"function","function":{"name":"web_fetch","arguments":"{\"url\":\"https://www.tao.app/subnets/120?active_tab=about\"}"}}]},"finish_reason":null}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`[DONE]`,
	}
	turn3 := []string{
		`{"choices":[{"delta":{"role":"assistant","content":"The TAO.app page shows two separate price-like values and they should not be conflated. The title/top-bar value is TAO Price $277.32, which is the broader TAO token price shown by the site chrome. The subnet/body value is Price 0.06342 T, which is the Affine SN120 subnet price in the page content. The body also shows Market Cap 201.04K T and FDV 1.32M T, while the larger MC $3.03B value belongs to the TAO top bar. So the subnet price is 0.06342 T, not $277.32."},"finish_reason":"stop"}]}`,
		`[DONE]`,
	}
	srv := newScriptedLLM(t, [][]string{turn1, turn2, turn3})
	searchArgs := func(args map[string]any) bool {
		q, _ := args["query"].(string)
		return strings.Contains(q, "Affine") && strings.Contains(q, "TAO.app") && strings.Contains(q, "price")
	}
	fetchURL := func(url string) func(map[string]any) bool {
		return func(args map[string]any) bool { return args["url"] == url }
	}

	scenario := Scenario{
		Name:         "external_research_affine_price_disambiguation",
		Description:  "agent must keep the TAO top-bar price separate from the Affine subnet/body price",
		Prompt:       "Affine is a Bittensor SN120 subnet. Read the TAO.app page and report the different price-like values without conflating them. Use the visible labels exactly.",
		MaxTurnSteps: 6,
		Checks: []Check{
			TurnEndedCleanly(),
			ToolCalled("web_search", searchArgs),
			ToolResultContains("web_search", "TAO Price $277.32"),
			ToolCalled("web_fetch", fetchURL("https://www.tao.app/subnets/120?active_tab=about")),
			ToolStatsAtLeast("source_access_results", 1),
			ToolStatsAtLeast("source_access_verified", 1),
			ToolCalledBeforeMatching("web_search", searchArgs, "web_fetch", fetchURL("https://www.tao.app/subnets/120?active_tab=about")),
			FinalTextContains("TAO Price $277.32"),
			FinalTextContains("Price 0.06342 T"),
			FinalTextContains("Market Cap 201.04K T"),
			FinalTextContains("FDV 1.32M T"),
			FinalTextContains("broader TAO token price"),
			FinalTextContains("Affine SN120 subnet price"),
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
	if strings.Contains(out.Trace.FinalText, "TAO Price $277.32 is the Affine price") ||
		strings.Contains(out.Trace.FinalText, "0.06342 T is the broader TAO price") {
		t.Fatalf("final text conflated the two price-like values:\n%s", out.Trace.FinalText)
	}
}
