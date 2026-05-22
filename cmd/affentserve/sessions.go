package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	affentbrowser "github.com/affinefoundation/affent/extras/browser"
	affentweb "github.com/affinefoundation/affent/extras/web"
	agent "github.com/affinefoundation/affent/internal/agent"
	"github.com/affinefoundation/affent/internal/executor"
	"github.com/affinefoundation/affent/internal/sse"
	"github.com/google/uuid"
	"github.com/rs/zerolog"
)

// Session is one server-managed agent session: an agent.Loop plus
// its supporting state, plus a fan-out for events going to multiple
// concurrent consumers (chat-completions accumulator + raw SSE
// subscribers).
type Session struct {
	ID string

	mu sync.Mutex

	loop      *agent.Loop
	conv      *agent.Conversation
	llm       *agent.LLMClient
	registry  *agent.Registry
	events    chan sse.Event
	browser   *affentbrowser.Session
	workspace string
	createdAt time.Time
	lastUsed  time.Time

	// fan-out
	subsMu  sync.Mutex
	subs    map[int]chan sse.Event
	nextSub int

	closedCh  chan struct{}
	closeOnce sync.Once
	closeErr  error
}

// SessionPool owns the in-memory session map plus an idle-GC goroutine.
type SessionPool struct {
	cfg    Config
	logger zerolog.Logger

	idleTTL time.Duration

	// browserCache, when non-nil, is the process-wide response cache
	// every new browser session installs. One instance shared across
	// sessions so identical URLs hit the same on-disk entries.
	browserCache affentbrowser.ResponseCache

	mu       sync.Mutex
	sessions map[string]*Session

	// shuttingDown turns GetOrCreate into a fast error after Shutdown
	// starts. Guarded by mu.
	shuttingDown bool

	// gcStop signals the idle-GC goroutine to exit.
	gcStop chan struct{}
	gcDone chan struct{}

	// retention controls the disk-level GC of durable session dirs.
	// Zero = disabled (the empty SessionRetention config). retentionStop
	// / retentionDone gate the sweeper goroutine.
	retention     time.Duration
	retentionStop chan struct{}
	retentionDone chan struct{}

	// shutdownOnce guards Shutdown from racing with itself when both
	// the signal handler and a top-level defer call it.
	shutdownOnce sync.Once
}

// ErrShuttingDown is returned by GetOrCreate after Shutdown has begun.
var ErrShuttingDown = errors.New("session pool is shutting down")

// NewSessionPool constructs a pool with the idle-GC goroutine running.
func NewSessionPool(cfg Config, logger zerolog.Logger) (*SessionPool, error) {
	ttl, err := cfg.IdleTTL()
	if err != nil {
		return nil, err
	}
	retention, err := cfg.Retention()
	if err != nil {
		return nil, err
	}
	pool := &SessionPool{
		cfg:       cfg,
		logger:    logger,
		idleTTL:   ttl,
		sessions:  map[string]*Session{},
		gcStop:    make(chan struct{}),
		gcDone:    make(chan struct{}),
		retention: retention,
	}
	if cfg.BrowserCacheDir != "" {
		cacheTTL := 24 * time.Hour
		if cfg.BrowserCacheTTL != "" {
			parsed, err := time.ParseDuration(cfg.BrowserCacheTTL)
			if err != nil {
				return nil, fmt.Errorf("parse browser_cache_ttl=%q: %w", cfg.BrowserCacheTTL, err)
			}
			cacheTTL = parsed
		}
		bc, err := affentbrowser.NewFileResponseCache(cfg.BrowserCacheDir, cacheTTL)
		if err != nil {
			return nil, fmt.Errorf("init browser cache: %w", err)
		}
		pool.browserCache = bc
		logger.Info().
			Str("dir", cfg.BrowserCacheDir).
			Dur("ttl", cacheTTL).
			Msg("browser response cache enabled")

		// Cache GC: walk the dir periodically to actually delete
		// entries past TTL. Without this, expired entries pile up on
		// disk forever (Get just returns miss past TTL). Default
		// interval = max(TTL/8, 5min) so a 24h TTL sweeps every 3h
		// and a 30m TTL sweeps every 5min. Operators can override
		// via BrowserCacheSweepInterval.
		sweepInterval := cacheTTL / 8
		if cfg.BrowserCacheSweepInterval != "" {
			if d, err := time.ParseDuration(cfg.BrowserCacheSweepInterval); err == nil && d > 0 {
				sweepInterval = d
			}
		}
		if sweepInterval < 5*time.Minute {
			sweepInterval = 5 * time.Minute
		}
		bc.StartSweeper(sweepInterval, func(deleted int) {
			if deleted > 0 {
				logger.Info().Int("deleted", deleted).Msg("browser cache sweep")
			}
		})
	}
	go pool.gcLoop()
	if pool.retention > 0 {
		pool.retentionStop = make(chan struct{})
		pool.retentionDone = make(chan struct{})
		go pool.gcRetentionLoop()
		logger.Info().Dur("retention", pool.retention).Msg("session retention sweep enabled")
	}
	return pool, nil
}

