package loopstate

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/affinefoundation/affent/internal/textutil"
)

const (
	ProtocolFileName         = "LOOP.md"
	StateFileName            = "state.json"
	EventsFileName           = "events.jsonl"
	MaxProtocolBytes         = 64 * 1024
	MaxCurrentSituationChars = 1200
)

type ProtocolTemplateOptions struct {
	LoopID       string
	OwnerSession string
	Goal         string
	Workspace    string
	Status       string
	Plan         PlanCheckpoint
	Stop         []string
	CreatedAt    time.Time
}

type Summary struct {
	Path         string `json:"path,omitempty"`
	LoopID       string `json:"loop_id,omitempty"`
	OwnerSession string `json:"owner_session,omitempty"`
	Status       string `json:"status,omitempty"`
	UpdatedAt    string `json:"updated_at,omitempty"`
	Bytes        int    `json:"bytes"`
	Preview      string `json:"preview,omitempty"`
	State        *State `json:"state,omitempty"`
}

type ProtocolSectionPatch struct {
	Heading string
	Body    string
}

func ProtocolDir(sessionDir, loopID string) string {
	return filepath.Join(sessionDir, ".affent", "loops", loopID)
}

func ProtocolPath(sessionDir, loopID string) string {
	return filepath.Join(ProtocolDir(sessionDir, loopID), ProtocolFileName)
}

func StatePath(sessionDir, loopID string) string {
	return filepath.Join(ProtocolDir(sessionDir, loopID), StateFileName)
}

func EventsPath(sessionDir, loopID string) string {
	return filepath.Join(ProtocolDir(sessionDir, loopID), EventsFileName)
}

func ProtocolRelPath(loopID string) string {
	return filepath.ToSlash(filepath.Join(".affent", "loops", loopID, ProtocolFileName))
}

func DefaultProtocolTemplate(opts ProtocolTemplateOptions) string {
	loopID := strings.TrimSpace(opts.LoopID)
	if loopID == "" {
		loopID = "loop"
	}
	owner := strings.TrimSpace(opts.OwnerSession)
	if owner == "" {
		owner = loopID
	}
	createdAt := opts.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	goal := templateLine(opts.Goal)
	if goal == "" {
		goal = "Make steady, evidence-backed progress on the user's long-running objective without losing recovery context."
	}
	workspace := protocolWorkspaceLabel(opts.Workspace)
	if workspace == "" {
		workspace = "not recorded"
	}
	status := templateStatus(opts.Status)
	if status == "" {
		status = "running"
	}
	planBlock := protocolTemplatePlanBlock(opts.Plan)
	stopBlock := protocolTemplateStopBlock(opts.Stop)
	return strings.TrimSpace(`# Loop Protocol: ` + templateLine(loopID) + `

## 0. Metadata

- loop_id: ` + templateLine(loopID) + `
- owner_session: ` + templateLine(owner) + `
- status: ` + status + `
- protocol_version: 1
- created_at: ` + formatTime(createdAt) + `
- updated_at: ` + formatTime(createdAt) + `
- workspace: ` + workspace + `

## 1. North Star

Long-term objective:

1. ` + goal + `
2. Prefer practical completion, low wasted tokens, reliable recovery, and cited evidence over broad but shallow exploration.
3. Keep the loop useful for smaller models: externalize durable state, avoid relying on hidden attention, and use tools only when they materially reduce uncertainty.

Do not:

1. Change the north star silently.
2. Duplicate authoritative task state here; plan/step state remains authoritative.
3. Continue a loop that is completed, unsafe, irrecoverably blocked, or no longer serving the user's objective.

Operational stop conditions:

` + stopBlock + `

## 2. Current Situation

Keep this section short: at most 8 bullets or about 1200 characters. It is the compact global snapshot used after long runs, compaction, or session recovery.

- current intent: ` + goal + `
- hard constraints:
- known evidence:
- current risk or blocker:
- next recovery anchor: check plan state, recent trace, memory search/list, and this protocol before continuing

## 3. Evolution Protocol

The model may maintain this file, but every update must be compact and justified.

1. Preserve the north star unless the user explicitly changes it.
2. Merge similar rules and remove stale rules that no longer trigger.
3. Move detailed history to trace, artifacts, memory, or plan state instead of growing this file.
4. If context is thin after compaction, reload this protocol, memory search/list, plan state, and recent trace pointers before guessing.
5. Record only durable lessons, recovery anchors, and decision rules that should survive many turns.

Latest protocol update:

- time: ` + formatTime(createdAt) + `
- change: initialized default loop protocol
- reason: loop protocol activation

## 4. Self-Attack

Before continuing a long-running step, challenge the current direction:

1. What claim or action lacks evidence?
2. What has changed in the world or repository since the last checkpoint?
3. What repeated failure pattern should become a rule or a stop condition?
4. Is this loop still advancing the north star, or only consuming turns?
5. Should this continue in the current session, pause for user input, or hand off to a focused subtask?

## 5. Rules

Durable rules:

1. Prefer verified primary evidence for live web facts; rendered pages and network responses are often better than raw HTML on JS-heavy sites.
2. After editing code, run the narrowest meaningful tests first, then broaden when the blast radius justifies it.
3. If a tool result is blocked or low quality, change strategy before retrying the same failed input.
4. Use memory for stable preferences, decisions, and lessons; do not copy memory contents into LOOP.md unless they are part of the current situation or a durable rule.
5. After compaction, restart, or a long delay, check plan state, recent trace, memory search/list, and this protocol before guessing.

Candidate rules:

1. Promote only if the same failure recurs or the lesson applies across tasks.

## 6. Plan/Step Pointers

Authoritative task progress lives outside this file.

- current plan: session plan state if present
- active step: injected by the active-plan checkpoint when available
- completed steps: plan state and trace
- blocked steps: plan state and loop decisions
- related trace: session event log and .affent/loops/<loop_id>/events.jsonl

Initialization plan checkpoint:

` + planBlock + `

## 7. Evidence And Recovery Index

Keep this section short. Store detailed history in artifacts or trace.

- loop state: state.json and events.jsonl
- memory lookup: use the memory tool or memory files only for stable facts and lessons
- important artifacts:
- important trace spans:
- last known recovery note:
`)
}

