package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"

	"github.com/affinefoundation/affent/internal/jsonl"
	"github.com/affinefoundation/affent/internal/sse"
	"github.com/affinefoundation/affent/internal/textutil"
	"github.com/affinefoundation/affent/internal/toolfailure"
)

const (
	sessionTaskStateMaxItems       = 8
	sessionTaskStateSummaryMaxChar = 180
)

type sessionTaskEventState struct {
	LatestTurnStatus  string
	LatestRequestText string
	RequestMode       string
	RequestSource     string
	ScheduleID        string
	ScheduleKind      string
	RuntimeSurface    *sse.RuntimeSurfacePayload
	RuntimeWorkspace  *sse.RuntimeWorkspace
	ChangedFiles      []sessionTaskStateFile
	AttemptedActions  []sessionTaskStateAction
	FailedActions     []sessionTaskStateFailure
	Evidence          []sessionTaskStateEvidence
	OpenQuestions     []string
	VerificationState string
	Sources           []string
}

type sessionTaskRequest struct {
	Tool   string
	TurnID string
	CallID string
	Args   map[string]any
}

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
		Objective:         firstNonEmpty(summary.TopicUserMessage, eventState.LatestRequestText, summary.LatestUserMessage),
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
	return scanSessionTaskStateFromEvents(bufio.NewReaderSize(f, 64*1024))
}

func scanSessionTaskStateFromEvents(r *bufio.Reader) (*sessionTaskEventState, error) {
	state := &sessionTaskEventState{}
	requests := map[string]sessionTaskRequest{}
	seen := false
	for {
		line, tooLong, err := jsonl.ReadBoundedLine(r, maxSessionSummaryLineBytes)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if tooLong {
			continue
		}
		line = bytes.TrimRight(line, "\r\n")
		var ev sse.Event
		if err := json.Unmarshal(line, &ev); err != nil {
			continue
		}
		switch ev.Type {
		case sse.TypeUserMessage:
			var p sse.UserMessagePayload
			if err := json.Unmarshal(ev.Data, &p); err != nil {
				continue
			}
			state.LatestRequestText = compactTaskSummary(p.Text)
			state.RequestMode = strings.TrimSpace(p.Mode)
			state.RequestSource = strings.TrimSpace(p.Source)
			state.ScheduleID = strings.TrimSpace(p.ScheduleID)
			state.ScheduleKind = strings.TrimSpace(p.ScheduleKind)
			addTaskStateSource(state, state.RequestSource)
			seen = true
		case sse.TypeRuntimeSurface:
			var p sse.RuntimeSurfacePayload
			if err := json.Unmarshal(ev.Data, &p); err != nil {
				continue
			}
			state.RuntimeSurface = &p
			if runtimeSurfaceHasCapabilityData(&p) {
				addTaskStateSource(state, "runtime_surface")
				seen = true
			}
			if p.Workspace != nil {
				state.RuntimeWorkspace = p.Workspace
				seen = true
			}
		case sse.TypeContextInjected:
			var p sse.ContextInjectedPayload
			if err := json.Unmarshal(ev.Data, &p); err != nil {
				continue
			}
			if p.Source != "" {
				addTaskStateSource(state, p.Source)
			}
			if p.Source == "runtime_workspace" || p.Source == "active_plan" || p.Source == "skill" {
				state.Evidence = appendTaskEvidence(state.Evidence, sessionTaskStateEvidence{
					Source:  p.Source,
					Summary: compactTaskSummary(firstNonEmpty(p.Summary, p.Title, p.Preview)),
					TurnID:  p.TurnID,
				})
				seen = true
			}
		case sse.TypeToolRequest:
			var p sse.ToolRequestPayload
			if err := json.Unmarshal(ev.Data, &p); err != nil || p.CallID == "" {
				continue
			}
			req := sessionTaskRequest{Tool: p.Tool, TurnID: p.TurnID, CallID: p.CallID, Args: p.Args}
			requests[p.CallID] = req
			state.AttemptedActions = appendTaskAction(state.AttemptedActions, sessionTaskStateAction{
				Tool:    p.Tool,
				Summary: compactTaskSummary(taskActionSummary(req)),
				TurnID:  p.TurnID,
				CallID:  p.CallID,
			})
			if file := changedFileFromTaskRequest(req); file.Path != "" {
				state.ChangedFiles = appendTaskFile(state.ChangedFiles, file)
			}
			seen = true
		case sse.TypeToolResult:
			var p sse.ToolResultPayload
			if err := json.Unmarshal(ev.Data, &p); err != nil {
				continue
			}
			req := requests[p.CallID]
			kinds := taskFailureKinds(req.Tool, p)
			if p.ExitCode != 0 || len(kinds) > 0 {
				state.FailedActions = appendTaskFailure(state.FailedActions, sessionTaskStateFailure{
					Tool:    firstNonEmpty(req.Tool, "tool"),
					Summary: compactTaskSummary(firstNonEmpty(p.ResultSummary, p.Result)),
					Kinds:   kinds,
					TurnID:  firstNonEmpty(p.TurnID, req.TurnID),
					CallID:  p.CallID,
				})
				state.VerificationState = "failed"
			} else if req.Tool == "shell" && p.ExitCode == 0 {
				state.VerificationState = "last_shell_passed"
				state.Evidence = appendTaskEvidence(state.Evidence, sessionTaskStateEvidence{
					Source:  "shell",
					Summary: compactTaskSummary(taskActionSummary(req)),
					TurnID:  firstNonEmpty(p.TurnID, req.TurnID),
					CallID:  p.CallID,
				})
			}
			seen = true
		case sse.TypeMessageRejected:
			var p sse.MessageRejectedPayload
			if err := json.Unmarshal(ev.Data, &p); err != nil {
				continue
			}
			if p.RequiredAction != "" {
				state.OpenQuestions = appendUniqueLimited(state.OpenQuestions, compactTaskSummary(p.RequiredAction), sessionTaskStateMaxItems)
				seen = true
			}
		case sse.TypeTurnEnd:
			var p sse.TurnEndPayload
			if err := json.Unmarshal(ev.Data, &p); err != nil {
				continue
			}
			state.LatestTurnStatus = strings.TrimSpace(p.Reason)
			seen = true
		}
	}
	if !seen {
		return nil, nil
	}
	return state, nil
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
	return out
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
	if len(task.OpenQuestions) > 0 {
		return task.OpenQuestions[len(task.OpenQuestions)-1]
	}
	if len(task.FailedActions) > 0 && task.VerificationState != "last_shell_passed" && task.Status != "completed" {
		return "latest failed action is unresolved"
	}
	if summary.PlanSummary != nil && summary.PlanSummary.CurrentStep != "" {
		return summary.PlanSummary.CurrentStep
	}
	if summary.LatestRecoveryHint != "" {
		return summary.LatestRecoveryHint
	}
	return ""
}

