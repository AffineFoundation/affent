package agenteval

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/affinefoundation/affent/internal/jsonl"
)

const (
	DefaultBatchTimeout           = 5 * time.Minute
	DefaultBatchMaxTurnSteps      = 10
	DefaultVerifierOutputCapBytes = 1 * 1024 * 1024
	maxTraceLineBytes             = jsonl.DefaultMaxRecordBytes
)

type ToolOrderRequirement struct {
	Earlier string
	Later   string
}

type CommandToolOrderRequirement struct {
	Command string
	Tool    string
}

type BatchScenario struct {
	Name                         string
	Suites                       []string
	Prompt                       string
	Files                        map[string]string
	VerifyCommand                string
	ExpectedSkill                string
	ForbiddenCommands            []string
	RequiredCommands             []string
	RequiredCommandCounts        map[string]int
	RequiredToolCounts           map[string]int
	RequiredCommandBeforeTool    []CommandToolOrderRequirement
	RequiredCommandAfterTool     []CommandToolOrderRequirement
	RequiredTools                []string
	ForbiddenTools               []string
	RequiredFocusedTaskCounts    map[string]int
	RequiredSubagentModeCounts   map[string]int
	RequireNoDelegationErrors    bool
	RequiredFinalText            []string
	ForbiddenFinalText           []string
	RequiredToolResultText       map[string][]string
	RequiredTruncatedResults     []string
	RequiredResultArtifacts      []string
	RequiredToolOrder            []ToolOrderRequirement
	ProtectedFiles               []string
	ForbiddenFileSubstrings      map[string][]string
	MaxParentToolCalls           int
	MaxSuccessfulToolCallsByTool map[string]int
	MaxTurns                     int
}

type BatchRunner struct {
	RepoRoot                 string
	WorkRoot                 string
	BaseURL                  string
	APIKey                   string
	Model                    string
	Temperature              string
	Executor                 string
	GoBin                    string
	Timeout                  time.Duration
	VerifierOutputCapBytes   int
	CleanupPassingWorkspaces bool
}

type BatchResult struct {
	BatchScenario      string
	Workspace          string
	TracePath          string
	OK                 bool
	Failures           []string
	Duration           time.Duration
	FinalText          string
	TraceSchemaVersion int
	TurnEndReason      string
	ToolCalls          int
	ToolStats          ToolRuntimeStats
	ToolTruncation     ToolTruncationStats
	Usage              Usage
	Verifier           VerifierResult
	WorkspaceRemoved   bool
	CleanupError       string
	// Delegation aggregates focused-task / subagent calls observed
	// in the trace. Zero-value when the scenario used no delegation
	// tool; HasAny() reports whether the block is worth surfacing.
	Delegation DelegationStats
	// Plan aggregates persisted-plan tool usage. Zero-value when the
	// scenario did not call the plan tool.
	Plan PlanStats
	// Repair aggregates tool-call recovery kinds from tool.request
	// repair_notes. Zero-value when no tool repair/canonicalization
	// occurred.
	Repair ToolRepairStats
}

type VerifierResult struct {
	Command            string
	Ran                bool
	OK                 bool
	ExitCode           int
	Duration           time.Duration
	OutputBytes        int
	OutputTruncated    bool
	OutputOmittedBytes int
	OutputCapBytes     int
}

