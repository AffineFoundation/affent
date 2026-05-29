package agenteval

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestTrace_RepairStats_ClassifiesRepairNotes(t *testing.T) {
	tr := Trace{Tools: []ToolCall{
		{
			CallID:              "repair-1",
			Tool:                "read_file",
			OriginalTool:        "readFile",
			Canonicalized:       true,
			ArgsRepaired:        true,
			OriginalArgsSummary: `{"file_path":"README.md","extra":true}`,
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
			Tool:         "plan",
			ArgsRepaired: true,
			RepairNotes:  []string{"inferred missing action=update for plan from structured fields"},
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
	if got.Calls != 6 {
		t.Fatalf("Calls = %d, want 6", got.Calls)
	}
	if got.SucceededCalls != 5 || got.FailedCalls != 1 {
		t.Fatalf("repair outcomes = ok:%d failed:%d, want 5/1", got.SucceededCalls, got.FailedCalls)
	}
	if got.Notes != 12 {
		t.Fatalf("Notes = %d, want 12; by_kind=%#v", got.Notes, got.ByKind)
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
		"action_inference":   1,
	}
	if !reflect.DeepEqual(got.ByKind, want) {
		t.Fatalf("ByKind = %#v, want %#v", got.ByKind, want)
	}
	if !got.HasAny() {
		t.Fatal("HasAny() = false, want true")
	}
	examples := tr.ToolRepairExamples(2)
	if len(examples) != 2 {
		t.Fatalf("ToolRepairExamples len = %d, want 2: %+v", len(examples), examples)
	}
	if examples[0].CallID != "repair-1" ||
		examples[0].Tool != "read_file" ||
		examples[0].OriginalTool != "readFile" ||
		!examples[0].Canonicalized ||
		!examples[0].ArgsRepaired ||
		!reflect.DeepEqual(examples[0].RepairKinds, []string{"tool_name", "wrapper_unwrap", "alias_rename", "enum_normalization", "type_coercion", "unknown_field_drop"}) ||
		!strings.Contains(examples[0].OriginalArgsSummary, "file_path") {
		t.Fatalf("ToolRepairExamples[0] = %+v", examples[0])
	}
	if examples[1].Tool != "shell" || !examples[1].ArgsRepaired || examples[1].Succeeded {
		t.Fatalf("ToolRepairExamples[1] = %+v", examples[1])
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
