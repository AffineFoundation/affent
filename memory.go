package affent

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"unicode"
)

// MemoryTarget selects which of the two persistent stores a memory
// tool call operates on.
//
//   - TargetMemory: agent notes — environment, conventions, lessons
//     learned. Workspace-scoped. Sub-bucketed by topic (see Topic).
//   - TargetUser: user profile — name, role, preferences, style.
//     User-scoped (crosses workspaces). Single bucket; Topic ignored.
type MemoryTarget string

const (
	TargetMemory MemoryTarget = "memory"
	TargetUser   MemoryTarget = "user"
)

// MemoryStore is the abstraction the Loop uses to inject persistent
// memory into the system prompt and the abstraction the `memory` tool
// uses to mutate / retrieve that state.
//
// Loop.EnsureSystemPrompt calls Snapshot() once per session; the
// returned text becomes the conversation log's first message. Mid-
// session memory mutations write to disk and surface live state
// through tool responses' Entries field. Snapshot() always reads
// current store state — implementations don't need to cache.
//
// Topic is the second-tier bucket inside TargetMemory: an arbitrary
// string the model picks at write-time ("auth", "deploy", "lessons"…)
// so memory can grow indefinitely without single-file char caps. A
// special topic "core" lands content directly in the always-in-prompt
// digest; everything else is on-demand-retrieved via Search.
// TargetUser ignores Topic — there's one user profile, not many.
//
// FileMemoryStore is the default implementation. Embedders can plug
// in their own (e.g. a Honcho / Mem0 / supermemory adapter) by
// satisfying this interface.
type MemoryStore interface {
	Snapshot() string
	Add(target MemoryTarget, topic, content string) (MemoryResponse, error)
	Replace(target MemoryTarget, topic, oldText, newContent string) (MemoryResponse, error)
	Remove(target MemoryTarget, topic, oldText string) (MemoryResponse, error)
	Search(target MemoryTarget, topic, query string, topK int) (MemoryResponse, error)
}

// MemoryResponse is the memory tool's return shape. Entries holds the
// live state of the bucket the operation targeted, or the current
// state when a capacity / ambiguity error blocked it (so the agent
// can consolidate or refine in the same turn). Matches is populated
// only when a Replace/Remove found multiple non-identical entries.
// Usage is set whenever the response can carry a coherent count.
// Results is set by Search.
type MemoryResponse struct {
	OK      bool                 `json:"ok"`
	Message string               `json:"message,omitempty"`
	Target  MemoryTarget         `json:"target"`
	Topic   string               `json:"topic,omitempty"`
	Entries []string             `json:"entries,omitempty"`
	Matches []string             `json:"matches,omitempty"`
	Results []MemorySearchResult `json:"results,omitempty"`
	Usage   *MemoryUsage         `json:"usage,omitempty"`
}

// MemorySearchResult is one ranked hit returned by Search.
type MemorySearchResult struct {
	Topic   string  `json:"topic"`
	Snippet string  `json:"snippet"`
	Score   float64 `json:"score"`
}

// MemoryUsage carries capacity numbers for a single bucket.
type MemoryUsage struct {
	Percent    int `json:"percent"`
	CharsUsed  int `json:"chars_used"`
	CharsLimit int `json:"chars_limit"`
	EntryCount int `json:"entry_count"`
}

// Default per-bucket character limits.
//
//   - DefaultCoreCharLimit caps core.md (always injected into the
//     system prompt at session start) so the prefix stays cache-
//     friendly. Use for tight, durable facts only.
//   - DefaultTopicCharLimit caps each per-topic file. Topics are
//     NOT auto-injected; the model retrieves them via the search
//     action, so the limit is per-topic and overall memory grows
//     with topic count.
//   - DefaultUserCharLimit caps the user profile (also injected).
const (
	DefaultCoreCharLimit  = 2200 // ~800 tokens, fits a few durable facts
	DefaultTopicCharLimit = 4400 // ~1600 tokens per topic
	DefaultUserCharLimit  = 1375 // ~500 tokens

	// DefaultMemoryCharLimit is the historical name for the cap that
	// today applies to core.md. Kept exported so existing callers
	// (gateway, tests) don't break.
	DefaultMemoryCharLimit = DefaultCoreCharLimit
)

// CoreTopic names the always-injected bucket. Writes with topic=""
// or topic="general" land in topics/general.md (still on-demand);
// topic="core" lands in core.md (always in the prompt).
const CoreTopic = "core"

