package main

import (
	"bytes"
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

	help, code := captureStderr(t, func() int {
		return run([]string{"--help"})
	})
	if code != 0 {
		t.Fatalf("run --help exit = %d", code)
	}
	for _, secret := range []string{"https://sentinel-base.example", "sk-sentinel-secret", "sentinel-model"} {
		if strings.Contains(help, secret) {
			t.Fatalf("--help leaked env value %q:\n%s", secret, help)
		}
	}
	for _, want := range []string{"AFFENTCTL_BASE_URL", "AFFENTCTL_API_KEY", "AFFENTCTL_MODEL"} {
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
		BatchScenario: "sample",
		Workspace:     "/tmp/ws",
		TracePath:     "/tmp/ws/trace.jsonl",
		OK:            true,
		Duration:      1234 * time.Millisecond,
		TurnEndReason: "completed",
		ToolCalls:     3,
		ToolStats: agenteval.ToolRuntimeStats{
			ToolArgsRepaired: 1,
			ToolErrors:       2,
			ToolDurationMS:   45,
		},
		Usage: agenteval.Usage{InputTokens: 100, OutputTokens: 25},
	})
	got := out.String()
	for _, want := range []string{
		"PASS sample (1.234s)",
		"workspace: /tmp/ws",
		"trace: /tmp/ws/trace.jsonl",
		"metrics: tools=3 errors=2 repaired=1 tool_ms=45 tokens=100/25 end=completed",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("output missing %q:\n%s", want, got)
		}
	}
}

func TestBatchSummaryAggregatesRuntimeMetrics(t *testing.T) {
	var summary batchSummary
	summary.add(agenteval.BatchResult{
		OK:        true,
		Duration:  100 * time.Millisecond,
		ToolCalls: 2,
		ToolStats: agenteval.ToolRuntimeStats{
			ToolArgsRepaired: 1,
			ToolErrors:       0,
			ToolDurationMS:   10,
		},
		Usage: agenteval.Usage{InputTokens: 20, OutputTokens: 5},
	})
	summary.add(agenteval.BatchResult{
		OK:        false,
		Duration:  250 * time.Millisecond,
		ToolCalls: 3,
		ToolStats: agenteval.ToolRuntimeStats{
			ToolArgsRepaired: 2,
			ToolErrors:       1,
			ToolDurationMS:   40,
		},
		Usage: agenteval.Usage{InputTokens: 70, OutputTokens: 15},
	})

	var out bytes.Buffer
	printBatchSummary(&out, summary)
	want := "SUMMARY scenarios=2 passed=1 failed=1 duration=350ms tools=5 errors=1 repaired=3 tool_ms=50 tokens=90/20"
	if !strings.Contains(out.String(), want) {
		t.Fatalf("summary output missing %q:\n%s", want, out.String())
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
