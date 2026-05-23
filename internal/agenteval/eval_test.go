package agenteval

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	}}
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
		`{"type":"tool.request","data":{"call_id":"c1","tool":"shell","args":{"command":"go test ./..."},"original_tool":"Shell","original_args_summary":"{\"cmd\":\"go test ./...\"}","canonicalized":true,"args_repaired":true,"repair_notes":["renamed tool","renamed field"]}}`,
		`{"type":"tool.result","data":{"call_id":"c1","result":"ok","exit_code":0}}`,
		`{"type":"tool.result","data":{"call_id":"guarded","result":"blocked","exit_code":1}}`,
		`{"type":"usage","data":{"input_tokens":11,"output_tokens":7}}`,
		`{"type":"error","data":{"message":"transient stream warning"}}`,
		`{"type":"message.done","data":{"text":"Conclusion: green","finish_reason":"stop"}}`,
		`{"type":"turn.end","data":{"reason":"completed","tool_stats":{"tool_requests":2,"tool_name_canonicalized":1,"tool_args_repaired":1,"tool_errors":1,"loop_guard_interventions":1,"forced_no_tools":1}}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(tracePath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	trace, err := ParseTraceFile(tracePath)
	if err != nil {
		t.Fatal(err)
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
	if tc.Result != "ok" || tc.ExitCode != 0 || tc.IsErr {
		t.Fatalf("tool result not stitched into request: %+v", tc)
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
	if trace.ToolStats.ToolRequests != 2 || trace.ToolStats.ToolArgsRepaired != 1 || trace.ToolStats.ToolErrors != 1 || trace.ToolStats.ForcedNoTools != 1 {
		t.Fatalf("ToolStats = %+v", trace.ToolStats)
	}
	if got := trace.RawTypes["tool.request"]; got != 1 {
		t.Fatalf("RawTypes[tool.request] = %d", got)
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
		RequiredCommands:  []string{`go test`, `gofmt`},
		ForbiddenCommands: []string{"| head", "|| true"},
		ProtectedFiles:    []string{"main_test.go", "doc_test.go"},
	}
	checks := BatchScenarioChecks(scenario)

	wantPrefixes := []string{
		"tool_called:read_file",
		"tool_not_called:write_file",
		"final_text_contains:done",
		"tool_result_contains:skill:AFFENT ACTIVE SKILL",
		"tool_result_contains:subagent_run:report",
		"shell_command_matching:go test",
		"shell_command_matching:gofmt",
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
	for _, scenario := range scenarios {
		if !scenarioInSuite(scenario, "small-model-tools") {
			t.Fatalf("scenario %s missing suite marker", scenario.Name)
		}
	}
	one, err := SelectBatchScenariosForSuite("small-model-tools", []string{"small-tools-wrong-field-read"})
	if err != nil {
		t.Fatal(err)
	}
	if len(one) != 1 || one[0].Name != "small-tools-wrong-field-read" {
		t.Fatalf("filtered suite result = %+v", one)
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
