package agent

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/affinefoundation/affent/internal/loopstate"
	"github.com/affinefoundation/affent/internal/sse"
	"github.com/affinefoundation/affent/internal/textutil"
)

const (
	maxActiveLoopProtocolFullBytes   = 12 * 1024
	maxActiveLoopProtocolDigestBytes = 4 * 1024
	loopProtocolFullFirstFeeds       = 1
	loopProtocolFullEveryFeeds       = 6
)

// WithLoopProtocolSkillProvider injects a session's LOOP.md only when that
// file exists. Missing protocols are a no-op, so runtimes can wire this once
// and let explicit loop activation decide whether extra context is spent.
func WithLoopProtocolSkillProvider(protocolPath string, next SkillProvider) SkillProvider {
	return WithLoopProtocolSkillProviderWithCheckpoint(protocolPath, nil, next)
}

type LoopProtocolCheckpointProvider func() loopstate.PlanCheckpoint

// WithLoopProtocolSkillProviderWithCheckpoint injects LOOP.md and mirrors an
// external plan checkpoint into the loop feed. The plan remains authoritative
// through the separate active-plan provider; this checkpoint is a recovery
// pointer for traces, WebUI, and post-compaction alignment.
func WithLoopProtocolSkillProviderWithCheckpoint(protocolPath string, checkpointProvider LoopProtocolCheckpointProvider, next SkillProvider) SkillProvider {
	return func(userText string) string {
		parts := make([]string, 0, 2)
		if block := activeLoopProtocolSkillBlockWithCheckpoint(protocolPath, checkpointProvider); block != "" {
			parts = append(parts, block)
		}
		if next != nil {
			if block := strings.TrimSpace(next(userText)); block != "" {
				parts = append(parts, block)
			}
		}
		return strings.Join(parts, "\n\n")
	}
}

func activeLoopProtocolSkillBlock(protocolPath string) string {
	return activeLoopProtocolSkillBlockWithCheckpoint(protocolPath, nil)
}

func loopProtocolFeedMode(feedNumber int) string {
	if feedNumber <= loopProtocolFullFirstFeeds {
		return "full"
	}
	if loopProtocolFullEveryFeeds > 0 && feedNumber%loopProtocolFullEveryFeeds == 0 {
		return "full"
	}
	return "digest"
}

func activeLoopProtocolSkillBlockWithCheckpoint(protocolPath string, checkpointProvider LoopProtocolCheckpointProvider) string {
	content, found, err := loopstate.ReadProtocol(protocolPath)
	if err != nil || !found {
		return ""
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return ""
	}
	if status := loopstate.ProtocolStatus(content); status != "" && status != "running" {
		return ""
	}
	if !loopProtocolActive(protocolPath) {
		return ""
	}
	feedNumber, mode := nextLoopProtocolFeedDecision(protocolPath)
	planCheckpoint := loopProtocolPlanCheckpoint(checkpointProvider)
	if _, ev, err := loopstate.RecordProtocolFeedWithCheckpoint(protocolPath, mode, planCheckpoint); err == nil && ev.FeedNumber > 0 {
		feedNumber = ev.FeedNumber
		mode = ev.Mode
	}
	stateLine := loopProtocolStateLine(protocolPath, planCheckpoint)
	situationLine := loopProtocolCurrentSituationLine(content)
	planLine := loopProtocolPlanStateLine(planCheckpoint)
	body := textutil.Preview(content, maxActiveLoopProtocolFullBytes)
	if mode == "digest" {
		body = loopProtocolDigest(content, maxActiveLoopProtocolDigestBytes)
	}
	return "AFFENT LOOP PROTOCOL:\n" +
		fmt.Sprintf("feed_mode=%s feed_number=%d protocol_path=%s\n", mode, feedNumber, loopstate.ProtocolRelPath(filepath.Base(filepath.Dir(protocolPath)))) +
		stateLine +
		situationLine +
		planLine +
		"The following is the active long-run loop protocol for this session. " +
		"Use it to realign with the north-star, current situation, self-checks, stop conditions, and recovery rules before continuing. " +
		"Digest mode is intentionally partial to save tokens; do not infer missing details from absence in the digest. " +
		"Do not treat it as task authority for step status; persisted plan state remains authoritative for steps.\n\n" +
		body
}

