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
)

type Summary struct {
	Label          string `json:"label"`
	TotalSteps     int    `json:"total_steps"`
	CompletedSteps int    `json:"completed_steps"`
	Active         bool   `json:"active"`
	Blocked        bool   `json:"blocked"`
	Error          bool   `json:"error"`
}

type summaryState struct {
	Steps []struct {
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
	for _, step := range st.Steps {
		switch strings.TrimSpace(step.Status) {
		case "completed":
			out.CompletedSteps++
		case "in_progress":
			out.Active = true
		case "blocked":
			out.Blocked = true
		}
	}
	out.Label = fmt.Sprintf("plan:%d/%d", out.CompletedSteps, out.TotalSteps)
	if out.Active {
		out.Label += ":active"
	}
	if out.Blocked {
		out.Label += ":blocked"
	}
	return out, nil
}

func ErrorSummary() Summary {
	return Summary{Label: LabelError, Error: true}
}
