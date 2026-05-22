package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/affinefoundation/affent/internal/executor"
	"github.com/affinefoundation/affent/internal/sse"
	"github.com/rs/zerolog"
)

func TestSubagentRun_ReturnsStructuredReport(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"role\":\"assistant\",\"content\":\"Conclusion:\\nfound it\\nEvidence:\\n- README says so\"},\"finish_reason\":\"stop\"}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	t.Cleanup(srv.Close)

	reg := NewRegistry()
	RegisterSubagent(reg, SubagentDeps{
		LLM:              NewLLMClient(srv.URL, "", "fake"),
		HostWorkspaceDir: t.TempDir(),
		TranscriptDir:    t.TempDir(),
		Log:              zerolog.Nop(),
		PerCallTimeout:   5 * time.Second,
	})
	tool, ok := reg.Get("subagent_run")
	if !ok {
		t.Fatal("subagent_run was not registered")
	}

	out, err := tool.Execute(context.Background(), json.RawMessage(`{"mode":"explore","task":"inspect README"}`))
	if err != nil {
		t.Fatal(err)
	}
	var resp struct {
		Mode          string `json:"mode"`
		ChildID       string `json:"child_session_id"`
		TurnEndReason string `json:"turn_end_reason"`
		Report        string `json:"report"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("decode response: %v\n%s", err, out)
	}
	if resp.Mode != "explore" || resp.TurnEndReason != "completed" {
		t.Fatalf("unexpected response metadata: %+v", resp)
	}
	if !strings.HasPrefix(resp.ChildID, "subagent_") {
		t.Fatalf("missing child session id: %+v", resp)
	}
	if !strings.Contains(resp.Report, "Conclusion:") || !strings.Contains(resp.Report, "found it") {
		t.Fatalf("report missing model output: %+v", resp)
	}
}

func TestSubagentRun_MaxTurnsReturnsStructuredPartialReport(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`data: {"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"c1","type":"function","function":{"name":"list_files","arguments":"{\"path\":\".\"}"}}]},"finish_reason":null}]}` + "\n\n"))
		_, _ = w.Write([]byte(`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}` + "\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	t.Cleanup(srv.Close)

	reg := NewRegistry()
	RegisterSubagent(reg, SubagentDeps{
		LLM:              NewLLMClient(srv.URL, "", "fake"),
		HostWorkspaceDir: t.TempDir(),
		Log:              zerolog.Nop(),
		PerCallTimeout:   5 * time.Second,
	})
	tool, _ := reg.Get("subagent_run")
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"mode":"explore","task":"loop","max_turns":1}`))
	if err != nil {
		t.Fatalf("max_turns is a structured subagent result, not a transport/tool error: %v\n%s", err, out)
	}
	var resp struct {
		OK            bool   `json:"ok"`
		TurnEndReason string `json:"turn_end_reason"`
		Report        string `json:"report"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("response should still be JSON: %v\n%s", err, out)
	}
	if resp.OK || resp.TurnEndReason != "max_turns" {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if !strings.Contains(resp.Report, "Subagent stopped before producing a complete final answer") {
		t.Fatalf("partial report should explain the incomplete result:\n%s", resp.Report)
	}
	if !strings.Contains(resp.Report, "list_files") {
		t.Fatalf("partial report should include attempted tool evidence:\n%s", resp.Report)
	}
}

func TestSubagentPostPolicyDoesNotActivateForPartialMaxTurnsReport(t *testing.T) {
	resp := subagentResponse{
		Report:        "Conclusion:\nSubagent stopped before producing a complete final answer.",
		OK:            false,
		TurnEndReason: sse.TurnEndMaxTurns,
	}
	raw, err := json.Marshal(resp)
	if err != nil {
		t.Fatal(err)
	}
	if SubagentPostToolPolicy().shouldActivate(string(raw), false) {
		t.Fatal("partial max_turns subagent reports should allow focused parent-side verification")
	}
	if SubagentPostToolPolicy().shouldActivate(string(raw), true) {
		t.Fatal("transport/tool errors should not activate post-tool policy")
	}
}

func TestSubagentPostPolicyBlocksParentBrowserAfterSuccessfulReport(t *testing.T) {
	resp := subagentResponse{
		Report:        "Conclusion:\nThe child gathered the page facts.\nEvidence:\n- browser snapshot",
		OK:            true,
		TurnEndReason: sse.TurnEndCompleted,
	}
	raw, err := json.Marshal(resp)
	if err != nil {
		t.Fatal(err)
	}
	policy := SubagentPostToolPolicy()
	if !policy.shouldActivate(string(raw), false) {
		t.Fatal("successful subagent report should activate post-tool policy")
	}
	blocked := map[string]bool{}
	for _, name := range policy.BlockedTools {
		blocked[name] = true
	}
	for _, name := range []string{"browser_navigate", "browser_snapshot", "browser_wait", "browser_click", "browser_scroll"} {
		if !blocked[name] {
			t.Fatalf("successful subagent report should block parent-side %s repeats; blocked=%v", name, policy.BlockedTools)
		}
	}
}

func TestReadOnlyShellToolRejectsMutatingCommands(t *testing.T) {
	ws := t.TempDir()
	tool := readOnlyShellTool(BuiltinDeps{
		Executor:         nilExecutor{},
		HostWorkspaceDir: ws,
	})
	_, err := tool.Execute(context.Background(), json.RawMessage(`{"command":"rm important.txt"}`))
	if err == nil {
		t.Fatal("expected mutating shell command to be rejected")
	}
	if !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("unexpected error: %v", err)
	}

	_, err = tool.Execute(context.Background(), json.RawMessage(`{"command":"python -m pytest ./...", "cwd":"`+filepath.ToSlash(ws)+`"}`))
	if err != nil {
		t.Fatalf("read-only command should pass guard and reach executor: %v", err)
	}
}

func TestSubagentFileToolsRejectTranscriptPaths(t *testing.T) {
	ws := t.TempDir()
	read := subagentReadFileTool(BuiltinDeps{HostWorkspaceDir: ws})
	_, err := read.Execute(context.Background(), json.RawMessage(`{"path":".affentctl/subagents/parent/child.jsonl"}`))
	if err == nil {
		t.Fatal("expected read_file to reject subagent transcript path")
	}
	if !strings.Contains(err.Error(), "private audit") {
		t.Fatalf("unexpected error: %v", err)
	}

	list := subagentListFilesTool(BuiltinDeps{HostWorkspaceDir: ws})
	_, err = list.Execute(context.Background(), json.RawMessage(`{"path":".affentctl/subagents"}`))
	if err == nil {
		t.Fatal("expected list_files to reject subagent transcript root")
	}

	shell := readOnlyShellTool(BuiltinDeps{
		Executor:         nilExecutor{},
		HostWorkspaceDir: ws,
	})
	_, err = shell.Execute(context.Background(), json.RawMessage(`{"command":"cat .affentctl/subagents/parent/child.jsonl"}`))
	if err == nil {
		t.Fatal("expected shell to reject subagent transcript path")
	}
}

func TestReadOnlyMemoryToolRejectsWrites(t *testing.T) {
	store := newTestStore(t)
	tool := readOnlyMemoryTool(store)
	_, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"add","content":"remember this"}`))
	if err == nil {
		t.Fatal("expected memory write to be rejected")
	}
	if !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestWithSubagentSystemGuidance(t *testing.T) {
	got := WithSubagentSystemGuidance("")
	if !strings.Contains(got, DefaultSystemPrompt) {
		t.Fatal("empty prompt should fall back to DefaultSystemPrompt")
	}
	if !strings.Contains(got, "Subagent delegation:") {
		t.Fatal("guidance missing from default prompt")
	}
	if strings.Count(WithSubagentSystemGuidance(got), "Subagent delegation:") != 1 {
		t.Fatal("guidance should not be appended twice")
	}
	for _, want := range []string{
		"delegate a narrow page/snapshot objective",
		"current-page visible information",
		"not to click tabs",
		"Split cross-tab or multi-page audits",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("subagent guidance missing web delegation constraint %q:\n%s", want, got)
		}
	}
}

