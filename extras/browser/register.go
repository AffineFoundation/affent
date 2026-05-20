package browser

import "github.com/affinefoundation/affent"

// Options bundles optional configuration for RegisterAll.
type Options struct {
	// IncludeScreenshot registers browser_screenshot. Off by default —
	// text LLMs don't benefit from it, and the base64 payload bloats
	// tool result events.
	IncludeScreenshot bool
}

// RegisterAll adds the standard browser tool family to reg:
//   - browser_navigate
//   - browser_back
//   - browser_wait
//   - browser_snapshot
//   - browser_click
//   - browser_type
//   - browser_scroll
//
// browser_screenshot is opt-in via Options.IncludeScreenshot.
//
// One Session is bound to one Loop. If you need multiple concurrent
// agent sessions in the same process, build one Session per Loop and
// call RegisterAll once per session-scoped Registry.
func RegisterAll(reg *affent.Registry, s *Session, opts Options) {
	reg.Add(NavigateTool(s))
	reg.Add(BackTool(s))
	reg.Add(WaitTool(s))
	reg.Add(SnapshotTool(s))
	reg.Add(ClickTool(s))
	reg.Add(TypeTool(s))
	reg.Add(ScrollTool(s))
	if opts.IncludeScreenshot {
		reg.Add(ScreenshotTool(s))
	}
}
