package loopstate

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	MaxStateBytes     = 16 * 1024
	MaxEventLineBytes = 32 * 1024
	maxDecisionText   = 1024
)

type State struct {
	Version                      int    `json:"version"`
	LoopID                       string `json:"loop_id,omitempty"`
	OwnerSession                 string `json:"owner_session,omitempty"`
	Status                       string `json:"status,omitempty"`
	ProtocolPath                 string `json:"protocol_path,omitempty"`
	CreatedAt                    string `json:"created_at,omitempty"`
	UpdatedAt                    string `json:"updated_at,omitempty"`
	InitialGoalPreview           string `json:"initial_goal_preview,omitempty"`
	InitialPlanLabel             string `json:"initial_plan_label,omitempty"`
	LastProtocolUpdateAt         string `json:"last_protocol_update_at,omitempty"`
	ProtocolUpdates              int    `json:"protocol_updates,omitempty"`
	CalibrationQuestions         int    `json:"calibration_questions,omitempty"`
	LastCalibrationQuestionAt    string `json:"last_calibration_question_at,omitempty"`
	LastCalibrationQuestion      string `json:"last_calibration_question_preview,omitempty"`
	CalibrationAnswers           int    `json:"calibration_answers,omitempty"`
	LastCalibrationAnswerAt      string `json:"last_calibration_answer_at,omitempty"`
	LastCalibrationAnswer        string `json:"last_calibration_answer_preview,omitempty"`
	ProtocolFeeds                int    `json:"protocol_feeds,omitempty"`
	LastProtocolFeedAt           string `json:"last_protocol_feed_at,omitempty"`
	LastProtocolFeedMode         string `json:"last_protocol_feed_mode,omitempty"`
	NeedsFullProtocolFeed        bool   `json:"needs_full_protocol_feed,omitempty"`
	LastPlanLabel                string `json:"last_plan_label,omitempty"`
	LastPlanStepIndex            int    `json:"last_plan_step_index,omitempty"`
	LastPlanStepStatus           string `json:"last_plan_step_status,omitempty"`
	LastPlanStep                 string `json:"last_plan_step,omitempty"`
	TurnCheckpoints              int    `json:"turn_checkpoints,omitempty"`
	LastTurnID                   string `json:"last_turn_id,omitempty"`
	LastTurnEndReason            string `json:"last_turn_end_reason,omitempty"`
	LastTurnAt                   string `json:"last_turn_at,omitempty"`
	LastTurnInputTokens          int    `json:"last_turn_input_tokens,omitempty"`
	LastTurnOutputTokens         int    `json:"last_turn_output_tokens,omitempty"`
	LastTurnToolRequests         int    `json:"last_turn_tool_requests,omitempty"`
	LastTurnToolRequestsAdmitted int    `json:"last_turn_tool_requests_admitted,omitempty"`
	LastTurnToolRequestsSkipped  int    `json:"last_turn_tool_requests_skipped,omitempty"`
	LastTurnToolErrors           int    `json:"last_turn_tool_errors,omitempty"`
	LastTurnLoopGuards           int    `json:"last_turn_loop_guards,omitempty"`
	LastTurnForcedNoTools        int    `json:"last_turn_forced_no_tools,omitempty"`
	LastTurnMemoryUpdates        int    `json:"last_turn_memory_updates,omitempty"`
	LastTurnMemorySearches       int    `json:"last_turn_memory_search_calls,omitempty"`
	LastTurnMemoryMisses         int    `json:"last_turn_memory_search_misses,omitempty"`
	LastTurnSessionSearch        int    `json:"last_turn_session_search_calls,omitempty"`
	MemoryUpdateEvents           int    `json:"memory_update_events,omitempty"`
	LastMemoryUpdateAction       string `json:"last_memory_update_action,omitempty"`
	LastMemoryUpdateTarget       string `json:"last_memory_update_target,omitempty"`
	LastMemoryUpdateTopic        string `json:"last_memory_update_topic,omitempty"`
	LastMemoryUpdateLoc          string `json:"last_memory_update_location,omitempty"`
	LastMemoryUpdatePrev         string `json:"last_memory_update_previous_preview,omitempty"`
	LastMemoryUpdateNext         string `json:"last_memory_update_next_preview,omitempty"`
	LastMemoryUpdate             string `json:"last_memory_update_preview,omitempty"`
	LastMemoryUpdateAt           string `json:"last_memory_update_at,omitempty"`
	LoopDecisions                int    `json:"loop_decisions,omitempty"`
	LastDecisionID               string `json:"last_decision_id,omitempty"`
	LastDecisionKind             string `json:"last_decision_kind,omitempty"`
	LastDecisionTrigger          string `json:"last_decision_trigger,omitempty"`
	LastDecision                 string `json:"last_decision,omitempty"`
	LastDecisionConfidence       string `json:"last_decision_confidence,omitempty"`
	LastDecisionReason           string `json:"last_decision_reason,omitempty"`
	LastDecisionAction           string `json:"last_decision_required_action,omitempty"`
	LastDecisionTokenBudget      int    `json:"last_decision_token_budget,omitempty"`
	LastDecisionObservedInput    int    `json:"last_decision_observed_input_tokens,omitempty"`
	LastDecisionProjectedInput   int    `json:"last_decision_projected_input_tokens,omitempty"`
	LastDecisionBudgetBytes      int    `json:"last_decision_budget_bytes,omitempty"`
	LastDecisionAt               string `json:"last_decision_at,omitempty"`
	ContextCompactions           int    `json:"context_compactions,omitempty"`
	LastCompactionAt             string `json:"last_context_compaction_at,omitempty"`
	LastCompactionReason         string `json:"last_context_compaction_reason,omitempty"`
	LastCompactionReactive       bool   `json:"last_context_compaction_reactive,omitempty"`
	EventCount                   int    `json:"event_count,omitempty"`
	LastEventType                string `json:"last_event_type,omitempty"`
	LastEventSummary             string `json:"last_event_summary,omitempty"`
	LastEventAt                  string `json:"last_event_at,omitempty"`
}

