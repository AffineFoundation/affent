package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHandleSessionFilesListsAndReadsWorkspace(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	sess, err := pool.GetOrCreate("files-workspace")
	if err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(sess.workspace, "src"), 0o755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sess.workspace, "src", "main.go"), []byte("package main\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatalf("write main: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sess.workspace, "README.md"), []byte("workspace readme\n"), 0o644); err != nil {
		t.Fatalf("write readme: %v", err)
	}

	r := httptest.NewRequest(http.MethodGet, "/v1/sessions/files-workspace/files", nil)
	w := httptest.NewRecorder()
	handleSessionFiles(pool, "files-workspace", w, r)

	if got := w.Result().StatusCode; got != http.StatusOK {
		t.Fatalf("list status = %d, want 200; body=%s", got, w.Body.String())
	}
	var list sessionFileResponse
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v body=%s", err, w.Body.String())
	}
	if list.Kind != "directory" || list.Path != "." {
		t.Fatalf("list response = %+v, want root directory", list)
	}
	if !fileEntryExists(list.Entries, "src", "directory") || !fileEntryExists(list.Entries, "README.md", "file") {
		t.Fatalf("entries = %+v, want src dir and README.md", list.Entries)
	}

	r = httptest.NewRequest(http.MethodGet, "/v1/sessions/files-workspace/files?path=src/main.go&limit=12", nil)
	w = httptest.NewRecorder()
	handleSessionFiles(pool, "files-workspace", w, r)
	if got := w.Result().StatusCode; got != http.StatusOK {
		t.Fatalf("read status = %d, want 200; body=%s", got, w.Body.String())
	}
	var file sessionFileResponse
	if err := json.Unmarshal(w.Body.Bytes(), &file); err != nil {
		t.Fatalf("decode file: %v body=%s", err, w.Body.String())
	}
	if file.Kind != "file" || file.Path != "src/main.go" || file.Text != "package main" || !file.HasMore {
		t.Fatalf("file response = %+v", file)
	}
}

func TestHandleSessionFilesUploadsWorkspaceFile(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	sess, err := pool.GetOrCreate("files-upload")
	if err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}

	r := httptest.NewRequest(http.MethodPost, "/v1/sessions/files-upload/files", strings.NewReader(`{"path":"src/upload.txt","text":"hello upload\n"}`))
	w := httptest.NewRecorder()
	handleSessionFiles(pool, "files-upload", w, r)

	if got := w.Result().StatusCode; got != http.StatusOK {
		t.Fatalf("upload status = %d, want 200; body=%s", got, w.Body.String())
	}
	var file sessionFileResponse
	if err := json.Unmarshal(w.Body.Bytes(), &file); err != nil {
		t.Fatalf("decode file: %v body=%s", err, w.Body.String())
	}
	if file.Kind != "file" || file.Path != "src/upload.txt" || file.Text != "hello upload\n" {
		t.Fatalf("file response = %+v", file)
	}
	got, err := os.ReadFile(filepath.Join(sess.workspace, "src", "upload.txt"))
	if err != nil {
		t.Fatalf("read uploaded file: %v", err)
	}
	if string(got) != "hello upload\n" {
		t.Fatalf("uploaded file = %q", got)
	}
}

func TestHandleSessionFilesRejectsUnsafeUploads(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	sess, err := pool.GetOrCreate("files-upload-guard")
	if err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(sess.workspace, "safe"), 0o755); err != nil {
		t.Fatalf("mkdir safe: %v", err)
	}
	if err := os.Symlink("/etc/passwd", filepath.Join(sess.workspace, "passwd-link")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if err := os.Symlink("/tmp", filepath.Join(sess.workspace, "tmp-link")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	for _, tc := range []struct {
		name string
		body string
		want string
	}{
		{name: "traversal", body: `{"path":"../outside.txt","text":"x"}`, want: "escapes workspace"},
		{name: "directory", body: `{"path":".","text":"x"}`, want: "must be a file"},
		{name: "symlink leaf", body: `{"path":"passwd-link","text":"x"}`, want: "symlink"},
		{name: "symlink parent", body: `{"path":"tmp-link/upload.txt","text":"x"}`, want: "symlink"},
		{name: "unknown field", body: `{"path":"safe/upload.txt","text":"x","mode":"append"}`, want: "unknown field"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/v1/sessions/files-upload-guard/files", strings.NewReader(tc.body))
			w := httptest.NewRecorder()
			handleSessionFiles(pool, "files-upload-guard", w, r)
			if got := w.Result().StatusCode; got != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", got, w.Body.String())
			}
			if !strings.Contains(w.Body.String(), tc.want) {
				t.Fatalf("body = %s, want %q", w.Body.String(), tc.want)
			}
		})
	}
}

func TestHandleSessionFilesRejectsTraversalAndSymlink(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	sess, err := pool.GetOrCreate("files-guard")
	if err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	if err := os.Symlink("/etc/passwd", filepath.Join(sess.workspace, "passwd-link")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	for _, tc := range []struct {
		name string
		path string
		want string
	}{
		{name: "traversal", path: "../etc/passwd", want: "escapes workspace"},
		{name: "symlink", path: "passwd-link", want: "symlink"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/v1/sessions/files-guard/files?path="+tc.path, nil)
			w := httptest.NewRecorder()
			handleSessionFiles(pool, "files-guard", w, r)
			if got := w.Result().StatusCode; got != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", got, w.Body.String())
			}
			if !strings.Contains(w.Body.String(), tc.want) {
				t.Fatalf("body = %s, want %q", w.Body.String(), tc.want)
			}
		})
	}
}

func fileEntryExists(entries []sessionFileEntry, name string, kind string) bool {
	for _, entry := range entries {
		if entry.Name == name && entry.Kind == kind {
			return true
		}
	}
	return false
}
