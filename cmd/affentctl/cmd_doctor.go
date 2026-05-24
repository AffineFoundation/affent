package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	agent "github.com/affinefoundation/affent/internal/agent"
	"github.com/affinefoundation/affent/internal/mcp"
	"github.com/affinefoundation/affent/internal/memory"
	"github.com/rs/zerolog"
)

type doctorFinding struct {
	Status  string
	Subject string
	Message string
}

func doctorCmd(args []string) int {
	return doctorCmdWithRunner(args, osCommandRunner{}, os.Stdout, os.Stderr)
}

func doctorCmdWithRunner(args []string, runner commandRunner, stdout, stderr io.Writer) int {
	var cf commonFlags
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cf.bind(fs)
	fs.Usage = func() {
		fmt.Fprintln(stderr, `usage: affentctl doctor [flags]

Checks the resolved affentctl config, workspace, executor, Docker availability,
and local image build sources without calling the model or starting containers.`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(stderr, "doctor: unexpected argument %q\n", fs.Arg(0))
		return exitUsage
	}
	if err := applyConfig(&cf, fs); err != nil {
		fmt.Fprintf(stderr, "doctor: %v\n", err)
		return exitUsage
	}
	findings := diagnoseAffentctl(cf, runner)
	hasError := false
	for _, f := range findings {
		if f.Status == "error" {
			hasError = true
		}
		fmt.Fprintf(stdout, "%s %-18s %s\n", f.Status, f.Subject+":", f.Message)
	}
	if hasError {
		return exitRuntime
	}
	return 0
}

func diagnoseAffentctl(c commonFlags, runner commandRunner) []doctorFinding {
	var out []doctorFinding
	add := func(status, subject, msg string) {
		out = append(out, doctorFinding{Status: status, Subject: subject, Message: msg})
	}

	workspace, err := filepath.Abs(strings.TrimSpace(c.workspace))
	if err != nil {
		add("error", "workspace", "cannot resolve: "+err.Error())
	} else if err := ensureWritableDir(workspace); err != nil {
		add("error", "workspace", err.Error())
	} else {
		add("ok", "workspace", workspace+" is writable")
	}

	if strings.TrimSpace(c.model) == "" {
		add("error", "model", "missing --model or AFFENTCTL_MODEL")
	} else {
		add("ok", "model", c.model)
	}

	baseURL := effectiveBaseURL(c.baseURL)
	add("ok", "base-url", baseURL)
	if strings.TrimSpace(c.apiKey) == "" {
		if baseURL == agent.DefaultBaseURL {
			add("error", "api-key", "missing --api-key or AFFENTCTL_API_KEY for the default OpenAI endpoint")
		} else {
			add("warn", "api-key", "empty; this is only valid for endpoints that do not require auth")
		}
	} else {
		add("ok", "api-key", "set")
	}

	if _, err := parseSampling(c.temperature, c.topP, c.maxTokens); err != nil {
		add("error", "sampling", err.Error())
	} else {
		add("ok", "sampling", "valid")
	}
	trigger, keepLast := resolveCompactionConfig(c.compactTrigger, c.compactKeepLast)
	add("ok", "compaction", fmt.Sprintf("trigger=%d keep_last=%d", trigger, keepLast))
	add("ok", "boundaries", doctorBoundarySummary(c))
	add("ok", "capabilities", doctorCapabilitySummary(c))
	if status, msg := doctorSystemPrompt(c.systemPromptPath); status != "" {
		add(status, "system-prompt", msg)
	}
	if status, msg := doctorTrace(c.tracePath); status != "" {
		add(status, "trace", msg)
	}

	if c.memoryEnabled {
		if summary, err := doctorMemorySummary(c, workspace); err != nil {
			add("error", "memory", err.Error())
		} else {
			add("ok", "memory", summary)
		}
	} else {
		add("ok", "memory", "disabled")
	}
	if c.subagentEnabled {
		add("ok", "subagent", fmt.Sprintf("enabled max_depth=%d", c.subagentMaxDepth))
	} else {
		add("ok", "subagent", "disabled")
	}

	executorSpec := strings.TrimSpace(c.executor)
	executorOK := true
	if _, err := buildExecutor(executorSpec, "doctor", workspace); err != nil && executorSpec != "sandbox" {
		add("error", "executor", err.Error())
		executorOK = false
	} else {
		add("ok", "executor", executorSpec)
	}

	if executorOK && executorNeedsDocker(executorSpec) {
		if err := checkDockerAvailable(runner); err != nil {
			add("error", "docker", err.Error())
		} else {
			add("ok", "docker", "available")
			if strings.HasPrefix(executorSpec, "docker:") {
				if err := checkDockerContainerRunning(executorSpec, runner); err != nil {
					add("error", "docker-container", err.Error())
				} else {
					add("ok", "docker-container", strings.TrimPrefix(executorSpec, "docker:")+" is running")
				}
			}
		}
	}
	if c.mcpConfigPath == "" {
		add("ok", "mcp", "disabled")
	} else if summary, err := doctorMCPConfig(c.mcpConfigPath); err != nil {
		add("error", "mcp", err.Error())
	} else {
		add("ok", "mcp", summary)
	}
	if executorSpec == "sandbox" {
		if dockerfile, contextDir, ok, err := findSandboxBuildSource(); err != nil {
			add("error", "sandbox-image", err.Error())
		} else if ok {
			add("ok", "sandbox-image", fmt.Sprintf("build source %s context %s", dockerfile, contextDir))
		} else {
			add("warn", "sandbox-image", "source Dockerfile not found; Docker must pull or already have "+defaultSandboxImage)
		}
	}
	if dockerfile, contextDir, ok, err := findRuntimeBuildSource(); err != nil {
		add("error", "runtime-image", err.Error())
	} else if ok {
		add("ok", "runtime-image", fmt.Sprintf("build source %s context %s", dockerfile, contextDir))
	} else {
		add("warn", "runtime-image", "source Dockerfile not found; run affentctl image build from a source checkout")
	}

	return out
}

func doctorCapabilitySummary(c commonFlags) string {
	builtins := !c.memoryOnly
	mcpEnabled := builtins && strings.TrimSpace(c.mcpConfigPath) != ""
	subagentEnabled := builtins && c.subagentEnabled
	focusedTasksEnabled := builtins && c.focusedTasksEnabled
	skillInstall := builtins
	sessionSearch := builtins
	projectContext := builtins && c.projectContext
	memoryTool := c.memoryEnabled
	if c.memoryOnly {
		memoryTool = true
	}
	profiles := "off"
	if focusedTasksEnabled {
		// Build a probe from what affentctl actually wires: workspace +
		// LLM are always present at run time, executor + memory +
		// session_search follow the builtins/memory flags. affentctl
		// does NOT wire a web registrar today, so research drops out
		// of the schema — the model never sees a task_type it can't
		// fulfill. Doctor reflects that filter so the operator can
		// confirm the deployed surface matches their intent.
		probe := agent.FocusedTaskAvailabilityProbe{
			HasLLM:       true,
			HasWorkspace: true,
			HasExecutor:  builtins,
			HasMemory:    memoryTool,
			HasSessions:  sessionSearch,
			// HasWeb / HasBrowser stay false: affentctl has no path
			// that registers web or browser tools for focused tasks.
		}
		kinds := probe.AvailableKinds(nil)
		if len(kinds) == 0 {
			profiles = "none"
		} else {
			parts := make([]string, len(kinds))
			for i, k := range kinds {
				parts[i] = string(k)
			}
			profiles = strings.Join(parts, ",")
		}
	}
	return fmt.Sprintf(
		"shell_file=%t skill_install=%t memory=%t memory_only=%t session_search=%t project_context=%t mcp=%t subagent=%t subagent_max_depth=%d focused_tasks=%t focused_task_profiles=%s executor=%s",
		builtins,
		skillInstall,
		memoryTool,
		c.memoryOnly,
		sessionSearch,
		projectContext,
		mcpEnabled,
		subagentEnabled,
		c.subagentMaxDepth,
		focusedTasksEnabled,
		profiles,
		doctorCapabilityExecutor(c.executor),
	)
}

func doctorCapabilityExecutor(executor string) string {
	executor = strings.TrimSpace(executor)
	if executor == "local" || executor == "sandbox" {
		return executor
	}
	if strings.HasPrefix(executor, "docker:") {
		return "docker"
	}
	if executor == "" {
		return "unset"
	}
	return "custom"
}

func doctorBoundarySummary(c commonFlags) string {
	ab := agent.DefaultRuntimeBoundaries()
	mb := mcp.DefaultRuntimeBoundaries()
	return fmt.Sprintf(
		"prompt_input=%s system_prompt=%s config=%s max_turns=%d call_timeout=%s llm_request=%s llm_error_body=%s stream_content=%s stream_reasoning=%s stream_tool_args=%s stream_tool_calls=%d stream_scanner=%s tool_args_event=%s tool_arg_string=%s tool_result_context=%s tool_result_event=%s tool_result_preview=%s repairable_tool_args=%s project_context=%s mcp_result=%s mcp_http_json=%s mcp_http_sse_line=%s mcp_stdio_frame=%s",
		formatBytes(maxPromptInputBytes),
		formatBytes(maxPromptInputBytes),
		formatBytes(maxConfigInputBytes),
		c.maxTurns,
		c.callTimeout,
		formatBytes(ab.LLMRequestBodyBytes),
		formatBytes(ab.LLMErrorBodyBytes),
		formatBytes(ab.StreamContentBytes),
		formatBytes(ab.StreamReasoningBytes),
		formatBytes(ab.StreamToolArgBytes),
		ab.StreamToolCalls,
		formatBytes(ab.StreamScannerBytes),
		formatBytes(ab.ToolRequestArgsEvent),
		formatBytes(ab.ToolRequestArgString),
		formatBytes(ab.ToolResultContextBytes),
		formatBytes(ab.ToolResultEventBytes),
		formatBytes(ab.ToolResultPreviewBytes),
		formatBytes(ab.RepairableToolArgBytes),
		formatBytes(ab.ProjectContextBytes),
		formatBytes(mb.ToolResultBytes),
		formatBytes(mb.HTTPJSONResponseBytes),
		formatBytes(mb.HTTPSSELineBytes),
		formatBytes(mb.StdioFrameBytes),
	)
}

func formatBytes(n int) string {
	const kib = 1024
	if n > 0 && n%kib == 0 {
		kiB := n / kib
		if kiB%kib == 0 {
			return fmt.Sprintf("%dMiB", kiB/kib)
		}
		return fmt.Sprintf("%dKiB", kiB)
	}
	return fmt.Sprintf("%dB", n)
}

func doctorSystemPrompt(spec string) (string, string) {
	spec = strings.TrimSpace(spec)
	switch {
	case spec == "":
		return "ok", "default"
	case spec == "-":
		return "warn", "stdin prompt cannot be checked without consuming stdin"
	case strings.HasPrefix(spec, "@"):
		path := strings.TrimPrefix(spec, "@")
		f, err := os.Open(path)
		if err != nil {
			return "error", fmt.Sprintf("read %s: %v", path, err)
		}
		defer f.Close()
		raw, err := readPromptInput(f)
		if err != nil {
			return "error", fmt.Sprintf("read %s: %v", path, err)
		}
		return "ok", fmt.Sprintf("%s (%d bytes)", path, len(raw))
	default:
		return "ok", "literal override"
	}
}

func doctorTrace(spec string) (string, string) {
	spec = strings.TrimSpace(spec)
	switch spec {
	case "":
		return "ok", "stderr"
	case "-":
		return "ok", "stdout"
	}
	path, err := filepath.Abs(spec)
	if err != nil {
		return "error", "cannot resolve: " + err.Error()
	}
	if st, err := os.Stat(path); err == nil && st.IsDir() {
		return "error", path + " is a directory"
	} else if err != nil && !os.IsNotExist(err) {
		return "error", fmt.Sprintf("stat %s: %v", path, err)
	}
	parent := filepath.Dir(path)
	if err := ensureWritableDir(parent); err != nil {
		return "error", err.Error()
	}
	return "ok", path
}

func doctorMCPConfig(path string) (string, error) {
	var cfg mcpConfig
	if err := readConfigJSON(path, &cfg); err != nil {
		return "", fmt.Errorf("load %s: %w", path, err)
	}
	specs := make([]mcp.ServerSpec, 0, len(cfg.Servers))
	for i, server := range cfg.Servers {
		spec, err := server.serverSpec()
		if err != nil {
			return "", fmt.Errorf("servers[%d]: %w", i, err)
		}
		if err := validateMCPServerSpec(spec); err != nil {
			return "", fmt.Errorf("servers[%d]: %w", i, err)
		}
		specs = append(specs, spec)
	}
	if len(specs) == 0 {
		return "0 server(s)", nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), mcpStartupTimeout(specs))
	defer cancel()
	owners := map[string]string{}
	reports := make([]mcp.ToolGovernanceReport, 0, len(specs))
	for i, spec := range specs {
		report, err := mcp.DiagnoseServerTools(ctx, spec, owners, zerolog.Nop())
		if err != nil {
			return "", fmt.Errorf("servers[%d]: %w", i, err)
		}
		reports = append(reports, report)
	}
	return formatMCPGovernanceSummary(reports), nil
}

