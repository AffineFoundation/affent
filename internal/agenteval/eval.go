package agenteval

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

type BatchScenario struct {
	Name              string
	Prompt            string
	Files             map[string]string
	VerifyCommand     string
	ExpectedSkill     string
	ForbiddenCommands []string
	RequiredCommands  []string
	ProtectedFiles    []string
	MaxTurns          int
}

type BatchRunner struct {
	RepoRoot    string
	WorkRoot    string
	BaseURL     string
	APIKey      string
	Model       string
	Temperature string
	GoBin       string
	Timeout     time.Duration
}

type BatchResult struct {
	BatchScenario string
	Workspace     string
	TracePath     string
	OK            bool
	Failures      []string
	Duration      time.Duration
	FinalText     string
}

func BuiltinBatchScenarios() []BatchScenario {
	return []BatchScenario{
		goMedianScenario(),
		pythonSlugScenario(),
	}
}

func BatchScenarioNames() []string {
	scenarios := BuiltinBatchScenarios()
	names := make([]string, 0, len(scenarios))
	for _, s := range scenarios {
		names = append(names, s.Name)
	}
	sort.Strings(names)
	return names
}

func SelectBatchScenarios(names []string) ([]BatchScenario, error) {
	all := BuiltinBatchScenarios()
	if len(names) == 0 {
		return all, nil
	}
	byName := map[string]BatchScenario{}
	for _, s := range all {
		byName[s.Name] = s
	}
	var selected []BatchScenario
	for _, name := range names {
		s, ok := byName[name]
		if !ok {
			return nil, fmt.Errorf("unknown scenario %q (valid: %s)", name, strings.Join(BatchScenarioNames(), ", "))
		}
		selected = append(selected, s)
	}
	return selected, nil
}

func (r BatchRunner) Run(ctx context.Context, scenario BatchScenario) BatchResult {
	start := time.Now()
	res := BatchResult{BatchScenario: scenario.Name}
	if r.Timeout <= 0 {
		r.Timeout = 5 * time.Minute
	}
	if scenario.MaxTurns <= 0 {
		scenario.MaxTurns = 10
	}
	if strings.TrimSpace(r.RepoRoot) == "" {
		r.RepoRoot = "."
	}
	repoRoot, err := filepath.Abs(r.RepoRoot)
	if err != nil {
		return res.fail("resolve repo root: %v", err)
	}
	workRoot := r.WorkRoot
	if strings.TrimSpace(workRoot) == "" {
		workRoot = filepath.Join(os.TempDir(), "affent-eval")
	}
	if err := os.MkdirAll(workRoot, 0o755); err != nil {
		return res.fail("create work root: %v", err)
	}
	workspace, err := os.MkdirTemp(workRoot, scenario.Name+"-*")
	if err != nil {
		return res.fail("create scenario workspace: %v", err)
	}
	res.Workspace = workspace
	if err := writeScenarioFiles(workspace, scenario.Files); err != nil {
		return res.fail("write scenario files: %v", err)
	}
	protected, err := readProtectedFiles(workspace, scenario.ProtectedFiles)
	if err != nil {
		return res.fail("snapshot protected files: %v", err)
	}
	tracePath := filepath.Join(workspace, "trace.jsonl")
	res.TracePath = tracePath
	runCtx, cancel := context.WithTimeout(ctx, r.Timeout)
	defer cancel()
	stdout, stderr, exitCode, err := r.runAffentctl(runCtx, repoRoot, workspace, tracePath, scenario)
	res.FinalText = strings.TrimSpace(stdout)
	if err != nil {
		res.Failures = append(res.Failures, fmt.Sprintf("affentctl run failed: exit=%d err=%v stderr=%s", exitCode, err, trimOneLine(stderr, 800)))
	}
	if err := verifyProtectedFiles(workspace, protected); err != nil {
		res.Failures = append(res.Failures, err.Error())
	}
	if scenario.VerifyCommand != "" {
		if out, err := r.runVerifier(ctx, workspace, repoRoot, scenario.VerifyCommand); err != nil {
			res.Failures = append(res.Failures, fmt.Sprintf("verify command failed: %s: %v\n%s", scenario.VerifyCommand, err, trimOneLine(out, 1200)))
		}
	}
	trace, err := ParseBatchTrace(tracePath)
	if err != nil {
		res.Failures = append(res.Failures, fmt.Sprintf("parse trace: %v", err))
	} else {
		res.Failures = append(res.Failures, CheckBatchTrace(trace, scenario)...)
	}
	if scenario.ExpectedSkill != "" {
		if err := checkConversationSkill(workspace, scenario.ExpectedSkill); err != nil {
			res.Failures = append(res.Failures, err.Error())
		}
	}
	res.Duration = time.Since(start)
	res.OK = len(res.Failures) == 0
	return res
}

