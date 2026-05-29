package taskstate

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/affinefoundation/affent/internal/jsonl"
	"github.com/affinefoundation/affent/internal/sse"
	"github.com/affinefoundation/affent/internal/textutil"
	"github.com/affinefoundation/affent/internal/toolfailure"
)

const (
	DefaultMaxItems       = 8
	DefaultSummaryMaxChar = 180
)

type EventState struct {
	Snapshot
	LatestTurnStatus  string
	LatestRequestText string
	RuntimeSurface    *sse.RuntimeSurfacePayload
	RuntimeWorkspace  *sse.RuntimeWorkspace
}

type EventScanOptions struct {
	MaxItems       int
	SummaryMaxChar int
	MaxLineBytes   int
}

type ToolRequest struct {
	Tool   string
	TurnID string
	CallID string
	Args   map[string]any
}

type ToolResult struct {
	Tool          string
	TurnID        string
	CallID        string
	Result        string
	ResultSummary string
	FailureKind   string
	FailureKinds  []string
	ExitCode      int
}

func ScanEvents(r io.Reader, opts EventScanOptions) (*EventState, error) {
	if r == nil {
		return nil, nil
	}
	maxLineBytes := opts.MaxLineBytes
	if maxLineBytes <= 0 {
		maxLineBytes = jsonl.DefaultMaxRecordBytes
	}
	reader, ok := r.(*bufio.Reader)
	if !ok {
		reader = bufio.NewReaderSize(r, 64*1024)
	}
	state := &EventState{}
	requests := map[string]ToolRequest{}
	seen := false
	for {
		line, tooLong, err := jsonl.ReadBoundedLine(reader, maxLineBytes)
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
			state.LatestRequestText = compactSummary(p.Text, opts.SummaryMaxChar)
			if state.Objective == "" {
				state.Objective = compactSummary(firstNonEmpty(p.DisplayText, p.Text), opts.SummaryMaxChar)
			}
			state.RequestMode = NormalizeRequestMode(p.Mode)
			state.RequestSource = NormalizeRequestSource(p.Source)
			state.ScheduleID = strings.TrimSpace(p.ScheduleID)
			state.ScheduleKind = strings.TrimSpace(p.ScheduleKind)
			addSource(state, state.RequestSource, opts.MaxItems)
			seen = true
		case sse.TypeRuntimeSurface:
			var p sse.RuntimeSurfacePayload
			if err := json.Unmarshal(ev.Data, &p); err != nil {
				continue
			}
			state.RuntimeSurface = &p
			if summary := RuntimeSurfaceSummary(&p); summary != "" {
				state.Evidence = appendEvidence(state.Evidence, Evidence{
					Source:  "runtime_surface",
					Summary: compactSummary(summary, opts.SummaryMaxChar),
					TurnID:  p.TurnID,
				}, opts.MaxItems)
				addSource(state, "runtime_surface", opts.MaxItems)
				seen = true
			}
			if summary := RuntimeSurfaceCompactionPolicySummary(&p); summary != "" {
				state.Evidence = appendEvidence(state.Evidence, Evidence{
					Source:  "runtime_surface",
					Summary: compactSummary(summary, opts.SummaryMaxChar),
					TurnID:  p.TurnID,
				}, opts.MaxItems)
				addSource(state, "runtime_surface", opts.MaxItems)
				seen = true
			}
			if summary := RuntimeSurfaceRequestPressureSummary(&p); summary != "" {
				state.Evidence = appendEvidence(state.Evidence, Evidence{
					Source:  "runtime_surface",
					Summary: compactSummary(summary, opts.SummaryMaxChar),
					TurnID:  p.TurnID,
				}, opts.MaxItems)
				addSource(state, "runtime_surface", opts.MaxItems)
				seen = true
			}
			if runtimeSurfaceHasCapabilityData(&p) {
				addSource(state, "runtime_surface", opts.MaxItems)
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
				addSource(state, p.Source, opts.MaxItems)
			}
			if p.Source == "runtime_workspace" || p.Source == "active_plan" || p.Source == "skill" {
				state.Evidence = appendEvidence(state.Evidence, Evidence{
					Source:  p.Source,
					Summary: compactSummary(firstNonEmpty(p.Summary, p.Title, p.Preview), opts.SummaryMaxChar),
					TurnID:  p.TurnID,
				}, opts.MaxItems)
				seen = true
			}
		case sse.TypeContextCompact:
			var p sse.ContextCompactPayload
			if err := json.Unmarshal(ev.Data, &p); err != nil {
				continue
			}
			summary := contextCompactionSummary(p)
			state.Evidence = appendEvidence(state.Evidence, Evidence{
				Source:  "context_compaction",
				Summary: compactSummary(summary, opts.SummaryMaxChar),
				TurnID:  p.TurnID,
			}, opts.MaxItems)
			addSource(state, "context_compaction", opts.MaxItems)
			seen = true
		case sse.TypeContextCompactSkipped:
			var p sse.ContextCompactSkippedPayload
			if err := json.Unmarshal(ev.Data, &p); err != nil {
				continue
			}
			state.Evidence = appendEvidence(state.Evidence, Evidence{
				Source:  "context_compaction_skipped",
				Summary: compactSummary(contextCompactionSkippedSummary(p), opts.SummaryMaxChar),
				TurnID:  p.TurnID,
			}, opts.MaxItems)
			addSource(state, "context_compaction_skipped", opts.MaxItems)
			seen = true
		case sse.TypeToolRequest:
			var p sse.ToolRequestPayload
			if err := json.Unmarshal(ev.Data, &p); err != nil || p.CallID == "" {
				continue
			}
			req := ToolRequest{Tool: p.Tool, TurnID: p.TurnID, CallID: p.CallID, Args: p.Args}
			requests[p.CallID] = req
			state.AttemptedActions = AppendAction(state.AttemptedActions, Action{
				Tool:    p.Tool,
				Summary: compactSummary(ToolActionSummary(req), opts.SummaryMaxChar),
				TurnID:  p.TurnID,
				CallID:  p.CallID,
			}, opts.MaxItems)
			if file := ToolChangedFile(req); file.Path != "" {
				state.ChangedFiles = appendFile(state.ChangedFiles, file, opts.MaxItems)
			}
			seen = true
		case sse.TypeToolResult:
			var p sse.ToolResultPayload
			if err := json.Unmarshal(ev.Data, &p); err != nil {
				continue
			}
			req := requests[p.CallID]
			kinds := ToolFailureKinds(ToolResult{
				Tool:          req.Tool,
				TurnID:        firstNonEmpty(p.TurnID, req.TurnID),
				CallID:        p.CallID,
				Result:        p.Result,
				ResultSummary: p.ResultSummary,
				FailureKind:   p.FailureKind,
				FailureKinds:  p.FailureKinds,
				ExitCode:      p.ExitCode,
			}, opts.MaxItems)
			if p.ExitCode != 0 || len(kinds) > 0 {
				state.FailedActions = appendFailure(state.FailedActions, Failure{
					Tool:    firstNonEmpty(req.Tool, "tool"),
					Summary: compactSummary(firstNonEmpty(p.ResultSummary, p.Result), opts.SummaryMaxChar),
					Kinds:   kinds,
					Next:    NextHint(p.ResultSummary, p.Result),
					TurnID:  firstNonEmpty(p.TurnID, req.TurnID),
					CallID:  p.CallID,
				}, opts.MaxItems)
				if ToolRequestLooksLikeVerification(req) {
					state.VerificationState = "failed"
				}
			} else if source := ToolEvidenceSource(req.Tool); source != "" {
				if ToolRequestLooksLikeVerification(req) {
					state.VerificationState = "last_shell_passed"
				}
				action := ToolActionSummary(req)
				summary := compactSummary(action, opts.SummaryMaxChar)
				state.Evidence = appendEvidence(state.Evidence, Evidence{
					Source:  source,
					Summary: summary,
					TurnID:  firstNonEmpty(p.TurnID, req.TurnID),
					CallID:  p.CallID,
				}, opts.MaxItems)
				addSource(state, source, opts.MaxItems)
				var sources []string
				if req.Tool == "shell" {
					sources = ShellHandoffEvidenceSources(action)
				}
				for _, source := range sources {
					state.Evidence = appendEvidence(state.Evidence, Evidence{
						Source:  source,
						Summary: summary,
						TurnID:  firstNonEmpty(p.TurnID, req.TurnID),
						CallID:  p.CallID,
					}, opts.MaxItems)
					addSource(state, source, opts.MaxItems)
				}
			}
			seen = true
		case sse.TypeMessageRejected:
			var p sse.MessageRejectedPayload
			if err := json.Unmarshal(ev.Data, &p); err != nil {
				continue
			}
			if p.RequiredAction != "" {
				state.OpenQuestions = appendUnique(state.OpenQuestions, compactSummary(p.RequiredAction, opts.SummaryMaxChar), opts.MaxItems)
				seen = true
			}
		case sse.TypeTurnEnd:
			var p sse.TurnEndPayload
			if err := json.Unmarshal(ev.Data, &p); err != nil {
				continue
			}
			state.LatestTurnStatus = strings.TrimSpace(p.Reason)
			state.Status = statusFromTurnEnd(p.Reason)
			seen = true
		}
	}
	if !seen || IsEmpty(state.Snapshot) {
		return nil, nil
	}
	if state.VerificationState == "" {
		state.VerificationState = "unknown"
	}
	if state.NextStep == "" {
		state.NextStep = NextStep(state.Snapshot)
	}
	return state, nil
}