// GetOrCreate returns the existing session for id, or builds a new one
// when id is empty / unknown. Returns ErrShuttingDown if Shutdown has
// begun, so in-flight requests fail fast instead of leaking sessions
// past the pool's lifetime.
func (p *SessionPool) GetOrCreate(id string) (*Session, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.shuttingDown {
		return nil, ErrShuttingDown
	}

	if id != "" {
		if s, ok := p.sessions[id]; ok {
			s.touch()
			return s, nil
		}
	}

	// Eviction before insert: simple LRU by lastUsed; we walk the
	// map since the pool is small (≤ MaxSessions).
	for len(p.sessions) >= p.cfg.MaxSessions {
		var oldestID string
		var oldestTS time.Time
		for k, v := range p.sessions {
			if oldestID == "" || v.lastUsed.Before(oldestTS) {
				oldestID = k
				oldestTS = v.lastUsed
			}
		}
		if oldestID == "" {
			break
		}
		p.logger.Info().Str("session_id", oldestID).Msg("evicting LRU session")
		p.evictLocked(oldestID)
	}

	newID := id
	if newID == "" {
		newID = "sess_" + uuid.NewString()
	}
	s, err := p.buildSession(newID)
	if err != nil {
		return nil, err
	}
	p.sessions[newID] = s
	return s, nil
}

