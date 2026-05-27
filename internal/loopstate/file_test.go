package loopstate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProtocolPathUsesPerSessionLoopDir(t *testing.T) {
	dir := t.TempDir()
	got := ProtocolPath(dir, "market-run")
	want := filepath.Join(dir, ".affent", "loops", "market-run", "LOOP.md")
	if got != want {
		t.Fatalf("ProtocolPath = %q, want %q", got, want)
	}
	if rel := ProtocolRelPath("market-run"); rel != ".affent/loops/market-run/LOOP.md" {
		t.Fatalf("ProtocolRelPath = %q", rel)
	}
}

func TestReadProtocolRejectsSymlinkAndOversize(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(dir, "outside.md")
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "LOOP.md")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReadProtocol(link); err == nil || !strings.Contains(err.Error(), "must not be a symlink") {
		t.Fatalf("ReadProtocol symlink err = %v", err)
	}

	oversize := filepath.Join(dir, "oversize.md")
	if err := os.WriteFile(oversize, []byte(strings.Repeat("x", MaxProtocolBytes+1)), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReadProtocol(oversize); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("ReadProtocol oversize err = %v", err)
	}
}

func TestWriteProtocolPersistsAtomicallyAndRejectsUnsafeTargets(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".affent", "loops", "alpha", "LOOP.md")
	if err := WriteProtocol(path, "  # Loop\n\nstatus: running  "); err != nil {
		t.Fatalf("WriteProtocol: %v", err)
	}
	got, found, err := ReadProtocol(path)
	if err != nil || !found || got != "# Loop\n\nstatus: running" {
		t.Fatalf("ReadProtocol = %q found=%v err=%v", got, found, err)
	}
	if _, err := os.Lstat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("temp file err = %v, want not exists", err)
	}
	if err := WriteProtocol(path, "updated"); err != nil {
		t.Fatalf("overwrite WriteProtocol: %v", err)
	}
	got, found, err = ReadProtocol(path)
	if err != nil || !found || got != "updated" {
		t.Fatalf("updated ReadProtocol = %q found=%v err=%v", got, found, err)
	}

	if err := WriteProtocol(filepath.Join(dir, "blank.md"), " \n\t "); err == nil || !strings.Contains(err.Error(), "content is required") {
		t.Fatalf("blank WriteProtocol err = %v", err)
	}
	if err := WriteProtocol(filepath.Join(dir, "too-big.md"), strings.Repeat("x", MaxProtocolBytes+1)); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversize WriteProtocol err = %v", err)
	}

	outside := filepath.Join(dir, "outside.md")
	if err := os.WriteFile(outside, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.md")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if err := WriteProtocol(link, "new"); err == nil || !strings.Contains(err.Error(), "must not be a symlink") {
		t.Fatalf("symlink WriteProtocol err = %v", err)
	}
	raw, err := os.ReadFile(outside)
	if err != nil || string(raw) != "outside" {
		t.Fatalf("outside content = %q err=%v", string(raw), err)
	}
}

func TestWriteProtocolRejectsOversizedCurrentSituation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "LOOP.md")
	protocol := "# Loop\n\n## Current Situation\n\n" + strings.Repeat("evidence ", MaxCurrentSituationChars/len("evidence ")+20)
	err := WriteProtocol(path, protocol)
	if err == nil ||
		!strings.Contains(err.Error(), "Current Situation") ||
		!strings.Contains(err.Error(), "1200") {
		t.Fatalf("oversized current situation WriteProtocol err = %v", err)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("oversized current situation should not be written, stat err=%v", err)
	}
}

func TestRemoveProtocolRejectsSymlinkAndRemovesRegularFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "LOOP.md")
	if err := WriteProtocol(path, "protocol"); err != nil {
		t.Fatal(err)
	}
	removed, err := RemoveProtocol(path)
	if err != nil || !removed {
		t.Fatalf("RemoveProtocol = removed %v err %v", removed, err)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("protocol still exists err=%v", err)
	}
	removed, err = RemoveProtocol(path)
	if err != nil || removed {
		t.Fatalf("second RemoveProtocol = removed %v err %v", removed, err)
	}

	outside := filepath.Join(dir, "outside.md")
	if err := os.WriteFile(outside, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.md")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if removed, err := RemoveProtocol(link); err == nil || removed || !strings.Contains(err.Error(), "must not be a symlink") {
		t.Fatalf("RemoveProtocol symlink = removed %v err %v", removed, err)
	}
	if _, err := os.Lstat(outside); err != nil {
		t.Fatalf("outside should remain: %v", err)
	}
}

