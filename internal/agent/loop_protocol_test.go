package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

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

func TestRunTurnRejectsUncalibratedLoopProtocolActivation(t *testing.T) {
	tmp := t.TempDir()
	protocolPath := loopstate.ProtocolPath(tmp, "uncalibrated")
	if _, _, _, err := loopstate.EnsureProtocolTemplate(protocolPath, loopstate.ProtocolTemplateOptions{
		LoopID:       "uncalibrated",
		OwnerSession: "uncalibrated",
		Goal:         "Run a long market analysis without losing recovery context.",
		Status:       "draft",
	}); err != nil {
		t.Fatalf("EnsureProtocolTemplate: %v", err)
	}
	protocol, found, err := loopstate.ReadProtocol(protocolPath)
	if err != nil || !found {
		t.Fatalf("ReadProtocol found=%v err=%v", found, err)
	}
	protocol = strings.Replace(protocol, "- status: draft", "- status: running", 1)
	protocol = strings.Replace(protocol, "- hard constraints:", "- hard constraints: cite evidence and pause on unclear intent", 1)
	protocol = strings.Replace(protocol, "- known evidence:", "- known evidence: user wants durable market analysis", 1)
	protocol = strings.Replace(protocol, "- current risk or blocker:", "- current risk or blocker: live source quality unknown", 1)
	protocol = strings.Replace(protocol, "- important artifacts:", "- important artifacts: none yet", 1)
	protocol = strings.Replace(protocol, "- important trace spans:", "- important trace spans: initial loop draft", 1)
	protocol = strings.Replace(protocol, "- last known recovery note:", "- last known recovery note: reload LOOP.md and plan state", 1)
	args, err := json.Marshal(map[string]any{
		"action":   "complete_activation",
		"protocol": protocol,
		"reason":   "same-turn activation attempt",
	})
	if err != nil {
		t.Fatal(err)
	}
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		fl := w.(http.Flusher)
		if call == 1 {
			writeSSEData(t, w, map[string]any{
				"choices": []any{map[string]any{
					"delta": map[string]any{
						"role": "assistant",
						"tool_calls": []any{map[string]any{
							"index": 0,
							"id":    "lp1",
							"type":  "function",
							"function": map[string]any{
								"name":      LoopProtocolToolName,
								"arguments": string(args),
							},
						}},
					},
					"finish_reason": nil,
				}},
			})
			w.Write([]byte("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\n"))
			w.Write([]byte("data: [DONE]\n\n"))
			fl.Flush()
			return
		}
		w.Write([]byte("data: {\"choices\":[{\"delta\":{\"role\":\"assistant\",\"content\":\"Before I can activate this loop, what stop condition should pause it?\"},\"finish_reason\":\"stop\"}]}\n\n"))
		w.Write([]byte("data: [DONE]\n\n"))
		fl.Flush()
	}))
	t.Cleanup(srv.Close)

	conv, err := OpenConversationAt(filepath.Join(tmp, "sess.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	reg := NewRegistry()
	RegisterLoopProtocolOnly(reg, protocolPath)
	events := make(chan sse.Event, 64)
	loop := &Loop{
		LLM: NewLLMClient(srv.URL, "", "fake-model"), Tools: reg, Conv: conv, Events: events,
		LoopProtocolPath: protocolPath, MaxTurnSteps: 4, PerCallTimeout: 5 * time.Second,
	}
	if err := loop.EnsureSystemPrompt("base"); err != nil {
		t.Fatal(err)
	}
	if _, err := loop.SendUser(context.Background(), "Set up a loop for market analysis"); err != nil {
		t.Fatal(err)
	}

	deadline := time.After(10 * time.Second)
	sawActivationError := false
	sawCalibrationQuestion := false
	for {
		select {
		case ev := <-events:
			switch ev.Type {
			case sse.TypeToolResult:
				var p sse.ToolResultPayload
				if err := json.Unmarshal(ev.Data, &p); err != nil {
					t.Fatalf("decode tool.result: %v", err)
				}
				if p.ExitCode != 0 && strings.Contains(p.Result, "requires a user calibration answer") {
					sawActivationError = true
				}
			case sse.TypeLoopCalibrationRequest:
				var p sse.LoopProtocolCalibrationPayload
				if err := json.Unmarshal(ev.Data, &p); err != nil {
					t.Fatalf("decode loop.protocol_calibration_request: %v", err)
				}
				if p.CalibrationQuestions == 1 &&
					p.ProtocolPath == loopstate.ProtocolRelPath("uncalibrated") &&
					strings.Contains(p.LastCalibrationQuestion, "stop condition") {
					sawCalibrationQuestion = true
				}
			case sse.TypeTurnEnd:
				if !sawActivationError {
					t.Fatal("turn ended without uncalibrated activation tool error")
				}
				if !sawCalibrationQuestion {
					t.Fatal("turn ended without loop calibration question event")
				}
				state, found, err := loopstate.ReadState(filepath.Join(filepath.Dir(protocolPath), loopstate.StateFileName))
				if err != nil || !found {
					t.Fatalf("ReadState found=%v err=%v", found, err)
				}
				if state.Status != "draft" || state.LastEventType == "loop.protocol_activate" {
					t.Fatalf("uncalibrated activation must leave draft state: %+v", state)
				}
				content, found, err := loopstate.ReadProtocol(protocolPath)
				if err != nil || !found {
					t.Fatalf("ReadProtocol after turn found=%v err=%v", found, err)
				}
				if !strings.Contains(content, "- status: draft") {
					t.Fatalf("uncalibrated activation overwrote LOOP.md:\n%s", content)
				}
				return
			}
		case <-deadline:
			t.Fatal("timeout waiting for turn.end")
		}
	}
}

func TestRunTurn_EmitsResearchCheckpointForHighImpactLoopReview(t *testing.T) {
	firstBody := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := readReqBody(r)
		select {
		case firstBody <- body:
		default:
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		fl := w.(http.Flusher)
		w.Write([]byte("data: {\"choices\":[{\"delta\":{\"role\":\"assistant\",\"content\":\"will calibrate before changing the loop route\"},\"finish_reason\":\"stop\"}]}\n\n"))
		w.Write([]byte("data: [DONE]\n\n"))
		fl.Flush()
	}))
	t.Cleanup(srv.Close)

	tmp := t.TempDir()
	protocolPath := filepath.Join(tmp, ".affent", "loops", "research-loop", "LOOP.md")
	if err := loopstate.WriteProtocol(protocolPath, "# Loop\n\n## 1. North Star\n\nKeep Affent grounded in evidence."); err != nil {
		t.Fatal(err)
	}
	conv, err := OpenConversationAt(filepath.Join(tmp, "sess.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	reg := NewRegistry()
	reg.Add(&Tool{
		Name:   FocusedTaskToolName,
		Schema: json.RawMessage(`{"type":"object","properties":{}}`),
	})
	reg.Add(&Tool{
		Name:   "web_fetch",
		Schema: json.RawMessage(`{"type":"object","properties":{}}`),
	})
	events := make(chan sse.Event, 32)
	loop := &Loop{
		LLM: NewLLMClient(srv.URL, "", "fake-model"), Tools: reg, Conv: conv, Events: events,
		LoopProtocolPath: protocolPath, MaxTurnSteps: 2, PerCallTimeout: 5 * time.Second,
	}
	if err := loop.EnsureSystemPrompt("base"); err != nil {
		t.Fatal(err)
	}
	if _, err := loop.SendUser(context.Background(), "请从全局角度结合主流 agent 和论文研究 loop 协议是否合理"); err != nil {
		t.Fatal(err)
	}

	deadline := time.After(10 * time.Second)
	sawDecision := false
	sawResearchContext := false
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				t.Fatal("event channel closed before turn.end")
			}
			switch ev.Type {
			case sse.TypeContextInjected:
				var p sse.ContextInjectedPayload
				if err := json.Unmarshal(ev.Data, &p); err != nil {
					t.Fatalf("decode context.injected: %v", err)
				}
				if p.Source == "research_checkpoint" &&
					strings.Contains(p.Preview, researchCheckpointSkillMarker) &&
					strings.Contains(p.Summary, "external-calibration") {
					sawResearchContext = true
				}
			case sse.TypeLoopDecision:
				var p sse.LoopDecisionPayload
				if err := json.Unmarshal(ev.Data, &p); err != nil {
					t.Fatalf("decode loop.decision: %v", err)
				}
				sawDecision = true
				if p.Kind != "research_checkpoint" || p.Decision != "trigger" || p.Trigger != "external_calibration_requested" {
					t.Fatalf("unexpected research checkpoint decision: %+v", p)
				}
				if p.VisibleInUI == nil || !*p.VisibleInUI {
					t.Fatalf("research checkpoint should be visible in UI: %+v", p)
				}
				if !strings.Contains(p.RequiredAction, "focused research task") || !strings.Contains(p.RequiredAction, "web/browser") {
					t.Fatalf("required action should reflect available research surface: %+v", p)
				}
			case sse.TypeTurnEnd:
				if !sawDecision {
					t.Fatal("expected research checkpoint decision before turn.end")
				}
				if !sawResearchContext {
					t.Fatal("expected research checkpoint context.injected event before turn.end")
				}
				var body string
				select {
				case body = <-firstBody:
				default:
					t.Fatal("missing captured LLM request body")
				}
				if !strings.Contains(body, researchCheckpointSkillMarker) ||
					!strings.Contains(body, "bounded external calibration") {
					t.Fatalf("LLM request missing research checkpoint guidance:\n%s", body)
				}
				state, found, err := loopstate.ReadState(filepath.Join(filepath.Dir(protocolPath), loopstate.StateFileName))
				if err != nil {
					t.Fatal(err)
				}
				if !found || state.LastDecisionKind != "research_checkpoint" || state.LastDecision != "trigger" {
					t.Fatalf("loop state missing persisted research checkpoint: found=%v state=%+v", found, state)
				}
				return
			}
		case <-deadline:
			t.Fatal("timeout waiting for turn.end")
		}
	}
}

