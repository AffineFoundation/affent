package agenteval

import (
	"strings"
	"testing"
)

// Each check below is exercised with a hand-built Trace fixture so the
// test is fast (no LLM round-trip, no executor) and focused on the
// predicate's exact contract. The end-to-end Runner→Check flow is
// covered separately in runner_test.go.

func TestToolCalled(t *testing.T) {
	trace := Trace{
		Tools: []ToolCall{
			{CallID: "a", Tool: "read_file", Args: map[string]any{"path": "README.md"}},
			{CallID: "b", Tool: "shell", Args: map[string]any{"command": "go test ./..."}},
		},
	}

	t.Run("matches any invocation", func(t *testing.T) {
		res := ToolCalled("read_file", nil).Eval(trace)
		if !res.Pass {
			t.Errorf("expected pass; got %+v", res)
		}
	})
	t.Run("argMatcher filters invocations", func(t *testing.T) {
		match := func(args map[string]any) bool {
			path, _ := args["path"].(string)
			return strings.HasSuffix(path, "README.md")
		}
		res := ToolCalled("read_file", match).Eval(trace)
		if !res.Pass {
			t.Errorf("expected pass with matching arg; got %+v", res)
		}
	})
	t.Run("argMatcher rejects non-matching", func(t *testing.T) {
		match := func(args map[string]any) bool {
			path, _ := args["path"].(string)
			return strings.HasSuffix(path, ".sql")
		}
		res := ToolCalled("read_file", match).Eval(trace)
		if res.Pass {
			t.Errorf("expected fail when no call matches; got pass")
		}
		if !strings.Contains(res.Detail, "read_file") {
			t.Errorf("detail should name the missing tool: %s", res.Detail)
		}
	})
	t.Run("fails when tool never called", func(t *testing.T) {
		res := ToolCalled("edit_file", nil).Eval(trace)
		if res.Pass {
			t.Errorf("expected fail; got pass")
		}
	})
	t.Run("fail detail summarizes observed tools", func(t *testing.T) {
		res := ToolCalled("edit_file", nil).Eval(trace)
		if !strings.Contains(res.Detail, "read_file") || !strings.Contains(res.Detail, "shell") {
			t.Errorf("detail should list observed tools: %s", res.Detail)
		}
	})
}

func TestToolNotCalled(t *testing.T) {
	trace := Trace{
		Tools: []ToolCall{
			{CallID: "a", Tool: "edit_file", Args: map[string]any{"path": "main_test.go"}},
			{CallID: "b", Tool: "edit_file", Args: map[string]any{"path": "main.go"}},
		},
	}

	t.Run("fails when forbidden tool was called", func(t *testing.T) {
		res := ToolNotCalled("edit_file", nil).Eval(trace)
		if res.Pass {
			t.Errorf("expected fail; got pass")
		}
		if !strings.Contains(res.Detail, "edit_file") {
			t.Errorf("detail should name the forbidden tool: %s", res.Detail)
		}
	})

	t.Run("passes when tool not called at all", func(t *testing.T) {
		res := ToolNotCalled("write_file", nil).Eval(trace)
		if !res.Pass {
			t.Errorf("expected pass; got %+v", res)
		}
	})

	t.Run("argMatcher narrows the prohibition", func(t *testing.T) {
		// Forbid editing test files specifically, not all editing.
		isTestFile := func(args map[string]any) bool {
			p, _ := args["path"].(string)
			return strings.HasSuffix(p, "_test.go")
		}
		res := ToolNotCalled("edit_file", isTestFile).Eval(trace)
		if res.Pass {
			t.Errorf("expected fail (main_test.go edited); got pass")
		}
	})

	t.Run("argMatcher passes when no matching call", func(t *testing.T) {
		isSQLEdit := func(args map[string]any) bool {
			p, _ := args["path"].(string)
			return strings.HasSuffix(p, ".sql")
		}
		res := ToolNotCalled("edit_file", isSQLEdit).Eval(trace)
		if !res.Pass {
			t.Errorf("expected pass (no .sql edits); got %+v", res)
		}
	})
}

