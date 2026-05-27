package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	affentbrowser "github.com/affinefoundation/affent/extras/browser"
	affentweb "github.com/affinefoundation/affent/extras/web"
	agent "github.com/affinefoundation/affent/internal/agent"
	"github.com/affinefoundation/affent/internal/eventlog"
	"github.com/affinefoundation/affent/internal/executor"
	"github.com/affinefoundation/affent/internal/mcp"
	"github.com/affinefoundation/affent/internal/memory"
	"github.com/affinefoundation/affent/internal/sse"
	"github.com/affinefoundation/affent/internal/textutil"
	"github.com/google/uuid"
	"github.com/rs/zerolog"
)

const maxConfigInputBytes = 1024 * 1024

const (
	// exitUsage signals a user/config error (bad flags, missing required
	// args). Matches sysexits.h EX_USAGE.
	exitUsage = 64
	// exitRuntime signals a runtime failure the user couldn't prevent
	// via flags (I/O errors, model failures, container problems).
	exitRuntime = 3
)

const (
	minMCPStartupTimeout       = 60 * time.Second
	mcpStartupPerServerOverrun = 5 * time.Second
)

// trimUTF8 returns the UTF-8-safe prefix of s clipped to at most n
// bytes, so multi-byte sequences (CJK / Cyrillic / accented Latin /
// emoji) aren't split across the cut.
func trimUTF8(s string, n int) string {
	head, _ := textutil.TruncateWithMarker(s, n, func(int) string { return "" })
	return head
}

// commonFlags is what every subcommand wants -- model endpoint, workspace,
// trace destination, system-prompt override, session selection. Bind once,
// register on each command's FlagSet via bind().
type commonFlags struct {
	configPath       string
	workspace        string
	baseURL          string
	apiKey           string
	model            string
	maxTurns         int
	callTimeout      time.Duration
	retryTransient   int
	retryBackoff     time.Duration
	tracePath        string
	traceSkipDeltas  bool
	systemPromptPath string
	quiet            bool
	// evalMode freezes the runtime to a strict single-loop benchmark
	// surface. It disables all tools by default; opt back in with
	// --eval-tools, --eval-all-tools, or explicit capability flags
	// such as --memory=true / --web=true.
	evalMode     bool
	evalTools    string
	evalAllTools bool

	// memoryEnabled composes the MEMORY.md / USER.md snapshot into
	// the system prompt at session start and registers the `memory`
	// tool.
	memoryEnabled  bool
	memoryExplicit bool
	// memoryOnly registers only the `memory` tool (no shell, files,
	// MCP) and disables project-context injection. Implies memoryEnabled.
	memoryOnly           bool
	memoryWorkspaceStore string // (legacy) path to a pre-v2 MEMORY.md file used only for migration detection
	memoryDir            string // path for the v2 memory dir ("" → <workspace>/.affent/memory)
	memoryUserStore      string // path for USER.md  ("" → $XDG_CONFIG_HOME/affent/USER.md)
	memoryMaxChars       string // "CORE,USER" or legacy "MEM,USER"; "" → defaults
	memoryTopicMaxChars  int    // per-topic char cap; <=0 → DefaultTopicCharLimit
	memoryMaxTopics      int    // distinct-topic count cap; <=0 → DefaultMaxTopics

	// projectContext loads recognized user-authored project notes
	// (AGENTS.md, CONVENTIONS.md, .cursorrules, .clinerules, CLAUDE.md,
	// GEMINI.md) from --workspace and inlines them into the system
	// prompt. Default on; set --project-context=false to disable.
	projectContext bool

	compactTrigger  int // <=0 falls back to agent.DefaultSummaryTriggerMsgs
	compactKeepLast int

	sessionID    string // explicit; empty means "use --continue or new"
	continueLast bool   // pick most recent session under workspace

	mcpConfigPath string // path to MCP server config JSON (optional)

	// executor selects the shell-tool backend.
	//   "local"            — run on the host (default; current behavior)
	//   "docker:<cid>"     — run inside the named container via `docker exec`
	executor string

	// subagentEnabled registers subagent_run for bounded read-only
	// delegation. Default on for affentctl; disable for strict single-
	// loop evals or when nested model calls are not desired.
	subagentEnabled bool
	// subagentMaxDepth caps recursive subagent layers. 1 means only a
	// direct child; the default allows one child to delegate one noisy
	// subtask while still preventing open-ended agent chains.
	subagentMaxDepth int

	// focusedTasksEnabled registers the productized run_task tool for
	// bounded, structured-output delegation (recall / explore /
	// research / verify / review). Independent of subagentEnabled —
	// the two surfaces target different model behaviors and can be
	// toggled independently. Default on.
	focusedTasksEnabled bool

	// webEnabled registers extras/web tools. It is off by default for
	// affentctl so local coding runs do not unexpectedly use network,
	// but eval/debug drivers can opt in for real external research.
	webEnabled        bool
	webSearchEnabled  bool
	browserEnabled    bool
	browserScreenshot bool

	// Sampling pass-through to the upstream LLM. Strings preserve the
	// "unset" / "explicit 0" distinction that evals need (temperature=0
	// for deterministic decoding must not be confused with "use provider
	// default"). Parsed into pointers in setupLoop.
	temperature string
	topP        string
	maxTokens   string
	seed        string
}

func (c *commonFlags) bind(fs *flag.FlagSet) {
	fs.StringVar(&c.configPath, "config", "", "JSON config file; CLI flags override config values (env: AFFENTCTL_CONFIG)")
	fs.StringVar(&c.workspace, "workspace", "./affent-workspace", "working dir for shell + file tools")
	fs.StringVar(&c.baseURL, "base-url", "", "OpenAI-compat endpoint (env: AFFENTCTL_BASE_URL)")
	fs.StringVar(&c.apiKey, "api-key", "", "API key (env: AFFENTCTL_API_KEY)")
	fs.StringVar(&c.model, "model", "", "model id (env: AFFENTCTL_MODEL)")
	fs.IntVar(&c.maxTurns, "max-turns", 10, "max tool-call rounds per user message (env: AFFENTCTL_MAX_TURNS)")
	fs.DurationVar(&c.callTimeout, "max-call-timeout", agent.DefaultPerCallTimeout, "per-LLM-call timeout (env: AFFENTCTL_MAX_CALL_TIMEOUT)")
	fs.IntVar(&c.retryTransient, "retry-transient", agent.DefaultTransientRetries, "retry attempts on transient LLM errors (5xx/429/408/net/EOF/timeout); 0 disables (env: AFFENTCTL_RETRY_TRANSIENT)")
	fs.DurationVar(&c.retryBackoff, "retry-backoff", agent.DefaultTransientBackoff, "initial backoff between retries; doubles each attempt (env: AFFENTCTL_RETRY_BACKOFF)")
	fs.StringVar(&c.tracePath, "trace", "", "JSONL trace path; '-' for stdout, '' for stderr")
	fs.BoolVar(&c.traceSkipDeltas, "trace-skip-deltas", false, "skip thinking/message deltas in trace (smaller trace, no token-level replay; final text still in message.end)")
	fs.StringVar(&c.systemPromptPath, "system-prompt", "", "override system prompt; '-' or file path or literal")
	fs.BoolVar(&c.quiet, "quiet", false, "suppress stderr progress")
	fs.BoolVar(&c.evalMode, "eval-mode", false, "strict benchmark mode: disable all tools by default; opt in with --eval-tools, --eval-all-tools, --memory=true, --web=true, --browser=true, or --mcp-config (env: AFFENTCTL_EVAL_MODE)")
	fs.StringVar(&c.evalTools, "eval-tools", "", "comma-separated eval tool allowlist; implies --eval-mode. Examples: read_file,shell,repo_search; groups: workspace,readonly_workspace,web,browser,recall,delegation,mcp,all (env: AFFENTCTL_EVAL_TOOLS)")
	fs.BoolVar(&c.evalAllTools, "eval-all-tools", false, "enable eval mode with the full tool surface instead of the default no-tool surface (env: AFFENTCTL_EVAL_ALL_TOOLS)")
	fs.BoolVar(&c.memoryEnabled, "memory", true, "enable persistent memory: inject MEMORY.md / USER.md snapshot into the system prompt and register the memory tool (env: AFFENTCTL_MEMORY)")
	fs.BoolVar(&c.memoryOnly, "memory-only", false, "register only the memory tool (no shell/file/MCP) and disable project context; for memory benchmarks. Implies --memory (env: AFFENTCTL_MEMORY_ONLY)")
	fs.StringVar(&c.memoryWorkspaceStore, "memory-workspace-store", "", "(legacy) path to a pre-v2 single-file MEMORY.md; if set, migration moves it into the v2 topic layout on first access. Prefer --memory-dir for new setups.")
	fs.StringVar(&c.memoryDir, "memory-dir", "", "path to the v2 memory dir (core.md + topics/*.md); default <workspace>/.affent/memory")
	fs.StringVar(&c.memoryUserStore, "memory-user-store", "", "path to USER.md; default $XDG_CONFIG_HOME/affent/USER.md (cross-workspace)")
	fs.StringVar(&c.memoryMaxChars, "memory-max-chars", "", "char limits as CORE,USER (default 2200,1375). Per-topic cap → --memory-topic-max-chars. (env: AFFENTCTL_MEMORY_MAX_CHARS)")
	fs.IntVar(&c.memoryTopicMaxChars, "memory-topic-max-chars", 0, "per-topic char cap; 0 → DefaultTopicCharLimit (4400). Each custom topic (auth, deploy, ...) is bounded independently; total memory grows by topic count. (env: AFFENTCTL_MEMORY_TOPIC_MAX_CHARS)")
	fs.IntVar(&c.memoryMaxTopics, "memory-max-topics", 0, "distinct-topic count cap; 0 → DefaultMaxTopics (32). Pass a large number (e.g. 1000) to effectively disable for memory benchmarks that legitimately want many named scratchpads. (env: AFFENTCTL_MEMORY_MAX_TOPICS)")
	fs.BoolVar(&c.projectContext, "project-context", true, "auto-load AGENTS.md / CONVENTIONS.md / .cursorrules / .clinerules / CLAUDE.md / GEMINI.md from --workspace into the system prompt (env: AFFENTCTL_PROJECT_CONTEXT)")
	fs.StringVar(&c.sessionID, "session-id", "", "resume the named session (under --workspace/.affentctl/)")
	fs.BoolVar(&c.continueLast, "continue", false, "resume the most recent session under --workspace")
	fs.StringVar(&c.mcpConfigPath, "mcp-config", "", "path to MCP server config JSON ({\"servers\":[{...}]}) (env: AFFENTCTL_MCP_CONFIG)")
	fs.IntVar(&c.compactTrigger, "compact-trigger", 240, "compact conversation when message count exceeds this. 0 / negative → fall back to agent runtime's default (240). Reactive compaction (on context-overflow errors) is unaffected. (env: AFFENTCTL_COMPACT_TRIGGER)")
	fs.IntVar(&c.compactKeepLast, "compact-keep-last", 10, "messages preserved verbatim at the tail of the conversation when compacting (env: AFFENTCTL_COMPACT_KEEP_LAST)")
	fs.StringVar(&c.executor, "executor", "local", "shell-tool backend: 'local' (host; no isolation), 'sandbox' (auto-start affentctl's memory-limited Docker sandbox), or 'docker:<container_id>' (exec into an existing container). (env: AFFENTCTL_EXECUTOR)")
	fs.BoolVar(&c.subagentEnabled, "subagent", true, "register subagent_run for bounded read-only delegation; set false to force a single-loop agent (env: AFFENTCTL_SUBAGENT)")
	fs.IntVar(&c.subagentMaxDepth, "subagent-max-depth", agent.DefaultSubagentMaxDepth, "maximum recursive subagent depth; 1 disables nested subagents, hard max 4 (env: AFFENTCTL_SUBAGENT_MAX_DEPTH)")
	fs.BoolVar(&c.focusedTasksEnabled, "focused-tasks", true, "register run_task for bounded focused tasks with structured output; set false to hide the surface (env: AFFENTCTL_FOCUSED_TASKS)")
	fs.BoolVar(&c.webEnabled, "web", false, "register web_fetch for public web retrieval; blocks private network addresses by default (env: AFFENTCTL_WEB)")
	fs.BoolVar(&c.webSearchEnabled, "web-search", false, "register web_search alongside web_fetch; requires --web (env: AFFENTCTL_WEB_SEARCH)")
	fs.BoolVar(&c.browserEnabled, "browser", false, "register browser_navigate/browser_snapshot/browser_find/browser_network tools for rendered web debugging (env: AFFENTCTL_BROWSER)")
	fs.BoolVar(&c.browserScreenshot, "browser-screenshot", false, "also register browser_screenshot; off by default because inline image payloads are large (env: AFFENTCTL_BROWSER_SCREENSHOT)")
	fs.StringVar(&c.temperature, "temperature", "", "sampling temperature forwarded to upstream LLM (omit → provider default; set 0 for deterministic eval decoding; env: AFFENTCTL_TEMPERATURE)")
	fs.StringVar(&c.topP, "top-p", "", "top-p (nucleus) sampling forwarded to upstream (omit → provider default; env: AFFENTCTL_TOP_P)")
	fs.StringVar(&c.maxTokens, "max-tokens", "", "max output tokens forwarded to upstream (omit → provider default; env: AFFENTCTL_MAX_TOKENS)")
	fs.StringVar(&c.seed, "seed", "", "deterministic-sampling seed forwarded to upstream (omit → provider default; env: AFFENTCTL_SEED)")
}