func ValidateProtocolActivation(protocol string) error {
	required := []string{
		"## 1. North Star",
		"## 2. Current Situation",
		"## 3. Evolution Protocol",
		"## 4. Self-Attack",
		"## 5. Rules",
		"## 6. Plan/Step Pointers",
		"## 7. Evidence And Recovery Index",
	}
	for _, section := range required {
		if !strings.Contains(protocol, section) {
			return fmt.Errorf("LOOP.md is missing required section %q", section)
		}
	}
	if unresolved := unresolvedActivationPlaceholders(protocol); len(unresolved) > 0 {
		return fmt.Errorf("LOOP.md has unresolved activation placeholder(s): %s", strings.Join(unresolved, ", "))
	}
	return ValidateProtocolMaintenance(protocol)
}

func ValidateProtocolMaintenance(protocol string) error {
	if workspace := protocolMetadataValue(protocol, "workspace"); looksAbsoluteWorkspacePath(workspace) {
		return fmt.Errorf("LOOP.md metadata workspace must not be an absolute runtime path; use a stable label such as %q", protocolWorkspaceLabel(""))
	}
	if n := currentSituationCharCount(protocol); n > MaxCurrentSituationChars {
		return fmt.Errorf("LOOP.md Current Situation section is %d characters; keep it at or below %d characters", n, MaxCurrentSituationChars)
	}
	return nil
}

func protocolWorkspaceLabel(workspace string) string {
	workspace = templateLine(workspace)
	if workspace == "" || looksAbsoluteWorkspacePath(workspace) {
		return "not recorded"
	}
	return workspace
}

func looksAbsoluteWorkspacePath(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	if filepath.IsAbs(value) || strings.HasPrefix(value, `\\`) {
		return true
	}
	if len(value) >= 3 && ((value[0] >= 'A' && value[0] <= 'Z') || (value[0] >= 'a' && value[0] <= 'z')) && value[1] == ':' && (value[2] == '\\' || value[2] == '/') {
		return true
	}
	return false
}

