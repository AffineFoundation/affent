package agent

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/affinefoundation/affent/internal/jsonl"
	"github.com/affinefoundation/affent/internal/textutil"
)

const maxConversationLineBytes = jsonl.DefaultMaxRecordBytes

const missingToolResultOnResume = `(tool result missing on resume; process likely crashed mid-turn)
Failure: kind=resume_missing_tool_result
Next: do not assume the tool succeeded; continue from available context and rerun the missing tool only if its result is still essential and safe to repeat.`

const (
	duplicateToolResultRecoveryHint  = "use the first matching tool result already in this conversation; do not replay the duplicate unless its evidence is still essential."
	unexpectedToolResultRecoveryHint = "treat this tool output as untrusted recovered context; rerun the tool only if the evidence is still essential and safe to repeat."
)

// Conversation is the in-memory + on-disk record of one session's messages.
// Persistence is JSONL on the host (under the user's home volume), one
// message per line, append-only. Reloads when the runtime reattaches.
type Conversation struct {
	mu          sync.Mutex
	messages    []ChatMessage
	path        string // host filesystem path of the JSONL log
	repairStats ConversationRepairStats
}

// ConversationRepairStats reports structural repairs applied while loading a
// persisted conversation. It is intentionally small: callers use it for trace
// and UI recovery signals, not for replaying the repair itself.
type ConversationRepairStats struct {
	MissingToolResults    int
	DuplicateToolResults  int
	UnexpectedToolResults int
}

func (s ConversationRepairStats) HasAny() bool {
	return s.MissingToolResults > 0 || s.DuplicateToolResults > 0 || s.UnexpectedToolResults > 0
}

func (s ConversationRepairStats) FailureKind() string {
	switch {
	case s.MissingToolResults > 0:
		return "resume_missing_tool_result"
	case s.DuplicateToolResults > 0:
		return "resume_duplicate_tool_result"
	case s.UnexpectedToolResults > 0:
		return "resume_unexpected_tool_result"
	default:
		return ""
	}
}

func (s ConversationRepairStats) RecoveryHint() string {
	switch s.FailureKind() {
	case "resume_missing_tool_result":
		return "do not assume the tool succeeded; continue from available context and rerun the missing tool only if its result is still essential and safe to repeat."
	case "resume_duplicate_tool_result":
		return duplicateToolResultRecoveryHint
	case "resume_unexpected_tool_result":
		return unexpectedToolResultRecoveryHint
	default:
		return ""
	}
}

// ValidateSessionID returns nil iff sessionID is safe to use as a
// single filename component AND safe to embed in operator log lines
// without splitting them. Untrusted callers (HTTP-driven servers
// that accept session_id from clients) MUST call this before joining
// the id into any filesystem path — otherwise "../escape" lands the
// derived file outside its intended root. Used by NewConversation
// and by affentserve's per-session-dir allocator.
//
// Rejected:
//   - path separators ('/', '\\'), null byte, literal "." and ".."
//     (filesystem traversal)
//   - ASCII control characters (< 0x20 or 0x7F): newline, tab, CR,
//     escape, etc. Newline-in-id was the path to log injection
//     ("session_id=victim\nFAKE LOG LINE")
//
// Allowed: any visible character, including Unicode. A client that
// uses "用户-001" or "user@host" as a session id stays valid; only
// the categories above trip the check.
func ValidateSessionID(sessionID string) error {
	if sessionID == "" {
		return errors.New("session id is required")
	}
	leaf := sessionID + ".jsonl"
	if filepath.Base(leaf) != leaf || strings.ContainsAny(sessionID, "/\\\x00") || sessionID == ".." || sessionID == "." {
		return fmt.Errorf("invalid session id %q (must be a plain filename, no path separators)", sessionID)
	}
	if textutil.ContainsASCIIControlBytes(sessionID) {
		return fmt.Errorf("invalid session id %q (contains ASCII control characters)", sessionID)
	}
	return nil
}

// NewConversation opens or creates the on-disk log for sessionID under the
// user's home volume. The file lives at <homeDir>/.affent/sessions/
// <sessionID>.jsonl. Existing entries are loaded into memory.
//
// The .affent/ directory is the same namespace FileMemoryStore writes
// MEMORY.md into, so a home-rooted setup keeps every persistent affent
// artifact under one path. CLI / training drivers that want full
// control of the file path should use OpenConversationAt instead.
//
// sessionID must be a plain filename component: no path separators, no
// "..", no NUL. Untrusted callers (HTTP-driven servers) that pass user-
// controlled ids would otherwise let `../escape` land the log outside
// the sessions/ dir. Validated via filepath.Base round-trip — the
// cheap, OS-portable equivalent of "reject anything that isn't a
// single name".
func NewConversation(homeDir, sessionID string) (*Conversation, error) {
	if err := ValidateSessionID(sessionID); err != nil {
		return nil, err
	}
	leaf := sessionID + ".jsonl"
	dir := filepath.Join(homeDir, ".affent", "sessions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir conversations: %w", err)
	}
	return OpenConversationAt(filepath.Join(dir, leaf))
}

