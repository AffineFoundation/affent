//go:build windows

package agenteval

import "os/exec"

func startEvalCommand(cmd *exec.Cmd) error {
	return cmd.Start()
}

func killEvalCommandGroup(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
}