func protocolMetadataValue(protocol, key string) string {
	key = strings.ToLower(strings.TrimSpace(key))
	if key == "" {
		return ""
	}
	inMetadata := false
	for _, line := range strings.Split(protocol, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "## ") {
			inMetadata = strings.EqualFold(trimmed, "## 0. Metadata")
			continue
		}
		if !inMetadata {
			continue
		}
		field, value, ok := strings.Cut(strings.TrimPrefix(trimmed, "- "), ":")
		if !ok || strings.ToLower(strings.TrimSpace(field)) != key {
			continue
		}
		return strings.TrimSpace(value)
	}
	return ""
}

func ValidateProtocolActivationReady(protocolPath string) error {
	state, found, err := ReadState(filepath.Join(filepath.Dir(protocolPath), StateFileName))
	if err != nil {
		return err
	}
	if !found || state.CalibrationQuestions <= 0 || state.CalibrationAnswers <= 0 {
		return errors.New("LOOP.md activation requires a recorded calibration question and user answer after draft setup")
	}
	if state.CalibrationAnswers < state.CalibrationQuestions {
		return errors.New("LOOP.md activation requires an answer for each recorded calibration question")
	}
	return nil
}

// RepairRecordedCalibrationFromProtocol recovers old draft loop state when the
// full protocol already contains a compact "Calibration Q&A (recorded)"
// section but state.json missed the corresponding calibration events.
func RepairRecordedCalibrationFromProtocol(protocolPath, protocol string) (bool, error) {
	state, found, err := ReadState(filepath.Join(filepath.Dir(protocolPath), StateFileName))
	if err != nil || !found {
		return false, err
	}
	if state.Status != "draft" || state.CalibrationAnswers > 0 {
		return false, nil
	}
	pairs := recordedCalibrationPairs(protocol)
	if len(pairs) == 0 {
		return false, nil
	}
	repaired := false
	if state.CalibrationQuestions <= 0 {
		for _, pair := range pairs {
			if _, _, err := RecordProtocolCalibrationQuestion(protocolPath, pair.question); err != nil {
				return repaired, err
			}
			if _, _, err := RecordProtocolCalibrationAnswer(protocolPath, pair.answer); err != nil {
				return repaired, err
			}
			repaired = true
		}
		return repaired, nil
	}
	missingAnswers := state.CalibrationQuestions - state.CalibrationAnswers
	for i := 0; i < missingAnswers && i < len(pairs); i++ {
		if _, _, err := RecordProtocolCalibrationAnswer(protocolPath, pairs[i].answer); err != nil {
			return repaired, err
		}
		repaired = true
	}
	return repaired, nil
}

type recordedCalibrationPair struct {
	question string
	answer   string
}

func recordedCalibrationPairs(protocol string) []recordedCalibrationPair {
	lines := strings.Split(protocol, "\n")
	inRecordedSection := false
	pairs := []recordedCalibrationPair{}
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		lower := strings.ToLower(trimmed)
		if strings.HasPrefix(trimmed, "## ") {
			inRecordedSection = strings.Contains(lower, "calibration q&a") && strings.Contains(lower, "recorded")
			continue
		}
		if !inRecordedSection || !strings.HasPrefix(trimmed, "-") {
			continue
		}
		if pair, ok := recordedCalibrationPairFromLine(trimmed); ok {
			pairs = append(pairs, pair)
		}
	}
	return pairs
}

func recordedCalibrationPairFromLine(line string) (recordedCalibrationPair, bool) {
	line = strings.TrimSpace(strings.TrimPrefix(line, "-"))
	if line == "" {
		return recordedCalibrationPair{}, false
	}
	if before, after, ok := strings.Cut(line, "**:"); ok && strings.Contains(before, "**Q") {
		line = strings.TrimSpace(after)
	}
	answerSplitters := []string{" A: ", " A：", " 答: ", " 答：", "A: ", "A：", "答: ", "答："}
	for _, splitter := range answerSplitters {
		question, answer, ok := strings.Cut(line, splitter)
		if !ok {
			continue
		}
		question = strings.TrimSpace(question)
		answer = strings.TrimSpace(answer)
		if question != "" && answer != "" {
			return recordedCalibrationPair{question: question, answer: answer}, true
		}
	}
	return recordedCalibrationPair{}, false
}

