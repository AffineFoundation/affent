package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"runtime/debug"
	"sort"
	"strings"
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

func (r *Registry) canonicalName(name string) (string, bool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if _, ok := r.tools[name]; ok {
		return name, true, false
	}
	want := normalizeToolIdentifier(name)
	if want == "" {
		return name, false, false
	}
	canonical := ""
	for _, toolName := range r.order {
		if normalizeToolIdentifier(toolName) != want {
			continue
		}
		if canonical != "" {
			return name, false, false
		}
		canonical = toolName
	}
	if canonical == "" {
		return name, false, false
	}
	return canonical, true, true
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

func (r *Registry) suggestions(name string, limit int) []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	type candidate struct {
		name     string
		distance int
		rank     int
	}
	candidates := make([]candidate, 0, len(r.order))
	lowerName := strings.ToLower(name)
	for rank, toolName := range r.order {
		lowerTool := strings.ToLower(toolName)
		distance := levenshtein(lowerName, lowerTool)
		if strings.Contains(lowerTool, lowerName) || strings.Contains(lowerName, lowerTool) {
			distance = 0
		}
		if distance <= 3 {
			candidates = append(candidates, candidate{name: toolName, distance: distance, rank: rank})
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].distance != candidates[j].distance {
			return candidates[i].distance < candidates[j].distance
		}
		return candidates[i].rank < candidates[j].rank
	})
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}
	out := make([]string, 0, len(candidates))
	for _, c := range candidates {
		out = append(out, c.name)
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
func (r *Registry) dispatch(ctx context.Context, name string, args json.RawMessage) (out string, isErr bool) {
	canonical, ok, _ := r.canonicalName(name)
	if !ok {
		msg := fmt.Sprintf("Error: tool %q is not available", name)
		if suggestions := r.suggestions(name, 3); len(suggestions) > 0 {
			msg += fmt.Sprintf(". Did you mean: %s?\nNext: retry once with one of those exact tool names, or choose a different available tool.", strings.Join(suggestions, ", "))
		} else {
			msg += "\nNext: choose a tool from the advertised tool list exactly as named; do not call this unavailable tool again."
		}
		return msg, true
	}
	t, _ := r.Get(canonical)
	// Recover from a panicking tool. Without this a buggy third-party
	// tool brings down the entire process — the runTurn goroutine
	// crashes, Go's runtime tears down every other goroutine in
	// affentserve, and every concurrent client loses their session.
	// Convert the panic into a tool error so the model sees it and
	// can recover (try a different approach, surface the failure to
	// the user), and log the stack so operators can root-cause.
	defer func() {
		if rec := recover(); rec != nil {
			log.Printf("affent: tool %q panicked: %v\n%s", canonical, rec, debug.Stack())
			out = fmt.Sprintf("Error: tool %q panicked: %v", canonical, rec)
			isErr = true
		}
	}()
	res, err := t.Execute(ctx, args)
	if err != nil {
		help := toolDispatchErrorHelp(t, args, err, res)
		if res != "" {
			return fmt.Sprintf("Error: %s\n%s%s", err, res, help), true
		}
		return fmt.Sprintf("Error: %s%s", err, help), true
	}
	return res, false
}

func toolDispatchErrorHelp(t *Tool, args json.RawMessage, err error, res string) string {
	help := toolErrorHelp(t, args)
	if help != "" || toolErrorAlreadyGuided(err, res) {
		return help
	}
	return "\nNext: read the error, change the arguments or choose a different tool; do not repeat the same failing call unchanged."
}

func toolErrorAlreadyGuided(err error, res string) bool {
	if err != nil && strings.Contains(err.Error(), "Next:") {
		return true
	}
	return strings.Contains(res, "Next:")
}

func levenshtein(a, b string) int {
	ar := []rune(a)
	br := []rune(b)
	if len(ar) == 0 {
		return len(br)
	}
	if len(br) == 0 {
		return len(ar)
	}
	prev := make([]int, len(br)+1)
	curr := make([]int, len(br)+1)
	for j := range prev {
		prev[j] = j
	}
	for i, ra := range ar {
		curr[0] = i + 1
		for j, rb := range br {
			cost := 0
			if ra != rb {
				cost = 1
			}
			curr[j+1] = min(prev[j+1]+1, curr[j]+1, prev[j]+cost)
		}
		prev, curr = curr, prev
	}
	return prev[len(br)]
}

func normalizeToolIdentifier(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + ('a' - 'A'))
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		}
	}
	return b.String()
}
