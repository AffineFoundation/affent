package agent

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNewConversation_RejectsPathInjectionInSessionID pins the
// untrusted-id guard. affentserve passes the request body's
// session_id (or X-Affent-Session-Id header) straight into
// NewConversation; without validation, an attacker setting
// session_id="../../etc/passwd" could place the conversation log
// outside the workspace's sessions/ dir. MkdirTemp in affentserve's
// allocWorkspace rejects ids with slashes, so the affentserve path
// is already safe in that exact spot, but NewConversation is a
// runtime boundary — callers other than affentserve
// could still call it with user-controlled ids. Validate at the
// runtime boundary, defense in depth.
func TestNewConversation_RejectsPathInjectionInSessionID(t *testing.T) {
	home := t.TempDir()
	bad := []string{
		"../escape",
		"foo/bar",
		"..",
		".",
		"id\x00bytes",
		"",
		// ASCII control characters — log injection vectors. Without
		// rejection, "session=victim\nFAKE LOG LINE" gets baked into
		// operator log streams.
		"newline\nattack",
		"carriage\rreturn",
		"tab\there",
		"escape\x1bbomb",
		"del\x7fchar",
	}
	for _, id := range bad {
		c, err := NewConversation(home, id)
		if err == nil {
			t.Errorf("NewConversation(%q) accepted; should have rejected (conv=%v)", id, c)
		}
	}
	// And a perfectly normal id round-trips fine.
	if _, err := NewConversation(home, "session-001_abc"); err != nil {
		t.Errorf("plain id rejected: %v", err)
	}
	// Visible non-ASCII characters stay legal: a client that picks
	// "用户-001" or "user@host" as its session id is well-formed.
	if err := ValidateSessionID("用户-001"); err != nil {
		t.Errorf("Unicode visible id rejected: %v", err)
	}
	if err := ValidateSessionID("user@host"); err != nil {
		t.Errorf("punctuation in visible id rejected: %v", err)
	}
}

