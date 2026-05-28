package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHandleSessionCommandRunsShellAndPersistsTrace(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	pool.cfg.EnableBuiltins = true

	r := httptest.NewRequest(http.MethodPost, "/v1/sessions/workbench-command/commands", bytes.NewReader([]byte(`{"command":"pwd; printf workbench-command","timeout_sec":5}`)))
	w := httptest.NewRecorder()
	handleSessionCommand(pool, "workbench-command", w, r)

	if got := w.Result().StatusCode; got != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", got, w.Body.String())
	}
	var resp sessionCommandResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v body=%s", err, w.Body.String())
	}
	if resp.SessionID != "workbench-command" || resp.ExitCode != 0 || !strings.Contains(resp.Result, "workbench-command") {
		t.Fatalf("response = %+v", resp)
	}
	if resp.Workspace == "" {
		t.Fatalf("command response should include workspace metadata: %+v", resp)
	}
	if strings.Contains(filepath.ToSlash(resp.Result), filepath.ToSlash(resp.Workspace)) {
		t.Fatalf("command result should expose a workspace-relative view, not the absolute workspace; workspace=%q result=%q", resp.Workspace, resp.Result)
	}
	if !strings.Contains(resp.Result, ".") {
		t.Fatalf("command should default to the session workspace and render it as relative root; result=%q", resp.Result)
	}
	tracePath := filepath.Join(pool.sessionDirPath("workbench-command"), "events.jsonl")
	waitForFileSubstring(t, tracePath, `"mode":"manual_command"`)
	waitForFileSubstring(t, tracePath, `"tool":"shell"`)
	waitForFileSubstring(t, tracePath, `workbench-command`)
	waitForFileSubstring(t, tracePath, `"reason":"completed"`)
}

func TestHandleSessionCommandResolvesRelativeCWDInsideWorkspace(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	pool.cfg.EnableBuiltins = true
	sess, err := pool.GetOrCreate("workbench-relative-cwd")
	if err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	subdir := filepath.Join(sess.workspace, "sub")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatalf("mkdir subdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(subdir, "marker.txt"), []byte("relative-cwd-ok"), 0o644); err != nil {
		t.Fatalf("write marker: %v", err)
	}

	r := httptest.NewRequest(http.MethodPost, "/v1/sessions/workbench-relative-cwd/commands", bytes.NewReader([]byte(`{"command":"pwd; cat marker.txt","cwd":"sub","timeout_sec":5}`)))
	w := httptest.NewRecorder()
	handleSessionCommand(pool, "workbench-relative-cwd", w, r)

	if got := w.Result().StatusCode; got != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", got, w.Body.String())
	}
	var resp sessionCommandResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v body=%s", err, w.Body.String())
	}
	if resp.ExitCode != 0 || !strings.Contains(resp.Result, "relative-cwd-ok") {
		t.Fatalf("relative cwd command failed: %+v", resp)
	}
	if strings.Contains(filepath.ToSlash(resp.Result), filepath.ToSlash(subdir)) {
		t.Fatalf("relative cwd result should not leak absolute workspace subdir %q; result=%q", subdir, resp.Result)
	}
	if !strings.Contains(filepath.ToSlash(resp.Result), "./sub") {
		t.Fatalf("relative cwd should render under workspace as ./sub; result=%q", resp.Result)
	}
}

func TestHandleSessionCommandRejectsBusySession(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	pool.cfg.EnableBuiltins = true
	sess, err := pool.GetOrCreate("busy-command")
	if err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	sess.activeTurns.Store(1)

	r := httptest.NewRequest(http.MethodPost, "/v1/sessions/busy-command/commands", bytes.NewReader([]byte(`{"command":"pwd"}`)))
	w := httptest.NewRecorder()
	handleSessionCommand(pool, "busy-command", w, r)

	if got := w.Result().StatusCode; got != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", got, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "session_busy") {
		t.Fatalf("response should identify busy session: %s", w.Body.String())
	}
}

func TestHandleSessionCommandRequiresShellTool(t *testing.T) {
	pool := newTestPool(t, 4, "5m")

	r := httptest.NewRequest(http.MethodPost, "/v1/sessions/no-shell/commands", bytes.NewReader([]byte(`{"command":"pwd"}`)))
	w := httptest.NewRecorder()
	handleSessionCommand(pool, "no-shell", w, r)

	if got := w.Result().StatusCode; got != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", got, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "command_unavailable") {
		t.Fatalf("response should identify unavailable command surface: %s", w.Body.String())
	}
}

func TestDecodeSessionCommandRequestValidatesShape(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want string
	}{
		{name: "empty command", body: `{"command":"   "}`, want: "command is required"},
		{name: "negative timeout", body: `{"command":"pwd","timeout_sec":-1}`, want: "timeout_sec must be positive"},
		{name: "unknown field", body: `{"command":"pwd","extra":true}`, want: "unknown field"},
		{name: "oversized command", body: `{"command":"` + strings.Repeat("x", maxSessionCommandBytes+1) + `"}`, want: "command exceeds"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/v1/sessions/s/commands", bytes.NewReader([]byte(tc.body)))
			w := httptest.NewRecorder()
			_, err := decodeSessionCommandRequest(w, r)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestSessionCommandToolResultPayloadClassifiesFailure(t *testing.T) {
	payload := sessionCommandToolResultPayload("turn", "call", 1, "npm test\nFailure: kind=test_failed\n[exit 1]", 0)
	if payload.FailureKind == "" || len(payload.FailureKinds) == 0 {
		t.Fatalf("failure kind missing from payload: %+v", payload)
	}
}

func TestHandleSessionCommandRejectsInvalidSessionID(t *testing.T) {
	pool := newTestPool(t, 4, "5m")

	r := httptest.NewRequest(http.MethodPost, "/v1/sessions/../bad/commands", bytes.NewReader([]byte(`{"command":"pwd"}`)))
	w := httptest.NewRecorder()
	handleSessionCommand(pool, "../bad", w, r)

	if got := w.Result().StatusCode; got != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", got, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "invalid session id") || !strings.Contains(w.Body.String(), "plain filename") {
		t.Fatalf("response should include invalid session evidence: %s", w.Body.String())
	}
}
