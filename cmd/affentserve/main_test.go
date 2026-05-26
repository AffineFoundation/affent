package main

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/rs/zerolog"
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

func TestParseFlagsAndConfig_DefaultOnToolsCanBeDisabled(t *testing.T) {
	cfg, err := parseFlagsAndConfig([]string{
		"--base-url", "https://example/v1",
		"--model", "demo",
	})
	if err != nil {
		t.Fatalf("parseFlagsAndConfig: %v", err)
	}
	if !cfg.EnableSubagent {
		t.Fatal("subagent should default on")
	}
	if !cfg.EnableMemory {
		t.Fatal("memory should default on")
	}
	if !cfg.EnableFocusedTasks {
		t.Fatal("focused tasks should default on")
	}

	cfg, err = parseFlagsAndConfig([]string{
		"--base-url", "https://example/v1",
		"--model", "demo",
		"--subagent=false",
		"--memory=false",
		"--focused-tasks=false",
	})
	if err != nil {
		t.Fatalf("parseFlagsAndConfig: %v", err)
	}
	if cfg.EnableSubagent {
		t.Fatal("--subagent=false should disable subagent")
	}
	if cfg.EnableMemory {
		t.Fatal("--memory=false should disable memory")
	}
	if cfg.EnableFocusedTasks {
		t.Fatal("--focused-tasks=false should disable focused tasks")
	}
}

func TestParseFlagsAndConfig_SubagentMaxDepth(t *testing.T) {
	t.Setenv("AFFENTSERVE_SUBAGENT_MAX_DEPTH", "3")
	cfg, err := parseFlagsAndConfig([]string{
		"--base-url", "https://example/v1",
		"--model", "demo",
	})
	if err != nil {
		t.Fatalf("parseFlagsAndConfig: %v", err)
	}
	if cfg.SubagentMaxDepth != 3 {
		t.Fatalf("env subagent max depth = %d, want 3", cfg.SubagentMaxDepth)
	}

	cfg, err = parseFlagsAndConfig([]string{
		"--base-url", "https://example/v1",
		"--model", "demo",
		"--subagent-max-depth=1",
	})
	if err != nil {
		t.Fatalf("parseFlagsAndConfig: %v", err)
	}
	if cfg.SubagentMaxDepth != 1 {
		t.Fatalf("cli subagent max depth = %d, want 1", cfg.SubagentMaxDepth)
	}
}

func TestParseFlagsAndConfig_FocusedTasksFromEnv(t *testing.T) {
	t.Setenv("AFFENTSERVE_FOCUSED_TASKS", "false")
	cfg, err := parseFlagsAndConfig([]string{
		"--base-url", "https://example/v1",
		"--model", "demo",
	})
	if err != nil {
		t.Fatalf("parseFlagsAndConfig: %v", err)
	}
	if cfg.EnableFocusedTasks {
		t.Fatal("AFFENTSERVE_FOCUSED_TASKS=false should disable focused tasks")
	}

	cfg, err = parseFlagsAndConfig([]string{
		"--base-url", "https://example/v1",
		"--model", "demo",
		"--focused-tasks=true",
	})
	if err != nil {
		t.Fatalf("parseFlagsAndConfig cli override: %v", err)
	}
	if !cfg.EnableFocusedTasks {
		t.Fatal("--focused-tasks=true should override AFFENTSERVE_FOCUSED_TASKS=false")
	}
}

func TestParseFlagsAndConfig_BuiltinsFromEnv(t *testing.T) {
	t.Setenv("AFFENTSERVE_BUILTINS", "true")
	cfg, err := parseFlagsAndConfig([]string{
		"--base-url", "https://example/v1",
		"--model", "demo",
	})
	if err != nil {
		t.Fatalf("parseFlagsAndConfig: %v", err)
	}
	if !cfg.EnableBuiltins {
		t.Fatal("AFFENTSERVE_BUILTINS=true should enable builtins")
	}

	cfg, err = parseFlagsAndConfig([]string{
		"--base-url", "https://example/v1",
		"--model", "demo",
		"--builtins=false",
	})
	if err != nil {
		t.Fatalf("parseFlagsAndConfig cli override: %v", err)
	}
	if cfg.EnableBuiltins {
		t.Fatal("--builtins=false should override AFFENTSERVE_BUILTINS=true")
	}
}

