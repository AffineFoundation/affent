package agent

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/affinefoundation/affent/internal/sse"
)

// fakeBigResultTool returns a result whose length exceeds
// MaxToolResultPreviewInEvent (4 KiB) so the tool.result event must
// truncate ResultSummary while keeping Result intact.
func fakeBigResultTool(payload string) *Tool {
	return &Tool{
		Name:        "big",
		Description: "test tool that returns oversized payload",
		Schema:      json.RawMessage(`{"type":"object","properties":{}}`),
		Execute: func(ctx context.Context, args json.RawMessage) (string, error) {
			return payload, nil
		},
	}
}

// TestToolResult_FullResultBypasses4KiBSummaryCap verifies the fix for
// the SSE-trace fidelity bug: when a tool's output exceeds
// MaxToolResultPreviewInEvent, the SSE event's ResultSummary may be
// truncated but the Result field carries the complete output.
func TestToolResult_FullResultBypasses4KiBSummaryCap(t *testing.T) {
	const payloadLen = 10 * 1024 // 10 KiB
	if payloadLen <= MaxToolResultPreviewInEvent {
		t.Fatalf("test premise broken: payload %d not above preview cap %d",
			payloadLen, MaxToolResultPreviewInEvent)
	}
	payload := strings.Repeat("X", payloadLen)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls := atomic.AddInt32(new(int32), 0)
		_ = calls
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		fl := w.(http.Flusher)
		// Turn 1: emit a tool_call for the "big" tool, then finish.
		// Turn 2 (after tool result lands): emit a final assistant message.
		body, _ := readReqBody(r)
		if !strings.Contains(body, `"tool"`) || !strings.Contains(body, `"role":"tool"`) {
			// Turn 1 path: model issues the tool_call.
			lines := []string{
				`data: {"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"c1","type":"function","function":{"name":"big","arguments":""}}]},"finish_reason":null}]}`,
				`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{}"}}]},"finish_reason":null}]}`,
				`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
				`data: [DONE]`,
			}
			for _, l := range lines {
				w.Write([]byte(l + "\n\n"))
				fl.Flush()
			}
			return
		}
		// Turn 2 path: model gives final answer.
		w.Write([]byte("data: {\"choices\":[{\"delta\":{\"role\":\"assistant\",\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\n"))
		w.Write([]byte("data: [DONE]\n\n"))
		fl.Flush()
	}))
	t.Cleanup(srv.Close)

	conv, err := OpenConversationAt(filepath.Join(t.TempDir(), "sess.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	reg := NewRegistry()
	reg.Add(fakeBigResultTool(payload))

	events := make(chan sse.Event, 256)
	llm := NewLLMClient(srv.URL, "", "fake-model")
	loop := &Loop{
		LLM: llm, Tools: reg, Conv: conv, Events: events,
		MaxTurnSteps: 4, PerCallTimeout: 5 * time.Second,
	}
	if err := loop.EnsureSystemPrompt("base"); err != nil {
		t.Fatal(err)
	}
	if _, err := loop.SendUser(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}

	deadline := time.After(10 * time.Second)
	var sawResult *sse.ToolResultPayload
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				t.Fatal("event channel closed before tool.result")
			}
			if ev.Type == sse.TypeToolResult {
				var p sse.ToolResultPayload
				if err := json.Unmarshal(ev.Data, &p); err != nil {
					t.Fatalf("decode tool.result: %v", err)
				}
				sawResult = &p
			}
			if ev.Type == sse.TypeTurnEnd {
				if sawResult == nil {
					t.Fatal("turn ended without a tool.result event")
				}
				goto done
			}
		case <-deadline:
			t.Fatal("timeout waiting for turn.end")
		}
	}

done:
	if len(sawResult.Result) != payloadLen {
		t.Fatalf("Result must carry full %d-byte payload, got %d bytes",
			payloadLen, len(sawResult.Result))
	}
	if sawResult.Result != payload {
		t.Fatalf("Result bytes do not match the payload exactly")
	}
	if len(sawResult.ResultSummary) > MaxToolResultPreviewInEvent+8 {
		t.Fatalf("ResultSummary should be truncated near %d, got %d",
			MaxToolResultPreviewInEvent, len(sawResult.ResultSummary))
	}
	if !strings.HasSuffix(sawResult.ResultSummary, "...") {
		t.Fatalf("ResultSummary expected ellipsis suffix on truncation, got tail %q",
			sawResult.ResultSummary[max(0, len(sawResult.ResultSummary)-10):])
	}
}

func TestRunTurn_UsesLoopToolResultContextCap(t *testing.T) {
	payload := strings.Repeat("A", 1024)
	var calls int32
	var secondReq string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := readReqBody(r)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		fl := w.(http.Flusher)
		if atomic.AddInt32(&calls, 1) == 1 {
			lines := []string{
				`data: {"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"c1","type":"function","function":{"name":"big","arguments":"{}"}}]},"finish_reason":null}]}`,
				`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
				`data: [DONE]`,
			}
			for _, l := range lines {
				w.Write([]byte(l + "\n\n"))
				fl.Flush()
			}
			return
		}
		secondReq = body
		w.Write([]byte("data: {\"choices\":[{\"delta\":{\"role\":\"assistant\",\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\n"))
		w.Write([]byte("data: [DONE]\n\n"))
		fl.Flush()
	}))
	t.Cleanup(srv.Close)

	conv, err := OpenConversationAt(filepath.Join(t.TempDir(), "sess.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	reg := NewRegistry()
	reg.Add(fakeBigResultTool(payload))
	events := make(chan sse.Event, 256)
	loop := &Loop{
		LLM: NewLLMClient(srv.URL, "", "fake-model"), Tools: reg, Conv: conv, Events: events,
		MaxTurnSteps: 4, PerCallTimeout: 5 * time.Second,
		ToolResultMaxBytesInContext: 128,
	}
	if err := loop.EnsureSystemPrompt("base"); err != nil {
		t.Fatal(err)
	}
	if _, err := loop.SendUser(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}

	deadline := time.After(10 * time.Second)
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				t.Fatal("event channel closed before turn.end")
			}
			if ev.Type != sse.TypeTurnEnd {
				continue
			}
			if !strings.Contains(secondReq, "[... 896 more bytes truncated.") {
				t.Fatalf("second request should include loop-specific truncation marker, body=%s", secondReq)
			}
			return
		case <-deadline:
			t.Fatal("timeout waiting for turn.end")
		}
	}
}

func TestRunTurn_RepairsToolArgumentsBeforeDispatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := readReqBody(r)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		fl := w.(http.Flusher)
		if !strings.Contains(body, `"role":"tool"`) {
			lines := []string{
				`data: {"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"c1","type":"function","function":{"name":"readFile","arguments":"{\"file_path\":\"README.md\",\"maxBytes\":\"128\",}"}}]},"finish_reason":null}]}`,
				`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
				`data: [DONE]`,
			}
			for _, l := range lines {
				w.Write([]byte(l + "\n\n"))
				fl.Flush()
			}
			return
		}
		w.Write([]byte("data: {\"choices\":[{\"delta\":{\"role\":\"assistant\",\"content\":\"done\"},\"finish_reason\":\"stop\"}]}\n\n"))
		w.Write([]byte("data: [DONE]\n\n"))
		fl.Flush()
	}))
	t.Cleanup(srv.Close)

	conv, err := OpenConversationAt(filepath.Join(t.TempDir(), "sess.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	argsCh := make(chan string, 1)
	reg := NewRegistry()
	reg.Add(&Tool{
		Name:        "read_file",
		Description: "read",
		Schema:      json.RawMessage(`{"type":"object","required":["path"],"properties":{"path":{"type":"string"},"max_bytes":{"type":"integer"}}}`),
		Execute: func(ctx context.Context, args json.RawMessage) (string, error) {
			argsCh <- string(args)
			return "ok", nil
		},
	})
	events := make(chan sse.Event, 256)
	loop := &Loop{
		LLM: NewLLMClient(srv.URL, "", "fake-model"), Tools: reg, Conv: conv, Events: events,
		MaxTurnSteps: 4, PerCallTimeout: 5 * time.Second,
	}
	if err := loop.EnsureSystemPrompt("base"); err != nil {
		t.Fatal(err)
	}
	if _, err := loop.SendUser(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}

	deadline := time.After(10 * time.Second)
	gotArgs := ""
	gotTool := ""
	var gotRepair sse.ToolRequestPayload
	for {
		select {
		case args := <-argsCh:
			gotArgs = args
		case ev, ok := <-events:
			if !ok {
				t.Fatal("event channel closed before turn.end")
			}
			if ev.Type == sse.TypeToolRequest {
				var p sse.ToolRequestPayload
				if err := json.Unmarshal(ev.Data, &p); err != nil {
					t.Fatalf("decode tool.request: %v", err)
				}
				gotTool = p.Tool
				gotRepair = p
			}
			if ev.Type == sse.TypeTurnEnd {
				if gotTool != "read_file" {
					t.Fatalf("tool request name = %q, want canonical read_file", gotTool)
				}
				if !gotRepair.Canonicalized || !gotRepair.ArgsRepaired {
					t.Fatalf("expected repair metadata, got %+v", gotRepair)
				}
				if !strings.Contains(gotRepair.OriginalArgsSummary, "file_path") {
					t.Fatalf("expected original args summary, got %+v", gotRepair)
				}
				if len(gotRepair.RepairNotes) == 0 {
					t.Fatalf("expected repair notes, got %+v", gotRepair)
				}
				if gotArgs != `{"max_bytes":128,"path":"README.md"}` {
					t.Fatalf("tool saw args %s, want repaired compact json", gotArgs)
				}
				return
			}
		case <-deadline:
			t.Fatal("timeout waiting for tool execution")
		}
	}
}

func TestRunTurn_BlocksExactRepeatedToolCalls(t *testing.T) {
	var reqs int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		fl := w.(http.Flusher)
		if atomic.AddInt32(&reqs, 1) <= 3 {
			lines := []string{
				`data: {"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"c1","type":"function","function":{"name":"probe","arguments":"{\"q\":\"same\"}"}}]},"finish_reason":null}]}`,
				`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
				`data: [DONE]`,
			}
			for _, l := range lines {
				w.Write([]byte(l + "\n\n"))
				fl.Flush()
			}
			return
		}
		w.Write([]byte("data: {\"choices\":[{\"delta\":{\"role\":\"assistant\",\"content\":\"done\"},\"finish_reason\":\"stop\"}]}\n\n"))
		w.Write([]byte("data: [DONE]\n\n"))
		fl.Flush()
	}))
	t.Cleanup(srv.Close)

	conv, err := OpenConversationAt(filepath.Join(t.TempDir(), "sess.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var executed int32
	reg := NewRegistry()
	reg.Add(&Tool{
		Name:        "probe",
		Description: "probe",
		Schema:      json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"}}}`),
		Execute: func(ctx context.Context, args json.RawMessage) (string, error) {
			atomic.AddInt32(&executed, 1)
			return "ok", nil
		},
	})
	events := make(chan sse.Event, 256)
	loop := &Loop{
		LLM: NewLLMClient(srv.URL, "", "fake-model"), Tools: reg, Conv: conv, Events: events,
		MaxTurnSteps: 5, PerCallTimeout: 5 * time.Second,
	}
	if err := loop.EnsureSystemPrompt("base"); err != nil {
		t.Fatal(err)
	}
	if _, err := loop.SendUser(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}

	deadline := time.After(10 * time.Second)
	sawGuard := false
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				t.Fatal("event channel closed before turn.end")
			}
			if ev.Type == sse.TypeToolResult {
				var p sse.ToolResultPayload
				if err := json.Unmarshal(ev.Data, &p); err != nil {
					t.Fatalf("decode tool.result: %v", err)
				}
				if strings.Contains(p.Result, "loop_guard: blocked exact repeated call") {
					sawGuard = true
				}
			}
			if ev.Type == sse.TypeTurnEnd {
				if !sawGuard {
					t.Fatal("expected loop guard tool result")
				}
				if got := atomic.LoadInt32(&executed); got != 2 {
					t.Fatalf("tool should execute twice before third repeat is blocked, got %d", got)
				}
				return
			}
		case <-deadline:
			t.Fatal("timeout waiting for turn.end")
		}
	}
}