func TestSubagentUserPromptIncludesToolBudget(t *testing.T) {
	got := subagentUserPrompt("review", "inspect auth", "/workspace", 6)
	if !strings.Contains(got, "Tool budget: at most 6 tool calls/rounds") {
		t.Fatalf("prompt missing tool budget guidance:\n%s", got)
	}
	if !strings.Contains(got, "Stop early when evidence is sufficient") {
		t.Fatalf("prompt missing early-stop guidance:\n%s", got)
	}
}

func TestSubagentSystemPromptIncludesWebExtractionGuidance(t *testing.T) {
	got := subagentSystemPrompt("explore")
	for _, want := range []string{
		"use the browser tools instead of shell/curl/python scraping",
		"call browser_navigate first",
		"answer directly from the returned snapshot",
		"Do not click through tabs",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("subagent web guidance missing %q:\n%s", want, got)
		}
	}
}

func TestSubagentToolDescriptionMentionsCallerProvidedBrowserTools(t *testing.T) {
	tool := subagentTool(SubagentDeps{
		LLM:              NewLLMClient("http://x", "", "m"),
		HostWorkspaceDir: t.TempDir(),
		Log:              zerolog.Nop(),
	})
	for _, want := range []string{
		"caller-provided extra capabilities",
		"browser_navigate/browser_snapshot",
		"when affentserve runs with --browser",
		"repeating the same file reads/tests/browser steps",
	} {
		if !strings.Contains(tool.Description, want) {
			t.Fatalf("subagent tool description missing %q:\n%s", want, tool.Description)
		}
	}
	raw := string(tool.Schema)
	if !strings.Contains(raw, "current-page visible snapshot facts") {
		t.Fatalf("subagent task schema should guide bounded web delegation:\n%s", raw)
	}
}

