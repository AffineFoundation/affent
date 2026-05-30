package agenteval

import (
	"reflect"
	"strings"
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
		RuntimeSurfaces: []sse.RuntimeSurfacePayload{{
			TurnID:                    "turn-1",
			ToolCount:                 3,
			MaxTurnInputTokens:        300000,
			ModelContextWindowTokens:  100000,
			ReservedOutputTokens:      30000,
			CompactTriggerInputTokens: 70000,
			Workspace: &sse.RuntimeWorkspace{
				PathMode:       "workspace_relative",
				WorkspacePath:  "app",
				WorkspaceLabel: "app",
			},
		}},
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
				Result:        "FAIL ./app/mathutil\nNext: inspect clamp edge cases then rerun go test\nFailure: kind=test_failed",
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
	if !taskStateHasAttemptedAction(got, "edit_file", "app/mathutil/clamp.go") || !taskStateHasAttemptedAction(got, "shell", "go test ./...") {
		t.Fatalf("attempted actions = %+v, want edit and shell actions", got.AttemptedActions)
	}
	if len(got.FailedActions) != 1 ||
		got.FailedActions[0].Tool != "shell" ||
		got.FailedActions[0].Summary != "FAIL ./app/mathutil" ||
		got.FailedActions[0].Next != "inspect clamp edge cases then rerun go test" ||
		!reflect.DeepEqual(got.FailedActions[0].Kinds, []string{"test_failed"}) {
		t.Fatalf("failed actions = %+v", got.FailedActions)
	}
	if !taskStateHasEvidence(got, "runtime_workspace") || !taskStateHasEvidence(got, "shell") {
		t.Fatalf("evidence = %+v", got.Evidence)
	}
	if !taskStateHasEvidenceSummary(got, "runtime_surface", "reserved_output_tokens=30000") ||
		!taskStateHasEvidenceSummary(got, "runtime_surface", "compact_trigger_input_tokens=70000") {
		t.Fatalf("evidence = %+v, want runtime surface policy limits", got.Evidence)
	}
	if !taskStateHasEvidenceSummary(got, "runtime_workspace", "workspace_path=app") {
		t.Fatalf("evidence = %+v, want runtime workspace path", got.Evidence)
	}
	if !taskStateHasSource(got, "runtime_workspace") || !taskStateHasSource(got, "runtime_surface") || !taskStateHasSource(got, "schedule") {
		t.Fatalf("sources = %+v", got.Sources)
	}
}

func TestDeriveTaskStateVerificationIgnoresNonVerificationFailureAfterPass(t *testing.T) {
	got := DeriveTaskState(Trace{
		TurnEndReason: "completed",
		Tools: []ToolCall{
			{
				Tool:     "shell",
				Args:     map[string]any{"command": "python3 -m unittest discover -s tests"},
				ExitCode: 0,
			},
			{
				Tool:         "shell",
				Args:         map[string]any{"command": "git status --short"},
				ExitCode:     1,
				FailureKinds: []string{"turn_input_budget_exhausted"},
				Result:       "Failure: kind=turn_input_budget_exhausted",
			},
		},
	})
	if got.VerificationState != "last_shell_passed" {
		t.Fatalf("verification state = %q, want last_shell_passed", got.VerificationState)
	}
	if len(got.FailedActions) != 1 || got.FailedActions[0].Tool != "shell" {
		t.Fatalf("failed actions = %+v, want retained non-verification failure", got.FailedActions)
	}
}

func TestDeriveTaskStateDefaultsBlankRequestModeToNormal(t *testing.T) {
	got := DeriveTaskState(Trace{
		Prompt:        "Run the scheduled check-in.",
		TurnEndReason: "completed",
		UserMessages: []UserMessage{{
			Text:         "Scheduled check-in for session: due-one",
			Source:       "schedule",
			ScheduleID:   "sched_due",
			ScheduleKind: "loop_tick",
		}},
	})
	if got.RequestMode != "normal" || got.RequestSource != "schedule" || got.ScheduleID != "sched_due" || got.ScheduleKind != "loop_tick" {
		t.Fatalf("request provenance = mode:%q source:%q schedule:%q kind:%q, want normalized scheduled request", got.RequestMode, got.RequestSource, got.ScheduleID, got.ScheduleKind)
	}
}

