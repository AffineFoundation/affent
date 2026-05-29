package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	agent "github.com/affinefoundation/affent/internal/agent"
	"github.com/affinefoundation/affent/internal/sessionstate"
)

func TestSessionWorkspaceToolSwitchesActiveWorkspaceForTools(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	pool.cfg.EnableBuiltins = true
	sess, err := pool.GetOrCreate("workspace-switch")
	if err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	project := filepath.Join(sess.workspaceRoot(), "project")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}
	if err := os.WriteFile(filepath.Join(project, "marker.txt"), []byte("active-workspace-ok\n"), 0o644); err != nil {
		t.Fatalf("write marker: %v", err)
	}

	workspaceTool, ok := sess.registry.Get(agent.SessionWorkspaceToolName)
	if !ok {
		t.Fatal("session_workspace tool missing")
	}
	out, err := workspaceTool.Execute(context.Background(), json.RawMessage(`{"action":"set","path":"project"}`))
	if err != nil {
		t.Fatalf("session_workspace set: %v", err)
	}
	if !strings.Contains(out, `"changed": true`) || sess.Workspace() != project {
		t.Fatalf("workspace set output=%s current=%q want %q", out, sess.Workspace(), project)
	}

	shell, ok := sess.registry.Get("shell")
	if !ok {
		t.Fatal("shell tool missing")
	}
	result, err := shell.Execute(context.Background(), json.RawMessage(`{"command":"pwd; cat marker.txt","timeout_sec":5}`))
	if err != nil {
		t.Fatalf("shell after workspace switch: %v\n%s", err, result)
	}
	if !strings.Contains(result, "active-workspace-ok") {
		t.Fatalf("shell did not run from switched workspace: %s", result)
	}
	if strings.Contains(filepath.ToSlash(result), filepath.ToSlash(project)) {
		t.Fatalf("shell result should be relativized to active workspace, got %q", result)
	}

	readFile, ok := sess.registry.Get("read_file")
	if !ok {
		t.Fatal("read_file tool missing")
	}
	result, err = readFile.Execute(context.Background(), json.RawMessage(`{"path":"marker.txt"}`))
	if err != nil {
		t.Fatalf("read_file after workspace switch: %v", err)
	}
	if !strings.Contains(result, "active-workspace-ok") {
		t.Fatalf("read_file did not use switched workspace: %s", result)
	}

	meta, found, err := sessionstate.ReadMetadata(pool.sessionDirPath("workspace-switch"))
	if err != nil || !found {
		t.Fatalf("ReadMetadata found=%v err=%v", found, err)
	}
	if meta.WorkspaceRoot != sess.workspaceRoot() || meta.WorkspacePath != project {
		t.Fatalf("metadata = %+v, want root %q path %q", meta, sess.workspaceRoot(), project)
	}
}

func TestSessionWorkspaceToolRejectsEscapesAndFiles(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	pool.cfg.EnableBuiltins = true
	sess, err := pool.GetOrCreate("workspace-guard")
	if err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sess.workspaceRoot(), "file.txt"), []byte("not a dir"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	workspaceTool, ok := sess.registry.Get(agent.SessionWorkspaceToolName)
	if !ok {
		t.Fatal("session_workspace tool missing")
	}
	for _, args := range []string{
		`{"action":"set","path":"../outside"}`,
		`{"action":"set","path":"file.txt"}`,
	} {
		if out, err := workspaceTool.Execute(context.Background(), json.RawMessage(args)); err == nil {
			t.Fatalf("session_workspace %s succeeded unexpectedly: %s", args, out)
		}
	}
	if sess.Workspace() != sess.workspaceRoot() {
		t.Fatalf("failed set should not change workspace: current=%q root=%q", sess.Workspace(), sess.workspaceRoot())
	}
}