// buildSession constructs all per-session affent state. Errors here
// propagate to the chat-completions caller and abort the request.
func (p *SessionPool) buildSession(id string) (*Session, error) {
	// Validate the id BEFORE any filesystem operations — defense in
	// depth, even though the chat-completions handler already accepts
	// only client-supplied ids that go through ValidateSessionID.
	// Anything that joins this id into a path (workspace alloc,
	// session-dir alloc) must be unreachable for traversal attempts.
	if err := agent.ValidateSessionID(id); err != nil {
		return nil, err
	}
	workspace, err := p.allocWorkspace(id)
	if err != nil {
		return nil, fmt.Errorf("alloc workspace: %w", err)
	}
	// Stable per-session-id dir for durable state. Holds the
	// conversation log (so the chat handler's "we treat the rest of
	// history as already captured in the agent's Conversation log" is
	// actually true across restarts) and the memory store's files
	// (when EnableMemory is on). Both live here so one session_id
	// resolves to one durable identity on disk.
	sessionDir, err := p.allocSessionDir(id)
	if err != nil {
		_ = os.RemoveAll(workspace)
		return nil, fmt.Errorf("alloc session dir: %w", err)
	}
	conv, err := agent.OpenConversationAt(filepath.Join(sessionDir, "conversation.jsonl"))
	if err != nil {
		_ = os.RemoveAll(workspace)
		return nil, fmt.Errorf("conversation: %w", err)
	}
	llm := agent.NewLLMClient(p.cfg.BaseURL, p.cfg.APIKey, p.cfg.Model)

	reg := agent.NewRegistry()
	// Memory store — used both as a tool dependency and as the
	// snapshot source for Loop.EnsureSystemPrompt. Created once so
	// builtins + standalone registration share the same on-disk state.
	//
	// Memory lives at a STABLE per-session path (not under the
	// ephemeral workspace dir) so it survives LRU eviction and
	// server restarts. The session workspace gets a random suffix
	// every time it's allocated — using it for memory means "same
	// client, same session_id" sees an empty memory after any
	// restart, which defeats the long-running purpose.
	var memStore agent.MemoryStore
	if p.cfg.EnableMemory {
		fms := agent.NewFileMemoryStore(workspace)
		fms.MemoryDir = sessionDir
		// Scope the user-profile file under the per-session memory dir
		// as well. The library default (defaultUserMemoryPath →
		// ~/.config/affent/USER.md) is correct for affentctl, where
		// one OS-user spans many workspaces and a shared USER.md is
		// the intent. In affentserve, distinct session_ids are
		// distinct tenants — leaving USER.md global would cross-leak
		// "what you know about the user" across all clients of this
		// server. Same-session_id round-trips still see the same
		// profile because sessionDir is stable.
		fms.UserPath = filepath.Join(sessionDir, "USER.md")
		memStore = fms
	}
	var localExec *executor.LocalExecutor
	if p.cfg.EnableBuiltins {
		localExec = executor.NewLocalExecutor(id, workspace)
		agent.RegisterBuiltins(reg, agent.BuiltinDeps{
			Executor:         localExec,
			HostWorkspaceDir: workspace,
			Memory:           memStore,
		})
	} else if memStore != nil {
		// Memory tool without the shell/file builtins — common for
		// remote-driven affentserve deployments that don't want shell
		// exposed but still want durable per-user notes.
		agent.RegisterMemoryOnly(reg, memStore)
	}

	var browser *affentbrowser.Session
	if p.cfg.EnableBrowser {
		bcfg := affentbrowser.SessionConfig{
			NoSandbox:      true,
			DisableStealth: p.cfg.BrowserNoStealth,
			// Sandbox screenshot save_path to the per-session workspace
			// so the model can't write PNGs to /etc/cron.d/ or similar.
			// Mirrors the safeWorkspacePath guard the builtin file
			// tools already apply.
			WorkspaceDir: workspace,
			Intercept: affentbrowser.InterceptConfig{
				AllowAllDomains: p.cfg.BrowserAllowAllDomains,
				Cache:           p.browserCache,
			},
		}
		bs, err := affentbrowser.NewSession(bcfg)
		if err != nil {
			_ = os.RemoveAll(workspace)
			return nil, fmt.Errorf("browser session: %w", err)
		}
		affentbrowser.RegisterAll(reg, bs, affentbrowser.Options{
			IncludeScreenshot: p.cfg.BrowserScreenshot,
		})
		browser = bs
	}
	if p.cfg.EnableWeb {
		if p.cfg.EnableWebSearch {
			if err := affentweb.RegisterAll(reg, affentweb.Options{}); err != nil {
				p.logger.Warn().Err(err).Msg("web_search not available; registering web_fetch only")
				affentweb.RegisterFetch(reg, affentweb.FetchConfig{})
			}
		} else {
			affentweb.RegisterFetch(reg, affentweb.FetchConfig{})
		}
	}
	if p.cfg.EnableBuiltins {
		agent.RegisterSubagent(reg, agent.SubagentDeps{
			LLM:              llm,
			Executor:         localExec,
			HostWorkspaceDir: workspace,
			Memory:           memStore,
			ParentSessionID:  id,
			TranscriptDir:    filepath.Join(sessionDir, "subagents", id),
			Log:              p.logger.With().Str("session_id", id).Logger(),
		})
	}

	// Generous event buffer — chat handler subscribes and drains, but
	// during turn execution we don't want to block the loop on a slow
	// subscriber.
	events := make(chan sse.Event, 1024)
	loop := &agent.Loop{
		LLM:          llm,
		Tools:        reg,
		Conv:         conv,
		Events:       events,
		Log:          p.logger.With().Str("session_id", id).Logger(),
		MaxTurnSteps: p.cfg.MaxTurnSteps,
		// Snapshot source for EnsureSystemPrompt — when nil, the
		// memory block is just omitted from the system prompt and
		// the tool isn't registered above anyway.
		Memory: memStore,
	}
	// Always-on rolling-summary compactor, same posture affentctl
	// takes. Without it, a long-running session eventually outgrows
	// the upstream's context window and runStep returns a
	// non-retryable error (context overflow is only recoverable when
	// l.Compactor != nil). For affentserve specifically that means a
	// session that's been chatting for a day dies at the boundary
	// with no recovery — and since the conv log is durable across
	// restarts, even reopening the same session_id won't help. The
	// LLM client reuses the same provider/model so summarization
	// hits the same backend the agent is already configured against.
	triggerMsgs := p.cfg.CompactTrigger
	if triggerMsgs <= 0 {
		triggerMsgs = agent.DefaultSummaryTriggerMsgs
	}
	keepLast := p.cfg.CompactKeepLast
	if keepLast <= 0 {
		keepLast = agent.DefaultSummaryKeepLast
	}
	loop.Compactor = &agent.LLMSummaryCompactor{
		LLM:         llm,
		TriggerMsgs: triggerMsgs,
		KeepLast:    keepLast,
	}
	if err := loop.EnsureSystemPrompt(p.cfg.SystemPrompt); err != nil {
		_ = os.RemoveAll(workspace)
		if browser != nil {
			_ = browser.Close()
		}
		return nil, fmt.Errorf("system prompt: %w", err)
	}

	s := &Session{
		ID:        id,
		loop:      loop,
		conv:      conv,
		llm:       llm,
		registry:  reg,
		events:    events,
		browser:   browser,
		workspace: workspace,
		createdAt: time.Now(),
		lastUsed:  time.Now(),
		subs:      map[int]chan sse.Event{},
		closedCh:  make(chan struct{}),
	}
	go s.fanout()
	return s, nil
}

