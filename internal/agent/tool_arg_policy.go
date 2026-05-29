package agent

// ToolArgPolicy describes runtime-relevant argument semantics that are shared
// by tools, evals, and operator surfaces. It is deliberately separate from the
// model-facing JSON schema: schemas validate shape, while policies describe how
// Affent should reason about an argument after the model emits it.
type ToolArgPolicy struct {
	WorkspacePathArgs []string `json:"workspace_path_args,omitempty"`
}

var toolArgPolicies = map[string]ToolArgPolicy{
	"shell": {
		WorkspacePathArgs: []string{"command", "cwd"},
	},
	"read_file": {
		WorkspacePathArgs: []string{"path"},
	},
	"write_file": {
		WorkspacePathArgs: []string{"path"},
	},
	"edit_file": {
		WorkspacePathArgs: []string{"path"},
	},
	"list_files": {
		WorkspacePathArgs: []string{"path"},
	},
	"file_context": {
		WorkspacePathArgs: []string{"path"},
	},
	"repo_search": {
		WorkspacePathArgs: []string{"path"},
	},
	SymbolContextToolName: {
		WorkspacePathArgs: []string{"path"},
	},
	SessionWorkspaceToolName: {
		WorkspacePathArgs: []string{"path"},
	},
	"browser_screenshot": {
		WorkspacePathArgs: []string{"save_path"},
	},
}

// ToolArgPolicyForName returns the shared argument policy for a registered tool
// name. The returned slices are copies so callers can safely retain or mutate
// them.
func ToolArgPolicyForName(toolName string) (ToolArgPolicy, bool) {
	policy, ok := toolArgPolicies[toolName]
	if !ok {
		return ToolArgPolicy{}, false
	}
	return cloneToolArgPolicy(policy), true
}

// WorkspacePathArgNames returns the argument names whose values can refer to
// the active workspace for the named tool.
func WorkspacePathArgNames(toolName string) []string {
	policy, ok := ToolArgPolicyForName(toolName)
	if !ok {
		return nil
	}
	return policy.WorkspacePathArgs
}

func cloneToolArgPolicy(policy ToolArgPolicy) ToolArgPolicy {
	out := ToolArgPolicy{}
	if len(policy.WorkspacePathArgs) > 0 {
		out.WorkspacePathArgs = append([]string(nil), policy.WorkspacePathArgs...)
	}
	return out
}

func (policy ToolArgPolicy) empty() bool {
	return len(policy.WorkspacePathArgs) == 0
}
