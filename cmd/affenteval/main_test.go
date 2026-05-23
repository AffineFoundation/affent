package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/affinefoundation/affent/internal/agenteval"
)

func TestRunListSuites(t *testing.T) {
	out, code := captureStdout(t, func() int {
		return run([]string{"--list-suites"})
	})
	if code != 0 {
		t.Fatalf("run --list-suites exit = %d", code)
	}
	for _, want := range []string{"hard-agent", "small-model-tools"} {
		if !strings.Contains(out, want) {
			t.Fatalf("--list-suites output missing %q:\n%s", want, out)
		}
	}
}

func TestRunListSuiteScenarios(t *testing.T) {
	out, code := captureStdout(t, func() int {
		return run([]string{"--list", "--suite", "small-model-tools"})
	})
	if code != 0 {
		t.Fatalf("run --list --suite exit = %d", code)
	}
	if !strings.Contains(out, "small-tools-wrong-field-read") {
		t.Fatalf("--list --suite output missing expected scenario:\n%s", out)
	}
	if strings.Contains(out, "coding-go-median") {
		t.Fatalf("--list --suite leaked non-suite scenario:\n%s", out)
	}
}

func TestRunHelpDoesNotLeakEnvSecrets(t *testing.T) {
	t.Setenv("AFFENTCTL_BASE_URL", "https://sentinel-base.example")
	t.Setenv("AFFENTCTL_API_KEY", "sk-sentinel-secret")
	t.Setenv("AFFENTCTL_MODEL", "sentinel-model")
	t.Setenv("AFFENTEVAL_PROVIDER_LABEL", "sentinel-provider")

	help, code := captureStderr(t, func() int {
		return run([]string{"--help"})
	})
	if code != 0 {
		t.Fatalf("run --help exit = %d", code)
	}
	for _, secret := range []string{"https://sentinel-base.example", "sk-sentinel-secret", "sentinel-model", "sentinel-provider"} {
		if strings.Contains(help, secret) {
			t.Fatalf("--help leaked env value %q:\n%s", secret, help)
		}
	}
	for _, want := range []string{"AFFENTCTL_BASE_URL", "AFFENTCTL_API_KEY", "AFFENTCTL_MODEL", "AFFENTEVAL_PROVIDER_LABEL"} {
		if !strings.Contains(help, want) {
			t.Fatalf("--help missing env hint %q:\n%s", want, help)
		}
	}
}

func TestRunRejectsInvalidConfigBeforeScenarios(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "zero timeout",
			args: []string{"--timeout=0"},
			want: "--timeout must be a positive duration",
		},
		{
			name: "negative timeout",
			args: []string{"--timeout=-1s"},
			want: "--timeout must be a positive duration",
		},
		{
			name: "temperature NaN",
			args: []string{"--temperature=NaN"},
			want: "--temperature must be between 0 and 2",
		},
		{
			name: "temperature too high",
			args: []string{"--temperature=2.1"},
			want: "--temperature must be between 0 and 2",
		},
		{
			name: "temperature not number",
			args: []string{"--temperature=warm"},
			want: "--temperature",
		},
		{
			name: "unknown executor",
			args: []string{"--executor=remote"},
			want: "unknown --executor",
		},
		{
			name: "zero verifier output cap",
			args: []string{"--verifier-output-cap=0"},
			want: "--verifier-output-cap must be positive",
		},
		{
			name: "empty docker executor",
			args: []string{"--executor=docker:"},
			want: "requires a container name",
		},
		{
			name: "docker executor requires work root",
			args: []string{"--executor=docker:affent-eval"},
			want: "requires explicit --work-root",
		},
		{
			name: "docker executor requires absolute work root",
			args: []string{"--executor=docker:affent-eval", "--work-root=relative-eval"},
			want: "--work-root must be an absolute path",
		},
		{
			name: "sandbox suite rejected",
			args: []string{"--executor=sandbox", "--suite=small-model-tools"},
			want: "--executor sandbox is only supported for one selected scenario",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stderr, code := captureStderr(t, func() int {
				return run(tc.args)
			})
			if code != 64 {
				t.Fatalf("exit = %d, want 64; stderr:\n%s", code, stderr)
			}
			if !strings.Contains(stderr, tc.want) {
				t.Fatalf("stderr missing %q:\n%s", tc.want, stderr)
			}
		})
	}
}

