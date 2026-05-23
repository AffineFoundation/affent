package agent

import (
	"context"
	"encoding/json"
	"errors"
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
