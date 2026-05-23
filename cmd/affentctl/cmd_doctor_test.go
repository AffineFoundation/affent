package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/affinefoundation/affent/internal/agent"
)

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
	mcpPath := filepath.Join(dir, "mcp.json")
	if err := os.WriteFile(mcpPath, []byte(`{"servers":[{"name":"shell","command":"sh","args":["-c","cat"]}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
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
		"1 server(s)",
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
