package agent

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
)

//go:embed builtin_skills/*/SKILL.md builtin_skills/*/skill.json
var builtinSkillFS embed.FS

// SkillProvider returns a concise, task-relevant system block for the
// current user turn. It is deliberately a tiny interface: deployments
// can back it with the BuiltinSkillProvider's registry, with workspace
// skills loaded from disk, with MCP-provided skills, or with a trained
// router — Loop only sees the function.
type SkillProvider func(userText string) string

// Skill is one registerable system-prompt block plus the rule for
// when to inject it. Fields are deliberately data-driven so a new
// skill is one struct literal, not a new function + new branch in
// the router.
//
// Activation precedence:
//
//  1. If Match is non-nil, it owns the decision. Built-ins use this
//     for conservative multi-signal predicates instead of broad word
//     triggers.
//  2. Otherwise, the skill fires when any string in Triggers is a
//     case-insensitive substring of the user message.
//
// Triggers are a simple extension point, not the long-term skill
// selection architecture. They should stay as generic shape signals
// ("http://", "page") rather than specific site / project / company
// names. The production direction is a manifest/indexed catalog plus
// explicit skill loading; this layer exists as a deterministic,
// eval-pinned bootstrap for small models.
type Skill struct {
	// Name is the skill's identifier. Surfaced in the Body's header
	// (e.g. "AFFENT ACTIVE SKILL: web_snapshot_fact_extraction") so
	// trace consumers and operators can grep / filter.
	Name string
	// Description is a one-line catalog summary used by skill listing
	// surfaces. It should describe when the skill helps, not repeat
	// the full procedure.
	Description string
	// Source identifies where the skill body came from. Built-ins use
	// an embedded SKILL.md path; deployments can use file://, mcp://,
	// or any operator-defined label.
	Source string
	// Body is the system-prompt block injected verbatim when the
	// skill activates. Multi-line markdown is fine; the registry
	// joins multiple active skills with a blank line.
	Body string
	// AutoActivation is a manifest-declared conservative local rule
	// for automatic injection. It keeps built-in skill routing data
	// near the skill file instead of scattering it through Go code.
	AutoActivation SkillAutoActivation
	// Triggers are substrings of the lowercased user text that fire
	// this skill. Ignored when Match is non-nil.
	Triggers []string
	// Match optionally replaces the default Triggers contains-check
	// with caller logic. Receives the LOWERCASED user text so callers
	// don't have to repeat case folding. Returning true activates
	// the skill.
	Match func(lowerUserText string) bool
}

// activates returns whether s should fire for the given lowercased
// user text.
func (s Skill) activates(lowerUserText string) bool {
	if s.Match != nil {
		return s.Match(lowerUserText)
	}
	if s.AutoActivation.hasRules() {
		return s.AutoActivation.matches(lowerUserText)
	}
	for _, trigger := range s.Triggers {
		if trigger == "" {
			continue
		}
		if strings.Contains(lowerUserText, trigger) {
			return true
		}
	}
	return false
}

type SkillAutoActivation struct {
	// Any activates the skill when any phrase is present.
	Any []string `json:"any,omitempty"`
	// AllAny activates the skill when each group has at least one
	// present phrase. Example: verb group AND domain group.
	AllAny [][]string `json:"all_any,omitempty"`
}

func (a SkillAutoActivation) hasRules() bool {
	return len(a.Any) > 0 || len(a.AllAny) > 0
}

func (a SkillAutoActivation) matches(lowerUserText string) bool {
	if containsAny(lowerUserText, a.Any) {
		return true
	}
	if len(a.AllAny) == 0 {
		return false
	}
	for _, group := range a.AllAny {
		if !containsAny(lowerUserText, group) {
			return false
		}
	}
	return true
}

// SkillRegistry is an ordered set of skills. The router iterates in
// registration order so multiple active skills compose deterministically
// (matters for prompt-shape stability across reproducible eval runs).
type SkillRegistry struct {
	skills []Skill
}

type SkillCatalogEntry struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Source      string `json:"source,omitempty"`
}

// Register appends a skill. Operators wiring a custom registry call
// Register once per skill; Provide composes from whatever's there.
// No de-dup by Name — callers that need uniqueness enforce it
// themselves at registration time.
func (r *SkillRegistry) Register(s Skill) {
	if s.Name == "" || strings.TrimSpace(s.Body) == "" {
		// A nameless or empty skill is operator error, but failing
		// loudly here would refuse a deploy; instead drop it and
		// keep going. The router is best-effort prompt enrichment.
		return
	}
	r.skills = append(r.skills, s)
}

// Lookup returns a registered skill by exact name.
func (r *SkillRegistry) Lookup(name string) (Skill, bool) {
	if r == nil {
		return Skill{}, false
	}
	for _, s := range r.skills {
		if s.Name == name {
			return s, true
		}
	}
	return Skill{}, false
}

// Catalog returns the non-body metadata for registered skills. Tool
// surfaces use this to let the model discover reusable workflows
// without injecting every skill body into the prompt.
func (r *SkillRegistry) Catalog() []SkillCatalogEntry {
	if r == nil {
		return nil
	}
	out := make([]SkillCatalogEntry, 0, len(r.skills))
	for _, s := range r.skills {
		out = append(out, SkillCatalogEntry{
			Name:        s.Name,
			Description: s.Description,
			Source:      s.Source,
		})
	}
	return out
}

// Provide is the SkillProvider implementation backed by this
// registry. Returns the empty string when no skill activates so the
// Loop can use `if got != "" { … inject … }` without an extra check.
func (r *SkillRegistry) Provide(userText string) string {
	if r == nil || len(r.skills) == 0 {
		return ""
	}
	lower := strings.ToLower(userText)
	var blocks []string
	for _, s := range r.skills {
		if s.activates(lower) {
			blocks = append(blocks, s.Body)
		}
	}
	return strings.Join(blocks, "\n\n")
}

