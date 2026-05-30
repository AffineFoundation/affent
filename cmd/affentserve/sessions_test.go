package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/affinefoundation/affent/internal/agent"
	"github.com/affinefoundation/affent/internal/loopstate"
	"github.com/affinefoundation/affent/internal/memory"
	"github.com/affinefoundation/affent/internal/sessionstate"
	"github.com/affinefoundation/affent/internal/sse"
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

func TestSessionPoolLoopRequestsFinalNoToolAnswerOnMaxTurns(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	s, err := pool.GetOrCreate("max-turn-summary")
	if err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	if !s.loop.FinalNoToolsOnMaxTurns {
		t.Fatal("affentserve sessions should request a final no-tool answer when max turns are exhausted")
	}
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

func TestSessionPoolBuildSessionWritesMetadata(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	s, err := pool.GetOrCreate("metadata-session")
	if err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	meta, found, err := sessionstate.ReadMetadata(pool.sessionDirPath("metadata-session"))
	if err != nil {
		t.Fatalf("ReadMetadata: %v", err)
	}
	if !found {
		t.Fatal("session metadata not written")
	}
	if meta.SessionID != "metadata-session" || meta.WorkspacePath != s.workspace {
		t.Fatalf("metadata = %+v, want session id and workspace %q", meta, s.workspace)
	}
}

func TestSessionPool_RestoresAutoCompactWindowFromDurableCompaction(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	pool.cfg.ModelContextWindowTokens = 100_000
	pool.cfg.CompactTriggerInputPercent = 80
	createDurableSessionDir(t, pool, "compact-window-resume")
	dir := pool.sessionDirPath("compact-window-resume")
	body := sessionEventLine(t, sse.TypeContextCompact, sse.ContextCompactPayload{
		TurnID:                             "t1",
		BeforeMessages:                     60,
		AfterMessages:                      18,
		RemovedMessages:                    42,
		Reason:                             "estimated_context_pressure",
		EstimatedInputTokens:               120_000,
		AfterEstimatedInputTokens:          68_000,
		TriggerInputTokens:                 80_000,
		ModelContextWindowTokens:           100_000,
		CompactTriggerInputPercent:         80,
		CompactScopeActive:                 true,
		CompactWindowOrdinal:               3,
		CompactWindowPrefillInputTokens:    72_000,
		CompactWindowPrefillSource:         sse.CompactWindowPrefillSourceServerObserved,
		CompactScopedInputTokens:           0,
		CompactHardInputLimitTokens:        100_000,
		SummaryPresent:                     true,
		SummaryBytes:                       512,
		ModelContextWindowEffectivePercent: 0,
	})
	if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	s, err := pool.GetOrCreate("compact-window-resume")
	if err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	got := s.loop.AutoCompactWindowState()
	if got.Ordinal != 3 || got.PrefillInputTokens != 72_000 || !got.Observed {
		t.Fatalf("auto compact window = %+v, want durable observed compaction scope", got)
	}
}

func TestSessionPool_RestoresObservedAutoCompactWindowFromRuntimeSurface(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	pool.cfg.ModelContextWindowTokens = 100_000
	pool.cfg.CompactTriggerInputPercent = 80
	createDurableSessionDir(t, pool, "compact-window-observed")
	dir := pool.sessionDirPath("compact-window-observed")
	body := sessionEventLine(t, sse.TypeContextCompact, sse.ContextCompactPayload{
		TurnID:                          "t1",
		Reason:                          "estimated_context_pressure",
		EstimatedInputTokens:            120_000,
		AfterEstimatedInputTokens:       68_000,
		TriggerInputTokens:              80_000,
		ModelContextWindowTokens:        100_000,
		CompactTriggerInputPercent:      80,
		CompactScopeActive:              true,
		CompactWindowOrdinal:            3,
		CompactWindowPrefillInputTokens: 68_000,
		CompactWindowPrefillSource:      sse.CompactWindowPrefillSourceEstimated,
		CompactHardInputLimitTokens:     100_000,
		SummaryPresent:                  true,
		SummaryBytes:                    512,
	}) + sessionEventLine(t, sse.TypeRuntimeSurface, sse.RuntimeSurfacePayload{
		TurnID:                          "t1",
		RefreshReason:                   sse.RuntimeSurfaceRefreshCompactWindowObserved,
		ModelContextWindowTokens:        100_000,
		ReservedOutputTokens:            30_000,
		CompactTriggerInputTokens:       80_000,
		CompactTriggerInputPercent:      80,
		CompactScopeActive:              true,
		CompactWindowOrdinal:            3,
		CompactWindowPrefillInputTokens: 72_000,
		CompactWindowPrefillSource:      sse.CompactWindowPrefillSourceServerObserved,
		CompactHardInputLimitTokens:     100_000,
	})
	if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	s, err := pool.GetOrCreate("compact-window-observed")
	if err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	got := s.loop.AutoCompactWindowState()
	if got.Ordinal != 3 || got.PrefillInputTokens != 72_000 || !got.Observed {
		t.Fatalf("auto compact window = %+v, want latest observed runtime surface state", got)
	}
	runtime := s.RuntimeStatsSnapshot()
	if runtime.ContextCompactions != 1 ||
		runtime.RuntimeSurfaceRefreshByReason[sse.RuntimeSurfaceRefreshCompactWindowObserved] != 1 ||
		runtime.RuntimeSurfaceLatestRefreshReason != sse.RuntimeSurfaceRefreshCompactWindowObserved ||
		runtime.ContextCompactionLatestCompactWindowPrefill != 72_000 ||
		runtime.ContextCompactionLatestCompactWindowPrefillSource != sse.CompactWindowPrefillSourceServerObserved {
		t.Fatalf("runtime stats = %+v, want compaction count with observed compact window", runtime)
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

func TestSessionPool_GCSkipsActiveTurns(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	s, err := pool.GetOrCreate("busy")
	if err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	s.mu.Lock()
	s.lastUsed = time.Now().Add(-time.Hour)
	s.mu.Unlock()
	s.activeTurns.Store(1)

	pool.gcOnce()
	if _, err := pool.Get("busy"); err != nil {
		t.Fatalf("active turn must not be idle-GC'd: %v", err)
	}

	s.activeTurns.Store(0)
	s.mu.Lock()
	s.lastUsed = time.Now().Add(-time.Hour)
	s.mu.Unlock()
	pool.gcOnce()
	if _, err := pool.Get("busy"); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("inactive stale session should be evicted, got %v", err)
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

func TestSessionPool_allocWorkspaceUsesConfiguredRootWithoutOwningIt(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	memDir, err := pool.allocSessionDir("alpha")
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := pool.allocWorkspace("alpha")
	if err != nil {
		t.Fatal(err)
	}
	if workspace.Path != pool.cfg.WorkspaceRoot || workspace.Owned {
		t.Fatalf("workspace allocation = %+v, want configured root %q without session ownership", workspace, pool.cfg.WorkspaceRoot)
	}
	if memDir != filepath.Join(pool.cfg.WorkspaceRoot, ".affent", "session-state", "alpha") {
		t.Fatalf("session state dir = %q, want hidden state under workspace root", memDir)
	}
	s, err := pool.GetOrCreate("alpha")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(workspace.Path); err != nil || !info.IsDir() {
		t.Fatalf("closing a session must not remove configured workspace root %q: info=%v err=%v", workspace.Path, info, err)
	}
}

func TestSessionPool_allocSessionDirRejectsSymlinkLeaf(t *testing.T) {
	memRoot := t.TempDir()
	pool := newPoolWithMemoryRoot(t, memRoot)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(memRoot, "linked")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	if _, err := pool.allocSessionDir("linked"); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("allocSessionDir symlink error = %v, want symlink", err)
	}
	if entries, err := os.ReadDir(outside); err != nil {
		t.Fatal(err)
	} else if len(entries) != 0 {
		t.Fatalf("outside target should not be populated, got %v", entries)
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

func TestSessionPool_UserMemoryCanBeSharedAcrossSessions(t *testing.T) {
	cfg := Config{
		Listen:           "127.0.0.1:0",
		MaxSessions:      4,
		SessionIdleTTL:   "5m",
		WorkspaceRoot:    t.TempDir(),
		MemoryRoot:       t.TempDir(),
		EnableMemory:     true,
		SharedUserMemory: true,
		BaseURL:          "http://127.0.0.1:0",
		APIKey:           "test",
		Model:            "fake",
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
	if _, err := sa.loop.Memory.Add(memory.TargetUser, "", "shared preference marker"); err != nil {
		t.Fatalf("alpha Add: %v", err)
	}

	snapB := sb.loop.Memory.Snapshot()
	if !strings.Contains(snapB, "shared preference marker") {
		t.Fatalf("beta should see shared target=user memory:\n%s", snapB)
	}
	if _, err := os.Stat(pool.sharedUserMemoryPath()); err != nil {
		t.Fatalf("shared USER.md missing: %v", err)
	}
	if durableStatePathExists(filepath.Join(pool.sessionDirPath("alpha"), "USER.md")) ||
		durableStatePathExists(filepath.Join(pool.sessionDirPath("beta"), "USER.md")) {
		t.Fatal("shared user memory should not create per-session USER.md files")
	}
}

func TestSessionPool_SharedUserMemoryReservesUserFileSessionID(t *testing.T) {
	cfg := Config{
		Listen:           "127.0.0.1:0",
		MaxSessions:      4,
		SessionIdleTTL:   "5m",
		WorkspaceRoot:    t.TempDir(),
		MemoryRoot:       t.TempDir(),
		EnableMemory:     true,
		SharedUserMemory: true,
		BaseURL:          "http://127.0.0.1:0",
		APIKey:           "test",
		Model:            "fake",
	}
	pool, err := NewSessionPool(cfg, zerolog.New(io.Discard))
	if err != nil {
		t.Fatalf("NewSessionPool: %v", err)
	}
	t.Cleanup(pool.Shutdown)
	if err := os.WriteFile(pool.sharedUserMemoryPath(), []byte("- keep me\n"), 0o644); err != nil {
		t.Fatalf("write shared user memory: %v", err)
	}

	if _, err := pool.GetOrCreate(sharedUserMemoryFileName); err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("GetOrCreate reserved shared user memory id err=%v, want reserved", err)
	}
	if pool.Delete(sharedUserMemoryFileName) {
		t.Fatal("Delete should not report an evicted session for the reserved shared user memory id")
	}
	if raw, err := os.ReadFile(pool.sharedUserMemoryPath()); err != nil || !strings.Contains(string(raw), "keep me") {
		t.Fatalf("shared user memory should survive reserved-id delete: raw=%q err=%v", string(raw), err)
	}
}

// TestSessionPool_ConversationLogIsDurable pins that the JSONL chat
// log survives session eviction + recreation under the same id.
// Without this, the chat handler's design assumption — "we only
// forward the last user message; the rest of the history lives in
// the agent runtime's Conversation log keyed by session_id" — breaks
// on every server restart or LRU revive if the conversation log is tied to
// volatile workspace state, so the model wakes up with no prior context even
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

func TestSessionPool_RegistersSessionSearchWithoutShellBuiltins(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	pool.cfg.EnableBuiltins = false

	past, err := pool.GetOrCreate("past-research")
	if err != nil {
		t.Fatalf("GetOrCreate past: %v", err)
	}
	if err := past.conv.Append(agent.ChatMessage{Role: "user", Content: "investigate taostats subnet metrics"}); err != nil {
		t.Fatalf("append past user: %v", err)
	}
	if err := past.conv.Append(agent.ChatMessage{Role: "assistant", Content: "decision: use browser_network_read before citing hidden market cap values"}); err != nil {
		t.Fatalf("append past assistant: %v", err)
	}

	current, err := pool.GetOrCreate("current-research")
	if err != nil {
		t.Fatalf("GetOrCreate current: %v", err)
	}
	if err := current.conv.Append(agent.ChatMessage{Role: "user", Content: "browser_network_read should not match current session"}); err != nil {
		t.Fatalf("append current: %v", err)
	}
	tool, ok := current.registry.Get(agent.SessionSearchToolName)
	if !ok {
		t.Fatal("workflow tools should register session_search for durable session recall")
	}
	if _, ok := current.registry.Get("shell"); ok {
		t.Fatal("session_search should not require shell builtins")
	}
	msgs := current.conv.Snapshot()
	if len(msgs) == 0 || !strings.Contains(msgs[0].Content, "Session history retrieval:") {
		t.Fatalf("system prompt should include session search guidance when registered:\n%+v", msgs)
	}
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"query":"browser_network_read market cap","top_k":5}`))
	if err != nil {
		t.Fatalf("session_search execute: %v", err)
	}
	var resp agent.SessionSearchResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("decode session_search response: %v\n%s", err, out)
	}
	if len(resp.Results) == 0 {
		t.Fatalf("expected durable past session hit, got %+v", resp)
	}
	for _, hit := range resp.Results {
		if hit.SessionID == current.ID {
			t.Fatalf("session_search must exclude current session: %+v", resp.Results)
		}
	}
	if resp.Results[0].SessionID != past.ID || !strings.Contains(resp.Results[0].Snippet, "browser_network_read") {
		t.Fatalf("unexpected session_search hit: %+v", resp.Results[0])
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

func TestSessionPool_ReopensConversationWithOversizedLine(t *testing.T) {
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
	sessionID := "oversized-conversation"
	sessionDir := filepath.Join(memRoot, sessionID)
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := strings.Join([]string{
		`{"role":"user","content":"before oversized line"}`,
		`{"role":"assistant","content":"` + strings.Repeat("x", 4*1024*1024+1) + `"}`,
		`{"role":"assistant","content":"after oversized line"}`,
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(sessionDir, "conversation.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	pool, err := NewSessionPool(cfg, zerolog.New(io.Discard))
	if err != nil {
		t.Fatalf("NewSessionPool: %v", err)
	}
	t.Cleanup(pool.Shutdown)
	s, err := pool.GetOrCreate(sessionID)
	if err != nil {
		t.Fatalf("GetOrCreate must skip oversized conversation line while loading: %v", err)
	}
	msgs := s.conv.Snapshot()
	if len(msgs) != 2 || msgs[0].Content != "before oversized line" || msgs[1].Content != "after oversized line" {
		t.Fatalf("conversation snapshot after reopen = %+v, want before/after messages only", msgs)
	}
}

func TestSessionPool_EventLogIsDurable(t *testing.T) {
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
	tracePath := filepath.Join(memRoot, "trace-client", "events.jsonl")

	pool, err := NewSessionPool(cfg, zerolog.New(io.Discard))
	if err != nil {
		t.Fatalf("NewSessionPool first: %v", err)
	}
	s1, err := pool.GetOrCreate("trace-client")
	if err != nil {
		t.Fatalf("GetOrCreate first: %v", err)
	}
	ev1, err := sse.NewEvent(sse.TypeTurnStart, sse.TurnStartPayload{TurnID: "turn-one"})
	if err != nil {
		t.Fatal(err)
	}
	ev1.ID = 1
	s1.events <- ev1
	waitForFileSubstring(t, tracePath, `"turn_id":"turn-one"`)
	pool.Shutdown()

	first, err := os.ReadFile(tracePath)
	if err != nil {
		t.Fatalf("read first event log: %v", err)
	}
	if strings.Count(string(first), `"type":"trace.meta"`) != 1 {
		t.Fatalf("fresh event log should have exactly one trace.meta header:\n%s", string(first))
	}

	pool2, err := NewSessionPool(cfg, zerolog.New(io.Discard))
	if err != nil {
		t.Fatalf("NewSessionPool second: %v", err)
	}
	t.Cleanup(pool2.Shutdown)
	s2, err := pool2.GetOrCreate("trace-client")
	if err != nil {
		t.Fatalf("GetOrCreate second: %v", err)
	}
	ev2, err := sse.NewEvent(sse.TypeUsage, sse.UsagePayload{TurnID: "turn-two", InputTokens: 3, OutputTokens: 4})
	if err != nil {
		t.Fatal(err)
	}
	ev2.ID = 2
	s2.events <- ev2
	waitForFileSubstring(t, tracePath, `"turn_id":"turn-two"`)

	final, err := os.ReadFile(tracePath)
	if err != nil {
		t.Fatalf("read final event log: %v", err)
	}
	if strings.Count(string(final), `"type":"trace.meta"`) != 1 {
		t.Fatalf("resuming a session must append without a second trace.meta header:\n%s", string(final))
	}
	if !strings.Contains(string(final), `"type":"turn.start"`) || !strings.Contains(string(final), `"type":"usage"`) {
		t.Fatalf("event log should preserve events across restart:\n%s", string(final))
	}
}

func TestSessionPool_EventLogReopensWithOversizedLine(t *testing.T) {
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
	sessionID := "trace-large-line"
	sessionDir := filepath.Join(memRoot, sessionID)
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	tracePath := filepath.Join(sessionDir, "events.jsonl")
	body := strings.Join([]string{
		fmt.Sprintf(`{"id":0,"type":"trace.meta","data":{"schema_version":%d}}`, sse.TraceSchemaVersion),
		strings.Repeat("x", maxHistoryLineBytes+1),
		"",
	}, "\n")
	if err := os.WriteFile(tracePath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	pool, err := NewSessionPool(cfg, zerolog.New(io.Discard))
	if err != nil {
		t.Fatalf("NewSessionPool: %v", err)
	}
	t.Cleanup(pool.Shutdown)
	s, err := pool.GetOrCreate(sessionID)
	if err != nil {
		t.Fatalf("GetOrCreate must skip oversized event log line while counting cursors: %v", err)
	}
	ev, err := sse.NewEvent(sse.TypeUsage, sse.UsagePayload{TurnID: "after-large-line", InputTokens: 1})
	if err != nil {
		t.Fatal(err)
	}
	s.events <- ev
	waitForFileSubstring(t, tracePath, `"turn_id":"after-large-line"`)

	resp, err := readSessionHistory(sessionDir, sessionID, -1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Events) != 2 || resp.Events[0].ID != 0 || resp.Events[1].ID != 2 {
		t.Fatalf("history after reopen = %+v, want trace meta at 0 and appended event at 2", resp.Events)
	}
}

func waitForFileSubstring(t *testing.T, path, want string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		data, err := os.ReadFile(path)
		if err == nil && strings.Contains(string(data), want) {
			return
		}
		if time.Now().After(deadline) {
			if err != nil {
				t.Fatalf("timed out waiting for %q in %s; last read error: %v", want, path, err)
			}
			t.Fatalf("timed out waiting for %q in %s; file content:\n%s", want, path, string(data))
		}
		time.Sleep(10 * time.Millisecond)
	}
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
	tool, ok := s1.loop.Tools.Get(agent.SkillToolName)
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
	skillPath := filepath.Join(accountSkillDir(pool), "durable_demo", "SKILL.md")
	if _, err := os.Stat(skillPath); err != nil {
		t.Fatalf("runtime skill should be stored in durable account dir: %v", err)
	}
	if strings.HasPrefix(skillPath, s1.workspace) {
		t.Fatalf("runtime skill path %q must not live under active workspace %q", skillPath, s1.workspace)
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

func TestSessionPool_RetentionSweepIgnoresSymlinkConversationMtime(t *testing.T) {
	memRoot := t.TempDir()
	dir := filepath.Join(memRoot, "linked-conversation")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.jsonl")
	if err := os.WriteFile(outside, []byte("[]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-7 * 24 * time.Hour)
	if err := os.Chtimes(outside, old, old); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "conversation.jsonl")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
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

	if _, err := os.Lstat(dir); err != nil {
		t.Fatalf("session dir with symlink conversation should be kept by fresh dir mtime: %v", err)
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
		Listen:                             "127.0.0.1:0",
		MaxSessions:                        4,
		SessionIdleTTL:                     "5m",
		WorkspaceRoot:                      t.TempDir(),
		BaseURL:                            "http://127.0.0.1:0",
		APIKey:                             "test",
		Model:                              "fake",
		CompactTrigger:                     120,
		CompactTriggerInputTokens:          4096,
		ModelContextWindowTokens:           100000,
		ModelContextWindowAuto:             true,
		ModelContextWindowSource:           "provider",
		ModelContextWindowEffectivePercent: 95,
		CompactTriggerInputPercent:         75,
		CompactKeepLast:                    4,
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
	if lc.TriggerBytes != agent.DefaultSummaryTriggerBytes {
		t.Errorf("TriggerBytes = %d, want default %d", lc.TriggerBytes, agent.DefaultSummaryTriggerBytes)
	}
	if lc.KeepLast != 4 {
		t.Errorf("KeepLast = %d, want 4 from config", lc.KeepLast)
	}
	if s.loop.CompactTriggerInputTokens != 4096 {
		t.Errorf("CompactTriggerInputTokens = %d, want 4096 from config", s.loop.CompactTriggerInputTokens)
	}
	if s.loop.ModelContextWindowTokens != 100000 {
		t.Errorf("ModelContextWindowTokens = %d, want 100000 from config", s.loop.ModelContextWindowTokens)
	}
	if !s.loop.ModelContextWindowAuto {
		t.Error("ModelContextWindowAuto = false, want true from config")
	}
	if s.loop.ModelContextWindowSource != "provider" {
		t.Errorf("ModelContextWindowSource = %q, want provider from config", s.loop.ModelContextWindowSource)
	}
	if s.loop.ModelContextWindowEffectivePercent != 95 {
		t.Errorf("ModelContextWindowEffectivePercent = %d, want 95 from config", s.loop.ModelContextWindowEffectivePercent)
	}
	if s.loop.CompactTriggerInputPercent != 75 {
		t.Errorf("CompactTriggerInputPercent = %d, want 75 from config", s.loop.CompactTriggerInputPercent)
	}
}

func TestSessionPool_CompactorDerivesByteTriggerFromModelContextWindow(t *testing.T) {
	cfg := Config{
		Listen:                     "127.0.0.1:0",
		MaxSessions:                4,
		SessionIdleTTL:             "5m",
		WorkspaceRoot:              t.TempDir(),
		BaseURL:                    "http://127.0.0.1:0",
		APIKey:                     "test",
		Model:                      "fake",
		ModelContextWindowTokens:   100000,
		CompactTriggerInputPercent: 80,
	}
	pool, err := NewSessionPool(cfg, zerolog.New(io.Discard))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Shutdown)

	s, err := pool.GetOrCreate("compact-window-policy")
	if err != nil {
		t.Fatal(err)
	}
	lc, ok := s.loop.Compactor.(*agent.LLMSummaryCompactor)
	if !ok {
		t.Fatalf("compactor type = %T, want *LLMSummaryCompactor", s.loop.Compactor)
	}
	if got := s.loop.CompactTriggerInputTokens; got != 0 {
		t.Fatalf("explicit CompactTriggerInputTokens = %d, want 0", got)
	}
	if got := lc.TriggerBytes; got != 320000 {
		t.Fatalf("TriggerBytes = %d, want 320000", got)
	}
	if got := lc.MaxPromptBytes; got != agent.DefaultSummaryPromptMaxBytes {
		t.Fatalf("MaxPromptBytes = %d, want default %d for large model window", got, agent.DefaultSummaryPromptMaxBytes)
	}
}

func TestSessionPool_CompactorReservesOutputBudgetInModelWindowByteTrigger(t *testing.T) {
	maxTokens := 30_000
	cfg := Config{
		Listen:                     "127.0.0.1:0",
		MaxSessions:                4,
		SessionIdleTTL:             "5m",
		WorkspaceRoot:              t.TempDir(),
		BaseURL:                    "http://127.0.0.1:0",
		APIKey:                     "test",
		Model:                      "fake",
		MaxTokens:                  &maxTokens,
		ModelContextWindowTokens:   100000,
		CompactTriggerInputPercent: 80,
	}
	pool, err := NewSessionPool(cfg, zerolog.New(io.Discard))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Shutdown)

	s, err := pool.GetOrCreate("compact-window-output-reserve")
	if err != nil {
		t.Fatal(err)
	}
	lc, ok := s.loop.Compactor.(*agent.LLMSummaryCompactor)
	if !ok {
		t.Fatalf("compactor type = %T, want *agent.LLMSummaryCompactor", s.loop.Compactor)
	}
	if got := lc.TriggerBytes; got != 224000 {
		t.Fatalf("TriggerBytes = %d, want 224000", got)
	}
	if got := s.loop.CompactTriggerInputTokens; got != 0 {
		t.Fatalf("explicit CompactTriggerInputTokens = %d, want 0", got)
	}
	if got := lc.MaxPromptBytes; got != agent.DefaultSummaryPromptMaxBytes {
		t.Fatalf("MaxPromptBytes = %d, want default %d for large model window", got, agent.DefaultSummaryPromptMaxBytes)
	}
}

func TestSessionPool_CompactorShrinksSummaryPromptForSmallModelWindow(t *testing.T) {
	cfg := Config{
		Listen:                     "127.0.0.1:0",
		MaxSessions:                4,
		SessionIdleTTL:             "5m",
		WorkspaceRoot:              t.TempDir(),
		BaseURL:                    "http://127.0.0.1:0",
		APIKey:                     "test",
		Model:                      "fake",
		ModelContextWindowTokens:   200,
		CompactTriggerInputPercent: 80,
	}
	pool, err := NewSessionPool(cfg, zerolog.New(io.Discard))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Shutdown)

	s, err := pool.GetOrCreate("compact-small-window")
	if err != nil {
		t.Fatal(err)
	}
	lc, ok := s.loop.Compactor.(*agent.LLMSummaryCompactor)
	if !ok {
		t.Fatalf("compactor type = %T, want *agent.LLMSummaryCompactor", s.loop.Compactor)
	}
	if got := lc.TriggerBytes; got != 640 {
		t.Fatalf("TriggerBytes = %d, want 640", got)
	}
	if got := lc.MaxPromptBytes; got != 640 {
		t.Fatalf("MaxPromptBytes = %d, want 640", got)
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
	if s.loop.CompactTriggerInputTokens != 0 {
		t.Errorf("CompactTriggerInputTokens = %d, want 0 to use runtime default", s.loop.CompactTriggerInputTokens)
	}
	if lc.TriggerBytes != agent.DefaultSummaryTriggerBytes {
		t.Errorf("TriggerBytes = %d, want default %d", lc.TriggerBytes, agent.DefaultSummaryTriggerBytes)
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

func TestSessionPool_NoBuiltinsUsesCapabilityMatchedSystemPrompt(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	pool.cfg.EnableBuiltins = false
	pool.cfg.EnableMemory = true

	s, err := pool.GetOrCreate("memory-only-prompt")
	if err != nil {
		t.Fatal(err)
	}
	msgs := s.conv.Snapshot()
	if len(msgs) == 0 {
		t.Fatal("system prompt missing")
	}
	prompt := msgs[0].Content
	if !strings.Contains(prompt, "limited-tool runtime") {
		t.Fatalf("tool-light session should use limited-tool prompt:\n%s", prompt)
	}
	if !strings.Contains(prompt, "Memory retrieval:") {
		t.Fatalf("tool-light session should include memory retrieval guidance:\n%s", prompt)
	}
	if !strings.Contains(prompt, "Session history retrieval:") {
		t.Fatalf("tool-light session should include session history guidance:\n%s", prompt)
	}
	for _, forbidden := range []string{"'shell' tool", "read_file", "write_file", "edit_file", "list_files"} {
		if strings.Contains(prompt, forbidden) {
			t.Fatalf("tool-light prompt should not mention unavailable %q:\n%s", forbidden, prompt)
		}
	}
}

func TestSessionPool_ToolLightMixedSurfaceUsesLimitedPrompt(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	pool.cfg.EnableBuiltins = false
	pool.cfg.EnableMemory = true
	pool.cfg.EnableWeb = true

	s, err := pool.GetOrCreate("tool-light-prompt")
	if err != nil {
		t.Fatal(err)
	}
	msgs := s.conv.Snapshot()
	if len(msgs) == 0 {
		t.Fatal("system prompt missing")
	}
	prompt := msgs[0].Content
	if !strings.Contains(prompt, "limited-tool runtime") {
		t.Fatalf("mixed non-builtin session should use limited-tool prompt:\n%s", prompt)
	}
	if !strings.Contains(prompt, "Runtime context:") || !strings.Contains(prompt, "Current UTC date:") {
		t.Fatalf("limited prompt should include runtime date context:\n%s", prompt)
	}
	if !strings.Contains(prompt, "Memory retrieval:") {
		t.Fatalf("memory-enabled limited prompt should include memory retrieval guidance:\n%s", prompt)
	}
	for _, forbidden := range []string{"'shell' tool", "read_file", "write_file", "edit_file", "list_files"} {
		if strings.Contains(prompt, forbidden) {
			t.Fatalf("limited prompt should not mention unavailable %q:\n%s", forbidden, prompt)
		}
	}
}

func TestSessionPoolPromptIncludesScheduleGovernanceAndRuntimeTime(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	pool.cfg.EnableLoopProtocol = true

	s, err := pool.GetOrCreate("schedule-governance-prompt")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s.registry.Get(agent.SessionScheduleToolName); !ok {
		t.Fatal("session_schedule tool should be available")
	}
	if _, ok := s.registry.Get(agent.LoopProtocolToolName); !ok {
		t.Fatal("loop_protocol tool should be available")
	}
	msgs := s.conv.Snapshot()
	if len(msgs) == 0 {
		t.Fatal("system prompt missing")
	}
	prompt := msgs[0].Content
	for _, want := range []string{
		"Runtime context:",
		"Current UTC date:",
		"Current UTC time:",
		"relative timer and reminder calculations",
		"Loop protocol maintenance:",
		"Session scheduling:",
		"session_schedule for future turns, reminders, timers, recurring checks, and scheduled follow-ups",
		"does not require LOOP.md",
		"loop_protocol only for durable long-running task state",
		"action=start_setup is for explicit loop_setup mode",
		"RFC3339 next_run_at",
		"repeat_interval_seconds",
		"kind=loop_tick",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("system prompt missing %q:\n%s", want, prompt)
		}
	}
	if strings.Index(prompt, "Session scheduling:") < strings.Index(prompt, "Loop protocol maintenance:") {
		t.Fatalf("schedule guidance should follow loop guidance in the serve prompt:\n%s", prompt)
	}
}

func TestSessionPool_SubagentRegistersExternalResearchPolicy(t *testing.T) {
	cfg := Config{
		Listen:         "127.0.0.1:0",
		MaxSessions:    4,
		SessionIdleTTL: "5m",
		WorkspaceRoot:  t.TempDir(),
		BaseURL:        "http://127.0.0.1:0",
		APIKey:         "test",
		Model:          "fake",
		EnableSubagent: true,
	}
	pool, err := NewSessionPool(cfg, zerolog.New(io.Discard))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Shutdown)

	s, err := pool.GetOrCreate("subagent-policy")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, policy := range s.loop.ToolCallPolicies {
		if policy != nil && policy.ToolName == agent.SubagentToolName {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("subagent sessions should install a direct-research tool-call policy: %+v", s.loop.ToolCallPolicies)
	}
}

func TestSessionPool_EvalModeRegistersNoToolsByDefault(t *testing.T) {
	cfg := Config{
		Listen:             "127.0.0.1:0",
		MaxSessions:        4,
		SessionIdleTTL:     "5m",
		WorkspaceRoot:      t.TempDir(),
		BaseURL:            "http://127.0.0.1:0",
		APIKey:             "test",
		Model:              "fake",
		EvalMode:           true,
		EnableSubagent:     true,
		EnableFocusedTasks: true,
		EnableWeb:          true,
	}
	pool, err := NewSessionPool(cfg, zerolog.New(io.Discard))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Shutdown)

	s, err := pool.GetOrCreate("eval-basic")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"shell", "read_file", "write_file", "edit_file", "list_files"} {
		if _, ok := s.registry.Get(name); ok {
			t.Fatalf("%s should not be registered by default in eval mode", name)
		}
	}
	for _, name := range []string{
		agent.SkillToolName,
		agent.MemoryToolName,
		agent.PlanToolName,
		agent.SessionSearchToolName,
		agent.SubagentToolName,
		agent.FocusedTaskToolName,
		"web_fetch",
		"web_search",
		"browser_navigate",
		"browser_snapshot",
		"browser_find",
		"browser_network",
		"browser_network_read",
	} {
		if _, ok := s.registry.Get(name); ok {
			t.Fatalf("%s should not be registered in eval mode", name)
		}
	}
	pool.cfg.EnableMemory = true
	pool.cfg.enableMemorySet = true
	sWithMemory, err := pool.buildSession("eval-basic-memory")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sWithMemory.Close() }()
	if _, ok := sWithMemory.registry.Get(agent.MemoryToolName); !ok {
		t.Fatal("explicit memory should be registered in eval mode")
	}
	memoryMsgs := sWithMemory.conv.Snapshot()
	if len(memoryMsgs) == 0 || !strings.Contains(memoryMsgs[0].Content, "Memory retrieval:") {
		t.Fatalf("explicit-memory eval prompt should include memory guidance: %+v", memoryMsgs)
	}
	if s.loop.SkillProvider != nil {
		t.Fatal("eval mode should disable active skill/provider injection")
	}
	pool.cfg.EnableWeb = true
	pool.cfg.enableWebSet = true
	pool.cfg.EnableWebSearch = false
	pool.cfg.enableWebSearchSet = false
	sWithWebFetch, err := pool.buildSession("eval-basic-web-fetch")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sWithWebFetch.Close() }()
	if _, ok := sWithWebFetch.registry.Get("web_fetch"); !ok {
		t.Fatal("explicit web should register web_fetch in eval mode")
	}
	if _, ok := sWithWebFetch.registry.Get("web_search"); ok {
		t.Fatal("explicit web without web_search should not register web_search in eval mode")
	}
	webMsgs := sWithWebFetch.conv.Snapshot()
	if len(webMsgs) == 0 || !strings.Contains(webMsgs[0].Content, "External research:") {
		t.Fatalf("explicit-web eval prompt should include external research guidance: %+v", webMsgs)
	}
	if strings.Contains(webMsgs[0].Content, "web_search") {
		t.Fatalf("fetch-only eval prompt should not mention unavailable web_search:\n%s", webMsgs[0].Content)
	}
	if got := agent.BuiltinSkillProvider("请通过浏览器访问 https://example.com 并提取信息"); got == "" {
		t.Fatal("test prompt should trigger the built-in skill provider outside eval mode")
	}
	msgs := s.conv.Snapshot()
	if len(msgs) == 0 {
		t.Fatal("system prompt missing")
	}
	for _, forbidden := range []string{"Subagent delegation:", "Focused tasks (run_task):", "Affent plan tool guidance:", "Memory retrieval:", "Session history retrieval:"} {
		if strings.Contains(msgs[0].Content, forbidden) {
			t.Fatalf("eval-mode system prompt should not include %q guidance:\n%s", forbidden, msgs[0].Content)
		}
	}
}

func TestSessionPool_EvalModeAllowsIndividualToolsAndPromptMatchesRegistry(t *testing.T) {
	cfg := Config{
		Listen:         "127.0.0.1:0",
		MaxSessions:    4,
		SessionIdleTTL: "5m",
		WorkspaceRoot:  t.TempDir(),
		BaseURL:        "http://127.0.0.1:0",
		APIKey:         "test",
		Model:          "fake",
		EvalMode:       true,
		EvalTools:      "read_file,shell",
	}
	pool, err := NewSessionPool(cfg, zerolog.New(io.Discard))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Shutdown)

	s, err := pool.GetOrCreate("eval-tools")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"read_file", "shell"} {
		if _, ok := s.registry.Get(name); !ok {
			t.Fatalf("%s should be registered when requested by eval_tools", name)
		}
	}
	for _, name := range []string{"write_file", "list_files", agent.MemoryToolName, agent.PlanToolName, agent.SessionSearchToolName, agent.SubagentToolName, agent.FocusedTaskToolName} {
		if _, ok := s.registry.Get(name); ok {
			t.Fatalf("%s should not be registered when absent from eval_tools", name)
		}
	}
	msgs := s.conv.Snapshot()
	if len(msgs) == 0 {
		t.Fatal("system prompt missing")
	}
	if strings.Contains(msgs[0].Content, s.workspace) {
		t.Fatalf("eval-mode workspace prompt should not inject the absolute workspace path:\n%s", msgs[0].Content)
	}
	if strings.Contains(msgs[0].Content, "Use this exact path") {
		t.Fatalf("workspace prompt should not steer agents toward absolute paths:\n%s", msgs[0].Content)
	}
	for _, want := range []string{"Commands and workspace tools start there by default", "prefer relative paths", "omit cwd"} {
		if !strings.Contains(msgs[0].Content, want) {
			t.Fatalf("workspace prompt missing %q:\n%s", want, msgs[0].Content)
		}
	}
	for _, forbidden := range []string{"Memory retrieval:", "Session history retrieval:", "External research:", "Subagent delegation:", "Focused tasks (run_task):", "Affent plan tool guidance:", "write_file", "run_task"} {
		if strings.Contains(msgs[0].Content, forbidden) {
			t.Fatalf("eval-mode prompt should not include unregistered %q guidance:\n%s", forbidden, msgs[0].Content)
		}
	}
}

func TestSessionPool_EvalModeDoesNotInjectAccountAccessSkill(t *testing.T) {
	cfg := Config{
		Listen:         "127.0.0.1:0",
		MaxSessions:    4,
		SessionIdleTTL: "5m",
		WorkspaceRoot:  t.TempDir(),
		BaseURL:        "http://127.0.0.1:0",
		APIKey:         "test",
		Model:          "fake",
		EvalMode:       true,
		EvalTools:      "read_file,shell",
	}
	pool, err := NewSessionPool(cfg, zerolog.New(io.Discard))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Shutdown)
	if err := setAccountEnv(pool, "GITHUB_TOKEN", "ghp_eval_secret"); err != nil {
		t.Fatalf("set env: %v", err)
	}

	s, err := pool.GetOrCreate("eval-account-access")
	if err != nil {
		t.Fatal(err)
	}
	if s.loop.SkillProvider == nil {
		return
	}
	if got := s.loop.SkillProvider("clone private repo"); strings.Contains(got, "AFFENT ACCOUNT ACCESS") || strings.Contains(got, "GITHUB_TOKEN") {
		t.Fatalf("eval-mode skill provider should not inject account access hints:\n%s", got)
	}
}

func TestSessionPool_EvalModeAllowsSessionSearchWithoutWorkspaceBuiltins(t *testing.T) {
	cfg := Config{
		Listen:         "127.0.0.1:0",
		MaxSessions:    4,
		SessionIdleTTL: "5m",
		WorkspaceRoot:  t.TempDir(),
		BaseURL:        "http://127.0.0.1:0",
		APIKey:         "test",
		Model:          "fake",
		EvalMode:       true,
		EvalTools:      agent.SessionSearchToolName,
	}
	pool, err := NewSessionPool(cfg, zerolog.New(io.Discard))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Shutdown)

	s, err := pool.GetOrCreate("eval-session-search")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s.registry.Get(agent.SessionSearchToolName); !ok {
		t.Fatal("session_search should be registered when requested by eval_tools")
	}
	for _, name := range []string{"shell", "read_file", "write_file", "list_files", agent.MemoryToolName, agent.PlanToolName, agent.SubagentToolName, agent.FocusedTaskToolName} {
		if _, ok := s.registry.Get(name); ok {
			t.Fatalf("%s should not be registered with session_search-only eval_tools", name)
		}
	}
	msgs := s.conv.Snapshot()
	if len(msgs) == 0 || !strings.Contains(msgs[0].Content, "Session history retrieval:") {
		t.Fatalf("session_search-only eval prompt should include session guidance:\n%+v", msgs)
	}
	for _, forbidden := range []string{"Workspace:", "Memory retrieval:", "Affent plan tool guidance:", "Subagent delegation:", "Focused tasks (run_task):"} {
		if strings.Contains(msgs[0].Content, forbidden) {
			t.Fatalf("session_search-only eval prompt should not include %q guidance:\n%s", forbidden, msgs[0].Content)
		}
	}
}

func TestSessionPool_EvalModeRecallGroupRegistersMemoryAndSessionSearch(t *testing.T) {
	cfg := Config{
		Listen:         "127.0.0.1:0",
		MaxSessions:    4,
		SessionIdleTTL: "5m",
		WorkspaceRoot:  t.TempDir(),
		MemoryRoot:     t.TempDir(),
		BaseURL:        "http://127.0.0.1:0",
		APIKey:         "test",
		Model:          "fake",
		EvalMode:       true,
		EvalTools:      "recall",
	}
	pool, err := NewSessionPool(cfg, zerolog.New(io.Discard))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Shutdown)

	s, err := pool.GetOrCreate("eval-recall")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{agent.MemoryToolName, agent.SessionSearchToolName} {
		if _, ok := s.registry.Get(name); !ok {
			t.Fatalf("%s should be registered by recall eval_tools", name)
		}
	}
	for _, name := range []string{"shell", "read_file", "write_file", "list_files", agent.PlanToolName, agent.SubagentToolName, agent.FocusedTaskToolName} {
		if _, ok := s.registry.Get(name); ok {
			t.Fatalf("%s should not be registered with recall-only eval_tools", name)
		}
	}
	msgs := s.conv.Snapshot()
	if len(msgs) == 0 ||
		!strings.Contains(msgs[0].Content, "Memory retrieval:") ||
		!strings.Contains(msgs[0].Content, "Session history retrieval:") {
		t.Fatalf("recall eval prompt should include memory and session guidance:\n%+v", msgs)
	}
}

func TestSessionPool_WebSearchFallsBackToHTMLWithoutBackend(t *testing.T) {
	t.Setenv("AFFENT_WEB_SEARCH_PROVIDER", "")
	t.Setenv("TAVILY_API_KEY", "")
	t.Setenv("GOOGLE_CSE_API_KEY", "")
	t.Setenv("GOOGLE_API_KEY", "")
	t.Setenv("GOOGLE_CSE_ID", "")
	t.Setenv("GOOGLE_SEARCH_ENGINE_ID", "")
	cfg := Config{
		Listen:          "127.0.0.1:0",
		MaxSessions:     4,
		SessionIdleTTL:  "5m",
		WorkspaceRoot:   t.TempDir(),
		BaseURL:         "http://127.0.0.1:0",
		APIKey:          "test",
		Model:           "fake",
		EnableWeb:       true,
		EnableWebSearch: true,
	}
	pool, err := NewSessionPool(cfg, zerolog.New(io.Discard))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Shutdown)

	s, err := pool.GetOrCreate("web-search-no-backend")
	if err != nil {
		t.Fatal(err)
	}
	caps := summarizeActiveCapabilities(s, cfg)
	if caps.WebSearchBackend != "html" {
		t.Fatalf("WebSearchBackend = %q, want html fallback", caps.WebSearchBackend)
	}
	for _, name := range []string{"web_fetch", "web_search"} {
		if _, ok := s.registry.Get(name); !ok {
			t.Fatalf("%s should be registered with the HTML search fallback", name)
		}
	}
}

func TestSessionPool_WebSearchRegistersWhenBackendConfigured(t *testing.T) {
	t.Setenv("TAVILY_API_KEY", "test-key")
	cfg := Config{
		Listen:          "127.0.0.1:0",
		MaxSessions:     4,
		SessionIdleTTL:  "5m",
		WorkspaceRoot:   t.TempDir(),
		BaseURL:         "http://127.0.0.1:0",
		APIKey:          "test",
		Model:           "fake",
		EnableWeb:       true,
		EnableWebSearch: true,
	}
	pool, err := NewSessionPool(cfg, zerolog.New(io.Discard))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Shutdown)

	s, err := pool.GetOrCreate("web-search-backend")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"web_fetch", "web_search"} {
		if _, ok := s.registry.Get(name); !ok {
			t.Fatalf("%s should be registered when web_search backend is configured", name)
		}
	}
}

func TestSessionPool_SkillProviderInjectsActivePlan(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	pool.cfg.EnableBuiltins = true
	s, err := pool.GetOrCreate("planned-skill")
	if err != nil {
		t.Fatal(err)
	}
	planPath := filepath.Join(pool.sessionDirPath("planned-skill"), "plan.json")
	if err := os.WriteFile(planPath, []byte(`{"version":1,"steps":[{"text":"resume serve work","status":"in_progress","evidence":["cmd/affentserve/sessions.go"]}]}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if s.loop.SkillProvider == nil {
		t.Fatal("active plan skill provider should be installed")
	}
	got := s.loop.SkillProvider("continue")
	if !strings.Contains(got, "AFFENT ACTIVE PLAN:") || !strings.Contains(got, "resume serve work") {
		t.Fatalf("active plan should be injected, got %q", got)
	}
	if !strings.Contains(got, "cmd/affentserve/sessions.go") {
		t.Fatalf("active plan evidence missing, got %q", got)
	}
	if len(s.loop.CompletionGuards) == 0 {
		t.Fatal("active plan should install a completion guard")
	}
	if !stringSliceContains(s.loop.CompletionGuardLabels, "active_plan_unfinished") {
		t.Fatalf("completion guard labels = %#v, want active_plan_unfinished", s.loop.CompletionGuardLabels)
	}
	var guard agent.CompletionGuardResult
	for _, completionGuard := range s.loop.CompletionGuards {
		if result := completionGuard(); result.Trigger == "active_plan_unfinished" {
			guard = result
			break
		}
	}
	if !guard.Blocked ||
		guard.Trigger != "active_plan_unfinished" ||
		!strings.Contains(guard.Reason, "plan:0/1:active") ||
		!strings.Contains(guard.Prompt, "AFFENT COMPLETION GUARD:") {
		t.Fatalf("active plan completion guard = %+v", guard)
	}
}

func TestSessionPool_SkillProviderInjectsLoopProtocolWhenPresent(t *testing.T) {
	memRoot := t.TempDir()
	pool := newPoolWithMemoryRoot(t, memRoot)
	createDurableSessionDir(t, pool, "loop-protocol")
	path := sessionLoopProtocolPath(pool, "loop-protocol")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := loopstate.WriteProtocol(path, "# Loop Protocol\n\n## 0. Metadata\n\n- loop_id: loop-protocol\n- status: running\n\n## 1. North Star\n\nKeep long-run state recoverable."); err != nil {
		t.Fatal(err)
	}

	s, err := pool.GetOrCreate("loop-protocol")
	if err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	if s.loop.LoopProtocolPath != path {
		t.Fatalf("LoopProtocolPath = %q, want %q", s.loop.LoopProtocolPath, path)
	}
	if s.loop.LoopProtocolSkillProvider == nil {
		t.Fatal("loop protocol skill provider should be installed")
	}
	got := s.loop.LoopProtocolSkillProvider("continue")
	for _, want := range []string{
		"AFFENT LOOP PROTOCOL:",
		"Keep long-run state recoverable.",
		"persisted plan state remains authoritative",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("loop protocol skill provider missing %q:\n%s", want, got)
		}
	}
}

func TestSessionPool_LoopProtocolCompletionGuardBlocksOnlyWhenPolicyRequiresClose(t *testing.T) {
	memRoot := t.TempDir()
	pool := newPoolWithMemoryRoot(t, memRoot)
	createDurableSessionDir(t, pool, "loop-guard")
	path := sessionLoopProtocolPath(pool, "loop-guard")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("# Loop Protocol\n\n## 0. Metadata\n\n- loop_id: loop-guard\n- status: running\n\n## 1. North Star\n\nFinish the project."), 0o644); err != nil {
		t.Fatal(err)
	}

	s, err := pool.GetOrCreate("loop-guard")
	if err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	for _, guard := range s.loop.CompletionGuards {
		if res := guard(); res.Blocked {
			t.Fatalf("default running loop should not block per-turn completion: %+v", res)
		}
	}
	if err := os.WriteFile(path, []byte("# Loop Protocol\n\n## 0. Metadata\n\n- loop_id: loop-guard\n- status: running\n- finalization_policy: require_close_before_final\n\n## 1. North Star\n\nFinish the project."), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err = pool.GetOrCreate("loop-guard-policy")
	if err != nil {
		t.Fatalf("GetOrCreate policy: %v", err)
	}
	s.loop.CompletionGuards = append(s.loop.CompletionGuards, agent.LoopProtocolCompletionGuard(path))
	var blocked agent.CompletionGuardResult
	for _, guard := range s.loop.CompletionGuards {
		if res := guard(); res.Blocked {
			blocked = res
			break
		}
	}
	if !blocked.Blocked ||
		blocked.Trigger != "loop_protocol_running" ||
		!strings.Contains(blocked.Reason, "loop-guard") ||
		!strings.Contains(blocked.RequiredAction, "loop_protocol action=close") ||
		!strings.Contains(blocked.Prompt, "Do not leave a running loop behind a final answer") {
		t.Fatalf("loop protocol completion guard = %+v", blocked)
	}

	if _, _, err := loopstate.RecordProtocolStatus(path, "completed", "test completed"); err != nil {
		t.Fatalf("RecordProtocolStatus: %v", err)
	}
	for _, guard := range s.loop.CompletionGuards {
		if res := guard(); res.Blocked {
			t.Fatalf("closed loop should not block completion: %+v", res)
		}
	}
}

func TestSessionPool_InitializesLoopProtocolWhenEnabled(t *testing.T) {
	memRoot := t.TempDir()
	pool := newPoolWithMemoryRoot(t, memRoot)
	pool.cfg.EnableLoopProtocol = true
	s, err := pool.GetOrCreate("loop-init")
	if err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	if err := s.ensureLoopProtocolInitialized("Understand the user's long-run reporting goal."); err != nil {
		t.Fatalf("ensureLoopProtocolInitialized: %v", err)
	}
	path := sessionLoopProtocolPath(pool, "loop-init")
	if s.loop.LoopProtocolPath != path {
		t.Fatalf("LoopProtocolPath = %q, want %q", s.loop.LoopProtocolPath, path)
	}
	content, found, err := loopstate.ReadProtocol(path)
	if err != nil || !found {
		t.Fatalf("ReadProtocol found=%v err=%v", found, err)
	}
	for _, want := range []string{
		"# Loop Protocol: loop-init",
		"- loop_id: loop-init",
		"- status: draft",
		"- workspace: not recorded",
		"Understand the user's long-run reporting goal.",
		"North Star",
		"Evidence And Recovery Index",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("initialized protocol missing %q:\n%s", want, content)
		}
	}
	if strings.Contains(content, s.workspace) {
		t.Fatalf("loop protocol should not persist volatile absolute workspace path:\n%s", content)
	}
	state, found, err := loopstate.ReadState(sessionLoopStatePath(pool, "loop-init"))
	if err != nil || !found {
		t.Fatalf("ReadState found=%v err=%v", found, err)
	}
	if state.Status != "draft" || state.LastEventType != "loop.protocol_init" || state.ProtocolUpdates != 1 {
		t.Fatalf("state = %+v", state)
	}
	got := s.loop.SkillProvider("continue")
	if strings.Contains(got, "AFFENT LOOP PROTOCOL:") {
		t.Fatalf("draft loop protocol must not be injected as active feed:\n%s", got)
	}
	if _, ok := s.registry.Get(agent.LoopProtocolToolName); !ok {
		t.Fatal("loop_protocol tool should be available to complete draft activation")
	}
	messages := s.conv.Snapshot()
	if len(messages) == 0 {
		t.Fatal("system prompt missing")
	}
	prompt := messages[0].Content
	if !strings.Contains(prompt, "Loop protocol maintenance:") ||
		!strings.Contains(prompt, "explicit runtime mode") ||
		!strings.Contains(prompt, "action=read") ||
		!strings.Contains(prompt, "patch_draft") ||
		!strings.Contains(prompt, "exactly one concise calibration question") ||
		!strings.Contains(prompt, "one focused follow-up in a later turn") ||
		!strings.Contains(prompt, "complete_activation") {
		t.Fatalf("system prompt missing loop protocol guidance:\n%s", prompt)
	}
}

func TestSessionSendUserDoesNotImplicitlyCreateLoopProtocol(t *testing.T) {
	memRoot := t.TempDir()
	pool := newPoolWithMemoryRoot(t, memRoot)
	pool.cfg.EnableLoopProtocol = true
	s, err := pool.GetOrCreate("loop-explicit-start")
	if err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.SendUser(ctx, "ordinary chat that does not request loop"); !errors.Is(err, context.Canceled) {
		t.Fatalf("SendUser canceled err = %v, want context.Canceled", err)
	}
	if _, found, err := loopstate.ReadProtocol(sessionLoopProtocolPath(pool, "loop-explicit-start")); err != nil || found {
		t.Fatalf("ordinary SendUser created LOOP.md found=%v err=%v", found, err)
	}
	if _, found, err := loopstate.ReadState(sessionLoopStatePath(pool, "loop-explicit-start")); err != nil || found {
		t.Fatalf("ordinary SendUser created loop state found=%v err=%v", found, err)
	}
	if _, ok := s.registry.Get(agent.LoopProtocolToolName); !ok {
		t.Fatal("loop_protocol tool should remain available for explicit loop_setup mode")
	}
}

func TestSessionChatLoopStartSetupCreatesDraft(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		switch calls.Add(1) {
		case 1:
			args := `{"action":"start_setup","goal":"Maintain multi-day subnet research with stable recovery context."}`
			fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"role\":\"assistant\",\"tool_calls\":[{\"index\":0,\"id\":\"setup1\",\"type\":\"function\",\"function\":{\"name\":\"loop_protocol\",\"arguments\":%s}}]},\"finish_reason\":\"tool_calls\"}]}\n\n", jsonStringLiteral(args))
		default:
			t.Errorf("unexpected LLM call %d", calls.Load())
			fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"role\":\"assistant\",\"content\":\"Loop is activated and running.\"},\"finish_reason\":\"stop\"}]}\n\n")
		}
	}))
	defer srv.Close()
	cfg := Config{
		Listen:             "127.0.0.1:0",
		MaxSessions:        4,
		SessionIdleTTL:     "5m",
		WorkspaceRoot:      t.TempDir(),
		MemoryRoot:         t.TempDir(),
		BaseURL:            srv.URL,
		APIKey:             "test",
		Model:              "fake",
		EnableLoopProtocol: true,
	}
	pool, err := NewSessionPool(cfg, zerolog.New(io.Discard))
	if err != nil {
		t.Fatalf("NewSessionPool: %v", err)
	}
	t.Cleanup(pool.Shutdown)
	s, err := pool.GetOrCreate("chat-loop-setup")
	if err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	subID, ch := s.Subscribe(16)
	defer s.Unsubscribe(subID)
	turnID, err := s.SendUserWithOptions(context.Background(), "请开启 loop，长期分析 Bittensor 子网并保持恢复上下文。", agent.TurnOptions{
		UserMode:                     agent.UserModeLoopSetup,
		ForceLoopCalibrationQuestion: true,
	})
	if err != nil {
		t.Fatalf("SendUser: %v", err)
	}
	deadline := time.After(10 * time.Second)
	sawSetupToolResult := false
	sawCalibrationQuestion := false
	for {
		select {
		case ev := <-ch:
			switch ev.Type {
			case sse.TypeToolResult:
				var p sse.ToolResultPayload
				if err := json.Unmarshal(ev.Data, &p); err != nil {
					t.Fatalf("decode tool.result: %v", err)
				}
				if p.CallID == "setup1" && strings.Contains(p.Result, "initialized LOOP.md draft status=draft") {
					sawSetupToolResult = true
				}
			case sse.TypeLoopCalibrationRequest:
				var p sse.LoopProtocolCalibrationPayload
				if err := json.Unmarshal(ev.Data, &p); err != nil {
					t.Fatalf("decode loop calibration request: %v", err)
				}
				if p.CalibrationQuestions == 1 &&
					p.ProtocolPath == loopstate.ProtocolRelPath("chat-loop-setup") &&
					strings.Contains(p.LastCalibrationQuestion, "pause or stop") {
					sawCalibrationQuestion = true
				}
			case sse.TypeTurnEnd:
				var p sse.TurnEndPayload
				if err := json.Unmarshal(ev.Data, &p); err != nil {
					t.Fatalf("decode turn.end: %v", err)
				}
				if p.TurnID != turnID {
					continue
				}
				if !sawSetupToolResult {
					t.Fatal("turn ended without successful start_setup tool result")
				}
				if !sawCalibrationQuestion {
					t.Fatal("turn ended without mirrored loop calibration question")
				}
				if got := calls.Load(); got != 1 {
					t.Fatalf("LLM calls = %d, want 1 deterministic runtime calibration turn", got)
				}
				assertChatLoopSetupDraft(t, pool, s)
				return
			}
		case <-deadline:
			t.Fatal("timeout waiting for chat loop setup turn.end")
		}
	}
}

