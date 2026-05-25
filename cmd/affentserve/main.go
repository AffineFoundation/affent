package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/affinefoundation/affent/internal/agent"
	"github.com/rs/zerolog"
)

const (
	exitOK          = 0
	exitUsage       = 2
	exitConfig      = 3
	exitServerCrash = 4
)

func main() {
	logger := zerolog.New(zerolog.ConsoleWriter{
		Out: os.Stderr, TimeFormat: time.RFC3339,
	}).With().Timestamp().Logger()

	if err := loadDotEnv(); err != nil {
		fmt.Fprintf(os.Stderr, "affentserve: load .env: %v\n", err)
		os.Exit(exitConfig)
	}

	cfg, err := parseFlagsAndConfig(os.Args[1:])
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(exitOK)
		}
		fmt.Fprintf(os.Stderr, "affentserve: %v\n", err)
		os.Exit(exitConfig)
	}

	if err := run(cfg, logger); err != nil {
		fmt.Fprintf(os.Stderr, "affentserve: %v\n", err)
		os.Exit(exitServerCrash)
	}
}

// parseFlagsAndConfig merges --config (lowest priority) with CLI flags
// and env vars (highest), then Resolve+Validate.
func parseFlagsAndConfig(argv []string) (Config, error) {
	fs := flag.NewFlagSet("affentserve", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	var (
		configPath         = fs.String("config", "", "Path to JSON config file (CLI flags override).")
		listen             = fs.String("listen", "", "Address to listen on (default 127.0.0.1:7777).")
		baseURL            = fs.String("base-url", "", "Upstream OpenAI-compatible chat completions URL (env: AFFENTSERVE_BASE_URL).")
		apiKey             = fs.String("api-key", "", "API key for --base-url (env: AFFENTSERVE_API_KEY, DASHSCOPE_API_KEY).")
		model              = fs.String("model", "", "Default model id reported by /v1/models and used when a request omits 'model' (env: AFFENTSERVE_MODEL).")
		authToken          = fs.String("auth-token", "", "Optional bearer token gating the server itself (env: AFFENTSERVE_AUTH_TOKEN).")
		workspaceRoot      = fs.String("workspace-root", "", "Parent directory for per-session workspaces. Empty creates per-session temp dirs.")
		memoryRoot         = fs.String("memory-root", "", "Parent directory for durable per-session state: conversation log, event log, runtime skills, and memory. Empty defaults under --workspace-root. Env: AFFENTSERVE_MEMORY_ROOT.")
		maxSessions        = fs.Int("max-sessions", 0, "LRU upper bound on in-memory sessions (default 32). Env: AFFENTSERVE_MAX_SESSIONS.")
		sessionIdleTTL     = fs.String("session-idle-ttl", "", "Positive duration for how long an idle session stays in the pool before GC (default 10m). Env: AFFENTSERVE_SESSION_IDLE_TTL.")
		sessionRetention   = fs.String("session-retention", "", "How long durable session dirs (conv log + memory) live on disk after last activity. Empty disables — dirs live until explicit DELETE. Set to a Go duration like '720h' (30d) to enable background GC.")
		maxTurnSteps       = fs.Int("max-turn-steps", 0, "Per-turn step cap (assistant↔tool round trips). 0 = agent runtime's default. Env: AFFENTSERVE_MAX_TURN_STEPS.")
		perCallTimeout     = fs.String("per-call-timeout", "", "Per-LLM-call timeout as a Go duration string (default 3m). Bump for reasoning models that may think for several minutes per call. Env: AFFENTSERVE_PER_CALL_TIMEOUT.")
		maxRetries         = fs.Int("max-transient-retries", 0, "Retry budget for transient LLM failures (5xx/429/408/net/EOF/timeout). 0 = agent runtime's default (3); negative disables retries. Env: AFFENTSERVE_MAX_TRANSIENT_RETRIES.")
		retryBackoff       = fs.String("retry-backoff", "", "Initial backoff between transient-error retries (default 4s); each subsequent attempt doubles it. Go duration string. Env: AFFENTSERVE_RETRY_BACKOFF.")
		compactTrigger     = fs.Int("compact-trigger", 0, "Rolling-summary compactor's message threshold per session. 0 = agent runtime's default (240). Lower on small-context upstream models to compact earlier. Env: AFFENTSERVE_COMPACT_TRIGGER.")
		compactKeepLast    = fs.Int("compact-keep-last", 0, "Messages preserved verbatim at the tail of the conversation when compacting. 0 = agent runtime's default (10). Env: AFFENTSERVE_COMPACT_KEEP_LAST.")
		enableBrowser      = fs.Bool("browser", false, "Register the extras/browser tool family for each new session. Env: AFFENTSERVE_BROWSER.")
		enableWeb          = fs.Bool("web", false, "Register extras/web's web_fetch tool. Env: AFFENTSERVE_WEB.")
		enableWebSearch    = fs.Bool("web-search", false, "Register web_search alongside web_fetch (requires TAVILY_API_KEY by default). Env: AFFENTSERVE_WEB_SEARCH.")
		enableMemory       = fs.Bool("memory", true, "Register agent runtime's memory tool.")
		enableBuiltins     = fs.Bool("builtins", false, "Register shell + file builtins (LocalExecutor). DANGEROUS on a shared host — only enable in a sandboxed environment. Env: AFFENTSERVE_BUILTINS.")
		evalMode           = fs.Bool("eval-mode", false, "Strict benchmark mode: disable skills, plan, subagent, focused tasks, dynamic workflow injection, memory, and environment tools by default; opt into needed env permissions with --browser=true, --web=true, or --memory=true. Env: AFFENTSERVE_EVAL_MODE.")
		enableSubagent     = fs.Bool("subagent", true, "Register the subagent_run tool — a bounded isolated Loop with read-only inspection tools. Doesn't require --builtins but inherits the shell tool when --builtins is also on.")
		subagentMaxDepth   = fs.Int("subagent-max-depth", agent.DefaultSubagentMaxDepth, "Maximum recursive subagent depth; 1 disables nested subagents, hard max 4. Env: AFFENTSERVE_SUBAGENT_MAX_DEPTH.")
		enableFocusedTasks = fs.Bool("focused-tasks", true, "Register the run_task tool — bounded focused tasks (recall/explore/research/verify/review) with a per-kind tool whitelist and structured JSON output. Independent of --subagent. Env: AFFENTSERVE_FOCUSED_TASKS.")
		browserCacheDir    = fs.String("browser-cache-dir", "", "Enable an on-disk response cache for browser sessions; empty disables caching.")
		browserCacheTTL    = fs.String("browser-cache-ttl", "", "Cache TTL when --browser-cache-dir is set ('24h' default; '0s' disables expiry).")
		browserCacheSweep  = fs.String("browser-cache-sweep-interval", "", "How often cache GC deletes expired files when --browser-cache-dir is set (default = TTL/8, min 5m; explicit values must be >=5m).")
		browserNoStealth   = fs.Bool("browser-no-stealth", false, "Disable the webdriver-detection bypass script. Default off (stealth on).")
		browserAllowAll    = fs.Bool("browser-allow-all-domains", false, "Allow third-party / tracker domains the default list normally blocks.")
		browserScreenshot  = fs.Bool("browser-screenshot", false, "Register the browser_screenshot tool. Off by default — base64 image payloads bloat tool result events; flip on for vision-capable models. Env: AFFENTSERVE_BROWSER_SCREENSHOT.")
		systemPrompt       = fs.String("system-prompt", "", "Override agent.DefaultSystemPrompt. '-' reads from stdin, '@FILE' from a file, anything else is literal.")
		// Sampling pass-through. Strings (not Float64Var / Int) so an
		// unset flag is distinguishable from --temperature=0 — that
		// distinction matters for evals where temperature=0 is the
		// deterministic decode setting.
		temperature = fs.String("temperature", "", "Sampling temperature forwarded to upstream LLM (omit to use provider default). Set 0 for deterministic decode in evals. Env: AFFENTSERVE_TEMPERATURE.")
		topP        = fs.String("top-p", "", "Top-p (nucleus) sampling forwarded to upstream (omit to use provider default). Env: AFFENTSERVE_TOP_P.")
		maxTokens   = fs.String("max-tokens", "", "Max output tokens forwarded to upstream (omit to use provider default). Env: AFFENTSERVE_MAX_TOKENS.")
	)

	if err := fs.Parse(argv); err != nil {
		return Config{}, err
	}

	cfg, err := LoadConfig(*configPath)
	if err != nil {
		return cfg, err
	}

	// Resolve runs BEFORE the CLI-override block so env vars correctly
	// beat config-file values (12factor); CLI then wins over env.
	// Final precedence: CLI > env > config > built-in defaults.
	if err := cfg.Resolve(); err != nil {
		return cfg, err
	}

	// Track which flags the user actually passed so we can distinguish
	// "explicit override" from "default left untouched". The earlier
	// check, `fs.Lookup(...).Value.String() == "true"`, only fired
	// when the boolean's resolved value was true — meaning a config
	// file's true could not be overridden back to false from the CLI.
	setFlags := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { setFlags[f.Name] = true })

	// CLI flags override file/env values when set (non-zero / non-empty).
	if *listen != "" {
		cfg.Listen = *listen
	}
	if *baseURL != "" {
		cfg.BaseURL = *baseURL
	}
	if *apiKey != "" {
		cfg.APIKey = *apiKey
	}
	if *model != "" {
		cfg.Model = *model
	}
	if *authToken != "" {
		cfg.AuthToken = *authToken
	}
	if *workspaceRoot != "" {
		cfg.WorkspaceRoot = *workspaceRoot
	}
	if *memoryRoot != "" {
		cfg.MemoryRoot = *memoryRoot
	}
	if setFlags["max-sessions"] {
		cfg.maxSessionsSet = true
		cfg.MaxSessions = *maxSessions
	}
	if *sessionIdleTTL != "" {
		cfg.SessionIdleTTL = *sessionIdleTTL
	}
	if *sessionRetention != "" {
		cfg.SessionRetention = *sessionRetention
	}
	if setFlags["max-turn-steps"] {
		cfg.MaxTurnSteps = *maxTurnSteps
	}
	if *perCallTimeout != "" {
		cfg.PerCallTimeout = *perCallTimeout
	}
	if setFlags["max-transient-retries"] {
		cfg.MaxTransientRetries = *maxRetries
	}
	if *retryBackoff != "" {
		cfg.RetryBackoff = *retryBackoff
	}
	if setFlags["compact-trigger"] {
		cfg.CompactTrigger = *compactTrigger
	}
	if setFlags["compact-keep-last"] {
		cfg.CompactKeepLast = *compactKeepLast
	}
	if setFlags["browser"] {
		cfg.enableBrowserSet = true
		cfg.EnableBrowser = *enableBrowser
	}
	if setFlags["web"] {
		cfg.enableWebSet = true
		cfg.EnableWeb = *enableWeb
	}
	if setFlags["web-search"] {
		cfg.enableWebSearchSet = true
		cfg.EnableWebSearch = *enableWebSearch
	}
	if setFlags["memory"] {
		cfg.enableMemorySet = true
		cfg.EnableMemory = *enableMemory
	}
	if setFlags["builtins"] {
		cfg.EnableBuiltins = *enableBuiltins
	}
	if setFlags["eval-mode"] {
		cfg.EvalMode = *evalMode
	}
	if setFlags["subagent"] {
		cfg.enableSubagentSet = true
		cfg.EnableSubagent = *enableSubagent
	}
	if setFlags["subagent-max-depth"] {
		cfg.subagentMaxDepthSet = true
		cfg.SubagentMaxDepth = *subagentMaxDepth
	}
	if setFlags["focused-tasks"] {
		cfg.enableFocusedTasksSet = true
		cfg.EnableFocusedTasks = *enableFocusedTasks
	}
	if *browserCacheDir != "" {
		cfg.BrowserCacheDir = *browserCacheDir
	}
	if *browserCacheTTL != "" {
		cfg.BrowserCacheTTL = *browserCacheTTL
	}
	if *browserCacheSweep != "" {
		cfg.BrowserCacheSweepInterval = *browserCacheSweep
	}
	if setFlags["browser-no-stealth"] {
		cfg.BrowserNoStealth = *browserNoStealth
	}
	if setFlags["browser-allow-all-domains"] {
		cfg.BrowserAllowAllDomains = *browserAllowAll
	}
	if setFlags["browser-screenshot"] {
		cfg.browserScreenshotSet = true
		cfg.BrowserScreenshot = *browserScreenshot
	}
	if *systemPrompt != "" {
		resolved, err := resolveSystemPromptFlag(*systemPrompt)
		if err != nil {
			return cfg, fmt.Errorf("--system-prompt: %w", err)
		}
		cfg.SystemPrompt = resolved
	}
	if *temperature != "" {
		t, err := strconv.ParseFloat(*temperature, 64)
		if err != nil {
			return cfg, fmt.Errorf("--temperature: %w", err)
		}
		cfg.Temperature = &t
	}
	if *topP != "" {
		t, err := strconv.ParseFloat(*topP, 64)
		if err != nil {
			return cfg, fmt.Errorf("--top-p: %w", err)
		}
		cfg.TopP = &t
	}
	if *maxTokens != "" {
		n, err := strconv.Atoi(*maxTokens)
		if err != nil {
			return cfg, fmt.Errorf("--max-tokens: %w", err)
		}
		cfg.MaxTokens = &n
	}

	if err := cfg.Validate(); err != nil {
		return cfg, err
	}
	cfg.ApplyEvalMode()
	return cfg, nil
}

