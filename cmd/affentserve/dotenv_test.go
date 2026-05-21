package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDotEnvFile_ParsesAndSetsUnsetKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	content := `# header
AFFENTSERVE_BASE_URL=https://example.com/v1
AFFENTSERVE_API_KEY="sk-abcdef"
export AFFENTSERVE_AUTH_TOKEN='token-xyz'
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{
		"AFFENTSERVE_BASE_URL", "AFFENTSERVE_API_KEY", "AFFENTSERVE_AUTH_TOKEN",
	} {
		os.Unsetenv(k)
	}
	if err := loadDotEnvFile(path); err != nil {
		t.Fatalf("loadDotEnvFile: %v", err)
	}
	if got := os.Getenv("AFFENTSERVE_BASE_URL"); got != "https://example.com/v1" {
		t.Errorf("BASE_URL = %q", got)
	}
	if got := os.Getenv("AFFENTSERVE_API_KEY"); got != "sk-abcdef" {
		t.Errorf("API_KEY = %q", got)
	}
	if got := os.Getenv("AFFENTSERVE_AUTH_TOKEN"); got != "token-xyz" {
		t.Errorf("AUTH_TOKEN = %q", got)
	}
}

func TestLoadDotEnvFile_ShellEnvWinsOverFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte("AFFENTSERVE_API_KEY=from-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AFFENTSERVE_API_KEY", "from-shell")
	if err := loadDotEnvFile(path); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("AFFENTSERVE_API_KEY"); got != "from-shell" {
		t.Errorf("shell env should win; got %q", got)
	}
}

func TestLoadDotEnvFile_MissingIsNoOp(t *testing.T) {
	if err := loadDotEnvFile(filepath.Join(t.TempDir(), "no-such")); err != nil {
		t.Errorf("missing .env should be silent: %v", err)
	}
}
