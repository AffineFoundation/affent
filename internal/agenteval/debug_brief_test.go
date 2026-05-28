package agenteval

import (
	"testing"
	"time"

	"github.com/affinefoundation/affent/internal/sse"
)

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

func TestBuildDebugBriefClassifiesLoopProtocolFixtureFailures(t *testing.T) {
	brief := BuildDebugBrief(BatchResult{
		OK: false,
		Failures: []string{
			`scenario "loop-draft" requires loop protocol feeds but active protocol file .affent/loops/loop-draft/LOOP.md has status "draft", want running`,
		},
	})
	item := debugBriefItemByKind(brief, "loop_protocol_fixture")
	if item == nil ||
		item.Severity != "fail" ||
		item.Counts["failures"] != 1 ||
		!stringSliceContains(item.Inspect, "expectations") ||
		!stringSliceContains(brief.Tags, "loop_protocol") ||
		!stringSliceContains(brief.Tags, "loop_protocol:fixture") {
		t.Fatalf("loop protocol fixture debug brief item=%+v tags=%+v", item, brief.Tags)
	}
}

func TestBuildDebugBriefTagsBrowserLaunchFailure(t *testing.T) {
	brief := BuildDebugBrief(BatchResult{
		OK:                 false,
		RuntimeErrorByKind: map[string]int{"browser_launch_failed": 1},
		RuntimeErrorExamples: map[string][]RuntimeErrorExample{
			"browser_launch_failed": {
				{Kind: "browser_launch_failed", Message: "launch chromium: missing_shared_library=libglib-2.0.so.0"},
			},
		},
	})
	item := debugBriefItemByKind(brief, "runtime_error_by_kind")
	if item == nil ||
		item.Severity != "warn" ||
		item.Counts["browser_launch_failed"] != 1 ||
		!stringSliceContains(brief.Tags, "runtime_error") ||
		!stringSliceContains(brief.Tags, "runtime_error:browser_launch_failed") {
		t.Fatalf("browser launch failure debug item = %+v tags=%+v", item, brief.Tags)
	}
}

func TestBuildDebugBriefClassifiesVerifierFailures(t *testing.T) {
	brief := BuildDebugBrief(BatchResult{
		OK: false,
		Verifier: VerifierResult{
			Command:            "go test ./...",
			Ran:                true,
			OK:                 false,
			ExitCode:           1,
			Duration:           2500 * time.Millisecond,
			OutputBytes:        4096,
			OutputTruncated:    true,
			OutputOmittedBytes: 2048,
			OutputCapBytes:     2048,
		},
	})
	item := debugBriefItemByKind(brief, "verifier")
	if item == nil ||
		item.Severity != "warn" ||
		item.Message != "verifier command failed; inspect verifier result before trusting code-task output" ||
		item.Counts["ran"] != 1 ||
		item.Counts["exit_code"] != 1 ||
		item.Counts["duration_ms"] != 2500 ||
		item.Counts["output_bytes"] != 4096 ||
		item.Counts["output_truncated"] != 1 ||
		item.Counts["output_omitted_bytes"] != 2048 ||
		item.Counts["output_cap_bytes"] != 2048 ||
		!stringSliceContains(item.Inspect, "verifier") ||
		!stringSliceContains(item.Inspect, "failures") ||
		!stringSliceContains(brief.Tags, "verifier") ||
		!stringSliceContains(brief.Tags, "verifier:failed") ||
		!stringSliceContains(brief.Tags, "verifier:output_truncated") {
		t.Fatalf("verifier failure item = %+v tags=%+v", item, brief.Tags)
	}

	brief = BuildDebugBrief(BatchResult{
		OK: false,
		Verifier: VerifierResult{
			Command:  "go test ./...",
			Ran:      true,
			OK:       false,
			ExitCode: -1,
		},
	})
	item = debugBriefItemByKind(brief, "verifier")
	if item == nil ||
		item.Counts["abnormal_exit"] != 1 ||
		!stringSliceContains(brief.Tags, "verifier:abnormal") ||
		!stringSliceContains(brief.Tags, "verifier:failed") {
		t.Fatalf("abnormal verifier item = %+v tags=%+v", item, brief.Tags)
	}

	brief = BuildDebugBrief(BatchResult{
		OK:       false,
		Verifier: VerifierResult{Command: "go test ./..."},
	})
	item = debugBriefItemByKind(brief, "verifier")
	if item == nil ||
		item.Severity != "warn" ||
		item.Message != "verifier command was configured but did not run; inspect runtime failure before trusting code-task outcome" ||
		!stringSliceContains(brief.Tags, "verifier:not_run") {
		t.Fatalf("not-run verifier item = %+v tags=%+v", item, brief.Tags)
	}

	if clean := BuildDebugBrief(BatchResult{
		OK: true,
		Verifier: VerifierResult{
			Command:  "go test ./...",
			Ran:      true,
			OK:       true,
			ExitCode: 0,
		},
	}); clean != nil {
		t.Fatalf("clean verifier pass should not emit debug brief: %+v", clean)
	}
}

