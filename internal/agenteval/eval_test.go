package agenteval

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCheckTraceFlagsProcessRegressions(t *testing.T) {
	trace := Trace{Tools: []ToolCall{
		{Tool: "shell", Args: map[string]any{"command": "python -m pytest 2>&1 | head -80"}},
		{Tool: "edit_file", Args: map[string]any{"path": "test_slug.py"}},
	}}
	scenario := BatchScenario{
		RequiredCommands:  []string{`python(3)? -m pytest`},
		ForbiddenCommands: []string{"| head"},
		ProtectedFiles:    []string{"test_slug.py"},
	}
	failures := CheckBatchTrace(trace, scenario)
	joined := strings.Join(failures, "\n")
	for _, want := range []string{"forbidden command substring", "modified protected file"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in failures:\n%s", want, joined)
		}
	}
}

func TestCheckTraceIgnoresGuardRejectedForbiddenCommand(t *testing.T) {
	trace := Trace{Tools: []ToolCall{
		{
			Tool:     "shell",
			Args:     map[string]any{"command": "python -m pytest 2>&1 | head -80"},
			ExitCode: 1,
			IsErr:    true,
			Result:   "Error: shell command masks a test/build exit code",
		},
		{
			Tool: "shell",
			Args: map[string]any{"command": "python -m pytest"},
		},
	}, TurnEndReason: "completed"}
	scenario := BatchScenario{
		RequiredCommands:  []string{`python(3)? -m pytest`},
		ForbiddenCommands: []string{"| head"},
	}
	if failures := CheckBatchTrace(trace, scenario); len(failures) != 0 {
		t.Fatalf("guard-rejected command should not fail batch eval: %v", failures)
	}
}