func resolveSystemPromptFlag(v string) (string, error) {
	if v == "-" {
		data, err := readSystemPrompt(os.Stdin)
		if err != nil {
			return "", fmt.Errorf("read stdin: %w", err)
		}
		return data, nil
	}
	if len(v) > 1 && v[0] == '@' {
		f, err := os.Open(v[1:])
		if err != nil {
			return "", fmt.Errorf("read %s: %w", v[1:], err)
		}
		defer f.Close()
		data, err := readSystemPrompt(f)
		if err != nil {
			return "", fmt.Errorf("read %s: %w", v[1:], err)
		}
		return data, nil
	}
	return v, nil
}

// run starts the HTTP server and blocks until SIGINT/SIGTERM. Returns
// the first fatal error encountered. graceful: 10s drain on shutdown.
func run(cfg Config, logger zerolog.Logger) error {
	pool, err := NewSessionPool(cfg, logger)
	if err != nil {
		return fmt.Errorf("session pool: %w", err)
	}
	defer pool.Shutdown()

	mux := newRouter(cfg, pool, logger)
	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           mux,
		ReadHeaderTimeout: 15 * time.Second,
		// IdleTimeout caps keep-alive connections so an abandoned
		// client doesn't pin a goroutine forever. WriteTimeout is
		// deliberately omitted: /v1/chat/completions and the events
		// stream are long-running by design (LLM turns + SSE), and a
		// global write deadline would cut them off mid-stream. The
		// per-turn cap inside affent (MaxTurnSteps + PerCallTimeout)
		// bounds runtime; client disconnect propagates via
		// ctx.Done → Session.CancelTurn.
		IdleTimeout: 120 * time.Second,
	}

	logServeStartup(logger, cfg, pool.sessionRootPath())
	errCh := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	select {
	case sig := <-stop:
		logger.Info().Stringer("signal", sig).Msg("shutdown requested")
	case err := <-errCh:
		if err != nil {
			return err
		}
		return nil
	}

	// Flip the shutting-down flag BEFORE srv.Shutdown so /healthz
	// starts returning 503 immediately. Any LB probe that lands
	// during the graceful-shutdown window sees the readiness change
	// and drains traffic — without this, the flag only flipped via
	// defer pool.Shutdown() after srv.Shutdown had already finished,
	// by which time the LB had spent the whole drain window still
	// sending fresh requests.
	pool.SignalShutdown()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	return nil
}