func TestBuildDebugBriefClassifiesUnfinishedPlan(t *testing.T) {
	brief := BuildDebugBrief(BatchResult{
		OK: true,
		Plan: PlanStats{
			Calls:             3,
			ByAction:          map[string]int{"set": 1, "update": 2},
			TotalSteps:        4,
			CompletedSteps:    2,
			CurrentStepIndex:  3,
			CurrentStepStatus: "pending",
			CurrentStep:       "verify browser evidence",
		},
	})
	plan := debugBriefItemByKind(brief, "plan")
	if plan == nil ||
		plan.Severity != "warn" ||
		plan.Message != "latest plan state still has unfinished steps; inspect current step before resuming" ||
		plan.Counts["total_steps"] != 4 ||
		plan.Counts["completed_steps"] != 2 ||
		plan.Counts["current_step_index"] != 3 ||
		!stringSliceContains(brief.Tags, "plan:unfinished") {
		t.Fatalf("unfinished plan debug item = %+v tags=%+v", plan, brief.Tags)
	}
}

func TestBuildDebugBriefClassifiesToolRepairQuality(t *testing.T) {
	brief := BuildDebugBrief(BatchResult{
		OK: true,
		Repair: ToolRepairStats{
			Calls:          2,
			SucceededCalls: 2,
			Notes:          3,
			ByKind:         map[string]int{"alias_rename": 1, "type_coercion": 2},
		},
	})
	repair := debugBriefItemByKind(brief, "tool_repair")
	if repair == nil ||
		repair.Severity != "info" ||
		repair.Message != "tool calls were repaired or canonicalized; inspect examples for small-model tool drift" ||
		repair.Counts["calls"] != 2 ||
		repair.Counts["succeeded"] != 2 ||
		repair.Counts["notes"] != 3 ||
		repair.Counts["kind:type_coercion"] != 2 ||
		!stringSliceContains(repair.Inspect, "tool_repair_examples") ||
		!stringSliceContains(brief.Tags, "tool_repair:alias_rename") ||
		!stringSliceContains(brief.Tags, "tool_repair:type_coercion") {
		t.Fatalf("tool repair debug item = %+v tags=%+v", repair, brief.Tags)
	}

	brief = BuildDebugBrief(BatchResult{
		OK: true,
		Repair: ToolRepairStats{
			Calls:          2,
			SucceededCalls: 1,
			FailedCalls:    1,
			Notes:          1,
			ByKind:         map[string]int{"malformed_json": 1},
		},
	})
	repair = debugBriefItemByKind(brief, "tool_repair")
	if repair == nil ||
		repair.Severity != "warn" ||
		repair.Message != "tool repair failed for at least one call; inspect repair examples before trusting tool recovery" ||
		repair.Counts["failed"] != 1 ||
		!stringSliceContains(brief.Tags, "tool_repair:failed") ||
		!stringSliceContains(brief.Tags, "tool_repair:malformed_json") {
		t.Fatalf("failed tool repair debug item = %+v tags=%+v", repair, brief.Tags)
	}
}