func TestParseTraceFileReadsToolRequestsAndFinalText(t *testing.T) {
	dir := t.TempDir()
	tracePath := filepath.Join(dir, "trace.jsonl")
	body := strings.Join([]string{
		`{"type":"trace.meta","data":{"schema_version":1}}`,
		`{"type":"tool.request","data":{"call_id":"c1","tool":"shell","args":{"command":"go test ./..."},"args_truncated":true,"args_bytes":70000,"args_omitted_bytes":512,"args_cap_bytes":65536,"original_tool":"Shell","original_args_summary":"{\"cmd\":\"go test ./...\"}","canonicalized":true,"args_repaired":true,"repair_notes":["renamed tool","renamed field"]}}`,
		`{"type":"tool.result","data":{"call_id":"c1","result":"ok","exit_code":0,"duration_ms":17,"result_truncated":true,"result_bytes":300000,"result_omitted_bytes":4096,"result_cap_bytes":262144,"result_artifact_path":".affent/artifacts/tool-results/000001-c1.txt"}}`,
		`{"type":"tool.result","data":{"call_id":"guarded","result":"blocked","exit_code":1}}`,
		`{"type":"usage","data":{"input_tokens":11,"output_tokens":7}}`,
		`{"type":"error","data":{"message":"transient stream warning"}}`,
		`{"type":"message.done","data":{"text":"Conclusion: green","finish_reason":"stop"}}`,
		`{"type":"turn.end","data":{"reason":"completed","tool_stats":{"tool_requests":2,"tool_name_canonicalized":1,"tool_args_repaired":1,"tool_errors":1,"tool_duration_ms":17,"loop_guard_interventions":1,"forced_no_tools":1}}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(tracePath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	trace, err := ParseTraceFile(tracePath)
	if err != nil {
		t.Fatal(err)
	}
	if trace.SchemaVersion != 1 {
		t.Fatalf("SchemaVersion = %d, want 1", trace.SchemaVersion)
	}
	if len(trace.Tools) != 2 {
		t.Fatalf("tools = %d, want 2", len(trace.Tools))
	}
	tc := trace.Tools[0]
	if tc.Tool != "shell" || tc.Args["command"] != "go test ./..." {
		t.Fatalf("first tool call wrong: %+v", tc)
	}
	if !tc.Canonicalized || !tc.ArgsRepaired || tc.OriginalTool != "Shell" || !strings.Contains(tc.OriginalArgsSummary, "cmd") || len(tc.RepairNotes) != 2 {
		t.Fatalf("tool repair metadata missing: %+v", tc)
	}
	if !tc.ArgsTruncated || tc.ArgsBytes != 70000 || tc.ArgsOmittedBytes != 512 || tc.ArgsCapBytes != 65536 {
		t.Fatalf("tool request truncation metadata not parsed: %+v", tc)
	}
	if tc.Result != "ok" || tc.ExitCode != 0 || tc.IsErr {
		t.Fatalf("tool result not stitched into request: %+v", tc)
	}
	if tc.DurationMS != 17 {
		t.Fatalf("tool duration not parsed: %+v", tc)
	}
	if !tc.ResultTruncated || tc.ResultBytes != 300000 || tc.ResultOmittedBytes != 4096 || tc.ResultCapBytes != 262144 {
		t.Fatalf("tool result truncation metadata not parsed: %+v", tc)
	}
	if tc.ResultArtifactPath != ".affent/artifacts/tool-results/000001-c1.txt" {
		t.Fatalf("ResultArtifactPath = %q", tc.ResultArtifactPath)
	}
	if stats := SummarizeToolTruncation(trace); stats.ArgsTruncated != 1 || stats.ArgsOmittedBytes != 512 || stats.ResultsTruncated != 1 || stats.ResultsOmittedBytes != 4096 || stats.ResultArtifacts != 1 {
		t.Fatalf("ToolTruncationStats = %+v", stats)
	}
	if guarded := trace.Tools[1]; guarded.CallID != "guarded" || !guarded.IsErr || guarded.ExitCode != 1 {
		t.Fatalf("unmatched error tool result not recorded: %+v", guarded)
	}
	if trace.Usage.InputTokens != 11 || trace.Usage.OutputTokens != 7 {
		t.Fatalf("usage = %+v", trace.Usage)
	}
	if len(trace.LoopErrors) != 1 || !strings.Contains(trace.LoopErrors[0], "transient stream warning") {
		t.Fatalf("LoopErrors = %+v", trace.LoopErrors)
	}
	if trace.FinalText != "Conclusion: green" {
		t.Fatalf("FinalText = %q", trace.FinalText)
	}
	if trace.FinishReason != "stop" {
		t.Fatalf("FinishReason = %q", trace.FinishReason)
	}
	if trace.TurnEndReason != "completed" {
		t.Fatalf("TurnEndReason = %q", trace.TurnEndReason)
	}
	if trace.ToolStats.ToolRequests != 2 || trace.ToolStats.ToolArgsRepaired != 1 || trace.ToolStats.ToolErrors != 1 || trace.ToolStats.ToolDurationMS != 17 || trace.ToolStats.ForcedNoTools != 1 {
		t.Fatalf("ToolStats = %+v", trace.ToolStats)
	}
	if got := trace.RawTypes["trace.meta"]; got != 1 {
		t.Fatalf("RawTypes[trace.meta] = %d", got)
	}
	if got := trace.RawTypes["tool.request"]; got != 1 {
		t.Fatalf("RawTypes[tool.request] = %d", got)
	}
}

func TestParseTraceFileRejectsUnsupportedSchemaVersion(t *testing.T) {
	tracePath := filepath.Join(t.TempDir(), "trace.jsonl")
	body := `{"type":"trace.meta","data":{"schema_version":999}}` + "\n"
	if err := os.WriteFile(tracePath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := ParseTraceFile(tracePath)
	if err == nil || !strings.Contains(err.Error(), "unsupported trace schema_version 999") {
		t.Fatalf("ParseTraceFile err = %v, want unsupported schema version", err)
	}
}

func TestCheckBatchTraceRequiresCleanTurnEnd(t *testing.T) {
	failures := CheckBatchTrace(Trace{TurnEndReason: "max_turns"}, BatchScenario{})
	if len(failures) != 1 || !strings.Contains(failures[0], "turn ended with reason") {
		t.Fatalf("failures = %+v, want turn-end failure", failures)
	}
}

// TestBatchScenarioChecks_UsesSharedCheckLibrary pins the unification:
// a BatchScenario's declarative fields map to the same Check builders
// the in-process Runner uses, so adding a check happens once. A
// regression that grows a parallel check pipeline back into eval.go
// fires this test by leaving one of the asserted check names off the
// list.
func TestBatchScenarioChecks_UsesSharedCheckLibrary(t *testing.T) {
	scenario := BatchScenario{
		RequiredTools:     []string{"read_file"},
		ForbiddenTools:    []string{"write_file"},
		RequiredFinalText: []string{"done"},
		RequiredToolResultText: map[string][]string{
			"subagent_run": {"report"},
			"skill":        {"AFFENT ACTIVE SKILL"},
		},
		RequiredTruncatedResults: []string{"shell"},
		RequiredResultArtifacts:  []string{"shell"},
		RequiredToolOrder: []ToolOrderRequirement{
			{Earlier: "read_file", Later: "edit_file"},
		},
		MaxSuccessfulToolCallsByTool: map[string]int{
			"read_file": 1,
		},
		RequiredCommands: []string{`go test`, `gofmt`},
		RequiredCommandCounts: map[string]int{
			`go test`: 2,
		},
		ForbiddenCommands: []string{"| head", "|| true"},
		ProtectedFiles:    []string{"main_test.go", "doc_test.go"},
	}
	checks := BatchScenarioChecks(scenario)

	wantPrefixes := []string{
		"turn_ended_cleanly",
		"tool_called:read_file",
		"tool_not_called:write_file",
		"final_text_contains:done",
		"tool_result_contains:skill:AFFENT ACTIVE SKILL",
		"tool_result_contains:subagent_run:report",
		"tool_result_truncated:shell",
		"tool_result_artifact:shell",
		"tool_called_before:read_file->edit_file",
		"max_successful_tool_calls:read_file:1",
		"shell_command_matching:go test",
		"shell_command_matching:gofmt",
		"shell_command_matching_at_least:go test:2",
		"shell_command_lacks_unguarded:| head",
		"shell_command_lacks_unguarded:|| true",
		"file_not_edited:",
	}
	if len(checks) != len(wantPrefixes) {
		t.Fatalf("checks count = %d, want %d (%v)", len(checks), len(wantPrefixes), checks)
	}
	for i, want := range wantPrefixes {
		if !strings.HasPrefix(checks[i].Name, want) {
			t.Errorf("check[%d].Name = %q, want prefix %q", i, checks[i].Name, want)
		}
	}
}

func TestSelectBatchScenariosForSuite(t *testing.T) {
	scenarios, err := SelectBatchScenariosForSuite("small-model-tools", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(scenarios) < 6 {
		t.Fatalf("small-model-tools suite size = %d, want at least 6", len(scenarios))
	}
	foundOversized := false
	foundRepeatedRead := false
	foundEditRecovery := false
	for _, scenario := range scenarios {
		if !scenarioInSuite(scenario, "small-model-tools") {
			t.Fatalf("scenario %s missing suite marker", scenario.Name)
		}
		if scenario.Name == "runtime-oversized-tool-result" {
			foundOversized = true
		}
		if scenario.Name == "small-tools-repeated-read" {
			foundRepeatedRead = true
			if scenario.MaxSuccessfulToolCallsByTool["read_file"] != 1 {
				t.Fatalf("small-tools-repeated-read read_file cap = %#v, want 1", scenario.MaxSuccessfulToolCallsByTool)
			}
		}
		if scenario.Name == "small-tools-edit-recovery" {
			foundEditRecovery = true
			if len(scenario.RequiredToolOrder) != 1 || scenario.RequiredToolOrder[0] != (ToolOrderRequirement{Earlier: "read_file", Later: "edit_file"}) {
				t.Fatalf("small-tools-edit-recovery order = %#v, want read_file before edit_file", scenario.RequiredToolOrder)
			}
		}
	}
	if !foundOversized {
		t.Fatalf("small-model-tools suite missing runtime-oversized-tool-result")
	}
	if !foundRepeatedRead {
		t.Fatalf("small-model-tools suite missing small-tools-repeated-read")
	}
	if !foundEditRecovery {
		t.Fatalf("small-model-tools suite missing small-tools-edit-recovery")
	}
	one, err := SelectBatchScenariosForSuite("small-model-tools", []string{"small-tools-wrong-field-read"})
	if err != nil {
		t.Fatal(err)
	}
	if len(one) != 1 || one[0].Name != "small-tools-wrong-field-read" {
		t.Fatalf("filtered suite result = %+v", one)
	}
}

func TestRepairScenariosRequireRepeatedVerification(t *testing.T) {
	want := map[string]map[string]int{
		"coding-go-median":            {`go test`: 2},
		"coding-go-config-precedence": {`go test`: 2},
		"coding-python-slug":          {`python(3)? -m pytest`: 2},
		"coding-go-redaction-overlap": {`go test`: 2},
		"coding-python-config-parser": {`python(3)? -m pytest`: 2},
	}
	seen := map[string]bool{}
	for _, scenario := range BuiltinBatchScenarios() {
		counts, ok := want[scenario.Name]
		if !ok {
			continue
		}
		seen[scenario.Name] = true
		for pattern, min := range counts {
			if scenario.RequiredCommandCounts[pattern] != min {
				t.Fatalf("%s RequiredCommandCounts[%q] = %d, want %d; all counts=%#v", scenario.Name, pattern, scenario.RequiredCommandCounts[pattern], min, scenario.RequiredCommandCounts)
			}
		}
	}
	for name := range want {
		if !seen[name] {
			t.Fatalf("missing repair scenario %s", name)
		}
	}
}

func TestProtectedFiles(t *testing.T) {
	dir := t.TempDir()
	if err := writeScenarioFiles(dir, map[string]string{"test.py": "original"}); err != nil {
		t.Fatal(err)
	}
	snap, err := readProtectedFiles(dir, []string{"test.py"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "test.py"), []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := verifyProtectedFiles(dir, snap); err == nil {
		t.Fatal("expected protected file change to be detected")
	}
}

func TestBatchRunnerCleanupPassingWorkspace(t *testing.T) {
	dir := t.TempDir()
	workspace := filepath.Join(dir, "passing")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	res := BatchResult{OK: true}
	BatchRunner{CleanupPassingWorkspaces: true}.cleanupPassingWorkspace(&res, workspace)
	if !res.WorkspaceRemoved || res.CleanupError != "" {
		t.Fatalf("cleanup result = %+v, want removed without error", res)
	}
	if _, err := os.Stat(workspace); !os.IsNotExist(err) {
		t.Fatalf("workspace should be removed, stat err=%v", err)
	}
}

func TestBatchRunnerKeepsFailingWorkspace(t *testing.T) {
	workspace := t.TempDir()
	res := BatchResult{OK: false}
	BatchRunner{CleanupPassingWorkspaces: true}.cleanupPassingWorkspace(&res, workspace)
	if res.WorkspaceRemoved || res.CleanupError != "" {
		t.Fatalf("cleanup result = %+v, want untouched failure", res)
	}
	if _, err := os.Stat(workspace); err != nil {
		t.Fatalf("failing workspace should remain: %v", err)
	}
}

func TestBatchRunnerRunVerifierHonorsContext(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := (BatchRunner{}).runVerifier(ctx, t.TempDir(), ".", "sleep 1")
	if err == nil {
		t.Fatal("expected verifier to be killed by context timeout")
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("verifier ignored context timeout; elapsed=%s err=%v", elapsed, err)
	}
}

func TestBatchRunnerAffentctlRunArgsForwardsExecutor(t *testing.T) {
	args := (BatchRunner{
		BaseURL:     "https://llm.example/v1",
		Model:       "model-a",
		APIKey:      "secret",
		Temperature: "0",
		Executor:    "docker:affent-eval",
	}).affentctlRunArgs("/tmp/ws", "/tmp/ws/trace.jsonl", BatchScenario{
		Prompt:   "fix it",
		MaxTurns: 3,
	})
	joined := strings.Join(args, "\x00")
	for _, want := range []string{
		"--executor\x00docker:affent-eval",
		"--workspace\x00/tmp/ws",
		"--trace\x00/tmp/ws/trace.jsonl",
		"--max-turns\x003",
		"--temperature\x000",
		"--api-key\x00secret",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("args missing %q:\n%q", want, args)
		}
	}
}
