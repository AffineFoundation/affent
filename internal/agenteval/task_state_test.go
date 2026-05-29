package agenteval

import (
	"reflect"
	"testing"

	"github.com/affinefoundation/affent/internal/sse"
)

func TestDeriveTaskStateKeepsProgressAuditableAfterRecoveredFailure(t *testing.T) {
	trace := Trace{
		Prompt:        "Improve the clamp helper and verify it.",
		TurnEndReason: "completed",
		UserMessages: []UserMessage{{
			DisplayText:  "Update clamp behavior, keep the public contract, and run tests.",
			Mode:         "execute_plan",
			Source:       "schedule",
			ScheduleID:   "sched_clamp",
			ScheduleKind: "checkin",
		}},
		RuntimeSurfaces: []sse.RuntimeSurfacePayload{{ToolCount: 3}},
		ContextInjections: []ContextInjection{{
			TurnID:  "turn-1",
			Source:  "runtime_workspace",
			Summary: "Workspace root is available; use relative workspace paths.",
		}},
		LoopProtocolFeeds: []LoopProtocolFeed{{
			PlanCurrentStep: "fix clamp edge cases",
		}},
		Tools: []ToolCall{
			{
				Tool: "edit_file",
				Args: map[string]any{"path": "app/mathutil/clamp.go"},
			},
			{
				TurnID:        "turn-1",
				CallID:        "test-1",
				Tool:          "shell",
				Args:          map[string]any{"command": "go test ./..."},
				ExitCode:      1,
				FailureKinds:  []string{"test_failed"},
				ResultSummary: "FAIL ./app/mathutil",
			},
			{
				TurnID:   "turn-1",
				CallID:   "test-2",
				Tool:     "shell",
				Args:     map[string]any{"command": "go test ./..."},
				ExitCode: 0,
			},
		},
	}

	got := DeriveTaskState(trace)
	if got.Objective != "Update clamp behavior, keep the public contract, and run tests." {
		t.Fatalf("objective = %q", got.Objective)
	}
	if got.Status != "completed" {
		t.Fatalf("status = %q", got.Status)
	}
	if got.CurrentStep != "fix clamp edge cases" {
		t.Fatalf("current step = %q", got.CurrentStep)
	}
	if got.RequestMode != "execute_plan" || got.RequestSource != "schedule" || got.ScheduleID != "sched_clamp" || got.ScheduleKind != "checkin" {
		t.Fatalf("request provenance = mode:%q source:%q schedule:%q kind:%q", got.RequestMode, got.RequestSource, got.ScheduleID, got.ScheduleKind)
	}
	if got.VerificationState != "last_shell_passed" {
		t.Fatalf("verification state = %q", got.VerificationState)
	}
	if got.NextStep != "" {
		t.Fatalf("next step = %q, want empty after recovered completed run", got.NextStep)
	}
	if !reflect.DeepEqual(got.ChangedFiles, []TaskStateFile{{Path: "app/mathutil/clamp.go", Action: "edit"}}) {
		t.Fatalf("changed files = %+v", got.ChangedFiles)
	}
	if len(got.FailedActions) != 1 ||
		got.FailedActions[0].Tool != "shell" ||
		got.FailedActions[0].Summary != "FAIL ./app/mathutil" ||
		!reflect.DeepEqual(got.FailedActions[0].Kinds, []string{"test_failed"}) {
		t.Fatalf("failed actions = %+v", got.FailedActions)
	}
	if !taskStateHasEvidence(got, "runtime_workspace") || !taskStateHasEvidence(got, "shell") {
		t.Fatalf("evidence = %+v", got.Evidence)
	}
	if !taskStateHasSource(got, "runtime_workspace") || !taskStateHasSource(got, "runtime_surface") || !taskStateHasSource(got, "schedule") {
		t.Fatalf("sources = %+v", got.Sources)
	}
}

func TestCloneTaskStateSnapshotPtrDeepCopiesSlices(t *testing.T) {
	original := TaskStateSnapshot{
		Objective:    "ship task",
		ChangedFiles: []TaskStateFile{{Path: "main.go", Action: "edit"}},
		FailedActions: []TaskStateFailure{{
			Tool:  "shell",
			Kinds: []string{"test_failed"},
		}},
		Evidence: []TaskStateEvidence{{Source: "shell", Summary: "go test ./..."}},
		Sources:  []string{"runtime_surface"},
	}
	clone := CloneTaskStateSnapshotPtr(original)
	if clone == nil {
		t.Fatal("clone is nil")
	}
	original.ChangedFiles[0].Path = "changed.go"
	original.FailedActions[0].Kinds[0] = "changed"
	original.Evidence[0].Source = "changed"
	original.Sources[0] = "changed"
	if clone.ChangedFiles[0].Path != "main.go" ||
		clone.FailedActions[0].Kinds[0] != "test_failed" ||
		clone.Evidence[0].Source != "shell" ||
		clone.Sources[0] != "runtime_surface" {
		t.Fatalf("clone shared mutable slices: %+v", clone)
	}
}

func taskStateHasEvidence(task TaskStateSnapshot, source string) bool {
	for _, evidence := range task.Evidence {
		if evidence.Source == source {
			return true
		}
	}
	return false
}

func taskStateHasSource(task TaskStateSnapshot, source string) bool {
	for _, got := range task.Sources {
		if got == source {
			return true
		}
	}
	return false
}
