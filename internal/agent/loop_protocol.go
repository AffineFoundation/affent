package agent

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/affinefoundation/affent/internal/loopstate"
	"github.com/affinefoundation/affent/internal/sse"
	"github.com/affinefoundation/affent/internal/textutil"
)

const (
	maxActiveLoopProtocolFullBytes   = 12 * 1024
	maxActiveLoopProtocolDigestBytes = 4 * 1024
	loopProtocolFullFirstFeeds       = 3
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
	stateLine := loopProtocolStateLine(protocolPath)
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

func loopProtocolStateLine(protocolPath string) string {
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
		lines = append(lines, "last_decision: "+loopProtocolInlineFields([]string{
			"kind=" + state.LastDecisionKind,
			"trigger=" + state.LastDecisionTrigger,
			"decision=" + state.LastDecision,
			"action=" + state.LastDecisionAction,
		}))
	}
	return strings.Join(lines, "\n") + "\n"
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

func (l *Loop) recordLoopProtocolCalibrationQuestionIfReady(turnID, text string) {
	if l == nil {
		return
	}
	path := strings.TrimSpace(l.LoopProtocolPath)
	question := strings.TrimSpace(text)
	if path == "" || !looksLikeLoopProtocolCalibrationQuestion(question) {
		return
	}
	state, found, err := loopstate.ReadState(filepath.Join(filepath.Dir(path), loopstate.StateFileName))
	if err != nil || !found || state.Status != "draft" {
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

func looksLikeLoopProtocolCalibrationQuestion(text string) bool {
	normalized := strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(text)), " "))
	if normalized == "" || (!strings.Contains(normalized, "?") && !strings.Contains(normalized, "？")) {
		return false
	}
	loopishMarkers := []string{"loop", "loop.md", "long-run", "long running", "长期", "循环"}
	if !loopProtocolContainsAny(normalized, loopishMarkers) {
		return false
	}
	calibrationMarkers := []string{
		"calibration", "stop condition", "pause", "stop", "memory", "remember", "recovery", "goal", "objective", "constraint", "success", "timer", "schedule",
		"校准", "暂停", "停止", "记忆", "恢复", "目标", "约束", "成功", "定时",
	}
	return loopProtocolContainsAny(normalized, calibrationMarkers)
}

func loopProtocolContainsAny(text string, markers []string) bool {
	for _, marker := range markers {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
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
	}
	if payload.Mode == "" || payload.FeedNumber <= 0 {
		return sse.LoopProtocolFeedPayload{}, false
	}
	if payload.ProtocolFeeds == 0 {
		payload.ProtocolFeeds = payload.FeedNumber
	}
	return payload, true
}

func parsePositiveInt(raw string) int {
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return 0
	}
	return n
}
