package affent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/affinefoundation/affent/sse"
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
	for sawResult == nil {
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
			}
		case <-deadline:
			t.Fatal("timeout waiting for tool.result")
		}
	}

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
