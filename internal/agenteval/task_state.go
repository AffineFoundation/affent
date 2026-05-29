package agenteval

import (
	"strings"

	"github.com/affinefoundation/affent/internal/taskstate"
)

const (
	taskStateMaxItems       = 8
	taskStateSummaryMaxRune = 180
)

type TaskStateSnapshot struct {
	Objective         string              `json:"objective,omitempty"`
	Status            string              `json:"status,omitempty"`
	CurrentStep       string              `json:"current_step,omitempty"`
	RequestMode       string              `json:"request_mode,omitempty"`
	RequestSource     string              `json:"request_source,omitempty"`
	ScheduleID        string              `json:"schedule_id,omitempty"`
	ScheduleKind      string              `json:"schedule_kind,omitempty"`
	NextStep          string              `json:"next_step,omitempty"`
	VerificationState string              `json:"verification_state,omitempty"`
	ChangedFiles      []TaskStateFile     `json:"changed_files,omitempty"`
	AttemptedActions  []TaskStateAction   `json:"attempted_actions,omitempty"`
	FailedActions     []TaskStateFailure  `json:"failed_actions,omitempty"`
	Evidence          []TaskStateEvidence `json:"evidence,omitempty"`
	Sources           []string            `json:"sources,omitempty"`
}

type TaskStateFile struct {
	Path   string `json:"path"`
	Action string `json:"action,omitempty"`
}

type TaskStateAction struct {
	Tool    string `json:"tool"`
	Summary string `json:"summary,omitempty"`
	TurnID  string `json:"turn_id,omitempty"`
	CallID  string `json:"call_id,omitempty"`
}

type TaskStateFailure struct {
	Tool    string   `json:"tool"`
	Summary string   `json:"summary,omitempty"`
	Kinds   []string `json:"kinds,omitempty"`
	TurnID  string   `json:"turn_id,omitempty"`
	CallID  string   `json:"call_id,omitempty"`
}

type TaskStateEvidence struct {
	Source  string `json:"source"`
	Summary string `json:"summary,omitempty"`
	TurnID  string `json:"turn_id,omitempty"`
	CallID  string `json:"call_id,omitempty"`
}

func DeriveTaskState(trace Trace) TaskStateSnapshot {
	task := TaskStateSnapshot{
		Objective:         traceTaskObjective(trace),
		Status:            traceTaskStatus(trace),
		CurrentStep:       traceTaskCurrentStep(trace),
		VerificationState: traceTaskVerificationState(trace),
	}
	if latest := latestTaskRequest(trace); latest != nil {
		task.RequestMode = normalizeTaskRequestMode(latest.Mode)
		task.RequestSource = normalizeTaskRequestSource(latest.Source)
		task.ScheduleID = strings.TrimSpace(latest.ScheduleID)
		task.ScheduleKind = strings.TrimSpace(latest.ScheduleKind)
		task.Sources = appendUniqueTaskString(task.Sources, task.RequestSource, taskStateMaxItems)
	}
	for _, injection := range trace.ContextInjections {
		task.Sources = appendUniqueTaskString(task.Sources, injection.Source, taskStateMaxItems)
		switch injection.Source {
		case "runtime_workspace", "active_plan", "skill":
			task.Evidence = appendTaskEvidence(task.Evidence, TaskStateEvidence{
				Source:  injection.Source,
				Summary: compactTaskStateSummary(firstNonEmpty(injection.Summary, injection.Title, injection.Preview)),
				TurnID:  injection.TurnID,
			})
		}
	}
	if len(trace.RuntimeSurfaces) > 0 {
		task.Sources = appendUniqueTaskString(task.Sources, "runtime_surface", taskStateMaxItems)
	}
	for _, tool := range trace.Tools {
		task.AttemptedActions = appendTaskAction(task.AttemptedActions, TaskStateAction{
			Tool:    tool.Tool,
			Summary: compactTaskStateSummary(taskStateToolSummary(tool)),
			TurnID:  tool.TurnID,
			CallID:  tool.CallID,
		})
		if file := taskStateChangedFile(tool); file.Path != "" {
			task.ChangedFiles = appendTaskFile(task.ChangedFiles, file)
		}
		if taskStateToolFailed(tool) {
			task.FailedActions = appendTaskFailure(task.FailedActions, TaskStateFailure{
				Tool:    firstNonEmpty(tool.Tool, "tool"),
				Summary: compactTaskStateSummary(firstNonEmpty(tool.ResultSummary, tool.Result, taskStateToolSummary(tool))),
				Kinds:   append([]string(nil), tool.FailureKinds...),
				TurnID:  tool.TurnID,
				CallID:  tool.CallID,
			})
			continue
		}
		if source := taskStateToolEvidenceSource(tool); source != "" {
			summary := compactTaskStateSummary(taskStateToolSummary(tool))
			task.Evidence = appendTaskEvidence(task.Evidence, TaskStateEvidence{
				Source:  source,
				Summary: summary,
				TurnID:  tool.TurnID,
				CallID:  tool.CallID,
			})
			task.Sources = appendUniqueTaskString(task.Sources, source, taskStateMaxItems)
			if source := taskstate.ShellHandoffEvidenceSource(taskStateToolSummary(tool)); source != "" {
				task.Evidence = appendTaskEvidence(task.Evidence, TaskStateEvidence{
					Source:  source,
					Summary: summary,
					TurnID:  tool.TurnID,
					CallID:  tool.CallID,
				})
				task.Sources = appendUniqueTaskString(task.Sources, source, taskStateMaxItems)
			}
		}
	}
	task.NextStep = traceTaskNextStep(trace, task)
	if task.VerificationState == "" {
		task.VerificationState = "unknown"
	}
	if taskStateEmpty(task) {
		return TaskStateSnapshot{}
	}
	return task
}

