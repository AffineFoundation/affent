package agent

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/affinefoundation/affent/internal/loopstate"
)

func repairToolArgsForAction(toolName string, args json.RawMessage) (json.RawMessage, bool, []string) {
	switch toolName {
	case LoopProtocolToolName:
		return repairLoopProtocolArgsForAction(args)
	case PlanToolName:
		return repairPlanArgsForAction(args)
	default:
		return args, false, nil
	}
}

func repairPlanArgsForAction(args json.RawMessage) (json.RawMessage, bool, []string) {
	var obj map[string]any
	if err := json.Unmarshal(args, &obj); err != nil || obj == nil {
		return args, false, nil
	}
	action := ""
	if value, ok := obj["action"]; ok {
		action = strings.ToLower(strings.TrimSpace(fmt.Sprint(value)))
	}
	if action != "" {
		return args, false, nil
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(args, &raw); err != nil || raw == nil {
		return args, false, nil
	}
	inferred := ""
	if _, ok := raw["steps"]; ok {
		inferred = "set"
	} else if _, ok := raw["index"]; ok && planArgsHaveUpdateField(raw) {
		inferred = "update"
	}
	if inferred == "" {
		return args, false, nil
	}
	obj["action"] = inferred
	repaired, err := json.Marshal(obj)
	if err != nil {
		return args, false, nil
	}
	return json.RawMessage(repaired), true, []string{fmt.Sprintf("inferred missing action=%s for %s from structured fields", inferred, PlanToolName)}
}

func planArgsHaveUpdateField(raw map[string]json.RawMessage) bool {
	for _, key := range []string{"status", "text", "evidence", "note"} {
		if _, ok := raw[key]; ok {
			return true
		}
	}
	return false
}

func repairLoopProtocolArgsForAction(args json.RawMessage) (json.RawMessage, bool, []string) {
	var obj map[string]any
	if err := json.Unmarshal(args, &obj); err != nil || obj == nil {
		return args, false, nil
	}
	action := strings.ToLower(strings.TrimSpace(fmt.Sprint(obj["action"])))
	if action == "" {
		return args, false, nil
	}
	allowed := loopProtocolAllowedArgsForAction(action)
	if len(allowed) == 0 {
		return args, false, nil
	}
	out := make(map[string]any, len(obj))
	changed := false
	var notes []string
	for key, value := range obj {
		if !allowed[key] {
			changed = true
			notes = append(notes, fmt.Sprintf("dropped action-inapplicable field %s for %s action=%s", key, LoopProtocolToolName, action))
			continue
		}
		if key == "protocol" && action == "complete_activation" {
			protocol := strings.TrimSpace(fmt.Sprint(value))
			if protocol != "" && loopstate.ProtocolStatus(protocol) == "" {
				changed = true
				notes = append(notes, "dropped action-inapplicable field protocol for loop_protocol action=complete_activation missing LOOP.md metadata")
				continue
			}
		}
		out[key] = value
	}
	if !changed {
		return args, false, nil
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return args, false, nil
	}
	return json.RawMessage(raw), true, notes
}

func loopProtocolAllowedArgsForAction(action string) map[string]bool {
	switch action {
	case "start_setup":
		return map[string]bool{"action": true, "goal": true, "reason": true}
	case "read":
		return map[string]bool{"action": true}
	case "patch_draft":
		return map[string]bool{"action": true, "reason": true, "sections_changed": true, "patches": true}
	case "update_draft":
		return map[string]bool{"action": true, "protocol": true, "reason": true, "sections_changed": true}
	case "complete_activation":
		return map[string]bool{"action": true, "protocol": true, "reason": true, "sections_changed": true}
	case "close":
		return map[string]bool{"action": true, "status": true, "reason": true}
	default:
		return nil
	}
}
