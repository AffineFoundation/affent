package agent

import (
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/affinefoundation/affent/internal/sse"
)

// Output-shaping limits for focused tasks. Kept small so structured
// responses don't blow the parent's per-tool-result truncation cap and
// the parent agent sees the most useful fields first.
const (
	// maxFocusedTaskSummaryBytes caps the summary string. The summary
	// is the single most-read field after task_type/ok, so it lands
	// well within the parent's 8 KiB truncation window even when the
	// rest of the response is large.
	maxFocusedTaskSummaryBytes = 2000
	// maxFocusedTaskFindingEvidenceBytes caps inline evidence per
	// finding. Findings should point at sources rather than inline
	// large blobs.
	maxFocusedTaskFindingEvidenceBytes = 1000
	// maxFocusedTaskFindings caps the number of findings returned.
	// Twenty is far more than any realistic focused task should need;
	// the cap exists to bound the response size, not to shape behavior.
	maxFocusedTaskFindings = 20
	// maxFocusedTaskListEntries caps not_found/warnings/suggested_next.
	maxFocusedTaskListEntries = 20
	// maxFocusedTaskToolCalls caps the surfaced child tool calls so
	// the response stays bounded even after a max-budget child run.
	maxFocusedTaskToolCalls = 20
)

// FocusedTaskFinding is one structured fact the child found. Every
// finding must carry evidence the parent can verify; missing evidence
// downgrades the parent's trust in that finding.
//
// Severity is review-only (low/medium/high), Confidence is recall/
// research-only. Both stay optional so the same schema works for all
// task types without forcing irrelevant fields.
type FocusedTaskFinding struct {
	Claim      string `json:"claim"`
	Evidence   string `json:"evidence,omitempty"`
	Source     string `json:"source,omitempty"`
	Severity   string `json:"severity,omitempty"`
	Confidence string `json:"confidence,omitempty"`
}

// FocusedTaskResult is the structured response a run_task call hands
// back to the parent agent. Field ordering is deliberate: the parent's
// per-tool-result truncation (default 8 KiB) clips from the tail, so
// task_type / ok / summary / findings are guaranteed to land in
// context even when usage/tool_calls are clipped off.
type FocusedTaskResult struct {
	TaskType      FocusedTaskKind      `json:"task_type"`
	OK            bool                 `json:"ok"`
	Summary       string               `json:"summary"`
	Findings      []FocusedTaskFinding `json:"findings"`
	NotFound      []string             `json:"not_found"`
	Warnings      []string             `json:"warnings"`
	SuggestedNext []string             `json:"suggested_next"`

	Objective      string `json:"objective,omitempty"`
	ChildSessionID string `json:"child_session_id"`
	TurnEndReason  string `json:"turn_end_reason"`
	Depth          int    `json:"depth"`

	Error      string   `json:"error,omitempty"`
	LoopErrors []string `json:"loop_errors,omitempty"`

	Usage     subagentUsage      `json:"usage"`
	ToolCalls []subagentToolCall `json:"tool_calls,omitempty"`
}

// focusedTaskRawOutput is what we expect the child to emit as its final
// message. Only the model-driven fields appear here; runtime metadata
// (child_session_id, usage, etc.) is filled in by the runtime in
// buildFocusedTaskResult and overrides anything the model wrote.
type focusedTaskRawOutput struct {
	TaskType      string               `json:"task_type"`
	OK            *bool                `json:"ok,omitempty"`
	Summary       string               `json:"summary"`
	Findings      []FocusedTaskFinding `json:"findings"`
	NotFound      []string             `json:"not_found"`
	Warnings      []string             `json:"warnings"`
	SuggestedNext []string             `json:"suggested_next"`
}

// parseFocusedTaskOutput is the JSON-preferred + text-fallback parser
// the user chose. It tries three increasingly forgiving extraction
// strategies in order: raw, fenced ```json block, balanced brace span.
// All three failing returns an error so the caller can surface the raw
// report under structured_output_parse_failed instead.
func parseFocusedTaskOutput(raw string) (focusedTaskRawOutput, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return focusedTaskRawOutput{}, errors.New("empty response")
	}
	if out, err := tryParseFocusedTaskJSON([]byte(raw)); err == nil {
		return out, nil
	}
	if block, ok := extractFencedJSON(raw); ok {
		if out, err := tryParseFocusedTaskJSON([]byte(block)); err == nil {
			return out, nil
		}
	}
	if block, ok := extractBalancedJSONObject(raw); ok {
		if out, err := tryParseFocusedTaskJSON([]byte(block)); err == nil {
			return out, nil
		}
	}
	return focusedTaskRawOutput{}, errors.New("could not parse JSON object from response")
}

