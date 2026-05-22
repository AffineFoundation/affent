package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/affinefoundation/affent/internal/executor"
	"github.com/rs/zerolog"
)

func TestSubagentRun_ReturnsStructuredReport(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"role\":\"assistant\",\"content\":\"Conclusion:\\nfound it\\nEvidence:\\n- README says so\"},\"finish_reason\":\"stop\"}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	t.Cleanup(srv.Close)

	reg := NewRegistry()
	RegisterSubagent(reg, SubagentDeps{
		LLM:              NewLLMClient(srv.URL, "", "fake"),
		HostWorkspaceDir: t.TempDir(),
		TranscriptDir:    t.TempDir(),
		Log:              zerolog.Nop(),
		PerCallTimeout:   5 * time.Second,
	})
	tool, ok := reg.Get("subagent_run")
	if !ok {
		t.Fatal("subagent_run was not registered")
	}

	out, err := tool.Execute(context.Background(), json.RawMessage(`{"mode":"explore","task":"inspect README"}`))
	if err != nil {
		t.Fatal(err)
	}
	var resp struct {
		Mode          string `json:"mode"`
		ChildID       string `json:"child_session_id"`
		Transcript    string `json:"transcript_path"`
		TurnEndReason string `json:"turn_end_reason"`
		Report        string `json:"report"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("decode response: %v\n%s", err, out)
	}
	if resp.Mode != "explore" || resp.TurnEndReason != "completed" {
		t.Fatalf("unexpected response metadata: %+v", resp)
	}
	if !strings.HasPrefix(resp.ChildID, "subagent_") || resp.Transcript == "" {
		t.Fatalf("missing child transcript metadata: %+v", resp)
	}
	if !strings.Contains(resp.Report, "Conclusion:") || !strings.Contains(resp.Report, "found it") {
		t.Fatalf("report missing model output: %+v", resp)
	}
}

func TestReadOnlyShellToolRejectsMutatingCommands(t *testing.T) {
	ws := t.TempDir()
	tool := readOnlyShellTool(BuiltinDeps{
		Executor:         nilExecutor{},
		HostWorkspaceDir: ws,
	})
	_, err := tool.Execute(context.Background(), json.RawMessage(`{"command":"rm important.txt"}`))
	if err == nil {
		t.Fatal("expected mutating shell command to be rejected")
	}
	if !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("unexpected error: %v", err)
	}

	_, err = tool.Execute(context.Background(), json.RawMessage(`{"command":"python -m pytest ./...", "cwd":"`+filepath.ToSlash(ws)+`"}`))
	if err != nil {
		t.Fatalf("read-only command should pass guard and reach executor: %v", err)
	}
}

func TestReadOnlyMemoryToolRejectsWrites(t *testing.T) {
	store := newTestStore(t)
	tool := readOnlyMemoryTool(store)
	_, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"add","content":"remember this"}`))
	if err == nil {
		t.Fatal("expected memory write to be rejected")
	}
	if !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("unexpected error: %v", err)
	}
}

type nilExecutor struct{}

func (nilExecutor) SessionID() string { return "test" }

func (nilExecutor) Exec(context.Context, []string, executor.ExecOptions) (executor.ExecResult, error) {
	return executor.ExecResult{ExitCode: 0, Stdout: "ok"}, nil
}