func (p *SessionPool) allocWorkspace(id string) (string, error) {
	root := p.cfg.WorkspaceRoot
	if root == "" {
		// Per-session temp dir. Cleaned on session close.
		return os.MkdirTemp("", "affentserve-"+id+"-")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", err
	}
	return os.MkdirTemp(root, id+"-")
}

// sessionDirPath computes the durable per-session-id state dir path
// WITHOUT touching the filesystem. Caller must have already validated
// id via agent.ValidateSessionID — passing an unsanitized id is a
// path-traversal bug.
//
// The historical name "MemoryRoot" predates conversation-log
// durability; the dir now holds both, but the env/config knob keeps
// its old name to avoid an unmotivated rename of a public surface.
func (p *SessionPool) sessionDirPath(id string) string {
	root := p.cfg.MemoryRoot
	if root == "" {
		if p.cfg.WorkspaceRoot != "" {
			root = filepath.Join(p.cfg.WorkspaceRoot, "memory")
		} else {
			root = filepath.Join(os.TempDir(), "affentserve-memory")
		}
	}
	return filepath.Join(root, id)
}

// allocSessionDir returns the durable per-session-id state dir. Holds
// the JSONL conversation log and (when EnableMemory is on) the memory
// store files. Unlike allocWorkspace, this is STABLE: same id → same
// path across server restarts and LRU evictions, so the chat handler's
// "the rest of history lives in the Conversation log keyed by
// session_id" contract actually holds — and the long-running-memory
// promise survives. Callers must have already passed id through
// agent.ValidateSessionID; buildSession enforces this at the top.
func (p *SessionPool) allocSessionDir(id string) (string, error) {
	dir := p.sessionDirPath(id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

// touch updates lastUsed under the pool lock.
func (s *Session) touch() {
	s.mu.Lock()
	s.lastUsed = time.Now()
	s.mu.Unlock()
}

// Subscribe registers a channel that will receive a copy of every
// event emitted by this session's loop until Unsubscribe (or the
// session closes). Returns a pre-closed channel and id < 0 when the
// session is already closed, so callers see immediate EOF instead of
// leaking a goroutine on a never-delivered subscription.
func (s *Session) Subscribe(buf int) (int, <-chan sse.Event) {
	s.subsMu.Lock()
	defer s.subsMu.Unlock()
	select {
	case <-s.closedCh:
		closed := make(chan sse.Event)
		close(closed)
		return -1, closed
	default:
	}
	ch := make(chan sse.Event, buf)
	id := s.nextSub
	s.nextSub++
	s.subs[id] = ch
	return id, ch
}

func (s *Session) Unsubscribe(id int) {
	s.subsMu.Lock()
	if ch, ok := s.subs[id]; ok {
		delete(s.subs, id)
		close(ch)
	}
	s.subsMu.Unlock()
}

// fanout drains the loop's event channel into every subscriber. Drops
// events for slow subscribers (non-blocking send) rather than stalling
// the loop, which would back-pressure the LLM call. Exits when the
// session's closedCh fires; we deliberately do NOT close s.events
// from the close path because affent's Loop may still be mid-turn and
// publish-on-closed-channel would panic. The events channel becomes
// unreferenced after fanout exits and is GC'd.
func (s *Session) fanout() {
	defer func() {
		s.subsMu.Lock()
		for id, ch := range s.subs {
			close(ch)
			delete(s.subs, id)
		}
		s.subsMu.Unlock()
	}()
	for {
		select {
		case <-s.closedCh:
			return
		case ev, ok := <-s.events:
			if !ok {
				return
			}
			s.subsMu.Lock()
			for _, ch := range s.subs {
				select {
				case ch <- ev:
				default:
					// Subscriber is behind. Skip rather than block
					// the loop. SSE clients see this as a missing
					// event id and reconnect with Last-Event-ID for
					// replay once that feature lands.
				}
			}
			s.subsMu.Unlock()
		}
	}
}

// Close releases all session resources. Idempotent and safe under
// concurrent callers — sync.Once guarantees the close+cancel+remove
// sequence runs at most once even if Delete races with Shutdown.
//
// Lifecycle invariant: we never close `s.events`. affent's Loop
// publishes events with a plain `send` (not `select` with `default`
// to nil), so closing the channel while runTurn is still alive panics.
// Cancel() asks the loop to wind down — we don't currently block here
// waiting for the goroutine to exit; relying on the in-flight context
// to drain in the background is acceptable because the channel and
// loop are both garbage-collectable once nothing else references them.
func (s *Session) Close() error {
	s.closeOnce.Do(func() {
		close(s.closedCh)
		s.loop.Cancel()
		if s.browser != nil {
			if err := s.browser.Close(); err != nil {
				s.closeErr = err
			}
		}
		if s.workspace != "" {
			_ = os.RemoveAll(s.workspace)
		}
	})
	return s.closeErr
}

// Delete removes a session from the pool, closes it, AND purges the
// on-disk durable state (conversation log + memory files). Unlike
// idle-GC eviction — which keeps the durable dir so a future
// GetOrCreate(id) resumes the same session — an explicit DELETE
// means "I'm done with this session, clean up". Without the disk
// purge the very next GetOrCreate(id) would resurrect a zombie
// session with all the old conv history and memory intact,
// contradicting what every other DELETE in the codebase promises.
//
// id is validated before any filesystem call so a malicious
// DELETE /v1/sessions/.. can't RemoveAll the MemoryRoot parent.
func (p *SessionPool) Delete(id string) bool {
	if err := agent.ValidateSessionID(id); err != nil {
		// Unknown / unsafe id — nothing to do. Matches the
		// handler-level contract that DELETE is idempotent and
		// doesn't 404 on unknown ids.
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	evicted := p.evictLocked(id)
	dir := p.sessionDirPath(id)
	// RemoveAll runs under the pool lock so a concurrent
	// GetOrCreate(id) can't slip in between the eviction and the
	// disk wipe — without that ordering it would observe the old
	// dir, OpenConversationAt the stale jsonl, and build a fresh
	// session backed by the predecessor's conv log and memory.
	// GetOrCreate already serializes on this lock through
	// buildSession, so we're not penalizing throughput here that
	// wasn't already serialized.
	if err := os.RemoveAll(dir); err != nil {
		p.logger.Warn().Err(err).Str("session_id", id).Msg("purge session dir")
	}
	return evicted
}

// evictLocked must be called with p.mu held.
func (p *SessionPool) evictLocked(id string) bool {
	s, ok := p.sessions[id]
	if !ok {
		return false
	}
	delete(p.sessions, id)
	go func() {
		if err := s.Close(); err != nil {
			p.logger.Warn().Err(err).Str("session_id", id).Msg("session close")
		}
	}()
	return true
}

// gcLoop GCs idle sessions. Runs at min(idleTTL/4, 1m) — the doc-comment
// always said "whichever is smaller", but the original code had the
// inequality flipped: `if interval < time.Minute { interval = time.Minute }`
// effectively clamped to >= 1 minute. With `--session-idle-ttl 10s`
// the user got 60s of slop after the TTL expired before eviction
// actually fired, contradicting the flag's promise.
//
// Lower clamp at 1 second so a misconfigured idleTTL=0 doesn't busy-
// loop the ticker.
func (p *SessionPool) gcLoop() {
	defer close(p.gcDone)
	interval := p.idleTTL / 4
	if interval > time.Minute {
		interval = time.Minute
	}
	if interval < time.Second {
		interval = time.Second
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-p.gcStop:
			return
		case <-t.C:
			p.gcOnce()
		}
	}
}

func (p *SessionPool) gcOnce() {
	cutoff := time.Now().Add(-p.idleTTL)
	var stale []string
	p.mu.Lock()
	for id, s := range p.sessions {
		s.mu.Lock()
		idleSince := s.lastUsed
		s.mu.Unlock()
		if idleSince.Before(cutoff) {
			stale = append(stale, id)
		}
	}
	for _, id := range stale {
		p.logger.Info().Str("session_id", id).Msg("GC idle session")
		p.evictLocked(id)
	}
	p.mu.Unlock()
}

// gcRetentionLoop runs the disk-level GC of durable per-session dirs.
// Sweep interval is min(retention/24, 24h) clamped to >= 10min — a
// 30-day retention sweeps every 30min, a week sweeps every ~7min
// (clamped to 10), a year-long retention sweeps every 24h. Trade-off:
// frequent enough to catch stale dirs within a single retention window,
// rare enough not to thrash a multi-thousand-id MemoryRoot.
func (p *SessionPool) gcRetentionLoop() {
	defer close(p.retentionDone)
	interval := p.retention / 24
	if interval > 24*time.Hour {
		interval = 24 * time.Hour
	}
	if interval < 10*time.Minute {
		interval = 10 * time.Minute
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-p.retentionStop:
			return
		case <-t.C:
			p.sweepRetentionOnce()
		}
	}
}

// sweepRetentionOnce walks MemoryRoot looking for session dirs whose
// conversation.jsonl mtime (or the dir mtime as fallback) is older
// than the configured retention. Active sessions — those currently
// in the in-memory pool — are skipped no matter how stale: their
// users are mid-conversation, and the disk image isn't authoritative
// while the loop's in-memory state holds the latest message.
//
// Exported (uppercase first letter inside the package) to make it
// drive-testable: hand-craft stale dirs, call sweepRetentionOnce,
// assert the right ones disappeared.
func (p *SessionPool) sweepRetentionOnce() {
	if p.retention <= 0 {
		return
	}
	root := p.cfg.MemoryRoot
	if root == "" {
		if p.cfg.WorkspaceRoot != "" {
			root = filepath.Join(p.cfg.WorkspaceRoot, "memory")
		} else {
			root = filepath.Join(os.TempDir(), "affentserve-memory")
		}
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		if !os.IsNotExist(err) {
			p.logger.Warn().Err(err).Str("root", root).Msg("retention sweep read")
		}
		return
	}
	cutoff := time.Now().Add(-p.retention)
	deleted := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		id := e.Name()
		if agent.ValidateSessionID(id) != nil {
			// Some operator-placed file or a malformed name we wouldn't
			// recognize as ours. Don't touch it.
			continue
		}
		dir := filepath.Join(root, id)
		// Stat is cheap and independent of pool state — do it outside
		// the lock so we don't gate the lock on filesystem latency for
		// every dir on every sweep, including the fresh ones we'll
		// skip immediately.
		var mtime time.Time
		if fi, err := os.Stat(filepath.Join(dir, "conversation.jsonl")); err == nil {
			mtime = fi.ModTime()
		} else if fi, err := os.Stat(dir); err == nil {
			// Conversation log missing (memory-only setup / half-built
			// session) — fall back to the dir's own mtime.
			mtime = fi.ModTime()
		} else {
			continue
		}
		if !mtime.Before(cutoff) {
			continue
		}
		// Active-check + RemoveAll must run UNDER THE SAME LOCK as
		// GetOrCreate. Without that, a client reconnecting with this
		// id between the active-check and the RemoveAll would land a
		// freshly-built session pointing at conv.jsonl — and we'd then
		// wipe the dir out from under it, breaking the next Append
		// with ENOENT. Pre-fix the two operations sat in separate
		// critical sections.
		p.mu.Lock()
		if _, active := p.sessions[id]; active {
			p.mu.Unlock()
			continue
		}
		if err := os.RemoveAll(dir); err != nil {
			p.mu.Unlock()
			p.logger.Warn().Err(err).Str("session_id", id).Msg("retention sweep remove")
			continue
		}
		p.mu.Unlock()
		deleted++
		p.logger.Info().
			Str("session_id", id).
			Time("last_active", mtime).
			Msg("retention sweep removed stale session dir")
	}
	if deleted > 0 {
		p.logger.Info().Int("deleted", deleted).Msg("retention sweep")
	}
}

