package affent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTestConv(t *testing.T) *Conversation {
	t.Helper()
	path := filepath.Join(t.TempDir(), "session.jsonl")
	c, err := OpenConversationAt(path)
	if err != nil {
		t.Fatalf("open conversation: %v", err)
	}
	return c
}

func TestEnsureSystemPrompt_EmptyConv_NoMemory(t *testing.T) {
	conv := newTestConv(t)
	l := &Loop{Conv: conv}
	if err := l.EnsureSystemPrompt("custom prompt"); err != nil {
		t.Fatal(err)
	}
	msgs := conv.Snapshot()
	if len(msgs) != 1 {
		t.Fatalf("expected exactly 1 system message, got %d", len(msgs))
	}
	if msgs[0].Role != "system" || msgs[0].Content != "custom prompt" {
		t.Fatalf("system message wrong: %+v", msgs[0])
	}
}

func TestEnsureSystemPrompt_EmptyConv_WithMemory(t *testing.T) {
	conv := newTestConv(t)
	mem := newTestStore(t)
	if _, err := mem.Add(TargetMemory, "User uses Go 1.22 + sqlc"); err != nil {
		t.Fatal(err)
	}
	l := &Loop{Conv: conv, Memory: mem}
	if err := l.EnsureSystemPrompt("base prompt"); err != nil {
		t.Fatal(err)
	}
	msgs := conv.Snapshot()
	if len(msgs) != 1 {
		t.Fatalf("expected exactly 1 system message, got %d", len(msgs))
	}
	c := msgs[0].Content
	if !strings.HasPrefix(c, "base prompt") {
		t.Fatalf("system message should start with base prompt: %q", c)
	}
	if !strings.Contains(c, "User uses Go 1.22") {
		t.Fatalf("system message should contain memory entry: %q", c)
	}
	if !strings.Contains(c, "MEMORY") {
		t.Fatalf("system message should contain memory header: %q", c)
	}
}

func TestEnsureSystemPrompt_ResumedConv_NoMemory_Untouched(t *testing.T) {
	conv := newTestConv(t)
	if err := conv.Append(ChatMessage{Role: "system", Content: "original prompt"}); err != nil {
		t.Fatal(err)
	}
	if err := conv.Append(ChatMessage{Role: "user", Content: "hi"}); err != nil {
		t.Fatal(err)
	}
	l := &Loop{Conv: conv}
	if err := l.EnsureSystemPrompt("new prompt that should NOT be applied"); err != nil {
		t.Fatal(err)
	}
	msgs := conv.Snapshot()
	if len(msgs) != 2 {
		t.Fatalf("resumed conv must not gain a message, got %d", len(msgs))
	}
	if msgs[0].Content != "original prompt" {
		t.Fatalf("resumed conv without memory must preserve system msg, got %q", msgs[0].Content)
	}
}

func TestEnsureSystemPrompt_ResumedConv_WithMemory_Rewritten(t *testing.T) {
	conv := newTestConv(t)
	if err := conv.Append(ChatMessage{Role: "system", Content: "old base + old memory block"}); err != nil {
		t.Fatal(err)
	}
	if err := conv.Append(ChatMessage{Role: "user", Content: "hi"}); err != nil {
		t.Fatal(err)
	}
	if err := conv.Append(ChatMessage{Role: "assistant", Content: "hello"}); err != nil {
		t.Fatal(err)
	}

	mem := newTestStore(t)
	if _, err := mem.Add(TargetMemory, "Fresh fact for this session"); err != nil {
		t.Fatal(err)
	}
	l := &Loop{Conv: conv, Memory: mem}
	if err := l.EnsureSystemPrompt("fresh base"); err != nil {
		t.Fatal(err)
	}

	msgs := conv.Snapshot()
	if len(msgs) != 3 {
		t.Fatalf("message count must be preserved, got %d", len(msgs))
	}
	if msgs[0].Role != "system" {
		t.Fatalf("first message must remain a system message, got role=%q", msgs[0].Role)
	}
	if !strings.HasPrefix(msgs[0].Content, "fresh base") {
		t.Fatalf("system msg must start with new base prompt, got %q", msgs[0].Content)
	}
	if !strings.Contains(msgs[0].Content, "Fresh fact for this session") {
		t.Fatalf("system msg must include current memory entry, got %q", msgs[0].Content)
	}
	if msgs[1].Role != "user" || msgs[1].Content != "hi" {
		t.Fatalf("user message must survive rewrite, got %+v", msgs[1])
	}
	if msgs[2].Role != "assistant" {
		t.Fatalf("assistant message must survive rewrite, got %+v", msgs[2])
	}
}

func TestEnsureSystemPrompt_ResumedConv_WithMemory_AlreadyEqual_NoOp(t *testing.T) {
	conv := newTestConv(t)
	mem := newTestStore(t)
	if _, err := mem.Add(TargetMemory, "stable fact"); err != nil {
		t.Fatal(err)
	}
	// Compute what EnsureSystemPrompt would produce and pre-seed the
	// conversation with exactly that.
	want := "base" + "\n\n" + mem.Snapshot()
	if err := conv.Append(ChatMessage{Role: "system", Content: want}); err != nil {
		t.Fatal(err)
	}
	if err := conv.Append(ChatMessage{Role: "user", Content: "earlier"}); err != nil {
		t.Fatal(err)
	}

	// Capture file mtime to assert no Replace happened.
	path := conv.path
	st1, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	l := &Loop{Conv: conv, Memory: mem}
	if err := l.EnsureSystemPrompt("base"); err != nil {
		t.Fatal(err)
	}
	st2, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !st1.ModTime().Equal(st2.ModTime()) {
		t.Fatalf("expected no-op when system msg already equals composition; file was rewritten")
	}
	msgs := conv.Snapshot()
	if msgs[0].Content != want {
		t.Fatalf("system message changed unexpectedly")
	}
}

