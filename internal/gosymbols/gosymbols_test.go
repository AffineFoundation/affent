package gosymbols

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/affinefoundation/affent/internal/workspaceignore"
)

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestScanWorkspaceAndSearch(t *testing.T) {
	dir := t.TempDir()
	mustDir := func(name string) {
		if err := os.MkdirAll(filepath.Join(dir, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	mustDir("internal/agent")
	writeFile(t, dir, "internal/agent/repo_search.go", `package agent

type BuiltinDeps struct{}

func repoSearchTool(deps BuiltinDeps) {}
`)
	writeFile(t, dir, "internal/agent/other.go", `package agent

const Something = 1
`)
	writeFile(t, dir, "internal/agent/repo_search_test.go", `package agent

func TestIgnoreMe(t *testing.T) {}
`)

	ignore, err := workspaceignore.LoadGitignore(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ScanWorkspace(context.Background(), dir, filepath.Join(dir, "internal"), mustStat(t, filepath.Join(dir, "internal")), ignore, ScanOptions{IncludeTests: false})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Records) == 0 {
		t.Fatal("expected symbol records")
	}
	if strings.Contains(joinRecords(got.Records), "TestIgnoreMe") {
		t.Fatalf("test symbols should be skipped:\n%s", joinRecords(got.Records))
	}
	if !strings.Contains(joinRecords(got.Records), "repoSearchTool") {
		t.Fatalf("missing repoSearchTool record:\n%s", joinRecords(got.Records))
	}
	hits := got.Search("repo_search implementation", 5)
	if len(hits) == 0 {
		t.Fatalf("expected search hits, records=%s", joinRecords(got.Records))
	}
	if !strings.Contains(hits[0].RelPath, "internal/agent/repo_search.go") || hits[0].Name != "repoSearchTool" {
		t.Fatalf("unexpected top hit: %+v", hits[0])
	}
}

func TestScanWorkspaceRespectsGitignore(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".gitignore", "generated/\n*.jsonl\n")
	mustDir := func(name string) {
		if err := os.MkdirAll(filepath.Join(dir, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	mustDir("generated")
	mustDir("internal/app")
	writeFile(t, dir, "generated/skip.go", `package generated

func Hidden() {}
`)
	writeFile(t, dir, "internal/app/main.go", `package app

func Visible() {}
`)
	writeFile(t, dir, "run.jsonl", "ignored")

	ignore, err := workspaceignore.LoadGitignore(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ScanWorkspace(context.Background(), dir, dir, mustStat(t, dir), ignore, ScanOptions{IncludeTests: true})
	if err != nil {
		t.Fatal(err)
	}
	joined := joinRecords(got.Records)
	if strings.Contains(joined, "Hidden") || strings.Contains(joined, "run.jsonl") {
		t.Fatalf("gitignored content should be skipped:\n%s", joined)
	}
	if !strings.Contains(joined, "Visible") {
		t.Fatalf("visible symbol should remain:\n%s", joined)
	}
}

func TestSearchRanksExactSymbolName(t *testing.T) {
	result := ScanResult{
		Records: []SymbolRecord{
			{RelPath: "internal/agent/repo_search.go", Name: "repoSearchTool", Kind: "func", Signature: "func repoSearchTool()"},
			{RelPath: "internal/agent/loop.go", Name: "DefaultSystemPrompt", Kind: "var", Signature: "var DefaultSystemPrompt = \"\""},
		},
	}
	hits := result.Search("repo_search", 3)
	if len(hits) == 0 {
		t.Fatal("expected hits")
	}
	if hits[0].Name != "repoSearchTool" {
		t.Fatalf("expected repoSearchTool first, got %+v", hits[0])
	}
}

func mustStat(t *testing.T, path string) os.FileInfo {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return fi
}

func joinRecords(records []SymbolRecord) string {
	var parts []string
	for _, rec := range records {
		parts = append(parts, rec.RelPath+":"+rec.Name+":"+rec.Signature)
	}
	return strings.Join(parts, "\n")
}
