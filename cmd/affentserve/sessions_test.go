package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/affinefoundation/affent/internal/agent"
	"github.com/affinefoundation/affent/internal/memory"
	"github.com/rs/zerolog"
)

func newTestPool(t *testing.T, maxSessions int, idleTTL string) *SessionPool {
	t.Helper()
	cfg := Config{
		Listen:         "127.0.0.1:0",
		MaxSessions:    maxSessions,
		SessionIdleTTL: idleTTL,
		WorkspaceRoot:  t.TempDir(),
		BaseURL:        "http://127.0.0.1:0",
		APIKey:         "test",
		Model:          "fake",
	}
	logger := zerolog.New(io.Discard)
	pool, err := NewSessionPool(cfg, logger)
	if err != nil {
		t.Fatalf("NewSessionPool: %v", err)
	}
	t.Cleanup(pool.Shutdown)
	return pool
}

func TestSessionPool_GetOrCreate_FailsAfterShutdown(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	pool.Shutdown()
	_, err := pool.GetOrCreate("")
	if err == nil {
		t.Fatal("expected error after shutdown, got nil")
	}
	if err != ErrShuttingDown {
		t.Fatalf("expected ErrShuttingDown, got %v", err)
	}
}

// TestSessionPool_SignalShutdown pins the early-flip contract:
// SignalShutdown sets IsShuttingDown immediately (so /healthz can
// return 503 the moment SIGTERM arrives), GetOrCreate fails with
// ErrShuttingDown after it, and the regular Shutdown remains
// callable (idempotent / safe to follow up with full drain).
func TestSessionPool_SignalShutdown(t *testing.T) {
	pool := newTestPool(t, 4, "5m")

	// Build a session first, then SignalShutdown. Two things to pin:
	// (a) NEW session creation after Signal fails with the specific
	//     ErrShuttingDown sentinel (so chat_completions.go's typed
	//     503-vs-500 branch keeps working);
	// (b) Looking up the ALREADY-EXISTING session also fails with
	//     ErrShuttingDown. The shutting-down check sits BEFORE the
	//     existing-session lookup in GetOrCreate, so once the pool
	//     is draining EVERY chat request is told to retry elsewhere
	//     — even ones that would otherwise continue a live session.
	if _, err := pool.GetOrCreate("alive"); err != nil {
		t.Fatalf("pre-shutdown GetOrCreate: %v", err)
	}

	if pool.IsShuttingDown() {
		t.Fatal("fresh pool must not report shutting down")
	}
	pool.SignalShutdown()
	if !pool.IsShuttingDown() {
		t.Fatal("after SignalShutdown, IsShuttingDown must be true")
	}
	if _, err := pool.GetOrCreate("new"); !errors.Is(err, ErrShuttingDown) {
		t.Errorf("new-session GetOrCreate after Signal must return ErrShuttingDown; got %v", err)
	}
	if _, err := pool.GetOrCreate("alive"); !errors.Is(err, ErrShuttingDown) {
		t.Errorf("existing-session GetOrCreate after Signal must also return ErrShuttingDown; got %v", err)
	}
	// Full Shutdown still completes cleanly. shutdownOnce guards the
	// real drain, so calling Shutdown after SignalShutdown is fine.
	pool.Shutdown()
}

func TestSessionPool_Shutdown_IsIdempotent(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	pool.Shutdown()
	pool.Shutdown() // must not panic / hang
}

func TestSession_Subscribe_AfterCloseReturnsClosedChannel(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	s, err := pool.GetOrCreate("")
	if err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	id, ch := s.Subscribe(1)
	if id != -1 {
		t.Fatalf("Subscribe after Close must return id=-1, got %d", id)
	}
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("channel returned by Subscribe-after-Close must be closed")
		}
	case <-time.After(time.Second):
		t.Fatal("Subscribe-after-Close channel should be already closed")
	}
}

func TestSession_Subscribe_BeforeCloseStillWorks(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	s, err := pool.GetOrCreate("")
	if err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	defer s.Close()

	id, ch := s.Subscribe(4)
	if id < 0 {
		t.Fatalf("Subscribe before Close should yield non-negative id, got %d", id)
	}
	s.Unsubscribe(id)
	// Channel should be closed by Unsubscribe.
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("channel should be closed after Unsubscribe")
		}
	case <-time.After(time.Second):
		t.Fatal("Unsubscribe should have closed the channel")
	}
}

