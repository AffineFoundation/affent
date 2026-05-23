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
	reg.Add(&agent.Tool{
		Name: "shell",
		Execute: func(context.Context, json.RawMessage) (string, error) {
			t.Fatal("plan-only must not keep executable non-plan tools")
			return "", nil
		},
	})
	b := &loopBundle{loop: &agent.Loop{Tools: reg}}

	if err := enableRunPlanOnly(b); err != nil {
		t.Fatalf("enableRunPlanOnly: %v", err)
	}
	if b.loop.FirstToolPolicy == nil || b.loop.FirstToolPolicy.ToolName != agent.PlanToolName {
		t.Fatalf("first tool policy = %+v, want plan", b.loop.FirstToolPolicy)
	}
	if b.loop.MaxToolCalls != runPlanOnlyMaxToolCalls {
		t.Fatalf("MaxToolCalls = %d, want %d", b.loop.MaxToolCalls, runPlanOnlyMaxToolCalls)
	}
	if !b.loop.FinalNoToolsOnMaxTurns {
		t.Fatal("FinalNoToolsOnMaxTurns should be enabled for plan-only")
	}
	defs := b.loop.Tools.Defs()
	if len(defs) != 1 || defs[0].Function.Name != agent.PlanToolName {
		t.Fatalf("plan-only tool defs = %+v, want only plan", defs)
	}
	if _, ok := b.loop.Tools.Get("shell"); ok {
		t.Fatal("plan-only should remove non-plan tools from the run registry")
	}
}

func TestEnableRunPlanOnlyRequiresPlanTool(t *testing.T) {
	err := enableRunPlanOnly(&loopBundle{loop: &agent.Loop{Tools: agent.NewRegistry()}})
	if err == nil || !strings.Contains(err.Error(), "plan tool is not available") {
		t.Fatalf("enableRunPlanOnly error = %v, want missing plan tool", err)
	}
}
