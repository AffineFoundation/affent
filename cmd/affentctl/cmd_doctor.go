package main

import (
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	agent "github.com/affinefoundation/affent/internal/agent"
	"github.com/affinefoundation/affent/internal/mcp"
	"github.com/affinefoundation/affent/internal/memory"
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
	for i, server := range cfg.Servers {
		spec, err := server.serverSpec()
		if err != nil {
			return "", fmt.Errorf("servers[%d]: %w", i, err)
		}
		if err := validateMCPServerSpec(spec); err != nil {
			return "", fmt.Errorf("servers[%d]: %w", i, err)
		}
	}
	return fmt.Sprintf("%d server(s)", len(cfg.Servers)), nil
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