func loopProtocolActive(protocolPath string) bool {
	state, found, err := loopstate.ReadState(filepath.Join(filepath.Dir(protocolPath), loopstate.StateFileName))
	if err != nil || !found {
		return true
	}
	status := strings.ToLower(strings.TrimSpace(state.Status))
	return status == "" || status == "running"
}

// LoopProtocolCompletionGuard prevents a final answer from leaving an active
// long-running LOOP.md in status=running. It observes the durable protocol
// state directly so it works after resume, restart, or protocol closure.
func LoopProtocolCompletionGuard(protocolPath string) CompletionGuard {
	return func() CompletionGuardResult {
		if strings.TrimSpace(protocolPath) == "" {
			return CompletionGuardResult{}
		}
		relPath := loopstate.ProtocolRelPath(filepath.Base(filepath.Dir(protocolPath)))
		summary, found, err := loopstate.SummarizeFile(protocolPath, relPath)
		if err != nil || !found || strings.TrimSpace(summary.Status) != "running" {
			return CompletionGuardResult{}
		}
		reason := "Active loop protocol is still running."
		if summary.LoopID != "" {
			reason = fmt.Sprintf("Active loop protocol %s is still running.", summary.LoopID)
		}
		required := "Use loop_protocol action=close with status completed, blocked, or paused before finalizing."
		prompt := "AFFENT COMPLETION GUARD:\n" +
			reason + "\n" +
			required + "\n" +
			"If the loop objective is complete, close it as completed with compact evidence. If it cannot continue, close it as blocked with the missing external condition. If it should wait deliberately, close it as paused. Do not leave a running loop behind a final answer."
		return CompletionGuardResult{
			Blocked:        true,
			ID:             "loop-protocol-running",
			Trigger:        "loop_protocol_running",
			Reason:         reason,
			RequiredAction: required,
			Prompt:         prompt,
		}
	}
}

