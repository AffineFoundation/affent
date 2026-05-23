// Package architecture is a test-only package that pins the internal/
// dependency layering as a CI-enforced contract. There is no
// production code here — just the test.
//
// Why this exists: the agent/memory bridge file (deleted in commit
// 439a3e5) was redundant because nothing prevented agent/ from
// re-exporting symbols defined in memory/. The fix was a refactor,
// but a refactor without a guard rots in 30 days when someone adds
// `import "internal/agent"` from memory/ for "convenience". This
// test fails loudly on that drift.
//
// The layering rules below are the team's actual contract; edit them
// only with deliberate intent, and update this comment when the
// contract changes.
package architecture_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// modulePath is the Go module path; package imports inside this
// repo start with it. Kept as a constant so the contract reads
// declaratively below.
const modulePath = "github.com/affinefoundation/affent"

// allowedDeps is the full set of internal/* imports each internal/*
// package is allowed to use. A package omitted from the map has NO
// internal/* dependencies (leaf package); a package whose value is
// empty has the same constraint expressed explicitly.
//
// Adding an edge: edit this map. Adding a NEW package: add its key
// here AND set its allowed parents. Drift is intentional friction.
//
// The contract is layered:
//
//	leaves      - sse, textutil                              (zero internal deps)
//	leaf-deps   - projectcontext, sessionsearch, memory      (only textutil; no cross-layer)
//	exec        - executor                                   (no internal deps)
//	wire        - mcp                                        (textutil caps + sse + agent adapter — see note)
//	core        - agent                                      (everything below)
//	app         - agenteval, e2e                             (depends on agent)
//
// Note on mcp -> agent: register.go in the mcp package adapts MCP
// tool descriptors into agent.Tool / agent.Registry entries. That's
// an inversion of the protocol-vs-runtime ideal — protocol code
// referencing the consumer. It's preserved because the alternative
// (a separate internal/agentmcp adapter package) would split mcp
// across two places. The edge is allowed but flagged here so a
// reviewer knows it's deliberate, not drift.
//
// A future "tools sanitization" sub-package (tool_arg_repair,
// tool_schema_repair, tool_loop_guard — currently 608 LoC inside
// internal/agent/) would live BELOW agent and depend on nothing
// from agent.
var allowedDeps = map[string]map[string]bool{
	// --- leaves ---
	"internal/sse":      {},
	"internal/textutil": {},

	// --- leaf-deps: small packages that legitimately use textutil
	// for cap-aware string trimming. None of them depend on each
	// other or on anything heavier than textutil.
	"internal/projectcontext": {
		"internal/textutil": true,
	},
	"internal/memory": {
		"internal/textutil": true,
	},
	"internal/sessionsearch": {
		"internal/textutil": true,
	},

	// --- exec ---
	"internal/executor": {},

	// --- trace persistence ---
	// eventlog is the canonical JSONL recorder for sse.Events. It
	// lives between sse (the wire shape) and agent / cmd consumers, so
	// it depends on sse only.
	"internal/eventlog": {
		"internal/sse": true,
	},

	// --- protocol wire ---
	"internal/mcp": {
		"internal/textutil": true,
		"internal/sse":      true,
		"internal/agent":    true, // see package-level note on register.go
	},

	// --- agent runtime ---
	"internal/agent": {
		"internal/executor":       true,
		"internal/memory":         true,
		"internal/mcp":            true,
		"internal/projectcontext": true,
		"internal/sessionsearch":  true,
		"internal/sse":            true,
		"internal/textutil":       true,
	},

	// --- evaluation harness ---
	"internal/agenteval": {
		"internal/agent":    true,
		"internal/executor": true,
		"internal/sse":      true,
	},

	// --- end-to-end test fixtures ---
	"internal/e2e": {
		"internal/agent":  true,
		"internal/memory": true,
		"internal/sse":    true,
	},
}

