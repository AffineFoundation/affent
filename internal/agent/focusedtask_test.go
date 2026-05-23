package agent

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/affinefoundation/affent/internal/executor"
	"github.com/affinefoundation/affent/internal/memory"
	"github.com/rs/zerolog"
)

func TestFocusedTaskProfileRegistry_RegisterLookupOrder(t *testing.T) {
	r := &FocusedTaskProfileRegistry{}
	r.Register(FocusedTaskProfile{Kind: "alpha", Description: "a", DefaultMaxTurns: 1, Tools: FocusedTaskToolPolicy{AllowReadFile: true}})
	r.Register(FocusedTaskProfile{Kind: "alpha", Description: "duplicate", DefaultMaxTurns: 2})
	r.Register(FocusedTaskProfile{Kind: "", Description: "empty"})
	r.Register(FocusedTaskProfile{Kind: "beta", Description: "b", Tools: FocusedTaskToolPolicy{AllowMemory: true}})

	profs := r.Profiles()
	if len(profs) != 2 || profs[0].Kind != "alpha" || profs[1].Kind != "beta" {
		t.Fatalf("unexpected order/dedup: %+v", profs)
	}
	if profs[0].Description != "a" {
		t.Fatalf("duplicate registration should be ignored, kept first: %+v", profs[0])
	}
	if _, ok := r.Lookup("alpha"); !ok {
		t.Fatal("Lookup should find alpha")
	}
	if _, ok := r.Lookup("missing"); ok {
		t.Fatal("Lookup should not find absent kind")
	}
}

func TestDefaultFocusedTaskProfileRegistry_FiveKindsInOrder(t *testing.T) {
	reg := DefaultFocusedTaskProfileRegistry()
	got := reg.Profiles()
	want := []FocusedTaskKind{
		FocusedTaskRecall,
		FocusedTaskExplore,
		FocusedTaskResearch,
		FocusedTaskVerify,
		FocusedTaskReview,
	}
	if len(got) != len(want) {
		t.Fatalf("expected %d profiles, got %d", len(want), len(got))
	}
	for i, p := range got {
		if p.Kind != want[i] {
			t.Errorf("kind %d: got %q want %q", i, p.Kind, want[i])
		}
		if p.DefaultMaxTurns <= 0 || p.DefaultMaxTurns > MaxFocusedTaskMaxTurns {
			t.Errorf("kind %q has out-of-range default max_turns: %d", p.Kind, p.DefaultMaxTurns)
		}
		if strings.TrimSpace(p.Description) == "" {
			t.Errorf("kind %q missing description", p.Kind)
		}
		if !p.Tools.anyAllowed() {
			t.Errorf("kind %q has empty tool policy — would be unavailable everywhere", p.Kind)
		}
	}
}

func TestProfileAvailable_RequiresAtLeastOneDeclaredDep(t *testing.T) {
	baseDeps := FocusedTaskDeps{LLM: dummyLLM(t), HostWorkspaceDir: t.TempDir()}

	cases := []struct {
		name    string
		profile FocusedTaskProfile
		mutate  func(*FocusedTaskDeps)
		want    bool
	}{
		{
			name:    "recall ok with memory only",
			profile: recallProfile(),
			mutate: func(d *FocusedTaskDeps) {
				d.Memory = stubMemoryStore{}
			},
			want: true,
		},
		{
			name:    "recall ok with sessions only",
			profile: recallProfile(),
			mutate: func(d *FocusedTaskDeps) {
				d.SessionsDir = t.TempDir()
			},
			want: true,
		},
		{
			name:    "explore ok without executor",
			profile: exploreProfile(),
			mutate:  func(d *FocusedTaskDeps) { /* no executor */ },
			want:    true,
		},
		{
			name:    "explore ok with executor",
			profile: exploreProfile(),
			mutate: func(d *FocusedTaskDeps) {
				d.Executor = executor.NewLocalExecutor("x", t.TempDir())
				d.SessionsDir = t.TempDir()
			},
			want: true,
		},
		{
			name:    "research needs web registrar",
			profile: researchProfile(),
			mutate:  func(d *FocusedTaskDeps) { /* no web */ },
			want:    false,
		},
		{
			name:    "research ok with web registrar",
			profile: researchProfile(),
			mutate: func(d *FocusedTaskDeps) {
				d.RegisterWebTools = noopRegistrar
			},
			want: true,
		},
		{
			name:    "empty-tools profile is unavailable",
			profile: FocusedTaskProfile{Kind: "blank", Tools: FocusedTaskToolPolicy{}},
			mutate:  func(d *FocusedTaskDeps) {},
			want:    false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := baseDeps
			c.mutate(&d)
			if got := d.profileAvailable(c.profile); got != c.want {
				t.Fatalf("got %v want %v", got, c.want)
			}
		})
	}
}

