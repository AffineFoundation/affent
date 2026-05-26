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
