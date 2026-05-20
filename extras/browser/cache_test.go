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
