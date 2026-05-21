package memory

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
)

func newTestStore(t *testing.T) *FileMemoryStore {
	t.Helper()
	dir := t.TempDir()
	return &FileMemoryStore{
		MemoryDir:      filepath.Join(dir, "memory"),
		UserPath:       filepath.Join(dir, "USER.md"),
		TopicCharLimit: 200,
		CoreCharLimit:  200,
		UserCharLimit:  100,
	}
}

// generalPath is the on-disk file the default "general" topic writes to.
// Tests that previously poked at s.MemoryPath read it instead.
func (s *FileMemoryStore) generalPath() string {
	return filepath.Join(s.MemoryDir, "topics", "general.md")
}

func TestMemoryAddReadWrite(t *testing.T) {
	s := newTestStore(t)
	resp, err := s.Add(TargetMemory, "", "Project is Go 1.22, uses sqlc + chi")
	if err != nil {
		t.Fatal(err)
	}
	if !resp.OK {
		t.Fatalf("add failed: %+v", resp)
	}
	if len(resp.Entries) != 1 || !strings.Contains(resp.Entries[0], "sqlc") {
		t.Fatalf("entries not returned live: %+v", resp.Entries)
	}

	// File on disk should round-trip.
	raw, err := os.ReadFile(s.generalPath())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "sqlc") {
		t.Fatalf("on-disk content missing entry: %q", raw)
	}
}

func TestMemoryAddRejectsDuplicate(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Add(TargetMemory, "", "fact"); err != nil {
		t.Fatal(err)
	}
	resp, err := s.Add(TargetMemory, "", "fact")
	if err != nil {
		t.Fatal(err)
	}
	if !resp.OK {
		t.Fatalf("duplicate should not error: %+v", resp)
	}
	if !strings.Contains(resp.Message, "duplicate") {
		t.Fatalf("expected duplicate message, got %q", resp.Message)
	}
	if len(resp.Entries) != 1 {
		t.Fatalf("duplicate must not produce second entry: %+v", resp.Entries)
	}
}

func TestMemoryAddOverflow(t *testing.T) {
	s := newTestStore(t)
	s.TopicCharLimit = 50
	if _, err := s.Add(TargetMemory, "", strings.Repeat("a", 30)); err != nil {
		t.Fatal(err)
	}
	resp, err := s.Add(TargetMemory, "", strings.Repeat("b", 30))
	if err != nil {
		t.Fatal(err)
	}
	if resp.OK {
		t.Fatalf("expected overflow rejection, got OK: %+v", resp)
	}
	if !strings.Contains(resp.Message, "push past the limit") {
		t.Fatalf("expected limit-exceeded message, got %q", resp.Message)
	}
	// Current state returned so agent can consolidate.
	if len(resp.Entries) != 1 {
		t.Fatalf("overflow rejection should carry current entries, got %+v", resp.Entries)
	}
}

// TestMemoryAdd_HitsTopicCountCap pins that creating a NEW topic once
// the dir is already at MaxTopics is rejected with the same overflow-
// style "consolidate or remove" message — parallel to how the per-
// topic char limit behaves. Without this cap a model that spins up a
// new topic per session ("incidents-2026-05-21", "incidents-2026-05-22",
// …) eventually slows session startup (Snapshot walks topics/) and
// bloats the in-prompt topic index.
func TestMemoryAdd_HitsTopicCountCap(t *testing.T) {
	s := newTestStore(t)
	s.MaxTopics = 3 // tight cap for the test

	for i, name := range []string{"a", "b", "c"} {
		resp, err := s.Add(TargetMemory, name, "fact "+name)
		if err != nil {
			t.Fatalf("add %d: %v", i, err)
		}
		if !resp.OK {
			t.Fatalf("add %d (%q) should succeed under cap, got %+v", i, name, resp)
		}
	}

	resp, err := s.Add(TargetMemory, "d", "fourth fact would exceed cap")
	if err != nil {
		t.Fatal(err)
	}
	if resp.OK {
		t.Fatalf("at 3/3 topics, a new topic must be rejected; got OK %+v", resp)
	}
	if !strings.Contains(resp.Message, "topics") || !strings.Contains(resp.Message, "3/3") {
		t.Errorf("rejection message must name the cap and current count; got %q", resp.Message)
	}
}

// TestMemoryAdd_ExistingTopicSucceedsAtCap pins the carve-out: at the
// cap, writes to an already-existing topic still succeed. The cap is
// "no NEW topics", not "no more writes" — once a topic exists, the
// per-topic char limit governs growth.
func TestMemoryAdd_ExistingTopicSucceedsAtCap(t *testing.T) {
	s := newTestStore(t)
	s.MaxTopics = 2
	for _, name := range []string{"alpha", "beta"} {
		if _, err := s.Add(TargetMemory, name, "first "+name); err != nil {
			t.Fatal(err)
		}
	}
	resp, err := s.Add(TargetMemory, "alpha", "another alpha fact")
	if err != nil {
		t.Fatal(err)
	}
	if !resp.OK {
		t.Errorf("adding to an EXISTING topic at cap must succeed; got %+v", resp)
	}
}

