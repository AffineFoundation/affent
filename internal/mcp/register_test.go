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
				ProtocolVersion: ProtocolVersion,
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

func TestRegisterServerRejectsInvalidAdvertisedToolNames(t *testing.T) {
	longName := strings.Repeat("a", maxToolNameBytes+1)
	cases := []struct {
		name       string
		serverName string
		toolName   string
		namespace  bool
		want       string
	}{
		{name: "blank raw name", serverName: "MCP", toolName: "", namespace: false, want: "empty name"},
		{name: "space in raw name", serverName: "MCP", toolName: "bad name", namespace: false, want: "may contain only ASCII"},
		{name: "slash in prefixed name", serverName: "MCP", toolName: "bad/name", namespace: true, want: "may contain only ASCII"},
		{name: "too long", serverName: "MCP", toolName: longName, namespace: false, want: "max"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := newFakeRegisterMCP(t, []ToolDescriptor{{Name: tc.toolName}})
			reg := agent.NewRegistry()
			namespace := tc.namespace
			client, names, err := RegisterServer(context.Background(), reg, ServerSpec{
				Name:      tc.serverName,
				Namespace: &namespace,
				URL:       fake.srv.URL,
			}, zerolog.Nop())
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("RegisterServer error = %v, want contains %q", err, tc.want)
			}
			if client != nil || names != nil {
				t.Fatalf("client=%v names=%v, want nils on invalid tool name", client, names)
			}
		})
	}
}

