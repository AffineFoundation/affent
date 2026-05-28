package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

func newPoolWithAccountHome(t *testing.T) (*SessionPool, string) {
	t.Helper()
	home := filepath.Join(t.TempDir(), "home")
	t.Setenv("HOME", home)
	return newPoolWithMemoryRoot(t, t.TempDir()), home
}

func TestAccountSettingsEnvSetListDeleteWithoutValueLeak(t *testing.T) {
	pool, _ := newPoolWithAccountHome(t)
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
	if got := pool.accountSecretValues(); !stringSliceContains(got, "ghp_secret") {
		t.Fatalf("account secret values = %+v, want the stored secret for redaction", got)
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
	pool, _ := newPoolWithAccountHome(t)
	r := httptest.NewRequest(http.MethodPost, "/v1/settings/env", bytes.NewBufferString(`{"name":"BAD NAME","value":"x"}`))
	w := httptest.NewRecorder()
	handleAccountSettingsRoutes(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", got, w.Body.String())
	}
}

func TestAccountSettingsSSHKeyGeneratesAndThenShowsExisting(t *testing.T) {
	t.Setenv("GIT_SSH_COMMAND", "")
	pool, home := newPoolWithAccountHome(t)
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
	if privatePath != filepath.Join(home, ".ssh", accountSSHKeyName) {
		t.Fatalf("private key path = %q, want standard ~/.ssh path under %q", privatePath, home)
	}
	if strings.Contains(w.Body.String(), "private_key_path") || strings.Contains(w.Body.String(), strconv.Quote(privatePath)) {
		t.Fatalf("generate ssh response leaked private key path: %s", w.Body.String())
	}
	privateInfo, err := os.Stat(privatePath)
	if err != nil {
		t.Fatalf("stat private key: %v", err)
	}
	if privateInfo.Mode().Perm() != 0o600 {
		t.Fatalf("private key perm = %v, want 0600", privateInfo.Mode().Perm())
	}
	privateRaw, err := os.ReadFile(privatePath)
	if err != nil {
		t.Fatalf("read private key: %v", err)
	}
	if !bytes.Contains(privateRaw, []byte("BEGIN OPENSSH PRIVATE KEY")) {
		t.Fatalf("private key should be OpenSSH format, got header %q", firstLine(privateRaw))
	}
	if _, err := os.Stat(publicPath); err != nil {
		t.Fatalf("stat public key: %v", err)
	}
	if first.SSH.PublicKeyPath != publicPath {
		t.Fatalf("public key path = %q, want %q", first.SSH.PublicKeyPath, publicPath)
	}
	if !strings.Contains(w.Body.String(), `"public_key_path"`) || !strings.Contains(w.Body.String(), publicPath) {
		t.Fatalf("generate ssh response missing public key path: %s", w.Body.String())
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
	if second.SSH.PublicKeyPath != publicPath {
		t.Fatalf("second public key path = %q, want %q", second.SSH.PublicKeyPath, publicPath)
	}
	if strings.Contains(w.Body.String(), "private_key_path") || strings.Contains(w.Body.String(), strconv.Quote(privatePath)) {
		t.Fatalf("existing ssh response leaked private key path: %s", w.Body.String())
	}
	pairs := pool.accountEnvPairs()
	gitSSHCommand := findEnvPairValue(pairs, "GIT_SSH_COMMAND")
	if gitSSHCommand == "" {
		t.Fatalf("account env pairs = %+v, want GIT_SSH_COMMAND for generated key", pairs)
	}
	if !strings.Contains(gitSSHCommand, privatePath) || !strings.Contains(gitSSHCommand, "IdentitiesOnly=yes") {
		t.Fatalf("GIT_SSH_COMMAND = %q, want generated private key command", gitSSHCommand)
	}
	secrets := pool.accountSecretValues()
	if !stringSliceContains(secrets, gitSSHCommand) || !stringSliceContains(secrets, privatePath) {
		t.Fatalf("account secret values = %+v, want generated GIT_SSH_COMMAND and private key path", secrets)
	}
	block := pool.accountAccessSystemBlock()
	for _, want := range []string{"AFFENT ACCOUNT ACCESS:", "SSH public key is configured"} {
		if !strings.Contains(block, want) {
			t.Fatalf("account access block missing %q:\n%s", want, block)
		}
	}
	for _, leaked := range []string{gitSSHCommand, privatePath, first.SSH.PublicKey} {
		if strings.Contains(block, leaked) {
			t.Fatalf("account access block leaked sensitive detail %q:\n%s", leaked, block)
		}
	}
}

func TestAccountSettingsSSHKeyUsesStandardHomeSSHDir(t *testing.T) {
	accountRoot := filepath.Join(t.TempDir(), "isolated-account")
	pool, home := newPoolWithAccountHome(t)
	pool.cfg.AccountRoot = accountRoot

	info, err := ensureAccountSSHKey(pool)
	if err != nil {
		t.Fatalf("ensure ssh key: %v", err)
	}
	if !info.Exists || !info.Created {
		t.Fatalf("ssh info = %+v, want created key", info)
	}
	privatePath, publicPath := accountSSHKeyPaths(pool)
	if privatePath != filepath.Join(home, ".ssh", accountSSHKeyName) {
		t.Fatalf("private key path = %q, want standard ~/.ssh path under %q", privatePath, home)
	}
	if strings.HasPrefix(privatePath, accountRoot+string(os.PathSeparator)) {
		t.Fatalf("private key path = %q, must not be under account root %q", privatePath, accountRoot)
	}
	if _, err := os.Stat(privatePath); err != nil {
		t.Fatalf("stat private key: %v", err)
	}
	if _, err := os.Stat(publicPath); err != nil {
		t.Fatalf("stat public key: %v", err)
	}
}

func TestAccountSettingsSSHKeyMigratesLegacyAccountRootKeyToHomeSSH(t *testing.T) {
	t.Setenv("GIT_SSH_COMMAND", "")
	pool, home := newPoolWithAccountHome(t)
	pool.cfg.AccountRoot = filepath.Join(t.TempDir(), "isolated-account")
	legacyPrivatePath, legacyPublicPath := legacyAccountSSHKeyPaths(pool)
	if err := os.MkdirAll(filepath.Dir(legacyPrivatePath), 0o700); err != nil {
		t.Fatal(err)
	}
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	legacyPrivate := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	legacyPublic := marshalOpenSSHEd25519PublicKey(publicKey, "affentserve")
	if err := os.WriteFile(legacyPrivatePath, legacyPrivate, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacyPublicPath, []byte(legacyPublic+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	r := httptest.NewRequest(http.MethodGet, "/v1/settings", nil)
	w := httptest.NewRecorder()
	handleAccountSettings(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusOK {
		t.Fatalf("get settings status = %d, want 200; body=%s", got, w.Body.String())
	}
	var resp accountSettingsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode settings: %v", err)
	}
	standardPrivatePath, standardPublicPath := accountSSHKeyPaths(pool)
	if standardPrivatePath != filepath.Join(home, ".ssh", accountSSHKeyName) {
		t.Fatalf("standard private path = %q, want under home %q", standardPrivatePath, home)
	}
	if resp.SSH.PublicKeyPath != standardPublicPath || resp.SSH.PublicKey != legacyPublic {
		t.Fatalf("ssh response = %+v, want migrated public key at %q", resp.SSH, standardPublicPath)
	}
	privateRaw, err := os.ReadFile(standardPrivatePath)
	if err != nil {
		t.Fatalf("read standard private key: %v", err)
	}
	if !bytes.Contains(privateRaw, []byte("BEGIN OPENSSH PRIVATE KEY")) {
		t.Fatalf("standard private key should be converted to OpenSSH format, got header %q", firstLine(privateRaw))
	}
	if got := findEnvPairValue(pool.accountEnvPairs(), "GIT_SSH_COMMAND"); !strings.Contains(got, standardPrivatePath) {
		t.Fatalf("GIT_SSH_COMMAND = %q, want standard private key path %q", got, standardPrivatePath)
	}
	if _, err := os.Stat(legacyPrivatePath); !os.IsNotExist(err) {
		t.Fatalf("legacy private key should be removed after migration, stat err=%v", err)
	}
	if _, err := os.Stat(legacyPublicPath); !os.IsNotExist(err) {
		t.Fatalf("legacy public key should be removed after migration, stat err=%v", err)
	}
}

func firstLine(raw []byte) string {
	line, _, _ := bytes.Cut(raw, []byte("\n"))
	return string(line)
}

func TestAccountSettingsEnvOverridesGeneratedGitSSHCommand(t *testing.T) {
	t.Setenv("GIT_SSH_COMMAND", "")
	pool, _ := newPoolWithAccountHome(t)
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

func TestAccountAccessSkillProviderListsNamesWithoutValues(t *testing.T) {
	pool, _ := newPoolWithAccountHome(t)
	provider := pool.withAccountAccessSkillProvider(func(string) string {
		return "NEXT BLOCK"
	})
	if got := provider("before settings"); got != "NEXT BLOCK" {
		t.Fatalf("provider before settings = %q, want next block only", got)
	}
	if err := setAccountEnv(pool, "GITHUB_TOKEN", "ghp_dynamic_secret"); err != nil {
		t.Fatalf("set env: %v", err)
	}
	got := provider("clone private repo")
	for _, want := range []string{"AFFENT ACCOUNT ACCESS:", "GITHUB_TOKEN", "NEXT BLOCK"} {
		if !strings.Contains(got, want) {
			t.Fatalf("provider block missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "ghp_dynamic_secret") {
		t.Fatalf("provider block leaked env value:\n%s", got)
	}
}

func TestAccountSettingsSSHKeyDoesNotOverwriteExistingKey(t *testing.T) {
	pool, _ := newPoolWithAccountHome(t)
	privatePath, publicPath := accountSSHKeyPaths(pool)
	if err := os.MkdirAll(filepath.Dir(privatePath), 0o700); err != nil {
		t.Fatal(err)
	}
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	existingPrivate, err := marshalOpenSSHEd25519PrivateKey(privateKey, publicKey, "affentserve")
	if err != nil {
		t.Fatal(err)
	}
	existingPublic := marshalOpenSSHEd25519PublicKey(publicKey, "affentserve")
	if err := os.WriteFile(privatePath, existingPrivate, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(publicPath, []byte(existingPublic+"\n"), 0o644); err != nil {
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
	if resp.SSH.Created || resp.SSH.PublicKey != existingPublic {
		t.Fatalf("ssh = %+v, want existing public key without overwrite", resp.SSH)
	}
	privateRaw, err := os.ReadFile(privatePath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(privateRaw, existingPrivate) {
		t.Fatalf("private key was overwritten: %q", string(privateRaw))
	}
}

func TestAccountSettingsSSHKeyConcurrentEnsureReusesSingleKey(t *testing.T) {
	pool, _ := newPoolWithAccountHome(t)
	const workers = 8
	results := make([]accountSSHKeyInfo, workers)
	errs := make([]error, workers)
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		i := i
		go func() {
			defer wg.Done()
			results[i], errs[i] = ensureAccountSSHKey(pool)
		}()
	}
	wg.Wait()

	created := 0
	publicKey := ""
	for i := 0; i < workers; i++ {
		if errs[i] != nil {
			t.Fatalf("ensure[%d] error: %v", i, errs[i])
		}
		if !results[i].Exists || !strings.HasPrefix(results[i].PublicKey, "ssh-ed25519 ") {
			t.Fatalf("ensure[%d] = %+v, want public key", i, results[i])
		}
		if results[i].Created {
			created++
		}
		if publicKey == "" {
			publicKey = results[i].PublicKey
		} else if results[i].PublicKey != publicKey {
			t.Fatalf("ensure[%d] public key changed:\nfirst=%s\nnext=%s", i, publicKey, results[i].PublicKey)
		}
	}
	if created != 1 {
		t.Fatalf("created count = %d, want exactly one generated key", created)
	}
}

func TestAccountSettingsSSHKeyDerivesMissingPublicKeyFromExistingPrivate(t *testing.T) {
	pool, _ := newPoolWithAccountHome(t)
	first, err := ensureAccountSSHKey(pool)
	if err != nil {
		t.Fatalf("ensure first ssh key: %v", err)
	}
	_, publicPath := accountSSHKeyPaths(pool)
	if err := os.Remove(publicPath); err != nil {
		t.Fatalf("remove public key: %v", err)
	}

	r := httptest.NewRequest(http.MethodGet, "/v1/settings", nil)
	w := httptest.NewRecorder()
	handleAccountSettings(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusOK {
		t.Fatalf("get settings status = %d, want 200; body=%s", got, w.Body.String())
	}
	var got accountSettingsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode get settings: %v", err)
	}
	if !got.SSH.Exists || got.SSH.PublicKey != first.PublicKey || got.SSH.PublicKeyError != "" {
		t.Fatalf("get ssh = %+v, want derived public key from existing private", got.SSH)
	}
	if _, err := os.Stat(publicPath); !os.IsNotExist(err) {
		t.Fatalf("GET settings should not create public key file; stat err=%v", err)
	}

	r = httptest.NewRequest(http.MethodPost, "/v1/settings/ssh-key", nil)
	w = httptest.NewRecorder()
	handleAccountSettingsRoutes(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusOK {
		t.Fatalf("ensure settings status = %d, want 200; body=%s", got, w.Body.String())
	}
	var ensured accountSettingsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &ensured); err != nil {
		t.Fatalf("decode ensure settings: %v", err)
	}
	if ensured.SSH.Created || ensured.SSH.PublicKey != first.PublicKey {
		t.Fatalf("ensured ssh = %+v, want existing derived public key without private overwrite", ensured.SSH)
	}
	raw, err := os.ReadFile(publicPath)
	if err != nil {
		t.Fatalf("read derived public key file: %v", err)
	}
	if strings.TrimSpace(string(raw)) != first.PublicKey {
		t.Fatalf("derived public key file = %q, want %q", strings.TrimSpace(string(raw)), first.PublicKey)
	}
}

func TestAccountSettingsSSHKeyDerivesMissingPublicKeyFromOpenSSHPrivate(t *testing.T) {
	pool, _ := newPoolWithAccountHome(t)
	privatePath, publicPath := accountSSHKeyPaths(pool)
	if err := os.MkdirAll(filepath.Dir(privatePath), 0o700); err != nil {
		t.Fatal(err)
	}
	privateKey := strings.Join([]string{
		"-----BEGIN OPENSSH PRIVATE KEY-----",
		"b3BlbnNzaC1rZXktdjEAAAAABG5vbmUAAAAEbm9uZQAAAAAAAAABAAAAMwAAAAtzc2gtZW",
		"QyNTUxOQAAACBkz90yP72VBEik3G5JWsnM72CHVKMdepUTDdhM55dekQAAAJi0xYNWtMWD",
		"VgAAAAtzc2gtZWQyNTUxOQAAACBkz90yP72VBEik3G5JWsnM72CHVKMdepUTDdhM55dekQ",
		"AAAEB31GgQIq3ctwOPYQHLqfpgCpdVDYxAPXjXgtOV2ZD7x2TP3TI/vZUESKTcbklayczv",
		"YIdUox16lRMN2Eznl16RAAAADmFmZmVudC1maXh0dXJlAQIDBAUGBw==",
		"-----END OPENSSH PRIVATE KEY-----",
		"",
	}, "\n")
	if err := os.WriteFile(privatePath, []byte(privateKey), 0o600); err != nil {
		t.Fatal(err)
	}
	wantPublicKey := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGTP3TI/vZUESKTcbklayczvYIdUox16lRMN2Eznl16R affentserve"

	r := httptest.NewRequest(http.MethodGet, "/v1/settings", nil)
	w := httptest.NewRecorder()
	handleAccountSettings(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusOK {
		t.Fatalf("get settings status = %d, want 200; body=%s", got, w.Body.String())
	}
	var got accountSettingsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode get settings: %v", err)
	}
	if !got.SSH.Exists || got.SSH.PublicKey != wantPublicKey || got.SSH.PublicKeyError != "" {
		t.Fatalf("get ssh = %+v, want derived public key from OpenSSH private", got.SSH)
	}
	if _, err := os.Stat(publicPath); !os.IsNotExist(err) {
		t.Fatalf("GET settings should not create public key file; stat err=%v", err)
	}

	r = httptest.NewRequest(http.MethodPost, "/v1/settings/ssh-key", nil)
	w = httptest.NewRecorder()
	handleAccountSettingsRoutes(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusOK {
		t.Fatalf("ensure settings status = %d, want 200; body=%s", got, w.Body.String())
	}
	var ensured accountSettingsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &ensured); err != nil {
		t.Fatalf("decode ensure settings: %v", err)
	}
	if ensured.SSH.Created || ensured.SSH.PublicKey != wantPublicKey {
		t.Fatalf("ensured ssh = %+v, want existing OpenSSH public key without private overwrite", ensured.SSH)
	}
	raw, err := os.ReadFile(publicPath)
	if err != nil {
		t.Fatalf("read derived public key file: %v", err)
	}
	if strings.TrimSpace(string(raw)) != wantPublicKey {
		t.Fatalf("derived public key file = %q, want %q", strings.TrimSpace(string(raw)), wantPublicKey)
	}
}

func TestAccountSettingsSSHKeyRefusesPrivateOnlyOverwrite(t *testing.T) {
	pool, _ := newPoolWithAccountHome(t)
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

func stringSliceContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
