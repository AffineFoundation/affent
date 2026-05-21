package e2e

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	agent "github.com/affinefoundation/affent/internal/agent"
	"github.com/affinefoundation/affent/sse"
)

// mockLLM serves OpenAI-compat /chat/completions with a scripted
// sequence of responses. Each request consumes one script entry; the
// raw request body is captured so tests can inspect what the Loop
// actually sent.
type mockLLM struct {
	script   [][]string // each turn is a list of SSE data: lines
	captured [][]byte
	calls    int32
}

func newMockLLM(t *testing.T, script [][]string) *httptest.Server {
	t.Helper()
	m := &mockLLM{script: script}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		idx := atomic.AddInt32(&m.calls, 1) - 1
		body, _ := io.ReadAll(r.Body)
		m.captured = append(m.captured, body)

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		fl := w.(http.Flusher)
		if int(idx) >= len(m.script) {
			// No more scripted responses; emit a generic stop.
			w.Write([]byte("data: {\"choices\":[{\"delta\":{\"role\":\"assistant\",\"content\":\"done\"},\"finish_reason\":\"stop\"}]}\n\n"))
			fl.Flush()
			return
		}
		for _, line := range m.script[idx] {
			w.Write([]byte("data: " + line + "\n\n"))
			fl.Flush()
		}
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { srv.CloseClientConnections() })
	t.Cleanup(func() {
		t.Logf("mock LLM received %d call(s)", atomic.LoadInt32(&m.calls))
	})
	mockServers[srv] = m
	return srv
}

var mockServers = map[*httptest.Server]*mockLLM{}

func mockFor(srv *httptest.Server) *mockLLM { return mockServers[srv] }

// drainTurnEnd drains the SSE event channel until it sees turn.end or
// the context fires. Returns the turn-end reason.
func drainTurnEnd(t *testing.T, events <-chan sse.Event, timeout time.Duration) string {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				t.Fatalf("event channel closed before turn.end")
			}
			if ev.Type == sse.TypeTurnEnd {
				var p sse.TurnEndPayload
				_ = json.Unmarshal(ev.Data, &p)
				return p.Reason
			}
		case <-deadline:
			t.Fatalf("timeout waiting for turn.end")
		}
	}
}

