package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestScanLog_CountAndPreview pins the JSONL scan that feeds
// `affentctl sessions`: each line is a message, the FIRST user
// message becomes the preview, malformed lines are skipped (not
// fatal). A regression that mis-counts or picks the wrong line
// would show stale / confusing data in the sessions list.
func TestScanLog_CountAndPreview(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	lines := []string{
		`{"role":"system","content":"be helpful"}`,
		`{"role":"user","content":"first user message"}`,
		`{this is malformed json — must be skipped, not crash}`,
		`{"role":"assistant","content":"reply"}`,
		`{"role":"user","content":"second user, ignored for preview"}`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	count, preview := scanLog(path)
	// Count = total lines scanned (Scanner skips empty lines + the
	// malformed one is still ONE line as far as Scanner is concerned).
	// The function increments on every Scan, so 5 lines = 5.
	if count != 5 {
		t.Errorf("count = %d, want 5", count)
	}
	if !strings.Contains(preview, "first user message") {
		t.Errorf("preview = %q, want 'first user message'", preview)
	}
}

// TestScanLog_MissingFileReturnsZero pins the silent-fail-on-open
// behavior — sessionsCmd renders the listing best-effort and
// shouldn't crash when a file disappears between ReadDir and scan.
func TestScanLog_MissingFileReturnsZero(t *testing.T) {
	count, preview := scanLog("/no/such/path.jsonl")
	if count != 0 || preview != "" {
		t.Errorf("missing file: got (%d, %q), want (0, \"\")", count, preview)
	}
}

// TestOneLine_TrimAndTruncate pins three behaviors of the preview
// formatter: newlines collapse to spaces (so multi-line user input
// stays on one tabular row), trailing whitespace stripped, max-
// length adds an ellipsis at a UTF-8-safe boundary.
func TestOneLine_TrimAndTruncate(t *testing.T) {
	cases := []struct {
		name string
		in   string
		max  int
		want string
	}{
		{"short stays untouched", "hello", 80, "hello"},
		{"newlines become spaces", "line1\nline2\nline3", 80, "line1 line2 line3"},
		{"leading/trailing space stripped", "   hello   ", 80, "hello"},
		{"over max gets ellipsis", "12345678901234567890", 10, "123456789…"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := oneLine(c.in, c.max); got != c.want {
				t.Errorf("oneLine(%q, %d) = %q, want %q", c.in, c.max, got, c.want)
			}
		})
	}
}

// TestMostRecentSession_PicksByMtime pins the --continue resolution.
// Multiple .jsonl files exist; the one with the newest mtime wins.
// Non-jsonl files are ignored.
func TestMostRecentSession_PicksByMtime(t *testing.T) {
	dir := t.TempDir()

	// Three session files, plus one decoy non-jsonl.
	mk := func(name string, ageHours int) {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
		when := time.Now().Add(-time.Duration(ageHours) * time.Hour)
		if err := os.Chtimes(p, when, when); err != nil {
			t.Fatal(err)
		}
	}
	mk("old.jsonl", 48)
	mk("middle.jsonl", 24)
	mk("newest.jsonl", 1)
	mk("ignored.txt", 0) // not .jsonl — must be skipped

	got, err := mostRecentSession(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != "newest" {
		t.Errorf("got %q, want newest", got)
	}
}

// TestMostRecentSession_EmptyDir pins the no-sessions case: returns
// ("", nil) without erroring. sessionsCmd / --continue handle empty
// by starting fresh.
func TestMostRecentSession_EmptyDir(t *testing.T) {
	got, err := mostRecentSession(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Errorf("empty dir should return empty sid; got %q", got)
	}
}

// TestMostRecentSession_NonexistentDir pins the os.IsNotExist branch:
// the convDir hasn't been created yet (first run in a workspace),
// return ("", nil) cleanly.
func TestMostRecentSession_NonexistentDir(t *testing.T) {
	got, err := mostRecentSession(filepath.Join(t.TempDir(), "never-created"))
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Errorf("missing dir should return empty sid; got %q", got)
	}
}