type Event struct {
	Seq                  int      `json:"seq"`
	Time                 string   `json:"time"`
	Type                 string   `json:"type"`
	Summary              string   `json:"summary,omitempty"`
	SectionsChanged      []string `json:"sections_changed,omitempty"`
	Reason               string   `json:"reason,omitempty"`
	Path                 string   `json:"path,omitempty"`
	Mode                 string   `json:"mode,omitempty"`
	Reactive             bool     `json:"reactive,omitempty"`
	FeedNumber           int      `json:"feed_number,omitempty"`
	PlanLabel            string   `json:"plan_label,omitempty"`
	PlanStepIndex        int      `json:"plan_step_index,omitempty"`
	PlanStepStatus       string   `json:"plan_step_status,omitempty"`
	PlanStep             string   `json:"plan_step,omitempty"`
	TurnID               string   `json:"turn_id,omitempty"`
	TurnEndReason        string   `json:"turn_end_reason,omitempty"`
	InputTokens          int      `json:"input_tokens,omitempty"`
	OutputTokens         int      `json:"output_tokens,omitempty"`
	ToolRequests         int      `json:"tool_requests,omitempty"`
	ToolRequestsAdmitted int      `json:"tool_requests_admitted,omitempty"`
	ToolRequestsSkipped  int      `json:"tool_requests_skipped,omitempty"`
	ToolErrors           int      `json:"tool_errors,omitempty"`
	LoopGuards           int      `json:"loop_guards,omitempty"`
	ForcedNoTools        int      `json:"forced_no_tools,omitempty"`
	MemoryUpdates        int      `json:"memory_updates,omitempty"`
	MemorySearches       int      `json:"memory_search_calls,omitempty"`
	MemoryMisses         int      `json:"memory_search_misses,omitempty"`
	SessionSearch        int      `json:"session_search_calls,omitempty"`
	DecisionID           string   `json:"decision_id,omitempty"`
	DecisionKind         string   `json:"decision_kind,omitempty"`
	Trigger              string   `json:"trigger,omitempty"`
	Decision             string   `json:"decision,omitempty"`
	Confidence           string   `json:"confidence,omitempty"`
	RequiredAction       string   `json:"required_action,omitempty"`
	TokenBudget          int      `json:"token_budget,omitempty"`
	ObservedInput        int      `json:"observed_input_tokens,omitempty"`
	ProjectedInput       int      `json:"projected_input_tokens,omitempty"`
	BudgetBytes          int      `json:"budget_bytes,omitempty"`
	CallID               string   `json:"call_id,omitempty"`
	MemoryAction         string   `json:"memory_action,omitempty"`
	MemoryTarget         string   `json:"memory_target,omitempty"`
	MemoryTopic          string   `json:"memory_topic,omitempty"`
	MemoryLocation       string   `json:"memory_location,omitempty"`
	MemoryPreview        string   `json:"memory_preview,omitempty"`
	PreviousPreview      string   `json:"previous_preview,omitempty"`
	NextPreview          string   `json:"next_preview,omitempty"`
	Calibration          string   `json:"calibration_preview,omitempty"`
}