func unresolvedActivationPlaceholders(protocol string) []string {
	blankMarkers := map[string]bool{
		"- hard constraints:":         true,
		"- known evidence:":           true,
		"- current risk or blocker:":  true,
		"- important artifacts:":      true,
		"- important trace spans:":    true,
		"- last known recovery note:": true,
	}
	var unresolved []string
	for _, line := range strings.Split(protocol, "\n") {
		trimmed := strings.TrimSpace(line)
		if blankMarkers[trimmed] {
			unresolved = append(unresolved, strings.TrimPrefix(trimmed, "- "))
		}
	}
	return unresolved
}

func protocolSectionCharCount(protocol, heading string) int {
	body, ok := protocolSectionBody(protocol, heading)
	if !ok {
		return 0
	}
	return len([]rune(body))
}

func currentSituationCharCount(protocol string) int {
	if n := protocolSectionCharCount(protocol, "## 2. Current Situation"); n > 0 {
		return n
	}
	body, ok := protocolSectionBodyByHeadingMarkers(protocol, []string{
		"current situation",
		"current state",
		"现状",
		"当前状态",
	})
	if !ok {
		return 0
	}
	return len([]rune(body))
}

func protocolSectionBody(protocol, heading string) (string, bool) {
	start := strings.Index(protocol, heading)
	if start < 0 {
		return "", false
	}
	body := protocol[start+len(heading):]
	if next := strings.Index(body, "\n## "); next >= 0 {
		body = body[:next]
	}
	return strings.TrimSpace(body), true
}

func ApplyProtocolSectionPatches(protocol string, patches []ProtocolSectionPatch) (string, []string, error) {
	out := strings.TrimSpace(protocol)
	if out == "" {
		return "", nil, errors.New("protocol content is required")
	}
	changed := make([]string, 0, len(patches))
	for _, patch := range patches {
		heading := strings.TrimSpace(patch.Heading)
		body := strings.TrimSpace(patch.Body)
		if heading == "" || body == "" {
			return "", nil, errors.New("protocol section patch requires heading and body")
		}
		next, ok := replaceProtocolSection(out, heading, body)
		if !ok {
			return "", nil, fmt.Errorf("LOOP.md section %q was not found", heading)
		}
		out = next
		changed = append(changed, heading)
	}
	return out, changed, nil
}

func replaceProtocolSection(protocol, heading, body string) (string, bool) {
	lines := strings.Split(protocol, "\n")
	start := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == heading {
			start = i
			break
		}
	}
	if start < 0 {
		return protocol, false
	}
	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		if strings.HasPrefix(strings.TrimSpace(lines[i]), "## ") {
			end = i
			break
		}
	}
	replacement := []string{lines[start], ""}
	replacement = append(replacement, strings.Split(strings.TrimSpace(body), "\n")...)
	replacement = append(replacement, "")
	next := make([]string, 0, len(lines)-end+start+len(replacement))
	next = append(next, lines[:start]...)
	next = append(next, replacement...)
	next = append(next, lines[end:]...)
	return strings.TrimSpace(strings.Join(next, "\n")), true
}

func protocolSectionBodyByHeadingMarkers(protocol string, markers []string) (string, bool) {
	lines := strings.Split(protocol, "\n")
	start := -1
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "##") {
			continue
		}
		heading := strings.ToLower(strings.TrimSpace(strings.TrimLeft(trimmed, "#")))
		for _, marker := range markers {
			if strings.Contains(heading, strings.ToLower(marker)) {
				start = i + 1
				break
			}
		}
		if start >= 0 {
			break
		}
	}
	if start < 0 {
		return "", false
	}
	end := len(lines)
	for i := start; i < len(lines); i++ {
		if strings.HasPrefix(strings.TrimSpace(lines[i]), "## ") {
			end = i
			break
		}
	}
	return strings.TrimSpace(strings.Join(lines[start:end], "\n")), true
}

