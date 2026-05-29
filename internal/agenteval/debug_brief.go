package agenteval

import (
	"fmt"
	"sort"
	"strings"

	"github.com/affinefoundation/affent/internal/sse"
)

type DebugBrief struct {
	Tags  []string         `json:"tags,omitempty"`
	Items []DebugBriefItem `json:"items,omitempty"`
}

type DebugBriefItem struct {
	Kind     string         `json:"kind"`
	Severity string         `json:"severity,omitempty"`
	Message  string         `json:"message"`
	Inspect  []string       `json:"inspect,omitempty"`
	Counts   map[string]int `json:"counts,omitempty"`
}

func BuildDebugBrief(res BatchResult) *DebugBrief {
	var items []DebugBriefItem
	tagSet := map[string]bool{}
	add := func(kind, severity, message string, inspect []string, counts map[string]int, tags ...string) {
		items = append(items, DebugBriefItem{
			Kind:     kind,
			Severity: severity,
			Message:  message,
			Inspect:  append([]string(nil), inspect...),
			Counts:   filteredPositiveCounts(counts),
		})
		for _, tag := range tags {
			if tag != "" {
				tagSet[tag] = true
			}
		}
	}
	if !res.OK {
		add("outcome", "fail", "scenario failed; inspect failures before trusting final answer", []string{"failures"}, nil, "outcome:failed")
	}
	if count := loopProtocolFixtureFailureCount(res.Failures); count > 0 {
		add("loop_protocol_fixture", "fail", "loop protocol fixture is missing or inactive; fix the scenario LOOP.md/state before trusting model behavior", []string{"failures", "expectations", "debug_manifest"}, map[string]int{
			"failures": count,
		}, "loop_protocol", "loop_protocol:fixture")
	}
	if counts, ok := loopProtocolCalibrationBacklogCounts(res); ok {
		add("loop_protocol_calibration_backlog", "warn", "loop protocol calibration requests outpaced recorded answers; inspect setup state before spending more turn budget", []string{"loop_protocol_calibration_request_examples", "loop_protocol_calibration_examples", "trace_events", "timeline"}, counts, "loop_protocol", "loop_protocol:calibration_backlog")
	}
	if counts, ok := loopProtocolSetupOverrunCounts(res); ok {
		add("loop_protocol_setup_overrun", "fail", "loop protocol draft setup was followed by more non-skipped tool work in the same turn; ask calibration before spending task budget", []string{"tool_timeline", "loop_protocol_calibration_request_examples", "trace_events", "timeline"}, counts, "loop_protocol", "loop_protocol:setup_tool_overrun")
	}
	if count := sourceRepoSetupFailureCount(res.Failures); count > 0 {
		add("source_repo_setup", "fail", "source repository setup failed before the agent turn; fix the eval repository URL, ref, target directory, or setup command before judging model behavior", []string{"failures", "expectations", "debug_manifest", "workspace"}, map[string]int{
			"failures": count,
		}, "source_repo", "source_repo:setup")
	}
	if count := workspaceAbsolutePathFailureCount(res.Failures); count > 0 || res.WorkspacePath.Total() > 0 {
		add("workspace_path", "fail", "agent used the workspace absolute path where the runtime expected workspace-relative commands or file args; inspect tool arguments and prompt/context path guidance before comparing task quality", []string{"failures", "tool_timeline", "runtime_surface", "timeline"}, map[string]int{
			"failures":           count,
			"arg_occurrences":    res.WorkspacePath.ArgOccurrences,
			"result_occurrences": res.WorkspacePath.ResultOccurrences,
			"child_transcripts":  res.WorkspacePath.ChildTranscriptOccurrences,
		}, "workspace_path", "workspace_path:absolute")
	}
	if counts, tags, ok := loopProtocolStillRunningCounts(res); ok {
		add("loop_protocol_state", "warn", "loop protocol remained running at the latest checkpoint; inspect closure guard and close/plan state before treating the session as complete", []string{"loop_turn_checkpoint_examples", "runtime_surface", "message_rejected_examples", "timeline"}, counts, tags...)
	}
	if counts, tags, ok := durableCompletionOpenStateCounts(res); ok {
		add("durable_completion", "fail", "final answer was accepted while durable control state remained unfinished; inspect completion guards before treating the session as complete", []string{"final_text", "runtime_surface", "message_rejected_examples", "loop_turn_checkpoint_examples", "plan_calls", "timeline"}, counts, tags...)
	}
	expectedTurnEndReason := expectedTurnEndReasonFromExpectations(res.Expectations)
	if res.TurnEndReason != "" && res.TurnEndReason != expectedTurnEndReason {
		add("turn_end", "fail", fmt.Sprintf("turn ended with reason %q; expected %q", res.TurnEndReason, expectedTurnEndReason), []string{"final_text", "tool_timeline"}, map[string]int{res.TurnEndReason: 1}, "turn_end:"+res.TurnEndReason)
	}
	if strings.TrimSpace(res.Verifier.Command) != "" || res.Verifier.Ran {
		tags := []string{"verifier"}
		counts := map[string]int{
			"duration_ms":          int(res.Verifier.Duration.Milliseconds()),
			"output_bytes":         res.Verifier.OutputBytes,
			"output_omitted_bytes": res.Verifier.OutputOmittedBytes,
			"output_cap_bytes":     res.Verifier.OutputCapBytes,
		}
		severity := "info"
		message := "verifier command ran; inspect verifier status when comparing code-task regressions"
		emit := false
		if res.Verifier.Ran {
			counts["ran"] = 1
		} else {
			severity = "warn"
			message = "verifier command was configured but did not run; inspect runtime failure before trusting code-task outcome"
			tags = append(tags, "verifier:not_run")
			emit = true
		}
		if res.Verifier.OK {
			counts["ok"] = 1
		} else if res.Verifier.Ran {
			severity = "warn"
			message = "verifier command failed; inspect verifier result before trusting code-task output"
			tags = append(tags, "verifier:failed")
			emit = true
		}
		if res.Verifier.ExitCode > 0 {
			counts["exit_code"] = res.Verifier.ExitCode
		} else if res.Verifier.Ran && res.Verifier.ExitCode < 0 {
			counts["abnormal_exit"] = 1
			tags = append(tags, "verifier:abnormal")
			emit = true
		}
		if res.Verifier.OutputTruncated {
			counts["output_truncated"] = 1
			tags = append(tags, "verifier:output_truncated")
			emit = true
		}
		if emit {
			add("verifier", severity, message, []string{"verifier", "failures", "timeline"}, counts, tags...)
		}
	}
	if counts := filteredPositiveCounts(res.ToolStats.ToolFailureByKind); len(counts) > 0 {
		tags := []string{"tool_failure"}
		for kind := range counts {
			tags = append(tags, "tool_failure:"+kind)
		}
		add("tool_failure_by_kind", "warn", "structured tool failures observed", []string{"tool_timeline", "tool_failure_examples"}, counts, tags...)
	}
	if res.Repair.HasAny() {
		severity := "info"
		message := "tool calls were repaired or canonicalized; inspect examples for small-model tool drift"
		tags := []string{"tool_repair"}
		counts := map[string]int{
			"calls":     res.Repair.Calls,
			"succeeded": res.Repair.SucceededCalls,
			"failed":    res.Repair.FailedCalls,
			"notes":     res.Repair.Notes,
		}
		for kind, count := range res.Repair.ByKind {
			counts["kind:"+kind] = count
			tags = append(tags, "tool_repair:"+kind)
		}
		if res.Repair.FailedCalls > 0 {
			severity = "warn"
			message = "tool repair failed for at least one call; inspect repair examples before trusting tool recovery"
			tags = append(tags, "tool_repair:failed")
		}
		add("tool_repair", severity, message, []string{"tool_repair_examples", "tool_timeline"}, counts, tags...)
	}
	if counts := filteredPositiveCounts(res.RuntimeErrorByKind); len(counts) > 0 {
		tags := []string{"runtime_error"}
		for kind := range counts {
			tags = append(tags, "runtime_error:"+kind)
		}
		add("runtime_error_by_kind", "warn", "runtime errors observed", []string{"runtime_errors", "provider_logs"}, counts, tags...)
	}
	if len(res.ConversationRepairs) > 0 {
		counts := map[string]int{"events": len(res.ConversationRepairs)}
		tags := []string{"conversation_repair"}
		for _, repair := range res.ConversationRepairs {
			if repair.MissingToolResults > 0 {
				counts["missing_tool_results"] += repair.MissingToolResults
			}
			if repair.DuplicateToolResults > 0 {
				counts["duplicate_tool_results"] += repair.DuplicateToolResults
			}
			if repair.UnexpectedToolResults > 0 {
				counts["unexpected_tool_results"] += repair.UnexpectedToolResults
			}
			if repair.FailureKind != "" {
				counts["kind:"+repair.FailureKind]++
				tags = append(tags, "conversation_repair:"+repair.FailureKind)
			}
		}
		add("conversation_repair", "warn", "conversation log was repaired during resume; inspect repaired history before trusting recovered state", []string{"conversation_repair_examples", "conversation_dir", "trace_events"}, counts, tags...)
	}
	if res.MessageRejectedStats.Count > 0 {
		tags := []string{"completion_guard", "message_rejected"}
		counts := map[string]int{"count": res.MessageRejectedStats.Count}
		for trigger, count := range res.MessageRejectedStats.ByTrigger {
			counts["trigger:"+trigger] = count
			tags = append(tags, "message_rejected:"+trigger)
		}
		add("message_rejected", "info", "completion guard rejected a candidate assistant final answer before it became authoritative", []string{"message_rejected_examples", "loop_decision_examples", "timeline"}, counts, tags...)
	}
	if res.ToolStats.LoopGuardInterventions > 0 || res.ToolStats.ForcedNoTools > 0 {
		tags := []string{"loop_guard"}
		message := "loop guard intervened; inspect repeated tool or evidence patterns"
		if res.ToolStats.ForcedNoTools > 0 {
			tags = append(tags, "loop_guard:forced_no_tools")
			message = "loop guard forced no-tool continuation; inspect repeated failures before trusting recovery"
		}
		add("loop_guard", "warn", message, []string{"loop_guard_examples", "loop_decisions", "tool_timeline"}, map[string]int{
			"interventions":   res.ToolStats.LoopGuardInterventions,
			"forced_no_tools": res.ToolStats.ForcedNoTools,
		}, tags...)
	}
	if counts, ok := toolBudgetRunawayCounts(res); ok {
		add("tool_budget", "warn", "a turn exceeded the runtime-advertised tool-call budget; inspect checkpoints and tool timeline before trusting token efficiency", []string{"loop_turn_checkpoint_examples", "runtime_surface", "tool_timeline", "timeline"}, counts, "tool_budget", "tool_budget:turn_overrun")
	}
	if counts, ok := inputBudgetRunawayCounts(res); ok {
		add("input_budget", "warn", "a turn exceeded the runtime-advertised input-token budget; inspect checkpoints, compaction, and repeated context before trusting long-run efficiency", []string{"loop_turn_checkpoint_examples", "runtime_surface", "context_compaction_examples", "tool_timeline", "timeline"}, counts, "input_budget", "input_budget:turn_overrun")
	}
	if researchCheckpoints := loopDecisionCountByKind(res.LoopDecisionStats, "research_checkpoint"); researchCheckpoints > 0 {
		severity := "info"
		message := "loop triggered an external-calibration checkpoint; inspect decision action before changing durable direction"
		tags := []string{"research_checkpoint", "loop_decision:research_checkpoint"}
		if !researchCheckpointHasExternalEvidence(res) {
			severity = "warn"
			message = "research checkpoint triggered without source-backed external evidence or delegated research; inspect whether the turn stayed internally calibrated"
			tags = append(tags, "research_checkpoint:no_external_evidence")
		}
		add("research_checkpoint", severity, message, []string{"loop_decision_examples", "source_evidence", "child_transcripts", "timeline", "plan"}, map[string]int{
			"decisions":                     researchCheckpoints,
			"source_access_results":         res.ToolStats.SourceAccessResults,
			"source_access_verified":        res.ToolStats.SourceAccessVerified,
			"source_access_network":         res.ToolStats.SourceAccessNetwork,
			"source_access_dynamic_partial": res.ToolStats.SourceAccessDynamicPartial,
			"source_access_discovery_only":  res.ToolStats.SourceAccessDiscoveryOnly,
			"focused_task_calls":            res.Delegation.FocusedTaskCalls,
			"focused_task_research":         researchCheckpointFocusedTaskEvidenceCount(res.Delegation),
			"focused_task_source_findings":  researchCheckpointFocusedTaskSourceFindingCount(res.Delegation),
			"subagent_calls":                res.Delegation.SubagentCalls,
			"subagent_research":             res.Delegation.SubagentByMode["research"],
			"subagent_source_evidence":      res.Delegation.SubagentSourceEvidenceByMode["research"],
		}, tags...)
	}
	if res.Delegation.HasAny() {
		severity := "info"
		message := "delegated child work was used; inspect child reports before trusting merged state"
		tags := []string{"delegation"}
		counts := map[string]int{
			"focused_task_calls":      res.Delegation.FocusedTaskCalls,
			"focused_task_errors":     res.Delegation.FocusedTaskErrors,
			"focused_task_incomplete": res.Delegation.FocusedTaskIncomplete,
			"subagent_calls":          res.Delegation.SubagentCalls,
			"subagent_errors":         res.Delegation.SubagentErrors,
			"subagent_incomplete":     res.Delegation.SubagentIncomplete,
		}
		for taskType, count := range res.Delegation.FocusedTaskByType {
			counts["focused_task:"+taskType] = count
		}
		for mode, count := range res.Delegation.SubagentByMode {
			counts["subagent:"+mode] = count
		}
		if res.Delegation.FocusedTaskCalls > 0 {
			tags = append(tags, "delegation:focused_task")
		}
		if res.Delegation.SubagentCalls > 0 {
			tags = append(tags, "delegation:subagent")
		}
		if res.Delegation.FocusedTaskErrors > 0 || res.Delegation.SubagentErrors > 0 {
			severity = "warn"
			message = "delegated child work had runtime errors or incomplete reports; inspect child transcripts before continuing"
			tags = append(tags, "delegation_error")
			if res.Delegation.FocusedTaskErrors > 0 {
				tags = append(tags, "delegation_error:focused_task")
			}
			if res.Delegation.SubagentErrors > 0 {
				tags = append(tags, "delegation_error:subagent")
			}
		}
		add("delegation", severity, message, []string{"tool_timeline", "child_transcripts", "debug_manifest"}, counts, tags...)
	}
	if res.Plan.HasAny() {
		severity := "info"
		message := "plan tool was used; inspect plan actions if task recovery drifted"
		tags := []string{"plan"}
		counts := map[string]int{
			"calls":              res.Plan.Calls,
			"errors":             res.Plan.Errors,
			"total_steps":        res.Plan.TotalSteps,
			"completed_steps":    res.Plan.CompletedSteps,
			"current_step_index": res.Plan.CurrentStepIndex,
		}
		for action, count := range res.Plan.ByAction {
			counts["action:"+action] = count
			tags = append(tags, "plan:"+action)
		}
		if res.Plan.Errors > 0 {
			severity = "warn"
			message = "plan tool had runtime errors; inspect plan actions before continuing"
			tags = append(tags, "plan_error")
		}
		if res.Plan.TotalSteps > 0 && res.Plan.CompletedSteps < res.Plan.TotalSteps {
			severity = "warn"
			message = "latest plan state still has unfinished steps; inspect current step before resuming"
			tags = append(tags, "plan:unfinished")
		}
		add("plan", severity, message, []string{"tool_timeline", "plan_calls"}, counts, tags...)
	}
	if res.ContextInjections.Count > 0 {
		tags := []string{"context_injection"}
		counts := map[string]int{
			"count":            res.ContextInjections.Count,
			"bytes":            res.ContextInjections.Bytes,
			"estimated_tokens": res.ContextInjections.EstimatedTokens,
		}
		for source, count := range res.ContextInjections.BySource {
			counts["source:"+source] = count
			tags = append(tags, "context_injection:"+source)
		}
		add("context_injection", "info", "hidden system context was injected; inspect size and source before tuning long-run prompts", []string{"context_injection_examples", "timeline", "debug_manifest"}, counts, tags...)
	}
	if res.ToolStats.SourceAccessResults > 0 {
		severity := "info"
		message := "source evidence was captured; inspect evidence before relying on current facts"
		tags := []string{"source_access"}
		if res.ToolStats.SourceAccessVerified < res.ToolStats.SourceAccessResults {
			severity = "warn"
			tags = append(tags, "source_unverified")
			message = "some source evidence was unverified; inspect examples before trusting current facts"
			if res.ToolStats.SourceAccessVerified == 0 {
				tags = append(tags, "source_unverified_all")
				message = "source access returned no verified evidence; inspect examples before trusting current facts"
			}
		}
		if res.ToolStats.SourceAccessDiscoveryOnly > 0 {
			severity = "warn"
			tags = append(tags, "source_discovery_only")
			message = "some source evidence stopped at discovery results; inspect examples before trusting current facts"
			if res.ToolStats.SourceAccessDiscoveryOnly == res.ToolStats.SourceAccessResults {
				tags = append(tags, "source_discovery_only_all")
				message = "source access only found discovery results; fetch readable pages or network evidence before trusting current facts"
			}
		}
		if res.ToolStats.SourceAccessDynamicPartial > 0 {
			severity = "warn"
			tags = append(tags, "source_dynamic_partial")
			message = "dynamic source evidence was partial; inspect rendered text and network captures before trusting current facts"
			if res.ToolStats.SourceAccessNetwork == 0 {
				tags = append(tags, "source_dynamic_without_network")
				message = "dynamic source evidence lacked network-backed reads; inspect browser network captures before trusting current facts"
			} else if !hasLoopDecisionStatsMatch(res.LoopDecisionStats, "evidence_quality", "defer", "source_access_dynamic_partial") {
				tags = append(tags, "source_dynamic_without_decision")
				message = "dynamic source evidence was partial without an evidence-quality defer decision; inspect network evidence and final citations"
			}
		}
		if res.ToolStats.SourceAccessNetwork > 0 {
			tags = append(tags, "source_network")
		}
		missingResponseDiagnostics := sourceNetworkMissingResponseDiagnostics(res)
		if missingResponseDiagnostics > 0 {
			severity = "warn"
			tags = append(tags, "source_network:missing_response_diagnostics")
			message = "network source evidence lacked response diagnostics; inspect status/content_type before trusting current facts"
		}
		partialNetworkReads := sourceNetworkUnresolvedPartialReads(res)
		if partialNetworkReads > 0 {
			severity = "warn"
			tags = append(tags, "source_network:partial_read")
			message = "network source evidence has unresolved partial reads; continue from next_offset or use a narrower json_path before trusting missing fields"
		}
		add("source_access", severity, message, []string{"source_evidence"}, map[string]int{
			"results":                      res.ToolStats.SourceAccessResults,
			"verified":                     res.ToolStats.SourceAccessVerified,
			"network":                      res.ToolStats.SourceAccessNetwork,
			"dynamic_partial":              res.ToolStats.SourceAccessDynamicPartial,
			"discovery_only":               res.ToolStats.SourceAccessDiscoveryOnly,
			"missing_response_diagnostics": missingResponseDiagnostics,
			"partial_network_reads":        partialNetworkReads,
		}, tags...)
	}
	if len(res.BrowserScrollExamples) > 0 {
		boundary, stuck := 0, 0
		for _, ex := range res.BrowserScrollExamples {
			switch ex.Status {
			case "boundary":
				boundary++
			case "stuck":
				stuck++
			}
		}
		severity := "info"
		message := "browser scroll telemetry was captured; inspect it when rendered page evidence is thin"
		tags := []string{"browser_scroll"}
		if boundary > 0 || stuck > 0 {
			severity = "warn"
			message = "browser scroll did not expose new evidence; switch to browser network reads before citing hidden dynamic values"
			if boundary > 0 {
				tags = append(tags, "browser_scroll:boundary")
			}
			if stuck > 0 {
				tags = append(tags, "browser_scroll:stuck")
			}
			if res.ToolStats.SourceAccessNetwork == 0 {
				tags = append(tags, "browser_scroll:stuck_without_network")
				message = "browser scroll stalled without network-backed evidence; inspect network captures before trusting hidden dashboard values"
			}
		}
		add("browser_scroll", severity, message, []string{"browser_scroll_examples", "source_evidence", "tool_timeline"}, map[string]int{
			"scrolls":  len(res.BrowserScrollExamples),
			"boundary": boundary,
			"stuck":    stuck,
		}, tags...)
	}
	if len(res.BrowserNetworkExamples) > 0 {
		refs, noMatches := 0, 0
		for _, ex := range res.BrowserNetworkExamples {
			if ex.Status == "matches" {
				refs++
			}
			if ex.Status == "no_matches" {
				noMatches++
			}
		}
		severity := "info"
		message := "browser network searches produced refs or checks; read refs before citing values"
		tags := []string{"browser_network"}
		if noMatches > 0 {
			severity = "warn"
			tags = append(tags, "browser_network:no_matches")
			message = "browser network searches returned no matches; inspect queries and current pages before trusting hidden dashboard gaps"
		}
		if refs > 0 && !browserNetworkRefsHaveSourceEvidence(res) {
			severity = "warn"
			tags = append(tags, "browser_network:unread_refs")
			message = "browser network searches found refs without matching network SourceAccess evidence; call browser_network_read before citing values"
		} else if refs > 0 {
			tags = append(tags, "browser_network:refs")
		}
		add("browser_network", severity, message, []string{"browser_network_examples", "source_evidence", "tool_timeline"}, map[string]int{
			"searches":   len(res.BrowserNetworkExamples),
			"refs":       refs,
			"no_matches": noMatches,
		}, tags...)
	}
	if res.ToolStats.SessionSearchCalls > 0 || res.ToolStats.SessionSearchResults > 0 || res.ToolStats.SessionSearchRecent > 0 {
		kind := "recall"
		severity := "info"
		message := "session recall returned history with adjacent context or persisted task-state anchors"
		tags := []string{"recall", "recall:context"}
		if res.ToolStats.SessionSearchCalls > 0 && res.ToolStats.SessionSearchResults == 0 {
			kind = "empty_recall"
			tags = []string{"empty_recall"}
			if res.ToolStats.SessionSearchRecent > 0 {
				severity = "info"
				tags = append(tags, "empty_recall:recent_sessions")
				message = "session recall returned no direct hits but exposed recent session anchors for retry"
			} else {
				severity = "warn"
				tags = append(tags, "empty_recall:no_recent_sessions")
				message = "session recall returned no results"
			}
		} else if res.ToolStats.SessionSearchResults > 0 && res.ToolStats.SessionSearchContextHits == 0 {
			severity = "warn"
			tags = []string{"recall", "recall:no_context"}
			message = "session recall returned hits without adjacent context or persisted task-state anchors; inspect examples for stale or shallow recovery"
		} else if res.ToolStats.SessionSearchResults > 0 && res.ToolStats.SessionSearchMatchedTerms == 0 {
			severity = "warn"
			tags = []string{"recall", "recall:no_matched_terms"}
			message = "session recall returned hits without matched terms; inspect examples before trusting recovery"
		} else if res.ToolStats.SessionSearchResults > 0 && res.ToolStats.SessionSearchContextHits < res.ToolStats.SessionSearchResults {
			severity = "warn"
			tags = append(tags, "recall:weak_context")
			message = "session recall returned only partial adjacent context; inspect examples for incomplete recovery"
		} else if res.ToolStats.SessionSearchCalls > 0 && res.ToolStats.SessionSearchMatchedTerms < res.ToolStats.SessionSearchCalls {
			severity = "warn"
			tags = append(tags, "recall:weak_matched_terms")
			message = "session recall matched fewer query terms than calls; inspect examples for broad or stale recovery"
		}
		metrics := map[string]int{
			"calls":         res.ToolStats.SessionSearchCalls,
			"results":       res.ToolStats.SessionSearchResults,
			"context_hits":  res.ToolStats.SessionSearchContextHits,
			"matched_terms": res.ToolStats.SessionSearchMatchedTerms,
		}
		if res.ToolStats.SessionSearchRecent > 0 {
			metrics["recent"] = res.ToolStats.SessionSearchRecent
		}
		add(kind, severity, message, []string{"session_search_examples", "session_search_results", "tool_timeline"}, metrics, tags...)
	}
	if res.ToolStats.MemoryUpdates > 0 ||
		res.ToolStats.MemoryUpdateAdd > 0 ||
		res.ToolStats.MemoryUpdateReplace > 0 ||
		res.ToolStats.MemoryUpdateRemove > 0 {
		tags := []string{"memory_update"}
		if res.ToolStats.MemoryUpdateAdd > 0 {
			tags = append(tags, "memory_update:add")
		}
		if res.ToolStats.MemoryUpdateReplace > 0 {
			tags = append(tags, "memory_update:replace")
		}
		if res.ToolStats.MemoryUpdateRemove > 0 {
			tags = append(tags, "memory_update:remove")
		}
		add("memory_update", "info", "durable memory was updated; inspect examples before comparing long-run behavior", []string{"memory_update_examples", "memory_updates", "tool_timeline"}, map[string]int{
			"updates": res.ToolStats.MemoryUpdates,
			"add":     res.ToolStats.MemoryUpdateAdd,
			"replace": res.ToolStats.MemoryUpdateReplace,
			"remove":  res.ToolStats.MemoryUpdateRemove,
		}, tags...)
	} else if counts, ok := missingExpectedMemoryUpdateCounts(res); ok {
		add("memory_update_missing", "fail", "scenario expected a durable memory update but none was confirmed; inspect memory tool calls, args, and result metadata before trusting long-run recall", []string{"expectations", "tool_timeline", "memory_update_examples", "failures"}, counts, "memory_update", "memory_update:missing")
	} else if counts, tags, ok := absentLongRunMemoryUpdateCounts(res); ok {
		add("memory_update_absent", "warn", "long-running session produced no durable memory updates; inspect whether stable verified lessons or preferences should have been written", []string{"runtime_surface", "tool_timeline", "loop_turn_checkpoint_examples", "loop_protocol_feed_examples", "memory_search_miss_examples"}, counts, tags...)
	}
	if res.ToolStats.MemorySearchMisses > 0 || len(res.MemorySearchMissExamples) > 0 {
		topics := 0
		anchorExamples := 0
		noAnchorExamples := 0
		for _, ex := range res.MemorySearchMissExamples {
			topics += ex.TopicCount
			if ex.TopicCount > 0 || len(ex.Topics) > 0 {
				anchorExamples++
			} else {
				noAnchorExamples++
			}
		}
		misses := res.ToolStats.MemorySearchMisses
		if misses == 0 {
			misses = len(res.MemorySearchMissExamples)
		}
		calls := res.ToolStats.MemorySearchCalls
		if calls == 0 {
			calls = misses
		}
		severity := "info"
		message := "memory search returned no direct hits; inspect examples or trace before retrying"
		tags := []string{"memory_search_miss"}
		if anchorExamples > 0 {
			message = "memory search returned no direct hits but exposed topic anchors for retry"
			tags = append(tags, "recall:memory_topic_anchors")
			if noAnchorExamples > 0 {
				severity = "warn"
				message = "some memory search misses lacked topic anchors; inspect target/topic/query before retrying"
				tags = append(tags, "recall:memory_no_topic_anchors")
			}
		} else if len(res.MemorySearchMissExamples) > 0 {
			severity = "warn"
			message = "memory search returned no direct hits and no topic anchors; inspect target/topic/query or confirm memory is empty"
			tags = append(tags, "recall:memory_no_topic_anchors")
		}
		add("memory_search_miss", severity, message, []string{"memory_search_miss_examples", "tool_timeline"}, map[string]int{
			"calls":              calls,
			"misses":             misses,
			"topics":             topics,
			"anchor_examples":    anchorExamples,
			"no_anchor_examples": noAnchorExamples,
		}, tags...)
	}
	if res.ContextCompactions.Count > 0 {
		tags := []string{"context_compaction"}
		message := "context was compacted; inspect summary quality if resume degraded"
		if res.ContextCompactions.Reactive > 0 {
			tags = append(tags, "context_compaction:reactive")
		}
		if res.ContextCompactions.ReducedBytes > 0 {
			tags = append(tags, "context_compaction:bytes_reduced")
		}
		for _, reason := range sortedStringMapKeys(res.ContextCompactions.ByReason) {
			tags = append(tags, "context_compaction:"+reason)
		}
		if res.ContextCompactions.SummaryMissing > 0 {
			tags = append(tags, "context_compaction:summary_missing")
			message = "context was compacted without a persisted summary; inspect examples before continuing"
		} else if res.ContextCompactions.SummaryEmpty > 0 {
			tags = append(tags, "context_compaction:summary_empty")
			message = "context compaction summary was empty; inspect examples before continuing"
		}
		add("context_compaction", "warn", message, []string{"context_compaction_examples", "context_compactions"}, map[string]int{
			"count":            res.ContextCompactions.Count,
			"reactive":         res.ContextCompactions.Reactive,
			"removed_messages": res.ContextCompactions.RemovedMessages,
			"reduced_bytes":    res.ContextCompactions.ReducedBytes,
			"summary_bytes":    res.ContextCompactions.SummaryBytes,
			"summary_missing":  res.ContextCompactions.SummaryMissing,
			"summary_empty":    res.ContextCompactions.SummaryEmpty,
		}, tags...)
	}
	if hasDebugBriefTruncation(res) {
		message := "tool or context output was truncated; inspect examples and artifacts before judging evidence"
		tags := []string{"truncation"}
		contextTruncated := max(res.ToolStats.ToolContextTruncated, res.ToolTruncation.ContextTruncated)
		contextOmittedBytes := max(res.ToolStats.ToolContextOmittedBytes, res.ToolTruncation.ContextOmittedBytes)
		if contextTruncated > 0 || contextOmittedBytes > 0 {
			tags = append(tags, "truncation:tool_context")
			message = "tool output was trimmed before entering model context; inspect tool timeline and context omitted bytes"
		}
		resultMissingArtifacts := res.ToolTruncation.ResultMissingArtifacts
		if resultMissingArtifacts == 0 && res.ToolTruncation.ResultsTruncated > res.ToolTruncation.ResultArtifacts {
			resultMissingArtifacts = res.ToolTruncation.ResultsTruncated - res.ToolTruncation.ResultArtifacts
		}
		contextMissingArtifacts := res.ToolTruncation.ContextMissingArtifacts
		missingArtifacts := resultMissingArtifacts + contextMissingArtifacts
		if missingArtifacts > 0 {
			tags = append(tags, "truncation:missing_artifact")
			message = "tool results were truncated without matching artifacts; inspect tool timeline before trusting evidence"
		}
		add("truncation", "warn", message, []string{"tool_truncation_examples", "artifacts", "tool_timeline"}, map[string]int{
			"tool_context":              contextTruncated,
			"omitted_context":           contextOmittedBytes,
			"args":                      res.ToolTruncation.ArgsTruncated,
			"args_omitted":              res.ToolTruncation.ArgsOmittedBytes,
			"results":                   res.ToolTruncation.ResultsTruncated,
			"results_omitted":           res.ToolTruncation.ResultsOmittedBytes,
			"artifacts":                 res.ToolTruncation.ResultArtifacts,
			"result_missing_artifacts":  resultMissingArtifacts,
			"context_artifacts":         res.ToolTruncation.ContextArtifacts,
			"context_missing_artifacts": contextMissingArtifacts,
			"missing_artifacts":         missingArtifacts,
		}, tags...)
	}
	if len(items) == 0 {
		return nil
	}
	tags := make([]string, 0, len(tagSet))
	for tag := range tagSet {
		tags = append(tags, tag)
	}
	sort.Strings(tags)
	return &DebugBrief{Tags: tags, Items: items}
}

