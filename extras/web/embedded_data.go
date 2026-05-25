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
	type candidate struct {
		start int
		end   int
		score int
	}
	var candidates []candidate
	seen := map[int]bool{}
	for _, term := range prioritizeEmbeddedDataTerms(terms) {
		searchFrom := 0
		needle := strings.ToLower(term)
		termCandidates := 0
		for termCandidates < maxEmbeddedDataSnippets*6 {
			pos := strings.Index(lower[searchFrom:], needle)
			if pos < 0 {
				break
			}
			idx := searchFrom + pos
			searchFrom = idx + len(needle)
			start, end := embeddedDataSnippetBounds(source, idx)
			if seen[start] {
				continue
			}
			snippet := source[start:end]
			score := embeddedDataSnippetScore(snippet, terms)
			if score <= 0 {
				continue
			}
			seen[start] = true
			candidates = append(candidates, candidate{start: start, end: end, score: score})
			termCandidates++
		}
	}
	if len(candidates) == 0 {
		return nil
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
