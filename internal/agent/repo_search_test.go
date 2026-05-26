package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRepoSearchToolFindsCodeAndSkipsHiddenDirs(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("generated/\n*.jsonl\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustWrite := func(path, content string) {
		t.Helper()
		full := filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite("internal/worker.go", `package internal

func RepoSearch() {}
`)
	mustWrite("generated/skip.go", `package generated

func ShouldBeIgnored() {}
`)
	mustWrite("logs/run.jsonl", `{"ignore":true}`)
	mustWrite(".git/ignored.go", `package ignored

func HiddenHit() {}
`)
	tool := repoSearchTool(BuiltinDeps{HostWorkspaceDir: dir})
	got, err := tool.Execute(context.Background(), json.RawMessage(`{"query":"RepoSearch","path":".","max_results":5}`))
	if err != nil {
		t.Fatalf("repo_search: %v", err)
	}
	if !strings.Contains(got, "internal/worker.go") || !strings.Contains(got, "RepoSearch") {
		t.Fatalf("repo_search missing expected hit:\n%s", got)
	}
	if strings.Contains(got, ".git") || strings.Contains(got, "HiddenHit") {
		t.Fatalf("repo_search should skip hidden dirs:\n%s", got)
	}
	if strings.Contains(got, "generated/skip.go") || strings.Contains(got, "ShouldBeIgnored") || strings.Contains(got, "logs/run.jsonl") {
		t.Fatalf("repo_search should skip gitignored files:\n%s", got)
	}
}

func TestRepoSearchToolMatchesSymbolVariants(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "internal"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "internal", "worker.go"), []byte("package internal\n\nfunc repoSearchTool() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := repoSearchTool(BuiltinDeps{HostWorkspaceDir: dir})
	got, err := tool.Execute(context.Background(), json.RawMessage(`{"query":"repo_search","path":"internal","max_results":5}`))
	if err != nil {
		t.Fatalf("repo_search: %v", err)
	}
	if !strings.Contains(got, "internal/worker.go") || !strings.Contains(got, "repoSearchTool") {
		t.Fatalf("repo_search should match symbol variants:\n%s", got)
	}
}

func TestRepoSearchToolNoMatchesExplainsNextStep(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "note.txt"), []byte("hello world"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := repoSearchTool(BuiltinDeps{HostWorkspaceDir: dir})
	got, err := tool.Execute(context.Background(), json.RawMessage(`{"query":"needle","path":".","max_results":3}`))
	if err != nil {
		t.Fatalf("repo_search: %v", err)
	}
	for _, want := range []string{"no matches", "Next:", "broaden the query"} {
		if !strings.Contains(got, want) {
			t.Fatalf("repo_search missing %q:\n%s", want, got)
		}
	}
}