func TestPrintBatchResultIncludesTraceMetrics(t *testing.T) {
	var out bytes.Buffer
	printBatchResult(&out, agenteval.BatchResult{
		BatchScenario:    "sample",
		Workspace:        "/tmp/ws",
		TracePath:        "/tmp/ws/trace.jsonl",
		OK:               true,
		Duration:         1234 * time.Millisecond,
		TurnEndReason:    "completed",
		ToolCalls:        3,
		WorkspaceRemoved: true,
		ToolStats: agenteval.ToolRuntimeStats{
			ToolArgsRepaired: 1,
			ToolErrors:       2,
			ToolDurationMS:   45,
		},
		ToolTruncation: agenteval.ToolTruncationStats{
			ArgsTruncated:       1,
			ArgsOmittedBytes:    512,
			ResultsTruncated:    1,
			ResultsOmittedBytes: 4096,
			ResultArtifacts:     1,
		},
		Verifier: agenteval.VerifierResult{
			Command:            "go test ./...",
			Ran:                true,
			OK:                 true,
			ExitCode:           0,
			Duration:           80 * time.Millisecond,
			OutputBytes:        1200,
			OutputTruncated:    true,
			OutputOmittedBytes: 176,
			OutputCapBytes:     1024,
		},
		Usage: agenteval.Usage{InputTokens: 100, OutputTokens: 25},
	})
	got := out.String()
	for _, want := range []string{
		"PASS sample (1.234s)",
		"workspace: /tmp/ws (removed)",
		"trace: /tmp/ws/trace.jsonl",
		"metrics: tools=3 errors=2 repaired=1 tool_ms=45 tokens=100/25 trunc=args:1,results:1,artifacts:1 omitted=512/4096 end=completed",
		`verifier: pass exit=0 duration=80ms output=1200 truncated omitted=176 cap=1024 command="go test ./..."`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("output missing %q:\n%s", want, got)
		}
	}
}

func TestBatchSummaryAggregatesRuntimeMetrics(t *testing.T) {
	var summary batchSummary
	summary.add(agenteval.BatchResult{
		OK:                 true,
		Duration:           100 * time.Millisecond,
		ToolCalls:          2,
		WorkspaceRemoved:   true,
		TraceSchemaVersion: 1,
		TurnEndReason:      "completed",
		ToolStats: agenteval.ToolRuntimeStats{
			ToolArgsRepaired: 1,
			ToolErrors:       0,
			ToolDurationMS:   10,
		},
		ToolTruncation: agenteval.ToolTruncationStats{
			ArgsTruncated:    1,
			ArgsOmittedBytes: 128,
		},
		Verifier: agenteval.VerifierResult{Ran: true, OK: true, ExitCode: 0, OutputBytes: 64, OutputCapBytes: 1024},
		Usage:    agenteval.Usage{InputTokens: 20, OutputTokens: 5},
	})
	summary.add(agenteval.BatchResult{
		OK:                 false,
		Duration:           250 * time.Millisecond,
		ToolCalls:          3,
		TraceSchemaVersion: 1,
		TurnEndReason:      "max_turns",
		Failures: []string{
			`turn ended with reason "max_turns" (expected completed)`,
			`missing required command match "go test"; commands=[]`,
		},
		ToolStats: agenteval.ToolRuntimeStats{
			ToolArgsRepaired: 2,
			ToolErrors:       1,
			ToolDurationMS:   40,
		},
		ToolTruncation: agenteval.ToolTruncationStats{
			ResultsTruncated:    2,
			ResultsOmittedBytes: 2048,
			ResultArtifacts:     1,
		},
		Verifier: agenteval.VerifierResult{
			Ran:                true,
			OK:                 false,
			ExitCode:           1,
			OutputBytes:        4096,
			OutputTruncated:    true,
			OutputOmittedBytes: 2048,
			OutputCapBytes:     2048,
		},
		Usage: agenteval.Usage{InputTokens: 70, OutputTokens: 15},
	})

	var out bytes.Buffer
	printBatchSummary(&out, summary)
	want := "SUMMARY scenarios=2 passed=1 failed=1 duration=350ms tools=5 errors=1 repaired=3 tool_ms=50 trunc=args:1,results:2,artifacts:1 omitted=128/2048 verifier=run:2,passed:1,failed:1,truncated:1,omitted:2048 tokens=90/20 ends=completed:1,max_turns:1,error:0,cancelled:0,unknown:0 failure_kinds=missing_command:1,turn_end:1 removed_workspaces=1 cleanup_errors=0"
	if !strings.Contains(out.String(), want) {
		t.Fatalf("summary output missing %q:\n%s", want, out.String())
	}
	if summary.TraceSchemaVersions[1] != 2 {
		t.Fatalf("TraceSchemaVersions = %#v, want version 1 count 2", summary.TraceSchemaVersions)
	}
}

