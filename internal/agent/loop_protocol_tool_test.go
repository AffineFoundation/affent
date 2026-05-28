package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/affinefoundation/affent/internal/loopstate"
)

func TestLoopProtocolToolStartsSetupFromChat(t *testing.T) {
	dir := t.TempDir()
	path := loopstate.ProtocolPath(dir, "chat-loop")
	tool := loopProtocolTool(path)
	out, err := tool.Execute(context.Background(), json.RawMessage(mustMarshalJSON(t, map[string]any{
		"action": "start_setup",
		"goal":   "Run multi-day subnet research with stable recovery context.",
	})))
	if err != nil {
		t.Fatalf("start_setup: %v", err)
	}
	if !strings.Contains(out, "initialized LOOP.md draft status=draft") || !strings.Contains(out, "ask one concise calibration question") {
		t.Fatalf("start_setup output = %q", out)
	}
	protocol, found, err := loopstate.ReadProtocol(path)
	if err != nil || !found {
		t.Fatalf("ReadProtocol found=%v err=%v", found, err)
	}
	if loopstate.ProtocolStatus(protocol) != "draft" || !strings.Contains(protocol, "Run multi-day subnet research") {
		t.Fatalf("protocol after start_setup:\n%s", protocol)
	}
	state, found, err := loopstate.ReadState(filepath.Join(filepath.Dir(path), loopstate.StateFileName))
	if err != nil || !found {
		t.Fatalf("ReadState found=%v err=%v", found, err)
	}
	if state.Status != "draft" || state.CalibrationAnswers != 0 || state.LastEventType != "loop.protocol_init" {
		t.Fatalf("state = %+v", state)
	}
	if _, err := os.Stat(filepath.Dir(path)); err != nil {
		t.Fatalf("loop dir not created: %v", err)
	}
}