func assertChatLoopSetupDraft(t *testing.T, pool *SessionPool, s *Session) {
	t.Helper()
	protocol, found, err := loopstate.ReadProtocol(sessionLoopProtocolPath(pool, "chat-loop-setup"))
	if err != nil || !found {
		t.Fatalf("ReadProtocol found=%v err=%v", found, err)
	}
	if loopstate.ProtocolStatus(protocol) != "draft" || !strings.Contains(protocol, "Maintain multi-day subnet research") {
		t.Fatalf("chat start_setup protocol:\n%s", protocol)
	}
	state, found, err := loopstate.ReadState(sessionLoopStatePath(pool, "chat-loop-setup"))
	if err != nil || !found {
		t.Fatalf("ReadState found=%v err=%v", found, err)
	}
	if state.Status != "draft" || state.CalibrationQuestions != 1 || state.CalibrationAnswers != 0 || state.ProtocolUpdates != 1 {
		t.Fatalf("chat setup state = %+v", state)
	}
	if !strings.Contains(state.LastCalibrationQuestion, "pause or stop") {
		t.Fatalf("chat setup missing calibration question preview: %+v", state)
	}
	messages := s.conv.Snapshot()
	if len(messages) == 0 || !strings.Contains(messages[len(messages)-1].Content, "pause or stop") {
		t.Fatalf("assistant did not ask calibration question; messages=%+v", messages)
	}
}

