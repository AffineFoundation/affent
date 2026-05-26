package agenteval

import (
	"fmt"
	"sort"
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
	if res.TurnEndReason != "" && res.TurnEndReason != "completed" {
		add("turn_end", "fail", fmt.Sprintf("turn ended with reason %q", res.TurnEndReason), []string{"final_text", "tool_timeline"}, map[string]int{res.TurnEndReason: 1}, "turn_end:"+res.TurnEndReason)
	}
	if counts := filteredPositiveCounts(res.ToolStats.ToolFailureByKind); len(counts) > 0 {
		tags := []string{"tool_failure"}
		for kind := range counts {
			tags = append(tags, "tool_failure:"+kind)
		}
		add("tool_failure_by_kind", "warn", "structured tool failures observed", []string{"tool_timeline", "tool_failure_examples"}, counts, tags...)
	}
	if counts := filteredPositiveCounts(res.RuntimeErrorByKind); len(counts) > 0 {
		tags := []string{"runtime_error"}
		for kind := range counts {
			tags = append(tags, "runtime_error:"+kind)
		}
		add("runtime_error_by_kind", "warn", "runtime errors observed", []string{"runtime_errors", "provider_logs"}, counts, tags...)
	}
	if res.ToolStats.LoopGuardInterventions > 0 {
		add("loop_guard", "warn", "loop guard intervened; inspect repeated tool or evidence patterns", []string{"loop_decisions", "tool_timeline"}, map[string]int{
			"interventions":   res.ToolStats.LoopGuardInterventions,
			"forced_no_tools": res.ToolStats.ForcedNoTools,
		}, "loop_guard")
	}
	if res.Delegation.HasAny() {
		severity := "info"
		message := "delegated child work was used; inspect child reports before trusting merged state"
		tags := []string{"delegation"}
		counts := map[string]int{
			"focused_task_calls":  res.Delegation.FocusedTaskCalls,
			"focused_task_errors": res.Delegation.FocusedTaskErrors,
			"subagent_calls":      res.Delegation.SubagentCalls,
			"subagent_errors":     res.Delegation.SubagentErrors,
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
			message = "delegated child work had runtime errors; inspect child transcripts before continuing"
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
			"calls":  res.Plan.Calls,
			"errors": res.Plan.Errors,
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
		add("plan", severity, message, []string{"tool_timeline", "plan_calls"}, counts, tags...)
	}
	if res.ToolStats.SourceAccessResults > 0 {
		severity := "info"
		tags := []string{"source_access"}
		if res.ToolStats.SourceAccessVerified < res.ToolStats.SourceAccessResults {
			severity = "warn"
			tags = append(tags, "source_unverified")
		}
		if res.ToolStats.SourceAccessDynamicPartial > 0 {
			tags = append(tags, "source_dynamic_partial")
		}
		if res.ToolStats.SourceAccessDiscoveryOnly > 0 {
			tags = append(tags, "source_discovery_only")
		}
		if res.ToolStats.SourceAccessNetwork > 0 {
			tags = append(tags, "source_network")
		}
		add("source_access", severity, "source evidence quality needs review before relying on final answer", []string{"source_evidence"}, map[string]int{
			"results":         res.ToolStats.SourceAccessResults,
			"verified":        res.ToolStats.SourceAccessVerified,
			"network":         res.ToolStats.SourceAccessNetwork,
			"dynamic_partial": res.ToolStats.SourceAccessDynamicPartial,
			"discovery_only":  res.ToolStats.SourceAccessDiscoveryOnly,
		}, tags...)
	}
	if res.ToolStats.SessionSearchCalls > 0 || res.ToolStats.SessionSearchResults > 0 {
		kind := "recall"
		severity := "info"
		message := "session recall returned history with adjacent context"
		tags := []string{"recall", "recall:context"}
		if res.ToolStats.SessionSearchCalls > 0 && res.ToolStats.SessionSearchResults == 0 {
			kind = "empty_recall"
			severity = "warn"
			tags = []string{"empty_recall"}
			message = "session recall returned no results"
		} else if res.ToolStats.SessionSearchResults > 0 && res.ToolStats.SessionSearchContextHits == 0 {
			severity = "warn"
			tags = []string{"recall", "recall:no_context"}
			message = "session recall returned hits without adjacent context; inspect examples for stale or shallow recovery"
		} else if res.ToolStats.SessionSearchResults > 0 && res.ToolStats.SessionSearchMatchedTerms == 0 {
			severity = "warn"
			tags = []string{"recall", "recall:no_matched_terms"}
			message = "session recall returned hits without matched terms; inspect examples before trusting recovery"
		}
		add(kind, severity, message, []string{"session_search_examples", "session_search_results", "tool_timeline"}, map[string]int{
			"calls":         res.ToolStats.SessionSearchCalls,
			"results":       res.ToolStats.SessionSearchResults,
			"context_hits":  res.ToolStats.SessionSearchContextHits,
			"matched_terms": res.ToolStats.SessionSearchMatchedTerms,
		}, tags...)
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
	}
	if res.ContextCompactions.Count > 0 {
		tags := []string{"context_compaction"}
		if res.ContextCompactions.Reactive > 0 {
			tags = append(tags, "context_compaction:reactive")
		}
		add("context_compaction", "warn", "context was compacted; inspect summary quality if resume degraded", []string{"context_compactions"}, map[string]int{
			"count":            res.ContextCompactions.Count,
			"reactive":         res.ContextCompactions.Reactive,
			"removed_messages": res.ContextCompactions.RemovedMessages,
			"summary_bytes":    res.ContextCompactions.SummaryBytes,
		}, tags...)
	}
	if hasDebugBriefTruncation(res) {
		add("truncation", "warn", "tool or context output was truncated; inspect examples and artifacts before judging evidence", []string{"tool_truncation_examples", "artifacts", "tool_timeline"}, map[string]int{
			"tool_context":    res.ToolStats.ToolContextTruncated,
			"omitted_context": res.ToolStats.ToolContextOmittedBytes,
			"args":            res.ToolTruncation.ArgsTruncated,
			"args_omitted":    res.ToolTruncation.ArgsOmittedBytes,
			"results":         res.ToolTruncation.ResultsTruncated,
			"results_omitted": res.ToolTruncation.ResultsOmittedBytes,
			"artifacts":       res.ToolTruncation.ResultArtifacts,
		}, "truncation")
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

func hasDebugBriefTruncation(res BatchResult) bool {
	return res.ToolStats.ToolContextTruncated > 0 ||
		res.ToolStats.ToolContextOmittedBytes > 0 ||
		res.ToolTruncation.ArgsTruncated > 0 ||
		res.ToolTruncation.ArgsOmittedBytes > 0 ||
		res.ToolTruncation.ResultsTruncated > 0 ||
		res.ToolTruncation.ResultsOmittedBytes > 0 ||
		res.ToolTruncation.ResultArtifacts > 0
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