func TestLoopProtocolToolCompletesActivation(t *testing.T) {
	dir := t.TempDir()
	path := loopstate.ProtocolPath(dir, "longrun")
	if _, _, _, err := loopstate.EnsureProtocolTemplate(path, loopstate.ProtocolTemplateOptions{
		LoopID:       "longrun",
		OwnerSession: "longrun",
		Goal:         "Run a long market analysis without losing recovery context.",
		Status:       "draft",
	}); err != nil {
		t.Fatalf("EnsureProtocolTemplate: %v", err)
	}
	tool := loopProtocolTool(path)
	read, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"read"}`))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(read, "status=draft") || !strings.Contains(read, "Current Situation") {
		t.Fatalf("read result missing draft/current situation:\n%s", read)
	}
	protocol, found, err := loopstate.ReadProtocol(path)
	if err != nil || !found {
		t.Fatalf("ReadProtocol found=%v err=%v", found, err)
	}
	protocol = strings.Replace(protocol, "- status: draft", "- status: running", 1)
	protocol = strings.Replace(protocol, "- hard constraints:", "- hard constraints: keep evidence cited and stop on unresolved user intent", 1)
	protocol = strings.Replace(protocol, "- known evidence:", "- known evidence: user requested durable market analysis", 1)
	protocol = strings.Replace(protocol, "- current risk or blocker:", "- current risk or blocker: needs live source verification", 1)
	protocol = strings.Replace(protocol, "- important artifacts:", "- important artifacts: none yet", 1)
	protocol = strings.Replace(protocol, "- important trace spans:", "- important trace spans: loop activation draft", 1)
	protocol = strings.Replace(protocol, "- last known recovery note:", "- last known recovery note: reload LOOP.md and plan state before continuing", 1)
	if _, _, err := loopstate.RecordProtocolCalibrationQuestion(path, "What stop condition should pause this loop?"); err != nil {
		t.Fatalf("RecordProtocolCalibrationQuestion: %v", err)
	}
	if _, _, err := loopstate.RecordProtocolCalibrationAnswer(path, "Stop if live source quality is too weak; remember source rules in LOOP.md."); err != nil {
		t.Fatalf("RecordProtocolCalibrationAnswer: %v", err)
	}
	out, err := tool.Execute(context.Background(), json.RawMessage(mustMarshalJSON(t, map[string]any{
		"action":           "complete_activation",
		"protocol":         protocol,
		"reason":           "user intent understood",
		"sections_changed": []string{"Current Situation", "Rules"},
	})))
	if err != nil {
		t.Fatalf("complete_activation: %v", err)
	}
	if !strings.Contains(out, "activated LOOP.md status=running") {
		t.Fatalf("activation output = %q", out)
	}
	state, found, err := loopstate.ReadState(filepath.Join(filepath.Dir(path), loopstate.StateFileName))
	if err != nil || !found {
		t.Fatalf("ReadState found=%v err=%v", found, err)
	}
	if state.Status != "running" || state.LastEventType != "loop.protocol_activate" {
		t.Fatalf("state = %+v", state)
	}
}

func TestLoopProtocolToolCompletesActivationFromSavedDraftWhenProtocolPayloadHasNoMetadata(t *testing.T) {
	dir := t.TempDir()
	path := loopstate.ProtocolPath(dir, "longrun")
	if _, _, _, err := loopstate.EnsureProtocolTemplate(path, loopstate.ProtocolTemplateOptions{
		LoopID:       "longrun",
		OwnerSession: "longrun",
		Goal:         "Run a long market analysis without losing recovery context.",
		Status:       "draft",
	}); err != nil {
		t.Fatalf("EnsureProtocolTemplate: %v", err)
	}
	protocol, found, err := loopstate.ReadProtocol(path)
	if err != nil || !found {
		t.Fatalf("ReadProtocol found=%v err=%v", found, err)
	}
	for _, replacement := range [][2]string{
		{"- hard constraints:", "- hard constraints: keep evidence cited and stop on unresolved user intent"},
		{"- known evidence:", "- known evidence: user requested durable market analysis"},
		{"- current risk or blocker:", "- current risk or blocker: needs live source verification"},
		{"- important artifacts:", "- important artifacts: none yet"},
		{"- important trace spans:", "- important trace spans: loop activation draft"},
		{"- last known recovery note:", "- last known recovery note: reload LOOP.md and plan state before continuing"},
	} {
		protocol = strings.Replace(protocol, replacement[0], replacement[1], 1)
	}
	if err := loopstate.WriteProtocol(path, protocol); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loopstate.RecordProtocolCalibrationQuestion(path, "What stop condition should pause this loop?"); err != nil {
		t.Fatalf("RecordProtocolCalibrationQuestion: %v", err)
	}
	if _, _, err := loopstate.RecordProtocolCalibrationAnswer(path, "Stop if live source quality is too weak."); err != nil {
		t.Fatalf("RecordProtocolCalibrationAnswer: %v", err)
	}

	tool := loopProtocolTool(path)
	out, err := tool.Execute(context.Background(), json.RawMessage(mustMarshalJSON(t, map[string]any{
		"action":   "complete_activation",
		"protocol": "# Loop Protocol — legacy payload without metadata\n\n## Goal\nRun a different stale loop.",
		"reason":   "user intent understood",
	})))
	if err != nil {
		t.Fatalf("complete_activation: %v", err)
	}
	if !strings.Contains(out, "activated LOOP.md status=running") ||
		!strings.Contains(out, "ignored_protocol_payload=missing_metadata") {
		t.Fatalf("activation output = %q", out)
	}
	got, found, err := loopstate.ReadProtocol(path)
	if err != nil || !found {
		t.Fatalf("ReadProtocol after activation found=%v err=%v", found, err)
	}
	if loopstate.ProtocolStatus(got) != "running" ||
		!strings.Contains(got, "Run a long market analysis") ||
		strings.Contains(got, "different stale loop") {
		t.Fatalf("activation should use saved draft, got:\n%s", got)
	}
}

func TestLoopProtocolToolMalformedActivationPayloadValidatesSavedDraft(t *testing.T) {
	dir := t.TempDir()
	path := loopstate.ProtocolPath(dir, "longrun")
	if _, _, _, err := loopstate.EnsureProtocolTemplate(path, loopstate.ProtocolTemplateOptions{
		LoopID:       "longrun",
		OwnerSession: "longrun",
		Goal:         "Build and verify a small CLI project.",
		Status:       "draft",
	}); err != nil {
		t.Fatalf("EnsureProtocolTemplate: %v", err)
	}
	if _, _, err := loopstate.RecordProtocolCalibrationQuestion(path, "What should pause this loop?"); err != nil {
		t.Fatalf("RecordProtocolCalibrationQuestion: %v", err)
	}
	if _, _, err := loopstate.RecordProtocolCalibrationAnswer(path, "Stop after tests pass and the code is committed."); err != nil {
		t.Fatalf("RecordProtocolCalibrationAnswer: %v", err)
	}

	tool := loopProtocolTool(path)
	_, err := tool.Execute(context.Background(), json.RawMessage(mustMarshalJSON(t, map[string]any{
		"action":   "complete_activation",
		"protocol": "# Loop Protocol — stale payload without metadata\n\n## Goal\nBuild something else.",
		"reason":   "calibration answered",
	})))
	if err == nil {
		t.Fatal("complete_activation unexpectedly succeeded")
	}
	if !strings.Contains(err.Error(), loopProtocolActivationInvalidFailureKind) ||
		!strings.Contains(err.Error(), "unresolved activation placeholder") ||
		strings.Contains(err.Error(), loopProtocolActivationStatusFailureKind) {
		t.Fatalf("complete_activation error should validate saved draft, got:\n%v", err)
	}
	got, found, readErr := loopstate.ReadProtocol(path)
	if readErr != nil || !found {
		t.Fatalf("ReadProtocol after failed activation found=%v err=%v", found, readErr)
	}
	if loopstate.ProtocolStatus(got) != "draft" || strings.Contains(got, "Build something else") {
		t.Fatalf("failed activation should keep saved draft unchanged:\n%s", got)
	}
}

func TestLoopProtocolToolClosesCompletedLoopAndDisablesFeed(t *testing.T) {
	dir := t.TempDir()
	path := loopstate.ProtocolPath(dir, "longrun")
	if _, _, _, err := loopstate.EnsureProtocolTemplate(path, loopstate.ProtocolTemplateOptions{
		LoopID:       "longrun",
		OwnerSession: "longrun",
		Goal:         "Build and push a small CLI game.",
		Status:       "running",
	}); err != nil {
		t.Fatalf("EnsureProtocolTemplate: %v", err)
	}
	tool := loopProtocolTool(path)
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"close","status":"completed","reason":"implementation committed, tests passed, and code pushed"}`))
	if err != nil {
		t.Fatalf("close: %v", err)
	}
	if !strings.Contains(out, "closed LOOP.md status=completed") {
		t.Fatalf("close output = %q", out)
	}
	protocol, found, err := loopstate.ReadProtocol(path)
	if err != nil || !found {
		t.Fatalf("ReadProtocol found=%v err=%v", found, err)
	}
	if loopstate.ProtocolStatus(protocol) != "completed" {
		t.Fatalf("protocol status = %q\n%s", loopstate.ProtocolStatus(protocol), protocol)
	}
	state, found, err := loopstate.ReadState(filepath.Join(filepath.Dir(path), loopstate.StateFileName))
	if err != nil || !found {
		t.Fatalf("ReadState found=%v err=%v", found, err)
	}
	if state.Status != "completed" || state.LastEventType != "loop.protocol_status" || state.NeedsFullProtocolFeed {
		t.Fatalf("state = %+v", state)
	}
	if got := activeLoopProtocolSkillBlock(path); got != "" {
		t.Fatalf("completed protocol should not be fed, got:\n%s", got)
	}
}

