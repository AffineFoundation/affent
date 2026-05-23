package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/affinefoundation/affent/internal/memory"
	"github.com/affinefoundation/affent/internal/sessionsearch"
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
	if !strings.Contains(out, "Next:") {
		t.Errorf("query-required response should include a corrective Next step: %q", out)
	}
}

func TestSessionSearchToolSchemaPublishesQueryLimit(t *testing.T) {
	tool := sessionSearchTool(t.TempDir(), "current")
	var schema struct {
		AdditionalProperties *bool `json:"additionalProperties"`
		Properties           map[string]struct {
			MinLength int `json:"minLength"`
			MaxLength int `json:"maxLength"`
			Default   int `json:"default"`
			Maximum   int `json:"maximum"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(tool.Schema, &schema); err != nil {
		t.Fatal(err)
	}
	if schema.AdditionalProperties == nil {
		t.Fatal("session_search schema missing additionalProperties")
	}
	if *schema.AdditionalProperties {
		t.Fatal("session_search schema allows unknown arguments")
	}
	if schema.Properties["query"].MinLength != 1 {
		t.Fatalf("query minLength = %d, want 1", schema.Properties["query"].MinLength)
	}
	if schema.Properties["query"].MaxLength != sessionsearch.MaxQueryBytes {
		t.Fatalf("query maxLength = %d, want %d", schema.Properties["query"].MaxLength, sessionsearch.MaxQueryBytes)
	}
	if schema.Properties["top_k"].Default != sessionsearch.DefaultTopK {
		t.Fatalf("top_k default = %d, want %d", schema.Properties["top_k"].Default, sessionsearch.DefaultTopK)
	}
	if schema.Properties["top_k"].Maximum != sessionsearch.MaxTopK {
		t.Fatalf("top_k maximum = %d, want %d", schema.Properties["top_k"].Maximum, sessionsearch.MaxTopK)
	}
	if schema.Properties["max_per_session"].Default != sessionsearch.DefaultMaxPerSession {
		t.Fatalf("max_per_session default = %d, want %d", schema.Properties["max_per_session"].Default, sessionsearch.DefaultMaxPerSession)
	}
	if schema.Properties["max_per_session"].Maximum != sessionsearch.MaxPerSession {
		t.Fatalf("max_per_session maximum = %d, want %d", schema.Properties["max_per_session"].Maximum, sessionsearch.MaxPerSession)
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

func TestSessionSearchTool_NoResultsSuggestsRetryShape(t *testing.T) {
	tool := sessionSearchTool(t.TempDir(), "current")
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"query":"nothing matches this"}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"no results", "Next:", "different keywords", "outcome words"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
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

func TestSessionSearchToolRejectsUnknownArgs(t *testing.T) {
	tool := sessionSearchTool(t.TempDir(), "current")
	_, err := tool.Execute(context.Background(), json.RawMessage(`{"query":"deploy","session_id":"past"}`))
	if err == nil || !strings.Contains(err.Error(), `unknown field "session_id"`) {
		t.Fatalf("error = %v, want unknown field", err)
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

func TestSessionSearchToolCapsRunawayLimits(t *testing.T) {
	dir := t.TempDir()
	var msgs []ChatMessage
	for i := 0; i < sessionsearchMaxPerSessionForTest()+2; i++ {
		msgs = append(msgs, ChatMessage{Role: "user", Content: "needle session recall"})
	}
	writeSessionLog(t, dir, "past", msgs...)

	tool := sessionSearchTool(dir, "current")
	out, err := tool.Execute(context.Background(), json.RawMessage(
		`{"query":"needle","top_k":999999,"max_per_session":999999}`))
	if err != nil {
		t.Fatal(err)
	}
	var resp SessionSearchResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("decode response: %v\n%s", err, out)
	}
	if len(resp.Results) != sessionsearchMaxPerSessionForTest() {
		t.Fatalf("runaway limits returned %d results, want %d", len(resp.Results), sessionsearchMaxPerSessionForTest())
	}
}

func sessionsearchMaxPerSessionForTest() int { return sessionsearch.MaxPerSession }

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
	store := memory.NewFileMemoryStore(t.TempDir())
	RegisterMemoryOnly(reg, store)

	defs := reg.Defs()
	if len(defs) != 1 {
		t.Fatalf("RegisterMemoryOnly should add exactly one tool; got %d: %+v", len(defs), defs)
	}
	if defs[0].Function.Name != "memory" {
		t.Errorf("the one tool must be 'memory'; got %q", defs[0].Function.Name)
	}
}
