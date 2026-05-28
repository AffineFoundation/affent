package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/affinefoundation/affent/internal/loopstate"
	"github.com/affinefoundation/affent/internal/memory"
	"github.com/affinefoundation/affent/internal/sessionsearch"
	"github.com/affinefoundation/affent/internal/sse"
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

func writeDurableSessionEvents(t *testing.T, sessionsDir, sid string, events ...sse.Event) {
	t.Helper()
	dir := filepath.Join(sessionsDir, sid)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "events.jsonl")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetEscapeHTML(false)
	for _, ev := range events {
		if err := enc.Encode(ev); err != nil {
			t.Fatal(err)
		}
	}
}

func mustSSEEvent(t *testing.T, eventType string, payload any) sse.Event {
	t.Helper()
	ev, err := sse.NewEvent(eventType, payload)
	if err != nil {
		t.Fatal(err)
	}
	return ev
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

func TestWithSessionSearchSystemGuidance_AppendsOnce(t *testing.T) {
	base := "be helpful"
	once := WithSessionSearchSystemGuidance(base)
	for _, want := range []string{"Session history retrieval:", "2-6 concrete keywords", "recent_sessions", "recovery previews", "session id", "logical turn_idx", "JSONL message_idx", "untrusted evidence"} {
		if !strings.Contains(once, want) {
			t.Fatalf("session search guidance missing %q:\n%s", want, once)
		}
	}
	if twice := WithSessionSearchSystemGuidance(once); twice != once {
		t.Fatal("session search guidance should be idempotent")
	}
	if got := WithSessionSearchSystemGuidance(""); !strings.Contains(got, DefaultSystemPrompt) || !strings.Contains(got, "Session history retrieval:") {
		t.Fatalf("empty prompt should fall back to default + session search guidance:\n%s", got)
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
	for _, want := range []string{"no results", "Next:", "different keywords", "outcome words", "recent_sessions"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
}

func TestSessionSearchTool_NoResultsIncludesRecentSessionAnchors(t *testing.T) {
	dir := t.TempDir()
	writeSessionLog(t, dir, "past-stock",
		ChatMessage{Role: "user", Content: "Analyze Alpha Coast stock recovery"},
		ChatMessage{Role: "assistant", Content: "final marker HIST-STOCK-44"},
	)
	writeSessionLog(t, dir, "current",
		ChatMessage{Role: "user", Content: "current task should not appear"},
	)

	tool := sessionSearchTool(dir, "current")
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"query":"unmatched needle"}`))
	if err != nil {
		t.Fatal(err)
	}
	var resp SessionSearchResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("decode response: %v\n%s", err, out)
	}
	if len(resp.Results) != 0 || resp.Total != 0 {
		t.Fatalf("expected no direct hits, got %+v", resp)
	}
	if len(resp.RecentSessions) != 1 {
		t.Fatalf("expected one recent session anchor, got %+v", resp.RecentSessions)
	}
	if resp.RecentSessions[0].SessionID != "past-stock" {
		t.Fatalf("unexpected recent session: %+v", resp.RecentSessions[0])
	}
	if !strings.Contains(resp.RecentSessions[0].LatestUser, "Alpha Coast") || !strings.Contains(resp.RecentSessions[0].LatestAssistant, "HIST-STOCK-44") {
		t.Fatalf("recent session should include compact user/assistant previews: %+v", resp.RecentSessions[0])
	}
	if strings.Contains(out, "current task") {
		t.Fatalf("current session leaked into recent anchors:\n%s", out)
	}
}

func TestSessionSearchTool_NoResultsIncludesRecoveryAnchors(t *testing.T) {
	dir := t.TempDir()
	writeDurableSessionEvents(t, dir, "past-recovery",
		mustSSEEvent(t, sse.TypeLoopDecision, sse.LoopDecisionPayload{
			Kind:           "evidence_quality",
			Decision:       "defer",
			RequiredAction: "read browser_network_read ref n7 before citing market cap",
		}),
	)

	tool := sessionSearchTool(dir, "current")
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"query":"unmatched needle"}`))
	if err != nil {
		t.Fatal(err)
	}
	var resp SessionSearchResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("decode response: %v\n%s", err, out)
	}
	if len(resp.RecentSessions) != 1 || resp.RecentSessions[0].SessionID != "past-recovery" {
		t.Fatalf("expected recovery recent session anchor, got %+v", resp.RecentSessions)
	}
	if !strings.Contains(resp.RecentSessions[0].Recovery, "browser_network_read ref n7") {
		t.Fatalf("recent session should include recovery preview: %+v", resp.RecentSessions[0])
	}
	if !strings.Contains(out, `"recovery"`) {
		t.Fatalf("raw response should expose recovery anchor:\n%s", out)
	}
}

