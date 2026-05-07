package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFetchTool_HTML(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(`
<html><head>
<style>.hidden{display:none}</style>
<script>alert('chrome')</script>
</head><body>
<nav>chrome navigation we should drop</nav>
<h1>Hello, agent</h1>
<p>This page has a <a href="/docs">link</a> to docs.</p>
<pre><code>print("code block")</code></pre>
<footer>also dropped</footer>
</body></html>`))
	}))
	defer srv.Close()

	tool := FetchTool(FetchConfig{})
	args, _ := json.Marshal(map[string]string{"url": srv.URL})
	out, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	wantContains := []string{
		"# Hello, agent",        // h1 rendered
		"link",                  // anchor text preserved
		srv.URL + "/docs",       // href resolved against base URL
		"```",                   // pre block fenced
		`print("code block")`,
	}
	for _, s := range wantContains {
		if !strings.Contains(out, s) {
			t.Errorf("output missing %q\n----\n%s", s, out)
		}
	}
	dropped := []string{"chrome navigation", "also dropped", "alert('chrome')"}
	for _, s := range dropped {
		if strings.Contains(out, s) {
			t.Errorf("output should have dropped %q\n----\n%s", s, out)
		}
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

func TestSearchTool_EmptyQuery(t *testing.T) {
	tool, _ := SearchTool(SearchConfig{Provider: stubProvider{}})
	_, err := tool.Execute(context.Background(), json.RawMessage(`{"query":""}`))
	if err == nil || !strings.Contains(err.Error(), "query is required") {
		t.Errorf("expected query-required error, got %v", err)
	}
}
