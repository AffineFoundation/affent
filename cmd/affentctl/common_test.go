package main

import (
	"context"
	"encoding/json"
	"flag"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/affinefoundation/affent/internal/agent"
	"github.com/affinefoundation/affent/internal/loopstate"
	"github.com/affinefoundation/affent/internal/mcp"
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

func TestReadMaybeStdin_AtFileRejectsOversize(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "huge.txt")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", maxPromptInputBytes+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := readMaybeStdin("@" + path)
	if err == nil || !strings.Contains(err.Error(), "prompt input exceeds") {
		t.Fatalf("oversized prompt file error = %v, want prompt input exceeds", err)
	}
}

func TestReadMaybeStdin_StdinRejectsOversize(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = orig })

	go func() {
		_, _ = io.WriteString(w, strings.Repeat("x", maxPromptInputBytes+1))
		_ = w.Close()
	}()

	_, err = readMaybeStdin("-")
	if err == nil || !strings.Contains(err.Error(), "prompt input exceeds") {
		t.Fatalf("oversized stdin prompt error = %v, want prompt input exceeds", err)
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
	if err := os.WriteFile(cfgPath, []byte(`{"model":"from-config","base_url":"http://from-config","temperature":"0.7","top_p":"0.8","max_tokens":"256","seed":"99"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AFFENTCTL_MODEL", "from-env")
	t.Setenv("AFFENTCTL_TEMPERATURE", "0")
	t.Setenv("AFFENTCTL_TOP_P", "0.95")
	t.Setenv("AFFENTCTL_MAX_TOKENS", "512")
	t.Setenv("AFFENTCTL_SEED", "42")
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
	if cf.temperature != "0" || cf.topP != "0.95" || cf.maxTokens != "512" || cf.seed != "42" {
		t.Errorf("sampling env should win over config; got temperature=%q top_p=%q max_tokens=%q seed=%q", cf.temperature, cf.topP, cf.maxTokens, cf.seed)
	}
}

func TestApplyConfigRejectsUnknownConfigFields(t *testing.T) {
	for _, c := range []struct {
		name string
		body string
		want string
	}{
		{
			name: "top level",
			body: `{"model":"m","modle":"typo"}`,
			want: "unknown field \"modle\"",
		},
		{
			name: "nested memory",
			body: `{"memory":{"max_topicz":3}}`,
			want: "unknown field \"max_topicz\"",
		},
		{
			name: "nested compact",
			body: `{"compact":{"keep":3}}`,
			want: "unknown field \"keep\"",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			cfgPath := filepath.Join(dir, "c.json")
			if err := os.WriteFile(cfgPath, []byte(c.body), 0o644); err != nil {
				t.Fatal(err)
			}
			var cf commonFlags
			fs := flag.NewFlagSet("test", flag.ContinueOnError)
			cf.bind(fs)
			if err := fs.Parse([]string{"--config", cfgPath}); err != nil {
				t.Fatal(err)
			}
			err := applyConfig(&cf, fs)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("error = %v, want contains %q", err, c.want)
			}
		})
	}
}

func TestApplyConfigRejectsOversizeConfigFile(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "huge.json")
	if err := os.WriteFile(cfgPath, []byte(strings.Repeat("x", maxConfigInputBytes+1)), 0o644); err != nil {
		t.Fatal(err)
	}
	var cf commonFlags
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	cf.bind(fs)
	if err := fs.Parse([]string{"--config", cfgPath}); err != nil {
		t.Fatal(err)
	}
	err := applyConfig(&cf, fs)
	if err == nil || !strings.Contains(err.Error(), "config exceeds") {
		t.Fatalf("error = %v, want config exceeds", err)
	}
}

func TestApplyConfigRejectsMultipleJSONValues(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "multi.json")
	if err := os.WriteFile(cfgPath, []byte(`{"model":"one"} {"model":"two"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var cf commonFlags
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	cf.bind(fs)
	if err := fs.Parse([]string{"--config", cfgPath}); err != nil {
		t.Fatal(err)
	}
	err := applyConfig(&cf, fs)
	if err == nil || !strings.Contains(err.Error(), "multiple JSON values") {
		t.Fatalf("error = %v, want multiple JSON values", err)
	}
}

func TestMCPConfigServerParsesInitTimeout(t *testing.T) {
	spec, err := (mcpConfigServer{
		Name:        "maps",
		Command:     "sh",
		InitTimeout: "250ms",
	}).serverSpec()
	if err != nil {
		t.Fatal(err)
	}
	if spec.InitTimeout != 250*time.Millisecond {
		t.Fatalf("InitTimeout = %s, want 250ms", spec.InitTimeout)
	}
	_, err = (mcpConfigServer{Name: "bad", Command: "sh", InitTimeout: "0s"}).serverSpec()
	if err == nil || !strings.Contains(err.Error(), "must be positive") {
		t.Fatalf("zero init timeout error = %v, want positive rejection", err)
	}
}

func TestMCPConfigServerParsesToolFilters(t *testing.T) {
	spec, err := (mcpConfigServer{
		Name:       "maps",
		Command:    "sh",
		AllowTools: []string{"search", "geocode"},
		DenyTools:  []string{"admin_delete"},
	}).serverSpec()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(spec.ToolAllowlist, ",") != "search,geocode" {
		t.Fatalf("ToolAllowlist = %v", spec.ToolAllowlist)
	}
	if strings.Join(spec.ToolDenylist, ",") != "admin_delete" {
		t.Fatalf("ToolDenylist = %v", spec.ToolDenylist)
	}
}

func TestMCPStartupTimeoutHonorsConfiguredInitTimeout(t *testing.T) {
	got := mcpStartupTimeout([]mcp.ServerSpec{
		{Name: "slow", InitTimeout: 2 * time.Minute},
	})
	want := 2*time.Minute + mcpStartupPerServerOverrun
	if got != want {
		t.Fatalf("startup timeout = %s, want %s", got, want)
	}
}

func TestMCPStartupTimeoutScalesWithDefaultServers(t *testing.T) {
	got := mcpStartupTimeout([]mcp.ServerSpec{{Name: "one"}, {Name: "two"}})
	want := 2 * (mcp.DefaultInitTimeout + mcpStartupPerServerOverrun)
	if got != want {
		t.Fatalf("startup timeout = %s, want %s", got, want)
	}
}

func TestMCPStartupTimeoutKeepsMinimumForSmallConfigs(t *testing.T) {
	got := mcpStartupTimeout([]mcp.ServerSpec{{Name: "fast", InitTimeout: time.Second}})
	if got != minMCPStartupTimeout {
		t.Fatalf("startup timeout = %s, want minimum %s", got, minMCPStartupTimeout)
	}
}

func TestSubagentCanBeDisabledFromConfigEnvAndCLI(t *testing.T) {
	t.Run("config subagent false", func(t *testing.T) {
		dir := t.TempDir()
		cfgPath := filepath.Join(dir, "c.json")
		if err := os.WriteFile(cfgPath, []byte(`{"subagent":false}`), 0o644); err != nil {
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
		if cf.subagentEnabled {
			t.Fatal("config subagent:false should disable subagent")
		}
	})

	t.Run("enable_subagent alias", func(t *testing.T) {
		dir := t.TempDir()
		cfgPath := filepath.Join(dir, "c.json")
		if err := os.WriteFile(cfgPath, []byte(`{"enable_subagent":false}`), 0o644); err != nil {
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
		if cf.subagentEnabled {
			t.Fatal("config enable_subagent:false should disable subagent")
		}
	})

	t.Run("env beats config", func(t *testing.T) {
		dir := t.TempDir()
		cfgPath := filepath.Join(dir, "c.json")
		if err := os.WriteFile(cfgPath, []byte(`{"subagent":true}`), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Setenv("AFFENTCTL_SUBAGENT", "false")
		var cf commonFlags
		fs := flag.NewFlagSet("test", flag.ContinueOnError)
		cf.bind(fs)
		if err := fs.Parse([]string{"--config", cfgPath}); err != nil {
			t.Fatal(err)
		}
		if err := applyConfig(&cf, fs); err != nil {
			t.Fatal(err)
		}
		if cf.subagentEnabled {
			t.Fatal("AFFENTCTL_SUBAGENT=false should win over config")
		}
	})

	t.Run("cli beats env", func(t *testing.T) {
		t.Setenv("AFFENTCTL_SUBAGENT", "false")
		var cf commonFlags
		fs := flag.NewFlagSet("test", flag.ContinueOnError)
		cf.bind(fs)
		if err := fs.Parse([]string{"--subagent=true"}); err != nil {
			t.Fatal(err)
		}
		if err := applyConfig(&cf, fs); err != nil {
			t.Fatal(err)
		}
		if !cf.subagentEnabled {
			t.Fatal("--subagent=true should win over AFFENTCTL_SUBAGENT=false")
		}
	})
}

func TestTypedEnvConfigRejectsInvalidValues(t *testing.T) {
	cases := []struct {
		name string
		env  string
		val  string
		want string
	}{
		{
			name: "subagent bool",
			env:  "AFFENTCTL_SUBAGENT",
			val:  "sometimes",
			want: "AFFENTCTL_SUBAGENT=\"sometimes\"",
		},
		{
			name: "eval mode bool",
			env:  "AFFENTCTL_EVAL_MODE",
			val:  "sometimes",
			want: "AFFENTCTL_EVAL_MODE=\"sometimes\"",
		},
		{
			name: "eval all tools bool",
			env:  "AFFENTCTL_EVAL_ALL_TOOLS",
			val:  "sometimes",
			want: "AFFENTCTL_EVAL_ALL_TOOLS=\"sometimes\"",
		},
		{
			name: "web bool",
			env:  "AFFENTCTL_WEB",
			val:  "sometimes",
			want: "AFFENTCTL_WEB=\"sometimes\"",
		},
		{
			name: "web search bool",
			env:  "AFFENTCTL_WEB_SEARCH",
			val:  "sometimes",
			want: "AFFENTCTL_WEB_SEARCH=\"sometimes\"",
		},
		{
			name: "subagent max depth int",
			env:  "AFFENTCTL_SUBAGENT_MAX_DEPTH",
			val:  "deep",
			want: "AFFENTCTL_SUBAGENT_MAX_DEPTH=\"deep\"",
		},
		{
			name: "memory bool",
			env:  "AFFENTCTL_MEMORY",
			val:  "sometimes",
			want: "AFFENTCTL_MEMORY=\"sometimes\"",
		},
		{
			name: "max turns int",
			env:  "AFFENTCTL_MAX_TURNS",
			val:  "many",
			want: "AFFENTCTL_MAX_TURNS=\"many\"",
		},
		{
			name: "call timeout duration",
			env:  "AFFENTCTL_MAX_CALL_TIMEOUT",
			val:  "soon",
			want: "AFFENTCTL_MAX_CALL_TIMEOUT=\"soon\"",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(tc.env, tc.val)
			var cf commonFlags
			fs := flag.NewFlagSet("test", flag.ContinueOnError)
			cf.bind(fs)
			err := applyConfig(&cf, fs)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("applyConfig error = %v, want contains %q", err, tc.want)
			}
		})
	}
}

func TestTypedEnvConfigInvalidValueIgnoredWhenCLIOverrides(t *testing.T) {
	t.Setenv("AFFENTCTL_SUBAGENT", "sometimes")
	t.Setenv("AFFENTCTL_SUBAGENT_MAX_DEPTH", "deep")
	var cf commonFlags
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	cf.bind(fs)
	if err := fs.Parse([]string{"--subagent=true", "--subagent-max-depth=1"}); err != nil {
		t.Fatal(err)
	}
	if err := applyConfig(&cf, fs); err != nil {
		t.Fatal(err)
	}
	if !cf.subagentEnabled || cf.subagentMaxDepth != 1 {
		t.Fatalf("CLI override not honored: subagent=%t depth=%d", cf.subagentEnabled, cf.subagentMaxDepth)
	}
}

func TestPortableEnvConfigOverridesConfig(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "affent.json")
	if err := os.WriteFile(cfgPath, []byte(`{
		"max_turns": 7,
		"max_call_timeout": "11s",
		"retry_transient": 2,
		"retry_backoff": "3s",
		"memory": {
			"enabled": true,
			"only": false,
			"max_chars": "100,200",
			"topic_max_chars": 300,
			"max_topics": 4
		},
		"project_context": true,
		"compact": {
			"trigger": 120,
			"keep_last": 8
		}
	}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AFFENTCTL_MAX_TURNS", "9")
	t.Setenv("AFFENTCTL_MAX_CALL_TIMEOUT", "13s")
	t.Setenv("AFFENTCTL_RETRY_TRANSIENT", "5")
	t.Setenv("AFFENTCTL_RETRY_BACKOFF", "7s")
	t.Setenv("AFFENTCTL_MEMORY", "false")
	t.Setenv("AFFENTCTL_MEMORY_MAX_CHARS", "2200,1375")
	t.Setenv("AFFENTCTL_MEMORY_TOPIC_MAX_CHARS", "4400")
	t.Setenv("AFFENTCTL_MEMORY_MAX_TOPICS", "32")
	t.Setenv("AFFENTCTL_PROJECT_CONTEXT", "false")
	t.Setenv("AFFENTCTL_COMPACT_TRIGGER", "240")
	t.Setenv("AFFENTCTL_COMPACT_KEEP_LAST", "10")

	var cf commonFlags
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	cf.bind(fs)
	if err := fs.Parse([]string{"--config", cfgPath}); err != nil {
		t.Fatal(err)
	}
	if err := applyConfig(&cf, fs); err != nil {
		t.Fatal(err)
	}
	if cf.maxTurns != 9 || cf.callTimeout != 13*time.Second ||
		cf.retryTransient != 5 || cf.retryBackoff != 7*time.Second {
		t.Fatalf("runtime env not applied: maxTurns=%d callTimeout=%s retryTransient=%d retryBackoff=%s", cf.maxTurns, cf.callTimeout, cf.retryTransient, cf.retryBackoff)
	}
	if cf.memoryEnabled || cf.memoryMaxChars != "2200,1375" ||
		cf.memoryTopicMaxChars != 4400 || cf.memoryMaxTopics != 32 {
		t.Fatalf("memory env not applied: enabled=%t maxChars=%q topic=%d topics=%d", cf.memoryEnabled, cf.memoryMaxChars, cf.memoryTopicMaxChars, cf.memoryMaxTopics)
	}
	if cf.projectContext {
		t.Fatal("project context env should disable project context")
	}
	if cf.compactTrigger != 240 || cf.compactKeepLast != 10 {
		t.Fatalf("compact env not applied: trigger=%d keep_last=%d", cf.compactTrigger, cf.compactKeepLast)
	}
}

func TestMemoryOnlyEnvStillAppliesIsolationMode(t *testing.T) {
	t.Setenv("AFFENTCTL_MEMORY_ONLY", "true")
	t.Setenv("AFFENTCTL_PROJECT_CONTEXT", "true")
	var cf commonFlags
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	cf.bind(fs)
	if err := applyConfig(&cf, fs); err != nil {
		t.Fatal(err)
	}
	if !cf.memoryEnabled || !cf.memoryOnly {
		t.Fatalf("memory-only env should enable memory-only, got memory=%t only=%t", cf.memoryEnabled, cf.memoryOnly)
	}
	if cf.projectContext || cf.subagentEnabled || cf.focusedTasksEnabled {
		t.Fatalf("memory-only env should isolate runtime: project_context=%t subagent=%t focused_tasks=%t", cf.projectContext, cf.subagentEnabled, cf.focusedTasksEnabled)
	}
}

func TestEvalModeEnvAppliesStrictToolSurface(t *testing.T) {
	t.Setenv("AFFENTCTL_EVAL_MODE", "true")
	t.Setenv("AFFENTCTL_MCP_CONFIG", "/tmp/eval-mcp.json")
	t.Setenv("AFFENTCTL_PROJECT_CONTEXT", "true")
	t.Setenv("AFFENTCTL_SUBAGENT", "true")
	t.Setenv("AFFENTCTL_FOCUSED_TASKS", "true")
	var cf commonFlags
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	cf.bind(fs)
	if err := applyConfig(&cf, fs); err != nil {
		t.Fatal(err)
	}
	if !cf.evalMode {
		t.Fatal("AFFENTCTL_EVAL_MODE=true should enable eval mode")
	}
	caps := resolveRuntimeCapabilities(cf)
	if caps.Builtins || !caps.MCP {
		t.Fatalf("eval mode should disable builtins by default and allow explicit MCP config, caps=%+v", caps)
	}
	if caps.Memory || caps.Skill || caps.Plan || caps.SessionSearch || caps.ProjectContext || caps.RepoSearch || caps.SymbolContext || caps.WebFetch || caps.WebSearch || caps.Browser || caps.Subagent || caps.FocusedTasks {
		t.Fatalf("eval mode should default to a no-tool benchmark surface except explicit MCP config, caps=%+v", caps)
	}
	if cf.memoryOnly {
		t.Fatalf("eval mode should not imply memory-only: memory_only=%t", cf.memoryOnly)
	}
}

func TestEvalModeAllowsExplicitMemory(t *testing.T) {
	var cf commonFlags
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	cf.bind(fs)
	if err := fs.Parse([]string{"--eval-mode", "--memory=true"}); err != nil {
		t.Fatal(err)
	}
	if err := applyConfig(&cf, fs); err != nil {
		t.Fatal(err)
	}
	caps := resolveRuntimeCapabilities(cf)
	if !caps.Memory {
		t.Fatalf("--eval-mode --memory=true should enable memory, caps=%+v", caps)
	}
	if caps.Builtins || caps.Skill || caps.Plan || caps.SessionSearch || caps.RepoSearch || caps.SymbolContext || caps.WebFetch || caps.WebSearch || caps.Browser || caps.Subagent || caps.FocusedTasks {
		t.Fatalf("explicit memory must not re-enable other eval surfaces, caps=%+v", caps)
	}
}

func TestEvalModeAllowsExplicitWeb(t *testing.T) {
	var cf commonFlags
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	cf.bind(fs)
	if err := fs.Parse([]string{"--eval-mode", "--web", "--web-search"}); err != nil {
		t.Fatal(err)
	}
	if err := applyConfig(&cf, fs); err != nil {
		t.Fatal(err)
	}
	caps := resolveRuntimeCapabilities(cf)
	if !caps.WebFetch || !caps.WebSearch {
		t.Fatalf("--eval-mode --web --web-search should enable web tools, caps=%+v", caps)
	}
	if caps.Memory || caps.Skill || caps.Plan || caps.SessionSearch || caps.ProjectContext || caps.Browser || caps.Subagent || caps.FocusedTasks {
		t.Fatalf("explicit web must not re-enable non-basic eval surfaces, caps=%+v", caps)
	}
}

func TestEvalToolsWebGroupEnablesFetchAndSearch(t *testing.T) {
	var cf commonFlags
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	cf.bind(fs)
	if err := fs.Parse([]string{"--eval-tools=web"}); err != nil {
		t.Fatal(err)
	}
	if err := applyConfig(&cf, fs); err != nil {
		t.Fatal(err)
	}
	caps := resolveRuntimeCapabilities(cf)
	if !caps.WebFetch || !caps.WebSearch {
		t.Fatalf("--eval-tools=web should enable both fetch and search, caps=%+v", caps)
	}
	if caps.Builtins || caps.Memory || caps.Skill || caps.Plan || caps.SessionSearch || caps.ProjectContext || caps.Browser || caps.Subagent || caps.FocusedTasks {
		t.Fatalf("--eval-tools=web must not enable unrelated surfaces, caps=%+v", caps)
	}
}

func TestEvalModeAllowsExplicitEvalTools(t *testing.T) {
	var cf commonFlags
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	cf.bind(fs)
	if err := fs.Parse([]string{"--eval-mode", "--eval-tools=read_file,shell,web_search"}); err != nil {
		t.Fatal(err)
	}
	if err := applyConfig(&cf, fs); err != nil {
		t.Fatal(err)
	}
	caps := resolveRuntimeCapabilities(cf)
	if !caps.Builtins || !caps.WebFetch || !caps.WebSearch {
		t.Fatalf("--eval-tools should enable requested tool families, caps=%+v", caps)
	}
	if caps.Memory || caps.Skill || caps.Plan || caps.SessionSearch || caps.ProjectContext || caps.Browser || caps.Subagent || caps.FocusedTasks {
		t.Fatalf("--eval-tools must not enable unrequested surfaces, caps=%+v", caps)
	}

	var history commonFlags
	historyFS := flag.NewFlagSet("test", flag.ContinueOnError)
	history.bind(historyFS)
	if err := historyFS.Parse([]string{"--eval-mode", "--eval-tools=session_search"}); err != nil {
		t.Fatal(err)
	}
	if err := applyConfig(&history, historyFS); err != nil {
		t.Fatal(err)
	}
	historyCaps := resolveRuntimeCapabilities(history)
	if historyCaps.Builtins || !historyCaps.SessionSearch {
		t.Fatalf("--eval-tools=session_search should enable history recall without builtins, caps=%+v", historyCaps)
	}
	if historyCaps.Memory || historyCaps.Skill || historyCaps.Plan || historyCaps.ProjectContext || historyCaps.WebFetch || historyCaps.WebSearch || historyCaps.Browser || historyCaps.Subagent || historyCaps.FocusedTasks {
		t.Fatalf("--eval-tools=session_search must not enable unrelated surfaces, caps=%+v", historyCaps)
	}

	var recall commonFlags
	recallFS := flag.NewFlagSet("test", flag.ContinueOnError)
	recall.bind(recallFS)
	if err := recallFS.Parse([]string{"--eval-mode", "--eval-tools=recall"}); err != nil {
		t.Fatal(err)
	}
	if err := applyConfig(&recall, recallFS); err != nil {
		t.Fatal(err)
	}
	recallCaps := resolveRuntimeCapabilities(recall)
	if recallCaps.Builtins || !recallCaps.Memory || !recallCaps.SessionSearch {
		t.Fatalf("--eval-tools=recall should enable memory and session_search without builtins, caps=%+v", recallCaps)
	}
	if recallCaps.Skill || recallCaps.Plan || recallCaps.ProjectContext || recallCaps.WebFetch || recallCaps.WebSearch || recallCaps.Browser || recallCaps.Subagent || recallCaps.FocusedTasks {
		t.Fatalf("--eval-tools=recall must not enable unrelated surfaces, caps=%+v", recallCaps)
	}
}

func TestEvalToolFlagsImplyEvalMode(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want func(runtimeCapabilities) bool
	}{
		{
			name: "allowlist",
			args: []string{"--eval-tools=read_file"},
			want: func(caps runtimeCapabilities) bool {
				return caps.Builtins && !caps.Memory && !caps.WebFetch && !caps.Browser && !caps.Subagent && !caps.FocusedTasks
			},
		},
		{
			name: "all tools",
			args: []string{"--eval-all-tools"},
			want: func(caps runtimeCapabilities) bool {
				return caps.Builtins && caps.Memory && caps.Skill && caps.Plan && caps.SessionSearch && caps.WebFetch && caps.WebSearch && caps.Browser && caps.BrowserScreenshot && caps.Subagent && caps.FocusedTasks && !caps.ProjectContext
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var cf commonFlags
			fs := flag.NewFlagSet("test", flag.ContinueOnError)
			cf.bind(fs)
			if err := fs.Parse(tc.args); err != nil {
				t.Fatal(err)
			}
			if err := applyConfig(&cf, fs); err != nil {
				t.Fatal(err)
			}
			if !cf.evalMode {
				t.Fatalf("applyConfig(%v) should infer eval mode", tc.args)
			}
			caps := resolveRuntimeCapabilities(cf)
			if !tc.want(caps) {
				t.Fatalf("resolveRuntimeCapabilities(%v) = %+v", tc.args, caps)
			}
		})
	}
}

func TestEvalModeAllToolsEnablesFullSurface(t *testing.T) {
	var cf commonFlags
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	cf.bind(fs)
	if err := fs.Parse([]string{"--eval-mode", "--eval-all-tools", "--mcp-config", "/tmp/mcp.json"}); err != nil {
		t.Fatal(err)
	}
	if err := applyConfig(&cf, fs); err != nil {
		t.Fatal(err)
	}
	caps := resolveRuntimeCapabilities(cf)
	if !caps.Builtins || !caps.Memory || !caps.MCP || !caps.Skill || !caps.Plan || !caps.SessionSearch || !caps.SymbolContext || !caps.RepoSearch || !caps.WebFetch || !caps.WebSearch || !caps.Browser || !caps.BrowserScreenshot || !caps.Subagent || !caps.FocusedTasks {
		t.Fatalf("--eval-all-tools should enable the full tool surface, caps=%+v", caps)
	}
	if caps.ProjectContext {
		t.Fatalf("--eval-all-tools should not re-enable project context, caps=%+v", caps)
	}
}

func TestEvalToolsAllDoesNotRequireMCPConfig(t *testing.T) {
	var cf commonFlags
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	cf.bind(fs)
	if err := fs.Parse([]string{"--eval-mode", "--eval-tools=all"}); err != nil {
		t.Fatal(err)
	}
	if err := applyConfig(&cf, fs); err != nil {
		t.Fatal(err)
	}
	caps := resolveRuntimeCapabilities(cf)
	if !caps.Builtins || !caps.Memory || caps.MCP || !caps.WebFetch || !caps.WebSearch || !caps.Browser || !caps.Subagent || !caps.FocusedTasks {
		t.Fatalf("--eval-tools=all should enable built-in available tools without requiring MCP config, caps=%+v", caps)
	}
}

func TestWebCanBeEnabledFromConfigEnvAndCLI(t *testing.T) {
	t.Run("config aliases", func(t *testing.T) {
		dir := t.TempDir()
		cfgPath := filepath.Join(dir, "c.json")
		if err := os.WriteFile(cfgPath, []byte(`{"enable_web":true,"enable_web_search":true}`), 0o644); err != nil {
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
		if !cf.webEnabled || !cf.webSearchEnabled {
			t.Fatalf("config web aliases not applied: web=%t search=%t", cf.webEnabled, cf.webSearchEnabled)
		}
	})

	t.Run("env beats config", func(t *testing.T) {
		dir := t.TempDir()
		cfgPath := filepath.Join(dir, "c.json")
		if err := os.WriteFile(cfgPath, []byte(`{"web":false,"web_search":false}`), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Setenv("AFFENTCTL_WEB", "true")
		t.Setenv("AFFENTCTL_WEB_SEARCH", "true")
		var cf commonFlags
		fs := flag.NewFlagSet("test", flag.ContinueOnError)
		cf.bind(fs)
		if err := fs.Parse([]string{"--config", cfgPath}); err != nil {
			t.Fatal(err)
		}
		if err := applyConfig(&cf, fs); err != nil {
			t.Fatal(err)
		}
		if !cf.webEnabled || !cf.webSearchEnabled {
			t.Fatalf("env web flags not applied: web=%t search=%t", cf.webEnabled, cf.webSearchEnabled)
		}
	})

	t.Run("cli beats env", func(t *testing.T) {
		t.Setenv("AFFENTCTL_WEB", "false")
		t.Setenv("AFFENTCTL_WEB_SEARCH", "false")
		var cf commonFlags
		fs := flag.NewFlagSet("test", flag.ContinueOnError)
		cf.bind(fs)
		if err := fs.Parse([]string{"--web=true", "--web-search=true"}); err != nil {
			t.Fatal(err)
		}
		if err := applyConfig(&cf, fs); err != nil {
			t.Fatal(err)
		}
		if !cf.webEnabled || !cf.webSearchEnabled {
			t.Fatalf("cli web flags not applied: web=%t search=%t", cf.webEnabled, cf.webSearchEnabled)
		}
	})
}

func TestBrowserCanBeEnabledFromConfigEnvAndCLI(t *testing.T) {
	t.Run("config aliases", func(t *testing.T) {
		dir := t.TempDir()
		cfgPath := filepath.Join(dir, "c.json")
		if err := os.WriteFile(cfgPath, []byte(`{"enable_browser":true,"browser_screenshot":true}`), 0o644); err != nil {
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
		if !cf.browserEnabled || !cf.browserScreenshot {
			t.Fatalf("config browser aliases not applied: browser=%t screenshot=%t", cf.browserEnabled, cf.browserScreenshot)
		}
	})

	t.Run("env beats config", func(t *testing.T) {
		dir := t.TempDir()
		cfgPath := filepath.Join(dir, "c.json")
		if err := os.WriteFile(cfgPath, []byte(`{"browser":false,"browser_screenshot":false}`), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Setenv("AFFENTCTL_BROWSER", "true")
		t.Setenv("AFFENTCTL_BROWSER_SCREENSHOT", "true")
		var cf commonFlags
		fs := flag.NewFlagSet("test", flag.ContinueOnError)
		cf.bind(fs)
		if err := fs.Parse([]string{"--config", cfgPath}); err != nil {
			t.Fatal(err)
		}
		if err := applyConfig(&cf, fs); err != nil {
			t.Fatal(err)
		}
		if !cf.browserEnabled || !cf.browserScreenshot {
			t.Fatalf("env browser flags not applied: browser=%t screenshot=%t", cf.browserEnabled, cf.browserScreenshot)
		}
	})

	t.Run("cli beats env", func(t *testing.T) {
		t.Setenv("AFFENTCTL_BROWSER", "false")
		t.Setenv("AFFENTCTL_BROWSER_SCREENSHOT", "false")
		var cf commonFlags
		fs := flag.NewFlagSet("test", flag.ContinueOnError)
		cf.bind(fs)
		if err := fs.Parse([]string{"--browser=true", "--browser-screenshot=true"}); err != nil {
			t.Fatal(err)
		}
		if err := applyConfig(&cf, fs); err != nil {
			t.Fatal(err)
		}
		if !cf.browserEnabled || !cf.browserScreenshot {
			t.Fatalf("cli browser flags not applied: browser=%t screenshot=%t", cf.browserEnabled, cf.browserScreenshot)
		}
	})
}

func TestEvalModeCanExplicitlyEnableBrowserOnly(t *testing.T) {
	var cf commonFlags
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	cf.bind(fs)
	if err := fs.Parse([]string{"--eval-mode", "--browser=true"}); err != nil {
		t.Fatal(err)
	}
	if err := applyConfig(&cf, fs); err != nil {
		t.Fatal(err)
	}
	caps := resolveRuntimeCapabilities(cf)
	if !caps.Browser {
		t.Fatalf("--eval-mode --browser should enable browser tools, caps=%+v", caps)
	}
	if caps.Memory || caps.Skill || caps.Plan || caps.SessionSearch || caps.ProjectContext || caps.WebFetch || caps.WebSearch || caps.Subagent || caps.FocusedTasks {
		t.Fatalf("explicit browser must not re-enable non-basic eval surfaces, caps=%+v", caps)
	}
}

func TestMemoryDefaultsOnAndCanBeDisabled(t *testing.T) {
	var cf commonFlags
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	cf.bind(fs)
	if err := fs.Parse(nil); err != nil {
		t.Fatal(err)
	}
	if err := applyConfig(&cf, fs); err != nil {
		t.Fatal(err)
	}
	if !cf.memoryEnabled {
		t.Fatal("memory should default on for affentctl")
	}

	var disabled commonFlags
	fs = flag.NewFlagSet("test", flag.ContinueOnError)
	disabled.bind(fs)
	if err := fs.Parse([]string{"--memory=false"}); err != nil {
		t.Fatal(err)
	}
	if err := applyConfig(&disabled, fs); err != nil {
		t.Fatal(err)
	}
	if disabled.memoryEnabled {
		t.Fatal("--memory=false should disable memory")
	}
}

func TestSubagentMaxDepthFromConfigEnvAndCLI(t *testing.T) {
	t.Run("config", func(t *testing.T) {
		dir := t.TempDir()
		cfgPath := filepath.Join(dir, "c.json")
		if err := os.WriteFile(cfgPath, []byte(`{"subagent_max_depth":1}`), 0o644); err != nil {
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
		if cf.subagentMaxDepth != 1 {
			t.Fatalf("config subagent_max_depth = %d, want 1", cf.subagentMaxDepth)
		}
	})

	t.Run("env beats config", func(t *testing.T) {
		dir := t.TempDir()
		cfgPath := filepath.Join(dir, "c.json")
		if err := os.WriteFile(cfgPath, []byte(`{"subagent_max_depth":1}`), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Setenv("AFFENTCTL_SUBAGENT_MAX_DEPTH", "3")
		var cf commonFlags
		fs := flag.NewFlagSet("test", flag.ContinueOnError)
		cf.bind(fs)
		if err := fs.Parse([]string{"--config", cfgPath}); err != nil {
			t.Fatal(err)
		}
		if err := applyConfig(&cf, fs); err != nil {
			t.Fatal(err)
		}
		if cf.subagentMaxDepth != 3 {
			t.Fatalf("env subagent max depth = %d, want 3", cf.subagentMaxDepth)
		}
	})

	t.Run("cli beats env", func(t *testing.T) {
		t.Setenv("AFFENTCTL_SUBAGENT_MAX_DEPTH", "3")
		var cf commonFlags
		fs := flag.NewFlagSet("test", flag.ContinueOnError)
		cf.bind(fs)
		if err := fs.Parse([]string{"--subagent-max-depth=1"}); err != nil {
			t.Fatal(err)
		}
		if err := applyConfig(&cf, fs); err != nil {
			t.Fatal(err)
		}
		if cf.subagentMaxDepth != 1 {
			t.Fatalf("cli subagent max depth = %d, want 1", cf.subagentMaxDepth)
		}
	})
}

func TestSetupLoop_SubagentDisabledDoesNotRegisterToolOrPolicies(t *testing.T) {
	var cf commonFlags
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	cf.bind(fs)
	if err := fs.Parse([]string{
		"--workspace", t.TempDir(),
		"--model", "fake-model",
		"--base-url", "http://127.0.0.1:1/v1",
		"--max-turn-input-tokens", "123",
		"--subagent=false",
		"--quiet",
	}); err != nil {
		t.Fatal(err)
	}
	if err := applyConfig(&cf, fs); err != nil {
		t.Fatal(err)
	}
	b, code := setupLoop(cf)
	if code != 0 {
		t.Fatalf("setupLoop code=%d", code)
	}
	defer b.close()
	if _, ok := b.loop.Tools.Get("subagent_run"); ok {
		t.Fatal("subagent_run should not be registered when --subagent=false")
	}
	if b.loop.FirstToolPolicy != nil || b.loop.PostToolPolicy != nil {
		t.Fatal("subagent policies should not be installed when --subagent=false")
	}
	if !b.loop.FinalNoToolsOnMaxTurns {
		t.Fatal("setupLoop should request a final no-tool answer when max turns are exhausted")
	}
	if b.loop.MaxTurnInputTokens != 123 {
		t.Fatalf("MaxTurnInputTokens = %d, want 123", b.loop.MaxTurnInputTokens)
	}
	msgs := b.loop.Conv.Snapshot()
	if len(msgs) == 0 || strings.Contains(msgs[0].Content, "Subagent delegation:") {
		t.Fatal("system prompt should not include subagent guidance when disabled")
	}
	if strings.Contains(msgs[0].Content, cf.workspace) || strings.Contains(msgs[0].Content, "Use this exact path") {
		t.Fatalf("system prompt should not steer agents toward workspace absolute paths:\n%s", msgs[0].Content)
	}
	for _, want := range []string{"start in", "use relative paths", "omit cwd"} {
		if !strings.Contains(msgs[0].Content, want) {
			t.Fatalf("system prompt missing relative workspace guidance %q:\n%s", want, msgs[0].Content)
		}
	}
}

func TestSetupLoop_WebOptInRegistersTools(t *testing.T) {
	var cf commonFlags
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	cf.bind(fs)
	if err := fs.Parse([]string{
		"--workspace", t.TempDir(),
		"--model", "fake-model",
		"--base-url", "http://127.0.0.1:1/v1",
		"--web",
		"--web-search",
		"--quiet",
	}); err != nil {
		t.Fatal(err)
	}
	if err := applyConfig(&cf, fs); err != nil {
		t.Fatal(err)
	}
	b, code := setupLoop(cf)
	if code != 0 {
		t.Fatalf("setupLoop code=%d", code)
	}
	defer b.close()
	for _, name := range []string{"web_fetch", "web_search"} {
		if _, ok := b.loop.Tools.Get(name); !ok {
			t.Fatalf("%s should be registered when --web --web-search is set", name)
		}
	}
}

func TestSetupLoop_EvalModeOmitsSkillsDelegationAndSkillProvider(t *testing.T) {
	t.Setenv("AFFENTCTL_PROJECT_CONTEXT", "true")
	t.Setenv("AFFENTCTL_SUBAGENT", "true")
	t.Setenv("AFFENTCTL_FOCUSED_TASKS", "true")
	var cf commonFlags
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	cf.bind(fs)
	if err := fs.Parse([]string{
		"--workspace", t.TempDir(),
		"--model", "fake-model",
		"--base-url", "http://127.0.0.1:1/v1",
		"--eval-mode",
		"--quiet",
	}); err != nil {
		t.Fatal(err)
	}
	if err := applyConfig(&cf, fs); err != nil {
		t.Fatal(err)
	}
	b, code := setupLoop(cf)
	if code != 0 {
		t.Fatalf("setupLoop code=%d", code)
	}
	defer b.close()
	for _, name := range []string{agent.SkillToolName, agent.MemoryToolName, agent.SessionSearchToolName, agent.PlanToolName, agent.SubagentToolName, agent.FocusedTaskToolName} {
		if _, ok := b.loop.Tools.Get(name); ok {
			t.Fatalf("%s should not be registered in eval mode", name)
		}
	}
	for _, name := range []string{"shell", "read_file", "file_context", "write_file", "edit_file", "list_files", agent.SymbolContextToolName, "repo_search"} {
		if _, ok := b.loop.Tools.Get(name); ok {
			t.Fatalf("%s should not be registered by default in eval mode", name)
		}
	}
	if b.loop.FirstToolPolicy != nil || b.loop.PostToolPolicy != nil || len(b.loop.FirstToolPolicies) != 0 || len(b.loop.PostToolPolicies) != 0 {
		t.Fatal("delegation tool policies should not be installed in eval mode")
	}
	if b.loop.SkillProvider != nil {
		t.Fatal("eval mode should disable built-in skill/provider injection")
	}
	if b.loop.ProjectContextDir != "" {
		t.Fatalf("eval mode should disable project context even when env enables it, got %q", b.loop.ProjectContextDir)
	}
	if got := agent.BuiltinSkillProvider("请通过浏览器访问 https://example.com 并提取信息"); got == "" {
		t.Fatal("test prompt should trigger the built-in skill provider outside eval mode")
	}
	msgs := b.loop.Conv.Snapshot()
	if len(msgs) == 0 {
		t.Fatal("system prompt missing")
	}
	if !strings.Contains(msgs[0].Content, "Runtime context:") || !strings.Contains(msgs[0].Content, "Current UTC date:") {
		t.Fatalf("setupLoop system prompt should include runtime date context:\n%s", msgs[0].Content)
	}
	for _, forbidden := range []string{"Subagent delegation:", "Subagent browser delegation:", "Focused tasks (run_task):", "Affent plan tool guidance:", "Memory retrieval:", "Session history retrieval:", "Project context:", "External research:", "Workspace directory", "run_task", "subagent_run"} {
		if strings.Contains(msgs[0].Content, forbidden) {
			t.Fatalf("eval-mode system prompt should not include %q guidance:\n%s", forbidden, msgs[0].Content)
		}
	}
}

func TestSetupLoop_EvalModeAllowsIndividualToolsAndPromptMatchesRegistry(t *testing.T) {
	var cf commonFlags
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	cf.bind(fs)
	workspace := t.TempDir()
	if err := fs.Parse([]string{
		"--workspace", workspace,
		"--model", "fake-model",
		"--base-url", "http://127.0.0.1:1/v1",
		"--eval-mode",
		"--eval-tools=read_file,shell",
		"--quiet",
	}); err != nil {
		t.Fatal(err)
	}
	if err := applyConfig(&cf, fs); err != nil {
		t.Fatal(err)
	}
	b, code := setupLoop(cf)
	if code != 0 {
		t.Fatalf("setupLoop code=%d", code)
	}
	defer b.close()
	for _, name := range []string{"read_file", "shell"} {
		if _, ok := b.loop.Tools.Get(name); !ok {
			t.Fatalf("%s should be registered when requested by --eval-tools", name)
		}
	}
	for _, name := range []string{"write_file", "list_files", agent.MemoryToolName, agent.PlanToolName, agent.SessionSearchToolName, agent.SubagentToolName, agent.FocusedTaskToolName} {
		if _, ok := b.loop.Tools.Get(name); ok {
			t.Fatalf("%s should not be registered when absent from --eval-tools", name)
		}
	}
	msgs := b.loop.Conv.Snapshot()
	if len(msgs) == 0 {
		t.Fatal("system prompt missing")
	}
	if strings.Contains(msgs[0].Content, workspace) {
		t.Fatalf("eval-mode workspace prompt should not inject the absolute workspace path:\n%s", msgs[0].Content)
	}
	if strings.Contains(msgs[0].Content, "Use this exact path") {
		t.Fatalf("eval-mode workspace prompt should not steer agents toward absolute paths:\n%s", msgs[0].Content)
	}
	for _, want := range []string{"Commands and workspace tools start there by default", "prefer relative paths", "omit cwd"} {
		if !strings.Contains(msgs[0].Content, want) {
			t.Fatalf("eval-mode workspace prompt missing %q:\n%s", want, msgs[0].Content)
		}
	}
	for _, forbidden := range []string{"Memory retrieval:", "Session history retrieval:", "External research:", "Subagent delegation:", "Focused tasks (run_task):", "Affent plan tool guidance:", "write_file", "run_task"} {
		if strings.Contains(msgs[0].Content, forbidden) {
			t.Fatalf("eval-mode prompt should not include unregistered %q guidance:\n%s", forbidden, msgs[0].Content)
		}
	}
}

func TestSetupLoop_InjectsLoopProtocolWhenWorkspaceFileExists(t *testing.T) {
	workspace := t.TempDir()
	sessionID := "plan-loop"
	protocolPath := loopstate.ProtocolPath(workspace, sessionID)
	if err := os.MkdirAll(filepath.Dir(protocolPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(protocolPath, []byte(`# Loop

- loop_id: plan-loop
- status: running
- north_star: preserve durable loop protocol state.
`), 0o644); err != nil {
		t.Fatal(err)
	}
	convDir := filepath.Join(workspace, ".affentctl")
	if err := os.MkdirAll(convDir, 0o755); err != nil {
		t.Fatal(err)
	}
	planPath := filepath.Join(convDir, sessionID+".plan.json")
	if err := os.WriteFile(planPath, []byte(`{"steps":[{"text":"done","status":"completed"},{"text":"continue active loop evidence","status":"in_progress"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	var cf commonFlags
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	cf.bind(fs)
	if err := fs.Parse([]string{
		"--workspace", workspace,
		"--session-id", sessionID,
		"--model", "fake-model",
		"--base-url", "http://127.0.0.1:1/v1",
		"--eval-mode",
		"--eval-tools=plan",
		"--quiet",
	}); err != nil {
		t.Fatal(err)
	}
	if err := applyConfig(&cf, fs); err != nil {
		t.Fatal(err)
	}
	b, code := setupLoop(cf)
	if code != 0 {
		t.Fatalf("setupLoop code=%d", code)
	}
	defer b.close()
	if b.loop.SkillProvider == nil {
		t.Fatal("loop protocol file should install a skill provider even in eval mode")
	}
	if b.loop.LoopProtocolPath != protocolPath {
		t.Fatalf("LoopProtocolPath = %q, want %q", b.loop.LoopProtocolPath, protocolPath)
	}
	if len(b.loop.CompletionGuards) == 0 {
		t.Fatal("active LOOP.md should install a completion guard")
	}
	for _, label := range []string{"active_plan_unfinished", "loop_protocol_running"} {
		if !slices.Contains(b.loop.CompletionGuardLabels, label) {
			t.Fatalf("completion guard labels = %#v, want %s", b.loop.CompletionGuardLabels, label)
		}
	}
	var loopBlocked, planBlocked agent.CompletionGuardResult
	for _, guard := range b.loop.CompletionGuards {
		switch result := guard(); result.Trigger {
		case "loop_protocol_running":
			loopBlocked = result
		case "active_plan_unfinished":
			planBlocked = result
		}
	}
	if !loopBlocked.Blocked ||
		!strings.Contains(loopBlocked.RequiredAction, "loop_protocol action=close") {
		t.Fatalf("loop protocol completion guard = %+v", loopBlocked)
	}
	if !planBlocked.Blocked || !strings.Contains(planBlocked.Reason, "plan:1/2:active") {
		t.Fatalf("active plan completion guard = %+v", planBlocked)
	}
	if b.loopProtocolInitialized {
		t.Fatal("existing active LOOP.md must not mark the next turn as loop setup")
	}
	got := b.loop.SkillProvider("continue")
	for _, want := range []string{
		"AFFENT LOOP PROTOCOL:",
		"protocol_path=.affent/loops/plan-loop/LOOP.md",
		"north_star: preserve durable loop protocol state",
		"plan_label=plan:1/2:active",
		"plan_step_index=2",
		"plan_step_status=in_progress",
		"continue active loop evidence",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("loop protocol provider missing %q:\n%s", want, got)
		}
	}
}

func TestSetupLoop_DoesNotInjectDraftLoopProtocolWithoutState(t *testing.T) {
	workspace := t.TempDir()
	sessionID := "draft-loop"
	protocolPath := loopstate.ProtocolPath(workspace, sessionID)
	if err := os.MkdirAll(filepath.Dir(protocolPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(protocolPath, []byte(`# Loop Protocol

## 0. Metadata

- status: draft

## 1. North Star

Pending user calibration.
`), 0o644); err != nil {
		t.Fatal(err)
	}

	var cf commonFlags
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	cf.bind(fs)
	if err := fs.Parse([]string{
		"--workspace", workspace,
		"--session-id", sessionID,
		"--model", "fake-model",
		"--base-url", "http://127.0.0.1:1/v1",
		"--quiet",
	}); err != nil {
		t.Fatal(err)
	}
	if err := applyConfig(&cf, fs); err != nil {
		t.Fatal(err)
	}
	b, code := setupLoop(cf)
	if code != 0 {
		t.Fatalf("setupLoop code=%d", code)
	}
	defer b.close()
	if b.loop.LoopProtocolPath != protocolPath {
		t.Fatalf("LoopProtocolPath = %q, want draft path for loop_protocol tool access %q", b.loop.LoopProtocolPath, protocolPath)
	}
	if b.loopProtocolSkillInstalled {
		t.Fatal("draft LOOP.md without state must not install active loop protocol feeds")
	}
	if b.loopProtocolInitialized {
		t.Fatal("existing draft LOOP.md must not mark the next turn as loop setup")
	}
	if b.loop.SkillProvider != nil {
		if got := b.loop.SkillProvider("continue"); strings.Contains(got, "AFFENT LOOP PROTOCOL:") {
			t.Fatalf("draft LOOP.md without state was injected:\n%s", got)
		}
	}
}

func TestSetupLoop_InitializesLoopProtocolWhenFlagSet(t *testing.T) {
	workspace := t.TempDir()
	sessionID := "longrun-init"
	var cf commonFlags
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	cf.bind(fs)
	if err := fs.Parse([]string{
		"--workspace", workspace,
		"--session-id", sessionID,
		"--model", "fake-model",
		"--base-url", "http://127.0.0.1:1/v1",
		"--loop-protocol",
		"--quiet",
	}); err != nil {
		t.Fatal(err)
	}
	if err := applyConfig(&cf, fs); err != nil {
		t.Fatal(err)
	}
	cf.loopProtocolGoal = "Investigate a long-running Bittensor subnet and preserve evidence."
	b, code := setupLoop(cf)
	if code != 0 {
		t.Fatalf("setupLoop code=%d", code)
	}
	defer b.close()
	protocolPath := loopstate.ProtocolPath(workspace, sessionID)
	if b.loop.LoopProtocolPath != protocolPath {
		t.Fatalf("LoopProtocolPath = %q, want %q", b.loop.LoopProtocolPath, protocolPath)
	}
	if b.loopProtocolSkillInstalled {
		t.Fatal("draft protocol must not install active loop protocol feeds before agent supplementation")
	}
	if !b.loopProtocolInitialized {
		t.Fatal("fresh --loop-protocol setup should mark the next run turn as loop setup")
	}
	if _, ok := b.loop.Tools.Get(agent.LoopProtocolToolName); !ok {
		t.Fatal("loop_protocol tool should be available to supplement draft protocol")
	}
	content, found, err := loopstate.ReadProtocol(protocolPath)
	if err != nil || !found {
		t.Fatalf("ReadProtocol found=%v err=%v", found, err)
	}
	for _, want := range []string{
		"# Loop Protocol: longrun-init",
		"- loop_id: longrun-init",
		"Investigate a long-running Bittensor subnet and preserve evidence.",
		"North Star",
		"Self-Attack",
		"Operational stop conditions:",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("initialized protocol missing %q:\n%s", want, content)
		}
	}
	state, found, err := loopstate.ReadState(loopstate.StatePath(workspace, sessionID))
	if err != nil || !found {
		t.Fatalf("ReadState found=%v err=%v", found, err)
	}
	if state.Status != "draft" || state.LastEventType != "loop.protocol_init" || state.ProtocolUpdates != 1 || state.InitialGoalPreview != "Investigate a long-running Bittensor subnet and preserve evidence." {
		t.Fatalf("state = %+v", state)
	}
}

func TestSetupLoop_EvalModeAllowsSessionSearchWithoutWorkspaceBuiltins(t *testing.T) {
	var cf commonFlags
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	cf.bind(fs)
	if err := fs.Parse([]string{
		"--workspace", t.TempDir(),
		"--model", "fake-model",
		"--base-url", "http://127.0.0.1:1/v1",
		"--eval-mode",
		"--eval-tools=session_search",
		"--quiet",
	}); err != nil {
		t.Fatal(err)
	}
	if err := applyConfig(&cf, fs); err != nil {
		t.Fatal(err)
	}
	b, code := setupLoop(cf)
	if code != 0 {
		t.Fatalf("setupLoop code=%d", code)
	}
	defer b.close()
	if _, ok := b.loop.Tools.Get(agent.SessionSearchToolName); !ok {
		t.Fatal("session_search should be registered when requested by --eval-tools")
	}
	for _, name := range []string{"shell", "read_file", "write_file", "list_files", agent.MemoryToolName, agent.PlanToolName, agent.SubagentToolName, agent.FocusedTaskToolName} {
		if _, ok := b.loop.Tools.Get(name); ok {
			t.Fatalf("%s should not be registered with session_search-only --eval-tools", name)
		}
	}
	msgs := b.loop.Conv.Snapshot()
	if len(msgs) == 0 || !strings.Contains(msgs[0].Content, "Session history retrieval:") {
		t.Fatalf("session_search-only eval prompt should include session guidance:\n%+v", msgs)
	}
	for _, forbidden := range []string{"Workspace directory", "Memory retrieval:", "Affent plan tool guidance:", "Subagent delegation:", "Focused tasks (run_task):"} {
		if strings.Contains(msgs[0].Content, forbidden) {
			t.Fatalf("session_search-only eval prompt should not include %q guidance:\n%s", forbidden, msgs[0].Content)
		}
	}
}

func TestSetupLoop_EvalModeRecallGroupRegistersMemoryAndSessionSearch(t *testing.T) {
	var cf commonFlags
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	cf.bind(fs)
	if err := fs.Parse([]string{
		"--workspace", t.TempDir(),
		"--model", "fake-model",
		"--base-url", "http://127.0.0.1:1/v1",
		"--eval-mode",
		"--eval-tools=recall",
		"--quiet",
	}); err != nil {
		t.Fatal(err)
	}
	if err := applyConfig(&cf, fs); err != nil {
		t.Fatal(err)
	}
	b, code := setupLoop(cf)
	if code != 0 {
		t.Fatalf("setupLoop code=%d", code)
	}
	defer b.close()
	for _, name := range []string{agent.MemoryToolName, agent.SessionSearchToolName} {
		if _, ok := b.loop.Tools.Get(name); !ok {
			t.Fatalf("%s should be registered by --eval-tools=recall", name)
		}
	}
	for _, name := range []string{"shell", "read_file", "write_file", "list_files", agent.PlanToolName} {
		if _, ok := b.loop.Tools.Get(name); ok {
			t.Fatalf("%s should not be registered by recall-only --eval-tools", name)
		}
	}
	msgs := b.loop.Conv.Snapshot()
	if len(msgs) == 0 ||
		!strings.Contains(msgs[0].Content, "Memory retrieval:") ||
		!strings.Contains(msgs[0].Content, "Session history retrieval:") {
		t.Fatalf("recall eval prompt should include memory and session guidance:\n%+v", msgs)
	}
}

func TestSetupLoop_MemoryPromptGuidanceFollowsCapability(t *testing.T) {
	t.Run("enabled by default", func(t *testing.T) {
		var cf commonFlags
		fs := flag.NewFlagSet("test", flag.ContinueOnError)
		cf.bind(fs)
		if err := fs.Parse([]string{
			"--workspace", t.TempDir(),
			"--model", "fake-model",
			"--base-url", "http://127.0.0.1:1/v1",
			"--subagent=false",
			"--focused-tasks=false",
			"--quiet",
		}); err != nil {
			t.Fatal(err)
		}
		if err := applyConfig(&cf, fs); err != nil {
			t.Fatal(err)
		}
		b, code := setupLoop(cf)
		if code != 0 {
			t.Fatalf("setupLoop code=%d", code)
		}
		defer b.close()
		msgs := b.loop.Conv.Snapshot()
		if len(msgs) == 0 || !strings.Contains(msgs[0].Content, "Memory retrieval:") {
			t.Fatalf("memory-enabled prompt should include memory retrieval guidance: %+v", msgs)
		}
		if !strings.Contains(msgs[0].Content, "Session history retrieval:") {
			t.Fatalf("default prompt should include session history guidance when session_search is registered: %+v", msgs)
		}
	})

	t.Run("eval mode omits unless explicitly enabled", func(t *testing.T) {
		var cf commonFlags
		fs := flag.NewFlagSet("test", flag.ContinueOnError)
		cf.bind(fs)
		if err := fs.Parse([]string{
			"--workspace", t.TempDir(),
			"--model", "fake-model",
			"--base-url", "http://127.0.0.1:1/v1",
			"--eval-mode",
			"--quiet",
		}); err != nil {
			t.Fatal(err)
		}
		if err := applyConfig(&cf, fs); err != nil {
			t.Fatal(err)
		}
		b, code := setupLoop(cf)
		if code != 0 {
			t.Fatalf("setupLoop code=%d", code)
		}
		defer b.close()
		msgs := b.loop.Conv.Snapshot()
		if len(msgs) == 0 {
			t.Fatal("system prompt missing")
		}
		if strings.Contains(msgs[0].Content, "Memory retrieval:") {
			t.Fatalf("eval prompt should not include memory guidance when memory is disabled:\n%s", msgs[0].Content)
		}
		if strings.Contains(msgs[0].Content, "Session history retrieval:") {
			t.Fatalf("eval prompt should not include session history guidance when session_search is disabled:\n%s", msgs[0].Content)
		}
	})

	t.Run("eval mode includes explicit memory", func(t *testing.T) {
		var cf commonFlags
		fs := flag.NewFlagSet("test", flag.ContinueOnError)
		cf.bind(fs)
		if err := fs.Parse([]string{
			"--workspace", t.TempDir(),
			"--model", "fake-model",
			"--base-url", "http://127.0.0.1:1/v1",
			"--eval-mode",
			"--memory=true",
			"--quiet",
		}); err != nil {
			t.Fatal(err)
		}
		if err := applyConfig(&cf, fs); err != nil {
			t.Fatal(err)
		}
		b, code := setupLoop(cf)
		if code != 0 {
			t.Fatalf("setupLoop code=%d", code)
		}
		defer b.close()
		msgs := b.loop.Conv.Snapshot()
		if len(msgs) == 0 || !strings.Contains(msgs[0].Content, "Memory retrieval:") {
			t.Fatalf("explicit memory eval prompt should include memory guidance: %+v", msgs)
		}
	})
}

func TestSetupLoop_SkillProviderFiltersUnavailableToolSkills(t *testing.T) {
	var cf commonFlags
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	cf.bind(fs)
	if err := fs.Parse([]string{
		"--workspace", t.TempDir(),
		"--model", "fake-model",
		"--base-url", "http://127.0.0.1:1/v1",
		"--subagent=false",
		"--focused-tasks=false",
		"--quiet",
	}); err != nil {
		t.Fatal(err)
	}
	if err := applyConfig(&cf, fs); err != nil {
		t.Fatal(err)
	}
	b, code := setupLoop(cf)
	if code != 0 {
		t.Fatalf("setupLoop code=%d", code)
	}
	defer b.close()
	if b.loop.SkillProvider == nil {
		t.Fatal("default runtime should install a skill provider")
	}
	got := b.loop.SkillProvider("请通过浏览器访问 https://example.com 并提取信息")
	if strings.Contains(got, "web_snapshot_fact_extraction") || strings.Contains(got, "browser_navigate") {
		t.Fatalf("affentctl without browser tools must not inject browser skill:\n%s", got)
	}
	got = b.loop.SkillProvider("这个 Go 项目的测试失败，请修复代码并运行 go test")
	if !strings.Contains(got, "coding_repair_workflow") {
		t.Fatalf("tool filtering should not disable unrelated coding skills:\n%s", got)
	}
}

func TestSetupLoop_RuntimeSkillInstallUpdatesActiveProvider(t *testing.T) {
	var cf commonFlags
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	cf.bind(fs)
	workspace := t.TempDir()
	if err := fs.Parse([]string{
		"--workspace", workspace,
		"--model", "fake-model",
		"--base-url", "http://127.0.0.1:1/v1",
		"--subagent=false",
		"--quiet",
	}); err != nil {
		t.Fatal(err)
	}
	if err := applyConfig(&cf, fs); err != nil {
		t.Fatal(err)
	}
	b, code := setupLoop(cf)
	if code != 0 {
		t.Fatalf("setupLoop code=%d", code)
	}
	defer b.close()
	tool, ok := b.loop.Tools.Get(agent.SkillToolName)
	if !ok {
		t.Fatal("skill tool missing")
	}
	body := "AFFENT ACTIVE SKILL: runtime_demo\nUse the runtime demo workflow."
	args, err := json.Marshal(map[string]any{
		"action":   "install",
		"name":     "runtime_demo",
		"body":     body,
		"triggers": []string{"runtime demo"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tool.Execute(context.Background(), args); err != nil {
		t.Fatalf("skill install: %v", err)
	}
	if got := b.loop.SkillProvider("please use runtime demo"); !strings.Contains(got, body) {
		t.Fatalf("installed skill should be active without setupLoop restart, got %q", got)
	}
	if _, err := os.Stat(filepath.Join(workspace, ".affent", "skills", "runtime_demo", "SKILL.md")); err != nil {
		t.Fatalf("installed skill was not persisted: %v", err)
	}
}

func TestSetupLoop_SkillProviderInjectsActivePlan(t *testing.T) {
	var cf commonFlags
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	cf.bind(fs)
	workspace := t.TempDir()
	if err := fs.Parse([]string{
		"--workspace", workspace,
		"--model", "fake-model",
		"--base-url", "http://127.0.0.1:1/v1",
		"--subagent=false",
		"--quiet",
	}); err != nil {
		t.Fatal(err)
	}
	if err := applyConfig(&cf, fs); err != nil {
		t.Fatal(err)
	}
	b, code := setupLoop(cf)
	if code != 0 {
		t.Fatalf("setupLoop code=%d", code)
	}
	defer b.close()

	convDir := filepath.Join(workspace, ".affentctl")
	planPath := localSessionPlanPath(convDir, b.sessionID)
	if err := os.WriteFile(planPath, []byte(`{"version":1,"steps":[{"text":"resume implementation","status":"in_progress","evidence":["cmd/affentctl/common.go"]}]}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := b.loop.SkillProvider("continue")
	if !strings.Contains(got, "AFFENT ACTIVE PLAN:") || !strings.Contains(got, "resume implementation") {
		t.Fatalf("active plan should be injected, got %q", got)
	}
	if !strings.Contains(got, "cmd/affentctl/common.go") {
		t.Fatalf("active plan evidence missing, got %q", got)
	}
	if !slices.Contains(b.loop.CompletionGuardLabels, "active_plan_unfinished") {
		t.Fatalf("completion guard labels = %#v, want active_plan_unfinished", b.loop.CompletionGuardLabels)
	}
	var blocked agent.CompletionGuardResult
	for _, guard := range b.loop.CompletionGuards {
		if result := guard(); result.Trigger == "active_plan_unfinished" {
			blocked = result
			break
		}
	}
	if !blocked.Blocked ||
		!strings.Contains(blocked.Reason, "plan:0/1:active") ||
		!strings.Contains(blocked.Prompt, "AFFENT COMPLETION GUARD:") {
		t.Fatalf("active plan completion guard = %+v", blocked)
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
		"AFFENTCTL_CONFIG":             "/sentinel-config-XYZ123",
		"AFFENTCTL_WORKSPACE":          "/sentinel-workspace-XYZ123",
		"AFFENTCTL_BASE_URL":           "https://sentinel-base-XYZ123",
		"AFFENTCTL_API_KEY":            "sk-sentinel-XYZ123",
		"AFFENTCTL_MODEL":              "sentinel-model-XYZ123",
		"AFFENTCTL_MCP_CONFIG":         "/sentinel-mcp-XYZ123",
		"AFFENTCTL_EXECUTOR":           "docker:sentinel-XYZ123",
		"AFFENTCTL_EVAL_MODE":          "sentinel-eval-mode-XYZ123",
		"AFFENTCTL_EVAL_TOOLS":         "sentinel-eval-tools-XYZ123",
		"AFFENTCTL_EVAL_ALL_TOOLS":     "sentinel-eval-all-tools-XYZ123",
		"AFFENTCTL_SUBAGENT_MAX_DEPTH": "99",
		"AFFENTCTL_WEB":                "sentinel-web-XYZ123",
		"AFFENTCTL_WEB_SEARCH":         "sentinel-web-search-XYZ123",
		"AFFENTCTL_TEMPERATURE":        "sentinel-temperature-XYZ123",
		"AFFENTCTL_TOP_P":              "sentinel-top-p-XYZ123",
		"AFFENTCTL_MAX_TOKENS":         "sentinel-max-tokens-XYZ123",
		"AFFENTCTL_SEED":               "sentinel-seed-XYZ123",
	}
	for k, v := range planted {
		t.Setenv(k, v)
	}
	t.Setenv("AFFENTCTL_SUBAGENT", "false")

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
		} else if name == "workspace" {
			want = "./affent-workspace"
		} else if name == "eval-mode" || name == "eval-all-tools" {
			want = "false"
		} else if name == "subagent" {
			want = "true"
		} else if name == "subagent-max-depth" {
			want = "2"
		} else if name == "focused-tasks" {
			want = "true"
		} else if name == "web" || name == "web-search" || name == "browser" || name == "browser-screenshot" || name == "loop-protocol" {
			want = "false"
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

func TestWorkspaceFromEnv(t *testing.T) {
	t.Setenv("AFFENTCTL_WORKSPACE", "/tmp/affent-env-workspace")
	var cf commonFlags
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	cf.bind(fs)
	if err := applyConfig(&cf, fs); err != nil {
		t.Fatal(err)
	}
	if cf.workspace != "/tmp/affent-env-workspace" {
		t.Fatalf("workspace from env = %q", cf.workspace)
	}
}

func TestSandboxExecutorUsesPersistentWorkspaceDefault(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dir)
	var cf commonFlags
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	cf.bind(fs)
	if err := fs.Parse([]string{"--executor", "sandbox"}); err != nil {
		t.Fatal(err)
	}
	if err := applyConfig(&cf, fs); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, "affent", "sandbox", "workspace")
	if cf.workspace != want {
		t.Fatalf("sandbox default workspace = %q, want %q", cf.workspace, want)
	}
}

func TestSandboxExecutorKeepsExplicitWorkspace(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", filepath.Join(dir, "xdg"))
	explicit := filepath.Join(dir, "explicit")
	var cf commonFlags
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	cf.bind(fs)
	if err := fs.Parse([]string{"--executor", "sandbox", "--workspace", explicit}); err != nil {
		t.Fatal(err)
	}
	if err := applyConfig(&cf, fs); err != nil {
		t.Fatal(err)
	}
	if cf.workspace != explicit {
		t.Fatalf("explicit workspace = %q, want %q", cf.workspace, explicit)
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
	t.Run("rejects out-of-range sampling values before upstream call", func(t *testing.T) {
		cases := []struct {
			name        string
			temperature string
			topP        string
			maxTokens   string
			want        string
		}{
			{name: "temperature negative", temperature: "-0.1", want: "--temperature must be between 0 and 2"},
			{name: "temperature NaN", temperature: "NaN", want: "--temperature must be between 0 and 2"},
			{name: "temperature too high", temperature: "2.1", want: "--temperature must be between 0 and 2"},
			{name: "top-p negative", topP: "-0.1", want: "--top-p must be between 0 and 1"},
			{name: "top-p infinity", topP: "+Inf", want: "--top-p must be between 0 and 1"},
			{name: "top-p too high", topP: "1.1", want: "--top-p must be between 0 and 1"},
			{name: "max-tokens zero", maxTokens: "0", want: "--max-tokens must be a positive integer"},
			{name: "max-tokens negative", maxTokens: "-1", want: "--max-tokens must be a positive integer"},
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				_, err := parseSampling(c.temperature, c.topP, c.maxTokens, "")
				if err == nil || !strings.Contains(err.Error(), c.want) {
					t.Fatalf("error = %v, want contains %q", err, c.want)
				}
			})
		}
	})
}

func TestValidateLLMConfigRequiresAPIKeyOnlyForDefaultEndpoint(t *testing.T) {
	for _, c := range []struct {
		name string
		cfg  commonFlags
		want string
	}{
		{
			name: "missing model",
			cfg:  commonFlags{baseURL: "http://127.0.0.1:1/v1"},
			want: "--model",
		},
		{
			name: "default endpoint needs key",
			cfg:  commonFlags{model: "gpt-4o-mini"},
			want: "--api-key",
		},
		{
			name: "default endpoint with trailing slash needs key",
			cfg:  commonFlags{model: "gpt-4o-mini", baseURL: agent.DefaultBaseURL + "/"},
			want: "--api-key",
		},
		{
			name: "custom endpoint can be keyless",
			cfg:  commonFlags{model: "local-model", baseURL: "http://127.0.0.1:11434/v1"},
		},
		{
			name: "default endpoint with key",
			cfg:  commonFlags{model: "gpt-4o-mini", apiKey: "key"},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			err := validateLLMConfig(c.cfg)
			if c.want == "" {
				if err != nil {
					t.Fatalf("validateLLMConfig: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("error = %v, want contains %q", err, c.want)
			}
		})
	}
}

func TestResolveCompactionConfig(t *testing.T) {
	t.Run("non-positive values fall back to runtime defaults", func(t *testing.T) {
		trigger, keepLast := resolveCompactionConfig(0, -1)
		if trigger != agent.DefaultSummaryTriggerMsgs {
			t.Fatalf("trigger = %d, want default %d", trigger, agent.DefaultSummaryTriggerMsgs)
		}
		if keepLast != agent.DefaultSummaryKeepLast {
			t.Fatalf("keepLast = %d, want default %d", keepLast, agent.DefaultSummaryKeepLast)
		}
	})
	t.Run("positive values are honored", func(t *testing.T) {
		trigger, keepLast := resolveCompactionConfig(12, 3)
		if trigger != 12 || keepLast != 3 {
			t.Fatalf("trigger, keepLast = %d, %d; want 12, 3", trigger, keepLast)
		}
	})
}

func TestNormalizeRuntimeLimits(t *testing.T) {
	valid := func() commonFlags {
		return commonFlags{
			maxTurns:         10,
			callTimeout:      time.Second,
			retryTransient:   agent.DefaultTransientRetries,
			retryBackoff:     time.Second,
			subagentMaxDepth: agent.DefaultSubagentMaxDepth,
		}
	}

	t.Run("retry zero maps to loop disable sentinel", func(t *testing.T) {
		cf := valid()
		cf.retryTransient = 0
		if err := normalizeRuntimeLimits(&cf); err != nil {
			t.Fatal(err)
		}
		if cf.retryTransient != -1 {
			t.Fatalf("retryTransient = %d, want -1 loop disable sentinel", cf.retryTransient)
		}
	})

	cases := []struct {
		name string
		edit func(*commonFlags)
		want string
	}{
		{name: "max turns", edit: func(c *commonFlags) { c.maxTurns = 0 }, want: "--max-turns must be a positive integer"},
		{name: "max turn input tokens", edit: func(c *commonFlags) { c.maxTurnInputTokens = -1 }, want: "--max-turn-input-tokens must be zero or a positive integer"},
		{name: "call timeout", edit: func(c *commonFlags) { c.callTimeout = 0 }, want: "--max-call-timeout must be a positive duration"},
		{name: "negative retries", edit: func(c *commonFlags) { c.retryTransient = -1 }, want: "--retry-transient must be zero or a positive integer"},
		{name: "retry backoff", edit: func(c *commonFlags) { c.retryBackoff = 0 }, want: "--retry-backoff must be a positive duration"},
		{name: "subagent depth too low", edit: func(c *commonFlags) { c.subagentMaxDepth = 0 }, want: "--subagent-max-depth must be between 1 and 4"},
		{name: "subagent depth too high", edit: func(c *commonFlags) { c.subagentMaxDepth = agent.MaxSubagentDepth + 1 }, want: "--subagent-max-depth must be between 1 and 4"},
		{name: "web search without web", edit: func(c *commonFlags) { c.webSearchEnabled = true }, want: "--web-search requires --web"},
		{name: "browser screenshot without browser", edit: func(c *commonFlags) { c.browserScreenshot = true }, want: "--browser-screenshot requires --browser"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cf := valid()
			tc.edit(&cf)
			err := normalizeRuntimeLimits(&cf)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want contains %q", err, tc.want)
			}
		})
	}
}

func TestRetryTransientZeroDisablesRetriesFromCLI(t *testing.T) {
	var cf commonFlags
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	cf.bind(fs)
	if err := fs.Parse([]string{"--retry-transient=0"}); err != nil {
		t.Fatal(err)
	}
	if err := applyConfig(&cf, fs); err != nil {
		t.Fatal(err)
	}
	if cf.retryTransient != -1 {
		t.Fatalf("retryTransient = %d, want -1 loop disable sentinel", cf.retryTransient)
	}
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
			"max_chars": "4400,2750",
			"topic_max_chars": 9000,
			"max_topics": 64
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
	if cf.memoryTopicMaxChars != 9000 {
		t.Fatalf("topic_max_chars not loaded: %d", cf.memoryTopicMaxChars)
	}
	if cf.memoryMaxTopics != 64 {
		t.Fatalf("max_topics not loaded: %d", cf.memoryMaxTopics)
	}
}

func TestMemoryConfigLimitsRespectCLIOverride(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "affent.json")
	if err := os.WriteFile(cfgPath, []byte(`{
		"memory": {
			"topic_max_chars": 9000,
			"max_topics": 64
		}
	}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var cf commonFlags
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	cf.bind(fs)
	if err := fs.Parse([]string{
		"--config", cfgPath,
		"--memory-topic-max-chars", "12000",
		"--memory-max-topics", "128",
	}); err != nil {
		t.Fatal(err)
	}
	if err := applyConfig(&cf, fs); err != nil {
		t.Fatal(err)
	}
	if cf.memoryTopicMaxChars != 12000 {
		t.Fatalf("CLI memory-topic-max-chars should win over config, got %d", cf.memoryTopicMaxChars)
	}
	if cf.memoryMaxTopics != 128 {
		t.Fatalf("CLI memory-max-topics should win over config, got %d", cf.memoryMaxTopics)
	}
}

func TestMemoryLimitsRejectNegative(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "negative topic max chars",
			args: []string{"--memory-topic-max-chars", "-1"},
			want: "--memory-topic-max-chars must be zero or a positive integer",
		},
		{
			name: "negative max topics",
			args: []string{"--memory-max-topics", "-1"},
			want: "--memory-max-topics must be zero or a positive integer",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var cf commonFlags
			fs := flag.NewFlagSet("test", flag.ContinueOnError)
			cf.bind(fs)
			if err := fs.Parse(tc.args); err != nil {
				t.Fatal(err)
			}
			err := applyConfig(&cf, fs)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("applyConfig error = %v, want contains %q", err, tc.want)
			}
		})
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
