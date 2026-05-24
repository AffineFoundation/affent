package agenteval

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestTrace_RepairStats_ClassifiesRepairNotes(t *testing.T) {
	tr := Trace{Tools: []ToolCall{
		{
			Tool:          "read_file",
			Canonicalized: true,
			ArgsRepaired:  true,
			RepairNotes: []string{
				"canonicalized tool readFile to read_file",
				"unwrapped field arguments",
				"renamed field file_path to path",
				"normalized enum field action",
				"coerced field max_bytes to integer",
				"coerced field evidence to array",
				"dropped unknown field extra",
			},
		},
		{
			Tool:         "shell",
			ArgsRepaired: true,
			RepairNotes:  []string{"repaired malformed JSON arguments"},
			ExitCode:     1,
		},
		{
			Tool:         "memory",
			ArgsRepaired: true,
			RepairNotes:  []string{"wrapped object arguments as query"},
		},
		{
			Tool:          "session_search",
			Canonicalized: true, // defensive fallback when older traces omit notes
		},
		{
			Tool:         "web_fetch",
			ArgsRepaired: true, // defensive fallback when older traces omit notes
		},
		{Tool: "read_file"},
	}}

	got := tr.RepairStats()
	if got.Calls != 5 {
		t.Fatalf("Calls = %d, want 5", got.Calls)
	}
	if got.SucceededCalls != 4 || got.FailedCalls != 1 {
		t.Fatalf("repair outcomes = ok:%d failed:%d, want 4/1", got.SucceededCalls, got.FailedCalls)
	}
	if got.Notes != 11 {
		t.Fatalf("Notes = %d, want 11; by_kind=%#v", got.Notes, got.ByKind)
	}
	want := map[string]int{
		"tool_name":          2,
		"wrapper_unwrap":     1,
		"alias_rename":       1,
		"enum_normalization": 1,
		"type_coercion":      2,
		"unknown_field_drop": 1,
		"malformed_json":     2,
		"scalar_wrap":        1,
	}
	if !reflect.DeepEqual(got.ByKind, want) {
		t.Fatalf("ByKind = %#v, want %#v", got.ByKind, want)
	}
	if !got.HasAny() {
		t.Fatal("HasAny() = false, want true")
	}
}

func TestTrace_RepairStats_EmptyTraceKeepsMapsNil(t *testing.T) {
	got := Trace{}.RepairStats()
	if got.HasAny() {
		t.Fatalf("empty RepairStats should report no activity: %+v", got)
	}
	if got.ByKind != nil {
		t.Fatalf("empty RepairStats should keep ByKind nil, got %#v", got.ByKind)
	}
}

func TestToolRepairStats_HasAnyIncludesPartialRuntimeFields(t *testing.T) {
	for _, got := range []ToolRepairStats{
		{SucceededCalls: 1},
		{FailedCalls: 1},
		{ByKind: map[string]int{"alias_rename": 1}},
	} {
		if !got.HasAny() {
			t.Fatalf("HasAny() = false for partial stats %+v", got)
		}
	}
}

func TestTrace_RepairStats_PrefersTurnEndToolStats(t *testing.T) {
	tr := Trace{
		ToolStats: ToolRuntimeStats{
			ToolRepairCalls:     2,
			ToolRepairSucceeded: 1,
			ToolRepairFailed:    1,
			ToolRepairNotes:     4,
			ToolRepairByKind:    map[string]int{"alias_rename": 3, "type_coercion": 1},
		},
		Tools: []ToolCall{
			{
				Tool:          "read_file",
				Canonicalized: true,
				RepairNotes:   []string{"canonicalized tool readFile to read_file"},
			},
		},
	}
	got := tr.RepairStats()
	if got.Calls != 2 || got.SucceededCalls != 1 || got.FailedCalls != 1 || got.Notes != 4 {
		t.Fatalf("RepairStats = %+v, want runtime turn stats", got)
	}
	want := map[string]int{"alias_rename": 3, "type_coercion": 1}
	if !reflect.DeepEqual(got.ByKind, want) {
		t.Fatalf("ByKind = %#v, want %#v", got.ByKind, want)
	}
	got.ByKind["alias_rename"] = 99
	if tr.ToolStats.ToolRepairByKind["alias_rename"] != 3 {
		t.Fatalf("RepairStats must not expose trace ToolRepairByKind map: %#v", tr.ToolStats.ToolRepairByKind)
	}
}

func TestTrace_RepairStats_UsesRequestOutcomesWhenTurnStatsOnlyHaveKinds(t *testing.T) {
	tr := Trace{
		ToolStats: ToolRuntimeStats{
			ToolRepairNotes:  2,
			ToolRepairByKind: map[string]int{"alias_rename": 2},
		},
		Tools: []ToolCall{
			{Tool: "read_file", ArgsRepaired: true, ExitCode: 0},
			{Tool: "shell", ArgsRepaired: true, ExitCode: 1},
		},
	}
	got := tr.RepairStats()
	if got.Calls != 2 || got.SucceededCalls != 1 || got.FailedCalls != 1 {
		t.Fatalf("repair outcomes = calls:%d ok:%d failed:%d, want request-derived 2/1/1", got.Calls, got.SucceededCalls, got.FailedCalls)
	}
	if got.Notes != 2 || got.ByKind["alias_rename"] != 2 {
		t.Fatalf("repair notes/kinds = %+v, want runtime turn stats", got)
	}
}

func TestParseTraceFile_ComputesRepairStatsFromRequestNotes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trace.jsonl")
	body := `{"type":"trace.meta","data":{"schema_version":1}}` + "\n" +
		`{"type":"tool.request","data":{"turn_id":"t1","call_id":"c1","tool":"read_file","args":{"path":"README.md"},"canonicalized":true,"args_repaired":true,"repair_notes":["canonicalized tool readFile to read_file","renamed field file_path to path"]}}` + "\n" +
		`{"type":"tool.result","data":{"call_id":"c1","exit_code":0,"result":"ok"}}` + "\n" +
		`{"type":"turn.end","data":{"turn_id":"t1","reason":"completed"}}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	trace, err := ParseTraceFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := trace.RepairStats()
	if got.Calls != 1 || got.Notes != 2 {
		t.Fatalf("RepairStats = %+v, want one call/two notes", got)
	}
	if got.ByKind["tool_name"] != 1 || got.ByKind["alias_rename"] != 1 {
		t.Fatalf("ByKind = %#v", got.ByKind)
	}
}
