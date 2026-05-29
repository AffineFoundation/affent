package toolrepair

import "strings"

// Kind maps a runtime repair note to a stable stats bucket.
func Kind(note string) string {
	note = strings.TrimSpace(strings.ToLower(note))
	switch {
	case note == "":
		return ""
	case strings.Contains(note, "canonicalized tool"):
		return "tool_name"
	case strings.Contains(note, "malformed json"):
		return "malformed_json"
	case strings.HasPrefix(note, "unwrapped field "):
		return "wrapper_unwrap"
	case strings.HasPrefix(note, "wrapped arguments as ") ||
		strings.HasPrefix(note, "wrapped object arguments as "):
		return "scalar_wrap"
	case strings.HasPrefix(note, "renamed field ") ||
		strings.HasPrefix(note, "filled required field "):
		return "alias_rename"
	case strings.HasPrefix(note, "normalized enum field "):
		return "enum_normalization"
	case strings.HasPrefix(note, "coerced field "):
		return "type_coercion"
	case strings.HasPrefix(note, "dropped unknown field "):
		return "unknown_field_drop"
	case strings.HasPrefix(note, "dropped action-inapplicable field "):
		return "action_field_drop"
	default:
		return "other"
	}
}