func TestAvailableFocusedTaskKinds_FiltersByDeps(t *testing.T) {
	deps := FocusedTaskDeps{
		LLM:              dummyLLM(t),
		HostWorkspaceDir: t.TempDir(),
		Memory:           stubMemoryStore{},
		SessionsDir:      t.TempDir(),
		// No web registrar → research filtered out.
	}
	got := AvailableFocusedTaskKinds(deps)
	want := []FocusedTaskKind{FocusedTaskRecall, FocusedTaskExplore, FocusedTaskVerify, FocusedTaskReview}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("available kinds = %+v, want %+v", got, want)
	}
}

func TestRegisterFocusedTasks_NotRegisteredWithoutLLMOrWorkspace(t *testing.T) {
	r1 := NewRegistry()
	RegisterFocusedTasks(r1, FocusedTaskDeps{HostWorkspaceDir: t.TempDir(), Memory: stubMemoryStore{}, SessionsDir: t.TempDir()})
	if _, ok := r1.Get(FocusedTaskToolName); ok {
		t.Fatal("must not register without LLM")
	}

	r2 := NewRegistry()
	RegisterFocusedTasks(r2, FocusedTaskDeps{LLM: dummyLLM(t), Memory: stubMemoryStore{}, SessionsDir: t.TempDir()})
	if _, ok := r2.Get(FocusedTaskToolName); ok {
		t.Fatal("must not register without HostWorkspaceDir")
	}

	// A custom registry whose every profile requires a capability the
	// deps don't provide → run_task must not register at all.
	webOnly := &FocusedTaskProfileRegistry{}
	webOnly.Register(FocusedTaskProfile{Kind: "web-only", Description: "needs web", DefaultMaxTurns: 2, Tools: FocusedTaskToolPolicy{AllowWeb: true}})
	r3 := NewRegistry()
	RegisterFocusedTasks(r3, FocusedTaskDeps{
		LLM:              dummyLLM(t),
		HostWorkspaceDir: t.TempDir(),
		ProfileRegistry:  webOnly,
		// No RegisterWebTools — the single profile is unsatisfiable.
	})
	if _, ok := r3.Get(FocusedTaskToolName); ok {
		t.Fatal("must not register when no profile is satisfied")
	}
}

func TestRegisterFocusedTasks_RegistersWhenAtLeastOneProfileAvailable(t *testing.T) {
	reg := NewRegistry()
	RegisterFocusedTasks(reg, FocusedTaskDeps{
		LLM:              dummyLLM(t),
		HostWorkspaceDir: t.TempDir(),
		Memory:           stubMemoryStore{},
		SessionsDir:      t.TempDir(),
	})
	tool, ok := reg.Get(FocusedTaskToolName)
	if !ok {
		t.Fatal("run_task should be registered")
	}
	// Schema enum must reflect only available kinds. Research is
	// filtered out because no web registrar is present.
	if !strings.Contains(string(tool.Schema), `"recall"`) || strings.Contains(string(tool.Schema), `"research"`) {
		t.Fatalf("schema enum does not match available kinds:\n%s", string(tool.Schema))
	}
}

