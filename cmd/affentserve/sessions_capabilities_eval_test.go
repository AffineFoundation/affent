package main

import (
	"io"
	"reflect"
	"strings"
	"testing"

	agent "github.com/affinefoundation/affent/internal/agent"
	"github.com/rs/zerolog"
)

func TestSessionCapabilitiesReportsEvalMode(t *testing.T) {
	cfg := Config{
		Listen:             "127.0.0.1:0",
		MaxSessions:        4,
		SessionIdleTTL:     "5m",
		WorkspaceRoot:      t.TempDir(),
		BaseURL:            "http://127.0.0.1:0",
		APIKey:             "test",
		Model:              "fake",
		EvalMode:           true,
		EvalTools:          "read_file,shell",
		EnableSubagent:     true,
		EnableFocusedTasks: true,
	}
	pool, err := NewSessionPool(cfg, zerolog.New(io.Discard))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Shutdown)

	s, err := pool.GetOrCreate("eval-basic")
	if err != nil {
		t.Fatal(err)
	}
	caps := summarizeActiveCapabilities(s, pool.cfg)
	if !caps.EvalMode {
		t.Fatal("active session capabilities should report eval_mode=true")
	}
	if caps.EvalTools != "read_file,shell" || caps.EvalAllTools {
		t.Fatalf("active session capabilities should report eval allowlist, got %+v", caps)
	}
	if !reflect.DeepEqual(caps.WorkspaceTools, []string{"shell", "read_file"}) {
		t.Fatalf("active session capabilities should report partial workspace tools, got %+v", caps.WorkspaceTools)
	}
	if caps.Builtins || caps.Browser {
		t.Fatalf("strict eval mode should not report complete builtins or browser for a partial allowlist, got %+v", caps)
	}
	for _, name := range []string{"read_file", "shell"} {
		if _, ok := s.registry.Get(name); !ok {
			t.Fatalf("test sanity: eval allowlist should register %s", name)
		}
	}
	if caps.SkillInstall || caps.Plan || caps.Subagent || caps.FocusedTasks {
		t.Fatalf("eval mode should report workflow tools disabled, got %+v", caps)
	}
	if _, ok := s.registry.Get(agent.SubagentToolName); ok {
		t.Fatal("test sanity: eval mode should not register subagent")
	}
	msgs := s.conv.Snapshot()
	if len(msgs) == 0 {
		t.Fatal("system prompt missing")
	}
	for _, forbidden := range []string{"Subagent delegation:", "Focused tasks (run_task):", "Affent plan tool guidance:", "Memory retrieval:", "Session history retrieval:"} {
		if strings.Contains(msgs[0].Content, forbidden) {
			t.Fatalf("eval-mode system prompt should not include %q guidance:\n%s", forbidden, msgs[0].Content)
		}
	}

	reg := agent.NewRegistry()
	for _, name := range []string{"shell", "read_file", "write_file", "edit_file", "list_files", "browser_navigate"} {
		reg.Add(&agent.Tool{Name: name})
	}
	explicit := summarizeActiveCapabilities(&Session{registry: reg}, Config{EvalMode: true})
	if !explicit.EvalMode || !explicit.Builtins || !explicit.Browser {
		t.Fatalf("capabilities should report explicitly registered eval permissions, got %+v", explicit)
	}
	if explicit.SkillInstall || explicit.Plan || explicit.Subagent || explicit.FocusedTasks {
		t.Fatalf("synthetic browser-only eval surface should not report workflow tools, got %+v", explicit)
	}
}