func TestParseFlagsAndConfig_EvalModeDisablesNonBasicSurfaces(t *testing.T) {
	t.Setenv("AFFENTSERVE_SUBAGENT", "true")
	t.Setenv("AFFENTSERVE_FOCUSED_TASKS", "true")
	t.Setenv("TAVILY_API_KEY", "test-key")
	cfg, err := parseFlagsAndConfig([]string{
		"--base-url", "https://example/v1",
		"--model", "demo",
		"--builtins",
		"--eval-mode",
	})
	if err != nil {
		t.Fatalf("parseFlagsAndConfig: %v", err)
	}
	if !cfg.EvalMode || !cfg.EnableBuiltins {
		t.Fatalf("eval mode should preserve explicit basic builtins: eval=%t builtins=%t", cfg.EvalMode, cfg.EnableBuiltins)
	}
	if cfg.EnableMemory || cfg.EnableBrowser || cfg.BrowserScreenshot || cfg.EnableWeb || cfg.EnableWebSearch || cfg.EnableSubagent || cfg.EnableFocusedTasks {
		t.Fatalf("eval mode should disable non-basic surfaces: %+v", cfg)
	}

	cfg, err = parseFlagsAndConfig([]string{
		"--base-url", "https://example/v1",
		"--model", "demo",
		"--eval-mode",
		"--memory=true",
	})
	if err != nil {
		t.Fatalf("parseFlagsAndConfig explicit memory: %v", err)
	}
	if !cfg.EnableMemory {
		t.Fatal("--eval-mode --memory=true should opt memory back in")
	}

	cfg, err = parseFlagsAndConfig([]string{
		"--base-url", "https://example/v1",
		"--model", "demo",
		"--eval-mode",
		"--browser=true",
	})
	if err != nil {
		t.Fatalf("parseFlagsAndConfig explicit browser: %v", err)
	}
	if !cfg.EnableBrowser {
		t.Fatal("--eval-mode --browser=true should opt browser back in for browser-only evals")
	}
	if cfg.EnableWeb || cfg.EnableMemory || cfg.EnableSubagent || cfg.EnableFocusedTasks {
		t.Fatalf("browser-only eval should not enable unrelated capabilities: %+v", cfg)
	}

	cfg, err = parseFlagsAndConfig([]string{
		"--base-url", "https://example/v1",
		"--model", "demo",
		"--eval-mode",
		"--web=true",
		"--web-search=true",
	})
	if err != nil {
		t.Fatalf("parseFlagsAndConfig explicit web search: %v", err)
	}
	if !cfg.EnableWeb || !cfg.EnableWebSearch {
		t.Fatal("--eval-mode --web=true --web-search=true should opt web_search back in")
	}

	cfg, err = parseFlagsAndConfig([]string{
		"--base-url", "https://example/v1",
		"--model", "demo",
		"--eval-mode",
		"--web=true",
	})
	if err != nil {
		t.Fatalf("parseFlagsAndConfig explicit web fetch: %v", err)
	}
	if !cfg.EnableWeb {
		t.Fatal("--eval-mode --web=true should opt web_fetch back in")
	}
	if cfg.EnableWebSearch {
		t.Fatalf("--eval-mode --web=true must not imply web_search: %+v", cfg)
	}
}

