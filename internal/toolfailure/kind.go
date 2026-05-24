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
	if !failed && !IsNoEvidenceResult(tool, result) {
		return ""
	}
	return Kind(result)
}

func IsNoEvidenceResult(tool, result string) bool {
	switch tool {
	case "web_fetch":
		return IsNoEvidenceWebFetchResult(result)
	case "web_search":
		return IsNoEvidenceWebSearchResult(result)
	default:
		return false
	}
}

func IsNoEvidenceWebFetchResult(result string) bool {
	result = strings.TrimSpace(result)
	return strings.HasPrefix(result, "[empty response:") ||
		strings.HasPrefix(result, "[non-text response:")
}

func IsNoEvidenceWebSearchResult(result string) bool {
	result = strings.TrimSpace(result)
	return strings.HasPrefix(result, "(no results)") ||
		strings.HasPrefix(result, "(no usable results:")
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
