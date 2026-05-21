package executor

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
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
	if len(opts.Env) > 0 {
		c.Env = append(c.Environ(), opts.Env...)
	}
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
	}
	// CommandContext's SIGKILL on ctx.Done surfaces as *ExitError with
	// ExitCode -1 — indistinguishable from a real `exit -1` until we
	// also consult ctx. When ctx fired (timeout or parent cancel) we
	// surface the timeout as the error so the LLM sees "killed for
	// running too long" rather than "exited with code -1".
	if ctx.Err() != nil && execErr == nil {
		execErr = fmt.Errorf("exec %q: %w", cmd[0], ctx.Err())
		exitCode = -1
	}
	return ExecResult{
		ExitCode: exitCode,
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
	}, execErr
}

// Compile-time interface check.
var _ Executor = (*LocalExecutor)(nil)
