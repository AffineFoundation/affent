package toolfailure

import "strings"

// Kind extracts a structured failure kind from tool output lines such as:
//
//	Failure: kind=blocked, status=403
//
// Invalid kinds return an empty string so callers can safely surface the
// result in JSON, logs, and eval summaries without trusting arbitrary text.
func Kind(output string) string {
	kinds := Kinds(output)
	if len(kinds) == 0 {
		return ""
	}
	return kinds[0]
}

// Kinds extracts every distinct structured failure kind from tool output, in
// first-seen order. A single result may contain both the underlying tool
// failure (for example blocked) and a runtime guard classification appended
// afterward (for example loop_guard_repeated_failures).
func Kinds(output string) []string {
	var kinds []string
	seen := map[string]bool{}
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
			if validKind(kind) && !seen[kind] {
				seen[kind] = true
				kinds = append(kinds, kind)
			}
		}
	}
	return kinds
}

func KindForResult(tool, result string, failed bool) string {
	kinds := KindsForResult(tool, result, failed)
	if len(kinds) == 0 {
		return ""
	}
	return kinds[0]
}

func KindsForResult(tool, result string, failed bool) []string {
	if !failed && !IsNoEvidenceResult(tool, result) {
		return nil
	}
	kinds := Kinds(result)
	if failed {
		if kind := skippedToolBudgetKind(result); kind != "" && !containsString(kinds, kind) {
			kinds = append(kinds, kind)
		}
	}
	return kinds
}

func IsNoEvidenceResult(tool, result string) bool {
	switch tool {
	case "web_fetch":
		return IsNoEvidenceWebFetchResult(result)
	case "web_search":
		return IsNoEvidenceWebSearchResult(result)
	case "browser_network":
		return IsNoEvidenceBrowserNetworkResult(result)
	default:
		return false
	}
}

func IsNoEvidenceWebFetchResult(result string) bool {
	result = strings.TrimSpace(result)
	if strings.HasPrefix(result, "[dynamic page shell:") &&
		strings.Contains(result, "Embedded data preview (page source evidence; verify relevance before using):") {
		return false
	}
	return strings.HasPrefix(result, "[empty response:") ||
		strings.HasPrefix(result, "[blocked response:") ||
		strings.HasPrefix(result, "[dynamic page shell:") ||
		strings.HasPrefix(result, "[non-text response:")
}

func IsNoEvidenceWebSearchResult(result string) bool {
	result = strings.TrimSpace(result)
	return strings.HasPrefix(result, "(no results)") ||
		strings.HasPrefix(result, "(no usable results:")
}

func IsNoEvidenceBrowserNetworkResult(result string) bool {
	result = strings.TrimSpace(result)
	return strings.HasPrefix(result, "BROWSER NETWORK EVIDENCE") &&
		strings.Contains(result, "\nMATCHES: none\n")
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

func skippedToolBudgetKind(result string) string {
	switch {
	case strings.Contains(result, "max_turns reached before this tool ran"):
		return "loop_guard_no_budget"
	case strings.Contains(result, "tool call budget reached before this tool ran"):
		return "loop_guard_no_budget"
	default:
		return ""
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
