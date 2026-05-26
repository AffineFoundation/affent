package workspaceignore

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadGitignoreAndIgnored(t *testing.T) {
	dir := t.TempDir()
	body := "" +
		"# generated artifacts\n" +
		"generated/\n" +
		"build/\n" +
		"*.jsonl\n" +
		"**/node_modules/\n" +
		"!keep.jsonl\n"
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := LoadGitignore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if m == nil {
		t.Fatal("expected matcher")
	}
	cases := []struct {
		path  string
		isDir bool
		want  bool
	}{
		{"generated", true, true},
		{"generated/out.txt", false, true},
		{"web/build", true, true},
		{"web/build/output.txt", false, true},
		{"logs/run.jsonl", false, true},
		{"keep.jsonl", false, false},
		{"web/node_modules", true, true},
		{"web/node_modules/pkg/index.js", false, true},
		{"src/main.go", false, false},
	}
	for _, tc := range cases {
		if got := m.Ignored(tc.path, tc.isDir); got != tc.want {
			t.Fatalf("Ignored(%q, %v) = %v, want %v", tc.path, tc.isDir, got, tc.want)
		}
	}
}

func TestLoadGitignoreMissingFileIsNilMatcher(t *testing.T) {
	m, err := LoadGitignore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if m != nil {
		t.Fatalf("expected nil matcher, got %#v", m)
	}
}
