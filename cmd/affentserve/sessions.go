package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	affentbrowser "github.com/affinefoundation/affent/extras/browser"
	affentweb "github.com/affinefoundation/affent/extras/web"
	agent "github.com/affinefoundation/affent/internal/agent"
	"github.com/affinefoundation/affent/internal/eventlog"
	"github.com/affinefoundation/affent/internal/executor"
	"github.com/affinefoundation/affent/internal/jsonl"
	"github.com/affinefoundation/affent/internal/loopstate"
	"github.com/affinefoundation/affent/internal/memory"
	"github.com/affinefoundation/affent/internal/planstate"
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

	loop             *agent.Loop
	conv             *agent.Conversation
	llm              *agent.LLMClient
	registry         *agent.Registry
	skillRegistry    *agent.SkillRegistry
	events           chan sse.Event
	browser          *affentbrowser.Session
	sessionDir       string
	workspace        string
	loopProtocolPath string
	loopProtocolInit bool
	planPath         string
	createdAt        time.Time
	lastUsed         time.Time
	trace            *eventlog.Recorder
	traceFile        *os.File
	nextEventLine    int64
	activeTurns      atomic.Int64

	// Per-session lifetime token counters, accumulated in fanout as
	// sse.TypeUsage events flow through. Atomic so the /v1/stats
	// reader doesn't need to hold s.mu. The Loop emits one Usage event
	// per turn with the per-turn totals; these sum to a session
	// lifetime spend that operators can poll without subscribing.
	inputTokens  atomic.Int64
	outputTokens atomic.Int64
	turns        atomic.Int64

	toolRequests                      atomic.Int64
	toolNameCanonicalized             atomic.Int64
	toolArgsRepaired                  atomic.Int64
	toolRepairCalls                   atomic.Int64
	toolRepairSucceeded               atomic.Int64
	toolRepairFailed                  atomic.Int64
	toolRepairNotes                   atomic.Int64
	toolErrors                        atomic.Int64
	toolDurationMS                    atomic.Int64
	loopGuardInterventions            atomic.Int64
	forcedNoTools                     atomic.Int64
	sourceAccessResults               atomic.Int64
	sourceAccessVerified              atomic.Int64
	sourceAccessDiscovery             atomic.Int64
	sourceAccessNetwork               atomic.Int64
	sourceAccessDynamic               atomic.Int64
	memoryUpdates                     atomic.Int64
	memoryUpdateAdd                   atomic.Int64
	memoryUpdateReplace               atomic.Int64
	memoryUpdateRemove                atomic.Int64
	memorySearchCalls                 atomic.Int64
	memorySearchMisses                atomic.Int64
	sessionSearchCalls                atomic.Int64
	sessionSearchResults              atomic.Int64
	sessionSearchContext              atomic.Int64
	sessionSearchTerms                atomic.Int64
	sessionSearchRecent               atomic.Int64
	toolContextTruncated              atomic.Int64
	toolContextOmitted                atomic.Int64
	toolRepairMu                      sync.Mutex
	toolRepairByKind                  map[string]int64
	toolFailureByKind                 map[string]int64
	toolGovernanceMu                  sync.Mutex
	toolGovernanceCallsByID           map[string]sessionToolGovernanceClass
	planByAction                      map[string]int64
	focusedTaskByType                 map[string]int64
	subagentByMode                    map[string]int64
	planCalls                         atomic.Int64
	planErrors                        atomic.Int64
	focusedTaskCalls                  atomic.Int64
	focusedTaskErrors                 atomic.Int64
	subagentCalls                     atomic.Int64
	subagentErrors                    atomic.Int64
	runtimeErrors                     atomic.Int64
	contextCompactions                atomic.Int64
	contextCompactionReact            atomic.Int64
	contextCompactionRmMsg            atomic.Int64
	contextCompactionBytes            atomic.Int64
	contextCompactionMiss             atomic.Int64
	contextCompactionEmpty            atomic.Int64
	runtimeStatsMu                    sync.Mutex
	turnEndByReason                   map[string]int64
	runtimeErrorByKind                map[string]int64
	contextCompactionLastReason       string
	contextCompactionLastReactive     bool
	contextCompactionLastSummaryState string

	// fan-out
	subsMu  sync.Mutex
	subs    map[int]chan sse.Event
	nextSub int

	closedCh   chan struct{}
	fanoutDone chan struct{}
	closeOnce  sync.Once
	closeErr   error
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

	// scheduleStop signals the durable session schedule runner to exit.
	scheduleStop chan struct{}
	scheduleDone chan struct{}
	schedulesMu  sync.Mutex

	// settingsMu serializes account-level settings writes such as
	// environment variables and generated SSH keys.
	settingsMu sync.Mutex

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

// ErrNoIdleSession is returned when the pool is at capacity but every
// in-memory session is currently running a turn.
var ErrNoIdleSession = errors.New("no idle session available for eviction")

func workflowToolsEnabled(cfg Config) bool {
	return resolveServeRuntimeCapabilities(cfg).WorkflowTools
}