func TestResearchCheckpointSkipsDraftLoopProtocol(t *testing.T) {
	tmp := t.TempDir()
	protocolPath := filepath.Join(tmp, ".affent", "loops", "draft-loop", "LOOP.md")
	if err := loopstate.WriteProtocol(protocolPath, "# Loop\n\n## 0. Metadata\n\n- status: draft\n\n## 1. North Star\n\nKeep route changes researched."); err != nil {
		t.Fatal(err)
	}
	loop := &Loop{LoopProtocolPath: protocolPath, Tools: NewRegistry()}
	if _, ok := loop.researchCheckpointDecision("请结合主流 agent 研究 loop 协议是否合理", TurnOptions{}); ok {
		t.Fatal("draft LOOP.md must not emit research checkpoints")
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

func TestWithLoopProtocolSkillProviderSkipsDraftProtocol(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "LOOP.md")
	if err := loopstate.WriteProtocol(path, "# Loop Protocol\n\n## 0. Metadata\n\n- status: draft\n\n## North Star\n\nPending activation."); err != nil {
		t.Fatal(err)
	}
	if err := loopstate.WriteState(filepath.Join(dir, loopstate.StateFileName), loopstate.State{
		Version: 1,
		LoopID:  filepath.Base(dir),
		Status:  "draft",
	}); err != nil {
		t.Fatal(err)
	}
	got := WithLoopProtocolSkillProvider(path, func(string) string { return "next" })("continue")
	if got != "next" {
		t.Fatalf("draft protocol should not be injected as active loop; got:\n%s", got)
	}
}

func TestWithLoopProtocolSkillProviderSkipsDraftProtocolWithoutState(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "LOOP.md")
	if err := loopstate.WriteProtocol(path, "# Loop Protocol\n\n## 0. Metadata\n\n- status: draft\n\n## North Star\n\nPending activation."); err != nil {
		t.Fatal(err)
	}
	got := WithLoopProtocolSkillProvider(path, func(string) string { return "next" })("continue")
	if got != "next" {
		t.Fatalf("draft protocol without sidecar state should not be injected; got:\n%s", got)
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

## 2. Current Situation

- current intent: finish the SN120 evidence audit
- current risk or blocker: JS dashboard values are partial until network refs are read
- next recovery anchor: read the latest plan state before continuing

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
	for _, want := range []string{
		"finish the SN120 evidence audit",
		"JS dashboard values are partial until network refs are read",
		"read the latest plan state before continuing",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("digest missing current situation %q:\n%s", want, got)
		}
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

func TestWithLoopProtocolSkillProviderIncludesRuntimeCheckpoints(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "LOOP.md")
	if err := os.WriteFile(path, []byte("# Loop Protocol\n\n## North Star\n\nRecover from recent runtime checkpoints."), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loopstate.RecordMemoryUpdate(path, loopstate.MemoryUpdateCheckpoint{
		TurnID:          "turn_mem",
		CallID:          "memory-1",
		Action:          "replace",
		Target:          "memory",
		Topic:           "markets",
		Location:        "memory:markets",
		Preview:         "old dashboard rule -> prefer browser network evidence",
		PreviousPreview: "old dashboard rule",
		NextPreview:     "prefer browser network evidence",
	}); err != nil {
		t.Fatalf("RecordMemoryUpdate: %v", err)
	}
	if _, _, err := loopstate.RecordDecision(path, loopstate.DecisionCheckpoint{
		DecisionID:     "evidence-quality-dynamic-partial",
		Kind:           "evidence_quality",
		Trigger:        "source_access_dynamic_partial",
		Decision:       "defer",
		Confidence:     "high",
		Reason:         "dynamic widgets lacked text",
		RequiredAction: "read browser network responses",
	}); err != nil {
		t.Fatalf("RecordDecision: %v", err)
	}
	if _, _, err := loopstate.RecordProtocolCalibrationAnswer(path, "Pause if the requested market source cannot be verified."); err != nil {
		t.Fatalf("RecordProtocolCalibrationAnswer: %v", err)
	}
	if _, _, err := loopstate.RecordTurnCheckpoint(path, loopstate.TurnCheckpoint{
		TurnID:             "turn_done",
		EndReason:          sse.TurnEndCompleted,
		InputTokens:        123,
		OutputTokens:       45,
		ToolRequests:       2,
		MemoryUpdates:      1,
		MemorySearchCalls:  3,
		MemorySearchMisses: 2,
		SessionSearchCalls: 1,
		LoopGuards:         1,
	}); err != nil {
		t.Fatalf("RecordTurnCheckpoint: %v", err)
	}

	got := WithLoopProtocolSkillProvider(path, nil)("continue")
	for _, want := range []string{
		"calibration_answers=1",
		"last_calibration: answer=Pause if the requested market source cannot be verified.",
		"last_turn: id=turn_done reason=completed tokens=123/45 tools=2 memory_updates=1 memory_searches=3 memory_misses=2 session_search=1 loop_guards=1",
		"last_memory_update: action=replace location=memory:markets preview=old dashboard rule -> prefer browser network evidence",
		"last_decision: kind=evidence_quality trigger=source_access_dynamic_partial decision=defer action=read browser network responses",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("loop protocol feed missing runtime checkpoint %q:\n%s", want, got)
		}
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
	if err := os.WriteFile(path, []byte("# Loop Protocol\n\n## 1. North Star\n\nTrace protocol feeds.\n\n## 2. Current Situation\n\n- current intent: recover the long-run trace\n- current risk: network evidence is not verified yet"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loopstate.RecordProtocolCalibrationAnswer(path, "Stop when the requested source cannot be verified."); err != nil {
		t.Fatalf("RecordProtocolCalibrationAnswer: %v", err)
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
	if err := loop.appendUserMessage("turn_loop_feed", "continue", TurnOptions{}); err != nil {
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
			payload.CalibrationAnswers != 1 ||
			payload.LastCalibrationAnswer != "Stop when the requested source cannot be verified." ||
			payload.PlanLabel != "plan:0/1:active" ||
			payload.PlanCurrentStepIndex != 1 ||
			payload.PlanCurrentStepStatus != "in_progress" ||
			payload.PlanCurrentStep != "read trace evidence" ||
			!strings.Contains(payload.CurrentSituation, "recover the long-run trace") ||
			!strings.Contains(payload.CurrentSituation, "network evidence is not verified yet") ||
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
		MemorySearchCalls:      4,
		MemorySearchMisses:     2,
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
		state.LastTurnMemorySearches != 4 ||
		state.LastTurnMemoryMisses != 2 ||
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

func TestPublishLoopDecisionPersistsSidecarDecision(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "LOOP.md")
	if err := os.WriteFile(path, []byte("# Loop Protocol\n\n## North Star\n\nAudit gate decisions."), 0o644); err != nil {
		t.Fatal(err)
	}
	events := make(chan sse.Event, 1)
	loop := &Loop{LoopProtocolPath: path, Events: events}
	loop.publishLoopDecision(sse.LoopDecisionPayload{
		TurnID:         "turn_decision",
		DecisionID:     "decision-1",
		Kind:           "evidence_quality",
		Trigger:        "source_access_dynamic_partial",
		Decision:       "defer",
		Confidence:     "high",
		Reason:         "dynamic widgets lacked text",
		RequiredAction: "read browser network responses",
	})

	select {
	case ev := <-events:
		if ev.Type != sse.TypeLoopDecision {
			t.Fatalf("event type = %q, want %q", ev.Type, sse.TypeLoopDecision)
		}
		var payload sse.LoopDecisionPayload
		if err := json.Unmarshal(ev.Data, &payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		if payload.LoopID != filepath.Base(dir) || payload.DecisionID != "decision-1" || payload.Decision != "defer" {
			t.Fatalf("payload = %+v", payload)
		}
	default:
		t.Fatal("expected loop.decision event")
	}

	state, found, err := loopstate.ReadState(filepath.Join(dir, loopstate.StateFileName))
	if err != nil || !found {
		t.Fatalf("ReadState found=%v err=%v", found, err)
	}
	if state.LoopDecisions != 1 ||
		state.LastDecisionID != "decision-1" ||
		state.LastDecisionKind != "evidence_quality" ||
		state.LastDecision != "defer" ||
		state.LastDecisionAction != "read browser network responses" {
		t.Fatalf("state = %+v", state)
	}
	sidecar, found, err := loopstate.ReadRecentEvents(filepath.Join(dir, loopstate.EventsFileName), 1)
	if err != nil || !found || len(sidecar) != 1 {
		t.Fatalf("ReadRecentEvents found=%v len=%d err=%v", found, len(sidecar), err)
	}
	if sidecar[0].Type != "loop.decision" || sidecar[0].DecisionID != "decision-1" || sidecar[0].RequiredAction != "read browser network responses" {
		t.Fatalf("sidecar event = %+v", sidecar[0])
	}
}

func TestRecordLoopMemoryUpdatePersistsSidecarUpdate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "LOOP.md")
	if err := os.WriteFile(path, []byte("# Loop Protocol\n\n## Memory\n\nAudit memory updates."), 0o644); err != nil {
		t.Fatal(err)
	}
	loop := &Loop{LoopProtocolPath: path}
	loop.recordLoopMemoryUpdate("turn_mem", "memory-1", &sse.MemoryUpdateMeta{
		Action:          "add",
		Target:          "memory",
		Topic:           "markets",
		Location:        "memory:markets",
		Preview:         "prefer browser_network_read for dynamic dashboards",
		NextPreview:     "prefer browser_network_read for dynamic dashboards",
		PreviousPreview: "",
	})

	state, found, err := loopstate.ReadState(filepath.Join(dir, loopstate.StateFileName))
	if err != nil || !found {
		t.Fatalf("ReadState found=%v err=%v", found, err)
	}
	if state.MemoryUpdateEvents != 1 ||
		state.LastMemoryUpdateAction != "add" ||
		state.LastMemoryUpdateLoc != "memory:markets" ||
		state.LastMemoryUpdate != "prefer browser_network_read for dynamic dashboards" ||
		state.LastMemoryUpdateNext != "prefer browser_network_read for dynamic dashboards" {
		t.Fatalf("state = %+v", state)
	}
	sidecar, found, err := loopstate.ReadRecentEvents(filepath.Join(dir, loopstate.EventsFileName), 1)
	if err != nil || !found || len(sidecar) != 1 {
		t.Fatalf("ReadRecentEvents found=%v len=%d err=%v", found, len(sidecar), err)
	}
	if sidecar[0].Type != "loop.memory_update" ||
		sidecar[0].TurnID != "turn_mem" ||
		sidecar[0].CallID != "memory-1" ||
		sidecar[0].MemoryAction != "add" ||
		sidecar[0].MemoryLocation != "memory:markets" ||
		sidecar[0].NextPreview != "prefer browser_network_read for dynamic dashboards" {
		t.Fatalf("sidecar event = %+v", sidecar[0])
	}
}

func writeSSEData(t *testing.T, w http.ResponseWriter, payload any) {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("data: " + string(raw) + "\n\n")); err != nil {
		t.Fatal(err)
	}
}
