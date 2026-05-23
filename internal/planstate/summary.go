package planstate

import (
	"encoding/json"
	"fmt"
	"strings"
)

const (
	LabelMissing = "-"
	LabelEmpty   = "plan:empty"
	LabelError   = "plan:error"

	maxCurrentStepBytes = 240
)

type Summary struct {
	Label            string `json:"label"`
	TotalSteps       int    `json:"total_steps"`
	CompletedSteps   int    `json:"completed_steps"`
	Active           bool   `json:"active"`
	Blocked          bool   `json:"blocked"`
	Done             bool   `json:"done"`
	CurrentStep      string `json:"current_step,omitempty"`
	CurrentStepIndex int    `json:"current_step_index,omitempty"`
	Error            bool   `json:"error"`
}

type summaryState struct {
	Steps []struct {
		Text   string `json:"text"`
		Status string `json:"status"`
	} `json:"steps"`
}

func SummarizeJSON(raw json.RawMessage) (Summary, error) {
	var st summaryState
	if err := json.Unmarshal(raw, &st); err != nil {
		return Summary{}, err
	}
	if len(st.Steps) == 0 {
		return Summary{Label: LabelEmpty}, nil
	}
	out := Summary{TotalSteps: len(st.Steps)}
	currentPriority := 0
	for i, step := range st.Steps {
		status := strings.TrimSpace(step.Status)
		switch status {
		case "completed":
			out.CompletedSteps++
		case "in_progress":
			out.Active = true
		case "blocked":
			out.Blocked = true
		}
		if priority := currentStepPriority(status); priority > currentPriority {
			currentPriority = priority
			out.CurrentStepIndex = i + 1
			out.CurrentStep = compactCurrentStep(step.Text)
		}
	}
	out.Label = fmt.Sprintf("plan:%d/%d", out.CompletedSteps, out.TotalSteps)
	if out.CompletedSteps == out.TotalSteps {
		out.Done = true
		out.Label += ":done"
		return out, nil
	}
	if out.Active {
		out.Label += ":active"
	}
	if out.Blocked {
		out.Label += ":blocked"
	}
	return out, nil
}

func currentStepPriority(status string) int {
	switch status {
	case "in_progress":
		return 3
	case "blocked":
		return 2
	case "completed":
		return 0
	default:
		return 1
	}
}

func compactCurrentStep(text string) string {
	text = strings.TrimSpace(strings.ReplaceAll(text, "\n", " "))
	if len(text) <= maxCurrentStepBytes {
		return text
	}
	end := 0
	for i := range text {
		if i > maxCurrentStepBytes {
			break
		}
		end = i
	}
	return strings.TrimSpace(text[:end])
}

func ErrorSummary() Summary {
	return Summary{Label: LabelError, Error: true}
}
