package main

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTruncate_UTF8Safe(t *testing.T) {
	got := truncate("hello世界", 7)
	if !utf8.ValidString(got) {
		t.Fatalf("truncate returned invalid UTF-8: %q", got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("truncate should append ellipsis, got %q", got)
	}
}

func TestTruncateShortInput(t *testing.T) {
	if got := truncate("short", 10); got != "short" {
		t.Fatalf("truncate() = %q, want %q", got, "short")
	}
}
