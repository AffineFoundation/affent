package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

// Config is the resolved, post-flag-merge configuration the server runs
// with. JSON-loadable from a `--config` file; CLI flags override.
type Config struct {
	// Listen address ("host:port") for the HTTP server.
	Listen string `json:"listen"`

	// BaseURL is the upstream OpenAI-compatible LLM endpoint affent
	// will talk to. Required.
	BaseURL string `json:"base_url"`

	// APIKey for BaseURL. Empty allowed (some endpoints don't require
	// auth). Reads AFFENTSERVE_API_KEY env var if unset on disk.
	APIKey string `json:"api_key"`

	// Model the loop drives. Empty falls back to whatever the request
	// body specifies; otherwise the server overrides per-request.
	Model string `json:"model"`

	// AuthToken gates the server itself. When non-empty, every
	// request must present `Authorization: Bearer <AuthToken>`.
	// Leave empty for trusted-network deployments.
	AuthToken string `json:"auth_token"`

	// WorkspaceRoot is the parent directory under which each session
	// gets its own ephemeral workspace (for agent runtime's Conversation JSONL).
	WorkspaceRoot string `json:"workspace_root"`

	// MemoryRoot is the parent directory for DURABLE per-session memory.
	// Memory lives separately from the session workspace so it survives
	// LRU eviction and server restarts: same session_id → same memory
	// dir, regardless of how many times the workspace was recreated.
	// Empty defaults to "<WorkspaceRoot>/memory" (or an OS temp dir
	// when WorkspaceRoot itself is empty).
	MemoryRoot string `json:"memory_root"`

	// MaxSessions caps the in-memory session pool size. Sessions
	// past the cap are LRU-evicted. Default 32.
	MaxSessions int `json:"max_sessions"`

	// SessionIdleTTL closes sessions with no activity for at least
	// this long. Default 10m. Use a duration string ("30s", "10m").
	SessionIdleTTL string `json:"session_idle_ttl"`

	// SessionRetention controls how long a session's DURABLE state
	// (conversation log + memory under MemoryRoot/<id>/) is kept
	// after the last activity. Empty (the default) disables the
	// retention sweep — durable state lives forever until an
	// explicit DELETE on the session id. Set to a Go duration
	// string ("720h" for 30 days, "168h" for a week) to enable
	// background GC. The sweep skips dirs whose session_id is
	// currently in the in-memory pool, so an active session is
	// never wiped under it.
	SessionRetention string `json:"session_retention"`

	// MaxTurnSteps overrides agent runtime's default per-turn step cap.
	MaxTurnSteps int `json:"max_turn_steps"`

	// CompactTrigger overrides the rolling-summary compactor's
	// per-session message threshold. Zero falls back to
	// agent.DefaultSummaryTriggerMsgs (240). Lower it (e.g. 120) on
	// small-context upstream models so compaction kicks in earlier
	// and you spend less per turn shipping a near-full window.
	CompactTrigger int `json:"compact_trigger"`

	// CompactKeepLast overrides how many trailing messages survive
	// compaction verbatim. Zero falls back to
	// agent.DefaultSummaryKeepLast (10). Smaller = more aggressive
	// reduction; larger = more context retained.
	CompactKeepLast int `json:"compact_keep_last"`

	// EnableBrowser registers the extras/browser tool family on each
	// new session. Disabled by default since it adds a Chromium
	// runtime dependency.
	EnableBrowser bool `json:"enable_browser"`

	// EnableWeb registers extras/web's web_fetch (and optionally
	// web_search). Disabled by default to keep the Tavily key
	// requirement opt-in.
	EnableWeb       bool `json:"enable_web"`
	EnableWebSearch bool `json:"enable_web_search"`

	// EnableMemory exposes agent runtime's `memory` tool. Disabled by
	// default; eval workloads should leave it off so per-question
	// state doesn't accumulate.
	EnableMemory bool `json:"enable_memory"`

	// EnableBuiltins registers agent runtime's shell + file tools. Defaults
	// to false — running shell on behalf of remote callers is
	// dangerous on a shared host. Operators who want it must opt in
	// explicitly. When enabled, shell commands run as the affentserve
	// process's UID via executor.LocalExecutor; for kernel-level
	// isolation, run affentserve itself inside a sandbox.
	EnableBuiltins bool `json:"enable_builtins"`

	// SystemPrompt overrides agent.DefaultSystemPrompt. Empty falls
	// through to agent runtime's builtin.
	SystemPrompt string `json:"system_prompt"`

	// BrowserCacheDir, when non-empty, enables an on-disk response
	// cache shared across all browser sessions in this process.
	// Cache keys are URL hashes; entries store the document body +
	// headers + status. Reduces wall-clock for repeated benchmark
	// questions hitting the same pages.
	BrowserCacheDir string `json:"browser_cache_dir"`

	// BrowserCacheTTL is the cache freshness window. Default 24h.
	// Duration string ("1h", "30m"). "0s" disables expiry.
	BrowserCacheTTL string `json:"browser_cache_ttl"`

	// BrowserCacheSweepInterval is how often the cache GC walks the
	// dir and deletes expired entries from disk. Default = TTL/8
	// clamped to >= 5m. Operators with very large caches may want a
	// longer interval to spread out the I/O cost of each sweep.
	BrowserCacheSweepInterval string `json:"browser_cache_sweep_interval"`

	// BrowserNoStealth disables the stealth bypass. Default off
	// (stealth on). Flip when debugging fingerprint regressions.
	BrowserNoStealth bool `json:"browser_no_stealth"`

	// BrowserAllowAllDomains disables the default tracker block list.
	// Useful when capturing full third-party traffic for trace
	// inspection; harmful for Cloudflare-avoidance.
	BrowserAllowAllDomains bool `json:"browser_allow_all_domains"`

	// EnableSubagent registers the subagent_run tool. The subagent is
	// a fresh isolated Loop with read-only tools (read_file, list_files,
	// guarded shell, read-only memory) and a step budget. Off by
	// default so the only cost an operator opts into is the upstream
	// LLM bill; on for deployments that want bounded exploration /
	// review without polluting the main session context. The
	// subagent's tools are read-only by design — enabling this does
	// NOT enable shell or file writes for the parent agent. The
	// previous wiring tied subagent registration to EnableBuiltins,
	// which mixed two unrelated capabilities; operators relying on
	// that coupling must now set EnableSubagent: true explicitly.
	EnableSubagent bool `json:"enable_subagent"`

	// BrowserScreenshot registers the browser_screenshot tool. Off by
	// default because the base64 image payload bloats tool result events
	// and text-only models can't act on it. Vision-capable callers
	// (qwen-vl, gpt-4o, claude-3.x) flip this on to let the agent see
	// the rendered page; the tool's save_path option keeps base64 out
	// of the result for callers that only want a PNG on disk.
	BrowserScreenshot bool `json:"browser_screenshot"`

	// Sampling knobs forwarded to the upstream LLM on every chat
	// completion. Pointers so "unset" (nil) differs from "explicitly 0"
	// — temperature=0 is the deterministic-decode setting evals rely
	// on, not the same as "use provider default". Empty pointers leave
	// the field off the wire so OpenAI / vLLM / sglang fall through to
	// their own defaults.
	Temperature *float64 `json:"temperature,omitempty"`
	TopP        *float64 `json:"top_p,omitempty"`
	MaxTokens   *int     `json:"max_tokens,omitempty"`
}

