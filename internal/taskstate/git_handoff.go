package taskstate

import "strings"

const (
	GitCommitEvidenceSource = "git_commit"
	GitPushEvidenceSource   = "git_push"
)

func ShellHandoffEvidenceSource(command string) string {
	switch GitSubcommand(command) {
	case "commit":
		return GitCommitEvidenceSource
	case "push":
		return GitPushEvidenceSource
	default:
		return ""
	}
}

func GitSubcommand(command string) string {
	fields := strings.Fields(command)
	if len(fields) == 0 || strings.Trim(fields[0], " \t\r\n()") != "git" {
		return ""
	}
	for i := 1; i < len(fields); i++ {
		token := strings.Trim(fields[i], " \t\r\n;()")
		switch {
		case token == "-C" || token == "-c" || token == "--git-dir" || token == "--work-tree" || token == "--namespace":
			i++
			continue
		case strings.HasPrefix(token, "--git-dir=") || strings.HasPrefix(token, "--work-tree=") || strings.HasPrefix(token, "--namespace="):
			continue
		case strings.HasPrefix(token, "-C") && token != "-C":
			continue
		case strings.HasPrefix(token, "-c") && token != "-c":
			continue
		case strings.HasPrefix(token, "-"):
			continue
		default:
			return token
		}
	}
	return ""
}
