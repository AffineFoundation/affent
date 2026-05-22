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
		configPath       = fs.String("config", "", "Path to JSON config file (CLI flags override).")
		listen           = fs.String("listen", "", "Address to listen on (default 127.0.0.1:7777).")
		baseURL          = fs.String("base-url", "", "Upstream OpenAI-compatible chat completions URL (env: AFFENTSERVE_BASE_URL).")
		apiKey           = fs.String("api-key", "", "API key for --base-url (env: AFFENTSERVE_API_KEY).")
		model            = fs.String("model", "", "Default model id reported by /v1/models and used when a request omits 'model' (env: AFFENTSERVE_MODEL).")
		authToken        = fs.String("auth-token", "", "Optional bearer token gating the server itself (env: AFFENTSERVE_AUTH_TOKEN).")
		workspaceRoot    = fs.String("workspace-root", "", "Parent directory for per-session workspaces. Empty creates per-session temp dirs.")
		maxSessions      = fs.Int("max-sessions", 0, "LRU upper bound on in-memory sessions (default 32).")
		sessionIdleTTL   = fs.String("session-idle-ttl", "", "How long an idle session stays in the pool before GC (default 10m).")
		sessionRetention = fs.String("session-retention", "", "How long durable session dirs (conv log + memory) live on disk after last activity. Empty disables — dirs live until explicit DELETE. Set to a Go duration like '720h' (30d) to enable background GC.")
		maxTurnSteps     = fs.Int("max-turn-steps", 0, "Per-turn step cap (assistant↔tool round trips). 0 = agent runtime's default.")
		perCallTimeout   = fs.String("per-call-timeout", "", "Per-LLM-call timeout as a Go duration string (default 3m). Bump for reasoning models that may think for several minutes per call.")
		maxRetries       = fs.Int("max-transient-retries", 0, "Retry budget for transient LLM failures (5xx/429/408/net/EOF/timeout). 0 = agent runtime's default (3); negative disables retries (use when the upstream provider already handles retries and you want a single attempt).")
		retryBackoff     = fs.String("retry-backoff", "", "Initial backoff between transient-error retries (default 4s); each subsequent attempt doubles it. Go duration string.")
		compactTrigger   = fs.Int("compact-trigger", 0, "Rolling-summary compactor's message threshold per session. 0 = agent runtime's default (240). Lower on small-context upstream models to compact earlier.")
		compactKeepLast  = fs.Int("compact-keep-last", 0, "Messages preserved verbatim at the tail of the conversation when compacting. 0 = agent runtime's default (10).")
		enableBrowser    = fs.Bool("browser", false, "Register the extras/browser tool family for each new session.")
		enableWeb        = fs.Bool("web", false, "Register extras/web's web_fetch tool.")
		enableWebSearch  = fs.Bool("web-search", false, "Register web_search alongside web_fetch (requires TAVILY_API_KEY by default).")
		enableMemory     = fs.Bool("memory", false, "Register agent runtime's memory tool. Off by default — eval workloads should leave it off.")
		enableBuiltins   = fs.Bool("builtins", false, "Register shell + file builtins (LocalExecutor). DANGEROUS on a shared host — only enable in a sandboxed environment.")
		enableSubagent   = fs.Bool("subagent", false, "Register the subagent_run tool — a bounded isolated Loop with read-only inspection tools. Off by default; doesn't require --builtins but inherits the shell tool when --builtins is also on.")
		browserCacheDir  = fs.String("browser-cache-dir", "", "Enable an on-disk response cache for browser sessions; empty disables caching.")
		browserCacheTTL  = fs.String("browser-cache-ttl", "", "Cache TTL ('24h' default; '0s' disables expiry).")
		browserCacheSweep = fs.String("browser-cache-sweep-interval", "", "How often the cache GC deletes expired files (default = TTL/8, min 5m).")
		browserNoStealth = fs.Bool("browser-no-stealth", false, "Disable the webdriver-detection bypass script. Default off (stealth on).")
		browserAllowAll  = fs.Bool("browser-allow-all-domains", false, "Allow third-party / tracker domains the default list normally blocks.")
		browserScreenshot = fs.Bool("browser-screenshot", false, "Register the browser_screenshot tool. Off by default — base64 image payloads bloat tool result events; flip on for vision-capable models.")
		systemPrompt     = fs.String("system-prompt", "", "Override agent.DefaultSystemPrompt. '-' reads from stdin, '@FILE' from a file, anything else is literal.")
		// Sampling pass-through. Strings (not Float64Var / Int) so an
		// unset flag is distinguishable from --temperature=0 — that
		// distinction matters for evals where temperature=0 is the
		// deterministic decode setting.
		temperature = fs.String("temperature", "", "Sampling temperature forwarded to upstream LLM (omit to use provider default). Set 0 for deterministic decode in evals.")
		topP        = fs.String("top-p", "", "Top-p (nucleus) sampling forwarded to upstream (omit to use provider default).")
		maxTokens   = fs.String("max-tokens", "", "Max output tokens forwarded to upstream (omit to use provider default).")
	)

	if err := fs.Parse(argv); err != nil {
		return Config{}, err
	}

	cfg, err := LoadConfig(*configPath)
	if err != nil {
		return cfg, err
	}

	// Track which flags the user actually passed so we can distinguish
	// "explicit override" from "default left untouched". The earlier
	// check, `fs.Lookup(...).Value.String() == "true"`, only fired
	// when the boolean's resolved value was true — meaning a config
	// file's true could not be overridden back to false from the CLI.
	setFlags := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { setFlags[f.Name] = true })

	// CLI flags override file values when set (non-zero / non-empty).
	if *listen != "" {
		cfg.Listen = *listen
	}
	if *baseURL != "" {
		cfg.BaseURL = *baseURL
	} else if cfg.BaseURL == "" {
		cfg.BaseURL = os.Getenv("AFFENTSERVE_BASE_URL")
	}
	if *apiKey != "" {
		cfg.APIKey = *apiKey
	}
	if *model != "" {
		cfg.Model = *model
	} else if cfg.Model == "" {
		cfg.Model = os.Getenv("AFFENTSERVE_MODEL")
	}
	if *authToken != "" {
		cfg.AuthToken = *authToken
	}
	if *workspaceRoot != "" {
		cfg.WorkspaceRoot = *workspaceRoot
	}
	if *maxSessions > 0 {
		cfg.MaxSessions = *maxSessions
	}
	if *sessionIdleTTL != "" {
		cfg.SessionIdleTTL = *sessionIdleTTL
	}
	if *sessionRetention != "" {
		cfg.SessionRetention = *sessionRetention
	}
	if *maxTurnSteps > 0 {
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
	if *compactTrigger > 0 {
		cfg.CompactTrigger = *compactTrigger
	}
	if *compactKeepLast > 0 {
		cfg.CompactKeepLast = *compactKeepLast
	}
	if setFlags["browser"] {
		cfg.EnableBrowser = *enableBrowser
	}
	if setFlags["web"] {
		cfg.EnableWeb = *enableWeb
	}
	if setFlags["web-search"] {
		cfg.EnableWebSearch = *enableWebSearch
	}
	if setFlags["memory"] {
		cfg.EnableMemory = *enableMemory
	}
	if setFlags["builtins"] {
		cfg.EnableBuiltins = *enableBuiltins
	}
	if setFlags["subagent"] {
		cfg.EnableSubagent = *enableSubagent
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

	if err := cfg.Resolve(); err != nil {
		return cfg, err
	}
	if err := cfg.Validate(); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func resolveSystemPromptFlag(v string) (string, error) {
	if v == "-" {
		data, err := readAll(os.Stdin)
		if err != nil {
			return "", fmt.Errorf("read stdin: %w", err)
		}
		return data, nil
	}
	if len(v) > 1 && v[0] == '@' {
		data, err := os.ReadFile(v[1:])
		if err != nil {
			return "", fmt.Errorf("read %s: %w", v[1:], err)
		}
		return string(data), nil
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

	logger.Info().Str("listen", cfg.Listen).Msg("affentserve starting")
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
