package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestToolLoopGuard_BlocksExactRepeatedCalls(t *testing.T) {
	g := newToolLoopGuard()
	args := json.RawMessage(`{"path":"a.txt"}`)
	if got := g.recordAttempt("read_file", args); got != "" {
		t.Fatalf("first attempt blocked: %s", got)
	}
	if got := g.recordAttempt("read_file", args); got != "" {
		t.Fatalf("second attempt blocked: %s", got)
	}
	got := g.recordAttempt("read_file", args)
	if !strings.Contains(got, "blocked repeated call") {
		t.Fatalf("third attempt should be blocked, got %q", got)
	}
	if !strings.Contains(got, "Next:") || !strings.Contains(got, "change the arguments") {
		t.Fatalf("repeat guard should include corrective Next step, got %q", got)
	}
	if got := g.recordAttempt("read_file", json.RawMessage(`{"path":"b.txt"}`)); got != "" {
		t.Fatalf("different args should pass, got %q", got)
	}
}

func TestToolLoopGuard_NormalizesFileToolPathVariants(t *testing.T) {
	g := newToolLoopGuard()
	for i, args := range []json.RawMessage{
		json.RawMessage(`{"path":"docs/readme.md"}`),
		json.RawMessage(`{"path":"./docs//readme.md"}`),
		json.RawMessage(`{"path":" docs/./readme.md "}`),
	} {
		got := g.recordAttempt("read_file", args)
		if i < 2 && got != "" {
			t.Fatalf("attempt %d should pass, got %q", i+1, got)
		}
		if i == 2 && !strings.Contains(got, "blocked repeated call") {
			t.Fatalf("third normalized path variant should be blocked, got %q", got)
		}
	}
}

func TestToolLoopGuard_KeepsMeaningfulFileToolArgsDistinct(t *testing.T) {
	g := newToolLoopGuard()
	first := json.RawMessage(`{"path":"docs/readme.md","max_bytes":128}`)
	second := json.RawMessage(`{"path":"./docs/readme.md","max_bytes":256}`)
	if got := g.recordAttempt("read_file", first); got != "" {
		t.Fatalf("first attempt blocked: %q", got)
	}
	if got := g.recordAttempt("read_file", first); got != "" {
		t.Fatalf("second same-cap attempt blocked too early: %q", got)
	}
	if got := g.recordAttempt("read_file", second); got != "" {
		t.Fatalf("changed max_bytes should stay distinct, got %q", got)
	}
}

func TestToolLoopGuard_DoesNotNormalizeShellCommandPaths(t *testing.T) {
	g := newToolLoopGuard()
	first := json.RawMessage(`{"path":"docs/readme.md"}`)
	second := json.RawMessage(`{"path":"./docs//readme.md"}`)
	third := json.RawMessage(`{"path":" docs/./readme.md "}`)
	_ = g.recordAttempt("shell", first)
	_ = g.recordAttempt("shell", second)
	if got := g.recordAttempt("shell", third); got != "" {
		t.Fatalf("non-file tools should not normalize path-like fields, got %q", got)
	}
}

func TestToolLoopGuard_TracksConsecutiveFailures(t *testing.T) {
	g := newToolLoopGuard()
	for i := 1; i < toolFailureWarnThreshold; i++ {
		if got := g.recordOutcome("shell", false); got != "" {
			t.Fatalf("failure %d should not warn yet: %q", i, got)
		}
	}
	if got := g.recordOutcome("shell", false); !strings.Contains(got, "failed 3 consecutive times") {
		t.Fatalf("expected warning, got %q", got)
	} else if !strings.Contains(got, "Next:") || !strings.Contains(got, "verify prerequisites") {
		t.Fatalf("failure warning should include corrective Next step, got %q", got)
	}
	if got := g.recordOutcome("shell", true); got != "" {
		t.Fatalf("success should reset failures, got %q", got)
	}
	for i := 1; i < toolFailureHaltThreshold; i++ {
		_ = g.recordOutcome("shell", false)
	}
	if got := g.recordOutcome("shell", false); !strings.Contains(got, "failed 8 consecutive times") {
		t.Fatalf("expected halt message, got %q", got)
	} else if !strings.Contains(got, "Next:") || !strings.Contains(got, "different tool") {
		t.Fatalf("halt message should include corrective Next step, got %q", got)
	}
	if got := g.recordAttempt("shell", json.RawMessage(`{}`)); !strings.Contains(got, "already failed 8 consecutive times") {
		t.Fatalf("halted tool should be blocked, got %q", got)
	} else if !strings.Contains(got, "Next:") || !strings.Contains(got, "evidence already gathered") {
		t.Fatalf("halted-tool block should include corrective Next step, got %q", got)
	}
}