func NextStep(task Snapshot) string {
	if len(task.OpenQuestions) > 0 {
		return task.OpenQuestions[len(task.OpenQuestions)-1]
	}
	if task.Status == "completed" && task.VerificationState == "last_shell_passed" {
		return ""
	}
	if len(task.FailedActions) > 0 && task.VerificationState != "last_shell_passed" {
		latest := task.FailedActions[len(task.FailedActions)-1]
		if next := strings.TrimSpace(latest.Next); next != "" {
			return next
		}
		return "latest failed action is unresolved"
	}
	return ""
}

func SearchText(task Snapshot) string {
	if IsEmpty(task) {
		return ""
	}
	var b strings.Builder
	appendStateLine(&b, "task_state",
		"status="+task.Status,
		"verification="+task.VerificationState,
		"mode="+task.RequestMode,
		"source="+task.RequestSource,
		"schedule_kind="+task.ScheduleKind,
		"schedule_id="+task.ScheduleID,
	)
	appendTextLine(&b, "objective", task.Objective)
	appendTextLine(&b, "current_step", task.CurrentStep)
	appendTextLine(&b, "next_step", task.NextStep)
	for _, item := range task.OpenQuestions {
		appendTextLine(&b, "open_question", item)
	}
	for _, item := range task.Constraints {
		appendTextLine(&b, "constraint", item)
	}
	for _, item := range task.KnownFacts {
		appendTextLine(&b, "known_fact", item)
	}
	for _, file := range task.ChangedFiles {
		appendStateLine(&b, "changed_file", "action="+file.Action, "path="+file.Path)
	}
	for _, action := range task.AttemptedActions {
		appendStateLine(&b, "attempted_action", "tool="+action.Tool, "summary="+action.Summary)
	}
	for _, failure := range task.FailedActions {
		appendStateLine(&b, "failed_action", "tool="+failure.Tool, "kinds="+strings.Join(failure.Kinds, ","), "summary="+failure.Summary, "next="+failure.Next)
	}
	for _, evidence := range task.Evidence {
		appendStateLine(&b, "evidence", "source="+evidence.Source, "summary="+evidence.Summary)
	}
	if len(task.Sources) > 0 {
		appendTextLine(&b, "sources", strings.Join(task.Sources, ", "))
	}
	return strings.TrimSpace(b.String())
}

