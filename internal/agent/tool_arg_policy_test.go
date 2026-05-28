package agent

import (
	"reflect"
	"testing"
)

func TestWorkspacePathArgNames(t *testing.T) {
	tests := []struct {
		tool string
		want []string
	}{
		{tool: "shell", want: []string{"command", "cwd"}},
		{tool: "read_file", want: []string{"path"}},
		{tool: "write_file", want: []string{"path"}},
		{tool: "edit_file", want: []string{"path"}},
		{tool: "list_files", want: []string{"path"}},
		{tool: "file_context", want: []string{"path"}},
		{tool: "repo_search", want: []string{"path"}},
		{tool: SymbolContextToolName, want: []string{"path"}},
		{tool: "browser_screenshot", want: []string{"save_path"}},
		{tool: "web_fetch", want: nil},
	}

	for _, tt := range tests {
		got := WorkspacePathArgNames(tt.tool)
		if !reflect.DeepEqual(got, tt.want) {
			t.Fatalf("WorkspacePathArgNames(%q) = %#v, want %#v", tt.tool, got, tt.want)
		}
	}
}

func TestToolArgPolicyForNameReturnsCopy(t *testing.T) {
	policy, ok := ToolArgPolicyForName("read_file")
	if !ok {
		t.Fatal("read_file policy missing")
	}
	policy.WorkspacePathArgs[0] = "mutated"

	got := WorkspacePathArgNames("read_file")
	if !reflect.DeepEqual(got, []string{"path"}) {
		t.Fatalf("mutating returned policy changed registry: %#v", got)
	}
}
