package verification

import "strings"

var DefaultCommandIndicators = []string{
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

func CommandLooksLikeVerificationWithIndicators(command string, indicators []string) bool {
	lower := strings.ToLower(command)
	for _, indicator := range indicators {
		if strings.Contains(lower, strings.ToLower(strings.TrimSpace(indicator))) {
			return true
		}
	}
	return false
}

func CommandLooksLikeVerification(command string) bool {
	return CommandLooksLikeVerificationWithIndicators(command, DefaultCommandIndicators)
}