func TestBuildDebugBriefClassifiesConversationRepair(t *testing.T) {
	brief := BuildDebugBrief(BatchResult{
		OK: true,
		ConversationRepairs: []sse.ConversationRepairedPayload{{
			MissingToolResults: 2,
			FailureKind:        "resume_missing_tool_result",
		}},
	})
	item := debugBriefItemByKind(brief, "conversation_repair")
	if item == nil ||
		item.Severity != "warn" ||
		item.Counts["events"] != 1 ||
		item.Counts["missing_tool_results"] != 2 ||
		item.Counts["kind:resume_missing_tool_result"] != 1 ||
		!stringSliceContains(brief.Tags, "conversation_repair:resume_missing_tool_result") {
		t.Fatalf("conversation repair item = %+v tags=%+v", item, brief.Tags)
	}
}

func TestBuildDebugBriefClassifiesForcedNoTools(t *testing.T) {
	brief := BuildDebugBrief(BatchResult{
		OK: true,
		ToolStats: ToolRuntimeStats{
			LoopGuardInterventions: 2,
			ForcedNoTools:          1,
		},
	})
	guard := debugBriefItemByKind(brief, "loop_guard")
	if guard == nil ||
		guard.Severity != "warn" ||
		guard.Message != "loop guard forced no-tool continuation; inspect repeated failures before trusting recovery" ||
		guard.Counts["interventions"] != 2 ||
		guard.Counts["forced_no_tools"] != 1 ||
		!stringSliceContains(guard.Inspect, "loop_guard_examples") ||
		!stringSliceContains(guard.Inspect, "loop_decisions") ||
		!stringSliceContains(brief.Tags, "loop_guard") ||
		!stringSliceContains(brief.Tags, "loop_guard:forced_no_tools") {
		t.Fatalf("forced no-tools debug item = %+v tags=%+v", guard, brief.Tags)
	}
}

func TestBuildDebugBriefClassifiesResearchCheckpoint(t *testing.T) {
	brief := BuildDebugBrief(BatchResult{
		OK: true,
		LoopDecisionStats: LoopDecisionStats{
			ByKind: map[string]int{"research_checkpoint": 1},
			Examples: []LoopDecision{{
				Kind:           "research_checkpoint",
				Decision:       "trigger",
				Trigger:        "external_calibration_requested",
				RequiredAction: "Compare mainstream agent designs before changing durable direction.",
			}},
		},
	})
	item := debugBriefItemByKind(brief, "research_checkpoint")
	if item == nil ||
		item.Severity != "warn" ||
		item.Message != "research checkpoint triggered without external evidence or delegated research; inspect whether the turn stayed internally calibrated" ||
		item.Counts["decisions"] != 1 ||
		!stringSliceContains(item.Inspect, "loop_decision_examples") ||
		!stringSliceContains(item.Inspect, "source_evidence") ||
		!stringSliceContains(item.Inspect, "child_transcripts") ||
		!stringSliceContains(brief.Tags, "research_checkpoint") ||
		!stringSliceContains(brief.Tags, "loop_decision:research_checkpoint") ||
		!stringSliceContains(brief.Tags, "research_checkpoint:no_external_evidence") {
		t.Fatalf("research checkpoint debug item = %+v tags=%+v", item, brief.Tags)
	}

	brief = BuildDebugBrief(BatchResult{
		OK: true,
		ToolStats: ToolRuntimeStats{
			SourceAccessResults: 1,
		},
		LoopDecisionStats: LoopDecisionStats{
			ByKind: map[string]int{"research_checkpoint": 1},
		},
	})
	item = debugBriefItemByKind(brief, "research_checkpoint")
	if item == nil ||
		item.Severity != "info" ||
		item.Message != "loop triggered an external-calibration checkpoint; inspect decision action before changing durable direction" ||
		stringSliceContains(brief.Tags, "research_checkpoint:no_external_evidence") {
		t.Fatalf("evidence-backed research checkpoint debug item = %+v tags=%+v", item, brief.Tags)
	}
}

