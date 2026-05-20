package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
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
		model            = fs.String("model", "", "Default model id reported by /v1/models and used when a request omits 'model'.")
		authToken        = fs.String("auth-token", "", "Optional bearer token gating the server itself (env: AFFENTSERVE_AUTH_TOKEN).")
		workspaceRoot    = fs.String("workspace-root", "", "Parent directory for per-session workspaces. Empty creates per-session temp dirs.")
		maxSessions      = fs.Int("max-sessions", 0, "LRU upper bound on in-memory sessions (default 32).")
		sessionIdleTTL   = fs.String("session-idle-ttl", "", "How long an idle session stays in the pool before GC (default 10m).")
		maxTurnSteps     = fs.Int("max-turn-steps", 0, "Per-turn step cap (assistant↔tool round trips). 0 = affent's default.")
		enableBrowser    = fs.Bool("browser", false, "Register the extras/browser tool family for each new session.")
		enableWeb        = fs.Bool("web", false, "Register extras/web's web_fetch tool.")
		enableWebSearch  = fs.Bool("web-search", false, "Register web_search alongside web_fetch (requires TAVILY_API_KEY by default).")
		enableMemory     = fs.Bool("memory", false, "Register affent's memory tool. Off by default — eval workloads should leave it off.")
		enableBuiltins   = fs.Bool("builtins", false, "Register shell + file builtins (LocalExecutor). DANGEROUS on a shared host — only enable in a sandboxed environment.")
		browserCacheDir  = fs.String("browser-cache-dir", "", "Enable an on-disk response cache for browser sessions; empty disables caching.")
		browserCacheTTL  = fs.String("browser-cache-ttl", "", "Cache TTL ('24h' default; '0s' disables expiry).")
		browserNoStealth = fs.Bool("browser-no-stealth", false, "Disable the webdriver-detection bypass script. Default off (stealth on).")
		browserAllowAll  = fs.Bool("browser-allow-all-domains", false, "Allow third-party / tracker domains the default list normally blocks.")
		systemPrompt     = fs.String("system-prompt", "", "Override affent.DefaultSystemPrompt. '-' reads from stdin, '@FILE' from a file, anything else is literal.")
	)

	if err := fs.Parse(argv); err != nil {
		return Config{}, err
	}

	cfg, err := LoadConfig(*configPath)
	if err != nil {
		return cfg, err
	}

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
	if *maxTurnSteps > 0 {
		cfg.MaxTurnSteps = *maxTurnSteps
	}
	if fs.Lookup("browser").Value.String() == "true" {
		cfg.EnableBrowser = *enableBrowser
	}
	if fs.Lookup("web").Value.String() == "true" {
		cfg.EnableWeb = *enableWeb
	}
	if fs.Lookup("web-search").Value.String() == "true" {
		cfg.EnableWebSearch = *enableWebSearch
	}
	if fs.Lookup("memory").Value.String() == "true" {
		cfg.EnableMemory = *enableMemory
	}
	if fs.Lookup("builtins").Value.String() == "true" {
		cfg.EnableBuiltins = *enableBuiltins
	}
	if *browserCacheDir != "" {
		cfg.BrowserCacheDir = *browserCacheDir
	}
	if *browserCacheTTL != "" {
		cfg.BrowserCacheTTL = *browserCacheTTL
	}
	if fs.Lookup("browser-no-stealth").Value.String() == "true" {
		cfg.BrowserNoStealth = *browserNoStealth
	}
	if fs.Lookup("browser-allow-all-domains").Value.String() == "true" {
		cfg.BrowserAllowAllDomains = *browserAllowAll
	}
	if *systemPrompt != "" {
		resolved, err := resolveSystemPromptFlag(*systemPrompt)
		if err != nil {
			return cfg, fmt.Errorf("--system-prompt: %w", err)
		}
		cfg.SystemPrompt = resolved
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

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	return nil
}
