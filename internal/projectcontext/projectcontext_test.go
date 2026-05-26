package projectcontext

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoad_NoneFound(t *testing.T) {
	if got := Load(t.TempDir()); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}

func TestLoad_EmptyDirArg(t *testing.T) {
	if got := Load(""); got != "" {
		t.Fatalf("expected empty for empty dir, got %q", got)
	}
}

func TestLoad_SingleAgentsMd(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "AGENTS.md", "Use Go 1.22. Tests via `make test`.")

	got := Load(dir)
	if !strings.Contains(got, "AGENTS.md") {
		t.Fatalf("missing filename header:\n%s", got)
	}
	if !strings.Contains(got, "Go 1.22") {
		t.Fatalf("missing content:\n%s", got)
	}
	if !strings.Contains(got, "PROJECT CONTEXT") {
		t.Fatalf("missing project-context header:\n%s", got)
	}
}

func TestLoad_PreservesOrder(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "CONVENTIONS.md", "third")
	writeFile(t, dir, "AGENTS.md", "first")
	writeFile(t, dir, "CLAUDE.md", "second")

	got := Load(dir)
	posAgents := strings.Index(got, "first")
	posClaude := strings.Index(got, "second")
	posConv := strings.Index(got, "third")
	if posAgents < 0 || posClaude < 0 || posConv < 0 {
		t.Fatalf("missing files in output:\n%s", got)
	}
	if !(posAgents < posClaude && posClaude < posConv) {
		t.Fatalf("expected order AGENTS -> CLAUDE -> CONVENTIONS, got positions %d %d %d",
			posAgents, posClaude, posConv)
	}
}

func TestLoad_SkipsEmptyFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "AGENTS.md", "   \n\n  ")
	writeFile(t, dir, "CONVENTIONS.md", "real content")
	got := Load(dir)
	if strings.Contains(got, "AGENTS.md") {
		t.Fatalf("empty AGENTS.md should be skipped, got:\n%s", got)
	}
	if !strings.Contains(got, "real content") {
		t.Fatalf("CONVENTIONS.md should be included, got:\n%s", got)
	}
}

func TestLoad_TruncatesAtBudget(t *testing.T) {
	dir := t.TempDir()
	huge := strings.Repeat("a", MaxBytes*2)
	writeFile(t, dir, "AGENTS.md", huge)

	got := Load(dir)
	if len(got) > MaxBytes+512 {
		t.Fatalf("output exceeded budget significantly: %d bytes > %d", len(got), MaxBytes)
	}
	if !strings.Contains(got, "truncated") {
		t.Fatalf("expected truncation marker:\n%s", got[:200])
	}
}

func TestReadContextFileReadsOnlyLimitPlusSentinel(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "AGENTS.md")
	if err := os.WriteFile(path, []byte(strings.Repeat("a", 100)+"TAIL"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, truncated, err := readContextFile(path, 10)
	if err != nil {
		t.Fatal(err)
	}
	if !truncated {
		t.Fatal("expected truncated=true for file larger than limit")
	}
	if got != strings.Repeat("a", 10) {
		t.Fatalf("got %q, want first 10 bytes only", got)
	}
	if strings.Contains(got, "TAIL") {
		t.Fatalf("limited read leaked tail content: %q", got)
	}
}

func TestReadContextFile_UTF8SafeLimit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "AGENTS.md")
	if err := os.WriteFile(path, []byte(strings.Repeat("你", 20)), 0o644); err != nil {
		t.Fatal(err)
	}
	got, truncated, err := readContextFile(path, 10)
	if err != nil {
		t.Fatal(err)
	}
	if !truncated {
		t.Fatal("expected truncated=true")
	}
	if !utf8.ValidString(got) {
		t.Fatalf("limited read returned invalid UTF-8: %q", got)
	}
}

func TestLoad_StopsAtBudgetAcrossFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "AGENTS.md", strings.Repeat("a", MaxBytes-30))
	writeFile(t, dir, "CONVENTIONS.md", "should be skipped")

	got := Load(dir)
	if strings.Contains(got, "should be skipped") {
		t.Fatalf("over-budget secondary file should be skipped, got tail:\n%s",
			got[max(0, len(got)-300):])
	}
	if !strings.Contains(got, "AGENTS.md") {
		t.Fatalf("AGENTS.md should still be included")
	}
}