func jsonStringLiteral(s string) string {
	raw, _ := json.Marshal(s)
	return string(raw)
}

func TestSessionRecordsLoopProtocolCalibrationAnswerAfterDraftQuestion(t *testing.T) {
	memRoot := t.TempDir()
	pool := newPoolWithMemoryRoot(t, memRoot)
	pool.cfg.EnableLoopProtocol = true
	s, err := pool.GetOrCreate("loop-calibration")
	if err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	if err := s.ensureLoopProtocolInitialized("Set up long-running subnet analysis."); err != nil {
		t.Fatalf("ensureLoopProtocolInitialized: %v", err)
	}
	s.recordLoopProtocolCalibrationAnswerIfReady("Loop setup prompt", agent.TurnOptions{UserMode: agent.UserModeLoopSetup})
	state, found, err := loopstate.ReadState(sessionLoopStatePath(pool, "loop-calibration"))
	if err != nil || !found {
		t.Fatalf("ReadState found=%v err=%v", found, err)
	}
	if state.CalibrationAnswers != 0 {
		t.Fatalf("synthetic setup prompt recorded calibration state = %+v", state)
	}
	if _, _, err := loopstate.RecordProtocolCalibrationQuestion(s.loopProtocolPath, "What stop condition should pause this loop?"); err != nil {
		t.Fatalf("RecordProtocolCalibrationQuestion: %v", err)
	}
	s.recordLoopProtocolCalibrationAnswerIfReady("Internal loop setup turn", agent.TurnOptions{UserMode: agent.UserModeLoopSetup})
	state, found, err = loopstate.ReadState(sessionLoopStatePath(pool, "loop-calibration"))
	if err != nil || !found {
		t.Fatalf("ReadState after structured setup turn found=%v err=%v", found, err)
	}
	if state.CalibrationAnswers != 0 {
		t.Fatalf("structured loop setup turn recorded calibration state = %+v", state)
	}
	s.recordLoopProtocolCalibrationAnswerIfReady("Pause if source quality is weak or weekly report is complete.", agent.TurnOptions{UserMode: agent.UserModeNormal})
	state, found, err = loopstate.ReadState(sessionLoopStatePath(pool, "loop-calibration"))
	if err != nil || !found {
		t.Fatalf("ReadState after calibration found=%v err=%v", found, err)
	}
	if state.CalibrationQuestions != 1 || state.CalibrationAnswers != 1 || state.LastEventType != "loop.protocol_calibration" || !strings.Contains(state.LastCalibrationQuestion, "stop condition") || !strings.Contains(state.LastCalibrationAnswer, "Pause if source quality") {
		t.Fatalf("calibration state = %+v", state)
	}
	if s.loop.LoopProtocolSkillProvider == nil {
		t.Fatal("draft activation provider should be installed")
	}
	if got := s.loop.LoopProtocolSkillProvider("continue"); !strings.Contains(got, "AFFENT LOOP DRAFT ACTIVATION:") ||
		!strings.Contains(got, "complete_activation without protocol") {
		t.Fatalf("draft activation context = %q", got)
	}
	tracePath := filepath.Join(pool.sessionDirPath("loop-calibration"), "events.jsonl")
	waitForFileSubstring(t, tracePath, `"type":"loop.protocol_calibration"`)
	history, err := readSessionHistory(pool.sessionDirPath("loop-calibration"), "loop-calibration", -1, 100)
	if err != nil {
		t.Fatalf("read history: %v", err)
	}
	var sawCalibration bool
	for _, ev := range history.Events {
		if ev.Type != sse.TypeLoopCalibration {
			continue
		}
		var payload sse.LoopProtocolCalibrationPayload
		if err := json.Unmarshal(ev.Data, &payload); err != nil {
			t.Fatalf("decode calibration payload: %v", err)
		}
		if payload.CalibrationAnswers == 1 &&
			payload.EventSeq == state.EventCount &&
			payload.ProtocolPath == loopstate.ProtocolRelPath("loop-calibration") &&
			strings.Contains(payload.LastCalibrationAnswer, "Pause if source quality") {
			sawCalibration = true
		}
	}
	if !sawCalibration {
		t.Fatalf("history missing loop calibration mirror event: %+v", history.Events)
	}
}