// DefaultTopic is the bucket used when the model omits the topic
// field. Mirrors how the pre-v2 single-file MEMORY.md behaved: a
// general grab-bag of agent notes.
const DefaultTopic = "general"

// memoryEntryDelim separates entries on disk. Content containing
// the exact sequence is rejected by scanMemoryContent so round-
// tripping stays safe.
const memoryEntryDelim = "\n§\n"

// memoryHeaderRuleWidth is the horizontal-rule width for the snapshot
// block headers.
const memoryHeaderRuleWidth = 46

// FileMemoryStore persists entries as plain Markdown files under a
// per-workspace memory directory:
//
//	<workspaceDir>/.affent/memory/
//	  core.md                  — always injected into the system prompt
//	  topics/general.md        — default bucket
//	  topics/<topic>.md        — model-named buckets, retrieved on demand
//
// Plus a user-scoped, cross-workspace profile at $XDG_CONFIG_HOME/affent/USER.md.
//
// Mutations write through a tempfile + rename. The mutate path takes
// an in-process mutex plus a per-file flock (POSIX advisory lock on
// a "<path>.lock" side-file) so multiple affent processes serialize
// their read-modify-write cycles against the same store. flock is a
// no-op on non-Unix platforms.
type FileMemoryStore struct {
	// MemoryDir is the workspace memory directory. Set automatically
	// by NewFileMemoryStore from workspaceDir; the historical
	// MemoryPath field still works as a back-compat shim
	// (NewFileMemoryStore mirrors it to MemoryDir's general topic).
	MemoryDir string

	// MemoryPath is the LEGACY single-file location. When set and
	// MemoryDir is empty, FileMemoryStore derives MemoryDir as the
	// parent dir + "memory" subdirectory and migrates the file on
	// first access. Kept so embedders that hardcoded MemoryPath keep
	// working.
	MemoryPath string

	UserPath string

	// CoreCharLimit caps core.md (always-injected). Zero falls back
	// to DefaultCoreCharLimit.
	CoreCharLimit int
	// TopicCharLimit caps each topic file. Zero falls back to
	// DefaultTopicCharLimit.
	TopicCharLimit int
	// MemoryCharLimit is the historical knob; when non-zero AND
	// CoreCharLimit is zero, it sets the core cap (preserves old
	// "make the memory cap smaller" semantics).
	MemoryCharLimit int
	UserCharLimit   int

	mu sync.Mutex

	// migrated tracks whether the legacy MEMORY.md → topics/general.md
	// migration has been run for this process. Re-checked under mu.
	migrated bool
}