func TestToolResultContains(t *testing.T) {
	trace := Trace{Tools: []ToolCall{
		{CallID: "c1", Tool: "read_file", Result: "ok"},
		{CallID: "c2", Tool: "probe", Result: "loop_guard: blocked exact repeated call"},
	}}
	if res := ToolResultContains("probe", "loop_guard: blocked").Eval(trace); !res.Pass {
		t.Fatalf("expected result substring to pass: %+v", res)
	}
	res := ToolResultContains("probe", "missing").Eval(trace)
	if res.Pass {
		t.Fatal("expected missing substring to fail")
	}
	if !strings.Contains(res.Detail, "expected") {
		t.Fatalf("failure detail should explain missing result: %s", res.Detail)
	}
}

func TestToolResultTruncated(t *testing.T) {
	trace := Trace{Tools: []ToolCall{
		{CallID: "c1", Tool: "shell", ResultTruncated: true, ResultOmittedBytes: 4096, ResultCapBytes: 262144},
		{CallID: "c2", Tool: "read_file"},
	}}
	if res := ToolResultTruncated("shell").Eval(trace); !res.Pass {
		t.Fatalf("expected truncated shell result to pass: %+v", res)
	}
	res := ToolResultTruncated("read_file").Eval(trace)
	if res.Pass {
		t.Fatal("expected non-truncated read_file result to fail")
	}
	if !strings.Contains(res.Detail, "event-truncated") {
		t.Fatalf("failure detail should explain missing truncation: %s", res.Detail)
	}
}

func TestToolRequestRepaired(t *testing.T) {
	trace := Trace{Tools: []ToolCall{
		{CallID: "c1", Tool: "read_file", ArgsRepaired: true, RepairNotes: []string{"renamed field file_path to path"}},
	}}
	if res := ToolRequestRepaired("read_file").Eval(trace); !res.Pass {
		t.Fatalf("expected repaired request to pass: %+v", res)
	}
	res := ToolRequestRepaired("shell").Eval(trace)
	if res.Pass {
		t.Fatal("expected missing repaired request to fail")
	}
}

func TestToolStatsAtLeast(t *testing.T) {
	trace := Trace{ToolStats: ToolRuntimeStats{ToolArgsRepaired: 2, ToolErrors: 1, ToolDurationMS: 25}}
	if res := ToolStatsAtLeast("tool_args_repaired", 2).Eval(trace); !res.Pass {
		t.Fatalf("expected stats check to pass: %+v", res)
	}
	if res := ToolStatsAtLeast("tool_errors", 1).Eval(trace); !res.Pass {
		t.Fatalf("expected tool_errors stats check to pass: %+v", res)
	}
	if res := ToolStatsAtLeast("tool_duration_ms", 20).Eval(trace); !res.Pass {
		t.Fatalf("expected tool_duration_ms stats check to pass: %+v", res)
	}
	if res := ToolStatsAtLeast("tool_args_repaired", 3).Eval(trace); res.Pass {
		t.Fatal("expected stats check below threshold to fail")
	}
	if res := ToolStatsAtLeast("bogus", 1).Eval(trace); res.Pass {
		t.Fatal("expected unknown stats field to fail")
	}
}

