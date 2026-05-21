package browser

import (
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
		{"nested relative inside ws", ws, "captures/page1.png", false},
		{"absolute inside ws", ws, filepath.Join(ws, "shot.png"), false},
		{"absolute outside ws", ws, "/etc/cron.d/evil.png", true},
		{"relative-with-traversal escape", ws, "../../../etc/passwd.png", true},
		{"empty workspace disables enforcement", "", "/anywhere/shot.png", false},
		{"empty workspace, relative pass-through", "", "shot.png", false},
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
			if c.ws == "" && got != c.in {
				t.Errorf("empty ws should pass through %q, got %q", c.in, got)
			}
		})
	}
}
