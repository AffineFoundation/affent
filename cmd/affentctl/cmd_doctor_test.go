package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/affinefoundation/affent/internal/agent"
)

func TestMCPDoctorHelper(t *testing.T) {
	if os.Getenv("AFFENT_MCP_DOCTOR_HELPER") != "1" {
		return
	}
	toolsJSON := os.Getenv("AFFENT_MCP_DOCTOR_TOOLS")
	if strings.TrimSpace(toolsJSON) == "" {
		toolsJSON = "[]"
	}
	var tools []map[string]any
	if err := json.Unmarshal([]byte(toolsJSON), &tools); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	writeResult := func(id any, result any) {
		raw, err := json.Marshal(map[string]any{
			"jsonrpc": "2.0",
			"id":      id,
			"result":  result,
		})
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		fmt.Println(string(raw))
	}
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		var req struct {
			ID     any    `json:"id"`
			Method string `json:"method"`
		}
		if err := json.Unmarshal(sc.Bytes(), &req); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		switch req.Method {
		case "initialize":
			writeResult(req.ID, map[string]any{
				"protocolVersion": "2025-06-18",
				"capabilities":    map[string]any{},
				"serverInfo": map[string]any{
					"name":    "doctor-helper",
					"version": "test",
				},
			})
		case "notifications/initialized":
		case "tools/list":
			writeResult(req.ID, map[string]any{"tools": tools})
		default:
			writeResult(req.ID, map[string]any{})
		}
	}
	os.Exit(0)
}

type errorCommandRunner struct {
	err error
}

func (r errorCommandRunner) Run(name string, args ...string) (string, error) {
	return "", r.err
}

type dockerInspectRunner struct {
	out   string
	err   error
	calls []recordedCommand
}

func (r *dockerInspectRunner) Run(name string, args ...string) (string, error) {
	r.calls = append(r.calls, recordedCommand{name: name, args: append([]string(nil), args...)})
	if len(args) >= 1 && args[0] == "inspect" {
		return r.out, r.err
	}
	return "", nil
}

func mcpDoctorHelperServer(t *testing.T, name string, tools []map[string]any) map[string]any {
	t.Helper()
	rawTools, err := json.Marshal(tools)
	if err != nil {
		t.Fatal(err)
	}
	return map[string]any{
		"name":    name,
		"command": os.Args[0],
		"args":    []string{"-test.run=TestMCPDoctorHelper"},
		"env": []string{
			"AFFENT_MCP_DOCTOR_HELPER=1",
			"AFFENT_MCP_DOCTOR_TOOLS=" + string(rawTools),
		},
	}
}