func formatMCPGovernanceSummary(reports []mcp.ToolGovernanceReport) string {
	totalRaw, totalFiltered, totalAccepted := 0, 0, 0
	totalSchemaBytes, totalDescriptionBytes, totalDescriptionTruncated := 0, 0, 0
	parts := make([]string, 0, len(reports))
	for _, r := range reports {
		totalRaw += r.RawToolCount
		totalFiltered += r.FilteredToolCount
		totalAccepted += len(r.AcceptedTools)
		serverSchemaBytes, serverDescriptionBytes, serverDescriptionTruncated := 0, 0, 0
		accepted := make([]string, 0, len(r.AcceptedTools))
		descriptionWarnings := make([]string, 0)
		for _, tool := range r.AcceptedTools {
			accepted = append(accepted, tool.AdvertisedName)
			serverSchemaBytes += tool.InputSchemaBytes
			serverDescriptionBytes += tool.DescriptionBytes
			if tool.DescriptionTruncated {
				serverDescriptionTruncated++
				descriptionWarnings = append(descriptionWarnings, tool.AdvertisedName)
			}
		}
		totalSchemaBytes += serverSchemaBytes
		totalDescriptionBytes += serverDescriptionBytes
		totalDescriptionTruncated += serverDescriptionTruncated
		sort.Strings(accepted)
		sort.Strings(descriptionWarnings)
		rejected := make([]string, 0, len(r.RejectedTools))
		for _, tool := range r.RejectedTools {
			rejected = append(rejected, tool.RawName+":"+tool.Reason)
		}
		sort.Strings(rejected)
		part := fmt.Sprintf("%s namespace=%t raw=%d filtered=%d schema=%s description=%s description_truncated=%d advertised=%s",
			r.ServerName,
			r.NamespaceEnabled,
			r.RawToolCount,
			r.FilteredToolCount,
			formatBytes(serverSchemaBytes),
			formatBytes(serverDescriptionBytes),
			serverDescriptionTruncated,
			formatDoctorList(accepted),
		)
		if len(descriptionWarnings) > 0 {
			part += " description_warnings=" + formatDoctorList(descriptionWarnings)
		}
		if len(rejected) > 0 {
			part += " rejected=" + formatDoctorList(rejected)
		}
		parts = append(parts, part)
	}
	return fmt.Sprintf("%d server(s) raw=%d filtered=%d advertised=%d schema=%s description=%s description_truncated=%d; %s",
		len(reports),
		totalRaw,
		totalFiltered,
		totalAccepted,
		formatBytes(totalSchemaBytes),
		formatBytes(totalDescriptionBytes),
		totalDescriptionTruncated,
		strings.Join(parts, "; "),
	)
}