func TestRegisterServerTruncatesLongDescriptions(t *testing.T) {
	desc := strings.Repeat("你", maxToolDescriptionBytes)
	fake := newFakeRegisterMCP(t, []ToolDescriptor{{
		Name:        "describe",
		Description: desc,
	}})
	reg := agent.NewRegistry()
	client, _, err := RegisterServer(context.Background(), reg, ServerSpec{
		Name: "MCP",
		URL:  fake.srv.URL,
	}, zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	tool, ok := reg.Get("MCP_describe")
	if !ok {
		t.Fatalf("tool not registered")
	}
	if len(tool.Description) > maxToolDescriptionBytes {
		t.Fatalf("description length = %d, want <= %d", len(tool.Description), maxToolDescriptionBytes)
	}
	if !strings.Contains(tool.Description, "[truncated]") {
		t.Fatalf("truncated description missing marker: %q", tool.Description[max(0, len(tool.Description)-80):])
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

func TestRegisterServerDefaultsEmptyInputSchema(t *testing.T) {
	fake := newFakeRegisterMCP(t, []ToolDescriptor{{Name: "ping"}})
	reg := agent.NewRegistry()
	client, names, err := RegisterServer(context.Background(), reg, ServerSpec{
		Name: "MCP",
		URL:  fake.srv.URL,
	}, zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if len(names) != 1 || names[0] != "MCP_ping" {
		t.Fatalf("registered names = %v, want [MCP_ping]", names)
	}
	tool, ok := reg.Get("MCP_ping")
	if !ok {
		t.Fatalf("tool not registered")
	}
	if string(tool.Schema) != `{"type":"object","properties":{}}` {
		t.Fatalf("default schema = %s", tool.Schema)
	}
}

func TestRegisterServerFiltersAllowedTools(t *testing.T) {
	fake := newFakeRegisterMCP(t, []ToolDescriptor{
		{Name: "search"},
		{Name: "geocode"},
		{Name: "admin_delete"},
	})
	reg := agent.NewRegistry()
	client, names, err := RegisterServer(context.Background(), reg, ServerSpec{
		Name:          "maps",
		URL:           fake.srv.URL,
		ToolAllowlist: []string{"search", "geocode"},
	}, zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if strings.Join(names, ",") != "maps_search,maps_geocode" {
		t.Fatalf("registered names = %v, want only allowed tools", names)
	}
	if _, ok := reg.Get("maps_admin_delete"); ok {
		t.Fatalf("denied-by-allowlist tool should not be registered")
	}
}

func TestRegisterServerDenylistRunsBeforeAdvertisedNameValidation(t *testing.T) {
	fake := newFakeRegisterMCP(t, []ToolDescriptor{
		{Name: "search"},
		{Name: "bad/name"},
	})
	reg := agent.NewRegistry()
	client, names, err := RegisterServer(context.Background(), reg, ServerSpec{
		Name:         "maps",
		URL:          fake.srv.URL,
		ToolDenylist: []string{"bad/name"},
	}, zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if len(names) != 1 || names[0] != "maps_search" {
		t.Fatalf("registered names = %v, want only maps_search", names)
	}
	if _, ok := reg.Get("maps_bad/name"); ok {
		t.Fatalf("denylisted invalid-name tool should not be registered")
	}
}

func TestRegisterServerRejectsInvalidToolFilters(t *testing.T) {
	for _, tc := range []struct {
		name string
		spec ServerSpec
		want string
	}{
		{name: "blank allow", spec: ServerSpec{ToolAllowlist: []string{" "}}, want: "allow_tools values must not be empty"},
		{name: "duplicate deny", spec: ServerSpec{ToolDenylist: []string{"search", "search"}}, want: "deny_tools contains duplicate"},
		{name: "allow unknown", spec: ServerSpec{ToolAllowlist: []string{"missing"}}, want: "allow_tools references unknown tool"},
		{name: "deny unknown", spec: ServerSpec{ToolDenylist: []string{"missing"}}, want: "deny_tools references unknown tool"},
		{name: "allow deny overlap", spec: ServerSpec{ToolAllowlist: []string{"search"}, ToolDenylist: []string{"search"}}, want: "both allow_tools and deny_tools"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := newFakeRegisterMCP(t, []ToolDescriptor{{Name: "search"}})
			reg := agent.NewRegistry()
			spec := tc.spec
			spec.Name = "maps"
			spec.URL = fake.srv.URL
			client, names, err := RegisterServer(context.Background(), reg, spec, zerolog.Nop())
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("RegisterServer error = %v, want contains %q", err, tc.want)
			}
			if client != nil || names != nil {
				t.Fatalf("client=%v names=%v, want nils on filter failure", client, names)
			}
			if _, ok := reg.Get("maps_search"); ok {
				t.Fatalf("filter failure should not register tools")
			}
		})
	}
}

func TestRegisterServerRejectsNoUsableTools(t *testing.T) {
	for _, tc := range []struct {
		name  string
		tools []ToolDescriptor
		spec  ServerSpec
	}{
		{name: "server exports none", tools: nil},
		{name: "deny filters all", tools: []ToolDescriptor{{Name: "search"}}, spec: ServerSpec{ToolDenylist: []string{"search"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := newFakeRegisterMCP(t, tc.tools)
			reg := agent.NewRegistry()
			spec := tc.spec
			spec.Name = "maps"
			spec.URL = fake.srv.URL
			client, names, err := RegisterServer(context.Background(), reg, spec, zerolog.Nop())
			if err == nil || !strings.Contains(err.Error(), "exposes no usable tools") {
				t.Fatalf("RegisterServer error = %v, want no-usable-tools error", err)
			}
			if client != nil || names != nil {
				t.Fatalf("client=%v names=%v, want nils on no-usable-tools failure", client, names)
			}
			if len(reg.Defs()) != 0 {
				t.Fatalf("no-usable-tools failure should not register tools: %+v", reg.Defs())
			}
		})
	}
}

func TestRegisterServerRejectsInvalidInputSchemas(t *testing.T) {
	cases := []struct {
		name   string
		schema json.RawMessage
		want   string
	}{
		{name: "non object", schema: json.RawMessage(`[]`), want: "inputSchema is not valid JSON"},
		{name: "null", schema: json.RawMessage(`null`), want: "must be a JSON object"},
		{name: "too large", schema: json.RawMessage(`{"description":"` + strings.Repeat("x", maxInputSchemaBytes) + `"}`), want: "max"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := newFakeRegisterMCP(t, []ToolDescriptor{{
				Name:        "bad",
				InputSchema: tc.schema,
			}})
			reg := agent.NewRegistry()
			client, names, err := RegisterServer(context.Background(), reg, ServerSpec{
				Name: "MCP",
				URL:  fake.srv.URL,
			}, zerolog.Nop())
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("RegisterServer error = %v, want contains %q", err, tc.want)
			}
			if client != nil || names != nil {
				t.Fatalf("client=%v names=%v, want nils on invalid schema", client, names)
			}
			if _, ok := reg.Get("MCP_bad"); ok {
				t.Fatalf("invalid schema tool should not be registered")
			}
		})
	}
}

func TestNormalizedInputSchemaRejectsMalformedJSON(t *testing.T) {
	for _, tc := range []struct {
		name   string
		schema json.RawMessage
		want   string
	}{
		{name: "invalid json", schema: json.RawMessage(`{`), want: "not valid JSON"},
		{name: "trailing json", schema: json.RawMessage(`{"type":"object"} {"type":"object"}`), want: "trailing JSON"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := normalizedInputSchema("MCP", "bad", tc.schema)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("normalizedInputSchema error = %v, want contains %q", err, tc.want)
			}
		})
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
