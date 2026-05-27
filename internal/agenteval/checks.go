package agenteval

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/affinefoundation/affent/internal/sse"
)

// ToolCalled passes when the agent invoked the named tool at least
// once during the run. Used to pin "the agent used edit_file" /
// "the agent used go test" requirements.
//
// argMatcher (optional) is an extra predicate over the tool's
// JSON-decoded args: when non-nil, only tool calls whose args satisfy
// it count. Lets a Check distinguish `read_file path=README.md` from
// `read_file path=other.txt`. Pass nil to match any invocation.
func ToolCalled(toolName string, argMatcher func(args map[string]any) bool) Check {
	return Check{
		Name: "tool_called:" + toolName,
		Eval: func(t Trace) CheckResult {
			for _, c := range t.Tools {
				if c.Tool != toolName {
					continue
				}
				if argMatcher == nil || argMatcher(c.Args) {
					return CheckResult{Pass: true, Detail: "matched call_id=" + c.CallID}
				}
			}
			return CheckResult{
				Pass:   false,
				Detail: fmt.Sprintf("expected at least one %q invocation, got %d tool calls (%s)", toolName, len(t.Tools), toolNamesSummary(t.Tools)),
			}
		},
	}
}

func ToolCalledAtLeast(toolName string, min int) Check {
	return Check{
		Name: fmt.Sprintf("tool_called_at_least:%s:%d", toolName, min),
		Eval: func(t Trace) CheckResult {
			count := 0
			for _, c := range t.Tools {
				if c.Tool == toolName {
					count++
				}
			}
			if count >= min {
				return CheckResult{Pass: true, Detail: fmt.Sprintf("%s calls=%d", toolName, count)}
			}
			return CheckResult{
				Pass:   false,
				Detail: fmt.Sprintf("expected at least %d %q invocation(s), got %d tool calls (%s)", min, toolName, count, toolNamesSummary(t.Tools)),
			}
		},
	}
}

func TraceEventCountAtLeast(eventType string, min int) Check {
	return Check{
		Name: fmt.Sprintf("trace_event_count_at_least:%s:%d", eventType, min),
		Eval: func(t Trace) CheckResult {
			got := 0
			if t.RawTypes != nil {
				got = t.RawTypes[eventType]
			}
			if got >= min {
				return CheckResult{Pass: true, Detail: fmt.Sprintf("%s=%d", eventType, got)}
			}
			return CheckResult{
				Pass:   false,
				Detail: fmt.Sprintf("expected at least %d %q event(s), got %d; observed=%v", min, eventType, got, t.RawTypes),
			}
		},
	}
}

func ConversationRepairStatsAtLeast(field string, min int) Check {
	return Check{
		Name: fmt.Sprintf("conversation_repair_stats_at_least:%s:%d", field, min),
		Eval: func(t Trace) CheckResult {
			got, ok := conversationRepairStatsField(t.ConversationRepairs, field)
			if !ok {
				return CheckResult{Pass: false, Detail: fmt.Sprintf("unknown conversation repair stats field %q", field)}
			}
			if got >= min {
				return CheckResult{Pass: true, Detail: fmt.Sprintf("%s=%d", field, got)}
			}
			return CheckResult{
				Pass:   false,
				Detail: fmt.Sprintf("%s=%d, want >= %d; repairs=%v", field, got, min, conversationRepairSummaryForCheck(t.ConversationRepairs)),
			}
		},
	}
}

func ConversationRepairKindAtLeast(kind string, min int) Check {
	return Check{
		Name: fmt.Sprintf("conversation_repair_kind_at_least:%s:%d", kind, min),
		Eval: func(t Trace) CheckResult {
			counts := conversationRepairKindCounts(t.ConversationRepairs)
			got := counts[kind]
			if got >= min {
				return CheckResult{Pass: true, Detail: fmt.Sprintf("%s=%d", kind, got)}
			}
			return CheckResult{
				Pass:   false,
				Detail: fmt.Sprintf("%s=%d, want >= %d; repair_kinds=%v", kind, got, min, counts),
			}
		},
	}
}

func conversationRepairStatsField(repairs []sse.ConversationRepairedPayload, field string) (int, bool) {
	field = strings.TrimSpace(field)
	switch field {
	case "events":
		return len(repairs), true
	case "missing_tool_results":
		total := 0
		for _, repair := range repairs {
			total += repair.MissingToolResults
		}
		return total, true
	case "duplicate_tool_results":
		total := 0
		for _, repair := range repairs {
			total += repair.DuplicateToolResults
		}
		return total, true
	case "unexpected_tool_results":
		total := 0
		for _, repair := range repairs {
			total += repair.UnexpectedToolResults
		}
		return total, true
	default:
		return 0, false
	}
}

func conversationRepairKindCounts(repairs []sse.ConversationRepairedPayload) map[string]int {
	counts := map[string]int{}
	for _, repair := range repairs {
		kind := strings.TrimSpace(repair.FailureKind)
		if kind == "" {
			kind = "unknown"
		}
		counts[kind]++
	}
	return counts
}

func conversationRepairSummaryForCheck(repairs []sse.ConversationRepairedPayload) string {
	if len(repairs) == 0 {
		return "none"
	}
	var parts []string
	for _, field := range []string{"events", "missing_tool_results", "duplicate_tool_results", "unexpected_tool_results"} {
		if got, ok := conversationRepairStatsField(repairs, field); ok && got > 0 {
			parts = append(parts, fmt.Sprintf("%s=%d", field, got))
		}
	}
	if len(parts) == 0 {
		return "events=0"
	}
	return strings.Join(parts, ",")
}

// ToolArgContainsAtLeast passes when at least min calls to toolName have an
// argument field whose string representation contains substr. It gives
// disambiguator-preservation evals a named, diagnostic check instead of hiding
// the requirement inside an anonymous ToolCalled matcher.
func ToolArgContainsAtLeast(toolName, argName, substr string, min int) Check {
	return Check{
		Name: fmt.Sprintf("tool_arg_contains_at_least:%s:%s:%s:%d", toolName, argName, previewSubstr(substr, 24), min),
		Eval: func(t Trace) CheckResult {
			count := 0
			var callIDs []string
			var observed []string
			for _, c := range t.Tools {
				if c.Tool != toolName {
					continue
				}
				value, ok := c.Args[argName]
				if !ok {
					observed = append(observed, fmt.Sprintf("%s=<missing>", c.CallID))
					continue
				}
				text := fmt.Sprint(value)
				if len(observed) < 3 {
					observed = append(observed, fmt.Sprintf("%s=%q", c.CallID, previewSubstr(text, 80)))
				}
				if strings.Contains(text, substr) {
					count++
					callIDs = append(callIDs, c.CallID)
				}
			}
			if count >= min {
				return CheckResult{Pass: true, Detail: fmt.Sprintf("%s.%s contains %q in %d call(s): %v", toolName, argName, substr, count, callIDs)}
			}
			return CheckResult{
				Pass:   false,
				Detail: fmt.Sprintf("expected at least %d %q call(s) with arg %q containing %q, got %d; observed=%v", min, toolName, argName, substr, count, observed),
			}
		},
	}
}

func ToolCalledAtMost(toolName string, max int) Check {
	return ToolCalledAtMostMatching(toolName, max, nil)
}

// ToolCalledAtMostMatching passes when at most max calls to toolName match
// argMatcher. It is useful for recovery evals where a single failed attempt is
// acceptable, but repeating the same broken URL/ref/query is not.
func ToolCalledAtMostMatching(toolName string, max int, argMatcher func(args map[string]any) bool) Check {
	return Check{
		Name: fmt.Sprintf("tool_called_at_most:%s:%d", toolName, max),
		Eval: func(t Trace) CheckResult {
			count := 0
			var callIDs []string
			for _, c := range t.Tools {
				if c.Tool != toolName {
					continue
				}
				if argMatcher != nil && !argMatcher(c.Args) {
					continue
				}
				count++
				callIDs = append(callIDs, c.CallID)
			}
			if count <= max {
				return CheckResult{Pass: true, Detail: fmt.Sprintf("%s calls=%d", toolName, count)}
			}
			return CheckResult{
				Pass:   false,
				Detail: fmt.Sprintf("expected at most %d matching %q invocation(s), got %d call_ids=%v", max, toolName, count, callIDs),
			}
		},
	}
}

