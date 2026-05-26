package agenteval

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestCompactOneLine_UTF8Safe(t *testing.T) {
	got := compactOneLine("hello 世界 from affent", 8)
	if !utf8.ValidString(got) {
		t.Fatalf("compactOneLine produced invalid UTF-8: %q", got)
	}
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("compactOneLine should append ellipsis when truncated, got %q", got)
	}
}

func TestCompactOneLine_ShortInputUnchanged(t *testing.T) {
	got := compactOneLine("short line", 80)
	if got != "short line" {
		t.Fatalf("compactOneLine() = %q, want %q", got, "short line")
	}
}
