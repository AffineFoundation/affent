package agent

import (
	"context"
	"encoding/json"
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
