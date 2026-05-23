package sessionsearch

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

type testMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func writeSessionLog(t *testing.T, dir, sessionID string, msgs []testMessage) {
	t.Helper()
	path := filepath.Join(dir, sessionID+".jsonl")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, m := range msgs {
		if err := enc.Encode(m); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSearchFindsAndRanksMatches(t *testing.T) {
	dir := t.TempDir()
	writeSessionLog(t, dir, "low", []testMessage{
		{Role: "user", Content: "we mentioned postgres once"},
	})
	writeSessionLog(t, dir, "high", []testMessage{
		{Role: "user", Content: "postgres docker compose postgres setup with postgres tuning"},
	})

	hits, err := Search(context.Background(), dir, "", "postgres docker", 5, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) < 2 {
		t.Fatalf("expected 2 hits, got %+v", hits)
	}
	if hits[0].SessionID != "high" {
		t.Fatalf("higher-overlap session should rank first, got %+v", hits)
	}
	if !strings.Contains(hits[0].Snippet, "postgres") {
		t.Fatalf("snippet should contain match: %+v", hits[0])
	}
}

func TestSearchExcludesCurrentAndSkipsNonConversationRoles(t *testing.T) {
	dir := t.TempDir()
	writeSessionLog(t, dir, "current", []testMessage{
		{Role: "user", Content: "the sqlc bug is here"},
	})
	writeSessionLog(t, dir, "earlier", []testMessage{
		{Role: "system", Content: "sqlc bug hidden in system"},
		{Role: "tool", Content: "sqlc bug hidden in tool"},
		{Role: "assistant", Content: "the sqlc bug was a missing index"},
	})

	hits, err := Search(context.Background(), dir, "current", "sqlc bug", 5, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Fatalf("expected one assistant hit from earlier session, got %+v", hits)
	}
	if hits[0].SessionID != "earlier" || hits[0].Role != "assistant" {
		t.Fatalf("unexpected hit: %+v", hits[0])
	}
}

func TestSearchSkipsSymlinkSessionLogs(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.jsonl")
	if err := os.WriteFile(outside, []byte(`{"role":"user","content":"needle from outside"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "linked.jsonl")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	writeSessionLog(t, dir, "real", []testMessage{
		{Role: "user", Content: "needle from real session"},
	})

	hits, err := Search(context.Background(), dir, "", "needle", 10, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].SessionID != "real" {
		t.Fatalf("search should skip symlink logs and keep real hit, got %+v", hits)
	}
	if _, err := scoreFile(filepath.Join(dir, "linked.jsonl"), "linked", []string{"needle"}, 3, ""); err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("scoreFile symlink error = %v, want regular file", err)
	}
}

func TestSearchCapsPerSession(t *testing.T) {
	dir := t.TempDir()
	writeSessionLog(t, dir, "many", []testMessage{
		{Role: "user", Content: "needle alpha"},
		{Role: "assistant", Content: "needle beta"},
		{Role: "user", Content: "needle gamma"},
	})

	hits, err := Search(context.Background(), dir, "", "needle", 10, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 {
		t.Fatalf("max_per_session=2 should cap hits, got %+v", hits)
	}
}

func TestSearchNormalizesRunawayLimits(t *testing.T) {
	dir := t.TempDir()
	var msgs []testMessage
	for i := 0; i < MaxPerSession+3; i++ {
		msgs = append(msgs, testMessage{Role: "user", Content: "needle repeated repeated"})
	}
	writeSessionLog(t, dir, "many", msgs)

	topK, maxPerSession := NormalizeLimits(1<<30, 1<<30)
	if topK != MaxTopK || maxPerSession != MaxPerSession {
		t.Fatalf("NormalizeLimits runaway = (%d,%d), want (%d,%d)", topK, maxPerSession, MaxTopK, MaxPerSession)
	}
	hits, err := Search(context.Background(), dir, "", "needle", 1<<30, 1<<30)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != MaxPerSession {
		t.Fatalf("runaway max_per_session should cap hits at %d, got %d", MaxPerSession, len(hits))
	}
}

func TestSearchKeepsGlobalTopKWhileScanning(t *testing.T) {
	dir := t.TempDir()
	writeSessionLog(t, dir, "a-low", []testMessage{
		{Role: "user", Content: "needle only"},
	})
	writeSessionLog(t, dir, "b-low", []testMessage{
		{Role: "user", Content: "needle again"},
	})
	writeSessionLog(t, dir, "y-high", []testMessage{
		{Role: "user", Content: "needle strong strong strong"},
	})
	writeSessionLog(t, dir, "z-high", []testMessage{
		{Role: "user", Content: "needle strong strong final"},
	})

	hits, err := Search(context.Background(), dir, "", "needle strong", 2, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 {
		t.Fatalf("top_k=2 should return exactly 2 hits, got %+v", hits)
	}
	gotSessions := map[string]bool{}
	for _, hit := range hits {
		gotSessions[hit.SessionID] = true
	}
	for _, want := range []string{"y-high", "z-high"} {
		if !gotSessions[want] {
			t.Fatalf("bounded global aggregation dropped later high-scoring hit %q: %+v", want, hits)
		}
	}
}

func TestSearchReadsPastOneDirectoryBatch(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < sessionDirReadBatch+2; i++ {
		writeSessionLog(t, dir, fmt.Sprintf("low-%03d", i), []testMessage{
			{Role: "user", Content: "needle only"},
		})
	}
	writeSessionLog(t, dir, "winner", []testMessage{
		{Role: "user", Content: "needle strong strong strong"},
	})

	hits, err := Search(context.Background(), dir, "", "needle strong", 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].SessionID != "winner" {
		t.Fatalf("search should scan beyond one directory batch and keep best hit, got %+v", hits)
	}
}

func TestScoreFileKeepsBestHitsWithinLimitWhileScanning(t *testing.T) {
	dir := t.TempDir()
	writeSessionLog(t, dir, "many", []testMessage{
		{Role: "user", Content: "needle"},
		{Role: "user", Content: "needle strong strong strong"},
		{Role: "user", Content: "needle weak"},
	})
	hits, err := scoreFile(filepath.Join(dir, "many.jsonl"), "many", []string{"needle", "strong"}, 1, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Fatalf("scoreFile limit=1 returned %d hits: %+v", len(hits), hits)
	}
	if !strings.Contains(hits[0].Snippet, "strong") {
		t.Fatalf("bounded scoring should keep best hit, got %+v", hits[0])
	}
}

func TestScoreContentMatchesWholeTokens(t *testing.T) {
	terms := Tokenize("go")
	if got := ScoreContent("ongoing cargo", terms); got != 0 {
		t.Fatalf("substring-only matches should not score, got %v", got)
	}
	if got := ScoreContent("go go cargo", terms); got != 1.2 {
		t.Fatalf("whole-token matches score = %v, want 1.2", got)
	}
}

func TestSearchEmptyAndMissingDirReturnNoHits(t *testing.T) {
	hits, err := Search(context.Background(), t.TempDir(), "", "", 5, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Fatalf("empty query should return no hits, got %+v", hits)
	}

	hits, err = Search(context.Background(), filepath.Join(t.TempDir(), "missing"), "", "x", 5, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Fatalf("missing dir should return no hits, got %+v", hits)
	}
}

func TestTokenize(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"x", nil},
		{"hello world", []string{"hello", "world"}},
		{"PostGRES-Docker.compose", []string{"postgres", "docker", "compose"}},
		{"привет мир", []string{"привет", "мир"}},
		{"café naïve résumé", []string{"café", "naïve", "résumé"}},
		{"What is the secret code", []string{"secret", "code"}},
	}
	for _, c := range cases {
		got := Tokenize(c.in)
		if len(got) != len(c.want) {
			t.Fatalf("Tokenize(%q) = %v, want %v", c.in, got, c.want)
		}
		for i, g := range got {
			if g != c.want[i] {
				t.Fatalf("Tokenize(%q)[%d] = %q, want %q", c.in, i, g, c.want[i])
			}
		}
	}
}

func TestTokenizeDedupesAndCapsTerms(t *testing.T) {
	var parts []string
	for i := 0; i < MaxQueryTerms+5; i++ {
		parts = append(parts, fmt.Sprintf("term%02d", i))
	}
	parts = append(parts, "term00", "term01")

	got := Tokenize(strings.Join(parts, " "))
	if len(got) != MaxQueryTerms {
		t.Fatalf("Tokenize should cap terms at %d, got %d: %v", MaxQueryTerms, len(got), got)
	}
	seen := map[string]bool{}
	for _, term := range got {
		if seen[term] {
			t.Fatalf("Tokenize should dedupe terms, got duplicate %q in %v", term, got)
		}
		seen[term] = true
	}
}

func TestNormalizeQueryCapsBytesSafely(t *testing.T) {
	in := strings.Repeat("界", MaxQueryBytes)
	got := NormalizeQuery(in)
	if len(got) > MaxQueryBytes {
		t.Fatalf("NormalizeQuery returned %d bytes, want <= %d", len(got), MaxQueryBytes)
	}
	if !utf8.ValidString(got) {
		t.Fatalf("NormalizeQuery returned invalid UTF-8: %q", got)
	}
}

func TestSnippetUTF8Safety(t *testing.T) {
	content := strings.Repeat("🔧", 50) + strings.Repeat("привет ", 20) +
		"postgres" + strings.Repeat(" привет", 20) + strings.Repeat("🔧", 50)
	snip := SnippetAround(content, []string{"postgres"})
	if !utf8.ValidString(snip) {
		t.Fatalf("snippet is not valid UTF-8: %q", snip)
	}
	if !strings.Contains(snip, "postgres") {
		t.Fatalf("snippet should contain query term: %q", snip)
	}

	out := TruncateSnippet(strings.Repeat("🔧", 200), 50)
	if !utf8.ValidString(out) {
		t.Fatalf("truncated snippet is not valid UTF-8: %q", out)
	}
}

func TestSnippetAroundUsesWholeTokenHit(t *testing.T) {
	content := "ongoing " + strings.Repeat("filler ", 180) + "go final"
	snip := SnippetAround(content, Tokenize("go"))
	if strings.Contains(snip, "ongoing") {
		t.Fatalf("snippet centered on substring-only hit: %q", snip)
	}
	if !strings.Contains(snip, "go final") {
		t.Fatalf("snippet should include whole-token hit: %q", snip)
	}
}
