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
		tag := "recall"
		message := "session recall was used"
		if res.ToolStats.SessionSearchCalls > 0 && res.ToolStats.SessionSearchResults == 0 {
			kind = "empty_recall"
			severity = "warn"
			tag = "empty_recall"
			message = "session recall returned no results"
		}
		add(kind, severity, message, []string{"session_search_results"}, map[string]int{
			"calls":         res.ToolStats.SessionSearchCalls,
			"results":       res.ToolStats.SessionSearchResults,
			"context_hits":  res.ToolStats.SessionSearchContextHits,
			"matched_terms": res.ToolStats.SessionSearchMatchedTerms,
		}, tag)
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
		add("truncation", "warn", "tool or context output was truncated; inspect artifacts before judging evidence", []string{"artifacts", "tool_timeline"}, map[string]int{
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