func TestDeriveTaskStateDefaultsScheduledKindToCustom(t *testing.T) {
	got := DeriveTaskState(Trace{
		Prompt:        "Run the scheduled check-in.",
		TurnEndReason: "completed",
		UserMessages: []UserMessage{{
			Text:       "Scheduled check-in for session: due-one",
			Source:     "schedule",
			ScheduleID: "sched_due",
		}},
	})
	if got.RequestSource != "schedule" || got.ScheduleID != "sched_due" || got.ScheduleKind != "custom" {
		t.Fatalf("request provenance = source:%q schedule:%q kind:%q, want scheduled custom", got.RequestSource, got.ScheduleID, got.ScheduleKind)
	}
}

func TestDeriveTaskStatePreservesDistinctActionAndEvidenceSourcesAtLimit(t *testing.T) {
	trace := Trace{
		Prompt:        "Switch workspace, fix, commit, push, and leave clean status.",
		TurnEndReason: "completed",
		ContextInjections: []ContextInjection{{
			TurnID: "turn-1",
			Source: "runtime_workspace",
		}},
		Tools: []ToolCall{
			{TurnID: "turn-1", CallID: "copy", Tool: "shell", Args: map[string]any{"command": "cp -r remote.git app"}, ExitCode: 0},
			{TurnID: "turn-1", CallID: "clone", Tool: "shell", Args: map[string]any{"command": "rm -rf app && git clone remote.git app"}, ExitCode: 0},
			{TurnID: "turn-1", CallID: "workspace", Tool: "session_workspace", Args: map[string]any{"action": "set", "path": "app"}},
			{TurnID: "turn-1", CallID: "test-1", Tool: "shell", Args: map[string]any{"command": "go test ./..."}, ExitCode: 0},
			{TurnID: "turn-1", CallID: "read-1", Tool: "read_file", Args: map[string]any{"path": "greet/greet_test.go"}},
			{TurnID: "turn-1", CallID: "edit", Tool: "edit_file", Args: map[string]any{"path": "greet/greet.go"}},
			{TurnID: "turn-1", CallID: "test-2", Tool: "shell", Args: map[string]any{"command": "go test ./..."}, ExitCode: 0},
			{TurnID: "turn-1", CallID: "commit", Tool: "shell", Args: map[string]any{"command": `git add greet/greet.go && git commit -m "fix"`}, ExitCode: 0},
			{TurnID: "turn-1", CallID: "push", Tool: "shell", Args: map[string]any{"command": "git push origin main"}, ExitCode: 0},
			{TurnID: "turn-1", CallID: "status", Tool: "shell", Args: map[string]any{"command": "git status --short"}, ExitCode: 0},
		},
	}

	got := DeriveTaskState(trace)
	if !taskStateHasAttemptedAction(got, "session_workspace", "app") {
		t.Fatalf("attempted actions = %+v, want session_workspace app preserved", got.AttemptedActions)
	}
	if !taskStateHasAttemptedAction(got, "shell", "git push") {
		t.Fatalf("attempted actions = %+v, want git push handoff preserved", got.AttemptedActions)
	}
	if !taskStateHasEvidenceSummary(got, "git_commit", "git commit") ||
		!taskStateHasEvidenceSummary(got, "git_push", "git push") {
		t.Fatalf("evidence = %+v, want git commit and push sources preserved", got.Evidence)
	}
}

