package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAccountSettingsEnvSetListDeleteWithoutValueLeak(t *testing.T) {
	pool := newPoolWithMemoryRoot(t, t.TempDir())
	r := httptest.NewRequest(http.MethodPost, "/v1/settings/env", bytes.NewBufferString(`{"name":"GITHUB_TOKEN","value":"ghp_secret"}`))
	w := httptest.NewRecorder()
	handleAccountSettingsRoutes(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusOK {
		t.Fatalf("set env status = %d, want 200; body=%s", got, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "ghp_secret") {
		t.Fatalf("settings response leaked env value: %s", w.Body.String())
	}
	var resp accountSettingsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode settings: %v", err)
	}
	if len(resp.Env) != 1 || resp.Env[0].Name != "GITHUB_TOKEN" || !resp.Env[0].Configured || resp.Env[0].UpdatedAt == "" {
		t.Fatalf("env summary = %+v, want configured GITHUB_TOKEN", resp.Env)
	}
	if got := pool.accountEnvPairs(); len(got) != 1 || got[0] != "GITHUB_TOKEN=ghp_secret" {
		t.Fatalf("account env pairs = %+v", got)
	}

	r = httptest.NewRequest(http.MethodGet, "/v1/settings", nil)
	w = httptest.NewRecorder()
	handleAccountSettings(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusOK {
		t.Fatalf("get settings status = %d, want 200; body=%s", got, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "ghp_secret") {
		t.Fatalf("GET settings leaked env value: %s", w.Body.String())
	}

	r = httptest.NewRequest(http.MethodDelete, "/v1/settings/env/GITHUB_TOKEN", nil)
	w = httptest.NewRecorder()
	handleAccountSettingsRoutes(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusOK {
		t.Fatalf("delete env status = %d, want 200; body=%s", got, w.Body.String())
	}
	if got := pool.accountEnvPairs(); len(got) != 0 {
		t.Fatalf("account env pairs after delete = %+v, want empty", got)
	}
}

func TestAccountSettingsRejectsInvalidEnvName(t *testing.T) {
	pool := newPoolWithMemoryRoot(t, t.TempDir())
	r := httptest.NewRequest(http.MethodPost, "/v1/settings/env", bytes.NewBufferString(`{"name":"BAD NAME","value":"x"}`))
	w := httptest.NewRecorder()
	handleAccountSettingsRoutes(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", got, w.Body.String())
	}
}

func TestAccountSettingsSSHKeyGeneratesAndThenShowsExisting(t *testing.T) {
	t.Setenv("GIT_SSH_COMMAND", "")
	pool := newPoolWithMemoryRoot(t, t.TempDir())
	r := httptest.NewRequest(http.MethodPost, "/v1/settings/ssh-key", nil)
	w := httptest.NewRecorder()
	handleAccountSettingsRoutes(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusOK {
		t.Fatalf("generate ssh status = %d, want 200; body=%s", got, w.Body.String())
	}
	var first accountSettingsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &first); err != nil {
		t.Fatalf("decode first: %v", err)
	}
	if !first.SSH.Exists || !first.SSH.Created || !strings.HasPrefix(first.SSH.PublicKey, "ssh-ed25519 ") {
		t.Fatalf("first ssh = %+v, want generated public key", first.SSH)
	}
	privatePath, publicPath := accountSSHKeyPaths(pool)
	privateInfo, err := os.Stat(privatePath)
	if err != nil {
		t.Fatalf("stat private key: %v", err)
	}
	if privateInfo.Mode().Perm() != 0o600 {
		t.Fatalf("private key perm = %v, want 0600", privateInfo.Mode().Perm())
	}
	if _, err := os.Stat(publicPath); err != nil {
		t.Fatalf("stat public key: %v", err)
	}

	r = httptest.NewRequest(http.MethodPost, "/v1/settings/ssh-key", nil)
	w = httptest.NewRecorder()
	handleAccountSettingsRoutes(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusOK {
		t.Fatalf("second ssh status = %d, want 200; body=%s", got, w.Body.String())
	}
	var second accountSettingsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &second); err != nil {
		t.Fatalf("decode second: %v", err)
	}
	if second.SSH.Created {
		t.Fatalf("second ssh = %+v, want existing key without regeneration", second.SSH)
	}
	if second.SSH.PublicKey != first.SSH.PublicKey {
		t.Fatalf("public key changed on second ensure:\nfirst=%s\nsecond=%s", first.SSH.PublicKey, second.SSH.PublicKey)
	}
	pairs := pool.accountEnvPairs()
	gitSSHCommand := findEnvPairValue(pairs, "GIT_SSH_COMMAND")
	if gitSSHCommand == "" {
		t.Fatalf("account env pairs = %+v, want GIT_SSH_COMMAND for generated key", pairs)
	}
	if !strings.Contains(gitSSHCommand, privatePath) || !strings.Contains(gitSSHCommand, "IdentitiesOnly=yes") {
		t.Fatalf("GIT_SSH_COMMAND = %q, want generated private key command", gitSSHCommand)
	}
}

func TestAccountSettingsEnvOverridesGeneratedGitSSHCommand(t *testing.T) {
	t.Setenv("GIT_SSH_COMMAND", "")
	pool := newPoolWithMemoryRoot(t, t.TempDir())
	if _, err := ensureAccountSSHKey(pool); err != nil {
		t.Fatalf("ensure ssh key: %v", err)
	}
	if err := setAccountEnv(pool, "GIT_SSH_COMMAND", "ssh -i /custom/key"); err != nil {
		t.Fatalf("set env: %v", err)
	}
	pairs := pool.accountEnvPairs()
	if got := findEnvPairValue(pairs, "GIT_SSH_COMMAND"); got != "ssh -i /custom/key" {
		t.Fatalf("GIT_SSH_COMMAND = %q from %+v, want custom override", got, pairs)
	}
}

func TestAccountSettingsSSHKeyDoesNotOverwriteExistingKey(t *testing.T) {
	pool := newPoolWithMemoryRoot(t, t.TempDir())
	privatePath, publicPath := accountSSHKeyPaths(pool)
	if err := os.MkdirAll(filepath.Dir(privatePath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(privatePath, []byte("existing-private\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(publicPath, []byte("ssh-ed25519 existing affent\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	r := httptest.NewRequest(http.MethodPost, "/v1/settings/ssh-key", nil)
	w := httptest.NewRecorder()
	handleAccountSettingsRoutes(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", got, w.Body.String())
	}
	var resp accountSettingsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.SSH.Created || resp.SSH.PublicKey != "ssh-ed25519 existing affent" {
		t.Fatalf("ssh = %+v, want existing public key without overwrite", resp.SSH)
	}
	privateRaw, err := os.ReadFile(privatePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(privateRaw) != "existing-private\n" {
		t.Fatalf("private key was overwritten: %q", string(privateRaw))
	}
}

func TestAccountSettingsSSHKeyRefusesPrivateOnlyOverwrite(t *testing.T) {
	pool := newPoolWithMemoryRoot(t, t.TempDir())
	privatePath, _ := accountSSHKeyPaths(pool)
	if err := os.MkdirAll(filepath.Dir(privatePath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(privatePath, []byte("existing-private\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	r := httptest.NewRequest(http.MethodPost, "/v1/settings/ssh-key", nil)
	w := httptest.NewRecorder()
	handleAccountSettingsRoutes(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body=%s", got, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "refusing to overwrite") {
		t.Fatalf("body missing no-overwrite guidance: %s", w.Body.String())
	}
}

func findEnvPairValue(pairs []string, name string) string {
	prefix := name + "="
	for _, pair := range pairs {
		if strings.HasPrefix(pair, prefix) {
			return strings.TrimPrefix(pair, prefix)
		}
	}
	return ""
}
