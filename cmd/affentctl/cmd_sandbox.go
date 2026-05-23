package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const (
	defaultSandboxName         = "affent-sandbox"
	defaultSandboxImage        = "affinefoundation/affent-sandbox:latest"
	defaultRuntimeImage        = "affinefoundation/affent:latest"
	defaultSandboxMemory       = "1g"
	defaultSandboxCPUs         = "2"
	defaultSandboxPIDs         = "512"
	minDockerMemoryBytes       = 128 * 1024 * 1024
	minDockerPIDsLimit         = 64
	defaultSandboxDockerfile   = "docker/sandbox.Dockerfile"
	defaultRuntimeDockerfile   = "docker/affent.Dockerfile"
	defaultSandboxBuildContext = "."

	sandboxLabelManaged   = "affent.sandbox"
	sandboxLabelImage     = "affent.sandbox.image"
	sandboxLabelWorkspace = "affent.sandbox.workspace"
	sandboxLabelMemory    = "affent.sandbox.memory"
	sandboxLabelCPUs      = "affent.sandbox.cpus"
	sandboxLabelPIDsLimit = "affent.sandbox.pids_limit"
	sandboxLabelUser      = "affent.sandbox.user"
)

var (
	sandboxDockerCommandTimeout = 60 * time.Second
	sandboxDockerBuildTimeout   = 20 * time.Minute
	runtimeDockerRunTimeout     = 30 * time.Minute
)

const sandboxInspectTemplate = "{{.State.Running}}\n{{index .Config.Labels \"" + sandboxLabelManaged + "\"}}\n{{index .Config.Labels \"" + sandboxLabelImage + "\"}}\n{{index .Config.Labels \"" + sandboxLabelWorkspace + "\"}}\n{{index .Config.Labels \"" + sandboxLabelMemory + "\"}}\n{{index .Config.Labels \"" + sandboxLabelCPUs + "\"}}\n{{index .Config.Labels \"" + sandboxLabelPIDsLimit + "\"}}\n{{index .Config.Labels \"" + sandboxLabelUser + "\"}}"

const sandboxStatusTemplate = "{{.State.Status}}\n{{.State.Running}}\n{{index .Config.Labels \"" + sandboxLabelManaged + "\"}}\n{{index .Config.Labels \"" + sandboxLabelImage + "\"}}\n{{index .Config.Labels \"" + sandboxLabelWorkspace + "\"}}\n{{index .Config.Labels \"" + sandboxLabelMemory + "\"}}\n{{index .Config.Labels \"" + sandboxLabelCPUs + "\"}}\n{{index .Config.Labels \"" + sandboxLabelPIDsLimit + "\"}}\n{{index .Config.Labels \"" + sandboxLabelUser + "\"}}\n{{.HostConfig.Memory}}\n{{.HostConfig.MemorySwap}}\n{{.HostConfig.PidsLimit}}\n{{.Config.WorkingDir}}"

type commandRunner interface {
	Run(name string, args ...string) (string, error)
}

type osCommandRunner struct {
	timeout      time.Duration
	buildTimeout time.Duration
	stdout       io.Writer
	stderr       io.Writer
	streamAll    bool
	streamBuild  bool
}

func (r osCommandRunner) Run(name string, args ...string) (string, error) {
	isDockerBuild := name == "docker" && len(args) > 0 && args[0] == "build"
	timeout := sandboxDockerCommandTimeout
	if r.timeout != 0 {
		timeout = r.timeout
	}
	if isDockerBuild && r.buildTimeout != 0 {
		timeout = r.buildTimeout
	}
	var (
		ctx    context.Context
		cancel context.CancelFunc
	)
	if timeout > 0 {
		ctx, cancel = context.WithTimeout(context.Background(), timeout)
	} else {
		ctx, cancel = context.WithCancel(context.Background())
	}
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout, stderr bytes.Buffer
	streamOutput := r.streamAll || (r.streamBuild && isDockerBuild)
	if streamOutput && r.stdout != nil {
		cmd.Stdout = io.MultiWriter(r.stdout, &stdout)
	} else {
		cmd.Stdout = &stdout
	}
	if streamOutput && r.stderr != nil {
		cmd.Stderr = io.MultiWriter(r.stderr, &stderr)
	} else {
		cmd.Stderr = &stderr
	}
	err := cmd.Run()
	out := strings.TrimSpace(stdout.String())
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return out, fmt.Errorf("%s: %w", msg, ctx.Err())
		}
		return out, fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), ctx.Err())
	}
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return out, fmt.Errorf("%s: %w", msg, err)
		}
		return out, err
	}
	return out, nil
}

func sandboxCmd(args []string) int {
	if len(args) == 0 {
		sandboxUsage(os.Stderr)
		return exitUsage
	}
	switch args[0] {
	case "build":
		return sandboxBuildCmd(args[1:], osCommandRunner{buildTimeout: sandboxDockerBuildTimeout, stdout: os.Stdout, stderr: os.Stderr, streamBuild: true}, os.Stdout, os.Stderr)
	case "start":
		return sandboxStartCmd(args[1:], osCommandRunner{buildTimeout: sandboxDockerBuildTimeout, stdout: os.Stdout, stderr: os.Stderr, streamBuild: true}, os.Stdout, os.Stderr)
	case "status":
		return sandboxStatusCmd(args[1:], osCommandRunner{}, os.Stdout, os.Stderr)
	case "stop":
		return sandboxStopCmd(args[1:], osCommandRunner{}, os.Stdout, os.Stderr)
	case "-h", "--help", "help":
		sandboxUsage(os.Stderr)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown sandbox subcommand: %s\n\n", args[0])
		sandboxUsage(os.Stderr)
		return exitUsage
	}
}

func sandboxUsage(w io.Writer) {
	fmt.Fprintln(w, `usage: affentctl sandbox <command> [flags]

Commands:
  build      build the project-owned sandbox image locally
  start      start or reuse a Docker tool container with persistent storage
  status     show the sandbox container status and resource limits
  stop       stop the sandbox container; add --remove to delete it

Run 'affentctl sandbox <command> -h' for flags.`)
}