func loopProtocolStateLine(protocolPath string, livePlanCheckpoint loopstate.PlanCheckpoint) string {
	state, found, err := loopstate.ReadState(filepath.Join(filepath.Dir(protocolPath), loopstate.StateFileName))
	if err != nil || !found {
		return ""
	}
	var parts []string
	if state.LoopID != "" {
		parts = append(parts, "loop_id="+state.LoopID)
	}
	if state.Status != "" {
		parts = append(parts, "status="+state.Status)
	}
	if state.ProtocolUpdates > 0 {
		parts = append(parts, fmt.Sprintf("protocol_updates=%d", state.ProtocolUpdates))
	}
	if state.ProtocolFeeds > 0 {
		parts = append(parts, fmt.Sprintf("protocol_feeds=%d", state.ProtocolFeeds))
	}
	if state.LastProtocolFeedMode != "" {
		parts = append(parts, "last_feed="+state.LastProtocolFeedMode)
	}
	if state.CalibrationAnswers > 0 {
		parts = append(parts, fmt.Sprintf("calibration_answers=%d", state.CalibrationAnswers))
	}
	if state.NeedsFullProtocolFeed {
		parts = append(parts, "needs_full_feed=true")
	}
	if state.ContextCompactions > 0 {
		parts = append(parts, fmt.Sprintf("context_compactions=%d", state.ContextCompactions))
	}
	if state.LastCompactionReason != "" {
		parts = append(parts, "last_compaction="+state.LastCompactionReason)
	}
	if state.LastPlanLabel != "" {
		parts = append(parts, "last_plan="+state.LastPlanLabel)
	}
	if state.LastEventSummary != "" {
		parts = append(parts, "last_event="+state.LastEventSummary)
	}
	if len(parts) == 0 {
		return ""
	}
	var lines []string
	lines = append(lines, strings.Join(parts, " "))
	if state.LastTurnID != "" || state.LastTurnEndReason != "" {
		var turn []string
		if state.LastTurnID != "" {
			turn = append(turn, "id="+state.LastTurnID)
		}
		if state.LastTurnEndReason != "" {
			turn = append(turn, "reason="+state.LastTurnEndReason)
		}
		if state.LastTurnInputTokens > 0 || state.LastTurnOutputTokens > 0 {
			turn = append(turn, fmt.Sprintf("tokens=%d/%d", state.LastTurnInputTokens, state.LastTurnOutputTokens))
		}
		if state.LastTurnToolRequests > 0 {
			turn = append(turn, fmt.Sprintf("tools=%d", state.LastTurnToolRequests))
		}
		if state.LastTurnToolRequestsSkipped > 0 || (state.LastTurnToolRequestsAdmitted > 0 && state.LastTurnToolRequestsAdmitted != state.LastTurnToolRequests) {
			turn = append(turn, fmt.Sprintf("tools_admitted=%d tools_skipped=%d", state.LastTurnToolRequestsAdmitted, state.LastTurnToolRequestsSkipped))
		}
		if state.LastTurnToolErrors > 0 {
			turn = append(turn, fmt.Sprintf("tool_errors=%d", state.LastTurnToolErrors))
		}
		if state.LastTurnForcedNoTools > 0 {
			turn = append(turn, fmt.Sprintf("forced_no_tools=%d", state.LastTurnForcedNoTools))
		}
		if state.LastTurnMemoryUpdates > 0 {
			turn = append(turn, fmt.Sprintf("memory_updates=%d", state.LastTurnMemoryUpdates))
		}
		if state.LastTurnMemorySearches > 0 {
			turn = append(turn, fmt.Sprintf("memory_searches=%d memory_misses=%d", state.LastTurnMemorySearches, state.LastTurnMemoryMisses))
		}
		if state.LastTurnSessionSearch > 0 {
			turn = append(turn, fmt.Sprintf("session_search=%d", state.LastTurnSessionSearch))
		}
		if state.LastTurnLoopGuards > 0 {
			turn = append(turn, fmt.Sprintf("loop_guards=%d", state.LastTurnLoopGuards))
		}
		if len(turn) > 0 {
			lines = append(lines, "last_turn: "+strings.Join(turn, " "))
		}
	}
	if !livePlanCheckpoint.Valid && (state.LastPlanLabel != "" || state.LastPlanStep != "") {
		planCheckpoint := loopstate.PlanCheckpoint{
			Valid:      true,
			Label:      state.LastPlanLabel,
			StepIndex:  state.LastPlanStepIndex,
			StepStatus: state.LastPlanStepStatus,
			Step:       state.LastPlanStep,
		}
		if line := strings.TrimSpace(loopProtocolPlanStateLine(planCheckpoint)); line != "" {
			lines = append(lines, "last_plan_checkpoint:\n"+line)
		}
	}
	if state.LastCalibrationAnswer != "" {
		lines = append(lines, "last_calibration: "+loopProtocolInlineFields([]string{
			"answer=" + state.LastCalibrationAnswer,
		}))
	}
	if state.LastMemoryUpdateAction != "" || state.LastMemoryUpdateLoc != "" || state.LastMemoryUpdate != "" {
		lines = append(lines, "last_memory_update: "+loopProtocolInlineFields([]string{
			"action=" + state.LastMemoryUpdateAction,
			"location=" + state.LastMemoryUpdateLoc,
			"preview=" + state.LastMemoryUpdate,
		}))
	}
	if state.LastDecisionKind != "" || state.LastDecision != "" || state.LastDecisionAction != "" {
		fields := []string{
			"kind=" + state.LastDecisionKind,
			"trigger=" + state.LastDecisionTrigger,
			"decision=" + state.LastDecision,
			"confidence=" + state.LastDecisionConfidence,
		}
		fields = appendLoopProtocolPositiveIntField(fields, "token_budget", state.LastDecisionTokenBudget)
		fields = appendLoopProtocolPositiveIntField(fields, "observed_input", state.LastDecisionObservedInput)
		fields = appendLoopProtocolPositiveIntField(fields, "projected_input", state.LastDecisionProjectedInput)
		fields = appendLoopProtocolPositiveIntField(fields, "budget_bytes", state.LastDecisionBudgetBytes)
		fields = append(fields,
			"reason="+state.LastDecisionReason,
			"action="+state.LastDecisionAction,
		)
		lines = append(lines, "last_decision: "+loopProtocolInlineFields(fields))
	}
	return strings.Join(lines, "\n") + "\n"
}

func appendLoopProtocolPositiveIntField(fields []string, key string, value int) []string {
	if value <= 0 {
		return fields
	}
	return append(fields, key+"="+strconv.Itoa(value))
}

func loopProtocolInlineFields(fields []string) string {
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		key, value, ok := strings.Cut(field, "=")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		out = append(out, key+"="+loopProtocolInlineValue(value))
	}
	return strings.Join(out, " ")
}

func loopProtocolInlineValue(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	return textutil.Preview(value, 220)
}

