package affent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/affinefoundation/affent/executor"
)

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

// compile-time: must implement both interfaces.
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

	// write_file
	wf, _ := r.Get("write_file")
	if _, err := wf.Execute(ctx, json.RawMessage(`{"path":"/c/foo.txt","content":"hello"}`)); err != nil {
		t.Fatalf("write_file: %v", err)
	}
	if fake.writeCalls != 1 || fake.files["/c/foo.txt"] != "hello" {
		t.Fatalf("write_file did not route through FileOps: writeCalls=%d, files=%v",
			fake.writeCalls, fake.files)
	}

	// read_file
	rf, _ := r.Get("read_file")
	got, err := rf.Execute(ctx, json.RawMessage(`{"path":"/c/foo.txt"}`))
	if err != nil {
		t.Fatalf("read_file: %v", err)
	}
	if got != "hello" || fake.readCalls != 1 {
		t.Fatalf("read_file: got=%q readCalls=%d", got, fake.readCalls)
	}

	// edit_file
	ef, _ := r.Get("edit_file")
	if _, err := ef.Execute(ctx, json.RawMessage(`{"path":"/c/foo.txt","old":"hello","new":"HELLO"}`)); err != nil {
		t.Fatalf("edit_file: %v", err)
	}
	if fake.editCalls != 1 || fake.files["/c/foo.txt"] != "HELLO" {
		t.Fatalf("edit_file: editCalls=%d files=%v", fake.editCalls, fake.files)
	}

	// list_files
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
	// LocalExecutor does NOT implement FileOps, so file tools must
	// fall back to direct host-fs access against HostWorkspaceDir.
	// This is the legacy contract — anybody relying on
	// HostWorkspaceDir semantics (CLI, training rigs, gateway bind
	// mounts) keeps working unchanged.
	tmp := t.TempDir()
	r := NewRegistry()
	RegisterBuiltins(r, BuiltinDeps{
		Executor:         executor.NewLocalExecutor("test", tmp),
		HostWorkspaceDir: tmp,
	})

	ctx := context.Background()

	// write_file should land on the host fs.
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
