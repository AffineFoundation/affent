package agent

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestRegistryCatalogGroupsByToolSource(t *testing.T) {
	reg := NewRegistry()
	reg.Add(&Tool{Name: "read_file", Description: "Read a file", Schema: json.RawMessage(`{"type":"object"}`)})
	reg.Add(&Tool{Name: "web_fetch", Description: "Fetch a URL", Schema: json.RawMessage(`{"type":"object"}`)})
	reg.Add(&Tool{
		Name:           "taostats_query",
		Description:    "Query TAO stats",
		Schema:         json.RawMessage(`{"type":"object"}`),
		CatalogGroup:   "MCP",
		CatalogSource:  "taostats",
		CatalogRawName: "query",
	})

	catalog := reg.Catalog()
	if len(catalog) != 3 {
		t.Fatalf("catalog length = %d, want 3", len(catalog))
	}
	if catalog[0].Group != "Workspace" {
		t.Fatalf("first tool group = %q, want Workspace", catalog[0].Group)
	}
	if catalog[1].Group != "Research" {
		t.Fatalf("second tool group = %q, want Research", catalog[1].Group)
	}
	if catalog[2].Group != "MCP" || catalog[2].Source != "taostats" || catalog[2].RawName != "query" {
		t.Fatalf("mcp catalog entry = %+v, want MCP/taostats/query", catalog[2])
	}
	if catalog[0].ArgPolicy == nil || !reflect.DeepEqual(catalog[0].ArgPolicy.WorkspacePathArgs, []string{"path"}) {
		t.Fatalf("read_file arg policy = %+v, want workspace path", catalog[0].ArgPolicy)
	}
	if catalog[1].ArgPolicy != nil {
		t.Fatalf("web_fetch arg policy = %+v, want nil", catalog[1].ArgPolicy)
	}
}

func TestRegistryModelDefsPrioritizeDurableControlTools(t *testing.T) {
	reg := NewRegistry()
	for _, name := range []string{
		"shell",
		"write_file",
		"read_file",
		SkillToolName,
		PlanToolName,
		LoopProtocolToolName,
		SessionScheduleToolName,
		MemoryToolName,
		SessionSearchToolName,
		"web_fetch",
	} {
		reg.Add(&Tool{Name: name, Description: name, Schema: json.RawMessage(`{"type":"object"}`)})
	}

	defs := reg.ModelDefs()
	got := make([]string, 0, len(defs))
	for _, def := range defs {
		got = append(got, def.Function.Name)
	}
	wantPrefix := []string{
		PlanToolName,
		SessionScheduleToolName,
		LoopProtocolToolName,
		MemoryToolName,
		SessionSearchToolName,
		SkillToolName,
	}
	if !reflect.DeepEqual(got[:len(wantPrefix)], wantPrefix) {
		t.Fatalf("model tool prefix = %#v, want %#v (full order %#v)", got[:len(wantPrefix)], wantPrefix, got)
	}

	catalog := reg.Catalog()
	if catalog[0].Name != "shell" || catalog[1].Name != "write_file" {
		t.Fatalf("catalog order = %#v, want registration order preserved", catalog[:2])
	}
}
