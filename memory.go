package affent

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// MemoryTarget selects which of the two persistent stores a memory
// tool call operates on.
//
//   - TargetMemory: agent notes — environment, conventions, lessons
//     learned. Workspace-scoped by default.
//   - TargetUser: user profile — name, role, preferences, style.
//     User-scoped by default (crosses workspaces).
type MemoryTarget string

const (
	TargetMemory MemoryTarget = "memory"
	TargetUser   MemoryTarget = "user"
)

// MemoryStore is the abstraction the Loop uses to inject persistent
// memory into the system prompt and the abstraction the `memory` tool
// uses to mutate that state.
//
// Loop.EnsureSystemPrompt calls Snapshot() once per session; the
// returned text becomes the conversation log's first message (a
// system message), which then stays in the log for the rest of the
// session. Mid-session memory mutations write to disk and surface
// live state through tool responses' Entries field. Snapshot()
// always reads current store state — implementations don't need to
// cache.
//
// FileMemoryStore is the default implementation. Embedders can plug
// in their own (e.g. a Honcho / Mem0 / supermemory adapter) by
// satisfying this interface.
type MemoryStore interface {
	Snapshot() string
	Add(target MemoryTarget, content string) (MemoryResponse, error)
	Replace(target MemoryTarget, oldText, newContent string) (MemoryResponse, error)
	Remove(target MemoryTarget, oldText string) (MemoryResponse, error)
}

// MemoryResponse is the memory tool's return shape. Entries holds the
// live state after the operation, or the current state when a
// capacity / ambiguity error blocked it (so the agent can consolidate
// or refine in the same turn). Matches is populated only when a
// Replace/Remove found multiple non-identical entries. Usage is set
// whenever the response can carry a coherent count; validation
// errors that occur before the target is resolved leave it nil.
type MemoryResponse struct {
	OK      bool         `json:"ok"`
	Message string       `json:"message,omitempty"`
	Target  MemoryTarget `json:"target"`
	Entries []string     `json:"entries,omitempty"`
	Matches []string     `json:"matches,omitempty"`
	Usage   *MemoryUsage `json:"usage,omitempty"`
}

// MemoryUsage carries capacity numbers for a single target.
type MemoryUsage struct {
	Percent    int `json:"percent"`
	CharsUsed  int `json:"chars_used"`
	CharsLimit int `json:"chars_limit"`
	EntryCount int `json:"entry_count"`
}

// Default per-target character limits. Bound the system-prompt prefix
// growth — each store's content is injected at session start. Both
// can be overridden on FileMemoryStore.
const (
	DefaultMemoryCharLimit = 2200 // ~800 tokens
	DefaultUserCharLimit   = 1375 // ~500 tokens
)

// memoryEntryDelim separates entries on disk and in the rendered
// snapshot. Content containing the exact sequence is rejected by
// scanMemoryContent so round-tripping stays safe.
const memoryEntryDelim = "\n§\n"

// memoryHeaderRuleWidth is the horizontal-rule width for the snapshot
// block headers (visual separators rendered into the system prompt).
const memoryHeaderRuleWidth = 46

// FileMemoryStore persists entries as plain Markdown files. The two
// targets have independent paths and char limits.
//
// Mutations write through a tempfile + rename. The mutate path takes
// an in-process mutex plus a per-file flock (POSIX advisory lock on a
// "<path>.lock" side-file) so multiple affent processes serialize
// their read-modify-write cycles against the same store. flock is a
// no-op on non-Unix platforms — Windows callers running multiple
// affent processes against the same file should serialize externally.
type FileMemoryStore struct {
	MemoryPath      string
	UserPath        string
	MemoryCharLimit int
	UserCharLimit   int

	mu sync.Mutex
}