// NewSessionPool constructs a pool with the idle-GC goroutine running.
func NewSessionPool(cfg Config, logger zerolog.Logger) (*SessionPool, error) {
	cfg.ApplyEvalMode()
	ttl, err := cfg.IdleTTL()
	if err != nil {
		return nil, err
	}
	retention, err := cfg.Retention()
	if err != nil {
		return nil, err
	}
	pool := &SessionPool{
		cfg:          cfg,
		logger:       logger,
		idleTTL:      ttl,
		sessions:     map[string]*Session{},
		gcStop:       make(chan struct{}),
		gcDone:       make(chan struct{}),
		scheduleStop: make(chan struct{}),
		scheduleDone: make(chan struct{}),
		retention:    retention,
	}
	if cfg.BrowserCacheDir != "" {
		cacheTTL, err := cfg.BrowserCacheTTLDuration()
		if err != nil {
			return nil, err
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
		sweepInterval, err := cfg.BrowserCacheSweepIntervalDuration(cacheTTL)
		if err != nil {
			return nil, err
		}
		bc.StartSweeper(sweepInterval, func(deleted int) {
			if deleted > 0 {
				logger.Info().Int("deleted", deleted).Msg("browser cache sweep")
			}
		})
	}
	go pool.gcLoop()
	if !cfg.EvalMode {
		go pool.scheduleLoop()
	} else {
		close(pool.scheduleDone)
	}
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
			if v.isActiveTurn() {
				continue
			}
			if oldestID == "" || v.lastUsed.Before(oldestTS) {
				oldestID = k
				oldestTS = v.lastUsed
			}
		}
		if oldestID == "" {
			return nil, ErrNoIdleSession
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

func (p *SessionPool) newBrowserSession(workspace string) (*affentbrowser.Session, error) {
	return affentbrowser.NewSession(affentbrowser.SessionConfig{
		NoSandbox:      true,
		DisableStealth: p.cfg.BrowserNoStealth,
		// Sandbox screenshot save_path to the per-session workspace
		// so the model can't write PNGs to /etc/cron.d/ or similar.
		// Mirrors the safeWorkspacePath guard the builtin file tools
		// already apply.
		WorkspaceDir: workspace,
		Intercept: affentbrowser.InterceptConfig{
			AllowAllDomains: p.cfg.BrowserAllowAllDomains,
			Cache:           p.browserCache,
		},
	})
}

func (p *SessionPool) subagentChildToolRegistrar(workspace string) func(context.Context, *agent.Registry) (func(), error) {
	if !p.cfg.EnableBrowser {
		return nil
	}
	return func(ctx context.Context, reg *agent.Registry) (func(), error) {
		bs, err := p.newBrowserSession(workspace)
		if err != nil {
			return nil, err
		}
		// Keep child browser tools read-mostly and text-oriented. The
		// parent may opt into browser_screenshot for vision models, but
		// subagents should not gain a file-writing screenshot surface.
		affentbrowser.RegisterAll(reg, bs, affentbrowser.Options{})
		return func() { _ = bs.Close() }, nil
	}
}

// focusedTaskWebRegistrar returns the per-call hook that gives a
// focused-task child the web_fetch (+ optional web_search) tools when
// the deployment has enabled web globally. nil disables the web_extract
// and research profiles via FocusedTaskDeps.profileAvailable; the
// run_task schema drops them from the enum in that case so the model
// never sees an option it can't fulfill.
func (p *SessionPool) focusedTaskWebRegistrar() func(context.Context, *agent.Registry) (func(), error) {
	if !p.cfg.EnableWeb {
		return nil
	}
	return func(ctx context.Context, reg *agent.Registry) (func(), error) {
		if p.cfg.EnableWebSearch {
			if err := affentweb.RegisterAll(reg, affentweb.Options{}); err != nil {
				return nil, fmt.Errorf("focused task web_search: %w", err)
			}
		} else {
			affentweb.RegisterFetch(reg, affentweb.FetchConfig{})
		}
		// Web tools are stateless — no per-call cleanup required.
		return nil, nil
	}
}

func (p *SessionPool) focusedTaskBrowserRegistrar(workspace string) func(context.Context, *agent.Registry) (func(), error) {
	if !p.cfg.EnableBrowser {
		return nil
	}
	return func(ctx context.Context, reg *agent.Registry) (func(), error) {
		bs, err := p.newBrowserSession(workspace)
		if err != nil {
			return nil, err
		}
		// Focused research children need text/rendered-page inspection, not
		// screenshot files. Keep the child surface browser-only and read-mostly.
		affentbrowser.RegisterAll(reg, bs, affentbrowser.Options{})
		if p.cfg.EnableWeb {
			// buildFocusedTaskRegistry registers web before browser. Once the
			// browser exists, replace web_fetch with an equivalent fetch tool
			// that can reuse this same child Chromium session for rendered
			// fallback instead of forcing the model to notice and recover from
			// direct-reader failures manually.
			reg.Remove("web_fetch")
			affentweb.RegisterFetch(reg, p.webFetchConfig(bs))
		}
		return func() { _ = bs.Close() }, nil
	}
}

func (p *SessionPool) webFetchConfig(browser *affentbrowser.Session) affentweb.FetchConfig {
	return affentweb.FetchConfig{
		RenderedFallback: browserRenderedFallback(browser),
	}
}

func browserRenderedFallback(browser *affentbrowser.Session) affentweb.RenderedFallbackFunc {
	if browser == nil {
		return nil
	}
	nav := affentbrowser.NavigateTool(browser)
	return func(ctx context.Context, requestURL string, reason affentweb.FetchFallbackReason) (string, error) {
		waitUntil := "networkidle"
		if reason.Kind == "search_results_page" {
			waitUntil = "load"
		}
		args, err := json.Marshal(map[string]string{
			"url":        requestURL,
			"wait_until": waitUntil,
		})
		if err != nil {
			return "", err
		}
		return nav.Execute(ctx, args)
	}
}

func filterServeEvalModeTools(reg *agent.Registry, cfg Config) error {
	if reg == nil || !cfg.EvalMode || cfg.EvalAllTools {
		return nil
	}
	allowed, requested := serveEvalToolAllowlist(cfg)
	if cfg.enableBuiltinsSet && cfg.EnableBuiltins {
		for _, name := range serveEvalWorkspaceToolNames() {
			allowed[name] = true
		}
	}
	if cfg.EnableMemory {
		allowed[agent.MemoryToolName] = true
	}
	if cfg.EnableWeb {
		allowed["web_fetch"] = true
	}
	if cfg.EnableWebSearch {
		allowed["web_search"] = true
	}
	if cfg.EnableBrowser {
		for _, name := range serveEvalBrowserToolNames() {
			if name == "browser_screenshot" && !cfg.BrowserScreenshot {
				continue
			}
			allowed[name] = true
		}
	}
	if cfg.EnableSubagent {
		allowed[agent.SubagentToolName] = true
	}
	if cfg.EnableFocusedTasks {
		allowed[agent.FocusedTaskToolName] = true
	}
	for _, def := range reg.Defs() {
		if !allowed[def.Function.Name] {
			reg.Remove(def.Function.Name)
		}
	}
	var missing []string
	for _, name := range requested {
		if _, ok := reg.Get(name); !ok {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf("eval_tools requested unavailable tool(s): %s", strings.Join(missing, ", "))
	}
	return nil
}

func serveRegistryHasWorkspaceTool(reg *agent.Registry) bool {
	if reg == nil {
		return false
	}
	for _, name := range serveEvalWorkspaceToolNames() {
		if _, ok := reg.Get(name); ok {
			return true
		}
	}
	return false
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
	// actually true across restarts), runtime-installed skills, and the
	// memory store's files (when EnableMemory is on). These live here so
	// one session_id resolves to one durable identity on disk.
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
	llm.Sampling = agent.SamplingDefaults{
		Temperature: p.cfg.Temperature,
		TopP:        p.cfg.TopP,
		MaxTokens:   p.cfg.MaxTokens,
	}

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
	var memStore memory.MemoryStore
	if p.cfg.EnableMemory {
		fms := memory.NewFileMemoryStore(workspace)
		fms.MemoryDir = sessionDir
		fms.UserPath = p.userMemoryPath(sessionDir)
		memStore = fms
	}
	var localExec *executor.LocalExecutor
	var skillReg *agent.SkillRegistry
	sessionSkillDir := ""
	accountSkillInstallDir := ""
	planPath := ""
	loopProtocolPath := ""
	if p.cfg.EnableBuiltins {
		localExec = executor.NewLocalExecutor(id, workspace)
		localExec.EnvProvider = p.accountEnvPairs
		if workflowToolsEnabled(p.cfg) {
			sessionSkillDir = agent.DefaultWorkspaceSkillDir(sessionDir)
			accountSkillInstallDir = accountSkillDir(p)
			var skillErr error
			skillReg, skillErr = sessionRuntimeSkillRegistry(p, sessionSkillDir)
			if skillErr != nil {
				_ = os.RemoveAll(workspace)
				return nil, fmt.Errorf("skills: %w", skillErr)
			}
		}
		if workflowToolsEnabled(p.cfg) {
			planPath = filepath.Join(sessionDir, "plan.json")
		}
		loopProtocolPath = loopstate.ProtocolPath(sessionDir, id)
		loopProtocolToolPath := ""
		if p.cfg.EnableLoopProtocol && !p.cfg.EvalMode {
			loopProtocolToolPath = loopProtocolPath
		}
		agent.RegisterBuiltins(reg, agent.BuiltinDeps{
			Executor:             localExec,
			HostWorkspaceDir:     workspace,
			Memory:               memStore,
			SessionsDir:          p.sessionRootPath(),
			SessionID:            id,
			PlanPath:             planPath,
			LoopProtocolPath:     loopProtocolToolPath,
			SkillRegistry:        skillReg,
			SkillDir:             accountSkillInstallDir,
			SecretValuesProvider: p.accountSecretValues,
			SkillInstallConfirmer: func(proposalID string) bool {
				return agent.UserConfirmedRuntimeSkillProposal(conv, proposalID)
			},
			DisableSkill: !workflowToolsEnabled(p.cfg),
		})
	} else if memStore != nil {
		// Memory tool without the shell/file builtins — common for
		// remote-driven affentserve deployments that don't want shell
		// exposed but still want durable per-user notes.
		agent.RegisterMemoryOnly(reg, memStore)
	}
	if loopProtocolPath == "" {
		loopProtocolPath = loopstate.ProtocolPath(sessionDir, id)
	}
	if p.cfg.EnableLoopProtocol && !p.cfg.EvalMode {
		agent.RegisterLoopProtocolOnly(reg, loopProtocolPath)
	}
	if workflowToolsEnabled(p.cfg) {
		if _, ok := reg.Get(agent.SessionSearchToolName); !ok {
			agent.RegisterSessionSearchOnly(reg, p.sessionRootPath(), id)
		}
	}

	var browser *affentbrowser.Session
	if p.cfg.EnableBrowser {
		bs, err := p.newBrowserSession(workspace)
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
		fetchCfg := p.webFetchConfig(browser)
		if p.cfg.EnableWebSearch {
			if err := affentweb.RegisterAll(reg, affentweb.Options{Fetch: fetchCfg}); err != nil {
				if browser != nil {
					_ = browser.Close()
				}
				_ = os.RemoveAll(workspace)
				return nil, fmt.Errorf("web_search: %w", err)
			}
		} else {
			affentweb.RegisterFetch(reg, fetchCfg)
		}
	}
	// Both subagent and focused-task children inherit shell only when
	// EnableBuiltins is on; read_file / list_files still work via the
	// host fs path without an executor, so a tool-light child is still
	// useful for code reading.
	var childExec executor.Executor
	if p.cfg.EnableBuiltins {
		childExec = localExec
	}
	if p.cfg.EnableSubagent {
		agent.RegisterSubagent(reg, agent.SubagentDeps{
			LLM:                  llm,
			Executor:             childExec,
			HostWorkspaceDir:     workspace,
			Memory:               memStore,
			SessionsDir:          p.sessionRootPath(),
			ParentSessionID:      id,
			TranscriptDir:        filepath.Join(sessionDir, "subagents", id),
			RegisterChildTools:   p.subagentChildToolRegistrar(workspace),
			Log:                  p.logger.With().Str("session_id", id).Logger(),
			MaxDepth:             p.cfg.SubagentMaxDepth,
			SecretValuesProvider: p.accountSecretValues,
		})
	}
	if p.cfg.EnableFocusedTasks {
		agent.RegisterFocusedTasks(reg, agent.FocusedTaskDeps{
			LLM:                  llm,
			Executor:             childExec,
			HostWorkspaceDir:     workspace,
			Memory:               memStore,
			SessionsDir:          p.sessionRootPath(),
			ParentSessionID:      id,
			TranscriptDir:        filepath.Join(sessionDir, "focused-tasks", id),
			Log:                  p.logger.With().Str("session_id", id).Logger(),
			SecretValuesProvider: p.accountSecretValues,
			// Research profile needs external lookup tools; these hooks
			// are nil unless the deployment has opted into web/browser, so
			// availableProfiles() drops research cleanly when neither is on.
			RegisterWebTools:     p.focusedTaskWebRegistrar(),
			RegisterBrowserTools: p.focusedTaskBrowserRegistrar(workspace),
		})
	}
	if err := filterServeEvalModeTools(reg, p.cfg); err != nil {
		_ = os.RemoveAll(workspace)
		if browser != nil {
			_ = browser.Close()
		}
		return nil, err
	}

	// Generous event buffer — chat handler subscribes and drains, but
	// during turn execution we don't want to block the loop on a slow
	// subscriber.
	// Duration knobs already validated by cfg.Validate() at startup, so
	// a parse error here would mean someone bypassed the pipeline —
	// keep the error path for safety but it shouldn't fire.
	perCallTimeout, err := p.cfg.PerCallTimeoutDuration()
	if err != nil {
		_ = os.RemoveAll(workspace)
		if browser != nil {
			_ = browser.Close()
		}
		return nil, fmt.Errorf("per call timeout: %w", err)
	}
	retryBackoff, err := p.cfg.RetryBackoffDuration()
	if err != nil {
		_ = os.RemoveAll(workspace)
		if browser != nil {
			_ = browser.Close()
		}
		return nil, fmt.Errorf("retry backoff: %w", err)
	}

	events := make(chan sse.Event, 1024)
	loop := &agent.Loop{
		LLM:                    llm,
		Tools:                  reg,
		Conv:                   conv,
		Events:                 events,
		Log:                    p.logger.With().Str("session_id", id).Logger(),
		MaxTurnSteps:           p.cfg.MaxTurnSteps,
		FinalNoToolsOnMaxTurns: true,
		PerCallTimeout:         perCallTimeout,
		MaxTransientRetries:    p.cfg.MaxTransientRetries,
		TransientBackoff:       retryBackoff,
		ToolResultArtifactDir: filepath.Join(
			sessionDir,
			".affent",
			"artifacts",
			"tool-results",
		),
		ToolResultArtifactPathPrefix: ".affent/artifacts/tool-results",
		SecretValuesProvider:         p.accountSecretValues,
		// Snapshot source for EnsureSystemPrompt — when nil, the
		// memory block is just omitted from the system prompt and
		// the tool isn't registered above anyway.
		Memory: memStore,
	}
	if workflowToolsEnabled(p.cfg) {
		loop.SkillProvider = agent.SkillProviderForTools(nil, reg)
	}
	if skillReg != nil {
		loop.SkillProvider = agent.SkillProviderForTools(skillReg, reg)
	}
	if planPath != "" {
		loop.SkillProvider = agent.WithActivePlanSkillProvider(planPath, loop.SkillProvider)
	}
	if !p.cfg.EvalMode {
		loop.LoopProtocolPath = loopProtocolPath
		loop.SkillProvider = agent.WithLoopProtocolSkillProviderWithCheckpoint(loopProtocolPath, loopProtocolPlanCheckpointProvider(planPath), loop.SkillProvider)
	}
	if p.cfg.EnableBuiltins && !p.cfg.EvalMode {
		loop.SkillProvider = p.withAccountAccessSkillProvider(loop.SkillProvider)
	}
	if p.cfg.EnableSubagent {
		loop.FirstToolPolicy = agent.SubagentFirstToolPolicy()
		loop.PostToolPolicy = agent.SubagentPostToolPolicy()
		loop.ToolCallPolicies = append(loop.ToolCallPolicies, agent.SubagentExternalResearchPolicy())
	}
	if p.cfg.EnableFocusedTasks {
		if _, ok := reg.Get(agent.FocusedTaskToolName); ok {
			loop.FirstToolPolicies = append(loop.FirstToolPolicies, agent.FocusedTaskFirstToolPolicy())
			loop.PostToolPolicies = append(loop.PostToolPolicies, agent.FocusedTaskPostToolPolicy())
		}
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
	systemPrompt := p.cfg.SystemPrompt
	if systemPrompt == "" {
		systemPrompt = agent.BaseSystemPromptForRegistry(reg)
	}
	systemPrompt = agent.WithRegistrySystemGuidance(systemPrompt, reg)
	systemPrompt = agent.WithRuntimeContextSystemGuidance(systemPrompt, time.Now())
	if serveRegistryHasWorkspaceTool(reg) {
		// Affentserve's per-session workspace is a freshly allocated
		// temp dir. Expose it as a diagnostic binding while keeping the
		// operational rule workspace-relative so the agent doesn't paste
		// long absolute paths into routine shell/file calls.
		systemPrompt += "\n\nWorkspace: \"" + workspace +
			"\". Commands and workspace tools start there by default; prefer relative paths such as `.` or `src/...` and omit cwd unless a different directory is needed. Treat the absolute path as a diagnostic binding, not as the normal command path."
	}
	if err := loop.EnsureSystemPrompt(systemPrompt); err != nil {
		_ = os.RemoveAll(workspace)
		if browser != nil {
			_ = browser.Close()
		}
		return nil, fmt.Errorf("system prompt: %w", err)
	}
	trace, traceFile, nextEventLine, err := openSessionEventLog(sessionDir)
	if err != nil {
		_ = os.RemoveAll(workspace)
		if browser != nil {
			_ = browser.Close()
		}
		return nil, fmt.Errorf("event log: %w", err)
	}

	s := &Session{
		ID:               id,
		loop:             loop,
		conv:             conv,
		llm:              llm,
		registry:         reg,
		skillRegistry:    skillReg,
		events:           events,
		browser:          browser,
		sessionDir:       sessionDir,
		workspace:        workspace,
		loopProtocolPath: loopProtocolPath,
		loopProtocolInit: p.cfg.EnableLoopProtocol && !p.cfg.EvalMode,
		planPath:         planPath,
		createdAt:        time.Now(),
		lastUsed:         time.Now(),
		trace:            trace,
		traceFile:        traceFile,
		nextEventLine:    nextEventLine,
		subs:             map[int]chan sse.Event{},
		closedCh:         make(chan struct{}),
		fanoutDone:       make(chan struct{}),
	}
	if err := s.seedStatsFromEventsFile(filepath.Join(sessionDir, "events.jsonl")); err != nil {
		p.logger.Warn().Err(err).Str("session_id", id).Msg("seed session stats from event log")
	}
	go s.fanout()
	if repairStats := conv.RepairStats(); repairStats.HasAny() {
		s.publishSessionEvent(sse.TypeConversationRepaired, sse.ConversationRepairedPayload{
			SessionID:             id,
			MissingToolResults:    repairStats.MissingToolResults,
			DuplicateToolResults:  repairStats.DuplicateToolResults,
			UnexpectedToolResults: repairStats.UnexpectedToolResults,
			FailureKind:           repairStats.FailureKind(),
			Next:                  repairStats.RecoveryHint(),
		})
	}
	return s, nil
}

func loopProtocolPlanCheckpointProvider(planPath string) agent.LoopProtocolCheckpointProvider {
	if strings.TrimSpace(planPath) == "" {
		return nil
	}
	return func() loopstate.PlanCheckpoint {
		return serveLoopProtocolCurrentPlanCheckpoint(planPath)
	}
}

func serveLoopProtocolCurrentPlanCheckpoint(planPath string) loopstate.PlanCheckpoint {
	if strings.TrimSpace(planPath) == "" {
		return loopstate.PlanCheckpoint{}
	}
	summary, found := planstate.SummarizeFile(planPath)
	if !found || summary.Done || summary.Label == "" ||
		summary.Label == planstate.LabelMissing ||
		summary.Label == planstate.LabelEmpty ||
		summary.Label == planstate.LabelError {
		return loopstate.PlanCheckpoint{Valid: true}
	}
	return loopstate.PlanCheckpoint{
		Valid:      true,
		Label:      summary.Label,
		StepIndex:  summary.CurrentStepIndex,
		StepStatus: summary.CurrentStepStatus,
		Step:       summary.CurrentStep,
	}
}

func openSessionEventLog(sessionDir string) (*eventlog.Recorder, *os.File, int64, error) {
	path := filepath.Join(sessionDir, "events.jsonl")
	if info, err := os.Lstat(path); err == nil {
		if info.IsDir() {
			return nil, nil, 0, fmt.Errorf("events path is a directory")
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, nil, 0, fmt.Errorf("events path must not be a symlink")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, nil, 0, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, nil, 0, err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, nil, 0, err
	}
	rec := eventlog.NewRecorder(f, eventlog.Options{})
	if info.Size() == 0 {
		if err := rec.WriteMeta(); err != nil {
			_ = f.Close()
			return nil, nil, 0, err
		}
		return rec, f, 1, nil
	}
	nextLine, err := countJSONLLines(path)
	if err != nil {
		_ = f.Close()
		return nil, nil, 0, err
	}
	return rec, f, nextLine, nil
}

func countJSONLLines(path string) (int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	reader := bufio.NewReaderSize(f, 64*1024)
	var lines int64
	for {
		_, _, err := jsonl.ReadBoundedLine(reader, maxHistoryLineBytes)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return 0, err
		}
		lines++
	}
	return lines, nil
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
	return filepath.Join(p.sessionRootPath(), id)
}

func (p *SessionPool) sessionRootPath() string {
	root := p.cfg.MemoryRoot
	if root == "" {
		if p.cfg.WorkspaceRoot != "" {
			root = filepath.Join(p.cfg.WorkspaceRoot, "memory")
		} else {
			root = filepath.Join(os.TempDir(), "affentserve-memory")
		}
	}
	return root
}

func (p *SessionPool) userMemoryPath(sessionDir string) string {
	if p != nil && p.cfg.SharedUserMemory {
		return p.sharedUserMemoryPath()
	}
	return filepath.Join(sessionDir, sharedUserMemoryFileName)
}

func (p *SessionPool) sharedUserMemoryPath() string {
	return filepath.Join(p.sessionRootPath(), sharedUserMemoryFileName)
}

func (p *SessionPool) sessionIDConflictsSharedUserMemory(id string) bool {
	return p != nil && p.cfg.SharedUserMemory && filepath.Clean(p.sessionDirPath(id)) == filepath.Clean(p.sharedUserMemoryPath())
}

// allocSessionDir returns the durable per-session-id state dir. Holds
// the JSONL conversation log, runtime-installed skills, and (when
// EnableMemory is on) the memory store files. Unlike allocWorkspace,
// this is STABLE: same id → same path across server restarts and LRU
// evictions, so the chat handler's "the rest of history lives in the
// Conversation log keyed by session_id" contract actually holds — and
// the long-running-memory / runtime-skill promise survives. Callers
// must have already passed id through agent.ValidateSessionID;
// buildSession enforces this at the top.
func (p *SessionPool) allocSessionDir(id string) (string, error) {
	if p.sessionIDConflictsSharedUserMemory(id) {
		return "", fmt.Errorf("session id %q is reserved when shared user memory is enabled", id)
	}
	dir := p.sessionDirPath(id)
	if _, found, err := durableSessionDirInfo(dir); err != nil {
		return "", err
	} else if found {
		return dir, nil
	}
	if fi, err := os.Lstat(dir); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("session dir must not be a symlink: %s", dir)
	} else if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	if fi, err := os.Lstat(dir); err != nil {
		return "", err
	} else if !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("session dir is not a directory: %s", dir)
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
	toolByCallID := map[string]string{}
	defer func() {
		s.subsMu.Lock()
		for id, ch := range s.subs {
			close(ch)
			delete(s.subs, id)
		}
		s.subsMu.Unlock()
		close(s.fanoutDone)
	}()
	for {
		select {
		case <-s.closedCh:
			return
		case ev, ok := <-s.events:
			if !ok {
				return
			}
			derived, hasDerived := s.derivedLoopProtocolActivationEvent(ev, toolByCallID)
			s.dispatchSessionEvent(ev)
			if hasDerived {
				s.dispatchSessionEvent(derived)
			}
		}
	}
}

func (s *Session) dispatchSessionEvent(ev sse.Event) {
	ev.ID = s.nextEventLine
	s.nextEventLine++
	if s.trace != nil {
		if err := s.trace.Write(ev); err != nil {
			s.loop.Log.Warn().Err(err).Str("session_id", s.ID).Msg("event log write")
		}
	}
	s.touch()
	s.observeForStats(ev)
	if ev.Type == sse.TypeTurnEnd {
		s.endTurn()
	}
	s.subsMu.Lock()
	for _, ch := range s.subs {
		select {
		case ch <- ev:
		default:
			// Subscriber is behind. Skip rather than block the loop.
			// SSE clients can reconnect with Last-Event-ID for replay.
		}
	}
	s.subsMu.Unlock()
}

func (s *Session) derivedLoopProtocolActivationEvent(ev sse.Event, toolByCallID map[string]string) (sse.Event, bool) {
	switch ev.Type {
	case sse.TypeToolRequest:
		var p sse.ToolRequestPayload
		if err := json.Unmarshal(ev.Data, &p); err != nil {
			return sse.Event{}, false
		}
		if strings.TrimSpace(p.CallID) != "" {
			toolByCallID[p.CallID] = p.Tool
		}
	case sse.TypeToolResult:
		var p sse.ToolResultPayload
		if err := json.Unmarshal(ev.Data, &p); err != nil {
			return sse.Event{}, false
		}
		tool := toolByCallID[p.CallID]
		delete(toolByCallID, p.CallID)
		if tool != agent.LoopProtocolToolName ||
			p.ExitCode != 0 ||
			!strings.Contains(p.ResultSummary+"\n"+p.Result, "activated LOOP.md status=running") ||
			strings.TrimSpace(s.loopProtocolPath) == "" {
			return sse.Event{}, false
		}
		state, found, err := loopstate.ReadState(filepath.Join(filepath.Dir(s.loopProtocolPath), loopstate.StateFileName))
		if err != nil || !found || state.Status != "running" || state.LastEventType != "loop.protocol_activate" {
			return sse.Event{}, false
		}
		out, err := sse.NewEvent(sse.TypeLoopActivation, sse.LoopProtocolActivationPayload{
			TurnID:          p.TurnID,
			LoopID:          state.LoopID,
			Status:          state.Status,
			ProtocolUpdates: state.ProtocolUpdates,
			ProtocolPath:    loopstate.ProtocolRelPath(s.ID),
			EventSeq:        state.EventCount,
		})
		if err != nil {
			if s.loop != nil {
				s.loop.Log.Warn().Err(err).Str("session_id", s.ID).Msg("encode loop activation event")
			}
			return sse.Event{}, false
		}
		return out, true
	}
	return sse.Event{}, false
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
		if s.fanoutDone != nil {
			<-s.fanoutDone
		}
		if s.browser != nil {
			if err := s.browser.Close(); err != nil {
				s.closeErr = err
			}
		}
		if s.traceFile != nil {
			if err := s.traceFile.Close(); err != nil && s.closeErr == nil {
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
	if p.sessionIDConflictsSharedUserMemory(id) {
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
		if s.isActiveTurn() {
			continue
		}
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

const sessionRetentionReadDirBatch = 128

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
	root := p.sessionRootPath()
	dirFile, err := os.Open(root)
	if err != nil {
		if !os.IsNotExist(err) {
			p.logger.Warn().Err(err).Str("root", root).Msg("retention sweep read")
		}
		return
	}
	defer dirFile.Close()
	cutoff := time.Now().Add(-p.retention)
	deleted := 0
	for {
		entries, err := dirFile.ReadDir(sessionRetentionReadDirBatch)
		if err != nil && !errors.Is(err, io.EOF) {
			p.logger.Warn().Err(err).Str("root", root).Msg("retention sweep read")
			return
		}
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
			if exists, mod, err := durableRegularFileModTime(filepath.Join(dir, "conversation.jsonl")); err != nil {
				continue
			} else if exists {
				mtime = mod
			} else if fi, found, err := durableSessionDirInfo(dir); err == nil && found {
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
		if errors.Is(err, io.EOF) {
			break
		}
	}
	if deleted > 0 {
		p.logger.Info().Int("deleted", deleted).Msg("retention sweep")
	}
}

// Shutdown closes every live in-memory session and stops the GC
// goroutines. Safe to call multiple times. It deliberately preserves
// durable session dirs (conversation log + memory + runtime skills):
// process shutdown and container restart must not behave like an
// explicit user DELETE. Durable state is removed only through Delete
// or the retention sweeper.
//
// Marks the pool as shutting-down before snapshotting sessions so any
// concurrent GetOrCreate fails fast with ErrShuttingDown instead of
// creating sessions that nobody will close.
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
		sessions := make(map[string]*Session, len(p.sessions))
		for id, s := range p.sessions {
			sessions[id] = s
			delete(p.sessions, id)
		}
		p.mu.Unlock()

		close(p.gcStop)
		<-p.gcDone
		close(p.scheduleStop)
		<-p.scheduleDone
		if p.retentionStop != nil {
			close(p.retentionStop)
			<-p.retentionDone
		}

		var wg sync.WaitGroup
		for id, s := range sessions {
			wg.Add(1)
			go func(id string, s *Session) {
				defer wg.Done()
				if err := s.Close(); err != nil {
					p.logger.Warn().Err(err).Str("session_id", id).Msg("session close")
				}
			}(id, s)
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
	return s.SendUserWithOptions(ctx, text, agent.TurnOptions{})
}

// SendUserWithOptions starts one turn with per-turn Loop overrides. Used by
// product modes that need a narrower tool surface without permanently
// changing the session's capabilities.
func (s *Session) SendUserWithOptions(ctx context.Context, text string, opts agent.TurnOptions) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recordLoopProtocolCalibrationAnswerIfReady(text, opts)
	s.activeTurns.Add(1)
	s.lastUsed = time.Now()
	turnID, err := s.loop.SendUserWithOptions(ctx, text, opts)
	if err != nil {
		s.activeTurns.Add(-1)
		return "", err
	}
	return turnID, nil
}

func (s *Session) ensureLoopProtocolInitialized(goal string) error {
	_, err := s.ensureLoopProtocolInitializedWithCreated(goal)
	return err
}

func (s *Session) ensureLoopProtocolInitializedWithCreated(goal string) (bool, error) {
	if s == nil || !s.loopProtocolInit || strings.TrimSpace(s.loopProtocolPath) == "" {
		return false, nil
	}
	created, _, _, err := loopstate.EnsureProtocolTemplate(s.loopProtocolPath, loopstate.ProtocolTemplateOptions{
		LoopID:       s.ID,
		OwnerSession: s.ID,
		Goal:         goal,
		Workspace:    s.workspace,
		Status:       "draft",
		Plan:         serveLoopProtocolCurrentPlanCheckpoint(s.planPath),
	})
	return created, err
}

func shouldRecordLoopProtocolCalibrationAnswer(opts agent.TurnOptions) bool {
	if strings.TrimSpace(opts.UserSource) != "" {
		return false
	}
	switch strings.TrimSpace(opts.UserMode) {
	case "", agent.UserModeNormal:
		return true
	default:
		return false
	}
}

func (s *Session) recordLoopProtocolCalibrationAnswerIfReady(text string, opts agent.TurnOptions) {
	if s == nil || strings.TrimSpace(s.loopProtocolPath) == "" || !shouldRecordLoopProtocolCalibrationAnswer(opts) {
		return
	}
	state, found, err := loopstate.ReadState(filepath.Join(filepath.Dir(s.loopProtocolPath), loopstate.StateFileName))
	if err != nil || !found || state.Status != "draft" {
		return
	}
	var event loopstate.Event
	if state.CalibrationQuestions > 0 {
		if state.CalibrationAnswers >= state.CalibrationQuestions {
			return
		}
		state, event, err = loopstate.RecordProtocolCalibrationAnswer(s.loopProtocolPath, text)
		if err != nil {
			s.loop.Log.Warn().Err(err).Str("session_id", s.ID).Msg("record loop protocol calibration")
			return
		}
		s.publishSessionEvent(sse.TypeLoopCalibration, sse.LoopProtocolCalibrationPayload{
			LoopID:                  state.LoopID,
			Status:                  state.Status,
			CalibrationQuestions:    state.CalibrationQuestions,
			LastCalibrationQuestion: state.LastCalibrationQuestion,
			CalibrationAnswers:      state.CalibrationAnswers,
			LastCalibrationAnswer:   state.LastCalibrationAnswer,
			ProtocolPath:            loopstate.ProtocolRelPath(s.ID),
			EventSeq:                event.Seq,
		})
		return
	}
}

func (s *Session) publishSessionEvent(eventType string, payload any) {
	if s == nil || s.events == nil {
		return
	}
	ev, err := sse.NewEvent(eventType, payload)
	if err != nil {
		if s.loop != nil {
			s.loop.Log.Warn().Err(err).Str("type", eventType).Msg("encode session event")
		}
		return
	}
	select {
	case s.events <- ev:
	default:
		if s.loop != nil {
			s.loop.Log.Warn().Str("type", eventType).Msg("session event channel full; dropped")
		}
	}
}

func (s *Session) isActiveTurn() bool {
	return s != nil && s.activeTurns.Load() > 0
}

func (s *Session) endTurn() {
	if s == nil {
		return
	}
	for {
		n := s.activeTurns.Load()
		if n <= 0 {
			return
		}
		if s.activeTurns.CompareAndSwap(n, n-1) {
			s.touch()
			return
		}
	}
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
	BlockedByType     int64 `json:"blocked_by_type"`
	BlockedByDomain   int64 `json:"blocked_by_domain"`
	DomainRelaxations int64 `json:"domain_relaxations"`
	CacheHit          int64 `json:"cache_hit"`
	CacheMiss         int64 `json:"cache_miss"`
	NetworkFetch      int64 `json:"network_fetch"`
}

func (s *Session) BrowserStatsSnapshot() BrowserStatsSnapshot {
	if s == nil || s.browser == nil {
		return BrowserStatsSnapshot{}
	}
	bt, bd, dr, ch, cm, nf := s.browser.InterceptStats()
	return BrowserStatsSnapshot{
		BlockedByType:     bt,
		BlockedByDomain:   bd,
		DomainRelaxations: dr,
		CacheHit:          ch,
		CacheMiss:         cm,
		NetworkFetch:      nf,
	}
}

// UsageSnapshot returns the per-session token totals + turn count
// accumulated by observeForStats. Atomic loads so /v1/stats can
// read without grabbing s.mu — the counters are advisory and a
// torn read (input updated, output not yet) is at most a few
// tokens off, which doesn't matter for a polling stats surface.
type UsageSnapshot struct {
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
	Turns        int64 `json:"turns"`
}

func (s *Session) UsageSnapshot() UsageSnapshot {
	if s == nil {
		return UsageSnapshot{}
	}
	return UsageSnapshot{
		InputTokens:  s.inputTokens.Load(),
		OutputTokens: s.outputTokens.Load(),
		Turns:        s.turns.Load(),
	}
}

// ToolStatsSnapshot returns per-session runtime tool counters accumulated from
// turn.end.tool_stats. The values are advisory, cheap polling counters for
// operators and WebUI surfaces; the event log remains the source of truth.
type ToolStatsSnapshot struct {
	ToolRequests           int64            `json:"tool_requests"`
	ToolNameCanonicalized  int64            `json:"tool_name_canonicalized"`
	ToolArgsRepaired       int64            `json:"tool_args_repaired"`
	ToolRepairCalls        int64            `json:"tool_repair_calls"`
	ToolRepairSucceeded    int64            `json:"tool_repair_succeeded"`
	ToolRepairFailed       int64            `json:"tool_repair_failed"`
	ToolRepairNotes        int64            `json:"tool_repair_notes"`
	ToolRepairByKind       map[string]int64 `json:"tool_repair_by_kind,omitempty"`
	ToolFailureByKind      map[string]int64 `json:"tool_failure_by_kind,omitempty"`
	ToolErrors             int64            `json:"tool_errors"`
	ToolDurationMS         int64            `json:"tool_duration_ms"`
	LoopGuardInterventions int64            `json:"loop_guard_interventions"`
	ForcedNoTools          int64            `json:"forced_no_tools"`
	SourceAccessResults    int64            `json:"source_access_results"`
	SourceAccessVerified   int64            `json:"source_access_verified"`
	SourceAccessDiscovery  int64            `json:"source_access_discovery_only"`
	SourceAccessNetwork    int64            `json:"source_access_network"`
	SourceAccessDynamic    int64            `json:"source_access_dynamic_partial"`
	MemoryUpdates          int64            `json:"memory_updates"`
	MemoryUpdateAdd        int64            `json:"memory_update_add"`
	MemoryUpdateReplace    int64            `json:"memory_update_replace"`
	MemoryUpdateRemove     int64            `json:"memory_update_remove"`
	MemorySearchCalls      int64            `json:"memory_search_calls"`
	MemorySearchMisses     int64            `json:"memory_search_misses"`
	SessionSearchCalls     int64            `json:"session_search_calls"`
	SessionSearchResults   int64            `json:"session_search_results"`
	SessionSearchContext   int64            `json:"session_search_context_hits"`
	SessionSearchTerms     int64            `json:"session_search_matched_terms"`
	SessionSearchRecent    int64            `json:"session_search_recent_sessions"`
	ToolContextTruncated   int64            `json:"tool_context_truncated"`
	ToolContextOmitted     int64            `json:"tool_context_omitted_bytes"`
	PlanCalls              int64            `json:"plan_calls,omitempty"`
	PlanByAction           map[string]int64 `json:"plan_by_action,omitempty"`
	PlanErrors             int64            `json:"plan_errors,omitempty"`
	FocusedTaskCalls       int64            `json:"focused_task_calls,omitempty"`
	FocusedTaskByType      map[string]int64 `json:"focused_task_by_type,omitempty"`
	FocusedTaskErrors      int64            `json:"focused_task_errors,omitempty"`
	SubagentCalls          int64            `json:"subagent_calls,omitempty"`
	SubagentByMode         map[string]int64 `json:"subagent_by_mode,omitempty"`
	SubagentErrors         int64            `json:"subagent_errors,omitempty"`
}

func (s *Session) ToolStatsSnapshot() ToolStatsSnapshot {
	if s == nil {
		return ToolStatsSnapshot{}
	}
	s.toolRepairMu.Lock()
	repairByKind := cloneStringInt64Map(s.toolRepairByKind)
	failureByKind := cloneStringInt64Map(s.toolFailureByKind)
	s.toolRepairMu.Unlock()
	s.toolGovernanceMu.Lock()
	planByAction := cloneStringInt64Map(s.planByAction)
	focusedTaskByType := cloneStringInt64Map(s.focusedTaskByType)
	subagentByMode := cloneStringInt64Map(s.subagentByMode)
	s.toolGovernanceMu.Unlock()
	return ToolStatsSnapshot{
		ToolRequests:           s.toolRequests.Load(),
		ToolNameCanonicalized:  s.toolNameCanonicalized.Load(),
		ToolArgsRepaired:       s.toolArgsRepaired.Load(),
		ToolRepairCalls:        s.toolRepairCalls.Load(),
		ToolRepairSucceeded:    s.toolRepairSucceeded.Load(),
		ToolRepairFailed:       s.toolRepairFailed.Load(),
		ToolRepairNotes:        s.toolRepairNotes.Load(),
		ToolRepairByKind:       repairByKind,
		ToolFailureByKind:      failureByKind,
		ToolErrors:             s.toolErrors.Load(),
		ToolDurationMS:         s.toolDurationMS.Load(),
		LoopGuardInterventions: s.loopGuardInterventions.Load(),
		ForcedNoTools:          s.forcedNoTools.Load(),
		SourceAccessResults:    s.sourceAccessResults.Load(),
		SourceAccessVerified:   s.sourceAccessVerified.Load(),
		SourceAccessDiscovery:  s.sourceAccessDiscovery.Load(),
		SourceAccessNetwork:    s.sourceAccessNetwork.Load(),
		SourceAccessDynamic:    s.sourceAccessDynamic.Load(),
		MemoryUpdates:          s.memoryUpdates.Load(),
		MemoryUpdateAdd:        s.memoryUpdateAdd.Load(),
		MemoryUpdateReplace:    s.memoryUpdateReplace.Load(),
		MemoryUpdateRemove:     s.memoryUpdateRemove.Load(),
		MemorySearchCalls:      s.memorySearchCalls.Load(),
		MemorySearchMisses:     s.memorySearchMisses.Load(),
		SessionSearchCalls:     s.sessionSearchCalls.Load(),
		SessionSearchResults:   s.sessionSearchResults.Load(),
		SessionSearchContext:   s.sessionSearchContext.Load(),
		SessionSearchTerms:     s.sessionSearchTerms.Load(),
		SessionSearchRecent:    s.sessionSearchRecent.Load(),
		ToolContextTruncated:   s.toolContextTruncated.Load(),
		ToolContextOmitted:     s.toolContextOmitted.Load(),
		PlanCalls:              s.planCalls.Load(),
		PlanByAction:           planByAction,
		PlanErrors:             s.planErrors.Load(),
		FocusedTaskCalls:       s.focusedTaskCalls.Load(),
		FocusedTaskByType:      focusedTaskByType,
		FocusedTaskErrors:      s.focusedTaskErrors.Load(),
		SubagentCalls:          s.subagentCalls.Load(),
		SubagentByMode:         subagentByMode,
		SubagentErrors:         s.subagentErrors.Load(),
	}
}

// RuntimeStatsSnapshot returns non-tool runtime outcomes accumulated from
// turn.end and error events. This is intentionally separate from ToolStats:
// a missing final answer can be a turn budget outcome, while LLM stream
// failures are runtime errors, not browser/web_fetch failures.
type RuntimeStatsSnapshot struct {
	TurnEndByReason                  map[string]int64 `json:"turn_end_by_reason,omitempty"`
	RuntimeErrors                    int64            `json:"runtime_errors"`
	RuntimeErrorByKind               map[string]int64 `json:"runtime_error_by_kind,omitempty"`
	ContextCompactions               int64            `json:"context_compactions,omitempty"`
	ContextCompactionsReactive       int64            `json:"context_compactions_reactive,omitempty"`
	ContextCompactionRemovedMessages int64            `json:"context_compaction_removed_messages,omitempty"`
	ContextCompactionSummaryBytes    int64            `json:"context_compaction_summary_bytes,omitempty"`
	ContextCompactionSummaryMissing  int64            `json:"context_compaction_summary_missing,omitempty"`
	ContextCompactionSummaryEmpty    int64            `json:"context_compaction_summary_empty,omitempty"`
	ContextCompactionLatestReason    string           `json:"context_compaction_latest_reason,omitempty"`
	ContextCompactionLatestReactive  bool             `json:"context_compaction_latest_reactive,omitempty"`
	ContextCompactionLatestState     string           `json:"context_compaction_latest_summary_state,omitempty"`
}

func (s *Session) RuntimeStatsSnapshot() RuntimeStatsSnapshot {
	if s == nil {
		return RuntimeStatsSnapshot{}
	}
	s.runtimeStatsMu.Lock()
	turnEndByReason := cloneStringInt64Map(s.turnEndByReason)
	errorByKind := cloneStringInt64Map(s.runtimeErrorByKind)
	latestReason := s.contextCompactionLastReason
	latestReactive := s.contextCompactionLastReactive
	latestState := s.contextCompactionLastSummaryState
	s.runtimeStatsMu.Unlock()
	return RuntimeStatsSnapshot{
		TurnEndByReason:                  turnEndByReason,
		RuntimeErrors:                    s.runtimeErrors.Load(),
		RuntimeErrorByKind:               errorByKind,
		ContextCompactions:               s.contextCompactions.Load(),
		ContextCompactionsReactive:       s.contextCompactionReact.Load(),
		ContextCompactionRemovedMessages: s.contextCompactionRmMsg.Load(),
		ContextCompactionSummaryBytes:    s.contextCompactionBytes.Load(),
		ContextCompactionSummaryMissing:  s.contextCompactionMiss.Load(),
		ContextCompactionSummaryEmpty:    s.contextCompactionEmpty.Load(),
		ContextCompactionLatestReason:    latestReason,
		ContextCompactionLatestReactive:  latestReactive,
		ContextCompactionLatestState:     latestState,
	}
}

// observeForStats updates per-session counters from events flowing
// through fanout. Called once per event, before subscribers receive
// it, so the counters reflect everything the Loop emitted regardless
// of subscriber liveness. Misshapen JSON in the Usage payload is
// dropped silently — it's a stats surface, not a correctness one.
func (s *Session) observeForStats(ev sse.Event) {
	switch ev.Type {
	case sse.TypeUsage:
		var p sse.UsagePayload
		if err := json.Unmarshal(ev.Data, &p); err != nil {
			return
		}
		if p.InputTokens > 0 {
			s.inputTokens.Add(int64(p.InputTokens))
		}
		if p.OutputTokens > 0 {
			s.outputTokens.Add(int64(p.OutputTokens))
		}
	case sse.TypeTurnEnd:
		var p sse.TurnEndPayload
		if err := json.Unmarshal(ev.Data, &p); err != nil {
			return
		}
		// One turn-end per turn (regardless of reason). Count even
		// max_turns / error / cancelled — operators tracking spend
		// usually want to know how many turns the session has gone
		// through, completed or not.
		s.turns.Add(1)
		s.addTurnEndReason(p.Reason)
		if p.ToolStats == nil {
			return
		}
		s.addToolStats(*p.ToolStats)
	case sse.TypeToolRequest:
		var p sse.ToolRequestPayload
		if err := json.Unmarshal(ev.Data, &p); err != nil {
			return
		}
		if class, ok := classifySessionToolGovernanceRequest(p); ok {
			s.addToolGovernanceRequest(p.CallID, class)
		}
	case sse.TypeToolResult:
		var p sse.ToolResultPayload
		if err := json.Unmarshal(ev.Data, &p); err != nil {
			return
		}
		s.addToolGovernanceResult(p)
	case sse.TypeError:
		var p sse.ErrorPayload
		if err := json.Unmarshal(ev.Data, &p); err != nil {
			return
		}
		s.addRuntimeError(p.FailureKind)
	case sse.TypeContextCompact:
		var p sse.ContextCompactPayload
		if err := json.Unmarshal(ev.Data, &p); err != nil {
			return
		}
		s.addContextCompaction(p)
	}
}

func (s *Session) seedStatsFromEventsFile(path string) error {
	usage, err := usageSummaryFromEventsFile(path)
	if err != nil {
		return err
	}
	if usage != nil {
		s.addUsageSnapshot(*usage)
	}
	tools, err := toolStatsSummaryFromEventsFile(path)
	if err != nil {
		return err
	}
	if tools != nil {
		s.addToolStatsSnapshot(*tools)
	}
	runtime, err := runtimeStatsSummaryFromEventsFile(path)
	if err != nil {
		return err
	}
	if runtime != nil {
		s.addRuntimeStatsSnapshot(*runtime)
	}
	return nil
}

func (s *Session) addUsageSnapshot(stats UsageSnapshot) {
	s.inputTokens.Add(positiveInt64(stats.InputTokens))
	s.outputTokens.Add(positiveInt64(stats.OutputTokens))
	s.turns.Add(positiveInt64(stats.Turns))
}

func (s *Session) addTurnEndReason(reason string) {
	if reason == "" {
		reason = "unknown"
	}
	s.runtimeStatsMu.Lock()
	defer s.runtimeStatsMu.Unlock()
	if s.turnEndByReason == nil {
		s.turnEndByReason = map[string]int64{}
	}
	s.turnEndByReason[reason]++
}

func (s *Session) addRuntimeError(kind string) {
	s.runtimeErrors.Add(1)
	if kind == "" {
		return
	}
	s.runtimeStatsMu.Lock()
	defer s.runtimeStatsMu.Unlock()
	if s.runtimeErrorByKind == nil {
		s.runtimeErrorByKind = map[string]int64{}
	}
	s.runtimeErrorByKind[kind]++
}

func (s *Session) addContextCompaction(p sse.ContextCompactPayload) {
	s.contextCompactions.Add(1)
	if p.Reactive {
		s.contextCompactionReact.Add(1)
	}
	if p.RemovedMessages > 0 {
		s.contextCompactionRmMsg.Add(int64(p.RemovedMessages))
	}
	if p.SummaryBytes > 0 {
		s.contextCompactionBytes.Add(int64(p.SummaryBytes))
	}
	state := contextCompactSummaryState(p.SummaryPresent, p.SummaryBytes, p.SummaryPreview, true)
	switch state {
	case "missing":
		s.contextCompactionMiss.Add(1)
	case "empty":
		s.contextCompactionEmpty.Add(1)
	}
	s.runtimeStatsMu.Lock()
	s.contextCompactionLastReason = p.Reason
	s.contextCompactionLastReactive = p.Reactive
	s.contextCompactionLastSummaryState = state
	s.runtimeStatsMu.Unlock()
}

func (s *Session) addRuntimeStatsSnapshot(stats RuntimeStatsSnapshot) {
	s.runtimeErrors.Add(positiveInt64(stats.RuntimeErrors))
	s.contextCompactions.Add(positiveInt64(stats.ContextCompactions))
	s.contextCompactionReact.Add(positiveInt64(stats.ContextCompactionsReactive))
	s.contextCompactionRmMsg.Add(positiveInt64(stats.ContextCompactionRemovedMessages))
	s.contextCompactionBytes.Add(positiveInt64(stats.ContextCompactionSummaryBytes))
	s.contextCompactionMiss.Add(positiveInt64(stats.ContextCompactionSummaryMissing))
	s.contextCompactionEmpty.Add(positiveInt64(stats.ContextCompactionSummaryEmpty))

	s.runtimeStatsMu.Lock()
	defer s.runtimeStatsMu.Unlock()
	if len(stats.TurnEndByReason) > 0 {
		if s.turnEndByReason == nil {
			s.turnEndByReason = make(map[string]int64, len(stats.TurnEndByReason))
		}
		addStringInt64Counts(s.turnEndByReason, stats.TurnEndByReason)
	}
	if len(stats.RuntimeErrorByKind) > 0 {
		if s.runtimeErrorByKind == nil {
			s.runtimeErrorByKind = make(map[string]int64, len(stats.RuntimeErrorByKind))
		}
		addStringInt64Counts(s.runtimeErrorByKind, stats.RuntimeErrorByKind)
	}
	if stats.ContextCompactions > 0 || stats.ContextCompactionLatestReason != "" || stats.ContextCompactionLatestState != "" {
		s.contextCompactionLastReason = stats.ContextCompactionLatestReason
		s.contextCompactionLastReactive = stats.ContextCompactionLatestReactive
		s.contextCompactionLastSummaryState = stats.ContextCompactionLatestState
	}
}

func (s *Session) addToolStats(stats sse.ToolRuntimeStats) {
	s.toolRequests.Add(int64(stats.ToolRequests))
	s.toolNameCanonicalized.Add(int64(stats.ToolNameCanonicalized))
	s.toolArgsRepaired.Add(int64(stats.ToolArgsRepaired))
	s.toolRepairCalls.Add(int64(stats.ToolRepairCalls))
	s.toolRepairSucceeded.Add(int64(stats.ToolRepairSucceeded))
	s.toolRepairFailed.Add(int64(stats.ToolRepairFailed))
	s.toolRepairNotes.Add(int64(stats.ToolRepairNotes))
	s.toolErrors.Add(int64(stats.ToolErrors))
	s.toolDurationMS.Add(stats.ToolDurationMS)
	s.loopGuardInterventions.Add(int64(stats.LoopGuardInterventions))
	s.forcedNoTools.Add(int64(stats.ForcedNoTools))
	s.sourceAccessResults.Add(int64(stats.SourceAccessResults))
	s.sourceAccessVerified.Add(int64(stats.SourceAccessVerified))
	s.sourceAccessDiscovery.Add(int64(stats.SourceAccessDiscoveryOnly))
	s.sourceAccessNetwork.Add(int64(stats.SourceAccessNetwork))
	s.sourceAccessDynamic.Add(int64(stats.SourceAccessDynamicPartial))
	s.memoryUpdates.Add(int64(stats.MemoryUpdates))
	s.memoryUpdateAdd.Add(int64(stats.MemoryUpdateAdd))
	s.memoryUpdateReplace.Add(int64(stats.MemoryUpdateReplace))
	s.memoryUpdateRemove.Add(int64(stats.MemoryUpdateRemove))
	s.memorySearchCalls.Add(int64(stats.MemorySearchCalls))
	s.memorySearchMisses.Add(int64(stats.MemorySearchMisses))
	s.sessionSearchCalls.Add(int64(stats.SessionSearchCalls))
	s.sessionSearchResults.Add(int64(stats.SessionSearchResults))
	s.sessionSearchContext.Add(int64(stats.SessionSearchContextHits))
	s.sessionSearchTerms.Add(int64(stats.SessionSearchMatchedTerms))
	s.sessionSearchRecent.Add(int64(stats.SessionSearchRecent))
	s.toolContextTruncated.Add(int64(stats.ToolContextTruncated))
	s.toolContextOmitted.Add(int64(stats.ToolContextOmittedBytes))
	if len(stats.ToolRepairByKind) > 0 {
		s.addToolRepairKinds(stats.ToolRepairByKind)
	}
	if len(stats.ToolFailureByKind) > 0 {
		s.addToolFailureKinds(stats.ToolFailureByKind)
	}
}

func (s *Session) addToolStatsSnapshot(stats ToolStatsSnapshot) {
	s.toolRequests.Add(positiveInt64(stats.ToolRequests))
	s.toolNameCanonicalized.Add(positiveInt64(stats.ToolNameCanonicalized))
	s.toolArgsRepaired.Add(positiveInt64(stats.ToolArgsRepaired))
	s.toolRepairCalls.Add(positiveInt64(stats.ToolRepairCalls))
	s.toolRepairSucceeded.Add(positiveInt64(stats.ToolRepairSucceeded))
	s.toolRepairFailed.Add(positiveInt64(stats.ToolRepairFailed))
	s.toolRepairNotes.Add(positiveInt64(stats.ToolRepairNotes))
	s.toolErrors.Add(positiveInt64(stats.ToolErrors))
	s.toolDurationMS.Add(positiveInt64(stats.ToolDurationMS))
	s.loopGuardInterventions.Add(positiveInt64(stats.LoopGuardInterventions))
	s.forcedNoTools.Add(positiveInt64(stats.ForcedNoTools))
	s.sourceAccessResults.Add(positiveInt64(stats.SourceAccessResults))
	s.sourceAccessVerified.Add(positiveInt64(stats.SourceAccessVerified))
	s.sourceAccessDiscovery.Add(positiveInt64(stats.SourceAccessDiscovery))
	s.sourceAccessNetwork.Add(positiveInt64(stats.SourceAccessNetwork))
	s.sourceAccessDynamic.Add(positiveInt64(stats.SourceAccessDynamic))
	s.memoryUpdates.Add(positiveInt64(stats.MemoryUpdates))
	s.memoryUpdateAdd.Add(positiveInt64(stats.MemoryUpdateAdd))
	s.memoryUpdateReplace.Add(positiveInt64(stats.MemoryUpdateReplace))
	s.memoryUpdateRemove.Add(positiveInt64(stats.MemoryUpdateRemove))
	s.memorySearchCalls.Add(positiveInt64(stats.MemorySearchCalls))
	s.memorySearchMisses.Add(positiveInt64(stats.MemorySearchMisses))
	s.sessionSearchCalls.Add(positiveInt64(stats.SessionSearchCalls))
	s.sessionSearchResults.Add(positiveInt64(stats.SessionSearchResults))
	s.sessionSearchContext.Add(positiveInt64(stats.SessionSearchContext))
	s.sessionSearchTerms.Add(positiveInt64(stats.SessionSearchTerms))
	s.sessionSearchRecent.Add(positiveInt64(stats.SessionSearchRecent))
	s.toolContextTruncated.Add(positiveInt64(stats.ToolContextTruncated))
	s.toolContextOmitted.Add(positiveInt64(stats.ToolContextOmitted))
	s.planCalls.Add(positiveInt64(stats.PlanCalls))
	s.planErrors.Add(positiveInt64(stats.PlanErrors))
	s.focusedTaskCalls.Add(positiveInt64(stats.FocusedTaskCalls))
	s.focusedTaskErrors.Add(positiveInt64(stats.FocusedTaskErrors))
	s.subagentCalls.Add(positiveInt64(stats.SubagentCalls))
	s.subagentErrors.Add(positiveInt64(stats.SubagentErrors))

	s.toolRepairMu.Lock()
	if len(stats.ToolRepairByKind) > 0 {
		if s.toolRepairByKind == nil {
			s.toolRepairByKind = make(map[string]int64, len(stats.ToolRepairByKind))
		}
		addStringInt64Counts(s.toolRepairByKind, stats.ToolRepairByKind)
	}
	if len(stats.ToolFailureByKind) > 0 {
		if s.toolFailureByKind == nil {
			s.toolFailureByKind = make(map[string]int64, len(stats.ToolFailureByKind))
		}
		addStringInt64Counts(s.toolFailureByKind, stats.ToolFailureByKind)
	}
	s.toolRepairMu.Unlock()

	s.toolGovernanceMu.Lock()
	defer s.toolGovernanceMu.Unlock()
	if len(stats.PlanByAction) > 0 {
		if s.planByAction == nil {
			s.planByAction = make(map[string]int64, len(stats.PlanByAction))
		}
		addStringInt64Counts(s.planByAction, stats.PlanByAction)
	}
	if len(stats.FocusedTaskByType) > 0 {
		if s.focusedTaskByType == nil {
			s.focusedTaskByType = make(map[string]int64, len(stats.FocusedTaskByType))
		}
		addStringInt64Counts(s.focusedTaskByType, stats.FocusedTaskByType)
	}
	if len(stats.SubagentByMode) > 0 {
		if s.subagentByMode == nil {
			s.subagentByMode = make(map[string]int64, len(stats.SubagentByMode))
		}
		addStringInt64Counts(s.subagentByMode, stats.SubagentByMode)
	}
}

func (s *Session) addToolGovernanceRequest(callID string, class sessionToolGovernanceClass) {
	if class.Kind == "" {
		return
	}
	switch class.Kind {
	case sessionToolGovernancePlan:
		s.planCalls.Add(1)
	case sessionToolGovernanceFocusedTask:
		s.focusedTaskCalls.Add(1)
	case sessionToolGovernanceSubagent:
		s.subagentCalls.Add(1)
	default:
		return
	}

	s.toolGovernanceMu.Lock()
	defer s.toolGovernanceMu.Unlock()
	if callID != "" {
		if s.toolGovernanceCallsByID == nil {
			s.toolGovernanceCallsByID = map[string]sessionToolGovernanceClass{}
		}
		s.toolGovernanceCallsByID[callID] = class
	}
	s.addToolGovernanceBucketLocked(class)
}

func (s *Session) addToolGovernanceResult(p sse.ToolResultPayload) {
	class, ok := classifySessionToolGovernanceResult(p)
	if p.CallID != "" {
		s.toolGovernanceMu.Lock()
		storedClass, storedOK := s.toolGovernanceCallsByID[p.CallID]
		if storedOK {
			delete(s.toolGovernanceCallsByID, p.CallID)
		}
		s.toolGovernanceMu.Unlock()
		if !ok && storedOK {
			class, ok = storedClass, true
		}
	}
	if !ok || p.ExitCode == 0 {
		return
	}
	switch class.Kind {
	case sessionToolGovernancePlan:
		s.planErrors.Add(1)
	case sessionToolGovernanceFocusedTask:
		s.focusedTaskErrors.Add(1)
	case sessionToolGovernanceSubagent:
		s.subagentErrors.Add(1)
	}
}

func (s *Session) addToolGovernanceBucketLocked(class sessionToolGovernanceClass) {
	switch class.Kind {
	case sessionToolGovernancePlan:
		if s.planByAction == nil {
			s.planByAction = map[string]int64{}
		}
		s.planByAction[class.Bucket]++
	case sessionToolGovernanceFocusedTask:
		if s.focusedTaskByType == nil {
			s.focusedTaskByType = map[string]int64{}
		}
		s.focusedTaskByType[class.Bucket]++
	case sessionToolGovernanceSubagent:
		if s.subagentByMode == nil {
			s.subagentByMode = map[string]int64{}
		}
		s.subagentByMode[class.Bucket]++
	}
}

func (s *Session) addToolRepairKinds(counts map[string]int) {
	s.toolRepairMu.Lock()
	defer s.toolRepairMu.Unlock()
	if s.toolRepairByKind == nil {
		s.toolRepairByKind = make(map[string]int64, len(counts))
	}
	for kind, count := range counts {
		if count > 0 {
			s.toolRepairByKind[kind] += int64(count)
		}
	}
}

func (s *Session) addToolFailureKinds(counts map[string]int) {
	s.toolRepairMu.Lock()
	defer s.toolRepairMu.Unlock()
	if s.toolFailureByKind == nil {
		s.toolFailureByKind = make(map[string]int64, len(counts))
	}
	for kind, count := range counts {
		if count > 0 {
			s.toolFailureByKind[kind] += int64(count)
		}
	}
}

func addStringInt64Counts(dst map[string]int64, src map[string]int64) {
	for key, count := range src {
		if count > 0 {
			dst[key] += count
		}
	}
}

func cloneStringInt64Map(in map[string]int64) map[string]int64 {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]int64, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// Workspace exposes the session's on-disk directory for tests that
// want to inspect the JSONL conversation log. Not used by production
// paths.
func (s *Session) Workspace() string { return s.workspace }