func TestSummarizeFileExtractsMetadata(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "LOOP.md")
	content := `# Loop Protocol: market

## 0. Metadata

- loop_id: market-run
- owner_session: sess-market
- status: running

## 1. North Star

Keep market evidence cited.`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	got, found, err := SummarizeFile(path, ".affent/loops/market-run/LOOP.md")
	if err != nil {
		t.Fatalf("SummarizeFile: %v", err)
	}
	if !found {
		t.Fatal("expected summary")
	}
	if got.Path != ".affent/loops/market-run/LOOP.md" ||
		got.LoopID != "market-run" ||
		got.OwnerSession != "sess-market" ||
		got.Status != "running" ||
		got.UpdatedAt == "" ||
		got.Bytes != len([]byte(content)) ||
		!strings.Contains(got.Preview, "Keep market evidence cited.") {
		t.Fatalf("summary = %+v", got)
	}
}

func TestEnsureProtocolTemplateCreatesPerSessionLoopProtocol(t *testing.T) {
	dir := t.TempDir()
	path := ProtocolPath(dir, "longrun")
	created, state, event, err := EnsureProtocolTemplate(path, ProtocolTemplateOptions{
		LoopID:       "longrun",
		OwnerSession: "session-a",
		Goal:         "Analyze a JS-heavy market dashboard with durable evidence.",
		Workspace:    "/workspace/affent",
		Status:       "draft",
		Plan: PlanCheckpoint{
			Valid:      true,
			Label:      "plan:1/3:active",
			StepIndex:  2,
			StepStatus: "in_progress",
			Step:       "inspect rendered browser evidence",
		},
	})
	if err != nil {
		t.Fatalf("EnsureProtocolTemplate: %v", err)
	}
	if !created {
		t.Fatal("expected protocol to be created")
	}
	if state.LoopID != "longrun" || state.OwnerSession != "session-a" || state.Status != "draft" || state.ProtocolUpdates != 1 || state.LastEventType != "loop.protocol_init" ||
		state.InitialGoalPreview != "Analyze a JS-heavy market dashboard with durable evidence." || state.InitialPlanLabel != "plan:1/3:active" ||
		state.LastPlanStep != "inspect rendered browser evidence" {
		t.Fatalf("state = %+v", state)
	}
	if event.Type != "loop.protocol_init" || event.Path != ProtocolRelPath("longrun") || event.PlanLabel != "plan:1/3:active" || event.PlanStepIndex != 2 {
		t.Fatalf("event = %+v", event)
	}
	content, found, err := ReadProtocol(path)
	if err != nil || !found {
		t.Fatalf("ReadProtocol found=%v err=%v", found, err)
	}
	for _, want := range []string{
		"# Loop Protocol: longrun",
		"- loop_id: longrun",
		"- status: draft",
		"- owner_session: session-a",
		"Analyze a JS-heavy market dashboard with durable evidence.",
		"plan/step state remains authoritative",
		"Operational stop conditions:",
		"- plan_label: plan:1/3:active",
		"- active_step: inspect rendered browser evidence",
		"state.json and events.jsonl",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("template missing %q:\n%s", want, content)
		}
	}
	events, found, err := ReadRecentEvents(EventsPath(dir, "longrun"), 5)
	if err != nil || !found || len(events) != 1 {
		t.Fatalf("ReadRecentEvents found=%v len=%d err=%v", found, len(events), err)
	}
	if events[0].Type != "loop.protocol_init" {
		t.Fatalf("events[0] = %+v", events[0])
	}

	state, event, err = RecordProtocolActivation(path, "agent supplemented protocol")
	if err != nil {
		t.Fatalf("RecordProtocolActivation: %v", err)
	}
	if state.Status != "running" || !state.NeedsFullProtocolFeed || state.LastEventType != "loop.protocol_activate" || event.Type != "loop.protocol_activate" || event.Reason != "agent supplemented protocol" {
		t.Fatalf("activated state=%+v event=%+v", state, event)
	}

	created, state, event, err = EnsureProtocolTemplate(path, ProtocolTemplateOptions{LoopID: "longrun", OwnerSession: "other"})
	if err != nil {
		t.Fatalf("second EnsureProtocolTemplate: %v", err)
	}
	if created || event.Type != "" || state.OwnerSession != "session-a" {
		t.Fatalf("second ensure should not overwrite existing protocol: created=%v state=%+v event=%+v", created, state, event)
	}
}

