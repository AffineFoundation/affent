package executor

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// dockerOrSkip starts a short-lived alpine container for the test and
// returns its id + a cleanup. Skips when docker isn't reachable so the
// affent unit suite stays usable on machines without Docker.
func dockerOrSkip(t *testing.T) (string, func()) {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not in PATH")
	}
	// `docker info` is cheap and detects daemon-unreachable cases that
	// `docker --version` wouldn't.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := exec.CommandContext(ctx, "docker", "info").Run(); err != nil {
		t.Skip("docker daemon not reachable")
	}

	out, err := exec.Command("docker", "run", "-d", "--rm",
		"alpine:3.19", "sh", "-c", "sleep 30").Output()
	if err != nil {
		t.Skipf("docker run alpine failed (image not available?): %v", err)
	}
	cid := strings.TrimSpace(string(out))
	cleanup := func() {
		_ = exec.Command("docker", "rm", "-f", cid).Run()
	}
	return cid, cleanup
}

func TestDockerExecExec_Smoke(t *testing.T) {
	cid, cleanup := dockerOrSkip(t)
	defer cleanup()

	d := NewDockerExecExecutor("test", cid)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	res, err := d.Exec(ctx, []string{"sh", "-c", "echo hello && echo world >&2"}, ExecOptions{Timeout: 10 * time.Second})
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("exit code: got %d, want 0 (stderr=%q)", res.ExitCode, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "hello") {
		t.Errorf("stdout = %q, want contains 'hello'", res.Stdout)
	}
	if !strings.Contains(res.Stderr, "world") {
		t.Errorf("stderr = %q, want contains 'world'", res.Stderr)
	}
}