// ToolNotCalled passes when the agent never invoked the named tool.
// Used to pin "the agent must not edit tests" / "the agent must not
// run broad shell scans".
//
// argMatcher (optional) restricts the prohibition: only calls whose
// args match count as a violation. Lets a Check forbid
// `edit_file path=*_test.go` without forbidding edit_file outright.
func ToolNotCalled(toolName string, argMatcher func(args map[string]any) bool) Check {
	return Check{
		Name: "tool_not_called:" + toolName,
		Eval: func(t Trace) CheckResult {
			for _, c := range t.Tools {
				if c.Tool != toolName {
					continue
				}
				if argMatcher == nil || argMatcher(c.Args) {
					return CheckResult{
						Pass:   false,
						Detail: fmt.Sprintf("found forbidden %q call (call_id=%s args=%v)", toolName, c.CallID, c.Args),
					}
				}
			}
			return CheckResult{Pass: true}
		},
	}
}

// ToolResultContains passes when a named tool returned output containing
// substr. It is useful for runtime guard evals: the model may attempt a
// bad call, but the loop must surface a corrective tool result instead
// of crashing or spinning.
func ToolResultContains(toolName, substr string) Check {
	return Check{
		Name: "tool_result_contains:" + toolName + ":" + previewSubstr(substr, 32),
		Eval: func(t Trace) CheckResult {
			for _, c := range t.Tools {
				if c.Tool != toolName {
					continue
				}
				if strings.Contains(c.Result, substr) {
					return CheckResult{Pass: true, Detail: "matched call_id=" + c.CallID}
				}
			}
			return CheckResult{
				Pass:   false,
				Detail: fmt.Sprintf("expected %q result to contain %q; tools=%s", toolName, substr, toolNamesSummary(t.Tools)),
			}
		},
	}
}

func ToolResultTruncated(toolName string) Check {
	return Check{
		Name: "tool_result_truncated:" + toolName,
		Eval: func(t Trace) CheckResult {
			for _, c := range t.Tools {
				if c.Tool == toolName && c.ResultTruncated {
					return CheckResult{
						Pass:   true,
						Detail: fmt.Sprintf("matched call_id=%s omitted=%d cap=%d", c.CallID, c.ResultOmittedBytes, c.ResultCapBytes),
					}
				}
			}
			return CheckResult{
				Pass:   false,
				Detail: fmt.Sprintf("expected %q result to be event-truncated; tools=%s", toolName, toolNamesSummary(t.Tools)),
			}
		},
	}
}

func ToolResultArtifact(toolName string) Check {
	return Check{
		Name: "tool_result_artifact:" + toolName,
		Eval: func(t Trace) CheckResult {
			for _, c := range t.Tools {
				if c.Tool != toolName || c.ResultArtifactPath == "" {
					continue
				}
				if err := validateToolResultArtifact(t.WorkspaceDir, c); err != nil {
					return CheckResult{
						Pass:   false,
						Detail: fmt.Sprintf("%q result artifact invalid: %v", toolName, err),
					}
				}
				if t.WorkspaceDir == "" {
					return CheckResult{
						Pass:   true,
						Detail: fmt.Sprintf("matched call_id=%s artifact=%s (path only)", c.CallID, c.ResultArtifactPath),
					}
				}
				return CheckResult{
					Pass:   true,
					Detail: fmt.Sprintf("matched call_id=%s artifact=%s", c.CallID, c.ResultArtifactPath),
				}
			}
			return CheckResult{
				Pass:   false,
				Detail: fmt.Sprintf("expected %q result to expose an artifact path; tools=%s", toolName, toolNamesSummary(t.Tools)),
			}
		},
	}
}

func validateToolResultArtifact(workspace string, c ToolCall) error {
	rel, err := cleanTraceArtifactPath(c.ResultArtifactPath)
	if err != nil {
		return err
	}
	if strings.TrimSpace(workspace) == "" {
		return nil
	}
	workspaceAbs, err := filepath.Abs(workspace)
	if err != nil {
		return fmt.Errorf("resolve workspace: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(workspaceAbs); err == nil {
		workspaceAbs = resolved
	}
	full := filepath.Join(workspaceAbs, rel)
	resolvedFull, err := filepath.EvalSymlinks(full)
	if err != nil {
		return fmt.Errorf("artifact %q not readable: %w", c.ResultArtifactPath, err)
	}
	inside, err := pathWithin(workspaceAbs, resolvedFull)
	if err != nil {
		return err
	}
	if !inside {
		return fmt.Errorf("artifact %q escapes workspace", c.ResultArtifactPath)
	}
	st, err := os.Stat(resolvedFull)
	if err != nil {
		return fmt.Errorf("stat artifact %q: %w", c.ResultArtifactPath, err)
	}
	if st.IsDir() {
		return fmt.Errorf("artifact %q is a directory", c.ResultArtifactPath)
	}
	if c.ResultBytes > 0 && st.Size() != int64(c.ResultBytes) {
		return fmt.Errorf("artifact %q has %d bytes, want result_bytes=%d", c.ResultArtifactPath, st.Size(), c.ResultBytes)
	}
	return nil
}

func cleanTraceArtifactPath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("artifact path is empty")
	}
	rel := filepath.Clean(filepath.FromSlash(path))
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("artifact path %q must be workspace-relative", path)
	}
	if rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("artifact path %q escapes workspace", path)
	}
	return rel, nil
}

func pathWithin(root, candidate string) (bool, error) {
	rel, err := filepath.Rel(root, candidate)
	if err != nil {
		return false, fmt.Errorf("compare artifact path to workspace: %w", err)
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))), nil
}

func ToolRequestRepaired(toolName string) Check {
	return Check{
		Name: "tool_request_repaired:" + toolName,
		Eval: func(t Trace) CheckResult {
			for _, c := range t.Tools {
				if c.Tool == toolName && toolCallRepaired(c) {
					return CheckResult{Pass: true, Detail: fmt.Sprintf("matched call_id=%s notes=%v", c.CallID, c.RepairNotes)}
				}
			}
			return CheckResult{
				Pass:   false,
				Detail: fmt.Sprintf("expected repaired %q request; tools=%s", toolName, toolNamesSummary(t.Tools)),
			}
		},
	}
}

func toolCallRepaired(c ToolCall) bool {
	return c.Canonicalized || c.ArgsRepaired || len(c.RepairNotes) > 0
}

func ToolStatsAtLeast(field string, min int) Check {
	return Check{
		Name: fmt.Sprintf("tool_stats_at_least:%s:%d", field, min),
		Eval: func(t Trace) CheckResult {
			got, ok := toolStatsField(t.ToolStats, field)
			if !ok {
				return CheckResult{Pass: false, Detail: fmt.Sprintf("unknown tool stats field %q", field)}
			}
			if got >= int64(min) {
				return CheckResult{Pass: true, Detail: fmt.Sprintf("%s=%d", field, got)}
			}
			return CheckResult{Pass: false, Detail: fmt.Sprintf("%s=%d, want >= %d", field, got, min)}
		},
	}
}

func ToolRepairKindAtLeast(kind string, min int) Check {
	return Check{
		Name: fmt.Sprintf("tool_repair_kind_at_least:%s:%d", kind, min),
		Eval: func(t Trace) CheckResult {
			stats := t.RepairStats()
			got := stats.ByKind[kind]
			if got >= min {
				return CheckResult{Pass: true, Detail: fmt.Sprintf("%s=%d", kind, got)}
			}
			return CheckResult{
				Pass:   false,
				Detail: fmt.Sprintf("%s=%d, want >= %d; repair_kinds=%v", kind, got, min, stats.ByKind),
			}
		},
	}
}

func ToolFailureKindAtLeast(kind string, min int) Check {
	return Check{
		Name: fmt.Sprintf("tool_failure_kind_at_least:%s:%d", kind, min),
		Eval: func(t Trace) CheckResult {
			counts := t.ToolFailureKindCounts()
			got := counts[kind]
			if got >= min {
				return CheckResult{Pass: true, Detail: fmt.Sprintf("%s=%d", kind, got)}
			}
			return CheckResult{
				Pass:   false,
				Detail: fmt.Sprintf("%s=%d, want >= %d; failure_kinds=%v", kind, got, min, counts),
			}
		},
	}
}

func ToolFailureKindAtMost(kind string, max int) Check {
	return Check{
		Name: fmt.Sprintf("tool_failure_kind_at_most:%s:%d", kind, max),
		Eval: func(t Trace) CheckResult {
			counts := t.ToolFailureKindCounts()
			got := counts[kind]
			if got <= max {
				return CheckResult{Pass: true, Detail: fmt.Sprintf("%s=%d", kind, got)}
			}
			return CheckResult{
				Pass:   false,
				Detail: fmt.Sprintf("%s=%d, want <= %d; failure_kinds=%v", kind, got, max, counts),
			}
		},
	}
}

func LoopDecisionKindAtLeast(kind string, min int) Check {
	return Check{
		Name: fmt.Sprintf("loop_decision_kind_at_least:%s:%d", kind, min),
		Eval: func(t Trace) CheckResult {
			stats := t.LoopDecisionStats(0)
			got := stats.ByKind[kind]
			if got >= min {
				return CheckResult{Pass: true, Detail: fmt.Sprintf("%s=%d", kind, got)}
			}
			return CheckResult{
				Pass:   false,
				Detail: fmt.Sprintf("%s=%d, want >= %d; loop_decision_kinds=%v", kind, got, min, stats.ByKind),
			}
		},
	}
}