func TestPrintBatchResultJSONL(t *testing.T) {
	var out bytes.Buffer
	printBatchResultJSONL(&out, testEvalJSONLMetadata(), agenteval.BatchResult{
		BatchScenario:      "sample",
		Workspace:          "/tmp/ws",
		TracePath:          "/tmp/ws/trace.jsonl",
		OK:                 true,
		Duration:           1500 * time.Millisecond,
		TraceSchemaVersion: 1,
		TurnEndReason:      "completed",
		ToolCalls:          4,
		WorkspaceRemoved:   true,
		ToolStats: agenteval.ToolRuntimeStats{
			ToolArgsRepaired: 2,
			ToolErrors:       1,
			ToolDurationMS:   75,
		},
		ToolTruncation: agenteval.ToolTruncationStats{
			ArgsTruncated:       2,
			ArgsOmittedBytes:    1024,
			ResultsTruncated:    1,
			ResultsOmittedBytes: 8192,
			ResultArtifacts:     1,
		},
		Verifier: agenteval.VerifierResult{
			Command:            "go test ./...",
			Ran:                true,
			OK:                 false,
			ExitCode:           1,
			Duration:           25 * time.Millisecond,
			OutputBytes:        2048,
			OutputTruncated:    true,
			OutputOmittedBytes: 1024,
			OutputCapBytes:     1024,
		},
		Usage: agenteval.Usage{InputTokens: 200, OutputTokens: 50},
	})

	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("jsonl result did not decode: %v\n%s", err, out.String())
	}
	for key, want := range map[string]any{
		"schema_version":                float64(1),
		"type":                          "scenario",
		"suite":                         "small-model-tools",
		"model":                         "eval-model",
		"provider_label":                "eval-provider",
		"executor":                      "docker:affent-eval",
		"temperature":                   "0.2",
		"timeout_ms":                    float64(300000),
		"scenario":                      "sample",
		"ok":                            true,
		"duration_ms":                   float64(1500),
		"trace_schema_version":          float64(1),
		"turn_end_reason":               "completed",
		"tool_calls":                    float64(4),
		"tool_errors":                   float64(1),
		"tool_repaired":                 float64(2),
		"tool_duration_ms":              float64(75),
		"tool_args_truncated":           float64(2),
		"tool_args_omitted_bytes":       float64(1024),
		"tool_results_truncated":        float64(1),
		"tool_results_omitted_bytes":    float64(8192),
		"tool_result_artifacts":         float64(1),
		"verifier_command":              "go test ./...",
		"verifier_ran":                  true,
		"verifier_ok":                   false,
		"verifier_exit_code":            float64(1),
		"verifier_duration_ms":          float64(25),
		"verifier_output_bytes":         float64(2048),
		"verifier_output_truncated":     true,
		"verifier_output_omitted_bytes": float64(1024),
		"verifier_output_cap_bytes":     float64(1024),
		"input_tokens":                  float64(200),
		"output_tokens":                 float64(50),
		"workspace_removed":             true,
	} {
		if got[key] != want {
			t.Fatalf("%s = %v, want %v\njson=%s", key, got[key], want, out.String())
		}
	}
	if _, ok := got["failures"]; ok {
		t.Fatalf("passing result should omit failures, got %#v", got["failures"])
	}
	if _, ok := got["failure_kinds"]; ok {
		t.Fatalf("passing result should omit failure_kinds, got %#v", got["failure_kinds"])
	}
}

func TestPrintBatchResultJSONLIncludesFailureKinds(t *testing.T) {
	var out bytes.Buffer
	printBatchResultJSONL(&out, testEvalJSONLMetadata(), agenteval.BatchResult{
		BatchScenario:      "failing",
		Workspace:          "/tmp/ws",
		TracePath:          "/tmp/ws/trace.jsonl",
		OK:                 false,
		Duration:           500 * time.Millisecond,
		TraceSchemaVersion: 1,
		TurnEndReason:      "max_turns",
		Failures: []string{
			`turn ended with reason "max_turns" (expected completed)`,
			`missing required command match "go test"; commands=[]`,
			`missing required command match "pytest"; commands=[]`,
		},
	})

	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("jsonl result did not decode: %v\n%s", err, out.String())
	}
	failures, ok := got["failures"].([]any)
	if !ok || len(failures) != 3 {
		t.Fatalf("failures = %#v, want 3 entries\njson=%s", got["failures"], out.String())
	}
	failureKinds, ok := got["failure_kinds"].(map[string]any)
	if !ok {
		t.Fatalf("failure_kinds missing or wrong type: %#v\njson=%s", got["failure_kinds"], out.String())
	}
	if failureKinds["turn_end"] != float64(1) || failureKinds["missing_command"] != float64(2) {
		t.Fatalf("failure_kinds = %#v", failureKinds)
	}
	if got["trace_schema_version"] != float64(1) {
		t.Fatalf("trace_schema_version = %#v, want 1", got["trace_schema_version"])
	}
}

