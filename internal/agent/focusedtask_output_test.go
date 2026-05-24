package agent

import (
	"encoding/json"
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
		Report:        `{"task_type":"recall","ok":true,"summary":"two facts","findings":[{"claim":"c1","evidence":"e1","source":"sess:1","confidence":"high"},{"claim":"c2","evidence":"e2","source":"mem:topic"}],"warnings":["partial"],"suggested_next":["read X"]}`,
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
	if result.Findings[1].Evidence != "e2" {
		t.Fatalf("evidence not preserved: %+v", result.Findings)
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
		Err:           errors.New("upstream 500\x1b[31m"),
		LoopErrors:    []string{"retry failed\x00once"},
	}
	result := buildFocusedTaskResult(verifyProfile(), "obj", "focused_v", 1, res)
	if result.OK {
		t.Fatal("runtime error must yield ok=false")
	}
	if result.Error != "upstream 500[31m" {
		t.Fatalf("error field: %q", result.Error)
	}
	if len(result.LoopErrors) != 1 || result.LoopErrors[0] != "retry failedonce" {
		t.Fatalf("loop errors not sanitized: %+v", result.LoopErrors)
	}
}

func TestSanitizeFindings_DropsEmptyClaimsAndTruncatesEvidence(t *testing.T) {
	long := strings.Repeat("e", maxFocusedTaskFindingEvidenceBytes+200)
	in := []FocusedTaskFinding{
		{Claim: "ok", Evidence: long, Source: "file:1"},
		{Claim: "  ", Evidence: "should drop"},
		{Claim: "two", Evidence: "brief", Source: "memory:topic"},
	}
	out, warnings := sanitizeFindings(FocusedTaskRecall, in)
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %+v", warnings)
	}
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
		in[i] = FocusedTaskFinding{Claim: "c", Evidence: "e", Source: "src"}
	}
	out, warnings := sanitizeFindings(FocusedTaskRecall, in)
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %+v", warnings)
	}
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

func TestSanitizeUntrustedText_StripsControlBytesPreservesWhitespace(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "preserves printable + tab/newline/cr",
			in:   "hello\n\ttabbed\r\nworld",
			want: "hello\n\ttabbed\r\nworld",
		},
		{
			name: "drops NUL inside a string",
			in:   "before\x00after",
			want: "beforeafter",
		},
		{
			name: "drops the ESC that starts an ANSI sequence",
			in:   "red \x1b[31mword\x1b[0m end",
			want: "red [31mword[0m end",
		},
		{
			name: "drops DEL",
			in:   "abc\x7fdef",
			want: "abcdef",
		},
		{
			name: "strips a grab-bag of C0 controls",
			in:   "x\x01\x02\x03\x07\x08\x0b\x0c\x0e\x1fz",
			want: "xz",
		},
		{
			name: "clean input passes through unchanged",
			in:   "the quick brown fox",
			want: "the quick brown fox",
		},
		{
			name: "invalid utf8 is normalized",
			in:   "bad:\xff",
			want: "bad:\ufffd",
		},
		{
			name: "empty stays empty",
			in:   "",
			want: "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := sanitizeUntrustedText(c.in); got != c.want {
				t.Errorf("got %q want %q", got, c.want)
			}
		})
	}
}

func TestSanitizeFindings_StripsControlBytesFromEvidence(t *testing.T) {
	// A real attack file might embed ANSI escapes (terminal hijack on
	// trace UIs), NUL bytes (downstream string-handling footgun), or
	// just noise C0 controls. Evidence that's been read from such a
	// file must arrive at the parent agent with these bytes removed.
	in := []FocusedTaskFinding{
		{
			Claim:    "found the secret\x00 in env file",
			Evidence: "line:\n  AWS_KEY=\x1b[31mAKIA...\x1b[0m\x07",
			Source:   "config/\x00leak.env:42",
		},
	}
	out, warnings := sanitizeFindings(FocusedTaskExplore, in)
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %+v", warnings)
	}
	if len(out) != 1 {
		t.Fatalf("expected one finding, got %d", len(out))
	}
	if strings.ContainsAny(out[0].Claim, "\x00\x07\x1b") {
		t.Errorf("claim still contains control bytes: %q", out[0].Claim)
	}
	if strings.ContainsAny(out[0].Evidence, "\x00\x07\x1b") {
		t.Errorf("evidence still contains control bytes: %q", out[0].Evidence)
	}
	if strings.ContainsAny(out[0].Source, "\x00\x07\x1b") {
		t.Errorf("source still contains control bytes: %q", out[0].Source)
	}
	// Whitespace MUST survive — file excerpts depend on it for
	// readability and for the parent to cite by line.
	if !strings.Contains(out[0].Evidence, "\n") {
		t.Errorf("evidence newline was stripped: %q", out[0].Evidence)
	}
}

func TestSanitizeFindings_DowngradesMissingSourceToWarning(t *testing.T) {
	in := []FocusedTaskFinding{
		{Claim: "sourced", Evidence: "line", Source: "session:one"},
		{Claim: "unsupported", Evidence: "looks plausible"},
	}
	out, warnings := sanitizeFindings(FocusedTaskRecall, in)
	if len(out) != 1 || out[0].Claim != "sourced" {
		t.Fatalf("source-less finding should be omitted, got findings=%+v", out)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "omitted finding without source: unsupported") {
		t.Fatalf("missing-source warning = %+v", warnings)
	}
}

