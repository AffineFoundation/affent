package toolfailure

import "strings"

// Kind extracts a structured failure kind from tool output lines such as:
//
//	Failure: kind=blocked, status=403
//
// Invalid kinds return an empty string so callers can safely surface the
// result in JSON, logs, and eval summaries without trusting arbitrary text.
func Kind(output string) string {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "Failure:") {
			continue
		}
		rest := strings.TrimSpace(strings.TrimPrefix(line, "Failure:"))
		for _, part := range strings.Split(rest, ",") {
			part = strings.TrimSpace(part)
			if !strings.HasPrefix(part, "kind=") {
				continue
			}
			kind := strings.TrimSpace(strings.TrimPrefix(part, "kind="))
			if validKind(kind) {
				return kind
			}
		}
	}
	return ""
}

func KindForResult(tool, result string, failed bool) string {
	if !failed && !(tool == "web_fetch" && IsNoEvidenceWebFetchResult(result)) {
		return ""
	}
	return Kind(result)
}

func IsNoEvidenceWebFetchResult(result string) bool {
	result = strings.TrimSpace(result)
	return strings.HasPrefix(result, "[empty response:") ||
		strings.HasPrefix(result, "[non-text response:")
}

func validKind(kind string) bool {
	if kind == "" {
		return false
	}
	for _, r := range kind {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			continue
		}
		return false
	}
	return true
}
