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
	default:
		return args, false, nil
	}
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