func loopProtocolFixtureFailureCount(failures []string) int {
	count := 0
	for _, failure := range failures {
		if strings.Contains(strings.ToLower(failure), "requires loop protocol feeds") {
			count++
		}
	}
	return count
}

func sourceRepoSetupFailureCount(failures []string) int {
	count := 0
	for _, failure := range failures {
		lower := strings.ToLower(strings.TrimSpace(failure))
		if strings.HasPrefix(lower, "source repo ") {
			count++
		}
	}
	return count
}

func workspaceAbsolutePathFailureCount(failures []string) int {
	count := 0
	for _, failure := range failures {
		lower := strings.ToLower(strings.TrimSpace(failure))
		if strings.Contains(lower, "workspace absolute path") {
			count++
		}
	}
	return count
}

func loopProtocolStillRunningCounts(res BatchResult) (map[string]int, []string, bool) {
	latest := res.LoopTurnCheckpoints.Latest
	if strings.ToLower(strings.TrimSpace(latest.Status)) != "running" {
		return nil, nil, false
	}
	counts := map[string]int{
		"running":          1,
		"checkpoints":      res.LoopTurnCheckpoints.Count,
		"protocol_feeds":   res.LoopProtocolFeeds.Count,
		"tool_requests":    latest.ToolRequests,
		"tool_errors":      latest.ToolErrors,
		"forced_no_tools":  latest.ForcedNoTools,
		"message_rejected": res.MessageRejectedStats.Count,
	}
	tags := []string{"loop_protocol", "loop_protocol:still_running"}
	if !runtimeSurfaceHasCompletionGuard(res.RuntimeSurface, "loop_protocol_running") {
		counts["missing_completion_guard"] = 1
		tags = append(tags, "completion_guard:missing_loop_protocol")
	}
	return counts, tags, true
}