// parseSampling converts the string-shaped CLI/file values into a
// SamplingDefaults. Empty strings stay nil — that's how affent
// distinguishes "use upstream default" from "explicit 0".
func parseSampling(temperature, topP, maxTokens, seed string) (agent.SamplingDefaults, error) {
	var s agent.SamplingDefaults
	if temperature != "" {
		t, err := strconv.ParseFloat(temperature, 64)
		if err != nil {
			return s, fmt.Errorf("--temperature: %w", err)
		}
		s.Temperature = &t
	}
	if topP != "" {
		t, err := strconv.ParseFloat(topP, 64)
		if err != nil {
			return s, fmt.Errorf("--top-p: %w", err)
		}
		s.TopP = &t
	}
	if maxTokens != "" {
		n, err := strconv.Atoi(maxTokens)
		if err != nil {
			return s, fmt.Errorf("--max-tokens: %w", err)
		}
		s.MaxTokens = &n
	}
	if seed != "" {
		n, err := strconv.ParseInt(seed, 10, 64)
		if err != nil {
			return s, fmt.Errorf("--seed: %w", err)
		}
		s.Seed = &n
	}
	if err := s.Validate(); err != nil {
		return s, samplingFlagError(err)
	}
	return s, nil
}

func samplingFlagError(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	switch {
	case strings.HasPrefix(msg, "temperature "):
		return fmt.Errorf("--temperature %s", strings.TrimPrefix(msg, "temperature "))
	case strings.HasPrefix(msg, "top_p "):
		return fmt.Errorf("--top-p %s", strings.TrimPrefix(msg, "top_p "))
	case strings.HasPrefix(msg, "max_tokens "):
		return fmt.Errorf("--max-tokens %s", strings.TrimPrefix(msg, "max_tokens "))
	default:
		return err
	}
}

func effectiveBaseURL(baseURL string) string {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		return agent.DefaultBaseURL
	}
	return strings.TrimRight(baseURL, "/")
}

func defaultEndpointRequiresAPIKey(baseURL string) bool {
	return effectiveBaseURL(baseURL) == agent.DefaultBaseURL
}

func validateLLMConfig(c commonFlags) error {
	if strings.TrimSpace(c.model) == "" {
		return fmt.Errorf("--model (or AFFENTCTL_MODEL) is required")
	}
	if defaultEndpointRequiresAPIKey(c.baseURL) && strings.TrimSpace(c.apiKey) == "" {
		return fmt.Errorf("--api-key (or AFFENTCTL_API_KEY) is required when using the default %s endpoint; set --base-url for an endpoint that does not require auth", agent.DefaultBaseURL)
	}
	return nil
}

// flagEnvSources maps flag-name → env-var-name for every flag whose
// default reads from an env var. Used by loadConfigFile so the env
// var beats the config file (matches the documented precedence in
// the flag table: env is a first-class lane, not a default).
//
// Bind kept in sync with the `bind` method above. Tests in
// common_test.go catch drift.
var flagEnvSources = map[string]string{
	"config":             "AFFENTCTL_CONFIG",
	"workspace":          "AFFENTCTL_WORKSPACE",
	"base-url":           "AFFENTCTL_BASE_URL",
	"api-key":            "AFFENTCTL_API_KEY",
	"model":              "AFFENTCTL_MODEL",
	"mcp-config":         "AFFENTCTL_MCP_CONFIG",
	"executor":           "AFFENTCTL_EXECUTOR",
	"eval-mode":          "AFFENTCTL_EVAL_MODE",
	"eval-tools":         "AFFENTCTL_EVAL_TOOLS",
	"eval-all-tools":     "AFFENTCTL_EVAL_ALL_TOOLS",
	"subagent":           "AFFENTCTL_SUBAGENT",
	"subagent-max-depth": "AFFENTCTL_SUBAGENT_MAX_DEPTH",
	"focused-tasks":      "AFFENTCTL_FOCUSED_TASKS",
	"web":                "AFFENTCTL_WEB",
	"web-search":         "AFFENTCTL_WEB_SEARCH",
	"browser":            "AFFENTCTL_BROWSER",
	"browser-screenshot": "AFFENTCTL_BROWSER_SCREENSHOT",
	"temperature":        "AFFENTCTL_TEMPERATURE",
	"top-p":              "AFFENTCTL_TOP_P",
	"max-tokens":         "AFFENTCTL_MAX_TOKENS",
	"seed":               "AFFENTCTL_SEED",
}

func configPrecedenceEnvSources() map[string]string {
	out := make(map[string]string, len(flagEnvSources)+12)
	for name, env := range flagEnvSources {
		out[name] = env
	}
	for name, env := range map[string]string{
		"max-turns":              "AFFENTCTL_MAX_TURNS",
		"max-call-timeout":       "AFFENTCTL_MAX_CALL_TIMEOUT",
		"retry-transient":        "AFFENTCTL_RETRY_TRANSIENT",
		"retry-backoff":          "AFFENTCTL_RETRY_BACKOFF",
		"memory":                 "AFFENTCTL_MEMORY",
		"memory-only":            "AFFENTCTL_MEMORY_ONLY",
		"memory-max-chars":       "AFFENTCTL_MEMORY_MAX_CHARS",
		"memory-topic-max-chars": "AFFENTCTL_MEMORY_TOPIC_MAX_CHARS",
		"memory-max-topics":      "AFFENTCTL_MEMORY_MAX_TOPICS",
		"project-context":        "AFFENTCTL_PROJECT_CONTEXT",
		"compact-trigger":        "AFFENTCTL_COMPACT_TRIGGER",
		"compact-keep-last":      "AFFENTCTL_COMPACT_KEEP_LAST",
	} {
		out[name] = env
	}
	return out
}

