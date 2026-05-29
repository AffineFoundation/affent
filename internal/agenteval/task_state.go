package agenteval

import (
	"fmt"
	"strings"

	"github.com/affinefoundation/affent/internal/taskstate"
)

const (
	taskStateMaxItems       = 8
	taskStateSummaryMaxRune = 180
)

type TaskStateSnapshot = taskstate.Snapshot
type TaskStateFile = taskstate.File
type TaskStateAction = taskstate.Action
type TaskStateFailure = taskstate.Failure
type TaskStateEvidence = taskstate.Evidence

func DeriveTaskState(trace Trace) TaskStateSnapshot {
	task := TaskStateSnapshot{
		Objective:         traceTaskObjective(trace),
		Status:            traceTaskStatus(trace),
		CurrentStep:       traceTaskCurrentStep(trace),
		VerificationState: traceTaskVerificationState(trace),
	}
	if latest := latestTaskRequest(trace); latest != nil {
		task.RequestMode = taskstate.NormalizeRequestMode(latest.Mode)
		task.RequestSource = taskstate.NormalizeRequestSource(latest.Source)
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
		latest := trace.RuntimeSurfaces[len(trace.RuntimeSurfaces)-1]
		if summary := taskstate.RuntimeSurfaceSummary(&latest); summary != "" {
			task.Evidence = appendTaskEvidence(task.Evidence, TaskStateEvidence{
				Source:  "runtime_surface",
				Summary: compactTaskStateSummary(summary),
				TurnID:  latest.TurnID,
			})
		}
		if summary := taskstate.RuntimeSurfaceCompactionPolicySummary(&latest); summary != "" {
			task.Evidence = appendTaskEvidence(task.Evidence, TaskStateEvidence{
				Source:  "runtime_surface",
				Summary: compactTaskStateSummary(summary),
				TurnID:  latest.TurnID,
			})
		}
		if summary := taskstate.RuntimeSurfaceRequestPressureSummary(&latest); summary != "" {
			task.Evidence = appendTaskEvidence(task.Evidence, TaskStateEvidence{
				Source:  "runtime_surface",
				Summary: compactTaskStateSummary(summary),
				TurnID:  latest.TurnID,
			})
		}
	}
	for _, compaction := range trace.ContextCompactions {
		summary := traceContextCompactionSummary(compaction)
		task.Evidence = appendTaskEvidence(task.Evidence, TaskStateEvidence{
			Source:  "context_compaction",
			Summary: compactTaskStateSummary(summary),
			TurnID:  compaction.TurnID,
		})
		task.Sources = appendUniqueTaskString(task.Sources, "context_compaction", taskStateMaxItems)
	}
	for _, skipped := range trace.ContextCompactionSkips {
		summary := traceContextCompactionSkippedSummary(skipped)
		task.Evidence = appendTaskEvidence(task.Evidence, TaskStateEvidence{
			Source:  "context_compaction_skipped",
			Summary: compactTaskStateSummary(summary),
			TurnID:  skipped.TurnID,
		})
		task.Sources = appendUniqueTaskString(task.Sources, "context_compaction_skipped", taskStateMaxItems)
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
				Kinds:   taskstate.ToolFailureKinds(taskStateToolResult(tool), taskStateMaxItems),
				Next:    taskstate.NextHint(tool.ResultSummary, tool.Result),
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
			for _, source := range taskstate.ShellHandoffEvidenceSources(taskStateToolSummary(tool)) {
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
	return taskstate.CloneSnapshotPtr(in)
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
		if taskstate.ToolRequestLooksLikeVerification(taskStateToolRequest(tool)) && taskStateToolFailed(tool) {
			state = "failed"
		}
		if taskstate.ToolRequestLooksLikeVerification(taskStateToolRequest(tool)) && tool.ExitCode == 0 {
			state = "last_shell_passed"
		}
	}
	return state
}

func traceContextCompactionSummary(c ContextCompaction) string {
	var fields []string
	reason := strings.TrimSpace(c.Reason)
	if reason == "" {
		reason = "threshold"
	}
	fields = append(fields, "reason="+reason)
	if anchor := strings.TrimSpace(c.LoopProtocolAnchor); anchor != "" {
		fields = append(fields, "loop_anchor="+anchor)
	}
	if c.Reactive {
		fields = append(fields, "reactive=true")
	}
	if c.RemovedMessages > 0 {
		fields = append(fields, fmt.Sprintf("removed_messages=%d", c.RemovedMessages))
	}
	if c.ReducedBytes > 0 {
		fields = append(fields, fmt.Sprintf("reduced_bytes=%d", c.ReducedBytes))
	}
	if c.SummaryPresent {
		fields = append(fields, "summary_present=true")
	}
	return strings.Join(fields, " ")
}

func traceContextCompactionSkippedSummary(c ContextCompactionSkip) string {
	var fields []string
	cause := strings.TrimSpace(c.Cause)
	if cause == "" {
		cause = "discarded_candidate"
	}
	fields = append(fields, "cause="+cause)
	reason := strings.TrimSpace(c.Reason)
	if reason == "" {
		reason = "threshold"
	}
	fields = append(fields, "reason="+reason)
	if c.BeforeMessages > 0 || c.CandidateMessages > 0 {
		fields = append(fields, fmt.Sprintf("messages=%d->%d", c.BeforeMessages, c.CandidateMessages))
	}
	if c.BeforeBytes > 0 || c.CandidateBytes > 0 {
		fields = append(fields, fmt.Sprintf("bytes=%d->%d", c.BeforeBytes, c.CandidateBytes))
	}
	if c.EstimatedInputTokens > 0 {
		fields = append(fields, fmt.Sprintf("estimated_input_tokens=%d", c.EstimatedInputTokens))
	}
	if c.AfterEstimatedInputTokens > 0 {
		fields = append(fields, fmt.Sprintf("after_estimated_input_tokens=%d", c.AfterEstimatedInputTokens))
	}
	if c.TriggerInputTokens > 0 {
		fields = append(fields, fmt.Sprintf("trigger_input_tokens=%d", c.TriggerInputTokens))
	}
	if c.ModelContextWindowTokens > 0 {
		fields = append(fields, fmt.Sprintf("model_context_window_tokens=%d", c.ModelContextWindowTokens))
	}
	if c.ReservedOutputTokens > 0 {
		fields = append(fields, fmt.Sprintf("reserved_output_tokens=%d", c.ReservedOutputTokens))
	}
	if c.CompactTriggerInputPercent > 0 {
		fields = append(fields, fmt.Sprintf("compact_trigger_input_percent=%d", c.CompactTriggerInputPercent))
	}
	return strings.Join(fields, " ")
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
	return taskstate.ToolChangedFile(taskStateToolRequest(tool))
}

func taskStateToolFailed(tool ToolCall) bool {
	return taskstate.ToolFailed(taskStateToolResult(tool), taskStateMaxItems)
}

func taskStateToolSummary(tool ToolCall) string {
	return taskstate.ToolActionSummary(taskStateToolRequest(tool))
}

func taskStateToolEvidenceSource(tool ToolCall) string {
	return taskstate.ToolEvidenceSource(tool.Tool)
}

func taskStateToolRequest(tool ToolCall) taskstate.ToolRequest {
	return taskstate.ToolRequest{
		Tool:   tool.Tool,
		TurnID: tool.TurnID,
		CallID: tool.CallID,
		Args:   tool.Args,
	}
}

func taskStateToolResult(tool ToolCall) taskstate.ToolResult {
	return taskstate.ToolResult{
		Tool:          tool.Tool,
		TurnID:        tool.TurnID,
		CallID:        tool.CallID,
		Result:        tool.Result,
		ResultSummary: tool.ResultSummary,
		FailureKind:   tool.FailureKind,
		FailureKinds:  tool.FailureKinds,
		ExitCode:      tool.ExitCode,
	}
}

func appendTaskAction(items []TaskStateAction, item TaskStateAction) []TaskStateAction {
	return taskstate.AppendAction(items, item, taskStateMaxItems)
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
		items = removeOldestRepeatedTaskEvidenceSource(items)
	}
	return append(items, item)
}

func removeOldestRepeatedTaskEvidenceSource(items []TaskStateEvidence) []TaskStateEvidence {
	if len(items) == 0 {
		return items
	}
	counts := map[string]int{}
	for _, item := range items {
		counts[strings.TrimSpace(item.Source)]++
	}
	remove := 0
	for i, item := range items {
		if counts[strings.TrimSpace(item.Source)] > 1 {
			remove = i
			break
		}
	}
	out := make([]TaskStateEvidence, 0, len(items)-1)
	out = append(out, items[:remove]...)
	out = append(out, items[remove+1:]...)
	return out
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
	return taskstate.IsEmpty(task)
}