type DecisionCheckpoint struct {
	DecisionID     string
	Kind           string
	Trigger        string
	Decision       string
	Confidence     string
	Reason         string
	RequiredAction string
	TokenBudget    int
	ObservedInput  int
	ProjectedInput int
	BudgetBytes    int
}

type TurnCheckpoint struct {
	TurnID               string
	EndReason            string
	InputTokens          int
	OutputTokens         int
	ToolRequests         int
	ToolRequestsAdmitted int
	ToolRequestsSkipped  int
	ToolErrors           int
	LoopGuards           int
	ForcedNoTools        int
	MemoryUpdates        int
	MemorySearchCalls    int
	MemorySearchMisses   int
	SessionSearchCalls   int
}

type MemoryUpdateCheckpoint struct {
	TurnID          string
	CallID          string
	Action          string
	Target          string
	Topic           string
	Location        string
	Preview         string
	PreviousPreview string
	NextPreview     string
}

func ReadState(path string) (State, bool, error) {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return State{}, false, nil
		}
		return State{}, false, err
	}
	if info.IsDir() {
		return State{}, false, errors.New("loop state path is a directory")
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return State{}, false, errors.New("loop state path must not be a symlink")
	}
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return State{}, false, nil
		}
		return State{}, false, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, MaxStateBytes+1))
	if err != nil {
		return State{}, false, err
	}
	if len(raw) > MaxStateBytes {
		return State{}, false, fmt.Errorf("loop state file exceeds %d bytes", MaxStateBytes)
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return State{}, false, nil
	}
	var state State
	if err := json.Unmarshal(raw, &state); err != nil {
		return State{}, false, err
	}
	return state, true, nil
}

