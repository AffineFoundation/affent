package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestSessionPool_ToolResultArtifactsUseDurableSessionDir(t *testing.T) {
	memRoot := t.TempDir()
	cfg := Config{
		Listen:         "127.0.0.1:0",
		MaxSessions:    4,
		SessionIdleTTL: "5m",
		WorkspaceRoot:  t.TempDir(),
		MemoryRoot:     memRoot,
		BaseURL:        "http://127.0.0.1:0",
		APIKey:         "test",
		Model:          "fake",
	}
	pool, err := NewSessionPool(cfg, zerologDiscard())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Shutdown)

	s, err := pool.GetOrCreate("artifact-durable")
	if err != nil {
		t.Fatal(err)
	}
	wantPrefix := filepath.Join(memRoot, "artifact-durable", ".affent", "artifacts", "tool-results")
	if s.loop.ToolResultArtifactDir != wantPrefix {
		t.Fatalf("ToolResultArtifactDir = %q, want durable session dir %q", s.loop.ToolResultArtifactDir, wantPrefix)
	}
	if strings.HasPrefix(s.loop.ToolResultArtifactDir, s.workspace) {
		t.Fatalf("tool-result artifacts must not live under ephemeral workspace %q", s.workspace)
	}
}

func TestHandleSessionArtifacts_ListAndReadChunks(t *testing.T) {
	memRoot := t.TempDir()
	pool := artifactTestPool(t, memRoot)
	sessionID := "artifact-client"
	artifactRel := filepath.ToSlash(filepath.Join(artifactPathPrefix, "000001-c1.txt"))
	artifactPath := filepath.Join(memRoot, sessionID, filepath.FromSlash(artifactRel))
	if err := os.MkdirAll(filepath.Dir(artifactPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(artifactPath, []byte("0123456789abcdef"), 0o644); err != nil {
		t.Fatal(err)
	}

	r := httptest.NewRequest(http.MethodGet, "/v1/sessions/"+sessionID+"/artifacts", nil)
	w := httptest.NewRecorder()
	handleSessionArtifacts(pool, sessionID, "", w, r)
	if got := w.Result().StatusCode; got != http.StatusOK {
		t.Fatalf("list status = %d: %s", got, w.Body.String())
	}
	var list artifactListResponse
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v\n%s", err, w.Body.String())
	}
	if len(list.Artifacts) != 1 || list.Artifacts[0].Path != artifactRel || list.Artifacts[0].Size != 16 {
		t.Fatalf("artifact list = %+v", list.Artifacts)
	}
	if list.Artifacts[0].Preview != "0123456789abcdef" || list.Artifacts[0].PreviewTruncated {
		t.Fatalf("artifact preview = %q truncated=%v", list.Artifacts[0].Preview, list.Artifacts[0].PreviewTruncated)
	}
	if list.HasMore || list.NextAfter != "" {
		t.Fatalf("single artifact list cursor = has_more:%v next:%q, want false/empty", list.HasMore, list.NextAfter)
	}

	r = httptest.NewRequest(http.MethodGet, "/v1/sessions/"+sessionID+"/artifacts/"+artifactRel+"?offset=4&limit=6", nil)
	w = httptest.NewRecorder()
	handleSessionArtifacts(pool, sessionID, "/"+artifactRel, w, r)
	if got := w.Result().StatusCode; got != http.StatusOK {
		t.Fatalf("read status = %d: %s", got, w.Body.String())
	}
	if got := w.Body.String(); got != "456789" {
		t.Fatalf("chunk = %q, want 456789", got)
	}
	if got := w.Result().Header.Get("X-Affent-Artifact-Path"); got != artifactRel {
		t.Fatalf("artifact path header = %q, want %q", got, artifactRel)
	}
}

func TestHandleSessionArtifacts_PersistsAcrossGracefulShutdown(t *testing.T) {
	memRoot := t.TempDir()
	cfg := Config{
		Listen:         "127.0.0.1:0",
		MaxSessions:    4,
		SessionIdleTTL: "5m",
		WorkspaceRoot:  t.TempDir(),
		MemoryRoot:     memRoot,
		BaseURL:        "http://127.0.0.1:0",
		APIKey:         "test",
		Model:          "fake",
	}
	pool, err := NewSessionPool(cfg, zerologDiscard())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Shutdown)

	sessionID := "artifact-restart"
	if _, err := pool.GetOrCreate(sessionID); err != nil {
		t.Fatal(err)
	}
	artifactRel := filepath.ToSlash(filepath.Join(artifactPathPrefix, "000001-c1.txt"))
	artifactPath := filepath.Join(memRoot, sessionID, filepath.FromSlash(artifactRel))
	if err := os.MkdirAll(filepath.Dir(artifactPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(artifactPath, []byte("persisted artifact payload"), 0o644); err != nil {
		t.Fatal(err)
	}

	pool.Shutdown()

	pool2, err := NewSessionPool(cfg, zerologDiscard())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool2.Shutdown)

	r := httptest.NewRequest(http.MethodGet, "/v1/sessions/"+sessionID+"/artifacts", nil)
	w := httptest.NewRecorder()
	handleSessionArtifacts(pool2, sessionID, "", w, r)
	if got := w.Result().StatusCode; got != http.StatusOK {
		t.Fatalf("list after restart status = %d: %s", got, w.Body.String())
	}
	var list artifactListResponse
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list after restart: %v\n%s", err, w.Body.String())
	}
	if len(list.Artifacts) != 1 || list.Artifacts[0].Path != artifactRel {
		t.Fatalf("artifact list after restart = %+v", list.Artifacts)
	}

	r = httptest.NewRequest(http.MethodGet, "/v1/sessions/"+sessionID+"/artifacts/"+artifactRel, nil)
	w = httptest.NewRecorder()
	handleSessionArtifacts(pool2, sessionID, "/"+artifactRel, w, r)
	if got := w.Result().StatusCode; got != http.StatusOK {
		t.Fatalf("read after restart status = %d: %s", got, w.Body.String())
	}
	if got := w.Body.String(); got != "persisted artifact payload" {
		t.Fatalf("artifact payload after restart = %q", got)
	}
}