func LoopDecisionResultAtLeast(decision string, min int) Check {
	return Check{
		Name: fmt.Sprintf("loop_decision_result_at_least:%s:%d", decision, min),
		Eval: func(t Trace) CheckResult {
			stats := t.LoopDecisionStats(0)
			got := stats.ByDecision[decision]
			if got >= min {
				return CheckResult{Pass: true, Detail: fmt.Sprintf("%s=%d", decision, got)}
			}
			return CheckResult{
				Pass:   false,
				Detail: fmt.Sprintf("%s=%d, want >= %d; loop_decision_results=%v", decision, got, min, stats.ByDecision),
			}
		},
	}
}

func LoopDecisionMatchAtLeast(kind, decision, trigger string, min int) Check {
	return Check{
		Name: fmt.Sprintf("loop_decision_match_at_least:%s:%s:%s:%d", kind, decision, trigger, min),
		Eval: func(t Trace) CheckResult {
			count := 0
			var examples []string
			for _, d := range t.LoopDecisions {
				if kind != "" && d.Kind != kind {
					continue
				}
				if decision != "" && d.Decision != decision {
					continue
				}
				if trigger != "" && d.Trigger != trigger {
					continue
				}
				count++
				if len(examples) < 3 {
					examples = append(examples, formatLoopDecisionExample(d))
				}
			}
			if count >= min {
				return CheckResult{Pass: true, Detail: fmt.Sprintf("matched=%d examples=%v", count, examples)}
			}
			return CheckResult{
				Pass:   false,
				Detail: fmt.Sprintf("matched=%d, want >= %d for kind=%q decision=%q trigger=%q; observed=%v", count, min, kind, decision, trigger, loopDecisionExamples(t.LoopDecisions, 5)),
			}
		},
	}
}

func LoopProtocolFeedsAtLeast(min int) Check {
	return Check{
		Name: fmt.Sprintf("loop_protocol_feeds_at_least:%d", min),
		Eval: func(t Trace) CheckResult {
			stats := t.LoopProtocolFeedStats(0)
			if stats.Count >= min {
				return CheckResult{Pass: true, Detail: fmt.Sprintf("loop_protocol_feeds=%d", stats.Count)}
			}
			return CheckResult{
				Pass:   false,
				Detail: fmt.Sprintf("loop_protocol_feeds=%d, want >= %d", stats.Count, min),
			}
		},
	}
}

func LoopProtocolCalibrationsAtLeast(min int) Check {
	return Check{
		Name: fmt.Sprintf("loop_protocol_calibrations_at_least:%d", min),
		Eval: func(t Trace) CheckResult {
			stats := t.LoopProtocolCalibrationStats(5)
			if stats.Count >= min {
				return CheckResult{Pass: true, Detail: fmt.Sprintf("loop_protocol_calibrations=%d examples=%v", stats.Count, loopProtocolCalibrationExamples(stats.Examples, 3))}
			}
			return CheckResult{
				Pass:   false,
				Detail: fmt.Sprintf("loop_protocol_calibrations=%d, want >= %d; observed=%v", stats.Count, min, loopProtocolCalibrationExamples(t.LoopProtocolCalibrations, 5)),
			}
		},
	}
}

func LoopProtocolCalibrationRequestsAtLeast(min int) Check {
	return Check{
		Name: fmt.Sprintf("loop_protocol_calibration_requests_at_least:%d", min),
		Eval: func(t Trace) CheckResult {
			stats := t.LoopProtocolCalibrationRequestStats(5)
			if stats.Count >= min {
				return CheckResult{Pass: true, Detail: fmt.Sprintf("loop_protocol_calibration_requests=%d examples=%v", stats.Count, loopProtocolCalibrationExamples(stats.Examples, 3))}
			}
			return CheckResult{
				Pass:   false,
				Detail: fmt.Sprintf("loop_protocol_calibration_requests=%d, want >= %d; observed=%v", stats.Count, min, loopProtocolCalibrationExamples(t.LoopProtocolCalibrationRequests, 5)),
			}
		},
	}
}

func LoopProtocolFeedModeAtLeast(mode string, min int) Check {
	return Check{
		Name: fmt.Sprintf("loop_protocol_feed_mode_at_least:%s:%d", mode, min),
		Eval: func(t Trace) CheckResult {
			stats := t.LoopProtocolFeedStats(0)
			got := stats.ByMode[mode]
			if got >= min {
				return CheckResult{Pass: true, Detail: fmt.Sprintf("%s=%d", mode, got)}
			}
			return CheckResult{
				Pass:   false,
				Detail: fmt.Sprintf("%s=%d, want >= %d; loop_protocol_feed_modes=%v", mode, got, min, stats.ByMode),
			}
		},
	}
}

func LoopProtocolFeedMatchAtLeast(mode, planLabelContains, planCurrentStepStatus, planCurrentStep, currentSituation string, min int) Check {
	return LoopProtocolFeedRequirementAtLeast(LoopProtocolFeedRequirement{
		Mode:                  mode,
		PlanLabelContains:     planLabelContains,
		PlanCurrentStepStatus: planCurrentStepStatus,
		PlanCurrentStep:       planCurrentStep,
		CurrentSituation:      currentSituation,
		Min:                   min,
	})
}

func LoopProtocolFeedRequirementAtLeast(req LoopProtocolFeedRequirement) Check {
	min := req.Min
	if min <= 0 {
		min = 1
	}
	name := fmt.Sprintf(
		"loop_protocol_feed_match_at_least:%s:%s:%s:%s:%d",
		checkNamePart(req.Mode),
		checkNamePart(req.PlanLabelContains),
		checkNamePart(req.PlanCurrentStepStatus),
		checkNamePart(req.PlanCurrentStep),
		min,
	)
	var nameParts []string
	for _, part := range []string{
		req.Mode,
		req.PlanLabelContains,
		req.PlanCurrentStepStatus,
		req.PlanCurrentStep,
		req.CurrentSituation,
		req.LastTurnEndReason,
		positiveCheckNamePart("turn_tools", req.MinLastTurnToolRequests),
		positiveCheckNamePart("turn_mem_updates", req.MinLastTurnMemoryUpdates),
		positiveCheckNamePart("turn_mem_search", req.MinLastTurnMemorySearchCalls),
		positiveCheckNamePart("turn_mem_misses", req.MinLastTurnMemorySearchMisses),
		positiveCheckNamePart("turn_session_search", req.MinLastTurnSessionSearchCalls),
		positiveCheckNamePart("turn_loop_guards", req.MinLastTurnLoopGuards),
	} {
		if part != "" {
			nameParts = append(nameParts, checkNamePart(part))
		}
	}
	if len(nameParts) > 0 {
		name = "loop_protocol_feed_match_at_least:" + strings.Join(nameParts, ":") + fmt.Sprintf(":%d", min)
	}
	return Check{
		Name: name,
		Eval: func(t Trace) CheckResult {
			count := 0
			var examples []string
			for _, feed := range t.LoopProtocolFeeds {
				if !loopProtocolFeedMatchesRequirement(feed, req) {
					continue
				}
				count++
				if len(examples) < 3 {
					examples = append(examples, formatLoopProtocolFeedExample(feed))
				}
			}
			if count >= min {
				return CheckResult{Pass: true, Detail: fmt.Sprintf("matched=%d examples=%v", count, examples)}
			}
			return CheckResult{
				Pass: false,
				Detail: fmt.Sprintf(
					"matched=%d, want >= %d for %s; observed=%v",
					count,
					min,
					loopProtocolFeedRequirementSummary(req),
					loopProtocolFeedExamples(t.LoopProtocolFeeds, 5),
				),
			}
		},
	}
}