func ToolActionSummary(req ToolRequest) string {
	switch req.Tool {
	case "shell":
		return argString(req.Args, "command")
	case "read_file", "write_file", "edit_file", "list_files", "file_context", "symbol_context", "repo_search":
		return argString(req.Args, "path")
	case "plan", "memory", "skill", "loop_protocol", "session_schedule":
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

func ToolChangedFile(req ToolRequest) File {
	switch req.Tool {
	case "write_file":
		return File{Path: argString(req.Args, "path"), Action: "write"}
	case "edit_file":
		return File{Path: argString(req.Args, "path"), Action: "edit"}
	default:
		return File{}
	}
}

func ToolEvidenceSource(tool string) string {
	switch tool {
	case "shell", "plan", "memory", "loop_protocol", "session_schedule":
		return tool
	default:
		return ""
	}
}

func contextCompactionSummary(p sse.ContextCompactPayload) string {
	var fields []string
	reason := strings.TrimSpace(p.Reason)
	if reason == "" {
		reason = "threshold"
	}
	fields = append(fields, "reason="+reason)
	if anchor := strings.TrimSpace(p.LoopProtocolAnchor); anchor != "" {
		fields = append(fields, "loop_anchor="+anchor)
	}
	if p.Reactive {
		fields = append(fields, "reactive=true")
	}
	if p.RemovedMessages > 0 {
		fields = append(fields, fmt.Sprintf("removed_messages=%d", p.RemovedMessages))
	}
	if p.ReducedBytes > 0 {
		fields = append(fields, fmt.Sprintf("reduced_bytes=%d", p.ReducedBytes))
	}
	if p.EstimatedInputTokens > 0 {
		fields = append(fields, fmt.Sprintf("estimated_input_tokens=%d", p.EstimatedInputTokens))
	}
	if p.TriggerInputTokens > 0 {
		fields = append(fields, fmt.Sprintf("trigger_input_tokens=%d", p.TriggerInputTokens))
	}
	if p.ModelContextWindowTokens > 0 {
		fields = append(fields, fmt.Sprintf("model_context_window_tokens=%d", p.ModelContextWindowTokens))
	}
	if p.ModelContextWindowEffectivePercent > 0 {
		fields = append(fields, fmt.Sprintf("model_context_window_effective_percent=%d", p.ModelContextWindowEffectivePercent))
	}
	if p.ReservedOutputTokens > 0 {
		fields = append(fields, fmt.Sprintf("reserved_output_tokens=%d", p.ReservedOutputTokens))
	}
	if p.CompactTriggerInputPercent > 0 {
		fields = append(fields, fmt.Sprintf("compact_trigger_input_percent=%d", p.CompactTriggerInputPercent))
	}
	if p.SummaryPresent {
		fields = append(fields, "summary_present=true")
	}
	return strings.Join(fields, " ")
}

func contextCompactionSkippedSummary(p sse.ContextCompactSkippedPayload) string {
	var fields []string
	cause := strings.TrimSpace(p.Cause)
	if cause == "" {
		cause = "discarded_candidate"
	}
	fields = append(fields, "cause="+cause)
	reason := strings.TrimSpace(p.Reason)
	if reason == "" {
		reason = "threshold"
	}
	fields = append(fields, "reason="+reason)
	if p.BeforeMessages > 0 || p.CandidateMessages > 0 {
		fields = append(fields, fmt.Sprintf("messages=%d->%d", p.BeforeMessages, p.CandidateMessages))
	}
	if p.BeforeBytes > 0 || p.CandidateBytes > 0 {
		fields = append(fields, fmt.Sprintf("bytes=%d->%d", p.BeforeBytes, p.CandidateBytes))
	}
	if p.EstimatedInputTokens > 0 {
		fields = append(fields, fmt.Sprintf("estimated_input_tokens=%d", p.EstimatedInputTokens))
	}
	if p.AfterEstimatedInputTokens > 0 {
		fields = append(fields, fmt.Sprintf("after_estimated_input_tokens=%d", p.AfterEstimatedInputTokens))
	}
	if p.TriggerInputTokens > 0 {
		fields = append(fields, fmt.Sprintf("trigger_input_tokens=%d", p.TriggerInputTokens))
	}
	if p.ModelContextWindowTokens > 0 {
		fields = append(fields, fmt.Sprintf("model_context_window_tokens=%d", p.ModelContextWindowTokens))
	}
	if p.ModelContextWindowEffectivePercent > 0 {
		fields = append(fields, fmt.Sprintf("model_context_window_effective_percent=%d", p.ModelContextWindowEffectivePercent))
	}
	if p.ReservedOutputTokens > 0 {
		fields = append(fields, fmt.Sprintf("reserved_output_tokens=%d", p.ReservedOutputTokens))
	}
	if p.CompactTriggerInputPercent > 0 {
		fields = append(fields, fmt.Sprintf("compact_trigger_input_percent=%d", p.CompactTriggerInputPercent))
	}
	return strings.Join(fields, " ")
}

func ToolFailureKinds(result ToolResult, limit int) []string {
	var out []string
	if result.FailureKind != "" {
		out = appendUnique(out, result.FailureKind, limit)
	}
	for _, kind := range result.FailureKinds {
		out = appendUnique(out, kind, limit)
	}
	for _, kind := range toolfailure.KindsForResult(result.Tool, result.Result, result.ExitCode != 0) {
		out = appendUnique(out, kind, limit)
	}
	return out
}

func ToolFailed(result ToolResult, limit int) bool {
	return result.ExitCode != 0 || len(ToolFailureKinds(result, limit)) > 0
}

func AppendAction(items []Action, item Action, limit int) []Action {
	item.Tool = strings.TrimSpace(item.Tool)
	if item.Tool == "" {
		return items
	}
	item.Summary = strings.TrimSpace(item.Summary)
	items = append(items, item)
	if limit <= 0 || len(items) <= limit {
		return items
	}
	return removeLowestValueAction(items)
}

func removeLowestValueAction(items []Action) []Action {
	if len(items) == 0 {
		return items
	}
	remove := 0
	removeRank := ActionRetentionRank(items[0])
	for i, item := range items {
		rank := ActionRetentionRank(item)
		if rank < removeRank {
			remove = i
			removeRank = rank
		}
	}
	out := make([]Action, 0, len(items)-1)
	out = append(out, items[:remove]...)
	out = append(out, items[remove+1:]...)
	return out
}

func ActionRetentionRank(item Action) int {
	tool := strings.TrimSpace(item.Tool)
	summary := strings.TrimSpace(item.Summary)
	if tool == "shell" && len(ShellHandoffEvidenceSources(summary)) > 0 {
		return 100
	}
	if ToolEvidenceSource(tool) != "" {
		return 80
	}
	switch tool {
	case "edit_file", "write_file", "session_workspace":
		return 70
	case "file_context", "repo_search", "symbol_context", "read_file":
		return 40
	case "list_files":
		return 20
	default:
		if summary != "" {
			return 50
		}
		return 10
	}
}

func appendFailure(items []Failure, item Failure, limit int) []Failure {
	if item.Tool == "" {
		return items
	}
	if limit > 0 && len(items) >= limit {
		items = items[1:]
	}
	return append(items, item)
}

func appendEvidence(items []Evidence, item Evidence, limit int) []Evidence {
	if item.Source == "" && item.Summary == "" {
		return items
	}
	if limit > 0 && len(items) >= limit {
		items = removeOldestRepeatedEvidenceSource(items)
	}
	return append(items, item)
}

func removeOldestRepeatedEvidenceSource(items []Evidence) []Evidence {
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
	out := make([]Evidence, 0, len(items)-1)
	out = append(out, items[:remove]...)
	out = append(out, items[remove+1:]...)
	return out
}

func appendFile(items []File, item File, limit int) []File {
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
	if limit > 0 && len(items) >= limit {
		items = items[1:]
	}
	return append(items, item)
}

func addSource(state *EventState, source string, limit int) {
	if state == nil {
		return
	}
	state.Sources = appendUnique(state.Sources, source, limit)
}

func appendUnique(items []string, item string, limit int) []string {
	item = strings.TrimSpace(item)
	if item == "" {
		return items
	}
	for _, existing := range items {
		if existing == item {
			return items
		}
	}
	if limit <= 0 {
		limit = DefaultMaxItems
	}
	if len(items) >= limit {
		items = items[1:]
	}
	return append(items, item)
}

func compactSummary(text string, maxBytes int) string {
	text = strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
	if maxBytes <= 0 {
		maxBytes = DefaultSummaryMaxChar
	}
	return textutil.Preview(text, maxBytes)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func argString(args map[string]any, key string) string {
	if args == nil {
		return ""
	}
	value, ok := args[key]
	if !ok || value == nil {
		return ""
	}
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	default:
		return strings.TrimSpace(fmt.Sprint(v))
	}
}

func NormalizeRequestMode(mode string) string {
	mode = strings.TrimSpace(mode)
	if mode == "" {
		return "normal"
	}
	return mode
}

func NormalizeRequestSource(source string) string {
	source = strings.TrimSpace(source)
	if source == "" {
		return "user"
	}
	return source
}

func statusFromTurnEnd(reason string) string {
	switch strings.TrimSpace(reason) {
	case sse.TurnEndCompleted:
		return "completed"
	case sse.TurnEndCancelled:
		return "cancelled"
	case sse.TurnEndMaxTurns:
		return "blocked"
	case sse.TurnEndError:
		return "failed"
	default:
		return ""
	}
}

func runtimeSurfaceHasCapabilityData(p *sse.RuntimeSurfacePayload) bool {
	if p == nil {
		return false
	}
	return p.ToolCount > 0 ||
		len(p.Tools) > 0 ||
		len(p.ToolCallCaps) > 0 ||
		len(p.CompletionGuards) > 0 ||
		p.Capabilities.Builtins ||
		len(p.Capabilities.WorkspaceTools) > 0 ||
		p.Capabilities.Memory ||
		p.Capabilities.Plan ||
		p.Capabilities.LoopProtocol ||
		p.Capabilities.SessionSchedule ||
		p.Capabilities.SessionSearch ||
		p.Capabilities.WebFetch ||
		p.Capabilities.WebSearch ||
		p.Capabilities.Browser ||
		p.Capabilities.Subagent ||
		p.Capabilities.FocusedTasks ||
		p.Capabilities.Skill ||
		p.Capabilities.MCP
}

func RuntimeSurfaceSummary(p *sse.RuntimeSurfacePayload) string {
	if p == nil {
		return ""
	}
	var fields []string
	addIntField := func(name string, value int) {
		if value > 0 {
			fields = append(fields, fmt.Sprintf("%s=%d", name, value))
		}
	}
	addIntField("model_context_window_tokens", p.ModelContextWindowTokens)
	if p.ModelContextWindowAuto {
		fields = append(fields, "model_context_window_auto=true")
	}
	addIntField("model_context_window_effective_percent", p.ModelContextWindowEffectivePercent)
	addIntField("reserved_output_tokens", p.ReservedOutputTokens)
	addIntField("max_turn_steps", p.MaxTurnSteps)
	addIntField("max_tool_calls", p.MaxToolCalls)
	addIntField("max_turn_input_tokens", p.MaxTurnInputTokens)
	addIntField("available_tool_count", p.AvailableToolCount)
	addIntField("excluded_tool_count", p.ExcludedToolCount)
	if len(fields) == 0 {
		return ""
	}
	return strings.Join(fields, " ")
}

func RuntimeSurfaceCompactionPolicySummary(p *sse.RuntimeSurfacePayload) string {
	if p == nil {
		return ""
	}
	var fields []string
	addIntField := func(name string, value int) {
		if value > 0 {
			fields = append(fields, fmt.Sprintf("%s=%d", name, value))
		}
	}
	addIntField("compact_trigger_input_tokens", p.CompactTriggerInputTokens)
	addIntField("compact_trigger_input_percent", p.CompactTriggerInputPercent)
	addIntField("compact_summary_prompt_max_bytes", p.CompactSummaryPromptMaxBytes)
	if len(fields) == 0 {
		return ""
	}
	return strings.Join(fields, " ")
}

func RuntimeSurfaceRequestPressureSummary(p *sse.RuntimeSurfacePayload) string {
	if p == nil {
		return ""
	}
	var fields []string
	addIntField := func(name string, value int) {
		if value > 0 {
			fields = append(fields, fmt.Sprintf("%s=%d", name, value))
		}
	}
	addIntField("tool_schema_budget_tokens", p.ToolSchemaBudgetTokens)
	addIntField("estimated_tool_schema_tokens", p.EstimatedToolSchemaTokens)
	addIntField("estimated_request_input_tokens", p.EstimatedRequestInputTokens)
	addIntField("available_tool_count", p.AvailableToolCount)
	addIntField("excluded_tool_count", p.ExcludedToolCount)
	if len(fields) == 0 {
		return ""
	}
	return "request_pressure " + strings.Join(fields, " ")
}

func appendTextLine(b *strings.Builder, label, value string) {
	value = compactSummary(value, 300)
	if value == "" {
		return
	}
	fmt.Fprintf(b, "%s: %s\n", label, value)
}

func appendStateLine(b *strings.Builder, label string, fields ...string) {
	var parts []string
	for _, field := range fields {
		key, value, ok := strings.Cut(field, "=")
		if !ok || strings.TrimSpace(value) == "" {
			continue
		}
		parts = append(parts, strings.TrimSpace(key)+"="+compactSummary(value, 300))
	}
	if len(parts) == 0 {
		return
	}
	fmt.Fprintf(b, "%s: %s\n", label, strings.Join(parts, " "))
}
