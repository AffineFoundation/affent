package planstate

import (
	"encoding/json"
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
			raw:  `{"steps":[{"status":"completed"},{"status":"in_progress"},{"status":"pending"}]}`,
			want: Summary{Label: "plan:1/3:active", TotalSteps: 3, CompletedSteps: 1, Active: true},
		},
		{
			name: "blocked progress",
			raw:  `{"steps":[{"status":"completed"},{"status":"blocked"}]}`,
			want: Summary{Label: "plan:1/2:blocked", TotalSteps: 2, CompletedSteps: 1, Blocked: true},
		},
		{
			name: "blank status counts as pending",
			raw:  `{"steps":[{"status":"  "}]}`,
			want: Summary{Label: "plan:0/1", TotalSteps: 1},
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
