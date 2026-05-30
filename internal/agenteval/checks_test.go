package agenteval

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/affinefoundation/affent/internal/agent"
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

func TestToolArgContainsAtMost(t *testing.T) {
	trace := Trace{
		Tools: []ToolCall{
			{CallID: "m1", Tool: "memory", Args: map[string]any{"content": "Durable JSON outputs include AUTO-MEM-64"}},
			{CallID: "m2", Tool: "memory", Args: map[string]any{"content": "Repeat AUTO-MEM-64 JSON convention"}},
			{CallID: "m3", Tool: "memory", Args: map[string]any{"content": "Different durable convention"}},
		},
	}
	if res := ToolArgContainsAtMost("memory", "content", "Different", 1).Eval(trace); !res.Pass {
		t.Fatalf("expected single different convention to pass: %+v", res)
	}
	res := ToolArgContainsAtMost("memory", "content", "AUTO-MEM-64", 1).Eval(trace)
	if res.Pass {
		t.Fatal("expected duplicate memory convention check to fail")
	}
	for _, want := range []string{"at most 1", "memory", "content", "AUTO-MEM-64", "m1", "m2"} {
		if !strings.Contains(res.Detail, want) {
			t.Fatalf("failure detail missing %q: %s", want, res.Detail)
		}
	}
}