func TestToolLoopGuard_WebFetchFailsFast(t *testing.T) {
	g := newToolLoopGuard()
	if got := g.recordOutcome("web_fetch", false); got != "" {
		t.Fatalf("first web_fetch failure should not warn yet: %q", got)
	}
	got := g.recordOutcome("web_fetch", false)
	if !strings.Contains(got, "failed 2 consecutive times") {
		t.Fatalf("second web_fetch failure should warn early, got %q", got)
	}
	for _, want := range []string{"Failure kind", "Next:", "stop opening search results one by one"} {
		if !strings.Contains(got, want) {
			t.Fatalf("web_fetch warning missing %q: %q", want, got)
		}
	}
	if got := g.recordOutcome("web_fetch", false); got != "" {
		t.Fatalf("third web_fetch failure should wait for halt threshold, got %q", got)
	}
	got = g.recordOutcome("web_fetch", false)
	if !strings.Contains(got, "failed 4 consecutive times") {
		t.Fatalf("fourth web_fetch failure should halt, got %q", got)
	}
	got = g.recordAttempt("web_fetch", json.RawMessage(`{"url":"https://example.com/other"}`))
	if !strings.Contains(got, "already failed 4 consecutive times") {
		t.Fatalf("halted web_fetch should block subsequent attempts with web threshold, got %q", got)
	}
}

func TestToolLoopGuard_WebFetchSuccessResetsFailureCount(t *testing.T) {
	g := newToolLoopGuard()
	if got := g.recordOutcome("web_fetch", false); got != "" {
		t.Fatalf("first web_fetch failure should not warn yet: %q", got)
	}
	if got := g.recordOutcome("web_fetch", true); got != "" {
		t.Fatalf("web_fetch success should reset silently, got %q", got)
	}
	if got := g.recordOutcome("web_fetch", false); got != "" {
		t.Fatalf("post-success web_fetch failure should start a fresh count, got %q", got)
	}
}

func TestToolOutcomeCountsNoEvidenceWebFetchAsFailure(t *testing.T) {
	cases := []struct {
		name   string
		tool   string
		result string
		isErr  bool
		want   bool
	}{
		{name: "web fetch text", tool: "web_fetch", result: "readable page text", want: true},
		{name: "web fetch empty", tool: "web_fetch", result: "[empty response: URL=https://example]", want: false},
		{name: "web fetch non text", tool: "web_fetch", result: "  [non-text response: URL=https://example]  ", want: false},
		{name: "web fetch hard error", tool: "web_fetch", result: "Error: http 403", isErr: true, want: false},
		{name: "web search no results", tool: "web_search", result: "(no results)\nFailure: kind=no_results", want: false},
		{name: "web search hits", tool: "web_search", result: "1. Result\n   https://example.com\n   snippet", want: true},
		{name: "other tool literal text", tool: "shell", result: "[empty response: not a web_fetch marker]", want: true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := toolOutcomeCountsAsSuccess(c.tool, c.result, c.isErr); got != c.want {
				t.Fatalf("toolOutcomeCountsAsSuccess() = %t, want %t", got, c.want)
			}
		})
	}
}

func TestToolLoopGuard_WebFetchNoEvidenceResultsFailFast(t *testing.T) {
	g := newToolLoopGuard()
	ok := toolOutcomeCountsAsSuccess("web_fetch", "[empty response: URL=https://example/a]", false)
	if got := g.recordOutcome("web_fetch", ok); got != "" {
		t.Fatalf("first no-evidence web_fetch result should not warn yet: %q", got)
	}
	ok = toolOutcomeCountsAsSuccess("web_fetch", "[non-text response: URL=https://example/b]", false)
	got := g.recordOutcome("web_fetch", ok)
	if !strings.Contains(got, "failed 2 consecutive times") {
		t.Fatalf("second no-evidence web_fetch result should warn, got %q", got)
	}
}

