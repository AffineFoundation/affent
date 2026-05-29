package sessionstate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMetadataRoundTrip(t *testing.T) {
	dir := t.TempDir()
	want := Metadata{
		SessionID:     "sess_meta",
		WorkspaceRoot: "/workspace",
		WorkspacePath: "/workspace/project",
	}
	if err := WriteMetadata(dir, want); err != nil {
		t.Fatalf("WriteMetadata: %v", err)
	}
	got, found, err := ReadMetadata(dir)
	if err != nil {
		t.Fatalf("ReadMetadata: %v", err)
	}
	if !found {
		t.Fatal("ReadMetadata found=false")
	}
	if got.SchemaVersion != MetadataSchemaVersion ||
		got.SessionID != want.SessionID ||
		got.WorkspaceRoot != want.WorkspaceRoot ||
		got.WorkspacePath != want.WorkspacePath ||
		strings.TrimSpace(got.UpdatedAt) == "" {
		t.Fatalf("metadata = %+v, want session/workspace round trip", got)
	}
}

func TestReadMetadataRejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.json")
	if err := os.WriteFile(outside, []byte(`{"schema_version":1}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, MetadataFileName)); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	_, found, err := ReadMetadata(dir)
	if !found || err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("ReadMetadata symlink found=%v err=%v, want symlink error", found, err)
	}
}