func TestRecordProtocolUpdateForcesFullFeedForRunningProtocol(t *testing.T) {
	dir := t.TempDir()
	path := ProtocolPath(dir, "running-update")
	if err := WriteProtocol(path, "# Loop Protocol\n\n## 0. Metadata\n\n- status: running\n\n## 1. North Star\n\nKeep new rules visible."); err != nil {
		t.Fatal(err)
	}
	if _, _, err := RecordProtocolFeed(path, "full"); err != nil {
		t.Fatalf("RecordProtocolFeed: %v", err)
	}
	if _, _, err := RecordProtocolFeed(path, "digest"); err != nil {
		t.Fatalf("RecordProtocolFeed: %v", err)
	}
	state, event, err := RecordProtocolUpdate(path, "rules changed", []string{"Rules"})
	if err != nil {
		t.Fatalf("RecordProtocolUpdate: %v", err)
	}
	if !state.NeedsFullProtocolFeed || state.LastEventType != "loop.protocol_update" || event.Type != "loop.protocol_update" {
		t.Fatalf("running protocol update should force next full feed: state=%+v event=%+v", state, event)
	}
	state, event, err = RecordProtocolFeed(path, "full")
	if err != nil {
		t.Fatalf("RecordProtocolFeed after update: %v", err)
	}
	if state.NeedsFullProtocolFeed || state.LastProtocolFeedMode != "full" || event.FeedNumber != 3 {
		t.Fatalf("full feed should clear force flag: state=%+v event=%+v", state, event)
	}
}

func TestValidateProtocolActivationRejectsUnresolvedTemplatePlaceholders(t *testing.T) {
	protocol := strings.Replace(DefaultProtocolTemplate(ProtocolTemplateOptions{
		LoopID:       "longrun",
		OwnerSession: "longrun",
		Goal:         "Run a long market analysis without losing recovery context.",
		Status:       "running",
	}), "- current risk or blocker:", "- current risk or blocker: needs live source verification", 1)

	err := ValidateProtocolActivation(protocol)
	if err == nil ||
		!strings.Contains(err.Error(), "unresolved activation placeholder") ||
		!strings.Contains(err.Error(), "hard constraints") {
		t.Fatalf("ValidateProtocolActivation err = %v", err)
	}

	for _, replacement := range [][2]string{
		{"- hard constraints:", "- hard constraints: keep evidence cited"},
		{"- known evidence:", "- known evidence: user confirmed long-run market objective"},
		{"- important artifacts:", "- important artifacts: none yet"},
		{"- important trace spans:", "- important trace spans: loop activation draft"},
		{"- last known recovery note:", "- last known recovery note: reload LOOP.md and plan state"},
	} {
		protocol = strings.Replace(protocol, replacement[0], replacement[1], 1)
	}
	if err := ValidateProtocolActivation(protocol); err != nil {
		t.Fatalf("ValidateProtocolActivation supplemented protocol err = %v", err)
	}
}

func TestValidateProtocolActivationRejectsOversizedCurrentSituation(t *testing.T) {
	protocol := DefaultProtocolTemplate(ProtocolTemplateOptions{
		LoopID:       "longrun",
		OwnerSession: "longrun",
		Goal:         "Run a long market analysis without losing recovery context.",
		Status:       "running",
	})
	for _, replacement := range [][2]string{
		{"- hard constraints:", "- hard constraints: keep evidence cited"},
		{"- known evidence:", "- known evidence: user confirmed long-run market objective"},
		{"- current risk or blocker:", "- current risk or blocker: live web evidence can be stale"},
		{"- important artifacts:", "- important artifacts: none yet"},
		{"- important trace spans:", "- important trace spans: loop activation draft"},
		{"- last known recovery note:", "- last known recovery note: reload LOOP.md and plan state"},
	} {
		protocol = strings.Replace(protocol, replacement[0], replacement[1], 1)
	}
	protocol = strings.Replace(
		protocol,
		"- next recovery anchor: check plan state, recent trace, memory search/list, and this protocol before continuing",
		"- next recovery anchor: check plan state, recent trace, memory search/list, and this protocol before continuing\n- overflow: "+strings.Repeat("evidence ", MaxCurrentSituationChars/len("evidence ")+20),
		1,
	)

	err := ValidateProtocolActivation(protocol)
	if err == nil ||
		!strings.Contains(err.Error(), "Current Situation") ||
		!strings.Contains(err.Error(), "1200") {
		t.Fatalf("ValidateProtocolActivation oversized current situation err = %v", err)
	}
}

