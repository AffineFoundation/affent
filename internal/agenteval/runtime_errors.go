package agenteval

import "strings"

func RuntimeErrorKind(message string) string {
	lower := strings.ToLower(strings.TrimSpace(message))
	switch {
	case isBrowserLaunchFailure(lower):
		return "browser_launch_failed"
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
				Message: compactOneLine(actionableRuntimeErrorMessage(kind, failure), 520),
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

func actionableRuntimeErrorMessage(kind, message string) string {
	trimmed := strings.TrimSpace(message)
	lower := strings.ToLower(trimmed)
	switch kind {
	case "browser_launch_failed":
		if strings.Contains(lower, "chromium runtime dependencies") ||
			strings.Contains(lower, "affent_browser_binary") ||
			strings.Contains(lower, "missing_shared_library=") {
			return trimmed
		}
		return "Browser launch failed (kind=browser_launch_failed). Chromium could not start, so browser-based web evidence is unavailable until the runtime image has Chrome/Chromium and its shared-library dependencies, or AFFENT_BROWSER_BINARY points to a working binary. Original error: " + trimmed
	case "llm_timeout":
		if strings.Contains(lower, "first-token latency") ||
			strings.Contains(lower, "stream idle timeout") ||
			strings.Contains(lower, "while waiting for chat completion") {
			return trimmed
		}
		return "LLM call timed out (kind=llm_timeout). The per-call wall-clock timeout fired while waiting for the chat completion or next SSE chunk. Common causes: first-token latency from prefill or scheduler queueing, a reasoning model paused too long between chunks, scheduler/KV-cache stalls, proxy buffering, or an upstream that kept the HTTP stream open without useful tokens. Original error: " + trimmed
	case "llm_incomplete_stream":
		if strings.Contains(lower, "incomplete sse stream") ||
			strings.Contains(lower, "terminal finish_reason") ||
			strings.Contains(lower, "upstream/proxy abort") {
			return trimmed
		}
		return "LLM stream ended without a terminal finish_reason (kind=llm_incomplete_stream). HTTP streaming started, but the upstream closed before sending a final finish_reason chunk. Common causes: sglang/vLLM worker crash, KV-cache preemption or abort, reverse-proxy reset, or OOM kill. Original error: " + trimmed
	default:
		return trimmed
	}
}

func isBrowserLaunchFailure(lower string) bool {
	return strings.Contains(lower, "browser_launch_failed") ||
		(strings.Contains(lower, "launch chromium") &&
			(strings.Contains(lower, "error while loading shared libraries") ||
				strings.Contains(lower, "no such file or directory") ||
				strings.Contains(lower, "executable file not found") ||
				strings.Contains(lower, "chromium runtime dependencies")))
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
