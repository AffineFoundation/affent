package executor

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"
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

	// Truncation behavior.
	short, err := d.ReadFile(ctx, "/work/notes/x.txt", 5)
	if err != nil {
		t.Fatalf("read truncated: %v", err)
	}
	if !strings.HasSuffix(short, "[truncated; 5-byte cap]") {
		t.Errorf("truncated read missing suffix: %q", short)
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
