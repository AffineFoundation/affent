package agent

import "strings"

// SkillProvider returns a concise, task-relevant system block for the
// current user turn. It is deliberately a tiny interface: deployments
// can later back it with workspace skills, MCP-provided skills, or a
// trained router without changing Loop.
type SkillProvider func(userText string) string

const webSnapshotSkill = `AFFENT ACTIVE SKILL: web_snapshot_fact_extraction

Use this procedure for rendered web-page fact extraction:
- Keep the scope narrow. If the user asks for current-page visible facts, extract only the current page/snapshot and do not click tabs, paginate, or broaden across the site.
- Prefer browser_navigate with wait_until=networkidle, then read the returned snapshot. Use browser_wait/browser_snapshot/one small scroll only when the requested fact is missing.
- Do not use shell/curl/python to fetch the same web page when the user asked for browser-based access or when browser_* tools are available.
- Treat page titles, labels, and values separately. Do not label a nearby number as a metric unless the snapshot gives enough context.
- When a page exposes multiple price-like values, report them separately with their visible source (for example: title price vs body/top-bar USD price). Do not replace a small title decimal with a nearby large USD value, and do not infer which one is the asset price unless the label says so.
- If the user asks for "all information" on a dynamic site, report the visible overview first and say which extra tabs/pages require separate bounded inspection instead of trying to audit the whole site in one run.`

// BuiltinSkillProvider is the default lightweight skill router. It
// favors high-precision triggers over broad semantic guesses because
// injecting irrelevant instructions hurts smaller models more than it
// helps.
func BuiltinSkillProvider(userText string) string {
	text := strings.ToLower(userText)
	if wantsWebSnapshotSkill(text) {
		return webSnapshotSkill
	}
	return ""
}

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

func wantsWebSnapshotSkill(lowerUserText string) bool {
	for _, trigger := range webSnapshotTriggers {
		if strings.Contains(lowerUserText, trigger) {
			return true
		}
	}
	return false
}
