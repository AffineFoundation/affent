package main

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// TestReadMaybeStdin_AtMissingFileIsError pins the @-prefix contract.
// Real-test surface: `affentctl run --prompt @/typoed/path.txt` used
// to send the literal string "/typoed/path.txt" to the model. The
// model would gamely respond as if asked about that filename. Fix:
// @path means MUST exist, return an error otherwise so the user
// notices the typo before paying for a confused reply.
func TestReadMaybeStdin_AtMissingFileIsError(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "does-not-exist.txt")
	got, err := readMaybeStdin("@" + missing)
	if err == nil {
		t.Fatalf("@<missing> should error; got %q", got)
	}
	if !strings.Contains(err.Error(), "does-not-exist.txt") {
		t.Errorf("error should mention the path: %v", err)
	}
}

func TestReadMaybeStdin_AtExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "p.txt")
	if err := os.WriteFile(path, []byte("hello from file"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := readMaybeStdin("@" + path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got != "hello from file" {
		t.Errorf("got %q", got)
	}
}

func TestReadMaybeStdin_LiteralPassesThrough(t *testing.T) {
	got, err := readMaybeStdin("just a literal prompt")
	if err != nil || got != "just a literal prompt" {
		t.Errorf("literal mishandled: got=%q err=%v", got, err)
	}
}

func TestTrimUTF8_SnapsToRuneBoundary(t *testing.T) {
	cases := []struct {
		name string
		in   string
		n    int
	}{
		{"cyrillic", strings.Repeat("ё", 50), 7}, // 2-byte runes, odd n lands mid-rune
		{"chinese", strings.Repeat("你", 30), 8},  // 3-byte runes
		{"emoji", strings.Repeat("🔧", 20), 9},    // 4-byte runes
		{"ascii", strings.Repeat("a", 50), 7},    // single-byte, no snap-back needed
		{"under-cap", "short", 100},              // already under cap, returned verbatim
		{"empty", "", 5},                         // empty stays empty
		{"zero-cap", "anything", 0},              // n<=0 returns empty
		{"negative-cap", "anything", -5},         // same
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := trimUTF8(c.in, c.n)
			if !utf8.ValidString(got) {
				t.Fatalf("trimUTF8(%q, %d) = %q (invalid UTF-8)", c.in, c.n, got)
			}
			if len(got) > c.n && c.n > 0 {
				t.Fatalf("trimUTF8(%q, %d) = %q (len %d exceeds cap)", c.in, c.n, got, len(got))
			}
		})
	}
}

func TestApplyConfigMergesAndCLIOverrides(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "affent.json")
	if err := os.WriteFile(cfgPath, []byte(`{
		"workspace": "./from-config",
		"model": "config-model",
		"max_call_timeout": "9s",
		"compact": {"trigger": 10, "keep_last": 4}
	}`), 0o644); err != nil {
		t.Fatal(err)
	}

	var cf commonFlags
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	cf.bind(fs)
	if err := fs.Parse([]string{
		"--config", cfgPath,
		"--model", "cli-model",
		"--compact-keep-last", "7",
	}); err != nil {
		t.Fatal(err)
	}
	if err := applyConfig(&cf, fs); err != nil {
		t.Fatal(err)
	}

	if cf.workspace != "./from-config" {
		t.Fatalf("workspace not loaded from config: %q", cf.workspace)
	}
	if cf.model != "cli-model" {
		t.Fatalf("CLI model did not override config: %q", cf.model)
	}
	if cf.callTimeout != 9*time.Second {
		t.Fatalf("duration not parsed: %s", cf.callTimeout)
	}
	if cf.compactTrigger != 10 {
		t.Fatalf("compact.trigger not loaded from config: %d", cf.compactTrigger)
	}
	if cf.compactKeepLast != 7 {
		t.Fatalf("CLI compact-keep-last did not override config: %d", cf.compactKeepLast)
	}
}

