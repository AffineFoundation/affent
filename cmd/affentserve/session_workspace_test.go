package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	agent "github.com/affinefoundation/affent/internal/agent"
	"github.com/affinefoundation/affent/internal/sessionstate"
	"github.com/affinefoundation/affent/internal/sse"
	"github.com/rs/zerolog"
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

func TestSessionWorkspaceSupportsCloneModifyTestCommitPushWorkflow(t *testing.T) {
	var calls atomic.Int32
	fixedSource := `package mathutil

func Clamp(v, min, max int) int {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}
`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		switch calls.Add(1) {
		case 1:
			streamToolCall(t, w, "clone_repo", "shell", `{"command":"git clone remote.git app","timeout_sec":20}`)
		case 2:
			streamToolCall(t, w, "workspace_app", agent.SessionWorkspaceToolName, `{"action":"set","path":"app"}`)
		case 3:
			streamToolCall(t, w, "test_fail", "shell", `{"command":"go test ./...","timeout_sec":20}`)
		case 4:
			args := fmt.Sprintf(`{"path":"mathutil/clamp.go","content":%s}`, jsonStringLiteral(fixedSource))
			streamToolCall(t, w, "fix_clamp", "write_file", args)
		case 5:
			streamToolCall(t, w, "test_pass", "shell", `{"command":"go test ./...","timeout_sec":20}`)
		case 6:
			streamToolCall(t, w, "commit_push", "shell", `{"command":"git config user.email affent@example.test && git config user.name Affent && git add mathutil/clamp.go && git commit -m \"fix clamp\" && git push origin HEAD:main && git status --short","timeout_sec":20}`)
		case 7:
			fmt.Fprint(w, `data: {"choices":[{"delta":{"role":"assistant","content":"Fixed Clamp, verified tests, committed, and pushed to main."},"finish_reason":"stop"}]}`+"\n\n")
		default:
			t.Errorf("unexpected LLM call %d", calls.Load())
			fmt.Fprint(w, `data: {"choices":[{"delta":{"role":"assistant","content":"unexpected"},"finish_reason":"stop"}]}`+"\n\n")
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(srv.Close)

	cfg := Config{
		Listen:         "127.0.0.1:0",
		MaxSessions:    4,
		SessionIdleTTL: "5m",
		WorkspaceRoot:  t.TempDir(),
		MemoryRoot:     t.TempDir(),
		BaseURL:        srv.URL,
		APIKey:         "test",
		Model:          "fake",
		EnableBuiltins: true,
		MaxTurnSteps:   10,
	}
	pool, err := NewSessionPool(cfg, zerolog.New(io.Discard))
	if err != nil {
		t.Fatalf("NewSessionPool: %v", err)
	}
	t.Cleanup(pool.Shutdown)
	sess, err := pool.GetOrCreate("repo-workflow")
	if err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	remote := seedClampRemote(t, sess.workspaceRoot())

	subID, ch := sess.Subscribe(64)
	defer sess.Unsubscribe(subID)
	turnID, err := sess.SendUser(context.Background(), "Clone remote.git into app, fix Clamp, run tests, commit, and push.")
	if err != nil {
		t.Fatalf("SendUser: %v", err)
	}

	deadline := time.After(15 * time.Second)
	workspaceSet := false
	sawFailedTest := false
	sawPassedTest := false
	sawCommitPush := false
	for {
		select {
		case ev := <-ch:
			switch ev.Type {
			case sse.TypeToolRequest:
				var p sse.ToolRequestPayload
				if err := json.Unmarshal(ev.Data, &p); err != nil {
					t.Fatalf("decode tool.request: %v", err)
				}
				if p.Tool == agent.SessionWorkspaceToolName && p.CallID == "workspace_app" {
					workspaceSet = true
				}
				if workspaceSet && (p.Tool == "shell" || p.Tool == "write_file") {
					if command, _ := p.Args["command"].(string); strings.Contains(command, "app/") {
						t.Fatalf("tool %s after workspace switch still used project prefix: %q", p.CallID, command)
					}
					if path, _ := p.Args["path"].(string); strings.HasPrefix(path, "app/") {
						t.Fatalf("tool %s after workspace switch still used project prefix: %q", p.CallID, path)
					}
				}
			case sse.TypeToolResult:
				var p sse.ToolResultPayload
				if err := json.Unmarshal(ev.Data, &p); err != nil {
					t.Fatalf("decode tool.result: %v", err)
				}
				switch p.CallID {
				case "test_fail":
					if !strings.Contains(p.Result, "[exit 1]") || !strings.Contains(p.Result, "FAIL") {
						t.Fatalf("initial go test should fail before the fix: %s", p.ResultSummary)
					}
					sawFailedTest = true
				case "test_pass":
					if !strings.Contains(p.Result, "[exit 0]") {
						t.Fatalf("go test after fix failed: %s", p.ResultSummary)
					}
					sawPassedTest = true
				case "commit_push":
					if !strings.Contains(p.Result, "[exit 0]") {
						t.Fatalf("commit/push failed: %s", p.ResultSummary)
					}
					sawCommitPush = true
				}
			case sse.TypeTurnEnd:
				var p sse.TurnEndPayload
				if err := json.Unmarshal(ev.Data, &p); err != nil {
					t.Fatalf("decode turn.end: %v", err)
				}
				if p.TurnID != turnID {
					continue
				}
				if p.Reason != sse.TurnEndCompleted {
					t.Fatalf("turn end reason = %q, want completed", p.Reason)
				}
				if !workspaceSet || !sawFailedTest || !sawPassedTest || !sawCommitPush {
					t.Fatalf("workflow evidence missing: workspace=%v failed_test=%v passed_test=%v commit_push=%v", workspaceSet, sawFailedTest, sawPassedTest, sawCommitPush)
				}
				assertClampRemotePushed(t, remote)
				if sess.Workspace() != filepath.Join(sess.workspaceRoot(), "app") {
					t.Fatalf("active workspace = %q, want app under root %q", sess.Workspace(), sess.workspaceRoot())
				}
				return
			}
		case <-deadline:
			t.Fatal("timeout waiting for repo workflow turn.end")
		}
	}
}

func streamToolCall(t *testing.T, w io.Writer, id, name, args string) {
	t.Helper()
	fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"role\":\"assistant\",\"tool_calls\":[{\"index\":0,\"id\":%s,\"type\":\"function\",\"function\":{\"name\":%s,\"arguments\":%s}}]},\"finish_reason\":\"tool_calls\"}]}\n\n",
		jsonStringLiteral(id),
		jsonStringLiteral(name),
		jsonStringLiteral(args),
	)
}

