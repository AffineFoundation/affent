package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/affinefoundation/affent/internal/loopstate"
	"github.com/affinefoundation/affent/internal/sse"
)

func TestWithLoopProtocolSkillProviderInjectsWhenFileExists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "LOOP.md")
	if err := os.WriteFile(path, []byte("# Loop Protocol\n\n## 1. North Star\n\nKeep evidence cited."), 0o644); err != nil {
		t.Fatal(err)
	}
	provider := WithLoopProtocolSkillProvider(path, func(userText string) string {
		if userText != "continue" {
			t.Fatalf("userText = %q, want continue", userText)
		}
		return "AFFENT ACTIVE SKILL: demo"
	})

	got := provider("continue")
	for _, want := range []string{
		"AFFENT LOOP PROTOCOL:",
		"feed_mode=full feed_number=1",
		"active long-run loop protocol",
		"Keep evidence cited.",
		"persisted plan state remains authoritative",
		"AFFENT ACTIVE SKILL: demo",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("loop protocol provider missing %q:\n%s", want, got)
		}
	}
}

func TestWithLoopProtocolSkillProviderSkipsMissingInvalidOrBlankFile(t *testing.T) {
	provider := WithLoopProtocolSkillProvider(filepath.Join(t.TempDir(), "missing.md"), func(string) string {
		return "next"
	})
	if got := provider("continue"); got != "next" {
		t.Fatalf("missing protocol provider = %q, want next", got)
	}

	dir := t.TempDir()
	blank := filepath.Join(dir, "blank.md")
	if err := os.WriteFile(blank, []byte(" \n\t"), 0o644); err != nil {
		t.Fatal(err)
	}
	provider = WithLoopProtocolSkillProvider(blank, nil)
	if got := provider("continue"); got != "" {
		t.Fatalf("blank protocol provider = %q, want empty", got)
	}

	outside := filepath.Join(dir, "outside.md")
	if err := os.WriteFile(outside, []byte("protocol"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.md")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	provider = WithLoopProtocolSkillProvider(link, func(string) string { return "next" })
	if got := provider("continue"); got != "next" {
		t.Fatalf("invalid protocol provider = %q, want next", got)
	}
}

func TestWithLoopProtocolSkillProviderCompactsLargeFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "LOOP.md")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", maxActiveLoopProtocolFullBytes+100)), 0o644); err != nil {
		t.Fatal(err)
	}
	got := WithLoopProtocolSkillProvider(path, nil)("continue")
	if !strings.Contains(got, strings.Repeat("x", maxActiveLoopProtocolFullBytes)+"...") {
		t.Fatalf("large protocol should be compacted, got length %d", len(got))
	}
	if strings.Contains(got, strings.Repeat("x", maxActiveLoopProtocolFullBytes+20)) {
		t.Fatalf("large protocol was not compacted, got length %d", len(got))
	}
}

func TestWithLoopProtocolSkillProviderUsesDigestBetweenFullFeeds(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "LOOP.md")
	archive := strings.Repeat("old archive detail ", 800)
	content := `# Loop Protocol

## 0. Metadata

- loop_id: digest-loop
- status: running

## 1. North Star

Keep long-run work anchored to evidence.

## Archive

` + archive
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	provider := WithLoopProtocolSkillProvider(path, nil)
	for i := 0; i < loopProtocolFullFirstFeeds; i++ {
		got := provider("continue")
		if !strings.Contains(got, "feed_mode=full") {
			t.Fatalf("feed %d should be full:\n%s", i+1, got)
		}
	}
	got := provider("continue")
	if !strings.Contains(got, "feed_mode=digest feed_number=4") {
		t.Fatalf("fourth feed should be digest:\n%s", got)
	}
	if !strings.Contains(got, "Keep long-run work anchored to evidence.") {
		t.Fatalf("digest missing north star:\n%s", got)
	}
	if strings.Contains(got, "old archive detail old archive detail") {
		t.Fatalf("digest should omit archive detail:\n%s", got)
	}
	for i := 5; i < loopProtocolFullEveryFeeds; i++ {
		_ = provider("continue")
	}
	got = provider("continue")
	if !strings.Contains(got, "feed_mode=full feed_number=6") {
		t.Fatalf("sixth feed should return to full:\n%s", got)
	}
}

