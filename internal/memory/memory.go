package memory

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/affinefoundation/affent/internal/textutil"
)

// nowUTC is a package-level seam so tests can pin time. Defaults to
// time.Now().UTC().
var nowUTC = func() time.Time { return time.Now().UTC() }

// splitMemoryEntry extracts the leading timestamp (if any) from an
// on-disk entry. Returns (createdAt, content). createdAt is "" for
// undated entries (legacy or hand-edited).
func splitMemoryEntry(raw string) (string, string) {
	m := memoryTimestampPrefixRE.FindStringSubmatchIndex(raw)
	if m == nil {
		return "", raw
	}
	ts := raw[m[2]:m[3]]
	content := raw[m[1]:]
	return ts, content
}

// stampMemoryEntry prepends a fresh RFC3339-second timestamp.
func stampMemoryEntry(content string) string {
	return "[" + nowUTC().Format(time.RFC3339) + "]\n" + content
}

// entryContent strips the timestamp prefix if present, returning just
// the user-visible body. Used wherever content is matched / displayed
// without metadata.
func entryContent(raw string) string {
	_, c := splitMemoryEntry(raw)
	return c
}

// newestEntryTimestamp returns the lexicographically max timestamp
// across entries (RFC3339 sorts correctly as a string). "" when no
// entry carries one (all-legacy topic).
func newestEntryTimestamp(entries []string) string {
	newest := ""
	for _, e := range entries {
		ts, _ := splitMemoryEntry(e)
		if ts > newest {
			newest = ts
		}
	}
	return newest
}

// recencyHalfLifeDays defines the soft horizon for the recency boost.
// Linear decay from 1.0 today down to recencyFloor at horizon and
// beyond. Picked so a quarter-year-old fact loses ~25% of its weight
// vs a same-relevance fresh fact — enough to break ties, not enough
// to bury still-useful old facts.
const recencyHalfLifeDays = 365

// recencyFloor is the minimum recency factor: ancient entries never
// score below this fraction of their term-overlap value. Keeps long-
// term memory useful — a year-old fact about "user prefers dark mode"
// is still worth surfacing even if everything else is fresh.
const recencyFloor = 0.5

// recencyFactor returns a multiplier in [recencyFloor, 1.0] based on
// how old the entry is. createdAt="" means un-stamped legacy entry —
// these get factor 1.0 (no penalty) so the stamping rollout doesn't
// suddenly demote pre-existing memory.
func recencyFactor(createdAt string) float64 {
	if createdAt == "" {
		return 1.0
	}
	t, err := time.Parse(time.RFC3339, createdAt)
	if err != nil {
		return 1.0
	}
	ageDays := nowUTC().Sub(t).Hours() / 24
	if ageDays <= 0 {
		return 1.0
	}
	if ageDays >= recencyHalfLifeDays {
		return recencyFloor
	}
	return 1.0 - (1.0-recencyFloor)*(ageDays/recencyHalfLifeDays)
}

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
// in their own (an external memory service, a DB-backed adapter,
// etc.) by satisfying this interface.
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
// Results is set by Search. Topics is set by ListTopics.
type MemoryResponse struct {
	OK      bool                 `json:"ok"`
	Message string               `json:"message,omitempty"`
	Target  MemoryTarget         `json:"target"`
	Topic   string               `json:"topic,omitempty"`
	Entries []string             `json:"entries,omitempty"`
	Matches []string             `json:"matches,omitempty"`
	Results []MemorySearchResult `json:"results,omitempty"`
	Topics  []MemoryTopicSummary `json:"topics,omitempty"`
	Usage   *MemoryUsage         `json:"usage,omitempty"`
}

// MemorySearchResult is one ranked hit returned by Search.
type MemorySearchResult struct {
	Topic     string  `json:"topic"`
	Snippet   string  `json:"snippet"`
	Score     float64 `json:"score"`
	CreatedAt string  `json:"created_at,omitempty"` // RFC3339, "" for un-stamped legacy entries
}

