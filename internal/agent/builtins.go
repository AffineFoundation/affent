package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/affinefoundation/affent/internal/executor"
	"github.com/affinefoundation/affent/internal/textutil"
)

// looksBinary returns true when buf has a NUL byte in the first 8 KiB.
// Mirrors file(1) / git / `grep -I` — NUL is rare in any text encoding
// but ubiquitous in binary formats. Used by read_file to refuse rather
// than dump 64 KiB of replacement characters into the model context.
func looksBinary(buf []byte) bool {
	n := len(buf)
	if n > 8192 {
		n = 8192
	}
	return bytes.IndexByte(buf[:n], 0) >= 0
}

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
	// Memory enables the `memory` tool. Pass the same store assigned
	// to Loop.Memory so the snapshot in the system prompt and the
	// tool see the same on-disk state.
	Memory MemoryStore
	// SessionsDir is the directory holding past session JSONL logs.
	// When non-empty, the `session_search` tool is registered so the
	// agent can retrieve snippets from past conversations.
	SessionsDir string
	// SessionID is the current session's id; session_search excludes
	// it so the agent doesn't match its own in-progress turns.
	SessionID string
	// Shell is the command prefix the shell tool wraps the user's
	// command in. Default is `["sh", "-c"]` — POSIX-portable across
	// alpine / busybox / debian / centos containers. Gateways with a
	// bash dev-box that needs login-shell semantics (PATH, ~/.bashrc)
	// can set `["bash", "-lc"]` here.
	Shell []string
}

// defaultShell is the portable fallback when BuiltinDeps.Shell is unset.
// `sh -c` works in every shipping Linux container we've seen (alpine
// has busybox sh, distroless usually doesn't get the shell tool at all).
// `bash -lc` was the historical default — broke on alpine, see d1fecfe
// follow-up.
var defaultShell = []string{"sh", "-c"}

// RegisterBuiltins registers shell + file tools on the registry, the
// `memory` tool when deps.Memory is non-nil, and the `session_search`
// tool when deps.SessionsDir is non-empty.
func RegisterBuiltins(r *Registry, deps BuiltinDeps) {
	r.Add(shellTool(deps))
	r.Add(readFileTool(deps))
	r.Add(writeFileTool(deps))
	r.Add(editFileTool(deps))
	r.Add(listFilesTool(deps))
	if deps.Memory != nil {
		r.Add(memoryTool(deps.Memory))
	}
	if deps.SessionsDir != "" {
		r.Add(sessionSearchTool(deps.SessionsDir, deps.SessionID))
	}
}

// RegisterMemoryOnly registers just the `memory` tool. This is useful
// for controlled environments that must isolate memory behavior from
// shell / file / MCP surfaces.
func RegisterMemoryOnly(r *Registry, store MemoryStore) {
	r.Add(memoryTool(store))
}

// ---- shell ----

func shellTool(deps BuiltinDeps) *Tool {
	schema := json.RawMessage(`{
        "type": "object",
        "required": ["command"],
        "properties": {
            "command": {"type": "string", "description": "Shell command to run. Wrapped in a POSIX shell (sh -c by default; gateways may configure bash -lc)."},
            "cwd": {"type": "string", "description": "Optional working directory."},
            "timeout_sec": {"type": "integer", "description": "Optional timeout in seconds (default 120)."}
        }
    }`)
	shellPrefix := deps.Shell
	if len(shellPrefix) == 0 {
		shellPrefix = defaultShell
	}
	return &Tool{
		Name:        "shell",
		Description: "Run a shell command (Linux). Use this for git/curl/python/node/installs/inspection/anything CLI. Output: combined stdout+stderr followed by an exit code line.",
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
			argv := append(append([]string{}, shellPrefix...), p.Command)
			res, err := deps.Executor.Exec(ctx, argv, executor.ExecOptions{
				WorkingDir: p.Cwd,
				Timeout:    time.Duration(p.TimeoutSec) * time.Second,
			})
			// Pass the captured streams through even on error so the
			// model can see partial output from a timed-out / killed
			// command. The Loop's dispatch wraps a non-nil err alongside
			// res into "Error: <err>\n<res>" — exactly what we want.
			return formatShellOutput(res), err
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
//
// Symlinks are resolved before the escape check (defeats
// `ln -s /etc ws/escape` followed by write_file("escape/passwd") that
// would otherwise drop a file at /etc/passwd). The longest-existing-
// prefix variant supports write_file's new-file case where the leaf
// hasn't been created yet.
//
// Caveat: still TOCTOU-vulnerable in theory — a sufficiently fast
// attacker could swap a real subdir for a symlink between check and
// write. Defense-in-depth only; this isn't a substitute for trusting
// the executor / container boundary.
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
	resolved, err := resolveAncestorSymlinks(full)
	if err != nil {
		return "", err
	}
	wsAbs := deps.HostWorkspaceDir
	if r, err := filepath.EvalSymlinks(deps.HostWorkspaceDir); err == nil {
		// Workspace itself may be a stable symlink in some deployments;
		// resolve it so filepath.Rel below compares apples to apples.
		wsAbs = r
	}
	rel, err := filepath.Rel(wsAbs, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q escapes workspace %q", p, deps.HostWorkspaceDir)
	}
	return full, nil
}

// resolveAncestorSymlinks walks up `full` to the longest existing
// prefix, EvalSymlinks that, then re-attaches any missing tail
// components verbatim. Lets safeWorkspacePath validate paths whose
// leaf hasn't been created yet (write_file creating a new file)
// while still defeating symlinks that point outside the workspace
// in any existing ancestor.
func resolveAncestorSymlinks(full string) (string, error) {
	cur := full
	var missing []string
	for {
		if _, err := os.Lstat(cur); err == nil {
			resolved, err := filepath.EvalSymlinks(cur)
			if err != nil {
				return "", err
			}
			for i := len(missing) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, missing[i])
			}
			return resolved, nil
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			// Reached root without finding any existing component
			// (workspace must not exist either). Fall back to the
			// unresolved path; the caller's Rel check will still
			// catch absolute-outside cases.
			return full, nil
		}
		missing = append(missing, filepath.Base(cur))
		cur = parent
	}
}