func TestToolArgLacksSubstring(t *testing.T) {
	trace := Trace{
		Tools: []ToolCall{
			{CallID: "m1", Tool: "memory", Args: map[string]any{"content": "Durable JSON outputs include AUTO-MEM-64"}},
			{CallID: "m2", Tool: "memory", Args: map[string]any{"content": "temporary commit hash abc123"}},
			{CallID: "s1", Tool: "session_search", Args: map[string]any{"query": "commit hash"}},
		},
	}
	if res := ToolArgLacksSubstring("memory", "content", "push result").Eval(trace); !res.Pass {
		t.Fatalf("expected unrelated forbidden substring to pass: %+v", res)
	}
	res := ToolArgLacksSubstring("memory", "content", "commit hash").Eval(trace)
	if res.Pass {
		t.Fatal("expected memory content pollution check to fail")
	}
	for _, want := range []string{"memory", "content", "commit hash", "m2"} {
		if !strings.Contains(res.Detail, want) {
			t.Fatalf("failure detail missing %q: %s", want, res.Detail)
		}
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

func TestFocusedTaskSourceFindingsAtLeast(t *testing.T) {
	trace := Trace{Tools: []ToolCall{
		{
			Tool: "run_task",
			Result: `{"task_type":"research","ok":true,"findings":[` +
				`{"claim":"Claude Code supports subagents","source":"https://code.claude.com/docs/en/subagents"},` +
				`{"claim":"Hermes documents a learning loop","source":"https://hermes-agent.ai/features/learning-loop"},` +
				`{"claim":"unsupported"}]}`,
			Delegation: &sse.DelegationMeta{Kind: "focused_task", TaskType: "research"},
		},
		{
			Tool:       "run_task",
			Result:     `{"task_type":"research","ok":false,"findings":[{"claim":"partial","source":"https://example.test"}]}`,
			Delegation: &sse.DelegationMeta{Kind: "focused_task", TaskType: "research"},
		},
	}}
	if res := FocusedTaskSourceFindingsAtLeast("research", 2).Eval(trace); !res.Pass {
		t.Fatalf("expected two sourced research findings to pass: %+v", res)
	}
	res := FocusedTaskSourceFindingsAtLeast("research", 3).Eval(trace)
	if res.Pass {
		t.Fatal("expected source finding count check to fail")
	}
	if !strings.Contains(res.Detail, "research_source_findings=2") || !strings.Contains(res.Detail, "want >= 3") {
		t.Fatalf("failure detail should explain source finding count: %s", res.Detail)
	}
}

func TestSubagentSourceEvidenceAtLeast(t *testing.T) {
	trace := Trace{Tools: []ToolCall{
		{
			Tool:       "subagent_run",
			Result:     `{"mode":"research","ok":true,"report":"Conclusion:\nexternal calibration found two anchors\nEvidence:\n- source: https://code.claude.com/docs/en/subagents\n- memory topic: loop-route\n- unsourced claim\nFiles inspected:\n- docs/loop.md\nUncertainties:\n- none"}`,
			Delegation: &sse.DelegationMeta{Kind: "subagent", Mode: "research"},
		},
		{
			Tool:       "subagent_run",
			Result:     `{"mode":"research","ok":false,"report":"Conclusion:\npartial\nEvidence:\n- source: https://example.test"}`,
			Delegation: &sse.DelegationMeta{Kind: "subagent", Mode: "research"},
		},
	}}
	if res := SubagentSourceEvidenceAtLeast("research", 3).Eval(trace); !res.Pass {
		t.Fatalf("expected sourced research subagent evidence to pass: %+v", res)
	}
	res := SubagentSourceEvidenceAtLeast("research", 4).Eval(trace)
	if res.Pass {
		t.Fatal("expected source evidence count check to fail")
	}
	if !strings.Contains(res.Detail, "research_source_evidence=3") || !strings.Contains(res.Detail, "want >= 4") {
		t.Fatalf("failure detail should explain source evidence count: %s", res.Detail)
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
		ToolArgsRepaired:           2,
		ToolRepairCalls:            2,
		ToolRepairSucceeded:        1,
		ToolRepairFailed:           1,
		ToolRepairNotes:            3,
		ToolErrors:                 1,
		ToolDurationMS:             25,
		SourceAccessResults:        3,
		SourceAccessVerified:       2,
		SourceAccessDiscoveryOnly:  1,
		SourceAccessNetwork:        1,
		SourceAccessDynamicPartial: 1,
		MemoryUpdates:              2,
		MemoryUpdateAdd:            1,
		MemoryUpdateReplace:        1,
		MemorySearchCalls:          2,
		MemorySearchMisses:         1,
		SessionSearchCalls:         1,
		SessionSearchResults:       3,
		SessionSearchContextHits:   2,
		SessionSearchMatchedTerms:  2,
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
	if res := ToolStatsAtLeast("source_access_dynamic_partial", 1).Eval(trace); !res.Pass {
		t.Fatalf("expected source_access_dynamic_partial stats check to pass: %+v", res)
	}
	if res := ToolStatsAtLeast("memory_updates", 2).Eval(trace); !res.Pass {
		t.Fatalf("expected memory_updates stats check to pass: %+v", res)
	}
	if res := ToolStatsAtLeast("memory_update_replace", 1).Eval(trace); !res.Pass {
		t.Fatalf("expected memory_update_replace stats check to pass: %+v", res)
	}
	if res := ToolStatsAtLeast("memory_search_calls", 2).Eval(trace); !res.Pass {
		t.Fatalf("expected memory_search_calls stats check to pass: %+v", res)
	}
	if res := ToolStatsAtLeast("memory_search_misses", 1).Eval(trace); !res.Pass {
		t.Fatalf("expected memory_search_misses stats check to pass: %+v", res)
	}
	if res := ToolStatsAtLeast("session_search_calls", 1).Eval(trace); !res.Pass {
		t.Fatalf("expected session_search_calls stats check to pass: %+v", res)
	}
	if res := ToolStatsAtLeast("session_search_results", 3).Eval(trace); !res.Pass {
		t.Fatalf("expected session_search_results stats check to pass: %+v", res)
	}
	if res := ToolStatsAtLeast("session_search_context_hits", 2).Eval(trace); !res.Pass {
		t.Fatalf("expected session_search_context_hits stats check to pass: %+v", res)
	}
	if res := ToolStatsAtLeast("session_search_matched_terms", 2).Eval(trace); !res.Pass {
		t.Fatalf("expected session_search_matched_terms stats check to pass: %+v", res)
	}
	if res := ToolStatsAtLeast("tool_args_repaired", 3).Eval(trace); res.Pass {
		t.Fatal("expected stats check below threshold to fail")
	}
	if res := ToolStatsAtLeast("bogus", 1).Eval(trace); res.Pass {
		t.Fatal("expected unknown stats field to fail")
	}
}

func TestMemoryUpdateMetadataAtLeast(t *testing.T) {
	trace := Trace{Tools: []ToolCall{{
		CallID: "mem1",
		Tool:   "memory",
		MemoryUpdate: &sse.MemoryUpdateMeta{
			Action:      "add",
			Target:      "memory",
			Topic:       "markets",
			Location:    "memory:markets",
			NextPreview: "Alpha Coast reports use marker MEM-STOCK-73.",
		},
	}, {
		CallID: "legacy",
		Tool:   "memory",
		Args:   map[string]any{"action": "add", "target": "memory", "topic": "legacy", "content": "legacy only"},
		Result: `{"ok":true,"mutated":true,"target":"memory","topic":"legacy"}`,
	}}}
	if res := MemoryUpdateMetadataAtLeast(1).Eval(trace); !res.Pass {
		t.Fatalf("expected structured memory update metadata to pass: %+v", res)
	}
	if res := MemoryUpdateMetadataAtLeast(2).Eval(trace); res.Pass {
		t.Fatalf("fallback memory result parsing must not count as structured metadata: %+v", res)
	}
}

func TestSourceAccessMatchAtLeast(t *testing.T) {
	trace := Trace{Tools: []ToolCall{
		{
			CallID: "snap",
			Tool:   "browser_snapshot",
			Result: "SourceAccess: browser_rendered_url=https://taostats.io/subnets/120; page_text_below=partial_dynamic_page_evidence; rendered_browser_source_status=partial_dynamic_page_evidence\nPAGE TEXT:\nAffine",
		},
		{
			CallID: "net",
			Tool:   "browser_network_read",
			Result: "SourceAccess: browser_network_url=https://taostats.io/api/subnets/120; requested_url=https://taostats.io/subnets/120; ref=n1; status=200; content_type=application/json; source_method=network_xhr_fetch\nJSON_PATH: $.market_cap\n\"201.04K T\"",
		},
	}}

	if res := SourceAccessMatchAtLeast("network", "browser_network_read", "taostats.io/api", "network_xhr_fetch", "$.market_cap", 1).Eval(trace); !res.Pass {
		t.Fatalf("expected network source access check to pass: %+v", res)
	}
	if res := SourceAccessMatchWithRequestedAtLeast("network", "browser_network_read", "taostats.io/api", "taostats.io/subnets/120", "network_xhr_fetch", "$.market_cap", 1).Eval(trace); !res.Pass {
		t.Fatalf("expected network source access requested-url check to pass: %+v", res)
	}
	if res := SourceAccessMatchWithRequestedAtLeast("network", "browser_network_read", "taostats.io/api", "taostats.io/subnets/999", "network_xhr_fetch", "$.market_cap", 1).Eval(trace); res.Pass {
		t.Fatalf("expected requested URL mismatch to fail: %+v", res)
	}
	if res := SourceAccessMatchAtLeast("verified", "browser_network_read", "taostats.io/api", "", "", 1).Eval(trace); res.Pass {
		t.Fatalf("expected status mismatch to fail: %+v", res)
	}
	if res := SourceAccessMatchAtLeast("network", "browser_network_read", "missing.example", "", "", 1).Eval(trace); res.Pass {
		t.Fatalf("expected URL mismatch to fail: %+v", res)
	}
}

func TestSessionSearchMatchAtLeast(t *testing.T) {
	trace := Trace{Tools: []ToolCall{
		{
			CallID: "s1",
			Tool:   "session_search",
			Result: `{"query":"Northstar Biotech Q3","total":1,"results":[{"session_id":"northstar-q3-current","turn_idx":2,"role":"assistant","snippet":"user: Northstar Biotech Q3 review current recovery handoff\nassistant: current decision: recovery marker RECOVER-NSTAR-58, risk label trial-delay.","score":6.5,"matched_terms":["northstar","biotech","q3"],"context_included":true}]}`,
		},
	}}

	if res := SessionSearchMatchAtLeast("Northstar Biotech", "northstar-q3-current", "RECOVER-NSTAR-58", []string{"northstar", "biotech"}, true, 2, 1).Eval(trace); !res.Pass {
		t.Fatalf("expected session search check to pass: %+v", res)
	}
	if res := SessionSearchMatchAtLeast("Northstar Biotech", "northstar-q2-old", "RECOVER-NSTAR-58", []string{"northstar"}, true, 0, 1).Eval(trace); res.Pass {
		t.Fatalf("expected session id mismatch to fail: %+v", res)
	}
	if res := SessionSearchMatchAtLeast("Northstar Biotech", "northstar-q3-current", "RECOVER-NSTAR-58", []string{"missing"}, true, 0, 1).Eval(trace); res.Pass {
		t.Fatalf("expected missing matched term to fail: %+v", res)
	}
}

func TestRecentSessionSearchAnchorAtLeast(t *testing.T) {
	trace := Trace{Tools: []ToolCall{{
		CallID: "s1",
		Tool:   "session_search",
		Result: `{"query":"missing marker","total":0,"results":[],"message":"no results. Next: retry from anchors.","recent_sessions":[{"session_id":"market-alpha","latest_user":"Analyze Alpha Coast stock recovery","latest_assistant":"final marker HIST-STOCK-44","plan":"plan_status: current_step: 2 [in_progress] Recheck Alpha Coast risk","loop":"recent_loop_events: event: type=loop.protocol_feed mode=digest","task_state":"task_state: status=blocked verification=failed\nfailed_action: tool=shell kinds=test_failed summary=go test ./... next=inspect test failure","recovery":"turn_end: reason=max_turns; top_failure=loop_guard_no_new_evidence:2"}]}`,
	}}}

	if res := RecentSessionSearchAnchorAtLeast("missing marker", "market-alpha", "Alpha Coast", "HIST-STOCK-44", "Recheck Alpha Coast", "loop.protocol_feed", "test_failed", "loop_guard_no_new_evidence", "retry", 1).Eval(trace); !res.Pass {
		t.Fatalf("expected recent session anchor check to pass: %+v", res)
	}
	if res := RecentSessionSearchAnchorAtLeast("missing marker", "market-alpha", "", "", "", "", "", "different_failure", "", 1).Eval(trace); res.Pass {
		t.Fatalf("expected recovery mismatch to fail: %+v", res)
	}
}

func TestTraceEventCountAtLeast(t *testing.T) {
	trace := Trace{RawTypes: map[string]int{"conversation.repaired": 1}}
	if res := TraceEventCountAtLeast("conversation.repaired", 1).Eval(trace); !res.Pass {
		t.Fatalf("expected trace event count to pass: %+v", res)
	}
	res := TraceEventCountAtLeast("conversation.repaired", 2).Eval(trace)
	if res.Pass {
		t.Fatal("expected insufficient trace event count to fail")
	}
	if !strings.Contains(res.Detail, "conversation.repaired") {
		t.Fatalf("failure detail should name event type: %s", res.Detail)
	}
}

func TestContextInjectionTextAtLeast(t *testing.T) {
	trace := Trace{ContextInjections: []ContextInjection{{
		Source:  "skill",
		Title:   "Active skill injected",
		Summary: "Activated skill: reviewed_eval.",
		Preview: "AFFENT ACTIVE SKILL: reviewed_eval",
	}, {
		Source:  "active_plan",
		Summary: "Current step: install skill",
	}}}
	if res := ContextInjectionTextAtLeast("skill", "reviewed_eval", 1).Eval(trace); !res.Pass {
		t.Fatalf("expected skill context text check to pass: %+v", res)
	}
	if res := ContextInjectionTextAtLeast("skill", "playwright", 1).Eval(trace); res.Pass {
		t.Fatalf("expected missing skill context text to fail: %+v", res)
	}
	if res := ContextInjectionTextAtLeast("skill_provider", "reviewed_eval", 1).Eval(trace); res.Pass {
		t.Fatalf("expected wrong context source to fail: %+v", res)
	}
}

func TestUserMessageModeAtLeast(t *testing.T) {
	trace := Trace{UserMessages: []UserMessage{
		{TurnID: "t1", Mode: "normal"},
		{TurnID: "t2", Mode: "execute_plan", DisplayText: "Run plan step 2"},
		{TurnID: "t3"},
	}}

	if res := UserMessageModeAtLeast("execute_plan", 1).Eval(trace); !res.Pass {
		t.Fatalf("expected execute_plan mode check to pass: %+v", res)
	}
	if res := UserMessageModeAtLeast("normal", 2).Eval(trace); !res.Pass {
		t.Fatalf("empty user.message mode should count as normal: %+v", res)
	}
	res := UserMessageModeAtLeast("loop_setup", 1).Eval(trace)
	if res.Pass {
		t.Fatal("expected missing loop_setup mode to fail")
	}
	if !strings.Contains(res.Detail, "execute_plan") {
		t.Fatalf("failure detail should include observed modes: %s", res.Detail)
	}
}

func TestUserMessageModeAtMost(t *testing.T) {
	trace := Trace{UserMessages: []UserMessage{
		{TurnID: "t1", Mode: "normal"},
		{TurnID: "t2", Mode: "loop_setup"},
		{TurnID: "t3"},
	}}
	if res := UserMessageModeAtMost("loop_setup", 1).Eval(trace); !res.Pass {
		t.Fatalf("expected one loop_setup mode to pass max=1: %+v", res)
	}
	if res := UserMessageModeAtMost("normal", 2).Eval(trace); !res.Pass {
		t.Fatalf("empty user.message mode should count as normal for max checks: %+v", res)
	}
	res := UserMessageModeAtMost("loop_setup", 0).Eval(trace)
	if res.Pass {
		t.Fatal("expected forbidden loop_setup mode to fail")
	}
	if !strings.Contains(res.Detail, "at most 0") || !strings.Contains(res.Detail, "observed") {
		t.Fatalf("failure detail should include limit and observed modes: %s", res.Detail)
	}
}

func TestTaskStateRequestProvenanceChecks(t *testing.T) {
	trace := Trace{TaskState: TaskStateSnapshot{RequestMode: "execute_plan", RequestSource: "schedule", ScheduleID: "sched_clamp", ScheduleKind: "checkin"}}
	if res := TaskStateRequestModeIs("execute_plan").Eval(trace); !res.Pass {
		t.Fatalf("expected task state request mode to pass: %+v", res)
	}
	if res := TaskStateRequestSourceIs("schedule").Eval(trace); !res.Pass {
		t.Fatalf("expected task state request source to pass: %+v", res)
	}
	if res := TaskStateScheduleKindIs("checkin").Eval(trace); !res.Pass {
		t.Fatalf("expected task state schedule kind to pass: %+v", res)
	}
	if res := TaskStateScheduleIDIs("sched_clamp").Eval(trace); !res.Pass {
		t.Fatalf("expected task state schedule id to pass: %+v", res)
	}
	if res := TaskStateRequestModeIs("loop_setup").Eval(trace); res.Pass || !strings.Contains(res.Detail, "execute_plan") {
		t.Fatalf("expected mismatched task state request mode to fail with observed mode: %+v", res)
	}
	if res := TaskStateScheduleKindIs("loop_tick").Eval(trace); res.Pass || !strings.Contains(res.Detail, "checkin") {
		t.Fatalf("expected mismatched task state schedule kind to fail with observed kind: %+v", res)
	}
	if res := TaskStateScheduleIDIs("sched_other").Eval(trace); res.Pass || !strings.Contains(res.Detail, "sched_clamp") {
		t.Fatalf("expected mismatched task state schedule id to fail with observed id: %+v", res)
	}

	legacy := Trace{}
	if res := TaskStateRequestModeIs("normal").Eval(legacy); !res.Pass {
		t.Fatalf("empty task state request mode should normalize to normal: %+v", res)
	}
	if res := TaskStateRequestSourceIs("user").Eval(legacy); !res.Pass {
		t.Fatalf("empty task state request source should normalize to user: %+v", res)
	}
}

func TestTaskStateActionAndEvidenceChecks(t *testing.T) {
	trace := Trace{TaskState: TaskStateSnapshot{
		ChangedFiles: []TaskStateFile{
			{Path: "app/mathutil/clamp.go", Action: "edit"},
		},
		AttemptedActions: []TaskStateAction{
			{Tool: "shell", Summary: "git push origin main"},
			{Tool: "loop_protocol", Summary: "close completed"},
		},
		Evidence: []TaskStateEvidence{
			{Source: "git_push", Summary: "git push origin main"},
			{Source: "loop_protocol", Summary: "close completed"},
		},
	}}
	if res := TaskStateAttemptedActionAtLeast("shell", "git push", 1).Eval(trace); !res.Pass {
		t.Fatalf("expected attempted action check to pass: %+v", res)
	}
	if res := TaskStateEvidenceAtLeast("git_push", "git push", 1).Eval(trace); !res.Pass {
		t.Fatalf("expected evidence check to pass: %+v", res)
	}
	if res := TaskStateChangedFileAtLeast("mathutil/clamp.go", "edit", 1).Eval(trace); !res.Pass {
		t.Fatalf("expected changed file check to pass: %+v", res)
	}
	if res := TaskStateAttemptedActionAtLeast("shell", "git commit", 1).Eval(trace); res.Pass || !strings.Contains(res.Detail, "git push origin main") {
		t.Fatalf("expected attempted action mismatch to show observed actions: %+v", res)
	}
	if res := TaskStateChangedFileAtLeast("README.md", "edit", 1).Eval(trace); res.Pass || !strings.Contains(res.Detail, "app/mathutil/clamp.go") {
		t.Fatalf("expected changed file mismatch to show observed files: %+v", res)
	}
	if res := TaskStateEvidenceAtLeast("git_commit", "", 1).Eval(trace); res.Pass || !strings.Contains(res.Detail, "git_push") {
		t.Fatalf("expected evidence mismatch to show observed evidence: %+v", res)
	}
}

func TestConversationRepairChecks(t *testing.T) {
	trace := Trace{ConversationRepairs: []sse.ConversationRepairedPayload{
		{
			FailureKind:           "resume_duplicate_tool_result",
			DuplicateToolResults:  2,
			UnexpectedToolResults: 1,
		},
		{
			FailureKind:        "resume_missing_tool_result",
			MissingToolResults: 1,
		},
	}}
	for _, tc := range []struct {
		field string
		min   int
	}{
		{field: "events", min: 2},
		{field: "missing_tool_results", min: 1},
		{field: "duplicate_tool_results", min: 2},
		{field: "unexpected_tool_results", min: 1},
	} {
		if res := ConversationRepairStatsAtLeast(tc.field, tc.min).Eval(trace); !res.Pass {
			t.Fatalf("expected %s check to pass: %+v", tc.field, res)
		}
	}
	if res := ConversationRepairKindAtLeast("resume_duplicate_tool_result", 1).Eval(trace); !res.Pass {
		t.Fatalf("expected duplicate repair kind check to pass: %+v", res)
	}
	if res := ConversationRepairStatsAtLeast("duplicate_tool_results", 3).Eval(trace); res.Pass || !strings.Contains(res.Detail, "duplicate_tool_results=2") {
		t.Fatalf("expected duplicate count check to fail with detail, got %+v", res)
	}
	if res := ConversationRepairKindAtLeast("resume_unexpected_tool_result", 1).Eval(trace); res.Pass || !strings.Contains(res.Detail, "repair_kinds") {
		t.Fatalf("expected missing repair kind check to fail with detail, got %+v", res)
	}
	if res := ConversationRepairStatsAtLeast("unknown", 1).Eval(trace); res.Pass || !strings.Contains(res.Detail, "unknown") {
		t.Fatalf("expected unknown field check to fail, got %+v", res)
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
			{CallID: "c4", Tool: "browser_network", ExitCode: 0, Result: "BROWSER NETWORK EVIDENCE\nMATCHES: none\nFailure: kind=no_matches"},
			{CallID: "c5", Tool: "read_file", ExitCode: 0, Result: "Failure: kind=not_a_tool_failure"},
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
		if res := ToolFailureKindAtLeast("no_matches", 1).Eval(trace); !res.Pass {
			t.Fatalf("expected text fallback no_matches check to pass: %+v", res)
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

func TestLoopDecisionChecks(t *testing.T) {
	trace := Trace{LoopDecisions: []LoopDecision{
		{Kind: "evidence_quality", Decision: "defer", Trigger: "source_access_dynamic_partial"},
		{Kind: "loop_stop", Decision: "continue"},
	}}
	if res := LoopDecisionKindAtLeast("evidence_quality", 1).Eval(trace); !res.Pass {
		t.Fatalf("expected evidence_quality decision kind check to pass: %+v", res)
	}
	if res := LoopDecisionResultAtLeast("defer", 1).Eval(trace); !res.Pass {
		t.Fatalf("expected defer decision result check to pass: %+v", res)
	}
	res := LoopDecisionKindAtLeast("memory_write", 1).Eval(trace)
	if res.Pass {
		t.Fatal("expected missing loop decision kind to fail")
	}
	if !strings.Contains(res.Detail, "memory_write=0") || !strings.Contains(res.Detail, "evidence_quality") {
		t.Fatalf("failure detail should include requested and observed decision kinds: %s", res.Detail)
	}
	res = LoopDecisionResultAtLeast("yes", 1).Eval(trace)
	if res.Pass {
		t.Fatal("expected missing loop decision result to fail")
	}
	if !strings.Contains(res.Detail, "yes=0") || !strings.Contains(res.Detail, "defer") {
		t.Fatalf("failure detail should include requested and observed decision results: %s", res.Detail)
	}
}

func TestLoopDecisionMatchAtLeast(t *testing.T) {
	trace := Trace{LoopDecisions: []LoopDecision{
		{Kind: "input_budget", Decision: "defer", Trigger: "turn_input_tokens_observed_after_step", TokenBudget: 300000, ObservedInputTokens: 479974},
		{Kind: "evidence_quality", Decision: "defer", Trigger: "source_access_dynamic_partial", BudgetBytes: 32768},
		{Kind: "evidence_quality", Decision: "continue", Trigger: "memory_review"},
		{Kind: "loop_stop", Decision: "defer", Trigger: "source_access_dynamic_partial"},
	}}
	if res := LoopDecisionMatchAtLeast("evidence_quality", "defer", "source_access_dynamic_partial", 1).Eval(trace); !res.Pass {
		t.Fatalf("expected exact loop decision match to pass: %+v", res)
	}
	if res := LoopDecisionRequirementAtLeast(LoopDecisionRequirement{
		Kind:                   "input_budget",
		Decision:               "defer",
		Trigger:                "turn_input_tokens_observed_after_step",
		MinTokenBudget:         300000,
		MinObservedInputTokens: 479974,
	}).Eval(trace); !res.Pass {
		t.Fatalf("expected structured input-budget decision match to pass: %+v", res)
	}
	if res := LoopDecisionRequirementAtLeast(LoopDecisionRequirement{Kind: "input_budget", MinObservedInputTokens: 500000}).Eval(trace); res.Pass || !strings.Contains(res.Detail, "observed_input_tokens>=500000") || !strings.Contains(res.Detail, "observed_input=479974") {
		t.Fatalf("expected structured input-budget decision mismatch detail, got: %+v", res)
	}
	res := LoopDecisionMatchAtLeast("evidence_quality", "defer", "network_source_missing", 1).Eval(trace)
	if res.Pass {
		t.Fatal("expected exact loop decision match to fail when trigger is split across other decisions")
	}
	for _, want := range []string{"matched=0", `kind="evidence_quality"`, `decision="defer"`, `trigger="network_source_missing"`, "budget_bytes=32768"} {
		if !strings.Contains(res.Detail, want) {
			t.Fatalf("failure detail %q missing %q", res.Detail, want)
		}
	}
}

func TestMessageRejectedAtLeast(t *testing.T) {
	trace := Trace{MessageRejections: []MessageRejected{
		{Trigger: "active_plan_unfinished", Reason: "plan:0/1:active", Text: "All done."},
	}}
	if res := MessageRejectedAtLeast("active_plan_unfinished", 1).Eval(trace); !res.Pass {
		t.Fatalf("expected message rejected check to pass: %+v", res)
	}
	res := MessageRejectedAtLeast("loop_protocol_unclosed", 1).Eval(trace)
	if res.Pass {
		t.Fatal("expected mismatched trigger to fail")
	}
	for _, want := range []string{"message_rejected=0", "active_plan_unfinished", "All done."} {
		if !strings.Contains(res.Detail, want) {
			t.Fatalf("failure detail = %q, want %q", res.Detail, want)
		}
	}
}

func TestRuntimeSurfaceCompletionGuard(t *testing.T) {
	trace := Trace{RuntimeSurfaces: []sse.RuntimeSurfacePayload{
		{CompletionGuards: []string{"active_plan_unfinished"}},
		{RefreshReason: "post_compaction", CompletionGuards: []string{"loop_protocol_running"}, MaxTurnInputTokens: 300000, ModelContextWindowTokens: 100000, ModelContextWindowSource: "provider", ModelContextWindowEffectivePercent: 95, ReservedOutputTokens: 30000, CompactTriggerInputTokens: 56000, CompactScopeActive: true, CompactWindowOrdinal: 2, CompactWindowPrefillInputTokens: 45000, CompactWindowPrefillSource: "server_observed", CompactScopedInputTokens: 12000, CompactHardInputLimitTokens: 70000, CompactSummaryPromptMaxBytes: agent.DefaultSummaryPromptMaxBytes, EstimatedToolSchemaTokens: 400, ToolSchemaBudgetTokens: 500, ExcludedToolCount: 2, AvailableToolCount: 5},
	}}
	if res := RuntimeSurfaceCompletionGuard("loop_protocol_running").Eval(trace); !res.Pass {
		t.Fatalf("expected runtime surface completion guard check to pass: %+v", res)
	}
	if res := RuntimeSurfaceMaxTurnInputTokens(300000).Eval(trace); !res.Pass {
		t.Fatalf("expected runtime surface input budget check to pass: %+v", res)
	}
	if res := RuntimeSurfaceModelContextWindowTokens(100000).Eval(trace); !res.Pass {
		t.Fatalf("expected runtime surface model context check to pass: %+v", res)
	}
	if res := RuntimeSurfaceModelContextWindowEffectivePercent(95).Eval(trace); !res.Pass {
		t.Fatalf("expected runtime surface effective context percent check to pass: %+v", res)
	}
	if res := RuntimeSurfaceModelContextWindowSource("provider").Eval(trace); !res.Pass {
		t.Fatalf("expected runtime surface model context source check to pass: %+v", res)
	}
	if res := RuntimeSurfaceCompactTriggerInputTokens(56000).Eval(trace); !res.Pass {
		t.Fatalf("expected runtime surface compact trigger check to pass: %+v", res)
	}
	if res := RuntimeSurfaceCompactScopeActive().Eval(trace); !res.Pass {
		t.Fatalf("expected runtime surface compact scope check to pass: %+v", res)
	}
	if res := RuntimeSurfaceCompactPrefillSource("server_observed").Eval(trace); !res.Pass {
		t.Fatalf("expected runtime surface compact prefill source check to pass: %+v", res)
	}
	if res := RuntimeSurfaceCompactPrefillSource("estimated").Eval(trace); res.Pass || !strings.Contains(res.Detail, "server_observed") {
		t.Fatalf("expected runtime surface compact prefill source check to fail with observed source: %+v", res)
	}
	if res := RuntimeSurfaceRefreshReason("post_compaction").Eval(trace); !res.Pass {
		t.Fatalf("expected runtime surface refresh reason check to pass: %+v", res)
	}
	if res := RuntimeSurfaceRefreshReasonAtLeast("post_compaction", 1).Eval(trace); !res.Pass {
		t.Fatalf("expected counted runtime surface refresh reason check to pass: %+v", res)
	}
	if res := RuntimeSurfaceRefreshReason("turn_start").Eval(trace); res.Pass || !strings.Contains(res.Detail, "post_compaction") {
		t.Fatalf("expected runtime surface refresh reason check to fail with observed reason: %+v", res)
	}
	if res := RuntimeSurfaceRefreshReasonAtLeast("post_compaction", 2).Eval(trace); res.Pass || !strings.Contains(res.Detail, "observed_count=1") {
		t.Fatalf("expected counted runtime surface refresh reason check to fail below threshold: %+v", res)
	}
	scopeTrace := Trace{ContextCompactionSkips: []ContextCompactionSkip{{
		Cause:                           "request_pressure_not_reduced",
		CompactScopeActive:              true,
		CompactWindowOrdinal:            2,
		CompactWindowPrefillInputTokens: 45000,
		CompactWindowPrefillSource:      "server_observed",
		CompactScopedInputTokens:        12000,
		CompactHardInputLimitTokens:     70000,
	}}}
	if res := ContextMaintenanceCompactScopeActiveAtLeast(1).Eval(scopeTrace); !res.Pass {
		t.Fatalf("expected context maintenance compact scope check to pass: %+v", res)
	}
	if res := RuntimeSurfaceCompactTriggerMatchesModelPolicy().Eval(trace); !res.Pass {
		t.Fatalf("expected runtime surface model policy check to pass: %+v", res)
	}
	if res := RuntimeSurfaceCompactSummaryPromptMatchesModelPolicy().Eval(trace); !res.Pass {
		t.Fatalf("expected runtime surface summary prompt policy check to pass: %+v", res)
	}
	if res := RuntimeSurfaceHardInputLimitMatchesModelPolicy().Eval(trace); !res.Pass {
		t.Fatalf("expected runtime surface hard input limit policy check to pass: %+v", res)
	}
	if res := RuntimeSurfaceToolSchemaWithinBudget().Eval(trace); !res.Pass {
		t.Fatalf("expected runtime surface tool schema budget check to pass: %+v", res)
	}
	if res := RuntimeSurfaceReservedOutputTokens(30000).Eval(trace); !res.Pass {
		t.Fatalf("expected runtime surface reserved output check to pass: %+v", res)
	}
	res := RuntimeSurfaceMaxTurnInputTokens(1).Eval(trace)
	if res.Pass {
		t.Fatal("expected mismatched runtime surface input budget to fail")
	}
	for _, want := range []string{"max_turn_input_tokens=1", "observed=[300000]"} {
		if !strings.Contains(res.Detail, want) {
			t.Fatalf("failure detail = %q, want %q", res.Detail, want)
		}
	}
	res = RuntimeSurfaceCompactTriggerInputTokens(1).Eval(trace)
	if res.Pass {
		t.Fatal("expected mismatched runtime surface compact trigger to fail")
	}
	for _, want := range []string{"compact_trigger_input_tokens=1", "observed=[56000]"} {
		if !strings.Contains(res.Detail, want) {
			t.Fatalf("failure detail = %q, want %q", res.Detail, want)
		}
	}
	badPolicy := RuntimeSurfaceCompactTriggerMatchesModelPolicy().Eval(Trace{RuntimeSurfaces: []sse.RuntimeSurfacePayload{{
		ModelContextWindowTokens:   100000,
		ReservedOutputTokens:       30000,
		CompactTriggerInputTokens:  80000,
		CompactTriggerInputPercent: 80,
	}}})
	if badPolicy.Pass {
		t.Fatal("expected mismatched runtime surface model policy check to fail")
	}
	for _, want := range []string{"expected=56000", "trigger=80000", "reserve=30000"} {
		if !strings.Contains(badPolicy.Detail, want) {
			t.Fatalf("failure detail = %q, want %q", badPolicy.Detail, want)
		}
	}
	badSummaryPolicy := RuntimeSurfaceCompactSummaryPromptMatchesModelPolicy().Eval(Trace{RuntimeSurfaces: []sse.RuntimeSurfacePayload{{
		ModelContextWindowTokens:     200,
		CompactTriggerInputPercent:   80,
		CompactSummaryPromptMaxBytes: 196608,
	}}})
	if badSummaryPolicy.Pass {
		t.Fatal("expected mismatched runtime surface summary prompt policy check to fail")
	}
	for _, want := range []string{"expected=640", "summary_prompt_max_bytes=196608"} {
		if !strings.Contains(badSummaryPolicy.Detail, want) {
			t.Fatalf("failure detail = %q, want %q", badSummaryPolicy.Detail, want)
		}
	}
	badHardLimitPolicy := RuntimeSurfaceHardInputLimitMatchesModelPolicy().Eval(Trace{RuntimeSurfaces: []sse.RuntimeSurfacePayload{{
		ModelContextWindowTokens:    100000,
		ReservedOutputTokens:        30000,
		CompactHardInputLimitTokens: 80000,
	}}})
	if badHardLimitPolicy.Pass {
		t.Fatal("expected mismatched runtime surface hard input limit policy check to fail")
	}
	for _, want := range []string{"expected=70000", "hard_limit=80000", "reserve=30000"} {
		if !strings.Contains(badHardLimitPolicy.Detail, want) {
			t.Fatalf("failure detail = %q, want %q", badHardLimitPolicy.Detail, want)
		}
	}
	badToolBudget := RuntimeSurfaceToolSchemaWithinBudget().Eval(Trace{RuntimeSurfaces: []sse.RuntimeSurfacePayload{{
		EstimatedToolSchemaTokens: 600,
		ToolSchemaBudgetTokens:    500,
		ExcludedToolCount:         2,
		AvailableToolCount:        5,
	}}})
	if badToolBudget.Pass {
		t.Fatal("expected runtime surface tool schema budget check to fail")
	}
	for _, want := range []string{"tool schema exceeded budget", "tool_schema_tokens=600", "budget=500", "excluded=2/5"} {
		if !strings.Contains(badToolBudget.Detail, want) {
			t.Fatalf("failure detail = %q, want %q", badToolBudget.Detail, want)
		}
	}
	res = RuntimeSurfaceCompletionGuard("missing_guard").Eval(trace)
	if res.Pass {
		t.Fatal("expected missing runtime surface completion guard to fail")
	}
	for _, want := range []string{"missing_guard", "active_plan_unfinished", "loop_protocol_running"} {
		if !strings.Contains(res.Detail, want) {
			t.Fatalf("failure detail = %q, want %q", res.Detail, want)
		}
	}
}

func TestLoopProtocolFeedChecks(t *testing.T) {
	trace := Trace{LoopProtocolFeeds: []LoopProtocolFeed{
		{Mode: "digest", FeedNumber: 1, PlanLabel: "SN120 research", PlanCurrentStepStatus: "in_progress", PlanCurrentStep: "collect rendered page and network evidence", CurrentSituation: "current risk: dashboard values require network evidence", LastTurnEndReason: "completed", LastTurnToolRequests: 4, LastTurnToolErrors: 1, LastTurnForcedNoTools: 1, LastTurnMemorySearchCalls: 2, LastTurnMemorySearchMisses: 1, LastTurnSessionSearchCalls: 1, LastDecisionKind: "evidence_quality", LastDecisionTrigger: "source_access_dynamic_partial", LastDecision: "defer", LastDecisionConfidence: "high", LastDecisionReason: "dynamic widgets were empty", LastDecisionAction: "read browser_network_read ref n7", LastDecisionTokenBudget: 300000, LastDecisionObservedInput: 479974},
		{Mode: "full", FeedNumber: 2, PlanLabel: "SN120 research", PlanCurrentStepStatus: "pending", PlanCurrentStep: "write final cited analysis"},
	}, EventOrder: []TraceEventRef{
		{Index: 1, Type: sse.TypeLoopProtocolFeed, LoopProtocolMode: "digest", LoopProtocolPath: ".affent/loops/sn120/LOOP.md"},
		{Index: 2, Type: sse.TypeContextCompact, ContextReason: "threshold"},
		{Index: 3, Type: sse.TypeLoopProtocolFeed, LoopProtocolMode: "full", LoopProtocolPath: ".affent/loops/sn120/LOOP.md"},
	}}

	stats := trace.LoopProtocolFeedStats(1)
	if stats.Count != 2 || stats.ByMode["digest"] != 1 || stats.ByMode["full"] != 1 || stats.Latest.FeedNumber != 2 {
		t.Fatalf("LoopProtocolFeedStats = %+v", stats)
	}
	if len(stats.Examples) != 1 || stats.Examples[0].PlanCurrentStep != "collect rendered page and network evidence" || !strings.Contains(stats.Examples[0].CurrentSituation, "dashboard values") {
		t.Fatalf("LoopProtocolFeedStats examples = %+v", stats.Examples)
	}
	if res := LoopProtocolFeedsAtLeast(2).Eval(trace); !res.Pass {
		t.Fatalf("expected loop protocol feed count check to pass: %+v", res)
	}
	if res := LoopProtocolFeedModeAtLeast("full", 1).Eval(trace); !res.Pass {
		t.Fatalf("expected loop protocol feed mode check to pass: %+v", res)
	}
	if res := LoopProtocolFeedMatchAtLeast("digest", "SN120", "in_progress", "network evidence", "dashboard values", 1).Eval(trace); !res.Pass {
		t.Fatalf("expected loop protocol feed checkpoint match to pass: %+v", res)
	}
	if res := LoopProtocolFeedRequirementAtLeast(LoopProtocolFeedRequirement{
		Mode:                          "digest",
		PlanLabelContains:             "SN120",
		LastTurnEndReason:             "completed",
		MinLastTurnToolErrors:         1,
		MinLastTurnForcedNoTools:      1,
		MinLastTurnMemorySearchCalls:  2,
		MinLastTurnMemorySearchMisses: 1,
		MinLastTurnSessionSearchCalls: 1,
		LastDecisionKind:              "evidence_quality",
		LastDecisionTrigger:           "source_access_dynamic_partial",
		LastDecision:                  "defer",
		LastDecisionConfidence:        "high",
		LastDecisionReason:            "widgets were empty",
		LastDecisionAction:            "browser_network_read ref n7",
		MinLastDecisionTokenBudget:    300000,
		MinLastDecisionObservedInput:  479974,
	}).Eval(trace); !res.Pass {
		t.Fatalf("expected loop protocol last-turn checkpoint match to pass: %+v", res)
	}
	if res := LoopProtocolFullFeedAfterCompaction().Eval(trace); !res.Pass {
		t.Fatalf("expected post-compaction full feed check to pass: %+v", res)
	}
	res := LoopProtocolFeedMatchAtLeast("digest", "SN120", "completed", "network evidence", "dashboard values", 1).Eval(trace)
	if res.Pass {
		t.Fatal("expected mismatched plan status to fail")
	}
	for _, want := range []string{"matched=0", `mode="digest"`, `plan_current_step_status="completed"`, "collect rendered page"} {
		if !strings.Contains(res.Detail, want) {
			t.Fatalf("failure detail %q missing %q", res.Detail, want)
		}
	}
	res = LoopProtocolFeedRequirementAtLeast(LoopProtocolFeedRequirement{Mode: "digest", MinLastTurnMemorySearchMisses: 2, MinLastTurnForcedNoTools: 2, LastDecisionAction: "missing action"}).Eval(trace)
	if res.Pass {
		t.Fatal("expected mismatched last-turn memory miss requirement to fail")
	}
	for _, want := range []string{"matched=0", "last_turn_memory_search_misses>=2", "last_turn_forced_no_tools>=2", "last_decision_action_contains", "memory_misses=1", "forced_no_tools=1", "browser_network_read"} {
		if !strings.Contains(res.Detail, want) {
			t.Fatalf("failure detail %q missing %q", res.Detail, want)
		}
	}
	res = LoopProtocolFullFeedAfterCompaction().Eval(Trace{EventOrder: []TraceEventRef{
		{Index: 1, Type: sse.TypeLoopProtocolFeed, LoopProtocolMode: "full"},
		{Index: 2, Type: sse.TypeContextCompact, ContextReason: "context_overflow", ContextReactive: true},
		{Index: 3, Type: sse.TypeLoopProtocolFeed, LoopProtocolMode: "digest"},
	}})
	if res.Pass {
		t.Fatal("expected missing post-compaction full feed check to fail")
	}
	if !strings.Contains(res.Detail, "expected a full") || !strings.Contains(res.Detail, "context.compacted") {
		t.Fatalf("failure detail should explain event order: %s", res.Detail)
	}
}

func TestLoopProtocolCalibrationCheck(t *testing.T) {
	trace := Trace{LoopProtocolCalibrations: []LoopProtocolCalibration{
		{LoopID: "sn120", Status: "draft", CalibrationQuestions: 1, LastCalibrationQuestion: "What should pause this loop?", CalibrationAnswers: 1, LastCalibrationAnswer: "stop if network evidence is missing", ProtocolPath: ".affent/loops/sn120/LOOP.md", EventSeq: 7},
	}, LoopProtocolCalibrationRequests: []LoopProtocolCalibration{
		{LoopID: "sn120", Status: "draft", CalibrationQuestions: 1, LastCalibrationQuestion: "What should pause this loop?", ProtocolPath: ".affent/loops/sn120/LOOP.md", EventSeq: 6},
	}}

	if res := LoopProtocolCalibrationRequestsAtLeast(1).Eval(trace); !res.Pass {
		t.Fatalf("expected loop protocol calibration request check to pass: %+v", res)
	}
	if res := LoopProtocolCalibrationRequestStatusAtLeast("draft", 1).Eval(trace); !res.Pass {
		t.Fatalf("expected draft calibration request status check to pass: %+v", res)
	}
	if res := LoopProtocolCalibrationRequestTextContains("pause this loop").Eval(trace); !res.Pass {
		t.Fatalf("expected calibration request text check to pass: %+v", res)
	}
	requestText := LoopProtocolCalibrationRequestTextContains("LOOP-CALIBRATION-Q17").Eval(trace)
	if requestText.Pass {
		t.Fatal("expected missing calibration request text check to fail")
	}
	for _, want := range []string{"no loop_protocol_calibration_request question contained", "LOOP-CALIBRATION-Q17", "What should pause"} {
		if !strings.Contains(requestText.Detail, want) {
			t.Fatalf("failure detail %q missing %q", requestText.Detail, want)
		}
	}
	requestStatus := LoopProtocolCalibrationRequestStatusAtLeast("running", 1).Eval(trace)
	if requestStatus.Pass {
		t.Fatal("expected running calibration request status check to fail")
	}
	for _, want := range []string{"loop_protocol_calibration_request_status[running]=0", "want >= 1", "sn120:draft"} {
		if !strings.Contains(requestStatus.Detail, want) {
			t.Fatalf("failure detail %q missing %q", requestStatus.Detail, want)
		}
	}
	requests := LoopProtocolCalibrationRequestsAtLeast(2).Eval(trace)
	if requests.Pass {
		t.Fatal("expected missing calibration request count to fail")
	}
	for _, want := range []string{"loop_protocol_calibration_requests=1", "want >= 2", "sn120", "What should pause"} {
		if !strings.Contains(requests.Detail, want) {
			t.Fatalf("failure detail %q missing %q", requests.Detail, want)
		}
	}
	if res := LoopProtocolCalibrationsAtLeast(1).Eval(trace); !res.Pass {
		t.Fatalf("expected loop protocol calibration check to pass: %+v", res)
	}
	if res := LoopProtocolCalibrationStatusAtLeast("draft", 1).Eval(trace); !res.Pass {
		t.Fatalf("expected draft calibration status check to pass: %+v", res)
	}
	answerStatus := LoopProtocolCalibrationStatusAtLeast("running", 1).Eval(trace)
	if answerStatus.Pass {
		t.Fatal("expected running calibration status check to fail")
	}
	for _, want := range []string{"loop_protocol_calibration_status[running]=0", "want >= 1", "sn120:draft"} {
		if !strings.Contains(answerStatus.Detail, want) {
			t.Fatalf("failure detail %q missing %q", answerStatus.Detail, want)
		}
	}
	res := LoopProtocolCalibrationsAtLeast(2).Eval(trace)
	if res.Pass {
		t.Fatal("expected missing calibration count to fail")
	}
	for _, want := range []string{"loop_protocol_calibrations=1", "want >= 2", "sn120", "network evidence"} {
		if !strings.Contains(res.Detail, want) {
			t.Fatalf("failure detail %q missing %q", res.Detail, want)
		}
	}
}

func TestContextCompactionChecks(t *testing.T) {
	trace := Trace{ContextCompactions: []ContextCompaction{
		{TurnID: "t1", BeforeMessages: 50, AfterMessages: 20, RemovedMessages: 30, BeforeBytes: 12000, AfterBytes: 5000, ReducedBytes: 7000, EstimatedInputTokens: 48000, TriggerInputTokens: 40000, ModelContextWindowTokens: 100000, ReservedOutputTokens: 30000, CompactTriggerInputPercent: 80, Reactive: false, Reason: "threshold", SummaryPresent: true, SummaryBytes: 1200, SummaryPreview: "USER_CONTEXT: keep HRO market marker and source URLs.", LoopProtocolAnchor: "LOOP_PROTOCOL: active path=.affent/loops/longrun/LOOP.md loop_id=longrun mode=digest"},
		{TurnID: "t2", BeforeMessages: 40, AfterMessages: 10, RemovedMessages: 30, BeforeBytes: 9000, AfterBytes: 3000, ReducedBytes: 6000, EstimatedInputTokens: 90000, TriggerInputTokens: 70000, ModelContextWindowTokens: 100000, ReservedOutputTokens: 30000, CompactTriggerInputPercent: 80, Reactive: true, Reason: "context_overflow", SummaryPresent: true, SummaryBytes: 900, SummaryPreview: "TASK_TRACKING: preserve Affine SN120 subnet risks."},
	}, ContextCompactionSkips: []ContextCompactionSkip{
		{TurnID: "t3", Cause: "request_pressure_not_reduced", Reason: "estimated_context_pressure", EstimatedInputTokens: 91000, AfterEstimatedInputTokens: 91020, TriggerInputTokens: 70000},
	}}
	stats := trace.ContextCompactionStats(1)
	if stats.Count != 2 || stats.Proactive != 1 || stats.Reactive != 1 || stats.RemovedMessages != 60 || stats.ReducedBytes != 13000 || stats.SummaryBytes != 2100 || stats.PolicyObserved != 2 || stats.MaxPolicyPressurePercent != 129 {
		t.Fatalf("ContextCompactionStats = %+v", stats)
	}
	if len(stats.Examples) != 1 || stats.Examples[0].Reason != "threshold" {
		t.Fatalf("ContextCompactionStats examples = %+v", stats.Examples)
	}
	if res := ContextCompactionsAtLeast(2).Eval(trace); !res.Pass {
		t.Fatalf("expected total compaction check to pass: %+v", res)
	}
	if res := ContextCompactionPolicyObservedAtLeast(2).Eval(trace); !res.Pass {
		t.Fatalf("expected compaction policy metadata check to pass: %+v", res)
	}
	if res := ContextMaintenanceAtLeast(3).Eval(trace); !res.Pass {
		t.Fatalf("expected context maintenance check to include compactions and skips: %+v", res)
	}
	if res := ContextMaintenancePolicyObservedAtLeast(3).Eval(trace); !res.Pass {
		t.Fatalf("expected context maintenance policy check to include compactions and skips: %+v", res)
	}
	if res := ContextMaintenanceReasonAtLeast("estimated_context_pressure", 1).Eval(trace); !res.Pass {
		t.Fatalf("expected context maintenance reason check to include skipped compactions: %+v", res)
	}
	if res := ReactiveContextCompactionsAtLeast(1).Eval(trace); !res.Pass {
		t.Fatalf("expected reactive compaction check to pass: %+v", res)
	}
	if res := ContextCompactionReasonAtLeast("threshold", 1).Eval(trace); !res.Pass {
		t.Fatalf("expected threshold compaction reason check to pass: %+v", res)
	}
	if res := ContextCompactionReasonAtLeast("context_overflow", 1).Eval(trace); !res.Pass {
		t.Fatalf("expected overflow compaction reason check to pass: %+v", res)
	}
	if res := ContextCompactionRemovedMessagesAtLeast(60).Eval(trace); !res.Pass {
		t.Fatalf("expected removed-message compaction check to pass: %+v", res)
	}
	if res := ContextCompactionReducedBytesAtLeast(13000).Eval(trace); !res.Pass {
		t.Fatalf("expected byte-reduction compaction check to pass: %+v", res)
	}
	if res := ContextCompactionSummaryContains("Affine SN120").Eval(trace); !res.Pass {
		t.Fatalf("expected context summary content check to pass: %+v", res)
	}
	if res := ContextCompactionLoopProtocolAnchorContains("loop_id=longrun").Eval(trace); !res.Pass {
		t.Fatalf("expected context loop anchor content check to pass: %+v", res)
	}
	res := ReactiveContextCompactionsAtLeast(2).Eval(trace)
	if res.Pass {
		t.Fatal("expected reactive compaction check to fail")
	}
	if !strings.Contains(res.Detail, "reactive_context_compactions=1") || !strings.Contains(res.Detail, "proactive=1") {
		t.Fatalf("failure detail should include reactive/proactive counts: %s", res.Detail)
	}
	res = ContextCompactionReasonAtLeast("estimated_context_pressure", 1).Eval(trace)
	if res.Pass {
		t.Fatal("expected missing compaction reason check to fail")
	}
	if !strings.Contains(res.Detail, "context_compaction_reason[estimated_context_pressure]=0") || !strings.Contains(res.Detail, "threshold") || !strings.Contains(res.Detail, "context_overflow") {
		t.Fatalf("failure detail should include requested and observed reasons: %s", res.Detail)
	}
	res = ContextCompactionSummaryContains("missing marker").Eval(trace)
	if res.Pass {
		t.Fatal("expected missing context summary marker to fail")
	}
	if !strings.Contains(res.Detail, "missing marker") || !strings.Contains(res.Detail, "HRO market marker") {
		t.Fatalf("failure detail should include requested marker and observed previews: %s", res.Detail)
	}
	res = ContextCompactionReducedBytesAtLeast(20000).Eval(trace)
	if res.Pass {
		t.Fatal("expected reduced-byte compaction check to fail")
	}
	if !strings.Contains(res.Detail, "reduced_bytes=13000") || !strings.Contains(res.Detail, "want >= 20000") {
		t.Fatalf("reduced-byte failure detail = %q", res.Detail)
	}
	res = ContextCompactionLoopProtocolAnchorContains("missing-loop").Eval(trace)
	if res.Pass {
		t.Fatal("expected missing context loop anchor marker to fail")
	}
	if !strings.Contains(res.Detail, "missing-loop") || !strings.Contains(res.Detail, ".affent/loops/longrun/LOOP.md") {
		t.Fatalf("failure detail should include requested marker and observed anchors: %s", res.Detail)
	}
	trace.ContextCompactions[1].EstimatedInputTokens = 0
	res = ContextCompactionPolicyObservedAtLeast(2).Eval(trace)
	if res.Pass {
		t.Fatal("expected missing compaction policy metadata check to fail")
	}
	if !strings.Contains(res.Detail, "context_compaction_policy_observed=1") || !strings.Contains(res.Detail, "turn=t2") {
		t.Fatalf("policy metadata failure detail = %q", res.Detail)
	}
	res = ContextMaintenanceReasonAtLeast("missing_reason", 1).Eval(trace)
	if res.Pass {
		t.Fatal("expected missing context maintenance reason check to fail")
	}
	if !strings.Contains(res.Detail, "compaction_reasons=") || !strings.Contains(res.Detail, "skipped_reasons=") {
		t.Fatalf("context maintenance reason failure detail = %q", res.Detail)
	}
}

func TestLoopTurnTokenCeilingChecks(t *testing.T) {
	trace := Trace{LoopTurnCheckpoints: []LoopTurnCheckpoint{
		{TurnID: "t1", InputTokens: 120000, OutputTokens: 1500},
		{TurnID: "t2", InputTokens: 260000, OutputTokens: 2000},
	}, Usage: Usage{InputTokens: 177835, OutputTokens: 1258}}
	if res := MaxTraceTotalTokens(220000).Eval(trace); !res.Pass {
		t.Fatalf("expected total token check to pass: %+v", res)
	}
	if res := MaxLoopTurnInputTokens(300000).Eval(trace); !res.Pass {
		t.Fatalf("expected max input token check to pass: %+v", res)
	}
	if res := MaxLoopTurnTotalTokens(300000).Eval(trace); !res.Pass {
		t.Fatalf("expected max total token check to pass: %+v", res)
	}
	res := MaxLoopTurnInputTokens(200000).Eval(trace)
	if res.Pass {
		t.Fatal("expected max input token check to fail")
	}
	if !strings.Contains(res.Detail, "max_input_tokens=260000") || !strings.Contains(res.Detail, "want <= 200000") {
		t.Fatalf("failure detail should include observed max and threshold: %s", res.Detail)
	}
	res = MaxTraceTotalTokens(100000).Eval(trace)
	if res.Pass {
		t.Fatal("expected total token check to fail")
	}
	if !strings.Contains(res.Detail, "total_tokens=179093") || !strings.Contains(res.Detail, "want <= 100000") {
		t.Fatalf("total token failure detail = %q", res.Detail)
	}
	res = MaxLoopTurnTotalTokens(200000).Eval(Trace{})
	if res.Pass {
		t.Fatal("expected missing checkpoint token check to fail")
	}
	if !strings.Contains(res.Detail, "got none") {
		t.Fatalf("missing checkpoint failure detail = %q", res.Detail)
	}
	res = MaxTraceTotalTokens(200000).Eval(Trace{})
	if res.Pass {
		t.Fatal("expected missing usage token check to fail")
	}
	if !strings.Contains(res.Detail, "usage events") {
		t.Fatalf("missing usage failure detail = %q", res.Detail)
	}
}

func TestContextCompactionStatsClassifiesSummaryQuality(t *testing.T) {
	trace := Trace{ContextCompactions: []ContextCompaction{
		{TurnID: "unknown", BeforeMessages: 60, AfterMessages: 18, RemovedMessages: 42, Reactive: true, Reason: "legacy"},
		{TurnID: "missing", BeforeMessages: 50, AfterMessages: 10, RemovedMessages: 40, Reactive: true, Reason: "context_overflow", SummaryPresentKnown: true},
		{TurnID: "empty", BeforeMessages: 30, AfterMessages: 12, RemovedMessages: 18, SummaryPresent: true, SummaryPresentKnown: true, Reason: "threshold"},
		{TurnID: "ok", BeforeMessages: 20, AfterMessages: 12, RemovedMessages: 8, SummaryPresent: true, SummaryPresentKnown: true, SummaryBytes: 64, SummaryPreview: "USER_CONTEXT: keep source IDs."},
	}}

	stats := trace.ContextCompactionStats(4)

	if stats.Count != 4 || stats.SummaryMissing != 1 || stats.SummaryEmpty != 1 || stats.SummaryBytes != 64 {
		t.Fatalf("ContextCompactionStats summary quality = %+v", stats)
	}
	if len(stats.Examples) != 4 ||
		stats.Examples[0].TurnID != "unknown" ||
		stats.Examples[1].TurnID != "missing" ||
		stats.Examples[2].TurnID != "empty" ||
		!stats.Examples[1].SummaryPresentKnown {
		t.Fatalf("ContextCompactionStats examples = %+v", stats.Examples)
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

func TestToolFailureKindCountsMergesTurnStatsWithDerivedTimeline(t *testing.T) {
	trace := Trace{
		ToolStats: ToolRuntimeStats{
			ToolFailureByKind: map[string]int{"invalid_args": 1},
		},
		Tools: []ToolCall{
			{
				Tool:         "web_fetch",
				CallID:       "skipped",
				Result:       "(max_turns reached before this tool ran)",
				FailureKinds: []string{"loop_guard_no_budget"},
				ExitCode:     1,
			},
			{
				Tool:         "plan",
				CallID:       "invalid",
				Result:       "Error\nFailure: kind=invalid_args",
				FailureKinds: []string{"invalid_args"},
				ExitCode:     1,
			},
		},
	}
	got := trace.ToolFailureKindCounts()
	if got["invalid_args"] != 1 || got["loop_guard_no_budget"] != 1 {
		t.Fatalf("ToolFailureKindCounts = %+v, want merged turn stats plus derived timeline", got)
	}
}

func TestTraceToolFailureExamples(t *testing.T) {
	trace := Trace{Tools: []ToolCall{
		{
			Tool:         "web_fetch",
			CallID:       "fetch-1",
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
	if dynamic[0].ToolIndex != 1 || dynamic[0].CallID != "fetch-1" || dynamic[0].Tool != "web_fetch" || !strings.Contains(dynamic[0].ArgsSummary, "dashboard.example") {
		t.Fatalf("dynamic_shell example lost tool/URL context: %#v", dynamic[0])
	}
	if !strings.Contains(dynamic[0].ResultSummary, "dynamic page shell") || !strings.Contains(dynamic[0].ResultSummary, "Next:") {
		t.Fatalf("dynamic_shell result summary missing reason/Next: %#v", dynamic[0])
	}
	if !strings.Contains(dynamic[0].SuggestedNextStep, "text/API/source page") {
		t.Fatalf("dynamic_shell example missing structured next step: %#v", dynamic[0])
	}
	search := examples["no_results"]
	if len(search) != 1 || !strings.Contains(search[0].ArgsSummary, "rare subnet") {
		t.Fatalf("no_results example missing query context: %#v", search)
	}
	trace.Tools[0].FailureKinds = append(trace.Tools[0].FailureKinds, "loop_guard_repeated_failed_input")
	trace.Tools[0].Result = "loop_guard: blocked repeated failed call to \"web_fetch\" with the same effective URL after previous Failure kind=blocked.\nNext: do not retry the same failing URL; choose a different source.\nFailure: kind=loop_guard_repeated_failed_input"
	guards := trace.LoopGuardExamples(1)
	if len(guards) != 1 ||
		guards[0].Kind != "loop_guard_repeated_failed_input" ||
		guards[0].Category != "loop_guard" ||
		guards[0].ToolIndex != 1 ||
		guards[0].CallID != "fetch-1" ||
		guards[0].Tool != "web_fetch" ||
		!strings.Contains(guards[0].ArgsSummary, "dashboard.example") ||
		!strings.Contains(guards[0].GuardSummary, "blocked repeated failed call") ||
		!strings.Contains(guards[0].SuggestedNextStep, "choose a different source") {
		t.Fatalf("loop guard example = %#v", guards)
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

func TestFinalPlanCompleted(t *testing.T) {
	okTrace := Trace{Tools: []ToolCall{
		{
			CallID: "c1",
			Tool:   "plan",
			Args:   map[string]any{"action": "update"},
			Result: `{"version":1,"steps":[{"text":"inspect","status":"completed"},{"text":"ship","status":"completed"}]}`,
		},
	}}
	if res := FinalPlanCompleted().Eval(okTrace); !res.Pass {
		t.Fatalf("expected completed plan to pass: %+v", res)
	}

	compactReceiptTrace := Trace{Tools: []ToolCall{
		{
			CallID: "c1",
			Tool:   "plan",
			Args:   map[string]any{"action": "update"},
			Result: `{"version":1,"message":"updated step 2","label":"plan:2/2:done","total_steps":2,"completed_steps":2,"changed":[{"index":2,"text":"ship","status":"completed"}]}`,
		},
	}}
	if res := FinalPlanCompleted().Eval(compactReceiptTrace); !res.Pass {
		t.Fatalf("expected compact completed receipt to pass: %+v", res)
	}

	pendingTrace := Trace{Tools: []ToolCall{
		{
			CallID: "c1",
			Tool:   "plan",
			Args:   map[string]any{"action": "update"},
			Result: `{"version":1,"steps":[{"text":"inspect","status":"completed"},{"text":"ship","status":"pending"}]}`,
		},
	}}
	res := FinalPlanCompleted().Eval(pendingTrace)
	if res.Pass {
		t.Fatal("expected unfinished plan to fail")
	}
	for _, want := range []string{`completed_steps=1`, `total_steps=2`, `current_step_status="pending"`, `ship`} {
		if !strings.Contains(res.Detail, want) {
			t.Fatalf("failure detail = %q, want %q", res.Detail, want)
		}
	}

	if res := FinalPlanCompleted().Eval(Trace{}); res.Pass || !strings.Contains(res.Detail, "plan_calls=0") {
		t.Fatalf("missing plan should fail with plan call count: %+v", res)
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

func TestFinalTextContainsFold(t *testing.T) {
	trace := Trace{FinalText: "Git status: Clean working tree\nPush result: main -> main"}
	if res := FinalTextContainsFold("clean").Eval(trace); !res.Pass {
		t.Fatalf("case-insensitive clean evidence should pass: %+v", res)
	}
	if res := FinalTextContainsFold("push").Eval(trace); !res.Pass {
		t.Fatalf("case-insensitive push evidence should pass: %+v", res)
	}
	if res := FinalTextContainsFold("missing").Eval(trace); res.Pass {
		t.Fatalf("missing evidence should fail")
	}
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

func TestTurnEndedWithReason(t *testing.T) {
	check := TurnEndedWithReason("max_turns")
	if res := check.Eval(Trace{TurnEndReason: "max_turns"}); !res.Pass {
		t.Fatalf("max_turns should pass explicit turn-end check: %+v", res)
	}
	if res := check.Eval(Trace{TurnEndReason: "completed"}); res.Pass {
		t.Fatalf("completed should fail explicit max_turns turn-end check: %+v", res)
	}
	if check.Name != "turn_ended_with_reason:max_turns" {
		t.Fatalf("check name = %q", check.Name)
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

func TestShellCommandMatchingBeforeCommand(t *testing.T) {
	trace := Trace{Tools: []ToolCall{
		{Tool: "shell", Args: map[string]any{"command": "go test ./..."}},
		{Tool: "shell", Args: map[string]any{"command": "git commit -m fix"}},
		{Tool: "shell", Args: map[string]any{"command": "git push origin main"}},
	}}
	if res := ShellCommandMatchingBeforeCommand(`git commit`, `git push`).Eval(trace); !res.Pass {
		t.Fatalf("expected commit before push to pass: %+v", res)
	}

	reversed := Trace{Tools: []ToolCall{
		{Tool: "shell", Args: map[string]any{"command": "git push origin main"}},
		{Tool: "shell", Args: map[string]any{"command": "git commit -m fix"}},
	}}
	res := ShellCommandMatchingBeforeCommand(`git commit`, `git push`).Eval(reversed)
	if res.Pass {
		t.Fatal("expected reversed command order to fail")
	}
	if !strings.Contains(res.Detail, "before") {
		t.Fatalf("detail should explain command order failure: %s", res.Detail)
	}

	missingLater := Trace{Tools: []ToolCall{
		{Tool: "shell", Args: map[string]any{"command": "git commit -m fix"}},
	}}
	res = ShellCommandMatchingBeforeCommand(`git commit`, `git push`).Eval(missingLater)
	if res.Pass {
		t.Fatal("expected missing later command to fail")
	}
	if !strings.Contains(res.Detail, "never observed later command") {
		t.Fatalf("detail should explain missing later command: %s", res.Detail)
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
	t.Run("allows bounded inspection pipe", func(t *testing.T) {
		trace := Trace{Tools: []ToolCall{
			{Tool: "shell", Args: map[string]any{"command": `pwd && ls -la remote.git 2>/dev/null || find . -name "remote.git" -type d 2>/dev/null | head -5`}, ExitCode: 0},
		}}
		if res := ShellCommandLacksUnguarded("| head").Eval(trace); !res.Pass {
			t.Errorf("bounded inspection pipe should pass: %+v", res)
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
			{Tool: "shell", Args: map[string]any{"command": "go test ./... || True"}, ExitCode: 0},
		}}
		// Forbidden written lowercase; command uses "True". Match should still fire.
		res := ShellCommandLacksUnguarded("|| true").Eval(trace)
		if res.Pass {
			t.Errorf("case-insensitive match should fire; got pass")
		}
	})
}

func TestShellCommandLacksWorkspaceAbsolutePath(t *testing.T) {
	check := ShellCommandLacksWorkspaceAbsolutePath()
	workspace := filepath.Join(t.TempDir(), "workspace")

	t.Run("passes for relative command", func(t *testing.T) {
		trace := Trace{
			WorkspaceDir: workspace,
			Tools: []ToolCall{{
				CallID: "c1",
				Tool:   "shell",
				Args:   map[string]any{"command": "pwd; cat data/value.txt"},
			}},
		}
		if res := check.Eval(trace); !res.Pass {
			t.Fatalf("relative shell command should pass: %+v", res)
		}
	})

	t.Run("fails for command absolute workspace path", func(t *testing.T) {
		trace := Trace{
			WorkspaceDir: workspace,
			Tools: []ToolCall{{
				CallID: "c1",
				Tool:   "shell",
				Args:   map[string]any{"command": "cat " + filepath.Join(workspace, "data/value.txt")},
			}},
		}
		if res := check.Eval(trace); res.Pass {
			t.Fatalf("absolute workspace path in command should fail: %+v", res)
		}
	})

	t.Run("fails for cwd absolute workspace path", func(t *testing.T) {
		trace := Trace{
			WorkspaceDir: workspace,
			Tools: []ToolCall{{
				CallID: "c1",
				Tool:   "shell",
				Args:   map[string]any{"command": "cat data/value.txt", "cwd": workspace},
			}},
		}
		if res := check.Eval(trace); res.Pass {
			t.Fatalf("absolute workspace path in cwd should fail: %+v", res)
		}
	})

	t.Run("fails for workspace file tool absolute path", func(t *testing.T) {
		trace := Trace{
			WorkspaceDir: workspace,
			Tools: []ToolCall{{
				CallID: "c1",
				Tool:   "read_file",
				Args:   map[string]any{"path": filepath.Join(workspace, "data/value.txt")},
			}},
		}
		if res := check.Eval(trace); res.Pass {
			t.Fatalf("absolute workspace path in read_file should fail: %+v", res)
		}
	})

	t.Run("fails for workspace path alias without leading slash", func(t *testing.T) {
		alias := strings.TrimPrefix(filepath.ToSlash(filepath.Join(workspace, "data/value.txt")), "/")
		trace := Trace{
			WorkspaceDir: workspace,
			Tools: []ToolCall{{
				CallID: "c1",
				Tool:   "read_file",
				Args:   map[string]any{"path": alias},
			}},
		}
		if res := check.Eval(trace); res.Pass {
			t.Fatalf("workspace path alias in read_file should fail: %+v", res)
		}
	})

	t.Run("fails for shell result absolute workspace path", func(t *testing.T) {
		trace := Trace{
			WorkspaceDir: workspace,
			Tools: []ToolCall{{
				CallID:        "c1",
				Tool:          "shell",
				Args:          map[string]any{"command": "pwd"},
				Result:        filepath.Join(workspace, "cmd") + "\n[exit 0]",
				ResultSummary: filepath.Join(workspace, "cmd"),
			}},
		}
		res := check.Eval(trace)
		if res.Pass {
			t.Fatalf("absolute workspace path in shell result should fail: %+v", res)
		}
		if !strings.Contains(res.Detail, "returned workspace absolute path in result") {
			t.Fatalf("failure should identify tool result leak, got: %+v", res)
		}
	})

	t.Run("passes for shell result relative workspace path", func(t *testing.T) {
		trace := Trace{
			WorkspaceDir: workspace,
			Tools: []ToolCall{{
				CallID:        "c1",
				Tool:          "shell",
				Args:          map[string]any{"command": "pwd"},
				Result:        ".\n[exit 0]",
				ResultSummary: ".",
			}},
		}
		if res := check.Eval(trace); !res.Pass {
			t.Fatalf("relative workspace path in shell result should pass: %+v", res)
		}
	})

	t.Run("fails for runtime surface workspace path policy", func(t *testing.T) {
		trace := Trace{
			WorkspaceDir: workspace,
			RuntimeSurfaces: []sse.RuntimeSurfacePayload{{
				TurnID: "turn-custom",
				Tools: []sse.RuntimeSurfaceTool{{
					Name: "artifact_write",
					ArgPolicy: &sse.RuntimeToolArgPolicy{
						WorkspacePathArgs: []string{"output_path"},
					},
				}},
			}},
			Tools: []ToolCall{{
				TurnID: "turn-custom",
				CallID: "c1",
				Tool:   "artifact_write",
				Args:   map[string]any{"output_path": filepath.Join(workspace, "artifacts/result.txt")},
			}},
		}
		if res := check.Eval(trace); res.Pass {
			t.Fatalf("absolute workspace path from runtime surface policy should fail: %+v", res)
		}
	})

	t.Run("fails for child transcript shell absolute workspace path", func(t *testing.T) {
		transcriptRel := filepath.ToSlash(filepath.Join(".affentctl", "subagents", "parent", "subagent_child.jsonl"))
		transcriptPath := filepath.Join(workspace, filepath.FromSlash(transcriptRel))
		if err := os.MkdirAll(filepath.Dir(transcriptPath), 0o755); err != nil {
			t.Fatal(err)
		}
		line := `{"role":"assistant","tool_calls":[{"id":"child1","type":"function","function":{"name":"shell","arguments":"{\"command\":\"cat ` + filepath.ToSlash(filepath.Join(workspace, "data/value.txt")) + `\"}"}}]}`
		if err := os.WriteFile(transcriptPath, []byte(line+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		trace := Trace{
			WorkspaceDir:     workspace,
			ChildTranscripts: []DebugTranscriptRef{{Kind: "subagent", Path: transcriptRel}},
		}
		if res := check.Eval(trace); res.Pass {
			t.Fatalf("absolute workspace path in child transcript should fail: %+v", res)
		}
	})

	t.Run("fails for child transcript runtime surface workspace path policy", func(t *testing.T) {
		transcriptRel := filepath.ToSlash(filepath.Join(".affentctl", "subagents", "parent", "subagent_child_custom.jsonl"))
		transcriptPath := filepath.Join(workspace, filepath.FromSlash(transcriptRel))
		if err := os.MkdirAll(filepath.Dir(transcriptPath), 0o755); err != nil {
			t.Fatal(err)
		}
		line := `{"role":"assistant","tool_calls":[{"id":"child1","type":"function","function":{"name":"artifact_write","arguments":"{\"output_path\":\"` + filepath.ToSlash(filepath.Join(workspace, "artifacts/result.txt")) + `\"}"}}]}`
		if err := os.WriteFile(transcriptPath, []byte(line+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		trace := Trace{
			WorkspaceDir: workspace,
			RuntimeSurfaces: []sse.RuntimeSurfacePayload{{
				Tools: []sse.RuntimeSurfaceTool{{
					Name: "artifact_write",
					ArgPolicy: &sse.RuntimeToolArgPolicy{
						WorkspacePathArgs: []string{"output_path"},
					},
				}},
			}},
			ChildTranscripts: []DebugTranscriptRef{{Kind: "subagent", Path: transcriptRel}},
		}
		if res := check.Eval(trace); res.Pass {
			t.Fatalf("absolute workspace path in child transcript runtime surface policy should fail: %+v", res)
		}
	})

	t.Run("fails for child transcript file tool absolute workspace path", func(t *testing.T) {
		transcriptRel := filepath.ToSlash(filepath.Join(".affentctl", "focused-tasks", "parent", "focused_child.jsonl"))
		transcriptPath := filepath.Join(workspace, filepath.FromSlash(transcriptRel))
		if err := os.MkdirAll(filepath.Dir(transcriptPath), 0o755); err != nil {
			t.Fatal(err)
		}
		line := `{"role":"assistant","tool_calls":[{"id":"child1","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"` + filepath.ToSlash(filepath.Join(workspace, "docs/runtime.md")) + `\"}"}}]}`
		if err := os.WriteFile(transcriptPath, []byte(line+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		trace := Trace{
			WorkspaceDir:     workspace,
			ChildTranscripts: []DebugTranscriptRef{{Kind: "focused_task", Path: transcriptRel}},
		}
		if res := check.Eval(trace); res.Pass {
			t.Fatalf("absolute workspace path in child read_file should fail: %+v", res)
		}
	})
}

func TestTraceWorkspaceAbsolutePathStats(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "workspace")
	transcriptRel := filepath.ToSlash(filepath.Join(".affentctl", "subagents", "parent", "child.jsonl"))
	transcriptPath := filepath.Join(workspace, filepath.FromSlash(transcriptRel))
	if err := os.MkdirAll(filepath.Dir(transcriptPath), 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"role":"assistant","tool_calls":[{"id":"child1","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"` + filepath.ToSlash(filepath.Join(workspace, "docs/runtime.md")) + `\"}"}}]}`
	if err := os.WriteFile(transcriptPath, []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	trace := Trace{
		WorkspaceDir: workspace,
		Tools: []ToolCall{
			{
				CallID: "arg",
				Tool:   "shell",
				Args:   map[string]any{"command": "cat " + filepath.Join(workspace, "data/value.txt")},
			},
			{
				CallID:        "result",
				Tool:          "read_file",
				Args:          map[string]any{"path": "data/value.txt"},
				ResultSummary: filepath.Join(workspace, "data/value.txt"),
			},
		},
		ChildTranscripts: []DebugTranscriptRef{{Kind: "subagent", Path: transcriptRel}},
	}

	stats := TraceWorkspaceAbsolutePathStats(trace, 2)
	if stats.ArgOccurrences != 1 ||
		stats.ResultOccurrences != 1 ||
		stats.ChildTranscriptOccurrences != 1 ||
		stats.Total() != 3 {
		t.Fatalf("workspace path stats = %+v, want one arg/result/child occurrence", stats)
	}
	if len(stats.Examples) != 2 ||
		!strings.Contains(stats.Examples[0], "used workspace absolute path") ||
		!strings.Contains(stats.Examples[1], "returned workspace absolute path") {
		t.Fatalf("workspace path examples = %#v", stats.Examples)
	}
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