// NewFileMemoryStore returns a FileMemoryStore wired to the standard
// workspace + user paths. An empty workspaceDir leaves MemoryDir
// empty (the caller can set it directly).
func NewFileMemoryStore(workspaceDir string) *FileMemoryStore {
	s := &FileMemoryStore{
		UserPath: defaultUserMemoryPath(),
	}
	if workspaceDir != "" {
		s.MemoryDir = filepath.Join(workspaceDir, ".affent", "memory")
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
// block: core.md content (always-in-prompt durable facts) + USER.md
// + a one-line index of any other topics (model uses Search to read
// them on demand). Returns "" when nothing exists.
func (s *FileMemoryStore) Snapshot() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.migrateLegacyLocked() // idempotent; ignore errors so a half-broken disk doesn't break session start
	var parts []string
	if block := s.renderCoreLocked(); block != "" {
		parts = append(parts, block)
	}
	// "general" topic is auto-injected alongside core so the default
	// `add` (no explicit topic) keeps the pre-v2 UX: write a fact,
	// see it on the next session. Custom topics fall to the on-demand
	// index below.
	if block := s.renderGeneralLocked(); block != "" {
		parts = append(parts, block)
	}
	if block := s.renderUserLocked(); block != "" {
		parts = append(parts, block)
	}
	if block := s.renderTopicIndexLocked(); block != "" {
		parts = append(parts, block)
	}
	return strings.Join(parts, "\n\n")
}

// Add appends content to a bucket. Topic is the per-target sub-bucket
// for target=memory ("" or "general" → topics/general.md;
// "core" → core.md; anything else → topics/<topic>.md). Ignored for
// target=user. Byte-identical duplicates are accepted as no-op
// success; over-limit additions return OK=false with current Entries.
func (s *FileMemoryStore) Add(target MemoryTarget, topic, content string) (MemoryResponse, error) {
	if err := validateTarget(target); err != nil {
		return MemoryResponse{Target: target, Message: err.Error()}, nil
	}
	topic = normalizeTopic(target, topic)
	content = strings.TrimSpace(content)
	if content == "" {
		return MemoryResponse{Target: target, Topic: topic, Message: "content cannot be empty"}, nil
	}
	if reason := scanMemoryContent(content); reason != "" {
		return MemoryResponse{Target: target, Topic: topic, Message: "blocked: " + reason}, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.migrateLegacyLocked()

	path := s.bucketPathLocked(target, topic)
	if path == "" {
		return MemoryResponse{Target: target, Topic: topic, Message: "target is disabled (no path configured)"}, nil
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
			return s.respondLocked(target, topic, true, "entry already exists (no duplicate added)", entries, nil), nil
		}
	}
	limit := s.limitFor(target, topic)
	newEntries := append(append([]string{}, entries...), content)
	if total := joinedLen(newEntries); limit > 0 && total > limit {
		return s.respondLocked(target, topic, false,
			fmt.Sprintf("at %d/%d chars; adding %d-char entry would exceed the limit. consolidate or remove first",
				joinedLen(entries), limit, len(content)),
			entries, nil), nil
	}
	if err := writeMemoryFile(path, newEntries); err != nil {
		return MemoryResponse{}, err
	}
	return s.respondLocked(target, topic, true, "entry added", newEntries, nil), nil
}

// Replace substitutes newContent for the single entry containing
// oldText as a substring inside the named bucket.
func (s *FileMemoryStore) Replace(target MemoryTarget, topic, oldText, newContent string) (MemoryResponse, error) {
	if err := validateTarget(target); err != nil {
		return MemoryResponse{Target: target, Message: err.Error()}, nil
	}
	topic = normalizeTopic(target, topic)
	oldText = strings.TrimSpace(oldText)
	newContent = strings.TrimSpace(newContent)
	if oldText == "" {
		return MemoryResponse{Target: target, Topic: topic, Message: "old_text cannot be empty"}, nil
	}
	if newContent == "" {
		return MemoryResponse{Target: target, Topic: topic, Message: "new_content cannot be empty; use the remove action to delete"}, nil
	}
	if reason := scanMemoryContent(newContent); reason != "" {
		return MemoryResponse{Target: target, Topic: topic, Message: "blocked: " + reason}, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.migrateLegacyLocked()

	path := s.bucketPathLocked(target, topic)
	if path == "" {
		return MemoryResponse{Target: target, Topic: topic, Message: "target is disabled (no path configured)"}, nil
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
			return s.respondLocked(target, topic, false,
				fmt.Sprintf("multiple entries matched %q; pass a more specific old_text", oldText),
				entries, matches), nil
		}
		return s.respondLocked(target, topic, false, fmt.Sprintf("no entry matched %q", oldText), entries, nil), nil
	}

	newEntries := append([]string{}, entries...)
	newEntries[idx] = newContent
	limit := s.limitFor(target, topic)
	if total := joinedLen(newEntries); limit > 0 && total > limit {
		return s.respondLocked(target, topic, false,
			fmt.Sprintf("replacement would put bucket at %d/%d chars. shorten the new content or remove other entries first",
				total, limit),
			entries, nil), nil
	}
	if err := writeMemoryFile(path, newEntries); err != nil {
		return MemoryResponse{}, err
	}
	return s.respondLocked(target, topic, true, "entry replaced", newEntries, nil), nil
}

// Remove drops the entry containing oldText as a substring.
func (s *FileMemoryStore) Remove(target MemoryTarget, topic, oldText string) (MemoryResponse, error) {
	if err := validateTarget(target); err != nil {
		return MemoryResponse{Target: target, Message: err.Error()}, nil
	}
	topic = normalizeTopic(target, topic)
	oldText = strings.TrimSpace(oldText)
	if oldText == "" {
		return MemoryResponse{Target: target, Topic: topic, Message: "old_text cannot be empty"}, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.migrateLegacyLocked()

	path := s.bucketPathLocked(target, topic)
	if path == "" {
		return MemoryResponse{Target: target, Topic: topic, Message: "target is disabled (no path configured)"}, nil
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
			return s.respondLocked(target, topic, false,
				fmt.Sprintf("multiple entries matched %q; pass a more specific old_text", oldText),
				entries, matches), nil
		}
		return s.respondLocked(target, topic, false, fmt.Sprintf("no entry matched %q", oldText), entries, nil), nil
	}
	newEntries := append(entries[:idx:idx], entries[idx+1:]...)
	if err := writeMemoryFile(path, newEntries); err != nil {
		return MemoryResponse{}, err
	}
	return s.respondLocked(target, topic, true, "entry removed", newEntries, nil), nil
}

// Search returns up to topK entries matching query across the
// indicated bucket. Topic="" searches all topics in target=memory
// (the typical model use). For target=user, topic is ignored and the
// single bucket is searched.
//
// Scoring is lexical (term-overlap with a small total-occurrence
// boost). Stopwords filter out grammatical filler. Returns entries
// sorted by score, descending.
func (s *FileMemoryStore) Search(target MemoryTarget, topic, query string, topK int) (MemoryResponse, error) {
	if err := validateTarget(target); err != nil {
		return MemoryResponse{Target: target, Message: err.Error()}, nil
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return MemoryResponse{Target: target, Topic: topic, Message: "query cannot be empty"}, nil
	}
	terms := tokenizeMemoryQuery(query)
	if len(terms) == 0 {
		return MemoryResponse{Target: target, Topic: topic, Message: "query had no content terms after stopword filtering"}, nil
	}
	if topK <= 0 {
		topK = 5
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.migrateLegacyLocked()

	type bucket struct {
		topic string
		path  string
	}
	var buckets []bucket
	switch target {
	case TargetUser:
		if s.UserPath != "" {
			buckets = append(buckets, bucket{topic: "user", path: s.UserPath})
		}
	case TargetMemory:
		// Empty topic on Search means "all topics" (the common model
		// use). normalizeTopic would otherwise map "" to "general" —
		// only invoke it when the caller explicitly asked for a
		// specific bucket.
		if strings.TrimSpace(topic) != "" {
			topic = normalizeTopic(target, topic)
			path := s.bucketPathLocked(target, topic)
			if path != "" {
				buckets = append(buckets, bucket{topic: topic, path: path})
			}
		} else {
			if s.MemoryDir == "" {
				return MemoryResponse{Target: target, Message: "memory dir is not configured"}, nil
			}
			if core := filepath.Join(s.MemoryDir, "core.md"); fileExists(core) {
				buckets = append(buckets, bucket{topic: CoreTopic, path: core})
			}
			entries, _ := os.ReadDir(filepath.Join(s.MemoryDir, "topics"))
			for _, e := range entries {
				if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
					continue
				}
				name := strings.TrimSuffix(e.Name(), ".md")
				buckets = append(buckets, bucket{topic: name, path: filepath.Join(s.MemoryDir, "topics", e.Name())})
			}
		}
	}

	var hits []MemorySearchResult
	for _, b := range buckets {
		entries, err := readMemoryFile(b.path)
		if err != nil {
			continue
		}
		for _, e := range entries {
			score := scoreMemoryEntry(e, terms)
			if score <= 0 {
				continue
			}
			hits = append(hits, MemorySearchResult{Topic: b.topic, Snippet: e, Score: score})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].Score > hits[j].Score })
	if len(hits) > topK {
		hits = hits[:topK]
	}
	msg := fmt.Sprintf("%d result(s)", len(hits))
	if len(hits) == 0 {
		msg = "no entries matched"
	}
	return MemoryResponse{
		OK:      true,
		Target:  target,
		Topic:   topic,
		Message: msg,
		Results: hits,
	}, nil
}