func runtimeSurfaceHasCompletionGuard(surface *sse.RuntimeSurfacePayload, guard string) bool {
	if surface == nil {
		return false
	}
	for _, got := range surface.CompletionGuards {
		if got == guard {
			return true
		}
	}
	return false
}

func runtimeSurfaceHasTool(surface *sse.RuntimeSurfacePayload, tool string) bool {
	if surface == nil {
		return false
	}
	for _, got := range surface.Tools {
		if got.Name == tool {
			return true
		}
	}
	return false
}

func durableCompletionOpenStateCounts(res BatchResult) (map[string]int, []string, bool) {
	if strings.TrimSpace(res.FinalText) == "" {
		return nil, nil, false
	}
	loopRunning := strings.ToLower(strings.TrimSpace(res.LoopTurnCheckpoints.Latest.Status)) == "running"
	planUnfinished := res.Plan.TotalSteps > 0 && res.Plan.CompletedSteps < res.Plan.TotalSteps
	if !loopRunning && !planUnfinished {
		return nil, nil, false
	}
	counts := map[string]int{
		"final_answer":     1,
		"message_rejected": res.MessageRejectedStats.Count,
	}
	tags := []string{"durable_completion", "durable_completion:final_before_state_closed"}
	if loopRunning {
		counts["loop_running"] = 1
		counts["loop_checkpoints"] = res.LoopTurnCheckpoints.Count
		tags = append(tags, "loop_protocol", "loop_protocol:still_running")
		if !runtimeSurfaceHasCompletionGuard(res.RuntimeSurface, "loop_protocol_running") {
			counts["missing_loop_completion_guard"] = 1
			tags = append(tags, "completion_guard:missing_loop_protocol")
		}
	}
	if planUnfinished {
		counts["plan_unfinished"] = 1
		counts["plan_total_steps"] = res.Plan.TotalSteps
		counts["plan_completed_steps"] = res.Plan.CompletedSteps
		counts["plan_current_step_index"] = res.Plan.CurrentStepIndex
		tags = append(tags, "plan", "plan:unfinished")
		if !runtimeSurfaceHasCompletionGuard(res.RuntimeSurface, "active_plan_unfinished") {
			counts["missing_plan_completion_guard"] = 1
			tags = append(tags, "completion_guard:missing_active_plan")
		}
	}
	return counts, tags, true
}