func loopProtocolFeedMatchesRequirement(feed LoopProtocolFeed, req LoopProtocolFeedRequirement) bool {
	if req.Mode != "" && feed.Mode != req.Mode {
		return false
	}
	if req.PlanLabelContains != "" && !strings.Contains(feed.PlanLabel, req.PlanLabelContains) {
		return false
	}
	if req.PlanCurrentStepStatus != "" && feed.PlanCurrentStepStatus != req.PlanCurrentStepStatus {
		return false
	}
	if req.PlanCurrentStep != "" && !strings.Contains(feed.PlanCurrentStep, req.PlanCurrentStep) {
		return false
	}
	if req.CurrentSituation != "" && !strings.Contains(feed.CurrentSituation, req.CurrentSituation) {
		return false
	}
	if req.LastTurnEndReason != "" && feed.LastTurnEndReason != req.LastTurnEndReason {
		return false
	}
	if req.MinLastTurnToolRequests > 0 && feed.LastTurnToolRequests < req.MinLastTurnToolRequests {
		return false
	}
	if req.MinLastTurnMemoryUpdates > 0 && feed.LastTurnMemoryUpdates < req.MinLastTurnMemoryUpdates {
		return false
	}
	if req.MinLastTurnMemorySearchCalls > 0 && feed.LastTurnMemorySearchCalls < req.MinLastTurnMemorySearchCalls {
		return false
	}
	if req.MinLastTurnMemorySearchMisses > 0 && feed.LastTurnMemorySearchMisses < req.MinLastTurnMemorySearchMisses {
		return false
	}
	if req.MinLastTurnSessionSearchCalls > 0 && feed.LastTurnSessionSearchCalls < req.MinLastTurnSessionSearchCalls {
		return false
	}
	if req.MinLastTurnLoopGuards > 0 && feed.LastTurnLoopGuards < req.MinLastTurnLoopGuards {
		return false
	}
	return true
}

func loopProtocolFeedRequirementSummary(req LoopProtocolFeedRequirement) string {
	var parts []string
	if req.Mode != "" {
		parts = append(parts, fmt.Sprintf("mode=%q", req.Mode))
	}
	if req.PlanLabelContains != "" {
		parts = append(parts, fmt.Sprintf("plan_label_contains=%q", req.PlanLabelContains))
	}
	if req.PlanCurrentStepStatus != "" {
		parts = append(parts, fmt.Sprintf("plan_current_step_status=%q", req.PlanCurrentStepStatus))
	}
	if req.PlanCurrentStep != "" {
		parts = append(parts, fmt.Sprintf("plan_current_step=%q", req.PlanCurrentStep))
	}
	if req.CurrentSituation != "" {
		parts = append(parts, fmt.Sprintf("current_situation=%q", req.CurrentSituation))
	}
	if req.LastTurnEndReason != "" {
		parts = append(parts, fmt.Sprintf("last_turn_end_reason=%q", req.LastTurnEndReason))
	}
	appendMin := func(name string, value int) {
		if value > 0 {
			parts = append(parts, fmt.Sprintf("%s>=%d", name, value))
		}
	}
	appendMin("last_turn_tool_requests", req.MinLastTurnToolRequests)
	appendMin("last_turn_memory_updates", req.MinLastTurnMemoryUpdates)
	appendMin("last_turn_memory_search_calls", req.MinLastTurnMemorySearchCalls)
	appendMin("last_turn_memory_search_misses", req.MinLastTurnMemorySearchMisses)
	appendMin("last_turn_session_search_calls", req.MinLastTurnSessionSearchCalls)
	appendMin("last_turn_loop_guards", req.MinLastTurnLoopGuards)
	if len(parts) == 0 {
		return "any loop protocol feed"
	}
	return strings.Join(parts, " ")
}

func positiveCheckNamePart(label string, value int) string {
	if value <= 0 {
		return ""
	}
	return fmt.Sprintf("%s>=%d", label, value)
}

func LoopProtocolFullFeedAfterCompaction() Check {
	return Check{
		Name: "loop_protocol_full_feed_after_compaction",
		Eval: func(t Trace) CheckResult {
			seenCompaction := false
			compactionIndex := 0
			var observed []string
			for _, ev := range t.EventOrder {
				switch ev.Type {
				case sse.TypeContextCompact:
					seenCompaction = true
					compactionIndex = ev.Index
					if len(observed) < 6 {
						observed = append(observed, fmt.Sprintf("#%d context.compacted reason=%s reactive=%v", ev.Index, ev.ContextReason, ev.ContextReactive))
					}
				case sse.TypeLoopProtocolFeed:
					if len(observed) < 6 {
						observed = append(observed, fmt.Sprintf("#%d loop.protocol_feed mode=%s path=%s", ev.Index, ev.LoopProtocolMode, ev.LoopProtocolPath))
					}
					if seenCompaction && ev.LoopProtocolMode == "full" {
						return CheckResult{Pass: true, Detail: fmt.Sprintf("full feed index=%d after compaction index=%d", ev.Index, compactionIndex)}
					}
				}
			}
			return CheckResult{
				Pass:   false,
				Detail: fmt.Sprintf("expected a full loop.protocol_feed after context.compacted; event_order=%v", observed),
			}
		},
	}
}

func ContextCompactionsAtLeast(min int) Check {
	return Check{
		Name: fmt.Sprintf("context_compactions_at_least:%d", min),
		Eval: func(t Trace) CheckResult {
			stats := t.ContextCompactionStats(0)
			if stats.Count >= min {
				return CheckResult{Pass: true, Detail: fmt.Sprintf("context_compactions=%d", stats.Count)}
			}
			return CheckResult{
				Pass:   false,
				Detail: fmt.Sprintf("context_compactions=%d, want >= %d", stats.Count, min),
			}
		},
	}
}

func ReactiveContextCompactionsAtLeast(min int) Check {
	return Check{
		Name: fmt.Sprintf("reactive_context_compactions_at_least:%d", min),
		Eval: func(t Trace) CheckResult {
			stats := t.ContextCompactionStats(0)
			if stats.Reactive >= min {
				return CheckResult{Pass: true, Detail: fmt.Sprintf("reactive_context_compactions=%d", stats.Reactive)}
			}
			return CheckResult{
				Pass:   false,
				Detail: fmt.Sprintf("reactive_context_compactions=%d, want >= %d; total=%d proactive=%d", stats.Reactive, min, stats.Count, stats.Proactive),
			}
		},
	}
}

func ContextCompactionRemovedMessagesAtLeast(min int) Check {
	return Check{
		Name: fmt.Sprintf("context_compaction_removed_messages_at_least:%d", min),
		Eval: func(t Trace) CheckResult {
			stats := t.ContextCompactionStats(0)
			if stats.RemovedMessages >= min {
				return CheckResult{Pass: true, Detail: fmt.Sprintf("removed_messages=%d", stats.RemovedMessages)}
			}
			return CheckResult{
				Pass:   false,
				Detail: fmt.Sprintf("removed_messages=%d, want >= %d; context_compactions=%d", stats.RemovedMessages, min, stats.Count),
			}
		},
	}
}

func ContextCompactionSummaryContains(substr string) Check {
	return Check{
		Name: fmt.Sprintf("context_compaction_summary_contains:%s", previewSubstr(substr, 32)),
		Eval: func(t Trace) CheckResult {
			var previews []string
			for _, c := range t.ContextCompactions {
				preview := strings.TrimSpace(c.SummaryPreview)
				if preview == "" {
					continue
				}
				if strings.Contains(preview, substr) {
					return CheckResult{Pass: true, Detail: fmt.Sprintf("matched turn_id=%s", c.TurnID)}
				}
				if len(previews) < 3 {
					previews = append(previews, previewSubstr(preview, 120))
				}
			}
			return CheckResult{
				Pass:   false,
				Detail: fmt.Sprintf("expected a context summary preview to contain %q; compactions=%d previews=%v", substr, len(t.ContextCompactions), previews),
			}
		},
	}
}

func ContextCompactionLoopProtocolAnchorContains(substr string) Check {
	return Check{
		Name: fmt.Sprintf("context_compaction_loop_protocol_anchor_contains:%s", previewSubstr(substr, 32)),
		Eval: func(t Trace) CheckResult {
			var anchors []string
			for _, c := range t.ContextCompactions {
				anchor := strings.TrimSpace(c.LoopProtocolAnchor)
				if anchor == "" {
					continue
				}
				if strings.Contains(anchor, substr) {
					return CheckResult{Pass: true, Detail: fmt.Sprintf("matched turn_id=%s", c.TurnID)}
				}
				if len(anchors) < 3 {
					anchors = append(anchors, previewSubstr(anchor, 160))
				}
			}
			return CheckResult{
				Pass:   false,
				Detail: fmt.Sprintf("expected a context compaction loop protocol anchor to contain %q; compactions=%d anchors=%v", substr, len(t.ContextCompactions), anchors),
			}
		},
	}
}

func SourceAccessMatchAtLeast(status, toolName, urlContains, sourceMethod, jsonPath string, min int) Check {
	return SourceAccessMatchWithRequestedAtLeast(status, toolName, urlContains, "", sourceMethod, jsonPath, min)
}