// fileOps returns deps.Executor as a FileOps if it implements the
// extension interface. Tools route through it instead of touching the
// host fs when set — that's how container-backed executors (e.g.
// DockerExecExecutor) make file ops act on the container's view.
func fileOps(deps BuiltinDeps) executor.FileOps {
	if deps.Executor == nil {
		return nil
	}
	fo, _ := deps.Executor.(executor.FileOps)
	return fo
}

// MaxReadFileBytes hard-caps how much read_file will pull into
// memory regardless of the model's max_bytes argument. Prevents an
// untrusted/confused model from passing max_bytes=1<<30 and OOMing
// the process while waiting for io.ReadAll. Anything larger than
// this should be paginated via shell with head/tail/sed instead.
const MaxReadFileBytes = 4 * 1024 * 1024

func readFileTool(deps BuiltinDeps) *Tool {
	schema := json.RawMessage(`{
        "type": "object",
        "required": ["path"],
        "properties": {
            "path": {"type": "string", "description": "Path; relative joins onto the workspace root, absolute must fall inside it."},
            "max_bytes": {"type": "integer", "minimum": 1, "maximum": 4194304, "description": "Optional cap (default 64 KiB, hard upper bound 4 MiB). Larger requests are clamped silently — use shell with head/tail/sed for anything past 4 MiB."}
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
			if p.MaxBytes > MaxReadFileBytes {
				p.MaxBytes = MaxReadFileBytes
			}
			if fo := fileOps(deps); fo != nil {
				return fo.ReadFile(ctx, p.Path, p.MaxBytes)
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
			// Read MaxBytes+1 so we can detect when the file exceeds
			// the cap without loading any more than necessary. A bare
			// f.Read(buf) returns whatever the OS has buffered, often
			// just one page — which silently truncated large files
			// without emitting the cap marker the agent relies on to
			// know more content exists.
			buf, err := io.ReadAll(io.LimitReader(f, int64(p.MaxBytes)+1))
			if err != nil {
				return "", err
			}
			// Refuse binary files. Dumping null/non-UTF-8 bytes into
			// the model context wastes tokens and almost never tells
			// the model anything useful — the model just sees a wall
			// of replacement characters. The shell tool with file/xxd/
			// base64 is the right escape hatch for inspecting binary.
			// Heuristic matches file(1) / git / grep -I: any NUL in
			// the first 8 KiB.
			if looksBinary(buf) {
				return "", fmt.Errorf("%s appears to be binary (contains null bytes); use shell with file/xxd/base64 to inspect", p.Path)
			}
			if len(buf) > p.MaxBytes {
				// Snap back to a UTF-8 rune boundary so a CJK / accented
				// content read that lands mid-rune doesn't ship invalid
				// bytes to the model.
				cut := textutil.AlignBackward(string(buf), p.MaxBytes)
				return string(buf[:cut]) + fmt.Sprintf("\n... [truncated; %d-byte cap]", p.MaxBytes), nil
			}
			return string(buf), nil
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
			if fo := fileOps(deps); fo != nil {
				if err := fo.WriteFile(ctx, p.Path, p.Content); err != nil {
					return "", err
				}
				return fmt.Sprintf("wrote %d bytes to %s", len(p.Content), p.Path), nil
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
			if fo := fileOps(deps); fo != nil {
				n, err := fo.EditFile(ctx, p.Path, p.Old, p.New, p.ReplaceAll)
				if err != nil {
					return "", err
				}
				return fmt.Sprintf("replaced %d occurrence(s) in %s", n, p.Path), nil
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

// MaxListFilesEntries hard-caps the number of directory entries
// list_files will return regardless of the model's max_entries
// argument. Mainly a string-builder allocation guard: a model
// asking for a million entries on a busy /var/log shouldn't
// trigger a multi-MB temporary buffer (the actual model-facing
// output truncates to MaxToolResultBytesInContext = 8 KiB anyway,
// so anything past a few hundred entries is wasted work).
const MaxListFilesEntries = 1000

func listFilesTool(deps BuiltinDeps) *Tool {
	schema := json.RawMessage(`{
        "type": "object",
        "properties": {
            "path": {"type": "string", "description": "Directory; relative joins onto the workspace root. Empty / '.' lists the workspace root itself."},
            "max_entries": {"type": "integer", "minimum": 1, "maximum": 1000, "description": "Optional cap (default 200, hard upper bound 1000). Use shell with find/ls for larger or filtered enumerations."}
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
			if p.MaxEntries > MaxListFilesEntries {
				p.MaxEntries = MaxListFilesEntries
			}
			if fo := fileOps(deps); fo != nil {
				entries, err := fo.ListFiles(ctx, p.Path, p.MaxEntries+1)
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
					if e.IsDir {
						kind = "dir "
					}
					fmt.Fprintf(&b, "%s  %10d  %s\n", kind, e.Size, e.Name)
				}
				if b.Len() == 0 {
					return "(empty)", nil
				}
				return b.String(), nil
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