func toolBudgetRunawayCounts(res BatchResult) (map[string]int, bool) {
	budget := effectiveToolCallBudget(res)
	if budget <= 0 || res.LoopTurnCheckpoints.MaxToolRequests <= budget {
		return nil, false
	}
	counts := map[string]int{
		"max_tool_requests": res.LoopTurnCheckpoints.MaxToolRequests,
		"tool_call_budget":  budget,
		"checkpoints":       res.LoopTurnCheckpoints.Count,
	}
	if res.LoopTurnCheckpoints.MaxInputTokens > 0 {
		counts["max_input_tokens"] = res.LoopTurnCheckpoints.MaxInputTokens
	}
	if res.LoopTurnCheckpoints.MaxTotalTokens > 0 {
		counts["max_total_tokens"] = res.LoopTurnCheckpoints.MaxTotalTokens
	}
	return counts, true
}

func inputBudgetRunawayCounts(res BatchResult) (map[string]int, bool) {
	if res.RuntimeSurface == nil || res.RuntimeSurface.MaxTurnInputTokens <= 0 {
		return nil, false
	}
	budget := res.RuntimeSurface.MaxTurnInputTokens
	if res.LoopTurnCheckpoints.MaxInputTokens <= budget {
		return nil, false
	}
	counts := map[string]int{
		"max_input_tokens":        res.LoopTurnCheckpoints.MaxInputTokens,
		"max_turn_input_tokens":   budget,
		"checkpoints":             res.LoopTurnCheckpoints.Count,
		"context_compactions":     res.ContextCompactions.Count,
		"forced_no_tools":         res.ToolStats.ForcedNoTools,
		"tool_context_truncated":  res.ToolStats.ToolContextTruncated,
		"tool_context_omitted_mb": res.ToolStats.ToolContextOmittedBytes / (1024 * 1024),
	}
	if res.LoopTurnCheckpoints.MaxTotalTokens > 0 {
		counts["max_total_tokens"] = res.LoopTurnCheckpoints.MaxTotalTokens
	}
	return counts, true
}

