package taskstate

import "strings"

const (
	GitCommitEvidenceSource = "git_commit"
	GitPushEvidenceSource   = "git_push"
)

func ShellHandoffEvidenceSource(command string) string {
	sources := ShellHandoffEvidenceSources(command)
	if len(sources) == 0 {
		return ""
	}
	return sources[0]
}

func ShellHandoffEvidenceSources(command string) []string {
	var out []string
	for _, subcommand := range GitSubcommands(command) {
		switch subcommand {
		case "commit":
			out = appendUnique(out, GitCommitEvidenceSource, DefaultMaxItems)
		case "push":
			out = appendUnique(out, GitPushEvidenceSource, DefaultMaxItems)
		}
	}
	return out
}

func GitSubcommands(command string) []string {
	var out []string
	for _, segment := range splitShellCommandSegments(command) {
		if subcommand := GitSubcommand(segment); subcommand != "" {
			out = appendUnique(out, subcommand, DefaultMaxItems)
		}
	}
	return out
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

func splitShellCommandSegments(command string) []string {
	command = strings.TrimSpace(command)
	if command == "" {
		return nil
	}
	var segments []string
	start := 0
	var quote rune
	escaped := false
	for i, r := range command {
		if escaped {
			escaped = false
			continue
		}
		if r == '\\' && quote != '\'' {
			escaped = true
			continue
		}
		switch r {
		case '\'', '"':
			if quote == 0 {
				quote = r
			} else if quote == r {
				quote = 0
			}
			continue
		}
		if quote != 0 {
			continue
		}
		if r == ';' || r == '&' || r == '|' {
			if strings.TrimSpace(command[start:i]) != "" {
				segments = append(segments, command[start:i])
			}
			start = i + 1
		}
	}
	if strings.TrimSpace(command[start:]) != "" {
		segments = append(segments, command[start:])
	}
	return segments
}