// TestMemoryAdd_CoreDoesNotCountTowardTopicCap pins that core.md lives
// outside topics/ and is exempt from the count. Otherwise a project
// could be stuck at "31 topics + core" unable to add even a single
// custom topic.
func TestMemoryAdd_CoreDoesNotCountTowardTopicCap(t *testing.T) {
	s := newTestStore(t)
	s.MaxTopics = 1
	if _, err := s.Add(TargetMemory, CoreTopic, "durable fact"); err != nil {
		t.Fatal(err)
	}
	// core.md exists, but topics/ is empty → first custom topic OK.
	resp, err := s.Add(TargetMemory, "auth", "first custom fact")
	if err != nil {
		t.Fatal(err)
	}
	if !resp.OK {
		t.Errorf("core.md must not count toward MaxTopics; got %+v", resp)
	}
}

// TestMemoryAdd_DefaultMaxTopicsAllowsNormalUsage sanity-checks that
// the package-level default (32) is comfortable for typical agent
// use — adding a dozen named categories should never trip it.
func TestMemoryAdd_DefaultMaxTopicsAllowsNormalUsage(t *testing.T) {
	dir := t.TempDir()
	s := &FileMemoryStore{
		MemoryDir:      filepath.Join(dir, "memory"),
		UserPath:       filepath.Join(dir, "USER.md"),
		TopicCharLimit: 200,
		// MaxTopics left at zero → DefaultMaxTopics (32).
	}
	for _, name := range []string{
		"stack", "deploy", "conventions", "incidents", "people",
		"lessons", "auth", "ops", "alerts", "perf", "docs", "links",
	} {
		resp, err := s.Add(TargetMemory, name, "fact "+name)
		if err != nil {
			t.Fatal(err)
		}
		if !resp.OK {
			t.Fatalf("12 topics must fit under default cap of 32; %q failed: %+v", name, resp)
		}
	}
}

func TestMemoryReplaceSubstring(t *testing.T) {
	s := newTestStore(t)
	_, _ = s.Add(TargetMemory, "", "User prefers dark mode in editors")
	resp, err := s.Replace(TargetMemory, "", "dark mode", "User prefers light mode in VS Code, dark mode in terminal")
	if err != nil {
		t.Fatal(err)
	}
	if !resp.OK {
		t.Fatalf("replace failed: %+v", resp)
	}
	if !strings.Contains(resp.Entries[0], "light mode in VS Code") {
		t.Fatalf("replacement did not take: %+v", resp.Entries)
	}
}

func TestMemoryReplaceAmbiguous(t *testing.T) {
	s := newTestStore(t)
	_, _ = s.Add(TargetMemory, "", "use Go 1.22")
	_, _ = s.Add(TargetMemory, "", "use sqlc not gorm")
	resp, err := s.Replace(TargetMemory, "", "use", "REPLACED")
	if err != nil {
		t.Fatal(err)
	}
	if resp.OK {
		t.Fatalf("ambiguous replace should be rejected: %+v", resp)
	}
	if len(resp.Matches) != 2 {
		t.Fatalf("expected 2 match previews, got %+v", resp.Matches)
	}
}