type fileConfig struct {
	Workspace       *string `json:"workspace"`
	BaseURL         *string `json:"base_url"`
	APIKey          *string `json:"api_key"`
	Model           *string `json:"model"`
	MaxTurns        *int    `json:"max_turns"`
	MaxCallTimeout  *string `json:"max_call_timeout"`
	RetryTransient  *int    `json:"retry_transient"`
	RetryBackoff    *string `json:"retry_backoff"`
	Trace           *string `json:"trace"`
	TraceSkipDeltas *bool   `json:"trace_skip_deltas"`
	SystemPrompt    *string `json:"system_prompt"`
	Quiet           *bool   `json:"quiet"`
	EvalMode        *bool   `json:"eval_mode"`
	EvalTools       *string `json:"eval_tools"`
	EvalAllTools    *bool   `json:"eval_all_tools"`
	Memory          *struct {
		Enabled        *bool   `json:"enabled"`
		Only           *bool   `json:"only"`
		WorkspaceStore *string `json:"workspace_store"` // legacy single-file pointer; triggers migration
		Dir            *string `json:"dir"`             // v2 memory directory
		UserStore      *string `json:"user_store"`
		MaxChars       *string `json:"max_chars"`
		TopicMaxChars  *int    `json:"topic_max_chars"`
		MaxTopics      *int    `json:"max_topics"`
	} `json:"memory"`
	ProjectContext *bool `json:"project_context"`
	Compact        *struct {
		Trigger  *int `json:"trigger"`
		KeepLast *int `json:"keep_last"`
	} `json:"compact"`
	SessionID *string `json:"session_id"`
	Continue  *bool   `json:"continue"`
	MCPConfig *string `json:"mcp_config"`
	Executor  *string `json:"executor"`
	// Subagent is the affentctl-native key. EnableSubagent mirrors
	// affentserve's config spelling so shared config templates can
	// use the same name in both binaries.
	Subagent         *bool `json:"subagent"`
	EnableSubagent   *bool `json:"enable_subagent"`
	SubagentMaxDepth *int  `json:"subagent_max_depth"`
	// FocusedTasks is the affentctl-native key. EnableFocusedTasks
	// mirrors affentserve's config spelling so shared config templates
	// can use the same name in both binaries.
	FocusedTasks       *bool `json:"focused_tasks"`
	EnableFocusedTasks *bool `json:"enable_focused_tasks"`
	// Web is the affentctl-native key. EnableWeb mirrors
	// affentserve's config spelling so shared config templates can
	// use the same name in both binaries.
	Web       *bool `json:"web"`
	EnableWeb *bool `json:"enable_web"`
	// WebSearch is the affentctl-native key. EnableWebSearch mirrors
	// affentserve's config spelling.
	WebSearch       *bool `json:"web_search"`
	EnableWebSearch *bool `json:"enable_web_search"`
	// Browser mirrors affentserve's browser runtime switch for ad-hoc
	// CLI/eval debugging.
	Browser           *bool `json:"browser"`
	EnableBrowser     *bool `json:"enable_browser"`
	BrowserScreenshot *bool `json:"browser_screenshot"`
	// Sampling forwarded to upstream. Kept as strings to mirror the CLI
	// flags and preserve the "unset vs explicit 0" distinction that
	// pointers give us at the wire layer.
	Temperature *string `json:"temperature"`
	TopP        *string `json:"top_p"`
	MaxTokens   *string `json:"max_tokens"`
	Seed        *string `json:"seed"`
}

func applyConfig(c *commonFlags, fs *flag.FlagSet) error {
	if err := loadConfigFile(c, fs); err != nil {
		return err
	}
	if err := applyEnvConfig(c, fs); err != nil {
		return err
	}
	if c.executor == "sandbox" && c.workspace == "./affent-workspace" && !flagWasSet(fs, "workspace") && os.Getenv("AFFENTCTL_WORKSPACE") == "" {
		c.workspace = defaultSandboxWorkspace()
	}
	c.memoryExplicit = c.memoryExplicit || flagWasSet(fs, "memory") || os.Getenv("AFFENTCTL_MEMORY") != ""
	// memory-only is the isolation mode: register only the memory
	// tool and inject no other content sources into the system prompt.
	// It implies --memory=true and forces --project-context=false
	// regardless of how either was set.
	if c.memoryOnly {
		c.memoryEnabled = true
		c.memoryExplicit = true
		c.projectContext = false
		c.subagentEnabled = false
		// Focused tasks need a workspace tool surface (read_file,
		// shell, web) that memory-only mode deliberately omits, so
		// they're disabled together for the same isolation reason.
		c.focusedTasksEnabled = false
	}
	if strings.TrimSpace(c.evalTools) != "" || c.evalAllTools {
		c.evalMode = true
	}
	if err := normalizeRuntimeLimits(c); err != nil {
		return err
	}
	return nil
}

func normalizeRuntimeLimits(c *commonFlags) error {
	if c.maxTurns <= 0 {
		return fmt.Errorf("--max-turns must be a positive integer")
	}
	if c.callTimeout <= 0 {
		return fmt.Errorf("--max-call-timeout must be a positive duration")
	}
	if c.retryTransient < 0 {
		return fmt.Errorf("--retry-transient must be zero or a positive integer")
	}
	if c.retryTransient == 0 {
		// Loop uses negative to disable transient retries; affentctl's
		// user-facing flag documents 0 as the disable value.
		c.retryTransient = -1
	}
	if c.retryBackoff <= 0 {
		return fmt.Errorf("--retry-backoff must be a positive duration")
	}
	if c.memoryTopicMaxChars < 0 {
		return fmt.Errorf("--memory-topic-max-chars must be zero or a positive integer")
	}
	if c.memoryMaxTopics < 0 {
		return fmt.Errorf("--memory-max-topics must be zero or a positive integer")
	}
	if c.subagentMaxDepth < 1 || c.subagentMaxDepth > agent.MaxSubagentDepth {
		return fmt.Errorf("--subagent-max-depth must be between 1 and %d", agent.MaxSubagentDepth)
	}
	if c.webSearchEnabled && !c.webEnabled {
		return fmt.Errorf("--web-search requires --web")
	}
	if c.browserScreenshot && !c.browserEnabled {
		return fmt.Errorf("--browser-screenshot requires --browser")
	}
	return nil
}

func flagWasSet(fs *flag.FlagSet, name string) bool {
	wasSet := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			wasSet = true
		}
	})
	return wasSet
}

