package browser

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

func TestFileResponseCache_SkipsOversizedBodies(t *testing.T) {
	c, err := NewFileResponseCache(t.TempDir(), 0)
	if err != nil {
		t.Fatal(err)
	}
	body := make([]byte, maxCachedResponseBodyBytes+1)
	if err := c.Put(context.Background(), "https://example.com/huge", &CachedResponse{
		StatusCode: 200,
		Body:       body,
	}); err != nil {
		t.Fatalf("Put oversized body should be a silent cache skip: %v", err)
	}
	_, ok, err := c.Get(context.Background(), "https://example.com/huge")
	if err != nil {
		t.Fatalf("Get oversized skipped body: %v", err)
	}
	if ok {
		t.Fatalf("oversized body must not be cached")
	}
}

func TestFileResponseCache_PutResultReportsStoredAndSkipped(t *testing.T) {
	c, err := NewFileResponseCache(t.TempDir(), 0)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := c.PutResult(context.Background(), "https://example.com/ok", &CachedResponse{
		StatusCode: 200,
		Body:       []byte("<html>ok</html>"),
	})
	if err != nil || !stored {
		t.Fatalf("PutResult ok stored=%t err=%v, want stored", stored, err)
	}
	cases := []struct {
		name  string
		url   string
		entry *CachedResponse
	}{
		{
			name: "error status",
			url:  "https://example.com/not-found",
			entry: &CachedResponse{
				StatusCode: 404,
				Body:       []byte("not found"),
			},
		},
		{
			name: "challenge body",
			url:  "https://example.com/challenge",
			entry: &CachedResponse{
				StatusCode: 200,
				Body:       []byte("<html><title>Just a moment...</title></html>"),
			},
		},
		{
			name: "oversized body",
			url:  "https://example.com/huge",
			entry: &CachedResponse{
				StatusCode: 200,
				Body:       make([]byte, maxCachedResponseBodyBytes+1),
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stored, err := c.PutResult(context.Background(), tc.url, tc.entry)
			if err != nil {
				t.Fatalf("PutResult skipped response returned error: %v", err)
			}
			if stored {
				t.Fatalf("PutResult stored skipped response")
			}
		})
	}
}