func TestLoopProtocolToolCompletesActivationFromSavedDraft(t *testing.T) {
	dir := t.TempDir()
	path := loopstate.ProtocolPath(dir, "longrun")
	if _, _, _, err := loopstate.EnsureProtocolTemplate(path, loopstate.ProtocolTemplateOptions{
		LoopID:       "longrun",
		OwnerSession: "longrun",
		Goal:         "Run a long market analysis without losing recovery context.",
		Status:       "draft",
	}); err != nil {
		t.Fatalf("EnsureProtocolTemplate: %v", err)
	}
	protocol, found, err := loopstate.ReadProtocol(path)
	if err != nil || !found {
		t.Fatalf("ReadProtocol found=%v err=%v", found, err)
	}
	for _, replacement := range [][2]string{
		{"- hard constraints:", "- hard constraints: keep evidence cited and stop on unresolved user intent"},
		{"- known evidence:", "- known evidence: user requested durable market analysis"},
		{"- current risk or blocker:", "- current risk or blocker: needs live source verification"},
		{"- important artifacts:", "- important artifacts: none yet"},
		{"- important trace spans:", "- important trace spans: loop activation draft"},
		{"- last known recovery note:", "- last known recovery note: reload LOOP.md and plan state before continuing"},
	} {
		protocol = strings.Replace(protocol, replacement[0], replacement[1], 1)
	}
	if err := loopstate.WriteProtocol(path, protocol); err != nil {
		t.Fatalf("WriteProtocol: %v", err)
	}
	if _, _, err := loopstate.RecordProtocolCalibrationQuestion(path, "What stop condition should pause this loop?"); err != nil {
		t.Fatalf("RecordProtocolCalibrationQuestion: %v", err)
	}
	if _, _, err := loopstate.RecordProtocolCalibrationAnswer(path, "Stop if live source quality is too weak."); err != nil {
		t.Fatalf("RecordProtocolCalibrationAnswer: %v", err)
	}
	tool := loopProtocolTool(path)
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"complete_activation","reason":"user intent understood"}`))
	if err != nil {
		t.Fatalf("complete_activation: %v", err)
	}
	if !strings.Contains(out, "activated LOOP.md status=running") {
		t.Fatalf("activation output = %q", out)
	}
	written, found, err := loopstate.ReadProtocol(path)
	if err != nil || !found {
		t.Fatalf("ReadProtocol after activation found=%v err=%v", found, err)
	}
	if loopstate.ProtocolStatus(written) != "running" {
		t.Fatalf("protocol status after activation = %q\n%s", loopstate.ProtocolStatus(written), written)
	}
}

func TestLoopProtocolToolPatchesDraftBeforeActivation(t *testing.T) {
	dir := t.TempDir()
	path := loopstate.ProtocolPath(dir, "longrun")
	if _, _, _, err := loopstate.EnsureProtocolTemplate(path, loopstate.ProtocolTemplateOptions{
		LoopID:       "longrun",
		OwnerSession: "longrun",
		Goal:         "Build and maintain a small CLI project until verified.",
		Status:       "draft",
	}); err != nil {
		t.Fatalf("EnsureProtocolTemplate: %v", err)
	}
	if _, _, err := loopstate.RecordProtocolCalibrationQuestion(path, "What stop condition should pause this loop?"); err != nil {
		t.Fatalf("RecordProtocolCalibrationQuestion: %v", err)
	}
	if _, _, err := loopstate.RecordProtocolCalibrationAnswer(path, "Stop after tests pass and changes are committed."); err != nil {
		t.Fatalf("RecordProtocolCalibrationAnswer: %v", err)
	}
	tool := loopProtocolTool(path)
	out, err := tool.Execute(context.Background(), json.RawMessage(mustMarshalJSON(t, map[string]any{
		"action": "patch_draft",
		"reason": "calibration answered; fill activation placeholders without resending full LOOP.md",
		"patches": []map[string]string{
			{
				"heading": "## 2. Current Situation",
				"body": strings.Join([]string{
					"Keep this section short: at most 8 bullets or about 1200 characters. It is the compact global snapshot used after long runs, compaction, or session recovery.",
					"",
					"- current intent: build and verify the CLI project",
					"- hard constraints: stop after tests pass and code is committed",
					"- known evidence: user answered the loop stop condition",
					"- current risk or blocker: implementation has not started",
					"- next recovery anchor: inspect plan state, LOOP.md, and recent trace before continuing",
				}, "\n"),
			},
			{
				"heading": "## 7. Evidence And Recovery Index",
				"body": strings.Join([]string{
					"Keep this section short. Store detailed history in artifacts or trace.",
					"",
					"- loop state: state.json and events.jsonl",
					"- memory lookup: use memory only for stable facts and lessons",
					"- important artifacts: none yet",
					"- important trace spans: loop setup calibration answered",
					"- last known recovery note: continue from plan state and verify before claiming completion",
				}, "\n"),
			},
		},
	})))
	if err != nil {
		t.Fatalf("patch_draft: %v", err)
	}
	if !strings.Contains(out, "patched LOOP.md draft status=draft") ||
		!strings.Contains(out, "complete_activation without protocol") {
		t.Fatalf("patch output = %q", out)
	}
	out, err = tool.Execute(context.Background(), json.RawMessage(`{"action":"complete_activation","reason":"calibration answered and placeholders resolved"}`))
	if err != nil {
		t.Fatalf("complete_activation after patch: %v", err)
	}
	if !strings.Contains(out, "activated LOOP.md status=running") {
		t.Fatalf("activation output = %q", out)
	}
	written, found, err := loopstate.ReadProtocol(path)
	if err != nil || !found {
		t.Fatalf("ReadProtocol found=%v err=%v", found, err)
	}
	if loopstate.ProtocolStatus(written) != "running" ||
		!strings.Contains(written, "loop setup calibration answered") ||
		strings.Contains(written, "- known evidence:\n") {
		t.Fatalf("patched protocol not activated cleanly:\n%s", written)
	}
	state, found, err := loopstate.ReadState(filepath.Join(filepath.Dir(path), loopstate.StateFileName))
	if err != nil || !found {
		t.Fatalf("ReadState found=%v err=%v", found, err)
	}
	if state.ProtocolUpdates != 3 || state.Status != "running" {
		t.Fatalf("state after patch+activation = %+v, want updates=3 running", state)
	}
}

func TestLoopProtocolToolPatchDraftRejectsUnknownHeading(t *testing.T) {
	dir := t.TempDir()
	path := loopstate.ProtocolPath(dir, "longrun")
	if _, _, _, err := loopstate.EnsureProtocolTemplate(path, loopstate.ProtocolTemplateOptions{
		LoopID: "longrun",
		Goal:   "Keep a loop recoverable.",
		Status: "draft",
	}); err != nil {
		t.Fatalf("EnsureProtocolTemplate: %v", err)
	}
	tool := loopProtocolTool(path)
	_, err := tool.Execute(context.Background(), json.RawMessage(mustMarshalJSON(t, map[string]any{
		"action": "patch_draft",
		"patches": []map[string]string{{
			"heading": "## 9. Unknown",
			"body":    "details",
		}},
	})))
	if err == nil || !strings.Contains(err.Error(), "unsupported patch heading") {
		t.Fatalf("patch_draft unknown heading err = %v", err)
	}
}

func TestLoopProtocolToolCompletesActivationFromRecordedCalibrationSection(t *testing.T) {
	dir := t.TempDir()
	path := loopstate.ProtocolPath(dir, "longrun")
	if _, _, _, err := loopstate.EnsureProtocolTemplate(path, loopstate.ProtocolTemplateOptions{
		LoopID:       "longrun",
		OwnerSession: "longrun",
		Goal:         "Run a long market analysis without losing recovery context.",
		Status:       "draft",
	}); err != nil {
		t.Fatalf("EnsureProtocolTemplate: %v", err)
	}
	protocol, found, err := loopstate.ReadProtocol(path)
	if err != nil || !found {
		t.Fatalf("ReadProtocol found=%v err=%v", found, err)
	}
	protocol = strings.Replace(protocol, "# Loop Protocol: longrun", `# Loop Protocol: longrun

