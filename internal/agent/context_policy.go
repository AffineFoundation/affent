package agent

// DefaultCompactTriggerInputPercent is the runtime policy used when a model
// context window is known but no explicit request-input compaction limit is
// configured. Codex keeps model context-window metadata and auto-compaction
// limits as runtime model policy rather than prompt text; Affent follows that
// shape with a conservative default.
const DefaultCompactTriggerInputPercent = 80

// CompactTriggerInputTokensForPolicy resolves the estimated request-input
// token threshold used for proactive compaction.
func CompactTriggerInputTokensForPolicy(explicit, modelContextWindowTokens, percent, fallback int) int {
	if explicit < 0 {
		return 0
	}
	if explicit > 0 {
		return explicit
	}
	if modelContextWindowTokens > 0 {
		if percent <= 0 {
			percent = DefaultCompactTriggerInputPercent
		}
		if percent > 100 {
			percent = 100
		}
		return max(1, modelContextWindowTokens*percent/100)
	}
	if fallback < 0 {
		return 0
	}
	return fallback
}

// CompactTriggerBytesForPolicy keeps the conversation-byte compactor aligned
// with the request-input policy when a model context window is known. The byte
// value is heuristic because provider tokenizers differ.
func CompactTriggerBytesForPolicy(explicit, modelContextWindowTokens, percent, fallback int) int {
	tokens := CompactTriggerInputTokensForPolicy(explicit, modelContextWindowTokens, percent, 0)
	if tokens <= 0 {
		return fallback
	}
	return tokens * 4
}
