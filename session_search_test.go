package affent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func utf8ValidStr(s string) bool { return utf8.ValidString(s) }

// writeSessionLog writes a JSONL session log with the given messages
// and returns the file's session id (filename minus ".jsonl").
func writeSessionLog(t *testing.T, dir, sessionID string, msgs []ChatMessage) {
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

func TestSessionSearch_FindsMatch(t *testing.T) {
	dir := t.TempDir()
	writeSessionLog(t, dir, "sess_a", []ChatMessage{
		{Role: "system", Content: "you are an agent"},
		{Role: "user", Content: "how do I configure sqlc in this project?"},
		{Role: "assistant", Content: "Add a sqlc.yaml at the repo root with engine: postgresql."},
	})
	tool := sessionSearchTool(dir, "" /* current session = none */)

	out, err := tool.Execute(context.Background(), json.RawMessage(`{"query":"sqlc configure"}`))
	if err != nil {
		t.Fatal(err)
	}
	var resp SessionSearchResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("response not JSON: %v: %s", err, out)
	}
	if resp.Total == 0 {
		t.Fatalf("expected at least one result, got %+v", resp)
	}
	if !strings.Contains(resp.Results[0].Snippet, "sqlc") {
		t.Fatalf("snippet should contain match: %+v", resp.Results[0])
	}
}

func TestSessionSearch_ExcludesCurrentSession(t *testing.T) {
	dir := t.TempDir()
	writeSessionLog(t, dir, "current", []ChatMessage{
		{Role: "user", Content: "find the sqlc bug"},
	})
	writeSessionLog(t, dir, "earlier", []ChatMessage{
		{Role: "user", Content: "the sqlc bug was a missing index"},
	})

	tool := sessionSearchTool(dir, "current")
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"query":"sqlc bug"}`))
	if err != nil {
		t.Fatal(err)
	}
	var resp SessionSearchResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Total != 1 {
		t.Fatalf("expected exactly one hit (current session excluded), got %d: %+v", resp.Total, resp.Results)
	}
	if resp.Results[0].SessionID != "earlier" {
		t.Fatalf("expected hit from 'earlier' session, got %s", resp.Results[0].SessionID)
	}
}

func TestSessionSearch_SkipsSystemAndToolMessages(t *testing.T) {
	dir := t.TempDir()
	writeSessionLog(t, dir, "sess", []ChatMessage{
		{Role: "system", Content: "secret keyword here"},
		{Role: "tool", Content: "secret keyword in tool result"},
		{Role: "assistant", Content: "no match here"},
	})
	tool := sessionSearchTool(dir, "")
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"query":"secret keyword"}`))
	if err != nil {
		t.Fatal(err)
	}
	var resp SessionSearchResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Total != 0 {
		t.Fatalf("system/tool messages must be skipped, got hits: %+v", resp.Results)
	}
}

func TestSessionSearch_RanksByScore(t *testing.T) {
	dir := t.TempDir()
	writeSessionLog(t, dir, "low", []ChatMessage{
		{Role: "user", Content: "we mentioned postgres once"},
	})
	writeSessionLog(t, dir, "high", []ChatMessage{
		{Role: "user", Content: "postgres docker compose postgres setup with postgres tuning"},
	})
	tool := sessionSearchTool(dir, "")
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"query":"postgres docker"}`))
	if err != nil {
		t.Fatal(err)
	}
	var resp SessionSearchResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Total < 2 {
		t.Fatalf("expected 2 results, got %d", resp.Total)
	}
	if resp.Results[0].SessionID != "high" {
		t.Fatalf("higher-overlap session should rank first, got order %v", resp.Results)
	}
}

func TestSessionSearch_MaxPerSession(t *testing.T) {
	dir := t.TempDir()
	msgs := []ChatMessage{
		{Role: "user", Content: "needle alpha"},
		{Role: "assistant", Content: "needle beta"},
		{Role: "user", Content: "needle gamma"},
		{Role: "assistant", Content: "needle delta"},
		{Role: "user", Content: "needle epsilon"},
	}
	writeSessionLog(t, dir, "many", msgs)

	tool := sessionSearchTool(dir, "")
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"query":"needle","top_k":10,"max_per_session":2}`))
	if err != nil {
		t.Fatal(err)
	}
	var resp SessionSearchResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Total != 2 {
		t.Fatalf("max_per_session=2 should cap hits, got %d", resp.Total)
	}
}

