package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

	// MemoryRoot is the parent directory for DURABLE per-session state.
	// Conversation logs, runtime-installed skills, and memory live
	// separately from the session workspace so they survive LRU eviction
	// and server restarts: same session_id → same state dir, regardless
	// of how many times the workspace was recreated.
	// Empty defaults to "<WorkspaceRoot>/memory" (or an OS temp dir
	// when WorkspaceRoot itself is empty).
	MemoryRoot string `json:"memory_root"`

	// AccountRoot is durable account-level state shared by all sessions:
	// account env settings and account-installed skills. SSH keys use the
	// runtime user's standard ~/.ssh directory; container deployments should
	// mount HOME to an isolated persistent directory rather than the host's
	// real ~/.ssh.
	// Empty defaults to "<session root>/.affentserve" for compatibility.
	AccountRoot string `json:"account_root"`

	// MaxSessions caps the in-memory session pool size. Sessions
	// past the cap are LRU-evicted. Default 32.
	MaxSessions    int `json:"max_sessions"`
	maxSessionsSet bool

	// SessionIdleTTL closes sessions with no activity for at least
	// this long. Default 10m. Use a duration string ("30s", "10m").
	SessionIdleTTL string `json:"session_idle_ttl"`

	// SessionRetention controls how long a session's DURABLE state
	// (conversation log + runtime skills + memory under MemoryRoot/<id>/) is kept
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

	// MaxTurnInputTokens caps aggregate provider-reported prompt tokens spent
	// by one user turn across repeated assistant/tool calls. Zero falls back to
	// agent.DefaultMaxTurnInputTokens; negative is invalid.
	MaxTurnInputTokens int `json:"max_turn_input_tokens"`

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
	EnableBrowser    bool `json:"enable_browser"`
	enableBrowserSet bool

	// EnableWeb registers extras/web's web_fetch (and optionally
	// web_search). Disabled by default to keep the Tavily key
	// requirement opt-in.
	EnableWeb          bool `json:"enable_web"`
	enableWebSet       bool
	EnableWebSearch    bool `json:"enable_web_search"`
	enableWebSearchSet bool

	// EnableMemory exposes agent runtime's `memory` tool.
	EnableMemory    bool `json:"enable_memory"`
	enableMemorySet bool

	// SharedUserMemory stores target=user memory once under MemoryRoot/USER.md
	// instead of under each session directory. This is useful for local
	// single-user WebUI deployments where multiple sessions should share
	// stable user preferences. Leave disabled on multi-tenant servers.
	SharedUserMemory bool `json:"shared_user_memory"`

	// EnableBuiltins registers agent runtime's shell + file tools. Defaults
	// to false — running shell on behalf of remote callers is
	// dangerous on a shared host. Operators who want it must opt in
	// explicitly. When enabled, shell commands run as the affentserve
	// process's UID via executor.LocalExecutor; for kernel-level
	// isolation, run affentserve itself inside a sandbox.
	EnableBuiltins    bool `json:"enable_builtins"`
	enableBuiltinsSet bool

	// EvalMode freezes sessions to a strict benchmark surface. It
	// disables all tools by default; opt back in with EvalTools,
	// EvalAllTools, or explicit capability flags such as enable_memory,
	// enable_web, enable_browser, and enable_builtins.
	EvalMode bool `json:"eval_mode"`

	// EvalTools is a comma-separated allowlist used only with EvalMode.
	// Tool groups mirror affentctl: workspace, readonly_workspace, web,
	// browser, delegation, and all.
	EvalTools string `json:"eval_tools"`

	// EvalAllTools enables the full serve tool surface under EvalMode.
	EvalAllTools bool `json:"eval_all_tools"`

	// EnableLoopProtocol exposes explicit LOOP.md setup/maintenance tools
	// and feeds active protocols into future turns. Ordinary chat does not
	// create LOOP.md unless the model calls loop_protocol start_setup.
	// Existing LOOP.md files are still honored when this is false.
	EnableLoopProtocol    bool `json:"enable_loop_protocol"`
	enableLoopProtocolSet bool

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

	// EnableFocusedTasks registers the run_task tool — bounded focused
	// tasks with a per-kind tool whitelist and structured JSON output.
	// Independent of EnableSubagent: a deployment can run focused tasks
	// without exposing the free-form subagent_run surface, and vice versa.
	// On by default.
	EnableFocusedTasks    bool `json:"enable_focused_tasks"`
	enableFocusedTasksSet bool

	// BrowserScreenshot registers the browser_screenshot tool. Off by
	// default because the base64 image payload bloats tool result events
	// and text-only models can't act on it. Vision-capable callers
	// (qwen-vl, gpt-4o, claude-3.x) flip this on to let the agent see
	// the rendered page; the tool's save_path option keeps base64 out
	// of the result for callers that only want a PNG on disk.
	BrowserScreenshot    bool `json:"browser_screenshot"`
	browserScreenshotSet bool

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
	sharedUserMemoryFileName     = "USER.md"
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
		MaxSessions        *int  `json:"max_sessions"`
		EnableBrowser      *bool `json:"enable_browser"`
		BrowserScreenshot  *bool `json:"browser_screenshot"`
		EnableWeb          *bool `json:"enable_web"`
		EnableWebSearch    *bool `json:"enable_web_search"`
		EnableMemory       *bool `json:"enable_memory"`
		EnableLoopProtocol *bool `json:"enable_loop_protocol"`
		EnableSubagent     *bool `json:"enable_subagent"`
		SubagentMaxDepth   *int  `json:"subagent_max_depth"`
		EnableFocusedTasks *bool `json:"enable_focused_tasks"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return cfg, fmt.Errorf("parse config %s: %w", path, err)
	}
	cfg.maxSessionsSet = raw.MaxSessions != nil
	cfg.enableBrowserSet = raw.EnableBrowser != nil
	cfg.browserScreenshotSet = raw.BrowserScreenshot != nil
	cfg.enableWebSet = raw.EnableWeb != nil
	cfg.enableWebSearchSet = raw.EnableWebSearch != nil
	cfg.enableMemorySet = raw.EnableMemory != nil
	cfg.enableLoopProtocolSet = raw.EnableLoopProtocol != nil
	cfg.enableSubagentSet = raw.EnableSubagent != nil
	cfg.subagentMaxDepthSet = raw.SubagentMaxDepth != nil
	cfg.enableFocusedTasksSet = raw.EnableFocusedTasks != nil
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
	if !c.enableFocusedTasksSet {
		c.EnableFocusedTasks = true
	}
	if !c.enableLoopProtocolSet {
		c.EnableLoopProtocol = true
	}
	if c.EnableBuiltins {
		c.enableBuiltinsSet = true
	}
	for _, e := range []struct {
		envs []string
		dest *string
	}{
		{[]string{"AFFENTSERVE_BASE_URL"}, &c.BaseURL},
		{[]string{"AFFENTSERVE_API_KEY", "DASHSCOPE_API_KEY"}, &c.APIKey},
		{[]string{"AFFENTSERVE_MODEL"}, &c.Model},
		{[]string{"AFFENTSERVE_AUTH_TOKEN"}, &c.AuthToken},
		{[]string{"AFFENTSERVE_WORKSPACE_ROOT"}, &c.WorkspaceRoot},
		{[]string{"AFFENTSERVE_MEMORY_ROOT"}, &c.MemoryRoot},
		{[]string{"AFFENTSERVE_ACCOUNT_ROOT"}, &c.AccountRoot},
		{[]string{"AFFENTSERVE_SESSION_IDLE_TTL"}, &c.SessionIdleTTL},
		{[]string{"AFFENTSERVE_SESSION_RETENTION"}, &c.SessionRetention},
		{[]string{"AFFENTSERVE_PER_CALL_TIMEOUT"}, &c.PerCallTimeout},
		{[]string{"AFFENTSERVE_RETRY_BACKOFF"}, &c.RetryBackoff},
		{[]string{"AFFENTSERVE_EVAL_TOOLS"}, &c.EvalTools},
	} {
		if v := firstNonEmptyEnv(e.envs...); v != "" {
			*e.dest = v
		}
	}
	for _, e := range []struct {
		env  string
		dest *int
		set  *bool
	}{
		{"AFFENTSERVE_MAX_SESSIONS", &c.MaxSessions, &c.maxSessionsSet},
		{"AFFENTSERVE_MAX_TURN_STEPS", &c.MaxTurnSteps, nil},
		{"AFFENTSERVE_MAX_TURN_INPUT_TOKENS", &c.MaxTurnInputTokens, nil},
		{"AFFENTSERVE_MAX_TRANSIENT_RETRIES", &c.MaxTransientRetries, nil},
		{"AFFENTSERVE_COMPACT_TRIGGER", &c.CompactTrigger, nil},
		{"AFFENTSERVE_COMPACT_KEEP_LAST", &c.CompactKeepLast, nil},
	} {
		if v := os.Getenv(e.env); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil {
				return fmt.Errorf("%s=%q: %w", e.env, v, err)
			}
			*e.dest = n
			if e.set != nil {
				*e.set = true
			}
		}
	}
	for _, e := range []struct {
		env  string
		dest *bool
	}{
		{"AFFENTSERVE_BROWSER", &c.EnableBrowser},
		{"AFFENTSERVE_BROWSER_SCREENSHOT", &c.BrowserScreenshot},
		{"AFFENTSERVE_WEB", &c.EnableWeb},
		{"AFFENTSERVE_WEB_SEARCH", &c.EnableWebSearch},
		{"AFFENTSERVE_MEMORY", &c.EnableMemory},
		{"AFFENTSERVE_SHARED_USER_MEMORY", &c.SharedUserMemory},
		{"AFFENTSERVE_BUILTINS", &c.EnableBuiltins},
		{"AFFENTSERVE_EVAL_MODE", &c.EvalMode},
		{"AFFENTSERVE_EVAL_ALL_TOOLS", &c.EvalAllTools},
		{"AFFENTSERVE_LOOP_PROTOCOL", &c.EnableLoopProtocol},
		{"AFFENTSERVE_SUBAGENT", &c.EnableSubagent},
		{"AFFENTSERVE_FOCUSED_TASKS", &c.EnableFocusedTasks},
	} {
		if v := os.Getenv(e.env); v != "" {
			b, err := strconv.ParseBool(v)
			if err != nil {
				return fmt.Errorf("%s=%q: %w", e.env, v, err)
			}
			*e.dest = b
			switch e.env {
			case "AFFENTSERVE_BROWSER":
				c.enableBrowserSet = true
			case "AFFENTSERVE_BROWSER_SCREENSHOT":
				c.browserScreenshotSet = true
			case "AFFENTSERVE_WEB":
				c.enableWebSet = true
			case "AFFENTSERVE_WEB_SEARCH":
				c.enableWebSearchSet = true
			case "AFFENTSERVE_MEMORY":
				c.enableMemorySet = true
			case "AFFENTSERVE_LOOP_PROTOCOL":
				c.enableLoopProtocolSet = true
			case "AFFENTSERVE_BUILTINS":
				c.enableBuiltinsSet = true
			}
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

type serveRuntimeCapabilities struct {
	Builtins          bool
	Memory            bool
	Browser           bool
	BrowserScreenshot bool
	Web               bool
	WebSearch         bool
	Subagent          bool
	FocusedTasks      bool
	WorkflowTools     bool
}

func resolveServeRuntimeCapabilities(c Config) serveRuntimeCapabilities {
	caps := serveRuntimeCapabilities{
		Builtins:          c.EnableBuiltins,
		Memory:            c.EnableMemory,
		Browser:           c.EnableBrowser,
		BrowserScreenshot: c.EnableBrowser && c.BrowserScreenshot,
		Web:               c.EnableWeb,
		WebSearch:         c.EnableWeb && c.EnableWebSearch,
		Subagent:          c.EnableSubagent,
		FocusedTasks:      c.EnableFocusedTasks,
		WorkflowTools:     true,
	}
	if !serveEvalModeEnabled(c) {
		return caps
	}
	if c.EvalAllTools {
		caps.Builtins = true
		caps.Memory = true
		caps.Browser = true
		caps.BrowserScreenshot = true
		caps.Web = true
		caps.WebSearch = true
		caps.Subagent = true
		caps.FocusedTasks = true
		caps.WorkflowTools = true
		return caps
	}
	allowed, _ := serveEvalToolAllowlist(c)
	// Eval mode starts from a no-tool surface. Operators may explicitly
	// opt into just the tool families an eval suite needs.
	caps.Builtins = (c.enableBuiltinsSet && c.EnableBuiltins) || serveEvalAllowlistHasBuiltin(allowed)
	caps.Memory = (c.enableMemorySet && c.EnableMemory) || allowed[agent.MemoryToolName]
	caps.Browser = (c.enableBrowserSet && c.EnableBrowser) || serveEvalAllowlistHasBrowser(allowed)
	caps.BrowserScreenshot = caps.Browser && c.browserScreenshotSet && c.BrowserScreenshot
	if allowed["browser_screenshot"] {
		caps.BrowserScreenshot = true
	}
	caps.Web = (c.enableWebSet && c.EnableWeb) || allowed["web_fetch"] || allowed["web_search"]
	caps.WebSearch = (caps.Web && c.enableWebSearchSet && c.EnableWebSearch) || allowed["web_search"]
	caps.Subagent = (c.enableSubagentSet && c.EnableSubagent) || allowed[agent.SubagentToolName]
	caps.FocusedTasks = (c.enableFocusedTasksSet && c.EnableFocusedTasks) || allowed[agent.FocusedTaskToolName]
	caps.WorkflowTools = allowed[agent.SkillToolName] || allowed[agent.PlanToolName] || allowed[agent.SessionSearchToolName]
	return caps
}

func serveEvalModeEnabled(c Config) bool {
	return c.EvalMode || strings.TrimSpace(c.EvalTools) != "" || c.EvalAllTools
}

func serveEvalToolAllowlist(c Config) (map[string]bool, []string) {
	allowed := map[string]bool{}
	var requested []string
	add := func(name string) {
		name = strings.TrimSpace(name)
		if name == "" {
			return
		}
		if !allowed[name] {
			requested = append(requested, name)
		}
		allowed[name] = true
	}
	for _, raw := range strings.FieldsFunc(c.EvalTools, func(r rune) bool {
		return r == ',' || r == ';' || r == ' ' || r == '\n' || r == '\t'
	}) {
		switch name := strings.TrimSpace(raw); name {
		case "", "none":
			continue
		case "all":
			for _, n := range serveEvalKnownToolNames() {
				add(n)
			}
		case "workspace":
			for _, n := range serveEvalWorkspaceToolNames() {
				add(n)
			}
		case "readonly_workspace":
			for _, n := range serveEvalReadonlyWorkspaceToolNames() {
				add(n)
			}
		case "web":
			add("web_fetch")
			add("web_search")
		case "browser":
			for _, n := range serveEvalBrowserToolNames() {
				add(n)
			}
		case "recall":
			add(agent.MemoryToolName)
			add(agent.SessionSearchToolName)
		case "delegation":
			add(agent.SubagentToolName)
			add(agent.FocusedTaskToolName)
		default:
			add(name)
		}
	}
	return allowed, requested
}

func serveEvalWorkspaceToolNames() []string {
	return []string{"shell", "read_file", "file_context", "write_file", "edit_file", "list_files", agent.SymbolContextToolName, "repo_search"}
}

func serveEvalReadonlyWorkspaceToolNames() []string {
	return []string{"read_file", "file_context", "list_files", agent.SymbolContextToolName, "repo_search"}
}

func serveEvalBrowserToolNames() []string {
	return []string{"browser_navigate", "browser_snapshot", "browser_find", "browser_network", "browser_network_read", "browser_click", "browser_scroll", "browser_type", "browser_wait", "browser_screenshot"}
}

func serveEvalKnownToolNames() []string {
	out := append([]string{}, serveEvalWorkspaceToolNames()...)
	out = append(out, agent.SkillToolName, agent.MemoryToolName, agent.PlanToolName, agent.SessionSearchToolName, agent.SubagentToolName, agent.FocusedTaskToolName, "web_fetch", "web_search")
	out = append(out, serveEvalBrowserToolNames()...)
	return out
}

func serveEvalAllowlistHasBuiltin(allowed map[string]bool) bool {
	for _, name := range append(serveEvalWorkspaceToolNames(), agent.SkillToolName, agent.PlanToolName) {
		if allowed[name] {
			return true
		}
	}
	return false
}

func serveEvalAllowlistHasBrowser(allowed map[string]bool) bool {
	for _, name := range serveEvalBrowserToolNames() {
		if allowed[name] {
			return true
		}
	}
	return false
}

func (c Config) EffectiveRuntimeConfig() Config {
	if !serveEvalModeEnabled(c) {
		return c
	}
	c.EvalMode = true
	caps := resolveServeRuntimeCapabilities(c)
	c.EnableBuiltins = caps.Builtins
	c.EnableMemory = caps.Memory
	c.EnableBrowser = caps.Browser
	c.BrowserScreenshot = caps.BrowserScreenshot
	c.EnableWeb = caps.Web
	c.EnableWebSearch = caps.WebSearch
	c.EnableSubagent = caps.Subagent
	c.EnableFocusedTasks = caps.FocusedTasks
	c.EnableLoopProtocol = false
	if !caps.Browser {
		c.BrowserScreenshot = false
		c.BrowserCacheDir = ""
		c.BrowserCacheTTL = ""
		c.BrowserCacheSweepInterval = ""
		c.BrowserNoStealth = false
		c.BrowserAllowAllDomains = false
	}
	return c
}

func (c *Config) ApplyEvalMode() {
	if c == nil {
		return
	}
	*c = c.EffectiveRuntimeConfig()
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
		return fmt.Errorf("api_key is required when base_url is %s (use --api-key, AFFENTSERVE_API_KEY, or DASHSCOPE_API_KEY)", agent.DefaultBaseURL)
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
	if c.MaxTurnInputTokens < 0 {
		return fmt.Errorf("max_turn_input_tokens must be zero or a positive integer")
	}
	if c.CompactTrigger < 0 {
		return fmt.Errorf("compact_trigger must be zero or a positive integer")
	}
	if c.CompactKeepLast < 0 {
		return fmt.Errorf("compact_keep_last must be zero or a positive integer")
	}
	caps := resolveServeRuntimeCapabilities(c)
	if c.EnableWebSearch && !caps.Web {
		return errors.New("enable_web_search requires enable_web")
	}
	if caps.WebSearch {
		if err := validateSearchBackendEnv(); err != nil {
			return err
		}
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
	if !caps.Browser {
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
	sampling := agent.SamplingDefaults{
		Temperature: c.Temperature,
		TopP:        c.TopP,
		MaxTokens:   c.MaxTokens,
	}
	if err := sampling.Validate(); err != nil {
		return err
	}
	return nil
}

func validateSearchBackendEnv() error {
	provider := configuredSearchProvider()
	if provider == "" {
		provider = "auto"
	}
	switch provider {
	case "auto":
		return nil
	case "tavily":
		if tavilySearchConfigured() {
			return nil
		}
		return errors.New("AFFENT_WEB_SEARCH_PROVIDER=tavily requires TAVILY_API_KEY")
	case "google":
		if googleSearchConfigured() {
			return nil
		}
		return errors.New("AFFENT_WEB_SEARCH_PROVIDER=google requires GOOGLE_CSE_API_KEY or GOOGLE_API_KEY, plus GOOGLE_CSE_ID or GOOGLE_SEARCH_ENGINE_ID")
	default:
		return fmt.Errorf("unsupported AFFENT_WEB_SEARCH_PROVIDER=%q; valid values are auto, tavily, google", provider)
	}
}

func configuredSearchBackendName() string {
	switch provider := configuredSearchProvider(); provider {
	case "", "auto":
		if tavilySearchConfigured() {
			return "tavily"
		}
		if googleSearchConfigured() {
			return "google"
		}
		return "html"
	case "tavily", "google":
		return provider
	default:
		return "invalid"
	}
}

func configuredSearchProvider() string {
	return strings.ToLower(strings.TrimSpace(os.Getenv("AFFENT_WEB_SEARCH_PROVIDER")))
}

func tavilySearchConfigured() bool {
	return strings.TrimSpace(os.Getenv("TAVILY_API_KEY")) != ""
}

func googleSearchConfigured() bool {
	return googleSearchAPIKeyConfigured() && googleSearchEngineIDConfigured()
}

func googleSearchAPIKeyConfigured() bool {
	return firstNonEmptyEnv("GOOGLE_CSE_API_KEY", "GOOGLE_CUSTOM_SEARCH_API_KEY", "GOOGLE_API_KEY") != ""
}

func googleSearchEngineIDConfigured() bool {
	return firstNonEmptyEnv("GOOGLE_CSE_ID", "GOOGLE_CUSTOM_SEARCH_ENGINE_ID", "GOOGLE_SEARCH_ENGINE_ID", "GOOGLE_CSE_CX", "GOOGLE_CUSTOM_SEARCH_CX") != ""
}

func firstNonEmptyEnv(names ...string) string {
	for _, name := range names {
		if v := strings.TrimSpace(os.Getenv(name)); v != "" {
			return v
		}
	}
	return ""
}