func TestSanitizeFindings_DowngradesMissingEvidenceToWarning(t *testing.T) {
	in := []FocusedTaskFinding{
		{Claim: "supported", Evidence: "quoted user preference", Source: "memory:prefs"},
		{Claim: "no evidence", Source: "session:two"},
	}
	out, warnings := sanitizeFindings(FocusedTaskRecall, in)
	if len(out) != 1 || out[0].Claim != "supported" {
		t.Fatalf("evidence-less finding should be omitted, got findings=%+v", out)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "omitted finding without evidence: no evidence") {
		t.Fatalf("missing-evidence warning = %+v", warnings)
	}
}

func TestSanitizeFindings_ReviewRequiresSeverity(t *testing.T) {
	in := []FocusedTaskFinding{
		{Claim: "risk", Evidence: "call path missing test", Source: "internal/a.go:12", Severity: "blocker"},
		{Claim: "summary without severity", Evidence: "looks odd", Source: "internal/b.go:34"},
		{Claim: "invalid severity", Evidence: "maybe risky", Source: "internal/c.go:56", Severity: "unknown"},
	}
	out, warnings := sanitizeFindings(FocusedTaskReview, in)
	if len(out) != 1 || out[0].Claim != "risk" || out[0].Severity != "high" {
		t.Fatalf("review should keep only severitied risk findings, got findings=%+v", out)
	}
	if len(warnings) != 2 ||
		!strings.Contains(warnings[0], "omitted review finding without valid severity: summary without severity") ||
		!strings.Contains(warnings[1], "omitted review finding without valid severity: invalid severity") {
		t.Fatalf("review severity warnings = %+v", warnings)
	}
}

func TestTrimAndCapStringList_AlsoSanitizesControlBytes(t *testing.T) {
	got := trimAndCapStringList([]string{"normal", "bad\x00chars", "ansi\x1b[2Jescape"})
	for _, s := range got {
		if strings.ContainsAny(s, "\x00\x1b") {
			t.Errorf("entry still has control bytes: %q", s)
		}
	}
	if len(got) != 3 {
		t.Fatalf("entries lost during sanitize: %+v", got)
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

// TestFocusedTaskResult_FieldOrderSurvives8KiBTruncation pins the
// design property that earned FocusedTaskResult its struct-field
// ordering: the parent Loop's per-tool-result truncation (default
// MaxToolResultBytesInContext = 8 KiB) clips from the tail, so when
// the child returns a verbose result the parent agent must still see
// task_type / ok / summary / first findings — the load-bearing
// content. Later findings and metadata may be clipped.
//
// Without this assertion, someone could reorder fields (alphabetize,
// "group related fields", whatever) and silently regress the parent
// agent's view of a large focused-task result without any test
// failing. Field order in Go's encoding/json is a documented
// behavior (declaration order), so the regression vector is real.
func TestFocusedTaskResult_FieldOrderSurvives8KiBTruncation(t *testing.T) {
	// Build a result that will marshal past 8 KiB: 20 findings each
	// with ~600 bytes of evidence, plus the metadata tail.
	findings := make([]FocusedTaskFinding, 0, maxFocusedTaskFindings)
	for i := 0; i < maxFocusedTaskFindings; i++ {
		findings = append(findings, FocusedTaskFinding{
			Claim:    "the implementation has property " + strings.Repeat("X", 20),
			Evidence: strings.Repeat("evidence-line ", 40),
			Source:   "internal/agent/file.go:" + strings.Repeat("9", 3),
		})
	}
	toolCalls := make([]subagentToolCall, 0, maxFocusedTaskToolCalls)
	for i := 0; i < maxFocusedTaskToolCalls; i++ {
		toolCalls = append(toolCalls, subagentToolCall{
			Tool: "read_file",
			Args: map[string]any{"path": "internal/agent/file_" + strings.Repeat("z", 20) + ".go"},
		})
	}
	result := FocusedTaskResult{
		TaskType:       FocusedTaskExplore,
		OK:             true,
		Summary:        "located 20 implementations across the agent package",
		Findings:       findings,
		Objective:      "find every X in the agent package",
		ChildSessionID: "focused_abc123",
		TurnEndReason:  "completed",
		Depth:          1,
		Usage:          subagentUsage{InputTokens: 1234, OutputTokens: 567},
		ToolCalls:      toolCalls,
	}

	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if len(raw) <= MaxToolResultBytesInContext {
		t.Fatalf("test self-check: result %d bytes is not large enough to exercise truncation (need > %d)", len(raw), MaxToolResultBytesInContext)
	}

	truncated := truncateForContext(string(raw), MaxToolResultBytesInContext)

	// Critical fields the parent agent MUST still see after truncation.
	mustContain := []string{
		`"task_type":"explore"`,
		`"ok":true`,
		`"summary":"located 20 implementations`,
		// First finding's claim — proves at least one finding survived.
		`"the implementation has property`,
	}
	for _, needle := range mustContain {
		if !strings.Contains(truncated, needle) {
			t.Errorf("truncated result missing %q\n--- truncated (last 400 bytes) ---\n%s", needle, lastN(truncated, 400))
		}
	}

	// The truncation marker must be present so the model knows content
	// was clipped (otherwise it might assume the JSON is complete and
	// emit follow-ups based on missing fields).
	if !strings.Contains(truncated, "more bytes truncated") {
		t.Errorf("expected truncation marker in result; got tail:\n%s", lastN(truncated, 200))
	}
}

func lastN(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "..." + s[len(s)-n:]
}
