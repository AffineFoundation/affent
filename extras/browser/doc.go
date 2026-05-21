// Package browser is an affent extras package providing real
// browser-driven tools: navigate, click, type, scroll, snapshot,
// screenshot. Backed by Chromium via go-rod (pure-Go CDP client).
//
// # Design notes
//
// The tool surface follows the mainstream "snapshot + ref" pattern
// used by Playwright MCP and Browser Use: each snapshot enumerates
// interactive elements with stable integer ref ids, and action tools
// (click, type) take a ref rather than a CSS selector. Selectors are
// brittle for LLMs to author; refs let the model reason about the
// page it just saw without inventing query syntax.
//
// # Why a separate sub-module
//
// Real browser automation drags in a CDP client and an actual
// Chromium runtime. The root affent module keeps a tiny dep graph;
// callers who don't need browser access don't pay for those deps.
// Use this package only when you've decided your agent should drive
// a browser.
//
// Usage
//
//	import (
//	    agent "github.com/affinefoundation/affent/internal/agent"
//	    affentbrowser "github.com/affinefoundation/affent/extras/browser"
//	)
//
//	sess, err := affentbrowser.NewSession(affentbrowser.SessionConfig{
//	    Headless: true,
//	})
//	if err != nil { log.Fatal(err) }
//	defer sess.Close()
//
//	reg := agent.NewRegistry()
//	agent.RegisterBuiltins(reg, deps)
//	affentbrowser.RegisterAll(reg, sess, affentbrowser.Options{})
//
// One Session corresponds to one isolated Chromium profile. Most
// embedders create one Session per agent Loop (i.e. per conversation),
// and close it when the conversation ends.
package browser