func TestToolCalledBefore(t *testing.T) {
	t.Run("passes when earlier precedes later", func(t *testing.T) {
		trace := Trace{
			Tools: []ToolCall{
				{Tool: "shell"},     // go test (reproduce)
				{Tool: "read_file"}, // inspect source
				{Tool: "edit_file"}, // patch
				{Tool: "shell"},     // go test (verify)
			},
		}
		res := ToolCalledBefore("shell", "edit_file").Eval(trace)
		if !res.Pass {
			t.Errorf("expected pass; got %+v", res)
		}
	})

	t.Run("fails when later happens first (edit-before-reproduce)", func(t *testing.T) {
		trace := Trace{
			Tools: []ToolCall{
				{Tool: "edit_file"}, // patch
				{Tool: "shell"},     // go test (only after)
			},
		}
		res := ToolCalledBefore("shell", "edit_file").Eval(trace)
		if res.Pass {
			t.Errorf("expected fail (edit before reproduce); got pass")
		}
	})

	t.Run("fails when later never happens", func(t *testing.T) {
		trace := Trace{Tools: []ToolCall{{Tool: "shell"}}}
		res := ToolCalledBefore("shell", "edit_file").Eval(trace)
		if res.Pass {
			t.Errorf("expected fail (no edit_file); got pass")
		}
		if !strings.Contains(res.Detail, "never observed") {
			t.Errorf("detail should say later was never observed: %s", res.Detail)
		}
	})

	t.Run("fails when earlier never happens", func(t *testing.T) {
		trace := Trace{Tools: []ToolCall{{Tool: "edit_file"}}}
		res := ToolCalledBefore("shell", "edit_file").Eval(trace)
		if res.Pass {
			t.Errorf("expected fail; got pass")
		}
	})
}

func TestFinalTextContains(t *testing.T) {
	trace := Trace{FinalText: "Conclusion:\nAll tests pass.\nEvidence: ran go test ./..."}

	t.Run("matches substring", func(t *testing.T) {
		res := FinalTextContains("All tests pass").Eval(trace)
		if !res.Pass {
			t.Errorf("expected pass; got %+v", res)
		}
	})
	t.Run("case-sensitive", func(t *testing.T) {
		res := FinalTextContains("all tests pass").Eval(trace)
		if res.Pass {
			t.Errorf("expected fail (case mismatch); got pass")
		}
	})
	t.Run("fail detail includes preview", func(t *testing.T) {
		res := FinalTextContains("xyzzy").Eval(trace)
		if res.Pass {
			t.Errorf("expected fail; got pass")
		}
		if !strings.Contains(res.Detail, "Conclusion") {
			t.Errorf("detail should include the actual final text: %s", res.Detail)
		}
	})
}

func TestFinalTextLacks(t *testing.T) {
	t.Run("passes when forbidden absent", func(t *testing.T) {
		trace := Trace{FinalText: "All checks passed."}
		res := FinalTextLacks("I cannot help").Eval(trace)
		if !res.Pass {
			t.Errorf("expected pass; got %+v", res)
		}
	})

	t.Run("fails when forbidden present", func(t *testing.T) {
		trace := Trace{FinalText: "I cannot help with that."}
		res := FinalTextLacks("I cannot help").Eval(trace)
		if res.Pass {
			t.Errorf("expected fail; got pass")
		}
	})
}

func TestShellCommandLacks(t *testing.T) {
	t.Run("passes when no shell calls", func(t *testing.T) {
		trace := Trace{Tools: []ToolCall{{Tool: "read_file"}}}
		res := ShellCommandLacks("| head").Eval(trace)
		if !res.Pass {
			t.Errorf("expected pass (no shell calls); got %+v", res)
		}
	})

	t.Run("passes when shell calls don't contain forbidden", func(t *testing.T) {
		trace := Trace{Tools: []ToolCall{
			{Tool: "shell", Args: map[string]any{"command": "go test ./..."}},
		}}
		res := ShellCommandLacks("| head").Eval(trace)
		if !res.Pass {
			t.Errorf("expected pass; got %+v", res)
		}
	})

	t.Run("fails on exit-code-masking pipe", func(t *testing.T) {
		trace := Trace{Tools: []ToolCall{
			{Tool: "shell", Args: map[string]any{"command": "go test ./... | head -50"}},
		}}
		res := ShellCommandLacks("| head").Eval(trace)
		if res.Pass {
			t.Errorf("expected fail (pytest | head pattern); got pass")
		}
		if !strings.Contains(res.Detail, "| head") {
			t.Errorf("detail should name the forbidden substring: %s", res.Detail)
		}
	})

	t.Run("fails on || true exit-code masking", func(t *testing.T) {
		trace := Trace{Tools: []ToolCall{
			{Tool: "shell", Args: map[string]any{"command": "pytest tests/ || true"}},
		}}
		res := ShellCommandLacks("|| true").Eval(trace)
		if res.Pass {
			t.Errorf("expected fail; got pass")
		}
	})

	t.Run("treats non-string command as no-op", func(t *testing.T) {
		// Defensive: if a future tool ships args["command"] as a list
		// or nil, the check must not panic. It just skips that call.
		trace := Trace{Tools: []ToolCall{
			{Tool: "shell", Args: map[string]any{"command": []string{"go", "test"}}},
		}}
		res := ShellCommandLacks("| head").Eval(trace)
		if !res.Pass {
			t.Errorf("non-string command should not match; got fail: %+v", res)
		}
	})
}