func tryParseFocusedTaskJSON(b []byte) (focusedTaskRawOutput, error) {
	var out focusedTaskRawOutput
	dec := json.NewDecoder(strings.NewReader(string(b)))
	// We don't set DisallowUnknownFields because models commonly emit
	// extra fields under prose-like names ("notes", "explanation") that
	// don't change the schema's meaning. Strict mode would reject those
	// otherwise-correct payloads.
	if err := dec.Decode(&out); err != nil {
		return focusedTaskRawOutput{}, err
	}
	return out, nil
}

// extractFencedJSON looks for the first ```json ... ``` (or plain
// ```...```) fenced block and returns its contents. Models love
// wrapping JSON in fences even when told not to; this gives the parser
// a second chance before falling back to brace scanning.
func extractFencedJSON(s string) (string, bool) {
	lower := strings.ToLower(s)
	start := strings.Index(lower, "```json")
	if start >= 0 {
		body := s[start+len("```json"):]
		end := strings.Index(body, "```")
		if end >= 0 {
			return strings.TrimSpace(body[:end]), true
		}
	}
	start = strings.Index(s, "```")
	if start < 0 {
		return "", false
	}
	body := s[start+3:]
	end := strings.Index(body, "```")
	if end < 0 {
		return "", false
	}
	return strings.TrimSpace(body[:end]), true
}

// extractBalancedJSONObject scans for the first balanced { ... } span
// in s, treating string literals and their escapes correctly so a
// brace inside a quoted string does not confuse the matcher. Returns
// the substring (including the outer braces) and true on success.
func extractBalancedJSONObject(s string) (string, bool) {
	first := strings.IndexByte(s, '{')
	if first < 0 {
		return "", false
	}
	depth := 0
	inString := false
	escape := false
	for i := first; i < len(s); i++ {
		c := s[i]
		if inString {
			if escape {
				escape = false
				continue
			}
			if c == '\\' {
				escape = true
				continue
			}
			if c == '"' {
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[first : i+1], true
			}
		}
	}
	return "", false
}

// buildFocusedTaskResult shapes the raw child output into the final
// structured response the parent agent receives. It is the single
// place that reconciles model-emitted JSON with runtime metadata —
// runtime metadata always wins on identity fields (task_type,
// child_session_id, turn_end_reason) while the model owns the
// content fields (summary, findings, not_found, warnings).
func buildFocusedTaskResult(profile FocusedTaskProfile, objective, childID string, depth int, res childRunResult) FocusedTaskResult {
	result := FocusedTaskResult{
		TaskType:       profile.Kind,
		Objective:      objective,
		ChildSessionID: childID,
		TurnEndReason:  res.TurnEndReason,
		Depth:          depth,
		Usage:          res.Usage,
		ToolCalls:      capToolCalls(res.ToolCalls),
	}

	childCompleted := res.Err == nil && res.TurnEndReason == sse.TurnEndCompleted

	if !childCompleted {
		// Hard child-side failure (drain error, ctx cancel, max_turns
		// hit, error turn end). Parent gets enough metadata to decide
		// whether to retry / narrow / give up; no model content is
		// trusted under this branch.
		result.OK = false
		result.Summary = sanitizeUntrustedText(previewN(strings.TrimSpace(res.Report), maxFocusedTaskSummaryBytes))
		if result.Summary == "" {
			result.Summary = "focused task did not complete: " + nonEmpty(res.TurnEndReason, "unknown")
		}
		result.Warnings = []string{"child_did_not_complete:" + nonEmpty(res.TurnEndReason, "unknown")}
		if res.Err != nil {
			result.Error = sanitizeUntrustedText(previewN(strings.TrimSpace(res.Err.Error()), maxFocusedTaskFindingEvidenceBytes))
		}
		if len(res.LoopErrors) > 0 {
			result.LoopErrors = trimAndCapStringList(res.LoopErrors)
		}
		return result
	}

	parsed, parseErr := parseFocusedTaskOutput(res.Report)
	if parseErr != nil {
		// JSON-preferred + text-fallback (the user's chosen output
		// strategy). The parent can still consume the result; it just
		// loses the structured slices and is warned to treat summary
		// as free-form.
		result.OK = true
		result.Summary = sanitizeUntrustedText(previewN(strings.TrimSpace(res.Report), maxFocusedTaskSummaryBytes))
		result.Warnings = []string{"structured_output_parse_failed"}
		if len(res.LoopErrors) > 0 {
			result.LoopErrors = trimAndCapStringList(res.LoopErrors)
		}
		return result
	}

	result.OK = true
	if parsed.OK != nil {
		// Model can downgrade ok (e.g., verify reports the claim is
		// FALSE → ok:false), but cannot upgrade past runtime success.
		result.OK = result.OK && *parsed.OK
	}
	result.Summary = sanitizeUntrustedText(previewN(strings.TrimSpace(parsed.Summary), maxFocusedTaskSummaryBytes))
	var findingWarnings []string
	result.Findings, findingWarnings = sanitizeFindings(profile.Kind, parsed.Findings)
	result.NotFound = trimAndCapStringList(parsed.NotFound)
	result.Warnings = trimAndCapStringList(append(parsed.Warnings, findingWarnings...))
	result.SuggestedNext = trimAndCapStringList(parsed.SuggestedNext)
	if len(res.LoopErrors) > 0 {
		result.LoopErrors = trimAndCapStringList(res.LoopErrors)
	}
	return result
}