func TestHandleSessionArtifacts_ListPaginatesByArtifactPath(t *testing.T) {
	memRoot := t.TempDir()
	pool := artifactTestPool(t, memRoot)
	sessionID := "artifact-page"
	root := filepath.Join(memRoot, sessionID, filepath.FromSlash(artifactPathPrefix))
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"000003-c.txt", "000001-a.txt", "000002-b.txt"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	r := httptest.NewRequest(http.MethodGet, "/v1/sessions/"+sessionID+"/artifacts?limit=2", nil)
	w := httptest.NewRecorder()
	handleSessionArtifacts(pool, sessionID, "", w, r)
	var page1 artifactListResponse
	if err := json.Unmarshal(w.Body.Bytes(), &page1); err != nil {
		t.Fatalf("decode page1: %v body=%s", err, w.Body.String())
	}
	wantPage1 := []string{
		path.Join(artifactPathPrefix, "000001-a.txt"),
		path.Join(artifactPathPrefix, "000002-b.txt"),
	}
	if got := artifactPaths(page1.Artifacts); !reflect.DeepEqual(got, wantPage1) {
		t.Fatalf("page1 artifacts = %v, want %v", got, wantPage1)
	}
	if !page1.HasMore || page1.NextAfter != wantPage1[1] {
		t.Fatalf("page1 cursor = has_more:%v next:%q, want true/%q", page1.HasMore, page1.NextAfter, wantPage1[1])
	}

	r = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/v1/sessions/%s/artifacts?limit=2&after=%s", sessionID, url.QueryEscape(page1.NextAfter)), nil)
	w = httptest.NewRecorder()
	handleSessionArtifacts(pool, sessionID, "", w, r)
	var page2 artifactListResponse
	if err := json.Unmarshal(w.Body.Bytes(), &page2); err != nil {
		t.Fatalf("decode page2: %v body=%s", err, w.Body.String())
	}
	wantPage2 := []string{path.Join(artifactPathPrefix, "000003-c.txt")}
	if got := artifactPaths(page2.Artifacts); !reflect.DeepEqual(got, wantPage2) {
		t.Fatalf("page2 artifacts = %v, want %v", got, wantPage2)
	}
	if page2.HasMore || page2.NextAfter != "" {
		t.Fatalf("page2 cursor = has_more:%v next:%q, want false/empty", page2.HasMore, page2.NextAfter)
	}
}

func TestHandleSessionArtifacts_ListReturnsBoundedPreview(t *testing.T) {
	memRoot := t.TempDir()
	pool := artifactTestPool(t, memRoot)
	sessionID := "artifact-preview"
	root := filepath.Join(memRoot, sessionID, filepath.FromSlash(artifactPathPrefix))
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "first line\n\nsecond\tline " + strings.Repeat("x", artifactListPreviewBytes+128)
	if err := os.WriteFile(filepath.Join(root, "000001-large.txt"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	r := httptest.NewRequest(http.MethodGet, "/v1/sessions/"+sessionID+"/artifacts", nil)
	w := httptest.NewRecorder()
	handleSessionArtifacts(pool, sessionID, "", w, r)
	if got := w.Result().StatusCode; got != http.StatusOK {
		t.Fatalf("status = %d: %s", got, w.Body.String())
	}
	var list artifactListResponse
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v\n%s", err, w.Body.String())
	}
	if len(list.Artifacts) != 1 {
		t.Fatalf("artifacts = %+v, want one", list.Artifacts)
	}
	info := list.Artifacts[0]
	if !strings.HasPrefix(info.Preview, "first line second line") {
		t.Fatalf("preview = %q, want compact leading text", info.Preview)
	}
	if !info.PreviewTruncated {
		t.Fatalf("preview_truncated = false, want true for %d-byte artifact", len(body))
	}
	if len(info.Preview) > artifactListPreviewChars+8 {
		t.Fatalf("preview len = %d, want bounded near %d", len(info.Preview), artifactListPreviewChars)
	}
}

func TestHandleSessionArtifacts_ListRejectsBadCursor(t *testing.T) {
	memRoot := t.TempDir()
	pool := artifactTestPool(t, memRoot)
	sessionID := "artifact-bad-cursor"
	root := filepath.Join(memRoot, sessionID, filepath.FromSlash(artifactPathPrefix))
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}

	r := httptest.NewRequest(http.MethodGet, "/v1/sessions/"+sessionID+"/artifacts?after=../outside", nil)
	w := httptest.NewRecorder()
	handleSessionArtifacts(pool, sessionID, "", w, r)
	if got := w.Result().StatusCode; got != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", got, w.Body.String())
	}
}

