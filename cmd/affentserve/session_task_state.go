package main

import (
	"errors"
	"os"
	"strings"

	"github.com/affinefoundation/affent/internal/sse"
	"github.com/affinefoundation/affent/internal/taskstate"
)

const (
	sessionTaskStateMaxItems       = 8
	sessionTaskStateSummaryMaxChar = 180
)

type sessionTaskEventState = taskstate.EventState

func populateSessionTaskState(summary *sessionSummary, eventsPath string) error {
	if summary == nil {
		return nil
	}
	var eventState sessionTaskEventState
	if strings.TrimSpace(eventsPath) != "" {
		state, err := sessionTaskStateFromEventsFile(eventsPath)
		if err != nil {
			return err
		}
		if state != nil {
			eventState = *state
		}
	}
	task := deriveSessionTaskState(*summary, eventState)
	if sessionTaskStateEmpty(task) {
		summary.TaskState = nil
		return nil
	}
	summary.TaskState = &task
	return nil
}

func deriveSessionTaskState(summary sessionSummary, eventState sessionTaskEventState) sessionTaskStateSummary {
	task := sessionTaskStateSummary{
		Objective:         firstNonEmpty(eventState.Objective, summary.TopicUserMessage, eventState.LatestRequestText, summary.LatestUserMessage),
		Status:            sessionTaskStatus(summary, eventState),
		CurrentStep:       sessionTaskCurrentStep(summary),
		RequestMode:       eventState.RequestMode,
		RequestSource:     eventState.RequestSource,
		ScheduleID:        eventState.ScheduleID,
		ScheduleKind:      eventState.ScheduleKind,
		ChangedFiles:      eventState.ChangedFiles,
		AttemptedActions:  eventState.AttemptedActions,
		FailedActions:     eventState.FailedActions,
		Evidence:          eventState.Evidence,
		VerificationState: eventState.VerificationState,
		OpenQuestions:     eventState.OpenQuestions,
		Sources:           eventState.Sources,
	}
	task.Constraints = sessionTaskConstraints(summary, eventState)
	task.KnownFacts = sessionTaskKnownFacts(summary, eventState)
	task.NextStep = sessionTaskNextStep(task, summary)
	if task.VerificationState == "" {
		task.VerificationState = "unknown"
	}
	return task
}

func sessionTaskStateFromEventsFile(path string) (*sessionTaskEventState, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	if err := seekSessionSummaryTail(f); err != nil {
		return nil, err
	}
	return taskstate.ScanEvents(f, taskstate.EventScanOptions{
		MaxItems:       sessionTaskStateMaxItems,
		SummaryMaxChar: sessionTaskStateSummaryMaxChar,
		MaxLineBytes:   maxSessionSummaryLineBytes,
	})
}

func sessionTaskCurrentStep(summary sessionSummary) string {
	if summary.PlanSummary != nil {
		if summary.PlanSummary.CurrentStep != "" {
			return summary.PlanSummary.CurrentStep
		}
		if summary.PlanSummary.BlockedStep != "" {
			return summary.PlanSummary.BlockedStep
		}
	}
	if summary.LoopState != nil && strings.TrimSpace(summary.LoopState.Status) != "" {
		return "Loop protocol " + strings.TrimSpace(summary.LoopState.Status)
	}
	return ""
}

func sessionTaskStatus(summary sessionSummary, eventState sessionTaskEventState) string {
	if summary.PlanSummary != nil {
		switch {
		case summary.PlanSummary.Blocked:
			return "blocked"
		case summary.PlanSummary.Done:
			return "completed"
		case summary.PlanSummary.Active:
			return "running"
		}
	}
	if summary.LoopState != nil {
		switch strings.ToLower(strings.TrimSpace(summary.LoopState.Status)) {
		case "blocked":
			return "blocked"
		case "completed", "done":
			return "completed"
		case "running", "draft":
			return "running"
		}
	}
	switch strings.TrimSpace(eventState.LatestTurnStatus) {
	case sse.TurnEndCompleted:
		return "completed"
	case sse.TurnEndCancelled:
		return "cancelled"
	case sse.TurnEndMaxTurns:
		return "blocked"
	case sse.TurnEndError:
		return "failed"
	}
	if summary.Active {
		return "running"
	}
	return "unknown"
}

func sessionTaskConstraints(summary sessionSummary, eventState sessionTaskEventState) []string {
	var out []string
	if eventState.RuntimeWorkspace != nil && eventState.RuntimeWorkspace.PathMode != "" {
		out = appendUniqueLimited(out, "workspace path mode: "+eventState.RuntimeWorkspace.PathMode, sessionTaskStateMaxItems)
	}
	if eventState.RuntimeWorkspace != nil && eventState.RuntimeWorkspace.WorkspacePath != "" {
		out = appendUniqueLimited(out, "active workspace path: "+eventState.RuntimeWorkspace.WorkspacePath, sessionTaskStateMaxItems)
	}
	if summary.Capabilities != nil && len(summary.Capabilities.WorkspaceTools) > 0 {
		out = appendUniqueLimited(out, "workspace tools available", sessionTaskStateMaxItems)
	}
	if summary.PlanSummary != nil && summary.PlanSummary.Active {
		out = appendUniqueLimited(out, "active plan is unfinished", sessionTaskStateMaxItems)
	}
	if summary.LoopState != nil && strings.EqualFold(strings.TrimSpace(summary.LoopState.Status), "running") {
		out = appendUniqueLimited(out, "loop protocol is running", sessionTaskStateMaxItems)
	}
	if _, unavailable := runtimeSurfaceCapabilityLabels(eventState.RuntimeSurface); len(unavailable) > 0 {
		out = appendUniqueLimited(out, "unavailable capabilities: "+strings.Join(unavailable, ", "), sessionTaskStateMaxItems)
	}
	return out
}

