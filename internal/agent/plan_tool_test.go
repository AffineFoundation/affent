package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPlanToolSetUpdateViewPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "plan.json")
	tool := planTool(path)

	out, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"set","steps":[{"text":"inspect failing test","status":"in_progress"},{"text":"patch code"}]}`))
	if err != nil {
		t.Fatalf("set: %v", err)
	}
	var st planState
	if err := json.Unmarshal([]byte(out), &st); err != nil {
		t.Fatalf("decode set response: %v\n%s", err, out)
	}
	if st.Message != "plan set" || len(st.Steps) != 2 {
		t.Fatalf("set response = %+v", st)
	}
	if st.Steps[1].Status != "pending" {
		t.Fatalf("default status = %q, want pending", st.Steps[1].Status)
	}

	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"update","index":1,"status":"completed","evidence":["internal/agent/plan_tool.go"]}`)); err != nil {
		t.Fatalf("update: %v", err)
	}

	reopened := planTool(path)
	out, err = reopened.Execute(context.Background(), json.RawMessage(`{"action":"view"}`))
	if err != nil {
		t.Fatalf("view: %v", err)
	}
	if err := json.Unmarshal([]byte(out), &st); err != nil {
		t.Fatalf("decode view response: %v\n%s", err, out)
	}
	if len(st.Steps) != 2 {
		t.Fatalf("persisted steps len = %d, want 2", len(st.Steps))
	}
	if st.Steps[0].Status != "completed" || len(st.Steps[0].Evidence) != 1 {
		t.Fatalf("persisted step = %+v", st.Steps[0])
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("plan file should exist: %v", err)
	}
}

func TestPlanToolViewNormalizesPersistedPlanState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plan.json")
	if err := os.WriteFile(path, []byte(`{"version":1,"steps":[{"text":"  Ship  ","status":" IN_PROGRESS ","evidence":[" file ","file"," test "]},{"text":" ship ","status":"pending"},{"text":"Finish","status":"in_progress"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := planTool(path).Execute(context.Background(), json.RawMessage(`{"action":"view"}`))
	if err != nil {
		t.Fatalf("view: %v", err)
	}
	var st planState
	if err := json.Unmarshal([]byte(out), &st); err != nil {
		t.Fatalf("decode view response: %v\n%s", err, out)
	}
	if len(st.Steps) != 2 {
		t.Fatalf("steps len = %d, want duplicate persisted step dropped: %+v", len(st.Steps), st.Steps)
	}
	if st.Steps[0].Text != "Ship" || st.Steps[0].Status != "in_progress" || st.Steps[1].Status != "pending" {
		t.Fatalf("normalized steps = %+v", st.Steps)
	}
	wantEvidence := []string{"file", "test"}
	if got := st.Steps[0].Evidence; len(got) != len(wantEvidence) || got[0] != wantEvidence[0] || got[1] != wantEvidence[1] {
		t.Fatalf("evidence = %+v, want %+v", got, wantEvidence)
	}
}

func TestPlanToolTreatsBlankPlanFileAsNoActivePlan(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plan.json")
	if err := os.WriteFile(path, []byte(" \n\t "), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := planTool(path).Execute(context.Background(), json.RawMessage(`{"action":"view"}`))
	if err != nil {
		t.Fatalf("view blank plan: %v", err)
	}
	if !strings.Contains(out, "no active plan") {
		t.Fatalf("blank plan output = %s, want no active plan", out)
	}
}

func TestPlanToolClearRemovesPersistedPlan(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plan.json")
	tool := planTool(path)
	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"set","steps":[{"text":"x"}]}`)); err != nil {
		t.Fatalf("set: %v", err)
	}
	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"clear"}`)); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("plan file err = %v, want not exists", err)
	}
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"view"}`))
	if err != nil {
		t.Fatalf("view: %v", err)
	}
	if !strings.Contains(out, "no active plan") {
		t.Fatalf("view after clear = %s", out)
	}
}