func TestHandleSessionArtifacts_ListSkipsSymlinks(t *testing.T) {
	memRoot := t.TempDir()
	pool := artifactTestPool(t, memRoot)
	sessionID := "artifact-link-list"
	root := filepath.Join(memRoot, sessionID, filepath.FromSlash(artifactPathPrefix))
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "real.txt"), []byte("real"), 0o644); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "linked.txt")); err != nil {
		t.Skipf("symlink not available: %v", err)
	}

	r := httptest.NewRequest(http.MethodGet, "/v1/sessions/"+sessionID+"/artifacts", nil)
	w := httptest.NewRecorder()
	handleSessionArtifacts(pool, sessionID, "", w, r)
	if got := w.Result().StatusCode; got != http.StatusOK {
		t.Fatalf("status = %d: %s", got, w.Body.String())
	}
	var list artifactListResponse
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v\n%s", err, w.Body.String())
	}
	if len(list.Artifacts) != 1 || list.Artifacts[0].Path != path.Join(artifactPathPrefix, "real.txt") {
		t.Fatalf("artifact list should include only real artifact: %+v", list.Artifacts)
	}
}

func TestHandleSessionArtifacts_MissingArtifactReturns404(t *testing.T) {
	memRoot := t.TempDir()
	pool := artifactTestPool(t, memRoot)
	sessionID := "artifact-missing"
	root := filepath.Join(memRoot, sessionID, filepath.FromSlash(artifactPathPrefix))
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}

	rel := path.Join(artifactPathPrefix, "missing.txt")
	r := httptest.NewRequest(http.MethodGet, "/v1/sessions/"+sessionID+"/artifacts/"+rel, nil)
	w := httptest.NewRecorder()
	handleSessionArtifacts(pool, sessionID, "/"+rel, w, r)
	if got := w.Result().StatusCode; got != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", got, w.Body.String())
	}
}

func TestHandleSessionArtifacts_RejectsTraversalAndSymlinkEscape(t *testing.T) {
	memRoot := t.TempDir()
	pool := artifactTestPool(t, memRoot)
	sessionID := "artifact-safe"
	root := filepath.Join(memRoot, sessionID, filepath.FromSlash(artifactPathPrefix))
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link.txt")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink not available: %v", err)
	}

	for _, tc := range []struct {
		name string
		path string
	}{
		{name: "dotdot", path: "/../outside.txt"},
		{name: "wrong prefix", path: "/tmp/outside.txt"},
		{name: "symlink", path: "/" + filepath.ToSlash(filepath.Join(artifactPathPrefix, "link.txt"))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/v1/sessions/"+sessionID+"/artifacts"+tc.path, nil)
			w := httptest.NewRecorder()
			handleSessionArtifacts(pool, sessionID, tc.path, w, r)
			if got := w.Result().StatusCode; got != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", got, w.Body.String())
			}
		})
	}
}

func TestHandleSessionArtifacts_RejectsSymlinkInsideArtifactRoot(t *testing.T) {
	memRoot := t.TempDir()
	pool := artifactTestPool(t, memRoot)
	sessionID := "artifact-link-inside"
	root := filepath.Join(memRoot, sessionID, filepath.FromSlash(artifactPathPrefix))
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "real.txt"), []byte("real"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "real.txt"), filepath.Join(root, "alias.txt")); err != nil {
		t.Skipf("symlink not available: %v", err)
	}

	rel := path.Join(artifactPathPrefix, "alias.txt")
	r := httptest.NewRequest(http.MethodGet, "/v1/sessions/"+sessionID+"/artifacts/"+rel, nil)
	w := httptest.NewRecorder()
	handleSessionArtifacts(pool, sessionID, "/"+rel, w, r)
	if got := w.Result().StatusCode; got != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", got, w.Body.String())
	}
}

func artifactPaths(in []artifactInfo) []string {
	out := make([]string, len(in))
	for i, a := range in {
		out[i] = a.Path
	}
	return out
}

func artifactTestPool(t *testing.T, memRoot string) *SessionPool {
	t.Helper()
	cfg := Config{
		Listen:         "127.0.0.1:0",
		MaxSessions:    4,
		SessionIdleTTL: "5m",
		WorkspaceRoot:  t.TempDir(),
		MemoryRoot:     memRoot,
		BaseURL:        "http://127.0.0.1:0",
		APIKey:         "test",
		Model:          "fake",
	}
	pool, err := NewSessionPool(cfg, zerologDiscard())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Shutdown)
	return pool
}
