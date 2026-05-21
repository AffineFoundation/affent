// Package web is an affent extras package providing web_fetch and
// web_search tools.
//
// It lives as a separate Go module under affent/extras/web so that
// callers who don't need web access (sandboxed eval, training rigs
// without network, etc.) don't pay for the html parser or any search
// backend dependencies in their go.sum.
//
// Usage:
//
//	import (
//	    "github.com/affinefoundation/affent"
//	    affentweb "github.com/affinefoundation/affent/extras/web"
//	)
//
//	reg := affent.NewRegistry()
//	affent.RegisterBuiltins(reg, deps)
//
//	// Just web_fetch (no search backend needed):
//	affentweb.RegisterFetch(reg, affentweb.FetchConfig{})
//
//	// Both tools, default Tavily backend (reads TAVILY_API_KEY):
//	if err := affentweb.RegisterAll(reg, affentweb.Options{}); err != nil {
//	    log.Fatal(err)
//	}
//
//	// Custom search backend:
//	affentweb.RegisterFetch(reg, affentweb.FetchConfig{})
//	tool, _ := affentweb.SearchTool(affentweb.SearchConfig{Provider: myProvider})
//	reg.Add(tool)
package web

import "github.com/affinefoundation/affent"

// Options bundles the optional configuration for RegisterAll.
type Options struct {
	Fetch FetchConfig
	// SearchProvider overrides the default Tavily provider when set.
	// Leave nil to fall back to TAVILY_API_KEY.
	SearchProvider SearchProvider
	// MaxSearchResults caps the per-query result count. Default 8.
	MaxSearchResults int
	// SkipSearch lets callers register web_fetch only without
	// requiring a search backend.
	SkipSearch bool
}

// RegisterFetch adds the web_fetch tool to reg. Always succeeds (no
// external dependencies).
func RegisterFetch(reg *affent.Registry, cfg FetchConfig) {
	reg.Add(FetchTool(cfg))
}

// RegisterAll adds web_fetch and (unless SkipSearch is true) web_search
// to reg. Returns an error if web_search is requested but no provider
// is available — the caller can recover by setting SkipSearch and
// calling RegisterFetch directly.
//
// On failure, every tool this call added is removed from reg before
// returning so the caller doesn't end up with a half-registered
// web_fetch pointing into a setup that's about to be torn down.
func RegisterAll(reg *affent.Registry, opts Options) error {
	RegisterFetch(reg, opts.Fetch)
	if opts.SkipSearch {
		return nil
	}
	provider := opts.SearchProvider
	if provider == nil {
		p, err := NewTavilyProvider()
		if err != nil {
			reg.Remove("web_fetch")
			return err
		}
		provider = p
	}
	tool, err := SearchTool(SearchConfig{
		Provider:   provider,
		MaxResults: opts.MaxSearchResults,
	})
	if err != nil {
		reg.Remove("web_fetch")
		return err
	}
	reg.Add(tool)
	return nil
}
