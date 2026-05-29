package taskstate

import "testing"

func TestCloneSnapshotPtrDeepCopiesSlices(t *testing.T) {
	original := Snapshot{
		Objective:        "Fix and verify",
		Constraints:      []string{"workspace-relative paths"},
		KnownFacts:       []string{"tests are available"},
		ChangedFiles:     []File{{Path: "main.go", Action: "edit"}},
		AttemptedActions: []Action{{Tool: "shell", Summary: "go test ./..."}},
		FailedActions: []Failure{{
			Tool:  "shell",
			Kinds: []string{"test_failed"},
			Next:  "inspect failure",
		}},
		Evidence:      []Evidence{{Source: "shell", Summary: "go test ./..."}},
		OpenQuestions: []string{"which branch?"},
		Sources:       []string{"runtime_workspace"},
	}

	clone := CloneSnapshotPtr(original)
	if clone == nil {
		t.Fatal("CloneSnapshotPtr returned nil")
	}

	original.Constraints[0] = "changed"
	original.KnownFacts[0] = "changed"
	original.ChangedFiles[0].Path = "changed.go"
	original.AttemptedActions[0].Summary = "changed"
	original.FailedActions[0].Kinds[0] = "changed"
	original.Evidence[0].Summary = "changed"
	original.OpenQuestions[0] = "changed"
	original.Sources[0] = "changed"

	if clone.Constraints[0] != "workspace-relative paths" ||
		clone.KnownFacts[0] != "tests are available" ||
		clone.ChangedFiles[0].Path != "main.go" ||
		clone.AttemptedActions[0].Summary != "go test ./..." ||
		clone.FailedActions[0].Kinds[0] != "test_failed" ||
		clone.Evidence[0].Summary != "go test ./..." ||
		clone.OpenQuestions[0] != "which branch?" ||
		clone.Sources[0] != "runtime_workspace" {
		t.Fatalf("clone was mutated through original: %+v", clone)
	}
}

func TestIsEmptyTreatsUnknownStateAsEmpty(t *testing.T) {
	if !IsEmpty(Snapshot{Status: "unknown", VerificationState: "unknown"}) {
		t.Fatal("unknown-only snapshot should be empty")
	}
	if IsEmpty(Snapshot{KnownFacts: []string{"workspace tools available"}}) {
		t.Fatal("snapshot with facts should not be empty")
	}
}