// TestEnvVarBeatsConfigFile pins the documented precedence:
// CLI > env > config > built-in default. Real test: a user had
// AFFENTCTL_MODEL=qwen-plus exported, ran `affentctl --config c.json`
// where c.json set "model":"old-default", and the run silently used
// old-default — env was overridden by a static project file. The
// flag table documents env as a peer of --model (not a default), so
// env should win.
func TestEnvVarBeatsConfigFile(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "c.json")
	if err := os.WriteFile(cfgPath, []byte(`{"model":"from-config","base_url":"http://from-config"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AFFENTCTL_MODEL", "from-env")
	// base_url is NOT set in env — config should still fill that in.
	t.Setenv("AFFENTCTL_BASE_URL", "")
	os.Unsetenv("AFFENTCTL_BASE_URL")

	var cf commonFlags
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	cf.bind(fs)
	if err := fs.Parse([]string{"--config", cfgPath}); err != nil {
		t.Fatal(err)
	}
	if err := applyConfig(&cf, fs); err != nil {
		t.Fatal(err)
	}
	if cf.model != "from-env" {
		t.Errorf("env-set AFFENTCTL_MODEL should win over config; got %q", cf.model)
	}
	if cf.baseURL != "http://from-config" {
		t.Errorf("config should fill in base-url when env is unset; got %q", cf.baseURL)
	}
}

func TestAPIKeyEnvDoesNotLeakIntoFlagDefaults(t *testing.T) {
	t.Setenv("AFFENTCTL_API_KEY", "sk-test-secret")

	var cf commonFlags
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	var help strings.Builder
	fs.SetOutput(&help)
	cf.bind(fs)
	fs.PrintDefaults()

	if got := fs.Lookup("api-key").DefValue; got != "" {
		t.Fatalf("api-key flag default should stay empty so help cannot print secrets, got %q", got)
	}
	if strings.Contains(help.String(), "sk-test-secret") {
		t.Fatalf("flag help leaked API key: %s", help.String())
	}

	if err := applyConfig(&cf, fs); err != nil {
		t.Fatal(err)
	}
	if cf.apiKey != "sk-test-secret" {
		t.Fatalf("env API key was not applied after parse; got %q", cf.apiKey)
	}
}

// TestNoEnvVarLeaksIntoFlagDefaults pins the broader contract for every
// flag listed in flagEnvSources — none of them may use os.Getenv() as
// the bind default. The previous fix only caught api-key/base-url/model;
// config, mcp-config, and executor still used env-as-default and would
// have printed the operator's path or backend setting in --help.
func TestNoEnvVarLeaksIntoFlagDefaults(t *testing.T) {
	// Sentinels deliberately do not appear in any flag's hardcoded
	// help text (which includes example values like "docker:abc123def"
	// that would false-trigger a substring match).
	planted := map[string]string{
		"AFFENTCTL_CONFIG":     "/sentinel-config-XYZ123",
		"AFFENTCTL_BASE_URL":   "https://sentinel-base-XYZ123",
		"AFFENTCTL_API_KEY":    "sk-sentinel-XYZ123",
		"AFFENTCTL_MODEL":      "sentinel-model-XYZ123",
		"AFFENTCTL_MCP_CONFIG": "/sentinel-mcp-XYZ123",
		"AFFENTCTL_EXECUTOR":   "docker:sentinel-XYZ123",
	}
	for k, v := range planted {
		t.Setenv(k, v)
	}

	var cf commonFlags
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	var help strings.Builder
	fs.SetOutput(&help)
	cf.bind(fs)
	fs.PrintDefaults()

	for name := range flagEnvSources {
		got := fs.Lookup(name).DefValue
		// "executor" has a non-env default ("local") which is fine to
		// surface. Every other flag must have an empty default so
		// nothing in the env table reaches --help.
		want := ""
		if name == "executor" {
			want = "local"
		}
		if got != want {
			t.Errorf("%s default = %q, want %q (env-bound flags must not show env values in --help)", name, got, want)
		}
	}
	for _, v := range planted {
		if v == "local" {
			continue // executor's built-in default matches "local"; impossible to plant
		}
		if strings.Contains(help.String(), v) {
			t.Fatalf("help output leaked planted env value %q:\n%s", v, help.String())
		}
	}
}

// TestParseSampling pins the string-shaped sampling flag parsing.
// Empty strings stay nil (distinguishes "unset" from "explicit 0" so
// upstream provider defaults still apply); explicit "0" must become a
// non-nil pointer to 0 so deterministic-decode evals work.
func TestParseSampling(t *testing.T) {
	t.Run("all empty → all nil", func(t *testing.T) {
		s, err := parseSampling("", "", "", "")
		if err != nil {
			t.Fatal(err)
		}
		if s.Temperature != nil || s.TopP != nil || s.MaxTokens != nil || s.Seed != nil {
			t.Errorf("empty strings must yield nil pointers; got %+v", s)
		}
	})
	t.Run("temperature=0 keeps a non-nil zero pointer", func(t *testing.T) {
		s, err := parseSampling("0", "", "", "")
		if err != nil {
			t.Fatal(err)
		}
		if s.Temperature == nil {
			t.Fatal("temperature=0 must produce non-nil pointer")
		}
		if *s.Temperature != 0 {
			t.Errorf("temperature value lost: got %v", *s.Temperature)
		}
	})
	t.Run("all set", func(t *testing.T) {
		s, err := parseSampling("0.7", "0.95", "256", "42")
		if err != nil {
			t.Fatal(err)
		}
		if s.Temperature == nil || *s.Temperature != 0.7 {
			t.Errorf("temperature: got %v", s.Temperature)
		}
		if s.TopP == nil || *s.TopP != 0.95 {
			t.Errorf("top_p: got %v", s.TopP)
		}
		if s.MaxTokens == nil || *s.MaxTokens != 256 {
			t.Errorf("max_tokens: got %v", s.MaxTokens)
		}
		if s.Seed == nil || *s.Seed != 42 {
			t.Errorf("seed: got %v", s.Seed)
		}
	})
	t.Run("invalid temperature surfaces the parse error", func(t *testing.T) {
		_, err := parseSampling("hot", "", "", "")
		if err == nil {
			t.Fatal("expected parse error for non-numeric temperature")
		}
	})
}

func TestMemoryOnlyImpliesMemoryEnabled(t *testing.T) {
	var cf commonFlags
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	cf.bind(fs)
	if err := fs.Parse([]string{"--memory-only", "--project-context=true"}); err != nil {
		t.Fatal(err)
	}
	if err := applyConfig(&cf, fs); err != nil {
		t.Fatal(err)
	}
	if !cf.memoryEnabled {
		t.Fatal("--memory-only must imply --memory=true")
	}
	if !cf.memoryOnly {
		t.Fatal("--memory-only flag not set")
	}
	if cf.projectContext {
		t.Fatal("--memory-only must disable project context")
	}
}

func TestMemoryConfigFromFile(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "affent.json")
	if err := os.WriteFile(cfgPath, []byte(`{
		"memory": {
			"enabled": true,
			"workspace_store": "notes/MEMORY.md",
			"user_store": "/abs/USER.md",
			"max_chars": "4400,2750"
		}
	}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var cf commonFlags
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	cf.bind(fs)
	if err := fs.Parse([]string{"--config", cfgPath}); err != nil {
		t.Fatal(err)
	}
	if err := applyConfig(&cf, fs); err != nil {
		t.Fatal(err)
	}
	if !cf.memoryEnabled {
		t.Fatal("memory.enabled not loaded from config")
	}
	if cf.memoryWorkspaceStore != "notes/MEMORY.md" {
		t.Fatalf("workspace_store not loaded: %q", cf.memoryWorkspaceStore)
	}
	if cf.memoryUserStore != "/abs/USER.md" {
		t.Fatalf("user_store not loaded: %q", cf.memoryUserStore)
	}
	if cf.memoryMaxChars != "4400,2750" {
		t.Fatalf("max_chars not loaded: %q", cf.memoryMaxChars)
	}
}

func TestResolveStorePath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home dir: %v", err)
	}
	cases := []struct {
		name      string
		workspace string
		in        string
		want      string
	}{
		{"absolute pass-through", "/ws", "/abs/path.md", "/abs/path.md"},
		{"relative joins workspace", "/ws", "notes.md", "/ws/notes.md"},
		{"tilde alone", "/ws", "~", home},
		{"tilde slash", "/ws", "~/notes.md", filepath.Join(home, "notes.md")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := resolveStorePath(c.workspace, c.in); got != c.want {
				t.Fatalf("resolveStorePath(%q, %q) = %q want %q", c.workspace, c.in, got, c.want)
			}
		})
	}
}

