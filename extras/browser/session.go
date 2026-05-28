package browser

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/launcher/flags"
	"github.com/go-rod/rod/lib/proto"
)

// SessionConfig tunes a Session at construction time. Zero values pick
// safe defaults: headless Chromium, ephemeral profile, no sandbox
// override (so it works inside Docker without SYS_ADMIN), 1280x800
// viewport.
type SessionConfig struct {
	// Headed renders Chromium with a visible window. Defaults to
	// false (headless). Set true for interactive debugging on a host
	// with a display.
	Headed bool

	// UserAgent overrides the browser's UA string. Empty leaves
	// Chromium's default. Some sites serve degraded content to
	// automated UAs — callers may inject a plain-Chrome string.
	UserAgent string

	// Viewport size in CSS pixels. Width or Height of 0 falls back to
	// 1280x800.
	ViewportWidth  int
	ViewportHeight int

	// BinaryPath overrides the discovered Chromium binary. Most
	// callers leave this empty and let rod's launcher resolve.
	BinaryPath string

	// UserDataDir holds Chromium's profile. Empty creates an ephemeral
	// per-session tmp dir that Close() removes — the right default for
	// short-lived agent sessions.
	UserDataDir string

	// ExtraArgs are appended to Chromium's command line as flag names
	// (e.g. "disable-gpu"). Use sparingly.
	ExtraArgs []string

	// NoSandbox forces --no-sandbox. Required inside Docker without
	// SYS_ADMIN. Defaults to off; flip on for containerized callers.
	NoSandbox bool

	// DisableStealth turns off the go-rod/stealth bypass script that
	// masks `navigator.webdriver` and other automation tells.
	// Defaults to false (stealth on) — Cloudflare-fronted sites
	// otherwise serve a challenge instead of the real page.
	DisableStealth bool

	// DisableLaunchAntiDetect turns off the Chromium launch flags
	// (--disable-blink-features=AutomationControlled etc.) that
	// reduce the headless fingerprint at launch time. Defaults to
	// false (anti-detect on). Operators studying raw headless
	// behavior can opt out.
	DisableLaunchAntiDetect bool

	// Intercept governs request blocking (private networks, resource
	// types, domain list) and optional response caching. Zero value
	// applies the documented defaults: block private/internal network
	// destinations, block image/font/media + a starter tracker domain
	// list, no cache.
	Intercept InterceptConfig

	// WorkspaceDir, when non-empty, restricts file-writing browser
	// tools (currently just browser_screenshot's save_path) to paths
	// inside this directory. Relative save_path values join onto it;
	// absolute paths must fall within. Mirrors the same workspace
	// sandboxing the affent builtin file tools apply.
	//
	// Empty string disables enforcement and accepts any save_path —
	// the right choice for callers that already gate the tool set
	// some other way (a sandboxed container, a chroot, an internal
	// allowlist). affentserve always sets this to the session's
	// per-session workspace.
	WorkspaceDir string
}

func (c SessionConfig) viewport() (int, int) {
	w, h := c.ViewportWidth, c.ViewportHeight
	if w <= 0 {
		w = 1280
	}
	if h <= 0 {
		h = 800
	}
	return w, h
}

// Session is one isolated browser instance + its active page. Owns the
// snapshot ref store: ref ids map to data-affent-ref attribute selectors
// stamped on elements during the most recent Snapshot(). Older
// snapshots' refs become stale and click/type against them fail
// explicitly rather than silently targeting a different element.
type Session struct {
	cfg      SessionConfig
	launcher *launcher.Launcher
	browser  *rod.Browser
	page     *rod.Page

	mu sync.Mutex
	// refsMu guards snapshotID. We don't keep a Go-side ref→element
	// map because the JS side stamps data-affent-ref="N" on each
	// element; Go-side lookups use a shadow-piercing JS query for
	// data-affent-ref="N". Each new Snapshot() clears the attribute
	// on prior elements before stamping, so stale refs miss their
	// selector.
	refsMu     sync.Mutex
	snapshotID int64

	closedCount atomic.Int32
	tmpDirs     []string

	// hijackRouter and interceptStats are non-nil when the session
	// installed a request interceptor (the default). Close() stops
	// the router so its background goroutine exits.
	hijackRouter   *rod.HijackRouter
	interceptStats InterceptStats
	network        *NetworkEvidenceLog
}

