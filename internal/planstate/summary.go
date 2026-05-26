package planstate

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/affinefoundation/affent/internal/textutil"
)

const (
	LabelMissing = "-"
	LabelEmpty   = "plan:empty"
	LabelError   = "plan:error"

	maxCurrentStepBytes = 240
)

type Summary struct {
	Label                  string `json:"label"`
	TotalSteps             int    `json:"total_steps"`
	CompletedSteps         int    `json:"completed_steps"`
	Active                 bool   `json:"active"`
	Blocked                bool   `json:"blocked"`
	Done                   bool   `json:"done"`
	CurrentStep            string `json:"current_step,omitempty"`
	CurrentStepIndex       int    `json:"current_step_index,omitempty"`
	CurrentStepStatus      string `json:"current_step_status,omitempty"`
	LastCompletedStep      string `json:"last_completed_step,omitempty"`
	LastCompletedStepIndex int    `json:"last_completed_step_index,omitempty"`
	BlockedStep            string `json:"blocked_step,omitempty"`
	BlockedStepIndex       int    `json:"blocked_step_index,omitempty"`
	Error                  bool   `json:"error"`
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
	out := Summary{}
	seenSteps := map[string]bool{}
	currentPriority := 0
	for _, step := range st.Steps {
		status := normalizeStatus(step.Status)
		stepKey := canonicalStepKey(step.Text, status)
		if seenSteps[stepKey] {
			continue
		}
		seenSteps[stepKey] = true
		out.TotalSteps++
		switch status {
		case "completed":
			out.CompletedSteps++
			out.LastCompletedStepIndex = out.TotalSteps
			out.LastCompletedStep = compactCurrentStep(step.Text)
		case "in_progress":
			out.Active = true
		case "blocked":
			out.Blocked = true
			if out.BlockedStepIndex == 0 {
				out.BlockedStepIndex = out.TotalSteps
				out.BlockedStep = compactCurrentStep(step.Text)
			}
		}
		if priority := currentStepPriority(status); priority > currentPriority {
			currentPriority = priority
			out.CurrentStepIndex = out.TotalSteps
			out.CurrentStep = compactCurrentStep(step.Text)
			out.CurrentStepStatus = status
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

func normalizeStatus(status string) string {
	status = strings.ToLower(strings.TrimSpace(status))
	if status == "" {
		return "pending"
	}
	return status
}

func canonicalStepKey(text, status string) string {
	return strings.ToLower(textutil.CompactWhitespace(text)) + "\x00" + status
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
	text = textutil.CompactWhitespace(text)
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
