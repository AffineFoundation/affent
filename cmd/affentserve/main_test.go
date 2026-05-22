package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestParseFlagsAndConfig_CLIBoolCanOverrideFileTrue pins the
// override semantics for bool flags: if the config file enables a
// feature, the user must still be able to disable it for one run
// with --feature=false on the CLI. Pre-fix the override logic
// checked the flag's resolved string value ("true"), which silently
// dropped any explicit-false override and left the file's true
// active.
func TestParseFlagsAndConfig_CLIBoolCanOverrideFileTrue(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "cfg.json")
	if err := os.WriteFile(cfgPath, []byte(`{
        "listen": "127.0.0.1:9000",
        "base_url": "https://example/v1",
        "model": "demo",
        "enable_browser": true,
        "enable_memory":  true,
        "enable_builtins": true
    }`), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := parseFlagsAndConfig([]string{
		"--config", cfgPath,
		"--browser=false",
		"--memory=false",
		"--builtins=false",
	})
	if err != nil {
		t.Fatalf("parseFlagsAndConfig: %v", err)
	}
	if cfg.EnableBrowser {
		t.Errorf("--browser=false must override file's true (got EnableBrowser=true)")
	}
	if cfg.EnableMemory {
		t.Errorf("--memory=false must override file's true (got EnableMemory=true)")
	}
	if cfg.EnableBuiltins {
		t.Errorf("--builtins=false must override file's true (got EnableBuiltins=true)")
	}
}

// TestParseFlagsAndConfig_SubagentIsIndependentFromBuiltins pins the
// new gating: subagent_run registration must be controlled by its
// own EnableSubagent flag, not coupled to EnableBuiltins. An
// operator wanting bounded read-only delegation without exposing
// the parent agent's shell / file-write builtins should be able to
// say --subagent --builtins=false and get exactly that.
func TestParseFlagsAndConfig_SubagentIsIndependentFromBuiltins(t *testing.T) {
	cfg, err := parseFlagsAndConfig([]string{
		"--base-url", "https://example/v1",
		"--model", "demo",
		"--subagent",
		"--builtins=false",
	})
	if err != nil {
		t.Fatalf("parseFlagsAndConfig: %v", err)
	}
	if !cfg.EnableSubagent {
		t.Error("--subagent must enable EnableSubagent")
	}
	if cfg.EnableBuiltins {
		t.Error("--builtins=false must leave EnableBuiltins off — subagent should not pull it in")
	}
}

// TestParseFlagsAndConfig_ModelFromEnv pins the env fallback for the
// required Model field. affentserve already honored AFFENTSERVE_BASE_URL
// when neither --base-url nor the config file set it, but Model had no
// env path even though it's required at startup. Container deploys
// expect both vars to work the same way; without this an operator
// running `docker run -e AFFENTSERVE_MODEL=...` got "model is required"
// at boot.
func TestParseFlagsAndConfig_ModelFromEnv(t *testing.T) {
	t.Setenv("AFFENTSERVE_BASE_URL", "https://example/v1")
	t.Setenv("AFFENTSERVE_MODEL", "qwen-plus")
	cfg, err := parseFlagsAndConfig(nil) // no CLI args, no config file
	if err != nil {
		t.Fatalf("parseFlagsAndConfig: %v", err)
	}
	if cfg.Model != "qwen-plus" {
		t.Errorf("AFFENTSERVE_MODEL env should set cfg.Model; got %q", cfg.Model)
	}
}

