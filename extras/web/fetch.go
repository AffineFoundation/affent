package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	htmltomarkdown "github.com/JohannesKaufmann/html-to-markdown/v2"
	"github.com/JohannesKaufmann/html-to-markdown/v2/converter"
	agent "github.com/affinefoundation/affent/internal/agent"
	readability "github.com/go-shiori/go-readability"
)

// FetchConfig tunes WebFetchTool. Zero values pick sane defaults.
type FetchConfig struct {
	// HTTP is reused across calls. When nil FetchTool builds a client
	// with a 30s timeout AND an SSRF guard that blocks dialing to
	// private / loopback / link-local / unspecified / multicast IPs
	// (covers RFC1918, AWS-metadata at 169.254.169.254, 127.0.0.1,
	// IPv6 ULA / link-local, etc.). When non-nil, the caller owns
	// the safety story — pass in a hardened client or be sure your
	// network can't reach anything sensitive.
	HTTP *http.Client
	// MaxBytes caps the response body the tool reads. Default 2 MiB —
	// enough for most articles without letting a misconfigured server
	// stream gigabytes into memory. Pages larger than this are
	// truncated (bytes) before HTML→markdown. Values above the hard
	// cap are clamped so callers cannot accidentally disable the
	// memory guard.
	MaxBytes int64
	// MaxResultChars caps the markdown output handed back to the LLM.
	// Default 8000. Values above the hard cap are clamped. Truncated
	// output gets a "...(truncated)" marker.
	MaxResultChars int
	// UserAgent is sent on every request. Defaults to a generic
	// "affent-webfetch/0.1" — override if a target server requires
	// something specific.
	UserAgent string
	// AllowPrivateNetwork disables the default SSRF guard. Off by
	// default — a model that decides to fetch http://127.0.0.1:7777
	// (affentserve itself) or http://169.254.169.254 (cloud-metadata
	// IMDSv1) shouldn't be able to without operator opt-in. Flip on
	// only for development against local services, or when the agent
	// is running inside a network namespace that already isolates it.
	AllowPrivateNetwork bool
}

const (
	maxFetchURLBytes      = 4096
	defaultMaxBytes       = 2 * 1024 * 1024
	maxFetchBytes         = 8 * 1024 * 1024
	defaultMaxResultChars = 8000
	maxFetchResultChars   = 64 * 1024
	defaultUserAgent      = "affent-webfetch/0.1 (+https://github.com/AffineFoundation/affent)"
)

// FetchTool returns an agent.Tool that fetches a URL and returns its
// text content. HTML is converted to markdown; other text/* types
// (text/plain, application/json, …) are passed through; non-text bodies
// get a placeholder. Redirects are followed by net/http's default
// behaviour (10 hops max).
func FetchTool(cfg FetchConfig) *agent.Tool {
	cfg = normalizeFetchConfig(cfg)
	schema := json.RawMessage(fmt.Sprintf(`{
        "type": "object",
        "required": ["url"],
        "properties": {
            "url": {"type": "string", "minLength": 1, "maxLength": %d, "description": "The fully-qualified URL to fetch (http:// or https://)."}
        }
    }`, maxFetchURLBytes))

	return &agent.Tool{
		Name: "web_fetch",
		Description: "Fetch a URL and return its text content. HTML is " +
			"converted to compact markdown; text/plain, application/json, " +
			"and similar text types are returned as-is. Output is capped " +
			"and truncated with a marker. Redirects are followed.",
		Schema: schema,
		Execute: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var args struct {
				URL string `json:"url"`
			}
			if err := json.Unmarshal(raw, &args); err != nil {
				return "", fmt.Errorf("decode args: %w", err)
			}
			args.URL = strings.TrimSpace(args.URL)
			if args.URL == "" {
				return "", errors.New("url is required")
			}
			if len(args.URL) > maxFetchURLBytes {
				return "", fmt.Errorf("url is %d bytes; web_fetch supports URLs up to %d bytes", len(args.URL), maxFetchURLBytes)
			}
			if !strings.HasPrefix(args.URL, "http://") && !strings.HasPrefix(args.URL, "https://") {
				return "", fmt.Errorf("url must start with http:// or https:// (got %q)", args.URL)
			}
			return fetch(ctx, cfg, args.URL)
		},
	}
}

func normalizeFetchConfig(cfg FetchConfig) FetchConfig {
	if cfg.HTTP == nil {
		cfg.HTTP = newGuardedClient(cfg.AllowPrivateNetwork)
	}
	if cfg.MaxBytes <= 0 {
		cfg.MaxBytes = defaultMaxBytes
	} else if cfg.MaxBytes > maxFetchBytes {
		cfg.MaxBytes = maxFetchBytes
	}
	if cfg.MaxResultChars <= 0 {
		cfg.MaxResultChars = defaultMaxResultChars
	} else if cfg.MaxResultChars > maxFetchResultChars {
		cfg.MaxResultChars = maxFetchResultChars
	}
	if cfg.UserAgent == "" {
		cfg.UserAgent = defaultUserAgent
	}
	return cfg
}

