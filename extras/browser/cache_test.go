package browser

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFileResponseCache_GetMiss(t *testing.T) {
	c, err := NewFileResponseCache(t.TempDir(), 0)
	if err != nil {
		t.Fatal(err)
	}
	_, ok, err := c.Get(context.Background(), "https://example.com/")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Errorf("expected miss on empty cache")
	}
}

func TestFileResponseCache_PutGetRoundTrip(t *testing.T) {
	dir := t.TempDir()
	c, err := NewFileResponseCache(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := &CachedResponse{
		StatusCode: 200,
		Headers: http.Header{
			"Content-Type": {"text/html"},
			"X-Trace-Id":   {"abc"},
		},
		Body: []byte("<html>hi</html>"),
	}
	if err := c.Put(context.Background(), "https://example.com/page", want); err != nil {
		t.Fatalf("put: %v", err)
	}
	got, ok, err := c.Get(context.Background(), "https://example.com/page")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !ok {
		t.Fatalf("expected hit")
	}
	if got.StatusCode != 200 {
		t.Errorf("status = %d", got.StatusCode)
	}
	if string(got.Body) != "<html>hi</html>" {
		t.Errorf("body = %q", got.Body)
	}
	if got.Headers.Get("Content-Type") != "text/html" {
		t.Errorf("Content-Type = %q", got.Headers.Get("Content-Type"))
	}
	if got.FetchedAt.IsZero() {
		t.Errorf("FetchedAt should be set on Put")
	}
	// Confirm on-disk file naming is hash-based, not URL-based (no
	// path traversal exposure).
	matches, _ := filepath.Glob(filepath.Join(dir, "*.bin"))
	if len(matches) != 1 {
		t.Errorf("expected 1 body file, got %d", len(matches))
	}
}

func TestFileResponseCache_TTL_RejectsStale(t *testing.T) {
	c, err := NewFileResponseCache(t.TempDir(), 50*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	entry := &CachedResponse{StatusCode: 200, Body: []byte("hi")}
	_ = c.Put(context.Background(), "u", entry)
	time.Sleep(100 * time.Millisecond)
	_, ok, _ := c.Get(context.Background(), "u")
	if ok {
		t.Errorf("expected stale entry to miss")
	}
}

func TestFileResponseCache_DoesNotCacheErrorResponses(t *testing.T) {
	c, err := NewFileResponseCache(t.TempDir(), 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, code := range []int{0, 403, 404, 500, 503} {
		entry := &CachedResponse{StatusCode: code, Body: []byte("err")}
		if err := c.Put(context.Background(), "u", entry); err != nil {
			t.Errorf("Put(%d) should not error: %v", code, err)
		}
		_, ok, _ := c.Get(context.Background(), "u")
		if ok {
			t.Errorf("status %d should not be cached", code)
		}
	}
}

func TestFileResponseCache_RejectsCloudflareChallengePaths(t *testing.T) {
	c, err := NewFileResponseCache(t.TempDir(), 0)
	if err != nil {
		t.Fatal(err)
	}
	bodies := []string{
		// Original four
		"https://www.coingecko.com/cdn-cgi/challenge-platform/h/g/orchestrate/chl_page/v1?ray=9fea27db",
		"https://www.coingecko.com/cdn-cgi/rum?",
		"https://www.coingecko.com/cdn-cgi/zaraz/i.js?z=...",
		"https://www.coingecko.com/cdn-cgi/scripts/managed-challenge.js",
		// Discovered post-deploy
		"https://www.coingecko.com/cdn-cgi/speculation",
		"https://cloudflare.com/cdn-cgi/trace",
		// html-load.cc both flavors
		"https://3.html-load.cc/feed/uf3/0vs/sag/i4x/www.coingecko.com/sur/...",
		"https://html-load.cc/script/d3d3LmNvaW5nZWNrby5jb20.js",
	}
	for _, url := range bodies {
		entry := &CachedResponse{StatusCode: 200, Body: []byte("<html>ok</html>")}
		_ = c.Put(context.Background(), url, entry)
		_, ok, _ := c.Get(context.Background(), url)
		if ok {
			t.Errorf("CF challenge URL must not be cached: %s", url)
		}
	}
}

func TestFileResponseCache_RejectsCloudflareChallengeBody(t *testing.T) {
	c, err := NewFileResponseCache(t.TempDir(), 0)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string][]byte{
		"just_a_moment": []byte("<html><head><title>Just a moment...</title></head>"),
		"verifying":     []byte("<html><body>Verifying you are human. This may take a few seconds."),
		"cf_chl":        []byte("<script>window.__cf_chl_opt = ...</script>"),
		"cf_blocked":    []byte("<html>Sorry, you have been blocked"),
	}
	for name, body := range cases {
		entry := &CachedResponse{StatusCode: 200, Body: body}
		_ = c.Put(context.Background(), "https://example.com/page-"+name, entry)
		_, ok, _ := c.Get(context.Background(), "https://example.com/page-"+name)
		if ok {
			t.Errorf("%s body should not be cached", name)
		}
	}
}

// TestFileResponseCache_PlainHTMLCachesFine confirms that an article
// body without any challenge markers gets cached normally.
func TestFileResponseCache_Sweep_RemovesExpiredEntries(t *testing.T) {
	dir := t.TempDir()
	c, err := NewFileResponseCache(dir, 50*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	// Put 3 entries, wait past TTL, sweep, expect all gone.
	for _, u := range []string{"a", "b", "c"} {
		_ = c.Put(context.Background(), "https://example.com/"+u, &CachedResponse{
			StatusCode: 200,
			Body:       []byte("body-" + u),
		})
	}
	beforeBin := countFiles(t, dir, ".bin")
	if beforeBin != 3 {
		t.Fatalf("expected 3 bin files before sweep, got %d", beforeBin)
	}
	time.Sleep(80 * time.Millisecond)
	deleted, err := c.Sweep(context.Background())
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if deleted != 3 {
		t.Errorf("expected to delete 3 entries, got %d", deleted)
	}
	afterBin := countFiles(t, dir, ".bin")
	afterMeta := countFiles(t, dir, ".meta.json")
	if afterBin != 0 || afterMeta != 0 {
		t.Errorf("expected 0 files after sweep, got bin=%d meta=%d", afterBin, afterMeta)
	}
}

func TestFileResponseCache_Sweep_KeepsFreshEntries(t *testing.T) {
	dir := t.TempDir()
	c, err := NewFileResponseCache(dir, 1*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	_ = c.Put(context.Background(), "https://example.com/fresh", &CachedResponse{
		StatusCode: 200,
		Body:       []byte("still good"),
	})
	deleted, err := c.Sweep(context.Background())
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if deleted != 0 {
		t.Errorf("expected 0 deletions for fresh entry, got %d", deleted)
	}
	_, ok, _ := c.Get(context.Background(), "https://example.com/fresh")
	if !ok {
		t.Errorf("fresh entry should still be retrievable after sweep")
	}
}

func TestFileResponseCache_Sweep_NoTTLIsNoOp(t *testing.T) {
	dir := t.TempDir()
	c, err := NewFileResponseCache(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	_ = c.Put(context.Background(), "https://example.com/forever", &CachedResponse{StatusCode: 200, Body: []byte("x")})
	deleted, err := c.Sweep(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 0 {
		t.Errorf("Sweep with ttl=0 must be a no-op, got deleted=%d", deleted)
	}
}

func TestFileResponseCache_Sweeper_StartStopIdempotent(t *testing.T) {
	dir := t.TempDir()
	c, err := NewFileResponseCache(dir, 50*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	c.StartSweeper(5*time.Minute, nil)
	c.StartSweeper(5*time.Minute, nil) // second call must be no-op (not double-start)
	c.StopSweeper()
	c.StopSweeper() // must not panic on double-stop
}

func countFiles(t *testing.T, dir, suffix string) int {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), suffix) {
			n++
		}
	}
	return n
}

func TestFileResponseCache_PlainHTMLCachesFine(t *testing.T) {
	c, err := NewFileResponseCache(t.TempDir(), 0)
	if err != nil {
		t.Fatal(err)
	}
	body := []byte("<html><body><h1>Welcome</h1><p>Lorem ipsum dolor.</p></body></html>")
	_ = c.Put(context.Background(), "https://example.com/page", &CachedResponse{StatusCode: 200, Body: body})
	got, ok, _ := c.Get(context.Background(), "https://example.com/page")
	if !ok {
		t.Fatalf("plain HTML must be cached")
	}
	if !strings.Contains(string(got.Body), "Welcome") {
		t.Errorf("retrieved body mismatch")
	}
}

func TestInterceptConfig_DefaultsApply(t *testing.T) {
	cfg := InterceptConfig{}.resolved()
	// Default block list should be populated.
	if len(cfg.BlockedResourceTypes) == 0 {
		t.Errorf("expected default BlockedResourceTypes, got empty")
	}
	if len(cfg.BlockedDomains) == 0 {
		t.Errorf("expected default BlockedDomains, got empty")
	}
}

func TestInterceptConfig_AllowAllDomainsClearsDefaults(t *testing.T) {
	cfg := InterceptConfig{AllowAllDomains: true}.resolved()
	if len(cfg.BlockedDomains) != 0 {
		t.Errorf("expected empty BlockedDomains when AllowAllDomains=true, got %v", cfg.BlockedDomains)
	}
}

func TestInterceptConfig_ExplicitBlockedTypesWins(t *testing.T) {
	cfg := InterceptConfig{BlockedResourceTypes: []string{"Script"}}.resolved()
	if len(cfg.BlockedResourceTypes) != 1 || cfg.BlockedResourceTypes[0] != "Script" {
		t.Errorf("expected explicit BlockedResourceTypes preserved, got %v", cfg.BlockedResourceTypes)
	}
}

func TestDefaultBlockedDomains_CoverWellKnownTrackers(t *testing.T) {
	want := []string{
		"google-analytics.com",
		"doubleclick.net",
		"facebook.net",
		"hotjar.com",
	}
	have := strings.Join(DefaultBlockedDomains, ",")
	for _, d := range want {
		if !strings.Contains(have, d) {
			t.Errorf("default block list missing %q", d)
		}
	}
}