func imageCmd(args []string) int {
	if len(args) == 0 {
		imageUsage(os.Stderr)
		return exitUsage
	}
	switch args[0] {
	case "build":
		return imageBuildCmd(args[1:], osCommandRunner{buildTimeout: sandboxDockerBuildTimeout, stdout: os.Stdout, stderr: os.Stderr, streamBuild: true}, os.Stdout, os.Stderr)
	case "run":
		return imageRunCmd(args[1:], osCommandRunner{stdout: os.Stdout, stderr: os.Stderr, streamAll: true}, os.Stdout, os.Stderr)
	case "-h", "--help", "help":
		imageUsage(os.Stderr)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown image subcommand: %s\n\n", args[0])
		imageUsage(os.Stderr)
		return exitUsage
	}
}

func imageUsage(w io.Writer) {
	fmt.Fprintln(w, `usage: affentctl image <command> [flags]

Commands:
  build      build the Affent runtime image with binaries and standard tools
  run        run the Affent runtime image with persistent storage and limits

Run 'affentctl image <command> -h' for flags.`)
}

type dockerBuildOptions struct {
	Image      string
	Dockerfile string
	Context    string
	Memory     string
	NoCache    bool
}

func sandboxBuildCmd(args []string, runner commandRunner, stdout, stderr io.Writer) int {
	opts := defaultSandboxBuildOptions()
	fs := flag.NewFlagSet("sandbox build", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&opts.Image, "image", opts.Image, "Docker image tag to build")
	fs.StringVar(&opts.Dockerfile, "dockerfile", opts.Dockerfile, "sandbox Dockerfile path")
	fs.StringVar(&opts.Context, "context", opts.Context, "Docker build context path")
	fs.StringVar(&opts.Memory, "memory", opts.Memory, "Docker build memory limit")
	fs.BoolVar(&opts.NoCache, "no-cache", false, "build without using Docker layer cache")
	fs.Usage = func() {
		fmt.Fprintln(stderr, `usage: affentctl sandbox build [flags]

Builds the project-owned sandbox image used by affentctl sandbox start. The
build is memory-limited by default so a failed or heavy image build is less
likely to make the host unusable.`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	opts.Image = strings.TrimSpace(opts.Image)
	opts.Dockerfile = strings.TrimSpace(opts.Dockerfile)
	opts.Context = strings.TrimSpace(opts.Context)
	opts.Memory = strings.TrimSpace(opts.Memory)
	if err := buildDockerImage(opts, runner); err != nil {
		fmt.Fprintf(stderr, "sandbox build: %v\n", err)
		return exitRuntime
	}
	fmt.Fprintf(stdout, "image: %s\n", strings.TrimSpace(opts.Image))
	return 0
}

func imageBuildCmd(args []string, runner commandRunner, stdout, stderr io.Writer) int {
	opts := defaultRuntimeBuildOptions()
	fs := flag.NewFlagSet("image build", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&opts.Image, "image", opts.Image, "Docker image tag to build")
	fs.StringVar(&opts.Dockerfile, "dockerfile", opts.Dockerfile, "runtime Dockerfile path")
	fs.StringVar(&opts.Context, "context", opts.Context, "Docker build context path")
	fs.StringVar(&opts.Memory, "memory", opts.Memory, "Docker build memory limit")
	fs.BoolVar(&opts.NoCache, "no-cache", false, "build without using Docker layer cache")
	fs.Usage = func() {
		fmt.Fprintln(stderr, `usage: affentctl image build [flags]

Builds the Affent runtime image, including affentctl, affentserve, affenteval,
and the standard tool package manifest shared with the sandbox image. The build
is memory-limited by default.`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	opts.Image = strings.TrimSpace(opts.Image)
	opts.Dockerfile = strings.TrimSpace(opts.Dockerfile)
	opts.Context = strings.TrimSpace(opts.Context)
	opts.Memory = strings.TrimSpace(opts.Memory)
	if err := buildDockerImage(opts, runner); err != nil {
		fmt.Fprintf(stderr, "image build: %v\n", err)
		return exitRuntime
	}
	fmt.Fprintf(stdout, "image: %s\n", opts.Image)
	return 0
}

func defaultSandboxBuildOptions() dockerBuildOptions {
	return dockerBuildOptions{
		Image:      defaultSandboxImage,
		Dockerfile: defaultSandboxDockerfile,
		Context:    defaultSandboxBuildContext,
		Memory:     defaultSandboxMemory,
	}
}

func defaultRuntimeBuildOptions() dockerBuildOptions {
	return dockerBuildOptions{
		Image:      defaultRuntimeImage,
		Dockerfile: defaultRuntimeDockerfile,
		Context:    defaultSandboxBuildContext,
		Memory:     defaultSandboxMemory,
	}
}

type stringListFlag []string

func (f *stringListFlag) String() string {
	return strings.Join(*f, ",")
}

func (f *stringListFlag) Set(v string) error {
	if strings.TrimSpace(v) == "" {
		return errors.New("value must not be empty")
	}
	*f = append(*f, v)
	return nil
}

type runtimeRunOptions struct {
	Image     string
	Workspace string
	Memory    string
	CPUs      string
	PIDsLimit string
	User      string
	Timeout   time.Duration
	TTY       bool
	Remove    bool
	Env       []string
	Publish   []string
	Command   []string
}

func imageRunCmd(args []string, runner commandRunner, _ io.Writer, stderr io.Writer) int {
	opts := defaultRuntimeRunOptions(defaultRuntimeWorkspace())
	env := stringListFlag(opts.Env)
	publish := stringListFlag(opts.Publish)
	fs := flag.NewFlagSet("image run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&opts.Image, "image", opts.Image, "Docker image to run")
	fs.StringVar(&opts.Workspace, "workspace", opts.Workspace, "persistent host workspace mounted at /workspace")
	fs.StringVar(&opts.Memory, "memory", opts.Memory, "Docker memory limit")
	fs.StringVar(&opts.CPUs, "cpus", opts.CPUs, "Docker CPU limit")
	fs.StringVar(&opts.PIDsLimit, "pids-limit", opts.PIDsLimit, "Docker process-count limit")
	fs.StringVar(&opts.User, "user", opts.User, "Docker user UID:GID; empty uses the image default")
	fs.DurationVar(&opts.Timeout, "timeout", opts.Timeout, "Docker run timeout; 0 disables the wrapper timeout")
	fs.BoolVar(&opts.TTY, "tty", false, "allocate a TTY for interactive commands")
	fs.BoolVar(&opts.Remove, "rm", true, "remove the container after the command exits")
	fs.Var(&env, "env", "extra environment variable to pass through as KEY=VALUE; repeatable")
	fs.Var(&publish, "publish", "publish a container port, e.g. 7777:7777; repeatable")
	fs.Usage = func() {
		fmt.Fprintln(stderr, `usage: affentctl image run [flags] [-- command args...]

Runs the Affent runtime image with default memory/process limits and a durable
host workspace mounted at /workspace. With no command, runs affentctl --help.`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	opts.Env = []string(env)
	opts.Publish = []string(publish)
	opts.Command = fs.Args()
	if len(opts.Command) == 0 {
		opts.Command = []string{"affentctl", "--help"}
	}
	if osRunner, ok := runner.(osCommandRunner); ok {
		if opts.Timeout == 0 {
			osRunner.timeout = -1
		} else {
			osRunner.timeout = opts.Timeout
		}
		runner = osRunner
	}
	if err := runRuntimeImage(opts, runner); err != nil {
		fmt.Fprintf(stderr, "image run: %v\n", err)
		return exitRuntime
	}
	return 0
}

func defaultRuntimeRunOptions(workspace string) runtimeRunOptions {
	return runtimeRunOptions{
		Image:     defaultRuntimeImage,
		Workspace: workspace,
		Memory:    defaultSandboxMemory,
		CPUs:      defaultSandboxCPUs,
		PIDsLimit: defaultSandboxPIDs,
		User:      defaultSandboxUser(),
		Timeout:   runtimeDockerRunTimeout,
		Remove:    true,
	}
}

func defaultRuntimeWorkspace() string {
	return filepath.Join(defaultAffentDataRoot(), "affent", "runtime", "workspace")
}

type sandboxStartOptions struct {
	Name      string
	Image     string
	Workspace string
	Memory    string
	CPUs      string
	PIDsLimit string
	User      string
	Replace   bool
	PrintEnv  bool
}

func sandboxStartCmd(args []string, runner commandRunner, stdout, stderr io.Writer) int {
	opts := defaultSandboxStartOptions(defaultSandboxWorkspace())
	fs := flag.NewFlagSet("sandbox start", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&opts.Name, "name", opts.Name, "Docker container name")
	fs.StringVar(&opts.Image, "image", opts.Image, "Docker image to run")
	fs.StringVar(&opts.Workspace, "workspace", opts.Workspace, "persistent workspace path mounted into the container at the same absolute path")
	fs.StringVar(&opts.Memory, "memory", opts.Memory, "Docker memory limit")
	fs.StringVar(&opts.CPUs, "cpus", opts.CPUs, "Docker CPU limit")
	fs.StringVar(&opts.PIDsLimit, "pids-limit", opts.PIDsLimit, "Docker process-count limit")
	fs.StringVar(&opts.User, "user", opts.User, "Docker user UID:GID; empty uses the image default")
	fs.BoolVar(&opts.Replace, "replace", false, "remove any existing container with this name before starting")
	fs.BoolVar(&opts.PrintEnv, "print-env", false, "print shell exports only, for eval: eval \"$(affentctl sandbox start --print-env)\"")
	fs.Usage = func() {
		fmt.Fprintln(stderr, `usage: affentctl sandbox start [flags]

Starts a long-lived Docker container for Affent tools. The workspace is a
persistent host directory mounted at the same absolute path inside the
container, so affentctl state, memory, file tools, and shell tools all see one
path. The container is memory-limited by default.`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if err := startSandbox(opts, runner); err != nil {
		fmt.Fprintf(stderr, "sandbox start: %v\n", err)
		return exitRuntime
	}
	printSandboxStartResult(stdout, opts)
	return 0
}

func sandboxStatusCmd(args []string, runner commandRunner, stdout, stderr io.Writer) int {
	name := defaultSandboxName
	fs := flag.NewFlagSet("sandbox status", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&name, "name", name, "Docker container name")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: affentctl sandbox status [--name NAME]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	status, err := inspectSandboxStatus(strings.TrimSpace(name), runner)
	if err != nil {
		fmt.Fprintf(stderr, "sandbox status: %v\n", err)
		return exitRuntime
	}
	printSandboxStatus(stdout, status)
	return 0
}

func sandboxStopCmd(args []string, runner commandRunner, _ io.Writer, stderr io.Writer) int {
	name := defaultSandboxName
	remove := false
	fs := flag.NewFlagSet("sandbox stop", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&name, "name", name, "Docker container name")
	fs.BoolVar(&remove, "remove", false, "remove the container instead of only stopping it")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: affentctl sandbox stop [--name NAME] [--remove]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if err := stopSandbox(strings.TrimSpace(name), remove, runner); err != nil {
		fmt.Fprintf(stderr, "sandbox stop: %v\n", err)
		return exitRuntime
	}
	return 0
}

func defaultSandboxStartOptions(workspace string) sandboxStartOptions {
	return sandboxStartOptions{
		Name:      defaultSandboxName,
		Image:     defaultSandboxImage,
		Workspace: workspace,
		Memory:    defaultSandboxMemory,
		CPUs:      defaultSandboxCPUs,
		PIDsLimit: defaultSandboxPIDs,
		User:      defaultSandboxUser(),
	}
}

func defaultSandboxWorkspace() string {
	return filepath.Join(defaultAffentDataRoot(), "affent", "sandbox", "workspace")
}

func defaultAffentDataRoot() string {
	base := os.Getenv("XDG_DATA_HOME")
	if base != "" {
		return base
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" && filepath.Clean(home) != string(os.PathSeparator) {
		return filepath.Join(home, ".local", "share")
	}
	return "."
}

func defaultSandboxUser() string {
	u, err := user.Current()
	if err != nil || u == nil {
		return ""
	}
	uid := strings.TrimSpace(u.Uid)
	gid := strings.TrimSpace(u.Gid)
	if uid == "" || gid == "" {
		return ""
	}
	return uid + ":" + gid
}

func buildDockerImage(opts dockerBuildOptions, runner commandRunner) error {
	if runner == nil {
		return errors.New("docker runner is nil")
	}
	opts.Image = strings.TrimSpace(opts.Image)
	opts.Dockerfile = strings.TrimSpace(opts.Dockerfile)
	opts.Context = strings.TrimSpace(opts.Context)
	opts.Memory = strings.TrimSpace(opts.Memory)
	if opts.Image == "" {
		return errors.New("--image is required")
	}
	if err := validateDockerImageRef("--image", opts.Image); err != nil {
		return err
	}
	if opts.Dockerfile == "" {
		return errors.New("--dockerfile is required")
	}
	if opts.Context == "" {
		return errors.New("--context is required")
	}
	if opts.Memory == "" {
		return errors.New("--memory is required; use an explicit Docker build memory limit such as 1g")
	}
	if err := validateDockerMemoryLimit("--memory", opts.Memory); err != nil {
		return err
	}
	if err := resolveDockerBuildPaths(&opts); err != nil {
		return err
	}
	buildArgs := []string{
		"build",
		"--memory", opts.Memory,
		"--memory-swap", opts.Memory,
		"-f", opts.Dockerfile,
		"-t", opts.Image,
	}
	if opts.NoCache {
		buildArgs = append(buildArgs, "--no-cache")
	}
	buildArgs = append(buildArgs, opts.Context)
	_, err := runner.Run("docker", buildArgs...)
	return err
}

func resolveDockerBuildPaths(opts *dockerBuildOptions) error {
	if err := validateBuildPath("--dockerfile", opts.Dockerfile, false); err != nil {
		if filepath.IsAbs(opts.Dockerfile) || !errors.Is(err, os.ErrNotExist) {
			return err
		}
		dockerfile, contextDir, ok, findErr := findDockerBuildSource(opts.Dockerfile)
		if findErr != nil {
			return findErr
		}
		if !ok {
			return err
		}
		opts.Dockerfile = dockerfile
		if opts.Context == defaultSandboxBuildContext {
			opts.Context = contextDir
		}
	}
	if err := validateBuildPath("--context", opts.Context, true); err != nil {
		return err
	}
	return nil
}

func runRuntimeImage(opts runtimeRunOptions, runner commandRunner) error {
	if runner == nil {
		return errors.New("docker runner is nil")
	}
	opts.Image = strings.TrimSpace(opts.Image)
	opts.Workspace = strings.TrimSpace(opts.Workspace)
	opts.Memory = strings.TrimSpace(opts.Memory)
	opts.CPUs = strings.TrimSpace(opts.CPUs)
	opts.PIDsLimit = strings.TrimSpace(opts.PIDsLimit)
	opts.User = strings.TrimSpace(opts.User)
	if opts.Image == "" {
		return errors.New("--image is required")
	}
	if err := validateDockerImageRef("--image", opts.Image); err != nil {
		return err
	}
	if opts.Workspace == "" {
		return errors.New("--workspace is required")
	}
	if opts.Memory == "" {
		return errors.New("--memory is required; use an explicit Docker memory limit such as 1g")
	}
	if opts.CPUs == "" {
		return errors.New("--cpus is required; use an explicit Docker CPU limit such as 2")
	}
	if opts.PIDsLimit == "" {
		return errors.New("--pids-limit is required; use an explicit process limit such as 512")
	}
	if err := validateDockerMemoryLimit("--memory", opts.Memory); err != nil {
		return err
	}
	if err := validateDockerCPUs("--cpus", opts.CPUs); err != nil {
		return err
	}
	if err := validateDockerPIDsLimit("--pids-limit", opts.PIDsLimit); err != nil {
		return err
	}
	if err := validateDockerUser("--user", opts.User); err != nil {
		return err
	}
	if opts.Timeout < 0 {
		return errors.New("--timeout must be zero or a positive duration")
	}
	if err := validateRuntimeCommand(opts.Command); err != nil {
		return err
	}
	envs, err := runtimeForwardEnv(opts.Env)
	if err != nil {
		return err
	}
	publishValues := make([]string, 0, len(opts.Publish))
	for _, p := range opts.Publish {
		p, err := validateDockerPublish(p)
		if err != nil {
			return err
		}
		publishValues = append(publishValues, p)
	}
	workspace, err := filepath.Abs(opts.Workspace)
	if err != nil {
		return fmt.Errorf("resolve workspace: %w", err)
	}
	opts.Workspace = workspace
	if err := os.MkdirAll(opts.Workspace, 0o755); err != nil {
		return fmt.Errorf("mkdir workspace %s: %w", opts.Workspace, err)
	}
	for _, dir := range runtimePersistentDirs(opts.Workspace) {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("mkdir runtime directory %s: %w", dir, err)
		}
	}
	if err := ensureDefaultRuntimeImage(opts, runner); err != nil {
		return err
	}

	runArgs := []string{"run"}
	if opts.Remove {
		runArgs = append(runArgs, "--rm")
	}
	runArgs = append(runArgs,
		"-i",
		"--init",
		"--memory", opts.Memory,
		"--memory-swap", opts.Memory,
		"--cpus", opts.CPUs,
		"--pids-limit", opts.PIDsLimit,
		"-v", opts.Workspace+":/workspace",
		"-w", "/workspace",
	)
	if opts.TTY {
		runArgs = append(runArgs, "-t")
	}
	if opts.User != "" {
		runArgs = append(runArgs, "--user", opts.User)
	}
	runArgs = append(runArgs, runtimePersistentEnv(opts.Memory, opts.CPUs)...)
	for _, env := range envs {
		runArgs = append(runArgs, "-e", env)
	}
	for _, p := range publishValues {
		runArgs = append(runArgs, "-p", p)
	}
	runArgs = append(runArgs, opts.Image)
	runArgs = append(runArgs, opts.Command...)
	_, err = runner.Run("docker", runArgs...)
	return err
}

func validateRuntimeCommand(command []string) error {
	if len(command) == 0 {
		return errors.New("runtime command is required")
	}
	if strings.TrimSpace(command[0]) == "" {
		return errors.New("runtime command executable is required")
	}
	return nil
}

func runtimePersistentDirs(hostWorkspace string) []string {
	return []string{
		filepath.Join(hostWorkspace, ".home"),
		filepath.Join(hostWorkspace, ".cache"),
		filepath.Join(hostWorkspace, ".cache", "go-build"),
		filepath.Join(hostWorkspace, ".cache", "go-mod"),
		filepath.Join(hostWorkspace, ".cache", "npm"),
		filepath.Join(hostWorkspace, ".cache", "pip"),
	}
}

func runtimePersistentEnv(memory, cpus string) []string {
	env := []string{
		"-e", "HOME=/workspace/.home",
		"-e", "XDG_CACHE_HOME=/workspace/.cache",
		"-e", "GOCACHE=/workspace/.cache/go-build",
		"-e", "GOMODCACHE=/workspace/.cache/go-mod",
		"-e", "NPM_CONFIG_CACHE=/workspace/.cache/npm",
		"-e", "PIP_CACHE_DIR=/workspace/.cache/pip",
	}
	if gomem := sandboxGoMemLimit(memory); gomem != "" {
		env = append(env, "-e", "GOMEMLIMIT="+gomem)
	}
	if gomax := sandboxGoMaxProcs(cpus); gomax != "" {
		env = append(env, "-e", "GOMAXPROCS="+gomax)
	}
	return env
}

func runtimeForwardEnv(extra []string) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	addExplicit := func(kv string) error {
		if strings.TrimSpace(kv) == "" {
			return errors.New("--env values must not be empty")
		}
		i := strings.Index(kv, "=")
		if i < 0 {
			return fmt.Errorf("invalid --env %q: expected KEY=VALUE", kv)
		}
		name := kv[:i]
		if name == "" {
			return fmt.Errorf("invalid --env %q: missing variable name", kv)
		}
		if !validEnvName(name) {
			return fmt.Errorf("invalid --env %q: variable name must match [A-Za-z_][A-Za-z0-9_]*", kv)
		}
		if seen[name] {
			return fmt.Errorf("invalid --env %q: duplicate variable %s", kv, name)
		}
		seen[name] = true
		out = append(out, kv)
		return nil
	}
	addHost := func(name, value string) {
		if seen[name] {
			return
		}
		seen[name] = true
		out = append(out, name+"="+value)
	}
	for _, kv := range extra {
		if err := addExplicit(kv); err != nil {
			return nil, err
		}
	}
	for _, name := range runtimeForwardEnvNames() {
		if v, ok := os.LookupEnv(name); ok {
			addHost(name, v)
		}
	}
	return out, nil
}

func runtimeForwardEnvNames() []string {
	return []string{
		"AFFENTCTL_BASE_URL",
		"AFFENTCTL_API_KEY",
		"AFFENTCTL_MODEL",
		"AFFENTCTL_SUBAGENT",
		"AFFENTCTL_SUBAGENT_MAX_DEPTH",
		"AFFENTCTL_FOCUSED_TASKS",
		"AFFENTCTL_TEMPERATURE",
		"AFFENTCTL_TOP_P",
		"AFFENTCTL_MAX_TOKENS",
		"AFFENTSERVE_BASE_URL",
		"AFFENTSERVE_API_KEY",
		"AFFENTSERVE_MODEL",
		"AFFENTSERVE_AUTH_TOKEN",
		"AFFENTSERVE_MEMORY",
		"AFFENTSERVE_BUILTINS",
		"AFFENTSERVE_SUBAGENT",
		"AFFENTSERVE_SUBAGENT_MAX_DEPTH",
		"AFFENTSERVE_FOCUSED_TASKS",
		"AFFENTSERVE_SESSION_RETENTION",
		"AFFENTSERVE_TEMPERATURE",
		"AFFENTSERVE_TOP_P",
		"AFFENTSERVE_MAX_TOKENS",
		"TAVILY_API_KEY",
	}
}

func validEnvName(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		if r == '_' || ('A' <= r && r <= 'Z') || ('a' <= r && r <= 'z') || (i > 0 && '0' <= r && r <= '9') {
			continue
		}
		return false
	}
	return true
}

func findDockerBuildSource(dockerfileRel string) (dockerfile, contextDir string, ok bool, err error) {
	dockerfileRel = strings.TrimSpace(dockerfileRel)
	if dockerfileRel == "" {
		return "", "", false, errors.New("dockerfile path is required")
	}
	wd, err := os.Getwd()
	if err != nil {
		return "", "", false, err
	}
	for {
		candidate := filepath.Join(wd, dockerfileRel)
		if _, statErr := os.Stat(candidate); statErr == nil {
			return candidate, wd, true, nil
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return "", "", false, fmt.Errorf("stat %s: %w", candidate, statErr)
		}
		parent := filepath.Dir(wd)
		if parent == wd {
			return "", "", false, nil
		}
		wd = parent
	}
}

func findSandboxBuildSource() (dockerfile, contextDir string, ok bool, err error) {
	return findDockerBuildSource(defaultSandboxDockerfile)
}

func findRuntimeBuildSource() (dockerfile, contextDir string, ok bool, err error) {
	return findDockerBuildSource(defaultRuntimeDockerfile)
}

func ensureDefaultRuntimeImage(opts runtimeRunOptions, runner commandRunner) error {
	if opts.Image != defaultRuntimeImage {
		return nil
	}
	dockerfile, contextDir, ok, err := findRuntimeBuildSource()
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	if _, err := runner.Run("docker", "image", "inspect", opts.Image); err == nil {
		return nil
	}
	return buildDockerImage(dockerBuildOptions{
		Image:      opts.Image,
		Dockerfile: dockerfile,
		Context:    contextDir,
		Memory:     opts.Memory,
	}, runner)
}

func ensureDefaultSandboxImage(opts sandboxStartOptions, runner commandRunner) error {
	if opts.Image != defaultSandboxImage {
		return nil
	}
	dockerfile, contextDir, ok, err := findSandboxBuildSource()
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	if _, err := runner.Run("docker", "image", "inspect", opts.Image); err == nil {
		return nil
	}
	return buildDockerImage(dockerBuildOptions{
		Image:      opts.Image,
		Dockerfile: dockerfile,
		Context:    contextDir,
		Memory:     opts.Memory,
	}, runner)
}

func startSandbox(opts sandboxStartOptions, runner commandRunner) error {
	if runner == nil {
		return errors.New("docker runner is nil")
	}
	opts.Name = strings.TrimSpace(opts.Name)
	opts.Image = strings.TrimSpace(opts.Image)
	opts.Workspace = strings.TrimSpace(opts.Workspace)
	opts.Memory = strings.TrimSpace(opts.Memory)
	opts.CPUs = strings.TrimSpace(opts.CPUs)
	opts.PIDsLimit = strings.TrimSpace(opts.PIDsLimit)
	opts.User = strings.TrimSpace(opts.User)
	if opts.Name == "" {
		return errors.New("--name is required")
	}
	if err := validateDockerContainerName("--name", opts.Name); err != nil {
		return err
	}
	if opts.Image == "" {
		return errors.New("--image is required")
	}
	if err := validateDockerImageRef("--image", opts.Image); err != nil {
		return err
	}
	if opts.Workspace == "" {
		return errors.New("--workspace is required")
	}
	if opts.Memory == "" {
		return errors.New("--memory is required; use an explicit Docker memory limit such as 1g")
	}
	if opts.CPUs == "" {
		return errors.New("--cpus is required; use an explicit Docker CPU limit such as 2")
	}
	if opts.PIDsLimit == "" {
		return errors.New("--pids-limit is required; use an explicit process limit such as 512")
	}
	if err := validateDockerMemoryLimit("--memory", opts.Memory); err != nil {
		return err
	}
	if err := validateDockerCPUs("--cpus", opts.CPUs); err != nil {
		return err
	}
	if err := validateDockerPIDsLimit("--pids-limit", opts.PIDsLimit); err != nil {
		return err
	}
	if err := validateDockerUser("--user", opts.User); err != nil {
		return err
	}
	workspace, err := filepath.Abs(opts.Workspace)
	if err != nil {
		return fmt.Errorf("resolve workspace: %w", err)
	}
	opts.Workspace = workspace
	if err := os.MkdirAll(opts.Workspace, 0o755); err != nil {
		return fmt.Errorf("mkdir workspace %s: %w", opts.Workspace, err)
	}
	for _, dir := range sandboxPersistentDirs(opts.Workspace) {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("mkdir sandbox directory %s: %w", dir, err)
		}
	}

	if opts.Replace {
		_, _ = runner.Run("docker", "rm", "-f", opts.Name)
	}
	if out, err := runner.Run("docker", "inspect", "-f", sandboxInspectTemplate, opts.Name); err == nil {
		running, err := validateExistingSandbox(out, opts)
		if err != nil {
			return err
		}
		if running {
			return nil
		}
		_, err = runner.Run("docker", "start", opts.Name)
		return err
	}
	if err := ensureDefaultSandboxImage(opts, runner); err != nil {
		return err
	}

	runArgs := []string{
		"run", "-d",
		"--name", opts.Name,
		"--init",
		"--label", sandboxLabelManaged + "=true",
		"--label", sandboxLabelImage + "=" + opts.Image,
		"--label", sandboxLabelWorkspace + "=" + opts.Workspace,
		"--label", sandboxLabelMemory + "=" + opts.Memory,
		"--label", sandboxLabelCPUs + "=" + opts.CPUs,
		"--label", sandboxLabelPIDsLimit + "=" + opts.PIDsLimit,
		"--label", sandboxLabelUser + "=" + opts.User,
		"--memory", opts.Memory,
		"--memory-swap", opts.Memory,
		"--cpus", opts.CPUs,
		"--pids-limit", opts.PIDsLimit,
		"-v", opts.Workspace + ":" + opts.Workspace,
		"-w", opts.Workspace,
	}
	if opts.User != "" {
		runArgs = append(runArgs, "--user", opts.User)
	}
	runArgs = append(runArgs, sandboxPersistentEnv(opts.Workspace, opts.Memory, opts.CPUs)...)
	runArgs = append(runArgs,
		opts.Image,
		"sleep", "infinity",
	)
	_, err = runner.Run("docker", runArgs...)
	return err
}

func sandboxPersistentDirs(workspace string) []string {
	return []string{
		filepath.Join(workspace, ".home"),
		filepath.Join(workspace, ".cache"),
		filepath.Join(workspace, ".cache", "go-build"),
		filepath.Join(workspace, ".cache", "go-mod"),
		filepath.Join(workspace, ".cache", "npm"),
		filepath.Join(workspace, ".cache", "pip"),
	}
}

func sandboxPersistentEnv(workspace, memory, cpus string) []string {
	env := []string{
		"-e", "HOME=" + filepath.Join(workspace, ".home"),
		"-e", "XDG_CACHE_HOME=" + filepath.Join(workspace, ".cache"),
		"-e", "GOCACHE=" + filepath.Join(workspace, ".cache", "go-build"),
		"-e", "GOMODCACHE=" + filepath.Join(workspace, ".cache", "go-mod"),
		"-e", "NPM_CONFIG_CACHE=" + filepath.Join(workspace, ".cache", "npm"),
		"-e", "PIP_CACHE_DIR=" + filepath.Join(workspace, ".cache", "pip"),
	}
	if gomem := sandboxGoMemLimit(memory); gomem != "" {
		env = append(env, "-e", "GOMEMLIMIT="+gomem)
	}
	if gomax := sandboxGoMaxProcs(cpus); gomax != "" {
		env = append(env, "-e", "GOMAXPROCS="+gomax)
	}
	return env
}

func sandboxGoMemLimit(memory string) string {
	bytes, ok := parseDockerMemoryBytes(memory)
	if !ok || bytes <= 0 {
		return ""
	}
	mib := bytes * 3 / 4 / (1024 * 1024)
	if mib < 64 {
		return ""
	}
	return strconv.FormatInt(mib, 10) + "MiB"
}

func sandboxGoMaxProcs(cpus string) string {
	f, err := strconv.ParseFloat(strings.TrimSpace(cpus), 64)
	if err != nil || f <= 0 {
		return ""
	}
	n := int(math.Ceil(f))
	if n < 1 {
		n = 1
	}
	return strconv.Itoa(n)
}

func parseDockerMemoryBytes(raw string) (int64, bool) {
	s := strings.TrimSpace(strings.ToLower(raw))
	if s == "" {
		return 0, false
	}
	mult := int64(1)
	switch {
	case strings.HasSuffix(s, "gib"):
		mult = 1024 * 1024 * 1024
		s = strings.TrimSuffix(s, "gib")
	case strings.HasSuffix(s, "gb"):
		mult = 1000 * 1000 * 1000
		s = strings.TrimSuffix(s, "gb")
	case strings.HasSuffix(s, "g"):
		mult = 1024 * 1024 * 1024
		s = strings.TrimSuffix(s, "g")
	case strings.HasSuffix(s, "mib"):
		mult = 1024 * 1024
		s = strings.TrimSuffix(s, "mib")
	case strings.HasSuffix(s, "mb"):
		mult = 1000 * 1000
		s = strings.TrimSuffix(s, "mb")
	case strings.HasSuffix(s, "m"):
		mult = 1024 * 1024
		s = strings.TrimSuffix(s, "m")
	case strings.HasSuffix(s, "kib"):
		mult = 1024
		s = strings.TrimSuffix(s, "kib")
	case strings.HasSuffix(s, "kb"):
		mult = 1000
		s = strings.TrimSuffix(s, "kb")
	case strings.HasSuffix(s, "k"):
		mult = 1024
		s = strings.TrimSuffix(s, "k")
	case strings.HasSuffix(s, "b"):
		s = strings.TrimSuffix(s, "b")
	}
	n, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || n <= 0 {
		return 0, false
	}
	if n > float64(math.MaxInt64)/float64(mult) {
		return 0, false
	}
	return int64(n * float64(mult)), true
}

func validateDockerMemoryLimit(name, raw string) error {
	bytes, ok := parseDockerMemoryBytes(raw)
	if !ok || bytes <= 0 {
		return fmt.Errorf("%s must be a positive Docker memory limit such as 1g", name)
	}
	if bytes < minDockerMemoryBytes {
		return fmt.Errorf("%s must be at least 128m for Affent tool/runtime containers", name)
	}
	return nil
}

func validateDockerImageRef(name, raw string) error {
	ref := strings.TrimSpace(raw)
	if ref == "" {
		return fmt.Errorf("%s is required", name)
	}
	if strings.HasPrefix(ref, "-") {
		return fmt.Errorf("%s must not start with '-'", name)
	}
	for _, r := range ref {
		if unicode.IsSpace(r) || r < 0x20 || r == 0x7f {
			return fmt.Errorf("%s must not contain whitespace or control characters", name)
		}
	}
	return nil
}

func validateBuildPath(name, path string, wantDir bool) error {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%s path %q does not exist: %w", name, path, os.ErrNotExist)
		}
		return fmt.Errorf("stat %s path %q: %w", name, path, err)
	}
	if wantDir {
		if !info.IsDir() {
			return fmt.Errorf("%s path %q must be a directory", name, path)
		}
		return nil
	}
	if info.IsDir() {
		return fmt.Errorf("%s path %q must be a file", name, path)
	}
	return nil
}

func validateDockerCPUs(name, raw string) error {
	f, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) || f <= 0 {
		return fmt.Errorf("%s must be a positive Docker CPU limit such as 2 or 1.5", name)
	}
	return nil
}

func validateDockerPIDsLimit(name, raw string) error {
	n, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || n <= 0 {
		return fmt.Errorf("%s must be a positive integer process limit such as 512", name)
	}
	if n < minDockerPIDsLimit {
		return fmt.Errorf("%s must be at least %d for Affent tool/runtime containers", name, minDockerPIDsLimit)
	}
	return nil
}

func validateDockerContainerName(name, raw string) error {
	containerName := strings.TrimSpace(raw)
	if containerName == "" {
		return fmt.Errorf("%s is required", name)
	}
	for i, r := range containerName {
		if i == 0 {
			if ('A' <= r && r <= 'Z') || ('a' <= r && r <= 'z') || ('0' <= r && r <= '9') {
				continue
			}
			return fmt.Errorf("%s must start with a letter or digit", name)
		}
		if ('A' <= r && r <= 'Z') || ('a' <= r && r <= 'z') || ('0' <= r && r <= '9') || r == '.' || r == '_' || r == '-' {
			continue
		}
		return fmt.Errorf("%s may contain only letters, digits, '.', '_', or '-'", name)
	}
	return nil
}

func validateDockerUser(name, raw string) error {
	userSpec := strings.TrimSpace(raw)
	if userSpec == "" {
		return nil
	}
	for _, r := range userSpec {
		if unicode.IsSpace(r) {
			return fmt.Errorf("%s must not contain whitespace", name)
		}
	}
	userPart, groupPart, hasGroup := strings.Cut(userSpec, ":")
	if userPart == "" {
		return fmt.Errorf("%s must include a user before ':'", name)
	}
	if hasGroup && groupPart == "" {
		return fmt.Errorf("%s must include a group after ':'", name)
	}
	if hasGroup && strings.Contains(groupPart, ":") {
		return fmt.Errorf("%s must have the form user or user:group", name)
	}
	if !validDockerUserPart(userPart) {
		return fmt.Errorf("%s user must use letters, digits, '.', '_', or '-'", name)
	}
	if hasGroup && !validDockerUserPart(groupPart) {
		return fmt.Errorf("%s group must use letters, digits, '.', '_', or '-'", name)
	}
	return nil
}

func validDockerUserPart(part string) bool {
	if part == "" {
		return false
	}
	for _, r := range part {
		if ('A' <= r && r <= 'Z') || ('a' <= r && r <= 'z') || ('0' <= r && r <= '9') || r == '.' || r == '_' || r == '-' {
			continue
		}
		return false
	}
	return true
}

func validateDockerPublish(raw string) (string, error) {
	spec := strings.TrimSpace(raw)
	if spec == "" {
		return "", errors.New("--publish values must not be empty")
	}
	for _, r := range spec {
		if unicode.IsSpace(r) {
			return "", fmt.Errorf("invalid --publish %q: whitespace is not allowed", spec)
		}
	}
	base, proto, hasProto := strings.Cut(spec, "/")
	if hasProto {
		if base == "" {
			return "", fmt.Errorf("invalid --publish %q: missing port mapping before protocol", spec)
		}
		switch proto {
		case "tcp", "udp", "sctp":
		default:
			return "", fmt.Errorf("invalid --publish %q: protocol must be tcp, udp, or sctp", spec)
		}
		if strings.Contains(proto, "/") {
			return "", fmt.Errorf("invalid --publish %q: protocol must be tcp, udp, or sctp", spec)
		}
	}
	if base == "" {
		return "", fmt.Errorf("invalid --publish %q: missing container port", spec)
	}
	containerPort := base
	if i := strings.LastIndex(base, ":"); i >= 0 {
		containerPort = base[i+1:]
	}
	if err := validateDockerPortRange(containerPort); err != nil {
		return "", fmt.Errorf("invalid --publish %q: %w", spec, err)
	}
	return spec, nil
}

func validateDockerPortRange(raw string) error {
	if raw == "" {
		return errors.New("missing container port")
	}
	startRaw, endRaw, hasRange := strings.Cut(raw, "-")
	start, err := parseDockerPort(startRaw)
	if err != nil {
		return err
	}
	if !hasRange {
		return nil
	}
	if strings.Contains(endRaw, "-") {
		return errors.New("port range must contain one '-'")
	}
	end, err := parseDockerPort(endRaw)
	if err != nil {
		return err
	}
	if end < start {
		return errors.New("port range end must be greater than or equal to start")
	}
	return nil
}

func parseDockerPort(raw string) (int, error) {
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > 65535 {
		return 0, errors.New("container port must be between 1 and 65535")
	}
	return n, nil
}

func maybeStartSandboxExecutor(spec, workspace string, runner commandRunner) (string, error) {
	if spec != "sandbox" {
		return spec, nil
	}
	opts := defaultSandboxStartOptions(workspace)
	if err := startSandbox(opts, runner); err != nil {
		return "", err
	}
	return "docker:" + opts.Name, nil
}

type sandboxStatus struct {
	Name            string
	State           string
	Running         bool
	Image           string
	Workspace       string
	Memory          string
	CPUs            string
	PIDsLimit       string
	User            string
	MemoryBytes     string
	MemorySwapBytes string
	PIDsLimitActual string
	WorkingDir      string
}

func inspectSandboxStatus(name string, runner commandRunner) (sandboxStatus, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return sandboxStatus{}, errors.New("--name is required")
	}
	if err := validateDockerContainerName("--name", name); err != nil {
		return sandboxStatus{}, err
	}
	out, err := runner.Run("docker", "inspect", "-f", sandboxStatusTemplate, name)
	if err != nil {
		return sandboxStatus{}, err
	}
	status, err := parseSandboxStatus(name, out)
	if err != nil {
		return sandboxStatus{}, err
	}
	return status, nil
}

func parseSandboxStatus(name, inspect string) (sandboxStatus, error) {
	lines := strings.Split(strings.TrimSpace(inspect), "\n")
	for len(lines) < 13 {
		lines = append(lines, "")
	}
	if strings.TrimSpace(lines[2]) != "true" {
		return sandboxStatus{}, fmt.Errorf("container %q exists but is not an affent sandbox", name)
	}
	return sandboxStatus{
		Name:            name,
		State:           strings.TrimSpace(lines[0]),
		Running:         strings.TrimSpace(lines[1]) == "true",
		Image:           strings.TrimSpace(lines[3]),
		Workspace:       strings.TrimSpace(lines[4]),
		Memory:          strings.TrimSpace(lines[5]),
		CPUs:            strings.TrimSpace(lines[6]),
		PIDsLimit:       strings.TrimSpace(lines[7]),
		User:            strings.TrimSpace(lines[8]),
		MemoryBytes:     strings.TrimSpace(lines[9]),
		MemorySwapBytes: strings.TrimSpace(lines[10]),
		PIDsLimitActual: strings.TrimSpace(lines[11]),
		WorkingDir:      strings.TrimSpace(lines[12]),
	}, nil
}

func printSandboxStatus(w io.Writer, s sandboxStatus) {
	fmt.Fprintf(w, "name: %s\n", s.Name)
	fmt.Fprintf(w, "state: %s\n", s.State)
	fmt.Fprintf(w, "running: %t\n", s.Running)
	fmt.Fprintf(w, "image: %s\n", s.Image)
	fmt.Fprintf(w, "workspace: %s\n", s.Workspace)
	fmt.Fprintf(w, "memory: %s (%s bytes)\n", s.Memory, s.MemoryBytes)
	fmt.Fprintf(w, "memory_swap_bytes: %s\n", s.MemorySwapBytes)
	fmt.Fprintf(w, "cpus: %s\n", s.CPUs)
	fmt.Fprintf(w, "pids_limit: %s (%s actual)\n", s.PIDsLimit, s.PIDsLimitActual)
	fmt.Fprintf(w, "user: %s\n", s.User)
	fmt.Fprintf(w, "working_dir: %s\n", s.WorkingDir)
	fmt.Fprintf(w, "executor: docker:%s\n", s.Name)
}

func stopSandbox(name string, remove bool, runner commandRunner) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("--name is required")
	}
	if err := validateDockerContainerName("--name", name); err != nil {
		return err
	}
	if _, err := inspectSandboxStatus(name, runner); err != nil {
		return err
	}
	if remove {
		_, err := runner.Run("docker", "rm", "-f", name)
		return err
	}
	_, err := runner.Run("docker", "stop", name)
	return err
}

