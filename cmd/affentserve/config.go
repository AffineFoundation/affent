package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/affinefoundation/affent/internal/agent"
)

// Config is the resolved, post-flag-merge configuration the server runs
// with. JSON-loadable from a `--config` file; CLI flags override.
type Config struct {
	// Listen address ("host:port") for the HTTP server.
	Listen string `json:"listen"`

	// BaseURL is the upstream OpenAI-compatible LLM endpoint affent
	// will talk to. Required.
	BaseURL string `json:"base_url"`

	// APIKey for BaseURL. Empty is allowed for endpoints that do not
	// require auth, but not for the default OpenAI endpoint. Reads
	// AFFENTSERVE_API_KEY env var if unset on disk.
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
	MaxSessions    int `json:"max_sessions"`
	maxSessionsSet bool

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

	// PerCallTimeout overrides agent.DefaultPerCallTimeout (3 min) on
	// every LLM call the Loop makes. Reasoning models (o1, Claude
	// extended-thinking, Qwen QwQ) can take 5+ minutes to think; the
	// default trips them mid-stream. Empty falls back to the agent
	// default. Go duration string ("8m", "15m").
	PerCallTimeout string `json:"per_call_timeout"`

	// MaxTransientRetries overrides agent.DefaultTransientRetries (3)
	// for retryable LLM failures (HTTP 408/429/5xx, mid-stream EOF,
	// per-call timeout). Zero → fall back to the agent default;
	// negative → disable retry entirely. Disabling is useful when
	// the upstream provider already implements its own retry layer
	// and a double-retry doubles spend on a flaky day.
	MaxTransientRetries int `json:"max_transient_retries"`

	// RetryBackoff is the initial wait between retries; each
	// subsequent attempt doubles it. Empty falls back to
	// agent.DefaultTransientBackoff (4s). Go duration string.
	RetryBackoff string `json:"retry_backoff"`

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

	// EnableMemory exposes agent runtime's `memory` tool.
	EnableMemory    bool `json:"enable_memory"`
	enableMemorySet bool

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
	// guarded shell, read-only memory) and a step budget. On by
	// default for context isolation; disable it for strict single-loop
	// evals or deployments that do not want nested model calls. The
	// subagent's tools are read-only by design — enabling this does
	// NOT enable shell or file writes for the parent agent.
	EnableSubagent    bool `json:"enable_subagent"`
	enableSubagentSet bool

	// SubagentMaxDepth caps recursive subagent layers. 1 means only a
	// direct child; the default allows one child to delegate one noisy
	// subtask while preventing open-ended agent chains.
	SubagentMaxDepth    int `json:"subagent_max_depth"`
	subagentMaxDepthSet bool

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
	defaultListen                = "127.0.0.1:7777"
	defaultMaxSessions           = 32
	defaultSessionIdleTTL        = 10 * time.Minute
	defaultBrowserCacheTTL       = 24 * time.Hour
	minBrowserCacheSweepInterval = 5 * time.Minute
	maxConfigBytes               = 1024 * 1024
)