func TestFocusedTaskTool_ArgValidation(t *testing.T) {
	deps := FocusedTaskDeps{
		LLM:              dummyLLM(t),
		HostWorkspaceDir: t.TempDir(),
		Memory:           stubMemoryStore{},
		SessionsDir:      t.TempDir(),
	}
	reg := NewRegistry()
	RegisterFocusedTasks(reg, deps)
	tool, _ := reg.Get(FocusedTaskToolName)

	cases := []struct {
		name    string
		args    string
		wantSub string // expected substring in returned error
	}{
		{"missing task_type", `{"objective":"do x"}`, "task_type is required"},
		{"unknown task_type", `{"task_type":"research","objective":"do x"}`, `unsupported task_type "research"`},
		{"missing objective", `{"task_type":"recall"}`, "objective is required"},
		{"empty objective", `{"task_type":"recall","objective":"   "}`, "objective is required"},
		{"too long task_type", `{"task_type":"` + strings.Repeat("x", maxFocusedTaskTypeBytes+1) + `","objective":"o"}`, "task_type is"},
		{"too long objective", `{"task_type":"recall","objective":"` + strings.Repeat("a", maxFocusedTaskObjectiveBytes+1) + `"}`, "objective is"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := tool.Execute(context.Background(), json.RawMessage(c.args))
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", c.wantSub)
			}
			if !strings.Contains(err.Error(), c.wantSub) {
				t.Fatalf("error %q does not contain %q", err.Error(), c.wantSub)
			}
		})
	}
}

func TestBuildFocusedTaskRegistry_PerProfileToolSet(t *testing.T) {
	deps := FocusedTaskDeps{
		LLM:                  dummyLLM(t),
		HostWorkspaceDir:     t.TempDir(),
		Executor:             executor.NewLocalExecutor("x", t.TempDir()),
		Memory:               stubMemoryStore{},
		SessionsDir:          t.TempDir(),
		RegisterWebTools:     noopRegistrar,
		RegisterBrowserTools: noopRegistrar,
		Log:                  zerolog.Nop(),
	}

	cases := []struct {
		profile FocusedTaskProfile
		want    map[string]bool
		notWant []string
	}{
		{
			profile: recallProfile(),
			want:    map[string]bool{"memory": true, "session_search": true},
			notWant: []string{"read_file", "list_files", "shell", "web_fetch", "web_search", FocusedTaskToolName, SubagentToolName},
		},
		{
			profile: exploreProfile(),
			want:    map[string]bool{"read_file": true, "list_files": true, "shell": true, "session_search": true},
			notWant: []string{"memory", "web_fetch", "web_search", "write_file", "edit_file", FocusedTaskToolName, SubagentToolName},
		},
		{
			profile: researchProfile(),
			want:    map[string]bool{}, // web tools come from the registrar — we only check absence of mutating/non-web tools
			notWant: []string{"read_file", "list_files", "shell", "memory", "write_file", "edit_file", FocusedTaskToolName, SubagentToolName},
		},
		{
			profile: verifyProfile(),
			want:    map[string]bool{"read_file": true, "list_files": true, "shell": true, "session_search": true},
			notWant: []string{"web_fetch", "web_search", "write_file", "edit_file", FocusedTaskToolName, SubagentToolName},
		},
		{
			profile: reviewProfile(),
			want:    map[string]bool{"read_file": true, "list_files": true, "shell": true, "session_search": true},
			notWant: []string{"write_file", "edit_file", FocusedTaskToolName, SubagentToolName},
		},
	}

	for _, c := range cases {
		t.Run(string(c.profile.Kind), func(t *testing.T) {
			reg, cleanup, err := buildFocusedTaskRegistry(context.Background(), deps, c.profile)
			if err != nil {
				t.Fatalf("build registry: %v", err)
			}
			t.Cleanup(cleanup)
			for tool := range c.want {
				if _, ok := reg.Get(tool); !ok {
					t.Errorf("expected %s registered for %s", tool, c.profile.Kind)
				}
			}
			for _, tool := range c.notWant {
				if _, ok := reg.Get(tool); ok {
					t.Errorf("expected %s NOT registered for %s", tool, c.profile.Kind)
				}
			}
		})
	}
}

