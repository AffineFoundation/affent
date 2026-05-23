package planstate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadFileValidatesAndTrimsPlanJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plan.json")
	if err := os.WriteFile(path, []byte("\n\t {\"steps\":[]} \n"), 0o644); err != nil {
		t.Fatal(err)
	}

	raw, found, err := ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("ReadFile found=false, want true")
	}
	if string(raw) != `{"steps":[]}` {
		t.Fatalf("raw = %q, want trimmed JSON", raw)
	}
}

func TestReadFileBoundsPlanSize(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plan.json")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", MaxFileBytes+1)), 0o644); err != nil {
		t.Fatal(err)
	}

	if raw, found, err := ReadFile(path); err == nil || found || raw != nil {
		t.Fatalf("ReadFile oversized = raw %q found %v err %v, want error", raw, found, err)
	}
}

func TestReadFileRejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.json")
	if err := os.WriteFile(target, []byte(`{"steps":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "plan.json")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	if _, _, err := ReadFile(link); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("ReadFile symlink err = %v, want symlink error", err)
	}
}

func TestRemoveFileRejectsSymlinkAndRemovesRegularFile(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.json")
	if err := os.WriteFile(target, []byte(`{"steps":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "linked.json")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if removed, err := RemoveFile(link); err == nil || removed || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("RemoveFile symlink = removed %v err %v, want symlink error", removed, err)
	}
	if _, err := os.Lstat(target); err != nil {
		t.Fatalf("target should remain after rejected symlink remove: %v", err)
	}

	removed, err := RemoveFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !removed {
		t.Fatal("RemoveFile removed=false, want true")
	}
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Fatalf("target still exists after RemoveFile: %v", err)
	}
}