func TestResolveStorePathKeepsRelativePathAlreadyInsideWorkspace(t *testing.T) {
	cwd := t.TempDir()
	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(oldwd); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	})

	workspace := filepath.Join(cwd, ".tmp", "eval", "ws")
	in := filepath.Join(".tmp", "eval", "ws", ".affent", "memory")
	want := filepath.Join(workspace, ".affent", "memory")
	if got := resolveStorePath(workspace, in); got != want {
		t.Fatalf("resolveStorePath(%q, %q) = %q want %q", workspace, in, got, want)
	}
}

func TestParseMemoryMaxChars(t *testing.T) {
	cases := []struct {
		spec    string
		mem     int
		user    int
		ok      bool
		wantErr bool
	}{
		{"", 0, 0, false, false},
		{"2200,1375", 2200, 1375, true, false},
		{" 4000 , 2000 ", 4000, 2000, true, false},
		{"bad", 0, 0, false, true},
		{"2200,abc", 0, 0, false, true},
		{"-1,1000", 0, 0, false, true},
	}
	for _, c := range cases {
		t.Run(c.spec, func(t *testing.T) {
			m, u, ok, err := parseMemoryMaxChars(c.spec)
			if (err != nil) != c.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, c.wantErr)
			}
			if err != nil {
				return
			}
			if ok != c.ok {
				t.Fatalf("ok=%v want=%v", ok, c.ok)
			}
			if ok && (m != c.mem || u != c.user) {
				t.Fatalf("got (%d,%d) want (%d,%d)", m, u, c.mem, c.user)
			}
		})
	}
}