func nextLoopProtocolFeedDecision(protocolPath string) (int, string) {
	state, found, err := loopstate.ReadState(filepath.Join(filepath.Dir(protocolPath), loopstate.StateFileName))
	if err != nil || !found {
		return 1, loopProtocolFeedMode(1)
	}
	next := state.ProtocolFeeds + 1
	if state.NeedsFullProtocolFeed {
		return next, "full"
	}
	return next, loopProtocolFeedMode(next)
}

func loopProtocolPlanCheckpoint(provider LoopProtocolCheckpointProvider) loopstate.PlanCheckpoint {
	if provider == nil {
		return loopstate.PlanCheckpoint{}
	}
	return provider()
}

func loopProtocolCurrentSituationLine(content string) string {
	if preview := loopProtocolCurrentSituationPreview(content, 360); preview != "" {
		return "current_situation: " + preview + "\n"
	}
	return ""
}

func loopProtocolCurrentSituationPreview(content string, maxBytes int) string {
	for _, section := range splitMarkdownSections(content) {
		if !loopProtocolCurrentSituationHeading(section.heading) {
			continue
		}
		body := markdownSectionBody(section.text)
		if body == "" {
			return ""
		}
		return textutil.Preview(body, maxBytes)
	}
	return ""
}

func loopProtocolCurrentSituationHeading(heading string) bool {
	heading = strings.ToLower(strings.TrimSpace(heading))
	for _, marker := range []string{"current situation", "current state", "现状", "当前状态"} {
		if strings.Contains(heading, marker) {
			return true
		}
	}
	return false
}

func markdownSectionBody(section string) string {
	lines := strings.Split(section, "\n")
	if len(lines) > 0 && strings.HasPrefix(strings.TrimSpace(lines[0]), "#") {
		lines = lines[1:]
	}
	var out []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		out = append(out, line)
	}
	return strings.Join(strings.Fields(strings.Join(out, " ")), " ")
}

func (l *Loop) recordLoopProtocolCalibrationQuestionIfReady(turnID, text string, opts TurnOptions) {
	if l == nil {
		return
	}
	path := strings.TrimSpace(l.LoopProtocolPath)
	question := strings.TrimSpace(text)
	if path == "" || !opts.ForceLoopCalibrationQuestion {
		return
	}
	state, found, err := loopstate.ReadState(filepath.Join(filepath.Dir(path), loopstate.StateFileName))
	if err != nil || !found || state.Status != "draft" {
		return
	}
	if state.CalibrationQuestions > state.CalibrationAnswers {
		return
	}
	preview := loopstate.ProtocolCalibrationPreview(question)
	if state.LastCalibrationQuestion == preview {
		return
	}
	state, event, err := loopstate.RecordProtocolCalibrationQuestion(path, question)
	if err != nil {
		l.Log.Warn().Err(err).Msg("record loop protocol calibration question")
		return
	}
	l.publish(sse.TypeLoopCalibrationRequest, sse.LoopProtocolCalibrationPayload{
		LoopID:                  state.LoopID,
		Status:                  state.Status,
		CalibrationQuestions:    state.CalibrationQuestions,
		LastCalibrationQuestion: state.LastCalibrationQuestion,
		CalibrationAnswers:      state.CalibrationAnswers,
		LastCalibrationAnswer:   state.LastCalibrationAnswer,
		ProtocolPath:            loopstate.ProtocolRelPath(filepath.Base(filepath.Dir(path))),
		EventSeq:                event.Seq,
	})
}

func (l *Loop) loopProtocolStartSetupCreatedDraft(toolName string, args json.RawMessage, isErr bool) bool {
	if l == nil || isErr || toolName != LoopProtocolToolName || strings.TrimSpace(l.LoopProtocolPath) == "" {
		return false
	}
	var parsed loopProtocolToolArgs
	if err := json.Unmarshal(args, &parsed); err != nil {
		return false
	}
	if strings.ToLower(strings.TrimSpace(parsed.Action)) != "start_setup" {
		return false
	}
	state, found, err := loopstate.ReadState(filepath.Join(filepath.Dir(l.LoopProtocolPath), loopstate.StateFileName))
	return err == nil &&
		found &&
		state.Status == "draft" &&
		state.LastEventType == "loop.protocol_init" &&
		state.CalibrationQuestions == 0
}

