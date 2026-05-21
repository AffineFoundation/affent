package affent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
)

// SessionSearchHit is one matched message from a past session.
type SessionSearchHit struct {
	SessionID string  `json:"session_id"`
	TurnIdx   int     `json:"turn_idx"`
	Role      string  `json:"role"`
	Snippet   string  `json:"snippet"`
	Score     float64 `json:"score"`
	ModTime   string  `json:"mod_time,omitempty"`
}

// SessionSearchResponse is the tool's return shape.
type SessionSearchResponse struct {
	Query   string             `json:"query"`
	Total   int                `json:"total"`
	Results []SessionSearchHit `json:"results"`
	Message string             `json:"message,omitempty"`
}

// sessionSearchTool scans <sessionsDir>/*.jsonl for messages matching
// query. The current session (currentSessionID) is excluded. Scoring
// is simple term overlap on the lowercased content; sufficient for
// recall over a single user's session history.
func sessionSearchTool(sessionsDir, currentSessionID string) *Tool {
	schema, err := json.Marshal(map[string]any{
		"type":     "object",
		"required": []string{"query"},
		"properties": map[string]any{
			"query": map[string]any{
				"type":        "string",
				"description": "Keywords or phrases to look for in past session transcripts. Lowercase term matching; multiple words = higher score for messages containing more of them.",
			},
			"top_k": map[string]any{
				"type":        "integer",
				"description": "Maximum results to return. Default 5.",
			},
			"max_per_session": map[string]any{
				"type":        "integer",
				"description": "Cap on hits per session, to spread results across multiple sessions. Default 3.",
			},
		},
	})
	if err != nil {
		panic(fmt.Sprintf("session_search schema: %v", err))
	}
	return &Tool{
		Name: "session_search",
		Description: "Search your past session transcripts in this workspace. Use it for 'did we discuss X' / 'what was the conclusion last time' / 'find that command I ran a week ago'. " +
			"Returns short snippets with session id and turn index. " +
			"Use this for transcript recall; use the memory tool for durable facts.",
		Schema: json.RawMessage(schema),
		Execute: func(ctx context.Context, args json.RawMessage) (string, error) {
			var p struct {
				Query         string `json:"query"`
				TopK          int    `json:"top_k"`
				MaxPerSession int    `json:"max_per_session"`
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return "", fmt.Errorf("decode args: %w", err)
			}
			p.Query = strings.TrimSpace(p.Query)
			if p.Query == "" {
				return marshalSessionSearchResp(SessionSearchResponse{Message: "query is required"}), nil
			}
			if p.TopK <= 0 {
				p.TopK = 5
			}
			if p.MaxPerSession <= 0 {
				p.MaxPerSession = 3
			}
			if sessionsDir == "" {
				return marshalSessionSearchResp(SessionSearchResponse{Query: p.Query, Message: "session_search is not configured (no sessions directory)"}), nil
			}
			hits, err := scanSessions(ctx, sessionsDir, currentSessionID, p.Query, p.MaxPerSession)
			if err != nil {
				return "", err
			}
			sort.SliceStable(hits, func(i, j int) bool {
				if hits[i].Score != hits[j].Score {
					return hits[i].Score > hits[j].Score
				}
				return hits[i].ModTime > hits[j].ModTime
			})
			if len(hits) > p.TopK {
				hits = hits[:p.TopK]
			}
			return marshalSessionSearchResp(SessionSearchResponse{
				Query:   p.Query,
				Total:   len(hits),
				Results: hits,
			}), nil
		},
	}
}

func marshalSessionSearchResp(r SessionSearchResponse) string {
	if r.Results == nil {
		r.Results = []SessionSearchHit{}
	}
	out, err := json.Marshal(r)
	if err != nil {
		return fmt.Sprintf(`{"error":%q}`, err.Error())
	}
	return string(out)
}

// sessionSearchSnippetLen caps how much content surrounding the match
// is returned per hit. Long enough to read the gist, short enough to
// keep tool results bounded.
const sessionSearchSnippetLen = 300

// scanSessions walks <sessionsDir>/*.jsonl, scoring each user /
// assistant message against the query. System messages and tool
// results are skipped. Current session is excluded.
func scanSessions(ctx context.Context, sessionsDir, currentSessionID, query string, maxPerSession int) ([]SessionSearchHit, error) {
	terms := tokenize(query)
	if len(terms) == 0 {
		return nil, nil
	}
	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var all []SessionSearchHit
	for _, ent := range entries {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".jsonl") {
			continue
		}
		sid := strings.TrimSuffix(ent.Name(), ".jsonl")
		if sid == currentSessionID {
			continue
		}
		info, ierr := ent.Info()
		mtime := ""
		if ierr == nil {
			mtime = info.ModTime().UTC().Format(time.RFC3339)
		}
		hits, serr := scoreFile(filepath.Join(sessionsDir, ent.Name()), sid, terms, maxPerSession, mtime)
		if serr != nil {
			continue
		}
		all = append(all, hits...)
	}
	return all, nil
}

