package main

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/affinefoundation/affent/internal/agent"
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
        "enable_browser": true,
        "enable_memory": false,
        "enable_subagent": false,
        "enable_focused_tasks": false,
        "subagent_max_depth": 1
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
	if cfg.EnableMemory {
		t.Errorf("EnableMemory should preserve explicit false")
	}
	if cfg.EnableSubagent {
		t.Errorf("EnableSubagent should preserve explicit false")
	}
	if cfg.EnableFocusedTasks {
		t.Errorf("EnableFocusedTasks should preserve explicit false")
	}
	if cfg.SubagentMaxDepth != 1 {
		t.Errorf("SubagentMaxDepth = %d, want 1", cfg.SubagentMaxDepth)
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

func TestLoadConfig_RejectsOversizeFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "huge.json")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", maxConfigBytes+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadConfig(path)
	if err == nil || !strings.Contains(err.Error(), "config exceeds") {
		t.Fatalf("oversized config error = %v, want config exceeds", err)
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
	if !cfg.EnableMemory {
		t.Errorf("EnableMemory should default on")
	}
	if !cfg.EnableSubagent {
		t.Errorf("EnableSubagent should default on")
	}
	if !cfg.EnableFocusedTasks {
		t.Errorf("EnableFocusedTasks should default on")
	}
	if cfg.SubagentMaxDepth != agent.DefaultSubagentMaxDepth {
		t.Errorf("SubagentMaxDepth default = %d", cfg.SubagentMaxDepth)
	}
	ttl, err := cfg.IdleTTL()
	if err != nil {
		t.Fatal(err)
	}
	if ttl != defaultSessionIdleTTL {
		t.Errorf("IdleTTL default = %s", ttl)
	}
}

func TestConfig_Validate_RejectsExplicitNonPositiveMaxSessions(t *testing.T) {
	for _, raw := range []string{`0`, `-1`} {
		t.Run(raw, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "cfg.json")
			if err := os.WriteFile(path, []byte(`{
				"base_url": "https://example/v1",
				"model": "demo",
				"max_sessions": `+raw+`
			}`), 0o600); err != nil {
				t.Fatal(err)
			}
			cfg, err := LoadConfig(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := cfg.Resolve(); err != nil {
				t.Fatal(err)
			}
			err = cfg.Validate()
			if err == nil || !strings.Contains(err.Error(), "max_sessions") {
				t.Fatalf("Validate error = %v, want max_sessions", err)
			}
		})
	}
}

