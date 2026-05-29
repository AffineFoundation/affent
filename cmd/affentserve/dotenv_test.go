package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDotEnvFile_ParsesAndSetsUnsetKeys(t *testing.T) {
	preserveEnv(t, "AFFENTSERVE_BASE_URL", "AFFENTSERVE_API_KEY", "AFFENTSERVE_AUTH_TOKEN")
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

func preserveEnv(t *testing.T, keys ...string) {
	t.Helper()
	type entry struct {
		key string
		val string
		set bool
	}
	entries := make([]entry, 0, len(keys))
	for _, key := range keys {
		val, set := os.LookupEnv(key)
		entries = append(entries, entry{key: key, val: val, set: set})
	}
	t.Cleanup(func() {
		for _, e := range entries {
			if e.set {
				_ = os.Setenv(e.key, e.val)
			} else {
				_ = os.Unsetenv(e.key)
			}
		}
	})
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
