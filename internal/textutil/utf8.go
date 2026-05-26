package textutil

import (
	"strings"
	"unicode"
)

// Preview returns a UTF-8-safe prefix of s with an optional suffix
// appended when truncation is needed. When no suffix is provided it
// defaults to "...".
func Preview(s string, n int, suffix ...string) string {
	if len(s) <= n {
		return s
	}
	marker := "..."
	if len(suffix) > 0 {
		marker = suffix[0]
	}
	return s[:AlignBackward(s, n)] + marker
}

// PreviewRunes returns a rune-count-safe prefix of s with an optional
// suffix appended when truncation is needed. When no suffix is provided
// it defaults to "...".
func PreviewRunes(s string, n int, suffix ...string) string {
	if n <= 0 {
		return ""
	}
	runes := 0
	for i := range s {
		if runes == n {
			marker := "..."
			if len(suffix) > 0 {
				marker = suffix[0]
			}
			return s[:i] + marker
		}
		runes++
	}
	return s
}

// CompactWhitespace collapses all Unicode whitespace runs to single
// spaces and trims leading/trailing whitespace.
func CompactWhitespace(s string) string {
	if s == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(s))
	pendingSpace := false
	for _, r := range s {
		if unicode.IsSpace(r) {
			if b.Len() > 0 {
				pendingSpace = true
			}
			continue
		}
		if pendingSpace {
			b.WriteByte(' ')
			pendingSpace = false
		}
		b.WriteRune(r)
	}
	return b.String()
}

// TruncateWithMarker returns a UTF-8-safe prefix of s and a marker that
// describes how many bytes were omitted. The marker function may depend on
// the omitted-byte count, which can shift when the marker itself changes
// length. The returned omitted count is the final omitted byte count.
func TruncateWithMarker(s string, max int, marker func(omitted int) string) (string, int) {
	if len(s) <= max {
		return s, 0
	}
	omitted := len(s) - max
	for {
		m := ""
		if marker != nil {
			m = marker(omitted)
		}
		limit := max - len(m)
		if limit <= 0 {
			cut := AlignBackward(s, max)
			return s[:cut], len(s) - cut
		}
		cut := AlignBackward(s, limit)
		actualOmitted := len(s) - cut
		if actualOmitted == omitted {
			return s[:cut] + m, actualOmitted
		}
		omitted = actualOmitted
	}
}

// PreviewHead returns the UTF-8-safe prefix of s up to max bytes and the
// number of bytes omitted from the tail.
func PreviewHead(s string, max int) (string, int) {
	if max <= 0 {
		return "", len(s)
	}
	if len(s) <= max {
		return strings.TrimSpace(s), 0
	}
	cut := AlignBackward(s, max)
	return strings.TrimSpace(s[:cut]), len(s) - cut
}

// AlignForward returns the smallest index i >= pos such that s[i] is a UTF-8
// leading byte, or len(s) if pos is past the end.
func AlignForward(s string, pos int) int {
	if pos <= 0 {
		return 0
	}
	if pos >= len(s) {
		return len(s)
	}
	for pos < len(s) && (s[pos]&0xC0) == 0x80 {
		pos++
	}
	return pos
}

// AlignBackward returns the largest index i <= pos such that s[i] is a UTF-8
// leading byte, or 0 if pos is at the start.
func AlignBackward(s string, pos int) int {
	if pos <= 0 {
		return 0
	}
	if pos >= len(s) {
		return len(s)
	}
	for pos > 0 && (s[pos]&0xC0) == 0x80 {
		pos--
	}
	return pos
}