func TestPrintBatchSummaryJSONL(t *testing.T) {
	var out bytes.Buffer
	printBatchSummaryJSONL(&out, testEvalJSONLMetadata(), batchSummary{
		Total:                      2,
		Passed:                     1,
		Failed:                     1,
		Duration:                   2500 * time.Millisecond,
		ToolCalls:                  5,
		ToolErrors:                 1,
		ToolRepaired:               3,
		ToolDurationMS:             120,
		ToolArgsTruncated:          1,
		ToolArgsOmittedBytes:       256,
		ToolResultsTruncated:       2,
		ToolResultsOmittedBytes:    4096,
		ToolResultArtifacts:        2,
		VerifierRuns:               2,
		VerifierPassed:             1,
		VerifierFailed:             1,
		VerifierOutputTruncated:    1,
		VerifierOutputOmittedBytes: 1024,
		TraceSchemaVersions:        map[int]int{1: 2},
		InputTokens:                90,
		OutputTokens:               20,
		EndCompleted:               1,
		EndMaxTurns:                1,
		EndErrors:                  0,
		EndCancelled:               0,
		EndUnknown:                 0,
		FailureKinds:               map[string]int{"missing_command": 1, "turn_end": 1},
		RemovedWorkspaces:          1,
	})

	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("jsonl summary did not decode: %v\n%s", err, out.String())
	}
	for key, want := range map[string]any{
		"schema_version":                float64(1),
		"type":                          "summary",
		"suite":                         "small-model-tools",
		"model":                         "eval-model",
		"provider_label":                "eval-provider",
		"executor":                      "docker:affent-eval",
		"temperature":                   "0.2",
		"timeout_ms":                    float64(300000),
		"scenarios":                     float64(2),
		"passed":                        float64(1),
		"failed":                        float64(1),
		"duration_ms":                   float64(2500),
		"tool_calls":                    float64(5),
		"tool_errors":                   float64(1),
		"tool_repaired":                 float64(3),
		"tool_duration_ms":              float64(120),
		"tool_args_truncated":           float64(1),
		"tool_args_omitted_bytes":       float64(256),
		"tool_results_truncated":        float64(2),
		"tool_results_omitted_bytes":    float64(4096),
		"tool_result_artifacts":         float64(2),
		"verifier_runs":                 float64(2),
		"verifier_passed":               float64(1),
		"verifier_failed":               float64(1),
		"verifier_output_truncated":     float64(1),
		"verifier_output_omitted_bytes": float64(1024),
		"input_tokens":                  float64(90),
		"output_tokens":                 float64(20),
		"end_completed":                 float64(1),
		"end_max_turns":                 float64(1),
		"end_errors":                    float64(0),
		"end_cancelled":                 float64(0),
		"end_unknown":                   float64(0),
		"removed_workspaces":            float64(1),
		"cleanup_errors":                float64(0),
	} {
		if got[key] != want {
			t.Fatalf("%s = %v, want %v\njson=%s", key, got[key], want, out.String())
		}
	}
	failureKinds, ok := got["failure_kinds"].(map[string]any)
	if !ok {
		t.Fatalf("failure_kinds missing or wrong type: %#v\njson=%s", got["failure_kinds"], out.String())
	}
	if failureKinds["missing_command"] != float64(1) || failureKinds["turn_end"] != float64(1) {
		t.Fatalf("failure_kinds = %#v", failureKinds)
	}
	traceSchemaVersions, ok := got["trace_schema_versions"].(map[string]any)
	if !ok {
		t.Fatalf("trace_schema_versions missing or wrong type: %#v\njson=%s", got["trace_schema_versions"], out.String())
	}
	if traceSchemaVersions["1"] != float64(2) {
		t.Fatalf("trace_schema_versions = %#v", traceSchemaVersions)
	}
}

func TestCloneTraceSchemaVersions(t *testing.T) {
	if got := cloneTraceSchemaVersions(nil); got != nil {
		t.Fatalf("nil trace schema versions should produce nil map, got %#v", got)
	}
	in := map[int]int{1: 2}
	got := cloneTraceSchemaVersions(in)
	if got[1] != 2 {
		t.Fatalf("cloneTraceSchemaVersions = %#v, want version 1 count 2", got)
	}
	got[1] = 3
	if in[1] != 2 {
		t.Fatalf("cloneTraceSchemaVersions should not alias input, input = %#v", in)
	}
}

