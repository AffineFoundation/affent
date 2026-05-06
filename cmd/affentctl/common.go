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
	"strings"
	"time"

	"github.com/affinefoundation/affent"
	"github.com/affinefoundation/affent/executor"
	"github.com/affinefoundation/affent/mcp"
	"github.com/affinefoundation/affent/sse"
	"github.com/google/uuid"
	"github.com/rs/zerolog"
)

// commonFlags is what every subcommand wants -- model endpoint, workspace,
// trace destination, system-prompt override, session selection. Bind once,
// register on each command's FlagSet via bind().
type commonFlags struct {
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

	sessionID    string // explicit; empty means "use --continue or new"
	continueLast bool   // pick most recent session under workspace

	mcpConfigPath string // path to MCP server config JSON (optional)
}

func (c *commonFlags) bind(fs *flag.FlagSet) {
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
	fs.StringVar(&c.sessionID, "session-id", "", "resume the named session (under --workspace/.affentctl/)")
	fs.BoolVar(&c.continueLast, "continue", false, "resume the most recent session under --workspace")
	fs.StringVar(&c.mcpConfigPath, "mcp-config", os.Getenv("AFFENTCTL_MCP_CONFIG"), "path to MCP server config JSON ({\"servers\":[{...}]})")
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

	exec := executor.NewLocalExecutor(sid, workspace)

	tools := affent.NewRegistry()
	affent.RegisterBuiltins(tools, affent.BuiltinDeps{
		Executor:         exec,
		HostWorkspaceDir: workspace,
	})

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
	loop := &affent.Loop{
		LLM:                 affent.NewLLMClient(c.baseURL, c.apiKey, c.model),
		Tools:               tools,
		Conv:                conv,
		Events:              events,
		Log:                 log,
		MaxTurnSteps:        c.maxTurns,
		PerCallTimeout:      c.callTimeout,
		MaxTransientRetries: c.retryTransient,
		TransientBackoff:    c.retryBackoff,
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

// quiet unused-import linter when helpers get trimmed.
var _ = fmt.Sprintf