func EnsureProtocolTemplate(path string, opts ProtocolTemplateOptions) (bool, State, Event, error) {
	content, found, err := ReadProtocol(path)
	if err != nil {
		return false, State{}, Event{}, err
	}
	if found && strings.TrimSpace(content) != "" {
		state, stateFound, err := ReadState(filepath.Join(filepath.Dir(path), StateFileName))
		if err != nil {
			return false, State{}, Event{}, err
		}
		if stateFound {
			return false, state, Event{}, nil
		}
		return false, State{}, Event{}, nil
	}
	loopDir := filepath.Dir(path)
	loopID := strings.TrimSpace(opts.LoopID)
	if loopID == "" {
		loopID = filepath.Base(loopDir)
	}
	if opts.OwnerSession == "" {
		opts.OwnerSession = loopID
	}
	opts.LoopID = loopID
	now := opts.CreatedAt
	if now.IsZero() {
		now = time.Now().UTC()
	}
	opts.CreatedAt = now
	if err := WriteProtocol(path, DefaultProtocolTemplate(opts)); err != nil {
		return false, State{}, Event{}, err
	}
	status := templateStatus(opts.Status)
	if status == "" {
		status = "running"
	}
	event, err := AppendEvent(filepath.Join(loopDir, EventsFileName), Event{
		Type:           "loop.protocol_init",
		Summary:        "Initialized LOOP.md",
		Reason:         "loop protocol activation",
		Path:           ProtocolRelPath(loopID),
		Time:           formatTime(now),
		PlanLabel:      checkpointLabel(opts.Plan),
		PlanStepIndex:  checkpointStepIndex(opts.Plan),
		PlanStepStatus: checkpointStepStatus(opts.Plan),
		PlanStep:       checkpointStep(opts.Plan),
	})
	if err != nil {
		return false, State{}, Event{}, err
	}
	statePath := filepath.Join(loopDir, StateFileName)
	state, found, err := ReadState(statePath)
	if err != nil {
		return false, State{}, Event{}, err
	}
	state = normalizeStateForProtocol(state, found, loopID, now)
	state.OwnerSession = strings.TrimSpace(opts.OwnerSession)
	if state.OwnerSession == "" {
		state.OwnerSession = loopID
	}
	state.Status = status
	state.InitialGoalPreview = templateLine(opts.Goal)
	if opts.Plan.Valid {
		state.InitialPlanLabel = strings.TrimSpace(opts.Plan.Label)
		state.LastPlanLabel = strings.TrimSpace(opts.Plan.Label)
		state.LastPlanStepIndex = opts.Plan.StepIndex
		state.LastPlanStepStatus = strings.TrimSpace(opts.Plan.StepStatus)
		state.LastPlanStep = strings.TrimSpace(opts.Plan.Step)
	}
	state.LastProtocolUpdateAt = event.Time
	state.ProtocolUpdates++
	state.UpdatedAt = event.Time
	state.EventCount = event.Seq
	state.LastEventType = event.Type
	state.LastEventSummary = event.Summary
	state.LastEventAt = event.Time
	if err := WriteState(statePath, state); err != nil {
		return false, State{}, Event{}, err
	}
	return true, state, event, nil
}

func RecordProtocolActivation(protocolPath, reason string) (State, Event, error) {
	loopDir := filepath.Dir(protocolPath)
	loopID := filepath.Base(loopDir)
	now := time.Now().UTC()
	statePath := filepath.Join(loopDir, StateFileName)
	state, found, err := ReadState(statePath)
	if err != nil {
		return State{}, Event{}, err
	}
	state = normalizeStateForProtocol(state, found, loopID, now)
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "loop protocol completed"
	}
	event, err := AppendEvent(filepath.Join(loopDir, EventsFileName), Event{
		Type:    "loop.protocol_activate",
		Summary: "Activated LOOP.md",
		Reason:  reason,
		Path:    ProtocolRelPath(loopID),
		Time:    formatTime(now),
	})
	if err != nil {
		return State{}, Event{}, err
	}
	state.Status = "running"
	state.NeedsFullProtocolFeed = true
	state.LastProtocolUpdateAt = event.Time
	state.ProtocolUpdates++
	state.UpdatedAt = event.Time
	state.EventCount = event.Seq
	state.LastEventType = event.Type
	state.LastEventSummary = event.Summary
	state.LastEventAt = event.Time
	if err := WriteState(statePath, state); err != nil {
		return State{}, Event{}, err
	}
	return state, event, nil
}

