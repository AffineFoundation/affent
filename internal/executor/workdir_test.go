package executor

import "testing"

func TestResolveWorkingDirDefaultsToWorkspace(t *testing.T) {
	got, err := resolveWorkingDir("/tmp/ws", "")
	if err != nil {
		t.Fatalf("resolveWorkingDir: %v", err)
	}
	if got != "/tmp/ws" {
		t.Fatalf("working dir = %q, want default workspace", got)
	}
}

func TestResolveWorkingDirJoinsRelativeToWorkspace(t *testing.T) {
	got, err := resolveWorkingDir("/tmp/ws", "src/app")
	if err != nil {
		t.Fatalf("resolveWorkingDir: %v", err)
	}
	if got != "/tmp/ws/src/app" {
		t.Fatalf("working dir = %q, want workspace-relative path", got)
	}
}

func TestResolveWorkingDirRejectsRelativeEscape(t *testing.T) {
	if got, err := resolveWorkingDir("/tmp/ws", "../other"); err == nil {
		t.Fatalf("expected relative escape rejection, got %q", got)
	}
}
