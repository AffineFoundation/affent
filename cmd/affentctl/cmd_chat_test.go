package main

import (
	"strings"
	"testing"

	"github.com/affinefoundation/affent/internal/sse"
)

func TestToolResultStatusLabel(t *testing.T) {
	cases := []struct {
		name string
		in   sse.ToolResultPayload
		want string
	}{
		{
			name: "success",
			in:   sse.ToolResultPayload{},
			want: "ok",
		},
		{
			name: "error with failure kind",
			in:   sse.ToolResultPayload{ExitCode: 1, FailureKind: "blocked"},
			want: "exit 1, failure=blocked",
		},
		{
			name: "zero exit no evidence",
			in:   sse.ToolResultPayload{FailureKinds: []string{"dynamic_shell"}},
			want: "no evidence, failure=dynamic_shell",
		},
		{
			name: "combined failure kinds",
			in:   sse.ToolResultPayload{ExitCode: 1, FailureKinds: []string{"blocked", "loop_guard_repeated_failed_input"}},
			want: "exit 1, failure=blocked+loop_guard_repeated_failed_input",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := toolResultStatusLabel(c.in); got != c.want {
				t.Fatalf("toolResultStatusLabel() = %q, want %q", got, c.want)
			}
		})
	}
}

func TestToolResultFailureDetails(t *testing.T) {
	p := sse.ToolResultPayload{
		FailureKinds: []string{"dynamic_shell"},
		ResultSummary: "[dynamic page shell: URL=https://dashboard.example/helio, Content-Type=\"text/html\", Reason=\"low evidence app shell\"]\n" +
			"Failure: kind=dynamic_shell\n" +
			"Next: do not treat this loading/app shell as source evidence; use a canonical API/text/source page.",
	}
	got := toolResultFailureDetails(p)
	if len(got) != 2 {
		t.Fatalf("details = %#v, want reason and Next line", got)
	}
	if !strings.Contains(got[0], "dynamic page shell") || !strings.Contains(got[0], "dashboard.example") {
		t.Fatalf("reason detail lost source context: %#v", got)
	}
	if !strings.HasPrefix(got[1], "Next:") || !strings.Contains(got[1], "loading/app shell") {
		t.Fatalf("Next guidance missing: %#v", got)
	}
}

func TestToolResultFailureDetailsSkipsSuccessfulResults(t *testing.T) {
	got := toolResultFailureDetails(sse.ToolResultPayload{ResultSummary: "normal output"})
	if len(got) != 0 {
		t.Fatalf("successful result should not print details: %#v", got)
	}
}