func WriteState(path string, state State) error {
	if state.Version == 0 {
		state.Version = 1
	}
	raw, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	if len(raw) > MaxStateBytes {
		return fmt.Errorf("loop state file exceeds %d bytes", MaxStateBytes)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if info, err := os.Lstat(path); err == nil {
		if info.IsDir() {
			return errors.New("loop state path is a directory")
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("loop state path must not be a symlink")
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
	if _, err := f.Write(append(raw, '\n')); err != nil {
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
	syncDir(filepath.Dir(path))
	return nil
}

type PlanCheckpoint struct {
	Valid      bool
	Label      string
	StepIndex  int
	StepStatus string
	Step       string
}

func RecordProtocolFeed(protocolPath, mode string) (State, Event, error) {
	return RecordProtocolFeedWithCheckpoint(protocolPath, mode, PlanCheckpoint{})
}

func RecordProtocolFeedWithCheckpoint(protocolPath, mode string, checkpoint PlanCheckpoint) (State, Event, error) {
	mode = strings.TrimSpace(mode)
	if mode == "" {
		mode = "digest"
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
	feedNumber := state.ProtocolFeeds + 1
	event, err := AppendEvent(filepath.Join(loopDir, EventsFileName), Event{
		Type:           "loop.protocol_feed",
		Summary:        "Fed LOOP.md " + mode,
		Reason:         "loop protocol feed policy",
		Path:           ProtocolRelPath(loopID),
		Mode:           mode,
		FeedNumber:     feedNumber,
		Time:           formatTime(now),
		PlanLabel:      checkpointLabel(checkpoint),
		PlanStepIndex:  checkpointStepIndex(checkpoint),
		PlanStepStatus: checkpointStepStatus(checkpoint),
		PlanStep:       checkpointStep(checkpoint),
	})
	if err != nil {
		return State{}, Event{}, err
	}
	state.ProtocolFeeds = feedNumber
	state.LastProtocolFeedAt = event.Time
	state.LastProtocolFeedMode = mode
	if mode == "full" {
		state.NeedsFullProtocolFeed = false
	}
	if checkpoint.Valid {
		state.LastPlanLabel = strings.TrimSpace(checkpoint.Label)
		state.LastPlanStepIndex = checkpoint.StepIndex
		state.LastPlanStepStatus = strings.TrimSpace(checkpoint.StepStatus)
		state.LastPlanStep = strings.TrimSpace(checkpoint.Step)
	}
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

func RecordContextCompaction(protocolPath, reason string, reactive bool) (State, Event, error) {
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
		reason = "context_compaction"
	}
	event, err := AppendEvent(filepath.Join(loopDir, EventsFileName), Event{
		Type:     "context.compacted",
		Summary:  "Context compacted; force next LOOP.md full feed",
		Reason:   reason,
		Path:     ProtocolRelPath(loopID),
		Time:     formatTime(now),
		Reactive: reactive,
	})
	if err != nil {
		return State{}, Event{}, err
	}
	state.ContextCompactions++
	state.LastCompactionAt = event.Time
	state.LastCompactionReason = reason
	state.LastCompactionReactive = reactive
	state.NeedsFullProtocolFeed = true
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

func RecordTurnCheckpoint(protocolPath string, checkpoint TurnCheckpoint) (State, Event, error) {
	loopDir := filepath.Dir(protocolPath)
	loopID := filepath.Base(loopDir)
	now := time.Now().UTC()
	statePath := filepath.Join(loopDir, StateFileName)
	state, found, err := ReadState(statePath)
	if err != nil {
		return State{}, Event{}, err
	}
	state = normalizeStateForProtocol(state, found, loopID, now)
	checkpoint.TurnID = strings.TrimSpace(checkpoint.TurnID)
	checkpoint.EndReason = strings.TrimSpace(checkpoint.EndReason)
	if checkpoint.EndReason == "" {
		checkpoint.EndReason = "unknown"
	}
	toolRequests, toolRequestsAdmitted, toolRequestsSkipped := normalizeToolRequestCounts(
		checkpoint.ToolRequests,
		checkpoint.ToolRequestsAdmitted,
		checkpoint.ToolRequestsSkipped,
	)
	event, err := AppendEvent(filepath.Join(loopDir, EventsFileName), Event{
		Type:                 "loop.turn_checkpoint",
		Summary:              "Turn ended: " + checkpoint.EndReason,
		Reason:               "turn_end",
		Path:                 ProtocolRelPath(loopID),
		Time:                 formatTime(now),
		TurnID:               checkpoint.TurnID,
		TurnEndReason:        checkpoint.EndReason,
		InputTokens:          clampNonNegative(checkpoint.InputTokens),
		OutputTokens:         clampNonNegative(checkpoint.OutputTokens),
		ToolRequests:         toolRequests,
		ToolRequestsAdmitted: toolRequestsAdmitted,
		ToolRequestsSkipped:  toolRequestsSkipped,
		ToolErrors:           clampNonNegative(checkpoint.ToolErrors),
		LoopGuards:           clampNonNegative(checkpoint.LoopGuards),
		ForcedNoTools:        clampNonNegative(checkpoint.ForcedNoTools),
		MemoryUpdates:        clampNonNegative(checkpoint.MemoryUpdates),
		MemorySearches:       clampNonNegative(checkpoint.MemorySearchCalls),
		MemoryMisses:         clampNonNegative(checkpoint.MemorySearchMisses),
		SessionSearch:        clampNonNegative(checkpoint.SessionSearchCalls),
	})
	if err != nil {
		return State{}, Event{}, err
	}
	state.TurnCheckpoints++
	state.LastTurnID = checkpoint.TurnID
	state.LastTurnEndReason = checkpoint.EndReason
	state.LastTurnAt = event.Time
	state.LastTurnInputTokens = event.InputTokens
	state.LastTurnOutputTokens = event.OutputTokens
	state.LastTurnToolRequests = event.ToolRequests
	state.LastTurnToolRequestsAdmitted = event.ToolRequestsAdmitted
	state.LastTurnToolRequestsSkipped = event.ToolRequestsSkipped
	state.LastTurnToolErrors = event.ToolErrors
	state.LastTurnLoopGuards = event.LoopGuards
	state.LastTurnForcedNoTools = event.ForcedNoTools
	state.LastTurnMemoryUpdates = event.MemoryUpdates
	state.LastTurnMemorySearches = event.MemorySearches
	state.LastTurnMemoryMisses = event.MemoryMisses
	state.LastTurnSessionSearch = event.SessionSearch
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

func normalizeToolRequestCounts(total, admitted, skipped int) (int, int, int) {
	total = clampNonNegative(total)
	admitted = clampNonNegative(admitted)
	skipped = clampNonNegative(skipped)
	if admitted == 0 && skipped == 0 && total > 0 {
		admitted = total
	}
	if sum := admitted + skipped; sum > total {
		total = sum
	}
	return total, admitted, skipped
}

func RecordMemoryUpdate(protocolPath string, checkpoint MemoryUpdateCheckpoint) (State, Event, error) {
	loopDir := filepath.Dir(protocolPath)
	loopID := filepath.Base(loopDir)
	now := time.Now().UTC()
	statePath := filepath.Join(loopDir, StateFileName)
	state, found, err := ReadState(statePath)
	if err != nil {
		return State{}, Event{}, err
	}
	state = normalizeStateForProtocol(state, found, loopID, now)
	checkpoint = normalizeMemoryUpdateCheckpoint(checkpoint)
	if checkpoint.Action == "" || checkpoint.Location == "" {
		return State{}, Event{}, errors.New("memory update requires action and location")
	}
	event, err := AppendEvent(filepath.Join(loopDir, EventsFileName), Event{
		Type:            "loop.memory_update",
		Summary:         "Memory " + checkpoint.Action + ": " + checkpoint.Location,
		Reason:          "memory_tool_update",
		Path:            ProtocolRelPath(loopID),
		Time:            formatTime(now),
		TurnID:          checkpoint.TurnID,
		CallID:          checkpoint.CallID,
		MemoryAction:    checkpoint.Action,
		MemoryTarget:    checkpoint.Target,
		MemoryTopic:     checkpoint.Topic,
		MemoryLocation:  checkpoint.Location,
		MemoryPreview:   checkpoint.Preview,
		PreviousPreview: checkpoint.PreviousPreview,
		NextPreview:     checkpoint.NextPreview,
	})
	if err != nil {
		return State{}, Event{}, err
	}
	state.MemoryUpdateEvents++
	state.LastMemoryUpdateAction = checkpoint.Action
	state.LastMemoryUpdateTarget = checkpoint.Target
	state.LastMemoryUpdateTopic = checkpoint.Topic
	state.LastMemoryUpdateLoc = checkpoint.Location
	state.LastMemoryUpdatePrev = checkpoint.PreviousPreview
	state.LastMemoryUpdateNext = checkpoint.NextPreview
	state.LastMemoryUpdate = checkpoint.Preview
	state.LastMemoryUpdateAt = event.Time
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

func RecordDecision(protocolPath string, checkpoint DecisionCheckpoint) (State, Event, error) {
	loopDir := filepath.Dir(protocolPath)
	loopID := filepath.Base(loopDir)
	now := time.Now().UTC()
	statePath := filepath.Join(loopDir, StateFileName)
	state, found, err := ReadState(statePath)
	if err != nil {
		return State{}, Event{}, err
	}
	state = normalizeStateForProtocol(state, found, loopID, now)
	checkpoint = normalizeDecisionCheckpoint(checkpoint)
	if checkpoint.Kind == "" || checkpoint.Decision == "" {
		return State{}, Event{}, errors.New("loop decision requires kind and decision")
	}
	event, err := AppendEvent(filepath.Join(loopDir, EventsFileName), Event{
		Type:           "loop.decision",
		Summary:        "Decision: " + checkpoint.Kind + "=" + checkpoint.Decision,
		Reason:         checkpoint.Reason,
		Path:           ProtocolRelPath(loopID),
		Time:           formatTime(now),
		DecisionID:     checkpoint.DecisionID,
		DecisionKind:   checkpoint.Kind,
		Trigger:        checkpoint.Trigger,
		Decision:       checkpoint.Decision,
		Confidence:     checkpoint.Confidence,
		RequiredAction: checkpoint.RequiredAction,
		TokenBudget:    checkpoint.TokenBudget,
		ObservedInput:  checkpoint.ObservedInput,
		ProjectedInput: checkpoint.ProjectedInput,
		BudgetBytes:    checkpoint.BudgetBytes,
	})
	if err != nil {
		return State{}, Event{}, err
	}
	state.LoopDecisions++
	state.LastDecisionID = checkpoint.DecisionID
	state.LastDecisionKind = checkpoint.Kind
	state.LastDecisionTrigger = checkpoint.Trigger
	state.LastDecision = checkpoint.Decision
	state.LastDecisionConfidence = checkpoint.Confidence
	state.LastDecisionReason = checkpoint.Reason
	state.LastDecisionAction = checkpoint.RequiredAction
	state.LastDecisionTokenBudget = checkpoint.TokenBudget
	state.LastDecisionObservedInput = checkpoint.ObservedInput
	state.LastDecisionProjectedInput = checkpoint.ProjectedInput
	state.LastDecisionBudgetBytes = checkpoint.BudgetBytes
	state.LastDecisionAt = event.Time
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

func normalizeStateForProtocol(state State, found bool, loopID string, now time.Time) State {
	if !found {
		state = State{
			Version:      1,
			LoopID:       loopID,
			OwnerSession: loopID,
			Status:       "running",
			ProtocolPath: ProtocolRelPath(loopID),
			CreatedAt:    formatTime(now),
		}
	}
	if state.Version == 0 {
		state.Version = 1
	}
	if state.LoopID == "" {
		state.LoopID = loopID
	}
	if state.OwnerSession == "" {
		state.OwnerSession = loopID
	}
	if state.Status == "" {
		state.Status = "running"
	}
	if state.ProtocolPath == "" {
		state.ProtocolPath = ProtocolRelPath(loopID)
	}
	return state
}

func checkpointLabel(checkpoint PlanCheckpoint) string {
	if !checkpoint.Valid {
		return ""
	}
	return strings.TrimSpace(checkpoint.Label)
}

func checkpointStepIndex(checkpoint PlanCheckpoint) int {
	if !checkpoint.Valid {
		return 0
	}
	return checkpoint.StepIndex
}

func checkpointStepStatus(checkpoint PlanCheckpoint) string {
	if !checkpoint.Valid {
		return ""
	}
	return strings.TrimSpace(checkpoint.StepStatus)
}

func checkpointStep(checkpoint PlanCheckpoint) string {
	if !checkpoint.Valid {
		return ""
	}
	return strings.TrimSpace(checkpoint.Step)
}

func clampNonNegative(n int) int {
	if n < 0 {
		return 0
	}
	return n
}

func normalizeDecisionCheckpoint(in DecisionCheckpoint) DecisionCheckpoint {
	return DecisionCheckpoint{
		DecisionID:     trimEventText(in.DecisionID, 160),
		Kind:           trimEventText(in.Kind, 160),
		Trigger:        trimEventText(in.Trigger, 240),
		Decision:       trimEventText(in.Decision, 160),
		Confidence:     trimEventText(in.Confidence, 80),
		Reason:         trimEventText(in.Reason, maxDecisionText),
		RequiredAction: trimEventText(in.RequiredAction, maxDecisionText),
		TokenBudget:    clampNonNegative(in.TokenBudget),
		ObservedInput:  clampNonNegative(in.ObservedInput),
		ProjectedInput: clampNonNegative(in.ProjectedInput),
		BudgetBytes:    clampNonNegative(in.BudgetBytes),
	}
}

func normalizeMemoryUpdateCheckpoint(in MemoryUpdateCheckpoint) MemoryUpdateCheckpoint {
	return MemoryUpdateCheckpoint{
		TurnID:          trimEventText(in.TurnID, 160),
		CallID:          trimEventText(in.CallID, 160),
		Action:          trimEventText(in.Action, 80),
		Target:          trimEventText(in.Target, 80),
		Topic:           trimEventText(in.Topic, 160),
		Location:        trimEventText(in.Location, 240),
		Preview:         trimEventText(in.Preview, maxDecisionText),
		PreviousPreview: trimEventText(in.PreviousPreview, maxDecisionText),
		NextPreview:     trimEventText(in.NextPreview, maxDecisionText),
	}
}

func trimEventText(s string, maxBytes int) string {
	s = strings.TrimSpace(s)
	if maxBytes <= 0 || len([]byte(s)) <= maxBytes {
		return s
	}
	out := make([]rune, 0, maxBytes)
	bytes := 0
	for _, r := range s {
		n := len(string(r))
		if bytes+n > maxBytes {
			break
		}
		out = append(out, r)
		bytes += n
	}
	return strings.TrimSpace(string(out))
}

func AppendEvent(path string, ev Event) (Event, error) {
	if strings.TrimSpace(ev.Type) == "" {
		return Event{}, errors.New("loop event type is required")
	}
	count, err := CountEvents(path)
	if err != nil {
		return Event{}, err
	}
	if ev.Seq == 0 {
		ev.Seq = count + 1
	}
	if ev.Time == "" {
		ev.Time = formatTime(time.Now())
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return Event{}, err
	}
	if info, err := os.Lstat(path); err == nil {
		if info.IsDir() {
			return Event{}, errors.New("loop events path is a directory")
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return Event{}, errors.New("loop events path must not be a symlink")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return Event{}, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return Event{}, err
	}
	enc := json.NewEncoder(f)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(ev); err != nil {
		_ = f.Close()
		return Event{}, err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return Event{}, err
	}
	if err := f.Close(); err != nil {
		return Event{}, err
	}
	syncDir(filepath.Dir(path))
	return ev, nil
}

func CountEvents(path string) (int, error) {
	events, count, _, err := readEvents(path, 0)
	_ = events
	return count, err
}

func ReadRecentEvents(path string, limit int) ([]Event, bool, error) {
	events, _, found, err := readEvents(path, limit)
	return events, found, err
}

func readEvents(path string, limit int) ([]Event, int, bool, error) {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, 0, false, nil
		}
		return nil, 0, false, err
	}
	if info.IsDir() {
		return nil, 0, false, errors.New("loop events path is a directory")
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, 0, false, errors.New("loop events path must not be a symlink")
	}
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, 0, false, nil
		}
		return nil, 0, false, err
	}
	defer f.Close()
	reader := bufio.NewReaderSize(f, 64*1024)
	var out []Event
	count := 0
	for {
		line, err := readBoundedLine(reader, MaxEventLineBytes)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, 0, true, err
		}
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var ev Event
		if err := json.Unmarshal(line, &ev); err != nil {
			return nil, 0, true, err
		}
		count++
		if limit <= 0 {
			continue
		}
		if len(out) < limit {
			out = append(out, ev)
		} else {
			copy(out, out[1:])
			out[len(out)-1] = ev
		}
	}
	return out, count, true, nil
}

func readBoundedLine(r *bufio.Reader, max int) ([]byte, error) {
	if max <= 0 {
		max = MaxEventLineBytes
	}
	var out []byte
	for {
		part, isPrefix, err := r.ReadLine()
		if err != nil {
			if errors.Is(err, io.EOF) && len(out) > 0 {
				return out, nil
			}
			return nil, err
		}
		if len(out)+len(part) > max {
			return nil, fmt.Errorf("line exceeds %d bytes", max)
		}
		out = append(out, part...)
		if !isPrefix {
			return out, nil
		}
	}
}

func syncDir(path string) {
	if d, err := os.Open(path); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
}
