package executor

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCappingWriter_UnderCapKeepsBytesVerbatim(t *testing.T) {
	w := newCappingWriter(1024)
	n, err := w.Write([]byte("hello world"))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if n != 11 {
		t.Fatalf("Write n = %d, want 11", n)
	}
	if got := w.String(); got != "hello world" {
		t.Fatalf("String = %q, want %q", got, "hello world")
	}
}

func TestCappingWriter_OverCapTruncatesAndCountsDropped(t *testing.T) {
	w := newCappingWriter(8)
	// First chunk fits exactly.
	if _, err := w.Write([]byte("12345678")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	// Second chunk: every byte must be reported written (so the child
	// process doesn't block) but discarded internally.
	n, err := w.Write([]byte("ABCDEFGHIJ"))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if n != 10 {
		t.Fatalf("Write n = %d, want 10 (must report full len to avoid blocking caller)", n)
	}
	out := w.String()
	if !strings.HasPrefix(out, "12345678") {
		t.Fatalf("output should start with the buffered prefix, got %q", out)
	}
	if !strings.Contains(out, "10 more bytes truncated") {
		t.Fatalf("truncation marker should report dropped byte count, got %q", out)
	}
}

func TestCappingWriter_PartialFillThenOverflow(t *testing.T) {
	w := newCappingWriter(10)
	// Single write that fills the cap and overflows.
	in := []byte("AAAAA-BBBBB-CCCCC") // 17 bytes; cap=10 → 10 buffered, 7 dropped
	n, err := w.Write(in)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if n != 17 {
		t.Fatalf("Write n = %d, want 17", n)
	}
	out := w.String()
	if !strings.HasPrefix(out, "AAAAA-BBBB") {
		t.Fatalf("first 10 bytes should be buffered, got prefix %q", out[:min(10, len(out))])
	}
	if !strings.Contains(out, "7 more bytes truncated") {
		t.Fatalf("expected '7 more bytes truncated' marker, got %q", out)
	}
}

func TestCappingWriter_ZeroCapFallsBackToDefault(t *testing.T) {
	w := newCappingWriter(0)
	if w.cap != DefaultExecOutputCap {
		t.Fatalf("zero cap should fall back to DefaultExecOutputCap (%d), got %d",
			DefaultExecOutputCap, w.cap)
	}
}

// TestLocalExecutor_TimeoutSurfacesPartialOutput pins that a killed-
// by-timeout command still returns the bytes it managed to write
// before the kill. Pre-fix the executor returned ExecResult{} on any
// non-ExitError and dropped the captured stdout, so the LLM saw a
// bare "context deadline exceeded" with no clue how far the command
// got.
//
// The shell command uses `exec sleep` so the PID of the spawned sh
// is replaced by sleep — without that, SIGKILL would target sh while
// sleep (its child) kept the inherited stdout pipe open, and Wait()
// would block until the orphan finished even though Cancel had fired.
func TestLocalExecutor_TimeoutSurfacesPartialOutput(t *testing.T) {
	e := NewLocalExecutor("test", "")
	res, err := e.Exec(context.Background(), []string{"sh", "-c", "echo TIMEOUT-MARKER; exec sleep 2"}, ExecOptions{
		Timeout: 200 * time.Millisecond,
	})
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if !strings.Contains(res.Stdout, "TIMEOUT-MARKER") {
		t.Fatalf("partial stdout lost on timeout; got %q", res.Stdout)
	}
	if res.ExitCode != -1 {
		t.Errorf("ExitCode = %d, want -1 (abnormal exit marker)", res.ExitCode)
	}
}

// pathFromEnv returns the PATH value from a slice of KEY=VALUE env
// entries, or "" if PATH isn't present.
func pathFromEnv(env []string) string {
	for _, kv := range env {
		if strings.HasPrefix(kv, "PATH=") {
			return strings.TrimPrefix(kv, "PATH=")
		}
	}
	return ""
}

func TestLocalExecutor_DefaultExtraPathDirsAppended(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	e := NewLocalExecutor("test", "")
	// nil ExtraPathDirs → DefaultExtraPathDirs() applies.
	got := pathFromEnv(e.augmentPath([]string{"PATH=/bin"}))
	for _, want := range []string{
		"/bin",
		filepath.Join(home, ".local", "go-toolchain", "go", "bin"),
		filepath.Join(home, ".local", "bin"),
		filepath.Join(home, "go", "bin"),
		"/usr/local/go/bin",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("PATH missing %q:\n%s", want, got)
		}
	}
}

// TestLocalExecutor_ExtraPathDirsOverride pins the data-driven knob.
// Operators that want a different candidate set (or want to remove
// the default heuristic for a minimal-container deploy) set
// ExtraPathDirs directly; nil → defaults; empty slice → augmentation
// disabled.
func TestLocalExecutor_ExtraPathDirsOverride(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	t.Run("explicit non-empty replaces defaults", func(t *testing.T) {
		e := NewLocalExecutor("test", "")
		e.ExtraPathDirs = []string{"/custom/toolchain/bin", "/other/bin"}
		got := pathFromEnv(e.augmentPath([]string{"PATH=/bin"}))
		// Custom dirs land.
		for _, want := range []string{"/custom/toolchain/bin", "/other/bin"} {
			if !strings.Contains(got, want) {
				t.Errorf("explicit ExtraPathDirs missing %q in PATH:\n%s", want, got)
			}
		}
		// Default dirs (~/.local/go-toolchain/go/bin etc.) MUST NOT
		// appear — operator-specified set replaces them, doesn't merge.
		if strings.Contains(got, filepath.Join(home, ".local", "go-toolchain")) {
			t.Errorf("default toolchain path leaked despite explicit override:\n%s", got)
		}
	})

	t.Run("non-nil empty disables augmentation", func(t *testing.T) {
		e := NewLocalExecutor("test", "")
		e.ExtraPathDirs = []string{} // not nil — explicit "no augmentation"
		got := pathFromEnv(e.augmentPath([]string{"PATH=/bin"}))
		if got != "/bin" {
			t.Errorf("empty ExtraPathDirs should leave PATH untouched; got %q", got)
		}
	})
}

// TestLocalExecutor_ExtraPathDirsNoDuplicates pins that PATH segments
// already present aren't re-appended. Without this, calling Exec
// repeatedly on a session that inherits a previously-augmented PATH
// would grow PATH on every call (each round adds the same default
// candidates again).
func TestLocalExecutor_ExtraPathDirsNoDuplicates(t *testing.T) {
	e := NewLocalExecutor("test", "")
	e.ExtraPathDirs = []string{"/a/bin", "/b/bin"}
	first := pathFromEnv(e.augmentPath([]string{"PATH=/bin"}))
	second := pathFromEnv(e.augmentPath([]string{"PATH=" + first}))
	if first != second {
		t.Errorf("augmenting an already-augmented PATH should be idempotent:\nfirst:  %q\nsecond: %q", first, second)
	}
}

// TestCappingWriter_BannerContainsTruncationMarker locks the
// producer/detector contract: cappingWriter.String()'s truncated
// banner must contain the truncationMarker substring so callers
// like docker.go's readFileFull can detect truncation reliably.
// Refactoring the banner wording without preserving this marker
// would silently re-enable the EditFile data-corruption bug it
// guards against.
func TestCappingWriter_BannerContainsTruncationMarker(t *testing.T) {
	w := newCappingWriter(4)
	_, _ = w.Write([]byte("more-than-four"))
	out := w.String()
	if !strings.Contains(out, truncationMarker) {
		t.Fatalf("banner missing truncationMarker %q: %q", truncationMarker, out)
	}
}