func TestLoad_RecognizesAllSupportedFiles(t *testing.T) {
	dir := t.TempDir()
	for _, name := range Files {
		writeFile(t, dir, name, "content for "+name)
	}
	got := Load(dir)
	for _, name := range Files {
		if !strings.Contains(got, name) {
			t.Fatalf("expected %s in output:\n%s", name, got)
		}
		if !strings.Contains(got, "content for "+name) {
			t.Fatalf("expected body of %s in output", name)
		}
	}
}

func TestLoad_IncludesRepoMap(t *testing.T) {
	dir := t.TempDir()
	mustDir := func(name string) {
		if err := os.Mkdir(filepath.Join(dir, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	mustDir("cmd")
	mustDir("internal")
	mustDir("docs")
	mustDir(".git")
	mustDir("node_modules")
	mustDir(filepath.Join("cmd", "affentctl"))
	mustDir(filepath.Join("cmd", "affentserve"))
	writeFile(t, dir, "go.mod", "module example.test")

	got := Load(dir)
	for _, want := range []string{
		"REPO MAP",
		"Top-level directories:",
		"- cmd/",
		"affentctl",
		"affentserve",
		"- internal/",
		"- docs/",
		"Top-level files:",
		"go.mod",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("repo map missing %q:\n%s", want, got)
		}
	}
	for _, forbidden := range []string{".git/", "node_modules"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("repo map should skip %q:\n%s", forbidden, got)
		}
	}
}

func TestLoad_RespectsGitignoreInRepoMapAndGoHints(t *testing.T) {
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
	writeFile(t, dir, "run.jsonl", "ignored runtime record")

	got := Load(dir)
	if strings.Contains(got, "generated/skip.go") || strings.Contains(got, "Hidden") || strings.Contains(got, "run.jsonl") {
		t.Fatalf("project context should skip gitignored content:\n%s", got)
	}
	if !strings.Contains(got, "internal/app") || !strings.Contains(got, "Visible") {
		t.Fatalf("project context should still include visible Go hints:\n%s", got)
	}
}

func TestLoad_IncludesGoSymbolHints(t *testing.T) {
	dir := t.TempDir()
	mustDir := func(name string) {
		if err := os.MkdirAll(filepath.Join(dir, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	mustDir("cmd/affentctl")
	mustDir("internal/projectcontext")
	mustDir(".git")
	writeFile(t, dir, "cmd/affentctl/main.go", `package main

type Config struct{}

func main() {}
`)
	writeFile(t, dir, "internal/projectcontext/projectcontext.go", `package projectcontext

type Index struct{}

func Load() {}
`)
	writeFile(t, dir, "internal/projectcontext/projectcontext_test.go", `package projectcontext

func TestIgnoreMe(t *testing.T) {}
`)

	got := Load(dir)
	for _, want := range []string{
		"GO SYMBOL HINTS",
		"cmd/affentctl",
		"main.go",
		"Config",
		"main",
		"internal/projectcontext",
		"projectcontext.go",
		"Index",
		"Load",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("go symbol hints missing %q:\n%s", want, got)
		}
	}
	for _, forbidden := range []string{"TestIgnoreMe", ".git"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("go symbol hints should skip %q:\n%s", forbidden, got)
		}
	}
}

func TestLoad_NonexistentDir(t *testing.T) {
	if got := Load(filepath.Join(t.TempDir(), "does-not-exist")); got != "" {
		t.Fatalf("expected empty for nonexistent dir, got %q", got)
	}
}

func TestTruncateFile_UTF8Safe(t *testing.T) {
	s := strings.Repeat("🔧", 100) + strings.Repeat("привет", 50)
	out := truncateFile(s, 50, true)
	if len(out) > 50 {
		t.Fatalf("truncated string exceeds limit: %d > 50", len(out))
	}
	if !utf8.ValidString(out) {
		t.Fatalf("truncated string is not valid UTF-8: %q", out)
	}
	if !strings.HasSuffix(out, "[truncated]") {
		t.Fatalf("missing truncation marker: %q", out[max(0, len(out)-30):])
	}
}
