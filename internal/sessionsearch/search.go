package sessionsearch

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/affinefoundation/affent/internal/textutil"
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
	Total   int    `json:"total"`
	Results []Hit  `json:"results"`
	Message string `json:"message,omitempty"`
}

// Search scans sessionsDir/*.jsonl for messages matching query. The
// current session is excluded. Scoring is lexical term overlap, which
// is enough for local recall over one workspace's transcript history.
const (
	DefaultTopK          = 5
	MaxTopK              = 20
	DefaultMaxPerSession = 3
	MaxPerSession        = 5
	MaxQueryBytes        = 2048
	MaxQueryTerms        = 16

	sessionDirReadBatch = 128
)

func Search(ctx context.Context, sessionsDir, currentSessionID, query string, topK, maxPerSession int) ([]Hit, error) {
	topK, maxPerSession = NormalizeLimits(topK, maxPerSession)
	terms := Tokenize(query)
	if len(terms) == 0 {
		return nil, nil
	}
	dir, err := os.Open(sessionsDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer dir.Close()
	var all []Hit
	for {
		entries, rerr := dir.ReadDir(sessionDirReadBatch)
		if rerr != nil && !errors.Is(rerr, io.EOF) {
			return nil, rerr
		}
		for _, ent := range entries {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".jsonl") {
				continue
			}
			if entryIsSymlink(ent) {
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
			hits, serr := scoreFile(ctx, filepath.Join(sessionsDir, ent.Name()), sid, terms, maxPerSession, mtime)
			if serr != nil {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				continue
			}
			for _, hit := range hits {
				all = appendBoundedHits(all, hit, topK)
			}
		}
		if errors.Is(rerr, io.EOF) {
			break
		}
	}
	sortHits(all)
	if topK > 0 && len(all) > topK {
		all = all[:topK]
	}
	return all, nil
}

func NormalizeLimits(topK, maxPerSession int) (int, int) {
	if topK <= 0 {
		topK = DefaultTopK
	}
	if topK > MaxTopK {
		topK = MaxTopK
	}
	if maxPerSession <= 0 {
		maxPerSession = DefaultMaxPerSession
	}
	if maxPerSession > MaxPerSession {
		maxPerSession = MaxPerSession
	}
	return topK, maxPerSession
}

func scoreFile(ctx context.Context, path, sid string, terms []string, maxPerSession int, mtime string) ([]Hit, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("session log must be a regular file")
	}
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
		if err := ctx.Err(); err != nil {
			return nil, err
		}
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
		fileHits = appendBoundedHits(fileHits, Hit{
			SessionID: sid,
			TurnIdx:   turn,
			Role:      m.Role,
			Snippet:   SnippetAround(content, terms),
			Score:     score,
			ModTime:   mtime,
		}, maxPerSession)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	sortHits(fileHits)
	if maxPerSession > 0 && len(fileHits) > maxPerSession {
		fileHits = fileHits[:maxPerSession]
	}
	return fileHits, nil
}

func entryIsSymlink(ent os.DirEntry) bool {
	info, err := ent.Info()
	return err == nil && info.Mode()&os.ModeSymlink != 0
}

func appendBoundedHits(hits []Hit, hit Hit, limit int) []Hit {
	if limit <= 0 {
		return append(hits, hit)
	}
	hits = append(hits, hit)
	sortHits(hits)
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits
}

func sortHits(hits []Hit) {
	sort.SliceStable(hits, func(i, j int) bool { return hitLess(hits[i], hits[j]) })
}

func hitLess(a, b Hit) bool {
	if a.Score != b.Score {
		return a.Score > b.Score
	}
	if a.ModTime != b.ModTime {
		return a.ModTime > b.ModTime
	}
	if a.SessionID != b.SessionID {
		return a.SessionID < b.SessionID
	}
	return a.TurnIdx < b.TurnIdx
}

// Tokenize lowercases and splits on non-letter / non-digit runes
// across scripts. Tokens shorter than 2 bytes and common English
// stopwords are dropped.
func Tokenize(s string) []string {
	s = NormalizeQuery(s)
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
	seen := map[string]bool{}
	for _, t := range raw {
		if stopwords[t] || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
		if len(out) >= MaxQueryTerms {
			break
		}
	}
	return out
}

func NormalizeQuery(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= MaxQueryBytes {
		return s
	}
	cut := textutil.AlignBackward(s, MaxQueryBytes)
	return strings.TrimSpace(s[:cut])
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
	counts := countContentTerms(content, terms)
	unique, total := 0, 0
	for _, c := range counts {
		unique++
		total += c
	}
	if unique == 0 {
		return 0
	}
	return float64(unique) + 0.1*float64(total)
}

func countContentTerms(content string, terms []string) map[string]int {
	want := make(map[string]bool, len(terms))
	for _, term := range terms {
		if term != "" {
			want[term] = true
		}
	}
	counts := map[string]int{}
	var cur strings.Builder
	flush := func() {
		t := strings.ToLower(cur.String())
		cur.Reset()
		if want[t] {
			counts[t]++
		}
	}
	for _, r := range content {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			cur.WriteRune(r)
		} else {
			flush()
		}
	}
	flush()
	return counts
}

// snippetLen is intentionally larger than a search-engine teaser. Session
// search is used by the agent as working evidence; small models often need the
// surrounding conclusion/test result in the same hit instead of a clipped
// fragment that forces another query.
const snippetLen = 900

// SnippetAround returns a UTF-8-safe substring of content centered on
// the first term hit.
func SnippetAround(content string, terms []string) string {
	hitIdx := firstTermTokenIndex(content, terms)
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
	start = textutil.AlignForward(content, start)
	end = textutil.AlignBackward(content, end)
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

type termHit struct {
	start int
	term  string
}

func firstTermTokenIndex(content string, terms []string) int {
	want := make(map[string]bool, len(terms))
	for _, term := range terms {
		if term != "" {
			want[term] = true
		}
	}
	bestStart := -1
	bestCovered := 0
	var window []termHit
	counts := map[string]int{}
	half := snippetLen / 2
	consider := func(hit termHit) {
		window = append(window, hit)
		counts[hit.term]++
		minStart := hit.start - half
		trim := 0
		for trim < len(window) && window[trim].start < minStart {
			counts[window[trim].term]--
			if counts[window[trim].term] == 0 {
				delete(counts, window[trim].term)
			}
			trim++
		}
		if trim > 0 {
			window = window[trim:]
		}
		if len(counts) > bestCovered {
			bestStart = hit.start
			bestCovered = len(counts)
		}
	}
	tokenStart := -1
	var cur strings.Builder
	flush := func() {
		t := strings.ToLower(cur.String())
		cur.Reset()
		if tokenStart >= 0 && want[t] {
			consider(termHit{start: tokenStart, term: t})
		}
		tokenStart = -1
	}
	for i, r := range content {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if tokenStart < 0 {
				tokenStart = i
			}
			cur.WriteRune(r)
			continue
		}
		flush()
	}
	flush()
	return bestStart
}

func TruncateSnippet(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := textutil.AlignBackward(s, n)
	return s[:cut] + "..."
}
