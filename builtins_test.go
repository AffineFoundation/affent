package affent

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestSafeWorkspacePath pins the path-resolution contract: relative paths
// join onto the workspace, absolute paths are taken literally and must
// fall inside the workspace, anything else is an explicit escape error.
func TestSafeWorkspacePath(t *testing.T) {
	ws := "/app"
	deps := BuiltinDeps{HostWorkspaceDir: ws}

	cases := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{"relative", "cmd/root.go", "/app/cmd/root.go", false},
		{"relative-with-dot", "./cmd/root.go", "/app/cmd/root.go", false},
		{"absolute-inside-workspace", "/app/cmd/root.go", "/app/cmd/root.go", false},
		{"absolute-equals-workspace", "/app", "/app", false},
		{"empty-resolves-to-workspace", "", "/app", false},
		{"absolute-outside-workspace", "/etc/passwd", "", true},
		{"relative-traversal-out", "../etc/passwd", "", true},
		{"sentinel-no-longer-magic", "/workspace/foo", "", true},
		{"deep-relative", "a/b/c/d.txt", "/app/a/b/c/d.txt", false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := safeWorkspacePath(deps, c.in)
			if c.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q, got %q", c.in, got)
				}
				if !strings.Contains(err.Error(), "escape") {
					t.Errorf("error %q should mention escape", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for %q: %v", c.in, err)
			}
			want := filepath.Clean(c.want)
			if got != want {
				t.Errorf("safeWorkspacePath(%q) = %q, want %q", c.in, got, want)
			}
		})
	}
}

// TestSafeWorkspacePath_NonStandardWorkspace exercises the case that broke
// SWE-INFINITE: workspace mounted at the same real path the model addresses
// in absolute form. Pre-fix this would silently double-prefix into /app/app.
func TestSafeWorkspacePath_NonStandardWorkspace(t *testing.T) {
	deps := BuiltinDeps{HostWorkspaceDir: "/app"}
	got, err := safeWorkspacePath(deps, "/app/cmd/root.go")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "/app/cmd/root.go" {
		t.Errorf("got %q, want /app/cmd/root.go", got)
	}
}