func TestTurnEndedCleanly(t *testing.T) {
	cases := []struct {
		reason string
		want   bool
	}{
		{"completed", true},
		{"max_turns", false},
		{"error", false},
		{"cancelled", false},
		{"", false},
	}
	for _, tc := range cases {
		t.Run(tc.reason, func(t *testing.T) {
			res := TurnEndedCleanly().Eval(Trace{TurnEndReason: tc.reason})
			if res.Pass != tc.want {
				t.Errorf("reason=%q want pass=%v got %+v", tc.reason, tc.want, res)
			}
		})
	}
}

func TestMaxToolCalls(t *testing.T) {
	trace := Trace{Tools: []ToolCall{{Tool: "a"}, {Tool: "b"}, {Tool: "c"}}}

	t.Run("passes at cap", func(t *testing.T) {
		if res := MaxToolCalls(3).Eval(trace); !res.Pass {
			t.Errorf("3 tool calls at cap=3 should pass; got %+v", res)
		}
	})
	t.Run("passes under cap", func(t *testing.T) {
		if res := MaxToolCalls(10).Eval(trace); !res.Pass {
			t.Errorf("under-cap should pass; got %+v", res)
		}
	})
	t.Run("fails over cap", func(t *testing.T) {
		if res := MaxToolCalls(2).Eval(trace); res.Pass {
			t.Errorf("over-cap should fail; got pass")
		}
	})
	t.Run("negative cap means unbounded", func(t *testing.T) {
		if res := MaxToolCalls(-1).Eval(trace); !res.Pass {
			t.Errorf("negative cap = unbounded should pass; got %+v", res)
		}
	})
}

func TestMaxSuccessfulToolCalls(t *testing.T) {
	trace := Trace{Tools: []ToolCall{
		{Tool: "list_files", ExitCode: 1, IsErr: true, Result: "first_tool_policy: call subagent_run before other tools"},
		{Tool: "subagent_run", ExitCode: 0},
		{Tool: "read_file", ExitCode: 0},
	}}
	t.Run("ignores guard rejected attempts", func(t *testing.T) {
		if res := MaxSuccessfulToolCalls(2).Eval(trace); !res.Pass {
			t.Fatalf("two successful calls should pass despite one rejected attempt: %+v", res)
		}
	})
	t.Run("fails over cap", func(t *testing.T) {
		if res := MaxSuccessfulToolCalls(1).Eval(trace); res.Pass {
			t.Fatal("expected two successful calls over cap=1 to fail")
		}
	})
	t.Run("negative cap means unbounded", func(t *testing.T) {
		if res := MaxSuccessfulToolCalls(-1).Eval(trace); !res.Pass {
			t.Fatalf("negative cap should pass: %+v", res)
		}
	})
}

// TestOutcomeAggregates pins Outcome.PassCount and Outcome.FailedChecks —
// they're the load-bearing summary methods reporters / next-iteration
// loops will read.
func TestOutcomeAggregates(t *testing.T) {
	o := Outcome{
		Results: []CheckResult{
			{Check: "a", Pass: true},
			{Check: "b", Pass: false, Detail: "boom"},
			{Check: "c", Pass: true},
			{Check: "d", Pass: false},
		},
	}
	if got := o.PassCount(); got != 2 {
		t.Errorf("PassCount = %d, want 2", got)
	}
	failed := o.FailedChecks()
	if len(failed) != 2 || failed[0] != "b" || failed[1] != "d" {
		t.Errorf("FailedChecks = %v, want [b d]", failed)
	}
}