// Shutdown closes every session and stops the GC goroutine. Safe to
// call multiple times. Marks the pool as shutting-down before
// snapshotting sessions so any concurrent GetOrCreate fails fast with
// ErrShuttingDown instead of creating sessions that nobody will close.
// IsShuttingDown reports whether Shutdown has begun. Used by /healthz
// so a fronting load balancer can drain new traffic the moment a
// graceful shutdown starts, before in-flight sessions finish closing.
func (p *SessionPool) IsShuttingDown() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.shuttingDown
}

// SignalShutdown flips the shutting-down flag WITHOUT actually
// draining sessions or stopping goroutines. Lets the main loop
// mark the pool as unavailable the moment a SIGTERM lands so
// /healthz starts returning 503 immediately — the LB then has the
// http-server's graceful-shutdown window to notice the readiness
// flip and drain new traffic, instead of finding out only after
// srv.Shutdown closed the listener.
//
// Idempotent. The real cleanup still runs through Shutdown (which
// the main loop typically calls via defer), and Shutdown is a no-op
// after SignalShutdown thanks to shutdownOnce in the original
// implementation — but to be safe we set the flag under lock
// without taking the once latch.
func (p *SessionPool) SignalShutdown() {
	p.mu.Lock()
	p.shuttingDown = true
	p.mu.Unlock()
}