// migrateLegacyLocked moves a pre-v2 .affent/MEMORY.md into
// .affent/memory/topics/general.md the first time the new layout
// is touched, then marks the migration done. Idempotent and safe
// when MEMORY.md doesn't exist (the common case for new users).
//
// Caller must hold s.mu.
func (s *FileMemoryStore) migrateLegacyLocked() error {
	if s.migrated {
		return nil
	}
	s.migrated = true
	if s.MemoryDir == "" {
		// Derive from legacy MemoryPath if set: same parent + "memory".
		if s.MemoryPath != "" {
			s.MemoryDir = filepath.Join(filepath.Dir(s.MemoryPath), "memory")
		} else {
			return nil
		}
	}
	if s.MemoryPath == "" {
		// Standard derivation for migration detection.
		s.MemoryPath = filepath.Join(filepath.Dir(s.MemoryDir), "MEMORY.md")
	}
	info, err := os.Stat(s.MemoryPath)
	if err != nil || info.IsDir() {
		return nil // nothing to migrate
	}
	dest := filepath.Join(s.MemoryDir, "topics", "general.md")
	if _, err := os.Stat(dest); err == nil {
		// New layout already populated — leave the legacy file in place,
		// don't clobber.
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	if err := os.Rename(s.MemoryPath, dest); err != nil {
		return err
	}
	return nil
}

// bucketPathLocked returns the on-disk path for (target, topic).
// Caller must hold s.mu.
func (s *FileMemoryStore) bucketPathLocked(target MemoryTarget, topic string) string {
	if target == TargetUser {
		return s.UserPath
	}
	if s.MemoryDir == "" {
		return ""
	}
	if topic == CoreTopic {
		return filepath.Join(s.MemoryDir, "core.md")
	}
	if topic == "" {
		topic = DefaultTopic
	}
	return filepath.Join(s.MemoryDir, "topics", topic+".md")
}

// limitFor returns the char limit for (target, topic).
func (s *FileMemoryStore) limitFor(target MemoryTarget, topic string) int {
	if target == TargetUser {
		if s.UserCharLimit > 0 {
			return s.UserCharLimit
		}
		return DefaultUserCharLimit
	}
	if topic == CoreTopic {
		if s.CoreCharLimit > 0 {
			return s.CoreCharLimit
		}
		if s.MemoryCharLimit > 0 {
			// Back-compat: old MemoryCharLimit knob applied to the
			// always-in-prompt store, which is now core.
			return s.MemoryCharLimit
		}
		return DefaultCoreCharLimit
	}
	if s.TopicCharLimit > 0 {
		return s.TopicCharLimit
	}
	return DefaultTopicCharLimit
}

// renderGeneralLocked renders the default "general" topic content
// directly into the snapshot — back-compat with the pre-v2 MEMORY.md
// experience where `memory action=add target=memory content=fact`
// landed something the model would see on the next session start.
// Custom topics ("auth", "deploy", …) stay on-demand via search.
func (s *FileMemoryStore) renderGeneralLocked() string {
	if s.MemoryDir == "" {
		return ""
	}
	generalPath := filepath.Join(s.MemoryDir, "topics", "general.md")
	entries, err := readMemoryFile(generalPath)
	if err != nil || len(entries) == 0 {
		return ""
	}
	limit := s.limitFor(TargetMemory, DefaultTopic)
	body := strings.Join(entries, memoryEntryDelim)
	pct := pctOf(len(body), limit)
	header := fmt.Sprintf("MEMORY:general (default bucket — your persistent notes) [%d%% — %d/%d chars]", pct, len(body), limit)
	sep := strings.Repeat("=", memoryHeaderRuleWidth)
	return fmt.Sprintf("%s\n%s\n%s\n%s", sep, header, sep, body)
}

func (s *FileMemoryStore) renderCoreLocked() string {
	corePath := filepath.Join(s.MemoryDir, "core.md")
	entries, err := readMemoryFile(corePath)
	if err != nil || len(entries) == 0 {
		return ""
	}
	limit := s.limitFor(TargetMemory, CoreTopic)
	body := strings.Join(entries, memoryEntryDelim)
	pct := pctOf(len(body), limit)
	header := fmt.Sprintf("MEMORY:core (durable facts always in scope) [%d%% — %d/%d chars]", pct, len(body), limit)
	sep := strings.Repeat("=", memoryHeaderRuleWidth)
	return fmt.Sprintf("%s\n%s\n%s\n%s", sep, header, sep, body)
}

func (s *FileMemoryStore) renderUserLocked() string {
	if s.UserPath == "" {
		return ""
	}
	entries, err := readMemoryFile(s.UserPath)
	if err != nil || len(entries) == 0 {
		return ""
	}
	limit := s.limitFor(TargetUser, "")
	body := strings.Join(entries, memoryEntryDelim)
	pct := pctOf(len(body), limit)
	header := fmt.Sprintf("USER PROFILE (what you know about the user) [%d%% — %d/%d chars]", pct, len(body), limit)
	sep := strings.Repeat("=", memoryHeaderRuleWidth)
	return fmt.Sprintf("%s\n%s\n%s\n%s", sep, header, sep, body)
}

// renderTopicIndexLocked produces a one-liner per topic so the model
// knows what buckets exist and uses the `search` action to read them.
// Returns "" when no topic file exists.
func (s *FileMemoryStore) renderTopicIndexLocked() string {
	if s.MemoryDir == "" {
		return ""
	}
	entries, err := os.ReadDir(filepath.Join(s.MemoryDir, "topics"))
	if err != nil {
		return ""
	}
	type topicInfo struct {
		name  string
		count int
	}
	var topics []topicInfo
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".md")
		// "general" is rendered inline in renderGeneralLocked; the
		// on-demand index covers only the custom topics.
		if name == DefaultTopic {
			continue
		}
		es, err := readMemoryFile(filepath.Join(s.MemoryDir, "topics", e.Name()))
		if err != nil || len(es) == 0 {
			continue
		}
		topics = append(topics, topicInfo{name: name, count: len(es)})
	}
	if len(topics) == 0 {
		return ""
	}
	sort.Slice(topics, func(i, j int) bool { return topics[i].name < topics[j].name })
	var b strings.Builder
	sep := strings.Repeat("=", memoryHeaderRuleWidth)
	fmt.Fprintf(&b, "%s\nMEMORY:topics (read with action=search) [%d topic(s)]\n%s\n", sep, len(topics), sep)
	for _, t := range topics {
		fmt.Fprintf(&b, "- %s: %d entry(ies)\n", t.name, t.count)
	}
	return strings.TrimRight(b.String(), "\n")
}

