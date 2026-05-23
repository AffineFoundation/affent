package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadLocalSessionPlan_ReadsBoundedJSON(t *testing.T) {
	convDir := t.TempDir()
	path := localSessionPlanPath(convDir, "sess_ok")
	if err := os.WriteFile(path, []byte(" \n{\"version\":1,\"steps\":[{\"text\":\"ship\",\"status\":\"pending\"}]}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	raw, found, err := readLocalSessionPlan(convDir, "sess_ok")
	if err != nil {
		t.Fatalf("readLocalSessionPlan: %v", err)
	}
	if !found {
		t.Fatal("plan should be found")
	}
	if !bytes.Contains(raw, []byte(`"steps"`)) {
		t.Fatalf("plan = %s", raw)
	}
	if !sessionPlanExists(convDir, "sess_ok") {
		t.Fatal("sessionPlanExists should report the plan file")
	}
}

func TestReadLocalSessionPlan_MissingReturnsNotFound(t *testing.T) {
	_, found, err := readLocalSessionPlan(t.TempDir(), "missing")
	if err != nil {
		t.Fatalf("readLocalSessionPlan: %v", err)
	}
	if found {
		t.Fatal("missing plan should not be found")
	}
}

func TestReadLocalSessionPlan_RejectsUnsafeOrBadPlan(t *testing.T) {
	convDir := t.TempDir()
	if _, _, err := readLocalSessionPlan(convDir, "../escape"); err == nil || !strings.Contains(err.Error(), "invalid session id") {
		t.Fatalf("unsafe id error = %v, want invalid session id", err)
	}

	if err := os.WriteFile(localSessionPlanPath(convDir, "bad_json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readLocalSessionPlan(convDir, "bad_json"); err == nil || !strings.Contains(err.Error(), "valid JSON") {
		t.Fatalf("bad json error = %v, want valid JSON", err)
	}

	tooLarge := strings.Repeat(" ", maxLocalSessionPlanBytes+1)
	if err := os.WriteFile(filepath.Join(convDir, "too_large.plan.json"), []byte(tooLarge), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readLocalSessionPlan(convDir, "too_large"); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("too-large error = %v, want exceeds", err)
	}
}

func TestReadLocalSessionPlan_RejectsSymlinkPlan(t *testing.T) {
	convDir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside-plan.json")
	if err := os.WriteFile(outside, []byte(`{"version":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, localSessionPlanPath(convDir, "linked")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	_, found, err := readLocalSessionPlan(convDir, "linked")
	if err == nil || found {
		t.Fatalf("readLocalSessionPlan symlink = found:%v err:%v, want error", found, err)
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("error = %v, want symlink", err)
	}
	if sessionPlanExists(convDir, "linked") {
		t.Fatal("sessionPlanExists must not follow symlink plans")
	}
}

func TestClearLocalSessionPlan_RemovesOnlySafePlanFiles(t *testing.T) {
	convDir := t.TempDir()
	if removed, err := clearLocalSessionPlan(convDir, "missing"); err != nil || removed {
		t.Fatalf("missing clear = removed:%v err:%v, want false/nil", removed, err)
	}
	if _, err := clearLocalSessionPlan(convDir, "../escape"); err == nil || !strings.Contains(err.Error(), "invalid session id") {
		t.Fatalf("unsafe id error = %v, want invalid session id", err)
	}

	path := localSessionPlanPath(convDir, "safe")
	if err := os.WriteFile(path, []byte(`{"version":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	removed, err := clearLocalSessionPlan(convDir, "safe")
	if err != nil || !removed {
		t.Fatalf("safe clear = removed:%v err:%v, want true/nil", removed, err)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("safe plan should be removed, err=%v", err)
	}

	outside := filepath.Join(t.TempDir(), "outside-plan.json")
	if err := os.WriteFile(outside, []byte(`{"version":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, localSessionPlanPath(convDir, "linked")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := clearLocalSessionPlan(convDir, "linked"); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("linked clear error = %v, want symlink rejection", err)
	}
	if _, err := os.Lstat(outside); err != nil {
		t.Fatalf("outside plan should remain: %v", err)
	}
}

func TestSessionsCmd_PrintsPlanAndMarksListingProgress(t *testing.T) {
	workspace := t.TempDir()
	convDir := filepath.Join(workspace, ".affentctl")
	if err := os.MkdirAll(convDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(convDir, "sess_one.jsonl"), []byte(`{"role":"user","content":"hello"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(localSessionPlanPath(convDir, "sess_one"), []byte(`{"version":1,"steps":[{"text":"inspect","status":"completed"},{"text":"ship","status":"in_progress"}]}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	listOut := captureStdout(t, func() {
		if code := sessionsCmd([]string{"--workspace", workspace}); code != 0 {
			t.Fatalf("sessionsCmd list exit = %d, want 0", code)
		}
	})
	if !strings.Contains(listOut, "\tplan:1/2:active\t") {
		t.Fatalf("list output should mark session plan progress, got:\n%s", listOut)
	}

	planOut := captureStdout(t, func() {
		if code := sessionsCmd([]string{"--workspace", workspace, "--plan", "sess_one"}); code != 0 {
			t.Fatalf("sessionsCmd plan exit = %d, want 0", code)
		}
	})
	if !strings.Contains(planOut, `"steps"`) || !strings.Contains(planOut, `"ship"`) {
		t.Fatalf("plan output = %s", planOut)
	}
}

func TestSessionPlanSummaryReportsEmptyBlockedAndErrors(t *testing.T) {
	convDir := t.TempDir()
	if got := sessionPlanSummary(convDir, "missing"); got != "-" {
		t.Fatalf("missing summary = %q, want -", got)
	}
	if err := os.WriteFile(localSessionPlanPath(convDir, "empty"), []byte(`{"version":1,"steps":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := sessionPlanSummary(convDir, "empty"); got != "plan:empty" {
		t.Fatalf("empty summary = %q, want plan:empty", got)
	}
	if err := os.WriteFile(localSessionPlanPath(convDir, "blocked"), []byte(`{"version":1,"steps":[{"status":"completed"},{"status":"blocked"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := sessionPlanSummary(convDir, "blocked"); got != "plan:1/2:blocked" {
		t.Fatalf("blocked summary = %q, want plan:1/2:blocked", got)
	}
	if err := os.WriteFile(localSessionPlanPath(convDir, "bad"), []byte(`{`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := sessionPlanSummary(convDir, "bad"); got != "plan:error" {
		t.Fatalf("bad summary = %q, want plan:error", got)
	}
}

func TestSessionsCmdSkipsSymlinkLogs(t *testing.T) {
	workspace := t.TempDir()
	convDir := filepath.Join(workspace, ".affentctl")
	if err := os.MkdirAll(convDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(convDir, "real.jsonl"), []byte(`{"role":"user","content":"hello"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.jsonl")
	if err := os.WriteFile(outside, []byte(`{"role":"user","content":"outside"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(convDir, "linked.jsonl")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	if n, preview := scanLog(filepath.Join(convDir, "linked.jsonl")); n != 0 || preview != "" {
		t.Fatalf("scanLog symlink = (%d, %q), want zero/empty", n, preview)
	}
	listOut := captureStdout(t, func() {
		if code := sessionsCmd([]string{"--workspace", workspace}); code != 0 {
			t.Fatalf("sessionsCmd list exit = %d, want 0", code)
		}
	})
	if strings.Contains(listOut, "linked") || strings.Contains(listOut, "outside") {
		t.Fatalf("list output should skip symlink log, got:\n%s", listOut)
	}
	if !strings.Contains(listOut, "real") {
		t.Fatalf("list output should keep real session, got:\n%s", listOut)
	}
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	defer func() { os.Stdout = orig }()

	fn()
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	return string(out)
}
