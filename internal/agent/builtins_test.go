package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/affinefoundation/affent/internal/executor"
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

// TestSafeWorkspacePath_RejectsSymlinkEscape pins the sandbox against
// the lns -s attack: a symlink inside the workspace pointing OUT must
// not let a relative-path file op land outside. Real-rollout setup:
//
//	ln -s /tmp/affent-escape-target ws/exit
//	write_file path="exit/sneaky.txt"
//
// Pre-fix, filepath.Rel saw "exit/sneaky.txt" as inside the workspace
// and accepted the write, which os.WriteFile then followed onto
// /tmp/affent-escape-target/sneaky.txt — escaped.
func TestSafeWorkspacePath_RejectsSymlinkEscape(t *testing.T) {
	ws := t.TempDir()
	outside := t.TempDir()
	link := filepath.Join(ws, "exit")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	deps := BuiltinDeps{HostWorkspaceDir: ws}

	// Both an existing leaf via the symlink and a not-yet-existing
	// one must be rejected — write_file's "new file" case is exactly
	// where the parent-resolves-symlink path lands.
	for _, target := range []string{"exit/file.txt", "exit/nested/new.txt", "exit"} {
		got, err := safeWorkspacePath(deps, target)
		if err == nil {
			t.Errorf("safeWorkspacePath(%q via symlink) = %q, want escape error", target, got)
			continue
		}
		if !strings.Contains(err.Error(), "escape") {
			t.Errorf("error %q should mention escape", err)
		}
	}
}

