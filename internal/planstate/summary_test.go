package planstate

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSummarizeJSONLabelsPlanProgress(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
		want Summary
	}{
		{
			name: "active progress",
			raw:  `{"steps":[{"text":"done","status":"completed"},{"text":"resume work","status":"in_progress"},{"text":"later","status":"pending"}]}`,
			want: Summary{Label: "plan:1/3:active", TotalSteps: 3, CompletedSteps: 1, Active: true, CurrentStep: "resume work"},
		},
		{
			name: "blocked progress",
			raw:  `{"steps":[{"text":"done","status":"completed"},{"text":"need input","status":"blocked"}]}`,
			want: Summary{Label: "plan:1/2:blocked", TotalSteps: 2, CompletedSteps: 1, Blocked: true, CurrentStep: "need input"},
		},
		{
			name: "blank status counts as pending",
			raw:  `{"steps":[{"text":"  next\nstep  ","status":"  "}]}`,
			want: Summary{Label: "plan:0/1", TotalSteps: 1, CurrentStep: "next step"},
		},
		{
			name: "empty plan",
			raw:  `{"steps":[]}`,
			want: Summary{Label: LabelEmpty},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := SummarizeJSON(json.RawMessage(tc.raw))
			if err != nil {
				t.Fatalf("SummarizeJSON: %v", err)
			}
			if got != tc.want {
				t.Fatalf("summary = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestSummarizeJSONCurrentStepPriority(t *testing.T) {
	got, err := SummarizeJSON(json.RawMessage(`{"steps":[{"text":"pending first","status":"pending"},{"text":"blocked second","status":"blocked"},{"text":"active third","status":"in_progress"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if got.CurrentStep != "active third" {
		t.Fatalf("current step = %q, want active third", got.CurrentStep)
	}
}

func TestSummarizeJSONCurrentStepIsBounded(t *testing.T) {
	got, err := SummarizeJSON(json.RawMessage(`{"steps":[{"text":"` + strings.Repeat("a", maxCurrentStepBytes+10) + `","status":"in_progress"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(got.CurrentStep) > maxCurrentStepBytes {
		t.Fatalf("current step len = %d, want <= %d", len(got.CurrentStep), maxCurrentStepBytes)
	}
}

func TestSummarizeJSONRejectsInvalidJSON(t *testing.T) {
	if _, err := SummarizeJSON(json.RawMessage(`{`)); err == nil {
		t.Fatal("SummarizeJSON should reject invalid JSON")
	}
}

func TestErrorSummary(t *testing.T) {
	got := ErrorSummary()
	if got.Label != LabelError || !got.Error {
		t.Fatalf("ErrorSummary = %+v", got)
	}
}