func RecordProtocolStatus(protocolPath, status, reason string) (State, Event, error) {
	status = templateStatus(status)
	switch status {
	case "completed", "blocked", "paused", "stopping", "disabled":
	default:
		return State{}, Event{}, fmt.Errorf("unsupported loop protocol status %q", status)
	}
	protocol, found, err := ReadProtocol(protocolPath)
	if err != nil {
		return State{}, Event{}, err
	}
	if !found {
		return State{}, Event{}, errors.New("LOOP.md is not initialized")
	}
	next, ok := ProtocolWithStatus(protocol, status)
	if !ok {
		return State{}, Event{}, errors.New("LOOP.md metadata status could not be updated")
	}
	if err := WriteProtocol(protocolPath, next); err != nil {
		return State{}, Event{}, err
	}
	loopDir := filepath.Dir(protocolPath)
	loopID := filepath.Base(loopDir)
	now := time.Now().UTC()
	statePath := filepath.Join(loopDir, StateFileName)
	state, found, err := ReadState(statePath)
	if err != nil {
		return State{}, Event{}, err
	}
	state = normalizeStateForProtocol(state, found, loopID, now)
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "loop protocol status changed"
	}
	event, err := AppendEvent(filepath.Join(loopDir, EventsFileName), Event{
		Type:    "loop.protocol_status",
		Summary: "Updated LOOP.md status to " + status,
		Reason:  reason,
		Path:    ProtocolRelPath(loopID),
		Time:    formatTime(now),
	})
	if err != nil {
		return State{}, Event{}, err
	}
	state.Status = status
	state.NeedsFullProtocolFeed = false
	state.LastProtocolUpdateAt = event.Time
	state.ProtocolUpdates++
	state.UpdatedAt = event.Time
	state.EventCount = event.Seq
	state.LastEventType = event.Type
	state.LastEventSummary = event.Summary
	state.LastEventAt = event.Time
	if err := WriteState(statePath, state); err != nil {
		return State{}, Event{}, err
	}
	return state, event, nil
}

func RecordProtocolCalibrationQuestion(protocolPath, question string) (State, Event, error) {
	loopDir := filepath.Dir(protocolPath)
	loopID := filepath.Base(loopDir)
	now := time.Now().UTC()
	statePath := filepath.Join(loopDir, StateFileName)
	state, found, err := ReadState(statePath)
	if err != nil {
		return State{}, Event{}, err
	}
	state = normalizeStateForProtocol(state, found, loopID, now)
	preview := ProtocolCalibrationPreview(question)
	event, err := AppendEvent(filepath.Join(loopDir, EventsFileName), Event{
		Type:        "loop.protocol_calibration_request",
		Summary:     "Asked loop calibration question",
		Reason:      "assistant requested loop setup calibration",
		Path:        ProtocolRelPath(loopID),
		Time:        formatTime(now),
		Calibration: preview,
	})
	if err != nil {
		return State{}, Event{}, err
	}
	state.CalibrationQuestions++
	state.LastCalibrationQuestionAt = event.Time
	state.LastCalibrationQuestion = preview
	state.UpdatedAt = event.Time
	state.EventCount = event.Seq
	state.LastEventType = event.Type
	state.LastEventSummary = event.Summary
	state.LastEventAt = event.Time
	if err := WriteState(statePath, state); err != nil {
		return State{}, Event{}, err
	}
	return state, event, nil
}

func RecordProtocolCalibrationAnswer(protocolPath, answer string) (State, Event, error) {
	loopDir := filepath.Dir(protocolPath)
	loopID := filepath.Base(loopDir)
	now := time.Now().UTC()
	statePath := filepath.Join(loopDir, StateFileName)
	state, found, err := ReadState(statePath)
	if err != nil {
		return State{}, Event{}, err
	}
	state = normalizeStateForProtocol(state, found, loopID, now)
	preview := ProtocolCalibrationPreview(answer)
	event, err := AppendEvent(filepath.Join(loopDir, EventsFileName), Event{
		Type:        "loop.protocol_calibration",
		Summary:     "Recorded loop calibration answer",
		Reason:      "user answered loop setup calibration",
		Path:        ProtocolRelPath(loopID),
		Time:        formatTime(now),
		Calibration: preview,
	})
	if err != nil {
		return State{}, Event{}, err
	}
	state.CalibrationAnswers++
	state.LastCalibrationAnswerAt = event.Time
	state.LastCalibrationAnswer = preview
	state.UpdatedAt = event.Time
	state.EventCount = event.Seq
	state.LastEventType = event.Type
	state.LastEventSummary = event.Summary
	state.LastEventAt = event.Time
	if err := WriteState(statePath, state); err != nil {
		return State{}, Event{}, err
	}
	return state, event, nil
}

