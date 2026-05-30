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

// SessionScheduleToolName is the optional serve-runtime tool for future or
// recurring scheduled session turns. The implementation lives in affentserve,
// but the name is shared so traces and capability summaries do not drift.
const SessionScheduleToolName = "session_schedule"

// SessionScheduleKindLoopTick is the structured schedule kind that advances
// the long-running loop protocol control plane. Other schedule kinds are
// ordinary timers/check-ins and should not receive loop-protocol control scope.
const SessionScheduleKindLoopTick = "loop_tick"

// SessionWorkspaceToolName is the optional runtime tool that lets a session
// inspect or switch its active workspace within the configured root.
const SessionWorkspaceToolName = "session_workspace"

// Tool is the in-process handler the loop dispatches to when the model
// emits a tool_call. The schema is JSON Schema served verbatim to the
// model alongside the tool name and description.
type Tool struct {
	Name                  string
	Description           string
	Schema                json.RawMessage // JSON Schema for the function arguments
	NormalizeArgs         func(args json.RawMessage) (json.RawMessage, bool, []string)
	Execute               func(ctx context.Context, args json.RawMessage) (string, error)
	RuntimeSurfaceRefresh func(args json.RawMessage, result string, isErr bool) string
	CatalogGroup          string
	CatalogSource         string
	CatalogRawName        string
}