func formatDoctorList(items []string) string {
	const maxItems = 20
	if len(items) == 0 {
		return "[]"
	}
	shown := items
	suffix := ""
	if len(items) > maxItems {
		shown = items[:maxItems]
		suffix = fmt.Sprintf(",+%d", len(items)-maxItems)
	}
	return "[" + strings.Join(shown, ",") + suffix + "]"
}

func validateMCPServerSpec(spec mcp.ServerSpec) error {
	if strings.TrimSpace(spec.Name) == "" {
		return fmt.Errorf("name is required")
	}
	if spec.URL != "" && spec.Command != "" {
		return fmt.Errorf("set either url or command, not both")
	}
	if spec.URL == "" && spec.Command == "" {
		return fmt.Errorf("url or command is required")
	}
	if spec.URL != "" {
		u, err := url.Parse(spec.URL)
		if err != nil || u.Scheme == "" || u.Host == "" {
			if err != nil {
				return fmt.Errorf("invalid url %q: %w", spec.URL, err)
			}
			return fmt.Errorf("invalid url %q", spec.URL)
		}
	}
	if spec.Command != "" {
		if _, err := exec.LookPath(spec.Command); err != nil {
			return fmt.Errorf("command %q not found in PATH", spec.Command)
		}
	}
	if spec.Cwd != "" {
		st, err := os.Stat(spec.Cwd)
		if err != nil {
			return fmt.Errorf("cwd %q: %w", spec.Cwd, err)
		}
		if !st.IsDir() {
			return fmt.Errorf("cwd %q is not a directory", spec.Cwd)
		}
	}
	for _, env := range spec.Env {
		if !strings.Contains(env, "=") || strings.HasPrefix(env, "=") {
			return fmt.Errorf("invalid env %q; want KEY=VALUE", env)
		}
	}
	if err := validateMCPToolFilter("allow_tools", spec.ToolAllowlist); err != nil {
		return err
	}
	if err := validateMCPToolFilter("deny_tools", spec.ToolDenylist); err != nil {
		return err
	}
	allowed := map[string]bool{}
	for _, name := range spec.ToolAllowlist {
		allowed[strings.TrimSpace(name)] = true
	}
	for _, name := range spec.ToolDenylist {
		name = strings.TrimSpace(name)
		if allowed[name] {
			return fmt.Errorf("tool %q appears in both allow_tools and deny_tools", name)
		}
	}
	return nil
}