func TestFileResponseCache_HonorsCanceledContext(t *testing.T) {
	dir := t.TempDir()
	c, err := NewFileResponseCache(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, ok, err := c.Get(ctx, "https://example.com/cancelled")
	if !errors.Is(err, context.Canceled) || ok {
		t.Fatalf("Get canceled ok=%t err=%v, want context.Canceled miss", ok, err)
	}
	stored, err := c.PutResult(ctx, "https://example.com/cancelled", &CachedResponse{
		StatusCode: 200,
		Body:       []byte("body"),
	})
	if !errors.Is(err, context.Canceled) || stored {
		t.Fatalf("PutResult canceled stored=%t err=%v, want context.Canceled without store", stored, err)
	}
	if err := c.Put(ctx, "https://example.com/cancelled-put", &CachedResponse{
		StatusCode: 200,
		Body:       []byte("body"),
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Put canceled err=%v, want context.Canceled", err)
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "*"))
	if len(matches) != 0 {
		t.Fatalf("canceled cache writes must not create files: %v", matches)
	}
}

func TestFileResponseCache_GetSkipsOversizedBodyFile(t *testing.T) {
	dir := t.TempDir()
	c, err := NewFileResponseCache(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	url := "https://example.com/grew-after-write"
	if err := c.Put(context.Background(), url, &CachedResponse{
		StatusCode: 200,
		Body:       []byte("small"),
	}); err != nil {
		t.Fatalf("Put: %v", err)
	}
	_, bodyPath := c.paths(url)
	if err := os.Truncate(bodyPath, int64(maxCachedResponseBodyBytes+1)); err != nil {
		t.Fatalf("inflate body file: %v", err)
	}
	_, ok, err := c.Get(context.Background(), url)
	if err != nil {
		t.Fatalf("Get oversized body file: %v", err)
	}
	if ok {
		t.Fatal("oversized on-disk body must be treated as a cache miss")
	}
}

func TestFileResponseCache_GetSkipsOversizedMetaFile(t *testing.T) {
	dir := t.TempDir()
	c, err := NewFileResponseCache(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	url := "https://example.com/oversized-meta"
	if err := c.Put(context.Background(), url, &CachedResponse{
		StatusCode: 200,
		Body:       []byte("small"),
	}); err != nil {
		t.Fatalf("Put: %v", err)
	}
	metaPath, _ := c.paths(url)
	if err := os.Truncate(metaPath, int64(maxCachedResponseMetaBytes+1)); err != nil {
		t.Fatalf("inflate meta file: %v", err)
	}
	_, ok, err := c.Get(context.Background(), url)
	if err != nil {
		t.Fatalf("Get oversized meta file: %v", err)
	}
	if ok {
		t.Fatal("oversized on-disk meta must be treated as a cache miss")
	}
}

func TestReadCacheFileCapped(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cache.bin")
	if err := os.WriteFile(path, []byte("12345"), 0o644); err != nil {
		t.Fatalf("write cache file: %v", err)
	}

	got, err := readCacheFileCapped(path, 5)
	if err != nil {
		t.Fatalf("read exact limit: %v", err)
	}
	if string(got) != "12345" {
		t.Fatalf("read exact limit = %q, want 12345", got)
	}

	got, err = readCacheFileCapped(path, 4)
	if err != nil {
		t.Fatalf("read over limit: %v", err)
	}
	if got != nil {
		t.Fatalf("over limit read returned %d bytes, want nil miss", len(got))
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

func TestFileResponseCache_Sweep_DropsOversizedMeta(t *testing.T) {
	dir := t.TempDir()
	c, err := NewFileResponseCache(dir, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	url := "https://example.com/oversized-meta-sweep"
	if err := c.Put(context.Background(), url, &CachedResponse{
		StatusCode: 200,
		Body:       []byte("body"),
	}); err != nil {
		t.Fatalf("Put: %v", err)
	}
	metaPath, bodyPath := c.paths(url)
	if err := os.Truncate(metaPath, int64(maxCachedResponseMetaBytes+1)); err != nil {
		t.Fatalf("inflate meta file: %v", err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(metaPath, old, old); err != nil {
		t.Fatalf("age meta file: %v", err)
	}

	deleted, err := c.Sweep(context.Background())
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("deleted = %d, want 1", deleted)
	}
	for _, path := range []string{metaPath, bodyPath} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("%s still exists or stat failed unexpectedly: %v", path, err)
		}
	}
}

func TestFileResponseCache_Sweep_DoesNotKillConcurrentPut(t *testing.T) {
	// Regression: Sweep used to read FetchedAt, then Remove without
	// holding the cache mutex, leaving a TOCTOU window during which a
	// concurrent Put's freshly-written entry could be deleted. We
	// drive Put + Sweep in parallel and assert every Put's URL is
	// still readable afterward.
	dir := t.TempDir()
	// TTL chosen so seeded entries (FetchedAt=now-1h) are stale and
	// Sweep finds work, but every fresh Put stays comfortably within
	// the window for the assertion. An earlier 50ms TTL was sensitive
	// to per-Put latency: once atomicWriteFile started fsyncing the
	// tmp file the cumulative time across 64 sequential Puts pushed
	// the earliest entry past 50ms and made the test flake.
	c, err := NewFileResponseCache(dir, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}

	// Pre-seed enough stale entries that Sweep has real work to do
	// while Puts race in.
	stale := time.Now().Add(-time.Hour).UTC()
	for i := 0; i < 32; i++ {
		entry := &CachedResponse{
			URL:        fmt.Sprintf("https://stale.example.com/%d", i),
			StatusCode: 200,
			Body:       []byte("stale-body"),
			FetchedAt:  stale,
		}
		if err := c.Put(context.Background(), entry.URL, entry); err != nil {
			t.Fatal(err)
		}
	}

	const freshN = 64
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < freshN; i++ {
			url := fmt.Sprintf("https://fresh.example.com/%d", i)
			_ = c.Put(context.Background(), url, &CachedResponse{
				StatusCode: 200,
				Body:       []byte("fresh-body"),
			})
		}
	}()
	go func() {
		defer wg.Done()
		// Run Sweep repeatedly so its critical sections overlap with
		// the Put loop's writes.
		for i := 0; i < 8; i++ {
			_, _ = c.Sweep(context.Background())
		}
	}()
	wg.Wait()

	// After the dust settles, every fresh URL must still resolve.
	for i := 0; i < freshN; i++ {
		url := fmt.Sprintf("https://fresh.example.com/%d", i)
		got, ok, err := c.Get(context.Background(), url)
		if err != nil {
			t.Fatalf("Get %s: %v", url, err)
		}
		if !ok {
			t.Fatalf("fresh entry %s was deleted by Sweep race", url)
		}
		if string(got.Body) != "fresh-body" {
			t.Fatalf("fresh entry %s body wrong: %q", url, got.Body)
		}
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

func TestURLMatchesBlockedDomain(t *testing.T) {
	patterns := []string{
		"doubleclick.net",
		"facebook.net",
		"accounts.google.com/gsi",
	}
	cases := []struct {
		url  string
		want bool
		why  string
	}{
		{"https://doubleclick.net/track", true, "exact host"},
		{"https://stats.g.doubleclick.net/log", true, "subdomain"},
		{"https://accounts.google.com/gsi/client.js?v=1", true, "host+path prefix"},
		{"https://accounts.google.com/oauth/v2", false, "right host, wrong path"},

		// False-positive prevention. strings.Contains used to flag all of
		// these — they don't actually go to a blocked origin.
		{"https://example.com/?ref=doubleclick.net", false, "tracker name in query string"},
		{"https://example.com/static/doubleclick.net/sdk.js", false, "tracker name in path segment"},
		{"https://my-doubleclick.net.attacker.com/", false, "lookalike suffix"},
		{"https://notfacebook.net/", false, "lookalike prefix"},

		// IPv6 + port don't trip the host parse.
		{"http://[::1]:8080/health", false, "IPv6 loopback"},
		{"https://doubleclick.net:443/x", true, "exact host with port"},

		// Garbage in, false out.
		{"not a url", false, "unparseable"},
		{"", false, "empty"},
	}
	for _, c := range cases {
		t.Run(c.why, func(t *testing.T) {
			got := urlMatchesBlockedDomain(c.url, patterns)
			if got != c.want {
				t.Errorf("urlMatchesBlockedDomain(%q) = %v, want %v", c.url, got, c.want)
			}
		})
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