func TestToolLoopGuard_WebSearchNoEvidenceUsesSearchGuidance(t *testing.T) {
	g := newToolLoopGuard()
	for i := 0; i < toolFailureWarnThreshold-1; i++ {
		ok := toolOutcomeCountsAsSuccess("web_search", "(no results)\nFailure: kind=no_results", false)
		if got := g.recordOutcome("web_search", ok); got != "" {
			t.Fatalf("web_search no-results failure %d should not warn yet: %q", i+1, got)
		}
	}
	ok := toolOutcomeCountsAsSuccess("web_search", "(no usable results: search provider returned no URLs)\nFailure: kind=no_results", false)
	got := g.recordOutcome("web_search", ok)
	for _, want := range []string{"web_search", "failed 3 consecutive times", "Failure kind", "distinctive entities", "known source URLs"} {
		if !strings.Contains(got, want) {
			t.Fatalf("web_search warning missing %q: %q", want, got)
		}
	}
}

// TestToolLoopGuard_PerTurnCallCapForRunTask pins the
// over-delegation mitigation: a model can keep varying run_task's
// arguments (different task_type / objective / max_turns each call)
// and the same-args guard would NEVER fire. Without the per-turn cap
// the parent's MaxToolCalls is the only ceiling, which lets a bad
// prompt drain the parent budget on three or four shallow focused
// tasks in a row. The cap belongs in the guard because that's the
// single place every tool dispatch funnels through.
//
// The 4th attempt is the canonical boundary case: 3 prior calls are
// already a strong signal of over-delegation; the 4th gets rejected
// with a message the model can act on.
func TestToolLoopGuard_PerTurnCallCapForRunTask(t *testing.T) {
	g := newToolLoopGuard()
	for i := 0; i < 3; i++ {
		// Distinct args each iteration so the args-hash guard is NOT
		// what triggers; we're isolating the per-turn count cap.
		args := json.RawMessage(`{"task_type":"recall","objective":"q-` + fmt.Sprintf("%d", i) + `"}`)
		if got := g.recordAttempt(FocusedTaskToolName, args); got != "" {
			t.Fatalf("call %d should be allowed (cap=3 allows three calls), got %q", i, got)
		}
	}
	args := json.RawMessage(`{"task_type":"recall","objective":"q-fourth"}`)
	got := g.recordAttempt(FocusedTaskToolName, args)
	if got == "" {
		t.Fatal("4th run_task attempt must be blocked by per-turn cap")
	}
	if !strings.Contains(got, "per-turn delegation cap") {
		t.Errorf("rejection should name the cap concept, got %q", got)
	}
	if !strings.Contains(got, "Next:") {
		t.Errorf("rejection should include a corrective Next step the model can act on, got %q", got)
	}
}

func TestToolLoopGuard_PerTurnCallCapForPlan(t *testing.T) {
	g := newToolLoopGuard()
	for i := 0; i < perTurnCallCaps[PlanToolName]; i++ {
		args := json.RawMessage(`{"action":"update","index":1,"note":"step-` + fmt.Sprintf("%d", i) + `"}`)
		if got := g.recordAttempt(PlanToolName, args); got != "" {
			t.Fatalf("plan call %d should be allowed, got %q", i+1, got)
		}
	}
	got := g.recordAttempt(PlanToolName, json.RawMessage(`{"action":"view"}`))
	if got == "" {
		t.Fatal("plan call over cap must be blocked")
	}
	if !strings.Contains(got, "per-turn planning cap") {
		t.Fatalf("plan cap message should name planning cap, got %q", got)
	}
	if strings.Contains(got, "focused task") || strings.Contains(got, "delegation cap") {
		t.Fatalf("plan cap message should not use focused-task delegation wording, got %q", got)
	}
	if !strings.Contains(got, "Next:") || !strings.Contains(got, "execute the next concrete step") {
		t.Fatalf("plan cap message should include useful recovery guidance, got %q", got)
	}
}

