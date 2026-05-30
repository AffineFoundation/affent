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

// CompactTriggerInputTokensForModelPolicy resolves the same request-input
// compaction threshold, then applies the trigger percentage to the input
// capacity left after reserving output tokens. Explicit thresholds keep their
// exact meaning; the reserve only applies when Affent is deriving the trigger
// from model metadata.
func CompactTriggerInputTokensForModelPolicy(explicit, modelContextWindowTokens, percent, reservedOutputTokens, fallback int) int {
	if explicit != 0 || modelContextWindowTokens <= 0 || reservedOutputTokens <= 0 {
		return CompactTriggerInputTokensForPolicy(explicit, modelContextWindowTokens, percent, fallback)
	}
	inputCapacity := ModelInputCapacityTokens(modelContextWindowTokens, reservedOutputTokens)
	if inputCapacity <= 0 {
		return CompactTriggerInputTokensForPolicy(explicit, modelContextWindowTokens, percent, fallback)
	}
	if percent <= 0 {
		percent = DefaultCompactTriggerInputPercent
	}
	if percent > 100 {
		percent = 100
	}
	trigger := inputCapacity * percent / 100
	if trigger < 1 {
		return 1
	}
	return trigger
}

// ModelInputCapacityTokens returns the request-input capacity after reserving
// output tokens from the model's context window.
func ModelInputCapacityTokens(modelContextWindowTokens, reservedOutputTokens int) int {
	if modelContextWindowTokens <= 0 {
		return 0
	}
	inputCapacity := modelContextWindowTokens - reservedOutputTokens
	if inputCapacity < 1 {
		return 1
	}
	return inputCapacity
}