func fetch(ctx context.Context, cfg FetchConfig, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", cfg.UserAgent)
	// Hint we want text-ish content. Servers that honor q= ranking will
	// prefer HTML over JSON when both are available.
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,text/plain;q=0.8,*/*;q=0.5")

	resp, err := cfg.HTTP.Do(req)
	if err != nil {
		return "", fmt.Errorf("http get: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		// Read a little so the error is informative.
		preview, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("http %d %s: %s",
			resp.StatusCode, resp.Status, strings.TrimSpace(string(preview)))
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, cfg.MaxBytes+1))
	if err != nil {
		return "", fmt.Errorf("read body: %w", err)
	}
	bodyTruncated := int64(len(body)) > cfg.MaxBytes
	if bodyTruncated {
		body = body[:cfg.MaxBytes]
	}

	ct := resp.Header.Get("Content-Type")
	out := renderBody(body, ct, resp.Request.URL.String())

	if len(out) > cfg.MaxResultChars {
		// Snap back to a UTF-8 rune boundary so accented Latin, CJK,
		// or emoji content doesn't get cut mid-rune and land in the
		// model's context as invalid UTF-8.
		cut := cfg.MaxResultChars
		for cut > 0 && !utf8.RuneStart(out[cut]) {
			cut--
		}
		out = out[:cut] + "\n\n...(truncated)"
	}
	if bodyTruncated {
		out = strings.TrimSpace(out) + "\n\n...(response body truncated)"
	}
	return out, nil
}

func renderBody(body []byte, contentType, finalURL string) string {
	ct := strings.ToLower(strings.TrimSpace(contentType))
	switch {
	case strings.HasPrefix(ct, "text/html"), strings.HasPrefix(ct, "application/xhtml+xml"):
		// Standard reader pipeline: Readability extracts the article's
		// main content (drops nav/header/footer/sidebar/ads), then
		// html-to-markdown converts the cleaned HTML. Both libraries
		// are the de-facto Go choices:
		//   - go-shiori/go-readability — Mozilla Readability port
		//   - JohannesKaufmann/html-to-markdown — commonmark-spec converter
		// Falling back to direct conversion when readability can't
		// identify a main article (e.g. on a homepage, listing, or
		// non-article page) so the model still gets something useful.
		htmlText := string(body)
		var pageURL *url.URL
		if u, err := url.Parse(finalURL); err == nil {
			pageURL = u
		}
		articleHTML := htmlText
		if art, err := readability.FromReader(bytes.NewReader(body), pageURL); err == nil && art.Content != "" {
			articleHTML = art.Content
		}

		var opts []converter.ConvertOptionFunc
		if domain := domainOf(finalURL); domain != "" {
			opts = append(opts, converter.WithDomain(domain))
		}
		md, err := htmltomarkdown.ConvertString(articleHTML, opts...)
		if err != nil {
			return htmlText
		}
		return md
	case strings.HasPrefix(ct, "text/"),
		strings.HasPrefix(ct, "application/json"),
		strings.HasPrefix(ct, "application/xml"),
		strings.HasPrefix(ct, "application/javascript"),
		strings.HasPrefix(ct, "application/yaml"):
		return string(body)
	default:
		return fmt.Sprintf("[non-text response: Content-Type=%q, %d bytes]", contentType, len(body))
	}
}

// newGuardedClient builds an http.Client whose Transport refuses to
// dial to any IP we don't want a model-driven URL to reach. The check
// runs in net.Dialer.Control, AFTER DNS resolution but BEFORE the TCP
// SYN, so we catch the actual IP the OS is about to connect to —
// even when the hostname has multiple A/AAAA records or the resolver
// returns a different answer than a separate "preflight" lookup
// would (defeats trivial DNS-rebinding attacks on a single connect).
// Redirects re-enter the same dialer per hop, so a public→private
// hop is blocked too.
//
// When allowPrivate is true the control hook is omitted entirely so
// dev / local-service fetching works as expected.
func newGuardedClient(allowPrivate bool) *http.Client {
	if allowPrivate {
		return &http.Client{Timeout: 30 * time.Second}
	}
	dialer := &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
		Control: func(network, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				host = address
			}
			ip := net.ParseIP(host)
			if ip == nil {
				// Shouldn't happen — by the time Control fires, the
				// Dialer has resolved to a numeric address. Be
				// defensive: refuse rather than connect blind.
				return fmt.Errorf("ssrf-guard: unparseable dial target %q", address)
			}
			if isBlockedIP(ip) {
				return fmt.Errorf("ssrf-guard: refusing to dial %s (private / loopback / link-local / unspecified / multicast)", ip)
			}
			return nil
		},
	}
	return &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			DialContext:           dialer.DialContext,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 30 * time.Second,
			// Match http.DefaultTransport's idle-pool bounds. A
			// zero-value Transport keeps idle conns forever (no
			// IdleConnTimeout) and the pool is effectively unbounded;
			// a long-running affentserve fetching many distinct
			// hosts would accumulate sockets until something killed
			// the process. 90 s / 100 conns are net/http's own
			// defaults.
			MaxIdleConns:    100,
			IdleConnTimeout: 90 * time.Second,
			// HTTP/2 needs an explicit opt-in once any field on the
			// Transport is set (Go's transport stops auto-upgrading
			// in that case). Without this, every fetch is HTTP/1.1,
			// missing out on multiplexing and ALPN for hosts that
			// only optimize the h2 path.
			ForceAttemptHTTP2: true,
		},
	}
}

// isBlockedIP collapses Go's net.IP category methods plus IPv4
// broadcast into one check. IsPrivate covers RFC1918 + IPv6 ULA;
// IsLoopback / IsLinkLocalUnicast / IsUnspecified / IsMulticast
// cover the rest of the families that a model has no business
// reaching through a fetch tool.
func isBlockedIP(ip net.IP) bool {
	if ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() || ip.IsMulticast() {
		return true
	}
	// 255.255.255.255 — not covered by any Is* method but obviously
	// not a real fetch target.
	if v4 := ip.To4(); v4 != nil && v4[0] == 255 && v4[1] == 255 && v4[2] == 255 && v4[3] == 255 {
		return true
	}
	return false
}

// domainOf extracts "scheme://host" from a URL, used to resolve
// relative links/images against the page's origin.
func domainOf(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}
