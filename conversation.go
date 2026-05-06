package affent

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Conversation is the in-memory + on-disk record of one session's messages.
// Persistence is JSONL on the host (under the user's home volume), one
// message per line, append-only. Reloads when the runtime reattaches.
type Conversation struct {
	mu       sync.Mutex
	messages []ChatMessage
	path     string // host filesystem path of the JSONL log
}

// NewConversation opens or creates the on-disk log for sessionID under the
// user's home volume. The file lives at <homeDir>/.affine-agents/sessions/
// <sessionID>.jsonl. Existing entries are loaded into memory.
//
// This is the gateway-style constructor: it imposes the standard
// directory layout. CLI / training drivers that want full control of
// the file path should use OpenConversationAt instead.
func NewConversation(homeDir, sessionID string) (*Conversation, error) {
	dir := filepath.Join(homeDir, ".affine-agents", "sessions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir conversations: %w", err)
	}
	return OpenConversationAt(filepath.Join(dir, sessionID+".jsonl"))
}

// OpenConversationAt opens or creates the conversation log at the given
// file path. Parent directories are created. Existing entries are
// loaded into memory.
func OpenConversationAt(path string) (*Conversation, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("mkdir conversations: %w", err)
	}
	c := &Conversation{path: path}
	if err := c.load(); err != nil {
		return nil, err
	}
	return c, nil
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
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		var m ChatMessage
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			continue
		}
		c.messages = append(c.messages, m)
	}
	return sc.Err()
}

// Append adds a message and persists it. Caller passes a fully-formed
// ChatMessage (including any tool_calls / tool_call_id).
func (c *Conversation) Append(m ChatMessage) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.messages = append(c.messages, m)

	f, err := os.OpenFile(c.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetEscapeHTML(false)
	return enc.Encode(m)
}

// Snapshot returns a copy of the message log for sending to the LLM.
func (c *Conversation) Snapshot() []ChatMessage {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]ChatMessage, len(c.messages))
	copy(out, c.messages)
	return out
}

// Replace overwrites the entire message log, on disk and in memory. Used
// by Compactors after summarizing earlier turns; the caller is responsible
// for preserving tool_calls / tool message pairing — Replace will not
// validate or repair it. Atomic via temp-file + rename so a crash mid-
// rewrite leaves the old log intact.
func (c *Conversation) Replace(msgs []ChatMessage) error {
	c.mu.Lock()
	defer c.mu.Unlock()

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
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, c.path); err != nil {
		os.Remove(tmp)
		return err
	}
	c.messages = append(c.messages[:0], msgs...)
	return nil
}