// TestEvaluateChecks_NilEvalIsFailureNotPanic pins the safety floor:
// a Check accidentally registered with Eval=nil must surface as a
// failed CheckResult, not panic mid-eval. Without this the framework
// would crash on operator typos.
func TestEvaluateChecks_NilEvalIsFailureNotPanic(t *testing.T) {
	got := evaluateChecks(Trace{}, []Check{
		{Name: "well_formed", Eval: func(Trace) CheckResult { return CheckResult{Pass: true} }},
		{Name: "broken_check"}, // Eval is nil
	})
	if len(got) != 2 {
		t.Fatalf("expected 2 results, got %d", len(got))
	}
	if !got[0].Pass {
		t.Errorf("well-formed check should pass")
	}
	if got[1].Pass {
		t.Errorf("nil-Eval check should fail, not panic")
	}
	if !strings.Contains(got[1].Detail, "no Eval") {
		t.Errorf("nil-Eval detail should explain the cause: %s", got[1].Detail)
	}
}

func TestShellCommandMatching(t *testing.T) {
	t.Run("regex match", func(t *testing.T) {
		trace := Trace{Tools: []ToolCall{
			{Tool: "shell", Args: map[string]any{"command": "python3 -m pytest tests/"}},
		}}
		if res := ShellCommandMatching(`python(3)? -m pytest`).Eval(trace); !res.Pass {
			t.Errorf("regex should match python3 invocation: %+v", res)
		}
	})
	t.Run("substring fallback when pattern is not a valid regex", func(t *testing.T) {
		trace := Trace{Tools: []ToolCall{
			{Tool: "shell", Args: map[string]any{"command": "go test ./... -count=1"}},
		}}
		if res := ShellCommandMatching("go test ./...").Eval(trace); !res.Pass {
			t.Errorf("substring fallback should pass: %+v", res)
		}
	})
	t.Run("fails when no command matches", func(t *testing.T) {
		trace := Trace{Tools: []ToolCall{
			{Tool: "shell", Args: map[string]any{"command": "ls -la"}},
		}}
		res := ShellCommandMatching(`go test`).Eval(trace)
		if res.Pass {
			t.Errorf("expected fail; got pass")
		}
		if !strings.Contains(res.Detail, "go test") {
			t.Errorf("detail should name the missing pattern: %s", res.Detail)
		}
	})
	t.Run("ignores tool calls without command arg", func(t *testing.T) {
		trace := Trace{Tools: []ToolCall{
			{Tool: "read_file", Args: map[string]any{"path": "README.md"}},
			{Tool: "shell", Args: map[string]any{"command": "go test ./..."}},
		}}
		if res := ShellCommandMatching("go test").Eval(trace); !res.Pass {
			t.Errorf("non-shell calls should be skipped, not block the match: %+v", res)
		}
	})
}

