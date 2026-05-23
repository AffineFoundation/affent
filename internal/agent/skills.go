package agent

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
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

const (
	maxRuntimeSkillNameBytes        = 128
	maxRuntimeSkillDescriptionBytes = 512
	maxRuntimeSkillBodyBytes        = 64 * 1024
	maxRuntimeSkills                = 128
	maxRuntimeSkillTriggers         = 20
	maxRuntimeSkillTriggerBytes     = 128
	maxRuntimeSkillManifestBytes    = maxRuntimeSkillDescriptionBytes + maxRuntimeSkillTriggerBytes*maxRuntimeSkillTriggers + 1024
)

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
	mu     sync.RWMutex
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
	r.mu.Lock()
	defer r.mu.Unlock()
	r.skills = append(r.skills, s)
}

// Upsert validates and installs a skill by name. It is used for runtime
// skill installation where repeated installs should update the active body
// instead of registering duplicate catalog entries.
func (r *SkillRegistry) Upsert(s Skill) error {
	if r == nil {
		return fmt.Errorf("skill registry is nil")
	}
	normalized, err := normalizeRuntimeSkill(s)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, existing := range r.skills {
		if existing.Name == normalized.Name {
			r.skills[i] = normalized
			return nil
		}
	}
	r.skills = append(r.skills, normalized)
	return nil
}