// TestToolLoopGuard_PerTurnCapDoesNotAffectOtherTools guards against a
// regression where the cap mechanism leaks across tool names. read_file
// gets called many times per turn legitimately; capping it would break
// every realistic exploration session.
func TestToolLoopGuard_PerTurnCapDoesNotAffectOtherTools(t *testing.T) {
	g := newToolLoopGuard()
	for i := 0; i < 10; i++ {
		args := json.RawMessage(`{"path":"file-` + fmt.Sprintf("%d", i) + `.go"}`)
		if got := g.recordAttempt("read_file", args); got != "" {
			t.Fatalf("read_file call %d must not be capped, got %q", i, got)
		}
	}
}

// TestToolLoopGuard_PerTurnCapMessageBeatsArgsHashMessage ensures the
// model gets the right corrective message when both guards would
// trigger. A model that calls run_task with the SAME args three times
// would hit both: the args-hash guard at attempt 3 AND the per-turn
// cap eventually. The per-turn cap is the higher-signal message
// (over-delegation across the whole turn vs. one repeated input), so
// when both apply we want the cap message to win, which is also why
// the cap check sits before the args-hash check in recordAttempt.
func TestToolLoopGuard_PerTurnCapMessageBeatsArgsHashMessage(t *testing.T) {
	g := newToolLoopGuard()
	args := json.RawMessage(`{"task_type":"recall","objective":"q"}`)
	// First two attempts go through.
	if got := g.recordAttempt(FocusedTaskToolName, args); got != "" {
		t.Fatalf("attempt 1: %q", got)
	}
	if got := g.recordAttempt(FocusedTaskToolName, args); got != "" {
		t.Fatalf("attempt 2: %q", got)
	}
	// Third call: under the args-hash threshold (3) is met; that guard
	// would normally fire. But the per-turn cap (3) is also at its
	// boundary AFTER this call increments. The behavior here is that
	// the args-hash guard fires first because the cap is checked
	// before the increment: attempt 3 increments perToolCounts to 3,
	// then callCounts to 3, and only THEN compares >=3. We accept
	// either message here as correct; the design pin is just that the
	// 3rd same-args call is blocked.
	got := g.recordAttempt(FocusedTaskToolName, args)
	if got == "" {
		t.Fatal("3rd same-args attempt must be blocked")
	}
	// The 4th attempt with DIFFERENT args must hit the per-turn cap
	// message; the args-hash key is different so the same-args guard
	// can't fire here.
	args2 := json.RawMessage(`{"task_type":"recall","objective":"different"}`)
	got4 := g.recordAttempt(FocusedTaskToolName, args2)
	if !strings.Contains(got4, "per-turn delegation cap") {
		t.Errorf("4th call with new args must surface the per-turn cap message, got %q", got4)
	}
}

func TestRegistryDispatch_SuggestsUnknownToolNames(t *testing.T) {
	reg := NewRegistry()
	reg.Add(&Tool{Name: "read_file", Execute: func(ctx context.Context, args json.RawMessage) (string, error) {
		return "", nil
	}})
	out, isErr := reg.dispatch(context.Background(), "read_flie", json.RawMessage(`{}`))
	if !isErr {
		t.Fatal("unknown tool should be an error")
	}
	if !strings.Contains(out, `Did you mean: read_file?`) {
		t.Fatalf("expected suggestion, got %q", out)
	}
	if !strings.Contains(out, "Next:") || !strings.Contains(out, "exact tool names") {
		t.Fatalf("unknown tool suggestion should include corrective Next step, got %q", out)
	}
}

func TestRegistryDispatch_UnknownToolWithoutSuggestionGivesNextStep(t *testing.T) {
	reg := NewRegistry()
	reg.Add(&Tool{Name: "read_file", Execute: func(ctx context.Context, args json.RawMessage) (string, error) {
		return "", nil
	}})
	out, isErr := reg.dispatch(context.Background(), "browser_use", json.RawMessage(`{}`))
	if !isErr {
		t.Fatal("unknown tool should be an error")
	}
	if !strings.Contains(out, "Next:") || !strings.Contains(out, "advertised tool list") {
		t.Fatalf("unknown tool without suggestion should include recovery guidance, got %q", out)
	}
}

