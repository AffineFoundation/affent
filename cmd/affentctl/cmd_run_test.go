package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
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

func TestPrepareRunExecutePlanUsesPersistedUnfinishedPlan(t *testing.T) {
	workspace := t.TempDir()
	convDir := filepath.Join(workspace, ".affentctl")
	if err := os.MkdirAll(convDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(localSessionPlanPath(convDir, "planned"), []byte(`{"version":1,"steps":[{"text":"inspect","status":"completed"},{"text":"ship","status":"pending"}]}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	b := &loopBundle{workspace: workspace, sessionID: "planned"}

	got, err := prepareRunExecutePlan(b, "")
	if err != nil {
		t.Fatalf("prepareRunExecutePlan: %v", err)
	}
	for _, want := range []string{
		"Execute-plan mode is enabled.",
		"The user has confirmed execution",
		"plan:1/2",
		"Proceed with the active persisted plan.",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("execute-plan prompt missing %q:\n%s", want, got)
		}
	}
}

func TestPrepareRunExecutePlanRejectsNonExecutablePlans(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
		want string
	}{
		{name: "missing", want: "has no persisted plan"},
		{name: "empty", raw: `{"version":1,"steps":[]}`, want: "empty plan"},
		{name: "done", raw: `{"version":1,"steps":[{"text":"done","status":"completed"}]}`, want: "already done"},
		{name: "blocked", raw: `{"version":1,"steps":[{"text":"need input","status":"blocked"}]}`, want: "plan is blocked"},
		{name: "bad", raw: `{`, want: "unreadable plan"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			workspace := t.TempDir()
			convDir := filepath.Join(workspace, ".affentctl")
			if err := os.MkdirAll(convDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if tc.raw != "" {
				if err := os.WriteFile(localSessionPlanPath(convDir, "planned"), []byte(tc.raw), 0o644); err != nil {
					t.Fatal(err)
				}
			}

			_, err := prepareRunExecutePlan(&loopBundle{workspace: workspace, sessionID: "planned"}, "continue")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("prepareRunExecutePlan error = %v, want %q", err, tc.want)
			}
		})
	}
}
