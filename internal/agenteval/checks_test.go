package agenteval

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/affinefoundation/affent/internal/sse"
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

func TestToolCalledAtLeast(t *testing.T) {
	trace := Trace{
		Tools: []ToolCall{
			{CallID: "a", Tool: "plan"},
			{CallID: "b", Tool: "read_file"},
			{CallID: "c", Tool: "plan"},
		},
	}
	if res := ToolCalledAtLeast("plan", 2).Eval(trace); !res.Pass {
		t.Fatalf("expected plan>=2 to pass: %+v", res)
	}
	res := ToolCalledAtLeast("plan", 3).Eval(trace)
	if res.Pass {
		t.Fatal("expected plan>=3 to fail")
	}
	if !strings.Contains(res.Detail, "at least 3") || !strings.Contains(res.Detail, "read_file") {
		t.Fatalf("failure detail should explain count and observed tools: %s", res.Detail)
	}
}

func TestToolArgContainsAtLeast(t *testing.T) {
	trace := Trace{
		Tools: []ToolCall{
			{CallID: "s1", Tool: "web_search", Args: map[string]any{"query": "Vega Bittensor subnet 88 trend"}},
			{CallID: "s2", Tool: "web_search", Args: map[string]any{"query": "Vega Bittensor subnet 88 official domain"}},
			{CallID: "f1", Tool: "web_fetch", Args: map[string]any{"url": "https://official.example/vega"}},
		},
	}
	if res := ToolArgContainsAtLeast("web_search", "query", "Bittensor", 2).Eval(trace); !res.Pass {
		t.Fatalf("expected Bittensor in both search queries: %+v", res)
	}
	res := ToolArgContainsAtLeast("web_search", "query", "subnet 88", 3).Eval(trace)
	if res.Pass {
		t.Fatal("expected subnet count check to fail")
	}
	for _, want := range []string{"at least 3", "web_search", "query", "subnet 88", "s1", "s2"} {
		if !strings.Contains(res.Detail, want) {
			t.Fatalf("failure detail missing %q: %s", want, res.Detail)
		}
	}
	missing := ToolArgContainsAtLeast("web_fetch", "query", "anything", 1).Eval(trace)
	if missing.Pass || !strings.Contains(missing.Detail, "<missing>") {
		t.Fatalf("missing arg should fail with diagnostic, got %+v", missing)
	}
}

