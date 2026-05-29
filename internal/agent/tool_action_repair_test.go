package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRepairLoopProtocolArgsForActionDropsStartSetupProtocol(t *testing.T) {
	args := json.RawMessage(`{
		"action": "start_setup",
		"goal": "Build a CLI app",
		"protocol": "# Loop Protocol\n\nlarge stale draft",
		"sections_changed": ["Goal"]
	}`)
	got, repaired, notes := repairToolArgsForAction(LoopProtocolToolName, args)
	if !repaired {
		t.Fatal("expected loop_protocol start_setup args to be repaired")
	}
	var obj map[string]any
	if err := json.Unmarshal(got, &obj); err != nil {
		t.Fatalf("unmarshal repaired args: %v", err)
	}
	if obj["action"] != "start_setup" || obj["goal"] != "Build a CLI app" {
		t.Fatalf("required start_setup args were not preserved: %s", got)
	}
	if _, ok := obj["protocol"]; ok {
		t.Fatalf("protocol should be dropped from start_setup args: %s", got)
	}
	if _, ok := obj["sections_changed"]; ok {
		t.Fatalf("sections_changed should be dropped from start_setup args: %s", got)
	}
	if joined := strings.Join(notes, "\n"); !strings.Contains(joined, "action=start_setup") || !strings.Contains(joined, "protocol") {
		t.Fatalf("repair notes should explain dropped start_setup fields: %v", notes)
	}
}

func TestRepairLoopProtocolArgsForActionDropsMalformedActivationProtocol(t *testing.T) {
	args := json.RawMessage(`{
		"action": "complete_activation",
		"protocol": "# Loop Protocol — stale payload without metadata\n\n## Goal\nWrong task",
		"reason": "calibration answered",
		"goal": "unused"
	}`)
	got, repaired, notes := repairToolArgsForAction(LoopProtocolToolName, args)
	if !repaired {
		t.Fatal("expected malformed complete_activation protocol payload to be repaired")
	}
	var obj map[string]any
	if err := json.Unmarshal(got, &obj); err != nil {
		t.Fatalf("unmarshal repaired args: %v", err)
	}
	if obj["action"] != "complete_activation" || obj["reason"] != "calibration answered" {
		t.Fatalf("required activation args were not preserved: %s", got)
	}
	if _, ok := obj["protocol"]; ok {
		t.Fatalf("malformed protocol should be dropped from complete_activation args: %s", got)
	}
	if _, ok := obj["goal"]; ok {
		t.Fatalf("goal should be dropped from complete_activation args: %s", got)
	}
	if joined := strings.Join(notes, "\n"); !strings.Contains(joined, "missing LOOP.md metadata") {
		t.Fatalf("repair notes should explain malformed activation protocol: %v", notes)
	}
}

func TestRepairLoopProtocolArgsForActionKeepsValidActivationProtocol(t *testing.T) {
	valid := `# Loop Protocol: loop

## 0. Metadata

- loop_id: loop
- owner_session: loop
- status: draft

## 1. North Star

Long-term objective:

1. Build a CLI app.
`
	args, err := json.Marshal(map[string]any{
		"action":   "complete_activation",
		"protocol": valid,
		"reason":   "calibration answered",
	})
	if err != nil {
		t.Fatal(err)
	}
	got, repaired, notes := repairToolArgsForAction(LoopProtocolToolName, args)
	if repaired {
		t.Fatalf("valid activation protocol should not be repaired: got=%s notes=%v", got, notes)
	}
}