func TestPlanToolRejectsUnknownAndUnusedArgs(t *testing.T) {
	tool := planTool(filepath.Join(t.TempDir(), "plan.json"))
	for _, tc := range []struct {
		name string
		args string
		want string
	}{
		{name: "unknown field", args: `{"action":"view","session_id":"x"}`, want: `unknown field "session_id"`},
		{name: "unused set field", args: `{"action":"set","steps":[{"text":"x"}],"index":1}`, want: "unused field(s) for action=set: index"},
		{name: "unused view field", args: `{"action":"view","steps":[{"text":"x"}]}`, want: "unused field(s) for action=view: steps"},
		{name: "update needs change", args: `{"action":"update","index":1}`, want: "no active plan"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tool.Execute(context.Background(), json.RawMessage(tc.args))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestPlanToolRejectsAmbiguousInProgress(t *testing.T) {
	tool := planTool(filepath.Join(t.TempDir(), "plan.json"))
	_, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"set","steps":[{"text":"a","status":"in_progress"},{"text":"b","status":"in_progress"}]}`))
	if err == nil || !strings.Contains(err.Error(), "only one plan step may be in_progress") {
		t.Fatalf("error = %v, want in_progress rejection", err)
	}
}

func TestPlanToolRejectsDuplicateSteps(t *testing.T) {
	tool := planTool(filepath.Join(t.TempDir(), "plan.json"))
	_, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"set","steps":[{"text":"Run tests"},{"text":" run   TESTS "}]}`))
	if err == nil || !strings.Contains(err.Error(), "step 2 duplicates step 1") {
		t.Fatalf("error = %v, want duplicate-step rejection", err)
	}
}

func TestPlanToolDeduplicatesEvidenceRefs(t *testing.T) {
	tool := planTool(filepath.Join(t.TempDir(), "plan.json"))
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"set","steps":[{"text":"ship","evidence":[" internal/agent/plan_tool.go ","internal/agent/plan_tool.go","go test ./internal/agent","go test ./internal/agent"]}]}`))
	if err != nil {
		t.Fatalf("set: %v", err)
	}
	var st planState
	if err := json.Unmarshal([]byte(out), &st); err != nil {
		t.Fatalf("decode set response: %v\n%s", err, out)
	}
	want := []string{"internal/agent/plan_tool.go", "go test ./internal/agent"}
	if len(st.Steps) != 1 || len(st.Steps[0].Evidence) != len(want) {
		t.Fatalf("evidence = %+v, want %+v", st.Steps, want)
	}
	for i := range want {
		if st.Steps[0].Evidence[i] != want[i] {
			t.Fatalf("evidence[%d] = %q, want %q", i, st.Steps[0].Evidence[i], want[i])
		}
	}
}

func TestPlanToolEvidenceLimitCountsUniqueRefs(t *testing.T) {
	tool := planTool(filepath.Join(t.TempDir(), "plan.json"))
	evidence := strings.Repeat(`"internal/agent/plan_tool.go",`, maxPlanEvidence+2) + `"go test ./internal/agent"`
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"set","steps":[{"text":"ship","evidence":[`+evidence+`]}]}`))
	if err != nil {
		t.Fatalf("set with repeated refs: %v", err)
	}
	var st planState
	if err := json.Unmarshal([]byte(out), &st); err != nil {
		t.Fatalf("decode set response: %v\n%s", err, out)
	}
	if got := len(st.Steps[0].Evidence); got != 2 {
		t.Fatalf("evidence len = %d, want 2 unique refs", got)
	}
}

