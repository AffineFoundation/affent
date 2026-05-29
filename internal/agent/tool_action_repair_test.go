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

func TestRepairPlanArgsForActionInfersUpdate(t *testing.T) {
	args := json.RawMessage(`{
		"index": 1,
		"status": "completed",
		"evidence": ["commit b53cb8b pushed to origin/main"]
	}`)
	got, repaired, notes := repairToolArgsForAction(PlanToolName, args)
	if !repaired {
		t.Fatal("expected missing plan update action to be repaired")
	}
	var obj map[string]any
	if err := json.Unmarshal(got, &obj); err != nil {
		t.Fatalf("unmarshal repaired args: %v", err)
	}
	if obj["action"] != "update" || obj["status"] != "completed" {
		t.Fatalf("required update args were not preserved: %s", got)
	}
	if joined := strings.Join(notes, "\n"); !strings.Contains(joined, "action=update") || !strings.Contains(joined, PlanToolName) {
		t.Fatalf("repair notes should explain inferred update action: %v", notes)
	}
}

func TestRepairPlanArgsForActionInfersBatchUpdate(t *testing.T) {
	args := json.RawMessage(`{
		"updates": [
			{"index": 1, "status": "completed"},
			{"index": 2, "status": "in_progress"}
		]
	}`)
	got, repaired, notes := repairToolArgsForAction(PlanToolName, args)
	if !repaired {
		t.Fatal("expected missing plan batch update action to be repaired")
	}
	var obj map[string]any
	if err := json.Unmarshal(got, &obj); err != nil {
		t.Fatalf("unmarshal repaired args: %v", err)
	}
	if obj["action"] != "update" || obj["updates"] == nil {
		t.Fatalf("required batch update args were not preserved: %s", got)
	}
	if joined := strings.Join(notes, "\n"); !strings.Contains(joined, "action=update") {
		t.Fatalf("repair notes should explain inferred batch update action: %v", notes)
	}
}

func TestRepairPlanArgsForActionDecodesStringifiedBatchUpdate(t *testing.T) {
	args := json.RawMessage(`{
		"action": "update",
		"updates": "[{\"index\": 4, \"status\": \"completed\", \"evidence\": [\"git status clean\"]}]"
	}`)
	got, repaired, notes := repairToolArgsForAction(PlanToolName, args)
	if !repaired {
		t.Fatal("expected stringified plan updates to be repaired")
	}
	var obj struct {
		Action  string       `json:"action"`
		Updates []planUpdate `json:"updates"`
	}
	if err := json.Unmarshal(got, &obj); err != nil {
		t.Fatalf("unmarshal repaired args: %v\n%s", err, got)
	}
	if obj.Action != "update" || len(obj.Updates) != 1 || obj.Updates[0].Index != 4 || obj.Updates[0].Status != "completed" {
		t.Fatalf("decoded updates not preserved: %+v args=%s", obj, got)
	}
	if joined := strings.Join(notes, "\n"); !strings.Contains(joined, "decoded stringified JSON array field updates") {
		t.Fatalf("repair notes should explain decoded updates: %v", notes)
	}
}

func TestRepairPlanArgsForActionCompletesStringifiedBatchUpdateArray(t *testing.T) {
	args := json.RawMessage(`{
		"updates": "\n[{\"index\": 4, \"status\": \"completed\", \"evidence\": [\"git status clean\"]}\n"
	}`)
	got, repaired, notes := repairToolArgsForAction(PlanToolName, args)
	if !repaired {
		t.Fatal("expected unterminated stringified plan updates array to be repaired")
	}
	var obj struct {
		Action  string       `json:"action"`
		Updates []planUpdate `json:"updates"`
	}
	if err := json.Unmarshal(got, &obj); err != nil {
		t.Fatalf("unmarshal repaired args: %v\n%s", err, got)
	}
	if obj.Action != "update" || len(obj.Updates) != 1 || obj.Updates[0].Index != 4 {
		t.Fatalf("decoded updates not preserved: %+v args=%s notes=%v", obj, got, notes)
	}
}

func TestRepairPlanArgsForActionTrimsObjectSuffixFromStringifiedBatchUpdate(t *testing.T) {
	args := json.RawMessage(`{
		"updates": "\n[{\"index\": 4, \"status\": \"completed\"}, {\"index\": 5, \"status\": \"completed\"}]}\n"
	}`)
	got, repaired, _ := repairToolArgsForAction(PlanToolName, args)
	if !repaired {
		t.Fatal("expected object-suffixed stringified plan updates array to be repaired")
	}
	var obj struct {
		Action  string       `json:"action"`
		Updates []planUpdate `json:"updates"`
	}
	if err := json.Unmarshal(got, &obj); err != nil {
		t.Fatalf("unmarshal repaired args: %v\n%s", err, got)
	}
	if obj.Action != "update" || len(obj.Updates) != 2 || obj.Updates[1].Index != 5 {
		t.Fatalf("decoded updates not preserved: %+v args=%s", obj, got)
	}
}

func TestRepairPlanArgsForActionInfersSet(t *testing.T) {
	args := json.RawMessage(`{
		"steps": [
			{"text": "inspect"},
			{"text": "ship", "status": "pending"}
		]
	}`)
	got, repaired, notes := repairToolArgsForAction(PlanToolName, args)
	if !repaired {
		t.Fatal("expected missing plan set action to be repaired")
	}
	var obj map[string]any
	if err := json.Unmarshal(got, &obj); err != nil {
		t.Fatalf("unmarshal repaired args: %v", err)
	}
	if obj["action"] != "set" {
		t.Fatalf("action = %v, want set; args=%s", obj["action"], got)
	}
	if joined := strings.Join(notes, "\n"); !strings.Contains(joined, "action=set") {
		t.Fatalf("repair notes should explain inferred set action: %v", notes)
	}
}

func TestRepairMemoryArgsForActionInfersSearch(t *testing.T) {
	args := json.RawMessage(`{"query":"AUTO-MEM-64 JSON","topic":"conventions"}`)
	got, repaired, notes := repairToolArgsForAction(MemoryToolName, args)
	if !repaired {
		t.Fatal("expected missing memory search action to be repaired")
	}
	var obj map[string]any
	if err := json.Unmarshal(got, &obj); err != nil {
		t.Fatalf("unmarshal repaired args: %v", err)
	}
	if obj["action"] != memoryActionSearch || obj["query"] != "AUTO-MEM-64 JSON" {
		t.Fatalf("required search args were not preserved: %s", got)
	}
	if joined := strings.Join(notes, "\n"); !strings.Contains(joined, "action=search") || !strings.Contains(joined, MemoryToolName) {
		t.Fatalf("repair notes should explain inferred memory action: %v", notes)
	}
}

func TestRepairPlanArgsForActionDoesNotInferAmbiguousAction(t *testing.T) {
	args := json.RawMessage(`{"index": 1}`)
	got, repaired, notes := repairToolArgsForAction(PlanToolName, args)
	if repaired {
		t.Fatalf("ambiguous plan args should not be repaired: got=%s notes=%v", got, notes)
	}
}
