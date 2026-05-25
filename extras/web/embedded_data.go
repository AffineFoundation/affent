package web

import (
	"net/url"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	maxEmbeddedDataSnippets     = 3
	maxEmbeddedDataSnippetChars = 700
)

type embeddedDataCandidate struct {
	start        int
	end          int
	score        int
	structuredID bool
}

func embeddedDataSnippets(body []byte, finalURL string) []string {
	terms := embeddedDataTerms(finalURL)
	if len(terms) == 0 || len(body) == 0 {
		return nil
	}
	source := normalizeEmbeddedDataSource(body)
	if !looksLikeEmbeddedState(source) {
		return nil
	}
	lower := strings.ToLower(source)
	var candidates []embeddedDataCandidate
	seen := map[int]bool{}
	addCandidate := func(idx, boost int) {
		start, end := embeddedDataSnippetBounds(source, idx)
		if seen[start] {
			return
		}
		snippet := source[start:end]
		score := embeddedDataSnippetScore(snippet, terms) + boost
		if score <= 0 {
			return
		}
		seen[start] = true
		candidates = append(candidates, embeddedDataCandidate{
			start:        start,
			end:          end,
			score:        score,
			structuredID: embeddedDataSnippetHasStructuredID(snippet, terms),
		})
	}
	for _, structured := range embeddedDataStructuredNeedles(terms) {
		searchFrom := 0
		for {
			pos := strings.Index(lower[searchFrom:], structured.needle)
			if pos < 0 {
				break
			}
			idx := searchFrom + pos
			searchFrom = idx + len(structured.needle)
			addCandidate(idx, structured.boost)
		}
	}
	for _, term := range prioritizeEmbeddedDataTerms(terms) {
		searchFrom := 0
		needle := strings.ToLower(term)
		termCandidates := 0
		limit := maxEmbeddedDataSnippets * 6
		if isMostlyDigits(term) {
			limit = maxEmbeddedDataSnippets * 64
		}
		for termCandidates < limit {
			pos := strings.Index(lower[searchFrom:], needle)
			if pos < 0 {
				break
			}
			idx := searchFrom + pos
			searchFrom = idx + len(needle)
			before := len(candidates)
			addCandidate(idx, 0)
			if len(candidates) == before {
				continue
			}
			termCandidates++
		}
	}
	if len(candidates) == 0 {
		return nil
	}
	if hasStructuredIDCandidate(candidates) {
		filtered := candidates[:0]
		for _, c := range candidates {
			if c.structuredID {
				filtered = append(filtered, c)
			}
		}
		candidates = filtered
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}
		return candidates[i].start < candidates[j].start
	})
	out := make([]string, 0, maxEmbeddedDataSnippets)
	for _, c := range candidates {
		snippet := compactEmbeddedDataSnippet(source[c.start:c.end])
		if snippet == "" || embeddedDataSnippetOverlaps(out, snippet) {
			continue
		}
		out = append(out, snippet)
		if len(out) >= maxEmbeddedDataSnippets {
			break
		}
	}
	return out
}

type embeddedDataStructuredNeedle struct {
	needle string
	boost  int
}

func hasStructuredIDCandidate(candidates []embeddedDataCandidate) bool {
	for _, c := range candidates {
		if c.structuredID {
			return true
		}
	}
	return false
}

func embeddedDataStructuredNeedles(terms []string) []embeddedDataStructuredNeedle {
	seen := map[string]bool{}
	var out []embeddedDataStructuredNeedle
	add := func(needle string, boost int) {
		needle = strings.ToLower(strings.TrimSpace(needle))
		if needle == "" || seen[needle] {
			return
		}
		seen[needle] = true
		out = append(out, embeddedDataStructuredNeedle{needle: needle, boost: boost})
	}
	for _, term := range terms {
		if !isMostlyDigits(term) {
			continue
		}
		for _, field := range []string{"netuid", "subnet_id", "subnetid", "id"} {
			add(`"`+field+`":`+term, 20)
			add(`"`+field+`": `+term, 20)
		}
		add("sn"+term, 4)
	}
	return out
}

func embeddedDataSnippetHasStructuredID(snippet string, terms []string) bool {
	lower := strings.ToLower(snippet)
	for _, needle := range embeddedDataStructuredNeedles(terms) {
		if needle.boost >= 20 && strings.Contains(lower, needle.needle) {
			return true
		}
	}
	return false
}

