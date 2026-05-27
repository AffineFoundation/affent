package agent

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/affinefoundation/affent/internal/loopstate"
)

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
	if err == nil || !strings.Contains(err.Error(), "requires a user calibration answer") || !strings.Contains(err.Error(), "ask one concise calibration question") {
		t.Fatalf("complete_activation without calibration err = %v", err)
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
		!strings.Contains(prompt, "at least one concise calibration question") ||
		!strings.Contains(prompt, "Do not complete activation in the same turn") ||
		!strings.Contains(prompt, "Never claim that a loop is running") ||
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
