package agent

import (
	"crypto/sha256"
	"embed"
	"encoding/json"
	"fmt"
	"io"
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
	maxRuntimeSkillSourceBytes      = 2 * 1024
	maxRuntimeSkills                = 128
	runtimeSkillDirReadBatch        = 64
	maxRuntimeSkillTriggers         = 20
	maxRuntimeSkillTriggerBytes     = 128
	maxRuntimeSkillManifestBytes    = maxRuntimeSkillDescriptionBytes + maxRuntimeSkillSourceBytes + maxRuntimeSkillTriggerBytes*maxRuntimeSkillTriggers + 1024
	maxRuntimeSkillProposalBytes    = maxRuntimeSkillBodyBytes + maxRuntimeSkillManifestBytes + maxRuntimeSkillSourceBytes + 4096
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
	Name           string               `json:"name"`
	Description    string               `json:"description,omitempty"`
	Source         string               `json:"source,omitempty"`
	Triggers       []string             `json:"triggers,omitempty"`
	AutoActivation *SkillAutoActivation `json:"auto_activation,omitempty"`
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
		entry := SkillCatalogEntry{
			Name:        s.Name,
			Description: s.Description,
			Source:      s.Source,
		}
		if len(s.Triggers) > 0 {
			entry.Triggers = append([]string(nil), s.Triggers...)
		}
		if s.AutoActivation.hasRules() {
			auto := s.AutoActivation
			entry.AutoActivation = &auto
		}
		out = append(out, entry)
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
	Source         string              `json:"source,omitempty"`
	AutoActivation SkillAutoActivation `json:"auto_activation,omitempty"`
}

