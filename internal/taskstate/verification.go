package taskstate

import "strings"

var DefaultVerificationCommandIndicators = []string{
	"pytest",
	"python -m unittest",
	"python3 -m unittest",
	"go test",
	"go build",
	"go vet",
	"npm test",
	"npm run test",
	"npm run build",
	"pnpm test",
	"yarn test",
	"cargo test",
	"mvn test",
	"gradle test",
	"make test",
	"tsc",
}

func ShellCommandLooksLikeVerificationWithIndicators(command string, indicators []string) bool {
	lower := strings.ToLower(command)
	for _, indicator := range indicators {
		if strings.Contains(lower, strings.ToLower(strings.TrimSpace(indicator))) {
			return true
		}
	}
	return false
}

func ShellCommandLooksLikeVerification(command string) bool {
	return ShellCommandLooksLikeVerificationWithIndicators(command, DefaultVerificationCommandIndicators)
}

func ToolRequestLooksLikeVerification(req ToolRequest) bool {
	if req.Tool != "shell" {
		return false
	}
	return ShellCommandLooksLikeVerification(argString(req.Args, "command"))
}