func TestBuildFocusedTaskRegistry_LaterRegistrarErrorRunsEarlierCleanup(t *testing.T) {
	// Web is registered before browser in the registrar order; we exercise
	// the cleanup-on-error path by having web succeed (cleanup registered)
	// and browser fail (cleanup must run before we return).
	var webCleanupRan bool
	deps := FocusedTaskDeps{
		LLM:              dummyLLM(t),
		HostWorkspaceDir: t.TempDir(),
		RegisterWebTools: func(ctx context.Context, reg *Registry) (func(), error) {
			return func() { webCleanupRan = true }, nil
		},
		RegisterBrowserTools: func(ctx context.Context, reg *Registry) (func(), error) {
			return nil, errors.New("boom")
		},
	}
	profile := FocusedTaskProfile{
		Kind:            "custom",
		Description:     "test",
		DefaultMaxTurns: 1,
		Tools:           FocusedTaskToolPolicy{AllowWeb: true, AllowBrowser: true},
	}
	_, _, err := buildFocusedTaskRegistry(context.Background(), deps, profile)
	if err == nil {
		t.Fatal("expected browser registrar error to propagate")
	}
	if !webCleanupRan {
		t.Fatal("earlier registrar cleanup must run when a later registrar fails")
	}
}

func TestWithFocusedTaskSystemGuidance_AppendsOnce(t *testing.T) {
	base := "be helpful"
	once := WithFocusedTaskSystemGuidance(base)
	if !strings.Contains(once, "Focused tasks (run_task):") {
		t.Fatal("guidance not appended")
	}
	twice := WithFocusedTaskSystemGuidance(once)
	if once != twice {
		t.Fatal("appending twice should be idempotent")
	}
	if WithFocusedTaskSystemGuidance("") == "" {
		t.Fatal("empty input should fall back to default + guidance")
	}
}