// trimSnippet returns at most max bytes of s, ending at a UTF-8 rune
// boundary, appending "..." when truncated. Used so a single long
// entry doesn't dump its full body into the model context — the
// snippet is enough to decide if the model wants to replace/refine
// for the full content.
func trimSnippet(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := textutil.AlignBackward(s, max)
	return s[:cut] + "..."
}

// centerSnippet returns a max-byte window of body centered on the
// first occurrence of any of terms, with leading/trailing ellipsis
// when the window is interior. Falls back to trimSnippet (head-
// truncation) when no term is found in body — this happens when the
// scoring match was on topic name only, not on body content. UTF-8
// boundaries are respected so we never split a multi-byte rune.
//
// Why this matters: with head-truncation, a 1k-byte postmortem
// matching the query at byte 700 returns bytes 0–300, none of which
// contain the matched term — the model sees a snippet that doesn't
// explain why this hit ranked. Standard IR highlighters (Elastic,
// Lucene) center the window on the match for this reason.
func centerSnippet(body string, terms []string, max int) string {
	if len(body) <= max {
		return body
	}
	lower := strings.ToLower(body)
	firstHit := -1
	for _, t := range terms {
		if t == "" {
			continue
		}
		idx := strings.Index(lower, t)
		if idx < 0 {
			continue
		}
		if firstHit < 0 || idx < firstHit {
			firstHit = idx
		}
	}
	if firstHit < 0 {
		return trimSnippet(body, max)
	}
	// Budget: ellipses cost 3 bytes each. Reserve up to 6 bytes so
	// "..." + body + "..." stays within the same envelope callers
	// already expected from trimSnippet (max body + a few marker
	// bytes). When one side touches the entry edge, that side
	// reclaims its 3-byte budget for body content.
	const ellipsis = "..."
	maxBody := max
	if maxBody > 6 {
		maxBody -= 6
	}
	half := maxBody / 2
	start := firstHit - half
	end := start + maxBody
	if start < 0 {
		end += -start
		start = 0
	}
	if end > len(body) {
		start -= end - len(body)
		end = len(body)
	}
	if start < 0 {
		start = 0
	}
	start = textutil.AlignBackward(body, start)
	end = textutil.AlignForward(body, end)
	left := start > 0
	right := end < len(body)
	out := body[start:end]
	if left {
		out = ellipsis + out
	}
	if right {
		out = out + ellipsis
	}
	return out
}

