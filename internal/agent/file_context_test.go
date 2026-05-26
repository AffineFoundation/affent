package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFileContextToolSummarizesLongFileAndMatchesQuery(t *testing.T) {
	dir := t.TempDir()
	var b strings.Builder
	for i := 1; i <= 60; i++ {
		if i == 29 {
			b.WriteString("line 29: target needle appears here with extra detail\n")
			continue
		}
		fmt.Fprintf(&b, "line %02d: filler text for context\n", i)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := fileContextTool(BuiltinDeps{HostWorkspaceDir: dir})
	got, err := tool.Execute(context.Background(), json.RawMessage(`{"path":"notes.txt","query":"needle","max_bytes":1200,"context_lines":3,"max_matches":2}`))
	if err != nil {
		t.Fatalf("file_context: %v", err)
	}
	var out struct {
		Path      string `json:"path"`
		Truncated bool   `json:"truncated"`
		Lines     int    `json:"lines"`
		Head      []struct {
			Line int    `json:"line"`
			Text string `json:"text"`
		} `json:"head"`
		Matches []struct {
			StartLine int    `json:"start_line"`
			EndLine   int    `json:"end_line"`
			HitLine   int    `json:"hit_line"`
			Text      string `json:"text"`
		} `json:"matches"`
		Tail []struct {
			Line int    `json:"line"`
			Text string `json:"text"`
		} `json:"tail"`
	}
	if err := json.Unmarshal([]byte(got), &out); err != nil {
		t.Fatalf("decode response: %v\n%s", err, got)
	}
	if out.Path != "notes.txt" {
		t.Fatalf("path = %q", out.Path)
	}
	if !out.Truncated {
		t.Fatalf("expected truncated file_context result, got %#v", out)
	}
	if out.Lines == 0 || len(out.Head) == 0 || len(out.Matches) == 0 {
		t.Fatalf("expected head and matches, got %+v", out)
	}
	if out.Matches[0].HitLine != 29 || !strings.Contains(out.Matches[0].Text, "needle") {
		t.Fatalf("query hit missing or wrong: %+v", out.Matches[0])
	}
	if len(out.Tail) == 0 {
		t.Fatalf("expected tail for truncated file_context result: %+v", out)
	}
}

func TestFileContextToolIncludesGoSymbols(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "internal", "agent"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "internal", "agent", "demo.go"), []byte(`package agent

type Demo struct{}

func DemoOne() {}
func DemoTwo() {}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := fileContextTool(BuiltinDeps{HostWorkspaceDir: dir})
	got, err := tool.Execute(context.Background(), json.RawMessage(`{"path":"internal/agent/demo.go","max_bytes":2048,"context_lines":2,"max_matches":3}`))
	if err != nil {
		t.Fatalf("file_context: %v", err)
	}
	var out struct {
		Symbols []struct {
			Name string `json:"name"`
			Kind string `json:"kind"`
			Line int    `json:"line"`
		} `json:"symbols"`
	}
	if err := json.Unmarshal([]byte(got), &out); err != nil {
		t.Fatalf("decode response: %v\n%s", err, got)
	}
	if len(out.Symbols) < 2 {
		t.Fatalf("expected go symbols in file_context result, got %+v", out)
	}
	if out.Symbols[0].Name == "" || out.Symbols[0].Line == 0 {
		t.Fatalf("invalid symbol payload: %+v", out.Symbols[0])
	}
}