func TestDeriveTaskStatePreservesDurableControlActionsAtLimit(t *testing.T) {
	trace := Trace{
		Prompt:        "Finish a long-running project loop.",
		TurnEndReason: "completed",
		Tools: []ToolCall{
			{TurnID: "turn-1", CallID: "test-1", Tool: "shell", Args: map[string]any{"command": "python3 -m unittest discover -s tests"}, ExitCode: 0},
			{TurnID: "turn-1", CallID: "test-2", Tool: "shell", Args: map[string]any{"command": "python3 -m unittest discover -s tests"}, ExitCode: 0},
			{TurnID: "turn-1", CallID: "test-3", Tool: "shell", Args: map[string]any{"command": "python3 -m unittest discover -s tests 2>&1"}, ExitCode: 0},
			{TurnID: "turn-1", CallID: "status-1", Tool: "shell", Args: map[string]any{"command": "git status"}, ExitCode: 0},
			{TurnID: "turn-1", CallID: "commit", Tool: "shell", Args: map[string]any{"command": `git commit -m "feat"`}, ExitCode: 0},
			{TurnID: "turn-1", CallID: "push", Tool: "shell", Args: map[string]any{"command": "git push origin main"}, ExitCode: 0},
			{TurnID: "turn-1", CallID: "status-2", Tool: "shell", Args: map[string]any{"command": "git status"}, ExitCode: 0},
			{TurnID: "turn-1", CallID: "plan", Tool: "plan", Args: map[string]any{"action": "update"}, ExitCode: 0},
			{TurnID: "turn-1", CallID: "loop-close", Tool: "loop_protocol", Args: map[string]any{"action": "close", "status": "completed"}, ExitCode: 0},
		},
	}

	got := DeriveTaskState(trace)
	if !taskStateHasAttemptedAction(got, "loop_protocol", "close") {
		t.Fatalf("attempted actions = %+v, want loop_protocol close preserved", got.AttemptedActions)
	}
	if !taskStateHasAttemptedAction(got, "shell", "git push") {
		t.Fatalf("attempted actions = %+v, want git push preserved", got.AttemptedActions)
	}
}

func TestDeriveTaskStateKeepsWriteAfterLaterEdit(t *testing.T) {
	got := DeriveTaskState(Trace{
		Tools: []ToolCall{
			{Tool: "write_file", Args: map[string]any{"path": "tests/test_store.py"}},
			{Tool: "edit_file", Args: map[string]any{"path": "tests/test_store.py"}},
		},
	})
	if !reflect.DeepEqual(got.ChangedFiles, []TaskStateFile{{Path: "tests/test_store.py", Action: "write"}}) {
		t.Fatalf("changed files = %+v, want write preserved after later edit", got.ChangedFiles)
	}
}

func TestDeriveTaskStateKeepsDurableObjectiveAcrossScheduledTurns(t *testing.T) {
	got := DeriveTaskState(Trace{
		Prompt:        "Build a release notes generator.",
		TurnEndReason: "completed",
		UserMessages: []UserMessage{
			{
				Text: "Build a release notes generator and keep iterating until tests pass.",
			},
			{
				Text:         "Scheduled loop tick for release notes generator",
				DisplayText:  "Loop tick: continue release notes generator",
				Source:       "schedule",
				ScheduleID:   "sched_release_notes",
				ScheduleKind: "loop_tick",
			},
		},
	})
	if got.Objective != "Build a release notes generator and keep iterating until tests pass." {
		t.Fatalf("objective = %q, want first durable task request", got.Objective)
	}
	if got.RequestSource != "schedule" || got.ScheduleKind != "loop_tick" || got.ScheduleID != "sched_release_notes" {
		t.Fatalf("request provenance = source:%q kind:%q id:%q, want latest scheduled tick", got.RequestSource, got.ScheduleKind, got.ScheduleID)
	}
}

func TestDeriveTaskStateDefaultsBlankRequestSourceToUser(t *testing.T) {
	got := DeriveTaskState(Trace{
		Prompt:        "Inspect the repo.",
		TurnEndReason: "completed",
		UserMessages: []UserMessage{{
			Text: "Inspect the repo.",
		}},
	})
	if got.RequestMode != "normal" || got.RequestSource != "user" {
		t.Fatalf("request provenance = mode:%q source:%q, want normal/user", got.RequestMode, got.RequestSource)
	}
	if !taskStateHasSource(got, "user") {
		t.Fatalf("sources = %+v, want user source", got.Sources)
	}
}