func TestSessionSearch_EmptyQuery(t *testing.T) {
	tool := sessionSearchTool(t.TempDir(), "")
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"query":""}`))
	if err != nil {
		t.Fatal(err)
	}
	var resp SessionSearchResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(resp.Message, "query is required") {
		t.Fatalf("expected query-required message, got %+v", resp)
	}
}

func TestSessionSearch_MissingSessionsDir(t *testing.T) {
	tool := sessionSearchTool("", "")
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"query":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	var resp SessionSearchResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(resp.Message, "not configured") {
		t.Fatalf("expected 'not configured' message, got %+v", resp)
	}
}

func TestSessionSearch_NonexistentDirReturnsEmpty(t *testing.T) {
	tool := sessionSearchTool(filepath.Join(t.TempDir(), "does-not-exist"), "")
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"query":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	var resp SessionSearchResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Total != 0 {
		t.Fatalf("nonexistent dir should yield zero hits, got %+v", resp)
	}
}

func TestTokenize(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"x", nil}, // single-letter dropped
		{"hello world", []string{"hello", "world"}},
		{"PostGRES-Docker.compose", []string{"postgres", "docker", "compose"}},
		{"foo, bar; baz!", []string{"foo", "bar", "baz"}},
		// Non-ASCII letters must survive across scripts.
		{"привет мир", []string{"привет", "мир"}},                  // Cyrillic
		{"γεια κόσμος", []string{"γεια", "κόσμος"}},                // Greek
		{"café naïve résumé", []string{"café", "naïve", "résumé"}}, // Latin w/ diacritics
		// Stopword filter: common English filler words drop, content
		// words survive. Tested separately in TestTokenize_DropsStopwords
		// to catch the inverse (stopword-only queries → empty).
		{"What is the secret code", []string{"secret", "code"}},
	}
	for _, c := range cases {
		got := tokenize(c.in)
		if len(got) != len(c.want) {
			t.Fatalf("tokenize(%q) = %v, want %v", c.in, got, c.want)
		}
		for i, g := range got {
			if g != c.want[i] {
				t.Fatalf("tokenize(%q)[%d] = %q, want %q", c.in, i, g, c.want[i])
			}
		}
	}
}

// TestTokenize_DropsStopwords pins the precision fix: a stopword-only
// query produces no terms, so session_search returns nothing instead
// of substring-matching "no" inside "know" across every past message.
// Surfaced in real-LLM testing where `zzzz-no-such-thing-9999` got
// three spurious low-score hits because "no" / "such" / "thing"
// were treated as content terms.
func TestTokenize_DropsStopwords(t *testing.T) {
	cases := map[string][]string{
		"what is the":            nil, // pure-stopword query → no terms
		"the and of":             nil,
		"no such thing":          {"such", "thing"}, // "no" drops, content stays
		"deploy the new release": {"deploy", "new", "release"},
		"the deploy itself":      {"deploy", "itself"},
	}
	for in, want := range cases {
		got := tokenize(in)
		if len(got) != len(want) {
			t.Errorf("tokenize(%q) = %v, want %v", in, got, want)
			continue
		}
		for i, g := range got {
			if g != want[i] {
				t.Errorf("tokenize(%q)[%d] = %q, want %q", in, i, g, want[i])
			}
		}
	}
}

func TestSessionSearch_NonASCIIQueryFindsMatches(t *testing.T) {
	dir := t.TempDir()
	writeSessionLog(t, dir, "sess", []ChatMessage{
		{Role: "user", Content: "где находится конфиг postgres?"},
		{Role: "assistant", Content: "Конфиг лежит в /etc/postgresql/postgresql.conf"},
	})
	tool := sessionSearchTool(dir, "")
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"query":"конфиг postgres"}`))
	if err != nil {
		t.Fatal(err)
	}
	var resp SessionSearchResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Total == 0 {
		t.Fatalf("expected hits for non-ASCII query, got %+v", resp)
	}
}

func TestSnippetAround_UTF8Safety(t *testing.T) {
	// Content surrounds the query with 4-byte emoji and 2-byte
	// Cyrillic runes. Byte-aligned slicing would split mid-rune; the
	// returned snippet must remain valid UTF-8.
	content := strings.Repeat("🔧", 50) + strings.Repeat("привет ", 20) +
		"postgres" + strings.Repeat(" привет", 20) + strings.Repeat("🔧", 50)
	snip := snippetAround(content, []string{"postgres"})
	if !utf8ValidStr(snip) {
		t.Fatalf("snippet is not valid UTF-8: %q", snip)
	}
	if !strings.Contains(snip, "postgres") {
		t.Fatalf("snippet should contain the query term: %q", snip)
	}
}

func TestTruncateSnippet_UTF8Safety(t *testing.T) {
	s := strings.Repeat("🔧", 200) // 800 bytes, all 4-byte emoji
	out := truncateSnippet(s, 50)
	if !utf8ValidStr(out) {
		t.Fatalf("truncated snippet is not valid UTF-8: %q", out)
	}
}
