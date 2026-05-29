package taskstate

import "github.com/affinefoundation/affent/internal/verification"

var DefaultVerificationCommandIndicators = verification.DefaultCommandIndicators

func ShellCommandLooksLikeVerificationWithIndicators(command string, indicators []string) bool {
	return verification.CommandLooksLikeVerificationWithIndicators(command, indicators)
}

func ShellCommandLooksLikeVerification(command string) bool {
	return verification.CommandLooksLikeVerification(command)
}

func ToolRequestLooksLikeVerification(req ToolRequest) bool {
	if req.Tool != "shell" {
		return false
	}
	return ShellCommandLooksLikeVerification(argString(req.Args, "command"))
}
