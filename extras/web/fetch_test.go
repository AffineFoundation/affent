package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	agent "github.com/affinefoundation/affent/internal/agent"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

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

	tool := FetchTool(FetchConfig{AllowPrivateNetwork: true})
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
		"Hello, agent",    // heading text preserved
		"[link]",          // anchor rendered as markdown link
		srv.URL + "/docs", // href resolved against base URL
		"```",             // pre block fenced
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
	tool := FetchTool(FetchConfig{AllowPrivateNetwork: true})
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
	tool := FetchTool(FetchConfig{AllowPrivateNetwork: true})
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
	tool := FetchTool(FetchConfig{AllowPrivateNetwork: true})
	_, err := tool.Execute(context.Background(), json.RawMessage(`{}`))
	if err == nil || !strings.Contains(err.Error(), "url is required") || !strings.Contains(err.Error(), "Next:") {
		t.Errorf("expected url-required error, got %v", err)
	}
	_, err = tool.Execute(context.Background(), json.RawMessage(`{"url":"   "}`))
	if err == nil || !strings.Contains(err.Error(), "url is required") || !strings.Contains(err.Error(), "Next:") {
		t.Errorf("expected blank-url required error, got %v", err)
	}
	_, err = tool.Execute(context.Background(), json.RawMessage(`{"url":"ftp://x"}`))
	if err == nil || !strings.Contains(err.Error(), "http://") || !strings.Contains(err.Error(), "Next:") {
		t.Errorf("expected scheme guard error, got %v", err)
	}
}

func TestFetchToolSchemaPublishesURLMinLength(t *testing.T) {
	tool := FetchTool(FetchConfig{AllowPrivateNetwork: true})
	if !strings.Contains(string(tool.Schema), `"additionalProperties": false`) {
		t.Fatalf("schema should reject unknown args: %s", tool.Schema)
	}
	if !strings.Contains(string(tool.Schema), `"minLength": 1`) {
		t.Fatalf("schema should publish url minLength: %s", tool.Schema)
	}
	if !strings.Contains(string(tool.Schema), `"maxLength": 4096`) {
		t.Fatalf("schema should publish url maxLength: %s", tool.Schema)
	}
}