// TestOpenConversationAt_CorruptedLineIsLogged covers the load path:
// a malformed JSONL row is skipped (so a single bad line doesn't
// brick session resumption) but emits a log line so the operator
// notices before the next Replace() rewrites the file and drops
// the bad row forever.
func TestOpenConversationAt_CorruptedLineIsLogged(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sess.jsonl")
	body := strings.Join([]string{
		`{"role":"user","content":"hello"}`,
		`{this is not valid json`,
		`{"role":"assistant","content":"hi"}`,
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	var buf bytes.Buffer
	prev := log.Writer()
	prevFlags := log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(prev)
		log.SetFlags(prevFlags)
	})

	c, err := OpenConversationAt(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	msgs := c.Snapshot()
	if got, want := len(msgs), 2; got != want {
		t.Fatalf("loaded %d messages, want %d (corrupted line should be skipped)", got, want)
	}
	if msgs[0].Content != "hello" || msgs[1].Content != "hi" {
		t.Fatalf("messages out of order or wrong content: %+v", msgs)
	}
	if !strings.Contains(buf.String(), "line 2") || !strings.Contains(buf.String(), "corrupted") {
		t.Fatalf("expected corruption log mentioning line 2; got %q", buf.String())
	}
}

func TestOpenConversationAt_OversizedLineIsSkipped(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sess.jsonl")
	body := strings.Join([]string{
		`{"role":"user","content":"before oversized line"}`,
		`{"role":"assistant","content":"` + strings.Repeat("x", maxConversationLineBytes+1) + `"}`,
		`{"role":"assistant","content":"after oversized line"}`,
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	var buf bytes.Buffer
	prev := log.Writer()
	prevFlags := log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(prev)
		log.SetFlags(prevFlags)
	})

	c, err := OpenConversationAt(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	msgs := c.Snapshot()
	if got, want := len(msgs), 2; got != want {
		t.Fatalf("loaded %d messages, want %d with only oversized line skipped", got, want)
	}
	if msgs[0].Content != "before oversized line" || msgs[1].Content != "after oversized line" {
		t.Fatalf("messages out of order or wrong content: %+v", msgs)
	}
	if !strings.Contains(buf.String(), "line 2") || !strings.Contains(buf.String(), "oversized") {
		t.Fatalf("expected oversized log mentioning line 2; got %q", buf.String())
	}
}

// TestConversationAppend_NoGhostMessageOnDiskFailure pins the
// persist-then-remember ordering. Pre-fix, Append wrote to the
// in-memory slice BEFORE attempting the disk write, so a failed
// write left a "ghost" message visible to Snapshot but missing from
// the persisted log — the model would see it for the rest of the
// session, then it'd vanish on resume. The fix writes to disk
// first; if that fails, in-memory stays unchanged.
func TestConversationAppend_NoGhostMessageOnDiskFailure(t *testing.T) {
	dir := t.TempDir()
	// Point Conversation at the dir itself, not a file inside it.
	// OpenFile(dir, O_WRONLY) returns EISDIR — same failure mode as
	// disk-full or permission-denied without needing chmod or syscall
	// magic that varies across CI environments.
	c := &Conversation{path: dir}
	err := c.Append(ChatMessage{Role: "user", Content: "should not be remembered"})
	if err == nil {
		t.Fatalf("Append to a directory path must return an error")
	}
	if got := c.Snapshot(); len(got) != 0 {
		t.Fatalf("Snapshot must be empty after a failed Append; got %d messages: %+v", len(got), got)
	}
}

func TestOpenConversationAtRejectsSymlinkLog(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.jsonl")
	if err := os.WriteFile(outside, []byte(`{"role":"user","content":"outside"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "sess.jsonl")
	if err := os.Symlink(outside, path); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	if _, err := OpenConversationAt(path); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("OpenConversationAt symlink error = %v, want symlink", err)
	}
}

func TestConversationAppendRejectsSymlinkSwappedAfterOpen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sess.jsonl")
	c, err := OpenConversationAt(path)
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.jsonl")
	if err := os.WriteFile(outside, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, path); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	err = c.Append(ChatMessage{Role: "user", Content: "must not follow symlink"})
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("Append symlink error = %v, want symlink", err)
	}
	raw, err := os.ReadFile(outside)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) != 0 {
		t.Fatalf("outside symlink target was written: %q", raw)
	}
}

// TestConversationReplace_AtomicWrite asserts the post-Replace file
// contains exactly the new content. (Crash-safety is hard to test
// without process-level fault injection; the encode + sync + rename
// path is exercised end-to-end here.)
func TestConversationReplace_AtomicWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sess.jsonl")
	c, err := OpenConversationAt(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := c.Append(ChatMessage{Role: "user", Content: "first"}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := c.Replace([]ChatMessage{
		{Role: "user", Content: "replaced-1"},
		{Role: "assistant", Content: "replaced-2"},
	}); err != nil {
		t.Fatalf("replace: %v", err)
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("tmp file leaked: stat err = %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if strings.Count(string(raw), "\n") != 2 {
		t.Fatalf("expected 2 lines on disk, got %q", raw)
	}
	if strings.Contains(string(raw), "first") {
		t.Fatalf("pre-replace content leaked: %q", raw)
	}
}

// TestOpenConversationAt_RepairsCrashMidTurnToolCalls plants the
// exact disk shape a process crash mid-turn leaves behind: an
// assistant message with two tool_calls and only one matching tool
// response on the next line. The next request snapshot would
// otherwise carry that broken pairing to a strict OpenAI-compat
// upstream and get rejected with 400.
//
// load() must synthesize a placeholder tool message for the missing
// call_id, persist the repair so the next process also sees a clean
// file, and leave Snapshot() returning a structurally valid
// sequence.
func TestOpenConversationAt_RepairsCrashMidTurnToolCalls(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sess.jsonl")
	// Plant the inconsistent log directly: assistant tool_calls=[c1, c2],
	// then only c1's response. c2 is missing — the crash window.
	body := `{"role":"system","content":"sys"}
{"role":"user","content":"go"}
{"role":"assistant","content":"","tool_calls":[{"id":"c1","type":"function","function":{"name":"f","arguments":"{}"}},{"id":"c2","type":"function","function":{"name":"g","arguments":"{}"}}]}
{"role":"tool","content":"r1","tool_call_id":"c1","name":"f"}
{"role":"user","content":"next"}
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	c, err := OpenConversationAt(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	snap := c.Snapshot()
	// Expected after repair: system, user, assistant(c1,c2), tool(c1),
	// tool(c2 placeholder), user — six messages.
	if len(snap) != 6 {
		t.Fatalf("snapshot len after repair = %d, want 6: %+v", len(snap), snap)
	}
	// Verify the c2 placeholder sits BETWEEN c1's response and the
	// follow-up user message so the tool window is contiguous.
	if snap[3].Role != "tool" || snap[3].ToolCallID != "c1" {
		t.Errorf("snap[3] should be tool(c1); got %+v", snap[3])
	}
	if snap[4].Role != "tool" || snap[4].ToolCallID != "c2" {
		t.Errorf("snap[4] should be the synthesized tool(c2); got %+v", snap[4])
	}
	if !strings.Contains(snap[4].Content, "tool result missing") {
		t.Errorf("placeholder content should explain the gap; got %q", snap[4].Content)
	}
	if snap[5].Role != "user" || snap[5].Content != "next" {
		t.Errorf("post-window user message should survive intact; got %+v", snap[5])
	}

	// Disk should match memory now — second OpenConversationAt must
	// observe a clean log with no further repair needed.
	c2, err := OpenConversationAt(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(c2.Snapshot()) != 6 {
		t.Errorf("repaired file should reload to 6 messages, not be repaired again; got %d", len(c2.Snapshot()))
	}
}

// TestOpenConversationAt_CleanLogIsNotRewritten pins the no-op path:
// a well-formed JSONL must NOT trigger the repair rewrite. Otherwise
// every load would touch the file even when nothing's wrong, which
// burns sync/rename calls and bumps the mtime in a way operators
// watching retention sweeps would find confusing.
func TestOpenConversationAt_CleanLogIsNotRewritten(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sess.jsonl")
	body := `{"role":"system","content":"sys"}
{"role":"user","content":"hi"}
{"role":"assistant","content":"hello"}
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	beforeMtime := before.ModTime()

	if _, err := OpenConversationAt(path); err != nil {
		t.Fatal(err)
	}

	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(beforeMtime) {
		t.Errorf("clean log should not be rewritten on load; mtime changed from %v to %v", beforeMtime, after.ModTime())
	}
}