func TestRecordProtocolCalibrationQuestionAndAnswer(t *testing.T) {
	dir := t.TempDir()
	path := ProtocolPath(dir, "longrun")
	if _, _, _, err := EnsureProtocolTemplate(path, ProtocolTemplateOptions{
		LoopID:       "longrun",
		OwnerSession: "session-a",
		Goal:         "Run a durable loop setup.",
		Status:       "draft",
	}); err != nil {
		t.Fatalf("EnsureProtocolTemplate: %v", err)
	}

	state, event, err := RecordProtocolCalibrationQuestion(path, "What stop condition should pause this loop?")
	if err != nil {
		t.Fatalf("RecordProtocolCalibrationQuestion: %v", err)
	}
	if state.CalibrationQuestions != 1 ||
		state.LastEventType != "loop.protocol_calibration_request" ||
		!strings.Contains(state.LastCalibrationQuestion, "stop condition") ||
		event.Type != "loop.protocol_calibration_request" ||
		event.Calibration != state.LastCalibrationQuestion {
		t.Fatalf("question state=%+v event=%+v", state, event)
	}

	state, event, err = RecordProtocolCalibrationAnswer(path, "Pause if source quality is weak.")
	if err != nil {
		t.Fatalf("RecordProtocolCalibrationAnswer: %v", err)
	}
	if state.CalibrationQuestions != 1 ||
		state.CalibrationAnswers != 1 ||
		state.LastEventType != "loop.protocol_calibration" ||
		!strings.Contains(state.LastCalibrationAnswer, "source quality") ||
		event.Type != "loop.protocol_calibration" {
		t.Fatalf("answer state=%+v event=%+v", state, event)
	}
}

func TestStatePersistsAtomicallyAndSummaryPrefersState(t *testing.T) {
	dir := t.TempDir()
	loopDir := ProtocolDir(dir, "market-run")
	protocolPath := filepath.Join(loopDir, ProtocolFileName)
	if err := WriteProtocol(protocolPath, `# Loop

- loop_id: from-markdown
- owner_session: from-markdown
- status: draft`); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(loopDir, StateFileName)
	state := State{
		Version:       1,
		LoopID:        "market-run",
		OwnerSession:  "sess-market",
		Status:        "running",
		UpdatedAt:     "2026-05-27T00:00:00Z",
		EventCount:    2,
		LastEventType: "loop.protocol_update",
	}
	if err := WriteState(statePath, state); err != nil {
		t.Fatalf("WriteState: %v", err)
	}
	gotState, found, err := ReadState(statePath)
	if err != nil || !found {
		t.Fatalf("ReadState found=%v err=%v", found, err)
	}
	if gotState.LoopID != "market-run" || gotState.Status != "running" || gotState.EventCount != 2 {
		t.Fatalf("state = %+v", gotState)
	}
	raw, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(raw) {
		t.Fatalf("state is not valid JSON: %s", string(raw))
	}
	summary, found, err := SummarizeFile(protocolPath, ProtocolRelPath("market-run"))
	if err != nil || !found {
		t.Fatalf("SummarizeFile found=%v err=%v", found, err)
	}
	if summary.LoopID != "market-run" || summary.OwnerSession != "sess-market" || summary.Status != "running" || summary.State == nil {
		t.Fatalf("summary did not prefer state: %+v", summary)
	}

	outside := filepath.Join(dir, "outside.json")
	if err := os.WriteFile(outside, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "state-link.json")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if err := WriteState(link, State{LoopID: "bad"}); err == nil || !strings.Contains(err.Error(), "must not be a symlink") {
		t.Fatalf("symlink WriteState err = %v", err)
	}
}

func TestRecordContextCompactionForcesNextFullProtocolFeed(t *testing.T) {
	dir := t.TempDir()
	protocolPath := ProtocolPath(dir, "market-run")
	if err := WriteProtocol(protocolPath, "# Loop\n\n## North Star\n\nRecover after compaction."); err != nil {
		t.Fatal(err)
	}
	if _, _, err := RecordProtocolFeed(protocolPath, "full"); err != nil {
		t.Fatalf("RecordProtocolFeed: %v", err)
	}
	state, event, err := RecordContextCompaction(protocolPath, "context_overflow", true)
	if err != nil {
		t.Fatalf("RecordContextCompaction: %v", err)
	}
	if event.Type != "context.compacted" || event.Reason != "context_overflow" || event.Path != ProtocolRelPath("market-run") || !event.Reactive {
		t.Fatalf("compaction event = %+v", event)
	}
	if !state.NeedsFullProtocolFeed || state.ContextCompactions != 1 || state.LastCompactionReason != "context_overflow" || !state.LastCompactionReactive {
		t.Fatalf("state after compaction = %+v", state)
	}
	state, event, err = RecordProtocolFeed(protocolPath, "full")
	if err != nil {
		t.Fatalf("RecordProtocolFeed after compaction: %v", err)
	}
	if state.NeedsFullProtocolFeed || state.ProtocolFeeds != 2 || state.LastProtocolFeedMode != "full" || event.FeedNumber != 2 {
		t.Fatalf("state after full feed = %+v event=%+v", state, event)
	}
}