// scoreFile streams a single JSONL session log, scores each
// user/assistant message, and returns up to maxPerSession top hits.
func scoreFile(path, sid string, terms []string, maxPerSession int, mtime string) ([]SessionSearchHit, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	var fileHits []SessionSearchHit
	turn := 0
	for sc.Scan() {
		turn++
		var m ChatMessage
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			continue
		}
		if m.Role != "user" && m.Role != "assistant" {
			continue
		}
		content := strings.TrimSpace(m.Content)
		if content == "" {
			continue
		}
		score := scoreContent(content, terms)
		if score <= 0 {
			continue
		}
		fileHits = append(fileHits, SessionSearchHit{
			SessionID: sid,
			TurnIdx:   turn,
			Role:      m.Role,
			Snippet:   snippetAround(content, terms),
			Score:     score,
			ModTime:   mtime,
		})
	}
	sort.SliceStable(fileHits, func(i, j int) bool { return fileHits[i].Score > fileHits[j].Score })
	if len(fileHits) > maxPerSession {
		fileHits = fileHits[:maxPerSession]
	}
	return fileHits, nil
}

// tokenize lowercases and splits on non-letter / non-digit runes,
// across all scripts (CJK, Cyrillic, Arabic, etc., not only ASCII).
// Tokens shorter than 2 bytes are dropped to filter noise, and
// common English stopwords are filtered so short queries don't
// match every past message that happens to contain "the" / "is" /
// "no". Pre-filter, a query like "zzzz-no-such-thing" tokenized to
// [zzzz, no, such, thing] and the substring count of "no" inside
// know / ignore / diagnose returned 3 spurious low-score hits.
func tokenize(s string) []string {
	s = strings.ToLower(s)
	var raw []string
	var cur strings.Builder
	flush := func() {
		t := cur.String()
		cur.Reset()
		if len(t) >= 2 {
			raw = append(raw, t)
		}
	}
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			cur.WriteRune(r)
		} else {
			flush()
		}
	}
	flush()
	out := raw[:0]
	for _, t := range raw {
		if !stopwords[t] {
			out = append(out, t)
		}
	}
	return out
}

// stopwords is a small list of the most common English filler words.
// Kept tight on purpose — we want to drop "the" / "and" / "is" noise
// without scrubbing useful content words. Mainstream IR libraries
// ship hundreds of stopwords; for an LLM-driven query rephrased into
// keywords by the model, this tighter set keeps precision high
// without erasing intent.
var stopwords = map[string]bool{
	"a": true, "an": true, "and": true, "are": true, "as": true,
	"at": true, "be": true, "but": true, "by": true, "for": true,
	"from": true, "has": true, "have": true, "in": true, "is": true,
	"it": true, "its": true, "no": true, "not": true, "of": true,
	"on": true, "or": true, "that": true, "the": true, "this": true,
	"to": true, "was": true, "we": true, "were": true, "what": true,
	"when": true, "where": true, "which": true, "who": true, "will": true,
	"with": true, "you": true, "your": true,
}

// scoreContent counts unique-term overlap with a small boost for
// total-occurrence count. The exact formula doesn't matter much at
// this scale; we just need a stable ordering.
func scoreContent(content string, terms []string) float64 {
	lower := strings.ToLower(content)
	unique := 0
	total := 0
	for _, t := range terms {
		c := strings.Count(lower, t)
		if c > 0 {
			unique++
			total += c
		}
	}
	if unique == 0 {
		return 0
	}
	return float64(unique) + 0.1*float64(total)
}

// snippetAround returns a substring of content centered on the first
// hit of any term, capped at sessionSearchSnippetLen. Falls back to
// the content prefix when no term is located. start/end are aligned
// to UTF-8 boundaries so multi-byte runes aren't split.
func snippetAround(content string, terms []string) string {
	lower := strings.ToLower(content)
	hitIdx := -1
	for _, t := range terms {
		if i := strings.Index(lower, t); i >= 0 {
			if hitIdx < 0 || i < hitIdx {
				hitIdx = i
			}
		}
	}
	if hitIdx < 0 {
		return truncateSnippet(content, sessionSearchSnippetLen)
	}
	half := sessionSearchSnippetLen / 2
	start := hitIdx - half
	if start < 0 {
		start = 0
	}
	end := start + sessionSearchSnippetLen
	if end > len(content) {
		end = len(content)
	}
	start = utf8AlignForward(content, start)
	end = utf8AlignBackward(content, end)
	if start >= end {
		return ""
	}
	prefix := ""
	if start > 0 {
		prefix = "..."
	}
	suffix := ""
	if end < len(content) {
		suffix = "..."
	}
	return prefix + content[start:end] + suffix
}

func truncateSnippet(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := utf8AlignBackward(s, n)
	return s[:cut] + "..."
}
