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
	if strings.Contains(filepath.ToSlash(out), filepath.ToSlash(root)) ||
		!strings.Contains(out, `"workspace_root": "."`) ||
		!strings.Contains(out, `"workspace_path": "app"`) ||
		!strings.Contains(out, `"path_mode": "workspace_relative"`) {
		t.Fatalf("tool output should expose only workspace-relative paths:\n%s", out)
	}

	out, err = tool.Execute(context.Background(), json.RawMessage(`{"action":"reset"}`))
	if err != nil {
		t.Fatalf("reset workspace: %v", err)
	}
	if state.Current() != root || persisted != root {
		t.Fatalf("workspace current=%q persisted=%q want root %q", state.Current(), persisted, root)
	}
	if strings.Contains(filepath.ToSlash(out), filepath.ToSlash(root)) ||
		!strings.Contains(out, `"workspace_root": "."`) ||
		!strings.Contains(out, `"workspace_path": "."`) {
		t.Fatalf("reset output should expose workspace-relative root:\n%s", out)
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

func TestSessionWorkspaceToolErrorsAreStructured(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	state := NewActiveWorkspaceState("sess-workspace", root, root, false, nil)
	tool := SessionWorkspaceTool(state)

	cases := []struct {
		name string
		args string
		want string
		next string
	}{
		{
			name: "decode",
			args: `{"action":`,
			want: "Failure: kind=invalid_args",
			next: "retry session_workspace",
		},
		{
			name: "missing action",
			args: `{}`,
			want: "Failure: kind=invalid_args",
			next: "action=inspect",
		},
		{
			name: "missing set path",
			args: `{"action":"set"}`,
			want: "Failure: kind=workspace_path_required",
			next: "workspace-relative project directory",
		},
		{
			name: "escape",
			args: `{"action":"set","path":"../outside"}`,
			want: "Failure: kind=workspace_path_escape",
			next: "under the configured workspace root",
		},
		{
			name: "missing target",
			args: `{"action":"set","path":"missing"}`,
			want: "Failure: kind=workspace_path_not_found",
			next: "create or clone the project directory first",
		},
		{
			name: "file target",
			args: `{"action":"set","path":"file.txt"}`,
			want: "Failure: kind=workspace_path_not_directory",
			next: "not a file",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := tool.Execute(context.Background(), json.RawMessage(tc.args))
			if err == nil {
				t.Fatalf("Execute succeeded unexpectedly: %s", out)
			}
			if !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "Next:") || !strings.Contains(err.Error(), tc.next) {
				t.Fatalf("error = %q, want %q and next %q", err.Error(), tc.want, tc.next)
			}
		})
	}
}

func TestSessionWorkspaceToolPersisterFailureIsStructured(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "app")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}
	state := NewActiveWorkspaceState("sess-workspace", root, root, false, func(string) error {
		return os.ErrPermission
	})
	tool := SessionWorkspaceTool(state)
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"set","path":"app"}`))
	if err == nil {
		t.Fatalf("Execute succeeded unexpectedly: %s", out)
	}
	for _, want := range []string{"Failure: kind=workspace_update_failed", "Next:", "state store is writable"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %q, want %q", err.Error(), want)
		}
	}
}