func TestShellCommandLacksUnguarded(t *testing.T) {
	t.Run("fails on unguarded forbidden", func(t *testing.T) {
		trace := Trace{Tools: []ToolCall{
			{Tool: "shell", Args: map[string]any{"command": "pytest tests/ | head -50"}, ExitCode: 0},
		}}
		res := ShellCommandLacksUnguarded("| head").Eval(trace)
		if res.Pass {
			t.Errorf("unguarded | head should fail; got pass")
		}
	})
	t.Run("ignores guard-rejected attempt", func(t *testing.T) {
		// This is the key contract: the model tried `pytest | head`,
		// the runtime shell guard refused. The check must NOT
		// penalize that — the mechanism worked.
		trace := Trace{Tools: []ToolCall{
			{
				Tool:     "shell",
				Args:     map[string]any{"command": "pytest tests/ | head -50"},
				ExitCode: 1,
				IsErr:    true,
				Result:   "Error: shell command masks a test/build exit code",
			},
		}}
		if res := ShellCommandLacksUnguarded("| head").Eval(trace); !res.Pass {
			t.Errorf("guard-rejected attempts must not fail this check: %+v", res)
		}
	})
	t.Run("ignores guard-rejected broad scan", func(t *testing.T) {
		trace := Trace{Tools: []ToolCall{
			{
				Tool:     "shell",
				Args:     map[string]any{"command": "find / -name go"},
				ExitCode: 1,
				IsErr:    true,
				Result:   "Error: shell command looks like an unbounded filesystem scan.",
			},
		}}
		if res := ShellCommandLacksUnguarded("find /").Eval(trace); !res.Pass {
			t.Errorf("guard-rejected find / must not fail check: %+v", res)
		}
	})
	t.Run("case-insensitive substring", func(t *testing.T) {
		trace := Trace{Tools: []ToolCall{
			{Tool: "shell", Args: map[string]any{"command": "echo XYZ || True"}, ExitCode: 0},
		}}
		// Forbidden written lowercase; command uses "True". Match should still fire.
		res := ShellCommandLacksUnguarded("|| true").Eval(trace)
		if res.Pass {
			t.Errorf("case-insensitive match should fire; got pass")
		}
	})
}

func TestFileNotEdited(t *testing.T) {
	t.Run("fails when protected file edited", func(t *testing.T) {
		trace := Trace{Tools: []ToolCall{
			{Tool: "edit_file", Args: map[string]any{"path": "pkg/main_test.go"}},
		}}
		res := FileNotEdited([]string{"main_test.go"}).Eval(trace)
		if res.Pass {
			t.Errorf("path suffix match should fire; got pass")
		}
		if !strings.Contains(res.Detail, "main_test.go") {
			t.Errorf("detail should name the file: %s", res.Detail)
		}
	})
	t.Run("fails for write_file too, not just edit_file", func(t *testing.T) {
		trace := Trace{Tools: []ToolCall{
			{Tool: "write_file", Args: map[string]any{"path": "main_test.go", "content": "..."}},
		}}
		if res := FileNotEdited([]string{"main_test.go"}).Eval(trace); res.Pass {
			t.Errorf("write_file on protected path should fail; got pass")
		}
	})
	t.Run("passes when only non-protected files edited", func(t *testing.T) {
		trace := Trace{Tools: []ToolCall{
			{Tool: "edit_file", Args: map[string]any{"path": "main.go"}},
		}}
		if res := FileNotEdited([]string{"main_test.go"}).Eval(trace); !res.Pass {
			t.Errorf("editing impl file must pass; got fail: %+v", res)
		}
	})
	t.Run("exact match (no suffix collision)", func(t *testing.T) {
		// Suffix matching is "ends with /name" so "go" doesn't catch
		// "main.go"; only "go" at workspace root or under a dir.
		trace := Trace{Tools: []ToolCall{
			{Tool: "edit_file", Args: map[string]any{"path": "main.go"}},
		}}
		if res := FileNotEdited([]string{"go"}).Eval(trace); !res.Pass {
			t.Errorf(`unrelated edits must not match short name; got fail: %+v`, res)
		}
	})
}

// TestEvaluateChecks_FillsCheckNameIfEvalForgot pins the small UX
// guarantee: a Check's Eval that returns CheckResult{Pass:true} without
// setting Check still produces a named result. Otherwise reports look
// like rows of empty-name checks.
func TestEvaluateChecks_FillsCheckNameIfEvalForgot(t *testing.T) {
	got := evaluateChecks(Trace{}, []Check{
		{
			Name: "my_check",
			Eval: func(Trace) CheckResult { return CheckResult{Pass: true} }, // no Check set
		},
	})
	if got[0].Check != "my_check" {
		t.Errorf("framework should backfill Check name; got %q", got[0].Check)
	}
}