func TestPlanToolNormalizesActionAndStatusCase(t *testing.T) {
	tool := planTool(filepath.Join(t.TempDir(), "plan.json"))
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"action":" Set ","steps":[{"text":"ship","status":" IN_PROGRESS "}]}`))
	if err != nil {
		t.Fatalf("set: %v", err)
	}
	var st planState
	if err := json.Unmarshal([]byte(out), &st); err != nil {
		t.Fatalf("decode set response: %v\n%s", err, out)
	}
	if len(st.Steps) != 1 || st.Steps[0].Status != "in_progress" {
		t.Fatalf("steps = %+v, want normalized in_progress", st.Steps)
	}

	out, err = tool.Execute(context.Background(), json.RawMessage(`{"action":"UPDATE","index":1,"status":" Completed "}`))
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if err := json.Unmarshal([]byte(out), &st); err != nil {
		t.Fatalf("decode update response: %v\n%s", err, out)
	}
	if st.Steps[0].Status != "completed" {
		t.Fatalf("status = %q, want completed", st.Steps[0].Status)
	}
}

func TestPlanToolRejectsSymlinkPlanFile(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside-plan.json")
	if err := os.WriteFile(outside, []byte(`{"version":1,"steps":[{"text":"outside"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "plan.json")
	if err := os.Symlink(outside, path); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	tool := planTool(path)
	_, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"view"}`))
	if err == nil || !strings.Contains(err.Error(), "plan path must not be a symlink") {
		t.Fatalf("view error = %v, want symlink rejection", err)
	}
	_, err = tool.Execute(context.Background(), json.RawMessage(`{"action":"set","steps":[{"text":"safe"}]}`))
	if err == nil || !strings.Contains(err.Error(), "plan path must not be a symlink") {
		t.Fatalf("set error = %v, want symlink rejection", err)
	}
	_, err = tool.Execute(context.Background(), json.RawMessage(`{"action":"clear"}`))
	if err == nil || !strings.Contains(err.Error(), "plan path must not be a symlink") {
		t.Fatalf("clear error = %v, want symlink rejection", err)
	}
	if _, err := os.Lstat(path); err != nil {
		t.Fatalf("symlink plan should remain for operator inspection: %v", err)
	}
	if _, err := os.Lstat(outside); err != nil {
		t.Fatalf("outside plan should remain: %v", err)
	}
}

func TestPlanToolRejectsOversizedPlanFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plan.json")
	if err := os.WriteFile(path, []byte(strings.Repeat(" ", maxPlanStateBytes+1)), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := planTool(path)
	_, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"view"}`))
	if err == nil || !strings.Contains(err.Error(), "plan file exceeds") {
		t.Fatalf("view error = %v, want size rejection", err)
	}
}

func TestPlanToolClearRejectsDirectoryPlanPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plan.json")
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	tool := planTool(path)
	_, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"clear"}`))
	if err == nil || !strings.Contains(err.Error(), "plan path is a directory") {
		t.Fatalf("clear error = %v, want directory rejection", err)
	}
	if info, err := os.Lstat(path); err != nil || !info.IsDir() {
		t.Fatalf("directory plan path should remain, info=%+v err=%v", info, err)
	}
}

func TestPlanToolWriteDoesNotFollowStaleTempSymlink(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plan.json")
	outside := filepath.Join(t.TempDir(), "outside-plan.json")
	if err := os.WriteFile(outside, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, path+".tmp"); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	tool := planTool(path)
	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"set","steps":[{"text":"safe"}]}`)); err != nil {
		t.Fatalf("set: %v", err)
	}
	raw, err := os.ReadFile(outside)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "outside" {
		t.Fatalf("outside file was modified through temp symlink: %q", raw)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Fatal("plan file must be a regular file, not a symlink")
	}
}

func TestPlanToolUpdateRequiresChangedField(t *testing.T) {
	tool := planTool(filepath.Join(t.TempDir(), "plan.json"))
	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"set","steps":[{"text":"x"}]}`)); err != nil {
		t.Fatalf("set: %v", err)
	}
	_, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"update","index":1}`))
	if err == nil || !strings.Contains(err.Error(), "update requires at least one") {
		t.Fatalf("error = %v, want changed-field rejection", err)
	}
}

func TestPlanToolUpdateRequiresIndexWhenPlanExists(t *testing.T) {
	tool := planTool(filepath.Join(t.TempDir(), "plan.json"))
	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"set","steps":[{"text":"x"}]}`)); err != nil {
		t.Fatalf("set: %v", err)
	}
	_, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"update","status":"completed"}`))
	if err == nil || !strings.Contains(err.Error(), "index is required when action=update") {
		t.Fatalf("error = %v, want missing-index rejection", err)
	}
}

