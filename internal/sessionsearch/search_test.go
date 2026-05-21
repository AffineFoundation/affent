package sessionsearch

import (
	"context"
	"encoding/json"
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