func loadConfigFile(c *commonFlags, fs *flag.FlagSet) error {
	if c.configPath == "" {
		// AFFENTCTL_CONFIG was previously surfaced as a flag default,
		// which leaked the path into --help output. The default is now
		// empty; the env-var lookup moves here so --config still has
		// its env lane without showing up in usage.
		c.configPath = os.Getenv("AFFENTCTL_CONFIG")
		if c.configPath == "" {
			return nil
		}
	}
	var cfg fileConfig
	if err := readConfigJSON(c.configPath, &cfg); err != nil {
		return fmt.Errorf("load config %s: %w", c.configPath, err)
	}
	// Precedence: CLI flag > env var > config file > built-in default.
	// Env vars are presented as a first-class column in the flag table
	// (AFFENTCTL_*) — users who set them expect them to win over a
	// static config file. The old code only tracked CLI-explicit flags
	// (fs.Visit) and silently let the config override the env-derived
	// default for unset flags. Treat env-derived as "user-set" so the
	// config can only fill in things env didn't reach.
	setByCLIOrEnv := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { setByCLIOrEnv[f.Name] = true })
	for name, env := range configPrecedenceEnvSources() {
		if os.Getenv(env) != "" {
			setByCLIOrEnv[name] = true
		}
	}
	setByCLI := setByCLIOrEnv

	setString := func(name string, dst *string, v *string) {
		if v != nil && !setByCLI[name] {
			*dst = *v
		}
	}
	setBool := func(name string, dst *bool, v *bool) {
		if v != nil && !setByCLI[name] {
			*dst = *v
		}
	}
	setInt := func(name string, dst *int, v *int) {
		if v != nil && !setByCLI[name] {
			*dst = *v
		}
	}
	setDuration := func(name string, dst *time.Duration, v *string) error {
		if v == nil || setByCLI[name] {
			return nil
		}
		d, err := time.ParseDuration(*v)
		if err != nil {
			return fmt.Errorf("parse %s duration %q: %w", name, *v, err)
		}
		*dst = d
		return nil
	}

	setString("workspace", &c.workspace, cfg.Workspace)
	setString("base-url", &c.baseURL, cfg.BaseURL)
	setString("api-key", &c.apiKey, cfg.APIKey)
	setString("model", &c.model, cfg.Model)
	setInt("max-turns", &c.maxTurns, cfg.MaxTurns)
	if err := setDuration("max-call-timeout", &c.callTimeout, cfg.MaxCallTimeout); err != nil {
		return err
	}
	setInt("retry-transient", &c.retryTransient, cfg.RetryTransient)
	if err := setDuration("retry-backoff", &c.retryBackoff, cfg.RetryBackoff); err != nil {
		return err
	}
	setString("trace", &c.tracePath, cfg.Trace)
	setBool("trace-skip-deltas", &c.traceSkipDeltas, cfg.TraceSkipDeltas)
	setString("system-prompt", &c.systemPromptPath, cfg.SystemPrompt)
	setBool("quiet", &c.quiet, cfg.Quiet)
	setBool("eval-mode", &c.evalMode, cfg.EvalMode)
	setString("eval-tools", &c.evalTools, cfg.EvalTools)
	setBool("eval-all-tools", &c.evalAllTools, cfg.EvalAllTools)
	if cfg.Memory != nil {
		if cfg.Memory.Enabled != nil {
			c.memoryExplicit = true
		}
		setBool("memory", &c.memoryEnabled, cfg.Memory.Enabled)
		setBool("memory-only", &c.memoryOnly, cfg.Memory.Only)
		setString("memory-workspace-store", &c.memoryWorkspaceStore, cfg.Memory.WorkspaceStore)
		setString("memory-dir", &c.memoryDir, cfg.Memory.Dir)
		setString("memory-user-store", &c.memoryUserStore, cfg.Memory.UserStore)
		setString("memory-max-chars", &c.memoryMaxChars, cfg.Memory.MaxChars)
		setInt("memory-topic-max-chars", &c.memoryTopicMaxChars, cfg.Memory.TopicMaxChars)
		setInt("memory-max-topics", &c.memoryMaxTopics, cfg.Memory.MaxTopics)
	}
	setBool("project-context", &c.projectContext, cfg.ProjectContext)
	if cfg.Compact != nil {
		setInt("compact-trigger", &c.compactTrigger, cfg.Compact.Trigger)
		setInt("compact-keep-last", &c.compactKeepLast, cfg.Compact.KeepLast)
	}
	setString("session-id", &c.sessionID, cfg.SessionID)
	setBool("continue", &c.continueLast, cfg.Continue)
	setString("mcp-config", &c.mcpConfigPath, cfg.MCPConfig)
	setString("executor", &c.executor, cfg.Executor)
	setBool("subagent", &c.subagentEnabled, cfg.Subagent)
	setBool("subagent", &c.subagentEnabled, cfg.EnableSubagent)
	setInt("subagent-max-depth", &c.subagentMaxDepth, cfg.SubagentMaxDepth)
	setBool("focused-tasks", &c.focusedTasksEnabled, cfg.FocusedTasks)
	setBool("focused-tasks", &c.focusedTasksEnabled, cfg.EnableFocusedTasks)
	setBool("web", &c.webEnabled, cfg.Web)
	setBool("web", &c.webEnabled, cfg.EnableWeb)
	setBool("web-search", &c.webSearchEnabled, cfg.WebSearch)
	setBool("web-search", &c.webSearchEnabled, cfg.EnableWebSearch)
	setBool("browser", &c.browserEnabled, cfg.Browser)
	setBool("browser", &c.browserEnabled, cfg.EnableBrowser)
	setBool("browser-screenshot", &c.browserScreenshot, cfg.BrowserScreenshot)
	setString("temperature", &c.temperature, cfg.Temperature)
	setString("top-p", &c.topP, cfg.TopP)
	setString("max-tokens", &c.maxTokens, cfg.MaxTokens)
	setString("seed", &c.seed, cfg.Seed)
	return nil
}

func readConfigJSON(path string, dst any) error {
	data, err := readConfigFileLimited(path)
	if err != nil {
		return fmt.Errorf("read: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("parse: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("parse: multiple JSON values")
		}
		return fmt.Errorf("parse: %w", err)
	}
	return nil
}

func readConfigFileLimited(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxConfigInputBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxConfigInputBytes {
		return nil, fmt.Errorf("config exceeds %d-byte limit", maxConfigInputBytes)
	}
	return data, nil
}

func applyEnvConfig(c *commonFlags, fs *flag.FlagSet) error {
	setByCLI := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { setByCLI[f.Name] = true })

	setString := func(name, env string, dst *string) {
		if setByCLI[name] {
			return
		}
		if v := os.Getenv(env); v != "" {
			*dst = v
		}
	}
	setBoolStrict := func(name, env string, dst *bool) error {
		if setByCLI[name] {
			return nil
		}
		v := os.Getenv(env)
		if v == "" {
			return nil
		}
		parsed, err := strconv.ParseBool(v)
		if err != nil {
			return fmt.Errorf("%s=%q: %w", env, v, err)
		}
		*dst = parsed
		return nil
	}

	setString("base-url", "AFFENTCTL_BASE_URL", &c.baseURL)
	setString("api-key", "AFFENTCTL_API_KEY", &c.apiKey)
	setString("model", "AFFENTCTL_MODEL", &c.model)
	setString("mcp-config", "AFFENTCTL_MCP_CONFIG", &c.mcpConfigPath)
	setString("executor", "AFFENTCTL_EXECUTOR", &c.executor)
	setString("eval-tools", "AFFENTCTL_EVAL_TOOLS", &c.evalTools)
	setString("temperature", "AFFENTCTL_TEMPERATURE", &c.temperature)
	setString("top-p", "AFFENTCTL_TOP_P", &c.topP)
	setString("max-tokens", "AFFENTCTL_MAX_TOKENS", &c.maxTokens)
	setString("seed", "AFFENTCTL_SEED", &c.seed)
	setString("memory-max-chars", "AFFENTCTL_MEMORY_MAX_CHARS", &c.memoryMaxChars)
	if err := setBoolStrict("subagent", "AFFENTCTL_SUBAGENT", &c.subagentEnabled); err != nil {
		return err
	}
	if err := setBoolStrict("focused-tasks", "AFFENTCTL_FOCUSED_TASKS", &c.focusedTasksEnabled); err != nil {
		return err
	}
	if err := setBoolStrict("web", "AFFENTCTL_WEB", &c.webEnabled); err != nil {
		return err
	}
	if err := setBoolStrict("web-search", "AFFENTCTL_WEB_SEARCH", &c.webSearchEnabled); err != nil {
		return err
	}
	if err := setBoolStrict("browser", "AFFENTCTL_BROWSER", &c.browserEnabled); err != nil {
		return err
	}
	if err := setBoolStrict("browser-screenshot", "AFFENTCTL_BROWSER_SCREENSHOT", &c.browserScreenshot); err != nil {
		return err
	}
	if err := setBoolStrict("eval-mode", "AFFENTCTL_EVAL_MODE", &c.evalMode); err != nil {
		return err
	}
	if err := setBoolStrict("eval-all-tools", "AFFENTCTL_EVAL_ALL_TOOLS", &c.evalAllTools); err != nil {
		return err
	}
	if err := setBoolStrict("memory", "AFFENTCTL_MEMORY", &c.memoryEnabled); err != nil {
		return err
	}
	if err := setBoolStrict("memory-only", "AFFENTCTL_MEMORY_ONLY", &c.memoryOnly); err != nil {
		return err
	}
	if err := setBoolStrict("project-context", "AFFENTCTL_PROJECT_CONTEXT", &c.projectContext); err != nil {
		return err
	}
	setString("workspace", "AFFENTCTL_WORKSPACE", &c.workspace)
	setInt := func(name, env string, dst *int) error {
		if setByCLI[name] {
			return nil
		}
		v := os.Getenv(env)
		if v == "" {
			return nil
		}
		parsed, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("%s=%q: %w", env, v, err)
		}
		*dst = parsed
		return nil
	}
	if err := setInt("subagent-max-depth", "AFFENTCTL_SUBAGENT_MAX_DEPTH", &c.subagentMaxDepth); err != nil {
		return err
	}
	if err := setInt("max-turns", "AFFENTCTL_MAX_TURNS", &c.maxTurns); err != nil {
		return err
	}
	if err := setInt("retry-transient", "AFFENTCTL_RETRY_TRANSIENT", &c.retryTransient); err != nil {
		return err
	}
	if err := setInt("memory-topic-max-chars", "AFFENTCTL_MEMORY_TOPIC_MAX_CHARS", &c.memoryTopicMaxChars); err != nil {
		return err
	}
	if err := setInt("memory-max-topics", "AFFENTCTL_MEMORY_MAX_TOPICS", &c.memoryMaxTopics); err != nil {
		return err
	}
	if err := setInt("compact-trigger", "AFFENTCTL_COMPACT_TRIGGER", &c.compactTrigger); err != nil {
		return err
	}
	if err := setInt("compact-keep-last", "AFFENTCTL_COMPACT_KEEP_LAST", &c.compactKeepLast); err != nil {
		return err
	}
	setDuration := func(name, env string, dst *time.Duration) error {
		if setByCLI[name] {
			return nil
		}
		v := os.Getenv(env)
		if v == "" {
			return nil
		}
		parsed, err := time.ParseDuration(v)
		if err != nil {
			return fmt.Errorf("%s=%q: %w", env, v, err)
		}
		*dst = parsed
		return nil
	}
	if err := setDuration("max-call-timeout", "AFFENTCTL_MAX_CALL_TIMEOUT", &c.callTimeout); err != nil {
		return err
	}
	if err := setDuration("retry-backoff", "AFFENTCTL_RETRY_BACKOFF", &c.retryBackoff); err != nil {
		return err
	}
	return nil
}

// loopBundle is everything a subcommand needs after setup: the loop
// (already system-primed), its events channel, the trace recorder, the
// resolved session id, MCP clients to keep alive, and a closer to call
// before exit.
type loopBundle struct {
	loop       *agent.Loop
	events     chan sse.Event
	recorder   *eventlog.Recorder
	traceClose func() error
	browser    *affentbrowser.Session
	sessionID  string
	resumed    bool // true if we loaded an existing conversation
	workspace  string
	log        zerolog.Logger

	// turnsSeen / inputTokens / outputTokens accumulate across every
	// drain*() pass for this REPL session. drainInteractive bumps them
	// on TypeUsage / TypeTurnEnd; /usage in handleSlash reports the
	// running totals. drainBatch (one-shot run) doesn't touch them
	// since it exits after a single turn.
	turnsSeen    int
	inputTokens  int
	outputTokens int

	mcpClients []*mcp.Client
}