type RuntimeSkillProposal struct {
	ID             string              `json:"id"`
	Name           string              `json:"name"`
	Description    string              `json:"description,omitempty"`
	Source         string              `json:"source,omitempty"`
	Body           string              `json:"body"`
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
	if err := rejectRuntimeSkillRootSymlink(root); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	dir, err := os.Open(root)
	if err != nil {
		return nil, fmt.Errorf("list skills %s: %w", root, err)
	}
	defer dir.Close()
	var out []Skill
	for {
		entries, rerr := dir.ReadDir(runtimeSkillDirReadBatch)
		if rerr != nil && rerr != io.EOF {
			return nil, fmt.Errorf("list skills %s: %w", root, rerr)
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			if strings.HasPrefix(entry.Name(), ".") {
				continue
			}
			if len(out) >= maxRuntimeSkills {
				return nil, fmt.Errorf("runtime skill directory %s has more than %d skills", root, maxRuntimeSkills)
			}
			skillDir := filepath.Join(root, entry.Name())
			complete, err := runtimeSkillDirHasRequiredFiles(skillDir)
			if err != nil {
				return nil, err
			}
			if !complete {
				continue
			}
			skill, err := loadRuntimeSkill(skillDir)
			if err != nil {
				return nil, err
			}
			out = append(out, skill)
		}
		if rerr == io.EOF {
			break
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func runtimeSkillDirHasRequiredFiles(dir string) (bool, error) {
	for _, name := range []string{"skill.json", "SKILL.md"} {
		path := filepath.Join(dir, name)
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if info.Mode()&os.ModeSymlink != 0 || info.IsDir() {
			return true, nil
		}
	}
	return true, nil
}

func InstallRuntimeSkill(root string, skill Skill) (Skill, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return Skill{}, fmt.Errorf("runtime skill directory is not configured")
	}
	if err := ensureRuntimeSkillRoot(root); err != nil {
		return Skill{}, err
	}
	normalized, err := normalizeRuntimeSkill(skill)
	if err != nil {
		return Skill{}, err
	}
	dir := filepath.Join(root, normalized.Name)
	if err := rejectRuntimeSkillDirSymlinkIfExists(dir); err != nil {
		return Skill{}, err
	}
	if normalized.Source == "" {
		normalized.Source = runtimeSkillBodySource(dir)
	}
	manifestPath := filepath.Join(dir, "skill.json")
	bodyPath := filepath.Join(dir, "SKILL.md")
	if err := rejectRuntimeSkillFileTarget(manifestPath); err != nil {
		return Skill{}, err
	}
	if err := rejectRuntimeSkillFileTarget(bodyPath); err != nil {
		return Skill{}, err
	}
	stageDir := filepath.Join(root, ".install-"+normalized.Name+".tmp")
	backupDir := filepath.Join(root, ".install-"+normalized.Name+".old")
	if err := resetRuntimeSkillStagingDir(stageDir); err != nil {
		return Skill{}, fmt.Errorf("reset skill staging directory: %w", err)
	}
	defer os.RemoveAll(stageDir)
	if err := os.MkdirAll(stageDir, 0o755); err != nil {
		return Skill{}, fmt.Errorf("create skill staging directory: %w", err)
	}
	manifest := runtimeSkillManifest{
		Name:           normalized.Name,
		Description:    normalized.Description,
		Source:         normalized.Source,
		AutoActivation: normalized.AutoActivation,
	}
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return Skill{}, err
	}
	if err := writeRuntimeSkillFile(filepath.Join(stageDir, "SKILL.md"), []byte(normalized.Body+"\n")); err != nil {
		return Skill{}, fmt.Errorf("write skill body: %w", err)
	}
	if err := writeRuntimeSkillFile(filepath.Join(stageDir, "skill.json"), append(raw, '\n')); err != nil {
		return Skill{}, fmt.Errorf("write skill manifest: %w", err)
	}
	if err := publishRuntimeSkillDir(dir, stageDir, backupDir); err != nil {
		return Skill{}, err
	}
	return normalized, nil
}

func resetRuntimeSkillStagingDir(dir string) error {
	if err := rejectRuntimeSkillDirSymlinkIfExists(dir); err != nil {
		if info, statErr := os.Lstat(dir); statErr == nil && info.Mode()&os.ModeSymlink != 0 {
			return os.Remove(dir)
		}
		return err
	}
	return os.RemoveAll(dir)
}

func publishRuntimeSkillDir(finalDir, stageDir, backupDir string) error {
	if err := rejectRuntimeSkillDirSymlinkIfExists(finalDir); err != nil {
		return err
	}
	if err := resetRuntimeSkillStagingDir(backupDir); err != nil {
		return fmt.Errorf("reset skill backup directory: %w", err)
	}
	hadFinal := false
	if _, err := os.Lstat(finalDir); err == nil {
		hadFinal = true
		if err := os.Rename(finalDir, backupDir); err != nil {
			return fmt.Errorf("replace skill directory: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.Rename(stageDir, finalDir); err != nil {
		if hadFinal {
			_ = os.Rename(backupDir, finalDir)
		}
		return fmt.Errorf("publish skill directory: %w", err)
	}
	_ = os.RemoveAll(backupDir)
	if d, err := os.Open(filepath.Dir(finalDir)); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

func ProposeRuntimeSkill(root string, skill Skill) (RuntimeSkillProposal, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return RuntimeSkillProposal{}, fmt.Errorf("runtime skill directory is not configured")
	}
	if err := ensureRuntimeSkillRoot(root); err != nil {
		return RuntimeSkillProposal{}, err
	}
	normalized, err := normalizeRuntimeSkill(skill)
	if err != nil {
		return RuntimeSkillProposal{}, err
	}
	id := runtimeSkillProposalID(normalized)
	proposal := RuntimeSkillProposal{
		ID:             id,
		Name:           normalized.Name,
		Description:    normalized.Description,
		Source:         normalized.Source,
		Body:           normalized.Body,
		AutoActivation: normalized.AutoActivation,
	}
	dir := filepath.Join(root, ".pending")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return RuntimeSkillProposal{}, fmt.Errorf("create pending skill directory: %w", err)
	}
	if err := rejectRuntimeSkillDirSymlink(dir); err != nil {
		return RuntimeSkillProposal{}, err
	}
	raw, err := json.MarshalIndent(proposal, "", "  ")
	if err != nil {
		return RuntimeSkillProposal{}, err
	}
	if len(raw) > maxRuntimeSkillProposalBytes {
		return RuntimeSkillProposal{}, fmt.Errorf("skill proposal is %d bytes; max %d", len(raw), maxRuntimeSkillProposalBytes)
	}
	if err := writeRuntimeSkillFile(filepath.Join(dir, id+".json"), append(raw, '\n')); err != nil {
		return RuntimeSkillProposal{}, fmt.Errorf("write pending skill proposal: %w", err)
	}
	return proposal, nil
}

func ConfirmRuntimeSkillProposal(root, id string) (Skill, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return Skill{}, fmt.Errorf("runtime skill directory is not configured")
	}
	if err := rejectRuntimeSkillRootSymlink(root); err != nil {
		return Skill{}, err
	}
	id = strings.ToLower(strings.TrimSpace(id))
	if !validRuntimeSkillProposalID(id) {
		return Skill{}, fmt.Errorf("skill proposal id %q is invalid", id)
	}
	pendingDir := filepath.Join(root, ".pending")
	if err := rejectRuntimeSkillDirSymlinkIfExists(pendingDir); err != nil {
		return Skill{}, err
	}
	path := filepath.Join(pendingDir, id+".json")
	raw, err := readRuntimeSkillFile(path, maxRuntimeSkillProposalBytes)
	if err != nil {
		return Skill{}, fmt.Errorf("load pending skill proposal: %w", err)
	}
	var proposal RuntimeSkillProposal
	if err := json.Unmarshal(raw, &proposal); err != nil {
		return Skill{}, fmt.Errorf("parse pending skill proposal: %w", err)
	}
	if proposal.ID != id {
		return Skill{}, fmt.Errorf("pending skill proposal id mismatch: file has %q", proposal.ID)
	}
	installed, err := InstallRuntimeSkill(root, Skill{
		Name:           proposal.Name,
		Description:    proposal.Description,
		Source:         proposal.Source,
		Body:           proposal.Body,
		AutoActivation: proposal.AutoActivation,
	})
	if err != nil {
		return Skill{}, err
	}
	_ = os.Remove(path)
	return installed, nil
}

func UserConfirmedRuntimeSkillProposal(conv *Conversation, proposalID string) bool {
	if conv == nil {
		return false
	}
	msgs := conv.Snapshot()
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "user" {
			return userTextConfirmsRuntimeSkillProposal(msgs[i].Content, proposalID)
		}
	}
	return false
}