func taskActionSummary(req sessionTaskRequest) string {
	switch req.Tool {
	case "shell":
		return argString(req.Args, "command")
	case "read_file", "write_file", "edit_file", "list_files", "file_context", "symbol_context", "repo_search":
		return argString(req.Args, "path")
	case "plan", "memory", "skill", "loop_protocol":
		return argString(req.Args, "action")
	case "run_task":
		return argString(req.Args, "task_type")
	case "subagent_run":
		return argString(req.Args, "mode")
	default:
		for _, key := range []string{"path", "query", "url", "action", "task", "objective"} {
			if value := argString(req.Args, key); value != "" {
				return key + ": " + value
			}
		}
	}
	return ""
}

func changedFileFromTaskRequest(req sessionTaskRequest) sessionTaskStateFile {
	switch req.Tool {
	case "write_file":
		return sessionTaskStateFile{Path: argString(req.Args, "path"), Action: "write"}
	case "edit_file":
		return sessionTaskStateFile{Path: argString(req.Args, "path"), Action: "edit"}
	default:
		return sessionTaskStateFile{}
	}
}

func taskFailureKinds(tool string, p sse.ToolResultPayload) []string {
	var out []string
	for _, kind := range p.FailureKinds {
		out = appendUniqueLimited(out, kind, sessionTaskStateMaxItems)
	}
	if p.FailureKind != "" {
		out = appendUniqueLimited(out, p.FailureKind, sessionTaskStateMaxItems)
	}
	for _, kind := range toolfailure.KindsForResult(tool, p.Result, p.ExitCode != 0) {
		out = appendUniqueLimited(out, kind, sessionTaskStateMaxItems)
	}
	return out
}

func appendTaskAction(items []sessionTaskStateAction, item sessionTaskStateAction) []sessionTaskStateAction {
	if item.Tool == "" {
		return items
	}
	if len(items) >= sessionTaskStateMaxItems {
		items = items[1:]
	}
	return append(items, item)
}

func appendTaskFailure(items []sessionTaskStateFailure, item sessionTaskStateFailure) []sessionTaskStateFailure {
	if item.Tool == "" {
		return items
	}
	if len(items) >= sessionTaskStateMaxItems {
		items = items[1:]
	}
	return append(items, item)
}

func appendTaskEvidence(items []sessionTaskStateEvidence, item sessionTaskStateEvidence) []sessionTaskStateEvidence {
	if item.Source == "" && item.Summary == "" {
		return items
	}
	if len(items) >= sessionTaskStateMaxItems {
		items = items[1:]
	}
	return append(items, item)
}

func appendTaskFile(items []sessionTaskStateFile, item sessionTaskStateFile) []sessionTaskStateFile {
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
	if len(items) >= sessionTaskStateMaxItems {
		items = items[1:]
	}
	return append(items, item)
}

func addTaskStateSource(state *sessionTaskEventState, source string) {
	if state == nil {
		return
	}
	state.Sources = appendUniqueLimited(state.Sources, strings.TrimSpace(source), sessionTaskStateMaxItems)
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

func compactTaskSummary(text string) string {
	text = strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
	return textutil.Preview(text, sessionTaskStateSummaryMaxChar)
}

func sessionTaskStateEmpty(task sessionTaskStateSummary) bool {
	return task.Objective == "" &&
		(task.Status == "" || task.Status == "unknown") &&
		task.CurrentStep == "" &&
		task.RequestMode == "" &&
		task.RequestSource == "" &&
		task.ScheduleID == "" &&
		task.ScheduleKind == "" &&
		len(task.Constraints) == 0 &&
		len(task.KnownFacts) == 0 &&
		len(task.ChangedFiles) == 0 &&
		len(task.AttemptedActions) == 0 &&
		len(task.FailedActions) == 0 &&
		len(task.Evidence) == 0 &&
		(task.VerificationState == "" || task.VerificationState == "unknown") &&
		len(task.OpenQuestions) == 0 &&
		task.NextStep == "" &&
		len(task.Sources) == 0
}
