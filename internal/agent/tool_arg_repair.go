package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

const maxRepairableToolArgBytes = 1024 * 1024

// repairToolCallArgsForDispatch turns a model-emitted arguments string into
// valid JSON before a tool sees it. It is deliberately deterministic and
// conservative: exact JSON passes through unchanged; common stream-cut shapes
// get repaired; irreparable small inputs fall back to {} so the tool can
// return its normal "missing field" error instead of bricking the turn.
func repairToolCallArgsForDispatch(raw string) (json.RawMessage, bool, error) {
	if strings.TrimSpace(raw) == "" {
		return json.RawMessage("{}"), raw != "{}", nil
	}
	if len(raw) > maxRepairableToolArgBytes {
		return nil, false, fmt.Errorf("tool arguments are %d bytes; refusing to repair over %d bytes", len(raw), maxRepairableToolArgBytes)
	}
	if fixed, ok := parseToolArgJSON(raw); ok {
		return fixed, false, nil
	}

	candidates := []string{
		stripControlCharsInJSONString(raw),
		stripTrailingCommas(stripControlCharsInJSONString(raw)),
		balanceJSONClosers(stripTrailingCommas(stripControlCharsInJSONString(raw))),
		stripExcessJSONClosers(balanceJSONClosers(stripTrailingCommas(stripControlCharsInJSONString(raw)))),
	}
	for _, c := range candidates {
		if fixed, ok := parseToolArgJSON(c); ok {
			return fixed, true, nil
		}
	}
	return json.RawMessage("{}"), true, nil
}

func parseToolArgJSON(raw string) (json.RawMessage, bool) {
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return nil, false
	}
	if s, ok := v.(string); ok && strings.HasPrefix(strings.TrimSpace(s), "{") {
		var inner any
		if err := json.Unmarshal([]byte(s), &inner); err == nil {
			raw = s
		}
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, []byte(raw)); err != nil {
		return nil, false
	}
	return json.RawMessage(compact.Bytes()), true
}

func stripControlCharsInJSONString(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	inString := false
	escaped := false
	for _, r := range s {
		if escaped {
			b.WriteRune(r)
			escaped = false
			continue
		}
		switch r {
		case '\\':
			b.WriteRune(r)
			escaped = true
		case '"':
			b.WriteRune(r)
			inString = !inString
		default:
			if inString && r < 0x20 && r != '\t' && r != '\n' && r != '\r' {
				continue
			}
			b.WriteRune(r)
		}
	}
	return b.String()
}

func stripTrailingCommas(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	inString := false
	escaped := false
	for i, r := range s {
		if escaped {
			b.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' {
			b.WriteRune(r)
			escaped = true
			continue
		}
		if r == '"' {
			b.WriteRune(r)
			inString = !inString
			continue
		}
		if !inString && r == ',' {
			rest := strings.TrimSpace(s[i+len(string(r)):])
			if rest == "" || strings.HasPrefix(rest, "}") || strings.HasPrefix(rest, "]") {
				continue
			}
		}
		b.WriteRune(r)
	}
	return b.String()
}

func balanceJSONClosers(s string) string {
	var stack []rune
	inString := false
	escaped := false
	for _, r := range s {
		if escaped {
			escaped = false
			continue
		}
		if r == '\\' {
			escaped = true
			continue
		}
		if r == '"' {
			inString = !inString
			continue
		}
		if inString {
			continue
		}
		switch r {
		case '{', '[':
			stack = append(stack, r)
		case '}':
			if len(stack) > 0 && stack[len(stack)-1] == '{' {
				stack = stack[:len(stack)-1]
			}
		case ']':
			if len(stack) > 0 && stack[len(stack)-1] == '[' {
				stack = stack[:len(stack)-1]
			}
		}
	}
	if len(stack) == 0 {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + len(stack))
	b.WriteString(s)
	for i := len(stack) - 1; i >= 0; i-- {
		if stack[i] == '{' {
			b.WriteByte('}')
		} else {
			b.WriteByte(']')
		}
	}
	return b.String()
}

func stripExcessJSONClosers(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	var stack []rune
	inString := false
	escaped := false
	for _, r := range s {
		if escaped {
			b.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' {
			b.WriteRune(r)
			escaped = true
			continue
		}
		if r == '"' {
			b.WriteRune(r)
			inString = !inString
			continue
		}
		if inString {
			b.WriteRune(r)
			continue
		}
		switch r {
		case '{', '[':
			stack = append(stack, r)
			b.WriteRune(r)
		case '}':
			if len(stack) > 0 && stack[len(stack)-1] == '{' {
				stack = stack[:len(stack)-1]
				b.WriteRune(r)
			}
		case ']':
			if len(stack) > 0 && stack[len(stack)-1] == '[' {
				stack = stack[:len(stack)-1]
				b.WriteRune(r)
			}
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
