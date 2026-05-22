// probe_cmd is a standalone diagnostic that tries every client-side
// trick to access a CF-protected URL. It exists outside the affent
// session machinery so we can vary one parameter at a time and see
// exactly which technique (if any) gets past the challenge.
//
// Not for production. Build + run from extras/browser:
//
//	go run ./probe_cmd https://www.coingecko.com/en/coins/ethereum
package main

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/launcher/flags"
	"github.com/go-rod/rod/lib/proto"
	"github.com/go-rod/stealth"
)

const chromeUA = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/132.0.0.0 Safari/537.36"

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: probe_cmd <url> [--no-stealth-flags] [--cookie-warmup <warmup_url>]")
		os.Exit(2)
	}
	target := os.Args[1]
	var warmupURL string
	withStealthFlags := true
	for i := 2; i < len(os.Args); i++ {
		switch os.Args[i] {
		case "--no-stealth-flags":
			withStealthFlags = false
		case "--cookie-warmup":
			if i+1 >= len(os.Args) {
				fmt.Fprintln(os.Stderr, "--cookie-warmup needs a URL")
				os.Exit(2)
			}
			warmupURL = os.Args[i+1]
			i++
		}
	}

	bin := os.Getenv("AFFENT_BROWSER_BINARY")
	if bin == "" {
		bin = "/home/claudeuser/.cache/ms-playwright/chromium-1208/chrome-linux64/chrome"
	}

	l := launcher.New().Bin(bin).Headless(true).NoSandbox(true)
	// Anti-detection chromium flags. These plus the JS-level stealth
	// patch close the most common headless-fingerprint gaps:
	//   AutomationControlled blink feature — hide the "automation"
	//     bit from `navigator.userAgentData` and a handful of WebDriver
	//     APIs that puppeteer-extra-plugin-stealth also patches.
	//   --disable-features=IsolateOrigins,site-per-process — relax
	//     site isolation so cross-origin iframes (CF challenge
	//     iframes) run in the same process and benefit from our
	//     stealth patch.
	//   --enable-features=NetworkService — match real Chrome's
	//     networking architecture (the default already, but explicit
	//     to defend against a future regression).
	if withStealthFlags {
		l = l.Set(flags.Flag("disable-blink-features"), "AutomationControlled")
		l = l.Set(flags.Flag("disable-features"), "IsolateOrigins,site-per-process,Translate,AutomationControlled")
		l = l.Set(flags.Flag("enable-features"), "NetworkService")
		l = l.Set(flags.Flag("disable-infobars"))
		l = l.Set(flags.Flag("no-first-run"))
	}
	l = l.Set(flags.Flag("window-size"), "1280,800")

	url, err := l.Launch()
	if err != nil {
		fail("launch: %v", err)
	}
	defer l.Kill()

	br := rod.New().ControlURL(url)
	if err := br.Connect(); err != nil {
		fail("connect: %v", err)
	}
	defer br.Close()

	page, err := br.Page(proto.TargetCreateTarget{URL: "about:blank"})
	if err != nil {
		fail("page: %v", err)
	}

	// JS-level stealth patch.
	if _, err := page.EvalOnNewDocument(stealth.JS); err != nil {
		fail("stealth: %v", err)
	}

	// HTTP-level UA + Accept-Language + sec-ch-ua via UserAgentMetadata.
	mob := false
	if err := page.SetUserAgent(&proto.NetworkSetUserAgentOverride{
		UserAgent:      chromeUA,
		AcceptLanguage: "en-US,en;q=0.9",
		Platform:       "Linux",
		UserAgentMetadata: &proto.EmulationUserAgentMetadata{
			Brands: []*proto.EmulationUserAgentBrandVersion{
				{Brand: "Not_A Brand", Version: "8"},
				{Brand: "Chromium", Version: "132"},
				{Brand: "Google Chrome", Version: "132"},
			},
			FullVersionList: []*proto.EmulationUserAgentBrandVersion{
				{Brand: "Not_A Brand", Version: "8.0.0.0"},
				{Brand: "Chromium", Version: "132.0.0.0"},
				{Brand: "Google Chrome", Version: "132.0.0.0"},
			},
			FullVersion:     "132.0.0.0",
			Platform:        "Linux",
			PlatformVersion: "6.5.0",
			Architecture:    "x86",
			Model:           "",
			Mobile:          mob,
			Bitness:         "64",
			Wow64:           false,
		},
	}); err != nil {
		fail("set ua: %v", err)
	}

	// Optional cookie warmup. Visit the warmup URL first so CF gets a
	// chance to set the __cf_bm / cf_clearance cookies that real users
	// pick up implicitly. Then navigate to the target.
	if warmupURL != "" {
		fmt.Printf("== warmup: %s ==\n", warmupURL)
		if err := page.Navigate(warmupURL); err != nil {
			fmt.Printf("warmup nav err: %v\n", err)
		} else {
			_ = page.WaitLoad()
			time.Sleep(4 * time.Second) // give CF time to challenge / set cookies
			report(page, "(warmup)")
		}
	}

	fmt.Printf("== target: %s ==\n", target)
	if err := page.Navigate(target); err != nil {
		fail("navigate: %v", err)
	}
	_ = page.WaitLoad()
	// Wait extra for CF JS / challenge auto-resolve. Some CF light
	// challenges resolve in 4-8s after document load.
	time.Sleep(8 * time.Second)

	report(page, "")

	// Try a re-render: scroll a bit, wait, re-read. CF challenges
	// sometimes replace the document when their JS finishes.
	page.Mouse.MustMoveTo(640, 400)
	_, _ = page.Eval("() => window.scrollBy(0, 400)")
	time.Sleep(3 * time.Second)
	report(page, "(after scroll + 3s)")
}

func report(p *rod.Page, label string) {
	info, _ := p.Info()
	title := ""
	if info != nil {
		title = info.Title
	}
	body, _ := p.Eval("() => document.body ? document.body.innerText.slice(0, 600) : ''")
	bodyStr := ""
	if body != nil && body.Value.Val() != nil {
		bodyStr, _ = body.Value.Val().(string)
	}
	url := ""
	if info != nil {
		url = info.URL
	}
	fmt.Printf("  %s\n", label)
	fmt.Printf("  URL:   %s\n", url)
	fmt.Printf("  TITLE: %s\n", title)
	markers := []string{"Just a moment", "Verifying you are human", "cf-please-wait", "Sorry, you have been blocked", "checking your browser"}
	hits := []string{}
	for _, m := range markers {
		if strings.Contains(bodyStr, m) {
			hits = append(hits, m)
		}
	}
	if len(hits) > 0 {
		fmt.Printf("  CHALLENGE MARKERS HIT: %v\n", hits)
	} else {
		fmt.Printf("  no challenge markers detected\n")
	}
	fmt.Printf("  body[:600]: %s\n", strings.ReplaceAll(bodyStr, "\n", " | "))
	// Also report any cookies set on the cookie jar so we can see if
	// CF cleared us.
	cookies, _ := p.Cookies(nil)
	cfHits := []string{}
	for _, c := range cookies {
		if strings.HasPrefix(c.Name, "cf_") || strings.HasPrefix(c.Name, "__cf") {
			cfHits = append(cfHits, c.Name+"="+truncate(c.Value, 24))
		}
	}
	if len(cfHits) > 0 {
		fmt.Printf("  CF cookies: %v\n", cfHits)
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func fail(msg string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, msg+"\n", args...)
	os.Exit(1)
}

// silence unused import warning for net/http in case we add a non-CDP
// path probe later.
var _ = http.StatusOK
