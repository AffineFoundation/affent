package affent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
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

// TestReadFileTool_LargeFileFullyRead pins the contract that read_file
// either returns the full content (when within max_bytes) or appends
// the truncation marker — never silently emits a partial-page read.
// The earlier implementation used a single f.Read(buf), which returns
// whatever the OS has buffered (often a single 4 KiB page) and left
// large files looking complete to the model.
func TestReadFileTool_LargeFileFullyRead(t *testing.T) {
	tmp := t.TempDir()
	// 128 KiB of deterministic data — well above any single-page read.
	const total = 128 * 1024
	body := bytes.Repeat([]byte("0123456789abcdef"), total/16)
	path := filepath.Join(tmp, "big.txt")
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	deps := BuiltinDeps{HostWorkspaceDir: tmp}
	tool := readFileTool(deps)

	// Case 1: max_bytes >= file size — must return the whole file, no marker.
	args, _ := json.Marshal(map[string]any{"path": "big.txt", "max_bytes": total + 1024})
	out, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("read_file (full): %v", err)
	}
	if len(out) != total {
		t.Fatalf("expected full %d bytes, got %d (head=%q)", total, len(out), out[:min(len(out), 32)])
	}
	if strings.Contains(out, "truncated") {
		t.Fatalf("unexpected truncation marker on within-cap read")
	}

	// Case 2: max_bytes < file size — must return exactly max_bytes plus marker.
	const cap = 16 * 1024
	args2, _ := json.Marshal(map[string]any{"path": "big.txt", "max_bytes": cap})
	out2, err := tool.Execute(context.Background(), args2)
	if err != nil {
		t.Fatalf("read_file (capped): %v", err)
	}
	if !strings.Contains(out2, fmt.Sprintf("[truncated; %d-byte cap]", cap)) {
		t.Fatalf("expected truncation marker for capped read, got tail %q", out2[max(0, len(out2)-200):])
	}
}

// TestReadFileTool_TruncationIsUTF8Safe pins the prefix of a capped
// read to a valid UTF-8 boundary. Pre-fix the readFileTool sliced
// the buffer at the raw byte offset, so a cap landing mid-rune
// shipped invalid bytes (and U+FFFD on the model side).
func TestReadFileTool_TruncationIsUTF8Safe(t *testing.T) {
	tmp := t.TempDir()
	// 1000 Cyrillic ё runes = 2000 bytes. An odd-byte cap is
	// guaranteed to land inside one of them.
	body := strings.Repeat("ё", 1000)
	path := filepath.Join(tmp, "ru.txt")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	deps := BuiltinDeps{HostWorkspaceDir: tmp}
	tool := readFileTool(deps)

	args, _ := json.Marshal(map[string]any{"path": "ru.txt", "max_bytes": 51}) // odd → mid-rune
	out, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("read_file: %v", err)
	}
	// The truncation banner starts with "\n... [truncated"; anything
	// before that is the file prefix.
	prefix := strings.SplitN(out, "\n... [truncated", 2)[0]
	for _, r := range prefix {
		if r == '�' {
			t.Fatalf("UTF-8 replacement char in capped read prefix (mid-rune cut)\nprefix=%q", prefix)
		}
	}
}

// TestRegistry_RemovePullsToolFromBothMapAndOrder pins the symmetric
// counterpart to Add: callers can take a tool back out of the
// registry, and subsequent Defs() / Get() reflect the removal.
// Important for rollback paths (MCP RegisterAll, future extension
// reloads) that previously had no way to clean up after themselves.
func TestRegistry_RemovePullsToolFromBothMapAndOrder(t *testing.T) {
	reg := NewRegistry()
	mkTool := func(name string) *Tool {
		return &Tool{
			Name:        name,
			Description: name,
			Schema:      json.RawMessage(`{"type":"object"}`),
			Execute:     func(context.Context, json.RawMessage) (string, error) { return "", nil },
		}
	}
	reg.Add(mkTool("alpha"))
	reg.Add(mkTool("beta"))
	reg.Add(mkTool("gamma"))

	if !reg.Remove("beta") {
		t.Fatalf("Remove returned false for present tool")
	}
	if _, ok := reg.Get("beta"); ok {
		t.Fatalf("Get(beta) succeeded after Remove")
	}
	defs := reg.Defs()
	if len(defs) != 2 {
		t.Fatalf("Defs len = %d, want 2 after removal", len(defs))
	}
	// Order preservation: alpha then gamma.
	if defs[0].Function.Name != "alpha" || defs[1].Function.Name != "gamma" {
		t.Fatalf("Defs order = [%s, %s], want [alpha, gamma]", defs[0].Function.Name, defs[1].Function.Name)
	}

	if reg.Remove("beta") {
		t.Fatalf("Remove returned true for already-removed tool")
	}
	if reg.Remove("never-there") {
		t.Fatalf("Remove returned true for unknown tool")
	}
}