func TestDockerExecFileOps_Roundtrip(t *testing.T) {
	cid, cleanup := dockerOrSkip(t)
	defer cleanup()

	d := NewDockerExecExecutor("test", cid)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Write into a nested path; verify parent dirs auto-created.
	content := "alpha\nbeta\ngamma\n"
	if err := d.WriteFile(ctx, "/work/notes/x.txt", content); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := d.ReadFile(ctx, "/work/notes/x.txt", 1024)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got != content {
		t.Fatalf("read mismatch:\n got %q\nwant %q", got, content)
	}

	// Binary content must be refused, not dumped into the model context.
	// Earlier the test exercised a NUL-byte round-trip, which is no longer
	// the contract — readFileTool / FileOps.ReadFile now reject binary.
	if err := d.WriteFile(ctx, "/work/notes/bin.dat", "head\x00mid\x00tail"); err != nil {
		t.Fatalf("write bin: %v", err)
	}
	if _, err := d.ReadFile(ctx, "/work/notes/bin.dat", 1024); err == nil ||
		!strings.Contains(err.Error(), "binary") {
		t.Errorf("expected binary-refusal error, got %v", err)
	}

	// Edit (unique match).
	n, err := d.EditFile(ctx, "/work/notes/x.txt", "beta", "BETA", false)
	if err != nil {
		t.Fatalf("edit: %v", err)
	}
	if n != 1 {
		t.Errorf("edit count = %d, want 1", n)
	}
	got, _ = d.ReadFile(ctx, "/work/notes/x.txt", 1024)
	if !strings.Contains(got, "BETA") || strings.Contains(got, "beta") {
		t.Errorf("after edit, file = %q", got)
	}

	// Edit non-unique without replace_all should error.
	if _, err := d.EditFile(ctx, "/work/notes/x.txt", "a", "A", false); err == nil {
		t.Error("expected non-unique edit without replace_all to fail")
	} else if !errors.Is(err, ErrEditAmbiguousMatch) {
		t.Fatalf("non-unique edit error should wrap ErrEditAmbiguousMatch, got %v", err)
	}
	// Edit refuses large files before streaming them back to the host
	// process. Use a sparse file so the test exercises size handling
	// without consuming container or host memory.
	res, err := d.Exec(ctx, []string{"sh", "-c", "dd if=/dev/zero of=/work/notes/huge.txt bs=1 count=0 seek=4194305"}, ExecOptions{})
	if err != nil {
		t.Fatalf("create sparse large file: %v", err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("create sparse large file exit=%d stderr=%s", res.ExitCode, res.Stderr)
	}
	if _, err := d.EditFile(ctx, "/work/notes/huge.txt", "x", "y", false); err == nil ||
		!strings.Contains(err.Error(), "supports files up to") {
		t.Fatalf("expected oversized edit refusal, got %v", err)
	}

	// List the directory.
	entries, err := d.ListFiles(ctx, "/work/notes", 50)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var found bool
	for _, e := range entries {
		if e.Name == "x.txt" && !e.IsDir {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("listFiles missed x.txt; entries=%+v", entries)
	}
	limited, err := d.ListFiles(ctx, "/work/notes", 1)
	if err != nil {
		t.Fatalf("list limited: %v", err)
	}
	if len(limited) != 1 {
		t.Fatalf("limited list len = %d, want 1; entries=%+v", len(limited), limited)
	}

	// Truncation behavior.
	short, err := d.ReadFile(ctx, "/work/notes/x.txt", 5)
	if err != nil {
		t.Fatalf("read truncated: %v", err)
	}
	if !strings.HasSuffix(short, "[truncated; 5-byte cap]") {
		t.Errorf("truncated read missing suffix: %q", short)
	}
	res, err = d.Exec(ctx, []string{"sh", "-c", "printf 'hello世界world' > /work/notes/utf8.txt"}, ExecOptions{})
	if err != nil {
		t.Fatalf("create utf8 text file: %v", err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("create utf8 text file exit=%d stderr=%s", res.ExitCode, res.Stderr)
	}
	utf8Short, err := d.ReadFile(ctx, "/work/notes/utf8.txt", 7)
	if err != nil {
		t.Fatalf("read utf8 truncated: %v", err)
	}
	if !utf8.ValidString(utf8Short) {
		t.Fatalf("truncated utf8 read should stay valid UTF-8: %q", utf8Short)
	}
	res, err = d.Exec(ctx, []string{"sh", "-c", "yes x | head -c 4194305 > /work/notes/large.txt"}, ExecOptions{})
	if err != nil {
		t.Fatalf("create large text file: %v", err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("create large text file exit=%d stderr=%s", res.ExitCode, res.Stderr)
	}
	large, err := d.ReadFile(ctx, "/work/notes/large.txt", 1<<30)
	if err != nil {
		t.Fatalf("read large text file: %v", err)
	}
	if !strings.Contains(large, "[truncated; 4194304-byte cap]") {
		t.Fatalf("large read missing hard-cap truncation marker; tail=%q", large[max(0, len(large)-200):])
	}
	if strings.Contains(large, truncationMarker) {
		t.Fatalf("large read leaked executor stream-cap marker; tail=%q", large[max(0, len(large)-200):])
	}
}

// TestDockerExecFileOps_NotFound exercises the ErrNotFoundInContainer
// path so callers (and the agent) can distinguish "no such file" from
// generic exec errors.
func TestDockerExecFileOps_NotFound(t *testing.T) {
	cid, cleanup := dockerOrSkip(t)
	defer cleanup()

	d := NewDockerExecExecutor("test", cid)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if _, err := d.ReadFile(ctx, "/definitely/not/there.txt", 1024); err == nil {
		t.Fatal("ReadFile on missing path must error")
	} else if !errors.Is(err, ErrNotFoundInContainer) {
		t.Fatalf("ReadFile error should wrap ErrNotFoundInContainer, got %v", err)
	}

	if _, err := d.ListFiles(ctx, "/no/such/dir", 50); err == nil {
		t.Fatal("ListFiles on missing dir must error")
	} else if !errors.Is(err, ErrNotFoundInContainer) {
		t.Fatalf("ListFiles error should wrap ErrNotFoundInContainer, got %v", err)
	}
}

func TestDockerExecFileOpsHaveDefaultTimeout(t *testing.T) {
	prev := dockerFileOpTimeout
	dockerFileOpTimeout = 50 * time.Millisecond
	t.Cleanup(func() {
		dockerFileOpTimeout = prev
	})

	dir := t.TempDir()
	dockerPath := filepath.Join(dir, "docker")
	if err := os.WriteFile(dockerPath, []byte("#!/bin/sh\nwhile :; do :; done\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	d := NewDockerExecExecutor("test", "fake-container")
	d.DockerBinary = dockerPath
	start := time.Now()
	_, err := d.ReadFile(context.Background(), "/slow.txt", 1024)
	if err == nil || !strings.Contains(err.Error(), "deadline exceeded") {
		t.Fatalf("ReadFile error = %v, want deadline exceeded", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("ReadFile took %s; file-op timeout did not bound docker exec", elapsed)
	}
}
