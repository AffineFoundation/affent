package agenteval

import "testing"

func TestBuildDebugBriefIncludesDelegationAndPlanSignals(t *testing.T) {
	brief := BuildDebugBrief(BatchResult{
		OK: true,
		Delegation: DelegationStats{
			FocusedTaskCalls:  2,
			FocusedTaskByType: map[string]int{"explore": 1, "verify": 1},
			FocusedTaskErrors: 1,
			SubagentCalls:     1,
			SubagentByMode:    map[string]int{"review": 1},
		},
		Plan: PlanStats{
			Calls:    2,
			ByAction: map[string]int{"set": 1, "update": 1},
			Errors:   1,
		},
	})
	if brief == nil {
		t.Fatal("BuildDebugBrief returned nil")
	}
	for _, tag := range []string{
		"delegation",
		"delegation:focused_task",
		"delegation:subagent",
		"delegation_error",
		"delegation_error:focused_task",
		"plan",
		"plan:set",
		"plan:update",
		"plan_error",
	} {
		if !stringSliceContains(brief.Tags, tag) {
			t.Fatalf("debug brief tags = %#v, want %q", brief.Tags, tag)
		}
	}

	delegation := debugBriefItemByKind(brief, "delegation")
	if delegation == nil || delegation.Severity != "warn" ||
		delegation.Counts["focused_task_calls"] != 2 ||
		delegation.Counts["focused_task_errors"] != 1 ||
		delegation.Counts["subagent_calls"] != 1 ||
		delegation.Counts["focused_task:explore"] != 1 ||
		delegation.Counts["subagent:review"] != 1 {
		t.Fatalf("delegation debug item = %+v", delegation)
	}

	plan := debugBriefItemByKind(brief, "plan")
	if plan == nil || plan.Severity != "warn" ||
		plan.Counts["calls"] != 2 ||
		plan.Counts["errors"] != 1 ||
		plan.Counts["action:set"] != 1 ||
		plan.Counts["action:update"] != 1 {
		t.Fatalf("plan debug item = %+v", plan)
	}
}

func TestBuildDebugBriefClassifiesSessionRecallQuality(t *testing.T) {
	brief := BuildDebugBrief(BatchResult{
		OK: true,
		ToolStats: ToolRuntimeStats{
			SessionSearchCalls:        1,
			SessionSearchResults:      2,
			SessionSearchContextHits:  1,
			SessionSearchMatchedTerms: 3,
		},
	})
	recall := debugBriefItemByKind(brief, "recall")
	if recall == nil ||
		recall.Severity != "info" ||
		recall.Message != "session recall returned history with adjacent context" ||
		recall.Counts["context_hits"] != 1 ||
		!stringSliceContains(recall.Inspect, "session_search_examples") ||
		!stringSliceContains(brief.Tags, "recall:context") {
		t.Fatalf("context recall debug item = %+v tags=%+v", recall, brief.Tags)
	}

	brief = BuildDebugBrief(BatchResult{
		OK: true,
		ToolStats: ToolRuntimeStats{
			SessionSearchCalls:        1,
			SessionSearchResults:      2,
			SessionSearchContextHits:  0,
			SessionSearchMatchedTerms: 3,
		},
	})
	recall = debugBriefItemByKind(brief, "recall")
	if recall == nil ||
		recall.Severity != "warn" ||
		recall.Message != "session recall returned hits without adjacent context; inspect examples for stale or shallow recovery" ||
		!stringSliceContains(brief.Tags, "recall:no_context") {
		t.Fatalf("shallow recall debug item = %+v tags=%+v", recall, brief.Tags)
	}

	brief = BuildDebugBrief(BatchResult{
		OK: true,
		ToolStats: ToolRuntimeStats{
			SessionSearchCalls:   1,
			SessionSearchResults: 0,
		},
	})
	empty := debugBriefItemByKind(brief, "empty_recall")
	if empty == nil ||
		empty.Severity != "warn" ||
		empty.Message != "session recall returned no results" ||
		!stringSliceContains(empty.Inspect, "session_search_examples") ||
		!stringSliceContains(brief.Tags, "empty_recall") {
		t.Fatalf("empty recall debug item = %+v tags=%+v", empty, brief.Tags)
	}

	brief = BuildDebugBrief(BatchResult{
		OK: true,
		ToolStats: ToolRuntimeStats{
			SessionSearchCalls:        2,
			SessionSearchResults:      2,
			SessionSearchContextHits:  2,
			SessionSearchMatchedTerms: 1,
		},
	})
	recall = debugBriefItemByKind(brief, "recall")
	if recall == nil ||
		recall.Severity != "warn" ||
		recall.Message != "session recall matched fewer query terms than calls; inspect examples for broad or stale recovery" ||
		recall.Counts["matched_terms"] != 1 ||
		!stringSliceContains(brief.Tags, "recall:weak_matched_terms") {
		t.Fatalf("weak recall debug item = %+v tags=%+v", recall, brief.Tags)
	}
}

