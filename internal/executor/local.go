package executor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// LocalExecutor satisfies Executor by running commands directly on the
// host. It is meant for the standalone CLI / training-environment use
// case where the caller already runs in an isolated environment (a VM, a
// per-task container, a chroot, etc.) and doesn't need a per-session
// Docker container around the agent.
//
// Caveat: this offers no kernel-level isolation; it just shells out as
// the parent process's UID. NEVER point an untrusted model at this on a
// shared machine.
type LocalExecutor struct {
	id           string
	workspaceDir string

	// ExtraPathDirs are appended to the spawned process's PATH (in
	// addition to whatever the inherited / opts.Env PATH already has).
	// Controls the convenience "find tools the operator installed in
	// the usual places" behavior:
	//
	//   nil          → use DefaultExtraPathDirs() (common Unix paths
	//                   like /usr/local/go/bin, ~/.local/bin, ~/go/bin).
	//   []string{}   → DISABLED. PATH is left exactly as the caller
	//                   passed it; operators who pin PATH explicitly
	//                   (e.g. inside a minimal container image) take
	//                   this path to keep affent from injecting.
	//   non-empty    → use these dirs verbatim, replacing the default.
	//
	// Data-driven so operators don't have to fork the package to add
	// or remove a candidate dir.
	ExtraPathDirs []string

	// ExtraEnv is layered onto every spawned command before per-call
	// opts.Env. Use for account/session level credentials such as
	// GITHUB_TOKEN. Do not include these values in logs or tool output.
	ExtraEnv []string

	// EnvProvider, when set, is called at exec time and layered after
	// ExtraEnv but before opts.Env. This lets long-running server
	// sessions pick up settings changes without being recreated.
	EnvProvider func() []string
}

// DefaultExtraPathDirs returns the candidate set used when
// LocalExecutor.ExtraPathDirs is nil. They're the locations a user
// commonly installs a Go / Python / Node toolchain into when the
// system package manager didn't (or in a non-root setup like the
// dev-box / training-rig affentctl is typically run on). Operators
// who want a different list pass their own slice via ExtraPathDirs.
func DefaultExtraPathDirs() []string {
	dirs := []string{
		"/usr/local/go/bin",
		"/snap/bin",
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		dirs = append([]string{
			filepath.Join(home, ".local", "go-toolchain", "go", "bin"),
			filepath.Join(home, ".local", "bin"),
			filepath.Join(home, "go", "bin"),
		}, dirs...)
	}
	return dirs
}

func NewLocalExecutor(sessionID, workspaceDir string) *LocalExecutor {
	return &LocalExecutor{id: sessionID, workspaceDir: workspaceDir}
}

func (h *LocalExecutor) SessionID() string { return h.id }

func (h *LocalExecutor) Exec(ctx context.Context, cmd []string, opts ExecOptions) (ExecResult, error) {
	if len(cmd) == 0 {
		return ExecResult{}, errors.New("empty command")
	}
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	c := exec.CommandContext(ctx, cmd[0], cmd[1:]...)
	if opts.WorkingDir != "" {
		c.Dir = opts.WorkingDir
	} else {
		c.Dir = h.workspaceDir
	}
	env := append(c.Environ(), h.ExtraEnv...)
	if h.EnvProvider != nil {
		env = append(env, h.EnvProvider()...)
	}
	c.Env = h.augmentPath(append(env, opts.Env...))
	if opts.Stdin != nil {
		c.Stdin = opts.Stdin
	}

	stdout := newCappingWriter(opts.MaxOutputBytes)
	stderr := newCappingWriter(opts.MaxOutputBytes)
	c.Stdout = stdout
	c.Stderr = stderr

	err := c.Run()
	exitCode := 0
	var execErr error
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			exitCode = ee.ExitCode()
		} else {
			// Spawn failure (binary missing, fork failed, etc.).
			execErr = fmt.Errorf("exec %q: %w", cmd[0], err)
			exitCode = -1
		}
		// CommandContext's SIGKILL on ctx.Done surfaces as *ExitError
		// with ExitCode -1 — indistinguishable from a real `exit -1`
		// until we also consult ctx. When the cmd failed AND ctx is
		// done, the kill was the ctx-signal; report the timeout/cancel
		// as the cause so the LLM sees "killed for running too long"
		// rather than "exited with code -1". Gated on err != nil
		// because a command that completed cleanly just before its
		// deadline must not be flipped to a "timeout" — c.Run already
		// gave us the truth.
		if ctx.Err() != nil && execErr == nil {
			execErr = fmt.Errorf("exec %q: %w", cmd[0], ctx.Err())
			exitCode = -1
		}
	}
	return ExecResult{
		ExitCode: exitCode,
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
	}, execErr
}

// augmentPath appends the executor's configured ExtraPathDirs (or the
// default set when nil) to env's PATH variable. A non-nil empty
// ExtraPathDirs disables augmentation entirely — operators with a
// pinned PATH (minimal-container deploys) opt into that.
func (h *LocalExecutor) augmentPath(env []string) []string {
	candidates := h.ExtraPathDirs
	if candidates == nil {
		candidates = DefaultExtraPathDirs()
	}
	if len(candidates) == 0 {
		return env
	}
	return withExtraPathDirs(env, candidates)
}

// withExtraPathDirs is the pure helper. Takes the env and the
// candidate dirs; returns env with PATH augmented (or appended if
// PATH wasn't there). Pulled out so tests can exercise the slice
// logic without instantiating a LocalExecutor.
func withExtraPathDirs(env []string, candidates []string) []string {
	pathIdx := -1
	pathVal := ""
	for i, kv := range env {
		if strings.HasPrefix(kv, "PATH=") {
			pathIdx = i
			pathVal = strings.TrimPrefix(kv, "PATH=")
		}
	}
	if pathIdx < 0 {
		return append(env, "PATH="+strings.Join(candidates, string(os.PathListSeparator)))
	}
	seen := map[string]bool{}
	for _, part := range filepath.SplitList(pathVal) {
		if part != "" {
			seen[part] = true
		}
	}
	additions := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate != "" && !seen[candidate] {
			additions = append(additions, candidate)
			seen[candidate] = true
		}
	}
	if len(additions) == 0 {
		return env
	}
	next := make([]string, 0, len(env))
	for _, kv := range env {
		if !strings.HasPrefix(kv, "PATH=") {
			next = append(next, kv)
		}
	}
	finalPath := pathVal
	if finalPath == "" {
		finalPath = strings.Join(additions, string(os.PathListSeparator))
	} else {
		finalPath = finalPath + string(os.PathListSeparator) + strings.Join(additions, string(os.PathListSeparator))
	}
	return append(next, "PATH="+finalPath)
}

// Compile-time interface check.
var _ Executor = (*LocalExecutor)(nil)