func TestWithLoopProtocolSkillProviderForcesFullFeedAfterCompaction(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "LOOP.md")
	archive := strings.Repeat("post compaction archive detail ", 120)
	content := `# Loop Protocol

## North Star

Reload the full protocol after compaction.

## Archive

` + archive
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	provider := WithLoopProtocolSkillProvider(path, nil)
	for i := 0; i < loopProtocolFullFirstFeeds; i++ {
		got := provider("continue")
		if !strings.Contains(got, "feed_mode=full") {
			t.Fatalf("feed %d should be full:\n%s", i+1, got)
		}
	}
	if _, _, err := loopstate.RecordContextCompaction(path, "context_overflow", true); err != nil {
		t.Fatalf("RecordContextCompaction: %v", err)
	}
	got := provider("continue")
	if !strings.Contains(got, "feed_mode=full feed_number=4") ||
		!strings.Contains(got, "context_compactions=1") ||
		!strings.Contains(got, "last_compaction=context_overflow") ||
		!strings.Contains(got, "post compaction archive detail post compaction archive detail") {
		t.Fatalf("post-compaction feed should be full with recovery state:\n%s", got)
	}
	state, found, err := loopstate.ReadState(filepath.Join(dir, loopstate.StateFileName))
	if err != nil || !found {
		t.Fatalf("ReadState found=%v err=%v", found, err)
	}
	if state.NeedsFullProtocolFeed || state.LastProtocolFeedMode != "full" || state.ProtocolFeeds != 4 {
		t.Fatalf("state after recovery feed = %+v", state)
	}
}

func TestWithLoopProtocolSkillProviderPersistsFeedCadenceAcrossProviders(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "LOOP.md")
	if err := os.WriteFile(path, []byte("# Loop Protocol\n\n## 1. North Star\n\nPersist feed cadence."), 0o644); err != nil {
		t.Fatal(err)
	}
	first := WithLoopProtocolSkillProvider(path, nil)
	for i := 0; i < 4; i++ {
		_ = first("continue")
	}
	state, found, err := loopstate.ReadState(filepath.Join(dir, loopstate.StateFileName))
	if err != nil || !found {
		t.Fatalf("ReadState found=%v err=%v", found, err)
	}
	if state.ProtocolFeeds != 4 || state.LastProtocolFeedMode != "digest" || state.LastEventType != "loop.protocol_feed" {
		t.Fatalf("state after first provider = %+v", state)
	}
	events, found, err := loopstate.ReadRecentEvents(filepath.Join(dir, loopstate.EventsFileName), 10)
	if err != nil || !found || len(events) != 4 {
		t.Fatalf("events found=%v len=%d err=%v", found, len(events), err)
	}
	if events[3].Type != "loop.protocol_feed" || events[3].Mode != "digest" || events[3].FeedNumber != 4 {
		t.Fatalf("fourth event = %+v", events[3])
	}

	resumed := WithLoopProtocolSkillProvider(path, nil)
	got := resumed("continue")
	if !strings.Contains(got, "feed_mode=digest feed_number=5") {
		t.Fatalf("resumed provider should continue persisted cadence:\n%s", got)
	}
	if !strings.Contains(got, "protocol_feeds=5") || !strings.Contains(got, "last_feed=digest") {
		t.Fatalf("state line should include persisted feed stats:\n%s", got)
	}
}

func TestWithLoopProtocolSkillProviderIncludesPlanCheckpoint(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "LOOP.md")
	if err := os.WriteFile(path, []byte("# Loop Protocol\n\n## North Star\n\nRecover the current step."), 0o644); err != nil {
		t.Fatal(err)
	}
	checkpoint := func() loopstate.PlanCheckpoint {
		return loopstate.PlanCheckpoint{
			Valid:      true,
			Label:      "plan:1/2:active",
			StepIndex:  2,
			StepStatus: "in_progress",
			Step:       "continue loop runtime implementation",
		}
	}

	got := WithLoopProtocolSkillProviderWithCheckpoint(path, checkpoint, nil)("continue")
	for _, want := range []string{
		"plan_label=plan:1/2:active plan_step_index=2 plan_step_status=in_progress",
		"plan_current_step: continue loop runtime implementation",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("loop protocol provider missing plan checkpoint %q:\n%s", want, got)
		}
	}
	state, found, err := loopstate.ReadState(filepath.Join(dir, loopstate.StateFileName))
	if err != nil || !found {
		t.Fatalf("read state found=%v err=%v", found, err)
	}
	if state.LastPlanLabel != "plan:1/2:active" || state.LastPlanStepIndex != 2 || state.LastPlanStepStatus != "in_progress" || state.LastPlanStep != "continue loop runtime implementation" {
		t.Fatalf("state plan checkpoint = %+v", state)
	}
	events, _, err := loopstate.ReadRecentEvents(filepath.Join(dir, loopstate.EventsFileName), 1)
	if err != nil || len(events) != 1 {
		t.Fatalf("read events len=%d err=%v", len(events), err)
	}
	if events[0].PlanLabel != "plan:1/2:active" || events[0].PlanStepIndex != 2 || events[0].PlanStepStatus != "in_progress" || events[0].PlanStep != "continue loop runtime implementation" {
		t.Fatalf("event plan checkpoint = %+v", events[0])
	}
}