func effectiveToolCallBudget(res BatchResult) int {
	if res.RuntimeSurface == nil {
		return 0
	}
	if res.RuntimeSurface.MaxToolCalls > 0 {
		return res.RuntimeSurface.MaxToolCalls
	}
	return res.RuntimeSurface.MaxTurnSteps
}

func loopProtocolCalibrationBacklogCounts(res BatchResult) (map[string]int, bool) {
	questionEvents := res.LoopProtocolCalibrationRequests.Count
	answerEvents := res.LoopProtocolCalibrations.Count
	questions := max(res.LoopProtocolCalibrationRequests.Latest.CalibrationQuestions, res.LoopProtocolCalibrations.Latest.CalibrationQuestions)
	answers := max(res.LoopProtocolCalibrationRequests.Latest.CalibrationAnswers, res.LoopProtocolCalibrations.Latest.CalibrationAnswers)
	if questions == 0 {
		questions = questionEvents
	}
	if answers == 0 {
		answers = answerEvents
	}
	backlog := questions - answers
	if backlog <= 1 {
		return nil, false
	}
	return map[string]int{
		"backlog":         backlog,
		"questions":       questions,
		"answers":         answers,
		"request_events":  questionEvents,
		"answer_events":   answerEvents,
		"pending_allowed": 1,
	}, true
}