func TestDeriveTaskStateClassifiesGitHandoffEvidence(t *testing.T) {
	trace := Trace{
		Prompt:        "Fix the project, commit, and push.",
		TurnEndReason: "completed",
		Tools: []ToolCall{
			{
				TurnID:   "turn-1",
				CallID:   "commit-1",
				Tool:     "shell",
				Args:     map[string]any{"command": "git -C app commit -m fix"},
				ExitCode: 0,
			},
			{
				TurnID:   "turn-1",
				CallID:   "push-1",
				Tool:     "shell",
				Args:     map[string]any{"command": "git push origin main"},
				ExitCode: 0,
			},
		},
	}

	got := DeriveTaskState(trace)
	for _, want := range []string{"shell", "git_commit", "git_push"} {
		if !taskStateHasEvidence(got, want) {
			t.Fatalf("evidence = %+v, want %q", got.Evidence, want)
		}
		if !taskStateHasSource(got, want) {
			t.Fatalf("sources = %+v, want %q", got.Sources, want)
		}
	}
}

func TestDeriveTaskStateIncludesRuntimeOwnedToolEvidence(t *testing.T) {
	trace := Trace{
		Prompt:        "Schedule recurring BTC price checks.",
		TurnEndReason: "completed",
		Tools: []ToolCall{{
			TurnID:        "turn-1",
			CallID:        "schedule-1",
			Tool:          "session_schedule",
			Args:          map[string]any{"action": "create"},
			ExitCode:      0,
			ResultSummary: "created schedule sched_btc",
		}},
	}

	got := DeriveTaskState(trace)
	if !taskStateHasEvidence(got, "session_schedule") {
		t.Fatalf("evidence = %+v, want session_schedule", got.Evidence)
	}
	if !taskStateHasSource(got, "session_schedule") {
		t.Fatalf("sources = %+v, want session_schedule", got.Sources)
	}
}

func TestDeriveTaskStateIncludesContextCompactionEvidence(t *testing.T) {
	trace := Trace{
		Prompt:        "Continue the durable release loop.",
		TurnEndReason: "max_turns",
		ContextCompactions: []ContextCompaction{{
			TurnID:             "turn-compact",
			BeforeMessages:     80,
			AfterMessages:      16,
			RemovedMessages:    64,
			ReducedBytes:       70000,
			Reactive:           true,
			Reason:             "context_overflow",
			SummaryPresent:     true,
			LoopProtocolAnchor: "LOOP_PROTOCOL: id=release-loop status=running step=rerun tests",
		}},
	}

	got := DeriveTaskState(trace)
	if !taskStateHasEvidence(got, "context_compaction") {
		t.Fatalf("evidence = %+v, want context_compaction", got.Evidence)
	}
	if !taskStateHasSource(got, "context_compaction") {
		t.Fatalf("sources = %+v, want context_compaction", got.Sources)
	}
	evidence := taskStateEvidence(got, "context_compaction")
	for _, want := range []string{"context_overflow", "reactive=true", "removed_messages=64", "LOOP_PROTOCOL: id=release-loop"} {
		if !strings.Contains(evidence.Summary, want) {
			t.Fatalf("context compaction summary missing %q: %+v", want, evidence)
		}
	}
}

