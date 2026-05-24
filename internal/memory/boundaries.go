package memory

// RuntimeBoundaries is a diagnostic snapshot of hard memory-tool guardrails.
// Configurable bucket limits are still reported by callers that resolve config;
// this snapshot covers fixed parser/search/response caps used by the package.
type RuntimeBoundaries struct {
	DefaultCoreChars  int
	DefaultTopicChars int
	DefaultUserChars  int
	DefaultMaxTopics  int
	FileBytes         int
	SearchQueryBytes  int
	SearchQueryTerms  int
	DefaultSearchTopK int
	MaxSearchTopK     int
	SearchSnippet     int
	ResponseEntry     int
}

func DefaultRuntimeBoundaries() RuntimeBoundaries {
	return RuntimeBoundaries{
		DefaultCoreChars:  DefaultCoreCharLimit,
		DefaultTopicChars: DefaultTopicCharLimit,
		DefaultUserChars:  DefaultUserCharLimit,
		DefaultMaxTopics:  DefaultMaxTopics,
		FileBytes:         MaxMemoryFileBytes,
		SearchQueryBytes:  MaxSearchQueryBytes,
		SearchQueryTerms:  MaxSearchQueryTerms,
		DefaultSearchTopK: DefaultSearchTopK,
		MaxSearchTopK:     MaxSearchTopK,
		SearchSnippet:     memorySnippetMax,
		ResponseEntry:     memoryResponseEntryMax,
	}
}
