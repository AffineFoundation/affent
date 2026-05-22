package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestHandleModels_ReportsConfiguredModel pins the /v1/models
// response shape. OpenAI SDKs round-trip this endpoint to discover
// the available model id; the response must list exactly one model
// with stable fields (id, object, created, owned_by).
func TestHandleModels_ReportsConfiguredModel(t *testing.T) {
	cfg := Config{Model: "qwen3.6-35b"}
	h := handleModels(cfg)
	r := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	w := httptest.NewRecorder()
	h(w, r)

	if got := w.Result().StatusCode; got != 200 {
		t.Fatalf("status = %d, want 200", got)
	}
	if got := w.Result().Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("content-type = %q, want application/json", got)
	}

	var resp struct {
		Object string `json:"object"`
		Data   []struct {
			ID      string `json:"id"`
			Object  string `json:"object"`
			OwnedBy string `json:"owned_by"`
			Created int64  `json:"created"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v\n%s", err, w.Body.String())
	}
	if resp.Object != "list" {
		t.Errorf("object = %q, want list", resp.Object)
	}
	if len(resp.Data) != 1 {
		t.Fatalf("expected exactly one model, got %d", len(resp.Data))
	}
	if resp.Data[0].ID != "qwen3.6-35b" {
		t.Errorf("model id = %q, want qwen3.6-35b", resp.Data[0].ID)
	}
	if resp.Data[0].OwnedBy != "affent" {
		t.Errorf("owned_by = %q, want affent", resp.Data[0].OwnedBy)
	}
	if resp.Data[0].Object != "model" {
		t.Errorf("inner object = %q, want model", resp.Data[0].Object)
	}
}

// TestHandleModels_FallsBackToDefault pins the empty-Model
// behavior. /v1/models still responds with one model (id "default")
// so naive clients calling it during startup don't get a confusing
// empty list.
func TestHandleModels_FallsBackToDefault(t *testing.T) {
	h := handleModels(Config{}) // no Model set
	r := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	w := httptest.NewRecorder()
	h(w, r)

	var resp struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if len(resp.Data) != 1 || resp.Data[0].ID != "default" {
		t.Errorf("expected single 'default' model on empty cfg; got %+v", resp.Data)
	}
}

// TestHandleSessionDelete_AlwaysReturns204 pins the idempotent
// contract: DELETE on a known or unknown session id returns 204
// either way. The handler doc says "clients calling 'make sure
// this session is gone' don't need a 404", so we lock that in.
func TestHandleSessionDelete_AlwaysReturns204(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	// One known session, one unknown id.
	if _, err := pool.GetOrCreate("known"); err != nil {
		t.Fatal(err)
	}

	for _, id := range []string{"known", "never-existed"} {
		r := httptest.NewRequest(http.MethodDelete, "/v1/sessions/"+id, nil)
		w := httptest.NewRecorder()
		handleSessionDelete(pool, id, w, r)
		if got := w.Result().StatusCode; got != http.StatusNoContent {
			t.Errorf("DELETE %s status = %d, want 204", id, got)
		}
	}
	// The known session should now be gone.
	if _, err := pool.Get("known"); err == nil {
		t.Errorf("after DELETE, known session must be evicted from pool")
	}
}

// TestResolveSystemPromptFlag_Literal pins the most common path:
// anything that isn't "-" or "@<path>" is treated as a literal
// prompt. Quoting / escaping is the shell's job, not ours.
func TestResolveSystemPromptFlag_Literal(t *testing.T) {
	got, err := resolveSystemPromptFlag("you are helpful")
	if err != nil {
		t.Fatal(err)
	}
	if got != "you are helpful" {
		t.Errorf("got %q, want literal", got)
	}
}

// TestResolveSystemPromptFlag_AtFile pins the @path form: read the
// file's content verbatim. Errors include the path so a typo is
// obvious in the operator's log.
func TestResolveSystemPromptFlag_AtFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "prompt.txt")
	if err := os.WriteFile(path, []byte("from file\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := resolveSystemPromptFlag("@" + path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "from file") {
		t.Errorf("got %q, want content from file", got)
	}
}

// TestResolveSystemPromptFlag_AtMissingFile pins the error path:
// "@missing.txt" must error so the operator sees the typo, not
// silently fall through to the literal string "@missing.txt".
func TestResolveSystemPromptFlag_AtMissingFile(t *testing.T) {
	_, err := resolveSystemPromptFlag("@/no/such/file")
	if err == nil {
		t.Error("@<missing-file> should error, not fall through to literal")
	}
}

// TestResolveSystemPromptFlag_Stdin pins the "-" path. Driving
// stdin in a test is fiddly; we replace os.Stdin with a pipe and
// write the canned content from a goroutine. The bug this guards
// against: silently returning "" when stdin is empty would let a
// misconfigured invocation drop the system prompt entirely.
func TestResolveSystemPromptFlag_Stdin(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = orig })

	go func() {
		_, _ = io.WriteString(w, "stdin prompt\n")
		_ = w.Close()
	}()

	got, err := resolveSystemPromptFlag("-")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "stdin prompt") {
		t.Errorf("got %q, want content from stdin", got)
	}
}