func writeMCPDoctorConfig(t *testing.T, dir string, servers []map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"servers": servers})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "mcp.json")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDoctorCmdReportsReadyLocalConfig(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "ws")
	var stdout, stderr strings.Builder
	code := doctorCmdWithRunner([]string{
		"--workspace", workspace,
		"--model", "gpt-4o-mini",
		"--api-key", "key",
		"--executor", "local",
	}, &fakeCommandRunner{}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	got := stdout.String()
	for _, want := range []string{
		"ok workspace:",
		workspace + " is writable",
		"ok model:",
		"gpt-4o-mini",
		"ok api-key:",
		"ok boundaries:",
		"prompt_input=256KiB",
		"config=1MiB",
		"max_turns=10",
		"call_timeout=3m0s",
		"llm_request=8MiB",
		"llm_error_body=64KiB",
		"stream_content=1MiB",
		"stream_tool_calls=64",
		"stream_scanner=4MiB",
		"tool_args_event=64KiB",
		"tool_arg_string=4KiB",
		"tool_result_context=8KiB",
		"tool_result_context_budget=32KiB",
		"tool_result_event=256KiB",
		"repairable_tool_args=1MiB",
		"project_context=32KiB",
		"mcp_result=256KiB",
		"ok capabilities:",
		"shell_file=true",
		"skill_install=true",
		"memory=true",
		"memory_only=false",
		"session_search=true",
		"project_context=true",
		"symbol_context=true",
		"repo_search=true",
		"web_fetch=false",
		"web_search=false",
		"browser=false",
		"subagent=true",
		"subagent_max_depth=2",
		"focused_tasks=true",
		"executor=local",
		"ok executor:",
		"local",
		"ok runtime-image:",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("doctor output missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "error ") {
		t.Fatalf("doctor output should not contain errors:\n%s", got)
	}
}

func TestDoctorCmdReportsOutputReservedCompactionPolicy(t *testing.T) {
	var stdout, stderr strings.Builder
	code := doctorCmdWithRunner([]string{
		"--workspace", t.TempDir(),
		"--model", "gpt-4o-mini",
		"--api-key", "key",
		"--executor", "local",
		"--max-tokens", "30000",
		"--model-context-window-tokens", "100000",
		"--compact-trigger-input-percent", "80",
	}, &fakeCommandRunner{}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	got := stdout.String()
	if !strings.Contains(got, "ok compaction:") ||
		!strings.Contains(got, "trigger_bytes=280000") ||
		!strings.Contains(got, "trigger_input_tokens=70000") {
		t.Fatalf("doctor output missing output-reserved compaction policy:\n%s", got)
	}
}

func TestDoctorCmdUsesAutoModelContextWindowForCompactionPolicy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Fatalf("path = %q, want /v1/models", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"auto-model","context_window":100000}]}`))
	}))
	t.Cleanup(srv.Close)

	var stdout, stderr strings.Builder
	code := doctorCmdWithRunner([]string{
		"--workspace", t.TempDir(),
		"--base-url", srv.URL + "/v1",
		"--model", "auto-model",
		"--executor", "local",
		"--model-context-window-auto",
		"--max-tokens", "30000",
		"--compact-trigger-input-percent", "80",
	}, &fakeCommandRunner{}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	got := stdout.String()
	if !strings.Contains(got, "ok compaction:") ||
		!strings.Contains(got, "trigger_bytes=280000") ||
		!strings.Contains(got, "trigger_input_tokens=70000") {
		t.Fatalf("doctor output did not use auto model context window for compaction policy:\n%s", got)
	}
}

// TestDoctorCapabilitySummary_FocusedTaskProfiles_Default pins the
// per-profile breakdown doctor exposes under affentctl's default
// configuration. The breakdown matters because the model only sees
// task_types whose deps are wired — without this report an operator
// can't tell which task_type values their deployment actually accepts
// (and would have to guess from the schema enum in trace events).
//
// Under default affentctl wiring: workspace + sessions + memory are
// always present, no web registrar → research is the one profile
// always filtered out.
func TestDoctorCapabilitySummary_FocusedTaskProfiles_Default(t *testing.T) {
	got := doctorCapabilitySummary(commonFlags{
		memoryEnabled:       true,
		projectContext:      true,
		subagentEnabled:     true,
		subagentMaxDepth:    2,
		focusedTasksEnabled: true,
		executor:            "local",
	})
	want := "focused_task_profiles=recall,explore,verify,review"
	if !strings.Contains(got, want) {
		t.Fatalf("doctor capability summary missing %q:\n%s", want, got)
	}
	// And explicitly NOT research — that's the contract this whole
	// reporting line exists to surface for the operator.
	if strings.Contains(got, "research") {
		t.Fatalf("research must be filtered out without a web registrar:\n%s", got)
	}
}

func TestDoctorCapabilitySummary_RepoSearch_DefaultAndMemoryOnly(t *testing.T) {
	got := doctorCapabilitySummary(commonFlags{
		memoryEnabled:       true,
		projectContext:      true,
		subagentEnabled:     true,
		focusedTasksEnabled: true,
		executor:            "local",
	})
	if !strings.Contains(got, "repo_search=true") {
		t.Fatalf("repo_search should be on by default in affentctl:\n%s", got)
	}
	if !strings.Contains(got, "symbol_context=true") {
		t.Fatalf("symbol_context should be on by default in affentctl:\n%s", got)
	}

	got = doctorCapabilitySummary(commonFlags{
		memoryEnabled:       true,
		memoryOnly:          true,
		projectContext:      true,
		focusedTasksEnabled: true,
		executor:            "sandbox",
	})
	if !strings.Contains(got, "repo_search=false") {
		t.Fatalf("memory-only should not report repo_search:\n%s", got)
	}
	if !strings.Contains(got, "symbol_context=false") {
		t.Fatalf("memory-only should not report symbol_context:\n%s", got)
	}
}

// TestDoctorCapabilitySummary_FocusedTaskProfiles_OffWhenDisabled
// pins the "off" / "none" sentinels so an operator can tell at a
// glance between "feature disabled" and "feature enabled but every
// profile filtered out by deps" — those are different ops conditions
// and both should be greppable.
func TestDoctorCapabilitySummary_FocusedTaskProfiles_OffWhenDisabled(t *testing.T) {
	got := doctorCapabilitySummary(commonFlags{
		memoryEnabled:       true,
		projectContext:      true,
		subagentEnabled:     true,
		focusedTasksEnabled: false, // explicitly off
		executor:            "local",
	})
	if !strings.Contains(got, "focused_tasks=false") {
		t.Fatalf("expected focused_tasks=false marker:\n%s", got)
	}
	if !strings.Contains(got, "focused_task_profiles=off") {
		t.Fatalf("expected focused_task_profiles=off sentinel for disabled feature:\n%s", got)
	}
}

// TestDoctorCapabilitySummary_FocusedTaskProfiles_MemoryOnlyForcesOff
// pins the memory-only interaction: memory-only mode strips every
// non-memory tool, so even if focusedTasksEnabled=true upstream the
// reported state must be off. This mirrors applyConfig's coercion of
// focusedTasksEnabled to false under memoryOnly.
func TestDoctorCapabilitySummary_FocusedTaskProfiles_MemoryOnlyForcesOff(t *testing.T) {
	got := doctorCapabilitySummary(commonFlags{
		memoryEnabled:       true,
		memoryOnly:          true,
		focusedTasksEnabled: true, // would be coerced to false by applyConfig
		executor:            "sandbox",
	})
	if !strings.Contains(got, "focused_tasks=false") || !strings.Contains(got, "focused_task_profiles=off") {
		t.Fatalf("memory-only must report focused_tasks=false and profiles=off:\n%s", got)
	}
}

// TestDoctorCapabilitySummary_FocusedTaskProfiles_NoMemoryStillExposesRecall
// pins graceful degradation: without the memory tool, recall is still
// available because session_search satisfies recall's "any declared
// capability" rule. This is the behavior contract — if a future
// refactor strict-ifies the matrix, this test catches it.
func TestDoctorCapabilitySummary_FocusedTaskProfiles_NoMemoryStillExposesRecall(t *testing.T) {
	got := doctorCapabilitySummary(commonFlags{
		memoryEnabled:       false,
		projectContext:      true,
		focusedTasksEnabled: true,
		executor:            "local",
	})
	want := "focused_task_profiles=recall,explore,verify,review"
	if !strings.Contains(got, want) {
		t.Fatalf("recall should remain available via session_search even without memory; got:\n%s", got)
	}
}

func TestDoctorCapabilitySummaryMemoryOnlyMatchesRegisteredTools(t *testing.T) {
	got := doctorCapabilitySummary(commonFlags{
		memoryEnabled:       true,
		memoryOnly:          true,
		projectContext:      true,
		mcpConfigPath:       "/tmp/mcp.json",
		subagentEnabled:     true,
		subagentMaxDepth:    4,
		focusedTasksEnabled: true,
		executor:            "sandbox",
	})
	for _, want := range []string{
		"shell_file=false",
		"skill_install=false",
		"memory=true",
		"memory_only=true",
		"session_search=false",
		"project_context=false",
		"symbol_context=false",
		"repo_search=false",
		"web_fetch=false",
		"web_search=false",
		"browser=false",
		"mcp=false",
		"subagent=false",
		"subagent_max_depth=4",
		"focused_tasks=false",
		"executor=sandbox",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("capability summary missing %q:\n%s", want, got)
		}
	}
}

func TestDoctorCapabilitySummaryEvalModeMatchesStrictSurface(t *testing.T) {
	got := doctorCapabilitySummary(commonFlags{
		evalMode:            true,
		memoryEnabled:       true,
		projectContext:      true,
		mcpConfigPath:       "/tmp/mcp.json",
		subagentEnabled:     true,
		subagentMaxDepth:    4,
		focusedTasksEnabled: true,
		executor:            "local",
	})
	for _, want := range []string{
		"shell_file=false",
		"skill_install=false",
		"memory=false",
		"memory_only=false",
		"eval_mode=true",
		"session_search=false",
		"project_context=false",
		"symbol_context=false",
		"repo_search=false",
		"web_fetch=false",
		"web_search=false",
		"browser=false",
		"mcp=true",
		"subagent=false",
		"subagent_max_depth=4",
		"focused_tasks=false",
		"focused_task_profiles=off",
		"executor=local",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("capability summary missing %q:\n%s", want, got)
		}
	}
}

func TestDoctorBoundarySummaryUsesConfiguredTurnLimits(t *testing.T) {
	got := doctorBoundarySummary(commonFlags{
		maxTurns:    7,
		callTimeout: 9,
	})
	for _, want := range []string{
		"max_turns=7",
		"call_timeout=9ns",
		"prompt_input=256KiB",
		"system_prompt=256KiB",
		"config=1MiB",
		"llm_request=8MiB",
		"stream_reasoning=1MiB",
		"tool_result_preview=4KiB",
		"repairable_tool_args=1MiB",
		"project_context=32KiB",
		"loop_guard_identical_calls=3",
		"loop_guard_failure_warn=3",
		"loop_guard_failure_halt=8",
		"loop_guard_browser_find_no_match=3",
		"plan_per_turn_calls=6",
		"plan_steps=12",
		"plan_step_text=240B",
		"plan_note=500B",
		"plan_evidence_refs=6",
		"plan_evidence_ref=240B",
		"plan_state=32KiB",
		"active_plan_step=160B",
		"active_plan_note=160B",
		"active_plan_evidence_refs=3",
		"active_plan_evidence_ref=120B",
		"focused_task_default_turns=4",
		"focused_task_max_turns=12",
		"focused_task_per_turn_calls=3",
		"focused_task_type=32B",
		"focused_task_objective=4KiB",
		"focused_task_tool_result=8KiB",
		"focused_task_summary=2000B",
		"focused_task_finding_evidence=1000B",
		"focused_task_findings=20",
		"focused_task_list_entries=20",
		"focused_task_tool_calls=20",
		"subagent_default_turns=6",
		"subagent_max_turns=12",
		"subagent_task=8KiB",
		"subagent_mode=64B",
		"subagent_tool_result=4KiB",
		"subagent_default_depth=2",
		"subagent_hard_max_depth=4",
		"skill_action=16B",
		"skill_name=128B",
		"skill_description=512B",
		"skill_body=64KiB",
		"skill_source=2KiB",
		"skill_triggers=20",
		"skill_trigger=128B",
		"skill_required_tools=20",
		"skill_required_tool=128B",
		"runtime_skills=128",
		"runtime_skill_dir_read_batch=64",
		"tool_result_context_budget=32KiB",
		"runtime_skill_manifest=9KiB",
		"runtime_skill_proposal=79KiB",
		"runtime_skill_proposal_id=16B",
		"mcp_http_json=4MiB",
		"mcp_http_sse_line=4MiB",
		"mcp_stdio_frame=4MiB",
		"jsonl_record=4MiB",
		"memory_file=1MiB",
		"memory_search_query=2KiB",
		"memory_search_terms=16",
		"memory_search_snippet=500",
		"memory_response_entry=1000",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("boundary summary missing %q:\n%s", want, got)
		}
	}
}

func TestFormatBytes(t *testing.T) {
	cases := map[int]string{
		0:           "0B",
		1:           "1B",
		1024:        "1KiB",
		256 * 1024:  "256KiB",
		1024 * 1024: "1MiB",
	}
	for n, want := range cases {
		if got := formatBytes(n); got != want {
			t.Fatalf("formatBytes(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestDoctorCmdRejectsUnexpectedArgs(t *testing.T) {
	var stdout, stderr strings.Builder
	code := doctorCmdWithRunner([]string{"extra"}, &fakeCommandRunner{}, &stdout, &stderr)
	if code != 64 {
		t.Fatalf("exit = %d stdout=%s stderr=%s, want 64", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "unexpected argument") {
		t.Fatalf("stderr = %q, want unexpected argument", stderr.String())
	}
}

func TestDoctorCmdFailsMissingModelAndInvalidExecutor(t *testing.T) {
	var stdout, stderr strings.Builder
	code := doctorCmdWithRunner([]string{
		"--workspace", t.TempDir(),
		"--executor", "bad",
	}, &fakeCommandRunner{}, &stdout, &stderr)
	if code != 3 {
		t.Fatalf("exit = %d stderr=%s stdout=%s, want 3", code, stderr.String(), stdout.String())
	}
	got := stdout.String()
	for _, want := range []string{
		"error model:",
		"missing --model",
		"error api-key:",
		"error executor:",
		"unknown --executor",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("doctor output missing %q:\n%s", want, got)
		}
	}
}

func TestDoctorCmdFailsMissingAPIKeyForDefaultEndpoint(t *testing.T) {
	var stdout, stderr strings.Builder
	code := doctorCmdWithRunner([]string{
		"--workspace", t.TempDir(),
		"--model", "gpt-4o-mini",
		"--base-url", agent.DefaultBaseURL + "/",
	}, &fakeCommandRunner{}, &stdout, &stderr)
	if code != 3 {
		t.Fatalf("exit = %d stderr=%s stdout=%s, want 3", code, stderr.String(), stdout.String())
	}
	got := stdout.String()
	if !strings.Contains(got, "error api-key:") || !strings.Contains(got, "default OpenAI endpoint") {
		t.Fatalf("doctor output should report missing API key for default endpoint:\n%s", got)
	}
}

func TestDoctorCmdWarnsMissingAPIKeyForCustomEndpoint(t *testing.T) {
	var stdout, stderr strings.Builder
	code := doctorCmdWithRunner([]string{
		"--workspace", t.TempDir(),
		"--model", "local-model",
		"--base-url", "http://127.0.0.1:11434/v1",
	}, &fakeCommandRunner{}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	got := stdout.String()
	if !strings.Contains(got, "warn api-key:") || strings.Contains(got, "error api-key:") {
		t.Fatalf("doctor output should warn, not error, for custom keyless endpoint:\n%s", got)
	}
}

func TestDoctorCmdChecksDockerForSandboxExecutor(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dir)
	runner := &fakeCommandRunner{}
	var stdout, stderr strings.Builder
	code := doctorCmdWithRunner([]string{
		"--model", "gpt-4o-mini",
		"--api-key", "key",
		"--executor", "sandbox",
	}, runner, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if len(runner.calls) == 0 || runner.calls[0].name != "docker" || !equalStrings(runner.calls[0].args, []string{"version", "--format", "{{.Server.Version}}"}) {
		t.Fatalf("docker version call missing: %+v", runner.calls)
	}
	got := stdout.String()
	for _, want := range []string{
		"ok docker:",
		"available",
		"ok sandbox-image:",
		filepath.Join(dir, "affent", "sandbox", "workspace"),
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("doctor output missing %q:\n%s", want, got)
		}
	}
}

func TestDoctorCmdReportsDockerUnavailable(t *testing.T) {
	var stdout, stderr strings.Builder
	code := doctorCmdWithRunner([]string{
		"--workspace", t.TempDir(),
		"--model", "gpt-4o-mini",
		"--api-key", "key",
		"--executor", "docker:affent-sandbox",
	}, errorCommandRunner{err: errors.New("no docker daemon")}, &stdout, &stderr)
	if code != 3 {
		t.Fatalf("exit = %d stderr=%s stdout=%s, want 3", code, stderr.String(), stdout.String())
	}
	got := stdout.String()
	if !strings.Contains(got, "error docker:") || !strings.Contains(got, "no docker daemon") {
		t.Fatalf("doctor output should report docker error:\n%s", got)
	}
}

func TestDoctorCmdChecksDockerContainerRunning(t *testing.T) {
	runner := &dockerInspectRunner{out: "true"}
	var stdout, stderr strings.Builder
	code := doctorCmdWithRunner([]string{
		"--workspace", t.TempDir(),
		"--model", "gpt-4o-mini",
		"--api-key", "key",
		"--executor", "docker:affent-sandbox",
	}, runner, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if len(runner.calls) < 2 || !equalStrings(runner.calls[1].args, []string{"inspect", "-f", "{{.State.Running}}", "affent-sandbox"}) {
		t.Fatalf("container inspect call missing: %+v", runner.calls)
	}
	if !strings.Contains(stdout.String(), "ok docker-container:") {
		t.Fatalf("doctor output missing running container:\n%s", stdout.String())
	}
}

func TestDoctorCmdRejectsInvalidDockerExecutorBeforeDocker(t *testing.T) {
	runner := &fakeCommandRunner{}
	var stdout, stderr strings.Builder
	code := doctorCmdWithRunner([]string{
		"--workspace", t.TempDir(),
		"--model", "gpt-4o-mini",
		"--api-key", "key",
		"--executor", "docker:bad/name",
	}, runner, &stdout, &stderr)
	if code != 3 {
		t.Fatalf("exit = %d stderr=%s stdout=%s, want 3", code, stderr.String(), stdout.String())
	}
	got := stdout.String()
	if !strings.Contains(got, "error executor:") || !strings.Contains(got, "--executor docker may contain only") {
		t.Fatalf("doctor output should report invalid docker executor:\n%s", got)
	}
	if strings.Contains(got, "docker:") {
		t.Fatalf("doctor should not run docker checks for invalid executor:\n%s", got)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("invalid executor must not call docker, calls=%+v", runner.calls)
	}
}

func TestDoctorCmdReportsStoppedDockerContainer(t *testing.T) {
	runner := &dockerInspectRunner{out: "false"}
	var stdout, stderr strings.Builder
	code := doctorCmdWithRunner([]string{
		"--workspace", t.TempDir(),
		"--model", "gpt-4o-mini",
		"--api-key", "key",
		"--executor", "docker:affent-sandbox",
	}, runner, &stdout, &stderr)
	if code != 3 {
		t.Fatalf("exit = %d stderr=%s stdout=%s, want 3", code, stderr.String(), stdout.String())
	}
	if !strings.Contains(stdout.String(), "error docker-container:") || !strings.Contains(stdout.String(), "not running") {
		t.Fatalf("doctor output should report stopped container:\n%s", stdout.String())
	}
}

func TestDoctorCmdRejectsBadMemoryCap(t *testing.T) {
	var stdout, stderr strings.Builder
	code := doctorCmdWithRunner([]string{
		"--workspace", t.TempDir(),
		"--model", "gpt-4o-mini",
		"--api-key", "key",
		"--memory-max-chars", "bad",
	}, &fakeCommandRunner{}, &stdout, &stderr)
	if code != 3 {
		t.Fatalf("exit = %d stderr=%s stdout=%s, want 3", code, stderr.String(), stdout.String())
	}
	if !strings.Contains(stdout.String(), "error memory:") {
		t.Fatalf("doctor output should report memory cap error:\n%s", stdout.String())
	}
}

func TestDoctorCmdChecksSystemPromptTraceAndMCPConfig(t *testing.T) {
	dir := t.TempDir()
	promptPath := filepath.Join(dir, "SYSTEM.md")
	if err := os.WriteFile(promptPath, []byte("custom prompt"), 0o644); err != nil {
		t.Fatal(err)
	}
	server := mcpDoctorHelperServer(t, "maps", []map[string]any{
		{"name": "poi_search", "description": strings.Repeat("search places ", 300), "inputSchema": map[string]any{"type": "object", "properties": map[string]any{}}},
		{"name": "debug", "description": "debug helper", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{}}},
	})
	server["allow_tools"] = []string{"poi_search"}
	mcpPath := writeMCPDoctorConfig(t, dir, []map[string]any{server})
	var stdout, stderr strings.Builder
	code := doctorCmdWithRunner([]string{
		"--workspace", filepath.Join(dir, "ws"),
		"--model", "gpt-4o-mini",
		"--api-key", "key",
		"--system-prompt", "@" + promptPath,
		"--trace", filepath.Join(dir, "traces", "run.jsonl"),
		"--mcp-config", mcpPath,
	}, &fakeCommandRunner{}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	got := stdout.String()
	for _, want := range []string{
		"ok system-prompt:",
		promptPath,
		"ok trace:",
		filepath.Join(dir, "traces", "run.jsonl"),
		"ok mcp:",
		"1 server(s) raw=2 filtered=1 advertised=1",
		"schema=",
		"description=",
		"description_truncated=1",
		"maps namespace=true raw=2 filtered=1",
		"advertised=[maps_poi_search]",
		"description_warnings=[maps_poi_search]",
		"rejected=[debug:not in allow_tools]",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("doctor output missing %q:\n%s", want, got)
		}
	}
}

func TestDoctorCmdReportsBadSystemPromptTraceAndMCPConfig(t *testing.T) {
	dir := t.TempDir()
	traceDir := filepath.Join(dir, "trace-dir")
	if err := os.Mkdir(traceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	mcpPath := filepath.Join(dir, "mcp.json")
	if err := os.WriteFile(mcpPath, []byte(`{"servers":[{"name":"bad","command":"affent-does-not-exist"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr strings.Builder
	code := doctorCmdWithRunner([]string{
		"--workspace", filepath.Join(dir, "ws"),
		"--model", "gpt-4o-mini",
		"--api-key", "key",
		"--system-prompt", "@" + filepath.Join(dir, "missing.md"),
		"--trace", traceDir,
		"--mcp-config", mcpPath,
	}, &fakeCommandRunner{}, &stdout, &stderr)
	if code != 3 {
		t.Fatalf("exit = %d stderr=%s stdout=%s, want 3", code, stderr.String(), stdout.String())
	}
	got := stdout.String()
	for _, want := range []string{
		"error system-prompt:",
		"missing.md",
		"error trace:",
		"is a directory",
		"error mcp:",
		"command \"affent-does-not-exist\" not found",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("doctor output missing %q:\n%s", want, got)
		}
	}
}

func TestDoctorSystemPromptRejectsOversizeFile(t *testing.T) {
	dir := t.TempDir()
	promptPath := filepath.Join(dir, "huge.md")
	if err := os.WriteFile(promptPath, []byte(strings.Repeat("x", maxPromptInputBytes+1)), 0o644); err != nil {
		t.Fatal(err)
	}
	status, msg := doctorSystemPrompt("@" + promptPath)
	if status != "error" {
		t.Fatalf("status = %q msg=%q, want error", status, msg)
	}
	if !strings.Contains(msg, "prompt input exceeds") {
		t.Fatalf("message = %q, want prompt input exceeds", msg)
	}
}

func TestValidateMCPServerSpecRejectsInvalidStaticConfig(t *testing.T) {
	for _, c := range []struct {
		name string
		raw  string
		want string
	}{
		{name: "missing transport", raw: `{"servers":[{"name":"x"}]}`, want: "url or command is required"},
		{name: "both transports", raw: `{"servers":[{"name":"x","url":"http://127.0.0.1/mcp","command":"sh"}]}`, want: "either url or command"},
		{name: "missing name", raw: `{"servers":[{"command":"sh"}]}`, want: "name is required"},
		{name: "bad url", raw: `{"servers":[{"name":"x","url":"::://bad"}]}`, want: "invalid url"},
		{name: "bad env", raw: `{"servers":[{"name":"x","command":"sh","env":["=bad"]}]}`, want: "invalid env"},
		{name: "bad init timeout", raw: `{"servers":[{"name":"x","command":"sh","init_timeout":"0s"}]}`, want: "init_timeout"},
		{name: "blank allow tool", raw: `{"servers":[{"name":"x","command":"sh","allow_tools":[" "]}]}`, want: "allow_tools values must not be empty"},
		{name: "duplicate deny tool", raw: `{"servers":[{"name":"x","command":"sh","deny_tools":["search","search"]}]}`, want: "deny_tools contains duplicate"},
		{name: "overlapping allow deny", raw: `{"servers":[{"name":"x","command":"sh","allow_tools":["search"],"deny_tools":["search"]}]}`, want: "appears in both allow_tools and deny_tools"},
		{name: "unknown server field", raw: `{"servers":[{"name":"x","command":"sh","unused":true}]}`, want: "unknown field \"unused\""},
	} {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "mcp.json")
			if err := os.WriteFile(path, []byte(c.raw), 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := doctorMCPConfig(path)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("error = %v, want contains %q", err, c.want)
			}
		})
	}
}