// TestE2E_MemoryAddPersistsAcrossSessions drives a full Loop with a
// mock OpenAI-compat server. The model "calls" the memory tool to add
// a fact; the test verifies (a) the system prompt sent on the LLM
// request contains the (initially empty) memory snapshot scaffold, (b)
// MEMORY.md on disk contains the new fact, and (c) a fresh Loop
// against the same store sees the fact in its outbound system prompt.
func TestE2E_MemoryAddPersistsAcrossSessions(t *testing.T) {
	dir := t.TempDir()
	workspaceDir := filepath.Join(dir, "ws")
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Script: turn 1 emits a tool_call for memory.add, then turn 2
	// emits a plain "ok" final answer after the tool result is in.
	turn1 := []string{
		`{"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"memory","arguments":""}}]},"finish_reason":null}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"action\":\"add\",\"target\":\"memory\",\"content\":\"User prefers Go 1.22 + sqlc\"}"}}]},"finish_reason":null}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`[DONE]`,
	}
	turn2 := []string{
		`{"choices":[{"delta":{"role":"assistant","content":"Saved your preference."},"finish_reason":"stop"}]}`,
		`[DONE]`,
	}
	srv := newMockLLM(t, [][]string{turn1, turn2})

	convPath := filepath.Join(workspaceDir, ".affentctl", "sess.jsonl")
	conv, err := agent.OpenConversationAt(convPath)
	if err != nil {
		t.Fatal(err)
	}

	memStore := agent.NewFileMemoryStore(workspaceDir)
	// USER store off — keep the test focused on the workspace path.
	memStore.UserPath = ""

	reg := agent.NewRegistry()
	// Don't need shell / files for this test; just register memory.
	agent.RegisterMemoryOnly(reg, memStore)

	events := make(chan sse.Event, 256)
	llm := agent.NewLLMClient(srv.URL, "", "fake-model")
	loop := &agent.Loop{
		LLM:                 llm,
		Tools:               reg,
		Conv:                conv,
		Events:              events,
		Memory:              memStore,
		MaxTurnSteps:        4,
		PerCallTimeout:      5 * time.Second,
		MaxTransientRetries: 0,
	}
	if err := loop.EnsureSystemPrompt("base prompt"); err != nil {
		t.Fatal(err)
	}

	// Drain events in the background so the Loop can publish freely.
	done := make(chan string, 1)
	go func() { done <- drainTurnEnd(t, events, 10*time.Second) }()

	if _, err := loop.SendUser(context.Background(), "remember that I use Go 1.22"); err != nil {
		t.Fatalf("SendUser: %v", err)
	}
	reason := <-done
	if reason != sse.TurnEndCompleted {
		t.Fatalf("turn ended with reason=%q; expected completed", reason)
	}

	// Verify the new bucket layout: default topic lives at
	// .affent/memory/topics/general.md (was .affent/MEMORY.md pre-v2).
	memPath := filepath.Join(workspaceDir, ".affent", "memory", "topics", "general.md")
	raw, err := os.ReadFile(memPath)
	if err != nil {
		t.Fatalf("general topic file missing: %v", err)
	}
	if !strings.Contains(string(raw), "Go 1.22 + sqlc") {
		t.Fatalf("general topic does not contain the added fact:\n%s", raw)
	}

	// Verify the system prompt sent on turn 1 contained the (empty)
	// memory scaffold composition (just "base prompt" since no facts
	// were on disk yet).
	mock := mockFor(srv)
	if len(mock.captured) < 1 {
		t.Fatal("no LLM requests captured")
	}
	firstReq := decodeReq(t, mock.captured[0])
	if firstReq.Messages[0].Role != "system" {
		t.Fatalf("first message was not system: %+v", firstReq.Messages[0])
	}
	if firstReq.Messages[0].Content != "base prompt" {
		t.Fatalf("first-turn system prompt should be exactly 'base prompt' (no memory yet), got %q",
			firstReq.Messages[0].Content)
	}

	// Now simulate a fresh session against the same store: new Conv,
	// new Loop, new EnsureSystemPrompt. The mock has run out of script
	// for turn 1 of this new session, but EnsureSystemPrompt does not
	// hit the LLM, so we don't need to issue a SendUser. We just check
	// the conversation log's system message.
	conv2Path := filepath.Join(workspaceDir, ".affentctl", "sess2.jsonl")
	conv2, err := agent.OpenConversationAt(conv2Path)
	if err != nil {
		t.Fatal(err)
	}
	loop2 := &agent.Loop{Conv: conv2, Memory: memStore}
	if err := loop2.EnsureSystemPrompt("base prompt"); err != nil {
		t.Fatal(err)
	}
	sys := conv2.Snapshot()[0].Content
	if !strings.Contains(sys, "Go 1.22 + sqlc") {
		t.Fatalf("second session's system prompt missing prior memory:\n%s", sys)
	}
	if !strings.HasPrefix(sys, "base prompt") {
		t.Fatalf("second session's system prompt should start with base prompt, got:\n%s", sys)
	}
}

// chatReqShape is a minimal decoder for the outbound request payload.
type chatReqShape struct {
	Model    string `json:"model"`
	Messages []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"messages"`
}

func decodeReq(t *testing.T, raw []byte) chatReqShape {
	t.Helper()
	var r chatReqShape
	if err := json.Unmarshal(raw, &r); err != nil {
		t.Fatalf("decode req: %v body=%s", err, raw)
	}
	return r
}

// TestE2E_MemoryOnlyRejectsShell verifies RegisterMemoryOnly
// actually limits the registry to the memory surface — no shell, no
// file tools available, even if the embedder calls
// RegisterBuiltins logic by mistake elsewhere.
func TestE2E_MemoryOnlyRejectsShell(t *testing.T) {
	memStore := agent.NewFileMemoryStore(t.TempDir())
	reg := agent.NewRegistry()
	agent.RegisterMemoryOnly(reg, memStore)

	names := []string{}
	for _, d := range reg.Defs() {
		names = append(names, d.Function.Name)
	}
	if len(names) != 1 || names[0] != "memory" {
		t.Fatalf("expected exactly {memory}, got %v", names)
	}
}

// TestE2E_SessionSearchIntegratesWithRegistry verifies session_search
// is wired through RegisterBuiltins when SessionsDir is set.
func TestE2E_SessionSearchIntegratesWithRegistry(t *testing.T) {
	dir := t.TempDir()
	convDir := filepath.Join(dir, ".affentctl")
	if err := os.MkdirAll(convDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Write a prior session's log.
	writeE2ESessionLog(t, convDir, "prev", []agent.ChatMessage{
		{Role: "user", Content: "we set up prometheus on port 9090"},
		{Role: "assistant", Content: "yes, scrape config under /etc/prometheus"},
	})

	reg := agent.NewRegistry()
	agent.RegisterBuiltins(reg, agent.BuiltinDeps{
		HostWorkspaceDir: dir,
		SessionsDir:      convDir,
		SessionID:        "current",
	})

	tool, ok := reg.Get("session_search")
	if !ok {
		t.Fatal("session_search should be registered when SessionsDir is set")
	}
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"query":"prometheus port"}`))
	if err != nil {
		t.Fatal(err)
	}
	var resp agent.SessionSearchResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("response: %v body=%s", err, out)
	}
	if resp.Total == 0 {
		t.Fatalf("session_search should have found prior 'prometheus' message, got %+v", resp)
	}
	if resp.Results[0].SessionID != "prev" {
		t.Fatalf("expected hit in 'prev' session, got %s", resp.Results[0].SessionID)
	}
}

// writeE2ESessionLog is the test helper used by the memory + session-
// search end-to-end test. Lived in session_search_test.go before the
// internal/sessionsearch refactor; kept inline here so the e2e test
// stays self-contained.
func writeE2ESessionLog(t *testing.T, dir, sessionID string, msgs []agent.ChatMessage) {
	t.Helper()
	path := filepath.Join(dir, sessionID+".jsonl")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, m := range msgs {
		if err := enc.Encode(m); err != nil {
			t.Fatal(err)
		}
	}
}