func validateExistingSandbox(inspect string, opts sandboxStartOptions) (bool, error) {
	lines := strings.Split(strings.TrimSpace(inspect), "\n")
	for len(lines) < 8 {
		lines = append(lines, "")
	}
	running := strings.TrimSpace(lines[0]) == "true"
	got := map[string]string{
		sandboxLabelManaged:   strings.TrimSpace(lines[1]),
		sandboxLabelImage:     strings.TrimSpace(lines[2]),
		sandboxLabelWorkspace: strings.TrimSpace(lines[3]),
		sandboxLabelMemory:    strings.TrimSpace(lines[4]),
		sandboxLabelCPUs:      strings.TrimSpace(lines[5]),
		sandboxLabelPIDsLimit: strings.TrimSpace(lines[6]),
		sandboxLabelUser:      strings.TrimSpace(lines[7]),
	}
	want := map[string]string{
		sandboxLabelManaged:   "true",
		sandboxLabelImage:     opts.Image,
		sandboxLabelWorkspace: opts.Workspace,
		sandboxLabelMemory:    opts.Memory,
		sandboxLabelCPUs:      opts.CPUs,
		sandboxLabelPIDsLimit: opts.PIDsLimit,
		sandboxLabelUser:      opts.User,
	}
	for label, wantValue := range want {
		if got[label] != wantValue {
			return false, fmt.Errorf("container %q already exists but does not match requested sandbox %s=%q (got %q). Re-run with --replace to recreate it", opts.Name, label, wantValue, got[label])
		}
	}
	return running, nil
}

