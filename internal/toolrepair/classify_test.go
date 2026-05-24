package toolrepair

import "testing"

func TestKind(t *testing.T) {
	tests := []struct {
		note string
		want string
	}{
		{"", ""},
		{"canonicalized tool readFile to read_file", "tool_name"},
		{"repaired malformed JSON arguments", "malformed_json"},
		{"unwrapped field arguments from JSON string", "wrapper_unwrap"},
		{"wrapped arguments as path", "scalar_wrap"},
		{"wrapped object arguments as query", "scalar_wrap"},
		{"renamed field file_path to path", "alias_rename"},
		{"filled required field path from alias file", "alias_rename"},
		{"coerced field max_bytes from string to integer", "type_coercion"},
		{"dropped unknown field foo", "unknown_field_drop"},
		{"some new repair note", "other"},
	}
	for _, tt := range tests {
		if got := Kind(tt.note); got != tt.want {
			t.Fatalf("Kind(%q) = %q, want %q", tt.note, got, tt.want)
		}
	}
}