// InterceptStats returns a snapshot of the session's request
// interceptor counters. Useful for benchmark logs / debug.
func (s *Session) InterceptStats() (blockedType, blockedDomain, domainRelaxations, cacheHit, cacheMiss, networkFetch int64) {
	return s.interceptStats.BlockedByType.Load(),
		s.interceptStats.BlockedByDomain.Load(),
		s.interceptStats.DomainRelaxations.Load(),
		s.interceptStats.CacheHit.Load(),
		s.interceptStats.CacheMiss.Load(),
		s.interceptStats.NetworkFetch.Load()
}

// NewSession boots a fresh browser and an about:blank page.
func NewSession(cfg SessionConfig) (*Session, error) {
	s := &Session{cfg: cfg, network: NewNetworkEvidenceLog()}

	l := launcher.New()
	bin := chromiumBinaryPath(cfg.BinaryPath)
	if bin != "" {
		l = l.Bin(bin)
	}
	l = l.Headless(!cfg.Headed)
	if cfg.NoSandbox {
		l = l.NoSandbox(true)
	}
	if cfg.UserDataDir != "" {
		l = l.UserDataDir(cfg.UserDataDir)
	} else {
		dir, err := os.MkdirTemp("", "affent-browser-*")
		if err != nil {
			return nil, fmt.Errorf("user data dir: %w", err)
		}
		s.tmpDirs = append(s.tmpDirs, dir)
		l = l.UserDataDir(dir)
	}
	w, h := cfg.viewport()
	l = l.Set(flags.Flag("window-size"), fmt.Sprintf("%d,%d", w, h))

	// Anti-detection launch flags. The JS-side stealth patch
	// (applyStealth) masks navigator.* signals AFTER the document
	// loads; these Chromium flags close the launch-time gaps that
	// CF-class bot detection keys on, BEFORE any script runs:
	//   --disable-blink-features=AutomationControlled removes the
	//     navigator.userAgentData/webdriver-related properties
	//     Chromium normally exposes when started for automation.
	//   --disable-features=IsolateOrigins,site-per-process relaxes
	//     site isolation so CF challenge iframes share our stealth
	//     patches with the parent document.
	//   --disable-features=Translate,AutomationControlled removes a
	//     couple of headless-only feature flags that fingerprinters
	//     check.
	//   --no-first-run, --disable-infobars suppress the headless-only
	//     post-launch UI tells.
	// Empirically this combination plus the JS-side stealth patch
	// + a current Chrome UA + UserAgentMetadata gets past the
	// Cloudflare Turnstile / 'Verifying you are human' interstitial
	// on CoinGecko (validated 2026-05-20). Operators wanting raw
	// Chromium for fingerprint debugging can pass DisableStealth +
	// DisableLaunchAntiDetect to opt out.
	if !cfg.DisableLaunchAntiDetect {
		l = l.Set(flags.Flag("disable-blink-features"), "AutomationControlled")
		l = l.Set(flags.Flag("disable-features"), "IsolateOrigins,site-per-process,Translate,AutomationControlled")
		l = l.Set(flags.Flag("enable-features"), "NetworkService")
		l = l.Set(flags.Flag("no-first-run"))
		l = l.Set(flags.Flag("disable-infobars"))
	}

	for _, a := range cfg.ExtraArgs {
		l = l.Set(flags.Flag(a))
	}

	url, err := l.Launch()
	if err != nil {
		s.cleanupTmpDirs()
		return nil, browserLaunchError(err, bin)
	}
	s.launcher = l

	browser := rod.New().ControlURL(url)
	if err := browser.Connect(); err != nil {
		l.Kill()
		s.cleanupTmpDirs()
		return nil, fmt.Errorf("connect to chromium: %w", err)
	}
	s.browser = browser

	page, err := browser.Page(proto.TargetCreateTarget{URL: "about:blank"})
	if err != nil {
		_ = browser.Close()
		l.Kill()
		s.cleanupTmpDirs()
		return nil, fmt.Errorf("open initial page: %w", err)
	}
	// User-agent override. go-rod/stealth.JS does NOT touch
	// navigator.userAgent — so Chromium's default "HeadlessChrome /
	// Chrome for Testing 145.x" UA leaks past the JS-side mask
	// and trips Cloudflare-class fingerprinters that read the UA
	// header directly. Default to a current Chrome stable UA on
	// Linux so the HTTP-side fingerprint matches the JS-side mask.
	//
	// UserAgentMetadata populates the Sec-CH-UA-* client hint
	// headers and navigator.userAgentData fields that modern Chrome
	// always sends; CF and similar WAFs treat their absence as a
	// strong bot signal. The brand mix and version split mirror
	// real Chrome 132.
	ua := cfg.UserAgent
	if ua == "" {
		ua = defaultStableUA
	}
	_ = page.SetUserAgent(&proto.NetworkSetUserAgentOverride{
		UserAgent:      ua,
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
			Mobile:          false,
			Bitness:         "64",
			Wow64:           false,
		},
	})
	// Non-fatal: SetViewport occasionally returns an error on slow
	// startup; the launch flag above already constrained the window.
	_ = page.SetViewport(&proto.EmulationSetDeviceMetricsOverride{
		Width:             w,
		Height:            h,
		DeviceScaleFactor: 1,
	})
	s.page = page

	// Stealth before the interceptor — the bypass JS runs on every
	// document and includes the about:blank context; with the
	// interceptor installed it doesn't interfere.
	if !cfg.DisableStealth {
		if err := applyStealth(page); err != nil {
			_ = browser.Close()
			l.Kill()
			s.cleanupTmpDirs()
			return nil, fmt.Errorf("apply stealth: %w", err)
		}
	}

	router, err := installInterceptor(page, cfg.Intercept, &s.interceptStats)
	if err != nil {
		_ = browser.Close()
		l.Kill()
		s.cleanupTmpDirs()
		return nil, fmt.Errorf("install interceptor: %w", err)
	}
	s.hijackRouter = router

	// Cache writes happen out-of-band: Chrome fetches via its own
	// network stack (preserving the real TLS fingerprint), and a
	// Network domain observer copies successful responses into the
	// cache afterwards.
	startCacheObserver(page, cfg.Intercept.Cache, &s.interceptStats, s.network)

	return s, nil
}