// ToolCatalogEntry is the read-only catalog shape exposed to the UI.
// It keeps the model-facing tool name, the original/raw name when one
// exists, and enough source metadata to group by category/server.
type ToolCatalogEntry struct {
	Name        string          `json:"name"`
	RawName     string          `json:"raw_name,omitempty"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
	Group       string          `json:"group"`
	Source      string          `json:"source,omitempty"`
	ArgPolicy   *ToolArgPolicy  `json:"arg_policy,omitempty"`
}

// ToolDef returns the OpenAI-shape definition the LLM sees.
func (t *Tool) AsDef() ToolDef {
	d := ToolDef{Type: "function"}
	d.Function.Name = t.Name
	d.Function.Description = t.Description
	d.Function.Parameters = t.Schema
	return d
}

func (t *Tool) CatalogEntry() ToolCatalogEntry {
	entry := ToolCatalogEntry{
		Name:        t.Name,
		RawName:     t.catalogRawName(),
		Description: t.Description,
		Parameters:  t.Schema,
		Group:       t.catalogGroup(),
		Source:      strings.TrimSpace(t.CatalogSource),
	}
	if policy, ok := ToolArgPolicyForName(t.Name); ok && !policy.empty() {
		entry.ArgPolicy = &policy
	}
	return entry
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

// Without returns a read-only-equivalent registry copy with the named tools
// omitted. Tool handlers are shared because tools are immutable after
// registration; callers use this to narrow a single turn's runtime surface.
func (r *Registry) Without(names ...string) *Registry {
	if r == nil {
		return nil
	}
	omit := map[string]bool{}
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name != "" {
			omit[name] = true
		}
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := NewRegistry()
	for _, name := range r.order {
		if omit[name] {
			continue
		}
		out.Add(r.tools[name])
	}
	return out
}

func (r *Registry) canonicalName(name string) (string, bool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if _, ok := r.tools[name]; ok {
		return name, true, false
	}
	want := toolIdentifierKeys(name)
	if len(want) == 0 {
		return name, false, false
	}
	canonical := ""
	for _, toolName := range r.order {
		if !toolIdentifierSetsOverlap(want, canonicalToolIdentifierKeys(toolName)) {
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

// ToolSurfacePolicy constrains the model-visible tool surface for one request.
// It is intentionally budget-shaped rather than task-keyword-shaped: callers
// decide the request budget, Registry applies the existing modelToolRank order.
type ToolSurfacePolicy struct {
	SchemaTokenBudget int
}

// ToolSurfaceSelection is the concrete model-visible tool surface for a turn.
type ToolSurfaceSelection struct {
	Defs               []ToolDef
	Catalog            []ToolCatalogEntry
	ExcludedCatalog    []ToolCatalogEntry
	AvailableCount     int
	SchemaBytes        int
	SchemaTokens       int
	SchemaBudgetTokens int
}

// ModelDefs returns the tool definitions in model-facing priority order.
// Defs and Catalog preserve registration order for UI/API stability; model
// calls should see durable state/control tools before broad execution tools so
// scheduling, planning, memory, or loop maintenance requests are less likely
// to be routed through shell or file edits first.
func (r *Registry) ModelDefs() []ToolDef {
	return r.SelectModelTools(ToolSurfacePolicy{}).Defs
}

// SelectModelTools returns the model-facing tools after applying an optional
// schema-token budget. When the full schema fits, the registry exposes every
// tool. When it does not, it keeps the highest-priority prefix by modelToolRank
// and always exposes at least one tool so the model still has a valid surface.
func (r *Registry) SelectModelTools(policy ToolSurfacePolicy) ToolSurfaceSelection {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := r.modelOrderedNamesLocked()
	allDefs := make([]ToolDef, 0, len(names))
	for _, n := range names {
		allDefs = append(allDefs, r.tools[n].AsDef())
	}
	allEstimate := EstimateRequestInput(nil, allDefs)
	budget := policy.SchemaTokenBudget
	selected := allDefs
	if budget > 0 && allEstimate.ToolSchemaTokens > budget {
		selected = selected[:0]
		for _, n := range names {
			candidate := append(append([]ToolDef(nil), selected...), r.tools[n].AsDef())
			if len(selected) > 0 && EstimateRequestInput(nil, candidate).ToolSchemaTokens > budget {
				break
			}
			selected = candidate
		}
		if len(selected) == 0 && len(names) > 0 {
			selected = []ToolDef{r.tools[names[0]].AsDef()}
		}
	}
	selectedNames := map[string]bool{}
	for _, def := range selected {
		selectedNames[def.Function.Name] = true
	}
	catalog := make([]ToolCatalogEntry, 0, len(selected))
	excluded := make([]ToolCatalogEntry, 0, len(r.order)-len(selected))
	for _, n := range r.order {
		entry := r.tools[n].CatalogEntry()
		if selectedNames[n] {
			catalog = append(catalog, entry)
		} else {
			excluded = append(excluded, entry)
		}
	}
	estimate := EstimateRequestInput(nil, selected)
	if budget <= 0 || allEstimate.ToolSchemaTokens <= budget {
		budget = 0
	}
	return ToolSurfaceSelection{
		Defs:               append([]ToolDef(nil), selected...),
		Catalog:            catalog,
		ExcludedCatalog:    excluded,
		AvailableCount:     len(r.order),
		SchemaBytes:        estimate.ToolSchemaBytes,
		SchemaTokens:       estimate.ToolSchemaTokens,
		SchemaBudgetTokens: budget,
	}
}

func (r *Registry) modelOrderedNamesLocked() []string {
	names := append([]string(nil), r.order...)
	sort.SliceStable(names, func(i, j int) bool {
		return modelToolRank(r.tools[names[i]]) < modelToolRank(r.tools[names[j]])
	})
	return names
}

func (r *Registry) Catalog() []ToolCatalogEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]ToolCatalogEntry, 0, len(r.order))
	for _, n := range r.order {
		out = append(out, r.tools[n].CatalogEntry())
	}
	return out
}

func modelToolRank(t *Tool) int {
	if t == nil {
		return 90
	}
	switch t.Name {
	case PlanToolName:
		return 0
	case SessionScheduleToolName:
		return 1
	case SessionWorkspaceToolName:
		return 2
	case LoopProtocolToolName:
		return 3
	case MemoryToolName:
		return 4
	case SessionSearchToolName:
		return 5
	case SkillToolName:
		return 6
	case FocusedTaskToolName, SubagentToolName:
		return 7
	case "file_context", SymbolContextToolName, "repo_search", "list_files", "read_file":
		return 20
	case "write_file", "edit_file":
		return 30
	case "shell":
		return 40
	case "web_fetch", "web_search", "browser_find", "browser_snapshot", "browser_network", "browser_network_read", "browser_navigate":
		return 50
	}
	switch t.catalogGroup() {
	case "Core":
		return 10
	case "Memory":
		return 11
	case "History":
		return 12
	case "Workspace":
		return 35
	case "Research":
		return 50
	default:
		return 90
	}
}

func (t *Tool) catalogGroup() string {
	if t == nil {
		return "Other"
	}
	if group := strings.TrimSpace(t.CatalogGroup); group != "" {
		return group
	}
	switch t.Name {
	case SkillToolName, PlanToolName, SubagentToolName, FocusedTaskToolName:
		return "Core"
	case MemoryToolName:
		return "Memory"
	case SessionSearchToolName:
		return "History"
	case "shell", "read_file", "file_context", "write_file", "edit_file", "list_files", SymbolContextToolName, "repo_search":
		return "Workspace"
	case "web_search", "web_fetch", "browser_navigate", "browser_snapshot", "browser_find", "browser_network", "browser_network_read", "browser_click", "browser_scroll", "browser_type", "browser_wait", "browser_screenshot":
		return "Research"
	default:
		if strings.TrimSpace(t.CatalogSource) != "" {
			return "MCP"
		}
		return "Other"
	}
}

func (t *Tool) catalogRawName() string {
	if t == nil {
		return ""
	}
	if raw := strings.TrimSpace(t.CatalogRawName); raw != "" {
		return raw
	}
	return ""
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
	wantKeys := toolIdentifierKeys(name)
	lowerName := strings.ToLower(name)
	for rank, toolName := range r.order {
		lowerTool := strings.ToLower(toolName)
		distance := levenshtein(lowerName, lowerTool)
		if len(wantKeys) > 0 {
			for key := range canonicalToolIdentifierKeys(toolName) {
				for want := range wantKeys {
					distance = min(distance, levenshtein(want, key))
				}
			}
		}
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
	if toolErrorIsInvalidArgs(err, res) {
		return toolErrorHelp(t, args)
	}
	if toolErrorAlreadyGuided(err, res) {
		return ""
	}
	return "\nNext: read the error, change the arguments or choose a different tool; do not repeat the same failing call unchanged."
}

func toolErrorIsInvalidArgs(err error, res string) bool {
	if err != nil && strings.Contains(err.Error(), "Failure: kind=invalid_args") {
		return true
	}
	return strings.Contains(res, "Failure: kind=invalid_args")
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

func toolIdentifierKeys(s string) map[string]bool {
	base := normalizeToolIdentifier(s)
	if base == "" {
		return nil
	}
	out := map[string]bool{base: true}
	for _, suffix := range []string{"tool", "function", "fn"} {
		if strings.HasSuffix(base, suffix) && len(base) > len(suffix) {
			out[strings.TrimSuffix(base, suffix)] = true
		}
	}
	for _, prefix := range []string{"tool", "function", "fn"} {
		if strings.HasPrefix(base, prefix) && len(base) > len(prefix) {
			out[strings.TrimPrefix(base, prefix)] = true
		}
	}
	return out
}

func canonicalToolIdentifierKeys(toolName string) map[string]bool {
	out := toolIdentifierKeys(toolName)
	if len(out) == 0 {
		return nil
	}
	base := normalizeToolIdentifier(toolName)
	for _, alias := range commonToolNameAliases[base] {
		for key := range toolIdentifierKeys(alias) {
			out[key] = true
		}
	}
	return out
}

func toolIdentifierSetsOverlap(a, b map[string]bool) bool {
	if len(a) == 0 || len(b) == 0 {
		return false
	}
	if len(a) > len(b) {
		a, b = b, a
	}
	for key := range a {
		if b[key] {
			return true
		}
	}
	return false
}

var commonToolNameAliases = map[string][]string{
	"editfile":      {"file_edit", "modify_file", "patch_file", "replace_file"},
	"listfiles":     {"list_dir", "list_directory", "directory_list", "ls"},
	"readfile":      {"file_read", "open_file", "view_file"},
	"runtask":       {"focused_task", "focused_task_run", "task_run"},
	"sessionsearch": {"search_sessions", "session_lookup"},
	"shell":         {"run_command", "exec_command", "execute_command", "terminal"},
	"subagentrun":   {"subagent", "run_subagent"},
	"writefile":     {"file_write", "create_file", "save_file"},
}