func SourceAccessMatchWithRequestedAtLeast(status, toolName, urlContains, requestedURLContains, sourceMethod, jsonPath string, min int) Check {
	if min <= 0 {
		min = 1
	}
	nameParts := []string{"source_access_match_at_least"}
	for _, part := range []string{status, toolName, urlContains, sourceMethod, jsonPath, fmt.Sprint(min)} {
		part = strings.TrimSpace(part)
		if part == "" {
			part = "*"
		}
		nameParts = append(nameParts, previewSubstr(part, 24))
	}
	if strings.TrimSpace(requestedURLContains) != "" {
		nameParts = append(nameParts[:4], append([]string{"requested=" + previewSubstr(strings.TrimSpace(requestedURLContains), 24)}, nameParts[4:]...)...)
	}
	return Check{
		Name: strings.Join(nameParts, ":"),
		Eval: func(t Trace) CheckResult {
			examples := t.SourceAccessExamples(len(t.Tools))
			count := 0
			var matched []string
			var observed []string
			for _, ex := range examples {
				if sourceAccessRequirementMatches(ex, status, toolName, urlContains, requestedURLContains, sourceMethod, jsonPath) {
					count++
					if len(matched) < 5 {
						matched = append(matched, sourceAccessExampleSummary(ex))
					}
					continue
				}
				if len(observed) < 5 {
					observed = append(observed, sourceAccessExampleSummary(ex))
				}
			}
			if count >= min {
				return CheckResult{Pass: true, Detail: fmt.Sprintf("matched %d source access result(s): %v", count, matched)}
			}
			return CheckResult{
				Pass: false,
				Detail: fmt.Sprintf(
					"expected at least %d SourceAccess result(s) matching status=%q tool=%q url_contains=%q requested_url_contains=%q source_method=%q json_path=%q, got %d; observed=%v",
					min, status, toolName, urlContains, requestedURLContains, sourceMethod, jsonPath, count, observed,
				),
			}
		},
	}
}

func sourceAccessRequirementMatches(ex SourceAccessExample, status, toolName, urlContains, requestedURLContains, sourceMethod, jsonPath string) bool {
	if status = strings.TrimSpace(status); status != "" && ex.Status != status {
		return false
	}
	if toolName = strings.TrimSpace(toolName); toolName != "" && ex.Tool != toolName {
		return false
	}
	if urlContains = strings.TrimSpace(urlContains); urlContains != "" && !strings.Contains(ex.URL, urlContains) {
		return false
	}
	if requestedURLContains = strings.TrimSpace(requestedURLContains); requestedURLContains != "" && !strings.Contains(ex.RequestedURL, requestedURLContains) {
		return false
	}
	if sourceMethod = strings.TrimSpace(sourceMethod); sourceMethod != "" && ex.SourceMethod != sourceMethod {
		return false
	}
	if jsonPath = strings.TrimSpace(jsonPath); jsonPath != "" && ex.JSONPath != jsonPath {
		return false
	}
	return true
}

func sourceAccessExampleSummary(ex SourceAccessExample) string {
	parts := []string{
		fmt.Sprintf("tool#%d", ex.ToolIndex),
		ex.Tool,
		ex.Status,
	}
	if ex.URL != "" {
		parts = append(parts, "url="+previewSubstr(ex.URL, 80))
	}
	if ex.RequestedURL != "" {
		parts = append(parts, "requested="+previewSubstr(ex.RequestedURL, 80))
	}
	if ex.SourceMethod != "" {
		parts = append(parts, "method="+ex.SourceMethod)
	}
	if ex.HTTPStatus != "" {
		parts = append(parts, "http_status="+ex.HTTPStatus)
	}
	if ex.ContentType != "" {
		parts = append(parts, "content_type="+ex.ContentType)
	}
	if ex.JSONPath != "" {
		parts = append(parts, "json_path="+ex.JSONPath)
	}
	if ex.ResultPreview != "" {
		parts = append(parts, "preview="+previewSubstr(ex.ResultPreview, 100))
	}
	return strings.Join(parts, " ")
}

func SessionSearchMatchAtLeast(queryContains, sessionID, snippetContains string, matchedTerms []string, contextIncluded bool, turnIdx int, min int) Check {
	if min <= 0 {
		min = 1
	}
	nameParts := []string{"session_search_match_at_least"}
	for _, part := range []string{queryContains, sessionID, snippetContains, strings.Join(matchedTerms, ","), fmt.Sprint(contextIncluded), fmt.Sprint(turnIdx), fmt.Sprint(min)} {
		part = strings.TrimSpace(part)
		if part == "" {
			part = "*"
		}
		nameParts = append(nameParts, previewSubstr(part, 24))
	}
	return Check{
		Name: strings.Join(nameParts, ":"),
		Eval: func(t Trace) CheckResult {
			examples := t.SessionSearchExamples(len(t.Tools))
			count := 0
			var matched []string
			var observed []string
			for _, ex := range examples {
				if sessionSearchRequirementMatches(ex, queryContains, sessionID, snippetContains, matchedTerms, contextIncluded, turnIdx) {
					count++
					if len(matched) < 5 {
						matched = append(matched, sessionSearchExampleSummary(ex))
					}
					continue
				}
				if len(observed) < 5 {
					observed = append(observed, sessionSearchExampleSummary(ex))
				}
			}
			if count >= min {
				return CheckResult{Pass: true, Detail: fmt.Sprintf("matched %d session_search hit(s): %v", count, matched)}
			}
			return CheckResult{
				Pass: false,
				Detail: fmt.Sprintf(
					"expected at least %d session_search hit(s) matching query_contains=%q session_id=%q snippet_contains=%q matched_terms=%v context_included=%t turn_idx=%d, got %d; observed=%v",
					min, queryContains, sessionID, snippetContains, matchedTerms, contextIncluded, turnIdx, count, observed,
				),
			}
		},
	}
}

func sessionSearchRequirementMatches(ex SessionSearchExample, queryContains, sessionID, snippetContains string, matchedTerms []string, contextIncluded bool, turnIdx int) bool {
	if queryContains = strings.TrimSpace(queryContains); queryContains != "" && !strings.Contains(ex.Query, queryContains) {
		return false
	}
	if sessionID = strings.TrimSpace(sessionID); sessionID != "" && ex.SessionID != sessionID {
		return false
	}
	if snippetContains = strings.TrimSpace(snippetContains); snippetContains != "" && !strings.Contains(ex.SnippetPreview, snippetContains) {
		return false
	}
	if contextIncluded && !ex.ContextIncluded {
		return false
	}
	if turnIdx > 0 && ex.TurnIdx != turnIdx {
		return false
	}
	for _, term := range matchedTerms {
		term = strings.TrimSpace(strings.ToLower(term))
		if term == "" {
			continue
		}
		if !containsString(ex.MatchedTerms, term) {
			return false
		}
	}
	return true
}

func sessionSearchExampleSummary(ex SessionSearchExample) string {
	parts := []string{
		fmt.Sprintf("tool#%d", ex.ToolIndex),
		"query=" + previewSubstr(ex.Query, 80),
	}
	if ex.SessionID != "" {
		parts = append(parts, "session="+ex.SessionID)
	}
	if ex.RecentSessionID != "" {
		parts = append(parts, "recent_session="+ex.RecentSessionID)
	}
	if ex.TurnIdx > 0 {
		parts = append(parts, fmt.Sprintf("turn=%d", ex.TurnIdx))
	}
	if ex.MessageIdx > 0 {
		parts = append(parts, fmt.Sprintf("message=%d", ex.MessageIdx))
	}
	if ex.ModTime != "" {
		parts = append(parts, "mod_time="+previewSubstr(ex.ModTime, 80))
	}
	if ex.ContextIncluded {
		parts = append(parts, "context=true")
	}
	if len(ex.MatchedTerms) > 0 {
		parts = append(parts, "terms="+strings.Join(ex.MatchedTerms, ","))
	}
	if ex.SnippetPreview != "" {
		parts = append(parts, "snippet="+previewSubstr(ex.SnippetPreview, 100))
	}
	if ex.RecentPlanPreview != "" {
		parts = append(parts, "recent_plan="+previewSubstr(ex.RecentPlanPreview, 100))
	}
	return strings.Join(parts, " ")
}

func loopDecisionExamples(decisions []LoopDecision, max int) []string {
	if max <= 0 || len(decisions) == 0 {
		return nil
	}
	examples := make([]string, 0, min(max, len(decisions)))
	for i, d := range decisions {
		if i >= max {
			break
		}
		examples = append(examples, formatLoopDecisionExample(d))
	}
	return examples
}

func formatLoopDecisionExample(d LoopDecision) string {
	parts := []string{
		"kind=" + d.Kind,
		"decision=" + d.Decision,
	}
	if d.Trigger != "" {
		parts = append(parts, "trigger="+d.Trigger)
	}
	return strings.Join(parts, " ")
}

func loopProtocolFeedExamples(feeds []LoopProtocolFeed, max int) []string {
	if max <= 0 || len(feeds) == 0 {
		return nil
	}
	examples := make([]string, 0, min(max, len(feeds)))
	for i, feed := range feeds {
		if i >= max {
			break
		}
		examples = append(examples, formatLoopProtocolFeedExample(feed))
	}
	return examples
}