// TestSessionPool_gcOnce_EvictsIdleSessions pins the idle-TTL eviction
// path: a session whose lastUsed is older than now-idleTTL must be
// reaped on the next sweep. Before the gcLoop inequality fix, the
// loop's tick interval was clamped to >= 1m no matter how small the
// TTL was, so `--session-idle-ttl 10s` actually evicted ~60s late.
// gcOnce itself was always correct; we test it directly to keep the
// test fast.
func TestSessionPool_gcOnce_EvictsIdleSessions(t *testing.T) {
	pool := newTestPool(t, 8, "50ms")
	fresh, _ := pool.GetOrCreate("fresh")
	if fresh == nil {
		t.Fatal("create fresh")
	}
	// Backdate "stale" so its lastUsed is well past the 50ms TTL.
	stale, _ := pool.GetOrCreate("stale")
	stale.mu.Lock()
	stale.lastUsed = time.Now().Add(-1 * time.Hour)
	stale.mu.Unlock()

	pool.gcOnce()

	if _, err := pool.Get("fresh"); err != nil {
		t.Errorf("fresh session was evicted: %v", err)
	}
	if _, err := pool.Get("stale"); err == nil {
		t.Errorf("stale session survived gcOnce; should have been evicted")
	}
}

// TestSessionPool_allocSessionDir_StableAcrossCalls pins the durable
// memory-dir contract: same session id MUST resolve to the same path
// every time, regardless of how many times a workspace was allocated
// in between. Without this stability the "long-running memory" claim
// breaks on server restarts and LRU revives — same client, same
// session_id, different memory dir, empty recall.
func TestSessionPool_allocSessionDir_StableAcrossCalls(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	a1, err := pool.allocSessionDir("customer-alpha")
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a second call after eviction/restart.
	a2, err := pool.allocSessionDir("customer-alpha")
	if err != nil {
		t.Fatal(err)
	}
	if a1 != a2 {
		t.Errorf("memory dir for same id must be stable; got %q then %q", a1, a2)
	}
	// Different id → different dir.
	b, err := pool.allocSessionDir("customer-beta")
	if err != nil {
		t.Fatal(err)
	}
	if a1 == b {
		t.Errorf("different session ids must get different dirs; both got %q", a1)
	}
	// Both dirs created on disk.
	for _, p := range []string{a1, b} {
		if info, err := os.Stat(p); err != nil || !info.IsDir() {
			t.Errorf("memory dir %q not a directory (err=%v info=%v)", p, err, info)
		}
	}
}

// TestSessionPool_allocSessionDir_OutsideWorkspace pins that memory
// lives outside the per-session workspace, so Session.Close()'s
// os.RemoveAll(workspace) doesn't blow it away. The cross-restart
// persistence experiment that motivated this design only works if
// the two paths don't overlap.
func TestSessionPool_allocSessionDir_OutsideWorkspace(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	memDir, err := pool.allocSessionDir("alpha")
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := pool.allocWorkspace("alpha")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(workspace) })
	if strings.HasPrefix(memDir, workspace) {
		t.Errorf("memory dir %q must not live inside the ephemeral workspace %q", memDir, workspace)
	}
}