func (s *FileMemoryStore) respondLocked(target MemoryTarget, topic string, ok bool, msg string, entries, matches []string) MemoryResponse {
	limit := s.limitFor(target, topic)
	current := joinedLen(entries)
	return MemoryResponse{
		OK:      ok,
		Target:  target,
		Topic:   topic,
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

// pctOf returns 0..100, clamped.
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

// normalizeTopic returns the canonical topic name. Target=user collapses
// to "" (single bucket). Target=memory: "" → DefaultTopic; "core"
// preserved; anything else is sanitized to plain filename chars.
func normalizeTopic(target MemoryTarget, topic string) string {
	if target == TargetUser {
		return ""
	}
	topic = strings.TrimSpace(topic)
	if topic == "" {
		return DefaultTopic
	}
	if topic == CoreTopic {
		return CoreTopic
	}
	// Allow [a-z0-9_-], normalize uppercase to lower, drop everything else.
	// Keeps filesystem-safety simple without surprising the model — it
	// gets back the normalized name in the response so the next call
	// is consistent.
	var b strings.Builder
	for _, r := range strings.ToLower(topic) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		}
	}
	out := b.String()
	if out == "" {
		return DefaultTopic
	}
	return out
}

func joinedLen(entries []string) int {
	if len(entries) == 0 {
		return 0
	}
	return len(strings.Join(entries, memoryEntryDelim))
}

