package affent

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

func TestLoadProjectContext_NoneFound(t *testing.T) {
	if got := LoadProjectContext(t.TempDir()); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}

func TestLoadProjectContext_EmptyDirArg(t *testing.T) {
	if got := LoadProjectContext(""); got != "" {
		t.Fatalf("expected empty for empty dir, got %q", got)
	}
}

func TestLoadProjectContext_SingleAgentsMd(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "AGENTS.md", "Use Go 1.22. Tests via `make test`.")

	got := LoadProjectContext(dir)
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

func TestLoadProjectContext_PreservesOrder(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "CONVENTIONS.md", "third")
	writeFile(t, dir, "AGENTS.md", "first")
	writeFile(t, dir, "CLAUDE.md", "second")

	got := LoadProjectContext(dir)
	// AGENTS.md should come before CLAUDE.md should come before CONVENTIONS.md
	posAgents := strings.Index(got, "first")
	posClaude := strings.Index(got, "second")
	posConv := strings.Index(got, "third")
	if posAgents < 0 || posClaude < 0 || posConv < 0 {
		t.Fatalf("missing files in output:\n%s", got)
	}
	if !(posAgents < posClaude && posClaude < posConv) {
		t.Fatalf("expected order AGENTS → CLAUDE → CONVENTIONS, got positions %d %d %d",
			posAgents, posClaude, posConv)
	}
}

func TestLoadProjectContext_SkipsEmptyFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "AGENTS.md", "   \n\n  ")
	writeFile(t, dir, "CONVENTIONS.md", "real content")
	got := LoadProjectContext(dir)
	if strings.Contains(got, "AGENTS.md") {
		t.Fatalf("empty AGENTS.md should be skipped, got:\n%s", got)
	}
	if !strings.Contains(got, "real content") {
		t.Fatalf("CONVENTIONS.md should be included, got:\n%s", got)
	}
}

func TestLoadProjectContext_TruncatesAtBudget(t *testing.T) {
	dir := t.TempDir()
	huge := strings.Repeat("a", MaxProjectContextBytes*2)
	writeFile(t, dir, "AGENTS.md", huge)

	got := LoadProjectContext(dir)
	if len(got) > MaxProjectContextBytes+512 {
		t.Fatalf("output exceeded budget significantly: %d bytes > %d", len(got), MaxProjectContextBytes)
	}
	if !strings.Contains(got, "truncated") {
		t.Fatalf("expected truncation marker:\n%s", got[:200])
	}
}

func TestLoadProjectContext_StopsAtBudget_AcrossFiles(t *testing.T) {
	dir := t.TempDir()
	// AGENTS.md fills the budget so the second file's body has no
	// usable room left (less than the 64-byte minimum). It must be
	// skipped entirely.
	writeFile(t, dir, "AGENTS.md", strings.Repeat("a", MaxProjectContextBytes-30))
	writeFile(t, dir, "CONVENTIONS.md", "should be skipped")

	got := LoadProjectContext(dir)
	if strings.Contains(got, "should be skipped") {
		t.Fatalf("over-budget secondary file should be skipped, got tail:\n%s",
			got[max(0, len(got)-300):])
	}
	if !strings.Contains(got, "AGENTS.md") {
		t.Fatalf("AGENTS.md should still be included")
	}
}

func TestLoadProjectContext_RecognizesAllSupportedFiles(t *testing.T) {
	dir := t.TempDir()
	for _, name := range projectContextFiles {
		writeFile(t, dir, name, "content for "+name)
	}
	got := LoadProjectContext(dir)
	for _, name := range projectContextFiles {
		if !strings.Contains(got, name) {
			t.Fatalf("expected %s in output:\n%s", name, got)
		}
		if !strings.Contains(got, "content for "+name) {
			t.Fatalf("expected body of %s in output", name)
		}
	}
}

func TestLoadProjectContext_NonexistentDir(t *testing.T) {
	if got := LoadProjectContext(filepath.Join(t.TempDir(), "does-not-exist")); got != "" {
		t.Fatalf("expected empty for nonexistent dir, got %q", got)
	}
}

func TestTruncateProjectFile_UTF8Safe(t *testing.T) {
	// 4-byte emoji and 2-byte Cyrillic exercise both UTF-8 lengths.
	// Truncating somewhere inside must not split a rune.
	s := strings.Repeat("🔧", 100) + strings.Repeat("привет", 50)
	out := truncateProjectFile(s, 50)
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