type runtimeCapabilities struct {
	Builtins             bool
	Memory               bool
	MCP                  bool
	Skill                bool
	BuiltinSkillProvider bool
	Plan                 bool
	SessionSearch        bool
	ProjectContext       bool
	SymbolContext        bool
	RepoSearch           bool
	WebFetch             bool
	WebSearch            bool
	Browser              bool
	BrowserScreenshot    bool
	Subagent             bool
	FocusedTasks         bool
}

func resolveRuntimeCapabilities(c commonFlags) runtimeCapabilities {
	if c.memoryOnly {
		return runtimeCapabilities{Memory: true}
	}
	defaultCaps := runtimeCapabilities{
		Builtins:             true,
		Memory:               c.memoryEnabled,
		MCP:                  strings.TrimSpace(c.mcpConfigPath) != "",
		Skill:                true,
		BuiltinSkillProvider: true,
		Plan:                 true,
		SessionSearch:        true,
		ProjectContext:       c.projectContext,
		SymbolContext:        true,
		RepoSearch:           true,
		WebFetch:             c.webEnabled,
		WebSearch:            c.webEnabled && c.webSearchEnabled,
		Browser:              c.browserEnabled,
		BrowserScreenshot:    c.browserEnabled && c.browserScreenshot,
		Subagent:             c.subagentEnabled,
		FocusedTasks:         c.focusedTasksEnabled,
	}
	if c.evalMode && c.evalAllTools {
		defaultCaps.Builtins = true
		defaultCaps.Memory = true
		defaultCaps.Skill = true
		defaultCaps.BuiltinSkillProvider = true
		defaultCaps.Plan = true
		defaultCaps.SessionSearch = true
		defaultCaps.SymbolContext = true
		defaultCaps.RepoSearch = true
		defaultCaps.WebFetch = true
		defaultCaps.WebSearch = true
		defaultCaps.Browser = true
		defaultCaps.BrowserScreenshot = true
		defaultCaps.Subagent = true
		defaultCaps.FocusedTasks = true
		defaultCaps.ProjectContext = false
		defaultCaps.MCP = strings.TrimSpace(c.mcpConfigPath) != ""
		return defaultCaps
	}
	caps := runtimeCapabilities{
		Builtins:             defaultCaps.Builtins,
		Memory:               defaultCaps.Memory,
		MCP:                  defaultCaps.MCP,
		Skill:                defaultCaps.Skill,
		BuiltinSkillProvider: defaultCaps.BuiltinSkillProvider,
		Plan:                 defaultCaps.Plan,
		SessionSearch:        defaultCaps.SessionSearch,
		ProjectContext:       defaultCaps.ProjectContext,
		SymbolContext:        defaultCaps.SymbolContext,
		RepoSearch:           defaultCaps.RepoSearch,
		WebFetch:             defaultCaps.WebFetch,
		WebSearch:            defaultCaps.WebSearch,
		Browser:              defaultCaps.Browser,
		BrowserScreenshot:    defaultCaps.BrowserScreenshot,
		Subagent:             defaultCaps.Subagent,
		FocusedTasks:         defaultCaps.FocusedTasks,
	}
	if c.evalMode {
		allowed, _ := evalToolAllowlist(c)
		caps.Builtins = evalAllowlistHasBuiltin(allowed)
		caps.Memory = (c.memoryExplicit && c.memoryEnabled) || allowed[agent.MemoryToolName]
		caps.MCP = strings.TrimSpace(c.mcpConfigPath) != "" && (strings.TrimSpace(c.evalTools) == "" || allowed["mcp"] || evalAllowlistHasUnknown(allowed))
		caps.Skill = false
		caps.BuiltinSkillProvider = false
		if allowed[agent.SkillToolName] {
			caps.Skill = true
			caps.BuiltinSkillProvider = true
			caps.Builtins = true
		}
		caps.Plan = false
		if allowed[agent.PlanToolName] {
			caps.Plan = true
			caps.Builtins = true
		}
		caps.SessionSearch = false
		if allowed[agent.SessionSearchToolName] {
			caps.SessionSearch = true
		}
		caps.ProjectContext = false
		caps.SymbolContext = allowed[agent.SymbolContextToolName]
		caps.RepoSearch = allowed["repo_search"]
		caps.WebFetch = c.webEnabled
		if allowed["web_fetch"] || allowed["web_search"] {
			caps.WebFetch = true
		}
		caps.WebSearch = (c.webEnabled && c.webSearchEnabled) || allowed["web_search"]
		caps.Browser = c.browserEnabled
		if evalAllowlistHasBrowser(allowed) {
			caps.Browser = true
		}
		caps.BrowserScreenshot = (c.browserEnabled && c.browserScreenshot) || allowed["browser_screenshot"]
		caps.Subagent = false
		if allowed[agent.SubagentToolName] {
			caps.Subagent = true
		}
		caps.FocusedTasks = false
		if allowed[agent.FocusedTaskToolName] {
			caps.FocusedTasks = true
		}
	}
	return caps
}

func evalToolAllowlist(c commonFlags) (map[string]bool, []string) {
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
	for _, raw := range strings.FieldsFunc(c.evalTools, func(r rune) bool {
		return r == ',' || r == ';' || r == ' ' || r == '\n' || r == '\t'
	}) {
		switch name := strings.TrimSpace(raw); name {
		case "", "none":
			continue
		case "all":
			for _, n := range evalKnownToolNames() {
				if n == "mcp" && strings.TrimSpace(c.mcpConfigPath) == "" {
					continue
				}
				add(n)
			}
		case "workspace":
			for _, n := range evalWorkspaceToolNames() {
				add(n)
			}
		case "readonly_workspace":
			for _, n := range evalReadonlyWorkspaceToolNames() {
				add(n)
			}
		case "web":
			add("web_fetch")
			add("web_search")
		case "browser":
			for _, n := range evalBrowserToolNames() {
				add(n)
			}
		case "recall":
			add(agent.MemoryToolName)
			add(agent.SessionSearchToolName)
		case "delegation":
			add(agent.SubagentToolName)
			add(agent.FocusedTaskToolName)
		case "mcp":
			add("mcp")
		default:
			add(name)
		}
	}
	return allowed, requested
}

func evalWorkspaceToolNames() []string {
	return []string{"shell", "read_file", "file_context", "write_file", "edit_file", "list_files", agent.SymbolContextToolName, "repo_search"}
}

func evalReadonlyWorkspaceToolNames() []string {
	return []string{"read_file", "file_context", "list_files", agent.SymbolContextToolName, "repo_search"}
}

func evalBrowserToolNames() []string {
	return []string{"browser_navigate", "browser_snapshot", "browser_find", "browser_network", "browser_network_read", "browser_click", "browser_scroll", "browser_type", "browser_wait", "browser_screenshot"}
}

func evalKnownToolNames() []string {
	out := append([]string{}, evalWorkspaceToolNames()...)
	out = append(out, agent.SkillToolName, agent.MemoryToolName, agent.SessionSearchToolName, agent.PlanToolName, agent.SubagentToolName, agent.FocusedTaskToolName, "web_fetch", "web_search", "mcp")
	out = append(out, evalBrowserToolNames()...)
	return out
}

func evalKnownToolSet() map[string]bool {
	out := map[string]bool{}
	for _, name := range evalKnownToolNames() {
		out[name] = true
	}
	return out
}

func evalAllowlistHasUnknown(allowed map[string]bool) bool {
	known := evalKnownToolSet()
	for name := range allowed {
		if !known[name] {
			return true
		}
	}
	return false
}

func evalAllowlistHasBuiltin(allowed map[string]bool) bool {
	for _, name := range append(evalWorkspaceToolNames(), agent.SkillToolName, agent.PlanToolName) {
		if allowed[name] {
			return true
		}
	}
	return false
}

func evalAllowlistHasBrowser(allowed map[string]bool) bool {
	for _, name := range evalBrowserToolNames() {
		if allowed[name] {
			return true
		}
	}
	return false
}

