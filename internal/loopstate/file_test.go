package loopstate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProtocolPathUsesPerSessionLoopDir(t *testing.T) {
	dir := t.TempDir()
	got := ProtocolPath(dir, "market-run")
	want := filepath.Join(dir, ".affent", "loops", "market-run", "LOOP.md")
	if got != want {
		t.Fatalf("ProtocolPath = %q, want %q", got, want)
	}
	if rel := ProtocolRelPath("market-run"); rel != ".affent/loops/market-run/LOOP.md" {
		t.Fatalf("ProtocolRelPath = %q", rel)
	}
}

func TestReadProtocolRejectsSymlinkAndOversize(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(dir, "outside.md")
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "LOOP.md")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReadProtocol(link); err == nil || !strings.Contains(err.Error(), "must not be a symlink") {
		t.Fatalf("ReadProtocol symlink err = %v", err)
	}

	oversize := filepath.Join(dir, "oversize.md")
	if err := os.WriteFile(oversize, []byte(strings.Repeat("x", MaxProtocolBytes+1)), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReadProtocol(oversize); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("ReadProtocol oversize err = %v", err)
	}
}

func TestSummarizeFileExtractsMetadata(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "LOOP.md")
	content := `# Loop Protocol: market

## 0. Metadata

- loop_id: market-run
- owner_session: sess-market
- status: running

## 1. North Star

Keep market evidence cited.`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	got, found, err := SummarizeFile(path, ".affent/loops/market-run/LOOP.md")
	if err != nil {
		t.Fatalf("SummarizeFile: %v", err)
	}
	if !found {
		t.Fatal("expected summary")
	}
	if got.Path != ".affent/loops/market-run/LOOP.md" ||
		got.LoopID != "market-run" ||
		got.OwnerSession != "sess-market" ||
		got.Status != "running" ||
		got.UpdatedAt == "" ||
		got.Bytes != len([]byte(content)) ||
		!strings.Contains(got.Preview, "Keep market evidence cited.") {
		t.Fatalf("summary = %+v", got)
	}
}
