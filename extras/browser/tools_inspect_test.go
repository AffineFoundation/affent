package browser

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// TestResolveSavePath_SandboxesScreenshotWrites pins the workspace
// guard on browser_screenshot's save_path. Pre-fix, a save_path of
// "/etc/cron.d/anything.png" would have landed wherever the model
// asked — inconsistent with write_file's safeWorkspacePath sandbox.
// Now: WorkspaceDir != "" enforces the same boundary; "" preserves
// back-compat for callers that gate the tool some other way.
func TestResolveSavePath_SandboxesScreenshotWrites(t *testing.T) {
	ws := t.TempDir()
	cases := []struct {
		name    string
		ws      string
		in      string
		wantErr bool
	}{
		{"relative inside ws", ws, "shot.png", false},
		{"relative trims surrounding spaces", ws, "  shot.png  ", false},
		{"nested relative inside ws", ws, "captures/page1.png", false},
		{"absolute inside ws", ws, filepath.Join(ws, "shot.png"), false},
		{"absolute outside ws", ws, "/etc/cron.d/evil.png", true},
		{"relative-with-traversal escape", ws, "../../../etc/passwd.png", true},
		{"empty workspace disables enforcement", "", "/anywhere/shot.png", false},
		{"empty workspace, relative pass-through", "", "shot.png", false},
		{"empty workspace, blank path normalizes empty", "", "   ", false},
		// Filenames that start with ".." literally — must NOT be treated
		// as a traversal escape. Pre-fix the HasPrefix(rel, "..") check
		// rejected "..backup.png" because it shares the prefix with the
		// "../" go-up token; correct check requires "..<sep>" specifically.
		{"filename starting with two dots inside ws", ws, "..backup.png", false},
		{"nested file starting with two dots", ws, "captures/..thumb.png", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := resolveSavePath(c.ws, c.in)
			if c.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q, got %q", c.in, got)
				}
				if !strings.Contains(err.Error(), "escape") {
					t.Errorf("error message should explain escape: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for %q: %v", c.in, err)
			}
			if c.ws != "" && !strings.HasPrefix(got, c.ws) {
				t.Errorf("resolved %q to %q, should be inside ws %q", c.in, got, c.ws)
			}
			if c.ws == "" && got != strings.TrimSpace(c.in) {
				t.Errorf("empty ws should pass through trimmed %q, got %q", c.in, got)
			}
		})
	}
}

func TestScreenshotToolValidatesArgsBeforePageCheck(t *testing.T) {
	tool := ScreenshotTool(&Session{})
	if !strings.Contains(string(tool.Schema), `"maxLength": 4096`) {
		t.Fatalf("schema should publish save_path maxLength: %s", tool.Schema)
	}

	_, err := tool.Execute(context.Background(), json.RawMessage(`{"save_path":`))
	if err == nil || !strings.Contains(err.Error(), "decode args") {
		t.Fatalf("invalid JSON error = %v, want decode args", err)
	}

	_, err = tool.Execute(context.Background(), json.RawMessage(`{"save_path":"   "}`))
	if err == nil || !strings.Contains(err.Error(), "save_path must not be blank") {
		t.Fatalf("blank save_path error = %v, want blank save_path rejection", err)
	}
	if !strings.Contains(err.Error(), "Next:") {
		t.Fatalf("blank save_path error should include Next step, got %v", err)
	}

	longPath := strings.Repeat("x", maxScreenshotSavePathBytes+1)
	_, err = tool.Execute(context.Background(), json.RawMessage(`{"save_path":"`+longPath+`"}`))
	if err == nil || !strings.Contains(err.Error(), "browser_screenshot supports save_path up to") {
		t.Fatalf("oversized save_path error = %v, want save_path length rejection", err)
	}
	if !strings.Contains(err.Error(), "Next:") {
		t.Fatalf("oversized save_path error should include Next step, got %v", err)
	}
}
