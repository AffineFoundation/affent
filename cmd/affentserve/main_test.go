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
