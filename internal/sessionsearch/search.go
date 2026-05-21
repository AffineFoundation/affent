package sessionsearch

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Hit is one matched message from a past session.
type Hit struct {
	SessionID string  `json:"session_id"`
	TurnIdx   int     `json:"turn_idx"`
	Role      string  `json:"role"`
	Snippet   string  `json:"snippet"`
	Score     float64 `json:"score"`
	ModTime   string  `json:"mod_time,omitempty"`
}

// Response is the session_search tool return shape.
type Response struct {
	Query   string `json:"query"`
	Total   int   `json:"total"`
	Results []Hit  `json:"results"`
	Message string `json:"message,omitempty"`
}

// Search scans sessionsDir/*.jsonl for messages matching query. The
// current session is excluded. Scoring is lexical term overlap, which
// is enough for local recall over one workspace's transcript history.
func Search(ctx context.Context, sessionsDir, currentSessionID, query string, topK, maxPerSession int) ([]Hit, error) {
	terms := Tokenize(query)
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
	var all []Hit
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
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].Score != all[j].Score {
			return all[i].Score > all[j].Score
		}
		return all[i].ModTime > all[j].ModTime
	})
	if topK > 0 && len(all) > topK {
		all = all[:topK]
	}
	return all, nil
}

func scoreFile(path, sid string, terms []string, maxPerSession int, mtime string) ([]Hit, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	var fileHits []Hit
	turn := 0
	for sc.Scan() {
		turn++
		var m struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		}
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
		score := ScoreContent(content, terms)
		if score <= 0 {
			continue
		}
		fileHits = append(fileHits, Hit{
			SessionID: sid,
			TurnIdx:   turn,
			Role:      m.Role,
			Snippet:   SnippetAround(content, terms),
			Score:     score,
			ModTime:   mtime,
		})
	}
	sort.SliceStable(fileHits, func(i, j int) bool { return fileHits[i].Score > fileHits[j].Score })
	if maxPerSession > 0 && len(fileHits) > maxPerSession {
		fileHits = fileHits[:maxPerSession]
	}
	return fileHits, nil
}

// Tokenize lowercases and splits on non-letter / non-digit runes
// across scripts. Tokens shorter than 2 bytes and common English
// stopwords are dropped.
func Tokenize(s string) []string {
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

// ScoreContent counts unique-term overlap with a small boost for
// total-occurrence count.
func ScoreContent(content string, terms []string) float64 {
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

const snippetLen = 300

// SnippetAround returns a UTF-8-safe substring of content centered on
// the first term hit.
func SnippetAround(content string, terms []string) string {
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
		return TruncateSnippet(content, snippetLen)
	}
	half := snippetLen / 2
	start := hitIdx - half
	if start < 0 {
		start = 0
	}
	end := start + snippetLen
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

func TruncateSnippet(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := utf8AlignBackward(s, n)
	return s[:cut] + "..."
}

func utf8AlignBackward(s string, n int) int {
	if n <= 0 {
		return 0
	}
	if n >= len(s) {
		return len(s)
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return n
}

func utf8AlignForward(s string, n int) int {
	if n <= 0 {
		return 0
	}
	if n >= len(s) {
		return len(s)
	}
	for n < len(s) && !utf8.RuneStart(s[n]) {
		n++
	}
	return n
}
