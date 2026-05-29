package executor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

func TestLocalExecutorUsesWorkspaceDirProvider(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "first")
	second := filepath.Join(root, "second")
	if err := os.MkdirAll(first, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(second, 0o755); err != nil {
		t.Fatal(err)
	}
	current := second
	exec := NewLocalExecutor("dynamic", first)
	exec.WorkspaceDirProvider = func() string { return current }
	res, err := exec.Exec(context.Background(), []string{"pwd"}, ExecOptions{MaxOutputBytes: 1024})
	if err != nil {
		t.Fatalf("Exec: %v; stderr=%s", err, res.Stderr)
	}
	if strings.TrimSpace(res.Stdout) != second {
		t.Fatalf("pwd = %q, want provider workspace %q", strings.TrimSpace(res.Stdout), second)
	}
}