// ClampAutoCompactTokenLimit applies Affent's effective model-window policy to
// provider-advertised auto-compaction thresholds. Some providers expose their
// own token limit; Affent accepts lower provider limits but caps higher values
// with the same percent/output-reserve policy used for derived triggers.
func ClampAutoCompactTokenLimit(providerLimit, modelContextWindowTokens, percent, reservedOutputTokens int) int {
	if providerLimit <= 0 {
		return 0
	}
	maxPolicy := CompactTriggerInputTokensForModelPolicy(0, modelContextWindowTokens, percent, reservedOutputTokens, 0)
	if maxPolicy > 0 && providerLimit > maxPolicy {
		return maxPolicy
	}
	return providerLimit
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

// CompactTriggerBytesForModelPolicy is the byte-side companion to
// CompactTriggerInputTokensForModelPolicy for callers wiring an
// LLMSummaryCompactor from model metadata. Explicit request-input thresholds do
// not change the byte compactor's meaning; the output reserve only applies when
// Affent derives context pressure from the model window.
func CompactTriggerBytesForModelPolicy(explicitInputTokens, modelContextWindowTokens, percent, reservedOutputTokens, fallback int) int {
	if explicitInputTokens != 0 {
		return fallback
	}
	tokens := CompactTriggerInputTokensForModelPolicy(0, modelContextWindowTokens, percent, reservedOutputTokens, 0)
	if tokens <= 0 {
		return fallback
	}
	return tokens * 4
}

// SummaryPromptMaxBytesForModelPolicy caps the compactor's own summarization
// request with the same model-window policy used for request-pressure
// compaction. It never raises the caller's fallback; large-context models keep
// the conservative default, while small-context models avoid spending a failed
// compaction attempt on an oversized summary prompt.
func SummaryPromptMaxBytesForModelPolicy(modelContextWindowTokens, percent, reservedOutputTokens, fallback int) int {
	limit := CompactTriggerBytesForModelPolicy(0, modelContextWindowTokens, percent, reservedOutputTokens, fallback)
	if limit <= 0 {
		return fallback
	}
	if fallback > 0 && limit > fallback {
		return fallback
	}
	return limit
}

// SummaryCompactorPolicy is the host-facing configuration for Affent's rolling
// context compactor. Keeping this policy in agent prevents serve, ctl, and eval
// from drifting on model-window or reserved-output behavior.
type SummaryCompactorPolicy struct {
	LLM                        *LLMClient
	TriggerMsgs                int
	TriggerInputTokens         int
	ModelContextWindowTokens   int
	CompactTriggerInputPercent int
	ReservedOutputTokens       int
	KeepLast                   int
}

type ResolvedSummaryCompactorPolicy struct {
	TriggerMsgs          int
	TriggerBytes         int
	TriggerInputTokens   int
	HardInputLimitTokens int
	KeepLast             int
	MaxPromptBytes       int
}

func ResolveSummaryCompactorPolicy(policy SummaryCompactorPolicy) ResolvedSummaryCompactorPolicy {
	triggerMsgs := policy.TriggerMsgs
	if triggerMsgs <= 0 {
		triggerMsgs = DefaultSummaryTriggerMsgs
	}
	keepLast := policy.KeepLast
	if keepLast <= 0 {
		keepLast = DefaultSummaryKeepLast
	}
	triggerBytes := DefaultSummaryTriggerBytes
	if policy.ModelContextWindowTokens > 0 && policy.TriggerInputTokens == 0 {
		triggerBytes = CompactTriggerBytesForModelPolicy(
			0,
			policy.ModelContextWindowTokens,
			policy.CompactTriggerInputPercent,
			policy.ReservedOutputTokens,
			DefaultSummaryTriggerBytes,
		)
	}
	triggerInputTokens := CompactTriggerInputTokensForModelPolicy(
		policy.TriggerInputTokens,
		policy.ModelContextWindowTokens,
		policy.CompactTriggerInputPercent,
		policy.ReservedOutputTokens,
		DefaultSummaryTriggerInputTokens,
	)
	maxSummaryPromptBytes := SummaryPromptMaxBytesForModelPolicy(
		policy.ModelContextWindowTokens,
		policy.CompactTriggerInputPercent,
		policy.ReservedOutputTokens,
		DefaultSummaryPromptMaxBytes,
	)
	return ResolvedSummaryCompactorPolicy{
		TriggerMsgs:          triggerMsgs,
		TriggerBytes:         triggerBytes,
		TriggerInputTokens:   triggerInputTokens,
		HardInputLimitTokens: ModelInputCapacityTokens(policy.ModelContextWindowTokens, policy.ReservedOutputTokens),
		KeepLast:             keepLast,
		MaxPromptBytes:       maxSummaryPromptBytes,
	}
}

func NewLLMSummaryCompactorForPolicy(policy SummaryCompactorPolicy) *LLMSummaryCompactor {
	resolved := ResolveSummaryCompactorPolicy(policy)
	return &LLMSummaryCompactor{
		LLM:            policy.LLM,
		TriggerMsgs:    resolved.TriggerMsgs,
		TriggerBytes:   resolved.TriggerBytes,
		KeepLast:       resolved.KeepLast,
		MaxPromptBytes: resolved.MaxPromptBytes,
	}
}

// ToolSchemaBudgetTokensForRequestPolicy returns the model-visible tool-schema
// budget remaining after the current conversation has consumed part of the
// request-input compaction trigger. It keeps tool selection aligned with the
// same whole-request pressure policy used by proactive compaction.
func ToolSchemaBudgetTokensForRequestPolicy(inputTriggerTokens, conversationTokens int) int {
	if inputTriggerTokens <= 0 {
		return 0
	}
	remaining := inputTriggerTokens - conversationTokens
	if remaining <= 0 {
		return 1
	}
	return remaining
}

// RequestInputLimitPressure is the normalized pressure of an estimated next
// request against one input-token limit. It is shared by runtime telemetry,
// session summaries, and eval checks so operator-facing pressure math stays
// consistent.
type RequestInputLimitPressure struct {
	Percent          int
	TokensUntilLimit int
}

func RequestInputPressureForLimit(estimatedTokens, limitTokens int) RequestInputLimitPressure {
	if limitTokens <= 0 {
		return RequestInputLimitPressure{}
	}
	remaining := limitTokens - estimatedTokens
	if remaining < 0 {
		remaining = 0
	}
	percent := 0
	if estimatedTokens > 0 {
		percent = (estimatedTokens*100 + limitTokens/2) / limitTokens
	}
	return RequestInputLimitPressure{
		Percent:          percent,
		TokensUntilLimit: remaining,
	}
}