func TestRecordTurnCheckpointUpdatesStateAndEvents(t *testing.T) {
	dir := t.TempDir()
	protocolPath := ProtocolPath(dir, "market-run")
	if err := WriteProtocol(protocolPath, "# Loop\n\n## North Star\n\nKeep checkpoints visible."); err != nil {
		t.Fatal(err)
	}
	state, event, err := RecordTurnCheckpoint(protocolPath, TurnCheckpoint{
		TurnID:             "turn_123",
		EndReason:          "completed",
		InputTokens:        120,
		OutputTokens:       45,
		ToolRequests:       3,
		ToolErrors:         1,
		LoopGuards:         1,
		ForcedNoTools:      0,
		MemoryUpdates:      2,
		SessionSearchCalls: 1,
	})
	if err != nil {
		t.Fatalf("RecordTurnCheckpoint: %v", err)
	}
	if event.Type != "loop.turn_checkpoint" ||
		event.TurnID != "turn_123" ||
		event.TurnEndReason != "completed" ||
		event.InputTokens != 120 ||
		event.ToolRequests != 3 ||
		event.LoopGuards != 1 ||
		event.MemoryUpdates != 2 ||
		event.SessionSearch != 1 ||
		event.Path != ProtocolRelPath("market-run") {
		t.Fatalf("event = %+v", event)
	}
	if state.TurnCheckpoints != 1 ||
		state.LastTurnID != "turn_123" ||
		state.LastTurnEndReason != "completed" ||
		state.LastTurnInputTokens != 120 ||
		state.LastTurnOutputTokens != 45 ||
		state.LastTurnToolRequests != 3 ||
		state.LastTurnToolErrors != 1 ||
		state.LastTurnLoopGuards != 1 ||
		state.LastTurnMemoryUpdates != 2 ||
		state.LastTurnSessionSearch != 1 ||
		state.LastEventType != "loop.turn_checkpoint" {
		t.Fatalf("state = %+v", state)
	}
	events, found, err := ReadRecentEvents(EventsPath(dir, "market-run"), 1)
	if err != nil || !found || len(events) != 1 {
		t.Fatalf("ReadRecentEvents found=%v len=%d err=%v", found, len(events), err)
	}
	if events[0].TurnID != "turn_123" || events[0].TurnEndReason != "completed" {
		t.Fatalf("recent event = %+v", events[0])
	}
}

func TestRecordDecisionUpdatesStateAndEvents(t *testing.T) {
	dir := t.TempDir()
	protocolPath := ProtocolPath(dir, "market-run")
	if err := WriteProtocol(protocolPath, "# Loop\n\n## North Star\n\nPersist runtime decisions."); err != nil {
		t.Fatal(err)
	}
	state, event, err := RecordDecision(protocolPath, DecisionCheckpoint{
		DecisionID:     "evidence-quality-dynamic-partial",
		Kind:           "evidence_quality",
		Trigger:        "source_access_dynamic_partial",
		Decision:       "defer",
		Confidence:     "high",
		Reason:         "dynamic widgets lacked text",
		RequiredAction: "read browser network responses",
	})
	if err != nil {
		t.Fatalf("RecordDecision: %v", err)
	}
	if event.Type != "loop.decision" ||
		event.DecisionID != "evidence-quality-dynamic-partial" ||
		event.DecisionKind != "evidence_quality" ||
		event.Trigger != "source_access_dynamic_partial" ||
		event.Decision != "defer" ||
		event.Confidence != "high" ||
		event.Reason != "dynamic widgets lacked text" ||
		event.RequiredAction != "read browser network responses" ||
		event.Path != ProtocolRelPath("market-run") {
		t.Fatalf("event = %+v", event)
	}
	if state.LoopDecisions != 1 ||
		state.LastDecisionID != "evidence-quality-dynamic-partial" ||
		state.LastDecisionKind != "evidence_quality" ||
		state.LastDecisionTrigger != "source_access_dynamic_partial" ||
		state.LastDecision != "defer" ||
		state.LastDecisionConfidence != "high" ||
		state.LastDecisionReason != "dynamic widgets lacked text" ||
		state.LastDecisionAction != "read browser network responses" ||
		state.LastEventType != "loop.decision" {
		t.Fatalf("state = %+v", state)
	}
	if _, _, err := RecordDecision(protocolPath, DecisionCheckpoint{Kind: "memory_write"}); err == nil || !strings.Contains(err.Error(), "requires kind and decision") {
		t.Fatalf("missing decision err = %v", err)
	}
}