func userTextConfirmsRuntimeSkillProposal(text, proposalID string) bool {
	proposalID = strings.ToLower(strings.TrimSpace(proposalID))
	if !validRuntimeSkillProposalID(proposalID) {
		return false
	}
	lower := strings.ToLower(text)
	if !strings.Contains(lower, proposalID) {
		return false
	}
	for _, phrase := range []string{
		"do not", "don't", "dont", "not install", "not approve", "not approved", "not ok", "not okay", "not sure", "cancel", "reject", "no ",
		"不要", "别", "不安装", "不批准", "不可以", "取消", "拒绝", "不同意",
	} {
		if strings.Contains(lower, phrase) {
			if phrase == "no " && containsAny(lower, []string{"no problem", "no worries"}) {
				continue
			}
			return false
		}
	}
	for _, phrase := range []string{
		"confirm", "confirmed", "approve", "approved", "install", "yes", "ok", "okay", "sure", "sounds good", "go ahead", "proceed", "no problem", "no worries",
		"确认", "同意", "批准", "安装", "可以", "继续", "没问题",
	} {
		if strings.Contains(lower, phrase) {
			return true
		}
	}
	return false
}

func loadRuntimeSkill(dir string) (Skill, error) {
	if err := rejectRuntimeSkillDirSymlink(dir); err != nil {
		return Skill{}, err
	}
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
		Source:         strings.TrimSpace(manifest.Source),
		Body:           string(body),
		AutoActivation: manifest.AutoActivation,
	}
	if skill.Source == "" {
		skill.Source = runtimeSkillBodySource(dir)
	}
	return normalizeRuntimeSkill(skill)
}

func runtimeSkillBodySource(dir string) string {
	return "file://" + filepath.ToSlash(filepath.Join(dir, "SKILL.md"))
}

func readRuntimeSkillFile(path string, maxBytes int) ([]byte, error) {
	st, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if st.IsDir() {
		return nil, fmt.Errorf("%s is a directory", path)
	}
	if st.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("%s must not be a symlink", path)
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

func ensureRuntimeSkillRoot(root string) error {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	return rejectRuntimeSkillRootSymlink(root)
}

func rejectRuntimeSkillRootSymlink(root string) error {
	return rejectRuntimeSkillDirSymlink(root)
}

func rejectRuntimeSkillDirSymlinkIfExists(dir string) error {
	info, err := os.Lstat(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s must not be a symlink", dir)
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", dir)
	}
	return nil
}

func rejectRuntimeSkillDirSymlink(dir string) error {
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s must not be a symlink", dir)
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", dir)
	}
	return nil
}

func writeRuntimeSkillFile(path string, raw []byte) error {
	if err := rejectRuntimeSkillFileTarget(path); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.Remove(tmp); err != nil && !os.IsNotExist(err) {
		return err
	}
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(raw); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if d, err := os.Open(filepath.Dir(path)); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

func rejectRuntimeSkillFileTarget(path string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s must not be a symlink", path)
	}
	if info.IsDir() {
		return fmt.Errorf("%s is a directory", path)
	}
	return nil
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
	s.Source = strings.TrimSpace(s.Source)
	if len(s.Source) > maxRuntimeSkillSourceBytes {
		return Skill{}, fmt.Errorf("skill source is %d bytes; max %d", len(s.Source), maxRuntimeSkillSourceBytes)
	}
	if err := validateRuntimeSkillSource(s.Source); err != nil {
		return Skill{}, err
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

func validateRuntimeSkillSource(source string) error {
	for _, r := range source {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("skill source must not contain control characters")
		}
	}
	return nil
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

func runtimeSkillProposalID(skill Skill) string {
	h := sha256.New()
	_, _ = h.Write([]byte(skill.Name))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(skill.Description))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(skill.Source))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(skill.Body))
	for _, trigger := range skill.AutoActivation.Any {
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(trigger))
	}
	for _, group := range skill.AutoActivation.AllAny {
		for _, trigger := range group {
			_, _ = h.Write([]byte{0})
			_, _ = h.Write([]byte(trigger))
		}
	}
	sum := h.Sum(nil)
	return fmt.Sprintf("%x", sum[:8])
}

func validRuntimeSkillProposalID(id string) bool {
	if len(id) != 16 {
		return false
	}
	for _, r := range id {
		if ('0' <= r && r <= '9') || ('a' <= r && r <= 'f') {
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