// Names returns the registered skill names in order. Lets operators
// log / inspect the active set at boot without exposing the slice.
func (r *SkillRegistry) Names() []string {
	if r == nil {
		return nil
	}
	out := make([]string, 0, len(r.skills))
	for _, s := range r.skills {
		out = append(out, s.Name)
	}
	return out
}

// --- builtin skill catalog ---

func containsAny(text string, needles []string) bool {
	for _, needle := range needles {
		if needle != "" && strings.Contains(text, needle) {
			return true
		}
	}
	return false
}

type builtinSkillManifest struct {
	Name           string              `json:"name"`
	Description    string              `json:"description"`
	Order          int                 `json:"order,omitempty"`
	AutoActivation SkillAutoActivation `json:"auto_activation"`
}

func mustBuiltinSkill(name string) Skill {
	s, _, err := loadBuiltinSkill(builtinSkillFS, name)
	if err != nil {
		panic(err.Error())
	}
	return s
}

func mustBuiltinSkills() []Skill {
	skills, err := loadBuiltinSkills(builtinSkillFS)
	if err != nil {
		panic(err.Error())
	}
	return skills
}

type orderedBuiltinSkill struct {
	skill Skill
	order int
}

func loadBuiltinSkills(fsys fs.FS) ([]Skill, error) {
	entries, err := fs.ReadDir(fsys, "builtin_skills")
	if err != nil {
		return nil, fmt.Errorf("list builtin skills: %w", err)
	}
	ordered := make([]orderedBuiltinSkill, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		skill, order, err := loadBuiltinSkill(fsys, entry.Name())
		if err != nil {
			return nil, err
		}
		ordered = append(ordered, orderedBuiltinSkill{skill: skill, order: order})
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].order != ordered[j].order {
			return ordered[i].order < ordered[j].order
		}
		return ordered[i].skill.Name < ordered[j].skill.Name
	})
	skills := make([]Skill, 0, len(ordered))
	for _, item := range ordered {
		skills = append(skills, item.skill)
	}
	return skills, nil
}

func loadBuiltinSkill(fsys fs.FS, dir string) (Skill, int, error) {
	base := path.Join("builtin_skills", dir)
	body, err := fs.ReadFile(fsys, path.Join(base, "SKILL.md"))
	if err != nil {
		return Skill{}, 0, fmt.Errorf("load builtin skill %s: %w", dir, err)
	}
	manifestRaw, err := fs.ReadFile(fsys, path.Join(base, "skill.json"))
	if err != nil {
		return Skill{}, 0, fmt.Errorf("load builtin skill manifest %s: %w", dir, err)
	}
	var manifest builtinSkillManifest
	if err := json.Unmarshal(manifestRaw, &manifest); err != nil {
		return Skill{}, 0, fmt.Errorf("parse builtin skill manifest %s: %w", dir, err)
	}
	if manifest.Name == "" {
		manifest.Name = dir
	}
	if manifest.Name != dir {
		return Skill{}, 0, fmt.Errorf("builtin skill %s manifest name %q does not match directory", dir, manifest.Name)
	}
	if strings.TrimSpace(string(body)) == "" {
		return Skill{}, 0, fmt.Errorf("builtin skill %s has empty SKILL.md", dir)
	}
	return Skill{
		Name:           manifest.Name,
		Description:    manifest.Description,
		Source:         "embed:internal/agent/" + path.Join(base, "SKILL.md"),
		Body:           strings.TrimSpace(string(body)),
		AutoActivation: manifest.AutoActivation,
	}, builtinSkillOrder(manifest), nil
}

func builtinSkillOrder(manifest builtinSkillManifest) int {
	if manifest.Order > 0 {
		return manifest.Order
	}
	return 1000
}

// WebSnapshotSkill is the built-in skill for rendered web-page fact
// extraction. Exported so operators composing a custom registry can
// keep it without copy-pasting the body.
func WebSnapshotSkill() Skill {
	return mustBuiltinSkill("web_snapshot_fact_extraction")
}

// CodingRepairSkill is the built-in skill for code-edit / test-fix
// workflows. Same rationale as WebSnapshotSkill — exported so a
// custom registry can include it selectively.
func CodingRepairSkill() Skill {
	return mustBuiltinSkill("coding_repair_workflow")
}

func EvidenceFactExtractionSkill() Skill {
	return mustBuiltinSkill("evidence_fact_extraction")
}

// DefaultSkillRegistry returns the registry affentctl / affentserve
// install when the operator doesn't override SkillProvider. Built-in
// skills are discovered from embedded skill.json manifests so adding
// one is a new directory, not a router edit.
func DefaultSkillRegistry() *SkillRegistry {
	r := &SkillRegistry{}
	for _, skill := range mustBuiltinSkills() {
		r.Register(skill)
	}
	return r
}

// builtinSkillProviderRegistry is the package-level default backing
// BuiltinSkillProvider. Constructed once so the per-turn lookup is
// just a slice iteration, not a fresh registry build.
var builtinSkillProviderRegistry = DefaultSkillRegistry()

// BuiltinSkillProvider is the default lightweight skill bootstrap. It
// delegates to DefaultSkillRegistry, which only auto-injects skills
// when conservative local signals match. This is intentionally modest:
// irrelevant skill prompts hurt smaller models, and a mature skill
// system should expose catalog/search/load behavior rather than hide
// every decision in Go predicates.
//
// Custom deployments can build their own SkillRegistry (or any
// SkillProvider function) and wire it via Loop.SkillProvider without
// touching this file.
func BuiltinSkillProvider(userText string) string {
	return builtinSkillProviderRegistry.Provide(userText)
}