func TestConfig_Validate_RejectsExplicitNonPositiveSubagentMaxDepth(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.json")
	if err := os.WriteFile(path, []byte(`{
		"base_url": "https://example/v1",
		"model": "demo",
		"subagent_max_depth": 0
	}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Resolve(); err != nil {
		t.Fatal(err)
	}
	err = cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "subagent_max_depth") {
		t.Fatalf("Validate error = %v, want subagent_max_depth", err)
	}
}

func TestConfig_Validate_RejectsNegativeTurnAndCompactLimits(t *testing.T) {
	for _, c := range []struct {
		name string
		edit func(*Config)
		want string
	}{
		{
			name: "max turn steps",
			edit: func(cfg *Config) { cfg.MaxTurnSteps = -1 },
			want: "max_turn_steps",
		},
		{
			name: "compact trigger",
			edit: func(cfg *Config) { cfg.CompactTrigger = -1 },
			want: "compact_trigger",
		},
		{
			name: "compact keep last",
			edit: func(cfg *Config) { cfg.CompactKeepLast = -1 },
			want: "compact_keep_last",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			cfg := Config{BaseURL: "https://example/v1", Model: "demo"}
			if err := cfg.Resolve(); err != nil {
				t.Fatal(err)
			}
			c.edit(&cfg)
			err := cfg.Validate()
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("Validate error = %v, want contains %q", err, c.want)
			}
		})
	}
}

func TestConfig_Resolve_PreservesExplicitSubagentAndMemoryFalse(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.json")
	if err := os.WriteFile(path, []byte(`{
        "base_url": "https://example/v1",
        "model": "demo",
        "enable_memory": false,
        "enable_subagent": false,
        "enable_focused_tasks": false
    }`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Resolve(); err != nil {
		t.Fatal(err)
	}
	if cfg.EnableMemory {
		t.Fatal("explicit enable_memory:false should survive Resolve")
	}
	if cfg.EnableSubagent {
		t.Fatal("explicit enable_subagent:false should survive Resolve")
	}
	if cfg.EnableFocusedTasks {
		t.Fatal("explicit enable_focused_tasks:false should survive Resolve")
	}
}

func TestConfig_ValidateEvalModeIgnoresDisabledNetworkDeps(t *testing.T) {
	cfg := Config{
		BaseURL:                   "https://example/v1",
		Model:                     "demo",
		MaxSessions:               1,
		SessionIdleTTL:            "5m",
		PerCallTimeout:            "3m",
		RetryBackoff:              "4s",
		SubagentMaxDepth:          agent.DefaultSubagentMaxDepth,
		EvalMode:                  true,
		EnableWebSearch:           true,
		BrowserScreenshot:         true,
		BrowserCacheDir:           filepath.Join(t.TempDir(), "cache"),
		BrowserCacheTTL:           "1h",
		BrowserCacheSweepInterval: "5m",
		BrowserNoStealth:          true,
		BrowserAllowAllDomains:    true,
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("eval mode should disable non-basic surfaces before validation, got %v", err)
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

func TestConfig_Validate_RequiresAPIKeyForDefaultOpenAIEndpoint(t *testing.T) {
	cfg := Config{BaseURL: agent.DefaultBaseURL + "/", Model: "gpt-4o-mini"}
	if err := cfg.Resolve(); err != nil {
		t.Fatal(err)
	}
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "api_key") || !strings.Contains(err.Error(), agent.DefaultBaseURL) {
		t.Fatalf("expected api_key validation error for default endpoint, got %v", err)
	}
}

func TestConfig_Validate_AllowsKeylessCustomEndpoint(t *testing.T) {
	cfg := Config{BaseURL: "http://127.0.0.1:11434/v1", Model: "local-model"}
	if err := cfg.Resolve(); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("custom keyless endpoint should be allowed: %v", err)
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

// TestConfig_RetryBackoff_HappyPath mirrors the PerCallTimeout
// happy-path coverage for the second new duration knob. Empty → 0
// (Loop falls back to agent.DefaultTransientBackoff). Non-positive
// and unparseable both error so a typo at deploy time surfaces
// before a flaky upstream silently uses the 4-second default.
func TestConfig_RetryBackoff_HappyPath(t *testing.T) {
	cases := []struct {
		in      string
		want    time.Duration
		wantErr bool
	}{
		{"", 0, false},
		{"1s", time.Second, false},
		{"30s", 30 * time.Second, false},
		{"0s", 0, true},
		{"-1s", 0, true},
		{"nope", 0, true},
	}
	for _, c := range cases {
		cfg := Config{RetryBackoff: c.in}
		d, err := cfg.RetryBackoffDuration()
		if c.wantErr {
			if err == nil {
				t.Errorf("RetryBackoffDuration(%q) should error", c.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("RetryBackoffDuration(%q): %v", c.in, err)
		}
		if d != c.want {
			t.Errorf("RetryBackoffDuration(%q) = %s, want %s", c.in, d, c.want)
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

func TestConfig_Validate_RejectsBadSampling(t *testing.T) {
	tempHigh := 2.1
	tempNaN := math.NaN()
	topHigh := 1.1
	topInf := math.Inf(1)
	zeroTokens := 0
	negTokens := -1
	cases := []struct {
		name string
		edit func(*Config)
		want string
	}{
		{name: "temperature high", edit: func(c *Config) { c.Temperature = &tempHigh }, want: "temperature must be between 0 and 2"},
		{name: "temperature NaN", edit: func(c *Config) { c.Temperature = &tempNaN }, want: "temperature must be between 0 and 2"},
		{name: "top_p high", edit: func(c *Config) { c.TopP = &topHigh }, want: "top_p must be between 0 and 1"},
		{name: "top_p inf", edit: func(c *Config) { c.TopP = &topInf }, want: "top_p must be between 0 and 1"},
		{name: "max_tokens zero", edit: func(c *Config) { c.MaxTokens = &zeroTokens }, want: "max_tokens must be a positive integer"},
		{name: "max_tokens negative", edit: func(c *Config) { c.MaxTokens = &negTokens }, want: "max_tokens must be a positive integer"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Config{BaseURL: "https://example/v1", Model: "m"}
			if err := cfg.Resolve(); err != nil {
				t.Fatal(err)
			}
			tc.edit(&cfg)
			err := cfg.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Validate error = %v, want contains %q", err, tc.want)
			}
		})
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
	for _, in := range []string{"30d", "0s", "-1h"} {
		cfg := Config{SessionRetention: in}
		_, err := cfg.Retention()
		if err == nil {
			t.Errorf("Retention(%q) should error", in)
		}
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
	for _, in := range []string{"not-a-duration", "0s", "-1s"} {
		cfg := Config{BaseURL: "https://example/v1", SessionIdleTTL: in}
		_, err := cfg.IdleTTL()
		if err == nil {
			t.Errorf("IdleTTL(%q) should error", in)
		}
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

func TestConfig_BrowserCacheDurations(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		cfg := Config{}
		ttl, err := cfg.BrowserCacheTTLDuration()
		if err != nil {
			t.Fatal(err)
		}
		if ttl != defaultBrowserCacheTTL {
			t.Fatalf("BrowserCacheTTLDuration default = %s, want %s", ttl, defaultBrowserCacheTTL)
		}
		sweep, err := cfg.BrowserCacheSweepIntervalDuration(ttl)
		if err != nil {
			t.Fatal(err)
		}
		if sweep != 3*time.Hour {
			t.Fatalf("BrowserCacheSweepIntervalDuration default = %s, want 3h", sweep)
		}
	})

	t.Run("zero ttl disables expiry", func(t *testing.T) {
		cfg := Config{BrowserCacheTTL: "0s"}
		ttl, err := cfg.BrowserCacheTTLDuration()
		if err != nil {
			t.Fatal(err)
		}
		if ttl != 0 {
			t.Fatalf("BrowserCacheTTLDuration zero = %s, want 0", ttl)
		}
		sweep, err := cfg.BrowserCacheSweepIntervalDuration(ttl)
		if err != nil {
			t.Fatal(err)
		}
		if sweep != 0 {
			t.Fatalf("BrowserCacheSweepIntervalDuration disabled = %s, want 0", sweep)
		}
	})

	t.Run("rejects bad values", func(t *testing.T) {
		cases := []struct {
			name string
			cfg  Config
			want string
		}{
			{name: "bad ttl", cfg: Config{BrowserCacheTTL: "nope"}, want: "browser_cache_ttl"},
			{name: "negative ttl", cfg: Config{BrowserCacheTTL: "-1s"}, want: "must be zero or positive"},
			{name: "bad sweep", cfg: Config{BrowserCacheSweepInterval: "nope"}, want: "browser_cache_sweep_interval"},
			{name: "zero sweep", cfg: Config{BrowserCacheSweepInterval: "0s"}, want: "at least"},
			{name: "short sweep", cfg: Config{BrowserCacheSweepInterval: "1m"}, want: "at least"},
			{name: "sweep with no expiry", cfg: Config{BrowserCacheTTL: "0s", BrowserCacheSweepInterval: "10m"}, want: "positive browser_cache_ttl"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				ttl, err := tc.cfg.BrowserCacheTTLDuration()
				if err == nil {
					_, err = tc.cfg.BrowserCacheSweepIntervalDuration(ttl)
				}
				if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("error = %v, want contains %q", err, tc.want)
				}
			})
		}
	})
}

func TestConfig_Validate_RejectsUnusedBrowserCacheDurations(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
		want string
	}{
		{
			name: "cache dir without browser",
			cfg:  Config{BaseURL: "https://example/v1", Model: "m", BrowserCacheDir: t.TempDir()},
			want: "browser_cache_dir requires enable_browser",
		},
		{
			name: "ttl without cache dir",
			cfg:  Config{BaseURL: "https://example/v1", Model: "m", EnableBrowser: true, BrowserCacheTTL: "1h"},
			want: "browser_cache_ttl requires browser_cache_dir",
		},
		{
			name: "sweep without cache dir",
			cfg:  Config{BaseURL: "https://example/v1", Model: "m", EnableBrowser: true, BrowserCacheSweepInterval: "10m"},
			want: "browser_cache_sweep_interval requires browser_cache_dir",
		},
		{
			name: "invalid sweep with cache dir",
			cfg: Config{
				BaseURL:                   "https://example/v1",
				Model:                     "m",
				EnableBrowser:             true,
				BrowserCacheDir:           t.TempDir(),
				BrowserCacheSweepInterval: "1m",
			},
			want: "at least",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.cfg.Resolve(); err != nil {
				t.Fatal(err)
			}
			err := tc.cfg.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Validate error = %v, want contains %q", err, tc.want)
			}
		})
	}
}

func TestConfig_Validate_RejectsUnusedFeatureSubOptions(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
		want string
	}{
		{
			name: "web search without web",
			cfg:  Config{BaseURL: "https://example/v1", Model: "m", EnableWebSearch: true},
			want: "enable_web_search requires enable_web",
		},
		{
			name: "browser screenshot without browser",
			cfg:  Config{BaseURL: "https://example/v1", Model: "m", BrowserScreenshot: true},
			want: "browser_screenshot requires enable_browser",
		},
		{
			name: "browser no stealth without browser",
			cfg:  Config{BaseURL: "https://example/v1", Model: "m", BrowserNoStealth: true},
			want: "browser_no_stealth requires enable_browser",
		},
		{
			name: "browser allow all without browser",
			cfg:  Config{BaseURL: "https://example/v1", Model: "m", BrowserAllowAllDomains: true},
			want: "browser_allow_all_domains requires enable_browser",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.cfg.Resolve(); err != nil {
				t.Fatal(err)
			}
			err := tc.cfg.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Validate error = %v, want contains %q", err, tc.want)
			}
		})
	}
}
