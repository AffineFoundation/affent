package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	agent "github.com/affinefoundation/affent/internal/agent"
)

const (
	accountSettingsFileName       = "settings.json"
	accountSSHKeyName             = "id_ed25519"
	accountSettingsMaxFileBytes   = 256 * 1024
	accountSSHKeyMaxFileBytes     = 128 * 1024
	accountSettingsMaxEnvVars     = 128
	accountSettingsMaxEnvValueLen = 32 * 1024
	accountAccessPromptMaxEnvVars = 12
)

var accountEnvNameRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

type accountSettingsFile struct {
	Version int               `json:"version"`
	Env     []accountEnvEntry `json:"env,omitempty"`
}

type accountEnvEntry struct {
	Name      string `json:"name"`
	Value     string `json:"value"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

type accountSettingsResponse struct {
	Env []accountEnvSummary `json:"env"`
	SSH accountSSHKeyInfo   `json:"ssh"`
}

type accountEnvSummary struct {
	Name       string `json:"name"`
	Configured bool   `json:"configured"`
	UpdatedAt  string `json:"updated_at,omitempty"`
}

type accountSSHKeyInfo struct {
	Exists         bool   `json:"exists"`
	PublicKey      string `json:"public_key,omitempty"`
	PublicKeyPath  string `json:"public_key_path,omitempty"`
	PrivateKeyPath string `json:"-"`
	Created        bool   `json:"created,omitempty"`
	PublicKeyError string `json:"public_key_error,omitempty"`
}

type accountEnvSetRequest struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

func handleAccountSettings(pool *SessionPool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			settings, err := readAccountSettingsResponse(pool)
			if err != nil {
				writeJSONError(w, http.StatusInternalServerError, "read account settings", err)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(settings)
		default:
			writeJSONErrorTyped(w, http.StatusNotFound, "not found", nil, "not_found")
		}
	}
}

func handleAccountSettingsRoutes(pool *SessionPool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sub := strings.TrimPrefix(r.URL.Path, "/v1/settings/")
		switch {
		case sub == "env" && r.Method == http.MethodPost:
			handleAccountEnvSet(pool, w, r)
		case strings.HasPrefix(sub, "env/") && r.Method == http.MethodDelete:
			handleAccountEnvDelete(pool, strings.TrimPrefix(sub, "env/"), w)
		case sub == "ssh-key" && r.Method == http.MethodPost:
			handleAccountSSHKeyEnsure(pool, w)
		default:
			writeJSONErrorTyped(w, http.StatusNotFound, "not found", nil, "not_found")
		}
	}
}

func handleAccountEnvSet(pool *SessionPool, w http.ResponseWriter, r *http.Request) {
	req, err := decodeAccountEnvSetRequest(w, r)
	if err != nil {
		writeJSONErrorTyped(w, http.StatusBadRequest, "invalid environment variable request", err, "bad_request")
		return
	}
	if err := setAccountEnv(pool, req.Name, req.Value); err != nil {
		writeJSONErrorTyped(w, http.StatusBadRequest, "set environment variable", err, "bad_request")
		return
	}
	settings, err := readAccountSettingsResponse(pool)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "read account settings", err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(settings)
}

func handleAccountEnvDelete(pool *SessionPool, rawName string, w http.ResponseWriter) {
	name, err := normalizeAccountEnvName(rawName)
	if err != nil {
		writeJSONErrorTyped(w, http.StatusBadRequest, "invalid environment variable name", err, "bad_request")
		return
	}
	if err := deleteAccountEnv(pool, name); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "delete environment variable", err)
		return
	}
	settings, err := readAccountSettingsResponse(pool)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "read account settings", err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(settings)
}

func handleAccountSSHKeyEnsure(pool *SessionPool, w http.ResponseWriter) {
	key, err := ensureAccountSSHKey(pool)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "ensure ssh key", err)
		return
	}
	settings, err := readAccountSettingsResponse(pool)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "read account settings", err)
		return
	}
	settings.SSH.Created = key.Created
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(settings)
}

func decodeAccountEnvSetRequest(w http.ResponseWriter, r *http.Request) (accountEnvSetRequest, error) {
	var req accountEnvSetRequest
	if r.Body == nil || r.Body == http.NoBody {
		return req, errors.New("request body is required")
	}
	defer r.Body.Close()
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, accountSettingsMaxEnvValueLen+4096))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		return req, err
	}
	var extra struct{}
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return req, errors.New("request body must contain a single JSON object")
	}
	return req, nil
}

func readAccountSettingsResponse(pool *SessionPool) (accountSettingsResponse, error) {
	settings, _, err := readAccountSettingsFile(pool)
	if err != nil {
		return accountSettingsResponse{}, err
	}
	env := make([]accountEnvSummary, 0, len(settings.Env))
	for _, entry := range settings.Env {
		env = append(env, accountEnvSummary{
			Name:       entry.Name,
			Configured: entry.Value != "",
			UpdatedAt:  entry.UpdatedAt,
		})
	}
	sort.Slice(env, func(i, j int) bool { return env[i].Name < env[j].Name })
	ssh, err := readAccountSSHKeyInfo(pool)
	if err != nil {
		return accountSettingsResponse{}, err
	}
	return accountSettingsResponse{Env: env, SSH: ssh}, nil
}

func (p *SessionPool) accountEnvPairs() []string {
	settings, _, err := readAccountSettingsFile(p)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(settings.Env))
	configured := map[string]bool{}
	for _, entry := range settings.Env {
		if entry.Name == "" {
			continue
		}
		configured[entry.Name] = true
		out = append(out, entry.Name+"="+entry.Value)
	}
	if !configured["GIT_SSH_COMMAND"] && os.Getenv("GIT_SSH_COMMAND") == "" {
		if command, ok := accountGitSSHCommand(p); ok {
			out = append(out, "GIT_SSH_COMMAND="+command)
		}
	}
	return out
}

func (p *SessionPool) accountSecretValues() []string {
	settings, _, err := readAccountSettingsFile(p)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(settings.Env))
	seen := map[string]bool{}
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			return
		}
		seen[value] = true
		out = append(out, value)
	}
	for _, entry := range settings.Env {
		add(entry.Value)
	}
	add(os.Getenv("GIT_SSH_COMMAND"))
	if command, ok := accountGitSSHCommand(p); ok {
		add(command)
	}
	privatePath, _ := accountSSHKeyPaths(p)
	if exists, err := accountPrivateKeyExists(privatePath); err == nil && exists {
		add(privatePath)
	}
	return out
}

func (p *SessionPool) accountAccessSystemBlock() string {
	settings, _, err := readAccountSettingsFile(p)
	if err != nil {
		return ""
	}
	names := make([]string, 0, len(settings.Env))
	for _, entry := range settings.Env {
		name := strings.TrimSpace(entry.Name)
		if name != "" && strings.TrimSpace(entry.Value) != "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	ssh, err := readAccountSSHKeyInfo(p)
	if err != nil {
		ssh = accountSSHKeyInfo{}
	}
	if len(names) == 0 && !ssh.Exists {
		return ""
	}
	var b strings.Builder
	b.WriteString("AFFENT ACCOUNT ACCESS:\n")
	b.WriteString("Use these account-level access settings when relevant for cloning private repositories, installing dependencies, or opening PRs. Values, key paths, and command bodies are secrets; do not print them.\n")
	if len(names) > 0 {
		visible := names
		if len(visible) > accountAccessPromptMaxEnvVars {
			visible = visible[:accountAccessPromptMaxEnvVars]
		}
		b.WriteString("- Configured environment variables available to shell commands: ")
		b.WriteString(strings.Join(visible, ", "))
		if omitted := len(names) - len(visible); omitted > 0 {
			fmt.Fprintf(&b, " (+%d more)", omitted)
		}
		b.WriteString(".\n")
	}
	if ssh.Exists {
		if ssh.PublicKey != "" {
			b.WriteString("- SSH public key is configured for Git host access; use SSH remotes when appropriate. GIT_SSH_COMMAND is injected automatically unless a custom value is configured.\n")
		} else {
			b.WriteString("- An SSH private key exists, but its public key is unavailable; ask the user to fix the key before relying on SSH remotes.\n")
		}
	}
	return strings.TrimSpace(b.String())
}

func (p *SessionPool) withAccountAccessSkillProvider(next agent.SkillProvider) agent.SkillProvider {
	return func(userText string) string {
		blocks := make([]string, 0, 2)
		if block := strings.TrimSpace(p.accountAccessSystemBlock()); block != "" {
			blocks = append(blocks, block)
		}
		if next != nil {
			if block := strings.TrimSpace(next(userText)); block != "" {
				blocks = append(blocks, block)
			}
		}
		return strings.Join(blocks, "\n\n")
	}
}

func setAccountEnv(pool *SessionPool, rawName, value string) error {
	name, err := normalizeAccountEnvName(rawName)
	if err != nil {
		return err
	}
	if len([]byte(value)) > accountSettingsMaxEnvValueLen {
		return fmt.Errorf("environment variable value exceeds %d bytes", accountSettingsMaxEnvValueLen)
	}
	pool.settingsMu.Lock()
	defer pool.settingsMu.Unlock()
	settings, _, err := readAccountSettingsFile(pool)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	for i := range settings.Env {
		if settings.Env[i].Name != name {
			continue
		}
		settings.Env[i].Value = value
		settings.Env[i].UpdatedAt = now
		return writeAccountSettingsFile(pool, settings)
	}
	if len(settings.Env) >= accountSettingsMaxEnvVars {
		return fmt.Errorf("account settings supports at most %d environment variables", accountSettingsMaxEnvVars)
	}
	settings.Env = append(settings.Env, accountEnvEntry{Name: name, Value: value, UpdatedAt: now})
	return writeAccountSettingsFile(pool, settings)
}

func deleteAccountEnv(pool *SessionPool, name string) error {
	pool.settingsMu.Lock()
	defer pool.settingsMu.Unlock()
	settings, _, err := readAccountSettingsFile(pool)
	if err != nil {
		return err
	}
	next := settings.Env[:0]
	for _, entry := range settings.Env {
		if entry.Name == name {
			continue
		}
		next = append(next, entry)
	}
	settings.Env = next
	return writeAccountSettingsFile(pool, settings)
}

func normalizeAccountEnvName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return "", errors.New("environment variable name is required")
	}
	if !accountEnvNameRE.MatchString(name) {
		return "", errors.New("environment variable name must match [A-Za-z_][A-Za-z0-9_]*")
	}
	return name, nil
}

func readAccountSettingsFile(pool *SessionPool) (accountSettingsFile, bool, error) {
	path := accountSettingsPath(pool)
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return accountSettingsFile{Version: 1}, false, nil
		}
		return accountSettingsFile{}, false, err
	}
	if info.IsDir() {
		return accountSettingsFile{}, false, errors.New("account settings path is a directory")
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return accountSettingsFile{}, false, errors.New("account settings path must not be a symlink")
	}
	f, err := os.Open(path)
	if err != nil {
		return accountSettingsFile{}, false, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, accountSettingsMaxFileBytes+1))
	if err != nil {
		return accountSettingsFile{}, false, err
	}
	if len(raw) > accountSettingsMaxFileBytes {
		return accountSettingsFile{}, false, fmt.Errorf("account settings exceeds %d bytes", accountSettingsMaxFileBytes)
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return accountSettingsFile{Version: 1}, false, nil
	}
	var settings accountSettingsFile
	if err := json.Unmarshal(raw, &settings); err != nil {
		return accountSettingsFile{}, false, err
	}
	settings = normalizeAccountSettings(settings)
	return settings, true, nil
}

func writeAccountSettingsFile(pool *SessionPool, settings accountSettingsFile) error {
	settings = normalizeAccountSettings(settings)
	raw, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	if len(raw) > accountSettingsMaxFileBytes {
		return fmt.Errorf("account settings exceeds %d bytes", accountSettingsMaxFileBytes)
	}
	dir := accountSettingsDir(pool)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	path := accountSettingsPath(pool)
	if info, err := os.Lstat(path); err == nil {
		if info.IsDir() {
			return errors.New("account settings path is a directory")
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("account settings path must not be a symlink")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	tmpName := path + ".tmp"
	if err := os.Remove(tmpName); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	tmp, err := os.OpenFile(tmpName, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := tmp.Write(append(raw, '\n')); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	syncAccountDir(dir)
	return nil
}

func normalizeAccountSettings(settings accountSettingsFile) accountSettingsFile {
	settings.Version = 1
	out := settings.Env[:0]
	seen := map[string]bool{}
	for _, entry := range settings.Env {
		name, err := normalizeAccountEnvName(entry.Name)
		if err != nil || seen[name] {
			continue
		}
		seen[name] = true
		entry.Name = name
		if len([]byte(entry.Value)) > accountSettingsMaxEnvValueLen {
			entry.Value = entry.Value[:accountSettingsMaxEnvValueLen]
		}
		out = append(out, entry)
	}
	settings.Env = out
	return settings
}

func readAccountSSHKeyInfo(pool *SessionPool) (accountSSHKeyInfo, error) {
	privatePath, publicPath := accountSSHKeyPaths(pool)
	pub, err := readAccountPublicKey(publicPath)
	if err != nil {
		return accountSSHKeyInfo{}, err
	}
	if pub == "" {
		derived, deriveErr := deriveAccountPublicKeyFromPrivate(privatePath)
		if deriveErr != nil {
			if privateExists, existsErr := accountPrivateKeyExists(privatePath); existsErr != nil {
				return accountSSHKeyInfo{}, existsErr
			} else if privateExists {
				return accountSSHKeyInfo{
					Exists:         true,
					PublicKeyPath:  publicPath,
					PrivateKeyPath: privatePath,
					PublicKeyError: deriveErr.Error(),
				}, nil
			}
		} else if derived != "" {
			pub = derived
		}
	}
	return accountSSHKeyInfo{
		Exists:         pub != "",
		PublicKey:      pub,
		PublicKeyPath:  publicPath,
		PrivateKeyPath: privatePath,
	}, nil
}

func ensureAccountSSHKey(pool *SessionPool) (accountSSHKeyInfo, error) {
	pool.settingsMu.Lock()
	defer pool.settingsMu.Unlock()
	privatePath, publicPath := accountSSHKeyPaths(pool)
	if pub, err := readAccountPublicKey(publicPath); err != nil {
		return accountSSHKeyInfo{}, err
	} else if pub != "" {
		return accountSSHKeyInfo{Exists: true, PublicKey: pub, PublicKeyPath: publicPath, PrivateKeyPath: privatePath}, nil
	}
	if _, err := os.Lstat(privatePath); err == nil {
		pub, deriveErr := deriveAccountPublicKeyFromPrivate(privatePath)
		if deriveErr != nil {
			return accountSSHKeyInfo{}, fmt.Errorf("private SSH key already exists but public key is missing and could not be derived: %w; refusing to overwrite", deriveErr)
		}
		if err := writeNewFile(publicPath, []byte(pub+"\n"), 0o644); err != nil {
			return accountSSHKeyInfo{}, err
		}
		syncAccountDir(filepath.Dir(privatePath))
		return accountSSHKeyInfo{Exists: true, PublicKey: pub, PublicKeyPath: publicPath, PrivateKeyPath: privatePath}, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return accountSSHKeyInfo{}, err
	}
	if err := os.MkdirAll(filepath.Dir(privatePath), 0o700); err != nil {
		return accountSSHKeyInfo{}, err
	}
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return accountSSHKeyInfo{}, err
	}
	der, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return accountSSHKeyInfo{}, err
	}
	privatePEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	if err := writeNewFile(privatePath, privatePEM, 0o600); err != nil {
		return accountSSHKeyInfo{}, err
	}
	pub := marshalOpenSSHEd25519PublicKey(publicKey, "affentserve")
	if err := writeNewFile(publicPath, []byte(pub+"\n"), 0o644); err != nil {
		_ = os.Remove(privatePath)
		return accountSSHKeyInfo{}, err
	}
	syncAccountDir(filepath.Dir(privatePath))
	return accountSSHKeyInfo{Exists: true, Created: true, PublicKey: pub, PublicKeyPath: publicPath, PrivateKeyPath: privatePath}, nil
}

func accountGitSSHCommand(pool *SessionPool) (string, bool) {
	privatePath, _ := accountSSHKeyPaths(pool)
	info, err := os.Lstat(privatePath)
	if err != nil || info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", false
	}
	return "ssh -i " + shellQuote(privatePath) + " -o IdentitiesOnly=yes -o BatchMode=yes -o StrictHostKeyChecking=accept-new", true
}

func readAccountPublicKey(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	if info.IsDir() {
		return "", errors.New("ssh public key path is a directory")
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("ssh public key path must not be a symlink")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(raw)), nil
}

func accountPrivateKeyExists(path string) (bool, error) {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	if info.IsDir() {
		return true, errors.New("ssh private key path is a directory")
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return true, errors.New("ssh private key path must not be a symlink")
	}
	return true, nil
}

func deriveAccountPublicKeyFromPrivate(path string) (string, error) {
	exists, err := accountPrivateKeyExists(path)
	if err != nil {
		return "", err
	}
	if !exists {
		return "", nil
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, accountSSHKeyMaxFileBytes+1))
	if err != nil {
		return "", err
	}
	if len(raw) > accountSSHKeyMaxFileBytes {
		return "", fmt.Errorf("ssh private key exceeds %d bytes", accountSSHKeyMaxFileBytes)
	}
	block, _ := pem.Decode(bytes.TrimSpace(raw))
	if block == nil {
		return "", errors.New("ssh private key is not PEM encoded")
	}
	if block.Type == "OPENSSH PRIVATE KEY" {
		return deriveAccountPublicKeyFromOpenSSHPrivate(block.Bytes)
	}
	if block.Type != "PRIVATE KEY" {
		return "", fmt.Errorf("unsupported private key PEM type %q", block.Type)
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return "", err
	}
	privateKey, ok := key.(ed25519.PrivateKey)
	if !ok {
		return "", fmt.Errorf("unsupported private key type %T", key)
	}
	publicKey, ok := privateKey.Public().(ed25519.PublicKey)
	if !ok {
		return "", errors.New("private key did not expose an Ed25519 public key")
	}
	return marshalOpenSSHEd25519PublicKey(publicKey, "affentserve"), nil
}

func deriveAccountPublicKeyFromOpenSSHPrivate(raw []byte) (string, error) {
	const magic = "openssh-key-v1\x00"
	if !bytes.HasPrefix(raw, []byte(magic)) {
		return "", errors.New("invalid OpenSSH private key header")
	}
	rest := raw[len(magic):]
	_, rest, err := readSSHString(rest)
	if err != nil {
		return "", fmt.Errorf("read OpenSSH cipher name: %w", err)
	}
	_, rest, err = readSSHString(rest)
	if err != nil {
		return "", fmt.Errorf("read OpenSSH kdf name: %w", err)
	}
	_, rest, err = readSSHString(rest)
	if err != nil {
		return "", fmt.Errorf("read OpenSSH kdf options: %w", err)
	}
	if len(rest) < 4 {
		return "", errors.New("OpenSSH private key missing key count")
	}
	keyCount := binary.BigEndian.Uint32(rest[:4])
	rest = rest[4:]
	if keyCount != 1 {
		return "", fmt.Errorf("unsupported OpenSSH private key count %d", keyCount)
	}
	publicBlob, _, err := readSSHString(rest)
	if err != nil {
		return "", fmt.Errorf("read OpenSSH public key: %w", err)
	}
	return marshalOpenSSHPublicKeyBlob(publicBlob, "affentserve")
}

func marshalOpenSSHPublicKeyBlob(publicBlob []byte, comment string) (string, error) {
	keyType, _, err := readSSHString(publicBlob)
	if err != nil {
		return "", fmt.Errorf("read OpenSSH public key type: %w", err)
	}
	if len(keyType) == 0 {
		return "", errors.New("OpenSSH public key type is empty")
	}
	out := string(keyType) + " " + base64.StdEncoding.EncodeToString(publicBlob)
	if strings.TrimSpace(comment) != "" {
		out += " " + strings.TrimSpace(comment)
	}
	return out, nil
}

func marshalOpenSSHEd25519PublicKey(pub ed25519.PublicKey, comment string) string {
	const keyType = "ssh-ed25519"
	var payload bytes.Buffer
	writeSSHString(&payload, []byte(keyType))
	writeSSHString(&payload, []byte(pub))
	out := keyType + " " + base64.StdEncoding.EncodeToString(payload.Bytes())
	if strings.TrimSpace(comment) != "" {
		out += " " + strings.TrimSpace(comment)
	}
	return out
}

func readSSHString(raw []byte) ([]byte, []byte, error) {
	if len(raw) < 4 {
		return nil, nil, errors.New("short SSH string length")
	}
	length := binary.BigEndian.Uint32(raw[:4])
	rest := raw[4:]
	if length > uint32(len(rest)) {
		return nil, nil, errors.New("SSH string length exceeds remaining data")
	}
	return rest[:length], rest[length:], nil
}

func writeSSHString(buf *bytes.Buffer, value []byte) {
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(value)))
	buf.Write(length[:])
	buf.Write(value)
}

func writeNewFile(path string, content []byte, perm os.FileMode) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, perm)
	if err != nil {
		return err
	}
	if _, err := f.Write(content); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return err
	}
	return nil
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func syncAccountDir(path string) {
	if d, err := os.Open(path); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
}

func accountSettingsDir(pool *SessionPool) string {
	if pool != nil && strings.TrimSpace(pool.cfg.AccountRoot) != "" {
		return pool.cfg.AccountRoot
	}
	return filepath.Join(pool.sessionRootPath(), ".affentserve")
}

func accountSettingsPath(pool *SessionPool) string {
	return filepath.Join(accountSettingsDir(pool), accountSettingsFileName)
}

func accountSSHKeyPaths(pool *SessionPool) (string, string) {
	privatePath := filepath.Join(accountSettingsDir(pool), "ssh", accountSSHKeyName)
	return privatePath, privatePath + ".pub"
}
