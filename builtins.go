package affent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/affinefoundation/affent/executor"
)

// BuiltinDeps is what the built-in tools need to do their job. The agent
// loop is intentionally tool-agnostic; the gateway (or a CLI driver)
// builds its own tool set on top of these.
type BuiltinDeps struct {
	// Executor runs the shell tool's commands. The choice of backend
	// (Docker container, local os/exec, ...) is up to the caller; the
	// loop doesn't care.
	Executor executor.Executor
	// HostWorkspaceDir is the host path the file tools read/write
	// through directly. The executor's view of this path doesn't have to
	// match (e.g. the gateway bind-mounts it as /workspace inside the
	// container), but file tools always operate via the host path.
	HostWorkspaceDir string
}

// RegisterBuiltins registers shell + file tools (the always-on set) on
// the registry. The gateway separately adds schedule tools, profile
// tools, etc; the agent module stays free of that layer.
func RegisterBuiltins(r *Registry, deps BuiltinDeps) {
	r.Add(shellTool(deps))
	r.Add(readFileTool(deps))
	r.Add(writeFileTool(deps))
	r.Add(editFileTool(deps))
	r.Add(listFilesTool(deps))
}

// ---- shell ----

func shellTool(deps BuiltinDeps) *Tool {
	schema := json.RawMessage(`{
        "type": "object",
        "required": ["command"],
        "properties": {
            "command": {"type": "string", "description": "Shell command to run via /bin/bash -lc inside the user's dev box. Working dir defaults to /workspace."},
            "cwd": {"type": "string", "description": "Optional working directory inside the container."},
            "timeout_sec": {"type": "integer", "description": "Optional timeout in seconds (default 120)."}
        }
    }`)
	return &Tool{
		Name:        "shell",
		Description: "Run a bash command inside the user's persistent dev box (Linux). Use this for git/curl/python/node/installs/inspection/anything CLI. Output: combined stdout+stderr followed by an exit code line.",
		Schema:      schema,
		Execute: func(ctx context.Context, args json.RawMessage) (string, error) {
			var p struct {
				Command    string `json:"command"`
				Cwd        string `json:"cwd"`
				TimeoutSec int    `json:"timeout_sec"`
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return "", fmt.Errorf("decode args: %w", err)
			}
			if strings.TrimSpace(p.Command) == "" {
				return "", errors.New("command is required")
			}
			if p.TimeoutSec <= 0 {
				p.TimeoutSec = 120
			}
			res, err := deps.Executor.Exec(ctx, []string{"bash", "-lc", p.Command}, executor.ExecOptions{
				WorkingDir: p.Cwd,
				Timeout:    time.Duration(p.TimeoutSec) * time.Second,
			})
			if err != nil {
				return "", err
			}
			return formatShellOutput(res), nil
		},
	}
}

func formatShellOutput(res executor.ExecResult) string {
	var b strings.Builder
	b.WriteString(res.Stdout)
	if res.Stderr != "" {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString("STDERR:\n")
		b.WriteString(res.Stderr)
	}
	fmt.Fprintf(&b, "\n[exit %d]", res.ExitCode)
	return b.String()
}

// ---- file ops (operate on the host bind mount, never via docker exec --
// way faster + we can preview/diff in the gateway) ----

// safeWorkspacePath resolves p against the workspace and rejects anything
// that escapes it. Two rules, no sentinels:
//
//   - relative path  -> joined onto HostWorkspaceDir
//   - absolute path  -> taken literally; must fall inside HostWorkspaceDir
//
// Earlier versions silently rewrote any leading "/" or "/workspace" prefix
// into a workspace-relative path. That made `/etc/passwd` look like an
// in-workspace lookup instead of an explicit escape (sandbox check missed
// it) and caused real-mount paths like `/app/foo` to double-prefix into
// `/app/app/foo` when the workspace happened to be `/app`. The two-rule
// version below has no such ambiguity: callers that want the old "model
// always sees /workspace" behaviour set HostWorkspaceDir to "/workspace"
// and the absolute-path branch handles it directly.
func safeWorkspacePath(deps BuiltinDeps, p string) (string, error) {
	if p == "" {
		return deps.HostWorkspaceDir, nil
	}
	var full string
	if filepath.IsAbs(p) {
		full = filepath.Clean(p)
	} else {
		full = filepath.Join(deps.HostWorkspaceDir, p)
	}
	rel, err := filepath.Rel(deps.HostWorkspaceDir, full)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q escapes workspace %q", p, deps.HostWorkspaceDir)
	}
	return full, nil
}

func readFileTool(deps BuiltinDeps) *Tool {
	schema := json.RawMessage(`{
        "type": "object",
        "required": ["path"],
        "properties": {
            "path": {"type": "string", "description": "Path relative to /workspace, or absolute starting with /workspace."},
            "max_bytes": {"type": "integer", "description": "Optional cap (default 64KiB)."}
        }
    }`)
	return &Tool{
		Name:        "read_file",
		Description: "Read a file from the user's workspace. Returns the file's text. For very large files, use the shell tool with head/sed/etc.",
		Schema:      schema,
		Execute: func(ctx context.Context, args json.RawMessage) (string, error) {
			var p struct {
				Path     string `json:"path"`
				MaxBytes int    `json:"max_bytes"`
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return "", err
			}
			if p.Path == "" {
				return "", errors.New("path is required")
			}
			if p.MaxBytes <= 0 {
				p.MaxBytes = 64 * 1024
			}
			full, err := safeWorkspacePath(deps, p.Path)
			if err != nil {
				return "", err
			}
			f, err := os.Open(full)
			if err != nil {
				return "", err
			}
			defer f.Close()
			buf := make([]byte, p.MaxBytes+1)
			n, _ := f.Read(buf)
			if n > p.MaxBytes {
				return string(buf[:p.MaxBytes]) + fmt.Sprintf("\n... [truncated; %d-byte cap]", p.MaxBytes), nil
			}
			return string(buf[:n]), nil
		},
	}
}