// logServeStartup emits one structured line so operators dropping a fresh
// container can see at a glance which feature flags and durable paths are in
// use. Secrets (api_key, auth_token) are deliberately NOT included — auth is
// summarized as on/off instead.
func logServeStartup(logger zerolog.Logger, cfg Config, sessionStateRoot string) {
	auth := "off"
	if cfg.AuthToken != "" {
		auth = "on"
	}
	logger.Info().
		Str("build_revision", buildRevision).
		Str("build_date", buildDate).
		Str("listen", cfg.Listen).
		Str("model", cfg.Model).
		Str("base_url", cfg.BaseURL).
		Str("auth", auth).
		Str("workspace_root", cfg.WorkspaceRoot).
		Str("memory_root", cfg.MemoryRoot).
		Str("session_state_root", sessionStateRoot).
		Bool("builtins", cfg.EnableBuiltins).
		Bool("eval_mode", cfg.EvalMode).
		Bool("subagent", cfg.EnableSubagent).
		Int("subagent_max_depth", cfg.SubagentMaxDepth).
		Bool("focused_tasks", cfg.EnableFocusedTasks).
		Strs("focused_task_profiles", focusedTaskProfilesForLog(cfg)).
		Bool("memory", cfg.EnableMemory).
		Bool("browser", cfg.EnableBrowser).
		Bool("web", cfg.EnableWeb).
		Bool("web_search", cfg.EnableWebSearch).
		Int("max_sessions", cfg.MaxSessions).
		Str("session_idle_ttl", cfg.SessionIdleTTL).
		Str("session_retention", cfg.SessionRetention).
		Str("per_call_timeout", cfg.PerCallTimeout).
		Msg("affentserve starting")
}

