package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	agent "github.com/affinefoundation/affent/internal/agent"
)

func TestEnableRunPlanOnlyInstallsPlanPolicyAndBudget(t *testing.T) {
	reg := agent.NewRegistry()
	reg.Add(&agent.Tool{
		Name: agent.PlanToolName,
		Execute: func(context.Context, json.RawMessage) (string, error) {
			return "{}", nil
		},
	})
	b := &loopBundle{loop: &agent.Loop{Tools: reg}}

	if err := enableRunPlanOnly(b); err != nil {
		t.Fatalf("enableRunPlanOnly: %v", err)
	}
	if b.loop.FirstToolPolicy == nil || b.loop.FirstToolPolicy.ToolName != agent.PlanToolName {
		t.Fatalf("first tool policy = %+v, want plan", b.loop.FirstToolPolicy)
	}
	if b.loop.MaxToolCalls != 1 {
		t.Fatalf("MaxToolCalls = %d, want 1", b.loop.MaxToolCalls)
	}
	if !b.loop.FinalNoToolsOnMaxTurns {
		t.Fatal("FinalNoToolsOnMaxTurns should be enabled for plan-only")
	}
}

func TestEnableRunPlanOnlyRequiresPlanTool(t *testing.T) {
	err := enableRunPlanOnly(&loopBundle{loop: &agent.Loop{Tools: agent.NewRegistry()}})
	if err == nil || !strings.Contains(err.Error(), "plan tool is not available") {
		t.Fatalf("enableRunPlanOnly error = %v, want missing plan tool", err)
	}
}