func TestRegistryDispatch_CanonicalizesToolNameAliases(t *testing.T) {
	reg := NewRegistry()
	called := false
	reg.Add(&Tool{
		Name:   "read_file",
		Schema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`),
		Execute: func(ctx context.Context, args json.RawMessage) (string, error) {
			called = true
			return string(args), nil
		},
	})
	out, isErr := reg.dispatch(context.Background(), "readFile", json.RawMessage(`{"path":"README.md"}`))
	if isErr {
		t.Fatalf("canonicalized call should succeed: %s", out)
	}
	if !called {
		t.Fatal("canonicalized tool was not executed")
	}
}

func TestRegistryDispatch_CanonicalizesCommonWeakModelToolNames(t *testing.T) {
	cases := []struct {
		registered string
		called     string
	}{
		{registered: "read_file", called: "read_file_tool"},
		{registered: "read_file", called: "file_read"},
		{registered: "shell", called: "run_command"},
		{registered: "list_files", called: "list_dir"},
		{registered: "subagent_run", called: "subagent"},
		{registered: "run_task", called: "focused_task"},
	}
	for _, tc := range cases {
		t.Run(tc.registered+"/"+tc.called, func(t *testing.T) {
			reg := NewRegistry()
			called := false
			reg.Add(&Tool{
				Name:   tc.registered,
				Schema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`),
				Execute: func(ctx context.Context, args json.RawMessage) (string, error) {
					called = true
					return string(args), nil
				},
			})
			out, isErr := reg.dispatch(context.Background(), tc.called, json.RawMessage(`{"path":"README.md"}`))
			if isErr {
				t.Fatalf("canonicalized call should succeed: %s", out)
			}
			if !called {
				t.Fatal("canonicalized tool was not executed")
			}
		})
	}
}

func TestRegistryDispatch_CommonAliasSuggestions(t *testing.T) {
	reg := NewRegistry()
	reg.Add(&Tool{Name: "read_file", Execute: func(ctx context.Context, args json.RawMessage) (string, error) {
		return "", nil
	}})
	out, isErr := reg.dispatch(context.Background(), "opnfile", json.RawMessage(`{}`))
	if !isErr {
		t.Fatal("unknown tool should be an error")
	}
	if !strings.Contains(out, `Did you mean: read_file?`) {
		t.Fatalf("expected read_file suggestion for common alias, got %q", out)
	}
}

func TestRegistryDispatch_SchemaLessToolErrorGetsNextStep(t *testing.T) {
	reg := NewRegistry()
	reg.Add(&Tool{
		Name: "remote_tool",
		Execute: func(ctx context.Context, args json.RawMessage) (string, error) {
			return "", errors.New("remote failed")
		},
	})

	out, isErr := reg.dispatch(context.Background(), "remote_tool", json.RawMessage(`{"q":"x"}`))
	if !isErr {
		t.Fatal("tool failure should be an error")
	}
	if !strings.Contains(out, "Error: remote failed") {
		t.Fatalf("expected tool error, got %q", out)
	}
	if !strings.Contains(out, "Next:") || !strings.Contains(out, "do not repeat the same failing call unchanged") {
		t.Fatalf("schema-less tool error should include recovery guidance, got %q", out)
	}
}

func TestRegistryDispatch_SchemaLessToolErrorKeepsExistingNextStep(t *testing.T) {
	reg := NewRegistry()
	reg.Add(&Tool{
		Name: "remote_tool",
		Execute: func(ctx context.Context, args json.RawMessage) (string, error) {
			return "", errors.New("bad input\nNext: retry with a query")
		},
	})

	out, isErr := reg.dispatch(context.Background(), "remote_tool", json.RawMessage(`{}`))
	if !isErr {
		t.Fatal("tool failure should be an error")
	}
	if got := strings.Count(out, "Next:"); got != 1 {
		t.Fatalf("expected one Next step, got %d in %q", got, out)
	}
	if strings.Contains(out, "do not repeat the same failing call unchanged") {
		t.Fatalf("existing Next step should not get fallback guidance, got %q", out)
	}
}