// TestMemoryReplaceAmbiguousPreviewsAreUTF8Safe pins that the
// multi-match preview clamping doesn't slice a multi-byte rune.
// findUnique previously used e[:80], which corrupted Cyrillic /
// CJK / accent previews when the cut landed inside a rune.
func TestMemoryReplaceAmbiguousPreviewsAreUTF8Safe(t *testing.T) {
	s := newTestStore(t)
	// Two long Cyrillic entries containing the same trigger token —
	// "use". Each rune is 2 bytes, so the 80-byte cap lands inside a
	// rune for sure.
	long := strings.Repeat("ё", 100)        // 200 bytes, exceeds 80
	if _, err := s.Add(TargetMemory, "", "use "+long+" alpha"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Add(TargetMemory, "", "use "+long+" beta"); err != nil {
		t.Fatal(err)
	}
	resp, err := s.Replace(TargetMemory, "", "use", "REPLACED")
	if err != nil {
		t.Fatal(err)
	}
	if resp.OK {
		t.Fatalf("ambiguous replace must be rejected: %+v", resp)
	}
	for i, p := range resp.Matches {
		if !utf8.ValidString(p) {
			t.Fatalf("match preview %d is not valid UTF-8: %q", i, p)
		}
	}
}

func TestMemoryReplaceIdenticalDuplicatesTreatedAsOne(t *testing.T) {
	s := newTestStore(t)
	// Write file directly so we can simulate exact duplicates (Add
	// would reject the second copy).
	if err := writeMemoryFile(s.generalPath(), []string{"identical fact", "identical fact"}); err != nil {
		t.Fatal(err)
	}
	resp, err := s.Replace(TargetMemory, "", "identical fact", "updated")
	if err != nil {
		t.Fatal(err)
	}
	if !resp.OK {
		t.Fatalf("exact-dupe replace should succeed (operate on first): %+v", resp)
	}
}

func TestMemoryRemove(t *testing.T) {
	s := newTestStore(t)
	_, _ = s.Add(TargetMemory, "", "first")
	_, _ = s.Add(TargetMemory, "", "second")
	resp, err := s.Remove(TargetMemory, "", "first")
	if err != nil {
		t.Fatal(err)
	}
	if !resp.OK {
		t.Fatalf("remove failed: %+v", resp)
	}
	if len(resp.Entries) != 1 || resp.Entries[0] != "second" {
		t.Fatalf("remove left wrong entries: %+v", resp.Entries)
	}
}

func TestMemoryRemoveNotFound(t *testing.T) {
	s := newTestStore(t)
	_, _ = s.Add(TargetMemory, "", "first")
	resp, err := s.Remove(TargetMemory, "", "nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	if resp.OK {
		t.Fatalf("remove of nonexistent should fail: %+v", resp)
	}
}

func TestMemorySecurityScanRejectsAuthorizedKeys(t *testing.T) {
	s := newTestStore(t)
	resp, err := s.Add(TargetMemory, "", "echo my_key >> ~/.ssh/authorized_keys")
	if err != nil {
		t.Fatal(err)
	}
	if resp.OK {
		t.Fatalf("authorized_keys content should be blocked: %+v", resp)
	}
	if !strings.Contains(resp.Message, "blocked") {
		t.Fatalf("expected blocked message, got %q", resp.Message)
	}
}

func TestMemorySecurityScanRejectsInvisibleUnicode(t *testing.T) {
	s := newTestStore(t)
	resp, err := s.Add(TargetMemory, "", "innocent‮looking note")
	if err != nil {
		t.Fatal(err)
	}
	if resp.OK {
		t.Fatalf("invisible unicode should be blocked: %+v", resp)
	}
	if !strings.Contains(resp.Message, "invisible") {
		t.Fatalf("expected invisible-unicode message, got %q", resp.Message)
	}
}

func TestMemorySecurityScanRejectsDelimiter(t *testing.T) {
	s := newTestStore(t)
	resp, err := s.Add(TargetMemory, "", "fact\n§\nother")
	if err != nil {
		t.Fatal(err)
	}
	if resp.OK {
		t.Fatalf("delimiter sequence in content should be blocked: %+v", resp)
	}
}

func TestMemorySnapshotReflectsDiskState(t *testing.T) {
	s := newTestStore(t)
	if got := s.Snapshot(); got != "" {
		t.Fatalf("empty snapshot expected, got %q", got)
	}
	_, _ = s.Add(TargetMemory, "", "first fact")
	snap1 := s.Snapshot()
	if !strings.Contains(snap1, "first fact") {
		t.Fatalf("snapshot missing first entry: %q", snap1)
	}
	// Snapshot is not internally cached: a second write surfaces on
	// the next read. Per-session prompt stability is the Loop's job.
	_, _ = s.Add(TargetMemory, "", "second fact")
	snap2 := s.Snapshot()
	if !strings.Contains(snap2, "second fact") {
		t.Fatalf("snapshot should reflect post-write disk state: %q", snap2)
	}
	if snap1 == snap2 {
		t.Fatalf("expected snapshot to change after a write, got identical %q", snap1)
	}
}

func TestMemorySnapshotEmptyReturnsEmpty(t *testing.T) {
	s := newTestStore(t)
	if got := s.Snapshot(); got != "" {
		t.Fatalf("expected empty snapshot, got %q", got)
	}
}

func TestMemoryTwoTargetsIndependent(t *testing.T) {
	s := newTestStore(t)
	_, _ = s.Add(TargetMemory, "", "agent fact")
	_, _ = s.Add(TargetUser, "", "user preference")

	memEntries, err := readMemoryFile(s.generalPath())
	if err != nil {
		t.Fatal(err)
	}
	userEntries, err := readMemoryFile(s.UserPath)
	if err != nil {
		t.Fatal(err)
	}
	// On-disk entries carry a leading "[<RFC3339>]\n" timestamp prefix.
	// Compare against stripped content via entryContent.
	if len(memEntries) != 1 || entryContent(memEntries[0]) != "agent fact" {
		t.Fatalf("memory target wrong: %+v", memEntries)
	}
	if len(userEntries) != 1 || entryContent(userEntries[0]) != "user preference" {
		t.Fatalf("user target wrong: %+v", userEntries)
	}

	snap := s.Snapshot()
	if !strings.Contains(snap, "agent fact") || !strings.Contains(snap, "user preference") {
		t.Fatalf("snapshot must include both targets:\n%s", snap)
	}
	if !strings.Contains(snap, "MEMORY") || !strings.Contains(snap, "USER PROFILE") {
		t.Fatalf("snapshot missing headers:\n%s", snap)
	}
}

func TestMemoryAtomicWriteRoundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "MEMORY.md")
	entries := []string{"one", "two", "three"}
	if err := writeMemoryFile(path, entries); err != nil {
		t.Fatal(err)
	}
	got, err := readMemoryFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0] != "one" || got[2] != "three" {
		t.Fatalf("roundtrip mismatch: %+v", got)
	}
	// No stray tempfiles.
	matches, _ := filepath.Glob(filepath.Join(dir, ".mem-*.tmp"))
	if len(matches) != 0 {
		t.Fatalf("tempfile not cleaned up: %v", matches)
	}
}