func filterEvalModeTools(reg *agent.Registry, c commonFlags, caps runtimeCapabilities) error {
	if reg == nil {
		return nil
	}
	allowed, requested := evalToolAllowlist(c)
	if caps.Memory {
		allowed[agent.MemoryToolName] = true
	}
	if c.webEnabled {
		allowed["web_fetch"] = true
	}
	if c.webEnabled && c.webSearchEnabled {
		allowed["web_search"] = true
	}
	if c.browserEnabled {
		for _, name := range evalBrowserToolNames() {
			if name == "browser_screenshot" && !caps.BrowserScreenshot {
				continue
			}
			allowed[name] = true
		}
	}
	if caps.BrowserScreenshot {
		allowed["browser_screenshot"] = true
	}
	if caps.MCP && strings.TrimSpace(c.evalTools) == "" {
		allowed["mcp"] = true
	}
	if caps.MCP && allowed["mcp"] {
		for _, entry := range reg.Catalog() {
			if strings.TrimSpace(entry.Source) != "" {
				allowed[entry.Name] = true
			}
		}
	}
	for _, def := range reg.Defs() {
		if !allowed[def.Function.Name] {
			reg.Remove(def.Function.Name)
		}
	}
	var missing []string
	for _, name := range requested {
		if name == "mcp" {
			if !caps.MCP {
				missing = append(missing, "mcp (requires --mcp-config)")
			}
			continue
		}
		if _, ok := reg.Get(name); !ok {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf("--eval-tools requested unavailable tool(s): %s", strings.Join(missing, ", "))
	}
	return nil
}

func registryHasWorkspaceTool(reg *agent.Registry) bool {
	if reg == nil {
		return false
	}
	for _, name := range evalWorkspaceToolNames() {
		if _, ok := reg.Get(name); ok {
			return true
		}
	}
	return false
}

func (b *loopBundle) close() {
	if b.browser != nil {
		_ = b.browser.Close()
	}
	for _, c := range b.mcpClients {
		_ = c.Close()
	}
	if b.traceClose != nil {
		_ = b.traceClose()
	}
}

func registerAffentctlWebTools(reg *agent.Registry, includeSearch bool) error {
	if includeSearch {
		return affentweb.RegisterAll(reg, affentweb.Options{Fetch: affentweb.FetchConfig{}})
	}
	affentweb.RegisterFetch(reg, affentweb.FetchConfig{})
	return nil
}

func registerAffentctlWebToolsWithBrowser(reg *agent.Registry, includeSearch bool, browser *affentbrowser.Session) error {
	fetchCfg := affentweb.FetchConfig{RenderedFallback: affentctlBrowserRenderedFallback(browser)}
	if includeSearch {
		return affentweb.RegisterAll(reg, affentweb.Options{Fetch: fetchCfg})
	}
	affentweb.RegisterFetch(reg, fetchCfg)
	return nil
}

func affentctlBrowserRenderedFallback(browser *affentbrowser.Session) affentweb.RenderedFallbackFunc {
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

func newAffentctlBrowserSession(workspace string) (*affentbrowser.Session, error) {
	return affentbrowser.NewSession(affentbrowser.SessionConfig{
		NoSandbox:    true,
		WorkspaceDir: workspace,
	})
}

func affentctlFocusedTaskWebRegistrar(enabled, includeSearch bool) func(context.Context, *agent.Registry) (func(), error) {
	if !enabled {
		return nil
	}
	return func(_ context.Context, reg *agent.Registry) (func(), error) {
		if err := registerAffentctlWebTools(reg, includeSearch); err != nil {
			return nil, fmt.Errorf("focused task web_search: %w", err)
		}
		return nil, nil
	}
}

func affentctlFocusedTaskBrowserRegistrar(enabled, includeScreenshot bool, workspace string, webEnabled, webSearch bool) func(context.Context, *agent.Registry) (func(), error) {
	if !enabled {
		return nil
	}
	return func(_ context.Context, reg *agent.Registry) (func(), error) {
		browser, err := newAffentctlBrowserSession(workspace)
		if err != nil {
			return nil, err
		}
		affentbrowser.RegisterAll(reg, browser, affentbrowser.Options{
			IncludeScreenshot: includeScreenshot,
		})
		if webEnabled {
			reg.Remove("web_fetch")
			if err := registerAffentctlWebToolsWithBrowser(reg, webSearch, browser); err != nil {
				_ = browser.Close()
				return nil, err
			}
		}
		return func() { _ = browser.Close() }, nil
	}
}

func affentctlSubagentBrowserRegistrar(enabled bool, workspace string) func(context.Context, *agent.Registry) (func(), error) {
	if !enabled {
		return nil
	}
	return func(_ context.Context, reg *agent.Registry) (func(), error) {
		browser, err := newAffentctlBrowserSession(workspace)
		if err != nil {
			return nil, err
		}
		affentbrowser.RegisterAll(reg, browser, affentbrowser.Options{})
		return func() { _ = browser.Close() }, nil
	}
}

// setupLoop builds the loop + tools, resolves the session id (new vs.
// resume), opens the trace writer, seeds the system prompt if needed.
// Errors are returned as plain CLI exit codes via the second return.
func setupLoop(c commonFlags) (*loopBundle, int) {
	logLevel := zerolog.InfoLevel
	if c.quiet {
		logLevel = zerolog.ErrorLevel
	}
	log := zerolog.New(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.Kitchen}).
		Level(logLevel).
		With().Timestamp().Logger()

	if err := validateLLMConfig(c); err != nil {
		log.Error().Err(err).Msg("llm config")
		return nil, exitUsage
	}
	caps := resolveRuntimeCapabilities(c)

	workspace, err := filepath.Abs(c.workspace)
	if err != nil {
		log.Error().Err(err).Msg("resolve workspace")
		return nil, exitRuntime
	}
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		log.Error().Err(err).Msg("mkdir workspace")
		return nil, exitRuntime
	}

	convDir := filepath.Join(workspace, ".affentctl")
	if err := os.MkdirAll(convDir, 0o755); err != nil {
		log.Error().Err(err).Msg("mkdir conv dir")
		return nil, exitRuntime
	}

	sid, resumed, err := resolveSessionID(convDir, c.sessionID, c.continueLast)
	if err != nil {
		log.Error().Err(err).Msg("resolve session id")
		return nil, exitRuntime
	}

	systemPrompt, code := resolveSystemPrompt(c, workspace, caps)
	if code != 0 {
		log.Error().Msg("system-prompt")
		return nil, code
	}

	// Deferred cleanup: any resource opened below registers a closer.
	// On success, closers is transferred to the bundle; on error, the
	// defer tears everything down so early-return paths don't leak.
	var closers []func()
	success := false
	defer func() {
		if !success {
			for i := len(closers) - 1; i >= 0; i-- {
				closers[i]()
			}
		}
	}()

	traceWriter, traceClose, err := openTrace(c.tracePath, resumed)
	if err != nil {
		log.Error().Err(err).Msg("open trace")
		return nil, exitRuntime
	}
	closers = append(closers, func() { _ = traceClose() })

	if c.memoryOnly && c.mcpConfigPath != "" {
		log.Error().Msg("--memory-only cannot be combined with --mcp-config; --memory-only exposes only the memory tool")
		return nil, exitUsage
	}

	memoryConfig := c
	memoryConfig.memoryEnabled = caps.Memory
	memStore, code := setupMemoryStore(memoryConfig, workspace, log)
	if code != 0 {
		return nil, code
	}

	tools := agent.NewRegistry()
	var execBackend executor.Executor
	var skillReg *agent.SkillRegistry
	var conv *agent.Conversation
	var browser *affentbrowser.Session
	planPath := ""
	if caps.Builtins {
		var execErr error
		executorSpec := c.executor
		executorSpec, execErr = maybeStartSandboxExecutor(executorSpec, workspace, osCommandRunner{buildTimeout: sandboxDockerBuildTimeout, stdout: os.Stdout, stderr: os.Stderr, streamBuild: true})
		if execErr != nil {
			log.Error().Err(execErr).Msg("sandbox")
			return nil, exitRuntime
		}
		execBackend, execErr = buildExecutor(executorSpec, sid, workspace)
		if execErr != nil {
			log.Error().Err(execErr).Msg("executor")
			return nil, exitUsage
		}
		skillDir := ""
		if caps.Skill {
			skillDir = agent.DefaultWorkspaceSkillDir(workspace)
			var skillErr error
			skillReg, skillErr = agent.RuntimeSkillRegistry(skillDir)
			if skillErr != nil {
				log.Error().Err(skillErr).Msg("skills")
				return nil, exitRuntime
			}
		}
		if caps.Plan {
			planPath = filepath.Join(convDir, sid+".plan.json")
		}
		sessionsDir := convDir
		if !caps.SessionSearch {
			sessionsDir = ""
		}
		agent.RegisterBuiltins(tools, agent.BuiltinDeps{
			Executor:         execBackend,
			HostWorkspaceDir: workspace,
			Memory:           memStore,
			SessionsDir:      sessionsDir,
			SessionID:        sid,
			PlanPath:         planPath,
			SkillRegistry:    skillReg,
			SkillDir:         skillDir,
			SkillInstallConfirmer: func(proposalID string) bool {
				return agent.UserConfirmedRuntimeSkillProposal(conv, proposalID)
			},
			DisableSkill: !caps.Skill,
		})
	} else if caps.Memory {
		if memStore == nil {
			log.Error().Msg("memory tool requires a usable memory store; check --memory-workspace-store / --memory-user-store")
			return nil, exitRuntime
		}
		agent.RegisterMemoryOnly(tools, memStore)
	}
	if caps.SessionSearch {
		if _, ok := tools.Get(agent.SessionSearchToolName); !ok {
			agent.RegisterSessionSearchOnly(tools, convDir, sid)
		}
	}
	if caps.WebFetch {
		if caps.Browser {
			var browserErr error
			browser, browserErr = newAffentctlBrowserSession(workspace)
			if browserErr != nil {
				log.Error().Err(browserErr).Msg("browser setup")
				return nil, exitRuntime
			}
			closers = append(closers, func() { _ = browser.Close() })
			affentbrowser.RegisterAll(tools, browser, affentbrowser.Options{
				IncludeScreenshot: caps.BrowserScreenshot,
			})
		}
		if err := registerAffentctlWebToolsWithBrowser(tools, caps.WebSearch, browser); err != nil {
			log.Error().Err(err).Msg("web setup")
			return nil, exitRuntime
		}
	} else if caps.Browser {
		var browserErr error
		browser, browserErr = newAffentctlBrowserSession(workspace)
		if browserErr != nil {
			log.Error().Err(browserErr).Msg("browser setup")
			return nil, exitRuntime
		}
		closers = append(closers, func() { _ = browser.Close() })
		affentbrowser.RegisterAll(tools, browser, affentbrowser.Options{
			IncludeScreenshot: caps.BrowserScreenshot,
		})
	}

	mcpConfigPath := ""
	if caps.MCP {
		mcpConfigPath = c.mcpConfigPath
	}
	mcpClients, err := startMCP(mcpConfigPath, tools, log)
	if err != nil {
		log.Error().Err(err).Msg("mcp setup")
		return nil, exitRuntime
	}
	for _, mc := range mcpClients {
		mc := mc
		closers = append(closers, func() { _ = mc.Close() })
	}
	conv, err = agent.OpenConversationAt(filepath.Join(convDir, sid+".jsonl"))
	if err != nil {
		log.Error().Err(err).Msg("conversation")
		return nil, exitRuntime
	}

	events := make(chan sse.Event, 64)
	llm := agent.NewLLMClient(c.baseURL, c.apiKey, c.model)
	if sampling, err := parseSampling(c.temperature, c.topP, c.maxTokens, c.seed); err != nil {
		log.Error().Err(err).Msg("parse sampling")
		return nil, exitRuntime
	} else {
		llm.Sampling = sampling
	}
	projectContextDir := ""
	if caps.ProjectContext {
		projectContextDir = workspace
	}
	if caps.Subagent {
		agent.RegisterSubagent(tools, agent.SubagentDeps{
			LLM:                llm,
			Executor:           execBackend,
			HostWorkspaceDir:   workspace,
			Memory:             memStore,
			SessionsDir:        convDir,
			ParentSessionID:    sid,
			TranscriptDir:      filepath.Join(convDir, "subagents", sid),
			ProjectContextDir:  projectContextDir,
			RegisterChildTools: affentctlSubagentBrowserRegistrar(caps.Browser, workspace),
			Log:                log,
			PerCallTimeout:     c.callTimeout,
			MaxDepth:           c.subagentMaxDepth,
		})
	}
	if caps.FocusedTasks {
		// RegisterFocusedTasks itself filters out profiles whose deps
		// aren't satisfied — e.g., `research` is dropped because
		// affentctl doesn't wire web/browser registrars by default. If every
		// profile gets filtered out, the function is a no-op and the
		// system-prompt fragment is still appended so the model knows
		// run_task isn't available; we sniff registration to keep the
		// prompt and the schema consistent.
		agent.RegisterFocusedTasks(tools, agent.FocusedTaskDeps{
			LLM:                  llm,
			Executor:             execBackend,
			HostWorkspaceDir:     workspace,
			Memory:               memStore,
			SessionsDir:          convDir,
			ParentSessionID:      sid,
			TranscriptDir:        filepath.Join(convDir, "focused-tasks", sid),
			ProjectContextDir:    projectContextDir,
			Log:                  log,
			PerCallTimeout:       c.callTimeout,
			RegisterWebTools:     affentctlFocusedTaskWebRegistrar(caps.WebFetch, caps.WebSearch),
			RegisterBrowserTools: affentctlFocusedTaskBrowserRegistrar(caps.Browser, false, workspace, caps.WebFetch, caps.WebSearch),
		})
	}
	if c.evalMode && !c.evalAllTools {
		if err := filterEvalModeTools(tools, c, caps); err != nil {
			log.Error().Err(err).Msg("eval tools")
			return nil, exitUsage
		}
	}
	if c.evalMode && c.systemPromptPath == "" {
		systemPrompt = agent.BaseSystemPromptForRegistry(tools)
		if registryHasWorkspaceTool(tools) && workspace != "/workspace" {
			systemPrompt += "\n\nYour workspace directory is \"" + workspace +
				"\". Use this exact path (or a relative path inside it) with registered workspace tools."
		}
	}
	systemPrompt = agent.WithRegistrySystemGuidance(systemPrompt, tools)
	systemPrompt = agent.WithRuntimeContextSystemGuidance(systemPrompt, time.Now())
	loop := &agent.Loop{
		LLM:                    llm,
		Tools:                  tools,
		Conv:                   conv,
		Events:                 events,
		Log:                    log,
		MaxTurnSteps:           c.maxTurns,
		FinalNoToolsOnMaxTurns: true,
		PerCallTimeout:         c.callTimeout,
		MaxTransientRetries:    c.retryTransient,
		TransientBackoff:       c.retryBackoff,
		ToolResultArtifactDir: filepath.Join(
			workspace,
			".affent",
			"artifacts",
			"tool-results",
		),
		ToolResultArtifactPathPrefix: ".affent/artifacts/tool-results",
		Memory:                       memStore,
		ProjectContextDir:            projectContextDir,
	}
	if caps.BuiltinSkillProvider {
		loop.SkillProvider = agent.SkillProviderForTools(nil, tools)
	}
	if skillReg != nil {
		loop.SkillProvider = agent.SkillProviderForTools(skillReg, tools)
	}
	if planPath != "" {
		loop.SkillProvider = agent.WithActivePlanSkillProvider(planPath, loop.SkillProvider)
	}
	if caps.Subagent {
		loop.FirstToolPolicy = agent.SubagentFirstToolPolicy()
		loop.PostToolPolicy = agent.SubagentPostToolPolicy()
	}
	if caps.FocusedTasks {
		if _, ok := tools.Get(agent.FocusedTaskToolName); ok {
			loop.FirstToolPolicies = append(loop.FirstToolPolicies, agent.FocusedTaskFirstToolPolicy())
			loop.PostToolPolicies = append(loop.PostToolPolicies, agent.FocusedTaskPostToolPolicy())
		}
	}
	triggerMsgs, keepLast := resolveCompactionConfig(c.compactTrigger, c.compactKeepLast)
	loop.Compactor = &agent.LLMSummaryCompactor{
		LLM:         llm,
		TriggerMsgs: triggerMsgs,
		KeepLast:    keepLast,
	}
	if err := loop.EnsureSystemPrompt(systemPrompt); err != nil {
		log.Error().Err(err).Msg("seed system prompt")
		return nil, exitRuntime
	}

	success = true
	return &loopBundle{
		loop:       loop,
		events:     events,
		recorder:   eventlog.NewRecorder(traceWriter, eventlog.Options{SkipDeltas: c.traceSkipDeltas}),
		traceClose: traceClose,
		browser:    browser,
		sessionID:  sid,
		resumed:    resumed,
		workspace:  workspace,
		log:        log,
		mcpClients: mcpClients,
	}, 0
}