func TestEnsureSystemPrompt_ProjectContext_EmptyConv(t *testing.T) {
	conv := newTestConv(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("Project uses Go 1.22"), 0o644); err != nil {
		t.Fatal(err)
	}
	l := &Loop{Conv: conv, ProjectContextDir: dir}
	if err := l.EnsureSystemPrompt("base"); err != nil {
		t.Fatal(err)
	}
	msgs := conv.Snapshot()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	c := msgs[0].Content
	if !strings.HasPrefix(c, "base") {
		t.Fatalf("system msg should start with base: %q", c)
	}
	if !strings.Contains(c, "PROJECT CONTEXT") || !strings.Contains(c, "Project uses Go 1.22") {
		t.Fatalf("project context missing:\n%s", c)
	}
}

func TestEnsureSystemPrompt_ProjectContextPlusMemory_Order(t *testing.T) {
	conv := newTestConv(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("user-authored fact"), 0o644); err != nil {
		t.Fatal(err)
	}
	mem := newTestStore(t)
	if _, err := mem.Add(TargetMemory, "agent-authored fact"); err != nil {
		t.Fatal(err)
	}
	l := &Loop{Conv: conv, ProjectContextDir: dir, Memory: mem}
	if err := l.EnsureSystemPrompt("base"); err != nil {
		t.Fatal(err)
	}
	c := conv.Snapshot()[0].Content

	basePos := strings.Index(c, "base")
	projPos := strings.Index(c, "user-authored fact")
	memPos := strings.Index(c, "agent-authored fact")
	if basePos < 0 || projPos < 0 || memPos < 0 {
		t.Fatalf("missing pieces in composed prompt:\n%s", c)
	}
	if !(basePos < projPos && projPos < memPos) {
		t.Fatalf("expected order base → project-context → memory; got positions %d %d %d",
			basePos, projPos, memPos)
	}
}

func TestEnsureSystemPrompt_ProjectContext_ResumeRewrites(t *testing.T) {
	conv := newTestConv(t)
	if err := conv.Append(ChatMessage{Role: "system", Content: "old prompt without project context"}); err != nil {
		t.Fatal(err)
	}
	if err := conv.Append(ChatMessage{Role: "user", Content: "hi"}); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("freshly added project rule"), 0o644); err != nil {
		t.Fatal(err)
	}
	l := &Loop{Conv: conv, ProjectContextDir: dir}
	if err := l.EnsureSystemPrompt("base"); err != nil {
		t.Fatal(err)
	}
	msgs := conv.Snapshot()
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages preserved, got %d", len(msgs))
	}
	if !strings.Contains(msgs[0].Content, "freshly added project rule") {
		t.Fatalf("project context not refreshed on resume:\n%s", msgs[0].Content)
	}
}

func TestEnsureSystemPrompt_ProjectContext_DirEmptyOrMissing_NoOp(t *testing.T) {
	conv := newTestConv(t)
	l := &Loop{Conv: conv, ProjectContextDir: t.TempDir()} // dir exists but no files
	if err := l.EnsureSystemPrompt("base"); err != nil {
		t.Fatal(err)
	}
	if got := conv.Snapshot()[0].Content; got != "base" {
		t.Fatalf("with no project files, system msg should equal base, got %q", got)
	}
}

func TestEnsureSystemPrompt_SnapshotLiveAcrossSessions(t *testing.T) {
	// One store, two sessions: each session's system message reflects
	// store state at that session's start.
	mem := newTestStore(t)
	if _, err := mem.Add(TargetMemory, "session-1 fact"); err != nil {
		t.Fatal(err)
	}

	conv1 := newTestConv(t)
	l1 := &Loop{Conv: conv1, Memory: mem}
	if err := l1.EnsureSystemPrompt("base"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(conv1.Snapshot()[0].Content, "session-1 fact") {
		t.Fatalf("session 1 system msg missing the fact")
	}

	if _, err := mem.Add(TargetMemory, "session-2 fact"); err != nil {
		t.Fatal(err)
	}
	conv2 := newTestConv(t)
	l2 := &Loop{Conv: conv2, Memory: mem}
	if err := l2.EnsureSystemPrompt("base"); err != nil {
		t.Fatal(err)
	}
	sys2 := conv2.Snapshot()[0].Content
	if !strings.Contains(sys2, "session-1 fact") || !strings.Contains(sys2, "session-2 fact") {
		t.Fatalf("session 2 system msg must reflect current store state, got %q", sys2)
	}

	// And session 1's prompt must NOT have been retroactively changed.
	if strings.Contains(conv1.Snapshot()[0].Content, "session-2 fact") {
		t.Fatalf("session 1 prompt must not see session-2 fact retroactively")
	}
}