func TestSessionRecordsLoopProtocolAnswerFromPendingCalibrationState(t *testing.T) {
	memRoot := t.TempDir()
	pool := newPoolWithMemoryRoot(t, memRoot)
	pool.cfg.EnableLoopProtocol = true
	s, err := pool.GetOrCreate("loop-calibration-pending-state")
	if err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	if err := s.ensureLoopProtocolInitialized("Build a CLI puzzle game."); err != nil {
		t.Fatalf("ensureLoopProtocolInitialized: %v", err)
	}

	question := "Which implementation language should I use?"
	if _, _, err := loopstate.RecordProtocolCalibrationQuestion(s.loopProtocolPath, question); err != nil {
		t.Fatalf("RecordProtocolCalibrationQuestion: %v", err)
	}
	state, found, err := loopstate.ReadState(sessionLoopStatePath(pool, "loop-calibration-pending-state"))
	if err != nil || !found {
		t.Fatalf("ReadState after pending question found=%v err=%v", found, err)
	}
	if state.CalibrationQuestions != 1 ||
		state.LastEventType != "loop.protocol_calibration_request" ||
		!strings.Contains(state.LastCalibrationQuestion, "implementation language") {
		t.Fatalf("pending calibration question state = %+v", state)
	}
	s.recordLoopProtocolCalibrationAnswerIfReady("Python", agent.TurnOptions{})
	state, found, err = loopstate.ReadState(sessionLoopStatePath(pool, "loop-calibration-pending-state"))
	if err != nil || !found {
		t.Fatalf("ReadState after answer found=%v err=%v", found, err)
	}
	if state.CalibrationQuestions != 1 ||
		state.CalibrationAnswers != 1 ||
		state.LastEventType != "loop.protocol_calibration" ||
		state.LastCalibrationAnswer != "Python" {
		t.Fatalf("pending calibration answer state = %+v", state)
	}
	tracePath := filepath.Join(pool.sessionDirPath("loop-calibration-pending-state"), "events.jsonl")
	waitForFileSubstring(t, tracePath, `"type":"loop.protocol_calibration"`)
}