func TestBuildDebugBriefClassifiesSessionRecallQuality(t *testing.T) {
	brief := BuildDebugBrief(BatchResult{
		OK: true,
		ToolStats: ToolRuntimeStats{
			SessionSearchCalls:        1,
			SessionSearchResults:      2,
			SessionSearchContextHits:  2,
			SessionSearchMatchedTerms: 3,
		},
	})
	recall := debugBriefItemByKind(brief, "recall")
	if recall == nil ||
		recall.Severity != "info" ||
		recall.Message != "session recall returned history with adjacent context or persisted task-state anchors" ||
		recall.Counts["context_hits"] != 2 ||
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
		recall.Message != "session recall returned hits without adjacent context or persisted task-state anchors; inspect examples for stale or shallow recovery" ||
		!stringSliceContains(brief.Tags, "recall:no_context") {
		t.Fatalf("shallow recall debug item = %+v tags=%+v", recall, brief.Tags)
	}

	brief = BuildDebugBrief(BatchResult{
		OK: true,
		ToolStats: ToolRuntimeStats{
			SessionSearchCalls:        1,
			SessionSearchResults:      2,
			SessionSearchContextHits:  1,
			SessionSearchMatchedTerms: 3,
		},
	})
	recall = debugBriefItemByKind(brief, "recall")
	if recall == nil ||
		recall.Severity != "warn" ||
		recall.Message != "session recall returned only partial adjacent context; inspect examples for incomplete recovery" ||
		recall.Counts["context_hits"] != 1 ||
		!stringSliceContains(brief.Tags, "recall:weak_context") {
		t.Fatalf("weak context recall debug item = %+v tags=%+v", recall, brief.Tags)
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
		!stringSliceContains(brief.Tags, "empty_recall") ||
		!stringSliceContains(brief.Tags, "empty_recall:no_recent_sessions") {
		t.Fatalf("empty recall debug item = %+v tags=%+v", empty, brief.Tags)
	}

	brief = BuildDebugBrief(BatchResult{
		OK: true,
		ToolStats: ToolRuntimeStats{
			SessionSearchCalls:   1,
			SessionSearchResults: 0,
			SessionSearchRecent:  2,
		},
	})
	empty = debugBriefItemByKind(brief, "empty_recall")
	if empty == nil ||
		empty.Severity != "info" ||
		empty.Message != "session recall returned no direct hits but exposed recent session anchors for retry" ||
		empty.Counts["recent"] != 2 ||
		!stringSliceContains(brief.Tags, "empty_recall:recent_sessions") {
		t.Fatalf("recent-session anchor recall debug item = %+v tags=%+v", empty, brief.Tags)
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

	brief = BuildDebugBrief(BatchResult{
		OK: true,
		ToolStats: ToolRuntimeStats{
			SourceAccessResults:  1,
			SourceAccessVerified: 1,
			SourceAccessNetwork:  1,
		},
		SourceAccessExamples: []SourceAccessExample{{
			Tool:         "browser_network_read",
			Status:       "network",
			URLField:     "browser_network_url",
			SourceMethod: "network_xhr_fetch",
			Ref:          "n1",
		}},
	})
	item = debugBriefItemByKind(brief, "source_access")
	if item == nil ||
		item.Severity != "warn" ||
		item.Message != "network source evidence lacked response diagnostics; inspect status/content_type before trusting current facts" ||
		item.Counts["missing_response_diagnostics"] != 1 ||
		!stringSliceContains(brief.Tags, "source_network:missing_response_diagnostics") {
		t.Fatalf("network source missing diagnostics item = %+v tags=%+v", item, brief.Tags)
	}

	brief = BuildDebugBrief(BatchResult{
		OK: true,
		ToolStats: ToolRuntimeStats{
			SourceAccessResults:  1,
			SourceAccessVerified: 1,
			SourceAccessNetwork:  1,
		},
		SourceAccessExamples: []SourceAccessExample{{
			Tool:         "browser_network_read",
			Status:       "network",
			URLField:     "browser_network_url",
			SourceMethod: "network_xhr_fetch",
			Ref:          "n1",
			HTTPStatus:   "200",
			ContentType:  "application/json",
		}},
	})
	if stringSliceContains(brief.Tags, "source_network:missing_response_diagnostics") {
		t.Fatalf("network source with diagnostics should not be tagged: %+v", brief.Tags)
	}

	brief = BuildDebugBrief(BatchResult{
		OK: true,
		ToolStats: ToolRuntimeStats{
			SourceAccessResults:  1,
			SourceAccessVerified: 1,
			SourceAccessNetwork:  1,
		},
		SourceAccessExamples: []SourceAccessExample{{
			Tool:         "browser_network_read",
			Status:       "network",
			URLField:     "browser_network_url",
			SourceMethod: "network_xhr_fetch",
			Ref:          "n1",
			HTTPStatus:   "200",
			ContentType:  "application/json",
			BodyBytes:    70,
			BodyOffset:   14,
			ShowingBytes: 12,
			OmittedAfter: 44,
			NextOffset:   26,
			HasMore:      true,
		}},
	})
	item = debugBriefItemByKind(brief, "source_access")
	if item == nil ||
		item.Severity != "warn" ||
		item.Message != "network source evidence has unresolved partial reads; continue from next_offset or use a narrower json_path before trusting missing fields" ||
		item.Counts["partial_network_reads"] != 1 ||
		!stringSliceContains(brief.Tags, "source_network:partial_read") {
		t.Fatalf("partial network read item = %+v tags=%+v", item, brief.Tags)
	}

	brief = BuildDebugBrief(BatchResult{
		OK: true,
		ToolStats: ToolRuntimeStats{
			SourceAccessResults:  2,
			SourceAccessVerified: 2,
			SourceAccessNetwork:  2,
		},
		SourceAccessExamples: []SourceAccessExample{{
			Tool:         "browser_network_read",
			Status:       "network",
			URLField:     "browser_network_url",
			SourceMethod: "network_xhr_fetch",
			Ref:          "n1",
			HTTPStatus:   "200",
			ContentType:  "application/json",
			BodyBytes:    70,
			BodyOffset:   14,
			ShowingBytes: 12,
			OmittedAfter: 44,
			NextOffset:   26,
			HasMore:      true,
		}, {
			Tool:         "browser_network_read",
			Status:       "network",
			URLField:     "browser_network_url",
			SourceMethod: "network_xhr_fetch",
			Ref:          "n1",
			HTTPStatus:   "200",
			ContentType:  "application/json",
			BodyBytes:    70,
			BodyOffset:   26,
			ShowingBytes: 44,
		}},
	})
	if stringSliceContains(brief.Tags, "source_network:partial_read") {
		t.Fatalf("continued network read should not be tagged partial: %+v", brief.Tags)
	}

	brief = BuildDebugBrief(BatchResult{
		OK: true,
		ToolStats: ToolRuntimeStats{
			SourceAccessResults:  2,
			SourceAccessVerified: 2,
			SourceAccessNetwork:  2,
		},
		SourceAccessExamples: []SourceAccessExample{{
			Tool:         "browser_network_read",
			Status:       "network",
			URLField:     "browser_network_url",
			SourceMethod: "network_xhr_fetch",
			Ref:          "n1",
			HTTPStatus:   "200",
			ContentType:  "application/json",
			BodyBytes:    200,
			BodyOffset:   0,
			ShowingBytes: 80,
			OmittedAfter: 120,
			NextOffset:   80,
			HasMore:      true,
		}, {
			Tool:         "browser_network_read",
			Status:       "network",
			URLField:     "browser_network_url",
			SourceMethod: "network_xhr_fetch",
			Ref:          "n1",
			HTTPStatus:   "200",
			ContentType:  "application/json",
			BodyBytes:    200,
			BodyOffset:   80,
			ShowingBytes: 80,
			OmittedAfter: 40,
			NextOffset:   160,
			HasMore:      true,
		}},
	})
	if !stringSliceContains(brief.Tags, "source_network:partial_read") {
		t.Fatalf("continued but still-truncated network read should remain tagged partial: %+v", brief.Tags)
	}

	brief = BuildDebugBrief(BatchResult{
		OK: true,
		ToolStats: ToolRuntimeStats{
			SourceAccessResults:  2,
			SourceAccessVerified: 2,
			SourceAccessNetwork:  2,
		},
		SourceAccessExamples: []SourceAccessExample{{
			Tool:         "browser_network_read",
			Status:       "network",
			URLField:     "browser_network_url",
			SourceMethod: "network_xhr_fetch",
			Ref:          "n2",
			HTTPStatus:   "200",
			ContentType:  "application/json",
			BodyBytes:    120000,
			ShowingBytes: 65536,
			OmittedAfter: 54464,
			NextOffset:   65536,
			HasMore:      true,
		}, {
			Tool:         "browser_network_read",
			Status:       "network",
			URLField:     "browser_network_url",
			SourceMethod: "network_xhr_fetch",
			Ref:          "n2",
			HTTPStatus:   "200",
			ContentType:  "application/json",
			JSONPath:     "$.subnet.market_cap",
			BodyBytes:    16,
			ShowingBytes: 16,
		}},
	})
	if stringSliceContains(brief.Tags, "source_network:partial_read") {
		t.Fatalf("json_path follow-up should resolve partial read: %+v", brief.Tags)
	}
}

func TestBuildDebugBriefClassifiesBrowserScrollStuckWithoutNetwork(t *testing.T) {
	brief := BuildDebugBrief(BatchResult{
		OK: true,
		BrowserScrollExamples: []BrowserScrollExample{{
			Status:            "boundary",
			Movement:          "none",
			Boundary:          "bottom",
			SuggestedNextStep: "use browser_network_read",
		}},
	})
	item := debugBriefItemByKind(brief, "browser_scroll")
	if item == nil ||
		item.Severity != "warn" ||
		item.Message != "browser scroll stalled without network-backed evidence; inspect network captures before trusting hidden dashboard values" ||
		item.Counts["scrolls"] != 1 ||
		item.Counts["boundary"] != 1 ||
		!stringSliceContains(item.Inspect, "browser_scroll_examples") ||
		!stringSliceContains(item.Inspect, "source_evidence") ||
		!stringSliceContains(brief.Tags, "browser_scroll") ||
		!stringSliceContains(brief.Tags, "browser_scroll:boundary") ||
		!stringSliceContains(brief.Tags, "browser_scroll:stuck_without_network") {
		t.Fatalf("browser scroll debug item = %+v tags=%+v", item, brief.Tags)
	}
}

func TestBuildDebugBriefMatchesBrowserNetworkRefsToSourceEvidence(t *testing.T) {
	brief := BuildDebugBrief(BatchResult{
		OK: true,
		ToolStats: ToolRuntimeStats{
			SourceAccessNetwork: 1,
		},
		BrowserNetworkExamples: []BrowserNetworkSearchExample{{
			Status:       "matches",
			Refs:         []string{"n1"},
			RequiresRead: true,
			NotCitable:   true,
		}},
		SourceAccessExamples: []SourceAccessExample{{
			Status:       "network",
			URLField:     "browser_network_url",
			SourceMethod: "network_xhr_fetch",
			Ref:          "n2",
		}},
	})
	item := debugBriefItemByKind(brief, "browser_network")
	if item == nil ||
		item.Severity != "warn" ||
		item.Message != "browser network searches found refs without matching network SourceAccess evidence; call browser_network_read before citing values" ||
		!stringSliceContains(brief.Tags, "browser_network:unread_refs") ||
		stringSliceContains(brief.Tags, "browser_network:refs") {
		t.Fatalf("unmatched browser network refs item = %+v tags=%+v", item, brief.Tags)
	}

	brief = BuildDebugBrief(BatchResult{
		OK: true,
		ToolStats: ToolRuntimeStats{
			SourceAccessNetwork: 1,
		},
		BrowserNetworkExamples: []BrowserNetworkSearchExample{{
			Status:       "matches",
			Refs:         []string{"n1"},
			RequiresRead: true,
			NotCitable:   true,
		}},
		SourceAccessExamples: []SourceAccessExample{{
			Status:       "network",
			URLField:     "browser_network_url",
			SourceMethod: "network_xhr_fetch",
			Ref:          "n1",
		}},
	})
	item = debugBriefItemByKind(brief, "browser_network")
	if item == nil ||
		item.Severity != "info" ||
		!stringSliceContains(brief.Tags, "browser_network:refs") ||
		stringSliceContains(brief.Tags, "browser_network:unread_refs") {
		t.Fatalf("matched browser network refs item = %+v tags=%+v", item, brief.Tags)
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

func TestBuildDebugBriefClassifiesTruncationArtifactQuality(t *testing.T) {
	brief := BuildDebugBrief(BatchResult{
		OK: true,
		ToolTruncation: ToolTruncationStats{
			ResultsTruncated:    2,
			ResultsOmittedBytes: 4096,
			ResultArtifacts:     1,
		},
	})
	item := debugBriefItemByKind(brief, "truncation")
	if item == nil ||
		item.Severity != "warn" ||
		item.Message != "tool results were truncated without matching artifacts; inspect tool timeline before trusting evidence" ||
		item.Counts["results"] != 2 ||
		item.Counts["artifacts"] != 1 ||
		item.Counts["missing_artifacts"] != 1 ||
		!stringSliceContains(item.Inspect, "tool_truncation_examples") ||
		!stringSliceContains(brief.Tags, "truncation:missing_artifact") {
		t.Fatalf("missing-artifact truncation item = %+v tags=%+v", item, brief.Tags)
	}

	brief = BuildDebugBrief(BatchResult{
		OK: true,
		ToolTruncation: ToolTruncationStats{
			ResultsTruncated: 2,
			ResultArtifacts:  2,
		},
	})
	item = debugBriefItemByKind(brief, "truncation")
	if item == nil ||
		item.Message != "tool or context output was truncated; inspect examples and artifacts before judging evidence" ||
		stringSliceContains(brief.Tags, "truncation:missing_artifact") {
		t.Fatalf("artifact-backed truncation item = %+v tags=%+v", item, brief.Tags)
	}

	brief = BuildDebugBrief(BatchResult{
		OK: true,
		ToolStats: ToolRuntimeStats{
			ToolContextTruncated:    3,
			ToolContextOmittedBytes: 9216,
		},
	})
	item = debugBriefItemByKind(brief, "truncation")
	if item == nil ||
		item.Message != "tool output was trimmed before entering model context; inspect tool timeline and context omitted bytes" ||
		item.Counts["tool_context"] != 3 ||
		item.Counts["omitted_context"] != 9216 ||
		item.Counts["missing_artifacts"] != 0 ||
		!stringSliceContains(brief.Tags, "truncation:tool_context") ||
		stringSliceContains(brief.Tags, "truncation:missing_artifact") {
		t.Fatalf("tool-context truncation item = %+v tags=%+v", item, brief.Tags)
	}

	brief = BuildDebugBrief(BatchResult{
		OK: true,
		ToolTruncation: ToolTruncationStats{
			ContextTruncated:        1,
			ContextOmittedBytes:     2048,
			ContextMissingArtifacts: 1,
		},
	})
	item = debugBriefItemByKind(brief, "truncation")
	if item == nil ||
		item.Counts["tool_context"] != 1 ||
		item.Counts["omitted_context"] != 2048 ||
		item.Counts["context_missing_artifacts"] != 1 ||
		item.Counts["missing_artifacts"] != 1 ||
		!stringSliceContains(brief.Tags, "truncation:tool_context") ||
		!stringSliceContains(brief.Tags, "truncation:missing_artifact") {
		t.Fatalf("context missing-artifact truncation item = %+v tags=%+v", item, brief.Tags)
	}

	brief = BuildDebugBrief(BatchResult{
		OK: true,
		ToolTruncation: ToolTruncationStats{
			ContextTruncated:    1,
			ContextOmittedBytes: 2048,
			ResultArtifacts:     1,
			ContextArtifacts:    1,
		},
	})
	item = debugBriefItemByKind(brief, "truncation")
	if item == nil ||
		item.Counts["missing_artifacts"] != 0 ||
		item.Counts["context_artifacts"] != 1 ||
		stringSliceContains(brief.Tags, "truncation:missing_artifact") {
		t.Fatalf("context artifact-backed truncation item = %+v tags=%+v", item, brief.Tags)
	}
}

func TestBuildDebugBriefClassifiesMemorySearchMissAnchors(t *testing.T) {
	brief := BuildDebugBrief(BatchResult{
		OK: true,
		MemorySearchMissExamples: []MemorySearchMissExample{{
			CallID:     "mem-search-empty",
			Query:      "helm deployment",
			TopicCount: 2,
			Topics:     []string{"deploy", "auth"},
		}},
	})

	item := debugBriefItemByKind(brief, "memory_search_miss")
	if item == nil ||
		item.Counts["calls"] != 1 ||
		item.Counts["misses"] != 1 ||
		item.Counts["topics"] != 2 ||
		item.Counts["anchor_examples"] != 1 ||
		item.Counts["no_anchor_examples"] != 0 ||
		!stringSliceContains(brief.Tags, "memory_search_miss") ||
		!stringSliceContains(brief.Tags, "recall:memory_topic_anchors") {
		t.Fatalf("memory search miss item = %+v tags=%+v", item, brief.Tags)
	}

	brief = BuildDebugBrief(BatchResult{
		OK: true,
		ToolStats: ToolRuntimeStats{
			MemorySearchCalls:  4,
			MemorySearchMisses: 3,
		},
	})
	item = debugBriefItemByKind(brief, "memory_search_miss")
	if item == nil || item.Counts["calls"] != 4 || item.Counts["misses"] != 3 {
		t.Fatalf("memory search miss stats-only item = %+v tags=%+v", item, brief.Tags)
	}
	if stringSliceContains(brief.Tags, "recall:memory_topic_anchors") ||
		stringSliceContains(brief.Tags, "recall:memory_no_topic_anchors") {
		t.Fatalf("stats-only memory miss should not infer anchor state: %+v tags=%+v", item, brief.Tags)
	}

	brief = BuildDebugBrief(BatchResult{
		OK: true,
		MemorySearchMissExamples: []MemorySearchMissExample{{
			CallID: "mem-user-empty",
			Target: "user",
			Query:  "ssh key preference",
		}},
	})
	item = debugBriefItemByKind(brief, "memory_search_miss")
	if item == nil ||
		item.Severity != "warn" ||
		item.Counts["calls"] != 1 ||
		item.Counts["misses"] != 1 ||
		item.Counts["topics"] != 0 ||
		item.Counts["anchor_examples"] != 0 ||
		item.Counts["no_anchor_examples"] != 1 ||
		!stringSliceContains(brief.Tags, "recall:memory_no_topic_anchors") ||
		stringSliceContains(brief.Tags, "recall:memory_topic_anchors") {
		t.Fatalf("memory search no-anchor item = %+v tags=%+v", item, brief.Tags)
	}

	brief = BuildDebugBrief(BatchResult{
		OK: true,
		MemorySearchMissExamples: []MemorySearchMissExample{{
			CallID:     "mem-search-anchored",
			Query:      "deploy",
			TopicCount: 1,
			Topics:     []string{"deploy"},
		}, {
			CallID: "mem-search-no-anchor",
			Query:  "ssh key preference",
		}},
	})
	item = debugBriefItemByKind(brief, "memory_search_miss")
	if item == nil ||
		item.Severity != "warn" ||
		item.Message != "some memory search misses lacked topic anchors; inspect target/topic/query before retrying" ||
		item.Counts["anchor_examples"] != 1 ||
		item.Counts["no_anchor_examples"] != 1 ||
		!stringSliceContains(brief.Tags, "recall:memory_topic_anchors") ||
		!stringSliceContains(brief.Tags, "recall:memory_no_topic_anchors") {
		t.Fatalf("mixed memory search miss item = %+v tags=%+v", item, brief.Tags)
	}

	brief = BuildDebugBrief(BatchResult{
		OK: true,
		ToolStats: ToolRuntimeStats{
			MemorySearchCalls: 4,
		},
	})
	if item := debugBriefItemByKind(brief, "memory_search_miss"); item != nil {
		t.Fatalf("memory search calls without misses should not create miss item: %+v tags=%+v", item, brief.Tags)
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
