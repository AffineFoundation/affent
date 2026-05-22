package main

import (
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
	"unicode/utf8"

	agent "github.com/affinefoundation/affent/internal/agent"
	"github.com/affinefoundation/affent/internal/executor"
	"github.com/affinefoundation/affent/internal/mcp"
	"github.com/affinefoundation/affent/internal/sse"
	"github.com/google/uuid"
	"github.com/rs/zerolog"
)

// trimUTF8 returns s clipped to at most n bytes, snapping back to a
// UTF-8 rune boundary so multi-byte sequences (CJK / Cyrillic /
// accented Latin / emoji) aren't split across the cut. Callers append
// their own ellipsis marker.
func trimUTF8(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
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

	// memoryEnabled composes the MEMORY.md / USER.md snapshot into
	// the system prompt at session start and registers the `memory`
	// tool.
	memoryEnabled bool
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

	compactTrigger  int // 0 disables proactive compaction; reactive still works
	compactKeepLast int

	sessionID    string // explicit; empty means "use --continue or new"
	continueLast bool   // pick most recent session under workspace

	mcpConfigPath  string // path to MCP server config JSON (optional)
	mcpNoNamespace bool   // advertise MCP tools under their raw MCP tool names

	// executor selects the shell-tool backend.
	//   "local"            — run on the host (default; current behavior)
	//   "docker:<cid>"     — run inside the named container via `docker exec`
	executor string

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
	fs.IntVar(&c.maxTurns, "max-turns", 10, "max tool-call rounds per user message")
	fs.DurationVar(&c.callTimeout, "max-call-timeout", agent.DefaultPerCallTimeout, "per-LLM-call timeout")
	fs.IntVar(&c.retryTransient, "retry-transient", agent.DefaultTransientRetries, "retry attempts on transient LLM errors (5xx/429/408/net/EOF/timeout); 0 disables")
	fs.DurationVar(&c.retryBackoff, "retry-backoff", agent.DefaultTransientBackoff, "initial backoff between retries; doubles each attempt")
	fs.StringVar(&c.tracePath, "trace", "", "JSONL trace path; '-' for stdout, '' for stderr")
	fs.BoolVar(&c.traceSkipDeltas, "trace-skip-deltas", false, "skip thinking/message deltas in trace (smaller trace, no token-level replay; final text still in message.end)")
	fs.StringVar(&c.systemPromptPath, "system-prompt", "", "override system prompt; '-' or file path or literal")
	fs.BoolVar(&c.quiet, "quiet", false, "suppress stderr progress")
	fs.BoolVar(&c.memoryEnabled, "memory", false, "enable persistent memory: inject MEMORY.md / USER.md snapshot into the system prompt and register the memory tool")
	fs.BoolVar(&c.memoryOnly, "memory-only", false, "register only the memory tool (no shell/file/MCP) and disable project context; for memory benchmarks. Implies --memory")
	fs.StringVar(&c.memoryWorkspaceStore, "memory-workspace-store", "", "(legacy) path to a pre-v2 single-file MEMORY.md; if set, migration moves it into the v2 topic layout on first access. Prefer --memory-dir for new setups.")
	fs.StringVar(&c.memoryDir, "memory-dir", "", "path to the v2 memory dir (core.md + topics/*.md); default <workspace>/.affent/memory")
	fs.StringVar(&c.memoryUserStore, "memory-user-store", "", "path to USER.md; default $XDG_CONFIG_HOME/affent/USER.md (cross-workspace)")
	fs.StringVar(&c.memoryMaxChars, "memory-max-chars", "", "char limits as CORE,USER (default 2200,1375). Per-topic cap → --memory-topic-max-chars.")
	fs.IntVar(&c.memoryTopicMaxChars, "memory-topic-max-chars", 0, "per-topic char cap; 0 → DefaultTopicCharLimit (4400). Each custom topic (auth, deploy, ...) is bounded independently; total memory grows by topic count.")
	fs.IntVar(&c.memoryMaxTopics, "memory-max-topics", 0, "distinct-topic count cap; 0 → DefaultMaxTopics (32). Pass a large number (e.g. 1000) to effectively disable for memory benchmarks that legitimately want many named scratchpads.")
	fs.BoolVar(&c.projectContext, "project-context", true, "auto-load AGENTS.md / CONVENTIONS.md / .cursorrules / .clinerules / CLAUDE.md / GEMINI.md from --workspace into the system prompt")
	fs.StringVar(&c.sessionID, "session-id", "", "resume the named session (under --workspace/.affentctl/)")
	fs.BoolVar(&c.continueLast, "continue", false, "resume the most recent session under --workspace")
	fs.StringVar(&c.mcpConfigPath, "mcp-config", "", "path to MCP server config JSON ({\"servers\":[{...}]}) (env: AFFENTCTL_MCP_CONFIG)")
	fs.BoolVar(&c.mcpNoNamespace, "mcp-no-namespace", false, "advertise MCP tools to the model under their original names (no <server>_ prefix); use with models tuned for unprefixed MCP tool names")
	fs.IntVar(&c.compactTrigger, "compact-trigger", 240, "compact conversation when message count exceeds this. 0 / negative → fall back to agent runtime's default (240). Reactive compaction (on context-overflow errors) is unaffected.")
	fs.IntVar(&c.compactKeepLast, "compact-keep-last", 10, "messages preserved verbatim at the tail of the conversation when compacting")
	fs.StringVar(&c.executor, "executor", "local", "shell-tool backend: 'local' (host; no isolation), or 'docker:<container_id>' (exec into an already-running container, e.g. 'docker:abc123def'; file tools also route through docker so they see the container's filesystem). Caller manages container lifecycle. (env: AFFENTCTL_EXECUTOR)")
	fs.StringVar(&c.temperature, "temperature", "", "sampling temperature forwarded to upstream LLM (omit → provider default; set 0 for deterministic eval decoding)")
	fs.StringVar(&c.topP, "top-p", "", "top-p (nucleus) sampling forwarded to upstream (omit → provider default)")
	fs.StringVar(&c.maxTokens, "max-tokens", "", "max output tokens forwarded to upstream (omit → provider default)")
	fs.StringVar(&c.seed, "seed", "", "deterministic-sampling seed forwarded to upstream (omit → provider default)")
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
	return s, nil
}

