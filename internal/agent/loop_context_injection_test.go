package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/affinefoundation/affent/internal/sse"
)

func TestAppendUserMessagePublishesContextInjectedEvents(t *testing.T) {
	conv, err := OpenConversationAt(filepath.Join(t.TempDir(), "session.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	events := make(chan sse.Event, 4)
	loop := &Loop{
		Conv:   conv,
		Events: events,
		SecretValuesProvider: func() []string {
			return []string{"super-secret-token"}
		},
		SkillProvider: func(string) string {
			return strings.Join([]string{
				"AFFENT ACCOUNT ACCESS:\n- Configured environment variables available to shell commands: GITHUB_TOKEN.\n- SSH public key is configured for Git host access; use SSH remotes when appropriate. token=super-secret-token",
				"AFFENT ACTIVE SKILL: demo\nUse demo workflow.",
			}, "\n\n")
		},
	}

	if err := loop.appendUserMessage("turn_context", "clone private repo", TurnOptions{}); err != nil {
		t.Fatal(err)
	}

	var payloads []sse.ContextInjectedPayload
	for i := 0; i < 2; i++ {
		select {
		case ev := <-events:
			if ev.Type != sse.TypeContextInjected {
				t.Fatalf("event type = %q, want %q", ev.Type, sse.TypeContextInjected)
			}
			var payload sse.ContextInjectedPayload
			if err := json.Unmarshal(ev.Data, &payload); err != nil {
				t.Fatalf("decode payload: %v", err)
			}
			payloads = append(payloads, payload)
		default:
			t.Fatalf("expected context.injected event %d", i+1)
		}
	}

	if payloads[0].TurnID != "turn_context" || payloads[0].Source != "account_access" {
		t.Fatalf("account payload = %+v", payloads[0])
	}
	if !strings.Contains(payloads[0].Preview, "GITHUB_TOKEN") || strings.Contains(payloads[0].Preview, "super-secret-token") {
		t.Fatalf("account preview = %q", payloads[0].Preview)
	}
	if payloads[1].Source != "skill" || !strings.Contains(payloads[1].Summary, "demo") {
		t.Fatalf("skill payload = %+v", payloads[1])
	}
	if payloads[0].Bytes <= 0 || payloads[0].EstimatedTokens <= 0 {
		t.Fatalf("payload should carry prompt size metadata: %+v", payloads[0])
	}
	msgs := conv.Snapshot()
	if len(msgs) != 2 || !msgs[0].TransientContext || msgs[1].Role != "user" {
		t.Fatalf("dynamic context should be persisted as transient before the user message: %+v", msgs)
	}
}

func TestAppendUserMessageInjectsRuntimeWorkspaceContext(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "remote.git"), 0o755); err != nil {
		t.Fatal(err)
	}
	conv, err := OpenConversationAt(filepath.Join(t.TempDir(), "session.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	events := make(chan sse.Event, 2)
	loop := &Loop{
		Conv:          conv,
		Events:        events,
		WorkspaceRoot: dir,
		Tools: func() *Registry {
			reg := NewRegistry()
			reg.Add(readFileTool(BuiltinDeps{HostWorkspaceDir: dir}))
			return reg
		}(),
	}

	if err := loop.appendUserMessage("turn_workspace", "clone and test", TurnOptions{}); err != nil {
		t.Fatal(err)
	}

	ev := <-events
	if ev.Type != sse.TypeContextInjected {
		t.Fatalf("event type = %q, want %q", ev.Type, sse.TypeContextInjected)
	}
	var payload sse.ContextInjectedPayload
	if err := json.Unmarshal(ev.Data, &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if payload.Source != "runtime_workspace" || !strings.Contains(payload.Preview, "workspace-relative") {
		t.Fatalf("workspace payload = %+v", payload)
	}
	msgs := conv.Snapshot()
	if len(msgs) != 2 || msgs[0].Role != "system" || !msgs[0].TransientContext || !strings.Contains(msgs[0].Content, `"remote.git" (dir)`) {
		t.Fatalf("workspace context should be transient before user message: %+v", msgs)
	}
}

func TestAppendUserMessageInjectsScheduledTurnContext(t *testing.T) {
	conv, err := OpenConversationAt(filepath.Join(t.TempDir(), "session.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	events := make(chan sse.Event, 2)
	loop := &Loop{
		Conv:   conv,
		Events: events,
	}

	if err := loop.appendUserMessage("turn_schedule", "inspect metrics", TurnOptions{
		UserSource:   "schedule",
		ScheduleID:   "sched_metrics",
		ScheduleKind: "custom",
	}); err != nil {
		t.Fatal(err)
	}

	ev := <-events
	if ev.Type != sse.TypeContextInjected {
		t.Fatalf("event type = %q, want %q", ev.Type, sse.TypeContextInjected)
	}
	var payload sse.ContextInjectedPayload
	if err := json.Unmarshal(ev.Data, &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if payload.Source != "schedule" ||
		payload.Title != "Scheduled turn context injected" ||
		!strings.Contains(payload.Preview, "sched_metrics") {
		t.Fatalf("schedule payload = %+v", payload)
	}
	msgs := conv.Snapshot()
	if len(msgs) != 2 ||
		msgs[0].Role != "system" ||
		!msgs[0].TransientContext ||
		!strings.Contains(msgs[0].Content, "AFFENT SCHEDULED TURN:") ||
		!strings.Contains(msgs[0].Content, "- schedule_kind: custom") ||
		!strings.Contains(msgs[0].Content, "do not say the schedule was newly set") ||
		msgs[1].Role != "user" ||
		msgs[1].Content != "inspect metrics" {
		t.Fatalf("scheduled context should be transient before user message: %+v", msgs)
	}
}

func TestAppendUserMessagePublishesLoopDraftActivationContext(t *testing.T) {
	conv, err := OpenConversationAt(filepath.Join(t.TempDir(), "session.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	events := make(chan sse.Event, 2)
	loop := &Loop{
		Conv:   conv,
		Events: events,
		SkillProvider: func(string) string {
			return "AFFENT LOOP DRAFT ACTIVATION:\nstatus=draft protocol_path=.affent/loops/demo/LOOP.md calibration_questions=1 calibration_answers=1\nnext_action: patch_draft compact existing sections, then complete_activation without protocol"
		},
	}

	if err := loop.appendUserMessage("turn_loop_draft", "activation answer", TurnOptions{}); err != nil {
		t.Fatal(err)
	}
	select {
	case ev := <-events:
		if ev.Type != sse.TypeContextInjected {
			t.Fatalf("event type = %q, want %q", ev.Type, sse.TypeContextInjected)
		}
		var payload sse.ContextInjectedPayload
		if err := json.Unmarshal(ev.Data, &payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		if payload.Source != "loop_protocol_activation" ||
			payload.Title != "Loop draft activation context injected" ||
			!strings.Contains(payload.Preview, "status=draft") {
			t.Fatalf("loop draft activation payload = %+v", payload)
		}
	default:
		t.Fatal("expected loop draft activation context event")
	}
}

func TestAppendUserMessagePrunesPreviousTransientContext(t *testing.T) {
	conv, err := OpenConversationAt(filepath.Join(t.TempDir(), "session.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	loop := &Loop{
		Conv:   conv,
		Events: make(chan sse.Event, 8),
		SkillProvider: func(userText string) string {
			return "runtime context for " + userText
		},
	}

	if err := loop.appendUserMessage("turn_1", "first", TurnOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := conv.Append(ChatMessage{Role: "assistant", Content: "first done"}); err != nil {
		t.Fatal(err)
	}
	if err := loop.appendUserMessage("turn_2", "second", TurnOptions{}); err != nil {
		t.Fatal(err)
	}

	msgs := conv.Snapshot()
	var transientCount int
	for _, msg := range msgs {
		if msg.TransientContext {
			transientCount++
			if !strings.Contains(msg.Content, "second") {
				t.Fatalf("stale transient context survived: %+v", msgs)
			}
		}
		if strings.Contains(msg.Content, "runtime context for first") {
			t.Fatalf("first turn transient context should be pruned: %+v", msgs)
		}
	}
	if transientCount != 1 {
		t.Fatalf("transient context count = %d, want 1: %+v", transientCount, msgs)
	}
}

func TestContextInjectedPayloadSkipsLoopProtocolBlock(t *testing.T) {
	payload, ok := contextInjectedPayload("turn_loop", "AFFENT LOOP PROTOCOL:\nfeed_mode=full feed_number=1", nil)
	if ok {
		t.Fatalf("loop protocol has a dedicated event; got context payload %+v", payload)
	}
}

func TestContextInjectedPayloadCapturesResearchCheckpoint(t *testing.T) {
	payload, ok := contextInjectedPayload("turn_research", researchCheckpointSkillMarker+"\nDo a bounded external calibration before changing durable direction.", nil)
	if !ok {
		t.Fatal("research checkpoint should emit context.injected metadata")
	}
	if payload.Source != "research_checkpoint" ||
		payload.Title != "Research checkpoint injected" ||
		!strings.Contains(payload.Summary, "external-calibration") ||
		!strings.Contains(payload.Preview, researchCheckpointSkillMarker) {
		t.Fatalf("research checkpoint payload = %+v", payload)
	}
}

func TestAppendUserMessagePublishesLoopFeedWhenOtherContextPrecedesIt(t *testing.T) {
	conv, err := OpenConversationAt(filepath.Join(t.TempDir(), "session.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	events := make(chan sse.Event, 4)
	loop := &Loop{
		Conv:   conv,
		Events: events,
		SkillProvider: func(string) string {
			return "AFFENT ACCOUNT ACCESS:\n- Configured environment variables available to shell commands: GITHUB_TOKEN.\n\n" +
				"AFFENT LOOP PROTOCOL:\nfeed_mode=full feed_number=2 protocol_path=.affent/loops/demo/LOOP.md\n"
		},
	}

	if err := loop.appendUserMessage("turn_loop", "continue", TurnOptions{}); err != nil {
		t.Fatal(err)
	}

	var sawContext, sawLoop bool
	for i := 0; i < 2; i++ {
		select {
		case ev := <-events:
			switch ev.Type {
			case sse.TypeContextInjected:
				sawContext = true
			case sse.TypeLoopProtocolFeed:
				var payload sse.LoopProtocolFeedPayload
				if err := json.Unmarshal(ev.Data, &payload); err != nil {
					t.Fatalf("decode loop payload: %v", err)
				}
				if payload.TurnID != "turn_loop" || payload.Mode != "full" || payload.FeedNumber != 2 {
					t.Fatalf("loop payload = %+v", payload)
				}
				sawLoop = true
			default:
				t.Fatalf("unexpected event type %q", ev.Type)
			}
		default:
			t.Fatalf("expected event %d", i+1)
		}
	}
	if !sawContext || !sawLoop {
		t.Fatalf("saw context=%v loop=%v", sawContext, sawLoop)
	}
}