func validateMCPToolFilter(field string, names []string) error {
	seen := map[string]bool{}
	for _, raw := range names {
		name := strings.TrimSpace(raw)
		if name == "" {
			return fmt.Errorf("%s values must not be empty", field)
		}
		if seen[name] {
			return fmt.Errorf("%s contains duplicate tool %q", field, name)
		}
		seen[name] = true
	}
	return nil
}

func ensureWritableDir(dir string) error {
	if dir == "" {
		return fmt.Errorf("workspace is empty")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	f, err := os.CreateTemp(dir, ".affent-doctor-*")
	if err != nil {
		return fmt.Errorf("%s is not writable: %w", dir, err)
	}
	name := f.Name()
	if err := f.Close(); err != nil {
		_ = os.Remove(name)
		return fmt.Errorf("close temp file in %s: %w", dir, err)
	}
	if err := os.Remove(name); err != nil {
		return fmt.Errorf("remove temp file in %s: %w", dir, err)
	}
	return nil
}

func doctorMemorySummary(c commonFlags, workspace string) (string, error) {
	coreCap, userCap, err := memoryDefaultsForDoctor(c)
	if err != nil {
		return "", err
	}
	topicCap := c.memoryTopicMaxChars
	if topicCap <= 0 {
		topicCap = memory.DefaultTopicCharLimit
	}
	maxTopics := c.memoryMaxTopics
	if maxTopics <= 0 {
		maxTopics = memory.DefaultMaxTopics
	}
	dir := c.memoryDir
	if dir == "" {
		dir = filepath.Join(workspace, ".affent", "memory")
	} else {
		dir = resolveStorePath(workspace, dir)
	}
	return fmt.Sprintf("enabled dir=%s core=%d user=%d topic=%d max_topics=%d", dir, coreCap, userCap, topicCap, maxTopics), nil
}

func memoryDefaultsForDoctor(c commonFlags) (int, int, error) {
	coreCap, userCap := memory.DefaultCoreCharLimit, memory.DefaultUserCharLimit
	if memCap, usrCap, ok, err := parseMemoryMaxChars(c.memoryMaxChars); err != nil {
		return 0, 0, err
	} else if ok {
		coreCap, userCap = memCap, usrCap
	}
	return coreCap, userCap, nil
}

func executorNeedsDocker(spec string) bool {
	return spec == "sandbox" || strings.HasPrefix(spec, "docker:")
}

func checkDockerAvailable(runner commandRunner) error {
	if runner == nil {
		return fmt.Errorf("docker runner is nil")
	}
	if _, err := runner.Run("docker", "version", "--format", "{{.Server.Version}}"); err != nil {
		return fmt.Errorf("docker unavailable: %w", err)
	}
	return nil
}

func checkDockerContainerRunning(spec string, runner commandRunner) error {
	name := strings.TrimPrefix(spec, "docker:")
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("--executor docker: requires a container id")
	}
	out, err := runner.Run("docker", "inspect", "-f", "{{.State.Running}}", name)
	if err != nil {
		return fmt.Errorf("inspect %s: %w", name, err)
	}
	if strings.TrimSpace(out) != "true" {
		return fmt.Errorf("%s is not running", name)
	}
	return nil
}