// NewFileMemoryStore returns a FileMemoryStore with:
//
//   - MemoryPath = <workspaceDir>/.affent/MEMORY.md
//   - UserPath   = $XDG_CONFIG_HOME/affent/USER.md, falling back to
//     $HOME/.config/affent/USER.md, or "" when neither resolves
//   - Char limits = DefaultMemoryCharLimit / DefaultUserCharLimit
//
// An empty workspaceDir leaves MemoryPath empty for the caller to
// set. An empty path on either target disables that target.
func NewFileMemoryStore(workspaceDir string) *FileMemoryStore {
	s := &FileMemoryStore{
		MemoryCharLimit: DefaultMemoryCharLimit,
		UserCharLimit:   DefaultUserCharLimit,
		UserPath:        defaultUserMemoryPath(),
	}
	if workspaceDir != "" {
		s.MemoryPath = filepath.Join(workspaceDir, ".affent", "MEMORY.md")
	}
	return s
}

func defaultUserMemoryPath() string {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "affent", "USER.md")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".config", "affent", "USER.md")
}

// Snapshot reads current disk state and renders the system-prompt
// block. Returns "" when both targets are empty.
func (s *FileMemoryStore) Snapshot() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var parts []string
	if block := s.renderBlockLocked(TargetMemory); block != "" {
		parts = append(parts, block)
	}
	if block := s.renderBlockLocked(TargetUser); block != "" {
		parts = append(parts, block)
	}
	return strings.Join(parts, "\n\n")
}