func TestParseFlagsAndConfig_NetworkToolsFromEnv(t *testing.T) {
	t.Setenv("AFFENTSERVE_BROWSER", "true")
	t.Setenv("AFFENTSERVE_BROWSER_SCREENSHOT", "true")
	t.Setenv("AFFENTSERVE_WEB", "true")
	t.Setenv("AFFENTSERVE_WEB_SEARCH", "true")
	t.Setenv("TAVILY_API_KEY", "test-key")
	cfg, err := parseFlagsAndConfig([]string{
		"--base-url", "https://example/v1",
		"--model", "demo",
	})
	if err != nil {
		t.Fatalf("parseFlagsAndConfig: %v", err)
	}
	if !cfg.EnableBrowser || !cfg.BrowserScreenshot || !cfg.EnableWeb || !cfg.EnableWebSearch {
		t.Fatalf("network tool envs should enable browser/web capabilities: %+v", cfg)
	}

	cfg, err = parseFlagsAndConfig([]string{
		"--base-url", "https://example/v1",
		"--model", "demo",
		"--browser=false",
		"--browser-screenshot=false",
		"--web=false",
		"--web-search=false",
	})
	if err != nil {
		t.Fatalf("parseFlagsAndConfig cli override: %v", err)
	}
	if cfg.EnableBrowser || cfg.BrowserScreenshot || cfg.EnableWeb || cfg.EnableWebSearch {
		t.Fatalf("CLI false flags should override network tool envs: %+v", cfg)
	}
}

func TestParseFlagsAndConfig_EvalModeAllowsEnvironmentPermissionEnv(t *testing.T) {
	t.Run("browser", func(t *testing.T) {
		t.Setenv("AFFENTSERVE_BROWSER", "true")
		cfg, err := parseFlagsAndConfig([]string{
			"--base-url", "https://example/v1",
			"--model", "demo",
			"--eval-mode",
		})
		if err != nil {
			t.Fatalf("parseFlagsAndConfig: %v", err)
		}
		if !cfg.EnableBrowser {
			t.Fatal("AFFENTSERVE_BROWSER=true should opt browser into eval mode")
		}
		if cfg.EnableWeb || cfg.EnableMemory || cfg.EnableSubagent || cfg.EnableFocusedTasks {
			t.Fatalf("browser env opt-in should not enable unrelated eval capabilities: %+v", cfg)
		}
	})

	t.Run("web fetch only", func(t *testing.T) {
		t.Setenv("AFFENTSERVE_WEB", "true")
		cfg, err := parseFlagsAndConfig([]string{
			"--base-url", "https://example/v1",
			"--model", "demo",
			"--eval-mode",
		})
		if err != nil {
			t.Fatalf("parseFlagsAndConfig: %v", err)
		}
		if !cfg.EnableWeb {
			t.Fatal("AFFENTSERVE_WEB=true should opt web_fetch into eval mode")
		}
		if cfg.EnableWebSearch {
			t.Fatalf("AFFENTSERVE_WEB=true must not imply web_search in eval mode: %+v", cfg)
		}
		if cfg.EnableBrowser || cfg.EnableMemory || cfg.EnableSubagent || cfg.EnableFocusedTasks {
			t.Fatalf("web fetch env opt-in should not enable unrelated eval capabilities: %+v", cfg)
		}
	})
}

func TestParseFlagsAndConfig_MemoryRootFromCLI(t *testing.T) {
	t.Setenv("AFFENTSERVE_MEMORY_ROOT", "/env-state")
	cfg, err := parseFlagsAndConfig([]string{
		"--base-url", "https://example/v1",
		"--model", "demo",
		"--memory-root", "/cli-state",
	})
	if err != nil {
		t.Fatalf("parseFlagsAndConfig: %v", err)
	}
	if cfg.MemoryRoot != "/cli-state" {
		t.Fatalf("--memory-root should override AFFENTSERVE_MEMORY_ROOT, got %q", cfg.MemoryRoot)
	}
}

