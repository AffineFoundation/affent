package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadConfig_EmptyPath(t *testing.T) {
	cfg, err := LoadConfig("")
	if err != nil {
		t.Fatalf("LoadConfig(\"\"): %v", err)
	}
	if (cfg != Config{}) {
		t.Errorf("expected zero-value Config, got %+v", cfg)
	}
}

func TestLoadConfig_File(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.json")
	if err := os.WriteFile(path, []byte(`{
        "listen": "127.0.0.1:9000",
        "base_url": "https://example/v1",
        "model": "demo",
        "max_sessions": 8,
        "session_idle_ttl": "30s",
        "enable_browser": true
    }`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Listen != "127.0.0.1:9000" {
		t.Errorf("Listen = %q", cfg.Listen)
	}
	if cfg.BaseURL != "https://example/v1" {
		t.Errorf("BaseURL = %q", cfg.BaseURL)
	}
	if cfg.MaxSessions != 8 {
		t.Errorf("MaxSessions = %d", cfg.MaxSessions)
	}
	if !cfg.EnableBrowser {
		t.Errorf("EnableBrowser should be true")
	}
}

func TestLoadConfig_RejectsUnknownFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.json")
	_ = os.WriteFile(path, []byte(`{"unknown_key": 1}`), 0o600)
	_, err := LoadConfig(path)
	if err == nil {
		t.Fatalf("expected error for unknown field")
	}
}

func TestConfig_Resolve_AppliesDefaults(t *testing.T) {
	cfg := Config{BaseURL: "https://example/v1"}
	if err := cfg.Resolve(); err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != defaultListen {
		t.Errorf("Listen default = %q", cfg.Listen)
	}
	if cfg.MaxSessions != defaultMaxSessions {
		t.Errorf("MaxSessions default = %d", cfg.MaxSessions)
	}
	ttl, err := cfg.IdleTTL()
	if err != nil {
		t.Fatal(err)
	}
	if ttl != defaultSessionIdleTTL {
		t.Errorf("IdleTTL default = %s", ttl)
	}
}

func TestConfig_Resolve_PullsEnvFallback(t *testing.T) {
	t.Setenv("AFFENTSERVE_BASE_URL", "")
	t.Setenv("AFFENTSERVE_API_KEY", "from-env")
	cfg := Config{BaseURL: "https://example/v1"}
	if err := cfg.Resolve(); err != nil {
		t.Fatal(err)
	}
	if cfg.APIKey != "from-env" {
		t.Errorf("APIKey should pick up env, got %q", cfg.APIKey)
	}
}

func TestConfig_Validate_RequiresBaseURL(t *testing.T) {
	cfg := Config{}
	if err := cfg.Resolve(); err != nil {
		t.Fatal(err)
	}
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "base_url") {
		t.Errorf("expected base_url validation error, got %v", err)
	}
}

// TestConfig_Validate_RequiresModel pins that startup fails when
// --model is missing. Without this check the LLMClient sends an
// empty `model` field upstream and every OpenAI-compat backend
// 400s the first chat-completions call — failures the operator
// only discovers via a client log error, well after deploy time.
func TestConfig_Validate_RequiresModel(t *testing.T) {
	cfg := Config{BaseURL: "https://example/v1"}
	if err := cfg.Resolve(); err != nil {
		t.Fatal(err)
	}
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "model") {
		t.Errorf("expected model validation error, got %v", err)
	}
}

func TestConfig_IdleTTL_RejectsBadDuration(t *testing.T) {
	cfg := Config{BaseURL: "https://example/v1", SessionIdleTTL: "not-a-duration"}
	_, err := cfg.IdleTTL()
	if err == nil {
		t.Errorf("expected error on bad duration")
	}
}

func TestConfig_IdleTTL_HappyPath(t *testing.T) {
	cfg := Config{BaseURL: "https://example/v1", SessionIdleTTL: "5m"}
	d, err := cfg.IdleTTL()
	if err != nil {
		t.Fatal(err)
	}
	if d != 5*time.Minute {
		t.Errorf("IdleTTL = %s", d)
	}
}
