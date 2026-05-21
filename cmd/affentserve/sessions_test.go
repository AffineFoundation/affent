package main

import (
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/affinefoundation/affent/internal/agent"
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
	if _, err := sa.loop.Memory.Add(agent.TargetUser, "", "alpha-only fact"); err != nil {
		t.Fatalf("alpha Add: %v", err)
	}
	if _, err := sb.loop.Memory.Add(agent.TargetUser, "", "beta-only fact"); err != nil {
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
