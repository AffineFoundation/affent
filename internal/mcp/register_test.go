package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	agent "github.com/affinefoundation/affent/internal/agent"
	"github.com/rs/zerolog"
)

type fakeRegisterMCP struct {
	srv *httptest.Server

	mu     sync.Mutex
	tools  []ToolDescriptor
	called []string
}

func newFakeRegisterMCP(t *testing.T, tools []ToolDescriptor) *fakeRegisterMCP {
	t.Helper()
	f := &fakeRegisterMCP{tools: tools}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req rpcRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		switch req.Method {
		case "initialize":
			f.writeResult(w, req.ID, initializeResult{
				ProtocolVersion: "2025-06-18",
				Capabilities:    map[string]any{},
				ServerInfo:      serverInfo{Name: "fake", Version: "test"},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			f.writeResult(w, req.ID, toolsListResult{Tools: f.tools})
		case "tools/call":
			var params toolsCallParams
			if err := json.Unmarshal(req.Params, &params); err != nil {
				t.Errorf("decode tools/call params: %v", err)
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			f.mu.Lock()
			f.called = append(f.called, params.Name)
			f.mu.Unlock()
			f.writeResult(w, req.ID, toolsCallResult{
				Content: []contentBlock{{Type: "text", Text: "ok:" + params.Name}},
			})
		default:
			http.Error(w, fmt.Sprintf("unexpected method %s", req.Method), http.StatusBadRequest)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeRegisterMCP) writeResult(w http.ResponseWriter, id any, result any) {
	raw, _ := json.Marshal(result)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(rpcResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  raw,
	})
}

func (f *fakeRegisterMCP) calledNames() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.called))
	copy(out, f.called)
	return out
}

func TestAdvertisedToolNameDefaultAndDisabled(t *testing.T) {
	spec := ServerSpec{Name: "AMap"}
	if got := advertisedToolName(spec, "poi_search"); got != "AMap_poi_search" {
		t.Fatalf("default advertised name = %q, want prefixed", got)
	}
	enabled := true
	spec.Namespace = &enabled
	if got := advertisedToolName(spec, "poi_search"); got != "AMap_poi_search" {
		t.Fatalf("namespace=true advertised name = %q, want prefixed", got)
	}
	enabled = false
	if got := advertisedToolName(spec, "poi_search"); got != "poi_search" {
		t.Fatalf("namespace=false advertised name = %q, want raw", got)
	}
}

func TestRegisterServerDefaultPrefixesAdvertisedNameButCallsRawTool(t *testing.T) {
	fake := newFakeRegisterMCP(t, []ToolDescriptor{{
		Name:        "poi_search",
		Description: "search points of interest",
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
	}})
	reg := agent.NewRegistry()
	client, names, err := RegisterServer(context.Background(), reg, ServerSpec{
		Name: "AMap",
		URL:  fake.srv.URL,
	}, zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	if len(names) != 1 || names[0] != "AMap_poi_search" {
		t.Fatalf("registered names = %v, want [AMap_poi_search]", names)
	}
	if _, ok := reg.Get("poi_search"); ok {
		t.Fatalf("raw name should not be advertised by default")
	}
	tool, ok := reg.Get("AMap_poi_search")
	if !ok {
		t.Fatalf("prefixed tool not registered")
	}
	got, err := tool.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if got != "ok:poi_search" {
		t.Fatalf("tool result = %q, want raw MCP tool call result", got)
	}
	if calls := fake.calledNames(); len(calls) != 1 || calls[0] != "poi_search" {
		t.Fatalf("MCP called names = %v, want [poi_search]", calls)
	}
}

func TestRegisterServerNamespaceFalseAdvertisesRawName(t *testing.T) {
	fake := newFakeRegisterMCP(t, []ToolDescriptor{{Name: "poi_search"}})
	reg := agent.NewRegistry()
	namespace := false
	client, names, err := RegisterServer(context.Background(), reg, ServerSpec{
		Name:      "AMap",
		Namespace: &namespace,
		URL:       fake.srv.URL,
	}, zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	if len(names) != 1 || names[0] != "poi_search" {
		t.Fatalf("registered names = %v, want [poi_search]", names)
	}
	if _, ok := reg.Get("AMap_poi_search"); ok {
		t.Fatalf("prefixed name should not be advertised when namespace=false")
	}
	if _, ok := reg.Get("poi_search"); !ok {
		t.Fatalf("raw tool name not registered")
	}
}

func TestRegisterAllRejectsNameCollisionAndRollsBack(t *testing.T) {
	first := newFakeRegisterMCP(t, []ToolDescriptor{{Name: "search"}})
	second := newFakeRegisterMCP(t, []ToolDescriptor{{Name: "search"}})
	namespace := false
	reg := agent.NewRegistry()
	clients, err := RegisterAll(context.Background(), reg, []ServerSpec{
		{Name: "One", Namespace: &namespace, URL: first.srv.URL},
		{Name: "Two", Namespace: &namespace, URL: second.srv.URL},
	}, zerolog.Nop())
	if err == nil {
		t.Fatal("expected collision error")
	}
	if clients != nil {
		t.Fatalf("clients = %v, want nil on failure", clients)
	}
	if !strings.Contains(err.Error(), `"search"`) || !strings.Contains(err.Error(), `"One"`) || !strings.Contains(err.Error(), `"Two"`) {
		t.Fatalf("collision error should name tool and both servers, got: %v", err)
	}
	if _, ok := reg.Get("search"); ok {
		t.Fatalf("first server tool should be rolled back after later collision")
	}
}

func TestRegisterServerRejectsCollisionWithExistingRegistryTool(t *testing.T) {
	fake := newFakeRegisterMCP(t, []ToolDescriptor{{Name: "search"}})
	namespace := false
	reg := agent.NewRegistry()
	reg.Add(&agent.Tool{
		Name:        "search",
		Description: "existing",
		Schema:      json.RawMessage(`{"type":"object"}`),
		Execute:     func(context.Context, json.RawMessage) (string, error) { return "existing", nil },
	})

	client, names, err := RegisterServer(context.Background(), reg, ServerSpec{
		Name:      "MCP",
		Namespace: &namespace,
		URL:       fake.srv.URL,
	}, zerolog.Nop())
	if err == nil {
		t.Fatal("expected collision with existing registry tool")
	}
	if client != nil || names != nil {
		t.Fatalf("client=%v names=%v, want nils on failure", client, names)
	}
	tool, ok := reg.Get("search")
	if !ok {
		t.Fatalf("existing tool was removed")
	}
	got, err := tool.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil || got != "existing" {
		t.Fatalf("existing tool changed: got=%q err=%v", got, err)
	}
}