// TestSessionPool_UserMemoryIsolatedPerSession pins that target=user
// writes from one session_id do NOT leak into another session_id's
// snapshot. The library default (~/.config/affent/USER.md) is correct
// for affentctl — one OS user, many workspaces — but in a multi-
// tenant affentserve deployment distinct session_ids are distinct
// tenants, and sharing USER.md across them is a privacy bug.
func TestSessionPool_UserMemoryIsolatedPerSession(t *testing.T) {
	cfg := Config{
		Listen:         "127.0.0.1:0",
		MaxSessions:    4,
		SessionIdleTTL: "5m",
		WorkspaceRoot:  t.TempDir(),
		MemoryRoot:     t.TempDir(),
		EnableMemory:   true,
		BaseURL:        "http://127.0.0.1:0",
		APIKey:         "test",
		Model:          "fake",
	}
	pool, err := NewSessionPool(cfg, zerolog.New(io.Discard))
	if err != nil {
		t.Fatalf("NewSessionPool: %v", err)
	}
	t.Cleanup(pool.Shutdown)

	sa, err := pool.GetOrCreate("alpha")
	if err != nil {
		t.Fatalf("GetOrCreate alpha: %v", err)
	}
	sb, err := pool.GetOrCreate("beta")
	if err != nil {
		t.Fatalf("GetOrCreate beta: %v", err)
	}
	if sa.loop.Memory == nil || sb.loop.Memory == nil {
		t.Fatal("both sessions must have a memory store when EnableMemory=true")
	}
	if _, err := sa.loop.Memory.Add(memory.TargetUser, "", "alpha-only fact"); err != nil {
		t.Fatalf("alpha Add: %v", err)
	}
	if _, err := sb.loop.Memory.Add(memory.TargetUser, "", "beta-only fact"); err != nil {
		t.Fatalf("beta Add: %v", err)
	}
	snapA := sa.loop.Memory.Snapshot()
	snapB := sb.loop.Memory.Snapshot()
	if !strings.Contains(snapA, "alpha-only") {
		t.Errorf("alpha must see its own fact:\n%s", snapA)
	}
	if !strings.Contains(snapB, "beta-only") {
		t.Errorf("beta must see its own fact:\n%s", snapB)
	}
	if strings.Contains(snapA, "beta-only") {
		t.Errorf("beta's user fact leaked into alpha's snapshot:\n%s", snapA)
	}
	if strings.Contains(snapB, "alpha-only") {
		t.Errorf("alpha's user fact leaked into beta's snapshot:\n%s", snapB)
	}
}