// TestBuildSubagentRegistry_HasNoWriteAndNoNestedSubagent pins the
// two load-bearing invariants of the subagent design:
//
//  1. The child cannot recursively spawn another subagent (would
//     produce unbounded fan-out + token spend with no operator
//     visibility).
//  2. The child cannot mutate the workspace (subagent is for
//     exploration / review; writes go through the parent so the
//     audit trail stays linear).
//
// Both invariants are enforced by what is/isn't registered in the
// child's tool registry. A future refactor that adds write_file or
// subagent_run here would silently break the design contract — this
// test catches that at the registry-construction layer instead of
// after the model already triggered a runaway.
func TestBuildSubagentRegistry_HasNoWriteAndNoNestedSubagent(t *testing.T) {
	// All optional deps populated so the maximum-tool variant runs.
	reg := buildSubagentRegistry(SubagentDeps{
		LLM:              NewLLMClient("http://x", "", "m"),
		Executor:         nilExecutor{},
		HostWorkspaceDir: t.TempDir(),
		Memory:           newTestStore(t),
		SessionsDir:      t.TempDir(),
		ParentSessionID:  "parent_test",
		Log:              zerolog.Nop(),
	})
	names := map[string]bool{}
	for _, d := range reg.Defs() {
		names[d.Function.Name] = true
	}
	for _, forbidden := range []string{"subagent_run", "write_file", "edit_file"} {
		if names[forbidden] {
			t.Errorf("subagent must NOT register %q (would break the %s invariant)", forbidden, forbidden)
		}
	}
	// Sanity: the expected read-only set IS present.
	for _, expected := range []string{"read_file", "list_files", "shell", "memory", "session_search"} {
		if !names[expected] {
			t.Errorf("subagent missing expected read-only tool %q", expected)
		}
	}
}

// TestBuildSubagentRegistry_HonorsOptionalDeps verifies the "minimum
// dep" configuration produces the minimum tool set. A subagent
// without an Executor must not get a shell tool; without a Memory it
// must not get memory; without a SessionsDir it must not get
// session_search. These are not just defaults — they're how a
// deployment that intentionally strips a capability ensures the
// child doesn't inherit one anyway.
func TestBuildSubagentRegistry_HonorsOptionalDeps(t *testing.T) {
	// Strip everything optional.
	reg := buildSubagentRegistry(SubagentDeps{
		LLM:              NewLLMClient("http://x", "", "m"),
		HostWorkspaceDir: t.TempDir(),
		Log:              zerolog.Nop(),
	})
	names := map[string]bool{}
	for _, d := range reg.Defs() {
		names[d.Function.Name] = true
	}
	for _, gated := range []string{"shell", "memory", "session_search"} {
		if names[gated] {
			t.Errorf("subagent must NOT register %q without its supporting dep", gated)
		}
	}
	// read_file / list_files don't gate on executor — they always exist.
	for _, always := range []string{"read_file", "list_files"} {
		if !names[always] {
			t.Errorf("subagent must always register %q (no gating dep)", always)
		}
	}
}