func TestMemoryDisabledTarget(t *testing.T) {
	dir := t.TempDir()
	s := &FileMemoryStore{
		MemoryPath: filepath.Join(dir, "MEMORY.md"),
		// UserPath intentionally empty
		MemoryCharLimit: 200,
	}
	resp, err := s.Add(TargetUser, "", "anything")
	if err != nil {
		t.Fatal(err)
	}
	if resp.OK {
		t.Fatalf("user target with empty path should be disabled, got %+v", resp)
	}
	if !strings.Contains(resp.Message, "disabled") {
		t.Fatalf("expected disabled message, got %q", resp.Message)
	}
}

func TestMemoryResponse_UsageOmittedOnEarlyErrors(t *testing.T) {
	s := newTestStore(t)
	// Empty content fails before we compute usage; the resulting
	// MemoryResponse should omit Usage entirely so the agent doesn't
	// see misleading zero counters.
	resp, err := s.Add(TargetMemory, "", "   ")
	if err != nil {
		t.Fatal(err)
	}
	if resp.Usage != nil {
		t.Fatalf("Usage should be omitted on validation error, got %+v", resp.Usage)
	}
	out, err := json.Marshal(resp)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), `"usage"`) {
		t.Fatalf("serialized response should not include usage field: %s", out)
	}
}

func TestMemoryResponse_UsagePresentOnSuccess(t *testing.T) {
	s := newTestStore(t)
	resp, err := s.Add(TargetMemory, "", "real entry")
	if err != nil {
		t.Fatal(err)
	}
	if resp.Usage == nil {
		t.Fatal("Usage should be present on successful add")
	}
	if resp.Usage.EntryCount != 1 {
		t.Fatalf("Usage.EntryCount = %d, want 1", resp.Usage.EntryCount)
	}
	if resp.Usage.CharsLimit <= 0 {
		t.Fatalf("Usage.CharsLimit should be set, got %d", resp.Usage.CharsLimit)
	}
}

func TestMemoryFlockSerializesConcurrentStores(t *testing.T) {
	// Two FileMemoryStore instances pointing at the same MEMORY.md
	// stand in for two affent processes. Without cross-process flock,
	// interleaved read-modify-write cycles drop entries; with flock,
	// every add survives.
	dir := t.TempDir()
	memDir := filepath.Join(dir, "memory")
	newStore := func() *FileMemoryStore {
		return &FileMemoryStore{
			MemoryDir:      memDir,
			TopicCharLimit: 32 * 1024,
		}
	}
	generalPath := filepath.Join(memDir, "topics", "general.md")

	const writers = 4
	const perWriter = 12
	var wg sync.WaitGroup
	errCh := make(chan error, writers*perWriter)
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(wid int) {
			defer wg.Done()
			s := newStore()
			for i := 0; i < perWriter; i++ {
				resp, err := s.Add(TargetMemory, "", fmt.Sprintf("writer-%d entry-%d", wid, i))
				if err != nil {
					errCh <- err
					return
				}
				if !resp.OK {
					errCh <- fmt.Errorf("writer %d add %d: not ok: %s", wid, i, resp.Message)
					return
				}
			}
		}(w)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatal(err)
	}

	final, err := readMemoryFile(generalPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(final); got != writers*perWriter {
		t.Fatalf("expected %d entries to survive concurrent writers, got %d", writers*perWriter, got)
	}
}