func printSandboxStartResult(w io.Writer, opts sandboxStartOptions) {
	workspace, err := filepath.Abs(opts.Workspace)
	if err == nil {
		opts.Workspace = workspace
	}
	if opts.PrintEnv {
		fmt.Fprintf(w, "export AFFENTCTL_EXECUTOR=%s\n", shellQuoteForEnv("docker:"+opts.Name))
		fmt.Fprintf(w, "export AFFENTCTL_WORKSPACE=%s\n", shellQuoteForEnv(opts.Workspace))
		return
	}
	fmt.Fprintf(w, "container: %s\n", opts.Name)
	fmt.Fprintf(w, "workspace: %s\n", opts.Workspace)
	fmt.Fprintf(w, "executor: docker:%s\n", opts.Name)
	fmt.Fprintf(w, "\nUse with:\n")
	fmt.Fprintf(w, "  affentctl run --executor docker:%s --workspace %s ...\n", opts.Name, shellQuoteForEnv(opts.Workspace))
	fmt.Fprintf(w, "\nOr export:\n")
	fmt.Fprintf(w, "  export AFFENTCTL_EXECUTOR=%s\n", shellQuoteForEnv("docker:"+opts.Name))
	fmt.Fprintf(w, "  export AFFENTCTL_WORKSPACE=%s\n", shellQuoteForEnv(opts.Workspace))
}

func shellQuoteForEnv(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}