func TestLogServeStartupIncludesDurablePathsWithoutSecrets(t *testing.T) {
	var buf bytes.Buffer
	cfg := Config{
		Listen:         "127.0.0.1:7777",
		BaseURL:        "https://example/v1",
		APIKey:         "sk-secret-should-not-log",
		Model:          "demo",
		AuthToken:      "bearer-secret-should-not-log",
		WorkspaceRoot:  "/workspace/sessions",
		MemoryRoot:     "/workspace/session-state",
		MaxSessions:    8,
		SessionIdleTTL: "5m",
		EnableBuiltins: true,
		EnableMemory:   true,
	}
	logServeStartup(zerolog.New(&buf), cfg, cfg.MemoryRoot)
	logLine := buf.String()
	for _, want := range []string{
		`"workspace_root":"/workspace/sessions"`,
		`"memory_root":"/workspace/session-state"`,
		`"session_state_root":"/workspace/session-state"`,
		`"auth":"on"`,
		`"builtins":true`,
	} {
		if !strings.Contains(logLine, want) {
			t.Fatalf("startup log missing %s:\n%s", want, logLine)
		}
	}
	for _, secret := range []string{"sk-secret-should-not-log", "bearer-secret-should-not-log"} {
		if strings.Contains(logLine, secret) {
			t.Fatalf("startup log leaked secret %q:\n%s", secret, logLine)
		}
	}
}

// TestFocusedTaskProfilesForLog_MatchesProbeRules pins the cfg →
// profile-list translation in one place so changes to the probe
// rules show up as test failures here (and in doctor's parallel
// tests) instead of silently diverging between the CLI diagnostic
// and the server boot log.
func TestFocusedTaskProfilesForLog_MatchesProbeRules(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
		want []string
	}{
		{
			name: "disabled returns nil",
			cfg:  Config{EnableFocusedTasks: false, EnableBuiltins: true, EnableMemory: true, EnableWeb: true},
			want: nil,
		},
		{
			name: "default server wiring (no web) exposes 4 profiles",
			cfg:  Config{EnableFocusedTasks: true, EnableBuiltins: true, EnableMemory: true},
			want: []string{"recall", "explore", "verify", "review"},
		},
		{
			name: "with web exposes web_extract and research",
			cfg:  Config{EnableFocusedTasks: true, EnableBuiltins: true, EnableMemory: true, EnableWeb: true},
			want: []string{"recall", "explore", "web_extract", "research", "verify", "review"},
		},
		{
			name: "with browser exposes web_extract and research",
			cfg:  Config{EnableFocusedTasks: true, EnableBuiltins: true, EnableMemory: true, EnableBrowser: true},
			want: []string{"recall", "explore", "web_extract", "research", "verify", "review"},
		},
		{
			name: "no memory and no builtins still has session-backed recall + file-tool-backed others",
			cfg:  Config{EnableFocusedTasks: true},
			// Probe: HasLLM, HasWorkspace, HasSessions all true;
			// HasMemory/HasExecutor/HasWeb/HasBrowser are false. Recall
			// stays available via sessions; explore/verify/review via
			// read_file+list_files; research is filtered out.
			want: []string{"recall", "explore", "verify", "review"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := focusedTaskProfilesForLog(c.cfg)
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("got %+v\nwant %+v", got, c.want)
			}
		})
	}
}

// TestLogServeStartup_IncludesFocusedTaskProfiles pins the wire shape
// of the startup log: a "focused_task_profiles" JSON array appears
// when the feature is on, with the actual profile list. zerolog's
// JSON encoder writes Strs as a JSON array, so the array form is
// what trace/log consumers will see.
func TestLogServeStartup_IncludesFocusedTaskProfiles(t *testing.T) {
	var buf bytes.Buffer
	cfg := Config{
		Listen:             "127.0.0.1:7777",
		BaseURL:            "https://example/v1",
		Model:              "demo",
		MaxSessions:        8,
		SessionIdleTTL:     "5m",
		EnableBuiltins:     true,
		EnableMemory:       true,
		EnableFocusedTasks: true,
	}
	logServeStartup(zerolog.New(&buf), cfg, cfg.MemoryRoot)
	line := buf.String()
	if !strings.Contains(line, `"focused_task_profiles":["recall","explore","verify","review"]`) {
		t.Fatalf("startup log missing focused_task_profiles array:\n%s", line)
	}
}