// TestMemoryTopicBucketIsolation pins that two custom topics keep
// their entries independent (the central design promise of v2 —
// capacity grows by topic count, not single-file cap).
func TestMemoryTopicBucketIsolation(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Add(TargetMemory, "auth", "JWT secret rotates monthly"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Add(TargetMemory, "deploy", "deploy via fly.io with `fly deploy --remote-only`"); err != nil {
		t.Fatal(err)
	}
	authPath := filepath.Join(s.MemoryDir, "topics", "auth.md")
	deployPath := filepath.Join(s.MemoryDir, "topics", "deploy.md")
	authBody, _ := os.ReadFile(authPath)
	deployBody, _ := os.ReadFile(deployPath)
	if !strings.Contains(string(authBody), "JWT") {
		t.Errorf("auth topic missing entry: %q", authBody)
	}
	if strings.Contains(string(authBody), "fly.io") {
		t.Error("auth topic leaked deploy content")
	}
	if !strings.Contains(string(deployBody), "fly.io") {
		t.Errorf("deploy topic missing entry: %q", deployBody)
	}
}

// TestMemorySnapshotInlinesGeneralButIndexesCustomTopics pins the
// composition: core + general are auto-injected (so the default
// "save a fact, see it next session" UX still works), custom topics
// appear only as an index hint (model uses search to read them).
func TestMemorySnapshotInlinesGeneralButIndexesCustomTopics(t *testing.T) {
	s := newTestStore(t)
	_, _ = s.Add(TargetMemory, "", "general fact one")
	_, _ = s.Add(TargetMemory, CoreTopic, "core durable fact")
	_, _ = s.Add(TargetMemory, "auth", "details about auth that grow over time")

	snap := s.Snapshot()
	if !strings.Contains(snap, "general fact one") {
		t.Errorf("general fact must inline into snapshot:\n%s", snap)
	}
	if !strings.Contains(snap, "core durable fact") {
		t.Errorf("core fact must inline into snapshot:\n%s", snap)
	}
	if strings.Contains(snap, "details about auth") {
		t.Error("custom topic body must NOT inline; only the index should mention it")
	}
	if !strings.Contains(snap, "auth: 1 entry") {
		t.Errorf("custom topic index missing in snapshot:\n%s", snap)
	}
}

// TestMemorySnapshotTopicIndexOrdersByRecency pins that the in-prompt
// topic index lists freshest-first with a date marker, not lex-sorted.
// Reason: in a long-running memory the model scans the system prompt
// top-down — a topic touched yesterday is almost always more useful
// to investigate than one last edited six months ago.
func TestMemorySnapshotTopicIndexOrdersByRecency(t *testing.T) {
	defer func(orig func() time.Time) { nowUTC = orig }(nowUTC)
	s := newTestStore(t)

	nowUTC = func() time.Time { return time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC) }
	_, _ = s.Add(TargetMemory, "alpha-stale", "very old fact")
	nowUTC = func() time.Time { return time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC) }
	_, _ = s.Add(TargetMemory, "zeta-fresh", "just-written fact")

	snap := s.Snapshot()
	pStale := strings.Index(snap, "alpha-stale")
	pFresh := strings.Index(snap, "zeta-fresh")
	if pStale < 0 || pFresh < 0 {
		t.Fatalf("both topics must appear in snapshot:\n%s", snap)
	}
	if pFresh > pStale {
		t.Errorf("fresh topic must appear before stale one in index:\n%s", snap)
	}
	if !strings.Contains(snap, "newest 2026-05-20") {
		t.Errorf("snapshot must show a date marker for the fresh topic:\n%s", snap)
	}
	if !strings.Contains(snap, "newest 2025-01-15") {
		t.Errorf("snapshot must show a date marker for the stale topic too:\n%s", snap)
	}
}

// TestMemorySearchMatchesTopicName pins a real-rollout finding:
// users organize memory by topic and query with terms that echo
// the topic name ("incident details"), but the topic word often
// isn't inside the entry body. Pre-fix scoring against content
// alone returned 0 hits; now topic name is part of the scored
// corpus so the obvious match surfaces.
func TestMemorySearchMatchesTopicName(t *testing.T) {
	s := newTestStore(t)
	_, _ = s.Add(TargetMemory, "incidents", "Yesterday DB connection pool exhausted at 14:30 UTC — diagnosed via pg_stat_activity.")
	_, _ = s.Add(TargetMemory, "people", "Lead engineer is Wei, prefers async-only meetings.")

	// Query echoes the topic name only — content has no "incident" word.
	resp, err := s.Search(TargetMemory, "", "incident", 5)
	if err != nil || !resp.OK || len(resp.Results) == 0 {
		t.Fatalf("topic-name match must surface; got %+v err=%v", resp, err)
	}
	if resp.Results[0].Topic != "incidents" {
		t.Errorf("expected incidents topic first, got %q", resp.Results[0].Topic)
	}

	// Same with "people" — content says "Lead engineer is Wei".
	resp, _ = s.Search(TargetMemory, "", "people", 5)
	if !resp.OK || len(resp.Results) == 0 {
		t.Fatalf("topic-name match must surface for 'people' too: %+v", resp)
	}
}