func (r BatchResult) fail(format string, args ...any) BatchResult {
	r.Failures = append(r.Failures, fmt.Sprintf(format, args...))
	r.OK = false
	return r
}

func (r BatchRunner) runAffentctl(ctx context.Context, repoRoot, workspace, tracePath string, scenario BatchScenario) (string, string, int, error) {
	if strings.TrimSpace(r.BaseURL) == "" {
		r.BaseURL = os.Getenv("AFFENTCTL_BASE_URL")
	}
	if strings.TrimSpace(r.APIKey) == "" {
		r.APIKey = os.Getenv("AFFENTCTL_API_KEY")
	}
	if strings.TrimSpace(r.Model) == "" {
		r.Model = os.Getenv("AFFENTCTL_MODEL")
	}
	if strings.TrimSpace(r.BaseURL) == "" || strings.TrimSpace(r.Model) == "" {
		return "", "", 64, errors.New("base URL and model are required (flags or AFFENTCTL_BASE_URL/AFFENTCTL_MODEL)")
	}
	goBin := r.GoBin
	if goBin == "" {
		goBin = findGo(repoRoot)
	}
	args := []string{
		"run", "./cmd/affentctl", "run",
		"--workspace", workspace,
		"--base-url", r.BaseURL,
		"--model", r.Model,
		"--max-turns", fmt.Sprint(scenario.MaxTurns),
		"--trace", tracePath,
		"--trace-skip-deltas",
		"--prompt", scenario.Prompt,
	}
	if r.APIKey != "" {
		args = append(args, "--api-key", r.APIKey)
	}
	if r.Temperature != "" {
		args = append(args, "--temperature", r.Temperature)
	}
	cmd := exec.CommandContext(ctx, goBin, args...)
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), "PATH="+evalPath(repoRoot))
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	exitCode := 0
	if err != nil {
		exitCode = -1
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			exitCode = ee.ExitCode()
		}
	}
	return stdout.String(), stderr.String(), exitCode, err
}