// TestOpenTrace_SpecialSpecs pins the two reserved trace specs: ""
// → stderr, "-" → stdout. Both are unbuffered destinations; the
// returned close func must be a no-op (closing stderr/stdout would
// silently break the rest of the program).
func TestOpenTrace_SpecialSpecs(t *testing.T) {
	for _, spec := range []string{"", "-"} {
		w, closer, err := openTrace(spec, false)
		if err != nil {
			t.Fatalf("openTrace(%q) err: %v", spec, err)
		}
		if w == nil {
			t.Fatalf("openTrace(%q) writer is nil", spec)
		}
		// Closer must be safe to call and idempotent.
		if err := closer(); err != nil {
			t.Errorf("openTrace(%q) closer err: %v", spec, err)
		}
	}
}

// TestOpenTrace_FileAppendVsTruncate pins the resume contract:
// fresh sessions truncate the trace file (so re-running on the
// same workspace doesn't mix old + new JSONL); resumed sessions
// append so the trace keeps growing alongside the conv log.
func TestOpenTrace_FileAppendVsTruncate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trace.jsonl")
	if err := os.WriteFile(path, []byte("existing\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Truncate mode (resume=false).
	w, closer, err := openTrace(path, false)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.WriteString(w, "fresh\n")
	_ = closer()
	got, _ := os.ReadFile(path)
	if string(got) != "fresh\n" {
		t.Errorf("truncate mode should overwrite; got %q", got)
	}

	// Append mode (resume=true).
	w, closer, err = openTrace(path, true)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.WriteString(w, "more\n")
	_ = closer()
	got, _ = os.ReadFile(path)
	if string(got) != "fresh\nmore\n" {
		t.Errorf("append mode should preserve prior content; got %q", got)
	}
}

// TestSummarizeArgs_PrefersInformativeKeys pins the chat REPL's
// per-tool preview formatter. command / path / name / id are
// surfaced before generic JSON because they're the most-revealing
// piece of context for shell / read_file / write_file / etc.
func TestSummarizeArgs_PrefersInformativeKeys(t *testing.T) {
	cases := []struct {
		name string
		args map[string]any
		want string
	}{
		{"empty → {}", nil, "{}"},
		{"command wins", map[string]any{"command": "ls -la", "cwd": "/tmp"}, `command="ls -la"`},
		{"path wins when no command", map[string]any{"path": "/etc/hosts"}, `path="/etc/hosts"`},
		{"name wins next", map[string]any{"name": "myfunc"}, `name="myfunc"`},
		{"id wins last", map[string]any{"id": "abc123"}, `id="abc123"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := summarizeArgs(c.args); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

// TestSummarizeArgs_FallsBackToJSON pins the catch-all: when no
// informative key is present, the whole map is marshaled.
func TestSummarizeArgs_FallsBackToJSON(t *testing.T) {
	got := summarizeArgs(map[string]any{"action": "add", "topic": "ops"})
	if !strings.Contains(got, "action") || !strings.Contains(got, "add") {
		t.Errorf("fallback should include all keys; got %q", got)
	}
	if !strings.HasPrefix(got, "{") {
		t.Errorf("fallback should be JSON-shaped; got %q", got)
	}
}

// TestSummarizeArgs_TruncatesLongCommand pins the 80-char cap.
// A typical shell command can be hundreds of chars; the chat REPL
// truncates so the display line stays readable.
func TestSummarizeArgs_TruncatesLongCommand(t *testing.T) {
	long := strings.Repeat("x", 200)
	got := summarizeArgs(map[string]any{"command": long})
	if !strings.HasSuffix(got, `..."`) {
		t.Errorf("long command should end with truncation marker + closing quote; got %q", got)
	}
	if len(got) > 95 {
		t.Errorf("truncated output should be ~85 chars max; got %d", len(got))
	}
}
