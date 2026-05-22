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

// TestConfig_PerCallTimeout_HappyPath pins the parser: empty → 0
// (Loop falls back to agent.DefaultPerCallTimeout), explicit
// duration parses, invalid string and non-positive both error so a
// typo in a deploy config doesn't silently keep the 3-minute default.
func TestConfig_PerCallTimeout_HappyPath(t *testing.T) {
	cases := []struct {
		in      string
		want    time.Duration
		wantErr bool
	}{
		{"", 0, false},
		{"3m", 3 * time.Minute, false},
		{"10m", 10 * time.Minute, false},
		{"0s", 0, true},  // non-positive must error
		{"-5s", 0, true}, // negative must error
		{"abc", 0, true}, // unparseable must error
	}
	for _, c := range cases {
		cfg := Config{PerCallTimeout: c.in}
		d, err := cfg.PerCallTimeoutDuration()
		if c.wantErr {
			if err == nil {
				t.Errorf("PerCallTimeoutDuration(%q) should error", c.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("PerCallTimeoutDuration(%q): %v", c.in, err)
		}
		if d != c.want {
			t.Errorf("PerCallTimeoutDuration(%q) = %s, want %s", c.in, d, c.want)
		}
	}
}

// TestConfig_Validate_RejectsBadPerCallTimeout pins that a typo
// fails at startup, not when the first chat request finds the
// duration unparseable.
func TestConfig_Validate_RejectsBadPerCallTimeout(t *testing.T) {
	cfg := Config{
		BaseURL:        "https://example/v1",
		Model:          "m",
		PerCallTimeout: "not-a-duration",
	}
	if err := cfg.Resolve(); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "per_call_timeout") {
		t.Errorf("expected per_call_timeout validation error, got %v", err)
	}
}

// TestConfig_Retention_HappyPath pins the parser: an explicit duration
// returns the parsed Duration; an empty value returns 0 (= disabled).
func TestConfig_Retention_HappyPath(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
	}{
		{"", 0},
		{"24h", 24 * time.Hour},
		{"720h", 720 * time.Hour},
	}
	for _, c := range cases {
		cfg := Config{SessionRetention: c.in}
		d, err := cfg.Retention()
		if err != nil {
			t.Errorf("Retention(%q): %v", c.in, err)
		}
		if d != c.want {
			t.Errorf("Retention(%q) = %s, want %s", c.in, d, c.want)
		}
	}
}

// TestConfig_Retention_RejectsBadDuration pins that a typo is loud,
// not silent — an operator who writes "30d" (Go doesn't support d)
// should see an error, not have retention silently disabled.
func TestConfig_Retention_RejectsBadDuration(t *testing.T) {
	cfg := Config{SessionRetention: "30d"}
	_, err := cfg.Retention()
	if err == nil {
		t.Errorf("expected error for unparseable duration; got nil")
	}
}

// TestConfig_Resolve_PullsRetentionEnvFallback pins the env path —
// AFFENTSERVE_SESSION_RETENTION acts the same way the other env
// vars do (fills in only when the field is empty).
func TestConfig_Resolve_PullsRetentionEnvFallback(t *testing.T) {
	t.Setenv("AFFENTSERVE_SESSION_RETENTION", "168h")
	cfg := Config{BaseURL: "https://example/v1"}
	if err := cfg.Resolve(); err != nil {
		t.Fatal(err)
	}
	if cfg.SessionRetention != "168h" {
		t.Errorf("SessionRetention should pick up env, got %q", cfg.SessionRetention)
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
