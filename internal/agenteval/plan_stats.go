package agenteval

import (
	"encoding/json"
	"strings"

	"github.com/affinefoundation/affent/internal/agent"
	"github.com/affinefoundation/affent/internal/planstate"
)

// PlanStats summarizes persisted-plan tool usage observed in a Trace.
// It is intentionally derived from the existing tool.request surface so
// evals can track plan-mode behavior without adding runtime state.
type PlanStats struct {
	Calls             int
	ByAction          map[string]int
	Errors            int
	TotalSteps        int
	CompletedSteps    int
	CurrentStepIndex  int
	CurrentStepStatus string
	CurrentStep       string
}

func (s PlanStats) HasAny() bool {
	return s.Calls > 0
}

// PlanStats walks the trace tool timeline and aggregates plan calls by
// action (view/set/update/clear). Calls whose action is missing or not a
// string are grouped under "unknown" so malformed model behavior remains
// visible instead of disappearing from eval output.
func (t Trace) PlanStats() PlanStats {
	var s PlanStats
	for _, c := range t.Tools {
		if c.Tool != agent.PlanToolName {
			continue
		}
		s.Calls++
		if c.IsErr || c.ExitCode != 0 {
			s.Errors++
		}
		action := planActionFromArgs(c.Args)
		if s.ByAction == nil {
			s.ByAction = map[string]int{}
		}
		s.ByAction[action]++
		if c.IsErr || c.ExitCode != 0 || strings.TrimSpace(c.Result) == "" {
			continue
		}
		if summary, err := planstate.SummarizeJSON(json.RawMessage(c.Result)); err == nil {
			s.TotalSteps = summary.TotalSteps
			s.CompletedSteps = summary.CompletedSteps
			s.CurrentStepIndex = summary.CurrentStepIndex
			s.CurrentStepStatus = summary.CurrentStepStatus
			s.CurrentStep = compactOneLine(summary.CurrentStep, 220)
		}
	}
	return s
}

func planActionFromArgs(args map[string]any) string {
	action, _ := args["action"].(string)
	action = strings.ToLower(strings.TrimSpace(action))
	if action == "" {
		return "unknown"
	}
	return action
}

type PlanExample struct {
	Scenario          string   `json:"scenario,omitempty"`
	ToolIndex         int      `json:"tool_index"`
	CallID            string   `json:"call_id,omitempty"`
	Action            string   `json:"action"`
	Index             int      `json:"index,omitempty"`
	Status            string   `json:"status,omitempty"`
	StepText          string   `json:"step_text,omitempty"`
	Evidence          []string `json:"evidence,omitempty"`
	NotePreview       string   `json:"note_preview,omitempty"`
	ResultMessage     string   `json:"result_message,omitempty"`
	TotalSteps        int      `json:"total_steps,omitempty"`
	CompletedSteps    int      `json:"completed_steps,omitempty"`
	CurrentStepIndex  int      `json:"current_step_index,omitempty"`
	CurrentStepStatus string   `json:"current_step_status,omitempty"`
	CurrentStep       string   `json:"current_step,omitempty"`
	Error             bool     `json:"error,omitempty"`
	ResultSummary     string   `json:"result_summary,omitempty"`
}

// PlanExamples returns bounded, trace-derived plan samples for debug manifests
// and JSONL summaries. It intentionally uses only existing tool args/results so
// old traces remain readable and runtime plan state does not gain a second
// metadata channel.
func (t Trace) PlanExamples(maxExamples int) []PlanExample {
	if maxExamples <= 0 {
		return nil
	}
	var out []PlanExample
	for i, c := range t.Tools {
		if len(out) >= maxExamples {
			break
		}
		if c.Tool != agent.PlanToolName {
			continue
		}
		out = append(out, planExampleForTool(i+1, c))
	}
	return out
}

