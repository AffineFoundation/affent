package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	agent "github.com/affinefoundation/affent/internal/agent"
)

const (
	maxBrowserFindQueryBytes = 256
	defaultBrowserFindLimit  = 8
	maxBrowserFindLimit      = 25
	maxBrowserFindSnippet    = 220
)

// FindTool returns `browser_find`. It searches the current rendered
// page snapshot and returns compact matching snippets, so the agent
// can look for labels like "market cap" or "price" without repeated
// scroll/snapshot calls.
func FindTool(s *Session) *agent.Tool {
	schema := json.RawMessage(fmt.Sprintf(`{
        "type": "object",
        "additionalProperties": false,
        "required": ["query"],
        "properties": {
            "query": {
                "type": "string",
                "minLength": 1,
                "maxLength": %d,
                "description": "Case-insensitive text to find on the current rendered page."
            },
            "max_results": {
                "type": "integer",
                "minimum": 1,
                "maximum": %d,
                "default": %d,
                "description": "Maximum matching snippets to return."
            }
        }
    }`, maxBrowserFindQueryBytes, maxBrowserFindLimit, defaultBrowserFindLimit))
	return &agent.Tool{
		Name:        "browser_find",
		Description: "Search the current rendered page for text and return compact matching snippets plus link refs. Use before repeated scrolling when looking for specific facts such as price, market cap, docs links, dates, or names.",
		Schema:      schema,
		Execute: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var args struct {
				Query      string `json:"query"`
				MaxResults int    `json:"max_results"`
			}
			if err := decodeBrowserToolArgs(raw, &args, "retry browser_find with only documented fields: query and max_results"); err != nil {
				return "", err
			}
			query := strings.TrimSpace(args.Query)
			if query == "" {
				return "", browserInvalidArgs("query is required", "retry browser_find with a visible word or label from the current page")
			}
			if len(query) > maxBrowserFindQueryBytes {
				return "", browserInvalidArgs(fmt.Sprintf("query is %d bytes; browser_find supports queries up to %d bytes", len(query), maxBrowserFindQueryBytes), "retry with a shorter distinctive phrase")
			}
			limit := args.MaxResults
			if limit <= 0 {
				limit = defaultBrowserFindLimit
			}
			if limit > maxBrowserFindLimit {
				return "", browserInvalidArgs(fmt.Sprintf("max_results must be between 1 and %d", maxBrowserFindLimit), "omit max_results to use the default, or retry with a smaller value")
			}
			if s.page == nil {
				return "", ErrNoPage
			}
			snap, err := s.TakeSnapshot(ctx)
			if err != nil {
				return "", fmt.Errorf("snapshot: %w", err)
			}
			return formatBrowserFindResults(snap, query, limit), nil
		},
	}
}

func formatBrowserFindResults(snap *Snapshot, query string, limit int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "URL: %s\n", snap.URL)
	if snap.Title != "" {
		fmt.Fprintf(&b, "TITLE: %s\n", snap.Title)
	}
	fmt.Fprintf(&b, "SNAPSHOT_ID: %d\n", snap.SnapshotID)
	fmt.Fprintf(&b, "QUERY: %q\n\n", query)

	matches := browserFindMatches(snap, query, limit)
	if len(matches) == 0 {
		b.WriteString("MATCHES: none\n")
		b.WriteString("Next: retry browser_find with a shorter or different visible phrase, call browser_snapshot to inspect current text, scroll once if the desired section is likely off-screen, or continue from existing evidence.\n")
		return b.String()
	}
	b.WriteString("MATCHES:\n")
	for _, m := range matches {
		b.WriteString(m)
		b.WriteByte('\n')
	}
	return b.String()
}

func browserFindMatches(snap *Snapshot, query string, limit int) []string {
	needle := normalizedSnapshotText(query)
	if needle == "" || limit <= 0 {
		return nil
	}
	var out []string
	add := func(line string) bool {
		out = append(out, line)
		return len(out) >= limit
	}
	for _, el := range snap.Interactive {
		hay := normalizedSnapshotText(strings.Join([]string{el.Role, el.Name, el.Href, el.Value}, " "))
		if !strings.Contains(hay, needle) {
			continue
		}
		if add(fmt.Sprintf("[interactive ref=%d] %s", el.Ref, formatInteractive(el))) {
			return out
		}
	}
	for _, tb := range snap.TextBlocks {
		text := strings.TrimSpace(tb.Text)
		if text == "" || !strings.Contains(normalizedSnapshotText(text), needle) {
			continue
		}
		typ := strings.TrimSpace(tb.Type)
		if typ == "" {
			typ = "text"
		}
		if add(fmt.Sprintf("[text %s] %s", typ, snippetAround(text, query, maxBrowserFindSnippet))) {
			return out
		}
	}
	return out
}

func snippetAround(text, query string, limit int) string {
	text = strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
	if limit <= 0 || len(text) <= limit {
		return text
	}
	lowerText := strings.ToLower(text)
	lowerQuery := strings.ToLower(strings.TrimSpace(query))
	idx := strings.Index(lowerText, lowerQuery)
	if idx < 0 {
		return truncateSnapshotField(text, limit)
	}
	start := idx - (limit-len(lowerQuery))/2
	if start < 0 {
		start = 0
	}
	if start+limit > len(text) {
		start = len(text) - limit
	}
	if start < 0 {
		start = 0
	}
	end := start + limit
	prefix, suffix := "", ""
	if start > 0 {
		prefix = "... "
	}
	if end < len(text) {
		suffix = " ..."
	}
	return prefix + strings.TrimSpace(text[start:end]) + suffix
}