// flagEnvSources maps flag-name → env-var-name for every flag whose
// default reads from an env var. Used by loadConfigFile so the env
// var beats the config file (matches the documented precedence in
// the flag table: env is a first-class lane, not a default).
//
// Bind kept in sync with the `bind` method above. Tests in
// common_test.go catch drift.
var flagEnvSources = map[string]string{
	"config":     "AFFENTCTL_CONFIG",
	"base-url":   "AFFENTCTL_BASE_URL",
	"api-key":    "AFFENTCTL_API_KEY",
	"model":      "AFFENTCTL_MODEL",
	"mcp-config": "AFFENTCTL_MCP_CONFIG",
	"executor":   "AFFENTCTL_EXECUTOR",
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
	Memory          *struct {
		Enabled        *bool   `json:"enabled"`
		Only           *bool   `json:"only"`
		WorkspaceStore *string `json:"workspace_store"` // legacy single-file pointer; triggers migration
		Dir            *string `json:"dir"`             // v2 memory directory
		UserStore      *string `json:"user_store"`
		MaxChars       *string `json:"max_chars"`
	} `json:"memory"`
	ProjectContext *bool `json:"project_context"`
	Compact        *struct {
		Trigger  *int `json:"trigger"`
		KeepLast *int `json:"keep_last"`
	} `json:"compact"`
	SessionID      *string `json:"session_id"`
	Continue       *bool   `json:"continue"`
	MCPConfig      *string `json:"mcp_config"`
	MCPNoNamespace *bool   `json:"mcp_no_namespace"`
	Executor       *string `json:"executor"`
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
	applyEnvConfig(c, fs)
	// memory-only is the isolation mode: register only the memory
	// tool and inject no other content sources into the system prompt.
	// It implies --memory=true and forces --project-context=false
	// regardless of how either was set.
	if c.memoryOnly {
		c.memoryEnabled = true
		c.projectContext = false
	}
	return nil
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
	raw, err := os.ReadFile(c.configPath)
	if err != nil {
		return fmt.Errorf("read config %s: %w", c.configPath, err)
	}
	var cfg fileConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return fmt.Errorf("parse config %s: %w", c.configPath, err)
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
	for name, env := range flagEnvSources {
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
	if cfg.Memory != nil {
		setBool("memory", &c.memoryEnabled, cfg.Memory.Enabled)
		setBool("memory-only", &c.memoryOnly, cfg.Memory.Only)
		setString("memory-workspace-store", &c.memoryWorkspaceStore, cfg.Memory.WorkspaceStore)
		setString("memory-dir", &c.memoryDir, cfg.Memory.Dir)
		setString("memory-user-store", &c.memoryUserStore, cfg.Memory.UserStore)
		setString("memory-max-chars", &c.memoryMaxChars, cfg.Memory.MaxChars)
	}
	setBool("project-context", &c.projectContext, cfg.ProjectContext)
	if cfg.Compact != nil {
		setInt("compact-trigger", &c.compactTrigger, cfg.Compact.Trigger)
		setInt("compact-keep-last", &c.compactKeepLast, cfg.Compact.KeepLast)
	}
	setString("session-id", &c.sessionID, cfg.SessionID)
	setBool("continue", &c.continueLast, cfg.Continue)
	setString("mcp-config", &c.mcpConfigPath, cfg.MCPConfig)
	setBool("mcp-no-namespace", &c.mcpNoNamespace, cfg.MCPNoNamespace)
	setString("executor", &c.executor, cfg.Executor)
	setString("temperature", &c.temperature, cfg.Temperature)
	setString("top-p", &c.topP, cfg.TopP)
	setString("max-tokens", &c.maxTokens, cfg.MaxTokens)
	setString("seed", &c.seed, cfg.Seed)
	return nil
}

func applyEnvConfig(c *commonFlags, fs *flag.FlagSet) {
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

	setString("base-url", "AFFENTCTL_BASE_URL", &c.baseURL)
	setString("api-key", "AFFENTCTL_API_KEY", &c.apiKey)
	setString("model", "AFFENTCTL_MODEL", &c.model)
	setString("mcp-config", "AFFENTCTL_MCP_CONFIG", &c.mcpConfigPath)
	setString("executor", "AFFENTCTL_EXECUTOR", &c.executor)
}

// loopBundle is everything a subcommand needs after setup: the loop
// (already system-primed), its events channel, the trace writer, the
// resolved session id, MCP clients to keep alive, and a closer to call
// before exit.
type loopBundle struct {
	loop       *agent.Loop
	events     chan sse.Event
	trace      io.Writer
	traceClose func() error
	sessionID  string
	resumed    bool // true if we loaded an existing conversation
	workspace  string
	log        zerolog.Logger

	mcpClients []*mcp.Client
}

func (b *loopBundle) close() {
	for _, c := range b.mcpClients {
		_ = c.Close()
	}
	if b.traceClose != nil {
		_ = b.traceClose()
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

	if c.model == "" {
		log.Error().Msg("--model (or AFFENTCTL_MODEL) is required")
		return nil, 64
	}

	workspace, err := filepath.Abs(c.workspace)
	if err != nil {
		log.Error().Err(err).Msg("resolve workspace")
		return nil, 3
	}
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		log.Error().Err(err).Msg("mkdir workspace")
		return nil, 3
	}

	convDir := filepath.Join(workspace, ".affentctl")
	if err := os.MkdirAll(convDir, 0o755); err != nil {
		log.Error().Err(err).Msg("mkdir conv dir")
		return nil, 3
	}

	sid, resumed, err := resolveSessionID(convDir, c.sessionID, c.continueLast)
	if err != nil {
		log.Error().Err(err).Msg("resolve session id")
		return nil, 3
	}

	// Default system prompt; resumed conversations already have one in
	// the log so EnsureSystemPrompt below is a no-op for them.
	// --memory-only swaps to a memory-only-tailored prompt because the
	// generic dev-box default tells the model about shell/file/schedule_*
	// tools that aren't registered in this mode (leads to wasted "tool
	// not available" tool calls + misleading the user about capabilities).
	systemPrompt := agent.DefaultSystemPrompt
	if c.memoryOnly {
		systemPrompt = agent.MemoryOnlySystemPrompt
	}
	if c.systemPromptPath != "" {
		raw, err := readMaybeStdin(c.systemPromptPath)
		if err != nil {
			log.Error().Err(err).Msg("read system-prompt")
			return nil, 3
		}
		systemPrompt = raw
	} else if !c.memoryOnly && workspace != "/workspace" {
		// DefaultSystemPrompt is framed for the affine-agents dev-box
		// where /workspace is bind-mounted. Standalone affentctl points
		// at an arbitrary --workspace, so the model would otherwise burn
		// 1-2 tool calls calling /workspace paths before discovering the
		// real path from the rejection message. Anchor it explicitly.
		// Skipped under --memory-only — that mode has no file tools at
		// all and the workspace anchor would be a non sequitur.
		systemPrompt += "\n\nYour workspace directory is \"" + workspace +
			"\". Use this exact path (or a relative path inside it) with the file tools."
	}

	traceWriter, traceClose, err := openTrace(c.tracePath, resumed)
	if err != nil {
		log.Error().Err(err).Msg("open trace")
		return nil, 3
	}

	if c.memoryOnly && c.mcpConfigPath != "" {
		log.Error().Msg("--memory-only cannot be combined with --mcp-config; --memory-only exposes only the memory tool")
		_ = traceClose()
		return nil, 64
	}

	var memStore agent.MemoryStore
	if c.memoryEnabled {
		fs := agent.NewFileMemoryStore(workspace)
		if c.memoryDir != "" {
			fs.MemoryDir = resolveStorePath(workspace, c.memoryDir)
		}
		if c.memoryWorkspaceStore != "" {
			// Legacy single-file pointer — migration code picks it up on
			// first access and moves it into the v2 layout.
			fs.MemoryPath = resolveStorePath(workspace, c.memoryWorkspaceStore)
		}
		if c.memoryUserStore != "" {
			fs.UserPath = resolveStorePath(workspace, c.memoryUserStore)
		}
		if memCap, userCap, ok, perr := parseMemoryMaxChars(c.memoryMaxChars); perr != nil {
			log.Error().Err(perr).Msg("parse --memory-max-chars")
			_ = traceClose()
			return nil, 64
		} else if ok {
			// memCap maps to core (always-in-prompt).
			fs.CoreCharLimit = memCap
			fs.UserCharLimit = userCap
		}
		if c.memoryTopicMaxChars > 0 {
			fs.TopicCharLimit = c.memoryTopicMaxChars
		}
		if c.memoryMaxTopics > 0 {
			fs.MaxTopics = c.memoryMaxTopics
		}
		memStore = fs
	}

	tools := agent.NewRegistry()
	var execBackend executor.Executor
	if c.memoryOnly {
		if memStore == nil {
			log.Error().Msg("--memory-only requires a usable memory store; check --memory-workspace-store / --memory-user-store")
			_ = traceClose()
			return nil, 3
		}
		agent.RegisterMemoryOnly(tools, memStore)
	} else {
		var execErr error
		execBackend, execErr = buildExecutor(c.executor, sid, workspace)
		if execErr != nil {
			log.Error().Err(execErr).Msg("executor")
			_ = traceClose()
			return nil, 64
		}
		agent.RegisterBuiltins(tools, agent.BuiltinDeps{
			Executor:         execBackend,
			HostWorkspaceDir: workspace,
			Memory:           memStore,
			SessionsDir:      convDir,
			SessionID:        sid,
		})
	}

	// Optional MCP servers, registered onto the same tool registry as
	// the builtins. Tool names are namespaced by default so they can't
	// collide with the builtins or with each other.
	mcpClients, err := startMCP(c.mcpConfigPath, c.mcpNoNamespace, tools, log)
	if err != nil {
		log.Error().Err(err).Msg("mcp setup")
		_ = traceClose()
		return nil, 3
	}

	conv, err := agent.OpenConversationAt(filepath.Join(convDir, sid+".jsonl"))
	if err != nil {
		log.Error().Err(err).Msg("conversation")
		_ = traceClose()
		for _, mc := range mcpClients {
			_ = mc.Close()
		}
		return nil, 3
	}

	events := make(chan sse.Event, 64)
	llm := agent.NewLLMClient(c.baseURL, c.apiKey, c.model)
	if sampling, err := parseSampling(c.temperature, c.topP, c.maxTokens, c.seed); err != nil {
		log.Error().Err(err).Msg("parse sampling")
		_ = traceClose()
		for _, mc := range mcpClients {
			_ = mc.Close()
		}
		return nil, 3
	} else {
		llm.Sampling = sampling
	}
	projectContextDir := ""
	if c.projectContext {
		projectContextDir = workspace
	}
	if !c.memoryOnly {
		agent.RegisterSubagent(tools, agent.SubagentDeps{
			LLM:               llm,
			Executor:          execBackend,
			HostWorkspaceDir:  workspace,
			Memory:            memStore,
			SessionsDir:       convDir,
			ParentSessionID:   sid,
			TranscriptDir:     filepath.Join(convDir, "subagents", sid),
			ProjectContextDir: projectContextDir,
			Log:               log,
			PerCallTimeout:    c.callTimeout,
		})
		systemPrompt = agent.WithSubagentSystemGuidance(systemPrompt)
	}
	loop := &agent.Loop{
		LLM:                 llm,
		Tools:               tools,
		Conv:                conv,
		Events:              events,
		Log:                 log,
		MaxTurnSteps:        c.maxTurns,
		PerCallTimeout:      c.callTimeout,
		MaxTransientRetries: c.retryTransient,
		TransientBackoff:    c.retryBackoff,
		Memory:              memStore,
		ProjectContextDir:   projectContextDir,
	}
	if !c.memoryOnly {
		loop.FirstToolPolicy = agent.SubagentFirstToolPolicy()
		loop.PostToolPolicy = agent.SubagentPostToolPolicy()
	}
	// Always attach the rolling-summary compactor. Without it, an
	// overflowed context kills the turn (the loop's reactive compaction
	// path is gated on l.Compactor != nil). User knobs override the
	// OpenHands-style defaults; reusing the same LLM client means
	// compactions hit the same provider/model the agent uses.
	triggerMsgs := c.compactTrigger
	if triggerMsgs <= 0 {
		triggerMsgs = agent.DefaultSummaryTriggerMsgs
	}
	keepLast := c.compactKeepLast
	if keepLast <= 0 {
		keepLast = agent.DefaultSummaryKeepLast
	}
	loop.Compactor = &agent.LLMSummaryCompactor{
		LLM:         llm,
		TriggerMsgs: triggerMsgs,
		KeepLast:    keepLast,
	}
	if err := loop.EnsureSystemPrompt(systemPrompt); err != nil {
		log.Error().Err(err).Msg("seed system prompt")
		_ = traceClose()
		for _, mc := range mcpClients {
			_ = mc.Close()
		}
		return nil, 3
	}

	return &loopBundle{
		loop:       loop,
		events:     events,
		trace:      traceWriter,
		traceClose: traceClose,
		sessionID:  sid,
		resumed:    resumed,
		workspace:  workspace,
		log:        log,
		mcpClients: mcpClients,
	}, 0
}

// buildExecutor parses the --executor spec and returns the matching
// affent executor. "local" (or empty) → LocalExecutor; "docker:<cid>" →
// DockerExecExecutor pointed at the named container. Unknown specs are
// a hard error so typos don't silently fall back.
func buildExecutor(spec, sessionID, workspace string) (executor.Executor, error) {
	switch {
	case spec == "" || spec == "local":
		return executor.NewLocalExecutor(sessionID, workspace), nil
	case strings.HasPrefix(spec, "docker:"):
		cid := strings.TrimPrefix(spec, "docker:")
		if cid == "" {
			return nil, fmt.Errorf("--executor docker: requires a container id (e.g. docker:abc123)")
		}
		return executor.NewDockerExecExecutor(sessionID, cid), nil
	default:
		return nil, fmt.Errorf("unknown --executor %q (valid: local, docker:<container_id>)", spec)
	}
}

// mcpConfig is the on-disk shape for --mcp-config. Compatible with the
// "servers" array shape used by Claude Desktop / Goose configs (just
// our flat field names).
type mcpConfig struct {
	Servers []mcp.ServerSpec `json:"servers"`
}

func startMCP(configPath string, noNamespace bool, reg *agent.Registry, log zerolog.Logger) ([]*mcp.Client, error) {
	if configPath == "" {
		return nil, nil
	}
	raw, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("read mcp config %s: %w", configPath, err)
	}
	var cfg mcpConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("parse mcp config: %w", err)
	}
	if len(cfg.Servers) == 0 {
		return nil, nil
	}
	if noNamespace {
		disabled := false
		for i := range cfg.Servers {
			cfg.Servers[i].Namespace = &disabled
		}
	}
	// Bound the launch + handshake total. Each Start has its own 30s
	// initialize timeout; we wrap the loop in a slightly larger budget
	// for sanity.
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	return mcp.RegisterAll(ctx, reg, cfg.Servers, log)
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
	entries, err := os.ReadDir(convDir)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	type item struct {
		sid string
		mt  time.Time
	}
	var items []item
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		items = append(items, item{
			sid: strings.TrimSuffix(e.Name(), ".jsonl"),
			mt:  info.ModTime(),
		})
	}
	if len(items) == 0 {
		return "", nil
	}
	sort.Slice(items, func(i, j int) bool { return items[i].mt.After(items[j].mt) })
	return items[0].sid, nil
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
func readMaybeStdin(spec string) (string, error) {
	if spec == "" {
		return "", nil
	}
	if spec == "-" {
		b, err := io.ReadAll(os.Stdin)
		return string(b), err
	}
	if strings.HasPrefix(spec, "@") {
		path := spec[1:]
		b, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("read %s: %w", path, err)
		}
		return string(b), nil
	}
	return spec, nil
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