func TestRunTurn_LoopGuardForcesNoToolSummaryAfterRepeatedInterventions(t *testing.T) {
	var reqs int32
	noToolsReq := make(chan bool, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := readReqBody(r)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		fl := w.(http.Flusher)
		if atomic.AddInt32(&reqs, 1) <= 4 {
			lines := []string{
				`data: {"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"c1","type":"function","function":{"name":"probe","arguments":"{\"q\":\"same\"}"}}]},"finish_reason":null}]}`,
				`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
				`data: [DONE]`,
			}
			for _, l := range lines {
				w.Write([]byte(l + "\n\n"))
				fl.Flush()
			}
			return
		}
		noToolsReq <- !strings.Contains(body, `"tools"`)
		w.Write([]byte("data: {\"choices\":[{\"delta\":{\"role\":\"assistant\",\"content\":\"summarized\"},\"finish_reason\":\"stop\"}]}\n\n"))
		w.Write([]byte("data: [DONE]\n\n"))
		fl.Flush()
	}))
	t.Cleanup(srv.Close)

	conv, err := OpenConversationAt(filepath.Join(t.TempDir(), "sess.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var executed int32
	reg := NewRegistry()
	reg.Add(&Tool{
		Name:        "probe",
		Description: "probe",
		Schema:      json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"}}}`),
		Execute: func(ctx context.Context, args json.RawMessage) (string, error) {
			atomic.AddInt32(&executed, 1)
			return "ok", nil
		},
	})
	events := make(chan sse.Event, 256)
	loop := &Loop{
		LLM: NewLLMClient(srv.URL, "", "fake-model"), Tools: reg, Conv: conv, Events: events,
		MaxTurnSteps: 6, PerCallTimeout: 5 * time.Second,
	}
	if err := loop.EnsureSystemPrompt("base"); err != nil {
		t.Fatal(err)
	}
	if _, err := loop.SendUser(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}

	deadline := time.After(10 * time.Second)
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				t.Fatal("event channel closed before turn.end")
			}
			if ev.Type == sse.TypeTurnEnd {
				var p sse.TurnEndPayload
				if err := json.Unmarshal(ev.Data, &p); err != nil {
					t.Fatalf("decode turn.end: %v", err)
				}
				if p.ToolStats == nil || p.ToolStats.LoopGuardInterventions < 2 || p.ToolStats.ForcedNoTools != 1 {
					t.Fatalf("expected guard stats in turn.end, got %+v", p.ToolStats)
				}
				select {
				case ok := <-noToolsReq:
					if !ok {
						t.Fatal("expected final recovery request to omit tools")
					}
				default:
					t.Fatal("did not observe final no-tools request")
				}
				if got := atomic.LoadInt32(&executed); got != 2 {
					t.Fatalf("tool should execute twice before repeated guard blocks, got %d", got)
				}
				return
			}
		case <-deadline:
			t.Fatal("timeout waiting for turn.end")
		}
	}
}