func writeFileTool(deps BuiltinDeps) *Tool {
	schema := json.RawMessage(`{
        "type": "object",
        "required": ["path", "content"],
        "properties": {
            "path": {"type": "string"},
            "content": {"type": "string"}
        }
    }`)
	return &Tool{
		Name:        "write_file",
		Description: "Write (overwrite) a file in the user's workspace. Creates parent dirs as needed.",
		Schema:      schema,
		Execute: func(ctx context.Context, args json.RawMessage) (string, error) {
			var p struct {
				Path    string `json:"path"`
				Content string `json:"content"`
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return "", err
			}
			if p.Path == "" {
				return "", errors.New("path is required")
			}
			full, err := safeWorkspacePath(deps, p.Path)
			if err != nil {
				return "", err
			}
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				return "", err
			}
			if err := os.WriteFile(full, []byte(p.Content), 0o644); err != nil {
				return "", err
			}
			return fmt.Sprintf("wrote %d bytes to %s", len(p.Content), p.Path), nil
		},
	}
}

func editFileTool(deps BuiltinDeps) *Tool {
	schema := json.RawMessage(`{
        "type": "object",
        "required": ["path", "old", "new"],
        "properties": {
            "path": {"type": "string"},
            "old":  {"type": "string", "description": "Exact string to find. Must occur exactly once unless replace_all is true."},
            "new":  {"type": "string"},
            "replace_all": {"type": "boolean", "default": false}
        }
    }`)
	return &Tool{
		Name:        "edit_file",
		Description: "Find-and-replace within a file in the workspace. Useful for surgical edits without rewriting the whole file. The 'old' string must match exactly (whitespace + newlines).",
		Schema:      schema,
		Execute: func(ctx context.Context, args json.RawMessage) (string, error) {
			var p struct {
				Path       string `json:"path"`
				Old        string `json:"old"`
				New        string `json:"new"`
				ReplaceAll bool   `json:"replace_all"`
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return "", err
			}
			if p.Path == "" || p.Old == "" {
				return "", errors.New("path and old are required")
			}
			full, err := safeWorkspacePath(deps, p.Path)
			if err != nil {
				return "", err
			}
			raw, err := os.ReadFile(full)
			if err != nil {
				return "", err
			}
			body := string(raw)
			n := strings.Count(body, p.Old)
			if n == 0 {
				return "", fmt.Errorf("old string not found in %s", p.Path)
			}
			if n > 1 && !p.ReplaceAll {
				return "", fmt.Errorf("old string occurs %d times in %s; pass replace_all=true or include more context to make it unique", n, p.Path)
			}
			var updated string
			if p.ReplaceAll {
				updated = strings.ReplaceAll(body, p.Old, p.New)
			} else {
				updated = strings.Replace(body, p.Old, p.New, 1)
			}
			if err := os.WriteFile(full, []byte(updated), 0o644); err != nil {
				return "", err
			}
			return fmt.Sprintf("replaced %d occurrence(s) in %s", n, p.Path), nil
		},
	}
}

func listFilesTool(deps BuiltinDeps) *Tool {
	schema := json.RawMessage(`{
        "type": "object",
        "properties": {
            "path": {"type": "string", "description": "Directory under /workspace; defaults to /workspace itself."},
            "max_entries": {"type": "integer"}
        }
    }`)
	return &Tool{
		Name:        "list_files",
		Description: "List files / dirs at the given path inside the workspace. For deep recursion or filtering, use the shell tool with find/ls.",
		Schema:      schema,
		Execute: func(ctx context.Context, args json.RawMessage) (string, error) {
			var p struct {
				Path       string `json:"path"`
				MaxEntries int    `json:"max_entries"`
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return "", err
			}
			if p.Path == "" {
				p.Path = "."
			}
			if p.MaxEntries <= 0 {
				p.MaxEntries = 200
			}
			full, err := safeWorkspacePath(deps, p.Path)
			if err != nil {
				return "", err
			}
			entries, err := os.ReadDir(full)
			if err != nil {
				return "", err
			}
			var b strings.Builder
			for i, e := range entries {
				if i >= p.MaxEntries {
					fmt.Fprintf(&b, "... and %d more\n", len(entries)-i)
					break
				}
				kind := "file"
				if e.IsDir() {
					kind = "dir "
				}
				info, _ := e.Info()
				size := int64(0)
				if info != nil {
					size = info.Size()
				}
				fmt.Fprintf(&b, "%s  %10d  %s\n", kind, size, e.Name())
			}
			if b.Len() == 0 {
				return "(empty)", nil
			}
			return b.String(), nil
		},
	}
}