func planExampleForTool(index int, c ToolCall) PlanExample {
	ex := PlanExample{
		ToolIndex: index,
		CallID:    c.CallID,
		Action:    planActionFromArgs(c.Args),
		Index:     intArg(c.Args, "index"),
		Status:    stringArg(c.Args, "status"),
		StepText:  compactOneLine(stringArg(c.Args, "text"), 220),
		Evidence:  compactStringSlice(stringSliceArg(c.Args, "evidence"), 6, 160),
		Error:     c.IsErr || c.ExitCode != 0,
	}
	ex.NotePreview = compactOneLine(stringArg(c.Args, "note"), 220)
	if ex.Action == "set" && ex.StepText == "" {
		ex.StepText = compactOneLine(firstPlanStepTextArg(c.Args), 220)
	}
	if ex.Error {
		ex.ResultSummary = compactOneLine(c.Result, 240)
		return ex
	}
	var st evalPlanState
	if err := json.Unmarshal([]byte(c.Result), &st); err != nil {
		return ex
	}
	ex.ResultMessage = compactOneLine(st.Message, 220)
	if summary, err := planstate.SummarizeJSON(json.RawMessage(c.Result)); err == nil {
		ex.TotalSteps = summary.TotalSteps
		ex.CompletedSteps = summary.CompletedSteps
		ex.CurrentStepIndex = summary.CurrentStepIndex
		ex.CurrentStepStatus = summary.CurrentStepStatus
		ex.CurrentStep = compactOneLine(summary.CurrentStep, 220)
	}
	if ex.StepText == "" || ex.Status == "" || len(ex.Evidence) == 0 || ex.NotePreview == "" {
		if step, ok := planExampleResultStep(st, ex.Action, ex.Index); ok {
			if ex.StepText == "" {
				ex.StepText = compactOneLine(step.Text, 220)
			}
			if ex.Status == "" {
				ex.Status = strings.ToLower(strings.TrimSpace(step.Status))
			}
			if len(ex.Evidence) == 0 {
				ex.Evidence = compactStringSlice(step.Evidence, 6, 160)
			}
			if ex.NotePreview == "" {
				ex.NotePreview = compactOneLine(step.Note, 220)
			}
		}
	}
	return ex
}

type evalPlanState struct {
	Message string         `json:"message"`
	Steps   []evalPlanStep `json:"steps"`
}

type evalPlanStep struct {
	Text     string   `json:"text"`
	Status   string   `json:"status"`
	Evidence []string `json:"evidence"`
	Note     string   `json:"note"`
}

func planExampleResultStep(st evalPlanState, action string, index int) (evalPlanStep, bool) {
	if len(st.Steps) == 0 {
		return evalPlanStep{}, false
	}
	if index > 0 && index <= len(st.Steps) {
		return st.Steps[index-1], true
	}
	switch action {
	case "set", "view":
		for _, step := range st.Steps {
			if strings.EqualFold(strings.TrimSpace(step.Status), "in_progress") {
				return step, true
			}
		}
		return st.Steps[0], true
	default:
		return evalPlanStep{}, false
	}
}

func stringArg(args map[string]any, key string) string {
	v, _ := args[key].(string)
	return strings.TrimSpace(v)
}

func intArg(args map[string]any, key string) int {
	switch v := args[key].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	default:
		return 0
	}
}

func stringSliceArg(args map[string]any, key string) []string {
	raw, ok := args[key].([]any)
	if !ok {
		if values, ok := args[key].([]string); ok {
			return values
		}
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func firstPlanStepTextArg(args map[string]any) string {
	switch raw := args["steps"].(type) {
	case []any:
		if len(raw) == 0 {
			return ""
		}
		step, ok := raw[0].(map[string]any)
		if !ok {
			return ""
		}
		return stringArg(step, "text")
	case []map[string]any:
		if len(raw) == 0 {
			return ""
		}
		return stringArg(raw[0], "text")
	default:
		return ""
	}
}

func compactStringSlice(in []string, maxItems, maxBytes int) []string {
	if maxItems <= 0 || len(in) == 0 {
		return nil
	}
	out := make([]string, 0, len(in))
	for _, value := range in {
		if len(out) >= maxItems {
			break
		}
		value = compactOneLine(value, maxBytes)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}