func TestSessionMirrorsLoopProtocolActivationIntoTrace(t *testing.T) {
	memRoot := t.TempDir()
	pool := newPoolWithMemoryRoot(t, memRoot)
	pool.cfg.EnableLoopProtocol = true
	s, err := pool.GetOrCreate("loop-activation-mirror")
	if err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	if err := s.ensureLoopProtocolInitialized("Mirror loop activation into the session event trace."); err != nil {
		t.Fatalf("ensureLoopProtocolInitialized: %v", err)
	}
	protocol, found, err := loopstate.ReadProtocol(s.loopProtocolPath)
	if err != nil || !found {
		t.Fatalf("ReadProtocol found=%v err=%v", found, err)
	}
	protocol, ok := loopstate.ProtocolWithStatus(protocol, "running")
	if !ok {
		t.Fatalf("ProtocolWithStatus failed")
	}
	if err := loopstate.WriteProtocol(s.loopProtocolPath, protocol); err != nil {
		t.Fatalf("WriteProtocol: %v", err)
	}
	state, activationEvent, err := loopstate.RecordProtocolActivation(s.loopProtocolPath, "test activation")
	if err != nil {
		t.Fatalf("RecordProtocolActivation: %v", err)
	}
	if state.Status != "running" || state.LastEventType != "loop.protocol_activate" {
		t.Fatalf("activation state = %+v", state)
	}

	subID, ch := s.Subscribe(16)
	defer s.Unsubscribe(subID)
	s.events <- mustSSEEvent(t, sse.TypeToolRequest, sse.ToolRequestPayload{
		TurnID: "t1",
		CallID: "lp1",
		Tool:   agent.LoopProtocolToolName,
		Args:   map[string]any{"action": "complete_activation"},
	})
	s.events <- mustSSEEvent(t, sse.TypeToolResult, sse.ToolResultPayload{
		TurnID:        "t1",
		CallID:        "lp1",
		ExitCode:      0,
		ResultSummary: "activated LOOP.md status=running event_seq=2",
		Result:        "activated LOOP.md status=running event_seq=2",
	})

	deadline := time.After(2 * time.Second)
	for {
		select {
		case ev := <-ch:
			if ev.Type != sse.TypeLoopActivation {
				continue
			}
			var payload sse.LoopProtocolActivationPayload
			if err := json.Unmarshal(ev.Data, &payload); err != nil {
				t.Fatalf("decode activation payload: %v", err)
			}
			if payload.TurnID != "t1" ||
				payload.LoopID != "loop-activation-mirror" ||
				payload.Status != "running" ||
				payload.ProtocolPath != loopstate.ProtocolRelPath("loop-activation-mirror") ||
				payload.EventSeq != activationEvent.Seq {
				t.Fatalf("activation payload = %+v, event=%+v", payload, activationEvent)
			}
			waitForFileSubstring(t, filepath.Join(pool.sessionDirPath("loop-activation-mirror"), "events.jsonl"), `"type":"loop.protocol_activate"`)
			return
		case <-deadline:
			t.Fatal("timeout waiting for loop activation event")
		}
	}
}