func latestTaskRequest(trace Trace) *UserMessage {
	for i := len(trace.UserMessages) - 1; i >= 0; i-- {
		msg := trace.UserMessages[i]
		if strings.TrimSpace(msg.Text) != "" ||
			strings.TrimSpace(msg.DisplayText) != "" ||
			strings.TrimSpace(msg.Mode) != "" ||
			strings.TrimSpace(msg.Source) != "" ||
			strings.TrimSpace(msg.ScheduleID) != "" ||
			strings.TrimSpace(msg.ScheduleKind) != "" {
			return &trace.UserMessages[i]
		}
	}
	return nil
}

func CloneTaskStateSnapshotPtr(in TaskStateSnapshot) *TaskStateSnapshot {
	if taskStateEmpty(in) {
		return nil
	}
	out := in
	out.ChangedFiles = append([]TaskStateFile(nil), in.ChangedFiles...)
	out.AttemptedActions = append([]TaskStateAction(nil), in.AttemptedActions...)
	out.FailedActions = append([]TaskStateFailure(nil), in.FailedActions...)
	for i := range out.FailedActions {
		out.FailedActions[i].Kinds = append([]string(nil), in.FailedActions[i].Kinds...)
	}
	out.Evidence = append([]TaskStateEvidence(nil), in.Evidence...)
	out.Sources = append([]string(nil), in.Sources...)
	return &out
}

func traceTaskObjective(trace Trace) string {
	for _, msg := range trace.UserMessages {
		if text := firstNonEmpty(msg.DisplayText, msg.Text); text != "" {
			return compactTaskStateSummary(text)
		}
	}
	return compactTaskStateSummary(trace.Prompt)
}

func traceTaskStatus(trace Trace) string {
	switch strings.TrimSpace(trace.TurnEndReason) {
	case "completed":
		return "completed"
	case "cancelled":
		return "cancelled"
	case "max_turns":
		return "blocked"
	case "error":
		return "failed"
	default:
		return "unknown"
	}
}

func traceTaskCurrentStep(trace Trace) string {
	for i := len(trace.LoopProtocolFeeds) - 1; i >= 0; i-- {
		if step := strings.TrimSpace(trace.LoopProtocolFeeds[i].PlanCurrentStep); step != "" {
			return compactTaskStateSummary(step)
		}
	}
	planExamples := trace.PlanExamples(maxDebugPlanExamples)
	for i := len(planExamples) - 1; i >= 0; i-- {
		example := planExamples[i]
		if step := strings.TrimSpace(example.CurrentStep); step != "" {
			return compactTaskStateSummary(step)
		}
	}
	return ""
}

func traceTaskVerificationState(trace Trace) string {
	state := ""
	for _, tool := range trace.Tools {
		if taskStateToolFailed(tool) {
			state = "failed"
		}
		if tool.Tool == "shell" && tool.ExitCode == 0 {
			state = "last_shell_passed"
		}
	}
	return state
}

