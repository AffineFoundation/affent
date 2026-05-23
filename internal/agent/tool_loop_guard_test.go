package agent

import (
	"context"
	"encoding/json"
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
	if !strings.Contains(got, "blocked exact repeated call") {
		t.Fatalf("third attempt should be blocked, got %q", got)
	}
	if got := g.recordAttempt("read_file", json.RawMessage(`{"path":"b.txt"}`)); got != "" {
		t.Fatalf("different args should pass, got %q", got)
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
	}
	if got := g.recordOutcome("shell", true); got != "" {
		t.Fatalf("success should reset failures, got %q", got)
	}
	for i := 1; i < toolFailureHaltThreshold; i++ {
		_ = g.recordOutcome("shell", false)
	}
	if got := g.recordOutcome("shell", false); !strings.Contains(got, "failed 8 consecutive times") {
		t.Fatalf("expected halt message, got %q", got)
	}
	if got := g.recordAttempt("shell", json.RawMessage(`{}`)); !strings.Contains(got, "already failed 8 consecutive times") {
		t.Fatalf("halted tool should be blocked, got %q", got)
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
