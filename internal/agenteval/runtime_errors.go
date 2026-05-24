package agenteval

import "strings"

func RuntimeErrorKind(message string) string {
	lower := strings.ToLower(strings.TrimSpace(message))
	switch {
	case isLLMTimeoutFailure(lower):
		return "llm_timeout"
	case isLLMIncompleteStreamFailure(lower):
		return "llm_incomplete_stream"
	case isContextOverflowFailure(lower):
		return "context_overflow"
	default:
		return ""
	}
}

func RuntimeErrorDiagnosticsFromFailures(failures []string, maxPerKind int) (map[string]int, map[string][]RuntimeErrorExample) {
	if len(failures) == 0 {
		return nil, nil
	}
	counts := map[string]int{}
	examples := map[string][]RuntimeErrorExample{}
	for _, failure := range failures {
		kind := RuntimeErrorKind(failure)
		if kind == "" {
			continue
		}
		counts[kind]++
		if maxPerKind > 0 && len(examples[kind]) < maxPerKind {
			examples[kind] = append(examples[kind], RuntimeErrorExample{
				Kind:    kind,
				Message: compactOneLine(failure, 320),
			})
		}
	}
	if len(counts) == 0 {
		counts = nil
	}
	if len(examples) == 0 {
		examples = nil
	}
	return counts, examples
}

func isLLMTimeoutFailure(lower string) bool {
	return (strings.Contains(lower, "llm ") && strings.Contains(lower, "timed out")) ||
		strings.Contains(lower, "stream idle timeout") ||
		(strings.Contains(lower, "context deadline exceeded") &&
			(strings.Contains(lower, "max-call-timeout") ||
				strings.Contains(lower, "per-call-timeout") ||
				strings.Contains(lower, "waiting for chat completion")))
}

func isLLMIncompleteStreamFailure(lower string) bool {
	return strings.Contains(lower, "incomplete sse stream") ||
		strings.Contains(lower, "stream ended without finish") ||
		(strings.Contains(lower, "finish_reason") &&
			(strings.Contains(lower, "closed") || strings.Contains(lower, "ended")))
}

func isContextOverflowFailure(lower string) bool {
	for _, needle := range []string{
		"context overflow",
		"context length",
		"context window",
		"maximum context",
		"context_length_exceeded",
		"prompt is too long",
		"input is too long",
		"too many tokens",
		"request too large",
	} {
		if strings.Contains(lower, needle) {
			return true
		}
	}
	return false
}