func sessionTaskKnownFacts(summary sessionSummary, eventState sessionTaskEventState) []string {
	var out []string
	if requestMode := strings.TrimSpace(eventState.RequestMode); requestMode != "" && requestMode != "normal" {
		out = appendUniqueLimited(out, "latest request mode: "+requestMode, sessionTaskStateMaxItems)
	}
	if requestSource := sessionTaskRequestSourceFact(eventState); requestSource != "" {
		out = appendUniqueLimited(out, requestSource, sessionTaskStateMaxItems)
	}
	if summary.WorkspaceLabel != "" {
		out = appendUniqueLimited(out, "workspace: "+summary.WorkspaceLabel, sessionTaskStateMaxItems)
	}
	if eventState.RuntimeWorkspace != nil && eventState.RuntimeWorkspace.WorkspaceLabel != "" {
		out = appendUniqueLimited(out, "active workspace: "+eventState.RuntimeWorkspace.WorkspaceLabel, sessionTaskStateMaxItems)
	}
	if eventState.RuntimeWorkspace != nil && len(eventState.RuntimeWorkspace.RootEntries) > 0 {
		var names []string
		for _, entry := range eventState.RuntimeWorkspace.RootEntries {
			if strings.TrimSpace(entry.Name) != "" {
				names = append(names, entry.Name)
			}
			if len(names) >= 5 {
				break
			}
		}
		if len(names) > 0 {
			out = appendUniqueLimited(out, "workspace root entries: "+strings.Join(names, ", "), sessionTaskStateMaxItems)
		}
	}
	if summary.LatestRecoveryHint != "" {
		out = appendUniqueLimited(out, "latest recovery hint: "+summary.LatestRecoveryHint, sessionTaskStateMaxItems)
	}
	if available, _ := runtimeSurfaceCapabilityLabels(eventState.RuntimeSurface); len(available) > 0 {
		out = appendUniqueLimited(out, "available capabilities: "+strings.Join(available, ", "), sessionTaskStateMaxItems)
	}
	if fact := sessionTaskLoopProtocolControlFact(eventState.RuntimeSurface); fact != "" {
		out = appendUniqueLimited(out, fact, sessionTaskStateMaxItems)
	}
	return out
}

func sessionTaskLoopProtocolControlFact(surface *sse.RuntimeSurfacePayload) string {
	if surface == nil || surface.LoopProtocolControl == nil {
		return ""
	}
	state := "disabled"
	if surface.LoopProtocolControl.Enabled {
		state = "enabled"
	}
	return "latest turn loop protocol control: " + state
}

func sessionTaskRequestSourceFact(eventState sessionTaskEventState) string {
	source := strings.TrimSpace(eventState.RequestSource)
	if source == "" {
		return ""
	}
	var parts []string
	parts = append(parts, "latest request source: "+source)
	if kind := strings.TrimSpace(eventState.ScheduleKind); kind != "" {
		parts = append(parts, kind)
	}
	if id := strings.TrimSpace(eventState.ScheduleID); id != "" {
		parts = append(parts, id)
	}
	return strings.Join(parts, " ")
}

func sessionTaskNextStep(task sessionTaskStateSummary, summary sessionSummary) string {
	if next := taskstate.NextStep(task); next != "" || task.Status == "completed" && task.VerificationState == "last_shell_passed" {
		return next
	}
	if summary.PlanSummary != nil && summary.PlanSummary.CurrentStep != "" {
		return summary.PlanSummary.CurrentStep
	}
	if summary.LatestRecoveryHint != "" {
		return summary.LatestRecoveryHint
	}
	return ""
}

func appendUniqueLimited(items []string, item string, limit int) []string {
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

func sessionTaskStateEmpty(task sessionTaskStateSummary) bool {
	return taskstate.IsEmpty(task)
}

func sessionTaskStateContextProvider(eventsPath string) func(string) string {
	eventsPath = strings.TrimSpace(eventsPath)
	if eventsPath == "" {
		return nil
	}
	return func(string) string {
		state, err := sessionTaskStateFromEventsFile(eventsPath)
		if err != nil || state == nil || !sessionTaskStateNeedsContext(state.Snapshot) {
			return ""
		}
		text := taskstate.SearchText(state.Snapshot)
		if text == "" {
			return ""
		}
		return "AFFENT TASK STATE:\n" + text
	}
}

func sessionTaskStateNeedsContext(task sessionTaskStateSummary) bool {
	if strings.TrimSpace(task.NextStep) != "" || len(task.OpenQuestions) > 0 {
		return true
	}
	switch strings.TrimSpace(task.VerificationState) {
	case "failed":
		return true
	}
	switch strings.TrimSpace(task.Status) {
	case "failed", "blocked", "cancelled":
		return true
	default:
		return false
	}
}