// TestLogServeStartup_OmitsFocusedTaskProfilesWhenDisabled pins the
// omitempty-like behavior: with focused tasks disabled, the field is
// emitted as an empty array (zerolog renders nil slice as []), which
// signals "feature off" without lying about which profiles would be
// available. We assert the explicit empty form rather than asserting
// absence, because zerolog Strs always emits the key.
func TestLogServeStartup_OmitsFocusedTaskProfilesWhenDisabled(t *testing.T) {
	var buf bytes.Buffer
	cfg := Config{
		Listen:             "127.0.0.1:7777",
		BaseURL:            "https://example/v1",
		Model:              "demo",
		MaxSessions:        8,
		SessionIdleTTL:     "5m",
		EnableBuiltins:     true,
		EnableMemory:       true,
		EnableFocusedTasks: false,
	}
	logServeStartup(zerolog.New(&buf), cfg, cfg.MemoryRoot)
	line := buf.String()
	if !strings.Contains(line, `"focused_tasks":false`) {
		t.Fatalf("expected focused_tasks=false:\n%s", line)
	}
	if !strings.Contains(line, `"focused_task_profiles":[]`) {
		t.Fatalf("expected empty focused_task_profiles array when feature is off:\n%s", line)
	}
}

func TestParseFlagsAndConfig_RejectsNonPositiveLimitsFromCLI(t *testing.T) {
	for _, c := range []struct {
		name string
		args []string
		want string
	}{
		{
			name: "max sessions zero",
			args: []string{"--base-url", "https://example/v1", "--model", "m", "--max-sessions", "0"},
			want: "max_sessions",
		},
		{
			name: "max sessions negative",
			args: []string{"--base-url", "https://example/v1", "--model", "m", "--max-sessions", "-1"},
			want: "max_sessions",
		},
		{
			name: "subagent depth zero",
			args: []string{"--base-url", "https://example/v1", "--model", "m", "--subagent-max-depth", "0"},
			want: "subagent_max_depth",
		},
		{
			name: "max turn steps negative",
			args: []string{"--base-url", "https://example/v1", "--model", "m", "--max-turn-steps", "-1"},
			want: "max_turn_steps",
		},
		{
			name: "compact trigger negative",
			args: []string{"--base-url", "https://example/v1", "--model", "m", "--compact-trigger", "-1"},
			want: "compact_trigger",
		},
		{
			name: "compact keep last negative",
			args: []string{"--base-url", "https://example/v1", "--model", "m", "--compact-keep-last", "-1"},
			want: "compact_keep_last",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := parseFlagsAndConfig(c.args)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("parseFlagsAndConfig error = %v, want contains %q", err, c.want)
			}
		})
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

// TestParseFlagsAndConfig_SamplingFromEnv pins env support for the
// three sampling knobs. Eval rigs running batches of affentserve
// containers need to set deterministic decoding via env
// (AFFENTSERVE_TEMPERATURE=0) without inlining --temperature into
// every CMD. Same pattern as AFFENTSERVE_MODEL.
//
// Also: the parser uses pointers (so "unset" / "0" stays
// distinguishable for the wire layer). A pointer to 0 must survive
// env parsing — otherwise temperature=0 evals silently revert to
// the provider default the moment env is the source.
func TestParseFlagsAndConfig_SamplingFromEnv(t *testing.T) {
	t.Setenv("AFFENTSERVE_BASE_URL", "https://example/v1")
	t.Setenv("AFFENTSERVE_MODEL", "m")
	t.Setenv("AFFENTSERVE_TEMPERATURE", "0")
	t.Setenv("AFFENTSERVE_TOP_P", "0.95")
	t.Setenv("AFFENTSERVE_MAX_TOKENS", "512")

	cfg, err := parseFlagsAndConfig(nil)
	if err != nil {
		t.Fatalf("parseFlagsAndConfig: %v", err)
	}
	if cfg.Temperature == nil {
		t.Fatal("Temperature pointer must be non-nil when env is 0 (not the same as unset)")
	}
	if *cfg.Temperature != 0 {
		t.Errorf("Temperature = %v, want 0", *cfg.Temperature)
	}
	if cfg.TopP == nil || *cfg.TopP != 0.95 {
		t.Errorf("TopP = %v, want 0.95", cfg.TopP)
	}
	if cfg.MaxTokens == nil || *cfg.MaxTokens != 512 {
		t.Errorf("MaxTokens = %v, want 512", cfg.MaxTokens)
	}
}