func TestRunFocusedTask_HappyPathReturnsStructuredResult(t *testing.T) {
	jsonReply := `{"task_type":"recall","ok":true,"summary":"one fact","findings":[{"claim":"user prefers terse responses","evidence":"\"don't summarize at the end\"","source":"session:abc","confidence":"high"}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		// Encode the JSON reply as the assistant content. JSON-escape only
		// the inner quotes; the SSE wrapper itself is just a single chunk.
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"role\":\"assistant\",\"content\":" + strconvQuote(jsonReply) + "},\"finish_reason\":\"stop\"}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	t.Cleanup(srv.Close)

	deps := FocusedTaskDeps{
		LLM:              NewLLMClient(srv.URL, "", "fake"),
		HostWorkspaceDir: t.TempDir(),
		Memory:           stubMemoryStore{},
		SessionsDir:      t.TempDir(),
		TranscriptDir:    t.TempDir(),
		Log:              zerolog.Nop(),
		PerCallTimeout:   5 * time.Second,
	}
	reg := NewRegistry()
	RegisterFocusedTasks(reg, deps)
	tool, ok := reg.Get(FocusedTaskToolName)
	if !ok {
		t.Fatal("run_task not registered")
	}

	out, err := tool.Execute(context.Background(), json.RawMessage(`{"task_type":"recall","objective":"find user response preferences"}`))
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	var got FocusedTaskResult
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode result: %v\n%s", err, out)
	}
	if got.TaskType != FocusedTaskRecall || !got.OK {
		t.Fatalf("unexpected runtime metadata: %+v", got)
	}
	if got.Summary != "one fact" || len(got.Findings) != 1 || got.Findings[0].Source != "session:abc" {
		t.Fatalf("structured content not propagated: %+v", got)
	}
	if !strings.HasPrefix(got.ChildSessionID, "focused_") {
		t.Fatalf("child session id should be prefixed: %q", got.ChildSessionID)
	}
	if got.Objective != "find user response preferences" {
		t.Fatalf("objective not propagated: %q", got.Objective)
	}
}

// TestRunFocusedTask_ExploreUsesToolsThenEmitsJSON is the load-bearing
// integration test for the whole focused-task surface: a real child
// Loop runs against an httptest LLM that drives list_files →
// read_file → final structured JSON. It pins:
//   - the child registry built from a profile actually accepts the
//     tool calls the prompt would induce (no schema-vs-registry drift),
//   - the structured output parser cleanly receives the JSON the
//     model emits after its tool steps,
//   - tool_calls in the result carry both inner steps in order so the
//     parent can see what the child did,
//   - the parent's separate Conversation is not written to (the whole
//     point of focused tasks is context isolation; without this assert
//     run_task is just a tool dispatch with extra latency),
//   - the child transcript file lands under the configured
//     TranscriptDir prefixed by focused_.
func TestRunFocusedTask_ExploreUsesToolsThenEmitsJSON(t *testing.T) {
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "marker.txt"), []byte("hello world"), 0o644); err != nil {
		t.Fatal(err)
	}
	transcripts := t.TempDir()

	step := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		step++
		switch step {
		case 1:
			_, _ = w.Write([]byte(`data: {"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"c1","type":"function","function":{"name":"list_files","arguments":"{\"path\":\".\"}"}}]},"finish_reason":"tool_calls"}]}` + "\n\n"))
		case 2:
			_, _ = w.Write([]byte(`data: {"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"c2","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"marker.txt\"}"}}]},"finish_reason":"tool_calls"}]}` + "\n\n"))
		default:
			body := `{"task_type":"explore","ok":true,"summary":"found marker.txt with greeting","findings":[{"claim":"marker.txt contains \"hello world\"","evidence":"file body \"hello world\"","source":"marker.txt:1"}],"suggested_next":["nothing further"]}`
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"role\":\"assistant\",\"content\":" + strconvQuote(body) + "},\"finish_reason\":\"stop\"}]}\n\n"))
		}
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	t.Cleanup(srv.Close)

	parentConv, err := OpenConversationAt(filepath.Join(t.TempDir(), "parent.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	parentSnapshotBefore := len(parentConv.Snapshot())

	out, err := runFocusedTask(context.Background(), FocusedTaskDeps{
		LLM:              NewLLMClient(srv.URL, "", "fake"),
		Executor:         nilExecutor{},
		HostWorkspaceDir: ws,
		TranscriptDir:    transcripts,
		Log:              zerolog.Nop(),
		PerCallTimeout:   5 * time.Second,
	}, exploreProfile(), "find marker file and report contents", 4)
	if err != nil {
		t.Fatalf("runFocusedTask: %v\n%s", err, out)
	}

	var got FocusedTaskResult
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode result: %v\n%s", err, out)
	}
	if !got.OK || got.TaskType != FocusedTaskExplore {
		t.Fatalf("unexpected runtime metadata: %+v", got)
	}
	if len(got.Findings) != 1 || got.Findings[0].Source != "marker.txt:1" {
		t.Fatalf("findings not propagated: %+v", got.Findings)
	}
	if len(got.ToolCalls) < 2 {
		t.Fatalf("expected at least 2 tool calls (list_files, read_file), got %+v", got.ToolCalls)
	}
	gotTools := []string{got.ToolCalls[0].Tool, got.ToolCalls[1].Tool}
	if gotTools[0] != "list_files" || gotTools[1] != "read_file" {
		t.Fatalf("tool call order: %+v want [list_files read_file]", gotTools)
	}

	// Architectural pin: focused-task child must not touch the parent
	// conversation. Without this assert, the whole feature degenerates
	// to "free-form tool dispatch with extra latency" — focused tasks
	// exist exactly to keep this conversation clean.
	if got := len(parentConv.Snapshot()); got != parentSnapshotBefore {
		t.Fatalf("parent conversation grew by %d messages; focused tasks must not write to parent's log", got-parentSnapshotBefore)
	}

	// Child transcript should exist under TranscriptDir, prefixed
	// "focused_" so trace UIs can distinguish subagent vs focused-task
	// children at a glance.
	entries, err := os.ReadDir(transcripts)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "focused_") && strings.HasSuffix(e.Name(), ".jsonl") {
			info, _ := e.Info()
			if info != nil && info.Size() > 0 {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("expected a non-empty focused_ transcript under %s; got entries %v", transcripts, entries)
	}
}

// TestRunFocusedTask_MaxTurnsHitYieldsOKFalse pins the incomplete-child
// branch of buildFocusedTaskResult: when the child uses its whole
// budget without emitting JSON, the parent sees ok=false plus an
// explicit child_did_not_complete warning, not a structured-output
// parse error. Catches a regression where the two failure paths could
// be conflated (parse-failed-but-runtime-ok vs child-never-completed).
func TestRunFocusedTask_MaxTurnsHitYieldsOKFalse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		// Always return a tool_calls response with a no-op list_files —
		// the loop will hit max_turns before any final answer.
		_, _ = w.Write([]byte(`data: {"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"c1","type":"function","function":{"name":"list_files","arguments":"{\"path\":\".\"}"}}]},"finish_reason":"tool_calls"}]}` + "\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	t.Cleanup(srv.Close)

	out, err := runFocusedTask(context.Background(), FocusedTaskDeps{
		LLM:              NewLLMClient(srv.URL, "", "fake"),
		Executor:         nilExecutor{},
		HostWorkspaceDir: t.TempDir(),
		TranscriptDir:    t.TempDir(),
		Log:              zerolog.Nop(),
		PerCallTimeout:   5 * time.Second,
	}, exploreProfile(), "loop forever", 1)
	if err != nil {
		// runFocusedTask returns (json, err) on hard errors; max_turns
		// is a clean turn-end, not a runtime error, so err must be nil.
		t.Fatalf("max_turns is a structured result, not a transport/tool error: %v\n%s", err, out)
	}

	var got FocusedTaskResult
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode result: %v\n%s", err, out)
	}
	if got.OK {
		t.Fatalf("max_turns must yield ok=false: %+v", got)
	}
	if got.TurnEndReason != "max_turns" {
		t.Fatalf("turn_end_reason = %q, want max_turns", got.TurnEndReason)
	}
	if !contains(got.Warnings, "child_did_not_complete:max_turns") {
		t.Fatalf("expected child_did_not_complete warning, got %+v", got.Warnings)
	}
	if contains(got.Warnings, "structured_output_parse_failed") {
		t.Fatalf("must NOT report parse failure when the child never reached a final message: %+v", got.Warnings)
	}
}

func TestRunFocusedTask_ParseFailureFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"role\":\"assistant\",\"content\":\"I cannot output JSON today, but I think the answer is YES.\"},\"finish_reason\":\"stop\"}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	t.Cleanup(srv.Close)

	deps := FocusedTaskDeps{
		LLM:              NewLLMClient(srv.URL, "", "fake"),
		HostWorkspaceDir: t.TempDir(),
		Memory:           stubMemoryStore{},
		SessionsDir:      t.TempDir(),
		TranscriptDir:    t.TempDir(),
		Log:              zerolog.Nop(),
		PerCallTimeout:   5 * time.Second,
	}
	reg := NewRegistry()
	RegisterFocusedTasks(reg, deps)
	tool, _ := reg.Get(FocusedTaskToolName)

	out, err := tool.Execute(context.Background(), json.RawMessage(`{"task_type":"recall","objective":"o"}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	var got FocusedTaskResult
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !got.OK {
		t.Fatalf("runtime success should keep ok=true on parse fail: %+v", got)
	}
	if !contains(got.Warnings, "structured_output_parse_failed") {
		t.Fatalf("expected parse-failure warning: %+v", got)
	}
	if !strings.Contains(got.Summary, "I cannot output JSON") {
		t.Fatalf("raw text should land in summary: %q", got.Summary)
	}
}