// readReqBody reads the request body without consuming r.Body for the
// real handler. Returns "" on error (not under test here).
func readReqBody(r *http.Request) (string, error) {
	if r.Body == nil {
		return "", nil
	}
	buf := make([]byte, 64*1024)
	n, _ := r.Body.Read(buf)
	return string(buf[:n]), nil
}

// TestRunTurn_MaxStepsEmitsMaxTurnsReason pins the "step limit" exit
// path: a model that keeps issuing tool_calls forever must end with
// reason=max_turns, not the misleading reason=completed the loop
// emitted before this fix.
func TestRunTurn_MaxStepsEmitsMaxTurnsReason(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		fl := w.(http.Flusher)
		// Every call: emit a single tool_call and a finish_reason of
		// "tool_calls". The loop will dispatch the tool and come back
		// for the next turn — ad infinitum, until MaxTurnSteps kicks in.
		lines := []string{
			`data: {"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"c","type":"function","function":{"name":"big","arguments":""}}]},"finish_reason":null}]}`,
			`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{}"}}]},"finish_reason":null}]}`,
			`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
			`data: [DONE]`,
		}
		for _, l := range lines {
			w.Write([]byte(l + "\n\n"))
			fl.Flush()
		}
	}))
	t.Cleanup(srv.Close)

	conv, err := OpenConversationAt(filepath.Join(t.TempDir(), "sess.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	reg := NewRegistry()
	reg.Add(fakeBigResultTool("ok"))

	events := make(chan sse.Event, 256)
	llm := NewLLMClient(srv.URL, "", "fake-model")
	loop := &Loop{
		LLM: llm, Tools: reg, Conv: conv, Events: events,
		MaxTurnSteps: 2, PerCallTimeout: 5 * time.Second,
	}
	if err := loop.EnsureSystemPrompt("base"); err != nil {
		t.Fatal(err)
	}
	if _, err := loop.SendUser(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}

	deadline := time.After(10 * time.Second)
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				t.Fatal("event channel closed before turn.end")
			}
			if ev.Type != sse.TypeTurnEnd {
				continue
			}
			var p sse.TurnEndPayload
			if err := json.Unmarshal(ev.Data, &p); err != nil {
				t.Fatalf("decode turn.end: %v", err)
			}
			if p.Reason != sse.TurnEndMaxTurns {
				t.Fatalf("expected reason=%q, got %q", sse.TurnEndMaxTurns, p.Reason)
			}
			return
		case <-deadline:
			t.Fatal("timeout waiting for turn.end")
		}
	}
}

func TestRunTurn_AllowsFinalAnswerAfterLastToolRound(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		fl := w.(http.Flusher)
		if atomic.AddInt32(&calls, 1) == 1 {
			lines := []string{
				`data: {"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"c1","type":"function","function":{"name":"big","arguments":""}}]},"finish_reason":null}]}`,
				`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{}"}}]},"finish_reason":null}]}`,
				`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
				`data: [DONE]`,
			}
			for _, l := range lines {
				w.Write([]byte(l + "\n\n"))
				fl.Flush()
			}
			return
		}
		w.Write([]byte("data: {\"choices\":[{\"delta\":{\"role\":\"assistant\",\"content\":\"done from tool evidence\"},\"finish_reason\":\"stop\"}]}\n\n"))
		w.Write([]byte("data: [DONE]\n\n"))
		fl.Flush()
	}))
	t.Cleanup(srv.Close)

	conv, err := OpenConversationAt(filepath.Join(t.TempDir(), "sess.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	reg := NewRegistry()
	reg.Add(fakeBigResultTool("evidence"))

	events := make(chan sse.Event, 256)
	loop := &Loop{
		LLM: NewLLMClient(srv.URL, "", "fake-model"), Tools: reg, Conv: conv, Events: events,
		MaxTurnSteps: 1, PerCallTimeout: 5 * time.Second,
	}
	if err := loop.EnsureSystemPrompt("base"); err != nil {
		t.Fatal(err)
	}
	if _, err := loop.SendUser(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}

	deadline := time.After(10 * time.Second)
	var finalText string
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				t.Fatal("event channel closed before turn.end")
			}
			switch ev.Type {
			case sse.TypeMessageDone:
				var p sse.MessageDonePayload
				if err := json.Unmarshal(ev.Data, &p); err != nil {
					t.Fatalf("decode message.done: %v", err)
				}
				finalText = p.Text
			case sse.TypeTurnEnd:
				var p sse.TurnEndPayload
				if err := json.Unmarshal(ev.Data, &p); err != nil {
					t.Fatalf("decode turn.end: %v", err)
				}
				if p.Reason != sse.TurnEndCompleted {
					t.Fatalf("expected reason=%q, got %q", sse.TurnEndCompleted, p.Reason)
				}
				if finalText != "done from tool evidence" {
					t.Fatalf("final answer missing after last tool round: %q", finalText)
				}
				return
			}
		case <-deadline:
			t.Fatal("timeout waiting for turn.end")
		}
	}
}

func TestRunTurn_FinalNoToolsOnMaxTurnsForcesSummary(t *testing.T) {
	var calls int32
	var finalRequestHadTools atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := readReqBody(r)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		fl := w.(http.Flusher)
		switch atomic.AddInt32(&calls, 1) {
		case 1, 2:
			lines := []string{
				`data: {"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"c1","type":"function","function":{"name":"big","arguments":"{}"}}]},"finish_reason":null}]}`,
				`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
				`data: [DONE]`,
			}
			for _, l := range lines {
				w.Write([]byte(l + "\n\n"))
				fl.Flush()
			}
		default:
			finalRequestHadTools.Store(strings.Contains(body, `"tools"`))
			w.Write([]byte("data: {\"choices\":[{\"delta\":{\"role\":\"assistant\",\"content\":\"partial summary from evidence\"},\"finish_reason\":\"stop\"}]}\n\n"))
			w.Write([]byte("data: [DONE]\n\n"))
			fl.Flush()
		}
	}))
	t.Cleanup(srv.Close)

	conv, err := OpenConversationAt(filepath.Join(t.TempDir(), "sess.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	reg := NewRegistry()
	reg.Add(fakeBigResultTool("evidence"))

	events := make(chan sse.Event, 256)
	loop := &Loop{
		LLM: NewLLMClient(srv.URL, "", "fake-model"), Tools: reg, Conv: conv, Events: events,
		MaxTurnSteps: 1, PerCallTimeout: 5 * time.Second,
		FinalNoToolsOnMaxTurns: true,
	}
	if err := loop.EnsureSystemPrompt("base"); err != nil {
		t.Fatal(err)
	}
	if _, err := loop.SendUser(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}

	deadline := time.After(10 * time.Second)
	var finalText string
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				t.Fatal("event channel closed before turn.end")
			}
			switch ev.Type {
			case sse.TypeMessageDone:
				var p sse.MessageDonePayload
				if err := json.Unmarshal(ev.Data, &p); err != nil {
					t.Fatalf("decode message.done: %v", err)
				}
				finalText = p.Text
			case sse.TypeTurnEnd:
				var p sse.TurnEndPayload
				if err := json.Unmarshal(ev.Data, &p); err != nil {
					t.Fatalf("decode turn.end: %v", err)
				}
				if p.Reason != sse.TurnEndCompleted {
					t.Fatalf("expected completed after final no-tool pass, got %q", p.Reason)
				}
				if finalText != "partial summary from evidence" {
					t.Fatalf("final answer missing: %q", finalText)
				}
				if finalRequestHadTools.Load() {
					t.Fatal("final max-turns recovery request must not include tools")
				}
				return
			}
		case <-deadline:
			t.Fatal("timeout waiting for turn.end")
		}
	}
}

func TestRunTurn_MaxToolCallsForcesNoToolSummary(t *testing.T) {
	var calls int32
	var secondToolRan int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := readReqBody(r)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		fl := w.(http.Flusher)
		if atomic.AddInt32(&calls, 1) == 1 {
			lines := []string{
				`data: {"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"c1","type":"function","function":{"name":"first","arguments":"{}"}},{"index":1,"id":"c2","type":"function","function":{"name":"second","arguments":"{}"}}]},"finish_reason":null}]}`,
				`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
				`data: [DONE]`,
			}
			for _, l := range lines {
				w.Write([]byte(l + "\n\n"))
				fl.Flush()
			}
			return
		}
		if strings.Contains(body, `"tools"`) {
			t.Errorf("final max-tool-call recovery request must not include tools: %s", body)
		}
		w.Write([]byte("data: {\"choices\":[{\"delta\":{\"role\":\"assistant\",\"content\":\"summary after tool cap\"},\"finish_reason\":\"stop\"}]}\n\n"))
		w.Write([]byte("data: [DONE]\n\n"))
		fl.Flush()
	}))
	t.Cleanup(srv.Close)

	reg := NewRegistry()
	reg.Add(&Tool{Name: "first", Description: "first", Schema: json.RawMessage(`{"type":"object","properties":{}}`), Execute: func(context.Context, json.RawMessage) (string, error) {
		return "first ok", nil
	}})
	reg.Add(&Tool{Name: "second", Description: "second", Schema: json.RawMessage(`{"type":"object","properties":{}}`), Execute: func(context.Context, json.RawMessage) (string, error) {
		atomic.AddInt32(&secondToolRan, 1)
		return "second should not run", nil
	}})

	conv, err := OpenConversationAt(filepath.Join(t.TempDir(), "sess.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	events := make(chan sse.Event, 256)
	loop := &Loop{
		LLM: NewLLMClient(srv.URL, "", "fake-model"), Tools: reg, Conv: conv, Events: events,
		MaxTurnSteps: 4, MaxToolCalls: 1, PerCallTimeout: 5 * time.Second,
		FinalNoToolsOnMaxTurns: true,
	}
	if err := loop.EnsureSystemPrompt("base"); err != nil {
		t.Fatal(err)
	}
	if _, err := loop.SendUser(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}

	deadline := time.After(10 * time.Second)
	var sawSkipped bool
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				t.Fatal("event channel closed before turn.end")
			}
			if ev.Type == sse.TypeToolResult {
				var p sse.ToolResultPayload
				if err := json.Unmarshal(ev.Data, &p); err != nil {
					t.Fatalf("decode tool.result: %v", err)
				}
				if strings.Contains(p.Result, "tool call budget reached") {
					sawSkipped = true
				}
			}
			if ev.Type != sse.TypeTurnEnd {
				continue
			}
			var p sse.TurnEndPayload
			if err := json.Unmarshal(ev.Data, &p); err != nil {
				t.Fatalf("decode turn.end: %v", err)
			}
			if p.Reason != sse.TurnEndCompleted {
				t.Fatalf("expected completed after tool cap recovery, got %q", p.Reason)
			}
			if !sawSkipped {
				t.Fatal("expected skipped tool result for capped second tool")
			}
			if got := atomic.LoadInt32(&secondToolRan); got != 0 {
				t.Fatalf("second tool should not run after cap; got %d calls", got)
			}
			return
		case <-deadline:
			t.Fatal("timeout waiting for turn.end")
		}
	}
}

func TestRunTurn_SubagentFirstPolicyGuardsParentExploration(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		fl := w.(http.Flusher)
		switch atomic.AddInt32(&calls, 1) {
		case 1:
			lines := []string{
				`data: {"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"c1","type":"function","function":{"name":"list_files","arguments":"{\"path\":\".\"}"}}]},"finish_reason":null}]}`,
				`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
				`data: [DONE]`,
			}
			for _, l := range lines {
				w.Write([]byte(l + "\n\n"))
				fl.Flush()
			}
		case 2:
			lines := []string{
				`data: {"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"c2","type":"function","function":{"name":"subagent_run","arguments":"{\"task\":\"review\",\"mode\":\"review\"}"}}]},"finish_reason":null}]}`,
				`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
				`data: [DONE]`,
			}
			for _, l := range lines {
				w.Write([]byte(l + "\n\n"))
				fl.Flush()
			}
		default:
			w.Write([]byte("data: {\"choices\":[{\"delta\":{\"role\":\"assistant\",\"content\":\"done\"},\"finish_reason\":\"stop\"}]}\n\n"))
			w.Write([]byte("data: [DONE]\n\n"))
			fl.Flush()
		}
	}))
	t.Cleanup(srv.Close)

	var listCalls int32
	var subagentCalls int32
	reg := NewRegistry()
	reg.Add(&Tool{
		Name:        "list_files",
		Description: "test list",
		Schema:      json.RawMessage(`{"type":"object","properties":{}}`),
		Execute: func(context.Context, json.RawMessage) (string, error) {
			atomic.AddInt32(&listCalls, 1)
			return "listed", nil
		},
	})
	reg.Add(&Tool{
		Name:        "subagent_run",
		Description: "test subagent",
		Schema:      json.RawMessage(`{"type":"object","properties":{}}`),
		Execute: func(context.Context, json.RawMessage) (string, error) {
			atomic.AddInt32(&subagentCalls, 1)
			return `{"report":"Conclusion:\nok","ok":true}`, nil
		},
	})

	conv, err := OpenConversationAt(filepath.Join(t.TempDir(), "sess.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	events := make(chan sse.Event, 256)
	loop := &Loop{
		LLM: NewLLMClient(srv.URL, "", "fake-model"), Tools: reg, Conv: conv, Events: events,
		MaxTurnSteps: 3, PerCallTimeout: 5 * time.Second,
		FirstToolPolicy: SubagentFirstToolPolicy(),
	}
	if err := loop.EnsureSystemPrompt("base"); err != nil {
		t.Fatal(err)
	}
	if _, err := loop.SendUser(context.Background(), "please use subagent_run first"); err != nil {
		t.Fatal(err)
	}

	deadline := time.After(10 * time.Second)
	var sawGuard bool
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				t.Fatal("event channel closed before turn.end")
			}
			if ev.Type == sse.TypeToolResult {
				var p sse.ToolResultPayload
				if err := json.Unmarshal(ev.Data, &p); err != nil {
					t.Fatalf("decode tool.result: %v", err)
				}
				if strings.Contains(p.Result, "first_tool_policy") {
					sawGuard = true
				}
			}
			if ev.Type != sse.TypeTurnEnd {
				continue
			}
			var p sse.TurnEndPayload
			if err := json.Unmarshal(ev.Data, &p); err != nil {
				t.Fatalf("decode turn.end: %v", err)
			}
			if p.Reason != sse.TurnEndCompleted {
				t.Fatalf("expected completed, got %q", p.Reason)
			}
			if !sawGuard {
				t.Fatal("expected parent exploration tool to be guarded before subagent_run")
			}
			if got := atomic.LoadInt32(&listCalls); got != 0 {
				t.Fatalf("list_files should not execute before subagent_run; got %d calls", got)
			}
			if got := atomic.LoadInt32(&subagentCalls); got != 1 {
				t.Fatalf("subagent_run should execute once; got %d calls", got)
			}
			return
		case <-deadline:
			t.Fatal("timeout waiting for turn.end")
		}
	}
}

func TestRunTurn_SubagentPostPolicyGuardsDuplicateExploration(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		fl := w.(http.Flusher)
		switch atomic.AddInt32(&calls, 1) {
		case 1:
			lines := []string{
				`data: {"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"c1","type":"function","function":{"name":"subagent_run","arguments":"{\"task\":\"review\",\"mode\":\"review\"}"}}]},"finish_reason":null}]}`,
				`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
				`data: [DONE]`,
			}
			for _, l := range lines {
				w.Write([]byte(l + "\n\n"))
				fl.Flush()
			}
		case 2:
			lines := []string{
				`data: {"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"c2","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"internal/agent/subagent.go\"}"}}]},"finish_reason":null}]}`,
				`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
				`data: [DONE]`,
			}
			for _, l := range lines {
				w.Write([]byte(l + "\n\n"))
				fl.Flush()
			}
		default:
			w.Write([]byte("data: {\"choices\":[{\"delta\":{\"role\":\"assistant\",\"content\":\"answered from report\"},\"finish_reason\":\"stop\"}]}\n\n"))
			w.Write([]byte("data: [DONE]\n\n"))
			fl.Flush()
		}
	}))
	t.Cleanup(srv.Close)

	var readCalls int32
	reg := NewRegistry()
	reg.Add(&Tool{
		Name:        "subagent_run",
		Description: "test subagent",
		Schema:      json.RawMessage(`{"type":"object","properties":{}}`),
		Execute: func(context.Context, json.RawMessage) (string, error) {
			return `{"report":"Conclusion:\nok","ok":true}`, nil
		},
	})
	reg.Add(&Tool{
		Name:        "read_file",
		Description: "test read",
		Schema:      json.RawMessage(`{"type":"object","properties":{}}`),
		Execute: func(context.Context, json.RawMessage) (string, error) {
			atomic.AddInt32(&readCalls, 1)
			return "file contents", nil
		},
	})

	conv, err := OpenConversationAt(filepath.Join(t.TempDir(), "sess.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	events := make(chan sse.Event, 256)
	loop := &Loop{
		LLM: NewLLMClient(srv.URL, "", "fake-model"), Tools: reg, Conv: conv, Events: events,
		MaxTurnSteps: 3, PerCallTimeout: 5 * time.Second,
		PostToolPolicy: SubagentPostToolPolicy(),
	}
	if err := loop.EnsureSystemPrompt("base"); err != nil {
		t.Fatal(err)
	}
	if _, err := loop.SendUser(context.Background(), "review with subagent"); err != nil {
		t.Fatal(err)
	}

	deadline := time.After(10 * time.Second)
	var sawGuard bool
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				t.Fatal("event channel closed before turn.end")
			}
			if ev.Type == sse.TypeToolResult {
				var p sse.ToolResultPayload
				if err := json.Unmarshal(ev.Data, &p); err != nil {
					t.Fatalf("decode tool.result: %v", err)
				}
				if strings.Contains(p.Result, "post_tool_policy") {
					sawGuard = true
				}
			}
			if ev.Type != sse.TypeTurnEnd {
				continue
			}
			var p sse.TurnEndPayload
			if err := json.Unmarshal(ev.Data, &p); err != nil {
				t.Fatalf("decode turn.end: %v", err)
			}
			if p.Reason != sse.TurnEndCompleted {
				t.Fatalf("expected completed, got %q", p.Reason)
			}
			if !sawGuard {
				t.Fatal("expected duplicate parent exploration to be guarded after subagent_run")
			}
			if got := atomic.LoadInt32(&readCalls); got != 0 {
				t.Fatalf("read_file should not execute after successful subagent_run; got %d calls", got)
			}
			return
		case <-deadline:
			t.Fatal("timeout waiting for turn.end")
		}
	}
}

func TestRunTurn_SubagentPostPolicyGuardsRepeatedSubagentAfterFailure(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fl, _ := w.(http.Flusher)
		switch atomic.AddInt32(&calls, 1) {
		case 1:
			_, _ = w.Write([]byte(`data: {"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"c1","type":"function","function":{"name":"subagent_run","arguments":"{\"task\":\"too broad\"}"}},{"index":1,"id":"c2","type":"function","function":{"name":"subagent_run","arguments":"{\"task\":\"try again\"}"}}]},"finish_reason":"tool_calls"}]}` + "\n\n"))
			_, _ = w.Write([]byte("data: [DONE]\n\n"))
		default:
			_, _ = w.Write([]byte(`data: {"choices":[{"delta":{"role":"assistant","content":"used partial evidence"},"finish_reason":"stop"}]}` + "\n\n"))
			_, _ = w.Write([]byte("data: [DONE]\n\n"))
		}
		if fl != nil {
			fl.Flush()
		}
	}))
	t.Cleanup(srv.Close)

	var subagentCalls int32
	reg := NewRegistry()
	reg.Add(&Tool{
		Name:        "subagent_run",
		Description: "test subagent",
		Schema:      json.RawMessage(`{"type":"object","properties":{}}`),
		Execute: func(context.Context, json.RawMessage) (string, error) {
			atomic.AddInt32(&subagentCalls, 1)
			return `{"report":"Conclusion:\npartial","ok":false,"turn_end_reason":"max_turns"}`, nil
		},
	})

	conv, err := OpenConversationAt(filepath.Join(t.TempDir(), "sess.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	events := make(chan sse.Event, 256)
	loop := &Loop{
		LLM: NewLLMClient(srv.URL, "", "fake-model"), Tools: reg, Conv: conv, Events: events,
		MaxTurnSteps: 3, PerCallTimeout: 5 * time.Second,
		PostToolPolicy: SubagentPostToolPolicy(),
	}
	if err := loop.EnsureSystemPrompt("base"); err != nil {
		t.Fatal(err)
	}
	if _, err := loop.SendUser(context.Background(), "use subagent broadly"); err != nil {
		t.Fatal(err)
	}

	deadline := time.After(10 * time.Second)
	var sawRepeatGuard bool
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				t.Fatal("event channel closed before turn.end")
			}
			if ev.Type == sse.TypeToolResult {
				var p sse.ToolResultPayload
				if err := json.Unmarshal(ev.Data, &p); err != nil {
					t.Fatalf("decode tool.result: %v", err)
				}
				if strings.Contains(p.Result, "already ran this turn") {
					sawRepeatGuard = true
				}
			}
			if ev.Type != sse.TypeTurnEnd {
				continue
			}
			if !sawRepeatGuard {
				t.Fatal("expected repeated subagent_run to be guarded after first result")
			}
			if got := atomic.LoadInt32(&subagentCalls); got != 1 {
				t.Fatalf("subagent_run should execute once; got %d", got)
			}
			return
		case <-deadline:
			t.Fatal("timeout waiting for turn.end")
		}
	}
}

// cancelOnFirstCompact is a Compactor that calls Loop.Cancel during
// its very first invocation, then returns ctx.Err. Lets the test
// trigger the "cancel during reactive compaction" race window.
type cancelOnFirstCompact struct {
	loop  *Loop
	fired *int32
}

func (c *cancelOnFirstCompact) Compact(ctx context.Context, msgs []ChatMessage) ([]ChatMessage, error) {
	if atomic.AddInt32(c.fired, 1) == 1 && c.loop != nil {
		c.loop.Cancel()
	}
	return nil, ctx.Err()
}

// TestRunTurn_CancelDuringReactiveCompactSurfacesCancelled pins that
// a Loop.Cancel fired WHILE the reactive compactor is summarizing
// surfaces as TurnEndCancelled, not as the misleading TurnEndError
// carrying the upstream's context-overflow text. Pre-fix the path
// was: LLM returns overflow → maybeCompact runs the (cancel-aware)
// compactor → compactor returns ctx.Err → maybeCompact logs and
// returns false → runStep falls through to the transient-retry
// branch, finds the overflow err non-retryable, and bails with
// reason=error. Operator's turn.end log entry was wrong about why
// the turn ended.
func TestRunTurn_CancelDuringReactiveCompactSurfacesCancelled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Every request returns a non-retryable 400 whose body matches
		// IsContextOverflow's keyword list — forcing the reactive
		// compaction branch on every attempt.
		w.WriteHeader(400)
		_, _ = w.Write([]byte(`{"error":{"message":"This model's maximum context length is 8192 tokens. However, your messages resulted in 12345 tokens."}}`))
	}))
	t.Cleanup(srv.Close)

	conv, err := OpenConversationAt(filepath.Join(t.TempDir(), "sess.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	events := make(chan sse.Event, 256)
	llm := NewLLMClient(srv.URL, "", "fake-model")
	loop := &Loop{
		LLM: llm, Tools: NewRegistry(), Conv: conv, Events: events,
		MaxTurnSteps: 4, PerCallTimeout: 5 * time.Second,
	}
	fired := int32(0)
	loop.Compactor = &cancelOnFirstCompact{loop: loop, fired: &fired}

	if err := loop.EnsureSystemPrompt("base"); err != nil {
		t.Fatal(err)
	}
	if _, err := loop.SendUser(context.Background(), "trigger overflow"); err != nil {
		t.Fatal(err)
	}

	deadline := time.After(10 * time.Second)
	var endReason string
	for endReason == "" {
		select {
		case ev, ok := <-events:
			if !ok {
				t.Fatal("event channel closed before turn.end")
			}
			if ev.Type != sse.TypeTurnEnd {
				continue
			}
			var p sse.TurnEndPayload
			if err := json.Unmarshal(ev.Data, &p); err != nil {
				t.Fatalf("decode turn.end: %v", err)
			}
			endReason = p.Reason
		case <-deadline:
			t.Fatal("timeout waiting for turn.end")
		}
	}
	if endReason != sse.TurnEndCancelled {
		t.Errorf("expected reason=%q, got %q (compactor.fired=%d)",
			sse.TurnEndCancelled, endReason, atomic.LoadInt32(&fired))
	}
}

// TestRunTurn_BackfillsMissingToolCallID pins the end-to-end path:
// a model that emits a tool_call WITHOUT an `id` (some chutes-routed
// / DeepSeek-mode providers do this) still produces a conv log where
// the assistant's tool_call id matches the tool message's
// tool_call_id. Pre-fix, persistence kept id="" while runTurn
// generated a local "call_xxx" for the response — and the next LLM
// request 400'd on the unmatched pair, bricking the session.
func TestRunTurn_BackfillsMissingToolCallID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		fl := w.(http.Flusher)
		body, _ := readReqBody(r)
		if strings.Contains(body, `"role":"tool"`) {
			// Turn 2: produce a final answer once the tool result is in.
			w.Write([]byte("data: {\"choices\":[{\"delta\":{\"role\":\"assistant\",\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\n"))
			fl.Flush()
			return
		}
		// Turn 1: emit a tool_call delta with NO "id" field — this is
		// the upstream-bug shape that pre-fix would brick the session.
		lines := []string{
			`data: {"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"type":"function","function":{"name":"big","arguments":""}}]},"finish_reason":null}]}`,
			`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{}"}}]},"finish_reason":null}]}`,
			`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
			`data: [DONE]`,
		}
		for _, l := range lines {
			w.Write([]byte(l + "\n\n"))
			fl.Flush()
		}
	}))
	t.Cleanup(srv.Close)

	conv, err := OpenConversationAt(filepath.Join(t.TempDir(), "sess.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	reg := NewRegistry()
	reg.Add(fakeBigResultTool("ok"))

	events := make(chan sse.Event, 256)
	llm := NewLLMClient(srv.URL, "", "fake-model")
	loop := &Loop{
		LLM: llm, Tools: reg, Conv: conv, Events: events,
		MaxTurnSteps: 4, PerCallTimeout: 5 * time.Second,
	}
	if err := loop.EnsureSystemPrompt("base"); err != nil {
		t.Fatal(err)
	}
	if _, err := loop.SendUser(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}

	deadline := time.After(10 * time.Second)
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				t.Fatal("event channel closed before turn.end")
			}
			if ev.Type != sse.TypeTurnEnd {
				continue
			}
			// Drain done — inspect the conv.
			goto inspect
		case <-deadline:
			t.Fatal("timeout waiting for turn.end")
		}
	}
inspect:
	msgs := conv.Snapshot()
	var assistantID, toolID string
	for _, m := range msgs {
		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			assistantID = m.ToolCalls[0].ID
		}
		if m.Role == "tool" {
			toolID = m.ToolCallID
		}
	}
	if assistantID == "" {
		t.Fatalf("assistant tool_call.id must be backfilled (not left empty); conv: %+v", msgs)
	}
	if toolID == "" {
		t.Fatalf("tool message tool_call_id missing; conv: %+v", msgs)
	}
	if assistantID != toolID {
		t.Errorf("assistant tool_call.id (%q) must match tool tool_call_id (%q) — strict OpenAI-compat backends 400 otherwise", assistantID, toolID)
	}
}

// TestRunTurn_CancelMidBatchSkipsRemainingToolCalls pins that a
// Loop.Cancel fired while the loop is partway through a batch of
// tool_calls aborts the batch instead of running every remaining
// tool. Pre-fix the cancellation check only sat at the top of each
// outer step iteration, so a 3-tool batch where the user cancelled
// after the first tool returned still executed tools #2 and #3 with
// no way to interrupt. Now the inner loop checks ctx between calls.
func TestRunTurn_CancelMidBatchSkipsRemainingToolCalls(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		fl := w.(http.Flusher)
		body, _ := readReqBody(r)
		// Turn 2 path doesn't occur — we cancel out of turn 1 — but be
		// defensive: if anything ever drives a second LLM call, emit a
		// trivial final answer so the test doesn't hang.
		if strings.Contains(body, `"role":"tool"`) {
			w.Write([]byte("data: {\"choices\":[{\"delta\":{\"role\":\"assistant\",\"content\":\"done\"},\"finish_reason\":\"stop\"}]}\n\n"))
			fl.Flush()
			return
		}
		// Three tool_calls in one batch, all targeting "counter".
		lines := []string{
			`data: {"choices":[{"delta":{"role":"assistant","tool_calls":[` +
				`{"index":0,"id":"c1","type":"function","function":{"name":"counter","arguments":""}},` +
				`{"index":1,"id":"c2","type":"function","function":{"name":"counter","arguments":""}},` +
				`{"index":2,"id":"c3","type":"function","function":{"name":"counter","arguments":""}}` +
				`]},"finish_reason":null}]}`,
			`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{}"}}]},"finish_reason":null}]}`,
			`data: {"choices":[{"delta":{"tool_calls":[{"index":1,"function":{"arguments":"{}"}}]},"finish_reason":null}]}`,
			`data: {"choices":[{"delta":{"tool_calls":[{"index":2,"function":{"arguments":"{}"}}]},"finish_reason":null}]}`,
			`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
			`data: [DONE]`,
		}
		for _, l := range lines {
			w.Write([]byte(l + "\n\n"))
			fl.Flush()
		}
	}))
	t.Cleanup(srv.Close)

	var loopPtr *Loop
	var calls int32
	counterTool := &Tool{
		Name:        "counter",
		Description: "increments a counter; first call cancels the loop",
		Schema:      json.RawMessage(`{"type":"object","properties":{}}`),
		Execute: func(ctx context.Context, args json.RawMessage) (string, error) {
			n := atomic.AddInt32(&calls, 1)
			if n == 1 && loopPtr != nil {
				loopPtr.Cancel()
			}
			return "ok", nil
		},
	}

	conv, err := OpenConversationAt(filepath.Join(t.TempDir(), "sess.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	reg := NewRegistry()
	reg.Add(counterTool)

	events := make(chan sse.Event, 256)
	llm := NewLLMClient(srv.URL, "", "fake-model")
	loop := &Loop{
		LLM: llm, Tools: reg, Conv: conv, Events: events,
		MaxTurnSteps: 4, PerCallTimeout: 5 * time.Second,
	}
	loopPtr = loop
	if err := loop.EnsureSystemPrompt("base"); err != nil {
		t.Fatal(err)
	}
	if _, err := loop.SendUser(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}

	deadline := time.After(10 * time.Second)
	var endReason string
	for endReason == "" {
		select {
		case ev, ok := <-events:
			if !ok {
				t.Fatal("event channel closed before turn.end")
			}
			if ev.Type != sse.TypeTurnEnd {
				continue
			}
			var p sse.TurnEndPayload
			if err := json.Unmarshal(ev.Data, &p); err != nil {
				t.Fatalf("decode turn.end: %v", err)
			}
			endReason = p.Reason
		case <-deadline:
			t.Fatal("timeout waiting for turn.end")
		}
	}
	if endReason != sse.TurnEndCancelled {
		t.Errorf("expected reason=%q, got %q", sse.TurnEndCancelled, endReason)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("expected exactly 1 tool call before cancel propagated, got %d", got)
	}

	// The assistant message (already appended by consumeAndPersist
	// before the tool loop ran) carries three tool_calls. The conv
	// log must contain a matching tool message for every one of
	// them, otherwise the next LLM request on this session is
	// rejected by every OpenAI-compatible backend with "tool_calls
	// expect matching tool messages". Cancellation must leave the
	// log in a replayable state.
	msgs := conv.Snapshot()
	var toolCallIDs []string
	respondedIDs := map[string]bool{}
	for _, m := range msgs {
		if m.Role == "assistant" {
			for _, tc := range m.ToolCalls {
				toolCallIDs = append(toolCallIDs, tc.ID)
			}
		}
		if m.Role == "tool" && m.ToolCallID != "" {
			respondedIDs[m.ToolCallID] = true
		}
	}
	if len(toolCallIDs) != 3 {
		t.Fatalf("expected the assistant message to carry 3 tool_calls; got %d in conv: %+v", len(toolCallIDs), msgs)
	}
	for _, id := range toolCallIDs {
		if !respondedIDs[id] {
			t.Errorf("tool_call %q has no matching tool message after cancel; conv would be rejected on next LLM request", id)
		}
	}
}

// TestSendUser_HonorsCancelledCtx pins the entry-time ctx check.
// Pre-fix, SendUser accepted a ctx in its signature but never read
// it, so a caller whose ctx was already cancelled (e.g. an HTTP
// request that disconnected before reaching the handler) would
// still allocate a turn slot and start the loop. Now the call short-
// circuits with ctx.Err() before any state changes.
func TestSendUser_HonorsCancelledCtx(t *testing.T) {
	conv, err := OpenConversationAt(filepath.Join(t.TempDir(), "sess.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	loop := &Loop{
		LLM:   &LLMClient{BaseURL: "http://unused", Model: "fake"},
		Tools: NewRegistry(),
		Conv:  conv,
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	turnID, err := loop.SendUser(ctx, "hi")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got err=%v turnID=%q", err, turnID)
	}
	if turnID != "" {
		t.Errorf("turnID must be empty when ctx was pre-cancelled, got %q", turnID)
	}
	// Verify the loop's state machine wasn't perturbed: the next valid
	// call (uncancelled ctx) should still be able to start a turn, not
	// see a stale "turn in flight" slot.
	loop.mu.Lock()
	current := loop.current
	loop.mu.Unlock()
	if current != "" {
		t.Errorf("a cancelled SendUser must not leave loop.current set: got %q", current)
	}
}