// TestParseFlagsAndConfig_SamplingEnvRejectsMalformed ensures a
// typo in AFFENTSERVE_TEMPERATURE fails at boot (a 5xx during the
// first chat request would otherwise be the first signal).
func TestParseFlagsAndConfig_SamplingEnvRejectsMalformed(t *testing.T) {
	t.Setenv("AFFENTSERVE_BASE_URL", "https://example/v1")
	t.Setenv("AFFENTSERVE_MODEL", "m")
	t.Setenv("AFFENTSERVE_TEMPERATURE", "warm")
	_, err := parseFlagsAndConfig(nil)
	if err == nil || !strings.Contains(err.Error(), "AFFENTSERVE_TEMPERATURE") {
		t.Errorf("malformed AFFENTSERVE_TEMPERATURE must fail boot; got err=%v", err)
	}
}

func TestParseFlagsAndConfig_RuntimeBoundaryEnv(t *testing.T) {
	t.Setenv("AFFENTSERVE_BASE_URL", "https://example/v1")
	t.Setenv("AFFENTSERVE_MODEL", "m")
	t.Setenv("AFFENTSERVE_MAX_SESSIONS", "7")
	t.Setenv("AFFENTSERVE_SESSION_IDLE_TTL", "2m")
	t.Setenv("AFFENTSERVE_MAX_TURN_STEPS", "11")
	t.Setenv("AFFENTSERVE_PER_CALL_TIMEOUT", "9m")
	t.Setenv("AFFENTSERVE_MAX_TRANSIENT_RETRIES", "-1")
	t.Setenv("AFFENTSERVE_RETRY_BACKOFF", "6s")
	t.Setenv("AFFENTSERVE_COMPACT_TRIGGER", "120")
	t.Setenv("AFFENTSERVE_COMPACT_KEEP_LAST", "6")

	cfg, err := parseFlagsAndConfig(nil)
	if err != nil {
		t.Fatalf("parseFlagsAndConfig: %v", err)
	}
	if cfg.MaxSessions != 7 || !cfg.maxSessionsSet {
		t.Fatalf("MaxSessions = %d set=%t, want 7 true", cfg.MaxSessions, cfg.maxSessionsSet)
	}
	if cfg.SessionIdleTTL != "2m" {
		t.Fatalf("SessionIdleTTL = %q, want 2m", cfg.SessionIdleTTL)
	}
	if cfg.MaxTurnSteps != 11 {
		t.Fatalf("MaxTurnSteps = %d, want 11", cfg.MaxTurnSteps)
	}
	if cfg.PerCallTimeout != "9m" {
		t.Fatalf("PerCallTimeout = %q, want 9m", cfg.PerCallTimeout)
	}
	if cfg.MaxTransientRetries != -1 {
		t.Fatalf("MaxTransientRetries = %d, want -1", cfg.MaxTransientRetries)
	}
	if cfg.RetryBackoff != "6s" {
		t.Fatalf("RetryBackoff = %q, want 6s", cfg.RetryBackoff)
	}
	if cfg.CompactTrigger != 120 {
		t.Fatalf("CompactTrigger = %d, want 120", cfg.CompactTrigger)
	}
	if cfg.CompactKeepLast != 6 {
		t.Fatalf("CompactKeepLast = %d, want 6", cfg.CompactKeepLast)
	}
}

func TestParseFlagsAndConfig_RuntimeBoundaryEnvRejectsMalformed(t *testing.T) {
	t.Setenv("AFFENTSERVE_BASE_URL", "https://example/v1")
	t.Setenv("AFFENTSERVE_MODEL", "m")
	t.Setenv("AFFENTSERVE_MAX_TURN_STEPS", "many")
	_, err := parseFlagsAndConfig(nil)
	if err == nil || !strings.Contains(err.Error(), "AFFENTSERVE_MAX_TURN_STEPS") {
		t.Fatalf("malformed AFFENTSERVE_MAX_TURN_STEPS must fail boot; got err=%v", err)
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