func (p *SessionPool) Shutdown() {
	p.shutdownOnce.Do(func() {
		if p.browserCache != nil {
			if bc, ok := p.browserCache.(*affentbrowser.FileResponseCache); ok {
				bc.StopSweeper()
			}
		}
		// Mark shutdown FIRST so /healthz can start returning 503 and
		// any front-line load balancer can drain new traffic from this
		// pod. Pre-fix the flag was set only after the gc goroutines
		// joined — which can take seconds — and the LB kept routing
		// requests at a dying server during that window.
		p.mu.Lock()
		p.shuttingDown = true
		ids := make([]string, 0, len(p.sessions))
		for id := range p.sessions {
			ids = append(ids, id)
		}
		p.mu.Unlock()

		close(p.gcStop)
		<-p.gcDone
		if p.retentionStop != nil {
			close(p.retentionStop)
			<-p.retentionDone
		}

		var wg sync.WaitGroup
		for _, id := range ids {
			wg.Add(1)
			go func(id string) {
				defer wg.Done()
				p.Delete(id)
			}(id)
		}
		wg.Wait()
	})
}

// ErrSessionNotFound is returned by handlers when a session id has no
// pool entry.
var ErrSessionNotFound = errors.New("session not found")

// Get returns the session by id or ErrSessionNotFound.
func (p *SessionPool) Get(id string) (*Session, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	s, ok := p.sessions[id]
	if !ok {
		return nil, ErrSessionNotFound
	}
	s.touch()
	return s, nil
}