// MemoryTopicSummary is one row in a ListTopics response. NewestAt
// is the RFC3339 timestamp of the most recently stamped entry; "" for
// topics whose entries are all un-stamped legacy content. Lets the
// model / operator prioritize topics by freshness without reading
// each topic body.
type MemoryTopicSummary struct {
	Topic    string `json:"topic"`
	Entries  int    `json:"entries"`
	Chars    int    `json:"chars"`
	NewestAt string `json:"newest_at,omitempty"`
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

	// DefaultMaxTopics caps how many DISTINCT custom topics one
	// memory dir can hold (general counts; core doesn't — it lives
	// outside topics/). The limit exists because:
	//
	//   - Every topic gets one line in the system-prompt topic index
	//     (~60 chars / ~15 tokens). 32 topics ≈ 500 tokens per turn,
	//     paid forever once the model creates them.
	//   - Snapshot() walks topics/ on every session start and reads
	//     each file to compute newest_at. 32 = a few ms; hundreds
	//     starts being noticeable.
	//   - Models left unconstrained tend to spawn topics rather than
	//     consolidate ("new month → new topic"), which is the same
	//     drift pattern overflow-on-char-limit was added to catch.
	//
	// 32 is comfortably above the ~10–15 named categories a real
	// project actually needs (stack, deploy, conventions, incidents,
	// people, lessons, auth, …). Hit it = a signal the model is
	// fragmenting rather than tracking real new categories.
	DefaultMaxTopics = 32
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

// memorySnippetMax caps the per-entry text returned in search hits.
// Long entries (multi-paragraph deployment guides, incident postmortems)
// would otherwise dump their full body into the model context — wasted
// when the model can replay/refine the query for the full content.
const memorySnippetMax = 500

// memoryResponseEntryMax caps each entry echoed in mutation responses.
// The response still carries Usage with the true bucket size, and disk
// state is untouched. This prevents one hand-edited long entry from
// dominating the model context when an add/replace/remove fails and
// respondLocked returns the current bucket to help the model repair.
const memoryResponseEntryMax = 1000

const (
	DefaultSearchTopK   = 5
	MaxSearchTopK       = 20
	MaxSearchQueryBytes = 2048
	MaxSearchQueryTerms = 16

	MaxMemoryFileBytes = 1 << 20

	memoryTopicDirReadBatch = 128
)

// memoryTimestampPrefixRE matches the optional leading "[<RFC3339>]\n"
// stored at the head of each entry. Stamped entries look like:
//
//	[2026-05-21T09:42:11Z]
//	my favorite color is teal
//
// Old entries (pre-stamping rollout, or hand-edited files) lack the
// prefix and are treated as undated — their content is returned as-is
// and score/snippet logic still works.
var memoryTimestampPrefixRE = regexp.MustCompile(`^\[(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z)\]\n`)

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
	// first access. Kept so callers that hardcoded MemoryPath keep
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

	// MaxTopics caps the number of distinct topic files under topics/.
	// Zero falls back to DefaultMaxTopics. Set to a very large number
	// to effectively disable the cap (some benchmark / eval workloads
	// legitimately want hundreds of named scratchpads).
	MaxTopics int

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
	// Topic count cap: only triggers when this Add would CREATE a new
	// topic file. Writes to an existing topic (including general) go
	// through regardless of how many topics already exist — same
	// per-topic char limit catches over-stuffing within one topic
	// anyway. core.md lives outside topics/ so it doesn't count.
	if target == TargetMemory && topic != CoreTopic && !fileExists(path) {
		cap := s.topicCountLimit()
		have := s.countTopicsLocked()
		if have >= cap {
			return s.respondLocked(target, topic, false,
				fmt.Sprintf("at %d/%d topics; creating a new topic %q would push past the limit. Either merge this fact into an existing topic (use action=add with that topic name) or remove an obsolete topic first (action=remove on every entry leaves the topic file gone).",
					have, cap, topic),
				nil, nil), nil
		}
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
	contentKey := normalizedMemoryContentKey(content)
	for _, e := range entries {
		// Compare on content only — a legacy unstamped duplicate of
		// new stamped content shouldn't be double-saved.
		if normalizedMemoryContentKey(entryContent(e)) == contentKey {
			return s.respondLocked(target, topic, true, "entry already exists (no duplicate added)", entries, nil), nil
		}
	}
	stamped := stampMemoryEntry(content)
	limit := s.limitFor(target, topic)
	newEntries := append(append([]string{}, entries...), stamped)
	if total := joinedLen(newEntries); limit > 0 && total > limit {
		return s.respondLocked(target, topic, false,
			fmt.Sprintf("at %d/%d chars; the new %d-char entry would push past the limit. Consolidate existing entries (use replace to merge related ones into one denser entry) or remove obsolete ones, THEN retry this add — don't just delete and lose information.",
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
	// Same orphan-.lock-file prevention as Remove: short-circuit a
	// non-existent topic before lockFile creates the sidecar that
	// nobody's around to clean up.
	if !fileExists(path) {
		return s.respondLocked(target, topic, false,
			fmt.Sprintf("no entry matched %q (topic does not exist)", oldText),
			nil, nil), nil
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
	// Re-stamp on replace so the entry's freshness reflects this
	// update, not the original creation. Helps the model see "I just
	// re-confirmed this fact" vs "this is from 6 months ago".
	newEntries[idx] = stampMemoryEntry(newContent)
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
	// Short-circuit on a non-existent topic BEFORE creating the lock
	// sidecar. lockFile would otherwise create <path>.lock as a
	// side-effect, and since we're about to return "no entry matched"
	// (nothing to remove), that .lock file would never get cleaned
	// up. Repeated misses on non-existent topics would slowly accrete
	// orphan .lock files in topics/. Concurrent creation by another
	// process between fileExists and now is fine — our caller asked
	// to remove an entry that didn't exist at the moment we checked,
	// "no entry matched" is still the right answer.
	if !fileExists(path) {
		return s.respondLocked(target, topic, false,
			fmt.Sprintf("no entry matched %q (topic does not exist)", oldText),
			nil, nil), nil
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
	if len(newEntries) == 0 {
		// Empty topic file is pollution — every operator-visible
		// listing has to skip it, ListTopics filters it out, snapshot
		// composition ignores it. Delete the file (plus its .lock
		// sidecar) so the topic disappears from the directory listing
		// after the last entry is removed.
		_ = os.Remove(path)
		_ = os.Remove(path + ".lock")
	} else if err := writeMemoryFile(path, newEntries); err != nil {
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
	query = NormalizeSearchQuery(query)
	if query == "" {
		return MemoryResponse{Target: target, Topic: topic, Message: "query cannot be empty. Next: retry with 2-6 specific keywords, or list topics before searching."}, nil
	}
	terms := tokenizeMemoryQuery(query)
	if len(terms) == 0 {
		return MemoryResponse{Target: target, Topic: topic, Message: "query had no content terms after stopword filtering. Next: retry with concrete nouns, identifiers, project names, or outcome words."}, nil
	}
	topK = NormalizeSearchTopK(topK)

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
				return MemoryResponse{Target: target, Message: "memory dir is not configured. Next: configure the memory directory or disable the memory tool for this run."}, nil
			}
			if core := filepath.Join(s.MemoryDir, "core.md"); fileExists(core) {
				buckets = append(buckets, bucket{topic: CoreTopic, path: core})
			}
			topicFiles, _ := s.listTopicFilesLocked(s.topicCountLimit())
			for _, tf := range topicFiles {
				buckets = append(buckets, bucket{topic: tf.name, path: tf.path})
			}
		}
	}

	var hits []MemorySearchResult
	var topicSummaries []MemoryTopicSummary
	for _, b := range buckets {
		entries, err := readMemoryFile(b.path)
		if err != nil {
			continue
		}
		if len(entries) > 0 {
			topicSummaries = append(topicSummaries, MemoryTopicSummary{
				Topic:    b.topic,
				Entries:  len(entries),
				Chars:    joinedLen(entries),
				NewestAt: newestEntryTimestamp(entries),
			})
		}
		for _, e := range entries {
			createdAt, body := splitMemoryEntry(e)
			// Score against content AND topic name. Real-rollout
			// finding: a user organizing memory by topic ("incidents",
			// "deploy", "auth") naturally queries with terms that
			// echo the topic ("incident details"), but the topic name
			// doesn't appear inside the entry body. Pre-fix this
			// produced zero results despite an obvious topical match.
			// Mixing the topic name into the scoring corpus surfaces
			// those hits with a small boost. Timestamps are stripped
			// so date digits don't pollute the term overlap.
			score := scoreMemoryEntry(b.topic+" "+body, terms)
			if score <= 0 {
				continue
			}
			// Recency boost: at equal term overlap, fresher facts rank
			// above stale ones. Linear decay from 1.0 (today) down to
			// a floor of 0.5 (1 year+ old). Old entries don't vanish
			// — they just lose ties to newer entries. This is the
			// standard "freshness boost" pattern from classical IR
			// (Elastic / Lucene). Entries without a timestamp (legacy
			// un-stamped) get factor 1.0, not 0.5, so the rollout of
			// stamping doesn't suddenly down-rank everything that was
			// already there.
			score *= recencyFactor(createdAt)
			hits = appendBoundedMemoryHits(hits, MemorySearchResult{
				Topic:     b.topic,
				Snippet:   centerSnippet(body, terms, memorySnippetMax),
				Score:     score,
				CreatedAt: createdAt,
			}, topK)
		}
	}
	sortMemoryHits(hits)
	if len(hits) > topK {
		hits = hits[:topK]
	}
	msg := fmt.Sprintf("%d result(s)", len(hits))
	var topics []MemoryTopicSummary
	if len(hits) == 0 {
		msg = "no entries matched. Next: retry with fewer/different keywords, search a specific topic from topics, or use action=list for full topic discovery."
		topics = topicSummaries
	}
	return MemoryResponse{
		OK:      true,
		Target:  target,
		Topic:   topic,
		Message: msg,
		Results: hits,
		Topics:  topics,
	}, nil
}

func NormalizeSearchTopK(topK int) int {
	if topK <= 0 {
		return DefaultSearchTopK
	}
	if topK > MaxSearchTopK {
		return MaxSearchTopK
	}
	return topK
}

func NormalizeSearchQuery(query string) string {
	query = strings.TrimSpace(query)
	if len(query) <= MaxSearchQueryBytes {
		return query
	}
	cut := textutil.AlignBackward(query, MaxSearchQueryBytes)
	return strings.TrimSpace(query[:cut])
}

func appendBoundedMemoryHits(hits []MemorySearchResult, hit MemorySearchResult, limit int) []MemorySearchResult {
	if limit <= 0 {
		return append(hits, hit)
	}
	hits = append(hits, hit)
	sortMemoryHits(hits)
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits
}

func sortMemoryHits(hits []MemorySearchResult) {
	sort.SliceStable(hits, func(i, j int) bool { return memoryHitLess(hits[i], hits[j]) })
}

func memoryHitLess(a, b MemorySearchResult) bool {
	if a.Score != b.Score {
		return a.Score > b.Score
	}
	if a.CreatedAt != b.CreatedAt {
		return a.CreatedAt > b.CreatedAt
	}
	if a.Topic != b.Topic {
		return a.Topic < b.Topic
	}
	return a.Snippet < b.Snippet
}

// ListTopics enumerates the buckets in target. For target=memory it
// returns core + general + every custom topic, each row with entry
// count and total chars — cheap discovery so the model can decide
// which topic to search without doing N empty searches. For
// target=user there's a single bucket; the response carries its
// usage.
func (s *FileMemoryStore) ListTopics(target MemoryTarget) (MemoryResponse, error) {
	if err := validateTarget(target); err != nil {
		return MemoryResponse{Target: target, Message: err.Error()}, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.migrateLegacyLocked()

	if target == TargetUser {
		entries, _ := readMemoryFile(s.UserPath)
		return MemoryResponse{
			OK:     true,
			Target: target,
			Topics: []MemoryTopicSummary{{
				Topic:    "user",
				Entries:  len(entries),
				Chars:    joinedLen(entries),
				NewestAt: newestEntryTimestamp(entries),
			}},
		}, nil
	}
	if s.MemoryDir == "" {
		return MemoryResponse{Target: target, Message: "memory dir is not configured. Next: configure the memory directory or disable the memory tool for this run."}, nil
	}
	var topics []MemoryTopicSummary
	corePath := filepath.Join(s.MemoryDir, "core.md")
	if entries, _ := readMemoryFile(corePath); len(entries) > 0 {
		topics = append(topics, MemoryTopicSummary{
			Topic:    CoreTopic,
			Entries:  len(entries),
			Chars:    joinedLen(entries),
			NewestAt: newestEntryTimestamp(entries),
		})
	}
	topicFiles, truncated := s.listTopicFilesLocked(s.topicCountLimit())
	for _, tf := range topicFiles {
		entries, _ := readMemoryFile(tf.path)
		if len(entries) == 0 {
			continue
		}
		topics = append(topics, MemoryTopicSummary{
			Topic:    tf.name,
			Entries:  len(entries),
			Chars:    joinedLen(entries),
			NewestAt: newestEntryTimestamp(entries),
		})
	}
	sort.Slice(topics, func(i, j int) bool { return topics[i].Topic < topics[j].Topic })
	msg := ""
	if truncated {
		msg = fmt.Sprintf("showing first %d topic file(s); directory exceeds configured max_topics", s.topicCountLimit())
	}
	return MemoryResponse{OK: true, Target: target, Topics: topics, Message: msg}, nil
}

// Inspect returns one bucket's current entries without mutating the store.
// Entries are content-only previews, matching Add/Replace/Remove responses.
func (s *FileMemoryStore) Inspect(target MemoryTarget, topic string) (MemoryResponse, error) {
	if err := validateTarget(target); err != nil {
		return MemoryResponse{Target: target, Message: err.Error()}, nil
	}
	topic = normalizeTopic(target, topic)
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.migrateLegacyLocked()
	if target == TargetMemory && s.MemoryDir == "" {
		return MemoryResponse{Target: target, Topic: topic, Message: "memory dir is not configured"}, nil
	}
	path := s.bucketPathLocked(target, topic)
	entries, err := readMemoryFile(path)
	if err != nil {
		return MemoryResponse{}, err
	}
	return s.respondLocked(target, topic, true, "bucket inspected", entries, nil), nil
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

// topicCountLimit returns the configured cap on distinct topic files.
func (s *FileMemoryStore) topicCountLimit() int {
	if s.MaxTopics > 0 {
		return s.MaxTopics
	}
	return DefaultMaxTopics
}

type memoryTopicFile struct {
	name string
	path string
}

func (s *FileMemoryStore) listTopicFilesLocked(limit int) ([]memoryTopicFile, bool) {
	if s.MemoryDir == "" {
		return nil, false
	}
	if limit <= 0 {
		limit = s.topicCountLimit()
	}
	topicDir := filepath.Join(s.MemoryDir, "topics")
	info, err := os.Lstat(topicDir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, false
	}
	dir, err := os.Open(topicDir)
	if err != nil {
		return nil, false
	}
	defer dir.Close()

	var files []memoryTopicFile
	truncated := false
	for {
		entries, rerr := dir.ReadDir(memoryTopicDirReadBatch)
		if rerr != nil && !errors.Is(rerr, io.EOF) {
			break
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
				continue
			}
			if len(files) >= limit {
				truncated = true
				break
			}
			files = append(files, memoryTopicFile{
				name: strings.TrimSuffix(e.Name(), ".md"),
				path: filepath.Join(s.MemoryDir, "topics", e.Name()),
			})
		}
		if truncated || errors.Is(rerr, io.EOF) {
			break
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].name < files[j].name })
	return files, truncated
}

// countTopicsLocked returns the current number of .md files under
// topics/. Caller must hold s.mu. core.md doesn't live here so it
// doesn't count toward the cap.
func (s *FileMemoryStore) countTopicsLocked() int {
	if s.MemoryDir == "" {
		return 0
	}
	limit := s.topicCountLimit()
	n := 0
	dir, err := os.Open(filepath.Join(s.MemoryDir, "topics"))
	if err != nil {
		return 0
	}
	defer dir.Close()
	for {
		entries, rerr := dir.ReadDir(memoryTopicDirReadBatch)
		if rerr != nil && !errors.Is(rerr, io.EOF) {
			return n
		}
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
				n++
				if n >= limit {
					return n
				}
			}
		}
		if errors.Is(rerr, io.EOF) {
			return n
		}
	}
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
	body, used, truncated := snapshotBody(entries, limit)
	if body == "" {
		return ""
	}
	header := fmt.Sprintf("MEMORY:general (default bucket — your persistent notes) %s", snapshotUsage(used, limit, len(body), truncated))
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
	body, used, truncated := snapshotBody(entries, limit)
	if body == "" {
		return ""
	}
	header := fmt.Sprintf("MEMORY:core (durable facts always in scope) %s", snapshotUsage(used, limit, len(body), truncated))
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
	body, used, truncated := snapshotBody(entries, limit)
	if body == "" {
		return ""
	}
	header := fmt.Sprintf("USER PROFILE (what you know about the user) %s", snapshotUsage(used, limit, len(body), truncated))
	sep := strings.Repeat("=", memoryHeaderRuleWidth)
	return fmt.Sprintf("%s\n%s\n%s\n%s", sep, header, sep, body)
}

func snapshotBody(entries []string, limit int) (body string, used int, truncated bool) {
	if len(entries) == 0 {
		return "", 0, false
	}
	cleaned := make([]string, 0, len(entries))
	for _, e := range entries {
		content := strings.TrimSpace(entryContent(e))
		if content != "" {
			cleaned = append(cleaned, content)
		}
	}
	body = strings.Join(cleaned, memoryEntryDelim)
	used = len(body)
	if limit > 0 && len(body) > limit {
		cut := textutil.AlignBackward(body, limit)
		body = strings.TrimRight(body[:cut], "\n")
		truncated = true
	}
	return body, used, truncated
}

func snapshotUsage(used, limit, injected int, truncated bool) string {
	if truncated {
		return fmt.Sprintf("[%d%% — %d/%d chars; snapshot injected first %d chars]", pctOf(used, limit), used, limit, injected)
	}
	return fmt.Sprintf("[%d%% — %d/%d chars]", pctOf(used, limit), used, limit)
}

// renderTopicIndexLocked produces a one-liner per topic so the model
// knows what buckets exist and uses the `search` action to read them.
// Returns "" when no topic file exists.
func (s *FileMemoryStore) renderTopicIndexLocked() string {
	if s.MemoryDir == "" {
		return ""
	}
	type topicInfo struct {
		name     string
		count    int
		newestAt string
	}
	var topics []topicInfo
	topicFiles, truncated := s.listTopicFilesLocked(s.topicCountLimit())
	for _, tf := range topicFiles {
		// "general" is rendered inline in renderGeneralLocked; the
		// on-demand index covers only the custom topics.
		if tf.name == DefaultTopic {
			continue
		}
		es, err := readMemoryFile(tf.path)
		if err != nil || len(es) == 0 {
			continue
		}
		topics = append(topics, topicInfo{
			name:     tf.name,
			count:    len(es),
			newestAt: newestEntryTimestamp(es),
		})
	}
	if len(topics) == 0 {
		return ""
	}
	// Order by recency: freshest topics surface first, stale ones
	// sink. The model is scanning this top-down; what was touched
	// today is almost always more interesting than something last
	// written six months ago. Lexicographic-fallback on equal
	// newest_at (and on un-stamped legacy topics) keeps the order
	// stable and predictable.
	sort.SliceStable(topics, func(i, j int) bool {
		if topics[i].newestAt != topics[j].newestAt {
			return topics[i].newestAt > topics[j].newestAt
		}
		return topics[i].name < topics[j].name
	})
	var b strings.Builder
	sep := strings.Repeat("=", memoryHeaderRuleWidth)
	capNote := ""
	if truncated {
		capNote = fmt.Sprintf(", capped at %d topic file(s)", s.topicCountLimit())
	}
	fmt.Fprintf(&b, "%s\nMEMORY:topics (read with action=search) [%d topic(s)%s]\n%s\n", sep, len(topics), capNote, sep)
	for _, t := range topics {
		fresh := ""
		if t.newestAt != "" {
			// Date-only is enough signal for staleness scanning in a
			// system prompt; full RFC3339 just costs tokens.
			if parsed, err := time.Parse(time.RFC3339, t.newestAt); err == nil {
				fresh = ", newest " + parsed.Format("2006-01-02")
			}
		}
		fmt.Fprintf(&b, "- %s: %d entry(ies)%s\n", t.name, t.count, fresh)
	}
	return strings.TrimRight(b.String(), "\n")
}

func (s *FileMemoryStore) respondLocked(target MemoryTarget, topic string, ok bool, msg string, entries, matches []string) MemoryResponse {
	limit := s.limitFor(target, topic)
	current := joinedLen(entries)
	// Strip timestamp prefixes and cap per-entry previews so the
	// model sees useful current state without dumping a whole bucket
	// into the tool response. Freshness data is available separately
	// via Search (MemorySearchResult.CreatedAt).
	cleaned := make([]string, len(entries))
	for i, e := range entries {
		cleaned[i] = trimSnippet(entryContent(e), memoryResponseEntryMax)
	}
	return MemoryResponse{
		OK:      ok,
		Target:  target,
		Topic:   topic,
		Message: msg,
		Entries: cleaned,
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
		return fmt.Errorf("invalid memory target %q; expected %q or %q. Next: retry with target=%s for workspace/project facts or target=%s for stable user preferences", t, TargetMemory, TargetUser, TargetMemory, TargetUser)
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
	// Char limits count user-visible CONTENT, not the stamped
	// on-disk text. Otherwise the 22-byte timestamp prefix on
	// every entry eats into the configured cap and a 30-char
	// "stack" entry suddenly costs ~52 chars against a 50-char
	// limit — invisible cost that broke ports of the pre-stamp
	// overflow contract.
	total := 0
	for i, e := range entries {
		if i > 0 {
			total += len(memoryEntryDelim)
		}
		total += len(entryContent(e))
	}
	return total
}

// findUnique locates the single entry matching oldText as a substring.
// Matches against entry CONTENT — the leading timestamp prefix isn't
// part of what callers thought they were searching, so it's stripped
// before comparison. Previews are content-only for the same reason.
func findUnique(entries []string, oldText string) (int, []string) {
	var hits []int
	for i, e := range entries {
		if strings.Contains(entryContent(e), oldText) {
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
		seen[entryContent(entries[i])] = true
	}
	if len(seen) == 1 {
		return hits[0], nil
	}
	previews := make([]string, 0, len(hits))
	for _, i := range hits {
		e := entryContent(entries[i])
		if len(e) > 80 {
			e = e[:textutil.AlignBackward(e, 80)] + "..."
		}
		previews = append(previews, e)
	}
	return -1, previews
}

func readMemoryFile(path string) ([]string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	if info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, nil
	}
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	raw, err := io.ReadAll(io.LimitReader(f, MaxMemoryFileBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > MaxMemoryFileBytes {
		return nil, fmt.Errorf("memory file %q exceeds %d-byte cap", path, MaxMemoryFileBytes)
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
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	body := strings.Join(entries, memoryEntryDelim)
	if len(body) > MaxMemoryFileBytes {
		return fmt.Errorf("memory file %q would exceed %d-byte cap", path, MaxMemoryFileBytes)
	}
	tmp := path + ".tmp"
	// temp + fsync + rename + fsync(dir) is the same durability
	// recipe Conversation.Replace uses for the JSONL log. Without
	// fsync, a crash between rename and the OS flushing buffers can
	// leave the renamed file empty or partially written even though
	// the rename itself is atomic. For long-running memory we want
	// the same crash-survival guarantee the conv log has — same
	// session_id, same restart, same data.
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write([]byte(body)); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
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
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	// Best-effort parent fsync so the rename itself survives a
	// crash. Some filesystems (notably certain Windows configs) don't
	// support directory fsync; failure here only weakens durability,
	// not correctness — the rename is still atomic at the FS layer.
	if d, derr := os.Open(dir); derr == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
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
	s = NormalizeSearchQuery(s)
	s = strings.ToLower(s)
	var out []string
	seen := map[string]bool{}
	var cur strings.Builder
	flush := func() {
		t := cur.String()
		cur.Reset()
		if len(t) >= 2 && !memoryStopwords[t] && !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	for _, r := range s {
		if len(out) >= MaxSearchQueryTerms {
			break
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			cur.WriteRune(r)
		} else {
			flush()
		}
	}
	if len(out) < MaxSearchQueryTerms {
		flush()
	}
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

func normalizedMemoryContentKey(content string) string {
	return strings.ToLower(strings.Join(strings.Fields(content), " "))
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