func embeddedDataTerms(finalURL string) []string {
	u, err := url.Parse(finalURL)
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var terms []string
	add := func(term string) {
		term = strings.ToLower(strings.Trim(term, " \t\r\n-_./"))
		if term == "" || seen[term] {
			return
		}
		alphaNum := 0
		for _, r := range term {
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				alphaNum++
			}
		}
		if alphaNum < 2 || len(term) > 64 {
			return
		}
		seen[term] = true
		terms = append(terms, term)
	}
	for _, seg := range strings.Split(u.EscapedPath(), "/") {
		if decoded, err := url.PathUnescape(seg); err == nil {
			seg = decoded
		}
		add(seg)
		if strings.HasSuffix(seg, "s") && len(seg) > 3 {
			add(strings.TrimSuffix(seg, "s"))
		}
	}
	for _, vals := range u.Query() {
		for _, val := range vals {
			add(val)
		}
	}
	return terms
}

func prioritizeEmbeddedDataTerms(terms []string) []string {
	out := append([]string(nil), terms...)
	sort.SliceStable(out, func(i, j int) bool {
		iDigits := isMostlyDigits(out[i])
		jDigits := isMostlyDigits(out[j])
		if iDigits != jDigits {
			return iDigits
		}
		if len(out[i]) != len(out[j]) {
			return len(out[i]) > len(out[j])
		}
		return false
	})
	return out
}

func normalizeEmbeddedDataSource(body []byte) string {
	source := string(body)
	replacements := []struct {
		old string
		new string
	}{
		{`\"`, `"`},
		{`\\u0026`, `&`},
		{`\u0026`, `&`},
		{`\\u003c`, `<`},
		{`\u003c`, `<`},
		{`\\u003e`, `>`},
		{`\u003e`, `>`},
	}
	for _, r := range replacements {
		source = strings.ReplaceAll(source, r.old, r.new)
	}
	return source
}

func looksLikeEmbeddedState(source string) bool {
	lower := strings.ToLower(source)
	return strings.Contains(lower, "__next") ||
		strings.Contains(lower, "dehydratedstate") ||
		strings.Contains(lower, "application/ld+json") ||
		strings.Contains(lower, `"props"`) ||
		strings.Contains(lower, `"data"`)
}

func embeddedDataSnippetBounds(source string, idx int) (int, int) {
	start := idx - maxEmbeddedDataSnippetChars/2
	if start < 0 {
		start = 0
	}
	end := start + maxEmbeddedDataSnippetChars
	if end > len(source) {
		end = len(source)
		start = max(0, end-maxEmbeddedDataSnippetChars)
	}
	for start > 0 && !utf8.RuneStart(source[start]) {
		start--
	}
	for end < len(source) && !utf8.RuneStart(source[end]) {
		end++
	}
	return start, end
}

func embeddedDataSnippetScore(snippet string, terms []string) int {
	lower := strings.ToLower(snippet)
	if !strings.Contains(snippet, "{") || !strings.Contains(snippet, ":") {
		return 0
	}
	score := 0
	for _, term := range terms {
		if strings.Contains(lower, strings.ToLower(term)) {
			if isMostlyDigits(term) {
				score += 2
			} else {
				score++
			}
		}
	}
	strongSignals := 0
	for _, needle := range []string{
		`"id"`, `"name"`, `"title"`, `"description"`, `"url"`, `"github"`,
		`"contact"`, `"owner"`, `"timestamp"`, `"created_at"`, `"updated_at"`,
		`"price"`, `"market"`, `"market_cap"`, `"volume"`, `"rank"`,
		`"status"`, `"metrics"`, `"stats"`,
	} {
		if strings.Contains(lower, needle) {
			strongSignals++
			score++
		}
	}
	if strongSignals == 0 {
		return 0
	}
	return score
}

func isMostlyDigits(s string) bool {
	digits := 0
	other := 0
	for _, r := range s {
		switch {
		case unicode.IsDigit(r):
			digits++
		case unicode.IsLetter(r):
			other++
		}
	}
	return digits > 0 && digits >= other
}

func compactEmbeddedDataSnippet(snippet string) string {
	snippet = trimEmbeddedDataToJSONish(snippet)
	snippet = strings.Join(strings.Fields(snippet), " ")
	snippet = strings.Trim(snippet, " ,;")
	if snippet == "" {
		return ""
	}
	if len(snippet) <= maxEmbeddedDataSnippetChars {
		return snippet
	}
	cut := maxEmbeddedDataSnippetChars
	for cut > 0 && !utf8.RuneStart(snippet[cut]) {
		cut--
	}
	return strings.TrimSpace(snippet[:cut]) + "...(truncated)"
}

func trimEmbeddedDataToJSONish(snippet string) string {
	start := strings.Index(snippet, "{")
	end := strings.LastIndex(snippet, "}")
	if start >= 0 && end > start {
		return snippet[start : end+1]
	}
	return snippet
}

func embeddedDataSnippetOverlaps(existing []string, candidate string) bool {
	if len(candidate) < 120 {
		return false
	}
	prefix := candidate[:120]
	for _, prev := range existing {
		if strings.Contains(prev, prefix) {
			return true
		}
	}
	return false
}