// SendUser is the single-turn driver: take the lock, push a user
// message, and signal the caller when the turn ends. Returns the
// turn id assigned by agent.
func (s *Session) SendUser(ctx context.Context, text string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loop.SendUser(ctx, text)
}

// CancelTurn signals the in-flight turn (if any) to abort. Used by
// the chat-completions handler when the HTTP client disconnects mid-
// turn, so the affent Loop and any in-flight tool call wind down
// promptly instead of running to MaxTurnSteps with no listener.
//
// Idempotent — affent's Cancel is a no-op when no turn is active.
func (s *Session) CancelTurn() {
	if s == nil || s.loop == nil {
		return
	}
	s.loop.Cancel()
}

// BrowserStatsSnapshot returns the session's browser interceptor
// counters when a browser is attached; zeros otherwise. Used by the
// /v1/stats endpoint and per-session debug logs.
type BrowserStatsSnapshot struct {
	BlockedByType   int64 `json:"blocked_by_type"`
	BlockedByDomain int64 `json:"blocked_by_domain"`
	CacheHit        int64 `json:"cache_hit"`
	CacheMiss       int64 `json:"cache_miss"`
	NetworkFetch    int64 `json:"network_fetch"`
}

func (s *Session) BrowserStatsSnapshot() BrowserStatsSnapshot {
	if s == nil || s.browser == nil {
		return BrowserStatsSnapshot{}
	}
	bt, bd, ch, cm, nf := s.browser.InterceptStats()
	return BrowserStatsSnapshot{
		BlockedByType:   bt,
		BlockedByDomain: bd,
		CacheHit:        ch,
		CacheMiss:       cm,
		NetworkFetch:    nf,
	}
}

// Workspace exposes the session's on-disk directory for tests that
// want to inspect the JSONL conversation log. Not used by production
// paths.
func (s *Session) Workspace() string { return s.workspace }