// findUnique locates the single entry matching oldText as a substring.
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
			e = e[:utf8AlignBackward(e, 80)] + "..."
		}
		previews = append(previews, e)
	}
	return -1, previews
}

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
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out, nil
}

func writeMemoryFile(path string, entries []string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	body := strings.Join(entries, memoryEntryDelim)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(body), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// memoryStopwords is the same tight set session_search uses.
var memoryStopwords = map[string]bool{
	"a": true, "an": true, "and": true, "are": true, "as": true,
	"at": true, "be": true, "but": true, "by": true, "for": true,
	"from": true, "has": true, "have": true, "in": true, "is": true,
	"it": true, "its": true, "no": true, "not": true, "of": true,
	"on": true, "or": true, "that": true, "the": true, "this": true,
	"to": true, "was": true, "we": true, "were": true, "what": true,
	"when": true, "where": true, "which": true, "who": true, "will": true,
	"with": true, "you": true, "your": true,
}

func tokenizeMemoryQuery(s string) []string {
	s = strings.ToLower(s)
	var out []string
	var cur strings.Builder
	flush := func() {
		t := cur.String()
		cur.Reset()
		if len(t) >= 2 && !memoryStopwords[t] {
			out = append(out, t)
		}
	}
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			cur.WriteRune(r)
		} else {
			flush()
		}
	}
	flush()
	return out
}

func scoreMemoryEntry(content string, terms []string) float64 {
	lower := strings.ToLower(content)
	unique := 0
	total := 0
	for _, t := range terms {
		c := strings.Count(lower, t)
		if c > 0 {
			unique++
			total += c
		}
	}
	if unique == 0 {
		return 0
	}
	return float64(unique) + 0.1*float64(total)
}

// scanMemoryContent blocks content that would be unsafe to inject
// into the system prompt or that would break on-disk round-trip.
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

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