// TestInternalLayering walks every Go file under internal/ and pins
// that the package's imports stay within the allowed set declared in
// allowedDeps.
//
// A failure here is almost always a design regression: a leaf package
// reaching for agent state, a storage layer pulling in execution
// concerns, or the agent runtime taking a dependency on its own
// evaluation harness. Fix by either widening the contract in
// allowedDeps (deliberately) or removing the import.
func TestInternalLayering(t *testing.T) {
	root := repoRoot(t)
	internalDir := filepath.Join(root, "internal")

	pkgImports, err := walkInternalImports(internalDir)
	if err != nil {
		t.Fatalf("walk internal/: %v", err)
	}

	// Every package we found should be declared in the contract.
	// An undeclared package is operator error: it can pass the test
	// by being silently exempt, which defeats the point. Force the
	// contract to mention every internal/* package.
	declared := map[string]bool{}
	for name := range allowedDeps {
		declared[name] = true
	}
	for pkg := range pkgImports {
		if !declared[pkg] {
			t.Errorf("internal package %q is not declared in allowedDeps; add it to the contract (use {} for a leaf)", pkg)
		}
	}

	// Every actual edge must be in the allowed set.
	for pkg, imports := range pkgImports {
		allowed := allowedDeps[pkg]
		for _, imp := range imports {
			if !strings.HasPrefix(imp, modulePath+"/internal/") {
				continue
			}
			rel := strings.TrimPrefix(imp, modulePath+"/")
			if rel == pkg {
				continue // self-import doesn't really happen but defensively skip
			}
			if !allowed[rel] {
				t.Errorf("layering violation: %s imports %s; not in allowedDeps[%q]", pkg, rel, pkg)
			}
		}
	}
}

// TestSpecificForbiddenEdges spells out the most damaging layering
// inversions as named cases. TestInternalLayering catches them too,
// but a named test fails with a message a reviewer reads immediately:
// "memory must not depend on agent" beats a generic "violation:
// memory imports agent" in a long failure list.
func TestSpecificForbiddenEdges(t *testing.T) {
	root := repoRoot(t)
	pkgImports, err := walkInternalImports(filepath.Join(root, "internal"))
	if err != nil {
		t.Fatalf("walk internal/: %v", err)
	}

	forbidden := []struct {
		from, to string
		reason   string
	}{
		{"internal/memory", "internal/agent",
			"storage layer must not depend on the runtime that consumes it; agent_tool wraps memory, not the other way around"},
		{"internal/memory", "internal/agenteval",
			"storage layer must not depend on the eval harness"},
		{"internal/sse", "internal/agent",
			"sse is the wire format; the runtime depends on sse, not the other way around"},
		{"internal/textutil", "internal/agent",
			"textutil is a leaf utility package"},
		{"internal/projectcontext", "internal/agent",
			"projectcontext is read by the runtime via dependency injection; it knows nothing about the consumer"},
		{"internal/executor", "internal/agent",
			"executor is the shell backend; the runtime selects an executor, the executor does not know about the runtime"},
		{"internal/agent", "internal/agenteval",
			"the eval harness depends on the runtime, not the reverse — otherwise running the runtime forces an eval-framework dep in production binaries"},
	}

	for _, edge := range forbidden {
		imports, ok := pkgImports[edge.from]
		if !ok {
			continue // package not present yet
		}
		target := modulePath + "/" + edge.to
		for _, imp := range imports {
			if imp == target {
				t.Errorf("%s -> %s: %s", edge.from, edge.to, edge.reason)
			}
		}
	}
}

// walkInternalImports returns a map of "internal/<pkg>" -> the
// distinct import paths used in non-test .go files under that
// package. We deliberately exclude *_test.go: tests legitimately need
// to import sibling internal packages (e.g. e2e tests import agent
// + memory + sse) and that's already covered by the production
// import contract.
func walkInternalImports(internalDir string) (map[string][]string, error) {
	pkgImports := map[string]map[string]bool{}

	err := filepath.WalkDir(internalDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		if strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(filepath.Dir(internalDir), path)
		if err != nil {
			return err
		}
		// rel is like "internal/agent/loop.go"; convert to slash for
		// platform consistency and trim the file segment to get the
		// package key "internal/agent".
		pkg := filepath.ToSlash(filepath.Dir(rel))
		// Skip the architecture test package itself (no production deps).
		if pkg == "internal/architecture" {
			return nil
		}

		fset := token.NewFileSet()
		f, perr := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if perr != nil {
			return perr
		}
		if pkgImports[pkg] == nil {
			pkgImports[pkg] = map[string]bool{}
		}
		for _, imp := range f.Imports {
			val, err := strconvUnquote(imp.Path.Value)
			if err != nil {
				continue
			}
			pkgImports[pkg][val] = true
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Flatten to []string sorted for stable test output.
	out := map[string][]string{}
	for pkg, set := range pkgImports {
		var imports []string
		for imp := range set {
			imports = append(imports, imp)
		}
		sort.Strings(imports)
		out[pkg] = imports
	}
	return out, nil
}

// strconvUnquote is a tiny shim: imp.Path.Value comes wrapped in
// double quotes from the parser. We just strip them.
func strconvUnquote(s string) (string, error) {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1], nil
	}
	return s, nil
}

// repoRoot walks upward from the test binary's working directory
// until it finds a go.mod, then returns that directory. Lets the
// test work whether run from the repo root or from
// internal/architecture/.
func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	dir := wd
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("repo root not found from %s", wd)
		}
		dir = parent
	}
}