// TestParseFlagsAndConfig_EnvBeatsConfigFile pins the documented
// precedence after the Resolve re-ordering: env > config file. The
// previous code applied env only when both CLI and config left a
// field empty, which meant a deploy overriding a stale config-file
// model via AFFENTSERVE_MODEL silently kept the config value.
// affentctl's docs and the README both promise the 12factor-style
// "env beats config" ordering, so affentserve has to match.
func TestParseFlagsAndConfig_EnvBeatsConfigFile(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "cfg.json")
	if err := os.WriteFile(cfgPath, []byte(`{
        "base_url": "https://config-host/v1",
        "model":    "config-model",
        "api_key":  "config-key"
    }`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AFFENTSERVE_BASE_URL", "https://env-host/v1")
	t.Setenv("AFFENTSERVE_MODEL", "env-model")
	t.Setenv("AFFENTSERVE_API_KEY", "env-key")

	cfg, err := parseFlagsAndConfig([]string{"--config", cfgPath})
	if err != nil {
		t.Fatalf("parseFlagsAndConfig: %v", err)
	}
	if cfg.BaseURL != "https://env-host/v1" {
		t.Errorf("env should override config base_url; got %q", cfg.BaseURL)
	}
	if cfg.Model != "env-model" {
		t.Errorf("env should override config model; got %q", cfg.Model)
	}
	if cfg.APIKey != "env-key" {
		t.Errorf("env should override config api_key; got %q", cfg.APIKey)
	}
}

// TestParseFlagsAndConfig_CLIBeatsEnv pins the top of the precedence
// chain: --model on the command line wins over AFFENTSERVE_MODEL
// even when env is also set. Standard CLI-tops-everything posture.
func TestParseFlagsAndConfig_CLIBeatsEnv(t *testing.T) {
	t.Setenv("AFFENTSERVE_BASE_URL", "https://env/v1")
	t.Setenv("AFFENTSERVE_MODEL", "env-model")
	cfg, err := parseFlagsAndConfig([]string{
		"--model", "cli-model",
	})
	if err != nil {
		t.Fatalf("parseFlagsAndConfig: %v", err)
	}
	if cfg.Model != "cli-model" {
		t.Errorf("--model should win over AFFENTSERVE_MODEL; got %q", cfg.Model)
	}
}

// TestParseFlagsAndConfig_RetryFlagsReachConfig pins both new retry
// knobs end-to-end. --max-transient-retries=-1 is the "disable
// retries" case (some providers handle retries themselves and a
// double-retry doubles spend on a flaky day); the explicit int
// must reach cfg.MaxTransientRetries unchanged so Loop's negative
// → disable path fires. --retry-backoff is a Go duration string
// that just needs to survive parseFlagsAndConfig intact (parsing
// happens lazily via cfg.RetryBackoffDuration()).
func TestParseFlagsAndConfig_RetryFlagsReachConfig(t *testing.T) {
	cfg, err := parseFlagsAndConfig([]string{
		"--base-url", "https://example/v1",
		"--model", "m",
		"--max-transient-retries", "-1",
		"--retry-backoff", "8s",
	})
	if err != nil {
		t.Fatalf("parseFlagsAndConfig: %v", err)
	}
	if cfg.MaxTransientRetries != -1 {
		t.Errorf("MaxTransientRetries = %d, want -1", cfg.MaxTransientRetries)
	}
	if cfg.RetryBackoff != "8s" {
		t.Errorf("RetryBackoff = %q, want 8s", cfg.RetryBackoff)
	}
}

// TestParseFlagsAndConfig_CLIBoolUnsetKeepsFileValue pins the
// opposite direction: when the user does NOT pass a bool flag, the
// file's value stays intact.
func TestParseFlagsAndConfig_CLIBoolUnsetKeepsFileValue(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "cfg.json")
	if err := os.WriteFile(cfgPath, []byte(`{
        "listen": "127.0.0.1:9000",
        "base_url": "https://example/v1",
        "model": "demo",
        "enable_browser": true
    }`), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := parseFlagsAndConfig([]string{"--config", cfgPath})
	if err != nil {
		t.Fatalf("parseFlagsAndConfig: %v", err)
	}
	if !cfg.EnableBrowser {
		t.Errorf("unset --browser must keep file's true (got EnableBrowser=false)")
	}
}