func TestSessionRecordsLoopProtocolCalibrationAnswerAfterActivationScopeQuestion(t *testing.T) {
	memRoot := t.TempDir()
	pool := newPoolWithMemoryRoot(t, memRoot)
	pool.cfg.EnableLoopProtocol = true
	s, err := pool.GetOrCreate("loop-calibration-activation-scope")
	if err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	if err := s.ensureLoopProtocolInitialized("Set up recurring global situation reporting."); err != nil {
		t.Fatalf("ensureLoopProtocolInitialized: %v", err)
	}
	if _, _, err := loopstate.RecordProtocolCalibrationQuestion(s.loopProtocolPath, "草案已就绪。在激活之前，我需要确认：分析范围和产出频率是什么？"); err != nil {
		t.Fatalf("RecordProtocolCalibrationQuestion: %v", err)
	}
	s.recordLoopProtocolCalibrationAnswerIfReady("全面覆盖；每天更新一次，每周进行一次深度分析。", agent.TurnOptions{})
	state, found, err := loopstate.ReadState(sessionLoopStatePath(pool, "loop-calibration-activation-scope"))
	if err != nil || !found {
		t.Fatalf("ReadState after calibration found=%v err=%v", found, err)
	}
	if state.CalibrationQuestions != 1 ||
		state.CalibrationAnswers != 1 ||
		!strings.Contains(state.LastCalibrationQuestion, "激活之前") ||
		!strings.Contains(state.LastCalibrationAnswer, "全面覆盖") {
		t.Fatalf("calibration state = %+v", state)
	}
}

