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

	"github.com/affinefoundation/affent"
	"github.com/affinefoundation/affent/executor"
	"github.com/affinefoundation/affent/mcp"
	"github.com/affinefoundation/affent/sse"
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
	memoryWorkspaceStore string // path for MEMORY.md ("" → <workspace>/.affent/MEMORY.md)
	memoryUserStore      string // path for USER.md  ("" → $XDG_CONFIG_HOME/affent/USER.md)
	memoryMaxChars       string // "MEM,USER", e.g. "2200,1375"; "" → defaults

	// projectContext loads recognized user-authored project notes
	// (AGENTS.md, CONVENTIONS.md, .cursorrules, .clinerules, CLAUDE.md,
	// GEMINI.md) from --workspace and inlines them into the system
	// prompt. Default on; set --project-context=false to disable.
	projectContext bool

	compactTrigger  int // 0 disables proactive compaction; reactive still works
	compactKeepLast int

	sessionID    string // explicit; empty means "use --continue or new"
	continueLast bool   // pick most recent session under workspace

	mcpConfigPath string // path to MCP server config JSON (optional)

	// executor selects the shell-tool backend.
	//   "local"            — run on the host (default; current behavior)
	//   "docker:<cid>"     — run inside the named container via `docker exec`
	executor string
}

func (c *commonFlags) bind(fs *flag.FlagSet) {
	fs.StringVar(&c.configPath, "config", os.Getenv("AFFENTCTL_CONFIG"), "JSON config file; CLI flags override config values")
	fs.StringVar(&c.workspace, "workspace", "./affent-workspace", "working dir for shell + file tools")
	fs.StringVar(&c.baseURL, "base-url", os.Getenv("AFFENTCTL_BASE_URL"), "OpenAI-compat endpoint")
	fs.StringVar(&c.apiKey, "api-key", os.Getenv("AFFENTCTL_API_KEY"), "API key")
	fs.StringVar(&c.model, "model", os.Getenv("AFFENTCTL_MODEL"), "model id")
	fs.IntVar(&c.maxTurns, "max-turns", 10, "max tool-call rounds per user message")
	fs.DurationVar(&c.callTimeout, "max-call-timeout", affent.DefaultPerCallTimeout, "per-LLM-call timeout")
	fs.IntVar(&c.retryTransient, "retry-transient", affent.DefaultTransientRetries, "retry attempts on transient LLM errors (5xx/429/408/net/EOF/timeout); 0 disables")
	fs.DurationVar(&c.retryBackoff, "retry-backoff", affent.DefaultTransientBackoff, "initial backoff between retries; doubles each attempt")
	fs.StringVar(&c.tracePath, "trace", "", "JSONL trace path; '-' for stdout, '' for stderr")
	fs.BoolVar(&c.traceSkipDeltas, "trace-skip-deltas", false, "skip thinking/message deltas in trace (smaller trace, no token-level replay; final text still in message.end)")
	fs.StringVar(&c.systemPromptPath, "system-prompt", "", "override system prompt; '-' or file path or literal")
	fs.BoolVar(&c.quiet, "quiet", false, "suppress stderr progress")
	fs.BoolVar(&c.memoryEnabled, "memory", false, "enable persistent memory: inject MEMORY.md / USER.md snapshot into the system prompt and register the memory tool")
	fs.BoolVar(&c.memoryOnly, "memory-only", false, "register only the memory tool (no shell/file/MCP) and disable project context; for memory benchmarks. Implies --memory")
	fs.StringVar(&c.memoryWorkspaceStore, "memory-workspace-store", "", "path to MEMORY.md; default <workspace>/.affent/MEMORY.md")
	fs.StringVar(&c.memoryUserStore, "memory-user-store", "", "path to USER.md; default $XDG_CONFIG_HOME/affent/USER.md (cross-workspace)")
	fs.StringVar(&c.memoryMaxChars, "memory-max-chars", "", "memory char limits as MEM,USER (default 2200,1375)")
	fs.BoolVar(&c.projectContext, "project-context", true, "auto-load AGENTS.md / CONVENTIONS.md / .cursorrules / .clinerules / CLAUDE.md / GEMINI.md from --workspace into the system prompt")
	fs.StringVar(&c.sessionID, "session-id", "", "resume the named session (under --workspace/.affentctl/)")
	fs.BoolVar(&c.continueLast, "continue", false, "resume the most recent session under --workspace")
	fs.StringVar(&c.mcpConfigPath, "mcp-config", os.Getenv("AFFENTCTL_MCP_CONFIG"), "path to MCP server config JSON ({\"servers\":[{...}]})")
	fs.IntVar(&c.compactTrigger, "compact-trigger", 240, "compact conversation when message count exceeds this; 0 disables proactive compaction (reactive still kicks in on context-overflow errors)")
	fs.IntVar(&c.compactKeepLast, "compact-keep-last", 10, "messages preserved verbatim at the tail of the conversation when compacting")
	fs.StringVar(&c.executor, "executor", envOr("AFFENTCTL_EXECUTOR", "local"), "shell-tool backend: 'local' (host; no isolation), or 'docker:<container_id>' (exec into an already-running container, e.g. 'docker:abc123def'; file tools also route through docker so they see the container's filesystem). Caller manages container lifecycle.")
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
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
		WorkspaceStore *string `json:"workspace_store"`
		UserStore      *string `json:"user_store"`
		MaxChars       *string `json:"max_chars"`
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
}