func seedClampRemote(t *testing.T, workspace string) string {
	t.Helper()
	remote := filepath.Join(workspace, "remote.git")
	seed := filepath.Join(t.TempDir(), "seed")
	mustRun(t, "", "git", "init", "-b", "main", seed)
	if err := os.MkdirAll(filepath.Join(seed, "mathutil"), 0o755); err != nil {
		t.Fatalf("mkdir seed mathutil: %v", err)
	}
	files := map[string]string{
		"go.mod": "module example.com/affent-workflow\n\ngo 1.22\n",
		"mathutil/clamp.go": `package mathutil

func Clamp(v, min, max int) int {
	return v
}
`,
		"mathutil/clamp_test.go": `package mathutil

import "testing"

func TestClamp(t *testing.T) {
	if got := Clamp(-2, 0, 10); got != 0 {
		t.Fatalf("Clamp below min = %d, want 0", got)
	}
	if got := Clamp(12, 0, 10); got != 10 {
		t.Fatalf("Clamp above max = %d, want 10", got)
	}
	if got := Clamp(5, 0, 10); got != 5 {
		t.Fatalf("Clamp in range = %d, want 5", got)
	}
}
`,
	}
	for rel, body := range files {
		path := filepath.Join(seed, filepath.FromSlash(rel))
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	mustRun(t, seed, "git", "config", "user.email", "seed@example.test")
	mustRun(t, seed, "git", "config", "user.name", "Seed")
	mustRun(t, seed, "git", "add", ".")
	mustRun(t, seed, "git", "commit", "-m", "seed clamp")
	mustRun(t, "", "git", "init", "--bare", "-b", "main", remote)
	mustRun(t, seed, "git", "remote", "add", "origin", remote)
	mustRun(t, seed, "git", "push", "origin", "HEAD:main")
	return remote
}

func assertClampRemotePushed(t *testing.T, remote string) {
	t.Helper()
	verify := filepath.Join(t.TempDir(), "verify")
	mustRun(t, "", "git", "clone", remote, verify)
	log := mustRun(t, verify, "git", "log", "--oneline", "-1")
	if !strings.Contains(log, "fix clamp") {
		t.Fatalf("remote latest commit = %q, want fix clamp", log)
	}
	source, err := os.ReadFile(filepath.Join(verify, "mathutil", "clamp.go"))
	if err != nil {
		t.Fatalf("read pushed clamp.go: %v", err)
	}
	if !strings.Contains(string(source), "if v < min") || !strings.Contains(string(source), "if v > max") {
		t.Fatalf("remote clamp.go was not fixed:\n%s", string(source))
	}
	mustRun(t, verify, "go", "test", "./...")
}

func mustRun(t *testing.T, dir, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s failed: %v\n%s", name, strings.Join(args, " "), err, string(out))
	}
	return string(out)
}