func TestToolCalledAtMost(t *testing.T) {
	trace := Trace{
		Tools: []ToolCall{
			{CallID: "a", Tool: "web_fetch", Args: map[string]any{"url": "https://blocked.example/a"}},
			{CallID: "b", Tool: "web_fetch", Args: map[string]any{"url": "https://ok.example/a"}},
			{CallID: "c", Tool: "web_fetch", Args: map[string]any{"url": "https://blocked.example/a"}},
		},
	}

	if res := ToolCalledAtMost("web_search", 0).Eval(trace); !res.Pass {
		t.Fatalf("expected missing web_search to satisfy at-most 0: %+v", res)
	}
	res := ToolCalledAtMost("web_fetch", 2).Eval(trace)
	if res.Pass {
		t.Fatal("expected web_fetch at-most 2 to fail")
	}
	if !strings.Contains(res.Detail, "at most 2") || !strings.Contains(res.Detail, "a") || !strings.Contains(res.Detail, "c") {
		t.Fatalf("failure detail should explain count and matching call ids: %s", res.Detail)
	}

	blockedURL := func(args map[string]any) bool {
		return args["url"] == "https://blocked.example/a"
	}
	if res := ToolCalledAtMostMatching("web_fetch", 1, blockedURL).Eval(trace); res.Pass {
		t.Fatal("expected repeated blocked URL to fail at-most 1")
	}
	if res := ToolCalledAtMostMatching("web_fetch", 2, blockedURL).Eval(trace); !res.Pass {
		t.Fatalf("expected repeated blocked URL to satisfy at-most 2: %+v", res)
	}
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
		{CallID: "c2", Tool: "probe", Result: "loop_guard: blocked repeated call"},
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

func TestToolResultArtifact(t *testing.T) {
	workspace := t.TempDir()
	payload := "complete oversized output"
	artifactPath := filepath.Join(workspace, ".affent", "artifacts", "tool-results", "000001-c1.txt")
	if err := os.MkdirAll(filepath.Dir(artifactPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(artifactPath, []byte(payload), 0o644); err != nil {
		t.Fatal(err)
	}
	trace := Trace{Tools: []ToolCall{
		{
			CallID:             "c1",
			Tool:               "shell",
			ResultBytes:        len(payload),
			ResultArtifactPath: ".affent/artifacts/tool-results/000001-c1.txt",
		},
		{CallID: "c2", Tool: "read_file"},
	}, WorkspaceDir: workspace}
	if res := ToolResultArtifact("shell").Eval(trace); !res.Pass {
		t.Fatalf("expected shell artifact to pass: %+v", res)
	}
	pathOnlyTrace := Trace{Tools: []ToolCall{
		{CallID: "c1", Tool: "shell", ResultArtifactPath: ".affent/artifacts/tool-results/000001-c1.txt"},
	}}
	if res := ToolResultArtifact("shell").Eval(pathOnlyTrace); !res.Pass {
		t.Fatalf("expected path-only shell artifact to pass: %+v", res)
	}
	res := ToolResultArtifact("read_file").Eval(trace)
	if res.Pass {
		t.Fatal("expected read_file without artifact to fail")
	}
	if !strings.Contains(res.Detail, "artifact path") {
		t.Fatalf("failure detail should explain missing artifact: %s", res.Detail)
	}
}

func TestToolResultArtifactRejectsUnsafeOrMissingArtifacts(t *testing.T) {
	workspace := t.TempDir()
	cases := []struct {
		name string
		call ToolCall
		want string
	}{
		{
			name: "absolute",
			call: ToolCall{CallID: "c1", Tool: "shell", ResultArtifactPath: "/tmp/out.txt"},
			want: "workspace-relative",
		},
		{
			name: "traversal",
			call: ToolCall{CallID: "c1", Tool: "shell", ResultArtifactPath: "../out.txt"},
			want: "escapes workspace",
		},
		{
			name: "missing",
			call: ToolCall{CallID: "c1", Tool: "shell", ResultArtifactPath: ".affent/artifacts/tool-results/missing.txt"},
			want: "not readable",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			trace := Trace{WorkspaceDir: workspace, Tools: []ToolCall{tc.call}}
			res := ToolResultArtifact("shell").Eval(trace)
			if res.Pass {
				t.Fatalf("expected invalid artifact to fail: %+v", res)
			}
			if !strings.Contains(res.Detail, tc.want) {
				t.Fatalf("detail missing %q: %s", tc.want, res.Detail)
			}
		})
	}
}

func TestToolResultArtifactChecksSizeAndSymlinkEscape(t *testing.T) {
	workspace := t.TempDir()
	artifactDir := filepath.Join(workspace, ".affent", "artifacts", "tool-results")
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		t.Fatal(err)
	}
	smallPath := filepath.Join(artifactDir, "small.txt")
	if err := os.WriteFile(smallPath, []byte("small"), 0o644); err != nil {
		t.Fatal(err)
	}
	sizeTrace := Trace{WorkspaceDir: workspace, Tools: []ToolCall{{
		CallID:             "c1",
		Tool:               "shell",
		ResultBytes:        999,
		ResultArtifactPath: ".affent/artifacts/tool-results/small.txt",
	}}}
	res := ToolResultArtifact("shell").Eval(sizeTrace)
	if res.Pass {
		t.Fatalf("expected size mismatch to fail: %+v", res)
	}
	if !strings.Contains(res.Detail, "want result_bytes=999") {
		t.Fatalf("detail should explain size mismatch: %s", res.Detail)
	}

	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	linkPath := filepath.Join(artifactDir, "link.txt")
	if err := os.Symlink(outside, linkPath); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	linkTrace := Trace{WorkspaceDir: workspace, Tools: []ToolCall{{
		CallID:             "c1",
		Tool:               "shell",
		ResultBytes:        len("outside"),
		ResultArtifactPath: ".affent/artifacts/tool-results/link.txt",
	}}}
	res = ToolResultArtifact("shell").Eval(linkTrace)
	if res.Pass {
		t.Fatalf("expected symlink escape to fail: %+v", res)
	}
	if !strings.Contains(res.Detail, "escapes workspace") {
		t.Fatalf("detail should explain symlink escape: %s", res.Detail)
	}
}

func TestToolRequestRepaired(t *testing.T) {
	trace := Trace{Tools: []ToolCall{
		{CallID: "c1", Tool: "read_file", ArgsRepaired: true, RepairNotes: []string{"renamed field file_path to path"}},
		{CallID: "c2", Tool: "shell", RepairNotes: []string{"coerced field timeout_sec to integer"}},
	}}
	if res := ToolRequestRepaired("read_file").Eval(trace); !res.Pass {
		t.Fatalf("expected repaired request to pass: %+v", res)
	}
	if res := ToolRequestRepaired("shell").Eval(trace); !res.Pass {
		t.Fatalf("expected repair notes alone to count as repaired request: %+v", res)
	}
	res := ToolRequestRepaired("write_file").Eval(trace)
	if res.Pass {
		t.Fatal("expected missing repaired request to fail")
	}
}

func TestToolStatsAtLeast(t *testing.T) {
	trace := Trace{ToolStats: ToolRuntimeStats{
		ToolArgsRepaired:          2,
		ToolRepairCalls:           2,
		ToolRepairSucceeded:       1,
		ToolRepairFailed:          1,
		ToolRepairNotes:           3,
		ToolErrors:                1,
		ToolDurationMS:            25,
		SourceAccessResults:       3,
		SourceAccessVerified:      2,
		SourceAccessDiscoveryOnly: 1,
		SourceAccessNetwork:       1,
	}}
	if res := ToolStatsAtLeast("tool_args_repaired", 2).Eval(trace); !res.Pass {
		t.Fatalf("expected stats check to pass: %+v", res)
	}
	if res := ToolStatsAtLeast("tool_repair_notes", 3).Eval(trace); !res.Pass {
		t.Fatalf("expected tool_repair_notes stats check to pass: %+v", res)
	}
	if res := ToolStatsAtLeast("tool_repair_succeeded", 1).Eval(trace); !res.Pass {
		t.Fatalf("expected tool_repair_succeeded stats check to pass: %+v", res)
	}
	if res := ToolStatsAtLeast("tool_repair_failed", 1).Eval(trace); !res.Pass {
		t.Fatalf("expected tool_repair_failed stats check to pass: %+v", res)
	}
	if res := ToolStatsAtLeast("tool_errors", 1).Eval(trace); !res.Pass {
		t.Fatalf("expected tool_errors stats check to pass: %+v", res)
	}
	if res := ToolStatsAtLeast("tool_duration_ms", 20).Eval(trace); !res.Pass {
		t.Fatalf("expected tool_duration_ms stats check to pass: %+v", res)
	}
	if res := ToolStatsAtLeast("source_access_verified", 2).Eval(trace); !res.Pass {
		t.Fatalf("expected source_access_verified stats check to pass: %+v", res)
	}
	if res := ToolStatsAtLeast("source_access_discovery_only", 1).Eval(trace); !res.Pass {
		t.Fatalf("expected source_access_discovery_only stats check to pass: %+v", res)
	}
	if res := ToolStatsAtLeast("source_access_network", 1).Eval(trace); !res.Pass {
		t.Fatalf("expected source_access_network stats check to pass: %+v", res)
	}
	if res := ToolStatsAtLeast("tool_args_repaired", 3).Eval(trace); res.Pass {
		t.Fatal("expected stats check below threshold to fail")
	}
	if res := ToolStatsAtLeast("bogus", 1).Eval(trace); res.Pass {
		t.Fatal("expected unknown stats field to fail")
	}
}

func TestToolRepairKindAtLeast(t *testing.T) {
	t.Run("uses runtime repair stats", func(t *testing.T) {
		trace := Trace{ToolStats: ToolRuntimeStats{
			ToolRepairNotes:  3,
			ToolRepairByKind: map[string]int{"enum_normalization": 2, "alias_rename": 1},
		}}
		if res := ToolRepairKindAtLeast("enum_normalization", 2).Eval(trace); !res.Pass {
			t.Fatalf("expected enum_normalization repair kind check to pass: %+v", res)
		}
		res := ToolRepairKindAtLeast("type_coercion", 1).Eval(trace)
		if res.Pass {
			t.Fatal("expected missing repair kind to fail")
		}
		if !strings.Contains(res.Detail, "type_coercion=0") || !strings.Contains(res.Detail, "enum_normalization") {
			t.Fatalf("failure detail should include requested and observed repair kinds: %s", res.Detail)
		}
	})

	t.Run("falls back to request repair notes", func(t *testing.T) {
		trace := Trace{Tools: []ToolCall{
			{CallID: "c1", Tool: "shell", ArgsRepaired: true, RepairNotes: []string{"coerced field limit to integer"}},
		}}
		if res := ToolRepairKindAtLeast("type_coercion", 1).Eval(trace); !res.Pass {
			t.Fatalf("expected request-note repair kind check to pass: %+v", res)
		}
	})
}

func TestToolFailureKindAtLeast(t *testing.T) {
	t.Run("uses turn end stats", func(t *testing.T) {
		trace := Trace{ToolStats: ToolRuntimeStats{
			ToolFailureByKind: map[string]int{"invalid_args": 2, "timeout": 1},
		}}
		if res := ToolFailureKindAtLeast("invalid_args", 2).Eval(trace); !res.Pass {
			t.Fatalf("expected invalid_args failure kind check to pass: %+v", res)
		}
		res := ToolFailureKindAtLeast("empty_response", 1).Eval(trace)
		if res.Pass {
			t.Fatal("expected missing failure kind to fail")
		}
		if !strings.Contains(res.Detail, "empty_response=0") || !strings.Contains(res.Detail, "invalid_args") {
			t.Fatalf("failure detail should include requested and observed failure kinds: %s", res.Detail)
		}
	})

	t.Run("falls back to tool result failure_kind", func(t *testing.T) {
		trace := Trace{Tools: []ToolCall{
			{CallID: "c1", Tool: "web_fetch", ExitCode: 1, FailureKind: "blocked"},
			{CallID: "c2", Tool: "web_fetch", ExitCode: 0, FailureKind: "empty_response"},
			{CallID: "c3", Tool: "web_search", ExitCode: 0, FailureKind: "no_results"},
			{CallID: "c4", Tool: "read_file", ExitCode: 0},
		}}
		if res := ToolFailureKindAtLeast("blocked", 1).Eval(trace); !res.Pass {
			t.Fatalf("expected blocked fallback check to pass: %+v", res)
		}
		if res := ToolFailureKindAtLeast("empty_response", 1).Eval(trace); !res.Pass {
			t.Fatalf("expected empty_response fallback check to pass: %+v", res)
		}
		if res := ToolFailureKindAtLeast("no_results", 1).Eval(trace); !res.Pass {
			t.Fatalf("expected no_results fallback check to pass: %+v", res)
		}
	})

	t.Run("falls back to structured tool result text", func(t *testing.T) {
		trace := Trace{Tools: []ToolCall{
			{CallID: "c1", Tool: "web_fetch", ExitCode: 1, Result: "Error\nFailure: kind=blocked\nNext: use another source"},
			{CallID: "c2", Tool: "web_fetch", ExitCode: 0, Result: "[empty response: URL=https://example]\nFailure: kind=empty_response"},
			{CallID: "c3", Tool: "web_search", ExitCode: 0, Result: "(no results)\nFailure: kind=no_results"},
			{CallID: "c4", Tool: "read_file", ExitCode: 0, Result: "Failure: kind=not_a_tool_failure"},
		}}
		if res := ToolFailureKindAtLeast("blocked", 1).Eval(trace); !res.Pass {
			t.Fatalf("expected text fallback blocked check to pass: %+v", res)
		}
		if res := ToolFailureKindAtLeast("empty_response", 1).Eval(trace); !res.Pass {
			t.Fatalf("expected text fallback empty_response check to pass: %+v", res)
		}
		if res := ToolFailureKindAtLeast("no_results", 1).Eval(trace); !res.Pass {
			t.Fatalf("expected text fallback no_results check to pass: %+v", res)
		}
		if res := ToolFailureKindAtLeast("not_a_tool_failure", 1).Eval(trace); res.Pass {
			t.Fatalf("successful read_file text must not count as failure kind: %+v", res)
		}
	})

	t.Run("falls back to multiple structured tool result kinds", func(t *testing.T) {
		trace := Trace{Tools: []ToolCall{
			{
				CallID:   "c1",
				Tool:     "web_fetch",
				ExitCode: 1,
				Result:   "Error\nFailure: kind=blocked\n\nloop_guard\nFailure: kind=loop_guard_repeated_failures",
			},
		}}
		if res := ToolFailureKindAtLeast("blocked", 1).Eval(trace); !res.Pass {
			t.Fatalf("expected blocked text fallback check to pass: %+v", res)
		}
		if res := ToolFailureKindAtLeast("loop_guard_repeated_failures", 1).Eval(trace); !res.Pass {
			t.Fatalf("expected loop guard text fallback check to pass: %+v", res)
		}
	})
}

func TestToolFailureKindAtMost(t *testing.T) {
	trace := Trace{Tools: []ToolCall{
		{CallID: "c1", Tool: "web_fetch", ExitCode: 0, FailureKind: "empty_response"},
		{CallID: "c2", Tool: "web_fetch", ExitCode: 0, FailureKind: "empty_response"},
	}}
	if res := ToolFailureKindAtMost("empty_response", 2).Eval(trace); !res.Pass {
		t.Fatalf("expected at-most check to pass: %+v", res)
	}
	if res := ToolFailureKindAtMost("dynamic_shell", 0).Eval(trace); !res.Pass {
		t.Fatalf("expected missing kind to pass at max 0: %+v", res)
	}
	if res := ToolFailureKindAtMost("empty_response", 1).Eval(trace); res.Pass {
		t.Fatal("expected at-most check to fail when count is above max")
	}
}

func TestApplyTraceEventDerivesToolResultFailureKind(t *testing.T) {
	trace := Trace{}
	pending := map[string]int{}
	req := json.RawMessage(`{"call_id":"c1","tool":"web_search","args":{"query":"rare topic"}}`)
	if _, err := applyTraceEvent(&trace, pending, sse.TypeToolRequest, req, ""); err != nil {
		t.Fatal(err)
	}
	res := json.RawMessage(`{"call_id":"c1","result":"(no results)\nFailure: kind=no_results","exit_code":0}`)
	if _, err := applyTraceEvent(&trace, pending, sse.TypeToolResult, res, ""); err != nil {
		t.Fatal(err)
	}
	if len(trace.Tools) != 1 || trace.Tools[0].FailureKind != "no_results" {
		t.Fatalf("derived FailureKind = %+v, want no_results", trace.Tools)
	}
	if len(trace.Tools[0].FailureKinds) != 1 || trace.Tools[0].FailureKinds[0] != "no_results" {
		t.Fatalf("derived FailureKinds = %+v, want no_results", trace.Tools[0].FailureKinds)
	}
}

func TestApplyTraceEventReadsToolResultFailureKinds(t *testing.T) {
	trace := Trace{}
	pending := map[string]int{}
	req := json.RawMessage(`{"call_id":"c1","tool":"web_fetch","args":{"url":"https://blocked.example"}}`)
	if _, err := applyTraceEvent(&trace, pending, sse.TypeToolRequest, req, ""); err != nil {
		t.Fatal(err)
	}
	res := json.RawMessage(`{"call_id":"c1","result":"blocked\nFailure: kind=blocked\nFailure: kind=loop_guard_repeated_failed_input","exit_code":1,"failure_kind":"blocked","failure_kinds":["blocked","loop_guard_repeated_failed_input"]}`)
	if _, err := applyTraceEvent(&trace, pending, sse.TypeToolResult, res, ""); err != nil {
		t.Fatal(err)
	}
	if len(trace.Tools) != 1 {
		t.Fatalf("tools = %+v", trace.Tools)
	}
	if trace.Tools[0].FailureKind != "blocked" {
		t.Fatalf("FailureKind = %q, want blocked", trace.Tools[0].FailureKind)
	}
	if got := trace.ToolFailureKindCounts(); got["blocked"] != 1 || got["loop_guard_repeated_failed_input"] != 1 {
		t.Fatalf("ToolFailureKindCounts = %+v", got)
	}
}

func TestTraceToolFailureExamples(t *testing.T) {
	trace := Trace{Tools: []ToolCall{
		{
			Tool:         "web_fetch",
			Args:         map[string]any{"url": "https://dashboard.example/helio", "timeout": 10},
			Result:       "[dynamic page shell: URL=https://dashboard.example/helio]\nFailure: kind=dynamic_shell\nNext: use a text/API/source page.",
			FailureKinds: []string{"dynamic_shell"},
			ExitCode:     0,
		},
		{
			Tool:     "web_search",
			Args:     map[string]any{"query": "rare subnet official metrics"},
			Result:   "(no results)\nFailure: kind=no_results\nNext: retry with official domains.",
			ExitCode: 0,
		},
	}}
	examples := trace.ToolFailureExamples(1)
	dynamic := examples["dynamic_shell"]
	if len(dynamic) != 1 {
		t.Fatalf("dynamic_shell examples = %#v", dynamic)
	}
	if dynamic[0].Tool != "web_fetch" || !strings.Contains(dynamic[0].ArgsSummary, "dashboard.example") {
		t.Fatalf("dynamic_shell example lost tool/URL context: %#v", dynamic[0])
	}
	if !strings.Contains(dynamic[0].ResultSummary, "dynamic page shell") || !strings.Contains(dynamic[0].ResultSummary, "Next:") {
		t.Fatalf("dynamic_shell result summary missing reason/Next: %#v", dynamic[0])
	}
	search := examples["no_results"]
	if len(search) != 1 || !strings.Contains(search[0].ArgsSummary, "rare subnet") {
		t.Fatalf("no_results example missing query context: %#v", search)
	}
}

func TestFocusedTaskCalledAtLeast(t *testing.T) {
	trace := Trace{Tools: []ToolCall{
		{CallID: "c1", Tool: "run_task", Delegation: &sse.DelegationMeta{Kind: "focused_task", TaskType: "explore"}},
		{CallID: "c2", Tool: "run_task", Delegation: &sse.DelegationMeta{Kind: "focused_task", TaskType: "explore"}},
		{CallID: "c3", Tool: "run_task", Delegation: &sse.DelegationMeta{Kind: "focused_task", TaskType: "recall"}},
		{CallID: "c4", Tool: "subagent_run", Delegation: &sse.DelegationMeta{Kind: "subagent", Mode: "review"}},
	}}
	if res := FocusedTaskCalledAtLeast("explore", 2).Eval(trace); !res.Pass {
		t.Fatalf("expected focused explore>=2 check to pass: %+v", res)
	}
	res := FocusedTaskCalledAtLeast("verify", 1).Eval(trace)
	if res.Pass {
		t.Fatal("expected missing focused verify check to fail")
	}
	if !strings.Contains(res.Detail, "verify=0") || !strings.Contains(res.Detail, "explore") {
		t.Fatalf("failure detail should include requested and observed task types: %s", res.Detail)
	}
}

func TestSubagentCalledAtLeast(t *testing.T) {
	trace := Trace{Tools: []ToolCall{
		{CallID: "c1", Tool: "subagent_run", Delegation: &sse.DelegationMeta{Kind: "subagent", Mode: "explore"}},
		{CallID: "c2", Tool: "subagent_run", Delegation: &sse.DelegationMeta{Kind: "subagent", Mode: "review"}},
		{CallID: "c3", Tool: "subagent_run", Delegation: &sse.DelegationMeta{Kind: "subagent", Mode: "review"}},
		{CallID: "c4", Tool: "run_task", Delegation: &sse.DelegationMeta{Kind: "focused_task", TaskType: "review"}},
	}}
	if res := SubagentCalledAtLeast("review", 2).Eval(trace); !res.Pass {
		t.Fatalf("expected subagent review>=2 check to pass: %+v", res)
	}
	res := SubagentCalledAtLeast("test", 1).Eval(trace)
	if res.Pass {
		t.Fatal("expected missing subagent test check to fail")
	}
	if !strings.Contains(res.Detail, "test=0") || !strings.Contains(res.Detail, "review") {
		t.Fatalf("failure detail should include requested and observed subagent modes: %s", res.Detail)
	}
}

func TestNoDelegationErrors(t *testing.T) {
	okTrace := Trace{Tools: []ToolCall{
		{CallID: "c1", Tool: "run_task", Delegation: &sse.DelegationMeta{Kind: "focused_task", TaskType: "explore"}},
		{CallID: "c2", Tool: "subagent_run", Delegation: &sse.DelegationMeta{Kind: "subagent", Mode: "review"}},
	}}
	if res := NoDelegationErrors().Eval(okTrace); !res.Pass {
		t.Fatalf("expected clean delegation trace to pass: %+v", res)
	}

	failedTrace := Trace{Tools: []ToolCall{
		{CallID: "c1", Tool: "run_task", ExitCode: 1, IsErr: true, Delegation: &sse.DelegationMeta{Kind: "focused_task", TaskType: "explore"}},
		{CallID: "c2", Tool: "subagent_run", ExitCode: 1, Delegation: &sse.DelegationMeta{Kind: "subagent", Mode: "review"}},
	}}
	res := NoDelegationErrors().Eval(failedTrace)
	if res.Pass {
		t.Fatal("expected delegation errors check to fail")
	}
	if !strings.Contains(res.Detail, "focused_task_errors=1") || !strings.Contains(res.Detail, "subagent_errors=1") {
		t.Fatalf("failure detail should include both error counts: %s", res.Detail)
	}
}

func TestNoPlanErrors(t *testing.T) {
	okTrace := Trace{Tools: []ToolCall{
		{CallID: "c1", Tool: "plan", Args: map[string]any{"action": "set"}},
		{CallID: "c2", Tool: "plan", Args: map[string]any{"action": "update"}},
	}}
	if res := NoPlanErrors().Eval(okTrace); !res.Pass {
		t.Fatalf("expected clean plan trace to pass: %+v", res)
	}

	failedTrace := Trace{Tools: []ToolCall{
		{CallID: "c1", Tool: "plan", Args: map[string]any{"action": "set"}},
		{CallID: "c2", Tool: "plan", Args: map[string]any{"action": "update"}, ExitCode: 1, IsErr: true},
	}}
	res := NoPlanErrors().Eval(failedTrace)
	if res.Pass {
		t.Fatal("expected plan errors check to fail")
	}
	if !strings.Contains(res.Detail, "plan_errors=1") {
		t.Fatalf("failure detail should include plan error count: %s", res.Detail)
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

func TestToolCalledBeforeMatching(t *testing.T) {
	searchArgs := func(args map[string]any) bool {
		q, _ := args["query"].(string)
		return strings.Contains(q, "Nimbus")
	}
	metricsURL := func(args map[string]any) bool {
		return args["url"] == "https://metrics.example/nimbus"
	}

	t.Run("passes when matching earlier precedes matching later", func(t *testing.T) {
		trace := Trace{
			Tools: []ToolCall{
				{Tool: "web_fetch", Args: map[string]any{"url": "https://unrelated.example"}},
				{Tool: "web_search", Args: map[string]any{"query": "Nimbus Protocol metrics"}},
				{Tool: "web_fetch", Args: map[string]any{"url": "https://metrics.example/nimbus"}},
			},
		}
		res := ToolCalledBeforeMatching("web_search", searchArgs, "web_fetch", metricsURL).Eval(trace)
		if !res.Pass {
			t.Fatalf("expected pass; got %+v", res)
		}
	})

	t.Run("fails when only nonmatching earlier precedes later", func(t *testing.T) {
		trace := Trace{
			Tools: []ToolCall{
				{Tool: "web_search", Args: map[string]any{"query": "other project metrics"}},
				{Tool: "web_fetch", Args: map[string]any{"url": "https://metrics.example/nimbus"}},
			},
		}
		res := ToolCalledBeforeMatching("web_search", searchArgs, "web_fetch", metricsURL).Eval(trace)
		if res.Pass {
			t.Fatal("expected failure for nonmatching earlier call")
		}
		if !strings.Contains(res.Detail, "matching") {
			t.Fatalf("failure detail should mention matching calls: %s", res.Detail)
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

func TestMaxSuccessfulToolCallsForTool(t *testing.T) {
	trace := Trace{Tools: []ToolCall{
		{Tool: "read_file", ExitCode: 0},
		{Tool: "list_files", ExitCode: 0},
		{Tool: "read_file", ExitCode: 1, IsErr: true},
		{Tool: "read_file", ExitCode: 0},
	}}
	t.Run("counts only successful matching tool", func(t *testing.T) {
		if res := MaxSuccessfulToolCallsForTool("read_file", 2).Eval(trace); !res.Pass {
			t.Fatalf("two successful read_file calls should pass cap=2: %+v", res)
		}
	})
	t.Run("fails matching tool over cap", func(t *testing.T) {
		res := MaxSuccessfulToolCallsForTool("read_file", 1).Eval(trace)
		if res.Pass {
			t.Fatal("expected two successful read_file calls over cap=1 to fail")
		}
		if !strings.Contains(res.Detail, `successful "read_file" calls`) {
			t.Fatalf("detail should mention tool-specific count: %s", res.Detail)
		}
	})
	t.Run("ignores other tools", func(t *testing.T) {
		if res := MaxSuccessfulToolCallsForTool("list_files", 1).Eval(trace); !res.Pass {
			t.Fatalf("one successful list_files call should pass: %+v", res)
		}
	})
	t.Run("negative cap means unbounded", func(t *testing.T) {
		if res := MaxSuccessfulToolCallsForTool("read_file", -1).Eval(trace); !res.Pass {
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

func TestShellCommandMatchingAtLeast(t *testing.T) {
	trace := Trace{Tools: []ToolCall{
		{Tool: "shell", Args: map[string]any{"command": "python3 -m pytest tests/"}},
		{Tool: "read_file", Args: map[string]any{"path": "README.md"}},
		{Tool: "shell", Args: map[string]any{"command": "python -m pytest -q"}},
		{Tool: "shell", Args: map[string]any{"command": "go test ./..."}},
	}}
	t.Run("regex count", func(t *testing.T) {
		if res := ShellCommandMatchingAtLeast(`python(3)? -m pytest`, 2).Eval(trace); !res.Pass {
			t.Fatalf("expected two pytest matches: %+v", res)
		}
	})
	t.Run("fails under minimum", func(t *testing.T) {
		res := ShellCommandMatchingAtLeast(`go test`, 2).Eval(trace)
		if res.Pass {
			t.Fatal("expected one go test match below min=2 to fail")
		}
		if !strings.Contains(res.Detail, "observed 1") {
			t.Fatalf("detail should include observed count: %s", res.Detail)
		}
	})
	t.Run("substring fallback", func(t *testing.T) {
		if res := ShellCommandMatchingAtLeast("go test ./...", 1).Eval(trace); !res.Pass {
			t.Fatalf("substring fallback should pass: %+v", res)
		}
	})
	t.Run("non-positive minimum passes", func(t *testing.T) {
		if res := ShellCommandMatchingAtLeast(`missing`, 0).Eval(trace); !res.Pass {
			t.Fatalf("non-positive min should pass: %+v", res)
		}
	})
}

func TestShellCommandMatchingBeforeAfterTool(t *testing.T) {
	trace := Trace{Tools: []ToolCall{
		{Tool: "shell", Args: map[string]any{"command": "go test ./..."}},
		{Tool: "read_file", Args: map[string]any{"path": "calc/calc.go"}},
		{Tool: "edit_file", Args: map[string]any{"path": "calc/calc.go"}},
		{Tool: "shell", Args: map[string]any{"command": "go test ./... -count=1"}},
	}}
	if res := ShellCommandMatchingBeforeTool(`go test`, "edit_file").Eval(trace); !res.Pass {
		t.Fatalf("expected go test before edit_file to pass: %+v", res)
	}
	if res := ShellCommandMatchingAfterTool(`go test`, "edit_file").Eval(trace); !res.Pass {
		t.Fatalf("expected go test after edit_file to pass: %+v", res)
	}

	editFirst := Trace{Tools: []ToolCall{
		{Tool: "edit_file", Args: map[string]any{"path": "calc/calc.go"}},
		{Tool: "shell", Args: map[string]any{"command": "go test ./..."}},
	}}
	res := ShellCommandMatchingBeforeTool(`go test`, "edit_file").Eval(editFirst)
	if res.Pass {
		t.Fatal("expected missing pre-edit command to fail")
	}
	if !strings.Contains(res.Detail, "before") {
		t.Fatalf("detail should explain order failure: %s", res.Detail)
	}

	noPostVerify := Trace{Tools: []ToolCall{
		{Tool: "shell", Args: map[string]any{"command": "go test ./..."}},
		{Tool: "edit_file", Args: map[string]any{"path": "calc/calc.go"}},
	}}
	res = ShellCommandMatchingAfterTool(`go test`, "edit_file").Eval(noPostVerify)
	if res.Pass {
		t.Fatal("expected missing post-edit command to fail")
	}
	if !strings.Contains(res.Detail, "after") {
		t.Fatalf("detail should explain order failure: %s", res.Detail)
	}

	noEdit := Trace{Tools: []ToolCall{
		{Tool: "shell", Args: map[string]any{"command": "go test ./..."}},
	}}
	res = ShellCommandMatchingAfterTool(`go test`, "edit_file").Eval(noEdit)
	if res.Pass {
		t.Fatal("expected missing edit_file to fail")
	}
	if !strings.Contains(res.Detail, "never observed") {
		t.Fatalf("detail should explain missing tool: %s", res.Detail)
	}
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
