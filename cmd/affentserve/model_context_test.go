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