func TestDeriveTaskStateIncludesContextCompactionSkippedEvidence(t *testing.T) {
	trace := Trace{
		Prompt:        "Continue after request pressure.",
		TurnEndReason: "completed",
		ContextCompactionSkips: []ContextCompactionSkip{{
			TurnID:                    "turn-skip",
			Cause:                     "request_pressure_not_reduced",
			Reason:                    "estimated_context_pressure",
			BeforeMessages:            6,
			CandidateMessages:         5,
			BeforeBytes:               25396,
			CandidateBytes:            25535,
			EstimatedInputTokens:      6732,
			AfterEstimatedInputTokens: 6766,
			TriggerInputTokens:        1,
		}},
	}

	got := DeriveTaskState(trace)
	if !taskStateHasEvidence(got, "context_compaction_skipped") {
		t.Fatalf("evidence = %+v, want context_compaction_skipped", got.Evidence)
	}
	if !taskStateHasSource(got, "context_compaction_skipped") {
		t.Fatalf("sources = %+v, want context_compaction_skipped", got.Sources)
	}
	evidence := taskStateEvidence(got, "context_compaction_skipped")
	for _, want := range []string{"request_pressure_not_reduced", "estimated_context_pressure", "after_estimated_input_tokens=6766"} {
		if !strings.Contains(evidence.Summary, want) {
			t.Fatalf("context compaction skipped summary missing %q: %+v", want, evidence)
		}
	}
}

func TestDeriveTaskStateUsesCanonicalPrimaryFailureKind(t *testing.T) {
	trace := Trace{
		Prompt:        "Fetch current evidence.",
		TurnEndReason: "error",
		Tools: []ToolCall{{
			TurnID:        "turn-1",
			CallID:        "fetch-1",
			Tool:          "web_fetch",
			Args:          map[string]any{"url": "https://example.test/report"},
			FailureKind:   "empty_response",
			ResultSummary: "empty response from source",
		}},
	}

	got := DeriveTaskState(trace)
	if len(got.FailedActions) != 1 {
		t.Fatalf("failed actions = %+v, want one web_fetch failure", got.FailedActions)
	}
	failure := got.FailedActions[0]
	if failure.Tool != "web_fetch" || failure.Summary != "empty response from source" || !reflect.DeepEqual(failure.Kinds, []string{"empty_response"}) {
		t.Fatalf("failed action = %+v, want canonical primary failure kind", failure)
	}
}

func TestCloneTaskStateSnapshotPtrDeepCopiesSlices(t *testing.T) {
	original := TaskStateSnapshot{
		Objective:        "ship task",
		ChangedFiles:     []TaskStateFile{{Path: "main.go", Action: "edit"}},
		AttemptedActions: []TaskStateAction{{Tool: "shell", Summary: "go test ./..."}},
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
	original.AttemptedActions[0].Summary = "changed"
	original.FailedActions[0].Kinds[0] = "changed"
	original.Evidence[0].Source = "changed"
	original.Sources[0] = "changed"
	if clone.ChangedFiles[0].Path != "main.go" ||
		clone.AttemptedActions[0].Summary != "go test ./..." ||
		clone.FailedActions[0].Kinds[0] != "test_failed" ||
		clone.Evidence[0].Source != "shell" ||
		clone.Sources[0] != "runtime_surface" {
		t.Fatalf("clone shared mutable slices: %+v", clone)
	}
}

func taskStateHasAttemptedAction(task TaskStateSnapshot, tool, summaryPart string) bool {
	for _, action := range task.AttemptedActions {
		if action.Tool == tool && (summaryPart == "" || strings.Contains(action.Summary, summaryPart)) {
			return true
		}
	}
	return false
}

func taskStateHasEvidence(task TaskStateSnapshot, source string) bool {
	for _, evidence := range task.Evidence {
		if evidence.Source == source {
			return true
		}
	}
	return false
}

func taskStateHasEvidenceSummary(task TaskStateSnapshot, source, summaryPart string) bool {
	for _, evidence := range task.Evidence {
		if evidence.Source == source && strings.Contains(evidence.Summary, summaryPart) {
			return true
		}
	}
	return false
}

func taskStateEvidence(task TaskStateSnapshot, source string) TaskStateEvidence {
	for _, evidence := range task.Evidence {
		if evidence.Source == source {
			return evidence
		}
	}
	return TaskStateEvidence{}
}

func taskStateHasSource(task TaskStateSnapshot, source string) bool {
	for _, got := range task.Sources {
		if got == source {
			return true
		}
	}
	return false
}