func loopProtocolSetupOverrunCounts(res BatchResult) (map[string]int, bool) {
	stats := res.LoopProtocolSetupOverrun
	if stats.NonSkippedToolCalls <= 0 {
		return nil, false
	}
	return map[string]int{
		"initializations":       stats.Initializations,
		"post_setup_tools":      stats.PostSetupToolCalls,
		"non_skipped_tools":     stats.NonSkippedToolCalls,
		"runtime_skipped_tools": stats.SkippedToolCalls,
	}, true
}

func missingExpectedMemoryUpdateCounts(res BatchResult) (map[string]int, bool) {
	if res.ToolStats.MemoryUpdates > 0 || res.Expectations == nil {
		return nil, false
	}
	required := max(
		res.Expectations.RequiredToolStatsAtLeast["memory_updates"],
		res.Expectations.RequiredToolStatsAtLeast["memory_update_add"],
		res.Expectations.RequiredToolStatsAtLeast["memory_update_replace"],
		res.Expectations.RequiredToolStatsAtLeast["memory_update_remove"],
	)
	if required <= 0 {
		return nil, false
	}
	return map[string]int{
		"required": required,
		"observed": res.ToolStats.MemoryUpdates,
	}, true
}

func absentLongRunMemoryUpdateCounts(res BatchResult) (map[string]int, []string, bool) {
	if res.ToolStats.MemoryUpdates > 0 {
		return nil, nil, false
	}
	toolRequests := res.ToolStats.ToolRequests
	if toolRequests == 0 {
		toolRequests = res.ToolCalls
	}
	loopSignals := res.LoopTurnCheckpoints.Count + res.LoopProtocolFeeds.Count
	totalTokens := res.Usage.InputTokens + res.Usage.OutputTokens
	longRun := loopSignals > 0 && (toolRequests >= 10 || totalTokens >= 100000)
	if !longRun {
		return nil, nil, false
	}
	counts := map[string]int{
		"tool_requests":         toolRequests,
		"loop_turn_checkpoints": res.LoopTurnCheckpoints.Count,
		"loop_protocol_feeds":   res.LoopProtocolFeeds.Count,
		"memory_search_calls":   res.ToolStats.MemorySearchCalls,
	}
	tags := []string{"memory_update", "memory_update:absent_longrun"}
	if res.RuntimeSurface != nil && (res.RuntimeSurface.Capabilities.Memory || runtimeSurfaceHasTool(res.RuntimeSurface, "memory")) {
		counts["memory_available"] = 1
		if res.ToolStats.MemorySearchCalls == 0 {
			tags = append(tags, "memory_update:available_unused")
		}
	}
	if totalTokens := res.Usage.InputTokens + res.Usage.OutputTokens; totalTokens > 0 {
		counts["total_tokens"] = totalTokens
	}
	return counts, tags, true
}

