package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSessionWorkspaceToolSwitchesActiveWorkspace(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "app")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}
	var persisted string
	state := NewActiveWorkspaceState("sess-workspace", root, root, false, func(current string) error {
		persisted = current
		return nil
	})
	tool := SessionWorkspaceTool(state)

	out, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"set","path":"app"}`))
	if err != nil {
		t.Fatalf("set workspace: %v", err)
	}
	if state.Current() != project || persisted != project {
		t.Fatalf("workspace current=%q persisted=%q want %q", state.Current(), persisted, project)
	}
	if !strings.Contains(out, `"workspace_label": "app"`) || !strings.Contains(out, `"changed": true`) {
		t.Fatalf("tool output missing workspace details:\n%s", out)
	}

	out, err = tool.Execute(context.Background(), json.RawMessage(`{"action":"reset"}`))
	if err != nil {
		t.Fatalf("reset workspace: %v", err)
	}
	if state.Current() != root || persisted != root {
		t.Fatalf("workspace current=%q persisted=%q want root %q", state.Current(), persisted, root)
	}
	if !strings.Contains(out, `"workspace_root": "`+root+`"`) {
		t.Fatalf("reset output missing root:\n%s", out)
	}
}

func TestResolveActiveWorkspacePathRejectsEscapesFilesAndSymlinks(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if err := os.Symlink(root, filepath.Join(root, "link")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	for _, path := range []string{"../outside", "file.txt", "link"} {
		if resolved, err := ResolveActiveWorkspacePath(root, path); err == nil {
			t.Fatalf("ResolveActiveWorkspacePath(%q) = %q, want error", path, resolved)
		}
	}
}