// TestMemorySearchAcrossTopics pins lexical scoring + stopword
// filtering. A query lands ranked results across whichever topic
// the matching content lives in; stopword-only queries return no
// results to avoid the substring-of-noise problem.
func TestMemorySearchAcrossTopics(t *testing.T) {
	s := newTestStore(t)
	_, _ = s.Add(TargetMemory, "auth", "rotate the JWT signing key quarterly")
	_, _ = s.Add(TargetMemory, "deploy", "JWT validation runs in the edge worker")
	_, _ = s.Add(TargetMemory, "deploy", "deploys go through fly.io")

	resp, err := s.Search(TargetMemory, "", "JWT key rotation", 5)
	if err != nil {
		t.Fatal(err)
	}
	if !resp.OK {
		t.Fatalf("search failed: %+v", resp)
	}
	if len(resp.Results) != 2 {
		t.Fatalf("expected 2 JWT-bearing entries, got %d: %+v", len(resp.Results), resp.Results)
	}
	// auth entry has BOTH JWT + rotation, should outrank the deploy one.
	if resp.Results[0].Topic != "auth" {
		t.Errorf("auth entry (more matching terms) should rank first, got topic=%q", resp.Results[0].Topic)
	}

	// Stopword-only query → empty terms → zero results.
	resp, _ = s.Search(TargetMemory, "", "the and of with", 5)
	if resp.OK {
		t.Errorf("stopword-only query should be rejected with OK=false; got %+v", resp)
	}
}

// TestMemoryLegacyMigration pins that a pre-v2 .affent/MEMORY.md is
// moved into .affent/memory/topics/general.md on first access — so
// existing users don't lose data on the layout bump.
func TestMemoryLegacyMigration(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, ".affent", "MEMORY.md")
	if err := os.MkdirAll(filepath.Dir(legacy), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy, []byte("legacy fact"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := NewFileMemoryStore(dir)
	// First Snapshot triggers migration.
	snap := s.Snapshot()
	if !strings.Contains(snap, "legacy fact") {
		t.Fatalf("legacy fact not surfaced after migration: %q", snap)
	}
	// Legacy file should be gone; new file should exist.
	if _, err := os.Stat(legacy); err == nil {
		t.Error("legacy MEMORY.md should have been moved away")
	}
	migrated := filepath.Join(dir, ".affent", "memory", "topics", "general.md")
	body, err := os.ReadFile(migrated)
	if err != nil || !strings.Contains(string(body), "legacy fact") {
		t.Fatalf("migrated file missing/wrong: err=%v body=%q", err, body)
	}
}

// TestMemoryAddStampsTimestamp pins that Add writes the on-disk entry
// with a leading "[<RFC3339>]\n" prefix and that the response surfaces
// only the bare content (no prefix leaking into the model context).
func TestMemoryAddStampsTimestamp(t *testing.T) {
	defer func(orig func() time.Time) { nowUTC = orig }(nowUTC)
	fixed := time.Date(2026, 5, 21, 10, 30, 0, 0, time.UTC)
	nowUTC = func() time.Time { return fixed }

	s := newTestStore(t)
	resp, err := s.Add(TargetMemory, "stack", "go 1.22 + sqlc")
	if err != nil {
		t.Fatal(err)
	}
	if !resp.OK {
		t.Fatalf("add not ok: %+v", resp)
	}
	if len(resp.Entries) != 1 || resp.Entries[0] != "go 1.22 + sqlc" {
		t.Errorf("response Entries should be content-only (no timestamp); got %+v", resp.Entries)
	}
	path := filepath.Join(s.MemoryDir, "topics", "stack.md")
	raw, _ := os.ReadFile(path)
	want := "[2026-05-21T10:30:00Z]\ngo 1.22 + sqlc"
	if string(raw) != want {
		t.Errorf("on-disk content = %q, want %q", raw, want)
	}
}

// TestMemoryReplaceRestampsTimestamp pins that Replace updates the
// timestamp — the entry was re-affirmed, so its freshness signal
// should reflect now, not the original Add time.
func TestMemoryReplaceRestampsTimestamp(t *testing.T) {
	defer func(orig func() time.Time) { nowUTC = orig }(nowUTC)
	t1 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	nowUTC = func() time.Time { return t1 }

	s := newTestStore(t)
	_, _ = s.Add(TargetMemory, "auth", "JWT rotates monthly")

	nowUTC = func() time.Time { return t2 }
	_, _ = s.Replace(TargetMemory, "auth", "JWT", "JWT rotates weekly")

	raw, _ := os.ReadFile(filepath.Join(s.MemoryDir, "topics", "auth.md"))
	if !strings.HasPrefix(string(raw), "[2026-05-01T00:00:00Z]") {
		t.Errorf("Replace should re-stamp to now; on-disk: %q", raw)
	}
}

// TestMemoryRemoveDeletesEmptyTopicFile pins the empty-file-cleanup
// contract: after Remove drops the last entry, the topic file and
// its .lock sidecar are gone — no zombie 0-byte files lingering in
// the topics dir.
func TestMemoryRemoveDeletesEmptyTopicFile(t *testing.T) {
	s := newTestStore(t)
	_, _ = s.Add(TargetMemory, "tmp", "only entry")
	path := filepath.Join(s.MemoryDir, "topics", "tmp.md")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected topic file to exist: %v", err)
	}
	_, err := s.Remove(TargetMemory, "tmp", "only entry")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("topic file should be gone after last Remove, got err=%v", err)
	}
	if _, err := os.Stat(path + ".lock"); !os.IsNotExist(err) {
		t.Errorf("lock sidecar should also be gone, got err=%v", err)
	}
}

