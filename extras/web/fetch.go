package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	htmltomarkdown "github.com/JohannesKaufmann/html-to-markdown/v2"
	"github.com/JohannesKaufmann/html-to-markdown/v2/converter"
	agent "github.com/affinefoundation/affent/internal/agent"
	readability "github.com/go-shiori/go-readability"
)

// FetchConfig tunes WebFetchTool. Zero values pick sane defaults.
type FetchConfig struct {
	// HTTP is reused across calls. Defaults to a client with a
	// 30s timeout if nil.
	HTTP *http.Client
	// MaxBytes caps the response body the tool reads. Default 2 MiB —
	// enough for most articles without letting a misconfigured server
	// stream gigabytes into memory. Pages larger than this are
	// truncated (bytes) before HTML→markdown.
	MaxBytes int64
	// MaxResultChars caps the markdown output handed back to the LLM.
	// Default 8000. Truncated output gets a "...(truncated)" marker.
	MaxResultChars int
	// UserAgent is sent on every request. Defaults to a generic
	// "affent-webfetch/0.1" — override if a target server requires
	// something specific.
	UserAgent string
}

const (
	defaultMaxBytes       = 2 * 1024 * 1024
	defaultMaxResultChars = 8000
	defaultUserAgent      = "affent-webfetch/0.1 (+https://github.com/AffineFoundation/affent)"
)

// FetchTool returns an agent.Tool that fetches a URL and returns its
// text content. HTML is converted to markdown; other text/* types
// (text/plain, application/json, …) are passed through; non-text bodies
// get a placeholder. Redirects are followed by net/http's default
// behaviour (10 hops max).
func FetchTool(cfg FetchConfig) *agent.Tool {
	if cfg.HTTP == nil {
		cfg.HTTP = &http.Client{Timeout: 30 * time.Second}
	}
	if cfg.MaxBytes <= 0 {
		cfg.MaxBytes = defaultMaxBytes
	}
	if cfg.MaxResultChars <= 0 {
		cfg.MaxResultChars = defaultMaxResultChars
	}
	if cfg.UserAgent == "" {
		cfg.UserAgent = defaultUserAgent
	}

	schema := json.RawMessage(`{
        "type": "object",
        "required": ["url"],
        "properties": {
            "url": {"type": "string", "description": "The fully-qualified URL to fetch (http:// or https://)."}
        }
    }`)

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
			if args.URL == "" {
				return "", errors.New("url is required")
			}
			if !strings.HasPrefix(args.URL, "http://") && !strings.HasPrefix(args.URL, "https://") {
				return "", fmt.Errorf("url must start with http:// or https:// (got %q)", args.URL)
			}
			return fetch(ctx, cfg, args.URL)
		},
	}
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

	body, err := io.ReadAll(io.LimitReader(resp.Body, cfg.MaxBytes))
	if err != nil {
		return "", fmt.Errorf("read body: %w", err)
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

// domainOf extracts "scheme://host" from a URL, used to resolve
// relative links/images against the page's origin.
func domainOf(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}
