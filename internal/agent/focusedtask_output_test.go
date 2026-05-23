package agent

import (
	"errors"
	"strings"
	"testing"

	"github.com/affinefoundation/affent/internal/sse"
)

func TestParseFocusedTaskOutput_PlainJSON(t *testing.T) {
	raw := `{"task_type":"recall","ok":true,"summary":"found 1","findings":[{"claim":"x","evidence":"e","source":"s","confidence":"high"}]}`
	out, err := parseFocusedTaskOutput(raw)
	if err != nil {
		t.Fatalf("expected JSON to parse, got %v", err)
	}
	if out.TaskType != "recall" || out.Summary != "found 1" || len(out.Findings) != 1 {
		t.Fatalf("unexpected parse: %+v", out)
	}
}

func TestParseFocusedTaskOutput_FencedJSON(t *testing.T) {
	raw := "Here is the result:\n```json\n{\"task_type\":\"explore\",\"summary\":\"ok\"}\n```\nDone."
	out, err := parseFocusedTaskOutput(raw)
	if err != nil {
		t.Fatalf("expected fenced JSON to parse, got %v", err)
	}
	if out.TaskType != "explore" || out.Summary != "ok" {
		t.Fatalf("unexpected parse: %+v", out)
	}
}

func TestParseFocusedTaskOutput_FencedJSONNoLanguageTag(t *testing.T) {
	raw := "```\n{\"task_type\":\"verify\",\"summary\":\"pass\"}\n```"
	out, err := parseFocusedTaskOutput(raw)
	if err != nil {
		t.Fatalf("expected plain fenced block to parse, got %v", err)
	}
	if out.TaskType != "verify" {
		t.Fatalf("unexpected parse: %+v", out)
	}
}

func TestParseFocusedTaskOutput_ProseWithBalancedBraces(t *testing.T) {
	raw := `I looked at the files. Here is my answer: {"task_type":"explore","summary":"three matches"}. Hope that helps.`
	out, err := parseFocusedTaskOutput(raw)
	if err != nil {
		t.Fatalf("expected balanced JSON span to parse, got %v", err)
	}
	if out.TaskType != "explore" || out.Summary != "three matches" {
		t.Fatalf("unexpected parse: %+v", out)
	}
}

func TestParseFocusedTaskOutput_EmptyAndMalformed(t *testing.T) {
	if _, err := parseFocusedTaskOutput(""); err == nil {
		t.Fatal("empty input should error")
	}
	if _, err := parseFocusedTaskOutput("hello world, no JSON here"); err == nil {
		t.Fatal("no-json input should error")
	}
	if _, err := parseFocusedTaskOutput(`{"task_type":"recall"`); err == nil {
		t.Fatal("malformed JSON should error")
	}
}