## Calibration Q&A (recorded)

- **Q1**: What stop condition should pause this loop? A: Stop if live source quality is too weak.`, 1)
	for _, replacement := range [][2]string{
		{"- status: draft", "- status: running"},
		{"- hard constraints:", "- hard constraints: keep evidence cited and stop on unresolved user intent"},
		{"- known evidence:", "- known evidence: user requested durable market analysis"},
		{"- current risk or blocker:", "- current risk or blocker: needs live source verification"},
		{"- important artifacts:", "- important artifacts: none yet"},
		{"- important trace spans:", "- important trace spans: loop activation draft"},
		{"- last known recovery note:", "- last known recovery note: reload LOOP.md and plan state before continuing"},
	} {
		protocol = strings.Replace(protocol, replacement[0], replacement[1], 1)
	}
	tool := loopProtocolTool(path)
	out, err := tool.Execute(context.Background(), json.RawMessage(mustMarshalJSON(t, map[string]any{
		"action":   "complete_activation",
		"protocol": protocol,
		"reason":   "user intent understood from recorded calibration",
	})))
	if err != nil {
		t.Fatalf("complete_activation: %v", err)
	}
	if !strings.Contains(out, "activated LOOP.md status=running") {
		t.Fatalf("activation output = %q", out)
	}
	state, found, err := loopstate.ReadState(filepath.Join(filepath.Dir(path), loopstate.StateFileName))
	if err != nil || !found {
		t.Fatalf("ReadState found=%v err=%v", found, err)
	}
	if state.Status != "running" ||
		state.CalibrationQuestions != 1 ||
		state.CalibrationAnswers != 1 ||
		!strings.Contains(state.LastCalibrationAnswer, "source quality") {
		t.Fatalf("state = %+v", state)
	}
}

func TestLoopProtocolToolRejectsActivationBeforeCalibrationAnswer(t *testing.T) {
	dir := t.TempDir()
	path := loopstate.ProtocolPath(dir, "longrun")
	if _, _, _, err := loopstate.EnsureProtocolTemplate(path, loopstate.ProtocolTemplateOptions{
		LoopID:       "longrun",
		OwnerSession: "longrun",
		Goal:         "Run a long market analysis without losing recovery context.",
		Status:       "draft",
	}); err != nil {
		t.Fatalf("EnsureProtocolTemplate: %v", err)
	}
	protocol, found, err := loopstate.ReadProtocol(path)
	if err != nil || !found {
		t.Fatalf("ReadProtocol found=%v err=%v", found, err)
	}
	protocol = strings.Replace(protocol, "- status: draft", "- status: running", 1)
	protocol = strings.Replace(protocol, "- hard constraints:", "- hard constraints: keep evidence cited and stop on unresolved user intent", 1)
	protocol = strings.Replace(protocol, "- known evidence:", "- known evidence: user requested durable market analysis", 1)
	protocol = strings.Replace(protocol, "- current risk or blocker:", "- current risk or blocker: needs live source verification", 1)
	protocol = strings.Replace(protocol, "- important artifacts:", "- important artifacts: none yet", 1)
	protocol = strings.Replace(protocol, "- important trace spans:", "- important trace spans: loop activation draft", 1)
	protocol = strings.Replace(protocol, "- last known recovery note:", "- last known recovery note: reload LOOP.md and plan state before continuing", 1)
	tool := loopProtocolTool(path)
	_, err = tool.Execute(context.Background(), json.RawMessage(mustMarshalJSON(t, map[string]any{
		"action":   "complete_activation",
		"protocol": protocol,
		"reason":   "premature activation",
	})))
	if err == nil || !strings.Contains(err.Error(), "requires a recorded calibration question and user answer") || !strings.Contains(err.Error(), "ask one concise calibration question") {
		t.Fatalf("complete_activation without calibration err = %v", err)
	}
	if !strings.Contains(err.Error(), "retry loop_protocol action=complete_activation") ||
		!strings.Contains(err.Error(), "Failure: kind=loop_protocol_activation_unready") {
		t.Fatalf("complete_activation without calibration should include failure kind, err = %v", err)
	}
}

func TestLoopProtocolToolRejectsUnresolvedActivationPlaceholders(t *testing.T) {
	dir := t.TempDir()
	path := loopstate.ProtocolPath(dir, "longrun")
	if _, _, _, err := loopstate.EnsureProtocolTemplate(path, loopstate.ProtocolTemplateOptions{
		LoopID:       "longrun",
		OwnerSession: "longrun",
		Goal:         "Run a long market analysis without losing recovery context.",
		Status:       "draft",
	}); err != nil {
		t.Fatalf("EnsureProtocolTemplate: %v", err)
	}
	protocol, found, err := loopstate.ReadProtocol(path)
	if err != nil || !found {
		t.Fatalf("ReadProtocol found=%v err=%v", found, err)
	}
	protocol = strings.Replace(protocol, "- status: draft", "- status: running", 1)
	tool := loopProtocolTool(path)
	_, err = tool.Execute(context.Background(), json.RawMessage(mustMarshalJSON(t, map[string]any{
		"action":   "complete_activation",
		"protocol": protocol,
		"reason":   "premature activation",
	})))
	if err == nil || !strings.Contains(err.Error(), "unresolved activation placeholder") || !strings.Contains(err.Error(), "keep status=draft") {
		t.Fatalf("complete_activation unresolved placeholders err = %v", err)
	}
	if !strings.Contains(err.Error(), "Failure: kind=loop_protocol_activation_invalid") {
		t.Fatalf("complete_activation unresolved placeholders should include failure kind, err = %v", err)
	}
}

func TestLoopProtocolToolRejectsOversizedCurrentSituationWithFailureKind(t *testing.T) {
	dir := t.TempDir()
	path := loopstate.ProtocolPath(dir, "longrun")
	if _, _, _, err := loopstate.EnsureProtocolTemplate(path, loopstate.ProtocolTemplateOptions{
		LoopID:       "longrun",
		OwnerSession: "longrun",
		Goal:         "Run a long market analysis without losing recovery context.",
		Status:       "draft",
	}); err != nil {
		t.Fatalf("EnsureProtocolTemplate: %v", err)
	}
	protocol, found, err := loopstate.ReadProtocol(path)
	if err != nil || !found {
		t.Fatalf("ReadProtocol found=%v err=%v", found, err)
	}
	for _, replacement := range [][2]string{
		{"- status: draft", "- status: running"},
		{"- hard constraints:", "- hard constraints: keep evidence cited and stop on unresolved user intent"},
		{"- known evidence:", "- known evidence: user requested durable market analysis"},
		{"- current risk or blocker:", "- current risk or blocker: needs live source verification"},
		{"- important artifacts:", "- important artifacts: none yet"},
		{"- important trace spans:", "- important trace spans: loop activation draft"},
		{"- last known recovery note:", "- last known recovery note: reload LOOP.md and plan state before continuing"},
	} {
		protocol = strings.Replace(protocol, replacement[0], replacement[1], 1)
	}
	protocol = strings.Replace(
		protocol,
		"- next recovery anchor: check plan state, recent trace, memory search/list, and this protocol before continuing",
		"- next recovery anchor: check plan state, recent trace, memory search/list, and this protocol before continuing\n- overflow: "+strings.Repeat("status ", loopstate.MaxCurrentSituationChars/len("status ")+30),
		1,
	)
	if _, _, err := loopstate.RecordProtocolCalibrationQuestion(path, "What stop condition should pause this loop?"); err != nil {
		t.Fatalf("RecordProtocolCalibrationQuestion: %v", err)
	}
	if _, _, err := loopstate.RecordProtocolCalibrationAnswer(path, "Stop if live source quality is too weak."); err != nil {
		t.Fatalf("RecordProtocolCalibrationAnswer: %v", err)
	}
	tool := loopProtocolTool(path)
	_, err = tool.Execute(context.Background(), json.RawMessage(mustMarshalJSON(t, map[string]any{
		"action":   "complete_activation",
		"protocol": protocol,
		"reason":   "oversized current situation",
	})))
	if err == nil ||
		!strings.Contains(err.Error(), "Current Situation") ||
		!strings.Contains(err.Error(), "Failure: kind=loop_protocol_activation_invalid") ||
		!strings.Contains(err.Error(), "keep Current Situation compact") {
		t.Fatalf("complete_activation oversized current situation err = %v", err)
	}
}

func TestLoopProtocolToolDraftUpdateDoesNotActivate(t *testing.T) {
	dir := t.TempDir()
	path := loopstate.ProtocolPath(dir, "draft")
	if _, _, _, err := loopstate.EnsureProtocolTemplate(path, loopstate.ProtocolTemplateOptions{
		LoopID: "draft",
		Goal:   "Understand the user before enabling loop.",
		Status: "draft",
	}); err != nil {
		t.Fatalf("EnsureProtocolTemplate: %v", err)
	}
	protocol, _, _ := loopstate.ReadProtocol(path)
	protocol = strings.Replace(protocol, "- current risk or blocker:", "- current risk or blocker: missing stop condition", 1)
	tool := loopProtocolTool(path)
	out, err := tool.Execute(context.Background(), json.RawMessage(mustMarshalJSON(t, map[string]any{
		"action":           "update_draft",
		"protocol":         protocol,
		"reason":           "need user clarification",
		"sections_changed": []string{"Current Situation"},
	})))
	if err != nil {
		t.Fatalf("update_draft: %v", err)
	}
	if !strings.Contains(out, "updated LOOP.md draft status=draft") {
		t.Fatalf("draft update output = %q", out)
	}
	state, _, _ := loopstate.ReadState(filepath.Join(filepath.Dir(path), loopstate.StateFileName))
	if state.Status != "draft" || state.LastEventType != "loop.protocol_update" {
		t.Fatalf("state = %+v", state)
	}
	running := strings.Replace(protocol, "- status: draft", "- status: running", 1)
	_, err = tool.Execute(context.Background(), json.RawMessage(mustMarshalJSON(t, map[string]any{
		"action":   "update_draft",
		"protocol": running,
	})))
	if err == nil || !strings.Contains(err.Error(), "cannot activate") {
		t.Fatalf("update_draft running err = %v", err)
	}
	if !strings.Contains(err.Error(), "tool performs the draft-to-running transition") ||
		!strings.Contains(err.Error(), "keep the saved draft status=draft") {
		t.Fatalf("update_draft running next step is ambiguous: %v", err)
	}
}

func TestLoopProtocolToolMalformedActivationPayloadUsesSavedDraft(t *testing.T) {
	dir := t.TempDir()
	path := loopstate.ProtocolPath(dir, "longrun")
	if _, _, _, err := loopstate.EnsureProtocolTemplate(path, loopstate.ProtocolTemplateOptions{
		LoopID:       "longrun",
		OwnerSession: "longrun",
		Goal:         "Run a long market analysis without losing recovery context.",
		Status:       "draft",
	}); err != nil {
		t.Fatalf("EnsureProtocolTemplate: %v", err)
	}
	protocol, found, err := loopstate.ReadProtocol(path)
	if err != nil || !found {
		t.Fatalf("ReadProtocol found=%v err=%v", found, err)
	}
	protocol = strings.Replace(protocol, "- status: draft\n", "", 1)
	tool := loopProtocolTool(path)
	_, err = tool.Execute(context.Background(), json.RawMessage(mustMarshalJSON(t, map[string]any{
		"action":   "complete_activation",
		"protocol": protocol,
	})))
	if err == nil ||
		!strings.Contains(err.Error(), "unresolved activation placeholder") ||
		!strings.Contains(err.Error(), "Failure: kind=loop_protocol_activation_invalid") ||
		strings.Contains(err.Error(), "Failure: kind=loop_protocol_activation_status") {
		t.Fatalf("complete_activation without status err = %v", err)
	}
}

func TestLoopProtocolFailureAlwaysIncludesNextGuidance(t *testing.T) {
	err := loopProtocolFailure("activation validation failed", loopProtocolActivationInvalidFailureKind)
	if err == nil ||
		!strings.Contains(err.Error(), "activation validation failed") ||
		!strings.Contains(err.Error(), "Next:") ||
		!strings.Contains(err.Error(), "retry loop_protocol") ||
		!strings.Contains(err.Error(), "Failure: kind=loop_protocol_activation_invalid") {
		t.Fatalf("loopProtocolFailure without next guidance = %v", err)
	}

	err = loopProtocolFailure("activation validation failed\nNext: ask one concise calibration question", loopProtocolActivationUnreadyFailureKind)
	if err == nil || strings.Count(err.Error(), "Next:") != 1 || !strings.Contains(err.Error(), "Failure: kind=loop_protocol_activation_unready") {
		t.Fatalf("loopProtocolFailure duplicated or lost next guidance = %v", err)
	}
}

func TestLoopProtocolToolRegistryGuidance(t *testing.T) {
	reg := NewRegistry()
	RegisterBuiltins(reg, BuiltinDeps{
		HostWorkspaceDir: t.TempDir(),
		LoopProtocolPath: loopstate.ProtocolPath(t.TempDir(), "loop"),
	})
	if _, ok := reg.Get(LoopProtocolToolName); !ok {
		t.Fatal("loop_protocol tool not registered")
	}
	prompt := WithRegistrySystemGuidance(BaseSystemPromptForRegistry(reg), reg)
	if !strings.Contains(prompt, "Loop protocol maintenance:") ||
		!strings.Contains(prompt, "ordinary chat") ||
		!strings.Contains(prompt, "action=start_setup") ||
		!strings.Contains(prompt, "patch_draft") ||
		!strings.Contains(prompt, "Do not tell the user to press the UI button") ||
		!strings.Contains(prompt, "exactly one concise calibration question") ||
		!strings.Contains(prompt, "one focused follow-up in a later turn") ||
		!strings.Contains(prompt, "Do not complete activation in the same turn") ||
		!strings.Contains(prompt, "Never claim that a loop is running") ||
		!strings.Contains(prompt, "action=close") ||
		!strings.Contains(prompt, "draft-to-running transition") ||
		!strings.Contains(prompt, "do not use update_draft for running status") ||
		!strings.Contains(prompt, "complete_activation") {
		t.Fatalf("prompt missing loop protocol guidance:\n%s", prompt)
	}
}

func mustMarshalJSON(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