func formatLoopProtocolFeedExample(feed LoopProtocolFeed) string {
	parts := []string{
		"mode=" + feed.Mode,
		fmt.Sprintf("feed_number=%d", feed.FeedNumber),
	}
	if feed.PlanLabel != "" {
		parts = append(parts, "plan_label="+previewSubstr(feed.PlanLabel, 80))
	}
	if feed.PlanCurrentStepStatus != "" {
		parts = append(parts, "plan_current_step_status="+feed.PlanCurrentStepStatus)
	}
	if feed.PlanCurrentStep != "" {
		parts = append(parts, "plan_current_step="+previewSubstr(feed.PlanCurrentStep, 100))
	}
	if feed.CurrentSituation != "" {
		parts = append(parts, "current_situation="+previewSubstr(feed.CurrentSituation, 120))
	}
	if feed.LastTurnID != "" || feed.LastTurnMemorySearchCalls > 0 || feed.LastTurnSessionSearchCalls > 0 {
		parts = append(parts, "last_turn="+previewSubstr(loopProtocolFeedLastTurnSummary(feed), 140))
	}
	return strings.Join(parts, " ")
}

func loopProtocolFeedLastTurnSummary(feed LoopProtocolFeed) string {
	var parts []string
	if feed.LastTurnID != "" {
		parts = append(parts, "id="+feed.LastTurnID)
	}
	if feed.LastTurnEndReason != "" {
		parts = append(parts, "reason="+feed.LastTurnEndReason)
	}
	if feed.LastTurnToolRequests > 0 {
		parts = append(parts, fmt.Sprintf("tools=%d", feed.LastTurnToolRequests))
	}
	if feed.LastTurnMemoryUpdates > 0 {
		parts = append(parts, fmt.Sprintf("memory_updates=%d", feed.LastTurnMemoryUpdates))
	}
	if feed.LastTurnMemorySearchCalls > 0 {
		parts = append(parts, fmt.Sprintf("memory_searches=%d", feed.LastTurnMemorySearchCalls))
	}
	if feed.LastTurnMemorySearchMisses > 0 {
		parts = append(parts, fmt.Sprintf("memory_misses=%d", feed.LastTurnMemorySearchMisses))
	}
	if feed.LastTurnSessionSearchCalls > 0 {
		parts = append(parts, fmt.Sprintf("session_search=%d", feed.LastTurnSessionSearchCalls))
	}
	if feed.LastTurnLoopGuards > 0 {
		parts = append(parts, fmt.Sprintf("loop_guards=%d", feed.LastTurnLoopGuards))
	}
	return strings.Join(parts, " ")
}

func loopProtocolCalibrationExamples(calibrations []LoopProtocolCalibration, max int) []string {
	if max <= 0 || len(calibrations) == 0 {
		return nil
	}
	examples := make([]string, 0, min(max, len(calibrations)))
	for i, calibration := range calibrations {
		if i >= max {
			break
		}
		examples = append(examples, formatLoopProtocolCalibrationExample(calibration))
	}
	return examples
}

func formatLoopProtocolCalibrationExample(calibration LoopProtocolCalibration) string {
	parts := []string{
		"loop_id=" + calibration.LoopID,
		"status=" + calibration.Status,
	}
	if calibration.CalibrationQuestions > 0 {
		parts = append(parts, fmt.Sprintf("questions=%d", calibration.CalibrationQuestions))
	}
	if calibration.CalibrationAnswers > 0 {
		parts = append(parts, fmt.Sprintf("answers=%d", calibration.CalibrationAnswers))
	}
	if calibration.ProtocolPath != "" {
		parts = append(parts, "path="+calibration.ProtocolPath)
	}
	if calibration.LastCalibrationQuestion != "" {
		parts = append(parts, "question="+previewSubstr(calibration.LastCalibrationQuestion, 100))
	}
	if calibration.LastCalibrationAnswer != "" {
		parts = append(parts, "answer="+previewSubstr(calibration.LastCalibrationAnswer, 100))
	}
	return strings.Join(parts, " ")
}

func checkNamePart(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "*"
	}
	return previewSubstr(s, 24)
}

func FocusedTaskCalledAtLeast(taskType string, min int) Check {
	return Check{
		Name: fmt.Sprintf("focused_task_called_at_least:%s:%d", taskType, min),
		Eval: func(t Trace) CheckResult {
			stats := t.DelegationStats()
			got := stats.FocusedTaskByType[taskType]
			if got >= min {
				return CheckResult{Pass: true, Detail: fmt.Sprintf("%s=%d", taskType, got)}
			}
			return CheckResult{
				Pass:   false,
				Detail: fmt.Sprintf("%s=%d, want >= %d; focused_tasks=%v", taskType, got, min, stats.FocusedTaskByType),
			}
		},
	}
}

func SubagentCalledAtLeast(mode string, min int) Check {
	return Check{
		Name: fmt.Sprintf("subagent_called_at_least:%s:%d", mode, min),
		Eval: func(t Trace) CheckResult {
			stats := t.DelegationStats()
			got := stats.SubagentByMode[mode]
			if got >= min {
				return CheckResult{Pass: true, Detail: fmt.Sprintf("%s=%d", mode, got)}
			}
			return CheckResult{
				Pass:   false,
				Detail: fmt.Sprintf("%s=%d, want >= %d; subagents=%v", mode, got, min, stats.SubagentByMode),
			}
		},
	}
}

func NoDelegationErrors() Check {
	return Check{
		Name: "no_delegation_errors",
		Eval: func(t Trace) CheckResult {
			stats := t.DelegationStats()
			if stats.FocusedTaskErrors == 0 && stats.SubagentErrors == 0 {
				return CheckResult{
					Pass:   true,
					Detail: fmt.Sprintf("focused_task_errors=%d subagent_errors=%d", stats.FocusedTaskErrors, stats.SubagentErrors),
				}
			}
			return CheckResult{
				Pass:   false,
				Detail: fmt.Sprintf("focused_task_errors=%d subagent_errors=%d", stats.FocusedTaskErrors, stats.SubagentErrors),
			}
		},
	}
}

func NoPlanErrors() Check {
	return Check{
		Name: "no_plan_errors",
		Eval: func(t Trace) CheckResult {
			stats := t.PlanStats()
			if stats.Errors == 0 {
				return CheckResult{Pass: true, Detail: fmt.Sprintf("plan_errors=%d", stats.Errors)}
			}
			return CheckResult{Pass: false, Detail: fmt.Sprintf("plan_errors=%d", stats.Errors)}
		},
	}
}

var toolStatsAccessors = map[string]func(ToolRuntimeStats) int64{
	"tool_requests":                 func(s ToolRuntimeStats) int64 { return int64(s.ToolRequests) },
	"tool_name_canonicalized":       func(s ToolRuntimeStats) int64 { return int64(s.ToolNameCanonicalized) },
	"tool_args_repaired":            func(s ToolRuntimeStats) int64 { return int64(s.ToolArgsRepaired) },
	"tool_repair_calls":             func(s ToolRuntimeStats) int64 { return int64(s.ToolRepairCalls) },
	"tool_repair_succeeded":         func(s ToolRuntimeStats) int64 { return int64(s.ToolRepairSucceeded) },
	"tool_repair_failed":            func(s ToolRuntimeStats) int64 { return int64(s.ToolRepairFailed) },
	"tool_repair_notes":             func(s ToolRuntimeStats) int64 { return int64(s.ToolRepairNotes) },
	"tool_errors":                   func(s ToolRuntimeStats) int64 { return int64(s.ToolErrors) },
	"tool_duration_ms":              func(s ToolRuntimeStats) int64 { return s.ToolDurationMS },
	"loop_guard_interventions":      func(s ToolRuntimeStats) int64 { return int64(s.LoopGuardInterventions) },
	"forced_no_tools":               func(s ToolRuntimeStats) int64 { return int64(s.ForcedNoTools) },
	"source_access_results":         func(s ToolRuntimeStats) int64 { return int64(s.SourceAccessResults) },
	"source_access_verified":        func(s ToolRuntimeStats) int64 { return int64(s.SourceAccessVerified) },
	"source_access_discovery_only":  func(s ToolRuntimeStats) int64 { return int64(s.SourceAccessDiscoveryOnly) },
	"source_access_network":         func(s ToolRuntimeStats) int64 { return int64(s.SourceAccessNetwork) },
	"source_access_dynamic_partial": func(s ToolRuntimeStats) int64 { return int64(s.SourceAccessDynamicPartial) },
	"memory_updates":                func(s ToolRuntimeStats) int64 { return int64(s.MemoryUpdates) },
	"memory_update_add":             func(s ToolRuntimeStats) int64 { return int64(s.MemoryUpdateAdd) },
	"memory_update_replace":         func(s ToolRuntimeStats) int64 { return int64(s.MemoryUpdateReplace) },
	"memory_update_remove":          func(s ToolRuntimeStats) int64 { return int64(s.MemoryUpdateRemove) },
	"memory_search_calls":           func(s ToolRuntimeStats) int64 { return int64(s.MemorySearchCalls) },
	"memory_search_misses":          func(s ToolRuntimeStats) int64 { return int64(s.MemorySearchMisses) },
	"session_search_calls":          func(s ToolRuntimeStats) int64 { return int64(s.SessionSearchCalls) },
	"session_search_results":        func(s ToolRuntimeStats) int64 { return int64(s.SessionSearchResults) },
	"session_search_context_hits":   func(s ToolRuntimeStats) int64 { return int64(s.SessionSearchContextHits) },
	"session_search_matched_terms":  func(s ToolRuntimeStats) int64 { return int64(s.SessionSearchMatchedTerms) },
	"session_search_recent_sessions": func(s ToolRuntimeStats) int64 {
		return int64(s.SessionSearchRecent)
	},
}