func TestAppendUserMessagePublishesLoopProtocolFeedEvent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "LOOP.md")
	if err := os.WriteFile(path, []byte("# Loop Protocol\n\n## 1. North Star\n\nTrace protocol feeds."), 0o644); err != nil {
		t.Fatal(err)
	}
	checkpoint := func() loopstate.PlanCheckpoint {
		return loopstate.PlanCheckpoint{Valid: true, Label: "plan:0/1:active", StepIndex: 1, StepStatus: "in_progress", Step: "read trace evidence"}
	}
	conv, err := OpenConversationAt(filepath.Join(t.TempDir(), "session.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	events := make(chan sse.Event, 1)
	loop := &Loop{
		Conv:          conv,
		Events:        events,
		SkillProvider: WithLoopProtocolSkillProviderWithCheckpoint(path, checkpoint, nil),
	}
	if err := loop.appendUserMessage("turn_loop_feed", "continue"); err != nil {
		t.Fatal(err)
	}
	select {
	case ev := <-events:
		if ev.Type != sse.TypeLoopProtocolFeed {
			t.Fatalf("event type = %q, want %q", ev.Type, sse.TypeLoopProtocolFeed)
		}
		var payload sse.LoopProtocolFeedPayload
		if err := json.Unmarshal(ev.Data, &payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		if payload.TurnID != "turn_loop_feed" ||
			payload.Mode != "full" ||
			payload.FeedNumber != 1 ||
			payload.ProtocolFeeds != 1 ||
			payload.PlanLabel != "plan:0/1:active" ||
			payload.PlanCurrentStepIndex != 1 ||
			payload.PlanCurrentStepStatus != "in_progress" ||
			payload.PlanCurrentStep != "read trace evidence" ||
			payload.ProtocolPath != ".affent/loops/"+filepath.Base(dir)+"/LOOP.md" {
			t.Fatalf("payload = %+v", payload)
		}
	default:
		t.Fatal("expected loop.protocol_feed event")
	}
}

func TestRecordLoopTurnCheckpointPersistsRuntimeSummary(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "LOOP.md")
	if err := os.WriteFile(path, []byte("# Loop Protocol\n\n## North Star\n\nAudit every long-run turn."), 0o644); err != nil {
		t.Fatal(err)
	}
	loop := &Loop{LoopProtocolPath: path}
	loop.recordLoopTurnCheckpoint("turn_runtime", sse.TurnEndMaxTurns, 300, 80, sse.ToolRuntimeStats{
		ToolRequests:           4,
		ToolErrors:             2,
		LoopGuardInterventions: 1,
		ForcedNoTools:          1,
		MemoryUpdates:          1,
		SessionSearchCalls:     2,
	})

	state, found, err := loopstate.ReadState(filepath.Join(dir, loopstate.StateFileName))
	if err != nil || !found {
		t.Fatalf("ReadState found=%v err=%v", found, err)
	}
	if state.TurnCheckpoints != 1 ||
		state.LastTurnID != "turn_runtime" ||
		state.LastTurnEndReason != sse.TurnEndMaxTurns ||
		state.LastTurnInputTokens != 300 ||
		state.LastTurnOutputTokens != 80 ||
		state.LastTurnToolRequests != 4 ||
		state.LastTurnToolErrors != 2 ||
		state.LastTurnLoopGuards != 1 ||
		state.LastTurnForcedNoTools != 1 ||
		state.LastTurnMemoryUpdates != 1 ||
		state.LastTurnSessionSearch != 2 {
		t.Fatalf("state = %+v", state)
	}
	events, found, err := loopstate.ReadRecentEvents(filepath.Join(dir, loopstate.EventsFileName), 1)
	if err != nil || !found || len(events) != 1 {
		t.Fatalf("ReadRecentEvents found=%v len=%d err=%v", found, len(events), err)
	}
	if events[0].Type != "loop.turn_checkpoint" || events[0].TurnID != "turn_runtime" || events[0].TurnEndReason != sse.TurnEndMaxTurns {
		t.Fatalf("event = %+v", events[0])
	}
}