func TestPlanToolSchemaRejectsUnknownArguments(t *testing.T) {
	tool := planTool(filepath.Join(t.TempDir(), "plan.json"))
	var schema struct {
		AdditionalProperties *bool `json:"additionalProperties"`
		Properties           map[string]struct {
			Maximum  int `json:"maximum"`
			MaxItems int `json:"maxItems"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(tool.Schema, &schema); err != nil {
		t.Fatal(err)
	}
	if schema.AdditionalProperties == nil || *schema.AdditionalProperties {
		t.Fatalf("plan schema must reject unknown args")
	}
	if schema.Properties["index"].Maximum != maxPlanSteps {
		t.Fatalf("index maximum = %d, want %d", schema.Properties["index"].Maximum, maxPlanSteps)
	}
	if schema.Properties["steps"].MaxItems != maxPlanSteps {
		t.Fatalf("steps maxItems = %d, want %d", schema.Properties["steps"].MaxItems, maxPlanSteps)
	}
}

func TestRegisterBuiltinsIncludesPlanWhenConfigured(t *testing.T) {
	reg := NewRegistry()
	RegisterBuiltins(reg, BuiltinDeps{
		HostWorkspaceDir: t.TempDir(),
		PlanPath:         filepath.Join(t.TempDir(), "plan.json"),
	})
	if _, ok := reg.Get(PlanToolName); !ok {
		t.Fatal("plan tool should be registered when PlanPath is set")
	}
}

func TestWithPlanSystemGuidanceIsIdempotent(t *testing.T) {
	first := WithPlanSystemGuidance("base")
	second := WithPlanSystemGuidance(first)
	if first != second {
		t.Fatal("plan system guidance should be idempotent")
	}
	if !strings.Contains(first, planSystemGuidanceMarker) {
		t.Fatalf("missing guidance marker:\n%s", first)
	}
}

func TestPlanFirstToolPolicyAlwaysRequiresPlan(t *testing.T) {
	policy := PlanFirstToolPolicy()
	if policy == nil {
		t.Fatal("policy missing")
	}
	if policy.ToolName != PlanToolName {
		t.Fatalf("policy tool = %q, want %q", policy.ToolName, PlanToolName)
	}
	if policy.Trigger == nil || !policy.Trigger("change several files") || !policy.Trigger("") {
		t.Fatal("plan-first policy should trigger for every plan-only request")
	}
	if !strings.Contains(policy.Rejection, "plan_only") || !strings.Contains(policy.Rejection, "action=set") {
		t.Fatalf("rejection should guide recovery, got %q", policy.Rejection)
	}
}

func TestPlanOnlyTurnOptionsNarrowsToolSurface(t *testing.T) {
	reg := NewRegistry()
	reg.Add(&Tool{Name: PlanToolName})
	reg.Add(&Tool{Name: "shell"})
	opts, err := PlanOnlyTurnOptions(reg, 2)
	if err != nil {
		t.Fatalf("PlanOnlyTurnOptions: %v", err)
	}
	if opts.FirstToolPolicy == nil || opts.FirstToolPolicy.ToolName != PlanToolName {
		t.Fatalf("first tool policy = %+v, want plan", opts.FirstToolPolicy)
	}
	if opts.MaxToolCalls != 2 || !opts.FinalNoToolsOnMaxTurns {
		t.Fatalf("options = %+v, want bounded plan-only turn", opts)
	}
	defs := opts.Tools.Defs()
	if len(defs) != 1 || defs[0].Function.Name != PlanToolName {
		t.Fatalf("plan-only defs = %+v, want only plan", defs)
	}
	if _, ok := opts.Tools.Get("shell"); ok {
		t.Fatal("plan-only options must not keep non-plan tools")
	}
}

func TestPlanOnlyTurnOptionsRequiresPlanTool(t *testing.T) {
	for _, reg := range []*Registry{nil, NewRegistry()} {
		_, err := PlanOnlyTurnOptions(reg, 2)
		if err == nil || !strings.Contains(err.Error(), "plan tool is not available") {
			t.Fatalf("PlanOnlyTurnOptions error = %v, want missing plan tool", err)
		}
	}
}

func TestPlanOnlyTurnOptionsRequiresPositiveBudget(t *testing.T) {
	reg := NewRegistry()
	reg.Add(&Tool{Name: PlanToolName})
	for _, maxToolCalls := range []int{0, -1} {
		_, err := PlanOnlyTurnOptions(reg, maxToolCalls)
		if err == nil || !strings.Contains(err.Error(), "must be positive") {
			t.Fatalf("PlanOnlyTurnOptions(%d) error = %v, want positive budget error", maxToolCalls, err)
		}
	}
}

func TestPlanExecuteToolCallPolicyRejectsSetAndClear(t *testing.T) {
	policy := PlanExecuteToolCallPolicy()
	for _, action := range []string{"set", "clear"} {
		t.Run(action, func(t *testing.T) {
			got, reject := policy.Reject(ToolCallPolicyContext{
				ToolName: PlanToolName,
				Args:     json.RawMessage(fmt.Sprintf(`{"action":%q}`, action)),
			})
			if !reject {
				t.Fatalf("action %s should be rejected", action)
			}
			for _, want := range []string{"execute_plan", "do not replace or clear", "action=update"} {
				if !strings.Contains(got, want) {
					t.Fatalf("rejection missing %q: %q", want, got)
				}
			}
		})
	}
}

func TestPlanExecuteToolCallPolicyAllowsViewAndUpdate(t *testing.T) {
	policy := PlanExecuteToolCallPolicy()
	for _, action := range []string{"view", "update"} {
		t.Run(action, func(t *testing.T) {
			if got, reject := policy.Reject(ToolCallPolicyContext{
				ToolName: PlanToolName,
				Args:     json.RawMessage(fmt.Sprintf(`{"action":%q}`, action)),
			}); reject {
				t.Fatalf("action %s should pass, got %q", action, got)
			}
		})
	}
}

func TestPlanExecuteToolCallPolicyRejectsWrongStepUpdate(t *testing.T) {
	policy := PlanExecuteToolCallPolicyForStep(2)
	got, reject := policy.Reject(ToolCallPolicyContext{
		ToolName: PlanToolName,
		Args:     json.RawMessage(`{"action":"update","index":3,"status":"completed"}`),
	})
	if !reject {
		t.Fatal("wrong step update should be rejected")
	}
	for _, want := range []string{"execute_plan", "current active step 2", "index=2"} {
		if !strings.Contains(got, want) {
			t.Fatalf("wrong-step rejection missing %q: %q", want, got)
		}
	}
	if got, reject := policy.Reject(ToolCallPolicyContext{
		ToolName: PlanToolName,
		Args:     json.RawMessage(`{"action":"update","index":2,"status":"completed"}`),
	}); reject {
		t.Fatalf("current step update should pass, got %q", got)
	}
}

func TestExecutePlanTurnOptionsInstallsPlanPolicy(t *testing.T) {
	opts := ExecutePlanTurnOptions()
	if len(opts.ToolCallPolicies) != 1 || opts.ToolCallPolicies[0].ToolName != PlanToolName {
		t.Fatalf("ExecutePlanTurnOptions policies = %+v, want one plan policy", opts.ToolCallPolicies)
	}
}

func TestExecutePlanTurnOptionsForStepInstallsStepPolicy(t *testing.T) {
	opts := ExecutePlanTurnOptionsForStep(4)
	if len(opts.ToolCallPolicies) != 1 || opts.ToolCallPolicies[0].ToolName != PlanToolName {
		t.Fatalf("ExecutePlanTurnOptionsForStep policies = %+v, want one plan policy", opts.ToolCallPolicies)
	}
	if got, reject := opts.ToolCallPolicies[0].Reject(ToolCallPolicyContext{
		ToolName: PlanToolName,
		Args:     json.RawMessage(`{"action":"update","index":1,"status":"completed"}`),
	}); !reject || !strings.Contains(got, "current active step 4") {
		t.Fatalf("step policy reject=%v got=%q, want current active step 4", reject, got)
	}
}

func TestPlanOnlyUserPromptPreservesRequestAndForbidsExecution(t *testing.T) {
	got := PlanOnlyUserPrompt("  fix the failing tests  ")
	for _, want := range []string{
		"Plan-only mode is enabled.",
		"Do not execute the task yet.",
		"plan tool",
		"affentctl run --execute-plan",
		"same session",
		"Do not call shell",
		"fix the failing tests",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("plan-only prompt missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "  fix the failing tests  ") {
		t.Fatalf("plan-only prompt should trim request:\n%s", got)
	}
}

func TestWithActivePlanSkillProviderInjectsPersistedPlan(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plan.json")
	tool := planTool(path)
	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"set","steps":[{"text":"inspect resume state","status":"completed","evidence":["cmd/affentctl/cmd_chat.go"]},{"text":"continue implementation","status":"in_progress","note":"resume here"}]}`)); err != nil {
		t.Fatalf("set plan: %v", err)
	}
	provider := WithActivePlanSkillProvider(path, func(userText string) string {
		if userText != "continue" {
			t.Fatalf("userText = %q, want continue", userText)
		}
		return "AFFENT ACTIVE SKILL: demo\nUse demo workflow."
	})

	got := provider("continue")
	for _, want := range []string{
		"AFFENT ACTIVE PLAN:",
		"Completed steps: 1 (details omitted from active context).",
		"Current step: 2. Execute this step before broadening",
		"2. [in_progress] continue implementation note: resume here",
		"AFFENT ACTIVE SKILL: demo",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("active plan provider missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "inspect resume state") || strings.Contains(got, "cmd/affentctl/cmd_chat.go") {
		t.Fatalf("completed step details should be omitted from active context:\n%s", got)
	}
}

func TestActivePlanCurrentStepPrefersInProgressThenPending(t *testing.T) {
	steps := []planStep{
		{Text: "done", Status: "completed"},
		{Text: "queued", Status: "pending"},
		{Text: "active", Status: "in_progress"},
	}
	if got, want := activePlanCurrentStepIndex(steps), 3; got != want {
		t.Fatalf("current step = %d, want %d", got, want)
	}

	steps[2].Status = "completed"
	if got, want := activePlanCurrentStepIndex(steps), 2; got != want {
		t.Fatalf("current step without in_progress = %d, want %d", got, want)
	}
}

func TestWithActivePlanSkillProviderSkipsMissingOrInvalidPlan(t *testing.T) {
	provider := WithActivePlanSkillProvider(filepath.Join(t.TempDir(), "missing.json"), func(string) string {
		return "AFFENT ACTIVE SKILL: demo"
	})
	if got := provider("anything"); got != "AFFENT ACTIVE SKILL: demo" {
		t.Fatalf("missing plan provider = %q", got)
	}

	badPath := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(badPath, []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	provider = WithActivePlanSkillProvider(badPath, nil)
	if got := provider("anything"); got != "" {
		t.Fatalf("invalid plan should be skipped, got %q", got)
	}
}

func TestWithActivePlanSkillProviderSkipsCompletedPlan(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plan.json")
	tool := planTool(path)
	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"set","steps":[{"text":"inspect","status":"completed"},{"text":"ship","status":"completed"}]}`)); err != nil {
		t.Fatalf("set plan: %v", err)
	}
	provider := WithActivePlanSkillProvider(path, func(string) string {
		return "AFFENT ACTIVE SKILL: demo"
	})
	got := provider("new task")
	if strings.Contains(got, "AFFENT ACTIVE PLAN") || strings.Contains(got, "inspect") {
		t.Fatalf("completed plan should not be injected, got %q", got)
	}
	if got != "AFFENT ACTIVE SKILL: demo" {
		t.Fatalf("next provider should still run, got %q", got)
	}
}

func TestWithActivePlanSkillProviderCompactsInlineDetails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plan.json")
	longText := strings.Repeat("t", maxActivePlanStepTextBytes+20)
	longNote := strings.Repeat("n", maxActivePlanNoteBytes+20)
	longEvidence := strings.Repeat("e", maxActivePlanEvidenceRefBytes+20)
	raw := fmt.Sprintf(`{"version":1,"steps":[{"text":%q,"status":"in_progress","evidence":[%q,"ref2","ref3","ref4"],"note":%q}]}`+"\n", longText, longEvidence, longNote)
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}

	got := WithActivePlanSkillProvider(path, nil)("continue")
	if !strings.Contains(got, "1. [in_progress] "+strings.Repeat("t", maxActivePlanStepTextBytes)+"...") {
		t.Fatalf("step text should be compacted, got:\n%s", got)
	}
	if !strings.Contains(got, strings.Repeat("e", maxActivePlanEvidenceRefBytes)+"...") {
		t.Fatalf("evidence ref should be compacted, got:\n%s", got)
	}
	if !strings.Contains(got, "(+1 more)") {
		t.Fatalf("evidence summary should report omitted refs, got:\n%s", got)
	}
	if strings.Contains(got, "ref4") {
		t.Fatalf("evidence beyond cap should be omitted, got:\n%s", got)
	}
	if !strings.Contains(got, "note: "+strings.Repeat("n", maxActivePlanNoteBytes)+"...") {
		t.Fatalf("note should be compacted, got:\n%s", got)
	}
}
