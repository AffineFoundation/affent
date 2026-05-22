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