func TestDoctorMCPConfigReportsEmptyLiveToolSet(t *testing.T) {
	dir := t.TempDir()
	mcpPath := writeMCPDoctorConfig(t, dir, []map[string]any{
		mcpDoctorHelperServer(t, "empty", nil),
	})
	_, err := doctorMCPConfig(mcpPath)
	if err == nil || !strings.Contains(err.Error(), "exposes no usable tools") {
		t.Fatalf("error = %v, want empty tool set rejection", err)
	}
}

func TestDoctorMCPConfigReportsAdvertisedToolCollision(t *testing.T) {
	dir := t.TempDir()
	first := mcpDoctorHelperServer(t, "one", []map[string]any{
		{"name": "search", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{}}},
	})
	first["namespace"] = false
	second := mcpDoctorHelperServer(t, "two", []map[string]any{
		{"name": "search", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{}}},
	})
	second["namespace"] = false
	mcpPath := writeMCPDoctorConfig(t, dir, []map[string]any{first, second})
	_, err := doctorMCPConfig(mcpPath)
	if err == nil || !strings.Contains(err.Error(), "tool name collision") {
		t.Fatalf("error = %v, want tool name collision", err)
	}
}

func TestEnsureWritableDirCreatesAndCleansProbeFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "new", "workspace")
	if err := ensureWritableDir(dir); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("probe file should be cleaned up, entries=%v", entries)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