// TestSubagentRun_ReportComesFirstInResponse pins the JSON-field
// ordering. The parent Loop truncates tool results to 8 KiB before
// feeding them back to the model; if `tool_calls` (potentially
// thousands of bytes of args / metadata) sits before `report` in
// the JSON, the model never sees the conclusion under a verbose
// child. encoding/json preserves struct-field declaration order, so
// a regression here means a field-shuffling diff that someone might
// otherwise consider cosmetic.
func TestSubagentRun_ReportComesFirstInResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"role\":\"assistant\",\"content\":\"Conclusion:\\nyes\\nEvidence:\\n- ev\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":123,\"completion_tokens\":45}}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	t.Cleanup(srv.Close)

	reg := NewRegistry()
	RegisterSubagent(reg, SubagentDeps{
		LLM:              NewLLMClient(srv.URL, "", "fake"),
		HostWorkspaceDir: t.TempDir(),
		Log:              zerolog.Nop(),
		PerCallTimeout:   5 * time.Second,
	})
	tool, _ := reg.Get("subagent_run")
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"task":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	// The literal byte ordering must put "report" before "tool_calls"
	// in the JSON output.
	reportIdx := strings.Index(out, `"report"`)
	callsIdx := strings.Index(out, `"tool_calls"`)
	if reportIdx < 0 {
		t.Fatalf("no report field in response: %s", out)
	}
	if callsIdx < 0 {
		t.Fatalf("no tool_calls field in response: %s", out)
	}
	if reportIdx > callsIdx {
		t.Errorf("report (%d) must come before tool_calls (%d) so 8 KiB truncation keeps the conclusion visible", reportIdx, callsIdx)
	}
}

// TestSubagentRun_SurfacesUsage pins the token-cost contract. The
// subagent's whole reason for existing is to keep the parent
// context clean, but operators still need visibility into what each
// child cost. Without this, a parent that fires off N subagents has
// no way to budget — token counts only show up in trace events the
// parent never sees.
func TestSubagentRun_SurfacesUsage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		// Final assistant message + a usage chunk before [DONE]. The
		// Loop accumulates usage from the SSE stream's "usage" field.
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"role\":\"assistant\",\"content\":\"Conclusion:\\nfound\"},\"finish_reason\":\"stop\"}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[],\"usage\":{\"prompt_tokens\":777,\"completion_tokens\":42}}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	t.Cleanup(srv.Close)

	reg := NewRegistry()
	RegisterSubagent(reg, SubagentDeps{
		LLM:              NewLLMClient(srv.URL, "", "fake"),
		HostWorkspaceDir: t.TempDir(),
		Log:              zerolog.Nop(),
		PerCallTimeout:   5 * time.Second,
	})
	tool, _ := reg.Get("subagent_run")
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"task":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	var resp subagentResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("decode: %v\n%s", err, out)
	}
	if resp.Usage.InputTokens != 777 || resp.Usage.OutputTokens != 42 {
		t.Errorf("usage = %+v, want {input=777, output=42}", resp.Usage)
	}
}