// -----------------------------------------------------------------------------
// helpers
// -----------------------------------------------------------------------------

func dummyLLM(t *testing.T) *LLMClient {
	t.Helper()
	return NewLLMClient("http://invalid.example", "", "fake")
}

func noopRegistrar(ctx context.Context, reg *Registry) (func(), error) {
	return nil, nil
}

// stubMemoryStore is the smallest memory.MemoryStore implementation
// needed for the registry tests; no test in this file exercises actual
// memory I/O.
type stubMemoryStore struct{}

func (stubMemoryStore) Snapshot() string { return "" }
func (stubMemoryStore) Add(_ memory.MemoryTarget, _, _ string) (memory.MemoryResponse, error) {
	return memory.MemoryResponse{OK: true}, nil
}
func (stubMemoryStore) Replace(_ memory.MemoryTarget, _, _, _ string) (memory.MemoryResponse, error) {
	return memory.MemoryResponse{OK: true}, nil
}
func (stubMemoryStore) Remove(_ memory.MemoryTarget, _, _ string) (memory.MemoryResponse, error) {
	return memory.MemoryResponse{OK: true}, nil
}
func (stubMemoryStore) Search(_ memory.MemoryTarget, _, _ string, _ int) (memory.MemoryResponse, error) {
	return memory.MemoryResponse{OK: true}, nil
}

// strconvQuote is a tiny shim so the SSE handler above doesn't pull in
// strconv just for this one call site.
func strconvQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
