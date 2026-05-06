package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

// fakeMCPServer implements the streamable-http transport against a
// pinned tool catalog. Used to exercise httpWire end-to-end without
// pulling in a real MCP server.
func fakeMCPServer(t *testing.T) *httptest.Server {
	t.Helper()

	type rpc struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      any             `json:"id,omitempty"`
		Method  string          `json:"method"`
		Params  json.RawMessage `json:"params,omitempty"`
		Result  json.RawMessage `json:"result,omitempty"`
	}

	const sessionID = "fake-session-001"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", 405)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var req rpc
		_ = json.Unmarshal(body, &req)

		// Notifications (no id) get a 202 ack and nothing else.
		if req.ID == nil {
			w.Header().Set("Mcp-Session-Id", sessionID)
			w.WriteHeader(http.StatusAccepted)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Mcp-Session-Id", sessionID)

		switch req.Method {
		case "initialize":
			result := map[string]any{
				"protocolVersion": "2025-06-18",
				"serverInfo":      map[string]string{"name": "fake-server", "version": "0.1"},
				"capabilities":    map[string]any{},
			}
			rb, _ := json.Marshal(result)
			resp := rpc{JSONRPC: "2.0", ID: req.ID, Result: rb}
			_ = json.NewEncoder(w).Encode(resp)
		case "tools/list":
			tools := map[string]any{
				"tools": []map[string]any{
					{
						"name":        "echo",
						"description": "echo back",
						"inputSchema": json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}}}`),
					},
				},
			}
			rb, _ := json.Marshal(tools)
			_ = json.NewEncoder(w).Encode(rpc{JSONRPC: "2.0", ID: req.ID, Result: rb})
		case "tools/call":
			// Pretend the echo tool just dumps its args back as text.
			text := fmt.Sprintf("got: %s", req.Params)
			result := map[string]any{
				"content": []map[string]any{{"type": "text", "text": text}},
				"isError": false,
			}
			rb, _ := json.Marshal(result)
			_ = json.NewEncoder(w).Encode(rpc{JSONRPC: "2.0", ID: req.ID, Result: rb})
		default:
			http.Error(w, "method not supported: "+req.Method, http.StatusBadRequest)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestHTTPWire_ListAndCall(t *testing.T) {
	srv := fakeMCPServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	c, err := Start(ctx, ServerSpec{
		Name: "fake",
		URL:  srv.URL,
	}, zerolog.Nop())
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer c.Close()

	tools, err := c.ListTools(ctx)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("expected one tool 'echo', got %+v", tools)
	}

	out, err := c.CallTool(ctx, "echo", json.RawMessage(`{"text":"hello"}`))
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if out.IsError {
		t.Errorf("call shouldn't be error")
	}
	if !strings.Contains(out.Text, `"text":"hello"`) {
		t.Errorf("expected echoed args in text; got %q", out.Text)
	}
}

func TestStartWire_RejectsBothCommandAndURL(t *testing.T) {
	_, err := startWire(context.Background(), ServerSpec{
		Name:    "bad",
		Command: "echo",
		URL:     "http://x",
	}, zerolog.Nop())
	if err == nil || !strings.Contains(err.Error(), "both URL and Command") {
		t.Fatalf("expected mutual-exclusion error, got %v", err)
	}
}

func TestStartWire_RejectsNeither(t *testing.T) {
	_, err := startWire(context.Background(), ServerSpec{Name: "empty"}, zerolog.Nop())
	if err == nil || !strings.Contains(err.Error(), "URL") {
		t.Fatalf("expected missing-transport error, got %v", err)
	}
}