// focusedTaskProfilesForLog returns the focused-task profile kinds
// (recall, explore, ...) that run_task will expose to clients of this
// server, computed from cfg via FocusedTaskAvailabilityProbe. Empty
// slice when focused tasks are disabled or when no profile's deps are
// satisfied. The result agrees with affentctl doctor's
// focused_task_profiles=… field for the equivalent flags, so operators
// see the same answer from the CLI diagnostic and the server boot log.
func focusedTaskProfilesForLog(cfg Config) []string {
	if !cfg.EnableFocusedTasks {
		return nil
	}
	probe := agent.FocusedTaskAvailabilityProbe{
		HasLLM:       true, // cfg.Model is required at Validate; live LLMs are constructed per session
		HasWorkspace: true, // every session gets a workspace
		HasExecutor:  cfg.EnableBuiltins,
		HasMemory:    cfg.EnableMemory,
		HasSessions:  true, // every session has a session dir for transcripts/conv
		HasWeb:       cfg.EnableWeb,
		HasBrowser:   cfg.EnableBrowser,
	}
	kinds := probe.AvailableKinds(nil)
	if len(kinds) == 0 {
		return nil
	}
	out := make([]string, len(kinds))
	for i, k := range kinds {
		out[i] = string(k)
	}
	return out
}
