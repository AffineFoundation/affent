// Package executor provides the small interface the affent loop's shell
// tool calls into, plus a no-isolation LocalExecutor for CLI / training
// use. The "real" sandbox -- per-session Docker containers, with all
// the capability-drop / read-only-rootfs hardening -- lives in the
// gateway module which depends on this one.
package executor

import (
	"context"
	"io"
	"time"
)

// Executor is what the agent's shell tool talks to. It exists so the same
// loop can run against either:
//
//   - a per-session Docker container (gateway uses this),
//   - a plain os/exec backed local runner (CLI uses this),
//   - any other future provider (Kata, E2B, Firecracker, ...)
//
// without the loop knowing the difference. The name "executor" is
// deliberately not "sandbox": the LocalExecutor offers no isolation, and
// the real sandboxing lives one layer up in the gateway.
type Executor interface {
	// SessionID returns whatever id the owner of this executor uses.
	// Mostly useful for logging.
	SessionID() string

	// Exec runs cmd inside the executor's environment, captures
	// stdout/stderr separately, and returns the result with the exit
	// code. Stdin is optional. Timeout caps the run; ctx still applies.
	Exec(ctx context.Context, cmd []string, opts ExecOptions) (ExecResult, error)
}

// ExecOptions controls a single Exec call.
type ExecOptions struct {
	// WorkingDir overrides the executor's default working directory.
	WorkingDir string
	// Env additions in "KEY=VALUE" form, layered on top of the executor's
	// process env.
	Env []string
	// Timeout caps the run; zero means "no extra timeout, use ctx".
	Timeout time.Duration
	// Stdin, if non-nil, is fed to the command's stdin and closed.
	Stdin io.Reader
	// MaxOutputBytes caps how much per-stream (stdout, stderr) output
	// the executor buffers in memory. Above the cap, bytes are
	// accepted from the child (so its pipe doesn't block) but
	// discarded, and a truncation marker is appended to the returned
	// stream. Zero falls back to DefaultExecOutputCap.
	MaxOutputBytes int
}

// ExecResult is what Exec returns.
type ExecResult struct {
	ExitCode int
	Stdout   string
	Stderr   string
}
