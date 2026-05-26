package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSymbolContextToolFindsExactSymbol(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "internal", "agent"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "internal", "agent", "repo_search.go"), []byte(`package agent

type BuiltinDeps struct{}

func repoSearchTool(deps BuiltinDeps) {}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := symbolContextTool(BuiltinDeps{HostWorkspaceDir: dir})
	got, err := tool.Execute(context.Background(), json.RawMessage(`{"query":"repoSearchTool","path":"internal/agent","max_results":3}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"found 1 symbol hit", "repo_search.go", "repoSearchTool", "[agent]", "func repoSearchTool"} {
		if !strings.Contains(got, want) {
			t.Fatalf("symbol_context missing %q:\n%s", want, got)
		}
	}
}

func TestSymbolContextToolDefaultsPathToWorkspaceRoot(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "cmd", "affentctl"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cmd", "affentctl", "common.go"), []byte(`package main

type runtimeCapabilities struct {
	SymbolContext bool
}

func resolveRuntimeCapabilities() runtimeCapabilities {
	return runtimeCapabilities{SymbolContext: true}
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := symbolContextTool(BuiltinDeps{HostWorkspaceDir: dir})
	got, err := tool.Execute(context.Background(), json.RawMessage(`{"query":"runtime capabilities","max_results":3}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"found 2 symbol hit", "cmd/affentctl/common.go", "resolveRuntimeCapabilities", "[main]", "func resolveRuntimeCapabilities"} {
		if !strings.Contains(got, want) {
			t.Fatalf("symbol_context default path missing %q:\n%s", want, got)
		}
	}
}
