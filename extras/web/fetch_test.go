package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	agent "github.com/affinefoundation/affent/internal/agent"
)

func TestFetchTool_HTML(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(`
<html><head>
<script>alert('chrome')</script>
</head><body>
<h1>Hello, agent</h1>
<p>This page has a <a href="/docs">link</a> to docs.</p>
<pre><code>print("code block")</code></pre>
</body></html>`))
	}))
	defer srv.Close()

	tool := FetchTool(FetchConfig{})
	args, _ := json.Marshal(map[string]string{"url": srv.URL})
	out, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	// Core markdown features round-trip through the
	// Readability + html-to-markdown pipeline. We don't pin specific
	// h-levels or whitespace because that depends on readability's
	// scoring; only the load-bearing facts are checked.
	wantContains := []string{
		"Hello, agent",          // heading text preserved
		"[link]",                // anchor rendered as markdown link
		srv.URL + "/docs",       // href resolved against base URL
		"```",                   // pre block fenced
		`print("code block")`,
	}
	for _, s := range wantContains {
		if !strings.Contains(out, s) {
			t.Errorf("output missing %q\n----\n%s", s, out)
		}
	}
	// <script> contents must always go (basic safety; both readability
	// and html-to-markdown drop these — a regression here would mean
	// we accidentally bypassed both).
	if strings.Contains(out, "alert('chrome')") {
		t.Errorf("script contents leaked into output:\n%s", out)
	}
}

func TestFetchTool_PlainText(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("just some plain text"))
	}))
	defer srv.Close()
	tool := FetchTool(FetchConfig{})
	args, _ := json.Marshal(map[string]string{"url": srv.URL})
	out, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out != "just some plain text" {
		t.Errorf("expected plain text passthrough, got %q", out)
	}
}

func TestFetchTool_NonText(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write([]byte{0x89, 'P', 'N', 'G', 0, 1, 2, 3})
	}))
	defer srv.Close()
	tool := FetchTool(FetchConfig{})
	args, _ := json.Marshal(map[string]string{"url": srv.URL})
	out, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "non-text response") {
		t.Errorf("expected non-text placeholder, got %q", out)
	}
}

func TestFetchTool_RequiresURL(t *testing.T) {
	tool := FetchTool(FetchConfig{})
	_, err := tool.Execute(context.Background(), json.RawMessage(`{}`))
	if err == nil || !strings.Contains(err.Error(), "url is required") {
		t.Errorf("expected url-required error, got %v", err)
	}
	_, err = tool.Execute(context.Background(), json.RawMessage(`{"url":"ftp://x"}`))
	if err == nil || !strings.Contains(err.Error(), "http://") {
		t.Errorf("expected scheme guard error, got %v", err)
	}
}

func TestFetchTool_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusForbidden)
	}))
	defer srv.Close()
	tool := FetchTool(FetchConfig{})
	args, _ := json.Marshal(map[string]string{"url": srv.URL})
	_, err := tool.Execute(context.Background(), args)
	if err == nil || !strings.Contains(err.Error(), "http 403") {
		t.Errorf("expected 403 surface, got %v", err)
	}
}

// stubProvider lets us test SearchTool without a real backend.
type stubProvider struct{ results []SearchResult }

func (s stubProvider) Search(_ context.Context, _ string, _ int) ([]SearchResult, error) {
	return s.results, nil
}

func TestSearchTool_FormatsResults(t *testing.T) {
	tool, err := SearchTool(SearchConfig{
		Provider: stubProvider{results: []SearchResult{
			{Title: "First", URL: "https://example.com/a", Snippet: "snippet A"},
			{Title: "Second", URL: "https://example.com/b", Snippet: "snippet B"},
		}},
	})
	if err != nil {
		t.Fatalf("SearchTool: %v", err)
	}
	args, _ := json.Marshal(map[string]any{"query": "anything"})
	out, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	for _, want := range []string{"1. First", "https://example.com/a", "snippet A", "2. Second"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

// TestFetchTool_UTF8SafeTruncation pins that the MaxResultChars cap
// snaps back to a rune boundary instead of slicing mid-rune. Pre-fix
// the byte slice produced invalid UTF-8 (orphaned continuation
// bytes) which most providers either drop or render as U+FFFD.
func TestFetchTool_UTF8SafeTruncation(t *testing.T) {
	// 1000 Cyrillic ё's = 2000 bytes. Each rune is exactly 2 bytes,
	// so capping at an odd byte offset deliberately lands inside a
	// rune.
	body := strings.Repeat("ё", 1000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write([]byte(body))
	}))
	defer srv.Close()

	tool := FetchTool(FetchConfig{MaxResultChars: 51}) // odd → mid-rune
	args, _ := json.Marshal(map[string]string{"url": srv.URL})
	out, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	prefix := strings.SplitN(out, "\n\n...(truncated)", 2)[0]
	for i, r := range prefix {
		if r == '�' {
			t.Fatalf("truncation produced invalid UTF-8 at byte %d (U+FFFD)\nprefix=%q", i, prefix)
		}
	}
}

func TestSearchTool_EmptyQuery(t *testing.T) {
	tool, _ := SearchTool(SearchConfig{Provider: stubProvider{}})
	_, err := tool.Execute(context.Background(), json.RawMessage(`{"query":""}`))
	if err == nil || !strings.Contains(err.Error(), "query is required") {
		t.Errorf("expected query-required error, got %v", err)
	}
}

// failingProvider always returns an error, so SearchTool construction
// succeeds but RegisterAll fails to wire it in. Used to test that the
// rollback path strips the already-registered web_fetch.
type failingProvider struct{}

func (failingProvider) Search(context.Context, string, int) ([]SearchResult, error) {
	return nil, errors.New("intentional test failure")
}

// TestRegisterAll_RollsBackWebFetchOnSearchFailure pins the
// partial-failure contract: if any tool RegisterAll meant to add
// can't be wired up, every tool it already added is removed before
// returning. Previously, RegisterAll left web_fetch dangling in the
// registry after a missing-Tavily-key failure.
func TestRegisterAll_RollsBackWebFetchOnSearchFailure(t *testing.T) {
	reg := agent.NewRegistry()
	// SearchConfig{Provider: nil} causes SearchTool() inside
	// RegisterAll to return an error after RegisterFetch has already
	// added web_fetch.
	err := RegisterAll(reg, Options{
		SearchProvider: nil,
		// Force the Tavily branch (provider == nil + SkipSearch false).
		// Setting TAVILY_API_KEY isn't available in the unit test env;
		// NewTavilyProvider returns an error and we exercise the
		// rollback path.
	})
	if err == nil {
		t.Skip("expected RegisterAll to fail without TAVILY_API_KEY; env appears to have one set")
	}
	if _, ok := reg.Get("web_fetch"); ok {
		t.Errorf("RegisterAll failure must roll web_fetch back out of the registry")
	}
	if _, ok := reg.Get("web_search"); ok {
		t.Errorf("web_search should never have been registered when setup failed")
	}
}