// TestSessionPool_ConversationLogIsDurable pins that the JSONL chat
// log survives session eviction + recreation under the same id.
// Without this, the chat handler's design assumption — "we only
// forward the last user message; the rest of the history lives in
// the agent runtime's Conversation log keyed by session_id" — breaks
// on every server restart or LRU revive: the new ephemeral workspace
// holds no .jsonl, so the model wakes up with no prior context even
// though memory is intact.
func TestSessionPool_ConversationLogIsDurable(t *testing.T) {
	cfg := Config{
		Listen:         "127.0.0.1:0",
		MaxSessions:    4,
		SessionIdleTTL: "5m",
		WorkspaceRoot:  t.TempDir(),
		MemoryRoot:     t.TempDir(),
		BaseURL:        "http://127.0.0.1:0",
		APIKey:         "test",
		Model:          "fake",
	}
	pool, err := NewSessionPool(cfg, zerolog.New(io.Discard))
	if err != nil {
		t.Fatalf("NewSessionPool: %v", err)
	}
	t.Cleanup(pool.Shutdown)

	s1, err := pool.GetOrCreate("durable-client")
	if err != nil {
		t.Fatalf("GetOrCreate first: %v", err)
	}
	if err := s1.conv.Append(agent.ChatMessage{Role: "user", Content: "first turn marker"}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	// Force eviction: close + drop from the pool, simulating a restart
	// or LRU eviction. The workspace dir is wiped by Close.
	if err := s1.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	pool.mu.Lock()
	delete(pool.sessions, "durable-client")
	pool.mu.Unlock()

	// Same id again — must see the prior message on reload.
	s2, err := pool.GetOrCreate("durable-client")
	if err != nil {
		t.Fatalf("GetOrCreate second: %v", err)
	}
	msgs := s2.conv.Snapshot()
	found := false
	for _, m := range msgs {
		if strings.Contains(m.Content, "first turn marker") {
			found = true
		}
	}
	if !found {
		t.Errorf("conversation log must persist across session re-create; got messages: %+v", msgs)
	}
}

func TestSessionPool_ShutdownPreservesDurableState(t *testing.T) {
	memRoot := t.TempDir()
	cfg := Config{
		Listen:         "127.0.0.1:0",
		MaxSessions:    4,
		SessionIdleTTL: "5m",
		WorkspaceRoot:  t.TempDir(),
		MemoryRoot:     memRoot,
		BaseURL:        "http://127.0.0.1:0",
		APIKey:         "test",
		Model:          "fake",
	}
	pool, err := NewSessionPool(cfg, zerolog.New(io.Discard))
	if err != nil {
		t.Fatalf("NewSessionPool first: %v", err)
	}
	s1, err := pool.GetOrCreate("shutdown-durable")
	if err != nil {
		t.Fatalf("GetOrCreate first: %v", err)
	}
	if err := s1.conv.Append(agent.ChatMessage{Role: "user", Content: "survives graceful shutdown"}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	pool.Shutdown()
	convPath := filepath.Join(memRoot, "shutdown-durable", "conversation.jsonl")
	if _, err := os.Stat(convPath); err != nil {
		t.Fatalf("Shutdown must preserve durable conversation log: %v", err)
	}

	pool2, err := NewSessionPool(cfg, zerolog.New(io.Discard))
	if err != nil {
		t.Fatalf("NewSessionPool second: %v", err)
	}
	t.Cleanup(pool2.Shutdown)
	s2, err := pool2.GetOrCreate("shutdown-durable")
	if err != nil {
		t.Fatalf("GetOrCreate second: %v", err)
	}
	for _, m := range s2.conv.Snapshot() {
		if strings.Contains(m.Content, "survives graceful shutdown") {
			return
		}
	}
	t.Fatalf("conversation log must reload after graceful shutdown; got %+v", s2.conv.Snapshot())
}

func TestSessionPool_RuntimeSkillsAreDurable(t *testing.T) {
	memRoot := t.TempDir()
	cfg := Config{
		Listen:         "127.0.0.1:0",
		MaxSessions:    4,
		SessionIdleTTL: "5m",
		WorkspaceRoot:  t.TempDir(),
		MemoryRoot:     memRoot,
		EnableBuiltins: true,
		BaseURL:        "http://127.0.0.1:0",
		APIKey:         "test",
		Model:          "fake",
	}
	pool, err := NewSessionPool(cfg, zerolog.New(io.Discard))
	if err != nil {
		t.Fatalf("NewSessionPool: %v", err)
	}
	t.Cleanup(pool.Shutdown)

	const body = "AFFENT ACTIVE SKILL: durable_demo\nSERVER_RUNTIME_SKILL_MARKER"
	s1, err := pool.GetOrCreate("durable-skill-client")
	if err != nil {
		t.Fatalf("GetOrCreate first: %v", err)
	}
	tool, ok := s1.loop.Tools.Get("skill")
	if !ok {
		t.Fatal("skill tool missing")
	}
	args, err := json.Marshal(map[string]any{
		"action":   "install",
		"name":     "durable_demo",
		"body":     body,
		"triggers": []string{"durable demo"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tool.Execute(context.Background(), args); err != nil {
		t.Fatalf("install runtime skill: %v", err)
	}
	skillPath := filepath.Join(memRoot, "durable-skill-client", ".affent", "skills", "durable_demo", "SKILL.md")
	if _, err := os.Stat(skillPath); err != nil {
		t.Fatalf("runtime skill should be stored in durable session dir: %v", err)
	}
	if strings.HasPrefix(skillPath, s1.workspace) {
		t.Fatalf("runtime skill path %q must not live under ephemeral workspace %q", skillPath, s1.workspace)
	}
	if err := s1.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	pool.mu.Lock()
	delete(pool.sessions, "durable-skill-client")
	pool.mu.Unlock()

	s2, err := pool.GetOrCreate("durable-skill-client")
	if err != nil {
		t.Fatalf("GetOrCreate second: %v", err)
	}
	if got := s2.loop.SkillProvider("please use durable demo"); !strings.Contains(got, "SERVER_RUNTIME_SKILL_MARKER") {
		t.Fatalf("durable runtime skill should reload for same session id, got %q", got)
	}
}

// TestSessionPool_DeletePurgesDurableState pins that DELETE is a real
// delete: the durable session dir (conv log + memory) is gone, and a
// subsequent GetOrCreate(id) starts a fresh session with no leaked
// state. Idle eviction is the OTHER path that keeps durable state so
// session resume works; this one explicitly cleans up because the
// client asked us to.
func TestSessionPool_DeletePurgesDurableState(t *testing.T) {
	memRoot := t.TempDir()
	cfg := Config{
		Listen:         "127.0.0.1:0",
		MaxSessions:    4,
		SessionIdleTTL: "5m",
		WorkspaceRoot:  t.TempDir(),
		MemoryRoot:     memRoot,
		EnableMemory:   true,
		BaseURL:        "http://127.0.0.1:0",
		APIKey:         "test",
		Model:          "fake",
	}
	pool, err := NewSessionPool(cfg, zerolog.New(io.Discard))
	if err != nil {
		t.Fatalf("NewSessionPool: %v", err)
	}
	t.Cleanup(pool.Shutdown)

	s1, err := pool.GetOrCreate("done-with-this")
	if err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	if _, err := s1.loop.Memory.Add(memory.TargetMemory, "core", "secret fact"); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := s1.conv.Append(agent.ChatMessage{Role: "user", Content: "first turn"}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	dir := filepath.Join(memRoot, "done-with-this")
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("expected durable dir to exist before delete: %v", err)
	}

	if !pool.Delete("done-with-this") {
		t.Fatal("Delete returned false for known session")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("durable dir must be purged on Delete; stat err=%v", err)
	}

	// New session under the same id starts fresh: no leaked memory, no
	// leaked conv messages from the deleted predecessor.
	s2, err := pool.GetOrCreate("done-with-this")
	if err != nil {
		t.Fatalf("GetOrCreate after delete: %v", err)
	}
	if snap := s2.loop.Memory.Snapshot(); strings.Contains(snap, "secret fact") {
		t.Errorf("deleted-session memory leaked into successor:\n%s", snap)
	}
	for _, m := range s2.conv.Snapshot() {
		if strings.Contains(m.Content, "first turn") {
			t.Errorf("deleted-session conv log leaked into successor")
		}
	}
}

// TestSessionPool_DeletePurgeIsSynchronous pins that Delete's disk
// wipe completes BEFORE Delete returns. Earlier the RemoveAll ran
// after releasing the pool lock so a concurrent GetOrCreate(id)
// could observe the not-yet-deleted dir, OpenConversationAt the
// stale jsonl, and resurrect a zombie session with the predecessor's
// conv log + memory. Now Delete holds the lock until the dir is
// gone — the next GetOrCreate sees a clean slate.
func TestSessionPool_DeletePurgeIsSynchronous(t *testing.T) {
	memRoot := t.TempDir()
	cfg := Config{
		Listen:         "127.0.0.1:0",
		MaxSessions:    4,
		SessionIdleTTL: "5m",
		WorkspaceRoot:  t.TempDir(),
		MemoryRoot:     memRoot,
		EnableMemory:   true,
		BaseURL:        "http://127.0.0.1:0",
		APIKey:         "test",
		Model:          "fake",
	}
	pool, err := NewSessionPool(cfg, zerolog.New(io.Discard))
	if err != nil {
		t.Fatalf("NewSessionPool: %v", err)
	}
	t.Cleanup(pool.Shutdown)

	s, err := pool.GetOrCreate("sync-purge")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.loop.Memory.Add(memory.TargetMemory, "core", "must-be-gone"); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(memRoot, "sync-purge")

	// Delete is supposed to be synchronous wrt the disk purge.
	pool.Delete("sync-purge")
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("sessionDir must be gone by the time Delete returns; stat err=%v", err)
	}
}

// TestSessionPool_DeleteRejectsTraversalID pins that a malicious
// DELETE /v1/sessions/.. cannot RemoveAll the MemoryRoot parent.
// The agent.ValidateSessionID check runs BEFORE the disk purge.
func TestSessionPool_DeleteRejectsTraversalID(t *testing.T) {
	memRoot := t.TempDir()
	// Drop a sentinel file at the MemoryRoot's parent so we can detect
	// an out-of-bounds RemoveAll.
	parent := filepath.Dir(memRoot)
	sentinel := filepath.Join(parent, "delete-me-not.txt")
	if err := os.WriteFile(sentinel, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(sentinel) })

	cfg := Config{
		Listen:         "127.0.0.1:0",
		MaxSessions:    4,
		SessionIdleTTL: "5m",
		WorkspaceRoot:  t.TempDir(),
		MemoryRoot:     memRoot,
		BaseURL:        "http://127.0.0.1:0",
		APIKey:         "test",
		Model:          "fake",
	}
	pool, err := NewSessionPool(cfg, zerolog.New(io.Discard))
	if err != nil {
		t.Fatalf("NewSessionPool: %v", err)
	}
	t.Cleanup(pool.Shutdown)

	for _, bad := range []string{"..", "../escape", "a/b", "a\\b", "with\x00null"} {
		if pool.Delete(bad) {
			t.Errorf("Delete(%q) must reject path-unsafe id", bad)
		}
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("sentinel at MemoryRoot's parent was removed: %v", err)
	}
	if _, err := os.Stat(memRoot); err != nil {
		t.Fatalf("MemoryRoot itself was removed: %v", err)
	}
}

// TestBuildSession_RejectsTraversalSessionID pins that buildSession
// refuses an id that would otherwise let filepath.Join inside the
// session-dir allocator escape the configured MemoryRoot. The
// ValidateSessionID guard runs BEFORE any filesystem call, so a
// malicious client can't even land an empty dir outside the root.
func TestBuildSession_RejectsTraversalSessionID(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	for _, bad := range []string{"..", "../escape", "a/b", "a\\b", "with\x00null"} {
		if _, err := pool.buildSession(bad); err == nil {
			t.Errorf("buildSession(%q) must reject path-unsafe id", bad)
		}
	}
}

// TestSessionPool_RetentionSweep pins the disk-level GC: dirs whose
// conversation.jsonl mtime is older than the configured retention
// get deleted, dirs newer than the cutoff are kept, and dirs whose
// session is currently in the in-memory pool are NEVER touched no
// matter how stale on disk (their in-memory state is authoritative).
func TestSessionPool_RetentionSweep(t *testing.T) {
	memRoot := t.TempDir()
	// Hand-craft three dirs:
	//   stale-evicted: stale jsonl, NOT in pool → should be deleted.
	//   fresh-evicted: fresh jsonl, NOT in pool → kept (mtime newer).
	//   stale-active: stale jsonl, IS in pool   → kept (active wins).
	for _, id := range []string{"stale-evicted", "fresh-evicted", "stale-active"} {
		dir := filepath.Join(memRoot, id)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "conversation.jsonl"), []byte("[]\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Backdate the stale ones to a week ago.
	long := time.Now().Add(-7 * 24 * time.Hour)
	for _, id := range []string{"stale-evicted", "stale-active"} {
		p := filepath.Join(memRoot, id, "conversation.jsonl")
		if err := os.Chtimes(p, long, long); err != nil {
			t.Fatal(err)
		}
	}

	cfg := Config{
		Listen:           "127.0.0.1:0",
		MaxSessions:      4,
		SessionIdleTTL:   "5m",
		SessionRetention: "24h",
		WorkspaceRoot:    t.TempDir(),
		MemoryRoot:       memRoot,
		BaseURL:          "http://127.0.0.1:0",
		APIKey:           "test",
		Model:            "fake",
	}
	pool, err := NewSessionPool(cfg, zerolog.New(io.Discard))
	if err != nil {
		t.Fatalf("NewSessionPool: %v", err)
	}
	t.Cleanup(pool.Shutdown)

	// Make "stale-active" live in the pool so the sweep should skip it
	// despite its on-disk file looking ancient.
	if _, err := pool.GetOrCreate("stale-active"); err != nil {
		t.Fatal(err)
	}
	// GetOrCreate refreshed the mtime by touching conversation.jsonl
	// (via OpenConversationAt's MkdirAll), so re-backdate AFTER the
	// session exists to model "live session but disk file looks old".
	stalePath := filepath.Join(memRoot, "stale-active", "conversation.jsonl")
	if err := os.Chtimes(stalePath, long, long); err != nil {
		t.Fatal(err)
	}

	pool.sweepRetentionOnce()

	if _, err := os.Stat(filepath.Join(memRoot, "stale-evicted")); !os.IsNotExist(err) {
		t.Errorf("stale-evicted dir must be deleted; stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(memRoot, "fresh-evicted")); err != nil {
		t.Errorf("fresh-evicted dir must be kept; stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(memRoot, "stale-active")); err != nil {
		t.Errorf("stale-active dir must be kept (session in pool overrides disk staleness): %v", err)
	}
}

func TestSessionPool_RetentionSweepProcessesMultipleDirectoryBatches(t *testing.T) {
	memRoot := t.TempDir()
	long := time.Now().Add(-7 * 24 * time.Hour)
	total := sessionRetentionReadDirBatch + 19
	for i := 0; i < total; i++ {
		id := fmt.Sprintf("stale-%03d", i)
		dir := filepath.Join(memRoot, id)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		conv := filepath.Join(dir, "conversation.jsonl")
		if err := os.WriteFile(conv, []byte("[]\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(conv, long, long); err != nil {
			t.Fatal(err)
		}
	}
	cfg := Config{
		Listen:           "127.0.0.1:0",
		MaxSessions:      4,
		SessionIdleTTL:   "5m",
		SessionRetention: "24h",
		WorkspaceRoot:    t.TempDir(),
		MemoryRoot:       memRoot,
		BaseURL:          "http://127.0.0.1:0",
		APIKey:           "test",
		Model:            "fake",
	}
	pool, err := NewSessionPool(cfg, zerolog.New(io.Discard))
	if err != nil {
		t.Fatalf("NewSessionPool: %v", err)
	}
	t.Cleanup(pool.Shutdown)

	pool.sweepRetentionOnce()

	entries, err := os.ReadDir(memRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("remaining session dirs = %d, want 0", len(entries))
	}
}

// TestSessionPool_RetentionDisabledByDefault pins that without a
// SessionRetention config, no sweep goroutine runs and stale dirs
// stick around — the "long-running memory survives forever" promise
// the package made before retention existed.
func TestSessionPool_RetentionDisabledByDefault(t *testing.T) {
	memRoot := t.TempDir()
	dir := filepath.Join(memRoot, "ancient")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "conversation.jsonl"), []byte("[]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	long := time.Now().Add(-365 * 24 * time.Hour)
	_ = os.Chtimes(filepath.Join(dir, "conversation.jsonl"), long, long)

	cfg := Config{
		Listen:         "127.0.0.1:0",
		MaxSessions:    4,
		SessionIdleTTL: "5m",
		// SessionRetention intentionally left empty.
		WorkspaceRoot: t.TempDir(),
		MemoryRoot:    memRoot,
		BaseURL:       "http://127.0.0.1:0",
		APIKey:        "test",
		Model:         "fake",
	}
	pool, err := NewSessionPool(cfg, zerolog.New(io.Discard))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Shutdown)

	pool.sweepRetentionOnce() // no-op since retention == 0
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("retention disabled must NOT delete anything; ancient dir gone: %v", err)
	}
	if pool.retentionStop != nil {
		t.Errorf("retention sweep goroutine must not be started when SessionRetention is empty")
	}
}

// TestSessionPool_AttachesRollingCompactor pins that every session
// gets an LLMSummaryCompactor wired up. Without it the loop's
// runStep treats upstream context-overflow errors as non-retryable
// (the IsContextOverflow branch is gated on l.Compactor != nil), so
// a session that's been chatting long enough to outgrow the model's
// window dies at the boundary with no recovery. affentctl already
// always-attaches; affentserve should do the same.
func TestSessionPool_AttachesRollingCompactor(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	s, err := pool.GetOrCreate("compactor-check")
	if err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	if s.loop.Compactor == nil {
		t.Fatal("loop.Compactor must be non-nil so context overflow can recover")
	}
	if _, ok := s.loop.Compactor.(*agent.LLMSummaryCompactor); !ok {
		t.Errorf("expected *agent.LLMSummaryCompactor, got %T", s.loop.Compactor)
	}
}

// TestSessionPool_CompactorRespectsConfigOverrides pins that
// non-zero CompactTrigger / CompactKeepLast in the config actually
// reach the attached LLMSummaryCompactor. Without them an operator
// running a small-context model has no way to compact earlier —
// the compactor stays on the 240/10 defaults forever.
func TestSessionPool_CompactorRespectsConfigOverrides(t *testing.T) {
	cfg := Config{
		Listen:          "127.0.0.1:0",
		MaxSessions:     4,
		SessionIdleTTL:  "5m",
		WorkspaceRoot:   t.TempDir(),
		BaseURL:         "http://127.0.0.1:0",
		APIKey:          "test",
		Model:           "fake",
		CompactTrigger:  120,
		CompactKeepLast: 4,
	}
	pool, err := NewSessionPool(cfg, zerolog.New(io.Discard))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Shutdown)

	s, err := pool.GetOrCreate("compact-cfg")
	if err != nil {
		t.Fatal(err)
	}
	lc, ok := s.loop.Compactor.(*agent.LLMSummaryCompactor)
	if !ok {
		t.Fatalf("compactor type = %T, want *LLMSummaryCompactor", s.loop.Compactor)
	}
	if lc.TriggerMsgs != 120 {
		t.Errorf("TriggerMsgs = %d, want 120 from config", lc.TriggerMsgs)
	}
	if lc.KeepLast != 4 {
		t.Errorf("KeepLast = %d, want 4 from config", lc.KeepLast)
	}
}

// TestSessionPool_CompactorFallsBackToDefaults pins the zero-value
// behavior: leaving the knobs at 0 means "use the agent defaults",
// matching the precedent set by affentctl.
func TestSessionPool_CompactorFallsBackToDefaults(t *testing.T) {
	pool := newTestPool(t, 4, "5m") // no compact overrides
	s, _ := pool.GetOrCreate("compact-default")
	lc := s.loop.Compactor.(*agent.LLMSummaryCompactor)
	if lc.TriggerMsgs != agent.DefaultSummaryTriggerMsgs {
		t.Errorf("TriggerMsgs = %d, want default %d", lc.TriggerMsgs, agent.DefaultSummaryTriggerMsgs)
	}
	if lc.KeepLast != agent.DefaultSummaryKeepLast {
		t.Errorf("KeepLast = %d, want default %d", lc.KeepLast, agent.DefaultSummaryKeepLast)
	}
}

func TestSessionPool_FocusedTasksRegisterWhenEnabled(t *testing.T) {
	cfg := Config{
		Listen:             "127.0.0.1:0",
		MaxSessions:        4,
		SessionIdleTTL:     "5m",
		WorkspaceRoot:      t.TempDir(),
		BaseURL:            "http://127.0.0.1:0",
		APIKey:             "test",
		Model:              "fake",
		EnableSubagent:     false,
		EnableFocusedTasks: true,
	}
	pool, err := NewSessionPool(cfg, zerolog.New(io.Discard))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Shutdown)

	s, err := pool.GetOrCreate("focused-on")
	if err != nil {
		t.Fatal(err)
	}
	tool, ok := s.registry.Get(agent.FocusedTaskToolName)
	if !ok {
		t.Fatal("run_task should be registered when EnableFocusedTasks is true")
	}
	if strings.Contains(string(tool.Schema), `"research"`) {
		t.Fatalf("research should be filtered out when web is disabled:\n%s", string(tool.Schema))
	}
	msgs := s.conv.Snapshot()
	if len(msgs) == 0 || !strings.Contains(msgs[0].Content, "Focused tasks (run_task):") {
		t.Fatalf("system prompt should include focused-task guidance, got %+v", msgs)
	}
}

func TestSessionPool_FocusedTasksCanBeDisabled(t *testing.T) {
	cfg := Config{
		Listen:             "127.0.0.1:0",
		MaxSessions:        4,
		SessionIdleTTL:     "5m",
		WorkspaceRoot:      t.TempDir(),
		BaseURL:            "http://127.0.0.1:0",
		APIKey:             "test",
		Model:              "fake",
		EnableFocusedTasks: false,
	}
	pool, err := NewSessionPool(cfg, zerolog.New(io.Discard))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Shutdown)

	s, err := pool.GetOrCreate("focused-off")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s.registry.Get(agent.FocusedTaskToolName); ok {
		t.Fatal("run_task should not be registered when EnableFocusedTasks is false")
	}
	msgs := s.conv.Snapshot()
	if len(msgs) > 0 && strings.Contains(msgs[0].Content, "Focused tasks (run_task):") {
		t.Fatal("system prompt should not include focused-task guidance when disabled")
	}
}

func TestSessionPool_MaxSessionsEvictsLRU(t *testing.T) {
	pool := newTestPool(t, 2, "5m")
	a, _ := pool.GetOrCreate("a")
	if a.ID != "a" {
		t.Fatalf("session a id = %q", a.ID)
	}
	// Touch order: a was used; force b's lastUsed to be later.
	time.Sleep(2 * time.Millisecond)
	b, _ := pool.GetOrCreate("b")
	if b.ID != "b" {
		t.Fatalf("session b id = %q", b.ID)
	}
	// Force a's lastUsed older than b's already.
	time.Sleep(2 * time.Millisecond)
	c, _ := pool.GetOrCreate("c")
	if c.ID != "c" {
		t.Fatalf("session c id = %q", c.ID)
	}
	// Eviction should drop "a".
	_, err := pool.Get("a")
	if err != ErrSessionNotFound {
		t.Fatalf("expected a to be evicted, Get returned %v", err)
	}
}

func TestSessionPool_WorkspaceCleanupOnEvict(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	s, _ := pool.GetOrCreate("ephemeral")
	ws := s.Workspace()
	if _, err := os.Stat(ws); err != nil {
		t.Fatalf("workspace must exist while session is alive: %v", err)
	}
	pool.Delete("ephemeral")
	// Delete spawns Close in a goroutine; wait for the workspace to disappear.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(ws); os.IsNotExist(err) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("workspace was not cleaned up: %s", ws)
}