// relaxDomainBlocking widens the current session's interceptor just
// enough to stop blocking tracker domains. It keeps resource-type and
// private-network guards intact. This is a recovery path for pages
// that are otherwise healthy but depend on one of the default blocked
// domains to boot their main document or JS bundle.
func (s *Session) relaxDomainBlocking() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cfg.Intercept.AllowAllDomains {
		return nil
	}
	original := s.cfg.Intercept
	if s.hijackRouter != nil {
		if err := s.hijackRouter.Stop(); err != nil {
			return err
		}
		s.hijackRouter = nil
	}
	relaxed := relaxedDomainInterceptConfig(original)
	router, err := installInterceptor(s.page, relaxed, &s.interceptStats)
	if err != nil {
		if restore, restoreErr := installInterceptor(s.page, original, &s.interceptStats); restoreErr == nil {
			s.hijackRouter = restore
		}
		return err
	}
	s.interceptStats.DomainRelaxations.Add(1)
	s.cfg.Intercept = relaxed
	s.hijackRouter = router
	return nil
}

func relaxedDomainInterceptConfig(cfg InterceptConfig) InterceptConfig {
	relaxed := cfg
	if relaxed.BlockedDomains == nil {
		relaxed.BlockedDomains = []string{}
	}
	return relaxed
}

