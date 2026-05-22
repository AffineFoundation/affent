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
