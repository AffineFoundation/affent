package agent

import "strings"

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
//  1. If Match is non-nil, it owns the decision (used for trained
//     classifiers or multi-signal rules).
//  2. Otherwise, the skill fires when any string in Triggers is a
//     case-insensitive substring of the user message.
//
// Triggers should stay as generic shape signals ("http://", "page",
// "测试") not specific site / project / company names — domain-
// specific tokens leak eval state into the router and bias unrelated
// traffic. The router itself does not enforce that; reviewers do.
type Skill struct {
	// Name is the skill's identifier. Surfaced in the Body's header
	// (e.g. "AFFENT ACTIVE SKILL: web_snapshot_fact_extraction") so
	// trace consumers and operators can grep / filter.
	Name string
	// Body is the system-prompt block injected verbatim when the
	// skill activates. Multi-line markdown is fine; the registry
	// joins multiple active skills with a blank line.
	Body string
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

// SkillRegistry is an ordered set of skills. The router iterates in
// registration order so multiple active skills compose deterministically
// (matters for prompt-shape stability across reproducible eval runs).
type SkillRegistry struct {
	skills []Skill
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

// webSnapshotTriggers are generic shape signals: the user mentioned a
// URL, a browser, or "this page". A specific site name leaking into
// this list (an earlier draft had "taostats") would make the codebase
// carry domain-specific eval state and bias the router on unrelated
// traffic without buying anything — a URL or "page" / "网页" match
// already fires for the cases that motivated the trigger.
var webSnapshotTriggers = []string{
	"http://",
	"https://",
	"browser",
	"web page",
	"website",
	"网页",
	"页面",
	"浏览器",
	"访问",
}

// codingRepairTriggers are vocabulary signals that the user is asking
// for a code edit / test fix. Same rule: stay generic — no project /
// language-version / framework names, since those would over-trigger
// on planning conversations.
var codingRepairTriggers = []string{
	"fix",
	"bug",
	"failing test",
	"test fails",
	"implement",
	"refactor",
	"code",
	"compile",
	"go test",
	"pytest",
	"npm test",
	"修复",
	"失败",
	"测试",
	"实现",
	"代码",
	"编译",
}

const webSnapshotSkillBody = `AFFENT ACTIVE SKILL: web_snapshot_fact_extraction

Use this procedure for rendered web-page fact extraction:
- Keep the scope narrow. If the user asks for current-page visible facts, extract only the current page/snapshot and do not click tabs, paginate, or broaden across the site.
- Prefer browser_navigate with wait_until=networkidle, then read the returned snapshot. Use browser_wait/browser_snapshot/one small scroll only when the requested fact is missing.
- Do not use shell/curl/python to fetch the same web page when the user asked for browser-based access or when browser_* tools are available.
- Treat page titles, labels, and values separately. Do not label a nearby number as a metric unless the snapshot gives enough context.
- When a page exposes multiple price-like values, report them separately with their visible source (for example: title price vs body/top-bar USD price). Do not replace a small title decimal with a nearby large USD value, and do not infer which one is the asset price unless the label says so.
- If the user asks for "all information" on a dynamic site, report the visible overview first and say which extra tabs/pages require separate bounded inspection instead of trying to audit the whole site in one run.`

const codingRepairSkillBody = `AFFENT ACTIVE SKILL: coding_repair_workflow

Use this procedure for code changes:
- Reproduce first with the narrowest relevant test or command before editing, unless the user only asked for analysis.
- Inspect the failing code and the failing test/spec. Change implementation files by default; do not edit tests unless the user asks or the test is clearly wrong.
- Keep the patch small and coherent. Prefer surgical edit_file changes over broad rewrites.
- If a build/test tool is not on PATH, do bounded discovery: command -v, repo-local toolchains such as ./.tmp/toolchains, and common user-local paths such as $HOME/.local. Do not run broad filesystem searches like find /.
- After editing, run the same failing command again. If the language has a standard formatter and it is available, run it before the final test.
- In the final answer, state the files changed and the exact verification command/result.`

// WebSnapshotSkill is the built-in skill for rendered web-page fact
// extraction. Exported so operators composing a custom registry can
// keep it without copy-pasting the body.
func WebSnapshotSkill() Skill {
	return Skill{
		Name:     "web_snapshot_fact_extraction",
		Body:     webSnapshotSkillBody,
		Triggers: webSnapshotTriggers,
	}
}

// CodingRepairSkill is the built-in skill for code-edit / test-fix
// workflows. Same rationale as WebSnapshotSkill — exported so a
// custom registry can include it selectively.
func CodingRepairSkill() Skill {
	return Skill{
		Name:     "coding_repair_workflow",
		Body:     codingRepairSkillBody,
		Triggers: codingRepairTriggers,
	}
}

// DefaultSkillRegistry returns the registry affentctl / affentserve
// install when the operator doesn't override SkillProvider. Adding a
// new builtin skill is two lines: a Skill struct above and one
// Register call here. No router code changes.
func DefaultSkillRegistry() *SkillRegistry {
	r := &SkillRegistry{}
	r.Register(WebSnapshotSkill())
	r.Register(CodingRepairSkill())
	return r
}

// builtinSkillProviderRegistry is the package-level default backing
// BuiltinSkillProvider. Constructed once so the per-turn lookup is
// just a slice iteration, not a fresh registry build.
var builtinSkillProviderRegistry = DefaultSkillRegistry()

// BuiltinSkillProvider is the default lightweight skill router. It
// delegates to DefaultSkillRegistry, which uses high-precision
// substring triggers over broad semantic guesses because injecting
// irrelevant instructions hurts smaller models more than it helps.
//
// Custom deployments can build their own SkillRegistry (or any
// SkillProvider function) and wire it via Loop.SkillProvider without
// touching this file.
func BuiltinSkillProvider(userText string) string {
	return builtinSkillProviderRegistry.Provide(userText)
}