func RecordProtocolUpdate(protocolPath, reason string, sectionsChanged []string) (State, Event, error) {
	loopDir := filepath.Dir(protocolPath)
	loopID := filepath.Base(loopDir)
	now := time.Now().UTC()
	statePath := filepath.Join(loopDir, StateFileName)
	state, found, err := ReadState(statePath)
	if err != nil {
		return State{}, Event{}, err
	}
	state = normalizeStateForProtocol(state, found, loopID, now)
	status := ProtocolStatusFromFile(protocolPath)
	reason = strings.TrimSpace(reason)
	event, err := AppendEvent(filepath.Join(loopDir, EventsFileName), Event{
		Type:            "loop.protocol_update",
		Summary:         "Updated LOOP.md",
		Reason:          reason,
		Path:            ProtocolRelPath(loopID),
		Time:            formatTime(now),
		SectionsChanged: sanitizeEventSections(sectionsChanged),
	})
	if err != nil {
		return State{}, Event{}, err
	}
	if status != "" {
		state.Status = status
	}
	if status == "" || status == "running" {
		state.NeedsFullProtocolFeed = true
	}
	state.LastProtocolUpdateAt = event.Time
	state.ProtocolUpdates++
	state.UpdatedAt = event.Time
	state.EventCount = event.Seq
	state.LastEventType = event.Type
	state.LastEventSummary = event.Summary
	state.LastEventAt = event.Time
	if err := WriteState(statePath, state); err != nil {
		return State{}, Event{}, err
	}
	return state, event, nil
}

func ProtocolCalibrationPreview(answer string) string {
	answer = strings.Join(strings.Fields(strings.TrimSpace(answer)), " ")
	const max = 240
	if len([]byte(answer)) <= max {
		return answer
	}
	out := answer
	for len([]byte(out)) > max-3 {
		out = strings.TrimSpace(out[:len(out)-1])
	}
	return out + "..."
}

func ReadProtocol(path string) (string, bool, error) {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", false, nil
		}
		return "", false, err
	}
	if info.IsDir() {
		return "", false, errors.New("loop protocol path is a directory")
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", false, errors.New("loop protocol path must not be a symlink")
	}
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", false, nil
		}
		return "", false, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, MaxProtocolBytes+1))
	if err != nil {
		return "", false, err
	}
	if len(raw) > MaxProtocolBytes {
		return "", false, fmt.Errorf("loop protocol file exceeds %d bytes", MaxProtocolBytes)
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return "", false, nil
	}
	return string(raw), true, nil
}

func WriteProtocol(path, content string) error {
	content = strings.TrimSpace(content)
	if content == "" {
		return errors.New("loop protocol content is required")
	}
	if len([]byte(content)) > MaxProtocolBytes {
		return fmt.Errorf("loop protocol file exceeds %d bytes", MaxProtocolBytes)
	}
	if err := ValidateProtocolMaintenance(content); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if info, err := os.Lstat(path); err == nil {
		if info.IsDir() {
			return errors.New("loop protocol path is a directory")
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("loop protocol path must not be a symlink")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	tmp := path + ".tmp"
	if err := os.Remove(tmp); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write([]byte(content + "\n")); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if d, err := os.Open(filepath.Dir(path)); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

func RemoveProtocol(path string) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if info.IsDir() {
		return false, errors.New("loop protocol path is a directory")
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return false, errors.New("loop protocol path must not be a symlink")
	}
	if err := os.Remove(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	if d, err := os.Open(filepath.Dir(path)); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return true, nil
}

func SummarizeFile(path, relPath string) (Summary, bool, error) {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Summary{}, false, nil
		}
		return Summary{}, false, err
	}
	content, found, err := ReadProtocol(path)
	if err != nil {
		return Summary{}, false, err
	}
	if !found {
		return Summary{}, false, nil
	}
	summary := Summary{
		Path:      relPath,
		UpdatedAt: formatTime(info.ModTime()),
		Bytes:     len([]byte(content)),
		Preview:   textutil.Preview(content, 240),
	}
	for _, line := range strings.Split(content, "\n") {
		key, value, ok := parseMetadataLine(line)
		if !ok {
			continue
		}
		switch key {
		case "loop_id":
			summary.LoopID = value
		case "owner_session":
			summary.OwnerSession = value
		case "status":
			summary.Status = value
		}
	}
	if state, found, err := ReadState(filepath.Join(filepath.Dir(path), StateFileName)); err != nil {
		return Summary{}, false, err
	} else if found {
		summary.State = &state
		if state.LoopID != "" {
			summary.LoopID = state.LoopID
		}
		if state.OwnerSession != "" {
			summary.OwnerSession = state.OwnerSession
		}
		if state.Status != "" {
			summary.Status = state.Status
		}
		if state.UpdatedAt != "" {
			summary.UpdatedAt = state.UpdatedAt
		}
	}
	return summary, true, nil
}

func ProtocolStatus(content string) string {
	for _, line := range strings.Split(content, "\n") {
		key, value, ok := parseMetadataLine(line)
		if !ok || key != "status" {
			continue
		}
		return templateStatus(value)
	}
	return ""
}

func ProtocolWithStatus(content, status string) (string, bool) {
	status = templateStatus(status)
	if status == "" {
		return content, false
	}
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		key, _, ok := parseMetadataLine(line)
		if !ok || key != "status" {
			continue
		}
		indentLen := len(line) - len(strings.TrimLeft(line, " \t"))
		prefix := line[:indentLen]
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "-") {
			prefix += "- "
		}
		lines[i] = prefix + "status: " + status
		return strings.Join(lines, "\n"), true
	}
	return content, false
}