// Lookup returns a registered skill by exact name.
func (r *SkillRegistry) Lookup(name string) (Skill, bool) {
	if r == nil {
		return Skill{}, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
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
	r.mu.RLock()
	defer r.mu.RUnlock()
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
	if r == nil {
		return ""
	}
	r.mu.RLock()
	skills := append([]Skill(nil), r.skills...)
	r.mu.RUnlock()
	if len(skills) == 0 {
		return ""
	}
	lower := strings.ToLower(userText)
	var blocks []string
	for _, s := range skills {
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
	r.mu.RLock()
	defer r.mu.RUnlock()
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

type runtimeSkillManifest struct {
	Name           string              `json:"name"`
	Description    string              `json:"description,omitempty"`
	AutoActivation SkillAutoActivation `json:"auto_activation,omitempty"`
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

// RuntimeSkillRegistry returns the default built-in registry plus any skills
// installed under skillDir. The directory is optional; callers pass an empty
// path when they want built-ins only.
func RuntimeSkillRegistry(skillDir string) (*SkillRegistry, error) {
	r := DefaultSkillRegistry()
	if strings.TrimSpace(skillDir) == "" {
		return r, nil
	}
	skills, err := LoadSkillDir(skillDir)
	if err != nil {
		return nil, err
	}
	for _, skill := range skills {
		if err := r.Upsert(skill); err != nil {
			return nil, err
		}
	}
	return r, nil
}

// DefaultWorkspaceSkillDir is the per-workspace runtime skill install path.
func DefaultWorkspaceSkillDir(workspace string) string {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		return ""
	}
	return filepath.Join(workspace, ".affent", "skills")
}

func LoadSkillDir(root string) ([]Skill, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list skills %s: %w", root, err)
	}
	var out []Skill
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if len(out) >= maxRuntimeSkills {
			return nil, fmt.Errorf("runtime skill directory %s has more than %d skills", root, maxRuntimeSkills)
		}
		skill, err := loadRuntimeSkill(filepath.Join(root, entry.Name()))
		if err != nil {
			return nil, err
		}
		out = append(out, skill)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func InstallRuntimeSkill(root string, skill Skill) (Skill, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return Skill{}, fmt.Errorf("runtime skill directory is not configured")
	}
	normalized, err := normalizeRuntimeSkill(skill)
	if err != nil {
		return Skill{}, err
	}
	dir := filepath.Join(root, normalized.Name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Skill{}, fmt.Errorf("create skill directory: %w", err)
	}
	normalized.Source = "file://" + filepath.ToSlash(filepath.Join(dir, "SKILL.md"))
	manifest := runtimeSkillManifest{
		Name:           normalized.Name,
		Description:    normalized.Description,
		AutoActivation: normalized.AutoActivation,
	}
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return Skill{}, err
	}
	if err := os.WriteFile(filepath.Join(dir, "skill.json"), append(raw, '\n'), 0o644); err != nil {
		return Skill{}, fmt.Errorf("write skill manifest: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(normalized.Body+"\n"), 0o644); err != nil {
		return Skill{}, fmt.Errorf("write skill body: %w", err)
	}
	return normalized, nil
}

func loadRuntimeSkill(dir string) (Skill, error) {
	manifestPath := filepath.Join(dir, "skill.json")
	manifestRaw, err := readRuntimeSkillFile(manifestPath, maxRuntimeSkillManifestBytes)
	if err != nil {
		return Skill{}, fmt.Errorf("load skill manifest %s: %w", dir, err)
	}
	var manifest runtimeSkillManifest
	if err := json.Unmarshal(manifestRaw, &manifest); err != nil {
		return Skill{}, fmt.Errorf("parse skill manifest %s: %w", dir, err)
	}
	body, err := readRuntimeSkillFile(filepath.Join(dir, "SKILL.md"), maxRuntimeSkillBodyBytes)
	if err != nil {
		return Skill{}, fmt.Errorf("load skill body %s: %w", dir, err)
	}
	skill := Skill{
		Name:           manifest.Name,
		Description:    manifest.Description,
		Source:         "file://" + filepath.ToSlash(filepath.Join(dir, "SKILL.md")),
		Body:           string(body),
		AutoActivation: manifest.AutoActivation,
	}
	return normalizeRuntimeSkill(skill)
}

func readRuntimeSkillFile(path string, maxBytes int) ([]byte, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if st.IsDir() {
		return nil, fmt.Errorf("%s is a directory", path)
	}
	if st.Size() > int64(maxBytes) {
		return nil, fmt.Errorf("%s is %d bytes; max %d", path, st.Size(), maxBytes)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(raw) > maxBytes {
		return nil, fmt.Errorf("%s is %d bytes; max %d", path, len(raw), maxBytes)
	}
	return raw, nil
}

func normalizeRuntimeSkill(s Skill) (Skill, error) {
	s.Name = strings.TrimSpace(s.Name)
	s.Description = strings.TrimSpace(s.Description)
	s.Body = strings.TrimSpace(s.Body)
	if s.Name == "" {
		return Skill{}, fmt.Errorf("skill name is required")
	}
	if len(s.Name) > maxRuntimeSkillNameBytes {
		return Skill{}, fmt.Errorf("skill name is %d bytes; max %d", len(s.Name), maxRuntimeSkillNameBytes)
	}
	if !validRuntimeSkillName(s.Name) {
		return Skill{}, fmt.Errorf("skill name %q may contain only ASCII letters, digits, '_' or '-'", s.Name)
	}
	if len(s.Description) > maxRuntimeSkillDescriptionBytes {
		return Skill{}, fmt.Errorf("skill description is %d bytes; max %d", len(s.Description), maxRuntimeSkillDescriptionBytes)
	}
	if s.Body == "" {
		return Skill{}, fmt.Errorf("skill body is required")
	}
	if len(s.Body) > maxRuntimeSkillBodyBytes {
		return Skill{}, fmt.Errorf("skill body is %d bytes; max %d", len(s.Body), maxRuntimeSkillBodyBytes)
	}
	if err := validateSkillAutoActivation(s.AutoActivation); err != nil {
		return Skill{}, err
	}
	return s, nil
}

func validateSkillAutoActivation(a SkillAutoActivation) error {
	if len(a.Any) > maxRuntimeSkillTriggers {
		return fmt.Errorf("skill auto_activation.any has %d entries; max %d", len(a.Any), maxRuntimeSkillTriggers)
	}
	for _, trigger := range a.Any {
		if err := validateRuntimeSkillTrigger(trigger); err != nil {
			return err
		}
	}
	if len(a.AllAny) > maxRuntimeSkillTriggers {
		return fmt.Errorf("skill auto_activation.all_any has %d groups; max %d", len(a.AllAny), maxRuntimeSkillTriggers)
	}
	for _, group := range a.AllAny {
		if len(group) > maxRuntimeSkillTriggers {
			return fmt.Errorf("skill auto_activation.all_any group has %d entries; max %d", len(group), maxRuntimeSkillTriggers)
		}
		for _, trigger := range group {
			if err := validateRuntimeSkillTrigger(trigger); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateRuntimeSkillTrigger(trigger string) error {
	trigger = strings.TrimSpace(trigger)
	if trigger == "" {
		return fmt.Errorf("skill auto-activation trigger must not be empty")
	}
	if len(trigger) > maxRuntimeSkillTriggerBytes {
		return fmt.Errorf("skill auto-activation trigger is %d bytes; max %d", len(trigger), maxRuntimeSkillTriggerBytes)
	}
	return nil
}

func validRuntimeSkillName(name string) bool {
	for _, r := range name {
		if r == '_' || r == '-' || ('A' <= r && r <= 'Z') || ('a' <= r && r <= 'z') || ('0' <= r && r <= '9') {
			continue
		}
		return false
	}
	return true
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
