package metrictext

import (
	"strings"

	"github.com/affinefoundation/affent/internal/textutil"
)

const AmbiguityNote = "Metric caution: multiple price-like values are visible in this source; keep their visible labels separate (for example, title/top-bar price versus body/subnet price) and do not merge them."

// HasMultiplePriceLikeValues reports whether the text contains at least two
// distinct price-like lines. It is intentionally conservative: the caller must
// already have a rendered source string, and this is only used to surface a
// caution, not to reject evidence.
func HasMultiplePriceLikeValues(text string) bool {
	seen := map[string]struct{}{}
	priceLikeLines := 0
	for _, raw := range strings.Split(text, "\n") {
		line := textutil.CompactWhitespace(raw)
		if line == "" {
			continue
		}
		if !LineLooksPriceLike(line) {
			continue
		}
		key := strings.ToLower(line)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		priceLikeLines++
		if priceLikeLines >= 2 {
			return true
		}
	}
	return false
}

// LineLooksPriceLike reports whether a single rendered line contains a
// price-like metric that should participate in ambiguity detection.
func LineLooksPriceLike(line string) bool {
	lower := strings.ToLower(line)
	switch {
	case strings.Contains(lower, "price"):
		return true
	case strings.Contains(lower, "market cap"):
		return true
	case strings.Contains(lower, "fdv"):
		return true
	case strings.Contains(lower, "vol ") || strings.Contains(lower, " volume "):
		return true
	case strings.Contains(lower, "supply"):
		return true
	case strings.Contains(lower, "tvl"):
		return true
	default:
		return false
	}
}