func applyConfig(c *commonFlags, fs *flag.FlagSet) error {
	if err := loadConfigFile(c, fs); err != nil {
		return err
	}
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
		return nil
	}
	raw, err := os.ReadFile(c.configPath)
	if err != nil {
		return fmt.Errorf("read config %s: %w", c.configPath, err)
	}
	var cfg fileConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return fmt.Errorf("parse config %s: %w", c.configPath, err)
	}
	setByCLI := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { setByCLI[f.Name] = true })

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
	setString("executor", &c.executor, cfg.Executor)
	return nil
}

// loopBundle is everything a subcommand needs after setup: the loop
// (already system-primed), its events channel, the trace writer, the
// resolved session id, MCP clients to keep alive, and a closer to call
// before exit.
type loopBundle struct {
	loop       *affent.Loop
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
	systemPrompt := affent.DefaultSystemPrompt
	if c.systemPromptPath != "" {
		raw, err := readMaybeStdin(c.systemPromptPath)
		if err != nil {
			log.Error().Err(err).Msg("read system-prompt")
			return nil, 3
		}
		systemPrompt = raw
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

	var memStore affent.MemoryStore
	if c.memoryEnabled {
		fs := affent.NewFileMemoryStore(workspace)
		if c.memoryWorkspaceStore != "" {
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
			fs.MemoryCharLimit = memCap
			fs.UserCharLimit = userCap
		}
		memStore = fs
	}

	tools := affent.NewRegistry()
	if c.memoryOnly {
		if memStore == nil {
			log.Error().Msg("--memory-only requires a usable memory store; check --memory-workspace-store / --memory-user-store")
			_ = traceClose()
			return nil, 3
		}
		affent.RegisterMemoryOnly(tools, memStore)
	} else {
		exec, execErr := buildExecutor(c.executor, sid, workspace)
		if execErr != nil {
			log.Error().Err(execErr).Msg("executor")
			_ = traceClose()
			return nil, 64
		}
		affent.RegisterBuiltins(tools, affent.BuiltinDeps{
			Executor:         exec,
			HostWorkspaceDir: workspace,
			Memory:           memStore,
			SessionsDir:      convDir,
			SessionID:        sid,
		})
	}

	// Optional MCP servers, registered onto the same tool registry as
	// the builtins. Tool names are namespaced "<server>_<tool>" so they
	// can't collide with the builtins or with each other.
	mcpClients, err := startMCP(c.mcpConfigPath, tools, log)
	if err != nil {
		log.Error().Err(err).Msg("mcp setup")
		_ = traceClose()
		return nil, 3
	}

	conv, err := affent.OpenConversationAt(filepath.Join(convDir, sid+".jsonl"))
	if err != nil {
		log.Error().Err(err).Msg("conversation")
		_ = traceClose()
		for _, mc := range mcpClients {
			_ = mc.Close()
		}
		return nil, 3
	}

	events := make(chan sse.Event, 64)
	llm := affent.NewLLMClient(c.baseURL, c.apiKey, c.model)
	projectContextDir := ""
	if c.projectContext {
		projectContextDir = workspace
	}
	loop := &affent.Loop{
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
	// Always attach the rolling-summary compactor. Without it, an
	// overflowed context kills the turn (the loop's reactive compaction
	// path is gated on l.Compactor != nil). User knobs override the
	// OpenHands-style defaults; reusing the same LLM client means
	// compactions hit the same provider/model the agent uses.
	triggerMsgs := c.compactTrigger
	if triggerMsgs <= 0 {
		triggerMsgs = affent.DefaultSummaryTriggerMsgs
	}
	keepLast := c.compactKeepLast
	if keepLast <= 0 {
		keepLast = affent.DefaultSummaryKeepLast
	}
	loop.Compactor = &affent.LLMSummaryCompactor{
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

func startMCP(configPath string, reg *affent.Registry, log zerolog.Logger) ([]*mcp.Client, error) {
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
func readMaybeStdin(spec string) (string, error) {
	if spec == "" {
		return "", nil
	}
	if spec == "-" {
		b, err := io.ReadAll(os.Stdin)
		return string(b), err
	}
	if strings.HasPrefix(spec, "@") {
		spec = spec[1:]
	}
	if _, err := os.Stat(spec); err == nil {
		b, err := os.ReadFile(spec)
		return string(b), err
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
// a concrete path. Relative paths anchor to workspace; `~` expands to
// $HOME; absolute paths pass through unchanged.
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
	return filepath.Join(workspace, p)
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