// TestSafeWorkspacePath_AllowsInWorkspaceSymlink confirms the fix
// doesn't over-block: a symlink that points to a path STILL INSIDE
// the workspace must work (common pattern: `ln -s ../shared a/link`).
func TestSafeWorkspacePath_AllowsInWorkspaceSymlink(t *testing.T) {
	ws := t.TempDir()
	if err := os.MkdirAll(filepath.Join(ws, "shared"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(ws, "shared"), filepath.Join(ws, "link")); err != nil {
		t.Fatal(err)
	}
	deps := BuiltinDeps{HostWorkspaceDir: ws}
	got, err := safeWorkspacePath(deps, "link/file.txt")
	if err != nil {
		t.Fatalf("in-workspace symlink rejected: %v", err)
	}
	if got == "" {
		t.Fatal("empty result")
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

// TestReadFileTool_CapsRunawayMaxBytes pins the hard upper bound on
// read_file's max_bytes. Pre-fix, a model emitting max_bytes=1<<30
// would feed that into io.LimitReader and io.ReadAll, pulling
// gigabytes into memory before the rest of the pipeline noticed.
// Now MaxReadFileBytes (4 MiB) caps the request, so the result
// gets at most that-many bytes plus the standard truncation marker
// — same shape the model already handles for the 64-KiB default.
func TestReadFileTool_CapsRunawayMaxBytes(t *testing.T) {
	tmp := t.TempDir()
	const fileSize = MaxReadFileBytes + 100*1024 // 100 KiB above the cap
	body := bytes.Repeat([]byte("x"), fileSize)
	path := filepath.Join(tmp, "huge.txt")
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	deps := BuiltinDeps{HostWorkspaceDir: tmp}
	tool := readFileTool(deps)

	// Model asks for 1 GiB; cap kicks in and the read returns
	// MaxReadFileBytes worth of content plus the truncation marker.
	args, _ := json.Marshal(map[string]any{"path": "huge.txt", "max_bytes": 1 << 30})
	out, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("read_file: %v", err)
	}
	if !strings.Contains(out, fmt.Sprintf("[truncated; %d-byte cap]", MaxReadFileBytes)) {
		t.Fatalf("expected MaxReadFileBytes truncation marker; got tail %q",
			out[max(0, len(out)-200):])
	}
	// Total output size: content prefix + small marker. Must NOT be
	// anywhere near the model-requested 1 GiB.
	if len(out) > MaxReadFileBytes+1024 {
		t.Fatalf("output ballooned past the cap: len=%d", len(out))
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

// recordingExec captures the argv passed to Exec so tests can assert
// shell-prefix wiring without a real shell.
type recordingExec struct {
	gotArgv []string
}

func (r *recordingExec) SessionID() string { return "test" }
func (r *recordingExec) Exec(_ context.Context, cmd []string, _ executor.ExecOptions) (executor.ExecResult, error) {
	r.gotArgv = append([]string(nil), cmd...)
	return executor.ExecResult{ExitCode: 0, Stdout: "ok"}, nil
}

// TestShellTool_DefaultPrefixIsPortableSh pins the BuiltinDeps.Shell
// default to `sh -c` so the tool works against alpine / busybox / any
// POSIX-shell-only container. Hardcoding `bash -lc` (the old default)
// failed with "exec: bash: executable file not found in $PATH" the
// moment a real model tried `affentctl run --executor docker:<alpine>`
// — observed and fixed in real-LLM rollout testing.
func TestShellTool_DefaultPrefixIsPortableSh(t *testing.T) {
	rec := &recordingExec{}
	tool := shellTool(BuiltinDeps{Executor: rec})
	args, _ := json.Marshal(map[string]any{"command": "echo hi"})
	if _, err := tool.Execute(context.Background(), args); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	want := []string{"sh", "-c", "echo hi"}
	if len(rec.gotArgv) != 3 || rec.gotArgv[0] != want[0] || rec.gotArgv[1] != want[1] || rec.gotArgv[2] != want[2] {
		t.Fatalf("argv = %v, want %v", rec.gotArgv, want)
	}
}

// TestShellTool_ShellOverrideIsHonored pins the gateway escape hatch:
// dev-box deployments that want bash login semantics (PATH, ~/.bashrc)
// pass `["bash","-lc"]` into BuiltinDeps.Shell and get that exact prefix.
func TestShellTool_ShellOverrideIsHonored(t *testing.T) {
	rec := &recordingExec{}
	tool := shellTool(BuiltinDeps{Executor: rec, Shell: []string{"bash", "-lc"}})
	args, _ := json.Marshal(map[string]any{"command": "pwd"})
	if _, err := tool.Execute(context.Background(), args); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	want := []string{"bash", "-lc", "pwd"}
	if len(rec.gotArgv) != 3 || rec.gotArgv[0] != want[0] || rec.gotArgv[1] != want[1] || rec.gotArgv[2] != want[2] {
		t.Fatalf("argv = %v, want %v", rec.gotArgv, want)
	}
}

func TestShellToolRejectsUnboundedFilesystemScans(t *testing.T) {
	tool := shellTool(BuiltinDeps{Executor: nilExecutor{}})
	for _, command := range []string{
		`find / -name go -type f`,
		`grep -R "secret" /`,
		`rg / --files`,
	} {
		_, err := tool.Execute(context.Background(), json.RawMessage(`{"command":`+strconv.Quote(command)+`}`))
		if err == nil {
			t.Fatalf("expected broad scan command to be rejected: %s", command)
		}
		if !strings.Contains(err.Error(), "unbounded filesystem scan") {
			t.Fatalf("unexpected error for %s: %v", command, err)
		}
	}
}

func TestShellToolAllowsBoundedFinds(t *testing.T) {
	tool := shellTool(BuiltinDeps{Executor: nilExecutor{}})
	_, err := tool.Execute(context.Background(), json.RawMessage(`{"command":"find . -name '*.go' -maxdepth 3"}`))
	if err != nil {
		t.Fatalf("bounded workspace find should be allowed: %v", err)
	}
}

func TestShellTool_ExtraBroadScanIndicators(t *testing.T) {
	defaultTool := shellTool(BuiltinDeps{Executor: nilExecutor{}})
	out, err := defaultTool.Execute(context.Background(), json.RawMessage(`{"command":"du / -h --max-depth=1"}`))
	if err != nil && strings.Contains(err.Error(), "unbounded filesystem scan") {
		t.Fatalf("'du /' should not be rejected by the default set; got %v out=%q", err, out)
	}

	customTool := shellTool(BuiltinDeps{
		Executor:                 nilExecutor{},
		ExtraBroadScanIndicators: []string{"du "},
	})
	_, err = customTool.Execute(context.Background(), json.RawMessage(`{"command":"du / -h --max-depth=1"}`))
	if err == nil || !strings.Contains(err.Error(), "unbounded filesystem scan") {
		t.Fatalf("with extra broad-scan indicator, 'du /' must be rejected; got %v", err)
	}
}

// TestRegistry_RemovePullsToolFromBothMapAndOrder pins the symmetric
// counterpart to Add: callers can take a tool back out of the
// registry, and subsequent Defs() / Get() reflect the removal.
// Important for rollback paths (MCP RegisterAll, future extension
// reloads) that previously had no way to clean up after themselves.
// TestRegistry_dispatch_isErrFromExecuteError pins that the structured
// isErr flag comes from the underlying Execute error, not from a
// prefix scan on the result string. Surfaced when a `shell echo "Error:"`
// (legitimate stdout starting with "Error:" but exit=0) was being
// reported to consumers as exit_code=1.
func TestRegistry_dispatch_isErrFromExecuteError(t *testing.T) {
	reg := NewRegistry()
	reg.Add(&Tool{
		Name:   "ok_with_error_prefix",
		Schema: json.RawMessage(`{"type":"object"}`),
		Execute: func(context.Context, json.RawMessage) (string, error) {
			return "Error: this looks like an error but actually success", nil
		},
	})
	reg.Add(&Tool{
		Name:   "real_failure",
		Schema: json.RawMessage(`{"type":"object"}`),
		Execute: func(context.Context, json.RawMessage) (string, error) {
			return "", fmt.Errorf("boom")
		},
	})
	res, isErr := reg.dispatch(context.Background(), "ok_with_error_prefix", json.RawMessage(`{}`))
	if isErr {
		t.Errorf("isErr=true for nil-error Execute; result prefix scan must not flip the bool: %q", res)
	}
	res, isErr = reg.dispatch(context.Background(), "real_failure", json.RawMessage(`{}`))
	if !isErr {
		t.Errorf("isErr=false for non-nil Execute error: %q", res)
	}
	if !strings.HasPrefix(res, "Error: ") {
		t.Errorf("real_failure result must still carry 'Error: ' prefix so the model sees the failure: %q", res)
	}
	// Unknown tool is also an error (kept for parity with the previous behavior).
	_, isErr = reg.dispatch(context.Background(), "no-such-tool", json.RawMessage(`{}`))
	if !isErr {
		t.Errorf("isErr=false for unknown tool")
	}
}

// TestRegistry_dispatch_RecoversFromToolPanic pins that a panicking
// tool does NOT crash the dispatch goroutine. Without recover, a
// third-party tool with a latent nil-deref bug would tear down the
// whole runTurn goroutine — and in affentserve, the Go runtime would
// take every other concurrent session's goroutines down with it. The
// dispatch wraps the panic into a tool error so the model sees it
// and can adapt, and the stack is logged for operator triage.
func TestRegistry_dispatch_RecoversFromToolPanic(t *testing.T) {
	// Capture log output to verify the stack trace is recorded.
	var logBuf bytes.Buffer
	origOut := log.Writer()
	log.SetOutput(&logBuf)
	t.Cleanup(func() { log.SetOutput(origOut) })

	reg := NewRegistry()
	reg.Add(&Tool{
		Name:   "panicker",
		Schema: json.RawMessage(`{"type":"object"}`),
		Execute: func(context.Context, json.RawMessage) (string, error) {
			var nilPtr *string
			return *nilPtr, nil // nil deref, panic
		},
	})

	res, isErr := reg.dispatch(context.Background(), "panicker", json.RawMessage(`{}`))
	if !isErr {
		t.Errorf("panic must surface as isErr=true; got %q", res)
	}
	if !strings.Contains(res, "panicked") {
		t.Errorf("result must mention the panic so the model sees it; got %q", res)
	}
	logged := logBuf.String()
	if !strings.Contains(logged, "panicker") || !strings.Contains(logged, "goroutine") {
		t.Errorf("expected stack trace logged for operator triage; got: %s", logged)
	}
}

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

// fakeFileOpsExecutor is a recording Executor + FileOps so we can
// verify the builtin file tools route through FileOps when present.
type fakeFileOpsExecutor struct {
	*executor.LocalExecutor // satisfies Executor
	readCalls               int
	writeCalls              int
	editCalls               int
	listCalls               int

	files map[string]string
}

func newFakeFileOpsExecutor() *fakeFileOpsExecutor {
	return &fakeFileOpsExecutor{
		LocalExecutor: executor.NewLocalExecutor("test", "/unused"),
		files:         map[string]string{},
	}
}

func (f *fakeFileOpsExecutor) ReadFile(_ context.Context, path string, _ int) (string, error) {
	f.readCalls++
	if v, ok := f.files[path]; ok {
		return v, nil
	}
	return "", os.ErrNotExist
}

func (f *fakeFileOpsExecutor) WriteFile(_ context.Context, path, content string) error {
	f.writeCalls++
	f.files[path] = content
	return nil
}

func (f *fakeFileOpsExecutor) EditFile(_ context.Context, path, oldStr, newStr string, replaceAll bool) (int, error) {
	f.editCalls++
	body, ok := f.files[path]
	if !ok {
		return 0, os.ErrNotExist
	}
	n := strings.Count(body, oldStr)
	if n == 0 {
		return 0, os.ErrInvalid
	}
	if replaceAll {
		f.files[path] = strings.ReplaceAll(body, oldStr, newStr)
	} else {
		f.files[path] = strings.Replace(body, oldStr, newStr, 1)
	}
	return n, nil
}

func (f *fakeFileOpsExecutor) ListFiles(_ context.Context, _ string, _ int) ([]executor.FileEntry, error) {
	f.listCalls++
	out := make([]executor.FileEntry, 0, len(f.files))
	for name := range f.files {
		out = append(out, executor.FileEntry{Name: filepath.Base(name), Size: int64(len(f.files[name]))})
	}
	return out, nil
}

var _ executor.Executor = (*fakeFileOpsExecutor)(nil)
var _ executor.FileOps = (*fakeFileOpsExecutor)(nil)

func TestBuiltinFileToolsRouteThroughFileOps(t *testing.T) {
	fake := newFakeFileOpsExecutor()

	r := NewRegistry()
	RegisterBuiltins(r, BuiltinDeps{
		Executor:         fake,
		HostWorkspaceDir: "/this/path/must/never/be/touched",
	})

	ctx := context.Background()

	wf, _ := r.Get("write_file")
	if _, err := wf.Execute(ctx, json.RawMessage(`{"path":"/c/foo.txt","content":"hello"}`)); err != nil {
		t.Fatalf("write_file: %v", err)
	}
	if fake.writeCalls != 1 || fake.files["/c/foo.txt"] != "hello" {
		t.Fatalf("write_file did not route through FileOps: writeCalls=%d, files=%v",
			fake.writeCalls, fake.files)
	}

	rf, _ := r.Get("read_file")
	got, err := rf.Execute(ctx, json.RawMessage(`{"path":"/c/foo.txt"}`))
	if err != nil {
		t.Fatalf("read_file: %v", err)
	}
	if got != "hello" || fake.readCalls != 1 {
		t.Fatalf("read_file: got=%q readCalls=%d", got, fake.readCalls)
	}

	ef, _ := r.Get("edit_file")
	if _, err := ef.Execute(ctx, json.RawMessage(`{"path":"/c/foo.txt","old":"hello","new":"HELLO"}`)); err != nil {
		t.Fatalf("edit_file: %v", err)
	}
	if fake.editCalls != 1 || fake.files["/c/foo.txt"] != "HELLO" {
		t.Fatalf("edit_file: editCalls=%d files=%v", fake.editCalls, fake.files)
	}

	lf, _ := r.Get("list_files")
	out, err := lf.Execute(ctx, json.RawMessage(`{"path":"/c"}`))
	if err != nil {
		t.Fatalf("list_files: %v", err)
	}
	if fake.listCalls != 1 || !strings.Contains(out, "foo.txt") {
		t.Fatalf("list_files: listCalls=%d out=%q", fake.listCalls, out)
	}
}

func TestBuiltinFileToolsPreserveHostFallback(t *testing.T) {
	tmp := t.TempDir()
	r := NewRegistry()
	RegisterBuiltins(r, BuiltinDeps{
		Executor:         executor.NewLocalExecutor("test", tmp),
		HostWorkspaceDir: tmp,
	})

	ctx := context.Background()

	wf, _ := r.Get("write_file")
	if _, err := wf.Execute(ctx, json.RawMessage(`{"path":"hello.txt","content":"world"}`)); err != nil {
		t.Fatalf("write_file: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(tmp, "hello.txt"))
	if err != nil {
		t.Fatalf("expected host-fs write at %s/hello.txt: %v", tmp, err)
	}
	if string(body) != "world" {
		t.Fatalf("host-fs content = %q, want %q", body, "world")
	}
}
