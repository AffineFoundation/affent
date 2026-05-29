package taskstate

import (
	"strings"
	"testing"
)

func TestNextHintExtractsStructuredToolRecovery(t *testing.T) {
	got := NextHint(
		"file not found\nNext: run rg --files config before retrying\nFailure: kind=not_found",
		"",
	)
	if got != "run rg --files config before retrying" {
		t.Fatalf("NextHint = %q", got)
	}
}

func TestNextHintFallsBackToFullResult(t *testing.T) {
	got := NextHint(
		"file not found",
		"file not found\nNext: inspect the workspace root before retrying\nFailure: kind=not_found",
	)
	if got != "inspect the workspace root before retrying" {
		t.Fatalf("NextHint = %q", got)
	}
}

func TestNextHintCompactsLongText(t *testing.T) {
	got := NextHint("failed\nNext: "+strings.Repeat("retry with evidence ", 40)+"\nFailure: kind=tool_failed", "")
	if len([]rune(got)) > 263 || !strings.HasSuffix(got, "...") {
		t.Fatalf("NextHint did not compact long hint: len=%d text=%q", len([]rune(got)), got)
	}
}