func chromiumBinaryPath(override string) string {
	if override != "" {
		return override
	}
	for _, name := range []string{"chromium", "chromium-browser", "google-chrome", "google-chrome-stable"} {
		if path, err := exec.LookPath(name); err == nil {
			return path
		}
	}
	return ""
}

func browserLaunchError(err error, binaryPath string) error {
	if err == nil {
		return nil
	}
	msg := strings.TrimSpace(err.Error())
	var details []string
	if binaryPath != "" {
		details = append(details, "binary="+binaryPath)
	}
	if lib := chromiumMissingSharedLibrary(msg); lib != "" {
		details = append(details, "missing_shared_library="+lib)
	}
	detailLine := ""
	if len(details) > 0 {
		detailLine = "\nDetails: " + strings.Join(details, "; ")
	}
	return fmt.Errorf("launch chromium: %w%s\nFailure: kind=browser_launch_failed\nNext: install Chromium runtime dependencies for the host image, or set AFFENT_BROWSER_BINARY/SessionConfig.BinaryPath to a working Chrome/Chromium binary; then rerun the browser smoke test before trusting browser-based web evidence", err, detailLine)
}

func chromiumMissingSharedLibrary(msg string) string {
	const marker = "error while loading shared libraries:"
	_, tail, ok := strings.Cut(msg, marker)
	if !ok {
		return ""
	}
	tail = strings.TrimSpace(tail)
	if tail == "" {
		return ""
	}
	lib, _, _ := strings.Cut(tail, ":")
	return strings.TrimSpace(lib)
}

// Close releases the browser, kills Chromium, and removes ephemeral
// user-data directories. Idempotent.
func (s *Session) Close() error {
	if s.closedCount.Add(1) > 1 {
		return nil
	}
	var firstErr error
	if s.hijackRouter != nil {
		if err := s.hijackRouter.Stop(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if s.browser != nil {
		if err := s.browser.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if s.launcher != nil {
		s.launcher.Kill()
	}
	s.cleanupTmpDirs()
	return firstErr
}

func (s *Session) cleanupTmpDirs() {
	for _, d := range s.tmpDirs {
		_ = os.RemoveAll(d)
	}
	s.tmpDirs = nil
}

// Page returns the active page. Exported for tests and callers that
// need direct rod access.
func (s *Session) Page() *rod.Page { return s.page }

// withContext returns a context-bound page clone so per-call ctx
// cancellation propagates into rod's wait/eval logic.
func (s *Session) withContext(ctx context.Context) *rod.Page {
	if ctx == nil {
		return s.page
	}
	return s.page.Context(ctx)
}

// newSnapshotID bumps the snapshot counter monotonically per session.
func (s *Session) newSnapshotID() int64 {
	s.refsMu.Lock()
	defer s.refsMu.Unlock()
	s.snapshotID++
	return s.snapshotID
}

// ErrNoPage is returned when a tool runs before the session has any
// page loaded. NewSession opens about:blank so this shouldn't fire in
// practice; reserved for future code paths that close the page on
// idle.
var ErrNoPage = errors.New("browser session has no active page\nFailure: kind=no_page\nNext: call browser_navigate with the target http:// or https:// URL before using browser_snapshot, browser_find, browser_network_read, or interaction tools")

// defaultStableUA is a current Chrome stable UA on Linux x86_64.
// We override the headless "Chrome for Testing" UA with this so the
// HTTP-side fingerprint blends with real-Chrome traffic. Bump on a
// quarterly cadence — exact version doesn't need to be cutting-edge
// but should stay within the last 2 Chrome releases to avoid the
// "outdated UA" fingerprint vector.
const defaultStableUA = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/132.0.0.0 Safari/537.36"