// OpenConversationAt opens or creates the conversation log at the given
// file path. Parent directories are created. Existing entries are
// loaded into memory.
func OpenConversationAt(path string) (*Conversation, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("mkdir conversations: %w", err)
	}
	if err := rejectUnsafeConversationPath(path); err != nil {
		return nil, err
	}
	c := &Conversation{path: path}
	if err := c.load(); err != nil {
		return nil, err
	}
	return c, nil
}

func rejectUnsafeConversationPath(path string) error {
	fi, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if fi.IsDir() {
		return fmt.Errorf("conversation path is a directory: %s", path)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("conversation path must not be a symlink: %s", path)
	}
	return nil
}

func (c *Conversation) load() error {
	f, err := os.Open(c.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()
	reader := bufio.NewReaderSize(f, 64*1024)
	lineNo := 0
	for {
		line, overLimit, err := jsonl.ReadBoundedLine(reader, maxConversationLineBytes)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		lineNo++
		if overLimit {
			log.Printf("affent: conversation %s line %d: skipping oversized JSONL record above %d bytes", c.path, lineNo, maxConversationLineBytes)
			continue
		}
		var m ChatMessage
		if err := json.Unmarshal(line, &m); err != nil {
			// Surface — but don't fail — so an operator notices the
			// corruption before the next Replace() compacts the file
			// and quietly drops the bad line forever. Embedders that
			// want structured handling can wrap log.SetOutput.
			log.Printf("affent: conversation %s line %d: skipping corrupted JSON (%v)", c.path, lineNo, err)
			continue
		}
		c.messages = append(c.messages, m)
	}
	// Post-load repair: if the previous process crashed mid-turn we
	// may have an assistant.tool_calls on disk with no matching tool
	// responses. Strict OpenAI-compat upstreams reject that pairing
	// on the next request, which would brick the session permanently
	// even though it could trivially be patched. Synthesize a
	// placeholder tool result per unmatched call_id and persist the
	// repair so the next resume sees a clean log.
	return c.repairToolCallPairs()
}

// repairToolCallPairs walks the loaded messages and ensures every
// assistant.tool_calls is immediately followed by exactly one
// role=tool message per call_id. Missing tool messages — left over
// from a crash between assistant Append and tool-result Append —
// get a synthetic placeholder so the conversation is structurally
// valid for resume. When the in-memory state changes, the on-disk
// JSONL is atomically rewritten via Replace; otherwise this is a
// no-op.
func (c *Conversation) repairToolCallPairs() error {
	var out []ChatMessage
	var stats ConversationRepairStats
	for i := 0; i < len(c.messages); i++ {
		m := c.messages[i]
		if m.Role == "tool" {
			out = append(out, conversationRepairNote("resume_unexpected_tool_result", unexpectedToolResultRecoveryHint, m))
			stats.UnexpectedToolResults++
			continue
		}
		out = append(out, m)
		if m.Role != "assistant" || len(m.ToolCalls) == 0 {
			continue
		}
		expected := map[string]bool{}
		for _, tc := range m.ToolCalls {
			if tc.ID != "" {
				expected[tc.ID] = true
			}
		}
		// Collect the contiguous tool-response window. Any
		// non-tool message ends it (a well-formed log puts every
		// matching tool message right after the assistant).
		seen := map[string]bool{}
		var repairNotes []ChatMessage
		j := i + 1
		for j < len(c.messages) && c.messages[j].Role == "tool" {
			toolMsg := c.messages[j]
			switch {
			case !expected[toolMsg.ToolCallID]:
				repairNotes = append(repairNotes, conversationRepairNote("resume_unexpected_tool_result", unexpectedToolResultRecoveryHint, toolMsg))
				stats.UnexpectedToolResults++
			case seen[toolMsg.ToolCallID]:
				repairNotes = append(repairNotes, conversationRepairNote("resume_duplicate_tool_result", duplicateToolResultRecoveryHint, toolMsg))
				stats.DuplicateToolResults++
			default:
				seen[toolMsg.ToolCallID] = true
				out = append(out, toolMsg)
			}
			j++
		}
		for _, tc := range m.ToolCalls {
			if tc.ID == "" || seen[tc.ID] {
				continue
			}
			out = append(out, ChatMessage{
				Role:       "tool",
				Content:    missingToolResultOnResume,
				ToolCallID: tc.ID,
				Name:       tc.Function.Name,
			})
			stats.MissingToolResults++
		}
		out = append(out, repairNotes...)
		i = j - 1
	}
	if !stats.HasAny() {
		return nil
	}
	c.repairStats.MissingToolResults += stats.MissingToolResults
	c.repairStats.DuplicateToolResults += stats.DuplicateToolResults
	c.repairStats.UnexpectedToolResults += stats.UnexpectedToolResults
	log.Printf(
		"affent: conversation %s: repaired missing_tool_results=%d duplicate_tool_results=%d unexpected_tool_results=%d from a prior crashed or corrupted turn",
		c.path,
		stats.MissingToolResults,
		stats.DuplicateToolResults,
		stats.UnexpectedToolResults,
	)
	return c.replaceWithoutLock(out)
}

func conversationRepairNote(kind, next string, toolMsg ChatMessage) ChatMessage {
	var b strings.Builder
	fmt.Fprintf(&b, "(tool result repaired on resume; original role=tool message was converted to a user-visible repair note)\n")
	fmt.Fprintf(&b, "Failure: kind=%s\n", kind)
	if next != "" {
		fmt.Fprintf(&b, "Next: %s\n", next)
	}
	if toolMsg.ToolCallID != "" {
		fmt.Fprintf(&b, "ToolCallID: %s\n", toolMsg.ToolCallID)
	}
	if toolMsg.Name != "" {
		fmt.Fprintf(&b, "Tool: %s\n", toolMsg.Name)
	}
	if preview := textutil.Preview(toolMsg.Content, 800); preview != "" {
		fmt.Fprintf(&b, "RecoveredPreview: %s", preview)
	}
	return ChatMessage{Role: "user", Content: strings.TrimSpace(b.String())}
}

func (c *Conversation) RepairStats() ConversationRepairStats {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.repairStats
}

// replaceWithoutLock is Replace's body without c.mu acquisition.
// load() runs at construction time before any caller has a handle
// to the Conversation, so the mutex isn't meaningful there; calling
// Replace directly would re-enter the lock and (more importantly)
// hide the load-only context from a reader skimming the file.
func (c *Conversation) replaceWithoutLock(msgs []ChatMessage) error {
	if err := rejectUnsafeConversationPath(c.path); err != nil {
		return err
	}
	tmp := c.path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	enc.SetEscapeHTML(false)
	for _, m := range msgs {
		if err := enc.Encode(m); err != nil {
			f.Close()
			os.Remove(tmp)
			return err
		}
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, c.path); err != nil {
		os.Remove(tmp)
		return err
	}
	if d, derr := os.Open(filepath.Dir(c.path)); derr == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	c.messages = append(c.messages[:0], msgs...)
	return nil
}

// Append adds a message and persists it. Caller passes a fully-formed
// ChatMessage (including any tool_calls / tool_call_id).
//
// Persist-then-remember ordering matters: if we appended to the
// in-memory slice first and the disk write then failed, the next
// Snapshot would feed the model a message that disappears the moment
// the process restarts. Reversing the order keeps memory and disk in
// lockstep — a failed Append leaves both empty of m, so the caller's
// error path doesn't have a hidden ghost message to clean up.
func (c *Conversation) Append(m ChatMessage) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := rejectUnsafeConversationPath(c.path); err != nil {
		return err
	}
	f, err := os.OpenFile(c.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(m); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	c.messages = append(c.messages, m)
	return nil
}

// Snapshot returns a copy of the message log for sending to the LLM.
func (c *Conversation) Snapshot() []ChatMessage {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]ChatMessage, len(c.messages))
	copy(out, c.messages)
	return out
}

// PruneTransientContext removes runtime-injected context from prior turns.
// These messages are persisted while a turn is active so crashes remain
// inspectable, but they are not durable user/assistant history.
func (c *Conversation) PruneTransientContext() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.messages) == 0 {
		return nil
	}
	out := make([]ChatMessage, 0, len(c.messages))
	changed := false
	for _, msg := range c.messages {
		if msg.TransientContext {
			changed = true
			continue
		}
		out = append(out, msg)
	}
	if !changed {
		return nil
	}
	return c.replaceWithoutLock(out)
}

// Replace overwrites the entire message log, on disk and in memory. Used
// by Compactors after summarizing earlier turns; the caller is responsible
// for preserving tool_calls / tool message pairing — Replace will not
// validate or repair it. Atomic via temp-file + fsync + rename + fsync(dir)
// so a crash mid-rewrite leaves either the old log fully intact or the
// new log fully durable, never a half-written intermediate.
func (c *Conversation) Replace(msgs []ChatMessage) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.replaceWithoutLock(msgs)
}