func resolveSystemPrompt(c commonFlags, workspace string, caps runtimeCapabilities) (string, int) {
	systemPrompt := agent.BaseSystemPromptForSurface(agent.SystemPromptSurface{
		Builtins:   caps.Builtins,
		Memory:     caps.Memory,
		OtherTools: caps.MCP || caps.Subagent || caps.FocusedTasks || caps.Plan || caps.SessionSearch || caps.Skill || caps.WebFetch || caps.WebSearch || caps.Browser,
	})
	if c.systemPromptPath != "" {
		raw, err := readMaybeStdin(c.systemPromptPath)
		if err != nil {
			return "", exitRuntime
		}
		systemPrompt = raw
	} else if caps.Builtins && workspace != "/workspace" {
		systemPrompt += "\n\nYour workspace directory is \"" + workspace +
			"\". Use this exact path (or a relative path inside it) with the file tools."
	}
	return systemPrompt, 0
}

func setupMemoryStore(c commonFlags, workspace string, log zerolog.Logger) (memory.MemoryStore, int) {
	if !c.memoryEnabled {
		return nil, 0
	}
	fs := memory.NewFileMemoryStore(workspace)
	if c.memoryDir != "" {
		fs.MemoryDir = resolveStorePath(workspace, c.memoryDir)
	}
	if c.memoryWorkspaceStore != "" {
		fs.MemoryPath = resolveStorePath(workspace, c.memoryWorkspaceStore)
	}
	if c.memoryUserStore != "" {
		fs.UserPath = resolveStorePath(workspace, c.memoryUserStore)
	}
	if memCap, userCap, ok, perr := parseMemoryMaxChars(c.memoryMaxChars); perr != nil {
		log.Error().Err(perr).Msg("parse --memory-max-chars")
		return nil, exitUsage
	} else if ok {
		fs.CoreCharLimit = memCap
		fs.UserCharLimit = userCap
	}
	if c.memoryTopicMaxChars > 0 {
		fs.TopicCharLimit = c.memoryTopicMaxChars
	}
	if c.memoryMaxTopics > 0 {
		fs.MaxTopics = c.memoryMaxTopics
	}
	return fs, 0
}

func resolveCompactionConfig(trigger, keepLast int) (int, int) {
	if trigger <= 0 {
		trigger = agent.DefaultSummaryTriggerMsgs
	}
	if keepLast <= 0 {
		keepLast = agent.DefaultSummaryKeepLast
	}
	return trigger, keepLast
}

// buildExecutor parses the resolved --executor spec and returns the
// matching affent executor. "local" (or empty) → LocalExecutor;
// "docker:<cid>" → DockerExecExecutor pointed at the named container.
// The user-facing "sandbox" alias is expanded before this function.
// Unknown specs are a hard error so typos don't silently fall back.
func buildExecutor(spec, sessionID, workspace string) (executor.Executor, error) {
	switch {
	case spec == "" || spec == "local":
		return executor.NewLocalExecutor(sessionID, workspace), nil
	case strings.HasPrefix(spec, "docker:"):
		cid := strings.TrimPrefix(spec, "docker:")
		if cid == "" {
			return nil, fmt.Errorf("--executor docker: requires a container id (e.g. docker:abc123)")
		}
		if err := validateDockerContainerName("--executor docker", cid); err != nil {
			return nil, err
		}
		return executor.NewDockerExecExecutor(sessionID, cid).WithDefaultCwd(workspace), nil
	default:
		return nil, fmt.Errorf("unknown --executor %q (valid: local, sandbox, docker:<container_id>)", spec)
	}
}

