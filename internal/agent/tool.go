package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
)

// Tool is the in-process handler the loop dispatches to when the model
// emits a tool_call. The schema is JSON Schema served verbatim to the
// model alongside the tool name and description.
type Tool struct {
	Name        string
	Description string
	Schema      json.RawMessage // JSON Schema for the function arguments
	Execute     func(ctx context.Context, args json.RawMessage) (string, error)
}

// ToolDef returns the OpenAI-shape definition the LLM sees.
func (t *Tool) AsDef() ToolDef {
	d := ToolDef{Type: "function"}
	d.Function.Name = t.Name
	d.Function.Description = t.Description
	d.Function.Parameters = t.Schema
	return d
}

// Registry is a name -> Tool map shared by a session loop. Safe for
// concurrent reads after construction.
type Registry struct {
	mu    sync.RWMutex
	tools map[string]*Tool
	order []string // preserve insertion order for stable tool list to model
}

func NewRegistry() *Registry {
	return &Registry{tools: map[string]*Tool{}}
}

func (r *Registry) Add(t *Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.tools[t.Name]; !ok {
		r.order = append(r.order, t.Name)
	}
	r.tools[t.Name] = t
}

// Remove unregisters the tool named name. Returns true if the tool
// was present. Useful when an extension that adds several tools (e.g.
// an MCP server) needs to roll back after a partial-success failure;
// without this, dangling entries point at backends the caller has
// already torn down.
func (r *Registry) Remove(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.tools[name]; !ok {
		return false
	}
	delete(r.tools, name)
	for i, n := range r.order {
		if n == name {
			r.order = append(r.order[:i], r.order[i+1:]...)
			break
		}
	}
	return true
}

func (r *Registry) Get(name string) (*Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

func (r *Registry) Defs() []ToolDef {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]ToolDef, 0, len(r.order))
	for _, n := range r.order {
		out = append(out, r.tools[n].AsDef())
	}
	return out
}

// dispatch runs the tool and returns (result string, isErr). Errors are
// folded into the result string with an "Error: " prefix so the model
// sees them and can recover — most LLMs handle "Error: ..." better
// than a separate error channel. isErr is the structured signal callers
// need so they don't have to prefix-sniff the result: a tool that
// legitimately echoes "Error:" in its stdout (e.g. `echo Error: ...`
// from a passing test) is success, not failure.
func (r *Registry) dispatch(ctx context.Context, name string, args json.RawMessage) (string, bool) {
	t, ok := r.Get(name)
	if !ok {
		return fmt.Sprintf("Error: tool %q is not available", name), true
	}
	res, err := t.Execute(ctx, args)
	if err != nil {
		if res != "" {
			return fmt.Sprintf("Error: %s\n%s", err, res), true
		}
		return fmt.Sprintf("Error: %s", err), true
	}
	return res, false
}