// LoadConfig reads a JSON file and returns the parsed Config. An empty
// path returns a zero-value Config with no error.
func LoadConfig(path string) (Config, error) {
	var cfg Config
	if path == "" {
		return cfg, nil
	}
	data, err := readConfigFile(path)
	if err != nil {
		return cfg, fmt.Errorf("read config %s: %w", path, err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return cfg, fmt.Errorf("parse config %s: %w", path, err)
	}
	var raw struct {
		MaxSessions      *int  `json:"max_sessions"`
		EnableMemory     *bool `json:"enable_memory"`
		EnableSubagent   *bool `json:"enable_subagent"`
		SubagentMaxDepth *int  `json:"subagent_max_depth"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return cfg, fmt.Errorf("parse config %s: %w", path, err)
	}
	cfg.maxSessionsSet = raw.MaxSessions != nil
	cfg.enableMemorySet = raw.EnableMemory != nil
	cfg.enableSubagentSet = raw.EnableSubagent != nil
	cfg.subagentMaxDepthSet = raw.SubagentMaxDepth != nil
	return cfg, nil
}

func readConfigFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxConfigBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxConfigBytes {
		return nil, fmt.Errorf("config exceeds %d-byte limit", maxConfigBytes)
	}
	return data, nil
}

// Resolve fills in defaults and applies env-var overrides. Env beats
// the config file (12factor convention; affentctl already worked this
// way). Idempotent.
//
// Precedence the surrounding code constructs:
//
//	CLI flag  >  env var  >  config file  >  built-in default
//
// Resolve only handles the env > config > default tail. main.go's
// CLI-override block runs AFTER Resolve so CLI flags still win over
// whatever env produced.
func (c *Config) Resolve() error {
	if c.Listen == "" {
		c.Listen = defaultListen
	}
	if !c.maxSessionsSet && c.MaxSessions <= 0 {
		c.MaxSessions = defaultMaxSessions
	}
	if !c.enableMemorySet {
		c.EnableMemory = true
	}
	if !c.enableSubagentSet {
		c.EnableSubagent = true
	}
	if !c.subagentMaxDepthSet && c.SubagentMaxDepth <= 0 {
		c.SubagentMaxDepth = agent.DefaultSubagentMaxDepth
	}
	for _, e := range []struct {
		env  string
		dest *string
	}{
		{"AFFENTSERVE_BASE_URL", &c.BaseURL},
		{"AFFENTSERVE_API_KEY", &c.APIKey},
		{"AFFENTSERVE_MODEL", &c.Model},
		{"AFFENTSERVE_AUTH_TOKEN", &c.AuthToken},
		{"AFFENTSERVE_WORKSPACE_ROOT", &c.WorkspaceRoot},
		{"AFFENTSERVE_MEMORY_ROOT", &c.MemoryRoot},
		{"AFFENTSERVE_SESSION_RETENTION", &c.SessionRetention},
	} {
		if v := os.Getenv(e.env); v != "" {
			*e.dest = v
		}
	}
	for _, e := range []struct {
		env  string
		dest *bool
	}{
		{"AFFENTSERVE_MEMORY", &c.EnableMemory},
		{"AFFENTSERVE_SUBAGENT", &c.EnableSubagent},
	} {
		if v := os.Getenv(e.env); v != "" {
			b, err := strconv.ParseBool(v)
			if err != nil {
				return fmt.Errorf("%s=%q: %w", e.env, v, err)
			}
			*e.dest = b
		}
	}
	if v := os.Getenv("AFFENTSERVE_SUBAGENT_MAX_DEPTH"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("AFFENTSERVE_SUBAGENT_MAX_DEPTH=%q: %w", v, err)
		}
		c.SubagentMaxDepth = n
	}
	if c.SessionIdleTTL == "" {
		c.SessionIdleTTL = defaultSessionIdleTTL.String()
	}
	for _, e := range []struct {
		env  string
		dest **float64
	}{
		{"AFFENTSERVE_TEMPERATURE", &c.Temperature},
		{"AFFENTSERVE_TOP_P", &c.TopP},
	} {
		if v := os.Getenv(e.env); v != "" {
			f, err := strconv.ParseFloat(v, 64)
			if err != nil {
				return fmt.Errorf("%s=%q: %w", e.env, v, err)
			}
			*e.dest = &f
		}
	}
	if v := os.Getenv("AFFENTSERVE_MAX_TOKENS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("AFFENTSERVE_MAX_TOKENS=%q: %w", v, err)
		}
		c.MaxTokens = &n
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
	if d <= 0 {
		return 0, fmt.Errorf("session_idle_ttl=%q must be positive", c.SessionIdleTTL)
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
	if d <= 0 {
		return 0, fmt.Errorf("session_retention=%q must be positive when set", c.SessionRetention)
	}
	return d, nil
}

// BrowserCacheTTLDuration parses BrowserCacheTTL. Empty falls back to
// 24h; 0s is allowed and means "cache forever" (FileResponseCache's
// documented no-expiry mode). Negative values are rejected because
// they turn a freshness window into an always-stale cache.
func (c Config) BrowserCacheTTLDuration() (time.Duration, error) {
	if c.BrowserCacheTTL == "" {
		return defaultBrowserCacheTTL, nil
	}
	d, err := time.ParseDuration(c.BrowserCacheTTL)
	if err != nil {
		return 0, fmt.Errorf("browser_cache_ttl=%q: %w", c.BrowserCacheTTL, err)
	}
	if d < 0 {
		return 0, fmt.Errorf("browser_cache_ttl=%q must be zero or positive", c.BrowserCacheTTL)
	}
	return d, nil
}

// BrowserCacheSweepIntervalDuration parses the optional cache-GC
// interval. Empty derives the documented default from the cache TTL.
// A configured value must be at least minBrowserCacheSweepInterval;
// tighter sweeps add directory-walk I/O without improving agent
// behavior in normal deployments.
func (c Config) BrowserCacheSweepIntervalDuration(cacheTTL time.Duration) (time.Duration, error) {
	if c.BrowserCacheSweepInterval != "" {
		if cacheTTL <= 0 {
			return 0, errors.New("browser_cache_sweep_interval requires a positive browser_cache_ttl")
		}
		d, err := time.ParseDuration(c.BrowserCacheSweepInterval)
		if err != nil {
			return 0, fmt.Errorf("browser_cache_sweep_interval=%q: %w", c.BrowserCacheSweepInterval, err)
		}
		if d < minBrowserCacheSweepInterval {
			return 0, fmt.Errorf("browser_cache_sweep_interval=%q must be at least %s", c.BrowserCacheSweepInterval, minBrowserCacheSweepInterval)
		}
		return d, nil
	}
	if cacheTTL <= 0 {
		return 0, nil
	}
	d := cacheTTL / 8
	if d < minBrowserCacheSweepInterval {
		d = minBrowserCacheSweepInterval
	}
	return d, nil
}

// PerCallTimeoutDuration parses PerCallTimeout into a duration.
// Returns 0 when the field is empty (the Loop then falls back to
// agent.DefaultPerCallTimeout). An invalid string is a hard error
// so an operator's typo on a "raise timeout for reasoning models"
// change surfaces at startup, not as a silent 3-minute cap.
func (c Config) PerCallTimeoutDuration() (time.Duration, error) {
	if c.PerCallTimeout == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(c.PerCallTimeout)
	if err != nil {
		return 0, fmt.Errorf("per_call_timeout=%q: %w", c.PerCallTimeout, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("per_call_timeout=%q must be positive", c.PerCallTimeout)
	}
	return d, nil
}

// RetryBackoffDuration parses RetryBackoff. Empty → 0 (Loop falls
// back to agent.DefaultTransientBackoff). Same fail-fast posture
// as PerCallTimeoutDuration: a typo at startup beats a silent
// 4-second-default for the lifetime of the deploy.
func (c Config) RetryBackoffDuration() (time.Duration, error) {
	if c.RetryBackoff == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(c.RetryBackoff)
	if err != nil {
		return 0, fmt.Errorf("retry_backoff=%q: %w", c.RetryBackoff, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("retry_backoff=%q must be positive", c.RetryBackoff)
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
	if strings.TrimRight(c.BaseURL, "/") == agent.DefaultBaseURL && strings.TrimSpace(c.APIKey) == "" {
		return fmt.Errorf("api_key is required when base_url is %s (use --api-key or AFFENTSERVE_API_KEY)", agent.DefaultBaseURL)
	}
	if c.Model == "" {
		// Without a model, the LLMClient sends `"model":""` upstream
		// and every OpenAI-compat backend 400s the first request.
		// Better to fail fast at startup than wait for the operator
		// to discover this through a runtime error in a client log.
		return errors.New("model is required (use --model or set model in config file)")
	}
	if c.MaxSessions <= 0 {
		return fmt.Errorf("max_sessions must be a positive integer")
	}
	if c.MaxTurnSteps < 0 {
		return fmt.Errorf("max_turn_steps must be zero or a positive integer")
	}
	if c.CompactTrigger < 0 {
		return fmt.Errorf("compact_trigger must be zero or a positive integer")
	}
	if c.CompactKeepLast < 0 {
		return fmt.Errorf("compact_keep_last must be zero or a positive integer")
	}
	if c.EnableWebSearch && !c.EnableWeb {
		return errors.New("enable_web_search requires enable_web")
	}
	if _, err := c.IdleTTL(); err != nil {
		return err
	}
	if _, err := c.PerCallTimeoutDuration(); err != nil {
		return err
	}
	if _, err := c.RetryBackoffDuration(); err != nil {
		return err
	}
	if !c.EnableBrowser {
		if c.BrowserCacheDir != "" {
			return errors.New("browser_cache_dir requires enable_browser")
		}
		if c.BrowserCacheTTL != "" {
			return errors.New("browser_cache_ttl requires enable_browser")
		}
		if c.BrowserCacheSweepInterval != "" {
			return errors.New("browser_cache_sweep_interval requires enable_browser")
		}
		if c.BrowserNoStealth {
			return errors.New("browser_no_stealth requires enable_browser")
		}
		if c.BrowserAllowAllDomains {
			return errors.New("browser_allow_all_domains requires enable_browser")
		}
		if c.BrowserScreenshot {
			return errors.New("browser_screenshot requires enable_browser")
		}
	} else if c.BrowserCacheDir == "" {
		if c.BrowserCacheTTL != "" {
			return errors.New("browser_cache_ttl requires browser_cache_dir")
		}
		if c.BrowserCacheSweepInterval != "" {
			return errors.New("browser_cache_sweep_interval requires browser_cache_dir")
		}
	} else {
		cacheTTL, err := c.BrowserCacheTTLDuration()
		if err != nil {
			return err
		}
		if _, err := c.BrowserCacheSweepIntervalDuration(cacheTTL); err != nil {
			return err
		}
	}
	if c.SubagentMaxDepth < 1 || c.SubagentMaxDepth > agent.MaxSubagentDepth {
		return fmt.Errorf("subagent_max_depth must be between 1 and %d", agent.MaxSubagentDepth)
	}
	if err := c.validateSampling(); err != nil {
		return err
	}
	return nil
}

func (c Config) validateSampling() error {
	if c.Temperature != nil {
		t := *c.Temperature
		if math.IsNaN(t) || math.IsInf(t, 0) || t < 0 || t > 2 {
			return fmt.Errorf("temperature must be between 0 and 2")
		}
	}
	if c.TopP != nil {
		t := *c.TopP
		if math.IsNaN(t) || math.IsInf(t, 0) || t < 0 || t > 1 {
			return fmt.Errorf("top_p must be between 0 and 1")
		}
	}
	if c.MaxTokens != nil && *c.MaxTokens <= 0 {
		return fmt.Errorf("max_tokens must be a positive integer")
	}
	return nil
}