func ProtocolStatusFromFile(path string) string {
	content, found, err := ReadProtocol(path)
	if err != nil || !found {
		return ""
	}
	return ProtocolStatus(content)
}

func parseMetadataLine(line string) (string, string, bool) {
	line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "-"))
	key, value, ok := strings.Cut(line, ":")
	if !ok {
		return "", "", false
	}
	key = strings.ToLower(strings.TrimSpace(key))
	value = strings.TrimSpace(value)
	if key == "" || value == "" {
		return "", "", false
	}
	return key, value, true
}

func sanitizeEventSections(in []string) []string {
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, item := range in {
		item = templateLine(item)
		if item == "" || seen[item] {
			continue
		}
		seen[item] = true
		out = append(out, item)
		if len(out) >= 16 {
			break
		}
	}
	return out
}

func templateLine(s string) string {
	s = strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
	s = strings.ReplaceAll(s, "\x00", "")
	return textutil.Preview(s, 1600)
}

func templateStatus(s string) string {
	s = strings.ToLower(templateLine(s))
	switch s {
	case "draft", "running", "paused", "stopping", "completed", "blocked", "disabled":
		return s
	default:
		return ""
	}
}

func protocolTemplatePlanBlock(plan PlanCheckpoint) string {
	if !plan.Valid || strings.TrimSpace(plan.Label) == "" {
		return "- plan_label: not available at initialization\n- active_step: not available at initialization"
	}
	lines := []string{"- plan_label: " + templateLine(plan.Label)}
	if plan.StepIndex > 0 {
		lines = append(lines, fmt.Sprintf("- active_step_index: %d", plan.StepIndex))
	}
	if strings.TrimSpace(plan.StepStatus) != "" {
		lines = append(lines, "- active_step_status: "+templateLine(plan.StepStatus))
	}
	if strings.TrimSpace(plan.Step) != "" {
		lines = append(lines, "- active_step: "+templateLine(plan.Step))
	}
	return strings.Join(lines, "\n")
}

func protocolTemplateStopBlock(stop []string) string {
	candidates := append([]string{}, stop...)
	if len(candidates) == 0 {
		candidates = []string{
			"The objective is complete and evidence/artifacts are available.",
			"The loop is blocked on missing user input, credentials, permissions, budget, or external state.",
			"Repeated retries are no longer changing the failure mode.",
			"The task no longer serves the north star or has become unsafe.",
		}
	}
	var lines []string
	seen := map[string]bool{}
	for _, item := range candidates {
		item = templateLine(item)
		if item == "" || seen[item] {
			continue
		}
		seen[item] = true
		lines = append(lines, "- "+item)
		if len(lines) >= 8 {
			break
		}
	}
	if len(lines) == 0 {
		return "- Stop when the objective is complete, blocked, unsafe, or no longer useful."
	}
	return strings.Join(lines, "\n")
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}