const (
	defaultListen         = "127.0.0.1:7777"
	defaultMaxSessions    = 32
	defaultSessionIdleTTL = 10 * time.Minute
)

// LoadConfig reads a JSON file and returns the parsed Config. An empty
// path returns a zero-value Config with no error.
func LoadConfig(path string) (Config, error) {
	var cfg Config
	if path == "" {
		return cfg, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, fmt.Errorf("read config %s: %w", path, err)
	}
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return cfg, fmt.Errorf("parse config %s: %w", path, err)
	}
	return cfg, nil
}

// Resolve fills in defaults and reads supporting env vars. Idempotent
// once values are set.
func (c *Config) Resolve() error {
	if c.Listen == "" {
		c.Listen = defaultListen
	}
	if c.MaxSessions <= 0 {
		c.MaxSessions = defaultMaxSessions
	}
	if c.APIKey == "" {
		c.APIKey = os.Getenv("AFFENTSERVE_API_KEY")
	}
	if c.AuthToken == "" {
		c.AuthToken = os.Getenv("AFFENTSERVE_AUTH_TOKEN")
	}
	if c.WorkspaceRoot == "" {
		c.WorkspaceRoot = os.Getenv("AFFENTSERVE_WORKSPACE_ROOT")
	}
	if c.MemoryRoot == "" {
		c.MemoryRoot = os.Getenv("AFFENTSERVE_MEMORY_ROOT")
	}
	if c.SessionIdleTTL == "" {
		c.SessionIdleTTL = defaultSessionIdleTTL.String()
	}
	if c.SessionRetention == "" {
		c.SessionRetention = os.Getenv("AFFENTSERVE_SESSION_RETENTION")
	}
	return nil
}

// IdleTTL parses SessionIdleTTL into a duration with the documented
// default fallback.
func (c Config) IdleTTL() (time.Duration, error) {
	if c.SessionIdleTTL == "" {
		return defaultSessionIdleTTL, nil
	}
	d, err := time.ParseDuration(c.SessionIdleTTL)
	if err != nil {
		return 0, fmt.Errorf("session_idle_ttl=%q: %w", c.SessionIdleTTL, err)
	}
	return d, nil
}

// Retention parses SessionRetention. Returns 0 (disabled) when the
// field is empty, or the parsed duration when set. An invalid string
// is a hard error so an operator's typo doesn't silently turn off
// what they thought they enabled.
func (c Config) Retention() (time.Duration, error) {
	if c.SessionRetention == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(c.SessionRetention)
	if err != nil {
		return 0, fmt.Errorf("session_retention=%q: %w", c.SessionRetention, err)
	}
	return d, nil
}

// Validate reports unrecoverable configuration problems. Defaults are
// applied by Resolve() before this is meaningful — callers should
// always Resolve() then Validate().
func (c Config) Validate() error {
	if c.BaseURL == "" {
		return errors.New("base_url is required (use --base-url or AFFENTSERVE_BASE_URL)")
	}
	if c.Model == "" {
		// Without a model, the LLMClient sends `"model":""` upstream
		// and every OpenAI-compat backend 400s the first request.
		// Better to fail fast at startup than wait for the operator
		// to discover this through a runtime error in a client log.
		return errors.New("model is required (use --model or set model in config file)")
	}
	if _, err := c.IdleTTL(); err != nil {
		return err
	}
	return nil
}