// TestMemorySearchTrimsLongSnippet pins that search snippets cap at
// memorySnippetMax chars + "..." marker. Without this, a single
// multi-paragraph postmortem dumps its full body into the model
// context every time something hits it.
func TestMemorySearchTrimsLongSnippet(t *testing.T) {
	s := newTestStore(t)
	s.TopicCharLimit = 4000 // override the tight default so the long fixture fits
	long := strings.Repeat("kubernetes operator deployment ", 30) // ~900 chars
	_, _ = s.Add(TargetMemory, "infra", long)

	resp, err := s.Search(TargetMemory, "infra", "kubernetes", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Results) != 1 {
		t.Fatalf("expected 1 hit, got %d", len(resp.Results))
	}
	snippet := resp.Results[0].Snippet
	if !strings.HasSuffix(snippet, "...") {
		t.Errorf("long snippet should end with '...'; got %q", snippet)
	}
	if len(snippet) > memorySnippetMax+5 {
		t.Errorf("snippet length %d exceeds cap %d+marker", len(snippet), memorySnippetMax)
	}
	if resp.Results[0].CreatedAt == "" {
		t.Errorf("CreatedAt should be populated for stamped entries")
	}
}

// TestMemorySearchSnippetCentersOnMatch pins that the search snippet
// frames the matched term rather than always returning the entry's
// first 300 bytes. Without this, a long entry whose match falls past
// the truncation point shows a head-of-body snippet that doesn't
// explain why the hit ranked.
func TestMemorySearchSnippetCentersOnMatch(t *testing.T) {
	s := newTestStore(t)
	s.TopicCharLimit = 4000

	// 600 bytes of filler, then the unique match term, then more filler.
	body := strings.Repeat("filler one two three ", 30) + " UNIQUEKW middle marker " + strings.Repeat("tail words here ", 30)
	_, _ = s.Add(TargetMemory, "infra", body)

	resp, err := s.Search(TargetMemory, "infra", "uniquekw", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Results) != 1 {
		t.Fatalf("expected 1 hit, got %d", len(resp.Results))
	}
	snip := resp.Results[0].Snippet
	if !strings.Contains(strings.ToLower(snip), "uniquekw") {
		t.Errorf("snippet should contain the matched term; got %q", snip)
	}
	if !strings.HasPrefix(snip, "...") {
		t.Errorf("interior match should have leading '...'; got %q", snip)
	}
	if !strings.HasSuffix(snip, "...") {
		t.Errorf("interior match should have trailing '...'; got %q", snip)
	}
	if len(snip) > memorySnippetMax+8 {
		t.Errorf("snippet length %d should stay within max+ellipsis budget", len(snip))
	}
}

// TestMemorySearchSnippetFallsBackOnTopicMatch pins that when the
// scoring hit came from the topic name (term doesn't appear in the
// body), the snippet falls back to head-truncation — there's no
// in-body position to center on.
func TestMemorySearchSnippetFallsBackOnTopicMatch(t *testing.T) {
	s := newTestStore(t)
	s.TopicCharLimit = 4000

	long := strings.Repeat("paged out detail line ", 30) // ~660 chars, no "incident" inside
	_, _ = s.Add(TargetMemory, "incidents", long)

	// Query word lives in the topic name, not the entry body.
	resp, err := s.Search(TargetMemory, "incidents", "incident", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Results) != 1 {
		t.Fatalf("expected 1 hit, got %d", len(resp.Results))
	}
	snip := resp.Results[0].Snippet
	if strings.HasPrefix(snip, "...") {
		t.Errorf("topic-only match should head-truncate, not center; got %q", snip)
	}
	if !strings.HasSuffix(snip, "...") {
		t.Errorf("long entry should still be truncated; got %q", snip)
	}
}