// TestRunSubagent_DoesNotPolluteParentConversation is the
// architectural pin for the whole feature: the parent's
// conversation log must NOT contain any of the child's intermediate
// tool_request / tool_result events. The child's many file reads,
// shell calls, etc. are by design invisible to the parent — that's
// what "context isolation" actually means. Without this, the
// subagent stops being load-bearing: it's just a wrapped tool
// dispatch with extra latency.
//
// We run an actual subagent_run with a mock LLM that does one
// list_files tool call before its final answer, then assert that
// the parent's separately-created Conversation (which the subagent
// does NOT share) stays empty.
func TestRunSubagent_DoesNotPolluteParentConversation(t *testing.T) {
	ws := t.TempDir()
	// Pre-populate the workspace so the list_files call returns something.
	if err := os.WriteFile(filepath.Join(ws, "marker.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	transcripts := t.TempDir()

	// Mock upstream: first response calls list_files; second response
	// is the final answer. The Loop will append the child's
	// assistant+tool messages to the CHILD conv only.
	step := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		step++
		switch step {
		case 1:
			_, _ = w.Write([]byte(`data: {"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"c1","type":"function","function":{"name":"list_files","arguments":"{\"path\":\".\"}"}}]},"finish_reason":"tool_calls"}]}` + "\n\n"))
		default:
			_, _ = w.Write([]byte(`data: {"choices":[{"delta":{"role":"assistant","content":"Conclusion:\nsaw marker.txt"},"finish_reason":"stop"}]}` + "\n\n"))
		}
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	t.Cleanup(srv.Close)

	// Parent conv lives in its OWN file. Subagent gets its own
	// TranscriptDir for the child conv.
	parentConv, err := OpenConversationAt(filepath.Join(t.TempDir(), "parent.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	parentSnapshotBefore := len(parentConv.Snapshot())

	out, err := runSubagent(context.Background(), SubagentDeps{
		LLM:              NewLLMClient(srv.URL, "", "fake"),
		Executor:         nilExecutor{},
		HostWorkspaceDir: ws,
		TranscriptDir:    transcripts,
		Log:              zerolog.Nop(),
		PerCallTimeout:   5 * time.Second,
	}, "explore", "find marker file", 4)
	if err != nil {
		t.Fatalf("runSubagent: %v\n%s", err, out)
	}

	// Parent conv must be untouched — the subagent never had a handle
	// to it, by design.
	if got := len(parentConv.Snapshot()); got != parentSnapshotBefore {
		t.Errorf("parent conversation grew by %d messages; subagent_run must not write to parent's log", got-parentSnapshotBefore)
	}

	// And the child transcript file should exist with its own
	// turn/tool messages (proving the child DID record everything —
	// it's just isolated from the parent).
	entries, err := os.ReadDir(transcripts)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "subagent_") && strings.HasSuffix(e.Name(), ".jsonl") {
			info, _ := e.Info()
			if info != nil && info.Size() > 0 {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("expected a non-empty subagent transcript under %s; got entries %v", transcripts, entries)
	}
}

func TestRunSubagent_RegisterChildToolsAddsAndCleansExtras(t *testing.T) {
	var cleanupCalled bool
	step := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		step++
		switch step {
		case 1:
			_, _ = w.Write([]byte(`data: {"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"c1","type":"function","function":{"name":"browser_navigate","arguments":"{\"url\":\"https://example.com\"}"}}]},"finish_reason":"tool_calls"}]}` + "\n\n"))
		default:
			_, _ = w.Write([]byte(`data: {"choices":[{"delta":{"role":"assistant","content":"Conclusion:\nbrowser worked\nEvidence:\n- used browser_navigate"},"finish_reason":"stop"}]}` + "\n\n"))
		}
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	t.Cleanup(srv.Close)

	out, err := runSubagent(context.Background(), SubagentDeps{
		LLM:              NewLLMClient(srv.URL, "", "fake"),
		HostWorkspaceDir: t.TempDir(),
		Log:              zerolog.Nop(),
		PerCallTimeout:   5 * time.Second,
		RegisterChildTools: func(ctx context.Context, reg *Registry) (func(), error) {
			reg.Add(&Tool{
				Name:        "browser_navigate",
				Description: "test browser navigate",
				Schema:      json.RawMessage(`{"type":"object","required":["url"],"properties":{"url":{"type":"string"}}}`),
				Execute: func(context.Context, json.RawMessage) (string, error) {
					return "URL: https://example.com\nTitle: Example", nil
				},
			})
			return func() { cleanupCalled = true }, nil
		},
	}, "explore", "open example", 4)
	if err != nil {
		t.Fatalf("runSubagent: %v\n%s", err, out)
	}
	if !cleanupCalled {
		t.Fatal("RegisterChildTools cleanup was not called")
	}
	var resp subagentResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("decode response: %v\n%s", err, out)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Tool != "browser_navigate" {
		t.Fatalf("child extra tool was not executed: %+v", resp.ToolCalls)
	}
}

// TestSubagentTool_InputValidation pins the schema enforcement that
// happens BEFORE we spin up a child Loop:
//
//   - empty task is rejected (a subagent with no task wastes tokens).
//   - unknown mode is rejected (only explore / review are supported;
//     "test", "research", etc. are listed in the design doc as future
//     modes and must not silently fall through with the wrong toolset).
//   - max_turns > maxSubagentMaxTurns is clamped silently (a bounded
//     escape hatch from a confused model asking for max_turns=999).
func TestSubagentTool_InputValidation(t *testing.T) {
	tool := subagentTool(SubagentDeps{
		LLM:              NewLLMClient("http://x", "", "m"),
		HostWorkspaceDir: t.TempDir(),
		Log:              zerolog.Nop(),
	})

	t.Run("empty task is rejected", func(t *testing.T) {
		_, err := tool.Execute(context.Background(), json.RawMessage(`{"task":""}`))
		if err == nil || !strings.Contains(err.Error(), "task is required") {
			t.Errorf("empty task must be rejected; got err=%v", err)
		}
	})

	t.Run("whitespace-only task is rejected", func(t *testing.T) {
		_, err := tool.Execute(context.Background(), json.RawMessage(`{"task":"   "}`))
		if err == nil || !strings.Contains(err.Error(), "task is required") {
			t.Errorf("whitespace-only task must be rejected; got err=%v", err)
		}
	})

	t.Run("unknown mode is rejected", func(t *testing.T) {
		_, err := tool.Execute(context.Background(), json.RawMessage(`{"task":"x","mode":"test"}`))
		if err == nil || !strings.Contains(err.Error(), "unsupported mode") {
			t.Errorf("future-mode 'test' must be rejected until the v0 tool set actually supports it; got err=%v", err)
		}
	})
}

// TestSubagentTool_MaxTurnsClamp verifies that an oversized
// max_turns from the model gets silently clamped to
// maxSubagentMaxTurns instead of returning an error — operators
// would rather have a bounded run than a thrown tool call.
func TestSubagentTool_MaxTurnsClamp(t *testing.T) {
	// Mock LLM that returns immediately so we can observe behavior
	// without burning real budget.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"role\":\"assistant\",\"content\":\"Conclusion:\\nok\"},\"finish_reason\":\"stop\"}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	t.Cleanup(srv.Close)

	tool := subagentTool(SubagentDeps{
		LLM:              NewLLMClient(srv.URL, "", "fake"),
		HostWorkspaceDir: t.TempDir(),
		Log:              zerolog.Nop(),
		PerCallTimeout:   5 * time.Second,
	})
	// max_turns=999 should not error; it should clamp.
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"task":"x","max_turns":999}`))
	if err != nil {
		t.Fatalf("oversized max_turns must clamp, not error: %v", err)
	}
	var resp subagentResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.OK {
		t.Errorf("clamped run should still complete OK; got resp=%+v", resp)
	}
}

func TestRunSubagentCancelsChildLoopWhenParentContextCancels(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-block
	}))
	t.Cleanup(func() {
		close(block)
		srv.Close()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_, err := runSubagent(ctx, SubagentDeps{
		LLM:              NewLLMClient(srv.URL, "", "fake"),
		HostWorkspaceDir: t.TempDir(),
		Log:              zerolog.Nop(),
		PerCallTimeout:   5 * time.Second,
	}, "explore", "inspect", 1)
	if err == nil {
		t.Fatal("expected cancelled context error")
	}
}

type nilExecutor struct{}

func (nilExecutor) SessionID() string { return "test" }

func (nilExecutor) Exec(context.Context, []string, executor.ExecOptions) (executor.ExecResult, error) {
	return executor.ExecResult{ExitCode: 0, Stdout: "ok"}, nil
}