func BuiltinBatchScenarios() []BatchScenario {
	return []BatchScenario{
		goMedianScenario(),
		goConfigPrecedenceScenario(),
		pythonSlugScenario(),
		goRedactionScenario(),
		pythonConfigParserScenario(),
		promptInjectionFactsScenario(),
		focusedTaskProjectFactsScenario(),
		subagentProjectFactsScenario(),
		subagentNoisyFactsScenario(),
		subagentNestedFactsScenario(),
		smallToolBadJSONReadScenario(),
		smallToolWrongFieldReadScenario(),
		smallToolWrongToolNameScenario(),
		skillToolReadScenario(),
		planCodingRepairScenario(),
		planNotForSimpleReadScenario(),
		smallToolRepeatedReadScenario(),
		smallToolEditRecoveryScenario(),
		smallToolShellFailureScenario(),
		oversizedToolResultScenario(),
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

func BatchSuiteNames() []string {
	seen := map[string]bool{}
	for _, s := range BuiltinBatchScenarios() {
		for _, suite := range s.Suites {
			if strings.TrimSpace(suite) != "" {
				seen[suite] = true
			}
		}
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func SelectBatchScenarios(names []string) ([]BatchScenario, error) {
	return SelectBatchScenariosForSuite("", names)
}

func SelectBatchScenariosForSuite(suite string, names []string) ([]BatchScenario, error) {
	all := BuiltinBatchScenarios()
	if suite != "" {
		filtered := all[:0]
		for _, s := range all {
			if scenarioInSuite(s, suite) {
				filtered = append(filtered, s)
			}
		}
		all = filtered
		if len(all) == 0 {
			return nil, fmt.Errorf("unknown suite %q (valid: %s)", suite, strings.Join(BatchSuiteNames(), ", "))
		}
	}
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
			valid := make([]string, 0, len(all))
			for _, s := range all {
				valid = append(valid, s.Name)
			}
			sort.Strings(valid)
			return nil, fmt.Errorf("unknown scenario %q (valid: %s)", name, strings.Join(valid, ", "))
		}
		selected = append(selected, s)
	}
	return selected, nil
}

func scenarioInSuite(s BatchScenario, suite string) bool {
	for _, candidate := range s.Suites {
		if candidate == suite {
			return true
		}
	}
	return false
}

func (r BatchRunner) Run(ctx context.Context, scenario BatchScenario) BatchResult {
	start := time.Now()
	res := BatchResult{BatchScenario: scenario.Name}
	if r.Timeout <= 0 {
		r.Timeout = DefaultBatchTimeout
	}
	if scenario.MaxTurns <= 0 {
		scenario.MaxTurns = DefaultBatchMaxTurnSteps
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
	if err := verifyForbiddenFileSubstrings(workspace, scenario.ForbiddenFileSubstrings); err != nil {
		res.Failures = append(res.Failures, err.Error())
	}
	if scenario.VerifyCommand != "" {
		verifier := r.runVerifier(runCtx, workspace, repoRoot, scenario.VerifyCommand)
		res.Verifier = verifier.Result
		if verifier.Err != nil {
			res.Failures = append(res.Failures, fmt.Sprintf("verify command failed: %s: %v\n%s", scenario.VerifyCommand, verifier.Err, trimOneLine(verifier.Output, 1200)))
		}
	}
	trace, err := ParseTraceFile(tracePath)
	if err != nil {
		res.Failures = append(res.Failures, fmt.Sprintf("parse trace: %v", err))
	} else {
		trace.WorkspaceDir = workspace
		res.TraceSchemaVersion = trace.SchemaVersion
		res.TurnEndReason = trace.TurnEndReason
		res.ToolCalls = len(trace.Tools)
		res.ToolStats = trace.ToolStats
		res.ToolTruncation = SummarizeToolTruncation(trace)
		res.Usage = trace.Usage
		res.Delegation = trace.DelegationStats()
		res.Plan = trace.PlanStats()
		res.Repair = trace.RepairStats()
		res.Failures = append(res.Failures, CheckBatchTrace(trace, scenario)...)
	}
	if scenario.ExpectedSkill != "" {
		if err := checkConversationSkill(workspace, scenario.ExpectedSkill); err != nil {
			res.Failures = append(res.Failures, err.Error())
		}
	}
	res.Duration = time.Since(start)
	res.OK = len(res.Failures) == 0
	r.cleanupPassingWorkspace(&res, workspace)
	return res
}

func (r BatchRunner) cleanupPassingWorkspace(res *BatchResult, workspace string) {
	if res == nil || !res.OK || !r.CleanupPassingWorkspaces {
		return
	}
	if err := os.RemoveAll(workspace); err != nil {
		res.CleanupError = err.Error()
		return
	}
	res.WorkspaceRemoved = true
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
	args := r.affentctlRunArgs(workspace, tracePath, scenario)
	cmd := exec.CommandContext(ctx, goBin, args...)
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), "PATH="+evalPath(repoRoot))
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := runEvalCommand(ctx, cmd)
	return stdout.String(), stderr.String(), exitCodeFromError(err), err
}

func (r BatchRunner) affentctlRunArgs(workspace, tracePath string, scenario BatchScenario) []string {
	executor := strings.TrimSpace(r.Executor)
	if executor == "" {
		executor = "local"
	}
	args := []string{
		"run", "./cmd/affentctl", "run",
		"--workspace", workspace,
		"--executor", executor,
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
	return args
}

type verifierRun struct {
	Result VerifierResult
	Output string
	Err    error
}

func (r BatchRunner) runVerifier(ctx context.Context, workspace, repoRoot, command string) verifierRun {
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Dir = workspace
	cmd.Env = append(os.Environ(), "PATH="+evalPath(repoRoot))
	out := newVerifierOutputBuffer(r.VerifierOutputCapBytes)
	cmd.Stdout = out
	cmd.Stderr = out
	start := time.Now()
	err := runEvalCommand(ctx, cmd)
	output := out.String()
	stats := out.Stats()
	result := VerifierResult{
		Command:            command,
		Ran:                true,
		OK:                 err == nil,
		ExitCode:           exitCodeFromError(err),
		Duration:           time.Since(start),
		OutputBytes:        stats.Bytes,
		OutputTruncated:    stats.Truncated,
		OutputOmittedBytes: stats.OmittedBytes,
		OutputCapBytes:     stats.CapBytes,
	}
	return verifierRun{Result: result, Output: output, Err: err}
}

func exitCodeFromError(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}

type verifierOutputStats struct {
	Bytes        int
	Truncated    bool
	OmittedBytes int
	CapBytes     int
}

type verifierOutputBuffer struct {
	mu        sync.Mutex
	buf       []byte
	cap       int
	bytes     int
	omitted   int
	truncated bool
}

func newVerifierOutputBuffer(capBytes int) *verifierOutputBuffer {
	if capBytes <= 0 {
		capBytes = DefaultVerifierOutputCapBytes
	}
	return &verifierOutputBuffer{cap: capBytes}
}

func (b *verifierOutputBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.bytes += len(p)
	room := b.cap - len(b.buf)
	switch {
	case room <= 0:
		b.omitted += len(p)
		b.truncated = true
	case len(p) > room:
		b.buf = append(b.buf, p[:room]...)
		b.omitted += len(p) - room
		b.truncated = true
	default:
		b.buf = append(b.buf, p...)
	}
	return len(p), nil
}

func (b *verifierOutputBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	if !b.truncated {
		return string(b.buf)
	}
	return string(b.buf) + fmt.Sprintf("\n[... %d more bytes truncated from verifier output; %d-byte cap.]", b.omitted, b.cap)
}

func (b *verifierOutputBuffer) Stats() verifierOutputStats {
	b.mu.Lock()
	defer b.mu.Unlock()

	return verifierOutputStats{
		Bytes:        b.bytes,
		Truncated:    b.truncated,
		OmittedBytes: b.omitted,
		CapBytes:     b.cap,
	}
}

func runEvalCommand(ctx context.Context, cmd *exec.Cmd) error {
	if err := startEvalCommand(cmd); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		killEvalCommandGroup(cmd)
		<-done
		return ctx.Err()
	}
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

func verifyForbiddenFileSubstrings(root string, forbidden map[string][]string) error {
	for name, substrings := range forbidden {
		raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil {
			return fmt.Errorf("forbidden-content file %s missing: %w", name, err)
		}
		body := string(raw)
		for _, substr := range substrings {
			if substr == "" {
				continue
			}
			if strings.Contains(body, substr) {
				return fmt.Errorf("forbidden content %q found in %s", substr, name)
			}
		}
	}
	return nil
}

// ParseTraceFile reads a JSONL trace file emitted by affentctl (or any
// SSE-event-shaped log) and returns the unified Trace the in-memory
// Runner also produces. One trace type, one check library — the
// BatchRunner path used to ship its own BatchTrace/BatchToolRequest
// twins which forced every check to be written twice.
//
// The file format is one JSON object per line with `{"type":"...",
// "data":{...}}`; new traces start with trace.meta carrying the schema
// version. Unknown event types are counted into RawTypes but otherwise
// ignored.
func ParseTraceFile(path string) (Trace, error) {
	f, err := os.Open(path)
	if err != nil {
		return Trace{}, err
	}
	defer f.Close()
	trace := Trace{RawTypes: map[string]int{}}
	pending := map[string]int{}
	r := bufio.NewReaderSize(f, 64*1024)
	lineNo := 0
	for {
		line, overLimit, err := jsonl.ReadBoundedLine(r, maxTraceLineBytes)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return trace, err
		}
		lineNo++
		if overLimit {
			return trace, fmt.Errorf("trace %s line %d exceeds max JSONL record size %d bytes", path, lineNo, maxTraceLineBytes)
		}
		var ev struct {
			Type string          `json:"type"`
			Data json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(line, &ev); err != nil {
			return trace, fmt.Errorf("trace %s line %d: %w", path, lineNo, err)
		}
		trace.RawTypes[ev.Type]++
		if _, err := applyTraceEvent(&trace, pending, ev.Type, ev.Data, ""); err != nil {
			return trace, fmt.Errorf("trace %s line %d: %w", path, lineNo, err)
		}
	}
	return trace, nil
}

// BatchScenarioChecks returns the Check slice derived from the
// declarative fields of a BatchScenario: RequiredCommands become
// ShellCommandMatching checks, ForbiddenCommands become
// ShellCommandLacksUnguarded checks, ProtectedFiles become
// FileNotEdited checks. Lets one Check library cover both pipelines.
func BatchScenarioChecks(scenario BatchScenario) []Check {
	checks := []Check{TurnEndedCleanly()}
	for _, tool := range scenario.RequiredTools {
		checks = append(checks, ToolCalled(tool, nil))
	}
	for _, tool := range scenario.ForbiddenTools {
		checks = append(checks, ToolNotCalled(tool, nil))
	}
	for _, substr := range scenario.RequiredFinalText {
		checks = append(checks, FinalTextContains(substr))
	}
	for _, substr := range scenario.ForbiddenFinalText {
		checks = append(checks, FinalTextLacks(substr))
	}
	for _, tool := range sortedStringMapKeys(scenario.RequiredToolResultText) {
		substrings := scenario.RequiredToolResultText[tool]
		for _, substr := range substrings {
			checks = append(checks, ToolResultContains(tool, substr))
		}
	}
	for _, tool := range scenario.RequiredTruncatedResults {
		checks = append(checks, ToolResultTruncated(tool))
	}
	for _, tool := range scenario.RequiredResultArtifacts {
		checks = append(checks, ToolResultArtifact(tool))
	}
	for _, order := range scenario.RequiredToolOrder {
		checks = append(checks, ToolCalledBefore(order.Earlier, order.Later))
	}
	for _, tool := range sortedStringMapKeys(scenario.RequiredToolCounts) {
		checks = append(checks, ToolCalledAtLeast(tool, scenario.RequiredToolCounts[tool]))
	}
	for _, taskType := range sortedStringMapKeys(scenario.RequiredFocusedTaskCounts) {
		checks = append(checks, FocusedTaskCalledAtLeast(taskType, scenario.RequiredFocusedTaskCounts[taskType]))
	}
	for _, mode := range sortedStringMapKeys(scenario.RequiredSubagentModeCounts) {
		checks = append(checks, SubagentCalledAtLeast(mode, scenario.RequiredSubagentModeCounts[mode]))
	}
	if scenario.RequireNoDelegationErrors {
		checks = append(checks, NoDelegationErrors())
	}
	if scenario.MaxParentToolCalls > 0 {
		checks = append(checks, MaxSuccessfulToolCalls(scenario.MaxParentToolCalls))
	}
	for _, tool := range sortedStringMapKeys(scenario.MaxSuccessfulToolCallsByTool) {
		checks = append(checks, MaxSuccessfulToolCallsForTool(tool, scenario.MaxSuccessfulToolCallsByTool[tool]))
	}
	for _, want := range scenario.RequiredCommands {
		checks = append(checks, ShellCommandMatching(want))
	}
	for _, pattern := range sortedStringMapKeys(scenario.RequiredCommandCounts) {
		checks = append(checks, ShellCommandMatchingAtLeast(pattern, scenario.RequiredCommandCounts[pattern]))
	}
	for _, order := range scenario.RequiredCommandBeforeTool {
		checks = append(checks, ShellCommandMatchingBeforeTool(order.Command, order.Tool))
	}
	for _, order := range scenario.RequiredCommandAfterTool {
		checks = append(checks, ShellCommandMatchingAfterTool(order.Command, order.Tool))
	}
	for _, forbidden := range scenario.ForbiddenCommands {
		checks = append(checks, ShellCommandLacksUnguarded(forbidden))
	}
	if len(scenario.ProtectedFiles) > 0 {
		checks = append(checks, FileNotEdited(scenario.ProtectedFiles))
	}
	return checks
}

func sortedStringMapKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func SummarizeToolTruncation(trace Trace) ToolTruncationStats {
	var stats ToolTruncationStats
	for _, tool := range trace.Tools {
		if tool.ArgsTruncated {
			stats.ArgsTruncated++
		}
		stats.ArgsOmittedBytes += tool.ArgsOmittedBytes
		if tool.ResultTruncated {
			stats.ResultsTruncated++
		}
		stats.ResultsOmittedBytes += tool.ResultOmittedBytes
		if tool.ResultArtifactPath != "" {
			stats.ResultArtifacts++
		}
	}
	return stats
}

// CheckBatchTrace runs BatchScenarioChecks against the trace and
// returns failure detail strings — the legacy signature BatchRunner.Run
// expects. New code should compose Check slices directly and read
// Outcome.FailedChecks() / Outcome.Results.
func CheckBatchTrace(trace Trace, scenario BatchScenario) []string {
	results := evaluateChecks(trace, BatchScenarioChecks(scenario))
	var failures []string
	for _, r := range results {
		if !r.Pass {
			failures = append(failures, r.Detail)
		}
	}
	return failures
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