func TestEvalJSONLMetadataFromConfig(t *testing.T) {
	t.Setenv("AFFENTCTL_MODEL", "env-model")
	t.Setenv("AFFENTEVAL_PROVIDER_LABEL", "env-provider")
	meta := evalJSONLMetadataFromConfig("small-model-tools", "", "", "", "0", 5*time.Minute)
	if meta.SchemaVersion != evalJSONLSchemaVersion {
		t.Fatalf("SchemaVersion = %d, want %d", meta.SchemaVersion, evalJSONLSchemaVersion)
	}
	if meta.Model != "env-model" || meta.ProviderLabel != "env-provider" {
		t.Fatalf("env metadata not used: %+v", meta)
	}
	if meta.Executor != "local" {
		t.Fatalf("Executor = %q, want local", meta.Executor)
	}
	if meta.Suite != "small-model-tools" || meta.Temperature != "0" || meta.TimeoutMS != int64(300000) {
		t.Fatalf("metadata = %+v", meta)
	}

	meta = evalJSONLMetadataFromConfig(" custom ", " flag-model ", " flag-provider ", " sandbox ", " 0.4 ", time.Second)
	if meta.Model != "flag-model" || meta.ProviderLabel != "flag-provider" || meta.Executor != "sandbox" || meta.Temperature != "0.4" || meta.Suite != "custom" || meta.TimeoutMS != 1000 {
		t.Fatalf("flag metadata not normalized: %+v", meta)
	}
}

func TestFailureKindsForResult(t *testing.T) {
	if got := failureKindsForResult(nil); got != nil {
		t.Fatalf("nil failures should produce nil map, got %#v", got)
	}
	got := failureKindsForResult([]string{
		`turn ended with reason "max_turns" (expected completed)`,
		`missing required command match "go test"; commands=[]`,
		`missing required command match "pytest"; commands=[]`,
	})
	if got["turn_end"] != 1 || got["missing_command"] != 2 {
		t.Fatalf("failureKindsForResult = %#v", got)
	}
}

func testEvalJSONLMetadata() evalJSONLMetadata {
	return evalJSONLMetadata{
		SchemaVersion: evalJSONLSchemaVersion,
		Suite:         "small-model-tools",
		Model:         "eval-model",
		ProviderLabel: "eval-provider",
		Executor:      "docker:affent-eval",
		Temperature:   "0.2",
		TimeoutMS:     int64(300000),
	}
}

func TestFailureKind(t *testing.T) {
	cases := []struct {
		failure string
		want    string
	}{
		{"affentctl run failed: exit=1", "affentctl_run"},
		{"verify command failed: go test: exit status 1", "verify_command"},
		{"parse trace: open trace.jsonl: no such file", "parse_trace"},
		{`turn ended with reason "max_turns" (expected completed)`, "turn_end"},
		{`missing required command match "go test"; commands=[]`, "missing_command"},
		{`forbidden command substring "| head" in "go test | head"`, "forbidden_command"},
		{`protected file changed: app_test.go`, "protected_file"},
		{`forbidden content "bad" found in config.py`, "forbidden_content"},
		{`final text did not contain "done"; got ""`, "final_text_missing"},
		{`final text leaked 1 forbidden substring(s): [ignore me]`, "final_text_forbidden"},
		{`expected at least one "read_file" invocation, got 0 tool calls`, "missing_tool"},
		{`found forbidden "write_file" call (call_id=c1 args=map[])`, "forbidden_tool"},
		{`expected "shell" result to contain "ok"; tools=shell`, "tool_result_missing"},
		{`something else`, "other"},
	}
	for _, tc := range cases {
		if got := failureKind(tc.failure); got != tc.want {
			t.Fatalf("failureKind(%q) = %q, want %q", tc.failure, got, tc.want)
		}
	}
}

func captureStdout(t *testing.T, fn func() int) (string, int) {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	var buf bytes.Buffer
	done := make(chan error, 1)
	go func() {
		_, err := io.Copy(&buf, r)
		done <- err
	}()
	code := fn()
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	os.Stdout = old
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.String(), code
}

func captureStderr(t *testing.T, fn func() int) (string, int) {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	var buf bytes.Buffer
	done := make(chan error, 1)
	go func() {
		_, err := io.Copy(&buf, r)
		done <- err
	}()
	code := fn()
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	os.Stderr = old
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.String(), code
}