func toolStatsField(stats ToolRuntimeStats, field string) (int64, bool) {
	accessor, ok := toolStatsAccessors[field]
	if !ok {
		return 0, false
	}
	return accessor(stats), true
}

// ToolCalledBefore passes when at least one `earlier` call happens
// before the first `later` call, AND a `later` call was made.
//
// Models the load-bearing "reproduce-first" workflow: the agent
// should run the test BEFORE editing the impl. Failing the check
// either means it edited without reproducing (likely wrong) or
// it never edited at all.
func ToolCalledBefore(earlier, later string) Check {
	return ToolCalledBeforeMatching(earlier, nil, later, nil)
}

// ToolCalledBeforeMatching is the argument-aware form of ToolCalledBefore.
// It lets evals say "search for this entity before fetching this source" or
// "read this file before editing that file" instead of relying only on tool
// names. Nil matchers accept any call for that tool.
func ToolCalledBeforeMatching(earlier string, earlierMatcher func(args map[string]any) bool, later string, laterMatcher func(args map[string]any) bool) Check {
	return Check{
		Name: "tool_called_before:" + earlier + "->" + later,
		Eval: func(t Trace) CheckResult {
			firstEarlier := -1
			firstLater := -1
			for i, c := range t.Tools {
				if c.Tool == earlier && firstEarlier == -1 && (earlierMatcher == nil || earlierMatcher(c.Args)) {
					firstEarlier = i
				}
				if c.Tool == later && firstLater == -1 && (laterMatcher == nil || laterMatcher(c.Args)) {
					firstLater = i
				}
			}
			switch {
			case firstLater == -1:
				return CheckResult{Pass: false, Detail: fmt.Sprintf("never observed a matching %q call", later)}
			case firstEarlier == -1:
				return CheckResult{Pass: false, Detail: fmt.Sprintf("never observed a matching %q call before matching %q", earlier, later)}
			case firstEarlier >= firstLater:
				return CheckResult{
					Pass:   false,
					Detail: fmt.Sprintf("expected matching %q before matching %q; first %q at step %d, first %q at step %d", earlier, later, earlier, firstEarlier, later, firstLater),
				}
			default:
				return CheckResult{Pass: true}
			}
		},
	}
}

// FinalTextContains passes when the agent's final assistant message
// contains the substring. Used for "the answer should mention X"
// assertions. Case-sensitive on purpose — eval is usually checking
// for a specific quoted value or section label, where the case
// matters.
func FinalTextContains(substr string) Check {
	return Check{
		Name: "final_text_contains:" + previewSubstr(substr, 32),
		Eval: func(t Trace) CheckResult {
			if strings.Contains(t.FinalText, substr) {
				return CheckResult{Pass: true}
			}
			return CheckResult{
				Pass:   false,
				Detail: fmt.Sprintf("final text did not contain %q; got %q", substr, previewSubstr(t.FinalText, 200)),
			}
		},
	}
}

// FinalTextLacks passes when the final assistant message does NOT
// contain the substring. Used for refusal/anti-leak assertions:
// "the answer must not include the raw stack trace", "the answer
// must not say 'I cannot help'".
func FinalTextLacks(substr string) Check {
	return Check{
		Name: "final_text_lacks:" + previewSubstr(substr, 32),
		Eval: func(t Trace) CheckResult {
			if !strings.Contains(t.FinalText, substr) {
				return CheckResult{Pass: true}
			}
			return CheckResult{
				Pass:   false,
				Detail: fmt.Sprintf("final text contained forbidden %q", substr),
			}
		},
	}
}

// ShellCommandLacks passes when no shell tool call's command string
// contains the given substring. The "no | head, no || true" guard
// the user named: shell commands that mask exit codes destroy the
// signal the agent's own verification step needs. Default-on for
// any code-repair scenario.
//
// Matches both `shell` and the read-only `read_only_shell` flavor
// (any tool whose first arg is named "command"). Keeps the check
// invariant to which shell variant the scenario wires in.
func ShellCommandLacks(forbidden string) Check {
	return Check{
		Name: "shell_command_lacks:" + previewSubstr(forbidden, 32),
		Eval: func(t Trace) CheckResult {
			for _, c := range t.Tools {
				cmd, ok := c.Args["command"].(string)
				if !ok {
					continue
				}
				if strings.Contains(cmd, forbidden) {
					return CheckResult{
						Pass:   false,
						Detail: fmt.Sprintf("shell call_id=%s used forbidden %q in command %q", c.CallID, forbidden, cmd),
					}
				}
			}
			return CheckResult{Pass: true}
		},
	}
}

// TurnEndedCleanly passes when TurnEndReason is "completed". Used as
// a smoke-level prerequisite on scenarios where any other end state
// (max_turns, error, cancelled) is itself a failure even before
// looking at content.
func TurnEndedCleanly() Check {
	return Check{
		Name: "turn_ended_cleanly",
		Eval: func(t Trace) CheckResult {
			if t.TurnEndReason == "completed" {
				return CheckResult{Pass: true}
			}
			return CheckResult{
				Pass:   false,
				Detail: fmt.Sprintf("turn ended with reason %q (expected completed)", t.TurnEndReason),
			}
		},
	}
}

// MaxToolCalls passes when the run made at most n tool invocations.
// Caps wasteful behavior — a scenario that repeats the same read
// five times wasted tool budget even if the final answer is right.
// Negative n is treated as unbounded (always passes).
func MaxToolCalls(n int) Check {
	return Check{
		Name: fmt.Sprintf("max_tool_calls:%d", n),
		Eval: func(t Trace) CheckResult {
			if n < 0 || len(t.Tools) <= n {
				return CheckResult{Pass: true}
			}
			return CheckResult{
				Pass:   false,
				Detail: fmt.Sprintf("expected at most %d tool calls, observed %d (%s)", n, len(t.Tools), toolNamesSummary(t.Tools)),
			}
		},
	}
}

// MaxSuccessfulToolCalls passes when the run made at most n tool
// invocations that actually completed successfully. Guard-rejected
// attempts still matter for other checks and telemetry, but they do
// not represent successful parent-side evidence gathering. This is
// useful for subagent isolation evals: a first-tool policy rejection
// should not be counted as parent context pollution.
func MaxSuccessfulToolCalls(n int) Check {
	return Check{
		Name: fmt.Sprintf("max_successful_tool_calls:%d", n),
		Eval: func(t Trace) CheckResult {
			if n < 0 {
				return CheckResult{Pass: true}
			}
			var names []string
			for _, c := range t.Tools {
				if c.ExitCode == 0 && !c.IsErr {
					names = append(names, c.Tool)
				}
			}
			if len(names) <= n {
				return CheckResult{Pass: true}
			}
			return CheckResult{
				Pass:   false,
				Detail: fmt.Sprintf("expected at most %d successful tool calls, observed %d (%s)", n, len(names), strings.Join(names, ", ")),
			}
		},
	}
}

func MaxSuccessfulToolCallsForTool(toolName string, n int) Check {
	return Check{
		Name: fmt.Sprintf("max_successful_tool_calls:%s:%d", toolName, n),
		Eval: func(t Trace) CheckResult {
			if n < 0 {
				return CheckResult{Pass: true}
			}
			count := 0
			for _, c := range t.Tools {
				if c.Tool == toolName && c.ExitCode == 0 && !c.IsErr {
					count++
				}
			}
			if count <= n {
				return CheckResult{Pass: true}
			}
			return CheckResult{
				Pass:   false,
				Detail: fmt.Sprintf("expected at most %d successful %q calls, observed %d; tools=%s", n, toolName, count, toolNamesSummary(t.Tools)),
			}
		},
	}
}