func loopProtocolPlanStateLine(checkpoint loopstate.PlanCheckpoint) string {
	if !checkpoint.Valid || checkpoint.Label == "" {
		return ""
	}
	parts := []string{"plan_label=" + checkpoint.Label}
	if checkpoint.StepIndex > 0 {
		parts = append(parts, fmt.Sprintf("plan_step_index=%d", checkpoint.StepIndex))
	}
	if checkpoint.StepStatus != "" {
		parts = append(parts, "plan_step_status="+checkpoint.StepStatus)
	}
	out := strings.Join(parts, " ") + "\n"
	if step := strings.TrimSpace(checkpoint.Step); step != "" {
		out += "plan_current_step: " + textutil.Preview(step, 240) + "\n"
	}
	return out
}

func loopProtocolDigest(content string, maxBytes int) string {
	content = strings.TrimSpace(content)
	if content == "" {
		return ""
	}
	sections := splitMarkdownSections(content)
	if len(sections) == 0 {
		return textutil.Preview(content, maxBytes)
	}
	var selected []string
	for _, section := range sections {
		if loopProtocolSectionRelevant(section.heading) {
			selected = append(selected, section.text)
		}
	}
	if len(selected) == 0 {
		return textutil.Preview(content, maxBytes)
	}
	return textutil.Preview(strings.Join(selected, "\n\n"), maxBytes)
}

type markdownSection struct {
	heading string
	text    string
}

func splitMarkdownSections(content string) []markdownSection {
	lines := strings.Split(content, "\n")
	var sections []markdownSection
	var current []string
	heading := ""
	flush := func() {
		text := strings.TrimSpace(strings.Join(current, "\n"))
		if text == "" {
			return
		}
		sections = append(sections, markdownSection{heading: heading, text: text})
	}
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			flush()
			current = []string{line}
			heading = strings.ToLower(strings.TrimSpace(strings.TrimLeft(trimmed, "#")))
			continue
		}
		current = append(current, line)
	}
	flush()
	return sections
}

func loopProtocolSectionRelevant(heading string) bool {
	if heading == "" {
		return true
	}
	keywords := []string{
		"metadata",
		"north star",
		"北极星",
		"current situation",
		"current state",
		"现状",
		"当前状态",
		"self",
		"自我",
		"memory",
		"记忆",
		"rule",
		"规则",
		"plan",
		"step",
		"停止",
		"stop",
		"checkpoint",
		"恢复",
		"recovery",
	}
	for _, keyword := range keywords {
		if strings.Contains(heading, keyword) {
			return true
		}
	}
	return false
}

func loopProtocolFeedPayloadFromBlock(turnID, block string) (sse.LoopProtocolFeedPayload, bool) {
	block = strings.TrimSpace(block)
	if !strings.HasPrefix(block, "AFFENT LOOP PROTOCOL:") {
		return sse.LoopProtocolFeedPayload{}, false
	}
	payload := sse.LoopProtocolFeedPayload{TurnID: turnID}
	lines := strings.Split(block, "\n")
	for _, line := range lines[1:] {
		line = strings.TrimSpace(line)
		if line == "" {
			break
		}
		for _, field := range strings.Fields(line) {
			key, value, ok := strings.Cut(field, "=")
			if !ok || value == "" {
				continue
			}
			switch key {
			case "feed_mode":
				payload.Mode = value
			case "feed_number":
				payload.FeedNumber = parsePositiveInt(value)
			case "protocol_path":
				payload.ProtocolPath = value
			case "loop_id":
				payload.LoopID = value
			case "status":
				payload.Status = value
			case "protocol_feeds":
				payload.ProtocolFeeds = parsePositiveInt(value)
			case "calibration_answers":
				payload.CalibrationAnswers = parsePositiveInt(value)
			case "plan_label":
				payload.PlanLabel = value
			case "plan_step_index":
				payload.PlanCurrentStepIndex = parsePositiveInt(value)
			case "plan_step_status":
				payload.PlanCurrentStepStatus = value
			}
		}
		if step, ok := strings.CutPrefix(line, "plan_current_step:"); ok {
			payload.PlanCurrentStep = strings.TrimSpace(step)
		}
		if situation, ok := strings.CutPrefix(line, "current_situation:"); ok {
			payload.CurrentSituation = textutil.Preview(strings.TrimSpace(situation), 360)
		}
		if calibration, ok := strings.CutPrefix(line, "last_calibration:"); ok {
			calibration = strings.TrimSpace(calibration)
			if answer, ok := strings.CutPrefix(calibration, "answer="); ok {
				payload.LastCalibrationAnswer = textutil.Preview(strings.TrimSpace(answer), 220)
			}
		}
		if turn, ok := strings.CutPrefix(line, "last_turn:"); ok {
			applyLoopProtocolLastTurnFields(&payload, turn)
		}
		if decision, ok := strings.CutPrefix(line, "last_decision:"); ok {
			applyLoopProtocolLastDecisionFields(&payload, decision)
		}
	}
	if payload.Mode == "" || payload.FeedNumber <= 0 {
		return sse.LoopProtocolFeedPayload{}, false
	}
	if payload.ProtocolFeeds == 0 {
		payload.ProtocolFeeds = payload.FeedNumber
	}
	return payload, true
}

