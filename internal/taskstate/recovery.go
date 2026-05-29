package taskstate

import (
	"regexp"
	"strings"
)

var nextHintRe = regexp.MustCompile(`(?m)(?:^|\n)Next:\s*([\s\S]*?)(?:\nFailure:|\n[A-Z][A-Za-z _-]{0,40}:|$)`)

func NextHint(summary, result string) string {
	text := strings.TrimSpace(summary)
	if trimmed := strings.TrimSpace(result); trimmed != "" && trimmed != text {
		if text != "" {
			text += "\n"
		}
		text += trimmed
	}
	match := nextHintRe.FindStringSubmatch(text)
	if len(match) < 2 {
		return ""
	}
	return compactOneLine(match[1], 260)
}

func compactOneLine(text string, maxRunes int) string {
	text = strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
	if maxRunes <= 0 {
		return text
	}
	runes := []rune(text)
	if len(runes) <= maxRunes {
		return text
	}
	return strings.TrimSpace(string(runes[:maxRunes-1])) + "..."
}