// ShellCommandMatching passes when at least one shell tool call's
// `command` argument matches the given pattern. Pattern is a Go
// regexp; on regex compile failure it falls back to plain substring
// match so scenarios authored as substrings keep working.
//
// Used to pin "the agent must have actually run pytest" / "the agent
// must have run go test ./..." style scenario expectations.
func ShellCommandMatching(pattern string) Check {
	re, reErr := regexp.Compile(pattern)
	return Check{
		Name: "shell_command_matching:" + previewSubstr(pattern, 48),
		Eval: func(t Trace) CheckResult {
			var observed []string
			for _, c := range t.Tools {
				cmd, ok := c.Args["command"].(string)
				if !ok || cmd == "" {
					continue
				}
				observed = append(observed, cmd)
				if shellCommandMatches(pattern, re, reErr, cmd) {
					return CheckResult{Pass: true}
				}
			}
			return CheckResult{
				Pass:   false,
				Detail: fmt.Sprintf("missing required command match %q; commands=%v", pattern, observed),
			}
		},
	}
}

func ShellCommandMatchingAtLeast(pattern string, min int) Check {
	re, reErr := regexp.Compile(pattern)
	return Check{
		Name: fmt.Sprintf("shell_command_matching_at_least:%s:%d", previewSubstr(pattern, 48), min),
		Eval: func(t Trace) CheckResult {
			if min <= 0 {
				return CheckResult{Pass: true}
			}
			var observed []string
			matches := 0
			for _, c := range t.Tools {
				cmd, ok := c.Args["command"].(string)
				if !ok || cmd == "" {
					continue
				}
				observed = append(observed, cmd)
				if shellCommandMatches(pattern, re, reErr, cmd) {
					matches++
				}
			}
			if matches >= min {
				return CheckResult{Pass: true, Detail: fmt.Sprintf("matched %d command(s)", matches)}
			}
			return CheckResult{
				Pass:   false,
				Detail: fmt.Sprintf("expected at least %d command match(es) %q, observed %d; commands=%v", min, pattern, matches, observed),
			}
		},
	}
}

func ShellCommandMatchingBeforeTool(pattern, toolName string) Check {
	re, reErr := regexp.Compile(pattern)
	return Check{
		Name: fmt.Sprintf("shell_command_before_tool:%s->%s", previewSubstr(pattern, 48), toolName),
		Eval: func(t Trace) CheckResult {
			firstTool := -1
			firstMatch := -1
			var observed []string
			for i, c := range t.Tools {
				if c.Tool == toolName && firstTool == -1 {
					firstTool = i
				}
				cmd, ok := c.Args["command"].(string)
				if !ok || cmd == "" {
					continue
				}
				observed = append(observed, cmd)
				if shellCommandMatches(pattern, re, reErr, cmd) && firstMatch == -1 {
					firstMatch = i
				}
			}
			switch {
			case firstTool == -1:
				return CheckResult{Pass: false, Detail: fmt.Sprintf("never observed a %q call", toolName)}
			case firstMatch == -1:
				return CheckResult{Pass: false, Detail: fmt.Sprintf("never observed command match %q before %q; commands=%v", pattern, toolName, observed)}
			case firstMatch >= firstTool:
				return CheckResult{
					Pass:   false,
					Detail: fmt.Sprintf("expected command match %q before %q; first command at step %d, first %q at step %d", pattern, toolName, firstMatch, toolName, firstTool),
				}
			default:
				return CheckResult{Pass: true}
			}
		},
	}
}

func ShellCommandMatchingAfterTool(pattern, toolName string) Check {
	re, reErr := regexp.Compile(pattern)
	return Check{
		Name: fmt.Sprintf("shell_command_after_tool:%s->%s", previewSubstr(pattern, 48), toolName),
		Eval: func(t Trace) CheckResult {
			lastTool := -1
			lastMatch := -1
			var observed []string
			for i, c := range t.Tools {
				if c.Tool == toolName {
					lastTool = i
				}
				cmd, ok := c.Args["command"].(string)
				if !ok || cmd == "" {
					continue
				}
				observed = append(observed, cmd)
				if shellCommandMatches(pattern, re, reErr, cmd) {
					lastMatch = i
				}
			}
			switch {
			case lastTool == -1:
				return CheckResult{Pass: false, Detail: fmt.Sprintf("never observed a %q call", toolName)}
			case lastMatch == -1:
				return CheckResult{Pass: false, Detail: fmt.Sprintf("never observed command match %q after %q; commands=%v", pattern, toolName, observed)}
			case lastMatch <= lastTool:
				return CheckResult{
					Pass:   false,
					Detail: fmt.Sprintf("expected command match %q after %q; last command at step %d, last %q at step %d", pattern, toolName, lastMatch, toolName, lastTool),
				}
			default:
				return CheckResult{Pass: true}
			}
		},
	}
}

func shellCommandMatches(pattern string, re *regexp.Regexp, reErr error, cmd string) bool {
	if reErr == nil {
		return re.MatchString(cmd)
	}
	return strings.Contains(cmd, pattern)
}

// ShellCommandLacksUnguarded is ShellCommandLacks with one important
// twist: commands that the runtime's shell guard already rejected
// (exit code != 0 plus a guard-shaped error message) DO NOT count as
// failures. That distinction matters because the whole point of the
// guard is to let the model attempt the dangerous shape and still
// produce a correct outcome — penalizing the model for the attempt
// would discourage exploration of the guard's coverage.
//
// Guard rejection is detected by ExitCode != 0 plus a "masks a
// test/build exit code" or "unbounded filesystem scan" substring in
// the result — the two messages the current shell guards emit.
func ShellCommandLacksUnguarded(forbidden string) Check {
	lower := strings.ToLower(forbidden)
	return Check{
		Name: "shell_command_lacks_unguarded:" + previewSubstr(forbidden, 32),
		Eval: func(t Trace) CheckResult {
			for _, c := range t.Tools {
				cmd, ok := c.Args["command"].(string)
				if !ok || cmd == "" {
					continue
				}
				if !strings.Contains(strings.ToLower(cmd), lower) {
					continue
				}
				if guardRejected(c) {
					continue
				}
				return CheckResult{
					Pass:   false,
					Detail: fmt.Sprintf("forbidden command substring %q in %q", forbidden, cmd),
				}
			}
			return CheckResult{Pass: true}
		},
	}
}

// FileNotEdited passes when no write_file / edit_file tool call
// targeted any of the named paths. Paths match against the trailing
// segment of args.path (so "test_slug.py" catches both
// `test_slug.py` and `pkg/test_slug.py`), preserving the
// CheckBatchTrace semantics that scenarios were written against.
//
// Used to pin "the agent must not edit the test file" in repair
// scenarios where editing tests would be cheating.
func FileNotEdited(paths []string) Check {
	return Check{
		Name: "file_not_edited:" + previewSubstr(strings.Join(paths, ","), 64),
		Eval: func(t Trace) CheckResult {
			for _, c := range t.Tools {
				if c.Tool != "write_file" && c.Tool != "edit_file" {
					continue
				}
				rawPath, _ := c.Args["path"].(string)
				slash := filepath.ToSlash(rawPath)
				for _, name := range paths {
					if slash == name || strings.HasSuffix(slash, "/"+name) {
						return CheckResult{
							Pass:   false,
							Detail: fmt.Sprintf("modified protected file through %s: %v", c.Tool, rawPath),
						}
					}
				}
			}
			return CheckResult{Pass: true}
		},
	}
}

// guardRejected returns true when the shell tool's own guard refused
// to execute the command (vs the command ran and exited non-zero on
// its own merits). The signal is exit_code != 0 + a guard-shaped
// substring in the tool result; new guards that surface a different
// substring must be added here to keep ShellCommandLacksUnguarded
// honest.
var guardRejectionMarkers = []string{
	"masks a test/build exit code",
	"unbounded filesystem scan",
}

func guardRejected(c ToolCall) bool {
	if c.ExitCode == 0 {
		return false
	}
	for _, marker := range guardRejectionMarkers {
		if strings.Contains(c.Result, marker) {
			return true
		}
	}
	return false
}

// toolNamesSummary returns "name1, name2, name3" for diagnostics.
// Order is invocation order; duplicates kept on purpose so the
// summary shows repeated calls.
func toolNamesSummary(tools []ToolCall) string {
	if len(tools) == 0 {
		return "no tool calls"
	}
	names := make([]string, 0, len(tools))
	for _, c := range tools {
		names = append(names, c.Tool)
	}
	return strings.Join(names, ", ")
}

// previewSubstr returns s truncated to n runes with an ellipsis when
// truncated. Used to keep Check.Name and Detail short when the
// asserted substring is long.
func previewSubstr(s string, n int) string {
	if n <= 0 || len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
