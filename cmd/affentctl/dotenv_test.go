package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDotEnvFile_ParsesAndSetsUnsetKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	content := `# header comment
AFFENTCTL_BASE_URL=https://example.com/v1
AFFENTCTL_API_KEY="sk-abcdef"
export AFFENTCTL_MODEL='qwen-plus'

# trailing comment
EMPTY_VALUE=
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{
		"AFFENTCTL_BASE_URL", "AFFENTCTL_API_KEY", "AFFENTCTL_MODEL", "EMPTY_VALUE",
	} {
		t.Setenv(k, "") // start unset (Setenv "" still counts as set on some platforms; clear via Unsetenv)
		os.Unsetenv(k)
	}

	if err := loadDotEnvFile(path); err != nil {
		t.Fatalf("loadDotEnvFile: %v", err)
	}
	if got := os.Getenv("AFFENTCTL_BASE_URL"); got != "https://example.com/v1" {
		t.Errorf("BASE_URL = %q", got)
	}
	if got := os.Getenv("AFFENTCTL_API_KEY"); got != "sk-abcdef" {
		t.Errorf("API_KEY = %q (quote stripping)", got)
	}
	if got := os.Getenv("AFFENTCTL_MODEL"); got != "qwen-plus" {
		t.Errorf("MODEL = %q (export + single-quote stripping)", got)
	}
	if _, set := os.LookupEnv("EMPTY_VALUE"); !set {
		t.Errorf("EMPTY_VALUE should be set even when value is empty")
	}
}

// Shell env must win over .env so users can override per-command.
func TestLoadDotEnvFile_ShellEnvWinsOverFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte("AFFENTCTL_API_KEY=from-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AFFENTCTL_API_KEY", "from-shell")
	if err := loadDotEnvFile(path); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("AFFENTCTL_API_KEY"); got != "from-shell" {
		t.Errorf("shell env should win; got %q", got)
	}
}

// A missing file is not an error — most environments won't have one.
func TestLoadDotEnvFile_MissingIsNoOp(t *testing.T) {
	if err := loadDotEnvFile(filepath.Join(t.TempDir(), "does-not-exist")); err != nil {
		t.Errorf("missing .env should be silent: %v", err)
	}
}