func TestBuildDebugBriefClassifiesSourceAccessQuality(t *testing.T) {
	brief := BuildDebugBrief(BatchResult{
		OK: true,
		ToolStats: ToolRuntimeStats{
			SourceAccessResults:        2,
			SourceAccessVerified:       0,
			SourceAccessDynamicPartial: 1,
		},
	})
	item := debugBriefItemByKind(brief, "source_access")
	if item == nil ||
		item.Severity != "warn" ||
		item.Message != "dynamic source evidence lacked network-backed reads; inspect browser network captures before trusting current facts" ||
		item.Counts["results"] != 2 ||
		item.Counts["dynamic_partial"] != 1 ||
		!stringSliceContains(item.Inspect, "source_evidence") ||
		!stringSliceContains(brief.Tags, "source_unverified_all") ||
		!stringSliceContains(brief.Tags, "source_dynamic_without_network") {
		t.Fatalf("dynamic source access item = %+v tags=%+v", item, brief.Tags)
	}

	brief = BuildDebugBrief(BatchResult{
		OK: true,
		ToolStats: ToolRuntimeStats{
			SourceAccessResults:       2,
			SourceAccessDiscoveryOnly: 2,
		},
	})
	item = debugBriefItemByKind(brief, "source_access")
	if item == nil ||
		item.Severity != "warn" ||
		item.Message != "source access only found discovery results; fetch readable pages or network evidence before trusting current facts" ||
		item.Counts["discovery_only"] != 2 ||
		!stringSliceContains(brief.Tags, "source_discovery_only_all") {
		t.Fatalf("discovery-only source access item = %+v tags=%+v", item, brief.Tags)
	}

	brief = BuildDebugBrief(BatchResult{
		OK: true,
		ToolStats: ToolRuntimeStats{
			SourceAccessResults:        1,
			SourceAccessVerified:       1,
			SourceAccessNetwork:        1,
			SourceAccessDynamicPartial: 1,
		},
	})
	item = debugBriefItemByKind(brief, "source_access")
	if item == nil ||
		item.Severity != "warn" ||
		item.Message != "dynamic source evidence was partial without an evidence-quality defer decision; inspect network evidence and final citations" ||
		!stringSliceContains(brief.Tags, "source_network") ||
		!stringSliceContains(brief.Tags, "source_dynamic_without_decision") {
		t.Fatalf("dynamic source access without decision item = %+v tags=%+v", item, brief.Tags)
	}

	brief = BuildDebugBrief(BatchResult{
		OK: true,
		ToolStats: ToolRuntimeStats{
			SourceAccessResults:        1,
			SourceAccessVerified:       1,
			SourceAccessNetwork:        1,
			SourceAccessDynamicPartial: 1,
		},
		LoopDecisionStats: LoopDecisionStats{
			Examples: []LoopDecision{{
				Kind:     "evidence_quality",
				Decision: "defer",
				Trigger:  "source_access_dynamic_partial",
			}},
		},
	})
	item = debugBriefItemByKind(brief, "source_access")
	if item == nil ||
		item.Severity != "warn" ||
		item.Message != "dynamic source evidence was partial; inspect rendered text and network captures before trusting current facts" ||
		!stringSliceContains(brief.Tags, "source_network") ||
		stringSliceContains(brief.Tags, "source_dynamic_without_decision") {
		t.Fatalf("dynamic source access with decision item = %+v tags=%+v", item, brief.Tags)
	}

	brief = BuildDebugBrief(BatchResult{
		OK: true,
		ToolStats: ToolRuntimeStats{
			SourceAccessResults:  1,
			SourceAccessVerified: 1,
			SourceAccessNetwork:  1,
		},
	})
	item = debugBriefItemByKind(brief, "source_access")
	if item == nil ||
		item.Severity != "info" ||
		item.Message != "source evidence was captured; inspect evidence before relying on current facts" ||
		!stringSliceContains(brief.Tags, "source_network") {
		t.Fatalf("verified source access item = %+v tags=%+v", item, brief.Tags)
	}
}

func TestBuildDebugBriefClassifiesContextCompactionSummaryQuality(t *testing.T) {
	brief := BuildDebugBrief(BatchResult{
		OK: true,
		ContextCompactions: ContextCompactionStats{
			Count:           1,
			Reactive:        1,
			RemovedMessages: 42,
			SummaryMissing:  1,
		},
	})
	item := debugBriefItemByKind(brief, "context_compaction")
	if item == nil ||
		item.Severity != "warn" ||
		item.Message != "context was compacted without a persisted summary; inspect examples before continuing" ||
		item.Counts["summary_missing"] != 1 ||
		!stringSliceContains(item.Inspect, "context_compaction_examples") ||
		!stringSliceContains(brief.Tags, "context_compaction:summary_missing") {
		t.Fatalf("missing-summary compaction item = %+v tags=%+v", item, brief.Tags)
	}

	brief = BuildDebugBrief(BatchResult{
		OK: true,
		ContextCompactions: ContextCompactionStats{
			Count:        1,
			SummaryEmpty: 1,
		},
	})
	item = debugBriefItemByKind(brief, "context_compaction")
	if item == nil ||
		item.Message != "context compaction summary was empty; inspect examples before continuing" ||
		item.Counts["summary_empty"] != 1 ||
		!stringSliceContains(brief.Tags, "context_compaction:summary_empty") {
		t.Fatalf("empty-summary compaction item = %+v tags=%+v", item, brief.Tags)
	}
}

func debugBriefItemByKind(brief *DebugBrief, kind string) *DebugBriefItem {
	if brief == nil {
		return nil
	}
	for i := range brief.Items {
		if brief.Items[i].Kind == kind {
			return &brief.Items[i]
		}
	}
	return nil
}