func researchCheckpointHasExternalEvidence(res BatchResult) bool {
	if res.ToolStats.SourceAccessVerified > 0 || res.ToolStats.SourceAccessNetwork > 0 {
		return true
	}
	if researchCheckpointFocusedTaskSourceFindingCount(res.Delegation) > 0 {
		return true
	}
	if res.Delegation.SubagentSourceEvidenceByMode["research"] > 0 {
		return true
	}
	return false
}

func researchCheckpointFocusedTaskEvidenceCount(stats DelegationStats) int {
	return stats.FocusedTaskByType["research"] + stats.FocusedTaskByType["web_extract"]
}

func researchCheckpointFocusedTaskSourceFindingCount(stats DelegationStats) int {
	return stats.FocusedTaskSourceFindingsByType["research"] + stats.FocusedTaskSourceFindingsByType["web_extract"]
}

func browserNetworkRefsHaveSourceEvidence(res BatchResult) bool {
	wantedRefs := map[string]bool{}
	for _, ex := range res.BrowserNetworkExamples {
		if ex.Status != "matches" {
			continue
		}
		for _, ref := range ex.Refs {
			if ref = strings.TrimSpace(ref); ref != "" {
				wantedRefs[ref] = true
			}
		}
	}
	sawNetworkSource := false
	readRefs := map[string]bool{}
	for _, ex := range res.SourceAccessExamples {
		isNetwork := ex.Status == "network" || ex.URLField == "browser_network_url" || ex.SourceMethod == "network_xhr_fetch"
		if !isNetwork {
			continue
		}
		sawNetworkSource = true
		if ref := strings.TrimSpace(ex.Ref); ref != "" {
			readRefs[ref] = true
		}
	}
	if len(wantedRefs) > 0 && len(readRefs) > 0 {
		for ref := range wantedRefs {
			if readRefs[ref] {
				return true
			}
		}
		return false
	}
	if sawNetworkSource {
		return true
	}
	return res.ToolStats.SourceAccessNetwork > 0
}