// TestMemorySearchRecencyBoost pins that two entries with identical
// term overlap rank by freshness — the fresh one above the old one.
// Without this, a stale fact with the same keyword density dominates
// retrieval purely by lexical accident in a long-running memory.
// Standard IR "freshness boost" pattern; recencyFloor keeps old
// facts in the results, just behind fresh ones.
func TestMemorySearchRecencyBoost(t *testing.T) {
	defer func(orig func() time.Time) { nowUTC = orig }(nowUTC)
	s := newTestStore(t)

	nowUTC = func() time.Time { return time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC) }
	_, _ = s.Add(TargetMemory, "ops", "deploy via fly.io main path")
	nowUTC = func() time.Time { return time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC) }
	_, _ = s.Add(TargetMemory, "ops", "deploy via fly.io fresh path")

	nowUTC = func() time.Time { return time.Date(2026, 6, 1, 0, 1, 0, 0, time.UTC) }
	resp, err := s.Search(TargetMemory, "ops", "deploy fly.io", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Results) < 2 {
		t.Fatalf("expected 2 results, got %d", len(resp.Results))
	}
	if !strings.Contains(resp.Results[0].Snippet, "fresh") {
		t.Errorf("fresh entry should rank above year-old one; got order: %+v", resp.Results)
	}
	// Old entry must still appear (recencyFloor > 0 keeps it visible).
	old := false
	for _, r := range resp.Results {
		if strings.Contains(r.Snippet, "main path") {
			old = true
		}
	}
	if !old {
		t.Errorf("old entry should still appear (floor prevents complete burial)")
	}
}

// TestMemoryRecencyFactorBoundaries pins the math: today=1.0, half-
// horizon=midway, past-horizon=floor, empty/unparseable=1.0 (no
// penalty for legacy unstamped entries — the rollout of stamping
// shouldn't suddenly demote everything that was there before).
func TestMemoryRecencyFactorBoundaries(t *testing.T) {
	defer func(orig func() time.Time) { nowUTC = orig }(nowUTC)
	nowUTC = func() time.Time { return time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC) }
	cases := []struct {
		name string
		in   string
		want float64
	}{
		{"empty → no penalty", "", 1.0},
		{"unparseable → no penalty", "not-a-date", 1.0},
		{"now → 1.0", "2026-06-01T00:00:00Z", 1.0},
		{"half horizon → midway", "2025-12-02T00:00:00Z", 1.0 - (1.0-recencyFloor)*0.5},
		{"past horizon → floor", "2024-01-01T00:00:00Z", recencyFloor},
	}
	for _, c := range cases {
		got := recencyFactor(c.in)
		if got < c.want-0.01 || got > c.want+0.01 {
			t.Errorf("%s: recencyFactor(%q) = %v, want %v", c.name, c.in, got, c.want)
		}
	}
}

// TestMemoryListTopicsCarriesNewestAt pins that ListTopics returns
// the most-recent timestamp per topic — operators / models use this
// to prioritize stale-vs-fresh topics without reading bodies.
func TestMemoryListTopicsCarriesNewestAt(t *testing.T) {
	defer func(orig func() time.Time) { nowUTC = orig }(nowUTC)

	s := newTestStore(t)
	nowUTC = func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	_, _ = s.Add(TargetMemory, "old", "ancient fact")
	nowUTC = func() time.Time { return time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC) }
	_, _ = s.Add(TargetMemory, "fresh", "recent fact")

	resp, err := s.ListTopics(TargetMemory)
	if err != nil {
		t.Fatal(err)
	}
	var oldTS, freshTS string
	for _, t := range resp.Topics {
		switch t.Topic {
		case "old":
			oldTS = t.NewestAt
		case "fresh":
			freshTS = t.NewestAt
		}
	}
	if oldTS != "2026-01-01T00:00:00Z" {
		t.Errorf("old topic NewestAt = %q, want 2026-01-01", oldTS)
	}
	if freshTS != "2026-05-01T00:00:00Z" {
		t.Errorf("fresh topic NewestAt = %q, want 2026-05-01", freshTS)
	}
}

func TestMemoryInvalidTarget(t *testing.T) {
	s := newTestStore(t)
	resp, err := s.Add("garbage", "", "x")
	if err != nil {
		t.Fatalf("invalid target should surface as MemoryResponse, not Go error: %v", err)
	}
	if resp.OK {
		t.Fatalf("invalid target should return OK=false, got %+v", resp)
	}
	if !strings.Contains(resp.Message, "invalid memory target") {
		t.Fatalf("expected invalid-target message, got %q", resp.Message)
	}
}