// Add appends content. Byte-identical duplicates are accepted as
// no-op success. Over-limit additions return OK=false with the
// current Entries.
func (s *FileMemoryStore) Add(target MemoryTarget, content string) (MemoryResponse, error) {
	if err := validateTarget(target); err != nil {
		return MemoryResponse{Target: target, Message: err.Error()}, nil
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return MemoryResponse{Target: target, Message: "content cannot be empty"}, nil
	}
	if reason := scanMemoryContent(content); reason != "" {
		return MemoryResponse{Target: target, Message: "blocked: " + reason}, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	path := s.pathFor(target)
	if path == "" {
		return MemoryResponse{Target: target, Message: "target is disabled (no path configured)"}, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return MemoryResponse{}, err
	}
	release, lerr := lockFile(path)
	if lerr != nil {
		return MemoryResponse{}, lerr
	}
	defer release()

	entries, err := readMemoryFile(path)
	if err != nil {
		return MemoryResponse{}, err
	}
	for _, e := range entries {
		if e == content {
			return s.respondLocked(target, true, "entry already exists (no duplicate added)", entries, nil), nil
		}
	}
	limit := s.limitFor(target)
	newEntries := append(append([]string{}, entries...), content)
	if total := joinedLen(newEntries); limit > 0 && total > limit {
		return s.respondLocked(target, false,
			fmt.Sprintf("at %d/%d chars; adding %d-char entry would exceed the limit. consolidate or remove first",
				joinedLen(entries), limit, len(content)),
			entries, nil), nil
	}
	if err := writeMemoryFile(path, newEntries); err != nil {
		return MemoryResponse{}, err
	}
	return s.respondLocked(target, true, "entry added", newEntries, nil), nil
}

// Replace substitutes newContent for the single entry containing
// oldText as a substring. Multiple non-identical matches return
// OK=false with Matches previews.
func (s *FileMemoryStore) Replace(target MemoryTarget, oldText, newContent string) (MemoryResponse, error) {
	if err := validateTarget(target); err != nil {
		return MemoryResponse{Target: target, Message: err.Error()}, nil
	}
	oldText = strings.TrimSpace(oldText)
	newContent = strings.TrimSpace(newContent)
	if oldText == "" {
		return MemoryResponse{Target: target, Message: "old_text cannot be empty"}, nil
	}
	if newContent == "" {
		return MemoryResponse{Target: target, Message: "new_content cannot be empty; use the remove action to delete"}, nil
	}
	if reason := scanMemoryContent(newContent); reason != "" {
		return MemoryResponse{Target: target, Message: "blocked: " + reason}, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	path := s.pathFor(target)
	if path == "" {
		return MemoryResponse{Target: target, Message: "target is disabled (no path configured)"}, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return MemoryResponse{}, err
	}
	release, lerr := lockFile(path)
	if lerr != nil {
		return MemoryResponse{}, lerr
	}
	defer release()

	entries, err := readMemoryFile(path)
	if err != nil {
		return MemoryResponse{}, err
	}
	idx, matches := findUnique(entries, oldText)
	if idx < 0 {
		if len(matches) > 1 {
			return s.respondLocked(target, false,
				fmt.Sprintf("multiple entries matched %q; pass a more specific old_text", oldText),
				entries, matches), nil
		}
		return s.respondLocked(target, false, fmt.Sprintf("no entry matched %q", oldText), entries, nil), nil
	}

	newEntries := append([]string{}, entries...)
	newEntries[idx] = newContent
	limit := s.limitFor(target)
	if total := joinedLen(newEntries); limit > 0 && total > limit {
		return s.respondLocked(target, false,
			fmt.Sprintf("replacement would put memory at %d/%d chars. shorten the new content or remove other entries first",
				total, limit),
			entries, nil), nil
	}
	if err := writeMemoryFile(path, newEntries); err != nil {
		return MemoryResponse{}, err
	}
	return s.respondLocked(target, true, "entry replaced", newEntries, nil), nil
}

// Remove drops the entry containing oldText as a substring. Same
// match semantics as Replace.
func (s *FileMemoryStore) Remove(target MemoryTarget, oldText string) (MemoryResponse, error) {
	if err := validateTarget(target); err != nil {
		return MemoryResponse{Target: target, Message: err.Error()}, nil
	}
	oldText = strings.TrimSpace(oldText)
	if oldText == "" {
		return MemoryResponse{Target: target, Message: "old_text cannot be empty"}, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	path := s.pathFor(target)
	if path == "" {
		return MemoryResponse{Target: target, Message: "target is disabled (no path configured)"}, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return MemoryResponse{}, err
	}
	release, lerr := lockFile(path)
	if lerr != nil {
		return MemoryResponse{}, lerr
	}
	defer release()

	entries, err := readMemoryFile(path)
	if err != nil {
		return MemoryResponse{}, err
	}
	idx, matches := findUnique(entries, oldText)
	if idx < 0 {
		if len(matches) > 1 {
			return s.respondLocked(target, false,
				fmt.Sprintf("multiple entries matched %q; pass a more specific old_text", oldText),
				entries, matches), nil
		}
		return s.respondLocked(target, false, fmt.Sprintf("no entry matched %q", oldText), entries, nil), nil
	}
	newEntries := append(entries[:idx:idx], entries[idx+1:]...)
	if err := writeMemoryFile(path, newEntries); err != nil {
		return MemoryResponse{}, err
	}
	return s.respondLocked(target, true, "entry removed", newEntries, nil), nil
}

// pathFor / limitFor / renderBlockLocked / respondLocked must be called
// with s.mu held.

func (s *FileMemoryStore) pathFor(target MemoryTarget) string {
	if target == TargetUser {
		return s.UserPath
	}
	return s.MemoryPath
}

func (s *FileMemoryStore) limitFor(target MemoryTarget) int {
	if target == TargetUser {
		if s.UserCharLimit > 0 {
			return s.UserCharLimit
		}
		return DefaultUserCharLimit
	}
	if s.MemoryCharLimit > 0 {
		return s.MemoryCharLimit
	}
	return DefaultMemoryCharLimit
}

func (s *FileMemoryStore) renderBlockLocked(target MemoryTarget) string {
	path := s.pathFor(target)
	if path == "" {
		return ""
	}
	entries, err := readMemoryFile(path)
	if err != nil || len(entries) == 0 {
		return ""
	}
	limit := s.limitFor(target)
	body := strings.Join(entries, memoryEntryDelim)
	pct := pctOf(len(body), limit)
	var header string
	if target == TargetUser {
		header = fmt.Sprintf("USER PROFILE (what you know about the user) [%d%% — %d/%d chars]", pct, len(body), limit)
	} else {
		header = fmt.Sprintf("MEMORY (your persistent notes across sessions) [%d%% — %d/%d chars]", pct, len(body), limit)
	}
	sep := strings.Repeat("=", memoryHeaderRuleWidth)
	return fmt.Sprintf("%s\n%s\n%s\n%s", sep, header, sep, body)
}

func (s *FileMemoryStore) respondLocked(target MemoryTarget, ok bool, msg string, entries, matches []string) MemoryResponse {
	limit := s.limitFor(target)
	current := joinedLen(entries)
	return MemoryResponse{
		OK:      ok,
		Target:  target,
		Message: msg,
		Entries: entries,
		Matches: matches,
		Usage: &MemoryUsage{
			Percent:    pctOf(current, limit),
			CharsUsed:  current,
			CharsLimit: limit,
			EntryCount: len(entries),
		},
	}
}

// pctOf returns 0..100, clamped, dividing safely when limit is 0.
func pctOf(used, limit int) int {
	if limit <= 0 {
		return 0
	}
	p := (used * 100) / limit
	if p > 100 {
		return 100
	}
	return p
}

// --- helpers ---

func validateTarget(t MemoryTarget) error {
	if t != TargetMemory && t != TargetUser {
		return fmt.Errorf("invalid memory target %q; expected %q or %q", t, TargetMemory, TargetUser)
	}
	return nil
}

func joinedLen(entries []string) int {
	if len(entries) == 0 {
		return 0
	}
	return len(strings.Join(entries, memoryEntryDelim))
}

// findUnique locates the single entry matching oldText as a
// substring. Returns (idx>=0, nil) on unique match, (-1, previews)
// on multi-match, (-1, nil) on no match. Byte-identical duplicates
// count as unique and resolve to the first index.
func findUnique(entries []string, oldText string) (int, []string) {
	var hits []int
	for i, e := range entries {
		if strings.Contains(e, oldText) {
			hits = append(hits, i)
		}
	}
	if len(hits) == 0 {
		return -1, nil
	}
	if len(hits) == 1 {
		return hits[0], nil
	}
	// Byte-identical duplicates resolve to the first hit.
	seen := map[string]bool{}
	for _, i := range hits {
		seen[entries[i]] = true
	}
	if len(seen) == 1 {
		return hits[0], nil
	}
	previews := make([]string, 0, len(hits))
	for _, i := range hits {
		e := entries[i]
		if len(e) > 80 {
			e = e[:80] + "..."
		}
		previews = append(previews, e)
	}
	return -1, previews
}

// readMemoryFile splits the on-disk file by memoryEntryDelim. A
// missing file returns (nil, nil). Empty entries are filtered.
func readMemoryFile(path string) ([]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	text := strings.TrimSpace(string(raw))
	if text == "" {
		return nil, nil
	}
	parts := strings.Split(text, memoryEntryDelim)
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out, nil
}

// writeMemoryFile writes entries via tempfile + rename. Parent dirs
// are created. An empty slice writes an empty file (the next read
// returns nil entries).
func writeMemoryFile(path string, entries []string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	body := strings.Join(entries, memoryEntryDelim)
	tmp, err := os.CreateTemp(filepath.Dir(path), ".mem-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.WriteString(body); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return nil
}

// scanMemoryContent blocks content that would be unsafe to inject
// into the system prompt or that would break on-disk round-trip.
// Rejects:
//
//   - invisible / bidi-override unicode (zero-width, RTL/LTR override)
//   - the entry delimiter sequence
//   - the substring "authorized_keys"
//
// Returns the reason string on block, "" on accept.
func scanMemoryContent(content string) string {
	for _, r := range content {
		switch r {
		case '\u200b', '\u200c', '\u200d', '\u2060', '\ufeff',
			'\u202a', '\u202b', '\u202c', '\u202d', '\u202e':
			return fmt.Sprintf("invisible unicode U+%04X", r)
		}
	}
	if strings.Contains(content, memoryEntryDelim) {
		return "contains the entry delimiter sequence"
	}
	if strings.Contains(strings.ToLower(content), "authorized_keys") {
		return "references authorized_keys"
	}
	return ""
}
