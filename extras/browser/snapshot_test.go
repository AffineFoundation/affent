package browser

import (
	"strings"
	"testing"
)

// Snapshot serialization unit tests. These don't need a real browser —
// they exercise Format() against constructed Snapshot values, which is
// what the LLM actually sees and where regressions hurt most.

func TestFormat_BasicShape(t *testing.T) {
	checked := true
	snap := &Snapshot{
		SnapshotID: 7,
		URL:        "https://example.com/",
		Title:      "Example",
		TextBlocks: []TextBlock{
			{Type: "h1", Text: "Welcome"},
			{Type: "p", Text: "Lorem ipsum dolor sit amet."},
		},
		Interactive: []InteractiveElement{
			{Ref: 1, Role: "link", Name: "More info", Href: "/info"},
			{Ref: 2, Role: "textbox", Name: "Search", Value: "old query"},
			{Ref: 3, Role: "checkbox", Name: "Subscribe", Checked: &checked},
		},
	}
	out := snap.Format()

	for _, want := range []string{
		"URL: https://example.com/",
		"TITLE: Example",
		"SNAPSHOT_ID: 7",
		"h1: Welcome",
		"p: Lorem ipsum",
		"[1] link \"More info\" → /info",
		"[2] textbox \"Search\" (value: \"old query\")",
		"[3] checkbox \"Subscribe\" (checked)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("Format output missing %q\n---\n%s\n---", want, out)
		}
	}
}

func TestFormat_NoInteractive(t *testing.T) {
	snap := &Snapshot{
		SnapshotID: 1,
		URL:        "about:blank",
		Title:      "",
	}
	out := snap.Format()
	if !strings.Contains(out, "(no interactive elements detected)") {
		t.Errorf("expected empty-page hint, got:\n%s", out)
	}
	if strings.Contains(out, "TITLE:") {
		t.Errorf("empty title should be omitted from output, got:\n%s", out)
	}
}

func TestFormat_TruncatedBlocks(t *testing.T) {
	snap := &Snapshot{
		SnapshotID: 1,
		URL:        "https://example.com",
		TextBlocks: []TextBlock{
			{Type: "p", Text: "first"},
		},
		TruncatedBlocks: true,
	}
	out := snap.Format()
	if !strings.Contains(out, "text blocks truncated") {
		t.Errorf("expected truncation marker, got:\n%s", out)
	}
}

func TestFormatInteractive_UncheckedRendersExplicitly(t *testing.T) {
	unchecked := false
	el := InteractiveElement{
		Ref: 1, Role: "checkbox", Name: "Opt in", Checked: &unchecked,
	}
	got := formatInteractive(el)
	if !strings.Contains(got, "(unchecked)") {
		t.Errorf("expected explicit (unchecked) marker, got %q", got)
	}
}

func TestStaleRefError_Message(t *testing.T) {
	err := &StaleRefError{Ref: 42}
	msg := err.Error()
	if !strings.Contains(msg, "ref 42") {
		t.Errorf("error message should mention ref number, got %q", msg)
	}
	if !strings.Contains(msg, "browser_snapshot") {
		t.Errorf("error message should hint at the recovery action, got %q", msg)
	}
	if !strings.Contains(msg, "Failure: kind=stale_ref") {
		t.Errorf("error message should expose stale_ref failure kind, got %q", msg)
	}
	if !strings.Contains(msg, "Next:") {
		t.Errorf("error message should include a Next step, got %q", msg)
	}
	if !strings.Contains(msg, "fresh ref") {
		t.Errorf("error message should ask for a fresh ref, got %q", msg)
	}
}

// TestSnapshotJS_IsValidJSFunction is a smoke check that the embedded
// JS at least parses as an arrow function body. It catches the common
// regression of breaking the JS with an unescaped backtick or a stray
// raw string.
func TestSnapshotJS_IsValidJSFunction(t *testing.T) {
	if !strings.HasPrefix(snapshotJS, "() => {") {
		t.Errorf("snapshotJS must begin with arrow-function header so rod's Eval treats it as an expression, got prefix %q", snapshotJS[:min(20, len(snapshotJS))])
	}
	if !strings.HasSuffix(strings.TrimSpace(snapshotJS), "}") {
		t.Errorf("snapshotJS must close its function body, got suffix %q", snapshotJS[max(0, len(snapshotJS)-20):])
	}
	// Pair-balance sanity check on braces.
	if strings.Count(snapshotJS, "{") != strings.Count(snapshotJS, "}") {
		t.Errorf("snapshotJS has unbalanced braces: opens=%d closes=%d",
			strings.Count(snapshotJS, "{"), strings.Count(snapshotJS, "}"))
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
