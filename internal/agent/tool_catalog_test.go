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

func TestRegistryWithoutNarrowsCopyWithoutMutatingBase(t *testing.T) {
	reg := NewRegistry()
	reg.Add(&Tool{Name: "shell", Description: "shell", Schema: json.RawMessage(`{"type":"object"}`)})
	reg.Add(&Tool{Name: LoopProtocolToolName, Description: "loop", Schema: json.RawMessage(`{"type":"object"}`)})
	reg.Add(&Tool{Name: PlanToolName, Description: "plan", Schema: json.RawMessage(`{"type":"object"}`)})

	narrowed := reg.Without(LoopProtocolToolName)
	if _, ok := narrowed.Get(LoopProtocolToolName); ok {
		t.Fatalf("narrowed registry still contains %s", LoopProtocolToolName)
	}
	if _, ok := narrowed.Get("shell"); !ok {
		t.Fatal("narrowed registry dropped unrelated shell tool")
	}
	if _, ok := reg.Get(LoopProtocolToolName); !ok {
		t.Fatalf("base registry lost %s", LoopProtocolToolName)
	}
	if got, want := len(narrowed.Catalog()), 2; got != want {
		t.Fatalf("narrowed catalog length = %d, want %d", got, want)
	}
}

func TestRegistrySelectModelToolsAppliesSchemaBudgetByModelRank(t *testing.T) {
	reg := NewRegistry()
	reg.Add(&Tool{Name: "shell", Description: "run commands", Schema: json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"}}}`)})
	reg.Add(&Tool{Name: "read_file", Description: "read files", Schema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`)})
	reg.Add(&Tool{Name: PlanToolName, Description: "manage plan", Schema: json.RawMessage(`{"type":"object","properties":{"action":{"type":"string"}}}`)})
	reg.Add(&Tool{Name: "web_fetch", Description: "fetch web", Schema: json.RawMessage(`{"type":"object","properties":{"url":{"type":"string"}}}`)})

	all := reg.SelectModelTools(ToolSurfacePolicy{})
	if len(all.Defs) != 4 || all.SchemaBudgetTokens != 0 || len(all.ExcludedCatalog) != 0 {
		t.Fatalf("unbudgeted selection = %+v, want all tools without exclusions", all)
	}
	budget := EstimateRequestInput(nil, all.Defs[:2]).ToolSchemaTokens
	got := reg.SelectModelTools(ToolSurfacePolicy{SchemaTokenBudget: budget})
	names := make([]string, 0, len(got.Defs))
	for _, def := range got.Defs {
		names = append(names, def.Function.Name)
	}
	if !reflect.DeepEqual(names, []string{PlanToolName, "read_file"}) {
		t.Fatalf("budgeted model tools = %#v, want plan/read_file", names)
	}
	if got.AvailableCount != 4 || len(got.Catalog) != 2 || len(got.ExcludedCatalog) != 2 {
		t.Fatalf("budgeted counts = available:%d catalog:%d excluded:%d", got.AvailableCount, len(got.Catalog), len(got.ExcludedCatalog))
	}
	if got.SchemaBudgetTokens != budget || got.SchemaTokens > budget {
		t.Fatalf("budget/tokens = %d/%d, want <= %d", got.SchemaTokens, got.SchemaBudgetTokens, budget)
	}
	if got.Catalog[0].Name != "read_file" || got.Catalog[1].Name != PlanToolName {
		t.Fatalf("catalog order = %#v, want selected tools in registration order", got.Catalog)
	}
}