func TestRecordMemoryUpdateUpdatesStateAndEvents(t *testing.T) {
	dir := t.TempDir()
	protocolPath := ProtocolPath(dir, "market-run")
	if err := WriteProtocol(protocolPath, "# Loop\n\n## Memory\n\nPersist memory updates."); err != nil {
		t.Fatal(err)
	}
	state, event, err := RecordMemoryUpdate(protocolPath, MemoryUpdateCheckpoint{
		TurnID:          "turn_mem",
		CallID:          "memory-1",
		Action:          "replace",
		Target:          "memory",
		Topic:           "markets",
		Location:        "memory:markets",
		Preview:         "old dashboard rule -> prefer browser network evidence",
		PreviousPreview: "old dashboard rule",
		NextPreview:     "prefer browser network evidence",
	})
	if err != nil {
		t.Fatalf("RecordMemoryUpdate: %v", err)
	}
	if event.Type != "loop.memory_update" ||
		event.TurnID != "turn_mem" ||
		event.CallID != "memory-1" ||
		event.MemoryAction != "replace" ||
		event.MemoryTarget != "memory" ||
		event.MemoryTopic != "markets" ||
		event.MemoryLocation != "memory:markets" ||
		event.MemoryPreview != "old dashboard rule -> prefer browser network evidence" ||
		event.PreviousPreview != "old dashboard rule" ||
		event.NextPreview != "prefer browser network evidence" {
		t.Fatalf("event = %+v", event)
	}
	if state.MemoryUpdateEvents != 1 ||
		state.LastMemoryUpdateAction != "replace" ||
		state.LastMemoryUpdateTarget != "memory" ||
		state.LastMemoryUpdateTopic != "markets" ||
		state.LastMemoryUpdateLoc != "memory:markets" ||
		state.LastMemoryUpdate != "old dashboard rule -> prefer browser network evidence" ||
		state.LastMemoryUpdatePrev != "old dashboard rule" ||
		state.LastMemoryUpdateNext != "prefer browser network evidence" ||
		state.LastEventType != "loop.memory_update" {
		t.Fatalf("state = %+v", state)
	}
	if _, _, err := RecordMemoryUpdate(protocolPath, MemoryUpdateCheckpoint{Action: "add"}); err == nil || !strings.Contains(err.Error(), "requires action and location") {
		t.Fatalf("missing location err = %v", err)
	}
}

func TestAppendAndReadRecentEventsRejectsUnsafeTargets(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".affent", "loops", "alpha", EventsFileName)
	for i := 0; i < 3; i++ {
		ev, err := AppendEvent(path, Event{
			Type:    "loop.protocol_update",
			Summary: "update",
			Reason:  "test",
		})
		if err != nil {
			t.Fatalf("AppendEvent %d: %v", i, err)
		}
		if ev.Seq != i+1 || ev.Time == "" {
			t.Fatalf("event %d = %+v", i, ev)
		}
	}
	count, err := CountEvents(path)
	if err != nil || count != 3 {
		t.Fatalf("CountEvents = %d err=%v", count, err)
	}
	events, found, err := ReadRecentEvents(path, 2)
	if err != nil || !found {
		t.Fatalf("ReadRecentEvents found=%v err=%v", found, err)
	}
	if len(events) != 2 || events[0].Seq != 2 || events[1].Seq != 3 {
		t.Fatalf("recent events = %+v", events)
	}

	outside := filepath.Join(dir, "outside.jsonl")
	if err := os.WriteFile(outside, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "events-link.jsonl")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if _, err := AppendEvent(link, Event{Type: "loop.protocol_update"}); err == nil || !strings.Contains(err.Error(), "must not be a symlink") {
		t.Fatalf("symlink AppendEvent err = %v", err)
	}
}
