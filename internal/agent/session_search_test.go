package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeSessionLog drops a JSONL conversation file for one past
// session id under sessionsDir. Each message becomes one line.
func writeSessionLog(t *testing.T, sessionsDir, sid string, msgs ...ChatMessage) {
	t.Helper()
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(sessionsDir, sid+".jsonl")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetEscapeHTML(false)
	for _, m := range msgs {
		if err := enc.Encode(m); err != nil {
			t.Fatal(err)
		}
	}
}

// TestSessionSearchTool_QueryRequired pins the friendly error for
// empty query — same pattern memoryTool uses, so the model sees a
// helpful Message field instead of a raw decode failure.
func TestSessionSearchTool_QueryRequired(t *testing.T) {
	tool := sessionSearchTool(t.TempDir(), "current")
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"query":""}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "query is required") {
		t.Errorf("missing 'query is required' in %q", out)
	}
}

// TestSessionSearchTool_NoConfiguredDir pins the unconfigured path:
// when sessionsDir is empty, the tool returns a clear message
// rather than a stat error.
func TestSessionSearchTool_NoConfiguredDir(t *testing.T) {
	tool := sessionSearchTool("", "current")
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"query":"anything"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "not configured") {
		t.Errorf("missing 'not configured' in %q", out)
	}
}

// TestSessionSearchTool_BadArgs pins JSON-decode failure surfacing
// as an error (not silently swallowed).
func TestSessionSearchTool_BadArgs(t *testing.T) {
	tool := sessionSearchTool(t.TempDir(), "current")
	_, err := tool.Execute(context.Background(), json.RawMessage(`{not valid json`))
	if err == nil {
		t.Error("expected decode error for malformed JSON args")
	}
}

// TestSessionSearchTool_HitsAndCurrentSessionExcluded pins the two
// load-bearing behaviors: (a) search across other sessions finds
// matching transcript lines; (b) the CURRENT session is excluded
// so the agent doesn't match its own in-flight turns.
func TestSessionSearchTool_HitsAndCurrentSessionExcluded(t *testing.T) {
	dir := t.TempDir()

	// One past session with the keyword we're going to search for.
	writeSessionLog(t, dir, "past-deploy",
		ChatMessage{Role: "user", Content: "how do I deploy to fly.io?"},
		ChatMessage{Role: "assistant", Content: "use flyctl"},
	)
	// Current in-flight session ALSO contains the keyword — must be
	// excluded so the model doesn't 'remember' something it just said.
	writeSessionLog(t, dir, "current",
		ChatMessage{Role: "user", Content: "deploy fly.io reminder"},
	)

	tool := sessionSearchTool(dir, "current")
	out, err := tool.Execute(context.Background(), json.RawMessage(
		`{"query":"fly.io deploy"}`))
	if err != nil {
		t.Fatal(err)
	}

	var resp SessionSearchResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("decode response: %v\n%s", err, out)
	}
	if len(resp.Results) == 0 {
		t.Fatalf("expected hits from past-deploy session, got %+v", resp)
	}
	for _, h := range resp.Results {
		if h.SessionID == "current" {
			t.Errorf("current session must not appear in results: %+v", h)
		}
		if h.SessionID != "past-deploy" {
			t.Errorf("unexpected session in results: %+v", h)
		}
	}
}

// TestMarshalSessionSearchResp_EmptyResultsIsArray pins the
// JSON-shape detail clients depend on: Results must marshal to []
// (not null) when empty. A null breaks any client that does
// `for _, h := range resp.results` in JS/Python — those languages
// treat null and [] differently.
func TestMarshalSessionSearchResp_EmptyResultsIsArray(t *testing.T) {
	out := marshalSessionSearchResp(SessionSearchResponse{Query: "x"})
	if !strings.Contains(out, `"results":[]`) {
		t.Errorf("empty Results must marshal as []; got %s", out)
	}
}

// TestRegisterMemoryOnly_RegistersJustMemory pins the trivial but
// load-bearing shim that affentserve uses when EnableBuiltins is
// false but EnableMemory is true. Must register exactly one tool
// named "memory", no others.
func TestRegisterMemoryOnly_RegistersJustMemory(t *testing.T) {
	reg := NewRegistry()
	store := NewFileMemoryStore(t.TempDir())
	RegisterMemoryOnly(reg, store)

	defs := reg.Defs()
	if len(defs) != 1 {
		t.Fatalf("RegisterMemoryOnly should add exactly one tool; got %d: %+v", len(defs), defs)
	}
	if defs[0].Function.Name != "memory" {
		t.Errorf("the one tool must be 'memory'; got %q", defs[0].Function.Name)
	}
}