func TestFetchTool_URLMaxLength(t *testing.T) {
	called := false
	tool := FetchTool(FetchConfig{
		HTTP: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			called = true
			return &http.Response{
				StatusCode: 200,
				Status:     "200 OK",
				Header:     http.Header{"Content-Type": []string{"text/plain"}},
				Body:       io.NopCloser(strings.NewReader("ok")),
				Request:    req,
			}, nil
		})},
	})
	url := "https://example.com/" + strings.Repeat("x", maxFetchURLBytes-len("https://example.com/"))
	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"url":"`+url+`"}`)); err != nil {
		t.Fatalf("max-size URL should pass validation and reach HTTP client: %v", err)
	}
	if !called {
		t.Fatal("max-size URL did not reach HTTP client")
	}

	url += "x"
	_, err := tool.Execute(context.Background(), json.RawMessage(`{"url":"`+url+`"}`))
	if err == nil || !strings.Contains(err.Error(), "web_fetch supports URLs up to") || !strings.Contains(err.Error(), "Next:") {
		t.Fatalf("expected oversized URL error, got %v", err)
	}
}

func TestFetchToolRejectsUnknownArgs(t *testing.T) {
	tool := FetchTool(FetchConfig{AllowPrivateNetwork: true})
	_, err := tool.Execute(context.Background(), json.RawMessage(`{"url":"https://example.com","query":"ignored"}`))
	if err == nil ||
		!strings.Contains(err.Error(), "unknown field") ||
		!strings.Contains(err.Error(), "query") ||
		!strings.Contains(err.Error(), "Next:") {
		t.Fatalf("unknown arg error = %v", err)
	}
}

func TestFetchTool_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusForbidden)
	}))
	defer srv.Close()
	tool := FetchTool(FetchConfig{AllowPrivateNetwork: true})
	args, _ := json.Marshal(map[string]string{"url": srv.URL})
	_, err := tool.Execute(context.Background(), args)
	if err == nil || !strings.Contains(err.Error(), "http 403") {
		t.Errorf("expected 403 surface, got %v", err)
	}
	for _, want := range []string{"Next:", "blocked URL", "another available source"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("403 error missing %q guidance: %v", want, err)
		}
	}
}

func TestFetchTool_DefaultHeadersLookBrowserCompatible(t *testing.T) {
	var ua, acceptLanguage string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ua = r.Header.Get("User-Agent")
		acceptLanguage = r.Header.Get("Accept-Language")
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	tool := FetchTool(FetchConfig{AllowPrivateNetwork: true})
	args, _ := json.Marshal(map[string]string{"url": srv.URL})
	if _, err := tool.Execute(context.Background(), args); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	for _, want := range []string{"Mozilla/5.0", "Chrome/", "AffentWebFetch"} {
		if !strings.Contains(ua, want) {
			t.Fatalf("User-Agent missing %q: %q", want, ua)
		}
	}
	if !strings.Contains(acceptLanguage, "en-US") {
		t.Fatalf("Accept-Language = %q, want en-US hint", acceptLanguage)
	}
}

func TestFetchTool_BodyCapReportsTruncation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write([]byte("abcdefghi"))
	}))
	defer srv.Close()

	tool := FetchTool(FetchConfig{
		MaxBytes:            5,
		MaxResultChars:      100,
		AllowPrivateNetwork: true,
	})
	args, _ := json.Marshal(map[string]string{"url": srv.URL})
	out, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "abcde") || strings.Contains(out, "fghi") {
		t.Fatalf("body cap not applied correctly:\n%s", out)
	}
	if !strings.Contains(out, "response body truncated") {
		t.Fatalf("truncated fetch should be explicit to the model:\n%s", out)
	}

	tool = FetchTool(FetchConfig{
		MaxBytes:            5,
		MaxResultChars:      4,
		AllowPrivateNetwork: true,
	})
	out, err = tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute with result cap: %v", err)
	}
	for _, want := range []string{"...(truncated)", "response body truncated"} {
		if !strings.Contains(out, want) {
			t.Fatalf("truncated fetch should preserve marker %q after result cap:\n%s", want, out)
		}
	}
}

func TestNormalizeFetchConfigClampsHardCaps(t *testing.T) {
	cfg := normalizeFetchConfig(FetchConfig{
		HTTP:           &http.Client{},
		MaxBytes:       maxFetchBytes + 1,
		MaxResultChars: maxFetchResultChars + 1,
	})
	if cfg.MaxBytes != maxFetchBytes {
		t.Fatalf("MaxBytes = %d, want hard cap %d", cfg.MaxBytes, maxFetchBytes)
	}
	if cfg.MaxResultChars != maxFetchResultChars {
		t.Fatalf("MaxResultChars = %d, want hard cap %d", cfg.MaxResultChars, maxFetchResultChars)
	}

	defaults := normalizeFetchConfig(FetchConfig{HTTP: &http.Client{}})
	if defaults.MaxBytes != defaultMaxBytes {
		t.Fatalf("default MaxBytes = %d, want %d", defaults.MaxBytes, defaultMaxBytes)
	}
	if defaults.MaxResultChars != defaultMaxResultChars {
		t.Fatalf("default MaxResultChars = %d, want %d", defaults.MaxResultChars, defaultMaxResultChars)
	}
}

// stubProvider lets us test SearchTool without a real backend.
type stubProvider struct{ results []SearchResult }

func (s stubProvider) Search(_ context.Context, _ string, _ int) ([]SearchResult, error) {
	return s.results, nil
}

type recordingSearchProvider struct {
	gotN int
}

func (p *recordingSearchProvider) Search(_ context.Context, _ string, n int) ([]SearchResult, error) {
	p.gotN = n
	return []SearchResult{{Title: "Only", URL: "https://example.com", Snippet: "ok"}}, nil
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

func TestSearchTool_NoResultsIncludesNext(t *testing.T) {
	tool, err := SearchTool(SearchConfig{Provider: stubProvider{}})
	if err != nil {
		t.Fatalf("SearchTool: %v", err)
	}
	args, _ := json.Marshal(map[string]any{"query": "narrow unknown thing"})
	out, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	for _, want := range []string{"(no results)", "Next:", "different keywords", "official domain"} {
		if !strings.Contains(out, want) {
			t.Fatalf("no-result output missing %q:\n%s", want, out)
		}
	}
}

func TestSearchTool_ProviderErrorIncludesNext(t *testing.T) {
	tool, err := SearchTool(SearchConfig{Provider: failingProvider{}})
	if err != nil {
		t.Fatalf("SearchTool: %v", err)
	}
	_, err = tool.Execute(context.Background(), json.RawMessage(`{"query":"affine bittensor subnet"}`))
	if err == nil {
		t.Fatal("expected provider error")
	}
	for _, want := range []string{"intentional test failure", "Next:", "fewer/different keywords"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("provider error missing %q: %v", want, err)
		}
	}
}

func TestSearchTool_NumResultsMatchesAdvertisedCap(t *testing.T) {
	cases := []struct {
		name          string
		cfgMax        int
		args          string
		wantN         int
		wantSchemaMax string
		wantDefault   int
	}{
		{
			name:          "default cap",
			cfgMax:        0,
			args:          `{"query":"anything","num_results":20}`,
			wantN:         defaultSearchResults,
			wantSchemaMax: `"maximum": 8`,
			wantDefault:   defaultSearchResults,
		},
		{
			name:          "custom lower cap",
			cfgMax:        3,
			args:          `{"query":"anything","num_results":20}`,
			wantN:         3,
			wantSchemaMax: `"maximum": 3`,
			wantDefault:   3,
		},
		{
			name:          "custom cap above hard maximum",
			cfgMax:        100,
			args:          `{"query":"anything","num_results":100}`,
			wantN:         maxSearchResults,
			wantSchemaMax: `"maximum": 20`,
			wantDefault:   defaultSearchResults,
		},
		{
			name:          "missing argument uses effective default",
			cfgMax:        20,
			args:          `{"query":"anything"}`,
			wantN:         defaultSearchResults,
			wantSchemaMax: `"maximum": 20`,
			wantDefault:   defaultSearchResults,
		},
		{
			name:          "default follows lower custom cap",
			cfgMax:        5,
			args:          `{"query":"anything"}`,
			wantN:         5,
			wantSchemaMax: `"maximum": 5`,
			wantDefault:   5,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			provider := &recordingSearchProvider{}
			tool, err := SearchTool(SearchConfig{Provider: provider, MaxResults: c.cfgMax})
			if err != nil {
				t.Fatalf("SearchTool: %v", err)
			}
			if !strings.Contains(string(tool.Schema), c.wantSchemaMax) {
				t.Fatalf("schema %s missing %s", tool.Schema, c.wantSchemaMax)
			}
			if !strings.Contains(string(tool.Schema), `"additionalProperties": false`) {
				t.Fatalf("schema should reject unknown args: %s", tool.Schema)
			}
			if !strings.Contains(string(tool.Schema), fmt.Sprintf(`"default": %d`, c.wantDefault)) {
				t.Fatalf("schema %s missing default %d", tool.Schema, c.wantDefault)
			}
			if _, err := tool.Execute(context.Background(), json.RawMessage(c.args)); err != nil {
				t.Fatalf("Execute: %v", err)
			}
			if provider.gotN != c.wantN {
				t.Fatalf("provider n = %d, want %d", provider.gotN, c.wantN)
			}
		})
	}
}

func TestSearchToolRejectsUnknownArgs(t *testing.T) {
	tool, err := SearchTool(SearchConfig{Provider: stubProvider{}})
	if err != nil {
		t.Fatalf("SearchTool: %v", err)
	}
	_, err = tool.Execute(context.Background(), json.RawMessage(`{"query":"affent","url":"https://example.com"}`))
	if err == nil ||
		!strings.Contains(err.Error(), "unknown field") ||
		!strings.Contains(err.Error(), "url") ||
		!strings.Contains(err.Error(), "Next:") {
		t.Fatalf("unknown arg error = %v", err)
	}

	_, err = tool.Execute(context.Background(), json.RawMessage(`{"query":"`+strings.Repeat("x", maxSearchQueryBytes+1)+`"}`))
	if err == nil || !strings.Contains(err.Error(), "web_search supports queries up to") || !strings.Contains(err.Error(), "Next:") {
		t.Fatalf("oversized query error = %v", err)
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

	tool := FetchTool(FetchConfig{MaxResultChars: 51, AllowPrivateNetwork: true}) // odd → mid-rune
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

// TestFetchTool_SSRFGuardBlocksLoopback pins that the default
// FetchConfig refuses to dial a loopback address. A model under
// prompt injection that tries to fetch http://127.0.0.1:7777 (the
// affentserve port itself) or http://169.254.169.254 (cloud-metadata
// IMDSv1) hits the dialer's Control hook before TCP even opens.
func TestFetchTool_SSRFGuardBlocksLoopback(t *testing.T) {
	// httptest binds to 127.0.0.1; the guard should reject it.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("should not see this"))
	}))
	t.Cleanup(srv.Close)

	tool := FetchTool(FetchConfig{}) // default: guard ON
	args, _ := json.Marshal(map[string]string{"url": srv.URL})
	out, err := tool.Execute(context.Background(), args)
	if err == nil {
		t.Fatalf("expected SSRF rejection; got out=%q", out)
	}
	if !strings.Contains(err.Error(), "ssrf-guard") {
		t.Errorf("error must mention ssrf-guard so operators can grep; got %v", err)
	}
	if !strings.Contains(err.Error(), "Next:") || !strings.Contains(err.Error(), "private") {
		t.Errorf("SSRF rejection should include recovery guidance, got %v", err)
	}
}

// TestFetchTool_SSRFGuardOptInAllowsLoopback pins the escape hatch:
// when AllowPrivateNetwork is on, the same loopback target succeeds.
// This is the path dev / local-service fetching takes.
func TestFetchTool_SSRFGuardOptInAllowsLoopback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("local ok"))
	}))
	t.Cleanup(srv.Close)

	tool := FetchTool(FetchConfig{AllowPrivateNetwork: true})
	args, _ := json.Marshal(map[string]string{"url": srv.URL})
	out, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("opt-in path must work: %v", err)
	}
	if !strings.Contains(out, "local ok") {
		t.Errorf("expected body to come through; got %q", out)
	}
}

// TestIsBlockedIP pins the category coverage so a future refactor of
// isBlockedIP doesn't silently unblock something. Covers the
// well-known SSRF targets agents see in the wild: cloud-metadata,
// RFC1918 internal services, IPv6 ULA / link-local.
func TestIsBlockedIP(t *testing.T) {
	blocked := []string{
		"127.0.0.1",       // loopback v4
		"::1",             // loopback v6
		"10.0.0.5",        // RFC1918
		"172.16.0.5",      // RFC1918
		"192.168.1.5",     // RFC1918
		"169.254.169.254", // AWS / Azure / GCP metadata
		"fe80::1",         // IPv6 link-local
		"fc00::1",         // IPv6 ULA
		"0.0.0.0",         // unspecified
		"255.255.255.255", // broadcast
		"224.0.0.1",       // IPv4 multicast
		"ff02::1",         // IPv6 multicast
		// IPv6-mapped IPv4 ("::ffff:N.N.N.N") is the bypass shape a
		// motivated attacker reaches for once the straightforward
		// IPv4 SSRF check is in place. net.IP.IsLoopback /
		// IsPrivate / IsLinkLocalUnicast in Go's standard library
		// already understand the v4-mapped form (they call .To4()
		// internally), so these should all block — pin the
		// assumption so a refactor that bypasses To4() can't quietly
		// regress the guard.
		"::ffff:127.0.0.1",       // v4-mapped loopback
		"::ffff:10.0.0.5",        // v4-mapped RFC1918
		"::ffff:169.254.169.254", // v4-mapped cloud-metadata
	}
	for _, s := range blocked {
		if !isBlockedIP(net.ParseIP(s)) {
			t.Errorf("isBlockedIP(%s) = false; want true", s)
		}
	}
	allowed := []string{
		"8.8.8.8",
		"1.1.1.1",
		"203.0.113.5", // TEST-NET-3 public-range example
		"2606:4700:4700::1111",
	}
	for _, s := range allowed {
		if isBlockedIP(net.ParseIP(s)) {
			t.Errorf("isBlockedIP(%s) = true; want false (public IP)", s)
		}
	}
}

func TestSearchTool_EmptyQuery(t *testing.T) {
	tool, _ := SearchTool(SearchConfig{Provider: stubProvider{}})
	_, err := tool.Execute(context.Background(), json.RawMessage(`{"query":""}`))
	if err == nil || !strings.Contains(err.Error(), "query is required") {
		t.Errorf("expected query-required error, got %v", err)
	}
	if err == nil || !strings.Contains(err.Error(), "Next:") {
		t.Errorf("query-required error should include corrective Next step, got %v", err)
	}
	_, err = tool.Execute(context.Background(), json.RawMessage(`{"query":"   "}`))
	if err == nil || !strings.Contains(err.Error(), "query is required") {
		t.Errorf("expected blank-query required error, got %v", err)
	}
	if err == nil || !strings.Contains(err.Error(), "Next:") {
		t.Errorf("blank-query error should include corrective Next step, got %v", err)
	}
	if !strings.Contains(string(tool.Schema), `"minLength": 1`) {
		t.Fatalf("schema should publish query minLength: %s", tool.Schema)
	}
	if !strings.Contains(string(tool.Schema), `"maxLength": 2048`) {
		t.Fatalf("schema should publish query maxLength: %s", tool.Schema)
	}
}

func TestSearchTool_QueryMaxLength(t *testing.T) {
	tool, _ := SearchTool(SearchConfig{Provider: stubProvider{results: []SearchResult{{Title: "ok"}}}})
	query := strings.Repeat("x", maxSearchQueryBytes)
	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"query":"`+query+`"}`)); err != nil {
		t.Fatalf("max-size query should pass: %v", err)
	}
	query += "x"
	_, err := tool.Execute(context.Background(), json.RawMessage(`{"query":"`+query+`"}`))
	if err == nil || !strings.Contains(err.Error(), "web_search supports queries up to") {
		t.Fatalf("expected oversized query error, got %v", err)
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
