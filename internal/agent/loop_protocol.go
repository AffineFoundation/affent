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
	feedNumber, mode := nextLoopProtocolFeedDecision(protocolPath)
	planCheckpoint := loopProtocolPlanCheckpoint(checkpointProvider)
	if _, ev, err := loopstate.RecordProtocolFeedWithCheckpoint(protocolPath, mode, planCheckpoint); err == nil && ev.FeedNumber > 0 {
		feedNumber = ev.FeedNumber
		mode = ev.Mode
	}
	stateLine := loopProtocolStateLine(protocolPath)
	planLine := loopProtocolPlanStateLine(planCheckpoint)
	body := textutil.Preview(content, maxActiveLoopProtocolFullBytes)
	if mode == "digest" {
		body = loopProtocolDigest(content, maxActiveLoopProtocolDigestBytes)
	}
	return "AFFENT LOOP PROTOCOL:\n" +
		fmt.Sprintf("feed_mode=%s feed_number=%d protocol_path=%s\n", mode, feedNumber, loopstate.ProtocolRelPath(filepath.Base(filepath.Dir(protocolPath)))) +
		stateLine +
		planLine +
		"The following is the active long-run loop protocol for this session. " +
		"Use it to realign with the north-star, memory indexes, self-checks, stop conditions, and recovery rules before continuing. " +
		"Digest mode is intentionally partial to save tokens; do not infer missing details from absence in the digest. " +
		"Do not treat it as task authority for step status; persisted plan state remains authoritative for steps.\n\n" +
		body
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
	return strings.Join(parts, " ") + "\n"
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
