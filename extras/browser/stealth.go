package browser

import (
	"github.com/go-rod/rod"
	"github.com/go-rod/stealth"
)

// applyStealth installs go-rod/stealth's webdriver-detection bypass
// on the given page. The script runs in the page's main world BEFORE
// any document JS, masking the telltale `navigator.webdriver`,
// `navigator.plugins`, `chrome.runtime`, WebGL vendor, and a dozen
// other signals automated browsers expose.
//
// This is enabled by default in NewSession. The Cloudflare bot
// detection (and many of the simpler WAF rules) keys on these
// signals — without stealth, a fresh navigation to a CF-fronted
// origin often serves a challenge page instead of the real content.
// Higher-tier CF protections (managed challenges, JS challenges
// requiring residential IPs) still require proxy infrastructure.
func applyStealth(page *rod.Page) error {
	_, err := page.EvalOnNewDocument(stealth.JS)
	return err
}