func TestExtractBalancedJSONObject_BraceInString(t *testing.T) {
	raw := `noise {"a":"value with } inside","b":1} trailing`
	got, ok := extractBalancedJSONObject(raw)
	if !ok {
		t.Fatal("expected to find balanced object")
	}
	want := `{"a":"value with } inside","b":1}`
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestExtractBalancedJSONObject_HandlesEscapedQuotes(t *testing.T) {
	raw := `{"a":"text with \"quote\" and } brace"}`
	got, ok := extractBalancedJSONObject(raw)
	if !ok || got != raw {
		t.Fatalf("expected full input, got %q ok=%v", got, ok)
	}
}

func TestBuildFocusedTaskResult_HappyPath(t *testing.T) {
	profile := recallProfile()
	res := childRunResult{
		Report:        `{"task_type":"recall","ok":true,"summary":"two facts","findings":[{"claim":"c1","evidence":"e1","source":"sess:1","confidence":"high"},{"claim":"c2","source":"mem:topic"}],"warnings":["partial"],"suggested_next":["read X"]}`,
		TurnEndReason: sse.TurnEndCompleted,
		Usage:         subagentUsage{InputTokens: 100, OutputTokens: 50},
	}
	result := buildFocusedTaskResult(profile, "find prefs", "focused_x", 1, res)

	if !result.OK {
		t.Fatalf("expected ok=true: %+v", result)
	}
	if result.TaskType != FocusedTaskRecall || result.ChildSessionID != "focused_x" || result.Depth != 1 {
		t.Fatalf("unexpected runtime metadata: %+v", result)
	}
	if len(result.Findings) != 2 || result.Findings[0].Confidence != "high" {
		t.Fatalf("findings not preserved: %+v", result.Findings)
	}
	if result.Findings[1].Evidence != "" {
		t.Fatalf("missing evidence should remain empty, got %q", result.Findings[1].Evidence)
	}
	if result.Objective != "find prefs" {
		t.Fatalf("objective not propagated: %q", result.Objective)
	}
	if result.Usage.InputTokens != 100 {
		t.Fatalf("usage not propagated: %+v", result.Usage)
	}
}

func TestBuildFocusedTaskResult_ModelCanDowngradeOK(t *testing.T) {
	res := childRunResult{
		Report:        `{"task_type":"verify","ok":false,"summary":"claim falsified","findings":[{"claim":"x","evidence":"go test FAIL","source":"shell"}]}`,
		TurnEndReason: sse.TurnEndCompleted,
	}
	result := buildFocusedTaskResult(verifyProfile(), "check", "focused_v", 1, res)
	if result.OK {
		t.Fatalf("model-downgraded ok should win: %+v", result)
	}
	if result.Summary != "claim falsified" {
		t.Fatalf("summary: %q", result.Summary)
	}
}

func TestBuildFocusedTaskResult_ParseFailureFallback(t *testing.T) {
	res := childRunResult{
		Report:        "I cannot find a way to format JSON. The answer is YES.",
		TurnEndReason: sse.TurnEndCompleted,
	}
	result := buildFocusedTaskResult(exploreProfile(), "obj", "focused_e", 1, res)
	if !result.OK {
		t.Fatalf("runtime success should keep ok=true on parse failure: %+v", result)
	}
	if !contains(result.Warnings, "structured_output_parse_failed") {
		t.Fatalf("expected parse-failure warning: %+v", result.Warnings)
	}
	if !strings.Contains(result.Summary, "I cannot find") {
		t.Fatalf("raw text should land in summary: %q", result.Summary)
	}
	if len(result.Findings) != 0 {
		t.Fatalf("findings must be empty on fallback: %+v", result.Findings)
	}
}

func TestBuildFocusedTaskResult_ChildDidNotComplete(t *testing.T) {
	res := childRunResult{
		Report:        "Partial output, no JSON",
		TurnEndReason: sse.TurnEndMaxTurns,
	}
	result := buildFocusedTaskResult(exploreProfile(), "obj", "focused_e", 1, res)
	if result.OK {
		t.Fatalf("incomplete child must yield ok=false: %+v", result)
	}
	if !contains(result.Warnings, "child_did_not_complete:max_turns") {
		t.Fatalf("expected child_did_not_complete warning: %+v", result.Warnings)
	}
}

func TestBuildFocusedTaskResult_PropagatesRuntimeError(t *testing.T) {
	res := childRunResult{
		Report:        "",
		TurnEndReason: "error",
		Err:           errors.New("upstream 500"),
	}
	result := buildFocusedTaskResult(verifyProfile(), "obj", "focused_v", 1, res)
	if result.OK {
		t.Fatal("runtime error must yield ok=false")
	}
	if result.Error != "upstream 500" {
		t.Fatalf("error field: %q", result.Error)
	}
}

func TestSanitizeFindings_DropsEmptyClaimsAndTruncatesEvidence(t *testing.T) {
	long := strings.Repeat("e", maxFocusedTaskFindingEvidenceBytes+200)
	in := []FocusedTaskFinding{
		{Claim: "ok", Evidence: long},
		{Claim: "  ", Evidence: "should drop"},
		{Claim: "two"},
	}
	out := sanitizeFindings(in)
	if len(out) != 2 {
		t.Fatalf("expected 2 findings (empty-claim dropped), got %d: %+v", len(out), out)
	}
	if len(out[0].Evidence) > maxFocusedTaskFindingEvidenceBytes+32 {
		// previewN can append "…(omitted N bytes)" style suffix; allow small overhead.
		t.Fatalf("evidence too long: %d bytes", len(out[0].Evidence))
	}
}

func TestSanitizeFindings_CapsCount(t *testing.T) {
	in := make([]FocusedTaskFinding, maxFocusedTaskFindings+5)
	for i := range in {
		in[i] = FocusedTaskFinding{Claim: "c"}
	}
	out := sanitizeFindings(in)
	if len(out) != maxFocusedTaskFindings {
		t.Fatalf("expected cap to %d, got %d", maxFocusedTaskFindings, len(out))
	}
}

func TestTrimAndCapStringList_DropsBlankAndCaps(t *testing.T) {
	in := []string{"a", "  ", "b", ""}
	got := trimAndCapStringList(in)
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("trim drops blanks: %+v", got)
	}
	big := make([]string, maxFocusedTaskListEntries+3)
	for i := range big {
		big[i] = "x"
	}
	if len(trimAndCapStringList(big)) != maxFocusedTaskListEntries {
		t.Fatalf("expected cap to %d", maxFocusedTaskListEntries)
	}
}

func TestNormalizeSeverityAndConfidence(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"low", "low"},
		{"L", "low"},
		{"Medium", "medium"},
		{"moderate", "medium"},
		{"High", "high"},
		{"critical", "high"},
		{"", ""},
		{"unknown", "unknown"},
	}
	for _, c := range cases {
		if got := normalizeSeverity(c.in); got != c.want {
			t.Errorf("normalizeSeverity(%q)=%q want %q", c.in, got, c.want)
		}
	}
	if normalizeConfidence("Med") != "medium" || normalizeConfidence("h") != "high" {
		t.Fatal("normalizeConfidence aliases")
	}
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