func sanitizeFindings(kind FocusedTaskKind, in []FocusedTaskFinding) ([]FocusedTaskFinding, []string) {
	if len(in) == 0 {
		return nil, nil
	}
	if len(in) > maxFocusedTaskFindings {
		in = in[:maxFocusedTaskFindings]
	}
	out := make([]FocusedTaskFinding, 0, len(in))
	var warnings []string
	for _, f := range in {
		claim := sanitizeUntrustedText(strings.TrimSpace(f.Claim))
		if claim == "" {
			continue
		}
		source := sanitizeUntrustedText(strings.TrimSpace(f.Source))
		if source == "" {
			warnings = append(warnings, "omitted finding without source: "+previewN(claim, 160))
			continue
		}
		evidence := sanitizeUntrustedText(previewN(strings.TrimSpace(f.Evidence), maxFocusedTaskFindingEvidenceBytes))
		if evidence == "" {
			warnings = append(warnings, "omitted finding without evidence: "+previewN(claim, 160))
			continue
		}
		severity := normalizeSeverity(f.Severity)
		if kind == FocusedTaskReview && !validFocusedTaskSeverity(severity) {
			warnings = append(warnings, "omitted review finding without valid severity: "+previewN(claim, 160))
			continue
		}
		out = append(out, FocusedTaskFinding{
			Claim:      claim,
			Evidence:   evidence,
			Source:     source,
			Severity:   severity,
			Confidence: normalizeConfidence(f.Confidence),
		})
	}
	if len(out) == 0 {
		out = nil
	}
	return out, trimAndCapStringList(warnings)
}

func validFocusedTaskSeverity(severity string) bool {
	switch severity {
	case "low", "medium", "high":
		return true
	default:
		return false
	}
}

func trimAndCapStringList(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	if len(in) > maxFocusedTaskListEntries {
		in = in[:maxFocusedTaskListEntries]
	}
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = sanitizeUntrustedText(strings.TrimSpace(s))
		if s == "" {
			continue
		}
		out = append(out, s)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// sanitizeUntrustedText scrubs C0 control bytes that have no place in
// human-readable evidence quoted to the parent agent. Preserves \t \n
// \r so multi-line file excerpts and indented snippets stay useful;
// drops everything else under 0x20 (notably 0x1B / ESC which starts
// ANSI escape sequences) and 0x7F / DEL.
//
// This is byte-level hygiene, not semantic injection scrubbing.
// Content that says "ignore previous instructions" passes through
// verbatim — the parent agent's system-prompt rule that tool outputs
// are untrusted is the defense layer for that. Sanitizing for
// semantic content here would create false positives on legitimate
// evidence ("the test case explicitly checks the 'ignore previous'
// jailbreak phrase") and a false sense of safety.
//
// Invalid UTF-8 bytes are folded to U+FFFD by Go's `range` rune
// decoding, which normalizes downstream string handling without
// having to write a separate UTF-8 validator.
func sanitizeUntrustedText(s string) string {
	if s == "" {
		return s
	}
	clean := utf8.ValidString(s)
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\t' || c == '\n' || c == '\r' {
			continue
		}
		if c < 0x20 || c == 0x7F {
			clean = false
			break
		}
	}
	if clean {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\t' || r == '\n' || r == '\r':
			b.WriteRune(r)
		case r < 0x20, r == 0x7F:
			// dropped: C0 controls (incl. NUL and ESC) and DEL
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func capToolCalls(in []subagentToolCall) []subagentToolCall {
	if len(in) <= maxFocusedTaskToolCalls {
		return in
	}
	return in[:maxFocusedTaskToolCalls]
}

// normalizeSeverity maps various model-emitted severity strings to a
// canonical low/medium/high. Empty / unrecognized values pass through
// unchanged so we don't silently rewrite something we didn't expect.
func normalizeSeverity(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "":
		return ""
	case "low", "l":
		return "low"
	case "medium", "med", "moderate", "m":
		return "medium"
	case "high", "h", "critical", "crit", "blocker":
		return "high"
	default:
		return strings.TrimSpace(s)
	}
}

// normalizeConfidence maps model-emitted confidence to low/medium/high.
// Same passthrough rule as normalizeSeverity for unrecognized values.
func normalizeConfidence(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "":
		return ""
	case "low", "l":
		return "low"
	case "medium", "med", "moderate", "m":
		return "medium"
	case "high", "h":
		return "high"
	default:
		return strings.TrimSpace(s)
	}
}

func nonEmpty(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}