// mcpConfig is the on-disk shape for --mcp-config. Compatible with the
// "servers" array shape used by Claude Desktop / Goose configs (just
// our flat field names).
type mcpConfig struct {
	Servers []mcpConfigServer `json:"servers"`
}

type mcpConfigServer struct {
	Name        string            `json:"name"`
	Namespace   *bool             `json:"namespace,omitempty"`
	Command     string            `json:"command,omitempty"`
	Args        []string          `json:"args,omitempty"`
	Env         []string          `json:"env,omitempty"`
	Cwd         string            `json:"cwd,omitempty"`
	URL         string            `json:"url,omitempty"`
	Headers     map[string]string `json:"headers,omitempty"`
	InitTimeout string            `json:"init_timeout,omitempty"`
	AllowTools  []string          `json:"allow_tools,omitempty"`
	DenyTools   []string          `json:"deny_tools,omitempty"`
}

func (s mcpConfigServer) serverSpec() (mcp.ServerSpec, error) {
	spec := mcp.ServerSpec{
		Name:          s.Name,
		Namespace:     s.Namespace,
		Command:       s.Command,
		Args:          s.Args,
		Env:           s.Env,
		Cwd:           s.Cwd,
		URL:           s.URL,
		Headers:       s.Headers,
		ToolAllowlist: append([]string{}, s.AllowTools...),
		ToolDenylist:  append([]string{}, s.DenyTools...),
	}
	if strings.TrimSpace(s.InitTimeout) != "" {
		d, err := time.ParseDuration(s.InitTimeout)
		if err != nil {
			return spec, fmt.Errorf("init_timeout=%q: %w", s.InitTimeout, err)
		}
		if d <= 0 {
			return spec, fmt.Errorf("init_timeout=%q must be positive", s.InitTimeout)
		}
		spec.InitTimeout = d
	}
	return spec, nil
}

func startMCP(configPath string, reg *agent.Registry, log zerolog.Logger) ([]*mcp.Client, error) {
	if configPath == "" {
		return nil, nil
	}
	var cfg mcpConfig
	if err := readConfigJSON(configPath, &cfg); err != nil {
		return nil, fmt.Errorf("load mcp config %s: %w", configPath, err)
	}
	if len(cfg.Servers) == 0 {
		return nil, nil
	}
	specs := make([]mcp.ServerSpec, 0, len(cfg.Servers))
	for i, server := range cfg.Servers {
		spec, err := server.serverSpec()
		if err != nil {
			return nil, fmt.Errorf("parse mcp config servers[%d]: %w", i, err)
		}
		specs = append(specs, spec)
	}
	// Bound the launch + handshake total. Each Start has its own
	// initialize timeout; the outer budget is derived from the same
	// per-server values so a configured init_timeout is not cut off by
	// a stale fixed wrapper.
	ctx, cancel := context.WithTimeout(context.Background(), mcpStartupTimeout(specs))
	defer cancel()
	return mcp.RegisterAll(ctx, reg, specs, log)
}

func mcpStartupTimeout(specs []mcp.ServerSpec) time.Duration {
	if len(specs) == 0 {
		return minMCPStartupTimeout
	}
	total := time.Duration(0)
	for _, spec := range specs {
		initTimeout := spec.InitTimeout
		if initTimeout <= 0 {
			initTimeout = mcp.DefaultInitTimeout
		}
		total += initTimeout + mcpStartupPerServerOverrun
	}
	if total < minMCPStartupTimeout {
		return minMCPStartupTimeout
	}
	return total
}

// resolveSessionID picks the session id and tells the caller whether
// we're resuming. Precedence: explicit --session-id > --continue > new.
func resolveSessionID(convDir, explicit string, continueLast bool) (string, bool, error) {
	if explicit != "" {
		// Caller-named session. May or may not exist yet; either way we
		// reuse the id and Conversation.load handles the "no file" case.
		path := filepath.Join(convDir, explicit+".jsonl")
		if _, err := os.Stat(path); err == nil {
			return explicit, true, nil
		}
		return explicit, false, nil
	}
	if continueLast {
		sid, err := mostRecentSession(convDir)
		if err != nil {
			return "", false, err
		}
		if sid == "" {
			return "", false, errors.New("--continue requested but no prior session found under " + convDir)
		}
		return sid, true, nil
	}
	return "run_" + uuid.NewString(), false, nil
}

func mostRecentSession(convDir string) (string, error) {
	dir, err := os.Open(convDir)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	defer dir.Close()

	var latestID string
	var latestMod time.Time
	for {
		entries, rerr := dir.ReadDir(localSessionDirReadBatch)
		if rerr != nil && !errors.Is(rerr, io.EOF) {
			return "", rerr
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			if latestID == "" || info.ModTime().After(latestMod) {
				latestID = strings.TrimSuffix(e.Name(), ".jsonl")
				latestMod = info.ModTime()
			}
		}
		if errors.Is(rerr, io.EOF) {
			break
		}
	}
	return latestID, nil
}

// readMaybeStdin handles "-", a file path that exists, or a literal
// string. Used for --prompt and --system-prompt flags.
//
// Behavior:
//   - "" → ""
//   - "-" → read stdin
//   - "@path" → MUST be a readable file; missing/unreadable is a hard
//     error. Pre-fix this silently fell through to the literal path
//     so `--prompt @/typoed/path.txt` sent the literal "/typoed/path.txt"
//     to the model — burning tokens on a confused reply.
//   - anything else → take as-is (literal prompt). To pass a literal
//     starting with "@", escape it (e.g. " @foo" with a leading space)
//     or use --prompt - and pipe in.
const maxPromptInputBytes = 256 * 1024

func readMaybeStdin(spec string) (string, error) {
	if spec == "" {
		return "", nil
	}
	if spec == "-" {
		return readPromptInput(os.Stdin)
	}
	if strings.HasPrefix(spec, "@") {
		path := spec[1:]
		f, err := os.Open(path)
		if err != nil {
			return "", fmt.Errorf("read %s: %w", path, err)
		}
		defer f.Close()
		b, err := readPromptInput(f)
		if err != nil {
			return "", fmt.Errorf("read %s: %w", path, err)
		}
		return b, nil
	}
	return spec, nil
}

func readPromptInput(r io.Reader) (string, error) {
	b, err := io.ReadAll(io.LimitReader(r, maxPromptInputBytes+1))
	if err != nil {
		return "", err
	}
	if len(b) > maxPromptInputBytes {
		return "", fmt.Errorf("prompt input exceeds %d-byte limit", maxPromptInputBytes)
	}
	return string(b), nil
}

// openTrace opens the JSONL trace destination. For resumed sessions we
// append rather than truncate so the trace keeps growing alongside the
// conversation log.
func openTrace(spec string, append bool) (io.Writer, func() error, error) {
	switch spec {
	case "":
		return os.Stderr, func() error { return nil }, nil
	case "-":
		return os.Stdout, func() error { return nil }, nil
	}
	if err := os.MkdirAll(filepath.Dir(spec), 0o755); err != nil {
		return nil, nil, err
	}
	flags := os.O_CREATE | os.O_WRONLY
	if append {
		flags |= os.O_APPEND
	} else {
		flags |= os.O_TRUNC
	}
	f, err := os.OpenFile(spec, flags, 0o644)
	if err != nil {
		return nil, nil, err
	}
	return f, f.Close, nil
}

// resolveStorePath turns a user-supplied --memory-*-store value into
// a concrete path. Relative paths normally anchor to workspace; `~`
// expands to $HOME; absolute paths pass through unchanged. If the
// relative path already points at the workspace from the caller's cwd
// (for example ".tmp/eval/ws/.affent/memory" while --workspace is
// ".tmp/eval/ws"), keep that cwd-relative target instead of nesting it
// under workspace again.
func resolveStorePath(workspace, p string) string {
	if strings.HasPrefix(p, "~/") || p == "~" {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			if p == "~" {
				return home
			}
			return filepath.Join(home, p[2:])
		}
	}
	if filepath.IsAbs(p) {
		return p
	}
	if absP, err := filepath.Abs(p); err == nil && pathInside(workspace, absP) {
		return absP
	}
	return filepath.Join(workspace, p)
}

func pathInside(root, path string) bool {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(absRoot, absPath)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)))
}

// parseMemoryMaxChars accepts "MEM,USER" and returns the two ints.
// Returns ok=false when the input is empty (caller falls back to
// FileMemoryStore defaults).
func parseMemoryMaxChars(spec string) (memCap, userCap int, ok bool, err error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return 0, 0, false, nil
	}
	parts := strings.SplitN(spec, ",", 2)
	if len(parts) != 2 {
		return 0, 0, false, fmt.Errorf("expected MEM,USER, got %q", spec)
	}
	m, mErr := strconv.Atoi(strings.TrimSpace(parts[0]))
	if mErr != nil {
		return 0, 0, false, fmt.Errorf("parse MEM cap: %w", mErr)
	}
	u, uErr := strconv.Atoi(strings.TrimSpace(parts[1]))
	if uErr != nil {
		return 0, 0, false, fmt.Errorf("parse USER cap: %w", uErr)
	}
	if m <= 0 || u <= 0 {
		return 0, 0, false, fmt.Errorf("char limits must be positive, got mem=%d user=%d", m, u)
	}
	return m, u, true, nil
}
