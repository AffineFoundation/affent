package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rs/zerolog"
)

func TestNewSessionPoolResolvesModelContextWindowFromProvider(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Fatalf("path = %q, want /v1/models", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"ctx-model","context_window":131072,"auto_compact_token_limit":120000}]}`))
	}))
	defer srv.Close()

	cfg := Config{
		Listen:                 "127.0.0.1:0",
		MaxSessions:            4,
		SessionIdleTTL:         "5m",
		WorkspaceRoot:          t.TempDir(),
		BaseURL:                srv.URL + "/v1",
		APIKey:                 "test",
		Model:                  "ctx-model",
		ModelContextWindowAuto: true,
	}
	pool, err := NewSessionPool(cfg, zerolog.New(io.Discard))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Shutdown)
	if pool.cfg.ModelContextWindowTokens != 131072 {
		t.Fatalf("ModelContextWindowTokens = %d, want 131072", pool.cfg.ModelContextWindowTokens)
	}
	if pool.cfg.CompactTriggerInputTokens != 104857 {
		t.Fatalf("CompactTriggerInputTokens = %d, want clamped default policy limit 104857", pool.cfg.CompactTriggerInputTokens)
	}
}

func TestNewSessionPoolResolvesModelContextWindowByDefaultAfterResolve(t *testing.T) {
	t.Setenv("AFFENTSERVE_BASE_URL", "")
	t.Setenv("AFFENTSERVE_MODEL", "")
	t.Setenv("AFFENTSERVE_API_KEY", "")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Fatalf("path = %q, want /v1/models", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"ctx-model","context_window":100000}]}`))
	}))
	defer srv.Close()

	cfg := Config{
		Listen:         "127.0.0.1:0",
		MaxSessions:    4,
		SessionIdleTTL: "5m",
		WorkspaceRoot:  t.TempDir(),
		BaseURL:        srv.URL + "/v1",
		APIKey:         "test",
		Model:          "ctx-model",
	}
	if err := cfg.Resolve(); err != nil {
		t.Fatal(err)
	}
	pool, err := NewSessionPool(cfg, zerolog.New(io.Discard))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Shutdown)
	if pool.cfg.ModelContextWindowTokens != 100000 {
		t.Fatalf("ModelContextWindowTokens = %d, want 100000", pool.cfg.ModelContextWindowTokens)
	}
	if pool.cfg.CompactTriggerInputTokens != 0 {
		t.Fatalf("CompactTriggerInputTokens = %d, want 0 derived at runtime", pool.cfg.CompactTriggerInputTokens)
	}
}

func TestNewSessionPoolModelContextWindowHonorsCompactPercent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"ctx-model","context_window":100000,"auto_compact_token_limit":95000}]}`))
	}))
	defer srv.Close()

	cfg := Config{
		Listen:                     "127.0.0.1:0",
		MaxSessions:                4,
		SessionIdleTTL:             "5m",
		WorkspaceRoot:              t.TempDir(),
		BaseURL:                    srv.URL + "/v1",
		APIKey:                     "test",
		Model:                      "ctx-model",
		ModelContextWindowAuto:     true,
		CompactTriggerInputPercent: 75,
	}
	pool, err := NewSessionPool(cfg, zerolog.New(io.Discard))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Shutdown)
	if pool.cfg.CompactTriggerInputTokens != 75000 {
		t.Fatalf("CompactTriggerInputTokens = %d, want explicit 75%% policy limit 75000", pool.cfg.CompactTriggerInputTokens)
	}
}

func TestNewSessionPoolUsesKnownModelContextWindowFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"qwen3.6-35b-a3b"}]}`))
	}))
	defer srv.Close()

	cfg := Config{
		Listen:                 "127.0.0.1:0",
		MaxSessions:            4,
		SessionIdleTTL:         "5m",
		WorkspaceRoot:          t.TempDir(),
		BaseURL:                srv.URL + "/v1",
		APIKey:                 "test",
		Model:                  "qwen3.6-35b-a3b",
		ModelContextWindowAuto: true,
	}
	pool, err := NewSessionPool(cfg, zerolog.New(io.Discard))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Shutdown)
	if pool.cfg.ModelContextWindowTokens != 262144 {
		t.Fatalf("ModelContextWindowTokens = %d, want registry fallback 262144", pool.cfg.ModelContextWindowTokens)
	}
	if pool.cfg.CompactTriggerInputTokens != 0 {
		t.Fatalf("CompactTriggerInputTokens = %d, want 0 derived at runtime", pool.cfg.CompactTriggerInputTokens)
	}
}

func TestNewSessionPoolUsesEffectiveModelContextWindowFromProvider(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"ctx-model","context_window":100000,"effective_context_window_percent":95,"auto_compact_token_limit":90000}]}`))
	}))
	defer srv.Close()

	cfg := Config{
		Listen:                 "127.0.0.1:0",
		MaxSessions:            4,
		SessionIdleTTL:         "5m",
		WorkspaceRoot:          t.TempDir(),
		BaseURL:                srv.URL + "/v1",
		APIKey:                 "test",
		Model:                  "ctx-model",
		ModelContextWindowAuto: true,
	}
	pool, err := NewSessionPool(cfg, zerolog.New(io.Discard))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Shutdown)
	if pool.cfg.ModelContextWindowTokens != 95000 {
		t.Fatalf("ModelContextWindowTokens = %d, want effective 95000", pool.cfg.ModelContextWindowTokens)
	}
	if pool.cfg.ModelContextWindowEffectivePercent != 95 {
		t.Fatalf("ModelContextWindowEffectivePercent = %d, want 95", pool.cfg.ModelContextWindowEffectivePercent)
	}
	if pool.cfg.CompactTriggerInputTokens != 76000 {
		t.Fatalf("CompactTriggerInputTokens = %d, want provider limit clamped to 80%% of effective window", pool.cfg.CompactTriggerInputTokens)
	}
	if !pool.cfg.compactTriggerInputTokensAuto {
		t.Fatal("compactTriggerInputTokensAuto = false, want provider-derived auto compact limit")
	}
}

func TestNewSessionPoolExplicitModelContextWindowSkipsProvider(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		t.Fatalf("provider metadata should not be called when explicit window is set")
	}))
	defer srv.Close()

	cfg := Config{
		Listen:                   "127.0.0.1:0",
		MaxSessions:              4,
		SessionIdleTTL:           "5m",
		WorkspaceRoot:            t.TempDir(),
		BaseURL:                  srv.URL + "/v1",
		APIKey:                   "test",
		Model:                    "ctx-model",
		ModelContextWindowTokens: 65536,
		ModelContextWindowAuto:   true,
	}
	pool, err := NewSessionPool(cfg, zerolog.New(io.Discard))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Shutdown)
	if called {
		t.Fatal("provider metadata was called")
	}
	if pool.cfg.ModelContextWindowTokens != 65536 {
		t.Fatalf("ModelContextWindowTokens = %d, want 65536", pool.cfg.ModelContextWindowTokens)
	}
}