func sourceNetworkMissingResponseDiagnostics(res BatchResult) int {
	missing := 0
	for _, ex := range res.SourceAccessExamples {
		isNetwork := ex.Status == "network" || ex.URLField == "browser_network_url" || ex.SourceMethod == "network_xhr_fetch"
		if !isNetwork {
			continue
		}
		if strings.TrimSpace(ex.HTTPStatus) == "" || strings.TrimSpace(ex.ContentType) == "" {
			missing++
		}
	}
	return missing
}

func sourceNetworkUnresolvedPartialReads(res BatchResult) int {
	partial := 0
	for i, ex := range res.SourceAccessExamples {
		isNetwork := ex.Status == "network" || ex.URLField == "browser_network_url" || ex.SourceMethod == "network_xhr_fetch"
		if isNetwork && ex.HasMore && !sourceNetworkPartialReadResolved(ex, res.SourceAccessExamples[i+1:]) {
			partial++
		}
	}
	return partial
}

func sourceNetworkPartialReadResolved(partial SourceAccessExample, later []SourceAccessExample) bool {
	for _, ex := range later {
		isNetwork := ex.Status == "network" || ex.URLField == "browser_network_url" || ex.SourceMethod == "network_xhr_fetch"
		if !isNetwork || !sameSourceNetworkResponse(partial, ex) {
			continue
		}
		if ex.JSONPath != "" && !ex.HasMore {
			return true
		}
		if partial.NextOffset > 0 && ex.BodyOffset >= partial.NextOffset && !ex.HasMore {
			return true
		}
		if partial.NextOffset == 0 && !ex.HasMore {
			return true
		}
	}
	return false
}

func sameSourceNetworkResponse(a, b SourceAccessExample) bool {
	aRef := strings.TrimSpace(a.Ref)
	bRef := strings.TrimSpace(b.Ref)
	if aRef != "" && bRef != "" {
		return aRef == bRef
	}
	aURL := strings.TrimSpace(a.URL)
	bURL := strings.TrimSpace(b.URL)
	if aURL != "" && bURL != "" {
		return aURL == bURL
	}
	aRequestedURL := strings.TrimSpace(a.RequestedURL)
	bRequestedURL := strings.TrimSpace(b.RequestedURL)
	if aRequestedURL != "" && bRequestedURL != "" {
		return aRequestedURL == bRequestedURL
	}
	return false
}

func hasDebugBriefTruncation(res BatchResult) bool {
	return res.ToolStats.ToolContextTruncated > 0 ||
		res.ToolStats.ToolContextOmittedBytes > 0 ||
		res.ToolTruncation.ArgsTruncated > 0 ||
		res.ToolTruncation.ArgsOmittedBytes > 0 ||
		res.ToolTruncation.ResultsTruncated > 0 ||
		res.ToolTruncation.ResultsOmittedBytes > 0 ||
		res.ToolTruncation.ResultArtifacts > 0 ||
		res.ToolTruncation.ResultMissingArtifacts > 0 ||
		res.ToolTruncation.ContextTruncated > 0 ||
		res.ToolTruncation.ContextOmittedBytes > 0 ||
		res.ToolTruncation.ContextArtifacts > 0 ||
		res.ToolTruncation.ContextMissingArtifacts > 0
}

func hasLoopDecisionStatsMatch(stats LoopDecisionStats, kind, decision, trigger string) bool {
	if key := loopDecisionMatchKey(kind, decision, trigger); key != "" && stats.ByMatch[key] > 0 {
		return true
	}
	for _, d := range stats.Examples {
		if d.Kind == kind && d.Decision == decision && d.Trigger == trigger {
			return true
		}
	}
	return false
}

func loopDecisionCountByKind(stats LoopDecisionStats, kind string) int {
	if stats.ByKind != nil {
		return stats.ByKind[kind]
	}
	count := 0
	for _, example := range stats.Examples {
		if example.Kind == kind {
			count++
		}
	}
	return count
}

func filteredPositiveCounts(counts map[string]int) map[string]int {
	if len(counts) == 0 {
		return nil
	}
	out := make(map[string]int, len(counts))
	for key, count := range counts {
		if key != "" && count > 0 {
			out[key] = count
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
