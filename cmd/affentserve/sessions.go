package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/affinefoundation/affent"
	"github.com/affinefoundation/affent/executor"
	affentbrowser "github.com/affinefoundation/affent/extras/browser"
	affentweb "github.com/affinefoundation/affent/extras/web"
	"github.com/affinefoundation/affent/sse"
	"github.com/google/uuid"
	"github.com/rs/zerolog"
)

// Session is one server-managed agent session: an affent.Loop plus
// its supporting state, plus a fan-out for events going to multiple
// concurrent consumers (chat-completions accumulator + raw SSE
// subscribers).
type Session struct {
	ID string

	mu sync.Mutex

	loop      *affent.Loop
	conv      *affent.Conversation
	llm       *affent.LLMClient
	registry  *affent.Registry
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
	pool := &SessionPool{
		cfg:      cfg,
		logger:   logger,
		idleTTL:  ttl,
		sessions: map[string]*Session{},
		gcStop:   make(chan struct{}),
		gcDone:   make(chan struct{}),
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
	workspace, err := p.allocWorkspace(id)
	if err != nil {
		return nil, fmt.Errorf("alloc workspace: %w", err)
	}
	conv, err := affent.NewConversation(workspace, id)
	if err != nil {
		_ = os.RemoveAll(workspace)
		return nil, fmt.Errorf("conversation: %w", err)
	}
	llm := affent.NewLLMClient(p.cfg.BaseURL, p.cfg.APIKey, p.cfg.Model)

	reg := affent.NewRegistry()
	if p.cfg.EnableBuiltins {
		affent.RegisterBuiltins(reg, affent.BuiltinDeps{
			Executor:         executor.NewLocalExecutor(id, workspace),
			HostWorkspaceDir: workspace,
		})
	}

	var browser *affentbrowser.Session
	if p.cfg.EnableBrowser {
		bcfg := affentbrowser.SessionConfig{
			NoSandbox:      true,
			DisableStealth: p.cfg.BrowserNoStealth,
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

	// Generous event buffer — chat handler subscribes and drains, but
	// during turn execution we don't want to block the loop on a slow
	// subscriber.
	events := make(chan sse.Event, 1024)
	loop := &affent.Loop{
		LLM:          llm,
		Tools:        reg,
		Conv:         conv,
		Events:       events,
		Log:          p.logger.With().Str("session_id", id).Logger(),
		MaxTurnSteps: p.cfg.MaxTurnSteps,
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

// Delete removes a session from the pool and closes it.
func (p *SessionPool) Delete(id string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.evictLocked(id)
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

// Shutdown closes every session and stops the GC goroutine. Safe to
// call multiple times. Marks the pool as shutting-down before
// snapshotting sessions so any concurrent GetOrCreate fails fast with
// ErrShuttingDown instead of creating sessions that nobody will close.
func (p *SessionPool) Shutdown() {
	p.shutdownOnce.Do(func() {
		if p.browserCache != nil {
			if bc, ok := p.browserCache.(*affentbrowser.FileResponseCache); ok {
				bc.StopSweeper()
			}
		}
		close(p.gcStop)
		<-p.gcDone

		p.mu.Lock()
		p.shuttingDown = true
		ids := make([]string, 0, len(p.sessions))
		for id := range p.sessions {
			ids = append(ids, id)
		}
		p.mu.Unlock()

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
// turn id assigned by affent.
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

// filename helper kept here so test code outside the package can rely
// on the stable JSONL log name in the workspace.
func sessionLogPath(workspace, id string) string {
	return filepath.Join(workspace, ".affentctl", id+".jsonl")
}