func traceTaskNextStep(trace Trace, task TaskStateSnapshot) string {
	for i := len(trace.MessageRejections) - 1; i >= 0; i-- {
		if action := strings.TrimSpace(trace.MessageRejections[i].RequiredAction); action != "" {
			return compactTaskStateSummary(action)
		}
	}
	for i := len(trace.LoopDecisions) - 1; i >= 0; i-- {
		decision := trace.LoopDecisions[i]
		if action := strings.TrimSpace(decision.RequiredAction); action != "" {
			return compactTaskStateSummary(action)
		}
	}
	if len(task.FailedActions) > 0 && task.VerificationState != "last_shell_passed" && task.Status != "completed" {
		return "latest failed action is unresolved"
	}
	return ""
}

func taskStateChangedFile(tool ToolCall) TaskStateFile {
	switch tool.Tool {
	case "write_file":
		return TaskStateFile{Path: stringArg(tool.Args, "path"), Action: "write"}
	case "edit_file":
		return TaskStateFile{Path: stringArg(tool.Args, "path"), Action: "edit"}
	default:
		return TaskStateFile{}
	}
}

func taskStateToolFailed(tool ToolCall) bool {
	return tool.ExitCode != 0 || len(tool.FailureKinds) > 0 || tool.FailureKind != ""
}

func taskStateToolSummary(tool ToolCall) string {
	switch tool.Tool {
	case "shell":
		return stringArg(tool.Args, "command")
	case "read_file", "write_file", "edit_file", "list_files", "file_context", "symbol_context", "repo_search":
		return stringArg(tool.Args, "path")
	case "plan", "memory", "skill", "loop_protocol", "session_schedule":
		return stringArg(tool.Args, "action")
	case "run_task":
		return stringArg(tool.Args, "task_type")
	case "subagent_run":
		return stringArg(tool.Args, "mode")
	default:
		for _, key := range []string{"path", "query", "url", "action", "task", "objective"} {
			if value := stringArg(tool.Args, key); value != "" {
				return key + ": " + value
			}
		}
	}
	return ""
}

func taskStateToolEvidenceSource(tool ToolCall) string {
	switch tool.Tool {
	case "shell", "plan", "memory", "loop_protocol", "session_schedule":
		return tool.Tool
	default:
		return ""
	}
}

func appendTaskAction(items []TaskStateAction, item TaskStateAction) []TaskStateAction {
	item.Tool = strings.TrimSpace(item.Tool)
	if item.Tool == "" {
		return items
	}
	item.Summary = strings.TrimSpace(item.Summary)
	if len(items) >= taskStateMaxItems {
		items = items[1:]
	}
	return append(items, item)
}

func appendTaskFile(items []TaskStateFile, item TaskStateFile) []TaskStateFile {
	item.Path = strings.TrimSpace(item.Path)
	if item.Path == "" {
		return items
	}
	for i := range items {
		if items[i].Path == item.Path {
			if item.Action != "" {
				items[i].Action = item.Action
			}
			return items
		}
	}
	if len(items) >= taskStateMaxItems {
		items = items[1:]
	}
	return append(items, item)
}

func appendTaskFailure(items []TaskStateFailure, item TaskStateFailure) []TaskStateFailure {
	if strings.TrimSpace(item.Tool) == "" {
		return items
	}
	if len(items) >= taskStateMaxItems {
		items = items[1:]
	}
	return append(items, item)
}

func appendTaskEvidence(items []TaskStateEvidence, item TaskStateEvidence) []TaskStateEvidence {
	if strings.TrimSpace(item.Source) == "" && strings.TrimSpace(item.Summary) == "" {
		return items
	}
	if len(items) >= taskStateMaxItems {
		items = items[1:]
	}
	return append(items, item)
}

func appendUniqueTaskString(items []string, item string, limit int) []string {
	item = strings.TrimSpace(item)
	if item == "" {
		return items
	}
	for _, existing := range items {
		if existing == item {
			return items
		}
	}
	if limit > 0 && len(items) >= limit {
		items = items[1:]
	}
	return append(items, item)
}

func compactTaskStateSummary(text string) string {
	text = strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
	if text == "" {
		return ""
	}
	return previewSubstr(text, taskStateSummaryMaxRune)
}

func taskStateEmpty(task TaskStateSnapshot) bool {
	return task.Objective == "" &&
		(task.Status == "" || task.Status == "unknown") &&
		task.CurrentStep == "" &&
		task.RequestMode == "" &&
		task.RequestSource == "" &&
		task.ScheduleID == "" &&
		task.ScheduleKind == "" &&
		task.NextStep == "" &&
		(task.VerificationState == "" || task.VerificationState == "unknown") &&
		len(task.ChangedFiles) == 0 &&
		len(task.AttemptedActions) == 0 &&
		len(task.FailedActions) == 0 &&
		len(task.Evidence) == 0 &&
		len(task.Sources) == 0
}