func (r BatchRunner) runVerifier(ctx context.Context, workspace, repoRoot, command string) (string, error) {
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Dir = workspace
	cmd.Env = append(os.Environ(), "PATH="+evalPath(repoRoot))
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func writeScenarioFiles(root string, files map[string]string) error {
	for name, body := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func readProtectedFiles(root string, names []string) (map[string]string, error) {
	out := map[string]string{}
	for _, name := range names {
		raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil {
			return nil, err
		}
		out[name] = string(raw)
	}
	return out, nil
}

func verifyProtectedFiles(root string, protected map[string]string) error {
	for name, want := range protected {
		raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil {
			return fmt.Errorf("protected file %s missing: %w", name, err)
		}
		if string(raw) != want {
			return fmt.Errorf("protected file changed: %s", name)
		}
	}
	return nil
}

type BatchTrace struct {
	ToolRequests []BatchToolRequest
	RawTypes     map[string]int
}

type BatchToolRequest struct {
	CallID   string
	Tool     string
	Args     map[string]any
	Result   string
	ExitCode int
}

func ParseBatchTrace(path string) (BatchTrace, error) {
	f, err := os.Open(path)
	if err != nil {
		return BatchTrace{}, err
	}
	defer f.Close()
	trace := BatchTrace{RawTypes: map[string]int{}}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		var ev struct {
			Type string          `json:"type"`
			Data json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
			return trace, err
		}
		trace.RawTypes[ev.Type]++
		switch ev.Type {
		case "tool.request":
			var p struct {
				CallID string         `json:"call_id"`
				Tool   string         `json:"tool"`
				Args   map[string]any `json:"args"`
			}
			if err := json.Unmarshal(ev.Data, &p); err != nil {
				return trace, err
			}
			trace.ToolRequests = append(trace.ToolRequests, BatchToolRequest{CallID: p.CallID, Tool: p.Tool, Args: p.Args})
		case "tool.result":
			var p struct {
				CallID   string `json:"call_id"`
				Result   string `json:"result"`
				ExitCode int    `json:"exit_code"`
			}
			if err := json.Unmarshal(ev.Data, &p); err != nil {
				return trace, err
			}
			for i := range trace.ToolRequests {
				if trace.ToolRequests[i].CallID == p.CallID {
					trace.ToolRequests[i].Result = p.Result
					trace.ToolRequests[i].ExitCode = p.ExitCode
					break
				}
			}
		default:
			continue
		}
	}
	return trace, sc.Err()
}

func CheckBatchTrace(trace BatchTrace, scenario BatchScenario) []string {
	var failures []string
	var shellCalls []BatchToolRequest
	for _, req := range trace.ToolRequests {
		if req.Tool == "shell" {
			if command, _ := req.Args["command"].(string); command != "" {
				shellCalls = append(shellCalls, req)
			}
		}
		if (req.Tool == "write_file" || req.Tool == "edit_file") && protectedPathTouched(req, scenario.ProtectedFiles) {
			failures = append(failures, fmt.Sprintf("modified protected file through %s: %v", req.Tool, req.Args["path"]))
		}
	}
	for _, want := range scenario.RequiredCommands {
		if !commandMatches(shellCommands(shellCalls), want) {
			failures = append(failures, fmt.Sprintf("missing required command match %q; commands=%v", want, shellCommands(shellCalls)))
		}
	}
	for _, forbidden := range scenario.ForbiddenCommands {
		for _, call := range shellCalls {
			command, _ := call.Args["command"].(string)
			if commandRejectedByGuard(call) {
				continue
			}
			if strings.Contains(strings.ToLower(command), strings.ToLower(forbidden)) {
				failures = append(failures, fmt.Sprintf("forbidden command substring %q in %q", forbidden, command))
			}
		}
	}
	return failures
}

func shellCommands(calls []BatchToolRequest) []string {
	out := make([]string, 0, len(calls))
	for _, call := range calls {
		if command, _ := call.Args["command"].(string); command != "" {
			out = append(out, command)
		}
	}
	return out
}

func commandRejectedByGuard(req BatchToolRequest) bool {
	return req.ExitCode != 0 &&
		(strings.Contains(req.Result, "masks a test/build exit code") ||
			strings.Contains(req.Result, "unbounded filesystem scan"))
}

func protectedPathTouched(req BatchToolRequest, protected []string) bool {
	path, _ := req.Args["path"].(string)
	path = filepath.ToSlash(path)
	for _, name := range protected {
		if path == name || strings.HasSuffix(path, "/"+name) {
			return true
		}
	}
	return false
}

func commandMatches(commands []string, pattern string) bool {
	re, err := regexp.Compile(pattern)
	if err != nil {
		for _, command := range commands {
			if strings.Contains(command, pattern) {
				return true
			}
		}
		return false
	}
	for _, command := range commands {
		if re.MatchString(command) {
			return true
		}
	}
	return false
}

func checkConversationSkill(workspace, skill string) error {
	root := filepath.Join(workspace, ".affentctl")
	found := false
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".jsonl") {
			return err
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(raw), skill) {
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("search conversation skill: %w", err)
	}
	if !found {
		return fmt.Errorf("expected active skill %q not found in conversation log", skill)
	}
	return nil
}

func evalPath(repoRoot string) string {
	parts := []string{
		filepath.Join(repoRoot, ".tmp", "toolchains", "go", "bin"),
		filepath.Join(os.Getenv("HOME"), ".local", "go-toolchain", "go", "bin"),
		filepath.Join(os.Getenv("HOME"), ".local", "bin"),
		filepath.Join(os.Getenv("HOME"), "go", "bin"),
		"/usr/local/go/bin",
		"/snap/bin",
	}
	if path := os.Getenv("PATH"); path != "" {
		parts = append([]string{path}, parts...)
	}
	return strings.Join(dedupeNonEmpty(parts), string(os.PathListSeparator))
}

func findGo(repoRoot string) string {
	for _, candidate := range []string{
		filepath.Join(repoRoot, ".tmp", "toolchains", "go", "bin", "go"),
		filepath.Join(os.Getenv("HOME"), ".local", "go-toolchain", "go", "bin", "go"),
		"go",
	} {
		if path, err := exec.LookPath(candidate); err == nil {
			return path
		}
		if filepath.IsAbs(candidate) {
			if st, err := os.Stat(candidate); err == nil && !st.IsDir() {
				return candidate
			}
		}
	}
	return "go"
}

func dedupeNonEmpty(parts []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, part := range parts {
		if strings.TrimSpace(part) == "" || seen[part] {
			continue
		}
		seen[part] = true
		out = append(out, part)
	}
	return out
}

func trimOneLine(s string, n int) string {
	s = strings.TrimSpace(s)
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