func TestSessionSearchTool_SearchesLoopStateRuntimeAnchors(t *testing.T) {
	dir := t.TempDir()
	sessionID := "past-loop"
	protocolPath := loopstate.ProtocolPath(filepath.Join(dir, sessionID), sessionID)
	if err := loopstate.WriteProtocol(protocolPath, `# Loop Protocol

## 1. North Star

Keep the long-running task recoverable.`); err != nil {
		t.Fatalf("WriteProtocol: %v", err)
	}
	if err := loopstate.WriteState(filepath.Join(filepath.Dir(protocolPath), loopstate.StateFileName), loopstate.State{
		Version:                1,
		LoopID:                 sessionID,
		OwnerSession:           sessionID,
		Status:                 "running",
		LastTurnID:             "turn_42",
		LastTurnEndReason:      sse.TurnEndMaxTurns,
		LastTurnMemorySearches: 2,
		LastTurnMemoryMisses:   1,
		LastMemoryUpdateAction: "replace",
		LastMemoryUpdateLoc:    "memory:markets",
		LastMemoryUpdate:       "prefer browser network evidence for dashboard values",
		LastDecisionKind:       "evidence_quality",
		LastDecisionTrigger:    "source_access_dynamic_partial",
		LastDecision:           "defer",
		LastDecisionAction:     "read browser_network_read ref n7 before citing dashboard values",
		LastCompactionReason:   "context_overflow",
		LastCompactionReactive: true,
		ContextCompactions:     1,
		LastCalibrationAnswer:  "Pause if dashboard evidence cannot be verified.",
		LastEventType:          "loop.protocol_update",
		LastEventSummary:       "Updated LOOP.md after evidence-quality rule change",
	}); err != nil {
		t.Fatalf("WriteState: %v", err)
	}

	tool := sessionSearchTool(dir, "current")
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"query":"protocol update evidence-quality"}`))
	if err != nil {
		t.Fatal(err)
	}
	var resp SessionSearchResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("decode response: %v\n%s", err, out)
	}
	if resp.Total != 1 || len(resp.Results) != 1 {
		t.Fatalf("expected one loop-state hit, got %+v", resp)
	}
	hit := resp.Results[0]
	if hit.SessionID != sessionID || hit.Role != "loop" {
		t.Fatalf("unexpected loop-state hit: %+v", hit)
	}
	for _, want := range []string{"loop.protocol_update", "evidence-quality"} {
		if !strings.Contains(hit.Snippet, want) {
			t.Fatalf("loop-state hit missing %q:\n%+v\nraw=%s", want, hit, out)
		}
	}
}

func TestSessionSearchTool_RetryWithRecentSessionIDFindsAnchor(t *testing.T) {
	dir := t.TempDir()
	writeSessionLog(t, dir, "past-stock",
		ChatMessage{Role: "user", Content: "Review the latest risk packet"},
		ChatMessage{Role: "assistant", Content: "final marker HIST-STOCK-44"},
	)

	tool := sessionSearchTool(dir, "current")
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"query":"past-stock"}`))
	if err != nil {
		t.Fatal(err)
	}
	var resp SessionSearchResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("decode response: %v\n%s", err, out)
	}
	if resp.Total != 1 || len(resp.Results) != 1 {
		t.Fatalf("expected one session-id retry hit, got %+v", resp)
	}
	if resp.Results[0].SessionID != "past-stock" ||
		!strings.Contains(resp.Results[0].Snippet, "HIST-STOCK-44") ||
		len(resp.RecentSessions) != 0 ||
		resp.Message != "" {
		t.Fatalf("unexpected session-id retry response: %+v", resp)
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
	if err == nil || !strings.Contains(err.Error(), "Next:") || !strings.Contains(err.Error(), "query") {
		t.Fatalf("decode error should include corrective fields: %v", err)
	}
}

func TestSessionSearchToolRejectsUnknownArgs(t *testing.T) {
	tool := sessionSearchTool(t.TempDir(), "current")
	_, err := tool.Execute(context.Background(), json.RawMessage(`{"query":"deploy","session_id":"past"}`))
	if err == nil || !strings.Contains(err.Error(), `unknown field "session_id"`) {
		t.Fatalf("error = %v, want unknown field", err)
	}
	for _, want := range []string{"Failure: kind=invalid_args", "Next:", "query", "top_k", "max_per_session", "Do not pass session_id"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error missing %q:\n%s", want, err.Error())
		}
	}
}

func TestSessionSearchToolDecodeTypeErrorNamesValidFields(t *testing.T) {
	tool := sessionSearchTool(t.TempDir(), "current")
	_, err := tool.Execute(context.Background(), json.RawMessage(`{"query":"deploy","top_k":"many"}`))
	if err == nil {
		t.Fatal("expected decode error")
	}
	for _, want := range []string{"decode args", "Failure: kind=invalid_args", "Next:", "query", "top_k", "max_per_session"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error missing %q:\n%s", want, err.Error())
		}
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