func applyLoopProtocolLastTurnFields(payload *sse.LoopProtocolFeedPayload, raw string) {
	if payload == nil {
		return
	}
	for _, field := range strings.Fields(strings.TrimSpace(raw)) {
		key, value, ok := strings.Cut(field, "=")
		if !ok || value == "" {
			continue
		}
		switch key {
		case "id":
			payload.LastTurnID = value
		case "reason":
			payload.LastTurnEndReason = value
		case "tools":
			payload.LastTurnToolRequests = parsePositiveInt(value)
		case "tools_admitted":
			payload.LastTurnToolRequestsAdmitted = parsePositiveInt(value)
		case "tools_skipped":
			payload.LastTurnToolRequestsSkipped = parsePositiveInt(value)
		case "tool_errors":
			payload.LastTurnToolErrors = parsePositiveInt(value)
		case "forced_no_tools":
			payload.LastTurnForcedNoTools = parsePositiveInt(value)
		case "memory_updates":
			payload.LastTurnMemoryUpdates = parsePositiveInt(value)
		case "memory_searches":
			payload.LastTurnMemorySearchCalls = parsePositiveInt(value)
		case "memory_misses":
			payload.LastTurnMemorySearchMisses = parsePositiveInt(value)
		case "session_search":
			payload.LastTurnSessionSearchCalls = parsePositiveInt(value)
		case "loop_guards":
			payload.LastTurnLoopGuards = parsePositiveInt(value)
		}
	}
}

func applyLoopProtocolLastDecisionFields(payload *sse.LoopProtocolFeedPayload, raw string) {
	if payload == nil {
		return
	}
	fields := loopProtocolKeyValueSegments(raw, []string{"kind", "trigger", "decision", "confidence", "token_budget", "observed_input", "projected_input", "budget_bytes", "reason", "action"})
	payload.LastDecisionKind = fields["kind"]
	payload.LastDecisionTrigger = fields["trigger"]
	payload.LastDecision = fields["decision"]
	payload.LastDecisionConfidence = fields["confidence"]
	payload.LastDecisionTokenBudget = parsePositiveInt(fields["token_budget"])
	payload.LastDecisionObservedInput = parsePositiveInt(fields["observed_input"])
	payload.LastDecisionProjectedInput = parsePositiveInt(fields["projected_input"])
	payload.LastDecisionBudgetBytes = parsePositiveInt(fields["budget_bytes"])
	payload.LastDecisionReason = fields["reason"]
	payload.LastDecisionAction = fields["action"]
}

func loopProtocolKeyValueSegments(raw string, keys []string) map[string]string {
	raw = strings.TrimSpace(raw)
	out := make(map[string]string)
	type segment struct {
		key   string
		start int
		end   int
	}
	var segments []segment
	for _, key := range keys {
		marker := key + "="
		searchFrom := 0
		for {
			idx := strings.Index(raw[searchFrom:], marker)
			if idx < 0 {
				break
			}
			idx += searchFrom
			if idx == 0 || raw[idx-1] == ' ' {
				segments = append(segments, segment{key: key, start: idx, end: idx + len(marker)})
				break
			}
			searchFrom = idx + len(marker)
		}
	}
	sort.Slice(segments, func(i, j int) bool { return segments[i].start < segments[j].start })
	for i, segment := range segments {
		next := len(raw)
		if i+1 < len(segments) {
			next = segments[i+1].start
		}
		value := strings.TrimSpace(raw[segment.end:next])
		if value != "" {
			out[segment.key] = textutil.Preview(value, 220)
		}
	}
	return out
}

func parsePositiveInt(raw string) int {
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return 0
	}
	return n
}
