package executor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
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

	var stdout, stderr bytes.Buffer
	c.Stdout = &stdout
	c.Stderr = &stderr

	err := c.Run()
	exitCode := 0
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			exitCode = ee.ExitCode()
		} else {
			return ExecResult{}, fmt.Errorf("exec %q: %w", cmd[0], err)
		}
	}
	return ExecResult{
		ExitCode: exitCode,
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
	}, nil
}

// Compile-time interface check.
var _ Executor = (*LocalExecutor)(nil)

// quiet unused-import linter when callers don't need io.
var _ = io.Discard
