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

func TestValidateRunModeFlags(t *testing.T) {
	for _, tc := range []struct {
		name        string
		flags       commonFlags
		planOnly    bool
		executePlan bool
		wantErr     string
	}{
		{name: "normal run", flags: commonFlags{}, wantErr: ""},
		{name: "plan only", flags: commonFlags{}, planOnly: true, wantErr: ""},
		{name: "execute explicit session", flags: commonFlags{sessionID: "planned"}, executePlan: true, wantErr: ""},
		{name: "execute continue", flags: commonFlags{continueLast: true}, executePlan: true, wantErr: ""},
		{name: "conflict", flags: commonFlags{sessionID: "planned"}, planOnly: true, executePlan: true, wantErr: "cannot be used together"},
		{name: "execute needs selected session", flags: commonFlags{}, executePlan: true, wantErr: "requires --session-id or --continue"},
		{name: "blank session id still needs selection", flags: commonFlags{sessionID: " \t "}, executePlan: true, wantErr: "requires --session-id or --continue"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateRunModeFlags(tc.flags, tc.planOnly, tc.executePlan)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("validateRunModeFlags error = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("validateRunModeFlags error = %v, want %q", err, tc.wantErr)
			}
		})
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

func TestRunPlanOnlyNextStepLinePrintsExecutablePlanCommand(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "work dir's")
	convDir := filepath.Join(workspace, ".affentctl")
	if err := os.MkdirAll(convDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(localSessionPlanPath(convDir, "planned"), []byte(`{"version":1,"steps":[{"text":"ship","status":"pending"}]}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := runPlanOnlyNextStepLine(&loopBundle{workspace: workspace, sessionID: "planned"})
	for _, want := range []string{
		`[plan] saved for session "planned"`,
		"affentctl run",
		"--execute-plan",
		"--workspace " + shellQuoteForEnv(workspace),
		"--session-id 'planned'",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("next-step line missing %q:\n%s", want, got)
		}
	}
}

func TestRunPlanOnlyNextStepLineSkipsNonExecutablePlans(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
	}{
		{name: "missing"},
		{name: "empty", raw: `{"version":1,"steps":[]}`},
		{name: "done", raw: `{"version":1,"steps":[{"text":"done","status":"completed"}]}`},
		{name: "blocked", raw: `{"version":1,"steps":[{"text":"need input","status":"blocked"}]}`},
		{name: "bad", raw: `{`},
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
			if got := runPlanOnlyNextStepLine(&loopBundle{workspace: workspace, sessionID: "planned"}); got != "" {
				t.Fatalf("next-step line = %q, want empty", got)
			}
		})
	}
}