func TestSessionSkipsLoopProtocolCalibrationWithoutRecentLoopQuestion(t *testing.T) {
	memRoot := t.TempDir()
	pool := newPoolWithMemoryRoot(t, memRoot)
	pool.cfg.EnableLoopProtocol = true
	s, err := pool.GetOrCreate("loop-calibration-strict")
	if err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	if err := s.ensureLoopProtocolInitialized("Set up long-running subnet analysis."); err != nil {
		t.Fatalf("ensureLoopProtocolInitialized: %v", err)
	}
	s.recordLoopProtocolCalibrationAnswerIfReady("Please check whether the subnet page is reachable.", agent.TurnOptions{})
	state, found, err := loopstate.ReadState(sessionLoopStatePath(pool, "loop-calibration-strict"))
	if err != nil || !found {
		t.Fatalf("ReadState found=%v err=%v", found, err)
	}
	if state.CalibrationAnswers != 0 {
		t.Fatalf("generic assistant message recorded calibration state = %+v", state)
	}
	if _, _, err := loopstate.RecordProtocolCalibrationQuestion(s.loopProtocolPath, "什么条件应该暂停这个 loop？"); err != nil {
		t.Fatalf("RecordProtocolCalibrationQuestion: %v", err)
	}
	s.recordLoopProtocolCalibrationAnswerIfReady("当网页证据不足或者用户目标改变时暂停。", agent.TurnOptions{})
	state, found, err = loopstate.ReadState(sessionLoopStatePath(pool, "loop-calibration-strict"))
	if err != nil || !found {
		t.Fatalf("ReadState after calibration found=%v err=%v", found, err)
	}
	if state.CalibrationQuestions != 1 || state.CalibrationAnswers != 1 || !strings.Contains(state.LastCalibrationQuestion, "暂停") || !strings.Contains(state.LastCalibrationAnswer, "网页证据不足") {
		t.Fatalf("calibration state = %+v", state)
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

func TestSessionPool_DeleteDoesNotRemoveConfiguredWorkspaceRoot(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	s, _ := pool.GetOrCreate("shared-workspace")
	ws := s.Workspace()
	if _, err := os.Stat(ws); err != nil {
		t.Fatalf("workspace must exist while session is alive: %v", err)
	}
	if ws != pool.cfg.WorkspaceRoot {
		t.Fatalf("workspace = %q, want configured root %q", ws, pool.cfg.WorkspaceRoot)
	}
	pool.Delete("shared-workspace")
	if info, err := os.Stat(ws); err != nil || !info.IsDir() {
		t.Fatalf("configured workspace root should survive session delete: info=%v err=%v", info, err)
	}
}
